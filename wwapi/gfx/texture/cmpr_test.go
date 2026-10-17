package texture

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// decodeBlock is a reference DXT1/CMPR block decoder: 16 pixels, each a
// color and whether it is transparent. Index bit order does not matter to
// the tests (they compare pixel i before and after), so pixel i is 2-bit
// field i counted from the low bits of each byte.
func decodeBlock(blk []byte) (px [16]rgb, transparent [16]bool) {
	c0 := binary.BigEndian.Uint16(blk[0:])
	c1 := binary.BigEndian.Uint16(blk[2:])
	a, b := from565(c0), from565(c1)
	var pal [4]rgb
	pal[0], pal[1] = a, b
	four := c0 > c1
	if four {
		pal[2] = rgb{(2*a.r + b.r) / 3, (2*a.g + b.g) / 3, (2*a.b + b.b) / 3}
		pal[3] = rgb{(a.r + 2*b.r) / 3, (a.g + 2*b.g) / 3, (a.b + 2*b.b) / 3}
	} else {
		pal[2] = rgb{(a.r + b.r) / 2, (a.g + b.g) / 2, (a.b + b.b) / 2}
	}
	for i := 0; i < 16; i++ {
		v := (blk[4+i/4] >> (2 * (i % 4))) & 3
		px[i] = pal[v]
		transparent[i] = !four && v == 3
	}
	return
}

func block(c0, c1 uint16, idx [4]byte) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:], c0)
	binary.BigEndian.PutUint16(b[2:], c1)
	copy(b[4:], idx[:])
	return b
}

func c565(r, g, b uint8) uint16 { return to565(rgb{float64(r), float64(g), float64(b)}) }

var (
	green     = c565(0x3C, 0x9A, 0x2E)
	darkGreen = c565(0x1E, 0x5A, 0x18)
	skin      = c565(0xF0, 0xC8, 0x96)
	white     = c565(0xF8, 0xF8, 0xF0)
	red       = playercolor.Palette()[1].Color
)

func TestCMPRSize(t *testing.T) {
	cases := []struct{ w, h, mips, want int }{
		{256, 256, 1, 256 * 256 / 2},
		{256, 256, 0, 256 * 256 / 2},
		{4, 4, 1, 32}, // one 8x8 tile minimum
		{16, 8, 2, 64 + 32},
		{128, 128, 3, 8192 + 2048 + 512},
	}
	for _, c := range cases {
		if got := CMPRSize(c.w, c.h, c.mips); got != c.want {
			t.Errorf("CMPRSize(%d,%d,%d) = %d, want %d", c.w, c.h, c.mips, got, c.want)
		}
	}
}

func TestRGB565RoundTrip(t *testing.T) {
	for v := 0; v < 0x10000; v += 7 {
		if got := to565(from565(uint16(v))); got != uint16(v) {
			t.Fatalf("565 round trip %04X -> %04X", v, got)
		}
	}
}

func TestHSVRoundTrip(t *testing.T) {
	for _, c := range []rgb{{60, 154, 46}, {240, 200, 150}, {0, 0, 0}, {255, 255, 255}, {200, 30, 220}} {
		got := fromHSV(toHSV(c))
		if math.Abs(got.r-c.r) > 0.5 || math.Abs(got.g-c.g) > 0.5 || math.Abs(got.b-c.b) > 0.5 {
			t.Errorf("HSV round trip %v -> %v", c, got)
		}
	}
}

func TestVanillaTargetIsIdentity(t *testing.T) {
	img := append(block(green, darkGreen, [4]byte{0x1B, 0xE4, 0, 0xFF}), block(skin, white, [4]byte{1, 2, 3, 4})...)
	_, ref := Calibrate(img, DefaultMask)
	out, st := Recolor(img, playercolor.Vanilla(), DefaultMask, ref)
	if !bytes.Equal(out, img) || st.Changed != 0 {
		t.Fatalf("vanilla target changed the image: %x -> %x", img, out)
	}
	out[0] ^= 0xFF
	if img[0] == out[0] {
		t.Fatal("Recolor must return a copy, not alias its input")
	}
}

func TestGreenBlockTurnsRedAndSkinStays(t *testing.T) {
	g := block(green, darkGreen, [4]byte{0x1B, 0xE4, 0x4E, 0xB1})
	s := block(skin, white, [4]byte{0x1B, 0xE4, 0x4E, 0xB1})
	img := append(append([]byte(nil), g...), s...)
	m, ref := Calibrate(img, DefaultMask)
	if ref.Samples == 0 {
		t.Fatal("calibration found no green endpoints")
	}
	out, st := Recolor(img, red, m, ref)
	if st.Changed != 1 || st.Blocks != 2 {
		t.Fatalf("stats = %+v, want 1 of 2 blocks changed", st)
	}
	if !bytes.Equal(out[8:], s) {
		t.Errorf("skin/white block changed: %x -> %x", s, out[8:])
	}
	px, _ := decodeBlock(out[:8])
	for i, p := range px {
		h := toHSV(p)
		if hueDist(h.h, toHSV(rgbOf(red)).h) > 15 {
			t.Errorf("pixel %d hue %.0f is not red (%v)", i, h.h, p)
		}
	}
}

// Every pixel keeps its opacity and maps to the same palette slot role:
// a recolor never turns an opaque pixel transparent or vice versa, whatever
// the endpoint order does.
func TestModeAndOpacityPreserved(t *testing.T) {
	targets := []playercolor.Color{}
	for _, p := range playercolor.Palette() {
		targets = append(targets, p.Color)
	}
	targets = append(targets, playercolor.Color{R: 0xFF, G: 0xFF, B: 0xFF}, playercolor.Color{})
	idx := [4]byte{0x1B, 0xE4, 0x4E, 0xB1} // all four index values in every row
	blocks := [][]byte{
		block(green, darkGreen, idx), // 4-color
		block(darkGreen, green, idx), // 3-color + transparent
		block(green, green, idx),     // equal endpoints (3-color)
		block(green, skin, idx),      // mixed, 4-color if green > skin
		block(skin, green, idx),      // mixed, other order
		block(green+1, green, idx),   // nearly equal, 4-color
		block(c565(0, 200, 0), c565(0, 199, 0), idx),
	}
	_, ref := Calibrate(bytes.Join(blocks, nil), DefaultMask)
	for _, tg := range targets {
		for bi, b := range blocks {
			out, _ := Recolor(b, tg, DefaultMask, ref)
			_, tBefore := decodeBlock(b)
			pxAfter, tAfter := decodeBlock(out)
			for i := range tBefore {
				if tBefore[i] != tAfter[i] {
					t.Fatalf("target %v block %d pixel %d: transparent %v -> %v (%x -> %x)",
						tg, bi, i, tBefore[i], tAfter[i], b, out)
				}
			}
			// Pixels sharing a color before share one after (the block's
			// structure survives; only its colors move).
			pxBefore, _ := decodeBlock(b)
			for i := 0; i < 16; i++ {
				for j := i + 1; j < 16; j++ {
					if pxBefore[i] == pxBefore[j] && !tBefore[i] && pxAfter[i] != pxAfter[j] {
						t.Fatalf("target %v block %d: pixels %d and %d split", tg, bi, i, j)
					}
				}
			}
		}
	}
}

func TestCalibrateMeasuresTheTextureGreen(t *testing.T) {
	// A tunic bluer than the default center (hue ~129, not 105):
	// calibration should follow it, so the vanilla shade maps exactly onto
	// the target.
	teal := c565(0x30, 0xA0, 0x40)
	img := bytes.Repeat(block(teal, c565(0x2E, 0x9C, 0x3E), [4]byte{}), 10)
	m, ref := Calibrate(img, DefaultMask)
	want := toHSV(from565(teal)).h
	if math.Abs(m.HueCenter-want) > 3 || ref.Samples != 20 {
		t.Errorf("calibrated center %.1f (want ~%.1f), samples %d", m.HueCenter, want, ref.Samples)
	}
	// The reference color itself maps onto the target.
	got := mapColor(from565(teal), m, ref, toHSV(rgbOf(red)))
	if d := playercolor.Distance(playercolor.Color{R: uint8(got.r), G: uint8(got.g), B: uint8(got.b)}, red); d > 40 {
		t.Errorf("reference green mapped to %v, %.0f from red", got, d)
	}
}

func TestCalibrateWithNoGreenKeepsDefaults(t *testing.T) {
	img := bytes.Repeat(block(skin, white, [4]byte{}), 4)
	m, ref := Calibrate(img, DefaultMask)
	if ref.Samples != 0 || m.HueCenter != DefaultMask.HueCenter {
		t.Errorf("no-green calibration = %+v / %+v", m, ref)
	}
	out, st := Recolor(img, red, m, ref)
	if st.Changed != 0 || !bytes.Equal(out, img) {
		t.Error("an image without green changed")
	}
}

func TestRemap01(t *testing.T) {
	// fields (low to high): 0,1,2,3 -> 1,0,2,3
	if got := remap01(0b11100100); got != 0b11100001 {
		t.Errorf("remap01 = %08b", got)
	}
}
