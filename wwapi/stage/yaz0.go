// Package stage reads the stage data on the user's own Wind Waker disc
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
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package stage

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"slices"
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
func DecodeYaz0(src []byte) ([]byte, error) { return decodeYaz0(src, -1) }

// decodeYaz0 is DecodeYaz0, stopping after limit bytes of output when
// limit is not negative: a way to look at the start of a file without
// expanding it. The stream's header is checked the same either way.
func decodeYaz0(src []byte, limit int) ([]byte, error) {
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

	if limit >= 0 && uint64(limit) < uint64(size) {
		size = uint32(limit)
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

// The encoder's limits: a back-reference reaches 0x1000 bytes back (12
// bits of distance, plus one) and copies at most 273 (0xFF + 0x12); a
// shorter match than 3 bytes costs as much as the literals it replaces.
const (
	yaz0Window   = 0x1000
	yaz0MaxMatch = 0xFF + 0x12
	yaz0MinMatch = 3
)

// EncodeYaz0 compresses data as a Yaz0 stream that DecodeYaz0 expands
// back to it byte for byte. It is the encoder in wwrando's yaz0.py (the
// one the WW Online patcher ports), which is the one the game's own
// files were made with: every REL in the disc's RELS.arc re-encodes to
// the bytes stored there (TestEncodeYaz0VanillaRELs). At each position
// the longest match in the previous 0x1000 bytes wins, the farthest of
// equally long ones, as a back-reference when it is 3 bytes or longer;
// a position is written as a literal instead when the next position has
// a match at least 2 bytes longer, which is then used. A code byte
// precedes every eight operations; when the operations fill the last
// code byte exactly, a zero byte ends the stream, as yaz0.py writes one.
// Candidates are found through an index of positions by a hash of their
// first three bytes, so the RELs (2 MiB in all) encode in a fraction of
// a second, and a run of one byte, where every position in the window
// is a candidate, faster still: the far end matches first.
func EncodeYaz0(data []byte) []byte {
	out := make([]byte, yaz0HeaderSize, yaz0HeaderSize+len(data)+len(data)/8+2)
	copy(out, "Yaz0")
	binary.BigEndian.PutUint32(out[4:], uint32(len(data)))

	m := newYaz0Matcher(data)
	code, bit := 0, byte(0) // the current code byte's index in out, and its next bit
	var next yaz0Match      // the match reserved for the next position, when deferred
	deferred := false
	for i := 0; i < len(data); {
		if bit == 0 {
			code, bit = len(out), 0x80
			out = append(out, 0)
		}
		var mt yaz0Match
		if deferred {
			mt, deferred = next, false
		} else if mt = m.find(i); mt.n >= yaz0MinMatch {
			// Would a literal here let the next position match better?
			if n2 := m.find(i + 1); n2.n >= mt.n+2 {
				next, deferred, mt = n2, true, yaz0Match{}
			}
		}
		if mt.n >= yaz0MinMatch {
			d := mt.dist - 1
			if mt.n < 0x12 {
				out = append(out, byte(mt.n-2)<<4|byte(d>>8), byte(d))
			} else {
				out = append(out, byte(d>>8), byte(d), byte(mt.n-0x12))
			}
			i += mt.n
		} else {
			out[code] |= bit
			out = append(out, data[i])
			i++
		}
		bit >>= 1
	}
	if bit == 0 {
		out = append(out, 0)
	}
	return out
}

// yaz0Match is a back-reference candidate: n bytes from dist back; n is
// 0 when there is none.
type yaz0Match struct{ dist, n int }

// yaz0Matcher finds the longest match for a position of data among the
// positions before it: every position that has three bytes is bucketed
// by a hash of them, in ascending order, so a position's candidates are
// walked from the far end of the window, the way yaz0.py scans.
type yaz0Matcher struct {
	data  []byte
	pos   []int32 // positions, grouped by bucket, ascending in each
	start []int32 // per bucket, where its positions start in pos; one more for the end
	shift uint    // 32 minus the bucket index's bits: about one bucket per position, 2^8 to 2^16
}

func newYaz0Matcher(data []byte) *yaz0Matcher {
	n := max(len(data)-yaz0MinMatch+1, 0)
	hashBits := min(max(bits.Len(uint(n)), 8), 16)
	m := &yaz0Matcher{data: data, start: make([]int32, 1<<hashBits+1), shift: uint(32 - hashBits)}
	for i := 0; i < n; i++ {
		m.start[m.hash(i)+1]++
	}
	for h := 1; h < len(m.start); h++ {
		m.start[h] += m.start[h-1]
	}
	m.pos = make([]int32, n)
	fill := slices.Clone(m.start[:1<<hashBits])
	for i := 0; i < n; i++ {
		h := m.hash(i)
		m.pos[fill[h]] = int32(i)
		fill[h]++
	}
	return m
}

func (m *yaz0Matcher) hash(i int) uint32 {
	k := uint32(m.data[i])<<16 | uint32(m.data[i+1])<<8 | uint32(m.data[i+2])
	return (k * 2654435761) >> m.shift
}

// find returns the longest match for position i within the window
// before it, the farthest of equally long ones (yaz0.py scans from the
// far end of the window and keeps the first of the longest).
func (m *yaz0Matcher) find(i int) yaz0Match {
	data := m.data
	if i+yaz0MinMatch > len(data) {
		return yaz0Match{}
	}
	limit := min(yaz0MaxMatch, len(data)-i)
	h := m.hash(i)
	cands := m.pos[m.start[h]:m.start[h+1]]
	lo := int32(max(i-yaz0Window, 0))
	k, _ := slices.BinarySearch(cands, lo)
	var best yaz0Match
	for ; k < len(cands) && int(cands[k]) < i; k++ {
		c := int(cands[k])
		if best.n > 0 && data[c+best.n] != data[i+best.n] {
			continue // can't be longer than the best
		}
		l := 0
		for l+8 <= limit && binary.LittleEndian.Uint64(data[c+l:]) == binary.LittleEndian.Uint64(data[i+l:]) {
			l += 8
		}
		for l < limit && data[c+l] == data[i+l] {
			l++
		}
		if l > best.n {
			best = yaz0Match{dist: i - c, n: l}
			if l == limit {
				break
			}
		}
	}
	return best
}
