package stage

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
)

// Every sample, empty input included, comes back from DecodeYaz0 byte
// for byte, under a header declaring its size.
func TestEncodeYaz0RoundTrip(t *testing.T) {
	samples := yaz0Samples()
	samples["three bytes"] = []byte("abc")
	samples["eight literals, one code byte"] = []byte("abcdefgh")
	samples["zeros"] = make([]byte, 0x5000)
	for name, in := range samples {
		enc := EncodeYaz0(in)
		if !IsYaz0(enc) || binary.BigEndian.Uint32(enc[4:]) != uint32(len(in)) {
			t.Errorf("%s: header %x doesn't declare %d bytes", name, enc[:8], len(in))
		}
		got, err := DecodeYaz0(enc)
		if err != nil || !bytes.Equal(got, in) {
			t.Errorf("%s: decode(encode(%d bytes)) = %d bytes, %v", name, len(in), len(got), err)
		}
	}
	if enc := EncodeYaz0(nil); len(enc) != yaz0HeaderSize+1 || enc[yaz0HeaderSize] != 0 {
		t.Errorf("empty input encodes to %x, want the header and one zero byte", enc)
	}
	if enc, in := EncodeYaz0([]byte("abcdefgh")), 8; len(enc) != yaz0HeaderSize+1+in+1 {
		t.Errorf("eight literals encode to %d bytes, want header, code byte, 8 literals and the trailing zero", len(enc))
	}
	if text := samples["text"]; len(EncodeYaz0(text)) >= len(text)/4 {
		t.Errorf("repetitive text of %d bytes encodes to %d bytes", len(text), len(EncodeYaz0(text)))
	}
	if zeros := samples["zeros"]; len(EncodeYaz0(zeros)) > yaz0HeaderSize+len(zeros)/273*3+16 {
		t.Errorf("%d zeros encode to %d bytes", len(zeros), len(EncodeYaz0(zeros)))
	}
}

// The encoder uses every kind of operation: literals, short and long
// back-references, ones overlapping their own output, and the deferred
// literal when the next position matches better.
func TestEncodeYaz0Operations(t *testing.T) {
	nLit, nShort, nLong, nOverlap := yaz0Ops(t, EncodeYaz0(yaz0Samples()["mixed"]))
	if nLit == 0 || nShort == 0 || nLong == 0 || nOverlap == 0 {
		t.Errorf("mixed sample: %d literals, %d short, %d long, %d overlapping; want some of each", nLit, nShort, nLong, nOverlap)
	}
	if _, _, long, _ := yaz0Ops(t, EncodeYaz0(yaz0Samples()["window edge"])); long == 0 {
		t.Error("window edge: the 0x1000-back repeat wasn't matched")
	}
	// In the final "abcdefghij", "abc" matches 3 bytes (of "abcQ") and,
	// one byte on, "bcdefghij" matches 9: yaz0.py writes 'a' as a
	// literal and takes the 9-byte match, where a greedy encoder takes
	// the 3 and then 7.
	in := []byte("abcQ.bcdefghij.abcdefghij")
	enc := EncodeYaz0(in)
	if got, err := DecodeYaz0(enc); err != nil || !bytes.Equal(got, in) {
		t.Fatalf("round trip: %v", err)
	}
	if ops := yaz0MatchLengths(enc); len(ops) != 1 || ops[0] != 9 {
		t.Errorf("matches %v, want [9]: the deferred literal wasn't taken", ops)
	}
}

// yaz0MatchLengths lists the back-reference lengths of a valid stream,
// in order.
func yaz0MatchLengths(enc []byte) []int {
	var ops []int
	size, p, n := int(binary.BigEndian.Uint32(enc[4:])), yaz0HeaderSize, 0
	for n < size {
		code := enc[p]
		p++
		for bit := 0; bit < 8 && n < size; bit++ {
			if code&(0x80>>bit) != 0 {
				n++
				p++
				continue
			}
			count := int(enc[p]>>4) + 2
			if enc[p]>>4 == 0 {
				count = int(enc[p+2]) + 0x12
				p += 3
			} else {
				p += 2
			}
			ops = append(ops, count)
			n += count
		}
	}
	return ops
}

// decode(encode(x)) == x over random inputs of every shape: random
// bytes, runs, repeats at every distance up to past the window, and
// mixes.
func TestEncodeYaz0Property(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for round := 0; round < 300; round++ {
		n := rng.IntN(0x3000)
		in := make([]byte, 0, n)
		for len(in) < n {
			switch rng.IntN(4) {
			case 0: // random bytes
				k := rng.IntN(64)
				for i := 0; i < k; i++ {
					in = append(in, byte(rng.Uint32()))
				}
			case 1: // a run
				in = append(in, bytes.Repeat([]byte{byte(rng.Uint32())}, rng.IntN(600))...)
			case 2: // a repeat from some distance back
				if len(in) > 0 {
					dist := 1 + rng.IntN(min(len(in), 0x1100))
					k := rng.IntN(300)
					for i := 0; i < k; i++ {
						in = append(in, in[len(in)-dist])
					}
				}
			case 3: // a few bytes from a tiny alphabet
				k := rng.IntN(40)
				for i := 0; i < k; i++ {
					in = append(in, "ab"[rng.IntN(2)])
				}
			}
		}
		in = in[:min(len(in), n)]
		got, err := DecodeYaz0(EncodeYaz0(in))
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("round %d: %d bytes: %v", round, len(in), err)
		}
	}
}

// decodeYaz0 with a limit expands only that much, through the same
// checks.
func TestDecodeYaz0Limit(t *testing.T) {
	enc := EncodeYaz0([]byte("the stage is the stage is the stage"))
	got, err := decodeYaz0(enc, 4)
	if err != nil || string(got) != "the " {
		t.Errorf("limit 4: %q, %v", got, err)
	}
	if got, err := decodeYaz0(enc, 1000); err != nil || string(got) != "the stage is the stage is the stage" {
		t.Errorf("limit past the end: %q, %v", got, err)
	}
	if got, err := decodeYaz0(enc, 0); err != nil || len(got) != 0 {
		t.Errorf("limit 0: %q, %v", got, err)
	}
	if _, err := decodeYaz0(enc[:yaz0HeaderSize-1], 4); err == nil {
		t.Error("a truncated header passed")
	}
}

// Every REL on the disc re-encodes to a stream that expands to the same
// bytes, and the re-encoded archive is no larger than the vanilla one
// by more than a few percent (the encoder is the one the game's tools
// used, near enough).
func TestEncodeYaz0VanillaRELs(t *testing.T) {
	a, err := ParseArchive(vanillatest.ReadFile(t, vanillatest.RELSArc))
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Edit()
	if err != nil {
		t.Fatal(err)
	}
	var stored, ours, identical int
	for _, f := range a.Files() {
		if !f.Compressed() {
			t.Errorf("%s: not compressed", f.Path)
			continue
		}
		want, err := a.Read(f)
		if err != nil {
			t.Fatal(err)
		}
		enc := EncodeYaz0(want)
		got, err := DecodeYaz0(enc)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: round trip of %d bytes: %d bytes, %v", f.Path, len(want), len(got), err)
		}
		stored += int(f.Size)
		ours += len(enc)
		if bytes.Equal(enc, e.blobs[f.Index]) {
			identical++
		}
	}
	t.Logf("%d RELs: %d bytes as stored, %d re-encoded, %d entries byte-identical", len(a.Files()), stored, ours, identical)
	if identical != len(a.Files()) {
		t.Errorf("%d of %d RELs re-encode to the bytes on the disc; the encoder diverged from the game's", identical, len(a.Files()))
	}
}

func BenchmarkEncodeYaz0Zeros(b *testing.B) {
	in := make([]byte, 1<<20)
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		EncodeYaz0(in)
	}
}

func BenchmarkEncodeYaz0(b *testing.B) {
	var in []byte
	for _, s := range yaz0Samples() {
		in = append(in, s...)
	}
	for len(in) < 1<<20 {
		in = append(in, in...)
	}
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		EncodeYaz0(in)
	}
}
