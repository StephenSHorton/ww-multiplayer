package derive

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Max life (§3), in quarter hearts: 12 at the start, 1 a heart piece, 4 a
// container (see the package comment), over the receiver's own flags:
//
//   - a stage file's piece by its placement's own save bit;
//   - a Catalogue reward by the record its Record names (tables.RecordOf):
//     an event bit, or a letter register read. A reward no save bit
//     records (the three minigames, the rewards whose grant is not
//     decompiled) can't be counted;
//   - a container by its table's STAGE_LIFE (hearts' package comment).
//
// Max life is add-only: a write only ever raises it, through
// mItemMaxLifeCount, and a max life above the flags' is kept and noted.
// Its extra, the quarter hearts the table can't account for, is what the
// file holds beyond its flags on the baseline tick and every later rise no
// flag explains (a reward no bit records, or a piece taken twice, §6.7),
// so the partner's pieces are still credited past it.
//
// The HUD refills life to the new maximum's whole hearts on any positive
// count (d_meter.cpp:1426-1427), as it does for a piece the receiver takes
// itself: a partner's piece heals the receiver too. mLife is per player
// (§3); that refill is the game's own, not a co-op write.

// StartLife is a new file's max life, 3 hearts (dSv_player_status_a_c::
// init, d_save.cpp:26).
const StartLife = 12

// LifeCap is the most max life the HUD takes, 20 hearts (dMeter_LifeMove,
// d_meter.cpp:1432-1433): 12 + 44 pieces + 4 * 6 containers.
const LifeCap = 0x50

// LifeFacts are the heart sources in a View.
type LifeFacts struct {
	Pieces, PiecesTaken         int // the stage files' heart pieces, and those taken
	Rewards, Given              int // Catalogue rewards a save bit records, and those given
	Untracked                   int // Catalogue rewards no save bit records
	Containers, ContainersTaken int
	Have                        int // mMaxLife
}

// Derived is 12 + pieces + 4 * containers, the max life the flags give.
func (f LifeFacts) Derived() int {
	return StartLife + f.PiecesTaken + f.Given + 4*f.ContainersTaken
}

// CountLife counts h's heart sources in v.
func CountLife(v save.View, h *hearts.Result) LifeFacts {
	f := LifeFacts{Pieces: len(h.Pieces), Containers: len(h.Containers), Have: v.MaxLife()}
	for _, s := range h.Pieces {
		if set, _ := v.FlagSet(s.Flag); set {
			f.PiecesTaken++
		}
	}
	for _, r := range h.Rewards {
		set, known := v.RecordSet(tables.RecordOf(r))
		switch {
		case !known:
			f.Untracked++
		case set:
			f.Rewards++
			f.Given++
		default:
			f.Rewards++
		}
	}
	for _, s := range h.Containers {
		if set, _ := v.FlagSet(s.Flag); set {
			f.ContainersTaken++
		}
	}
	return f
}

// LifeEnabled reports whether max life may be derived from h, or why not:
// the heart table must pass its M1 check (the vanilla 44 pieces and 6
// containers, every piece recorded by a saved bit, no two containers on
// one STAGE_LIFE), and every flag must be one View reads.
func LifeEnabled(h *hearts.Result) (bool, string) {
	switch {
	case h == nil:
		return false, "no heart table (coop-tables)"
	case !h.Pass:
		return false, fmt.Sprintf("the M1 heart table fails its check (coop-tables): %d problem(s)", len(h.Problems))
	}
	for i, s := range h.Pieces {
		if !save.Known(s.Flag) {
			return false, fmt.Sprintf("piece %d: %s is no save bit the derive reads", i+1, s.Flag)
		}
	}
	for i, s := range h.Containers {
		if !save.Known(s.Flag) {
			return false, fmt.Sprintf("container %d: %s is no save bit the derive reads", i+1, s.Flag)
		}
	}
	return true, ""
}

// LifeTarget is the max life f's flags and extra give, capped at LifeCap.
func LifeTarget(f LifeFacts, extra int) int { return min(f.Derived()+extra, LifeCap) }

// PlanLife plans the raise of max life to LifeTarget(f, extra) through
// mItemMaxLifeCount. ok is false unless that is a raise: max life is
// never lowered. The caller checks the gates: IDLE, and that the counter
// reads 0.
func PlanLife(f LifeFacts, extra int) (w Write, ok bool) {
	want := LifeTarget(f, extra)
	if want <= f.Have {
		return Write{}, false
	}
	why := fmt.Sprintf("max life: %d -> %d quarter hearts: %d of %d piece(s), %d of %d reward(s), %d of %d container(s) taken",
		f.Have, want, f.PiecesTaken, f.Pieces, f.Given, f.Rewards, f.ContainersTaken, f.Containers)
	if extra > 0 {
		why += fmt.Sprintf(", %d kept that no flag records", extra)
	}
	return counterWrite(LifeCounter, addr.ItemMaxLifeCount, want-f.Have, why), true
}

// placedIn reports whether any of at is in stage.
func placedIn(at []itemsrc.Where, stage string) bool {
	for _, w := range at {
		if w.Stage == stage {
			return true
		}
	}
	return false
}

// lifeState is the Deriver's memory of max life.
type lifeState struct {
	h *hearts.Result

	base  bool
	extra int

	sig     lifeSig
	sigHave bool
	changed time.Time

	pend *pending
}

type lifeSig struct {
	have, derived int
	counter       int16
}

func newLifeState(h *hearts.Result) *lifeState { return &lifeState{h: h} }

func (l *lifeState) reset() { l.base, l.extra, l.sigHave, l.pend = false, 0, false, nil }

// tick derives max life for one tick.
func (l *lifeState) tick(d *Deriver, c *tick, p *Plan) {
	now := c.in.Now
	f := CountLife(c.v, l.h)
	counter := c.in.Snap.MaxLifeCount
	if sig := (lifeSig{f.Have, f.Derived(), counter}); !l.sigHave || sig != l.sig {
		l.sig, l.sigHave, l.changed = sig, true, now
	}
	if l.pend.landed(f.Have, now) {
		l.pend = nil
	}
	if !l.base {
		if counter != 0 {
			return // a count lands this frame: measure after it
		}
		l.base, l.extra = true, max(0, f.Have-f.Derived())
		if l.extra > 0 {
			p.note("max life: keeping %d quarter heart(s) beyond the %d its flags give: rewards no save bit records, or duplicates",
				l.extra, f.Derived())
		}
	}
	if l.pend != nil || counter != 0 || !d.settled(c, l.changed) {
		return
	}
	if over := f.Have - (f.Derived() + l.extra); over > 0 {
		l.extra += over
		p.note("max life: %d is %d quarter heart(s) above what its flags give (%d, with %d kept before): "+
			"a duplicate pickup or a reward no save bit records; kept, max life is add-only",
			f.Have, over, f.Derived(), l.extra-over)
	}
	if !c.canWrite() || !c.in.Gates.CanWriteCounter(gate.MaxLife) {
		return
	}
	w, ok := PlanLife(f, l.extra)
	if !ok {
		return
	}
	p.Writes = append(p.Writes, w)
	l.pend = &pending{want: LifeTarget(f, l.extra), prev: f.Have, expires: now.Add(d.inFlight)}
}
