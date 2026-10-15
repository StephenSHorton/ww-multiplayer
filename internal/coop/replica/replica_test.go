package replica

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// fakeClock is a Config.Now that moves only when told.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// armedRig is a replica whose file is bound to testWorld, connected,
// welcomed and armed, with its WORLD exchanged (the hub's is empty).
func armedRig(t *testing.T, cfg Config) *replicaRig {
	t.Helper()
	rr := newReplicaRig(t, cfg)
	rr.tag(rr.tagFor(testWorld, testNonce))
	rr.r.Connect(rr.hub)
	sent := rr.settle()
	if _, ok := find[*wire.CoHello](sent); !ok {
		t.Fatalf("no CO_HELLO: %s", subs(sent))
	}
	say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage|hub.FeaturePuzzle|hub.FeatureCombat), false))
	rep := rr.tick()
	sent = rr.hub.take()
	if !rep.World || rep.State != StateArmed {
		t.Fatalf("not armed after the welcome: %+v; sent %s", rep, subs(sent))
	}
	say(t, rr.r, worldOf(1, world.Image{}))
	return rr
}

func TestReplicaSession(t *testing.T) {
	rr := newReplicaRig(t, Config{})
	rr.tag(rr.tagFor(testWorld, testNonce))

	// Not connected: nothing to send, and the state says so.
	if rep := rr.tick(); rep.State != StateOff || rep.Hello {
		t.Fatalf("unconnected tick: %+v", rep)
	}

	rr.r.Connect(rr.hub)
	rr.tick()
	sent := rr.hub.take()
	h, ok := find[*wire.CoHello](sent)
	if !ok || len(sent) != 1 {
		t.Fatalf("first tick sent %s, want one CO_HELLO", subs(sent))
	}
	f, _ := bind.ReadFile(rr.m)
	want := wire.CoHello{
		Schema: wire.Schema, ClassHash: wire.ClassHash(), Slot: 0, NameHash: bind.NameHash(f.Name[:]),
		FileState: wire.FileBound, WorldID: wire.ID(testWorld), BindNonce: wire.ID(testNonce), Want: uint32(hub.FeatureImage),
	}
	if *h != want {
		t.Fatalf("CO_HELLO %+v, want %+v", *h, want)
	}

	// No welcome yet: nothing more, and no second hello.
	if sent := rr.settle(); len(sent) != 0 {
		t.Fatalf("before the welcome: sent %s", subs(sent))
	}

	say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage), false))
	rep := rr.tick()
	sent = rr.hub.take()
	if rep.State != StateArmed || !rep.World || !rep.Presence {
		t.Fatalf("after the welcome: %+v, sent %s", rep, subs(sent))
	}
	if subs(sent) != "PRESENCE WORLD" {
		t.Fatalf("after the welcome sent %s, want PRESENCE WORLD", subs(sent))
	}
	w, _ := find[*wire.World](sent)
	img := rr.image()
	wantImg := wire.Image(img)
	wire.StripLocal(&wantImg)
	if w.WorldID != wire.ID(testWorld) || w.Image != wantImg {
		t.Fatalf("WORLD %s seq %d: not the file's stripped image", w.WorldID, w.Seq)
	}
	p, _ := find[*wire.Presence](sent)
	if p.Flags&(wire.PresenceInGame|wire.PresenceArmed) != wire.PresenceInGame|wire.PresenceArmed || p.Room != outset.Room || p.Table != addr.TableSea {
		t.Fatalf("PRESENCE %+v", *p)
	}
	say(t, rr.r, worldOf(2, img))

	// Gains go up as they rise, batches counting up.
	c1, m1 := chest(addr.TableSea, 1)
	c2, m2 := chest(addr.TableSea, 2)
	var batches []uint32
	for _, c := range []struct {
		off int
		m   byte
	}{{c1, m1}, {c2, m2}} {
		rr.set(c.off, c.m)
		rr.tick()
		sent := rr.hub.take()
		g, ok := find[*wire.GainsUp](sent)
		if !ok || len(g.Gains) != 1 || g.Gains[0] != (wire.Gain{Off: uint16(c.off), Val: c.m}) {
			t.Fatalf("after a chest: sent %s %+v", subs(sent), g)
		}
		batches = append(batches, g.Batch)
	}
	if batches[1] <= batches[0] {
		t.Fatalf("batches %v do not count up", batches)
	}

	// A bit the world already has (relayed from the partner) is not
	// proposed again.
	c3, m3 := chest(addr.TableSea, 3)
	say(t, rr.r, &wire.GainsDown{Seq: 3, Origin: 2, Gains: []wire.Gain{{Off: uint16(c3), Val: m3}}})
	if st := rr.r.Status(); len(st.Planned) != 1 || st.Planned[0].Off != uint16(c3) {
		t.Logf("planned before the tick: %+v", st.Planned)
	}
	rr.tick()
	if st := rr.r.Status(); len(st.Planned) != 1 || st.Planned[0].Off != uint16(c3) || st.Seq != 3 {
		t.Fatalf("planned %+v at seq %d, want the partner's chest at seq 3", st.Planned, st.Seq)
	}
	rr.set(c3, m3)
	rr.tick()
	if g := all[*wire.GainsUp](rr.hub.take()); len(g) != 0 {
		t.Fatalf("re-proposed a bit the world has: %+v", g)
	}
	if st := rr.r.Status(); len(st.Planned) != 0 {
		t.Fatalf("still planned once the game has it: %+v", st.Planned)
	}
}

func TestReplicaPresence(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	rr := armedRig(t, Config{Now: clock.now})
	for i := range 5 {
		if rep := rr.tick(); rep.Presence {
			t.Fatalf("tick %d: PRESENCE with nothing changed and no time passed", i)
		}
	}
	clock.t = clock.t.Add(PresenceEvery)
	if rep := rr.tick(); !rep.Presence {
		t.Fatal("no PRESENCE after a second")
	}
	rr.hub.take()
	rr.enter(45, 44)
	rr.tick()
	p, ok := find[*wire.Presence](rr.hub.take())
	if !ok || p.Room != 45 {
		t.Fatalf("no PRESENCE for the room change: %+v", p)
	}
	rr.g.SetPauseMenu(true)
	rr.tick()
	if p, ok := find[*wire.Presence](rr.hub.take()); !ok || p.Flags&wire.PresencePaused == 0 {
		t.Fatalf("pause not in PRESENCE: %+v", p)
	}
}

// TestReplicaDryRunNeverWrites drives an armed replica through a whole
// session (a world full of bits, relays, scoped stores, every CTRL,
// transitions and holds) and checks that it never writes game memory,
// while it plans and logs what it would write.
func TestReplicaDryRunNeverWrites(t *testing.T) {
	rr := armedRig(t, Config{Policy: world.FullPolicy()})
	rr.m.Record(true)

	full := fullView()
	say(t, rr.r, worldOf(10, full))
	rr.settle()
	st := rr.r.Status()
	if len(st.Planned) == 0 || len(st.Held) == 0 {
		t.Fatalf("planned %d, held %d: want both", len(st.Planned), len(st.Held))
	}
	for _, w := range st.Planned {
		for _, a := range w.Addrs() {
			if !rr.r.Guard().Allows(a, 1) {
				t.Fatalf("planned write %+v at 0x%08X is outside the guard", w, a)
			}
		}
	}
	if !rr.log.has("dry-run: would set") || !rr.log.has("dry-run: holding") || !rr.log.has("nothing is written") {
		t.Fatalf("log lacks the dry-run lines:\n%s", rr.log)
	}

	stage, _ := wire.MakeStage(outset.Name)
	sea := int8(addr.TableSea)
	room := outset.Room
	say(t, rr.r,
		&wire.GainsDown{Seq: 11, Origin: 2, Gains: []wire.Gain{{Off: uint16(addr.OffGetItem + 3), Val: 0xFF}}},
		&wire.Scoped{Kind: wire.ScopeDan, Stage: stage, Table: sea, Room: -1, Words: []wire.ScopedWord{{Word: 0, Bits: 0x0F}}},
		&wire.Scoped{Kind: wire.ScopeZone, Stage: stage, Table: sea, Room: room, Words: []wire.ScopedWord{{Word: 1, Bits: 0x3}}},
		&wire.Scoped{Kind: wire.ScopeRoomEdge, Stage: stage, Table: sea, Room: room, Words: []wire.ScopedWord{{Word: 2, Bits: 0x8}}},
		&wire.Scoped{Kind: wire.ScopeKill, Stage: stage, Table: sea, Room: room, Words: []wire.ScopedWord{{Word: 3, Bits: 0x80}}},
		&wire.Notice{Code: wire.NoticeTombstone, Text: "the host blocked something"},
		&wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(uint16(addr.OffGetItem+3), 0x01)},
		&wire.Ctrl{Op: wire.CtrlResync},
		&wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(hub.FeatureImage)},
	)
	rr.settle()
	for _, want := range []string{"dan switch 0x80", "zone switch 0xD0", "zone switch 0xE3", "actor setID 103", "the host blocked"} {
		if !rr.log.has(want) {
			t.Errorf("log lacks %q", want)
		}
	}
	rr.load(drc)
	rr.settle()
	rr.enter(1, 0)
	rr.settle()
	rr.load(outset)
	rr.settle()
	if w := rr.m.Writes(); len(w) != 0 {
		t.Fatalf("the dry run wrote game memory: %+v", w)
	}
}

func TestReplicaPlanLogsOnce(t *testing.T) {
	rr := armedRig(t, Config{})
	c, m := chest(addr.TableDRC, 7)
	var img world.Image
	img[c] = m
	say(t, rr.r, worldOf(2, img))
	label := DefaultLabel(c, bits.TrailingZeros8(m))
	for range 10 {
		rr.tick()
	}
	if n := rr.log.count("would set " + label); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, rr.log)
	}
}

// leakedRig is a replica whose file carries tag tg, connected, with the
// hub's unbound welcome w handled and the gates open.
func leakedRig(t *testing.T, tg bind.Tag, saved bool, w *wire.CoWelcome) *replicaRig {
	t.Helper()
	rr := newReplicaRig(t, Config{})
	b := tg.Encode()
	rr.store(addr.Reserve, b[:])
	if saved {
		rr.saved()
	}
	rr.m.Record(true)
	rr.r.Connect(rr.hub)
	sent := rr.settle()
	h, ok := find[*wire.CoHello](sent)
	if !ok {
		t.Fatalf("no CO_HELLO: %s", subs(sent))
	}
	say(t, rr.r, w)
	_ = h
	return rr
}

func TestReplicaScrub(t *testing.T) {
	// V2 fails: a tag carried in from slot 1.
	carried := func(rr *rig) bind.Tag { tg := rr.tagFor(testWorld, testNonce); tg.Slot = 1; return tg }
	probe := newRig(t, "Link")
	tg := carried(probe)

	t.Run("confirmed", func(t *testing.T) {
		rr := leakedRig(t, tg, true, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), false))
		rr.settle()
		if len(rr.offers) != 1 || rr.offers[0].Plan.Kind != bind.KindScrub {
			t.Fatalf("offers %+v, want one scrub", rr.offers)
		}
		if w := rr.m.Writes(); len(w) != 0 {
			t.Fatalf("wrote before the user said yes: %+v", w)
		}
		rr.r.Confirm(rr.offers[0].Plan)
		rep := rr.tick()
		w := rr.m.Writes()
		if !rep.TagWritten || len(w) != 1 || w[0].Addr != addr.Reserve || !bytes.Equal(w[0].Data, make([]byte, bind.TagSize)) {
			t.Fatalf("scrub wrote %+v (report %+v), want 0x30 zero bytes at 0x%08X", w, rep, addr.Reserve)
		}
		h, ok := find[*wire.CoHello](rr.hub.take())
		if !ok || h.FileState != wire.FileUnbound || !h.WorldID.IsZero() {
			t.Fatalf("after the scrub: hello %+v", h)
		}
		rr.settle()
		if len(rr.m.Writes()) != 1 || len(rr.offers) != 1 {
			t.Fatalf("wrote (%d) or offered (%d) again after the scrub:\n%s", len(rr.m.Writes()), len(rr.offers), rr.log)
		}
	})
	t.Run("declined", func(t *testing.T) {
		rr := leakedRig(t, tg, true, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), false))
		rr.settle()
		if len(rr.offers) != 1 {
			t.Fatalf("offers %+v", rr.offers)
		}
		rr.r.Decline(rr.offers[0].Plan)
		rr.settle()
		rr.r.Confirm(rr.offers[0].Plan) // too late: no longer offered
		rr.settle()
		if len(rr.offers) != 1 || len(rr.m.Writes()) != 0 {
			t.Fatalf("after a no: %d offers, writes %+v", len(rr.offers), rr.m.Writes())
		}
	})
	t.Run("V5 only, and the hub says no fresh bind", func(t *testing.T) {
		tg := probe.tagFor(testWorld, testNonce)
		rr := leakedRig(t, tg, false, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), false))
		rr.settle()
		if len(rr.offers) != 1 || rr.offers[0].Plan.Kind != bind.KindScrub {
			t.Fatalf("offers %+v, want one scrub", rr.offers)
		}
	})
	t.Run("V5 only, and the nonce is a pending fresh bind", func(t *testing.T) {
		tg := probe.tagFor(testWorld, testNonce)
		rr := leakedRig(t, tg, false, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), true))
		rr.tick()
		h, ok := find[*wire.CoHello](rr.hub.take())
		if !ok || h.FileState != wire.FileBound || hub.Feature(h.Want)&hub.AskUnsaved == 0 {
			t.Fatalf("a fresh bind's file: hello %+v, want bound with AskUnsaved", h)
		}
		rr.settle()
		if len(rr.offers) != 0 || len(rr.m.Writes()) != 0 {
			t.Fatalf("offers %+v, writes %+v", rr.offers, rr.m.Writes())
		}
	})
	t.Run("V5 only, nothing known yet", func(t *testing.T) {
		tg := probe.tagFor(someWorld, testNonce)
		rr := leakedRig(t, tg, false, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), false))
		rr.settle()
		if len(rr.offers) != 0 {
			t.Fatalf("offered a scrub of another world's possible fresh bind: %+v", rr.offers)
		}
	})
	t.Run("V5 only, a pending fresh bind, then the host refuses", func(t *testing.T) {
		// The host comes back on another build and refuses for the class
		// table. A refusal's FreshBind is always false: it must not turn
		// the pending fresh bind into a leak to scrub.
		tg := probe.tagFor(testWorld, testNonce)
		rr := leakedRig(t, tg, false, welcome(wire.StatusUnbound, uint32(hub.FeatureImage), true))
		rr.tick()
		say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage), true))
		rr.settle()
		rr.r.Disconnect()
		rr.r.Connect(rr.hub)
		sent := rr.settle()
		if h, ok := find[*wire.CoHello](sent); !ok || h.FileState != wire.FileBound {
			t.Fatalf("on reconnect: hello %+v (sent %s)", h, subs(sent))
		}
		say(t, rr.r, &wire.CoWelcome{Status: wire.StatusRefused, Reason: wire.NoticeSchema, WorldID: wire.ID(testWorld)})
		rr.settle()
		if len(rr.offers) != 0 || len(rr.m.Writes()) != 0 {
			t.Fatalf("after a refusal: offers %+v, writes %+v", rr.offers, rr.m.Writes())
		}
		if st := rr.r.Status(); st.Verdict.State != bind.Bound || !st.Verdict.Fresh {
			t.Fatalf("after a refusal the file reads %s (%s), want bound through the fresh bind", st.Verdict.State, st.Verdict.Why)
		}
	})
}

// TestReplicaScopedBounded: the replica keeps the scoped stores a host
// sends only once welcomed as bound with the kind's feature, only for the
// stage Link is in, and never more than maxScopedIn of them.
func TestReplicaScopedBounded(t *testing.T) {
	sea := int8(addr.TableSea)
	here, _ := wire.MakeStage(outset.Name)
	dan := &wire.Scoped{Kind: wire.ScopeDan, Stage: here, Table: sea, Room: -1, Words: []wire.ScopedWord{{Word: 0, Bits: 0x0F}}}
	kill := func(t *testing.T, i int) *wire.Scoped {
		st, err := wire.MakeStage(fmt.Sprintf("Z%06d", i))
		if err != nil {
			t.Fatal(err)
		}
		return &wire.Scoped{Kind: wire.ScopeKill, Stage: st, Table: sea, Room: 0, Words: []wire.ScopedWord{{Word: 0, Bits: 1}}}
	}
	held := func(r *Replica) int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.scopedIn)
	}
	bound := func(t *testing.T, f hub.Feature) *replicaRig {
		rr := newReplicaRig(t, Config{Want: hub.DefaultFeatures})
		rr.tag(rr.tagFor(testWorld, testNonce))
		rr.r.Connect(rr.hub)
		rr.settle()
		say(t, rr.r, welcome(wire.StatusOK, uint32(f), false))
		return rr
	}

	t.Run("an unbound file", func(t *testing.T) {
		rr := unboundRig(t, hub.DefaultFeatures, world.Image{})
		for i := range 300 {
			say(t, rr.r, kill(t, i))
		}
		say(t, rr.r, dan)
		rr.settle()
		if n := held(rr.r); n != 0 {
			t.Fatalf("an unbound replica keeps %d scoped stores", n)
		}
	})
	t.Run("a feature not granted", func(t *testing.T) {
		rr := bound(t, hub.FeatureImage|hub.FeatureCombat)
		say(t, rr.r, dan)
		if n := held(rr.r); n != 0 {
			t.Fatalf("kept a DAN store without the puzzle feature (%d)", n)
		}
	})
	t.Run("sent with the welcome, before the replica arms", func(t *testing.T) {
		rr := bound(t, hub.DefaultFeatures)
		say(t, rr.r, dan, worldOf(1, world.Image{}))
		rr.settle()
		if !rr.log.has("would set dan switch 0x80") {
			t.Fatalf("the store sent with the welcome was not planned:\n%s", rr.log)
		}
	})
	t.Run("a flood of stages", func(t *testing.T) {
		rr := armedRig(t, Config{})
		for i := range 5000 {
			say(t, rr.r, kill(t, i))
			if n := held(rr.r); n > maxScopedIn {
				t.Fatalf("after %d stores: %d kept, cap %d", i+1, n, maxScopedIn)
			}
			if i%1000 == 999 {
				rr.tick()
				if n := held(rr.r); n != 0 {
					t.Fatalf("a tick in %s kept %d stores of other stages", outset.Name, n)
				}
			}
		}
		if !rr.log.has("more scoped stores than one stage has") {
			t.Fatalf("the cap was not logged:\n%s", rr.log)
		}
		say(t, rr.r, dan)
		rr.tick()
		if !rr.log.has("would set dan switch 0x80") {
			t.Fatalf("after the flood, the stage's own store was not planned:\n%s", rr.log)
		}
	})
}

// flakyMem is a Mem whose reads fail while fail is set.
type flakyMem struct {
	mem.Mem
	fail bool
}

func (f *flakyMem) Read(a uint32, n int) ([]byte, error) {
	if f.fail {
		return nil, errFail
	}
	return f.Mem.Read(a, n)
}

// TestReplicaRebaselineSendsWorld: progress that rises while the differ
// has no baseline (a save state, a failed read) is in the new baseline, so
// no GAINS carries it; the WORLD that follows does.
func TestReplicaRebaselineSendsWorld(t *testing.T) {
	c, m := chest(addr.TableSea, 30)
	cases := []struct {
		name string
		gap  func(rr *replicaRig, fm *flakyMem)
	}{
		{"a save state holding a chest the world lacks", func(rr *replicaRig, _ *flakyMem) {
			rr.store(addr.SpawnTrigger, []byte{0, 0, 0, 1})
			rr.set(c, m)
			rr.r.Tick()
		}},
		{"a chest opened across a failed read", func(rr *replicaRig, fm *flakyMem) {
			fm.fail = true
			if rep := rr.tick(); rep.Err == nil {
				t.Fatal("the read did not fail")
			}
			fm.fail = false
			rr.set(c, m)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := armedRig(t, Config{})
			fm := &flakyMem{Mem: rr.m}
			rr.r.m = fm
			rr.settle()
			tc.gap(rr, fm)
			var worlds []*wire.World
			for range 5 {
				rr.tick()
				worlds = append(worlds, all[*wire.World](rr.hub.take())...)
			}
			if len(worlds) != 1 || worlds[0].Image[c]&m == 0 {
				t.Fatalf("%d WORLDs after the gap (want one carrying the chest)", len(worlds))
			}
		})
	}
}

// unboundRig is a replica with a never-saved New Game, connected, its
// unbound welcome and the hub's WORLD (img) handled.
func unboundRig(t *testing.T, f hub.Feature, img world.Image) *replicaRig {
	t.Helper()
	rr := newReplicaRig(t, Config{})
	rr.m.Record(true)
	rr.r.Connect(rr.hub)
	sent := rr.settle()
	h, ok := find[*wire.CoHello](sent)
	if !ok || h.FileState != wire.FileUnbound || hub.Feature(h.Want) != hub.FeatureImage|hub.AskUnsaved {
		t.Fatalf("unbound hello %+v (sent %s)", h, subs(sent))
	}
	say(t, rr.r, welcome(wire.StatusUnbound, uint32(f), false), worldOf(1, img))
	return rr
}

func TestReplicaBind(t *testing.T) {
	t.Run("a fresh file into an empty world", func(t *testing.T) {
		rr := unboundRig(t, hub.FeatureImage, world.Image{})
		rr.tick()
		if len(rr.pfs) != 1 || rr.pfs[0].Verdict != PreflightSame {
			t.Fatalf("preflights %+v", rr.pfs)
		}
		if st := rr.r.Status(); st.State != StateUnbound || st.Preflight == nil {
			t.Fatalf("status %+v", st)
		}
		rr.r.RequestBind()
		rr.settle()
		if len(rr.offers) != 1 || rr.offers[0].Plan.Kind != bind.KindBind || rr.offers[0].Preflight == nil {
			t.Fatalf("offers %+v, want one bind with its preflight", rr.offers)
		}
		p := rr.offers[0].Plan
		if p.Tag.World != testWorld || !p.Fresh || !strings.Contains(p.Prompt, "Bind it?") {
			t.Fatalf("plan %+v", p)
		}
		rr.r.Confirm(p)
		rr.tick()
		sent := rr.hub.take()
		if subs(sent) != "WORLD CO_HELLO" {
			t.Fatalf("the ask sent %s, want WORLD CO_HELLO", subs(sent))
		}
		h := sent[1].(*wire.CoHello)
		if hub.Feature(h.Want) != hub.FeatureImage|hub.AskBind|hub.AskUnsaved || h.WorldID != wire.ID(testWorld) ||
			h.BindNonce != wire.ID(p.Tag.Nonce) || h.FileState != wire.FileUnbound {
			t.Fatalf("bind hello %+v", h)
		}
		rr.settle()
		if w := rr.m.Writes(); len(w) != 0 {
			t.Fatalf("wrote before the hub's yes: %+v", w)
		}
		if again := all[*wire.CoHello](rr.hub.take()); len(again) != 0 {
			t.Fatalf("asked again: %+v", again)
		}

		say(t, rr.r, welcome(wire.StatusUnbound, uint32(hub.FeatureImage|hub.FeatureBind), true))
		rep := rr.tick()
		w := rr.m.Writes()
		want := p.Bytes()
		if !rep.TagWritten || len(w) != 1 || w[0].Addr != addr.Reserve || !bytes.Equal(w[0].Data, want[:]) {
			t.Fatalf("bind wrote %+v, want the plan's tag at 0x%08X", w, addr.Reserve)
		}
		h, ok := find[*wire.CoHello](rr.hub.take())
		if !ok || h.FileState != wire.FileBound || h.BindNonce != wire.ID(p.Tag.Nonce) || hub.Feature(h.Want) != hub.FeatureImage|hub.AskUnsaved {
			t.Fatalf("bound hello %+v", h)
		}
		say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage), true))
		if rep := rr.tick(); rep.State != StateArmed || !rep.World {
			t.Fatalf("after the bind: %+v", rep)
		}
		rr.settle()
		if len(rr.m.Writes()) != 1 {
			t.Fatalf("writes %+v", rr.m.Writes())
		}
	})
	t.Run("the host says no", func(t *testing.T) {
		rr := unboundRig(t, hub.FeatureImage, world.Image{})
		rr.r.RequestBind()
		rr.settle()
		rr.r.Confirm(rr.offers[0].Plan)
		rr.tick()
		rr.hub.take()
		say(t, rr.r, &wire.CoWelcome{Status: wire.StatusUnbound, Reason: wire.NoticeDivergent, Features: uint32(hub.FeatureImage), WorldID: wire.ID(testWorld)})
		rr.settle()
		sent := rr.hub.take()
		for _, h := range all[*wire.CoHello](sent) {
			if hub.Feature(h.Want)&hub.AskBind != 0 {
				t.Fatalf("asked again after a no: %+v", h)
			}
		}
		if len(rr.m.Writes()) != 0 || !rr.log.has("did not accept the bind") {
			t.Fatalf("writes %+v; log:\n%s", rr.m.Writes(), rr.log)
		}
	})
	t.Run("a file ahead of the world needs --coop-merge", func(t *testing.T) {
		rr := unboundRig(t, hub.FeatureImage, world.Image{})
		c, m := chest(addr.TableSea, 5)
		rr.set(c, m)
		rr.r.RequestBind()
		rr.settle()
		if len(rr.offers) != 0 || !rr.log.has("can't bind") {
			t.Fatalf("offers %+v; log:\n%s", rr.offers, rr.log)
		}
	})
	t.Run("a file ahead of the world, with --coop-merge", func(t *testing.T) {
		rr := unboundRig(t, hub.FeatureImage|hub.FeatureMerge, world.Image{})
		c, m := chest(addr.TableSea, 5)
		rr.set(c, m)
		rr.r.RequestBind()
		rr.settle()
		if len(rr.offers) != 1 || rr.offers[0].Preflight.Verdict != PreflightAhead {
			t.Fatalf("offers %+v", rr.offers)
		}
	})
}

func TestReplicaOffline(t *testing.T) {
	rr := armedRig(t, Config{})
	c1, m1 := chest(addr.TableSea, 11)
	c2, m2 := chest(addr.TableSea, 20)
	rr.r.Disconnect()
	if st := rr.r.Status(); st.State != StateOffline {
		t.Fatalf("after a disconnect: %s (%s)", st.State, st.Why)
	}
	// One gain the game takes back again before the host returns (a
	// timer bit, say), one it keeps.
	rr.set(c1, m1)
	rr.tick()
	rr.clear(c1, m1)
	rr.set(c2, m2)
	rr.tick()
	if st := rr.r.Status(); st.Queued != 2 {
		t.Fatalf("queued %d gain bytes, want 2", st.Queued)
	}

	h2 := &hubEnd{t: t}
	rr.r.Connect(h2)
	sent := func() []wire.Msg {
		var out []wire.Msg
		for range 3 {
			rr.tick()
			out = append(out, h2.take()...)
		}
		return out
	}()
	if subs(sent) != "CO_HELLO" {
		t.Fatalf("on reconnect sent %s, want CO_HELLO", subs(sent))
	}
	say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage), false))
	rr.tick()
	sent = h2.take()
	if subs(sent) != "PRESENCE WORLD GAINS" {
		t.Fatalf("after the welcome sent %s, want PRESENCE WORLD GAINS", subs(sent))
	}
	w, _ := find[*wire.World](sent)
	g, _ := find[*wire.GainsUp](sent)
	if w.Image[c1]&m1 != 0 || w.Image[c2]&m2 == 0 {
		t.Fatal("the WORLD is not the file's image now")
	}
	gotC1 := false
	for _, x := range g.Gains {
		gotC1 = gotC1 || x.Off == uint16(c1) && x.Val&m1 != 0
	}
	if !gotC1 {
		t.Fatalf("GAINS %+v lack the gain the game took back", g.Gains)
	}
	if st := rr.r.Status(); st.Queued != 0 {
		t.Fatalf("queue not emptied: %d", st.Queued)
	}
}

func TestReplicaCtrl(t *testing.T) {
	rr := armedRig(t, Config{})
	c1, m1 := chest(addr.TableSea, 21)
	c2, m2 := chest(addr.TableSea, 22)

	say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: 0})
	rr.set(c1, m1)
	if rep := rr.tick(); rep.State != StateUnbound || rep.Gains != 0 || !strings.Contains(rep.Why, "turned image sync off") {
		t.Fatalf("image off: %+v", rep)
	}
	say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(hub.FeatureImage)}, &wire.Ctrl{Op: wire.CtrlResync})
	rr.tick()
	sent := rr.hub.take()
	if _, ok := find[*wire.World](sent); !ok {
		t.Fatalf("no WORLD after RESYNC: %s", subs(sent))
	}

	say(t, rr.r, &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(uint16(c2), m2)})
	rr.set(c2, m2)
	rr.tick()
	if g := all[*wire.GainsUp](rr.hub.take()); len(g) != 0 {
		t.Fatalf("proposed a tombstoned bit: %+v", g)
	}
	say(t, rr.r, &wire.Ctrl{Op: wire.CtrlResync})
	rr.tick()
	if w, ok := find[*wire.World](rr.hub.take()); !ok || w.Image[c2]&m2 != 0 {
		t.Fatal("the WORLD after a tombstone still carries the bit")
	}
}

func TestReplicaRefusals(t *testing.T) {
	t.Run("the host serves another world than the session's", func(t *testing.T) {
		rr := newReplicaRig(t, Config{World: someWorld})
		rr.tag(rr.tagFor(someWorld, testNonce))
		rr.r.Connect(rr.hub)
		rr.settle()
		say(t, rr.r, welcome(wire.StatusOK, uint32(hub.FeatureImage), false))
		rr.settle()
		if st := rr.r.Status(); st.State != StateRefused {
			t.Fatalf("status %s (%s)", st.State, st.Why)
		}
		if sent := rr.hub.take(); len(all[*wire.World](sent)) != 0 || len(all[*wire.Presence](sent)) != 0 {
			t.Fatalf("sent %s to a host of another world", subs(sent))
		}
	})
	t.Run("the hub refuses", func(t *testing.T) {
		rr := newReplicaRig(t, Config{})
		rr.r.Connect(rr.hub)
		rr.settle()
		say(t, rr.r, &wire.CoWelcome{Status: wire.StatusRefused, Reason: wire.NoticeSchema, WorldID: wire.ID(testWorld)})
		rr.settle()
		if st := rr.r.Status(); st.State != StateRefused || len(rr.hub.take()) != 0 {
			t.Fatalf("status %s (%s)", st.State, st.Why)
		}
	})
	t.Run("malformed payloads", func(t *testing.T) {
		rr := armedRig(t, Config{})
		for range 5 {
			rr.r.Handle([]byte{0xEE, 1, 2})
			rr.r.Handle(nil)
		}
		if n := rr.log.count("malformed"); n != 1 {
			t.Fatalf("logged %d malformed lines, want 1", n)
		}
		if st := rr.r.Status(); st.State != StateArmed {
			t.Fatalf("status %s", st.State)
		}
	})
	t.Run("an unreadable game", func(t *testing.T) {
		r, err := New(Config{Mem: failMem{}})
		if err != nil {
			t.Fatal(err)
		}
		r.Connect(&hubEnd{t: t})
		if rep := r.Tick(); rep.Err == nil || rep.State != StateOff {
			t.Fatalf("report %+v", rep)
		}
	})
	t.Run("no memory", func(t *testing.T) {
		if _, err := New(Config{}); err == nil {
			t.Fatal("New without Mem succeeded")
		}
	})
}

// failMem is a Mem whose every access fails.
type failMem struct{}

var errFail = errors.New("the emulator went away")

func (failMem) Read(uint32, int) ([]byte, error) { return nil, errFail }
func (failMem) Write(uint32, []byte) error       { return errFail }

var _ mem.Mem = failMem{}

// TestReplicaOutOfOrder hands an armed replica the hub's messages late,
// early and twice: its view only grows, and ends as the world.
func TestReplicaOutOfOrder(t *testing.T) {
	rr := armedRig(t, Config{})
	c1, m1 := chest(addr.TableDRC, 1)
	c2, m2 := chest(addr.TableDRC, 9)
	c3, m3 := chest(addr.TableDRC, 17)
	gain := func(seq uint64, off int, m byte) *wire.GainsDown {
		return &wire.GainsDown{Seq: seq, Origin: 2, Gains: []wire.Gain{{Off: uint16(off), Val: m}}}
	}
	at4 := imageOf(c1, m1)
	at6 := imageOf(c1, m1, c2, m2, c3, m3)
	say(t, rr.r,
		gain(5, c2, m2), gain(5, c2, m2), // a relay, twice
		worldOf(4, at4), // a WORLD from before it
		gain(3, c1, m1), // and an older relay
	)
	view, seq := func() (world.Image, uint64) {
		rr.r.mu.Lock()
		defer rr.r.mu.Unlock()
		return rr.r.view, rr.r.viewSeq
	}()
	if want := imageOf(c1, m1, c2, m2); view != want || seq != 5 {
		t.Fatalf("view %s at seq %d, want the chests of seq 5", fmtGains(world.Diff(&world.Image{}, &view)), seq)
	}
	say(t, rr.r, gain(6, c3, m3), worldOf(6, at6), worldOf(4, at4))
	rr.tick()
	st := rr.r.Status()
	if got := writesImage(st.Planned, world.Image{}); got != at6 || st.Seq != 6 {
		t.Fatalf("planned %+v at seq %d, want the three chests at seq 6", st.Planned, st.Seq)
	}
}
