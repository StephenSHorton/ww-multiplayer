package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboardModel renders the running multiplayer session: a status header
// and a rolling log panel fed by the tuiReporter the session goroutines
// write to. Esc tears the session down and emits a backMsg so the app
// transitions back to connect.
type dashboardModel struct {
	role string
	name string
	addr string

	sess *session
	logs []logEntry // ring of last logCap entries
}

const logCap = 200

// logArrivedMsg wraps a single log entry pulled off the session's logCh.
// We re-issue the drain cmd inside Update on each receive so the channel
// is continuously consumed without blocking Bubble Tea's main loop.
type logArrivedMsg struct{ entry logEntry }

// drainLog returns a tea.Cmd that blocks on the session's logCh and emits
// one logArrivedMsg per message. Bubble Tea schedules these on a worker
// goroutine, so the blocking wait does not stall the UI.
func drainLog(s *session) tea.Cmd {
	return func() tea.Msg {
		entry, ok := <-s.logCh
		if !ok {
			return nil
		}
		return logArrivedMsg{entry: entry}
	}
}

func newDashboard(hooks Hooks, role, name, addr string) dashboardModel {
	s := startSession(hooks, role, name, addr)
	return dashboardModel{
		role: role,
		name: name,
		addr: addr,
		sess: s,
	}
}

func (m dashboardModel) initCmd() tea.Cmd {
	return drainLog(m.sess)
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case logArrivedMsg:
		m.logs = append(m.logs, msg.entry)
		if len(m.logs) > logCap {
			m.logs = m.logs[len(m.logs)-logCap:]
		}
		return m, drainLog(m.sess)

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Stop the session and pop back to connect. stop() is
			// synchronous so the next host attempt finds :25565 free.
			m.sess.stop()
			return m, func() tea.Msg { return backMsg{} }
		}
	}
	return m, nil
}

func (m dashboardModel) view(width, height int) string {
	if width < 60 {
		width = 60
	}
	if height < 20 {
		height = 20
	}

	var b strings.Builder

	// Header
	roleLabel := m.role
	if m.role == "Host" {
		roleLabel = "Hosting"
	} else {
		roleLabel = "Joining"
	}
	target := m.addr
	if m.role == "Host" {
		target = ":25565"
	}
	headerLeft := panelTitleStyle.Render(roleLabel) + " " +
		valueStyle.Render(m.name) + " " +
		labelStyle.Render("→ "+target)
	header := lipgloss.NewStyle().
		Background(zeldaDark).
		Foreground(white).
		Width(width).
		Padding(0, 2).
		Render(headerLeft)
	b.WriteString(header)
	b.WriteString("\n")

	// Log panel — fills remaining vertical space, leaving room for header
	// (3 rows including border) and footer (2 rows).
	logHeight := height - 6
	if logHeight < 8 {
		logHeight = 8
	}

	// Tail the log ring to the visible window
	start := 0
	if len(m.logs) > logHeight {
		start = len(m.logs) - logHeight
	}
	visible := m.logs[start:]
	var lines []string
	for _, e := range visible {
		ts := logTime.Render(e.t.Format("15:04:05"))
		lines = append(lines, ts+" "+e.styled().Render(e.msg))
	}
	for len(lines) < logHeight {
		lines = append(lines, "")
	}
	logBody := strings.Join(lines, "\n")
	logPanel := panelStyle.Width(width - 2).Height(logHeight).Render(
		panelTitleStyle.Render("Log") + "\n" + logBody,
	)
	b.WriteString(logPanel)
	b.WriteString("\n")

	// Footer
	footer := helpStyle.Width(width).Render(fmt.Sprintf(
		"esc: back to connect    ctrl+c: quit",
	))
	b.WriteString(footer)

	return b.String()
}
