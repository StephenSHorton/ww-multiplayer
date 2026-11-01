package addr

// The Stalfos, st_class (include/d/actor/d_a_st.h:46-150; proc fpcNm_ST_e
// 0x00BF, ProcST in remote.go, f_pc_name.h:200), as the enemy mirror's ST
// row reads it (wind#27, docs/design/enemy-rows-next.md "Enemy mirror row:
// ST Stalfos"; the C copy is inject/include/wwenemy_kinds.h EMK_ST_*,
// pinned by mods/enemysync TestKindTablePins). Offsets are from the actor
// in the retail layout (VERSION > DEMO: m02F8, m0306, m1E82 and m1E86
// present); each cites the header line of the field. An fopEn_enemy_c with
// no damagereaction whose body morf is never drawn: Draw draws the 26
// part models (d_a_st.cpp:360-392).
const (
	// StClassSize is sizeof(st_class) (d_a_st.h:150).
	StClassSize = 0x2368
	// StOffBehavior is u8 mBehaviorType (d_a_st.h:49): params & 0xF at
	// create (d_a_st.cpp:2834); 1 underground, 2/3 the coffins, 14 the
	// split's upper half (spawned, refused). The walk's gate byte.
	StOffBehavior = 0x2B4
	// StOffDeathSwitch is u8 mDeathSwitch (d_a_st.h:53): current.angle.z
	// at create, 0xFF read as 0; 1..0x7F also sets fopAcStts_BOSS_e
	// (d_a_st.cpp:2838-2844).
	StOffDeathSwitch = 0x2B8
	// StOffDrawGate is u8 m02B9 (d_a_st.h:54): the ambush switch + 1 (or
	// 0xFF) until the rise; Draw returns while it is nonzero (d_a_st.cpp:420).
	StOffDrawGate = 0x2B9
	// StOffMorf is mDoExt_McaMorf* mpMorf (d_a_st.h:56), never drawn;
	// mpMorf2 (the detached head) follows it.
	StOffMorf  = 0x2BC
	StOffMorf2 = 0x2C0
	// StOffM02C4 is s16 m02C4, the action's step; StOffAction s16
	// mActionState (d_a_st.h:58-59).
	StOffM02C4  = 0x2C4
	StOffAction = 0x2C6
	// StOffTimers is s16 mTimers[5] (d_a_st.h:70); [2] gates the hair
	// (Draw's ke_disp, d_a_st.cpp:433).
	StOffTimers    = 0x2EC
	StOffHairTimer = StOffTimers + 4
	// StOffInvuln is s16 m02F6 (d_a_st.h:72): damage_check runs only while
	// it is 0, a hit sets 5 (d_a_st.cpp:1565, 1579).
	StOffInvuln = 0x2F6
	// StOffM02FA is s8 m02FA (d_a_st.h:75): nonzero, Execute deletes it.
	StOffM02FA = 0x2FA
	// StOffM0304 is s16 m0304 (d_a_st.h:79): the KAITEN spin's extra yaw,
	// added to shape_angle.y in the base matrix (d_a_st.cpp:894, 2592-2595).
	StOffM0304 = 0x304
	// StOffSph is dCcD_Sph m054C[7] (d_a_st.h:88): 0 the head (R 45), 1..6
	// the body (R 25; cc_set d_a_st.cpp:2352-2410); StOffWeaponSph the mace's
	// At sphere m0D80 (d_a_st.h:89).
	StOffSph       = 0x54C
	StSphStride    = 0x12C
	StSphs         = 7
	StOffWeaponSph = 0xD80
	// StOffPartsLeft is s8 m0ED2 (d_a_st.h:93): 0x19 while whole
	// (d_a_st.cpp:2908).
	StOffPartsLeft = 0xED2
	StPartsWhole   = 0x19
	// StOffParts is st_p mParts[26] (d_a_st.h:94; st_p :11-30, 0x6C each):
	// J3DModel* +0, s8 mPartState +4, cXyz mPartPos +8. Part 15 is the
	// head: its state 8 is the dying head (d_a_st.cpp:1592, 2195-2226).
	StOffParts      = 0xED4
	StPartStride    = 0x6C
	StPartCount     = 26
	StOffHeadState  = StOffParts + 15*StPartStride + 4
	StOffHeadPos    = StOffHeadState + 4
	StHeadStateDead = 8
	// StOffNunchaku is s8 m1DC8 (d_a_st.h:99): the arm pulled off (fight2).
	StOffNunchaku = 0x1DC8
	// StOffBokoPID is fpc_ProcID mHeldWeaponEntityId (d_a_st.h:101): the
	// mace, a BOKO of Type_STALFOS_MACE (d_a_st.cpp:2898); StOffMace s8
	// m1DD0 (d_a_st.h:102): 10 while it is carried (d_a_st.cpp:2271-2273).
	StOffBokoPID   = 0x1DCC
	StOffMace      = 0x1DD0
	StMaceCarried  = 10
	StOffUpperPID  = 0x1DD4 // fpc_ProcID mUpperBodyEntityId (d_a_st.h:104): StUpperNone none
	StUpperNone    = 0xFFF
	StOffHeadOff   = 0x1DD8 // s8 m1DD8 (d_a_st.h:105): nonzero, the head is detached
	StOffShake     = 0x1DE4 // s8 m1DE4 (d_a_st.h:110): the hit shake
	StOffBodyHP    = 0x1E84 // s8 m1E84 (d_a_st.h:118): the body's HP, 16 at create
	StOffDeathWait = 0x1E85 // s8 mCountdownToDeath (d_a_st.h:119)
	// StOffIce is enemyice mEnemyIce (d_a_st.h:145): shrink timer +0x6
	// (0x1FAE), freeze +0xE (0x1FB6).
	StOffIce       = 0x1FA8
	StOffIceShrink = StOffIce + EnemyIceOffTimer
	StOffIceFreeze = StOffIce + EnemyIceOffFreezeTimer
	// StOffShadowID is u32 mShadowId (d_a_st.h:146).
	StOffShadowID = 0x2360
	// StBehaviorUpper is BEHAVIOR_UPPER_BODY_ONLY (d_a_st.cpp:20).
	StBehaviorUpper = 14
	// The mActionState values the mirror adopts (d_a_st.cpp:1481-1517):
	// jyunkai, fight, the underground and coffin ambush waits; 0x1F
	// head_damage is intact too (a head spin on a whole body).
	StActJyunkai     = 0
	StActFight       = 5
	StActUnderground = 0xF
	StActCoffin      = 0x10
	StActHeadDamage  = 0x1F
	// StTgHitUp is the fixed belly height collider 2 is centred at above
	// current.pos (design R3; EMK_ST_TG_HIT_UP, knob st:tg_hit_up).
	StTgHitUp = 100
)
