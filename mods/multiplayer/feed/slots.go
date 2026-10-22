package feed

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// slotState is an actor-puppet slot's lerp-smoothed position and whether
// Go has its actor up (active=1 in the mailbox).
type slotState struct {
	active           bool
	haveCur          bool
	curX, curY, curZ float32
	// away is the stage gate's decision this slot last moved under
	// (Feed snaps the lerp when it changes).
	away bool
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
	// hidden: the stage gate cleared the slot (its remote is in another
	// stage); it stays reserved and is re-armed when the remote is back.
	hidden bool
}

// actorSlot returns rp's actor-puppet slot, assigning a free one on first
// sight (ok is false when all MaxPuppets are taken). Every new assignment
// starts the remote's actorGrace wait (at now); the slot's actor (KAMOME
// / Rose) comes up only once that wait ends. The wait counts only time
// this game spends in a play stage (inPlay), and an actor that is up
// goes back down, and back to waiting, when this game leaves play. A
// wait is called off as soon as the remote sends a usable pose and
// holds, or can still get, a link slot: it is drawn as a Link, and an
// actor too would put an NPC on top of that Link.
// A remote with a pose but no link slot to get (one beyond avatar.Cap)
// keeps waiting and gets its actor, as does one that never sends a
// usable pose. A remote is usually first seen with its position alone
// (the pose is a separate message), hence the wait. A remote the stage
// gate holds away (in another stage) counts as this game out of play.
func (f *Feeder) actorSlot(rp *network.RemotePlayer, hasPose, away bool, now time.Time) (int, bool) {
	inPlay := func() bool { return !away && f.inPlay() }
	idx, ok := f.remoteToSlot[rp.ID]
	if !ok {
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
		f.actorWait[rp.ID] = now
	}
	since, waiting := f.actorWait[rp.ID]
	if !waiting {
		if f.slots[idx].active && !inPlay() {
			// This game left play (reset, quit to the title, file
			// select, a stage change), or the remote left this stage:
			// release the actor, which the C side then deletes, and
			// bring it back through the in-play grace.
			f.setActorActive(idx, false)
			f.actorWait[rp.ID] = now
		}
		return idx, true
	}
	if since.IsZero() {
		// Connected restarted the clock.
		since = now
		f.actorWait[rp.ID] = now
	}
	_, holdsLink := f.remoteToLinkSlot[rp.ID]
	switch {
	case hasPose && (holdsLink || len(f.remoteToLinkSlot) < avatar.Cap):
		delete(f.actorWait, rp.ID) // drawn as a Link: no actor
	case !inPlay():
		// The grace counts in-play time only: a remote seen on the
		// title or file select waits actorGrace from when this game
		// enters a play stage, so its pose can still arrive first.
		f.actorWait[rp.ID] = now
	case now.Sub(since) >= actorGrace:
		delete(f.actorWait, rp.ID)
		f.setActorActive(idx, true)
		log.Logf(f.rep, log.OK, "slot %d := player %d (%s)", idx, rp.ID, rp.Name)
	}
	return idx, true
}

// setActorActive writes actor slot idx's active flag (C spawns the
// actor while it is 1 and deletes it when it goes to 0) and records it.
func (f *Feeder) setActorActive(idx int, on bool) {
	v := f.zero
	if on {
		v = f.one
	}
	f.m.Write(SlotAddr(idx, mailbox.PuppetOffActive), v)
	f.slots[idx].active = on
}

// inPlay reports whether this game is in a play stage with Link loaded
// and no stage change pending: not the title screen or file select,
// where the C side has no Link to spawn an actor puppet next to (and
// deletes every actor puppet). A read error counts as not in play.
func (f *Feeder) inPlay() bool {
	_, ok := f.stageHere()
	return ok
}

// stageHere is this game's stage (mCurStage.mName) and whether it is in
// play (inPlay).
func (f *Feeder) stageHere() (string, bool) {
	link, err := f.m.ReadU32(addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return "", false
	}
	n := int(addr.NextStageEnable-addr.CurStageName) + 1
	b, err := f.m.Read(addr.CurStageName, n)
	if err != nil || len(b) != n {
		return "", false
	}
	if b[len(b)-1] != 0 {
		return "", false // a stage change is under way
	}
	name := b[:addr.StageNameLen]
	if n := bytes.IndexByte(name, 0); n >= 0 {
		name = name[:n]
	}
	s := string(name)
	return s, s != "" && s != addr.StageTitle && s != addr.StageFileSelect
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
