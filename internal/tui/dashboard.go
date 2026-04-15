package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
)

type tickMsg time.Time

type logEntry struct {
	time    string
	message string
	color   lipgloss.Style
}

type dashboardModel struct {
	role     string
	gameName string
	addr     string
	dolphin  *dolphin.Dolphin
	pos      *dolphin.PlayerPosition
	logs     []logEntry
	cmdInput string
	status   string
	err      string
}

func newDashboard(role, addr string) dashboardModel {
	d := dashboardModel{
		role:     role,
		gameName: "The Wind Waker",
		addr:     addr,
		status:   "Connecting...",
	}

	d.addLog("Starting as "+role+"...", logGreen)

	// Try to connect to Dolphin
	dol, err := dolphin.Find("GZLE01")
	if err != nil {
		d.addLog("Dolphin: "+err.Error(), logError)
		d.status = "Dolphin not found"
	} else {
		d.dolphin = dol
		d.addLog(fmt.Sprintf("Dolphin found! RAM at 0x%X", dol.GCRamBase()), logGreen)
		d.status = "Connected"
	}

	return d
}

func (m *dashboardModel) addLog(msg string, style lipgloss.Style) {
	m.logs = append(m.logs, logEntry{
		time:    time.Now().Format("15:04:05"),
		message: msg,
		color:   style,
	})
	if len(m.logs) > 100 {
		m.logs = m.logs[len(m.logs)-100:]
	}
}

func (m dashboardModel) tick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.dolphin != nil {
			pos, err := m.dolphin.ReadPlayerPosition()
			if err != nil {
				m.pos = nil
			} else {
				m.pos = pos
			}
		}
		return m, m.tick()

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.dolphin != nil {
				m.dolphin.Close()
			}
			return m, func() tea.Msg { return backMsg{} }

		case "enter":
			if m.cmdInput != "" {
				m.addLog("> "+m.cmdInput, logGold)
				m.processCommand(m.cmdInput)
				m.cmdInput = ""
			}

		case "backspace":
			if len(m.cmdInput) > 0 {
				m.cmdInput = m.cmdInput[:len(m.cmdInput)-1]
			}

		default:
			if len(msg.String()) == 1 {
				m.cmdInput += msg.String()
			}
		}
	}
	return m, nil
}

func (m *dashboardModel) processCommand(cmd string) {
	switch cmd {
	case "help":
		m.addLog("Commands: help, status, pos, quit", logInfo)
	case "status":
		if m.dolphin != nil {
			m.addLog("Dolphin: connected", logGreen)
		} else {
			m.addLog("Dolphin: not connected", logError)
		}
	case "pos":
		if m.pos != nil {
			m.addLog(fmt.Sprintf("X:%.1f Y:%.1f Z:%.1f", m.pos.PosX, m.pos.PosY, m.pos.PosZ), logInfo)
		} else {
			m.addLog("Position: unavailable", logError)
		}
	case "quit":
		m.addLog("Use Ctrl+C or Esc to exit", logInfo)
	default:
		m.addLog("Unknown command: "+cmd, logError)
	}
}

func (m dashboardModel) view(width, height int) string {
	if width < 60 {
		width = 60
	}

	var b strings.Builder

	// ── Header ──
	statusIcon := statusOk.Render("●")
	if m.status != "Connected" {
		statusIcon = statusErr.Render("●")
	}

	header := lipgloss.JoinHorizontal(lipgloss.Center,
		panelTitleStyle.Render(m.gameName+" - "+m.role),
		lipgloss.NewStyle().Width(width-40).Render(""),
		statusIcon+" "+lipgloss.NewStyle().Foreground(white).Render(m.status),
	)
	headerBox := lipgloss.NewStyle().
		Background(lipgloss.Color("#1B5E20")).
		Foreground(white).
		Width(width).
		Padding(0, 2).
		Render(header)

	b.WriteString(headerBox)
	b.WriteString("\n")

	// ── Position Panel ──
	var posContent string
	if m.pos != nil {
		posContent = fmt.Sprintf(
			"%s %10.1f   %s %10.1f   %s %10.1f   %s %6d",
			labelStyle.Render("X:"), m.pos.PosX,
			labelStyle.Render("Y:"), m.pos.PosY,
			labelStyle.Render("Z:"), m.pos.PosZ,
			labelStyle.Render("Rot:"), m.pos.RotY,
		)
	} else {
		posContent = labelStyle.Render("Waiting for game data...")
	}

	posPanel := panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Player Position") + "\n" + posContent,
	)
	b.WriteString(posPanel)
	b.WriteString("\n")

	// ── Log Panel ──
	logHeight := height - 14
	if logHeight < 5 {
		logHeight = 5
	}

	var logLines []string
	start := 0
	if len(m.logs) > logHeight {
		start = len(m.logs) - logHeight
	}
	for _, entry := range m.logs[start:] {
		line := logTime.Render(entry.time) + " " + entry.color.Render(entry.message)
		logLines = append(logLines, line)
	}
	for len(logLines) < logHeight {
		logLines = append(logLines, "")
	}

	logContent := strings.Join(logLines, "\n")
	logPanel := panelStyle.Width(width - 2).Height(logHeight).Render(
		panelTitleStyle.Render("Log") + "\n" + logContent,
	)
	b.WriteString(logPanel)
	b.WriteString("\n")

	// ── Command Input ──
	cmdStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(zelda_green).
		Width(width - 4).
		Padding(0, 1)

	cmdContent := labelStyle.Render("> ") + m.cmdInput + "█"
	b.WriteString(cmdStyle.Render(cmdContent))

	return b.String()
}
