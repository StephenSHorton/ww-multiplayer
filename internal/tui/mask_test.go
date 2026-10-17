package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	tea "github.com/charmbracelet/bubbletea"
)

// anyIPv4 is any dotted quad on screen.
var anyIPv4 = regexp.MustCompile(`[0-9]{1,3}(?:\.[0-9]{1,3}){3}`)

// v6Literals are the IPv6 addresses the screens below are given.
var v6Literals = []string{"fd7a:115c:a1e0::53", "2001:db8::7"}

// noIPs fails when screen shows an IP address.
func noIPs(t *testing.T, what, screen string) {
	t.Helper()
	if ip := anyIPv4.FindString(screen); ip != "" {
		t.Errorf("%s shows the IPv4 %s\n---\n%s", what, ip, screen)
	}
	for _, ip := range v6Literals {
		if strings.Contains(screen, ip) {
			t.Errorf("%s shows the IPv6 %s\n---\n%s", what, ip, screen)
		}
	}
}

// ipDashboard is a host's dashboard with addresses in the header, the log,
// the chat and (ended) the end reason.
func ipDashboard() dashboardModel {
	m := busyDashboard("Host")
	m.hostIPs = []string{"192.168.1.10", "26.4.5.6", v6Literals[0]}
	m.logs = append(m.logs[:0],
		logEntry{t: time.Unix(0, 0), level: log.Info, msg: "Host IPs: 192.168.1.10, 26.4.5.6"},
		logEntry{t: time.Unix(0, 0), level: log.Warn, msg: "dial tcp [2001:db8::7]:25565: refused at 12:34:56"},
	)
	m.chatLines = []string{"Zelda: mine is 10.0.0.5:25570", "Link: ok"}
	return m
}

// TestTUIMasksIPs: by default no screen of the TUI draws an IP address
// (owner decision 2026-10-16, streamer-safe): the dashboard's header, log,
// chat and end reason, and the connect screen's Host IP field and error.
// ctrl+r shows them, and the help line says so.
func TestTUIMasksIPs(t *testing.T) {
	m := newModel(nil, "v9.9.9")
	m.width, m.height = 120, 40
	m.screen = screenDashboard
	m.dashboard = ipDashboard()

	out := plain(m.View())
	noIPs(t, "the dashboard", out)
	for _, want := range []string{"•••.•••.•••.10", "•••.•••.•••.6", "••••:…:53", "[••••:…:7]:25565", "•••.•••.•••.5:25570", "12:34:56", "ctrl+r: show IPs"} {
		if !strings.Contains(out, want) {
			t.Errorf("the masked dashboard lacks %q\n---\n%s", want, out)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(model)
	out = plain(m.View())
	for _, want := range []string{"192.168.1.10", "26.4.5.6", v6Literals[0], "[2001:db8::7]:25565", "10.0.0.5:25570", "ctrl+r: hide IPs"} {
		if !strings.Contains(out, want) {
			t.Errorf("after ctrl+r the dashboard lacks %q\n---\n%s", want, out)
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(model)
	noIPs(t, "the dashboard after ctrl+r twice", plain(m.View()))

	// An ended session's reason.
	m.dashboard.ended, m.dashboard.endReason = true, "lost the host at 192.168.1.10:25565"
	out = plain(m.View())
	noIPs(t, "the ended dashboard", out)
	if !strings.Contains(out, "•••.•••.•••.10:25565") {
		t.Errorf("the ended dashboard lacks the masked reason\n---\n%s", out)
	}

	// A joiner's header.
	j := busyDashboard("Join")
	j.addr = "[" + v6Literals[0] + "]:25570"
	m.dashboard = j
	out = plain(m.View())
	noIPs(t, "a joiner's dashboard", out)
	if !strings.Contains(out, "→ [••••:…:53]:25570") {
		t.Errorf("a joiner's header lacks the masked address\n---\n%s", out)
	}
}

// TestTUIConnectMasksIPs: the Host IP field draws a dot per character and
// a bad address's error is masked, until ctrl+r; the toggle carries over
// to the dashboard a Join opens.
func TestTUIConnectMasksIPs(t *testing.T) {
	m := newModel(nil, "v9.9.9")
	m.width, m.height = 80, 24
	next, _ := m.Update(splashDoneMsg{})
	m = next.(model)
	// A typo'd address: its error names it, masked whole.
	m.connect.role, m.connect.ipInput, m.connect.nameInput, m.connect.focused = 1, "10.0.0.300", "Bob", 4
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	out := plain(m.View())
	if !strings.Contains(m.connect.err, "10.0.0.300") || strings.Contains(out, "10.0.0") {
		t.Errorf("the bad address error = %q; the screen shows it\n---\n%s", m.connect.err, out)
	}

	m.connect.ipInput, m.connect.focused = "192.168.1.42", 2
	m.connect.err = "dial tcp 192.168.1.42:25565: refused"
	out = plain(m.View())
	noIPs(t, "the connect screen", out)
	if !strings.Contains(out, strings.Repeat("•", len("192.168.1.42"))+"█") || !strings.Contains(out, "ctrl+r: show IPs") {
		t.Errorf("the connect screen lacks the masked field or the help\n---\n%s", out)
	}
	// Typing still goes into the field, masked.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":25570")})
	m = next.(model)
	if m.connect.ipInput != "192.168.1.42:25570" {
		t.Fatalf("typed into the masked field: %q", m.connect.ipInput)
	}
	noIPs(t, "the connect screen after typing", plain(m.View()))

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(model)
	out = plain(m.View())
	if !strings.Contains(out, "192.168.1.42:25570█") || !strings.Contains(out, "ctrl+r: hide IPs") {
		t.Errorf("after ctrl+r the connect screen lacks the field or the help\n---\n%s", out)
	}
	if m.connect.ipInput != "192.168.1.42:25570" {
		t.Fatalf("ctrl+r changed the field: %q", m.connect.ipInput)
	}
}
