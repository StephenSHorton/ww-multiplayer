package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// The escape hatches' addresses past addr's (§6.7). None comes from the
// decomp.
const (
	// WarpAck is mailbox.warp_ack (mailbox.h:222): multiplayer_update
	// copies warp_seq into it once it has moved Link (multiplayer.c:
	// 365-421). Go reads it and never writes it.
	WarpAck = addr.WarpSeq + 4
	// WarpPos is mailbox.warp_x, warp_y and warp_z, three big-endian f32s
	// (mailbox.h:223-225), the target multiplayer_update moves Link to
	// when warp_seq != warp_ack (multiplayer.c:365-371).
	WarpPos = addr.WarpSeq + 8
)

// MaxStageLayer is the highest layer mNextStage may name: ACT0..ACTb
// (l_layerFuncTable_p, d_stage.cpp:2144-2150). MaxWipe is the highest
// mWipe: dScnPly_Draw JUT_ASSERTs it indexes its 12-entry l_wipeType
// (d_s_play.cpp:291-307).
const (
	MaxStageLayer = 11
	MaxWipe       = 11
)

// Field is one write of an escape (§6.7): Data at Addr, made in one memory
// write.
type Field struct {
	Addr uint32
	Data []byte
}

// ApplyFields makes an escape's writes, fs in order, right after the next
// heartbeat, as ApplyContext makes a plan's: every byte is checked by the
// WriteGuard (TargetEscape: the values the game accepts, and ARMED,
// STABLE, IDLE and HEARTBEAT as the tick's snapshot judged them), the
// heartbeat is followed from the snapshot's (waitBeat: a save state loaded
// since is caught), and after the beat the game state those gates rest on
// is read again: the stage, mNextStage.mEnable, the loaded table and file
// (recheck), and IDLE's inputs, the event, pause menu, minigame and
// refight bytes (recheckIdle). Unlike a plan, an escape is all or nothing:
//
//   - before the wait, a byte outside the allowlist, a value the guard
//     refuses or a closed gate stops the whole escape, writing nothing;
//   - after the beat each field is read fresh, checked byte by byte and
//     written in one write, and the first refusal or error stops the
//     rest. The caller puts the field that sets the request off last
//     (mNextStage.mEnable, or warp_seq), so an escape stopped part way
//     leaves inert bytes the game ignores: mNextStage's fields without
//     mEnable (dStage_nextStage_c::set overwrites them, d_stage.cpp:26-31),
//     warp coordinates without a new warp_seq.
//
// A field whose bytes already hold the data is not written (Already). Its
// bytes are not verified on the next tick: the game consumes both requests
// within frames (mEnable is cleared by the next play scene, d_s_play.cpp:
// 1277-1278; warp_ack catches up with warp_seq), so a read-back could not
// tell a lost write from a consumed one. It returns ErrDryRun for a dry
// run, a *DisabledError while co-op is off, ErrClosed, ErrStall,
// ErrAborted, the first refusal (ErrGuard), or a memory error.
func (w *FrameWriter) ApplyFields(ctx context.Context, fs []Field) (res Result, err error) {
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
			w.stats.Escapes++
		}
		w.stats.Refused += res.Refused
	}()
	n := 0
	for _, f := range fs {
		if len(f.Data) == 0 {
			return res, errors.New("replica: an escape field with no data")
		}
		if err := mem.CheckRange(f.Addr, len(f.Data)); err != nil {
			return res, err
		}
		n += len(f.Data)
	}
	if n == 0 {
		return res, nil
	}

	// Check every byte against what the game holds now, before the wait:
	// an escape is made whole or not at all.
	for _, f := range fs {
		old, err := w.Mem.Read(f.Addr, len(f.Data))
		if err != nil {
			return res, err
		}
		for i, v := range f.Data {
			a := f.Addr + uint32(i)
			if w.wg.Mask(a) != 0 {
				if err := w.wg.ready(a); err != nil {
					res.Closed = n
					return res, err
				}
			}
			if err := w.wg.check(a, old[i], v, pathEscape); err != nil {
				res.Refused++
				return res, err
			}
		}
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

	for _, f := range fs {
		if w.wg.brk.Off() {
			return res, w.wg.brk.Err()
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		old, err := w.Mem.Read(f.Addr, len(f.Data))
		if err != nil {
			return res, err
		}
		changed := false
		for i, v := range f.Data {
			if err := w.wg.check(f.Addr+uint32(i), old[i], v, pathEscape); err != nil {
				res.Refused++
				return res, err
			}
			changed = changed || old[i] != v
		}
		if !changed {
			res.Already += len(f.Data)
			continue
		}
		if err := w.Mem.Write(f.Addr, f.Data); err != nil {
			return res, fmt.Errorf("replica: writing 0x%08X: %w", f.Addr, err)
		}
		for i, v := range f.Data {
			if old[i] != v {
				res.Wrote = append(res.Wrote, ByteWrite{Addr: f.Addr + uint32(i), Old: old[i], New: v})
			}
		}
	}
	return res, nil
}
