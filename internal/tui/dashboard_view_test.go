package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
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
		players:      []PlayerView{{ID: 2, Name: "Zelda", X: 50, Z: 30}},
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

// TestDashboard_SessionEnded: the end-of-session message only applies to the
// dashboard's own session, and then the header, status and footer say so.
func TestDashboard_SessionEnded(t *testing.T) {
	current, stale := &session{}, &session{}
	m := busyDashboard("Host")
	m.sess = current

	m, _ = m.update(sessionEndedMsg{sess: stale, reason: "old session"})
	if m.ended {
		t.Fatal("a stale session's end marked the current dashboard as ended")
	}

	m, _ = m.update(sessionEndedMsg{sess: current, reason: "dolphin not running"})
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
	called := 0
	m := dashboardModel{
		name:      "Me",
		hooks:     Hooks{SendChat: func(string) error { called++; return nil }},
		chatInput: textinput.New(),
		ended:     true,
	}
	m.chatInput.SetValue("anyone there?")
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if called != 0 || len(m.chatLines) != 0 {
		t.Errorf("ended dashboard sent chat: calls=%d lines=%v", called, m.chatLines)
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
