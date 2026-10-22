package addr

// The Magtail, mt_class (proc fpcNm_MT_e 0x00D9, ProcMT in remote.go,
// f_pc_name.h:229; size 0x1CC0, g_profile_MT), as the enemy mirror's batch
// C names it (ww-multiplayer-handoff/design/enemy-mirror-batch-c.json "mt";
// the C copy is inject/include/wwenemy_kinds.h EMK_MT_*, pinned by
// mods/enemysync TestKindTablePins). The decomp has no field layout for
// it: every offset is from an offline disassembly of the retail
// d_a_mt.rel (SHA-1 9d7de8625389ef01cb34539efc782f2a0d0aeda1,
// design/research/C1-magtail.md, "mt.rel 0xNNNN" a .text offset) and is
// unverified live until the P0-MT probe; MT is the batch's first cut.
const (
	// MtClassSize is the profile's size (g_profile_MT +0x10).
	MtClassSize = 0x1CC0
	// MtOffMorf is the head's mDoExt_McaMorf*, the first of 8 at +4i;
	// only the head is animated.
	MtOffMorf = 0x2C4
	// MtOffMode is u8 mode (0 MOVE, 1 FIGHT, 2 BALL, 3 DEAD, 6 the pinned
	// mode), MtOffAction u8 action: the walk's gate byte and its neighbour.
	MtOffMode   = 0x454
	MtOffAction = 0x455
	// MtOffAppear is u8: nonzero while the Magtail waits on its appear
	// switch (r16 sw227), refused.
	MtOffAppear = 0x2BB
	// MtOffBallTimer is s16 (mt.rel 0x6F60-0x6F9C reads it with the mode).
	MtOffBallTimer = 0x466
	// MtOffBodySph is the first of 8 body dCcD_Sph (stride MtBodySphStride);
	// MtOffEyeSph the eye's.
	MtOffBodySph    = 0xE48
	MtBodySphStride = 0x12C
	MtOffEyeSph     = 0x17A8
	// MtOffDeath is u8, the death flag (>= 2 bakuha); MtOffKilled u8, the
	// flag daMt_Delete respawns on (mt.rel 0x7CF4-0x7DF8).
	MtOffDeath  = 0x18F8
	MtOffKilled = 0x1CBC
	// MtOffIce is enemyice (C1-magtail.md): shrink timer +0x6 (0x1906);
	// its freeze timer is never written (ice arrows stun).
	MtOffIce       = 0x1900
	MtOffIceShrink = MtOffIce + EnemyIceOffTimer
)

// What the batch C sampler (mods/enemysync ReadSampleMT) and the
// enemy-watch probe read beyond the row's fields ("mt".stream; the
// widths are the retail code's loads and stores).
const (
	// MtOffType is u8 params & 0xFF (0xFF reads 0): type 0 copies carry
	// the fire particles. MtOffRespawn u8 (params >> 15) & 1: set =
	// daMt_Delete re-creates it (mt.rel 0x8514-0x855C, 0x7CF4-0x7DF8).
	// MtOffDeathSwitch u8 mDeathSwitch.
	MtOffType        = 0x2B4
	MtOffRespawn     = 0x2B6
	MtOffDeathSwitch = 0x2B9
	// MtOffColorPhase is u8 (stb/lbz, 0..2); MtOffColorA / MtOffColorB the
	// s32 btk and brk frames daMt_Draw enters (lwz/stw, at most 0x82;
	// mt.rel 0x3464-0x3554, 0x6EFC-0x6F5C). The wire carries their low
	// bytes (aux[2]).
	MtOffColorPhase = 0x2E4
	MtOffColorA     = 0x2E8
	MtOffColorB     = 0x2EC
	// MtOffAnchor is cXyz[8], the segment translations (Execute's tail).
	MtOffAnchor = 0x350
	// MtOffHitCool is s16, the hit cooldown (5 after any hit), MtOffDive
	// s16 the dive timer: the ring index stands while it is nonzero
	// (mt.rel 0x637C-0x645C).
	MtOffHitCool = 0x460
	MtOffDive    = 0x48E
	// The head trail: cXyz[64] at MtOffRingPos, csXyz[64] at
	// MtOffRingAngle, u32 index & 0x3F at MtOffRingIdx (mt.rel 0x8750-0x87A0).
	MtOffRingPos   = 0x6F4
	MtOffRingAngle = 0x9F4
	MtOffRingIdx   = 0xBF4
	MtRingLen      = 64
	// MtSegs is the segment count: morfs at MtOffMorf + 4i, body spheres,
	// anchors.
	MtSegs = 8
	// MtOffStts is dCcD_Stts; MtOffBodyShieldSPrm the head sphere's GObjTg
	// mSPrm (u32, bit 0 Shield while fighting; mt.rel 0x883C, 0x682C-0x68A0).
	MtOffStts           = 0xE0C
	MtOffBodyShieldSPrm = 0xEDC
	MtOffBodySphsEnd    = MtOffBodySph + MtSegs*MtBodySphStride
	// MtOffEyeHP is u8 eye HP (2), MtOffBlur u8 the lunge blur's draw flag.
	MtOffBlur  = 0x18D4
	MtOffEyeHP = 0x18FB
)

// J3DModelOffBaseTRMtx is J3DModel's Mtx mBaseTransformMtx (J3DModel.h:149):
// the 8 Magtail segments' base matrices, whose translations (+0x0C,
// +0x1C, +0x2C) the P0-MT probe compares with the body spheres' centres.
const J3DModelOffBaseTRMtx = 0x24
