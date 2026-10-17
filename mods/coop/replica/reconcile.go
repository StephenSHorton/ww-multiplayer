package replica

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The reconciler (§4.2, §9 M3) turns the Planner's plan into game writes.
// It is level-triggered: every consistent armed tick, and so after every
// stage change, the plan is what the receiver's game lacks of the world
// (missing = desired &^ local &^ suppressed), and the reconciler hands
// the FrameWriter the part of it whose time has come:
//
//   - stage progress bits (chests, pickups, visited rooms, dungeon items
//     0-5) of the loaded table: when STABLE and IDLE (§3 "live"), to the
//     live memBit and the table's saved slot, live first, as
//     dComIfGs_onStageTbox does (d_com_inf_game.cpp:694-702). A chest or
//     pickup already in the room keeps what it read when it was created
//     (d_a_tbox.cpp:447-455, d_a_item.cpp:227-237): the bit shows from
//     the next time it is created. Dungeon items 3 and 5 (boss defeated,
//     boss demo seen) are the exception: a boss stage reads them every
//     frame (d_meter.cpp:1071, d_a_dr2.cpp:561, d_a_bdk.cpp:2741), so in
//     one they are held (bossHold) and written once Link has left it;
//   - stage progress bits and memory switches of any other table: when
//     STABLE, IDLE or not, to its saved slot only (Timing OnLoad): no
//     game code that runs under the pause menu writes such a slot, and
//     the game reads it whole only at that table's getSave (Appendix C
//     item 128);
//   - memory switches of the loaded table: on the first STABLE and IDLE
//     tick after the receiver's room or stage changes (§6.3; at least
//     gate.StableTicks ticks after it, as STABLE needs that many unchanged
//     ones) on which every shown room's actors have settled (the room
//     hold, reconcile_m5_rooms.go), live and saved; with
//     proto.FeatureLiveSwitches (--coop-live-switches) when IDLE and the
//     room hold allows instead. The hold is the M5 receiver's, for the
//     same reason: a shutter created less than 30 frames before ignores a
//     switch that changes (d_a_shutter.cpp:71-87, 133-142, 233-243), and
//     a memory switch reaches it as a dan or zone switch does
//     (dSv_info_c::isSwitch, d_save.cpp:1586-1603). A door creates the
//     next room's actors before Link's room changes (d_door.cpp:282,
//     d_s_room.cpp:72-76), and the next room change is the one that made
//     the write due;
//   - the ocean, the charts and the sea map: on the first STABLE and IDLE
//     tick after a room or stage change (Timing OnRoomChange). The sea
//     chart writes the charts and the sea map while the pause menu is
//     open (onSaveArriveGrid in its fishman mode, d_menu_fmap.cpp:3271;
//     onOpenCollectMap, d_menu_fmap2.cpp:1045 and 1691; calcGetCollectMap's
//     clears, d_menu_fmap2.cpp:3174-3191), each a word or byte
//     read-modify-write that a Go write into the same byte could race
//     (§4.2.1), so these wait for IDLE too (Appendix C item 129).
//
// Memory switches are written only while the hub grants
// proto.FeatureSwitches, and only those the policy opens (the M1 table's
// latching ones, proto.SwitchPolicy). Every byte goes through the
// WriteGuard, which refuses PER_PLAYER, NEVER, DERIVED and TAG bytes,
// writes while a gate is closed, the live boss bits in a boss stage and
// any clear. The other classes the planner plans (story bits, registers,
// inventory, capacities) are M4's: the M4 receiver writes them from their
// own plans (reconcile_m4.go), story.Pending's and inventory.Pending's,
// which the planner's plan agrees with but for the holds' details and the
// slots those flags derive.
//
// Why writing the loaded table both ways is safe across stage changes:
// getSave copies a table's saved slot over the live copy at a stage load
// (d_save.cpp:1506-1509, from dStage_stagInfoInit, d_stage.cpp:1675) and
// putSave copies the live copy back at the stage's delete
// (d_save.cpp:1512-1515, d_stage.cpp:2256), so with both copies written
// neither move loses a bit. Neither runs during a write: the FrameWriter
// writes only after its re-check found mNextStage.mEnable still clear,
// and a stage change reaches the old stage's delete and the new one's
// getSave only after the play scene's draw saw mEnable set and asked for
// the scene change behind a wipe (dScnPly_Draw, d_s_play.cpp:291-314;
// fopScnM_ChangeReq, f_op_scene_mng.cpp:18-27, with a peek time of 5).
// Should a write be lost all the same, a byte the game never clears (the
// stage progress bits, the ocean, the charts owned and the sea map) is
// planned again whenever it is missing (healing). The game does clear
// memory switches (offSwitch, d_save.cpp:1563-1583, from timers and
// per-frame writers, §6.1) and the charts deciphered and salvaged (when a
// chart is got, and by the sea chart menu): those are apply-once, the
// ever-had set keeping a bit the receiver's game had, or the reconciler
// wrote, from being written again.

// m3Category reports whether category c is one this version writes.
func m3Category(c Category) bool { return c == CatStage || c == CatSwitch || c == CatOcean }

// roomWatch tells the reconciler when the receiver changed room or stage,
// for the writes §6.3 defers to the next room change.
type roomWatch struct {
	have  bool // the last in-game tick's place is known
	stage string
	table int8
	room  int8
	// due: the room or stage changed (or a stage loaded) since the
	// deferred writes last had their tick. switches: the same for the
	// loaded table's memory switches, whose tick also waits for the room
	// hold, so it can come later.
	due, switches bool
}

// windows are the deferred writes' windows a tick's writes are made in.
type windows struct {
	room     bool // the room-change tick: the ocean, the charts and the sea map
	switches bool // the memory switches' tick: the room-change tick or, with the room hold, a later one
}

// changed makes the next room due, for every deferred write.
func (w *roomWatch) changed() { w.due, w.switches = true, true }

// observe takes one tick. A tick off the game, between stages or with a
// stage change pending makes the next room due (a stage is being loaded,
// with its rooms), as does an in-game tick whose stage, table or mStayNo
// differs from the last one's. The first tick seen in game, mid-room, is
// no change: what the world had before is written at the next one.
func (w *roomWatch) observe(o *obs) {
	s := o.s
	switch {
	case o.screen == screenOut:
		w.have = false
		w.changed()
		return
	case o.screen == screenNoLink, s.NextEnable != 0:
		w.changed()
		return
	}
	if w.have && (s.StageName != w.stage || s.Table != w.table || s.StayNo != w.room) {
		w.changed()
	}
	w.have, w.stage, w.table, w.room = true, s.StageName, s.Table, s.StayNo
}

// open reports which deferred writes o is the tick of. The room-change
// window is the first STABLE and IDLE tick after a room change. STABLE
// needs the stage and room unchanged for gate.StableTicks ticks, so it is
// at least that many ticks after the change, when the room is loaded
// (dStage_RoomCheck loads it as Link's ground check lands in it,
// d_stage.cpp:2264-2290). IDLE keeps the writes out of the pause menu,
// under which the sea chart writes the charts and the sea map, and out
// of events. The memory switches' window is the first STABLE and IDLE
// tick after it that the room hold allows (calm): every shown room's
// actors have had roomSettle IDLE frames since they were created. Until
// then the writes wait, still due, so none is lost.
func (w *roomWatch) open(o *obs, calm bool) windows {
	ok := o.consistent() && o.st.CanWrite() && o.st.Idle
	return windows{room: w.due && ok, switches: w.switches && ok && calm}
}

// close ends the windows win names: their writes had their tick.
func (w *roomWatch) close(win windows) {
	w.due = w.due && !win.room
	w.switches = w.switches && !win.switches
}

// pick splits the plan into the writes due this tick and the M3 writes
// that wait. win is the deferred writes' windows this tick opens, and
// calm whether the room hold allows a memory switch of the loaded table.
// Writes of classes this version does not write, and memory switches the
// hub does not grant, are in neither.
func (r *Replica) pick(o *obs, ws []Write, win windows, calm bool) (due, waiting []Write) {
	st := o.st
	for _, w := range ws {
		switch {
		case !m3Category(w.Category):
			continue
		case w.Category == CatSwitch && r.features&proto.FeatureSwitches == 0:
			continue
		}
		ok := false
		switch w.When {
		case Live:
			ok = st.CanWriteIdle()
		case OnLoad:
			ok = st.CanWrite()
		case OnRoomChange:
			switch {
			case w.Category != CatSwitch:
				ok = win.room
			case r.features&proto.FeatureLiveSwitches != 0:
				ok = st.CanWriteIdle() && calm
			default:
				ok = win.switches
			}
		}
		if ok {
			due = append(due, w)
		} else {
			waiting = append(waiting, w)
		}
	}
	return due, waiting
}

// reconcile picks this tick's writes from the plan (plan has run, and
// m5Observe, so the rooms' clocks are this tick's) into r.toWrite
// (r.window: the deferred writes' windows they are made in), for Tick to
// make once the lock is released, and logs what waits. A dry run's plan
// was logged by plan. A window closes on a tick that has nothing to
// write in it, and on a STABLE one the session is not armed in, IDLE or
// not: a room change seen before the session armed, or before the
// world's view came, is not one the deferred writes may use (they wait
// for the next).
func (r *Replica) reconcile(o *obs, armed bool, rep *Report) {
	calm := r.m5.rooms.calm()
	win := r.room.open(o, calm)
	switch {
	case r.writer.DryRun():
		return
	case !armed || !r.haveView || r.viewWorld != r.sessionWorld:
		r.loggedOR, r.loggedMax, r.loggedHeld = world.BitSet{}, world.Image{}, world.BitSet{}
		// The memory switches' window, still waiting for the hold, goes
		// with the room change's: the change is not theirs either. Both
		// close on the first STABLE tick, IDLE or not, so a change seen
		// unarmed while the pause menu was open is dropped as well.
		seen := o.consistent() && o.st.CanWrite()
		r.room.close(windows{room: r.room.due && seen, switches: r.room.due && seen || r.room.switches && seen && calm})
		return
	case !o.consistent():
		return // the last plan stays, and waits for a tick it can trust
	}
	due, waiting := r.pick(o, r.planned, win, calm)
	r.toWrite = due
	rep.Waiting, rep.RoomChange, rep.SwitchWindow = len(waiting), win.room, win.switches
	r.logWaits(r.planned, waiting, r.held, o, calm)
	if len(due) == 0 {
		r.room.close(win)
	} else {
		r.window = win
	}
}

// applied records what this tick's writes did: the bits written go into
// the planner's ever-had set (so an apply-once bit the game clears before
// the next tick reads it is still never written again), they are logged,
// and the windows win they were made in close unless nothing could be
// tried (the heartbeat stalled, or the game moved on), when the next
// STABLE tick is still the one right after the change (or the hold).
func (r *Replica) applied(res Result, err error, win windows, rep *Report) {
	rep.Wrote, rep.WriteErr = len(res.Wrote), err
	for _, b := range res.Wrote {
		r.planner.wrote(int(b.Off), b.New&^b.Old)
		if b.Addr >= addr.LiveMemBit && b.Addr < addr.LiveMemBit+addr.MemBitSize {
			// A key door there keeps the lock it read until Link's next
			// room change: the Deriver counts it from then (derive/keys.go).
			r.m4.deriver.WroteLive(int(b.Off), b.New&^b.Old)
		}
	}
	if !errors.Is(err, ErrStall) && !errors.Is(err, ErrAborted) {
		r.room.close(win)
	}
	r.logWrites(res)
}

// logWrites logs each bit a batch set.
func (r *Replica) logWrites(res Result) {
	if len(res.Wrote) == 0 {
		return
	}
	saved := map[uint16]bool{}
	for _, b := range res.Wrote {
		if b.Addr == addr.SaveImage+uint32(b.Off) {
			saved[b.Off] = true
		}
	}
	var lines []string
	for _, b := range res.Wrote {
		isSaved := b.Addr == addr.SaveImage+uint32(b.Off)
		if !isSaved && saved[b.Off] {
			continue // said with its saved byte
		}
		where := fmt.Sprintf("0x%08X", b.Addr)
		switch {
		case isSaved && r.wroteLive(res, b.Off):
			where += ", and its live copy"
		case !isSaved:
			where = "the live copy at " + where
		}
		for set := b.New &^ b.Old; set != 0; set &= set - 1 {
			bit := bits.TrailingZeros8(set)
			lines = append(lines, fmt.Sprintf("set %s (%s |= 0x%02X)", r.label(int(b.Off), bit), where, byte(1)<<bit))
		}
	}
	r.logf("co-op: wrote %d bytes of the world after heartbeat %d", len(res.Wrote), res.Beat)
	r.logLines(lines)
}

// wroteLive reports whether res wrote the live copy of image byte off.
func (r *Replica) wroteLive(res Result, off uint16) bool {
	for _, b := range res.Wrote {
		if b.Off == off && b.Addr != addr.SaveImage+uint32(off) {
			return true
		}
	}
	return false
}

// logWaits logs, once while it stays so, each held bit and each M3 bit
// that waits for the receiver's next room or stage change, for the room
// hold (calm: the hold allows the loaded table's memory switches), or,
// once the room change it waits for is in, for the game to be IDLE (o:
// the tick, whose gates say why the game is not). A stage change is in
// only once the new stage has loaded: while mNextStage is set, the write
// also waits for the load and the new stage's STABLE, so it logs that it
// waits for the stage change. One logged as waiting for the room change
// is not logged again when, after the change, it waits for STABLE or
// IDLE. One whose room change is in, with the game IDLE, gets no line: it
// waits only for STABLE, which opens within ticks, or for a stalled
// heartbeat. The planned bits of M4's classes are the M4 receiver's to
// write and log (reconcile_m4.go), from their own plans.
func (r *Replica) logWaits(planned, waiting []Write, held []HeldBit, o *obs, calm bool) {
	st := o.st
	var lines []string
	var nowOR, nowHeld world.BitSet
	var nowMax world.Image
	for _, h := range held {
		nowHeld[h.Off] |= h.Mask
		if fresh := h.Mask &^ r.loggedHeld[h.Off]; fresh != 0 {
			lines = append(lines, fmt.Sprintf("holding %s back: %s", r.label(int(h.Off), bits.TrailingZeros8(fresh)), h.Hold.Why))
		}
	}
	note := func(w Write, line func(bit int) string) {
		off := int(w.Off)
		if w.Max {
			nowMax[off] = w.Bits
			if r.loggedMax[off] != w.Bits {
				lines = append(lines, line(-1))
			}
			return
		}
		nowOR[off] |= w.Bits
		for set := w.Bits &^ r.loggedOR[off]; set != 0; set &= set - 1 {
			lines = append(lines, line(bits.TrailingZeros8(set)))
		}
	}
	// keep carries the bits of w logged already into this tick's sets,
	// with no line, so they are not logged again while they wait.
	keep := func(w Write) {
		off := int(w.Off)
		if w.Max {
			if r.loggedMax[off] == w.Bits {
				nowMax[off] = w.Bits
			}
			return
		}
		nowOR[off] |= w.Bits & r.loggedOR[off]
	}
	live := r.features&proto.FeatureLiveSwitches != 0
	for _, w := range waiting {
		// in: the room change w waits for is in, so its window waits for
		// the gates alone (roomWatch.open); for a memory switch of the
		// loaded table, the room hold has passed as well. roomWatch makes
		// the next room due as soon as mNextStage is set, but a stage
		// change still pending is not in: the stage has yet to load.
		in := r.room.due && o.s.NextEnable == 0
		if w.Category == CatSwitch {
			in = r.room.switches && calm && o.s.NextEnable == 0
		}
		why := "waits for the next room or stage change"
		switch {
		case w.When != OnRoomChange:
			continue // its gate opens within ticks
		case w.Category == CatSwitch && !calm && (live || r.room.switches):
			why = "waits for the room hold: every shown room's actors get 2 s of idle frames first, as a new shutter ignores a switch set in its first 30 (§6.1)"
		case w.Category == CatSwitch && live:
			continue // IDLE, within ticks
		case in && !st.Idle:
			why = fmt.Sprintf("waits for this game to be idle (%s): its room change is in", st.IdleWhy)
		case in:
			keep(w) // STABLE, within ticks
			continue
		}
		note(w, func(bit int) string {
			return fmt.Sprintf("%s %s", r.label(int(w.Off), bit), why)
		})
	}
	r.loggedOR, r.loggedMax, r.loggedHeld = nowOR, nowMax, nowHeld
	r.logLines(lines)
}

// logLines logs lines, at most maxPlanLines of them.
func (r *Replica) logLines(lines []string) {
	for i, l := range lines {
		if i == maxPlanLines {
			r.logf("co-op: ... and %d more", len(lines)-maxPlanLines)
			break
		}
		r.logf("co-op: %s", l)
	}
}

// TurnOff is "coop off": no game write is made once it returns (a batch
// under way stops before its next byte) until TurnOn. The session goes on
// detecting, proposing and planning, and Status says why writes are off.
// A dry run writes nothing anyway.
func (r *Replica) TurnOff(why string) {
	if why == "" {
		why = "co-op was turned off"
	}
	r.writer.Stop(why)
}

// TurnOn is "coop on": writes resume after TurnOff or an auto-disable
// (§4.2), from the next tick.
func (r *Replica) TurnOn() {
	if wg := r.writer.WriteGuard(); wg != nil {
		wg.Breaker().Reset()
	}
}

// Writer returns the FrameWriter the replica writes through: a dry run
// unless Config.Apply.
func (r *Replica) Writer() *FrameWriter { return r.writer }
