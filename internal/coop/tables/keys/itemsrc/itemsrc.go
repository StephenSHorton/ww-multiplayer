// Package itemsrc finds the items the user's own stage files place: for
// every actor entry whose class hands out an item its parameters name,
// which item that is and the save bit that records it was taken. The keys
// and hearts tables (M1, §6.4, §11 items 7-8) both read it, and the
// derived small keys and max hearts (M4) count those save bits.
//
// It reads only what the stage files say. Items an NPC, a minigame or a
// cutscene hands over are in code or in event data (hearts keeps a
// hand-written catalogue of those), and an enemy's random drop comes from
// the drop tables (dComIfGp_CharTbl, fopAcM_createItemFromEnemyTable,
// f_op_actor_mng.cpp:1179-1231), which are not stage data.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), as in wwapi/addr.
package itemsrc

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Item numbers (enum ItemTable, d_item_data.h) the tables look for.
const (
	ItemHeartPiece     uint8 = 0x07 // dItemNo_HEART_PIECE_e, d_item_data.h:14; item_func_kakera_heart (d_item.cpp:23, :583-585)
	ItemHeartContainer uint8 = 0x08 // dItemNo_HEART_CONTAINER_e, d_item_data.h:15; item_func_utuwa_heart (d_item.cpp:24, :588-604)
	ItemSmallKey       uint8 = 0x15 // dItemNo_SMALL_KEY_e, d_item_data.h:28; item_func_small_key (d_item.cpp:37, :665-667)
	ItemHeartPieceAlt  uint8 = 0x3F // dItemNo_HEART_PIECE_ALT_e, d_item_data.h:70; item_func_kakera_heart too (d_item.cpp:79)
	ItemBlueJelly      uint8 = 0x4B // dItemNo_BLUE_JELLY_e, d_item_data.h:82
	ItemNone           uint8 = 0xFF // dItemNo_NONE_e, d_item_data.h:262: creates nothing (f_op_actor_mng.cpp:947-949)
)

// Procs this package decodes beyond the ones wwapi/stage names, as GZLE01
// numbers them: the value in the comment at that line of f_pc_name.h (see
// wwapi/stage's Proc constants for why the comments, not line counts).
const (
	ProcOspbox       stage.Proc = 0x056 // fpcNm_Obj_Ospbox_e, f_pc_name.h:95; "Ospbox" (d_stage.cpp:904)
	ProcHomen        stage.Proc = 0x083 // fpcNm_Obj_Homen_e, f_pc_name.h:140; Wind Temple face stones "Homen1", "Homen2" (d_stage.cpp:1120-1121)
	ProcLeaves       stage.Proc = 0x092 // fpcNm_Obj_Leaves_e, f_pc_name.h:155; leaf piles "Vochi" (d_stage.cpp:1123)
	ProcHcbh         stage.Proc = 0x098 // fpcNm_Obj_Hcbh_e, f_pc_name.h:161; Tower of the Gods face pillars "Hcbh" (d_stage.cpp:1166)
	ProcBemos        stage.Proc = 0x0EA // fpcNm_Bemos_e, f_pc_name.h:246; Beamos "Hmos1"-"Hmos3" (d_stage.cpp:921-923)
	ProcRaceItem     stage.Proc = 0x104 // fpcNm_RACEITEM_e, f_pc_name.h:272; "Ritem" (d_stage.cpp:1255)
	ProcSwItem       stage.Proc = 0x139 // fpcNm_SW_ITEM_e, f_pc_name.h:325; "ITat00" (d_stage.cpp:634)
	ProcSalvage      stage.Proc = 0x194 // fpcNm_Salvage_e, f_pc_name.h:416; "Salvage", "SwSlvg" etc. (d_stage.cpp:535-540)
	ProcSalvageChest stage.Proc = 0x195 // fpcNm_SALVAGE_TBOX_e, f_pc_name.h:417; "STBox" (d_stage.cpp:1234)
	ProcTagKbItem    stage.Proc = 0x1A4 // fpcNm_TAG_KB_ITEM_e, f_pc_name.h:432; "TagKb" (d_stage.cpp:583)
	ProcTsubo        stage.Proc = 0x1CB // fpcNm_TSUBO_e, f_pc_name.h:471; pots, jugs, skulls "kotubo" etc. (d_stage.cpp:519-530)
	ProcStone        stage.Proc = 0x1CC // fpcNm_STONE_e, f_pc_name.h:472; "koisi1", "koisi2" (d_stage.cpp:532-533)
	ProcStone2       stage.Proc = 0x1CD // fpcNm_Stone2_e, f_pc_name.h:473; "Ebrock", "Ekao", "Ebrock2" (d_stage.cpp:654-656)
	ProcBarrel2      stage.Proc = 0x1CF // fpcNm_Obj_Barrel2_e, f_pc_name.h:475; "Ktarur" (d_stage.cpp:784)
)

// ProcGrass is grass, trees and flowers: fpcNm_GRASS_e, f_pc_name.h:452;
// "kusax1", "flower" etc. (d_stage.cpp:438-448). Decode reads none of its
// entries as a placement. Trees (kind (prm >> 4) & 3 == 1) drop nothing,
// and for grass (kind 0) and flowers (kinds 2, 3) daGrass_Create keeps
// item (prm >> 6) & 0x3F only when it is 0x20-0x3F and stores -1 (0xFF
// for flowers) otherwise (daGrass_prm, d_a_grass.cpp:17-18; :140-187).
// A cut blade or flower passes a stored item of 0 or more to
// fopAcM_createItemFromTable (d_grass.cpp:157-158, d_flower.cpp:220-221),
// so it only ever drops a random drop table pick (0x20-0x3E) or nothing
// (0x3F): never one fixed item.
const ProcGrass stage.Proc = 0x1B8

// Kind is the class of entry a placement is.
type Kind string

const (
	Chest        Kind = "chest"         // TBOX: "takara" etc.
	FieldItem    Kind = "item"          // ITEM: "item", "itemFLY" (d_stage.cpp:647-648)
	RaceItem     Kind = "race_item"     // RACEITEM
	Salvage      Kind = "salvage"       // a sunken treasure the crane pulls up
	SalvageChest Kind = "salvage_chest" // SALVAGE_TBOX placed by hand
	Pot          Kind = "pot"           // TSUBO
	Rock         Kind = "rock"          // STONE
	Rock2        Kind = "rock2"         // Stone2
	Barrel       Kind = "barrel"        // Obj_Barrel2, the boating-course barrels
	Box          Kind = "box"           // Obj_Movebox: a breakable crate's drop
	Leaves       Kind = "leaves"        // Obj_Leaves
	FacePillar   Kind = "face_pillar"   // Obj_Hcbh
	FaceStone    Kind = "face_stone"    // Obj_Homen
	Ospbox       Kind = "ospbox"        // Obj_Ospbox
	Beamos       Kind = "beamos"        // Bemos: the enemy drops it when destroyed
	SwitchItem   Kind = "switch_item"   // SW_ITEM
	Dig          Kind = "dig"           // TAG_KB_ITEM: a spot a pig digs up
)

// FlagKind is what records that a placement's item was taken.
type FlagKind string

const (
	// NoFlag: nothing does, so the item is back once its room reloads.
	NoFlag FlagKind = ""
	// TboxFlag is bit Bit (0-31) of the chest word mTbox of save table
	// Table (dSv_memBit_c, d_save.h:684; onTbox, d_save.cpp:1072-1075).
	TboxFlag FlagKind = "tbox"
	// ItemFlag is memory item bit Bit (0-0x3F) of save table Table
	// (dSv_info_c::isItem, d_save.cpp:1658-1659). The memBit has one item
	// word (mItem[1], d_save.h:686), so bits 0x20-0x3F share the first
	// word of mVisitedRoom (d_save.cpp:1112-1121).
	ItemFlag FlagKind = "item"
	// ZoneItemFlag is zone item bit Bit (0x40-0x4F) of room Room's zone
	// (d_save.cpp:1660-1667), which goes when the zone does (§7.1): it is
	// not kept.
	ZoneItemFlag FlagKind = "zone_item"
	// ChartFlag is "treasure chart Bit salvaged", the collect-map complete
	// bit (end_salvage, d_a_salvage.cpp:509-511).
	ChartFlag FlagKind = "chart"
	// OceanFlag is ocean salvage bit Bit of sea room Room (end_salvage,
	// d_a_salvage.cpp:514-518).
	OceanFlag FlagKind = "ocean"
	// SalvageEventFlag is the event bit daSalvage_c::m_savelabel[Bit]
	// (d_com_static.cpp:115+; end_salvage, d_a_salvage.cpp:520-522), one
	// of the bits the game clears again (§6.5's D8 list): not kept.
	SalvageEventFlag FlagKind = "salvage_event"
	// StageLifeFlag is STAGE_LIFE (dungeon item bit 4, d_save.h:644) of
	// save table Table, which item_func_utuwa_heart sets for the stage the
	// container is taken in, or for Forsaken Fortress (2) when that is the
	// sea (d_item.cpp:588-604). hearts uses it for heart containers.
	StageLifeFlag FlagKind = "stage_life"
	// BadFlag is an item bit the game can't record: 0x50-0x7E, where it
	// asserts as the item is created (dSv_info_c::isItem, d_save.cpp:
	// 1651-1652, through fopAcM_isItem, f_op_actor_mng.h:400-402) or
	// dropped (fopAcM_createItemFromTable and
	// fopAcM_createRaceItemFromTable, f_op_actor_mng.cpp:806, :903), or a
	// dig spot's bit byte 0x80-0xFE, a negative s8 that the same isItem
	// asserts against as the spot is created (d_a_tag_kb_item.cpp:11,
	// :42), and so halts (see Halts). Not kept.
	BadFlag FlagKind = "bad"
	// HaltFlag is a salvage point's ocean bit Bit of grid Room past what
	// dSv_ocean_c holds, grids 0-0x31 of bits 0-15: isOceanSvBit and
	// onOceanSvBit assert against it (d_save.cpp:1164-1173), so the game
	// halts as the point's room loads or it is salvaged. Not kept.
	HaltFlag FlagKind = "halt"
)

// Flag is the save bit that records a pickup.
type Flag struct {
	Kind  FlagKind `json:"kind,omitempty"`
	Table int      `json:"table,omitempty"` // save table: TboxFlag, ItemFlag, StageLifeFlag
	Room  int      `json:"room,omitempty"`  // ZoneItemFlag, OceanFlag, HaltFlag
	Bit   int      `json:"bit,omitempty"`
}

// Saved reports whether the flag outlives a stage change: a pickup with an
// unsaved flag can be taken again.
func (f Flag) Saved() bool {
	switch f.Kind {
	case TboxFlag, ItemFlag, ChartFlag, OceanFlag, StageLifeFlag:
		return true
	}
	return false
}

// Halts reports whether the game halts on the flag's save bit: it
// asserts against it, and JUT_ASSERT is live in retail and calls OSPanic
// (JUTAssert.h:9; m_Do_printf.cpp:208-230), the same as for an enemy's
// setID past 0x1FF. Such a pickup is never made, rather than made again.
func (f Flag) Halts() bool {
	return f.Kind == BadFlag || f.Kind == HaltFlag
}

func (f Flag) String() string {
	switch f.Kind {
	case NoFlag:
		return "unsaved"
	case TboxFlag:
		return fmt.Sprintf("tbox %d:%d", f.Table, f.Bit)
	case ItemFlag:
		return fmt.Sprintf("item %d:0x%02X", f.Table, f.Bit)
	case ZoneItemFlag:
		return fmt.Sprintf("zone item room %d:0x%02X (unsaved)", f.Room, f.Bit)
	case ChartFlag:
		return fmt.Sprintf("chart %d", f.Bit)
	case OceanFlag:
		return fmt.Sprintf("ocean room %d:%d", f.Room, f.Bit)
	case SalvageEventFlag:
		return fmt.Sprintf("salvage event label %d (game-cleared)", f.Bit)
	case StageLifeFlag:
		return fmt.Sprintf("stage life %d", f.Table)
	case BadFlag:
		return fmt.Sprintf("bad item bit 0x%02X (halts)", f.Bit)
	case HaltFlag:
		return fmt.Sprintf("ocean grid %d:%d (halts)", f.Room, f.Bit)
	}
	return fmt.Sprintf("flag %q", string(f.Kind))
}

// Where is one entry of a stage file. It leaves the position out, so
// that any result encodes as JSON (a NaN coordinate doesn't).
type Where struct {
	Stage string `json:"stage"`
	Room  int    `json:"room"`  // -1: the stage's own stage.dzs
	Layer int    `json:"layer"` // stage.AllLayers (-1) or 0-11
	Tag   string `json:"tag"`
	Index int    `json:"index"`
	Name  string `json:"name"`
	SetID uint16 `json:"set_id"`
}

// At is where p, an entry of s, is.
func At(s *stage.Stage, p stage.Placed) Where {
	return Where{
		Stage: s.Name, Room: p.Room, Layer: p.Layer, Tag: p.Tag, Index: p.Index,
		Name: p.Name, SetID: p.SetID,
	}
}

func (w Where) String() string {
	room := fmt.Sprintf("room %d", w.Room)
	if w.Room < 0 {
		room = "stage file"
	}
	layer := "all layers"
	if w.Layer != stage.AllLayers {
		layer = fmt.Sprintf("layer %d", w.Layer)
	}
	return fmt.Sprintf("%s %s, %s, %s#%d %q setID 0x%04X", w.Stage, room, layer, w.Tag, w.Index, w.Name, w.SetID)
}

// Placement is one entry that gives a known item.
type Placement struct {
	Where
	Kind Kind       `json:"kind"`
	Proc stage.Proc `json:"proc"`
	Item uint8      `json:"item"`
	Flag Flag       `json:"flag"`
}

// Find returns the placements in stages whose item want accepts, stage by
// stage in the given order, each stage's own entries first and then each
// room's, in creation order. An entry whose name l_objectName lacks is
// never created (dStage_actorCreate, d_stage.cpp:1300-1304) and is left
// out, as is one whose drop is random or empty. With no names table every
// entry is unnamed, and Find returns nothing.
func Find(stages []*stage.Stage, names *stage.ObjectNames, want func(item uint8) bool) []Placement {
	if names == nil {
		return nil
	}
	var out []Placement
	for _, s := range stages {
		if s == nil {
			continue
		}
		for _, p := range s.All() {
			proc, ok := names.Proc(p.Name)
			if !ok {
				continue
			}
			pl, ok := Decode(s, p, proc)
			if ok && want(pl.Item) {
				out = append(out, pl)
			}
		}
	}
	return out
}

// Decode reads p, an entry of s that creates proc, as a placement: ok is
// false for a class this package doesn't know, or an entry that gives no
// single known item.
func Decode(s *stage.Stage, p stage.Placed, proc stage.Proc) (Placement, bool) {
	d, ok := decoders[proc]
	if !ok {
		return Placement{}, false
	}
	item, flag, ok := d.decode(p.Actor, s.SaveTable, p.Room)
	if !ok {
		return Placement{}, false
	}
	return Placement{Where: At(s, p), Kind: d.kind, Proc: proc, Item: item, Flag: flag}, true
}

// decoder reads one class's entries. table is the stage's save table and
// room the room whose file holds the entry (-1 for the stage's own),
// which is the actor's home room (fopAc_Create, f_op_actor.cpp:280, from
// dStage_actorInit's room_no, d_stage.cpp:1701).
type decoder struct {
	kind   Kind
	decode func(a stage.Actor, table, room int) (item uint8, flag Flag, ok bool)
}

var decoders = map[stage.Proc]decoder{
	// A chest gives the item in angle z's high byte (getItemNo,
	// d_a_tbox.h:37; given at d_a_tbox.cpp:958-959) and records it in tbox
	// bit (prm >> 7) & 0x1F (getTboxNo, d_a_tbox.h:35): of the stage's
	// table, or of STAGE_SEA2 (1) for function types 7 and 8 (prm & 0x7F,
	// d_a_tbox.cpp:30-31, :396-398; checkOpen :314-321, OpenInit_com
	// :854-859). The function type also says when the chest appears (on a
	// switch, once a room is cleared of enemies); it gives the same item.
	stage.ProcTbox: {Chest, func(a stage.Actor, table, _ int) (uint8, Flag, bool) {
		item := uint8(uint16(a.Angle[2]) >> 8)
		if fn := a.Params & 0x7F; fn == 7 || fn == 8 {
			table = 1
		}
		return item, Flag{Kind: TboxFlag, Table: table, Bit: int(a.Params>>7) & 0x1F}, item != ItemNone
	}},

	// A field item is item prm & 0xFF with item bit (prm >> 8) & 0x7F
	// (daItem_prm, d_a_item.h:141-142; _daItem_create, d_a_item.cpp:
	// 217-237), except Blue Chu Jelly, whose bit is a switch (:229).
	stage.ProcItem: {FieldItem, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		item := uint8(a.Params)
		if item == ItemBlueJelly {
			return item, Flag{}, true
		}
		return item, itemBit(int(a.Params>>8)&0x7F, table, room), item != ItemNone
	}},

	// A race item is item prm & 0xFF with item bit (prm >> 8) & 0x7F
	// (d_a_race_item.cpp:61-64).
	ProcRaceItem: {RaceItem, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		item := uint8(a.Params)
		return item, itemBit(int(a.Params>>8)&0x7F, table, room), item != ItemNone
	}},

	// A salvage point holds item (prm >> 4) & 0xFF (daSalvage_prm,
	// d_salvage.cpp:12-19), which the crane hands to a salvage chest
	// (d_a_salvage.cpp:195-197). Its kind, prm >> 28, picks the record
	// end_salvage sets with save number (prm >> 20) & 0xFF
	// (d_a_salvage.cpp:503-523): kind 0 a treasure chart, kinds 2-4 an
	// ocean bit, kind 6 an event label. A chart's treasure has several
	// points, of which each file uses one (dComIfGs_getRandomSalvagePoint,
	// d_salvage.cpp:92-93).
	//
	// A point dSalvage_control_c::entry refuses (d_salvage.cpp:50-82) is
	// left out: a sea map room outside 1-64 (the actor's room, or
	// (prm >> 12) & 0xFF in room 0), a kind 0 save number outside 1-128, a
	// kind 2 or 3 one over 15, a kind 2 without a switch (angle z & 0xFF,
	// d_salvage.cpp:14).
	//
	// dSv_ocean_c holds grids 0-0x31 of 16 bits, and isOceanSvBit and
	// onOceanSvBit assert outside them (d_save.cpp:1164-1173). For kinds
	// 2-4, entry checks the bit for the map room unless the save number is
	// 31 (d_salvage.cpp:96-105), while kind 4's night check and end_salvage
	// use the actor's room (mRoomNo, d_salvage.cpp:115; d_a_salvage.cpp:
	// 323-326, :504, :517). So a map room past 0x31, or a kind 4 save
	// number over 15 (31 included; entry bounds only kinds 2 and 3), halts
	// the game: HaltFlag. Otherwise the bit is recorded for the actor's
	// room, which for a point in room 0 is grid 0. Entry never reads that
	// grid, so such a point can be salvaged again: unsaved.
	ProcSalvage: {Salvage, func(a stage.Actor, _, room int) (uint8, Flag, bool) {
		item := uint8(a.Params >> 4)
		kind := a.Params >> 28
		save := int(a.Params>>20) & 0xFF
		cmap := room
		if room == 0 {
			cmap = int(a.Params>>12) & 0xFF
		}
		switch {
		case cmap <= 0 || cmap > 64,
			(kind == 2 || kind == 3) && save > 15,
			kind == 0 && (save < 1 || save > 128),
			kind == 2 && uint16(a.Angle[2])&0xFF == 0xFF,
			item == ItemNone:
			return 0, Flag{}, false
		}
		switch kind {
		case 0:
			return item, Flag{Kind: ChartFlag, Bit: save}, true
		case 2, 3, 4:
			switch {
			case cmap > 0x31 || save > 15:
				grid := cmap // entry's check
				if save == 31 {
					grid = room // entry skips it; the night check and end_salvage don't
				}
				return item, Flag{Kind: HaltFlag, Room: grid, Bit: save}, true
			case room == 0:
				return item, Flag{}, true
			}
			return item, Flag{Kind: OceanFlag, Room: room, Bit: save}, true
		case 6:
			return item, Flag{Kind: SalvageEventFlag, Bit: save}, true
		}
		return item, Flag{}, true
	}},

	// A salvage chest gives item prm & 0xFF (daSTBox_prm::getItemNo,
	// d_a_salvage_tbox.h:104; d_a_salvage_tbox.cpp:217, :370) and records
	// nothing itself; the salvage point that makes one does.
	ProcSalvageChest: {SalvageChest, func(a stage.Actor, _, _ int) (uint8, Flag, bool) {
		item := uint8(a.Params)
		return item, Flag{}, item != ItemNone
	}},

	// These break or open into fopAcM_createItemFromTable(item, bit) with
	// a 6-bit item and a 7-bit item bit.
	ProcTsubo:         fromTable(Pot, 0, 16),       // d_a_tsubo.h:28-38, :190-191; damaged, d_a_tsubo.cpp:2208-2212
	ProcStone:         fromTable(Rock, 0, 16),      // d_a_stone.h:93-97, :106-107; damaged, d_a_stone.cpp:484-489
	ProcStone2:        fromTable(Rock2, 0, 16),     // d_a_stone2.h:47-54, :72-73; damaged, d_a_stone2.cpp:250-254
	stage.ProcMovebox: fromTable(Box, 0, 16),       // d_a_obj_movebox.h:150-154, :190-191; make_item, d_a_obj_movebox.cpp:1697-1704
	ProcLeaves:        fromTable(Leaves, 0, 6),     // d_a_obj_leaves.h:16-20, :27-28; d_a_obj_leaves.cpp:629-636
	ProcHcbh:          fromTable(FacePillar, 0, 6), // d_a_obj_hcbh.h:15-18, :23-24; make_item, d_a_obj_hcbh.cpp:327-333
	ProcHomen:         fromTable(FaceStone, 12, 0), // d_a_obj_homen.h:29-33, :45-46; make_item, d_a_obj_homen.cpp:453-462

	// A Beamos keeps item and bit in angle z: z & 0x3F and (z >> 6) &
	// 0x7F (d_a_obj_bemos.cpp:285-286, :336-337), and drops them when
	// destroyed (:1169).
	ProcBemos: {Beamos, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		z := uint32(uint16(a.Angle[2]))
		return tableItem(int(z&0x3F), int(z>>6)&0x7F, table, room)
	}},

	// A switch item drops table item (prm >> 8) & 0x3F with bit (prm >>
	// 14) & 0x7F when hit (daSwItem_prm, d_a_switem.h:28-29; d_a_switem.cpp:
	// 128-140). Once that bit is set it still drops, with no bit (:130-
	// 132), so its item comes back.
	ProcSwItem: {SwitchItem, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		return tableItem(int(a.Params>>8)&0x3F, int(a.Params>>14)&0x7F, table, room)
	}},

	// An Ospbox drops table item prm & 0x3F with no bit when its spec,
	// (prm >> 8) & 7, picks the box itself (d_a_obj_ospbox.h:11-21; the
	// pf_name table and drop, d_a_obj_ospbox.cpp:123-151); specs 1-3 make
	// a pig instead.
	ProcOspbox: {Ospbox, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		if spec := (a.Params >> 8) & 7; spec >= 1 && spec <= 3 {
			return 0, Flag{}, false
		}
		return tableItem(int(a.Params&0x3F), 0x7F, table, room)
	}},

	// A boating-course barrel drops item prm & 0x3F with bit (prm >> 16) &
	// 0x7F through fopAcM_createRaceItemFromTable, which reads items the
	// same way (d_a_obj_barrel2.h:52-57, :99-104; d_a_obj_barrel2.cpp:
	// 273-281; f_op_actor_mng.cpp:902-920).
	ProcBarrel2: fromTable(Barrel, 0, 16),

	// A pig's dig spot holds item prm & 0xFF and item bit (prm >> 8) &
	// 0xFF; (prm >> 16) & 0xFF is an enemy kind and prm >> 24 a switch
	// (daTagKbItem_prm, d_a_tag_kb_item.cpp:9-13). When a pig (fpcNm_KB_e)
	// digs it (d_a_kb.cpp:1995-1997, kb_dig, d_com_static.cpp:381-386),
	// dig_main creates that item, unless it is none, with
	// fopAcM_fastCreateItem and the bit (d_com_static.cpp:389-449, :408,
	// :413, :420-423), which packs it as a byte into the item's parameters
	// (f_op_actor_mng.cpp:1072-1076): the field item then keeps bit & 0x7F
	// and is never created again once that bit is set (_daItem_create,
	// d_a_item.cpp:227-233), except Blue Chu Jelly, as for FieldItem. The
	// spot is not created either once its bit is set, unless the bit is
	// 0x1F (d_a_tag_kb_item.cpp:42-47); a spot with bit 0x1F can come back,
	// but not its item. A spot of item none only sets an enemy kind for
	// the pig (d_com_static.cpp:439-443).
	//
	// The bit is an s8, sign-extended into the int mItemBitNo
	// (d_a_tag_kb_item.cpp:11; d_a_tag_kb_item.h:21), and _create checks
	// it unmasked, whatever the item, Blue Chu Jelly included
	// (d_a_tag_kb_item.cpp:42): isItem asserts unless it is 0-0x4F, -1 or
	// 127 (d_save.cpp:1651-1652), so a byte of 0x80-0xFE or 0x50-0x7E
	// halts as the spot's room loads. Byte 0xFF is s8 -1, which isItem
	// reads as unset (d_save.cpp:1654-1656); its item gets 0x7F.
	ProcTagKbItem: {Dig, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		item := uint8(a.Params)
		var f Flag
		switch b := int(a.Params>>8) & 0xFF; {
		case b == 0xFF:
		case b >= 0x80:
			f = Flag{Kind: BadFlag, Bit: b}
		default:
			f = itemBit(b, table, room)
		}
		if item == ItemBlueJelly {
			if f.Halts() {
				return item, f, true
			}
			return item, Flag{}, true
		}
		return item, f, item != ItemNone
	}},

	// ProcGrass is left out on purpose: grass and flowers drop only random
	// table picks (see ProcGrass).
}

// fromTable is a class that passes a 6-bit item at bit itemShift and a
// 7-bit item bit at bitShift of its parameters to
// fopAcM_createItemFromTable.
func fromTable(kind Kind, itemShift, bitShift uint) decoder {
	return decoder{kind, func(a stage.Actor, table, room int) (uint8, Flag, bool) {
		return tableItem(int(a.Params>>itemShift)&0x3F, int(a.Params>>bitShift)&0x7F, table, room)
	}}
}

// tableItem is what fopAcM_createItemFromTable(item, bit) makes: item
// itself for 0-0x1F, a random pick from drop table item - 0x20 for
// 0x20-0x3E, and nothing for 0x3F (f_op_actor_mng.cpp:805-899; the race
// version is the same, :902-920). Only the first is a placement.
func tableItem(item, bit, table, room int) (uint8, Flag, bool) {
	if item >= 0x20 {
		return 0, Flag{}, false
	}
	return uint8(item), itemBit(bit, table, room), true
}

// itemBit is the flag item bit bit records for an item in room room of a
// stage on save table table (dSv_info_c::isItem, d_save.cpp:1650-1668):
// 0x7F is none, 0-0x3F the table's memory bits, 0x40-0x4F the room's
// zone; the rest the game rejects.
func itemBit(bit, table, room int) Flag {
	switch {
	case bit == 0x7F:
		return Flag{}
	case bit < 0x40:
		return Flag{Kind: ItemFlag, Table: table, Bit: bit}
	case bit < 0x50:
		return Flag{Kind: ZoneItemFlag, Room: room, Bit: bit}
	}
	return Flag{Kind: BadFlag, Bit: bit}
}

// Source is one pickup: an item and every entry that places it. Entries
// that give the same item under the same saved flag (one chest on two
// layers, say) are one source: taking it sets the bit, and the game then
// creates none of them again. Each placement with an unsaved flag is a
// source of its own.
type Source struct {
	Kind Kind    `json:"kind"` // the first entry's
	Item uint8   `json:"item"`
	Flag Flag    `json:"flag"`
	At   []Where `json:"at"`
}

// Group groups placements into sources, in the order each source first
// appears.
func Group(ps []Placement) []Source {
	type key struct {
		flag Flag
		item uint8
	}
	var out []Source
	index := make(map[key]int)
	for _, p := range ps {
		if p.Flag.Saved() {
			k := key{p.Flag, p.Item}
			if i, ok := index[k]; ok {
				out[i].At = append(out[i].At, p.Where)
				continue
			}
			index[k] = len(out)
		}
		out = append(out, Source{Kind: p.Kind, Item: p.Item, Flag: p.Flag, At: []Where{p.Where}})
	}
	return out
}
