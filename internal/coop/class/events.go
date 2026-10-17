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
	}
)
