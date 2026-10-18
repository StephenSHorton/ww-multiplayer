package mailbox

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// HeaderMagic is the 'WWMB' word main01_init stores at mailbox
// +OffHdrMagic from Phase E1 on (design §6.4 step 5, §9 E1).
const HeaderMagic uint32 = WwmbMagic // "WWMB"

// HeaderLen is the size of the header proper: the magic (u32), the C ABI
// (u16) and the layout version (u16), at +OffHdrMagic. The features word
// (+OffFeatures) extends it.
const HeaderLen = OffHdrLayout + 2 - OffHdrMagic

// Header is the WWMB header at mailbox +OffHdrMagic and the features word
// at +OffFeatures. Every blob before E1 leaves those bytes zero, so
// Present is false and ABI, Layout and Features read 0 for all of them.
type Header struct {
	Present  bool   // the magic is there
	Magic    uint32 // the word read, whatever it is
	ABI      uint16 // 0 when !Present
	Layout   uint16 // 0 when !Present
	Features uint32 // the WW_FEAT_* bits; 0 when !Present
}

// ParseHeader decodes the HeaderLen bytes at +OffHdrMagic. Features is
// read separately (ReadHeader).
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderLen {
		return Header{}, fmt.Errorf("mailbox: header: %d bytes, want %d", len(b), HeaderLen)
	}
	h := Header{Magic: binary.BigEndian.Uint32(b)}
	if h.Magic == HeaderMagic {
		h.Present = true
		h.ABI = binary.BigEndian.Uint16(b[OffHdrAbi-OffHdrMagic:])
		h.Layout = binary.BigEndian.Uint16(b[OffHdrLayout-OffHdrMagic:])
	}
	return h, nil
}

// Reader is the read half of wwapi/mem's Reader, which every mem.Reader
// satisfies. mailbox cannot import mem: mem's tests import mailbox.
type Reader interface {
	Read(a uint32, n int) ([]byte, error)
}

// Writer is the write half of wwapi/mem's Mem.
type Writer interface {
	Write(a uint32, b []byte) error
}

// ReadHeader reads the WWMB header and, when the magic is there, the
// features word from the mailbox.
func ReadHeader(m Reader) (Header, error) {
	b, err := m.Read(Mailbox+OffHdrMagic, HeaderLen)
	if err != nil {
		return Header{}, fmt.Errorf("mailbox: header: %w", err)
	}
	h, err := ParseHeader(b)
	if err != nil || !h.Present {
		return h, err
	}
	f, err := m.Read(Mailbox+OffFeatures, 4)
	if err != nil {
		return Header{}, fmt.Errorf("mailbox: features: %w", err)
	}
	if len(f) != 4 {
		return Header{}, fmt.Errorf("mailbox: features: %d bytes, want 4", len(f))
	}
	h.Features = binary.BigEndian.Uint32(f)
	return h, nil
}

// WriteHeader writes h the way main01_init does: the magic, ABI and
// layout version, and the features word. It is for fakes and tests (a
// FakeMem booted like the patched game); nothing writes the header of a
// live game but the C side.
func WriteHeader(m Writer, h Header) error {
	b := make([]byte, HeaderLen)
	binary.BigEndian.PutUint32(b, HeaderMagic)
	binary.BigEndian.PutUint16(b[OffHdrAbi-OffHdrMagic:], h.ABI)
	binary.BigEndian.PutUint16(b[OffHdrLayout-OffHdrMagic:], h.Layout)
	if err := m.Write(Mailbox+OffHdrMagic, b); err != nil {
		return fmt.Errorf("mailbox: header: %w", err)
	}
	f := binary.BigEndian.AppendUint32(nil, h.Features)
	if err := m.Write(Mailbox+OffFeatures, f); err != nil {
		return fmt.Errorf("mailbox: features: %w", err)
	}
	return nil
}

// BlobHash is the CRC-32 (IEEE) of RAM over ranges, each [start, end),
// in order: the blob's read-only sections (internal/inject HashRanges:
// .rodata, .text, .init, .fini), which never change once the game
// booted, so it equals blob.go's BlobHash exactly when RAM runs that
// blob.
func BlobHash(m Reader, ranges [][2]uint32) (uint32, error) {
	if len(ranges) == 0 {
		return 0, fmt.Errorf("mailbox: blob hash: no ranges")
	}
	var h uint32
	for _, r := range ranges {
		start, end := r[0], r[1]
		if end <= start {
			return 0, fmt.Errorf("mailbox: blob hash: empty range 0x%08X..0x%08X", start, end)
		}
		b, err := m.Read(start, int(end-start))
		if err != nil {
			return 0, fmt.Errorf("mailbox: blob hash: %w", err)
		}
		if len(b) != int(end-start) {
			return 0, fmt.Errorf("mailbox: blob hash: read %d bytes, want %d", len(b), end-start)
		}
		h = crc32.Update(h, crc32.IEEETable, b)
	}
	return h, nil
}
