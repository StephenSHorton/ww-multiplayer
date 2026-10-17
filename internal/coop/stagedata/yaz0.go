// Package stagedata reads the stage data on the user's own Wind Waker disc
// (§6.2, §7.1 of the co-op design): the Yaz0 compression and RARC archives
// the stage files come in, and the DZS/DZR chunks inside them. Every
// reader takes untrusted bytes: offsets and counts are bounds-checked,
// decoded sizes are capped, and malformed input is an error, never a
// panic (the fuzz targets in fuzz_test.go hold them to that). No Nintendo
// data ships with it; the tests build their fixtures with stagetest.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), as in wwapi/addr.
package stagedata

import (
	"encoding/binary"
	"fmt"
)

// Yaz0 is JSystem's SZS compression, a byte-oriented LZ77. A stream is a
// 0x10-byte header (SYaz0Header, JKRDvdRipper.h:13-16; the data starts at
// +0x10, JKRDecomp.cpp:223):
//
//	0x00  "Yaz0"
//	0x04  u32 decoded size (JKRDecompExpandSize, JKRDecomp.h:69-71)
//	0x08  8 bytes the decoder ignores
//
// then groups of a code byte and up to eight operations, one per code bit
// from the top: a 1 bit copies the next byte through, a 0 bit is a
// back-reference of two or three bytes b0 b1 [b2]:
//
//	distance  ((b0 & 0x0F) << 8 | b1) + 1, counted back from the next output byte
//	count     (b0 >> 4) + 2 when b0 >> 4 is nonzero (3..17), else b2 + 0x12 (18..273)
//
// A back-reference may overlap the bytes it is producing (distance <
// count repeats them). Decoding stops at the declared size, even in the
// middle of a back-reference. All of this is decompSZS_subroutine
// (JKRDvdRipper.cpp:270-380), the decoder that streams stage archives off
// the disc; JKRDecomp::decodeSZS (JKRDecomp.cpp:207-269) is the same.
const (
	yaz0HeaderSize = 0x10

	// MaxYaz0Size caps the decoded size a Yaz0 header may declare. The
	// game expands a Yaz0 file into one heap block (loadToMainRAM,
	// JKRDvdRipper.cpp:78-84), which has to fit in MEM1's 24 MiB along
	// with the game itself, so a larger claim is corrupt.
	MaxYaz0Size = 24 << 20

	// yaz0MaxRatio bounds the output per input byte: a three-byte
	// back-reference makes at most 273 bytes.
	yaz0MaxRatio = 273 / 3
)

// IsYaz0 reports whether b starts with the Yaz0 magic, the test
// JKRDecomp::checkCompressed makes (JKRDecomp.cpp:272-284).
func IsYaz0(b []byte) bool {
	return len(b) >= 4 && string(b[:4]) == "Yaz0"
}

// DecodeYaz0 expands the Yaz0 stream src. It refuses a declared size over
// MaxYaz0Size or over what src's length could expand to, so the output
// (allocated once) stays proportional to the input; a back-reference
// before the start of the output; and a stream that ends early. Bytes
// after the declared size is reached are ignored, as the game ignores
// them.
func DecodeYaz0(src []byte) ([]byte, error) {
	if len(src) < yaz0HeaderSize {
		return nil, fmt.Errorf("yaz0: header truncated (%d bytes, need %d)", len(src), yaz0HeaderSize)
	}
	if !IsYaz0(src) {
		return nil, fmt.Errorf("yaz0: bad magic %q", src[:4])
	}
	size := binary.BigEndian.Uint32(src[4:])
	if size > MaxYaz0Size {
		return nil, fmt.Errorf("yaz0: header declares %d bytes, more than the %d-byte cap", size, MaxYaz0Size)
	}
	in := src[yaz0HeaderSize:]
	if uint64(size) > uint64(len(in))*yaz0MaxRatio {
		return nil, fmt.Errorf("yaz0: header declares %d bytes, more than %d bytes of data can expand to",
			size, len(in))
	}

	dst := make([]byte, size)
	n, p := 0, 0
	var code byte
	bits := 0
	for n < len(dst) {
		if bits == 0 {
			if p >= len(in) {
				return nil, yaz0Truncated(n, len(dst))
			}
			code, bits = in[p], 8
			p++
		}
		if code&0x80 != 0 {
			if p >= len(in) {
				return nil, yaz0Truncated(n, len(dst))
			}
			dst[n] = in[p]
			n++
			p++
		} else {
			if p+2 > len(in) {
				return nil, yaz0Truncated(n, len(dst))
			}
			b0, b1 := in[p], in[p+1]
			p += 2
			dist := int(b0&0x0F)<<8 | int(b1) + 1
			count := int(b0>>4) + 2
			if b0>>4 == 0 {
				if p >= len(in) {
					return nil, yaz0Truncated(n, len(dst))
				}
				count = int(in[p]) + 0x12
				p++
			}
			if dist > n {
				return nil, fmt.Errorf("yaz0: back-reference %d bytes back at output byte %d, before the start", dist, n)
			}
			count = min(count, len(dst)-n)
			if dist >= count {
				copy(dst[n:n+count], dst[n-dist:])
			} else {
				for i := n; i < n+count; i++ { // byte by byte: an overlapping copy repeats
					dst[i] = dst[i-dist]
				}
			}
			n += count
		}
		code <<= 1
		bits--
	}
	return dst, nil
}

func yaz0Truncated(n, size int) error {
	return fmt.Errorf("yaz0: data ends after %d of %d bytes", n, size)
}
