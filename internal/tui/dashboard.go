package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/redact"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboardModel renders the running multiplayer session: a compact status
// panel + a rolling log panel fed by the session's events. Esc tears the
// session down and emits a backMsg so the app transitions back to connect.
type dashboardModel struct {
	role string
	name string
	addr string

	// ctrl runs the session; sessID is the run this dashboard shows, and
	// events/unsubscribe its event subscription. ctrl is nil in render-only
	// tests.
	ctrl        *session.Controller
	sessID      session.ID
	events      <-chan session.Event
	unsubscribe func()

	logs []logEntry // ring of last logCap entries

	// Chat panel state (#20). chatInput is the bubbles textinput row; chatLines
	// is a ring of the last chatCap rendered lines ("name: text").
	chatInput textinput.Model
	chatLines []string

	// Status-panel state, refreshed by statusTick (~250ms) from the
	// controller's Snapshot; these hold the last read.
	playerCount int
	hostIPs     []string
	latency     time.Duration
	players     []session.Player

	// Minimap state (#22), refreshed alongside the above on the same
	// statusTick. localOK mirrors the snapshot's LocalOK -- false until
	// the broadcaster's first tick resolves a position.
	localX, localZ float32
	localOK        bool

	// minimapScale is read ONCE from WW_MINIMAP_SCALE at construction (see
	// newDashboard) rather than per render.
	minimapScale float32

	// ended is set by the EventEnded for THIS dashboard's session: the
	// header, status panel and footer then say so, with endReason.
	ended     bool
	endReason string

	// Co-op (App PR 2). coop says the session runs with co-op, so a chat
	// line starting with '/' is a console command (/help lists them);
	// coopStatus is each pipeline's status from the snapshot; questions
	// are the open co-op questions, oldest first: the first is shown in
	// place of the log and answered with y or n. The snapshot's
	// State.Questions is their source of truth: the events only show a
	// change sooner, and a subscription may drop one (see Subscribe).
	coop       bool
	coopStatus []session.CoopStatus
	questions  []session.Question
	// questionShown is when the question shown (questions[0]) appeared,
	// and lastKey when the last key came. A y or n answers only after
	// questionSettle of both: a key typed for the chat as the question
	// came up never answers it (docs/08 Appendix C items 64 and 164).
	questionShown time.Time
	lastKey       time.Time
	// now is the clock; nil is time.Now. Tests set it.
	now func() time.Time

	// showIPs turns off the IP masking (the app model's showIPsKey):
	// by default the header's addresses, the log, the chat and the end
	// reason show every IP masked (internal/redact), so a stream never
	// flashes one.
	showIPs bool
}

// questionSettle is how long a co-op question must have been shown, and
// the keyboard quiet, before a y or n answers it. A key within it is
// swallowed (and restarts the quiet), so someone typing "yeah ok" in the
// chat as a bind question comes up does not bind with the 'y'. It is
// longer than the CLI's promptSettleMax wait for typed-ahead input:
// keys come one at a time here, with no line to stamp.
const questionSettle = 600 * time.Millisecond

const logCap = 200

// chatCap bounds the chat scrollback ring; chatVisibleRows is how many of those
// lines the chat panel shows at once.
const (
	chatCap         = 50
	chatVisibleRows = 6
)

// statusInterval is how often the dashboard re-reads the controller's
// Snapshot into its status-panel state. 250ms is snappy enough for a
// presence/latency panel without spinning the render loop.
const statusInterval = 250 * time.Millisecond

// statusTickMsg fires every statusInterval to refresh the status panel.
type statusTickMsg struct{}

// statusTick schedules the next status refresh.
func statusTick() tea.Cmd {
	return tea.Tick(statusInterval, func(time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// eventMsg wraps one session event pulled off the dashboard's subscription.
// ch names the subscription it came from: a message still queued from a
// dashboard the user already left (Esc, then Host/Join again) must not
// touch the new dashboard or start a second drain on its channel.
type eventMsg struct {
	ch <-chan session.Event
	ev session.Event
}

// drainEvents returns a tea.Cmd that blocks on ch and emits one eventMsg.
// Update re-issues it after each event, so the channel is drained
// continuously; Bubble Tea runs it on a worker goroutine, so the blocking
// wait does not stall the UI. It returns nil once the subscription is
// closed, and is nil (no Cmd) for a nil channel.
func drainEvents(ch <-chan session.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg{ch: ch, ev: ev}
	}
}

// logEntry is one line in the log panel.
type logEntry struct {
	t     time.Time
	level report.Level
	msg   string
}

func (e logEntry) styled() lipgloss.Style {
	switch e.level {
	case report.OK:
		return logGreen
	case report.Warn:
		return logWarn
	case report.Err:
		return logError
	case report.Net:
		return logCyan
	default:
		return logInfo
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

// newDashboard builds the dashboard for the running session id, fed by the
// events subscription (see startDashboard). width is the terminal width as
// last reported (0 before the first WindowSizeMsg).
func newDashboard(ctrl *session.Controller, id session.ID, events <-chan session.Event, unsubscribe func(), role, name, addr string, width int) dashboardModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message, Enter to send…"
	ti.Prompt = "> "
	ti.CharLimit = 200
	ti.Width = chatInputWidth(width)
	ti.Focus()

	return dashboardModel{
		role:         role,
		name:         name,
		addr:         addr,
		ctrl:         ctrl,
		sessID:       id,
		events:       events,
		unsubscribe:  unsubscribe,
		minimapScale: minimapScaleFromEnv(),
		chatInput:    ti,
	}
}

func (m dashboardModel) initCmd() tea.Cmd {
	// Drain the session's events (log, chat, its end), start the
	// status-panel ticker and start the textinput cursor blink.
	return tea.Batch(drainEvents(m.events), statusTick(), textinput.Blink)
}

// refreshStatus re-reads the controller's snapshot into the model. Value
// receiver so it fits the (dashboardModel, tea.Cmd) update contract; returns
// the updated copy. A snapshot of another session than this dashboard's
// (which only a stale tick could see) reads as no session.
func (m dashboardModel) refreshStatus() dashboardModel {
	if m.ctrl == nil {
		return m
	}
	st := m.ctrl.Snapshot()
	if st.Session != m.sessID {
		st.Live = session.Live{}
	}
	m.playerCount = st.PlayerCount
	m.hostIPs = st.HostAddrs
	m.latency = st.Latency
	m.players = st.Players
	m.localX, m.localZ, m.localOK = st.LocalX, st.LocalZ, st.LocalOK
	m.coopStatus = st.CoopStatus
	if st.Session != m.sessID {
		st.Questions = nil
	}
	// The open questions as the controller has them: this repairs a
	// question event the subscription dropped.
	m = m.setQuestions(st.Questions)
	return m
}

// clock is the dashboard's time now.
func (m dashboardModel) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// setQuestions makes qs the open questions, restarting the settle when
// the question shown changes.
func (m dashboardModel) setQuestions(qs []session.Question) dashboardModel {
	if len(qs) > 0 && (len(m.questions) == 0 || m.questions[0].ID != qs[0].ID) {
		m.questionShown = m.clock()
	}
	m.questions = qs
	return m
}

// dropQuestion removes the question id from the open ones.
func (m dashboardModel) dropQuestion(id string) dashboardModel {
	qs := m.questions[:0:0]
	for _, q := range m.questions {
		if q.ID != id {
			qs = append(qs, q)
		}
	}
	return m.setQuestions(qs)
}

// applyEvent folds one event of this dashboard's session into the model.
func (m dashboardModel) applyEvent(ev session.Event) dashboardModel {
	switch ev.Kind {
	case session.EventLog:
		m.logs = append(m.logs, logEntry{t: ev.Time, level: ev.Level, msg: ev.Text})
		if len(m.logs) > logCap {
			m.logs = m.logs[len(m.logs)-logCap:]
		}
	case session.EventChat:
		m.chatLines = appendChat(m.chatLines, ev.From+": "+ev.Text)
	case session.EventEnded:
		m.ended = true
		m.endReason = ev.Text
		m.questions = nil // an ended session's questions are closed (a no)
	case session.EventQuestion:
		for _, q := range m.questions {
			if q.ID == ev.Question.ID {
				return m // the snapshot showed it first
			}
		}
		m = m.setQuestions(append(m.questions[:len(m.questions):len(m.questions)], ev.Question))
	case session.EventQuestionClosed:
		m = m.dropQuestion(ev.Question.ID)
	}
	return m
}

// answer answers the open question shown, yes or no. The question stays
// until its EventQuestionClosed (or a snapshot without it); one the
// controller no longer has open (its close was missed) is dropped now.
func (m dashboardModel) answer(yes bool) dashboardModel {
	if len(m.questions) > 0 && m.ctrl != nil {
		id := m.questions[0].ID
		if err := m.ctrl.Answer(id, yes); errors.Is(err, session.ErrNoQuestion) {
			m = m.dropQuestion(id)
		}
	}
	return m
}

// leave stops this dashboard's session (synchronously, so the next host
// attempt finds :25565 free) and ends its event subscription.
func (m dashboardModel) leave() {
	if m.ctrl != nil {
		m.ctrl.Leave()
	}
	if m.unsubscribe != nil {
		m.unsubscribe()
	}
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		if msg.ch != m.events {
			return m, nil // from a dashboard the user already left
		}
		if msg.ev.Session == m.sessID {
			m = m.applyEvent(msg.ev)
		}
		return m, drainEvents(m.events)

	case statusTickMsg:
		m = m.refreshStatus()
		return m, statusTick()

	case tea.WindowSizeMsg:
		// The textinput scrolls its value sideways in Update, against the
		// Width stored here, so it has to live in the model, not be set on
		// view's copy. Parking the cursor at the end and back makes it
		// recompute the visible window for the new width.
		m.chatInput.Width = chatInputWidth(msg.Width)
		pos := m.chatInput.Position()
		m.chatInput.CursorEnd()
		m.chatInput.SetCursor(pos)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Stop the session and pop back to connect. leave() is
			// synchronous so the next host attempt finds :25565 free.
			// (textinput never consumes Esc, so this always fires even while
			// the chat input is focused.)
			m.leave()
			return m, func() tea.Msg { return backMsg{} }
		}
		now := m.clock()
		quiet := now.Sub(m.lastKey) >= questionSettle
		m.lastKey = now
		if len(m.questions) > 0 {
			// A co-op question is shown: y or n answers it, and the chat
			// input waits. A key that comes before the question has
			// settled, or hard on another key, was typed for something
			// else: it is swallowed, never taken as the answer.
			if !quiet || now.Sub(m.questionShown) < questionSettle {
				return m, nil
			}
			switch strings.ToLower(msg.String()) {
			case "y":
				m = m.answer(true)
			case "n":
				m = m.answer(false)
			}
			return m, nil
		}
		switch msg.String() {
		case "enter":
			text := strings.TrimSpace(m.chatInput.Value())
			m.chatInput.SetValue("")
			if text == "" || m.ended {
				// Nothing to send, or no session left to send it on.
				return m, nil
			}
			if m.coop && strings.HasPrefix(text, "/") {
				// A co-op console command, as the CLI takes on stdin: the
				// console logs what came of it.
				m = m.applyEvent(session.Event{Kind: session.EventLog, Time: time.Now(), Level: report.Info, Text: "> " + text})
				if m.ctrl != nil {
					if err := m.ctrl.CommandLine(text); err != nil {
						m = m.applyEvent(session.Event{Kind: session.EventLog, Time: time.Now(), Level: report.Warn, Text: "co-op: " + err.Error()})
					}
				}
				return m, nil
			}
			if m.ctrl != nil {
				// Fire-and-forget: a send error (e.g. not connected yet) just
				// means the line doesn't go out; we still echo it locally.
				_ = m.ctrl.SendChat(text)
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

// chatInputWidth is the chat textinput's Width for a terminal width cells
// wide (0 when unknown): the chat panel's inner width less the "> " prompt,
// the cursor cell and one spare, so the input stays on one row.
func chatInputWidth(width int) int {
	if width <= 0 {
		width = dashDefaultWidth
	}
	return max(width, dashMinWidth) - 8
}

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

	// Header, then status (#19): the header carries the role and the Host
	// IPs to share, so it must never be what overflow pushes off. The status
	// panel is left out, rather than half-cut, on a terminal too short for
	// both (a multi-row header of IPs on a tiny one).
	addr, hostIPs := m.addr, m.hostIPs
	if !m.showIPs {
		addr = redact.Addr(addr)
		hostIPs = make([]string, len(m.hostIPs))
		for i, ip := range m.hostIPs {
			hostIPs[i] = redact.Addr(ip)
		}
	}
	header := renderHeader(w, m.role, m.name, addr, hostIPs, m.ended)
	var status string
	if m.ended {
		status = renderEnded(w, m.shownText(m.endReason))
	} else {
		status = renderStatusCoop(w, m.playerCount, m.latency, m.shownPlayers(), m.shownCoop())
	}
	parts := []string{header}
	used := lipgloss.Height(header)
	if used+lipgloss.Height(status) <= height {
		parts = append(parts, status)
		used += lipgloss.Height(status)
	}
	ips := ipsHelp(m.showIPs)
	help := "type + enter: chat    esc: back to connect    " + ips + "    ctrl+c: quit"
	switch {
	case m.ended:
		help = "esc: back to connect (your fields are kept)    " + ips + "    ctrl+c: quit"
	case len(m.questions) > 0:
		help = "y: yes    n: no    esc: back to connect    " + ips + "    ctrl+c: quit"
	case m.coop:
		help = "enter: chat, or /help for co-op commands    esc: back    " + ips + "    ctrl+c: quit"
	}
	footer := helpStyle.Width(w).Render(truncate(help, w))

	avail := height - used - lipgloss.Height(footer)
	l := planLayout(w, avail, m.localOK)

	// A co-op question takes the log's and the minimap's place until it
	// is answered.
	if len(m.questions) > 0 {
		chatH := 0
		if l.chat {
			chatH = l.chatRows + chatChrome
		}
		if rows := avail - chatH - logChrome; rows >= 1 {
			parts = append(parts, renderQuestion(w, rows, m.shownQuestion(m.questions[0]), len(m.questions)-1))
		}
	} else if l.logRows > 0 {
		// Log panel, with the minimap (#22) above it or beside it.
		logW := w
		if l.beside {
			logW = w - minimapPanelWidth
		}
		logPanel := renderLogPanel(logW, l.logRows, m.shownLogs())
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
	// The input's Width is kept in the model (newDashboard, WindowSizeMsg) so
	// a long line scrolls inside one row; truncate is the backstop for a
	// frame drawn before the input has caught up with a resize.
	if l.chat {
		chatBody := panelTitleStyle.Render("Chat") + "\n"
		if l.chatRows > 0 {
			chatBody += chatScrollback(m.shownChat(), l.chatRows, w-4) + "\n"
		}
		input := truncate(m.chatInput.View(), w-4)
		parts = append(parts, panelStyle.Width(w-2).Render(chatBody+input))
	}

	parts = append(parts, footer)
	return clipRows(strings.Join(parts, "\n"), height)
}

// shownText is a free line (log, chat, end reason) as the dashboard shows
// it: its IPs masked unless showIPs.
func (m dashboardModel) shownText(s string) string {
	if m.showIPs {
		return s
	}
	return redact.Text(s)
}

// shownPlayers are the other players as the status panel shows them: a
// peer chooses its name, and one may be an IP, so names are masked as
// free text (shownText). The minimap draws no names.
func (m dashboardModel) shownPlayers() []session.Player {
	if m.showIPs {
		return m.players
	}
	out := make([]session.Player, len(m.players))
	for i, p := range m.players {
		p.Name = redact.Text(p.Name)
		out[i] = p
	}
	return out
}

// shownCoop is the co-op row as the status panel shows it (player names
// masked, as shownPlayers).
func (m dashboardModel) shownCoop() []session.CoopStatus {
	if m.showIPs {
		return m.coopStatus
	}
	out := make([]session.CoopStatus, len(m.coopStatus))
	for i, c := range m.coopStatus {
		c.Player = redact.Text(c.Player)
		out[i] = c
	}
	return out
}

// shownQuestion is a co-op question as the dashboard shows it: the player
// it is for and its text masked (shownText).
func (m dashboardModel) shownQuestion(q session.Question) session.Question {
	if m.showIPs {
		return q
	}
	q.Player, q.Body = redact.Text(q.Player), redact.Text(q.Body)
	details := make([]string, len(q.Details))
	for i, d := range q.Details {
		details[i] = redact.Text(d)
	}
	q.Details = details
	return q
}

// shownLogs is the log as the dashboard shows it (shownText).
func (m dashboardModel) shownLogs() []logEntry {
	if m.showIPs {
		return m.logs
	}
	out := make([]logEntry, len(m.logs))
	for i, e := range m.logs {
		e.msg = redact.Text(e.msg)
		out[i] = e
	}
	return out
}

// shownChat is the chat scrollback as the dashboard shows it (shownText).
func (m dashboardModel) shownChat() []string {
	if m.showIPs {
		return m.chatLines
	}
	out := make([]string, len(m.chatLines))
	for i, l := range m.chatLines {
		out[i] = redact.Text(l)
	}
	return out
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

// renderHeader builds the dashboard's header: role, player name, and either
// the Host IPs to share (host) or the address being joined. It is the part
// that is always on screen, so the Host IPs live here rather than in the
// status panel. Usually one row; when a host's IPs don't fit beside the name
// they move to rows of their own below it (hostIPRows), so no IP is ever cut.
// Other text is truncated to width, never wrapped. PURE.
//
//	ended  swaps the role label for "Session ended" and drops the IPs, which
//	       are no longer worth sharing.
func renderHeader(width int, role, name, addr string, hostIPs []string, ended bool) string {
	inner := width - 4
	label := panelTitleStyle.Render("Joining")
	if role == "Host" {
		label = panelTitleStyle.Render("Hosting")
	}
	if ended {
		label = statusErr.Padding(0, 1).Render("Session ended")
	}
	rows := []string{label + " " + valueStyle.Render(name)}
	switch {
	case role != "Host":
		rows[0] += " " + labelStyle.Render("→ "+addr)
	case !ended:
		// The TUI always hosts on the default port (see main's sessionEngine).
		port := "   " + labelStyle.Render("port 25565")
		ips := labelStyle.Render(hostIPLabel) + valueStyle.Render(strings.Join(hostIPs, ", "))
		if len(hostIPs) > 0 && lipgloss.Width(rows[0]+"   "+ips+port) <= inner {
			rows[0] += "   " + ips + port
		} else {
			if len(hostIPs) > 0 {
				rows = append(rows, hostIPRows(hostIPs, inner)...)
			}
			rows = placePort(rows, port, inner)
		}
	}
	for i, r := range rows {
		rows[i] = truncate(r, inner)
	}
	return lipgloss.NewStyle().
		Background(zeldaDark).
		Foreground(white).
		Width(width).
		Padding(0, 2).
		Render(strings.Join(rows, "\n"))
}

const hostIPLabel = "Host IP "

// placePort appends port to the first header row with room for it, or adds
// it as a row of its own, indented under the IPs. PURE.
func placePort(rows []string, port string, width int) []string {
	for i, r := range rows {
		if lipgloss.Width(r+port) <= width {
			rows[i] += port
			return rows
		}
	}
	return append(rows, strings.Repeat(" ", lipgloss.Width(hostIPLabel))+strings.TrimLeft(port, " "))
}

// hostIPRows lays out the Host IPs as "Host IP a, b," rows at most width
// cells wide, with later rows indented under the first IP. IPs are packed
// whole: a host with a LAN address plus WSL, VirtualBox and VPN adapters
// gets every one on screen, wherever the LAN one falls in the list. Only an
// IP wider than a row by itself (an IPv6 literal at the minimum width) is
// split across rows. PURE.
func hostIPRows(ips []string, width int) []string {
	indent := lipgloss.Width(hostIPLabel)
	space := max(width-indent, 1)
	var lines []string
	cur := ""
	for i, ip := range ips {
		tok := ip
		if i < len(ips)-1 {
			tok += ","
		}
		switch {
		case cur == "":
			cur = tok
		case lipgloss.Width(cur+" "+tok) <= space:
			cur += " " + tok
		default:
			lines = append(lines, cur)
			cur = tok
		}
	}
	lines = append(lines, cur)

	var rows []string
	for _, l := range lines {
		for _, part := range wrapRows(l, space) {
			prefix := strings.Repeat(" ", indent)
			if len(rows) == 0 {
				prefix = labelStyle.Render(hostIPLabel)
			}
			rows = append(rows, prefix+valueStyle.Render(strings.TrimRight(part, " ")))
		}
	}
	return rows
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
func renderStatus(width, count int, latency time.Duration, players []session.Player) string {
	return renderStatusCoop(width, count, latency, players, nil)
}

// renderStatusCoop is renderStatus with a third line for co-op when coop
// has pipelines: each player's replica state and whether it writes. PURE.
func renderStatusCoop(width, count int, latency time.Duration, players []session.Player, coop []session.CoopStatus) string {
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
			n = valueStyle.Render(displayText(n))
			if p.HasColor {
				swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color.Hex())).Render("■")
				n = swatch + " " + n + labelStyle.Render(" ("+p.Color.String()+")")
			}
			names = append(names, n)
		}
		line2 = labelStyle.Render("In game ") + strings.Join(names, valueStyle.Render(", "))
	} else {
		line2 = labelStyle.Render("In game ") + labelStyle.Render("(waiting for players…)")
	}

	body := truncate(line1, inner) + "\n" + truncate(line2, inner)
	if len(coop) > 0 {
		var ps []string
		for _, c := range coop {
			ps = append(ps, valueStyle.Render(displayText(c.Player))+labelStyle.Render(": "+displayText(c.State)+"; "+displayText(c.Writes)))
		}
		body += "\n" + truncate(labelStyle.Render("Co-op   ")+strings.Join(ps, labelStyle.Render("  ")), inner)
	}
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

// renderQuestion draws co-op question q in rows rows: who asks, the
// question as the CLI words it (its last lines kept when it is too long,
// the question itself among them), a bind's preflight, and the keys.
// more is how many questions wait behind it. PURE.
func renderQuestion(width, rows int, q session.Question, more int) string {
	title := "Co-op question"
	if q.Player != "" {
		title += " for " + q.Player
	}
	if more > 0 {
		title += fmt.Sprintf(" (%d more waiting)", more)
	}
	lines := strings.Split(strings.TrimRight(q.Body, "\n"), "\n")
	if len(q.Details) > 0 {
		// The preflight goes before the question, which ends the body.
		last := lines[len(lines)-1]
		lines = append(lines[:len(lines)-1:len(lines)-1], q.Details...)
		lines = append(lines, last)
	}
	var entries []string
	for _, l := range lines {
		entries = append(entries, displayText(l))
	}
	keys := statusOk.Render("y") + labelStyle.Render(": yes, write it    ") + statusErr.Render("n") + labelStyle.Render(": no")
	body := keys
	if rows > 1 {
		body = strings.Join(wrapTail(entries, rows-1, width-4), "\n") + "\n" + truncate(keys, width-4)
	}
	return panelStyle.Width(width - 2).BorderForeground(zeldaGold).Render(
		panelTitleStyle.Render(truncate(title, width-6)) + "\n" + body,
	)
}
