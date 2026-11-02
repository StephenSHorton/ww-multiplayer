package seachart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
)

// The island tiles are BTI textures (J3D's ResTIMG header, 0x20 bytes,
// then the image). Only the formats the chart uses are decoded: the 49
// fm/rXX_b.bti tiles are IA4. GX textures are stored in tiles: IA4 in 8x4
// blocks of one byte a texel, alpha in the high nibble and intensity in
// the low (the GX texture formats, as Dolphin's TextureDecoder reads them).

const (
	btiHeaderLen = 0x20
	gxIA4        = 2
)

// errFormat is decodeBTI's error for a texture format it doesn't read.
var errFormat = errors.New("seachart: unsupported texture format")

// decodeBTI decodes a BTI file into an NRGBA image: gray from the
// intensity, alpha from the alpha.
func decodeBTI(b []byte) (*image.NRGBA, error) {
	if len(b) < btiHeaderLen {
		return nil, fmt.Errorf("seachart: BTI of %d bytes is shorter than its header", len(b))
	}
	f := b[0]
	w := int(binary.BigEndian.Uint16(b[2:]))
	h := int(binary.BigEndian.Uint16(b[4:]))
	off := int(binary.BigEndian.Uint32(b[0x1C:]))
	if w == 0 || h == 0 || w > 1024 || h > 1024 {
		return nil, fmt.Errorf("seachart: BTI size %dx%d out of range", w, h)
	}
	if f != gxIA4 {
		return nil, fmt.Errorf("%w %d", errFormat, f)
	}
	bw, bh := (w+7)/8, (h+3)/4
	need := bw * bh * 32
	if off < 0 || off > len(b) || len(b)-off < need {
		return nil, fmt.Errorf("seachart: BTI image data at 0x%X needs %d bytes, the file has %d", off, need, len(b))
	}
	d := b[off:]
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	i := 0
	for by := 0; by < bh*4; by += 4 {
		for bx := 0; bx < bw*8; bx += 8 {
			for y := 0; y < 4; y++ {
				for x := 0; x < 8; x++ {
					v := d[i]
					i++
					if bx+x >= w || by+y >= h {
						continue
					}
					l := (v & 0x0F) * 0x11
					img.SetNRGBA(bx+x, by+y, color.NRGBA{l, l, l, (v >> 4) * 0x11})
				}
			}
		}
	}
	return img, nil
}
