package vissync

import (
	"bytes"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestVitalsTrailer round-trips the vitals at the trailer's old and new
// lengths (wind#55): only a full 64-byte trailer with a max life carries
// them.
func TestVitalsTrailer(t *testing.T) {
	if VitalsOffset+VitalsBytes != network.MaxFaceBytes {
		t.Fatalf("the vitals end at %d, want the %d-byte cap", VitalsOffset+VitalsBytes, network.MaxFaceBytes)
	}
	want := Vitals{Life: 0x2B, MaxLife: 0x50, Magic: 0x11, MaxMagic: 0x20, Rupees: 4999}
	full := append(make([]byte, VitalsOffset), EncodeVitals(want, true)...)
	if got, ok := DecodeVitals(full); !ok || got != want {
		t.Fatalf("64 bytes: got %+v %v, want %+v", got, ok, want)
	}
	// Older senders: the eye bytes alone, + appearance, + equip, + boat run.
	for _, n := range []int{0, 16, 17, 19, 59, 63} {
		if _, ok := DecodeVitals(full[:n]); ok {
			t.Errorf("%d bytes: decoded vitals from a trailer without them", n)
		}
	}
	// A sender that couldn't read them: 64 bytes, all zero.
	zero := append(make([]byte, VitalsOffset), EncodeVitals(Vitals{Life: 3}, false)...)
	if len(zero) != network.MaxFaceBytes || !bytes.Equal(zero[VitalsOffset:], make([]byte, VitalsBytes)) {
		t.Fatalf("unknown vitals encode as %x", zero[VitalsOffset:])
	}
	if _, ok := DecodeVitals(zero); ok {
		t.Error("all-zero vitals decoded as known")
	}
	// Out of range values clamp; a 0 max life is unknown.
	big := Vitals{Life: 300, MaxLife: 300, Magic: 99, MaxMagic: 99, Rupees: 70000}
	got, ok := DecodeVitals(append(make([]byte, VitalsOffset), EncodeVitals(big, true)...))
	if !ok || got != (Vitals{Life: 255, MaxLife: 255, Magic: 48, MaxMagic: 48, Rupees: 0xFFFF}) {
		t.Errorf("clamped: got %+v %v", got, ok)
	}
	if b := EncodeVitals(Vitals{Life: 4}, true); !bytes.Equal(b, make([]byte, VitalsBytes)) {
		t.Errorf("max life 0 encodes as %x, want zeros", b)
	}
}

// TestLocalVitals reads the player status from RAM.
func TestLocalVitals(t *testing.T) {
	m := mem.NewFakeMem()
	if _, ok := LocalVitals(m); ok {
		t.Fatal("no file loaded (max life 0) read as vitals")
	}
	_ = mem.WriteU16(m, addr.MaxLife, 0x30)
	_ = mem.WriteU16(m, addr.Life, 0x2B)
	_ = mem.WriteU16(m, addr.Rupee, 250)
	_ = mem.WriteU8(m, addr.MaxMagic, 0x10)
	_ = mem.WriteU8(m, addr.Magic, 7)
	got, ok := LocalVitals(m)
	if want := (Vitals{Life: 0x2B, MaxLife: 0x30, Magic: 7, MaxMagic: 0x10, Rupees: 250}); !ok || got != want {
		t.Fatalf("got %+v %v, want %+v", got, ok, want)
	}
}
