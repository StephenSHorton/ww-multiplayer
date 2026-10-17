package replica

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The scoped path (§6.3, M5): the dan and zone switches a co-present
// partner's game set reach the receiver's game only through
// FrameWriter.ApplyScoped, the one call the WriteGuard lets write a dan or
// zone switch byte (pathScoped). Its writes are bytes, as the merge path's
// are: the game sets a dan switch with a u32 load, OR and store
// (dSv_danBit_c::onSwitch, d_save.cpp:1237-1241) and a zone switch with a
// u16 one (dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282), so a word write
// would put back the old value of a neighbouring byte the game set between
// Go's read and write: a clear, which nothing may make. A byte write
// changes only the byte that holds the planned bits.
//
// A zone write rests on the zone table: the slot is first-free per machine
// (createZone, d_save.cpp:1525-1537), freed by removeZone setting mRoomNo
// alone (d_save.h:969) and wiped when createZone reuses it
// (dSv_zone_c::init, d_save.cpp:1338-1342), and a room change clears every
// live zone's room switches (zoneCountCheck, d_stage.cpp:250-267). So the
// table is read again after the beat and before each zone byte, and a
// write is made only into the slot that is still the zone of the room
// Link is in.

// ScopedWrite is one byte of the scoped path: Bits to OR into a dan or zone
// switch byte.
type ScopedWrite struct {
	// Kind is the store the bits are of: wire.ScopeDan (mDan.mSwitch,
	// switches 0x80-0xBF), wire.ScopeZone (a zone's mSwitch[0..1],
	// 0xC0-0xDF) or wire.ScopeRoomEdge (its mSwitch[2], the room switches
	// 0xE0-0xEF).
	Kind wire.ScopeKind
	Addr uint32
	Bits byte
	// Room and Slot are a zone byte's room and the zone slot the plan
	// found for it, mStatus[Room].mZoneNo; both -1 for a dan byte.
	Room, Slot int8
}

// scopedWriteOf is the ScopedWrite that sets switch no in a store of kind:
// the dan bits, or the zone in slot of room. It fails for a switch outside
// the kind's range, so nothing past 0xEF, which onSwitch asserts on
// (d_save.cpp:1541-1542), is ever planned.
func scopedWriteOf(kind wire.ScopeKind, no, room, slot int) (ScopedWrite, error) {
	store, bit, err := addr.SwitchBit(no)
	if err != nil {
		return ScopedWrite{}, err
	}
	want := map[wire.ScopeKind]addr.SwitchStore{wire.ScopeDan: addr.SwitchDan, wire.ScopeZone: addr.SwitchZone, wire.ScopeRoomEdge: addr.SwitchRoom}[kind]
	if store != want || want == 0 {
		return ScopedWrite{}, fmt.Errorf("replica: switch 0x%02X is a %s switch, not one of a %s store", no, store, kind)
	}
	if kind == wire.ScopeDan {
		return ScopedWrite{Kind: kind, Addr: addr.Dan + uint32(bit.Off), Bits: bit.Mask, Room: -1, Slot: -1}, nil
	}
	base, err := addr.ZoneAddr(slot)
	if err != nil {
		return ScopedWrite{}, err
	}
	if room < 0 || room >= addr.RoomCount {
		return ScopedWrite{}, fmt.Errorf("replica: room %d has no zone", room)
	}
	return ScopedWrite{Kind: kind, Addr: base + uint32(bit.Off), Bits: bit.Mask, Room: int8(room), Slot: int8(slot)}, nil
}

// Switches lists the switch numbers w sets, ascending; nil if w is no byte
// of its kind.
func (w ScopedWrite) Switches() []int {
	base, first, size, n := addr.DanSwitch, addr.SwitchDanFirst, 4, addr.DanSize-addr.DanOffSwitch
	if w.Kind != wire.ScopeDan {
		z, err := addr.ZoneAddr(int(w.Slot))
		if err != nil {
			return nil
		}
		base, first, size, n = z+addr.ZoneOffSwitch, addr.SwitchZoneFirst, 2, addr.ZoneOffItem-addr.ZoneOffSwitch
	}
	if w.Addr < base || w.Addr >= base+uint32(n) {
		return nil
	}
	rel := int(w.Addr - base)
	word, at := rel/size, rel%size
	var out []int
	// A word is big-endian: its last byte holds bits 0-7.
	for set := w.Bits; set != 0; set &= set - 1 {
		out = append(out, first+word*8*size+(size-1-at)*8+bits.TrailingZeros8(set))
	}
	return out
}

func (w ScopedWrite) String() string {
	var names []string
	for _, no := range w.Switches() {
		names = append(names, fmt.Sprintf("0x%02X", no))
	}
	where := ""
	if w.Kind != wire.ScopeDan {
		where = fmt.Sprintf(" in room %d (zone slot %d)", w.Room, w.Slot)
	}
	return fmt.Sprintf("%s switch %s%s (0x%08X |= 0x%02X)", scopedStore(w.Kind), strings.Join(names, ", "), where, w.Addr, w.Bits)
}

// scopedStore names the switches of a store kind.
func scopedStore(k wire.ScopeKind) string {
	switch k {
	case wire.ScopeDan:
		return "dan"
	case wire.ScopeZone:
		return "zone"
	case wire.ScopeRoomEdge:
		return "room"
	}
	return k.String()
}

// scopedShape returns why w is no write of its kind, or 0: a dan byte is
// one of mDan.mSwitch's eight with Room and Slot -1; a zone byte one of
// mSwitch[0..1] of zone Slot, and a room edge's one of its mSwitch[2], with
// Room 0..63 and Slot 0..31; and Bits are not 0. The zone table is the
// guard's own to judge (Tick.scopedClosed). w.mu is held.
func (w *WriteGuard) scopedShape(sw ScopedWrite) (Refusal, string) {
	lo, n := addr.DanSwitch, addr.DanSize-addr.DanOffSwitch
	switch sw.Kind {
	case wire.ScopeDan:
		if sw.Room != -1 || sw.Slot != -1 {
			return RefuseTarget, fmt.Sprintf("a dan byte names room %d and zone slot %d", sw.Room, sw.Slot)
		}
	case wire.ScopeZone, wire.ScopeRoomEdge:
		base, err := addr.ZoneAddr(int(sw.Slot))
		if err != nil || sw.Room < 0 || int(sw.Room) >= addr.RoomCount {
			return RefuseTarget, fmt.Sprintf("a %s byte of room %d in zone slot %d", scopedStore(sw.Kind), sw.Room, sw.Slot)
		}
		lo, n = base+addr.ZoneOffSwitch, 4 // mSwitch[0..1]
		if sw.Kind == wire.ScopeRoomEdge {
			lo, n = lo+4, 2 // mSwitch[2]
		}
	default:
		return RefuseTarget, fmt.Sprintf("%v is no store the scoped path writes", sw.Kind)
	}
	switch {
	case sw.Addr < lo || sw.Addr >= lo+uint32(n):
		return RefuseTarget, fmt.Sprintf("0x%08X is no %s switch byte of its store (0x%08X..0x%08X)", sw.Addr, scopedStore(sw.Kind), lo, lo+uint32(n)-1)
	case sw.Bits == 0:
		return RefuseValue, "it sets no bit"
	}
	return 0, ""
}

// checkScoped reports whether sw may be tried this tick, before the wait:
// it must have its kind's shape (scopedShape), its slot must be the zone
// the tick's zone table gives its room, and its byte must be the scoped
// path's, whatever the gates say. A write that fails it is a bug in its
// plan, refused before the wait as a byte off the allowlist is; a closed
// gate is ready's to report. A refusal is counted, logged and reported as
// Check's.
func (w *WriteGuard) checkScoped(sw ScopedWrite) error {
	w.mu.Lock()
	e := &RefusalError{Addr: sw.Addr, New: sw.Bits, What: scopedStore(sw.Kind) + " switch byte"}
	switch {
	case w.brk.Off():
		e.Reason, e.Why = RefuseOff, w.brk.Why()
	default:
		e.Reason, e.Why = w.scopedShape(sw)
	}
	if e.Reason == 0 {
		what, br, reason := w.rule(sw.Addr)
		e.What = what
		switch {
		case reason != 0:
			e.Reason, e.Why = RefuseTarget, "it is not a co-op write target"
		case br.paths&pathScoped == 0:
			e.Reason, e.Why = RefusePath, fmt.Sprintf("it is written only by %s, not by %s", br.paths, pathScoped)
		case sw.Kind != wire.ScopeDan:
			zt := &w.tick.Zones
			if z, ok := zt.SlotOf(int(sw.Room)); !w.have || !ok || z != int(sw.Slot) {
				e.Reason, e.Why = RefuseTarget, fmt.Sprintf("room %d's zone is not slot %d by this tick's zone table", sw.Room, sw.Slot)
			}
		}
	}
	if e.Reason == 0 {
		w.mu.Unlock()
		return nil
	}
	w.checked++
	return w.refuse(e)
}

// pendingScoped is a scoped byte waiting for its verify.
type pendingScoped struct {
	sw       ScopedWrite
	old, val byte
}

// ApplyScoped makes the scoped path's writes, ws (one byte each, bytes at
// the same address joined), right after the next heartbeat, as
// ApplyContext makes a plan's:
//
//  1. before the wait, each byte is checked to be one of its store's
//     (checkScoped: the dan bits, or mSwitch[0..2] of the zone its plan
//     found for its room by the tick's zone table) and the scoped path's;
//     one that is not is refused, and one whose gate is closed (IDLE,
//     STABLE, HEARTBEAT, ARMED) is not tried;
//  2. the heartbeat is followed from the snapshot's (waitBeat), then the
//     game state the gates rest on is read again: the stage,
//     mNextStage.mEnable, the loaded table and file (recheck), IDLE's
//     inputs (recheckIdle) and the zone table every zone byte rests on,
//     mStayNo, the room's mZoneNo and the slot's mRoomNo (recheckZones).
//     Any change is ErrAborted, and nothing is written;
//  3. for each byte: the zone table read again for a zone byte, a fresh
//     1-byte read, the bits ORed in, WriteGuard.Check (which holds a zone
//     byte to the zone of Link's room by the tick's table, Tick.
//     scopedClosed), a 1-byte write. A byte already holding the bits is
//     left alone;
//  4. the next Observe checks each written byte against its snapshot, if
//     the stage, table and file are the same and, for a zone byte, Link
//     is still in the room and its zone in the slot: a reset (initDan,
//     clearRoomSwitch, a zone wiped or reused) is the game's own.
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed, ErrStall, ErrAborted, the first refusal (ErrGuard), ctx's
// error, or a memory error.
func (w *FrameWriter) ApplyScoped(ctx context.Context, ws []ScopedWrite) (res Result, err error) {
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
		w.stats.Scoped += len(res.Wrote)
		w.stats.Already += res.Already
		w.stats.Refused += res.Refused
	}()
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	refuse := func(err error) {
		res.Refused++
		note(err)
	}

	var bs []ScopedWrite
	at := map[uint32]int{}
	for _, sw := range ws {
		i, ok := at[sw.Addr]
		switch {
		case !ok:
			at[sw.Addr] = len(bs)
			bs = append(bs, sw)
		case bs[i].Kind == sw.Kind && bs[i].Room == sw.Room && bs[i].Slot == sw.Slot:
			bs[i].Bits |= sw.Bits
		default:
			bs = append(bs, sw) // checkScoped refuses the one its address is not
		}
	}

	open := bs[:0]
	var closed error
	for _, sw := range bs {
		if err := w.wg.checkScoped(sw); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			refuse(err)
			continue
		}
		if err := w.wg.ready(sw.Addr); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			res.Closed++
			if closed == nil {
				closed = err
			}
			continue
		}
		open = append(open, sw)
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
	for i := 0; err == nil && i < len(bs); i++ {
		if bs[i].Kind != wire.ScopeDan {
			err = w.zoneHolds(bs[i].Room, bs[i].Slot)
		}
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

	for _, sw := range bs {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		if sw.Kind != wire.ScopeDan {
			if err := w.zoneHolds(sw.Room, sw.Slot); err != nil {
				if errors.Is(err, ErrAborted) {
					w.stats.Aborted++
					w.wg.Close(err.Error())
				}
				note(err)
				break
			}
		}
		old, err := mem.ReadU8(w.Mem, sw.Addr)
		if err != nil {
			note(err)
			break
		}
		val := old | sw.Bits
		if val == old {
			res.Already++
			continue
		}
		if err := w.wg.check(sw.Addr, old, val, pathScoped); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			refuse(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, sw.Addr, val); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: sw.Addr, Off: noOff, Old: old, New: val})
		if len(w.scoped) == 0 {
			w.scAt = w.tick
		}
		w.scoped = append(w.scoped, pendingScoped{sw: sw, old: old, val: val})
	}
	return res, first
}

// oldStayOf reads mOldStayNo, which every room change moves (setStayNo,
// d_stage.cpp:171-173), for snapshot s: one read of mStayNo and
// mOldStayNo, which sit side by side (sym:18667-18668). It is not ok if
// the read fails or mStayNo no longer reads as s has it.
func (w *FrameWriter) oldStayOf(s *state.Snapshot) (int8, bool) {
	b, err := w.Mem.Read(addr.StayNo, 2)
	if err != nil || int8(b[0]) != s.StayNo {
		return 0, false
	}
	return int8(b[1]), true
}

// zoneHolds reads again what a write into zone slot of room rests on, and
// returns ErrAborted if the tick's snapshot no longer holds: Link is in
// room (mStayNo) and came from the same room (mOldStayNo: a room change
// out and back moves it, unless it went back to the room he came from),
// the room's zone is slot (mStatus[room].mZoneNo, the zone onSwitch looks
// up, d_save.cpp:1552-1556) and slot is the room's (mRoomNo).
// zoneCountCheck clears the room switches, moves mStayNo and removes zones
// at a room change (d_stage.cpp:250-267), and a room scene's createZone
// takes the first free slot (d_s_room.cpp:193-197; d_save.cpp:1525-1537).
func (w *FrameWriter) zoneHolds(room, slot int8) error {
	zn, err := addr.ZoneNoAddr(int(room))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAborted, err)
	}
	za, err := addr.ZoneAddr(int(slot))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAborted, err)
	}
	st, err := w.Mem.Read(addr.StayNo, 2)
	if err != nil {
		return err
	}
	var got [2]int8
	for i, a := range []uint32{zn, za + addr.ZoneOffRoomNo} {
		b, err := mem.ReadU8(w.Mem, a)
		if err != nil {
			return err
		}
		got[i] = int8(b)
	}
	zt := &w.tick.zones
	switch stay, old, z, owner := int8(st[0]), int8(st[1]), got[0], got[1]; {
	case stay != zt.Stay || stay != room:
		return fmt.Errorf("%w: mStayNo went from %d to %d (a room change)", ErrAborted, zt.Stay, stay)
	case !w.tick.oldOK || old != w.tick.oldStay:
		return fmt.Errorf("%w: mOldStayNo went from %d to %d (a room change and back)", ErrAborted, w.tick.oldStay, old)
	case z != slot || z != zt.ZoneNo[room]:
		return fmt.Errorf("%w: room %d's zone moved from slot %d to %d", ErrAborted, room, slot, z)
	case owner != room || owner != zt.RoomNo[slot]:
		return fmt.Errorf("%w: zone slot %d is now room %d's, not room %d's", ErrAborted, slot, owner, room)
	}
	return nil
}

// verifyScoped judges the scoped bytes written last tick against s, as
// verify does the merge path's, adding to rep; old is s's mOldStayNo
// (oldStayOf), if oldOK. A byte is not judged when the stage, table or
// file changed, a save state was loaded, or, for a zone byte, mStayNo
// moved or the room's zone is no longer the slot written, or, for a room
// switch byte, mOldStayNo moved too, or the zone's room switches all read
// 0: initDan, clearRoomSwitch and a zone wiped or reused reset those bytes
// legitimately. clearRoomSwitch zeroes every zone's room switches at
// every room change (d_save.cpp:1273-1276; zoneCountCheck,
// d_stage.cpp:250-267), one out of the room and back between two ticks
// too, which leaves mStayNo and the slot as they were. w.mu is held.
func (w *FrameWriter) verifyScoped(s *state.Snapshot, st gate.Status, f bind.File, old int8, oldOK bool, rep *VerifyReport) {
	ps := w.scoped
	w.scoped = nil
	if len(ps) == 0 {
		return
	}
	same := s != nil && !st.Regressed && s.StageName == w.scAt.stage && s.Table == w.scAt.table && f == w.scAt.file
	n := VerifyReport{}
	for _, p := range ps {
		ok := same
		if ok && p.sw.Kind != wire.ScopeDan {
			z, zok := s.ZoneFor(int(p.sw.Room))
			ok = zok && z == int(p.sw.Slot) && s.StayNo == p.sw.Room && s.StayNo == w.scAt.zones.Stay
		}
		if ok && p.sw.Kind == wire.ScopeRoomEdge {
			ok = oldOK && w.scAt.oldOK && old == w.scAt.oldStay
		}
		var v byte
		if ok {
			v, ok = snapByte(s, p.sw.Addr)
		}
		switch {
		case !ok:
			n.Unverified++
			continue
		case v&p.sw.Bits == p.sw.Bits:
			n.Verified++
			continue
		case p.sw.Kind == wire.ScopeRoomEdge && zoneWords(s, int(p.sw.Slot))[2] == 0:
			// Every room switch of the zone is clear: clearRoomSwitch's
			// work, a room change out and back that mOldStayNo does not
			// show (back into the room Link came from).
			n.Unverified++
			continue
		}
		n.Failed++
		n.Failures = append(n.Failures, VerifyFailure{Addr: p.sw.Addr, Off: noOff, Old: p.old, New: p.val, Read: v})
		detail := fmt.Sprintf("the write of %s (0x%02X -> 0x%02X) read back 0x%02X on the next tick", p.sw, p.old, p.val, v)
		w.logf("co-op: verify failed: %s: the game stored over it, or Dolphin dropped the write", detail)
		w.wg.brk.Fault(FaultVerify, detail)
	}
	rep.Verified += n.Verified
	rep.Failed += n.Failed
	rep.Unverified += n.Unverified
	rep.Failures = append(rep.Failures, n.Failures...)
	w.stats.Verified += n.Verified
	w.stats.Failed += n.Failed
	w.stats.Unverified += n.Unverified
}
