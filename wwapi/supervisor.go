package wwapi

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Options configure a Runtime. The zero value is ready to use.
type Options struct {
	// Resolve configures the resolver. When Resolve.Enabled is nil and
	// Config is set, Config.ModEnabled is used.
	Resolve ResolveOptions
	// Log gets the runtime's own lines (quarantines, failed Inits) and,
	// prefixed "[id] ", every mod's. Discarded when nil.
	Log log.Reporter
	// Config holds mod settings; nil runs every mod on its defaults.
	Config ConfigSource
	// DataRoot is the parent of each mod's DataDir; "" gives none.
	DataRoot string
	// Events configures the runtime's bus. Its OnPanic, if set, is
	// called after the runtime has quarantined the handler's mod.
	Events events.Options
	// Game is the game the mods see through Context.Game, or nil when
	// there is none (a pure server still runs its mods). With a Game,
	// mods register their commands in Game.Commands(), and the Game's
	// core events are forwarded onto the runtime's bus from Init until
	// Run ends, so a mod's subscriptions stay its own.
	Game *Game
}

var (
	// ErrRunning is returned by a second Run.
	ErrRunning = errors.New("wwapi: the runtime already ran")
)

// Runtime is the mod supervisor: it owns the event bus, initialises the
// loading mods in load order and runs them.
type Runtime struct {
	opts Options
	log  log.Reporter
	plan *Plan
	bus  *events.Bus
	cmds *commands.Registry   // core view: Game.Commands() or the runtime's own
	mods []*modState          // one per plan entry
	byID map[string]*modState // unique valid ids

	initOnce sync.Once
	mu       sync.Mutex
	ran      bool
	relayOff func() // ends the Game's event forwarding; set by Init
}

type modState struct {
	e      *Entry
	idx    int
	status Status             // guarded by Runtime.mu
	view   *events.Bus        // set by Init; guarded by Runtime.mu
	cmds   *commands.Registry // set by Init; guarded by Runtime.mu
	cancel context.CancelFunc // set by Run; guarded by Runtime.mu
	done   chan struct{}
}

// New resolves mods and returns their runtime. Nothing runs until Init
// or Run.
func New(mods []Mod, o Options) *Runtime {
	if o.Resolve.Enabled == nil && o.Config != nil {
		o.Resolve.Enabled = o.Config.ModEnabled
	}
	rt := &Runtime{opts: o, log: o.Log, byID: map[string]*modState{}}
	if rt.log == nil {
		rt.log = log.Discard{}
	}
	if o.Game != nil {
		rt.cmds = o.Game.Commands()
	} else {
		rt.cmds = commands.New()
	}
	rt.plan = Resolve(mods, o.Resolve)
	for i := range rt.plan.Entries {
		e := &rt.plan.Entries[i]
		m := &modState{e: e, idx: i, status: e.Status}
		rt.mods = append(rt.mods, m)
		if id := e.Manifest.ID; id != "" && e.Manifest.Err() == nil {
			if _, dup := rt.byID[id]; !dup {
				rt.byID[id] = m
			}
		}
	}
	eo := o.Events
	user := eo.OnPanic
	eo.OnPanic = func(p events.Panic) {
		if p.Owner == events.CoreNamespace {
			rt.log.Log(log.Err, fmt.Sprintf("panic in a core handler for %s: %v\n%s", p.Event.EventName(), p.Value, p.Stack))
		} else {
			rt.quarantine(p.Owner, fmt.Sprintf("panic in its handler for %s: %v", p.Event.EventName(), p.Value), p.Stack)
		}
		if user != nil {
			user(p)
		}
	}
	rt.bus = events.New(eo)
	return rt
}

// Plan is the resolver's result (statuses as resolved, before Init).
func (rt *Runtime) Plan() *Plan { return rt.plan }

// Bus is the core view of the runtime's event bus: the watcher emits
// core.* events on it (with Options.Game, the runtime forwards them).
func (rt *Runtime) Bus() *events.Bus { return rt.bus }

// Commands is the core view of the command registry the mods register
// in: Options.Game's, or the runtime's own when there is no Game.
func (rt *Runtime) Commands() *commands.Registry { return rt.cmds }

// Mods returns every mod with its current status, loading mods first in
// load order.
func (rt *Runtime) Mods() []ModInfo {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return infos(rt.plan.Entries, rt.plan.Order, func(i int) Status { return rt.mods[i].status })
}

// Status returns one mod's current status.
func (rt *Runtime) Status(id string) (Status, bool) { return rt.status(id) }

func (rt *Runtime) status(id string) (Status, bool) {
	m := rt.byID[id]
	if m == nil {
		return Status{}, false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return m.status, true
}

func (rt *Runtime) setFailed(m *modState, reason string) {
	rt.mu.Lock()
	m.status = Status{State: Failed, Reason: reason}
	rt.mu.Unlock()
	rt.log.Log(log.Err, fmt.Sprintf("mod %s failed: %s", m.e.Manifest.ID, reason))
}

// Init calls each loading mod's Init, in load order, once. A mod whose
// Init returns an error or panics is FAILED and its subscriptions end;
// a later mod that requires it fails too. Init is called by Run when it
// has not run yet. Every call, Run's included, returns only once the
// first call has finished, so Run never starts a mod whose Init is still
// running on another goroutine.
func (rt *Runtime) Init() { rt.initOnce.Do(rt.initMods) }

func (rt *Runtime) initMods() {
	rt.relayGame()
	for _, i := range rt.plan.Order {
		m := rt.mods[i]
		id := m.e.Manifest.ID
		if dep := rt.unmetDep(m); dep != "" {
			rt.setFailed(m, fmt.Sprintf("requires %s, which did not load", dep))
			continue
		}
		view, err := rt.bus.Mod(id)
		if err != nil {
			rt.setFailed(m, "events: "+err.Error())
			continue
		}
		cmds, err := rt.cmds.Mod(id)
		if err != nil {
			view.Close()
			rt.setFailed(m, "commands: "+err.Error())
			continue
		}
		rt.mu.Lock()
		m.view, m.cmds = view, cmds
		rt.mu.Unlock()
		c := newContext(rt, m, view, cmds)
		if err := callInit(m.e.Mod, c); err != nil {
			view.Close()
			cmds.Close()
			rt.setFailed(m, "Init: "+err.Error())
		}
	}
}

// relayGame forwards Options.Game's core events onto the runtime's bus,
// where the mods' views are: a Game is made before the runtime, so its
// watcher cannot emit on the runtime's bus itself. The forwarded events
// keep their type and payload; the runtime's bus stamps its own Seq, At,
// Source (SourceGame) and Origin (core), so a core.* event emitted
// through the Game bus's Net() view arrives as SourceGame.
func (rt *Runtime) relayGame() {
	g := rt.opts.Game
	if g == nil || g.Events() == rt.bus {
		return
	}
	prefix := events.CoreNamespace + "."
	off := events.On(g.Events(), func(e events.Event) {
		if strings.HasPrefix(e.EventName(), prefix) {
			_ = rt.bus.Emit(e) // only ErrClosed, once Run has ended
		}
	})
	rt.mu.Lock()
	rt.relayOff = off
	rt.mu.Unlock()
}

// unmetDep returns the first required dependency of m that is no longer
// LOADED (its Init failed, or it was quarantined during Init).
func (rt *Runtime) unmetDep(m *modState) string {
	for _, id := range sortedKeys(m.e.Manifest.Dependencies) {
		if s, ok := rt.status(id); !ok || s.State != Loaded {
			return id
		}
	}
	return ""
}

func callInit(mod Mod, c *Context) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
			c.rt.log.Log(log.Info, fmt.Sprintf("mod %s: Init panic stack:\n%s", c.ID(), debug.Stack()))
		}
	}()
	return mod.Init(c)
}

// Run initialises the mods if Init has not run, then runs every LOADED
// mod's Run on its own goroutine until ctx ends or the net provider
// returns ErrFatal. It is a copy of runPipelines' semantics (main.go)
// with four changes for mods:
//
//   - a mod whose Run returns nil (or context.Canceled) has finished; the
//     others go on (a pipeline's return ends the session in runPipelines);
//   - an error returned after the runtime stopped the mod (ctx ended, or
//     another mod's ErrFatal) is the mod shutting down, as a closed
//     socket's error is: it is logged at Info and the mod stays LOADED;
//   - a panic is recovered, and a panic or any other error quarantines
//     only that mod: its Run context is cancelled, its event
//     subscriptions end, ModQuarantined is emitted, and the error is
//     logged;
//   - an error wrapping ErrFatal from the net provider ends everything
//     and is returned as is, not logged (the provider reported it), like
//     network.ErrFatal in runPipelines.
//
// Once ctx ends (or on ErrFatal) the mods are stopped in reverse load
// order: each one's context is cancelled and its Run awaited before the
// next. Then the Game's events stop being forwarded, every mod's
// subscriptions end and its commands are removed, and the bus is closed. Run
// returns the fatal error or nil. A Runtime runs once.
func (rt *Runtime) Run(ctx context.Context) error {
	rt.mu.Lock()
	if rt.ran {
		rt.mu.Unlock()
		return ErrRunning
	}
	rt.ran = true
	rt.mu.Unlock()
	rt.Init()

	runCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	var (
		fatalMu sync.Mutex
		fatal   error
	)
	var started []*modState
	rt.mu.Lock()
	for _, i := range rt.plan.Order {
		m := rt.mods[i]
		if m.status.State != Loaded {
			continue
		}
		// Not derived from runCtx: the end of ctx must not reach every mod
		// at once, so that the stop below can go in reverse load order.
		mctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.cancel, m.done = cancel, make(chan struct{})
		started = append(started, m)
		go func() {
			defer close(m.done)
			err := callRun(m.e.Mod, mctx)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, ErrFatal) && m.e.Manifest.Provide(ProvideNet) {
				fatalMu.Lock()
				if fatal == nil {
					fatal = err
				}
				fatalMu.Unlock()
				cancelAll()
				return
			}
			var pe *runPanic
			if errors.As(err, &pe) {
				rt.quarantine(m.e.Manifest.ID, "panic: "+fmt.Sprint(pe.v), pe.stack)
				return
			}
			// mctx ends only on the stop below or on quarantine, which has
			// already set the status: this is the mod shutting down.
			if mctx.Err() != nil {
				rt.log.Log(log.Info, fmt.Sprintf("mod %s stopped: %v", m.e.Manifest.ID, err))
				return
			}
			rt.quarantine(m.e.Manifest.ID, err.Error(), nil)
		}()
	}
	rt.mu.Unlock()

	<-runCtx.Done()
	for k := len(started) - 1; k >= 0; k-- {
		m := started[k]
		m.cancel()
		<-m.done
	}
	rt.mu.Lock()
	if rt.relayOff != nil {
		rt.relayOff()
	}
	rt.mu.Unlock()
	for k := len(rt.plan.Order) - 1; k >= 0; k-- {
		rt.mu.Lock()
		m := rt.mods[rt.plan.Order[k]]
		v, cmds := m.view, m.cmds
		rt.mu.Unlock()
		if v != nil {
			v.Close()
		}
		if cmds != nil {
			cmds.Close()
		}
	}
	rt.bus.Close()
	fatalMu.Lock()
	defer fatalMu.Unlock()
	return fatal
}

type runPanic struct {
	v     any
	stack []byte
}

func (p *runPanic) Error() string { return fmt.Sprintf("panic: %v", p.v) }

func callRun(mod Mod, ctx context.Context) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &runPanic{v: v, stack: debug.Stack()}
		}
	}()
	return mod.Run(ctx)
}

// quarantine moves a LOADED mod to QUARANTINED: it cancels its Run,
// ends its subscriptions, removes its commands, logs why and emits
// ModQuarantined. A mod in any other state is left as it is.
func (rt *Runtime) quarantine(id, reason string, stack []byte) {
	m := rt.byID[id]
	if m == nil {
		return
	}
	rt.mu.Lock()
	if m.status.State != Loaded {
		rt.mu.Unlock()
		return
	}
	m.status = Status{State: Quarantined, Reason: reason}
	cancel, view, cmds := m.cancel, m.view, m.cmds
	rt.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if view != nil {
		view.Close()
	}
	if cmds != nil {
		cmds.Close()
	}
	rt.log.Log(log.Err, fmt.Sprintf("mod %s quarantined: %s", id, reason))
	if len(stack) > 0 {
		rt.log.Log(log.Info, fmt.Sprintf("mod %s: stack:\n%s", id, stack))
	}
	_ = rt.bus.Emit(ModQuarantined{ID: id, Reason: reason})
}
