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

	// ended is set by the sessionEndedMsg for THIS dashboard's session:
	// the header, status panel and footer then say so, with endReason.
	ended     bool
	endReason string
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

// sessionEndedMsg reports that a session goroutine returned. It names the
// session it belongs to, so a late one from a session the user already left
// (Esc, then Host/Join again) can't mark the new dashboard as ended.
type sessionEndedMsg struct {
	sess   *session
	reason string
}

// waitSessionEnd blocks until the session goroutine returns and reports why.
// Like drainLog, Bubble Tea runs it on a worker goroutine.
func waitSessionEnd(s *session) tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg {
		<-s.done
		return sessionEndedMsg{sess: s, reason: s.endReason()}
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
// (older lines dropped, blank-padded when short), joined by newlines. A line
// wider than `width` cells wraps within those rows instead of growing the
// panel. PURE: lines + rows + width in, string out — so it can be unit-tested
// without a tea.Program.
func chatScrollback(lines []string, rows, width int) string {
	if rows < 1 {
		rows = 1
	}
	// Every line takes at least one row, so only the newest `rows` matter.
	start := 0
	if len(lines) > rows {
		start = len(lines) - rows
	}
	visible := make([]string, 0, len(lines)-start)
	for _, l := range lines[start:] {
		visible = append(visible, displayText(l))
	}
	return strings.Join(wrapTail(visible, rows, width), "\n")
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
	// Drain the log + chat channels, start the status-panel ticker, start
	// the textinput cursor blink, and watch for the session ending.
	return tea.Batch(drainLog(m.sess), drainChat(m.chatCh), statusTick(), textinput.Blink, waitSessionEnd(m.sess))
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

	case sessionEndedMsg:
		if msg.sess != nil && msg.sess == m.sess {
			m.ended = true
			m.endReason = msg.reason
		}
		return m, nil

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
			if text == "" || m.ended {
				// Nothing to send, or no session left to send it on.
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

// Dashboard sizing. Until the first WindowSizeMsg the size is unknown, so the
// view assumes a classic 80x24. Narrower than dashMinWidth the panels stop
// making sense; they render at that width and the renderer clips the right.
const (
	dashDefaultWidth  = 80
	dashDefaultHeight = 24
	dashMinWidth      = 30
)

// Row budget for the panels between the status panel and the footer. A
// panel's chrome is its border (top + bottom) and title row; the chat panel
// adds its input row.
const (
	logChrome  = 3
	chatChrome = 4
	mapChrome  = 3

	logMinRows     = 3 // log lines kept whenever the terminal has room
	logComfortRows = 6 // log lines filled before the minimap gets any room
	chatMinRows    = 2
	minimapMinRows = 5 // a shorter grid isn't worth drawing; hide it instead

	// minimapPanelWidth is a full-width grid plus border and padding. When
	// the log keeps besideMinLogWidth columns next to it (a 120-column
	// Windows console), the minimap sits beside the log instead of
	// costing rows above it.
	minimapPanelWidth = minimapCols + 4
	besideMinLogWidth = 60
)

// dashLayout is planLayout's row split. logRows 0 hides the log panel and
// chat false hides the chat panel (tiny terminals only). mapRows is the
// minimap's body height (0 hides it); beside puts it right of the log panel,
// at the log's height.
type dashLayout struct {
	logRows  int
	chat     bool
	chatRows int
	mapRows  int
	beside   bool
}

// planLayout splits avail rows (everything under the header and status panel
// and above the footer) between the log, chat and minimap panels, in priority
// order: a minimum log and chat, a comfortable log and chat, the minimap
// (shrunk to fit, or hidden), then the rest to the log. When even the
// minimums don't fit, chat scrollback goes first, then log rows down to one,
// then the whole chat panel and finally the log panel, so no panel is ever
// drawn half-cut. PURE, so it can be unit-tested.
func planLayout(width, avail int, localOK bool) dashLayout {
	l := dashLayout{logRows: logMinRows, chat: true, chatRows: chatMinRows}
	spare := avail - (logMinRows + logChrome) - (chatMinRows + chatChrome)
	for spare < 0 && l.chatRows > 0 {
		l.chatRows--
		spare++
	}
	for spare < 0 && l.logRows > 1 {
		l.logRows--
		spare++
	}
	if spare < 0 {
		l.chat = false
		spare += chatChrome
	}
	if spare < 0 {
		return dashLayout{}
	}
	take := func(want int) int {
		n := clampInt(want, 0, spare)
		spare -= n
		return n
	}
	l.logRows += take(logComfortRows - l.logRows)
	if l.chat {
		l.chatRows += take(chatVisibleRows - l.chatRows)
	}

	// Body rows the minimap needs: one for "no position yet", or a grid.
	mapNeed := minimapMinRows
	if !localOK {
		mapNeed = 1
	}

	if width-minimapPanelWidth >= besideMinLogWidth {
		l.logRows += take(spare)
		if l.logRows >= mapNeed {
			l.mapRows = l.logRows
			l.beside = true
		}
		return l
	}
	if spare >= mapNeed+mapChrome {
		rows := 1
		if localOK {
			rows = min(minimapRows, spare-mapChrome)
			if rows%2 == 0 {
				rows-- // odd, so self gets a center row
			}
		}
		l.mapRows = rows
		spare -= rows + mapChrome
	}
	l.logRows += take(spare)
	return l
}

func (m dashboardModel) view(width, height int) string {
	if width <= 0 {
		width = dashDefaultWidth
	}
	if height <= 0 {
		height = dashDefaultHeight
	}
	w := max(width, dashMinWidth)

	// Header, then status (#19), always: the header carries the role and
	// the Host IPs to share, so it must never be what overflow pushes off.
	header := renderHeader(w, m.role, m.name, m.addr, m.hostIPs, m.ended)
	var status string
	if m.ended {
		status = renderEnded(w, m.endReason)
	} else {
		status = renderStatus(w, m.playerCount, m.latency, m.players)
	}
	help := "type + enter: chat    esc: back to connect    ctrl+c: quit"
	if m.ended {
		help = "esc: back to connect (your fields are kept)    ctrl+c: quit"
	}
	footer := helpStyle.Width(w).Render(truncate(help, w))

	avail := height - lipgloss.Height(header) - lipgloss.Height(status) - lipgloss.Height(footer)
	l := planLayout(w, avail, m.localOK)

	parts := []string{header, status}

	// Log panel, with the minimap (#22) above it or beside it.
	if l.logRows > 0 {
		logW := w
		if l.beside {
			logW = w - minimapPanelWidth
		}
		logPanel := renderLogPanel(logW, l.logRows, m.logs)
		switch {
		case l.mapRows > 0 && l.beside:
			mapPanel := renderMinimap(minimapPanelWidth, l.mapRows, m.localX, m.localZ, m.localOK, m.players, m.minimapScale)
			parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, logPanel, mapPanel))
		case l.mapRows > 0:
			mapPanel := renderMinimap(w, l.mapRows, m.localX, m.localZ, m.localOK, m.players, m.minimapScale)
			parts = append(parts, mapPanel, logPanel)
		default:
			parts = append(parts, logPanel)
		}
	}

	// Chat panel (#20) — scrollback + an input row, just above the footer.
	if l.chat {
		m.chatInput.Width = w - 8
		chatBody := panelTitleStyle.Render("Chat") + "\n"
		if l.chatRows > 0 {
			chatBody += chatScrollback(m.chatLines, l.chatRows, w-4) + "\n"
		}
		parts = append(parts, panelStyle.Width(w-2).Render(chatBody+m.chatInput.View()))
	}

	parts = append(parts, footer)
	return clipRows(strings.Join(parts, "\n"), height)
}

// renderLogPanel draws the newest log entries into exactly rows rows, wrapping
// a long entry at the panel's inner width so it can't add rows. PURE.
func renderLogPanel(width, rows int, logs []logEntry) string {
	// Every entry takes at least one row, so only the newest rows can show.
	start := 0
	if len(logs) > rows {
		start = len(logs) - rows
	}
	entries := make([]string, 0, len(logs)-start)
	for _, e := range logs[start:] {
		ts := logTime.Render(e.t.Format("15:04:05"))
		entries = append(entries, ts+" "+e.styled().Render(displayText(e.msg)))
	}
	body := strings.Join(wrapTail(entries, rows, width-4), "\n")
	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Log") + "\n" + body,
	)
}

// renderHeader builds the dashboard's top line: role, player name, and either
// the Host IPs to share (host) or the address being joined. It is the one row
// that is always on screen, so the Host IPs live here rather than in the
// status panel. Truncated to width, never wrapped. PURE.
//
//	ended  swaps the role label for "Session ended" and drops the IPs, which
//	       are no longer worth sharing.
func renderHeader(width int, role, name, addr string, hostIPs []string, ended bool) string {
	label := panelTitleStyle.Render("Joining")
	if role == "Host" {
		label = panelTitleStyle.Render("Hosting")
	}
	if ended {
		label = statusErr.Padding(0, 1).Render("Session ended")
	}
	s := label + " " + valueStyle.Render(name)
	switch {
	case role != "Host":
		s += " " + labelStyle.Render("→ "+addr)
	case !ended:
		if len(hostIPs) > 0 {
			s += "   " + labelStyle.Render("Host IP ") + valueStyle.Render(strings.Join(hostIPs, ", "))
		}
		// The TUI always hosts on the default port (see main's Hooks).
		s += "   " + labelStyle.Render("port 25565")
	}
	return lipgloss.NewStyle().
		Background(zeldaDark).
		Foreground(white).
		Width(width).
		Padding(0, 2).
		Render(truncate(s, width-4))
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
// canned count/latency/players and assert the output contains them. Each line
// is truncated to the panel, so the panel is always 5 rows tall. (The Host IPs
// moved to renderHeader.)
//
//	count    connected player count (0 renders as "0").
//	latency  client<->server RTT; 0/unknown renders "—" (never invented).
//	players  remote players; their names (or "player N") form the presence row.
func renderStatus(width, count int, latency time.Duration, players []PlayerView) string {
	inner := width - 4 // border + padding

	line1 := labelStyle.Render("Players ") + valueStyle.Render(fmt.Sprintf("%d", count)) +
		labelStyle.Render("    Latency ") + valueStyle.Render(formatLatency(latency))

	var line2 string
	if len(players) > 0 {
		names := make([]string, 0, len(players))
		for _, p := range players {
			n := p.Name
			if n == "" {
				n = fmt.Sprintf("player %d", p.ID)
			}
			names = append(names, displayText(n))
		}
		line2 = labelStyle.Render("In game ") + valueStyle.Render(strings.Join(names, ", "))
	} else {
		line2 = labelStyle.Render("In game ") + labelStyle.Render("(waiting for players…)")
	}

	body := truncate(line1, inner) + "\n" + truncate(line2, inner)
	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Status") + "\n" + body,
	)
}

// renderEnded takes the status panel's place once the session goroutine has
// returned: "Session ended: <reason>", wrapped over the panel's two body rows
// (cut with "…" past that), with the way back on the second row when the
// reason fits on one. Same height as renderStatus, so the layout doesn't
// jump. PURE.
func renderEnded(width int, reason string) string {
	inner := width - 4
	rows := wrapRows(statusErr.Render("Session ended: "+displayText(reason)), inner)
	switch {
	case len(rows) > 2:
		rows = []string{rows[0], truncate(strings.TrimRight(rows[1], " ")+" …", inner)}
	case len(rows) < 2:
		rows = append(rows, truncate(labelStyle.Render("Press Esc to go back and try again."), inner))
	}
	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Status") + "\n" + strings.Join(rows, "\n"),
	)
}
