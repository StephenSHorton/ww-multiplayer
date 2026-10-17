package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The light-shrink (§7.2, M6): FrameWriter.ApplyActors writes one byte, 1,
// into a live enemy's enemyice.mLightShrinkTimer, which runs the game's
// own light-arrow death on its next executed frames (enemy_ice,
// c_damagereaction.cpp:117-171), and then, for a carrier, clears CARRY on
// the weapon it holds, as the game's own light-arrow hit does for a
// Stalfos (fopAcM_cancelCarryNow, d_a_st.cpp:1570-1577;
// f_op_actor_mng.cpp:1366-1376). Only the CARRY bit is cleared: the
// room layer and shape_angle.z cancelCarryNow also sets are left.
//
// The order is the timer first, read back, then the weapon. Once the
// timer is set the carrier's execute returns at its enemy_ice call before
// the code that keeps the weapon in hand (d_a_bk.cpp:4159-4169, :4220-4224),
// so a weapon let go a frame later is harmless; a timer refused leaves the
// weapon held, so no live carrier ever stands without it.

// ActorTarget is one light-shrink the plan asks for: a carrier as the
// tick's walk read it, and the weapon it holds, nil for none.
type ActorTarget struct {
	Carrier actors.Actor
	Weapon  *actors.Actor
}

// ActorDone is a light-shrink ApplyActors wrote.
type ActorDone struct {
	Carrier  actors.Actor // as Revalidate read it just before the write
	Released bool         // its weapon's CARRY was cleared
}

// ActorHeld is a light-shrink ApplyActors did not write, and why.
type ActorHeld struct {
	Carrier actors.Actor
	Why     string
}

// ActorResult is what ApplyActors did.
type ActorResult struct {
	Result
	Done []ActorDone
	Held []ActorHeld
}

// pendingShrink is a timer written, waiting for its verify.
type pendingShrink struct {
	carrier actors.Actor
}

// readyK1 returns nil if the light-shrink's gates are open this tick
// (ARMED, IDLE, STABLE, HEARTBEAT), or ErrClosed with why not.
func (w *FrameWriter) readyK1() error {
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
	if why := g.tick.opens(TargetK1, 0); why != "" {
		return fmt.Errorf("%w: %s", ErrClosed, why)
	}
	return nil
}

// ApplyActors makes the light-shrinks ts right after the next heartbeat:
//
//  1. before the wait, the gates must be open (readyK1);
//  2. the heartbeat is followed from the snapshot's (waitBeat), then the
//     stage, mNextStage.mEnable, the loaded table and file (recheck), and
//     IDLE's inputs (recheckIdle) are read again: a change is ErrAborted;
//     with demo set, Link's mDemo.mDemoType is read again, and a demo
//     started holds every target;
//  3. for each target: carrier and weapon read again (actors.Revalidate,
//     with the home room's zone; unverified is its own), a target that
//     changed is held; else a fresh read of the timer, which must be 0,
//     WriteGuard.checkActor (T), a 1-byte write of 1, and a read back. Only
//     a timer that reads back nonzero lets the weapon go: a fresh read of
//     its CARRY byte, CARRY cleared, checkActor (W), a 1-byte write;
//  4. the next Observe reads each written carrier again: a timer that
//     reads 0 on the same live actor is a verify failure; an actor gone or
//     counting is not.
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed, ErrStall, ErrAborted, the first refusal (ErrGuard), ctx's
// error, or a memory error.
func (w *FrameWriter) ApplyActors(ctx context.Context, ts []ActorTarget, demo, unverified bool) (res ActorResult, err error) {
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
		w.stats.Shrunk += len(res.Done)
		w.stats.Held += len(res.Held)
		w.stats.Refused += res.Refused
		for _, d := range res.Done {
			if d.Released {
				w.stats.Released++
			}
		}
	}()
	if len(ts) == 0 {
		return res, nil
	}
	if err := w.readyK1(); err != nil {
		if !errors.Is(err, ErrOff) {
			res.Closed += len(ts)
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
	if demo {
		if why, err := w.demoHolds(); err != nil {
			return res, err
		} else if why != "" {
			for _, t := range ts {
				res.Held = append(res.Held, ActorHeld{Carrier: t.Carrier, Why: why})
			}
			return res, nil
		}
	}

	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, t := range ts {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		c, wp, err := actors.Revalidate(w.Mem, &t.Carrier, t.Weapon, unverified)
		if errors.Is(err, actors.ErrChanged) {
			res.Held = append(res.Held, ActorHeld{Carrier: t.Carrier, Why: err.Error()})
			continue
		}
		if err != nil {
			note(err)
			break
		}
		ta := c.TimerAddr()
		old, err := mem.ReadU8(w.Mem, ta)
		if err != nil {
			note(err)
			break
		}
		if err := w.wg.checkActor(actorTimer, &c, nil, ta, old, 1); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			res.Refused++
			note(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, ta, 1); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: ta, Off: noOff, Old: old, New: 1})
		w.shrinks = append(w.shrinks, pendingShrink{carrier: c})
		done := ActorDone{Carrier: c}
		back, err := mem.ReadU8(w.Mem, ta)
		if err != nil {
			note(err)
			res.Done = append(res.Done, done)
			break
		}
		if back != 0 && wp != nil {
			w.wg.noteShrunk(&c)
			ca := wp.Addr + addr.ActorOffCarryByte
			wo, err := mem.ReadU8(w.Mem, ca)
			if err != nil {
				note(err)
				res.Done = append(res.Done, done)
				break
			}
			if wo&addr.ActorCarryByteMask != 0 {
				wv := wo &^ addr.ActorCarryByteMask
				if err := w.wg.checkActor(actorWeapon, &c, wp, ca, wo, wv); err != nil {
					res.Refused++
					note(err)
				} else if err := mem.WriteU8(w.Mem, ca, wv); err != nil {
					note(err)
				} else {
					res.Wrote = append(res.Wrote, ByteWrite{Addr: ca, Off: noOff, Old: wo, New: wv})
					done.Released = true
				}
			}
		}
		res.Done = append(res.Done, done)
	}
	return res, first
}

// demoHolds reads Link's mDemo.mDemoType again (daPy_py_c +0x304,
// d_a_player.h:499) and returns why a light-shrink must wait, or "": a
// player demo runs (a Floormaster's or ReDead's grab, the Forsaken
// Fortress Moblin's holdup, d_a_mo2.cpp:2829-2830), or Link is gone.
func (w *FrameWriter) demoHolds() (string, error) {
	p, err := mem.ReadU32(w.Mem, addr.PlayerPtr)
	if err != nil {
		return "", err
	}
	if p == 0 || mem.CheckRange(p+addr.PlayerOffDemoType, 2) != nil {
		return "no Link", nil
	}
	b, err := w.Mem.Read(p+addr.PlayerOffDemoType, 2)
	if err != nil {
		return "", err
	}
	if t := uint16(b[0])<<8 | uint16(b[1]); t != 0 {
		return fmt.Sprintf("link demo %d", t), nil
	}
	return "", nil
}

// verifyShrinks reads each carrier whose timer was written last tick: one
// still queued with its process ID and a timer of 0 lost the write (a
// verify failure); one gone or counting is fine; with the stage changed
// or a save state loaded none is judged. w.mu is held.
func (w *FrameWriter) verifyShrinks(s *state.Snapshot, st gate.Status, rep *VerifyReport) {
	ps := w.shrinks
	w.shrinks = nil
	if len(ps) == 0 {
		return
	}
	n := VerifyReport{}
	for _, p := range ps {
		c := p.carrier
		if s == nil || st.Regressed || s.StageName != w.kiStage() {
			n.Unverified++
			continue
		}
		b, err := w.Mem.Read(c.Addr, addr.ActorSize)
		if err != nil {
			n.Unverified++
			continue
		}
		pid := uint32(b[addr.ActorOffBsPcID])<<24 | uint32(b[addr.ActorOffBsPcID+1])<<16 | uint32(b[addr.ActorOffBsPcID+2])<<8 | uint32(b[addr.ActorOffBsPcID+3])
		if pid != c.PID || b[addr.ActorOffActorTag+addr.TagOffUse] != 1 {
			n.Verified++ // gone, or its block reused: the shrink ran its course
			continue
		}
		t, err := mem.ReadU8(w.Mem, c.TimerAddr())
		switch {
		case err != nil:
			n.Unverified++
		case t != 0:
			n.Verified++
		default:
			n.Failed++
			n.Failures = append(n.Failures, VerifyFailure{Addr: c.TimerAddr(), Off: noOff, Old: 0, New: 1, Read: t})
			detail := fmt.Sprintf("the light-shrink of %s read back 0 on the next tick", c.String())
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
	w.stats.CombatFailed += n.Failed
	w.stats.Unverified += n.Unverified
}

// kiStage is the stage of the tick the light-shrinks were written in: the
// tick's own, as ApplyActors runs within it.
func (w *FrameWriter) kiStage() string { return w.tick.stage }
