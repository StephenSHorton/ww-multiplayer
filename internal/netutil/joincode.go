package netutil

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Join codes (App PR 11, design decision 12): a host's IPv4 address and
// port written in Crockford base32 with a check symbol, so a friend can
// read it out or paste it instead of an address. There is no service
// behind it: the code IS the address, encoded, so it is as private as the
// address and the app masks it the same way (internal/redact).
//
// Shape: "WW-" then the symbols in groups of four.
//
//	WW-XXXX-XXXX       the default port (25565): 7 data symbols + 1 check
//	WW-XXXX-XXXX-XXXX  any other port: 11 data symbols + 1 check
//
// Short data (35 bits): kind 0 (3 bits), the IPv4 (32 bits).
// Long data (55 bits): kind 1 (3 bits), 4 zero bits, the IPv4 (32 bits),
// the port (16 bits).
//
// The check symbol is a weighted sum over GF(32) (x^5+x^2+1): data symbol
// i (from 0) has weight α^(i+1) and the check symbol weight 1, and the
// whole sums to 0. Any one mistyped symbol and any swap of two neighbours
// (the check symbol included) changes the sum, so it is always caught.
//
// Parsing is forgiving the Crockford way: case is ignored, the dashes
// may be left out (or doubled), I and L read as 1, O as 0. U is not a
// symbol.

// JoinCodePort is the port a short code stands for.
const JoinCodePort = 25565

const (
	codePrefix   = "WW"
	codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	shortSymbols = 8  // 7 data + check
	longSymbols  = 12 // 11 data + check
	kindShort    = 0
	kindLong     = 1
)

// Join code errors. A typo the check symbol caught is ErrJoinCodeTypo.
var (
	ErrJoinCodeTypo   = errors.New("the join code has a typo (its check symbol doesn't match); copy it again")
	ErrJoinCodeLength = errors.New("a join code is WW and 8 or 12 letters and digits, like WW-K7Q2-9XHM")
	ErrJoinCodeSymbol = errors.New("a join code has only digits and the letters A to Z without U")
	ErrJoinCodeKind   = errors.New("this join code comes from a newer ww-multiplayer")
	ErrJoinCodeIPv6   = errors.New("join codes hold IPv4 addresses only")
)

// symbolValue maps a code character (any case) to its value, -1 for none.
var symbolValue = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(codeAlphabet); i++ {
		c := codeAlphabet[i]
		t[c] = int8(i)
		if c >= 'A' && c <= 'Z' {
			t[c+'a'-'A'] = int8(i)
		}
	}
	for _, c := range "IiLl" {
		t[c] = 1
	}
	for _, c := range "Oo" {
		t[c] = 0
	}
	return t
}()

// gfMul multiplies in GF(32) modulo x^5+x^2+1.
func gfMul(a, b byte) byte {
	var p byte
	for b != 0 {
		if b&1 != 0 {
			p ^= a
		}
		b >>= 1
		a <<= 1
		if a&0x20 != 0 {
			a ^= 0x25
		}
	}
	return p
}

// checkSymbol is the value that makes data's weighted sum zero.
func checkSymbol(data []byte) byte {
	var sum, w byte = 0, 2 // α = x
	for _, v := range data {
		sum ^= gfMul(v, w)
		w = gfMul(w, 2)
	}
	return sum
}

// JoinCode is the join code of ap, an IPv4 address and a port (1-65535).
func JoinCode(ap netip.AddrPort) (string, error) {
	ip := ap.Addr().Unmap()
	if !ip.Is4() {
		return "", ErrJoinCodeIPv6
	}
	if ap.Port() == 0 {
		return "", errors.New("a join code needs a port")
	}
	b := ip.As4()
	v4 := uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
	var bits uint64
	var n int
	if ap.Port() == JoinCodePort {
		bits, n = uint64(kindShort)<<32|v4, shortSymbols-1
	} else {
		bits, n = uint64(kindLong)<<52|v4<<16|uint64(ap.Port()), longSymbols-1
	}
	data := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		data[i] = byte(bits & 31)
		bits >>= 5
	}
	syms := append(data, checkSymbol(data))
	var sb strings.Builder
	sb.WriteString(codePrefix)
	for i, v := range syms {
		if i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[v])
	}
	return sb.String(), nil
}

// codeBody is s without its "WW" prefix and dashes, if s has the shape of
// a join code: the prefix, then only letters, digits and dashes. ok is
// false for anything else (an IP, a host name with a dot).
func codeBody(s string) (body string, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || !strings.EqualFold(s[:2], codePrefix) {
		return "", false
	}
	var sb strings.Builder
	for i := 2; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-':
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			sb.WriteByte(c)
		default:
			return "", false
		}
	}
	return sb.String(), true
}

// LooksLikeJoinCode says s is meant as a join code, valid or not: "WW",
// then 8 or 12 letters and digits with any dashes. A host name of exactly
// that shape ("ww-test-host") is read as a code too; join it by its IP.
func LooksLikeJoinCode(s string) bool {
	body, ok := codeBody(s)
	return ok && (len(body) == shortSymbols || len(body) == longSymbols)
}

// ParseJoinCode reads a join code (see the package's join code notes for
// what it forgives) and returns the address it holds.
func ParseJoinCode(s string) (netip.AddrPort, error) {
	body, ok := codeBody(s)
	if !ok || (len(body) != shortSymbols && len(body) != longSymbols) {
		return netip.AddrPort{}, ErrJoinCodeLength
	}
	syms := make([]byte, len(body))
	for i := 0; i < len(body); i++ {
		v := symbolValue[body[i]]
		if v < 0 {
			return netip.AddrPort{}, ErrJoinCodeSymbol
		}
		syms[i] = byte(v)
	}
	data, check := syms[:len(syms)-1], syms[len(syms)-1]
	if checkSymbol(data) != check {
		return netip.AddrPort{}, ErrJoinCodeTypo
	}
	var bits uint64
	for _, v := range data {
		bits = bits<<5 | uint64(v)
	}
	var v4 uint32
	var port uint16
	if len(syms) == shortSymbols {
		if bits>>32 != kindShort {
			return netip.AddrPort{}, ErrJoinCodeKind
		}
		v4, port = uint32(bits), JoinCodePort
	} else {
		if bits>>52 != kindLong {
			return netip.AddrPort{}, ErrJoinCodeKind
		}
		if bits>>48&0xF != 0 {
			return netip.AddrPort{}, ErrJoinCodeKind
		}
		v4, port = uint32(bits>>16), uint16(bits)
		if port == 0 {
			return netip.AddrPort{}, fmt.Errorf("the join code holds port 0: %w", ErrJoinCodeTypo)
		}
	}
	ip := netip.AddrFrom4([4]byte{byte(v4 >> 24), byte(v4 >> 16), byte(v4 >> 8), byte(v4)})
	return netip.AddrPortFrom(ip, port), nil
}
