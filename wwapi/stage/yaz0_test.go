package stage

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// yaz0Stream assembles a Yaz0 stream declaring size bytes from ops, each
// an encoded operation: one byte for a literal, two or three for a
// back-reference (told apart by length). Code bytes are inserted as the
// format puts them, one before every eight operations.
func yaz0Stream(size uint32, ops ...[]byte) []byte {
	s := make([]byte, yaz0HeaderSize)
	copy(s, "Yaz0")
	binary.BigEndian.PutUint32(s[4:], size)
	code := 0
	for i, op := range ops {
		if i%8 == 0 {
			code = len(s)
			s = append(s, 0)
		}
		if len(op) == 1 {
			s[code] |= 0x80 >> (i % 8)
		}
		s = append(s, op...)
	}
	return s
}

// lit is a literal operation; ref a back-reference of count bytes from
// dist back, in the short form for 3-17 and the long form for 18-273.
func lit(c byte) []byte { return []byte{c} }

func ref(dist, count int) []byte {
	d := dist - 1
	if count < 0x12 {
		return []byte{byte(count-2)<<4 | byte(d>>8), byte(d)}
	}
	return []byte{byte(d >> 8), byte(d), byte(count - 0x12)}
}

// lits returns one literal operation per byte of b.
func lits(b []byte) [][]byte {
	ops := make([][]byte, len(b))
	for i := range b {
		ops[i] = lit(b[i])
	}
	return ops
}

func TestDecodeYaz0(t *testing.T) {
	window := make([]byte, 0x1000)
	for i := range window {
		window[i] = byte(i*7 + i/256)
	}
	cases := []struct {
		name   string
		stream []byte
		want   []byte
	}{
		{"empty", yaz0Stream(0), nil},
		{"literals", yaz0Stream(5, lits([]byte("hello"))...), []byte("hello")},
		{"nine literals span two code bytes", yaz0Stream(9, lits([]byte("123456789"))...), []byte("123456789")},
		{"overlapping copy repeats", yaz0Stream(8, lit('a'), lit('b'), ref(2, 6)), []byte("abababab")},
		{"run of one byte, short form max", yaz0Stream(18, lit('x'), ref(1, 17)), bytes.Repeat([]byte("x"), 18)},
		{"long form min", yaz0Stream(19, lit('y'), ref(1, 0x12)), bytes.Repeat([]byte("y"), 19)},
		{"long form max", yaz0Stream(274, lit('z'), ref(1, 273)), bytes.Repeat([]byte("z"), 274)},
		{"short form min", yaz0Stream(6, lit('p'), lit('q'), lit('r'), ref(3, 3)), []byte("pqrpqr")},
		{"farthest distance", yaz0Stream(0x1003, append(lits(window), ref(0x1000, 3))...), append(bytes.Clone(window), window[:3]...)},
		{"stops inside a back-reference", yaz0Stream(5, lit('a'), lit('b'), ref(2, 6)), []byte("ababa")},
		{"ignores bytes past the end", append(yaz0Stream(2, lit('o'), lit('k')), "trailing junk"...), []byte("ok")},
	}
	for _, c := range cases {
		got, err := DecodeYaz0(c.stream)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDecodeYaz0Rejects(t *testing.T) {
	cases := []struct {
		name    string
		stream  []byte
		wantErr string
	}{
		{"header truncated", []byte("Yaz0\x00\x00\x00\x01"), "header truncated"},
		{"bad magic", append([]byte("Yay0"), make([]byte, 12)...), `bad magic "Yay0"`},
		{"size over the cap", yaz0Stream(MaxYaz0Size+1, lit(0)), "more than the 25165824-byte cap"},
		{"size the data can't reach", yaz0Stream(1000, lit('a'), ref(1, 273)), "more than 5 bytes of data can expand to"},
		{"no data at all", yaz0Stream(1), "more than 0 bytes of data can expand to"},
		{"literal missing", yaz0Stream(2, lit('a'))[:yaz0HeaderSize+2], "data ends after 1 of 2"},
		{"short reference cut", yaz0Stream(4, lit('a'), ref(1, 3))[:yaz0HeaderSize+3], "data ends after 1 of 4"},
		{"long reference cut", yaz0Stream(40, lit('a'), ref(1, 39))[:yaz0HeaderSize+4], "data ends after 1 of 40"},
		{"second code byte missing", yaz0Stream(9, lits([]byte("12345678"))...), "data ends after 8 of 9"},
		{"reference before the start", yaz0Stream(3, ref(1, 3)), "1 bytes back at output byte 0"},
		{"reference too far back", yaz0Stream(5, lit('a'), lit('b'), ref(3, 3)), "3 bytes back at output byte 2"},
	}
	for _, c := range cases {
		got, err := DecodeYaz0(c.stream)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: got %d bytes, err %v; want %q", c.name, len(got), err, c.wantErr)
		}
	}
}

// The cap is inclusive: a stream that really expands to MaxYaz0Size bytes
// decodes.
func TestDecodeYaz0AtTheCap(t *testing.T) {
	ops := [][]byte{lit(0)}
	for n := 1; n < MaxYaz0Size; n += 273 {
		ops = append(ops, ref(1, min(273, MaxYaz0Size-n)))
	}
	got, err := DecodeYaz0(yaz0Stream(MaxYaz0Size, ops...))
	if err != nil || len(got) != MaxYaz0Size || bytes.Count(got, []byte{0}) != MaxYaz0Size {
		t.Fatalf("decoded %d bytes, %v; want %d zeros", len(got), err, MaxYaz0Size)
	}
}

// yaz0Ops counts the operations of each kind in a valid stream, and the
// back-references that overlap their own output.
func yaz0Ops(t *testing.T, s []byte) (literals, short, long, overlapping int) {
	t.Helper()
	size := int(binary.BigEndian.Uint32(s[4:]))
	in, n := s[yaz0HeaderSize:], 0
	for p := 0; n < size; {
		code := in[p]
		p++
		for bit := 0; bit < 8 && n < size; bit++ {
			if code&(0x80>>bit) != 0 {
				literals++
				n++
				p++
				continue
			}
			dist, count := int(in[p]&0x0F)<<8|int(in[p+1])+1, int(in[p]>>4)+2
			if in[p]>>4 == 0 {
				count = int(in[p+2]) + 0x12
				long++
				p += 3
			} else {
				short++
				p += 2
			}
			if dist < count {
				overlapping++
			}
			n += count
		}
	}
	return
}

// yaz0Samples are inputs for the stagetest encoder round trip: empty,
// tiny, incompressible, runs longer than one back-reference, repeats at
// the edge of the window, and a mix that needs every kind of operation.
func yaz0Samples() map[string][]byte {
	rng := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 0x3000)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	edge := slices3(random[:0x1000], random[:0x20], random[:0x20])      // 0x1000 back, then 0x20 back
	far := slices3(random[:0x40], random[0x1000:0x2001], random[:0x40]) // 0x1041 back: out of the window
	var mixed []byte
	for i := 0; i < 64; i++ {
		mixed = append(mixed, random[i*7:i*7+5+i%13]...)             // literals
		mixed = append(mixed, bytes.Repeat([]byte{byte(i)}, i*9)...) // runs, short and long
		mixed = append(mixed, mixed[len(mixed)/3:len(mixed)/3+i%40]...)
	}
	return map[string][]byte{
		"empty":        nil,
		"one byte":     {0x42},
		"two bytes":    {0x42, 0x42},
		"text":         []byte(strings.Repeat("the stage is the stage; ", 20)),
		"random":       random,
		"long run":     bytes.Repeat([]byte{0}, 273*5+17),
		"window edge":  edge,
		"out of reach": far,
		"mixed":        mixed,
	}
}

func slices3(a, b, c []byte) []byte { return append(append(bytes.Clone(a), b...), c...) }

func TestYaz0RoundTrip(t *testing.T) {
	for name, in := range yaz0Samples() {
		enc := stagetest.Yaz0(in)
		got, err := DecodeYaz0(enc)
		if err != nil || !bytes.Equal(got, in) {
			t.Errorf("%s: round trip of %d bytes = %d bytes, %v", name, len(in), len(got), err)
		}
	}

	// The encoder exercises every kind of operation, so fixtures built
	// with it test the whole decoder.
	nLit, nShort, nLong, nOverlap := yaz0Ops(t, stagetest.Yaz0(yaz0Samples()["mixed"]))
	if nLit == 0 || nShort == 0 || nLong == 0 || nOverlap == 0 {
		t.Errorf("mixed sample: %d literals, %d short, %d long, %d overlapping references; want some of each",
			nLit, nShort, nLong, nOverlap)
	}
	if _, _, long, _ := yaz0Ops(t, stagetest.Yaz0(yaz0Samples()["window edge"])); long == 0 {
		t.Error("window edge: the 0x1000-back repeat wasn't matched")
	}
}

func TestIsYaz0(t *testing.T) {
	for b, want := range map[string]bool{"Yaz0": true, "Yaz0\x00\x00": true, "Yay0": false, "Yaz": false, "": false, "yaz0": false} {
		if got := IsYaz0([]byte(b)); got != want {
			t.Errorf("IsYaz0(%q) = %v, want %v", b, got, want)
		}
	}
}

func BenchmarkDecodeYaz0(b *testing.B) {
	var in []byte
	for _, s := range yaz0Samples() {
		in = append(in, s...)
	}
	for len(in) < 4<<20 {
		in = append(in, in...)
	}
	enc := stagetest.Yaz0(in)
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		if _, err := DecodeYaz0(enc); err != nil {
			b.Fatal(err)
		}
	}
}
