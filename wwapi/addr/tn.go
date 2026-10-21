package addr

// The Darknut, tn_class (include/d/actor/d_a_tn.h:29-165; proc fpcNm_TN_e
// 0x00C0, ProcTN in remote.go, f_pc_name.h:201), as the enemy mirror's
// TN batch reads it on the host (ww-multiplayer-handoff/design/
// enemy-mirror-tn.json, rel_changes R-1 and go_changes). Offsets are from
// the actor (GZLE: no m0414_demo, and m1520 exists, d_a_tn.h:69-71,
// 158-161); each cites the header line of the field. Batch B0 only reads
// them (enemy-watch --kind tn, enemy-match --dry); nothing writes a
// Darknut yet.
const (
	// TnClassSize is sizeof(tn_class) (d_a_tn.h:165): mpJntHit 0x1B04
	// (:164), the last member, + 4.
	TnClassSize = 0x1B08
	// TnOffBehavior is u8 mBehaviorType (d_a_tn.h:33): params & 0xF at
	// create, which rewrites 12, 13 and 14 to 0 (d_a_tn.cpp:4207,
	// 4244-4262); 15 is the frozen-in-time statue. The walk's gate byte.
	TnOffBehavior = 0x2BC
	// TnOffDeathSwitch is u8 mDisableSpawnOnDeathSwitch (d_a_tn.h:37):
	// current.angle.z at create, 0xFF read as 0; 1..0x7F also sets
	// fopAcStts_BOSS_e (d_a_tn.cpp:4212-4215, 4237-4239).
	TnOffDeathSwitch = 0x2C0
	// TnOffM02C1 is u8 m02C1 (d_a_tn.h:38): the spawn switch + 1 while
	// the Darknut waits on it (d_a_tn.cpp:4216-4218); nonzero hides it
	// (Draw, d_a_tn.cpp:437-439).
	TnOffM02C1 = 0x2C1
	// The three morfs (d_a_tn.h:40-42): mpBodyMorf (the gate's MORF),
	// mpShieldMorf, mpArmorMorf.
	TnOffMorf       = 0x2C4
	TnOffShieldMorf = 0x2C8
	TnOffArmorMorf  = 0x2CC
	// TnOffPieces is s8 mRemainingEquipmentPieces (d_a_tn.h:43): the
	// TnPiece* bits; create sets 0x0B, or 0x0F with a shield
	// (d_a_tn.cpp:4232-4236), and a hit clears them (:2985, 3193-3205).
	TnOffPieces = 0x2D0
	// TnOffParts is tn_p mParts[3] (d_a_tn.h:50; tn_p d_a_tn.h:12-27,
	// 0x4C each): J3DModel* mpPartModel +0, s8 m08 +8 (< 0: part_draw
	// skips it), s8 m09 +9.
	TnOffParts      = 0x2E0
	TnPartStride    = 0x4C
	TnPartOffModel  = 0x00
	TnPartOffM08    = 0x08
	TnPartOffM09    = 0x09
	TnOffPartsEnd   = TnOffParts + 3*TnPartStride
	TnOffArmorColor = 0x3C4 // u8 mArmorColorIndex (d_a_tn.h:51)
	// TnOffEquip is u8 mEquipmentType (d_a_tn.h:52): (current.angle.x >>
	// 5) & 7 at create, at most 5 (d_a_tn.cpp:4224-4231); >= 4 wears the
	// cape (TnEquipCape).
	TnOffEquip = 0x3C5
	// TnOffCountDown is s16 mCountDownTimers[5] (d_a_tn.h:56).
	TnOffCountDown = 0x3E0
	// TnOffPlaySkip is s16 m03EE (d_a_tn.h:59): the morfs play only while
	// it is 0; TnOffInvuln s16 m03F0 (d_a_tn.h:60): damage_check needs it
	// 0, a hit sets 5 (d_a_tn.cpp:2049).
	TnOffPlaySkip = 0x3EE
	TnOffInvuln   = 0x3F0
	// TnOffTargetAngle is s16 mTargetAngle (d_a_tn.h:73).
	TnOffTargetAngle = 0x416
	// TnOffDR is damagereaction mDamageReaction (d_a_tn.h:84;
	// c_damagereaction.h:71-193, DamageReactionSize): s16 mMode +4 (:80),
	// s16 mAction +6 (:81), csXyz m088[20] +0x88, csXyz m48C +0x48C (:124),
	// s16 mInvincibleTimer +0x4CE (:151).
	TnOffDR           = 0x474
	TnOffDRMode       = TnOffDR + 0x4   // s16 dr.mMode
	TnOffDRAction     = TnOffDR + 0x6   // s16 dr.mAction (the Action enum, d_a_tn.cpp:25-42)
	TnOffDRM088       = TnOffDR + 0x88  // csXyz dr.m088[20], 0x78 bytes
	TnDRM088Len       = 0x78            // 20 csXyz
	TnOffTilt         = TnOffDR + 0x48C // csXyz dr.m48C.x
	TnOffTiltZ        = TnOffTilt + 4   // dr.m48C.z
	TnOffDRInvincible = TnOffDR + 0x4CE // s16 dr.mInvincibleTimer
	// TnOffWeapon is int m0C34 (d_a_tn.h:86): nonzero while the Darknut
	// holds its sword (d_a_tn.cpp:151, 478, 1810).
	TnOffWeapon = 0xC34
	// The colliders (d_a_tn.h:94-99): dCcD_Cyl mCoCyl, dCcD_Cyl mTgCyl,
	// dCcD_Sph mHeadSph, dCcD_Sph mDefenceSph, dCcD_Sph mWeponSph,
	// dCcD_Sph mWepon2Sph; a dCcD_Cyl is 0x130, a dCcD_Sph 0x12C.
	TnOffCoCyl      = 0xC74
	TnOffTgCyl      = 0xDA4
	TnOffHeadSph    = 0xED4
	TnOffDefSph     = 0x1000
	TnOffWeponSph   = 0x112C
	TnOffWepon2Sph  = 0x1258
	TnOffDefR       = TnOffDefSph + CcShapeOffR // f32 mDefenceSph radius (0x1124; 70 in defence, d_a_tn.cpp:2831)
	TnOffHeadCenter = 0x1384                    // cXyz m1384 (d_a_tn.h:100), the head sphere's centre
	TnOffDefCenter  = 0x13E4                    // cXyz m13E4 (d_a_tn.h:108), the defence sphere's centre
	// TnOffM1402 is s16 m1402 (d_a_tn.h:116), the part wobble.
	TnOffM1402 = 0x1402
	// TnOffM140C is s8 m140C (d_a_tn.h:120): nonzero until the sword is
	// attached on the first execute (d_a_tn.cpp:3490-3497, 4323-4326).
	TnOffM140C = 0x140C
	// TnOffBokoPID is fpc_ProcID mBokoPcId (d_a_tn.h:122): the sword
	// (a proc 0x1D5 BOKO, ProcBoko) the Darknut carries, from search_wepon
	// (d_a_tn.cpp:770); fpcM_ERROR_PROCESS_ID_e when none.
	TnOffBokoPID = 0x1410
	// TnOffM1444 is s8 m1444 (d_a_tn.h:141): the parry window, 1 in
	// defence (d_a_tn.cpp:1934), counted down at :2826-2827.
	TnOffM1444 = 0x1444
	// TnOffMantPID is fpc_ProcID mMantPcId (d_a_tn.h:143), the cape.
	TnOffMantPID = 0x1448
	// TnOffM14F8 is s8 m14F8 (d_a_tn.h:148): nonzero runs the demo camera
	// (d_a_tn.cpp:2565, 3127, 3524-3530).
	TnOffM14F8 = 0x14F8
	// TnOffM1518 is s8 m1518 (d_a_tn.h:154): nonzero hides the Darknut
	// (d_a_tn.cpp:2065, 3499-3522).
	TnOffM1518 = 0x1518
	// TnOffIce is enemyice mEnemyIce (d_a_tn.h:162); TnOffIceShrink its
	// s8 mLightShrinkTimer (0x152A), TnOffIceFreeze its s16 mFreezeTimer.
	TnOffIce       = 0x1524
	TnOffIceShrink = TnOffIce + EnemyIceOffTimer
	TnOffIceFreeze = TnOffIce + EnemyIceOffFreezeTimer
	// TnOffFire is enemyfire mEnemyFire (d_a_tn.h:163), TnOffJntHit
	// JntHit_c* mpJntHit (:164), the last field.
	TnOffFire   = 0x18DC
	TnOffJntHit = 0x1B04

	// The mRemainingEquipmentPieces bits (d_a_tn.cpp:62-67).
	TnPieceArmor   = 0x01
	TnPieceHelmet  = 0x02
	TnPieceShield  = 0x04
	TnPieceUnknown = 0x08
	// The mEquipmentType values (d_a_tn.cpp:53-60): TnEquipShield and up
	// carry the shield, TnEquipCape and up the cape.
	TnEquipShield = 2
	TnEquipCape   = 4
	// The dr.mAction values (d_a_tn.cpp:25-42; 22 is the damagereaction's
	// water fail, dr_body_bg_check, c_damagereaction.cpp:669-676).
	TnActJyunkai   = 0
	TnActStand     = 1
	TnActFightRun  = 4
	TnActFight     = 5
	TnActDefence   = 10
	TnActFail      = 20
	TnActYoganFail = 21
	TnActWaterFail = 22
	TnActDDozou    = 23
	TnActSDemo     = 25
	// TnBehaviorFrozen is BEHAVIOR_FROZEN_IN_TIME (d_a_tn.cpp:51); the
	// params behaviour nibble 12..15 (d_a_tn.cpp:46-51) is the design's
	// params gate.
	TnBehaviorFrozen = 15
	// The body model's joints the attach hook uses (assets/GZLE01/res/
	// Object/Tn.h:259-277, TN_MAIN_JNT_*): MUNE1 the armor, ATAMA1 the
	// helmet, ITEM_L1 the shield hand, ITEM_R1 the sword hand.
	TnJntMune1  = 0x0A
	TnJntItemL1 = 0x0F
	TnJntItemR1 = 0x17
	TnJntAtama1 = 0x1C
)

// The actor's method table and its execute function: fopAc_ac_c's
// actor_method_class* sub_method (f_op_actor.h:274), whose
// process_method_class mpExecuteFunc is at +8 (f_pc_method.h:10-13). The
// TN design's P0-c dumps words at fixed distances below daTn_Execute
// (config/GZLE01/rels/d_a_tn/symbols.txt:68,71,73).
const (
	ActorOffSubMtd     = 0x0EC
	MethodOffExecute   = 0x08
	TnExecToPartMove   = 0xAEA8 - 0x9E68 // daTn_Execute - part_move__FP8tn_classi = 0x1040
	TnExecToYoroiBreak = 0xAEA8 - 0x8C5C // daTn_Execute - yoroi_break__FP8tn_classP4cXyzUc = 0x224C
)
