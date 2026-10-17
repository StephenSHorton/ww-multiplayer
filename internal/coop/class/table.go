package class

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// buildSpans writes out the class of every image byte, member by member
// in image order (d_save.h:586-605, 908-913). Classes follow §3; where the
// decomp narrowed a §3 row, the comment says so.
func buildSpans() []Span {
	var s []Span
	add := func(off, n int, c Class, mask byte, field string) {
		s = append(s, Span{Off: off, Len: n, Rule: Rule{Class: c, Mask: mask, Field: field}})
	}
	or := func(off, n int, field string) { add(off, n, OR, 0xFF, field) }
	maxOf := func(off int, mask byte, field string) { add(off, 1, Max, mask, field) }
	derived := func(off, n int, field string) { add(off, n, Derived, 0, field) }
	mine := func(off, n int, field string) { add(off, n, PerPlayer, 0, field) }
	never := func(off, n int, field string) { add(off, n, Never, 0, field) }

	// dSv_player_status_a_c (d_save.h:40-52).
	derived(addr.OffMaxLife, 2, "mMaxLife")        // 12 + pieces + 4 * containers, add-only (§3 "Max life")
	mine(addr.OffLife, 2, "mLife")                 // never written: 0 kills Link (§5.1)
	mine(addr.OffRupee, 2, "mRupee")               // rupees stay separate (§10 question 2)
	mine(addr.OffRupee+2, 3, "status_a.field_0x6") // and field_0x8: unknown, kept local
	mine(addr.OffSelectItem, 5, "mSelectItem")
	derived(addr.OffSelectEquip, 4, "mSelectEquip") // follows the merged mCollect (§6.6)
	maxOf(addr.OffWalletSize, 0xFF, "mWalletSize")
	derived(addr.OffMaxMagic, 1, "mMaxMagic") // from the Deku Leaf and event 0x3180, add-only
	mine(addr.OffMagic, 1, "mMagic")
	mine(addr.OffMagic+1, 2, "status_a.field_0x15")
	never(addr.OffStatusAPad, 1, "status_a padding")

	// dSv_player_status_b_c: time, date, wind and mDateIPL (tag rule V5).
	mine(addr.OffStatusB, addr.OffStatusBPad-addr.OffStatusB, "status_b")
	never(addr.OffStatusBPad, addr.OffReturnPlace-addr.OffStatusBPad, "status_b padding")

	mine(addr.OffReturnPlace, addr.ReturnPlaceSize, "mReturnPlace")

	// mItems slots are derived from the merged mGetItem flags (§6.6),
	// except the four bottles, which stay each player's (§6.6 "Bottles").
	derived(addr.OffItems, addr.SlotBottle0, "mItems")
	mine(addr.OffItems+addr.SlotBottle0, addr.SlotBottleCount, "mItems bottles")
	derived(addr.OffItems+addr.SlotDeliveryBag, addr.SlotCount-addr.SlotDeliveryBag, "mItems")
	// mGetItem[0..2] sync only bit 0: bits 1-7 record which bottle contents
	// this player has held (onBottleItem, d_save.cpp:224-282), and bottles
	// stay each player's.
	add(addr.OffGetItem, 3, OR, 0x01, "mGetItem[0..2]")
	or(addr.OffGetItem+3, addr.GetItemSize-3, "mGetItem")

	mine(addr.OffItemRecord, addr.ItemRecordSize, "mItemRecord") // ammo, picture and bottle counts
	mine(addr.OffItemMax, 1, "mItemMax.mReserved1Num")
	maxOf(addr.OffArrowMax, 0xFF, "arrow max")
	maxOf(addr.OffBombMax, 0xFF, "bomb max")
	mine(addr.OffBombMax+1, addr.OffItemMax+addr.ItemMaxSize-(addr.OffBombMax+1), "mItemMax.field_0x3")

	mine(addr.OffBagItem, addr.BagItemSize, "mBagItem")
	never(addr.OffBagItem+addr.BagItemSize, addr.OffGetBagItem-(addr.OffBagItem+addr.BagItemSize), "padding before mGetBagItem")
	mine(addr.OffReserveFlags, addr.OffBeastFlags-addr.OffReserveFlags, "mReserveFlags") // delivery-bag contents
	or(addr.OffBeastFlags, 1, "mBeastFlags")
	or(addr.OffBaitFlags, 1, "mBaitFlags")
	mine(addr.OffBaitFlags+1, addr.OffGetBagItem+addr.GetBagItemSize-(addr.OffBaitFlags+1), "mGetBagItem.unk_0x6")
	mine(addr.OffBagItemRecord, addr.BagItemRecordSize, "mBagItemRecord")

	// dSv_player_collect_c: mCollect[0..4] (swords, shields, bracelet,
	// charms), songs, Triforce shards and pearls merge. mCollect[5..7],
	// field_0x8 and field_0xc are outside §3's list and stay local.
	or(addr.OffCollectFlags, 4, "mCollect[0..3]")
	// mCollect[4] bit 0 is the Hero's Charm owned (item_func_heros_omamori,
	// d_item.cpp:895-898); bit 1 is whether it is worn, which the collect
	// menu turns on and off (d_menu_collect.cpp:1283-1286), so it stays local.
	add(addr.OffCollectFlags+4, 1, OR, 0x01, "mCollect[4]")
	mine(addr.OffCollectFlags+5, addr.OffTact-(addr.OffCollectFlags+5), "mCollect[5..7], field_0x8")
	or(addr.OffTact, 1, "mTact")
	or(addr.OffTriforce, 1, "mTriforce")
	or(addr.OffSymbol, 1, "mSymbol")
	mine(addr.OffSymbol+1, addr.OffCollect+addr.CollectSize-(addr.OffSymbol+1), "mCollect.field_0xc")
	never(addr.OffCollect+addr.CollectSize, addr.OffMap-(addr.OffCollect+addr.CollectSize), "padding before mMap")

	// dSv_player_map_c. §3 classes all 0x84 bytes OR; the decomp shows
	// row 0 of field_0x0 and field_0x71 are only ever zeroed (d_save.cpp:
	// 828-848), so they are NEVER rather than synced blind.
	never(addr.OffMapRow0, addr.OffMapGet-addr.OffMapRow0, "mMap row 0 (unused)")
	or(addr.OffMapGet, addr.OffFmapBits-addr.OffMapGet, "mMap charts get/open/complete")
	or(addr.OffFmapBits, addr.OffMap71-addr.OffFmapBits, "mFmapBits")
	never(addr.OffMap71, addr.OffMapTriforce-addr.OffMap71, "mMap.field_0x71 (unused)")
	or(addr.OffMapTriforce, 1, "mMap Triforce charts")
	never(addr.OffMapPad, addr.OffMap+addr.MapSize-addr.OffMapPad, "mMap padding")

	mine(addr.OffInfo, addr.InfoSize, "mInfo") // name, deaths, mClearCount (tag rules V3, V4)
	mine(addr.OffConfig, addr.ConfigSize, "mConfig")
	never(addr.OffConfigPad, addr.OffPriest-addr.OffConfigPad, "padding before mPriest")
	mine(addr.OffPriest, addr.PriestSize, "mPriest")
	mine(addr.OffStatusC, addr.StatusCCount*addr.StatusCSize, "mStatusC")
	never(addr.OffPlayerPad, addr.PlayerSize-addr.OffPlayerPad, "mPlayer padding")

	// mMemory[16] (d_save.h:684-689).
	for t := 0; t < addr.TableCount; t++ {
		m := addr.OffMemBit(t)
		name := func(f string) string { return fmt.Sprintf("mMemory[%d].%s", t, f) }
		or(m+addr.MemBitOffTbox, addr.MemBitOffSwitch-addr.MemBitOffTbox, name("mTbox"))
		// Latching setters only, per the M1 table (§6.2).
		add(m+addr.MemBitOffSwitch, addr.MemBitOffItem-addr.MemBitOffSwitch, ORMasked, 0xFF, name("mSwitch"))
		or(m+addr.MemBitOffItem, addr.MemBitOffVisited-addr.MemBitOffItem, name("mItem"))
		or(m+addr.MemBitOffVisited, addr.MemBitOffKeyNum-addr.MemBitOffVisited, name("mVisitedRoom"))
		derived(m+addr.MemBitOffKeyNum, 1, name("mKeyNum")) // sources set minus doors set (§6.4)
		add(m+addr.MemBitOffDungeonItem, 1, OR, addr.DungeonItemMask, name("mDungeonItem"))
		never(m+addr.MemBitOffPad, addr.MemBitSize-addr.MemBitOffPad, name("padding"))
	}

	or(addr.OffOcean, addr.OceanSize, "mOcean")

	// mEvent: named bits, unnamed bytes, registers (§3).
	for i := 0; i < addr.EventSize; i++ {
		off := addr.OffEvent + i
		name := fmt.Sprintf("mEvent[0x%02X]", i)
		switch {
		case i < addr.EventBitBytes:
			add(off, 1, ORMasked, namedEventBits[i], name)
		case i < addr.EventRegFirst:
			never(off, 1, name)
		default:
			c, mask := regRule(i)
			add(off, 1, c, mask, name)
		}
	}

	add(addr.OffReserve, addr.ReserveSize, Tag, 0, "mReserve")
	never(addr.OffImagePad, addr.SaveImageSize-addr.OffImagePad, "dSv_save_c padding")
	return s
}

// regRule is the class of event register byte i: OR or MAX for the
// allowlisted registers, per player for the rest.
func regRule(i int) (Class, byte) {
	for _, id := range figurineRegs {
		if b := addr.EventByte(id); b.Off == i {
			return OR, b.Mask
		}
	}
	for _, id := range maxRegs {
		if b := addr.EventByte(id); b.Off == i {
			return Max, b.Mask
		}
	}
	return PerPlayer, 0
}
