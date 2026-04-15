package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type connectedMsg struct {
	role string
	addr string
}

type backMsg struct{}

type connectModel struct {
	role      int // 0=server, 1=client
	ipInput   string
	nameInput string
	focused   int // 0=role, 1=ip, 2=name(client only), 3=connect
	err       string
}

func newConnect() connectModel {
	return connectModel{
		ipInput: "localhost",
	}
}

func (m connectModel) update(msg tea.Msg) (connectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			m.focused++
			max := 2
			if m.role == 1 {
				max = 3
			}
			if m.focused > max {
				m.focused = 0
			}

		case "shift+tab", "up":
			m.focused--
			if m.focused < 0 {
				if m.role == 1 {
					m.focused = 3
				} else {
					m.focused = 2
				}
			}

		case "left", "right":
			if m.focused == 0 {
				m.role = 1 - m.role
				// Reset focus if switching to server and focused on name field
				if m.role == 0 && m.focused > 2 {
					m.focused = 2
				}
			}

		case "enter":
			if m.focused == 0 {
				m.role = 1 - m.role
			} else if m.isConnectFocused() {
				return m.tryConnect()
			}

		case "backspace":
			if m.focused == 1 && len(m.ipInput) > 0 {
				m.ipInput = m.ipInput[:len(m.ipInput)-1]
			} else if m.focused == 2 && m.role == 1 && len(m.nameInput) > 0 {
				m.nameInput = m.nameInput[:len(m.nameInput)-1]
			}

		default:
			if len(msg.String()) == 1 {
				ch := msg.String()
				if m.focused == 1 {
					m.ipInput += ch
				} else if m.focused == 2 && m.role == 1 {
					if len(m.nameInput) < 20 && ch != " " && ch != "~" {
						m.nameInput += ch
					}
				}
			}
		}
	}
	return m, nil
}

func (m connectModel) isConnectFocused() bool {
	return (m.focused == 2 && m.role == 0) || (m.focused == 3 && m.role == 1)
}

func (m connectModel) tryConnect() (connectModel, tea.Cmd) {
	if m.ipInput == "" {
		m.err = "Enter an IP address"
		return m, nil
	}
	if m.role == 1 && m.nameInput == "" {
		m.err = "Enter a player name"
		return m, nil
	}
	m.err = ""
	role := "Server"
	if m.role == 1 {
		role = "Client"
	}
	addr := m.ipInput
	return m, func() tea.Msg {
		return connectedMsg{role: role, addr: addr}
	}
}

func (m connectModel) view(width int) string {
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Width(width).Render("The Wind Waker"))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Width(width).Render("Multiplayer"))
	b.WriteString("\n\n\n")

	// Role toggle
	serverStyle := lipgloss.NewStyle().Padding(0, 3).Foreground(dim)
	clientStyle := lipgloss.NewStyle().Padding(0, 3).Foreground(dim)
	if m.role == 0 {
		serverStyle = serverStyle.Foreground(zelda_green).Bold(true).Underline(true)
	} else {
		clientStyle = clientStyle.Foreground(zelda_green).Bold(true).Underline(true)
	}

	indicator := " "
	if m.focused == 0 {
		indicator = ">"
	}
	roleRow := lipgloss.JoinHorizontal(lipgloss.Center,
		serverStyle.Render("Server"),
		clientStyle.Render("Client"),
	)
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		labelStyle.Render(indicator) + " " + roleRow,
	))
	b.WriteString("\n\n")

	// IP input
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		labelStyle.Render("IP Address"),
	))
	b.WriteString("\n")
	ipStyle := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(30)
	if m.focused == 1 {
		ipStyle = ipStyle.BorderForeground(zelda_green)
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		ipStyle.Render(m.ipInput + "█"),
	))
	b.WriteString("\n\n")

	// Player name (client only)
	if m.role == 1 {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			labelStyle.Render("Player Name"),
		))
		b.WriteString("\n")
		nameStyle := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(30)
		if m.focused == 2 {
			nameStyle = nameStyle.BorderForeground(zelda_green)
		}
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			nameStyle.Render(m.nameInput + "█"),
		))
		b.WriteString("\n\n")
	}

	// Error
	if m.err != "" {
		b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
			statusErr.Render(m.err),
		))
		b.WriteString("\n\n")
	}

	// Connect button
	btnStyle := lipgloss.NewStyle().Padding(0, 4).Foreground(dim).Border(lipgloss.RoundedBorder())
	if m.isConnectFocused() {
		btnStyle = btnStyle.Foreground(zelda_green).BorderForeground(zelda_green).Bold(true)
	}
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		btnStyle.Render("Connect"),
	))

	b.WriteString("\n\n")
	b.WriteString(helpStyle.Width(width).Render("tab: next  arrows: toggle  enter: select  ctrl+c: quit"))

	return b.String()
}
