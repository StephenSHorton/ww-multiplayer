package stagedata

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// l_objectName is the table the game looks an actor entry's name up in to
// find what to create (dStage_searchName, d_stage.cpp:1266-1277, from
// dStage_actorCreate, :1300-1310): 825 entries (the OBJNAME lines at
// d_stage.cpp:438-1262) of dStage_objectNameInf (d_stage.h:989-994):
//
//	0x0  char name[8]
//	0x8  s16 procname, an fpcNm_*_e value (f_pc_name.h)
//	0xA  s8 argument: the subtype, copied to fopAcM_prm_class.argument
//	     (d_stage.cpp:1306); 255 (-1) in the table means none
//	0xB  s8 gbaName (d_stage.cpp:1307)
//
// It is read out of the user's own main.dol: .data 0x80372818, 0x26AC
// bytes (sym:14693 of the decomp's config/GZLE01/symbols.txt). The search
// is a linear strcmp from the top, so of two entries with one name (the
// table has two "Boko" and two "Sitem", d_stage.cpp:920,1241,1247,1256)
// the first wins. The last entry is "EndCode" (d_stage.cpp:1262), which
// ReadObjectNames checks as the sign that the table is where GZLE01 has
// it.
const (
	ObjectNameAddr      uint32 = 0x80372818
	ObjectNameSize             = 0x26AC
	ObjectNameCount            = ObjectNameSize / objectNameEntrySize // 825
	objectNameEntrySize        = 0xC
	objectNameLen              = 8
	objectNameEnd              = "EndCode"
)

// Proc is a process profile name, an fpcNm_*_e value: the s16 procname of
// an l_objectName entry and an actor's mProcName (f_pc_base.h:16).
type Proc int16

// The procs M1's tables use, as GZLE01 numbers them. Each is the
// fpcNm_*_e enum value at that line of f_pc_name.h, whose comments give
// the values; counting lines doesn't, because the DEMO-only
// fpcNm_KAMOME2_e (f_pc_name.h:205-207) and the PAL-only fpcNm_MSG3_e
// (:499-501) are compiled out of GZLE01 (VERSION_USA, global.h:65) and
// tk_Obj_Barrel2_e (:476) aliases the entry before it. A quoted name is
// an l_objectName name that creates the proc, with its d_stage.cpp line.
const (
	ProcPlayScene Proc = 0x007 // fpcNm_PLAY_SCENE_e, f_pc_name.h:16; "EndCode" (d_stage.cpp:1262)
	ProcAllDie    Proc = 0x016 // fpcNm_ALLDIE_e, f_pc_name.h:31; "ALLdie" (d_stage.cpp:573)
	ProcTagEvsw   Proc = 0x01C // fpcNm_TAG_EVSW_e, f_pc_name.h:37; "Evsw" (d_stage.cpp:601)
	ProcSwpush    Proc = 0x01D // fpcNm_Obj_Swpush_e, f_pc_name.h:38; floor switches "Kbota_A" etc. (d_stage.cpp:564-566)
	ProcSwheavy   Proc = 0x01E // fpcNm_Obj_Swheavy_e, f_pc_name.h:39; iron-boots switches "Hhbot1" (d_stage.cpp:567-568)
	ProcMovebox   Proc = 0x02D // fpcNm_Obj_Movebox_e, f_pc_name.h:54; push blocks "osiBLK0" etc. (d_stage.cpp:541-555)
	ProcEP        Proc = 0x0BA // fpcNm_EP_e, f_pc_name.h:195; torches "bonbori" (d_stage.cpp:481; d_a_ep.cpp)
	ProcItem      Proc = 0x101 // fpcNm_ITEM_e, f_pc_name.h:269; "item" (d_stage.cpp:647)
	ProcBossItem  Proc = 0x102 // fpcNm_BOSSITEM_e, f_pc_name.h:270; "Bitem", the heart container a beaten boss leaves (d_stage.cpp:1189; d_a_boss_item.cpp:24-41)
	ProcTbox      Proc = 0x126 // fpcNm_TBOX_e, f_pc_name.h:306; chests "takara" etc. (d_stage.cpp:483-501)
	ProcSwc00     Proc = 0x12C // fpcNm_SWC00_e, f_pc_name.h:312; "SW_C00" (d_stage.cpp:482)
	ProcDoor10    Proc = 0x12E // fpcNm_DOOR10_e, f_pc_name.h:314; "door10", "keyshut" etc. (d_stage.cpp:451-457)
	ProcDoor12    Proc = 0x12F // fpcNm_DOOR12_e, f_pc_name.h:315; "door12", "keyS12" etc. (d_stage.cpp:473-480)
	ProcAndsw0    Proc = 0x135 // fpcNm_ANDSW0_e, f_pc_name.h:321; "AND_SW0" (d_stage.cpp:651)
	ProcAndsw2    Proc = 0x136 // fpcNm_ANDSW2_e, f_pc_name.h:322; "AND_SW2" (d_stage.cpp:652)
	ProcSwhit0    Proc = 0x1C9 // fpcNm_SWHIT0_e, f_pc_name.h:469; crystal switches "SW_HIT0" (d_stage.cpp:531)
	ProcObjTimer  Proc = 0x1E1 // fpcNm_Obj_Timer_e, f_pc_name.h:494; "ObjTime" (d_stage.cpp:876); not fpcNm_TIMER_e, the HUD timer

	// The enemies §7 names: the K1 classes, the boss denylist and the M7
	// KILL classes. The English names are the ones each class's
	// d_a_*.cpp uses.
	ProcFM      Proc = 0x079 // fpcNm_FM_e, f_pc_name.h:130: Floormaster
	ProcBB      Proc = 0x0B6 // fpcNm_BB_e, f_pc_name.h:191: Kargaroc
	ProcMO2     Proc = 0x0BD // fpcNm_MO2_e, f_pc_name.h:198: Moblin
	ProcBK      Proc = 0x0BE // fpcNm_BK_e, f_pc_name.h:199: Bokoblin
	ProcST      Proc = 0x0BF // fpcNm_ST_e, f_pc_name.h:200: Stalfos
	ProcTN      Proc = 0x0C0 // fpcNm_TN_e, f_pc_name.h:201: Darknut
	ProcNZ      Proc = 0x0C7 // fpcNm_NZ_e, f_pc_name.h:211: Rat
	ProcAM      Proc = 0x0CB // fpcNm_AM_e, f_pc_name.h:215: Armos Knight
	ProcAM2     Proc = 0x0CC // fpcNm_AM2_e, f_pc_name.h:216: Armos
	ProcGM      Proc = 0x0CD // fpcNm_GM_e, f_pc_name.h:217: Mothula
	ProcKS      Proc = 0x0CE // fpcNm_KS_e, f_pc_name.h:218: Morth
	ProcCC      Proc = 0x0CF // fpcNm_CC_e, f_pc_name.h:219: ChuChu
	ProcBL      Proc = 0x0D0 // fpcNm_BL_e, f_pc_name.h:220: Bubble
	ProcWZ      Proc = 0x0D1 // fpcNm_WZ_e, f_pc_name.h:221: Wizzrobe
	ProcPH      Proc = 0x0D2 // fpcNm_PH_e, f_pc_name.h:222: Peahat
	ProcBPW     Proc = 0x0D4 // fpcNm_BPW_e, f_pc_name.h:224: Jalhalla
	ProcPW      Proc = 0x0D5 // fpcNm_PW_e, f_pc_name.h:225: Poe
	ProcBO      Proc = 0x0D7 // fpcNm_BO_e, f_pc_name.h:227: Boko Baba
	ProcKI      Proc = 0x0D8 // fpcNm_KI_e, f_pc_name.h:228: Keese
	ProcMT      Proc = 0x0D9 // fpcNm_MT_e, f_pc_name.h:229: Magtail
	ProcBWD     Proc = 0x0DA // fpcNm_BWD_e, f_pc_name.h:230: Molgera
	ProcRD      Proc = 0x0E1 // fpcNm_RD_e, f_pc_name.h:237: ReDead
	ProcDaiocta Proc = 0x0E2 // fpcNm_DAIOCTA_e, f_pc_name.h:238: Big Octo
	ProcOQ      Proc = 0x0E4 // fpcNm_OQ_e, f_pc_name.h:240: Octorok
	ProcGY      Proc = 0x0E5 // fpcNm_GY_e, f_pc_name.h:241: Gyorg
	ProcBTD     Proc = 0x0EB // fpcNm_BTD_e, f_pc_name.h:247: Gohma
	ProcBMD     Proc = 0x0EC // fpcNm_BMD_e, f_pc_name.h:248: Kalle Demos
	ProcBDK     Proc = 0x0F0 // fpcNm_BDK_e, f_pc_name.h:252: Helmaroc King
	ProcBST     Proc = 0x0F2 // fpcNm_BST_e, f_pc_name.h:254: Gohdan
	ProcFganon  Proc = 0x0F3 // fpcNm_FGANON_e, f_pc_name.h:255: Phantom Ganon
	ProcBGN     Proc = 0x0F5 // fpcNm_BGN_e, f_pc_name.h:257: Puppet Ganon
	ProcBGN2    Proc = 0x0F6 // fpcNm_BGN2_e, f_pc_name.h:258
	ProcBGN3    Proc = 0x0F7 // fpcNm_BGN3_e, f_pc_name.h:259
	ProcGND     Proc = 0x0F8 // fpcNm_GND_e, f_pc_name.h:260: Ganondorf
	ProcPT      Proc = 0x0F9 // fpcNm_PT_e, f_pc_name.h:261: Miniblin

	// ProcMaxNum is fpcNm_MAX_NUM_e (f_pc_name.h:520): every proc is
	// below it.
	ProcMaxNum Proc = 0x1F6
)

// procNames names the Proc constants for String.
var procNames = map[Proc]string{
	ProcPlayScene: "PLAY_SCENE", ProcAllDie: "ALLDIE", ProcTagEvsw: "TAG_EVSW", ProcSwpush: "Obj_Swpush",
	ProcSwheavy: "Obj_Swheavy", ProcMovebox: "Obj_Movebox", ProcEP: "EP", ProcItem: "ITEM",
	ProcBossItem: "BOSSITEM", ProcTbox: "TBOX", ProcSwc00: "SWC00", ProcDoor10: "DOOR10",
	ProcDoor12: "DOOR12", ProcAndsw0: "ANDSW0", ProcAndsw2: "ANDSW2", ProcSwhit0: "SWHIT0",
	ProcObjTimer: "Obj_Timer",
	ProcFM:       "FM", ProcBB: "BB", ProcMO2: "MO2", ProcBK: "BK", ProcST: "ST", ProcTN: "TN", ProcNZ: "NZ",
	ProcAM: "AM", ProcAM2: "AM2", ProcGM: "GM", ProcKS: "KS", ProcCC: "CC", ProcBL: "BL", ProcWZ: "WZ",
	ProcPH: "PH", ProcBPW: "BPW", ProcPW: "PW", ProcBO: "BO", ProcKI: "KI", ProcMT: "MT", ProcBWD: "BWD",
	ProcRD: "RD", ProcDaiocta: "DAIOCTA", ProcOQ: "OQ", ProcGY: "GY", ProcBTD: "BTD", ProcBMD: "BMD",
	ProcBDK: "BDK", ProcBST: "BST", ProcFganon: "FGANON", ProcBGN: "BGN", ProcBGN2: "BGN2",
	ProcBGN3: "BGN3", ProcGND: "GND", ProcPT: "PT",
}

// String is the fpcNm_*_e name of a Proc constant without its affixes,
// such as "DOOR10", or the number for any other proc.
func (p Proc) String() string {
	if s, ok := procNames[p]; ok {
		return s
	}
	return fmt.Sprintf("proc 0x%03X", uint16(p))
}

// ObjectName is one l_objectName entry.
type ObjectName struct {
	Name    string
	Proc    Proc
	Subtype int8 // the argument; -1 for none
	GBAName int8
}

// ObjectNames is a parsed l_objectName table. Its methods never modify it.
type ObjectNames struct {
	list   []ObjectName
	byName map[string]int // the first entry with each name
}

// ReadObjectNames reads l_objectName out of main.dol, the user's own
// (as Disc.ReadDOL returns it; the patcher doesn't touch .data). A DOL
// that has no data section bytes at 0x80372818, holds anything but a
// valid table there, or whose table doesn't end in "EndCode" is not the
// GZLE01 DOL, and the error says so.
func ReadObjectNames(dol []byte) (*ObjectNames, error) {
	b, err := inject.DOLBytesAt(dol, ObjectNameAddr, ObjectNameSize)
	if err != nil {
		return nil, fmt.Errorf("objname: no l_objectName in main.dol (%w); is it the GZLE01 (NTSC-U) DOL?", err)
	}
	o, err := ParseObjectNames(b)
	if err != nil {
		return nil, fmt.Errorf("objname: no l_objectName at 0x%08x in main.dol (%w); is it the GZLE01 (NTSC-U) DOL?",
			ObjectNameAddr, err)
	}
	if last := o.list[len(o.list)-1]; last.Name != objectNameEnd || last.Proc != ProcPlayScene {
		return nil, fmt.Errorf("objname: the table at 0x%08x ends in %q (proc 0x%03X), not %q (0x%03X); is main.dol the GZLE01 (NTSC-U) DOL?",
			ObjectNameAddr, last.Name, uint16(last.Proc), objectNameEnd, uint16(ProcPlayScene))
	}
	return o, nil
}

// ParseObjectNames parses b as a run of l_objectName entries. Every name
// must be 1-7 printable ASCII characters and NUL-terminated inside its 8
// bytes (no GZLE01 name is longer, and the game's strcmp needs the NUL),
// and every proc in [0, ProcMaxNum).
func ParseObjectNames(b []byte) (*ObjectNames, error) {
	if len(b) == 0 || len(b)%objectNameEntrySize != 0 {
		return nil, fmt.Errorf("objname: %d bytes is not a whole number of 0x%x-byte entries", len(b), objectNameEntrySize)
	}
	str := string(b) // one copy; every name is a slice of it
	o := &ObjectNames{
		list:   make([]ObjectName, len(b)/objectNameEntrySize),
		byName: make(map[string]int, len(b)/objectNameEntrySize),
	}
	for i := range o.list {
		e := str[i*objectNameEntrySize:][:objectNameEntrySize]
		name := cName(e[:objectNameLen])
		if len(name) == 0 || len(name) == objectNameLen || !printable(name) {
			return nil, fmt.Errorf("objname: entry %d: %q is not a NUL-terminated name of printable characters", i, e[:objectNameLen])
		}
		p := Proc(binary.BigEndian.Uint16([]byte(e[8:10])))
		if p < 0 || p >= ProcMaxNum {
			return nil, fmt.Errorf("objname: entry %d (%q): proc 0x%04X is out of range", i, name, uint16(p))
		}
		o.list[i] = ObjectName{Name: name, Proc: p, Subtype: int8(e[10]), GBAName: int8(e[11])}
		if _, ok := o.byName[name]; !ok {
			o.byName[name] = i
		}
	}
	return o, nil
}

// Lookup returns the entry the game uses for an actor named name: the
// first with that name (dStage_searchName, d_stage.cpp:1266-1277). An
// actor whose name has no entry is never created (d_stage.cpp:1303-1304).
func (o *ObjectNames) Lookup(name string) (ObjectName, bool) {
	i, ok := o.byName[name]
	if !ok {
		return ObjectName{}, false
	}
	return o.list[i], true
}

// Proc returns the proc an actor named name creates, as Lookup finds it.
func (o *ObjectNames) Proc(name string) (Proc, bool) {
	e, ok := o.Lookup(name)
	return e.Proc, ok
}

// Len is the number of entries.
func (o *ObjectNames) Len() int { return len(o.list) }

// List returns every entry in table order.
func (o *ObjectNames) List() []ObjectName { return append([]ObjectName(nil), o.list...) }

// ByProc returns the entries that create proc p, in table order, leaving
// out any whose name an earlier entry already took (Lookup never returns
// those).
func (o *ObjectNames) ByProc(p Proc) []ObjectName {
	var out []ObjectName
	for i, e := range o.list {
		if e.Proc == p && o.byName[e.Name] == i {
			out = append(out, e)
		}
	}
	return out
}

// printable reports whether s is all printable, non-space ASCII.
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}
