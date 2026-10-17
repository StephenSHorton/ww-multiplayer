package replica

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The kill path (§7.2, M6): a partner's recorded deaths reach the
// receiver's zones only through FrameWriter.ApplyKills, the one call the
// WriteGuard lets write a zone's dead-actor byte (pathKill). The game sets
// a dead-actor bit with a u32 read-modify-write (dSv_zoneActor_c::on,
// d_save.cpp:1326-1329), so each write is one byte ORed in, as the scoped
// path's are.
//
// Unlike a zone switch, a dead-actor bit is written into the zone of any
// loaded room of the stage, not only Link's: a hidden neighbour's zone
// written now keeps its dead enemies from being created when a door shows
// it (dStage_actorInit checks isActor as it creates a room's entries,
// d_stage.cpp:1688-1708). A room being created or deleted is left (its
// status flags 0x02, 0x04, d_stage.cpp:214-240): its zone may go or come
// within the write.

// KillWrite is one byte of the kill path: Bits to OR into the dead-actor
// words of zone Slot, the zone of Room.
type KillWrite struct {
	Addr       uint32
	Bits       byte
	Room, Slot int8
}

// killWriteOf is the KillWrite that sets setID's bit in the zone in slot
// of room.
func killWriteOf(room, slot, setID int) (KillWrite, error) {
	bit, err := addr.ZoneActorBit(setID)
	if err != nil {
		return KillWrite{}, err
	}
	base, err := addr.ZoneAddr(slot)
	if err != nil {
		return KillWrite{}, err
	}
	if room < 0 || room >= addr.RoomCount {
		return KillWrite{}, fmt.Errorf("replica: room %d has no zone", room)
	}
	return KillWrite{Addr: base + uint32(bit.Off), Bits: bit.Mask, Room: int8(room), Slot: int8(slot)}, nil
}

// SetIDs lists the setIDs kw sets, ascending; nil if kw is no dead-actor
// byte of its slot.
func (kw KillWrite) SetIDs() []int {
	base, err := addr.ZoneAddr(int(kw.Slot))
	if err != nil || kw.Addr < base+addr.ZoneOffActor || kw.Addr >= base+addr.ZoneSize {
		return nil
	}
	return killSetIDs(kw.Addr, int(kw.Slot), kw.Bits)
}

func (kw KillWrite) String() string {
	var ids []string
	for _, id := range kw.SetIDs() {
		ids = append(ids, fmt.Sprintf("%d", id))
	}
	return fmt.Sprintf("dead setID %s in room %d (zone slot %d) (0x%08X |= 0x%02X)", strings.Join(ids, ", "), kw.Room, kw.Slot, kw.Addr, kw.Bits)
}

// killShape returns why kw is no write of the kill path, or "": a byte of
// zone Slot's dead-actor words, Slot the zone of Room by the tick's zone
// table, and bits to set. w.wg.mu is not held.
func (w *FrameWriter) killShape(kw KillWrite) error {
	g := w.wg
	g.mu.Lock()
	e := &RefusalError{Addr: kw.Addr, New: kw.Bits, What: "zone dead-actor byte"}
	base, err := addr.ZoneAddr(int(kw.Slot))
	switch {
	case g.brk.Off():
		e.Reason, e.Why = RefuseOff, g.brk.Why()
	case err != nil || kw.Room < 0 || int(kw.Room) >= addr.RoomCount:
		e.Reason, e.Why = RefuseTarget, fmt.Sprintf("room %d in zone slot %d", kw.Room, kw.Slot)
	case kw.Addr < base+addr.ZoneOffActor || kw.Addr >= base+addr.ZoneSize:
		e.Reason, e.Why = RefuseTarget, fmt.Sprintf("0x%08X is no dead-actor byte of zone slot %d", kw.Addr, kw.Slot)
	case kw.Bits == 0:
		e.Reason, e.Why = RefuseValue, "it sets no bit"
	default:
		zt := &g.tick.Zones
		if z, ok := zt.SlotOf(int(kw.Room)); !g.have || !ok || z != int(kw.Slot) {
			e.Reason, e.Why = RefuseTarget, fmt.Sprintf("room %d's zone is not slot %d by this tick's zone table", kw.Room, kw.Slot)
		} else if _, br, reason := g.rule(kw.Addr); reason != 0 || br.paths&pathKill == 0 {
			e.Reason, e.Why = RefusePath, "it is not the kill path's byte"
		}
	}
	if e.Reason == 0 {
		g.mu.Unlock()
		return nil
	}
	g.checked++
	return g.refuse(e)
}

// pendingKill is a dead-actor byte waiting for its verify.
type pendingKill struct {
	kw       KillWrite
	old, val byte
}

// ApplyKills makes the kill path's writes, ks (bytes at one address
// joined), right after the next heartbeat:
//
//  1. before the wait, each byte is checked to be a dead-actor byte of the
//     zone the tick's table gives its room (killShape), and the gates
//     (IDLE, STABLE, HEARTBEAT, ARMED) to be open;
//  2. the heartbeat is followed from the snapshot's (waitBeat), then the
//     stage, mNextStage.mEnable, the loaded table and file (recheck) and
//     IDLE's inputs (recheckIdle) are read again: a change is ErrAborted;
//  3. for each byte: the room's zone and status read again (killHolds), a
//     fresh 1-byte read, the bits ORed in, WriteGuard.Check as the kill
//     path (Tick.killClosed: a loaded room's zone, the plan's setIDs), a
//     1-byte write. A byte already holding the bits is left alone;
//  4. the next Observe checks each written byte, if the stage, table and
//     file are the same and the room's zone is still the slot.
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed, ErrStall, ErrAborted, the first refusal (ErrGuard), ctx's
// error, or a memory error.
func (w *FrameWriter) ApplyKills(ctx context.Context, ks []KillWrite) (res Result, err error) {
	if w.wg == nil {
		return res, ErrDryRun
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.wg.brk.Err(); err != nil {
		return res, err
	}
	defer func() {
		if len(res.Wrote) > 0 {
			w.stats.Batches++
		}
		w.stats.Written += len(res.Wrote)
		w.stats.Kills += len(res.Wrote)
		w.stats.Already += res.Already
		w.stats.Refused += res.Refused
	}()
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}

	var bs []KillWrite
	at := map[uint32]int{}
	for _, kw := range ks {
		if i, ok := at[kw.Addr]; ok && bs[i].Room == kw.Room && bs[i].Slot == kw.Slot {
			bs[i].Bits |= kw.Bits
			continue
		}
		at[kw.Addr] = len(bs)
		bs = append(bs, kw)
	}
	open := bs[:0]
	var closed error
	for _, kw := range bs {
		if err := w.killShape(kw); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			res.Refused++
			note(err)
			continue
		}
		if err := w.wg.ready(kw.Addr); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			res.Closed++
			if closed == nil {
				closed = err
			}
			continue
		}
		open = append(open, kw)
	}
	if bs = open; len(bs) == 0 {
		if first == nil {
			first = closed
		}
		return res, first
	}

	hb, waited, err := w.waitBeat(ctx)
	res.Waited = waited
	if err == nil {
		err = w.recheck()
	}
	if err == nil {
		err = w.recheckIdle()
	}
	switch {
	case errors.Is(err, ErrStall):
		w.stats.Stalls++
		return res, err
	case errors.Is(err, ErrAborted):
		w.stats.Aborted++
		w.wg.Close(err.Error())
		return res, err
	case err != nil:
		return res, err
	}
	res.Beat = hb

	for _, kw := range bs {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		if err := w.killHolds(kw.Room, kw.Slot); err != nil {
			if errors.Is(err, ErrAborted) {
				w.stats.Aborted++
				w.wg.Close(err.Error())
			}
			note(err)
			break
		}
		old, err := mem.ReadU8(w.Mem, kw.Addr)
		if err != nil {
			note(err)
			break
		}
		val := old | kw.Bits
		if val == old {
			res.Already++
			continue
		}
		if err := w.wg.check(kw.Addr, old, val, pathKill); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			res.Refused++
			note(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, kw.Addr, val); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: kw.Addr, Off: noOff, Old: old, New: val})
		if len(w.kills) == 0 {
			w.kiAt = w.tick
		}
		w.kills = append(w.kills, pendingKill{kw: kw, old: old, val: val})
	}
	return res, first
}

// killHolds reads again what a dead-actor write into zone slot of room
// rests on, and returns ErrAborted if the tick's snapshot no longer holds:
// the room's mZoneNo is slot, the slot's mRoomNo is room, and the room is
// loaded and neither being created nor deleted. removeZone frees a slot by
// its mRoomNo alone and createZone wipes the slot it reuses
// (d_save.h:969; d_save.cpp:1338-1342, :1525-1537).
func (w *FrameWriter) killHolds(room, slot int8) error {
	zn, err := addr.ZoneNoAddr(int(room))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAborted, err)
	}
	za, err := addr.ZoneAddr(int(slot))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAborted, err)
	}
	rs, _ := addr.RoomStatusAddr(int(room))
	var got [3]byte
	for i, a := range []uint32{zn, za + addr.ZoneOffRoomNo, rs + addr.RoomStatusOffFlags} {
		if got[i], err = mem.ReadU8(w.Mem, a); err != nil {
			return err
		}
	}
	switch z, owner, f := int8(got[0]), int8(got[1]), got[2]; {
	case z != slot:
		return fmt.Errorf("%w: room %d's zone moved from slot %d to %d", ErrAborted, room, slot, z)
	case owner != room:
		return fmt.Errorf("%w: zone slot %d is now room %d's, not room %d's", ErrAborted, slot, owner, room)
	case f&addr.RoomFlagLoaded == 0 || f&(addr.RoomFlagCreating|addr.RoomFlagDeleting) != 0:
		return fmt.Errorf("%w: room %d's status is 0x%02X: not loaded and settled", ErrAborted, room, f)
	}
	return nil
}

// verifyKills judges the dead-actor bytes written last tick against s, as
// verifyScoped does the scoped path's: a byte is not judged when the
// stage, table or file changed, a save state was loaded, or its room's
// zone is no longer its slot (a zone wiped at a stage entry or reused is
// the game's own reset, d_stage.cpp:124-126, :250-267). The game never
// clears one bit of a live zone. w.mu is held.
func (w *FrameWriter) verifyKills(s *state.Snapshot, st gate.Status, f bind.File, rep *VerifyReport) {
	ps := w.kills
	w.kills = nil
	if len(ps) == 0 {
		return
	}
	same := s != nil && !st.Regressed && s.StageName == w.kiAt.stage && s.Table == w.kiAt.table && f == w.kiAt.file
	n := VerifyReport{}
	for _, p := range ps {
		ok := same
		if ok {
			z, zok := s.ZoneFor(int(p.kw.Room))
			ok = zok && z == int(p.kw.Slot)
		}
		var v byte
		if ok {
			v, ok = snapByte(s, p.kw.Addr)
		}
		switch {
		case !ok:
			n.Unverified++
			continue
		case v&p.kw.Bits == p.kw.Bits:
			n.Verified++
			continue
		}
		n.Failed++
		n.Failures = append(n.Failures, VerifyFailure{Addr: p.kw.Addr, Off: noOff, Old: p.old, New: p.val, Read: v})
		detail := fmt.Sprintf("the write of %s (0x%02X -> 0x%02X) read back 0x%02X on the next tick", p.kw, p.old, p.val, v)
		w.logf("co-op: verify failed: %s: the game stored over it, or Dolphin dropped the write", detail)
		w.wg.brk.Fault(FaultVerify, detail)
	}
	rep.Verified += n.Verified
	rep.Failed += n.Failed
	rep.Unverified += n.Unverified
	rep.Failures = append(rep.Failures, n.Failures...)
	w.stats.Verified += n.Verified
	w.stats.Failed += n.Failed
	w.stats.CombatFailed += n.Failed
	w.stats.Unverified += n.Unverified
}
