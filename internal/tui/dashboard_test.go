package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// ansiRE strips the SGR escape sequences lipgloss injects so substring
// assertions match the human-visible text (styling wraps values, but a Width /
// Foreground style can split a "label: value" pair with a reset code, so we
// compare on the de-styled string).
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// TestRenderStatus_Host asserts the pure #19 status renderer surfaces the
// player count, the host IPs, a formatted latency, and remote player names.
func TestRenderStatus_Host(t *testing.T) {
	out := plain(renderStatus(
		"Host",
		100,
		3,
		[]string{"192.168.1.10", "10.0.0.5"},
		42*time.Millisecond,
		[]PlayerView{{ID: 2, Name: "Zelda"}, {ID: 3, Name: "Ganon"}},
	))

	for _, want := range []string{
		"Status",       // panel title
		"Players",      // count label
		"3",            // the count
		"Latency",      // latency label
		"42ms",         // formatted RTT
		"Host IP",      // host-only line
		"192.168.1.10", // first IP verbatim
		"10.0.0.5",     // second IP verbatim
		"Zelda",        // remote name
		"Ganon",        // remote name
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderStatus(host) output missing %q\n---\n%s", want, out)
		}
	}
}

// TestRenderStatus_JoinZeroLatency asserts join role omits the Host IP line and
// that a zero/unknown latency renders the em dash rather than a fabricated
// "0ms" (the explicit #19 requirement).
func TestRenderStatus_JoinZeroLatency(t *testing.T) {
	out := plain(renderStatus(
		"Join",
		100,
		2,
		[]string{"192.168.1.10"}, // ips supplied but must be ignored on join
		0,                        // unknown latency
		[]PlayerView{{ID: 1, Name: "Link"}},
	))

	if strings.Contains(out, "Host IP") {
		t.Errorf("renderStatus(join) unexpectedly rendered the Host IP line\n---\n%s", out)
	}
	if strings.Contains(out, "192.168.1.10") {
		t.Errorf("renderStatus(join) leaked a host IP into the join view\n---\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("renderStatus(join) with zero latency should render an em dash, got:\n%s", out)
	}
	if strings.Contains(out, "0ms") {
		t.Errorf("renderStatus(join) fabricated a 0ms latency instead of the em dash\n---\n%s", out)
	}
	if !strings.Contains(out, "Link") {
		t.Errorf("renderStatus(join) missing remote name %q\n---\n%s", "Link", out)
	}
}

// TestRenderStatus_NoPlayers asserts the waiting-state presence line and that a
// nameless remote falls back to a stable "player N" label.
func TestRenderStatus_NoPlayers(t *testing.T) {
	out := plain(renderStatus("Host", 100, 1, nil, 0, nil))
	if !strings.Contains(out, "waiting for players") {
		t.Errorf("renderStatus with no players missing the waiting hint:\n%s", out)
	}

	named := plain(renderStatus("Join", 100, 2, nil, 5*time.Millisecond, []PlayerView{{ID: 7}}))
	if !strings.Contains(named, "player 7") {
		t.Errorf("renderStatus should label a nameless remote as %q\n---\n%s", "player 7", named)
	}
}

// TestFormatLatency covers the em-dash / sub-ms / ms branches.
func TestFormatLatency(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{500 * time.Microsecond, "<1ms"},
		{42 * time.Millisecond, "42ms"},
		{1500 * time.Millisecond, "1500ms"},
	}
	for _, c := range cases {
		if got := formatLatency(c.in); got != c.want {
			t.Errorf("formatLatency(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
