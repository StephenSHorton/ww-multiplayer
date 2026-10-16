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
