package switches

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// Fuzz targets for Build, in the style of internal/network/fuzz_test.go:
// one over whole stage files (the bytes stage.ParseDZx reads from an
// ISO), one over a single entry's params as every proc the table reads
// decodes them. The seed corpora are built in code (no game data) and run
// as ordinary tests; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzBuild -fuzztime=30s ./internal/coop/tables/switches/

// maxUsesPerEntry bounds what one entry adds to a table: an AND with
// MaxANDInputs inputs, each also cleared (andsw0 behaviour 2), plus a
// few more.
const maxUsesPerEntry = 2*MaxANDInputs + 8

// checkTable checks what Build must hold for any input: sorted, routable
// keys; verdicts that are a fixed point; a size proportional to the
// entries; and a Report and JSON round trip that work.
func checkTable(t *testing.T, tbl *Table, entries int) {
	t.Helper()
	uses := len(tbl.Invalid)
	for i := range tbl.Switches {
		s := &tbl.Switches[i]
		if i > 0 && compareKey(tbl.Switches[i-1].Key, s.Key) >= 0 {
			t.Fatalf("switches out of order at %d: %v, %v", i, tbl.Switches[i-1].Key, s.Key)
		}
		stage, room := s.Stage, s.Room
		if s.Store == StoreMemory || s.Store == StoreDan {
			stage, room = "", -1
		}
		if k, err := KeyFor(s.Table, stage, room, s.No); err != nil || k != s.Key {
			t.Fatalf("%v doesn't route back: %v, %v", s.Key, k, err)
		}
		for _, w := range s.Writers {
			if len(w.Inputs) > MaxANDInputs {
				t.Fatalf("%v: %d inputs", s.Key, len(w.Inputs))
			}
		}
		uses += len(s.Writers) + len(s.Readers)
	}
	checkFixedPoint(t, tbl)
	if tbl.Users+tbl.Unnamed != entries {
		t.Fatalf("%d users and %d unnamed of %d entries", tbl.Users, tbl.Unnamed, entries)
	}
	if uses > entries*maxUsesPerEntry || len(tbl.Undecoded) > entries || len(tbl.PushBlocks) > entries {
		t.Fatalf("%d uses, %d undecoded, %d push blocks from %d entries", uses, len(tbl.Undecoded), len(tbl.PushBlocks), entries)
	}
	if tbl.Report() == "" {
		t.Fatal("empty report")
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
		t.Fatalf("JSON round trip differs: %s", b)
	}
}

func FuzzBuild(f *testing.F) {
	a := func(name string, params uint32, angle ...int16) stagetest.Actor {
		e := stagetest.Actor{Name: name, Params: params}
		copy(e.Angle[:], angle)
		return e
	}
	stag := stagetest.StagChunk(stagetest.StagInfo(3, int(stage.StageDungeon), true))
	for _, seed := range [][]byte{
		stagetest.DZx(stag, stagetest.Actors("ACTR",
			a("SW_HIT0", 0xFF<<20|0x11), a("SW_HIT0", 0x10<<20|0x12, 0, 0, 0x13),
			a("AND_SW2", 0xFF<<24|0x10<<16|0<<8|2), a("AND_SW0", 0x20<<24|0x10<<16|0<<8|1),
			a("AND_SW0", 0x30<<24|0x31<<16|2<<8|3), a("AND_SW0", 0x31<<24|0x30<<16|0<<8|1),
		)),
		stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(20, 0, false)),
			stagetest.Actors("TGDR", a("door10", 0xC6<<20|5<<8|0xC5, 2|3<<6), a("door12", 1<<8|0x14, 0, 0, 9<<8)),
			stagetest.Actors("DOOR", a("doorKD", 0xC7<<20|2<<8|0x16, 1|4<<6)),
			stagetest.Actors("SCOB", a("takara", 0xC7<<12|2, 4, 0, 0xC8), a("SW_C00", 1<<16|0xFF<<8|0x20)),
		),
		stagetest.DZx(stagetest.Actors(stagetest.LayerTag("ACT", 3),
			a("osiBLK0", 1<<24|0x10<<8, 0, 0, 0x1103), a("osiBLK0", 0xB<<24|0x17<<8, 0, 0, 0x1806),
			a("Bk", 0xA, 0, 0, 0x22), a("Bdk", 0), a("Hfbot1A", 0x1234), a("Hseki1", 0x29<<8), a("Nope", 0),
			a("Kbota_A", 5<<24|0x10<<8), a("Evsw", 1<<24|0x30), a("AND_SW2", 0x00<<24|0x70<<16|0<<8|0xFE),
		)),
		stagetest.DZx(stagetest.Actors("ACTR", a("AND_SW0", 0xFE<<24|0xFE<<16|2<<8|MaxANDInputs))),
		// Tingle Tuner switches sharing a crystal, an AND fed by one, and
		// a type past ExeSubTable.
		stagetest.DZx(stag, stagetest.Actors("ACTR",
			a("SW_HIT0", 0xFF<<20|0x3D), a("agbA", 0xFF<<24|0x3D<<16|0xFFFF), a("agbMW", 0x3E<<24|0x3D<<16|0xFFFF, 0, 0, 7),
			a("AND_SW2", 0xFF<<24|0x3C<<16|0<<8|1), a("agbCSW", 0xC5<<24|0x85<<16|0xFFFF, 0, 0, 0x0F),
		)),
		stagetest.DZx(),
		nil,
	} {
		f.Add(seed)
	}
	names := testNames(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := stage.ParseDZx(b)
		if err != nil {
			return
		}
		st := &stage.Stage{Name: "F", SaveTable: 3, DZS: d, Rooms: []stage.Room{{No: 0, DZR: d}, {No: 63, DZR: d}}}
		if d.Stag != nil {
			st.SaveTable = d.Stag.SaveTable()
		}
		checkTable(t, Build([]*stage.Stage{st}, names), 3*len(d.Actors))
	})
}

// FuzzDecode places one entry of every proc the table reads, all with the
// same params and angle, in one room (or the stage's own file for a room
// outside 0-63) of one save table.
func FuzzDecode(f *testing.F) {
	for _, s := range []struct {
		params     uint32
		ax, ay, az int16
		table      uint8
		room       int8
	}{
		{0, 0, 0, 0, 3, 0},
		{0xFFFFFFFF, -1, -1, -1, 3, 1},
		{0x12345678, 0x1234, 0, 0x5678, 0, -1},
		{0x05C0FE21, 0x0102, 0, 0x0AFF, 15, 63},
		{0x80FF0102, 0x3F, 0, 0x7F01, 16, 5},
		{0x3E3DFFFF, 0, 0, 7, 3, 2},    // agbsw0 MW (a GBA writer)
		{0x3E3DFFFF, 0, 0, 0x0F, 3, 2}, // agbsw0 past ExeSubTable (unknown)
	} {
		f.Add(s.params, s.ax, s.ay, s.az, s.table, s.room)
	}
	names := testNames(f)
	f.Fuzz(func(t *testing.T, params uint32, ax, ay, az int16, table uint8, room int8) {
		var as []stage.Actor
		for _, sp := range specs {
			as = append(as, stage.Actor{
				Tag: "ACTR", Layer: stage.AllLayers, Index: len(as), Name: synthName(sp.proc),
				Params: params, Angle: [3]int16{ax, ay, az},
			})
		}
		st := &stage.Stage{Name: "F", SaveTable: int(table)}
		if room >= 0 && int(room) < stage.MaxRooms {
			st.Rooms = []stage.Room{{No: int(room), DZR: &stage.DZx{Actors: as}}}
		} else {
			st.DZS = &stage.DZx{Actors: as}
		}
		tbl := Build([]*stage.Stage{st}, names)
		if tbl.Users != len(specs) {
			t.Fatalf("%d users, want %d", tbl.Users, len(specs))
		}
		checkTable(t, tbl, len(as))
	})
}
