package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
)

// TestJoinArgsJoinCode: the CLI's join takes a join code (the app's share
// card shows one), in any case and without dashes, and dials the address
// and port it holds; a --port that disagrees with the code's is refused,
// as for an ip:port.
func TestJoinArgsJoinCode(t *testing.T) {
	for _, args := range [][]string{
		{"WW-460N-00JM-RZ2Q", "Medli"},
		{"ww460n00jmrz2q", "Medli"},
		{"WW-460N-00JM-RZ2Q", "Medli", "--port", "25570"},
	} {
		addr, name, err := parseJoinArgs(args)
		if err != nil || addr != "192.168.1.42:25570" || name != "Medli" {
			t.Errorf("parseJoinArgs(%q) = %q, %q, %v", args, addr, name, err)
		}
	}
	if addr, _, err := parseJoinArgs([]string{"WW-30AG-09AX"}); err != nil || addr != "192.168.1.42:25565" {
		t.Errorf("short code: %q, %v", addr, err)
	}
	if _, _, err := parseJoinArgs([]string{"WW-460N-00JM-RZ2Q", "--port", "26000"}); err == nil || !strings.Contains(err.Error(), "--port") {
		t.Errorf("a code with another --port: %v", err)
	}
	// A code with a typo is refused with the reason, not dialed as a host
	// name; the error doesn't repeat the code.
	for _, typo := range []string{"WW-30AG-09AZ", "ww30ag09az", "WW-460N-00JM-RZ2R"} {
		addr, _, err := parseJoinArgs([]string{typo, "Medli"})
		if !errors.Is(err, netutil.ErrJoinCodeTypo) || addr != "" || strings.Contains(err.Error(), "30AG") || strings.Contains(err.Error(), "460N") {
			t.Errorf("typo %q: %q, %v", typo, addr, err)
		}
	}
}
