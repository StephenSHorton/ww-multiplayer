package itemsrc

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// Fuzz target for the placement decoders over any entry. The seed corpus
// is built in code and runs as an ordinary test under `go test`; to fuzz
// it:
//
//	go test -run='^$' -fuzz=FuzzDecode -fuzztime=20s ./internal/coop/tables/keys/itemsrc/

func FuzzDecode(f *testing.F) {
	f.Add(int16(stagedata.ProcTbox), uint32(5<<7|7), int16(0), int16(0), int16(0x1500), 2, 3)
	f.Add(int16(stagedata.ProcItem), uint32(0x45<<8|0x15), int16(0), int16(0), int16(0), 2, 3)
	f.Add(int16(ProcSalvage), uint32(2<<28|1<<20|0x07<<4), int16(0), int16(0), int16(0x10), 0, 0)
	f.Add(int16(ProcSalvage), uint32(4<<28|31<<20|0x07<<4), int16(0), int16(0), int16(0), 50, 0)
	f.Add(int16(ProcBemos), uint32(0), int16(0), int16(0), int16(-1), -1, 3)
	f.Add(int16(ProcGrass), uint32(0xFFFFFFFF), int16(-1), int16(-1), int16(-1), 63, 0xF)
	f.Fuzz(func(t *testing.T, proc int16, prm uint32, ax, ay, az int16, room, table int) {
		s := &stagedata.Stage{Name: "fz", SaveTable: table}
		p := stagedata.Placed{Room: room, Actor: stagedata.Actor{Tag: "ACTR", Layer: stagedata.AllLayers, Name: "x", Params: prm, Angle: [3]int16{ax, ay, az}}}
		pl, ok := Decode(s, p, stagedata.Proc(proc))
		if _, known := decoders[stagedata.Proc(proc)]; !known && ok {
			t.Fatalf("proc %d decoded: %+v", proc, pl)
		}
		if !ok {
			return
		}
		if pl.Proc != stagedata.Proc(proc) || pl.Room != room || pl.Stage != "fz" || pl.Kind == "" {
			t.Fatalf("%+v", pl)
		}
		switch pl.Flag.Kind {
		case TboxFlag:
			if pl.Flag.Bit < 0 || pl.Flag.Bit > 31 {
				t.Fatalf("tbox bit %d", pl.Flag.Bit)
			}
		case ItemFlag:
			if pl.Flag.Bit < 0 || pl.Flag.Bit >= 0x40 || pl.Flag.Table != table {
				t.Fatalf("item flag %+v", pl.Flag)
			}
		case OceanFlag:
			if pl.Flag.Room < 1 || pl.Flag.Room > 0x31 || pl.Flag.Bit < 0 || pl.Flag.Bit > 15 {
				t.Fatalf("ocean flag %+v outside dSv_ocean_c", pl.Flag)
			}
		case HaltFlag:
			if pl.Flag.Room >= 0 && pl.Flag.Room <= 0x31 && pl.Flag.Bit >= 0 && pl.Flag.Bit <= 15 {
				t.Fatalf("halt flag %+v inside dSv_ocean_c", pl.Flag)
			}
		}
		_ = pl.Flag.String()
		_ = pl.Where.String()
		g := Group([]Placement{pl, pl})
		if want := 2; pl.Flag.Saved() {
			want = 1
			if len(g) != want || len(g[0].At) != 2 {
				t.Fatalf("Group = %+v", g)
			}
		} else if len(g) != want {
			t.Fatalf("Group = %+v", g)
		}
	})
}
