package addr

// The Kargaroc, bb_class (include/d/actor/d_a_bb.h:13-108; proc fpcNm_BB_e
// 0x00B6, ProcBB in remote.go, f_pc_name.h:191), as the enemy mirror's
// batch C reads it (ww-multiplayer-handoff/design/enemy-mirror-batch-c.json
// "bb", go_changes; the C copy is inject/include/wwenemy_kinds.h EMK_BB_*,
// pinned by mods/enemysync TestKindTablePins). Offsets are from the actor;
// each cites the header line of the field. An fopEn_enemy_c with no
// damagereaction, drawn from current.angle (d_a_bb.cpp:2313-2326).
const (
	// BbClassSize is sizeof(bb_class) (d_a_bb.h:108): enemyfire
	// mEnemyFire 0x1038 + 0x228.
	BbClassSize = 0x1260
	// BbOffMorf is mDoExt_McaMorf* mpMorf (d_a_bb.h:16).
	BbOffMorf = 0x2B4
	// BbOffEyeBtp is u8 unk_2CC, the eye btp frame (d_a_bb.h:18; Draw
	// d_a_bb.cpp:625).
	BbOffEyeBtp = 0x2CC
	// BbOffType is u8 unk_2D8 (d_a_bb.h:23): params & 0xFF at create
	// (d_a_bb.cpp:2689-2737): 3 attacks at once, 4 and 7 perch, 5 and 6
	// carry a Moblin / Bokoblin (riders, refused: batch C D9). The walk's
	// gate byte.
	BbOffType = 0x2D8
	// BbOffState is u8 unk_2DD (d_a_bb.h:28): 3 after a stun recovery
	// (d_a_bb.cpp:2180-2184), what the client's handback writes.
	BbOffState = 0x2DD
	// BbOffRider is u8 unk_2DF (d_a_bb.h:30): nonzero refused.
	BbOffRider = 0x2DF
	// BbOffDrawGate is u8 unk_2F2 (d_a_bb.h:37): params byte 3 + 1 while
	// the bird waits on its switch; Draw and Execute return while it is
	// nonzero (d_a_bb.cpp:611-614, 2242-2249). The sampler's SF_HIDDEN.
	BbOffDrawGate = 0x2F2
	// BbOffTimers is s16 unk_318[6] (d_a_bb.h:45); [1] the attack timer.
	BbOffTimers      = 0x318
	BbOffAttackTimer = BbOffTimers + 2
	// BbOffInvuln is s16 unk_340 (d_a_bb.h:55): damage_check runs only
	// while it is 0 (d_a_bb.cpp:2446).
	BbOffInvuln = 0x340
	// BbOffKnock is f32 unk_348 (d_a_bb.h:58): the knockback a death or a
	// hit starts (45), decayed ~23 frames (d_a_bb.cpp:2269-2294); a slot is
	// refused at listing while it is above 0.01.
	BbOffKnock = 0x348
	// The colliders (d_a_bb.h:77-80): dCcD_Sph mHeadAtSph (the host's own
	// Link only), mHeadTgSph (hit collider 1), mBodyTgSph (hit collider
	// 2), mBodyCoSph.
	BbOffHeadAtSph = 0x5BC
	BbOffHeadTgSph = 0x6E8
	BbOffBodyTgSph = 0x814
	BbOffBodyCoSph = 0x940
	// BbOffDead is u8 unk_C7E (d_a_bb.h:105): mbDead, set at the killing
	// hit (d_a_bb.cpp:2138-2141).
	BbOffDead = 0xC7E
	// BbOffIce is enemyice mEnemyIce (d_a_bb.h:106): shrink timer +0x6
	// (0xC86), freeze +0xE (0xC8E).
	BbOffIce       = 0xC80
	BbOffIceShrink = BbOffIce + EnemyIceOffTimer
	BbOffIceFreeze = BbOffIce + EnemyIceOffFreezeTimer
	// BbOffFire is enemyfire mEnemyFire (d_a_bb.h:107).
	BbOffFire = 0x1038
)

// What the batch C sampler (mods/enemysync ReadSampleBB) and the
// enemy-watch probe read beyond the row's fields (d_a_bb.h:13-108).
const (
	// BbOffM2F0 and BbOffM2F1 are s8 unk_2F0 / unk_2F1 (d_a_bb.h:35-36):
	// the client's handback zeroes them (d_a_bb.cpp:2180-2184).
	BbOffM2F0 = 0x2F0
	BbOffM2F1 = 0x2F1
	// BbOffM34C is f32 unk_34C (d_a_bb.h:59); BbOffM35D s8 unk_35D
	// (d_a_bb.h:65): handback 0.
	BbOffM34C = 0x34C
	BbOffM35D = 0x35D
	// BbOffM366 and BbOffM368 are s16 unk_366 / unk_368 (d_a_bb.h:70-71):
	// added to the matrix's Rx and Ry (d_a_bb.cpp:2318-2319), written only
	// by the demo build's bb_key_move, zeroed by the REL at adopt. The
	// sampler's mid span ends after unk_368 (BbOffMidEnd).
	BbOffM366   = 0x366
	BbOffM368   = 0x368
	BbOffMidEnd = 0x36A
	// BbOffGroundH is f32 mAcch (0x3B8) m_ground_h (d_bg_s_acch.h:235).
	BbOffGroundH = 0x44C
	// BbOffSphsEnd is the end of the four spheres (unk_A6C, d_a_bb.h:81).
	BbOffSphsEnd = 0xA6C
	// The tail (d_a_bb.h:82-86): J3DModel* unk_A84[9] (the 9 tail
	// models), cXyz unk_AA8[10] (positions; [0] = unk_BD4[1] after calc),
	// csXyz unk_B20[10], cXyz unk_B5C[10] (velocities), cXyz unk_BD4[2]
	// (written by the nodeCallBack inside calc; [1] the tail root).
	BbOffTailModels = 0xA84
	BbOffTailPos    = 0xAA8
	BbOffTailRot    = 0xB20
	BbOffTailVel    = 0xB5C
	BbOffTailRoot   = 0xBD4
	BbOffTailRoot1  = BbOffTailRoot + 12
	BbTailLen       = 10
	// BbOffHeadRot is csXyz unk_C00[11] (d_a_bb.h:91), BbOffBeak s16
	// unk_C4E (d_a_bb.h:93): the nodeCallBack's head and beak joints.
	BbOffHeadRot = 0xC00
	BbOffBeak    = 0xC4E
	// BbOffC58 is f32 unk_C58 (d_a_bb.h:97): handback 0.
	BbOffC58 = 0xC58
	// BbOffIceY is f32 mEnemyIce.m028, the matrix's y lift
	// (d_a_bb.cpp:2313).
	BbOffIceY = BbOffIce + 0x28
)

// The Kargaroc's types (params & 0xFF, unk_2D8; create d_a_bb.cpp:2689-
// 2754; batch C "bb".coverage): the keyed perches of M_NewD2 room 3 (set 0
// 0xFFFFFF04, set 2 0xFFFF0507). 5 and 6 are the riders (mods/enemysync
// BbTypeRiderMO2 / BbTypeRiderBK, refused).
const (
	BbTypePerch  = 4
	BbTypePerch2 = 7
)

// The tail fingerprint (batch C "bb".tail, D10): the REL calls the retail
// tail_control at exec - BbTextExec + BbTextTail, exec being the copy's
// mpExecuteFunc (*(*(actor+ActorOffSubMtd)+MethodOffExecute)), only when
// these words match (verified offline on retail d_a_bb.rel SHA-1
// 4271e18f447d7b2d2b052a1ae4e0f0862052269f; tww
// config/GZLE01/rels/d_a_bb/symbols.txt:8,10,11,44). The P0-BB probe
// (enemy-watch --kind bb) prints them live.
const (
	BbTextExec   = 0x69A8 // daBb_Execute
	BbTextTail   = 0x404  // tail_control__FP8bb_class (size 0x5A0)
	BbTailBlrOff = 0x59C  // tc + this: its blr
	BbExecToTail = BbTextExec - BbTextTail
	BbFpBlr      = 0x4E800020
)

// BbFpExec and BbFpTail are the first four words of daBb_Execute and of
// tail_control (EMK_BB_FP_EXEC0..3, EMK_BB_FP_TAIL0..3).
var (
	BbFpExec = [4]uint32{0x9421FF70, 0x7C0802A6, 0x90010094, 0xDBE10080}
	BbFpTail = [4]uint32{0x9421FEB0, 0x7C0802A6, 0x90010154, 0xDBE10140}
)
