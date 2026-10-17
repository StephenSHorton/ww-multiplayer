package save

import (
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Item numbers (dItemNo_e, d_item_data.h:6-262).
const (
	ItemNone = 0xFF // dItemNo_NONE_e (d_item_data.h:262); dInvSlot_NONE_e is 0xFF too (d_com_inf_game.h:1106)

	ItemTelescope      = 0x20 // d_item_data.h:39
	ItemTingleTuner    = 0x21 // :40
	ItemWindWaker      = 0x22 // :41
	ItemPictoBox       = 0x23 // :42
	ItemSpoilsBag      = 0x24 // :43
	ItemGrapplingHook  = 0x25 // :44
	ItemDeluxePictoBox = 0x26 // :45
	ItemBow            = 0x27 // :46
	ItemPowerBracelets = 0x28 // :47
	ItemIronBoots      = 0x29 // :48
	ItemMagicArmor     = 0x2A // :49
	ItemBaitBag        = 0x2C // :51
	ItemBoomerang      = 0x2D // :52
	ItemHookshot       = 0x2F // :54
	ItemDeliveryBag    = 0x30 // :55
	ItemBombBag        = 0x31 // :56
	ItemSkullHammer    = 0x33 // :58
	ItemDekuLeaf       = 0x34 // :59
	ItemMagicArrow     = 0x35 // :60, fire and ice arrows
	ItemLightArrow     = 0x36 // :61
	ItemSword          = 0x38 // :63, the Hero's Sword
	ItemMasterSword1   = 0x39 // :64, powerless
	ItemMasterSword2   = 0x3A // :65, half power
	ItemShield         = 0x3B // :66, the Hero's Shield
	ItemMirrorShield   = 0x3C // :67
	ItemMasterSword3   = 0x3E // :69, full power
	ItemSail           = 0x78 // :127
)

// Inventory slots, the index i of mItems[i] (dInvSlot_e,
// d_com_inf_game.h:1072-1095). 14-17 are the bottles.
const (
	SlotTelescope     = 0
	SlotSail          = 1
	SlotWindWaker     = 2
	SlotGrapplingHook = 3
	SlotSpoilsBag     = 4
	SlotBoomerang     = 5
	SlotDekuLeaf      = 6
	SlotTingleTuner   = 7
	SlotCamera        = 8
	SlotIronBoots     = 9
	SlotMagicArmor    = 10
	SlotBaitBag       = 11
	SlotBow           = addr.SlotBow // 12
	SlotBomb          = 13
	SlotDeliveryBag   = addr.SlotDeliveryBag // 18
	SlotHookshot      = 19
	SlotSkullHammer   = 20
)

// The equipment bytes, the index i of mSelectEquip[i] and of the
// mCollect[i] byte whose bits give it (d_save.h:46; dComIfGs_setSelectEquip,
// d_com_inf_game.cpp:1786-1822). mSelectEquip[3] has no setter.
const (
	EquipSword    = 0
	EquipShield   = 1
	EquipBracelet = 2
)

// Variant is one item a slot can hold and the flag bit that gives it.
type Variant struct {
	Bit  uint8 // the bit of the slot's flag byte
	Item byte  // the item number the slot then holds
	Name string
}

// Slot is one byte the game fills as it hands an item over, and the flag
// byte it follows: an inventory slot mItems[i] and its get-item flags
// mItemFlags[i] (dComIfGs_onGetItem sets bit n of mItemFlags[i],
// dSv_player_get_item_c::onItem, d_save.cpp:204-207, as the item_func sets
// the slot), or an equipment byte mSelectEquip[i] and its collect flags
// mCollect[i] (dSv_player_collect_c::onCollect, d_save.cpp:745-748, as the
// item_func equips the item).
type Slot struct {
	Name  string
	Index int // the mItems or mSelectEquip index
	// Off is the image offset of the slot byte, FlagOff that of its flag
	// byte (addr.Off*).
	Off, FlagOff int
	// Variants are the items the flags give, lesser first: the slot holds
	// the best one whose bit is set.
	Variants []Variant
	Cite     string // the item_funcs that set the flag and fill the slot
}

// Best returns the best variant flags give; ok is false when none of the
// slot's bits is set. Bits no variant names are ignored: mItemFlags[0..2]
// also hold the bottle-content flags above bit 0
// (dSv_player_get_item_c::onBottleItem, d_save.cpp:224-282).
func (s Slot) Best(flags byte) (v Variant, ok bool) {
	for i := len(s.Variants) - 1; i >= 0; i-- {
		if flags&(1<<s.Variants[i].Bit) != 0 {
			return s.Variants[i], true
		}
	}
	return Variant{}, false
}

// Rank returns item's place among the slot's variants, 0 for the least,
// or -1 when item is none of them (ItemNone included).
func (s Slot) Rank(item byte) int {
	return slices.IndexFunc(s.Variants, func(v Variant) bool { return v.Item == item })
}

// Derive returns what the slot holds once flags are applied to it while
// it holds cur, as §6.6 has it: the best variant the flags give, but only
// in place of ItemNone or a lesser variant. Nothing is ever taken away,
// and a slot holding an item that is none of its variants keeps it.
func (s Slot) Derive(cur, flags byte) byte {
	best, ok := s.Best(flags)
	if !ok {
		return cur
	}
	if cur == ItemNone || s.Rank(cur) >= 0 && s.Rank(cur) < s.Rank(best.Item) {
		return best.Item
	}
	return cur
}

// ItemName is the name of the variant holding item, "nothing" for
// ItemNone, or the item number.
func (s Slot) ItemName(item byte) string {
	if item == ItemNone {
		return "nothing"
	}
	if i := s.Rank(item); i >= 0 {
		return s.Variants[i].Name
	}
	return fmt.Sprintf("item 0x%02X", item)
}

func itemSlot(name string, slot int, cite string, vs ...Variant) Slot {
	return Slot{Name: name, Index: slot, Off: addr.OffItems + slot, FlagOff: addr.OffGetItem + slot, Variants: vs, Cite: cite}
}

func equipSlot(name string, i int, cite string, vs ...Variant) Slot {
	return Slot{Name: name, Index: i, Off: addr.OffSelectEquip + i, FlagOff: addr.OffCollectFlags + i, Variants: vs, Cite: cite}
}

// itemSlots are the inventory slots the get-item flags give, every slot
// but the four bottles, which are each player's own (§6.6; class table
// "mItems bottles"). The game derives the bow slot the same way itself, at
// every stage load outside GTower (d_s_play.cpp:1385-1392).
var itemSlots = []Slot{
	itemSlot("telescope", SlotTelescope, "item_func_telescope, d_item.cpp:691-695",
		Variant{0, ItemTelescope, "the telescope"}),
	itemSlot("sail", SlotSail, "item_func_normal_sail, d_item.cpp:1120-1123",
		Variant{0, ItemSail, "the sail"}),
	itemSlot("Wind Waker", SlotWindWaker, "item_func_wind_tact, d_item.cpp:703-707",
		Variant{0, ItemWindWaker, "the Wind Waker"}),
	itemSlot("grappling hook", SlotGrapplingHook, "item_func_rope, d_item.cpp:721-725",
		Variant{0, ItemGrapplingHook, "the grappling hook"}),
	itemSlot("spoils bag", SlotSpoilsBag, "item_func_emono_bag, d_item.cpp:715-719",
		Variant{0, ItemSpoilsBag, "the spoils bag"}),
	itemSlot("boomerang", SlotBoomerang, "item_func_boomerang, d_item.cpp:769-773",
		Variant{0, ItemBoomerang, "the boomerang"}),
	itemSlot("Deku Leaf", SlotDekuLeaf, "item_func_deku_leaf, d_item.cpp:810-815",
		Variant{0, ItemDekuLeaf, "the Deku Leaf"}),
	itemSlot("Tingle Tuner", SlotTingleTuner, "item_func_tncl_whitsl, d_item.cpp:697-701",
		Variant{0, ItemTingleTuner, "the Tingle Tuner"}),
	itemSlot("picto box", SlotCamera, "item_func_camera, d_item.cpp:709-713; item_func_camera2, :727-731",
		Variant{0, ItemPictoBox, "the picto box"}, Variant{1, ItemDeluxePictoBox, "the deluxe picto box"}),
	itemSlot("iron boots", SlotIronBoots, "item_func_hvy_boots, d_item.cpp:747-751",
		Variant{0, ItemIronBoots, "the iron boots"}),
	itemSlot("magic armor", SlotMagicArmor, "item_func_drgn_shield, d_item.cpp:753-757",
		Variant{0, ItemMagicArmor, "the magic armor"}),
	itemSlot("bait bag", SlotBaitBag, "item_func_esa_bag, d_item.cpp:763-767",
		Variant{0, ItemBaitBag, "the bait bag"}),
	itemSlot("bow", SlotBow, "item_func_bow, d_item.cpp:733-738; item_func_magic_arrow, :818-830; item_func_light_arrow, :833-845",
		Variant{0, ItemBow, "the bow"}, Variant{1, ItemMagicArrow, "fire and ice arrows"}, Variant{2, ItemLightArrow, "light arrows"}),
	itemSlot("bombs", SlotBomb, "item_func_bomb_bag, d_item.cpp:792-797; item_func_bomb_10..30, :624-643",
		Variant{0, ItemBombBag, "bombs"}),
	itemSlot("delivery bag", SlotDeliveryBag, "item_func_warasibe_bag, d_item.cpp:786-790",
		Variant{0, ItemDeliveryBag, "the delivery bag"}),
	itemSlot("hookshot", SlotHookshot, "item_func_hookshot, d_item.cpp:780-784",
		Variant{0, ItemHookshot, "the hookshot"}),
	itemSlot("Skull Hammer", SlotSkullHammer, "item_func_hummer, d_item.cpp:804-808",
		Variant{0, ItemSkullHammer, "the Skull Hammer"}),
}

// equipSlots are mSelectEquip[0..2], from mCollect[0..2]. Each item_func
// sets the collect bit and equips the item (dComIfGs_setSelectEquip sets
// the same bit again, d_com_inf_game.cpp:1786-1822), and the game counts a
// sword or shield as owned only while it is equipped
// (dComIfGs_checkGetItem, d_com_inf_game.cpp:1003-1007).
var equipSlots = []Slot{
	equipSlot("sword", EquipSword,
		"item_func_sword, d_item.cpp:848-852; item_func_master_sword, :854-858; item_func_lv3_sword, :860-864; item_func_master_sword_ex, :884-888",
		Variant{0, ItemSword, "the Hero's Sword"}, Variant{1, ItemMasterSword1, "the Master Sword"},
		Variant{2, ItemMasterSword2, "the half-powered Master Sword"}, Variant{3, ItemMasterSword3, "the full-powered Master Sword"}),
	equipSlot("shield", EquipShield, "item_func_shield, d_item.cpp:866-870; item_func_mirror_shield, :872-876",
		Variant{0, ItemShield, "the Hero's Shield"}, Variant{1, ItemMirrorShield, "the Mirror Shield"}),
	equipSlot("bracelets", EquipBracelet, "item_func_pwr_groove, d_item.cpp:741-745",
		Variant{0, ItemPowerBracelets, "the power bracelets"}),
}

// ItemSlots returns the derived inventory slots, in slot order.
func ItemSlots() []Slot { return slices.Clone(itemSlots) }

// EquipSlots returns the derived equipment bytes, sword first.
func EquipSlots() []Slot { return slices.Clone(equipSlots) }
