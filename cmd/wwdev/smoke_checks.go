package main

// smoke's checks (smoke.go runs them): what one read of a game says
// (gameSample) and the verdicts over a run of them, the remote tracking
// verdict and the co-op tag verdict. Everything here reads memory or
// takes values, so the unit tests drive it with mem.FakeMem.

import (
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/crash"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// smokeCheck is one verdict in the report.
type smokeCheck struct {
	Name    string         `json:"name"`
	Phase   string         `json:"phase"`
	Pass    bool           `json:"pass"`
	Skipped bool           `json:"skipped,omitempty"` // not run: says why in Detail; neither a pass nor a fail
	Detail  string         `json:"detail"`
	Values  map[string]any `json:"values,omitempty"`
}

func passIf(ok bool, name, detail string, values map[string]any) smokeCheck {
	return smokeCheck{Name: name, Pass: ok, Detail: detail, Values: values}
}

// gameSample is one read of a game's liveness words.
type gameSample struct {
	Heartbeat  uint32 // mailbox.spawn_trigger, bumped by T2 every frame
	REL        mailbox.REL
	PublishSeq uint8             // pose_publish_seq: this Link's pose published (the sender side)
	PoseSeqs   [avatar.Cap]uint8 // pose_seqs: Go's writes into each mini-Link slot (the receiver side)
	ShadowMode uint8             // 5 while the pose feed is armed
	VisMagic   uint32            // the visuals block's magic (Go's)
	VisErr     uint32            // the visuals block's last-error word (the REL's, WWVIS_STAT_ERR_OFF)
	Crash      crash.Exception   // exCallbackObject: Caught once JUTException caught a fault
	Err        string            // a read that failed
}

// readGameSample reads m's liveness words; a failed read is in Err and
// the rest stays zero.
func readGameSample(m mem.Reader) gameSample {
	var s gameSample
	var err error
	fail := func(what string, e error) {
		if s.Err == "" {
			s.Err = fmt.Sprintf("%s: %v", what, e)
		}
	}
	if s.Heartbeat, err = mem.ReadU32(m, mailbox.SpawnTrigger); err != nil {
		fail("spawn_trigger", err)
	}
	if s.REL, err = mailbox.ReadREL(m); err != nil {
		fail("REL words", err)
	}
	if s.PublishSeq, err = mem.ReadU8(m, mailbox.Mailbox+mailbox.OffPosePublishSeq); err != nil {
		fail("pose_publish_seq", err)
	}
	if b, err := m.Read(mailbox.Mailbox+mailbox.OffPoseSeqs, avatar.Cap); err != nil || len(b) != avatar.Cap {
		fail("pose_seqs", err)
	} else {
		copy(s.PoseSeqs[:], b)
	}
	if s.ShadowMode, err = mem.ReadU8(m, mailbox.Mailbox+mailbox.OffShadowMode); err != nil {
		fail("shadow_mode", err)
	}
	if s.VisMagic, err = mem.ReadU32(m, mailbox.VisAddr+mailbox.VisMagicOff); err != nil {
		fail("visuals magic", err)
	}
	if s.VisErr, err = mem.ReadU32(m, mailbox.VisAddr+mailbox.VisStatErrOff); err != nil {
		fail("visuals last error", err)
	}
	if s.Crash, err = crash.ReadException(m); err != nil {
		fail("exCallbackObject", err)
	}
	return s
}

// smokeLimits are the thresholds the liveness checks hold samples to.
type smokeLimits struct {
	MinFPS     float64 // spawn_trigger frames a second at least
	SeqChanges int     // a pose seq must change at least this often over the window
}

// livenessChecks judges one game's samples, taken secs apart in all:
// the heartbeat advanced at MinFPS, the REL is this build's and runs,
// JUTException caught nothing, and the visuals block's last error is 0.
// label names the game ("A", "B"); every check is named "<what> <label>".
func livenessChecks(label string, ss []gameSample, secs float64, lim smokeLimits) []smokeCheck {
	var out []smokeCheck
	for _, s := range ss {
		if s.Err != "" {
			return []smokeCheck{passIf(false, "read "+label, "a read failed: "+s.Err, nil)}
		}
	}
	if len(ss) < 2 || secs <= 0 {
		return []smokeCheck{passIf(false, "read "+label, fmt.Sprintf("%d samples over %.1f s: too few to judge", len(ss), secs), nil)}
	}
	first, last := ss[0], ss[len(ss)-1]
	frames := last.Heartbeat - first.Heartbeat // u32: wraps as the counter does
	fps := float64(frames) / secs
	monotonic := true
	for i := 1; i < len(ss); i++ {
		if d := ss[i].Heartbeat - ss[i-1].Heartbeat; d == 0 || d > 1<<31 {
			monotonic = false
		}
	}
	out = append(out, passIf(fps >= lim.MinFPS && monotonic, "heartbeat "+label,
		fmt.Sprintf("spawn_trigger %d -> %d: %.1f frames/s (at least %.0f, moving at every sample: %v)",
			first.Heartbeat, last.Heartbeat, fps, lim.MinFPS, monotonic),
		map[string]any{"fps": round2(fps), "from": first.Heartbeat, "to": last.Heartbeat}))

	switch r := last.REL; {
	case !r.Linked:
		out = append(out, passIf(false, "rel "+label, fmt.Sprintf("REL not linked (magic 0x%08X)", r.Magic), nil))
	case r.BuildID != inject.RELBuildID:
		out = append(out, passIf(false, "rel "+label, fmt.Sprintf("REL build 0x%08X, this build's is 0x%08X: repatch and recapture", r.BuildID, inject.RELBuildID), nil))
	default:
		ran := last.REL.Frames != first.REL.Frames
		out = append(out, passIf(ran, "rel "+label,
			fmt.Sprintf("REL 0x%08X linked, actor frames %d -> %d", r.BuildID, first.REL.Frames, last.REL.Frames),
			map[string]any{"build": fmt.Sprintf("0x%08X", r.BuildID)}))
	}

	caught := false
	for _, s := range ss {
		caught = caught || s.Crash.Caught
	}
	detail := "JUTException caught nothing"
	if caught {
		detail = "JUTException caught a fault: run wwdev crash-context on this Dolphin"
	}
	out = append(out, passIf(!caught, "no crash "+label, detail, nil))

	var errs []uint32
	for _, s := range ss {
		if s.VisErr != mailbox.VisErrNone && !containsU32(errs, s.VisErr) {
			errs = append(errs, s.VisErr)
		}
	}
	detail = fmt.Sprintf("visuals block last error 0 (magic 0x%08X)", last.VisMagic)
	if len(errs) > 0 {
		detail = fmt.Sprintf("visuals block last error %v (1 res, 2 create, 3 font; wwvis.h)", errs)
	}
	out = append(out, passIf(len(errs) == 0, "rel last error "+label, detail, nil))
	return out
}

// poseFlowChecks judges one game's pose traffic over ss: its own pose
// is published (pose_publish_seq moves) and Go feeds a remote into one
// of its mini-Link slots (a pose_seqs byte moves, NextSeq skipping 0 on
// wrap, so it is counted as changes, not as a rise) with shadow_mode 5.
func poseFlowChecks(label string, ss []gameSample, lim smokeLimits) []smokeCheck {
	if len(ss) < 2 {
		return []smokeCheck{passIf(false, "pose flow "+label, "too few samples", nil)}
	}
	pub := 0
	var slot [avatar.Cap]int
	for i := 1; i < len(ss); i++ {
		if ss[i].PublishSeq != ss[i-1].PublishSeq {
			pub++
		}
		for s := range slot {
			if ss[i].PoseSeqs[s] != ss[i-1].PoseSeqs[s] && ss[i].PoseSeqs[s] != 0 {
				slot[s]++
			}
		}
	}
	best := 0
	for s := range slot {
		if slot[s] > slot[best] {
			best = s
		}
	}
	last := ss[len(ss)-1]
	return []smokeCheck{
		passIf(pub >= lim.SeqChanges, "pose publish "+label,
			fmt.Sprintf("pose_publish_seq changed %d times in %d samples (at least %d)", pub, len(ss), lim.SeqChanges),
			map[string]any{"changes": pub}),
		passIf(slot[best] >= lim.SeqChanges && last.ShadowMode == avatar.ModePoseFeed, "pose feed "+label,
			fmt.Sprintf("pose_seqs[%d] changed %d times in %d samples (at least %d); shadow_mode %d (want %d)",
				best, slot[best], len(ss), lim.SeqChanges, last.ShadowMode, avatar.ModePoseFeed),
			map[string]any{"slot": best, "changes": slot[best], "shadow_mode": last.ShadowMode}),
	}
}

func containsU32(xs []uint32, v uint32) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// vec is a world position.
type vec [3]float32

func vdist(a, b vec) float64 {
	dx, dy, dz := float64(a[0]-b[0]), float64(a[1]-b[1]), float64(a[2]-b[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func (v vec) String() string { return fmt.Sprintf("(%.1f, %.1f, %.1f)", v[0], v[1], v[2]) }

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// linkPos is m's own Link's position.
func linkPos(m mem.Reader) (vec, error) {
	p, err := player.NewLink(m).Position()
	if err != nil {
		return vec{}, err
	}
	return vec{p.PosX, p.PosY, p.PosZ}, nil
}

// remoteSlotPos finds the actor-puppet slot nearest near: puppet-sync's
// smoothed position of a remote (Feeder.moveActor writes it every tick
// for every remote, with or without a mini-Link; the mini-Link's pose is
// re-applied at it, avatar.ApplyAt). ok is false when no slot holds a
// position.
func remoteSlotPos(m mem.Reader, near vec) (slot int, p vec, ok bool) {
	best := math.Inf(1)
	for i := range feed.MaxPuppets {
		b, err := m.Read(feed.SlotAddr(i, mailbox.PuppetOffPosX), 12)
		if err != nil || len(b) != 12 {
			continue
		}
		v := vec{mem.F32BE(b), mem.F32BE(b[4:]), mem.F32BE(b[8:])}
		if v == (vec{}) || !finite(v) {
			continue
		}
		if d := vdist(v, near); d < best {
			best, slot, p, ok = d, i, v, true
		}
	}
	return slot, p, ok
}

// remotePoseRoot finds the mini-Link slot whose root joint (joint 0's
// translation in its pose_buf, what the REL draws) is nearest near.
func remotePoseRoot(m mem.Reader, near vec) (slot int, p vec, ok bool) {
	best := math.Inf(1)
	for s := range avatar.Cap {
		seq, err := mem.ReadU8(m, mailbox.Mailbox+avatar.OffPoseSeq(s))
		if err != nil || seq == 0 {
			continue
		}
		buf, err := mem.ReadU32(m, mailbox.Mailbox+avatar.OffPoseBufPtr(s))
		if err != nil || !avatar.PoseDstInMEM1(buf, avatar.MtxBytes) {
			continue
		}
		v, err := mtxTrans(m, buf)
		if err != nil {
			continue
		}
		if d := vdist(v, near); d < best {
			best, slot, p, ok = d, s, v, true
		}
	}
	return slot, p, ok
}

// publishedRoot is m's own Link's root joint in its pose publish buffer
// (world space; player.Link.Pose's source).
func publishedRoot(m mem.Reader) (vec, error) {
	pose, err := player.NewLink(m).Pose()
	if err != nil {
		return vec{}, err
	}
	if len(pose) < avatar.MtxBytes {
		return vec{}, fmt.Errorf("no pose published")
	}
	return mtxTransBytes(pose), nil
}

// mtxTrans reads the translation column of the 3x4 Mtx at a.
func mtxTrans(m mem.Reader, a uint32) (vec, error) {
	b, err := m.Read(a, avatar.MtxBytes)
	if err != nil || len(b) != avatar.MtxBytes {
		return vec{}, fmt.Errorf("pose_buf 0x%08X: %v", a, err)
	}
	return mtxTransBytes(b), nil
}

// mtxTransBytes is a row-major 3x4 Mtx's m03, m13, m23 (posemath.go).
func mtxTransBytes(b []byte) vec {
	return vec{mem.F32BE(b[12:]), mem.F32BE(b[28:]), mem.F32BE(b[44:])}
}

func finite(v vec) bool {
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return false
		}
	}
	return true
}

// trackObs is what a tracking check saw: the mover's Link before and
// after its walk (and its published root after), the observer's slot
// position and mini-Link root for it after the settle.
type trackObs struct {
	MoverStart, MoverEnd vec
	MoverRoot            vec
	HaveMoverRoot        bool
	Slot                 int
	SlotPos              vec
	HaveSlot             bool
	PoseSlot             int
	PoseRoot             vec
	HavePose             bool
}

// trackCheck judges a walk: the mover's Link moved at least minMove
// (else the walk proves nothing: a wall, a menu, a cutscene) and, after
// the settle, the observer's smoothed slot position and mini-Link root
// are within tol of where the mover's Link and root are.
func trackCheck(name string, o trackObs, minMove, tol float64) smokeCheck {
	moved := vdist(o.MoverStart, o.MoverEnd)
	v := map[string]any{"moved": round2(moved), "mover": o.MoverEnd.String()}
	switch {
	case moved < minMove:
		return passIf(false, name, fmt.Sprintf("the walking Link moved %.1f units (at least %.0f): the walk proves nothing; is Link free to move (no menu, wall or cutscene)?", moved, minMove), v)
	case !o.HaveSlot:
		return passIf(false, name, "the other game has no puppet slot position for this player", v)
	}
	slotErr := vdist(o.SlotPos, o.MoverEnd)
	v["slot"], v["slot_pos"], v["slot_err"] = o.Slot, o.SlotPos.String(), round2(slotErr)
	ok := slotErr <= tol
	detail := fmt.Sprintf("moved %.1f; the other game's slot %d sits %.1f from it (tolerance %.0f)", moved, o.Slot, slotErr, tol)
	if o.HavePose && o.HaveMoverRoot {
		rootErr := vdist(o.PoseRoot, o.MoverRoot)
		v["pose_slot"], v["root_err"] = o.PoseSlot, round2(rootErr)
		ok = ok && rootErr <= tol
		detail += fmt.Sprintf("; mini-Link %d's root %.1f from the mover's (tolerance %.0f)", o.PoseSlot, rootErr, tol)
	} else {
		ok = false
		detail += "; no mini-Link root to compare (no armed pose slot, or the mover published no pose)"
	}
	return passIf(ok, name, detail, v)
}

// bindChecks judges the co-op tags of the two games' files: each a valid
// tag that passes V1-V5 (bind.Classify with no world: V6 skipped), and
// both for the same world.
func bindChecks(fa, fb bind.File) []smokeCheck {
	var out []smokeCheck
	var worlds [2]bind.WorldID
	for i, f := range []bind.File{fa, fb} {
		label := string(rune('A' + i))
		v := bind.Classify(f, bind.Expect{})
		ok := v.State == bind.Bound
		if ok && v.HasTag {
			worlds[i] = v.Tag.World
		}
		out = append(out, passIf(ok, "coop bind "+label, fmt.Sprintf("file %d: %s (%s)", f.Slot, v.State, v.Why), nil))
	}
	same := worlds[0] != (bind.WorldID{}) && worlds[0] == worlds[1]
	out = append(out, passIf(same, "coop same world", fmt.Sprintf("A's world %s, B's %s", worlds[0], worlds[1]), nil))
	return out
}
