package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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

	// Chat panel state (#20). chatInput is the bubbles textinput row; chatLines
	// is a ring of the last chatCap rendered lines ("name: text"); chatCh is the
	// incoming-line channel captured once from hooks.ChatCh() (nil when chat is
	// not wired, e.g. in tests).
	chatInput textinput.Model
	chatLines []string
	chatCh    <-chan string

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

// chatCap bounds the chat scrollback ring; chatVisibleRows is how many of those
// lines the chat panel shows at once.
const (
	chatCap         = 50
	chatVisibleRows = 6
)

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

// chatArrivedMsg wraps one incoming chat line pulled off hooks.ChatCh().
type chatArrivedMsg struct{ line string }

// drainChat mirrors drainLog for the chat channel: it blocks on ch and emits
// one chatArrivedMsg per line, re-issued in Update so the channel is drained
// continuously without stalling the UI. Returns nil (no Cmd) when chat isn't
// wired, so the dashboard simply has no chat feed.
func drainChat(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return chatArrivedMsg{line: line}
	}
}

// appendChat pushes a line onto the bounded chat ring, trimming to chatCap.
func appendChat(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > chatCap {
		lines = lines[len(lines)-chatCap:]
	}
	return lines
}

// chatScrollback renders the tail of the chat ring into exactly `rows` lines
// (older lines dropped, blank-padded when short), joined by newlines. PURE:
// lines + rows in, string out — so it can be unit-tested without a tea.Program.
func chatScrollback(lines []string, rows int) string {
	if rows < 1 {
		rows = 1
	}
	start := 0
	if len(lines) > rows {
		start = len(lines) - rows
	}
	visible := append([]string(nil), lines[start:]...)
	for len(visible) < rows {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}

func newDashboard(hooks Hooks, role, name, addr string) dashboardModel {
	s := startSession(hooks, role, name, addr)

	ti := textinput.New()
	ti.Placeholder = "Type a message, Enter to send…"
	ti.Prompt = "> "
	ti.CharLimit = 200
	ti.Focus()

	// Capture the incoming-chat channel once (nil when chat isn't wired). It is
	// stable for the liveState's lifetime, so a single capture is safe.
	var chatCh <-chan string
	if hooks.ChatCh != nil {
		chatCh = hooks.ChatCh()
	}

	return dashboardModel{
		role:         role,
		name:         name,
		addr:         addr,
		hooks:        hooks,
		sess:         s,
		minimapScale: minimapScaleFromEnv(),
		chatInput:    ti,
		chatCh:       chatCh,
	}
}

func (m dashboardModel) initCmd() tea.Cmd {
	// Drain the log + chat channels, start the status-panel ticker, and start
	// the textinput cursor blink.
	return tea.Batch(drainLog(m.sess), drainChat(m.chatCh), statusTick(), textinput.Blink)
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

	case chatArrivedMsg:
		m.chatLines = appendChat(m.chatLines, msg.line)
		return m, drainChat(m.chatCh)

	case statusTickMsg:
		m = m.refreshStatus()
		return m, statusTick()

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Stop the session and pop back to connect. stop() is
			// synchronous so the next host attempt finds :25565 free.
			// (textinput never consumes Esc, so this always fires even while
			// the chat input is focused.)
			m.sess.stop()
			return m, func() tea.Msg { return backMsg{} }

		case "enter":
			text := strings.TrimSpace(m.chatInput.Value())
			m.chatInput.SetValue("")
			if text == "" {
				return m, nil
			}
			if m.hooks.SendChat != nil {
				// Fire-and-forget: a send error (e.g. not connected yet) just
				// means the line doesn't go out; we still echo it locally.
				_ = m.hooks.SendChat(text)
				// Local echo: the server excludes the sender's own connection
				// from the relay (and the client self-filters its own name),
				// so a sent line never comes back — show it directly.
				m.chatLines = appendChat(m.chatLines, m.name+": "+text)
			}
			return m, nil
		}
		// Any other key is text input for the chat row.
		var cmd tea.Cmd
		m.chatInput, cmd = m.chatInput.Update(msg)
		return m, cmd
	}

	// Non-key messages (e.g. the textinput cursor blink) still drive the input.
	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
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

	// Chat panel (#20) — scrollback + an input row. Built here so its measured
	// height can be subtracted from the log's; it is written to the output
	// AFTER the log panel (see below), so it sits just above the footer.
	m.chatInput.Width = width - 8
	chatPanel := panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Chat") + "\n" +
			chatScrollback(m.chatLines, chatVisibleRows) + "\n" +
			m.chatInput.View(),
	)
	chatLines := lipgloss.Height(chatPanel)

	// Log panel — fills the remaining vertical space, leaving room for the
	// header (1 row + newline), the status panel, the minimap panel, the chat
	// panel, and the footer (2 rows). Recompute against ALL measured panel
	// heights (like #40) so nothing overlaps.
	logHeight := height - 6 - statusLines - minimapLines - chatLines
	if logHeight < 4 {
		logHeight = 4
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

	b.WriteString(chatPanel)
	b.WriteString("\n")

	// Footer
	footer := helpStyle.Width(width).Render(
		"type + enter: chat    esc: back to connect    ctrl+c: quit",
	)
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
