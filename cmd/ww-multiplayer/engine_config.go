package main

import (
	"github.com/StephenSHorton/ww-multiplayer/cheats"
	"github.com/StephenSHorton/ww-multiplayer/internal/cliutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// sessionEnv is what a host or join session (engine.RunHostSession,
// engine.RunJoinSession) takes from package main: the pose pipelines'
// feedEnv, and clearMultiplayerState when the session ends.
var sessionEnv = engine.Env{Feed: feedEnv, Clear: clearMultiplayerState}

// coopEnv is what co-op (mods/coop/session) takes from package main:
// cliutil.OpenDolphin.
var coopEnv = coopsession.Env{Open: cliutil.OpenDolphin}

// engineConfig is the multiplayer engine's config from package main: the
// sessions' Env, this build's version and cheats, co-op (the app's runs
// lobby sessions too; the CLI never runs one) and the mods its hellos
// carry.
func engineConfig(coop engine.CoopApp, mods network.Mods) engine.Config {
	return engine.Config{Env: sessionEnv, Version: version, CheatsIni: cheats.GZLE01, Coop: coop, Mods: mods}
}

// netMods is a ModsID as the network sends it (docs/api/02-mods.md,
// "mods_hash").
func netMods(id wwapi.ModsID) network.Mods {
	return network.Mods{Hash: id.Hash, List: id.List, Known: true}
}

// cliMods are the mods the CLI's sessions carry: the compiled-in mods
// resolved as `mods` resolves them (manifest defaults; the CLI never
// reads the app's settings). No engine is built to resolve them.
func cliMods() network.Mods {
	return netMods(wwapi.Resolve(compiledModsWith(nil), wwapi.ResolveOptions{}).ModsID())
}

// newTUIEngine is the app's engine (the TUI's and the window's): the
// status bridge, this machine's Dolphins, and co-op in its lobby
// sessions, with the mods at their defaults (cliMods; startApp's engine
// is newAppEngine's).
func newTUIEngine() *engine.Engine {
	return engine.NewTUIEngine(engineConfig(coopsession.App(coopEnv), cliMods()))
}

// newAppEngine is the engine of the multiplayer mod the app's faces load
// (startApp): the app's engine with co-op when the co-op mod loaded (a
// lobby offers none without), carrying mods, the runtime's
// (wwapi.ModsHash of its mods).
func newAppEngine(coop bool, mods network.Mods) session.Engine {
	if coop {
		return engine.NewTUIEngine(engineConfig(coopsession.App(coopEnv), mods))
	}
	return engine.NewTUIEngine(engineConfig(nil, mods))
}

// newCLIEngine is the CLI's host and join engine.
func newCLIEngine() *engine.Engine {
	return engine.NewCLIEngine(engineConfig(coopsession.CLIApp(coopEnv), cliMods()))
}

// runtimeNetMods are the mods rt runs (wwapi.ModsHash of its mods), the
// zero Mods before rt exists.
func runtimeNetMods(rt *wwapi.Runtime) network.Mods {
	if rt == nil {
		return network.Mods{}
	}
	return netMods(wwapi.ModsHash(rt.Mods()))
}
