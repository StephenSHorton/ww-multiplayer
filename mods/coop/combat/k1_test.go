package combat

import (
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

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
	if _, ok := K1(int16(stage.ProcKS)); ok {
		t.Error("Morth is a K1 target")
	}
	if bk, _ := K1(int16(stage.ProcBK)); bk.Weapon != 0x1200 || !bk.WeaponVerified {
		t.Errorf("the Bokoblin's weapon: %+v", bk)
	}
	if tn, _ := K1(int16(stage.ProcTN)); tn.Weapon != 0x1410 || !tn.WeaponVerified {
		t.Errorf("the Darknut's weapon: %+v", tn)
	}
}

// eligibleActor is a Bokoblin every gate passes.
func eligibleActor() actors.Actor {
	return actors.Actor{Addr: 0x80A10000, Node: 0x80A100C4, PID: 7, Proc: int16(stage.ProcBK), InitState: 2, SetID: 5, Group: 2, HomeRoom: 3,
		Health: 4, Ice: actors.IceState{Off: 0x1264, Read: true, Self: 0x80A10000, SelfOK: true, Mode: 1}}
}

// TestK1GateMatrix: each per-actor gate of §7.2 refuses on its own.
func TestK1GateMatrix(t *testing.T) {
	unverify(t, stage.ProcPW)
	if why := Eligible(&[]actors.Actor{eligibleActor()}[0], false); why != "" {
		t.Fatalf("the eligible Bokoblin: %s", why)
	}
	rd := eligibleActor()
	rd.Proc, rd.Ice.Off, rd.ModeOK = int16(stage.ProcRD), 0x6E0, true
	fm := eligibleActor()
	fm.Proc, fm.Ice.Off, fm.ModeOK = int16(stage.ProcFM), 0xAE8, true
	for _, c := range []struct {
		name       string
		base       actors.Actor
		mut        func(a *actors.Actor)
		unverified bool
		want       string
	}{
		{"not K1", eligibleActor(), func(a *actors.Actor) { a.Proc = 0x1CB }, false, "not a K1 class"},
		{"Morth", eligibleActor(), func(a *actors.Actor) { a.Proc = int16(stage.ProcKS) }, false, "not a K1 class"},
		{"a boss", eligibleActor(), func(a *actors.Actor) { a.Proc = int16(stage.ProcBDK) }, false, "not a K1 class"},
		{"unverified", eligibleActor(), func(a *actors.Actor) { a.Proc = int16(stage.ProcPW) }, false, "not verified"},
		{"creating", eligibleActor(), func(a *actors.Actor) { a.InitState = 1 }, false, "mInitState 1"},
		{"leaving", eligibleActor(), func(a *actors.Actor) { a.InitState = 3 }, false, "mInitState 3"},
		{"group", eligibleActor(), func(a *actors.Actor) { a.Group = 3 }, false, "group 3"},
		{"setID past the zone", eligibleActor(), func(a *actors.Actor) { a.SetID = 0x200 }, false, "asserts on"},
		{"keyed in room -1", eligibleActor(), func(a *actors.Actor) { a.HomeRoom = -1 }, false, "asserts on"},
		{"self", eligibleActor(), func(a *actors.Actor) { a.Ice.SelfOK = false }, false, "is not the actor"},
		{"timer", eligibleActor(), func(a *actors.Actor) { a.Ice.Timer = 1 }, false, "timer is 1"},
		{"ice mode 0", eligibleActor(), func(a *actors.Actor) { a.Ice.Mode = 0 }, false, "enemyice mode 0"},
		{"frozen", eligibleActor(), func(a *actors.Actor) { a.Ice.Mode = 2 }, false, "enemyice mode 2"},
		{"freeze timer", eligibleActor(), func(a *actors.Actor) { a.Ice.FreezeTimer = 5 }, false, "freeze timer"},
		{"dying", eligibleActor(), func(a *actors.Actor) { a.Health = 0 }, false, "health 0"},
		{"BOSS", eligibleActor(), func(a *actors.Actor) { a.Status = addr.ActorStatusBoss }, false, "BOSS"},
		{"carried", eligibleActor(), func(a *actors.Actor) { a.Status = addr.ActorStatusCarry }, false, "CARRY"},
		{"event", eligibleActor(), func(a *actors.Actor) { a.EvtCmd = 1 }, false, "an event"},
		{"ReDead grab", rd, func(a *actors.Actor) { a.Mode = 7 }, false, "MODE_ATTACK"},
		{"ReDead switch wait", rd, func(a *actors.Actor) { a.Mode = 0xA }, false, "MODE_SW_WAIT"},
		{"ReDead mode unread", rd, func(a *actors.Actor) { a.ModeOK = false }, false, "unreadable"},
		{"Floormaster grab", fm, func(a *actors.Actor) { a.Mode = 13 }, false, "grab"},
		{"Floormaster Link's demo", fm, func(a *actors.Actor) { a.Mode = 17 }, false, "grab"},
		// Pass:
		{"unkeyed", eligibleActor(), func(a *actors.Actor) { a.SetID, a.HomeRoom = addr.ActorSetIDNone, -1 }, false, ""},
		{"unverified with --unverified", eligibleActor(), func(a *actors.Actor) { a.Proc = int16(stage.ProcPW) }, true, ""},
		{"ReDead walking", rd, func(a *actors.Actor) { a.Mode = 4 }, false, ""},
		{"Floormaster waiting", fm, func(a *actors.Actor) { a.Mode = 7 }, false, ""},
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
		fakegame.Enemy{Stage: fakeDRC.Name, Room: fakeRoom, SetID: 1, Proc: stage.ProcBK, Weapon: true},
		fakegame.Enemy{Stage: fakeDRC.Name, Room: fakeRoom, SetID: 2, Proc: stage.ProcCC},
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
	if _, _, err := Revalidate(m, bk, wp, false); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("a weapon no longer carried: %v", err)
	}

	// Freed and reused by another actor: refused.
	cc, _ := es.Of(1)
	ccA, _ := w.ByPID(cc.PID)
	if _, err := es.FreeAndReuse(g, cc.PID, stage.ProcCC); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Revalidate(m, ccA, nil, false); !errors.Is(err, actors.ErrChanged) || !strings.Contains(err.Error(), "reused") {
		t.Errorf("a reused block: %v", err)
	}

	// The home room's zone gone: refused, as onActor would assert.
	_, m, es = fakeEnemies(t)
	w, _ = Read(m, 0)
	bkLive, _ = es.Of(0)
	bk, _ = w.ByPID(bkLive.PID)
	zn, _ := addr.ZoneNoAddr(fakeRoom)
	store(t, m, zn, []byte{0xFF})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, actors.ErrChanged) || !strings.Contains(err.Error(), "has zone -1") {
		t.Errorf("the zone gone: %v", err)
	}
	if ok, _ := actors.ZoneHolds(m, fakeRoom); ok {
		t.Error("ZoneHolds with no zone")
	}

	// Its timer set already, or taken off the queue: refused.
	_, m, es = fakeEnemies(t)
	w, _ = Read(m, 0)
	bkLive, _ = es.Of(0)
	bk, _ = w.ByPID(bkLive.PID)
	store(t, m, bk.TimerAddr(), []byte{1})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("a timer already set: %v", err)
	}
	store(t, m, bk.TimerAddr(), []byte{0})
	store(t, m, bk.Node+addr.TagOffUse, []byte{0})
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("a node off the queue: %v", err)
	}
}

// TestWeaponHeld: a weapon the carrier no longer holds is never let go,
// though its weapon field still names it; and a carrier that takes its
// weapon up between the walk and the write is held.
func TestWeaponHeld(t *testing.T) {
	g, m, es := fakeEnemies(t)
	bkLive, _ := es.Of(0)
	walk := func() (actors.Walk, *actors.Actor) {
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
	if _, nw, err := Revalidate(m, bk, lw, false); !errors.Is(err, actors.ErrChanged) || nw != nil {
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
	if _, _, err := Revalidate(m, bk, nil, false); !errors.Is(err, actors.ErrChanged) || !strings.Contains(err.Error(), "took up a weapon") {
		t.Errorf("a stick taken up after the walk: %v", err)
	}
	w, bk = walk()
	if wp, why := WeaponOf(&w, bk, false); wp == nil || why != "" {
		t.Errorf("the stick taken up again: %v %q", wp, why)
	}

	// A Stalfos whose mace is not yet drawn (m1DD0 1) waits.
	st := actors.Actor{Proc: int16(stage.ProcST), Weapon: 7, Hold: 1, HoldOK: true}
	if wp, why := WeaponOf(&w, &st, false); wp != nil || !strings.Contains(why, "between hands") {
		t.Errorf("a Stalfos's mace before its draw: %v %q", wp, why)
	}
}

// unverify makes K1 class p unverified for one test.
func unverify(t testing.TB, p stage.Proc) {
	old := SetVerified(int16(p), false)
	t.Cleanup(func() { SetVerified(int16(p), old) })
}
