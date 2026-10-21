package addr

// The Armos, am2_class (include/d/actor/d_a_am2.h:11-58; proc
// fpcNm_AM2_e 0x00CC, f_pc_name.h:216), as the enemy mirror's allow batch
// reads it on the host (ww-multiplayer-handoff/design/
// enemy-mirror-allow.json, "type_table" AM2). Offsets are from the actor;
// each cites the header line of the field. Batch A0 only reads them
// (enemy-watch --kind am2, enemy-match --dry).
const (
	// Am2ClassSize is sizeof(am2_class): mEnemyIce 0xA8C (d_a_am2.h:57),
	// the last member, + enemyice 0x3B8 (c_damagereaction.h:43).
	Am2ClassSize = 0xE44
	// Am2OffMorf is mDoExt_McaMorf* mpMorf (d_a_am2.h:15); actor.model is
	// its model (d_a_am2.cpp:1331).
	Am2OffMorf = 0x2B8
	// Am2OffBtk and Am2OffBrk are the mDoExt_btkAnm* and mDoExt_brkAnm*
	// (d_a_am2.h:17-18).
	Am2OffBtk = 0x2C0
	Am2OffBrk = 0x2C4
	// u8 mType, mPrmAreaRadius, mStartsInactive, mSwitch (d_a_am2.h:19-22).
	Am2OffType           = 0x2C8
	Am2OffStartsInactive = 0x2CA
	Am2OffSwitch         = 0x2CB
	// Am2OffAction is u8 mAction (d_a_am2.h:23; Action d_a_am2.cpp:25-31),
	// the gate byte; Am2OffMode u8 mMode (:24).
	Am2OffAction = 0x2CC
	Am2OffMode   = 0x2CD
	// u8 m2CE, bool mbIsWeakBeingHit, bool mbNotInHomeRoom, bool
	// mbMadeWaterSplash, u8 mInAbyssTimer (d_a_am2.h:25-29).
	Am2OffM2CE          = 0x2CE
	Am2OffWeakHit       = 0x2CF
	Am2OffNotInHomeRoom = 0x2D0
	Am2OffAbyssTimer    = 0x2D2
	// The joint positions (d_a_am2.h:31-33): cXyz mEyeballPos (eyePos is
	// it less 15 in y, d_a_am2.cpp:1168-1169), mNeedlePos, mWeakPos (the
	// weak sphere's centre, :1185).
	Am2OffEyeballPos = 0x2E0
	Am2OffNeedlePos  = 0x2EC
	Am2OffWeakPos    = 0x2F8
	// Am2OffCountDown is s16 mCountDownTimers[5] (d_a_am2.h:38), [2] the
	// MAHI (stun) timer (d_a_am2.cpp:661); Am2OffCountUp s16
	// mCountUpTimers[4] (:39).
	Am2OffCountDown = 0x334
	Am2OffStunTimer = Am2OffCountDown + 2*2
	Am2OffCountUp   = 0x33E
	// Am2OffTargetAngleY is s16 mTargetAngleY (d_a_am2.h:40).
	Am2OffTargetAngleY = 0x346
	// Am2OffCurrBckIdx is int mCurrBckIdx (d_a_am2.h:43): anm_init's index
	// (d_a_am2.cpp:118).
	Am2OffCurrBckIdx = 0x34C
	// Am2OffPickedUpY is f32 mPickedUpYPos (d_a_am2.h:46), Am2OffAcchCir
	// dBgS_AcchCir mAcchCir (:48), the end of the sampler's mid span.
	Am2OffPickedUpY = 0x358
	Am2OffAcchCir   = 0x360
	// Am2OffAcch is dBgS_ObjAcch mAcch (d_a_am2.h:49); Am2OffGroundH its
	// f32 m_ground_h (d_bg_s_acch.h:235).
	Am2OffAcch    = 0x3A0
	Am2OffGroundH = Am2OffAcch + AcchOffGroundH
	// Am2OffStts is dCcD_Stts mStts (d_a_am2.h:50). The colliders
	// (d_a_am2.h:51-54): dCcD_Cyl mBodyCyl (R 35 H 150, its Co cleared
	// while carried, d_a_am2.cpp:694, 709, 1171-1174), dCcD_Cyl mNeedleCyl,
	// dCcD_Sph mEyeSph, dCcD_Sph mWeakSph (R 30 at mWeakPos,
	// d_a_am2.cpp:1185-1187).
	Am2OffStts      = 0x564
	Am2OffBodyCyl   = 0x5A0
	Am2OffNeedleCyl = 0x6D0
	Am2OffEyeSph    = 0x800
	Am2OffWeakSph   = 0x92C
	// Am2OffSmokeCb is dPa_smokeEcallBack mSmokeCb (d_a_am2.h:55), the
	// field after the colliders.
	Am2OffSmokeCb = 0xA58
	// Am2OffIce is enemyice mEnemyIce (d_a_am2.h:57).
	Am2OffIce       = 0xA8C
	Am2OffIceShrink = Am2OffIce + EnemyIceOffTimer
	Am2OffIceFreeze = Am2OffIce + EnemyIceOffFreezeTimer
	// The actions (d_a_am2.cpp:25-31).
	Am2ActDousa  = 0
	Am2ActMahi   = 1
	Am2ActItai   = 2
	Am2ActHandou = 3
	Am2ActModoru = 4
	// Am2EyeDown and Am2AttnUp: eyePos is mEyeballPos less 15, the
	// attention point 120 above current.pos (d_a_am2.cpp:1166-1169);
	// Am2TgHitUp the body cylinder's half height.
	Am2EyeDown = 15
	Am2AttnUp  = 120
	Am2TgHitUp = 75
)
