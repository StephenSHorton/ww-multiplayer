package texture

import "testing"

func TestColorHexAndString(t *testing.T) {
	c := Color{R: 0xD0, G: 0x2C, B: 0x0A}
	if c.Hex() != "#D02C0A" || c.String() != "#D02C0A" {
		t.Errorf("Hex %q String %q", c.Hex(), c.String())
	}
	if !IsVanilla(Vanilla()) || IsVanilla(c) || IsVanilla(Color{}) {
		t.Error("IsVanilla")
	}
	if Vanilla().Hex() != "#3C9A2E" {
		t.Errorf("Vanilla %s", Vanilla().Hex())
	}
}
