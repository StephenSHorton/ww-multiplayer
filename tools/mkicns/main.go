// Command mkicns builds the macOS app icon (docs/img/icon.icns, copied into
// WW Multiplayer.app by scripts/build-mac.sh) from the Windows icon
// (docs/img/icon.ico), so both platforms show the same picture and the
// release build needs no iconutil/sips step. main.go's go:generate line
// runs it:
//
//	go run ./tools/mkicns -o docs/img/icon.icns docs/img/icon.ico
//
// The .ico must hold PNG-compressed images (as icon.ico's 256x256 one is).
// The largest one is stored as is; the 128, 32 and 16 pixel sizes are box
// downscales of it, so Finder lists and the Dock both get a real image.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	out := flag.String("o", "icon.icns", "output .icns path")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: mkicns -o out.icns in.ico")
		os.Exit(2)
	}
	ico, err := os.ReadFile(flag.Arg(0))
	if err == nil {
		var icns []byte
		if icns, err = build(ico); err == nil {
			err = os.WriteFile(*out, icns, 0o644)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkicns:", err)
		os.Exit(1)
	}
}

// icnsType maps a square PNG edge length to its icns element type.
var icnsType = map[int]string{
	16: "icp4", 32: "icp5", 64: "icp6", 128: "ic07",
	256: "ic08", 512: "ic09", 1024: "ic10",
}

// largestPNG returns the biggest PNG-compressed image in an .ico file.
func largestPNG(ico []byte) ([]byte, int, error) {
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return nil, 0, errors.New("not an .ico file")
	}
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	var best []byte
	bestW := 0
	for i := 0; i < n; i++ {
		e := 6 + 16*i
		if e+16 > len(ico) {
			return nil, 0, errors.New("truncated .ico directory")
		}
		size := int(binary.LittleEndian.Uint32(ico[e+8:]))
		off := int(binary.LittleEndian.Uint32(ico[e+12:]))
		if off < 0 || size < 0 || off+size > len(ico) {
			return nil, 0, errors.New("truncated .ico image")
		}
		data := ico[off : off+size]
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			continue // a BMP-format entry: not usable here
		}
		if cfg.Width == cfg.Height && cfg.Width > bestW {
			best, bestW = data, cfg.Width
		}
	}
	if best == nil {
		return nil, 0, errors.New("no square PNG image in the .ico")
	}
	if _, ok := icnsType[bestW]; !ok {
		return nil, 0, fmt.Errorf("largest image is %dx%d; icns needs 16/32/64/128/256/512/1024", bestW, bestW)
	}
	return best, bestW, nil
}

// build returns the .icns bytes for an .ico file.
func build(ico []byte) ([]byte, error) {
	src, w, err := largestPNG(ico)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	type elem struct {
		typ  string
		data []byte
	}
	elems := []elem{{icnsType[w], src}}
	for _, s := range []int{128, 32, 16} {
		if s >= w || w%s != 0 {
			continue
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, downscale(img, w/s)); err != nil {
			return nil, err
		}
		elems = append(elems, elem{icnsType[s], buf.Bytes()})
	}
	total := 8
	for _, e := range elems {
		total += 8 + len(e.data)
	}
	out := make([]byte, 0, total)
	out = append(out, "icns"...)
	out = binary.BigEndian.AppendUint32(out, uint32(total))
	for _, e := range elems {
		out = append(out, e.typ...)
		out = binary.BigEndian.AppendUint32(out, uint32(8+len(e.data)))
		out = append(out, e.data...)
	}
	return out, nil
}

// downscale averages f x f blocks in premultiplied space, so transparent
// edges don't darken.
func downscale(src image.Image, f int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx()/f, b.Dy()/f
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a uint64
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					c := color.RGBA64Model.Convert(src.At(b.Min.X+x*f+dx, b.Min.Y+y*f+dy)).(color.RGBA64)
					r, g, bl, a = r+uint64(c.R), g+uint64(c.G), bl+uint64(c.B), a+uint64(c.A)
				}
			}
			n := uint64(f * f)
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}
