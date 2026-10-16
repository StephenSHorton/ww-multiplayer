package tables

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
)

// timedCrystal is a crystal switch with a timer, which clears sw again
// (switches' SW_HIT0 decoder, d_a_swhit0.cpp:337-365): never synced.
func timedCrystal(sw uint32) stagetest.Actor {
	return stagetest.Actor{Name: "SW_HIT0", Params: 0x10<<20 | sw, Angle: [3]int16{0, 0, 0xFF}, SetID: 0xFFFF}
}

// TestSyncedScopedSwitch: the verdict on the switches SCOPED carries, per
// store: a dan switch per save table, a zone or room switch per stage and
// room; never a memory switch, never what the game asserts on.
func TestSyncedScopedSwitch(t *testing.T) {
	stages := replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", keys.TableDRC,
		tablestest.Crystal(0x20), tablestest.Crystal(0x85), tablestest.Crystal(0xC3), tablestest.Crystal(0xE1),
		timedCrystal(0x86), timedCrystal(0xC4), timedCrystal(0xE2),
		tablestest.PushBlock(1, 0xC8, 0xC9)))
	tb := buildFixture(t, stages)
	const drc, boss = "M_NewD2", "M_DragB"
	var none *Tables
	for _, c := range []struct {
		name  string
		tb    *Tables
		table int
		stage string
		room  int
		no    int
		want  bool
	}{
		{"a latching dan crystal", tb, keys.TableDRC, drc, 1, 0x85, true},
		{"the dan switch from the table's boss stage", tb, keys.TableDRC, boss, 0, 0x85, true},
		{"the dan switch from nowhere in the table", tb, keys.TableDRC, "", -1, 0x85, true},
		{"the dan switch's number in another table", tb, keys.TableFW, "kindan", 1, 0x85, false},
		{"a timed dan crystal", tb, keys.TableDRC, drc, 1, 0x86, false},
		{"a latching zone crystal", tb, keys.TableDRC, drc, 1, 0xC3, true},
		{"the zone switch in another room", tb, keys.TableDRC, drc, 0, 0xC3, false},
		{"the zone switch in another stage of the table", tb, keys.TableDRC, boss, 1, 0xC3, false},
		{"the zone switch with another table", tb, keys.TableFW, drc, 1, 0xC3, false},
		{"a timed zone crystal", tb, keys.TableDRC, drc, 1, 0xC4, false},
		{"a push-block path switch", tb, keys.TableDRC, drc, 1, 0xC8, false},
		{"a latching room switch", tb, keys.TableDRC, drc, 1, 0xE1, true},
		{"a timed room switch", tb, keys.TableDRC, drc, 1, 0xE2, false},
		{"a zone switch nothing sets", tb, keys.TableDRC, drc, 1, 0xDF, false},
		{"a latching memory switch is not SCOPED's", tb, keys.TableDRC, drc, 1, 0x20, false},
		{"room -1", tb, keys.TableDRC, drc, -1, 0xC3, false},
		{"room 64", tb, keys.TableDRC, drc, addr.RoomCount, 0xC3, false},
		{"no stage", tb, keys.TableDRC, "", 1, 0xC3, false},
		{"save table 16", tb, addr.TableCount, drc, 1, 0x85, false},
		{"save table -1", tb, -1, drc, 1, 0x85, false},
		{"switch 0xF0", tb, keys.TableDRC, drc, 1, 0xF0, false},
		{"switch 0xFE", tb, keys.TableDRC, drc, 1, 0xFE, false},
		{"switch 0xFF", tb, keys.TableDRC, drc, 1, 0xFF, false},
		{"switch -1", tb, keys.TableDRC, drc, 1, -1, false},
		{"switch 0x100", tb, keys.TableDRC, drc, 1, 0x100, false},
		{"no tables", none, keys.TableDRC, drc, 1, 0xC3, false},
		{"no switch table", tb.Only(SectionKeys), keys.TableDRC, drc, 1, 0xC3, false},
	} {
		if got := c.tb.SyncedScopedSwitch(c.table, c.stage, c.room, c.no); got != c.want {
			t.Errorf("%s: SyncedScopedSwitch(%d, %q, %d, 0x%X) = %v, want %v", c.name, c.table, c.stage, c.room, c.no, got, c.want)
		}
	}
	// The memory switch keeps its own verdict.
	if !tb.SyncedMemorySwitch(keys.TableDRC, 0x20) {
		t.Error("DRC's memory crystal 0x20 is no longer synced")
	}
	// A method value of nil tables is a usable verdict: it grants nothing.
	if f := none.SyncedScopedSwitch; f(keys.TableDRC, drc, 1, 0xC3) {
		t.Error("nil tables grant a zone switch")
	}
}

// TestGBASwitch: a switch a Tingle Tuner switch (agbsw0) shares with a
// latching setter syncs, and is GBA-dependent, in every store; one only
// the Tuner writes doesn't sync; a Tuner type the decomp's table doesn't
// reach keeps its switch unsynced.
func TestGBASwitch(t *testing.T) {
	stages := replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", keys.TableDRC,
		tablestest.Crystal(0x3D), tablestest.TingleTuner(0, 0x3D, 0xFF),
		tablestest.Crystal(0x87), tablestest.TingleTuner(7, 0x87, 0xC5), tablestest.Crystal(0xC5),
		tablestest.Crystal(0xE3), tablestest.TingleTuner(8, 0xFF, 0xE3),
		tablestest.TingleTuner(0, 0x3A, 0xFF),
		tablestest.Crystal(0x3B), tablestest.TingleTuner(0x0F, 0x3B, 0xFF),
		tablestest.Crystal(0x20)))
	tb := buildFixture(t, stages)
	const drc = "M_NewD2"
	var none *Tables
	for _, c := range []struct {
		name        string
		tb          *Tables
		no          int
		synced, gba bool
		memory      bool
	}{
		{"a key door's switch the Tuner names", tb, 0x3D, true, true, true},
		{"a dan crystal agbMW clears", tb, 0x87, true, true, false},
		{"a zone crystal the Tuner names", tb, 0xC5, true, true, false},
		{"a room crystal agbCSW sets", tb, 0xE3, true, true, false},
		{"a switch only the Tuner writes", tb, 0x3A, false, false, true},
		{"a type past ExeSubTable", tb, 0x3B, false, false, true},
		{"a crystal the Tuner doesn't name", tb, 0x20, true, false, true},
		{"no tables", none, 0x3D, false, false, true},
	} {
		var synced, gba bool
		if c.memory {
			synced, gba = c.tb.SyncedMemorySwitch(keys.TableDRC, c.no), c.tb.GBAMemorySwitch(keys.TableDRC, c.no)
			if c.tb.GBAScopedSwitch(keys.TableDRC, drc, 1, c.no) {
				t.Errorf("%s: GBAScopedSwitch takes a memory switch", c.name)
			}
		} else {
			synced, gba = c.tb.SyncedScopedSwitch(keys.TableDRC, drc, 1, c.no), c.tb.GBAScopedSwitch(keys.TableDRC, drc, 1, c.no)
			if c.tb.GBAMemorySwitch(keys.TableDRC, c.no) {
				t.Errorf("%s: GBAMemorySwitch takes a scoped switch", c.name)
			}
		}
		if synced != c.synced || gba != c.gba {
			t.Errorf("%s: synced %v, GBA %v; want %v, %v", c.name, synced, gba, c.synced, c.gba)
		}
	}
	if tb.GBAScopedSwitch(keys.TableDRC, drc, 0, 0xC5) || tb.GBAScopedSwitch(keys.TableDRC, "", -1, 0xC5) {
		t.Error("the zone switch is GBA-dependent in another room")
	}
	if !tb.GBAScopedSwitch(keys.TableDRC, "", -1, 0x87) {
		t.Error("a dan switch's GBA verdict depends on the stage")
	}
}
