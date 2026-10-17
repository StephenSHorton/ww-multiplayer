package mailbox

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestHeader: zero bytes (every blob before E1) are no header, ABI 0;
// the 'WWMB' magic carries the ABI and layout version.
func TestHeader(t *testing.T) {
	m := mem.NewFakeMem()
	h, err := ReadHeader(m)
	if err != nil {
		t.Fatal(err)
	}
	if h != (Header{}) {
		t.Errorf("zeroed header: %+v", h)
	}
	if err := m.Store(Mailbox+OffReservedHdr, []byte{'W', 'W', 'M', 'B', 0x00, 0x02, 0x01, 0x03}); err != nil {
		t.Fatal(err)
	}
	if h, err = ReadHeader(m); err != nil || !h.Present || h.ABI != 2 || h.Layout != 0x103 {
		t.Errorf("WWMB header: %+v, %v", h, err)
	}
	h, err = ParseHeader([]byte{'X', 'W', 'M', 'B', 0, 2, 0, 3})
	if err != nil || h.Present || h.ABI != 0 || h.Magic != 0x58574D42 {
		t.Errorf("other magic: %+v, %v", h, err)
	}
	if _, err := ParseHeader(make([]byte, HeaderLen-1)); err == nil {
		t.Error("short header parsed")
	}
	if HeaderLen != 8 || OffReservedHdr != 0x180 {
		t.Errorf("header at +0x%X, %d bytes; design §3 says +0x180, 8", OffReservedHdr, HeaderLen)
	}
}
