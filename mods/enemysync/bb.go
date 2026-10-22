package enemysync

import (
	"encoding/binary"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The Kargaroc's row helpers (ww-multiplayer-handoff/design/
// enemy-mirror-batch-c.json "bb", go_changes; the row Kinds[KindBB], off
// by default: bb keyed, bb+u the unkeyed sea birds): its live gate, gate
// read and sampler, as tn.go is the Darknut's. The REL's side is
// em_bb_gate, the TRYXZ matrix, the tail step and the handback
// (rel_changes R-10..R-18).

// BbKnockMax is the knockback (f32 unk_348) above which a Kargaroc is
// refused at listing (batch C go_changes BB sampler; the REL's em_bb_gate
// refuses the same at adopt): a hit or a death starts 45 and it decays
// over ~23 frames (d_a_bb.cpp:2269-2294).
const BbKnockMax = 0.01

// BbLiveRefusal is the Kargaroc's LiveOK (batch C go_changes BB sampler,
// the REL's em_bb_gate at listing): "" when the live bytes pass, else
// why not: a rider's type (5 carries a Moblin, 6 a Bokoblin: D9), unk_2DF
// (the rider byte) set, or a knockback above BbKnockMax. The gate is an
// adopt gate (liveSticky): a listed bird keeps its slot through a
// knockback and its death, which the sampler streams (SlotDying).
func BbLiveRefusal(g LiveGate) string {
	switch {
	case g.Mode == BbTypeRiderMO2 || g.Mode == BbTypeRiderBK:
		return "rider"
	case g.BbRider != 0:
		return "m2df"
	case !(g.BbKnock <= BbKnockMax): // NaN refused too
		return "knock"
	}
	return ""
}

// The Kargaroc's gate span: unk_2DF .. unk_348 (one Read).
const (
	bbGateOff = addr.BbOffRider
	bbGateLen = addr.BbOffKnock + 4 - addr.BbOffRider
)

// readBbGate reads a Kargaroc's LiveGate bytes beyond Mode (its type):
// u8 unk_2DF and f32 unk_348, in one Read; false when it came back short.
func readBbGate(m mem.Reader, a *actors.Actor, g *LiveGate) (bool, error) {
	b, err := m.Read(a.Addr+bbGateOff, bbGateLen)
	if err != nil || len(b) != bbGateLen {
		return false, err
	}
	g.BbRider = b[0]
	g.BbKnock = math.Float32frombits(binary.BigEndian.Uint32(b[addr.BbOffKnock-bbGateOff:]))
	return true, nil
}

// BbFields is what ReadSampleBB reads beyond the Slot fields: what P0-BB
// checks live (batch C test_plan) and the enemy-watch probe prints.
type BbFields struct {
	// Type is unk_2D8 (also Mode), State unk_2DD (also Action), Rider
	// unk_2DF, DrawGate unk_2F2 (params byte 3 + 1 while it waits on a
	// switch: HIDDEN), EyeBtp unk_2CC, Dead unk_C7E (mbDead).
	Type, State, Rider, DrawGate, EyeBtp, Dead uint8
	// AttackTimer is unk_318[1], Invuln unk_340 (INVULN), M366/M368 the
	// matrix's extra Rx/Ry (zeroed by the REL at adopt).
	AttackTimer, Invuln, M366, M368 int16
	// Knock is unk_348 (the knockback).
	Knock float32
	// SubMtd is the actor's actor_method_class* (+0xEC): the P0-BB probe
	// reads its execute (+8) and the tail fingerprint below it.
	SubMtd uint32
}

// The Kargaroc's spans (ReadSampleBB).
const (
	bbReadMidOff  = addr.BbOffMorf                          // mpMorf .. unk_368
	bbReadMidLen  = addr.BbOffMidEnd - addr.BbOffMorf       // 0xB6
	bbReadCollOff = addr.BbOffHeadAtSph                     // the four spheres
	bbReadCollLen = addr.BbOffSphsEnd - addr.BbOffHeadAtSph // 0x4B0
	bbReadTailOff = addr.BbOffDead                          // unk_C7E .. the enemyice's timers
	bbReadTailLen = addr.BbOffIce + addr.EnemyIceSize - addr.BbOffDead
	// BbReadsPerSample is how many Reads ReadSampleBB makes (four when the
	// morf is not in MEM1).
	BbReadsPerSample = 5
)

// bbColliders is the Kargaroc's spheres in header order (d_a_bb.h:77-80).
var bbColliders = []colliderSpec{{"at", addr.BbOffHeadAtSph, false}, {"head", addr.BbOffHeadTgSph, false},
	{"tg", addr.BbOffBodyTgSph, false}, {"co", addr.BbOffBodyCoSph, false}}

// ReadSampleBB reads a Kargaroc's tick in five Reads (batch C go_changes
// BB sampler): R1 the actor (current.angle, the method table pointer),
// R2 +0x2B4..+0x36A (mpMorf .. unk_368), R3 the four spheres
// (+0x5BC..+0xA6C), R4 +0xC7E..+0xC90 (mbDead and the enemyice's
// timers), R5 the morf. AngleY is current.angle.y (the row's AngleOff:
// BB draws from current.angle and never writes shape_angle,
// d_a_bb.cpp:2313-2326), Tilt current.angle.x and TiltZ current.angle.z,
// which wireSlot puts in the slot's s16 at +0x18 (Slot.SetCcM316Z, the
// REL's em_consume_owned BB case); Aux is unused. Mode is the type
// unk_2D8, Action the state unk_2DD. Flags: DYING health <= 0, mbDead
// (unk_C7E) or the ice shrink timer; INVULN unk_340; HIDDEN unk_2F2 (the
// switch wait, d_a_bb.cpp:611-614, 2242-2249). flags2: ATTN. A Kargaroc
// is never dormant.
func ReadSampleBB(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindBB, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcBB)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcBB
	allowActorFields(&s, b)
	be := binary.BigEndian
	bb := &s.Bb
	bb.SubMtd = be.Uint32(b[addr.ActorOffSubMtd:])
	s.Tilt, s.TiltZ = s.St.CurAngle[0], s.St.CurAngle[2]

	d, err := readExact(m, e.Addr+bbReadMidOff, bbReadMidLen, "Kargaroc mpMorf..unk_368")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-bbReadMidOff:] }
	morf := be.Uint32(mid(addr.BbOffMorf))
	bb.EyeBtp = mid(addr.BbOffEyeBtp)[0]
	bb.Type = mid(addr.BbOffType)[0]
	bb.State = mid(addr.BbOffState)[0]
	bb.Rider = mid(addr.BbOffRider)[0]
	bb.DrawGate = mid(addr.BbOffDrawGate)[0]
	bb.AttackTimer = int16(be.Uint16(mid(addr.BbOffAttackTimer)))
	bb.Invuln = int16(be.Uint16(mid(addr.BbOffInvuln)))
	bb.Knock = math.Float32frombits(be.Uint32(mid(addr.BbOffKnock)))
	bb.M366 = int16(be.Uint16(mid(addr.BbOffM366)))
	bb.M368 = int16(be.Uint16(mid(addr.BbOffM368)))
	s.Mode, s.Action = int16(bb.Type), int16(bb.State)

	st := &s.St
	if st.Colliders, err = readColliders(m, e.Addr, bbReadCollOff, bbReadCollLen, bbColliders); err != nil {
		return s, err
	}

	tail, err := readExact(m, e.Addr+bbReadTailOff, bbReadTailLen, "Kargaroc unk_C7E..enemyice")
	if err != nil {
		return s, err
	}
	t := func(off uint32) []byte { return tail[off-bbReadTailOff:] }
	bb.Dead = t(addr.BbOffDead)[0]
	s.IceTimer = int8(t(addr.BbOffIceShrink)[0])
	s.IceFreeze = int16(be.Uint16(t(addr.BbOffIceFreeze)))

	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if s.Health <= 0 || bb.Dead != 0 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if bb.Invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if bb.DrawGate != 0 {
		s.Flags |= SlotHidden
		s.Hidden = true
	}
	if s.Attn != 0 {
		s.Flags2 |= BbF2Attn
	}
	st.Dormant = false
	return s, allowFinish(&s, e, "Kargaroc")
}
