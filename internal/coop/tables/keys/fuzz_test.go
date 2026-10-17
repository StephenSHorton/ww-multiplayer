package keys

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// Fuzz target for the key table over fuzzed stage and room files. The
// seed corpus is built in code (the vanilla fixture's files; no game data)
// and runs as an ordinary test under `go test`; to fuzz it:
//
//	go test -run='^$' -fuzz=FuzzBuild -fuzztime=20s ./internal/coop/tables/keys/

// fuzzStage is a stage of one fuzzed stage file and one fuzzed room file,
// as stage.Load would make it of them.
func fuzzStage(dzs, dzr []byte) (*stage.Stage, bool) {
	d, err := stage.ParseDZx(dzs)
	if err != nil || d.Stag == nil {
		return nil, false
	}
	s := &stage.Stage{Name: "fz", SaveTable: d.Stag.SaveTable(), Stag: *d.Stag, DZS: d}
	if r, err := stage.ParseDZx(dzr); err == nil {
		s.Rooms = []stage.Room{{No: 1, DZR: r}}
	}
	return s, true
}

func FuzzBuild(f *testing.F) {
	for _, s := range vanilla() {
		var dzr []byte
		if len(s.Rooms) > 0 {
			dzr = s.Rooms[0].DZR
		}
		f.Add(s.DZS, dzr)
	}
	f.Add(stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(0x7F, 0, true)),
		stagetest.Actors("TGDR", door("keyS12", 1, 0xFF, 63, 63), door("doorKD", 2, 0xC0, 0, 0))), []byte(nil))
	f.Add([]byte(nil), []byte(nil))
	names := testNames(f)
	f.Fuzz(func(t *testing.T, dzs, dzr []byte) {
		s, ok := fuzzStage(dzs, dzr)
		if !ok {
			return
		}
		r := Build([]*stage.Stage{s}, names)
		n := len(s.All())
		pass := true
		for _, tb := range r.Tables {
			pass = pass && tb.Pass
			if tb.Pass != (len(tb.Problems) == 0) || len(tb.Sources) > n || len(tb.Doors) > n {
				t.Fatalf("table %+v from %d entries", tb, n)
			}
			for _, src := range tb.Sources {
				if src.Item != itemsrc.ItemSmallKey || len(src.At) == 0 {
					t.Fatalf("source %+v", src)
				}
			}
			for _, d := range tb.Doors {
				for _, e := range d.At {
					if e.Switch != d.Switch || e.FrontRoom > 63 || e.BackRoom > 63 || e.Class == "" {
						t.Fatalf("door %+v", d)
					}
				}
			}
		}
		if pass != r.Pass {
			t.Fatalf("Pass %v, tables %v", r.Pass, pass)
		}
		if rep := r.Report(); !strings.HasPrefix(rep, "small keys: ") {
			t.Fatalf("report %q", rep)
		}
		if _, err := json.Marshal(r); err != nil {
			t.Fatal(err)
		}
	})
}
