package story

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// TestApplyRegister: figurines by OR, a MAX register by the larger field
// with the byte's other bits kept local, every other byte untouched.
func TestApplyRegister(t *testing.T) {
	cases := []struct {
		name    string
		i       int
		l, w    byte
		want    byte
		comment string
	}{
		{"figurines OR", regFigure, 0x05, 0x30, 0x35, ""},
		{"figurines never clear", regFigure, 0xFF, 0x00, 0xFF, ""},
		{"a letter raised", regLetter, 0x01, 0x03, 0x03, ""},
		{"a letter raised keeps the other bits", regLetter, 0xFD, 0x02, 0xFE, "0xFC|1 -> 0xFC|2"},
		{"the world's other bits are not taken", regLetter, 0x00, 0xF1, 0x01, ""},
		{"a letter never lowered", regLetter, 0xA3, 0x01, 0xA3, ""},
		{"a letter equal", regLetter, 0x52, 0xF2, 0x52, ""},
		{"Beedle points raised", regBeedle, 10, 20, 20, ""},
		{"Beedle points never lowered", regBeedle, 20, 10, 20, ""},
		{"a per-player register", regLocal, 0x01, 0xFF, 0x01, ""},
		{"a story byte", 0x05, 0x00, 0xFF, 0x00, ""},
		{"below the bytes", -1, 0x00, 0xFF, 0x00, ""},
		{"above the bytes", addr.EventSize, 0x00, 0xFF, 0x00, ""},
	}
	for _, c := range cases {
		if got := ApplyRegister(c.i, c.l, c.w); got != c.want {
			t.Errorf("%s: ApplyRegister(0x%02X, 0x%02X, 0x%02X) = 0x%02X, want 0x%02X", c.name, c.i, c.l, c.w, got, c.want)
		}
	}
}

// TestApplyRegisters: only the allowlisted registers change.
func TestApplyRegisters(t *testing.T) {
	var img, w world.Image
	for off := range w {
		w[off] = 0xFF
	}
	img[addr.OffEvent+regLetter] = 0xFC
	img[addr.OffEvent+regBeedle] = 0xFF // already at the top
	n := ApplyRegisters(&img, &w)
	changed := 0
	for off := range img {
		i := off - addr.OffEvent
		r := class.Of(off)
		switch {
		case off == addr.OffEvent+regBeedle:
			if img[off] != 0xFF {
				t.Errorf("Beedle points 0x%02X", img[off])
			}
		case IsRegisterByte(off):
			want := r.Mask
			if off == addr.OffEvent+regLetter {
				want = 0xFF
			}
			if img[off] != want {
				t.Errorf("register 0x%02X: 0x%02X, want 0x%02X", i, img[off], want)
			}
			changed++
		case img[off] != 0:
			t.Errorf("byte 0x%X outside the registers changed to 0x%02X", off, img[off])
		}
	}
	if n != changed || n != 17+13 {
		t.Errorf("ApplyRegisters changed %d bytes, counted %d, want 30", n, changed)
	}
}

// FuzzApplyRegister: the rules never clear a bit or lower a field, keep
// the bits outside the field, are idempotent, and agree with the world's
// own merge on the field.
func FuzzApplyRegister(f *testing.F) {
	for _, s := range [][3]byte{{regFigure, 0x05, 0x30}, {regLetter, 0xFD, 0x02}, {regBeedle, 20, 10}, {regLocal, 1, 2}} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, ib, l, w byte) {
		i := int(ib)
		got := ApplyRegister(i, l, w)
		if ApplyRegister(i, got, w) != got {
			t.Fatalf("not idempotent at 0x%02X: 0x%02X 0x%02X", i, l, w)
		}
		r := class.Of(addr.OffEvent + i)
		switch {
		case i < addr.EventRegFirst || !r.Class.Merges():
			if got != l {
				t.Fatalf("byte 0x%02X changed: 0x%02X -> 0x%02X", i, l, got)
			}
		case r.Class == class.Max:
			if got&^r.Mask != l&^r.Mask || got&r.Mask < l&r.Mask || got&r.Mask != max(l&r.Mask, w&r.Mask) {
				t.Fatalf("MAX 0x%02X: 0x%02X with 0x%02X -> 0x%02X", i, l, w, got)
			}
		default:
			if got&l != l || got != l|w&r.Mask {
				t.Fatalf("OR 0x%02X: 0x%02X with 0x%02X -> 0x%02X", i, l, w, got)
			}
		}
		// The world's merge of the same two bytes has the same field.
		off := addr.OffEvent + i
		var img world.Image
		img[off] = l
		img.Apply([]world.Gain{{Off: uint16(off), Val: w & world.SharedMask(off)}})
		if m := world.SharedMask(off); i >= addr.EventRegFirst && got&m != img[off]&m {
			t.Fatalf("0x%02X: ApplyRegister field 0x%02X, world.Apply 0x%02X", i, got&m, img[off]&m)
		}
	})
}
