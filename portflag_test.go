package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestExtractPortFlag covers both flag spellings in any position, and that a
// bad value or an unknown "--" option is an error instead of silently
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

// TestShareAddrs: on the default port the host prints bare IPs (join adds
// the port); on any other port it must print ip:port, or a joiner who types
// what they were given dials 25565 and retries forever.
func TestShareAddrs(t *testing.T) {
	ips := []string{"192.168.1.42", "10.0.0.5", "fd00::1"}
	if got := shareAddrs(ips, defaultPort); !reflect.DeepEqual(got, ips) {
		t.Errorf("shareAddrs(default port) = %q, want the bare IPs %q", got, ips)
	}
	want := []string{"192.168.1.42:25566", "10.0.0.5:25566", "[fd00::1]:25566"}
	if got := shareAddrs(ips, 25566); !reflect.DeepEqual(got, want) {
		t.Errorf("shareAddrs(25566) = %q, want %q", got, want)
	}
	if got := shareAddrs(nil, 25566); len(got) != 0 {
		t.Errorf("shareAddrs(nil) = %q, want empty", got)
	}
}
