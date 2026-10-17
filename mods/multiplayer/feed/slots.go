package feed

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// slotState is an actor-puppet slot's lerp-smoothed position.
type slotState struct {
	haveCur          bool
	curX, curY, curZ float32
}

// linkSlotState is a pose-feed link slot's displayed pose and liveness.
type linkSlotState struct {
	displayed []byte
	// Vanished-sender state (#10 / D4). frozen: stop bumping pose_seq so
	// the C side holds the last frame (Link freezes in place). despawned:
	// slot cleared (Link gone) but RESERVED ~despawnReserve so the same
	// remote resumes in place; despawnAt timestamps it for that timer.
	frozen    bool
	despawned bool
	despawnAt time.Time
}

// actorSlot returns rp's actor-puppet slot, assigning a free one on first
// sight (ok is false when all MaxPuppets are taken). A new slot's actor
// (KAMOME / Rose / TSUBO) is activated only for a remote without pose
// data. Pose-driven remotes render as Link #2 directly; activating the
// actor too would overlap an NPC on top of Link #2 at the same coords,
// and C-side actor cleanup is best-effort (it stops syncing but leaves
// the actor stuck at its last position), so the duplicate sticks around
// forever.
func (f *Feeder) actorSlot(rp *network.RemotePlayer, hasPose bool) (int, bool) {
	idx, ok := f.remoteToSlot[rp.ID]
	if ok {
		return idx, true
	}
	// Find a free slot. Walk 0..N; first index not already
	// mapped to a live remote wins.
	used := map[int]bool{}
	for _, v := range f.remoteToSlot {
		used[v] = true
	}
	for i := 0; i < MaxPuppets; i++ {
		if !used[i] {
			idx = i
			break
		}
	}
	if used[idx] {
		return 0, false
	}
	f.remoteToSlot[rp.ID] = idx
	if !hasPose {
		f.m.Write(SlotAddr(idx, mailbox.PuppetOffActive), f.one)
		log.Logf(f.rep, log.OK, "slot %d := player %d (%s)", idx, rp.ID, rp.Name)
	}
	return idx, true
}

// moveActor lerps actor slot idx toward pos (lerpK per tick; the first
// sample snaps) and writes the smoothed position and the raw rotation
// into the slot. It returns the slot's state, whose smoothed position the
// pose feed re-applies the localized pose at.
func (f *Feeder) moveActor(idx int, pos *network.PlayerPosition) *slotState {
	tx, ty, tz := pos.PosX, pos.PosY, pos.PosZ
	st := &f.slots[idx]
	if !st.haveCur {
		st.curX, st.curY, st.curZ = tx, ty, tz
		st.haveCur = true
	} else {
		st.curX += (tx - st.curX) * lerpK
		st.curY += (ty - st.curY) * lerpK
		st.curZ += (tz - st.curZ) * lerpK
	}

	posBuf := make([]byte, 12)
	binary.BigEndian.PutUint32(posBuf[0:4], math.Float32bits(st.curX))
	binary.BigEndian.PutUint32(posBuf[4:8], math.Float32bits(st.curY))
	binary.BigEndian.PutUint32(posBuf[8:12], math.Float32bits(st.curZ))
	f.m.Write(SlotAddr(idx, mailbox.PuppetOffPosX), posBuf)

	rotBuf := make([]byte, 6)
	binary.BigEndian.PutUint16(rotBuf[0:2], uint16(pos.RotX))
	binary.BigEndian.PutUint16(rotBuf[2:4], uint16(pos.RotY))
	binary.BigEndian.PutUint16(rotBuf[4:6], uint16(pos.RotZ))
	f.m.Write(SlotAddr(idx, mailbox.PuppetOffRotX), rotBuf)
	return st
}

// pickLinkSlot returns the link slot assigned to this remote, or -1 if no
// slot is available (all avatar.Cap already taken by other remotes).
func (f *Feeder) pickLinkSlot(remoteID byte) int {
	if s, ok := f.remoteToLinkSlot[remoteID]; ok {
		return s
	}
	used := map[int]bool{}
	for _, s := range f.remoteToLinkSlot {
		used[s] = true
	}
	for s := 0; s < avatar.Cap; s++ {
		if !used[s] {
			f.remoteToLinkSlot[remoteID] = s
			return s
		}
	}
	return -1
}

// freeLinkSlot fully releases a pose-feed link slot: stop the C-side
// render (zero pose_seq + face_seq), drop the EMA buffer + id mapping,
// and invalidate the arm cache so the next claimant re-arms from scratch.
func (f *Feeder) freeLinkSlot(linkSlot int, id byte) {
	avatar.ClearSlot(f.m, linkSlot)
	delete(f.remoteToLinkSlot, id)
	f.linkSlots[linkSlot] = linkSlotState{}
	f.armedEpoch[linkSlot] = -1
	f.poseBufPtrs[linkSlot] = 0
}
