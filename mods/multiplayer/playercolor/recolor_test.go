package playercolor

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/gfx/texture"
)

// cmprBlock is one 8-byte CMPR (DXT1) block: two big-endian RGB565
// endpoints, then sixteen 2-bit indices.
func cmprBlock(c0, c1 uint16, idx [4]byte) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:], c0)
	binary.BigEndian.PutUint16(b[2:], c1)
	copy(b[4:], idx[:])
	return b
}

func rgb565(r, g, b uint8) uint16 {
	return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
}

// transparency reports, per pixel, whether the block draws it transparent:
// a 3-color block (c0 <= c1) with index 3.
func transparency(blk []byte) (four bool, tr [16]bool) {
	four = binary.BigEndian.Uint16(blk[0:]) > binary.BigEndian.Uint16(blk[2:])
	for i := 0; i < 16; i++ {
		v := (blk[4+i/4] >> (2 * (i % 4))) & 3
		tr[i] = !four && v == 3
	}
	return
}

// Every shipped palette color recolors a green tunic texture: the vanilla
// entry leaves it byte-identical, every other entry changes it, and no
// block's mode or any pixel's opacity changes. texture's own tests cover
// the recolor math over a fixed list; this ties that guarantee to Palette().
func TestPaletteRecolorsKeepModeAndOpacity(t *testing.T) {
	green := rgb565(0x3C, 0x9A, 0x2E)
	darkGreen := rgb565(0x1E, 0x5A, 0x18)
	skin := rgb565(0xF0, 0xC8, 0x96)
	idx := [4]byte{0x1B, 0xE4, 0x4E, 0xB1} // all four index values in every row
	img := bytes.Join([][]byte{
		cmprBlock(green, darkGreen, idx), // 4-color
		cmprBlock(darkGreen, green, idx), // 3-color + transparent
		cmprBlock(green, green, idx),     // equal endpoints
		cmprBlock(green, skin, idx),      // mixed
		cmprBlock(skin, green, idx),      // mixed, other order
	}, nil)
	m, ref := texture.Calibrate(img, texture.DefaultMask)
	if ref.Samples == 0 {
		t.Fatal("calibration found no green endpoints")
	}
	for _, p := range Palette() {
		out, _ := texture.Recolor(img, p.Color, m, ref)
		if len(out) != len(img) {
			t.Fatalf("%s: size %d -> %d", p.Name, len(img), len(out))
		}
		if IsVanilla(p.Color) {
			if !bytes.Equal(out, img) {
				t.Errorf("%s (vanilla) changed the texture", p.Name)
			}
			continue
		}
		if bytes.Equal(out, img) {
			t.Errorf("%s left the texture unchanged", p.Name)
		}
		for off := 0; off < len(img); off += 8 {
			fb, tb := transparency(img[off : off+8])
			fa, ta := transparency(out[off : off+8])
			if fb != fa || tb != ta {
				t.Errorf("%s block %d: mode/opacity %v %v -> %v %v", p.Name, off/8, fb, tb, fa, ta)
			}
		}
	}
}
