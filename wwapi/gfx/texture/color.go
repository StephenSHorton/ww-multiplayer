package texture

import "fmt"

// Color is an sRGB tunic color: a Recolor target. The palette, names and
// parsing of a player's color preference belong to the multiplayer mod
// (mods/multiplayer/playercolor, whose Color is this type).
type Color struct {
	R, G, B uint8
}

// Hex renders c as "#RRGGBB".
func (c Color) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// String is the hex form, "#RRGGBB". playercolor.Label gives a palette
// color's name instead.
func (c Color) String() string { return c.Hex() }

// vanilla is the display RGB of the original green tunic. It is
// approximate: Recolor measures the texture's real shade (Calibrate).
var vanilla = Color{0x3C, 0x9A, 0x2E}

// Vanilla is the original green tunic.
func Vanilla() Color { return vanilla }

// IsVanilla reports whether c is the original green, which Recolor leaves
// as the unmodified texture.
func IsVanilla(c Color) bool { return c == vanilla }
