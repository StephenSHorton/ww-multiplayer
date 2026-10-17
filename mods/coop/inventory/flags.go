package inventory

import (
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// itemSlots and equipSlots are save's slot table, which Apply and the
// planner walk.
var itemSlots, equipSlots = save.ItemSlots(), save.EquipSlots()

// slotAt returns the item slot rule of mItems index i.
func slotAt(i int) (save.Slot, bool) {
	for _, s := range itemSlots {
		if s.Index == i {
			return s, true
		}
	}
	return save.Slot{}, false
}

// flagByte is one OR byte of the inventory: the bits a receiver takes
// from the world.
type flagByte struct {
	off  int
	mask byte
	name string
}

// The flag bytes (§3 "Inventory and collection"), all class OR:
//
//   - mGetItem.mItemFlags[0..20], the get-item flags (d_save.h:131). Of
//     bytes 0-2 only bit 0 (the telescope, the sail, the Wind Waker) is
//     taken. Bits 1-7 of byte 0, 1-7 of byte 1 and 1-4 of byte 2 record
//     the bottle contents ever held (dSv_player_get_item_c::onBottleItem,
//     d_save.cpp:224-282), as the bottles themselves are each player's
//     own; they are read as such, by checkItemGet's first-get checks
//     (d_item.cpp:539-545, :2349-2392; Beedle's shop, d_a_npc_bs1.cpp:838)
//     and Grandma's soup talk (d_a_npc_ba1.cpp:678), so they stay each
//     player's too;
//   - mCollect[0..4]: swords, shields, bracelets and the two charms. Of
//     mCollect[4] only bit 0 (the Hero's Charm owned) is taken. Bit 1 is
//     whether it is worn: the collection menu turns it on and off
//     (d_menu_collect.cpp:1283, :1286), and the HUD (d_meter.cpp:3669,
//     :3682) and Link (d_a_player_main.cpp:5675) read it, so it is each
//     player's own;
//   - mTact (songs), mTriforce (shards), mSymbol (pearls) (d_save.h:291-296);
//   - mBeastFlags and mBaitFlags, the spoils and bait ever got
//     (dSv_player_get_bag_item_c::onBeast, onBait, d_save.cpp:676-697).
//
// The game never clears any of them in play but two: mCollect[0] bit 0,
// which the FF1 stage load and the barrel that carries Link there clear
// with the sword (d_s_play.cpp:1373, d_a_obj_demo_barrel.cpp:103; the
// FF1 context rule), and the worn bit above. dSv_player_get_item_c::
// offItem, offTact, offTriforce and offSymbol have no caller
// (d_save.cpp:210, :768, :785, :802).
var flagBytes = func() []flagByte {
	var fs []flagByte
	for i := range addr.GetItemSize {
		m := byte(0xFF)
		if i < bottleFlagBytes {
			m = 0x01
		}
		fs = append(fs, flagByte{addr.OffGetItem + i, m, fmt.Sprintf("mGetItem[%d]", i)})
	}
	for i := range 5 {
		m := byte(0xFF)
		if i == 4 {
			m = heroCharmOwned
		}
		fs = append(fs, flagByte{addr.OffCollectFlags + i, m, fmt.Sprintf("mCollect[%d]", i)})
	}
	fs = append(fs,
		flagByte{addr.OffTact, 0xFF, "mTact (songs)"},
		flagByte{addr.OffTriforce, 0xFF, "mTriforce (shards)"},
		flagByte{addr.OffSymbol, 0xFF, "mSymbol (pearls)"},
		flagByte{addr.OffBeastFlags, 0xFF, "mBeastFlags"},
		flagByte{addr.OffBaitFlags, 0xFF, "mBaitFlags"},
	)
	slices.SortFunc(fs, func(a, b flagByte) int { return a.off - b.off })
	return fs
}()

// bottleFlagBytes is how many get-item bytes hold bottle-content flags
// above bit 0 (onBottleItem, d_save.cpp:224-282).
const bottleFlagBytes = 3

// heroCharmOwned is the bit of mCollect[4] that syncs: the Hero's Charm
// owned (item_func_heros_omamori, d_item.cpp:895-898). Bit 1 is the charm
// worn (d_menu_collect.cpp:1776).
const heroCharmOwned = 0x01

// FlagMask returns the bits of image byte off that the inventory takes
// from the world: 0 for a byte that is no inventory flag byte.
func FlagMask(off int) byte {
	for _, f := range flagBytes {
		if f.off == off {
			return f.mask
		}
	}
	return 0
}

// capacity is one MAX byte the inventory raises.
type capacity struct {
	off  int
	most byte // the largest value the game ever sets
	name string
	cite string
}

// The capacities (§3 "Capacities"), each raised to the larger of the
// receiver's and the world's and never lowered. The game sets them only
// absolutely: a wallet upgrade to 1 or 2, the bow and the bomb bag to 30,
// a quiver or bomb bag upgrade to 60 then 99. A world value above the
// largest the game sets is taken as that largest.
var capacities = []capacity{
	{addr.OffWalletSize, 2, "wallet size", "item_func_max_rupee_up1/2, d_item.cpp:1358-1366; getRupeeMax, d_save.h:20-30"},
	{addr.OffArrowMax, 99, "arrow capacity", "item_func_bow, d_item.cpp:733-738; item_func_max_arrow_up1/2, :1380-1390"},
	{addr.OffBombMax, 99, "bomb capacity", "item_func_bomb_bag, d_item.cpp:792-797; item_func_max_bomb_up1/2, :1368-1378"},
}

// Current ammo, each player's own (class PER_PLAYER): read, never written.
// dSv_player_item_record_c is mTimer (u16) then picture, arrow and bomb
// counts (d_save.h:144-147, :164-165).
const (
	OffArrowNum = addr.OffItemRecord + 3
	OffBombNum  = addr.OffItemRecord + 4
)

// The HUD's pending ammo counters (s16; dComIfG_play_c, d_com_inf_game.h:
// 805, :807). Each frame dMeter_xyItemCountUp adds a nonzero count to the
// ammo, clamped to 0..max, and zeroes it: the arrows' while a bow is in
// its slot, the bombs' while the bomb bag is (d_meter.cpp:2873-2905;
// dComIfGs_checkGetItem counts the slots, d_com_inf_game.cpp:997-1001).
const (
	PlayOffItemArrowNumCount = 0x48E0
	PlayOffItemBombNumCount  = 0x48E4

	ItemArrowNumCount uint32 = 0x803CA788
	ItemBombNumCount  uint32 = 0x803CA78C
)

// firstGrant is an item whose grant sets a capacity and fills the ammo
// absolutely (§6.6 "Ordered grants"): the bow sets arrows and the arrow
// capacity to 30, the bomb bag bombs and the bomb capacity to 30.
type firstGrant struct {
	name    string
	flagOff int  // the slot's flag byte
	bits    byte // any of these set: the item is owned
	capOff  int
	fill    byte // the capacity and ammo the grant sets
	ammoOff int
	counter uint32
	cite    string
}

var firstGrants = []firstGrant{
	{"bow", addr.OffGetItem + save.SlotBow, 0x07, addr.OffArrowMax, 30, OffArrowNum, ItemArrowNumCount,
		"item_func_bow sets arrows and their capacity to 30 (d_item.cpp:733-738)"},
	{"bombs", addr.OffGetItem + save.SlotBomb, 0x01, addr.OffBombMax, 30, OffBombNum, ItemBombNumCount,
		"item_func_bomb_bag sets bombs and their capacity to 30 (d_item.cpp:792-797)"},
}

// Buttons is the number of item buttons, X, Y and Z (dItemBtn_COUNT_e,
// d_com_inf_game.h:1111-1115): the save's mSelectItem[5] binds each to an
// inventory slot (d_save.h:45), play.mSelectItem[4] holds the item the HUD
// shows on it (d_com_inf_game.h:830).
const Buttons = 3
