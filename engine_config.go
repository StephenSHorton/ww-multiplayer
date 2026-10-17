package main

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
)

// sessionEnv is what a host or join session (engine.RunHostSession,
// engine.RunJoinSession) takes from package main: the pose pipelines'
// feedEnv, and clearMultiplayerState when the session ends.
var sessionEnv = engine.Env{Feed: feedEnv, Clear: clearMultiplayerState}

// coopEnv is what co-op (mods/coop/session) takes from package main:
// openDolphin.
var coopEnv = coopsession.Env{Open: openDolphin}

// engineConfig is the multiplayer engine's config from package main: the
// sessions' Env, this build's version and cheats, and co-op (the app's
// runs lobby sessions too; the CLI never runs one).
func engineConfig(coop engine.CoopApp) engine.Config {
	return engine.Config{Env: sessionEnv, Version: version, CheatsIni: bundledCheatsIni, Coop: coop}
}

// newTUIEngine is the app's engine (the TUI's and the window's): the
// status bridge, this machine's Dolphins, and co-op in its lobby
// sessions.
func newTUIEngine() *engine.Engine {
	return engine.NewTUIEngine(engineConfig(coopsession.App(coopEnv)))
}

// newAppEngine is the engine of the multiplayer mod the app's faces load
// (startApp): newTUIEngine when the co-op mod loaded, else the same
// engine without co-op (a lobby offers none).
func newAppEngine(coop bool) session.Engine {
	if coop {
		return newTUIEngine()
	}
	return engine.NewTUIEngine(engineConfig(nil))
}

// newCLIEngine is the CLI's host and join engine.
func newCLIEngine() *engine.Engine {
	return engine.NewCLIEngine(engineConfig(coopsession.CLIApp(coopEnv)))
}
