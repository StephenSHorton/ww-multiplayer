package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	linkpos "github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Chest claims' writes (chests.go): FrameWriter.ApplyChests swaps a
// chest's action word between actionOpenWait and actionWait, judged by
// WriteGuard.checkChest (TargetChestHold).

// ChestResult is what ApplyChests did.
type ChestResult struct {
	Result
	Held     []ChestActor // holds written, as read just before
	Restored []ChestActor
	Skipped  []string // why a planned write was not made
}

// SetChests gives the guard the tick's chest plan. SetTick forgets it.
func (w *WriteGuard) SetChests(p ChestPlan) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chests, w.chestsOK = p, w.have
}

// checkChest reports whether val may be written over old at a, chest c's
// action word: a hold (actionOpenWait to actionWait, ARMED and IDLE) or a
// restore (actionWait to actionOpenWait, of a chest the plan restores,
// whose key the hub has not said is consumed; neither ARMED nor IDLE: it
// is how the replica fails open). Nothing else.
func (w *WriteGuard) checkChest(c ChestActor, hold bool, a, old, val uint32) error {
	w.mu.Lock()
	w.checked++
	e := &RefusalError{Addr: a, Old: byte(old), New: byte(val), What: "chest action word"}
	e.Reason, e.Why = w.judgeChest(c, hold, a, old, val)
	if e.Reason == 0 {
		w.mu.Unlock()
		return nil
	}
	return w.refuse(e)
}

// judgeChest is checkChest's verdict. w.mu is held.
func (w *WriteGuard) judgeChest(c ChestActor, hold bool, a, old, val uint32) (Refusal, string) {
	switch {
	case w.brk.Off():
		return RefuseOff, w.brk.Why()
	case w.allow.targets&TargetChestHold == 0:
		return RefuseTarget, "chest holds are not this guard's target"
	case !w.have:
		return RefuseGate, "no tick has opened the gates yet"
	case !w.chestsOK:
		return RefuseGate, "no chest plan this tick"
	case a != c.WordAddr():
		return RefuseTarget, fmt.Sprintf("0x%08X is not %s's action word", a, c)
	}
	list := w.chests.Restore
	if hold {
		if w.closed != "" {
			return RefuseGate, w.closed
		}
		if why := w.tick.opens(TargetChestHold, a); why != "" {
			return RefuseGate, why
		}
		list = w.chests.Hold
	}
	planned := false
	for _, p := range list {
		if p.same(c) {
			planned = true
			break
		}
	}
	switch {
	case !planned:
		return RefuseTarget, fmt.Sprintf("%s is not in this tick's plan", c)
	case hold && (old != c.OpenWait() || val != c.Wait()):
		return RefuseValue, fmt.Sprintf("a hold goes from actionOpenWait 0x%08X to actionWait 0x%08X, not 0x%08X to 0x%08X", c.OpenWait(), c.Wait(), old, val)
	case !hold && (old != c.Wait() || val != c.OpenWait()):
		return RefuseValue, fmt.Sprintf("a restore goes from actionWait 0x%08X to actionOpenWait 0x%08X, not 0x%08X to 0x%08X", c.Wait(), c.OpenWait(), old, val)
	case !hold && w.chests.Consumed[c.Key]:
		return RefuseValue, fmt.Sprintf("%s is consumed: it is never restored", c.Key)
	}
	return 0, ""
}

// ApplyChests makes plan right after the next heartbeat:
//
//  1. the heartbeat is followed from the snapshot's (waitBeat) and the
//     stage, mNextStage.mEnable and the loaded table are read again
//     (recheck): a change is ErrAborted;
//  2. per chest: the actor read again (the walk's node, pid and params,
//     the fingerprint and its base), then its word and mFlags;
//     - a hold: the word actionOpenWait, mFlags not busy, no event
//     order queued and Link farther than ChestHoldFar, all read now;
//     - a restore: the word actionWait and the chest bit clear, read now;
//  3. WriteGuard.checkChest, a 4-byte write and a read back.
//
// A restore-only plan needs no IDLE (recheckIdle is skipped), so held
// chests come back when co-op stops arming.
func (w *FrameWriter) ApplyChests(ctx context.Context, plan ChestPlan) (res ChestResult, err error) {
	if w.wg == nil {
		return res, ErrDryRun
	}
	if plan.empty() {
		return res, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.wg.brk.Err(); err != nil {
		return res, err
	}
	w.wg.SetChests(plan)
	defer func() {
		if len(res.Wrote) > 0 {
			w.stats.Batches++
		}
		w.stats.Written += len(res.Wrote)
	}()
	hb, waited, err := w.waitBeat(ctx)
	res.Waited = waited
	if err == nil {
		err = w.recheck()
	}
	if err == nil && len(plan.Hold) > 0 {
		if e := w.recheckIdle(); e != nil {
			plan.Hold = nil // the restores still go
			res.Skipped = append(res.Skipped, "holds wait: "+e.Error())
		}
	}
	switch {
	case errors.Is(err, ErrStall):
		w.stats.Stalls++
		return res, err
	case errors.Is(err, ErrAborted):
		w.stats.Aborted++
		return res, err
	case err != nil:
		return res, err
	}
	res.Beat = hb
	typ, err := mem.ReadU32(w.Mem, addr.FopAcType)
	if err != nil {
		return res, err
	}
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	skip := func(c ChestActor, why string) { res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %s", c, why)) }
	do := func(c ChestActor, hold bool) bool {
		if _, err := actors.RereadParams(w.Mem, &c.A, typ, nil); err != nil {
			skip(c, "changed since the plan: "+err.Error())
			return true
		}
		if exec := chestExec(w.Mem, c.Addr); exec == 0 || exec-mailbox.RefreshTboxTextExec != c.Base {
			skip(c, "its profile no longer matches the plan's")
			return true
		}
		word, err := mem.ReadU32(w.Mem, c.WordAddr())
		if err != nil {
			note(err)
			return false
		}
		set, err := chestBitSet(w.Mem, c.Key.No)
		if err != nil {
			note(err)
			return false
		}
		from, to := c.Wait(), c.OpenWait()
		if hold {
			from, to = c.OpenWait(), c.Wait()
			flags, err := mem.ReadU16(w.Mem, c.Addr+mailbox.RefreshTboxFlagsOff)
			if err != nil {
				note(err)
				return false
			}
			n, err := mem.ReadU8(w.Mem, addr.EvtOrderCount)
			if err != nil {
				note(err)
				return false
			}
			p, perr := linkpos.NewLink(w.Mem).Position()
			switch {
			case word != from:
				skip(c, fmt.Sprintf("its action word is 0x%08X now, not actionOpenWait", word))
				return true
			case flags&mailbox.RefreshTboxFlagsBusy != 0:
				skip(c, fmt.Sprintf("it is busy (mFlags 0x%04X)", flags))
				return true
			case n != 0:
				skip(c, "an event order is queued")
				return true
			case perr != nil:
				skip(c, "no Link: "+perr.Error())
				return true
			case distXZ([3]float32{p.PosX, p.PosY, p.PosZ}, c.Pos) <= ChestHoldFar:
				skip(c, fmt.Sprintf("Link is within %d units", ChestHoldFar))
				return true
			}
		} else {
			switch {
			case word != from:
				skip(c, fmt.Sprintf("its action word is 0x%08X now, not actionWait", word))
				return true
			case set:
				skip(c, "its chest bit is set: never restored")
				return true
			}
		}
		if err := w.wg.checkChest(c, hold, c.WordAddr(), word, to); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				return false
			}
			res.Refused++
			note(err)
			return true
		}
		if err := mem.WriteU32(w.Mem, c.WordAddr(), to); err != nil {
			note(err)
			return false
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: c.WordAddr(), Off: noOff, Old: byte(word), New: byte(to)})
		c.Word = to
		if hold {
			res.Held = append(res.Held, c)
		} else {
			res.Restored = append(res.Restored, c)
		}
		if back, err := mem.ReadU32(w.Mem, c.WordAddr()); err == nil && back != to {
			w.logf("co-op: chest claims: %s's action word read back 0x%08X at once (the next tick judges it)", c, back)
		}
		return true
	}
	for _, c := range plan.Restore {
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		if !do(c, false) {
			break
		}
	}
	for _, c := range plan.Hold {
		if err := ctx.Err(); err != nil || w.wg.brk.Off() {
			break
		}
		if !do(c, true) {
			break
		}
	}
	return res, first
}
