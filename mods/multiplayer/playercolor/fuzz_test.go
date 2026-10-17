package playercolor

import "testing"

// FuzzParse: Parse never panics, and whatever it accepts round-trips
// through its Label / Hex form (the TUI shows Label; the hello and
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
		for _, form := range []string{Label(c), c.Hex()} {
			back, err := Parse(form)
			if err != nil || back != c {
				t.Fatalf("Parse(%q) = %v, but Parse(%q) = %v, %v", s, c, form, back, err)
			}
		}
	})
}
