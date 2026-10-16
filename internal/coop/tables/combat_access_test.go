package tables

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
)

// TestSyncedKill: the vanilla fixture's M_NewD2 room 1 has a ChuChu with
// setID 2 (synced), two Bokoblins sharing setID 1 (never synced) and an
// unkeyed Bokoblin; every argument the game would assert on is false.
func TestSyncedKill(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	for _, c := range []struct {
		table       int
		stage       string
		room, setID int
		want        bool
	}{
		{keys.TableDRC, "M_NewD2", 1, 2, true},
		{keys.TableFW, "M_NewD2", 1, 2, true}, // the key is the stage's, any valid table
		{keys.TableDRC, "M_NewD2", 1, 1, false},
		{keys.TableDRC, "M_NewD2", 1, 0xFFFF, false},
		{keys.TableDRC, "M_NewD2", 0, 2, false},
		{keys.TableDRC, "kindan", 1, 2, false},
		{keys.TableDRC, "", 1, 2, false},
		{-1, "M_NewD2", 1, 2, false},
		{16, "M_NewD2", 1, 2, false},
		{keys.TableDRC, "M_NewD2", 64, 2, false},
		{keys.TableDRC, "M_NewD2", -1, 2, false},
		{keys.TableDRC, "M_NewD2", 1, 0x200, false},
		{keys.TableDRC, "M_NewD2", 1, -1, false},
	} {
		if got := tb.SyncedKill(c.table, c.stage, c.room, c.setID); got != c.want {
			t.Errorf("SyncedKill(%d, %q, %d, 0x%X) = %v, want %v", c.table, c.stage, c.room, c.setID, got, c.want)
		}
	}
	want := enemies.KillSet{}
	want[0] = 1 << 2
	if got := tb.KillMask(keys.TableDRC, "M_NewD2", 1); got != want {
		t.Errorf("KillMask = %08X, want %08X", got, want)
	}
	if got := tb.KillMask(keys.TableDRC, "M_NewD2", 64); got != (enemies.KillSet{}) {
		t.Errorf("KillMask of room 64 = %08X", got)
	}
	var none *Tables
	if none.SyncedKill(keys.TableDRC, "M_NewD2", 1, 2) || (&Tables{}).SyncedKill(keys.TableDRC, "M_NewD2", 1, 2) ||
		none.KillMask(keys.TableDRC, "M_NewD2", 1) != (enemies.KillSet{}) {
		t.Error("a verdict without an enemy table")
	}
}
