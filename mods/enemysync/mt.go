package enemysync

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The Magtail's row helpers (ww-multiplayer-handoff/design/
// enemy-mirror-batch-c.json "mt", go_changes; the row Kinds[KindMT], off
// by default: mt+u, every DRC Magtail being unkeyed). MT is the batch's
// first cut (R-20, CONTRACT.md section 7): everything MT-only on the Go
// side is in this file, plus one arm per dispatch switch, so a cut deletes
// the file and those arms. Its offsets are unverified live until the
// P0-MT probe (enemy-watch --kind mt).

// MtLiveRefusal is the Magtail's LiveOK (the REL's em_mt_gate at
// listing): "" when the live bytes pass, else why not: a mode other than
// MOVE, FIGHT or the pinned mode (BALL is local custody, DEAD dying), the
// appear switch wait (+0x2BB), the death flag (+0x18F8), the killed flag
// (+0x1CBC), the light-arrow shrink (+0x1906) or a carry. An adopt gate
// (liveSticky): a listed Magtail stays listed when it curls (mode 2 is the
// custody signal the client's REL hands back on) and while it dies.
func MtLiveRefusal(g LiveGate) string {
	switch {
	case g.Mode != MtModeMove && g.Mode != MtModeFight && g.Mode != MtModePinned:
		return fmt.Sprintf("mode%d", g.Mode)
	case g.MtAppear != 0:
		return "appear"
	case g.MtDeath != 0:
		return "death"
	case g.MtKilled != 0:
		return "killed"
	case g.MtIce != 0:
		return "ice"
	case g.Carried:
		return "carried"
	}
	return ""
}

// readMtGate reads a Magtail's LiveGate bytes beyond Mode and Carried:
// u8 +0x2BB, s8 +0x18F8, s8 +0x1906 and u8 +0x1CBC.
func readMtGate(m mem.Reader, a *actors.Actor, g *LiveGate) error {
	var err error
	u8 := func(off uint32) uint8 {
		if err != nil {
			return 0
		}
		var v uint8
		v, err = mem.ReadU8(m, a.Addr+off)
		return v
	}
	g.MtAppear = u8(addr.MtOffAppear)
	g.MtDeath = u8(addr.MtOffDeath)
	g.MtIce = u8(addr.MtOffIceShrink)
	g.MtKilled = u8(addr.MtOffKilled)
	return err
}

// MtFields is what ReadSampleMT reads beyond the Slot fields: what P0-MT
// checks live (batch C test_plan) and the enemy-watch probe prints.
type MtFields struct {
	// Type is +0x2B4 (type 0 carries the fire), Respawn +0x2B6, Appear
	// +0x2BB (HIDDEN), Mode +0x454 (also Mode), Action +0x455 (also
	// Action), Phase +0x2E4 (the colour phase), Death +0x18F8, EyeHP
	// +0x18FB, Killed +0x1CBC, Blur +0x18D4.
	Type, Respawn, Appear, Mode, Action, Phase, EyeHP, Killed, Blur uint8
	Death                                                           int8
	// ColorA and ColorB are the s32 btk / brk frames (+0x2E8, +0x2EC).
	ColorA, ColorB int32
	// BallTimer is +0x466, Dive +0x48E (the ring stands while nonzero),
	// HitCool +0x460.
	BallTimer, Dive, HitCool int16
	// Shield is the head sphere's GObjTg mSPrm (+0xEDC; bit 0 Shield).
	Shield uint32
	// Morfs is the 8 segments' mDoExt_McaMorf* (+0x2C4 + 4i), Anchor the
	// segment translations (+0x350[8]), EyePos eyePos (+0x260).
	Morfs  [addr.MtSegs]uint32
	Anchor [addr.MtSegs][3]float32
	EyePos [3]float32
}

// The Magtail's spans (ReadSampleMT).
const (
	mtReadMidOff  = addr.MtOffType                           // +0x2B4 .. the dive timer
	mtReadMidLen  = addr.MtOffDive + 2 - addr.MtOffType      // 0x1DC
	mtReadCollOff = addr.MtOffBodySph                        // body sph[0..7], the eye
	mtReadCollLen = addr.MtOffBlur - addr.MtOffBodySph       // 0xA8C
	mtReadTailOff = addr.MtOffBlur                           // +0x18D4 .. the class end
	mtReadTailLen = addr.MtClassSize - addr.MtOffBlur        // 0x3EC: blur, death, eye HP, the enemyice, killed
	mtShieldAt    = addr.MtOffBodyShieldSPrm - mtReadCollOff // in the collider span
	// MtReadsPerSample is how many Reads ReadSampleMT makes (four when the
	// morf is not in MEM1).
	MtReadsPerSample = 5
)

// mtColliders is the Magtail's spheres: the 8 body spheres, then the eye.
var mtColliders = func() []colliderSpec {
	var out []colliderSpec
	for i := range uint32(addr.MtSegs) {
		out = append(out, colliderSpec{fmt.Sprintf("body%d", i), addr.MtOffBodySph + i*addr.MtBodySphStride, false})
	}
	return append(out, colliderSpec{"eye", addr.MtOffEyeSph, false})
}()

// ReadSampleMT reads a Magtail's tick in five Reads (batch C "mt".stream):
// R1 the actor (shape_angle, eyePos), R2 +0x2B4..+0x490 (type .. the dive
// timer: the morfs, the colour phase and frames, the anchors, mode,
// action, the timers), R3 the 9 spheres (+0xE48..+0x18D4), R4 +0x18D4..
// the class end (the blur, death flag, eye HP, the enemyice and the
// killed flag), R5 the head morf. AngleY is shape_angle.y (the head yaw),
// Tilt shape_angle.x; Aux {mode | action << 8, the ball timer, the btk |
// brk frames' low bytes << 8, shape_angle.z}. Mode is the mode, Action
// the action. Flags: DYING mode DEAD, the death flag >= 2 (bakuha) or the
// light shrink; HIDDEN +0x2BB. flags2: the colour phase (bits 0-1), the
// head sphere's Shield, the dive timer, ATTN. Mode BALL is the custody
// signal (the client REL hands its copy back, MT-4). Never dormant.
func ReadSampleMT(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindMT, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcMT)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcMT
	allowActorFields(&s, b)
	be := binary.BigEndian
	mt := &s.Mt
	for i := range 3 {
		mt.EyePos[i] = mem.F32BE(b[addr.ActorOffEyePos+4*i:])
	}
	s.Tilt = s.St.ShapeAngle[0]

	d, err := readExact(m, e.Addr+mtReadMidOff, mtReadMidLen, "Magtail type..dive timer")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-mtReadMidOff:] }
	for i := range uint32(addr.MtSegs) {
		mt.Morfs[i] = be.Uint32(mid(addr.MtOffMorf + 4*i))
		for k := range uint32(3) {
			mt.Anchor[i][k] = mem.F32BE(mid(addr.MtOffAnchor + 12*i + 4*k))
		}
	}
	mt.Type = mid(addr.MtOffType)[0]
	mt.Respawn = mid(addr.MtOffRespawn)[0]
	mt.Appear = mid(addr.MtOffAppear)[0]
	mt.Phase = mid(addr.MtOffColorPhase)[0]
	mt.ColorA = int32(be.Uint32(mid(addr.MtOffColorA)))
	mt.ColorB = int32(be.Uint32(mid(addr.MtOffColorB)))
	mt.Mode = mid(addr.MtOffMode)[0]
	mt.Action = mid(addr.MtOffAction)[0]
	mt.HitCool = int16(be.Uint16(mid(addr.MtOffHitCool)))
	mt.BallTimer = int16(be.Uint16(mid(addr.MtOffBallTimer)))
	mt.Dive = int16(be.Uint16(mid(addr.MtOffDive)))
	s.Mode, s.Action = int16(mt.Mode), int16(mt.Action)
	s.Aux = [4]int16{int16(uint16(mt.Mode) | uint16(mt.Action)<<8), mt.BallTimer,
		int16(uint16(uint8(mt.ColorA)) | uint16(uint8(mt.ColorB))<<8), s.St.ShapeAngle[2]}

	coll, err := readExact(m, e.Addr+mtReadCollOff, mtReadCollLen, "Magtail spheres")
	if err != nil {
		return s, err
	}
	st := &s.St
	st.Colliders = parseColliders(coll, mtReadCollOff, mtColliders)
	mt.Shield = be.Uint32(coll[mtShieldAt:])

	tail, err := readExact(m, e.Addr+mtReadTailOff, mtReadTailLen, "Magtail blur..class end")
	if err != nil {
		return s, err
	}
	t := func(off uint32) []byte { return tail[off-mtReadTailOff:] }
	mt.Blur = t(addr.MtOffBlur)[0]
	mt.Death = int8(t(addr.MtOffDeath)[0])
	mt.EyeHP = t(addr.MtOffEyeHP)[0]
	mt.Killed = t(addr.MtOffKilled)[0]
	s.IceTimer = int8(t(addr.MtOffIceShrink)[0])

	if err := readMorfAnim(m, mt.Morfs[0], &s.Sample); err != nil {
		return s, err
	}

	if mt.Mode == MtModeDead || mt.Death >= 2 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if mt.Appear != 0 {
		s.Flags |= SlotHidden
		s.Hidden = true
	}
	s.Flags2 |= mt.Phase & MtF2Phase
	if mt.Shield&1 != 0 {
		s.Flags2 |= MtF2Shield
	}
	if mt.Dive != 0 {
		s.Flags2 |= MtF2Dive
	}
	if s.Attn != 0 {
		s.Flags2 |= MtF2Attn
	}
	st.Dormant = false
	return s, allowFinish(&s, e, "Magtail")
}

// mtKillHit reports a client custody death of a Magtail (MT-4: the REL's
// collider code HitMtKill): no collider and no hit object, its position
// the copy's own (readHit).
func mtKillHit(kind KindID, collider uint8) bool {
	return kind == KindMT && collider == HitMtKill
}

// mtHitCollider is the offset of the body sphere a Magtail's collider-2
// hit landed on (the first of the 8 whose Tg hit object is set; the REL's
// em_hits MT case reports any body sphere as collider 2 and withholds the
// rest while one is pending), body sph[0] when none is.
func mtHitCollider(m mem.Reader, actor uint32) uint32 {
	for i := range uint32(addr.MtSegs) {
		off := addr.MtOffBodySph + i*addr.MtBodySphStride
		if obj, err := mem.ReadU32(m, actor+off+addr.Mo2CcTgHitObjOff); err == nil && addr.InMEM1(obj) {
			return off
		}
	}
	return addr.MtOffBodySph
}

// mtWas is the client match walk's was (classifyWalk): this game's listed
// Magtails, so one stays listed when it curls. That BALL is the custody
// copy the REL hands back (MT-4, em_mt_handback); refused, it would leave
// c.local, the selection would free its block slot, and the REL's custody
// watch (em_mt_custody_watch, which needs the slot to keep naming the pid)
// would never report its death (HitMtKill). Only Magtails: the client's
// other allow kinds keep refusing on every walk, as before batch C.
func mtWas(local map[Ident]localEnemy) []Enemy {
	var was []Enemy
	for _, l := range local {
		if l.Kind == KindMT {
			was = append(was, l.Enemy)
		}
	}
	return was
}
