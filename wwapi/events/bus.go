package events

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Source says where an event came from.
type Source uint8

const (
	SourceGame Source = iota + 1 // the core watcher, from game memory
	SourceNet                    // relayed from another player
	SourceMod                    // a mod's own event
)

func (s Source) String() string {
	switch s {
	case SourceGame:
		return "game"
	case SourceNet:
		return "net"
	case SourceMod:
		return "mod"
	}
	return fmt.Sprintf("Source(%d)", uint8(s))
}

// Meta is what the bus stamps on every emitted event.
type Meta struct {
	Seq    uint64    // bus-wide, starts at 1, strictly increasing in emit order
	At     time.Time // when Emit ran
	Source Source
	Origin string // "core" or the emitting mod's id
}

// Event is implemented by struct types that embed Base and define
// EventName with a value receiver.
type Event interface {
	EventName() string
	EventMeta() Meta
}

// Base carries an event's Meta. Embed it by value; the bus fills it in.
type Base struct{ meta Meta }

// EventMeta returns the Meta the bus stamped (zero before Emit).
func (b Base) EventMeta() Meta { return b.meta }

func (b *Base) setMeta(m Meta) { b.meta = m }

type stamper interface{ setMeta(Meta) }

var stamperType = reflect.TypeFor[stamper]()

// DefaultBuffer is the buffer On and Wait use per subscription.
const DefaultBuffer = 256

var (
	ErrClosed    = errors.New("events: bus closed")
	ErrBadName   = errors.New("events: bad event name")
	ErrNamespace = errors.New("events: name outside this publisher's namespace")
	ErrBadEvent  = errors.New("events: not a stampable event")
	ErrNameTaken = errors.New("events: name already used by another type")
	ErrNotCore   = errors.New("events: only the core bus can do this")
	ErrBadModID  = errors.New("events: bad mod id")
)

// Options configure New. The zero value is ready to use.
type Options struct {
	// Now stamps Meta.At; time.Now when nil.
	Now func() time.Time
	// OnPanic is told about a panic in an On callback. The panic is
	// recovered and the subscription stays. It may be called from several
	// goroutines at once. When it is nil the panic and its stack are
	// logged with the standard log package instead. Either way Panics
	// counts it. The mod supervisor is expected to set OnPanic and close
	// the owning mod's view (quarantine).
	OnPanic func(p Panic)
}

// Panic describes a recovered panic in an On callback.
type Panic struct {
	Owner string // "core" or the id of the mod whose view subscribed
	Event Event
	Value any
	Stack []byte // the panicking goroutine's stack, from runtime/debug
}

// Bus is a view of one shared event hub. The view from New is the core
// view; Mod and Net derive restricted views of the same hub.
type Bus struct {
	h   *hub
	sc  *scope
	src Source
}

type scope struct {
	origin string
	prefix string // the names this scope may emit start with it
	core   bool
	closed bool              // guarded by hub.mu
	subs   map[*sub]struct{} // guarded by hub.mu
}

type hub struct {
	opts    Options
	mu      sync.Mutex
	closed  bool
	seq     uint64
	topics  map[reflect.Type]*topic   // concrete and interface types
	ifaces  []reflect.Type            // the interface topics
	match   map[reflect.Type][]*topic // concrete type -> topics it goes to
	names   map[string]reflect.Type   // event name -> the type that owns it
	views   map[string]*scope         // mod id -> its live scope
	dropped atomic.Uint64
	panics  atomic.Uint64
}

type topic struct {
	subs    []*sub // in subscribe order
	last    Event
	hasLast bool
}

type sub struct {
	t    *topic
	sc   *scope
	push func(Event) bool // typed non-blocking send; false when it dropped
	end  func()           // closes the typed channel
	done bool             // guarded by hub.mu
	dead atomic.Bool      // set with done; On skips queued events once set
}

// New returns the core view of a new bus. It emits core.* names with
// Source SourceGame and Origin "core".
func New(opts Options) *Bus {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	h := &hub{
		opts:   opts,
		topics: map[reflect.Type]*topic{},
		match:  map[reflect.Type][]*topic{},
		names:  map[string]reflect.Type{},
		views:  map[string]*scope{},
	}
	sc := &scope{origin: CoreNamespace, prefix: CoreNamespace + ".", core: true, subs: map[*sub]struct{}{}}
	return &Bus{h: h, sc: sc, src: SourceGame}
}

// Mod returns the view a mod's Context hands out: it emits only
// mod.<id>.* names, with Source SourceMod and Origin id, and owns the
// subscriptions made through it. Calling Mod again with the same id
// returns a view of the same scope until that scope is closed; after
// that a fresh scope is made. Only the core view can call it.
func (b *Bus) Mod(id string) (*Bus, error) {
	if !b.sc.core {
		return nil, ErrNotCore
	}
	if !ValidModID(id) {
		return nil, fmt.Errorf("%w: %q", ErrBadModID, id)
	}
	b.h.mu.Lock()
	defer b.h.mu.Unlock()
	if b.h.closed {
		return nil, ErrClosed
	}
	sc := b.h.views[id]
	if sc == nil || sc.closed {
		sc = &scope{origin: id, prefix: ModNamespace + "." + id + ".", subs: map[*sub]struct{}{}}
		b.h.views[id] = sc
	}
	return &Bus{h: b.h, sc: sc, src: SourceMod}, nil
}

// Net returns a view of the same scope that stamps Source SourceNet, for
// events relayed from another player. It shares b's namespace, its
// subscriptions and its Close.
func (b *Bus) Net() *Bus { return &Bus{h: b.h, sc: b.sc, src: SourceNet} }

// Origin is "core" or the view's mod id.
func (b *Bus) Origin() string { return b.sc.origin }

// Dropped counts events dropped from full subscription buffers, bus-wide.
func (b *Bus) Dropped() uint64 { return b.h.dropped.Load() }

// Panics counts recovered panics in On callbacks, bus-wide.
func (b *Bus) Panics() uint64 { return b.h.panics.Load() }

// Close on the core view closes the whole bus: every subscription ends,
// and Emit and Mod return ErrClosed. On a mod view it ends only the
// subscriptions made through that view (On, Chan and Wait), and the view
// can no longer emit or subscribe. Closing ends On subscriptions the way
// off does: events still queued are discarded, and only a call that is
// already running may finish after Close returns. Close is safe to call
// more than once.
func (b *Bus) Close() {
	h := b.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if b.sc.core {
		if h.closed {
			return
		}
		h.closed = true
		for _, t := range h.topics {
			for _, s := range t.subs {
				s.close()
			}
			t.subs = nil
		}
		for _, sc := range append(mapValues(h.views), b.sc) {
			sc.closed = true
			clear(sc.subs)
		}
		return
	}
	if b.sc.closed {
		return
	}
	b.sc.closed = true
	for s := range b.sc.subs {
		h.remove(s)
	}
}

func mapValues(m map[string]*scope) []*scope {
	out := make([]*scope, 0, len(m)+1)
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (b *Bus) closedLocked() bool { return b.h.closed || b.sc.closed }

// Emit stamps e and delivers it to every matching subscription. e must be
// a non-pointer struct that embeds Base, and its name must be inside this
// view's namespace (core.* for the core view, mod.<id>.* for a mod view).
// The first type to emit a name owns it; another type emitting the same
// name gets ErrNameTaken. Emit never blocks on a subscriber.
func (b *Bus) Emit(e Event) error {
	if e == nil {
		return fmt.Errorf("%w: nil", ErrBadEvent)
	}
	rt := reflect.TypeOf(e)
	if rt.Kind() != reflect.Struct || !reflect.PointerTo(rt).Implements(stamperType) {
		return fmt.Errorf("%w: %v must be a struct value that embeds events.Base", ErrBadEvent, rt)
	}
	name := e.EventName()
	if _, err := ParseName(name); err != nil {
		return err
	}
	if len(name) <= len(b.sc.prefix) || name[:len(b.sc.prefix)] != b.sc.prefix {
		return fmt.Errorf("%w: %s may emit only %s*, not %q", ErrNamespace, b.sc.origin, b.sc.prefix, name)
	}

	h := b.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if b.closedLocked() {
		return ErrClosed
	}
	if owner, ok := h.names[name]; ok && owner != rt {
		return fmt.Errorf("%w: %q belongs to %v, not %v", ErrNameTaken, name, owner, rt)
	}
	h.names[name] = rt

	h.seq++
	p := reflect.New(rt)
	p.Elem().Set(reflect.ValueOf(e))
	p.Interface().(stamper).setMeta(Meta{Seq: h.seq, At: h.opts.Now(), Source: b.src, Origin: b.sc.origin})
	ev := p.Elem().Interface().(Event)

	for _, t := range h.matching(rt) {
		t.last, t.hasLast = ev, true
		for _, s := range t.subs {
			if !s.push(ev) {
				h.dropped.Add(1)
			}
		}
	}
	return nil
}

// matching returns the topics an event of concrete type rt goes to: its
// own topic and every interface topic it implements. Caller holds mu.
func (h *hub) matching(rt reflect.Type) []*topic {
	if ts, ok := h.match[rt]; ok {
		return ts
	}
	ts := []*topic{h.topicLocked(rt)}
	for _, it := range h.ifaces {
		if rt.Implements(it) {
			ts = append(ts, h.topics[it])
		}
	}
	h.match[rt] = ts
	return ts
}

// topicLocked returns rt's topic, making it on first use. A new interface
// topic is added to the cached matches of the types that implement it.
// Caller holds mu.
func (h *hub) topicLocked(rt reflect.Type) *topic {
	if t, ok := h.topics[rt]; ok {
		return t
	}
	t := &topic{}
	h.topics[rt] = t
	if rt.Kind() == reflect.Interface {
		h.ifaces = append(h.ifaces, rt)
		for ct, ts := range h.match {
			if ct.Implements(rt) {
				h.match[ct] = append(ts, t)
			}
		}
	}
	return t
}

// remove ends s and takes it off its topic and scope. Caller holds mu.
func (h *hub) remove(s *sub) {
	if s.done {
		return
	}
	s.close()
	for i, x := range s.t.subs {
		if x == s {
			s.t.subs = append(s.t.subs[:i:i], s.t.subs[i+1:]...)
			break
		}
	}
	delete(s.sc.subs, s)
}

func (s *sub) close() {
	if !s.done {
		s.done = true
		s.dead.Store(true)
		s.end()
	}
}

// subscribe adds a typed drop-oldest subscription for T.
// The returned *sub is nil when the view was already closed.
func subscribe[T Event](b *Bus, buf int) (<-chan T, func(), *sub) {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan T, buf)
	h := b.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if b.closedLocked() {
		close(ch)
		return ch, func() {}, nil
	}
	s := &sub{t: h.topicLocked(reflect.TypeFor[T]()), sc: b.sc}
	s.push = func(e Event) bool { return sendDropOldest(ch, e.(T)) }
	s.end = func() { close(ch) }
	s.t.subs = append(s.t.subs, s)
	b.sc.subs[s] = struct{}{}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			h.remove(s)
			h.mu.Unlock()
		})
	}, s
}

// sendDropOldest queues v, dropping the oldest queued values while ch is
// full. The caller holds hub.mu, so it is ch's only sender and the loop
// ends: each pass queues v or frees a slot (or finds the reader just freed
// one). It reports false when it dropped something.
func sendDropOldest[T any](ch chan T, v T) bool {
	kept := true
	for {
		select {
		case ch <- v:
			return kept
		default:
		}
		select {
		case <-ch:
			kept = false
		default:
		}
	}
}

// Chan returns a channel of every T emitted from now on, buffered to buf
// (at least 1), and a func that ends the subscription and closes the
// channel (safe to call more than once). When the buffer is full the
// oldest queued event is dropped, so the publisher never waits. The
// channel is also closed when the bus, or the view it was made through,
// is closed.
func Chan[T Event](b *Bus, buf int) (<-chan T, func()) {
	ch, stop, _ := subscribe[T](b, buf)
	return ch, stop
}

// On calls fn on a goroutine of its own for every T emitted from now on,
// one call at a time, in emit order. Its buffer is DefaultBuffer,
// drop-oldest. off ends the subscription and discards what is still
// queued; a call that is already running, or just starting, may still
// finish after off returns. off may be called from inside fn and more
// than once. Closing the bus or the view behaves like off. A panic in fn
// is recovered, counted in Panics and passed to Options.OnPanic (logged
// when that is nil).
func On[T Event](b *Bus, fn func(T)) (off func()) {
	ch, stop, s := subscribe[T](b, DefaultBuffer)
	var stopped atomic.Bool
	go func() {
		for e := range ch {
			if stopped.Load() || s.dead.Load() {
				continue // drain what was queued before off or Close
			}
			b.h.call(b.sc.origin, e, func() { fn(e) })
		}
	}()
	return func() {
		stopped.Store(true)
		stop()
	}
}

func (h *hub) call(owner string, e Event, f func()) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		h.panics.Add(1)
		p := Panic{Owner: owner, Event: e, Value: v, Stack: debug.Stack()}
		if h.opts.OnPanic != nil {
			h.opts.OnPanic(p)
			return
		}
		log.Printf("events: panic in %s handler for %s: %v\n%s", p.Owner, e.EventName(), p.Value, p.Stack)
	}()
	f()
}

// Last returns the latest T emitted on the bus, if any. For an interface
// T it is the latest emitted event that implements T. Last works on a
// closed bus and reports what was emitted before Close.
func Last[T Event](b *Bus) (T, bool) {
	var zero T
	h := b.h
	h.mu.Lock()
	defer h.mu.Unlock()
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Interface {
		t, ok := h.topics[rt]
		if !ok || !t.hasLast {
			return zero, false
		}
		return t.last.(T), true
	}
	// An interface topic may be newer than the events it covers, so look
	// at every concrete topic and take the latest match.
	var best Event
	for ct, t := range h.topics {
		if ct.Kind() == reflect.Interface || !t.hasLast || !ct.Implements(rt) {
			continue
		}
		if best == nil || t.last.EventMeta().Seq > best.EventMeta().Seq {
			best = t.last
		}
	}
	if best == nil {
		return zero, false
	}
	return best.(T), true
}

// Wait blocks until a T emitted after the call satisfies pred (nil
// matches any) and returns it. It returns ctx.Err() when ctx ends first,
// and ErrClosed when the bus or the view closes. pred runs on the calling
// goroutine. Wait uses a DefaultBuffer drop-oldest subscription, so under
// a flood it can miss an event.
func Wait[T Event](ctx context.Context, b *Bus, pred func(T) bool) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	ch, stop, _ := subscribe[T](b, DefaultBuffer)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case e, ok := <-ch:
			if !ok {
				return zero, ErrClosed
			}
			if pred == nil || pred(e) {
				return e, nil
			}
		}
	}
}
