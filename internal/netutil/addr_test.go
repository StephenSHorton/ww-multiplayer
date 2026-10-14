package netutil

import (
	"strings"
	"testing"
)

func TestNormalizeAddr(t *testing.T) {
	const defaultPort = 25565

	cases := []struct {
		in   string
		want string
	}{
		{"1.2.3.4", "1.2.3.4:25565"},
		{"1.2.3.4:1234", "1.2.3.4:1234"},
		{"myhost", "myhost:25565"},
		{"myhost:1234", "myhost:1234"},
		{"[::1]", "[::1]:25565"},
		{"[::1]:1234", "[::1]:1234"},
		{"::1", "[::1]:25565"},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := NormalizeAddr(c.in, defaultPort)
			if got != c.want {
				t.Errorf("NormalizeAddr(%q, %d) = %q, want %q", c.in, defaultPort, got, c.want)
			}
		})
	}
}

func TestValidateAddr(t *testing.T) {
	valid := []string{
		"10.0.0.5",
		"192.168.1.42",
		"192.168.1.42:25566",
		" 10.0.0.5 ", // surrounding space is trimmed, like NormalizeAddr
		"::1",
		"[::1]",
		"[::1]:25566",
		"[2001:db8:85a3::8a2e:370:7334]:25565",
		"fe80::1%eth0",
		"localhost",
		"host.ts.net",
		"my-pc.local:1234",
		"DESKTOP_42",
		"1.2.3.4:65535",
	}
	for _, in := range valid {
		t.Run("ok/"+in, func(t *testing.T) {
			if err := ValidateAddr(in); err != nil {
				t.Errorf("ValidateAddr(%q) = %v, want nil", in, err)
			}
		})
	}

	invalid := []struct {
		in      string
		wantSub string // substring the error must carry
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"192.168.1.", "192.168.1."}, // the untouched TUI prefill
		{"192.168.1", "192.168.1"},
		{"10.0.0.256", "10.0.0.256"},
		{"1.2.3.4.5", "1.2.3.4.5"},
		{"42", "42"},
		{"abc def", "spaces"},
		{"abc!", "abc!"},
		{"a..b", "a..b"},
		{"-host", "-host"},
		{"host.example.", "host.example."},
		{":25565", `""`},
		{"1.2.3.4:", "port"},
		{"1.2.3.4:0", "port"},
		{"1.2.3.4:99999", "port"},
		{"1.2.3.4:+5", "port"},
		{"1.2.3.4:abc", "port"},
		{"[::1]:99999", "port"},
		{"1.2.3.4:5:6", "1.2.3.4:5:6"},
		{"[::1]x", "host:port"},
		{"http://10.0.0.5", "scheme"},
		{strings.Repeat("a", 64) + ".com", "aaaa"},
	}
	for _, c := range invalid {
		t.Run("bad/"+c.in, func(t *testing.T) {
			err := ValidateAddr(c.in)
			if err == nil {
				t.Fatalf("ValidateAddr(%q) = nil, want an error", c.in)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("ValidateAddr(%q) = %q, want it to mention %q", c.in, err, c.wantSub)
			}
		})
	}
}
