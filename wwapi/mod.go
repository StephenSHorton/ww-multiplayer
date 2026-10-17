package wwapi

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// Mod is a compiled-in mod.
type Mod interface {
	// Manifest returns the mod's embedded mod.json, parsed. Use
	// ManifestFrom so a bad mod.json becomes a FAILED mod, not a crash.
	Manifest() Manifest
	// Init registers what the mod needs (event handlers, config keys)
	// through c. It runs once, in dependency order, and must not block.
	// An error or a panic leaves the mod FAILED, and mods that require
	// it fail too.
	Init(c *Context) error
	// Run does the mod's long-running work until ctx ends, and returns
	// nil then. A mod with nothing to run embeds NoRun. A panic or an
	// error quarantines only this mod; an error returned once ctx has
	// ended is logged as the mod stopping, not quarantined.
	Run(ctx context.Context) error
}

// NoRun is embedded by mods that have no long-running work.
type NoRun struct{}

// Run returns nil at once.
func (NoRun) Run(context.Context) error { return nil }

// Optional interfaces a Mod may implement, found by type assertion.
// More come with the packages they need (design §6.1): CLI with the
// command registry, UI (verbs and panels) and NetChannel with the
// multiplayer mod.
type (
	// PublicAPI is what other mods get from Context.Mod(id).
	PublicAPI interface{ API() any }

	// NetProvider is the network session provider: the one mod whose
	// manifest "provides" ["net"]. The resolver requires the two to agree.
	NetProvider interface{ Engine() session.Engine }
)
