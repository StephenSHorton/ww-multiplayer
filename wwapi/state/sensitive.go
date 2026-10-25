package state

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// Fold is how Read combines its two reads of one sensitive save byte.
type Fold uint8

const (
	// FoldNone keeps the first read: counts, pointers, names and other
	// bytes a torn read can't make look gained.
	FoldNone Fold = iota
	// FoldBits keeps a bit within Mask only if both reads have it (flags,
	// which only ever turn on).
	FoldBits
	// FoldMax keeps the lower of the two reads' values within Mask (fields
	// that only ever grow).
	FoldMax
)

// Span is one run of dSv_save_c image bytes (addr.Off* offsets) that fold
// alike. Bits outside Mask keep the first read.
type Span struct {
	Off, Len int
	Fold     Fold
	Mask     byte
}

// Options tunes Read.
type Options struct {
	// Sensitive lists the image bytes Read folds (a later span wins where
	// two overlap); bytes in no span keep the first read. The live memBit
	// folds as the saved slots do, and the dan switches and every zone's
	// flags always fold as flags. nil means DefaultSensitive().
	Sensitive []Span
}

// DefaultSensitive returns the image bytes that merge between co-op
// players: every flag and every only-growing field the game keeps in the
// save, with the masks of §3. It is the fold Read has always used, and a
// co-op test pins it, byte for byte, to the co-op class table.
func DefaultSensitive() []Span {
	var s []Span
	bits := func(off, n int, mask byte) { s = append(s, Span{Off: off, Len: n, Fold: FoldBits, Mask: mask}) }
	maxOf := func(off int, mask byte) { s = append(s, Span{Off: off, Len: 1, Fold: FoldMax, Mask: mask}) }

	maxOf(addr.OffWalletSize, 0xFF)
	// mGetItem[0..2] bits 1-7 record the bottle contents a player has held
	// (onBottleItem, d_save.cpp:224-282), which stay each player's.
	bits(addr.OffGetItem, 3, 0x01)
	bits(addr.OffGetItem+3, addr.GetItemSize-3, 0xFF)
	maxOf(addr.OffArrowMax, 0xFF)
	maxOf(addr.OffBombMax, 0xFF)
	bits(addr.OffBeastFlags, 1, 0xFF)
	bits(addr.OffBaitFlags, 1, 0xFF)
	bits(addr.OffCollectFlags, 4, 0xFF)
	// mCollect[4] bit 1 is whether the Hero's Charm is worn
	// (d_menu_collect.cpp:1283-1286).
	bits(addr.OffCollectFlags+4, 1, 0x01)
	bits(addr.OffTact, 1, 0xFF)
	bits(addr.OffTriforce, 1, 0xFF)
	bits(addr.OffSymbol, 1, 0xFF)
	bits(addr.OffMapGet, addr.OffMap71-addr.OffMapGet, 0xFF) // charts and mFmapBits
	bits(addr.OffMapTriforce, 1, 0xFF)

	for t := range addr.TableCount {
		m := addr.OffMemBit(t)
		// mTbox, mSwitch, mItem and mVisitedRoom.
		bits(m+addr.MemBitOffTbox, addr.MemBitOffKeyNum-addr.MemBitOffTbox, 0xFF)
		bits(m+addr.MemBitOffDungeonItem, 1, addr.DungeonItemMask)
	}

	bits(addr.OffOcean, addr.OceanSize, 0xFF)

	for i, mask := range namedEventBits {
		bits(addr.OffEvent+i, 1, mask)
	}
	for _, list := range [][]uint16{figurineRegs, warpPotRegs, orRegs} {
		for _, id := range list {
			b := addr.EventByte(id)
			bits(addr.OffEvent+b.Off, 1, b.Mask)
		}
	}
	for _, id := range maxRegs {
		b := addr.EventByte(id)
		maxOf(addr.OffEvent+b.Off, b.Mask)
	}
	return s
}

// namedEventBits[i] is the OR of every named event bit in byte i of
// dSv_event_c: the dSv_event_flag_c bit enum (d_save_event_flag.inc:6-494),
// 487 bits over bytes 0x00-0x41.
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

// The event registers that only ever gain (register ids 0xHHMM: byte HH,
// field mask MM). The rest are each player's.
var (
	// figurineRegs are the Nintendo Gallery figurine bitfields, one bit
	// per figurine, only ever set (daNpcMt_c::setFigure, d_a_npc_mt.cpp:
	// 1365-1373).
	figurineRegs = []uint16{
		0x80FF, 0x81FF, 0x82FF, 0x83FF, 0x84FF,
		0x8CFF, 0x8DFF, 0x8EFF, 0x8FFF, 0x90FF, 0x91FF, 0x92FF, 0x93FF, 0x94FF, 0x95FF,
		0x9CFF, 0xB1FF,
	}

	// warpPotRegs are the warp pot registers, one bit per pot in the 0x07
	// field, only ever set (daObj_Warpt_c::onWarpBit, d_a_obj_warpt.cpp:
	// 285-289).
	warpPotRegs = []uint16{0xA207, 0xA107, 0xA007, 0x9F07, 0xA307, 0xA407}

	// maxRegs only move forward: the letters (NOSEND 0 -> SEND 1 -> STOCK
	// 2 -> READ 3, d_letter.cpp:10-62), Beedle points (d_a_npc_bs1.cpp:
	// 939-941), GHOST_SHIP (d_a_tag_ghostship.cpp:31-39), Orca's record
	// level with its unmasked level 4 (d_a_npc_ji1.cpp:409-422), Koboli's
	// level (d_a_npc_bmsw.cpp:430-451). Sploosh Kaboom's and barrel
	// shooting's prizes won (0xFE07, 0xB703) stay out on purpose
	// (mods/coop/class/events.go says why).
	maxRegs = []uint16{
		0x7A03, 0x7B03, 0x7C03, 0x7D03, 0x8B03, 0x9D03, 0xAC03, 0xAE03,
		0xAF03, 0xB003, 0xB203, 0xB503,
		0x86FF,
		0x8803,
		0xD007, 0xC203,
	}

	// orRegs only ever gain bits in their field: the pigs in Outset's pen
	// (d_a_kb.cpp:2170-2175).
	orRegs = []uint16{0xBF0F}
)
