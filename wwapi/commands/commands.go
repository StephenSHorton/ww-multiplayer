// Package commands is the Wind Waker modding API's command layer: the
// single audited path every write to the game is meant to take (design
// §5.4). A command is a name, a Spec saying what it may write, and a
// Handler. Typed helpers (player.Warp and the like) are thin wrappers
// over it.
//
// Today the registry holds the core commands a wwapi.Game registers;
// most of them return ErrUnsupported until the C side that backs them
// exists (actor.kill and actor.spawn wait for the C op ring). Mods
// register mod.<id>.* commands from D-phase on, through their Context.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// Scope says who may run a command in a session.
type Scope uint8

const (
	Local     Scope = iota // this player's game only
	Authority              // the session's authority (the host) decides
)

// Spec is what a command declares when it is registered.
type Spec struct {
	Scope Scope
	// Cheat marks a command that changes progress outside play.
	Cheat bool
	// Writes is the game memory the handler may write. A command that
	// writes is refused while the game's contract check refuses writes
	// (see Registry.SetWriteGate).
	Writes []write.Range
}

// Result is a command's outcome. Done, when not nil, closes once an
// asynchronous effect (a warp the C side acknowledges, say) has landed.
type Result struct {
	OK   bool
	Code string
	Err  error
	Done <-chan struct{}
}

// Handler runs a command with its JSON arguments.
type Handler func(ctx context.Context, args json.RawMessage) (Result, error)

var (
	// ErrUnsupported is returned by a command whose backing does not
	// exist yet (actor.kill and actor.spawn until the C op ring).
	ErrUnsupported = errors.New("commands: not supported by this build")
	// ErrUnknown is returned by Execute for a name nobody registered.
	ErrUnknown = errors.New("commands: unknown command")
	// ErrExists is returned by Register for a name already taken.
	ErrExists = errors.New("commands: name already registered")
	// ErrNamespace is returned by Register for a name outside the
	// registry's namespace.
	ErrNamespace = errors.New("commands: name outside this registry's namespace")
	// ErrWritesRefused is returned by Execute for a command that writes
	// while the write gate refuses.
	ErrWritesRefused = errors.New("commands: writes refused")
)

type entry struct {
	spec Spec
	h    Handler
}

type shared struct {
	mu   sync.RWMutex
	cmds map[string]entry
	gate func() error // nil: writes allowed
}

// Registry is a view of one command table. The view from New registers
// core.* names; Mod derives a view that registers only mod.<id>.* names.
// Every view executes any registered name.
type Registry struct {
	s      *shared
	prefix string
}

// New returns the core view of a new, empty registry.
func New() *Registry {
	return &Registry{s: &shared{cmds: map[string]entry{}}, prefix: events.CoreNamespace + "."}
}

// Mod returns the view a mod's Context hands out: it registers only
// mod.<id>.* names.
func (r *Registry) Mod(id string) (*Registry, error) {
	if r.prefix != events.CoreNamespace+"." {
		return nil, fmt.Errorf("%w: only the core view derives mod views", ErrNamespace)
	}
	if !events.ValidModID(id) {
		return nil, fmt.Errorf("commands: bad mod id %q", id)
	}
	return &Registry{s: r.s, prefix: events.ModNamespace + "." + id + "."}, nil
}

// SetWriteGate sets the check Execute runs before a command whose Spec
// declares Writes: a non-nil error refuses the command. A nil gate
// allows writes. wwapi.Game sets it to its contract check.
func (r *Registry) SetWriteGate(gate func() error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.gate = gate
}

// Register adds a command. Its name must be a valid event-style name
// (dot-separated lowercase segments) inside this view's namespace.
func (r *Registry) Register(name string, s Spec, h Handler) error {
	if h == nil {
		return fmt.Errorf("commands: %s: nil handler", name)
	}
	if _, err := events.ParseName(name); err != nil {
		return fmt.Errorf("commands: %w", err)
	}
	if !strings.HasPrefix(name, r.prefix) || len(name) == len(r.prefix) {
		return fmt.Errorf("%w: %q is not %s*", ErrNamespace, name, r.prefix)
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.cmds[name]; ok {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	r.s.cmds[name] = entry{spec: s, h: h}
	return nil
}

// Lookup returns a command's Spec.
func (r *Registry) Lookup(name string) (Spec, bool) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	e, ok := r.s.cmds[name]
	return e.spec, ok
}

// Names returns the registered names, sorted.
func (r *Registry) Names() []string {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]string, 0, len(r.s.cmds))
	for n := range r.s.cmds {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Execute runs a command. args is marshalled to JSON unless it already is
// a json.RawMessage; nil means no arguments.
func (r *Registry) Execute(ctx context.Context, name string, args any) (Result, error) {
	r.s.mu.RLock()
	e, ok := r.s.cmds[name]
	gate := r.s.gate
	r.s.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknown, name)
	}
	if len(e.spec.Writes) > 0 && gate != nil {
		if err := gate(); err != nil {
			return Result{Code: "writes_refused", Err: err}, fmt.Errorf("%w: %s: %w", ErrWritesRefused, name, err)
		}
	}
	var raw json.RawMessage
	switch a := args.(type) {
	case nil:
	case json.RawMessage:
		raw = a
	default:
		b, err := json.Marshal(a)
		if err != nil {
			return Result{}, fmt.Errorf("commands: %s: args: %w", name, err)
		}
		raw = b
	}
	return e.h(ctx, raw)
}

// Unsupported is a Handler that always returns ErrUnsupported.
func Unsupported(name string) Handler {
	return func(context.Context, json.RawMessage) (Result, error) {
		err := fmt.Errorf("%w: %s", ErrUnsupported, name)
		return Result{Code: "unsupported", Err: err}, err
	}
}
