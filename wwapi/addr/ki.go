package addr

// The Keese, ki_class (include/d/actor/d_a_ki.h:14-87; proc fpcNm_KI_e
// 0x00D8, f_pc_name.h:228), as the enemy mirror's allow batch reads it on
// the host (ww-multiplayer-handoff/design/enemy-mirror-allow.json,
// "type_table" KI). Offsets are from the actor (GZLE; m2E0 exists after
// the demo, d_a_ki.h:43-45); each cites the header line of the field.
// Batch A0 only reads them (enemy-watch --kind ki, enemy-match --dry).
const (
	// KiClassSize is sizeof(ki_class) (d_a_ki.h:87): mEnemyIce 0x924 +
	// enemyice 0x3B8 = 0xCDC, + enemyfire 0x228 (c_damagereaction.h:43,69).
	KiClassSize = 0xF04
	// KiOffMorf is mDoExt_McaMorf* mpMorf (d_a_ki.h:27), the body morf
	// (the Ki or the fire-Keese FK model, d_a_ki.cpp:1207-1232).
	KiOffMorf = 0x2B4
	// KiOffParams is u8 mParameters (d_a_ki.h:29): the action create
	// starts in and ki_atack_move returns to (d_a_ki.cpp:665, 1377).
	KiOffParams = 0x2CC
	// KiOffPathIndex is u8 mKiPathIndex (d_a_ki.h:31), 0xFF for none.
	KiOffPathIndex = 0x2CE
	// KiOffAction is u8 mAction (d_a_ki.h:34; ki_class::Action_e values
	// d_a_ki.h:15-23), the gate byte.
	KiOffAction = 0x2D1
	// KiOffBehavior is s8 mBehaviorType (d_a_ki.h:36), the sub-state each
	// action switches on (d_a_ki.cpp:286, 381, 429, 502).
	KiOffBehavior = 0x2D3
	// KiOffDrawGate is u8 m2D4 (d_a_ki.h:37): the switch (+1) a Keese
	// waits on before it draws or executes (d_a_ki.cpp:197, 971-982,
	// 1417).
	KiOffDrawGate = 0x2D4
	// KiOffCurrPathIndex is s8 mCurrKiPathIndex (d_a_ki.h:38).
	KiOffCurrPathIndex = 0x2D5
	// KiOffShrinkDelete is u8 m2DC (d_a_ki.h:42): nonzero shrinks the
	// Keese to nothing and deletes it (d_a_ki.cpp:963-969).
	KiOffShrinkDelete = 0x2DC
	// KiOffTimers is s16 mTimers[4] (d_a_ki.h:54), KiOffInvuln s16 m314
	// (:55; set d_a_ki.cpp:739, 1091, 1110; counted down :1006-1008).
	KiOffTimers = 0x30C
	KiOffInvuln = 0x314
	// KiOffKnockback is f32 m31C (d_a_ki.h:58), the knockback decay
	// (d_a_ki.cpp:793-794).
	KiOffKnockback = 0x31C
	// KiOffHeadBank is s16 m322, m324 (d_a_ki.h:60-61), the head-bank
	// targets; KiOffAux1 and KiOffAux2 s16 m326, m328 (:62-63), the
	// annex Aux[2..3].
	KiOffHeadBank = 0x322
	KiOffAux1     = 0x326
	KiOffAux2     = 0x328
	// KiOffM338 is s8 m338 (d_a_ki.h:70), not a play-skip timer (D15).
	KiOffM338 = 0x338
	// KiOffAcchCir is dBgS_AcchCir mAcchCir (d_a_ki.h:73), the end of the
	// sampler's mid span.
	KiOffAcchCir = 0x33C
	// KiOffAcch is dBgS_ObjAcch mAcch (d_a_ki.h:74); KiOffGroundH its
	// f32 m_ground_h (+0x94, d_bg_s_acch.h:235).
	KiOffAcch    = 0x37C
	KiOffGroundH = KiOffAcch + AcchOffGroundH
	// KiOffShadow is u8 m540 (d_a_ki.h:75).
	KiOffShadow = 0x540
	// KiOffStts is dCcD_Stts mStts (d_a_ki.h:76). The spheres
	// (d_a_ki.h:77-79): m580 the At (R 0, 30 in the lunge,
	// d_a_ki.cpp:578, 993), m6AC the Co (R 80, 0 in the lunge; l_kiHIO.m58,
	// d_a_ki.cpp:51, 580, 995), mDamageSphere the Tg (R 40, 20 in the
	// lunge, d_a_ki.cpp:579, 994). All three are centred on current.pos
	// (d_a_ki.cpp:1068-1070).
	KiOffStts      = 0x544
	KiOffAtSph     = 0x580
	KiOffCoSph     = 0x6AC
	KiOffTgSph     = 0x7D8
	KiOffSphsEnd   = 0x904 // u8 m904 (d_a_ki.h:80), the field after the spheres
	KiOffDmgType   = 0x905 // u8 mDamageType (d_a_ki.h:81): nonzero is the fire Keese
	KiOffBtk       = 0x920 // mDoExt_btkAnm* m920 (d_a_ki.h:84)
	KiOffIce       = 0x924 // enemyice mEnemyIce (d_a_ki.h:85)
	KiOffFire      = 0xCDC // enemyfire mEnemyFire (d_a_ki.h:86)
	KiOffIceShrink = KiOffIce + EnemyIceOffTimer
	KiOffIceFreeze = KiOffIce + EnemyIceOffFreezeTimer
	// The Keese's actions (ki_class::Action_e, d_a_ki.h:15-23).
	KiActWait        = 0
	KiActFly         = 1
	KiActFireSet     = 2
	KiActAttack      = 3
	KiActAttackIndex = 10
	KiActDamage      = 20
	KiActFail        = 30
	// KiBehaviorLunge is the mBehaviorType ki_atack_move dives in: the At
	// sphere grows to 30, the Tg shrinks to 20 and the Co to 0
	// (d_a_ki.cpp:502, 577-580).
	KiBehaviorLunge = 3
	// KiEyeUp and KiAttnUp: eyePos is current.pos, the attention point
	// 50 above it (d_a_ki.cpp:1064-1066).
	KiEyeUp  = 0
	KiAttnUp = 50
)
