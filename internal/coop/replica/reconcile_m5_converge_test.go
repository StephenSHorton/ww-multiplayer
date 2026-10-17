package replica

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// startM5Host is a host whose hub grants the default features over tb:
// its stage tables' identity, its memory-switch verdict and its verdict on
// the switches SCOPED carries.
func startM5Host(t *testing.T, tb *tables.Tables) *host {
	t.Helper()
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	st, err := world.Create(t.TempDir(), world.New(world.ID(testWorld), time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Config{Store: st, Features: hub.DefaultFeatures, StageTblHash: id,
		SyncedSwitch: memorySwitches(tb), SyncedScoped: scopedSwitches(tb)})
	if err != nil {
		t.Fatal(err)
	}
	srv := network.NewServer(0)
	srv.Coop = h
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Stop()
		if err := h.Stop(); err != nil {
			t.Errorf("hub stop: %v", err)
		}
	})
	return &host{store: st, hub: h, addr: fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port)}
}

// m5Player is a loopback player whose replica writes, over a zoneMem, with
// every SCOPED it sent and was sent kept.
type m5Player struct {
	*m3Player
	zm *zoneMem

	mu      sync.Mutex
	in, out []wire.Scoped // from the hub; to it
}

func newM5Player(t *testing.T, ctx context.Context, h *host, cfg Config, name string, nonce bind.Nonce, seed uint64) *m5Player {
	t.Helper()
	p := &player{rig: newRig(t, name), log: &logBuf{}}
	p.tag(p.tagFor(testWorld, nonce))
	bm := &beatMem{FakeMem: p.m, game: p.g}
	zm := &zoneMem{beatMem: bm}
	cfg.Mem, cfg.Logf, cfg.Apply, cfg.BeatTimeout = zm, p.log.logf, true, time.Second
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.r = r
	p.u = &unruly{rng: rand.New(rand.NewPCG(seed, 99)), r: r}
	mp := &m5Player{m3Player: &m3Player{player: p, bm: bm}, zm: zm}
	p.client = network.NewCoopClient(name, 0)
	p.client.OnCoop = func(b []byte) {
		mp.tap(t, &mp.in, b, wire.Down)
		p.u.onCoop(b)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, p.client, h.addr, nil, func(ctx context.Context, send func([]byte) error) error {
			return p.u.session(ctx, func(b []byte) error {
				mp.tap(t, &mp.out, b, wire.Up)
				return send(b)
			})
		})
	}()
	t.Cleanup(func() { <-done })
	return mp
}

// tap keeps payload b if it is a SCOPED.
func (p *m5Player) tap(t *testing.T, to *[]wire.Scoped, b []byte, dir wire.Dir) {
	m, err := wire.Decode(b, dir)
	if err != nil {
		t.Errorf("an undecodable co-op payload: %v", err)
		return
	}
	if sc, ok := m.(*wire.Scoped); ok {
		p.mu.Lock()
		*to = append(*to, *sc)
		p.mu.Unlock()
	}
}

func (p *m5Player) taps() (in, out []wire.Scoped) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wire.Scoped(nil), p.in...), append([]wire.Scoped(nil), p.out...)
}

// is reports whether switch no is set for room in p's game.
func (p *m5Player) is(no, room int) bool {
	p.t.Helper()
	on, err := p.g.IsSwitch(no, room)
	if err != nil {
		p.t.Fatalf("IsSwitch(0x%02X, %d): %v", no, room, err)
	}
	return on
}

// wrote counts p's scoped writes that set switch no.
func (p *m5Player) wrote(no int) int {
	g := NewGuardFor(M5Targets)
	n := 0
	for _, w := range p.bm.written() {
		if g.TargetOf(w.a) != TargetScoped {
			continue
		}
		sw := ScopedWrite{Kind: wire.ScopeDan, Addr: w.a, Bits: w.val &^ w.old, Room: -1, Slot: -1}
		if w.a >= addr.Zones {
			sw.Kind, sw.Slot = wire.ScopeZone, int8(int(w.a-addr.Zones)/addr.ZoneSize)
		}
		for _, s := range sw.Switches() {
			if s == no {
				n++
			}
		}
	}
	return n
}

// unsafeBits lists the bits of scs whose switch tb does not mark latching
// for the SCOPED's own scope.
func unsafeBits(tb *tables.Tables, scs []wire.Scoped) []string {
	var out []string
	for _, sc := range scs {
		if sc.Kind == wire.ScopeKill {
			continue
		}
		first, width := addr.SwitchZoneFirst, 16
		if sc.Kind == wire.ScopeDan {
			first, width = addr.SwitchDanFirst, 32
		}
		for _, w := range sc.Words {
			for b := range width {
				no := first + int(w.Word)*width + b
				if w.Bits&(1<<b) != 0 && !synctables.SyncedScopedSwitch(tb, int(sc.Table), sc.Stage.String(), int(sc.Room), no) {
					out = append(out, fmt.Sprintf("%v %s room %d: 0x%02X", sc.Kind, sc.Stage, sc.Room, no))
				}
			}
		}
	}
	return out
}

// TestConvergeM5Loopback is M5's acceptance test: two writing replicas on
// their own fake games, with the M1 tables of the same disc, and the real
// hub granting the default features over its own verdict, behind a
// loopback network.Server. Both play Dragon Roost Cavern's room 1.
//
//  1. A sets a latching dan, zone and room switch, and the timed ones,
//     which do not latch: B gets the three latching ones, the room switch
//     once, and none of the others, which never travel at all.
//  2. B goes through a transition storm while A sets more: room changes
//     (clearRoomSwitch, room 1's zone removed and created again in
//     another slot), a table change and back (initDan) and a reload of
//     the stage (initZone).
//  3. Both set fresh switches: each gets the other's.
//  4. B in another room gets none of A's zone or room switches, and gets
//     the zone switch, never the room one, on coming back; B in another
//     stage of the table gets none of A's dan switches until it is back.
//
// The write audit (auditM5Writes) holds for both all along: every scoped
// write a byte, a set of a latching switch, made when IDLE into the slot
// that was the zone of Link's room as it was made, no switch set twice in
// its scope, and every other write by M4's audit.
func TestConvergeM5Loopback(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	tb := m5Tables(t)
	h := startM5Host(t, tb)
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{StageTblHash: id, Policy: hub.SessionPolicy(memorySwitches(tb)), Tables: tb}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients
	a := newM5Player(t, ctx, h, cfg, "Link", bind.Nonce{1}, 5)
	b := newM5Player(t, ctx, h, cfg, "Tetra", bind.Nonce{2}, 6)
	players := []*m5Player{a, b}
	logs := func() string { return fmt.Sprintf("A:\n%s\nB:\n%s", a.log, b.log) }
	run := func(n int) {
		for range n {
			for _, p := range players {
				p.step()
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\n%s", what, logs())
			}
			run(1)
		}
	}
	// hold runs n rounds, failing as soon as bad reports something.
	hold := func(n int, bad func() string) {
		t.Helper()
		for range n {
			run(1)
			if why := bad(); why != "" {
				t.Fatalf("%s\n%s", why, logs())
			}
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// load changes p's stage to s as the game does: mNextStage set for a
	// few ticks, then the new stage.
	load := func(s fakegame.Stage, ps ...*m5Player) {
		t.Helper()
		for _, p := range ps {
			must(p.g.SetNextStage(s))
		}
		run(4)
		for _, p := range ps {
			must(p.g.ChangeStage())
		}
		run(3 * gate.StableTicks)
	}
	// sees waits for p's replica to have the hub's word that the partner
	// is in stage s, room room.
	sees := func(p *m5Player, s fakegame.Stage, room int) {
		t.Helper()
		waitFor(fmt.Sprintf("the partner in %s room %d", s.Name, room), func() bool {
			pr := p.r.Status().Partner
			return pr != nil && pr.Stage.String() == s.Name && int(pr.Room) == room
		})
	}

	waitFor("both armed with the room puzzles", func() bool {
		for _, p := range players {
			st := p.r.Status()
			if st.State != StateArmed || st.Features&hub.FeaturePuzzle == 0 {
				return false
			}
		}
		return true
	})
	load(drcKeys, a, b)
	sees(a, drcKeys, 1)
	sees(b, drcKeys, 1)

	// 1. A's switches, latching and not.
	for _, no := range []int{danOK, zoneOK, roomOK, danTimed, zoneTimed, roomTimed} {
		a.onSwitch(no, 1)
	}
	waitFor("B to have A's dan, zone and room switches", func() bool {
		return b.is(danOK, 1) && b.is(zoneOK, 1) && b.is(roomOK, 1)
	})
	hold(50, func() string {
		for _, no := range []int{danTimed, zoneTimed, roomTimed} {
			if b.is(no, 1) {
				return fmt.Sprintf("B has A's 0x%02X, which does not latch", no)
			}
		}
		return ""
	})

	// 2. B's storm, with A setting more of room 1's switches meanwhile.
	storm := []func(){
		func() { b.enter(2, 1) },
		func() { a.onSwitch(zoneOK2, 1) },
		func() { b.enter(3) }, // room 1's zone is removed
		func() { a.onSwitch(danOK2, 1) },
		func() { b.enter(1) }, // and created again, in another slot
		func() { a.onSwitch(roomOK2, 1) },
		func() { load(woods, b) }, // initDan
		func() { load(drcKeys, b) },
		func() { load(drcKeys, b) }, // initZone
		func() { must(a.g.OpenKeyChest(1)) },
	}
	for i, step := range storm {
		step()
		run(2 + 3*(i%3))
	}
	sees(a, drcKeys, 1)
	run(3 * gate.StableTicks)

	// 3. Fresh switches, set while both are in the room: each gets the
	// other's.
	a.onSwitch(m5Dan[2], 1)
	a.onSwitch(m5Zone[2], 1)
	a.onSwitch(m5Room[2], 1)
	b.onSwitch(m5Dan[3], 1)
	b.onSwitch(m5Zone[3], 1)
	b.onSwitch(m5Room[3], 1)
	waitFor("both to have the fresh switches", func() bool {
		for _, p := range players {
			for _, no := range []int{m5Dan[2], m5Dan[3], m5Zone[2], m5Zone[3]} {
				if !p.is(no, 1) {
					return false
				}
			}
		}
		return b.is(m5Room[2], 1) && a.is(m5Room[3], 1)
	})

	// 4. Other rooms and stages get nothing. B steps into room 2: A's
	// zone and room switches of room 1 stay A's.
	b.enter(2, 1)
	sees(a, drcKeys, 2)
	a.onSwitch(m5Zone[4], 1)
	a.onSwitch(m5Room[4], 1)
	bad := func() string {
		if b.is(m5Zone[4], 1) || b.is(m5Room[4], 1) {
			return "B in room 2 got A's switches of room 1"
		}
		return ""
	}
	hold(200, bad)
	b.enter(1, 2)
	waitFor("B back in room 1 to get the zone switch", func() bool { return b.is(m5Zone[4], 1) })
	hold(100, func() string {
		if b.is(m5Room[4], 1) {
			return "a room edge of A's was replayed into B's new visit"
		}
		return ""
	})
	// B in Dragon Roost's boss stage, of the same table: A's dan switch
	// stays A's until B is back in the cavern.
	load(drcBoss, b)
	sees(a, drcBoss, 0)
	a.onSwitch(m5Dan[4], 1)
	hold(200, func() string {
		if b.is(m5Dan[4], 0) {
			return "B in another stage of the table got A's dan switch"
		}
		return ""
	})
	load(drcKeys, b)
	waitFor("B back in the cavern to get the dan switch", func() bool { return b.is(m5Dan[4], 1) })
	run(3 * gate.StableTicks)

	// The audit, both ways.
	for k, p := range players {
		ws, zs := p.bm.written(), p.zm.zones()
		if len(ws) != len(zs) {
			t.Fatalf("player %c: %d writes, %d zone tables", 'A'+k, len(ws), len(zs))
		}
		auditM5Writes(t, ws, zs, p.reps, p.bm.placesSeen(), tb, p.r.Status().Writes.Derived)
		in, out := p.taps()
		if u := unsafeBits(tb, in); len(u) > 0 {
			t.Errorf("player %c was sent switches that do not latch: %s", 'A'+k, strings.Join(u, "; "))
		}
		if u := unsafeBits(tb, out); len(u) > 0 {
			t.Errorf("player %c proposed switches that do not latch: %s", 'A'+k, strings.Join(u, "; "))
		}
		if st := p.r.Status(); st.WritesOff != "" || st.Writes.Failed != 0 || st.Writes.Scoped == 0 {
			t.Errorf("player %c: off %q, %+v", 'A'+k, st.WritesOff, st.Writes)
		}
	}
	for _, c := range []struct {
		p    *m5Player
		name string
		no   int
		want int
	}{
		{b, "A's first room switch", roomOK, 1},
		{b, "A's fresh room switch", m5Room[2], 1},
		{a, "B's fresh room switch", m5Room[3], 1},
		{b, "A's room switch set while B was in room 2", m5Room[4], 0},
		{b, "A's dan switch set while B was in the boss stage", m5Dan[4], 1},
		{b, "a timed dan crystal's switch", danTimed, 0},
		{b, "a timed zone crystal's switch", zoneTimed, 0},
		{b, "a timed room crystal's switch", roomTimed, 0},
	} {
		if got := c.p.wrote(c.no); got != c.want {
			t.Errorf("%s (0x%02X) written %d times, want %d", c.name, c.no, got, c.want)
		}
	}
	if t.Failed() {
		t.Logf("%s", logs())
	}
	t.Logf("A: %+v; B: %+v", a.r.Status().Writes, b.r.Status().Writes)
}
