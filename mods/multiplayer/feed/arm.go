package feed

import (
	"context"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// armPoseSlot makes sure link slot slot has a live pose_buf to write
// into, driving shadow_mode=5 and polling (60 x 50 ms) for the C side to
// publish one. It reports whether the slot is armed; poseBufPtrs[slot]
// is then the buffer.
func (f *Feeder) armPoseSlot(ctx context.Context, slot int) bool {
	// Re-poll each call: the receiving Dolphin's mini-Link state can
	// reset when the player reloads a save (mini_link_reset_state in
	// inject/src/multiplayer.c clears mailbox.pose_buf_ptrs and
	// pose_buf_states, then the next mode-5 draw lazy-allocs a NEW
	// pose_buf at a possibly-different address). If we cached the
	// old pointer, we'd write fresh poses into freed memory while
	// the new mini-Link reads only its seed, producing a Link that
	// stays frozen at the seed pose forever. Authoritative source
	// is the mailbox; use it every tick instead of caching once.
	//
	// forceRepoll: on the FIRST arm of a new session (sessionEpoch
	// changed), don't trust a pose_buf_state that still reads 1 from the
	// prior session — its pointer may be freed. Fall through to the poll
	// path, which re-asserts shadow_mode=5 and re-validates the pointer.
	d := f.m
	forceRepoll := f.armedEpoch[slot] != f.sessionEpoch
	if !forceRepoll {
		state, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(slot), 1)
		if len(state) == 1 && state[0] == 1 {
			ptr, _ := d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(slot))
			if ptr != 0 {
				if f.poseBufPtrs[slot] != ptr {
					if f.poseBufPtrs[slot] == 0 {
						log.Logf(f.rep, log.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
					} else {
						log.Logf(f.rep, log.OK, "pose-feed slot %d re-armed: pose_buf=0x%08X (was 0x%08X)",
							slot, ptr, f.poseBufPtrs[slot])
					}
					f.poseBufPtrs[slot] = ptr
				}
				f.armedEpoch[slot] = f.sessionEpoch
				return true
			}
		}
	}
	// State != 1, or a new session forced a re-poll. Drop our cached
	// pointer so we don't write to a stale address while waiting, then
	// drive shadow_mode=5 and poll for the (re-published) pose_buf.
	if f.poseBufPtrs[slot] != 0 {
		log.Logf(f.rep, log.Warn, "pose-feed slot %d disarmed: waiting for C-side re-alloc", slot)
		f.poseBufPtrs[slot] = 0
	}
	// Check the live byte, not only our own flag: loading a save state
	// mid-session restores the mailbox from the snapshot, which
	// usually has shadow_mode=0, and the C side then never allocates a
	// pose_buf for us to find. Re-assert mode 5 whenever it isn't.
	if mode, err := d.Read(mailbox.Mailbox+mailbox.OffShadowMode, 1); !f.shadowModeArmed || (err == nil && len(mode) == 1 && mode[0] != avatar.ModePoseFeed) {
		if f.shadowModeArmed {
			log.Logf(f.rep, log.Warn, "shadow_mode was reset (a save-state load?): setting pose-feed mode again")
		}
		avatar.SetPoseFeedMode(d)
		f.shadowModeArmed = true
	}
	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			return false
		case <-f.after(50 * time.Millisecond):
		}
		state, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(slot), 1)
		if len(state) == 1 && state[0] == 1 {
			ptr, _ := d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(slot))
			if ptr != 0 {
				f.poseBufPtrs[slot] = ptr
				f.armedEpoch[slot] = f.sessionEpoch
				log.Logf(f.rep, log.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
				return true
			}
		}
		if len(state) == 1 && (state[0] == 0xFD || state[0] == 0xFE) {
			log.Logf(f.rep, log.Err, "pose-feed slot %d alloc failed (state=0x%02X)", slot, state[0])
			return false
		}
	}
	return false
}

// poseBufOK guards the write of a remote's pose through a slot's
// pose_buf pointer. The pointer comes back out of the mailbox, and Write
// checks it only as a whole range, so the whole Link-sized write must
// land inside MEM1. Reports each bad pointer value once.
func (f *Feeder) poseBufOK(slot int) bool {
	ptr := f.poseBufPtrs[slot]
	if avatar.PoseDstInMEM1(ptr, avatar.JointCount*avatar.MtxBytes) {
		return true
	}
	if f.badPoseBuf[slot] != ptr {
		f.badPoseBuf[slot] = ptr
		log.Logf(f.rep, log.Err, "pose-feed slot %d: pose_buf 0x%08X is outside MEM1; not writing poses there", slot, ptr)
	}
	return false
}
