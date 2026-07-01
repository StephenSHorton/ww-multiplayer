package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboardModel renders the running multiplayer session: a compact status
// panel + a rolling log panel fed by the tuiReporter the session goroutines
// write to. Esc tears the session down and emits a backMsg so the app
// transitions back to connect.
type dashboardModel struct {
	role string
	name string
	addr string

	hooks Hooks // data-only accessors for the status panel (#19)

	sess *session
	logs []logEntry // ring of last logCap entries

	// Status-panel state, refreshed by statusTick (~250ms) from hooks.
	// Every source accessor is nil-checked; these hold the last read.
	playerCount int
	hostIPs     []string
	latency     time.Duration
	players     []PlayerView

	// Minimap state (#22), refreshed alongside the above on the same
	// statusTick. localOK mirrors hooks.LocalPos's ok return -- false until
	// the broadcaster's first tick resolves a position (or when the hook
	// itself is nil, e.g. CLI-only paths that never construct a dashboard).
	localX, localZ float32
	localOK        bool

	// minimapScale is read ONCE from WW_MINIMAP_SCALE at construction (see
	// newDashboard) rather than per render.
	minimapScale float32
}

const logCap = 200

// statusInterval is how often the dashboard re-reads the Hooks accessors into
// its status-panel state. 250ms is snappy enough for a presence/latency panel
// without spinning the render loop.
const statusInterval = 250 * time.Millisecond

// statusTickMsg fires every statusInterval to refresh the status panel.
type statusTickMsg struct{}

// statusTick schedules the next status refresh.
func statusTick() tea.Cmd {
	return tea.Tick(statusInterval, func(time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

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
		role:         role,
		name:         name,
		addr:         addr,
		hooks:        hooks,
		sess:         s,
		minimapScale: minimapScaleFromEnv(),
	}
}

func (m dashboardModel) initCmd() tea.Cmd {
	// Drain the log AND start the status-panel refresh ticker.
	return tea.Batch(drainLog(m.sess), statusTick())
}

// refreshStatus re-reads every non-nil status accessor into the model. Value
// receiver so it fits the (dashboardModel, tea.Cmd) update contract; returns
// the updated copy.
func (m dashboardModel) refreshStatus() dashboardModel {
	if m.hooks.PlayerCount != nil {
		m.playerCount = m.hooks.PlayerCount()
	}
	if m.hooks.HostIPs != nil {
		m.hostIPs = m.hooks.HostIPs()
	}
	if m.hooks.Latency != nil {
		m.latency = m.hooks.Latency()
	}
	if m.hooks.Players != nil {
		m.players = m.hooks.Players()
	}
	if m.hooks.LocalPos != nil {
		m.localX, _, m.localZ, m.localOK = m.hooks.LocalPos()
	}
	return m
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case logArrivedMsg:
		m.logs = append(m.logs, msg.entry)
		if len(m.logs) > logCap {
			m.logs = m.logs[len(m.logs)-logCap:]
		}
		return m, drainLog(m.sess)

	case statusTickMsg:
		m = m.refreshStatus()
		return m, statusTick()

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

	// Status panel (#19) — compact presence/latency block above the log.
	statusPanel := renderStatus(m.role, width, m.playerCount, m.hostIPs, m.latency, m.players)
	b.WriteString(statusPanel)
	b.WriteString("\n")
	statusLines := lipgloss.Height(statusPanel)

	// Minimap panel (#22) — compact top-down X-Z plot, below status and
	// above the log. Same "measure the rendered height" pattern statusPanel
	// uses, so the log panel below still gets whatever room is left.
	minimapPanel := renderMinimap(width, m.localX, m.localZ, m.localOK, m.players, m.minimapScale)
	b.WriteString(minimapPanel)
	b.WriteString("\n")
	minimapLines := lipgloss.Height(minimapPanel)

	// Log panel — fills remaining vertical space, leaving room for header
	// (1 row + newline), the status panel, the minimap panel, and footer
	// (2 rows).
	logHeight := height - 6 - statusLines - minimapLines
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

// formatLatency renders an RTT for the status panel. Per #19, a zero/unknown
// latency shows an em dash rather than a fabricated "0ms"; a sub-millisecond
// but non-zero RTT shows "<1ms" so it's clearly live.
func formatLatency(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if ms := d.Milliseconds(); ms > 0 {
		return fmt.Sprintf("%dms", ms)
	}
	return "<1ms"
}

// renderStatus builds the compact #19 status panel. It is PURE (state in,
// string out) so it can be unit-tested without a running tea.Program: feed it
// canned count/ips/latency/players and assert the output contains them.
//
//	role     "Host" shows the shareable Host IP line; "Join" omits it.
//	count    connected player count (0 renders as "0").
//	ips      host LAN IPs (host role only; ignored when empty / on join).
//	latency  client<->server RTT; 0/unknown renders "—" (never invented).
//	players  remote players; their names (or "player N") form the presence row.
func renderStatus(role string, width, count int, ips []string, latency time.Duration, players []PlayerView) string {
	if width < 60 {
		width = 60
	}

	line1 := labelStyle.Render("Players ") + valueStyle.Render(fmt.Sprintf("%d", count)) +
		labelStyle.Render("    Latency ") + valueStyle.Render(formatLatency(latency))

	lines := []string{line1}

	if role == "Host" && len(ips) > 0 {
		lines = append(lines, labelStyle.Render("Host IP ")+valueStyle.Render(strings.Join(ips, ", ")))
	}

	if len(players) > 0 {
		names := make([]string, 0, len(players))
		for _, p := range players {
			n := p.Name
			if n == "" {
				n = fmt.Sprintf("player %d", p.ID)
			}
			names = append(names, n)
		}
		lines = append(lines, labelStyle.Render("In game ")+valueStyle.Render(strings.Join(names, ", ")))
	} else {
		lines = append(lines, labelStyle.Render("In game ")+labelStyle.Render("(waiting for players…)"))
	}

	body := strings.Join(lines, "\n")
	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Status") + "\n" + body,
	)
}
