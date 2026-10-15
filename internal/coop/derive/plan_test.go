package derive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
)

// drcTable is a passing two-key table: chests 1 and 2, doors 0x10 and
// 0x11.
func drcTable() *keys.Table {
	src := func(bit int) itemsrc.Source {
		return itemsrc.Source{Kind: itemsrc.Chest, Item: itemsrc.ItemSmallKey,
			Flag: itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: addr.TableDRC, Bit: bit},
			At:   []itemsrc.Where{{Stage: "M_NewD2", Room: 1}}}
	}
	return &keys.Table{
		SaveTable: addr.TableDRC, Dungeon: "DRC", Expected: 2,
		Sources: []itemsrc.Source{src(1), src(2)},
		Doors:   []keys.Door{{Switch: 0x10, Saved: true}, {Switch: 0x11, Saved: true}},
		Pass:    true,
	}
}

func TestCountKeys(t *testing.T) {
	s := snapAt(addr.TableDRC)
	setLive(s, must(addr.TboxBit(1)))
	setSaved(s, addr.TableDRC, must(addr.TboxBit(2)))
	_, b, _ := addr.SwitchBit(0x11)
	setLive(s, b)
	s.Save[addr.InfoOffMemory+addr.MemBitOffKeyNum] = 1
	got := CountKeys(ViewOf(s), drcTable())
	want := KeyFacts{Table: addr.TableDRC, Sources: 2, Taken: 2, Doors: 2, Open: 1, Have: 1}
	if got != want {
		t.Errorf("CountKeys = %+v, want %+v", got, want)
	}
	if got.Derived() != 1 {
		t.Errorf("Derived = %d, want 1", got.Derived())
	}
}

func TestKeyTarget(t *testing.T) {
	for _, tt := range []struct {
		taken, open, extra, want int
	}{
		{0, 0, 0, 0},
		{2, 1, 0, 1},
		{2, 1, 1, 2},
		{0, 1, 0, 0},    // more doors than keys: clamped at 0
		{1, 2, 1, 0},    // a door takes an extra key too: the clamp comes last
		{0, 2, 3, 1},    // two doors take two of the three
		{1, 2, 0, 0},    // keys(t) -1, nothing to take
		{1, 0, 200, 99}, // the HUD's cap
	} {
		f := KeyFacts{Taken: tt.taken, Open: tt.open}
		if got := KeyTarget(f, tt.extra); got != tt.want {
			t.Errorf("KeyTarget(taken %d, open %d, extra %d) = %d, want %d", tt.taken, tt.open, tt.extra, got, tt.want)
		}
	}
}

func TestPlanKeys(t *testing.T) {
	saved, _ := addr.SavedMemBitAddr(addr.TableDRC)
	tests := []struct {
		name   string
		f      KeyFacts
		c      KeyContext
		ok     bool
		kind   Kind
		at     uint32
		data   []byte
		expect []byte
	}{
		{"HUD, a key more", KeyFacts{Table: 3, Taken: 2, Open: 0, Have: 1}, KeyContext{Loaded: true, KeyHUD: true},
			true, KeyCounter, addr.ItemKeyNumCount, []byte{0, 1}, []byte{0, 0}},
		{"HUD, a key less", KeyFacts{Table: 3, Taken: 2, Open: 2, Have: 1}, KeyContext{Loaded: true, KeyHUD: true},
			true, KeyCounter, addr.ItemKeyNumCount, []byte{0xFF, 0xFF}, []byte{0, 0}},
		{"no HUD", KeyFacts{Table: 3, Taken: 2, Have: 0}, KeyContext{Loaded: true},
			true, KeyLive, addr.LiveKeyNum, []byte{2}, []byte{0}},
		{"another table", KeyFacts{Table: 3, Taken: 2, Open: 2, Have: 1}, KeyContext{KeyHUD: true},
			true, KeySaved, saved + addr.MemBitOffKeyNum, []byte{0}, []byte{1}},
		{"extra kept", KeyFacts{Table: 3, Taken: 1, Open: 1, Have: 0}, KeyContext{Extra: 1},
			true, KeySaved, saved + addr.MemBitOffKeyNum, []byte{1}, []byte{0}},
		{"at target", KeyFacts{Table: 3, Taken: 1, Have: 1}, KeyContext{Loaded: true, KeyHUD: true}, false, 0, 0, nil, nil},
		{"debt clamped at 0", KeyFacts{Table: 3, Open: 1, Have: 0}, KeyContext{Loaded: true, KeyHUD: true}, false, 0, 0, nil, nil},
		{"above the cap", KeyFacts{Table: 3, Have: 120}, KeyContext{}, false, 0, 0, nil, nil},
		{"invalid table", KeyFacts{Table: 16, Taken: 1}, KeyContext{}, false, 0, 0, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := PlanKeys(tt.f, tt.c)
			if ok != tt.ok {
				t.Fatalf("PlanKeys ok = %v, want %v (%v)", ok, tt.ok, w)
			}
			if !ok {
				return
			}
			if w.Kind != tt.kind || w.Addr != tt.at || !bytes.Equal(w.Data, tt.data) || !bytes.Equal(w.Expect, tt.expect) {
				t.Errorf("PlanKeys = %v %#x data % X expect % X; want %v %#x data % X expect % X",
					w.Kind, w.Addr, w.Data, w.Expect, tt.kind, tt.at, tt.data, tt.expect)
			}
			if w.Table != tt.f.Table || !strings.Contains(w.Reason, "small keys, table 3") {
				t.Errorf("table %d, reason %q", w.Table, w.Reason)
			}
		})
	}
}

func TestKeysEnabled(t *testing.T) {
	failed := drcTable()
	failed.Pass, failed.Problems = false, []string{"3 small-key source(s) but 2 key door(s)"}
	notDungeon := drcTable()
	notDungeon.Expected = 0
	unsaved := drcTable()
	unsaved.Sources[1].Flag = itemsrc.Flag{}
	danDoor := drcTable()
	danDoor.Doors[0].Switch = 0x85
	badTable := drcTable()
	badTable.SaveTable = 16
	for _, tt := range []struct {
		name string
		t    *keys.Table
		ok   bool
		why  string
	}{
		{"passing", drcTable(), true, ""},
		{"invariant fails", failed, false, "3 small-key source(s) but 2 key door(s)"},
		{"no dungeon", notDungeon, false, "no dungeon"},
		{"unsaved source", unsaved, false, "source 2"},
		{"dungeon-switch door", danDoor, false, "door 1: switch 0x85"},
		{"table 16", badTable, false, "past the last"},
	} {
		ok, why := KeysEnabled(tt.t)
		if ok != tt.ok || !strings.Contains(why, tt.why) {
			t.Errorf("%s: KeysEnabled = %v, %q; want %v, containing %q", tt.name, ok, why, tt.ok, tt.why)
		}
	}
}

// heartTable is a passing-shaped heart table: two stage pieces (sea chests
// 0 and 1), the Catalogue, and two containers (DRC's and FW's STAGE_LIFE).
func heartTable() *hearts.Result {
	piece := func(bit int) itemsrc.Source {
		return itemsrc.Source{Kind: itemsrc.Chest, Item: itemsrc.ItemHeartPiece,
			Flag: itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: addr.TableSea, Bit: bit}}
	}
	cont := func(t int) itemsrc.Source {
		return itemsrc.Source{Kind: hearts.Boss, Item: itemsrc.ItemHeartContainer,
			Flag: itemsrc.Flag{Kind: itemsrc.StageLifeFlag, Table: t}}
	}
	return &hearts.Result{
		Pieces:     []itemsrc.Source{piece(0), piece(1)},
		Containers: []itemsrc.Source{cont(addr.TableDRC), cont(addr.TableFW)},
		Rewards:    append([]hearts.Reward(nil), hearts.Catalogue...),
		Pass:       true,
	}
}

func TestCountLife(t *testing.T) {
	s := snapAt(addr.TableSea)
	setLive(s, must(addr.TboxBit(1)))
	setSaved(s, addr.TableFW, must(addr.DungeonItemBit(addr.DungeonItemStageLife)))
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0x13] |= 0x40                                              // Ivan's
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0xAE] |= 0x03                                              // Hoskit's girlfriend's letter, read
	s.Save[addr.InfoOffSavedata+addr.OffEvent+0xB5] |= 0x01                                              // Komali's father's, not read
	s.Save[addr.InfoOffSavedata+addr.OffMaxLife], s.Save[addr.InfoOffSavedata+addr.OffMaxLife+1] = 0, 19 // 12 + 7
	f := CountLife(ViewOf(s), heartTable())
	want := LifeFacts{Pieces: 2, PiecesTaken: 1, Rewards: f.Rewards, Given: 2, Untracked: f.Untracked,
		Containers: 2, ContainersTaken: 1, Have: 19}
	if f != want {
		t.Errorf("CountLife = %+v, want %+v", f, want)
	}
	// Seven Catalogue records name a save bit: four event bits and three
	// letter registers (tables.RecordOf).
	if f.Rewards != 7 || f.Rewards+f.Untracked != len(hearts.Catalogue) {
		t.Errorf("rewards: %d recorded + %d untracked, want 7 of %d", f.Rewards, f.Untracked, len(hearts.Catalogue))
	}
	if got := f.Derived(); got != StartLife+1+2+4 {
		t.Errorf("Derived = %d, want %d", got, StartLife+1+2+4)
	}
}

func TestPlanLife(t *testing.T) {
	for _, tt := range []struct {
		name         string
		f            LifeFacts
		extra, delta int
		ok           bool
	}{
		{"a piece", LifeFacts{PiecesTaken: 1, Have: 12}, 0, 1, true},
		{"a container and a reward", LifeFacts{ContainersTaken: 1, Given: 1, Have: 12}, 0, 5, true},
		{"extra credited", LifeFacts{PiecesTaken: 1, Have: 14}, 2, 1, true},
		{"at derived", LifeFacts{PiecesTaken: 1, Have: 13}, 0, 0, false},
		{"above derived: never lowered", LifeFacts{PiecesTaken: 1, Have: 16}, 0, 0, false},
		{"the cap", LifeFacts{PiecesTaken: 44, ContainersTaken: 6, Have: 78}, 4, 2, true},
		{"at the cap", LifeFacts{PiecesTaken: 44, ContainersTaken: 6, Have: LifeCap}, 9, 0, false},
	} {
		w, ok := PlanLife(tt.f, tt.extra)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if c, isCounter := w.Counter(); !isCounter || c != gate.MaxLife || w.Kind != LifeCounter || w.Addr != addr.ItemMaxLifeCount ||
			w.Delta() != tt.delta || !bytes.Equal(w.Expect, []byte{0, 0}) || w.Table != -1 {
			t.Errorf("%s: PlanLife = %+v (delta %d), want a max-life counter write of %d", tt.name, w, w.Delta(), tt.delta)
		}
	}
}

func TestLifeEnabled(t *testing.T) {
	failed := heartTable()
	failed.Pass, failed.Problems = false, []string{"43 heart pieces"}
	unknown := heartTable()
	unknown.Pieces[0].Flag = itemsrc.Flag{Kind: itemsrc.ZoneItemFlag, Room: 1, Bit: 0x40}
	for _, tt := range []struct {
		name string
		h    *hearts.Result
		ok   bool
		why  string
	}{
		{"passing", heartTable(), true, ""},
		{"nil", nil, false, "no heart table"},
		{"fails", failed, false, "1 problem"},
		{"unreadable piece", unknown, false, "piece 1"},
	} {
		ok, why := LifeEnabled(tt.h)
		if ok != tt.ok || !strings.Contains(why, tt.why) {
			t.Errorf("%s: LifeEnabled = %v, %q; want %v, containing %q", tt.name, ok, why, tt.ok, tt.why)
		}
	}
}

func TestMagic(t *testing.T) {
	for _, tt := range []struct {
		name          string
		leaf, upgrade bool
		have          int
		derived       int
		delta         int
	}{
		{"nothing", false, false, 0, 0, 0},
		{"the leaf", true, false, 0, MagicLeaf, 16},
		{"the upgrade after it", true, true, MagicLeaf, MagicCap, 16},
		{"both from nothing", true, true, 0, MagicCap, 32},
		{"the upgrade alone", false, true, 0, MagicCap, 32},
		{"a second leaf's 32: kept", true, false, MagicCap, MagicLeaf, 0},
	} {
		s := snapAt(-1)
		if tt.leaf {
			s.Save[addr.InfoOffSavedata+addr.OffGetItem+slotDekuLeaf] |= 1
		}
		if tt.upgrade {
			s.Save[addr.InfoOffSavedata+addr.OffEvent+0x31] |= 0x80
		}
		s.Save[addr.InfoOffSavedata+addr.OffMaxMagic] = byte(tt.have)
		f := CountMagic(ViewOf(s))
		if f.Leaf != tt.leaf || f.Upgrade != tt.upgrade || f.Have != tt.have || f.Derived() != tt.derived {
			t.Errorf("%s: CountMagic = %+v (derived %d)", tt.name, f, f.Derived())
		}
		w, ok := PlanMagic(f)
		if ok != (tt.delta > 0) || ok && (w.Kind != MagicCounter || w.Addr != addr.ItemMaxMagicCount || w.Delta() != tt.delta) {
			t.Errorf("%s: PlanMagic = %+v, %v; want a delta of %d", tt.name, w, ok, tt.delta)
		}
	}
}

func TestStagOf(t *testing.T) {
	for _, tt := range []struct {
		prop byte
		want Stag
	}{
		{0x07, Stag{OK: true, Table: 3, KeyHUD: true}},
		{0x06, Stag{OK: true, Table: 3}},
		{0x17, Stag{OK: true, Table: 0xB, KeyHUD: true}},
	} {
		if got := StagOf(tt.prop); got != tt.want {
			t.Errorf("StagOf(0x%02X) = %+v, want %+v", tt.prop, got, tt.want)
		}
	}
}
