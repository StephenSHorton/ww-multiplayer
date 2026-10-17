// Package bind owns the co-op binding tag (§5.2): the 0x30 bytes at the
// start of the loaded file's dSv_reserve_c (mReserve, 0x803C532C) that
// say which co-op world the file belongs to. The game saves mReserve with
// the file and loads it back (memory_to_card, d_save.cpp:1780;
// card_to_memory, d_save.cpp:1867), and no vanilla code reads it: only
// d_save.cpp names dComIfGs_getPReserve. So the tag travels with the
// file on the card, and the game never acts on it.
//
// It also travels where it shouldn't. dSv_save_c::init leaves mReserve
// alone (d_save.cpp:1494-1503; only initdata_to_card zeroes it,
// d_save.cpp:2007-2010), so a New Game keeps the previous file's tag
// (dComIfGs_init, d_s_name.cpp:1373), the second-quest ExDataSave writes
// it to the card without the player saving (d_file_select.cpp:3626-3646),
// and file select's copy takes it into another slot
// (d_file_select.cpp:1817-1830). Classify tells a real binding from those
// leaks with the rules V1-V6, and the package's two writers are the only
// game-RAM writes of milestone M2 (§5.1):
//
//   - Bind writes a tag, at an explicit, confirmed bind (§5.3);
//   - Scrub zeroes a leaked tag, or any tag at `coop-tag clear` (§5.5).
//
// Both go through mem.Mem, only into the 0x30 tag bytes, only while the
// gates are open, only after the user confirmed the prompt the plan
// carries, and both verify by reading back.
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, §N a section of docs/08-coop-design.md.
package bind

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The tag's layout (§5.2), as offsets into mReserve. The rest of the
// 0x50-byte dSv_reserve_c, +0x30..+0x4F, is never read or written.
const (
	// TagSize is the bytes of mReserve the tag uses: everything Bind
	// writes and Scrub zeroes.
	TagSize = addr.ReserveTagBytes

	// LayoutV1 is the only layout this version reads and writes.
	LayoutV1 = 1

	offMagic      = 0x00 // "WWCO"
	offLayout     = 0x04 // u8
	offFlags      = 0x05 // u8: none defined in layout 1, so 0
	offSlot       = 0x06 // u8: mDataNum at bind
	offClearCount = 0x07 // u8: mClearCount at bind
	offWorld      = 0x08 // [8]byte
	offNonce      = 0x10 // [8]byte
	offWorldSeq   = 0x18 // u64
	offNameHash   = 0x20 // u32: FNV-1a of mPlayerName
	offReserved   = 0x24 // u32, 0 in layout 1
	offCRC        = 0x28 // u32: CRC-32 (IEEE) of +0x00..+0x27
	offPad        = 0x2C // +0x2C..+0x2F: outside the CRC, kept zero
)

// Magic opens every tag.
var Magic = [4]byte{'W', 'W', 'C', 'O'}

// PlayerNameSize is mPlayerName's size: char[17] (d_save.h:463).
const PlayerNameSize = 17

// FileSlots is the number of files on a card: card_savedata holds
// gamedata[3] (m_Do_MemCardRWmng.h:35-42), so mDataNum of a loaded file
// is 0, 1 or 2.
const FileSlots = 3

// WorldID names a co-op world. Go mints it when a world is founded
// (§5.3); the zero WorldID is no world.
type WorldID [8]byte

// Nonce is the per-bind random number: a file bound twice to the same
// world gets two nonces, so a save state or an older save of the same
// file can be told from the current one (§5.5). The zero Nonce is none.
type Nonce [8]byte

// IsZero reports whether w is the zero WorldID.
func (w WorldID) IsZero() bool { return w == WorldID{} }

// String is w in lowercase hex, 16 digits.
func (w WorldID) String() string { return hex.EncodeToString(w[:]) }

// IsZero reports whether n is the zero Nonce.
func (n Nonce) IsZero() bool { return n == Nonce{} }

// String is n in lowercase hex, 16 digits.
func (n Nonce) String() string { return hex.EncodeToString(n[:]) }

// ParseWorldID reads a WorldID written by String: 16 hex digits, not all
// zero.
func ParseWorldID(s string) (WorldID, error) {
	var w WorldID
	if len(s) != 2*len(w) {
		return w, fmt.Errorf("bind: world ID %q must be %d hex digits", s, 2*len(w))
	}
	if _, err := hex.Decode(w[:], []byte(s)); err != nil {
		return WorldID{}, fmt.Errorf("bind: world ID %q is not hex", s)
	}
	if w.IsZero() {
		return w, fmt.Errorf("bind: world ID %q is the zero ID", s)
	}
	return w, nil
}

// randRead fills b with random bytes. A variable so a test can make it
// fail.
var randRead = rand.Read

// NewWorldID mints a random, non-zero WorldID.
func NewWorldID() (WorldID, error) {
	var w WorldID
	err := mint(w[:])
	return w, err
}

// NewNonce mints a random, non-zero Nonce.
func NewNonce() (Nonce, error) {
	var n Nonce
	err := mint(n[:])
	return n, err
}

func mint(b []byte) error {
	for range 4 {
		if _, err := randRead(b); err != nil {
			return fmt.Errorf("bind: no random numbers: %w", err)
		}
		if !allZero(b) {
			return nil
		}
	}
	return errors.New("bind: the random source returned only zeros")
}

// NameHash is the tag's name check (rule V4): FNV-1a (32-bit) of the
// player name, the bytes of mPlayerName before its NUL. The game sets the
// name with strcpy (setPlayerName, d_save.h:446; dSv_player_info_c::init,
// d_save.cpp:969), so the bytes after the NUL are whatever a longer
// earlier name left there and are not hashed. A name with no NUL is
// hashed in full.
func NameHash(name []byte) uint32 {
	name = name[:min(len(name), PlayerNameSize)]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	h := fnv.New32a()
	h.Write(name)
	return h.Sum32()
}

// Tag is a decoded binding tag (§5.2).
type Tag struct {
	Layout     uint8
	Flags      uint8
	Slot       uint8 // mDataNum when bound: rule V2
	ClearCount uint8 // mClearCount when bound: rule V3
	World      WorldID
	Nonce      Nonce
	// WorldSeq is the world's seq when the tag was last written. M2 writes
	// it only at bind; from M3 it is kept current while the file is armed,
	// so a file saved earlier, or a save state, shows how far behind it
	// is (§5.5).
	WorldSeq uint64
	NameHash uint32 // NameHash of mPlayerName when bound: rule V4
}

// The errors Decode returns, each matched with errors.Is. Classify maps
// them to rule V1 (see Classify).
var (
	// ErrEmpty: all TagSize bytes are zero, which is every vanilla file
	// (initdata_to_card zeroes mReserve, d_save.cpp:2007-2010).
	ErrEmpty = errors.New("bind: no tag (all zero)")
	// ErrMagic: non-zero bytes that do not start with "WWCO": not a
	// co-op tag at all.
	ErrMagic = errors.New("bind: not a co-op tag")
	// ErrLayout: a co-op tag of a layout this version does not know,
	// which a newer ww-multiplayer wrote.
	ErrLayout = errors.New("bind: co-op tag of an unknown layout")
	// ErrDamaged: a layout-1 tag whose CRC or zero fields are wrong.
	ErrDamaged = errors.New("bind: damaged co-op tag")
)

// Encode returns t as the TagSize bytes Bind writes, CRC included. It
// writes t's fields as they are; Decode is what checks them.
func (t Tag) Encode() [TagSize]byte {
	var b [TagSize]byte
	copy(b[offMagic:], Magic[:])
	b[offLayout] = t.Layout
	b[offFlags] = t.Flags
	b[offSlot] = t.Slot
	b[offClearCount] = t.ClearCount
	copy(b[offWorld:], t.World[:])
	copy(b[offNonce:], t.Nonce[:])
	binary.BigEndian.PutUint64(b[offWorldSeq:], t.WorldSeq)
	binary.BigEndian.PutUint32(b[offNameHash:], t.NameHash)
	binary.BigEndian.PutUint32(b[offCRC:], crc32.ChecksumIEEE(b[:offCRC]))
	return b
}

// Decode reads the tag in the first TagSize bytes of b (more are
// ignored, so b may be the whole 0x50-byte mReserve). The error says why
// the bytes are not a valid layout-1 tag, rule V1: ErrEmpty, ErrMagic,
// ErrLayout or ErrDamaged. A Tag that decodes re-encodes to the same
// bytes.
func Decode(b []byte) (Tag, error) {
	if len(b) < TagSize {
		return Tag{}, fmt.Errorf("%w: %d bytes, want 0x%X", ErrDamaged, len(b), TagSize)
	}
	b = b[:TagSize]
	if allZero(b) {
		return Tag{}, ErrEmpty
	}
	if !bytes.Equal(b[offMagic:offMagic+4], Magic[:]) {
		return Tag{}, fmt.Errorf("%w: starts % X", ErrMagic, b[:4])
	}
	t := Tag{
		Layout:     b[offLayout],
		Flags:      b[offFlags],
		Slot:       b[offSlot],
		ClearCount: b[offClearCount],
		WorldSeq:   binary.BigEndian.Uint64(b[offWorldSeq:]),
		NameHash:   binary.BigEndian.Uint32(b[offNameHash:]),
	}
	copy(t.World[:], b[offWorld:])
	copy(t.Nonce[:], b[offNonce:])
	switch {
	case t.Layout == 0:
		return Tag{}, fmt.Errorf("%w: layout 0, which no version writes", ErrDamaged)
	case t.Layout != LayoutV1:
		// A newer layout may check itself differently, so its CRC is
		// not this version's to judge.
		return Tag{}, fmt.Errorf("%w: layout %d, this version reads %d", ErrLayout, t.Layout, LayoutV1)
	case binary.BigEndian.Uint32(b[offCRC:]) != crc32.ChecksumIEEE(b[:offCRC]):
		return Tag{}, fmt.Errorf("%w: CRC mismatch", ErrDamaged)
	case t.Flags != 0 || binary.BigEndian.Uint32(b[offReserved:]) != 0:
		// Inside the CRC, so written on purpose, by a writer that knows
		// fields layout 1 doesn't.
		return Tag{}, fmt.Errorf("%w: layout 1 with flags 0x%02X and reserved word 0x%08X set",
			ErrLayout, t.Flags, binary.BigEndian.Uint32(b[offReserved:]))
	case !allZero(b[offPad:]):
		return Tag{}, fmt.Errorf("%w: bytes +0x2C..+0x2F are % X, want zero", ErrDamaged, b[offPad:])
	case t.World.IsZero() || t.Nonce.IsZero():
		return Tag{}, fmt.Errorf("%w: zero world ID or bind nonce", ErrDamaged)
	}
	return t, nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
