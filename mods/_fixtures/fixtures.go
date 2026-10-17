// Package fixtures holds test mods for the wwapi runtime: one folder per
// mod, each with the mod.json it embeds. Their ids start with "_", and
// the folder starts with "_" so `go build ./...` and the app never pick
// it up; wwapi's tests import it by path.
//
// # v0 UNSTABLE
//
// Like wwapi v0, these fixtures may change in any 0.x minor version.
//
// A folder with an invalid.txt holds a mod.json that must be refused;
// tools/validate_manifests checks that the error contains that text.
package fixtures

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
)

//go:embed _bad_manifest/mod.json _bad_range/mod.json _api_mismatch/mod.json
//go:embed _missing_dep/mod.json _cycle_a/mod.json _cycle_b/mod.json
//go:embed _init_error/mod.json _run_panic/mod.json _handler_panic/mod.json
//go:embed _ok_base/mod.json _ok_dependent/mod.json
var files embed.FS

// IDs lists every fixture, by folder name.
var IDs = []string{
	"_api_mismatch", "_bad_manifest", "_bad_range", "_cycle_a", "_cycle_b",
	"_handler_panic", "_init_error", "_missing_dep", "_ok_base",
	"_ok_dependent", "_run_panic",
}

// ManifestJSON returns a fixture's embedded mod.json.
func ManifestJSON(id string) []byte {
	b, err := files.ReadFile(id + "/mod.json")
	if err != nil {
		panic("fixtures: no fixture " + id)
	}
	return b
}

// ErrInit is what _init_error's Init returns.
var ErrInit = errors.New("fixture Init failed on purpose")

// Ping is the core event _handler_panic panics on. A test emits it on
// the runtime's core bus.
type Ping struct {
	events.Base
	N int
}

// EventName implements events.Event.
func (Ping) EventName() string { return "core.fixture_ping" }

// Trace records what fixture mods did, in order: "init:<id>",
// "run:<id>", "stop:<id>", and "handle:<id>:<N>" when a handler gets
// Ping N.
type Trace struct {
	mu    sync.Mutex
	steps []string
}

func (t *Trace) add(s string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.steps = append(t.steps, s)
	t.mu.Unlock()
}

// Steps returns a copy of the steps so far.
func (t *Trace) Steps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.steps)
}

// mod is every fixture: a manifest and a behaviour.
type mod struct {
	id  string
	m   wwapi.Manifest
	tr  *Trace
	api any // non-nil: the mod has a PublicAPI

	// seen is filled by _ok_dependent's Init: what Context.Mod gave it.
	seen any
}

// BaseAPI is _ok_base's public API value.
const BaseAPI = "ok-base-api"

func (f *mod) Manifest() wwapi.Manifest { return f.m }

func (f *mod) Init(c *wwapi.Context) error {
	f.tr.add("init:" + f.id)
	switch f.id {
	case "_init_error":
		return ErrInit
	case "_handler_panic":
		events.On(c.Events(), func(p Ping) {
			f.tr.add(fmt.Sprintf("handle:%s:%d", f.id, p.N))
			panic(fmt.Sprintf("fixture handler panic on ping %d", p.N))
		})
	case "_ok_dependent":
		api, ok := c.Mod("_ok_base")
		if !ok {
			return errors.New("_ok_base's API is missing")
		}
		f.seen = api
	}
	return nil
}

func (f *mod) Run(ctx context.Context) error {
	f.tr.add("run:" + f.id)
	switch f.id {
	case "_run_panic":
		panic("fixture Run panic")
	case "_ok_base", "_ok_dependent", "_handler_panic":
		<-ctx.Done()
		f.tr.add("stop:" + f.id)
		return ctx.Err()
	}
	return nil
}

type withAPI struct{ *mod }

func (w withAPI) API() any { return w.api }

// New returns the fixture mod id, recording into tr (which may be nil).
func New(id string, tr *Trace) wwapi.Mod {
	f := &mod{id: id, m: wwapi.ManifestFrom(ManifestJSON(id)), tr: tr}
	if id == "_ok_base" {
		f.api = BaseAPI
		return withAPI{f}
	}
	return f
}

// SeenAPI returns what _ok_dependent's Init got from Context.Mod.
func SeenAPI(m wwapi.Mod) any {
	if f, ok := m.(*mod); ok {
		return f.seen
	}
	return nil
}

// All returns every fixture.
func All(tr *Trace) []wwapi.Mod {
	out := make([]wwapi.Mod, len(IDs))
	for i, id := range IDs {
		out[i] = New(id, tr)
	}
	return out
}
