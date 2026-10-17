package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Engine is the session.Engine (and session.CoopEngine) the
// controllers run: the same RunHostSession / RunJoinSession for the CLI
// `host` and `join` subcommands and for the TUI.
//
// The TUI's engine (NewTUIEngine) has ls, the status bridge shared across
// every session of the app run, and puts the co-op questions to the
// controller (its Front carries the session's Output), whose UI answers
// them. The CLI's (NewCLIEngine) has no bridge, so chat and status go to
// stdout as they always have, and stdin answers the questions and takes
// the commands (a Front without an Output): its output is the CLI's,
// line for line.
type Engine struct {
	ls    *LiveState // nil for the CLI
	stdin bool       // the CLI: questions and console commands on stdin

	env       Env     // the host and join sessions' Env
	version   string  // this app's version, in the lobby HELLO
	cheatsIni []byte  // the bundled cheats a Dolphin launch puts in its user folder
	coopApp   CoopApp // co-op; nil runs every session without it

	// game is what a lobby session's START plays on (game.go): this
	// machine's Dolphins. nil starts no game (the tests' engines).
	game *GameDeps

	mu    sync.Mutex
	coop  CoopSide             // the running session's co-op side; nil without
	lobby *network.LobbyClient // the running lobby session's connection (lobby.go); nil without
	game1 *GameRunner          // the running lobby session's game side; nil without
	coop1 LobbyCoop            // the running lobby session's co-op side; nil without
}

func (e *Engine) Host(ctx context.Context, cancel context.CancelFunc, o session.HostOptions, out session.Output) error {
	defer e.begin(out)()
	port := o.Port
	if port == 0 {
		port = DefaultPort
	}
	ctx = WithColorPref(ctx, o.Color)
	return SessionErr(RunHostSession(ctx, cancel, e.env, o.Name, port, out, e.ls, e.sessionCoop(out, o.Coop)))
}

func (e *Engine) Join(ctx context.Context, cancel context.CancelFunc, o session.JoinOptions, out session.Output) error {
	defer e.begin(out)()
	// The app's color goes with its player connections (a lobby join that
	// fell back to this keeps the color it chose).
	ctx = WithColorPref(ctx, o.Color)
	return SessionErr(RunJoinSession(ctx, cancel, e.env, o.Addr, o.Name, out, e.ls, e.sessionCoop(out, o.Coop)))
}

// sessionCoop is the co-op side of a host or join session writing to out
// with options o: nil (none) when o is off or the engine has no co-op.
func (e *Engine) sessionCoop(out session.Output, o session.CoopOptions) Coop {
	if e.coopApp == nil {
		return nil
	}
	return e.coopApp.Session(o, e.Front(out, o))
}

// begin hands the status bridge the session's chat sink, and returns how
// to undo that and forget the session's co-op side.
func (e *Engine) begin(out session.Output) func() {
	if e.ls != nil {
		e.ls.SetChat(out.Chat)
	}
	return func() {
		if e.ls != nil {
			e.ls.SetChat(nil)
		}
		e.Attach(nil)
	}
}

// Front is the co-op front end of a session writing to out with options
// o.
func (e *Engine) Front(out session.Output, o session.CoopOptions) Front {
	if e.stdin {
		return Front{Attach: e.Attach}
	}
	return Front{Out: out, Yes: o.Yes, Attach: e.Attach}
}

// Attach makes cs the running session's co-op side (nil: none), which
// Command, CommandLine and Live reach.
func (e *Engine) Attach(cs CoopSide) {
	e.mu.Lock()
	e.coop = cs
	e.mu.Unlock()
}

// CoopSide is the running session's co-op side, nil without one.
func (e *Engine) CoopSide() CoopSide {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.coop
}

// SessionErr is the error the controller should end the session on: err
// itself, except a fatal connect error, which the session's ConnectLog
// already logged (LogSessionErr skips it the same way), so it is marked
// session.ErrReported: not logged again, but still a failure.
func SessionErr(err error) error {
	if errors.Is(err, network.ErrFatal) {
		return fmt.Errorf("%w: %w", session.ErrReported, err)
	}
	return err
}

func (e *Engine) Live() session.Live {
	var l session.Live
	if e.ls != nil {
		x, y, z, ok := e.ls.LocalPos()
		l = session.Live{
			Players:     e.ls.Players(),
			PlayerCount: e.ls.PlayerCount(),
			HostAddrs:   e.ls.HostIPList(),
			Latency:     e.ls.Latency(),
			LocalX:      x,
			LocalY:      y,
			LocalZ:      z,
			LocalOK:     ok,
		}
	}
	if cs := e.CoopSide(); cs != nil {
		l.CoopStatus, l.CoopHub = cs.CoopLive()
	}
	return l
}

func (e *Engine) SendChat(text string) error {
	if lc := e.lobbyClient(); lc != nil {
		return lc.SendChat(text) // a lobby session chats on its lobby connection
	}
	if e.ls == nil {
		return nil // the CLI has no chat input
	}
	return e.ls.SendChat(text)
}

// Command runs a console command on the running session's co-op side,
// as a typed line runs, and returns the error that kept it from every
// pipeline (no Dolphin runs co-op yet, no such player, say which
// player): logged as well, as the CLI logs it, so a button can tell a
// refusal without reading the log.
func (e *Engine) Command(c session.Command) error {
	cs := e.CoopSide()
	if cs == nil {
		return session.ErrNoCoop
	}
	return cs.CoopCommand(c)
}

// CommandLine runs a console line as typed, logging one that is no
// command, as the CLI's console does. It returns nil for a command
// refused: the line's outcome is in the log, and the TUI, which echoes
// a returned error, would say it twice.
func (e *Engine) CommandLine(line string) error {
	cs := e.CoopSide()
	if cs == nil {
		return session.ErrNoCoop
	}
	cs.CoopLine(line)
	return nil
}

// CoopWorlds lists the co-op worlds, as coop-world list does.
func (e *Engine) CoopWorlds() ([]session.CoopWorld, error) {
	if e.coopApp == nil {
		return nil, session.ErrNoCoop
	}
	return e.coopApp.Worlds()
}

// RunCLISession runs one host or join session through ctrl as the CLI
// subcommands do, and returns their exit code: 1 when the session ended
// on an error, which the controller has logged (the old runHost's
// LogSessionErr), else 0. A session that can't start is logged to rep.
func RunCLISession(ctrl *session.Controller, rep log.Reporter, start func() (session.ID, error)) int {
	id, err := start()
	if err != nil {
		rep.Log(log.Err, err.Error())
		return 1
	}
	if ctrl.Wait(id).Failed {
		return 1
	}
	return 0
}
