// Package engine is the multiplayer session core (docs/api/00-design.md
// §7): the status bridge between a running session and the controller
// (LiveState), the host and join sessions, the pipelines they run, the
// standalone server and fake client, and the player clients with this
// player's color preference.
//
// Package main keeps the CLI parsing (--port, --color, the host/join/
// server argument parsers) and hands the parsed values in.
package engine

import "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"

// Config is what an Engine is built from. Package main fills Env,
// Version, CheatsIni, Coop and Mods; NewTUIEngine adds the status bridge
// and this machine's Dolphins.
type Config struct {
	// Live is the status bridge (the TUI's); nil for the CLI.
	Live *LiveState
	// Stdin says the co-op questions and console commands come on stdin
	// (the CLI).
	Stdin bool
	// Game is what a lobby session's START plays on; nil starts no game.
	Game *GameDeps
	// Env is the host and join sessions' Env.
	Env Env
	// Version is this app's version, sent in the lobby HELLO.
	Version string
	// CheatsIni is the bundled cheats file a Dolphin launch puts in its
	// user folder when asked.
	CheatsIni []byte
	// Coop is co-op (package main's until D2 makes it its own mod); nil
	// runs every session without co-op.
	Coop CoopApp
	// Mods is this build's client_required mods (wwapi.ModsID): the
	// sessions' player and lobby connections send their hash, and a
	// host refuses a joiner with another. The zero Mods sends and checks
	// none.
	Mods network.Mods
}

// New is the engine c describes.
func New(c Config) *Engine {
	return &Engine{ls: c.Live, stdin: c.Stdin, game: c.Game, env: c.Env, version: c.Version, cheatsIni: c.CheatsIni, coopApp: c.Coop, mods: c.Mods}
}

// NewTUIEngine is the app's engine: c with a status bridge of its own and
// this machine's Dolphins for the lobby sessions' game side.
func NewTUIEngine(c Config) *Engine {
	c.Live, c.Stdin = &LiveState{}, false
	c.Game = realGameDeps(c.Live, c)
	return New(c)
}

// NewCLIEngine is the CLI's engine: c with the co-op questions and
// console commands on stdin, and no status bridge or game side.
func NewCLIEngine(c Config) *Engine {
	c.Live, c.Stdin, c.Game = nil, true, nil
	return New(c)
}
