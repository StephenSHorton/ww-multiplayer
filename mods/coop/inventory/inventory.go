// Package inventory plans how a receiver takes the world's inventory
// (§6.6, §9 M4): the get-item and collect flags, songs, shards, pearls and
// the spoils and bait flags by OR; the item slots and the equipment
// derived from those flags as the game derives them; the capacities by
// MAX, after the grants that set them; and the HUD's button items
// refreshed after a slot changes.
//
// Pending is a pure function of the world's image, the receiver's own
// image and a little live context (the stage, the HUD's button items and
// its pending ammo counters). It returns byte writes, each with the reason
// it is made; the replica routes them through the WriteGuard and the
// gates (§4.2, §3.2). It never writes a PER_PLAYER, NEVER or TAG byte,
// never clears a bit and never lowers a capacity, and a slot or an
// equipment byte only ever moves up to a better variant of what it holds.
//
// The game's own item_funcs set a slot, an equipment byte or a capacity
// absolutely as they set the flag (d_item.cpp:691-1390). Go does not run
// them: it ORs the flags and derives the rest, so its order is the
// grants' order, the base item before its upgrades:
//
//  1. the flags (live when IDLE);
//  2. the capacities, each raised once to the largest of the receiver's,
//     the world's and the 30 a first bow or bomb bag grant sets, so a
//     base grant never lowers what an upgrade gave (§6.6 "Ordered
//     grants");
//  3. the item slots, from the merged get-item flags (live when IDLE);
//  4. the equipment, from the merged collect flags, at the receiver's
//     next stage entry (see AtStageEntry);
//  5. the HUD's button items, for the buttons bound to a derived slot
//     whose item they show as nothing or as a lesser variant.
//
// The plan takes fewer bits than the class table merges in two bytes:
// mGetItem[0..2] above bit 0 (the bottle contents ever held, which go
// with the bottles, each player's own) and mCollect[4] bit 1 (the Hero's
// Charm worn). See flagBytes.
//
// Current ammo is each player's own (class PER_PLAYER). Only a first
// grant fills it, as the game's does, and only through the HUD's pending
// ammo counters (Plan.Ammo), never by writing the ammo itself.
//
// Two context rules hold writes, never drop them (§6.6): no sword while
// the receiver is in a Forsaken Fortress first-visit stage with event
// 0x0801 set, and no bow while it is in GTower. A third holds a first bow
// or bomb bag grant for the tick or so its ammo counter is busy. A held
// write is planned again once the rule no longer holds.
package inventory

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Kind is what a write changes.
type Kind uint8

const (
	// KindFlag ORs world bits into a flag byte (class OR: the
	// WriteGuard's TargetInventory).
	KindFlag Kind = iota + 1
	// KindCapacity raises a capacity (class MAX: TargetInventory).
	KindCapacity
	// KindSlot moves an item slot mItems[i] to a better variant (class
	// DERIVED: TargetDerived).
	KindSlot
	// KindEquip moves an equipment byte mSelectEquip[i] to a better
	// variant (class DERIVED: TargetDerived).
	KindEquip
	// KindSelectItem refreshes play.mSelectItem[btn], the item the HUD
	// shows on a button (TargetSelectItem).
	KindSelectItem
	// KindAmmo starts a first grant's ammo fill through the HUD's pending
	// arrow or bomb counter. The M4 WriteGuard's TargetCounters does not
	// list those counters, and gate.Counter has none for them: these
	// writes are kept apart in Plan.Ammo until the replica's guard and
	// gate cover them.
	KindAmmo
)

var kindNames = [...]string{"", "flag", "capacity", "slot", "equip", "select-item", "ammo"}

func (k Kind) String() string {
	if int(k) < len(kindNames) && k != 0 {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Op is how the writer lands a write over a fresh read of the byte.
type Op uint8

const (
	// OpOR ORs the bits Val adds to Old (Val &^ Old) into the byte.
	OpOR Op = iota + 1
	// OpMax raises the byte to Val if it is lower.
	OpMax
	// OpSet sets the byte to Val only if it still reads Old: a
	// compare-and-set, so a byte the game moved since the plan is left
	// alone and planned again next tick.
	OpSet
)

var opNames = [...]string{"", "or", "max", "set"}

func (o Op) String() string {
	if int(o) < len(opNames) && o != 0 {
		return opNames[o]
	}
	return fmt.Sprintf("Op(%d)", uint8(o))
}

// Timing is when a write may be made.
type Timing uint8

const (
	// Live writes need IDLE (§3.2): STABLE, HEARTBEAT and no event,
	// menu, minigame or refight snapshot.
	Live Timing = iota
	// AtStageEntry writes wait, IDLE too, for the receiver's stage-entry
	// window (Entry): the first IDLE tick after a stage load, before the
	// player has drawn the sword.
	//
	// The equipment waits for it. A live mSelectEquip change is a state
	// the game never makes while the sword is in Link's hand: the drawn
	// blade's model is built for the equipped sword when Link draws it
	// (setSwordModel, d_a_player_main.cpp:3847-3850;
	// d_a_player_sword.inc:23), and every sword change the game makes
	// itself rebuilds it in the same frame (d_a_player_dproc.inc:274-281).
	// At a stage load Link is created with nothing in hand (mEquipItem =
	// daPyItem_NONE_e, d_a_player_main.cpp:12179) and picks his sheathed
	// sword and shield models each frame from mSelectEquip
	// (d_a_player_main.cpp:1222-1300). Whether a live change is safe with
	// the sword sheathed is inferred, so the equipment is left to the
	// next stage load, as §6.6 allows.
	AtStageEntry
)

func (t Timing) String() string {
	switch t {
	case Live:
		return "live"
	case AtStageEntry:
		return "at stage entry"
	}
	return fmt.Sprintf("Timing(%d)", uint8(t))
}

// Write is one planned byte write.
type Write struct {
	Kind Kind
	// Addr is the byte's address; Off its image offset (dSv_save_c,
	// addr.Off*), or -1 for a play byte (KindSelectItem, KindAmmo).
	Addr uint32
	Off  int
	Op   Op
	// Old is the byte as the plan read it, Val what it becomes.
	Old, Val byte
	When     Timing
	Reason   string
}

// Bits returns the bits an OpOR write adds, or Val for the other ops.
func (w Write) Bits() byte {
	if w.Op == OpOR {
		return w.Val &^ w.Old
	}
	return w.Val
}

func (w Write) String() string {
	return fmt.Sprintf("%s 0x%08X %s 0x%02X -> 0x%02X (%s): %s", w.Kind, w.Addr, w.Op, w.Old, w.Val, w.When, w.Reason)
}

// Held is a write a context rule holds back.
type Held struct {
	Rule  Rule
	Write Write
}

// Plan is what a receiver takes from the world this tick.
type Plan struct {
	// Writes are the writes to make, in the order they must land: the
	// flags, the capacities, the slots, the equipment, the button items.
	Writes []Write
	// Ammo are the first grants' ammo fills (KindAmmo), to land after
	// Writes: the HUD adds the ammo only while the bow or the bomb bag
	// is in its slot.
	Ammo []Write
	// Held are the writes a context rule holds back. They are not in
	// Writes; they come back once the rule no longer holds.
	Held []Held
}

// Empty reports whether the plan writes nothing and holds nothing.
func (p Plan) Empty() bool { return len(p.Writes) == 0 && len(p.Ammo) == 0 && len(p.Held) == 0 }

// Pending returns what the receiver whose canonical image is raw takes
// from view, the world's image (the receiver's view of it), in ctx.
// Bytes of view outside the inventory are ignored, and so are view's slot,
// equipment and ammo bytes: the receiver derives those from the flags.
func Pending(view, raw *world.Image, ctx Context) Plan {
	pl := planner{view: view, raw: raw, ctx: ctx, m: *raw}
	pl.rules()
	pl.flags()
	pl.capacities()
	pl.slots()
	pl.equipment()
	pl.buttons()
	pl.ammo()
	return pl.p
}

type planner struct {
	view, raw *world.Image
	ctx       Context
	// m is raw with every write planned so far applied: what the
	// receiver holds once they land.
	m world.Image
	p Plan

	ff1, gtower bool
	// busy is the first grants this plan holds because their pending
	// ammo counter is not 0.
	busy map[int]bool
	// first is the first grants this plan makes, by their flag byte's
	// offset.
	first map[int]bool
}

// rules decides which context rules hold this tick.
func (pl *planner) rules() {
	pl.ff1 = pl.ctx.ff1(pl.raw)
	pl.gtower = pl.ctx.gtower()
	pl.busy = map[int]bool{}
	pl.first = map[int]bool{}
	for _, g := range firstGrants {
		if pl.holdFlag(g.flagOff) != 0 {
			continue
		}
		merged := pl.raw[g.flagOff] | pl.view[g.flagOff]&FlagMask(g.flagOff)
		if pl.raw[g.flagOff]&g.bits != 0 || merged&g.bits == 0 {
			continue
		}
		if pl.ctx.counter(g.counter) != 0 {
			pl.busy[g.flagOff] = true
		}
	}
}

// emit adds w to the plan, or to Held when rule holds it, and applies it
// to m when it is made.
func (pl *planner) emit(w Write, rule Rule) {
	if rule != 0 {
		pl.p.Held = append(pl.p.Held, Held{Rule: rule, Write: w})
		return
	}
	if w.Kind == KindAmmo {
		pl.p.Ammo = append(pl.p.Ammo, w)
		return
	}
	pl.p.Writes = append(pl.p.Writes, w)
	if w.Off >= 0 {
		pl.m[w.Off] = w.Val
	}
}

// holdFlag returns the rule holding flag byte off, or 0.
func (pl *planner) holdFlag(off int) Rule {
	switch {
	case pl.ff1 && off == equipSlots[save.EquipSword].FlagOff:
		return RuleFF1Sword
	case pl.gtower && off == addr.OffGetItem+save.SlotBow:
		return RuleGTowerBow
	case pl.busy[off]:
		return RuleAmmoBusy
	}
	return 0
}

func imageWrite(k Kind, off int, op Op, old, val byte, when Timing, reason string) Write {
	return Write{Kind: k, Addr: addr.SaveImage + uint32(off), Off: off, Op: op, Old: old, Val: val, When: when, Reason: reason}
}

// flags ORs the world's flag bits in, byte by byte in image order.
func (pl *planner) flags() {
	for _, f := range flagBytes {
		old := pl.raw[f.off]
		g := pl.view[f.off] & f.mask &^ old
		if g == 0 {
			continue
		}
		pl.emit(imageWrite(KindFlag, f.off, OpOR, old, old|g, Live,
			fmt.Sprintf("%s gains 0x%02X from the world", f.name, g)), pl.holdFlag(f.off))
		if pl.raw[f.off]&firstBits(f.off) == 0 && pl.m[f.off]&firstBits(f.off) != 0 {
			pl.first[f.off] = true
		}
	}
}

// firstBits returns the bits of flag byte off that own a first grant's
// item, or 0.
func firstBits(off int) byte {
	for _, g := range firstGrants {
		if g.flagOff == off {
			return g.bits
		}
	}
	return 0
}

// capacities raises each capacity to the largest of the receiver's, the
// world's (at most the largest the game sets) and a first grant's.
func (pl *planner) capacities() {
	for _, c := range capacities {
		old := pl.raw[c.off]
		target, why := old, ""
		if w := min(pl.view[c.off], c.most); w > target {
			target, why = w, "the world's"
		}
		for _, g := range firstGrants {
			if g.capOff == c.off && pl.first[g.flagOff] && g.fill > target {
				target, why = g.fill, "the first "+g.name+" grant's: "+g.cite
			}
		}
		if target > old {
			pl.emit(imageWrite(KindCapacity, c.off, OpMax, old, target, Live,
				fmt.Sprintf("%s %d -> %d, %s (%s)", c.name, old, target, why, c.cite)), 0)
		}
	}
}

// slots derives every item slot from the merged get-item flags.
func (pl *planner) slots() {
	for _, s := range itemSlots {
		old := pl.raw[s.Off]
		v := s.Derive(old, pl.m[s.FlagOff])
		if v == old {
			continue
		}
		var rule Rule
		if pl.gtower && s.Index == save.SlotBow {
			rule = RuleGTowerBow
		}
		pl.emit(imageWrite(KindSlot, s.Off, OpSet, old, v, Live,
			fmt.Sprintf("%s slot: %s -> %s, from %s (d_s_play.cpp:1385-1392; %s)",
				s.Name, s.ItemName(old), s.ItemName(v), flagName(s.FlagOff), s.Cite)), rule)
	}
}

// equipment derives the sword, shield and bracelet bytes from the merged
// collect flags.
func (pl *planner) equipment() {
	for _, e := range equipSlots {
		old := pl.raw[e.Off]
		v := e.Derive(old, pl.m[e.FlagOff])
		if v == old {
			continue
		}
		var rule Rule
		if pl.ff1 && e.Index == save.EquipSword {
			rule = RuleFF1Sword
		}
		pl.emit(imageWrite(KindEquip, e.Off, OpSet, old, v, AtStageEntry,
			fmt.Sprintf("%s: %s -> %s, from %s (dComIfGs_setSelectEquip, d_com_inf_game.cpp:1786-1822; %s)",
				e.Name, e.ItemName(old), e.ItemName(v), flagName(e.FlagOff), e.Cite)), rule)
	}
}

// buttons refreshes play.mSelectItem for every button bound to a derived
// slot whose item the HUD shows as nothing or as a lesser variant, as
// dComIfGp_setSelectItem does (d_com_inf_game.h:2924-2938): the HUD
// changes a button's icon when that byte changes (dMeter_xyMove,
// d_meter.cpp:2670-2686), and Link uses the item it names
// (daPy_lk_c::getReadyItem, d_a_player_main.cpp:196-205). The game does the
// same for the bow's buttons as it hands over the arrows (d_item.cpp:
// 822-828, :837-843). The save's own binding, mSelectItem, is each
// player's and is not written. A button bound to a bottle, a bag or
// nothing is left alone, and so is one showing an item the slot's
// variants don't name: only the game puts it there.
func (pl *planner) buttons() {
	for btn := range Buttons {
		slot := int(pl.raw[addr.OffSelectItem+btn])
		s, ok := slotAt(slot)
		if !ok {
			continue
		}
		v, shown := pl.m[s.Off], pl.ctx.SelectItem[btn]
		if rv, rs := s.Rank(v), s.Rank(shown); rv < 0 || shown != save.ItemNone && (rs < 0 || rs >= rv) {
			continue
		}
		var rule Rule
		if pl.gtower && slot == save.SlotBow {
			rule = RuleGTowerBow
		}
		pl.emit(Write{Kind: KindSelectItem, Addr: addr.PlaySelectItem + uint32(btn), Off: -1, Op: OpSet,
			Old: shown, Val: v, When: Live,
			Reason: fmt.Sprintf("button %s shows %s, bound to the %s slot (dComIfGp_setSelectItem, d_com_inf_game.h:2924-2938)",
				buttonNames[btn], s.ItemName(v), s.Name)}, rule)
	}
}

var buttonNames = [Buttons]string{"X", "Y", "Z"}

// ammo starts each first grant's fill: the game's grant sets the ammo to
// 30 (d_item.cpp:733-738, :792-797); Go adds what the receiver lacks of
// 30 through the HUD's pending counter, which the HUD adds to the ammo,
// clamped to the capacity (dMeter_xyItemCountUp, d_meter.cpp:2873-2905).
// Only a first grant fills the ammo: a capacity upgrade from the world
// does not, though the game's upgrade item fills it to the new capacity
// (d_item.cpp:1368-1390), as the ammo is each player's own.
func (pl *planner) ammo() {
	for _, g := range firstGrants {
		if !pl.first[g.flagOff] {
			continue
		}
		have := pl.raw[g.ammoOff]
		if have >= g.fill {
			continue
		}
		// The counter is an s16 that read 0: its low byte holds the
		// count, and a compare-and-set on it fails if the game moved
		// the counter since.
		pl.emit(Write{Kind: KindAmmo, Addr: g.counter + 1, Off: -1, Op: OpSet, Old: 0, Val: g.fill - have, When: Live,
			Reason: fmt.Sprintf("%s: %d -> %d ammo through the HUD's pending count", g.cite, have, g.fill)}, 0)
	}
}

// flagName names flag byte off.
func flagName(off int) string {
	for _, f := range flagBytes {
		if f.off == off {
			return f.name
		}
	}
	return fmt.Sprintf("image byte 0x%03X", off)
}
