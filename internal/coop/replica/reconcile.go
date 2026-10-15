package replica

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
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
//     STABLE, to its saved slot only (Timing OnLoad);
//   - memory switches of the loaded table: on the first STABLE tick after
//     the receiver's room or stage changes (§6.3; at least
//     gate.StableTicks ticks after it, as STABLE needs that many unchanged
//     ones), live and saved; with hub.FeatureLiveSwitches
//     (--coop-live-switches) when IDLE instead;
//   - the ocean, the charts and the sea map: on that same first STABLE
//     tick after a room or stage change (Timing OnRoomChange).
//
// Memory switches are written only while the hub grants
// hub.FeatureSwitches, and only those the policy opens (the M1 table's
// latching ones, hub.SwitchPolicy). Every byte goes through the
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
	// deferred writes last had their tick.
	due bool
}

// observe takes one tick. A tick off the game, between stages or with a
// stage change pending makes the next room due (a stage is being loaded,
// with its rooms), as does an in-game tick whose stage, table or mStayNo
// differs from the last one's. The first tick seen in game, mid-room, is
// no change: what the world had before is written at the next one.
func (w *roomWatch) observe(o *obs) {
	s := o.s
	switch {
	case o.screen == screenOut:
		w.have, w.due = false, true
		return
	case o.screen == screenNoLink, s.NextEnable != 0:
		w.due = true
		return
	}
	if w.have && (s.StageName != w.stage || s.Table != w.table || s.StayNo != w.room) {
		w.due = true
	}
	w.have, w.stage, w.table, w.room = true, s.StageName, s.Table, s.StayNo
}

// open reports whether o is the tick the deferred writes are made in:
// the first STABLE tick after a room change. STABLE needs the stage and
// room unchanged for gate.StableTicks ticks, so it is at least that many
// ticks after the change, when the room is loaded (dStage_RoomCheck loads
// it as Link's ground check lands in it, d_stage.cpp:2264-2290).
func (w *roomWatch) open(o *obs) bool {
	return w.due && o.consistent() && o.st.CanWrite()
}

// pick splits the plan into the writes due this tick and the M3 writes
// that wait. window is whether this is the room-change tick. Writes of
// classes this version does not write, and memory switches the hub does
// not grant, are in neither.
func (r *Replica) pick(o *obs, ws []Write, window bool) (due, waiting []Write) {
	st := o.st
	for _, w := range ws {
		switch {
		case !m3Category(w.Category):
			continue
		case w.Category == CatSwitch && r.features&hub.FeatureSwitches == 0:
			continue
		}
		ok := false
		switch w.When {
		case Live:
			ok = st.CanWriteIdle()
		case OnLoad:
			ok = st.CanWrite()
		case OnRoomChange:
			if w.Category == CatSwitch && r.features&hub.FeatureLiveSwitches != 0 {
				ok = st.CanWriteIdle()
			} else {
				ok = window
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

// reconcile picks this tick's writes from the plan (plan has run) into
// r.toWrite (r.window: they are the room-change window's), for Tick to
// make once the lock is released, and logs what
// waits. A dry run's plan was logged by plan. The room-change window
// closes on a tick that has nothing to write in it, and on one the
// session is not armed in: a room change seen before the session armed,
// or before the world's view came, is not one the deferred writes may
// use (they wait for the next).
func (r *Replica) reconcile(o *obs, armed bool, rep *Report) {
	window := r.room.open(o)
	switch {
	case r.writer.DryRun():
		return
	case !armed || !r.haveView || r.viewWorld != r.sessionWorld:
		r.loggedOR, r.loggedMax, r.loggedHeld = world.BitSet{}, world.Image{}, world.BitSet{}
		if window {
			r.room.due = false
		}
		return
	case !o.consistent():
		return // the last plan stays, and waits for a tick it can trust
	}
	due, waiting := r.pick(o, r.planned, window)
	r.toWrite, r.window = due, window && len(due) > 0
	rep.Waiting, rep.RoomChange = len(waiting), window
	r.logWaits(r.planned, waiting, r.held)
	if window && len(due) == 0 {
		r.room.due = false
	}
}

// applied records what this tick's writes did: the bits written go into
// the planner's ever-had set (so an apply-once bit the game clears before
// the next tick reads it is still never written again), they are logged,
// and the room-change window closes unless nothing could be tried (the
// heartbeat stalled, or the game moved on), when the next STABLE tick is
// still the one right after the change.
func (r *Replica) applied(res Result, err error, window bool, rep *Report) {
	rep.Wrote, rep.WriteErr = len(res.Wrote), err
	for _, b := range res.Wrote {
		r.planner.wrote(int(b.Off), b.New&^b.Old)
	}
	if window && !errors.Is(err, ErrStall) && !errors.Is(err, ErrAborted) {
		r.room.due = false
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
// that waits for the receiver's next room or stage change. The planned
// bits of M4's classes are the M4 receiver's to write and log
// (reconcile_m4.go), from their own plans.
func (r *Replica) logWaits(planned, waiting []Write, held []HeldBit) {
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
	for _, w := range waiting {
		if w.When != OnRoomChange {
			continue // its gate opens within ticks
		}
		note(w, func(bit int) string {
			return fmt.Sprintf("%s waits for the next room or stage change", r.label(int(w.Off), bit))
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
