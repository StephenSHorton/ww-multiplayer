package texture

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// FuzzRecolor: for any image bytes and target color, Recolor keeps the
// length, never changes a pixel's opacity, and a vanilla target is the
// identity. Go feeds RAM bytes into it, so it must not panic on a
// truncated or odd-length image either.
func FuzzRecolor(f *testing.F) {
	f.Add(block(green, darkGreen, [4]byte{0x1B, 0xE4, 0x4E, 0xB1}), uint8(0xD0), uint8(0x2C), uint8(0x2C))
	f.Add(block(darkGreen, green, [4]byte{0xFF, 0, 0x55, 0xAA}), uint8(0), uint8(0), uint8(0))
	f.Add([]byte{1, 2, 3}, uint8(0xFF), uint8(0xFF), uint8(0xFF))
	f.Fuzz(func(t *testing.T, data []byte, r, g, b uint8) {
		c := playercolor.Color{R: r, G: g, B: b}
		m, ref := Calibrate(data, DefaultMask)
		out, st := Recolor(data, c, m, ref)
		if len(out) != len(data) {
			t.Fatalf("len %d -> %d", len(data), len(out))
		}
		if st.Blocks != len(data)/BlockSize || st.Changed > st.Blocks {
			t.Fatalf("stats %+v for %d bytes", st, len(data))
		}
		if playercolor.IsVanilla(c) && string(out) != string(data) {
			t.Fatal("vanilla target changed the image")
		}
		for off := 0; off+BlockSize <= len(data); off += BlockSize {
			_, before := decodeBlock(data[off : off+BlockSize])
			_, after := decodeBlock(out[off : off+BlockSize])
			if before != after {
				t.Fatalf("block at %d: opacity %v -> %v", off, before, after)
			}
		}
	})
}
