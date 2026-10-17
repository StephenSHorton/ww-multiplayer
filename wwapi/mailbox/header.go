package mailbox

import (
	"encoding/binary"
	"fmt"
)

// HeaderMagic is the 'WWMB' word E1 will have main01_init store at
// mailbox._reserved_hdr (design §6.4 step 5, §9 E1).
const HeaderMagic uint32 = 0x57574D42 // "WWMB"

// HeaderLen is the size of the header in _reserved_hdr: the magic (u32),
// the C ABI (u16) and the layout version (u16).
const HeaderLen = LenReservedHdr * 4

// Header is the WWMB header at mailbox +OffReservedHdr. Every blob before
// E1 leaves those bytes zero, so Present is false and ABI and Layout
// read 0 for all of them.
type Header struct {
	Present bool   // the magic is there
	Magic   uint32 // the word read, whatever it is
	ABI     uint16 // 0 when !Present
	Layout  uint16 // 0 when !Present
}

// ParseHeader decodes the HeaderLen bytes of _reserved_hdr.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderLen {
		return Header{}, fmt.Errorf("mailbox: header: %d bytes, want %d", len(b), HeaderLen)
	}
	h := Header{Magic: binary.BigEndian.Uint32(b)}
	if h.Magic == HeaderMagic {
		h.Present = true
		h.ABI = binary.BigEndian.Uint16(b[4:])
		h.Layout = binary.BigEndian.Uint16(b[6:])
	}
	return h, nil
}

// Reader is the read half of wwapi/mem's Reader, which every mem.Reader
// satisfies. mailbox cannot import mem: mem's tests import mailbox.
type Reader interface {
	Read(a uint32, n int) ([]byte, error)
}

// ReadHeader reads the WWMB header from the mailbox.
func ReadHeader(m Reader) (Header, error) {
	b, err := m.Read(Mailbox+OffReservedHdr, HeaderLen)
	if err != nil {
		return Header{}, fmt.Errorf("mailbox: header: %w", err)
	}
	return ParseHeader(b)
}
