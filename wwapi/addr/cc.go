package addr

// The ChuChu, cc_class (include/d/actor/d_a_cc.h:14-95; proc fpcNm_CC_e
// 0x00CF, f_pc_name.h:219), as the generalised enemy mirror reads it on
// the host (ww-multiplayer-handoff/design/enemy-mirror-general.json,
// "type_table" ChuChu_0xCF and "flags" host_reads CC). Offsets are from
// the actor; each cites the header line of the field. Batch 1 only reads
// them (enemy-watch --kind cc, enemy-match --dry).
const (
	// CcClassSize is sizeof(cc_class) (d_a_cc.h:95).
	CcClassSize = 0xE04
	// CcOffMorf is mDoExt_McaMorf* m2B4 (d_a_cc.h:17), the body morf.
	CcOffMorf = 0x2B4
	// CcOffBehavior is u8 mBehaviorType (d_a_cc.h:28): params & 0xFF at
	// create, 0xFF read as 0 (d_a_cc.cpp:2564,2577-2579). The mirror takes
	// 0, 1 and 3.
	CcOffBehavior = 0x2F0
	// CcOffColor is u8 mColorType (d_a_cc.h:29): params >> 8 & 0xFF, 0xFF
	// read as 0, 10.. less 10, and 15 (the red insta-kill) read as 1 with
	// m304 set (d_a_cc.cpp:2565,2581-2583,2599-2605). The mirror takes 0,
	// 1, 2 and 4 (not 3, black).
	CcOffColor = 0x2F1
	// CcOffAction is u8 mCurrAction (d_a_cc.h:32), CcOffSubstate u8 m2F5
	// (:33), CcOffElectric u8 m2F7 (:35), CcOffNoShadowBG u8 m2F9 (:37; a
	// shadow/BG skip, not a hide: d_a_cc.cpp:168-174), CcOffStone u8 m2FE
	// (:42), CcOffSplit u8 m301 (:45), CcOffInstaKill u8 m304 (:48).
	CcOffAction     = 0x2F4
	CcOffSubstate   = 0x2F5
	CcOffElectric   = 0x2F7
	CcOffNoShadowBG = 0x2F9
	CcOffStone      = 0x2FE
	CcOffSplit      = 0x301
	CcOffInstaKill  = 0x304
	// CcOffWobble is csXyz m310 (d_a_cc.h:52), CcOffGroundTilt csXyz m316
	// (:53; .x at +0, .z at +4, the live ground tilt, d_a_cc.cpp:82-83).
	CcOffWobble      = 0x310
	CcOffGroundTilt  = 0x316
	CcOffGroundTiltZ = CcOffGroundTilt + 4
	// CcOffSquashPhase is s16 m31C (d_a_cc.h:54), CcOffBckIndex s32 m320
	// (:55, the BCK index the ChuChu last set), CcOffYOffset f32 m328 (:57),
	// CcOffScale f32 m334 (:60).
	CcOffSquashPhase = 0x31C
	CcOffBckIndex    = 0x320
	CcOffYOffset     = 0x328
	CcOffScale       = 0x334
	// CcOffTimers is s16 m34E[7] (d_a_cc.h:68); [4] the stun timer that
	// stops the squash (d_a_cc.cpp:88), CcOffTimersEnd where m35C starts
	// (:69).
	CcOffTimers    = 0x34E
	CcOffStunTimer = CcOffTimers + 2*4
	CcOffTimersEnd = 0x35C
	// CcOffStts is dCcD_Stts mStts (d_a_cc.h:89), CcOffCyl dCcD_Cyl mCyl
	// (:90): its cCcD_ObjHitInf At/Tg/Co mSPrm words at +0x00/+0x18/+0x2C
	// (c_cc_d.h:379-384,505-510), its centre at Mo2CcCenterOff.
	CcOffStts      = 0x680
	CcOffCyl       = 0x6BC
	CcCylOffAtSPrm = 0x00
	CcCylOffTgSPrm = 0x18
	CcCylOffCoSPrm = 0x2C
	// CcOffCylSPrmEnd is the end of the sampler's mCyl span: through Co
	// mSPrm.
	CcOffCylSPrmEnd = CcOffCyl + CcCylOffCoSPrm + 4
	// CcOffMtx is Mtx m7EC (d_a_cc.h:91), the field after mCyl.
	CcOffMtx = 0x7EC
	// CcOffIce is enemyice mEnemyIce (d_a_cc.h:92): shrink timer +6,
	// freeze timer +0xE (c_damagereaction.h:16,21).
	CcOffIce       = 0x81C
	CcOffIceShrink = CcOffIce + EnemyIceOffTimer
	CcOffIceFreeze = CcOffIce + EnemyIceOffFreezeTimer
	// CcSPrmSet is cCcD_TgSPrm_Set_e / cCcD_CoSPrm_Set_e (c_cc_d.h:33,47).
	CcSPrmSet uint32 = 0x01
	// CcColorBlack is mColorType 3, the black ChuChu (eff_color_dt,
	// d_a_cc.cpp:449 and its stone path); refused.
	CcColorBlack = 3
	// The ChuChu actions and substates the derived flags test
	// (d_a_cc.cpp:88; design "flags").
	CcActionLowEye    = 1
	CcActionDying     = 4
	CcActionNoSquash1 = 5
	CcActionNoSquash2 = 6
	CcSubstateDying   = 0x35
)
