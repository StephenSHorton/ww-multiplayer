package enemies

import (
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Enemy membership. An actor's group is copied from its profile when it is
// created (fopAc_Create, f_op_actor.cpp:266, from
// actor_process_profile_definition::group, f_op_actor.h:118-124), and the
// enemies are the profiles whose group is fopAc_ENEMY_e (2,
// f_op_actor.h:57): 46 of the decomp's 454 actor profiles. Morth is the
// one K1 class (design §7.2) outside it, in fopAc_ENV_e (3,
// f_op_actor.h:58; d_a_ks.cpp:1788), and is a member too. The group is
// what room-clear logic counts (enemySearchJugge, f_op_actor_mng.cpp:1275,
// for fopAcM_myRoomSearchEnemy; so a Morth never keeps a room shut) and
// §7.2's live filter tests. It also holds non-enemies, which the table
// keeps as Other or Boss: Orca, the Warship, a Forbidden Woods tentacle
// and the parts bosses create.
//
// Each class below cites its fpcNm_*_e line in f_pc_name.h (whose comments
// give the GZLE01 values, see wwapi/stage's Proc), the Group line of its
// profile, and how its death reaches the zone actor bit. dSv_info_c::onActor
// (d_save.cpp:1671-1682) is the only writer of those bits, reached only
// through fopAcM_onActor (f_op_actor_mng.h:444-446; dComIfGs_onActor,
// d_com_inf_game.h:1866-1868). That is called by ten classes' own death
// code and by enemy_ice (c_damagereaction.cpp:108-410) on a Light Arrow
// death (:159) and an ice shatter (:369); twelve classes poll enemy_ice.
// dStage_actorInit is the only reader (isActor, d_stage.cpp:1693).

// Group is a profile's fopAc_Group_e (f_op_actor.h:54-60).
type Group uint8

const (
	GroupEnemy Group = 2 // fopAc_ENEMY_e
	GroupEnv   Group = 3 // fopAc_ENV_e
)

func (g Group) String() string {
	switch g {
	case GroupEnemy:
		return "ENEMY"
	case GroupEnv:
		return "ENV"
	}
	return "group?"
}

// Kind is what co-op does with a class (design §7.2-7.3).
type Kind string

const (
	// K1 classes are the ones §7.2's light-shrink applier may kill.
	K1 Kind = "k1"
	// Boss classes are §7.2's exclusions: bosses and mini-bosses by proc,
	// and the parts their fights create.
	Boss Kind = "boss"
	// Other classes are every other member of the group.
	Other Kind = "other"
)

// Death is how a class's death reaches its zone actor bit.
type Death string

const (
	// DeathOwn classes call fopAcM_onActor from their own death code.
	DeathOwn Death = "own"
	// DeathIce classes don't; only enemy_ice does, on a Light Arrow
	// death or an ice shatter (c_damagereaction.cpp:159, :369).
	DeathIce Death = "ice"
	// DeathNone classes' code, all of it in the decomp, never records it.
	DeathNone Death = "none"
	// DeathUnknown classes' d_a_*.cpp is a NonMatching stub whose
	// function bodies are empty, so the decomp can't tell.
	DeathUnknown Death = "unknown"
)

// MayRecord reports whether a death of the class can set its zone actor
// bit: always for DeathOwn, on some deaths for DeathIce, perhaps for
// DeathUnknown.
func (d Death) MayRecord() bool { return d == DeathOwn || d == DeathIce || d == DeathUnknown }

// BossStatus is when the game gives a class fopAcStts_BOSS_e (0x04000000,
// f_op_actor.h:43), the status §7.2's K1 gate refuses. Nothing clears it
// (no fopAcM_OffStatus of it in the decomp).
type BossStatus string

const (
	BossNever   BossStatus = "never"   // not in its profile or code
	BossAlways  BossStatus = "always"  // in its profile's status, or set unconditionally on creation
	BossParams  BossStatus = "params"  // set on creation for some params; see Class.IsBoss
	BossUnknown BossStatus = "unknown" // a NonMatching stub whose profile lacks it
)

// Class is one enemy class.
type Class struct {
	Proc  stage.Proc `json:"proc"`
	Name  string     `json:"name"` // the fpcNm_*_e name without its affixes: "BK"
	What  string     `json:"what"` // the description its d_a_*.cpp opens with; "" when it has none
	Group Group      `json:"group"`
	Kind  Kind       `json:"kind"`
	Death Death      `json:"death"`
	// Ice is the offset of its enemyice member (c_damagereaction.h:12-46),
	// 0 for none; enemyice's mLightShrinkTimer is at +6.
	Ice  uint32     `json:"ice,omitempty"`
	Boss BossStatus `json:"boss"`
	// HomeMoves is true when its code moves home.pos after creation, so
	// a live actor's home.pos needn't be its entry's Pos (§7.2's
	// cross-check). A DeathUnknown stub's is unknown and false.
	HomeMoves bool `json:"home_moves,omitempty"`
	// K1Verified is true for a K1 class whose enemyice offset and
	// enemy_ice call the decomp shows for GZLE01 (a matching file), or a
	// live check confirmed. The NonMatching stubs (PH, PT, PW, BO) and
	// Darknut, whose file matches D44J01 only (configure.py:1858), were
	// verified by the live check K1 (docs/08 §8.2) on the owner's game on
	// 2026-10-15: a one-byte 1 at the class's timer shrank and deleted
	// each (Appendix C item 141). A class still false is held by M6's
	// applier (§7.2).
	K1Verified bool `json:"-"`

	bossIf func(params uint32, angle [3]int16) bool // for BossParams
}

// IsBoss reports whether the game gives an actor of the class placed with
// params and angle BOSS status on creation. A BossUnknown class reports
// false.
func (c Class) IsBoss(params uint32, angle [3]int16) bool {
	switch c.Boss {
	case BossAlways:
		return true
	case BossParams:
		return c.bossIf(params, angle)
	}
	return false
}

// The procs the table uses that wwapi/stage has no constant for (values
// from the f_pc_name.h comments, as wwapi/stage's Proc explains).
const (
	ProcSHAND   stage.Proc = 0x065 // fpcNm_SHAND_e, f_pc_name.h:110
	ProcOSHIP   stage.Proc = 0x0B4 // fpcNm_OSHIP_e, f_pc_name.h:189
	ProcBWDS    stage.Proc = 0x0DB // fpcNm_BWDS_e, f_pc_name.h:231
	ProcBWDG    stage.Proc = 0x0DC // fpcNm_BWDG_e, f_pc_name.h:232
	ProcBMDHAND stage.Proc = 0x0ED // fpcNm_BMDHAND_e, f_pc_name.h:249
	ProcBMDFOOT stage.Proc = 0x0EE // fpcNm_BMDFOOT_e, f_pc_name.h:250
	ProcFGMAHOU stage.Proc = 0x0F4 // fpcNm_FGMAHOU_e, f_pc_name.h:256
	ProcSS      stage.Proc = 0x0FD // fpcNm_SS_e, f_pc_name.h:265
	ProcSSS     stage.Proc = 0x0FE // fpcNm_SSS_e, f_pc_name.h:266
	ProcSITEM   stage.Proc = 0x0FF // fpcNm_SITEM_e, f_pc_name.h:267
	ProcNPCJI1  stage.Proc = 0x140 // fpcNm_NPC_JI1_e, f_pc_name.h:332
	ProcATT     stage.Proc = 0x1C6 // fpcNm_ATT_e, f_pc_name.h:466
)

// deathSwitchBoss is the rule ST and MO2 share, and TN's first half: BOSS
// when the u8 death switch read from current.angle z (the entry's angle:
// home.angle is copied from it and current from home, f_op_actor.cpp:273,
// :283) is a memory switch, 1-0x7F.
func deathSwitchBoss(angle [3]int16) bool {
	sw := uint8(angle[2])
	return sw != 0 && sw <= 0x7F
}

// Classes are the enemy classes, in proc order. The procs wwapi/stage
// already names are its constants (with their f_pc_name.h lines there).
var Classes = []Class{
	// d_a_shand.cpp:1-4 calls it an object; group d_a_shand.cpp:538. Only
	// Forbidden Woods' kokiie creates it (d_a_kokiie.cpp:190); it has no
	// l_objectName entry. Matching (configure.py:1624).
	{Proc: ProcSHAND, Name: "SHAND", What: "Forbidden Woods tentacle", Group: GroupEnemy, Kind: Other, Death: DeathNone, Boss: BossNever},
	// Group d_a_fm.cpp:3492; onActor :2097; enemy_ice :2996; enemyice
	// d_a_fm.h:213.
	{Proc: stage.ProcFM, Name: "FM", What: "Floormaster", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0xAE8, K1Verified: true, Boss: BossNever},
	// Group d_a_oship.cpp:1106. Matching (configure.py:1836).
	{Proc: ProcOSHIP, Name: "OSHIP", What: "Warship", Group: GroupEnemy, Kind: Other, Death: DeathNone, Boss: BossNever},
	// Group d_a_bb.cpp:2851; no onActor of its own; enemy_ice :2213;
	// enemyice d_a_bb.h:106. daBb_Create raises home.pos 70 above the
	// entry (:2710-2711).
	{Proc: stage.ProcBB, Name: "BB", What: "Kargaroc", Group: GroupEnemy, Kind: K1, Death: DeathIce, Ice: 0xC80, K1Verified: true, Boss: BossNever, HomeMoves: true},
	// Group d_a_mo2.cpp:4257; onActor :2411; enemy_ice :3380; enemyice
	// d_a_mo2.h:175 (mDeathSwitch u8, :42). BOSS: daMo2_Create, :4093,
	// :4099-4100.
	{Proc: stage.ProcMO2, Name: "MO2", What: "Moblin", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x2A54, K1Verified: true, Boss: BossParams,
		bossIf: func(_ uint32, angle [3]int16) bool { return deathSwitchBoss(angle) }},
	// Group d_a_bk.cpp:5337; onActor :2648; enemy_ice :4159; enemyice
	// d_a_bk.h:140. BOSS: daBk_Create reads its type from params & 0xF
	// (:4759; 0xB becomes 4, :4762-4766) and gives type 0xA the status
	// (:5030-5034). On a searchlight, stand2 keeps home.pos on the light
	// (:1455-1463).
	{Proc: stage.ProcBK, Name: "BK", What: "Bokoblin", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x1264, K1Verified: true, Boss: BossParams, HomeMoves: true,
		bossIf: func(params uint32, _ [3]int16) bool { return params&0xF == 0xA }},
	// Group d_a_st.cpp:2944; onActor :1239, :2220; enemy_ice :2524;
	// enemyice d_a_st.h:145 (mDeathSwitch u8, :53). BOSS: daSt_Create,
	// :2836, :2839-2841.
	{Proc: stage.ProcST, Name: "ST", What: "Stalfos", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x1FA8, K1Verified: true, Boss: BossParams,
		bossIf: func(_ uint32, angle [3]int16) bool { return deathSwitchBoss(angle) }},
	// Group d_a_tn.cpp:4405; onActor :2073; enemy_ice :3433; enemyice
	// d_a_tn.h:162 (mBehaviorType u8, :33; mDisableSpawnOnDeathSwitch u8,
	// :37). BOSS in daTn_Create: a death switch of 1-0x7F (:4212-4214;
	// REG0_S(0) there is a debug register, 0 in play, inferred), or
	// behaviour params & 0xF (:4207) BEHAVIOR_MINIBOSS 13 or
	// BEHAVIOR_MSWORD_CHAMBER_AMBUSH 14 (:48-49, :4244-4245). The file
	// matches D44J01 only (configure.py:1858), so this is GZLE01 by
	// inference. K1 live check on the owner's game, 2026-10-15: enemy-kill
	// on a kaze r8 Darknut (set 0) ran the timer at +0x152A to 91, deleted
	// it, recorded zone slot 0 setID 0 dead, and its sword fell once its
	// CARRY was cleared.
	{Proc: stage.ProcTN, Name: "TN", What: "Darknut", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x1524, K1Verified: true, Boss: BossParams,
		bossIf: func(params uint32, angle [3]int16) bool {
			b := params & 0xF
			return deathSwitchBoss(angle) || b == 13 || b == 14
		}},
	// Group d_a_nz.cpp:231. NonMatching stub (configure.py:1749).
	{Proc: stage.ProcNZ, Name: "NZ", What: "Rat", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_am.cpp:1397; onActor :954; enemy_ice :977; enemyice
	// d_a_am.h:55.
	{Proc: stage.ProcAM, Name: "AM", What: "Armos Knight", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0xABC, K1Verified: true, Boss: BossNever},
	// Group d_a_am2.cpp:1528; onActor :970; enemy_ice :1120; enemyice
	// d_a_am2.h:57.
	{Proc: stage.ProcAM2, Name: "AM2", What: "Armos", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0xA8C, K1Verified: true, Boss: BossNever},
	// Group d_a_gm.cpp:272; "Mini-Boss - Mothula / Enemy - Mothula Larva"
	// (:3). NonMatching stub (configure.py:1672).
	{Proc: stage.ProcGM, Name: "GM", What: "Mothula", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group fopAc_ENV_e, d_a_ks.cpp:1788; no onActor of its own; enemy_ice
	// :1337; enemyice d_a_ks.h:51.
	{Proc: stage.ProcKS, Name: "KS", What: "Morth", Group: GroupEnv, Kind: K1, Death: DeathIce, Ice: 0x6A8, K1Verified: true, Boss: BossNever},
	// Group d_a_cc.cpp:2792; onActor :308, :316, :347, :1402, :1453,
	// :1972; enemy_ice :2124; enemyice d_a_cc.h:92. Its light-shrink
	// write (one byte 0x01 at +0x822) was seen to shrink and delete it on
	// the owner's game (docs/08 Appendix C item 141).
	{Proc: stage.ProcCC, Name: "CC", What: "ChuChu", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x81C, K1Verified: true, Boss: BossNever},
	// Group d_a_bl.cpp:209. NonMatching stub (configure.py:1649).
	{Proc: stage.ProcBL, Name: "BL", What: "Bubble", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_wz.cpp:224; "Enemy - Wizzrobe / Mini-Boss - Wizzrobe"
	// (:3). NonMatching stub (configure.py:1868).
	{Proc: stage.ProcWZ, Name: "WZ", What: "Wizzrobe", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_ph.cpp:282; enemyice d_a_ph.h:68. NonMatching stub
	// (configure.py:1837). K1 live check on the owner's game, 2026-10-15:
	// a one-byte 1 at +0x99E deleted a kaze r2 Peahat (set 2) and its zone
	// recorded actor 2 dead (enemy_ice's onActor).
	{Proc: stage.ProcPH, Name: "PH", What: "Peahat/Seahat", Group: GroupEnemy, Kind: K1, Death: DeathUnknown, Ice: 0x998, K1Verified: true, Boss: BossUnknown},
	// Group d_a_bpw.cpp:4737; BOSS in body_create_init, :4391.
	// NonMatching (configure.py:1655) but every function decompiled.
	{Proc: stage.ProcBPW, Name: "BPW", What: "Jalhalla", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_pw.cpp:569; enemyice d_a_pw.h:84. NonMatching, 26 of its
	// 31 functions stubs (configure.py:1839). K1 live check on the
	// owner's game, 2026-10-15: a one-byte 1 at +0x88A (SubD71 r1) ran the
	// timer to 86, then the Poe burst and dropped a heart.
	{Proc: stage.ProcPW, Name: "PW", What: "Poe", Group: GroupEnemy, Kind: K1, Death: DeathUnknown, Ice: 0x884, K1Verified: true, Boss: BossUnknown},
	// Group d_a_bo.cpp:246; enemyice d_a_bo.h:15. NonMatching stub
	// (configure.py:1653). K1 live check on the owner's game, 2026-10-15:
	// a one-byte 1 at +0xA02 (kindan r1) ran the timer to 89 and deleted
	// it.
	{Proc: stage.ProcBO, Name: "BO", What: "Boko Baba", Group: GroupEnemy, Kind: K1, Death: DeathUnknown, Ice: 0x9FC, K1Verified: true, Boss: BossUnknown},
	// Group d_a_ki.cpp:1525; onActor :871; enemy_ice :951; enemyice
	// d_a_ki.h:85. ki_atack_move moves home.pos to the ceiling above it
	// (:676-682).
	{Proc: stage.ProcKI, Name: "KI", What: "Keese", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x924, K1Verified: true, Boss: BossNever, HomeMoves: true},
	// Group d_a_mt.cpp:215. NonMatching stub (configure.py:1693).
	{Proc: stage.ProcMT, Name: "MT", What: "Magtail", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_bwd.cpp:2607; BOSS in the profile, :2606.
	{Proc: stage.ProcBWD, Name: "BWD", What: "Molgera", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bwds.cpp:1094; Molgera also creates them (d_a_bwd.cpp:612).
	{Proc: ProcBWDS, Name: "BWDS", What: "Molgera Larva", Group: GroupEnemy, Kind: Other, Death: DeathNone, Boss: BossNever},
	// Group d_a_bwdg.cpp:268. No description; only Molgera creates it
	// (d_a_bwd.cpp:2172).
	{Proc: ProcBWDG, Name: "BWDG", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossNever},
	// Group d_a_rd.cpp:1981; onActor :845; enemy_ice :1662; enemyice
	// d_a_rd.h:172.
	{Proc: stage.ProcRD, Name: "RD", What: "ReDead", Group: GroupEnemy, Kind: K1, Death: DeathOwn, Ice: 0x6E0, K1Verified: true, Boss: BossNever},
	// Group d_a_daiocta.cpp:1772; no BOSS status (profile :1771).
	{Proc: stage.ProcDaiocta, Name: "DAIOCTA", What: "Big Octo", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossNever},
	// Group d_a_oq.cpp:251. NonMatching stub (configure.py:1620).
	{Proc: stage.ProcOQ, Name: "OQ", What: "Octorok", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_gy.cpp:357. NonMatching stub (configure.py:1675). GyCtrl
	// creates them at runtime (d_a_gy_ctrl.cpp:345), with setID 0xFFFF.
	{Proc: stage.ProcGY, Name: "GY", What: "Gyorg", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_btd.cpp:3042; BOSS in the profile, :3041.
	{Proc: stage.ProcBTD, Name: "BTD", What: "Gohma", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bmd.cpp:2056; BOSS in the profile, :2055. daBmd_Create
	// raises home.pos (:2001).
	{Proc: stage.ProcBMD, Name: "BMD", What: "Kalle Demos", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways, HomeMoves: true},
	// Group d_a_bmdhand.cpp:843; BOSS in the profile, :842.
	{Proc: ProcBMDHAND, Name: "BMDHAND", What: "Kalle Demos ceiling tentacle", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bmdfoot.cpp:840; BOSS in the profile, :839. NonMatching
	// (configure.py:1651) but every function decompiled.
	{Proc: ProcBMDFOOT, Name: "BMDFOOT", What: "Kalle Demos floor tentacle", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bdk.cpp:4292; BOSS in the profile, :4291. fly sets
	// home.pos (:680-690).
	{Proc: stage.ProcBDK, Name: "BDK", What: "Helmaroc King", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways, HomeMoves: true},
	// Group d_a_bst.cpp:3207; BOSS in the profile, :3206.
	{Proc: stage.ProcBST, Name: "BST", What: "Gohdan", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_fganon.cpp:2971; no BOSS status (profile :2970).
	{Proc: stage.ProcFganon, Name: "FGANON", What: "Phantom Ganon", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossNever},
	// Group d_a_fgmahou.cpp:449; one of Phantom Ganon's eight orbs
	// (d_a_fgmahou.h:14), which only it creates (d_a_fganon.cpp:573).
	{Proc: ProcFGMAHOU, Name: "FGMAHOU", What: "Phantom Ganon's orb", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossNever},
	// Group d_a_bgn.cpp:3712; BOSS in the non-demo profile, :3711.
	{Proc: stage.ProcBGN, Name: "BGN", What: "Puppet Ganon (phase 1)", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bgn2.cpp:1275; BOSS in the profile, :1274.
	{Proc: stage.ProcBGN2, Name: "BGN2", What: "Puppet Ganon (phase 2)", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_bgn3.cpp:1355; BOSS in the profile, :1354.
	{Proc: stage.ProcBGN3, Name: "BGN3", What: "Puppet Ganon (phase 3)", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossAlways},
	// Group d_a_gnd.cpp:334; BOSS in the profile, :333. NonMatching stub
	// (configure.py:1673).
	{Proc: stage.ProcGND, Name: "GND", What: "Ganondorf", Group: GroupEnemy, Kind: Boss, Death: DeathUnknown, Boss: BossAlways},
	// Group d_a_pt.cpp:197; enemyice d_a_pt.h:73. NonMatching stub
	// (configure.py:1838). K1 live check on the owner's game, 2026-10-15:
	// a one-byte 1 at +0x7EA (SubD71 r0) ran the timer to 49, deleted it
	// and dropped a rupee.
	{Proc: stage.ProcPT, Name: "PT", What: "Miniblin", Group: GroupEnemy, Kind: K1, Death: DeathUnknown, Ice: 0x7E4, K1Verified: true, Boss: BossUnknown},
	// Group d_a_ss.cpp:135. No description; NonMatching stub
	// (configure.py:1629).
	{Proc: ProcSS, Name: "SS", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_sss.cpp:797.
	{Proc: ProcSSS, Name: "SSS", What: "Dexivine", Group: GroupEnemy, Kind: Other, Death: DeathNone, Boss: BossNever},
	// Group d_a_sitem.cpp:167. No description; NonMatching stub
	// (configure.py:1628). "Sitem" names it (d_stage.cpp:920), before
	// the shop item's "Sitem" (:1256), so a placed "Sitem" is this.
	{Proc: ProcSITEM, Name: "SITEM", Group: GroupEnemy, Kind: Other, Death: DeathUnknown, Boss: BossUnknown},
	// Group d_a_npc_ji1.cpp:5532: Orca, the swordsman (:3). NonMatching
	// (configure.py:1716) but every function decompiled.
	{Proc: ProcNPCJI1, Name: "NPC_JI1", What: "Orca", Group: GroupEnemy, Kind: Other, Death: DeathNone, Boss: BossNever},
	// Group d_a_att.cpp:284. No description; Molgera, Puppet Ganon and
	// Gohdan create it (d_a_bwd.cpp:2581, d_a_bgn.cpp:3668,
	// d_a_bst.cpp:3163).
	{Proc: ProcATT, Name: "ATT", Group: GroupEnemy, Kind: Boss, Death: DeathNone, Boss: BossNever},
}

// classByProc indexes Classes.
var classByProc = func() map[stage.Proc]int {
	m := make(map[stage.Proc]int, len(Classes))
	for i, c := range Classes {
		m[c.Proc] = i
	}
	return m
}()

// ClassOf returns proc's class, if proc is an enemy.
func ClassOf(p stage.Proc) (Class, bool) {
	i, ok := classByProc[p]
	if !ok {
		return Class{}, false
	}
	return Classes[i], true
}

// IsEnemy reports whether proc is an enemy.
func IsEnemy(p stage.Proc) bool {
	_, ok := classByProc[p]
	return ok
}

// K1Classes returns the K1 classes in proc order.
func K1Classes() []Class {
	return slices.DeleteFunc(slices.Clone(Classes), func(c Class) bool { return c.Kind != K1 })
}
