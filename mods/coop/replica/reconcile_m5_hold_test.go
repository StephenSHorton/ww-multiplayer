package replica

import (
	"fmt"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// TestReconcileM5RoomHold: a shutter created as Link comes through a door
// ignores a switch set in its first 30 executes (fakegame.Shutter,
// d_a_shutter.cpp:71-87, 133-142, 233-243). The partner's switch, sent as
// the room's actors are created, is written only once they have run long
// enough to see it change, and the shutter opens: a zone switch for the
// room Link enters, a dan switch for any room shown, and with an event or
// the pause menu, whose frames no actor executes, in between.
func TestReconcileM5RoomHold(t *testing.T) {
	// door is Link coming through a door from room from into room to: the
	// door shows room to (d_door.cpp:282), Link enters it, and the door
	// hides room from behind him (d_door.cpp:305).
	door := func(rr *m5Rig, from, to int) {
		rr.t.Helper()
		if err := rr.g.ShowRoom(to); err != nil {
			rr.t.Fatal(err)
		}
		rr.enter(to, from)
		if err := rr.g.HideRoom(from); err != nil {
			rr.t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		room  int             // the shutter's
		no    int             // its switch
		setup func(rr *m5Rig) // before the store comes; the shutter is created on the next frame
		msg   func(t *testing.T) *wire.Scoped
		pause func(rr *m5Rig, on bool) // the game not IDLE right after, for 25 ticks
	}{
		{name: "a zone switch, through a door into its room",
			room: 1, no: zoneOK,
			setup: func(rr *m5Rig) { door(rr, 1, 2); rr.settleM5(); door(rr, 2, 1) },
			msg:   func(t *testing.T) *wire.Scoped { return zoneMsg(t, drc, 1, zoneOK) }},
		{name: "a dan switch, a door opening onto another room",
			room: 2, no: danOK,
			setup: func(rr *m5Rig) {
				rr.enter(1, 2) // room 2 loaded beside room 1
				if err := rr.g.HideRoom(2); err != nil {
					rr.t.Fatal(err)
				}
				rr.settleM5()
				if err := rr.g.ShowRoom(2); err != nil {
					rr.t.Fatal(err)
				}
			},
			msg: func(t *testing.T) *wire.Scoped { return danMsg(t, drc, danOK) }},
		{name: "a zone switch, with an event after the door",
			room: 1, no: zoneOK,
			setup: func(rr *m5Rig) { door(rr, 1, 2); rr.settleM5(); door(rr, 2, 1) },
			msg:   func(t *testing.T) *wire.Scoped { return zoneMsg(t, drc, 1, zoneOK) },
			pause: func(rr *m5Rig, on bool) {
				if on {
					rr.g.SetEvent(1, 0)
				} else {
					rr.g.SetEvent(0, 0)
				}
			}},
		{name: "a dan switch, with the pause menu after the door",
			room: 1, no: danOK,
			setup: func(rr *m5Rig) { door(rr, 1, 2); rr.settleM5(); door(rr, 2, 1) },
			msg:   func(t *testing.T) *wire.Scoped { return danMsg(t, drc, danOK) },
			pause: func(rr *m5Rig, on bool) { rr.g.SetPauseMenu(on) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
			sh := &fakegame.Shutter{Stage: drc.Name, Room: c.room, No: c.no}
			rr.bm.setActors(sh)
			c.setup(rr)
			if c.pause != nil {
				c.pause(rr, true)
			}
			rr.send(c.msg(t)) // the hub's, as the receiver's PRESENCE enters
			if c.pause != nil {
				if n := rr.ticks(25); n != 0 {
					t.Fatalf("%d bytes written while the game was not idle", n)
				}
				c.pause(rr, false)
			}
			at := rr.untilWrite(gate.StableTicks + 4 + roomSettle/m5Frames + 2)
			if at < 0 {
				t.Fatalf("never written:\n%s", rr.log)
			}
			rr.ticks(2)
			if err := rr.bm.actorErr(); err != nil {
				t.Fatal(err)
			}
			if !sh.Open() {
				t.Fatalf("the shutter stayed shut: written after its %d executes\n%s", sh.Executes(), rr.log)
			}
			rr.audit()
		})
	}
}

// clockRig drives the room clocks alone: rooms 1 and 2 have zones 0 and
// 1, in Dragon Roost Cavern, IDLE.
type clockRig struct {
	t     *testing.T
	rs    m5Rooms
	s     state.Snapshot
	p     roomProbe
	st    gate.Status
	stage wire.Stage
}

func newClockRig(t *testing.T) *clockRig {
	st, err := wire.MakeStage(drc.Name)
	if err != nil {
		t.Fatal(err)
	}
	c := &clockRig{t: t, s: state.Snapshot{Table: addr.TableDRC, StayNo: 1}, p: roomProbe{ok: true, stay: 1}, st: gate.Status{Idle: true}, stage: st}
	for room := range c.s.ZoneNo {
		c.s.ZoneNo[room] = -1
	}
	c.zone(1, 0)
	c.zone(2, 1)
	return c
}

func (c *clockRig) zone(room, slot int) {
	c.s.ZoneNo[room] = int8(slot)
	c.s.Save[addr.InfoOffZone+slot*addr.ZoneSize+addr.ZoneOffRoomNo] = byte(room)
}

// tick is a tick frames after the last.
func (c *clockRig) tick(frames uint32) {
	c.s.Heartbeat += frames
	c.rs.observe(&c.s, c.stage, c.st, &c.p)
}

// TestM5RoomClocks: a room's clock counts the heartbeat's frames between
// two IDLE ticks, a tick's worth at most, up to roomSettle, and starts
// over when its zone slot, its scene or its status flags 0x01-0x08 change;
// every clock does at a save state, a failed probe or another stage or
// save table. A hidden room's does not run, and no dan switch waits for
// it.
func TestM5RoomClocks(t *testing.T) {
	t.Run("counting", func(t *testing.T) {
		c := newClockRig(t)
		c.tick(0)
		for i := 0; i < roomSettle/3; i++ {
			if c.rs.settled(1) || c.rs.young() != 1 {
				t.Fatalf("settled after %d frames", 3*i)
			}
			c.tick(3)
		}
		if !c.rs.settled(1) || !c.rs.settled(2) || c.rs.young() != -1 {
			t.Fatalf("not settled after %d frames: %+v", roomSettle, c.rs.room[1])
		}
	})
	for _, c := range []struct {
		name   string
		change func(c *clockRig)
		frames uint32 // the tick after the change is this many frames on
		one    bool   // room 1 still settled after it
		two    bool   // room 2
		young  int
	}{
		{name: "nothing changes", change: func(*clockRig) {}, frames: 3, one: true, two: true, young: -1},
		{name: "room 1's zone moves", change: func(c *clockRig) { c.zone(1, 5) }, frames: 3, two: true, young: 1},
		{name: "room 1 hidden", change: func(c *clockRig) { c.p.flags[1] |= roomHidden }, frames: 3, two: true, young: -1},
		{name: "room 1 loading", change: func(c *clockRig) { c.p.flags[1] |= 0x02 }, frames: 3, two: true, young: 1},
		{name: "room 1's collision", change: func(c *clockRig) { c.p.flags[1] |= 0x10 }, frames: 3, one: true, two: true, young: -1},
		{name: "a new scene of room 2", change: func(c *clockRig) { c.p.proc[2]++ }, frames: 3, one: true, young: 2},
		{name: "room 2 loses its zone", change: func(c *clockRig) { c.s.ZoneNo[2] = -1 }, frames: 3, one: true, young: -1},
		{name: "a save state", change: func(c *clockRig) { c.st.Regressed = true }, frames: 3, young: 1},
		{name: "a failed probe", change: func(c *clockRig) { c.p.ok = false }, frames: 3, young: 1},
		{name: "another stage", change: func(c *clockRig) { c.stage, _ = wire.MakeStage(drcBoss.Name) }, frames: 3, young: 1},
		{name: "another save table", change: func(c *clockRig) { c.s.Table = addr.TableFW }, frames: 3, young: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newClockRig(t)
			for range roomSettle/3 + 1 {
				r.tick(3)
			}
			c.change(r)
			r.tick(c.frames)
			if got := [2]bool{r.rs.settled(1), r.rs.settled(2)}; got != [2]bool{c.one, c.two} {
				t.Fatalf("settled %v, want %v", got, [2]bool{c.one, c.two})
			}
			if y := r.rs.young(); y != c.young {
				t.Fatalf("young() = %d, want %d", y, c.young)
			}
		})
	}
	for _, c := range []struct {
		name string
		run  func(c *clockRig)
		want int // room 1's frames after it
	}{
		{"a frame counts between two IDLE ticks", func(c *clockRig) {
			c.tick(3)
			c.st.Idle = false
			c.tick(3) // not IDLE: its frames do not count
			c.st.Idle = true
			c.tick(3) // the tick before was not IDLE
			c.tick(3)
		}, 6},
		{"a gap longer than a tick adds nothing", func(c *clockRig) {
			c.tick(3)
			c.tick(maxRoomBeat + 1)
			c.tick(maxRoomBeat)
		}, 3 + int(maxRoomBeat)},
		{"a hidden room's does not run", func(c *clockRig) {
			c.p.flags[1] |= roomHidden
			c.tick(3)
			c.tick(3)
		}, 0},
		{"a heartbeat that does not move adds nothing", func(c *clockRig) {
			c.tick(3)
			c.tick(0)
		}, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newClockRig(t)
			r.tick(0)
			c.run(r)
			if got := r.rs.room[1].frames; got != c.want {
				t.Fatalf("room 1's clock at %d frames, want %d", got, c.want)
			}
		})
	}
}

// TestReconcileM5DanEverHadPerTable: the game keeps one mDan, for the save
// table initDan last ran for (d_save.cpp:1225-1235), so a dan bit the
// receiver's game had and then lost to initDan is not written again in
// another stage of the table either, where the partner holds it; a bit it
// never had is.
func TestReconcileM5DanEverHadPerTable(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
	rr.send(danMsg(t, drc, danOK))
	rr.settleM5()
	if !rr.is(danOK, 1) {
		t.Fatalf("not written:\n%s", rr.log)
	}
	// Out to another table and back: initDan clears mDan.
	rr.splitLoad(woods)
	rr.settleM5()
	rr.splitLoad(drcKeys)
	rr.settleM5()
	if rr.is(danOK, 1) {
		t.Fatal("initDan kept the bit")
	}
	n := len(rr.bm.written())
	// Into the boss stage of the same table, the partner there with it.
	rr.splitLoad(drcBoss)
	rr.partnerAt(drcBoss, 0)
	rr.settleM5()
	rr.send(danMsg(t, drcBoss, danOK, danOK2))
	rr.settleM5()
	if w := len(rr.bm.written()) - n; w != 1 || rr.is(danOK, 0) || !rr.is(danOK2, 0) {
		t.Fatalf("%d bytes written in %s (0x%02X %v, 0x%02X %v): want only the bit the table never had\n%s",
			w, drcBoss.Name, danOK, rr.is(danOK, 0), danOK2, rr.is(danOK2, 0), rr.log)
	}
	rr.audit()
}

// TestM5EverHadBound: at the bound the ever-had set drops other stages'
// and tables' scopes first, and keeps the dan bits of the table Link is
// in, which every stage of it reads.
func TestM5EverHadBound(t *testing.T) {
	stage := func(name string) wire.Stage {
		st, err := wire.MakeStage(name)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	m := newM5()
	dan := scopeKey{kind: wire.ScopeDan, stage: stage(drc.Name), table: addr.TableDRC, room: -1}
	zone := scopeKey{kind: wire.ScopeZone, stage: stage(drcBoss.Name), table: addr.TableDRC, room: 0}
	m.had(dan, [2]uint32{1, 0})
	m.had(zone, [2]uint32{4, 0})
	for i := 0; len(m.ever) < maxM5Ever; i++ {
		table := int8(addr.TableDRC)
		if i%2 == 1 {
			table = addr.TableFW
		}
		m.had(scopeKey{kind: wire.ScopeZone, stage: stage(fmt.Sprintf("Z%06d", i)), table: table, room: int8(i % addr.RoomCount)}, [2]uint32{1, 0})
	}
	m.had(scopeKey{kind: wire.ScopeZone, stage: stage(drcBoss.Name), table: addr.TableDRC, room: 1}, [2]uint32{2, 0})
	if got := m.everHad(scopeKey{kind: wire.ScopeDan, stage: stage(drcBoss.Name), table: addr.TableDRC, room: -1}); got != [2]uint32{1, 0} {
		t.Fatalf("the table's dan bits, read from another stage of it: %v", got)
	}
	if got := m.everHad(zone); got != [2]uint32{4, 0} {
		t.Fatalf("the stage's own zone bits: %v", got)
	}
	if len(m.ever) != 3 {
		t.Fatalf("%d scopes kept at the bound, want 3", len(m.ever))
	}
}

// TestReconcileM5MirrorInScope: the receiver keeps a dan or zone store
// only while it is in the store's scope, as the hub does (world.Scopes):
// one that came while the game was not idle is dropped when Link leaves
// the room (or, for a dan store, the stage and table), the stage reloads,
// he goes to the title, or the session ends, and is not written on a
// later visit unless the hub sends it again.
func TestReconcileM5MirrorInScope(t *testing.T) {
	for _, c := range []struct {
		name  string
		leave func(rr *m5Rig) // and back
		dan   bool            // the dan store is dropped too
	}{
		{"out of the room until its zone is freed", func(rr *m5Rig) {
			for _, r := range [][2]int{{2, 1}, {3, 2}, {4, 3}} {
				rr.enter(r[0], r[1])
				rr.ticks(2)
			}
			if z := rr.slot(1); z >= 0 {
				rr.t.Fatalf("room 1 still has zone %d", z)
			}
			rr.enter(3, 4)
			rr.enter(2, 3)
			rr.enter(1, 2)
		}, false},
		{"out of the room and back", func(rr *m5Rig) {
			rr.enter(2, 1)
			rr.ticks(2)
			rr.enter(1, 2)
		}, false},
		{"a title trip", func(rr *m5Rig) {
			rr.g.EnterFileSelect()
			rr.settleM5()
			if err := rr.g.LoadStage(drcKeys); err != nil {
				rr.t.Fatal(err)
			}
		}, true},
		// The same stage again: Link is gone for the load, and his
		// PRESENCE says he is in no stage (hub.placeOf).
		{"the stage reloaded", func(rr *m5Rig) { rr.splitLoad(drcKeys) }, true},
		{"another table and back", func(rr *m5Rig) {
			rr.splitLoad(woods)
			rr.ticks(2)
			rr.splitLoad(drcKeys)
		}, true},
		{"a reconnect", func(rr *m5Rig) {
			rr.r.Disconnect()
			rr.ticks(2)
			rr.r.Connect(rr.hub)
			rr.ticks(gate.StableTicks + 2)
			rr.hub.take()
			rr.welcome(m5Features)
			rr.partnerAt(drc, 1)
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
			rr.g.SetEvent(1, 0)
			rr.ticks(2)
			rr.send(zoneMsg(t, drc, 1, zoneOK), danMsg(t, drc, danOK)) // the partner's, as the receiver walks out
			rr.ticks(2)
			c.leave(rr)
			rr.g.SetEvent(0, 0)
			rr.settleM5()
			if rr.is(zoneOK, 1) || c.dan && rr.is(danOK, 1) {
				t.Fatalf("a store of an earlier visit was written (zone %v, dan %v)\n%s", rr.is(zoneOK, 1), rr.is(danOK, 1), rr.log)
			}
			// The hub sends the stores again as the receiver's PRESENCE
			// enters their scopes: written.
			rr.send(zoneMsg(t, drc, 1, zoneOK), danMsg(t, drc, danOK))
			rr.settleM5()
			if !rr.is(zoneOK, 1) || !rr.is(danOK, 1) {
				t.Fatalf("the stores the hub sent again were not written\n%s", rr.log)
			}
			rr.audit()
		})
	}
}

// TestReconcileM5RoomBounce: Link out of the room and back between two
// ticks (1 -> 2 -> 1): zoneCountCheck clears every zone's room switches
// twice and leaves the slot and mStayNo as they were (d_stage.cpp:250-267).
// The receiver sees it by mOldStayNo, which setStayNo moves
// (d_stage.cpp:171-173), or by a room switch of the visit gone: an edge
// waiting is dropped, not replayed, and a room switch written before it is
// not judged a failed write. Back into the room he came from, with no room
// switch set, nothing a tick reads shows the bounce, and an edge waiting
// across it is written (Appendix C item 124).
func TestReconcileM5RoomBounce(t *testing.T) {
	// Link in room 1, having come from room 1 (mOldStayNo 1) or room 2.
	from := func(rr *m5Rig, old int) {
		rr.t.Helper()
		if old != 1 {
			rr.enter(old, 1)
			rr.settleM5()
		}
		rr.enter(1, 2)
		rr.settleM5()
	}
	bounce := func(rr *m5Rig) {
		rr.enter(2, 1)
		rr.enter(1, 2)
	}
	t.Run("an edge waiting is dropped", func(t *testing.T) {
		for _, c := range []struct {
			name string
			old  int
			mine int // a room switch the receiver's game set itself, or 0
		}{
			{"mOldStayNo moves", 1, 0},
			{"a room switch of the visit gone", 2, roomOK2},
		} {
			t.Run(c.name, func(t *testing.T) {
				rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
				from(rr, c.old)
				if c.mine != 0 {
					rr.onSwitch(c.mine, 1)
				}
				rr.g.SetEvent(1, 0)
				rr.ticks(2)
				rr.send(edgeMsg(t, drc, 1, roomOK))
				rr.ticks(1)
				bounce(rr)
				rr.g.SetEvent(0, 0)
				rr.settleM5()
				if rr.is(roomOK, 1) {
					t.Fatalf("an edge from before the bounce was written after it\n%s", rr.log)
				}
				if st := rr.r.Status().Puzzle; st.Dropped != 1 {
					t.Fatalf("dropped %d edges, want 1", st.Dropped)
				}
			})
		}
	})
	t.Run("one no tick sees", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		from(rr, 2)
		rr.g.SetEvent(1, 0)
		rr.ticks(2)
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.ticks(1)
		bounce(rr)
		rr.g.SetEvent(0, 0)
		rr.settleM5()
		if !rr.is(roomOK, 1) {
			t.Fatalf("the edge was not written: the bounce is seen after all, so Appendix C item 124 is out of date\n%s", rr.log)
		}
	})
	t.Run("a room switch written before it is not judged", func(t *testing.T) {
		for _, c := range []struct {
			old   int
			after int // a room switch the game sets after the bounce, or 0
		}{
			{1, 0}, {1, roomOK2},
			// From room 2 only the cleared word shows it; with another room
			// switch set after it, the write would be judged a failure
			// (Appendix C item 124).
			{2, 0},
		} {
			t.Run(fmt.Sprintf("from room %d, then 0x%02X", c.old, c.after), func(t *testing.T) {
				rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
				from(rr, c.old)
				rr.send(edgeMsg(t, drc, 1, roomOK))
				if at := rr.untilWrite(gate.StableTicks + 4); at < 0 || !rr.is(roomOK, 1) {
					t.Fatalf("the edge was not written\n%s", rr.log)
				}
				bounce(rr)
				if c.after != 0 {
					rr.onSwitch(c.after, 1)
				}
				rep := rr.tick()
				if rep.Verify.Failed != 0 || rep.Verify.Unverified != 1 {
					t.Fatalf("verify after the bounce: %+v", rep.Verify)
				}
				if st := rr.r.Status(); st.Writes.Failed != 0 || st.WritesOff != "" {
					t.Fatalf("verify failures %d, writes off %q", st.Writes.Failed, st.WritesOff)
				}
			})
		}
	})
}
