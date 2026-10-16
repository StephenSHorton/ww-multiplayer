package main

import (
	"context"
	"errors"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// sessionEngine is the session.Engine the TUI's controller runs: the same
// runHostSession / runJoinSession the CLI `host` and `join` subcommands run,
// with ls as the status bridge shared across every session of the app run.
//
// The TUI doesn't expose port configuration (#18 is CLI-only), so Host pins
// defaultPort. Co-op stays off (coopFlags{}): its bind and scrub questions
// need a prompt the TUI doesn't have yet, which M8's co-op panel adds.
type sessionEngine struct {
	ls *liveState
}

func (e *sessionEngine) Host(ctx context.Context, cancel context.CancelFunc, o session.HostOptions, out session.Output) error {
	e.ls.setChat(out.Chat)
	defer e.ls.setChat(nil)
	return tuiSessionErr(runHostSession(ctx, cancel, o.Name, defaultPort, out, e.ls, coopFlags{}))
}

func (e *sessionEngine) Join(ctx context.Context, cancel context.CancelFunc, o session.JoinOptions, out session.Output) error {
	e.ls.setChat(out.Chat)
	defer e.ls.setChat(nil)
	return tuiSessionErr(runJoinSession(ctx, cancel, o.Addr, o.Name, out, e.ls, coopFlags{}))
}

// tuiSessionErr is the error the controller should log as the session's
// end: err itself, except a fatal connect error, which the session's
// ConnectLog already logged (logSessionErr skips it the same way).
func tuiSessionErr(err error) error {
	if errors.Is(err, network.ErrFatal) {
		return nil
	}
	return err
}

func (e *sessionEngine) Live() session.Live {
	x, y, z, ok := e.ls.localPos()
	return session.Live{
		Players:     e.ls.players(),
		PlayerCount: e.ls.playerCount(),
		HostAddrs:   e.ls.hostIPList(),
		Latency:     e.ls.latency(),
		LocalX:      x,
		LocalY:      y,
		LocalZ:      z,
		LocalOK:     ok,
	}
}

func (e *sessionEngine) SendChat(text string) error { return e.ls.sendChat(text) }
