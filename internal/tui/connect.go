package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// connectedMsg is emitted when the user hits Connect with valid input.
// The dashboard receives it, decides whether to start a server, and
// kicks off the multiplayer goroutines.
type connectedMsg struct {
	role string // "Host" or "Join"
	addr string // host's LAN IP for Join; ignored for Host
	name string // player display name
}

type backMsg struct{}

const (
	// ipPrefill seeds the Host IP field: it is the most common home LAN
	// prefix, so a joiner often types only the last octet.
	ipPrefill = "192.168.1."

	// ipMaxLen fits a bracketed IPv6 literal plus a port
	// ("[2001:db8:85a3::8a2e:370:7334]:25565" is 36) with room to spare.
	ipMaxLen   = 64
	nameMaxLen = 20

	// fieldBoxWidth is the Host IP and name boxes' width (padding
	// included, border not). Values longer than the box scroll.
	fieldBoxWidth = 44
)

type connectModel struct {
	role      int    // 0 = Host, 1 = Join
	ipInput   string // only used when role == 1
	nameInput string
	focused   int // see focusOrder()
	err       string
}

func newConnect() connectModel {
	return connectModel{
		ipInput: ipPrefill,
	}
}

// focusOrder returns the field index sequence for tab navigation.
//
//	Host: role(0) -> name(1) -> connect(2)
//	Join: role(0) -> ip(1)   -> name(2) -> connect(3)
func (m connectModel) maxFocus() int {
	if m.role == 0 {
		return 2
	}
	return 3
}

func (m connectModel) isConnectFocused() bool {
	return m.focused == m.maxFocus()
}

func (m connectModel) isIPFocused() bool {
	return m.role == 1 && m.focused == 1
}

// isNameFocused: the name field sits just before Connect in both roles.
func (m connectModel) isNameFocused() bool {
	return m.focused == m.maxFocus()-1
}

func (m connectModel) update(msg tea.Msg) (connectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Text first, by key type. Fast typing, an IME or a paste
		// delivers several runes in ONE KeyRunes message (a bracketed
		// paste also has Paste set and String() "[...]"), so matching on
		// msg.String() would drop it, and a pasted word such as "up"
		// would act as a navigation key. Alt+key is a shortcut, not text.
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if !msg.Alt {
				m = m.insertText(msg.Runes)
			}
			return m, nil
		}

		switch msg.String() {
		case "tab", "down":
			m.focused = (m.focused + 1) % (m.maxFocus() + 1)

		case "shift+tab", "up":
			m.focused--
			if m.focused < 0 {
				m.focused = m.maxFocus()
			}

		case "left", "right":
			if m.focused == 0 {
				m.role = 1 - m.role
				if m.focused > m.maxFocus() {
					m.focused = m.maxFocus()
				}
			}

		case "enter":
			if m.focused == 0 {
				m.role = 1 - m.role
			} else if m.isConnectFocused() {
				return m.tryConnect()
			}

		case "backspace":
			switch {
			case m.isIPFocused():
				m.ipInput = dropLastRune(m.ipInput)
			case m.isNameFocused():
				m.nameInput = dropLastRune(m.nameInput)
			}
		}
	}
	return m, nil
}

// insertText adds typed or pasted runes to the focused text field. Only
// printable ASCII without spaces is kept: that covers every IP, hostname
// and port, makes the length caps equal to the visible width, and drops
// the newlines and tabs a paste can carry. Names also lose '~', as before.
// A multi-rune paste that is a complete address by itself replaces the
// untouched ipPrefill instead of being appended to it, so pasting
// "10.0.0.5" doesn't produce "192.168.1.10.0.0.5"; pasting "42" still
// completes the prefill.
func (m connectModel) insertText(runes []rune) connectModel {
	switch {
	case m.isIPFocused():
		text := fieldText(runes, "")
		if m.ipInput == ipPrefill && len(text) > 1 && netutil.ValidateAddr(text) == nil {
			m.ipInput = ""
		}
		m.ipInput = appendCapped(m.ipInput, text, ipMaxLen)
	case m.isNameFocused():
		m.nameInput = appendCapped(m.nameInput, fieldText(runes, "~"), nameMaxLen)
	}
	return m
}

// fieldText keeps the runes of rs that are printable, non-space ASCII and
// not in reject.
func fieldText(rs []rune, reject string) string {
	var b strings.Builder
	for _, r := range rs {
		if r >= '!' && r <= '~' && !strings.ContainsRune(reject, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// appendCapped appends add to s, dropping whatever would take s past limit
// bytes. add is ASCII (see fieldText), so the cut can't split a rune.
func appendCapped(s, add string, limit int) string {
	if room := limit - len(s); len(add) > room {
		add = add[:clampInt(room, 0, len(add))]
	}
	return s + add
}

// dropLastRune removes the last rune of s (not the last byte), so
// backspace can never leave invalid UTF-8 behind.
func dropLastRune(s string) string {
	_, n := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-n]
}

func (m connectModel) tryConnect() (connectModel, tea.Cmd) {
	if m.nameInput == "" {
		m.err = "Enter a player name"
		return m, nil
	}
	addr := strings.TrimSpace(m.ipInput)
	if m.role == 1 {
		// Check the address here rather than let a typo reach the
		// dialer, where it would retry forever without a word.
		switch err := netutil.ValidateAddr(addr); {
		case addr == "":
			m.err = "Enter the host's IP address"
			return m, nil
		case strings.HasSuffix(addr, "."):
			m.err = "Finish the host's IP address (e.g. 192.168.1.42)"
			return m, nil
		case err != nil:
			m.err = "Bad address: " + err.Error()
			return m, nil
		}
	}
	m.err = ""
	role := "Host"
	if m.role == 1 {
		role = "Join"
	}
	name := m.nameInput
	return m, func() tea.Msg {
		return connectedMsg{role: role, addr: addr, name: name}
	}
}

// fieldView renders a text field's value plus the cursor block, keeping
// the tail in view (behind an ellipsis) once it outgrows the box.
func fieldView(value string) string {
	room := fieldBoxWidth - 2 - 1 // padding, then the cursor cell
	if r := []rune(value); len(r) > room {
		value = "…" + string(r[len(r)-(room-1):])
	}
	return value + "█"
}

func (m connectModel) view(width int) string {
	var b strings.Builder

	b.WriteString(titleStyle.Width(width).Render("The Wind Waker"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Width(width).Render("Multiplayer"))
	b.WriteString("\n\n\n")

	// Role toggle
	hostStyle := lipgloss.NewStyle().Padding(0, 3).Foreground(dim)
	joinStyle := lipgloss.NewStyle().Padding(0, 3).Foreground(dim)
	if m.role == 0 {
		hostStyle = hostStyle.Foreground(zeldaGreen).Bold(true).Underline(true)
	} else {
		joinStyle = joinStyle.Foreground(zeldaGreen).Bold(true).Underline(true)
	}
	indicator := " "
	if m.focused == 0 {
		indicator = ">"
	}
	roleRow := lipgloss.JoinHorizontal(lipgloss.Center,
		hostStyle.Render("Host"),
		joinStyle.Render("Join"),
	)
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		labelStyle.Render(indicator) + " " + roleRow,
	))
	b.WriteString("\n\n")

	// IP field (Join only). The TUI has no port field, so the hint says
	// how to reach a host on a non-default port.
	if m.role == 1 {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			labelStyle.Render("Host IP") + hintStyle.Render("  (add :port if it isn't 25565)"),
		))
		b.WriteString("\n")
		ipBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(fieldBoxWidth)
		if m.isIPFocused() {
			ipBox = ipBox.BorderForeground(zeldaGreen)
		}
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			ipBox.Render(fieldView(m.ipInput)),
		))
		b.WriteString("\n\n")
	}

	// Name field (always). The hint sits on the label row so it costs no
	// height: the Join screen with an error already uses 23 of 24 rows.
	// Same-name players currently filter each other out as "self".
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		labelStyle.Render("Player Name") + hintStyle.Render("  (each player needs a different name)"),
	))
	b.WriteString("\n")
	nameBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(fieldBoxWidth)
	if m.isNameFocused() {
		nameBox = nameBox.BorderForeground(zeldaGreen)
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		nameBox.Render(fieldView(m.nameInput)),
	))
	b.WriteString("\n\n")

	if m.err != "" {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			statusErr.Render(m.err),
		))
		b.WriteString("\n\n")
	}

	// Connect button
	btn := lipgloss.NewStyle().Padding(0, 4).Foreground(dim).Border(lipgloss.RoundedBorder())
	if m.isConnectFocused() {
		btn = btn.Foreground(zeldaGreen).BorderForeground(zeldaGreen).Bold(true)
	}
	label := "Start Hosting"
	if m.role == 1 {
		label = "Connect"
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		btn.Render(label),
	))

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Width(width).Render("tab: next  ←/→: toggle  enter: select  ctrl+c: quit"))

	return b.String()
}
