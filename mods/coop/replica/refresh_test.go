package replica

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/switches"
)

// The stale-actor refresh's fixture (refresh.go): DRC with Link in room 1
// and room 2 beside it, a small-key DOOR10 between them and a Bokoblin in
// room 1, both reading memory switch 0x05 (switch5Policy grants it): the
// door as its lock switch, the Bokoblin as its death switch. Both stand
// 1000 units from Link, who is at the origin.
const refreshSw = 0x05

// refreshDoorParams is a keyshut DOOR10 of type 4 (a small key door,
// chkMakeKey 1, d_a_door10.cpp:12-24) with swbit refreshSw and no second
// swbit, as M_NewD2's TGDR entries have them (0x0FFFF436 for sw54).
const refreshDoorParams = 0x0FFFF400 | refreshSw

// refreshDoorAngle is home.angle.x of a door between rooms 1 (front) and
// 2 (back): getFRoomNo & 0x3F, getBRoomNo >> 6 & 0x3F (d_door.cpp:15-61).
const refreshDoorAngle = 1 | 2<<6

var far = [3]float32{1000, 0, 0}

func refreshDoor() fakegame.Enemy {
	return fakegame.Enemy{Stage: drc.Name, Room: -1, SetID: addr.ActorSetIDNone, Proc: stage.ProcDoor10, Params: refreshDoorParams,
		Angle: [3]int16{refreshDoorAngle, 0, -193}, Pos: far}
}

// refreshBk is an unkeyed Bokoblin of room 1 with death switch refreshSw
// (home.angle.z's low byte, d_a_bk.cpp:4771-4780), as DRC's pot ambushers
// are placed.
func refreshBk() fakegame.Enemy {
	return fakegame.Enemy{Stage: drc.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcBK, Angle: [3]int16{0, 0, refreshSw}, Pos: far}
}

// refreshRig is a reconciling replica in DRC's room 1, welcomed with the
// image, the switches and combat, its actors es created and executing,
// with no settle time.
type refreshRig struct {
	*recRig
	es *fakegame.Enemies
}

func newRefreshRig(t *testing.T, claimed combat.ClaimFunc, es ...fakegame.Enemy) *refreshRig {
	t.Helper()
	return newRefreshRigSettle(t, -1, claimed, es...)
}

// newRefreshRigSettle is newRefreshRig with Config.RefreshSettle settle.
func newRefreshRigSettle(t *testing.T, settle time.Duration, claimed combat.ClaimFunc, es ...fakegame.Enemy) *refreshRig {
	t.Helper()
	demoVerified(t, true)
	rec := newRecRigNoWelcome(t, Config{Apply: true, Tables: m6Tables(t), Claimed: claimed, RefreshSettle: settle})
	rec.frames = 3
	for range gate.StableTicks + 2 {
		rec.tick()
	}
	rec.welcome(proto.FeatureImage | proto.FeatureSwitches | proto.FeatureCombat)
	rec.world(save.Image{})
	rec.tick()
	rec.hub.take()
	placed := fakegame.PlaceEnemies(es...)
	rec.bm.setActors(placed)
	rec.load(drcKeys)
	rec.enter(1, 2)
	rr := &refreshRig{recRig: rec, es: placed}
	if n := rr.ticks(gate.StableTicks + 4); n != 0 {
		t.Fatalf("%d bytes written before any bit came", n)
	}
	return rr
}

// door is the fixture's door, live.
func (rr *refreshRig) door(i int) fakegame.LiveEnemy {
	rr.t.Helper()
	d, ok := rr.es.Of(i)
	if !ok {
		rr.t.Fatalf("actor %d is not live", i)
	}
	return d
}

// setLock sets door i's dDoor_key2_c: mbEnabled and m20 (DOOR10's lock at
// +0x308, d_a_door10.h:46), as setKey and keyInit leave them.
func (rr *refreshRig) setLock(i int, enabled, anim byte) {
	d := rr.door(i)
	rr.store(d.Addr+0x308, []byte{enabled})
	rr.store(d.Addr+0x308+0x20, []byte{anim})
}

func (rr *refreshRig) lock(i int) byte { return rr.read(rr.door(i).Addr+0x308, 1)[0] }

// linkAt puts Link at p.
func (rr *refreshRig) linkAt(p [3]float32) {
	var b [12]byte
	for i, v := range p {
		binary.BigEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	rr.store(fakegame.PlayerActor+addr.ActorOffCurPos, b[:])
}

// landSwitch has the world set DRC's switch no, and Link change room so
// the replica writes it (OnRoomChange): into room 2, room 1 staying loaded
// with its actors, then runs after (if any). It returns once the write
// was made and verified.
func (rr *refreshRig) landSwitch(no int, after ...func()) {
	rr.t.Helper()
	off, m := switchImageByte(addr.TableDRC, no)
	var img save.Image
	img[off] = m
	rr.world(img)
	rr.ticks(2)
	rr.enter(2, 1)
	for _, f := range after {
		f()
	}
	for range gate.StableTicks + rr.holdTicks() + 4 {
		rr.tick()
		if _, ok := rr.r.writer.Landed().Has(SwitchKey(addr.TableDRC, -1, no)); ok {
			return
		}
	}
	rr.t.Fatalf("switch 0x%02X never landed\n%s", no, rr.log)
}

// heapWrites are the replica's writes on the actor heap.
func (rr *refreshRig) heapWrites() []auditWrite {
	var out []auditWrite
	for _, w := range rr.bm.written() {
		if w.a >= fakegame.EnemyHeap && w.a < fakegame.EnemyHeap+0x1000000 {
			out = append(out, w)
		}
	}
	return out
}

// TestLandedSetDecode: a verified write lands each bit it set, per bit
// and keyed by its store: a live memBit's chests, memory switches and
// pickups; a dan switch with room -1; a zone switch and a dead setID with
// their room. A bit of another table, a saved slot's byte and a MAX byte
// do not land; a new visit clears the set.
func TestLandedSetDecode(t *testing.T) {
	w := &FrameWriter{}
	s := &state.Snapshot{StageName: drc.Name, Table: addr.TableDRC}
	w.landVisit(s, gate.Status{}, bind.File{})
	now := time.Now()
	_, sw5, _ := addr.SwitchBit(0x05)
	tb, _ := addr.TboxBit(9)
	it, _ := addr.MemBitItemBit(2)
	w.landMemBit(addr.LiveMemBit+uint32(sw5.Off), addr.TableDRC, sw5.Mask, now)
	w.landMemBit(addr.LiveMemBit+uint32(tb.Off), addr.TableDRC, tb.Mask, now)
	w.landMemBit(addr.LiveMemBit+uint32(it.Off), addr.TableDRC, it.Mask, now)
	w.landMemBit(addr.LiveMemBit+addr.MemBitOffKeyNum, addr.TableDRC, 0x01, now)          // a key count: no bit
	w.landMemBit(addr.SaveImage+uint32(addr.OffMemBit(addr.TableDRC)+sw5.Off), 3, 1, now) // a saved slot
	_, dan, _ := addr.SwitchBit(0x85)
	w.landScoped(ScopedWrite{Kind: wire.ScopeDan, Addr: addr.Dan + uint32(dan.Off), Bits: dan.Mask, Room: -1, Slot: -1}, addr.TableDRC, now)
	zw, err := scopedWriteOf(wire.ScopeRoomEdge, 0xE0, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	w.landScoped(zw, addr.TableDRC, now)
	kw, err := killWriteOf(7, 3, 40)
	if err != nil {
		t.Fatal(err)
	}
	w.landKills(kw, addr.TableDRC, now)
	w.landMemBit(addr.LiveMemBit+uint32(sw5.Off), addr.TableFW, 0x02, now) // another table
	l := w.Landed()
	want := []LandedBit{
		{LandSwitch, addr.TableDRC, -1, 0x05},
		{LandTbox, addr.TableDRC, -1, 9},
		{LandItem, addr.TableDRC, -1, 2},
		{LandSwitch, addr.TableDRC, -1, 0x85},
		{LandSwitch, addr.TableDRC, 7, 0xE0},
		{LandKill, addr.TableDRC, 7, 40},
	}
	for _, b := range want {
		if _, ok := l.Has(b); !ok {
			t.Errorf("%s did not land", b)
		}
	}
	if len(l.At) != len(want) {
		t.Errorf("landed %v, want exactly %v", l.At, want)
	}
	if SwitchKey(addr.TableDRC, 7, 0x05) != want[0] || SwitchKey(addr.TableDRC, 7, 0xE0) != want[4] {
		t.Error("SwitchKey keys a memory switch by room, or a room switch without")
	}
	w.landVisit(&state.Snapshot{StageName: "M_Dra09", Table: addr.TableDRC}, gate.Status{}, bind.File{})
	if n := len(w.Landed().At); n != 0 {
		t.Errorf("%d bits survived a stage change", n)
	}
}

// TestLandedSetFromVerify: the merge path's write of a partner's switch
// lands it once verified, and a switch the game set itself in the same
// byte does not; a stage change clears the set.
func TestLandedSetFromVerify(t *testing.T) {
	rr := newRefreshRig(t, nil)
	rr.onSwitch(0x04, -1) // the receiver's own, in switch 5's byte
	rr.landSwitch(refreshSw)
	l := rr.r.writer.Landed()
	if _, ok := l.Has(SwitchKey(addr.TableDRC, -1, 0x04)); ok || len(l.At) != 1 {
		t.Errorf("landed %v, want switch 5 alone", l.At)
	}
	if st := rr.r.Status().Refresh; st.Landed != 1 {
		t.Errorf("status %+v", st)
	}
	rr.load(woods)
	rr.ticks(gate.StableTicks + 2)
	if n := len(rr.r.writer.Landed().At); n != 0 {
		t.Errorf("%d bits survived the stage change", n)
	}
}

// TestRefreshLockClear (Tier 1): a door whose lock switch landed has its
// lock cleared, one byte from 1 to 0, once Link is more than 300 units
// away, and only once.
func TestRefreshLockClear(t *testing.T) {
	rr := newRefreshRig(t, nil, refreshDoor())
	rr.setLock(0, 1, 0)
	rr.linkAt(far)
	rr.landSwitch(refreshSw)
	rr.ticks(4)
	if rr.lock(0) != 1 || len(rr.heapWrites()) != 0 {
		t.Fatalf("the lock was cleared with Link at the door (lock %d)", rr.lock(0))
	}
	if st := rr.r.Status().Refresh; st.LockHeld != 1 || !strings.Contains(st.LockLastHeld, "Link within 300") {
		t.Errorf("status %+v", st)
	}
	rr.linkAt([3]float32{})
	rr.ticks(3)
	if rr.lock(0) != 0 {
		t.Fatalf("the lock stands: %d\n%s", rr.lock(0), rr.log)
	}
	ws := rr.heapWrites()
	if len(ws) != 1 || ws[0].a != rr.door(0).Addr+0x308 || ws[0].n != 1 || ws[0].old != 1 || ws[0].val != 0 {
		t.Fatalf("heap writes %+v", ws)
	}
	// The game builds it again (as if a stale value came back): never
	// written twice, and the next tick's verify reads the clear held.
	rr.ticks(3)
	st := rr.r.Status()
	if st.Refresh.Locks != 1 || st.Refresh.LockHeld != 0 || st.Writes.Failed != 0 || st.WritesOff != "" {
		t.Errorf("status %+v, writes %+v", st.Refresh, st.Writes)
	}
	if !rr.log.has("refresh: cleared the stale lock of actor") {
		t.Errorf("the log:\n%s", rr.log)
	}
	rr.setLock(0, 1, 0)
	rr.ticks(4)
	if len(rr.heapWrites()) != 1 {
		t.Error("a door's lock was cleared twice")
	}
}

// TestRefreshLockHeld (Tier 1): each gate holds the clear on its own.
func TestRefreshLockHeld(t *testing.T) {
	for _, c := range []struct {
		name  string
		set   func(rr *refreshRig)
		why   string
		after bool // set after the room change that lands the switch
	}{
		{"the unlock animation plays", func(rr *refreshRig) { rr.setLock(0, 1, 1) }, "unlock animation", false},
		{"an event ordered it", func(rr *refreshRig) {
			rr.store(rr.door(0).Addr+addr.ActorOffEventCommand, []byte{0, 3})
		}, "an event ordered it", false},
		{"a mirror copy", func(rr *refreshRig) { rr.store(rr.door(0).Addr+addr.ActorOffPauseFlag, []byte{1}) }, "paused: mirror", false},
		{"Link in a demo", func(rr *refreshRig) { rr.g.SetLinkDemo(2) }, "link demo 2", false},
		{"neither room settled", func(rr *refreshRig) {
			for _, room := range []int{1, 2} {
				rs, _ := addr.RoomStatusAddr(room)
				f := rr.read(rs+addr.RoomStatusOffFlags, 1)[0]
				rr.store(rs+addr.RoomStatusOffFlags, []byte{f | addr.RoomFlagCreating})
			}
		}, "not loaded and settled", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newRefreshRig(t, nil, refreshDoor())
			rr.setLock(0, 1, 0)
			if c.after {
				rr.landSwitch(refreshSw, func() { c.set(rr) })
			} else {
				c.set(rr)
				rr.landSwitch(refreshSw)
			}
			rr.ticks(4)
			if rr.lock(0) != 1 || len(rr.heapWrites()) != 0 {
				t.Fatalf("cleared (lock %d)", rr.lock(0))
			}
			if st := rr.r.Status().Refresh; st.LockHeld != 1 || !strings.Contains(st.LockLastHeld, c.why) {
				t.Errorf("status %+v, want a hold for %q", st, c.why)
			}
		})
	}
}

// TestRefreshSettle: a bit that landed waits the settle time before either
// tier acts on it (design gate 8).
func TestRefreshSettle(t *testing.T) {
	rr := newRefreshRigSettle(t, time.Hour, nil, refreshDoor(), refreshBk())
	rr.setLock(0, 1, 0)
	rr.landSwitch(refreshSw)
	rr.ticks(4)
	if len(rr.heapWrites()) != 0 {
		t.Fatal("written before the bit settled")
	}
	st := rr.r.Status().Refresh
	if st.LockHeld != 1 || st.Held != 1 || !strings.Contains(st.LockLastHeld, "settling") || !strings.Contains(st.LastHeld, "settling") {
		t.Errorf("status %+v", st)
	}
}

// TestRefreshLockNotLanded (Tier 1): a lock switch the receiver's own game
// set is never acted on, nor is a door whose lock is already open.
func TestRefreshLockNotLanded(t *testing.T) {
	rr := newRefreshRig(t, nil, refreshDoor())
	rr.setLock(0, 1, 0)
	rr.onSwitch(refreshSw, -1) // a key opened it here
	rr.enter(2, 1)
	rr.ticks(gate.StableTicks + rr.holdTicks() + 6)
	if rr.lock(0) != 1 || len(rr.heapWrites()) != 0 {
		t.Fatalf("a local switch cleared the lock (%d)", rr.lock(0))
	}
	if st := rr.r.Status().Refresh; st.Landed != 0 || st.LockHeld != 0 {
		t.Errorf("status %+v", st)
	}

	rr2 := newRefreshRig(t, nil, refreshDoor())
	rr2.setLock(0, 0, 0)
	rr2.landSwitch(refreshSw)
	rr2.ticks(4)
	if len(rr2.heapWrites()) != 0 || rr2.r.Status().Refresh.LockHeld != 0 {
		t.Errorf("an open lock was written or held: %+v", rr2.r.Status().Refresh)
	}
}

// TestLockDoorRoles: the decoder names the lock switch of a small key
// door, and of a boss door only with a swbit below 0x80 (a boss door at
// 0x80 and above is keyOn unconditionally, d_a_door10.cpp:44, so item-
// gated, never refreshed); a plain door has none.
func TestLockDoorRoles(t *testing.T) {
	for _, c := range []struct {
		proc   stage.Proc
		params uint32
		no     int
		ok     bool
	}{
		{stage.ProcDoor10, 0x0FFFF436, 0x36, true}, // keyshut, type 4
		{stage.ProcDoor10, 0x0FFFF536, 0x36, true}, // type 5
		{stage.ProcDoor10, 0x0FFFF140, 0x40, true}, // door20, a boss door, swbit 0x40
		{stage.ProcDoor10, 0x0FFFF185, 0, false},   // a boss door, swbit 0x85
		{stage.ProcDoor10, 0x0FFFF036, 0, false},   // type 0: no lock
		{stage.ProcDoor12, 0x0FFFF136, 0x36, true}, // DOOR12 type 1, small key
		{stage.ProcBK, 0x0FFFF436, 0, false},
	} {
		u, ok := switches.LiveRole(c.proc, c.params, [3]int16{refreshDoorAngle, 0, -193}, -1, switches.RoleKeyLock)
		if ok != c.ok || ok && (u.No != c.no || u.Room != -1 || u.Store != addr.SwitchMemory) {
			t.Errorf("proc 0x%X params 0x%08X: %+v %v, want switch 0x%02X %v", uint16(c.proc), c.params, u, ok, c.no, c.ok)
		}
	}
}

// TestRefreshDeathSwitch (Tier 2): a Bokoblin whose death switch landed is
// light-shrunk once by the second allow rule, with no kill store at all,
// and its death runs as M6's keyed shrink does.
func TestRefreshDeathSwitch(t *testing.T) {
	rr := newRefreshRig(t, nil, refreshBk())
	bk, _ := rr.es.Of(0)
	rr.landSwitch(refreshSw)
	rr.ticks(3)
	k, _ := combat.K1(int16(stage.ProcBK))
	ws := rr.heapWrites()
	if len(ws) != 1 || ws[0].a != bk.Addr+k.Ice+addr.EnemyIceOffTimer || ws[0].old != 0 || ws[0].val != 1 {
		t.Fatalf("heap writes %+v\n%s", ws, rr.log)
	}
	rr.ticks(35)
	if _, ok := rr.es.Of(0); ok {
		t.Fatal("the Bokoblin outlived its shrink")
	}
	st := rr.r.Status()
	if st.Refresh.Shrunk != 1 || st.Combat.Shrunk != 1 || st.Refresh.Held != 0 || st.Writes.Failed != 0 {
		t.Errorf("status %+v %+v, writes %+v", st.Refresh, st.Combat, st.Writes)
	}
	if !rr.log.has("refresh: light-shrunk a stale enemy whose death switch landed") {
		t.Errorf("the log:\n%s", rr.log)
	}
	if len(rr.heapWrites()) != 1 {
		t.Error("written twice")
	}
	if err := rr.bm.actorErr(); err != nil {
		t.Fatal(err)
	}
}

// TestRefreshDeathSwitchHeld (Tier 2): a death switch the receiver's own
// game set is no target; a claimed Bokoblin and one Link stands by are
// held, with the refresh's own reason in its status.
func TestRefreshDeathSwitchHeld(t *testing.T) {
	t.Run("set locally", func(t *testing.T) {
		rr := newRefreshRig(t, nil, refreshBk())
		rr.onSwitch(refreshSw, -1)
		rr.enter(2, 1)
		rr.ticks(gate.StableTicks + rr.holdTicks() + 6)
		if len(rr.heapWrites()) != 0 {
			t.Fatal("a local death switch shrank the Bokoblin")
		}
		if st := rr.r.Status().Refresh; st.Held != 0 {
			t.Errorf("status %+v", st)
		}
	})
	t.Run("claimed", func(t *testing.T) {
		var claimedPID uint32
		rr := newRefreshRig(t, func(pid uint32) bool { return pid == claimedPID }, refreshBk())
		bk, _ := rr.es.Of(0)
		claimedPID = bk.PID
		rr.landSwitch(refreshSw)
		rr.ticks(4)
		if len(rr.heapWrites()) != 0 {
			t.Fatal("a claimed Bokoblin was shrunk")
		}
		st := rr.r.Status()
		if st.Refresh.Held != 1 || !strings.Contains(st.Refresh.LastHeld, "claimed") || st.Combat.Held != 0 {
			t.Errorf("refresh %+v, combat %+v", st.Refresh, st.Combat)
		}
	})
	t.Run("Link near", func(t *testing.T) {
		rr := newRefreshRig(t, nil, refreshBk())
		rr.linkAt(far)
		rr.landSwitch(refreshSw)
		rr.ticks(4)
		if len(rr.heapWrites()) != 0 {
			t.Fatal("shrunk with Link beside it")
		}
		if st := rr.r.Status().Refresh; !strings.Contains(st.LastHeld, "Link within 300") {
			t.Errorf("status %+v", st)
		}
		rr.linkAt([3]float32{})
		rr.ticks(3)
		if len(rr.heapWrites()) != 1 {
			t.Error("not shrunk once Link left")
		}
	})
}

// TestRefreshDeathKeyed (Tier 2): a keyed Bokoblin whose death switch
// landed is shrunk by the death switch rule only when M1's verdict syncs
// its key: its death records the setID in this game's zone
// (fopAcM_onActor, c_damagereaction.cpp:159). Room 1's setID 8 is placed
// by no enemy of the fixture, so its mask refuses it, as it would a
// Shared key.
func TestRefreshDeathKeyed(t *testing.T) {
	t.Run("key outside the mask", func(t *testing.T) {
		bk := refreshBk()
		bk.SetID = 8
		rr := newRefreshRig(t, nil, bk)
		rr.landSwitch(refreshSw)
		rr.ticks(4)
		if len(rr.heapWrites()) != 0 {
			t.Fatalf("a keyed Bokoblin whose key does not sync was shrunk\n%s", rr.log)
		}
		if st := rr.r.Status().Refresh; st.Held != 1 || !strings.Contains(st.LastHeld, "does not sync") {
			t.Errorf("status %+v", st)
		}
	})
	t.Run("synced key", func(t *testing.T) {
		bk := refreshBk()
		bk.SetID = 2
		rr := newRefreshRig(t, nil, bk)
		rr.landSwitch(refreshSw)
		rr.ticks(4)
		if len(rr.heapWrites()) != 1 {
			t.Fatalf("heap writes %+v\n%s", rr.heapWrites(), rr.log)
		}
		if st := rr.r.Status().Refresh; st.Shrunk != 1 {
			t.Errorf("status %+v", st)
		}
	})
}

// TestRefreshKeyedRoomSettled: once a switch landed the walk runs with no
// live store room, but M6's keyed rule still takes a target only in a
// room loaded and settled: a kill of room 1, being deleted, waits.
func TestRefreshKeyedRoomSettled(t *testing.T) {
	bk := fakegame.Enemy{Stage: drc.Name, Room: 1, SetID: 2, Proc: stage.ProcBK, Pos: far}
	rr := newRefreshRig(t, nil, bk)
	rr.landSwitch(refreshSw)
	rs, _ := addr.RoomStatusAddr(1)
	f := rr.read(rs+addr.RoomStatusOffFlags, 1)[0]
	rr.store(rs+addr.RoomStatusOffFlags, []byte{f | addr.RoomFlagDeleting})
	say(t, rr.r, killMsg(t, drc, 1, 2))
	rr.ticks(4)
	if len(rr.heapWrites()) != 0 {
		t.Fatalf("a keyed enemy of a room being deleted was shrunk\n%s", rr.log)
	}
	if st := rr.r.Status().Combat; !strings.Contains(st.LastHeld, "not loaded and settled") {
		t.Errorf("status %+v", st)
	}
	rr.store(rs+addr.RoomStatusOffFlags, []byte{f})
	rr.ticks(4)
	if len(rr.heapWrites()) != 1 {
		t.Errorf("not shrunk once the room settled: %+v", rr.heapWrites())
	}
}

// TestRefreshDeathLinkRecheck (Tier 2): Link's position is read again
// just before the write, not taken from the plan: Link far at the tick's
// read and beside the Bokoblin by the writer's heartbeat holds it.
func TestRefreshDeathLinkRecheck(t *testing.T) {
	rr := newRefreshRig(t, nil, refreshBk())
	rr.linkAt(far)
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	if len(rr.heapWrites()) != 0 {
		t.Fatal("shrunk with Link beside it")
	}
	moved := false
	for range 4 {
		rr.bm.onHeartbeat(func(n int) {
			switch n {
			case 1: // the tick's own read: Link away
				rr.linkAt([3]float32{})
			case 2: // the writer's first beat read: Link back beside it
				rr.linkAt(far)
				moved = true
			}
		})
		rr.tick()
	}
	rr.bm.onHeartbeat(nil)
	if !moved {
		t.Fatal("the hook never ran")
	}
	if len(rr.heapWrites()) != 0 {
		t.Fatal("shrunk though Link came near before the write")
	}
	if st := rr.r.Status().Refresh; !strings.Contains(st.LastHeld, "Link within 300") {
		t.Errorf("status %+v", st)
	}
	rr.linkAt([3]float32{})
	rr.ticks(3)
	if len(rr.heapWrites()) != 1 {
		t.Error("not shrunk once Link left")
	}
}

// TestRefreshDeathFromHomeAngle (Tier 2): the death switch is decoded from
// home.angle, which a Bokoblin keeps though its create zeroes
// current.angle.z (d_a_bk.cpp:4772); an actor of no K1 class is no
// candidate; a keyed one is held unless its key syncs.
func TestRefreshDeathFromHomeAngle(t *testing.T) {
	a := &actors.Actor{Proc: int16(stage.ProcBK), SetID: addr.ActorSetIDNone, HomeRoom: 1, HomeAngle: [3]int16{0, 0, 0x7F05}, Pos: far}
	rt := &refreshTick{
		landed: Landed{Stage: drc.Name, Table: addr.TableDRC, At: map[LandedBit]time.Time{SwitchKey(addr.TableDRC, 1, 0x05): {}}},
		s:      &state.Snapshot{StageName: drc.Name, Table: addr.TableDRC},
		linkOK: true, flagsOK: true,
	}
	rt.flags[1] = addr.RoomFlagLoaded
	_, bit, _ := addr.SwitchBit(0x05)
	rt.s.Save[addr.LiveMemBit-state.SaveRegion.Start+uint32(bit.Off)] = bit.Mask
	if why, cand := rt.deathWhy(a); !cand || why != "" {
		t.Errorf("deathWhy = %q, %v", why, cand)
	}
	keyed := *a
	keyed.SetID = 8
	if why, cand := rt.deathWhy(&keyed); !cand || !strings.Contains(why, "does not sync") {
		t.Errorf("keyed, no mask: deathWhy = %q, %v", why, cand)
	}
	var ks enemies.KillSet
	ks[0] = 1 << 8
	rt.keys = map[int]enemies.KillSet{1: ks}
	if why, cand := rt.deathWhy(&keyed); !cand || why != "" {
		t.Errorf("keyed, synced: deathWhy = %q, %v", why, cand)
	}
	keyed.SetID = 7
	if why, cand := rt.deathWhy(&keyed); !cand || !strings.Contains(why, "does not sync") {
		t.Errorf("keyed, outside the mask: deathWhy = %q, %v", why, cand)
	}
	rt.boss = true
	if why, cand := rt.deathWhy(a); !cand || !strings.Contains(why, "boss stage") {
		t.Errorf("in a boss stage: deathWhy = %q, %v", why, cand)
	}
	rt.boss = false
	rt.s.Save[addr.LiveMemBit-state.SaveRegion.Start+uint32(bit.Off)] = 0
	if why, cand := rt.deathWhy(a); !cand || !strings.Contains(why, "reads clear") {
		t.Errorf("cleared since: deathWhy = %q, %v", why, cand)
	}
	a.HomeAngle[2] = 0
	if _, cand := rt.deathWhy(a); cand {
		t.Error("a Bokoblin with no death switch is a candidate")
	}
	a.HomeAngle[2], a.Proc = 5, int16(stage.ProcDoor10)
	if _, cand := rt.deathWhy(a); cand {
		t.Error("a door is a death-switch candidate")
	}
}

// TestRefreshGuardLock: the WriteGuard lets a lock clear through only for
// a door of the tick's walk and plan, 1 to 0, at its mbEnabled.
func TestRefreshGuardLock(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	es := fakegame.PlaceEnemies(refreshDoor())
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
	walk, err := combat.Read(m, 0)
	if err != nil || len(walk.Actors) != 1 {
		t.Fatalf("walk %+v %v", walk, err)
	}
	d := walk.Actors[0]
	wg := NewWriteGuard(GuardConfig{Targets: RefreshTargets})
	wg.SetTick(Tick{Gates: settledGates(*s), Armed: true, Table: s.Table, Zones: ZoneTableOf(s)})
	plan := func(c CombatTick) {
		c.Walk = &walk
		wg.SetCombat(c)
	}
	a := d.Addr + 0x308
	if err := wg.checkLock(&d, a, 1, 0); err == nil {
		t.Error("taken with no plan")
	}
	plan(CombatTick{Demo: true, Locks: func(x *actors.Actor) bool { return x.PID == d.PID }})
	if err := wg.checkLock(&d, a, 1, 0); err != nil {
		t.Errorf("refused: %v", err)
	}
	other := func(f func(x *actors.Actor)) actors.Actor {
		x := d
		f(&x)
		return x
	}
	for _, c := range []struct {
		name     string
		d        actors.Actor
		a        uint32
		old, val byte
	}{
		{"another byte", d, a + 1, 1, 0},
		{"set, not clear", d, a, 0, 1},
		{"another pid", other(func(x *actors.Actor) { x.PID++ }), a, 1, 0},
		{"other params", other(func(x *actors.Actor) { x.Params ^= 1 }), a, 1, 0},
		{"a Bokoblin", other(func(x *actors.Actor) { x.Proc = int16(stage.ProcBK) }), a, 1, 0},
	} {
		if err := wg.checkLock(&c.d, c.a, c.old, c.val); err == nil {
			t.Errorf("%s: taken", c.name)
		}
	}
	plan(CombatTick{Demo: false, Locks: func(*actors.Actor) bool { return true }})
	if err := wg.checkLock(&d, a, 1, 0); err == nil {
		t.Error("taken with Link's demo gate shut")
	}
	m6 := NewWriteGuard(GuardConfig{Targets: M6Targets})
	m6.SetTick(Tick{Gates: settledGates(*s), Armed: true, Table: s.Table, Zones: ZoneTableOf(s)})
	m6.SetCombat(CombatTick{Walk: &walk, Demo: true, Locks: func(*actors.Actor) bool { return true }})
	if err := m6.checkLock(&d, a, 1, 0); err == nil {
		t.Error("a guard without TargetLock took it")
	}
	if !strings.Contains(RefreshTargets.String(), "lock") || AllTargets&TargetLock == 0 {
		t.Errorf("targets %s, all %s", RefreshTargets, AllTargets)
	}
}
