package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/redact"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
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
	coop session.CoopOptions
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

	// The co-op choice (docs/08-coop-design.md; App PR 2), one per role:
	// an index into coopChoices. 0 is off for both.
	hostCoop, joinCoop int
	// worlds are the co-op worlds on disk, newest first, for a host's
	// "Continue" choices (session.Controller.CoopWorlds).
	worlds []session.CoopWorld

	// showIPs turns off the IP masking (the app model's showIPsKey): by
	// default the Host IP field draws a dot per character, like a
	// password, and an error's IPs are masked.
	showIPs bool

	// mods are the app's mods that did not load (modsNotLoaded): one
	// status line each, under the form.
	mods []session.ModStatus
}

// coopChoice is one co-op setting the connect form offers.
type coopChoice struct {
	label string
	opts  session.CoopOptions
}

// coopChoices are the role's co-op settings: off; for a host a new world
// founded from its loaded file, then each world on disk to continue; for
// a joiner on, which plays the host's world.
func (m connectModel) coopChoices() []coopChoice {
	cs := []coopChoice{{label: "Off"}}
	if m.role == 1 {
		return append(cs, coopChoice{label: "On (the host's world)", opts: session.CoopOptions{On: true}})
	}
	cs = append(cs, coopChoice{label: "New world (from the file you load)", opts: session.CoopOptions{On: true, World: session.CoopWorldNew}})
	for _, w := range m.worlds {
		if w.Err != "" {
			continue
		}
		label := fmt.Sprintf("Continue %s (seq %d)", w.ID, w.Seq)
		if w.InUse {
			label += ", in use?"
		}
		cs = append(cs, coopChoice{label: label, opts: session.CoopOptions{On: true, World: w.ID}})
	}
	return cs
}

// coopIndex is the role's choice, kept within its choices.
func (m connectModel) coopIndex() int {
	i := m.hostCoop
	if m.role == 1 {
		i = m.joinCoop
	}
	return clampInt(i, 0, len(m.coopChoices())-1)
}

// stepCoop moves the role's choice by d, wrapping.
func (m connectModel) stepCoop(d int) connectModel {
	n := len(m.coopChoices())
	i := ((m.coopIndex()+d)%n + n) % n
	if m.role == 1 {
		m.joinCoop = i
	} else {
		m.hostCoop = i
	}
	return m
}

func (m connectModel) isCoopFocused() bool { return m.focused == 1 }

func newConnect() connectModel {
	return connectModel{
		ipInput: ipPrefill,
	}
}

// focusOrder returns the field index sequence for tab navigation.
//
//	Host: role(0) -> co-op(1) -> name(2) -> connect(3)
//	Join: role(0) -> co-op(1) -> ip(2)   -> name(3) -> connect(4)
func (m connectModel) maxFocus() int {
	if m.role == 0 {
		return 3
	}
	return 4
}

func (m connectModel) isConnectFocused() bool {
	return m.focused == m.maxFocus()
}

func (m connectModel) isIPFocused() bool {
	return m.role == 1 && m.focused == 2
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
			switch {
			case m.focused == 0:
				m.role = 1 - m.role
				if m.focused > m.maxFocus() {
					m.focused = m.maxFocus()
				}
			case m.isCoopFocused() && msg.String() == "left":
				m = m.stepCoop(-1)
			case m.isCoopFocused():
				m = m.stepCoop(1)
			}

		case "enter":
			switch {
			case m.focused == 0:
				m.role = 1 - m.role
			case m.isCoopFocused():
				m = m.stepCoop(1)
			case m.isConnectFocused():
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
			m.err = "Finish the host's IP address (four numbers with dots)"
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
	coop := m.coopChoices()[m.coopIndex()].opts
	return m, func() tea.Msg {
		return connectedMsg{role: role, addr: addr, name: name, coop: coop}
	}
}

// fieldView renders a text field's value plus the cursor block, keeping
// the tail in view (behind an ellipsis) once it outgrows the box.
func fieldView(value string, boxW int) string {
	room := boxW - 2 - 1 // padding, then the cursor cell
	if r := []rune(value); len(r) > room {
		value = "…" + string(r[len(r)-(room-1):])
	}
	return value + "█"
}

// labelRow renders a field label with its hint, dropping the hint when the
// pair would wrap in width cells: a wrapped row pushes the form past the
// terminal height.
func labelRow(label, hint string, width int) string {
	row := labelStyle.Render(label)
	if withHint := row + hintStyle.Render(hint); lipgloss.Width(withHint) <= width {
		row = withHint
	}
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(row)
}

func (m connectModel) view(width int) string {
	var b strings.Builder

	// The field boxes shrink with a narrow terminal (the border adds 2
	// cells) instead of wrapping; their values scroll inside.
	boxW := clampInt(width-2, 16, fieldBoxWidth)

	b.WriteString(titleStyle.Width(width).Render("The Wind Waker"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Width(width).Render("Multiplayer"))
	b.WriteString("\n\n")

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
	b.WriteString("\n")

	// Co-op (#23), right under the role: one row, its choice cycled with
	// left/right. It costs the blank row the title used to have, so the
	// busiest form still fits 24 rows.
	b.WriteString(m.coopRow(width))
	b.WriteString("\n\n")

	// IP field (Join only). The TUI has no port field, so the hint says
	// how to reach a host on a non-default port.
	if m.role == 1 {
		b.WriteString(labelRow("Host IP", "  (add :port if it isn't 25565)", width))
		b.WriteString("\n")
		ipBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(boxW)
		if m.isIPFocused() {
			ipBox = ipBox.BorderForeground(zeldaGreen)
		}
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			ipBox.Render(fieldView(m.shownIP(), boxW)),
		))
		b.WriteString("\n\n")
	}

	// Name field (always). The hint sits on the label row so it costs no
	// height: the Join screen with an error already uses 23 of 24 rows.
	// Same-name players currently filter each other out as "self".
	b.WriteString(labelRow("Player Name", "  (each player needs a different name)", width))
	b.WriteString("\n")
	nameBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(boxW)
	if m.isNameFocused() {
		nameBox = nameBox.BorderForeground(zeldaGreen)
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		nameBox.Render(fieldView(m.nameInput, boxW)),
	))
	b.WriteString("\n\n")

	if m.err != "" {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			statusErr.Render(m.shownErr()),
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
	for _, ms := range m.mods {
		line := modLine(ms)
		if !m.showIPs {
			line = redact.Text(line)
		}
		style := statusErr
		if ms.State == "DISABLED" {
			style = helpStyle
		}
		b.WriteString(style.Width(width).Render(truncate(line, width)))
		b.WriteString("\n")
	}
	if len(m.mods) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Width(width).Render(truncate("tab: next  ←/→: toggle  enter: select  "+ipsHelp(m.showIPs)+"  ctrl+c: quit", width)))

	return b.String()
}

// shownIP is the Host IP field as drawn: a dot per character unless
// showIPs, so a stream never shows the address being typed.
func (m connectModel) shownIP() string {
	if m.showIPs {
		return m.ipInput
	}
	return strings.Repeat(redact.Dot, utf8.RuneCountInString(m.ipInput))
}

// shownErr is the error as drawn: a bad address names it, masked unless
// showIPs. The typed address is masked whole, as the field draws it, even
// when it is no IP ("10.0.0.300" is most of one).
func (m connectModel) shownErr() string {
	if m.showIPs {
		return m.err
	}
	err := m.err
	if typed := strings.TrimSpace(m.ipInput); typed != "" {
		err = strings.ReplaceAll(err, typed, strings.Repeat(redact.Dot, utf8.RuneCountInString(typed)))
	}
	return redact.Text(err)
}

// coopRow renders the co-op choice: "> Co-op  < New world >", cut to
// width.
func (m connectModel) coopRow(width int) string {
	indicator, choice := " ", labelStyle
	if m.isCoopFocused() {
		indicator, choice = ">", valueStyle.Foreground(zeldaGreen)
	}
	label := m.coopChoices()[m.coopIndex()].label
	row := labelStyle.Render(indicator+" Co-op  ") + choice.Render("◀ "+label+" ▶")
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(truncate(row, width))
}

// modsNotLoaded are ctrl's mods that are not LOADED, for the connect
// screen's status lines; none when ctrl has no mods.
func modsNotLoaded(ctrl *session.Controller) []session.ModStatus {
	if ctrl == nil {
		return nil
	}
	var out []session.ModStatus
	for _, ms := range ctrl.Mods() {
		if ms.State != session.Loaded {
			out = append(out, ms)
		}
	}
	return out
}

// modLine is a mod's status line: "Mod coop 1.0.0: DISABLED (turned off
// in settings or by a flag)".
func modLine(ms session.ModStatus) string {
	name := ms.ID
	if ms.Version != "" {
		name += " " + ms.Version
	}
	line := "Mod " + name + ": " + ms.State
	if ms.Reason != "" {
		line += " (" + ms.Reason + ")"
	}
	return line
}

// modLevel is the log level of a mod's status line: OK once it loaded,
// Warn when it was turned off, else Err.
func modLevel(ms session.ModStatus) log.Level {
	switch ms.State {
	case session.Loaded:
		return log.OK
	case "DISABLED":
		return log.Warn
	}
	return log.Err
}
