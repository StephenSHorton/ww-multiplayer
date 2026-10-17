package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// M6.0 through the session plumbing (§7.2, §9 M6.0): a partner's kills of
// keyed enemies between two writing replicas of a --coop session, written
// into the receiver's zones and light-shrinking its live enemies, with the
// write audit over every byte the pipelines wrote and a tap on every
// SCOPED KILL either sent or got.
//
// The fixture is tablestest's vanilla disc with Dragon Roost Cavern
// (M_NewD2) holding the M6 enemies, the same in both games: in room 1
// keyed Bokoblins (the first holding its weapon), a ChuChu, a ReDead, a
// Floormaster, a Bokoblin of type 0xA (BOSS on creation), a Kargaroc, two
// unkeyed Bokoblins and a Darknut, a class not yet verified; in room 2 a
// Bokoblin with room 1's first setID and a ChuChu.

// coM6Enemies are the fixture's enemies.
var coM6Enemies = []struct {
	name   string
	proc   stage.Proc
	room   int
	setID  uint16
	boss   bool
	weapon bool
}{
	{"Bk", stage.ProcBK, 1, 1, false, true},
	{"Bk", stage.ProcBK, 1, 2, false, false},
	{"c_green", stage.ProcCC, 1, 3, false, false},
	{"Rdead1", stage.ProcRD, 1, 4, false, false},
	{"Fmaster", stage.ProcFM, 1, 5, false, false},
	{"Bk", stage.ProcBK, 1, 6, true, false},
	{"Bb", stage.ProcBB, 1, 7, false, false},
	{"Bk", stage.ProcBK, 1, 0xFFFF, false, false},
	{"Bk", stage.ProcBK, 1, 0xFFFF, false, false},
	{"Tn", stage.ProcTN, 1, 9, false, false},
	{"Bk", stage.ProcBK, 2, 1, false, false},
	{"c_green", stage.ProcCC, 2, 8, false, false},
}

// The fixture's enemies by role, indexes into coM6Enemies.
const (
	coM6BK, coM6BK2, coM6CC, coM6RD, coM6FM, coM6Boss, coM6BB, coM6Unkeyed, coM6Unkeyed2, coM6TN, coM6R2BK, coM6R2CC = 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11
)

func coM6Stages() []stagetest.Stage {
	var room1, room2 []stagetest.Actor
	for _, e := range coM6Enemies {
		a := tablestest.Enemy(e.name, e.setID)
		if e.boss {
			a.Params = 0xA
		}
		if e.room == 1 {
			room1 = append(room1, a)
		} else {
			room2 = append(room2, a)
		}
	}
	stages := tablestest.Vanilla()
	for i, s := range stages {
		if s.Name == coDRC.Name {
			stages[i] = tablestest.Dungeon(coDRC.Name, keys.TableDRC, room1...)
			stages[i].Rooms = append(stages[i].Rooms, tablestest.Room(2, room2...))
		}
	}
	return stages
}

func coM6Placed() *fakegame.Enemies {
	var es []fakegame.Enemy
	for _, e := range coM6Enemies {
		es = append(es, fakegame.Enemy{Stage: coDRC.Name, Room: e.room, SetID: e.setID, Proc: e.proc, Boss: e.boss, Weapon: e.weapon})
	}
	return fakegame.PlaceEnemies(es...)
}

// coKillAt is a dead setID in its scope.
type coKillAt struct {
	stage string
	table int8
	room  int8
	setID int
}

func (k coKillAt) String() string {
	return fmt.Sprintf("setID %d of %s (table %d, room %d)", k.setID, k.stage, k.table, k.room)
}

// coAuditM6Kill checks write w at a zone's dead-actor byte by M6's rules
// (§7.2) and returns the setIDs it set: one byte, OR only, ARMED and IDLE
// with the gates open, into the zone that, as it was made, was a room's
// (its mRoomNo names the room and the room's mZoneNo the slot), and only
// setIDs the tables sync there.
func coAuditM6Kill(w coWrite, rep replica.Report, tb *tables.Tables, bad func(format string, args ...any)) []coKillAt {
	if len(w.val) != 1 || w.zone == nil {
		bad("%d bytes (zone table recorded: %v): the kill path writes bytes", len(w.val), w.zone != nil)
		return nil
	}
	old, val := w.old[0], w.val[0]
	switch {
	case old&^val != 0:
		bad("a clear of dead actors")
	case old == val:
		bad("wrote what was there")
	case rep.State != replica.StateArmed:
		bad("a kill write while %s", rep.State)
	case !rep.Gates.CanWriteIdle():
		bad("a kill write, not IDLE with the gates open: %s", rep.Gates.Why())
	}
	slot := int(w.a-addr.Zones) / addr.ZoneSize
	z := w.zone
	room := int(z.roomNo[slot])
	if room < 0 || room >= addr.RoomCount || int(z.zoneNo[room]) != slot {
		bad("zone slot %d is no room's (its mRoomNo %d)", slot, room)
		return nil
	}
	za, _ := addr.ZoneAddr(slot)
	rel := int(w.a - za - addr.ZoneOffActor)
	var out []coKillAt
	for b := range 8 {
		if (val&^old)&(1<<b) == 0 {
			continue
		}
		id := 32*(rel/4) + 8*(3-rel%4) + b
		if !synctables.SyncedKill(tb, int(w.table), z.stage, room, id) {
			bad("setID %d of %s room %d does not sync", id, z.stage, room)
		}
		out = append(out, coKillAt{stage: z.stage, table: w.table, room: int8(room), setID: id})
	}
	return out
}

// coAuditM6Heap checks write w on the actor heap by the light-shrink's
// rules: one byte, ARMED and IDLE with the gates open, and either a
// verified K1 class's timer going from 0 to 1, or a weapon's CARRY byte
// losing CARRY and nothing else. Which actor it was, and whether the
// partner killed it, the M6 test judges (coM6Judge).
func coAuditM6Heap(w coWrite, rep replica.Report, bad func(format string, args ...any)) {
	if len(w.val) != 1 {
		bad("%d bytes on the actor heap: the light-shrink writes one", len(w.val))
		return
	}
	switch {
	case rep.State != replica.StateArmed:
		bad("a light-shrink write while %s", rep.State)
	case !rep.Gates.CanWriteIdle():
		bad("a light-shrink write, not IDLE with the gates open: %s", rep.Gates.Why())
	}
	off := (w.a - fakegame.EnemyHeap) % coBlock
	old, val := w.old[0], w.val[0]
	if off == addr.ActorOffCarryByte {
		if old&addr.ActorCarryByteMask == 0 || val != old&^addr.ActorCarryByteMask {
			bad("a weapon's CARRY byte 0x%02X -> 0x%02X", old, val)
		}
		return
	}
	for _, c := range combat.Classes() {
		if off == c.Ice+addr.EnemyIceOffTimer {
			if !c.Verified {
				bad("the timer of an unverified %s", c.Name)
			}
			if old != 0 || val != 1 {
				bad("a timer %d -> %d", int8(old), int8(val))
			}
			return
		}
	}
	bad("a heap write at block +0x%X", off)
}

// coM6Seen is what each game's zones have recorded dead, ever, as the
// test reads them after every change it makes and every step.
type coM6Seen map[coKillAt]bool

func (s coM6Seen) look(cg *coGame) {
	p, err := coPlaceOf(cg.m)
	if err != nil || p.stage == "" || !addr.ValidTable(int(p.table)) {
		return
	}
	for room := range addr.RoomCount {
		zn, _ := addr.ZoneNoAddr(room)
		slot := int(int8(cg.read(zn, 1)[0]))
		za, err := addr.ZoneAddr(slot)
		if err != nil || int8(cg.read(za+addr.ZoneOffRoomNo, 1)[0]) != int8(room) {
			continue
		}
		words := cg.read(za+addr.ZoneOffActor, addr.ZoneSize-addr.ZoneOffActor)
		for i, b := range words {
			for bit := range 8 {
				if b&(1<<bit) != 0 {
					s[coKillAt{stage: p.stage, table: p.table, room: int8(room), setID: 32*(i/4) + 8*(3-i%4) + bit}] = true
				}
			}
		}
	}
}

// coM6Dead reports whether setID id is recorded dead in room's zone of
// cg's game now.
func coM6Dead(cg *coGame, room, id int) bool {
	cg.t.Helper()
	z, err := cg.g.ZoneOf(room)
	if err != nil || z < 0 {
		return false
	}
	za, _ := addr.ZoneAddr(z)
	bit, _ := addr.ZoneActorBit(id)
	return cg.read(za+uint32(bit.Off), 1)[0]&bit.Mask != 0
}

// coKillTap checks every SCOPED KILL that crosses the wire, either way:
// each bit must be a key the fixture's tables sync. No BOSS, Morth,
// shared or unkeyed death travels.
type coKillTap struct {
	tb *tables.Tables

	mu  sync.Mutex
	n   int
	bad []string
}

func (tp *coKillTap) check(who string, dir wire.Dir, p []byte) {
	if len(p) == 0 || wire.Sub(p[0]) != wire.SubScoped {
		return
	}
	m, err := wire.Decode(p, dir)
	sc, ok := m.(*wire.Scoped)
	if err != nil || !ok || sc.Kind != wire.ScopeKill {
		return
	}
	var bad []string
	for _, w := range sc.Words {
		for b := range 32 {
			if id := 32*int(w.Word) + b; w.Bits&(1<<b) != 0 && !synctables.SyncedKill(tp.tb, int(sc.Table), sc.Stage.String(), int(sc.Room), id) {
				bad = append(bad, fmt.Sprintf("%s: a %s KILL carries setID %d of %s room %d, which does not sync", who, dir, id, sc.Stage, sc.Room))
			}
		}
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.n++
	tp.bad = append(tp.bad, bad...)
}

// newCoPlayerM6 is newCoPlayerM5 with the game's M6 enemies running on its
// frames and every SCOPED KILL checked by kills.
func newCoPlayerM6(t *testing.T, ctx context.Context, h *coHost, cs *coopSession, cg *coGame, es *fakegame.Enemies, name string, audit *coAudit, kills *coKillTap, seed uint64) *coPlayer {
	t.Helper()
	beat := newCoBeat(cg)
	beat.enemies = es
	p := &coPlayer{coGame: cg, name: name, rep: &coLog{}, beat: beat}
	s, err := cs.newSync(ctx, name, beat, p.rep)
	if err != nil {
		t.Fatal(err)
	}
	p.s = s
	p.u = &coWire{rng: rand.New(rand.NewPCG(seed, 7)), r: s.r, audit: audit}
	s.client.OnCoop = func(b []byte) {
		kills.check(name, wire.Down, b)
		p.u.onCoop(b)
	}
	session := func(ctx context.Context, send func([]byte) error) error {
		return p.u.session(ctx, func(b []byte) error {
			kills.check(name, wire.Up, b)
			return send(b)
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, s.client, h.addr, nil, session)
	}()
	t.Cleanup(func() { <-done })
	return p
}

// TestCoopSessionM6 is M6.0's acceptance test through the --coop session
// plumbing: the host founds a world from A's file and runs A's pipeline in
// its own session; B joins from a session of its own; both take their
// stage tables from --coop-iso (the M6 fixture) and write; Link's demo
// offset is taken as verified (combat.DemoTypeVerified). Scripted play
// first, in DRC:
//
//   - same room: A kills its first Bokoblin; B records it dead, its
//     Bokoblin light-shrinks and lets its weapon go after the timer;
//   - a hidden neighbour: B's room 2 hidden behind a door, A kills room 2's
//     enemies; B's zone of room 2 records them and B's room 2, shown
//     again, does not create them;
//   - B in another stage gets nothing of A's kill until it comes back,
//     when the hub's store brings it (B's ChuChu spawns, then shrinks);
//   - both leave the stage: the store goes, and the enemies come back;
//   - an unkeyed kill, a BOSS Bokoblin's and a Kargaroc's own death travel
//     nowhere; a Kargaroc A light-shrinks does;
//   - the pause menu holds B's shrink, and a ReDead waiting on its switch
//     is held until it moves; B's own ReDead dying is never targeted;
//   - a Darknut, made unverified here (every K1 class passed the live
//     check K1), is recorded, never light-shrunk.
//
// Then both play at random: kills, room changes, doors hiding and showing
// rooms, stage changes, events and the pause menu, while the connections
// deliver out of order, repeat and drop, and the host restarts. At the
// end both enter DRC afresh, each kills enemies of its own, and both games
// hold the union of the synced deaths and neither has a victim alive.
// Through it all:
//
//   - the write audit (coAuditM3 with coAuditM6Kill and coAuditM6Heap)
//     holds over every byte either pipeline wrote, and every earlier rule;
//   - a dead-actor bit was set only for a death the partner's game had
//     recorded in that scope; a light-shrink only for a live enemy of such
//     a key, never a BOSS one, never twice, its weapon only after its
//     timer on the same tick;
//   - no SCOPED KILL carried a key that does not sync, and the fake games
//     never halted (an onActor with no zone);
//   - the startup lines, /status and the hub's lines say what combat does.
func TestCoopSessionM6(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	demo := combat.DemoTypeVerified
	combat.DemoTypeVerified = true
	t.Cleanup(func() { combat.DemoTypeVerified = demo }) // after the players' cleanups
	tnVerified := combat.SetVerified(int16(stage.ProcTN), false)
	t.Cleanup(func() { combat.SetVerified(int16(stage.ProcTN), tnVerified) })
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(coM6Stages()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    strings.NewReader(""),
		tables:   loadCoopStageTables,
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true, iso: iso})
	defer h.stop()
	cs := h.cs
	tb := cs.m1()
	if tb == nil {
		t.Fatalf("the host has no stage tables:\n%s", h.rep)
	}
	for i, e := range coM6Enemies {
		want := e.setID != 0xFFFF && !e.boss
		if got := synctables.SyncedKill(tb, addr.TableDRC, coDRC.Name, e.room, int(e.setID)); got != want {
			t.Fatalf("the fixture's verdict on enemy %d is %v, want %v", i, got, want)
		}
	}
	joinIn, joinKeys := coInput(t)
	jlog := &coLog{}
	jcs := startCoopSession(jlog, coopFlags{on: true, yes: true, iso: iso}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: joinIn, tables: loadCoopStageTables})
	for _, l := range []*coLog{h.rep, jlog} {
		if !l.has("co-op: combat this session: a partner's kills of keyed enemies (9 in 1 stage(s), K1 classes, no bosses) are recorded in this game and light-shrink the live enemy while both are in the same stage; 2 unkeyed enemies") ||
			l.has("not yet checked live") {
			t.Errorf("the session did not say what of M6 it writes:\n%s", l)
		}
	}
	audit := &coAudit{}
	kills := &coKillTap{tb: tb}
	esA, esB := coM6Placed(), coM6Placed()
	a := newCoPlayerM6(t, ctx, h, cs, ga, esA, "Link", audit, kills, 3)
	b := newCoPlayerM6(t, ctx, h, jcs, gb, esB, "Tetra", audit, kills, 4)
	players := []*coPlayer{a, b}
	other := map[*coPlayer]*coPlayer{a: b, b: a}
	es := map[*coPlayer]*fakegame.Enemies{a: esA, b: esB}
	seen := map[*coPlayer]coM6Seen{a: {}, b: {}}
	logs := func() string { return fmt.Sprintf("host:\n%s\nA:\n%s\nB:\n%s", h.rep, a.rep, b.rep) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	do := func(p *coPlayer, err error) {
		t.Helper()
		must(err)
		seen[p].look(p.coGame)
	}
	stepAll := func() {
		t.Helper()
		for _, p := range players {
			p.step()
			seen[p].look(p.coGame)
			if err := p.beat.actErr; err != nil {
				t.Fatalf("%s's game halted: %v", p.name, err)
			}
		}
	}
	run := func(n int) {
		t.Helper()
		for range n {
			stepAll()
			time.Sleep(time.Millisecond)
		}
	}
	until := func(why string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("%s\n%s", why, logs())
			}
			stepAll()
			time.Sleep(2 * time.Millisecond)
		}
	}
	welcomes := func(p *coPlayer) int { return p.rep.count("welcome to world") }
	need := map[*coPlayer]int{}
	const want = hub.FeatureCombat | hub.FeaturePuzzle | hub.M4Features
	waitArmed := func(why string) {
		t.Helper()
		until(why+": not armed with M6's features", func() bool {
			for _, p := range players {
				if st := p.s.r.Status(); st.State != replica.StateArmed || st.Features&want != want || welcomes(p) < need[p] {
					return false
				}
			}
			return true
		})
	}
	live := func(p *coPlayer, i int) (fakegame.LiveEnemy, bool) { return es[p].Of(i) }
	// kill is p's own death of enemy i, as its class's code makes it.
	kill := func(p *coPlayer, i int) {
		t.Helper()
		l, ok := live(p, i)
		if !ok {
			t.Fatalf("%s's enemy %d is not live", p.name, i)
		}
		do(p, es[p].Kill(p.g, l.PID))
	}
	gone := func(p *coPlayer, i int) bool { _, ok := live(p, i); return !ok }
	inRoom := func(p *coPlayer, room int) bool {
		st := p.s.r.Status().Combat
		return st.Place.Stage == coDRC.Name && st.Place.Room == room
	}
	together := func(room int) {
		t.Helper()
		until(fmt.Sprintf("the players are never together in room %d", room), func() bool {
			hs, err := h.cs.hub.Status()
			if err != nil || len(hs.Peers) != 2 {
				return false
			}
			for _, ps := range hs.Peers {
				if ps.Place.Stage != coDRC.Name {
					return false
				}
			}
			return inRoom(a, room) && inRoom(b, room)
		})
		run(gate.StableTicks + 2)
	}
	enterDRC := func(p *coPlayer) {
		t.Helper()
		do(p, p.g.LoadStage(coDRCKeys))
		do(p, p.g.EnterRoom(1, 2))
	}
	waitArmed("binding")
	for _, p := range players {
		if !p.rep.has("co-op: combat: written to this game when idle: the partner's kills of keyed enemies") {
			t.Errorf("%s did not say combat is written:\n%s", p.name, p.rep)
		}
	}
	for _, p := range players {
		enterDRC(p)
	}
	together(1)

	// 1. Same room: A's kill is recorded in B, and B's Bokoblin shrinks
	// and lets go of its weapon.
	wp, _ := es[b].WeaponOf(coM6BK)
	kill(a, coM6BK)
	until("A's kill never reached B's zone", func() bool { return coM6Dead(b.coGame, 1, 1) })
	until("B's Bokoblin never let go of its weapon", func() bool {
		return gb.read(wp.Addr+addr.ActorOffCarryByte, 1)[0]&addr.ActorCarryByteMask == 0
	})
	until("B's Bokoblin never shrank away", func() bool { return gone(b, coM6BK) })
	if !b.rep.has("light-shrunk the partner's kill: actor") || !b.rep.has("its weapon let go") {
		t.Errorf("B's log:\n%s", b.rep)
	}

	// 2. A hidden neighbour: the door to room 2 closed behind B.
	do(b, gb.g.HideRoom(2))
	do(a, ga.g.EnterRoom(2, 1))
	until("A never in room 2", func() bool { return inRoom(a, 2) })
	run(gate.StableTicks + 2)
	kill(a, coM6R2BK)
	kill(a, coM6R2CC)
	until("A's room-2 kills never reached B's hidden room 2", func() bool { return coM6Dead(b.coGame, 2, 1) && coM6Dead(b.coGame, 2, 8) })
	do(b, gb.g.ShowRoom(2))
	run(3)
	if !gone(b, coM6R2BK) || !gone(b, coM6R2CC) {
		t.Fatal("B's room 2, shown again, created enemies A killed")
	}
	do(a, ga.g.EnterRoom(1, 2))
	together(1)

	// 3. B in another stage: nothing until it comes back, then the
	// hub's store, and B's ChuChu spawns and shrinks (§7.2's artefact).
	do(b, gb.g.LoadStage(coSea))
	until("B never at sea", func() bool { return b.s.r.Status().Combat.Place.Stage == coSea.Name })
	run(gate.StableTicks + 2)
	kill(a, coM6CC)
	run(20)
	enterDRC(b)
	until("A's kill never reached B back in DRC", func() bool { return coM6Dead(b.coGame, 1, 3) && coM6Dead(b.coGame, 1, 1) })
	until("B's ChuChu never shrank away", func() bool { return gone(b, coM6CC) })
	together(1)

	// 4. Both leave: the stores go, and the enemies come back.
	for _, p := range players {
		do(p, p.g.LoadStage(coSea))
	}
	until("the hub kept kill stores with the stage empty", func() bool {
		hs, err := h.cs.hub.Status()
		return err == nil && hs.KillStores == 0 && a.s.r.Status().Combat.Place.Stage == coSea.Name && b.s.r.Status().Combat.Place.Stage == coSea.Name
	})
	for _, p := range players {
		enterDRC(p)
	}
	together(1)
	run(10)
	for _, p := range players {
		if gone(p, coM6BK) || gone(p, coM6CC) || coM6Dead(p.coGame, 1, 1) {
			t.Fatalf("%s's dead enemies stayed dead with the stage emptied in between", p.name)
		}
	}

	// 5-6. An unkeyed kill, a BOSS Bokoblin's and a Kargaroc's own death
	// travel nowhere.
	before := kills.count()
	kill(a, coM6Unkeyed)
	kill(a, coM6Boss)
	kill(a, coM6BB)
	run(30)
	if gone(b, coM6Unkeyed) || gone(b, coM6Boss) || gone(b, coM6BB) || coM6Dead(b.coGame, 1, 6) || coM6Dead(b.coGame, 1, 7) {
		t.Fatal("an unkeyed, BOSS or Kargaroc death reached B")
	}
	if n := kills.count() - before; n != 0 {
		t.Errorf("%d SCOPED KILL for deaths that do not travel", n)
	}
	// A's Kargaroc on ice or by a Light Arrow records its death
	// (enemy_ice, c_damagereaction.cpp:159): A's own shrink.
	enterDRC(a)
	together(1)
	run(5)
	bb, _ := live(a, coM6BB)
	bbc, _ := combat.K1(int16(stage.ProcBB))
	must(ga.m.Store(bb.Addr+bbc.Ice+addr.EnemyIceOffTimer, []byte{1})) // the game's own write: a Light Arrow hit
	until("A's Kargaroc light-arrowed never reached B", func() bool { return coM6Dead(b.coGame, 1, 7) && gone(b, coM6BB) })

	// 7-8. The pause menu holds B's shrink; a ReDead waiting on its
	// switch is held until it moves.
	gb.g.SetPauseMenu(true)
	rd, _ := live(b, coM6RD)
	must(gb.m.Store(rd.Addr+0x2AC, []byte{0, 0, 0, 0xA})) // MODE_SW_WAIT (d_a_rd.h:34)
	kill(a, coM6RD)
	run(10)
	if gone(b, coM6RD) || gb.read(rd.Addr+0x6E0+addr.EnemyIceOffTimer, 1)[0] != 0 {
		t.Fatal("B's ReDead was shrunk with the pause menu up")
	}
	gb.g.SetPauseMenu(false)
	run(10)
	if gb.read(rd.Addr+0x6E0+addr.EnemyIceOffTimer, 1)[0] != 0 {
		t.Fatal("B's ReDead was shrunk while waiting on its switch")
	}
	if st := b.s.r.Status().Combat; !strings.Contains(st.LastHeld, "MODE_SW_WAIT") {
		t.Errorf("B's held reason: %+v", st)
	}
	must(gb.m.Store(rd.Addr+0x2AC, []byte{0, 0, 0, 4}))
	until("B's ReDead never shrank once it moved", func() bool { return gone(b, coM6RD) })
	// B's own Floormaster's death is never targeted: no store names it.
	kill(b, coM6FM)
	run(5)

	// 10. The Darknut: recorded, never shrunk.
	kill(a, coM6TN)
	until("A's Darknut never reached B's zone", func() bool { return coM6Dead(b.coGame, 1, 9) })
	run(40)
	if gone(b, coM6TN) {
		t.Fatal("B's unverified Darknut was light-shrunk")
	}

	// Random play, with drops and a host restart.
	rng := rand.New(rand.NewPCG(2026, 1016))
	pend := map[*coPlayer]int{}
	busy := map[*coPlayer]int{}
	keep := fakegame.Stage{Name: coDRC.Name, Point: -1, Room: 1, Table: addr.TableDRC, KeyHUD: true} // keeps the zones (d_stage.cpp:124-126)
	stages := []fakegame.Stage{coDRCKeys, coDRCKeys, keep, coDRCBoss, coSea}
	play := func(p *coPlayer) {
		t.Helper()
		if pend[p] > 0 {
			return
		}
		at, err := coPlaceOf(p.m)
		must(err)
		inDRC := at.stage == coDRC.Name && !at.pending
		switch k := rng.IntN(20); {
		case k < 6 && inDRC:
			var alive []fakegame.LiveEnemy
			for _, l := range es[p].Live() {
				if l.Placed >= 0 && l.InitState == 2 {
					alive = append(alive, l)
				}
			}
			if len(alive) > 0 {
				do(p, es[p].Kill(p.g, alive[rng.IntN(len(alive))].PID))
			}
		case k < 9 && inDRC:
			r := 1 + rng.IntN(2)
			do(p, p.g.EnterRoom(r, 3-r))
		case k < 11 && inDRC:
			r := 1 + rng.IntN(2)
			if r != p.g.StayNo() {
				if rng.IntN(2) == 0 {
					do(p, p.g.HideRoom(r))
				} else {
					do(p, p.g.ShowRoom(r))
				}
			}
		case k < 12:
			pend[p] = 4
			do(p, p.g.SetNextStage(stages[rng.IntN(len(stages))]))
		case k < 13 && !inDRC:
			pend[p] = 4
			do(p, p.g.SetNextStage(coDRCKeys))
		case k < 14:
			if busy[p] == 0 {
				busy[p] = 3 + rng.IntN(8)
				if rng.IntN(2) == 0 {
					p.g.SetEvent(1, 0)
				} else {
					p.g.SetPauseMenu(true)
				}
			}
		}
	}
	tickDown := func(p *coPlayer) {
		if pend[p] > 0 {
			if pend[p]--; pend[p] == 0 {
				do(p, p.g.ChangeStage())
				if p.g.StayNo() >= 0 {
					do(p, p.g.EnterRoom(p.g.StayNo(), 3-max(1, min(2, p.g.StayNo()))))
				}
			}
		}
		if busy[p] > 0 {
			if busy[p]--; busy[p] == 0 {
				p.g.SetEvent(0, 0)
				p.g.SetPauseMenu(false)
			}
		}
	}
	drop := func(ps ...*coPlayer) {
		for _, p := range ps {
			need[p] = welcomes(p) + 1
		}
	}
	const steps = 1000
	for i := range steps {
		for _, p := range players {
			if rng.IntN(2) == 0 {
				play(p)
			}
			tickDown(p)
		}
		stepAll()
		switch i {
		case steps / 4:
			drop(a)
			a.s.client.Disconnect()
		case steps / 2:
			drop(a, b)
			h.restart()
		case 3 * steps / 4:
			drop(b)
			b.s.client.Disconnect()
		}
		time.Sleep(time.Millisecond)
	}
	for _, p := range players {
		for pend[p] > 0 || busy[p] > 0 {
			tickDown(p)
			stepAll()
		}
	}

	// Quiesce: both leave DRC, so its stores go, and come back afresh;
	// each kills enemies of its own; both games end with the union.
	waitArmed("after the play")
	for _, p := range players {
		do(p, p.g.LoadStage(coSea))
	}
	until("the hub kept kill stores with DRC empty", func() bool {
		hs, err := h.cs.hub.Status()
		return err == nil && hs.KillStores == 0
	})
	for _, p := range players {
		enterDRC(p)
	}
	together(1)
	run(5)
	victims := map[*coPlayer][]int{a: {coM6BK, coM6CC}, b: {coM6BK2, coM6FM}}
	for _, p := range players {
		for _, i := range victims[p] {
			kill(p, i)
		}
	}
	until("the kills never converged", func() bool {
		for _, p := range players {
			for _, q := range players {
				for _, i := range victims[q] {
					if !coM6Dead(p.coGame, 1, int(coM6Enemies[i].setID)) || !gone(p, i) {
						return false
					}
				}
			}
		}
		return true
	})

	// /status: each player's line, and the host's hub.
	coType(t, joinKeys, "/status")
	until("/status said nothing of M6", func() bool { return b.rep.has("Tetra:   combat: on; ") })
	for _, s := range []string{"partner kill bit(s) held for M_NewD2 (table 3); ", " light-shrunk, ", " verify failures"} {
		if !b.rep.has(s) {
			t.Errorf("/status lacks %q:\n%s", s, b.rep)
		}
	}
	hs, err := h.cs.hub.Status()
	must(err)
	for _, l := range hubCombatStatus(hs) {
		t.Logf("host /status: %s", l)
	}
	if st := hubCombatStatus(hs); len(st) < 3 || !strings.HasPrefix(st[0], "combat allowed; ") ||
		!slices.ContainsFunc(st, func(l string) bool { return strings.Contains(l, "Tetra (bound): combat granted") }) {
		t.Errorf("the host's /status lines: %q", st)
	}

	// The audits.
	for _, p := range players {
		st := p.s.r.Status()
		if st.WritesOff != "" || st.Writes.Failed != 0 {
			t.Errorf("%s: writes off %q, %+v", p.name, st.WritesOff, st.Writes)
		}
		n := coAuditM3(t, p)
		coM6Judge(t, p, other[p], es[p], seen[other[p]], n)
		if welcomes(p) < 2 || !p.rep.has("armed offline") {
			t.Errorf("%s: %d welcomes, offline %v: the reconnects did not happen", p.name, welcomes(p), p.rep.has("armed offline"))
		}
		t.Logf("%s: %d dead-actor bits written, %d heap writes; %+v", p.name, len(n.kills), len(n.heap), st.Combat)
	}
	for _, ps := range hs.Peers {
		if ps.ScopedRefused != 0 {
			t.Errorf("the hub refused %d scoped bits of %s, whose sender proposes synced ones only", ps.ScopedRefused, ps.Name)
		}
	}
	kills.mu.Lock()
	for _, s := range kills.bad {
		t.Error(s)
	}
	if kills.n == 0 {
		t.Error("the tap saw no SCOPED KILL")
	}
	kills.mu.Unlock()
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, s := range audit.bad {
		t.Error(s)
	}
}

func (tp *coKillTap) count() int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.n
}

// coM6Judge checks p's M6 writes against what its partner q's game
// recorded (seenQ): each dead-actor bit p wrote is a death q's game had
// in that scope; each light-shrink is of a live enemy of p's game whose
// key q's game had recorded dead, never a BOSS one, never twice; each
// weapon let go follows its carrier's timer on the same tick.
func coM6Judge(t *testing.T, p, q *coPlayer, es *fakegame.Enemies, seenQ coM6Seen, n coAuditCount) {
	t.Helper()
	for _, k := range n.kills {
		if !seenQ[k] {
			t.Errorf("%s: %s written, which %s's game never recorded", p.name, k, q.name)
		}
	}
	shrunk := map[uint32]bool{}
	timerTick := map[int]bool{}
	for _, w := range n.heap {
		off := (w.a - fakegame.EnemyHeap) % coBlock
		if off == addr.ActorOffCarryByte {
			if !timerTick[w.tick] {
				t.Errorf("%s: a weapon let go on tick %d, with no timer before it", p.name, w.tick)
			}
			continue
		}
		e, _, ok := es.Born(w.pid)
		switch {
		case !ok:
			t.Errorf("%s: a timer of pid %d, no placed enemy", p.name, w.pid)
			continue
		case e.Boss:
			t.Errorf("%s: a BOSS enemy light-shrunk", p.name)
		case shrunk[w.pid]:
			t.Errorf("%s: pid %d light-shrunk twice", p.name, w.pid)
		case !seenQ[coKillAt{stage: e.Stage, table: addr.TableDRC, room: int8(e.Room), setID: int(e.SetID)}]:
			t.Errorf("%s: setID %d of room %d light-shrunk, which %s's game never recorded", p.name, e.SetID, e.Room, q.name)
		}
		shrunk[w.pid] = true
		timerTick[w.tick] = true
	}
	if len(n.kills) == 0 || len(shrunk) == 0 {
		t.Errorf("%s wrote %d dead-actor bits and %d light-shrinks: the test did not exercise M6", p.name, len(n.kills), len(shrunk))
	}
}

// TestLogCoopCombat: the startup lines say what this side's tables sync,
// what stays each player's own, and what waits for the live checks.
func TestLogCoopCombat(t *testing.T) {
	useTablesDir(t, t.TempDir())
	st, err := loadCoopStageTables(writeImage(t, "ww.iso", tablestest.ISO(coM6Stages())))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		t    *tables.Tables
		demo bool
		want []string
	}{
		{"M6 fixture, demo unverified", st.t, false, []string{
			"co-op: combat this session: a partner's kills of keyed enemies (9 in 1 stage(s), K1 classes, no bosses) are recorded in this game and light-shrink the live enemy while both are in the same stage; 2 unkeyed enemies (the sea, Savage Labyrinth, KATA_RM, caves, Hyrule and the rest) are not shared yet: each player fights their own.",
			"co-op: combat: every light-shrink waits for the live check of Link's demo offset, so for now a partner's kill is only recorded (docs/08 §8.2 K1).",
		}},
		{"M6 fixture, demo verified", st.t, true, nil},
		{"no tables", nil, false, []string{"co-op: combat is off this session: without co-op stage tables no enemy is known to sync, so no partner's kill is written."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := combat.DemoTypeVerified
			combat.DemoTypeVerified = c.demo
			defer func() { combat.DemoTypeVerified = old }()
			l := &coLog{}
			logCoopCombat(l, c.t)
			for _, w := range c.want {
				if !l.has(w) {
					t.Errorf("want %q in:\n%s", w, l)
				}
			}
			if c.demo && l.has("Link's demo") {
				t.Errorf("the demo line with the offset verified:\n%s", l)
			}
		})
	}
	var none *coopStageTables
	if none.killVerdict() != nil || (&coopStageTables{}).killVerdict() != nil {
		t.Error("a verdict without tables: the hub would keep kills")
	}
	if v := st.killVerdict(); v == nil || !v(addr.TableDRC, coDRC.Name, 1, 3) || v(addr.TableDRC, coDRC.Name, 1, 6) {
		t.Error("the session's kill verdict is not the tables'")
	}
}

// TestCombatStatus: /status's M6 line for each way combat stands.
func TestCombatStatus(t *testing.T) {
	in := world.Place{Stage: coDRC.Name, Table: addr.TableDRC, Room: 1}
	on := replica.CombatStatus{Granted: true, Tables: true, Place: in, StoreBits: 4, ZoneSet: 3, Shrunk: 2, Held: 1, DemoHeld: 1, LastHeld: "link demo 13", Refused: 1}
	for _, c := range []struct {
		name string
		st   replica.Status
		want string
	}{
		{"on", replica.Status{Combat: on}, "combat: on; 4 partner kill bit(s) held for M_NewD2 (table 3); 3 written to zones, 2 light-shrunk, 1 held (link demo 13, link demo 1), 1 refused, 0 verify failures"},
		{"nothing held", replica.Status{Combat: replica.CombatStatus{Granted: true, Tables: true, Place: world.Nowhere}},
			"combat: on; 0 partner kill bit(s) held for nowhere; 0 written to zones, 0 light-shrunk, 0 held, 0 refused, 0 verify failures"},
		{"not granted", replica.Status{Combat: replica.CombatStatus{Tables: true}}, "combat: not granted by the host (its co-op stage tables are not this side's, or it turned it off)"},
		{"no tables", replica.Status{Combat: replica.CombatStatus{Granted: true}}, "combat: granted, but this side has no co-op enemy table, so no kill is written"},
		{"dry run", replica.Status{DryRun: true}, "combat: none in a dry run, which asks the host for the shared image alone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := combatStatus(c.st); got != c.want {
				t.Errorf("got\n\t%s\nwant\n\t%s", got, c.want)
			}
		})
	}
}

// TestHubCombatStatus: a host's /status adds its hub's combat lines.
func TestHubCombatStatus(t *testing.T) {
	hs := hub.Status{
		Features: hub.DefaultFeatures, CombatAllowed: true, KillStores: 2,
		Peers: []hub.PeerStatus{
			{Name: "Link", State: hub.StateBound, Features: hub.DefaultFeatures},
			{Name: "Tetra", State: hub.StateBound, Features: hub.DefaultFeatures &^ hub.TableFeatures},
		},
	}
	want := []string{
		"combat allowed; 2 kill store(s) held",
		"  Link (bound): combat granted",
		"  Tetra (bound): combat not granted",
	}
	if got := hubCombatStatus(hs); !slices.Equal(got, want) {
		t.Errorf("got\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
	hs.CombatAllowed = false
	if got := hubCombatStatus(hs)[0]; got != "combat off (the host has no co-op enemy table); 2 kill store(s) held" {
		t.Errorf("without a verdict: %q", got)
	}
	hs.Features &^= hub.FeatureCombat
	if got := hubCombatStatus(hs)[0]; got != "combat off; 2 kill store(s) held" {
		t.Errorf("turned off: %q", got)
	}
	l := &coLog{}
	logHubStatus(l, func() (hub.Status, error) { return hs, nil })
	if !l.has("co-op: host: combat off; 2 kill store(s) held") {
		t.Errorf("logHubStatus:\n%s", l)
	}
}
