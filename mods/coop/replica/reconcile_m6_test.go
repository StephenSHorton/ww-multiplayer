package replica

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// The M6 fixture's enemies, in Dragon Roost Cavern's room 1 and one in
// room 2, as the disc places them and as the fake game creates them (the
// same index in both): keyed Bokoblins (the first holding its weapon), a
// ChuChu, a ReDead and a Floormaster, a Bokoblin of type 0xA (BOSS on
// creation, d_a_bk.cpp:5030-5034), a Kargaroc, an unkeyed Bokoblin, a
// Darknut (a class not yet verified), and a Bokoblin of room 2 with the
// setID of room 1's first.
const (
	m6BK, m6BK2, m6CC, m6RD, m6FM, m6Boss, m6BB, m6Unkeyed, m6TN, m6Room2 = 0, 1, 2, 3, 4, 5, 6, 7, 8, 9
)

var m6Placed = []struct {
	name  string
	proc  stage.Proc
	room  int
	setID uint16
	boss  bool
}{
	{"Bk", stage.ProcBK, 1, 1, false},
	{"Bk", stage.ProcBK, 1, 2, false},
	{"c_green", stage.ProcCC, 1, 3, false},
	{"Rdead1", stage.ProcRD, 1, 4, false},
	{"Fmaster", stage.ProcFM, 1, 5, false},
	{"Bk", stage.ProcBK, 1, 6, true},
	{"Bb", stage.ProcBB, 1, 7, false},
	{"Bk", stage.ProcBK, 1, 0xFFFF, false},
	{"Tn", stage.ProcTN, 1, 9, false},
	{"Bk", stage.ProcBK, 2, 1, false},
}

func m6Stages() []stagetest.Stage {
	var room1, room2 []stagetest.Actor
	for _, p := range m6Placed {
		a := tablestest.Enemy(p.name, p.setID)
		if p.boss {
			a.Params = 0xA
		}
		if p.room == 1 {
			room1 = append(room1, a)
		} else {
			room2 = append(room2, a)
		}
	}
	stages := tablestest.Vanilla()
	for i, s := range stages {
		if s.Name == drc.Name {
			stages[i] = tablestest.Dungeon(drc.Name, keys.TableDRC, room1...)
			stages[i].Rooms = append(stages[i].Rooms, tablestest.Room(2, room2...))
		}
	}
	return stages
}

var m6Fixture = sync.OnceValues(func() (*tables.Tables, error) {
	img := tablestest.ISO(m6Stages())
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

// m6Tables are the M6 fixture's tables, checked to sync what the tests
// rest on.
func m6Tables(t testing.TB) *tables.Tables {
	t.Helper()
	tb, err := m6Fixture()
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range m6Placed {
		want := p.setID != 0xFFFF && !p.boss
		if got := synctables.SyncedKill(tb, keys.TableDRC, drc.Name, p.room, int(p.setID)); got != want {
			t.Fatalf("the fixture's verdict on enemy %d (%s setID %d) is %v, want %v", i, p.name, p.setID, got, want)
		}
	}
	return tb
}

// m6Enemies are the fixture's enemies as the fake game creates them.
func m6Enemies() *fakegame.Enemies {
	var es []fakegame.Enemy
	for i, p := range m6Placed {
		es = append(es, fakegame.Enemy{Stage: drc.Name, Room: p.room, SetID: p.setID, Proc: p.proc, Boss: p.boss, Weapon: i == m6BK})
	}
	return fakegame.PlaceEnemies(es...)
}

// unverify makes K1 class p unverified for one test: every K1 class is
// verified since the live check K1 (docs/08 §8.2).
func unverify(t testing.TB, p stage.Proc) {
	old := combat.SetVerified(int16(p), false)
	t.Cleanup(func() { combat.SetVerified(int16(p), old) })
}

// demoVerified sets combat.DemoTypeVerified for one test.
func demoVerified(t testing.TB, on bool) {
	old := combat.DemoTypeVerified
	combat.DemoTypeVerified = on
	t.Cleanup(func() { combat.DemoTypeVerified = old })
}

// m6Rig is a reconciling replica in DRC's room 1 with room 2 loaded beside
// it, welcomed with the image and combat, its enemies created and
// executing.
type m6Rig struct {
	*recRig
	zm *zoneMem // the zone table as each write was made
	es *fakegame.Enemies
	tb *tables.Tables
}

func newM6Rig(t *testing.T) *m6Rig { t.Helper(); return newM6RigWith(t, nil) }

// newM6RigWith is newM6Rig with the replica's claim func (Config.Claimed)
// claimed.
func newM6RigWith(t *testing.T, claimed combat.ClaimFunc) *m6Rig {
	t.Helper()
	demoVerified(t, true)
	tb := m6Tables(t)
	r6 := &replicaRig{rig: newRig(t, "Link"), hub: &hubEnd{t: t}, log: &logBuf{}}
	bm := &beatMem{FakeMem: r6.m, game: r6.g}
	zm := &zoneMem{beatMem: bm}
	r, err := New(Config{Mem: zm, Logf: r6.log.logf, Apply: true, Tables: tb, Policy: switch5Policy(t), BeatTimeout: time.Second, Claimed: claimed})
	if err != nil {
		t.Fatal(err)
	}
	r6.r = r
	rec := &recRig{replicaRig: r6, bm: bm, seq: 1, frames: 3}
	r6.tag(r6.tagFor(testWorld, testNonce))
	r.Connect(r6.hub)
	for range gate.StableTicks + 2 {
		rec.tick()
	}
	rec.welcome(proto.FeatureImage | proto.FeatureCombat)
	say(t, r, worldOf(1, save.Image{}))
	rec.tick()
	r6.hub.take()
	es := m6Enemies()
	bm.setActors(es)
	rec.load(drcKeys)
	rec.enter(1, 2)
	rr := &m6Rig{recRig: rec, zm: zm, es: es, tb: tb}
	if n := rr.ticks(gate.StableTicks + 4); n != 0 {
		t.Fatalf("%d bytes written before any kill came", n)
	}
	rec.hub.take()
	return rr
}

// killMsg is a SCOPED KILL of room room of stage s with setIDs ids.
func killMsg(t testing.TB, s fakegame.Stage, room int, ids ...int) *wire.Scoped {
	t.Helper()
	st, err := wire.MakeStage(s.Name)
	if err != nil {
		t.Fatal(err)
	}
	var words [16]uint32
	for _, id := range ids {
		words[id/32] |= 1 << (id % 32)
	}
	m := &wire.Scoped{Kind: wire.ScopeKill, Stage: st, Table: int8(s.Table), Room: int8(room)}
	for w, b := range words {
		if b != 0 {
			m.Words = append(m.Words, wire.ScopedWord{Word: uint8(w), Bits: b})
		}
	}
	return m
}

func (rr *m6Rig) kill(room int, ids ...int) {
	rr.t.Helper()
	say(rr.t, rr.r, killMsg(rr.t, drc, room, ids...))
}

// dead reports whether setID id is recorded dead in room's zone.
func (rr *m6Rig) dead(room, id int) bool {
	rr.t.Helper()
	z, err := rr.g.ZoneOf(room)
	if err != nil || z < 0 {
		return false
	}
	za, _ := addr.ZoneAddr(z)
	bit, _ := addr.ZoneActorBit(id)
	return rr.read(za+uint32(bit.Off), 1)[0]&bit.Mask != 0
}

// live returns enemy i's actor, if it is in the queue.
func (rr *m6Rig) live(i int) (fakegame.LiveEnemy, bool) { return rr.es.Of(i) }

// timer is enemy i's light-shrink timer, -1 when it is gone.
func (rr *m6Rig) timer(i int) int {
	rr.t.Helper()
	l, ok := rr.live(i)
	if !ok {
		return -1
	}
	k, _ := combat.K1(int16(l.Proc))
	return int(int8(rr.read(l.Addr+k.Ice+addr.EnemyIceOffTimer, 1)[0]))
}

// m6Writes splits the replica's writes into dead-actor bytes and the rest
// on the actor heap.
func (rr *m6Rig) m6Writes() (kills, heap []auditWrite) {
	g := NewGuardFor(AllTargets)
	for _, w := range rr.bm.written() {
		switch {
		case g.TargetOf(w.a) == TargetKills:
			kills = append(kills, w)
		case w.a >= fakegame.EnemyHeap && w.a < fakegame.EnemyHeap+0x1000000:
			heap = append(heap, w)
		}
	}
	return kills, heap
}

// TestReconcileM6ZoneOR: a partner's kills are ORed into the zones of the
// rooms loaded here, a hidden one too, only for keys the tables sync, and
// a room being created or deleted is left.
func TestReconcileM6ZoneOR(t *testing.T) {
	rr := newM6Rig(t)
	demoVerified(t, false) // zone writes do not wait for Link's demo gate
	if err := rr.g.HideRoom(2); err != nil {
		t.Fatal(err)
	}
	rr.ticks(2)
	rr.kill(1, 3, 6, 9) // the ChuChu, the BOSS Bokoblin (refused), the Darknut
	rr.kill(2, 1)       // room 2's Bokoblin, its room hidden
	rr.kill(5, 1)       // a room not loaded here
	rr.ticks(4)
	for _, c := range []struct {
		room, id int
		want     bool
	}{{1, 3, true}, {1, 9, true}, {1, 6, false}, {2, 1, true}, {1, 1, false}} {
		if got := rr.dead(c.room, c.id); got != c.want {
			t.Errorf("room %d setID %d recorded dead %v, want %v", c.room, c.id, got, c.want)
		}
	}
	kills, heap := rr.m6Writes()
	if len(kills) != 3 || len(heap) != 0 {
		t.Fatalf("%d dead-actor bytes and %d heap writes, want 3 and none (the demo gate shut)", len(kills), len(heap))
	}
	for _, w := range kills {
		if w.n != 1 || w.old&^w.val != 0 {
			t.Errorf("a write of %d bytes, 0x%02X over 0x%02X", w.n, w.val, w.old)
		}
	}
	// Shown again, the hidden room's Bokoblin is not created.
	if err := rr.g.ShowRoom(2); err != nil {
		t.Fatal(err)
	}
	rr.ticks(2)
	if _, ok := rr.live(m6Room2); ok {
		t.Error("room 2's Bokoblin was created though its death was written")
	}
	// A room being created waits.
	rs, _ := addr.RoomStatusAddr(1)
	flags := rr.read(rs+addr.RoomStatusOffFlags, 1)[0]
	rr.store(rs+addr.RoomStatusOffFlags, []byte{flags | addr.RoomFlagCreating})
	rr.kill(1, 2)
	if rr.ticks(4); rr.dead(1, 2) {
		t.Error("a kill was written into a room being created")
	}
	rr.store(rs+addr.RoomStatusOffFlags, []byte{flags})
	if rr.ticks(4); !rr.dead(1, 2) {
		t.Error("the kill was not written once the room settled")
	}
	if st := rr.r.Status().Combat; st.ZoneSet != 4 || st.Refused != 2 || st.StoreBits != 6 { // refused: the BOSS Bokoblin, and room 5's setID 1, which no K1 entry has
		t.Errorf("status %+v", st)
	}
}

// TestReconcileM6Shrink: a partner's kill of an enemy alive here
// light-shrinks it, one byte, and the shrink runs the game's own death;
// a carrier's weapon is let go after its timer.
func TestReconcileM6Shrink(t *testing.T) {
	rr := newM6Rig(t)
	cc, _ := rr.live(m6CC)
	bk, _ := rr.live(m6BK)
	wp, _ := rr.es.WeaponOf(m6BK)
	rr.kill(1, 3, 1)
	rr.ticks(3)
	if rr.timer(m6CC) <= 0 || rr.timer(m6BK) <= 0 {
		t.Fatalf("timers %d, %d after the kill", rr.timer(m6CC), rr.timer(m6BK))
	}
	if st := rr.read(wp.Addr+addr.ActorOffCarryByte, 1)[0]; st&addr.ActorCarryByteMask != 0 {
		t.Error("the Bokoblin's weapon is still carried")
	}
	_, heap := rr.m6Writes()
	var timers, weapons int
	for _, w := range heap {
		switch w.a {
		case cc.Addr + 0x81C + addr.EnemyIceOffTimer, bk.Addr + 0x1264 + addr.EnemyIceOffTimer:
			timers++
			if w.n != 1 || w.old != 0 || w.val != 1 {
				t.Errorf("a timer write of %d bytes, %d over %d", w.n, w.val, w.old)
			}
		case wp.Addr + addr.ActorOffCarryByte:
			weapons++
		default:
			t.Errorf("a heap write at 0x%08X", w.a)
		}
	}
	if timers != 2 || weapons != 1 {
		t.Fatalf("%d timer and %d weapon writes", timers, weapons)
	}
	rr.ticks(35)
	if _, ok := rr.live(m6CC); ok {
		t.Fatal("the ChuChu outlived its shrink")
	}
	if got := rr.es.Shrunk(); len(got) != 2 {
		t.Errorf("shrunk %v", got)
	}
	st := rr.r.Status()
	if st.Combat.Shrunk != 2 || st.Combat.Released != 1 || st.WritesOff != "" || st.Writes.Failed != 0 {
		t.Errorf("status %+v, writes %+v", st.Combat, st.Writes)
	}
	if !rr.log.has("light-shrunk the partner's kill: actor") || !rr.log.has("its weapon let go") {
		t.Errorf("the log:\n%s", rr.log)
	}
	if err := rr.bm.actorErr(); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileM6Gates: each gate holds the light-shrink on its own, and
// lets it through once it opens.
func TestReconcileM6Gates(t *testing.T) {
	for _, c := range []struct {
		name  string
		enemy int
		id    int
		close func(rr *m6Rig)
		open  func(rr *m6Rig)
	}{
		{"pause menu", m6CC, 3, func(rr *m6Rig) { rr.g.SetPauseMenu(true) }, func(rr *m6Rig) { rr.g.SetPauseMenu(false) }},
		{"event", m6CC, 3, func(rr *m6Rig) { rr.g.SetEvent(1, 0) }, func(rr *m6Rig) { rr.g.SetEvent(0, 0) }},
		{"link demo", m6CC, 3, func(rr *m6Rig) { rr.g.SetLinkDemo(13) }, func(rr *m6Rig) { rr.g.SetLinkDemo(0) }},
		{"ReDead waiting on its switch", m6RD, 4, func(rr *m6Rig) { rr.setMode(m6RD, 0xA) }, func(rr *m6Rig) { rr.setMode(m6RD, 4) }},
		{"Floormaster grab", m6FM, 5, func(rr *m6Rig) { rr.setMode(m6FM, 13) }, func(rr *m6Rig) { rr.setMode(m6FM, 7) }},
		{"an event ordered it", m6CC, 3, func(rr *m6Rig) { rr.setU16(m6CC, addr.ActorOffEventCommand, 1) }, func(rr *m6Rig) { rr.setU16(m6CC, addr.ActorOffEventCommand, 0) }},
		{"Link carries it", m6CC, 3, func(rr *m6Rig) { rr.setStatus(m6CC, addr.ActorStatusCarry) }, func(rr *m6Rig) { rr.setStatus(m6CC, 0) }},
		{"frozen", m6CC, 3, func(rr *m6Rig) { rr.setIceMode(m6CC, 2) }, func(rr *m6Rig) { rr.setIceMode(m6CC, 1) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM6Rig(t)
			c.close(rr)
			rr.kill(1, c.id)
			rr.ticks(5)
			if rr.timer(c.enemy) != 0 {
				t.Fatalf("shrunk with the gate closed: timer %d", rr.timer(c.enemy))
			}
			if st := rr.r.Status().Combat; st.Held == 0 && c.name != "pause menu" && c.name != "event" {
				t.Errorf("nothing held: %+v", st)
			}
			c.open(rr)
			rr.ticks(5)
			if rr.timer(c.enemy) <= 0 {
				t.Fatalf("not shrunk once the gate opened: timer %d\n%s", rr.timer(c.enemy), rr.log)
			}
		})
	}
}

// TestReconcileM6MirrorPaused: a copy the enemy mirror adopted (its REL
// sets addr.ActorPauseMirrorBit) is held as "paused: mirror", never
// light-shrunk, until the REL hands it back (enemy-mirror-general.json
// D8).
func TestReconcileM6MirrorPaused(t *testing.T) {
	rr := newM6Rig(t)
	cc, _ := rr.live(m6CC)
	rr.store(cc.Addr+addr.ActorOffPauseFlag, []byte{addr.ActorPauseMirrorBit})
	rr.kill(1, 3)
	rr.ticks(5)
	if rr.timer(m6CC) != 0 {
		t.Fatalf("a mirror copy shrunk: timer %d", rr.timer(m6CC))
	}
	if st := rr.r.Status().Combat; st.Held == 0 || !strings.Contains(st.LastHeld, "paused: mirror") {
		t.Errorf("not held as a mirror copy: %+v", st)
	}
	rr.store(cc.Addr+addr.ActorOffPauseFlag, []byte{0})
	rr.ticks(5)
	if rr.timer(m6CC) <= 0 {
		t.Fatalf("not shrunk once handed back: timer %d\n%s", rr.timer(m6CC), rr.log)
	}
}

// TestClaimPerHandle: two games, each with its own enemy client's claim
// set (enemysync.ClaimSet, one per game handle as the session wiring
// makes them), do not see each other's pids. The fake games create the
// same enemies with the same process IDs, so game A's claim of its
// ChuChu's pid would hold game B's ChuChu too were the set shared: it
// holds A's alone, and B's shrinks. A claim made after the walk is
// caught too: the tick's plan (CombatTick.Claimed) gives it to
// combat.Revalidate's re-read before the write.
func TestClaimPerHandle(t *testing.T) {
	csA, csB := enemysync.NewClaimSet(), enemysync.NewClaimSet()
	a := newM6RigWith(t, csA.Claimed)
	b := newM6RigWith(t, csB.Claimed)
	ccA, _ := a.live(m6CC)
	ccB, _ := b.live(m6CC)
	if ccA.PID != ccB.PID {
		t.Fatalf("the fake games' ChuChus are pids %d and %d; the test needs them equal", ccA.PID, ccB.PID)
	}
	csA.Replace([]uint32{ccA.PID})
	a.kill(1, 3)
	b.kill(1, 3)
	a.ticks(5)
	b.ticks(5)
	if a.timer(m6CC) != 0 {
		t.Errorf("game A shrunk its claimed ChuChu: timer %d", a.timer(m6CC))
	}
	if st := a.r.Status().Combat; !strings.Contains(st.LastHeld, "claimed") {
		t.Errorf("game A did not hold it as claimed: %+v", st)
	}
	if b.timer(m6CC) <= 0 {
		t.Errorf("game B's ChuChu was held by game A's claim: timer %d\n%s", b.timer(m6CC), b.log)
	}

	// Unclaimed, A's shrinks.
	csA.Replace(nil)
	a.ticks(5)
	if a.timer(m6CC) <= 0 {
		t.Fatalf("game A's ChuChu not shrunk once unclaimed: timer %d\n%s", a.timer(m6CC), a.log)
	}
}

func (rr *m6Rig) setMode(i int, mode int32) {
	rr.t.Helper()
	l, _ := rr.live(i)
	rr.store(l.Addr+0x2AC, []byte{byte(mode >> 24), byte(mode >> 16), byte(mode >> 8), byte(mode)})
}

func (rr *m6Rig) setU16(i int, off uint32, v uint16) {
	rr.t.Helper()
	l, _ := rr.live(i)
	rr.store(l.Addr+off, []byte{byte(v >> 8), byte(v)})
}

func (rr *m6Rig) setStatus(i int, v uint32) {
	rr.t.Helper()
	l, _ := rr.live(i)
	rr.store(l.Addr+addr.ActorOffStatus, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func (rr *m6Rig) setIceMode(i int, mode int8) {
	rr.t.Helper()
	l, _ := rr.live(i)
	k, _ := combat.K1(int16(l.Proc))
	rr.store(l.Addr+k.Ice+addr.EnemyIceOffMode, []byte{byte(mode)})
}

// TestReconcileM6Revalidate: the target freed and its block reused by
// another actor between the tick and the write is not written; nor is one
// freed and not reused.
func TestReconcileM6Revalidate(t *testing.T) {
	rr := newM6Rig(t)
	cc, _ := rr.live(m6CC)
	reused := false
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 && !reused { // the writer's first beat read
			reused = true
			if _, err := rr.es.FreeAndReuse(rr.g, cc.PID, stage.ProcCC); err != nil {
				t.Error(err)
			}
		}
	})
	rr.kill(1, 3)
	rr.ticks(3)
	rr.bm.onHeartbeat(nil)
	if !reused {
		t.Fatal("the hook never ran")
	}
	_, heap := rr.m6Writes()
	for _, w := range heap {
		if w.a == cc.Addr+0x81C+addr.EnemyIceOffTimer {
			t.Fatal("the reused block's timer was written")
		}
	}
	if st := rr.r.Status().Combat; st.Held == 0 || !strings.Contains(st.LastHeld, "reused") && !strings.Contains(st.LastHeld, "changed") {
		t.Errorf("status %+v", st)
	}
}

// TestReconcileM6ZoneGoneAfterBeat: the target's room zone removed between
// the plan and the write: no write, and no assert (the shrink's onActor
// would halt the game).
func TestReconcileM6ZoneGoneAfterBeat(t *testing.T) {
	rr := newM6Rig(t)
	zn, _ := addr.ZoneNoAddr(1)
	z := rr.read(zn, 1)
	fired := false
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 && !fired {
			fired = true
			rr.store(zn, []byte{0xFF})
		}
	})
	rr.kill(1, 3)
	rr.tick()
	rr.bm.onHeartbeat(nil)
	_, heap := rr.m6Writes()
	if len(heap) != 0 {
		t.Fatalf("%d heap writes with the zone gone", len(heap))
	}
	rr.store(zn, z)
	if err := rr.bm.actorErr(); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileM6Ever: an actor light-shrunk once is never written again,
// though its timer reads 0 again.
func TestReconcileM6Ever(t *testing.T) {
	rr := newM6Rig(t)
	rr.kill(1, 3)
	rr.ticks(3)
	l, _ := rr.live(m6CC)
	k, _ := combat.K1(int16(l.Proc))
	rr.store(l.Addr+k.Ice+addr.EnemyIceOffTimer, []byte{0}) // as if the write was lost
	rr.ticks(5)
	_, heap := rr.m6Writes()
	if len(heap) != 1 {
		t.Fatalf("%d heap writes, want the one", len(heap))
	}
}

// TestReconcileM6SaveState: a save state loaded while an enemy shrinks
// brings it back alive, with its process ID and a timer of 0 (the heap
// and the process counter go back with it): it is light-shrunk again,
// since the partner's store still has its key.
func TestReconcileM6SaveState(t *testing.T) {
	rr := newM6Rig(t)
	rr.kill(1, 3)
	rr.ticks(3)
	cc, _ := rr.live(m6CC)
	if rr.timer(m6CC) <= 0 {
		t.Fatalf("timer %d after the kill", rr.timer(m6CC))
	}
	k, _ := combat.K1(int16(cc.Proc))
	rr.store(cc.Addr+k.Ice+addr.EnemyIceOffTimer, []byte{0}) // as the state had it
	rr.store(mailbox.SpawnTrigger, []byte{0, 0, 0, 1})       // the heartbeat goes back
	rr.ticks(gate.StableTicks + 4)
	if now, ok := rr.live(m6CC); !ok || now.PID != cc.PID {
		t.Fatal("the ChuChu is not the one the state brought back")
	}
	if rr.timer(m6CC) <= 0 {
		t.Fatalf("not light-shrunk again after the save state: timer %d: %s", rr.timer(m6CC), rr.log)
	}
}

// TestReconcileM6PlanCopiesStore: the WriteGuard judges by the tick's
// plan: bits Handle ORs into the partner's store after the plan (in
// place, onScoped) do not reach the kills the guard asks, which run after
// r.mu is released. The plan holds its own copy of the store's words.
func TestReconcileM6PlanCopiesStore(t *testing.T) {
	rr := newM6Rig(t)
	rr.kill(1, 3)
	rr.tick()
	rr.kill(1, 2) // the same word of the same store, after the plan
	wg := rr.r.writer.WriteGuard()
	wg.mu.Lock()
	kills := wg.combat.Kills
	wg.mu.Unlock()
	if kills == nil || !kills(1, 3) {
		t.Fatal("the plan does not take its own kill")
	}
	if kills(1, 2) {
		t.Error("a bit that came after the plan reached the guard's verdict")
	}
}

// TestReconcileM6StoreNotLocal: this game's own recorded death targets
// nothing; only the partner's store does.
func TestReconcileM6StoreNotLocal(t *testing.T) {
	rr := newM6Rig(t)
	if err := rr.g.OnActor(4, 1); err != nil { // the ReDead's own death, recording
		t.Fatal(err)
	}
	rr.ticks(5)
	if rr.timer(m6RD) != 0 {
		t.Fatal("this game's own zone bit light-shrank its ReDead")
	}
	// The partner's store names it, but it is dying (health 0): no
	// shrink either.
	rd, _ := rr.live(m6RD)
	if err := rr.es.Kill(rr.g, rd.PID); err != nil {
		t.Fatal(err)
	}
	rr.kill(1, 4)
	rr.ticks(3)
	if rr.timer(m6RD) > 0 {
		t.Fatal("a dying ReDead was light-shrunk")
	}
}

// TestReconcileM6WeaponOrder: a timer refused at the re-read leaves the
// weapon held.
func TestReconcileM6WeaponOrder(t *testing.T) {
	rr := newM6Rig(t)
	bk, _ := rr.live(m6BK)
	wp, _ := rr.es.WeaponOf(m6BK)
	fired := false
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 && !fired {
			fired = true
			rr.store(bk.Addr+addr.ActorOffHealth, []byte{0}) // it dies of its own between plan and write
		}
	})
	rr.kill(1, 1)
	rr.tick()
	rr.bm.onHeartbeat(nil)
	if rr.timer(m6BK) != 0 {
		t.Fatal("the timer was written")
	}
	if st := rr.read(wp.Addr+addr.ActorOffCarryByte, 1)[0]; st&addr.ActorCarryByteMask == 0 {
		t.Fatal("the weapon was let go though the timer was refused")
	}
}

// TestReconcileM6LinkHoldsStick: a Bokoblin knocked out of its stick
// keeps the stick's pid in m1200 (d_a_bk.cpp:100-137). Link picks the
// stick up, CARRY set; the Bokoblin's light-shrink leaves the stick in
// Link's hands.
func TestReconcileM6LinkHoldsStick(t *testing.T) {
	rr := newM6Rig(t)
	bk, _ := rr.live(m6BK)
	wp, _ := rr.es.WeaponOf(m6BK)
	if err := rr.es.DropWeapon(rr.g, bk.PID); err != nil {
		t.Fatal(err)
	}
	if err := rr.es.GrabWeapon(rr.g, bk.PID); err != nil {
		t.Fatal(err)
	}
	rr.kill(1, 1)
	rr.ticks(3)
	if rr.timer(m6BK) <= 0 {
		t.Fatalf("the Bokoblin was not light-shrunk: timer %d: %s", rr.timer(m6BK), rr.log)
	}
	_, heap := rr.m6Writes()
	for _, w := range heap {
		if w.a == wp.Addr+addr.ActorOffCarryByte {
			t.Fatal("the CARRY of the stick in Link's hands was cleared")
		}
	}
	if st := rr.read(wp.Addr+addr.ActorOffCarryByte, 1)[0]; st&addr.ActorCarryByteMask == 0 {
		t.Fatal("Link's stick lost CARRY")
	}
	if st := rr.r.Status().Combat; st.Released != 0 {
		t.Errorf("status %+v", st)
	}
}

// TestReconcileM6WeaponTakenUp: a Bokoblin planned with no stick picks
// its stick up before the write: the timer waits, so the stick is never
// left CARRY on a deleted carrier, and the next tick shrinks it and lets
// the stick go.
func TestReconcileM6WeaponTakenUp(t *testing.T) {
	rr := newM6Rig(t)
	bk, _ := rr.live(m6BK)
	wp, _ := rr.es.WeaponOf(m6BK)
	if err := rr.es.DropWeapon(rr.g, bk.PID); err != nil {
		t.Fatal(err)
	}
	fired := false
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 && !fired { // the writer's first beat read: after the plan
			fired = true
			if err := rr.es.TakeWeapon(rr.g, bk.PID); err != nil {
				t.Error(err)
			}
		}
	})
	rr.kill(1, 1)
	rr.tick()
	rr.bm.onHeartbeat(nil)
	if !fired {
		t.Fatal("the hook never ran")
	}
	if rr.timer(m6BK) != 0 {
		t.Fatal("the timer was written though it took its stick up after the plan")
	}
	if st := rr.r.Status().Combat; !strings.Contains(st.LastHeld, "took up a weapon") {
		t.Errorf("status %+v", st)
	}
	rr.ticks(3)
	if rr.timer(m6BK) <= 0 {
		t.Fatalf("not shrunk on the next tick: timer %d", rr.timer(m6BK))
	}
	if st := rr.read(wp.Addr+addr.ActorOffCarryByte, 1)[0]; st&addr.ActorCarryByteMask != 0 {
		t.Error("the stick it took up was not let go")
	}
}

// TestReconcileM6Unverified: a class not verified (the Darknut, made so
// here) gets its zone bit, never a light-shrink.
func TestReconcileM6Unverified(t *testing.T) {
	unverify(t, stage.ProcTN)
	rr := newM6Rig(t)
	rr.kill(1, 9)
	rr.ticks(5)
	if !rr.dead(1, 9) {
		t.Error("the Darknut's death was not recorded")
	}
	if rr.timer(m6TN) != 0 {
		t.Error("an unverified Darknut was light-shrunk")
	}
	if st := rr.r.Status().Combat; !strings.Contains(st.LastHeld, "not verified") {
		t.Errorf("status %+v", st)
	}
}

// TestReconcileM6DemoUnverified: until Link's demo offset is verified, no
// light-shrink is written, and /status counts the held targets.
func TestReconcileM6DemoUnverified(t *testing.T) {
	rr := newM6Rig(t)
	demoVerified(t, false)
	rr.kill(1, 3)
	rr.ticks(5)
	if rr.timer(m6CC) != 0 || !rr.dead(1, 3) {
		t.Fatalf("timer %d, recorded %v", rr.timer(m6CC), rr.dead(1, 3))
	}
	st := rr.r.Status().Combat
	if st.DemoHeld != 1 || !strings.Contains(st.LastHeld, "link demo unverified") {
		t.Errorf("status %+v", st)
	}
}

// TestReconcileM6StoreOffGame: a KILL store is dropped while Link is off
// the game, as the hub drops it when the partner is not in the stage
// (Scopes.vacate): the same stage loaded again starts with its zones
// wiped (d_stage.cpp:123-125), and nothing of the old store is written
// into them, nor is a respawned enemy light-shrunk.
func TestReconcileM6StoreOffGame(t *testing.T) {
	rr := newM6Rig(t)
	rr.kill(1, 3)
	rr.ticks(3)
	if rr.timer(m6CC) <= 0 || !rr.dead(1, 3) {
		t.Fatalf("the kill: timer %d, recorded %v", rr.timer(m6CC), rr.dead(1, 3))
	}
	rr.ticks(35)
	rr.splitLoad(drcKeys) // Continue after a game over: the same stage, its zones wiped
	rr.enter(1, 2)
	before := len(rr.bm.written())
	rr.ticks(gate.StableTicks + 8)
	if _, ok := rr.live(m6CC); !ok {
		t.Fatal("the ChuChu did not respawn on the reload")
	}
	if n := len(rr.bm.written()) - before; n != 0 || rr.timer(m6CC) != 0 || rr.dead(1, 3) {
		t.Fatalf("%d writes after the reload: timer %d, recorded %v", n, rr.timer(m6CC), rr.dead(1, 3))
	}
	if st := rr.r.Status().Combat; st.StoreBits != 0 {
		t.Errorf("the store outlived the trip off the game: %+v", st)
	}
}

// TestReconcileM6Status: what /status reads, and the grant lines.
func TestReconcileM6Status(t *testing.T) {
	rr := newM6Rig(t)
	st := rr.r.Status().Combat
	if !st.Granted || !st.Tables || st.Place != (world.Place{Stage: drc.Name, Table: addr.TableDRC, Room: 1}) {
		t.Errorf("status %+v", st)
	}
	if !rr.log.has("co-op: combat: written to this game when idle") {
		t.Errorf("the grant line:\n%s", rr.log)
	}
	// Without combat granted: nothing written, and said once.
	rec := newRecRig(t, Config{Apply: true, Tables: m6Tables(t)}, proto.FeatureImage)
	say(t, rec.r, killMsg(t, drc, 1, 3))
	rec.ticks(4)
	if rec.log.count("a partner's kills are not written to this game") != 1 {
		t.Errorf("the not-granted line:\n%s", rec.log)
	}
	if st := rec.r.Status().Combat; st.Granted || st.StoreBits != 0 {
		t.Errorf("status without the grant %+v", st)
	}
}

// FuzzReconcileM6 plays a partner's kill stores against the M6 game while
// it hides, shows and reloads rooms, changes stage, pauses, runs events,
// starts a Link demo, kills its own enemies and frees and reuses their
// blocks, knocks the Bokoblin's stick away and has Link or the Bokoblin
// pick it up, and audits every write the replica made:
//
//   - a zone byte is a dead-actor byte of the zone of a room that, as it
//     was written, was loaded and settled, setting only bits of setIDs the
//     store sent for that room and the tables sync, clearing none;
//   - a heap byte is a light-shrink timer going 0 to 1, at its class's
//     timer, in the block of a placed enemy (the block's process ID and
//     proc as the write was made, fakegame.Enemies.Born) of a verified
//     class, not BOSS, whose key the store sent and the tables sync, with
//     Link in no demo, and no process ID twice; or a weapon's CARRY byte
//     cleared, on a BOKO in ENEMY_CARRY, after its own carrier's timer in
//     the same tick;
//   - nothing is written while a gate is closed, and the fake game never
//     halts (an onActor with no zone).
func FuzzReconcileM6(f *testing.F) {
	f.Add([]byte{0, 3, 8, 4, 0, 1, 8, 9})
	f.Add([]byte{1, 1, 2, 2, 0, 4, 8, 3, 5, 1, 8, 5})
	f.Add([]byte{6, 2, 0, 3, 8, 1, 7, 0, 8, 6})
	f.Add([]byte{4, 2, 0, 2, 8, 2, 4, 1, 3, 3, 8, 8})
	f.Add([]byte{10, 0, 10, 1, 0, 1, 8, 4, 10, 2, 8, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32 {
			data = data[:32]
		}
		rr := newM6Rig(t)
		sent := map[[2]int]bool{} // (room, setID) of DRC the partner's store ever had
		next := func(i *int) byte {
			if *i >= len(data) {
				return 0
			}
			b := data[*i]
			*i++
			return b
		}
		ids := []int{1, 2, 3, 4, 5, 6, 7, 9, 0x1FF}
		for i := 0; i < len(data); {
			op, arg := next(&i), next(&i)
			room := 1 + int(arg>>4)%2
			switch op % 11 {
			case 0:
				id := ids[int(arg)%len(ids)]
				sent[[2]int{room, id}] = true
				rr.kill(room, id)
			case 1:
				rr.enter(room, 3-room)
			case 2:
				if arg&1 == 0 {
					rr.g.HideRoom(room)
				} else {
					rr.g.ShowRoom(room)
				}
			case 3:
				rr.g.SetEvent(arg&1, 0)
				rr.g.SetPauseMenu(arg&2 != 0)
			case 4:
				rr.g.SetLinkDemo(uint16(arg & 1))
			case 5:
				if l, ok := rr.es.Of(int(arg) % len(m6Placed)); ok {
					rr.es.Kill(rr.g, l.PID)
				}
			case 6:
				if l, ok := rr.es.Of(int(arg) % len(m6Placed)); ok {
					rr.es.FreeAndReuse(rr.g, l.PID, stage.ProcCC)
				}
			case 7:
				rr.splitLoad([]fakegame.Stage{drcKeys, drcBoss}[int(arg)%2])
			case 8:
				rr.ticks(1 + int(arg)%8)
			case 9:
				rr.g.ReloadRoom(room)
			case 10:
				if l, ok := rr.es.Of(m6BK); ok {
					switch arg % 3 {
					case 0:
						rr.es.DropWeapon(rr.g, l.PID)
					case 1:
						rr.es.GrabWeapon(rr.g, l.PID)
					case 2:
						rr.es.TakeWeapon(rr.g, l.PID)
					}
				}
			}
		}
		rr.g.SetEvent(0, 0)
		rr.g.SetPauseMenu(false)
		rr.ticks(4)
		if err := rr.bm.actorErr(); err != nil {
			t.Fatalf("the game halted: %v", err)
		}
		auditM6(t, rr, sent)
	})
}

// auditM6 is FuzzReconcileM6's write audit.
func auditM6(t *testing.T, rr *m6Rig, sent map[[2]int]bool) {
	t.Helper()
	g := NewGuardFor(AllTargets)
	ws, zs := rr.bm.written(), rr.zm.zones()
	if len(ws) != len(zs) {
		t.Fatalf("%d writes, %d zone tables", len(ws), len(zs))
	}
	shrunkAt := map[uint32]int{} // pid: 1 + the tick its timer was written (the fuzz loads no save state)
	for i, w := range ws {
		rep := rr.reps[w.tick]
		if !rep.Gates.CanWriteIdle() || rep.State != StateArmed {
			t.Fatalf("a write at 0x%08X on tick %d with %s, %s", w.a, w.tick, rep.Gates.Why(), rep.State)
		}
		if w.n != 1 || w.old&^w.val != 0 && g.TargetOf(w.a) == TargetKills {
			t.Fatalf("a write of %d bytes at 0x%08X, 0x%02X over 0x%02X", w.n, w.a, w.val, w.old)
		}
		switch {
		case g.TargetOf(w.a) == TargetKills:
			z := int(w.a-addr.Zones) / addr.ZoneSize
			at := zs[i]
			room := int(at.roomNo[z])
			if room < 0 || room >= addr.RoomCount || int(at.zoneNo[room]) != z {
				t.Fatalf("a dead-actor byte in zone slot %d, which was no room's (mRoomNo %d)", z, room)
			}
			for _, id := range killSetIDs(w.a, z, w.val&^w.old) {
				if at.stage != drc.Name || !sent[[2]int{room, id}] || !synctables.SyncedKill(rr.tb, addr.TableDRC, at.stage, room, id) {
					t.Fatalf("setID %d of room %d of %s written: sent %v, synced %v", id, room, at.stage,
						sent[[2]int{room, id}], synctables.SyncedKill(rr.tb, addr.TableDRC, at.stage, room, id))
				}
			}
		case w.a >= fakegame.EnemyHeap && w.a < fakegame.EnemyHeap+0x1000000:
			off := (w.a - fakegame.EnemyHeap) % 0x3000
			switch {
			case off == addr.ActorOffCarryByte:
				// A weapon let go: CARRY cleared and nothing else, on a
				// BOKO in ENEMY_CARRY (not Link's), after its own
				// carrier's timer in the same tick.
				carrier, ok := rr.es.CarrierOf(w.pid)
				switch {
				case w.old&addr.ActorCarryByteMask == 0 || w.val != w.old&^addr.ActorCarryByteMask:
					t.Fatalf("a weapon write 0x%02X over 0x%02X", w.val, w.old)
				case !ok || w.proc != addr.ProcBoko:
					t.Fatalf("a CARRY write on pid %d (proc 0x%04X), no carrier's weapon", w.pid, uint16(w.proc))
				case w.bokoMode != addr.BokoModeEnemyCarry:
					t.Fatalf("the CARRY of pid %d cleared in mNowMode %d, not ENEMY_CARRY", w.pid, w.bokoMode)
				case shrunkAt[carrier] != w.tick+1:
					t.Fatalf("the weapon pid %d let go on tick %d, its carrier pid %d's timer not written that tick", w.pid, w.tick, carrier)
				}
			case w.old == 0 && w.val == 1:
				// A timer: of the placed enemy whose actor the block held
				// as it was written, verified, keyed, its key sent and
				// synced, not BOSS, Link in no demo, and never before.
				p, _, ok := rr.es.Born(w.pid)
				k, isK1 := combat.K1(w.proc)
				switch {
				case !ok || int16(p.Proc) != w.proc:
					t.Fatalf("a timer write at +0x%X into pid %d (proc 0x%04X), no placed enemy's", off, w.pid, uint16(w.proc))
				case !isK1 || !k.Verified || off != k.Ice+addr.EnemyIceOffTimer:
					t.Fatalf("a timer write at +0x%X into %s, not a verified class's timer", off, p.Proc)
				case p.Stage != drc.Name || p.SetID == addr.ActorSetIDNone || !sent[[2]int{p.Room, int(p.SetID)}] ||
					!synctables.SyncedKill(rr.tb, addr.TableDRC, p.Stage, p.Room, int(p.SetID)):
					t.Fatalf("pid %d (%s r%d setID %d) shrunk: sent %v", w.pid, p.Stage, p.Room, p.SetID, sent[[2]int{p.Room, int(p.SetID)}])
				case p.Boss:
					t.Fatalf("a BOSS enemy pid %d shrunk", w.pid)
				case w.demo != 0:
					t.Fatalf("pid %d shrunk during Link demo %d", w.pid, w.demo)
				case shrunkAt[w.pid] != 0:
					t.Fatalf("pid %d shrunk twice", w.pid)
				}
				shrunkAt[w.pid] = w.tick + 1
			default:
				t.Fatalf("a heap write at +0x%X, 0x%02X over 0x%02X", off, w.val, w.old)
			}
		default:
			t.Fatalf("a write at 0x%08X outside M6's bytes", w.a)
		}
	}
}

var _ mem.Mem = (*beatMem)(nil)

func init() { _ = time.Second }
