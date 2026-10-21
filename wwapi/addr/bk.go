package addr

// The Bokoblin, bk_class (include/d/actor/d_a_bk.h:13-143; proc
// fpcNm_BK_e 0x00BE, f_pc_name.h:199), as the generalised enemy mirror
// reads it on the host (ww-multiplayer-handoff/design/
// enemy-mirror-general.json, "type_table" Bokoblin_0xBE and "flags"
// host_reads BK). Offsets are from the actor; each cites the header line
// of the field. Batch 1 only reads them (enemy-watch --kind bk,
// enemy-match --dry); nothing writes a Bokoblin yet.
const (
	// BkClassSize is sizeof(bk_class) (d_a_bk.h:143).
	BkClassSize = 0x1848
	// BkOffType is u8 mType (d_a_bk.h:16): params & 0xF at create
	// (d_a_bk.cpp:4759), except that 0xB becomes 4 with m02DC = 1
	// (d_a_bk.cpp:4762-4766). The mirror takes live types 0, 1 and 4.
	BkOffType = 0x2B4
	// BkOffDeathSwitch is u8 m02B8 (d_a_bk.h:20), the death switch: params
	// angle.z, 0xFF read as 0 (create); fail() sets it (d_a_bk.cpp:2644-2646,
	// :2729-2731), which a quiet death on a client never runs, so client Go
	// sets it (enemy-mirror-general.json death_and_drops.quiet_steps).
	BkOffDeathSwitch = 0x2B8
	// BkOffDrawGate is u8 m02BA (d_a_bk.h:22): nonzero hides the
	// Bokoblin (d_a_bk.cpp:526).
	BkOffDrawGate = 0x2BA
	// BkOffMorf is mDoExt_McaMorf* mpMorf (d_a_bk.h:24).
	BkOffMorf = 0x2BC
	// BkOffEyeBtp is mDoExt_btpAnm* m02C4 (d_a_bk.h:26), the eye pattern
	// (not redone by the mirror).
	BkOffEyeBtp = 0x2C4
	// The shield model and its gate (d_a_bk.h:32-33), the bouen (torch
	// guard) model and its gate (d_a_bk.h:36-37).
	BkOffShieldModel = 0x2D0
	BkOffShieldGate  = 0x2D4
	BkOffBouenModel  = 0x2D8
	BkOffBouenGate   = 0x2DC
	// BkOffHide2 is s8 m02DE (d_a_bk.h:39): nonzero hides the Bokoblin
	// (d_a_bk.cpp:526).
	BkOffHide2 = 0x2DE
	// The weapon blur (d_a_bk.h:44-46): f32 m02EC its scale, int m02F0
	// on, int m02F4 its shape.
	BkOffBlurScale = 0x2EC
	BkOffBlurOn    = 0x2F0
	BkOffBlurShape = 0x2F4
	// BkOffPlaySkip is s16 m030C (d_a_bk.h:51), BkOffInvuln s16 m030E
	// (d_a_bk.h:52).
	BkOffPlaySkip = 0x30C
	BkOffInvuln   = 0x30E
	// BkOffDR is damagereaction dr (d_a_bk.h:72; c_damagereaction.h:71-193,
	// 0x7BC bytes): mMode s16 +4, mAction s16 +6, m48C csXyz +0x48C,
	// mInvincibleTimer s16 +0x4CE, m714 fopAc_ac_c* +0x714.
	BkOffDR            = 0x370
	BkOffDRMode        = BkOffDR + 0x4   // s16 mMode (c_damagereaction.h:80)
	BkOffDRAction      = BkOffDR + 0x6   // s16 mAction (c_damagereaction.h:81)
	BkOffTilt          = BkOffDR + 0x48C // csXyz m48C.x (c_damagereaction.h:124)
	BkOffTiltZ         = BkOffTilt + 4   // m48C.z
	BkOffDRInvincible  = BkOffDR + 0x4CE // s16 mInvincibleTimer (c_damagereaction.h:151)
	BkOffDRM714        = BkOffDR + 0x714 // fopAc_ac_c* m714 (c_damagereaction.h:170)
	DamageReactionSize = 0x7BC           // c_damagereaction.h:193
	// BkOffWeaponHold is int m0B30 (d_a_bk.h:74): nonzero while the
	// Bokoblin holds its weapon.
	BkOffWeaponHold = 0xB30
	// The colliders (d_a_bk.h:89-93): dCcD_Cyl m0B88 (Co), dCcD_Cyl m0CB8
	// (Tg), dCcD_Sph m0DE8 (head), dCcD_Sph m0F14 (shield), dCcD_Sph m1040
	// (At); a dCcD_Cyl is 0x130, a dCcD_Sph 0x12C.
	BkOffCoCyl     = 0xB88
	BkOffTgCyl     = 0xCB8
	BkOffHeadSph   = 0xDE8
	BkOffShieldSph = 0xF14
	BkOffAtSph     = 0x1040
	// BkOffHeadCenter is cXyz m116C (d_a_bk.h:94), BkOffShieldCenter cXyz
	// m11CC (d_a_bk.h:102).
	BkOffHeadCenter   = 0x116C
	BkOffShieldCenter = 0x11CC
	// BkOffJaw is s16 m11F4 (d_a_bk.h:109).
	BkOffJaw = 0x11F4
	// BkOffBokoPID is u32 m1200 (d_a_bk.h:112): the process ID of the
	// BOKO (addr.ProcBoko) the Bokoblin carries, set by wepon_search
	// (d_a_bk.cpp:831); fpcM_ERROR_PROCESS_ID_e (0xFFFFFFFF) when none.
	BkOffBokoPID = 0x1200
	// BkOffBokoAttach is s8 m1214 (d_a_bk.h:118), BkOffDeleteReq s8 m121C
	// (d_a_bk.h:123; nonzero hides, d_a_bk.cpp:526), BkOffDemoGate s8
	// m1234 (d_a_bk.h:134).
	BkOffBokoAttach = 0x1214
	BkOffDeleteReq  = 0x121C
	BkOffDemoGate   = 0x1234
	// BkOffIce is enemyice mEnemyIce (d_a_bk.h:140); BkOffIceShrink its
	// s8 mLightShrinkTimer (c_damagereaction.h:16), BkOffIceFreeze its s16
	// mFreezeTimer (c_damagereaction.h:21).
	BkOffIce       = 0x1264
	BkOffIceShrink = BkOffIce + EnemyIceOffTimer
	BkOffIceFreeze = BkOffIce + EnemyIceOffFreezeTimer
	// BkOffJntHit is JntHit_c* mpJntHit (d_a_bk.h:142), the last field.
	BkOffJntHit = 0x1844
	// BkParamTypeMask is mType's source in the params (d_a_bk.cpp:4759).
	BkParamTypeMask = 0xF
	// BkTypeTorchShield is the params type create rewrites to 4 with the
	// bouen model (d_a_bk.cpp:4762-4766).
	BkTypeTorchShield = 0xB
	// BkBokoNone is fpcM_ERROR_PROCESS_ID_e: m1200 names no weapon.
	BkBokoNone uint32 = 0xFFFFFFFF
)
