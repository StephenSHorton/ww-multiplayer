package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// PlayerView is a tui-local, network-free snapshot of one player in a running
// session. main converts network.RemotePlayer -> PlayerView inside the Players
// hook, so internal/tui can render presence/positions without ever importing
// internal/network. Position fields are the remote's real world coords (0 when
// unknown).
type PlayerView struct {
	ID      byte
	Name    string
	X, Y, Z float32
}

// Hooks lets main package inject its multiplayer-session functions and a set of
// data-only accessors WITHOUT forcing internal/tui to import internal/dolphin
// or internal/network. Keeps the TUI a pure UI layer.
//
// Every accessor field is OPTIONAL: it is nil whenever the value is not
// applicable (CLI paths never set any of them; a given role may not populate
// all of them). Callers MUST nil-check before invoking. Accessors are expected
// to be cheap and race-free — main snapshots the live network objects behind
// its own lock — so the dashboard can poll them on a tick.
type Hooks struct {
	// HostSession runs the host flow (TCP server + broadcast-pose +
	// puppet-sync). Returns when ctx is cancelled OR any underlying
	// goroutine errors. Should be safe to call repeatedly across Esc -> back
	// transitions in the TUI (server is unbound between calls).
	HostSession func(ctx context.Context, cancel context.CancelFunc, name string, rep report.Reporter) error

	// JoinSession runs the joiner flow (broadcast-pose + puppet-sync only,
	// no server). Returns when ctx is cancelled OR the connection drops.
	JoinSession func(ctx context.Context, cancel context.CancelFunc, addr, name string, rep report.Reporter)

	// Players returns a snapshot of the remote players in the running session,
	// or nil when there is no session / it isn't wired. Data-only (PlayerView).
	Players func() []PlayerView

	// LocalPos returns this player's own world position; ok=false when unknown.
	// Nil for now — PR-C (minimap) populates it.
	LocalPos func() (x, y, z float32, ok bool)

	// PlayerCount returns how many players the session sees. Host role reports
	// the server's connection count; join role reports remotes + self. 0 when
	// unknown / idle.
	PlayerCount func() int

	// HostIPs returns the host's shareable LAN IPs (host role only); nil on
	// join or when idle.
	HostIPs func() []string

	// Latency returns the client<->server round-trip time, or 0 if not yet
	// measured / unknown.
	Latency func() time.Duration

	// SendChat sends a chat line to the session. Nil for now — PR-B (chat)
	// populates it.
	SendChat func(text string) error

	// ChatCh returns a receive-only channel of incoming chat lines. Nil for
	// now — PR-B (chat) populates it.
	ChatCh func() <-chan string
}

// session holds the running multiplayer goroutine + its log channel.
// One per dashboard incarnation; tornDown when the user hits Esc.
type session struct {
	role string
	name string
	addr string

	cancel context.CancelFunc
	done   chan struct{} // closed when the session goroutine returns
	logCh  chan logEntry
}

// tuiReporter pushes Reporter.Log calls onto a buffered channel that the
// dashboard drains via tea.Cmd. Non-blocking on full — broadcast-pose's
// 20 Hz tick and puppet-sync's 60 Hz tick must never stall on UI.
type tuiReporter struct {
	ch chan logEntry
}

func (r *tuiReporter) Log(level report.Level, msg string) {
	entry := logEntry{
		t:     time.Now(),
		level: level,
		msg:   msg,
	}
	select {
	case r.ch <- entry:
	default:
		// Channel full — drop the message rather than block. The user's
		// game-side latency is more important than seeing every log line.
	}
}

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

func startSession(hooks Hooks, role, name, addr string) *session {
	s := &session{
		role:  role,
		name:  name,
		addr:  addr,
		done:  make(chan struct{}),
		logCh: make(chan logEntry, 256),
	}
	rep := &tuiReporter{ch: s.logCh}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		defer close(s.done)
		if role == "Host" {
			if err := hooks.HostSession(ctx, cancel, name, rep); err != nil {
				rep.Log(report.Err, err.Error())
			}
		} else {
			hooks.JoinSession(ctx, cancel, addr, name, rep)
		}
	}()
	return s
}

// stop cancels the underlying ctx and waits for the session goroutine to
// finish (so the next session can rebind :25565 cleanly). Caller is the
// dashboard responding to Esc / Ctrl+C.
func (s *session) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}
