package wwapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// ConfigSource is where mod settings come from. The app's faces fill it
// from their saved settings (config.Mods); the CLI passes nil and runs on
// manifest defaults. wwapi never imports internal/config.
type ConfigSource interface {
	// ModEnabled is mods.<id>.enabled; set is false when unset.
	ModEnabled(id string) (on, set bool)
	// ModSettings is mods.<id>.settings (a JSON object), or nil.
	ModSettings(id string) json.RawMessage
}

// ConfigSection is one mod's settings.
type ConfigSection struct {
	raw json.RawMessage
}

// Raw returns the settings JSON, or nil when there is none.
func (c ConfigSection) Raw() json.RawMessage { return bytes.Clone(c.raw) }

// Decode unmarshals the settings into v. With no settings it leaves v
// alone and returns nil, so v's defaults stand.
func (c ConfigSection) Decode(v any) error {
	if len(bytes.TrimSpace(c.raw)) == 0 {
		return nil
	}
	return json.Unmarshal(c.raw, v)
}

// Get unmarshals one top-level key into v and reports whether it was set.
func (c ConfigSection) Get(key string, v any) (bool, error) {
	if len(bytes.TrimSpace(c.raw)) == 0 {
		return false, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(c.raw, &m); err != nil {
		return false, fmt.Errorf("mod settings: %w", err)
	}
	raw, ok := m[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

// Context is a mod's handle on the runtime, bound to that mod. What a mod
// registers through it (event subscriptions today; commands, verbs and
// write ranges later) belongs to the mod, so quarantine can remove it.
//
// More arrives with the packages behind it (design §6.2): Game (wwapi.Game
// exists; the runtime hands it out from D4), Commands, and Allow for
// write ranges (wwapi/write).
type Context struct {
	rt   *Runtime
	m    *modState
	log  log.Reporter
	bus  *events.Bus
	cfg  ConfigSection
	data string
}

// ID is the mod's id.
func (c *Context) ID() string { return c.m.e.Manifest.ID }

// Manifest is the mod's parsed mod.json.
func (c *Context) Manifest() Manifest { return c.m.e.Manifest }

// Log reports through the runtime's Reporter with a "[id] " prefix.
func (c *Context) Log() log.Reporter { return c.log }

// Events is the mod's view of the event bus: it emits only mod.<id>.*
// and owns the subscriptions made through it, which end when the mod is
// quarantined or the runtime stops.
func (c *Context) Events() *events.Bus { return c.bus }

// Config is the mod's settings.
func (c *Context) Config() ConfigSection { return c.cfg }

// DataDir is the mod's own directory (Options.DataRoot/<id>), or "" when
// the runtime has none. It is not created.
func (c *Context) DataDir() string { return c.data }

// Features is what the patched blob provides (unknown until the contract
// check exists).
func (c *Context) Features() Features { return c.rt.opts.Resolve.Features }

// Has reports whether the mod id is loaded and not quarantined.
func (c *Context) Has(id string) bool {
	s, ok := c.rt.status(id)
	return ok && s.State == Loaded
}

// Mod returns another loaded mod's public API (its PublicAPI.API()), and
// false when that mod is not loaded or has no public API.
func (c *Context) Mod(id string) (any, bool) {
	m := c.rt.byID[id]
	if m == nil || !c.Has(id) {
		return nil, false
	}
	p, ok := m.e.Mod.(PublicAPI)
	if !ok {
		return nil, false
	}
	return p.API(), true
}

// prefixed prefixes every line with "[id] ".
type prefixed struct {
	r      log.Reporter
	prefix string
}

func (p prefixed) Log(level log.Level, msg string) { p.r.Log(level, p.prefix+msg) }

func newContext(rt *Runtime, m *modState, bus *events.Bus) *Context {
	id := m.e.Manifest.ID
	c := &Context{rt: rt, m: m, bus: bus, log: prefixed{rt.log, "[" + id + "] "}}
	if rt.opts.Config != nil {
		c.cfg = ConfigSection{raw: bytes.Clone(rt.opts.Config.ModSettings(id))}
	}
	if rt.opts.DataRoot != "" {
		c.data = filepath.Join(rt.opts.DataRoot, id)
	}
	return c
}
