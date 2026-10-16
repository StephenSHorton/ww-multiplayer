package playercolor

import "testing"

func TestPaletteEntriesAreMutuallyDistinct(t *testing.T) {
	p := Palette()
	for i := range p {
		for j := i + 1; j < len(p); j++ {
			if d := Distance(p[i].Color, p[j].Color); d < MinDistance {
				t.Errorf("%s and %s are %.0f apart (min %d)", p[i].Name, p[j].Name, d, MinDistance)
			}
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Color
	}{
		{"red", palette[1].Color},
		{" Blue ", palette[2].Color},
		{"#FF8000", Color{0xFF, 0x80, 0x00}},
		{"ff8000", Color{0xFF, 0x80, 0x00}},
		{"0x102030", Color{0x10, 0x20, 0x30}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "chartreuse", "#12345", "#GGGGGG", "#1234567"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted a bad color", bad)
		}
	}
}

func TestStringAndHex(t *testing.T) {
	if s := palette[1].Color.String(); s != "red" {
		t.Errorf("palette red String() = %q", s)
	}
	if s := (Color{1, 2, 3}).String(); s != "#010203" {
		t.Errorf("custom String() = %q", s)
	}
}

func TestPickFollowsPaletteOrder(t *testing.T) {
	var used []Color
	for i, p := range palette {
		got := Pick(nil, used)
		if got != p.Color {
			t.Fatalf("player %d got %v, want palette %s", i+1, got, p.Name)
		}
		used = append(used, got)
	}
	// The palette is exhausted: wraps rather than failing.
	if got := Pick(nil, used); got != palette[len(used)%len(palette)].Color {
		t.Errorf("wrap pick = %v", got)
	}
}

func TestPickHonorsAFreePreference(t *testing.T) {
	pref := palette[3].Color // purple
	if got := Pick(&pref, []Color{palette[0].Color}); got != pref {
		t.Errorf("free preference not honored: got %v", got)
	}
	custom := Color{0xFF, 0xFF, 0x00}
	if got := Pick(&custom, nil); got != custom {
		t.Errorf("custom preference not honored: got %v", got)
	}
}

func TestPickMovesAClashingPreference(t *testing.T) {
	red := palette[1].Color
	nearRed := Color{0xD8, 0x30, 0x30}
	got := Pick(&nearRed, []Color{red})
	if got == nearRed {
		t.Fatal("a preference within MinDistance of a used color was honored")
	}
	if got != palette[0].Color {
		t.Errorf("fallback = %v, want the first free palette color (green)", got)
	}
}

func TestVanilla(t *testing.T) {
	if !IsVanilla(Vanilla()) || IsVanilla(palette[1].Color) {
		t.Error("IsVanilla is wrong")
	}
}

func TestForID(t *testing.T) {
	if ForID(1) != palette[0].Color || ForID(2) != palette[1].Color {
		t.Error("ForID does not follow palette order from ID 1")
	}
	if ForID(byte(len(palette)+1)) != palette[0].Color {
		t.Error("ForID does not wrap")
	}
	if ForID(0) != palette[0].Color {
		t.Error("ForID(0) should be the vanilla color")
	}
}
