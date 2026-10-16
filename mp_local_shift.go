package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

const (
	// startupShiftOverlap is how close the two Links must be, in world
	// units across the ground (X/Z) and in height (Y), to count as
	// overlapping.
	startupShiftOverlap = 5

	// startupShiftSettle is how long the shift keeps looking while only
	// STABLE or HEARTBEAT is closed on a Dolphin: just after a save-state
	// load, STABLE needs gate.StableTicks unchanged ticks.
	startupShiftSettle = 3 * time.Second

	// startupShiftWarpTimeout is how long the warp may wait for the C
	// side before it is withdrawn (warpLinkMem).
	startupShiftWarpTimeout = time.Second
)

// shiftEnv is how the startup shift reaches the two Dolphins. Production
// uses defaultShiftEnv; the tests point it at two FakeGames.
type shiftEnv struct {
	// open opens the Dolphin of pid, and returns how to close it.
	open func(pid uint32) (mem.Mem, func(), error)
	// tick waits between the snapshots the gates judge (coopTick).
	tick func()
	// settle is startupShiftSettle.
	settle time.Duration
}

func defaultShiftEnv() shiftEnv {
	return shiftEnv{
		open: func(pid uint32) (mem.Mem, func(), error) {
			d, err := openDolphin(pid)
			if err != nil {
				return nil, nil, err
			}
			return mem.NewDolphinMem(d), d.Close, nil
		},
		tick:   coopTick,
		settle: startupShiftSettle,
	}
}

// startupShift is mp-local's startup shift. When both Dolphins boot from
// the same SAVE_STATE, both Links stand at the same world coords and
// visually overlap, so it moves Dolphin B's Link by d from where it
// stands. planShift says when, and why not. The move goes through B's
// warp request (warpLinkMem), which is outside co-op's gates and
// WriteGuard, so under --coop (its dry run included) it never runs; the
// two Links then overlap until one walks a few steps away. d all 0
// (MP_LOCAL_SHIFT_X/Y/Z=0) turns it off.
//
// It used to write A's position + d into B on every start, whatever stage
// either was in, under --coop too. With A in a dungeon and B at sea, that
// is the sea's centre square, where B voids out (docs/08 Appendix C item
// 130).
func startupShift(rep report.Reporter, coopOn bool, pidA, pidB uint32, d [3]float32, env shiftEnv) {
	if coopOn {
		ignored := ""
		if shiftVarsSet() {
			ignored = " (MP_LOCAL_SHIFT_X/Y/Z ignored)"
		}
		report.Logf(rep, report.Info, "Not moving Dolphin B's Link at start under --coop%s: the move writes Link's position outside co-op's gates and WriteGuard. If the two Links overlap, walk one a few steps away.", ignored)
		return
	}
	if d == ([3]float32{}) {
		return
	}
	mA, closeA, err := env.open(pidA)
	if err != nil {
		report.Logf(rep, report.Warn, "couldn't warp Dolphin B Link: open A: %v", err)
		return
	}
	defer closeA()
	mB, closeB, err := env.open(pidB)
	if err != nil {
		report.Logf(rep, report.Warn, "couldn't warp Dolphin B Link: open B: %v", err)
		return
	}
	defer closeB()

	deadline := time.Now().Add(env.settle)
	for {
		a, b, err := shiftSides(mA, mB, env.tick)
		if err != nil {
			report.Logf(rep, report.Warn, "couldn't warp Dolphin B Link: %v", err)
			return
		}
		target, skip, retry := planShift(a, b, d)
		switch {
		case skip == "":
			if err := warpLinkMem(mB, target, startupShiftWarpTimeout); err != nil {
				report.Logf(rep, report.Warn, "couldn't warp Dolphin B Link: %v", err)
			} else {
				report.Logf(rep, report.OK, "Warped Dolphin B Link by (%.0f, %.0f, %.0f) so the two players don't overlap.", d[0], d[1], d[2])
			}
			return
		case !retry:
			report.Logf(rep, report.Info, "Not moving Dolphin B's Link at start: %s.", skip)
			return
		case !time.Now().Before(deadline):
			report.Logf(rep, report.Info, "Not moving Dolphin B's Link at start: %s (still so after %s).", skip, env.settle)
			return
		}
	}
}

// shiftVarsSet reports whether any of MP_LOCAL_SHIFT_X/Y/Z is set to
// something, as envFloat32 sees it: an empty one is unset.
func shiftVarsSet() bool {
	for _, k := range []string{"MP_LOCAL_SHIFT_X", "MP_LOCAL_SHIFT_Y", "MP_LOCAL_SHIFT_Z"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// shiftSide is what the startup shift reads of one Dolphin: the last of
// its gate snapshots, the gates over them, and Link's current.pos (read
// only while the gates allow an IDLE write).
type shiftSide struct {
	s   *snap.Snapshot
	st  gate.Status
	pos [3]float32
}

// shiftSides reads both Dolphins: each one's gates over gate.StableTicks
// ticks (gateSnapshots), B's last since B is the one written, then both
// Links' positions.
func shiftSides(mA, mB mem.Mem, tick func()) (a, b shiftSide, err error) {
	sides := []struct {
		name string
		m    mem.Mem
		side *shiftSide
	}{{"A", mA, &a}, {"B", mB, &b}}
	for _, x := range sides {
		s, st, _, err := gateSnapshots(x.m, tick)
		if err != nil {
			return a, b, fmt.Errorf("read Dolphin %s: %w", x.name, err)
		}
		x.side.s, x.side.st = s, st
	}
	for _, x := range sides {
		if !x.side.st.CanWriteIdle() {
			continue
		}
		p, err := x.m.Read(x.side.s.PlayerPtr+addr.ActorOffCurPos, 12)
		if err != nil {
			return a, b, fmt.Errorf("read Dolphin %s's Link: %w", x.name, err)
		}
		for i := range x.side.pos {
			x.side.pos[i] = math.Float32frombits(be32(p[4*i:]))
		}
	}
	return a, b, nil
}

// planShift decides the startup shift from what shiftSides read. It moves
// B only when all of these hold:
//
//   - d is not all 0, and is finite (envFloat32 takes "NaN" and "Inf");
//   - both games allow an IDLE write (gate.Status.CanWriteIdle): in game,
//     settled, no stage change pending, no event, menu, minigame or
//     refight, and the heartbeat moving;
//   - both are in the same stage, layer, save table and room (mStayNo,
//     known);
//   - the two Links overlap: within startupShiftOverlap across the ground
//     and in height.
//
// The target is then B's own position + d, in B's own stage by
// construction. After one shift the Links stand about 70 units apart, so
// the next start leaves them be. Otherwise skip says why not, and retry
// is true when only STABLE or HEARTBEAT was closed (a save state still
// settling, a stage still loading, emulation paused), which another look
// may find open. A gate's reason is written in parentheses, as some end in
// a question mark.
func planShift(a, b shiftSide, d [3]float32) (target [3]float32, skip string, retry bool) {
	if d == ([3]float32{}) {
		return target, "MP_LOCAL_SHIFT_X/Y/Z are all 0", false
	}
	for _, v := range d {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return target, fmt.Sprintf("MP_LOCAL_SHIFT_X/Y/Z (%v, %v, %v) must be numbers", d[0], d[1], d[2]), false
		}
	}
	sides := []struct {
		name string
		side shiftSide
	}{{"A", a}, {"B", b}}
	// A closed IDLE gate (an event, a menu, a minigame, a refight) can stay
	// closed for minutes, so the shift does not wait for it. It is the
	// reason named, even when STABLE or HEARTBEAT is closed too, on that
	// side or the other: another look would not help until it opens.
	for _, x := range sides {
		if !x.side.st.Idle {
			return target, fmt.Sprintf("Dolphin %s (%s)", x.name, x.side.st.IdleWhy), false
		}
	}
	for _, x := range sides {
		if !x.side.st.CanWriteIdle() {
			return target, fmt.Sprintf("Dolphin %s (%s)", x.name, x.side.st.Why()), true
		}
	}
	sa, sb := a.s, b.s
	switch {
	case sa.StageName != sb.StageName:
		return target, fmt.Sprintf("A is in %s, B in %s", sa.StageName, sb.StageName), false
	case sa.StageLayer != sb.StageLayer:
		return target, fmt.Sprintf("A is in layer %d of %s, B in layer %d", sa.StageLayer, sa.StageName, sb.StageLayer), false
	case sa.Table != sb.Table:
		return target, fmt.Sprintf("A's save table is %d %s, B's %d %s", sa.Table, tableName(int(sa.Table)), sb.Table, tableName(int(sb.Table))), false
	case sa.StayNo < 0 || sb.StayNo < 0:
		// STABLE has seen mStayNo unchanged for StableTicks ticks, so another
		// look would most likely find it the same: no retry, as for another
		// room.
		return target, fmt.Sprintf("the room is not known (mStayNo %d on A, %d on B)", sa.StayNo, sb.StayNo), false
	case sa.StayNo != sb.StayNo:
		return target, fmt.Sprintf("A is in room %d of %s, B in room %d", sa.StayNo, sa.StageName, sb.StayNo), false
	}
	dx, dy, dz := float64(a.pos[0]-b.pos[0]), float64(a.pos[1]-b.pos[1]), float64(a.pos[2]-b.pos[2])
	// Written so that a NaN position fails it.
	if !(math.Hypot(dx, dz) <= startupShiftOverlap && math.Abs(dy) <= startupShiftOverlap) {
		return target, fmt.Sprintf("the two Links are %.0f units apart", math.Sqrt(dx*dx+dy*dy+dz*dz)), false
	}
	for i := range target {
		target[i] = b.pos[i] + d[i]
	}
	return target, "", false
}
