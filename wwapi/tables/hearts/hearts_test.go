package hearts

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
)

// testNames is an l_objectName with the names the fixtures use
// (d_stage.cpp; the procs' own lines are cited where they are defined).
func testNames(t testing.TB) *stage.ObjectNames {
	t.Helper()
	o, err := stage.ParseObjectNames(stagetest.ObjectNameTable(
		stagetest.ObjectName{Name: "takara", Proc: int16(stage.ProcTbox), Subtype: -1},
		stagetest.ObjectName{Name: "item", Proc: int16(stage.ProcItem), Subtype: -1},
		stagetest.ObjectName{Name: "Ritem", Proc: int16(itemsrc.ProcRaceItem), Subtype: -1},
		stagetest.ObjectName{Name: "kotubo", Proc: int16(itemsrc.ProcTsubo), Subtype: 0},
		stagetest.ObjectName{Name: "Ktarur", Proc: int16(itemsrc.ProcBarrel2), Subtype: -1},
		stagetest.ObjectName{Name: "Salvage", Proc: int16(itemsrc.ProcSalvage), Subtype: -1},
		stagetest.ObjectName{Name: "Btd", Proc: int16(stage.ProcBTD), Subtype: -1},
		stagetest.ObjectName{Name: "Bkm", Proc: int16(stage.ProcBMD), Subtype: -1},
		stagetest.ObjectName{Name: "Bst", Proc: int16(stage.ProcBST), Subtype: -1},
		stagetest.ObjectName{Name: "big_pow", Proc: int16(stage.ProcBPW), Subtype: -1},
		stagetest.ObjectName{Name: "Bwd", Proc: int16(stage.ProcBWD), Subtype: -1},
		stagetest.ObjectName{Name: "Bdk", Proc: int16(stage.ProcBDK), Subtype: -1},
		stagetest.ObjectName{Name: "Bitem", Proc: int16(stage.ProcBossItem), Subtype: -1},
		stagetest.ObjectName{Name: "TagKb", Proc: int16(itemsrc.ProcTagKbItem), Subtype: -1},
	))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

type a = stagetest.Actor

func chest(bit uint32, item uint8) a {
	return a{Name: "takara", Params: bit << 7, Angle: [3]int16{0, 0, int16(uint16(item) << 8)}}
}

func fieldItem(bit uint32, item uint8) a { return a{Name: "item", Params: bit<<8 | uint32(item)} }

func boss(name string) a { return a{Name: name} }

func bitem(table uint32) a { return a{Name: "Bitem", Params: table} }

func mkStage(name string, table int, dzs []a, rooms ...stagetest.Room) stagetest.Stage {
	chunks := []stagetest.Chunk{stagetest.StagChunk(stagetest.StagInfo(table, int(stage.StageDungeon), false))}
	if len(dzs) > 0 {
		chunks = append(chunks, stagetest.Actors("ACTR", dzs...))
	}
	return stagetest.Stage{Name: name, DZS: stagetest.DZx(chunks...), Rooms: rooms}
}

func room(no int, chunks ...stagetest.Chunk) stagetest.Room {
	return stagetest.Room{No: no, DZR: stagetest.DZx(chunks...)}
}

// vanilla is a disc that, with the Catalogue, has the vanilla totals:
// pieces of every stage kind and the six containers, from the five
// bosses that drop one and Forsaken Fortress's Bitem on the sea, plus
// entries that must not count: among them Ganon's Tower's four refights
// (Xboss0-3, save table 8), whose bosses warp out instead.
func vanilla() []stagetest.Stage {
	hp := itemsrc.ItemHeartPiece
	first := []a{
		chest(1, hp),
		chest(2, itemsrc.ItemHeartPieceAlt),
		fieldItem(0x10, hp),
		{Name: "Ritem", Params: 0x11<<8 | uint32(hp)},
		{Name: "kotubo", Params: 0x12<<16 | uint32(hp)},
		{Name: "Ktarur", Params: 0x13<<16 | uint32(hp)},
		{Name: "Salvage", Params: 0<<28 | 5<<20 | uint32(hp)<<4}, // chart 5, one point
		{Name: "Salvage", Params: 0<<28 | 5<<20 | uint32(hp)<<4}, // and another
		{Name: "Salvage", Params: 3<<28 | 2<<20 | uint32(hp)<<4}, // an ocean bit
		{Name: "kotubo", Params: 0x14<<16 | 0x21},                // a random drop
		chest(25, 0x01),                     // not a piece
		bitem(tableFF),                      // Forsaken Fortress's container
		{Name: "TagKb", Params: 0xFFFF0207}, // a pig's dig spot, item bit 2
		{Name: "TagKb", Params: 0xE101FFFF}, // an enemy kind, no item
	}
	var rest []a
	for bit := uint32(3); len(rest) < ExpectedPieces-len(Catalogue)-9; bit++ {
		rest = append(rest, chest(bit, hp))
	}
	return []stagetest.Stage{
		mkStage("sea", 0, nil, room(1, stagetest.Actors("ACTR", first...)), room(2, stagetest.Actors("ACTR", rest...))),
		mkStage("M2tower", tableFF, []a{boss("Bdk")}),
		mkStage("M_DragB", 3, []a{boss("Btd")}, room(0, stagetest.Actors("ACTR", bitem(3)))),
		mkStage("kinBOSS", 4, []a{boss("Bkm")}),
		mkStage("SirenB", 5, []a{boss("Bst")}),
		mkStage("M_DaiB", 6, nil, room(0, stagetest.Actors("ACT1", boss("big_pow")), stagetest.Actors("ACT2", boss("big_pow")))),
		mkStage("kazeB", 7, []a{boss("Bwd")}),
		mkStage("Xboss0", tableGT, []a{boss("Btd")}),
		mkStage("Xboss1", tableGT, []a{boss("Bkm")}),
		mkStage("Xboss2", tableGT, nil, room(0, stagetest.Actors("ACTR", boss("big_pow")))),
		mkStage("Xboss3", tableGT, []a{boss("Bwd")}),
		// The two unreachable heart chests (unreachable), left out.
		mkStage("Cave06", 12, []a{chest(18, hp)}),
		mkStage("I_SubAN", 12, []a{chest(7, hp)}),
	}
}

// xboss0 gives vanilla's Xboss0, Gohma's refight, the rooms rs.
func xboss0(s []stagetest.Stage, rs ...stagetest.Room) []stagetest.Stage {
	for i := range s {
		if s[i].Name == "Xboss0" {
			s[i] = mkStage("Xboss0", tableGT, []a{boss("Btd")}, rs...)
		}
	}
	return s
}

// tableGT is Ganon's Tower's save table, STAGE_GT (d_save.h:897).
const tableGT = 0x8

func load(t testing.TB, stages []stagetest.Stage) []*stage.Stage {
	t.Helper()
	d := new(stagetest.Disc)
	for _, s := range stages {
		d.AddStage(s)
	}
	out, _, err := stage.Load(d)
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
	seen := 0
	for _, rw := range Catalogue {
		if rw.ItemSeen {
			seen++
		}
	}
	want := Totals{
		Pieces: 44, StagePieces: 44 - len(Catalogue), RewardPieces: seen, UnverifiedPieces: len(Catalogue) - seen,
		PiecesByKind: map[string]int{
			"chest": 2 + 44 - len(Catalogue) - 9, "dig": 1, "item": 1, "race_item": 1, "pot": 1, "barrel": 1, "salvage": 2,
			"npc": 7, "letter": 3, "shop": 1, "auction": 1, "minigame": 3, "drop": 1,
		},
		Containers:         6,
		ContainersByKind:   map[string]int{"boss": 5, "boss_item": 1},
		ExpectedPieces:     44,
		ExpectedContainers: 6,
	}
	if !reflect.DeepEqual(r.Totals, want) {
		t.Errorf("totals\n%+v, want\n%+v", r.Totals, want)
	}
	var flags []string
	for _, s := range r.Pieces[:9] {
		flags = append(flags, string(s.Kind)+" "+s.Flag.String())
	}
	if w := []string{
		"chest tbox 0:1", "chest tbox 0:2", "item item 0:0x10", "race_item item 0:0x11", "pot item 0:0x12",
		"barrel item 0:0x13", "salvage chart 5", "salvage ocean room 1:2", "dig item 0:0x02",
	}; !reflect.DeepEqual(flags, w) {
		t.Errorf("piece flags\n%q, want\n%q", flags, w)
	}
	if at := r.Pieces[6].At; len(at) != 2 {
		t.Errorf("chart 5's points: %+v", at)
	}
	if r.Pieces[1].Item != itemsrc.ItemHeartPieceAlt {
		t.Errorf("alt piece: %+v", r.Pieces[1])
	}
	var lives []string
	for _, c := range r.Containers {
		lives = append(lives, string(c.Kind)+" "+c.Flag.String()+" "+c.At[0].Stage)
		if c.Item != itemsrc.ItemHeartContainer {
			t.Errorf("container %+v", c)
		}
	}
	if w := []string{
		"boss_item stage life 2 sea", "boss stage life 3 M_DragB", "boss stage life 4 kinBOSS",
		"boss stage life 5 SirenB", "boss stage life 6 M_DaiB", "boss stage life 7 kazeB",
	}; !reflect.DeepEqual(lives, w) {
		t.Errorf("containers\n%q, want\n%q", lives, w)
	}
	if c := r.Containers[1]; len(c.At) != 2 || c.At[1].Name != "Bitem" {
		t.Errorf("Gohma's container and its Bitem: %+v", c.At)
	}
	if c := r.Containers[4]; len(c.At) != 2 || c.At[0].Layer != 1 || c.At[1].Layer != 2 {
		t.Errorf("Jalhalla on two layers: %+v", c.At)
	}
	if !reflect.DeepEqual(r.Rewards, Catalogue) {
		t.Error("rewards are not the catalogue")
	}
	if got := r.Unreachable; len(got) != 2 || got[0].Name != "Cave06" || got[1].Name != "I_SubAN" ||
		got[0].Why != unreachable["Cave06"] || got[1].Why != unreachable["I_SubAN"] {
		t.Errorf("unreachable %+v, want Cave06 and I_SubAN", got)
	}
	for _, p := range r.Pieces {
		for _, w := range p.At {
			if _, ok := unreachable[w.Stage]; ok {
				t.Errorf("piece in an unreachable stage: %+v", p)
			}
		}
	}

	rep := r.Report()
	for _, s := range []string{
		"hearts: PASS\n",
		"  heart pieces: 44, vanilla 44: 28 in stage files (barrel 1, chest 21, dig 1, item 1, pot 1, race_item 1, salvage 2), 11 rewards (auction 1, letter 3, npc 6, shop 1), 5 unverified (drop 1, minigame 3, npc 1)\n",
		"  heart containers: 6, vanilla 6 (boss 5, boss_item 1)\n  left out Cave06: a heart chest (save table 12, tbox 18)",
		"  left out I_SubAN: a heart chest (save table 12, tbox 7)",
		"  7: salvage, item 0x07, chart 5\n    sea room 1, all layers, ACTR#6 \"Salvage\" setID 0x0000\n    sea room 1, all layers, ACTR#7",
		"  2: boss, item 0x08, stage life 3\n    M_DragB stage file, all layers, ACTR#0 \"Btd\" setID 0x0000\n    M_DragB room 0",
		"  ivan: Ivan (Killer Bees), Windfall Island, npc, item 0x07\n    site: daNpc_Mk_c, d_a_npc_mk.cpp:820-825\n    record: event bit 0x1340",
		"  bird-man-contest: Willi and Obli (Bird-Man Contest), Flight Control Platform, minigame, item 0x07 (unverified)\n",
	} {
		if !strings.Contains(rep, s) {
			t.Errorf("report lacks %q:\n%s", s, rep)
		}
	}
	if strings.Contains(rep, "problem") {
		t.Errorf("report has problems:\n%s", rep)
	}

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
	for _, s := range []string{`"pieces":[`, `"containers":[`, `"rewards":[`, `"stage_pieces":28`, `"expected_pieces":44`, `"item_seen":true`, `"unreachable":[{"name":"Cave06"`, `"pass":true`} {
		if !strings.Contains(string(b), s) {
			t.Errorf("JSON lacks %s", s)
		}
	}
}

func TestBuildFailures(t *testing.T) {
	cases := []struct {
		name string
		edit func([]stagetest.Stage) []stagetest.Stage
		want []string
	}{
		{
			name: "extra piece",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("Omori", 0xB, nil, room(0, stagetest.Actors("ACTR", fieldItem(1, itemsrc.ItemHeartPiece)))))
			},
			want: []string{"45 heart pieces (29 in stage files, 11 rewards, 5 unverified rewards); vanilla has 44"},
		},
		{
			// The refight rule goes by the stage's name, as the bosses'
			// test does, not by Ganon's Tower's table.
			name: "a boss on Ganon's Tower's table, not in an X stage",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("GanonX", tableGT, []a{boss("Btd")}))
			},
			want: []string{"7 heart containers; vanilla has 6"},
		},
		{
			// Only the bosses of an X stage are left out (refight): a
			// Bitem there still counts.
			name: "a Bitem in an X stage",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return xboss0(s, room(0, stagetest.Actors("ACTR", bitem(tableGT))))
			},
			want: []string{"7 heart containers; vanilla has 6"},
		},
		{
			name: "a piece in an X stage",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return xboss0(s, room(0, stagetest.Actors("ACTR", fieldItem(1, itemsrc.ItemHeartPiece))))
			},
			want: []string{"45 heart pieces (29 in stage files, 11 rewards, 5 unverified rewards); vanilla has 44"},
		},
		{
			name: "missing boss",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return slices.DeleteFunc(s, func(st stagetest.Stage) bool { return st.Name == "kazeB" })
			},
			want: []string{"5 heart containers; vanilla has 6"},
		},
		{
			name: "unsaved piece",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("Omori", 0xB, nil, room(0, stagetest.Actors("ACTR", fieldItem(0x7F, itemsrc.ItemHeartPiece)))))
			},
			want: []string{"piece 29: unsaved: nothing saved records the pickup"},
		},
		{
			name: "zone-bit piece",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("Omori", 0xB, nil, room(4, stagetest.Actors("ACTR", fieldItem(0x41, itemsrc.ItemHeartPiece)))))
			},
			want: []string{"piece 29: zone item room 4:0x41 (unsaved): nothing saved"},
		},
		{
			// d_save.cpp:1164-1173 asserts on grid 50.
			name: "salvaged piece on an ocean bit the game halts on",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("Omori", 0, nil, room(50, stagetest.Actors("ACTR",
					a{Name: "Salvage", Params: 3<<28 | 4<<20 | uint32(itemsrc.ItemHeartPiece)<<4}))))
			},
			want: []string{"piece 29: ocean grid 50:4 (halts): the game halts on that save bit"},
		},
		{
			name: "container on a bit the game halts on",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("M_Dra09", 3, nil, room(1, stagetest.Actors("ACTR", fieldItem(0x55, itemsrc.ItemHeartContainer)))))
			},
			want: []string{`M_Dra09 room 1, all layers, ACTR#0 "item" setID 0x0000: container placement: the game halts on its save bit, bad item bit 0x55 (halts)`},
		},
		{
			name: "a placed container and a boss share STAGE_LIFE",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("M_Dra09", 3, nil, room(1, stagetest.Actors("ACTR", fieldItem(0x20, itemsrc.ItemHeartContainer)))))
			},
			want: []string{"container 2: 3 placements of 2 kinds (boss, item) all set stage life 3, which counts once"},
		},
		{
			name: "a container chest on the sea is Forsaken Fortress's",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				s = append(s, mkStage("Pfall", 0, nil, room(9, stagetest.Actors("ACTR", chest(4, itemsrc.ItemHeartContainer)))))
				return append(s, mkStage("M2tow2", tableFF, nil, room(0, stagetest.Actors("ACTR", fieldItem(0x7F, itemsrc.ItemHeartContainer)))))
			},
			want: []string{"container 1: 3 placements of 2 kinds (chest, item) all set stage life 2"},
		},
		{
			name: "Bitem for another table",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("M_Dra09", 3, []a{bitem(5)}))
			},
			want: []string{`M_Dra09 stage file, all layers, ACTR#0 "Bitem" setID 0x0000: Bitem checks save table 5's STAGE_LIFE, but its container sets table 3's`},
		},
		{
			name: "Bitem past the last table",
			edit: func(s []stagetest.Stage) []stagetest.Stage {
				return append(s, mkStage("M_Dra09", 3, []a{bitem(0x20)}))
			},
			want: []string{"Bitem checks save table 32, past the last (0xF)"},
		},
	}
	names := testNames(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Build(load(t, c.edit(vanilla())), names)
			if r.Pass {
				t.Fatalf("passes:\n%s", r.Report())
			}
			all := strings.Join(r.Problems, "\n")
			for _, w := range c.want {
				if !strings.Contains(all, w) {
					t.Errorf("problems lack %q:\n%s", w, all)
				}
			}
			if rep := r.Report(); !strings.HasPrefix(rep, "hearts: FAIL\n") || !strings.Contains(rep, "\nproblem: ") {
				t.Errorf("report:\n%s", rep)
			}
		})
	}
}

func TestBuildDegenerate(t *testing.T) {
	stages := load(t, vanilla())
	for name, r := range map[string]*Result{
		"no stages": Build(nil, testNames(t)),
		"nil stage": Build([]*stage.Stage{nil}, testNames(t)),
		"no names":  Build(stages, nil),
	} {
		if r.Pass || len(r.Pieces) != 0 || len(r.Containers) != 0 || r.Totals.Pieces != len(Catalogue) {
			t.Errorf("%s: %+v", name, r)
		}
		if rep := r.Report(); !strings.Contains(rep, "heart pieces in stage files:\n  (none)\n") {
			t.Errorf("%s: report\n%s", name, rep)
		}
	}
}

func TestCatalogue(t *testing.T) {
	ids := make(map[string]bool)
	for _, rw := range Catalogue {
		if ids[rw.ID] || rw.ID == "" || rw.Giver == "" || rw.Where == "" || rw.Site == "" ||
			(rw.Item != itemsrc.ItemHeartPiece && rw.Item != itemsrc.ItemHeartPieceAlt) {
			t.Errorf("%+v", rw)
		}
		ids[rw.ID] = true
		switch rw.Kind {
		case NPC, Minigame, Letter, Shop, Auction, Drop:
		default:
			t.Errorf("%s: kind %q", rw.ID, rw.Kind)
		}
		// Every site cites a file:line, or a file when nothing in it is
		// decompiled.
		if !strings.Contains(rw.Site, ".cpp") {
			t.Errorf("%s: site %q cites no file", rw.ID, rw.Site)
		}
		if rw.ItemSeen && !strings.Contains(rw.Site, ".cpp:") {
			t.Errorf("%s: seen item without a line", rw.ID)
		}
	}
	if lifeTable(0) != tableFF || lifeTable(1) != 1 || lifeTable(3) != 3 {
		t.Error("lifeTable")
	}
}
