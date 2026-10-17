package hearts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// Fuzz target for the heart table over fuzzed stage and room files. The
// seed corpus is built in code (the vanilla fixture's files; no game
// data) and runs as an ordinary test under `go test`; to fuzz it:
//
//	go test -run='^$' -fuzz=FuzzBuild -fuzztime=20s ./internal/coop/tables/hearts/

func FuzzBuild(f *testing.F) {
	for _, s := range vanilla() {
		var dzr []byte
		if len(s.Rooms) > 0 {
			dzr = s.Rooms[0].DZR
		}
		f.Add(s.DZS, dzr)
	}
	f.Add(stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(0x7F, 0, false)),
		stagetest.Actors("ACTR", bitem(0xFF), bitem(0x7F), boss("Bwd"))), []byte(nil))
	f.Add([]byte(nil), []byte(nil))
	names := testNames(f)
	f.Fuzz(func(t *testing.T, dzs, dzr []byte) {
		d, err := stage.ParseDZx(dzs)
		if err != nil || d.Stag == nil {
			return
		}
		s := &stage.Stage{Name: "fz", SaveTable: d.Stag.SaveTable(), Stag: *d.Stag, DZS: d}
		if r, err := stage.ParseDZx(dzr); err == nil {
			s.Rooms = []stage.Room{{No: 1, DZR: r}}
		}
		r := Build([]*stage.Stage{s}, names)
		n := len(s.All())
		at := 0
		for _, src := range r.Pieces {
			at += len(src.At)
			if src.Item != itemsrc.ItemHeartPiece && src.Item != itemsrc.ItemHeartPieceAlt {
				t.Fatalf("piece %+v", src)
			}
		}
		for _, src := range r.Containers {
			at += len(src.At)
			if src.Item != itemsrc.ItemHeartContainer || src.Flag.Kind != itemsrc.StageLifeFlag || src.Flag.Table != lifeTable(s.SaveTable) {
				t.Fatalf("container %+v", src)
			}
		}
		if at > n || len(r.Containers) > 1 {
			t.Fatalf("%d placements, %d containers from %d entries of one stage", at, len(r.Containers), n)
		}
		tt := r.Totals
		if tt.Pieces != tt.StagePieces+tt.RewardPieces+tt.UnverifiedPieces || tt.StagePieces != len(r.Pieces) ||
			tt.Containers != len(r.Containers) || r.Pass != (len(r.Problems) == 0) {
			t.Fatalf("totals %+v, problems %v", tt, r.Problems)
		}
		if rep := r.Report(); !strings.HasPrefix(rep, "hearts: ") {
			t.Fatalf("report %q", rep)
		}
		if _, err := json.Marshal(r); err != nil {
			t.Fatal(err)
		}
	})
}
