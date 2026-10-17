// Package coop is the co-op mod (docs/api/00-design.md §7): one shared
// world for the players of a multiplayer session (docs/08-coop-design.md).
// It requires the multiplayer mod. Its parts are the co-op engine
// (class, derive, inventory, story, bind, world, wire, hub, replica,
// combat, proto, ...), the session (session/, package coopsession), the
// stage tables (stagetables/) and the CLI commands (cli/, package
// coopcli).
//
// Enabling the mod changes nothing by itself: co-op play stays off
// until a player picks it (session.CoopOptions, the app's
// config.Coop.LastMode "off" by default, the CLI's --coop). What the mod
// adds is its developer commands (coop-tables, coop-tag, coop-world,
// save-diff, save-merge; wwapi.CLI) and the stage tables step after
// `patch` (stagetables.CacheForPatch; wwapi.PostPatcher). The sessions
// meet the multiplayer engine through engine.CoopApp, built by package
// main from coopsession.App/CLIApp until D4.
//
// # v0 UNSTABLE
//
// Like wwapi v0, this mod's Go API may change in any 0.x minor version of
// wwapi.
package coop

import (
	_ "embed"
	"io"

	coopcli "github.com/StephenSHorton/ww-multiplayer/mods/coop/cli"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/stagetables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

//go:embed mod.json
var manifestJSON []byte

// Mod is the co-op mod. Its sessions run under the multiplayer engine,
// not in Run, so it embeds wwapi.NoRun.
type Mod struct {
	wwapi.NoRun

	env coopsession.Env
}

// New returns the co-op mod over env (package main's openDolphin), which
// its commands use to reach Dolphin.
func New(env coopsession.Env) *Mod { return &Mod{env: env} }

// Manifest is the embedded mod.json. ManifestFrom turns a bad one into a
// FAILED mod instead of a crash.
func (*Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifestJSON) }

// Init registers nothing yet: the sessions run under the multiplayer
// engine (D4 moves them onto the bus).
func (*Mod) Init(*wwapi.Context) error { return nil }

// Commands are co-op's CLI commands (coopcli.Commands), all hidden
// developer tools.
func (m *Mod) Commands() []wwapi.Command { return coopcli.Commands(m.env) }

// PostPatch builds, caches and records the co-op stage tables of image,
// the disc Dolphin will run, and writes one line about it on w
// (stagetables.CacheForPatch). It never fails the patch.
func (*Mod) PostPatch(w io.Writer, image string) { stagetables.CacheForPatch(w, image) }

// The interfaces the mod implements.
var (
	_ wwapi.Mod         = (*Mod)(nil)
	_ wwapi.CLI         = (*Mod)(nil)
	_ wwapi.PostPatcher = (*Mod)(nil)
)
