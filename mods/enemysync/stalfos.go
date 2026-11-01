package enemysync

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The Stalfos's row helpers (wind#27, docs/design/enemy-rows-next.md "Enemy
// mirror row: ST Stalfos"; the row Kinds[KindST], off by default: st keyed,
// st+u unkeyed), intact phase only: its flags2 bits, BCK names, live gate,
// sampler and hit collider scan, as tn.go is the Darknut's. The REL's side
// is em_st_gate, the EMK_MTX_PLAIN ST case, em_st_attach, EMK_COLL_ST and
// the ST handback (inject/rel/enemy_mirror.c). Named stalfos.go and
// ReadSampleStalfos because KindSample.St is the allow kinds' field.

// The Stalfos's flags2 bits (wwenemy_kinds.h EMK_ST_F2_*, pinned by
// TestKindTablePins). KindF2Light stays generic.
const (
	StF2HasWeapon uint8 = 1 << 0 // m1DD0 == 10, the mace carried (== mailbox.EnemySF2HasWeapon)
	StF2Attn      uint8 = 1 << 3 // attention_info.flags != 0
)

// stBckNames is St.arc's BCKs, index StBckFirst+i
// (assets/GZLE01/res/Object/St.h).
var stBckNames = []string{
	"ATTACKL1", "ATTACKL1_END", "ATTACKL2", "ATTACKLEND", "ATTACKL_END", "ATTACKR1", "ATTACKR1_END", "ATTACKR2",
	"ATTACKREND", "ATTACKR_END", "FUKKI_WAIT", "HEADB_DAMAGE", "HEADB_DEAD", "HEADB_JUMP1", "HEADB_JUMP2", "HEADB_WAIT",
	"HONE_ATTACK", "HYUNHYUN", "KAITENL1", "KAITENL1_END", "KAITENL2", "KAITENL2_END", "KAITENR1", "KAITENR1_END",
	"KAITENR2", "KAITENR2_END", "KICK1", "KICK2", "KYORO", "LWAIT", "LWALK", "MAHI2", "MAHIL", "MAHIR", "NUKU1", "NUKU2",
	"OTOTOL", "OTOTOR", "SITA_WALK", "START", "TATE_START", "TUKERU1", "TUKERU2", "UE_JUMP1", "UE_JUMP2", "UE_WAIT",
	"UE_WALK", "WAIT", "WALK", "YOKO_START",
}

// StParamsOK is the Stalfos's params gate (design §3 "Refuse in
// StaticOK"): behaviour params & 0xF not 14, the split's upper half
// (BEHAVIOR_UPPER_BODY_ONLY, spawned unkeyed by the split, d_a_st.cpp:1681).
func StParamsOK(params uint32) bool { return params&0xF != addr.StBehaviorUpper }

// StLiveRefusal is the Stalfos's LiveOK: "" when the live bytes pass, else
// why not: BOSS status (a death switch 1..0x7F, d_a_st.cpp:2838-2841: the
// M_DaiMB trio; wwapi/tables/enemies deathSwitchBoss is the same rule on
// the disc's placements) or behaviour 14 (the live mBehaviorType, Mode).
// An adopt gate (liveSticky): a listed Stalfos keeps its slot through the
// break, the split and its death, which the sampler streams as SlotHidden
// and SlotDying; BOSS refuses it on every walk all the same
// (stickyRefused).
func StLiveRefusal(g LiveGate) string {
	switch {
	case g.Boss:
		return "boss"
	case g.Mode == addr.StBehaviorUpper:
		return "upper"
	}
	return ""
}

// StalfosFields is what ReadSampleStalfos reads beyond the Slot fields:
// the design's sampler terms and what the enemy-watch probe prints.
type StalfosFields struct {
	// Behavior is mBehaviorType (also Mode), DeathSwitch mDeathSwitch,
	// DrawGate m02B9 (the ambush switch + 1 until the rise), M02C4 the
	// action's step, Action mActionState (also Action).
	Behavior, DeathSwitch, DrawGate uint8
	M02C4, Action                   int16
	// Invuln is m02F6 (INVULN), M02FA the self-delete, M0304 the spin yaw
	// (aux[0]), HairTimer mTimers[2].
	Invuln, M0304, HairTimer int16
	M02FA                    int8
	// PartsLeft is m0ED2 (0x19 whole), HeadState mParts[15].mPartState (8
	// the dying head), Nunchaku m1DC8, HeadOff m1DD8, Mace m1DD0 (10
	// carried), Shake m1DE4, BodyHP m1E84, DeathWait mCountdownToDeath.
	PartsLeft, HeadState, Nunchaku, HeadOff, Mace, Shake, BodyHP, DeathWait int8
	// BokoPID is mHeldWeaponEntityId (the mace), UpperPID
	// mUpperBodyEntityId (addr.StUpperNone none), Morf mpMorf.
	BokoPID, UpperPID, Morf uint32
}

// The Stalfos's spans (ReadSampleStalfos).
const (
	stReadMidOff     = addr.StOffBehavior                     // mBehaviorType .. m0308
	stReadMidLen     = 0x30C - addr.StOffBehavior             // 0x58
	stReadPartOff    = addr.StOffPartsLeft - 2                // m0ED0 .. the head part's mPartPos
	stReadPartLen    = addr.StOffHeadPos + 12 - stReadPartOff // 0x66C
	stReadTailOff    = addr.StOffNunchaku                     // m1DC8 .. the enemyice's timers
	stReadTailLen    = addr.StOffIce + addr.EnemyIceSize - stReadTailOff
	StReadsPerSample = 5 // four when the morf is not in MEM1
)

// stIntact reports the design's adoptable or intact actions (jyunkai,
// fight, the two ambush waits, and head_damage 0x1F, a head spin on a
// whole body, d_a_st.cpp:1250-1302).
func stIntact(action int16) bool {
	switch action {
	case addr.StActJyunkai, addr.StActFight, addr.StActUnderground, addr.StActCoffin, addr.StActHeadDamage:
		return true
	}
	return false
}

// ReadSampleStalfos reads a Stalfos's tick in five Reads (design §3): R1
// the actor (shape_angle.y is AngleY, the row's AngleOff), R2
// +0x2B4..+0x30C (mBehaviorType .. m0308: m02B9, mpMorf, m02C4,
// mActionState, mTimers, m02F6, m02FA, m0304), R3 +0xED0..+0x153C (m0ED0
// .. m0ED3 and the head part's state and position), R4 +0x1DC8 through the
// enemyice's timers (m1DC8, the mace pid and m1DD0, mUpperBodyEntityId,
// m1DD8, m1DE4, m1E84, mCountdownToDeath, the ice timers), R5 the morf.
// Mode is mBehaviorType, Action mActionState; Aux {m0304, 0, 0, 0} (the
// REL adds aux[0] raw to the smoothed shape.y; the m1DE4 hit shake,
// aux[3], is not built: cut_order 1). Flags: DYING mCountdownToDeath, the
// head part dying (state 8), m02FA or the ice shrink (never health: it is
// the detached head's, 16 while whole); INVULN m02F6; HIDDEN the "not
// intact" test (m02B9, an action not in stIntact, m0ED2 != 0x19, m1DC8,
// m1DD8, an upper body, or the mace not carried). flags2: HAS_WEAPON (m1DD0
// 10), ATTN (never while HIDDEN). Dormant: an ambush wait (0xF/0x10)
// before its trigger (m02C4 0, d_a_st.cpp:1045-1062, 1099-1116).
func ReadSampleStalfos(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindST, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcST)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcST
	allowActorFields(&s, b)
	be := binary.BigEndian
	st := &s.Stalfos

	d, err := readExact(m, e.Addr+stReadMidOff, stReadMidLen, "Stalfos mBehaviorType..m0308")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-stReadMidOff:] }
	st.Behavior = mid(addr.StOffBehavior)[0]
	st.DeathSwitch = mid(addr.StOffDeathSwitch)[0]
	st.DrawGate = mid(addr.StOffDrawGate)[0]
	st.Morf = be.Uint32(mid(addr.StOffMorf))
	st.M02C4 = int16(be.Uint16(mid(addr.StOffM02C4)))
	st.Action = int16(be.Uint16(mid(addr.StOffAction)))
	st.HairTimer = int16(be.Uint16(mid(addr.StOffHairTimer)))
	st.Invuln = int16(be.Uint16(mid(addr.StOffInvuln)))
	st.M02FA = int8(mid(addr.StOffM02FA)[0])
	st.M0304 = int16(be.Uint16(mid(addr.StOffM0304)))
	s.Mode, s.Action = int16(st.Behavior), st.Action
	s.Aux = [4]int16{st.M0304, 0, 0, 0}

	p, err := readExact(m, e.Addr+stReadPartOff, stReadPartLen, "Stalfos m0ED0..head part")
	if err != nil {
		return s, err
	}
	st.PartsLeft = int8(p[addr.StOffPartsLeft-stReadPartOff])
	st.HeadState = int8(p[addr.StOffHeadState-stReadPartOff])

	tail, err := readExact(m, e.Addr+stReadTailOff, stReadTailLen, "Stalfos m1DC8..enemyice")
	if err != nil {
		return s, err
	}
	t := func(off uint32) []byte { return tail[off-stReadTailOff:] }
	st.Nunchaku = int8(t(addr.StOffNunchaku)[0])
	st.BokoPID = be.Uint32(t(addr.StOffBokoPID))
	st.Mace = int8(t(addr.StOffMace)[0])
	st.UpperPID = be.Uint32(t(addr.StOffUpperPID))
	st.HeadOff = int8(t(addr.StOffHeadOff)[0])
	st.Shake = int8(t(addr.StOffShake)[0])
	st.BodyHP = int8(t(addr.StOffBodyHP)[0])
	st.DeathWait = int8(t(addr.StOffDeathWait)[0])
	s.IceTimer = int8(t(addr.StOffIceShrink)[0])
	s.IceFreeze = int16(be.Uint16(t(addr.StOffIceFreeze)))

	if err := readMorfAnim(m, st.Morf, &s.Sample); err != nil {
		return s, err
	}

	if st.DeathWait != 0 || st.HeadState == addr.StHeadStateDead || st.M02FA != 0 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if st.Invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if st.DrawGate != 0 || !stIntact(st.Action) || st.PartsLeft != addr.StPartsWhole || st.Nunchaku != 0 ||
		st.HeadOff != 0 || st.UpperPID != addr.StUpperNone || st.Mace != addr.StMaceCarried {
		s.Flags |= SlotHidden
		s.Hidden = true
	}
	if st.Mace == addr.StMaceCarried {
		s.Flags2 |= StF2HasWeapon
	}
	// A hidden copy is never a Z-target (the REL writes LOCKON from this
	// bit): the host may keep its attention flags through the break.
	if s.Attn != 0 && !s.Hidden {
		s.Flags2 |= StF2Attn
	}
	s.St.Dormant = (st.Action == addr.StActUnderground || st.Action == addr.StActCoffin) && st.M02C4 == 0
	return s, allowFinish(&s, e, "Stalfos")
}

// stHitCollider is the offset of the body sphere a Stalfos's collider-2
// hit landed on (the first of spheres 1..6 whose Tg hit object is set: the
// REL's em_st_hit_collider keeps that one's record and clears the others),
// sphere 1 when none is.
func stHitCollider(m mem.Reader, actor uint32) uint32 {
	for i := uint32(1); i < addr.StSphs; i++ {
		off := addr.StOffSph + i*addr.StSphStride
		if obj, err := mem.ReadU32(m, actor+off+addr.Mo2CcTgHitObjOff); err == nil && addr.InMEM1(obj) {
			return off
		}
	}
	return addr.StOffSph + addr.StSphStride
}
