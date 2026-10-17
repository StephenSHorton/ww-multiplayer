package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stageChanged struct {
	Base
	To string
}

func (stageChanged) EventName() string { return "core.stage_changed" }

type tick struct {
	Base
	N int
}

func (tick) EventName() string { return "core.tick" }

type peerJoined struct {
	Base
	Name string
}

func (peerJoined) EventName() string { return "mod.multiplayer.peer_joined" }

type question struct {
	Base
	Text string
}

func (question) EventName() string { return "mod.coop.question" }

// namedEvent lets a test pick any name at run time.
type namedEvent struct {
	Base
	name string
}

func (e namedEvent) EventName() string { return e.name }

// twin has the same name as tick but another type.
type twin struct{ Base }

func (twin) EventName() string { return "core.tick" }

// noBase implements Event without embedding Base.
type noBase struct{}

func (noBase) EventName() string { return "core.no_base" }
func (noBase) EventMeta() Meta   { return Meta{} }

type stage interface {
	Event
	stageName() string
}

func (e stageChanged) stageName() string { return e.To }

func fixedClock() func() time.Time {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var n atomic.Int64
	return func() time.Time { return t0.Add(time.Duration(n.Add(1)) * time.Second) }
}

func mustMod(t *testing.T, b *Bus, id string) *Bus {
	t.Helper()
	m, err := b.Mod(id)
	if err != nil {
		t.Fatalf("Mod(%q): %v", id, err)
	}
	return m
}

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	panic("unreachable")
}

func TestEmitStampsMeta(t *testing.T) {
	b := New(Options{Now: fixedClock()})
	ch, off := Chan[stageChanged](b, 8)
	defer off()

	in := stageChanged{To: "sea"}
	for i := 0; i < 3; i++ {
		if err := b.Emit(in); err != nil {
			t.Fatal(err)
		}
	}
	if in.EventMeta() != (Meta{}) {
		t.Fatalf("Emit changed the caller's value: %+v", in.EventMeta())
	}
	for i := 1; i <= 3; i++ {
		e := recv(t, ch)
		m := e.EventMeta()
		if m.Seq != uint64(i) || m.Source != SourceGame || m.Origin != "core" || e.To != "sea" {
			t.Fatalf("event %d: %+v %+v", i, e, m)
		}
		want := time.Date(2026, 1, 2, 3, 4, 5+i, 0, time.UTC)
		if !m.At.Equal(want) {
			t.Fatalf("At = %v, want %v", m.At, want)
		}
	}
}

func TestSeqIsBusWide(t *testing.T) {
	b := New(Options{})
	mp := mustMod(t, b, "multiplayer")
	all, off := Chan[Event](b, 16)
	defer off()
	must(t, b.Emit(tick{}))
	must(t, mp.Emit(peerJoined{Name: "a"}))
	must(t, b.Emit(stageChanged{}))
	for i := 1; i <= 3; i++ {
		if s := recv(t, all).EventMeta().Seq; s != uint64(i) {
			t.Fatalf("seq %d, want %d", s, i)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSourcesAndOrigins(t *testing.T) {
	b := New(Options{})
	mp := mustMod(t, b, "multiplayer")
	all, off := Chan[Event](b, 16)
	defer off()
	must(t, b.Emit(tick{}))
	must(t, mp.Emit(peerJoined{}))
	must(t, mp.Net().Emit(peerJoined{}))
	must(t, b.Net().Emit(tick{}))
	want := []Meta{
		{Source: SourceGame, Origin: "core"},
		{Source: SourceMod, Origin: "multiplayer"},
		{Source: SourceNet, Origin: "multiplayer"},
		{Source: SourceNet, Origin: "core"},
	}
	for i, w := range want {
		m := recv(t, all).EventMeta()
		if m.Source != w.Source || m.Origin != w.Origin {
			t.Fatalf("event %d: %v/%q, want %v/%q", i, m.Source, m.Origin, w.Source, w.Origin)
		}
	}
	if mp.Origin() != "multiplayer" || b.Origin() != "core" {
		t.Fatal("Origin")
	}
}

func TestSourceString(t *testing.T) {
	for s, w := range map[Source]string{SourceGame: "game", SourceNet: "net", SourceMod: "mod", 0: "Source(0)", 9: "Source(9)"} {
		if s.String() != w {
			t.Errorf("%d: %q, want %q", s, s.String(), w)
		}
	}
}

func TestNamespaceRules(t *testing.T) {
	b := New(Options{})
	coop := mustMod(t, b, "coop")
	a := mustMod(t, b, "a")
	cases := []struct {
		bus  *Bus
		name string
		err  error
	}{
		{b, "core.tick", nil},
		{b, "core.stage.changed", nil},
		{b, "mod.coop.question", ErrNamespace},
		{coop, "mod.coop.question", nil},
		{coop, "mod.coop.sub.thing", nil},
		{coop, "core.tick", ErrNamespace},
		{coop, "mod.multiplayer.peer_joined", ErrNamespace},
		{a, "mod.ab.x", ErrNamespace}, // the prefix ends at the dot
		{coop.Net(), "core.tick", ErrNamespace},
		{b, "core", ErrBadName},
		{b, "", ErrBadName},
		{b, "game.tick", ErrBadName},
		{b, "core.Tick", ErrBadName},
		{b, "core..tick", ErrBadName},
		{b, "core.tick.", ErrBadName},
		{coop, "mod.coop", ErrBadName},
		{coop, "mod.core.x", ErrBadName},
	}
	for _, c := range cases {
		err := c.bus.Emit(namedEvent{name: c.name})
		if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("%s emits %q: err %v, want %v", c.bus.Origin(), c.name, err, c.err)
		}
	}
}

func TestModViewRules(t *testing.T) {
	b := New(Options{})
	for _, id := range []string{"", "core", "Coop", "1coop", "co-op", "_", "__x"} {
		if _, err := b.Mod(id); !errors.Is(err, ErrBadModID) {
			t.Errorf("Mod(%q): %v, want ErrBadModID", id, err)
		}
	}
	coop := mustMod(t, b, "coop")
	if _, err := coop.Mod("other"); !errors.Is(err, ErrNotCore) {
		t.Fatalf("Mod from a mod view: %v", err)
	}
	if _, err := coop.Net().Mod("other"); !errors.Is(err, ErrNotCore) {
		t.Fatalf("Mod from a mod's Net view: %v", err)
	}
	fx := mustMod(t, b, "_fixture")
	must(t, fx.Emit(namedEvent{name: "mod._fixture.ping"}))
}

func TestBadEvents(t *testing.T) {
	b := New(Options{})
	if err := b.Emit(nil); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("nil: %v", err)
	}
	if err := b.Emit(&tick{}); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("pointer: %v", err)
	}
	if err := b.Emit(noBase{}); !errors.Is(err, ErrBadEvent) {
		t.Fatalf("no Base: %v", err)
	}
	if b.Dropped() != 0 {
		t.Fatal("dropped")
	}
	if _, ok := Last[Event](b); ok {
		t.Fatal("a rejected event became Last")
	}
}

func TestNameTaken(t *testing.T) {
	b := New(Options{})
	must(t, b.Emit(tick{}))
	if err := b.Emit(twin{}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("twin: %v", err)
	}
	must(t, b.Emit(tick{}))
	if m, _ := Last[tick](b); m.EventMeta().Seq != 2 {
		t.Fatalf("a refused emit took a seq: %d", m.EventMeta().Seq)
	}
}

func TestChanDropOldest(t *testing.T) {
	b := New(Options{})
	ch, off := Chan[tick](b, 3)
	defer off()
	for i := 1; i <= 10; i++ {
		must(t, b.Emit(tick{N: i}))
	}
	if len(ch) != 3 {
		t.Fatalf("len %d", len(ch))
	}
	for _, want := range []int{8, 9, 10} {
		if e := recv(t, ch); e.N != want || e.EventMeta().Seq != uint64(want) {
			t.Fatalf("got %d (seq %d), want %d", e.N, e.EventMeta().Seq, want)
		}
	}
	if b.Dropped() != 7 {
		t.Fatalf("Dropped %d, want 7", b.Dropped())
	}
}

func TestChanBufferFloor(t *testing.T) {
	b := New(Options{})
	for _, n := range []int{0, -5} {
		ch, off := Chan[tick](b, n)
		if cap(ch) != 1 {
			t.Fatalf("buf %d: cap %d", n, cap(ch))
		}
		off()
	}
	ch, off := Chan[tick](b, 0)
	defer off()
	must(t, b.Emit(tick{N: 1}))
	must(t, b.Emit(tick{N: 2}))
	if e := recv(t, ch); e.N != 2 {
		t.Fatalf("got %d", e.N)
	}
}

// A full subscriber nobody reads never holds up Emit or other subscribers.
func TestEmitNeverBlocks(t *testing.T) {
	b := New(Options{})
	_, off := Chan[tick](b, 1) // never read
	defer off()
	fast, off2 := Chan[tick](b, 100000)
	defer off2()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50000; i++ {
			_ = b.Emit(tick{N: i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Emit blocked")
	}
	if len(fast) != 50000 {
		t.Fatalf("fast subscriber has %d", len(fast))
	}
}

func TestFanOutAndUnsubscribe(t *testing.T) {
	b := New(Options{})
	c1, off1 := Chan[tick](b, 4)
	c2, off2 := Chan[tick](b, 4)
	must(t, b.Emit(tick{N: 1}))
	off1()
	off1() // twice is fine
	if _, ok := <-drain(c1); ok {
		t.Fatal("c1 still open after off")
	}
	must(t, b.Emit(tick{N: 2}))
	if recv(t, c2).N != 1 || recv(t, c2).N != 2 {
		t.Fatal("c2 order")
	}
	off2()
	if _, ok := <-c2; ok {
		t.Fatal("c2 open")
	}
}

// drain reads ch to its close and returns a closed channel signalling that.
func drain[T any](ch <-chan T) <-chan T {
	for range ch {
	}
	return ch
}

func TestInterfaceSubscriptions(t *testing.T) {
	b := New(Options{})
	coop := mustMod(t, b, "coop")
	must(t, b.Emit(stageChanged{To: "early"})) // before the interface topic exists
	all, off := Chan[Event](b, 16)
	defer off()
	st, off2 := Chan[stage](b, 16)
	defer off2()
	must(t, b.Emit(tick{N: 1}))
	must(t, b.Emit(stageChanged{To: "sea"}))
	must(t, coop.Emit(question{Text: "?"}))

	if _, ok := recv(t, all).(tick); !ok {
		t.Fatal("want tick first")
	}
	if e := recv(t, all).(stageChanged); e.To != "sea" {
		t.Fatal("want stageChanged")
	}
	if e := recv(t, all).(question); e.Text != "?" {
		t.Fatal("want question")
	}
	if e := recv(t, st); e.stageName() != "sea" {
		t.Fatalf("stage sub got %v", e)
	}
	if len(st) != 0 {
		t.Fatal("stage sub got a non-stage event")
	}

	// Last over an interface sees events from before its topic existed.
	if e, ok := Last[stage](b); !ok || e.stageName() != "sea" {
		t.Fatalf("Last[stage] = %v %v", e, ok)
	}
	if e, ok := Last[Event](b); !ok || e.EventName() != "mod.coop.question" {
		t.Fatalf("Last[Event] = %v %v", e, ok)
	}
	b2 := New(Options{})
	must(t, b2.Emit(stageChanged{To: "x"}))
	if e, ok := Last[stage](b2); !ok || e.stageName() != "x" {
		t.Fatalf("Last[stage] with no interface topic = %v %v", e, ok)
	}
}

func TestLast(t *testing.T) {
	b := New(Options{})
	if _, ok := Last[tick](b); ok {
		t.Fatal("Last before any emit")
	}
	if _, ok := Last[Event](b); ok {
		t.Fatal("Last[Event] before any emit")
	}
	for i := 1; i <= 5; i++ {
		must(t, b.Emit(tick{N: i}))
	}
	must(t, b.Emit(stageChanged{To: "a"}))
	e, ok := Last[tick](b)
	if !ok || e.N != 5 || e.EventMeta().Seq != 5 {
		t.Fatalf("Last = %+v %v", e, ok)
	}
	b.Close()
	if e, ok := Last[stageChanged](b); !ok || e.To != "a" {
		t.Fatal("Last after Close")
	}
}

func TestOnOrderAndOff(t *testing.T) {
	b := New(Options{})
	var mu sync.Mutex
	var got []int
	done := make(chan struct{})
	off := On(b, func(e tick) {
		mu.Lock()
		got = append(got, e.N)
		n := len(got)
		mu.Unlock()
		if n == 100 {
			close(done)
		}
	})
	for i := 1; i <= 100; i++ {
		must(t, b.Emit(tick{N: i}))
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("callbacks did not run")
	}
	off()
	off()
	must(t, b.Emit(tick{N: 101}))
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 100 {
		t.Fatalf("%d calls", len(got))
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("call %d got %d", i, n)
		}
	}
}

func TestOnOffFromInsideCallback(t *testing.T) {
	b := New(Options{})
	var calls atomic.Int32
	var off func()
	ready := make(chan struct{})
	first := make(chan struct{})
	off = On(b, func(e tick) {
		<-ready
		if calls.Add(1) == 1 {
			off()
			close(first)
		}
	})
	for i := 0; i < 10; i++ {
		must(t, b.Emit(tick{N: i}))
	}
	close(ready)
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("off from inside fn deadlocked")
	}
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d calls after off", n)
	}
}

func TestOnPanicRecovered(t *testing.T) {
	panics := make(chan Panic, 4)
	b := New(Options{OnPanic: func(p Panic) { panics <- p }})
	coop := mustMod(t, b, "coop")
	got := make(chan int, 4)
	off := On(coop, func(e tick) {
		if e.N == 1 {
			panic("boom")
		}
		got <- e.N
	})
	defer off()
	must(t, b.Emit(tick{N: 1}))
	must(t, b.Emit(tick{N: 2}))
	p := recv(t, panics)
	if p.Owner != "coop" || p.Value != "boom" || p.Event.(tick).N != 1 {
		t.Fatalf("panic %+v", p)
	}
	if n := recv(t, got); n != 2 {
		t.Fatalf("after the panic got %d", n)
	}

	// Without OnPanic the panic is still contained.
	b2 := New(Options{})
	ok := make(chan struct{})
	off2 := On(b2, func(e tick) {
		if e.N == 1 {
			panic("quiet")
		}
		close(ok)
	})
	defer off2()
	must(t, b2.Emit(tick{N: 1}))
	must(t, b2.Emit(tick{N: 2}))
	recvClosed(t, ok)
	if b2.Panics() != 1 || b.Panics() != 1 {
		t.Fatalf("Panics = %d, %d; want 1, 1", b.Panics(), b2.Panics())
	}
	if !strings.Contains(string(p.Stack), "TestOnPanicRecovered") {
		t.Fatalf("Panic.Stack does not show the handler:\n%s", p.Stack)
	}
}

// Closing a view, or the whole bus, must stop an On handler the way off
// does: what is still queued is discarded, and only the call already
// running may finish (the quarantine path).
func TestCloseStopsQueuedOnCalls(t *testing.T) {
	for _, closeCore := range []bool{false, true} {
		name := "mod view"
		if closeCore {
			name = "core bus"
		}
		t.Run(name, func(t *testing.T) {
			b := New(Options{})
			coop := mustMod(t, b, "coop")
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			var calls atomic.Int32
			defer On(coop, func(tick) {
				calls.Add(1)
				select {
				case entered <- struct{}{}:
				default:
				}
				<-gate
			})()
			const n = 50
			for i := 1; i <= n; i++ {
				must(t, b.Emit(tick{N: i}))
			}
			recv(t, entered) // the first call is running and blocked
			if closeCore {
				b.Close()
			} else {
				coop.Close()
			}
			before := calls.Load()
			close(gate)
			waitSubs(t, b, 0)
			time.Sleep(20 * time.Millisecond)
			if after := calls.Load() - before; after > 0 {
				t.Fatalf("%d queued calls ran after Close (calls before Close = %d)", after, before)
			}
			if before != 1 {
				t.Fatalf("calls before Close = %d, want 1", before)
			}
		})
	}
}

func recvClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestWait(t *testing.T) {
	b := New(Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := make(chan stageChanged, 1)
	errc := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		e, err := Wait(ctx, b, func(e stageChanged) bool { return e.To == "dungeon" })
		res <- e
		errc <- err
	}()
	<-started
	// Wait subscribes after it starts; keep emitting until it answers.
	stop := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = b.Emit(stageChanged{To: "sea"})
			_ = b.Emit(stageChanged{To: "dungeon"})
			time.Sleep(time.Millisecond)
		}
	}()
	defer close(stop)
	must(t, <-errc)
	if e := <-res; e.To != "dungeon" || e.EventMeta().Seq == 0 {
		t.Fatalf("Wait = %+v", e)
	}
}

func TestWaitNilPredAndOld(t *testing.T) {
	b := New(Options{})
	must(t, b.Emit(tick{N: 1})) // before Wait: not seen
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Wait[tick](ctx, b, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait saw an old event: %v", err)
	}
}

func TestWaitCancellation(t *testing.T) {
	b := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Wait[tick](ctx, b, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := Wait(ctx, b, func(tick) bool { return false })
		errc <- err
	}()
	for i := 0; i < 10; i++ {
		_ = b.Emit(tick{N: i})
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait ignored cancel")
	}
	waitSubs(t, b, 0) // Wait unsubscribed
}

func TestWaitClosed(t *testing.T) {
	b := New(Options{})
	errc := make(chan error, 1)
	go func() {
		_, err := Wait[tick](context.Background(), b, nil)
		errc <- err
	}()
	waitSubs(t, b, 1)
	b.Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("err %v", err)
	}
	if _, err := Wait[tick](context.Background(), b, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("after Close: %v", err)
	}

	// Closing a mod's view ends its Wait too.
	b = New(Options{})
	coop := mustMod(t, b, "coop")
	go func() {
		_, err := Wait[tick](context.Background(), coop, nil)
		errc <- err
	}()
	waitSubs(t, b, 1)
	coop.Close()
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("view close: %v", err)
	}
}

// waitSubs waits until the bus has n subscriptions in all.
func waitSubs(t *testing.T, b *Bus, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.h.mu.Lock()
		got := 0
		for _, tp := range b.h.topics {
			got += len(tp.subs)
		}
		b.h.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subscriptions, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCloseCore(t *testing.T) {
	b := New(Options{})
	coop := mustMod(t, b, "coop")
	c1, off1 := Chan[tick](b, 4)
	c2, off2 := Chan[Event](coop, 4)
	must(t, b.Emit(tick{N: 1}))
	b.Close()
	b.Close()
	if recv(t, c1).N != 1 {
		t.Fatal("buffered event lost on Close")
	}
	if _, ok := <-c1; ok {
		t.Fatal("c1 open")
	}
	if recv(t, c2).(tick).N != 1 {
		t.Fatal("c2 buffered")
	}
	if _, ok := <-c2; ok {
		t.Fatal("c2 open")
	}
	off1()
	off2()
	if err := b.Emit(tick{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Emit: %v", err)
	}
	if err := coop.Emit(question{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("mod Emit: %v", err)
	}
	if _, err := b.Mod("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Mod: %v", err)
	}
	c3, off3 := Chan[tick](b, 1)
	if _, ok := <-c3; ok {
		t.Fatal("Chan after Close is open")
	}
	off3()
	called := false
	On(b, func(tick) { called = true })()
	coop.Close() // a no-op now
	if called {
		t.Fatal("On after Close was called")
	}
}

func TestCloseModView(t *testing.T) {
	b := New(Options{})
	coop := mustMod(t, b, "coop")
	coopSame := mustMod(t, b, "coop")
	mp := mustMod(t, b, "multiplayer")

	cc, offc := Chan[tick](coop, 4)
	defer offc()
	cn, offn := Chan[tick](coopSame.Net(), 4) // same scope
	defer offn()
	cm, offm := Chan[tick](mp, 4)
	defer offm()
	ccore, offcore := Chan[tick](b, 4)
	defer offcore()
	var onCalls atomic.Int32
	defer On(coop, func(tick) { onCalls.Add(1) })()

	coop.Close()
	coop.Close()
	for _, ch := range []<-chan tick{cc, cn} {
		if _, ok := <-ch; ok {
			t.Fatal("coop subscription open after its view closed")
		}
	}
	must(t, b.Emit(tick{N: 7}))
	if recv(t, cm).N != 7 || recv(t, ccore).N != 7 {
		t.Fatal("other subscriptions lost the event")
	}
	if err := coop.Emit(question{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed view Emit: %v", err)
	}
	if err := coopSame.Net().Emit(question{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed scope Net Emit: %v", err)
	}
	waitSubs(t, b, 2)
	time.Sleep(10 * time.Millisecond)
	if onCalls.Load() != 0 {
		t.Fatal("On of a closed view was called")
	}

	// The mod can be given a fresh view (a restart after quarantine).
	coop2 := mustMod(t, b, "coop")
	c2, off2 := Chan[question](b, 1)
	defer off2()
	must(t, coop2.Emit(question{Text: "again"}))
	if recv(t, c2).Text != "again" {
		t.Fatal("fresh view")
	}
}

// Many publishers, subscribers coming and going, and a Close at the end:
// run under -race. Every subscriber sees strictly increasing seqs.
func TestConcurrentOrdering(t *testing.T) {
	b := New(Options{})
	mods := []*Bus{mustMod(t, b, "multiplayer"), mustMod(t, b, "coop")}
	const pubs, per = 4, 2000

	type result struct {
		n   int
		err error
	}
	results := make(chan result, 16)
	var subWG sync.WaitGroup
	startSub := func(read func() (uint64, bool)) {
		subWG.Add(1)
		go func() {
			defer subWG.Done()
			var last uint64
			n := 0
			for {
				s, ok := read()
				if !ok {
					results <- result{n: n}
					return
				}
				if s <= last {
					results <- result{err: fmt.Errorf("seq %d after %d", s, last)}
					for _, ok := read(); ok; _, ok = read() {
					}
					return
				}
				last = s
				n++
			}
		}()
	}
	all, offAll := Chan[Event](b, pubs*per*3) // big enough to keep everything
	startSub(func() (uint64, bool) { e, ok := <-all; return seqOf(e, ok) })
	small, offSmall := Chan[tick](b, 7) // drops a lot
	startSub(func() (uint64, bool) { e, ok := <-small; return seqOf(e, ok) })
	viaMod, offMod := Chan[Event](mods[1], 64)
	startSub(func() (uint64, bool) { e, ok := <-viaMod; return seqOf(e, ok) })
	defer offAll()
	defer offSmall()
	defer offMod()

	var onLast atomic.Uint64
	var onBad atomic.Bool
	offOn := On(b, func(e peerJoined) {
		s := e.EventMeta().Seq
		if s <= onLast.Load() {
			onBad.Store(true)
		}
		onLast.Store(s)
	})
	defer offOn()

	var pubWG sync.WaitGroup
	for p := 0; p < pubs; p++ {
		pubWG.Add(1)
		go func(p int) {
			defer pubWG.Done()
			for i := 0; i < per; i++ {
				var err error
				switch p {
				case 0, 1:
					err = b.Emit(tick{N: i})
				case 2:
					err = mods[0].Emit(peerJoined{})
				case 3:
					err = mods[1].Emit(question{})
				}
				if err != nil {
					t.Error(err)
					return
				}
			}
		}(p)
	}
	// Churn: subscribe and unsubscribe while publishing.
	var churnWG sync.WaitGroup
	churnWG.Add(1)
	go func() {
		defer churnWG.Done()
		for i := 0; i < 200; i++ {
			ch, off := Chan[tick](b, 2)
			if i%2 == 0 {
				select {
				case <-ch:
				default:
				}
			}
			off()
			_, _ = Last[Event](b)
		}
	}()
	pubWG.Wait()
	churnWG.Wait()
	b.Close()
	subWG.Wait()
	close(results)
	total := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		total += r.n
	}
	if onBad.Load() {
		t.Fatal("On saw seqs out of order")
	}
	if b.Dropped() == 0 {
		t.Fatal("the small subscriber should have dropped")
	}
	if total < pubs*per {
		t.Fatalf("only %d events read; the big subscriber alone should see %d", total, pubs*per)
	}
	if e, _ := Last[Event](b); e.EventMeta().Seq != pubs*per {
		t.Fatalf("last seq %d", e.EventMeta().Seq)
	}
}

func seqOf(e Event, ok bool) (uint64, bool) {
	if !ok {
		return 0, false
	}
	return e.EventMeta().Seq, true
}

// Unsubscribing and closing a view race with Emit; run under -race.
func TestConcurrentCloseAndEmit(t *testing.T) {
	for round := 0; round < 20; round++ {
		b := New(Options{})
		coop := mustMod(t, b, "coop")
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			ch, off := Chan[tick](coop, 1)
			wg.Add(2)
			go func() { defer wg.Done(); drain(ch) }()
			go func() { defer wg.Done(); time.Sleep(time.Duration(i) * 100 * time.Microsecond); off() }()
		}
		defer On(coop, func(tick) {})()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = b.Emit(tick{N: i})
			}
		}()
		coop.Close()
		wg.Wait()
		b.Close()
	}
}
