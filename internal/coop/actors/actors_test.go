package actors

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// fakeActorType is what the hand-laid queues' g_fopAc_type and actor_type
// hold (any nonzero value; the game's counter starts at 0x9130001,
// f_pc_base.cpp:28-33).
const fakeActorType = 0x09130001

type fakeActor struct {
	at     uint32
	proc   int16
	setID  uint16
	group  uint8
	room   int8
	health int8
	pos    [3]float32
}

func store(t testing.TB, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u16b(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }

// layActors links actors into g_fopAcTg_Queue in order, the way
// fopAc_Create and cTg_Addition leave them, every one executing (mInitState
// 2), and returns their nodes.
func layActors(t testing.TB, m *mem.FakeMem, actors ...fakeActor) []uint32 {
	t.Helper()
	store(t, m, addr.FopAcType, u32b(fakeActorType))
	nodes := make([]uint32, len(actors))
	for i, a := range actors {
		nodes[i] = a.at + addr.ActorOffActorTag
	}
	for i, a := range actors {
		var prev, next uint32
		if i > 0 {
			prev = nodes[i-1]
		}
		if i+1 < len(nodes) {
			next = nodes[i+1]
		}
		n := nodes[i]
		store(t, m, n+addr.TagOffPrev, u32b(prev))
		store(t, m, n+addr.TagOffData, u32b(addr.ActorQueue))
		store(t, m, n+addr.TagOffNext, u32b(next))
		store(t, m, n+addr.TagOffTagData, u32b(a.at))
		store(t, m, n+addr.TagOffUse, []byte{1})
		store(t, m, a.at+addr.ActorOffActorType, u32b(fakeActorType))
		store(t, m, a.at+addr.ActorOffBsPcID, u32b(uint32(100+i)))
		store(t, m, a.at+addr.ActorOffProcName, u16b(uint16(a.proc)))
		store(t, m, a.at+addr.ActorOffInitState, []byte{2})
		store(t, m, a.at+addr.ActorOffSetID, u16b(a.setID))
		store(t, m, a.at+addr.ActorOffGroup, []byte{a.group})
		store(t, m, a.at+addr.ActorOffHomeRoomNo, []byte{byte(a.room)})
		store(t, m, a.at+addr.ActorOffHealth, []byte{byte(a.health)})
		store(t, m, a.at+addr.ActorOffMaxHealth, []byte{byte(a.health)})
		for j, f := range a.pos {
			store(t, m, a.at+addr.ActorOffCurPos+uint32(4*j), u32b(math.Float32bits(f)))
		}
	}
	var head, tail uint32
	if len(nodes) > 0 {
		head, tail = nodes[0], nodes[len(nodes)-1]
	}
	store(t, m, addr.ActorQueue+addr.ActorQueueOffHead, u32b(head))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(tail))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffSize, u32b(uint32(len(nodes))))
	store(t, m, addr.ProcessIDCounter, u32b(uint32(100+len(nodes))))
	return nodes
}

// iceUp gives a's enemyice its self-pointer and mode 1, as its create and
// enemy_ice's first call leave it.
func iceUp(t testing.TB, m *mem.FakeMem, a fakeActor) {
	t.Helper()
	c, _ := K1(a.proc)
	store(t, m, a.at+c.Ice, u32b(a.at))
	store(t, m, a.at+c.Ice+addr.EnemyIceOffMode, []byte{1})
}

var (
	link = fakeActor{at: 0x80A00000, proc: 0xA9, setID: addr.ActorSetIDNone, group: 1, room: -1, pos: [3]float32{10, 20, 30}}
	boko = fakeActor{at: 0x80A10000, proc: 0xBE, setID: 5, group: 2, room: 3, health: 4}
	pot  = fakeActor{at: 0x80A20000, proc: 0x1CB, setID: 9, group: 0, room: 3}
)

func TestWalkActors(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	iceUp(t, m, boko)
	w, err := Read(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Complete() || len(w.Actors) != 3 || w.Size != 3 || w.Type != fakeActorType || w.ProcCounter != 103 {
		t.Fatalf("walk: stop %q notes %q actors %d size %d type 0x%X counter %d", w.Stop, w.Notes, len(w.Actors), w.Size, w.Type, w.ProcCounter)
	}
	l, b := w.Actors[0], w.Actors[1]
	if l.Addr != link.at || l.Proc != 0xA9 || l.SetID != addr.ActorSetIDNone || l.Group != 1 || l.HomeRoom != -1 ||
		l.Pos != link.pos || l.Ice.Off != 0 || l.InitState != 2 {
		t.Errorf("Link: %+v", l)
	}
	if b.PID != 101 || b.SetID != 5 || b.Health != 4 || !b.Ice.Read || !b.Ice.SelfOK || b.Ice.Mode != 1 || b.Ice.Timer != 0 ||
		b.TimerAddr() != boko.at+0x126A || !b.Keyed() || ProcName(b.Proc) != "bk" {
		t.Errorf("Bokoblin: %+v", b)
	}
	if a, ok := w.ByPID(101); !ok || a.Addr != boko.at {
		t.Error("ByPID")
	}
	if a, ok := w.ByAddr(pot.at); !ok || a.PID != 102 {
		t.Error("ByAddr")
	}

	// An empty queue, and one before any actor was created, are clean
	// empty walks.
	empty := mem.NewFakeMem()
	layActors(t, empty)
	if w, err := Read(empty, 0); err != nil || !w.Complete() || len(w.Actors) != 0 {
		t.Errorf("empty queue: %+v, %v", w, err)
	}
	if w, err := Read(mem.NewFakeMem(), 0); err != nil || !w.Complete() || len(w.Actors) != 0 {
		t.Errorf("no actor type yet: %+v, %v", w, err)
	}
}

// TestWalkActorsStops breaks the list in each way the walk guards against
// and checks it stops there, having followed no pointer it did not check.
func TestWalkActorsStops(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		mut   func(m *mem.FakeMem, nodes []uint32)
		rows  int
		stop  string
	}{
		{"cycle", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[2]+addr.TagOffNext, u32b(n[0])) }, 3, "mSize is 3"},
		{"cycle within mSize", 0, func(m *mem.FakeMem, n []uint32) {
			m.Store(n[2]+addr.TagOffNext, u32b(n[0]))
			m.Store(addr.ActorQueue+addr.ActorQueueOffSize, u32b(9))
		}, 3, "has a cycle"},
		{"self loop", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[0])) }, 1, "has a cycle"},
		{"next outside MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(0x90000000)) }, 2, "is outside MEM1"},
		{"next at the end of MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(addr.MEM1End-8)) }, 2, "is outside MEM1"},
		{"head below its actor", 0, func(m *mem.FakeMem, n []uint32) { m.Store(addr.ActorQueue, u32b(addr.MEM1Base+0x10)) }, 0, "no room in MEM1"},
		{"misaligned", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[1]+2)) }, 1, "not word-aligned"},
		{"prev changed", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffPrev, u32b(0x80123450)) }, 1, "changed during the walk"},
		{"tag data", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffTagData, u32b(0x80123450)) }, 1, "not its actor"},
		{"actor type", 0, func(m *mem.FakeMem, n []uint32) { m.Store(boko.at+addr.ActorOffActorType, u32b(0)) }, 1, "not g_fopAc_type"},
		{"limit", 2, func(*mem.FakeMem, []uint32) {}, 2, "limit of 2"},
		{"type 0 with a head", 0, func(m *mem.FakeMem, n []uint32) { m.Store(addr.FopAcType, u32b(0)) }, 0, "g_fopAc_type is 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mem.NewFakeMem()
			c.mut(m, layActors(t, m, link, boko, pot))
			w, err := Read(m, c.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(w.Actors) != c.rows || !strings.Contains(w.Stop, c.stop) || w.Complete() {
				t.Errorf("actors %d stop %q; want %d actors, stop with %q", len(w.Actors), w.Stop, c.rows, c.stop)
			}
		})
	}

	// mpTail disagreeing with a clean walk is a note, not a stop, but the
	// walk is not complete.
	m := mem.NewFakeMem()
	layActors(t, m, link, boko)
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(0x80123450))
	if w, _ := Read(m, 0); w.Stop != "" || len(w.Actors) != 2 || len(w.Notes) != 1 || w.Complete() {
		t.Errorf("notes: stop %q actors %d notes %q", w.Stop, len(w.Actors), w.Notes)
	}

	// An enemyice past the end of MEM1 is not read.
	edge := boko
	edge.at = addr.MEM1End - addr.ActorSize
	m = mem.NewFakeMem()
	layActors(t, m, edge)
	if w, err := Read(m, 0); err != nil || len(w.Actors) != 1 || w.Actors[0].Ice.Read || Eligible(&w.Actors[0], false) != "enemyice unreadable" {
		t.Errorf("ice past MEM1: %+v %v", w.Actors, err)
	}
}

// TestWalkRequiresQueueData: a node whose mpData is not the queue is no
// queued actor (cLs_Addition sets it, c_list.cpp:40): the walk stops.
func TestWalkRequiresQueueData(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	store(t, m, boko.at+addr.ActorOffActorTag+addr.TagOffData, u32b(0))
	w, _ := Read(m, 0)
	if len(w.Actors) != 1 || !strings.Contains(w.Stop, "mpData") {
		t.Errorf("actors %d stop %q", len(w.Actors), w.Stop)
	}
}

// TestWalkMbIsUse: a node whose mbIsUse is 0 has left its list
// (cTg_SingleCut; cTg_Create clears it, c_tag.cpp:89-92): the walk stops.
func TestWalkMbIsUse(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	store(t, m, pot.at+addr.ActorOffActorTag+addr.TagOffUse, []byte{0})
	w, _ := Read(m, 0)
	if len(w.Actors) != 2 || !strings.Contains(w.Stop, "mbIsUse") {
		t.Errorf("actors %d stop %q", len(w.Actors), w.Stop)
	}
}

// TestK1MatchesDesign pins the K1 classes to the design's §7.2 timer
// offsets (mLightShrinkTimer is enemyice +6): every class but Morth, which
// is no target (every placed one is a generator).
func TestK1MatchesDesign(t *testing.T) {
	timers := map[string]uint32{
		"BK": 0x126A, "MO2": 0x2A5A, "ST": 0x1FAE, "RD": 0x6E6, "CC": 0x822, "KI": 0x92A,
		"AM": 0xAC2, "AM2": 0xA92, "BB": 0xC86, "FM": 0xAEE, "TN": 0x152A,
		"PH": 0x99E, "PT": 0x7EA, "PW": 0x88A, "BO": 0xA02,
	}
	cs := Classes()
	if len(cs) != len(timers) {
		t.Fatalf("%d classes, want %d", len(cs), len(timers))
	}
	for _, c := range cs {
		if want, ok := timers[c.Name]; !ok || c.Ice+addr.EnemyIceOffTimer != want {
			t.Errorf("proc 0x%X %s: timer at 0x%X, design says 0x%X", c.Proc, c.Name, c.Ice+6, want)
		}
		if c.Ice < addr.ActorSize {
			t.Errorf("%s: enemyice at 0x%X is inside fopAc_ac_c", c.Name, c.Ice)
		}
		if !c.Verified { // the live check K1, 2026-10-15
			t.Errorf("%s: not verified", c.Name)
		}
	}
	if _, ok := K1(int16(stagedata.ProcKS)); ok {
		t.Error("Morth is a K1 target")
	}
	if bk, _ := K1(int16(stagedata.ProcBK)); bk.Weapon != 0x1200 || !bk.WeaponVerified {
		t.Errorf("the Bokoblin's weapon: %+v", bk)
	}
	if tn, _ := K1(int16(stagedata.ProcTN)); tn.Weapon != 0x1410 || !tn.WeaponVerified {
		t.Errorf("the Darknut's weapon: %+v", tn)
	}
}

// eligibleActor is a Bokoblin every gate passes.
func eligibleActor() Actor {
	return Actor{Addr: 0x80A10000, Node: 0x80A100C4, PID: 7, Proc: int16(stagedata.ProcBK), InitState: 2, SetID: 5, Group: 2, HomeRoom: 3,
		Health: 4, Ice: IceState{Off: 0x1264, Read: true, Self: 0x80A10000, SelfOK: true, Mode: 1}}
}

// TestK1GateMatrix: each per-actor gate of §7.2 refuses on its own.
func TestK1GateMatrix(t *testing.T) {
	unverify(t, stagedata.ProcPW)
	if why := Eligible(&[]Actor{eligibleActor()}[0], false); why != "" {
		t.Fatalf("the eligible Bokoblin: %s", why)
	}
	rd := eligibleActor()
	rd.Proc, rd.Ice.Off, rd.ModeOK = int16(stagedata.ProcRD), 0x6E0, true
	fm := eligibleActor()
	fm.Proc, fm.Ice.Off, fm.ModeOK = int16(stagedata.ProcFM), 0xAE8, true
	for _, c := range []struct {
		name       string
		base       Actor
		mut        func(a *Actor)
		unverified bool
		want       string
	}{
		{"not K1", eligibleActor(), func(a *Actor) { a.Proc = 0x1CB }, false, "not a K1 class"},
		{"Morth", eligibleActor(), func(a *Actor) { a.Proc = int16(stagedata.ProcKS) }, false, "not a K1 class"},
		{"a boss", eligibleActor(), func(a *Actor) { a.Proc = int16(stagedata.ProcBDK) }, false, "not a K1 class"},
		{"unverified", eligibleActor(), func(a *Actor) { a.Proc = int16(stagedata.ProcPW) }, false, "not verified"},
		{"creating", eligibleActor(), func(a *Actor) { a.InitState = 1 }, false, "mInitState 1"},
		{"leaving", eligibleActor(), func(a *Actor) { a.InitState = 3 }, false, "mInitState 3"},
		{"group", eligibleActor(), func(a *Actor) { a.Group = 3 }, false, "group 3"},
		{"setID past the zone", eligibleActor(), func(a *Actor) { a.SetID = 0x200 }, false, "asserts on"},
		{"keyed in room -1", eligibleActor(), func(a *Actor) { a.HomeRoom = -1 }, false, "asserts on"},
		{"self", eligibleActor(), func(a *Actor) { a.Ice.SelfOK = false }, false, "is not the actor"},
		{"timer", eligibleActor(), func(a *Actor) { a.Ice.Timer = 1 }, false, "timer is 1"},
		{"ice mode 0", eligibleActor(), func(a *Actor) { a.Ice.Mode = 0 }, false, "enemyice mode 0"},
		{"frozen", eligibleActor(), func(a *Actor) { a.Ice.Mode = 2 }, false, "enemyice mode 2"},
		{"freeze timer", eligibleActor(), func(a *Actor) { a.Ice.FreezeTimer = 5 }, false, "freeze timer"},
		{"dying", eligibleActor(), func(a *Actor) { a.Health = 0 }, false, "health 0"},
		{"BOSS", eligibleActor(), func(a *Actor) { a.Status = addr.ActorStatusBoss }, false, "BOSS"},
		{"carried", eligibleActor(), func(a *Actor) { a.Status = addr.ActorStatusCarry }, false, "CARRY"},
		{"event", eligibleActor(), func(a *Actor) { a.EvtCmd = 1 }, false, "an event"},
		{"ReDead grab", rd, func(a *Actor) { a.Mode = 7 }, false, "MODE_ATTACK"},
		{"ReDead switch wait", rd, func(a *Actor) { a.Mode = 0xA }, false, "MODE_SW_WAIT"},
		{"ReDead mode unread", rd, func(a *Actor) { a.ModeOK = false }, false, "unreadable"},
		{"Floormaster grab", fm, func(a *Actor) { a.Mode = 13 }, false, "grab"},
		{"Floormaster Link's demo", fm, func(a *Actor) { a.Mode = 17 }, false, "grab"},
		// Pass:
		{"unkeyed", eligibleActor(), func(a *Actor) { a.SetID, a.HomeRoom = addr.ActorSetIDNone, -1 }, false, ""},
		{"unverified with --unverified", eligibleActor(), func(a *Actor) { a.Proc = int16(stagedata.ProcPW) }, true, ""},
		{"ReDead walking", rd, func(a *Actor) { a.Mode = 4 }, false, ""},
		{"Floormaster waiting", fm, func(a *Actor) { a.Mode = 7 }, false, ""},
	} {
		a := c.base
		c.mut(&a)
		got := Eligible(&a, c.unverified)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// fakeRoom is the room the fake game's enemies are placed in.
const fakeRoom = 1

var fakeDRC = fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}

// fakeEnemies is a game in DRC room 1 with a Bokoblin holding its weapon
// and a ChuChu, both executing.
func fakeEnemies(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: fakeDRC.Name, Room: fakeRoom, SetID: 1, Proc: stagedata.ProcBK, Weapon: true},
		fakegame.Enemy{Stage: fakeDRC.Name, Room: fakeRoom, SetID: 2, Proc: stagedata.ProcCC},
	)
	if err := g.LoadStage(fakeDRC); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(fakeRoom); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	return g, m, es
}

// TestRevalidate: the walk's actors pass, and each change between the walk
// and the write is caught.
func TestRevalidate(t *testing.T) {
	g, m, es := fakeEnemies(t)
	w, err := Read(m, 0)
	if err != nil || !w.Complete() {
		t.Fatalf("%+v %v", w, err)
	}
	bkLive, _ := es.Of(0)
	bk, ok := w.ByPID(bkLive.PID)
	if !ok || Eligible(bk, false) != "" {
		t.Fatalf("the Bokoblin: %+v, %s", bk, Eligible(bk, false))
	}
	wp, why := WeaponOf(&w, bk, false)
	if wp == nil || why != "" {
		t.Fatalf("its weapon: %v %s", wp, why)
	}
	if now, nw, err := Revalidate(m, bk, wp, false); err != nil || now.PID != bk.PID || nw == nil || nw.PID != wp.PID {
		t.Fatalf("Revalidate: %v", err)
	}

	// The weapon on the ground: no weapon to let go, the carrier still
	// fine; the weapon passed in is refused.
	store(t, m, wp.Addr+addr.ActorOffCarryByte, []byte{0})
	if w2, _ := Read(m, 0); func() bool { b, _ := w2.ByPID(bk.PID); x, _ := WeaponOf(&w2, b, false); return x != nil }() {
		t.Error("a weapon on the ground is let go")
	}
	if _, _, err := Revalidate(m, bk, wp, false); !errors.Is(err, ErrChanged) {
		t.Errorf("a weapon no longer carried: %v", err)
	}

	// Freed and reused by another actor: refused.
	cc, _ := es.Of(1)
	ccA, _ := w.ByPID(cc.PID)
	if _, err := es.FreeAndReuse(g, cc.PID, stagedata.ProcCC); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Revalidate(m, ccA, nil, false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "reused") {
		t.Errorf("a reused block: %v", err)
	}

	// The home room's zone gone: refused, as onActor would assert.
	_, m, es = fakeEnemies(t)
	w, _ = Read(m, 0)
	bkLive, _ = es.Of(0)
	bk, _ = w.ByPID(bkLive.PID)
	zn, _ := addr.ZoneNoAddr(fakeRoom)
	store(t, m, zn, []byte{0xFF})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "has zone -1") {
		t.Errorf("the zone gone: %v", err)
	}
	if ok, _ := ZoneHolds(m, fakeRoom); ok {
		t.Error("ZoneHolds with no zone")
	}

	// Its timer set already, or taken off the queue: refused.
	_, m, es = fakeEnemies(t)
	w, _ = Read(m, 0)
	bkLive, _ = es.Of(0)
	bk, _ = w.ByPID(bkLive.PID)
	store(t, m, bk.TimerAddr(), []byte{1})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, ErrChanged) {
		t.Errorf("a timer already set: %v", err)
	}
	store(t, m, bk.TimerAddr(), []byte{0})
	store(t, m, bk.Node+addr.TagOffUse, []byte{0})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, ErrChanged) {
		t.Errorf("a node off the queue: %v", err)
	}
}

// TestWeaponHeld: a weapon the carrier no longer holds is never let go,
// though its weapon field still names it; and a carrier that takes its
// weapon up between the walk and the write is held.
func TestWeaponHeld(t *testing.T) {
	g, m, es := fakeEnemies(t)
	bkLive, _ := es.Of(0)
	walk := func() (Walk, *Actor) {
		t.Helper()
		w, err := Read(m, 0)
		if err != nil || !w.Complete() {
			t.Fatalf("%+v %v", w, err)
		}
		bk, ok := w.ByPID(bkLive.PID)
		if !ok {
			t.Fatal("no Bokoblin")
		}
		return w, bk
	}
	w, bk := walk()
	held, _ := WeaponOf(&w, bk, false)
	if held == nil || HeldBy(bk, held) != "" {
		t.Fatalf("the held stick: %v", held)
	}

	// Knocked out of its hands (yari_off_check): m1200 still names the
	// stick, m0B30 is 0. Nothing to let go, and the carrier may go.
	if err := es.DropWeapon(g, bkLive.PID); err != nil {
		t.Fatal(err)
	}
	w, bk = walk()
	if bk.Weapon != held.PID || bk.Hold != 0 || !bk.HoldOK {
		t.Fatalf("the dropped state: weapon %d hold %d", bk.Weapon, bk.Hold)
	}
	if wp, why := WeaponOf(&w, bk, false); wp != nil || why != "" {
		t.Errorf("a dropped stick: %v %q", wp, why)
	}

	// Link picks the stick up: CARRY set again, in PLAYER_CARRY. Still
	// not the Bokoblin's: neither the plan nor the re-read takes it.
	if err := es.GrabWeapon(g, bkLive.PID); err != nil {
		t.Fatal(err)
	}
	w, bk = walk()
	lw, _ := w.ByPID(held.PID)
	if !lw.Carried() || lw.Mode != addr.BokoModePlayerCarry {
		t.Fatalf("Link's stick: %+v", lw)
	}
	if wp, why := WeaponOf(&w, bk, false); wp != nil || why != "" {
		t.Errorf("Link's stick taken as the Bokoblin's: %v %q", wp, why)
	}
	if why := HeldBy(bk, lw); why == "" {
		t.Error("HeldBy takes Link's stick")
	}
	if _, nw, err := Revalidate(m, bk, lw, false); !errors.Is(err, ErrChanged) || nw != nil {
		t.Errorf("Revalidate with Link's stick: %v", err)
	}
	if _, nw, err := Revalidate(m, bk, nil, false); err != nil || nw != nil {
		t.Errorf("Revalidate of the carrier alone: %v", err)
	}
	// Its record says it holds one while the stick is Link's: hold.
	store(t, m, bk.Addr+holdBK, u32b(1))
	w, bk = walk()
	if wp, why := WeaponOf(&w, bk, false); wp != nil || !strings.Contains(why, "Link holds") {
		t.Errorf("a record that disagrees: %v %q", wp, why)
	}
	store(t, m, bk.Addr+holdBK, u32b(0))

	// Planned with no weapon, it takes its stick up before the write:
	// the re-read holds it, so the new CARRY is never left on a deleted
	// carrier.
	w, bk = walk()
	if wp, why := WeaponOf(&w, bk, false); wp != nil || why != "" {
		t.Fatalf("the plan: %v %q", wp, why)
	}
	if err := es.TakeWeapon(g, bkLive.PID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), "took up a weapon") {
		t.Errorf("a stick taken up after the walk: %v", err)
	}
	w, bk = walk()
	if wp, why := WeaponOf(&w, bk, false); wp == nil || why != "" {
		t.Errorf("the stick taken up again: %v %q", wp, why)
	}

	// A Stalfos whose mace is not yet drawn (m1DD0 1) waits.
	st := Actor{Proc: int16(stagedata.ProcST), Weapon: 7, Hold: 1, HoldOK: true}
	if wp, why := WeaponOf(&w, &st, false); wp != nil || !strings.Contains(why, "between hands") {
		t.Errorf("a Stalfos's mace before its draw: %v %q", wp, why)
	}
}

// unverify makes K1 class p unverified for one test.
func unverify(t testing.TB, p stagedata.Proc) {
	old := SetVerified(int16(p), false)
	t.Cleanup(func() { SetVerified(int16(p), old) })
}
