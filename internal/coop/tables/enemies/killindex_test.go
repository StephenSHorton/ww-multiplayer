package enemies

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// killFixture is one dungeon whose room 1 holds every case of M6's kill
// index, and a room 2 of unkeyed enemies only.
func killFixture(t *testing.T) *Table {
	t.Helper()
	boss := at("ACTR", "Bk", 9)
	boss.Params = 0xA // type 0xA: BOSS on creation (d_a_bk.cpp:5030-5034)
	room1 := []stagedata.Actor{
		at("ACTR", "Bk", 1),                         // synced
		at("ACTR", "Stal", 2),                       // synced
		at("ACTR", "Bk", 3),                         // shared with the next: never synced
		at("ACTR", "keeth", 3),                      //
		boss,                                        // BOSS: not counted, never synced
		at("ACTR", "kuro_s", 4),                     // Morth: a generator, never synced
		at("ACTR", "Bb", 5),                         // Kargaroc: synced, its own death unrecorded
		at("ACTR", "pow", 6),                        // Poe: synced (verified by the live check K1)
		at("ACTR", "Bk", 0xFFFF),                    // unkeyed
		at("ACTR", "takara", 7),                     // no enemy
		at("ACTR", synthName(stagedata.ProcBDK), 8), // a boss class
	}
	room2 := []stagedata.Actor{at("ACTR", "Bk", 0xFFFF), at("ACTR", "keeth", 0xFFFF)}
	// Room 3 clears on B; room 4 is all keyed by its K1 entries, but its
	// Mothula (no K1 class) stays.
	room3 := []stagedata.Actor{at("ACTR", "Bk", 10), at("ACTR", "Bk", 11), at("ACTR", "kuro_s", 12)}
	room4 := []stagedata.Actor{at("ACTR", "Bk", 13), at("ACTR", synthName(stagedata.ProcGM), 0xFFFF)}
	return Build([]*stagedata.Stage{stage("M_NewD2", nil, nil, room1, room2, room3, room4)}, testNames(t))
}

func TestKillMaskExcludesBoss(t *testing.T) {
	tbl := killFixture(t)
	m := tbl.KillMask("M_NewD2", 1)
	for _, c := range []struct {
		setID int
		want  bool
	}{{1, true}, {2, true}, {5, true}, {6, true}, {9, false}, {4, false}, {7, false}, {8, false}, {0xFFFF, false}} {
		if got := m.Has(c.setID); got != c.want || tbl.KeyedK1("M_NewD2", 1, c.setID) != c.want {
			t.Errorf("setID %d: synced %v, want %v", c.setID, got, c.want)
		}
	}
	if tbl.KeyedK1("M_NewD2", 0, 1) || tbl.KeyedK1("kindan", 1, 1) || tbl.KeyedK1("M_NewD2", 2, 1) {
		t.Error("a key of another room or stage is synced")
	}
}

func TestKillMaskExcludesShared(t *testing.T) {
	tbl := killFixture(t)
	if tbl.KeyedK1("M_NewD2", 1, 3) {
		t.Error("a key two enemies share is synced")
	}
	// On different layers they never meet in one game: synced.
	room := []stagedata.Actor{at("ACT0", "Bk", 3), at("ACT1", "keeth", 3)}
	split := Build([]*stagedata.Stage{stage("M_NewD2", nil, nil, room)}, testNames(t))
	if !split.KeyedK1("M_NewD2", 1, 3) {
		t.Error("a key two layers' enemies have, never together, is not synced")
	}
}

func TestCoverageGolden(t *testing.T) {
	c := killFixture(t).Coverage()
	want := "K1 classes not BOSS: 14 entries; 11 (78.6%) keyed: 6 sync, 1 Kargaroc only by ice or light arrow, 2 Morth never live, 2 sharing a key, never synced; 3 (21.4%) unkeyed stay per player until M6.1; rooms: 4 with K1 enemies, 2 all keyed (1 clear on B), 1 mixed, 1 none"
	if got := c.Line(); got != want {
		t.Errorf("got\n\t%s\nwant\n\t%s", got, want)
	}
	if c.Synced() != 7 || c.Clearable != 1 || len(c.ByStage) != 1 || c.ByStage[0] != (KillShare{"M_NewD2", 14, 11, 7}) {
		t.Errorf("coverage %+v", c)
	}
	r := killFixture(t).Report()
	for _, s := range []string{"M6 kill coverage", "  " + want, "  BK               7  5 (71.4%)       4 (57.1%)", "clear on B\" counts the rooms"} {
		if !strings.Contains(r, s) {
			t.Errorf("the report lacks %q:\n%s", s, r)
		}
	}
	if strings.Contains(r, "after live checks") {
		t.Errorf("every K1 class is verified, but the report has classes after live checks:\n%s", r)
	}
	// The owner's disc (tables schema 4, every K1 class verified), as
	// docs/08 §7.2 quotes it, has this form.
	owner := KillCoverage{Entries: 1403, Keyed: 333, Unkeyed: 1070, Sync: 324, IceOnly: 9, NeverLive: 0,
		Rooms: 237, AllKeyed: 68, Clearable: 48, Mixed: 14, NoneKeyed: 155}
	if got, want := owner.Line(), "K1 classes not BOSS: 1403 entries; 333 (23.7%) keyed: 324 sync, 9 Kargaroc only by ice or light arrow, 0 Morth never live; 1070 (76.3%) unkeyed stay per player until M6.1; rooms: 237 with K1 enemies, 68 all keyed (48 clear on B), 14 mixed, 155 none"; got != want {
		t.Errorf("got\n\t%s\nwant\n\t%s", got, want)
	}
	// A class not yet verified (none is now) is listed apart.
	later := KillCoverage{Entries: 10, Keyed: 10, Sync: 6, Unverified: []ClassCount{{"TN", 1}, {"PW", 3}}, Rooms: 1, AllKeyed: 1}
	if got, want := later.Line(), "K1 classes not BOSS: 10 entries; 10 (100.0%) keyed: 6 sync, 4 after live checks (TN 1, PW 3), 0 Kargaroc only by ice or light arrow, 0 Morth never live; 0 (0.0%) unkeyed stay per player until M6.1; rooms: 1 with K1 enemies, 1 all keyed (0 clear on B), 0 mixed, 0 none"; got != want {
		t.Errorf("got\n\t%s\nwant\n\t%s", got, want)
	}
}
