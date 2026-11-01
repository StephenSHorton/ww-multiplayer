package seachart

import (
	"image"
	"image/color"
	"math"
)

// The island tiles are 100x100 texels for a 20000-unit square of sea,
// drawn in the app up to a few hundred pixels across. They are line art:
// land in one tone, outlines and lettering in another, and transparent
// sea. Upscaled as they are (nearest or smooth), the 16 alpha and
// intensity levels show as stairs or blur. upscale instead resamples
// alpha and intensity with a Catmull-Rom cubic (intensity premultiplied
// by alpha, so the transparent sea doesn't bleed into the coast) and
// then pulls both back to hard edges with a narrow smoothstep: the
// contours come out smooth and sharp at any of the chart's zooms. It
// runs on the player's machine, on the art read from their own disc.

// upscaleFactor is how much larger the tiles are drawn: 100 -> 800.
const upscaleFactor = 8

// Palette the tiles are colored in: land where the intensity is high, ink
// where it is low. The sea is the frontend's background.
var (
	landColor = [3]float64{0xEE, 0xDF, 0xB4}
	inkColor  = [3]float64{0x4A, 0x37, 0x22}
)

// upscale returns src scaled by k, sharpened and colored as above.
func upscale(src *image.NRGBA, k int) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	// planes: alpha and premultiplied intensity, 0..1
	a := make([]float64, w*h)
	ia := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.NRGBAAt(x, y)
			av := float64(c.A) / 255
			a[y*w+x] = av
			ia[y*w+x] = av * float64(c.R) / 255
		}
	}
	at := func(p []float64, x, y int) float64 {
		x = min(max(x, 0), w-1)
		y = min(max(y, 0), h-1)
		return p[y*w+x]
	}
	sample := func(p []float64, fx, fy float64) float64 {
		x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
		tx, ty := fx-float64(x0), fy-float64(y0)
		var rows [4]float64
		for j := -1; j <= 2; j++ {
			rows[j+1] = cubic(at(p, x0-1, y0+j), at(p, x0, y0+j), at(p, x0+1, y0+j), at(p, x0+2, y0+j), tx)
		}
		return cubic(rows[0], rows[1], rows[2], rows[3], ty)
	}
	W, H := w*k, h*k
	dst := image.NewNRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		fy := (float64(y)+0.5)/float64(k) - 0.5
		for x := 0; x < W; x++ {
			fx := (float64(x)+0.5)/float64(k) - 0.5
			av := clamp01(sample(a, fx, fy))
			iv := 0.0
			if av > 1e-4 {
				iv = clamp01(sample(ia, fx, fy) / av)
			}
			av = smoothstep(0.3, 0.7, av)
			iv = smoothstep(0.35, 0.65, iv)
			if av == 0 {
				continue
			}
			var c color.NRGBA
			c.R = uint8(math.Round(inkColor[0] + (landColor[0]-inkColor[0])*iv))
			c.G = uint8(math.Round(inkColor[1] + (landColor[1]-inkColor[1])*iv))
			c.B = uint8(math.Round(inkColor[2] + (landColor[2]-inkColor[2])*iv))
			c.A = uint8(math.Round(av * 255))
			dst.SetNRGBA(x, y, c)
		}
	}
	return dst
}

// cubic is the Catmull-Rom spline through p1..p2 at t (0..1).
func cubic(p0, p1, p2, p3, t float64) float64 {
	return p1 + 0.5*t*(p2-p0+t*(2*p0-5*p1+4*p2-p3+t*(3*(p1-p2)+p3-p0)))
}

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

func smoothstep(e0, e1, x float64) float64 {
	t := clamp01((x - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}
