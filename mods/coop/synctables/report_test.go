package synctables

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// TestAcceptanceVanilla: the vanilla fixture passes every check.
func TestAcceptanceVanilla(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	var got []string
	for _, c := range Acceptance(tb) {
		got = append(got, fmt.Sprintf("%s %s %s: %s", c.Section, c.Status, c.Name, c.Detail))
	}
	want := []string{
		"keys PASS keys DRC: 4/4 (sources/doors), vanilla 4/4",
		"keys PASS keys FW: 1/1 (sources/doors), vanilla 1/1",
		"keys PASS keys TotG: 2/2 (sources/doors), vanilla 2/2",
		"keys PASS keys ET: 3/3 (sources/doors), vanilla 3/3",
		"keys PASS keys WT: 2/2 (sources/doors), vanilla 2/2",
		"keys PASS key doors outside tables 3-7: none",
		"hearts PASS heart pieces: 44, vanilla 44: 28 in stage files, 11 rewards, 5 unverified rewards",
		"hearts PASS heart containers: 6, vanilla 6",
		"switches PASS push-block exclusion list: 1 push block(s) storing a path in 2 switch use(s), never synced",
		"enemies INFO enemy setID share: 10 enemy entries: 3 (30.0%) with a setID, 3 (30.0%) keyed (0 of them never dead-checked); K1 classes not BOSS: 3 (75.0%) keyed of 4",
		"enemies INFO duplicate (stage, room, setID) keys: 1: 1 with an enemy, 1 with two enemies on a common layer, 1 colliding in one game, 0 on different layers only",
		"enemies INFO M6 kill coverage: K1 classes not BOSS: 4 entries; 3 (75.0%) keyed: 1 sync, 0 Kargaroc only by ice or light arrow, 0 Morth never live, 2 sharing a key, never synced; 1 (25.0%) unkeyed stay per player until M6.1; rooms: 1 with K1 enemies, 0 all keyed (0 clear on B), 1 mixed, 0 none",
	}
	if !slices.Equal(got, want) {
		t.Errorf("acceptance\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestAcceptanceFails breaks each acceptance count in turn.
func TestAcceptanceFails(t *testing.T) {
	v := tablestest.Vanilla()
	doors := func(n int) []stagetest.Actor {
		var d []stagetest.Actor
		for i := range n {
			d = append(d, tablestest.KeyDoor(0x10+uint32(i), i, i+1))
		}
		return d
	}
	var pieces []stagetest.Actor
	for bit := range uint32(27) {
		pieces = append(pieces, tablestest.PieceChest(bit))
	}
	for _, c := range []struct {
		name   string
		stages []stagetest.Stage
		check  string // the check that fails
		detail string
	}{
		{"a DRC key short",
			replace(v, tablestest.Stage("M_NewD2", keys.TableDRC, stage.StageDungeon, nil, doors(4), tablestest.Room(1,
				tablestest.KeyChest(1), tablestest.KeyChest(2), tablestest.KeyChest(3), tablestest.PushBlock(1, 0x30, 0x31)))),
			"keys DRC", "3/4 (sources/doors), vanilla 4/4; 2 problem(s) in the keys section"},
		{"a key door on the sea",
			replace(v, tablestest.Stage("sea", 0, stage.StageSea, nil, []stagetest.Actor{tablestest.KeyDoor(0x05, 1, 2)},
				tablestest.Room(1, append(slices.Clone(pieces), tablestest.PieceChest(27), tablestest.Bitem(2))...))),
			"key doors outside tables 3-7", "1 (table 0: 1)"},
		{"a heart piece short",
			replace(v, tablestest.Stage("sea", 0, stage.StageSea, nil, nil, tablestest.Room(1, append(slices.Clone(pieces), tablestest.Bitem(2))...))),
			"heart pieces", "43, vanilla 44: 27 in stage files, 11 rewards, 5 unverified rewards"},
		{"Molgera missing",
			replace(v, tablestest.Stage("kazeB", keys.TableWT, stage.StageDungeon, nil, nil)),
			"heart containers", "5, vanilla 6; 1 problem(s) in the hearts section"},
		{"no push block",
			replace(v, tablestest.Dungeon("M_NewD2", keys.TableDRC)),
			"push-block exclusion list", "0 push block(s) storing a path in 0 switch use(s), never synced"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var failed []string
			for _, ch := range Acceptance(buildFixture(t, c.stages)) {
				if ch.Status == Fail {
					failed = append(failed, ch.Name+": "+ch.Detail)
				}
			}
			if want := []string{c.check + ": " + c.detail}; !slices.Equal(failed, want) {
				t.Errorf("failed checks %q, want %q", failed, want)
			}
		})
	}
}

func TestReportSections(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	all := Report(tb)
	var at []int
	for _, s := range tables.Sections {
		i := strings.Index(all, "\n== "+string(s)+" ==\n")
		if i < 0 {
			t.Fatalf("report has no %s section:\n%s", s, all)
		}
		at = append(at, i)
	}
	if !slices.IsSorted(at) {
		t.Errorf("sections at %v, want them in tables.Sections order", at)
	}
	if !strings.HasPrefix(all, fmt.Sprintf("Co-op tables (docs/08-coop-design.md §9 M1), schema %d, built by ", tables.SchemaVersion)+tb.Builder+"\n  main.dol SHA-256 "+tb.DOL+"\n") ||
		!strings.Contains(all, "stage files SHA-256 "+tb.StageFiles+": 12 stages, 12 rooms\n  left out 1 stage the play scene can't load: Name (no stage.dzs)\n"+
			"  left out 1 stage on save table 15 (STAGE_TEST, developer test stages): K_Test2\n") {
		t.Errorf("report header:\n%s", all[:min(len(all), 400)])
	}
	if want := Report(tb, tables.Sections...); all != want {
		t.Error("Report() differs from Report(tables.Sections...)")
	}

	keysOnly := Report(tb, tables.SectionKeys)
	for _, s := range []string{"== keys ==", "PASS  keys DRC", "PASS  key doors outside tables 3-7", tb.Keys.Report()} {
		if !strings.Contains(keysOnly, s) {
			t.Errorf("Report(keys) lacks %q", s)
		}
	}
	for _, s := range []string{"== hearts ==", "== switches ==", "== enemies ==", "heart pieces", "push-block", "setID share"} {
		if strings.Contains(keysOnly, s) {
			t.Errorf("Report(keys) has %q", s)
		}
	}

	// Only keeps the header and one section; the others are left out of
	// the report.
	if r := Report(tb.Only(tables.SectionHearts)); !strings.Contains(r, "== hearts ==") || strings.Contains(r, "== keys ==") || strings.Contains(r, "keys DRC") {
		t.Errorf("Report(Only(hearts)):\n%s", r)
	}
}

// TestReportAfterLoad: tables read back from the cache report as the
// tables built.
func TestReportAfterLoad(t *testing.T) {
	dir := t.TempDir()
	tb := buildFixture(t, tablestest.Vanilla())
	if _, err := tb.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := tables.Load(dir, tb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := Report(got), Report(tb); a != b {
		t.Errorf("loaded report differs:\n%s\nwant\n%s", a, b)
	}
}

// FuzzReport: whatever the cache reader accepts (tables.Load; its own
// FuzzDecode fuzzes the decoding) reports and measures without a panic.
//
//	go test -run='^$' -fuzz=FuzzReport -fuzztime=20s ./mods/coop/synctables/
func FuzzReport(f *testing.F) {
	tb := buildFixture(f, tablestest.Vanilla())
	good, err := json.Marshal(tb)
	if err != nil {
		f.Fatal(err)
	}
	empty, err := json.Marshal(map[string]any{
		"schema": tables.SchemaVersion, "builder": tb.Builder, "dol_sha256": tb.DOL, "stage_files_sha256": tb.StageFiles,
		"keys": map[string]any{}, "hearts": map[string]any{}, "switches": map[string]any{}, "enemies": map[string]any{},
	})
	if err != nil {
		f.Fatal(err)
	}
	tt := buildFixture(f, replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", 3,
		tablestest.Crystal(0x3D), tablestest.TingleTuner(0, 0x3D, 0x3A), tablestest.TingleTuner(0x0F, 0x3C, 0xFF))))
	tt.ID = tb.ID
	tuner, err := json.Marshal(tt)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{good, empty, tuner, good[:len(good)/3], []byte("{}")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(tables.CacheFile(dir, tb.DOL), data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := tables.Load(dir, tb.ID)
		if err != nil {
			return
		}
		_ = Report(got)
		_ = Acceptance(got)
	})
}
