// Package savefile reads and writes Wind Waker's save as Dolphin keeps it
// on a memory card: the card file "gczelda" (m_Do_MemCard.cpp:116,134),
// either exported as a GCI file (the file's 0x40-byte card directory
// entry, then its 0x2000-byte blocks) or inside a raw card image. It is
// what the offline save-diff and save-merge tools work on (§10 question 1,
// §11 item 20); no live co-op path writes a card file (§5.1).
//
// The file is 12 blocks, the 0x18000 bytes CARDCreate makes
// (m_Do_MemCard.cpp:134):
//
//   - block 0: the banner, icon and comment (mDoMemCdRWm_HeaderData,
//     m_Do_MemCardRWmng.h:9-15), which mDoMemCdRWm_Store rebuilds on every
//     save (m_Do_MemCardRWmng.cpp:34-35);
//   - blocks 1 and 2: two copies of card_savedata (m_Do_MemCardRWmng.h:
//     35-42), which Store writes alike (m_Do_MemCardRWmng.cpp:47-69) and
//     Restore reads, slot by slot, preferring the first
//     (m_Do_MemCardRWmng.cpp:125-162);
//   - blocks 3-11: the picture box photos, three per file slot from block
//     3 + 3·slot (card_pictdata, m_Do_MemCardRWmng.cpp:73-101).
//
// card_savedata holds the three file slots, each a card_gamedata: the
// packed dSv_save_c (dSv_save_c_PACKED, 0x768 bytes, d_save.h:922-932)
// and its u64 checksum (m_Do_MemCardRWmng.h:29-33). Unpack and Pack map a
// packed slot to and from the 0x778-byte image the class table indexes
// (internal/coop/class), the way card_to_memory and memory_to_card copy it
// (d_save.cpp:1703-1878). The checksums are the game's own
// (m_Do_MemCardRWmng.cpp:300-358).
//
// Citations follow internal/coop/addr: file:line is the zeldaret/tww
// decomp at 4d78c994 (card.h and CARD*.c are its Dolphin SDK), §N a
// section of docs/08-coop-design.md.
package savefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// The card file's facts.
const (
	// GameCode and Company are the GCI header's gameName and company
	// (card.h:45-46): GZLE01's. CARDOpen finds the file only under the
	// running disc's codes (__CARDAccess, CARDOpen.c:25-35).
	GameCode = "GZLE"
	Company  = "01"
	// FileName is the card file the game opens and creates
	// (m_Do_MemCard.cpp:116,134).
	FileName = "gczelda"

	// BlockSize is a card sector: CARD_SYSTEM_BLOCK_SIZE (card.h:200),
	// the only sector size the game accepts (m_Do_MemCard.cpp:324).
	BlockSize = 0x2000
	// HeaderSize is a GCI file's header: sizeof(CARDDir) (card.h:44-60).
	HeaderSize = 0x40
	// FileBlocks is the file's length in blocks: CARDCreate's 0x18000
	// bytes (m_Do_MemCard.cpp:134).
	FileBlocks = 12
	// FileSize is the file's data, FileBlocks blocks.
	FileSize = FileBlocks * BlockSize
	// GCISize is a whole GCI file: the header and the data.
	GCISize = HeaderSize + FileSize

	// Slots is the number of files on the file select screen: the
	// gamedata[3] of card_savedata (m_Do_MemCardRWmng.h:38). Slot n is
	// mDataNum n, shown as file n+1.
	Slots = 3
	// PackedSize is one slot's save image on the card: dSv_save_c
	// without its alignment padding (STATIC_ASSERT, d_save.h:932).
	PackedSize = 0x768
	// GameDataSize is card_gamedata: the packed image, then its u64
	// checksum (m_Do_MemCardRWmng.h:29-33); sizeof(dSv_save_c) - 8, as
	// the save menu computes it (d_menu_save.cpp:938).
	GameDataSize = 0x770
)

// The file's blocks.
const (
	blockSave  = 1 // card_savedata, the copy Restore reads first (m_Do_MemCardRWmng.cpp:125)
	blockSave2 = 2 // its second copy (m_Do_MemCardRWmng.cpp:130)
	blockPict  = 3 // card_pictdata of slot s from 3 + 3·s (m_Do_MemCardRWmng.cpp:75, :86)
)

// card_savedata (m_Do_MemCardRWmng.h:35-42) and card_gamedata
// (m_Do_MemCardRWmng.h:29-33) offsets.
const (
	sdOffSaveCount   = 0x0000 // u32 save_count: ++ on every Store (m_Do_MemCardRWmng.cpp:52)
	sdOffDataVersion = 0x0004 // u32 data_version: Store writes 0 (m_Do_MemCardRWmng.cpp:49)
	sdOffGameData    = 0x0008 // card_gamedata gamedata[3]
	sdOffPad         = 0x1658 // unused; Store zeroes it (m_Do_MemCardRWmng.cpp:47)
	sdOffSum         = 0x1FFC // u32 csum over 0x0000-0x1FFB (m_Do_MemCardRWmng.cpp:56-57)
	gdOffSum         = PackedSize
)

// CARDDir fields (card.h:44-60), big-endian, as Header offsets.
const (
	hdrOffGameName     = 0x00 // u8[4]
	hdrOffCompany      = 0x04 // u8[2]
	hdrOffBannerFormat = 0x07 // u8
	hdrOffFileName     = 0x08 // u8[CARD_FILENAME_MAX = 32] (card.h:11)
	hdrOffTime         = 0x28 // u32: seconds since 2000-01-01
	hdrOffIconAddr     = 0x2C // u32
	hdrOffIconFormat   = 0x30 // u16
	hdrOffIconSpeed    = 0x32 // u16
	hdrOffPermission   = 0x34 // u8
	hdrOffLength       = 0x38 // u16: blocks
	hdrOffCommentAddr  = 0x3C // u32
	fileNameMax        = 32
)

// commentAddr is where Store's header block keeps the comment strings:
// offsetof(mDoMemCdRWm_HeaderData, comment) (m_Do_MemCardRWmng.h:13).
const commentAddr = 0x1C00

// ErrFormat is the error, wrapped with what is wrong, for input that is
// not a Wind Waker save the game would open.
var ErrFormat = errors.New("savefile: not a Wind Waker (GZLE01) save")

func formatErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrFormat, fmt.Sprintf(format, a...))
}

// Header is a GCI file's header: the save's card directory entry
// (CARDDir, card.h:44-60). Its bytes are kept as they were read, so
// Encode writes back exactly what Parse took.
type Header [HeaderSize]byte

func (h *Header) GameName() string { return string(h[hdrOffGameName : hdrOffGameName+4]) }
func (h *Header) Company() string  { return string(h[hdrOffCompany : hdrOffCompany+2]) }

// FileName is the entry's file name, up to its NUL.
func (h *Header) FileName() string {
	n := h[hdrOffFileName : hdrOffFileName+fileNameMax]
	if i := bytes.IndexByte(n, 0); i >= 0 {
		n = n[:i]
	}
	return string(n)
}

func (h *Header) BannerFormat() byte  { return h[hdrOffBannerFormat] }
func (h *Header) Time() uint32        { return be32(h[hdrOffTime:]) }
func (h *Header) IconAddr() uint32    { return be32(h[hdrOffIconAddr:]) }
func (h *Header) IconFormat() uint16  { return be16(h[hdrOffIconFormat:]) }
func (h *Header) IconSpeed() uint16   { return be16(h[hdrOffIconSpeed:]) }
func (h *Header) Length() uint16      { return be16(h[hdrOffLength:]) }
func (h *Header) CommentAddr() uint32 { return be32(h[hdrOffCommentAddr:]) }

// isWindWaker reports whether the entry is the file CARDOpen(chan,
// "gczelda") finds on a GZLE01 disc: the disc's codes (__CARDAccess,
// CARDOpen.c:25-35) and the name compared up to its NUL
// (__CARDCompareFileName, CARDOpen.c:7-23).
func (h *Header) isWindWaker() bool {
	n := h[hdrOffFileName : hdrOffFileName+fileNameMax]
	return h.GameName() == GameCode && h.Company() == Company &&
		bytes.HasPrefix(n, []byte(FileName)) && n[len(FileName)] == 0
}

// CheckStat is mDoMemCdRWm_CheckCardStat (m_Do_MemCardRWmng.cpp:
// 273-298) on the entry, which CARDGetStatus copies field by field
// (CARDStat.c:91-96): the icon at 0, the comment at 0x1C00, a C8 banner
// without icon animation, icon 0 C8 at speed 3 and no other icon. Store
// wipes both card_savedata copies when it fails (m_Do_MemCardRWmng.cpp:
// 38-45), and Restore refuses the card (m_Do_MemCardRWmng.cpp:152-153),
// so a file whose entry fails it does not load. Store's own
// mDoMemCdRWm_SetCardStat writes these values (m_Do_MemCardRWmng.cpp:
// 246-270).
func (h *Header) CheckStat() error {
	var bad []string
	if v := h.IconAddr(); v != 0 {
		bad = append(bad, fmt.Sprintf("icon address 0x%X, not 0", v))
	}
	if v := h.CommentAddr(); v != commentAddr {
		bad = append(bad, fmt.Sprintf("comment address 0x%X, not 0x%X", v, commentAddr))
	}
	if v := h.BannerFormat() & 0x07; v != 1 { // CARDGetBannerFormat == 1, CARDGetIconAnim == 0 (card.h:128-129)
		bad = append(bad, fmt.Sprintf("banner format bits %d, not 1", v))
	}
	if v := h.IconFormat(); v != 0x0001 { // CARDGetIconFormat(n): 1 for icon 0, 0 for 1-7 (card.h:130)
		bad = append(bad, fmt.Sprintf("icon formats 0x%04X, not 0x0001", v))
	}
	if v := h.IconSpeed(); v != 0x0003 { // CARDGetIconSpeed(n): 3 for icon 0, 0 for 1-7 (card.h:131)
		bad = append(bad, fmt.Sprintf("icon speeds 0x%04X, not 0x0003", v))
	}
	if len(bad) > 0 {
		return formatErr("the game would refuse the card entry (mDoMemCdRWm_CheckCardStat): %s", strings.Join(bad, "; "))
	}
	return nil
}

// File is the gczelda card file: its directory entry and its 12 blocks.
type File struct {
	Header Header
	Data   [FileSize]byte
}

// Parse reads a GCI file: Wind Waker's entry, GZLE01's codes and the
// name "gczelda", then exactly the 12 blocks the entry's length says.
func Parse(b []byte) (*File, error) {
	if len(b) < HeaderSize {
		return nil, formatErr("%d bytes is shorter than a GCI header (0x%X)", len(b), HeaderSize)
	}
	var h Header
	copy(h[:], b)
	if !h.isWindWaker() {
		return nil, formatErr("the GCI holds %q of game %q company %q, not %q of %s%s",
			h.FileName(), h.GameName(), h.Company(), FileName, GameCode, Company)
	}
	if n := h.Length(); n != FileBlocks {
		return nil, formatErr("the file is %d blocks; the game's is %d", n, FileBlocks)
	}
	if len(b) != GCISize {
		return nil, formatErr("a GCI of %d blocks is 0x%X bytes, not 0x%X", FileBlocks, GCISize, len(b))
	}
	f := &File{Header: h}
	copy(f.Data[:], b[HeaderSize:])
	return f, nil
}

// Encode returns the file as a GCI: the header, then the blocks.
func (f *File) Encode() []byte {
	out := make([]byte, 0, GCISize)
	out = append(out, f.Header[:]...)
	return append(out, f.Data[:]...)
}

// Clone returns a deep copy of f.
func (f *File) Clone() *File {
	c := *f
	return &c
}

// block returns block i of the file's data.
func (f *File) block(i int) []byte { return f.Data[i*BlockSize : (i+1)*BlockSize] }

// Kind is what a save was read from.
type Kind uint8

const (
	KindGCI Kind = 1 + iota // a GCI file
	KindRaw                 // a raw card image (Card)
)

func (k Kind) String() string {
	switch k {
	case KindGCI:
		return "GCI"
	case KindRaw:
		return "raw card"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// maxRead bounds what Read takes in: the largest card, 2048 blocks
// (a 128 Mbit card: 16 blocks per Mbit, CARDMount.c:155).
const maxRead = 2048 * BlockSize

// Decode reads b as a GCI file or a raw card image, by its size: a GCI is
// its header plus whole blocks, a raw card whole blocks only.
func Decode(b []byte) (*File, Kind, error) {
	switch len(b) % BlockSize {
	case HeaderSize:
		f, err := Parse(b)
		return f, KindGCI, err
	case 0:
		f, err := ParseCard(b)
		return f, KindRaw, err
	}
	return nil, 0, formatErr("%d bytes is neither a GCI file (0x40 + blocks of 0x2000) nor a raw card (blocks of 0x2000)", len(b))
}

// Read reads the save at path, a GCI file or a raw card image (Decode).
func Read(path string) (*File, Kind, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxRead+HeaderSize+1))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > maxRead+HeaderSize {
		return nil, 0, fmt.Errorf("%s: %w: larger than any memory card", path, ErrFormat)
	}
	f, k, err := Decode(b)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	return f, k, nil
}

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }
