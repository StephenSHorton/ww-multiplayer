package feed

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/tunicsync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// The mailbox layout (base, field offsets, Puppet slot fields) is
// generated from inject/include/mailbox.h into wwapi/mailbox.
const MaxPuppets = mailbox.MaxPuppets

// SlotAddr is the address of field off (a mailbox.PuppetOff* offset)
// in puppet slot i.
func SlotAddr(i int, off uint32) uint32 {
	return mailbox.Mailbox + mailbox.OffPuppets + uint32(i)*mailbox.PuppetSize + off
}

// EnvFloat32 reads an environment variable as a float32, returning the
// fallback if unset or unparseable. Used for tunables that need to be
// changed per-launch (offsets, throttles) without a recompile.
func EnvFloat32(key string, fallback float32) float32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		fmt.Printf("warn: bad %s=%q (%v); using %g\n", key, v, err, fallback)
		return fallback
	}
	return float32(f)
}

// PuppetSync connects to addr, receives the remote players and draws
// each one in this Dolphin (a pose-feed Link slot, else an actor puppet)
// every 16 ms until ctx ends, reconnecting after a drop. It takes the
// self-filter name as a parameter (rather than reading WW_SELF_NAME) so host/join can
// plumb the player's name through without exporting an env var.
//
// connLog reports failed connects to addr, as in BroadcastPose; nil
// makes one that logs through rep.
//
// onClient, when non-nil, is invoked once with the freshly-created
// *network.Client so a caller (the TUI bridge) can register it for its status
// panel. The client survives reconnects (#8) — it's the same object across
// ReconnectLoop iterations — so a single registration is sufficient. CLI
// callers pass nil.
func PuppetSync(ctx context.Context, env Env, name, addr, selfFilter string, dolphinPID uint32, rep log.Reporter, connLog *network.ConnectLog, onClient func(*network.Client)) error {
	log.Logf(rep, log.Info, "=== Puppet Sync: %s <- %s ===", name, addr)

	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	rep.Log(log.OK, "Dolphin found.")

	client := env.NewClient(ctx, name)
	client.OnLog = func(msg string) {
		rep.Log(log.Net, msg)
	}
	if onClient != nil {
		onClient(client)
	}
	// Tunic colors (docs/09 §5.4): per-slot recolored body textures.
	// WW_TUNIC=off leaves the slots to the tunic-slot dev command.
	var tunics *avatar.Tunics
	if os.Getenv("WW_TUNIC") != "off" {
		tunics = avatar.NewTunics(d, func(format string, args ...any) { log.Logf(rep, log.Info, format, args...) })
		// Session over: stop the C swaps (the enables live in RAM and
		// would keep the local Link recolored). Runs before d.Close.
		defer tunics.DisableAll()
	}

	// Lerp-smoothed state per slot. lerpK = 0.2 closes ~80% of the gap in
	// ~5 ticks (~83 ms at 60 Hz). Raise for snappier tracking, lower for
	// more butter. Rotation is raw passthrough — angular lerp needs
	// shortest-arc handling; punt until it matters visibly.
	//
	// ALL of the following state lives at function scope so it SURVIVES
	// reconnects (#8): the connect/run callbacks for ReconnectLoop close
	// over it, and a transient drop must not lose slot assignments.
	const lerpK = 0.2
	type slotState struct {
		haveCur          bool
		curX, curY, curZ float32
	}
	var slots [MaxPuppets]slotState
	remoteToSlot := map[byte]int{}
	zero := make([]byte, 4)
	one := make([]byte, 4)
	binary.BigEndian.PutUint32(one, 1)

	// Pose-feed state. We can drive up to avatar.Cap Link puppets
	// from incoming remote poses. First remote with pose claims Link
	// slot 0, second claims slot 1, etc. Beyond that, additional
	// remotes still get their actor-puppet (KAMOME / NPC / TSUBO).
	remoteToLinkSlot := map[byte]int{}
	poseBufPtrs := [avatar.Cap]uint32{}
	// armedEpoch[slot] = the sessionEpoch the slot's pose_buf was last
	// validated in. A mismatch forces armPoseSlot to re-poll — defeating the
	// stale-buffer trap where pose_buf_state still reads 1 (with a now-freed
	// pointer) from the prior session after a reconnect.
	armedEpoch := [avatar.Cap]int{}
	for i := range armedEpoch {
		armedEpoch[i] = -1
	}
	shadowModeArmed := false
	announced := map[byte]bool{} // log "link slot := player N" once
	// parked[id] marks a remote whose despawn reservation expired (its link
	// slot was freed for reuse); it's skipped until a fresh pose un-parks it.
	parked := map[byte]bool{}
	// Log-once marks for poses we refuse to write into emulated RAM:
	// badRig[id] for a remote whose pose isn't a 42-joint Link skeleton,
	// badPoseBuf[slot] for the last out-of-MEM1 pose_buf pointer reported.
	badRig := map[byte]bool{}
	badPoseBuf := [avatar.Cap]uint32{}
	// sessionEpoch increments on every successful (re)connect; lastServerEpoch
	// tracks the server's boot epoch so a restart (epoch change) triggers a
	// full reset.
	sessionEpoch := 0
	var lastServerEpoch uint64
	haveServerEpoch := false

	// Per-link-slot displayed pose. Each tick we lerp this toward the
	// latest received pose for the slot's remote (poseLerpK fraction
	// per tick). Even at 60 Hz send, TCP arrival is bursty, so a hard
	// snap to the latest pose looks visibly jittery; EMA-style smoothing
	// soaks the jitter while staying responsive (k=0.5 closes ~94% of
	// the gap in 4 frames ≈ 66 ms). Allocated lazily on first pose so
	// we don't pre-commit a buffer size before knowing the joint count.
	const poseLerpK float32 = 0.5
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
	var linkSlots [avatar.Cap]linkSlotState
	// Render offset added on top of the localized pose's re-application.
	// Default 0 (real multiplayer renders Link #2 at remote's actual
	// world coords). Set WW_LINK2_OFFSET_X / _Y / _Z for loopback demos
	// so Link #2 doesn't overlap your own Link.
	link2OffsetX := EnvFloat32("WW_LINK2_OFFSET_X", 0)
	link2OffsetY := EnvFloat32("WW_LINK2_OFFSET_Y", 0)
	link2OffsetZ := EnvFloat32("WW_LINK2_OFFSET_Z", 0)
	if link2OffsetX != 0 || link2OffsetY != 0 || link2OffsetZ != 0 {
		log.Logf(rep, log.Info, "Link #2/3 render offset: (%.0f, %.0f, %.0f)",
			link2OffsetX, link2OffsetY, link2OffsetZ)
	}
	armPoseSlot := func(ctx context.Context, slot int) bool {
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
		forceRepoll := armedEpoch[slot] != sessionEpoch
		if !forceRepoll {
			state, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(slot), 1)
			if len(state) == 1 && state[0] == 1 {
				ptr, _ := d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(slot))
				if ptr != 0 {
					if poseBufPtrs[slot] != ptr {
						if poseBufPtrs[slot] == 0 {
							log.Logf(rep, log.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
						} else {
							log.Logf(rep, log.OK, "pose-feed slot %d re-armed: pose_buf=0x%08X (was 0x%08X)",
								slot, ptr, poseBufPtrs[slot])
						}
						poseBufPtrs[slot] = ptr
					}
					armedEpoch[slot] = sessionEpoch
					return true
				}
			}
		}
		// State != 1, or a new session forced a re-poll. Drop our cached
		// pointer so we don't write to a stale address while waiting, then
		// drive shadow_mode=5 and poll for the (re-published) pose_buf.
		if poseBufPtrs[slot] != 0 {
			log.Logf(rep, log.Warn, "pose-feed slot %d disarmed: waiting for C-side re-alloc", slot)
			poseBufPtrs[slot] = 0
		}
		// Check the live byte, not only our own flag: loading a save state
		// mid-session restores the mailbox from the snapshot, which
		// usually has shadow_mode=0, and the C side then never allocates a
		// pose_buf for us to find. Re-assert mode 5 whenever it isn't.
		if mode, err := d.Read(mailbox.Mailbox+mailbox.OffShadowMode, 1); !shadowModeArmed || (err == nil && len(mode) == 1 && mode[0] != avatar.ModePoseFeed) {
			if shadowModeArmed {
				log.Logf(rep, log.Warn, "shadow_mode was reset (a save-state load?): setting pose-feed mode again")
			}
			avatar.SetPoseFeedMode(d)
			shadowModeArmed = true
		}
		for i := 0; i < 60; i++ {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(50 * time.Millisecond):
			}
			state, _ := d.Read(mailbox.Mailbox+avatar.OffPoseBufState(slot), 1)
			if len(state) == 1 && state[0] == 1 {
				ptr, _ := d.ReadU32(mailbox.Mailbox + avatar.OffPoseBufPtr(slot))
				if ptr != 0 {
					poseBufPtrs[slot] = ptr
					armedEpoch[slot] = sessionEpoch
					log.Logf(rep, log.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
					return true
				}
			}
			if len(state) == 1 && (state[0] == 0xFD || state[0] == 0xFE) {
				log.Logf(rep, log.Err, "pose-feed slot %d alloc failed (state=0x%02X)", slot, state[0])
				return false
			}
		}
		return false
	}
	// Returns the link slot assigned to this remote, or -1 if no slot
	// is available (all avatar.Cap already taken by other remotes).
	pickLinkSlot := func(remoteID byte) int {
		if s, ok := remoteToLinkSlot[remoteID]; ok {
			return s
		}
		used := map[int]bool{}
		for _, s := range remoteToLinkSlot {
			used[s] = true
		}
		for s := 0; s < avatar.Cap; s++ {
			if !used[s] {
				remoteToLinkSlot[remoteID] = s
				return s
			}
		}
		return -1
	}
	// poseBufOK guards the write of a remote's pose through a slot's
	// pose_buf pointer. The pointer comes back out of the mailbox, and Write
	// checks it only as a whole range, so the whole Link-sized write must
	// land inside MEM1. Reports each bad pointer value once.
	poseBufOK := func(slot int) bool {
		ptr := poseBufPtrs[slot]
		if avatar.PoseDstInMEM1(ptr, avatar.JointCount*avatar.MtxBytes) {
			return true
		}
		if badPoseBuf[slot] != ptr {
			badPoseBuf[slot] = ptr
			log.Logf(rep, log.Err, "pose-feed slot %d: pose_buf 0x%08X is outside MEM1; not writing poses there", slot, ptr)
		}
		return false
	}

	// Vanished-sender thresholds (#10). freezeAfter: hold the last pose;
	// despawnAfter: clear the slot. despawnReserve keeps a despawned slot
	// reserved so the same remote resumes in place before it's freed for
	// reuse (the server's MsgLeave at serverReadTimeout is the slower
	// backstop).
	freezeAfter, despawnAfter := network.PoseLivenessDefaults()
	const despawnReserve = 10 * time.Second

	// freeLinkSlot fully releases a pose-feed link slot: stop the C-side
	// render (zero pose_seq + face_seq), drop the EMA buffer + id mapping,
	// and invalidate the arm cache so the next claimant re-arms from scratch.
	freeLinkSlot := func(linkSlot int, id byte) {
		avatar.ClearSlot(d, linkSlot)
		delete(remoteToLinkSlot, id)
		linkSlots[linkSlot] = linkSlotState{}
		armedEpoch[linkSlot] = -1
		poseBufPtrs[linkSlot] = 0
	}

	// connect (re)establishes the session and RE-ARMS the mailbox (#8 + D3).
	// Runs at startup and after every reconnect. A change in the welcome's
	// server epoch means the server restarted → full puppet reset.
	if connLog == nil {
		connLog = NewConnectLog(addr, rep)
	}
	connect := func(ctx context.Context) error {
		if err := connLog.Observe(ctx, client.ConnectContext(ctx, addr)); err != nil {
			return err
		}
		sessionEpoch++
		ep := client.ServerEpoch()
		serverRestarted := haveServerEpoch && ep != 0 && ep != lastServerEpoch
		lastServerEpoch = ep
		haveServerEpoch = true

		// Re-arm every pose slot: zero seqs + assert shadow_mode=5, and
		// invalidate cached pointers/epoch so armPoseSlot re-polls this
		// session (handles the stale pose_buf_state==1 trap). Mirrors
		// clearMultiplayerState + the re-arm half of armPoseSlot.
		for s := 0; s < avatar.Cap; s++ {
			avatar.Rearm(d, s)
			poseBufPtrs[s] = 0
			armedEpoch[s] = -1
		}
		shadowModeArmed = true

		if serverRestarted {
			// New server instance ⇒ new player ids; old slots are stale.
			// Drop every actor-puppet + link-slot mapping so fresh remotes
			// re-arm cleanly with no double-rendered orphan.
			for id, idx := range remoteToSlot {
				d.Write(SlotAddr(idx, mailbox.PuppetOffActive), zero)
				delete(remoteToSlot, id)
				slots[idx] = slotState{}
			}
			for s := range linkSlots {
				linkSlots[s] = linkSlotState{}
			}
			remoteToLinkSlot = map[byte]int{}
			announced = map[byte]bool{}
			parked = map[byte]bool{}
			badRig = map[byte]bool{}
			rep.Log(log.Warn, "server restarted (epoch changed): full puppet reset")
		}
		log.Logf(rep, log.OK, "puppet-sync session %d armed (server epoch %d)", sessionEpoch, ep)
		return nil
	}

	// selfFilter lets a puppet-sync attached to the SAME Dolphin as a
	// broadcast-pose twin ignore its twin's stream. Without this, the
	// twin's pose (= our local Link's live position) gets written into
	// a puppet actor that then physics-collides with our own Link. Empty
	// (default) keeps the loopback "mirror yourself with offset" demo
	// working. mplay2.sh still works because the CLI wrapper above reads
	// WW_SELF_NAME into this arg; `ww-multiplayer.exe host/join` pass the player name
	// so users never have to know the env var exists.
	if selfFilter != "" {
		log.Logf(rep, log.Info, "Filtering self-echo: remotes named %q will be ignored.", selfFilter)
	}

	// sessionLoop owns one live session: the 60 Hz receive→mailbox loop.
	// network.RunSession (in run, below) wraps it with a ctx-scoped teardown
	// watcher and WAITS for that watcher before returning, so a reconnect
	// can't overlap a stale watcher's (session-agnostic) Disconnect with the
	// next session's freshly-dialed conn — the self-chaining flap.
	sessionLoop := func(ctx context.Context) error {
		for client.IsConnected() {
			remotes := client.GetRemotePlayers()
			now := time.Now()
			seen := map[byte]bool{}
			for _, rp := range remotes {
				if rp.Position == nil {
					continue
				}
				if selfFilter != "" && rp.Name == selfFilter {
					continue
				}
				seen[rp.ID] = true
				// Only a whole 42-joint Link pose can drive a Link slot: the
				// C side's pose buffers are sized for exactly that. A remote
				// sending any other rig is shown as an actor puppet instead.
				hasPose := avatar.PoseUsable(rp.PoseJoints, len(rp.PoseMatrices))
				if rp.PoseMatrices != nil && !hasPose && !badRig[rp.ID] {
					badRig[rp.ID] = true
					log.Logf(rep, log.Warn, "player %d (%s) sent a %d-joint pose (%d B); Link has %d joints, so it won't drive a Link slot",
						rp.ID, rp.Name, rp.PoseJoints, len(rp.PoseMatrices), avatar.JointCount)
				}
				idx, ok := remoteToSlot[rp.ID]
				if !ok {
					// Find a free slot. Walk 0..N; first index not already
					// mapped to a live remote wins.
					used := map[int]bool{}
					for _, v := range remoteToSlot {
						used[v] = true
					}
					for i := 0; i < MaxPuppets; i++ {
						if !used[i] {
							idx = i
							break
						}
					}
					if used[idx] {
						// All slots full; drop this remote for now.
						continue
					}
					remoteToSlot[rp.ID] = idx
					// Only activate the actor-puppet (KAMOME / Rose / TSUBO)
					// for remotes without pose data. Pose-driven remotes
					// render as Link #2 directly; activating the actor too
					// would overlap an NPC on top of Link #2 at the same
					// coords, and C-side actor cleanup is best-effort (it
					// stops syncing but leaves the actor stuck at its last
					// position), so the duplicate sticks around forever.
					if !hasPose {
						d.Write(SlotAddr(idx, mailbox.PuppetOffActive), one)
						log.Logf(rep, log.OK, "slot %d := player %d (%s)", idx, rp.ID, rp.Name)
					}
				}

				tx, ty, tz := rp.Position.PosX, rp.Position.PosY, rp.Position.PosZ
				st := &slots[idx]
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
				d.Write(SlotAddr(idx, mailbox.PuppetOffPosX), posBuf)

				rotBuf := make([]byte, 6)
				binary.BigEndian.PutUint16(rotBuf[0:2], uint16(rp.Position.RotX))
				binary.BigEndian.PutUint16(rotBuf[2:4], uint16(rp.Position.RotY))
				binary.BigEndian.PutUint16(rotBuf[4:6], uint16(rp.Position.RotZ))
				d.Write(SlotAddr(idx, mailbox.PuppetOffRotX), rotBuf)

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
					// Vanished-sender classification (#10). LastPose stops
					// advancing the moment the remote's pose stream goes silent
					// (crash / cable pull), well before the server's MsgLeave at
					// serverReadTimeout.
					frozenNow, despawnedNow := network.PoseLiveness(rp.LastPose, now, freezeAfter, despawnAfter)

					// A parked remote (reservation expired, slot freed) stays
					// skipped until a fresh pose un-parks it.
					if parked[rp.ID] {
						if !frozenNow && !despawnedNow {
							delete(parked, rp.ID)
						} else {
							continue
						}
					}

					linkSlot := pickLinkSlot(rp.ID)
					if linkSlot >= 0 {
						ls := &linkSlots[linkSlot]

						// Sender RESUMED after a freeze/despawn: re-arm the
						// mailbox for this slot and snap back to live rendering.
						if !frozenNow && !despawnedNow && (ls.frozen || ls.despawned) {
							avatar.Rearm(d, linkSlot)
							poseBufPtrs[linkSlot] = 0
							armedEpoch[linkSlot] = -1
							ls.displayed = nil
							ls.frozen = false
							ls.despawned = false
							log.Logf(rep, log.OK, "link slot %d resumed: player %d pose fresh again", linkSlot, rp.ID)
						}

						switch {
						case despawnedNow:
							// Silent long enough → clear the slot (Link vanishes)
							// but keep it RESERVED so a late resume from the same
							// remote lands back here. A dual safety net alongside
							// the server's MsgLeave-on-timeout.
							if !ls.despawned {
								avatar.ClearSlot(d, linkSlot)
								ls.despawned = true
								ls.frozen = true
								ls.despawnAt = now
								log.Logf(rep, log.Warn, "link slot %d despawned: player %d silent (no MsgLeave yet)", linkSlot, rp.ID)
							}
						case frozenNow:
							// Brief silence → stop bumping pose_seq; the C side
							// holds the last pose so Link visually FREEZES rather
							// than snapping to a stale sample or vanishing.
							if !ls.frozen {
								ls.frozen = true
								log.Logf(rep, log.Info, "link slot %d frozen: player %d pose stalled", linkSlot, rp.ID)
							}
						default:
							// Normal path: arm + write the fresh pose (only through
							// a pose_buf pointer that keeps the write in MEM1).
							if armPoseSlot(ctx, linkSlot) && poseBufOK(linkSlot) {
								// First time this remote got a link slot — also
								// release their actor-puppet so we don't see both
								// a Link AND a KAMOME at the same position.
								d.Write(SlotAddr(idx, mailbox.PuppetOffActive), zero)
								if _, alreadyLogged := announced[rp.ID]; !alreadyLogged {
									log.Logf(rep, log.OK, "link slot %d := player %d (%s)", linkSlot, rp.ID, rp.Name)
									announced[rp.ID] = true
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
										st.curX+link2OffsetX, st.curY+link2OffsetY, st.curZ+link2OffsetZ)
								}
								// WritePose bumps pose_seq with NextSeq, which
								// skips 0: C reads 0 as "no pose yet" and would
								// skip this slot for a frame.
								avatar.WritePose(d, linkSlot, poseBufPtrs[linkSlot], adjusted)

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
									avatar.WriteFace(d, linkSlot, rp.FaceState[:player.FaceBytes])
								}
							}
						}
					}
				}
			}

			// Release slots for remotes that have disconnected (MsgLeave, or the
			// server's read-timeout teardown removed them from the player list).
			for id, idx := range remoteToSlot {
				if !seen[id] {
					d.Write(SlotAddr(idx, mailbox.PuppetOffActive), zero)
					delete(remoteToSlot, id)
					slots[idx] = slotState{}
					log.Logf(rep, log.Info, "slot %d freed (player %d left)", idx, id)
					if linkSlot, ok := remoteToLinkSlot[id]; ok {
						// freeLinkSlot zeroes pose_seq + face_seq (C-side stops
						// rendering Link #2; otherwise it'd stay frozen at the
						// last pose forever), drops the EMA buffer, and clears
						// the arm cache so the next claimant re-arms cleanly.
						freeLinkSlot(linkSlot, id)
						log.Logf(rep, log.Info, "link slot %d freed (will be reassigned on next pose)", linkSlot)
					}
					delete(announced, id)
					delete(parked, id)
					delete(badRig, id)
				}
			}

			if tunics != nil {
				mine, haveMine := client.MyColor()
				tunicsync.SyncTunics(tunics, mine, haveMine, remotes, remoteToLinkSlot, now)
			}

			// Reservation expiry (#10): a slot despawned longer than
			// despawnReserve is freed for reuse by ANOTHER remote even if the
			// dead sender hasn't formally left yet (its MsgLeave may lag). The
			// remote is parked so it won't immediately re-grab the slot; a fresh
			// pose un-parks it.
			for id, linkSlot := range remoteToLinkSlot {
				ls := &linkSlots[linkSlot]
				if ls.despawned && !ls.despawnAt.IsZero() && now.Sub(ls.despawnAt) > despawnReserve {
					freeLinkSlot(linkSlot, id)
					parked[id] = true
					// Drop the "announced" mark so a resume-after-expiry
					// re-logs the slot assignment.
					delete(announced, id)
					log.Logf(rep, log.Info, "link slot %d reservation expired: freed for reuse (player %d)", linkSlot, id)
				}
			}

			select {
			case <-ctx.Done():
				// RunSession's watcher will Disconnect(); IsConnected() will
				// flip false on the next iteration and the loop exits.
			case <-time.After(16 * time.Millisecond): // ~60 Hz
			}
		}
		rep.Log(log.Info, "Disconnected.")
		return nil
	}
	run := func(ctx context.Context) error {
		return network.RunSession(ctx, client.Disconnect, sessionLoop)
	}

	return network.ReconnectLoop(ctx, connect, run, network.NewBackoff(), network.SleepCtx)
}
