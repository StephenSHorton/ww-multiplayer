// Package roomitems reads what a co-op world holds, for the app's Room
// items card (the ww-online review's app ideas: their Room items page,
// datstarkey/ww-online WWOnline.Client/ViewModels/RoomItemsViewModel.cs
// and docs/screenshots/room-items.png, learned from, nothing copied): the
// items and equipment, songs, pearls and Triforce shards, charts, the
// bag sizes and the hearts, as every player's game takes them from the
// world.
//
// Of reads a world image, the hub's or a replica's view of it (§3): only
// bytes the world shares (world.SharedMask) are read, so a card never
// shows a byte the world doesn't hold. Items, equipment and the hearts are
// derived the way the receiver derives them (save.Slot.Best over the
// get-item and collect flags; derive.CountLife over the heart table;
// derive.CountMagic): the slot bytes, mMaxLife and mMaxMagic themselves
// are DERIVED and never in the world. It is read-only: our world only
// grows (§3, OR and MAX), so there is no "remove an item" here, unlike
// theirs.
package roomitems

import (
	"encoding/binary"
	"math/bits"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
)

// Item is one inventory slot or equipment byte: Name is the best variant
// the world's flags give ("" when it has none).
type Item struct {
	Slot string
	Name string
	Have bool
}

// Named is one song or pearl and whether the world has it.
type Named struct {
	Name string
	Have bool
}

// Hearts are the world's heart sources (derive.LifeFacts over the world).
// Max is the max life they give, in quarter hearts (derive.LifeFacts.
// Derived, capped as the HUD caps it).
type Hearts struct {
	Containers      int
	ContainersTaken int
	Pieces          int
	PiecesTaken     int
	Rewards         int
	Given           int
	Untracked       int
	Max             int
}

// Items is what a world holds.
type Items struct {
	Items  []Item
	Equip  []Item
	Songs  []Named
	Pearls []Named
	// Shards is the Triforce shards (mTriforce, 8 bits).
	Shards int
	// Charts is the game's own chart count over the world's charts
	// (dSv_player_map_c::getCollectMapNum), of ChartsTotal; Salvaged the
	// same charts whose sunken treasure is up (isCompleteMap);
	// TriforceCharts the deciphered Triforce charts (mMap+0x81).
	Charts, Salvaged, TriforceCharts int
	// Wallet is the rupee cap (getRupeeMax); Quiver and BombBag the arrow
	// and bomb capacities (0 before the first bow or bomb bag).
	Wallet, Quiver, BombBag int
	// Magic is the max magic the flags give (derive.CountMagic): 0, 16 or
	// 32.
	Magic int
	// Hearts is nil without a heart table that passes its check; HeartsWhy
	// is then why (derive.LifeEnabled).
	Hearts    *Hearts
	HeartsWhy string
}

// ChartsTotal is getCollectMapNum's most: charts 1-51 but 35 and 36.
const ChartsTotal = 49

// Songs, by their mTact bit (dSv_player_collect_c::onTact, d_save.cpp:
// 762-766; item_func_tact_song1..6, d_item.cpp:1089-1117; the melody
// numbers, m_Do_audio.h:61-66).
var songNames = []string{"Wind's Requiem", "Ballad of Gales", "Command Melody", "Earth God's Lyric", "Wind God's Aria", "Song of Passing"}

// Pearls, by their mSymbol bit (onSymbol, d_save.cpp:797-800;
// dSymbol_NAYRU_e..FARORE_e = 0..2, d_com_inf_game.h:1700-1702;
// item_func_pearl1..3, d_item.cpp:1070-1084).
var pearlNames = []string{"Nayru's Pearl", "Din's Pearl", "Farore's Pearl"}

// wallets is getRupeeMax by mWalletSize (d_save.h:20-30): 2 and above
// are 5000.
var wallets = []int{200, 1000, 5000}

// Of reads img, a world image; h is the heart table (nil: no hearts).
func Of(img *save.Image, h *hearts.Result) Items {
	var it Items
	for _, s := range save.ItemSlots() {
		v, ok := s.Best(img[s.FlagOff])
		it.Items = append(it.Items, Item{Slot: s.Name, Name: v.Name, Have: ok})
	}
	for _, s := range save.EquipSlots() {
		v, ok := s.Best(img[s.FlagOff])
		it.Equip = append(it.Equip, Item{Slot: s.Name, Name: v.Name, Have: ok})
	}
	for i, n := range songNames {
		it.Songs = append(it.Songs, Named{Name: n, Have: img[addr.OffTact]&(1<<i) != 0})
	}
	for i, n := range pearlNames {
		it.Pearls = append(it.Pearls, Named{Name: n, Have: img[addr.OffSymbol]&(1<<i) != 0})
	}
	it.Shards = bits.OnesCount8(img[addr.OffTriforce])
	it.TriforceCharts = bits.OnesCount8(img[addr.OffMapTriforce])
	// getCollectMapNum (d_save.cpp:917-927) counts isGetMap(i-1) for
	// i in 1..51 but 35 and 36; the bits are word i>>5, bit i&0x1F of
	// big-endian u32s (isGetMap, d_save.cpp:857-861).
	for i := 1; i <= 51; i++ {
		if i == 35 || i == 36 {
			continue
		}
		if mapBit(img, addr.OffMapGet, i-1) {
			it.Charts++
		}
		if mapBit(img, addr.OffMapComplete, i-1) {
			it.Salvaged++
		}
	}
	if w := int(img[addr.OffWalletSize]); w < len(wallets) {
		it.Wallet = wallets[w]
	} else {
		it.Wallet = wallets[len(wallets)-1]
	}
	it.Quiver, it.BombBag = int(img[addr.OffArrowMax]), int(img[addr.OffBombMax])
	v := viewOf(img)
	it.Magic = derive.CountMagic(v).Derived()
	if ok, why := derive.LifeEnabled(h); !ok {
		it.HeartsWhy = why
	} else {
		f := derive.CountLife(v, h)
		it.Hearts = &Hearts{Containers: f.Containers, ContainersTaken: f.ContainersTaken, Pieces: f.Pieces, PiecesTaken: f.PiecesTaken,
			Rewards: f.Rewards, Given: f.Given, Untracked: f.Untracked, Max: min(f.Derived(), derive.LifeCap)}
	}
	return it
}

// mapBit is chart i's bit in the u32[4] at off.
func mapBit(img *save.Image, off, i int) bool {
	w := binary.BigEndian.Uint32(img[off+4*(i>>5):])
	return w&(1<<(i&0x1F)) != 0
}

// viewOf is a save.View over img alone: no table loaded, so every memBit
// flag is read from its saved slot.
func viewOf(img *save.Image) save.View {
	s := &state.Snapshot{Table: -1}
	copy(s.Save[addr.InfoOffSavedata:], img[:])
	return save.ViewOf(s)
}
