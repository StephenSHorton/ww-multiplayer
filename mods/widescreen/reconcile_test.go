package widescreen

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops/opstest"
)

// game is a fake game for the reconcile loop: the ops runtime with WS_SET
// as widescreen.c's handler (knob included), ws_init's block, the painter
// box, and a frame goroutine.
type game struct {
	t    *testing.T
	m    *mem.FakeMem
	mu   sync.Mutex // the fake C is not safe for two goroutines
	c    *opstest.C
	sets atomic.Int32
	halt atomic.Bool // no frames: commands wait
}

func newGame(t *testing.T) *game {
	g := &game{t: t, m: mem.NewFakeMem()}
	g.c = opstest.New(g.m)
	g.boot()
	g.put32(PainterAddr, PainterVanilla)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if !g.halt.Load() {
				g.mu.Lock()
				g.c.Frame()
				g.mu.Unlock()
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return g
}

func (g *game) put32(a, v uint32) {
	var b [4]byte
	mem.PutU32BE(b[:], v)
	_ = g.m.Store(a, b[:])
}

// boot is main01_init: ops_init, then ws_init.
func (g *game) boot() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.c.Boot(nil)
	blk := make([]byte, mailbox.WsSize)
	mem.PutU32BE(blk[mailbox.WsMagicOff:], mailbox.WsMagic)
	blk[mailbox.WsVersionOff], blk[mailbox.WsModeOff] = 1, mailbox.WsModeClip
	mem.PutU32BE(blk[mailbox.WsFactorOff:], mailbox.WsFactorDefault)
	mem.PutU32BE(blk[mailbox.WsUIFactorOff:], math.Float32bits(1))
	_ = g.m.Store(mailbox.WsAddr, blk)
	g.put32(mailbox.WsKnobAddr, mailbox.WsKnobVanilla)
	g.c.Register(mailbox.OpsOpWsSet, func(a, b, cc uint32) uint8 {
		g.sets.Add(1)
		f := math.Float32frombits(b)
		if a > 2 || !mailbox.WsFactorValid(f) {
			return mailbox.OpsRRefused
		}
		_ = g.m.Store(mailbox.WsAddr+mailbox.WsModeOff, []byte{byte(a)})
		g.put32(mailbox.WsAddr+mailbox.WsFactorOff, b)
		_ = g.m.Store(mailbox.WsAddr+mailbox.WsFlagsOff, []byte{byte(cc & mailbox.WsFlagsSupported)})
		g.put32(mailbox.WsKnobAddr, KnobFor(mailbox.Widescreen{Present: true, Mode: uint8(a), Factor: f}))
		return mailbox.OpsROK
	})
}

func (g *game) block() mailbox.Widescreen {
	b, _ := mailbox.ReadWidescreen(g.m)
	return b
}

func (g *game) waitBlock(what string, ok func(mailbox.Widescreen) bool) {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok(g.block()) {
		if time.Now().After(deadline) {
			g.t.Fatalf("%s: block %s", what, g.block())
		}
		time.Sleep(time.Millisecond)
	}
}

type recLog struct {
	mu    sync.Mutex
	lines []string
}

func (r *recLog) Log(_ log.Level, msg string) {
	r.mu.Lock()
	r.lines = append(r.lines, msg)
	r.mu.Unlock()
}

func (r *recLog) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// TestReconcile: the loop waits for the runtime, sends mode 2 once, leaves
// the flags byte alone, heals a reboot, drops to mode 0 under the Gecko
// code with one warning, and waits for its own last ticket when stopped.
func TestReconcile(t *testing.T) {
	g := newGame(t)
	// Not booted yet: no runtime, nothing sent.
	g.mu.Lock()
	_ = g.m.Store(mailbox.OpsAddr, make([]byte, 4))
	g.mu.Unlock()

	s, _ := launch.ParseWidescreenSetting("16:9")
	var view atomic.Value
	view.Store(Dolphin{GFX: launch.GFXAspect{Mode: launch.AspectForceWide, CustomW: 1, CustomH: 1}})
	rep := &recLog{}
	q := ops.New(g.m)
	r := &Reconciler{Q: q, M: g.m, Setting: s, Rep: rep, Fast: time.Millisecond, Slow: 5 * time.Millisecond,
		Dolphin: func() (Dolphin, bool) { return view.Load().(Dolphin), true }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	if n := g.sets.Load(); n != 0 {
		t.Fatalf("%d WS_SETs before the runtime was up", n)
	}
	g.boot()
	f169 := math.Float32frombits(0x3FAAAAAB)
	g.waitBlock("native 16:9", func(b mailbox.Widescreen) bool { return b.Mode == 2 && b.Factor == f169 })
	if k, _ := mem.ReadU32(g.m, mailbox.WsKnobAddr); k != 0x3FE38E39 {
		t.Fatalf("knob %08X", k)
	}
	time.Sleep(30 * time.Millisecond)
	if n := g.sets.Load(); n != 1 {
		t.Fatalf("%d WS_SETs, want 1", n)
	}

	// A tester's flags byte is not a difference.
	_ = g.m.Store(mailbox.WsAddr+mailbox.WsFlagsOff, []byte{3})
	time.Sleep(30 * time.Millisecond)
	if n := g.sets.Load(); n != 1 {
		t.Fatalf("flags resubmitted: %d WS_SETs", n)
	}

	// A reboot reset: ws_init puts mode 1 back; the loop sends mode 2 again.
	g.boot()
	g.waitBlock("native after the reboot", func(b mailbox.Widescreen) bool { return b.Mode == 2 && b.Factor == f169 })

	// The Gecko code: mode 0 and one warning.
	g.put32(PainterAddr, PainterGecko)
	g.waitBlock("off under the Gecko", func(b mailbox.Widescreen) bool { return b.Mode == 0 && b.Factor == 1 })
	time.Sleep(30 * time.Millisecond)
	if n := strings.Count(rep.text(), "$16:9 Widescreen"); n != 1 {
		t.Fatalf("Gecko warned %d times:\n%s", n, rep.text())
	}
	g.put32(PainterAddr, PainterVanilla)
	g.waitBlock("native again", func(b mailbox.Widescreen) bool { return b.Mode == 2 })

	// Stopping with a ticket in flight waits for it.
	g.halt.Store(true)
	head := func() uint32 { v, _ := mem.ReadU32(g.m, mailbox.OpsAddr+mailbox.OpsCmdHeadOff); return v }
	h0 := head()
	view.Store(Dolphin{GFX: launch.GFXAspect{Hack: true, Mode: launch.AspectForceWide}})
	for deadline := time.Now().Add(5 * time.Second); head() == h0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the demotion was never sent")
		}
	}
	cancel()
	select {
	case <-done:
		t.Fatal("returned with its ticket not run")
	case <-time.After(20 * time.Millisecond):
	}
	g.halt.Store(false)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b := g.block(); b.Mode != 1 {
		t.Fatalf("the last ticket did not run: %s", b)
	}
	if !strings.Contains(rep.text(), "native widescreen (mode 2") {
		t.Fatalf("log:\n%s", rep.text())
	}
}

// TestReconcileUnknownDolphin: with Dolphin's settings unknown nothing is
// sent, so C's default stays.
func TestReconcileUnknownDolphin(t *testing.T) {
	g := newGame(t)
	r := &Reconciler{Q: ops.New(g.m), M: g.m, Rep: log.Discard{}, Fast: time.Millisecond, Slow: time.Millisecond,
		Dolphin: func() (Dolphin, bool) { return Dolphin{}, false }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := g.sets.Load(); n != 0 {
		t.Fatalf("%d WS_SETs", n)
	}
}

// TestReconcileRebootWindow: a reboot reloads the DOL, so the knob reads
// vanilla while the block still says mode 2 until ws_init runs. That one
// window must not read as another widescreen writer (no warning, no
// demote); a knob that stays foreign still warns on the next tick.
func TestReconcileRebootWindow(t *testing.T) {
	g := newGame(t)
	s, _ := launch.ParseWidescreenSetting("16:9")
	rep := &recLog{}
	r := &Reconciler{Q: ops.New(g.m), M: g.m, Setting: s, Rep: rep,
		Dolphin: func() (Dolphin, bool) {
			return Dolphin{GFX: launch.GFXAspect{Mode: launch.AspectForceWide, CustomW: 1, CustomH: 1}}, true
		}}
	st := &reconcileState{}
	for i := 0; i < 200 && g.block().Mode != 2; i++ {
		r.tick(st, time.Hour)
		time.Sleep(2 * time.Millisecond)
	}
	if g.block().Mode != 2 {
		t.Fatalf("never native: %s", g.block())
	}
	r.tick(st, time.Hour) // settle: applied, nothing pending
	sets := g.sets.Load()

	// The reboot's window: the knob is vanilla, the block still mode 2.
	g.halt.Store(true)
	g.put32(mailbox.WsKnobAddr, mailbox.WsKnobVanilla)
	r.tick(st, time.Hour)
	if strings.Contains(rep.text(), "camera aspect") || g.sets.Load() != sets {
		t.Fatalf("reboot window read as a foreign writer: %q, %d sets", rep.text(), g.sets.Load()-sets)
	}
	// ws_init runs: mode 1, knob vanilla; the loop sends mode 2 again, no warning.
	g.halt.Store(false)
	g.boot()
	for i := 0; i < 200 && g.block().Mode != 2; i++ {
		r.tick(st, time.Hour)
		time.Sleep(2 * time.Millisecond)
	}
	if g.block().Mode != 2 || strings.Contains(rep.text(), "camera aspect") {
		t.Fatalf("after ws_init: %s, log %q", g.block(), rep.text())
	}
	// A knob that stays foreign is still reported, on the second tick.
	r.tick(st, time.Hour)
	g.halt.Store(true)
	g.put32(mailbox.WsKnobAddr, mailbox.WsKnobVanilla)
	r.tick(st, time.Hour)
	r.tick(st, time.Hour)
	if !strings.Contains(rep.text(), "camera aspect") {
		t.Fatalf("a foreign knob was not reported: %q", rep.text())
	}
}

// errMem fails every read.
type errMem struct{}

func (errMem) Read(uint32, int) ([]byte, error) { return nil, errors.New("no read") }

// TestAtTitle: sea_T (NUL-padded) is the title; other stages, a name that
// only starts with sea_T and a read error are not.
func TestAtTitle(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"sea_T\x00\x00\x00", true},
		{"sea_T\x00xy", true},
		{"sea\x00\x00\x00\x00\x00", false},
		{"sea_Tx\x00\x00", false},
		{"sea_TTTT", false},
		{"\x00\x00\x00\x00\x00\x00\x00\x00", false},
	} {
		m := mem.NewFakeMem()
		if err := m.Store(addr.CurStageName, []byte(c.name)); err != nil {
			t.Fatal(err)
		}
		if got := atTitle(m); got != c.want {
			t.Errorf("atTitle(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	if atTitle(errMem{}) {
		t.Error("a read error counts as the title")
	}
}

// TestReconcileMarksAndPending2D drives tick by hand (no Dolphin view, so
// nothing is submitted by the loop): the three boot marks log once each,
// and the 2D-pending note logs on the rising edge and the "follows" line
// on the falling edge, once each.
func TestReconcileMarksAndPending2D(t *testing.T) {
	g := newGame(t)
	g.mu.Lock()
	_ = g.m.Store(addr.CurStageName, []byte("sea_T\x00\x00\x00"))
	g.mu.Unlock()
	rep := &recLog{}
	q := ops.New(g.m)
	r := &Reconciler{Q: q, M: g.m, Rep: rep, Dolphin: func() (Dolphin, bool) { return Dolphin{}, false }}
	s := &reconcileState{}
	count := func(sub string) int { return strings.Count(rep.text(), sub) }
	tick := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		r.tick(s, time.Hour)
	}
	// A WS_SET of the test's own, answered OK, for the "first WS_SET OK" mark.
	submit := func() {
		tk, err := q.Submit(mailbox.OpWsSet, mailbox.WsModeClip, mailbox.WsFactorDefault, 0xFF)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if res, err := q.Wait(ctx, tk); err != nil || res != ops.ROK {
			t.Fatalf("WS_SET: %v %v", res, err)
		}
		s.pending = &tk
	}
	tick()
	submit()
	tick()
	submit()
	tick()
	tick()
	for _, m := range []string{"first ready probe", "first probe on the title screen", "first WS_SET OK"} {
		if n := count("widescreen: " + m + " at "); n != 1 {
			t.Errorf("mark %q logged %d times:\n%s", m, n, rep.text())
		}
	}
	if count(mailbox.Pending2DNote) != 0 || count("2D follows") != 0 {
		t.Fatalf("2D lines with nothing pending:\n%s", rep.text())
	}

	// Mode 2 with F_2D, not latched: pending (once, however many ticks).
	set := func(mode, flags, ui byte, factor float32) {
		g.mu.Lock()
		defer g.mu.Unlock()
		_ = g.m.Store(mailbox.WsAddr+mailbox.WsModeOff, []byte{mode})
		_ = g.m.Store(mailbox.WsAddr+mailbox.WsFlagsOff, []byte{flags, ui})
		g.put32(mailbox.WsAddr+mailbox.WsFactorOff, math.Float32bits(factor))
		// ws_latch writes the latched factor with ui_mode (no WsFHUD here,
		// so ui_shift stays 0).
		uf := float32(1)
		if ui == mailbox.WsModeNative {
			uf = factor
		}
		g.put32(mailbox.WsAddr+mailbox.WsUIFactorOff, math.Float32bits(uf))
	}
	set(mailbox.WsModeNative, mailbox.WsF2D, 0, 4.0/3.0)
	tick()
	tick()
	if n := count(mailbox.Pending2DNote); n != 1 {
		t.Errorf("pending logged %d times on the rising edge:\n%s", n, rep.text())
	}
	// The stage load latched it: follows (once).
	set(mailbox.WsModeNative, mailbox.WsF2D, mailbox.WsModeNative, 4.0/3.0)
	tick()
	tick()
	if n := count("widescreen: 2D follows the setting"); n != 1 {
		t.Errorf("follows logged %d times on the falling edge:\n%s", n, rep.text())
	}
	// Mode 2 at factor 1: C latches nothing, so nothing is pending.
	set(mailbox.WsModeNative, mailbox.WsF2D, 0, 1)
	tick()
	if n := count(mailbox.Pending2DNote); n != 1 {
		t.Errorf("mode 2 at factor 1 reported pending:\n%s", rep.text())
	}
}
