package class

import (
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Story bits (§6.5). The story sync carries the named event bits of
// mEvent bytes 0x00-0x41 (namedEventBits) minus three lists that
// scripts/gen_event_masks.py derives from the decomp into syncmask_gen.go:
//
//   - gameClearedBits (§6.5's D8): every bit a dComIfGs_offEventBit call
//     the GZLE01 build compiles clears. OR-merging one would fight the
//     game: Carlov's figurine cycle, the daily and weekly resets, the
//     moonlight salvage points.
//   - localOnlyBits: the game-cleared bits plus the bits that are each
//     player's own although nothing clears them (ENDLESS_NIGHT, RODE_KORL,
//     0x3B20; MOVED_HYRULE_STATUE is both).
//   - refightBits: set as a dungeon's Ganon's Tower refight snapshot is
//     taken.
//
// What remains is syncMask. The hub opens exactly it in its policy (the
// event bytes are OR_MASKED; the class mask stays namedEventBits, the most
// the sync may ever carry), the sender batches its new bits, and the
// receiver applies them on its next stage transition, keeping back the
// ones holds names until their condition holds in its own game (package
// story).
//
// Doc vs decomp: §6.5 counts 34 D8 bits, every bit the 50 offEventBit
// calls outside d_save.cpp clear. Four of those calls are compiled only
// into the demo build: d_a_npc_rsh1.cpp:1703, :1709 and :1715 (the
// HIO toggles under #if VERSION == VERSION_DEMO, :1690-1718) and
// d_a_obj_YLzou.cpp:482 (:480-485). Both files match GZLE01 byte for
// byte (configure.py:1739, :1753), so GZLE01 never clears 0x0E08, 0x1110
// or 0x1108: they are not game-cleared there, and they sync (0x1110 and
// 0x1108 are set as daNpc_Rsh1_c's talks end, d_a_npc_rsh1.cpp:684-690,
// and 0x1108 lets daNpcRoten_c be created, d_a_npc_roten.cpp:1622).
// D8 has 31 bits.
// 0x3820 stays, cleared in GZLE01 at d_a_obj_YLzou.cpp:143. 0x2D01 stays
// out as §6.5 says, although its only clear is the map-select debug
// scene's toggle (d_s_menu.cpp:244, which GZLE01 does compile).

// EventBit is one story bit: id 0xHHLL is mEvent byte HH, mask LL
// (onEventBit, d_save.cpp:1187-1189), with its d_save_event_flag.inc name.
type EventBit struct {
	ID   uint16
	Name string
	// Cite is where the decomp shows why the bit is listed:
	// zeldaret/tww file:line, "; "-separated.
	Cite string
}

// EventSite is one dComIfGs_offEventBit call outside d_save.cpp.
type EventSite struct {
	ID uint16
	At string // zeldaret/tww file:line
	// GZLE01 is false for a call the GZLE01 build doesn't compile: one a
	// version guard removes from a file the decomp matches for GZLE01.
	GZLE01 bool
}

// SyncMask returns, for each mEvent byte 0x00-0x41, the story bits that
// sync.
func SyncMask() [addr.EventBitBytes]byte { return syncMask }

// SyncMaskOf returns the story bits of mEvent byte i that sync: 0 for a
// byte outside 0x00-0x41.
func SyncMaskOf(i int) byte {
	if i < 0 || i >= addr.EventBitBytes {
		return 0
	}
	return syncMask[i]
}

// EventSyncs reports whether event bit id is a story bit that syncs: a
// named bit of bytes 0x00-0x41 whose every mask bit is in the SyncMask.
func EventSyncs(id uint16) bool {
	b := addr.EventByte(id)
	return b.Mask != 0 && SyncMaskOf(b.Off)&b.Mask == b.Mask
}

// LocalOnly returns §6.5's LocalOnly bits, in id order.
func LocalOnly() []EventBit { return slices.Clone(localOnlyBits) }

// GameCleared returns §6.5's D8: the bits the GZLE01 game clears, in id
// order. Every one is also LocalOnly.
func GameCleared() []EventBit { return slices.Clone(gameClearedBits) }

// Refight returns the four refight snapshot bits.
func Refight() []EventBit { return slices.Clone(refightBits) }

// OffEventBitSites returns every dComIfGs_offEventBit call outside
// d_save.cpp, in file order.
func OffEventBitSites() []EventSite { return slices.Clone(offEventBitSites) }

// HoldUntil is what releases a hold (§6.5).
type HoldUntil uint8

const (
	// UntilLocal: the receiver's own game has event bit Arg. Bits the
	// same write would set don't count.
	UntilLocal HoldUntil = iota + 1
	// UntilWith: the receiver has event bit Arg, or gets it in the same
	// write, so the two arrive together.
	UntilWith
	// UntilMasterSword: the receiver's sword slot, mSelectEquip[0], holds
	// a Master Sword (MasterSwords).
	UntilMasterSword
)

func (u HoldUntil) String() string {
	switch u {
	case UntilLocal:
		return "until-local"
	case UntilWith:
		return "until-with"
	case UntilMasterSword:
		return "until-master-sword"
	}
	return "HoldUntil(?)"
}

// Hold is a story bit the receiver keeps out of its game, although the
// world has it, until a condition holds in that game (§6.5): applied
// early, the bit would move where the receiver's file resumes, pick a
// layer ahead of its own story (Outset, the Forsaken Fortress), or keep
// it from boarding its boat.
type Hold struct {
	EventBit
	Until HoldUntil
	Arg   uint16 // the event bit UntilLocal and UntilWith wait for
	Why   string
}

// Event bits the holds name (d_save_event_flag.inc).
const (
	evMetKorl         = 0x0F80 // MET_KORL, :111
	ev0801            = 0x0801 // UNK_0801, :66
	ev0808            = 0x0808 // UNK_0808, :63
	ev2401            = 0x2401 // UNK_2401, :279
	ev0101            = 0x0101 // UNK_0101, :20
	ev0E20            = 0x0E20 // UNK_0E20, :105
	ev0520            = 0x0520 // UNK_0520, :41
	ev1820            = 0x1820 // UNK_1820, :183
	ev2D01            = 0x2D01 // UNK_2D01, :346 (LocalOnly)
	ev2D10            = 0x2D10 // UNK_2D10, :342
	evZeldaAwakened   = 0x2D02 // ZELDA_AWAKENED, :345
	evHyruleCourtyard = 0x3804 // HYRULE_COURTYARD_CUTSCENE, :430
	ev3E10            = 0x3E10 // UNK_3E10, :476
	ev3F80            = 0x3F80 // UNK_3F80, :480
)

// holds are §6.5's receiver-side holds.
//
// dComIfGs_setGameStartStage picks where a loaded file resumes from the
// first of RODE_KORL, MET_KORL, 0x0801, 0x0808 and 0x2401 it has
// (l_checkData, d_com_inf_game.cpp:1304-1319). RODE_KORL is LocalOnly. A
// partner's MET_KORL would send a file still on Outset to Windfall, and
// each later bit to its own stage, so all four wait until the receiver has
// MET_KORL itself: from then on MET_KORL decides, and the other three
// change nothing. MET_KORL is set as the MEETSHISHIOH event starts
// (d_event_manager.cpp:599-600).
//
// getLayerNo picks Outset's layer (sea r44) from the first of 0x0520,
// 0x0E20 and 0x0101 it has (d_com_inf_game.cpp:193-200), and Asoko's
// from 0x0520 (:214-217). Layer 4/5 places no Tetra and no pirate ship,
// so a partner's 0x0520 would leave a player who has not sailed yet with
// no way off the island, and a partner's 0x0101 or 0x0E20 moves Outset
// to the kidnapping or departure layer ahead of that player's own story.
// The three wait for the receiver's own MET_KORL too. Vanilla sets them
// all before it (inferred from their readers: 0x0520's setter is not in
// the decomp, the overworld slice's C1), so from then on they change
// nothing the receiver's own game would not have.
//
// The same table puts the Forsaken Fortress (sea r1) on layer 3 with
// 0x1820, and ma2room loads ma3room's files with it (d_resorce.cpp:708).
// Warpdm20 sets it as it is created once ZELDA_AWAKENED is set
// (d_a_warpdm20.cpp:123-125), on the far side of Hyrule, so it waits for
// the receiver's own rescue in FF2, 0x2D01, which is LocalOnly and so
// always the receiver's own.
//
// daShip_c takes the boat's boarding action away while any of three
// clauses holds (d_a_ship.cpp:4224-4229): 0x2D10 without a Master Sword
// equipped (daPy_lk_c::checkMasterSwordEquip, d_a_player.h:600-604),
// HYRULE_COURTYARD_CUTSCENE without ZELDA_AWAKENED, and 0x3E10 without
// 0x3F80. §6.5 names the first two; the third is held the same way
// (Appendix C item 59). The last two wait only for their partner bit,
// which may come in the same write.
var holds = []Hold{
	{EventBit{evMetKorl, "MET_KORL", "d_com_inf_game.cpp:1306"}, UntilLocal, evMetKorl,
		"moves where the file resumes, to Windfall"},
	{EventBit{ev0801, "UNK_0801", "d_com_inf_game.cpp:1307"}, UntilLocal, evMetKorl,
		"moves where the file resumes, to MajyuE"},
	{EventBit{ev0808, "UNK_0808", "d_com_inf_game.cpp:1308"}, UntilLocal, evMetKorl,
		"moves where the file resumes, to MajyuE point 18"},
	{EventBit{ev2401, "UNK_2401", "d_com_inf_game.cpp:1309"}, UntilLocal, evMetKorl,
		"moves where the file resumes, to A_umikz point 204"},
	{EventBit{ev0101, "UNK_0101", "d_com_inf_game.cpp:199-200; d_a_npc_zl1.cpp:302"}, UntilLocal, evMetKorl,
		"moves Outset to layer 9 and changes Tetra's first state"},
	{EventBit{ev0E20, "UNK_0E20", "d_com_inf_game.cpp:197-198; d_a_npc_zl1.cpp:313; d_a_spc_item01.cpp:79"}, UntilLocal, evMetKorl,
		"moves Outset to layer 2/3, Tetra's departure offer and the shield"},
	{EventBit{ev0520, "UNK_0520", "d_com_inf_game.cpp:195-196, :214-217; d_stage.cpp:2324-2329"}, UntilLocal, evMetKorl,
		"moves Outset to layer 4/5 (no Tetra, no ship), Asoko to layer 2/3 and Asoko's deck exit"},
	{EventBit{ev1820, "UNK_1820", "d_com_inf_game.cpp:207-208; d_resorce.cpp:708; d_a_warpdm20.cpp:123-125"}, UntilLocal, ev2D01,
		"moves the Forsaken Fortress to layer 3 and ma2room to ma3room's files"},
	{EventBit{ev2D10, "UNK_2D10", "d_a_ship.cpp:4225, :4229"}, UntilMasterSword, 0,
		"keeps Link off the boat while no Master Sword is equipped"},
	{EventBit{evHyruleCourtyard, "HYRULE_COURTYARD_CUTSCENE", "d_a_ship.cpp:4226, :4229"}, UntilWith, evZeldaAwakened,
		"keeps Link off the boat until ZELDA_AWAKENED"},
	{EventBit{ev3E10, "UNK_3E10", "d_a_ship.cpp:4227, :4229"}, UntilWith, ev3F80,
		"keeps Link off the boat until 0x3F80"},
}

// Holds returns the receiver-side holds, MET_KORL's first.
func Holds() []Hold { return slices.Clone(holds) }

// masterSwords are the items checkMasterSwordEquip accepts in the sword
// slot mSelectEquip[0] (d_a_player.h:600-604; dItemNo_MASTER_SWORD_1-3,
// d_item_data.h:64, :65, :69; the slot, d_save.h:46).
var masterSwords = []byte{save.ItemMasterSword1, save.ItemMasterSword2, save.ItemMasterSword3}

// MasterSwords returns the item numbers of the three Master Swords.
func MasterSwords() []byte { return slices.Clone(masterSwords) }
