package roomitems

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
)

// heartTable is a passing heart table (savemerge's shape): pieces in sea
// chests 0-1, the Ivan reward on event bit 0x1340, containers by table
// 3's and 4's STAGE_LIFE.
func heartTable() *hearts.Result {
	h := &hearts.Result{Pass: true}
	for i := range 2 {
		h.Pieces = append(h.Pieces, itemsrc.Source{Item: itemsrc.ItemHeartPiece, Flag: itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 0, Bit: i}})
	}
	h.Rewards = []hearts.Reward{
		{ID: "ivan", Record: "event bit 0x1340 (UNK_1340, d_save_event_flag.inc:143)"},
		{ID: "minigame"},
	}
	for _, tb := range []int{3, 4} {
		h.Containers = append(h.Containers, itemsrc.Source{Item: itemsrc.ItemHeartContainer, Flag: itemsrc.Flag{Kind: itemsrc.StageLifeFlag, Table: tb}})
	}
	return h
}

func setMap(img *save.Image, off, i int) {
	p := img[off+4*(i>>5):]
	binary.BigEndian.PutUint32(p, binary.BigEndian.Uint32(p)|1<<(i&0x1F))
}

func TestOf(t *testing.T) {
	var img save.Image
	img[addr.OffGetItem+save.SlotTelescope] = 0x01
	img[addr.OffGetItem+save.SlotBow] = 0x03 // bow + fire and ice arrows
	img[addr.OffGetItem+save.SlotCamera] = 0x01
	img[addr.OffGetItem+save.SlotDekuLeaf] = 0x01 // 16 magic
	img[addr.OffCollectFlags+save.EquipSword] = 0x03
	img[addr.OffCollectFlags+save.EquipShield] = 0x01
	img[addr.OffTact] = 0x05     // Requiem, Command Melody
	img[addr.OffSymbol] = 0x02   // Din's
	img[addr.OffTriforce] = 0x81 // two shards
	img[addr.OffMapTriforce] = 0x01
	img[addr.OffWalletSize] = 1
	img[addr.OffArrowMax], img[addr.OffBombMax] = 30, 60
	for _, i := range []int{0, 33, 34, 35, 50, 51} { // 34, 35 (charts 35, 36) and 51 (chart 52) don't count
		setMap(&img, addr.OffMapGet, i)
	}
	setMap(&img, addr.OffMapComplete, 33)
	b, _ := addr.TboxBit(1)
	img[addr.OffMemBit(0)+b.Off] |= b.Mask // a piece
	img[addr.OffMemBit(4)+addr.MemBitOffDungeonItem] |= 1 << addr.DungeonItemStageLife
	img[addr.OffEvent+0x13] |= 0x40 // Ivan's, event 0x1340

	it := Of(&img, heartTable())
	have := func(xs []Item) []string {
		var out []string
		for _, x := range xs {
			if x.Have {
				out = append(out, x.Name)
			}
		}
		return out
	}
	if got, want := have(it.Items), []string{"the telescope", "the Deku Leaf", "the picto box", "fire and ice arrows"}; !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q", got, want)
	}
	if len(it.Items) != len(save.ItemSlots()) {
		t.Errorf("%d item rows, want one per slot (%d)", len(it.Items), len(save.ItemSlots()))
	}
	if got, want := have(it.Equip), []string{"the Master Sword", "the Hero's Shield"}; !slices.Equal(got, want) {
		t.Errorf("equipment = %q, want %q", got, want)
	}
	var songs []string
	for _, s := range it.Songs {
		if s.Have {
			songs = append(songs, s.Name)
		}
	}
	if want := []string{"Wind's Requiem", "Command Melody"}; !slices.Equal(songs, want) || len(it.Songs) != 6 {
		t.Errorf("songs = %q of %d, want %q of 6", songs, len(it.Songs), want)
	}
	if !it.Pearls[1].Have || it.Pearls[0].Have || it.Pearls[2].Have || it.Pearls[1].Name != "Din's Pearl" {
		t.Errorf("pearls = %+v, want Din's alone", it.Pearls)
	}
	if it.Shards != 2 || it.TriforceCharts != 1 {
		t.Errorf("shards %d, Triforce charts %d; want 2, 1", it.Shards, it.TriforceCharts)
	}
	if it.Charts != 3 || it.Salvaged != 1 {
		t.Errorf("charts %d, salvaged %d; want 3 (charts 1, 34, 51), 1", it.Charts, it.Salvaged)
	}
	if it.Wallet != 1000 || it.Quiver != 30 || it.BombBag != 60 || it.Magic != derive.MagicLeaf {
		t.Errorf("wallet %d, quiver %d, bombs %d, magic %d; want 1000, 30, 60, %d", it.Wallet, it.Quiver, it.BombBag, it.Magic, derive.MagicLeaf)
	}
	want := Hearts{Containers: 2, ContainersTaken: 1, Pieces: 2, PiecesTaken: 1, Rewards: 1, Given: 1, Untracked: 1, Max: derive.StartLife + 1 + 1 + 4}
	if it.Hearts == nil || *it.Hearts != want {
		t.Errorf("hearts = %+v, want %+v", it.Hearts, want)
	}
}

func TestOfEmpty(t *testing.T) {
	var img save.Image
	it := Of(&img, nil)
	for _, x := range append(it.Items, it.Equip...) {
		if x.Have || x.Name != "" {
			t.Errorf("empty world has %+v", x)
		}
	}
	if it.Wallet != 200 || it.Hearts != nil || it.HeartsWhy == "" {
		t.Errorf("empty world: wallet %d, hearts %+v (%q); want 200, nil with a why", it.Wallet, it.Hearts, it.HeartsWhy)
	}
	img[addr.OffWalletSize] = 7 // getRupeeMax's default
	if it := Of(&img, nil); it.Wallet != 5000 {
		t.Errorf("wallet size 7: %d, want 5000", it.Wallet)
	}
}

// TestShared: every byte Of reads directly is one the world holds, so the
// card never shows a byte a view can't have (the slots, mMaxLife and
// mMaxMagic are DERIVED: Of derives them from these).
func TestShared(t *testing.T) {
	offs := []int{addr.OffTact, addr.OffSymbol, addr.OffTriforce, addr.OffMapTriforce, addr.OffWalletSize, addr.OffArrowMax, addr.OffBombMax}
	for i := range 16 {
		offs = append(offs, addr.OffMapGet+i, addr.OffMapComplete+i)
	}
	for _, s := range append(save.ItemSlots(), save.EquipSlots()...) {
		offs = append(offs, s.FlagOff)
	}
	for _, off := range offs {
		if world.SharedMask(off) == 0 {
			t.Errorf("image byte 0x%03X is not in the world", off)
		}
	}
}
