package addr

// The Magtail, mt_class (proc fpcNm_MT_e 0x00D9, ProcMT in remote.go,
// f_pc_name.h:229; size 0x1CC0, g_profile_MT), as the enemy mirror's batch
// C named it and batch D restores it (ww-multiplayer-handoff/design/
// enemy-mirror-batch-c.json "mt", enemy-mirror-batch-d.json R-D6; the C
// copy is inject/include/wwenemy_kinds.h EMK_MT_*, pinned by
// mods/enemysync TestKindTablePins). The decomp has no field layout for
// it: every offset is from an offline disassembly of the retail
// d_a_mt.rel (SHA-1 9d7de8625389ef01cb34539efc782f2a0d0aeda1,
// design/research/C1-magtail.md, C1b-magtail-redo.md, "mt.rel 0xNNNN" a
// .text offset). Batch D's P0-MT2 verified the retail body model live
// (archive/enemy-batch-d/P0.md); MT is the batch's first cut.
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

// What the sampler (mods/enemysync ReadSampleMT) and the probes read
// beyond the row's fields (batch C "mt".stream, batch D G-D6; the widths
// are the retail code's loads and stores).
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

// Batch D's retail-call body step (enemy-mirror-batch-d.json R-D6,
// C1b-magtail-redo.md section 5): the fields the client REL writes into a
// copy around body_control1/2 (EMK_MT_*), and the builders' place in the
// loaded d_a_mt.rel with the fingerprint the REL checks once per adopt
// (rels/d_a_mt/symbols.txt:18,30,46; P0-MT2 read the words live).
const (
	// MtOffWiggle is s16 the head yaw wiggle body_control1 reads, streamed
	// in slot +0x18; MtOffSwayFrame s16 the counter FIGHT's sway reads
	// (the REL adds 1 per frame); MtOffF470 f32 the stretch (the copy's own).
	MtOffWiggle    = 0x468
	MtOffSwayFrame = 0x46A
	MtOffF470      = 0x470
	// MtOffLatch is u8 the engage latch (the REL writes 0 every frame);
	// MtOffBite u8 0 none, 1 wind-up, 2 bite (written 0 before body_control2).
	MtOffLatch = 0xC01
	MtOffBite  = 0xC04
	// MtOffChainPos is cXyz[8], MtOffChainAngle csXyz[8]: the FIGHT/PINNED
	// chain the mode edges copy to and from the ring (mt.rel 0x21D8-0x2218,
	// 0x1404-0x1524).
	MtOffChainPos   = 0x4A0
	MtOffChainAngle = 0x560
	// MtTextExec, MtTextBc1 and MtTextBc2 are .text offsets of daMt_Execute,
	// body_control1 and body_control2: bc = exec - MtTextExec + MtTextBc*.
	// MtBc1Blr and MtBc2Blr are each builder's blr offset.
	MtTextExec = 0x6188
	MtTextBc1  = 0x1F10
	MtTextBc2  = 0xB28
	MtBc1Blr   = 0x3C4
	MtBc2Blr   = 0xBF0
	MtFpBlr    = 0x4E800020
)

// MtFpExec, MtFpBc1 and MtFpBc2 are the first four words of daMt_Execute,
// body_control1 and body_control2 in the retail d_a_mt.rel.
var (
	MtFpExec = [4]uint32{0x9421FCC0, 0x7C0802A6, 0x90010344, 0xDBE10330}
	MtFpBc1  = [4]uint32{0x9421FF90, 0x7C0802A6, 0x90010074, 0x39610070}
	MtFpBc2  = [4]uint32{0x9421FE60, 0x7C0802A6, 0x900101A4, 0xDBE10190}
)
