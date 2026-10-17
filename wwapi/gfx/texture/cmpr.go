// Package texture recolors Link's green tunic in his body texture
// (docs/09-player-colors.md), and parses the GameCube texture headers
// (ResTIMG) it lives in.
//
// Link's tunic green is not a material color: it is painted into the
// "linktexS3TC" texture of cl.bdl (tww d_a_player_main.cpp:12319-12357,
// the casual-clothes swap of the same texture). The texture is GameCube
// CMPR (S3TC/DXT1-style) data, so a recolor does not need a full decode and
// re-encode: every 8-byte block holds two RGB565 endpoint colors and 2-bit
// indices into the colors interpolated between them. Recoloring the two
// endpoints of each green block recolors every pixel of the block, and
// keeps the block's mode (4-color, or 3-color plus transparent) by
// reordering the endpoints when the recolor flips their order.
//
// The package is pure: bytes in, bytes out. Reading and writing the texture
// in Dolphin's RAM is the caller's job (wwapi/avatar for the tunic slots).
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package texture

import (
	"encoding/binary"
	"math"
	"sort"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// GX texture format numbers (ResTIMG.format, tww JUTTexture.h:15).
const FormatCMPR = 0x0E

// BlockSize is the size of one CMPR (DXT1) block: 4x4 pixels.
const BlockSize = 8

// CMPRSize returns the bytes of a CMPR image of w x h pixels with mips
// levels (ResTIMG.mipmapCount; 0 and 1 both mean the base level only).
// CMPR is stored in 8x8 tiles of four 4x4 blocks, so each level is rounded
// up to whole tiles.
func CMPRSize(w, h, mips int) int {
	if mips < 1 {
		mips = 1
	}
	total := 0
	for i := 0; i < mips && w > 0 && h > 0; i++ {
		tw := (w + 7) / 8
		th := (h + 7) / 8
		total += tw * th * 32
		w /= 2
		h /= 2
	}
	return total
}

// rgb is an 8-bit color expanded from RGB565.
type rgb struct{ r, g, b float64 }

func from565(v uint16) rgb {
	r := (v >> 11) & 0x1F
	g := (v >> 5) & 0x3F
	b := v & 0x1F
	return rgb{
		float64(r<<3 | r>>2),
		float64(g<<2 | g>>4),
		float64(b<<3 | b>>2),
	}
}

func to565(c rgb) uint16 {
	q := func(x float64, max float64) uint16 {
		v := math.Round(clamp(x, 0, 255) * max / 255)
		return uint16(v)
	}
	return q(c.r, 31)<<11 | q(c.g, 63)<<5 | q(c.b, 31)
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// hsv: h in [0,360), s and v in [0,1].
type hsv struct{ h, s, v float64 }

func toHSV(c rgb) hsv {
	r, g, b := c.r/255, c.g/255, c.b/255
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	d := mx - mn
	var h float64
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = 60 * math.Mod((g-b)/d, 6)
	case mx == g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	s := 0.0
	if mx > 0 {
		s = d / mx
	}
	return hsv{h, s, mx}
}

func fromHSV(c hsv) rgb {
	h := math.Mod(c.h, 360)
	if h < 0 {
		h += 360
	}
	s, v := clamp(c.s, 0, 1), clamp(c.v, 0, 1)
	k := func(n float64) float64 {
		kk := math.Mod(n+h/60, 6)
		return v - v*s*math.Max(0, math.Min(kk, math.Min(4-kk, 1)))
	}
	return rgb{k(5) * 255, k(3) * 255, k(1) * 255}
}

func hueDist(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// smoothstep from 0 at e0 to 1 at e1.
func smoothstep(e0, e1, x float64) float64 {
	if e1 == e0 {
		if x >= e1 {
			return 1
		}
		return 0
	}
	t := clamp((x-e0)/(e1-e0), 0, 1)
	return t * t * (3 - 2*t)
}

// Mask decides which colors count as tunic green. A color's weight is 1
// inside the window, falls to 0 across the feathers, and the recolor blends
// by it so block edges between tunic and skin don't step.
type Mask struct {
	// HueCenter is the tunic's hue in degrees; HueHalf the full-weight
	// half-width and HueFeather the fade beyond it.
	HueCenter, HueHalf, HueFeather float64
	// MinSat and MinVal are the full-weight floors, faded over SatFeather
	// and ValFeather below them. They keep greys, whites and near-blacks
	// (eye whites, outlines, the pants) untouched.
	MinSat, SatFeather float64
	MinVal, ValFeather float64
}

// DefaultMask is a broad green window. Calibrate replaces HueCenter with
// the texture's measured tunic hue. (Inferred until the docs/09 live check
// confirms what else in linktexS3TC is green.)
var DefaultMask = Mask{
	HueCenter: 105, HueHalf: 30, HueFeather: 15,
	MinSat: 0.30, SatFeather: 0.12,
	MinVal: 0.10, ValFeather: 0.06,
}

func (m Mask) weight(c hsv) float64 {
	wh := 1 - smoothstep(m.HueHalf, m.HueHalf+m.HueFeather, hueDist(c.h, m.HueCenter))
	ws := smoothstep(m.MinSat-m.SatFeather, m.MinSat, c.s)
	wv := smoothstep(m.MinVal-m.ValFeather, m.MinVal, c.v)
	return wh * ws * wv
}

// Reference is the tunic's own color, measured from the texture.
type Reference struct {
	H, S, V float64
	// Samples is how many green endpoints the measurement used; 0 means
	// the texture had none and the defaults were kept.
	Samples int
}

// Calibrate measures the tunic green from a CMPR image: the median hue,
// saturation and value over the endpoints the mask selects. It returns the
// mask re-centered on that hue and the reference the recolor maps to the
// target color.
func Calibrate(data []byte, m Mask) (Mask, Reference) {
	var hs, ss, vs []float64
	for off := 0; off+BlockSize <= len(data); off += BlockSize {
		for _, e := range [2]uint16{
			binary.BigEndian.Uint16(data[off:]),
			binary.BigEndian.Uint16(data[off+2:]),
		} {
			c := toHSV(from565(e))
			if m.weight(c) >= 0.99 {
				hs = append(hs, c.h)
				ss = append(ss, c.s)
				vs = append(vs, c.v)
			}
		}
	}
	if len(hs) == 0 {
		ref := toHSV(rgbOf(playercolor.Vanilla()))
		return m, Reference{H: ref.h, S: ref.s, V: ref.v}
	}
	ref := Reference{H: median(hs), S: median(ss), V: median(vs), Samples: len(hs)}
	m.HueCenter = ref.H
	return m, ref
}

func median(x []float64) float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func rgbOf(c playercolor.Color) rgb {
	return rgb{float64(c.R), float64(c.G), float64(c.B)}
}

// mapColor moves a tunic color to the target: the target's hue plus the
// texel's offset from the reference hue (keeps shading variation), and
// saturation and value scaled by target/reference (keeps light and shadow).
// Non-tunic colors pass through by the mask weight.
func mapColor(c rgb, m Mask, ref Reference, target hsv) rgb {
	h := toHSV(c)
	w := m.weight(h)
	if w == 0 {
		return c
	}
	ns := h.s
	if ref.S > 0 {
		ns = h.s * target.s / ref.S
	}
	nv := h.v
	if ref.V > 0 {
		nv = h.v * target.v / ref.V
	}
	n := fromHSV(hsv{target.h + (h.h - ref.H), ns, nv})
	return rgb{
		c.r + (n.r-c.r)*w,
		c.g + (n.g-c.g)*w,
		c.b + (n.b-c.b)*w,
	}
}

// remap01 swaps index values 0 and 1 in a byte of four 2-bit indices,
// leaving 2 and 3 alone (3-color mode: 2 is the midpoint, 3 transparent).
func remap01(b byte) byte {
	var out byte
	for i := 0; i < 4; i++ {
		v := (b >> (2 * i)) & 3
		if v < 2 {
			v ^= 1
		}
		out |= v << (2 * i)
	}
	return out
}

// Stats reports what a Recolor changed.
type Stats struct {
	Blocks  int // CMPR blocks in the image
	Changed int // blocks whose bytes changed
}

// Recolor returns a copy of a CMPR image with its tunic green moved to
// target. A vanilla target returns an unmodified copy. ref comes from
// Calibrate on the ORIGINAL texture (calibrating a recolored one would
// measure the wrong color).
func Recolor(data []byte, target playercolor.Color, m Mask, ref Reference) ([]byte, Stats) {
	out := append([]byte(nil), data...)
	st := Stats{Blocks: len(data) / BlockSize}
	if playercolor.IsVanilla(target) {
		return out, st
	}
	t := toHSV(rgbOf(target))
	for off := 0; off+BlockSize <= len(out); off += BlockSize {
		blk := out[off : off+BlockSize]
		c0 := binary.BigEndian.Uint16(blk[0:])
		c1 := binary.BigEndian.Uint16(blk[2:])
		n0 := to565(mapColor(from565(c0), m, ref, t))
		n1 := to565(mapColor(from565(c1), m, ref, t))
		if n0 == c0 && n1 == c1 {
			continue
		}
		fourColor := c0 > c1
		switch {
		case fourColor && n0 < n1:
			n0, n1 = n1, n0
			for i := 4; i < 8; i++ {
				blk[i] ^= 0x55 // 0<->1, 2<->3
			}
		case fourColor && n0 == n1:
			// Equal endpoints switch the decoder to 3-color mode, where
			// index 3 is transparent. Both endpoints are one color now,
			// so point every pixel at endpoint 0.
			for i := 4; i < 8; i++ {
				blk[i] = 0
			}
		case !fourColor && n0 > n1:
			n0, n1 = n1, n0
			for i := 4; i < 8; i++ {
				blk[i] = remap01(blk[i])
			}
		}
		binary.BigEndian.PutUint16(blk[0:], n0)
		binary.BigEndian.PutUint16(blk[2:], n1)
		st.Changed++
	}
	return out, st
}
