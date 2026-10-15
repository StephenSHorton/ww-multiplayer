package fakegame

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// The game's own item gets, key doors and the few live facts M4's
// receiver reads beyond the save (§6.4, §6.6), as the game makes them:
// each sets its flag and hands the item over in the same frame, through
// the HUD's pending counters where the game does (hud takes them on the
// next Frame). Tests use them to make a player's progress the way play
// would, so a receiver's derive sees what a real game shows it.

// OpenKeyChest is a treasure chest of the loaded stage holding a small
// key, opened: daTbox_c sets its tbox bit in the live memBit
// (dComIfGs_onTbox, d_a_tbox.cpp:858; dSv_memBit_c::onTbox,
// d_save.cpp:1072-1075), and the item's item_func_small_key counts the
// key in through mItemKeyNumCount (d_item.cpp:665-667).
func (g *FakeGame) OpenKeyChest(tbox int) error {
	return g.openChest(tbox, addr.ItemKeyNumCount, 1)
}

// OpenPieceChest is a chest holding a piece of heart, opened: its tbox
// bit, then item_func_kakera_heart's +1 into mItemMaxLifeCount
// (d_item.cpp:583-585).
func (g *FakeGame) OpenPieceChest(tbox int) error {
	return g.openChest(tbox, addr.ItemMaxLifeCount, 1)
}

func (g *FakeGame) openChest(tbox int, counter uint32, n int16) error {
	b, err := addr.TboxBit(tbox)
	if err != nil {
		return assertf("onTbox(%d): %v", tbox, err) // d_save.cpp:1073
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing {
		return fmt.Errorf("fakegame: no stage is loaded")
	}
	g.rmwBit(addr.LiveMemBit, 4, b, true)
	g.addCount(counter, n)
	return nil
}

// UnlockKeyDoor is a locked door Link opens with a small key:
// dDoor_key2_c::keyInit sets the door's memory switch and counts the key
// out through mItemKeyNumCount (d_door.cpp:433-438). sw must be a memory
// switch (below 0x80), as a key door's is.
func (g *FakeGame) UnlockKeyDoor(sw int) error {
	if sw < 0 || sw >= 0x80 {
		return fmt.Errorf("fakegame: key door switch 0x%X is no memory switch", sw)
	}
	if err := g.OnSwitch(sw, -1); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addCount(addr.ItemKeyNumCount, -1)
	return nil
}

// addCount adds n to the s16 pending counter at a, as the play struct's
// set*Count setters do: each adds to the count already pending
// (setItemMaxLifeCount, setItemKeyNumCount, d_com_inf_game.h:441, :460).
func (g *FakeGame) addCount(a uint32, n int16) {
	g.st16(a, uint16(int16(g.ld16(a))+n))
}

// GetItem is an item_func that gives slot's item: the get-item flag bit
// (dComIfGs_onGetItem, d_com_inf_game.h:1912-1914;
// dSv_player_get_item_c::onItem, d_save.cpp:204-207) and the slot set to
// item (dComIfGs_setItem, d_com_inf_game.h:1148-1150), as item_func_bow
// does with the bow (d_item.cpp:733-735). A grant's capacity is
// SetCapacity's.
func (g *FakeGame) GetItem(slot, bit int, item byte) error {
	if slot < 0 || slot >= addr.ItemsSize || bit < 0 || bit >= 8 {
		return assertf("onItem(%d, %d)", slot, bit) // d_save.cpp:205
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	a := addr.SaveImage + uint32(addr.OffGetItem+slot)
	g.st8(a, g.ld8(a)|1<<bit)
	g.st8(addr.SaveImage+uint32(addr.OffItems+slot), item)
	return nil
}

// SetCapacity sets the capacity byte at image offset off (the wallet,
// arrow or bomb maximum) to v, as the item_funcs do, absolutely
// (dComIfGs_setArrowMax in item_func_bow, d_item.cpp:737;
// item_func_max_arrow_up1, :1380-1384).
func (g *FakeGame) SetCapacity(off int, v byte) error {
	switch off {
	case addr.OffWalletSize, addr.OffArrowMax, addr.OffBombMax:
	default:
		return fmt.Errorf("fakegame: image byte 0x%03X is no capacity", off)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.SaveImage+uint32(off), v)
	return nil
}

// SetStageType sets the loaded STAG chunk's stage type, the low 3 bits of
// mStageTypeAndSchbit's second byte (dStage_stagInfo_GetSTType,
// d_stage.h:1031-1033). The fake keeps one STAG chunk for every stage and
// writes only its mProp when a stage loads, so the type stays until it is
// set again.
func (g *FakeGame) SetStageType(typ int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	const stagOffType = 0x0D
	a := StagChunk + stagOffType
	g.st8(a, g.ld8(a)&^7|byte(typ&7))
}

// PartnerActor is the actor SetPartner puts in mpPlayerPtr[1].
const PartnerActor uint32 = 0x80A02000

// SetPartner loads Link's partner (Medli or Makar) into mpPlayerPtr[1]
// (d_com_inf_game.h:791), or unloads it. While the player controls the
// partner, the meter shows the fly gauge in place of the magic gauge
// (d_meter.cpp:815-816, :4015-4040).
func (g *FakeGame) SetPartner(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var p uint32
	if on {
		p = PartnerActor
	}
	g.st32(addr.PlayerPtr+4, p)
}

// GetDekuLeaf is item_func_deku_leaf (d_item.cpp:810-815): the Deku
// Leaf's get-item flag and slot (dInvSlot_DEKU_LEAF_e,
// d_com_inf_game.h:1079; dItemNo_DEKU_LEAF_e, d_item_data.h:59), then
// +16 into mItemMaxMagicCount. Its +16 into mItemMagicCount, the meter's
// fill, is not modelled.
func (g *FakeGame) GetDekuLeaf() error {
	const slot, item = 6, 0x34
	if err := g.GetItem(slot, 0, item); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addCount(addr.ItemMaxMagicCount, 16)
	return nil
}
