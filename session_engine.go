package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// sessionEngine is the session.Engine (and session.CoopEngine) the
// controllers run: the same runHostSession / runJoinSession for the CLI
// `host` and `join` subcommands and for the TUI.
//
// The TUI's engine (newTUIEngine) has ls, the status bridge shared across
// every session of the app run, and puts the co-op questions to the
// controller (outputAsker), whose UI answers them. The CLI's
// (newCLIEngine) has no bridge, so chat and status go to stdout as they
// always have, and stdin answers the questions and takes the commands
// (coopPrompter): its output is the CLI's, line for line.
type sessionEngine struct {
	ls    *liveState // nil for the CLI
	stdin bool       // the CLI: questions and console commands on stdin

	mu    sync.Mutex
	coop  *coopSession         // the running session's co-op side; nil without
	lobby *network.LobbyClient // the running lobby session's connection (lobby_engine.go); nil without
}

func newTUIEngine() *sessionEngine { return &sessionEngine{ls: &liveState{}} }

func newCLIEngine() *sessionEngine { return &sessionEngine{stdin: true} }

func (e *sessionEngine) Host(ctx context.Context, cancel context.CancelFunc, o session.HostOptions, out session.Output) error {
	defer e.begin(out)()
	port := o.Port
	if port == 0 {
		port = defaultPort
	}
	ctx = withColorPref(ctx, o.Color)
	return sessionErr(runHostSession(ctx, cancel, o.Name, port, out, e.ls, coopFlagsOf(o.Coop), e.front(out, o.Coop)))
}

func (e *sessionEngine) Join(ctx context.Context, cancel context.CancelFunc, o session.JoinOptions, out session.Output) error {
	defer e.begin(out)()
	// The app's color goes with its player connections (a lobby join that
	// fell back to this keeps the color it chose).
	ctx = withColorPref(ctx, o.Color)
	return sessionErr(runJoinSession(ctx, cancel, o.Addr, o.Name, out, e.ls, coopFlagsOf(o.Coop), e.front(out, o.Coop)))
}

// begin hands the status bridge the session's chat sink, and returns how
// to undo that and forget the session's co-op side.
func (e *sessionEngine) begin(out session.Output) func() {
	if e.ls != nil {
		e.ls.setChat(out.Chat)
	}
	return func() {
		if e.ls != nil {
			e.ls.setChat(nil)
		}
		e.attach(nil)
	}
}

// front is the co-op front end of a session writing to out.
func (e *sessionEngine) front(out session.Output, o session.CoopOptions) coopFront {
	if e.stdin {
		return coopFront{attach: e.attach}
	}
	return coopFront{ask: outputAsker{out: out, yes: o.Yes}, advise: out.Advise, attach: e.attach}
}

func (e *sessionEngine) attach(cs *coopSession) {
	e.mu.Lock()
	e.coop = cs
	e.mu.Unlock()
}

func (e *sessionEngine) coopSide() *coopSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.coop
}

// coopFlagsOf is the CLI's coopFlags for a controller's options.
func coopFlagsOf(o session.CoopOptions) coopFlags {
	return coopFlags{on: o.On, world: o.World, merge: o.Merge, yes: o.Yes, dryRun: o.DryRun, liveSwitches: o.LiveSwitches, iso: o.ISO}
}

// options is f as the controller's options.
func (f coopFlags) options() session.CoopOptions {
	return session.CoopOptions{On: f.on, World: f.world, Merge: f.merge, Yes: f.yes, DryRun: f.dryRun, LiveSwitches: f.liveSwitches, ISO: f.iso}
}

// sessionErr is the error the controller should end the session on: err
// itself, except a fatal connect error, which the session's ConnectLog
// already logged (logSessionErr skips it the same way), so it is marked
// session.ErrReported: not logged again, but still a failure.
func sessionErr(err error) error {
	if errors.Is(err, network.ErrFatal) {
		return fmt.Errorf("%w: %w", session.ErrReported, err)
	}
	return err
}

func (e *sessionEngine) Live() session.Live {
	var l session.Live
	if e.ls != nil {
		x, y, z, ok := e.ls.localPos()
		l = session.Live{
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
	if cs := e.coopSide(); cs != nil {
		l.CoopStatus = cs.console.statuses()
	}
	return l
}

func (e *sessionEngine) SendChat(text string) error {
	if lc := e.lobbyClient(); lc != nil {
		return lc.SendChat(text) // a lobby session chats on its lobby connection
	}
	if e.ls == nil {
		return nil // the CLI has no chat input
	}
	return e.ls.sendChat(text)
}

// Command runs a console command on the running session's co-op side,
// as a typed line runs, and returns the error that kept it from every
// pipeline (no Dolphin runs co-op yet, no such player, say which
// player): logged as well, as the CLI logs it, so a button can tell a
// refusal without reading the log.
func (e *sessionEngine) Command(c session.Command) error {
	cs := e.coopSide()
	if cs == nil {
		return session.ErrNoCoop
	}
	cmd, err := commandOf(c)
	if err != nil {
		return err
	}
	return cs.console.exec(cs.console.sessionCtx(), cmd)
}

// CommandLine runs a console line as typed, logging one that is no
// command, as the CLI's console does. It returns nil for a command
// refused: the line's outcome is in the log, and the TUI, which echoes
// a returned error, would say it twice.
func (e *sessionEngine) CommandLine(line string) error {
	cs := e.coopSide()
	if cs == nil {
		return session.ErrNoCoop
	}
	cs.console.do(cs.console.sessionCtx(), line)
	return nil
}

// CoopWorlds lists the worlds under world.Dir(), as coop-world list does.
func (e *sessionEngine) CoopWorlds() ([]session.CoopWorld, error) {
	root, err := world.Dir()
	if err != nil {
		return nil, err
	}
	ws, err := listCoopWorlds(root)
	if err != nil {
		return nil, err
	}
	out := make([]session.CoopWorld, 0, len(ws))
	for _, w := range ws {
		cw := session.CoopWorld{ID: w.id.String(), Created: w.created, Seq: w.seq, Text: w.String(),
			InUse: w.marker != nil && w.use != markerStale}
		if w.err != nil {
			cw.Err = w.err.Error()
		}
		out = append(out, cw)
	}
	return out, nil
}

// runCLISession runs one host or join session through ctrl as the CLI
// subcommands do, and returns their exit code: 1 when the session ended
// on an error, which the controller has logged (the old runHost's
// logSessionErr), else 0. A session that can't start is logged to rep.
func runCLISession(ctrl *session.Controller, rep report.Reporter, start func() (session.ID, error)) int {
	id, err := start()
	if err != nil {
		rep.Log(report.Err, err.Error())
		return 1
	}
	if ctrl.Wait(id).Failed {
		return 1
	}
	return 0
}
