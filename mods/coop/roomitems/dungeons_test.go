package roomitems

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
)

// TestDungeons reads a mid-game world: DRC done (map, compass, big key,
// Gohma, its heart container), FW part way (map and compass), FF's boss
// intro seen, the rest untouched.
func TestDungeons(t *testing.T) {
	var img save.Image
	set := func(tb int, bits ...int) {
		for _, b := range bits {
			img[addr.OffMemBit(tb)+addr.MemBitOffDungeonItem] |= 1 << b
		}
	}
	set(keys.TableDRC, addr.DungeonItemMap, addr.DungeonItemCompass, addr.DungeonItemBossKey,
		addr.DungeonItemStageBossEnemy, addr.DungeonItemStageLife, addr.DungeonItemStageBossDemo)
	set(keys.TableFW, addr.DungeonItemMap, addr.DungeonItemCompass)
	set(addr.TableFF, addr.DungeonItemStageBossDemo)
	img[addr.OffMemBit(keys.TableDRC)+addr.MemBitOffKeyNum] = 2 // derived: never read here
	shared := world.Shared(&img)
	ds := Of(&shared, nil).Dungeons
	want := []struct {
		short string
		table int
		keys  int
		items save.DungeonItems
	}{
		{"FF", addr.TableFF, 0, save.DungeonItems{BossDemo: true}},
		{"DRC", keys.TableDRC, 4, save.DungeonItems{Map: true, Compass: true, BossKey: true, Boss: true, HeartContainer: true, BossDemo: true}},
		{"FW", keys.TableFW, 1, save.DungeonItems{Map: true, Compass: true}},
		{"TotG", keys.TableTotG, 2, save.DungeonItems{}},
		{"ET", keys.TableET, 3, save.DungeonItems{}},
		{"WT", keys.TableWT, 2, save.DungeonItems{}},
		{"GT", addr.TableGT, 0, save.DungeonItems{}},
	}
	if len(ds) != len(want) {
		t.Fatalf("got %d dungeons, want %d", len(ds), len(want))
	}
	for i, w := range want {
		d := ds[i]
		if d.Short != w.short || d.Table != w.table || d.Keys != w.keys || d.DungeonItems != w.items {
			t.Errorf("dungeon %d = %+v, want %s table %d keys %d %+v", i, d, w.short, w.table, w.keys, w.items)
		}
	}
}

// TestDungeonsShared: the world holds every mDungeonItem bit (an OR byte
// with DungeonItemMask), so a world image keeps them all.
func TestDungeonsShared(t *testing.T) {
	for _, d := range dungeonList {
		if m := world.SharedMask(addr.OffMemBit(d.Table) + addr.MemBitOffDungeonItem); m != addr.DungeonItemMask {
			t.Errorf("%s: shared mask %#x, want %#x", d.Short, m, addr.DungeonItemMask)
		}
	}
}

func TestDungeonOfStage(t *testing.T) {
	for stage, want := range map[string]int{"M_NewD2": keys.TableDRC, "kinBOSS": keys.TableFW, "SirenB": keys.TableTotG,
		"M_DaiMB": keys.TableET, "kaze": keys.TableWT, "Mjtower": addr.TableFF, "Xboss2": addr.TableGT, "GTower": addr.TableGT} {
		if got, ok := DungeonOfStage(stage); !ok || got != want {
			t.Errorf("%s: %d %v, want %d", stage, got, ok, want)
		}
	}
	for _, stage := range []string{"sea", "Hyrule", "Ekaze", "Edaichi", ""} {
		if _, ok := DungeonOfStage(stage); ok {
			t.Errorf("%s: a dungeon stage", stage)
		}
	}
	// Every mapped table is one of the seven.
	for stage, tb := range dungeonStages {
		found := false
		for _, d := range dungeonList {
			found = found || d.Table == tb
		}
		if !found {
			t.Errorf("%s: table %d is not a listed dungeon", stage, tb)
		}
	}
}
