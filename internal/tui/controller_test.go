package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeEngine is a session.Engine whose sessions log one line, run until
// cancelled (or return failErr at once when it is set), and record the chat
// lines sent through them.
type fakeEngine struct {
	failErr error
	live    session.Live

	mu   sync.Mutex
	sent []string
}

func (e *fakeEngine) run(ctx context.Context, out session.Output) error {
	out.Log(report.OK, "session up")
	if e.failErr != nil {
		return e.failErr
	}
	<-ctx.Done()
	return nil
}

func (e *fakeEngine) Host(ctx context.Context, _ context.CancelFunc, _ session.HostOptions, out session.Output) error {
	return e.run(ctx, out)
}

func (e *fakeEngine) Join(ctx context.Context, _ context.CancelFunc, _ session.JoinOptions, out session.Output) error {
	return e.run(ctx, out)
}

func (e *fakeEngine) Live() session.Live { return e.live }

func (e *fakeEngine) SendChat(text string) error {
	e.mu.Lock()
	e.sent = append(e.sent, text)
	e.mu.Unlock()
	return nil
}

func (e *fakeEngine) sentChat() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sent...)
}

// runningDashboard starts a join session on eng and returns its dashboard,
// left again when the test ends.
func runningDashboard(t *testing.T, eng *fakeEngine) dashboardModel {
	t.Helper()
	m, err := startDashboard(session.New(eng, session.Options{}), "Join", "Me", "10.0.0.5", session.CoopOptions{}, 80)
	if err != nil {
		t.Fatalf("startDashboard: %v", err)
	}
	t.Cleanup(m.leave)
	return m
}

// nextEvent runs the dashboard's drain Cmd and applies what it delivers,
// failing instead of hanging if nothing comes.
func nextEvent(t *testing.T, m dashboardModel) dashboardModel {
	t.Helper()
	got := make(chan tea.Msg, 1)
	go func() { got <- drainEvents(m.events)() }()
	select {
	case msg := <-got:
		m, _ = m.update(msg)
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no session event arrived")
		return m
	}
}

// TestDashboard_LogsAndEndThroughController: the session's log lines reach
// the log panel, and a session that fails ends the dashboard with the error
// as its reason, the way a failed HostSession used to.
func TestDashboard_LogsAndEndThroughController(t *testing.T) {
	eng := &fakeEngine{failErr: errors.New("server start: bind: address already in use")}
	m := runningDashboard(t, eng)
	for !m.ended {
		m = nextEvent(t, m)
	}
	if m.endReason != "server start: bind: address already in use" {
		t.Errorf("endReason = %q, want the engine's error", m.endReason)
	}
	var lines []string
	for _, e := range m.logs {
		lines = append(lines, e.msg)
	}
	if got := strings.Join(lines, "|"); got != "session up|server start: bind: address already in use" {
		t.Errorf("log panel = %q, want the session's line then its error", got)
	}
}

// TestDashboard_StatusFromSnapshot: the status tick reads the controller's
// snapshot into the panel fields.
func TestDashboard_StatusFromSnapshot(t *testing.T) {
	eng := &fakeEngine{live: session.Live{
		Players:     []session.Player{{ID: 2, Name: "Zelda", X: 5, Z: 6}},
		PlayerCount: 2,
		Latency:     12 * time.Millisecond,
		LocalX:      1, LocalZ: 3, LocalOK: true,
	}}
	m := runningDashboard(t, eng)
	m, _ = m.update(statusTickMsg{})
	if m.playerCount != 2 || m.latency != 12*time.Millisecond || len(m.players) != 1 || m.players[0].Name != "Zelda" {
		t.Errorf("status = count %d latency %v players %v, want the engine's live values", m.playerCount, m.latency, m.players)
	}
	if !m.localOK || m.localX != 1 || m.localZ != 3 {
		t.Errorf("local = (%v, %v, ok=%v), want (1, 3, true)", m.localX, m.localZ, m.localOK)
	}
}

// TestDashboard_ColorFromSnapshot: a remote's tunic color
// (docs/09-player-colors.md) travels from the engine's Live through the
// controller snapshot to the status panel's swatch.
func TestDashboard_ColorFromSnapshot(t *testing.T) {
	red := playercolor.Palette()[1].Color
	eng := &fakeEngine{live: session.Live{
		Players:     []session.Player{{ID: 2, Name: "Zelda", Color: red, HasColor: true}},
		PlayerCount: 2,
	}}
	m := runningDashboard(t, eng)
	m, _ = m.update(statusTickMsg{})
	if len(m.players) != 1 || !m.players[0].HasColor || m.players[0].Color != red {
		t.Fatalf("players = %+v, want Zelda with red", m.players)
	}
	if out := plain(renderStatus(100, m.playerCount, m.latency, m.players)); !strings.Contains(out, "■ Zelda (red)") {
		t.Errorf("status panel missing the swatch\n---\n%s", out)
	}
}

// TestStartDashboard_Busy: a controller that refuses to start reports why,
// so the connect screen can show it, and keeps no subscription.
func TestStartDashboard_Busy(t *testing.T) {
	ctrl := session.New(&fakeEngine{}, session.Options{})
	first, err := startDashboard(ctrl, "Host", "Me", "", session.CoopOptions{}, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer first.leave()
	if _, err := startDashboard(ctrl, "Join", "Me", "10.0.0.5", session.CoopOptions{}, 80); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second start err = %v, want the busy error", err)
	}
}
