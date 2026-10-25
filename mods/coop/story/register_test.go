package story

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
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
		{"a warp pot ORs its field only", 0xA2, 0x81, 0xFE, 0x87, "DRC 0xA207"},
		{"a letter raised", regLetter, 0x01, 0x03, 0x03, ""},
		{"a letter raised keeps the other bits", regLetter, 0xFD, 0x02, 0xFE, "0xFC|1 -> 0xFC|2"},
		{"the world's other bits are not taken", regLetter, 0x00, 0xF1, 0x01, ""},
		{"a letter never lowered", regLetter, 0xA3, 0x01, 0xA3, ""},
		{"a letter equal", regLetter, 0x52, 0xF2, 0x52, ""},
		{"Beedle points raised", regBeedle, 10, 20, 20, ""},
		{"Beedle points never lowered", regBeedle, 20, 10, 20, ""},
		{"Orca's level 4 over 3", 0xD0, 0x03, 0x04, 0x04, "0xD007: the unmasked level 4 is in the field"},
		{"Orca's level 4 kept", 0xD0, 0x04, 0x03, 0x04, ""},
		{"Koboli's level raised", 0xC2, 0x01, 0x03, 0x03, "0xC203"},
		{"the pig pen ORs its field only", 0xBF, 0x01, 0xF4, 0x05, "0xBF0F"},
		{"Joy Pendants stay local", 0xC0, 0x02, 0x14, 0x02, "0xC0FF"},
		{"Sploosh Kaboom's prizes stay local", 0xFE, 0x00, 0x01, 0x00, "0xFE07: the partner's own first prize is still a heart piece"},
		{"barrel shooting's prizes stay local", 0xB7, 0x00, 0x01, 0x00, "0xB703"},
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
	var img, w save.Image
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
	// 17 figurines, 6 warp pots, the pig pen, and the 16 MAX registers but
	// Beedle points.
	if n != changed || n != 17+6+1+15 {
		t.Errorf("ApplyRegisters changed %d bytes, counted %d, want 39", n, changed)
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
		var img save.Image
		img[off] = l
		world.Apply(&img, []world.Gain{{Off: uint16(off), Val: w & world.SharedMask(off)}})
		if m := world.SharedMask(off); i >= addr.EventRegFirst && got&m != img[off]&m {
			t.Fatalf("0x%02X: ApplyRegister field 0x%02X, world.Apply 0x%02X", i, got&m, img[off]&m)
		}
	})
}
