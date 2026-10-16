package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// joinIP is the connect form in Join mode with the Host IP field focused.
func joinIP() connectModel {
	m := newConnect()
	m.role, m.focused = 1, 2
	return m
}

// joinName is the connect form in Join mode with the name field focused.
func joinName() connectModel {
	m := newConnect()
	m.role, m.focused = 1, 3
	return m
}

func paste(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

func send(m connectModel, msgs ...tea.Msg) connectModel {
	for _, msg := range msgs {
		m, _ = m.update(msg)
	}
	return m
}

// TestConnect_PasteIntoIP: a bracketed paste, which arrives as one KeyMsg
// with many runes, lands in the field. A complete pasted address replaces
// the untouched prefill; a partial one completes it.
func TestConnect_PasteIntoIP(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"10.0.0.5", "10.0.0.5"},
		{"192.168.1.42", "192.168.1.42"},
		{"42", "192.168.1.42"},
		{" 10.0.0.5\r\n", "10.0.0.5"}, // whitespace and newline dropped
		{"[2001:db8:85a3::8a2e:370:7334]:25565", "[2001:db8:85a3::8a2e:370:7334]:25565"},
	}
	for _, c := range cases {
		if got := send(joinIP(), paste(c.in)).ipInput; got != c.want {
			t.Errorf("paste %q into the prefilled IP field = %q, want %q", c.in, got, c.want)
		}
	}

	// Once edited, the field is appended to like normal typing.
	m := joinIP()
	m.ipInput = "10.0."
	if got := send(m, paste("0.5")).ipInput; got != "10.0.0.5" {
		t.Errorf("paste into an edited IP field = %q, want %q", got, "10.0.0.5")
	}
}

// TestConnect_MultiRuneTyping: an unbracketed burst of runes (fast typing or
// a terminal without bracketed paste) is inserted too.
func TestConnect_MultiRuneTyping(t *testing.T) {
	m := joinIP()
	m.ipInput = ""
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("10.0")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".0.5")})
	if m.ipInput != "10.0.0.5" {
		t.Errorf("multi-rune typing = %q, want %q", m.ipInput, "10.0.0.5")
	}
}

// TestConnect_PasteIntoName: names keep printable ASCII minus space and '~'.
func TestConnect_PasteIntoName(t *testing.T) {
	if got := send(joinName(), paste("Zoë Link~\tX")).nameInput; got != "ZoLinkX" {
		t.Errorf("paste into name = %q, want %q", got, "ZoLinkX")
	}
	if got := send(joinName(), paste(strings.Repeat("a", 50))).nameInput; len(got) != nameMaxLen {
		t.Errorf("pasted name is %d chars, want the %d cap", len(got), nameMaxLen)
	}
}

// TestConnect_IPCap: the IP field holds 64 characters, enough for a
// bracketed IPv6 literal with a port.
func TestConnect_IPCap(t *testing.T) {
	m := joinIP()
	m.ipInput = ""
	if got := send(m, paste(strings.Repeat("1", 100))).ipInput; len(got) != ipMaxLen {
		t.Errorf("IP field holds %d chars, want %d", len(got), ipMaxLen)
	}
}

// TestConnect_PastedWordIsText: a pasted or grouped "up" is text, not the
// up-arrow; Alt+key is ignored rather than typed.
func TestConnect_PastedWordIsText(t *testing.T) {
	m := send(joinName(), paste("up"), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("down")})
	if m.nameInput != "updown" || m.focused != 3 {
		t.Errorf("name=%q focus=%d, want %q and focus unchanged", m.nameInput, m.focused, "updown")
	}
	m = send(joinName(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	if m.nameInput != "" {
		t.Errorf("alt+b was typed into the name field: %q", m.nameInput)
	}
}

// TestConnect_BackspaceRuneSafe: backspace removes a whole rune, so a
// multi-byte value can't be left as invalid UTF-8.
func TestConnect_BackspaceRuneSafe(t *testing.T) {
	m := joinName()
	m.nameInput = "Zoë"
	m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.nameInput != "Zo" || !utf8.ValidString(m.nameInput) {
		t.Errorf("backspace on %q = %q, want %q", "Zoë", m.nameInput, "Zo")
	}
	m = send(joinIP(), tea.KeyMsg{Type: tea.KeyBackspace})
	if m.ipInput != "192.168.1" {
		t.Errorf("backspace on the IP prefill = %q, want %q", m.ipInput, "192.168.1")
	}
	m = send(connectModel{role: 1, focused: 2}, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.ipInput != "" {
		t.Errorf("backspace on an empty field = %q, want empty", m.ipInput)
	}
}

// TestConnect_TryConnectValidatesAddress: Join checks the address before
// connecting, with the error shown inline; valid forms connect.
func TestConnect_TryConnectValidatesAddress(t *testing.T) {
	bad := []struct {
		ip, wantErr string
	}{
		{"192.168.1.", "Finish the host's IP address"},
		{"", "Enter the host's IP address"},
		{"1.2.3.4:99999", "Bad address"},
		{"192.168.1", "Bad address"},
		{"abc!", "Bad address"},
	}
	for _, c := range bad {
		m := connectModel{role: 1, ipInput: c.ip, nameInput: "Bob", focused: 4}
		m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil {
			t.Errorf("ip %q: connected anyway", c.ip)
		}
		if !strings.Contains(m.err, c.wantErr) {
			t.Errorf("ip %q: err = %q, want it to contain %q", c.ip, m.err, c.wantErr)
		}
	}

	for _, ip := range []string{"10.0.0.5", "[::1]:25566", "::1", "host.ts.net"} {
		m := connectModel{role: 1, ipInput: ip, nameInput: "Bob", focused: 4, err: "stale"}
		m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Errorf("ip %q: rejected with %q", ip, m.err)
			continue
		}
		msg, ok := cmd().(connectedMsg)
		if !ok || msg.role != "Join" || msg.addr != ip || msg.name != "Bob" || m.err != "" {
			t.Errorf("ip %q: got %+v (err %q), want a Join connectedMsg for it", ip, msg, m.err)
		}
	}
}

// TestConnect_HostIgnoresIPField: hosting never looks at the (hidden) IP
// field, so the untouched prefill doesn't block it.
func TestConnect_HostIgnoresIPField(t *testing.T) {
	m := newConnect()
	m.nameInput, m.focused = "Link", 3
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Host with the default IP prefill did not connect")
	}
	if msg := cmd().(connectedMsg); msg.role != "Host" || msg.name != "Link" {
		t.Errorf("got %+v, want a Host connectedMsg", msg)
	}
}

// TestConnect_ViewHintAndFit: the different-names hint is on screen, a long
// value scrolls inside its box, and the busiest form (Join + an error) still
// fits an 80x24 terminal.
func TestConnect_ViewHintAndFit(t *testing.T) {
	m := joinIP()
	m.ipInput = strings.Repeat("a", ipMaxLen)
	m.err = "Bad address: \"192.168.1.\" is not a valid IP address or hostname"
	out := m.view(80)

	if !strings.Contains(plain(out), "different name") {
		t.Errorf("connect view missing the different-names hint\n---\n%s", plain(out))
	}
	if h := lipgloss.Height(out); h > 24 {
		t.Errorf("Join form with an error is %d rows, want <= 24", h)
	}
	if w := lipgloss.Width(out); w > 80 {
		t.Errorf("connect view is %d cells wide, want <= 80", w)
	}
	if !strings.Contains(plain(out), "…"+strings.Repeat("a", 10)) {
		t.Errorf("a value longer than its box should scroll behind an ellipsis\n---\n%s", plain(out))
	}
}

// TestConnect_ViewNarrow: on a terminal narrower than the field boxes, the
// boxes shrink and the hints drop instead of wrapping, so the form keeps
// its 80-column height and never runs past the right edge.
func TestConnect_ViewNarrow(t *testing.T) {
	m := joinIP()
	m.ipInput = strings.Repeat("a", ipMaxLen)
	m.nameInput = "Link"
	wantH := lipgloss.Height(m.view(80))
	for _, w := range []int{60, 46, 40, 30, 20} {
		out := m.view(w)
		if got := lipgloss.Width(out); got > w {
			t.Errorf("width %d: view is %d cells wide\n---\n%s", w, got, plain(out))
		}
		if got := lipgloss.Height(out); got != wantH {
			t.Errorf("width %d: view is %d rows, want %d (nothing wrapped)\n---\n%s", w, got, wantH, plain(out))
		}
		if !strings.Contains(plain(out), "Link█") {
			t.Errorf("width %d: name field lost its value\n---\n%s", w, plain(out))
		}
	}
	if !strings.Contains(plain(m.view(60)), "different name") {
		t.Error("the different-names hint should still show at 60 columns")
	}
}
