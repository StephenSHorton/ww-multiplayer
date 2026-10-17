package main

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// sessionEnv is what a host or join session (engine.RunHostSession,
// engine.RunJoinSession) takes from package main: the pose pipelines'
// feedEnv, and clearMultiplayerState when the session ends.
var sessionEnv = engine.Env{Feed: feedEnv, Clear: clearMultiplayerState}

// engineConfig is the multiplayer engine's config from package main: the
// sessions' Env, this build's version and cheats, and co-op, whose lobby
// sessions play on lobby (nil: lobby sessions run without co-op, as the
// CLI never runs one).
func engineConfig(lobby *appCoopDeps) engine.Config {
	return engine.Config{Env: sessionEnv, Version: version, CheatsIni: bundledCheatsIni, Coop: coopApp{lobby: lobby}}
}

// newTUIEngine is the app's engine (the TUI's and the window's): the
// status bridge, this machine's Dolphins, and co-op in its lobby
// sessions.
func newTUIEngine() *engine.Engine { return engine.NewTUIEngine(engineConfig(realAppCoopDeps())) }

// newCLIEngine is the CLI's host and join engine.
func newCLIEngine() *engine.Engine { return engine.NewCLIEngine(engineConfig(nil)) }

// coopApp is co-op as the multiplayer engine meets it (engine.CoopApp):
// coopHook for a host or join session, appCoop for a lobby session's on
// lobby (nil: none), the worlds under world.Dir() and the app's
// coop-sync.
type coopApp struct{ lobby *appCoopDeps }

func (coopApp) Session(o session.CoopOptions, f engine.Front) engine.Coop {
	return newCoopHook(coopFlagsOf(o), frontOf(f))
}

func (a coopApp) Lobby(ctx context.Context, env engine.LobbyEnv) engine.LobbyCoop {
	if a.lobby == nil {
		return nil
	}
	return newAppCoop(ctx, a.lobby, env)
}

// Worlds lists the worlds under world.Dir(), as coop-world list does.
func (coopApp) Worlds() ([]session.CoopWorld, error) {
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
			InUse: w.marker != nil && w.use != markerStale, Stale: w.marker != nil && w.use == markerStale}
		if w.err != nil {
			cw.Err = w.err.Error()
		}
		out = append(out, cw)
	}
	return out, nil
}

func (coopApp) Sync(ctx context.Context, name, addr string, pid uint32, rep log.Reporter, cs engine.CoopSide) error {
	return appCoopSync(ctx, name, addr, pid, rep, cs.(*coopSession))
}

// frontOf is the coopFront of the engine's front end f: the CLI's (no
// Output) asks on stdin and has no advice sink; the app's puts the
// questions to the controller (outputAsker, carrying --yes) and passes
// the advice on. Both attach the session to the engine.
func frontOf(f engine.Front) coopFront {
	attach := func(cs *coopSession) {
		if f.Attach == nil {
			return
		}
		if cs == nil {
			f.Attach(nil) // no typed nil in the engine's CoopSide
			return
		}
		f.Attach(cs)
	}
	if f.Out == nil {
		return coopFront{attach: attach}
	}
	return coopFront{ask: outputAsker{out: f.Out, yes: f.Yes}, advise: f.Out.Advise, attach: attach}
}

// coopFlagsOf is the CLI's coopFlags for a controller's options.
func coopFlagsOf(o session.CoopOptions) coopFlags {
	return coopFlags{on: o.On, world: o.World, merge: o.Merge, yes: o.Yes, dryRun: o.DryRun, liveSwitches: o.LiveSwitches, iso: o.ISO}
}

// options is f as the controller's options.
func (f coopFlags) options() session.CoopOptions {
	return session.CoopOptions{On: f.on, World: f.world, Merge: f.merge, Yes: f.yes, DryRun: f.dryRun, LiveSwitches: f.liveSwitches, ISO: f.iso}
}

// CoopLive is the pipelines' status and the host's hub (engine.CoopSide).
func (cs *coopSession) CoopLive() ([]session.CoopStatus, *session.CoopHubStatus) {
	return cs.console.statuses(), coopHubStatus(cs.console.hubStatus())
}

// CoopCommand runs one console command as a typed line runs
// (engine.CoopSide).
func (cs *coopSession) CoopCommand(c session.Command) error {
	cmd, err := commandOf(c)
	if err != nil {
		return err
	}
	return cs.console.exec(cs.console.sessionCtx(), cmd)
}

// CoopLine runs one console line as typed (engine.CoopSide).
func (cs *coopSession) CoopLine(line string) { cs.console.do(cs.console.sessionCtx(), line) }

// coopHook is the engine.Coop of a host or join session run with co-op
// (--coop): the coopSession that startCoopHost or startCoopSession opens
// with front as its front end, its coop-sync pipeline (runCoopSyncCtx)
// and its warps' pose clients (coopSession.withPoseClient).
type coopHook struct {
	co    coopFlags
	front coopFront
	cs    *coopSession // nil until started
}

// newCoopHook is the co-op side of a session with co, or nil (no co-op)
// when co is off.
func newCoopHook(co coopFlags, front coopFront) engine.Coop {
	if !co.on {
		return nil
	}
	return &coopHook{co: co, front: front}
}

// env is the co-op environment of this Dolphin with the hook's front end.
func (h *coopHook) env() coopEnv {
	env := defaultCoopEnv(0)
	env.front = h.front
	return env
}

// StartHost backs up the cards, opens or founds the co-op world and sets
// the CoopHub as srv's Coop.
func (h *coopHook) StartHost(ctx context.Context, rep log.Reporter, srv *network.Server) error {
	cs, err := startCoopHost(ctx, rep, srv.OnLog, h.co, h.env())
	if err != nil {
		return err
	}
	h.cs = cs
	srv.Coop = cs.hub
	return nil
}

// StartJoin backs up the cards and opens the joiner's co-op side.
func (h *coopHook) StartJoin(rep log.Reporter) {
	h.cs = startCoopSession(rep, h.co, h.env())
}

func (h *coopHook) WithPoseClient(name string, next func(*network.Client)) func(*network.Client) {
	return h.cs.withPoseClient(name, next)
}

// Pipelines is coop-sync, with its own Dolphin handle and co-op
// connection.
func (h *coopHook) Pipelines(name, addr string, rep log.Reporter) []engine.Pipeline {
	return []engine.Pipeline{{Name: "coop-sync", Run: func(ctx context.Context) error {
		return runCoopSyncCtx(ctx, name, addr, 0, rep, h.cs)
	}}}
}

// Stop ends the hub, which writes the world (coopSession.stop).
func (h *coopHook) Stop() { h.cs.stop() }
