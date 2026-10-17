package replica

import (
	"context"
	"errors"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The escape hatches' addresses past addr's (§6.7). None comes from the
// decomp.
const (
	// WarpAck is mailbox.warp_ack (mailbox.h:222): multiplayer_update
	// copies warp_seq into it once it has moved Link (multiplayer.c:
	// 365-421). The replica reads it and never writes it. wwapi/player's
	// Warp, which co-op never calls, writes it to withdraw a warp the
	// game did not take in time (docs/08 item 131).
	WarpAck = mailbox.WarpSeq + 4
	// WarpPos is mailbox.warp_x, warp_y and warp_z, three big-endian f32s
	// (mailbox.h:223-225), the target multiplayer_update moves Link to
	// when warp_seq != warp_ack (multiplayer.c:365-371).
	WarpPos = mailbox.WarpSeq + 8
)

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
	var from *write.Beat
	if w.have {
		from = &write.Beat{Heartbeat: w.tick.heartbeat, At: w.tick.at}
	}
	return w.eng.WriteFields(ctx, write.Batch{
		Guard: escapeGuard{w.wg},
		From:  from,
		Recheck: func() error {
			if err := w.recheck(); err != nil {
				return err
			}
			return w.recheckIdle()
		},
		Waited: func(err error) {
			switch {
			case errors.Is(err, ErrStall):
				w.stats.Stalls++
			case errors.Is(err, ErrAborted):
				w.stats.Aborted++
				w.wg.Close(err.Error())
			}
		},
	}, fs)
}

// escapeGuard is the WriteGuard as an escape's write.Guard: every byte
// judged as written by an escape (pathEscape).
type escapeGuard struct{ wg *WriteGuard }

func (g escapeGuard) Mask(a uint32) byte                  { return g.wg.Mask(a) }
func (g escapeGuard) Ready(a uint32) error                { return g.wg.ready(a) }
func (g escapeGuard) Check(a uint32, old, val byte) error { return g.wg.check(a, old, val, pathEscape) }
