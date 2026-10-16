package tunic

import (
	"encoding/binary"
	"fmt"
)

// ResTIMGSize is the size of a ResTIMG texture header (tww JUTTexture.h:14-37).
const ResTIMGSize = 0x20

// ResTIMG is the part of a texture header the recolor needs. Offsets are
// tww JUTTexture.h:15-36.
type ResTIMG struct {
	Format        uint8  // +0x00, GX texture format (FormatCMPR = 0x0E)
	AlphaEnabled  uint8  // +0x01
	Width         uint16 // +0x02
	Height        uint16 // +0x04
	IndexTexture  uint8  // +0x08
	PaletteOffset uint32 // +0x0C, relative to the header
	MipmapCount   uint8  // +0x18
	ImageOffset   uint32 // +0x1C, relative to the header
}

// ParseResTIMG decodes a big-endian ResTIMG header.
func ParseResTIMG(b []byte) (ResTIMG, error) {
	if len(b) < ResTIMGSize {
		return ResTIMG{}, fmt.Errorf("ResTIMG: %d bytes, need %d", len(b), ResTIMGSize)
	}
	return ResTIMG{
		Format:        b[0x00],
		AlphaEnabled:  b[0x01],
		Width:         binary.BigEndian.Uint16(b[0x02:]),
		Height:        binary.BigEndian.Uint16(b[0x04:]),
		IndexTexture:  b[0x08],
		PaletteOffset: binary.BigEndian.Uint32(b[0x0C:]),
		MipmapCount:   b[0x18],
		ImageOffset:   binary.BigEndian.Uint32(b[0x1C:]),
	}, nil
}

// ImageAddr is the address of the image data of a header at hdrAddr. The
// offset is unsigned and wraps, as in tww loadTexNo (J3DTevs.cpp:143).
func (t ResTIMG) ImageAddr(hdrAddr uint32) uint32 { return hdrAddr + t.ImageOffset }

// ImageSize is the image's size in bytes, for the formats the recolor
// handles (CMPR only); ok is false for any other format.
func (t ResTIMG) ImageSize() (n int, ok bool) {
	if t.Format != FormatCMPR {
		return 0, false
	}
	return CMPRSize(int(t.Width), int(t.Height), int(t.MipmapCount)), true
}

// LinktexName is the name of Link's body texture in cl.bdl's TEX1 section.
const LinktexName = "linktexS3TC"

// FindLinktex finds the two linktexS3TC headers in a texture name list.
// cl.bdl has two: the game skips the first and swaps the second for the
// casual clothes (tww d_a_player_main.cpp:12325-12343), so the second is
// the one the materials draw (used) and the first keeps the hero's-clothes
// header untouched (spare). ok is false unless both are present.
func FindLinktex(names []string) (spare, used int, ok bool) {
	spare, used = -1, -1
	for i, n := range names {
		if n != LinktexName {
			continue
		}
		if spare < 0 {
			spare = i
		} else {
			used = i
			return spare, used, true
		}
	}
	return spare, used, false
}
