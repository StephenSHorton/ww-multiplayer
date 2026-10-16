package replica

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// BeatTimeout is how long a FrameWriter waits for the heartbeat to move
// before it gives up on a tick's writes (§4.2).
const BeatTimeout = 100 * time.Millisecond

var (
	// ErrDryRun is what a dry-run FrameWriter returns: game data is never
	// written (§5.1 principle 5; --coop-dry-run).
	ErrDryRun = errors.New("replica: dry run: co-op writes no game data")
	// ErrClosed is matched by a batch skipped because a gate its bytes
	// need is closed this tick. It is not a fault.
	ErrClosed = errors.New("replica: the gates are closed")
	// ErrStall is a batch skipped because the heartbeat did not move
	// within the timeout: emulation is paused, or slow (§3.2 HEARTBEAT).
	ErrStall = errors.New("replica: the heartbeat did not move: nothing was written")
	// ErrAborted is a batch skipped because the game moved on between the
	// tick's snapshot and the beat: the stage, the pending stage change,
	// the loaded table or the loaded file changed, a save state was loaded,
	// or, for an escape or a scoped write, the game stopped being IDLE, and
	// for a zone write, the zone table moved (ApplyScoped).
	ErrAborted = errors.New("replica: the game moved on since the snapshot: nothing was written")
)

// WriterConfig sets up a FrameWriter.
type WriterConfig struct {
	// Mem is the Dolphin (or FakeMem) to write. Required.
	Mem mem.Mem
	// Guard checks every byte, and its Breaker is the writer's off
	// switch. Required unless DryRun.
	Guard *WriteGuard
	// DryRun makes a writer that writes nothing: Apply returns ErrDryRun,
	// as in M2.
	DryRun bool
	// BeatTimeout is how long Apply waits for the heartbeat; 0 is
	// BeatTimeout.
	BeatTimeout time.Duration
	// Logf gets the verify failures and stalls; nil discards them.
	Logf func(format string, args ...any)
	// Label names an image bit (bit -1: the byte) for the log; nil is
	// DefaultLabel.
	Label func(off, bit int) string
	// DrainFrames is how many frames the HUD has to take a pending
	// counter ApplyDerived wrote before the Breaker trips; 0 is
	// DrainFrames.
	DrainFrames uint32
	// GBA: the stage tables share a switch this replica syncs, in any
	// store, with a Tingle Tuner switch (gba.go), so recheck reads the GBA
	// link after the beat on a tick that took none to be linked. The
	// WriteGuard's mask (GuardConfig.GBA) covers only the memory switches;
	// a dan or zone switch is held back by m5Due on the snapshot alone.
	GBA bool
}

// FrameWriter makes the planned writes of a tick (§4.2, §4.2.1). Per
// tick, Observe takes the snapshot (verifying the last tick's writes and
// opening the WriteGuard for this one), then Apply makes the plan:
//
//  1. Wait for the heartbeat, spawn_trigger, to move: spin-reading it,
//     for at most BeatTimeout, from the value the tick's snapshot read.
//     A first read below that value, or further ahead of it than the
//     game can have run since (beatLead), and a heartbeat that goes
//     backwards or jumps while it is polled, are a save state loaded
//     since the snapshot: nothing is written. multiplayer_update's first
//     act is spawn_trigger++ (inject/src/multiplayer.c:341-342), run by
//     frame_shim in place of the cCt_Counter call that follows
//     fpcM_Management in fapGm_Execute (f_ap_game.cpp:79-80), so a beat
//     marks the end of a frame: its actors have run. What runs next is
//     the rest of multiplayer_update, cCt_Counter, then the next frame's
//     fpcM_Management: the Painter's VSync wait, deletes, then the actor
//     creates that can set flags (f_pc_manager.cpp:278-284). A write made
//     at once lands before those creates. That narrows the race with the
//     game's own flag stores to microseconds; it does not remove it
//     (§4.2.1).
//  2. Read again what the snapshot's gates rest on and the addresses
//     depend on: mCurStage's name, mNextStage.mEnable, mDan.mStageNo and
//     the loaded file (the tag, mDataNum, mClearCount, mPlayerName and
//     mDateIPL, which ARMED rests on). If any changed, the game moved on
//     (a stage change began, getSave loaded another table into the live
//     memBit, or a save state of another file was loaded): nothing is
//     written.
//  3. For each planned byte, the live copy before the saved slot as
//     dComIfGs_onStageTbox orders them (d_com_inf_game.cpp:694-702): a
//     fresh 1-byte read, the planned bits ORed in (a MAX byte raised),
//     WriteGuard.Check, a 1-byte write. A byte already holding the bits
//     is left alone. Writes are bytes, never words: the game sets a flag
//     with a word load, OR and store (dSv_memBit_c::onSwitch,
//     d_save.cpp:1084-1087), so a word write would undo a flag the game
//     set in a neighbouring byte between Go's read and write.
//  4. The next Observe checks every written byte against its snapshot.
//     A byte without its bits is a verify failure: the game stored a word
//     loaded before the write over it (the §4.2.1 race the other way),
//     or the write never reached the game (Dolphin's dual mapping, CLAUDE.md
//     gotcha 2). Failures go to the Breaker. A tick whose stage, table
//     or file changed, or whose heartbeat went backwards, is not judged:
//     getSave, putSave and a save state rewrite those bytes legitimately,
//     and the planner, being level-triggered, plans again what was lost.
//
// Stop turns co-op off and returns once no write is in flight; so does
// the Breaker tripping, at the next byte. A FrameWriter that
// NewFrameWriter did not make, such as M2's &FrameWriter{Mem: m, Guard:
// g}, is a dry run. A FrameWriter is safe for concurrent use; batches run
// one at a time.
type FrameWriter struct {
	// Mem and Guard are what M2 built a FrameWriter from. A writer that
	// has only these is a dry run.
	Mem   mem.Mem
	Guard *Guard

	wg          *WriteGuard // nil: a dry run
	beatTimeout time.Duration
	logf        func(format string, args ...any)
	label       func(off, bit int) string

	drainFrames uint32
	gbaCheck    bool // WriterConfig.GBA

	mu      sync.Mutex
	tick    seen // the last Observe's snapshot
	have    bool
	pending []pendingByte
	pendAt  seen         // the tick the pending bytes were written in
	drains  []drainWatch // the counters ApplyDerived wrote, until the HUD takes them
	scoped  []pendingScoped
	scAt    seen // the tick the scoped bytes were written in
	stats   WriterStats
}

// seen is what a snapshot said of the state the writes depend on, and
// when the writer was told of it.
type seen struct {
	stage      string
	nextEnable int8
	table      int8
	heartbeat  uint32
	file       bind.File // the loaded file, as ARMED was judged on it
	zones      ZoneTable // the zone table the scoped path's bytes rest on
	gba        bool      // a Game Boy Advance may be linked (snap.GBALink.MayLink)
	// oldStay is mOldStayNo, read just after the snapshot (oldStayOf);
	// oldOK is false if that read failed or mStayNo had moved by then.
	oldStay int8
	oldOK   bool
	at      time.Time
}

func seenOf(s *snap.Snapshot, f bind.File, at time.Time) seen {
	return seen{stage: s.StageName, nextEnable: s.NextEnable, table: s.Table, heartbeat: s.Heartbeat, file: f, zones: ZoneTableOf(s), gba: s.GBA.MayLink(), at: at}
}

// The heartbeat's pace (waitBeat). The game runs fapGm_Execute, and so
// bumps spawn_trigger, 30 times a second (mDoGph_gInf_c::setTickRate,
// d_s_play.cpp:1352); Dolphin may run it faster.
const (
	// maxBeatRate is the most frames a second the heartbeat is taken to
	// move at: 8 times the game's 30, for an emulator running unthrottled.
	maxBeatRate = 240
	// beatSlack is the frames allowed past maxBeatRate's count, between a
	// snapshot and the first heartbeat read of its writes: the beats of
	// the tick's earlier batches (the merge, derive and scoped paths')
	// before an escape's, and a frame of rounding.
	beatSlack = 4
	// maxBeatJump is the most a heartbeat poll may move by at once. The
	// poll is a tight loop, so a jump of more is a save state loaded, or
	// the poller descheduled for frames; either way the tick's view is
	// stale.
	maxBeatJump = 2
)

// beatLead is how far the heartbeat may have moved since a snapshot taken
// d ago.
func beatLead(d time.Duration) uint32 {
	return beatSlack + uint32(max(d, 0)*maxBeatRate/time.Second)
}

// pendingByte is a written byte waiting for its verify.
type pendingByte struct {
	a        uint32
	off      uint16
	old, val byte
	bits     byte // the planned bits (OR) or value (MAX)
	max      bool
	mask     byte // a MAX byte's class mask
	set      bool // a derive write's: the byte must read val itself
}

// holds reports whether v, the byte read back, still has the write.
func (p pendingByte) holds(v byte) bool {
	switch {
	case p.set:
		return v == p.val
	case p.max:
		return v&p.mask >= p.bits
	}
	return v&p.bits == p.bits
}

// NewFrameWriter returns a FrameWriter over cfg.Mem.
func NewFrameWriter(cfg WriterConfig) (*FrameWriter, error) {
	if cfg.Mem == nil {
		return nil, errors.New("replica: no memory to write")
	}
	w := &FrameWriter{Mem: cfg.Mem, beatTimeout: cfg.BeatTimeout, logf: cfg.Logf, label: cfg.Label, drainFrames: cfg.DrainFrames, gbaCheck: cfg.GBA}
	if !cfg.DryRun {
		if cfg.Guard == nil {
			return nil, errors.New("replica: a FrameWriter that writes needs a WriteGuard")
		}
		w.wg, w.Guard = cfg.Guard, cfg.Guard.Allowlist()
	}
	if w.beatTimeout <= 0 {
		w.beatTimeout = BeatTimeout
	}
	if w.drainFrames == 0 {
		w.drainFrames = DrainFrames
	}
	if w.logf == nil {
		w.logf = func(string, ...any) {}
	}
	if w.label == nil {
		w.label = DefaultLabel
	}
	return w, nil
}

// DryRun reports whether the writer writes nothing.
func (w *FrameWriter) DryRun() bool { return w.wg == nil }

// WriteGuard returns the writer's WriteGuard, nil for a dry run.
func (w *FrameWriter) WriteGuard() *WriteGuard { return w.wg }

// VerifyReport is what Observe found of the last batch.
type VerifyReport struct {
	Verified   int // written bytes that read back with their bits
	Failed     int // written bytes that did not: each a fault
	Unverified int // written bytes not judged: the stage, table or file changed, a save state, a failed read
	Failures   []VerifyFailure
}

// VerifyFailure is a written byte that did not hold.
type VerifyFailure struct {
	Addr     uint32
	Off      uint16 // the image byte
	Old, New byte   // what was read and written
	Read     byte   // what the next snapshot had
}

// Observe takes one tick's snapshot (nil when its read failed), its gate
// status and whether the replica is armed: it verifies the last batch
// against s, counts a heartbeat regression toward the Breaker's storm,
// and opens the WriteGuard for this tick (or shuts it while s is nil).
// Call it once per tick, with a snapshot read after the last Apply
// returned, and before this tick's Apply. ARMED is taken to rest on the
// file s shows loaded (bind.FileOf), which Apply reads again after the
// beat. A dry run ignores it.
func (w *FrameWriter) Observe(s *snap.Snapshot, st gate.Status, armed bool) VerifyReport {
	if w.wg == nil || s == nil {
		return w.observe(s, st, armed, bind.File{}, false)
	}
	_, typ, ok := readStag(w.Mem, s.StagInfo)
	return w.observe(s, st, armed, bind.FileOf(s), bossStage(typ, ok, s.Table))
}

// observe is Observe with the file ARMED was judged on (the replica's, a
// tag it just wrote included) and whether the loaded stage is a boss
// stage (bossStage).
func (w *FrameWriter) observe(s *snap.Snapshot, st gate.Status, armed bool, f bind.File, boss bool) VerifyReport {
	if w.wg == nil {
		return VerifyReport{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var old int8
	var oldOK bool
	if s != nil {
		old, oldOK = w.oldStayOf(s)
	}
	rep := w.verify(s, st, f)
	w.verifyScoped(s, st, f, old, oldOK, &rep)
	w.watchDrains(s, st, f)
	if st.Regressed {
		w.wg.brk.Regressed()
	}
	if s == nil {
		w.have = false
		w.wg.Close("the game state could not be read")
		return rep
	}
	w.tick, w.have = seenOf(s, f, time.Now()), true
	w.tick.oldStay, w.tick.oldOK = old, oldOK
	w.wg.SetTick(Tick{Gates: st, Armed: armed, Table: s.Table, Boss: boss, Zones: w.tick.zones, GBA: w.tick.gba})
	return rep
}

// verify judges the pending bytes against s, a snapshot of file f. w.mu
// is held.
func (w *FrameWriter) verify(s *snap.Snapshot, st gate.Status, f bind.File) VerifyReport {
	ps := w.pending
	w.pending = nil
	var rep VerifyReport
	if len(ps) == 0 {
		return rep
	}
	if s == nil || st.Regressed || s.StageName != w.pendAt.stage || s.Table != w.pendAt.table || f != w.pendAt.file {
		rep.Unverified = len(ps)
		w.stats.Unverified += len(ps)
		return rep
	}
	for _, p := range ps {
		v, ok := snapByte(s, p.a)
		switch {
		case !ok:
			rep.Unverified++
			continue
		case p.holds(v):
			rep.Verified++
			continue
		}
		rep.Failed++
		rep.Failures = append(rep.Failures, VerifyFailure{Addr: p.a, Off: p.off, Old: p.old, New: p.val, Read: v})
		detail := fmt.Sprintf("the write of %s at 0x%08X (0x%02X -> 0x%02X) read back 0x%02X on the next tick",
			w.name(p.a, p.off), p.a, p.old, p.val, v)
		w.logf("co-op: verify failed: %s: the game stored over it, or Dolphin dropped the write", detail)
		w.wg.brk.Fault(FaultVerify, detail)
	}
	w.stats.Verified += rep.Verified
	w.stats.Failed += rep.Failed
	w.stats.Unverified += rep.Unverified
	return rep
}

// snapByte returns the byte at a as s read it, if s read it.
func snapByte(s *snap.Snapshot, a uint32) (byte, bool) {
	for _, r := range []struct {
		reg addr.Region
		b   []byte
	}{{snap.SaveRegion, s.Save[:]}, {snap.StageRegion, s.Stage[:]}, {snap.PlayerRegion, s.Player[:]}} {
		if r.reg.Contains(a, 1) {
			return r.b[a-r.reg.Start], true
		}
	}
	return 0, false
}

// ByteWrite is one byte a batch wrote.
type ByteWrite struct {
	Addr     uint32
	Off      uint16 // the image byte (Write.Off)
	Old, New byte
}

// Result is what one Apply did.
type Result struct {
	Beat    uint32        // the heartbeat the writes followed
	Waited  time.Duration // from the call to the beat
	Wrote   []ByteWrite   // in write order
	Already int           // planned bytes that already had their bits: not written
	Closed  int           // planned bytes whose gate was closed this tick: not tried
	Refused int           // planned bytes or writes the WriteGuard refused
}

// WriterStats are a FrameWriter's counts since it was made.
type WriterStats struct {
	Batches    int // batches that wrote at least one byte
	Written    int // bytes written
	Already    int // planned bytes that already had their bits
	Refused    int // planned bytes or writes the WriteGuard refused
	Verified   int // written bytes that read back
	Failed     int // verify failures
	Unverified int // written bytes not judged
	Stalls     int // batches the heartbeat did not move for
	Aborted    int // batches dropped because the game moved on
	Escapes    int // escapes written (ApplyFields): refreshes and warps, §6.7
	Derived    int // derive writes made (ApplyDerived)
	Scoped     int // dan and zone switch bytes written (ApplyScoped)
	Stale      int // derive writes dropped: their bytes moved since the plan
	Drained    int // pending counters written that the HUD then took
}

// Stats returns the writer's counts.
func (w *FrameWriter) Stats() WriterStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// Disabled reports whether co-op is off, and why. A dry run is never
// off.
func (w *FrameWriter) Disabled() (bool, string) {
	if w.wg == nil {
		return false, ""
	}
	why := w.wg.brk.Why()
	return why != "", why
}

// Stop turns co-op off, why being what the session prints, and returns
// once no write is in flight: a batch waiting for the beat returns at
// once, one writing stops before its next byte. Nothing is written after
// Stop returns until the Breaker is Reset.
func (w *FrameWriter) Stop(why string) {
	if w.wg == nil {
		return
	}
	w.wg.brk.TurnOff(why)
	w.mu.Lock()
	w.pending, w.drains, w.scoped = nil, nil, nil
	w.mu.Unlock()
}

// Apply is ApplyContext without a context or a Result.
func (w *FrameWriter) Apply(ws []Write) error {
	_, err := w.ApplyContext(context.Background(), ws)
	return err
}

// planByte is one byte of a batch.
type planByte struct {
	a    uint32
	off  uint16
	bits byte
	max  bool
	mask byte
}

func (b planByte) apply(old byte) byte {
	if !b.max {
		return old | b.bits
	}
	if b.bits > old&b.mask {
		return old&^b.mask | b.bits
	}
	return old
}

// ApplyContext makes the writes ws plans (Planner.Plan's, or a subset the
// caller picked for this tick's timing) after the next heartbeat, as the
// FrameWriter doc says. It returns:
//
//   - ErrDryRun for a dry run, and a *DisabledError (ErrOff) while co-op
//     is off, writing nothing;
//   - ErrClosed when a gate the bytes need is closed (bytes whose gate is
//     open are still written when only some are closed);
//   - ErrStall or ErrAborted when no write was made this tick;
//   - the first refusal (ErrGuard): a refused byte is skipped and the
//     rest written;
//   - ctx's error, or a memory error, stopping the batch where it is.
//
// The writes made are in Result.Wrote, and are verified by the next
// Observe.
func (w *FrameWriter) ApplyContext(ctx context.Context, ws []Write) (res Result, err error) {
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
		w.stats.Already += res.Already
		w.stats.Refused += res.Refused
	}()
	var first error
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	refuse := func(err error) {
		res.Refused++
		note(err)
	}

	var bs []planByte
	at := map[uint32]int{}
	for _, wr := range ws {
		if wr.Bits == 0 {
			continue
		}
		if err := w.wg.CheckWrite(wr); err != nil {
			refuse(err)
			continue
		}
		as := []uint32{wr.Saved}
		if wr.Live != 0 {
			as = []uint32{wr.Live, wr.Saved}
		}
		for _, a := range as {
			if i, ok := at[a]; ok {
				if wr.Max {
					bs[i].bits = max(bs[i].bits, wr.Bits)
				} else {
					bs[i].bits |= wr.Bits
				}
				continue
			}
			at[a] = len(bs)
			bs = append(bs, planByte{a: a, off: wr.Off, bits: wr.Bits, max: wr.Max, mask: world.SharedMask(int(wr.Off))})
		}
	}

	// A byte that is no write target this tick, or not the merge path's,
	// is refused now, before the wait: a plan holding one is a bug,
	// whatever the gates say. Bytes whose gate is closed are not tried,
	// and are no fault: the caller may hand over a plan every tick.
	open := bs[:0]
	var closed error
	for _, b := range bs {
		if w.wg.Mask(b.a) == 0 || !w.wg.on(b.a, pathMerge) {
			old, err := mem.ReadU8(w.Mem, b.a)
			if err != nil {
				return res, err
			}
			if err := w.wg.check(b.a, old, b.apply(old), pathMerge); err != nil {
				if errors.Is(err, ErrOff) {
					return res, err
				}
				refuse(err)
			}
			continue
		}
		if err := w.wg.ready(b.a); err != nil {
			if errors.Is(err, ErrOff) {
				return res, err
			}
			res.Closed++
			if closed == nil {
				closed = err
			}
			continue
		}
		open = append(open, b)
	}
	if bs = open; len(bs) == 0 {
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
	switch {
	case errors.Is(err, ErrStall):
		w.stats.Stalls++
		if w.stats.Stalls == 1 || w.stats.Stalls%100 == 0 {
			w.logf("co-op: the heartbeat did not move for %v: is emulation paused? %d planned bytes wait (stall %d)",
				w.beatTimeout, len(bs), w.stats.Stalls)
		}
		return res, err
	case errors.Is(err, ErrAborted):
		w.stats.Aborted++
		w.wg.Close(err.Error())
		return res, err
	case err != nil:
		return res, err
	}
	res.Beat = hb

	for _, b := range bs {
		if w.wg.brk.Off() {
			note(w.wg.brk.Err())
			break
		}
		if err := ctx.Err(); err != nil {
			note(err)
			break
		}
		old, err := mem.ReadU8(w.Mem, b.a)
		if err != nil {
			note(err)
			break
		}
		val := b.apply(old)
		if val == old {
			res.Already++
			continue
		}
		if err := w.wg.check(b.a, old, val, pathMerge); err != nil {
			if errors.Is(err, ErrOff) {
				note(err)
				break
			}
			refuse(err)
			continue
		}
		if err := mem.WriteU8(w.Mem, b.a, val); err != nil {
			note(err)
			break
		}
		res.Wrote = append(res.Wrote, ByteWrite{Addr: b.a, Off: b.off, Old: old, New: val})
		if len(w.pending) == 0 {
			w.pendAt = w.tick
		}
		w.pending = append(w.pending, pendingByte{a: b.a, off: b.off, old: old, val: val, bits: b.bits, max: b.max, mask: b.mask})
	}
	return res, first
}

// waitBeat spin-reads spawn_trigger until it moves, and returns its new
// value and how long that took. It starts from the heartbeat the tick's
// snapshot read: a save state loaded since then is ErrAborted, whether
// the first read finds the heartbeat below the snapshot's or further ahead
// of it than the game can have run since (beatLead), or a poll finds it
// gone backwards or jumped by more than maxBeatJump. Short of that, the
// beat is the first poll that finds it moved on from the first read. It
// gives up after the timeout (ErrStall), when co-op turns off and when ctx
// ends.
func (w *FrameWriter) waitBeat(ctx context.Context) (uint32, time.Duration, error) {
	start := time.Now()
	off := w.wg.brk.Done()
	if !w.have {
		return 0, 0, fmt.Errorf("%w: no snapshot", ErrAborted)
	}
	hb0, err := mem.ReadU32(w.Mem, addr.SpawnTrigger)
	if err != nil {
		return 0, 0, err
	}
	switch d, lead := int32(hb0-w.tick.heartbeat), beatLead(start.Sub(w.tick.at)); {
	case d < 0:
		return hb0, 0, fmt.Errorf("%w: the heartbeat went from %d at the snapshot back to %d (a save state was loaded)", ErrAborted, w.tick.heartbeat, hb0)
	case uint32(d) > lead:
		return hb0, 0, fmt.Errorf("%w: the heartbeat ran %d frames past the snapshot's in %v, more than the %d the game can run (a save state was loaded)",
			ErrAborted, d, start.Sub(w.tick.at).Round(time.Millisecond), lead)
	}
	for last := hb0; ; {
		hb, err := mem.ReadU32(w.Mem, addr.SpawnTrigger)
		waited := time.Since(start)
		switch d := int32(hb - last); {
		case err != nil:
			return 0, waited, err
		case d < 0:
			return hb, waited, fmt.Errorf("%w: the heartbeat went backwards (a save state was loaded)", ErrAborted)
		case d > maxBeatJump:
			return hb, waited, fmt.Errorf("%w: the heartbeat jumped %d frames at once (a save state was loaded)", ErrAborted, d)
		case d > 0:
			return hb, waited, nil
		}
		select {
		case <-ctx.Done():
			return 0, waited, ctx.Err()
		case <-off:
			return 0, waited, w.wg.brk.Err()
		default:
		}
		if waited >= w.beatTimeout {
			return 0, waited, ErrStall
		}
		runtime.Gosched()
	}
}

// recheck reads again, after the beat, what the tick's gates and the
// planned addresses rest on, and returns ErrAborted if it changed since
// the snapshot:
//
//   - mCurStage's name: a new play scene sets it from mNextStage
//     (d_s_play.cpp:1277), a stage change under way;
//   - mNextStage.mEnable: a loading zone, warp or cutscene asked for a
//     stage change (d_stage.cpp:26-31), which STABLE refuses;
//   - mDan.mStageNo: getSave loaded another table into the live memBit
//     (d_stage.cpp:1674-1675), so a live-copy address now belongs to it;
//   - the loaded file, as bind.ReadFile reads it (the tag, mDataNum,
//     mClearCount, mPlayerName, mDateIPL): ARMED was judged on it, and a
//     save state of another file standing in the same stage and table
//     changes nothing else here;
//   - with switches shared with the Tingle Tuner (WriterConfig.GBA: a
//     memory, dan, zone or room switch) on a tick that took no GBA to be
//     linked, the GBA link (snap.GBALink): a GBA that may be linked now
//     shuts them, and neither the guard's mask for this tick nor the M5
//     batch planned on it knows it.
//
// mpPlayerPtr[0] adds nothing: it stays set through the fade-out while
// mNextStage.mEnable is 1, and dScnPly_Delete clears it (dComIfGp_init,
// d_s_play.cpp:1136; dComIfG_play_c::init, d_com_inf_game.cpp:60-69)
// before the new scene's phase_1 renames the stage and clears mEnable
// (d_s_play.cpp:1277-1278), so the mEnable check covers that window.
func (w *FrameWriter) recheck() error {
	if !w.have {
		return fmt.Errorf("%w: no snapshot", ErrAborted)
	}
	// mCurStage and mNextStage are adjacent: name[8] at 0x803C9D3C up to
	// mNextStage.mEnable at 0x803C9D54.
	b, err := w.Mem.Read(addr.CurStageName, int(addr.NextStageEnable-addr.CurStageName)+1)
	if err != nil {
		return err
	}
	table, err := mem.ReadU8(w.Mem, addr.DanStageNo)
	if err != nil {
		return err
	}
	f, err := bind.ReadFile(w.Mem)
	if err != nil {
		return err
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	switch enable := int8(b[len(b)-1]); {
	case string(name) != w.tick.stage:
		return fmt.Errorf("%w: the stage changed from %q to %q", ErrAborted, w.tick.stage, name)
	case enable != w.tick.nextEnable:
		return fmt.Errorf("%w: mNextStage.mEnable went from %d to %d (a stage change)", ErrAborted, w.tick.nextEnable, enable)
	case int8(table) != w.tick.table:
		return fmt.Errorf("%w: the loaded save table went from %d to %d", ErrAborted, w.tick.table, int8(table))
	case f != w.tick.file:
		return fmt.Errorf("%w: another file is loaded (%q in slot %d, was %q in slot %d), or its tag changed: a save state was loaded",
			ErrAborted, f.PlayerName(), f.Slot, w.tick.file.PlayerName(), w.tick.file.Slot)
	}
	if w.wg != nil && w.gbaCheck && !w.tick.gba {
		g, err := w.Mem.Read(addr.GbaCom, addr.GbaComLinkReadLen)
		if err != nil {
			return err
		}
		if l := (snap.GBALink{Enable: g[addr.GbaComOffEnable], Context: g[addr.GbaComOffContext]}); l.MayLink() {
			return fmt.Errorf("%w: a Game Boy Advance may be linked since the snapshot (the Tingle Tuner)", ErrAborted)
		}
	}
	return nil
}

// recheckIdle reads again, after the beat, the inputs of IDLE (§3.2;
// gate.Tracker): an escape, made only when IDLE, returns ErrAborted if an
// event, the pause menu, a minigame or a boss refight began since the
// snapshot.
func (w *FrameWriter) recheckIdle() error {
	for _, in := range []struct {
		a    uint32
		what string
	}{
		{addr.EvtMode, "an event began"},
		{addr.EvtOrderCount, "an event was queued"},
		{addr.MenuPause, "the pause menu opened"},
		{addr.MiniGameType, "a minigame began"},
		{addr.PlayerInfoBufStageNo, "a boss refight began"},
	} {
		v, err := mem.ReadU8(w.Mem, in.a)
		if err != nil {
			return err
		}
		if v != 0 {
			return fmt.Errorf("%w: %s since the snapshot (0x%08X reads 0x%02X)", ErrAborted, in.what, in.a, v)
		}
	}
	return nil
}

// name is what the log calls the byte at a, image byte off (noOff for a
// byte outside the image and the live memBit).
func (w *FrameWriter) name(a uint32, off uint16) string {
	switch {
	case a >= addr.PlaySelectItem && a < addr.PlaySelectItem+inventory.Buttons:
		return fmt.Sprintf("play.mSelectItem[%d]", a-addr.PlaySelectItem)
	case off == noOff:
		return fmt.Sprintf("0x%08X", a)
	case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize:
		return "the live copy of " + w.label(int(off), -1)
	}
	return w.label(int(off), -1)
}
