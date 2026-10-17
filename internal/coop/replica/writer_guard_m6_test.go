package replica

import (
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// m6Guard is a game in DRC's room 1, room 2 beside it, with the M6
// enemies created and executing, its snapshot, walk and a WriteGuard of
// M6Targets opened on an idle tick of it.
type m6Guard struct {
	m  *mem.FakeMem
	g  *fakegame.FakeGame
	es *fakegame.Enemies
	s  *state.Snapshot
	w  actors.Walk
	wg *WriteGuard
}

func newM6Guard(t testing.TB) *m6Guard {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	es := m6Enemies()
	if err := g.LoadStage(drcKeys); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	s, err := state.Read(m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	w, err := actors.Read(m, 0)
	if err != nil || !w.Complete() {
		t.Fatalf("walk %+v %v", w, err)
	}
	wg := NewWriteGuard(GuardConfig{Targets: M6Targets})
	wg.SetTick(Tick{Gates: settledGates(*s), Armed: true, Table: s.Table, Zones: ZoneTableOf(s)})
	return &m6Guard{m: m, g: g, es: es, s: s, w: w, wg: wg}
}

// plan opens the guard for the M6 plan: kills of the listed rooms'
// setIDs, every K1 actor of the walk allowed, Link's demo gate open.
func (mg *m6Guard) plan(kills map[int][]int) {
	var flags [addr.RoomCount]uint8
	for room := range addr.RoomCount {
		a, _ := addr.RoomStatusAddr(room)
		b, _ := mem.ReadU8(mg.m, a+addr.RoomStatusOffFlags)
		flags[room] = b
	}
	k := func(room, id int) bool {
		for _, x := range kills[room] {
			if x == id {
				return true
			}
		}
		return false
	}
	mg.wg.SetCombat(CombatTick{Flags: flags, FlagsOK: true, Kills: k, Walk: &mg.w,
		Allow: func(a *actors.Actor) bool { return a.Keyed() && k(int(a.HomeRoom), int(a.SetID)) }, Demo: true})
}

func (mg *m6Guard) actor(t testing.TB, i int) *actors.Actor {
	t.Helper()
	l, ok := mg.es.Of(i)
	if !ok {
		t.Fatalf("enemy %d is not live", i)
	}
	a, ok := mg.w.ByPID(l.PID)
	if !ok {
		t.Fatalf("enemy %d is not in the walk", i)
	}
	return a
}

// killByte is the dead-actor byte of setID id in room's zone, and its
// bit.
func (mg *m6Guard) killByte(t testing.TB, room, id int) (uint32, byte) {
	t.Helper()
	z, ok := mg.s.ZoneFor(room)
	if !ok {
		t.Fatalf("room %d has no zone", room)
	}
	kw, err := killWriteOf(room, z, id)
	if err != nil {
		t.Fatal(err)
	}
	return kw.Addr, kw.Bits
}

// TestWriteGuardKillSlot: a dead-actor byte is let through only into the
// zone of a loaded room, hidden or not, for the plan's setIDs, setting
// bits only.
func TestWriteGuardKillSlot(t *testing.T) {
	mg := newM6Guard(t)
	if err := mg.g.HideRoom(2); err != nil {
		t.Fatal(err)
	}
	a1, b3 := mg.killByte(t, 1, 3)
	a2, b1 := mg.killByte(t, 2, 1)
	// Without the plan, nothing.
	if err := mg.wg.check(a1, 0, b3, pathKill); err == nil || !strings.Contains(err.Error(), "no M6 plan") {
		t.Fatalf("without a plan: %v", err)
	}
	mg.plan(map[int][]int{1: {3}, 2: {1}})
	for _, c := range []struct {
		name     string
		a        uint32
		old, val byte
		p        path
		why      string
	}{
		{"room 1's ChuChu", a1, 0, b3, pathKill, ""},
		{"hidden room 2's Bokoblin", a2, 0, b1, pathKill, ""},
		{"another setID of room 1", a1, 0, b3 << 1, pathKill, "in no partner's store"},
		{"a clear", a1, b3, 0, pathKill, "clear"},
		{"through the merge path", a1, 0, b3, pathMerge, "only by"},
		{"a freed slot", zoneByteAddr(31, addr.ZoneOffActor+3), 0, 1, pathKill, "no room's"},
	} {
		err := mg.wg.check(c.a, c.old, c.val, c.p)
		if c.why == "" && err != nil || c.why != "" && (err == nil || !strings.Contains(err.Error(), c.why)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.why)
		}
	}
	// A room being created or deleted is left.
	rs, _ := addr.RoomStatusAddr(1)
	f, _ := mem.ReadU8(mg.m, rs+addr.RoomStatusOffFlags)
	mg.m.Store(rs+addr.RoomStatusOffFlags, []byte{f | addr.RoomFlagDeleting})
	mg.plan(map[int][]int{1: {3}})
	if err := mg.wg.check(a1, 0, b3, pathKill); err == nil || !strings.Contains(err.Error(), "not loaded and settled") {
		t.Errorf("a room being deleted: %v", err)
	}
	// The plan goes with the tick.
	mg.wg.SetTick(Tick{Gates: settledGates(*mg.s), Armed: true, Table: mg.s.Table, Zones: ZoneTableOf(mg.s)})
	if err := mg.wg.check(a2, 0, b1, pathKill); err == nil {
		t.Error("a write after SetTick without SetCombat")
	}
}

// TestWriteGuardActor: the light-shrink timer is let through as (T) only:
// one byte, 0 to 1, of an actor of the walk that the plan allows, verified
// and eligible, with Link's demo gate open and the gates open.
func TestWriteGuardActor(t *testing.T) {
	unverify(t, stage.ProcTN)
	mg := newM6Guard(t)
	mg.plan(map[int][]int{1: {3, 9}})
	cc := mg.actor(t, m6CC)
	tn := mg.actor(t, m6TN)
	bk := mg.actor(t, m6BK2) // not in the plan
	other := *cc
	other.PID++
	frozen := *cc
	frozen.Ice.Mode = 2
	for _, c := range []struct {
		name     string
		c        *actors.Actor
		a        uint32
		old, val byte
		why      string
	}{
		{"the ChuChu", cc, cc.TimerAddr(), 0, 1, ""},
		{"a u16's second byte", cc, cc.TimerAddr() + 1, 0, 1, "not"},
		{"a timer already counting", cc, cc.TimerAddr(), 1, 2, "only go from 0 to 1"},
		{"a 2", cc, cc.TimerAddr(), 0, 2, "only go from 0 to 1"},
		{"another process in the block", &other, cc.TimerAddr(), 0, 1, "not in this tick's walk"},
		{"no plan for it", bk, bk.TimerAddr(), 0, 1, "no target"},
		{"an unverified class", tn, tn.TimerAddr(), 0, 1, "not verified"},
		{"frozen", &frozen, cc.TimerAddr(), 0, 1, "enemyice mode 2"},
		{"no carrier", nil, cc.TimerAddr(), 0, 1, "no carrier"},
	} {
		err := mg.wg.checkActor(actorTimer, c.c, nil, c.a, c.old, c.val)
		if c.why == "" && err != nil || c.why != "" && (err == nil || !strings.Contains(err.Error(), c.why)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.why)
		}
	}
	// The demo gate, the gates and the target.
	ct := mg.wg.combat
	ct.Demo = false
	mg.wg.SetCombat(ct)
	if err := mg.wg.checkActor(actorTimer, cc, nil, cc.TimerAddr(), 0, 1); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Errorf("Link's demo gate shut: %v", err)
	}
	busy := *mg.s
	busy.EvtMode = 1
	mg.wg.SetTick(Tick{Gates: settledGates(busy), Armed: true, Table: mg.s.Table, Zones: ZoneTableOf(mg.s)})
	mg.plan(map[int][]int{1: {3}})
	if err := mg.wg.checkActor(actorTimer, cc, nil, cc.TimerAddr(), 0, 1); err == nil || !errors.Is(err, ErrGuard) {
		t.Errorf("during an event: %v", err)
	}
	if g := NewWriteGuard(GuardConfig{Targets: M5Targets}); g.checkActor(actorTimer, cc, nil, cc.TimerAddr(), 0, 1) == nil {
		t.Error("a guard without TargetK1 let the timer through")
	}
}

// TestWriteGuardWeapon: a weapon's CARRY byte is let go only after its
// carrier's timer was written and read back this tick, CARRY cleared and
// every other bit kept, and only for the weapon the carrier holds.
func TestWriteGuardWeapon(t *testing.T) {
	mg := newM6Guard(t)
	mg.plan(map[int][]int{1: {1, 2}})
	bk := mg.actor(t, m6BK)
	bk2 := mg.actor(t, m6BK2)
	wl, _ := mg.es.WeaponOf(m6BK)
	wp, _ := mg.w.ByPID(wl.PID)
	ca := wp.Addr + addr.ActorOffCarryByte
	carry := addr.ActorCarryByteMask
	if err := mg.wg.checkActor(actorWeapon, bk, wp, ca, carry, 0); err == nil || !strings.Contains(err.Error(), "not written and read back") {
		t.Fatalf("before the timer: %v", err)
	}
	mg.wg.noteShrunk(bk)
	for _, c := range []struct {
		name     string
		c, w     *actors.Actor
		a        uint32
		old, val byte
		why      string
	}{
		{"CARRY cleared", bk, wp, ca, carry, 0, ""},
		{"CARRY cleared, others kept", bk, wp, ca, carry | 1, 1, ""},
		{"another bit cleared too", bk, wp, ca, carry | 1, 0, "only lose CARRY"},
		{"not carried", bk, wp, ca, 0, 0, "only lose CARRY"},
		{"another byte", bk, wp, ca + 1, carry, 0, "not the CARRY byte"},
		{"another carrier's", bk2, wp, ca, carry, 0, "holds pid"},
		{"no weapon", bk, nil, ca, carry, 0, "no weapon"},
		{"a carrier as its weapon", bk, bk2, bk2.Addr + addr.ActorOffCarryByte, carry, 0, "no BOKO"},
	} {
		err := mg.wg.checkActor(actorWeapon, c.c, c.w, c.a, c.old, c.val)
		if c.why == "" && err != nil || c.why != "" && (err == nil || !strings.Contains(err.Error(), c.why)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.why)
		}
	}
	// A stick the Bokoblin does not hold, though m1200 names it, with
	// CARRY set: Link's (d_a_player_grab.inc:330-339), or flying.
	for _, c := range []struct {
		name string
		c, w *actors.Actor
		why  string
	}{
		{"Link's stick (PLAYER_CARRY)", bk, withMode(wp, addr.BokoModePlayerCarry), "Link holds"},
		{"a stick flying (MOVE)", bk, withMode(wp, 1), "not ENEMY_CARRY"},
		{"a carrier whose m0B30 says it holds none", withHold(bk, 0), wp, "holds no weapon"},
	} {
		g := newM6Guard(t)
		g.plan(map[int][]int{1: {1}})
		g.wg.noteShrunk(bk)
		if err := g.wg.checkActor(actorWeapon, c.c, c.w, ca, carry, 0); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want %q", c.name, err, c.why)
		}
	}
	// noteShrunk lasts the tick.
	mg.wg.SetTick(Tick{Gates: settledGates(*mg.s), Armed: true, Table: mg.s.Table, Zones: ZoneTableOf(mg.s)})
	mg.plan(map[int][]int{1: {1}})
	if err := mg.wg.checkActor(actorWeapon, bk, wp, ca, carry, 0); err == nil {
		t.Error("a weapon let go on a later tick than its carrier's timer")
	}
	_ = stage.ProcBK
}

func withMode(a *actors.Actor, mode int32) *actors.Actor {
	c := *a
	c.Mode = mode
	return &c
}

func withHold(a *actors.Actor, v int32) *actors.Actor {
	c := *a
	c.Hold = v
	return &c
}

// FuzzWriteGuardActor drives checkActor with an arbitrary write and plan
// over the M6 game, and checks its verdict against an independent reading
// of the rules: a timer write passes only as 0 to 1 at the timer of a
// walked, allowed, verified, eligible actor; a weapon write only as CARRY
// cleared on the walked BOKO the carrier holds after its timer: in
// ENEMY_CARRY, with the Bokoblin's m0B30 nonzero. weapon puts the
// Bokoblin's stick in its hand (0), knocked away (1), or in Link's hands
// (2) before the walk.
func FuzzWriteGuardActor(f *testing.F) {
	f.Add(uint8(m6CC), uint8(0), uint8(0), uint8(1), uint8(0xFF), true, false, int8(0), uint8(0))
	f.Add(uint8(m6BK), uint8(1), uint8(0x20), uint8(0), uint8(0xFF), true, true, int8(0), uint8(0))
	f.Add(uint8(m6BK), uint8(1), uint8(0x20), uint8(0), uint8(0xFF), true, true, int8(0), uint8(2))
	f.Add(uint8(m6TN), uint8(0), uint8(0), uint8(1), uint8(0x0F), false, false, int8(1), uint8(1))
	f.Fuzz(func(t *testing.T, who, kind, old, val, allow uint8, demo, shrunk bool, nudge int8, weapon uint8) {
		mg := newM6Guard(t)
		if bk, ok := mg.es.Of(m6BK); ok && weapon%3 != 0 {
			if err := mg.es.DropWeapon(mg.g, bk.PID); err != nil {
				t.Fatal(err)
			}
			if weapon%3 == 2 {
				if err := mg.es.GrabWeapon(mg.g, bk.PID); err != nil {
					t.Fatal(err)
				}
			}
			w, err := actors.Read(mg.m, 0)
			if err != nil {
				t.Fatal(err)
			}
			mg.w = w
		}
		live := mg.es.Live()
		l := live[int(who)%len(live)]
		c, _ := mg.w.ByPID(l.PID)
		var wp *actors.Actor
		if wl, ok := mg.es.WeaponOf(m6BK); ok {
			wp, _ = mg.w.ByPID(wl.PID)
		}
		allowed := func(a *actors.Actor) bool { return allow&(1<<(a.PID%8)) != 0 }
		mg.wg.SetCombat(CombatTick{Walk: &mg.w, Allow: allowed, Demo: demo, Kills: func(int, int) bool { return true }, FlagsOK: true})
		if shrunk {
			mg.wg.noteShrunk(c)
		}
		k := actorTimer
		a := c.TimerAddr()
		if kind%2 == 1 {
			k = actorWeapon
			if wp != nil {
				a = wp.Addr + addr.ActorOffCarryByte
			}
		}
		a += uint32(int32(nudge) % 3)
		err := mg.wg.checkActor(k, c, wp, a, old, val)

		// The oracle.
		ok := demo && allowed(c)
		cls, isK1 := actors.K1(c.Proc)
		switch k {
		case actorTimer:
			ok = ok && isK1 && cls.Verified && a == c.TimerAddr() && a != 0 && old == 0 && val == 1 &&
				c.InitState == 2 && c.Group == 2 && c.Ice.SelfOK && c.Ice.Mode == 1 && c.Ice.Timer == 0 &&
				c.Ice.FreezeTimer == 0 && c.Health > 0 && c.Status&(addr.ActorStatusBoss|addr.ActorStatusCarry) == 0 &&
				c.EvtCmd == 0 && (c.SetID == 0xFFFF || c.Keyed()) && (cls.Mode == 0 || c.ModeOK)
		case actorWeapon:
			ok = ok && wp != nil && a == wp.Addr+addr.ActorOffCarryByte && old&0x20 != 0 && val == old&^0x20 &&
				c.Weapon == wp.PID && shrunk && wp.Proc == addr.ProcBoko && isK1 && cls.Weapon != 0 &&
				wp.Status&addr.ActorStatusCarry != 0 && wp.ModeOK && wp.Mode == 2 && // Mode_ENEMY_CARRY_e
				(c.Proc != int16(stage.ProcBK) || c.HoldOK && c.Hold != 0) // m0B30
		}
		if ok != (err == nil) {
			t.Fatalf("%s of %s at 0x%08X, 0x%02X -> 0x%02X: %v; the oracle says %v", k, c, a, old, val, err, ok)
		}
	})
}
