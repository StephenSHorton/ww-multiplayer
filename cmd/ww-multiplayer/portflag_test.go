package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// TestExtractPortFlag covers every flag spelling in any position, and that
// a bad value or an unknown "-"/"--" option is an error instead of silently
// falling back to the default port or becoming the player name.
func TestExtractPortFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPort int
		wantRest []string
		wantErr  string // substring; "" = no error
	}{
		{"absent", []string{"Alice"}, defaultPort, []string{"Alice"}, ""},
		{"no args", nil, defaultPort, []string{}, ""},
		{"space form first", []string{"--port", "25566", "Alice"}, 25566, []string{"Alice"}, ""},
		{"space form last", []string{"Alice", "--port", "25566"}, 25566, []string{"Alice"}, ""},
		{"equals form", []string{"--port=25567", "Alice"}, 25567, []string{"Alice"}, ""},
		{"equals form last", []string{"Alice", "--port=1"}, 1, []string{"Alice"}, ""},
		{"max port", []string{"--port", "65535"}, 65535, []string{}, ""},
		{"later flag wins", []string{"--port", "2000", "--port=3000"}, 3000, []string{}, ""},
		{"missing value", []string{"Alice", "--port"}, 0, nil, "needs a value"},
		{"empty equals", []string{"--port="}, 0, nil, "invalid port"},
		{"non-numeric", []string{"--port", "abc"}, 0, nil, `invalid port "abc"`},
		{"zero", []string{"--port", "0"}, 0, nil, "invalid port"},
		{"negative", []string{"--port=-1"}, 0, nil, "invalid port"},
		{"too big", []string{"--port", "65536"}, 0, nil, "invalid port"},
		{"flag as value", []string{"--port", "--verbose"}, 0, nil, "invalid port"},
		{"unknown option", []string{"--name", "Alice"}, 0, nil, `unknown option "--name"`},
		{"typo option", []string{"--prot=25566", "Alice"}, 0, nil, "unknown option"},
		{"single-dash form", []string{"-port", "25566", "Alice"}, 25566, []string{"Alice"}, ""},
		{"single-dash equals", []string{"Alice", "-port=25567"}, 25567, []string{"Alice"}, ""},
		{"single-dash bad value", []string{"-port", "abc"}, 0, nil, "invalid port"},
		{"single-dash missing value", []string{"-port"}, 0, nil, "needs a value"},
		{"single-dash unknown", []string{"-p", "25566"}, 0, nil, `unknown option "-p"`},
		{"single-dash typo", []string{"-portt=25566"}, 0, nil, `unknown option "-portt=25566"`},
		{"lone dash is positional", []string{"-"}, defaultPort, []string{"-"}, ""},
		{"-- ends options", []string{"--port", "25566", "--", "-Alice-"}, 25566, []string{"-Alice-"}, ""},
		{"-- keeps later flags positional", []string{"--", "--port", "1"}, defaultPort, []string{"--port", "1"}, ""},
		{"-- alone", []string{"Alice", "--"}, defaultPort, []string{"Alice"}, ""},
		{"dash name hints at --", []string{"-Alice"}, 0, nil, "put -- before a name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port, rest, err := extractPortFlag(c.args, defaultPort)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("extractPortFlag(%q) error = %v, want one containing %q", c.args, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractPortFlag(%q) error = %v", c.args, err)
			}
			if port != c.wantPort || !reflect.DeepEqual(rest, c.wantRest) {
				t.Errorf("extractPortFlag(%q) = (%d, %q), want (%d, %q)", c.args, port, rest, c.wantPort, c.wantRest)
			}
		})
	}
}

func TestParseJoinArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantAddr string
		wantName string
		wantErr  string // substring; "" = no error
	}{
		{"bare ip", []string{"192.168.1.42"}, "192.168.1.42:25565", "", ""},
		{"ip and name", []string{"192.168.1.42", "Bob"}, "192.168.1.42:25565", "Bob", ""},
		{"ip:port", []string{"192.168.1.42:25566", "Bob"}, "192.168.1.42:25566", "Bob", ""},
		{"--port applies", []string{"192.168.1.42", "Bob", "--port", "25566"}, "192.168.1.42:25566", "Bob", ""},
		{"--port= first", []string{"--port=25566", "192.168.1.42"}, "192.168.1.42:25566", "", ""},
		{"--port matches addr", []string{"192.168.1.42:25566", "--port", "25566"}, "192.168.1.42:25566", "", ""},
		{"bare ipv6", []string{"::1"}, "[::1]:25565", "", ""},
		{"ipv6 + --port", []string{"::1", "--port", "25566"}, "[::1]:25566", "", ""},
		{"bracketed ipv6 port", []string{"[::1]:25566"}, "[::1]:25566", "", ""},
		{"no args", nil, "", "", "missing the host's address"},
		{"only a flag", []string{"--port", "25566"}, "", "", "missing the host's address"},
		{"flag never a name", []string{"192.168.1.42", "--prot", "25566"}, "", "", "unknown option"},
		{"conflicting ports", []string{"192.168.1.42:25566", "--port", "25567"}, "", "", "give the port once"},
		{"addr port out of range", []string{"1.2.3.4:99999"}, "", "", "invalid port"},
		{"addr port non-numeric", []string{"1.2.3.4:abc"}, "", "", "invalid port"},
		{"empty host", []string{":25566"}, "", "", "missing the host"},
		{"bad --port", []string{"1.2.3.4", "--port", "x"}, "", "", "invalid port"},
		{"-port applies", []string{"1.2.3.4", "-port", "25566"}, "1.2.3.4:25566", "", ""},
		{"-port never a name", []string{"1.2.3.4", "-prot", "25566"}, "", "", "unknown option"},
		{"bare port as 3rd arg", []string{"1.2.3.4", "Bob", "25566"}, "", "", `unexpected argument "25566" (did you mean --port 25566?)`},
		{"extra word", []string{"1.2.3.4", "Bob", "Carol"}, "", "", `unexpected argument "Carol"`},
		{"dash name after --", []string{"1.2.3.4", "--", "-Bob"}, "1.2.3.4:25565", "-Bob", ""},
		{"name too long", []string{"1.2.3.4", strings.Repeat("b", 33)}, "", "", "max 32"},
		{"name with control char", []string{"1.2.3.4", "Bo\tb"}, "", "", "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, name, err := parseJoinArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("parseJoinArgs(%q) error = %v, want one containing %q", c.args, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseJoinArgs(%q) error = %v", c.args, err)
			}
			if addr != c.wantAddr || name != c.wantName {
				t.Errorf("parseJoinArgs(%q) = (%q, %q), want (%q, %q)", c.args, addr, name, c.wantAddr, c.wantName)
			}
		})
	}
}

// TestParseHostArgs: `host` takes at most a name, so a port typed without
// the flag is an error instead of being dropped while hosting on 25565.
func TestParseHostArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantName string
		wantPort int
		wantErr  string // substring; "" = no error
	}{
		{"no args", nil, "", defaultPort, ""},
		{"name", []string{"Alice"}, "Alice", defaultPort, ""},
		{"name + --port", []string{"Alice", "--port", "25566"}, "Alice", 25566, ""},
		{"-port + name", []string{"-port", "25566", "Alice"}, "Alice", 25566, ""},
		{"bare port after name", []string{"Alice", "25566"}, "", 0, `unexpected argument "25566" (did you mean --port 25566?)`},
		{"two names", []string{"Alice", "Bob"}, "", 0, `unexpected argument "Bob"`},
		{"-port never a name", []string{"-port"}, "", 0, "needs a value"},
		{"dash name after --", []string{"--port", "25566", "--", "-Alice"}, "-Alice", 25566, ""},
		{"name at max length", []string{strings.Repeat("a", 32)}, strings.Repeat("a", 32), defaultPort, ""},
		{"name too long", []string{strings.Repeat("a", 33)}, "", 0, "max 32"},
		{"name not UTF-8", []string{"Al\xffce"}, "", 0, "not valid UTF-8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, port, err := parseHostArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("parseHostArgs(%q) error = %v, want one containing %q", c.args, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHostArgs(%q) error = %v", c.args, err)
			}
			if name != c.wantName || port != c.wantPort {
				t.Errorf("parseHostArgs(%q) = (%q, %d), want (%q, %d)", c.args, name, port, c.wantName, c.wantPort)
			}
		})
	}
}

// TestParseServerArgs: `server` takes no positional args at all; the
// owner key comes from --owner-key, else WW_OWNER_KEY, both trimmed (the
// app trims the key it sends), and is checked; with neither, the server
// makes one (KeyGenerated). An empty --owner-key is an error, never a
// silent "no key" over WW_OWNER_KEY.
func TestParseServerArgs(t *testing.T) {
	const key = "0123456789abcdef"
	const made = "generated-0123456789abcdef"
	cases := []struct {
		name     string
		args     []string
		env      string // WW_OWNER_KEY
		wantPort int
		wantKey  string
		wantErr  string // substring; "" = no error
	}{
		{"no args", nil, "", defaultPort, made, ""},
		{"--port", []string{"--port", "25566"}, "", 25566, made, ""},
		{"--port=", []string{"--port=25566"}, "", 25566, made, ""},
		{"-port", []string{"-port", "25566"}, "", 25566, made, ""},
		{"bare port", []string{"25566"}, "", 0, "", `unexpected argument "25566" (did you mean --port 25566?)`},
		{"stray word", []string{"now"}, "", 0, "", `unexpected argument "now"`},
		{"bad value", []string{"--port", "99999"}, "", 0, "", "invalid port"},
		{"--owner-key", []string{"--owner-key", key, "--port", "25570"}, "", 25570, key, ""},
		{"--owner-key=", []string{"--owner-key=" + key}, "", defaultPort, key, ""},
		{"flag trimmed", []string{"--owner-key", "  " + key + "\n"}, "", defaultPort, key, ""},
		{"flag= trimmed", []string{"--owner-key=" + key + " "}, "", defaultPort, key, ""},
		{"env", nil, "  " + key + "\n", defaultPort, key, ""},
		{"blank env", nil, " \n", defaultPort, made, ""},
		{"flag over env", []string{"--owner-key", key}, "other-key-xyz-0123", defaultPort, key, ""},
		{"no value", []string{"--owner-key"}, "", 0, "", "--owner-key needs a value"},
		{"empty =", []string{"--owner-key="}, key, 0, "", "--owner-key is empty"},
		{"empty arg", []string{"--owner-key", ""}, key, 0, "", "--owner-key is empty"},
		{"blank arg", []string{"--owner-key", "  "}, key, 0, "", "--owner-key is empty"},
		{"short", []string{"--owner-key", "abcdefgh"}, "", 0, "", "use at least 16"},
		{"short env", nil, "abcdefgh", 0, "", "use at least 16"},
		{"long", []string{"--owner-key", strings.Repeat("k", 257)}, "", 0, "", "at most 256"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := parseServerArgs(c.args, func(k string) string {
				if k == "WW_OWNER_KEY" {
					return c.env
				}
				return ""
			}, func() (string, error) { return made, nil })
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("parseServerArgs(%q) error = %v, want one containing %q", c.args, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServerArgs(%q) error = %v", c.args, err)
			}
			if o.Port != c.wantPort || o.OwnerKey != c.wantKey || o.KeyGenerated != (c.wantKey == made) {
				t.Errorf("parseServerArgs(%q) = port %d key %q generated %v, want %d %q", c.args, o.Port, o.OwnerKey, o.KeyGenerated, c.wantPort, c.wantKey)
			}
		})
	}
	// The real generator: a fresh 128-bit key per start.
	o1, err := parseServerArgs(nil, func(string) string { return "" }, network.GenerateOwnerKey)
	o2, _ := parseServerArgs(nil, func(string) string { return "" }, network.GenerateOwnerKey)
	if err != nil || len(o1.OwnerKey) != 32 || !o1.KeyGenerated || o1.OwnerKey == o2.OwnerKey {
		t.Fatalf("generated keys %q %q (%v)", o1.OwnerKey, o2.OwnerKey, err)
	}
}

// TestShareAddrs: on the default port the host prints bare IPs (join adds
// the port); on any other port it must print ip:port, or a joiner who types
// what they were given dials 25565 and retries forever.
func TestShareAddrs(t *testing.T) {
	ips := []string{"192.168.1.42", "10.0.0.5", "fd00::1"}
	if got := engine.ShareAddrs(ips, defaultPort); !reflect.DeepEqual(got, ips) {
		t.Errorf("engine.ShareAddrs(default port) = %q, want the bare IPs %q", got, ips)
	}
	want := []string{"192.168.1.42:25566", "10.0.0.5:25566", "[fd00::1]:25566"}
	if got := engine.ShareAddrs(ips, 25566); !reflect.DeepEqual(got, want) {
		t.Errorf("engine.ShareAddrs(25566) = %q, want %q", got, want)
	}
	if got := engine.ShareAddrs(nil, 25566); len(got) != 0 {
		t.Errorf("engine.ShareAddrs(nil) = %q, want empty", got)
	}
}
