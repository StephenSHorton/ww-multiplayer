package redact

import "testing"

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
		"999.1.1.1 is no IP",
		"01.02.03.004",
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
