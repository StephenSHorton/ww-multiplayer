package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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

// TestChatScrollback asserts the pure scrollback renderer: it surfaces recent
// lines, always emits exactly `rows` lines (blank-padded when short), and keeps
// only the newest lines when the history overflows the window.
func TestChatScrollback(t *testing.T) {
	// Short history: both lines present, padded to the requested row count.
	out := chatScrollback([]string{"Link: hi", "Zelda: hey"}, 4)
	if got := strings.Count(out, "\n") + 1; got != 4 {
		t.Errorf("chatScrollback padded to %d rows, want 4:\n%q", got, out)
	}
	for _, want := range []string{"Link: hi", "Zelda: hey"} {
		if !strings.Contains(out, want) {
			t.Errorf("chatScrollback missing %q\n---\n%q", want, out)
		}
	}

	// Overflow: only the newest `rows` lines survive.
	full := []string{"a: 1", "b: 2", "c: 3", "d: 4", "e: 5"}
	tail := chatScrollback(full, 2)
	if strings.Count(tail, "\n")+1 != 2 {
		t.Errorf("chatScrollback(rows=2) should emit 2 lines, got:\n%q", tail)
	}
	if !strings.Contains(tail, "d: 4") || !strings.Contains(tail, "e: 5") {
		t.Errorf("chatScrollback tail should keep the newest lines, got:\n%q", tail)
	}
	if strings.Contains(tail, "a: 1") {
		t.Errorf("chatScrollback tail leaked an evicted line:\n%q", tail)
	}
}

// TestChatSendPath_ForwardsAndEchoes: pressing Enter with text forwards it to
// hooks.SendChat, echoes it locally as "name: text", and clears the input.
func TestChatSendPath_ForwardsAndEchoes(t *testing.T) {
	var got string
	called := 0
	m := dashboardModel{
		name:      "Me",
		hooks:     Hooks{SendChat: func(text string) error { called++; got = text; return nil }},
		chatInput: textinput.New(),
	}
	m.chatInput.SetValue("hello world")

	m2, _ := m.update(tea.KeyMsg{Type: tea.KeyEnter})

	if called != 1 || got != "hello world" {
		t.Fatalf("SendChat forwarded (%d, %q), want (1, %q)", called, got, "hello world")
	}
	if m2.chatInput.Value() != "" {
		t.Errorf("input not cleared after send: %q", m2.chatInput.Value())
	}
	echoed := false
	for _, l := range m2.chatLines {
		if l == "Me: hello world" {
			echoed = true
		}
	}
	if !echoed {
		t.Errorf("local echo missing; chatLines=%v", m2.chatLines)
	}
}

// TestChatSendPath_NilSendChatSafe: a nil SendChat hook must be a safe no-op —
// no panic, input cleared, and nothing echoed (there was no send).
func TestChatSendPath_NilSendChatSafe(t *testing.T) {
	m := dashboardModel{name: "Me", hooks: Hooks{}, chatInput: textinput.New()}
	m.chatInput.SetValue("hi")

	m2, _ := m.update(tea.KeyMsg{Type: tea.KeyEnter})

	if m2.chatInput.Value() != "" {
		t.Errorf("input not cleared on no-op send: %q", m2.chatInput.Value())
	}
	if len(m2.chatLines) != 0 {
		t.Errorf("nil SendChat should not echo; chatLines=%v", m2.chatLines)
	}
}

// TestChatArrivedAppends: an incoming chat line is appended to the scrollback.
func TestChatArrivedAppends(t *testing.T) {
	m := dashboardModel{chatInput: textinput.New()}
	m2, _ := m.update(chatArrivedMsg{line: "Zelda: hi there"})
	if len(m2.chatLines) != 1 || m2.chatLines[0] != "Zelda: hi there" {
		t.Fatalf("chatArrivedMsg not appended; chatLines=%v", m2.chatLines)
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
