package texture

import (
	"encoding/binary"
	"testing"
)

func TestParseResTIMG(t *testing.T) {
	b := make([]byte, ResTIMGSize)
	b[0x00] = FormatCMPR
	b[0x01] = 1
	binary.BigEndian.PutUint16(b[0x02:], 256)
	binary.BigEndian.PutUint16(b[0x04:], 128)
	b[0x18] = 2
	binary.BigEndian.PutUint32(b[0x1C:], 0xFFFFFFE0) // image 0x20 bytes BEFORE the header
	h, err := ParseResTIMG(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.Format != FormatCMPR || h.Width != 256 || h.Height != 128 || h.MipmapCount != 2 || h.AlphaEnabled != 1 {
		t.Fatalf("parsed %+v", h)
	}
	if got := h.ImageAddr(0x80500000); got != 0x804FFFE0 {
		t.Errorf("ImageAddr wraps to %08X, want 804FFFE0", got)
	}
	if n, ok := h.ImageSize(); !ok || n != 256*128/2+128*64/2 {
		t.Errorf("ImageSize = %d %v", n, ok)
	}
	h.Format = 0x08 // C4
	if _, ok := h.ImageSize(); ok {
		t.Error("ImageSize claimed a non-CMPR format")
	}
	if _, err := ParseResTIMG(b[:0x1F]); err == nil {
		t.Error("short header accepted")
	}
}

func TestFindLinktex(t *testing.T) {
	spare, used, ok := FindLinktex([]string{"eyeL", LinktexName, "mayu", LinktexName, LinktexName})
	if !ok || spare != 1 || used != 3 {
		t.Errorf("FindLinktex = %d %d %v", spare, used, ok)
	}
	if _, _, ok := FindLinktex([]string{"a", LinktexName}); ok {
		t.Error("one header is not enough")
	}
}
