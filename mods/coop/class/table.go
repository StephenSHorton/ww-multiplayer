package class

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// keyed is the rule of the save.Layout span that starts at image offset
// Off. Its Field is the span's.
type keyed struct {
	Off int
	Rule
}

// buildRules writes out the class of every save.Layout span, keyed by
// the span's offset, member by member in image order (d_save.h:586-605,
// 908-913). Classes follow §3; where the decomp narrowed a §3 row, the
// comment says so.
func buildRules() []keyed {
	var s []keyed
	add := func(off int, c Class, mask byte) { s = append(s, keyed{Off: off, Rule: Rule{Class: c, Mask: mask}}) }
	or := func(off int) { add(off, OR, 0xFF) }
	maxOf := func(off int, mask byte) { add(off, Max, mask) }
	derived := func(off int) { add(off, Derived, 0) }
	mine := func(off int) { add(off, PerPlayer, 0) }
	never := func(off int) { add(off, Never, 0) }

	// dSv_player_status_a_c (d_save.h:40-52).
	derived(addr.OffMaxLife) // mMaxLife: 12 + pieces + 4 * containers, add-only (§3 "Max life")
	mine(addr.OffLife)       // mLife: never written: 0 kills Link (§5.1)
	mine(addr.OffRupee)      // mRupee: rupees stay separate (§10 question 2)
	mine(addr.OffRupee + 2)  // status_a.field_0x6 and field_0x8: unknown, kept local
	mine(addr.OffSelectItem)
	derived(addr.OffSelectEquip) // follows the merged mCollect (§6.6)
	maxOf(addr.OffWalletSize, 0xFF)
	derived(addr.OffMaxMagic) // from the Deku Leaf and event 0x3180, add-only
	mine(addr.OffMagic)
	mine(addr.OffMagic + 1)
	never(addr.OffStatusAPad)

	// dSv_player_status_b_c: time, date, wind and mDateIPL (tag rule V5).
	mine(addr.OffStatusB)
	never(addr.OffStatusBPad)

	mine(addr.OffReturnPlace)

	// mItems slots are derived from the merged mGetItem flags (§6.6),
	// except the four bottles, which stay each player's (§6.6 "Bottles").
	derived(addr.OffItems)
	mine(addr.OffItems + addr.SlotBottle0)
	derived(addr.OffItems + addr.SlotDeliveryBag)
	// mGetItem[0..2] sync only bit 0: bits 1-7 record which bottle contents
	// this player has held (onBottleItem, d_save.cpp:224-282), and bottles
	// stay each player's.
	add(addr.OffGetItem, OR, 0x01)
	or(addr.OffGetItem + 3)

	mine(addr.OffItemRecord) // ammo, picture and bottle counts
	mine(addr.OffItemMax)
	maxOf(addr.OffArrowMax, 0xFF)
	maxOf(addr.OffBombMax, 0xFF)
	mine(addr.OffBombMax + 1)

	mine(addr.OffBagItem)
	never(addr.OffBagItem + addr.BagItemSize)
	mine(addr.OffReserveFlags) // delivery-bag contents
	or(addr.OffBeastFlags)
	or(addr.OffBaitFlags)
	mine(addr.OffBaitFlags + 1)
	mine(addr.OffBagItemRecord)

	// dSv_player_collect_c: mCollect[0..4] (swords, shields, bracelet,
	// charms), songs, Triforce shards and pearls merge. mCollect[5..7],
	// field_0x8 and field_0xc are outside §3's list and stay local.
	or(addr.OffCollectFlags)
	// mCollect[4] bit 0 is the Hero's Charm owned (item_func_heros_omamori,
	// d_item.cpp:895-898); bit 1 is whether it is worn, which the collect
	// menu turns on and off (d_menu_collect.cpp:1283-1286), so it stays local.
	add(addr.OffCollectFlags+4, OR, 0x01)
	mine(addr.OffCollectFlags + 5)
	or(addr.OffTact)
	or(addr.OffTriforce)
	or(addr.OffSymbol)
	mine(addr.OffSymbol + 1)
	never(addr.OffCollect + addr.CollectSize)

	// dSv_player_map_c. §3 classes all 0x84 bytes OR; the decomp shows
	// row 0 of field_0x0 and field_0x71 are only ever zeroed (d_save.cpp:
	// 828-848), so they are NEVER rather than synced blind.
	never(addr.OffMapRow0)
	or(addr.OffMapGet)
	or(addr.OffFmapBits)
	never(addr.OffMap71)
	or(addr.OffMapTriforce)
	never(addr.OffMapPad)

	mine(addr.OffInfo) // name, deaths, mClearCount (tag rules V3, V4)
	mine(addr.OffConfig)
	never(addr.OffConfigPad)
	mine(addr.OffPriest)
	mine(addr.OffStatusC)
	never(addr.OffPlayerPad)

	// mMemory[16] (d_save.h:684-689).
	for t := 0; t < addr.TableCount; t++ {
		m := addr.OffMemBit(t)
		or(m + addr.MemBitOffTbox)
		// Latching setters only, per the M1 table (§6.2).
		add(m+addr.MemBitOffSwitch, ORMasked, 0xFF)
		or(m + addr.MemBitOffItem)
		or(m + addr.MemBitOffVisited)
		derived(m + addr.MemBitOffKeyNum) // sources set minus doors set (§6.4)
		add(m+addr.MemBitOffDungeonItem, OR, addr.DungeonItemMask)
		never(m + addr.MemBitOffPad)
	}

	or(addr.OffOcean)

	// mEvent: named bits, unnamed bytes, registers (§3).
	for i := 0; i < addr.EventSize; i++ {
		off := addr.OffEvent + i
		switch {
		case i < addr.EventBitBytes:
			add(off, ORMasked, namedEventBits[i])
		case i < addr.EventRegFirst:
			never(off)
		default:
			c, mask := regRule(i)
			add(off, c, mask)
		}
	}

	add(addr.OffReserve, Tag, 0)
	never(addr.OffImagePad)
	return s
}

// buildSpans gives every save.Layout span its rule from rules, and the
// span's Field. A span no rule is keyed to, or more than one is, gets the
// zero Rule (Unassigned), which Validate reports.
func buildSpans(rules []keyed) []Span {
	n := map[int]int{}
	byOff := map[int]Rule{}
	for _, k := range rules {
		n[k.Off]++
		byOff[k.Off] = k.Rule
	}
	var s []Span
	for _, l := range save.Layout() {
		var r Rule
		if n[l.Off] == 1 {
			r = byOff[l.Off]
		}
		r.Field = l.Field
		s = append(s, Span{Off: l.Off, Len: l.Len, Rule: r})
	}
	return s
}

// validateRules reports a rule keyed to an offset no save.Layout span
// starts at, and an offset more than one rule is keyed to.
func validateRules(rules []keyed) error {
	starts := map[int]bool{}
	for _, l := range save.Layout() {
		starts[l.Off] = true
	}
	var errs []error
	seen := map[int]bool{}
	for _, k := range rules {
		switch {
		case !starts[k.Off]:
			errs = append(errs, fmt.Errorf("rule at 0x%03X: no save.Layout span starts there", k.Off))
		case seen[k.Off]:
			errs = append(errs, fmt.Errorf("rule at 0x%03X: keyed twice", k.Off))
		}
		seen[k.Off] = true
	}
	return errors.Join(errs...)
}

// regRule is the class of event register byte i: OR or MAX for the
// allowlisted registers, per player for the rest.
func regRule(i int) (Class, byte) {
	for _, list := range [][]uint16{figurineRegs, warpPotRegs, orRegs} {
		for _, id := range list {
			if b := addr.EventByte(id); b.Off == i {
				return OR, b.Mask
			}
		}
	}
	for _, id := range maxRegs {
		if b := addr.EventByte(id); b.Off == i {
			return Max, b.Mask
		}
	}
	return PerPlayer, 0
}
