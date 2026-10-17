// Package avatartest stands in for the game and the C side of package
// avatar's tunic slots in tests: a sparse big-endian RAM holding Link #1,
// his worn linktexS3TC header and an 8x8 CMPR hero image, and
// multiplayer_update's "allocate once" tunic block.
package avatartest

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gfx/texture"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// RAM is a sparse big-endian GameCube RAM for the tunic tests. Mem is
// the bytes (unset reads as 0); Writes counts Write calls.
type RAM struct {
	Mem    map[uint32]byte
	Writes int
}

// NewRAM returns an empty RAM.
func NewRAM() *RAM { return &RAM{Mem: map[uint32]byte{}} }

func (f *RAM) Read(addr uint32, n int) ([]byte, error) {
	b := make([]byte, n)
	for i := range b {
		b[i] = f.Mem[addr+uint32(i)]
	}
	return b, nil
}

func (f *RAM) ReadU32(addr uint32) (uint32, error) {
	b, _ := f.Read(addr, 4)
	return binary.BigEndian.Uint32(b), nil
}

func (f *RAM) Write(addr uint32, data []byte) error {
	f.Writes++
	for i, v := range data {
		f.Mem[addr+uint32(i)] = v
	}
	return nil
}

// Put32 writes a big-endian u32 (through Write, so it counts).
func (f *RAM) Put32(addr, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	f.Write(addr, b[:])
}

// U8 reads one byte.
func (f *RAM) U8(addr uint32) byte { return f.Mem[addr] }

// Addresses NewLinkRAM lays Link out at.
const (
	Link    = 0x80A00000
	TexArr  = 0x80B00000 // ResTIMG[] of the shared model data
	Worn    = TexArr + 2*texture.ResTIMGSize
	Image   = 0x80B10000
	Casual  = 0x80B20000
	HeapTop = 0x80C00000 // where CAllocate puts the slot buffers, 0x1000 apart
)

// TestImage is an 8x8 CMPR image: two green blocks, a skin block and a
// white one.
func TestImage() []byte {
	c := func(r, g, b uint8) uint16 {
		return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
	}
	var out []byte
	for _, e := range [][2]uint16{
		{c(0x3C, 0x9A, 0x2E), c(0x1E, 0x5A, 0x18)},
		{c(0x40, 0xA0, 0x30), c(0x20, 0x60, 0x18)},
		{c(0xF0, 0xC8, 0x96), c(0xC0, 0x98, 0x70)},
		{c(0xF8, 0xF8, 0xF0), c(0xE0, 0xE0, 0xD8)},
	} {
		blk := make([]byte, 8)
		binary.BigEndian.PutUint16(blk[0:], e[0])
		binary.BigEndian.PutUint16(blk[2:], e[1])
		copy(blk[4:], []byte{0x1B, 0x1B, 0xE4, 0xE4})
		out = append(out, blk...)
	}
	return out
}

// ResTIMG builds a texture header with the fields the tunic code reads.
func ResTIMG(format byte, w, h uint16, imageOffset uint32) []byte {
	b := make([]byte, texture.ResTIMGSize)
	b[0] = format
	binary.BigEndian.PutUint16(b[2:], w)
	binary.BigEndian.PutUint16(b[4:], h)
	binary.BigEndian.PutUint32(b[0x1C:], imageOffset)
	return b
}

// NewLinkRAM sets up Link #1 in the hero's clothes (or casual), his worn
// linktexS3TC header and the hero image, plus an empty mailbox.
func NewLinkRAM(casual bool) *RAM {
	f := NewRAM()
	f.Put32(addr.PlayerPtr, Link)
	f.Put32(Link+addr.PlayerOffCurrLinktex, Worn)
	f.Write(Image, TestImage())
	hero := ResTIMG(texture.FormatCMPR, 8, 8, Image-Worn)
	if casual {
		f.Put32(Link+addr.PlayerOffNoResetFlg1, addr.PlayerFlg1CasualClothes)
		// The worn header holds the casual texture (another format);
		// mOtherLinktex holds the hero header, offsets relative to the
		// worn header.
		f.Write(Worn, ResTIMG(0x09, 16, 16, Casual-Worn))
		f.Write(Link+addr.PlayerOffOtherLinktex, hero)
	} else {
		f.Write(Worn, hero)
		f.Write(Link+addr.PlayerOffOtherLinktex, ResTIMG(0x09, 16, 16, Casual-Worn))
	}
	return f
}

// tunicBuf is the absolute address of tunic_bufs[slot].
func tunicBuf(slot int) uint32 { return mailbox.Mailbox + mailbox.OffTunicBufs + 4*uint32(slot) }

// CAllocate plays multiplayer_update's allocation block.
func (f *RAM) CAllocate() {
	if f.U8(mailbox.Mailbox+mailbox.OffTunicState) != 0 {
		return
	}
	size, _ := f.ReadU32(mailbox.Mailbox + mailbox.OffTunicBufSize)
	if size == 0 {
		return
	}
	for s := 0; s < mailbox.LenTunicBufs; s++ {
		f.Put32(tunicBuf(s), HeapTop+uint32(s)*0x1000)
	}
	f.Mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
}

// Frame advances spawn_trigger, the C side's per-frame counter.
func (f *RAM) Frame() {
	v, _ := f.ReadU32(mailbox.Mailbox)
	f.Put32(mailbox.Mailbox, v+1)
}

// SlotImage reads slot's image (TestImage's size) out of its buffer.
func (f *RAM) SlotImage(s int) []byte {
	buf, _ := f.ReadU32(tunicBuf(s))
	b, _ := f.Read(buf, len(TestImage()))
	return b
}

// ExpectRecolor is TestImage recolored to c, as the tunic slots do it.
func ExpectRecolor(c texture.Color) []byte {
	img := TestImage()
	m, ref := texture.Calibrate(img, texture.DefaultMask)
	out, _ := texture.Recolor(img, c, m, ref)
	return out
}
