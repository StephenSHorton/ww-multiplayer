// Package playercolor holds the per-player tunic colors (docs/09): the
// palette and parsing of a player's --color / WW_COLOR preference. Players
// may share a color; a player without a preference wears classic green
// (Vanilla).
//
// The package is pure: no network, no Dolphin. The server (internal/network)
// assigns a color per player name and sends it in the player list; the
// render side (wwapi/gfx/texture, the C bracket) turns it into a recolored
// tunic texture.
package playercolor

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Color is an sRGB tunic color.
type Color struct {
	R, G, B uint8
}

// Hex renders c as "#RRGGBB".
func (c Color) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// String is the palette name for a palette color, else the hex form.
func (c Color) String() string {
	if n, ok := Name(c); ok {
		return n
	}
	return c.Hex()
}

// Named is one palette entry.
type Named struct {
	Name  string
	Color Color
}

// palette is the colors offered to pick from, in display order. Entry 0 is
// the vanilla green tunic, what a player with no preference wears: it keeps
// the unmodified texture (IsVanilla). The next three are the Four Swords
// colors, then colors that stay far from green, skin and each other. Display RGBs are approximate for green (the texture's real
// shade is measured from the texture itself, see wwapi/gfx/texture).
var palette = []Named{
	{"green", Color{0x3C, 0x9A, 0x2E}},
	{"red", Color{0xD0, 0x2C, 0x2C}},
	{"blue", Color{0x2C, 0x5C, 0xE0}},
	{"purple", Color{0x8C, 0x3C, 0xD0}},
	{"orange", Color{0xF0, 0x8C, 0x1E}},
	{"cyan", Color{0x1E, 0xC0, 0xD0}},
	{"pink", Color{0xF0, 0x6E, 0xB4}},
	{"black", Color{0x2C, 0x2C, 0x34}},
}

// Palette returns a copy of the palette, in display order.
func Palette() []Named {
	return append([]Named(nil), palette...)
}

// Vanilla is the original green tunic.
func Vanilla() Color { return palette[0].Color }

// IsVanilla reports whether c is the original green, which is drawn with the
// unmodified texture.
func IsVanilla(c Color) bool { return c == palette[0].Color }

// Name returns the palette name of c, if c is a palette color.
func Name(c Color) (string, bool) {
	for _, p := range palette {
		if p.Color == c {
			return p.Name, true
		}
	}
	return "", false
}

// Parse reads a color preference: a palette name (any case) or a hex color
// "#RRGGBB", "RRGGBB" or "0xRRGGBB".
func Parse(s string) (Color, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return Color{}, fmt.Errorf("empty color; use a name (%s) or #RRGGBB", names())
	}
	for _, p := range palette {
		if p.Name == t {
			return p.Color, nil
		}
	}
	h := strings.TrimPrefix(strings.TrimPrefix(t, "#"), "0x")
	if len(h) == 6 {
		if v, err := strconv.ParseUint(h, 16, 32); err == nil {
			return Color{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
		}
	}
	return Color{}, fmt.Errorf("unknown color %q; use a name (%s) or #RRGGBB", s, names())
}

func names() string {
	n := make([]string, len(palette))
	for i, p := range palette {
		n[i] = p.Name
	}
	return strings.Join(n, ", ")
}

// Distance is a perceptual-ish distance between two colors ("redmean"
// weighted RGB). 0 for equal colors; about 765 for black against white.
func Distance(a, b Color) float64 {
	rm := (float64(a.R) + float64(b.R)) / 2
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	sq := (2+rm/256)*dr*dr + 4*dg*dg + (2+(255-rm)/256)*db*db
	return math.Sqrt(sq)
}

// ForID is the fallback color for a player whose server sent no colors (a
// build from before docs/09): palette order by player ID. Every client
// computes the same answer, but IDs are not reused, so a reconnect can
// change it; the server-assigned color does not have that problem.
func ForID(id byte) Color {
	if id == 0 {
		return palette[0].Color
	}
	return palette[int(id-1)%len(palette)].Color
}
