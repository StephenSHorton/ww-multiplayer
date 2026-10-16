package playercolor

import "testing"

// FuzzParse: Parse never panics, and whatever it accepts round-trips
// through its String / Hex form (the TUI shows String; the hello and
// player list carry the RGB).
func FuzzParse(f *testing.F) {
	for _, s := range []string{"red", "GREEN", " blue ", "#FF8000", "0x123456", "abcdef", "", "#12345", "#GGGGGG"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Parse(s)
		if err != nil {
			return
		}
		for _, form := range []string{c.String(), c.Hex()} {
			back, err := Parse(form)
			if err != nil || back != c {
				t.Fatalf("Parse(%q) = %v, but Parse(%q) = %v, %v", s, c, form, back, err)
			}
		}
	})
}

// FuzzNearest: Nearest never panics, returns want when it is clear, and
// otherwise a color clear of used whenever some palette color is.
func FuzzNearest(f *testing.F) {
	f.Add(byte(0xD0), byte(0x2C), byte(0x2C), []byte{0xD0, 0x2C, 0x2C})
	f.Add(byte(0), byte(0), byte(0), []byte{})
	f.Fuzz(func(t *testing.T, r, g, b byte, raw []byte) {
		var used []Color
		for i := 0; i+2 < len(raw) && len(used) < 32; i += 3 {
			used = append(used, Color{raw[i], raw[i+1], raw[i+2]})
		}
		want := Color{r, g, b}
		got := Nearest(want, used)
		if !Clashes(want, used) {
			if got != want {
				t.Fatalf("clear want %v became %v", want, got)
			}
			return
		}
		anyClear := false
		for _, p := range palette {
			if !Clashes(p.Color, used) {
				anyClear = true
			}
		}
		if anyClear && Clashes(got, used) {
			t.Fatalf("Nearest(%v) = %v clashes although a palette color is clear", want, got)
		}
	})
}
