package replica

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// The M4 receiver (§9 M4): what the world's progression gives the
// receiver's game beyond M3's bits, each class by its own plan and under
// its own gate, every write through the WriteGuard:
//
//   - story bits and the allowlisted event registers (story.Receiver):
//     the world's that the game lacks, less its ever-had set (the
//     Planner's) and the bits class.Holds keeps back until their
//     condition holds in this game, written only on the TRANSITION tick
//     (the first tick mNextStage.mEnable reads 1 after gate.StableTicks
//     stable ones, with HEARTBEAT and ARMED: gate.Status.
//     CanWriteOnTransition, Tick.opens), into the saved dSv_event_c,
//     once per stage change, through the merge path. They are apply-once:
//     the bits written go into the ever-had set (applied);
//   - the inventory (inventory.Pending): its flags by OR and its
//     capacities by MAX through the merge path, the item slots, the
//     equipment (at the next stage entry, inventory.Entry) and the
//     buttons' items through the derive path, when IDLE. It is
//     story-independent: it does not wait for a stage change;
//   - small keys, max life and max magic (derive.Deriver), from this
//     game's own flags: a key count into the pending counter or the key
//     byte, max life and max magic into their pending counters, when IDLE
//     and settled, a counter only while it reads 0, through the derive
//     path. The FrameWriter then watches each counter drain (DrainFrames).
//
// Each class is written only while the hub grants its feature
// (proto.FeatureStory, FeatureInventory, FeatureKeys, FeatureLife,
// FeatureMagic) and the replica is armed with the world's view. A dry run
// (--coop-dry-run) runs the same plans, whatever is granted, and logs the
// writes it would make: the merge-path bytes through logPlan, the derive
// path's and the stage-change story writes here.

// maxM4Logged bounds the lines m4 remembers having said.
const maxM4Logged = 512

// m4 is the replica's M4 receiver state.
type m4 struct {
	story   story.Receiver
	entry   inventory.Entry
	deriver *derive.Deriver
	// ticks is the Deriver's clock, TickEvery a tick: its settle and
	// in-flight times count ticks the replica saw, so a slow tick only
	// makes them longer.
	ticks int64

	// This tick's derive-path writes, made once the lock is released and
	// the merge path's writes are made (Replica.applyDerived), and the
	// derive.Write each came from (nil: inventory's).
	derived []DeriveWrite
	from    []*derive.Write

	logged    map[string]bool // dry-run and hold lines said while the replica stays armed
	granted   proto.Feature   // the features the "not granted" lines were said for
	ungranted proto.Feature   // the features whose "not granted" line was said
}

func newM4(t *tables.Tables) m4 {
	return m4{deriver: derive.New(derive.Config{Tables: t}), logged: map[string]bool{}}
}

// now is the Deriver's clock.
func (m *m4) now() time.Time {
	return time.Unix(1_700_000_000, 0).Add(time.Duration(m.ticks) * TickEvery)
}

// take returns this tick's derive-path writes and forgets them.
func (m *m4) take() m4Writes {
	ws := m4Writes{ds: m.derived, from: m.from}
	m.derived, m.from = nil, nil
	return ws
}

// m4Writes are one tick's derive-path writes, and where each came from.
type m4Writes struct {
	ds   []DeriveWrite
	from []*derive.Write
}

// say logs line once while the replica stays armed.
func (r *Replica) m4Say(key, line string) {
	m := &r.m4
	if m.logged[key] {
		return
	}
	if len(m.logged) >= maxM4Logged {
		clear(m.logged)
	}
	m.logged[key] = true
	r.logf("co-op: %s", line)
}

// m4Reset is a tick whose snapshot could not be read: the Deriver and the
// story Receiver start over, as the gates do.
func (r *Replica) m4Reset() {
	m := &r.m4
	m.ticks++
	m.derived, m.from = nil, nil
	m.deriver.Tick(derive.Input{Now: m.now()})
	m.story.Reset()
}

// m4Granted reports whether feature f's class is written: granted by the
// hub, or a dry run, which plans every class and writes none. The first
// time a writing replica finds f not granted while it has something to
// write, it says so.
func (r *Replica) m4Granted(f proto.Feature, what string) bool {
	m := &r.m4
	if m.granted != r.features {
		m.granted, m.ungranted = r.features, 0
	}
	switch {
	case r.writer.DryRun(), r.features&f != 0:
		return true
	case m.ungranted&f == 0:
		m.ungranted |= f
		r.logf("co-op: the world's %s is not written to this game: the session does not grant %v", what, f)
	}
	return false
}

// reconcileM4 runs the M4 receiver for one tick: o's snapshot, read and
// observed, with the M3 reconciler's writes picked (reconcile). The
// story bits it writes join r.toWrite, the merge path's batch; the derive
// path's writes wait in r.m4 for Tick to make once the lock is released.
func (r *Replica) reconcileM4(o *obs, armed bool, rep *Report) {
	m := &r.m4
	m.ticks++
	m.derived, m.from = nil, nil
	s, st := o.s, o.st
	stag, typ, typOK := readStagM4(r.m, s)
	ready := armed && r.haveView && r.viewWorld == r.sessionWorld
	if !ready {
		clear(m.logged)
	}
	dry := r.writer.DryRun()

	// The Deriver sees every tick, armed or not: it measures a file's
	// own keys and quarter hearts on its first tick. It plans whatever
	// the hub grants; a write of a derive not granted is dropped below
	// (and planned again a settle time later), so the log can say why it
	// is not made.
	dp := m.deriver.Tick(derive.Input{Now: m.now(), Snap: s, Gates: st, Stag: stag, Armed: ready})
	for _, n := range dp.Notes {
		r.logf("co-op: %s", n)
	}

	var view save.Image
	if ready {
		view = r.m4View()
	}

	// Story bits and registers: only on the TRANSITION tick. A dry run
	// asks the Receiver for them as a writing replica would, and logs
	// them.
	ok := ready && o.consistent() && r.m4Granted(proto.FeatureStory, "story")
	gains, _ := m.story.Tick(st, s.NextEnable, ok, &view, &o.raw, &r.planner.ever, &r.planner.everMax)
	if gains = r.spawnBitsHeld(gains); len(gains) > 0 {
		r.storyWrites(o, gains, dry)
	}

	// The inventory, when IDLE.
	entry := m.entry.Tick(s, st)
	var inv []inventory.Write
	if ready && o.consistent() && r.m4Granted(proto.FeatureInventory, "inventory") {
		p := inventory.Pending(&view, &o.raw, inventory.ContextOf(s, typ, typOK))
		inv = p.Due(st, entry)
		for _, h := range p.Held {
			r.m4Say("held "+h.Write.String(), fmt.Sprintf("holding %s back: %v", h.Write, h.Rule))
		}
		for _, w := range inventory.Due(p.Ammo, st, entry) {
			r.m4Say("ammo "+w.String(), fmt.Sprintf("not written: %s (the HUD's ammo counters are no WriteGuard target before M7)", w))
		}
	}
	for _, w := range inv {
		switch w.Kind {
		case inventory.KindFlag, inventory.KindCapacity:
			if w.Off < 0 || !class.Of(w.Off).Class.Merges() {
				r.logf("co-op: bug: inventory write %s is no image byte that merges; dropped", w)
				continue
			}
			if !dry {
				r.toWrite = append(r.toWrite, newWrite(w.Off, w.Bits(), int(s.Table)))
			}
			continue // a dry run's logPlan says these
		}
		d, ok := inventoryWriteOf(w)
		switch {
		case !ok:
			r.logf("co-op: bug: inventory write %s has no path; dropped", w)
		case dry:
			r.m4Say("dry "+w.String(), "dry-run: would write "+w.String())
		default:
			m.derived, m.from = append(m.derived, d), append(m.from, nil)
		}
	}

	// Keys, max life and max magic.
	for i := range dp.Writes {
		w := &dp.Writes[i]
		f, what := proto.FeatureKeys, "small keys"
		switch w.Kind {
		case derive.LifeCounter:
			f, what = proto.FeatureLife, "max life"
		case derive.MagicCounter:
			f, what = proto.FeatureMagic, "max magic"
		}
		switch {
		case dry:
			r.m4Say("dry "+w.String(), "dry-run: would write "+w.String())
			m.deriver.Dropped(*w)
		case !r.m4Granted(f, what):
			m.deriver.Dropped(*w)
		default:
			m.derived, m.from = append(m.derived, deriveWriteOf(*w)), append(m.from, w)
		}
	}
}

// storyWrites hands a stage change's story writes to the merge path, or
// for a dry run logs them.
func (r *Replica) storyWrites(o *obs, gains []world.Gain, dry bool) {
	var lines []string
	nBits, nRegs := 0, 0
	var wrote save.Image // the story bits written
	for _, g := range gains {
		off := int(g.Off)
		w := newWrite(off, g.Val, int(o.s.Table))
		if !r.guard.Allows(w.Saved, 1) || !w.Max && r.guard.Mask(w.Saved)&w.Bits != w.Bits {
			r.logf("co-op: bug: story write of %s at 0x%08X is outside the WriteGuard; dropped", r.label(off, -1), w.Saved)
			continue
		}
		if story.IsRegisterByte(off) {
			nRegs++
		} else {
			nBits += bits.OnesCount8(g.Val)
			wrote[off] |= g.Val
		}
		if dry {
			if w.Max {
				lines = append(lines, fmt.Sprintf("dry-run: would raise %s to %d", r.label(off, -1), g.Val))
			} else {
				for set := g.Val; set != 0; set &= set - 1 {
					lines = append(lines, fmt.Sprintf("dry-run: would set %s", r.label(off, bits.TrailingZeros8(set))))
				}
			}
			continue
		}
		r.toWrite = append(r.toWrite, w)
	}
	spawn := r.spawnWrites(o, &wrote)
	if dry {
		r.logf("co-op: dry-run: a stage change starts: %d story bits and %d registers of the world would be written now", nBits, nRegs)
		for _, w := range spawn {
			lines = append(lines, fmt.Sprintf("dry-run: would set %s with its story bit", r.label(int(w.Off), bits.TrailingZeros8(w.Bits))))
		}
		r.logLines(lines)
		return
	}
	r.logf("co-op: a stage change starts: writing %d story bits and %d registers of the world", nBits, nRegs)
	for _, w := range spawn {
		r.logf("co-op: and with its story bit, %s", r.label(int(w.Off), bits.TrailingZeros8(w.Bits)))
	}
	r.toWrite = append(r.toWrite, spawn...)
}

// spawnBitsHeld takes out of a stage change's story writes gs the story
// bits the planner holds because their spawn switch cannot go with them
// (spawnSwitch.bitHold; the reconciler logs the hold once): the receiver
// then enters with neither and plays its own intro. gs is reused.
func (r *Replica) spawnBitsHeld(gs []world.Gain) []world.Gain {
	out := gs[:0]
	for _, g := range gs {
		for _, h := range r.held {
			if h.Off == g.Off && spawnEvent(h.Hold.ID) {
				g.Val &^= h.Mask
			}
		}
		if g.Val != 0 {
			out = append(out, g)
		}
	}
	return out
}

// spawnEvent reports whether id is a spawn switch's story bit.
func spawnEvent(id uint16) bool {
	for _, s := range spawnSwitches {
		if s.event == id {
			return true
		}
	}
	return false
}

// spawnWrites are the start-event spawn switches (spawnSwitch) to write
// on this stage change's first tick with the story bits wrote it writes:
// each whose event bit is among them and which the planner holds for it,
// that is, the world has it and this game lacks it (and never had it). They
// go after the story bytes in the batch, so a batch cut short never leaves
// the switch without its bit. It can leave the bit without its switch (a
// memory error, co-op turned off or the switch's byte refused between the
// two writes): accepted, as the stage loads with the intro event on the
// time or fight layer only in that case, and the switch follows at its
// own timing once the bit is local (Plan releases it). The hub must grant
// the switches; the WriteGuard opens these bits alone on this tick
// (Tick.spawnMask, WriteGuard.readyBits). A bit whose switch cannot come
// at all is held instead (spawnBitsHeld).
func (r *Replica) spawnWrites(o *obs, wrote *save.Image) []Write {
	if r.features&proto.FeatureSwitches == 0 && !r.writer.DryRun() {
		return nil
	}
	var ws []Write
	for _, s := range spawnSwitches {
		b := addr.EventByte(s.event)
		if wrote[addr.OffEvent+b.Off]&b.Mask != b.Mask {
			continue
		}
		off, m := s.bit()
		for _, h := range r.held {
			if int(h.Off) == off && h.Mask&m != 0 && h.Hold.ID == s.event {
				ws = append(ws, newWrite(off, h.Mask&m, int(o.s.Table)))
			}
		}
	}
	return ws
}

// m4View is the world's image as the receiver may take it: the view
// without what the host tombstoned (desired), within the policy. A MAX
// byte whose value the policy does not wholly open is dropped, as the
// planner drops it.
func (r *Replica) m4View() save.Image {
	v := r.desired()
	for off := range v {
		m := r.policy[off]
		if class.Of(off).Class == class.Max {
			if v[off]&world.SharedMask(off)&^m != 0 {
				v[off] = 0
			}
			continue
		}
		v[off] &= m
	}
	return v
}

// readStagM4 reads the loaded STAG chunk's mProp and stage type in one
// read (dStage_stagInfo_GetSaveTbl, ChkKeyDisp and GetSTType,
// d_stage.h:1023-1033): the key HUD for the Deriver, the stage type for
// the inventory's context. ok is false for a null or unreadable chunk.
func readStagM4(m mem.Mem, s *state.Snapshot) (st derive.Stag, typ int, ok bool) {
	if s.StagInfo == 0 {
		return st, 0, false
	}
	b, err := m.Read(s.StagInfo+addr.StagOffProp, stagOffType-addr.StagOffProp+1)
	if err != nil {
		return st, 0, false
	}
	return derive.StagOf(b[0]), int(b[len(b)-1] & 7), true
}

// applyDerived makes a tick's derive-path writes, after the merge path's
// (mergeErr is what ApplyContext returned, nil when it had none): not at
// all when that batch stalled or the game moved on, since the slots
// follow the flags it carried. It takes r.mu for the result.
func (r *Replica) applyDerived(ctx context.Context, ws m4Writes, mergeErr error, rep *Report) {
	if len(ws.ds) == 0 {
		return
	}
	var res DeriveResult
	err := mergeErr
	if !errors.Is(err, ErrStall) && !errors.Is(err, ErrAborted) && !errors.Is(err, ErrOff) {
		res, err = r.writer.ApplyDerived(ctx, ws.ds)
	} else {
		res.Outcomes = make([]DeriveOutcome, len(ws.ds))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appliedDerived(ws, res, err, rep)
}

// appliedDerived records what the derive path did: a derive write not made
// is the Deriver's to plan again (Dropped), and each one made is logged.
// r.mu is held.
func (r *Replica) appliedDerived(ws m4Writes, res DeriveResult, err error, rep *Report) {
	rep.Wrote += len(res.Wrote)
	if rep.WriteErr == nil && err != nil {
		rep.WriteErr = err
	}
	var lines []string
	for i, o := range res.Outcomes {
		if o == DeriveMade {
			lines = append(lines, "wrote "+ws.ds[i].String())
			continue
		}
		if f := ws.from[i]; f != nil {
			r.m4.deriver.Dropped(*f)
		}
	}
	if len(lines) > 0 {
		r.logf("co-op: the derive path wrote %d of %d writes after heartbeat %d", len(lines), len(ws.ds), res.Beat)
		r.logLines(lines)
	}
}
