package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The salvage catch-up's write (salvage.go): FrameWriter.ApplySalvage sets
// bit 0x01 of a registered salvage point's mFlag, judged by
// WriteGuard.checkSalvage (TargetSalvage).

// SalvageHeld is a point ApplySalvage did not mark, and why.
type SalvageHeld struct {
	Point SalvagePoint
	Why   string
}

// SalvageResult is what ApplySalvage did.
type SalvageResult struct {
	Result
	Done []SalvagePoint // as read just before the write
	Held []SalvageHeld
}

// pendingSalvage is a point marked, waiting for its verify.
type pendingSalvage struct {
	base uint32
	p    SalvagePoint
}

// SetSalvage gives the guard the tick's salvage plan. SetTick forgets it.
func (w *WriteGuard) SetSalvage(t SalvageTick) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.salvage, w.salvageOK = t, w.have
}

// checkSalvage reports whether val may be written over old at a, the mFlag
// of point p of the table at base as ApplySalvage read it just before
// (TargetSalvage). It lets through exactly that byte with bit 0x01 set and
// nothing else changed, from a byte with it clear, for a point of the
// tick's plan read the same (SetSalvage), with ARMED, IDLE and the gates
// open. A refusal is counted, logged and reported as Check's.
func (w *WriteGuard) checkSalvage(base uint32, p SalvagePoint, a uint32, old, val byte) error {
	w.mu.Lock()
	w.checked++
	e := &RefusalError{Addr: a, Old: old, New: val, What: "salvage point flag"}
	e.Reason, e.Why = w.judgeSalvage(base, p, a, old, val)
	if e.Reason == 0 {
		w.mu.Unlock()
		return nil
	}
	return w.refuse(e)
}

// judgeSalvage is checkSalvage's verdict. w.mu is held.
func (w *WriteGuard) judgeSalvage(base uint32, p SalvagePoint, a uint32, old, val byte) (Refusal, string) {
	switch {
	case w.brk.Off():
		return RefuseOff, w.brk.Why()
	case w.allow.targets&TargetSalvage == 0:
		return RefuseTarget, "the salvage catch-up is not this guard's target"
	case !w.have:
		return RefuseGate, "no tick has opened the gates yet"
	case w.closed != "":
		return RefuseGate, w.closed
	}
	if why := w.tick.opens(TargetSalvage, a); why != "" {
		return RefuseGate, why
	}
	sv := &w.salvage
	planned, ok := sv.Points[p.Index]
	switch {
	case !w.salvageOK || sv.Points == nil:
		return RefuseGate, "no salvage plan this tick"
	case base != sv.Base:
		return RefuseTarget, fmt.Sprintf("the table at 0x%08X is not this tick's (0x%08X)", base, sv.Base)
	case !ok || !planned.same(p):
		return RefuseTarget, fmt.Sprintf("%s is no point of this tick's plan", p)
	case a != p.FlagAddr(base):
		return RefuseTarget, fmt.Sprintf("0x%08X is not %s's mFlag", a, p)
	case old&addr.SalvageFlagUnusable != 0 || val != old|addr.SalvageFlagUnusable:
		return RefuseValue, fmt.Sprintf("a salvage flag may only gain bit 0x01, not go from 0x%02X to 0x%02X", old, val)
	}
	return 0, ""
}

// readySalvage returns nil if the catch-up's gates are open this tick
// (ARMED, IDLE, STABLE, HEARTBEAT), or ErrClosed with why not.
func (w *FrameWriter) readySalvage() error {
	g := w.wg
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.brk.Off():
		return g.brk.Err()
	case !g.have:
		return fmt.Errorf("%w: no tick has opened the gates yet", ErrClosed)
	case g.closed != "":
		return fmt.Errorf("%w: %s", ErrClosed, g.closed)
	}
	if why := g.tick.opens(TargetSalvage, 0); why != "" {
		return fmt.Errorf("%w: %s", ErrClosed, why)
	}
	return nil
}

// ApplySalvage marks plan's points right after the next heartbeat:
//
//  1. before the wait, the gates must be open (readySalvage);
//  2. the heartbeat is followed from the snapshot's (waitBeat), then the
//     stage, mNextStage.mEnable, the loaded table and file (recheck) and
//     IDLE's inputs (recheckIdle) are read again: a change is ErrAborted;
//  3. mTagData_p is read again: another table holds every point;
//  4. for each point: its entry read again, held if it changed (another
//     point in its slot, or none) and skipped if its flag is set already;
//     its save bit read again in the live save, held if clear. Else
//     WriteGuard.checkSalvage, a 1-byte write of old | 0x01 and a read
//     back;
//  5. the next Observe reads each entry again (verifySalvage).
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed, ErrStall, ErrAborted, the first refusal (ErrGuard), ctx's
// error, or a memory error.
func (w *FrameWriter) ApplySalvage(ctx context.Context, plan SalvagePlan) (res SalvageResult, err error) {
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
		w.stats.Salvage += len(res.Done)
		w.stats.Held += len(res.Held)
		w.stats.Refused += res.Refused
	}()
	if len(plan.Points) == 0 {
		return res, nil
	}
	if err := w.readySalvage(); err != nil {
		if !errors.Is(err, ErrOff) {
			res.Closed += len(plan.Points)
		}
		return res, err
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
	hold := func(p SalvagePoint, why string) { res.Held = append(res.Held, SalvageHeld{Point: p, Why: why}) }
	base, err := mem.ReadU32(w.Mem, addr.SalvageTagDataPtr)
	if err != nil {
		return res, err
	}
	if base != plan.Base {
		for _, p := range plan.Points {
			hold(p, fmt.Sprintf("mTagData_p is 0x%08X now, not 0x%08X", base, plan.Base))
		}
		return res, nil
	}
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, p := range plan.Points {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		ea := addr.SalvageInfoAddr(base, p.Index)
		b, err := w.Mem.Read(ea, addr.SalvageInfoSize)
		if err != nil {
			note(err)
			break
		}
		now := decodeSalvageInfo(p.Index, b)
		if !now.same(p) || now.Room == -1 {
			hold(p, fmt.Sprintf("%s changed since the plan (now %s, mRoomNo %d)", p, now, now.Room))
			continue
		}
		if !now.Usable() {
			continue // the game set it since the plan: nothing to do
		}
		k, ok := now.Key(plan.Table)
		off, mask, okOff := salvageBitOff(k)
		if !ok || !okOff {
			hold(p, fmt.Sprintf("%s has no save bit", p))
			continue
		}
		v, err := mem.ReadU8(w.Mem, addr.SaveImage+uint32(off))
		if err != nil {
			note(err)
			break
		}
		if v&mask == 0 {
			hold(p, fmt.Sprintf("%s: %s reads clear", p, k))
			continue
		}
		a, old := now.FlagAddr(base), now.Flag
		val := old | addr.SalvageFlagUnusable
		if err := w.wg.checkSalvage(base, now, a, old, val); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			res.Refused++
			note(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, a, val); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: a, Off: noOff, Old: old, New: val})
		w.salvage = append(w.salvage, pendingSalvage{base: base, p: now})
		res.Done = append(res.Done, now)
		if back, err := mem.ReadU8(w.Mem, a); err != nil {
			note(err)
			break
		} else if back&addr.SalvageFlagUnusable == 0 {
			w.logf("co-op: salvage: the mark of %s read back 0x%02X at once (the next Observe judges it)", now, back)
		}
	}
	return res, first
}

// verifySalvage reads each point marked last tick: one still in its entry
// (the same table, place, room, save number and kind) whose flag bit reads
// clear lost the write (a verify failure); one gone is fine; with the
// stage changed or a save state loaded none is judged. w.mu is held.
func (w *FrameWriter) verifySalvage(s *state.Snapshot, st gate.Status, rep *VerifyReport) {
	ps := w.salvage
	w.salvage = nil
	if len(ps) == 0 {
		return
	}
	n := VerifyReport{}
	for _, p := range ps {
		if s == nil || st.Regressed || s.StageName != w.kiStage() {
			n.Unverified++
			continue
		}
		base, err := mem.ReadU32(w.Mem, addr.SalvageTagDataPtr)
		if err != nil {
			n.Unverified++
			continue
		}
		if base != p.base {
			n.Verified++ // another table
			continue
		}
		b, err := w.Mem.Read(addr.SalvageInfoAddr(base, p.p.Index), addr.SalvageInfoSize)
		if err != nil {
			n.Unverified++
			continue
		}
		now := decodeSalvageInfo(p.p.Index, b)
		switch {
		case now.Room == -1 || !now.same(p.p):
			n.Verified++ // init_one'd, or another point in its slot
		case !now.Usable():
			n.Verified++
		default:
			n.Failed++
			a := p.p.FlagAddr(base)
			n.Failures = append(n.Failures, VerifyFailure{Addr: a, Off: noOff, Old: p.p.Flag, New: p.p.Flag | addr.SalvageFlagUnusable, Read: now.Flag})
			detail := fmt.Sprintf("the mark of %s read back 0x%02X on the next tick", p.p, now.Flag)
			w.logf("co-op: verify failed: %s: the game stored over it, or Dolphin dropped the write", detail)
			w.wg.brk.Fault(FaultVerify, detail)
		}
	}
	rep.Verified += n.Verified
	rep.Failed += n.Failed
	rep.Unverified += n.Unverified
	rep.Failures = append(rep.Failures, n.Failures...)
	w.stats.Verified += n.Verified
	w.stats.Failed += n.Failed
	w.stats.Unverified += n.Unverified
}
