package switches

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// The decoders below read every actor the decomp shows writing a switch
// (every fopAcM_onSwitch, fopAcM_offSwitch, fopAcM_revSwitch and
// dComIfGs_onSwitch/offSwitch/revSwitch call under src/d/actor, plus
// keyInit in d_door.cpp and the enemy_ice death switch in
// c_damagereaction.cpp), and a few common readers. Not covered: the
// switch a stage EVNT entry sets as its event starts
// (dEvent_exception_c::setStartDemo, d_event_manager.cpp:23-48), since
// stagedata keeps no EVNT entries. A path such as
// "d_a_swhit0.cpp:72-75" is a decomp source line; params is
// fopAcM_GetParam, the entry's Params, and home.angle is the entry's
// Angle (dStage_actorCreate, d_stage.cpp:1300-1310). daObj::PrmAbstract
// (width, shift) is (params >> shift) & ((1 << width) - 1)
// (d_a_obj.h:11-15).
//
// The room a switch is routed with is the actor's own unless a use says
// otherwise: fopAcM_onSwitch passes the home room
// (f_op_actor_mng.h:366-379) and the dComIfGs_ calls here pass
// fopAcM_GetRoomNo, which is the same room for these actors. The game
// creates stage.dzs entries with room -1 (stagedata.Placed), which
// routes memory and dan switches but not zone ones.

// The procs the decoders key on that stagedata doesn't name, as GZLE01
// numbers them (the fpcNm_*_e value at that line of f_pc_name.h, as
// stagedata's Proc constants are given), with the l_objectName names that
// create them (d_stage.cpp lines).
const (
	procSwflat      stagedata.Proc = 0x01F // fpcNm_Obj_Swflat_e, f_pc_name.h:40; "Hfbot1A".."Hfbot1C" (d_stage.cpp:960-962)
	procSwlight     stagedata.Proc = 0x020 // fpcNm_Obj_Swlight_e, f_pc_name.h:41; "MsuSW", "MsuSWB" (d_stage.cpp:631-632)
	procSwhammer    stagedata.Proc = 0x021 // fpcNm_Obj_Swhammer_e, f_pc_name.h:42; "MhmrSW0" (d_stage.cpp:569)
	procTagVolcano  stagedata.Proc = 0x024 // fpcNm_Tag_Volcano_e, f_pc_name.h:45; "VolTag" (d_stage.cpp:973)
	procHmlif       stagedata.Proc = 0x02A // fpcNm_Hmlif_e, f_pc_name.h:51; "Hmlif", "Hyuf1", "Hyuf2" (d_stage.cpp:905, 1021-1022)
	procTribox      stagedata.Proc = 0x02E // fpcNm_Obj_Tribox_e, f_pc_name.h:55; "MtryB", "MtryBCr" (d_stage.cpp:1081-1082)
	procHlift       stagedata.Proc = 0x030 // fpcNm_Obj_Hlift_e, f_pc_name.h:57; "Hlift", "Hliftb" (d_stage.cpp:869-870)
	procHami2       stagedata.Proc = 0x031 // fpcNm_Obj_Hami2_e, f_pc_name.h:58; "Hami2" (d_stage.cpp:871)
	procHami3       stagedata.Proc = 0x032 // fpcNm_Obj_Hami3_e, f_pc_name.h:59; "Hami3" (d_stage.cpp:872)
	procVolcano     stagedata.Proc = 0x035 // fpcNm_Obj_Volcano_e, f_pc_name.h:62; "Ykzyg" (d_stage.cpp:963)
	procVmc         stagedata.Proc = 0x036 // fpcNm_Obj_Vmc_e, f_pc_name.h:63; "VmcBS" (d_stage.cpp:1094)
	procVfan        stagedata.Proc = 0x038 // fpcNm_Obj_Vfan_e, f_pc_name.h:65; "Vfan" (d_stage.cpp:1096)
	procDragonhead  stagedata.Proc = 0x03A // fpcNm_Obj_Dragonhead_e, f_pc_name.h:67; "Qdghd" (d_stage.cpp:965)
	procCanon       stagedata.Proc = 0x041 // fpcNm_OBJ_CANON_e, f_pc_name.h:74; "Ocanon" (d_stage.cpp:819)
	procWarpt       stagedata.Proc = 0x043 // fpcNm_OBJ_WARPT_e, f_pc_name.h:76; "Warpt".."Warpts3" (d_stage.cpp:605-609)
	procMjdoor      stagedata.Proc = 0x047 // fpcNm_OBJ_MJDOOR_e, f_pc_name.h:80; "MjDoor" (d_stage.cpp:829), d_a_obj_majyuu_door.cpp
	procMkie        stagedata.Proc = 0x04D // fpcNm_Obj_Mkie_e, f_pc_name.h:86; "MkieBA", "MkieBAB" (d_stage.cpp:949-950)
	procMkiek       stagedata.Proc = 0x04E // fpcNm_Obj_Mkiek_e, f_pc_name.h:87; "MkieK" (d_stage.cpp:951)
	procMknjD       stagedata.Proc = 0x04F // fpcNm_Obj_MknjD_e, f_pc_name.h:88; "MknjD" (d_stage.cpp:952)
	procMsdan       stagedata.Proc = 0x050 // fpcNm_Obj_Msdan_e, f_pc_name.h:89; "Msdan" (d_stage.cpp:953)
	procLadder      stagedata.Proc = 0x057 // fpcNm_Obj_Ladder_e, f_pc_name.h:96; "Mhsg6".."Mhsg4h" (d_stage.cpp:910-914)
	procAshut       stagedata.Proc = 0x05A // fpcNm_Obj_Ashut_e, f_pc_name.h:99; "Ashut" (d_stage.cpp:1092)
	procMflft       stagedata.Proc = 0x05D // fpcNm_MFLFT_e, f_pc_name.h:102; "Mflft" (d_stage.cpp:875)
	procFloor       stagedata.Proc = 0x062 // fpcNm_FLOOR_e, f_pc_name.h:107; "Hhyu1" (d_stage.cpp:637)
	procKokiie      stagedata.Proc = 0x064 // fpcNm_KOKIIE_e, f_pc_name.h:109; "Kokiie" (d_stage.cpp:888)
	procShutter     stagedata.Proc = 0x072 // fpcNm_SHUTTER_e, f_pc_name.h:123; "Htobi1", "Htobi2" (d_stage.cpp:957-958)
	procMbdoor      stagedata.Proc = 0x075 // fpcNm_MBDOOR_e, f_pc_name.h:126; "SMBdor", "KGBdor" (d_stage.cpp:943-944)
	procFerris      stagedata.Proc = 0x07D // fpcNm_Obj_Ferris_e, f_pc_name.h:134; "Skanran" (d_stage.cpp:1110)
	procHomen       stagedata.Proc = 0x083 // fpcNm_Obj_Homen_e, f_pc_name.h:140; "Homen1", "Homen2" (d_stage.cpp:1120-1121)
	procVds         stagedata.Proc = 0x086 // fpcNm_Obj_Vds_e, f_pc_name.h:143; "Vds" (d_stage.cpp:1148)
	procAjav        stagedata.Proc = 0x088 // fpcNm_Obj_Ajav_e, f_pc_name.h:145; "Ajav" (d_stage.cpp:1111)
	procHtetu1      stagedata.Proc = 0x08B // fpcNm_Obj_Htetu1_e, f_pc_name.h:148; "Htetu1" (d_stage.cpp:1114)
	procLeaves      stagedata.Proc = 0x092 // fpcNm_Obj_Leaves_e, f_pc_name.h:155; "Vochi" (d_stage.cpp:1123)
	procHcbh        stagedata.Proc = 0x098 // fpcNm_Obj_Hcbh_e, f_pc_name.h:161; "Hcbh" (d_stage.cpp:1166)
	procShmrgrd     stagedata.Proc = 0x09B // fpcNm_Obj_Shmrgrd_e, f_pc_name.h:164; "Shmrgrd" (d_stage.cpp:1171)
	procKanoke      stagedata.Proc = 0x0A1 // fpcNm_Obj_Kanoke_e, f_pc_name.h:170; "MKanoke", "MKanok2" (d_stage.cpp:999-1000)
	procEskban      stagedata.Proc = 0x0A2 // fpcNm_Obj_Eskban_e, f_pc_name.h:171; "Eskban" (d_stage.cpp:994)
	procEkskz       stagedata.Proc = 0x0A3 // fpcNm_Obj_Ekskz_e, f_pc_name.h:172; "Ekskz" (d_stage.cpp:995)
	procEbomzo      stagedata.Proc = 0x0A4 // fpcNm_Obj_Ebomzo_e, f_pc_name.h:173; "Ebomzo" (d_stage.cpp:1118)
	procOship       stagedata.Proc = 0x0B4 // fpcNm_OSHIP_e, f_pc_name.h:189; "Oship" (d_stage.cpp:816)
	procKui         stagedata.Proc = 0x0FC // fpcNm_KUI_e, f_pc_name.h:264; "Kui" (d_stage.cpp:645)
	procFlame       stagedata.Proc = 0x10D // fpcNm_Obj_Flame_e, f_pc_name.h:281; "Yfire00" (d_stage.cpp:788)
	procTenmado     stagedata.Proc = 0x119 // fpcNm_Obj_Tenmado_e, f_pc_name.h:293; "Tenmado" (d_stage.cpp:992)
	procFirewall    stagedata.Proc = 0x120 // fpcNm_Obj_Firewall_e, f_pc_name.h:300; "Yswdr00" (d_stage.cpp:1100)
	procTnTrap      stagedata.Proc = 0x121 // fpcNm_Obj_TnTrap_e, f_pc_name.h:301; "TnTrap", "FgTrap", "CmTrap" (d_stage.cpp:1101-1103)
	procTapestry    stagedata.Proc = 0x123 // fpcNm_Obj_Tapestry_e, f_pc_name.h:303; "Mcrtn" (d_stage.cpp:1093)
	procHsh         stagedata.Proc = 0x124 // fpcNm_Obj_HSH_e, f_pc_name.h:304; "Hsh", "Hsh2" (d_stage.cpp:1138-1139), d_a_obj_hsehi1.cpp
	procBombFlower  stagedata.Proc = 0x12A // fpcNm_BOMB_FLOWER_e, f_pc_name.h:310; "BFlower", "VbakH" (d_stage.cpp:945-946)
	procKddoor      stagedata.Proc = 0x132 // fpcNm_KDDOOR_e, f_pc_name.h:318; "doorKD", "ZenshKD", "K_ZshKD", "doorSH", "doorSZ" (d_stage.cpp:460-464)
	procSwAttack    stagedata.Proc = 0x138 // fpcNm_SW_ATTACK_e, f_pc_name.h:324; "SWat00" (d_stage.cpp:633)
	procSwTact      stagedata.Proc = 0x13A // fpcNm_SW_TACT_e, f_pc_name.h:326; "SWtact", "SWtactB" (d_stage.cpp:635-636)
	procNpcOs       stagedata.Proc = 0x13C // fpcNm_NPC_OS_e, f_pc_name.h:328; "Os", "Os1", "Os2" (d_stage.cpp:704-706)
	procNpcP2       stagedata.Proc = 0x145 // fpcNm_NPC_P2_e, f_pc_name.h:337; "P2a".."P2c" (d_stage.cpp:852-854)
	procNpcKk1      stagedata.Proc = 0x163 // fpcNm_NPC_KK1_e, f_pc_name.h:367; "Kk1" (d_stage.cpp:746)
	procNpcHr       stagedata.Proc = 0x16F // fpcNm_NPC_HR_e, f_pc_name.h:379; "Hr", "Hr2" (d_stage.cpp:1004-1005)
	procBigelf      stagedata.Proc = 0x173 // fpcNm_BIGELF_e, f_pc_name.h:383; "BigElf" (d_stage.cpp:1006)
	procNpcMn       stagedata.Proc = 0x17E // fpcNm_NPC_MN_e, f_pc_name.h:394; "Mn" (d_stage.cpp:1019)
	procItnak       stagedata.Proc = 0x18E // fpcNm_Obj_Itnak_e, f_pc_name.h:410; "Itnak" (d_stage.cpp:1144)
	procSaku        stagedata.Proc = 0x191 // fpcNm_SAKU_e, f_pc_name.h:413; "Ksaku", "Dsaku" (d_stage.cpp:795, 861)
	procTagEvent    stagedata.Proc = 0x19C // fpcNm_TAG_EVENT_e, f_pc_name.h:424; "TagEv" (d_stage.cpp:574)
	procTagHint     stagedata.Proc = 0x19D // fpcNm_TAG_HINT_e, f_pc_name.h:425; "TagHt", "TagHt2" (d_stage.cpp:575-576)
	procTagMsg      stagedata.Proc = 0x19E // fpcNm_TAG_MSG_e, f_pc_name.h:426; "TagMsg", "TagMsg2" (d_stage.cpp:577-578)
	procTagIsland   stagedata.Proc = 0x1A0 // fpcNm_TAG_ISLAND_e, f_pc_name.h:428; "TagIsl" (d_stage.cpp:579)
	procTagKbItem   stagedata.Proc = 0x1A4 // fpcNm_TAG_KB_ITEM_e, f_pc_name.h:432; "TagKb" (d_stage.cpp:583)
	procLightBridge stagedata.Proc = 0x1AF // fpcNm_LIGHTBRIDGE_e, f_pc_name.h:443; "Gbrg00" (d_stage.cpp:909)
	procWall        stagedata.Proc = 0x1B1 // fpcNm_WALL_e, f_pc_name.h:445; "Wall" (d_stage.cpp:916)
	procSwPropeller stagedata.Proc = 0x1B2 // fpcNm_SW_PROPELLER_e, f_pc_name.h:446; "Hpbot1", "Vpbot" (d_stage.cpp:621-622)
	procAgbsw0      stagedata.Proc = 0x1B6 // fpcNm_AGBSW0_e, f_pc_name.h:450; "agbA".."agbFA" (d_stage.cpp:503-515)
	procHys         stagedata.Proc = 0x1C8 // fpcNm_Hys_e, f_pc_name.h:468; "Hys", "Hys2" (d_stage.cpp:612-613)
	procStone       stagedata.Proc = 0x1CC // fpcNm_STONE_e, f_pc_name.h:472; "koisi1", "koisi2" (d_stage.cpp:532-533)
	procStone2      stagedata.Proc = 0x1CD // fpcNm_Stone2_e, f_pc_name.h:473; "Ebrock", "Ekao", "Ebrock2" (d_stage.cpp:654-656)
	procTry         stagedata.Proc = 0x1D0 // fpcNm_Obj_Try_e, f_pc_name.h:477; "Hseki1".."Hseki52" (d_stage.cpp:925-939)
	procIce         stagedata.Proc = 0x1D2 // fpcNm_Obj_Ice_e, f_pc_name.h:479; "Ikori" (d_stage.cpp:630)
	procCorrect     stagedata.Proc = 0x1D8 // fpcNm_Obj_Correct_e, f_pc_name.h:485; "BLK_CR".."CrTrBl" (d_stage.cpp:556-563)
	procTagLight    stagedata.Proc = 0x1DB // fpcNm_Tag_Light_e, f_pc_name.h:488; "LTag0", "LTag1", "LTagR0" (d_stage.cpp:625-627)
)

// MaxANDInputs caps the inputs of one AND actor this table expands. The
// game takes up to 254 (an 8-bit count, d_a_andsw0.cpp:329;
// d_a_andsw2.cpp:52-55); an actor with more is recorded with an Unknown
// setter and its inputs aren't listed, which keeps the table's size
// proportional to its input.
const MaxANDInputs = 32

// agbsw0Types is the length of daAgbsw0_c::execute's ExeSubTable
// (d_a_agbsw0.cpp:279-295), types 0 (A) to 0xE (M3).
const agbsw0Types = 15

// entry is a placed actor as the decoders read it.
type entry struct{ stagedata.Actor }

// bits is daObj::PrmAbstract(width, shift).
func (e entry) bits(shift, width uint) int { return int(e.Params>>shift) & (1<<width - 1) }

// byte is the 8-bit params field at shift: (params >> shift) & 0xFF, the
// u8 most getters return.
func (e entry) byte(shift uint) int { return e.bits(shift, 8) }

// angle is home.angle's component i (0 x, 1 y, 2 z) as an unsigned 16-bit
// field.
func (e entry) angle(i int) int { return int(uint16(e.Angle[i])) }

// angleByte is angle(i) & 0xFF.
func (e entry) angleByte(i int) int { return e.angle(i) & 0xFF }

// use is one switch an actor writes or reads.
type use struct {
	no      int
	room    int  // the room the game routes it with, when roomSet
	roomSet bool // false: the actor's own room
	write   bool
	op      Op
	class   Class
	why     string
	inputs  []int // an AND's inputs: the output is synced only if they all are
	path    int   // a push block's path id when the write stores a path point; -1 otherwise
}

func set(no int, c Class, why string) use {
	return use{no: no, write: true, op: OpSet, class: c, why: why, path: -1}
}

// setClear is a writer that sets and clears, so never syncs.
func setClear(no int, why string) use {
	return use{no: no, write: true, op: OpSetClear, class: Unsafe, why: why, path: -1}
}

func clear(no int, why string) use {
	return use{no: no, write: true, op: OpClear, class: Unsafe, why: why, path: -1}
}

func toggle(no int, why string) use {
	return use{no: no, write: true, op: OpToggle, class: Unsafe, why: why, path: -1}
}

func read(no int, why string) use { return use{no: no, why: why, path: -1} }

// and is an AND output: set (op OpSet) or set and cleared (OpSetClear)
// from inputs.
func and(no int, op Op, c Class, why string, inputs []int) use {
	return use{no: no, write: true, op: op, class: c, why: why, inputs: inputs, path: -1}
}

// as gives the use class c.
func (u use) as(c Class) use {
	u.class = c
	return u
}

// in routes the use with room instead of the actor's own.
func (u use) in(room int) use {
	u.room, u.roomSet = room, true
	return u
}

// spec is how a proc's entries use switches.
type spec struct {
	proc stagedata.Proc
	name string // the fpcNm_*_e name without its affixes, for the report
	// decode returns the entry's switch uses. Uses of switch 0xFF (and -1)
	// are dropped by Build, as the game's setters return early for them
	// (d_save.cpp:1544-1546).
	decode func(e entry) []use
	// undecoded is set for a proc that takes switch params the decomp
	// doesn't decode yet; its entries are listed in Table.Undecoded.
	undecoded string
}

// fixed is a decoder for an actor that sets one params field and never
// clears it.
func fixed(c Class, why string, field func(e entry) int) func(entry) []use {
	return func(e entry) []use { return []use{set(field(e), c, why)} }
}

func prmByte(shift uint) func(e entry) int { return func(e entry) int { return e.byte(shift) } }
func angleByte(i int) func(e entry) int    { return func(e entry) int { return e.angleByte(i) } }

// stub is the reason for a NonMatching actor whose switch getters are
// empty declarations in its header.
func stub(file, getters string) string {
	return fmt.Sprintf("%s is NonMatching (configure.py) and its switch getters (%s) have no bodies, so its switch fields and behaviour are unknown", file, getters)
}

// specs lists every proc the table reads.
var specs = []spec{
	// Crystal switches. d_a_swhit0.cpp: getSwNo params & 0xFF (:72-75),
	// getTimer (u8)(params >> 20) with 0xFF meaning none (:87-96),
	// getSwNo2 home.angle.z & 0xFF (:98-101). Every type sets the switch
	// when hit (actionOffWait :243-269, or through its event, DemoProc
	// :228-232); type 2 rearms when the switch is cleared but still only
	// sets (:328-334). A timer (getTimer() * 15 frames, :337-340) clears
	// it again unless SwNo2 is on (actionOnTimer :342-365).
	{proc: stagedata.ProcSwhit0, name: "SWHIT0", decode: func(e entry) []use {
		sw, timer := e.byte(0), e.byte(20)
		if timer == 0xFF {
			timer = 0
		}
		if timer == 0 {
			return []use{set(sw, Latching, "crystal switch: set when hit")}
		}
		return []use{
			setClear(sw, fmt.Sprintf("timed crystal switch: cleared after %d frames", timer*15)),
			read(e.angleByte(2), "stops the crystal's timer"),
		}
	}},

	// Torches. d_a_ep.cpp:643-652: type params & 0x3F (0x3F reads as 0),
	// m505 the timer (u8)(params >> 8) with 0xFF meaning none, m507
	// (u8)(params >> 16) a switch that stops the timer, mOnSwitchNo
	// params >> 24. Lit, it sets mOnSwitchNo (:216-227, through
	// m508 = mOnSwitchNo + 1, :686-688). A timer (m505 * 10 + 1 frames,
	// :223-225; untimed torches start at 20000, which never counts down,
	// :431-433, :690) clears it again unless m507 is on (:286-288).
	// Types 0 and 3 also clear it when doused: a water check over the
	// flame (dBgS_ObjGndChk_Spl, :476-484, then :311-319). The design
	// lists un-timed torches as latching, so they are; the douse clear
	// is noted in Why.
	{proc: stagedata.ProcEP, name: "EP", decode: func(e entry) []use {
		typ, timer, stop, sw := e.bits(0, 6), e.byte(8), e.byte(16), e.byte(24)
		if typ == 0x3F {
			typ = 0
		}
		if timer != 0xFF {
			return []use{
				setClear(sw, fmt.Sprintf("timed torch: cleared %d frames after it is lit", timer*10+1)),
				read(stop, "stops the torch's timer"),
			}
		}
		why := "torch: set when lit"
		if typ == 0 || typ == 3 {
			why += "; also cleared if water covers the flame (d_a_ep.cpp:311-319)"
		}
		return []use{set(sw, Latching, why)}
	}},

	// Floor switches. d_a_obj_swpush.h:63-90: type PrmAbstract(3, 24),
	// swSave PrmAbstract(8, 8), version PrmAbstract(1, 30), swSave2
	// home.angle.z & 0xFF for version 1 (prmZ_init, d_a_obj_swpush.cpp:
	// 118-128), else 0xFF. M_attr (:17-116) holds four types, indexed by
	// type without a check (d_a_obj_swpush.h:80):
	//
	//	0 STAY_PRESSED|OBEY_SAVE: set on press (mode_u_l :540-559), stays down
	//	1 UNK20: pops up when released and clears (mode_l_u :611-623)
	//	2 ON_IS_UP|STAY_PRESSED|OBEY_SAVE: clears on press (:547-548)
	//	3 REQ_HEAVY|STAY_PRESSED|OBEY_SAVE: as 0, pressed by a heavy load
	//
	// Type 1 stays down while swSave2 is on (mode_lower :570-600).
	{proc: stagedata.ProcSwpush, name: "Obj_Swpush", decode: func(e entry) []use {
		typ, sw := e.bits(24, 3), e.byte(8)
		swSave2 := 0xFF
		if e.bits(30, 1) >= 1 {
			swSave2 = e.angleByte(2)
		}
		switch typ {
		case 0:
			return []use{set(sw, Latching, "floor switch: set when pressed, stays down")}
		case 3:
			return []use{set(sw, Latching, "heavy floor switch: set when pressed, stays down")}
		case 1:
			return []use{setClear(sw, "momentary floor switch: cleared when released"), read(swSave2, "holds the floor switch down")}
		case 2:
			return []use{setClear(sw, "inverted floor switch: cleared when pressed")}
		}
		return []use{set(sw, Unknown, fmt.Sprintf("floor switch type %d is past M_attr's four entries", typ))}
	}},

	// Iron-boots switches. d_a_obj_swheavy.h:30-43: type PrmAbstract(3,
	// 24), swSave PrmAbstract(8, 8). M_attr (d_a_obj_swheavy.cpp:13-50),
	// four types indexed without a check (d_a_obj_swheavy.h:21): types 0
	// and 1 have no flags and pop back up, clearing (mode_l_u :387-391);
	// type 2 is IS_TOGGLE and revSwitches (mode_u_l :333-334); type 3 is
	// OBEY_SAVE|STAY_PRESSED|REQ_HEAVY and only sets (:336).
	{proc: stagedata.ProcSwheavy, name: "Obj_Swheavy", decode: func(e entry) []use {
		typ, sw := e.bits(24, 3), e.byte(8)
		switch typ {
		case 0, 1:
			return []use{setClear(sw, "momentary iron-boots switch: cleared when it pops back up")}
		case 2:
			return []use{toggle(sw, "toggling iron-boots switch: each press flips it")}
		case 3:
			return []use{set(sw, Latching, "iron-boots switch: set when pressed, stays down")}
		}
		return []use{set(sw, Unknown, fmt.Sprintf("iron-boots switch type %d is past M_attr's four entries", typ))}
	}},

	// Room-clear switch. d_a_alldie.cpp: getSwbit (u8)(params >> 8)
	// (:17-19), set once no enemy is left (:45); the actor deletes itself
	// at create when it is already on (:70).
	{proc: stagedata.ProcAllDie, name: "ALLDIE", decode: fixed(Latching, "room clear: set when no enemy is left", prmByte(8))},

	// AND switch 0. d_a_andsw0.cpp create (:327-344): num params & 0xFF,
	// behaviour (params >> 8) & 0xFF, first input (params >> 16) & 0xFF
	// (0xFF reads as 0), output params >> 24, timer (home.angle.z & 0xFF)
	// * 15, event home.angle.x. The inputs are first..first+num-1, or
	// output+1.. when first is 0 (:30-36). num 0xFF is the Forest Haven
	// special, which sets nothing (hajimarinomori_check :229-275, picked
	// at :300-304); num 0 never sets (its loop doesn't run).
	//
	//	0 one-shot: sets the output once all are on (ACT_ON_ALL :40-53)
	//	3 one-shot, 65 frames later (:55-57, ACT_TIMER_SET :129-135)
	//	2 timed: once one input is on, sets the output if all turn on
	//	  within the timer, else clears every input (ACT_TIMER :84-99,
	//	  ACT_TIMER2 :101-127)
	//	other: continuous, clears the output when one turns off
	//	  (ACT_OFF_ALL :59-82)
	//
	// The design lists every andsw0 as unsafe; the decomp makes types 0
	// and 3 one-shot.
	{proc: stagedata.ProcAndsw0, name: "ANDSW0", decode: func(e entry) []use {
		num, beh, first, out := e.byte(0), e.byte(8), e.byte(16), e.byte(24)
		if num == 0xFF || num == 0 {
			return nil
		}
		if num > MaxANDInputs {
			return []use{set(out, Unknown, fmt.Sprintf("AND of %d inputs, more than the %d this table expands", num, MaxANDInputs))}
		}
		if first == 0xFF || first == 0 {
			first = out + 1
		}
		inputs := make([]int, num)
		for i := range inputs {
			inputs[i] = first + i
		}
		switch beh {
		case 0:
			return []use{and(out, OpSet, Latching, "one-shot AND: set once its inputs are all on", inputs)}
		case 3:
			return []use{and(out, OpSet, Latching, "one-shot AND: set 65 frames after its inputs are all on", inputs)}
		case 2:
			u := []use{and(out, OpSet, Latching, "timed AND: set if its inputs all turn on before the timer runs out", inputs)}
			for _, in := range inputs {
				u = append(u, clear(in, fmt.Sprintf("timed AND 0x%02X: clears its inputs when the timer runs out", out)))
			}
			return u
		}
		return []use{and(out, OpSetClear, Unsafe, "continuous AND: cleared when an input turns off", inputs)}
	}},

	// AND switch 2. d_a_andsw2.cpp:25-69: event home.angle.x & 0xFF,
	// getSwbit (the output) (params >> 16) & 0xFF, getSwbit2 params >> 24,
	// type (params >> 8) & 0xFF, timer home.angle.z & 0xFF, num params &
	// 0xFF, and the first input getSwbit2, or getSwbit + 1 when that is
	// 0xFF. chkAllSw2 is never true for num 0 or 0xFF or no first input
	// (:71-83); create waits forever for an output of 0xFF or a type
	// other than 0 (one-off) or 1 (continuous) (:176-209). A timer only
	// delays the set (:89-93). The continuous type clears the output when
	// an input turns off (actionOff :150-157).
	{proc: stagedata.ProcAndsw2, name: "ANDSW2", decode: func(e entry) []use {
		num, typ, out, sw2 := e.byte(0), e.byte(8), e.byte(16), e.byte(24)
		top := sw2
		if top == 0xFF && out != 0xFF {
			top = out + 1
		}
		if out == 0xFF || num == 0 || num == 0xFF || top == 0xFF || (typ != 0 && typ != 1) {
			return nil
		}
		if num > MaxANDInputs {
			return []use{set(out, Unknown, fmt.Sprintf("AND of %d inputs, more than the %d this table expands", num, MaxANDInputs))}
		}
		inputs := make([]int, num)
		for i := range inputs {
			inputs[i] = top + i
		}
		if typ == 0 {
			return []use{and(out, OpSet, Latching, "one-shot AND: set once its inputs are all on", inputs)}
		}
		return []use{and(out, OpSetClear, Unsafe, "continuous AND: cleared when an input turns off", inputs)}
	}},

	// Switch timer. d_a_obj_timer.h:17-26: time PrmAbstract(8, 0), swSave
	// PrmAbstract(8, 16). Once the switch is on it counts time * 15
	// frames and clears it (mode_wait :36-41, mode_count :49-76).
	{proc: stagedata.ProcObjTimer, name: "Obj_Timer", decode: func(e entry) []use {
		return []use{clear(e.byte(16), fmt.Sprintf("switch timer: clears it %d frames after it turns on", e.byte(0)*15))}
	}},

	// Area switches. d_a_swc00.h:9-11: sw1 params & 0xFF, sw2 (params >>
	// 8) & 0xFF (an enable switch), type (params >> 16) & 3. Type 0 sets
	// sw1 while the local Link is inside and clears it while he is out,
	// every frame (d_a_swc00.cpp:11-33), and clears it at create
	// (:53-56). The other types set it once and delete themselves, and
	// don't create when it is already on (:23-25, :57-58). The design
	// lists every SW_C00 as unsafe; the decomp makes types 1-3 one-shot.
	{proc: stagedata.ProcSwc00, name: "SWC00", decode: func(e entry) []use {
		sw, enable := e.byte(0), e.byte(8)
		r := read(enable, "enables the area switch")
		if e.bits(16, 2) == 0 {
			return []use{setClear(sw, "area switch: set while the local Link is inside, cleared while he is out, every frame"), r}
		}
		return []use{set(sw, Latching, "one-shot area switch: set when the local Link walks in"), r}
	}},

	// Event-bit switches. d_a_tag_evsw.h:11-25: swSave PrmAbstract(8, 0),
	// eventbitID PrmAbstract(16, 8), type PrmAbstract(2, 24). At create
	// and every frame it sets the switch while the event bit is on and,
	// unless type 1, clears it while it is off (d_a_tag_evsw.cpp:10-48).
	// Type 1 never clears: the switch follows the event bit, which §6.5's
	// story batch carries, so it is classed Event.
	{proc: stagedata.ProcTagEvsw, name: "TAG_EVSW", decode: func(e entry) []use {
		sw, bit := e.byte(0), e.bits(8, 16)
		if e.bits(24, 2) == 1 {
			return []use{set(sw, Event, fmt.Sprintf("set while event bit 0x%04X is on", bit))}
		}
		return []use{setClear(sw, fmt.Sprintf("mirrors event bit 0x%04X every frame", bit))}
	}},

	// Doors (dDoor_info_c, d_door.cpp:15-61): getSwbit params & 0xFF,
	// getSwbit2 (params >> 20) & 0xFF, getType (params >> 8) & 0xF,
	// getFRoomNo home.angle.x & 0x3F, getBRoomNo (home.angle.x >> 6) &
	// 0x3F, getArg1 (home.angle.z >> 8) & 0xFF. A key door's keyInit sets
	// getSwbit as the key opens the lock, if it is below 0x80
	// (d_door.cpp:433-447). The kill-all bars set the switch of the side
	// Link is on, in that side's room, once the room has no enemies
	// (chkStopOpen); the other bars open when it is on.
	{proc: stagedata.ProcDoor10, name: "DOOR10", decode: func(e entry) []use {
		// d_a_door10.cpp: chkMakeKey types 4 and 5 small key, 1 boss
		// (:12-25), keyInit in demoProc (:568-570); chkStopOpen
		// (:117-172): kill-all front type 2, back type 5.
		typ := e.bits(8, 4)
		key := ""
		switch typ {
		case 4, 5:
			key = "small key door"
		case 1:
			key = "boss key door"
		}
		return doorUses(e, key, typ == 2, typ == 5)
	}},
	{proc: stagedata.ProcDoor12, name: "DOOR12", decode: func(e entry) []use {
		// d_a_door12.cpp: create makes it type 3 for getArg1 9 or 0xC
		// (:576-578); chkMakeKey type 1 small key, 3 boss (:38-49),
		// keyInit in demoProc (:620); chkStopOpen (:140-180): kill-all
		// front type 2 only.
		typ := e.bits(8, 4)
		if arg := e.angle(2) >> 8 & 0xFF; arg == 9 || arg == 0xC {
			typ = 3
		}
		key := ""
		switch typ {
		case 1:
			key = "small key door"
		case 3:
			key = "boss key door"
		}
		return doorUses(e, key, typ == 2, false)
	}},
	{proc: procKddoor, name: "KDDOOR", decode: func(e entry) []use {
		// d_a_kddoor.cpp: chkMakeKey type 2 (:10-13), keyInit in
		// demoProc (:770); chkGenocideCase front type 1, back types 2
		// and 4 (:84-99), set in chkStopOpen (:119-151).
		typ := e.bits(8, 4)
		key := ""
		if typ == 2 {
			key = "small key door"
		}
		return doorUses(e, key, typ == 1, typ == 2 || typ == 4)
	}},

	// Mini-boss doors. d_a_mbdoor.cpp: getSwbit params & 0xFF (:28-31),
	// getType home.angle.z & 0x3F (:33-36). Type 1 sets the switch once
	// its room has no enemies (checkUnlock :429-447); type 0 opens when
	// it is on (:433-434).
	{proc: procMbdoor, name: "MBDOOR", decode: func(e entry) []use {
		sw := e.byte(0)
		if e.angle(2)&0x3F == 1 {
			return []use{set(sw, Latching, "kill-all door: set when the room has no enemies")}
		}
		return []use{read(sw, "the door unlocks when it is on")}
	}},

	// Chests. d_a_tbox: getFuncType params & 0x7F (d_a_tbox.cpp:396-398),
	// getSwNo (params >> 12) & 0xFF (d_a_tbox.h:36), mRoomNo home.angle.x
	// & 0x3F (create, d_a_tbox.cpp:1241), the open switch home.angle.z &
	// 0xFF, set in mRoomNo when opened (OpenInit_com :846-864). Function
	// type 2 (FUNC_TYPE_ENEMIES, :23-31) sets SwNo once mRoomNo has no
	// enemies (actionGenocide :1026-1046); 1, 3, 4, 6 and 8 appear when
	// SwNo is on (:484-512, actionSwOnWait).
	{proc: stagedata.ProcTbox, name: "TBOX", decode: func(e entry) []use {
		fn, swNo, room, open := e.bits(0, 7), e.byte(12), e.angle(0)&0x3F, e.angleByte(2)
		u := []use{set(open, Latching, "chest: set when opened").in(room)}
		switch fn {
		case 2:
			u = append(u, set(swNo, Latching, "enemy chest: set when the room has no enemies").in(room))
		case 1, 3, 4, 6, 8:
			u = append(u, read(swNo, "the chest appears when it is on").in(room))
		}
		return u
	}},

	// Push blocks. d_a_obj_movebox.h:141-193: type PrmAbstract(4, 24)
	// (mType, d_a_obj_movebox.cpp:73), swSave1 PrmAbstract(8, 8), dmy
	// PrmAbstract(1, 30); mPrmZ is home.angle.z, or 0xFFFF for dmy
	// (prmZ_init :1020-1037); pathId mPrmZ & 0xFF and swSave2 mPrmZ >> 8,
	// both 0xFF for type 0xB, which instead gives (mPrmZ >> 8) to its Mkie
	// child (prmZ_get_swSave2_MkieB, :1356). With a path and swSave1,
	// path_save stores which path point the box is at: the low bit in
	// swSave1, the high bit in swSave2 (:1153-1198), and path_init reads
	// them back (:1127-1151). Without a path, swSave1 decides whether the
	// box appears (chk_appear :1469-1484).
	{proc: stagedata.ProcMovebox, name: "Obj_Movebox", decode: func(e entry) []use {
		typ, sw1, dmy := e.bits(24, 4), e.byte(8), e.bits(30, 1)
		prmZ := e.angle(2)
		if dmy != 0 {
			prmZ = 0xFFFF
		}
		path, sw2 := prmZ&0xFF, prmZ>>8&0xFF
		if typ == 0xB {
			// The Mkie child's switch (daObjMkie::prm_make,
			// d_a_obj_mkie.h:46-55), set when it is lit
			// (d_a_obj_mkie.cpp:365).
			return []use{read(sw1, "the box appears when it is off (on with dmy)"), set(prmZ>>8&0xFF, Latching, "the box's Mkie: set when it is lit")}
		}
		if path == 0xFF || sw1 == 0xFF {
			return []use{read(sw1, "the box appears when it is off (on with dmy)")}
		}
		why := fmt.Sprintf("push block path %d: stores the box's path point, not an event", path)
		u := setClear(sw1, why)
		u.path = path
		out := []use{u}
		if sw2 != 0xFF {
			u = setClear(sw2, why)
			u.path = path
			out = append(out, u)
		}
		return out
	}},

	// Enemy death switches: the u8 of current.angle.z (home.angle.z at
	// create), 0xFF read as 0, 0 meaning none; set on death and handed to
	// enemy_ice as mDeathSwitch, which sets it on a Light Arrow or frozen
	// death (c_damagereaction.cpp:165-166, :348-349).
	// d_a_bk.cpp: type params & 0xF (0xB read as 4), m02B8 (:4757-4777),
	// set :2644-2646 and :2729-2732, enemy_ice :5273; type 0xA, the boss
	// (:5027-5033), sets room switches 0xE0 (:3993) and 0xE1 (:4063) in
	// its fight.
	{proc: stagedata.ProcBK, name: "BK", decode: func(e entry) []use {
		u := deathSwitch(e, "Bokoblin")
		if e.bits(0, 4) == 0xA {
			u = append(u, set(0xE0, Latching, "boss Bokoblin's fight"), set(0xE1, Latching, "boss Bokoblin's fight"))
		}
		return u
	}},
	// d_a_mo2.cpp: create :4093-4097, set :2408-2410, enemy_ice :4194;
	// mEnableSpawnSwitch params >> 24.
	{proc: stagedata.ProcMO2, name: "MO2", decode: func(e entry) []use {
		return append(deathSwitch(e, "Moblin"), read(e.byte(24), "the Moblin spawns when it is on"))
	}},
	// d_a_st.cpp: create :2836-2844, set :1237, :2218, enemy_ice :2913;
	// the ambush switch params >> 24.
	{proc: stagedata.ProcST, name: "ST", decode: func(e entry) []use {
		return append(deathSwitch(e, "Stalfos"), read(e.byte(24), "the Stalfos ambushes when it is on"))
	}},
	// d_a_tn.cpp: create :4212, :4237-4238, set :2070-2072, enemy_ice
	// :4335.
	{proc: stagedata.ProcTN, name: "TN", decode: func(e entry) []use { return deathSwitch(e, "Darknut") }},

	// Bosses. d_a_bdk.cpp sets dan switch 0x80 in the Helmaroc King's
	// fight (:2064) and clears it again (:2308).
	{proc: stagedata.ProcBDK, name: "BDK", decode: func(e entry) []use {
		return []use{setClear(0x80, "Helmaroc King's fight: set, then cleared")}
	}},
	// d_a_bpw.cpp (NonMatching, but the code is there): the base switch
	// (params >> 16) & 0xFF (:4581); light_on_off sets and clears base,
	// base+1 and base+2 with the lights (:845-900).
	{proc: stagedata.ProcBPW, name: "BPW", decode: func(e entry) []use {
		base := e.byte(16)
		if base == 0xFF {
			return nil
		}
		var u []use
		for i := 0; i < 3; i++ {
			u = append(u, setClear(base+i, "Jalhalla's room lights: set and cleared with them"))
		}
		return u
	}},
	// d_a_daiocta.cpp: params >> 24 (:1562), set when beaten (:1261-1264).
	{proc: stagedata.ProcDaiocta, name: "DAIOCTA", decode: fixed(Latching, "Big Octo: set when beaten", prmByte(24))},
	// d_a_fganon.cpp create (:2735-2757): (params >> 16) & 0xFF set when
	// beaten (:1911, :2002); params >> 24 set in the fight (:1756) and
	// cleared (:1915).
	{proc: stagedata.ProcFganon, name: "FGANON", decode: func(e entry) []use {
		return []use{
			set(e.byte(16), Latching, "Phantom Ganon: set when beaten"),
			setClear(e.byte(24), "Phantom Ganon's fight: set, then cleared"),
		}
	}},

	// Tingle Tuner switches. d_a_agbsw0.h:74-82: sw0 (params >> 16) &
	// 0xFF, sw1 params >> 24, type home.angle.z & 0xFF ("agbA".."agbFA",
	// d_stage.cpp:503-515, are all this proc; the type, not the name,
	// picks the code). execute calls ExeSubTable[type], 15 entries with
	// no bound check (d_a_agbsw0.cpp:277-297); create turns some M types
	// into 0xD and 0xE and some R types into B (:154-171) and writes no
	// switch. Every switch write needs mDoGaC_GbaLink true on the frame
	// it is made (m_Do_gba_com.cpp:258-270):
	//
	//	MW (7): offSwitch sw0 (:1117), after returning at once while no
	//	   GBA is linked (:1074)
	//	T (6):  onSwitch sw0 when a bomb hits it (:1158), inside
	//	   if (mDoGaC_GbaLink()) (:1153)
	//	S (8):  onSwitch sw0 and sw1 (:1198-1200, :1216-1218), inside
	//	   field_0x299 == 0 && mDoGaC_GbaLink() (:1193); its M_NewD2 room
	//	   0xC case only deletes the actor (:1187-1190)
	//	others: no switch write; they read their switches and mail the GBA
	//	   (MailSend :2537-2547 only queues the mail, mDoGac_SendDataSet)
	//
	// The GBA's answer reaches the Tuner actor, whose SwitchOn flips
	// mSwitch.field_0x0 and sets or flips field_0x4 (d_a_agb.cpp:
	// 834-853); daAgb_Execute calls it only under mDoGaC_GbaLink
	// (:1682-1684). Which switches the GBA program names there is not in
	// the decomp, so the table takes both of the actor's, either way
	// (inferred). So every writer is GBA: none writes while no GBA is
	// linked. A type past the table's 15 entries runs whatever follows it
	// (Unknown).
	{proc: procAgbsw0, name: "AGBSW0", decode: func(e entry) []use {
		sw0, sw1 := e.byte(16), e.byte(24)
		if typ := e.angleByte(2); typ >= agbsw0Types {
			why := fmt.Sprintf("Tingle Tuner switch type %d is past ExeSubTable's %d entries", typ, agbsw0Types)
			return []use{toggle(sw0, why).as(Unknown), toggle(sw1, why).as(Unknown)}
		}
		why := "Tingle Tuner switch: written only while a GBA is linked (the actor, and the GBA player through the Tuner)"
		return []use{toggle(sw0, why).as(GBA), toggle(sw1, why).as(GBA)}
	}},

	// Breakables and other one-shot setters.
	{proc: procBombFlower, name: "BOMB_FLOWER", decode: fixed(Latching, "bomb flower: set when it grows", prmByte(8))},            // d_a_bflower.h:67-68; d_a_bflower.cpp:419-425
	{proc: procFloor, name: "FLOOR", decode: fixed(Latching, "breakable floor: set when broken", prmByte(0))},                     // d_a_floor.cpp:13-15, :79-90
	{proc: procHys, name: "Hys", decode: fixed(Latching, "eye switch: set when hit", prmByte(0))},                                 // d_a_hys.cpp:104, mode_sw_on_init :176-178
	{proc: procKokiie, name: "KOKIIE", decode: fixed(Latching, "hanging flower house: set when it falls", prmByte(24))},           // d_a_kokiie.cpp:393, :133-135
	{proc: procMflft, name: "MFLFT", decode: fixed(Latching, "flame lift: sets it", prmByte(24))},                                 // d_a_mflft.cpp:604, :216-218
	{proc: procNpcOs, name: "NPC_OS", decode: fixed(Latching, "Companion Statue: sets it in room 7", prmByte(16))},                // d_a_npc_os.cpp:2042, set in room 7 only :1605-1609
	{proc: procAjav, name: "Obj_Ajav", decode: fixed(Latching, "Jabun's cave wall: set when blown up", prmByte(0))},               // d_a_obj_ajav.cpp:468, :823; d_a_obj_ajav.h:76
	{proc: procCanon, name: "OBJ_CANON", decode: fixed(Latching, "sea cannon: set when destroyed", prmByte(24))},                  // d_a_obj_canon.cpp:573, :378-381
	{proc: procDragonhead, name: "Obj_Dragonhead", decode: fixed(Latching, "dragon head: sets it", prmByte(8))},                   // d_a_obj_dragonhead.cpp:52, :151-153
	{proc: procEbomzo, name: "Obj_Ebomzo", decode: fixed(Latching, "bombable statue: set when blown up", prmByte(0))},             // d_a_obj_ebomzo.h:12-17; d_a_obj_ebomzo.cpp:136
	{proc: procEskban, name: "Obj_Eskban", decode: fixed(Latching, "sets it", prmByte(0))},                                        // d_a_obj_eskban.h:20-25; d_a_obj_eskban.cpp:354
	{proc: procHcbh, name: "Obj_Hcbh", decode: fixed(Latching, "face pillar: sets it", prmByte(13))},                              // d_a_obj_hcbh.h:14-31
	{proc: procHomen, name: "Obj_Homen", decode: fixed(Latching, "face stone: sets it", prmByte(24))},                             // d_a_obj_homen.h:38-51; d_a_obj_homen.cpp:664
	{proc: procHsh, name: "Obj_HSH", decode: fixed(Latching, "Command Melody monument: set when the song is played", prmByte(0))}, // d_a_obj_hsehi1.cpp:265, :713-721
	{proc: procIce, name: "Obj_Ice", decode: fixed(Latching, "ice block: set when melted", prmByte(0))},                           // d_a_obj_ice.h:16-27
	{proc: procLeaves, name: "Obj_Leaves", decode: fixed(Latching, "leaf pile: set when blown away", prmByte(13))},                // d_a_obj_leaves.h:14-36
	{proc: procMjdoor, name: "OBJ_MJDOOR", decode: fixed(Latching, "Forsaken Fortress barricade: set when blown up", prmByte(0))}, // d_a_obj_majyuu_door.cpp:115, :217-220
	{proc: procMkie, name: "Obj_Mkie", decode: fixed(Latching, "eye statue: set when lit", prmByte(16))},                          // d_a_obj_mkie.h:42-58; d_a_obj_mkie.cpp:365
	{proc: procMkiek, name: "Obj_Mkiek", decode: fixed(Latching, "sets it", prmByte(0))},                                          // d_a_obj_mkiek.h:18-26; d_a_obj_mkiek.cpp:174
	{proc: procMknjD, name: "Obj_MknjD", decode: fixed(Latching, "god's song statue: sets it", prmByte(0))},                       // d_a_obj_mknjd.h:17-24; d_a_obj_mknjd.cpp:473
	{proc: procShmrgrd, name: "Obj_Shmrgrd", decode: fixed(Latching, "spiked hammer switch: set when hammered", prmByte(0))},      // d_a_obj_shmrgrd.h:21-38
	{proc: procVfan, name: "Obj_Vfan", decode: fixed(Latching, "Phantom Ganon door: sets it", prmByte(0))},                        // d_a_obj_vfan.h:16-23; d_a_obj_vfan.cpp:191
	{proc: procVmc, name: "Obj_Vmc", decode: fixed(Latching, "soft soil: set when the tree grows", prmByte(8))},                   // d_a_obj_vmc.h:17-22; d_a_obj_vmc.cpp:260
	{proc: procVolcano, name: "Obj_Volcano", decode: fixed(Latching, "lava: set when frozen", prmByte(8))},                        // d_a_obj_volcano.h:15-19; d_a_obj_volcano.cpp:279-283
	{proc: procOship, name: "OSHIP", decode: fixed(Latching, "enemy ship: set when sunk", prmByte(24))},                           // d_a_oship.cpp:993, :720-722, :749
	{proc: procStone2, name: "Stone2", decode: fixed(Latching, "sets it", prmByte(8))},                                            // d_a_stone2.h:47-57; d_a_stone2.cpp:108-112, :273, :513
	{proc: procSwAttack, name: "SW_ATTACK", decode: fixed(Latching, "sets it when hit", prmByte(8))},                              // d_a_swattack.h:26; d_a_swattack.cpp:115-117
	{proc: procSwTact, name: "SW_TACT", decode: fixed(Latching, "Wind Crest: set when the song is played", prmByte(0))},           // d_a_swtact.h:37; d_a_swtact.cpp:169-171
	{proc: procWall, name: "WALL", decode: fixed(Latching, "bombable wall: set when blown up", prmByte(0))},                       // d_a_wall.cpp:133, :141, :302-303
	{proc: procKui, name: "KUI", decode: func(e entry) []use {
		// d_a_kui.cpp:478-488: no actor for params 0xFFFFFFFF; else
		// params >> 24, with 0xFF read as switch 0; set at :189, :305,
		// :381.
		if e.Params == 0xFFFFFFFF {
			return nil
		}
		sw := e.byte(24)
		if sw == 0xFF {
			sw = 0
		}
		return []use{set(sw, Latching, "grapple post: sets it")}
	}},
	{proc: procHmlif, name: "Hmlif", decode: func(e entry) []use {
		// d_a_hmlif.cpp: getSwitchNo params & 0xFF (:76-78), read
		// (:468); getSwitchNo2 home.angle.z & 0xFF (:97-99, :129), set
		// on an arrow hit (:325-339).
		return []use{set(e.angleByte(2), Latching, "glowing platform: set when hit by an arrow"), read(e.byte(0), "the lift moves when it is on")}
	}},
	{proc: procHami3, name: "Obj_Hami3", decode: func(e entry) []use {
		// d_a_obj_hami3.h:10-22: sw params & 0xFF read, sw2 (params >>
		// 8) & 0xFF set as it opens (d_a_obj_hami3.cpp:149-150).
		return []use{set(e.byte(8), Latching, "sets it as it opens"), read(e.byte(0), "the gate opens when it is on")}
	}},
	{proc: procTenmado, name: "Obj_Tenmado", decode: func(e entry) []use {
		// d_a_obj_tenmado.cpp:41: sw params & 0xFF read, sw2 (params >>
		// 8) & 0xFF set (:117).
		return []use{set(e.byte(8), Latching, "light-beam doors: set when opened"), read(e.byte(0), "the doors open when it is on")}
	}},
	{proc: procNpcMn, name: "NPC_MN", decode: func(e entry) []use {
		// d_a_npc_mn.h:53-56: PRM_SWITCH2 (params >> 16) & 0xFF
		// (getter d_a_npc_mn.cpp:1377-1379), set in its event (:1003,
		// :1023); switch (params >> 8) & 0xFF read (:659-660).
		return []use{set(e.byte(16), Latching, "Manny: set in his event"), read(e.byte(8), "Manny's switch")}
	}},
	{proc: procSaku, name: "SAKU", decode: func(e entry) []use {
		// d_a_saku.h:137-142: sw (params >> 8) & 0xFF, sw2 (params >>
		// 16) & 0xFF, both set when broken (d_a_saku.cpp:369-393).
		return []use{set(e.byte(8), Latching, "wooden barricade: set when broken"), set(e.byte(16), Latching, "wooden barricade: set when broken")}
	}},
	{proc: procStone, name: "STONE", decode: func(e entry) []use {
		// d_a_stone.h:84-109: type PrmAbstract(3, 24) (d_a_stone.cpp:191),
		// sw PrmAbstract(8, 8). M_data (d_a_stone.cpp:60-118) has five
		// types; 2, 3 and 4 have DATAFLG_8 (1 << 3, d_a_stone.h:18) and
		// set the switch when broken (d_a_stone.cpp:500-504).
		switch typ := e.bits(24, 3); typ {
		case 2, 3, 4:
			return []use{set(e.byte(8), Latching, "rock: set when broken")}
		case 5, 6, 7:
			return []use{set(e.byte(8), Unknown, fmt.Sprintf("rock type %d is past M_data's five entries", typ))}
		}
		return nil
	}},
	{proc: procWarpt, name: "OBJ_WARPT", decode: func(e entry) []use {
		// d_a_obj_warpt.cpp getArg (:704-760): type params & 0xF, m2AC
		// home.angle.x & 0xFF, m2B0 (home.angle.x >> 8) & 0xFF. Types 2,
		// 3 and 4 (isSp, :260-263) are the warp jars, which open with
		// warp bits and only read m2AC (isOtherHuta :266-269); the others
		// set m2AC when the lid opens (openHuta :389-399), type 1 at
		// create (:716-718), and read m2B0 (:545). They route it with the
		// room Link stays in, which is the pot's own when he opens it.
		lid := e.angleByte(0)
		switch e.bits(0, 4) {
		case 2, 3, 4:
			return []use{read(lid, "the warp jar's other lid")}
		}
		return []use{set(lid, Latching, "warp pot: set when its lid opens"), read(e.angle(0)>>8&0xFF, "the warp pot's second switch")}
	}},
	{proc: procSwhammer, name: "Obj_Swhammer", decode: func(e entry) []use {
		// d_a_obj_swhammer.h:13-24: sw (params >> 8) & 0xFF set when
		// hammered (d_a_obj_swhammer.cpp:362-369), sw2 params & 0xFF
		// cleared then (off_switch2).
		return []use{set(e.byte(8), Latching, "hammer switch: set when hammered"), clear(e.byte(0), "hammer switch: clears its second switch when hammered")}
	}},
	{proc: procSwlight, name: "Obj_Swlight", decode: func(e entry) []use {
		// d_a_obj_swlight.h:29-55: sw params & 0xFF, sw2 (params >> 8) &
		// 0xFF, type (params >> 16) & 1 (mode, d_a_obj_swlight.cpp:
		// 134-148). Type 0 only sets (mode_norm_moon :286-312); type 1
		// clears when the light leaves unless sw2 is on (mode_active_sun
		// :372-380).
		sw, r := e.byte(0), read(e.byte(8), "holds the light switch on")
		if e.bits(16, 1) == 0 {
			return []use{set(sw, Latching, "light switch: set when lit"), r}
		}
		return []use{setClear(sw, "sun switch: cleared when the light leaves"), r}
	}},
	{proc: procTagHint, name: "TAG_HINT", decode: func(e entry) []use {
		// d_a_tag_hint.cpp:20-42: sw (params >> 8) & 0xFF, sw2 (params >>
		// 16) & 0xFF, type params & 0x3F. startProc sets sw except for
		// types 0, 6 and 12 (:443-460); type 1 also at create and in
		// waitTerms (:146-152, :747-750). sw2 gates the hint (:169, :186).
		u := []use{read(e.byte(16), "gates the hint")}
		switch e.bits(0, 6) {
		case 0, 6, 12:
			return u
		}
		return append(u, set(e.byte(8), Latching, "hint: set when it plays"))
	}},
	{proc: procTagLight, name: "Tag_Light", decode: func(e entry) []use {
		// d_a_tag_light.h:15-71: sw2 PrmAbstract(8, 2), sw PrmAbstract(8,
		// 16); sets sw when lit (d_a_tag_light.cpp:443-454).
		return []use{set(e.byte(16), Latching, "light target: set when lit"), read(e.bits(2, 8), "the light target's second switch")}
	}},
	{proc: procTagMsg, name: "TAG_MSG", decode: fixed(Latching, "message: set when it starts", prmByte(8))}, // d_a_tag_msg.cpp:19-22, :186

	// Event setters: set as a story event starts. The event's result
	// travels in §6.5's story batch; the switch is not synced on its own.
	{proc: procTagEvent, name: "TAG_EVENT", decode: func(e entry) []use {
		// d_a_tag_event.cpp:23-26: sw (params >> 8) & 0xFF, set as its
		// event starts (actionReady :218-231, actionMjReady :385-392);
		// sw2 (params >> 16) & 0xFF gates it.
		return []use{set(e.byte(8), Event, "event trigger: set as its event starts"), read(e.byte(16), "gates the event")}
	}},
	{proc: procTagIsland, name: "TAG_ISLAND", decode: fixed(Event, "island event: set as its event starts", prmByte(8))}, // d_a_tag_island.cpp:37-39, :400-410

	// Writers that set and clear.
	{proc: procNpcHr, name: "NPC_HR", decode: func(e entry) []use { // d_a_npc_hr.cpp:239-241; on and off at :373, :682, :805, :1218, :1730, :1785
		return []use{setClear(e.byte(8), "Zephos and Cyclos: set and cleared in their events")}
	}},
	{proc: procCorrect, name: "Obj_Correct", decode: func(e entry) []use { // d_a_obj_correct.h:19-37; d_a_obj_correct.cpp:279-302
		return []use{setClear(e.byte(8), "placement check: set while the object is in place, cleared when it leaves")}
	}},
	{proc: procFerris, name: "Obj_Ferris", decode: func(e entry) []use { // d_a_obj_ferris.h:33-39; d_a_obj_ferris.cpp:351
		return []use{clear(e.byte(0), "Windfall Ferris wheel: clears it")}
	}},
	{proc: procSwPropeller, name: "SW_PROPELLER", decode: func(e entry) []use { // d_a_swpropeller.cpp:15, :183-185
		return []use{toggle(e.byte(0), "propeller switch: flips with the wind")}
	}},
	{proc: procTagKbItem, name: "TAG_KB_ITEM", decode: func(e entry) []use {
		// d_a_tag_kb_item.cpp:13: params >> 24; set when the pig digs
		// (d_com_static.cpp:428-442), cleared in _delete once the item
		// is out (d_a_tag_kb_item.cpp:16-22).
		return []use{setClear(e.byte(24), "pig dig spot: set when dug, cleared when the item is out")}
	}},
	{proc: procTagVolcano, name: "Tag_Volcano", decode: func(e entry) []use {
		// d_a_tag_volcano.h:11-24: swSave PrmAbstract(8, 8); set and
		// cleared from event bits at create, then cleared by its timer
		// and at delete (d_a_tag_volcano.cpp:23-110).
		return []use{setClear(e.byte(8), "volcano tag: set and cleared from event bits and its timer")}
	}},

	// Readers only.
	{proc: procShutter, name: "SHUTTER", decode: func(e entry) []use { // d_a_shutter.h:60; d_a_shutter.cpp:133-144, 233-267
		return []use{read(e.byte(0), "the shutter opens when it is on")}
	}},
	{proc: procLightBridge, name: "LIGHTBRIDGE", decode: func(e entry) []use { // d_a_lbridge.h:46-47; d_a_lbridge.cpp:135-189
		return []use{read(e.byte(0), "the light bridge appears when it is on")}
	}},
	{proc: procLadder, name: "Obj_Ladder", decode: func(e entry) []use { // d_a_obj_ladder.h:27-38; d_a_obj_ladder.cpp:150-181
		return []use{read(e.byte(8), "the ladder drops when it is on")}
	}},
	{proc: procHlift, name: "Obj_Hlift", decode: func(e entry) []use { // d_a_obj_hlift.h:45-51; d_a_obj_hlift.cpp:110-164
		return []use{read(e.byte(8), "the lift rises when it is on")}
	}},

	// Unknown: a real switch field over code the decomp hasn't matched.
	{proc: procTry, name: "Obj_Try", decode: func(e entry) []use {
		// d_a_obj_try.h:25-36: PRM_SWSAVE PrmAbstract(8, 8), but
		// d_a_obj_try.cpp is NonMatching (configure.py:1824) with no
		// switch call decoded.
		return []use{set(e.byte(8), Unknown, "Obj_Try is NonMatching; how it uses the switch is unknown")}
	}},

	// Undecoded: switch users whose fields the decomp doesn't give.
	{proc: procSwflat, name: "Obj_Swflat", undecoded: stub("d_a_obj_swflat.cpp", "prmGetSwNo, prmGetSwNo2, d_a_obj_swflat.h:9-10")},
	{proc: procItnak, name: "Obj_Itnak", undecoded: stub("d_a_obj_Itnak.cpp", "is_switch, param_get_swbit, d_a_obj_Itnak.h:10-12")},
	{proc: procVds, name: "Obj_Vds", undecoded: stub("d_a_obj_Vds.cpp", "is_switch, d_a_obj_Vds.h:13")},
	{proc: procAshut, name: "Obj_Ashut", undecoded: stub("d_a_obj_ashut.cpp", "is_switch, prm_get_swSave, d_a_obj_ashut.h:13-15")},
	{proc: procEkskz, name: "Obj_Ekskz", undecoded: stub("d_a_obj_ekskz.cpp", "prm_get_swSave, d_a_obj_ekskz.h:13")},
	{proc: procFirewall, name: "Obj_Firewall", undecoded: stub("d_a_obj_firewall.cpp", "param_get_swSave, d_a_obj_firewall.h:8")},
	{proc: procFlame, name: "Obj_Flame", undecoded: stub("d_a_obj_flame.cpp", "prm_get_swSave, set_switch, d_a_obj_flame.h:27-29")},
	{proc: procHami2, name: "Obj_Hami2", undecoded: stub("d_a_obj_hami2.cpp", "prm_get_swSave, d_a_obj_hami2.h:9")},
	{proc: procHtetu1, name: "Obj_Htetu1", undecoded: stub("d_a_obj_htetu1.cpp", "check_sw, d_a_obj_htetu1.h:9-10")},
	{proc: procKanoke, name: "Obj_Kanoke", undecoded: stub("d_a_obj_kanoke.cpp", "getPrmSwNo, getPrmSwNo2, d_a_obj_kanoke.h:30-31")},
	{proc: procMsdan, name: "Obj_Msdan", undecoded: stub("d_a_obj_msdan.cpp", "prm_get_swSave, d_a_obj_msdan.h:12")},
	{proc: procTapestry, name: "Obj_Tapestry", undecoded: stub("d_a_obj_tapestry.cpp", "is_switch, on_switch, param_get_swSave, d_a_obj_tapestry.h:11-14")},
	{proc: procTnTrap, name: "Obj_TnTrap", undecoded: stub("d_a_obj_tntrap.cpp", "param_get_swSave, param_get_swSave2, d_a_obj_tntrap.h:10-11")},
	{proc: procTribox, name: "Obj_Tribox", undecoded: stub("d_a_obj_tribox.cpp", "is_switch, on_switch, prm_get_swSave, d_a_obj_tribox.h:10-12")},
	{proc: procBigelf, name: "BIGELF", undecoded: stub("d_a_bigelf.cpp", "getSwbit, getSwbit2, d_a_bigelf.h:58-59")},
	{proc: procNpcKk1, name: "NPC_KK1", undecoded: stub("d_a_npc_kk1.cpp", "getSWbit, d_a_npc_kk1.h:14")},
	{proc: procNpcP2, name: "NPC_P2", undecoded: stub("d_a_npc_p2.cpp", "cutSwOnStart, d_a_npc_p2.h:96")},
	{proc: stagedata.ProcPT, name: "PT", undecoded: "d_a_pt.cpp is NonMatching (configure.py:1838); its spawn switches (mEnableSpawnSwitch, mDisableRespawnSwitch, d_a_pt.h:14-15) have no decoded params"},
	{proc: stagedata.ProcWZ, name: "WZ", undecoded: "d_a_wz.cpp is NonMatching (configure.py:1868); its spawn switches (mEnableSpawnSwitch, mDisableSpawnOnDeathSwitch, d_a_wz.h:18-19) have no decoded params"},
}

// specByProc indexes specs.
var specByProc = func() map[stagedata.Proc]*spec {
	m := make(map[stagedata.Proc]*spec, len(specs))
	for i := range specs {
		m[specs[i].proc] = &specs[i]
	}
	return m
}()

// doorUses decodes a dDoor_info_c door: key names a key door's kind ("" for
// none); frontSets and backSets say whether that side's bars are kill-all
// ones, which set their switch rather than read it.
func doorUses(e entry, key string, frontSets, backSets bool) []use {
	sw, sw2 := e.byte(0), e.byte(20)
	front, back := e.angle(0)&0x3F, e.angle(0)>>6&0x3F
	var u []use
	if key != "" && sw < 0x80 {
		u = append(u, set(sw, Latching, key+": set when a key opens the lock").in(-1))
	}
	if frontSets {
		u = append(u, set(sw, Latching, "kill-all bars: set when the front room has no enemies").in(front))
	} else {
		u = append(u, read(sw, "the front bars open when it is on").in(front))
	}
	if backSets {
		u = append(u, set(sw2, Latching, "kill-all bars: set when the back room has no enemies").in(back))
	} else {
		u = append(u, read(sw2, "the back bars open when it is on").in(back))
	}
	return u
}

// deathSwitch decodes the angle.z death switch of BK, MO2, ST and TN.
func deathSwitch(e entry, who string) []use {
	sw := e.angleByte(2)
	if sw == 0xFF || sw == 0 {
		return nil
	}
	return []use{set(sw, Latching, who+": set when killed")}
}
