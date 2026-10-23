package coopsession

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Env is what co-op takes from package main, as the multiplayer engine
// takes engine.Env: Open attaches to the Dolphin with this PID, 0 for
// the one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select (openDolphin).
type Env struct {
	Open func(pid uint32) (*emu.Dolphin, error)
}

// App is co-op for the app's engine (the TUI's and the window's): host
// and join sessions, and lobby sessions on this machine's Dolphins.
func App(env Env) engine.CoopApp { return coopApp{env: env, lobby: realAppCoopDeps(env)} }

// CLIApp is co-op for the CLI's host and join engine, which never runs a
// lobby session.
func CLIApp(env Env) engine.CoopApp { return coopApp{env: env} }

// coopApp is co-op as the multiplayer engine meets it (engine.CoopApp):
// coopHook for a host or join session, appCoop for a lobby session's on
// lobby (nil: none), the worlds under world.Dir() and the app's
// coop-sync, on the Dolphins env opens.
type coopApp struct {
	env   Env
	lobby *appCoopDeps
}

func (a coopApp) Session(o session.CoopOptions, f engine.Front) engine.Coop {
	return newCoopHook(a.env, coopFlagsOf(o), frontOf(f))
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
	ws, err := ListWorlds(root)
	if err != nil {
		return nil, err
	}
	out := make([]session.CoopWorld, 0, len(ws))
	for _, w := range ws {
		cw := session.CoopWorld{ID: w.id.String(), Created: w.created, Seq: w.seq, Text: w.String(),
			InUse: w.marker != nil && w.use != MarkerStale, Stale: w.marker != nil && w.use == MarkerStale}
		if w.err != nil {
			cw.Err = w.err.Error()
		}
		out = append(out, cw)
	}
	return out, nil
}

func (a coopApp) Sync(ctx context.Context, name, addr string, pid uint32, rep log.Reporter, cs engine.CoopSide, claimed func(pid uint32) bool) error {
	return appCoopSync(ctx, a.env, name, addr, pid, rep, cs.(*Session), claimed)
}

// frontOf is the coopFront of the engine's front end f: the CLI's (no
// Output) asks on stdin and has no advice sink; the app's puts the
// questions to the controller (outputAsker, carrying --yes) and passes
// the advice on. Both attach the session to the engine.
func frontOf(f engine.Front) coopFront {
	attach := func(cs *Session) {
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

// coopFlagsOf is the CLI's Flags for a controller's options.
func coopFlagsOf(o session.CoopOptions) Flags {
	return Flags{on: o.On, world: o.World, merge: o.Merge, yes: o.Yes, dryRun: o.DryRun, liveSwitches: o.LiveSwitches, iso: o.ISO,
		bossKill: o.BossKill, refresh: o.Refresh}
}

// options is f as the controller's options.
func (f Flags) Options() session.CoopOptions {
	return session.CoopOptions{On: f.on, World: f.world, Merge: f.merge, Yes: f.yes, DryRun: f.dryRun, LiveSwitches: f.liveSwitches, ISO: f.iso,
		BossKill: f.bossKill, Refresh: f.refresh}
}

// CoopLive is the pipelines' status and the host's hub (engine.CoopSide).
func (cs *Session) CoopLive() ([]session.CoopStatus, *session.CoopHubStatus) {
	return cs.console.statuses(), coopHubStatus(cs.console.hubStatus())
}

// CoopCommand runs one console command as a typed line runs
// (engine.CoopSide).
func (cs *Session) CoopCommand(c session.Command) error {
	cmd, err := commandOf(c)
	if err != nil {
		return err
	}
	return cs.console.exec(cs.console.sessionCtx(), cmd)
}

// CoopLine runs one console line as typed (engine.CoopSide).
func (cs *Session) CoopLine(line string) { cs.console.do(cs.console.sessionCtx(), line) }

// coopHook is the engine.Coop of a host or join session run with co-op
// (--coop): the Session that startCoopHost or startCoopSession opens
// with front as its front end, its coop-sync pipeline (RunSync)
// and its warps' pose clients (Session.WithPoseClient).
type coopHook struct {
	env   Env
	co    Flags
	front coopFront
	cs    *Session // nil until started
}

// newCoopHook is the co-op side of a session with co, or nil (no co-op)
// when co is off.
func newCoopHook(env Env, co Flags, front coopFront) engine.Coop {
	if !co.on {
		return nil
	}
	return &coopHook{env: env, co: co, front: front}
}

// sessionEnv is the co-op environment of this Dolphin with the hook's
// front end.
func (h *coopHook) sessionEnv() coopEnv {
	env := defaultCoopEnv(h.env, 0)
	env.front = h.front
	return env
}

// StartHost backs up the cards, opens or founds the co-op world and sets
// the CoopHub as srv's Coop.
func (h *coopHook) StartHost(ctx context.Context, rep log.Reporter, srv *network.Server) error {
	if err := contractErrPID(h.env, 0); err != nil {
		return err // another version's patch or save state: no world is founded or served from it
	}
	cs, err := startCoopHost(ctx, rep, srv.OnLog, h.co, h.sessionEnv())
	if err != nil {
		return err
	}
	h.cs = cs
	srv.Coop = cs.hub
	return nil
}

// StartJoin backs up the cards and opens the joiner's co-op side.
func (h *coopHook) StartJoin(rep log.Reporter) {
	h.cs = startCoopSession(rep, h.co, h.sessionEnv())
}

func (h *coopHook) WithPoseClient(name string, next func(*network.Client)) func(*network.Client) {
	return h.cs.WithPoseClient(name, next)
}

// Pipelines is coop-sync, with its own Dolphin handle and co-op
// connection, and claimed, the game's enemy-mirror claims, for its K1.
func (h *coopHook) Pipelines(name, addr string, rep log.Reporter, claimed func(pid uint32) bool) []engine.Pipeline {
	return []engine.Pipeline{{Name: "coop-sync", Run: func(ctx context.Context) error {
		return RunSync(ctx, h.env, name, addr, 0, rep, h.cs, claimed)
	}}}
}

// Stop ends the hub, which writes the world (Session.stop).
func (h *coopHook) Stop() { h.cs.Stop() }
