package tui

import (
	"strings"

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

type connectModel struct {
	role      int    // 0 = Host, 1 = Join
	ipInput   string // only used when role == 1
	nameInput string
	focused   int // see focusOrder()
	err       string
}

func newConnect() connectModel {
	return connectModel{
		ipInput: "192.168.1.",
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

func (m connectModel) update(msg tea.Msg) (connectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
			case m.focused == 1 && m.role == 1 && len(m.ipInput) > 0:
				m.ipInput = m.ipInput[:len(m.ipInput)-1]
			case (m.focused == 1 && m.role == 0) || (m.focused == 2 && m.role == 1):
				if len(m.nameInput) > 0 {
					m.nameInput = m.nameInput[:len(m.nameInput)-1]
				}
			}

		default:
			if len(msg.String()) == 1 {
				ch := msg.String()
				switch {
				case m.focused == 1 && m.role == 1:
					if len(m.ipInput) < 30 {
						m.ipInput += ch
					}
				case (m.focused == 1 && m.role == 0) || (m.focused == 2 && m.role == 1):
					if len(m.nameInput) < 20 && ch != " " && ch != "~" {
						m.nameInput += ch
					}
				}
			}
		}
	}
	return m, nil
}

func (m connectModel) tryConnect() (connectModel, tea.Cmd) {
	if m.nameInput == "" {
		m.err = "Enter a player name"
		return m, nil
	}
	if m.role == 1 && m.ipInput == "" {
		m.err = "Enter the host's IP address"
		return m, nil
	}
	m.err = ""
	role := "Host"
	if m.role == 1 {
		role = "Join"
	}
	addr := m.ipInput
	name := m.nameInput
	return m, func() tea.Msg {
		return connectedMsg{role: role, addr: addr, name: name}
	}
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

	// IP field (Join only)
	if m.role == 1 {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			labelStyle.Render("Host IP"),
		))
		b.WriteString("\n")
		ipBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(30)
		if m.focused == 1 {
			ipBox = ipBox.BorderForeground(zeldaGreen)
		}
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			ipBox.Render(m.ipInput + "█"),
		))
		b.WriteString("\n\n")
	}

	// Name field (always)
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		labelStyle.Render("Player Name"),
	))
	b.WriteString("\n")
	nameBox := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(30)
	nameFocus := 1
	if m.role == 1 {
		nameFocus = 2
	}
	if m.focused == nameFocus {
		nameBox = nameBox.BorderForeground(zeldaGreen)
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		nameBox.Render(m.nameInput + "█"),
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
