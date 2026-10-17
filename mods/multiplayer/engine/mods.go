package engine

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// modsKey is the context key of a session's mods.
type modsKey struct{}

// WithMods returns ctx carrying this build's mods (Config.Mods), which the
// player connections a session opens send in their hello and a host's
// server checks joiners against (docs/api/02-mods.md, "mods_hash"). The
// zero Mods leaves ctx as it is.
func WithMods(ctx context.Context, m network.Mods) context.Context {
	if !m.Known {
		return ctx
	}
	return context.WithValue(ctx, modsKey{}, m)
}

// ModsFor is the mods under ctx; the zero Mods (send and check none)
// without.
func ModsFor(ctx context.Context) network.Mods {
	m, _ := ctx.Value(modsKey{}).(network.Mods)
	return m
}

// Mods are this engine's mods (Config.Mods).
func (e *Engine) Mods() network.Mods { return e.mods }
