package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// busyDashboard is a dashboard in the state that used to overflow: a resolved
// local position (full minimap), a full log with a long error at the end, a
// chat backlog, and host IPs to share.
func busyDashboard(role string) dashboardModel {
	m := dashboardModel{
		role:         role,
		name:         "Link",
		addr:         "192.168.1.10",
		playerCount:  2,
		latency:      12 * time.Millisecond,
		players:      []session.Player{{ID: 2, Name: "Zelda", X: 50, Z: 30}},
		localOK:      true,
		chatInput:    textinput.New(),
		minimapScale: defaultMinimapScale,
	}
	if role == "Host" {
		m.hostIPs = []string{"192.168.1.10", "10.0.0.5"}
	}
	for i := 0; i < logCap; i++ {
		m.logs = append(m.logs, logEntry{t: time.Unix(0, 0), level: report.Info, msg: fmt.Sprintf("log line %d", i)})
	}
	m.logs = append(m.logs, logEntry{
		t:     time.Unix(0, 0),
		level: report.Err,
		msg: "server start: listen tcp :25565: bind: Only one usage of each socket address " +
			"(protocol/network address/port) is normally permitted. NEWEST",
	})
	for i := 0; i < chatCap; i++ {
		m.chatLines = append(m.chatLines, fmt.Sprintf("Zelda: %s %d", strings.Repeat("blah ", 30), i))
	}
	return m
}

// checkFits asserts the rendered view never exceeds the terminal: Bubble Tea
// drops overflow rows off the TOP, which is how the header and Host IP used
// to disappear at 80x24 and 120x30.
func checkFits(t *testing.T, out string, w, h int) []string {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("view is %d rows, terminal is %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("row %d is %d cells, terminal is %d: %q", i, lw, w, plain(l))
		}
	}
	return lines
}

func TestDashboardView_FitsTerminal(t *testing.T) {
	sizes := [][2]int{{80, 24}, {120, 30}, {80, 40}, {100, 30}, {200, 60}}
	for _, role := range []string{"Host", "Join"} {
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%s/%dx%d", role, w, h), func(t *testing.T) {
				m := busyDashboard(role)
				lines := checkFits(t, m.view(w, h), w, h)
				out := plain(strings.Join(lines, "\n"))

				// The header is the top row and carries the role and,
				// for a host, every IP to share.
				head := plain(lines[0])
				if role == "Host" {
					for _, want := range []string{"Hosting", "Link", "192.168.1.10", "10.0.0.5"} {
						if !strings.Contains(head, want) {
							t.Errorf("header row missing %q: %q", want, head)
						}
					}
				} else if !strings.Contains(head, "Joining") || !strings.Contains(head, "192.168.1.10") {
					t.Errorf("header row should show Joining and the address: %q", head)
				}
				for _, want := range []string{"Status", "Players", "Log", "Chat", "NEWEST", "esc: back"} {
					if !strings.Contains(out, want) {
						t.Errorf("view missing %q\n---\n%s", want, out)
					}
				}
				if strings.Contains(out, "log line 0 ") {
					t.Errorf("view shows the oldest log line instead of the tail\n---\n%s", out)
				}
			})
		}
	}
}

// TestDashboardView_FitsWithColor repeats the fit check with real escape codes
// in the output, as in a color terminal (go test has no TTY, so lipgloss
// otherwise renders plain text): widths must be measured, and lines cut,
// around the styling.
func TestDashboardView_FitsWithColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(0) // termenv.TrueColor
	defer lipgloss.SetColorProfile(prev)

	m := busyDashboard("Host")
	for _, sz := range [][2]int{{80, 24}, {120, 30}} {
		out := m.view(sz[0], sz[1])
		if !strings.Contains(out, "\x1b[") {
			t.Fatal("expected styled output under the TrueColor profile")
		}
		lines := checkFits(t, out, sz[0], sz[1])
		if head := plain(lines[0]); !strings.Contains(head, "Hosting") || !strings.Contains(head, "10.0.0.5") {
			t.Errorf("%dx%d: header row = %q, want the role and IPs", sz[0], sz[1], head)
		}
	}

	// A header wrapped over several rows measures and fits the same way.
	m.hostIPs = multiAdapterIPs
	lines := checkFits(t, m.view(80, 24), 80, 24)
	toks := headerTokens(lines)
	for _, ip := range multiAdapterIPs {
		if !toks[ip] {
			t.Errorf("styled 80x24 header is missing host IP %s", ip)
		}
	}
}

// TestDashboardView_Minimap: hidden when it doesn't fit (80x24), stacked
// above the log when there's height (80x40), beside the log on a wide
// terminal (120x30), and full-size given room.
func TestDashboardView_Minimap(t *testing.T) {
	m := busyDashboard("Host")

	if out := plain(m.view(80, 24)); strings.Contains(out, "Minimap") {
		t.Errorf("80x24 has no room for the minimap, but it was drawn\n---\n%s", out)
	}

	tall := plain(m.view(80, 40))
	if !strings.Contains(tall, "Minimap") || !strings.ContainsRune(tall, minimapSelf) {
		t.Errorf("80x40 should draw the minimap\n---\n%s", tall)
	}

	wide := plain(m.view(120, 30))
	beside := false
	for _, l := range strings.Split(wide, "\n") {
		if strings.Contains(l, "Log") && strings.Contains(l, "Minimap") {
			beside = true
		}
	}
	if !beside {
		t.Errorf("120x30 should put the minimap beside the log\n---\n%s", wide)
	}

	l := planLayout(80, 60, true)
	if l.mapRows != minimapRows || l.beside {
		t.Errorf("planLayout(80, 60) = %+v, want a full %d-row minimap stacked", l, minimapRows)
	}
	if l := planLayout(80, 30, false); l.mapRows != 1 {
		t.Errorf("planLayout without a position = %+v, want the 1-row placeholder", l)
	}
}

// TestDashboardView_TinyTerminal: below the panels' minimums the view still
// never overflows, the header stays on top, and no panel is drawn half-cut.
func TestDashboardView_TinyTerminal(t *testing.T) {
	for _, sz := range [][2]int{{40, 12}, {60, 15}, {30, 8}, {80, 5}} {
		w, h := sz[0], sz[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			lines := checkFits(t, busyDashboard("Host").view(w, h), w, h)
			if !strings.Contains(plain(lines[0]), "Hosting") {
				t.Errorf("top row should be the header, got %q", plain(lines[0]))
			}
			// Every top border must have a matching bottom border.
			opened := 0
			for _, l := range lines {
				opened += strings.Count(l, "╭") - strings.Count(l, "╰")
			}
			if opened != 0 && h >= 7 {
				t.Errorf("a panel was cut off (%d unclosed)\n---\n%s", opened, plain(strings.Join(lines, "\n")))
			}
		})
	}
}

// TestDashboard_SessionEnded: the end-of-session event only applies to the
// dashboard's own session and subscription, and then the header, status and
// footer say so.
func TestDashboard_SessionEnded(t *testing.T) {
	ch, staleCh := make(chan session.Event), make(chan session.Event)
	m := busyDashboard("Host")
	m.sessID, m.events = 2, ch
	ended := func(c chan session.Event, id session.ID, reason string) eventMsg {
		return eventMsg{ch: c, ev: session.Event{Kind: session.EventEnded, Session: id, Text: reason}}
	}

	m, _ = m.update(ended(ch, 1, "old session"))
	if m.ended {
		t.Fatal("a stale session's end marked the current dashboard as ended")
	}
	m, cmd := m.update(ended(staleCh, 2, "old dashboard"))
	if m.ended || cmd != nil {
		t.Fatalf("an event from a left dashboard's subscription was applied (ended=%v) or re-drained (cmd=%v)", m.ended, cmd != nil)
	}

	m, _ = m.update(ended(ch, 2, "dolphin not running"))
	if !m.ended || m.endReason != "dolphin not running" {
		t.Fatalf("own session end not recorded: ended=%v reason=%q", m.ended, m.endReason)
	}

	lines := checkFits(t, m.view(80, 24), 80, 24)
	out := plain(strings.Join(lines, "\n"))
	if !strings.Contains(out, "Session ended: dolphin not running") {
		t.Errorf("view missing the session-ended reason\n---\n%s", out)
	}
	if head := plain(lines[0]); strings.Contains(head, "Hosting") || !strings.Contains(head, "Session ended") {
		t.Errorf("header should say the session ended, got %q", head)
	}
	if strings.Contains(out, "type + enter: chat") {
		t.Errorf("footer still offers chat on an ended session\n---\n%s", out)
	}
}

// TestDashboard_EndedChatNotSent: once the session is gone, Enter must not
// forward or echo a chat line as if it had been delivered.
func TestDashboard_EndedChatNotSent(t *testing.T) {
	eng := &fakeEngine{}
	m := runningDashboard(t, eng)
	m.ended = true
	m.chatInput.SetValue("anyone there?")
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := eng.sentChat(); len(got) != 0 || len(m.chatLines) != 0 {
		t.Errorf("ended dashboard sent chat: sent=%q lines=%v", got, m.chatLines)
	}
}

// TestChatScrollback_WrapsAndSanitizes: a long peer line wraps inside the
// row budget instead of growing the panel, and control characters (an ESC
// that could inject terminal sequences, a newline) never reach the screen.
func TestChatScrollback_WrapsAndSanitizes(t *testing.T) {
	out := chatScrollback([]string{"Ganon: \x1b[2J" + strings.Repeat("spam ", 30) + "\nEND"}, 3, 20)
	rows := strings.Split(out, "\n")
	if len(rows) != 3 {
		t.Fatalf("chatScrollback emitted %d rows, want 3: %q", len(rows), rows)
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w > 20 {
			t.Errorf("row %d is %d cells, want <= 20: %q", i, w, r)
		}
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("chatScrollback passed an ESC through: %q", out)
	}
	if !strings.HasPrefix(rows[0], "Ganon:") {
		t.Errorf("an over-long newest line should keep its start, got %q", rows[0])
	}
}

// TestRenderEnded_LongReasonWraps: a long reason uses both status rows and is
// cut with an ellipsis beyond that, keeping the panel at renderStatus's height.
func TestRenderEnded_LongReasonWraps(t *testing.T) {
	short := renderEnded(80, "boom")
	long := renderEnded(80, strings.Repeat("very long reason ", 20))
	want := lipgloss.Height(renderStatus(80, 1, 0, nil))
	for name, s := range map[string]string{"short": short, "long": long} {
		if got := lipgloss.Height(s); got != want {
			t.Errorf("%s renderEnded is %d rows, want %d like renderStatus", name, got, want)
		}
	}
	if !strings.Contains(plain(short), "Press Esc") {
		t.Errorf("short reason should leave room for the Esc hint\n%s", plain(short))
	}
	if !strings.Contains(plain(long), "…") {
		t.Errorf("long reason should be cut with an ellipsis\n%s", plain(long))
	}
}

// multiAdapterIPs is what listHostIPs returns on a typical Windows gaming
// PC: WSL/Hyper-V and VirtualBox adapters ahead of the real LAN address,
// then a VPN.
var multiAdapterIPs = []string{"172.29.160.1", "192.168.56.1", "192.168.1.23", "10.8.0.2"}

// headerTokens returns the words (split on spaces and commas) of the header
// rows: every row above the status panel's top border.
func headerTokens(lines []string) map[string]bool {
	toks := map[string]bool{}
	for _, l := range lines {
		if strings.Contains(l, "╭") {
			break
		}
		for _, f := range strings.FieldsFunc(plain(l), func(r rune) bool { return r == ' ' || r == ',' }) {
			toks[f] = true
		}
	}
	return toks
}

// TestDashboardView_HeaderKeepsEveryHostIP: a host with several adapters and
// a long name still has every IP on screen, whole, at any size the panels
// fit, instead of the tail of the list being cut with an ellipsis.
func TestDashboardView_HeaderKeepsEveryHostIP(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {120, 30}, {200, 60}}
	for w := dashMinWidth; w <= 220; w += 7 {
		sizes = append(sizes, [2]int{w, 40})
	}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		m := busyDashboard("Host")
		m.name = "LinkTheHeroOfTheWind" // nameMaxLen
		m.hostIPs = multiAdapterIPs
		lines := checkFits(t, m.view(w, h), w, h)
		if head := plain(lines[0]); !strings.Contains(head, "Hosting") {
			t.Errorf("%dx%d: top row should be the header, got %q", w, h, head)
		}
		toks := headerTokens(lines)
		for _, ip := range multiAdapterIPs {
			if !toks[ip] {
				t.Errorf("%dx%d: header is missing host IP %s\n---\n%s", w, h, ip, plain(strings.Join(lines, "\n")))
			}
		}
		if !toks["25565"] {
			t.Errorf("%dx%d: header is missing the port", w, h)
		}
	}
}

// TestRenderHeader_IPRows: the IPs share the first row when they fit and get
// rows of their own when they don't, each within the width.
func TestRenderHeader_IPRows(t *testing.T) {
	if got := lipgloss.Height(renderHeader(120, "Host", "Link", "", []string{"192.168.1.10", "10.0.0.5"}, false)); got != 1 {
		t.Errorf("two IPs at 120 columns took %d header rows, want 1", got)
	}
	h := renderHeader(80, "Host", "LinkTheHeroOfTheWind", "", multiAdapterIPs, false)
	rows := strings.Split(plain(h), "\n")
	if len(rows) < 2 {
		t.Fatalf("four IPs and a 20-character name should wrap the header, got %q", rows)
	}
	for i, r := range rows {
		if lipgloss.Width(r) != 80 {
			t.Errorf("header row %d is %d cells, want 80: %q", i, lipgloss.Width(r), r)
		}
		if strings.Contains(r, "…") {
			t.Errorf("header row %d was cut: %q", i, r)
		}
	}
	if !strings.Contains(rows[1], "Host IP 172.29.160.1,") {
		t.Errorf("second header row should start the IP list, got %q", rows[1])
	}
}

// TestHostIPRows_PacksWhole: IPs are never split unless one alone is wider
// than a row, and then its pieces add back up to the whole address.
func TestHostIPRows_PacksWhole(t *testing.T) {
	const width = 34 // two IPv4s share a row; the IPv6 literal doesn't fit one
	v6 := "2001:db8:85a3::8a2e:370:7334"
	rows := hostIPRows([]string{"192.168.1.23", "10.8.0.2", v6, "10.0.0.5"}, width)
	var text strings.Builder
	for i, r := range rows {
		if lw := lipgloss.Width(r); lw > width {
			t.Errorf("row %d is %d cells, want <= %d: %q", i, lw, width, plain(r))
		}
		text.WriteString(strings.TrimPrefix(strings.TrimSpace(plain(r)), strings.TrimSpace(hostIPLabel)))
	}
	joined := strings.ReplaceAll(text.String(), " ", "")
	if want := "192.168.1.23,10.8.0.2," + v6 + ",10.0.0.5"; joined != want {
		t.Errorf("hostIPRows text = %q, want %q", joined, want)
	}
	if !strings.Contains(plain(rows[0]), "192.168.1.23, 10.8.0.2,") {
		t.Errorf("first row should pack the two IPv4s together, got %q", plain(rows[0]))
	}
}

// typeChat sends s through update one rune at a time, the way a typist does.
func typeChat(m dashboardModel, s string) dashboardModel {
	for _, r := range s {
		m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// checkChatRow asserts a view with a long chat line still ends in the chat
// panel and footer, with the input on one row showing its newest text.
func checkChatRow(t *testing.T, m dashboardModel, w, h int, tail string) {
	t.Helper()
	lines := checkFits(t, m.view(w, h), w, h)
	n := len(lines)
	if n < 3 {
		t.Fatalf("%dx%d: only %d rows", w, h, n)
	}
	if !strings.Contains(plain(lines[n-1]), "esc: back") {
		t.Errorf("%dx%d: last row should be the footer, got %q", w, h, plain(lines[n-1]))
	}
	if !strings.Contains(lines[n-2], "╰") {
		t.Errorf("%dx%d: the chat panel's bottom border is missing, got %q", w, h, plain(lines[n-2]))
	}
	if in := plain(lines[n-3]); !strings.Contains(in, "> ") || !strings.Contains(in, tail) {
		t.Errorf("%dx%d: input row should show the end of the line being typed (%q), got %q", w, h, tail, in)
	}
}

// TestDashboard_LongChatInputScrolls: a chat line near CharLimit scrolls
// sideways inside the one input row, so it can't grow the chat panel and
// push the footer and the cursor off the bottom.
func TestDashboard_LongChatInputScrolls(t *testing.T) {
	long := strings.Repeat("a", 199) + "Z"

	t.Run("resize", func(t *testing.T) {
		m := busyDashboard("Host")
		m.chatInput.Focus()
		m, _ = m.update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = typeChat(m, long)
		checkChatRow(t, m, 80, 24, "aaaZ")

		// Typed wide, then shrunk: the window is recomputed for the new
		// width right away, not on the next keypress.
		m, _ = m.update(tea.WindowSizeMsg{Width: 200, Height: 30})
		checkChatRow(t, m, 200, 30, "aaaZ")
		m, _ = m.update(tea.WindowSizeMsg{Width: 60, Height: 24})
		checkChatRow(t, m, 60, 24, "aaaZ")
	})

	t.Run("app", func(t *testing.T) {
		// Through the app: the dashboard is built at the width the app
		// already knows, with no further WindowSizeMsg.
		var app tea.Model = newModel(session.New(&fakeEngine{}, session.Options{}), "test")
		app, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		app, _ = app.Update(connectedMsg{role: "Join", addr: "10.0.0.5", name: "Link"})
		m := app.(model).dashboard
		t.Cleanup(m.leave)
		if m.chatInput.Width != chatInputWidth(80) {
			t.Fatalf("dashboard chat input Width = %d, want %d", m.chatInput.Width, chatInputWidth(80))
		}
		m = typeChat(m, long)
		checkChatRow(t, m, 80, 24, "aaaZ")

		// A later resize reaches the dashboard through the app.
		app, _ = app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		if got := app.(model).dashboard.chatInput.Width; got != chatInputWidth(100) {
			t.Errorf("after a resize the chat input Width = %d, want %d", got, chatInputWidth(100))
		}
	})

	t.Run("no width yet", func(t *testing.T) {
		// Width never set: truncation still keeps the input on one row.
		m := busyDashboard("Host")
		m.chatInput.Focus()
		m.chatInput.SetValue(long)
		lines := checkFits(t, m.view(80, 24), 80, 24)
		if !strings.Contains(plain(lines[len(lines)-1]), "esc: back") {
			t.Errorf("last row should be the footer, got %q", plain(lines[len(lines)-1]))
		}
	})
}
