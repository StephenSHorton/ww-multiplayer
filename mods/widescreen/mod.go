// Package widescreen is the widescreen mod: one real aspect for the
// camera, the culling and (from later stages) the 2D layers. The C side
// (inject/src/ws) owns the WWWS block and the clipper hook; this package
// is the Go side: the setting parsers and the `widescreen` CLI command.
// Plans: docs/design/widescreen-culling.md, docs/design/native-widescreen.md.
//
// # v0 UNSTABLE
//
// Like wwapi v0, this package may change in any 0.x minor version.
package widescreen

import (
	_ "embed"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

//go:embed mod.json
var manifestJSON []byte

// Mod is the widescreen mod. The app's writer (RunDolphin) runs in the
// engine's Dolphin runner, not under the mod runtime (no Game yet), so
// it embeds wwapi.NoRun.
type Mod struct {
	wwapi.NoRun
	env Env
}

// New returns the mod over env.
func New(env Env) *Mod { return &Mod{env: env} }

// Manifest is the embedded mod.json.
func (*Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifestJSON) }

// Init registers nothing.
func (*Mod) Init(*wwapi.Context) error { return nil }

// Commands is the `widescreen` CLI command.
func (m *Mod) Commands() []wwapi.Command { return []wwapi.Command{Command(m.env)} }

var (
	_ wwapi.Mod = (*Mod)(nil)
	_ wwapi.CLI = (*Mod)(nil)
)
