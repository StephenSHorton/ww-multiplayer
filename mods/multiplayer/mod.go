// Package multiplayer is the multiplayer mod (docs/api/00-design.md §7):
// the network session provider ("provides": ["net"]). Its parts are the
// session engine (engine/: host, join and lobby sessions, the game side
// of a lobby's Start), the pose pipelines (feed/), the tunic colors
// (tunicsync/, playercolor/) and the wire (net/).
//
// The mod is compiled in (mods.go's compiledMods). It hands the engine
// out as wwapi.NetProvider: the app's faces run the mods under a
// wwapi.Runtime and drive this engine through the session controller,
// which adapts the runtime's event bus (EventMod); the CLI's host and
// join still build theirs (package main's newCLIEngine). Co-op is its own
// mod requiring this one, and meets the engine through engine.CoopApp.
//
// # v0 UNSTABLE
//
// Like wwapi v0, this mod's Go API may change in any 0.x minor version of
// wwapi.
package multiplayer

import (
	_ "embed"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

//go:embed mod.json
var manifestJSON []byte

// Mod is the multiplayer mod. Its sessions run under the session
// controller, not in Run, so it embeds wwapi.NoRun.
type Mod struct {
	wwapi.NoRun

	newEngine func() session.Engine
	once      sync.Once
	engine    session.Engine
}

// New returns the multiplayer mod, whose session engine newEngine builds
// on the first Engine call (package main's, which knows co-op and this
// build's version).
func New(newEngine func() session.Engine) *Mod { return &Mod{newEngine: newEngine} }

// Manifest is the embedded mod.json. ManifestFrom turns a bad one into a
// FAILED mod instead of a crash.
func (*Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifestJSON) }

// Init registers nothing yet: the sessions run under the session
// controller, which adapts the runtime's bus.
func (*Mod) Init(*wwapi.Context) error { return nil }

// Engine is the session engine (wwapi.NetProvider), built once; nil when
// the mod was made without one.
func (m *Mod) Engine() session.Engine {
	m.once.Do(func() {
		if m.newEngine != nil {
			m.engine = m.newEngine()
		}
	})
	return m.engine
}

// API is the multiplayer mod's public API (wwapi.PublicAPI), what other
// mods get from Context.Mod("multiplayer").
func (*Mod) API() any { return API{} }

// API is what the multiplayer mod offers other mods.
type API struct{}

// ValidatePlayerName reports why the server won't admit a player name,
// or nil (network.ValidatePlayerName).
func (API) ValidatePlayerName(name string) error { return network.ValidatePlayerName(name) }

// The interfaces the mod implements.
var (
	_ wwapi.Mod         = (*Mod)(nil)
	_ wwapi.NetProvider = (*Mod)(nil)
	_ wwapi.PublicAPI   = (*Mod)(nil)
)
