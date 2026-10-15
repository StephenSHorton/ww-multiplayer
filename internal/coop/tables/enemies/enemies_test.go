package enemies

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// Procs of the non-enemies the fixtures place (stagedata's constants
// cite their lines).
var (
	procTbox = stagedata.ProcTbox
	procItem = stagedata.ProcItem
)

// realNames are l_objectName names the tests place (d_stage.cpp lines).
var realNames = []struct {
	name string
	proc stagedata.Proc
}{
	{"Bk", stagedata.ProcBK},          // :811
	{"Stal", stagedata.ProcST},        // :948
	{"mo2", stagedata.ProcMO2},        // :642
	{"Tn", stagedata.ProcTN},          // :985
	{"keeth", stagedata.ProcKI},       // :779
	{"Bb", stagedata.ProcBB},          // :653
	{"kuro_s", stagedata.ProcKS},      // :679
	{"pow", stagedata.ProcPW},         // :695
	{"Bdk", stagedata.ProcBDK},        // :864
	{"Ji1", ProcNPCJI1},               // :710
	{"Sitem", ProcSITEM},              // :920
	{"takara", procTbox},              // :483
	{"item", procItem},                // :647
	{"SW_HIT0", stagedata.ProcSwhit0}, // :531
}

// synthName is the name the tests give a class realNames doesn't.
func synthName(p stagedata.Proc) string { return fmt.Sprintf("E%03X", uint16(p)) }

func testNames(t testing.TB) *stagedata.ObjectNames {
	t.Helper()
	var list []stagetest.ObjectName
	for _, n := range realNames {
		list = append(list, stagetest.ObjectName{Name: n.name, Proc: int16(n.proc), Subtype: -1})
	}
	for _, c := range Classes {
		list = append(list, stagetest.ObjectName{Name: synthName(c.Proc), Proc: int16(c.Proc), Subtype: -1})
	}
	names, err := stagedata.ParseObjectNames(stagetest.ObjectNameTable(list...))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// at is an entry of chunk tag (whose layer and entry size follow from the
// tag) named name with setID.
func at(tag, name string, setID uint16) stagedata.Actor {
	a := stagedata.Actor{Tag: tag, Layer: stagedata.AllLayers, Name: name, SetID: setID, Scaled: stagetest.Scaled(tag)}
	if len(tag) == 4 && (tag[:3] == "ACT" || tag[:3] == "SCO" || tag[:3] == "TRE") && tag != "ACTR" && tag != "SCOB" {
		a.Layer = strings.IndexByte("0123456789ab", tag[3])
	}
	return a
}

// stage builds a stage: rooms[i] is room i's entries (nil for no room),
// dzs the stage's own.
func stage(name string, dzs []stagedata.Actor, rooms ...[]stagedata.Actor) *stagedata.Stage {
	s := &stagedata.Stage{Name: name, DZS: &stagedata.DZx{Actors: index(dzs)}}
	for no, as := range rooms {
		if as != nil {
			s.Rooms = append(s.Rooms, stagedata.Room{No: no, DZR: &stagedata.DZx{Actors: index(as)}})
		}
	}
	return s
}

// index numbers entries within each chunk, as ParseDZx does.
func index(as []stagedata.Actor) []stagedata.Actor {
	out := slices.Clone(as)
	n := make(map[string]int)
	for i := range out {
		out[i].Index = n[out[i].Tag]
		n[out[i].Tag]++
	}
	return out
}

func TestClasses(t *testing.T) {
	if len(Classes) != 47 {
		t.Errorf("%d classes, want the 46 fopAc_ENEMY_e profiles plus Morth", len(Classes))
	}
	names := make(map[string]bool)
	for i, c := range Classes {
		if i > 0 && Classes[i-1].Proc >= c.Proc {
			t.Errorf("classes out of proc order at %s", c.Name)
		}
		if names[c.Name] {
			t.Errorf("class name %s twice", c.Name)
		}
		names[c.Name] = true
		if c.Proc <= 0 || c.Proc >= stagedata.ProcMaxNum {
			t.Errorf("%s: proc 0x%X out of range", c.Name, uint16(c.Proc))
		}
		if want := GroupEnemy; c.Proc == stagedata.ProcKS {
			if c.Group != GroupEnv {
				t.Errorf("KS group %v, want ENV (d_a_ks.cpp:1788)", c.Group)
			}
		} else if c.Group != want {
			t.Errorf("%s group %v", c.Name, c.Group)
		}
		// stagedata's own names for its constants agree.
		if s := c.Proc.String(); !strings.HasPrefix(s, "proc ") && s != c.Name {
			t.Errorf("%s: stagedata calls proc 0x%X %s", c.Name, uint16(c.Proc), s)
		}
		// Every class polling enemy_ice has one; a stub's may be unseen.
		if (c.Death == DeathOwn || c.Death == DeathIce) && c.Ice == 0 {
			t.Errorf("%s records through enemy_ice but has no enemyice", c.Name)
		}
		if (c.Boss == BossParams) != (c.bossIf != nil) {
			t.Errorf("%s: boss %s with bossIf %v", c.Name, c.Boss, c.bossIf != nil)
		}
		if got, ok := ClassOf(c.Proc); !ok || got.Name != c.Name || !IsEnemy(c.Proc) {
			t.Errorf("ClassOf(%s) = %v, %v", c.Name, got.Name, ok)
		}
	}
	if IsEnemy(stagedata.ProcTbox) || IsEnemy(stagedata.ProcPlayScene) {
		t.Error("a chest or the play scene is an enemy")
	}

	// §7.2's K1 table: proc and the enemyice mLightShrinkTimer offset
	// (enemyice + 6, c_damagereaction.h:15), which the headers confirm.
	k1 := map[string]struct {
		proc  stagedata.Proc
		timer uint32
	}{
		"BK": {0xBE, 0x126A}, "MO2": {0xBD, 0x2A5A}, "ST": {0xBF, 0x1FAE}, "RD": {0xE1, 0x6E6},
		"CC": {0xCF, 0x822}, "KI": {0xD8, 0x92A}, "AM": {0xCB, 0xAC2}, "AM2": {0xCC, 0xA92},
		"BB": {0xB6, 0xC86}, "FM": {0x79, 0xAEE}, "KS": {0xCE, 0x6AE}, "TN": {0xC0, 0x152A},
		"PH": {0xD2, 0x99E}, "PT": {0xF9, 0x7EA}, "PW": {0xD5, 0x88A}, "BO": {0xD7, 0xA02},
	}
	got := K1Classes()
	if len(got) != len(k1) {
		t.Errorf("%d K1 classes, want %d", len(got), len(k1))
	}
	for _, c := range got {
		w, ok := k1[c.Name]
		if !ok {
			t.Errorf("%s is K1, §7.2 doesn't list it", c.Name)
			continue
		}
		if c.Proc != w.proc || c.Ice+6 != w.timer {
			t.Errorf("%s: proc 0x%X timer +0x%X, §7.2 has 0x%X +0x%X", c.Name, uint16(c.Proc), c.Ice+6, uint16(w.proc), w.timer)
		}
	}
	// §7.2 says Keese and Kargaroc move home.pos; so does a Bokoblin on
	// a searchlight.
	var movers []string
	for _, c := range got {
		if c.HomeMoves {
			movers = append(movers, c.Name)
		}
	}
	if !slices.Equal(movers, []string{"BB", "BK", "KI"}) {
		t.Errorf("K1 classes moving home.pos: %v", movers)
	}
	// §7.2's boss denylist.
	for _, n := range []string{"BDK", "BMD", "BWD", "BST", "BTD", "BGN", "BGN2", "BGN3", "GND", "BPW", "FGANON", "DAIOCTA"} {
		if c := classByName(n); c.Kind != Boss {
			t.Errorf("%s kind %q, want boss", n, c.Kind)
		}
	}
	for d, want := range map[Death]bool{DeathOwn: true, DeathIce: true, DeathUnknown: true, DeathNone: false, "": false} {
		if d.MayRecord() != want {
			t.Errorf("%q.MayRecord() = %v", d, !want)
		}
	}
}

func TestIsBoss(t *testing.T) {
	az := func(z int) [3]int16 { return [3]int16{0, 0, int16(uint16(z))} }
	for _, c := range []struct {
		class  string
		params uint32
		angle  [3]int16
		want   bool
	}{
		{"BK", 0xA, az(0), true},
		{"BK", 0x1A, az(0), true},      // & 0xF
		{"BK", 0xB, az(0), false},      // read as type 4
		{"BK", 0x4, az(0x10), false},   // a death switch makes no boss
		{"ST", 0, az(0x05), true},      // memory switch death
		{"ST", 0, az(0x7F), true},      //
		{"ST", 0, az(0x80), false},     // dan
		{"ST", 0, az(0xFF), false},     // none
		{"ST", 0, az(0), false},        //
		{"ST", 0, az(0x1205), true},    // the u8 of angle z
		{"MO2", 0xFF, az(0x01), true},  //
		{"MO2", 0, az(0xC0), false},    //
		{"TN", 0, az(0x10), true},      //
		{"TN", 13, az(0), true},        // BEHAVIOR_MINIBOSS
		{"TN", 0x1E, az(0xFF), true},   // BEHAVIOR_MSWORD_CHAMBER_AMBUSH
		{"TN", 12, az(0xFF), false},    //
		{"BDK", 0, az(0), true},        // the profile
		{"BPW", 0, az(0), true},        // body_create_init
		{"KI", 0xA, az(5), false},      //
		{"PW", 0xA, az(5), false},      // unknown: a stub
		{"FGANON", 0, az(0), false},    // no BOSS status
		{"DAIOCTA", 0, az(0), false},   //
		{"NPC_JI1", 0xA, az(5), false}, //
	} {
		if got := classByName(c.class).IsBoss(c.params, c.angle); got != c.want {
			t.Errorf("%s params 0x%X angle %v: boss %v, want %v", c.class, c.params, c.angle, got, c.want)
		}
	}
}

// line is an entry in short, for comparing tables.
func line(e Entry) string {
	s := fmt.Sprintf("%s %s %s#%d %s %s 0x%04X %s", e.Stage, roomName(e.Room), e.Tag, e.Index, e.Name, e.Class, e.SetID, e.Key)
	if !e.Checked {
		s += " unchecked"
	}
	if e.Boss {
		s += " boss"
	}
	if e.Shared {
		s += " shared"
	}
	return s
}

// dupLine is a duplicate in short.
func dupLine(d Duplicate) string {
	refs := make([]string, len(d.Entries))
	for i, r := range d.Entries {
		refs[i] = fmt.Sprintf("%s@%d", r, r.Layer)
	}
	s := fmt.Sprintf("%s %s 0x%04X [%s] enemies %d", d.Stage, roomName(d.Room), d.SetID, strings.Join(refs, ", "), d.Enemies)
	for _, f := range []struct {
		on   bool
		name string
	}{{d.LayerOnly, "layer-only"}, {d.SharedKey, "shared"}, {d.Collides, "collides"}} {
		if f.on {
			s += " " + f.name
		}
	}
	return s
}

// fixture is a disc of synthetic stages covering every key state, the
// BOSS rules and each kind of duplicate.
func fixture() []stagetest.Stage {
	a := func(name string, setID uint16, params uint32, angle ...int16) stagetest.Actor {
		e := stagetest.Actor{Name: name, SetID: setID, Params: params, Pos: [3]float32{1.5, -2, 300}}
		copy(e.Angle[:], angle)
		return e
	}
	stag := stagetest.StagChunk(stagetest.StagInfo(6, int(stagedata.StageDungeon), true))
	room := func(no int, chunks ...stagetest.Chunk) stagetest.Room {
		return stagetest.Room{No: no, DZR: stagetest.DZx(chunks...)}
	}
	return []stagetest.Stage{
		{
			Name: "M_Dai",
			DZS:  stagetest.DZx(stag, stagetest.Actors("ACTR", a("Bk", 3, 0))),
			Rooms: []stagetest.Room{
				room(0, stagetest.Actors("ACTR",
					a("Bk", 1, 0x00FF0003, 0x10, 0x4000, 0x25), // the death switch is BK's, no boss
					a("Stal", 2, 0, 0, 0, 5),                   // BOSS: death switch 5
					a("takara", 1, 0),                          // shares Bk's key
					a("mo2", 0xFFFF, 0),                        // no key
					a("keeth", 0x200, 0),                       // past the zone
					a("Nope", 1, 0),                            // not in l_objectName
				)),
				room(1,
					stagetest.Actors(stagetest.LayerTag("ACT", 1), a("Bk", 4, 0)),
					stagetest.Actors(stagetest.LayerTag("ACT", 2), a("Bk", 4, 0)), // layer only
					stagetest.Actors("SCOB", a("Bk", 7, 0)),                       // unchecked, but records
					stagetest.Actors("ACTR", a("takara", 7, 0)),
				),
				room(2, stagetest.Actors("ACTR", a("Bk", 9, 0xA), a("Bk", 9, 0))), // shared and colliding
				room(3, stagetest.Actors("ACTR", a("pow", 6, 0), a("takara", 6, 0))),
				room(4, stagetest.Actors("ACTR", a("takara", 8, 0), a("takara", 8, 0))),
				room(5, stagetest.Actors("ACTR", a("kuro_s", 10, 0), a("item", 10, 0))),
				room(6,
					stagetest.Actors("ACTR", a("takara", 11, 0)),
					stagetest.Actors(stagetest.LayerTag("ACT", 3), a("Bk", 11, 0)),
					stagetest.Actors(stagetest.LayerTag("SCO", 4), a("Bk", 12, 0)),
					stagetest.Actors("TGDR", a("Bk", 12, 0))),
			},
		},
		{
			Name: "sea",
			DZS:  stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(0, int(stagedata.StageSea), false))),
			Rooms: []stagetest.Room{
				room(1, stagetest.Actors("ACTR", a("Bk", 1, 0), a("Ji1", 0, 0), a("SW_HIT0", 0, 0))),
			},
		},
	}
}

func load(t testing.TB, stages []stagetest.Stage) []*stagedata.Stage {
	t.Helper()
	d := new(stagetest.Disc)
	for _, s := range stages {
		d.AddStage(s)
	}
	out, _, err := stagedata.Load(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuild(t *testing.T) {
	tbl := Build(load(t, fixture()), testNames(t))
	var got []string
	for _, e := range tbl.Enemies {
		got = append(got, line(e))
	}
	want := []string{
		"M_Dai stage ACTR#0 Bk BK 0x0003 stage_file",
		"M_Dai r0 ACTR#0 Bk BK 0x0001 keyed",
		"M_Dai r0 ACTR#1 Stal ST 0x0002 keyed boss",
		"M_Dai r0 ACTR#3 mo2 MO2 0xFFFF none",
		"M_Dai r0 ACTR#4 keeth KI 0x0200 out_of_range",
		"M_Dai r1 SCOB#0 Bk BK 0x0007 keyed unchecked",
		"M_Dai r1 ACT1#0 Bk BK 0x0004 keyed",
		"M_Dai r1 ACT2#0 Bk BK 0x0004 keyed",
		"M_Dai r2 ACTR#0 Bk BK 0x0009 keyed boss shared",
		"M_Dai r2 ACTR#1 Bk BK 0x0009 keyed shared",
		"M_Dai r3 ACTR#0 pow PW 0x0006 keyed",
		"M_Dai r5 ACTR#0 kuro_s KS 0x000A keyed",
		"M_Dai r6 TGDR#0 Bk BK 0x000C keyed unchecked shared",
		"M_Dai r6 ACT3#0 Bk BK 0x000B keyed",
		"M_Dai r6 SCO4#0 Bk BK 0x000C keyed unchecked shared",
		"sea r1 ACTR#0 Bk BK 0x0001 keyed",
		"sea r1 ACTR#1 Ji1 NPC_JI1 0x0000 keyed",
	}
	if !slices.Equal(got, want) {
		t.Errorf("enemies:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if e := tbl.Enemies[1]; e.Pos != (Vec{1.5, -2, 300}) || e.Angle != [3]int16{0x10, 0x4000, 0x25} ||
		e.Params != 0x00FF0003 || e.Proc != stagedata.ProcBK || e.Layer != stagedata.AllLayers {
		t.Errorf("entry fields %+v", e)
	}
	if tbl.Stages != 2 || tbl.Unnamed != 1 || tbl.Placed != 26 || tbl.Halting != 1 {
		t.Errorf("stages %d, placed %d, unnamed %d, halting %d", tbl.Stages, tbl.Placed, tbl.Unnamed, tbl.Halting)
	}

	got = nil
	for _, d := range tbl.Duplicates {
		got = append(got, dupLine(d))
	}
	want = []string{
		"M_Dai r0 0x0001 [ACTR#0 Bk (BK)@-1, ACTR#2 takara@-1] enemies 1 collides",
		"M_Dai r1 0x0004 [ACT1#0 Bk (BK)@1, ACT2#0 Bk (BK)@2] enemies 2 layer-only",
		"M_Dai r1 0x0007 [ACTR#0 takara@-1, SCOB#0 Bk (BK)@-1] enemies 1 collides",
		"M_Dai r2 0x0009 [ACTR#0 Bk (BK)@-1, ACTR#1 Bk (BK)@-1] enemies 2 shared collides",
		"M_Dai r3 0x0006 [ACTR#0 pow (PW)@-1, ACTR#1 takara@-1] enemies 1 collides",
		"M_Dai r4 0x0008 [ACTR#0 takara@-1, ACTR#1 takara@-1] enemies 0",
		"M_Dai r5 0x000A [ACTR#0 kuro_s (KS)@-1, ACTR#1 item@-1] enemies 1 collides",
		"M_Dai r6 0x000B [ACTR#0 takara@-1, ACT3#0 Bk (BK)@3] enemies 1 collides",
		"M_Dai r6 0x000C [TGDR#0 Bk (BK)@-1, SCO4#0 Bk (BK)@4] enemies 2 shared",
		"sea r1 0x0000 [ACTR#1 Ji1 (NPC_JI1)@-1, ACTR#2 SW_HIT0@-1] enemies 1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("duplicates:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	whys := map[string]string{
		"r0": "a recorded death of ACTR#0 Bk (BK) (death own) keeps ACTR#2 takara from being created, on every layer",
		"r3": "a death of ACTR#0 pow (PW) (death unknown) keeps ACTR#1 takara from being created, on every layer",
		"r6": "a recorded death of ACT3#0 Bk (BK) (death own) keeps ACTR#0 takara from being created, on layer 3",
	}
	for _, d := range tbl.Duplicates {
		if w, ok := whys[roomName(d.Room)]; ok && d.Collides && d.Why != w {
			t.Errorf("%s why %q, want %q", dupLine(d), d.Why, w)
		}
		if d.Collides != (d.Why != "") {
			t.Errorf("%s: why %q", dupLine(d), d.Why)
		}
	}

	s := tbl.Summary
	if w := (Share{Entries: 17, WithSetID: 16, StageFile: 1, OutOfRange: 1, Keyed: 14, Unchecked: 3, Shared: 4}); s.All != w {
		t.Errorf("all %+v, want %+v", s.All, w)
	}
	// K1 leaves out the two BOSS entries and Orca.
	if w := (Share{Entries: 14, WithSetID: 13, StageFile: 1, OutOfRange: 1, Keyed: 11, Unchecked: 3, Shared: 3}); s.K1 != w {
		t.Errorf("k1 %+v, want %+v", s.K1, w)
	}
	var classes []string
	for _, c := range s.ByClass {
		classes = append(classes, fmt.Sprintf("%s %d/%d", c.Class, c.Keyed, c.Entries))
	}
	if w := []string{"MO2 0/1", "BK 10/11", "ST 1/1", "KS 1/1", "PW 1/1", "KI 0/1", "NPC_JI1 1/1"}; !slices.Equal(classes, w) {
		t.Errorf("by class %v, want %v", classes, w)
	}
	if len(s.ByStage) != 2 || s.ByStage[0].Stage != "M_Dai" || s.ByStage[0].Entries != 15 || s.ByStage[1].Stage != "sea" || s.ByStage[1].Keyed != 2 {
		t.Errorf("by stage %+v", s.ByStage)
	}

	if got := tbl.Lookup("M_Dai", 2, 9); len(got) != 2 || got[0].Index != 0 || got[1].Index != 1 {
		t.Errorf("Lookup(M_Dai, 2, 9) = %v", got)
	}
	if got := tbl.Lookup("M_Dai", 1, 4); len(got) != 2 || got[0].Layer != 1 || got[1].Layer != 2 {
		t.Errorf("Lookup(M_Dai, 1, 4) = %v", got)
	}
	if got := tbl.Lookup("M_Dai", 3, 9); got != nil {
		t.Errorf("Lookup of a missing key = %v", got)
	}

	b, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back Table
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, tbl) {
		t.Errorf("JSON round trip differs:\n%s", b)
	}
}

// TestDuplicate covers duplicate's flags, against a pairwise check, for
// groups of one room.
func TestDuplicate(t *testing.T) {
	for _, c := range []struct {
		name                 string
		entries              []stagedata.Actor
		layerOnly, shared, x bool
	}{
		{"two chests", []stagedata.Actor{at("ACTR", "takara", 1), at("TRES", "takara", 1)}, false, false, false},
		{"chests on two layers", []stagedata.Actor{at("ACT0", "takara", 1), at("TRE1", "takara", 1)}, true, false, false},
		{"Bokoblin and chest", []stagedata.Actor{at("ACTR", "Bk", 1), at("ACTR", "takara", 1)}, false, false, true},
		{"chest then Bokoblin", []stagedata.Actor{at("ACTR", "takara", 1), at("ACTR", "Bk", 1)}, false, false, true},
		{"Bokoblin, unchecked chest", []stagedata.Actor{at("ACTR", "Bk", 1), at("SCOB", "takara", 1)}, false, false, false},
		{"unchecked Bokoblin, chest", []stagedata.Actor{at("SCOB", "Bk", 1), at("ACTR", "takara", 1)}, false, false, true},
		{"two unchecked Bokoblins", []stagedata.Actor{at("SCOB", "Bk", 1), at("DOOR", "Bk", 1)}, false, true, false},
		{"two Bokoblins", []stagedata.Actor{at("ACTR", "Bk", 1), at("ACTR", "Bk", 1)}, false, true, true},
		{"Bokoblins on layers 1, 2", []stagedata.Actor{at("ACT1", "Bk", 1), at("ACT2", "Bk", 1)}, true, false, false},
		{"Bokoblin every layer, one on 2", []stagedata.Actor{at("ACTR", "Bk", 1), at("ACT2", "Bk", 1)}, false, true, true},
		{"Orca and chest", []stagedata.Actor{at("ACTR", "Ji1", 1), at("ACTR", "takara", 1)}, false, false, false},
		{"Kargaroc and Keese", []stagedata.Actor{at("ACTR", "Bb", 1), at("ACT5", "keeth", 1)}, false, true, true},
		{"Poe and crystal", []stagedata.Actor{at("SCO3", "pow", 1), at("ACT3", "SW_HIT0", 1)}, false, false, true},
		{"Poe and crystal apart", []stagedata.Actor{at("SCO3", "pow", 1), at("ACT4", "SW_HIT0", 1)}, true, false, false},
		{"one recorder, checked, alone on its layer", []stagedata.Actor{at("ACT1", "Bk", 1), at("ACT2", "takara", 1), at("SCO1", "takara", 1)}, false, false, false},
		{"three on layer 1", []stagedata.Actor{at("ACT1", "Bk", 1), at("ACT2", "takara", 1), at("SCO1", "Bk", 1)}, false, true, true},
		{"Bdk and its fight", []stagedata.Actor{at("ACTR", "Bdk", 1), at("ACTR", "Bk", 1)}, false, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tbl := Build([]*stagedata.Stage{stage("S", nil, c.entries)}, testNames(t))
			if len(tbl.Duplicates) != 1 {
				t.Fatalf("%d duplicates", len(tbl.Duplicates))
			}
			d := tbl.Duplicates[0]
			if d.LayerOnly != c.layerOnly || d.SharedKey != c.shared || d.Collides != c.x {
				t.Errorf("%s, want layer-only %v shared %v collides %v", dupLine(d), c.layerOnly, c.shared, c.x)
			}
			checkDuplicate(t, tbl, d)
		})
	}
}

// pairwise is what a duplicate's flags must be, from every pair of its
// entries.
func pairwise(refs []Ref) (layerOnly, shared, collides bool) {
	meet := func(a, b Ref) bool {
		return a.Layer == stagedata.AllLayers || b.Layer == stagedata.AllLayers || a.Layer == b.Layer
	}
	layerOnly = true
	for i, a := range refs {
		for j, b := range refs {
			if i == j || !meet(a, b) {
				continue
			}
			layerOnly = false
			shared = shared || (a.Class != "" && b.Class != "")
			collides = collides || (a.Records && b.Checked)
		}
	}
	return layerOnly, shared, collides
}

// checkDuplicate checks d's flags pairwise, and that each enemy entry of
// its key is Shared exactly when it meets another enemy of it.
func checkDuplicate(t *testing.T, tbl *Table, d Duplicate) {
	t.Helper()
	lo, sh, x := pairwise(d.Entries)
	if d.LayerOnly != lo || d.SharedKey != sh || d.Collides != x || d.Collides != (d.Why != "") {
		t.Fatalf("%s (why %q): pairwise layer-only %v shared %v collides %v", dupLine(d), d.Why, lo, sh, x)
	}
	enemies := 0
	for _, r := range d.Entries {
		if r.Class != "" {
			enemies++
		}
	}
	if enemies != d.Enemies || len(d.Entries) < 2 {
		t.Fatalf("%s: %d enemies", dupLine(d), enemies)
	}
	for _, e := range tbl.Lookup(d.Stage, d.Room, d.SetID) {
		var me Ref
		for _, r := range d.Entries {
			if r.Tag == e.Tag && r.Index == e.Index {
				me = r
			}
		}
		meets := false
		for _, r := range d.Entries {
			if r.Class != "" && (r.Tag != e.Tag || r.Index != e.Index) &&
				(r.Layer == stagedata.AllLayers || me.Layer == stagedata.AllLayers || r.Layer == me.Layer) {
				meets = true
			}
		}
		if e.Shared != (meets && e.Key == Keyed) {
			t.Fatalf("%v: shared %v, meets another enemy %v", e, e.Shared, meets)
		}
	}
}

// TestHalting counts the entries isActor halts the game on: a setID
// past the zone in a room's dead-checked chunk, named or not.
func TestHalting(t *testing.T) {
	st := stage("S",
		[]stagedata.Actor{at("ACTR", "Bk", 0x200)}, // the stage's own: room -1, never checked
		[]stagedata.Actor{
			at("ACTR", "Nope", 0x300), // not in l_objectName, but checked first
			at("ACT2", "takara", 0x1FF),
			at("TRE2", "takara", 0x200),
			at("SCOB", "Bk", 0x200), // never checked
			at("ACTR", "keeth", 0xFFFE),
			at("ACTR", "keeth", 0xFFFF),
		})
	tbl := Build([]*stagedata.Stage{st}, testNames(t))
	if tbl.Halting != 3 {
		t.Errorf("halting %d, want 3", tbl.Halting)
	}
	if s := tbl.Summary.All; s.OutOfRange != 2 || s.StageFile != 1 || s.Keyed != 0 || s.WithSetID != 3 {
		t.Errorf("share %+v", s)
	}
	if !strings.Contains(tbl.Report(), "entries of any kind, on which the game halts as the room loads: 3\n") {
		t.Errorf("report:\n%s", tbl.Report())
	}
}

func TestBuildNil(t *testing.T) {
	tbl := Build(nil, testNames(t))
	if tbl.Stages != 0 || tbl.Enemies != nil || tbl.Duplicates != nil || !reflect.DeepEqual(tbl.Summary, Summary{}) {
		t.Errorf("empty build %+v", tbl)
	}
	st := stage("S", []stagedata.Actor{at("ACTR", "Bk", 1)}, []stagedata.Actor{at("ACTR", "Bk", 1), at("ACTR", "Bk", 1)})
	tbl = Build([]*stagedata.Stage{nil, st, {Name: "empty"}}, nil)
	if tbl.Stages != 2 || tbl.Placed != 3 || tbl.Unnamed != 3 || len(tbl.Enemies) != 0 || len(tbl.Duplicates) != 0 {
		t.Errorf("nil names: %+v", tbl)
	}
	if !strings.HasPrefix(tbl.Report(), "Enemy table: 2 stages, 3 entries (3 not in l_objectName), 0 enemy entries of 0 classes\n") {
		t.Errorf("report:\n%s", tbl.Report())
	}
	// Two stages of one name stay apart in the duplicates.
	a, b := stage("S", nil, []stagedata.Actor{at("ACTR", "Bk", 1)}), stage("S", nil, []stagedata.Actor{at("ACTR", "Bk", 1)})
	if tbl := Build([]*stagedata.Stage{a, b}, testNames(t)); len(tbl.Duplicates) != 0 || len(tbl.Summary.ByStage) != 1 {
		t.Errorf("two stages named S: %v, %+v", tbl.Duplicates, tbl.Summary.ByStage)
	}
}

func TestVecJSON(t *testing.T) {
	inf := float32(math.Inf(1))
	for _, c := range []struct {
		v    Vec
		json string
	}{
		{Vec{0, 1.5, -2}, `[0,1.5,-2]`},
		{Vec{float32(math.NaN()), inf, -inf}, `["NaN","+Inf","-Inf"]`},
		{Vec{math.MaxFloat32, math.SmallestNonzeroFloat32, float32(math.Copysign(0, -1))}, `[3.4028235e+38,1e-45,-0]`},
		{Vec{0.1, 1e21, 123456.79}, `[0.1,1e+21,123456.79]`},
	} {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.json {
			t.Errorf("Marshal(%v) = %s, %v; want %s", c.v, b, err, c.json)
			continue
		}
		var back Vec
		if err := json.Unmarshal(b, &back); err != nil {
			t.Errorf("Unmarshal(%s): %v", b, err)
			continue
		}
		for i := range back {
			if math.Float32bits(back[i]) != math.Float32bits(c.v[i]) && !(back[i] != back[i] && c.v[i] != c.v[i]) {
				t.Errorf("%s read back as %v", b, back)
			}
		}
	}
	for _, bad := range []string{`[1,2]`, `[1,2,3,4]`, `["inf",0,0]`, `[1,2,"x"]`, `{}`, `[1e99,0,0]`, `[true,0,0]`} {
		var v Vec
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("Unmarshal(%s) = %v, want an error", bad, v)
		}
	}
}

func TestReport(t *testing.T) {
	r := Build(load(t, fixture()), testNames(t)).Report()
	for _, want := range []string{
		"Enemy table: 2 stages, 26 entries (1 not in l_objectName), 17 enemy entries of 7 classes\n",
		"Enemy entries by chunk: ACT1 1, ACT2 1, ACT3 1, ACTR 11, SCO4 1, SCOB 1, TGDR 1\n",
		"  all enemies                 17  16 (94.1%)      14 (82.4%)           1      1         3      4\n",
		"  K1 classes, not BOSS        14  13 (92.9%)      11 (78.6%)           1      1         3      3\n",
		"  BK      k1    own           11  11 (100.0%)     10 (90.9%)           1      0         3      4\n",
		"  sea                          2  2 (100.0%)      2 (100.0%)           0      0         0      0\n",
		"Duplicate keys (stage, room, setID) over every created entry: 10\n",
		"  9 with an enemy; 2 where two enemies share a layer",
		"  6 that collide in one game",
		"  1 that differ only by layer",
		"  with an enemy:\n    M_Dai r0 0x0001: ",
		"    M_Dai r2 0x0009: ACTR#0 Bk (BK), ACTR#1 Bk (BK) [shared key, collides]\n" +
			"      a recorded death of ACTR#0 Bk (BK) (death own) keeps ACTR#1 Bk (BK) from being created, on every layer\n",
		"    M_Dai r1 0x0004: ACT1#0 Bk (BK), ACT2#0 Bk (BK) [layer only]\n",
		"    sea r1 0x0000: ACTR#1 Ji1 (NPC_JI1), ACTR#2 SW_HIT0\n  without one:\n    M_Dai r4 0x0008: ACTR#0 takara, ACTR#1 takara\n\n",
		"(d_save.cpp:1672-1674, :1686-1688); enemy entries with a setID there: 1\n",
		"  0x0BE BK      Bokoblin                     ENEMY k1    death own     BOSS params  enemyice +0x1264  home moves placed 11\n",
		"  0x0CE KS      Morth                        ENV   k1    death ice     BOSS never   enemyice +0x6A8              placed 1\n",
		"  0x1C6 ATT                                  ENEMY boss  death none    BOSS never                                placed 0\n",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q:\n%s", want, r)
		}
	}

	// A duplicate with more members than a line lists.
	var many []stagedata.Actor
	for range maxRefsPerLine + 3 {
		many = append(many, at("ACTR", "takara", 5))
	}
	r = Build([]*stagedata.Stage{stage("S", nil, many)}, testNames(t)).Report()
	if !strings.Contains(r, "ACTR#7 takara, +3 more\n") {
		t.Errorf("long duplicate:\n%s", r)
	}
}

// TestJSONStable checks that a table with positions JSON has no number
// for still encodes, and reads back to the same encoding.
func TestJSONStable(t *testing.T) {
	st := load(t, fixture())
	nan := float32(math.NaN())
	st[0].Rooms[0].DZR.Actors[0].Pos = [3]float32{nan, float32(math.Inf(-1)), 0}
	tbl := Build(st, testNames(t))
	b1, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back Table
	if err := json.Unmarshal(b1, &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(&back)
	if !bytes.Equal(b1, b2) || !bytes.Contains(b1, []byte(`"pos":["NaN","-Inf",0]`)) {
		t.Errorf("JSON differs after a round trip:\n%s\n%s", b1, b2)
	}
}
