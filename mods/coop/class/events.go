package class

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// namedEventBits[i] is the OR of every named event bit in byte i of
// dSv_event_c: the dSv_event_flag_c bit enum (d_save_event_flag.inc:6-494),
// 487 bits over bytes 0x00-0x41. It is the most the story sync may ever
// carry. The M4 SyncMask (§6.5) is a subset of it, minus the LocalOnly,
// game-cleared and refight bits.
var namedEventBits = [addr.EventBitBytes]byte{
	0xBF, 0xEF, 0xF9, 0xDF, 0xA7, 0xFF, 0x4F, 0xF7, // 0x00
	0xFF, 0x7F, 0xFF, 0xEF, 0xFE, 0xFE, 0xFF, 0xFF, // 0x08
	0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFD, // 0x10
	0xFF, 0xFF, 0xBF, 0xF5, 0xEF, 0xFF, 0xFF, 0xFF, // 0x18
	0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xEF, 0xDF, // 0x20
	0xFF, 0xFF, 0xFF, 0xFC, 0xEF, 0xFF, 0xFF, 0xFF, // 0x28
	0xFF, 0xE7, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // 0x30
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xF7, 0xF3, // 0x38
	0xEF, 0x80, // 0x40
}

// Event registers (bytes 0x79-0xFF) are per player except the ones below
// (§3 "Event registers"). Each entry is a register id 0xHHMM: byte HH,
// field mask MM.
var (
	// figurineRegs are the Nintendo Gallery figurine bitfields, one bit per
	// figurine (l_figure_comp, d_a_obj_figure.cpp:43-61 and
	// d_a_npc_mt.cpp:304). The game only ever sets their bits
	// (daNpcMt_c::setFigure, d_a_npc_mt.cpp:1365-1373), and a second quest
	// keeps them (l_holdEventReg, d_save.cpp:1402-1420). They merge by OR.
	//
	// The list skips 0x85-0x8B, which hold Beedle points, GHOST_SHIP,
	// LETTER_ARYLL and four unrelated registers.
	figurineRegs = []uint16{
		0x80FF, 0x81FF, 0x82FF, 0x83FF, 0x84FF,
		0x8CFF, 0x8DFF, 0x8EFF, 0x8FFF, 0x90FF, 0x91FF, 0x92FF, 0x93FF, 0x94FF, 0x95FF,
		0x9CFF, 0xB1FF,
	}

	// warpPotRegs are the warp pot registers, one per pot set
	// (daObj_Warpt_c::m_event_reg, d_a_obj_warpt.cpp:37-44; 0xA207 is
	// DRC's). Each pot of a set owns one bit of the 0x07 field (bit 1, 2
	// or 4, getArg, d_a_obj_warpt.cpp:729-743). The game only ever sets
	// them (onWarpBit, :285-289, its only writer), so they merge by OR
	// within the field; the byte's other bits stay local.
	warpPotRegs = []uint16{0xA207, 0xA107, 0xA007, 0x9F07, 0xA307, 0xA407}

	// maxRegs only move forward, so they merge by MAX of the field (the
	// byte masked by MM); the byte's other bits stay local. Monotone is
	// inferred from the setters named here.
	maxRegs = []uint16{
		// Letters step NOSEND 0 -> SEND 1 -> STOCK 2 -> READ 3
		// (dLetterStts_e and the setters, d_letter.cpp:10-62).
		0x7A03, // LETTER_ROCK_SPIRE_SHOP_AD (d_save_event_flag.inc:499)
		0x7B03, // LETTER_ORCA
		0x7C03, // LETTER_BAITO
		0x7D03, // LETTER_BOMBS_AD
		0x8B03, // LETTER_ARYLL (d_save_event_flag.inc:516)
		0x9D03, // LETTER_GRANDMA (:534)
		0xAC03, // LETTER_BAITOS_MOM (:549)
		0xAE03, // LETTER_HOSKITS_GIRLFRIEND (:551)
		0xAF03, // LETTER_GOLD_MEMBERSHIP
		0xB003, // LETTER_SILVER_MEMBERSHIP
		0xB203, // LETTER_TINGLE (:555)
		0xB503, // LETTER_KOMALIS_FATHER (:556)

		// Beedle points: only ever incremented, capped at 0xFF
		// (d_a_npc_bs1.cpp:939-941).
		0x86FF,

		// GHOST_SHIP: set to 3 once the ship is cleared, its only writer
		// (daTag_Gship_c::modeClearEventInit, d_a_tag_ghostship.cpp:31-39).
		0x8803,

		// Orca's record level, 0-4 (UNK_D003, d_save_event_flag.inc:581):
		// setClearRecord (d_a_npc_ji1.cpp:409-422) writes a new level only
		// above the old one, 4 with 0x0F20 at 1000 hits. setEventReg clears
		// the id's 0x03 and ORs the value in unmasked (d_save.cpp:1202-1205),
		// so level 4 is bit 0x04, which the field carries: 0xD007, not the
		// enum's 0xD003. Once 0x0F20 is set, isClearRecord refuses (:395-406)
		// and the other caller runs only at 1000 hits (:4033-4051), which
		// writes 4 again, so the field never falls. createItem pays a purple
		// and an orange rupee at levels 1 and 2 (:1508-1512), which a
		// partner still at a lower level could otherwise earn again; the
		// heart piece is 0x0F10's (story bits, synced). Adapted from the idea
		// in datstarkey/ww-online WWOnline.Shared/Models/StoryFlags.cs:91-93
		// (MIT); checked here against the decomp.
		0xD007,

		// Koboli's mail-sorting level, 0-3 (UNK_C203, d_save_event_flag.inc:
		// 568): each level's winning end message steps to the next level's
		// write, 0x1A37 -> 0x1A38 sets 1, 0x1A4C -> 0x1A4D sets 2, 0x1A57 ->
		// 0x1A58 sets 3 (daNpc_Bmsw_getGameEndMsg, d_a_npc_bmsw.cpp:192-248;
		// :365-380, :430-451), its only writers. The level picks the pay
		// rate (setGameGetRupee, :813-854) and at 3 brings Baito in as Btsw
		// (:1144-1150), and Btsw2 stays away (d_a_npc_btsw2.cpp:545-547).
		0xC203,

		// UNVERIFIED: Sploosh Kaboom's and barrel shooting's prizes won.
		// d_a_npc_kg1 and d_a_npc_kg2 are not decompiled (symbols only), so
		// this is datstarkey/ww-online's reading of the vanilla RELs
		// (WWOnline.Shared/Models/StoryFlags.cs:96-99, docs/hearts.md §3.3,
		// MIT): each counts first prizes won, capped at 3, and indexes the
		// prize table {heart piece, chart, rupees}, so a partner who has not
		// won could take the first prize's heart piece again. Both ids are
		// in the enum (UNK_FE07, UNK_B703, d_save_event_flag.inc:627, :558);
		// their writers are not. Drop these two lines if a live run or a
		// disassembly disagrees.
		0xFE07,
		0xB703,
	}

	// orRegs are side-quest registers whose bits the game only ever sets
	// within the field; they merge by OR, the byte's other bits stay
	// local.
	orRegs = []uint16{
		// The pigs put in Outset's pig pen (UNK_BFFF, d_save_event_flag.inc:
		// 565): a pig landing in the pen ORs in 1 << (shape & 3) (d_a_kb.cpp:
		// 2170-2175), so the field is 0x0F, not the enum's 0xFF. The only
		// clear is behind REG8_S(8), a debug HIO register (:2496-2501).
		// Once 0x0520 is set, the bits pick which pig Outset keeps
		// (:2504-2530). No reward reads it; shared so both games keep the
		// same pig. From datstarkey/ww-online StoryFlags.cs:102-103 (MIT).
		0xBF0F,
	}

	// Left per player on purpose: 0xC0FF, the Joy Pendants given to Mrs.
	// Marie. receivePendant takes them out of the giver's own spoils bag
	// (d_a_npc_ho.cpp:70-82), and bag contents are each player's (§1),
	// so the count she keeps goes with it: her first talk at 0 (:398-405)
	// and the Hero's Charm at 40 (:329).
)
