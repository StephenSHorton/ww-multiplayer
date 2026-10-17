package savefile

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// The DERIVED bytes are what the game computes from flags as it hands an
// item over: an item_func_* sets a get-item or collect flag and fills the
// slot or equips the item, or adds to a pending counter the HUD folds into
// max life and max magic (§3). The merge ORs the flags; Derive fills in
// what they imply. Citations are zeldaret/tww at 4d78c994.

// noteNames are the save-merge notes' names for the slots whose
// save.Slot name they predate.
var noteNames = map[string]string{"bracelets": "power bracelets"}

// Magic: the Deku Leaf adds 16 to max magic and the magic upgrade 32
// (item_func_deku_leaf, d_item.cpp:810-815; item_func_max_mp_up1,
// :1396-1398), through mItemMaxMagicCount, which the HUD adds to
// mMaxMagic with a cap of 0x20 (d_meter.cpp:4069-4076).
//
// Doc vs decomp: §3 derives the upgrade from event 0x3180. No decompiled
// code sets 0x3180 or ties it to the upgrade: its one reader is the list
// of seven event bits in daNpcPhoto_c::isPhotoDxOk (d_a_npc_photo.cpp:
// 2190-2197), and item 0xB2 is handed over by an event the decomp doesn't
// hold. So Derive doesn't read 0x3180; it takes b's own max magic as the
// record of b's upgrade (deriveMagic).
const (
	magicLeaf = 16
	magicCap  = 0x20
	slotLeaf  = save.SlotDekuLeaf
)

// lifeCap is the HUD's cap on max life, 20 hearts in quarters
// (dMeter_LifeMove, d_meter.cpp:1424-1437).
const lifeCap = 0x50

// Derive returns the Deriver save-merge uses with the M1 tables, k and h
// (either may be nil: its bytes are then a's). Over the merged flags it
// derives:
//
//   - inventory slots and equipment, the best variant the flags give, but
//     only into a slot that is empty or holds a lesser variant: nothing is
//     ever taken away, and a slot holding an item its rule doesn't name
//     is a's (§6.6);
//   - small keys per save table whose key invariant passes (§6.4), where
//     the merge took a key source or opened a key door: the live derive's
//     target over the merged flags (deriveKeys);
//   - max life, when the heart table passes: a's plus a quarter per heart
//     piece and four per heart container whose record the merge set (a
//     stage piece's save bit, a reward's event bit or read letter, a
//     container's STAGE_LIFE), capped at 20 hearts. It counts what the
//     merge adds to a rather than 12 + pieces + 4 * containers outright,
//     because that formula can't see the rewards no save bit records (the
//     minigames), which a's own max life holds;
//   - max magic: 16 once the Deku Leaf's flag is set, and at least the
//     larger of a's and b's, capped at 0x20.
func Derive(k *keys.Result, h *hearts.Result) Deriver {
	return func(img, a, b *world.Image) []string {
		var notes []string
		for _, r := range save.ItemSlots() {
			notes = append(notes, deriveSlot(r, img, "mItems")...)
		}
		for _, r := range save.EquipSlots() {
			notes = append(notes, deriveSlot(r, img, "mSelectEquip")...)
		}
		if k != nil {
			for i := range k.Tables {
				notes = append(notes, deriveKeys(img, a, &k.Tables[i])...)
			}
		}
		if h != nil {
			notes = append(notes, deriveLife(img, a, h)...)
		}
		notes = append(notes, deriveMagic(img, a, b)...)
		return notes
	}
}

// deriveSlot fills r's slot with the best variant img's flags give, where
// the slot is empty or holds a lesser one. The slot's own Derive does the
// same silently; this one notes what it did, and a kept item it can't
// name.
func deriveSlot(r save.Slot, img *world.Image, field string) []string {
	v, ok := r.Best(img[r.FlagOff])
	if !ok {
		return nil
	}
	best := r.Rank(v.Item)
	name := r.Name
	if n, ok := noteNames[name]; ok {
		name = n
	}
	cur := img[r.Off]
	rank := -1
	if cur != save.ItemNone {
		if rank = r.Rank(cur); rank < 0 {
			rank = len(r.Variants) // an item the rule doesn't name: keep it
		}
	}
	switch {
	case rank == len(r.Variants):
		return []string{fmt.Sprintf("%s (%s): kept item 0x%02X; the merged flags give 0x%02X, and nothing is taken away",
			field, name, cur, v.Item)}
	case best > rank:
		img[r.Off] = v.Item
		return []string{fmt.Sprintf("%s (%s): 0x%02X -> 0x%02X, from the merged flags", field, name, cur, v.Item)}
	}
	return nil
}

// flagSet reports whether f is set in img, and false for ok when f is not
// a saved image bit.
func flagSet(img *world.Image, f itemsrc.Flag) (set, ok bool) {
	var off int
	var b addr.Bit
	var err error
	switch f.Kind {
	case itemsrc.TboxFlag:
		b, err = addr.TboxBit(f.Bit)
		off = addr.OffMemBit(f.Table)
	case itemsrc.ItemFlag:
		b, err = addr.MemBitItemBit(f.Bit)
		off = addr.OffMemBit(f.Table)
	case itemsrc.StageLifeFlag:
		b, err = addr.DungeonItemBit(addr.DungeonItemStageLife)
		off = addr.OffMemBit(f.Table)
	case itemsrc.OceanFlag:
		b, err = addr.OceanBit(f.Room, f.Bit)
		off = addr.OffOcean
	case itemsrc.ChartFlag:
		// onCompleteCollectMap(no) sets bit no-1 of the u32[4] at
		// dSv_player_map_c+0x30 (d_com_inf_game.h:1212-1214;
		// dSv_player_map_c::onCompleteMap, d_save.cpp:882-885).
		n := f.Bit - 1
		if n < 0 || n >= 128 {
			return false, false
		}
		off, b = addr.OffMapComplete, addr.Bit{Off: n/32*4 + 3 - n%32/8, Mask: 1 << (n % 8)}
	default:
		return false, false
	}
	if err != nil || (f.Kind != itemsrc.OceanFlag && f.Kind != itemsrc.ChartFlag && !addr.ValidTable(f.Table)) {
		return false, false
	}
	return img[off+b.Off]&b.Mask != 0, true
}

// keyCount counts t's sources whose flag is set in img, and its doors
// whose memory switch is: keys(t) is the one less the other (§6.4).
func keyCount(img *world.Image, t *keys.Table) (sources, doors int) {
	for _, s := range t.Sources {
		if set, _ := flagSet(img, s.Flag); set {
			sources++
		}
	}
	for _, d := range t.Doors {
		store, b, err := addr.SwitchBit(d.Switch)
		if err == nil && store == addr.SwitchMemory && addr.ValidTable(t.SaveTable) &&
			img[addr.OffMemBit(t.SaveTable)+b.Off]&b.Mask != 0 {
			doors++
		}
	}
	return sources, doors
}

// deriveKeys sets t's mKeyNum in img to what the live derive would bring
// a's file to once the merged flags landed in it: derive.KeyTarget over
// them, with no keys from sources the table doesn't know. The live
// derive's baseline on a file loaded from the card takes none either
// (derive/keys.go): t's invariant passes, so the keys a holds beyond what
// its own flags give are a door's key not yet taken (a partner's door a
// co-op session synced before the key went) or a duplicate, and a co-op
// session would take them out too. A table whose sources and doors the
// merge left as they were keeps a's count.
func deriveKeys(img, a *world.Image, t *keys.Table) []string {
	if !addr.ValidTable(t.SaveTable) {
		return nil
	}
	off := addr.OffMemBit(t.SaveTable) + addr.MemBitOffKeyNum
	name := fmt.Sprintf("table %d", t.SaveTable)
	if t.Dungeon != "" {
		name += " (" + t.Dungeon + ")"
	}
	sources, doors := keyCount(img, t)
	sources0, doors0 := keyCount(a, t)
	if sources == sources0 && doors == doors0 {
		return nil // the merge took no key and opened no door here
	}
	if ok, why := derive.KeysEnabled(t); !ok {
		return []string{fmt.Sprintf("small keys, %s: kept %d: not derived: %s", name, a[off], why)}
	}
	want := derive.KeyTarget(derive.KeyFacts{Taken: sources, Open: doors}, 0)
	if int(img[off]) == want {
		return nil
	}
	note := fmt.Sprintf("small keys, %s: %d -> %d: %d of %d source(s) taken, %d of %d door(s) open",
		name, img[off], want, sources, len(t.Sources), doors, len(t.Doors))
	if over := int(a[off]) - derive.KeyTarget(derive.KeyFacts{Taken: sources0, Open: doors0}, 0); over > 0 {
		note += fmt.Sprintf("; a held %d key(s) beyond what its own flags give, a door's key not yet taken or a duplicate, which co-op takes out too", over)
	}
	img[off] = byte(want)
	return []string{note}
}

// lifeSources counts the heart pieces and containers h knows whose record
// is set in img: a stage piece by its own save bit, a reward by the record
// its Record names (rewardGiven), a container by its STAGE_LIFE.
func lifeSources(img *world.Image, h *hearts.Result) (pieces, containers int) {
	for _, s := range h.Pieces {
		if set, _ := flagSet(img, s.Flag); set {
			pieces++
		}
	}
	for _, r := range h.Rewards {
		if rewardGiven(img, tables.RecordOf(r)) {
			pieces++
		}
	}
	for _, s := range h.Containers {
		if set, _ := flagSet(img, s.Flag); set {
			containers++
		}
	}
	return pieces, containers
}

// rewardGiven reports whether a heart Catalogue reward's record is set in
// img, as the live derive reads it (save.View.RecordSet): its event bit,
// or its letter register reading READ. A reward no save bit records
// (tables.RecordNone: the minigames) never counts.
func rewardGiven(img *world.Image, rec tables.Record) bool {
	b := addr.EventByte(rec.ID)
	switch rec.Kind {
	case tables.RecordEventBit:
		return b.Mask != 0 && img[addr.OffEvent+b.Off]&b.Mask == b.Mask
	case tables.RecordLetterRead:
		return img[addr.OffEvent+b.Off]&b.Mask == tables.LetterRead
	}
	return false
}

// deriveLife raises max life in img by the heart sources the merge set.
func deriveLife(img, a *world.Image, h *hearts.Result) []string {
	p, c := lifeSources(img, h)
	p0, c0 := lifeSources(a, h)
	dp, dc := p-p0, c-c0
	if dp <= 0 && dc <= 0 {
		return nil
	}
	had := int(be16(a[addr.OffMaxLife:]))
	if !h.Pass {
		return []string{fmt.Sprintf("max life: kept %d quarter hearts although the merge set %d heart piece and %d container flag(s): the M1 heart table fails its check (coop-tables)", had, dp, dc)}
	}
	want := min(had+dp+4*dc, max(had, lifeCap))
	if want == had {
		return []string{fmt.Sprintf("max life: kept %d quarter hearts, the cap", had)}
	}
	img[addr.OffMaxLife], img[addr.OffMaxLife+1] = byte(want>>8), byte(want)
	return []string{fmt.Sprintf("max life: %d -> %d quarter hearts: %d heart piece(s) and %d container(s) the merge set", had, want, dp, dc)}
}

// deriveMagic sets max magic in img: the larger of a's and b's (each is
// what its own Deku Leaf and upgrade gave, and the two add up to at most
// the cap, so the union of the two files' sources gives exactly the
// larger), and 16 once the merged Deku Leaf flag is set.
func deriveMagic(img, a, b *world.Image) []string {
	had := a[addr.OffMaxMagic]
	want := max(had, min(b[addr.OffMaxMagic], magicCap))
	why := "b's max magic"
	if img[addr.OffGetItem+slotLeaf]&1 != 0 && want < magicLeaf {
		want, why = magicLeaf, "the merged Deku Leaf flag"
	}
	if want == img[addr.OffMaxMagic] {
		return nil
	}
	img[addr.OffMaxMagic] = want
	return []string{fmt.Sprintf("max magic: %d -> %d, from %s", had, want, why)}
}
