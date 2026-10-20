package addr

// The remote-Link pipeline's reads of the local Link (a client's PSTATE,
// ww-multiplayer-handoff/design/remote-link-design.md §8.1) and the proc
// names its PROC_POLICY seed lists (§6.4). Every Link offset is from
// daPy_py_c (d_a_player.h:163) or daPy_lk_c (d_a_player_main.h:411), read
// through PlayerPtr. They are the decomp's struct layout; only
// PlayerOffShieldAngleY is corroborated by a live read so far (Step −1:
// Link's mCyl Tg shield pointer, mCyl+0xD8, read 0x80ABD9F2 with Link at
// 0x80ABA4AC, which is +0x3546).
const (
	// PlayerOffCutType and PlayerOffCutCount are daPy_py_c's u8 mCutType
	// and mCutCount (d_a_player.h:479-480): the sword swing at_power_check
	// types the attacker by (d_cc_uty.cpp:16-34,88-131).
	PlayerOffCutType  = 0x290
	PlayerOffCutCount = 0x291
	// PlayerOffDamageWait is daPy_py_c's s16 mDamageWaitTimer
	// (d_a_player.h:482): Link's i-frames, nonzero while setCollision
	// keeps his Tg off (d_a_player_main.cpp:9775-9781).
	PlayerOffDamageWait = 0x294
	// PlayerOffAnmHeapUpper is daPy_lk_c's daPy_anmHeap_c
	// m_anm_heap_upper[3] (d_a_player_main.h:2080), PlayerAnmHeapSize each
	// (u16 mIdx at +0, d_a_player_main.h:18-26); checkUpperAnime reads
	// entry PlayerUpperMove2 (UPPER_MOVE2_e, d_a_player_main.h:887,1805).
	// PlayerOffUpperAnm is that entry's mIdx, the upper body's LkAnm BCK.
	PlayerOffAnmHeapUpper = 0x2FFC
	PlayerAnmHeapSize     = 0x10
	PlayerUpperMove2      = 2
	PlayerOffUpperAnm     = PlayerOffAnmHeapUpper + PlayerUpperMove2*PlayerAnmHeapSize
	// PlayerOffShieldAngleY is daPy_lk_c's s16 mShieldFrontRangeYAngle
	// (d_a_player_main.h:2232), the angle mCyl's Tg shield front range
	// points at (SetTgShieldFrontRangeYAngle, d_a_player_main.cpp:12189).
	PlayerOffShieldAngleY = 0x3546
	// PlayerOffModeFlg is daPy_lk_c's u32 mModeFlg
	// (d_a_player_main.h:2302) and PlayerMode* its ModeFlg_* bits
	// (d_a_player_main.h:896-927).
	PlayerOffModeFlg = 0x3618
	PlayerModeDamage = 0x00000008 // ModeFlg_DAMAGE
	PlayerModeInShip = 0x00002000 // ModeFlg_IN_SHIP
	PlayerModeSwim   = 0x00040000 // ModeFlg_SWIM

	// daPy_lk_c::daPy_PROC values of mCurProc (PlayerOffCurProc;
	// d_a_player_main.h:413-630). checkPlayerGuard is CROUCH_DEFENSE, a
	// guard slip (checkGuardSlip: GUARD_SLIP or CROUCH_DEFENSE_SLIP) or the
	// upper ATNG / ATNGHAM anime (d_a_player_main.cpp:1038-1040;
	// d_a_player_main.h:1819-1826).
	PlayerProcCrouchDefense     = 0x0C // daPyProc_CROUCH_DEFENSE_e (:426)
	PlayerProcCrouchDefenseSlip = 0x0D // daPyProc_CROUCH_DEFENSE_SLIP_e (:427)
	PlayerProcGuardSlip         = 0x6D // daPyProc_GUARD_SLIP_e (:523)
	PlayerProcDemoDead          = 0xB2 // daPyProc_DEMO_DEAD_e (:592): the death demo
	// The LkAnm BCK indices checkUpperGuardAnime compares PlayerOffUpperAnm
	// with (dRes_INDEX_LKANM_BCK_ATNG_e / _ATNGHAM_e,
	// assets/GZLE01/res/Object/LkAnm.h:25,30).
	PlayerAnmAtng    = 0x16
	PlayerAnmAtngHam = 0x1B
)

// Proc names (fpcNm_*, f_pc_name.h) of the remote-Link PROC_POLICY seed
// (remote-link-design.md §6.4): ProcMo2 is the one audited ALLOW (§3);
// the rest are DENY, enemies whose execute writes into or grabs the
// player: Floormaster's grab (d_a_fm.cpp:2783-2790), ReDead's
// getLinkPlayer reads (d_a_rd.cpp:438-1224), the bosses, and the
// Bokoblin until its GetName gates are audited. ProcBoko (play.go) is the
// weapon, not the Bokoblin.
const (
	ProcPlayer  = 0x0A9 // fpcNm_PLAYER_e (:178)
	ProcMo2     = 0x0BD // fpcNm_MO2_e (:198)
	ProcFM      = 0x079 // fpcNm_FM_e (:130)
	ProcBK      = 0x0BE // fpcNm_BK_e (:199)
	ProcBPW     = 0x0D4 // fpcNm_BPW_e (:224)
	ProcBWD     = 0x0DA // fpcNm_BWD_e (:230)
	ProcRD      = 0x0E1 // fpcNm_RD_e (:237)
	ProcDaiocta = 0x0E2 // fpcNm_DAIOCTA_e (:238)
	ProcBTD     = 0x0EB // fpcNm_BTD_e (:247)
	ProcBMD     = 0x0EC // fpcNm_BMD_e (:248)
	ProcBDK     = 0x0F0 // fpcNm_BDK_e (:252)
	ProcBST     = 0x0F2 // fpcNm_BST_e (:254)
	ProcFganon  = 0x0F3 // fpcNm_FGANON_e (:255)
	ProcBGN     = 0x0F5 // fpcNm_BGN_e (:257)
	ProcBGN2    = 0x0F6 // fpcNm_BGN2_e (:258)
	ProcBGN3    = 0x0F7 // fpcNm_BGN3_e (:259)
)

// FF stage names (dStage_startStage_c mName) the remote-Link FF denylist
// seeds with: Forsaken Fortress, where every keyed Moblin may be a jail
// Moblin (the stage strings d_a_himo3.cpp:143-144, d_a_obj_barrel.cpp:200
// and d_a_mo2.cpp:3392 compare; the tower from the audio scene table,
// JAIZelScene.cpp:290). Not yet read on a live game (the design's Step −1
// left the FF names open).
const (
	StageFFOutside = "MajyuE"
	StageFFRooms   = "majroom"
	StageFFRooms2  = "Majroom"
	StageFFTower   = "Mjtower"
)
