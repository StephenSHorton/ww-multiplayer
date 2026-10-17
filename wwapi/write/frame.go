package write

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
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

// The heartbeat's pace (WaitBeat). The game runs fapGm_Execute, and so
// bumps spawn_trigger, 30 times a second (mDoGph_gInf_c::setTickRate,
// d_s_play.cpp:1352); Dolphin may run it faster.
const (
	// MaxBeatRate is the most frames a second the heartbeat is taken to
	// move at: 8 times the game's 30, for an emulator running unthrottled.
	MaxBeatRate = 240
	// beatSlack is the frames allowed past MaxBeatRate's count, between a
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

// BeatLead is how far the heartbeat may have moved since a snapshot taken
// d ago.
func BeatLead(d time.Duration) uint32 {
	return beatSlack + uint32(max(d, 0)*MaxBeatRate/time.Second)
}

// Beat is the heartbeat a tick's snapshot read, and when.
type Beat struct {
	Heartbeat uint32
	At        time.Time
}

// FrameWriter is the per-frame timing of a batch of writes (§4.2): it
// waits for the heartbeat to move from the snapshot's (WaitBeat), so the
// writes land just after a frame's actors have run, and gives up when the
// Breaker trips. It holds no state of its own between calls: the owner's
// writer (co-op's replica.FrameWriter) serialises its batches and
// verifies them.
type FrameWriter struct {
	// Mem is the game memory read and written. Required.
	Mem mem.Mem
	// Breaker is the off switch: a wait returns when it trips, and a
	// batch stops at its next field. Required.
	Breaker *Breaker
	// Timeout is how long WaitBeat waits; 0 is BeatTimeout.
	Timeout time.Duration
}

func (f *FrameWriter) timeout() time.Duration {
	if f.Timeout <= 0 {
		return BeatTimeout
	}
	return f.Timeout
}

// WaitBeat spin-reads spawn_trigger until it moves, and returns its new
// value and how long that took. It starts from the heartbeat the tick's
// snapshot read (from): a save state loaded since then is ErrAborted,
// whether the first read finds the heartbeat below the snapshot's or
// further ahead of it than the game can have run since (BeatLead), or a
// poll finds it gone backwards or jumped by more than maxBeatJump. Short
// of that, the beat is the first poll that finds it moved on from the
// first read. It gives up after the timeout (ErrStall), when the Breaker
// trips and when ctx ends.
func (f *FrameWriter) WaitBeat(ctx context.Context, from Beat) (uint32, time.Duration, error) {
	start := time.Now()
	off := f.Breaker.Done()
	hb0, err := mem.ReadU32(f.Mem, mailbox.SpawnTrigger)
	if err != nil {
		return 0, 0, err
	}
	switch d, lead := int32(hb0-from.Heartbeat), BeatLead(start.Sub(from.At)); {
	case d < 0:
		return hb0, 0, fmt.Errorf("%w: the heartbeat went from %d at the snapshot back to %d (a save state was loaded)", ErrAborted, from.Heartbeat, hb0)
	case uint32(d) > lead:
		return hb0, 0, fmt.Errorf("%w: the heartbeat ran %d frames past the snapshot's in %v, more than the %d the game can run (a save state was loaded)",
			ErrAborted, d, start.Sub(from.At).Round(time.Millisecond), lead)
	}
	timeout := f.timeout()
	for last := hb0; ; {
		hb, err := mem.ReadU32(f.Mem, mailbox.SpawnTrigger)
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
			return 0, waited, f.Breaker.Err()
		default:
		}
		if waited >= timeout {
			return 0, waited, ErrStall
		}
		runtime.Gosched()
	}
}

// Stage is what ReadStage reads of the state a write's gates and
// addresses rest on: mCurStage's name, mNextStage.mEnable and
// mDan.mStageNo, the save table the live memBit holds.
type Stage struct {
	Name       string
	NextEnable int8
	Table      int8
}

// ReadStage reads a Stage fresh from m: mCurStage and mNextStage in one
// read (they are adjacent: name[8] at 0x803C9D3C up to mNextStage.mEnable
// at 0x803C9D54), then mDan.mStageNo.
func ReadStage(m mem.Reader) (Stage, error) {
	b, err := m.Read(addr.CurStageName, int(addr.NextStageEnable-addr.CurStageName)+1)
	if err != nil {
		return Stage{}, err
	}
	table, err := mem.ReadU8(m, addr.DanStageNo)
	if err != nil {
		return Stage{}, err
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return Stage{Name: string(name), NextEnable: int8(b[len(b)-1]), Table: int8(table)}, nil
}

// Since returns ErrAborted, saying what changed, if s, read after the
// beat, differs from was, the snapshot's:
//
//   - mCurStage's name: a new play scene sets it from mNextStage
//     (d_s_play.cpp:1277), a stage change under way;
//   - mNextStage.mEnable: a loading zone, warp or cutscene asked for a
//     stage change (d_stage.cpp:26-31), which STABLE refuses;
//   - mDan.mStageNo: getSave loaded another table into the live memBit
//     (d_stage.cpp:1674-1675), so a live-copy address now belongs to it.
func (s Stage) Since(was Stage) error {
	switch {
	case s.Name != was.Name:
		return fmt.Errorf("%w: the stage changed from %q to %q", ErrAborted, was.Name, s.Name)
	case s.NextEnable != was.NextEnable:
		return fmt.Errorf("%w: mNextStage.mEnable went from %d to %d (a stage change)", ErrAborted, was.NextEnable, s.NextEnable)
	case s.Table != was.Table:
		return fmt.Errorf("%w: the loaded save table went from %d to %d", ErrAborted, was.Table, s.Table)
	}
	return nil
}

// CheckIdle reads again, after the beat, the inputs of IDLE (§3.2;
// gate.Tracker): it returns ErrAborted if an event, the pause menu, a
// minigame or a boss refight began since the snapshot.
func CheckIdle(m mem.Reader) error {
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
		v, err := mem.ReadU8(m, in.a)
		if err != nil {
			return err
		}
		if v != 0 {
			return fmt.Errorf("%w: %s since the snapshot (0x%08X reads 0x%02X)", ErrAborted, in.what, in.a, v)
		}
	}
	return nil
}

// ByteWrite is one byte a batch wrote.
type ByteWrite struct {
	Addr     uint32
	Off      uint16 // the owner's index of the byte (co-op: the image byte, Write.Off)
	Old, New byte
}

// Result is what one batch did.
type Result struct {
	Beat    uint32        // the heartbeat the writes followed
	Waited  time.Duration // from the call to the beat
	Wrote   []ByteWrite   // in write order
	Already int           // planned bytes that already had their bits: not written
	Closed  int           // planned bytes whose gate was closed this tick: not tried
	Refused int           // planned bytes or writes the WriteGuard refused
}

// Batch is how WriteFields judges and follows one batch.
type Batch struct {
	// Guard judges every byte. Required.
	Guard Guard
	// From is the heartbeat of the tick's snapshot, which WaitBeat
	// follows; nil, no snapshot to follow, is ErrAborted at the wait.
	From *Beat
	// Recheck reads again, after the beat, what the batch's gates rest
	// on, and returns ErrAborted if it changed; nil checks nothing.
	Recheck func() error
	// Waited, if not nil, gets the wait's or Recheck's error as soon as
	// there is one, before WriteFields returns it: ErrStall, ErrAborted,
	// ctx's error, the Breaker's, or a memory error.
	Waited func(err error)
}

// WriteFields makes fs, in order, right after the next heartbeat, all or
// nothing:
//
//   - before the wait, a field with no data or outside MEM1 stops the
//     batch; then every byte is checked against what the game holds now:
//     a byte in the allowlist (Guard.Mask) must be Ready, and every byte
//     must pass Guard.Check. The first closed gate (Closed is then every
//     byte) or refusal stops the whole batch, writing nothing;
//   - the heartbeat is followed from the snapshot's (WaitBeat), then
//     Recheck is run;
//   - after the beat each field is read fresh, checked byte by byte and
//     written in one write, and the first refusal or error stops the
//     rest; the Breaker tripping and ctx ending stop it before the next
//     field. The caller puts the field that sets a request off last, so
//     a batch stopped part way leaves inert bytes.
//
// A field whose bytes already hold the data is not written (Already).
// Nothing here verifies the writes later: that is the owner's.
func (f *FrameWriter) WriteFields(ctx context.Context, b Batch, fs []Field) (res Result, err error) {
	n := 0
	for _, fl := range fs {
		if len(fl.Data) == 0 {
			return res, errors.New("replica: an escape field with no data")
		}
		if err := mem.CheckRange(fl.Addr, len(fl.Data)); err != nil {
			return res, err
		}
		n += len(fl.Data)
	}
	if n == 0 {
		return res, nil
	}

	// Check every byte against what the game holds now, before the wait:
	// a batch is made whole or not at all.
	for _, fl := range fs {
		old, err := f.Mem.Read(fl.Addr, len(fl.Data))
		if err != nil {
			return res, err
		}
		for i, v := range fl.Data {
			a := fl.Addr + uint32(i)
			if b.Guard.Mask(a) != 0 {
				if err := b.Guard.Ready(a); err != nil {
					res.Closed = n
					return res, err
				}
			}
			if err := b.Guard.Check(a, old[i], v); err != nil {
				res.Refused++
				return res, err
			}
		}
	}

	var hb uint32
	var waited time.Duration
	if b.From == nil {
		err = fmt.Errorf("%w: no snapshot", ErrAborted)
	} else {
		hb, waited, err = f.WaitBeat(ctx, *b.From)
	}
	res.Waited = waited
	if err == nil && b.Recheck != nil {
		err = b.Recheck()
	}
	if err != nil {
		if b.Waited != nil {
			b.Waited(err)
		}
		return res, err
	}
	res.Beat = hb

	for _, fl := range fs {
		if f.Breaker.Off() {
			return res, f.Breaker.Err()
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		old, err := f.Mem.Read(fl.Addr, len(fl.Data))
		if err != nil {
			return res, err
		}
		changed := false
		for i, v := range fl.Data {
			if err := b.Guard.Check(fl.Addr+uint32(i), old[i], v); err != nil {
				res.Refused++
				return res, err
			}
			changed = changed || old[i] != v
		}
		if !changed {
			res.Already += len(fl.Data)
			continue
		}
		if err := f.Mem.Write(fl.Addr, fl.Data); err != nil {
			return res, fmt.Errorf("replica: writing 0x%08X: %w", fl.Addr, err)
		}
		for i, v := range fl.Data {
			if old[i] != v {
				res.Wrote = append(res.Wrote, ByteWrite{Addr: fl.Addr + uint32(i), Old: old[i], New: v})
			}
		}
	}
	return res, nil
}
