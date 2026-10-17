package netutil

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"testing"
)

var codeShape = regexp.MustCompile(`^WW-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4})?$`)

func TestJoinCodeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"192.168.1.42:25565", "26.12.34.56:25565", "0.0.0.0:25565", "255.255.255.255:25565",
		"10.0.0.7:25570", "127.0.0.1:1", "100.64.1.2:65535", "255.255.255.255:65535", "0.0.0.0:1",
	} {
		ap := netip.MustParseAddrPort(s)
		code, err := JoinCode(ap)
		if err != nil {
			t.Fatalf("JoinCode(%s): %v", s, err)
		}
		if !codeShape.MatchString(code) {
			t.Errorf("JoinCode(%s) = %q: not the code shape", s, code)
		}
		long := ap.Port() != JoinCodePort
		if (len(code) == len("WW-XXXX-XXXX-XXXX")) != long {
			t.Errorf("JoinCode(%s) = %q: wrong length for its port", s, code)
		}
		got, err := ParseJoinCode(code)
		if err != nil || got != ap {
			t.Errorf("ParseJoinCode(%q) = %v, %v; want %v", code, got, err, ap)
		}
		if !LooksLikeJoinCode(code) {
			t.Errorf("LooksLikeJoinCode(%q) = false", code)
		}
	}
}

func TestJoinCodeRefuses(t *testing.T) {
	if _, err := JoinCode(netip.MustParseAddrPort("[fd00::1]:25565")); !errors.Is(err, ErrJoinCodeIPv6) {
		t.Errorf("IPv6: %v", err)
	}
	if _, err := JoinCode(netip.AddrPortFrom(netip.MustParseAddr("10.0.0.1"), 0)); err == nil {
		t.Error("port 0 was encoded")
	}
	// An IPv4-mapped IPv6 is its IPv4.
	c1, _ := JoinCode(netip.MustParseAddrPort("[::ffff:10.0.0.1]:25565"))
	c2, _ := JoinCode(netip.MustParseAddrPort("10.0.0.1:25565"))
	if c1 != c2 {
		t.Errorf("mapped %q != %q", c1, c2)
	}
}

// TestJoinCodeParseVariants: lowercase, no dashes, extra dashes, spaces
// around, and Crockford's confusables (I, L for 1, O for 0).
func TestJoinCodeParseVariants(t *testing.T) {
	ap := netip.MustParseAddrPort("192.168.1.10:25565")
	code, _ := JoinCode(ap)
	variants := []string{
		code,
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		strings.ToLower(strings.ReplaceAll(code, "-", "")),
		"  " + code + "\t",
		strings.ReplaceAll(code, "-", "--"),
		"ww" + code[2:],
	}
	for _, v := range variants {
		if got, err := ParseJoinCode(v); err != nil || got != ap {
			t.Errorf("ParseJoinCode(%q) = %v, %v", v, got, err)
		}
	}
	// Confusables: find codes with a 1 and a 0 and type them as I/L and O.
	found1, found0 := false, false
	for i := 0; i < 4000 && !(found1 && found0); i++ {
		ap := netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1}), 25565)
		c, _ := JoinCode(ap)
		body := c[3:]
		if strings.Contains(body, "1") {
			found1 = true
			for _, r := range []string{"I", "i", "L", "l"} {
				if got, err := ParseJoinCode("WW-" + strings.ReplaceAll(body, "1", r)); err != nil || got != ap {
					t.Errorf("%q with 1 as %s: %v, %v", c, r, got, err)
				}
			}
		}
		if strings.Contains(body, "0") {
			found0 = true
			for _, r := range []string{"O", "o"} {
				if got, err := ParseJoinCode("WW-" + strings.ReplaceAll(body, "0", r)); err != nil || got != ap {
					t.Errorf("%q with 0 as %s: %v, %v", c, r, got, err)
				}
			}
		}
	}
	if !found1 || !found0 {
		t.Fatal("no code with a 1 and a 0 found")
	}
}

// TestJoinCodeTypos: every single-symbol change and every swap of two
// neighbouring symbols is caught, for both lengths.
func TestJoinCodeTypos(t *testing.T) {
	for _, s := range []string{"192.168.1.42:25565", "26.200.3.4:25570", "10.0.0.1:25565", "172.16.254.3:40000"} {
		ap := netip.MustParseAddrPort(s)
		code, _ := JoinCode(ap)
		body := []byte(strings.ReplaceAll(code[3:], "-", ""))
		for i := range body {
			for _, c := range []byte(codeAlphabet) {
				if c == body[i] {
					continue
				}
				typo := append([]byte(nil), body...)
				typo[i] = c
				if got, err := ParseJoinCode("WW" + string(typo)); err == nil {
					t.Errorf("%s: symbol %d as %c read as %v", code, i, c, got)
				}
			}
			if i+1 < len(body) && body[i] != body[i+1] {
				swap := append([]byte(nil), body...)
				swap[i], swap[i+1] = swap[i+1], swap[i]
				_, err := ParseJoinCode("WW" + string(swap))
				if !errors.Is(err, ErrJoinCodeTypo) {
					t.Errorf("%s: swapping %d and %d: %v", code, i, i+1, err)
				}
			}
		}
	}
}

func TestJoinCodeMalformed(t *testing.T) {
	for _, s := range []string{
		"", "WW", "WW-", "WW-K7Q2", "WW-K7Q2-9XH", "WW-K7Q2-9XHM-1", "XX-K7Q2-9XHM",
		"WW-K7Q2-9XH!", "WW-K7Q2-9XHU", "WW K7Q2 9XHM", "192.168.1.42", "ww-multiplayer",
		"WW-K7Q2-9XHM-K7Q2-9XHM", "WW.K7Q2.9XHM",
	} {
		if got, err := ParseJoinCode(s); err == nil {
			t.Errorf("ParseJoinCode(%q) = %v, want an error", s, got)
		}
	}
	for s, want := range map[string]bool{
		"WW-K7Q2-9XHM": true, "wwk7q29xhm": true, "WW-AAAA-BBBB-CCCC": true,
		"WW-K7Q2-9XH": false, "ww-multiplayer": false, "192.168.1.42": false, "WW.K7Q2.9XHM": false, "": false,
	} {
		if LooksLikeJoinCode(s) != want {
			t.Errorf("LooksLikeJoinCode(%q) = %v", s, !want)
		}
	}
}

// TestJoinCodeInAddr: join (NormalizeAddr) and the app's Join box
// (ValidateAddr) take a code; a code with a typo is refused without
// echoing it.
func TestJoinCodeInAddr(t *testing.T) {
	code, _ := JoinCode(netip.MustParseAddrPort("192.168.1.42:25570"))
	if got := NormalizeAddr(strings.ToLower(code), 25565); got != "192.168.1.42:25570" {
		t.Errorf("NormalizeAddr(code) = %q", got)
	}
	short, _ := JoinCode(netip.MustParseAddrPort("10.1.2.3:25565"))
	if got := NormalizeAddr(short, 9999); got != "10.1.2.3:25565" {
		t.Errorf("NormalizeAddr(short code) = %q", got)
	}
	if err := ValidateAddr(code); err != nil {
		t.Errorf("ValidateAddr(code) = %v", err)
	}
	bad := code[:len(code)-1] + string(codeAlphabet[(strings.IndexByte(codeAlphabet, code[len(code)-1])+1)%32])
	err := ValidateAddr(bad)
	if !errors.Is(err, ErrJoinCodeTypo) || strings.Contains(err.Error(), bad[3:]) {
		t.Errorf("ValidateAddr(typo) = %v", err)
	}
	// Host names and IPs are unchanged.
	if got := NormalizeAddr("ww-multiplayer.lan", 25565); got != "ww-multiplayer.lan:25565" {
		t.Errorf("host name: %q", got)
	}
}

func FuzzParseJoinCode(f *testing.F) {
	for _, s := range []string{"WW-K7Q2-9XHM", "ww-0000-0000-0000", "WW", "wwilol0000", "WW-ZZZZ-ZZZZ-ZZZZ", "WW-\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ap, err := ParseJoinCode(s)
		if err != nil {
			return
		}
		if !LooksLikeJoinCode(s) {
			t.Fatalf("%q parsed but doesn't look like a code", s)
		}
		code, err := JoinCode(ap)
		if err != nil {
			t.Fatalf("%q parsed to %v, which doesn't encode: %v", s, ap, err)
		}
		back, err := ParseJoinCode(code)
		if err != nil || back != ap {
			t.Fatalf("%q -> %v -> %q -> %v, %v", s, ap, code, back, err)
		}
		if NormalizeAddr(s, 1) != ap.String() {
			t.Fatalf("NormalizeAddr(%q) = %q", s, NormalizeAddr(s, 1))
		}
	})
}
