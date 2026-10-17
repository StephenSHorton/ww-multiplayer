package save

import (
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Span is one run of image bytes: Len bytes at image offset Off, named
// for the decomp member they belong to (for diagnostics).
type Span struct {
	Off, Len int
	Field    string
}

var layout = buildLayout()

// Layout returns every byte of the image as spans, in image order, each
// byte in exactly one span. A member is split where its parts differ (the
// bottles among mItems, mGetItem[0..2] beside the rest, one span per
// mEvent byte), so a co-op class table can key a rule by a span's Off.
func Layout() []Span { return slices.Clone(layout) }

// buildLayout writes out the image member by member, in image order
// (d_save.h:586-605, 908-913).
func buildLayout() []Span {
	var s []Span
	add := func(off, n int, field string) { s = append(s, Span{Off: off, Len: n, Field: field}) }

	// dSv_player_status_a_c (d_save.h:40-52).
	add(addr.OffMaxLife, 2, "mMaxLife")
	add(addr.OffLife, 2, "mLife")
	add(addr.OffRupee, 2, "mRupee")
	add(addr.OffRupee+2, 3, "status_a.field_0x6") // and field_0x8
	add(addr.OffSelectItem, 5, "mSelectItem")
	add(addr.OffSelectEquip, 4, "mSelectEquip")
	add(addr.OffWalletSize, 1, "mWalletSize")
	add(addr.OffMaxMagic, 1, "mMaxMagic")
	add(addr.OffMagic, 1, "mMagic")
	add(addr.OffMagic+1, 2, "status_a.field_0x15")
	add(addr.OffStatusAPad, 1, "status_a padding")

	// dSv_player_status_b_c: time, date, wind and mDateIPL.
	add(addr.OffStatusB, addr.OffStatusBPad-addr.OffStatusB, "status_b")
	add(addr.OffStatusBPad, addr.OffReturnPlace-addr.OffStatusBPad, "status_b padding")

	add(addr.OffReturnPlace, addr.ReturnPlaceSize, "mReturnPlace")

	// mItems, with the four bottles apart.
	add(addr.OffItems, addr.SlotBottle0, "mItems")
	add(addr.OffItems+addr.SlotBottle0, addr.SlotBottleCount, "mItems bottles")
	add(addr.OffItems+addr.SlotDeliveryBag, addr.SlotCount-addr.SlotDeliveryBag, "mItems")
	// mGetItem[0..2]: bit 0 is the item, bits 1-7 the bottle contents
	// ever held (onBottleItem, d_save.cpp:224-282).
	add(addr.OffGetItem, 3, "mGetItem[0..2]")
	add(addr.OffGetItem+3, addr.GetItemSize-3, "mGetItem")

	add(addr.OffItemRecord, addr.ItemRecordSize, "mItemRecord") // ammo, picture and bottle counts
	add(addr.OffItemMax, 1, "mItemMax.mReserved1Num")
	add(addr.OffArrowMax, 1, "arrow max")
	add(addr.OffBombMax, 1, "bomb max")
	add(addr.OffBombMax+1, addr.OffItemMax+addr.ItemMaxSize-(addr.OffBombMax+1), "mItemMax.field_0x3")

	add(addr.OffBagItem, addr.BagItemSize, "mBagItem")
	add(addr.OffBagItem+addr.BagItemSize, addr.OffGetBagItem-(addr.OffBagItem+addr.BagItemSize), "padding before mGetBagItem")
	add(addr.OffReserveFlags, addr.OffBeastFlags-addr.OffReserveFlags, "mReserveFlags") // delivery-bag contents
	add(addr.OffBeastFlags, 1, "mBeastFlags")
	add(addr.OffBaitFlags, 1, "mBaitFlags")
	add(addr.OffBaitFlags+1, addr.OffGetBagItem+addr.GetBagItemSize-(addr.OffBaitFlags+1), "mGetBagItem.unk_0x6")
	add(addr.OffBagItemRecord, addr.BagItemRecordSize, "mBagItemRecord")

	// dSv_player_collect_c: mCollect[0..4] (swords, shields, bracelet,
	// charms; mCollect[4] bit 0 is the Hero's Charm owned, bit 1 worn),
	// songs, Triforce shards and pearls.
	add(addr.OffCollectFlags, 4, "mCollect[0..3]")
	add(addr.OffCollectFlags+4, 1, "mCollect[4]")
	add(addr.OffCollectFlags+5, addr.OffTact-(addr.OffCollectFlags+5), "mCollect[5..7], field_0x8")
	add(addr.OffTact, 1, "mTact")
	add(addr.OffTriforce, 1, "mTriforce")
	add(addr.OffSymbol, 1, "mSymbol")
	add(addr.OffSymbol+1, addr.OffCollect+addr.CollectSize-(addr.OffSymbol+1), "mCollect.field_0xc")
	add(addr.OffCollect+addr.CollectSize, addr.OffMap-(addr.OffCollect+addr.CollectSize), "padding before mMap")

	// dSv_player_map_c. Row 0 of field_0x0 and field_0x71 are only ever
	// zeroed (d_save.cpp:828-848).
	add(addr.OffMapRow0, addr.OffMapGet-addr.OffMapRow0, "mMap row 0 (unused)")
	add(addr.OffMapGet, addr.OffFmapBits-addr.OffMapGet, "mMap charts get/open/complete")
	add(addr.OffFmapBits, addr.OffMap71-addr.OffFmapBits, "mFmapBits")
	add(addr.OffMap71, addr.OffMapTriforce-addr.OffMap71, "mMap.field_0x71 (unused)")
	add(addr.OffMapTriforce, 1, "mMap Triforce charts")
	add(addr.OffMapPad, addr.OffMap+addr.MapSize-addr.OffMapPad, "mMap padding")

	add(addr.OffInfo, addr.InfoSize, "mInfo") // name, deaths, mClearCount
	add(addr.OffConfig, addr.ConfigSize, "mConfig")
	add(addr.OffConfigPad, addr.OffPriest-addr.OffConfigPad, "padding before mPriest")
	add(addr.OffPriest, addr.PriestSize, "mPriest")
	add(addr.OffStatusC, addr.StatusCCount*addr.StatusCSize, "mStatusC")
	add(addr.OffPlayerPad, addr.PlayerSize-addr.OffPlayerPad, "mPlayer padding")

	// mMemory[16] (d_save.h:684-689).
	for t := 0; t < addr.TableCount; t++ {
		m := addr.OffMemBit(t)
		name := func(f string) string { return fmt.Sprintf("mMemory[%d].%s", t, f) }
		add(m+addr.MemBitOffTbox, addr.MemBitOffSwitch-addr.MemBitOffTbox, name("mTbox"))
		add(m+addr.MemBitOffSwitch, addr.MemBitOffItem-addr.MemBitOffSwitch, name("mSwitch"))
		add(m+addr.MemBitOffItem, addr.MemBitOffVisited-addr.MemBitOffItem, name("mItem"))
		add(m+addr.MemBitOffVisited, addr.MemBitOffKeyNum-addr.MemBitOffVisited, name("mVisitedRoom"))
		add(m+addr.MemBitOffKeyNum, 1, name("mKeyNum"))
		add(m+addr.MemBitOffDungeonItem, 1, name("mDungeonItem"))
		add(m+addr.MemBitOffPad, addr.MemBitSize-addr.MemBitOffPad, name("padding"))
	}

	add(addr.OffOcean, addr.OceanSize, "mOcean")

	// mEvent, one span per byte: named bits, unnamed bytes, registers.
	for i := 0; i < addr.EventSize; i++ {
		add(addr.OffEvent+i, 1, fmt.Sprintf("mEvent[0x%02X]", i))
	}

	add(addr.OffReserve, addr.ReserveSize, "mReserve")
	add(addr.OffImagePad, addr.SaveImageSize-addr.OffImagePad, "dSv_save_c padding")
	return s
}
