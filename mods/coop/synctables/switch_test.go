package synctables

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// TestSyncedSwitch: the switch table's verdicts, by the key the game
// routes a switch to; nothing for what it can't route.
func TestSyncedSwitch(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	var none *tables.Tables
	for _, c := range []struct {
		name      string
		tb        *tables.Tables
		table, no int
		want      bool
	}{
		{"DRC's latching crystal", tb, keys.TableDRC, 0x20, true},
		{"DRC's key door 0x10", tb, keys.TableDRC, 0x10, true}, // keyInit sets it below 0x80 (d_door.cpp:433-447)
		{"a push-block path switch", tb, keys.TableDRC, 0x30, false},
		{"the other path switch", tb, keys.TableDRC, 0x31, false},
		{"a switch nothing sets", tb, keys.TableDRC, 0x7F, false},
		{"the crystal's number in another table", tb, keys.TableFW, 0x20, false},
		{"a dan switch", tb, keys.TableDRC, 0x80, false},
		{"switch -1", tb, keys.TableDRC, -1, false},
		{"save table 16", tb, addr.TableCount, 0x20, false},
		{"save table -1", tb, -1, 0x20, false},
		{"no tables", none, keys.TableDRC, 0x20, false},
		{"no switch table", tb.Only(tables.SectionKeys), keys.TableDRC, 0x20, false},
	} {
		if got := SyncedMemorySwitch(c.tb, c.table, c.no); got != c.want {
			t.Errorf("%s: SyncedMemorySwitch(%d, 0x%X) = %v, want %v", c.name, c.table, c.no, got, c.want)
		}
	}
	// SyncedSwitch routes a memory switch whatever stage and room ask.
	if !SyncedSwitch(tb, keys.TableDRC, "M_NewD2", 1, 0x20) || !SyncedSwitch(tb, keys.TableDRC, "", -1, 0x20) {
		t.Error("SyncedSwitch of DRC's crystal from its room or from nowhere is false")
	}
	if SyncedSwitch(tb, keys.TableDRC, "M_NewD2", stage.MaxRooms, 0xC0) {
		t.Error("a zone switch of room 64 is synced")
	}
}
