package feed

import (
	"context"
	"encoding/binary"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Mem is the Dolphin memory access a Feeder needs; *emu.Dolphin and
// avatartest.RAM implement it.
type Mem interface {
	Read(addr uint32, n int) ([]byte, error)
	ReadU32(addr uint32) (uint32, error)
	Write(addr uint32, data []byte) error
}

// Lerp-smoothed state per slot. lerpK = 0.2 closes ~80% of the gap in
// ~5 ticks (~83 ms at 60 Hz). Raise for snappier tracking, lower for
// more butter. Rotation is raw passthrough — angular lerp needs
// shortest-arc handling; punt until it matters visibly.
const lerpK = 0.2

// Per-link-slot displayed pose. Each tick we lerp this toward the
// latest received pose for the slot's remote (poseLerpK fraction
// per tick). Even at 60 Hz send, TCP arrival is bursty, so a hard
// snap to the latest pose looks visibly jittery; EMA-style smoothing
// soaks the jitter while staying responsive (k=0.5 closes ~94% of
// the gap in 4 frames ≈ 66 ms). Allocated lazily on first pose so
// we don't pre-commit a buffer size before knowing the joint count.
const poseLerpK float32 = 0.5

// despawnReserve keeps a despawned link slot reserved so the same remote
// resumes in place before it's freed for reuse (the server's MsgLeave at
// serverReadTimeout is the slower backstop). See freezeAfter/despawnAfter.
const despawnReserve = 10 * time.Second

// actorGrace is how long a remote must go without a usable pose and a
// link slot before its actor puppet (KAMOME / NPC_OB1) comes up (see
// actorSlot). Position and pose are separate messages, so a remote whose
// Link has just appeared (its game left the title screen, or it just
// connected) is usually first seen with a position only; its pose
// follows a few ticks later and it is drawn as a Link, no actor needed.
// The C side deletes an actor puppet when its slot goes inactive and on
// every stage change (inject/src/mp/puppets.c puppet_reap): before
// that, a KAMOME left behind this way outlived its stage, read its old
// room's path and froze the game ("Invalid read from 0x1a0d0013, PC =
// 0x80f24230", d_a_kamome). The wait keeps that spawn-and-delete churn
// off the usual join.
const actorGrace = 2 * time.Second

// Feeder is PuppetSync's receive side over wwapi/avatar: it draws the
// remote players in one Dolphin. A remote with a whole Link pose claims
// one of the avatar.Cap pose-feed (mini-Link) slots, first come first
// served; every remote also gets an actor-puppet slot (KAMOME / NPC_OB1 /
// TSUBO, MaxPuppets of them) that carries its smoothed position, and the
// actor is drawn only for a remote that, actorGrace after it was first
// seen, still has no usable pose or no link slot to draw it in, and only
// until its pose takes a link slot. So players beyond avatar.Cap fall
// back to the actor puppet (decision 5 in docs/api/00-design.md).
//
// ALL of its state SURVIVES reconnects (#8): PuppetSync's connect/run
// callbacks for ReconnectLoop share one Feeder, and a transient drop must
// not lose slot assignments. A Feeder is not safe for concurrent use.
type Feeder struct {
	m          Mem
	rep        log.Reporter
	selfFilter string

	// Render offset added on top of the localized pose's re-application.
	// Default 0 (real multiplayer renders Link #2 at remote's actual
	// world coords). Set WW_LINK2_OFFSET_X / _Y / _Z for loopback demos
	// so Link #2 doesn't overlap your own Link.
	offX, offY, offZ float32

	// after is time.After; tests make the arming poll immediate.
	after func(time.Duration) <-chan time.Time

	zero, one []byte

	// Actor puppets (slots.go).
	slots        [MaxPuppets]slotState
	remoteToSlot map[byte]int
	// actorWait[id] is when remote id's actor-puppet slot was assigned,
	// while its actorGrace wait runs (actorSlot); the zero time means
	// "restart the clock on the next tick" (Connected).
	actorWait map[byte]time.Time

	// Pose-feed state. We can drive up to avatar.Cap Link puppets
	// from incoming remote poses. First remote with pose claims Link
	// slot 0, second claims slot 1, etc. Beyond that, additional
	// remotes still get their actor-puppet (KAMOME / NPC / TSUBO).
	remoteToLinkSlot map[byte]int
	linkSlots        [avatar.Cap]linkSlotState
	announced        map[byte]bool // log "link slot := player N" once
	// parked[id] marks a remote whose despawn reservation expired (its link
	// slot was freed for reuse); it's skipped until a fresh pose un-parks it.
	parked map[byte]bool
	// Log-once marks for poses we refuse to write into emulated RAM:
	// badRig[id] for a remote whose pose isn't a 42-joint Link skeleton,
	// badPoseBuf[slot] for the last out-of-MEM1 pose_buf pointer reported.
	badRig     map[byte]bool
	badPoseBuf [avatar.Cap]uint32

	// Arming (arm.go).
	poseBufPtrs [avatar.Cap]uint32
	// armedEpoch[slot] = the sessionEpoch the slot's pose_buf was last
	// validated in. A mismatch forces armPoseSlot to re-poll — defeating the
	// stale-buffer trap where pose_buf_state still reads 1 (with a now-freed
	// pointer) from the prior session after a reconnect.
	armedEpoch      [avatar.Cap]int
	shadowModeArmed bool
	// sessionEpoch increments on every successful (re)connect; lastServerEpoch
	// tracks the server's boot epoch so a restart (epoch change) triggers a
	// full reset.
	sessionEpoch    int
	lastServerEpoch uint64
	haveServerEpoch bool

	// Vanished-sender thresholds (#10). freezeAfter: hold the last pose;
	// despawnAfter: clear the slot.
	freezeAfter, despawnAfter time.Duration

	// The stage gate (partner.go): partner is where co-op's PRESENCE puts
	// the partner (nil: no gate); away is the gate's last decision.
	partner *PartnerPlace
	away    bool
	// placeAway[id] is the per-remote stage gate's last decision for a
	// remote whose positions carry a place (place.go, gateFor).
	placeAway map[byte]bool
}

// NewFeeder returns a Feeder drawing into m. Remotes named selfFilter
// (when non-empty) are ignored; it reads WW_LINK2_OFFSET_{X,Y,Z} and the
// network's pose liveness knobs once, here.
func NewFeeder(m Mem, selfFilter string, rep log.Reporter) *Feeder {
	f := &Feeder{
		m:                m,
		rep:              rep,
		selfFilter:       selfFilter,
		after:            time.After,
		zero:             make([]byte, 4),
		one:              make([]byte, 4),
		remoteToSlot:     map[byte]int{},
		actorWait:        map[byte]time.Time{},
		remoteToLinkSlot: map[byte]int{},
		announced:        map[byte]bool{},
		parked:           map[byte]bool{},
		badRig:           map[byte]bool{},
		placeAway:        map[byte]bool{},
	}
	binary.BigEndian.PutUint32(f.one, 1)
	for i := range f.armedEpoch {
		f.armedEpoch[i] = -1
	}
	f.offX = EnvFloat32("WW_LINK2_OFFSET_X", 0)
	f.offY = EnvFloat32("WW_LINK2_OFFSET_Y", 0)
	f.offZ = EnvFloat32("WW_LINK2_OFFSET_Z", 0)
	if f.offX != 0 || f.offY != 0 || f.offZ != 0 {
		log.Logf(rep, log.Info, "Link #2/3 render offset: (%.0f, %.0f, %.0f)",
			f.offX, f.offY, f.offZ)
	}
	f.freezeAfter, f.despawnAfter = network.PoseLivenessDefaults()
	return f
}

// LinkSlots is the remote ID → pose-feed slot map (for the tunic slots,
// tunicsync.SyncTunics). It is the Feeder's own map: read it, don't keep
// it past the next Feed or Expire.
func (f *Feeder) LinkSlots() map[byte]int { return f.remoteToLinkSlot }

// Connected re-arms the mailbox after a successful (re)connect (#8 + D3);
// serverEpoch is the welcome's (network.Client.ServerEpoch). A change in
// it means the server restarted → full puppet reset.
func (f *Feeder) Connected(serverEpoch uint64) {
	f.sessionEpoch++
	ep := serverEpoch
	serverRestarted := f.haveServerEpoch && ep != 0 && ep != f.lastServerEpoch
	f.lastServerEpoch = ep
	f.haveServerEpoch = true

	// Re-arm every pose slot: zero seqs + assert shadow_mode=5, and
	// invalidate cached pointers/epoch so armPoseSlot re-polls this
	// session (handles the stale pose_buf_state==1 trap). Mirrors
	// clearMultiplayerState + the re-arm half of armPoseSlot.
	for s := 0; s < avatar.Cap; s++ {
		avatar.Rearm(f.m, s)
		f.poseBufPtrs[s] = 0
		f.armedEpoch[s] = -1
	}
	f.shadowModeArmed = true
	// Face sync arms with the pose feed (F20): the C bracket draws each
	// mini-Link with its slot's face only while face_hook_enable is set.
	avatar.SetFaceSync(f.m, true)

	// A pending actorGrace wait starts over: the new connection delivers
	// each remote's position before its pose again.
	for id := range f.actorWait {
		f.actorWait[id] = time.Time{}
	}

	if serverRestarted {
		// New server instance ⇒ new player ids; old slots are stale.
		// Drop every actor-puppet + link-slot mapping so fresh remotes
		// re-arm cleanly with no double-rendered orphan.
		for id, idx := range f.remoteToSlot {
			f.m.Write(SlotAddr(idx, mailbox.PuppetOffActive), f.zero)
			delete(f.remoteToSlot, id)
			f.slots[idx] = slotState{}
		}
		f.actorWait = map[byte]time.Time{}
		for s := range f.linkSlots {
			f.linkSlots[s] = linkSlotState{}
		}
		f.remoteToLinkSlot = map[byte]int{}
		f.announced = map[byte]bool{}
		f.parked = map[byte]bool{}
		f.badRig = map[byte]bool{}
		f.placeAway = map[byte]bool{}
		f.rep.Log(log.Warn, "server restarted (epoch changed): full puppet reset")
	}
	log.Logf(f.rep, log.OK, "puppet-sync session %d armed (server epoch %d)", f.sessionEpoch, ep)
}

// Feed draws one tick of remotes (network.Client.GetRemotePlayers) at
// now: actor-puppet positions, and the pose and face of every remote
// holding a link slot. It then releases the slots of remotes that have
// left. ctx bounds a link slot's arming poll.
func (f *Feeder) Feed(ctx context.Context, remotes []network.RemotePlayer, now time.Time) {
	seen := map[byte]bool{}
	gate := f.gateFor(remotes, now)
	for i := range remotes {
		rp := &remotes[i]
		if rp.Position == nil {
			continue
		}
		if f.selfFilter != "" && rp.Name == f.selfFilter {
			continue
		}
		seen[rp.ID] = true
		// Only a whole 42-joint Link pose can drive a Link slot: the
		// C side's pose buffers are sized for exactly that. A remote
		// sending any other rig is shown as an actor puppet instead.
		hasPose := avatar.PoseUsable(rp.PoseJoints, len(rp.PoseMatrices))
		if rp.PoseMatrices != nil && !hasPose && !f.badRig[rp.ID] {
			f.badRig[rp.ID] = true
			log.Logf(f.rep, log.Warn, "player %d (%s) sent a %d-joint pose (%d B); Link has %d joints, so it won't drive a Link slot",
				rp.ID, rp.Name, rp.PoseJoints, len(rp.PoseMatrices), avatar.JointCount)
		}
		away := gate(rp)
		idx, ok := f.actorSlot(rp, hasPose, away, now)
		if !ok {
			// All slots full; drop this remote for now.
			continue
		}
		if sl := &f.slots[idx]; sl.away != away {
			// Into or out of another stage: snap to where it is now
			// rather than glide there from the other stage's spot.
			sl.away, sl.haveCur = away, false
		}
		st := f.moveActor(idx, rp.Position)

		// Pose feed (shadow_mode=5). First remote to deliver any pose
		// claims the Link-#2 driver slot; subsequent remotes still
		// puppet through the actor pipeline above. MAX_REMOTE_LINKS=1
		// for v0. Sender ships a LOCALIZED pose (translations
		// relative to its own world position); receiver re-adds
		// the smoothed remote position + WW_LINK2_OFFSET so Link #2
		// lands at the right world coords. The offset is normally 0
		// for two-Dolphin play; set it to e.g. 500 for loopback
		// development so Link #2 doesn't overlap your own Link.
		if hasPose {
			f.feedPose(ctx, rp, idx, st, away, now)
		}
	}
	f.releaseGone(seen)
}

// feedPose is one tick of a pose-sending remote: liveness (freeze,
// despawn, resume), its link slot, and the pose + face write. While away
// (the stage gate) its slot is cleared, as a despawn clears it, and kept.
func (f *Feeder) feedPose(ctx context.Context, rp *network.RemotePlayer, idx int, st *slotState, away bool, now time.Time) {
	// Vanished-sender classification (#10). LastPose stops
	// advancing the moment the remote's pose stream goes silent
	// (crash / cable pull), well before the server's MsgLeave at
	// serverReadTimeout.
	frozenNow, despawnedNow := network.PoseLiveness(rp.LastPose, now, f.freezeAfter, f.despawnAfter)

	// A parked remote (reservation expired, slot freed) stays
	// skipped until a fresh pose un-parks it.
	if f.parked[rp.ID] {
		if !frozenNow && !despawnedNow {
			delete(f.parked, rp.ID)
		} else {
			return
		}
	}

	linkSlot := f.pickLinkSlot(rp.ID)
	if linkSlot < 0 {
		return
	}
	ls := &f.linkSlots[linkSlot]

	// The stage gate: out of this stage, the pose's coordinates are
	// another stage's. Clear the slot (Link vanishes) but keep it; back
	// in this stage, re-arm it as a resume does and draw again.
	if away {
		if !ls.hidden {
			avatar.ClearSlot(f.m, linkSlot)
			ls.hidden = true
			ls.displayed = nil
		}
		return
	}
	if ls.hidden {
		avatar.Rearm(f.m, linkSlot)
		f.poseBufPtrs[linkSlot] = 0
		f.armedEpoch[linkSlot] = -1
		ls.displayed = nil
		ls.hidden = false
	}

	// Sender RESUMED after a freeze/despawn: re-arm the
	// mailbox for this slot and snap back to live rendering.
	if !frozenNow && !despawnedNow && (ls.frozen || ls.despawned) {
		avatar.Rearm(f.m, linkSlot)
		f.poseBufPtrs[linkSlot] = 0
		f.armedEpoch[linkSlot] = -1
		ls.displayed = nil
		ls.frozen = false
		ls.despawned = false
		log.Logf(f.rep, log.OK, "link slot %d resumed: player %d pose fresh again", linkSlot, rp.ID)
	}

	switch {
	case despawnedNow:
		// Silent long enough → clear the slot (Link vanishes)
		// but keep it RESERVED so a late resume from the same
		// remote lands back here. A dual safety net alongside
		// the server's MsgLeave-on-timeout.
		if !ls.despawned {
			avatar.ClearSlot(f.m, linkSlot)
			ls.despawned = true
			ls.frozen = true
			ls.despawnAt = now
			log.Logf(f.rep, log.Warn, "link slot %d despawned: player %d silent (no MsgLeave yet)", linkSlot, rp.ID)
		}
	case frozenNow:
		// Brief silence → stop bumping pose_seq; the C side
		// holds the last pose so Link visually FREEZES rather
		// than snapping to a stale sample or vanishing.
		if !ls.frozen {
			ls.frozen = true
			log.Logf(f.rep, log.Info, "link slot %d frozen: player %d pose stalled", linkSlot, rp.ID)
		}
	default:
		// Normal path: arm + write the fresh pose (only through
		// a pose_buf pointer that keeps the write in MEM1).
		if f.armPoseSlot(ctx, linkSlot) && f.poseBufOK(linkSlot) {
			// First time this remote got a link slot — also
			// release their actor-puppet so we don't see both
			// a Link AND a KAMOME at the same position.
			f.setActorActive(idx, false)
			if _, alreadyLogged := f.announced[rp.ID]; !alreadyLogged {
				log.Logf(f.rep, log.OK, "link slot %d := player %d (%s)", linkSlot, rp.ID, rp.Name)
				f.announced[rp.ID] = true
			}
			if ls.displayed == nil || len(ls.displayed) != len(rp.PoseMatrices) {
				// First pose for this slot, or joint count
				// changed — snap to the target so we don't
				// EMA from zero (which would render a degenerate
				// pose for the first few frames).
				ls.displayed = make([]byte, len(rp.PoseMatrices))
				copy(ls.displayed, rp.PoseMatrices)
			} else {
				avatar.Lerp(ls.displayed, rp.PoseMatrices, poseLerpK)
			}
			adjusted := make([]byte, len(ls.displayed))
			copy(adjusted, ls.displayed)
			if os.Getenv("WW_POSE_RAW") == "" {
				avatar.ApplyAt(adjusted, rp.PoseJoints,
					st.curX+f.offX, st.curY+f.offY, st.curZ+f.offZ)
			}
			// WritePose bumps pose_seq with NextSeq, which
			// skips 0: C reads 0 as "no pose yet" and would
			// skip this slot for a frame.
			avatar.WritePose(f.m, linkSlot, f.poseBufPtrs[linkSlot], adjusted)

			// Session 9: face-state suffix (eye texno values).
			// Writer is the C-side J3DMatPacket vtable hook —
			// it reads mailbox.face_state[slot] when mini-link's
			// eye matpackets draw, and only swaps when
			// face_seqs[slot] != 0. So we ALWAYS write fresh
			// bytes + bump the seq when we have face data,
			// even if it didn't change frame-to-frame (the C
			// hook reads a fresh memory location each draw,
			// not a stale cache; bumping the seq is just for
			// the gating).
			if len(rp.FaceState) >= player.FaceBytes {
				// 0 means "not yet armed" per the C-side
				// gate; WriteFace's NextSeq skips it on
				// rollover so the gate stays open.
				avatar.WriteFace(f.m, linkSlot, rp.FaceState[:player.FaceBytes])
			}
		}
	}
}

// releaseGone releases the slots of remotes not in seen: they have
// disconnected (MsgLeave, or the server's read-timeout teardown removed
// them from the player list).
func (f *Feeder) releaseGone(seen map[byte]bool) {
	for id, idx := range f.remoteToSlot {
		if !seen[id] {
			f.m.Write(SlotAddr(idx, mailbox.PuppetOffActive), f.zero)
			delete(f.remoteToSlot, id)
			delete(f.actorWait, id)
			f.slots[idx] = slotState{}
			log.Logf(f.rep, log.Info, "slot %d freed (player %d left)", idx, id)
			if linkSlot, ok := f.remoteToLinkSlot[id]; ok {
				// freeLinkSlot zeroes pose_seq + face_seq (C-side stops
				// rendering Link #2; otherwise it'd stay frozen at the
				// last pose forever), drops the EMA buffer, and clears
				// the arm cache so the next claimant re-arms cleanly.
				f.freeLinkSlot(linkSlot, id)
				log.Logf(f.rep, log.Info, "link slot %d freed (will be reassigned on next pose)", linkSlot)
			}
			delete(f.announced, id)
			delete(f.parked, id)
			delete(f.badRig, id)
			delete(f.placeAway, id)
		}
	}
}

// Expire ends despawn reservations (#10): a slot despawned longer than
// despawnReserve is freed for reuse by ANOTHER remote even if the
// dead sender hasn't formally left yet (its MsgLeave may lag). The
// remote is parked so it won't immediately re-grab the slot; a fresh
// pose un-parks it.
func (f *Feeder) Expire(now time.Time) {
	for id, linkSlot := range f.remoteToLinkSlot {
		ls := &f.linkSlots[linkSlot]
		if ls.despawned && !ls.despawnAt.IsZero() && now.Sub(ls.despawnAt) > despawnReserve {
			f.freeLinkSlot(linkSlot, id)
			f.parked[id] = true
			// Drop the "announced" mark so a resume-after-expiry
			// re-logs the slot assignment.
			delete(f.announced, id)
			log.Logf(f.rep, log.Info, "link slot %d reservation expired: freed for reuse (player %d)", linkSlot, id)
		}
	}
}
