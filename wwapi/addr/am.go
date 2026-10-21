package addr

// The Armos Knight, am_class (include/d/actor/d_a_am.h:11-57; proc
// fpcNm_AM_e 0x00CB, f_pc_name.h:215), as the enemy mirror's allow batch
// reads it on the host (ww-multiplayer-handoff/design/
// enemy-mirror-allow.json, "type_table" AM). Offsets are from the actor
// (GZLE: m033C_demo is the demo's only, d_a_am.h:42-44); each cites the
// header line of the field. Batch A0 only reads them (enemy-watch --kind
// am, enemy-match --dry).
const (
	// AmClassSize is sizeof(am_class): mEnemyFire 0xE74 (d_a_am.h:56) +
	// enemyfire 0x228 (c_damagereaction.h:69).
	AmClassSize = 0x109C
	// AmOffEyeJntHit is JntHit_c* mEyeJntHit (d_a_am.h:14), AmOffMorf
	// mDoExt_McaMorf* mpMorf (:15).
	AmOffEyeJntHit = 0x2B4
	AmOffMorf      = 0x2B8
	// u8 mType, mPrmAreaRadius, mStartsInactive, mSwitch (d_a_am.h:17-20).
	AmOffType           = 0x2C0
	AmOffStartsInactive = 0x2C2
	AmOffSwitch         = 0x2C3
	// AmOffAction is u8 mAction (d_a_am.h:21; Action d_a_am.cpp:28-33),
	// the gate byte; AmOffMode u8 mMode (:22; Mode d_a_am.cpp:35-46).
	AmOffAction = 0x2C4
	AmOffMode   = 0x2C5
	// AmOffHugeKnockback is u8 mHugeKnockback (d_a_am.h:23),
	// AmOffBodyHit bool mbIsBodyBeingHit (:24).
	AmOffHugeKnockback = 0x2C6
	AmOffBodyHit       = 0x2C7
	// AmOffCountDown is s16 mCountDownTimers[4] (d_a_am.h:25); [1] is the
	// play-skip timer (d_a_am.cpp:862, 1034-1036). AmOffCountUp is s16
	// mCountUpTimers[5] (:26).
	AmOffCountDown = 0x2C8
	AmOffPlaySkip  = AmOffCountDown + 2*1
	AmOffCountUp   = 0x2D0
	// AmOffTargetAngleY is s16 mTargetAngleY (d_a_am.h:27).
	AmOffTargetAngleY = 0x2DA
	// AmOffCurrBckIdx is int mCurrBckIdx (d_a_am.h:30): anm_init's index
	// (d_a_am.cpp:134).
	AmOffCurrBckIdx = 0x2E0
	// AmOffSwallowedPID is fpc_ProcID mSwallowedActorPID (d_a_am.h:31).
	AmOffSwallowedPID = 0x2E4
	// The joint positions nodeCallBack writes (d_a_am.h:35-38;
	// d_a_am.cpp:62-80): cXyz mEyeballPos (the eye sphere's centre),
	// mMouthPos (the mouth's), mWaistPos, mJawPos.
	AmOffEyeballPos = 0x2F4
	AmOffMouthPos   = 0x300
	AmOffWaistPos   = 0x30C
	AmOffJawPos     = 0x318
	// AmOffEyeRot is csXyz mEyeRot (d_a_am.h:40): .x at +0, .y at +2, the
	// annex Aux[0..1].
	AmOffEyeRot  = 0x330
	AmOffEyeRotY = AmOffEyeRot + 2
	// AmOffEmitter is JPABaseEmitter* m033C (d_a_am.h:45), the field after
	// mTargetEyeRot: the end of the sampler's mid span.
	AmOffEmitter = 0x33C
	// AmOffAcch is dBgS_ObjAcch mAcch (d_a_am.h:49); AmOffGroundH its f32
	// m_ground_h (d_bg_s_acch.h:235).
	AmOffAcch    = 0x404
	AmOffGroundH = AmOffAcch + AcchOffGroundH
	// AmOffStts is dCcD_Stts mStts (d_a_am.h:50). The colliders
	// (d_a_am.h:51-54): dCcD_Sph mEyeSph, dCcD_Sph mMouthSph (Co only),
	// dCcD_Cyl mBodyCyl, dCcD_Cyl mNeedleCyl (the At spike). Their create
	// tables carry R 15 (d_a_am.cpp:1237-1360); the sizes are set every
	// frame (d_a_am.cpp:1062-1078): eye R 60 (:1063), mouth R 100 (:1067),
	// body H 300 R 80 (:1071-1072), needle H 30 R 130 (:1077-1078) centred
	// 40 above current.pos (:1075).
	AmOffStts      = 0x5C8
	AmOffEyeSph    = 0x604
	AmOffMouthSph  = 0x730
	AmOffBodyCyl   = 0x85C
	AmOffNeedleCyl = 0x98C
	// AmOffIce is enemyice mEnemyIce (d_a_am.h:55), AmOffFire enemyfire
	// mEnemyFire (:56).
	AmOffIce       = 0xABC
	AmOffFire      = 0xE74
	AmOffIceShrink = AmOffIce + EnemyIceOffTimer
	AmOffIceFreeze = AmOffIce + EnemyIceOffFreezeTimer
	// The actions (d_a_am.cpp:28-33) and the ITAI modes the flags test:
	// 44 swallows a bomb, 45 chews it, 46 thrashes, 47 plays DEAD and
	// explodes (d_a_am.cpp:849-935).
	AmActDousa        = 0
	AmActModoru       = 1
	AmActHandou       = 2
	AmActItai         = 3
	AmModeBombSwallow = 44
	AmModeBombThrash  = 46
	AmModeDead        = 47
	// AmEyeUp and AmAttnUp: eyePos 250 and the attention point 330 above
	// current.pos (d_a_am.cpp:1055-1058); AmTgHitUp the body cylinder's
	// half height (H 300, d_a_am.cpp:1071).
	AmEyeUp   = 250
	AmAttnUp  = 330
	AmTgHitUp = 150
)
