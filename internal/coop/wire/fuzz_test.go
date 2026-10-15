package wire

import (
	"bytes"
	"errors"
	"testing"
)

// Native fuzz targets, one per decoder plus the dispatcher, in the style of
// internal/network/fuzz_test.go. The seed corpora (the golden payloads and
// a few broken ones) run as ordinary tests under `go test`; to fuzz one,
// e.g.:
//
//	go test -run='^$' -fuzz=FuzzDecodeGainsUp -fuzztime=20s ./internal/coop/wire/
//
// Every target checks the same contract: a body either fails with an error
// that wraps ErrMalformed or ErrSchema, or decodes to a valid message that
// re-encodes to exactly the payload it came from (decoding is strict, so
// there is one encoding per message).

// goldenBodies returns the bodies (payload minus the sub byte) of the
// golden payloads of sub s.
func goldenBodies(t testing.TB, s Sub) [][]byte {
	var out [][]byte
	for _, g := range golden {
		if b := unhex(t, g.hex); Sub(b[0]) == s {
			out = append(out, b[1:])
		}
	}
	return out
}

// fuzzDecoder runs the contract above over decode, the body decoder of
// sub.
func fuzzDecoder[M Msg](f *testing.F, sub Sub, decode func([]byte) (M, error), seeds ...[]byte) {
	f.Helper()
	for _, b := range goldenBodies(f, sub) {
		f.Add(b)
	}
	for _, b := range seeds {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		m, err := decode(body)
		if err != nil {
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrSchema) {
				t.Fatalf("error %v wraps neither ErrMalformed nor ErrSchema", err)
			}
			return
		}
		if err := m.validate(); err != nil {
			t.Fatalf("decoded an invalid %s: %v", sub, err)
		}
		enc, err := Encode(m)
		if err != nil {
			t.Fatalf("re-encoding a decoded %s: %v", sub, err)
		}
		if want := append([]byte{byte(sub)}, body...); !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x, want %x", enc, want)
		}
	})
}

func FuzzDecodeCoHello(f *testing.F) {
	fuzzDecoder(f, SubCoHello, decodeCoHello, nil, []byte{0, 1}, []byte{0, 2}, make([]byte, coHelloLen))
}

func FuzzDecodeCoWelcome(f *testing.F) {
	fuzzDecoder(f, SubCoWelcome, decodeCoWelcome, nil, make([]byte, coWelcomeLen))
}

func FuzzDecodeWorld(f *testing.F) {
	w, _ := Encode(goldenWorld())
	full := make([]byte, worldHeaderLen+0x778)
	full[16], full[17] = 0x07, 0x78
	for i := worldHeaderLen; i < len(full); i++ {
		full[i] = 0xFF
	}
	fuzzDecoder(f, SubWorld, decodeWorld, w[1:], full, w[1:worldHeaderLen+1])
}

func FuzzDecodeGainsUp(f *testing.F) {
	fuzzDecoder(f, SubGains, decodeGainsUp, nil, []byte{0, 0, 0, 0, 0xFF, 0xFF}, gainsUp(Gain{offLife, 1})[1:],
		gainsUp(Gain{offGetItem, 1}, Gain{offGetItem, 1})[1:])
}

func FuzzDecodeGainsDown(f *testing.F) {
	fuzzDecoder(f, SubGains, decodeGainsDown, nil, gainsDown(Gain{offLetterReg, 3})[1:], gainsDown(Gain{0xFFFF, 1})[1:])
}

func FuzzDecodePresence(f *testing.F) {
	fuzzDecoder(f, SubPresence, decodePresence, nil, presence([]byte("sea\x00wD2\x00"), 0, 0, 0, 0)[1:])
}

func FuzzDecodeScoped(f *testing.F) {
	fuzzDecoder(f, SubScoped, decodeScoped, nil, scoped(2, "sea", 0, 0, ScopedWord{1, 0x10000})[1:],
		scoped(4, "M_NewD2", 3, 5, ScopedWord{0, 1}, ScopedWord{15, 1})[1:])
}

func FuzzDecodeNotice(f *testing.F) {
	fuzzDecoder(f, SubNotice, decodeNotice, nil, []byte{1, 2, 0xC3, 0x28}, append([]byte{1, 4}, "\x1b[2J"...))
}

func FuzzDecodeCtrl(f *testing.F) {
	fuzzDecoder(f, SubCtrl, decodeCtrl, nil, []byte{3, 0, 0, 0x51, 0}, []byte{3, 0xFF, 0xFF, 0xFF, 0xFF})
}

// FuzzDecode runs the dispatcher over any payload in either direction: the
// same contract, plus the direction rule.
func FuzzDecode(f *testing.F) {
	for _, g := range golden {
		f.Add(byte(g.dir), unhex(f, g.hex))
	}
	f.Add(byte(Up), []byte{})
	f.Add(byte(Down), []byte{byte(SubCoHello)})
	f.Add(byte(3), []byte{byte(SubCtrl), 1, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, dir byte, payload []byte) {
		m, err := Decode(payload, Dir(dir))
		if err != nil {
			if m != nil {
				t.Fatalf("Decode returned %+v with error %v", m, err)
			}
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrSchema) {
				t.Fatalf("error %v wraps neither ErrMalformed nor ErrSchema", err)
			}
			return
		}
		if !m.Sub().travels(Dir(dir)) || Sub(payload[0]) != m.Sub() {
			t.Fatalf("decoded a %s from sub 0x%02X travelling %s", m.Sub(), payload[0], Dir(dir))
		}
		enc, err := Encode(m)
		if err != nil || !bytes.Equal(enc, payload) {
			t.Fatalf("round trip = %x, %v; want %x", enc, err, payload)
		}
	})
}
