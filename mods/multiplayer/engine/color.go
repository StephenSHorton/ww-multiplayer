package engine

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// ColorPref is the tunic color this player asks the server for
// (docs/09-player-colors.md): WW_COLOR, overridden by a --color option
// anywhere before a bare "--". nil lets the server pick. Package main
// sets it from the command line before any session starts.
var ColorPref *playercolor.Color

// NewPlayerClient is network.NewClient with this player's color
// preference set.
func NewPlayerClient(name string) *network.Client {
	c := network.NewClient(name)
	c.SetColorPreference(ColorPref)
	return c
}

// colorPrefKey is the context key of a session's own color preference.
type colorPrefKey struct{}

// WithColorPref returns ctx carrying a session's color preference (the
// app's JoinOptions.Color or HostOptions.Color), which overrides the
// process-wide --color / WW_COLOR for the player connections the session
// opens. A nil pref leaves ctx as it is.
func WithColorPref(ctx context.Context, pref *playercolor.Color) context.Context {
	if pref == nil {
		return ctx
	}
	p := *pref
	return context.WithValue(ctx, colorPrefKey{}, &p)
}

// ColorPrefFor is the color preference for a player connection under ctx.
func ColorPrefFor(ctx context.Context) *playercolor.Color {
	if p, ok := ctx.Value(colorPrefKey{}).(*playercolor.Color); ok {
		return p
	}
	return ColorPref
}

// NewPlayerClientCtx is NewPlayerClient with the session's preference
// from ctx, when it has one.
func NewPlayerClientCtx(ctx context.Context, name string) *network.Client {
	c := network.NewClient(name)
	c.SetColorPreference(ColorPrefFor(ctx))
	return c
}
