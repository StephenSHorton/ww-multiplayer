// Package gate decides, from one snapshot per tick, whether co-op may
// write game memory (§3.2). Three gates are evaluated:
//
//   - STABLE, needed for any write: a stage with Link is loaded, it is not
//     the title or file select, a save table is loaded, no stage change is
//     pending, and the stage and room have not changed for StableTicks
//     ticks;
//   - IDLE, needed for every write but the stage progress bits and memory
//     switches of a save table other than the loaded one (STABLE alone,
//     §6.3) and the story bits and registers (the transition tick, §6.5):
//     no event, pause menu, minigame or boss-refight snapshot, and the
//     pending counter about to be written is 0;
//   - HEARTBEAT: the mod's per-frame counter advanced since the last tick.
//     When it stalls (emulation paused, a vanilla ISO) every write stops.
//
// ARMED (the file's binding tag, §5.2) is the replica's business, not
// this package's. A closed gate always says why, in words the TUI can
// show.
package gate

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
)

// StableTicks is how many ticks the stage and room must stay unchanged
// before STABLE opens (§3.2). At 10 Hz that is the "at least 4 ticks or
// about 400 ms" after a room change of §6.3: a change first seen at tick
// T opens STABLE at T+4.
const StableTicks = 4

// Counter is one of the HUD's pending counters Go may write (§3.2 IDLE,
// §6.4).
type Counter int

const (
	KeyNum   Counter = iota // mItemKeyNumCount
	MaxLife                 // mItemMaxLifeCount
	MaxMagic                // mItemMaxMagicCount
	counters
)

func (c Counter) String() string {
	switch c {
	case KeyNum:
		return "key"
	case MaxLife:
		return "max-life"
	case MaxMagic:
		return "max-magic"
	}
	return fmt.Sprintf("Counter(%d)", int(c))
}

// Status is the gates' verdict for one tick.
type Status struct {
	Stable    bool
	Idle      bool
	Heartbeat bool

	// OnTransition is true on the first tick of a stage change: mNextStage
	// was just enabled, and every other STABLE condition holds, the 4
	// unchanged ticks included. It is the one tick STABLE's "no stage
	// change pending" rule is waived for, so story bits can be written as
	// the receiver leaves the stage (§6.5).
	OnTransition bool

	// Why each gate is closed; "" while it is open.
	StableWhy, IdleWhy, HeartbeatWhy string

	// StableRun is the number of ticks before this one, in a row, with
	// the same stage and room under STABLE's other conditions: 0 on the
	// tick the stage or room changed. STABLE needs StableTicks.
	StableRun int
	// IdleRun is the number of IDLE ticks in a row, this one included.
	IdleRun int
	// Stalled is the number of ticks in a row the heartbeat has not
	// advanced; Regressed is set when it went backwards this tick (a save
	// state was loaded, §5.5).
	Stalled   int
	Regressed bool

	pending [counters]int16
}

// CanWrite reports whether any write may be made: STABLE and HEARTBEAT.
func (s Status) CanWrite() bool { return s.Stable && s.Heartbeat }

// CanWriteIdle reports whether a write that also needs IDLE may be made.
func (s Status) CanWriteIdle() bool { return s.CanWrite() && s.Idle }

// CanWriteCounter reports whether counter c may be written: IDLE writes
// are allowed and c reads 0, so the HUD has consumed any earlier count.
func (s Status) CanWriteCounter(c Counter) bool {
	return s.CanWriteIdle() && s.Pending(c) == 0
}

// CanWriteOnTransition reports whether an on-transition write (§6.5) may
// be made this tick.
func (s Status) CanWriteOnTransition() bool { return s.OnTransition && s.Heartbeat }

// Pending returns counter c's value this tick.
func (s Status) Pending(c Counter) int16 {
	if c < 0 || c >= counters {
		return 0
	}
	return s.pending[c]
}

// Why returns the reason the first closed gate gives, heartbeat first,
// then STABLE, then IDLE, or "" when all three are open.
func (s Status) Why() string {
	for _, w := range []string{s.HeartbeatWhy, s.StableWhy, s.IdleWhy} {
		if w != "" {
			return w
		}
	}
	return ""
}

// key is what "the stage and room" means for the unchanged-ticks rule:
// all of mCurStage, the loaded save table and mStayNo.
type key struct {
	stage         string
	point         int16
	room, layer   int8
	table, stayNo int8
}

func keyOf(s *snap.Snapshot) key {
	return key{s.StageName, s.StagePoint, s.StageRoom, s.StageLayer, s.Table, s.StayNo}
}

// Tracker evaluates the gates over a snapshot history, one Observe per
// tick, oldest first. The zero Tracker is ready to use. It is not safe for
// concurrent use.
type Tracker struct {
	have      bool // prevKey and prevHB hold the last tick's
	prevKey   key
	prevHB    uint32
	stableRun int
	idleRun   int
	stalled   int
	moved     bool // the heartbeat has advanced since the history began
}

// Observe takes the tick's snapshot and returns the gates' status. A nil
// snapshot is a tick whose read failed: every gate closes and the history
// starts over.
func (t *Tracker) Observe(s *snap.Snapshot) Status {
	if s == nil {
		*t = Tracker{}
		const why = "the game state could not be read"
		return Status{StableWhy: why, IdleWhy: why, HeartbeatWhy: why}
	}
	var st Status
	k := keyOf(s)
	same := t.have && k == t.prevKey

	// HEARTBEAT. A regression is a discontinuity like a stage change, so
	// the STABLE history restarts with it.
	switch d := int32(s.Heartbeat - t.prevHB); {
	case !t.have:
		st.HeartbeatWhy = "waiting for a second snapshot to see the heartbeat move"
	case d > 0:
		st.Heartbeat = true
		t.stalled = 0
		t.moved = true
	case d == 0:
		t.stalled++
		if t.moved {
			st.HeartbeatWhy = fmt.Sprintf("heartbeat stalled for %d ticks: is emulation paused?", t.stalled)
		} else {
			st.HeartbeatWhy = "heartbeat has not moved: is emulation paused, or the ISO not patched?"
		}
	default:
		st.Regressed = true
		st.HeartbeatWhy = "heartbeat went backwards: a save state was loaded"
		t.stalled = 0
		same = false
	}
	st.Stalled = t.stalled

	// STABLE. stableRun counts the ticks in a row, this one included,
	// that met every condition but the tick count with the same key.
	coreWhy := coreClosed(s)
	prevRun := t.stableRun
	switch {
	case coreWhy != "" || s.NextEnable != 0:
		t.stableRun = 0
	case same && prevRun > 0:
		t.stableRun++
	default:
		t.stableRun = 1
	}
	switch {
	case coreWhy != "":
		st.StableWhy = coreWhy
	case s.NextEnable != 0:
		st.StableWhy = "a stage change is pending"
	case t.stableRun-1 < StableTicks:
		st.StableWhy = fmt.Sprintf("settling: stage and room unchanged for %d of %d ticks",
			t.stableRun-1, StableTicks)
	default:
		st.Stable = true
	}
	if t.stableRun > 0 {
		st.StableRun = t.stableRun - 1
	}
	// The tick mNextStage is enabled, after StableTicks stable ones: STABLE
	// in all but the pending change.
	st.OnTransition = coreWhy == "" && s.NextEnable == 1 && same && prevRun >= StableTicks

	// IDLE.
	st.pending = [counters]int16{KeyNum: s.KeyNumCount, MaxLife: s.MaxLifeCount, MaxMagic: s.MaxMagicCount}
	if st.IdleWhy = idleClosed(s); st.IdleWhy == "" {
		st.Idle = true
		t.idleRun++
	} else {
		t.idleRun = 0
	}
	st.IdleRun = t.idleRun

	t.have, t.prevKey, t.prevHB = true, k, s.Heartbeat
	return st
}

// coreClosed checks STABLE's instantaneous conditions other than the
// pending stage change (§3.2) and returns why one fails, or "".
func coreClosed(s *snap.Snapshot) string {
	switch {
	case s.PlayerPtr == 0:
		return "Link is not loaded"
	case s.StageName == "":
		return "no stage is loaded"
	case s.StageName == addr.StageTitle:
		return "on the title screen"
	case s.StageName == addr.StageFileSelect:
		return "on file select"
	case s.Table == -1:
		return "no save table is loaded"
	case !addr.ValidTable(int(s.Table)):
		// getSave asserts 0 <= t < 16 (d_save.cpp:1507), so the game
		// never stores this; a garbage read fails closed.
		return fmt.Sprintf("save table %d is not valid", s.Table)
	}
	return ""
}

// idleClosed checks IDLE's conditions other than the per-counter one
// (§3.2) and returns why one fails, or "".
func idleClosed(s *snap.Snapshot) string {
	switch {
	case s.EvtMode != 0:
		return "an event is running"
	case s.EvtOrderCount != 0:
		return "an event is queued"
	case s.MenuPause != 0:
		return "the pause menu is open"
	case s.MiniGameType != 0:
		return "a minigame is running"
	case s.RefightStageNo != 0:
		return "a boss refight is in progress"
	}
	return ""
}

// Evaluate runs a fresh Tracker over history, oldest first, and returns
// the status of the last tick.
func Evaluate(history []*snap.Snapshot) Status {
	var t Tracker
	var st Status
	for _, s := range history {
		st = t.Observe(s)
	}
	return st
}
