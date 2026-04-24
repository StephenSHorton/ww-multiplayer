package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// Hooks lets main package inject its multiplayer-session functions
// without forcing internal/tui to import internal/dolphin or internal/network.
// Keeps the TUI a pure UI layer.
type Hooks struct {
	// HostSession runs the host flow (TCP server + broadcast-pose +
	// puppet-sync). Returns when ctx is cancelled OR any underlying
	// goroutine errors. Should be safe to call repeatedly across Esc -> back
	// transitions in the TUI (server is unbound between calls).
	HostSession func(ctx context.Context, cancel context.CancelFunc, name string, rep report.Reporter) error

	// JoinSession runs the joiner flow (broadcast-pose + puppet-sync only,
	// no server). Returns when ctx is cancelled OR the connection drops.
	JoinSession func(ctx context.Context, cancel context.CancelFunc, addr, name string, rep report.Reporter)
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
