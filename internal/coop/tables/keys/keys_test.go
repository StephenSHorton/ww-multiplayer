package keys

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
)

// testNames is an l_objectName with the door and container names the
// fixtures use (d_stage.cpp:451-480, :483, :519, :647).
func testNames(t testing.TB) *stagedata.ObjectNames {
	t.Helper()
	o, err := stagedata.ParseObjectNames(stagetest.ObjectNameTable(
		stagetest.ObjectName{Name: "door10", Proc: int16(stagedata.ProcDoor10), Subtype: -1},
		stagetest.ObjectName{Name: "keyshut", Proc: int16(stagedata.ProcDoor10), Subtype: -1},
		stagetest.ObjectName{Name: "door12", Proc: int16(stagedata.ProcDoor12), Subtype: 0},
		stagetest.ObjectName{Name: "keyS12", Proc: int16(stagedata.ProcDoor12), Subtype: 1},
		stagetest.ObjectName{Name: "doorKD", Proc: int16(ProcKddoor), Subtype: -1},
		stagetest.ObjectName{Name: "takara", Proc: int16(stagedata.ProcTbox), Subtype: -1},
		stagetest.ObjectName{Name: "item", Proc: int16(stagedata.ProcItem), Subtype: -1},
		stagetest.ObjectName{Name: "kotubo", Proc: int16(itemsrc.ProcTsubo), Subtype: 0},
	))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

type a = stagetest.Actor

// door is a door entry of type typ with switch sw between rooms front and
// back.
func door(name string, typ, sw uint32, front, back int) a {
	return a{Name: name, Params: typ<<8 | sw, Angle: [3]int16{int16(back<<6 | front), 0, 0}}
}

func chest(bit uint32) a {
	return a{Name: "takara", Params: bit << 7, Angle: [3]int16{0, 0, int16(itemsrc.ItemSmallKey) << 8}}
}

func fieldKey(bit uint32) a { return a{Name: "item", Params: bit<<8 | uint32(itemsrc.ItemSmallKey)} }

func potKey(bit uint32) a { return a{Name: "kotubo", Params: bit<<16 | uint32(itemsrc.ItemSmallKey)} }

func dungeon(name string, table int, doors []a, rooms ...stagetest.Room) stagetest.Stage {
	return stagetest.Stage{
		Name: name,
		DZS: stagetest.DZx(
			stagetest.StagChunk(stagetest.StagInfo(table, int(stagedata.StageDungeon), true)),
			stagetest.Actors("TGDR", doors...),
		),
		Rooms: rooms,
	}
}

func room(no int, chunks ...stagetest.Chunk) stagetest.Room {
	return stagetest.Room{No: no, DZR: stagetest.DZx(chunks...)}
}

// vanilla is a disc whose key tables pass: the vanilla counts, every
// source kind and door class, and the entries that must not count (boss
// doors, plain doors, a non-key chest, keys whose drop is random).
func vanilla() []stagetest.Stage {
	return []stagetest.Stage{
		{Name: "sea", DZS: stagetest.DZx(
			stagetest.StagChunk(stagetest.StagInfo(0, int(stagedata.StageSea), false)),
			stagetest.Actors("TGDR", door("door10", 0, 0x05, 1, 2), door("door12", 0, 0xFF, 1, 2)),
		)},
		dungeon("M_NewD2", TableDRC,
			[]a{
				door("door10", 4, 0x10, 0, 1),
				door("keyshut", 5, 0x11, 1, 2),
				door("keyS12", 1, 0x12, 2, 3),
				door("doorKD", 2, 0x13, 3, 4),
				door("door10", 1, 0x14, 4, 5), // the boss door
				door("door12", 3, 0x15, 4, 5), // a door12 boss door
				door("door10", 0, 0x16, 4, 5), // plain doors
				door("keyS12", 2, 0x17, 4, 5),
				door("doorKD", 1, 0x18, 4, 5),
			},
			room(0, stagetest.Actors("ACTR", chest(1), potKey(0x0B), a{Name: "takara", Params: 2 << 7, Angle: [3]int16{0, 0, 0x0100}})),
			room(1, stagetest.Actors("ACTR", fieldKey(0x05), a{Name: "kotubo", Params: 0x06<<16 | 0x25})),
			room(2, stagetest.Actors("ACT1", chest(3)), stagetest.Actors("ACT2", chest(3))),
		),
		dungeon("M_DragB", TableDRC, nil, room(0, stagetest.Actors("ACTR", a{Name: "takara", Params: 9 << 7, Angle: [3]int16{0, 0, 0x0800}}))),
		dungeon("kindan", TableFW, []a{door("door10", 4, 0x01, 1, 2)}, room(1, stagetest.Actors("ACTR", chest(4)))),
		dungeon("Siren", TableTotG, []a{door("keyS12", 1, 0x02, 1, 2), door("keyS12", 1, 0x03, 2, 3)},
			room(1, stagetest.Actors("ACTR", chest(1), fieldKey(0x10)))),
		dungeon("M_Dai", TableET, []a{door("door10", 5, 0x04, 1, 2), door("door10", 5, 0x05, 2, 3), door("door10", 5, 0x06, 3, 4)},
			room(1, stagetest.Actors("ACTR", chest(1), chest(2), chest(3)))),
		// WT's doors are on two layers each; one key is in the stage file.
		{
			Name: "kaze",
			DZS: stagetest.DZx(
				stagetest.StagChunk(stagetest.StagInfo(TableWT, int(stagedata.StageDungeon), true)),
				stagetest.Actors("SCO1", door("doorKD", 2, 0x07, 1, 2), door("doorKD", 2, 0x08, 2, 3)),
				stagetest.Actors("SCO2", door("doorKD", 2, 0x07, 1, 2), door("doorKD", 2, 0x08, 2, 3)),
				stagetest.Actors("ACTR", fieldKey(0x11)),
			),
			Rooms: []stagetest.Room{room(1, stagetest.Actors("ACTR", chest(5)))},
		},
	}
}

func load(t testing.TB, stages []stagetest.Stage) []*stagedata.Stage {
	t.Helper()
	d := new(stagetest.Disc)
	for _, s := range stages {
		d.AddStage(s)
	}
	out, err := stagedata.Load(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildVanilla(t *testing.T) {
	r := Build(load(t, vanilla()), testNames(t))
	if !r.Pass {
		t.Fatalf("vanilla fixture fails:\n%s", r.Report())
	}
	var nums []int
	for _, tb := range r.Tables {
		nums = append(nums, tb.SaveTable)
	}
	if want := []int{3, 4, 5, 6, 7}; !reflect.DeepEqual(nums, want) {
		t.Fatalf("tables %v, want %v", nums, want)
	}
	drc, ok := r.Table(TableDRC)
	if !ok || drc.Dungeon != "DRC" || drc.Expected != 4 || !reflect.DeepEqual(drc.Stages, []string{"M_NewD2", "M_DragB"}) {
		t.Fatalf("DRC = %+v", drc)
	}
	var kinds []itemsrc.Kind
	var flags []string
	for _, s := range drc.Sources {
		kinds = append(kinds, s.Kind)
		flags = append(flags, s.Flag.String())
	}
	if want := []itemsrc.Kind{itemsrc.Chest, itemsrc.Pot, itemsrc.FieldItem, itemsrc.Chest}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("DRC source kinds %v, want %v", kinds, want)
	}
	if want := []string{"tbox 3:1", "item 3:0x0B", "item 3:0x05", "tbox 3:3"}; !reflect.DeepEqual(flags, want) {
		t.Errorf("DRC source flags %v, want %v", flags, want)
	}
	if at := drc.Sources[3].At; len(at) != 2 || at[0].Layer != 1 || at[1].Layer != 2 || at[0].Room != 2 {
		t.Errorf("layered chest: %+v", at)
	}
	type d struct {
		class  Class
		typ    int
		sw     int
		rooms  [2]int
		stage  string
		layers int
	}
	var got []d
	for _, door := range drc.Doors {
		e := door.At[0]
		got = append(got, d{e.Class, e.Type, door.Switch, [2]int{e.FrontRoom, e.BackRoom}, e.Stage, len(door.At)})
		if !door.Saved || e.Room != -1 || e.Tag != "TGDR" {
			t.Errorf("door %+v", door)
		}
	}
	want := []d{
		{Door10, 4, 0x10, [2]int{0, 1}, "M_NewD2", 1},
		{Door10, 5, 0x11, [2]int{1, 2}, "M_NewD2", 1},
		{Door12, 1, 0x12, [2]int{2, 3}, "M_NewD2", 1},
		{Kddoor, 2, 0x13, [2]int{3, 4}, "M_NewD2", 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DRC doors\n%+v, want\n%+v", got, want)
	}
	wt, _ := r.Table(TableWT)
	if len(wt.Doors) != 2 || len(wt.Doors[0].At) != 2 || wt.Doors[0].At[1].Layer != 2 || wt.Sources[0].At[0].Room != -1 {
		t.Errorf("WT = %+v", wt)
	}
	rep := r.Report()
	for _, s := range []string{
		"small keys: PASS (5 of 5 save tables pass)",
		"save table 3 (DRC, Dragon Roost Cavern): PASS, 4 source(s), 4 door(s), vanilla 4",
		"  stages: M_NewD2 M_DragB",
		"  source 1: chest, tbox 3:1",
		`    M_NewD2 room 0, all layers, ACTR#0 "takara" setID 0x0000`,
		"  door 4: switch 0x13\n",
		`    M_NewD2 stage file, all layers, TGDR#3 "doorKD" setID 0x0000, kddoor type 2, rooms 3/4`,
		"save table 7 (WT, Wind Temple): PASS",
	} {
		if !strings.Contains(rep, s) {
			t.Errorf("report lacks %q:\n%s", s, rep)
		}
	}
	if strings.Contains(rep, "problem") {
		t.Errorf("report has problems:\n%s", rep)
	}

	// The result survives JSON, for the cache.
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, r) {
		t.Errorf("JSON round trip changed the result:\n%s", b)
	}
	for _, s := range []string{`"save_table":3`, `"dungeon":"DRC"`, `"switch":16`, `"front_room":0`, `"sources":[`, `"pass":true`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("JSON lacks %s", s)
		}
	}
}

func TestBuildFailures(t *testing.T) {
	cases := []struct {
		name  string
		edit  func([]stagetest.Stage) []stagetest.Stage
		table int      // the table that fails
		want  []string // in its problems
	}{
		{
			name: "extra source",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, dungeon("M_Dra09", TableDRC, nil, room(0, stagetest.Actors("ACTR", chest(20)))))
			},
			table: TableDRC,
			want:  []string{"5 small-key source(s) but 4 key door(s)", "vanilla has 4 of each; the stage files have 5 source(s), 4 door(s)"},
		},
		{
			name: "extra door",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, dungeon("kinBOSS", TableFW, []a{door("door12", 1, 0x30, 0, 1)}))
			},
			table: TableFW,
			want:  []string{"1 small-key source(s) but 2 key door(s)"},
		},
		{
			name: "missing dungeon",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return slicesDelete(s, "Siren")
			},
			table: TableTotG,
			want:  []string{"vanilla has 2 of each; the stage files have 0 source(s), 0 door(s)"},
		},
		{
			name: "key door outside the dungeon tables",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, dungeon("Pjavdou", 0xB, []a{door("keyshut", 4, 0x01, 0, 1)}, room(0, stagetest.Actors("ACTR", chest(1)))))
			},
			table: 0xB,
			want:  []string{"1 key door(s) outside the dungeon tables 3-7", "vanilla has 0 of each; the stage files have 1 source(s), 1 door(s)"},
		},
		{
			name: "key outside the dungeon tables",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, dungeon("sea2", TableDRC-2, nil, room(0, stagetest.Actors("ACTR", fieldKey(1)))))
			},
			table: 1,
			want:  []string{"1 small-key source(s) but 0 key door(s)"},
		},
		{
			name: "unsaved source",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0x01, 1, 2)}, room(1, stagetest.Actors("ACTR", fieldKey(0x7F))))
				return s
			},
			table: TableFW,
			want:  []string{"source 1: unsaved: nothing saved records the pickup"},
		},
		{
			name: "zone-bit source",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0x01, 1, 2)}, room(1, stagetest.Actors("ACTR", fieldKey(0x42))))
				return s
			},
			table: TableFW,
			want:  []string{"source 1: zone item room 1:0x42 (unsaved): nothing saved"},
		},
		{
			name: "source on a bit the game halts on",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0x01, 1, 2)}, room(1, stagetest.Actors("ACTR", fieldKey(0x55))))
				return s
			},
			table: TableFW,
			want:  []string{"source 1: bad item bit 0x55 (halts): the game halts on that save bit"},
		},
		{
			name: "sea2 chest",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				c := chest(4)
				c.Params |= 7
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0x01, 1, 2)}, room(1, stagetest.Actors("ACTR", c)))
				return s
			},
			table: TableFW,
			want:  []string{"source 1: the chest records in save table 1, the key counts in 4"},
		},
		{
			name: "door without a switch",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0xFF, 1, 2)}, room(1, stagetest.Actors("ACTR", chest(4))))
				return s
			},
			table: TableFW,
			want:  []string{"door 1: no switch (0xFF)"},
		},
		{
			name: "door on a dungeon switch",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0x90, 1, 2)}, room(1, stagetest.Actors("ACTR", chest(4))))
				return s
			},
			table: TableFW,
			want:  []string{"door 1: switch 0x90 is a dungeon switch"},
		},
		{
			name: "door on a zone switch",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0xC5, 1, 2)}, room(1, stagetest.Actors("ACTR", chest(4))))
				return s
			},
			table: TableFW,
			want:  []string{"door 1: switch 0xC5 is a zone switch"},
		},
		{
			name: "two unsaved doors are two doors",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s[3] = dungeon("kindan", TableFW, []a{door("door10", 4, 0xFF, 1, 2), door("door10", 4, 0xFF, 1, 2)}, room(1, stagetest.Actors("ACTR", chest(4))))
				return s
			},
			table: TableFW,
			want:  []string{"1 small-key source(s) but 2 key door(s)", "door 2: no switch"},
		},
	}
	names := testNames(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Build(load(t, c.edit(vanilla())), names)
			if r.Pass {
				t.Fatalf("passes:\n%s", r.Report())
			}
			for _, tb := range r.Tables {
				if tb.SaveTable == c.table {
					continue
				}
				if !tb.Pass {
					t.Errorf("table %d fails too: %v", tb.SaveTable, tb.Problems)
				}
			}
			tb, ok := r.Table(c.table)
			if !ok {
				t.Fatalf("no table %d:\n%s", c.table, r.Report())
			}
			if tb.Pass || len(tb.Problems) == 0 {
				t.Fatalf("table %d passes", c.table)
			}
			all := strings.Join(tb.Problems, "\n")
			for _, w := range c.want {
				if !strings.Contains(all, w) {
					t.Errorf("problems lack %q:\n%s", w, all)
				}
			}
			rep := r.Report()
			if !strings.Contains(rep, "small keys: FAIL") || !strings.Contains(rep, "  problem: ") {
				t.Errorf("report:\n%s", rep)
			}
		})
	}
}

func slicesDelete(s []stagetest.Stage, name string) []stagetest.Stage {
	var out []stagetest.Stage
	for _, st := range s {
		if st.Name != name {
			out = append(out, st)
		}
	}
	return out
}

// The same switch in two stages of one table is one door: both stages
// read the table's switch.
func TestDoorSharedAcrossStages(t *testing.T) {
	s := vanilla()
	s = append(s, dungeon("kinBOSS", TableFW, []a{door("keyshut", 5, 0x01, 0, 1)}))
	r := Build(load(t, s), testNames(t))
	fw, _ := r.Table(TableFW)
	if !fw.Pass || len(fw.Doors) != 1 || len(fw.Doors[0].At) != 2 || fw.Doors[0].At[1].Stage != "kinBOSS" {
		t.Errorf("FW = %+v", fw)
	}
}

func TestBuildDegenerate(t *testing.T) {
	stages := load(t, vanilla())
	for name, r := range map[string]*Result{
		"no stages": Build(nil, testNames(t)),
		"nil stage": Build([]*stagedata.Stage{nil}, testNames(t)),
		"no names":  Build(stages, nil),
	} {
		if r.Pass || len(r.Tables) != len(Dungeons) {
			t.Errorf("%s: %+v", name, r)
		}
		for i, tb := range r.Tables {
			if tb.SaveTable != Dungeons[i].Table || len(tb.Sources) != 0 || len(tb.Doors) != 0 || tb.Pass {
				t.Errorf("%s: table %+v", name, tb)
			}
		}
		if rep := r.Report(); !strings.Contains(rep, "small keys: FAIL (0 of 5 save tables pass)") {
			t.Errorf("%s: report\n%s", name, rep)
		}
	}
	if r := Build(stages, nil); !reflect.DeepEqual(r.Tables[0].Stages, []string{"M_NewD2", "M_DragB"}) {
		t.Errorf("stages without names: %v", r.Tables[0].Stages)
	}
	if _, ok := Build(nil, nil).Table(0); ok {
		t.Error("Table(0) found")
	}
}

func TestDungeons(t *testing.T) {
	total := 0
	for i, d := range Dungeons {
		if d.Table != TableDRC+i || d.Keys <= 0 || d.Short == "" || d.Name == "" {
			t.Errorf("%+v", d)
		}
		total += d.Keys
	}
	if total != 12 {
		t.Errorf("%d vanilla keys, want 12 (§6.4)", total)
	}
}
