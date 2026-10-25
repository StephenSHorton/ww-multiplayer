package gamefiles

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxEmbeddedScan is the most of one file (or zip entry) Embedded reads.
// Our largest asset, the macOS zip, is a few tens of MiB; a bigger file
// fails the scan (an error, check_game_files' exit 2) rather than pass
// unread.
const MaxEmbeddedScan = 2 << 30

// ErrTooBig is Embedded's error for input past MaxEmbeddedScan.
var ErrTooBig = errors.New("too big to scan whole")

// embedded is one format Embedded finds anywhere in a file: its mark, the
// bytes after the mark's start its check reads, and the check, which looks
// past the mark at the header's other fields (a size, an offset, a count)
// so that the mark alone, as a string or a constant in our own binary
// (this package's magics table, the patcher's format checks), is not
// taken for the data.
type embedded struct {
	mark []byte
	// back is how many bytes before the mark the check reads.
	back int
	need int
	what string
	ok   func(h []byte) bool // h starts back bytes before the mark
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func pow2(v, lo, hi uint32) bool { return v >= lo && v <= hi && v&(v-1) == 0 }

// j3dTypes are the J3D file types the game loads (J3DModelLoader.cpp:24-57
// bmd1-3, bmt3, bdl3/4; J3DAnmLoader.cpp:21-76 the animations).
var j3dTypes = map[string]bool{
	"bmd1": true, "bmd2": true, "bmd3": true, "bmt3": true, "bdl3": true, "bdl4": true,
	"bck1": true, "bpk1": true, "blk1": true, "btk1": true, "brk1": true, "bca1": true,
	"btp1": true, "bva1": true, "bla1": true, "bxa1": true,
}

func idChar(c byte) bool { return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }

const gib8 = 8 << 30

// maxData bounds a size or offset field of a file from the disc: the
// game's RAM is 24 MiB, so no archive, model or compressed file reaches
// 64 MiB. It also keeps text out: a field this small starts with a byte
// below 4, which no text has.
const maxData = 1 << 26

var embeddeds = []embedded{
	// SArcHeader then SArcDataInfo (tww include/JSystem/JKernel/
	// JKRArchive.h:10-31): file_length, header_length 0x20 (where the info
	// block starts), file_data_offset inside the file, then num_nodes and
	// node_offset.
	{[]byte("RARC"), 0, 0x28, "a RARC archive", func(h []byte) bool {
		size, hdr, dataOff := be32(h[4:]), be32(h[8:]), be32(h[0xC:])
		nodes, nodeOff := be32(h[0x20:]), be32(h[0x24:])
		return hdr == 0x20 && size >= 0x40 && size <= maxData && dataOff >= 0x20 && dataOff < size &&
			nodes >= 1 && nodes <= 0xFFFF && nodeOff >= 0x20 && nodeOff < size
	}},
	// decodeSZS (JKRDecomp.cpp): the decoded size at 4; the 8 bytes up to
	// the data at 0x10 are zero in every Yaz0 file the game ships.
	{[]byte("Yaz0"), 0, 0x10, "Yaz0-compressed game data", func(h []byte) bool {
		size := be32(h[4:])
		return size >= 0x10 && size <= maxData && bytes.Equal(h[8:0x10], make([]byte, 8))
	}},
	// decodeSZP (JKRDecomp.cpp:131-137): the decoded size at 4, the link
	// table at 8 and the data at 12, both past the 16-byte header: the mask
	// words run from 0x10 to the link table, the link table's u16s to the
	// data.
	{[]byte("Yay0"), 0, 0x10, "Yay0-compressed game data", func(h []byte) bool {
		size, link, data := be32(h[4:]), be32(h[8:]), be32(h[0xC:])
		return size >= 0x10 && size <= maxData && link >= 0x10 && link%4 == 0 && data > link &&
			(data-link)%2 == 0 && data <= maxData
	}},
	// JUTDataFileHeader (JUTDataHeader.h:13-21): a type the game loads,
	// the file size at 8, the block count at 0xC.
	{[]byte("J3D"), 0, 0x10, "a J3D model or animation", func(h []byte) bool {
		size, blocks := be32(h[8:]), be32(h[0xC:])
		return (h[3] == '1' || h[3] == '2') && j3dTypes[string(h[4:8])] &&
			size >= 0x20 && size <= maxData && blocks >= 1 && blocks <= 64
	}},
	// JMessage's header (data.h TParse_THeader): size at 8, blocks at 0xC.
	{[]byte("MESGbmg1"), 0, 0x10, "a BMG message file", func(h []byte) bool {
		size, blocks := be32(h[8:]), be32(h[0xC:])
		return size >= 0x20 && size <= maxData && blocks >= 1 && blocks <= 16
	}},
	// A disc header: a six-character game ID at 0, the disc number (0 or
	// 1) and version at 6 and 7, the Wii magic at 0x18 zero, the GameCube
	// disc magic at 0x1C (unverified in the decomp, Header's note), then
	// the game's title as text at 0x20. The title is what keeps a run of
	// string constants (an ID, then the magic 0x1C on) from passing.
	{[]byte{0xC2, 0x33, 0x9F, 0x3D}, 0x1C, 0x28, "a GameCube disc image", func(h []byte) bool {
		for _, c := range h[:6] {
			if !idChar(c) {
				return false
			}
		}
		if h[6] > 1 || h[7] > 0x10 || be32(h[0x18:]) != 0 {
			return false
		}
		for _, c := range h[0x20:0x28] {
			if c < 0x20 || c > 0x7E {
				return false
			}
		}
		return true
	}},
	// A GCI's CARDDir (wwapi/savefile Header: card.h:44-60): GZLE01, the
	// 0xFF pad, then the file name "gczelda" and its NUL at 8.
	{[]byte("GZLE01\xFF"), 0, 0x10, "a Wind Waker save (GCI)", func(h []byte) bool {
		return bytes.Equal(h[8:0x10], []byte("gczelda\x00"))
	}},
	// CISO (internal/inject/ciso.go): a power-of-two block size, then the
	// block map, one 0 or 1 per block.
	{[]byte("CISO"), 0, 0x18, "a CISO disc image", func(h []byte) bool {
		if !pow2(le32(h[4:]), 0x400, 0x8000000) {
			return false
		}
		for _, b := range h[8:0x18] {
			if b > 1 {
				return false
			}
		}
		return true
	}},
	// WIA and RVZ header 1 (Dolphin WIABlob.h): versions, header 2's
	// size, the disc's size at 0x24.
	{[]byte("WIA\x01"), 0, 0x2C, "a WIA disc image", wiaOK},
	{[]byte("RVZ\x01"), 0, 0x2C, "an RVZ disc image", wiaOK},
	// GCZ (internal/inject/format.go, Dolphin CompressedBlob.h): the disc
	// size at 0x10, a power-of-two block size at 0x18 and the block count
	// at 0x1C that they make.
	{[]byte{0x01, 0xC0, 0x0B, 0xB1}, 0, 0x20, "a GCZ disc image", func(h []byte) bool {
		size := binary.LittleEndian.Uint64(h[0x10:])
		block, n := le32(h[0x18:]), le32(h[0x1C:])
		return size >= 1<<20 && size <= gib8 && pow2(block, 0x200, 1<<24) &&
			uint64(n) == (size+uint64(block)-1)/uint64(block)
	}},
}

func wiaOK(h []byte) bool {
	ver, compat, h2 := be32(h[4:]), be32(h[8:]), be32(h[0xC:])
	size := binary.BigEndian.Uint64(h[0x24:])
	return ver != 0 && ver>>24 <= 1 && compat != 0 && compat <= ver &&
		h2 >= 0x40 && h2 <= 0x1000 && size >= 1<<20 && size <= gib8
}

// window is how many bytes of one chunk Embedded carries into the next, so
// a header that straddles two reads is still seen whole.
const window = 0x40

// Embedded reads r to its end (at most MaxEmbeddedScan bytes) and says
// what game data it holds anywhere inside, not only at its start: an
// archive, compressed data, a model, a message file, a disc header, a
// save, as Header names them but checked by more of each header's fields
// (embeddeds). It is for a file whose name and first bytes passed, such as
// our exe: a go:embed of a game archive or a disc ends up in its data. It
// reads in fixed chunks, so it needs no more memory for a large file. ""
// when it finds none.
//
// A DOL or a REL has no mark of its own, so neither is found inside
// another file (Name catches them as files).
func Embedded(r io.Reader) (string, error) {
	const chunk = 1 << 20
	buf := make([]byte, 0, chunk+window)
	var total, base int64 // base: the file offset of buf[0]
	for {
		n, err := io.ReadFull(r, buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		total += int64(n)
		if total > MaxEmbeddedScan {
			return "", fmt.Errorf("%w (more than %d MiB)", ErrTooBig, MaxEmbeddedScan>>20)
		}
		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !eof {
			return "", err
		}
		if w, at := findEmbedded(buf, eof); w != "" {
			return fmt.Sprintf("holds %s at offset 0x%X", w, base+int64(at)), nil
		}
		if eof {
			return "", nil
		}
		// Keep the last window bytes: a mark there is checked again once
		// its header has all arrived.
		keep := copy(buf, buf[len(buf)-window:])
		base += int64(len(buf) - keep)
		buf = buf[:keep]
	}
}

// findEmbedded checks every mark in buf whose header fits; with more to
// come, a mark too near the end is left for the next chunk.
func findEmbedded(buf []byte, eof bool) (string, int) {
	for _, e := range embeddeds {
		for off := 0; ; {
			i := bytes.Index(buf[off:], e.mark)
			if i < 0 {
				break
			}
			p := off + i
			off = p + 1
			start := p - e.back
			if start < 0 {
				continue // the bytes before it were in the last chunk, already checked
			}
			if start+e.need > len(buf) {
				if !eof {
					break // checked once the next chunk is in
				}
				continue
			}
			if e.ok(buf[start : start+e.need]) {
				return e.what, start
			}
		}
	}
	return "", 0
}
