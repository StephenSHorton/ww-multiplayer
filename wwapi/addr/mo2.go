package addr

// The Moblin, daMo2_c (include/d/actor/d_a_mo2.h; proc fpcNm_MO2_e 0x00BD,
// f_pc_name.h:198), as the enemy mirror reads it on the host and writes
// it on a client (ww-multiplayer-handoff/design/moblin-mirror-design.json,
// "host_side"). Offsets are from the actor; each cites the header line of
// the field. Nothing here is executed yet on a live game but what the
// enemy-watch probe reads back (step 1 of the prototype).
const (
	// Mo2OffMode is u8 mMode (d_a_mo2.h:38), the low byte of the actor's
	// params at create (d_a_mo2.cpp:4080): 0 a placed Moblin on patrol, 1
	// the lantern/jail-capable variant, which is a normal Moblin once the
	// player holds a pearl (rouya_mode 0, d_a_mo2.cpp:4134-4146; M_Dai
	// room 1's setIDs 0 and 1 are mode 1, setID 2 mode 0); the mirror
	// takes 0 and 1 and refuses Mo2ModeCarried and Mo2ModeStatue.
	Mo2OffMode     = 0x2BC
	Mo2ModeCarried = 5
	Mo2ModeStatue  = 0xF
	// Mo2OffMorf is mDoExt_McaMorf* mpMorf (d_a_mo2.h:45).
	Mo2OffMorf = 0x2C4
	// Mo2OffDeathSwitch is u8 mDeathSwitch (d_a_mo2.h:42), the switch the
	// Moblin's death sets; a quiet death on a client never runs it, so client
	// Go sets it (enemy-mirror-general.json death_and_drops.quiet_steps).
	Mo2OffDeathSwitch = 0x2C0
	// The sword-blur mesh (d_a_mo2.h:58-60; br_draw, d_a_mo2.cpp:601-638):
	// f32 m0590 its scale, int m0594 on, int m0598 its shape.
	Mo2OffBlurScale = 0x590
	Mo2OffBlurOn    = 0x594
	Mo2OffBlurShape = 0x598
	// Mo2OffAnmGate is s16 m05B0 (d_a_mo2.h:65): while nonzero anm_init is
	// a no-op (d_a_mo2.cpp:137-153). Mo2OffPlaySkip is s16 m05B2
	// (d_a_mo2.h:66): while nonzero the Moblin skips its morf's play()
	// (d_a_mo2.cpp:3488-3490), so the frame and the blend stand still.
	Mo2OffAnmGate  = 0x5B0
	Mo2OffPlaySkip = 0x5B2
	// Mo2OffInvuln is s16 m05B4 (d_a_mo2.h:67): nonzero while
	// damage_check drops hits (d_a_mo2.cpp:3072).
	Mo2OffInvuln = 0x5B4
	// Mo2OffHasWeapon is s8 mbHasInnateWeapon (d_a_mo2.h:90): the spear
	// shape is drawn iff it is 1 (d_a_mo2.cpp:3882-3890) and wait_set picks
	// its rows by it (d_a_mo2.cpp:1059-1100).
	Mo2OffHasWeapon = 0xDD4
	// Mo2OffDR is damagereaction mDamageReaction (d_a_mo2.h:88;
	// c_damagereaction.h:71-84): mMode s16 at +4, mAction s16 at +6.
	Mo2OffDR       = 0x614
	Mo2OffDRMode   = Mo2OffDR + 0x4 // s16 mMode (c_damagereaction.h:80)
	Mo2OffDRAction = Mo2OffDR + 0x6 // s16 mAction (c_damagereaction.h:81)
	// Mo2OffTilt is the first component of damagereaction's csXyz m48C
	// (c_damagereaction.h:124), the body tilt the mirror streams as one s16.
	Mo2OffTilt = Mo2OffDR + 0x48C
	// Mo2OffAttackType and Mo2OffAttackPhase are int m2060 and m2064
	// (d_a_mo2.h:99-100), the attack's type and phase; the mirror streams
	// their low bytes.
	Mo2OffAttackType  = 0x2060
	Mo2OffAttackPhase = 0x2064
	// The colliders (d_a_mo2.h:109-112): dCcD_Cyl mCoCyl, dCcD_Cyl mTgCyl,
	// dCcD_Sph mHeadSph, dCcD_Sph mDefenseSph. dCcD_GObjInf is 0xF8 and
	// cCcD_ShapeAttr 0x20 (c_cc_d.h:65-68; d_cc_d.h:499-538), so each
	// shape's centre is at Mo2CcCenterOff from the collider.
	Mo2OffCoCyl      = 0x208C
	Mo2OffTgCyl      = 0x21BC
	Mo2OffHeadSph    = 0x22EC
	Mo2OffDefenseSph = 0x2418
	// Mo2CcCenterOff is a collider's centre: cM3dGCyl/cM3dGSph mCenter at
	// +0 of the shape (c_m3d_g_cyl.h:17-22) after dCcD_GObjInf (0xF8) and
	// cCcD_ShapeAttr (0x20) (header arithmetic, never read live yet).
	Mo2CcCenterOff = 0x118
	// CcShapeOffR is a collider's f32 radius, cM3dGCyl/cM3dGSph mRadius at
	// +0xC of the shape (c_m3d_g_cyl.h:20), and CcShapeOffH a cylinder's f32
	// mHeight at +0x10 (c_m3d_g_cyl.h:21); the REL's CC_SHAPE_RADIUS and
	// CC_SHAPE_HEIGHT (inject/rel/enemy_mirror.c:368-369).
	CcShapeOffR = Mo2CcCenterOff + 0xC
	CcShapeOffH = Mo2CcCenterOff + 0x10
	// Mo2CcTgHitOff is a collider's Tg hit word: cCcD_ObjHitInf Tg at
	// +0x18 (c_cc_d.h:505-510), cCcD_ObjCommonBase mRPrm at +4 of it
	// (c_cc_d.h:379-384); bit 0 is cCcD_TgRPrm_Hit_e (c_cc_d.h:42-43).
	Mo2CcTgHitOff = 0x1C
	// Mo2CcTgHitObjOff is the Tg's mHitObj pointer, cCcD_ObjCommonBase +8
	// (c_cc_d.h:379-384) inside Tg at +0x18.
	Mo2CcTgHitObjOff = 0x20
	// Mo2OffHeadCenter is cXyz m28C8 (d_a_mo2.h:116), the head position
	// the head sphere is set to; Mo2OffShieldCenter is cXyz m2928
	// (d_a_mo2.h:124), the shield's.
	Mo2OffHeadCenter   = 0x28C8
	Mo2OffShieldCenter = 0x2928
	// Mo2OffBareHanded is u8 m2943 (d_a_mo2.h:129): the Moblin lost its
	// spear.
	Mo2OffBareHanded = 0x2943
	// Mo2OffHide is s8 m2A1C (d_a_mo2.h:160): hidden (not drawn).
	Mo2OffHide = 0x2A1C
	// Mo2OffIce is enemyice mEnemyIce (d_a_mo2.h:175); Mo2OffIceShrink its
	// s8 mLightShrinkTimer (c_damagereaction.h:16, EnemyIceOffTimer).
	Mo2OffIce       = 0x2A54
	Mo2OffIceShrink = Mo2OffIce + EnemyIceOffTimer
)

// mDoExt_McaMorf (include/m_Do/m_Do_ext.h:323-380), what mpMorf points
// at: a J3DMtxCalcMaya, then the model, the animation and the
// J3DFrameCtrl (J3DAnimation.h:828-876: a vtable, then u8 mAttribute,
// u8 mState, s16 mStart, s16 mEnd, s16 mLoop, f32 mRate, f32 mFrame).
const (
	McaMorfOffModel     = 0x50                       // J3DModel* mpModel (m_Do_ext.h:369)
	McaMorfOffAnm       = 0x54                       // J3DAnmTransform* mpAnm (m_Do_ext.h:370)
	McaMorfOffFrameCtrl = 0x58                       // J3DFrameCtrl mFrameCtrl (m_Do_ext.h:371)
	McaMorfOffAttr      = McaMorfOffFrameCtrl + 0x04 // u8 mAttribute (J3DAnimation.h:870)
	McaMorfOffState     = McaMorfOffFrameCtrl + 0x05 // u8 mState (J3DAnimation.h:871)
	McaMorfOffStart     = McaMorfOffFrameCtrl + 0x06 // s16 mStart (J3DAnimation.h:872)
	McaMorfOffEnd       = McaMorfOffFrameCtrl + 0x08 // s16 mEnd (J3DAnimation.h:873)
	McaMorfOffLoop      = McaMorfOffFrameCtrl + 0x0A // s16 mLoop (J3DAnimation.h:874)
	McaMorfOffRate      = McaMorfOffFrameCtrl + 0x0C // f32 mRate (J3DAnimation.h:875)
	McaMorfOffFrame     = McaMorfOffFrameCtrl + 0x10 // f32 mFrame (J3DAnimation.h:876)
	McaMorfFrameCtrlLen = 0x14                       // J3DFrameCtrl's size (J3DAnimation.h:877)
	McaMorfOffCurMorf   = 0x74                       // f32 mCurMorf (m_Do_ext.h:374)
	McaMorfOffPrevMorf  = 0x78                       // f32 mPrevMorf (m_Do_ext.h:375)
	McaMorfOffMorfStep  = 0x7C                       // f32 mMorfStep (m_Do_ext.h:376): setMorf's 1/morf (m_Do_ext.cpp:1452-1460)
	McaMorfOffSound     = 0x80                       // mDoExt_zelAnime* mpSound (m_Do_ext.h:377)
	// McaMorfAnimSpanOff and McaMorfAnimSpanLen are the one read the host
	// sampler makes of a morf: mpAnm through mMorfStep (m_Do_ext.h:370-376),
	// 0x2C bytes from +0x54.
	McaMorfAnimSpanOff = McaMorfOffAnm
	McaMorfAnimSpanLen = McaMorfOffSound - McaMorfOffAnm
)

// J3DModelOffBaseScale is J3DModel's Vec mBaseScale (J3DModel.h:148),
// which setBaseScale writes and calc folds into the joints
// (J3DModel.cpp:494, 784). The base matrix follows it (mBaseTransformMtx,
// 0x24: J3DMODEL_BASE_TR_MTX_OFFSET in inject/include/wwapi.h).
const J3DModelOffBaseScale = 0x18

// The collider fields a hit object carries beyond its cCcD_ObjAt (the
// mType and mAtp at +0x10 and +0x14 the client reads today): dCcD_GObjAt
// sits at +0x50 of a dCcD_GObjInf (after cCcD_Obj 0x40 and cCcD_DivideInfo
// 0x08; d_cc_d.h:305-332), its dCcD_GAtTgCoCommonBase mSPrm first (0x1C
// bytes: mSPrm, mRPrm, mHitCallback, mApid, mAc, mEffCounter, vptr), then
// u8 mSe +0x1C, u8 mMtrl +0x1D, u8 mHitMark +0x1E, u8 mSpl +0x1F
// (d_cc_d.h:325-331). The host's v2 At sphere copies Se, HitMark and Spl
// from the client's sword object and NoHitMark from its GAt mSPrm.
const (
	CcAtOffSPrm    = 0x50
	CcAtOffSe      = 0x6C
	CcAtOffHitMark = 0x6E
	CcAtOffSpl     = 0x6F
	// CcAtSPrmNoHitMark is dCcG_AtSPrm_NoHitMark_e in GAt mSPrm's low byte.
	CcAtSPrmNoHitMark uint8 = 0x02
)

// Statics the mirror reads or the REL calls through.
const (
	// RoomStayNo is dStage_roomControl_c::mStayNo (sym:18667,
	// d_stage.h:930), the room Link is in: StayNo under the design's name.
	RoomStayNo = StayNo
	// CcS is g_dComIfG_gameInfo.play.mCcS, the dCcS every collider is
	// submitted to (dComIfG_Ccsp, d_com_inf_game.h:4420-4422): GameInfo
	// 0x803C4C08 (sym:17472) + play 0x12A0 (d_com_inf_game.h:915) + mCcS
	// 0x1404 (d_com_inf_game.h:721).
	CcS uint32 = 0x803C72AC
	// CalcMtxPtr is calc_mtx (sym:18270), the Mtx* the matrix stack's
	// dr_matrix_set and the REL's PSMTXCopy read.
	CalcMtxPtr uint32 = 0x803F63A0
)
