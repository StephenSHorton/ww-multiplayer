package tables

import (
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

func identityOf(t *testing.T, tb *Tables) uint64 {
	t.Helper()
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("identity 0")
	}
	return id
}

// TestIdentity: discs whose stages give the same tables have one identity,
// whatever their main.dol or image format; other stages give another.
func TestIdentity(t *testing.T) {
	vanilla := buildFixture(t, tablestest.Vanilla())
	want := identityOf(t, vanilla)

	patchable := tablestest.PatchableISO(tablestest.Vanilla())
	patched, err := inject.PatchISOInMemory(patchable)
	if err != nil {
		t.Fatal(err)
	}
	other := tablestest.ISO(replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", keys.TableDRC)))
	dols := map[string]bool{}

	for _, c := range []struct {
		name string
		img  []byte
		same bool
	}{
		{"the same disc again", tablestest.ISO(tablestest.Vanilla()), true},
		{"its CISO", ciso(tablestest.ISO(tablestest.Vanilla())), true},
		{"another main.dol with the same names", patchable, true},
		{"that disc patched", patched, true},
		{"other stage data", other, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tb, err := Build(openISO(t, c.img))
			if err != nil {
				t.Fatal(err)
			}
			if got := identityOf(t, tb); (got == want) != c.same {
				t.Errorf("identity %016x, the vanilla fixture's %016x: same %v, want %v", got, want, got == want, c.same)
			}
			dols[tb.DOL] = true
		})
	}
	if len(dols) != 3 {
		t.Errorf("%d main.dol hashes, want 3 (the fixture's, the patchable one and its patched copy)", len(dols))
	}

	// Who built the tables and from which file doesn't count.
	c := *vanilla
	c.Builder, c.ID = "another build", ID{DOL: strings.Repeat("a", 64), StageFiles: strings.Repeat("b", 64)}
	if got := identityOf(t, &c); got != want {
		t.Errorf("identity %016x after changing Builder and ID, want %016x", got, want)
	}
	// What a builder decided does.
	sw := *vanilla.Switches
	sw.Switches = slices.Clone(sw.Switches)
	sw.Switches[0].Synced = !sw.Switches[0].Synced
	c = *vanilla
	c.Switches = &sw
	if got := identityOf(t, &c); got == want {
		t.Error("flipping a switch verdict kept the identity")
	}
}

func TestIdentityErrors(t *testing.T) {
	vanilla := buildFixture(t, tablestest.Vanilla())
	var none *Tables
	for _, c := range []struct {
		name string
		tb   *Tables
	}{
		{"nil", none},
		{"one section", vanilla.Only(SectionSwitches)},
		{"no sections", &Tables{Schema: SchemaVersion}},
	} {
		if id, err := c.tb.Identity(); err == nil || id != 0 {
			t.Errorf("%s: Identity = %016x, %v; want an error", c.name, id, err)
		}
	}
}

// TestSyncedSwitch: the switch table's verdicts, by the key the game
// routes a switch to; nothing for what it can't route.
func TestSyncedSwitch(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	var none *Tables
	for _, c := range []struct {
		name      string
		tb        *Tables
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
		{"no switch table", tb.Only(SectionKeys), keys.TableDRC, 0x20, false},
	} {
		if got := c.tb.SyncedMemorySwitch(c.table, c.no); got != c.want {
			t.Errorf("%s: SyncedMemorySwitch(%d, 0x%X) = %v, want %v", c.name, c.table, c.no, got, c.want)
		}
	}
	// SyncedSwitch routes a memory switch whatever stage and room ask.
	if !tb.SyncedSwitch(keys.TableDRC, "M_NewD2", 1, 0x20) || !tb.SyncedSwitch(keys.TableDRC, "", -1, 0x20) {
		t.Error("SyncedSwitch of DRC's crystal from its room or from nowhere is false")
	}
	if tb.SyncedSwitch(keys.TableDRC, "M_NewD2", stagedata.MaxRooms, 0xC0) {
		t.Error("a zone switch of room 64 is synced")
	}
}
