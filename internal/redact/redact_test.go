package redact

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
)

// TestAddr: every IPv4 has one shape and every IPv6 another, whatever
// the octets' or groups' lengths; only the last octet or group and the
// port show.
func TestAddr(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.1.42", "•••.•••.•••.42"},
		{"10.0.0.7", "•••.•••.•••.7"},
		{"192.168.1.42:25570", "•••.•••.•••.42:25570"},
		{"26.12.34.56", "•••.•••.•••.56"},
		{"::ffff:10.0.0.7", "•••.•••.•••.7"},
		{"[::ffff:10.0.0.7]:25565", "•••.•••.•••.7:25565"},
		{"fd7a:115c:a1e0::53", "••••:…:53"},
		{"2001:db8:85a3::8a2e:370:7334", "••••:…:7334"},
		{"fe80::", "••••:…:0"},
		{"fe80::1a2b%eth0", "••••:…:1a2b"},
		{"[fd7a:115c:a1e0::53]", "••••:…:53"},
		{"[fd7a:115c:a1e0::53]:25570", "[••••:…:53]:25570"},
		{"localhost", "localhost"},
		{"localhost:25570", "localhost:25570"},
		{"host.ts.net", "host.ts.net"},
		{"", ""},
		// What doesn't parse as an address still shows no more than a
		// last group: a host name with an IP in it, a typo, a partial.
		{"26.12.34.56.nip.io:25570", "•••.•••.•••.56.nip.io:25570"},
		{"x.26.12.34.56.nip.io", "x.•••.•••.•••.56.nip.io"},
		{"26.12.34.56x", "•••.•••.•••.56x"},
		{"26-12-34-56.sslip.io:25570", "•••.•••.•••.56.sslip.io:25570"},
		{"26.12.34.566", "•••.•••.•••.566"},
		{"26.12.34.566:25565", "•••.•••.•••.566:25565"},
		{"192.168.001.042", "•••.•••.•••.042"},
		{"192.168.1.42;25565", "•••.•••.•••.42;25565"},
		{"192.168.1.42/25565", "•••.•••.•••.42/25565"},
		{"26.12.34", "•••.•••.•••.34"},
		{"26.12.34:25565", "•••.•••.•••.34:25565"},
		{"ww3.example.com:25570", "ww3.example.com:25570"},
	} {
		if got := Addr(c.in); got != c.want {
			t.Errorf("Addr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTextMasks: every IP form in a free line is masked.
func TestTextMasks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"dial tcp 192.168.1.42:25565: connectex: refused", "dial tcp •••.•••.•••.42:25565: connectex: refused"},
		{"Joining the lobby at 10.0.0.2:25570 as \"Medli\".", "Joining the lobby at •••.•••.•••.2:25570 as \"Medli\"."},
		{"try 26.1.2.3", "try •••.•••.•••.3"},
		{"at 26.1.2.3.", "at •••.•••.•••.3."},
		{"(192.168.1.42)", "(•••.•••.•••.42)"},
		{"addr=192.168.1.42,", "addr=•••.•••.•••.42,"},
		{"host:192.168.1.42", "host:•••.•••.•••.42"},
		{"Host IPs: 192.168.1.42, 26.5.6.7", "Host IPs: •••.•••.•••.42, •••.•••.•••.7"},
		{"dial tcp [fd7a::53]:25565: refused", "dial tcp [••••:…:53]:25565: refused"},
		{"dial tcp [fd7a:115c:a1e0::53]:25565: i/o timeout", "dial tcp [••••:…:53]:25565: i/o timeout"},
		{"listening on [::]:25565", "listening on [••••:…:0]:25565"},
		{"try 2001:db8:85a3::8a2e:370:7334 now", "try ••••:…:7334 now"},
		{"dial tcp fd00::1: refused", "dial tcp ••••:…:1: refused"},
		{"at fe80::1a2b%eth0.", "at ••••:…:1a2b."},
		{"loopback ::1", "loopback ••••:…:1"},
		{"mapped ::ffff:10.0.0.7 here", "mapped •••.•••.•••.7 here"},
		{"bare fd00::1:25565", "bare ••••:…:1:25565"},
		{"prefix fe80:: on eth0", "prefix ••••:…:0 on eth0"},
		{"both 1.2.3.4 and [2001:db8::9]:7", "both •••.•••.•••.4 and [••••:…:9]:7"},
		{"chat: 192.168.0.5 is me", "chat: •••.•••.•••.5 is me"},
		// An address glued to a word by a colon, with a port.
		{"host:192.168.1.42:25565", "host:•••.•••.•••.42:25565"},
		{"host:10.1.2.3:25565 is mine", "host:•••.•••.•••.3:25565 is mine"},
		{"ip:1.2.3.4:25565", "ip:•••.•••.•••.4:25565"},
		{"addr:fd00::1", "addr:••••:…:1"},
		{"Medli:fd7a::53", "Medli:••••:…:53"},
		{"ip=::1", "ip=••••:…:1"},
		// Near addresses: a typo keeps three real octets, a host name
		// carries a whole IP.
		{"dial tcp: lookup 26.12.34.566: no such host", "dial tcp: lookup •••.•••.•••.566: no such host"},
		{"Joining the lobby at 26.12.34.566:25565", "Joining the lobby at •••.•••.•••.566:25565"},
		{"999.1.1.1 is no IP", "•••.•••.•••.1 is no IP"},
		{"01.02.03.004", "•••.•••.•••.004"},
		{"at 192.168.001.042.", "at •••.•••.•••.042."},
		{"26.12.3456.7", "•••.•••.•••.7"},
		{"lookup 26.12.34.56.nip.io: refused", "lookup •••.•••.•••.56.nip.io: refused"},
		{"lookup x.26.12.34.56.nip.io", "lookup x.•••.•••.•••.56.nip.io"},
		{"see 26-12-34-56.sslip.io", "see •••.•••.•••.56.sslip.io"},
	} {
		if got := Text(c.in); got != c.want {
			t.Errorf("Text(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestTextLeaves: what only looks like an address stays as it is.
func TestTextLeaves(t *testing.T) {
	for _, in := range []string{
		"12:34:56",
		"at 12:34:56 the host left",
		"2026-10-16T12:34:56.789Z INFO ready",
		"12:34:56.789",
		"10:30",
		"ratio 3:2",
		"version v0.3.0 vs 0.2.9",
		"ww-multiplayer v1.2.3.4",
		"go1.26.2",
		"build 1.2.3.4.5",
		"1.2.3.4.5.6.7.8",
		"Windows 10.0.26200",
		"2026-10-16 and 2026-10-16-12",
		"room 12-34",
		"ww3.example.com",
		"std::map and a::b",
		"add:: cafe::",
		":: alone",
		"mac 00:11:22:33:44:55",
		"mac aa:bb:cc:dd:ee:ff",
		"port :25565",
		"x1.2.3.4",
		"1.2.3.4x",
		"192.168.1.",
		"hello world",
		"",
	} {
		if got := Text(in); got != in {
			t.Errorf("Text(%q) = %q, want it unchanged", in, got)
		}
	}
}

// TestTextFixedShape: two IPv4s of different octet lengths mask to the
// same width, so the mask gives nothing away but the last octet.
func TestTextFixedShape(t *testing.T) {
	a, b := Text("1.2.3.9"), Text("255.255.255.9")
	if a != b {
		t.Errorf("Text masks differ by octet length: %q vs %q", a, b)
	}
	a, b = Text("[1::9]:1"), Text("[ffff:ffff:ffff:ffff:ffff:ffff:ffff:9]:1")
	if a != b {
		t.Errorf("Text masks differ by group length: %q vs %q", a, b)
	}
}

// TestCodes: a join code holds an IP and a port, so Addr and Text mask it
// whole, in any case and with or without dashes, valid or with a typo;
// words that only start with "ww" stay.
func TestCodes(t *testing.T) {
	short, err := netutil.JoinCode(netip.MustParseAddrPort("192.168.1.42:25565"))
	if err != nil {
		t.Fatal(err)
	}
	long, _ := netutil.JoinCode(netip.MustParseAddrPort("26.12.34.56:25570"))
	const sm, lm = "WW-••••-••••", "WW-••••-••••-••••"
	for in, want := range map[string]string{
		short:                             sm,
		long:                              lm,
		strings.ToLower(short):            sm,
		strings.ReplaceAll(long, "-", ""): lm,
		short[:len(short)-1] + "Z":        sm, // a typo is masked too
		"WW-AAAA-BBBB":                    sm,
	} {
		if got := Addr(in); got != want {
			t.Errorf("Addr(%q) = %q, want %q", in, got, want)
		}
		line := "join code " + in + ", copied."
		if got := Text(line); got != "join code "+want+", copied." {
			t.Errorf("Text(%q) = %q", line, got)
		}
	}
	if got := Text("codes " + short + " and " + long); got != "codes "+sm+" and "+lm {
		t.Errorf("two codes: %q", got)
	}
	for _, in := range []string{"ww-multiplayer", "WW-multiplayer v0.3.0", "wwwwwwwwwwwwwwwwwwwwwwwwwwww", "WWII", "aWW-AAAA-BBBB", "WW-AAAA-BBBBx", "ww3.example.com"} {
		if got := Text(in); got != in {
			t.Errorf("Text(%q) = %q, want it unchanged", in, got)
		}
	}
	if Code(short) != sm || Code(long) != lm {
		t.Error("Code shapes")
	}
}
