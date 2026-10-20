package enemysync

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The full-body heap gate (ww-multiplayer-handoff/design/
// remote-link-stage4-plan.md D4-3). With FULL_BODIES set the host's Go
// decides, every Tick, which slot may begin one daPy_lk_c create
// (FULL_ALLOW, one bit at most) and why the gate is shut (HEAP_REASON).
// It fails closed: no fresh sample, a stage still settling, a heap error
// counter that rose, a FULL body still being created or too little heap
// all leave every bit clear. The REL waits while the reason is transient
// (NONE, NO_SAMPLE, INFLIGHT, STAGE_SETTLE, QUEUED) and creates a lite
// body at once for any other reason, so a reason is only reported for
// what holds on this Tick's inputs. The bit goes only to a slot the REL
// can create in (createEligible); while another such slot waits for a
// body the open gate says QUEUED, so that slot waits its turn instead of
// falling back to lite. A slot whose position is still crossing a jump
// (settling, players.go SettleDist) waits too: it counts as waiting but
// gets no bit until it arrives.

// Heap gate timing.
const (
	// HeapSampleFresh is how old a sample may be and still open the gate.
	HeapSampleFresh = 500 * time.Millisecond
	// HeapWalkFast is the walk cadence while an active slot has no body
	// (it may want a full one, a settling one too); HeapWalkSlow
	// otherwise (for the log).
	HeapWalkFast = 250 * time.Millisecond
	HeapWalkSlow = 2 * time.Second
	// heapSettleSamples is how many good samples a new stage needs.
	heapSettleSamples = 2
	// heapProblemStreak is how many walks in a row must report a Problem
	// before the gate says WALK_PROBLEM (which makes the REL fall back at
	// once); fewer read as NO_SAMPLE and walk again on the next Tick. The
	// game runs during a walk, so one inconsistent list is expected now
	// and then.
	heapProblemStreak = 3
)

// heapGate is the host's gate state. Its zero value with th and fullMax
// set is a gate before the first stage.
type heapGate struct {
	th      heap.Thresholds
	fullMax int

	flux       bool // a stage change is under way: the next in-play Tick takes the baseline
	haveBase   bool
	base       heap.Counters // the first sample of the stage
	solidBase  uint32        // solid at the last Tick without a FULL body QUEUED
	closed     bool          // a counter rose this stage
	prevClosed bool          // the stage before was closed by a counter
	sticky     bool          // two stages in a row closed: shut for the session

	haveSample bool // a sample in this stage
	sample     heap.Sample
	sampleAt   time.Time
	good       int // good samples this stage
	problems   int // walks in a row with a Problem
	nextWalk   time.Time

	inflight bool      // the last status had a FULL body QUEUED
	reopenAt time.Time // the gate opens only on a sample taken after this

	allow, reason uint8 // the last verdict
	counters      heap.Counters
	Samples       uint64
	WalkErrors    uint64
}

func newHeapGate(th heap.Thresholds, fullMax int) *heapGate {
	return &heapGate{th: th, fullMax: fullMax, flux: true}
}

// newStage starts a stage's rules from the counters c.
func (g *heapGate) newStage(c heap.Counters, now time.Time) {
	g.prevClosed, g.closed = g.closed, false
	g.flux, g.haveBase, g.base, g.solidBase = false, true, c, c.Solid
	g.haveSample, g.good, g.problems = false, 0, 0
	g.nextWalk = now
}

func (g *heapGate) closeByCounters() {
	if !g.closed {
		g.closed = true
		if g.prevClosed {
			g.sticky = true
		}
	}
}

// shanmAllocated reports whether the REL's shanm table exists: the word
// at OffRELShanm points at a block whose header carries the magic, the
// size and its own address (the REL treats anything else as 0).
func shanmAllocated(m mem.Reader) (bool, error) {
	p, err := mem.ReadU32(m, mailbox.Mailbox+mailbox.OffRELShanm)
	if err != nil || p == 0 || p&3 != 0 || mem.CheckRange(p, mailbox.RemoteSASelfOff+4) != nil {
		return false, err
	}
	h, err := m.Read(p, mailbox.RemoteSASelfOff+4)
	if err != nil || len(h) != mailbox.RemoteSASelfOff+4 {
		return false, nil
	}
	return mem.U32BE(h[mailbox.RemoteSAMagicOff:]) == mailbox.RemoteShanmMagic &&
		mem.U32BE(h[mailbox.RemoteSASizeOff:]) == mailbox.RemoteShanmBytes &&
		mem.U32BE(h[mailbox.RemoteSASelfOff:]) == p, nil
}

// tick runs the gate once at now and returns FULL_ALLOW and HEAP_REASON.
// stageChanged is the host's stage-name change this Tick; st is the REL's
// status as read at stAt (the Tick before); slots are the block's slots
// about to be written. Read errors are counted and shut the gate
// (NO_SAMPLE) for the Tick; they are returned for the log.
func (g *heapGate) tick(m mem.Reader, now time.Time, scope Scope, stageChanged bool, st *mailbox.RemoteStatus, stAt time.Time,
	slots *[mailbox.RemoteMaxSlots]mailbox.RemoteSlot, settling *[mailbox.RemoteMaxSlots]bool) (uint8, uint8, error) {
	allow, reason, err := g.verdict(m, now, scope, stageChanged, st, stAt, slots, settling)
	g.allow, g.reason = allow, reason
	return allow, reason, err
}

// createEligible reports whether the REL can create a body in slot s
// now: active, and its stage (a fresh PSTATE's; a stale one leaves it
// empty) the host's (remote.c rl_slot_may_create -> rl_stage_is).
func createEligible(s *mailbox.RemoteSlot, scope Scope) bool {
	stage := scope.Stage
	if len(stage) > StageLen {
		stage = stage[:StageLen]
	}
	return s.Active && s.Stage != "" && s.Stage == stage
}

func (g *heapGate) verdict(m mem.Reader, now time.Time, scope Scope, stageChanged bool, st *mailbox.RemoteStatus, stAt time.Time,
	slots *[mailbox.RemoteMaxSlots]mailbox.RemoteSlot, settling *[mailbox.RemoteMaxSlots]bool) (uint8, uint8, error) {
	if stageChanged || scope.Pending {
		g.flux = true
	}

	// The REL's bodies: a FULL one QUEUED, FULL ones live, slots that
	// have no body and can get one (only those can take a FULL create; a
	// lite body is never upgraded; BERR_TRIES never creates again this
	// stage). The bit goes to the lowest of those, preferring one that has
	// not already tried a FULL body (EMPTY with Kind FULL and Tries: most
	// likely its full attempt is spent and its next body is lite).
	inflight, liveFull, want, spent, waiting := false, 0, -1, -1, 0
	for i := range mailbox.MaxRemoteLinks {
		b := &st.Bodies[i]
		switch {
		case b.Kind == mailbox.RemoteKindFull && b.State == mailbox.RemoteBodyQueued:
			inflight = true
		case b.Kind == mailbox.RemoteKindFull && (b.State == mailbox.RemoteBodyLive || b.State == mailbox.RemoteBodyParked):
			liveFull++
		}
		if b.State != mailbox.RemoteBodyEmpty || b.Err == mailbox.RemoteBErrTries || !createEligible(&slots[i], scope) {
			continue
		}
		waiting++
		switch {
		case settling[i]:
			// Still crossing a jump: it waits (QUEUED) for its position.
		case b.Kind == mailbox.RemoteKindFull && b.Tries > 0:
			if spent < 0 {
				spent = i
			}
		case want < 0:
			want = i
		}
	}
	if want < 0 {
		want = spent
	}
	if g.inflight && !inflight {
		// The create ended (LIVE or EMPTY): reopen only on a sample taken
		// after the status that showed it, and take one now.
		g.reopenAt, g.nextWalk = stAt, now
	}
	g.inflight = inflight

	if !scope.InPlay {
		if !g.haveBase || g.flux {
			return 0, mailbox.RemoteHGStageSettle, nil
		}
		return 0, mailbox.RemoteHGNoSample, nil
	}
	c, err := heap.ReadCounters(m)
	if err != nil {
		g.WalkErrors++
		return 0, mailbox.RemoteHGNoSample, err
	}
	g.counters = c
	if g.flux || !g.haveBase {
		g.newStage(c, now)
	} else {
		if c.Game > g.base.Game || c.Zelda > g.base.Zelda || (inflight && c.Solid > g.solidBase) {
			g.closeByCounters()
		}
		if !inflight {
			g.solidBase = c.Solid
		}
	}

	if (want >= 0 || waiting > 0) && g.nextWalk.After(now.Add(HeapWalkFast)) {
		// A slot began waiting during a slow walk: walk at once rather
		// than spend the REL's FULL wait on NO_SAMPLE.
		g.nextWalk = now
	}
	var walkErr error
	if !now.Before(g.nextWalk) {
		archive, err := shanmAllocated(m)
		if err == nil {
			g.sample, err = heap.TakeSample(m, !archive)
		}
		if err != nil {
			g.WalkErrors++
			walkErr = err
			g.haveSample = false
		} else {
			g.Samples++
			g.haveSample, g.sampleAt = true, now
			if g.sample.Game.Problem != "" || g.sample.Zelda.Problem != "" || (g.sample.ArchiveWalked && g.sample.Archive.Problem != "") {
				g.problems++
			} else {
				g.problems = 0
				g.good++
			}
		}
		switch {
		case g.problems > 0 && g.problems < heapProblemStreak:
			g.nextWalk = now
		case want >= 0 || waiting > 0:
			g.nextWalk = now.Add(HeapWalkFast)
		default:
			g.nextWalk = now.Add(HeapWalkSlow)
		}
	}

	switch {
	case g.sticky:
		return 0, mailbox.RemoteHGSticky, walkErr
	case g.closed:
		return 0, mailbox.RemoteHGCounterRose, walkErr
	case inflight:
		return 0, mailbox.RemoteHGInflight, walkErr
	case liveFull >= g.fullMax:
		return 0, mailbox.RemoteHGFullMax, walkErr
	case !g.haveSample || (want >= 0 && now.Sub(g.sampleAt) > HeapSampleFresh):
		// With no slot waiting the walk is slow and the last sample only
		// names the reason (no bit is given), so it may be older.
		return 0, mailbox.RemoteHGNoSample, walkErr
	case !g.sampleAt.After(g.reopenAt):
		return 0, mailbox.RemoteHGInflight, walkErr
	case g.problems >= heapProblemStreak:
		return 0, mailbox.RemoteHGWalkProblem, walkErr
	case g.problems > 0:
		return 0, mailbox.RemoteHGNoSample, walkErr
	case g.good < heapSettleSamples:
		return 0, mailbox.RemoteHGStageSettle, walkErr
	}
	if ok, r := g.th.Gate(g.sample); !ok {
		return 0, r, walkErr
	}
	if want < 0 {
		if waiting > 0 {
			// Every waiting slot is settling: they hold for their bit.
			return 0, mailbox.RemoteHGQueued, walkErr
		}
		return 0, mailbox.RemoteHGOpen, walkErr
	}
	if waiting > 1 {
		// Another slot waits for a body: it holds (QUEUED) while this one
		// creates; the next verdict hands it the bit or the reason.
		return 1 << want, mailbox.RemoteHGQueued, walkErr
	}
	return 1 << want, mailbox.RemoteHGOpen, walkErr
}

// line is the gate for the host's status line.
func (g *heapGate) line() string {
	s := fmt.Sprintf("gate %s allow 0x%02X", hgName(g.reason), g.allow)
	if g.haveSample {
		s += fmt.Sprintf(" game largest 0x%X free 0x%X zelda largest 0x%X", g.sample.Game.Largest, g.sample.Game.Free, g.sample.Zelda.Largest)
		if g.sample.ArchiveWalked {
			s += fmt.Sprintf(" archive largest 0x%X", g.sample.Archive.Largest)
		}
	}
	return s + fmt.Sprintf(" counters game %d zelda %d solid %d", g.counters.Game, g.counters.Zelda, g.counters.Solid)
}

// logChange logs a reason change (Info) with the sample behind it.
func (g *heapGate) logChange(rep log.Reporter, was uint8) {
	if g.reason != was {
		log.Logf(rep, log.Info, "enemy remote: heap %s (was %s)", g.line(), hgName(was))
	}
}

// remoteHGNames name the RemoteHG* reasons by value.
var remoteHGNames = [...]string{"none", "open", "no_sample", "walk_problem", "game_largest", "game_free", "zelda_largest",
	"counter_rose", "inflight", "stage_settle", "sticky", "archive_largest", "full_max", "queued"}

func hgName(r uint8) string {
	if int(r) < len(remoteHGNames) {
		return remoteHGNames[r]
	}
	return fmt.Sprintf("hg%d", r)
}
