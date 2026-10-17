package replica

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The derive path (§6.4, §6.6, M4): what a receiver derives from its own
// flags reaches its game only through FrameWriter.ApplyDerived, the one
// call the WriteGuard lets write a DERIVED byte, a pending counter or a
// button's item (path, WriteGuard.checkDerive). Its writes are
// derive.Writes (small keys, max life, max magic) and inventory's derived
// writes (the item slots, the equipment, the buttons' items), each a
// compare-and-set: made only while its bytes still read what the plan
// saw, as the game moves the same bytes itself (a key counted in or out,
// a slot the game fills as it hands an item over).

// DeriveKind is what a write of the derive path changes.
type DeriveKind uint8

const (
	// DeriveKeyCounter is a delta into mItemKeyNumCount, for the loaded
	// table while its stage shows the key counter (derive.KeyCounter).
	DeriveKeyCounter DeriveKind = iota + 1
	// DeriveKeyLive is the loaded table's live mKeyNum while its stage
	// hides the key counter (derive.KeyLive).
	DeriveKeyLive
	// DeriveKeySaved is the saved mKeyNum of a table that is not loaded
	// (derive.KeySaved).
	DeriveKeySaved
	// DeriveLifeCounter is quarter hearts into mItemMaxLifeCount
	// (derive.LifeCounter).
	DeriveLifeCounter
	// DeriveMagicCounter is max magic into mItemMaxMagicCount
	// (derive.MagicCounter).
	DeriveMagicCounter
	// DeriveSlot moves an item slot mItems[i] to a better variant
	// (inventory.KindSlot).
	DeriveSlot
	// DeriveEquip moves mSelectEquip[i] to a better variant
	// (inventory.KindEquip).
	DeriveEquip
	// DeriveButton refreshes the item a button shows, play.mSelectItem
	// (inventory.KindSelectItem).
	DeriveButton
)

var deriveKindNames = [...]string{"", "key counter", "live mKeyNum", "saved mKeyNum", "max-life counter",
	"max-magic counter", "item slot", "equipment", "button item"}

func (k DeriveKind) String() string {
	if k != 0 && int(k) < len(deriveKindNames) {
		return deriveKindNames[k]
	}
	return fmt.Sprintf("DeriveKind(%d)", uint8(k))
}

// counter returns the pending counter a kind writes; ok is false for a
// byte.
func (k DeriveKind) counter() (c gate.Counter, ok bool) {
	switch k {
	case DeriveKeyCounter:
		return gate.KeyNum, true
	case DeriveLifeCounter:
		return gate.MaxLife, true
	case DeriveMagicCounter:
		return gate.MaxMagic, true
	}
	return 0, false
}

// DeriveWrite is one write of the derive path: Data at Addr, in one memory
// write, made only while the bytes at Addr still read Expect. A counter's
// Data is its s16 delta, big-endian, over an Expect of 0.
type DeriveWrite struct {
	Kind DeriveKind
	// Table is the save table a key write is for; -1 for the rest.
	Table  int
	Addr   uint32
	Expect []byte
	Data   []byte
	Reason string
}

func (d DeriveWrite) String() string {
	if _, ok := d.Kind.counter(); ok && len(d.Data) == 2 {
		return fmt.Sprintf("%v %+d at 0x%08X: %s", d.Kind, int16(binary.BigEndian.Uint16(d.Data)), d.Addr, d.Reason)
	}
	return fmt.Sprintf("%v 0x%08X % X -> % X: %s", d.Kind, d.Addr, d.Expect, d.Data, d.Reason)
}

// deriveWriteOf is derive's w as the derive path takes it.
func deriveWriteOf(w derive.Write) DeriveWrite {
	k := map[derive.Kind]DeriveKind{
		derive.KeyCounter: DeriveKeyCounter, derive.KeyLive: DeriveKeyLive, derive.KeySaved: DeriveKeySaved,
		derive.LifeCounter: DeriveLifeCounter, derive.MagicCounter: DeriveMagicCounter,
	}[w.Kind]
	return DeriveWrite{Kind: k, Table: w.Table, Addr: w.Addr, Expect: bytes.Clone(w.Expect), Data: bytes.Clone(w.Data), Reason: w.Reason}
}

// inventoryWriteOf is inventory's w as the derive path takes it: a slot,
// an equipment byte or a button, compare-and-set over the byte the plan
// read. ok is false for the rest: its flags and capacities are the merge
// path's, and its ammo fills no path's.
func inventoryWriteOf(w inventory.Write) (d DeriveWrite, ok bool) {
	switch w.Kind {
	case inventory.KindSlot:
		d.Kind = DeriveSlot
	case inventory.KindEquip:
		d.Kind = DeriveEquip
	case inventory.KindSelectItem:
		d.Kind = DeriveButton
	default:
		return d, false
	}
	if w.Op != inventory.OpSet {
		return d, false
	}
	d.Table, d.Addr, d.Expect, d.Data, d.Reason = -1, w.Addr, []byte{w.Old}, []byte{w.Val}, w.Reason
	return d, true
}

// DeriveOutcome is what became of one DeriveWrite.
type DeriveOutcome uint8

const (
	// DeriveNotTried: the batch stopped before it, or made nothing (a
	// stall, the game moved on, co-op off, an error).
	DeriveNotTried DeriveOutcome = iota
	// DeriveMade: written.
	DeriveMade
	// DeriveStale: its bytes no longer read Expect after the beat: the
	// game moved them since the plan. Not written, and no fault.
	DeriveStale
	// DeriveClosed: a gate it needs is closed this tick.
	DeriveClosed
	// DeriveRefused: the WriteGuard refused it.
	DeriveRefused
)

func (o DeriveOutcome) String() string {
	switch o {
	case DeriveNotTried:
		return "not tried"
	case DeriveMade:
		return "made"
	case DeriveStale:
		return "stale"
	case DeriveClosed:
		return "gate closed"
	case DeriveRefused:
		return "refused"
	}
	return fmt.Sprintf("DeriveOutcome(%d)", uint8(o))
}

// DeriveResult is what one ApplyDerived did.
type DeriveResult struct {
	Beat   uint32        // the heartbeat the writes followed
	Waited time.Duration // from the call to the beat
	Wrote  []ByteWrite   // every byte written, in write order; Off is the image byte, or noOff
	// Outcomes are the writes', in the order given.
	Outcomes []DeriveOutcome
}

// Made returns how many writes were made.
func (r DeriveResult) Made() int {
	n := 0
	for _, o := range r.Outcomes {
		if o == DeriveMade {
			n++
		}
	}
	return n
}

// noOff is the Off of a byte outside the image (a counter, a button).
const noOff = 0xFFFF

// DrainFrames is how many frames the HUD is given to take a pending
// counter the derive path wrote before the drain watch trips the Breaker.
// The meter takes each counter in the frame after a write: dMeter_Execute
// runs dMeter_heartMove, dMeter_magicMove and dMeter_keyMove every frame
// (d_meter.cpp:7340, 7391, 7395), and each adds a nonzero count and
// zeroes it (dMeter_LifeMove, :1420-1452; dMeter_magicGaugeMove,
// :4069-4076; dMeter_keyMove, :5242-5257). Frames are counted only while
// the meter would take that counter (hudTakes); the slack is for the one
// case the snapshot can't see, the fly gauge of a seagull Link possesses
// (daNpc_kam_c::m_hyoi_kamome, d_meter.cpp:815-816), which hides the
// magic gauge, and with it the max-magic count, for the flight (inferred
// to last less than ten seconds of play).
const DrainFrames = 300

// drainWatch is a counter the derive path wrote, waiting for the HUD.
type drainWatch struct {
	w      DeriveWrite
	c      gate.Counter
	at     seen   // the tick it was written in
	beat   uint32 // the heartbeat frames are counted from
	frames uint32 // frames the HUD has had to take it
}

// counterOf returns counter c as s read it.
func counterOf(s *state.Snapshot, c gate.Counter) int16 {
	switch c {
	case gate.MaxLife:
		return s.MaxLifeCount
	case gate.MaxMagic:
		return s.MaxMagicCount
	}
	return s.KeyNumCount
}

// miniGameOrca is the minigame type during which dMeter_heartMove runs
// dMeter_BattleLifeMove in place of dMeter_LifeMove (d_meter.cpp:
// 1561-1571), leaving mItemMaxLifeCount alone: Orca's sword training
// (dComIfGp_startMiniGame(6), d_a_npc_ji1.cpp:1965, :2535).
const miniGameOrca = 6

// hudTakes reports whether the meter takes counter c in the frames before
// s, as far as s shows:
//
//   - not while the pause menu is up: whether the meter runs under it is
//     not established, so those frames are not counted (inferred);
//   - the key counter otherwise always: the derive path writes it only
//     while the stage shows it (dStage_stagInfo_ChkKeyDisp, d_meter.cpp:
//     5247), and the watch drops it when the stage changes;
//   - max life unless Orca's training runs (miniGameOrca);
//   - max magic unless a partner is loaded, mpPlayerPtr[1]
//     (d_com_inf_game.h:791): while Link's partner is the one controlled
//     (daPy_py_c::changePlayer sets mPlayerInfo[0], d_a_player.cpp:13-25,
//     which the snapshot does not read) the meter shows the fly gauge in
//     place of the magic gauge (dComIfGp_getCb1Player() ==
//     daPy_getPlayerActorClass(), d_meter.cpp:815-816; dMeter_magicMove,
//     :4015-4040). A partner merely along is counted the same: the watch
//     only waits longer.
func hudTakes(c gate.Counter, s *state.Snapshot) bool {
	if s.MenuPause != 0 {
		return false
	}
	switch c {
	case gate.MaxLife:
		return s.MiniGameType != miniGameOrca
	case gate.MaxMagic:
		partner := addr.PlayerPtr + 4 - state.PlayerRegion.Start
		return binary.BigEndian.Uint32(s.Player[partner:]) == 0
	}
	return true
}

// ApplyDerived makes the derive path's writes, ds in order, right after
// the next heartbeat, as ApplyContext makes a plan's (§4.2.1):
//
//  1. Before the wait, each write must have its kind's shape
//     (WriteGuard.checkDerive: a counter's s16 over 0 with a delta the HUD
//     takes and that never lowers max life or magic, a key byte of the
//     right table, a slot, equipment byte or button of the derived
//     ones). One without it is refused; one whose gate is closed this
//     tick is not tried (DeriveClosed), and is no fault.
//  2. After the beat, the stage, mNextStage.mEnable, the loaded table and
//     file (recheck) and IDLE's inputs (recheckIdle) are read again: the
//     derive path writes only when IDLE, and an event that began since
//     the snapshot aborts the batch.
//  3. Each write: a fresh read of its bytes; if they no longer read
//     Expect, the game moved them since the plan and the write is
//     dropped (DeriveStale, no fault); else every byte is checked as the
//     derive path's and the write is made, a counter's two bytes in one
//     memory write, so the HUD never takes half of it.
//  4. The next Observe verifies a written byte reads what was written
//     (a verify failure is a fault), and watches a written counter: it
//     must read 0 again within DrainFrames frames the HUD would take it
//     in (hudTakes), or the Breaker trips (Breaker.Undrained). A counter
//     drains in the frame after the write, so one that never does was
//     written where the meter does not read.
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed when every write's gate is closed, ErrStall, ErrAborted, the
// first refusal (ErrGuard), ctx's error or a memory error. Outcomes says
// what became of each write.
func (w *FrameWriter) ApplyDerived(ctx context.Context, ds []DeriveWrite) (res DeriveResult, err error) {
	res.Outcomes = make([]DeriveOutcome, len(ds))
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
		for _, o := range res.Outcomes {
			switch o {
			case DeriveMade:
				w.stats.Derived++
			case DeriveStale:
				w.stats.Stale++
			case DeriveRefused:
				w.stats.Refused++
			}
		}
	}()
	var first, closed error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}

	var open []int
	for i, d := range ds {
		if err := w.wg.checkDeriveShape(d); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			res.Outcomes[i] = DeriveRefused
			note(err)
			continue
		}
		var gateErr error
		for j := range d.Data {
			if err := w.wg.ready(d.Addr + uint32(j)); err != nil {
				gateErr = err
				break
			}
		}
		switch {
		case errors.Is(gateErr, ErrOff):
			return res, gateErr
		case gateErr != nil:
			res.Outcomes[i] = DeriveClosed
			if closed == nil {
				closed = gateErr
			}
			continue
		}
		open = append(open, i)
	}
	if len(open) == 0 {
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

	for _, i := range open {
		d := ds[i]
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		old, err := w.Mem.Read(d.Addr, len(d.Data))
		if err != nil {
			note(err)
			break
		}
		if !bytes.Equal(old, d.Expect) {
			res.Outcomes[i] = DeriveStale
			continue
		}
		if err := w.wg.checkDerive(d, old); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			res.Outcomes[i] = DeriveRefused
			note(err)
			continue
		}
		if err := w.Mem.Write(d.Addr, d.Data); err != nil {
			note(fmt.Errorf("replica: writing 0x%08X: %w", d.Addr, err))
			break
		}
		res.Outcomes[i] = DeriveMade
		off := imageOff(d.Addr, w.tick.table)
		for j, v := range d.Data {
			res.Wrote = append(res.Wrote, ByteWrite{Addr: d.Addr + uint32(j), Off: off, Old: old[j], New: v})
		}
		if c, ok := d.Kind.counter(); ok {
			w.drains = append(w.drains, drainWatch{w: d, c: c, at: w.tick, beat: hb})
			continue
		}
		if len(w.pending) == 0 {
			w.pendAt = w.tick
		}
		w.pending = append(w.pending, pendingByte{a: d.Addr, off: off, old: old[0], val: d.Data[0], bits: d.Data[0], set: true})
	}
	return res, first
}

// imageOff is the image offset of the byte at a: a live memBit byte's is
// its place in the saved slot of table, the one loaded; noOff outside
// both.
func imageOff(a uint32, table int8) uint16 {
	switch {
	case a >= addr.SaveImage && a < addr.SaveImage+addr.SaveImageSize:
		return uint16(a - addr.SaveImage)
	case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize && addr.ValidTable(int(table)):
		return uint16(addr.OffMemBit(int(table)) + int(a-addr.LiveMemBit))
	}
	return noOff
}

// watchDrains judges the counters the derive path wrote against s, a
// snapshot of file f (nil when its read failed). One that reads 0 again
// was taken by the HUD; one written in another stage, table or file, or
// before a save state, is no longer judged; one still pending after
// w.drainFrames frames the HUD would have taken it in trips the Breaker.
// w.mu is held.
func (w *FrameWriter) watchDrains(s *state.Snapshot, st gate.Status, f bind.File) {
	keep := w.drains[:0]
	for _, d := range w.drains {
		switch {
		case s == nil || st.Regressed || s.StageName != d.at.stage || s.Table != d.at.table || f != d.at.file:
			w.stats.Unverified++
			continue
		case counterOf(s, d.c) == 0:
			w.stats.Drained++
			continue
		}
		if moved := int32(s.Heartbeat - d.beat); moved > 0 && hudTakes(d.c, s) {
			d.frames += uint32(moved)
		}
		d.beat = s.Heartbeat
		if d.frames > w.drainFrames {
			detail := fmt.Sprintf("the HUD never took the %s co-op wrote (%s): it still reads %d after %d frames",
				d.c, d.w, counterOf(s, d.c), d.frames)
			w.logf("co-op: %s; the write went where the meter does not read it", detail)
			w.wg.brk.Undrained(detail)
			w.drains = nil
			return
		}
		keep = append(keep, d)
	}
	w.drains = keep
}
