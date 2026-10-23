package replica

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The key-door lock clear (design/stale-actor-refresh.md Tier 1):
// FrameWriter.ApplyLocks writes one byte, 0, into a live key door's
// dDoor_key2_c.mbEnabled, the state setKey would have built at create had
// the lock switch been set then (keyOff, d_door.cpp:523-525):
//
//   - the draw skips the lock (d_a_door10.cpp:774);
//   - the key-count gate opens (:359-373), so a receiver with no key (the
//     count is derived, and drops when the partner opens the door) may go
//     through;
//   - keyInit skips its switch write and key decrement while mbEnabled is
//     false (d_door.cpp:433-447), so the write is safe even if the local
//     unlock event was already ordered.
//
// It never reverts: setKey runs again only from actionInit, after Link's
// stay room changes (checkExecute returns 0 when mRoomNo2 is not mStayNo,
// d_door.cpp:164-165; execute's case 0, d_a_door10.cpp:810-812;
// actionInit, :748-759), and by then the switch is set, so setKey keeps
// the lock off itself.

// The lock's layout: dDoor_key2_c (d_door.h:89-96) is a member of each
// door class at lockOff, its bool mbEnabled at +0x00 and its u8 m20,
// nonzero while the unlock animation plays (keyInit, keyProc,
// d_door.cpp:433-462), at +0x20.
const (
	lockOffEnabled = 0x00
	lockOffAnim    = 0x20
)

// lockOff returns the offset of proc's dDoor_key2_c mKeyLock: DOOR10's
// at +0x308 (d_a_door10.h:46), DOOR12's at +0x2E4 (d_a_door12.h:51). Only
// these two are taken; KDDOOR's lock (+0x310, d_a_kddoor.h:106) has no
// small-key placement in DRC or FW (Check note 7).
func lockOff(proc int16) (uint32, bool) {
	switch stage.Proc(proc) {
	case stage.ProcDoor10:
		return 0x308, true
	case stage.ProcDoor12:
		return 0x2E4, true
	}
	return 0, false
}

// LockTarget is one lock clear the plan asks for: the door as the tick's
// walk read it, and its lock switch No (a memory switch, < 0x80).
type LockTarget struct {
	Door actors.Actor
	No   int
}

// LockAddr is the address of t's mbEnabled, 0 for a door of no lock class.
func (t LockTarget) LockAddr() uint32 {
	off, ok := lockOff(t.Door.Proc)
	if !ok {
		return 0
	}
	return t.Door.Addr + off + lockOffEnabled
}

// LockDone is a lock ApplyLocks cleared.
type LockDone struct {
	Door actors.Actor // as Reread read it just before the write
	No   int
}

// LockHeld is a lock ApplyLocks did not clear, and why.
type LockHeld struct {
	Door actors.Actor
	Why  string
}

// LockResult is what ApplyLocks did.
type LockResult struct {
	Result
	Done []LockDone
	Held []LockHeld
}

// pendingLock is a lock cleared, waiting for its verify.
type pendingLock struct {
	door actors.Actor
	a    uint32
}

// linkRefreshRange is how far Link must be from an actor for the refresh
// to touch it (design gate 7): wider than DOOR10's own open check, 110 and
// 250 units (checkArea, d_a_door10.cpp:375). It is measured in the x-z
// plane, which holds more than a 3D distance would.
const linkRefreshRange = 300

// farFrom reports whether p and q are more than linkRefreshRange apart in
// x and z.
func farFrom(p, q [3]float32) bool {
	dx, dz := float64(p[0]-q[0]), float64(p[2]-q[2])
	d := dx*dx + dz*dz
	return !math.IsNaN(d) && d > linkRefreshRange*linkRefreshRange
}

// lockEligible returns "" if door may have its lock cleared, by what was
// read of it, or why not (design gate 6). A door is no K1 enemy, so
// combat.Eligible is not asked: a lock class (DOOR10, DOOR12), mInitState
// 2, eventInfo.mCommand 0 (its own open event is not ordered), no CARRY,
// no enemy-mirror pause bit, and a pid the enemy mirror does not claim.
func lockEligible(d *actors.Actor, claimed func(pid uint32) bool) string {
	_, ok := lockOff(d.Proc)
	switch {
	case !ok:
		return fmt.Sprintf("proc 0x%04X is no key door the refresh takes", uint16(d.Proc))
	case d.InitState != 2:
		return fmt.Sprintf("mInitState %d: not executing", d.InitState)
	case d.EvtCmd != 0:
		return fmt.Sprintf("an event ordered it (mCommand %d)", d.EvtCmd)
	case d.Carried():
		return "CARRY status"
	case d.Pause&addr.ActorPauseMirrorBit != 0:
		return fmt.Sprintf("paused: mirror (mPauseFlag 0x%02X)", d.Pause)
	case claimed != nil && claimed(d.PID):
		return "claimed by the enemy mirror"
	}
	return ""
}

// lockState reads door's lock bytes, mbEnabled and m20, and returns why
// it may not be cleared now, or "": mbEnabled 1 (the lock stands) and m20
// 0 (no unlock animation plays). gone is true when mbEnabled reads 0: the
// lock is open already.
func lockState(m mem.Reader, d *actors.Actor) (why string, gone bool, err error) {
	off, ok := lockOff(d.Proc)
	if !ok {
		return "no lock class", false, nil
	}
	a := d.Addr + off
	if mem.CheckRange(a, lockOffAnim+1) != nil {
		return fmt.Sprintf("its lock at 0x%08X is outside MEM1", a), false, nil
	}
	b, err := m.Read(a, lockOffAnim+1)
	if err != nil {
		return "", false, err
	}
	switch en, anim := b[lockOffEnabled], b[lockOffAnim]; {
	case en == 0:
		return "the lock is open", true, nil
	case en != 1:
		return fmt.Sprintf("mbEnabled reads 0x%02X, not a bool", en), false, nil
	case anim != 0:
		return fmt.Sprintf("the unlock animation plays (m20 %d)", anim), false, nil
	}
	return "", false, nil
}

// readLinkPos reads Link's current.pos through mpPlayerPtr[0]; ok is false
// with no Link.
func readLinkPos(m mem.Reader) (pos [3]float32, ok bool, err error) {
	p, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return pos, false, err
	}
	if p == 0 || mem.CheckRange(p+addr.ActorOffCurPos, 12) != nil {
		return pos, false, nil
	}
	b, err := m.Read(p+addr.ActorOffCurPos, 12)
	if err != nil {
		return pos, false, err
	}
	for i := range pos {
		pos[i] = math.Float32frombits(uint32(b[4*i])<<24 | uint32(b[4*i+1])<<16 | uint32(b[4*i+2])<<8 | uint32(b[4*i+3]))
	}
	return pos, true, nil
}

// memSwitchSet reads memory switch no of the live memBit.
func memSwitchSet(m mem.Reader, no int) (bool, error) {
	st, bit, err := addr.SwitchBit(no)
	if err != nil || st != addr.SwitchMemory {
		return false, fmt.Errorf("replica: switch 0x%02X is no memory switch", no)
	}
	v, err := mem.ReadU8(m, addr.LiveMemBit+uint32(bit.Off))
	if err != nil {
		return false, err
	}
	return v&bit.Mask != 0, nil
}

// readyLock returns nil if the lock clear's gates are open this tick
// (ARMED, IDLE, STABLE, HEARTBEAT), or ErrClosed with why not.
func (w *FrameWriter) readyLock() error {
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
	if why := g.tick.opens(TargetLock, 0); why != "" {
		return fmt.Errorf("%w: %s", ErrClosed, why)
	}
	return nil
}

// ApplyLocks makes the lock clears ts right after the next heartbeat:
//
//  1. before the wait, the gates must be open (readyLock);
//  2. the heartbeat is followed from the snapshot's (waitBeat), then the
//     stage, mNextStage.mEnable, the loaded table and file (recheck), IDLE's
//     inputs (recheckIdle) and Link's mDemo.mDemoType (demoHolds) are read
//     again: a change is ErrAborted, a demo holds every target;
//  3. for each target: the door read again (actors.Reread, and its params,
//     which Reread does not compare: Check note 8), its gates
//     (lockEligible), its lock switch read set again, Link more than 300
//     units away, and its lock bytes (lockState); a door that changed or
//     whose gate closed is held. Else WriteGuard.checkLock, a 1-byte write
//     of 0 and a read back;
//  4. the next Observe reads each cleared door again: a lock that reads 1
//     on the same live door is a verify failure; a door gone is not.
//
// It returns ErrDryRun for a dry run, a *DisabledError while co-op is off,
// ErrClosed, ErrStall, ErrAborted, the first refusal (ErrGuard), ctx's
// error, or a memory error.
func (w *FrameWriter) ApplyLocks(ctx context.Context, ts []LockTarget) (res LockResult, err error) {
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
		w.stats.Locks += len(res.Done)
		w.stats.Held += len(res.Held)
		w.stats.Refused += res.Refused
	}()
	if len(ts) == 0 {
		return res, nil
	}
	if err := w.readyLock(); err != nil {
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
	if why, err := w.demoHolds(); err != nil {
		return res, err
	} else if why != "" {
		for _, t := range ts {
			res.Held = append(res.Held, LockHeld{Door: t.Door, Why: why})
		}
		return res, nil
	}

	claimed := w.wg.combatClaimed()
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	hold := func(t LockTarget, why string) { res.Held = append(res.Held, LockHeld{Door: t.Door, Why: why}) }
	for _, t := range ts {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		typ, err := mem.ReadU32(w.Mem, addr.FopAcType)
		if err != nil {
			note(err)
			break
		}
		d, err := actors.Reread(w.Mem, &t.Door, typ, nil)
		if errors.Is(err, actors.ErrChanged) {
			hold(t, err.Error())
			continue
		}
		if err != nil {
			note(err)
			break
		}
		if d.Params != t.Door.Params {
			hold(t, fmt.Sprintf("%s: params 0x%08X, not 0x%08X as the walk read", d.String(), d.Params, t.Door.Params))
			continue
		}
		if why := lockEligible(&d, claimed); why != "" {
			hold(t, fmt.Sprintf("%s: %s", d.String(), why))
			continue
		}
		set, err := memSwitchSet(w.Mem, t.No)
		if err != nil {
			note(err)
			break
		}
		if !set {
			hold(t, fmt.Sprintf("%s: its lock switch 0x%02X reads clear", d.String(), t.No))
			continue
		}
		lp, ok, err := readLinkPos(w.Mem)
		if err != nil {
			note(err)
			break
		}
		if !ok || !farFrom(lp, d.Pos) {
			hold(t, fmt.Sprintf("%s: Link within %d units", d.String(), linkRefreshRange))
			continue
		}
		why, gone, err := lockState(w.Mem, &d)
		if err != nil {
			note(err)
			break
		}
		if gone {
			continue // setKey ran since the plan (a room change): not stale
		}
		if why != "" {
			hold(t, fmt.Sprintf("%s: %s", d.String(), why))
			continue
		}
		a := LockTarget{Door: d, No: t.No}.LockAddr()
		if err := w.wg.checkLock(&d, a, 1, 0); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			res.Refused++
			note(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, a, 0); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: a, Off: noOff, Old: 1, New: 0})
		w.locks = append(w.locks, pendingLock{door: d, a: a})
		res.Done = append(res.Done, LockDone{Door: d, No: t.No})
		if back, err := mem.ReadU8(w.Mem, a); err != nil {
			note(err)
			break
		} else if back != 0 {
			w.logf("co-op: refresh: the lock clear of %s read back 0x%02X at once (the next Observe judges it)", d.String(), back)
		}
	}
	return res, first
}

// verifyLocks reads each door whose lock was cleared last tick: one still
// queued with its process ID whose mbEnabled reads nonzero lost the write
// (a verify failure); one gone is fine; with the stage changed or a save
// state loaded none is judged. w.mu is held.
func (w *FrameWriter) verifyLocks(s *state.Snapshot, st gate.Status, rep *VerifyReport) {
	ps := w.locks
	w.locks = nil
	if len(ps) == 0 {
		return
	}
	n := VerifyReport{}
	for _, p := range ps {
		d := p.door
		if s == nil || st.Regressed || s.StageName != w.kiStage() {
			n.Unverified++
			continue
		}
		b, err := w.Mem.Read(d.Addr, addr.ActorSize)
		if err != nil {
			n.Unverified++
			continue
		}
		pid := uint32(b[addr.ActorOffBsPcID])<<24 | uint32(b[addr.ActorOffBsPcID+1])<<16 | uint32(b[addr.ActorOffBsPcID+2])<<8 | uint32(b[addr.ActorOffBsPcID+3])
		if pid != d.PID || b[addr.ActorOffActorTag+addr.TagOffUse] != 1 {
			n.Verified++ // gone, or its block reused
			continue
		}
		v, err := mem.ReadU8(w.Mem, p.a)
		switch {
		case err != nil:
			n.Unverified++
		case v == 0:
			n.Verified++
		default:
			n.Failed++
			n.Failures = append(n.Failures, VerifyFailure{Addr: p.a, Off: noOff, Old: 1, New: 0, Read: v})
			detail := fmt.Sprintf("the lock clear of %s read back 0x%02X on the next tick", d.String(), v)
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
