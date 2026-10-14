package tables

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
)

// The benchmarks run Get on a synthetic disc at about the scale of
// res/Stage: benchStages stages of benchRooms rooms each, every archive
// Yaz0-compressed with a filler file standing for its models and
// textures, about half of which compresses away. Run them with
//
//	go test -run='^$' -bench=. -benchtime=3x ./internal/coop/tables/
//
// MB/s is disc bytes read per second; the read-ms and build-ms metrics
// split the time between reading and expanding the archives and running
// the four builders.
const (
	benchStages     = 100
	benchRooms      = 6
	benchRoomFiller = 1 << 20   // bytes, expanded
	benchStageFill  = 256 << 10 // bytes, expanded
	benchVariants   = 8         // distinct room archives, reused
)

// filler is n bytes that Yaz0 halves: runs of random bytes, half of them
// repeats of something in the last 4 KiB.
func filler(r *rand.Rand, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		l := 8 + r.IntN(57)
		if len(out) > 0 && r.IntN(2) == 0 {
			back := 1 + r.IntN(min(len(out), 0x1000)) // a copy may overlap itself
			for i := 0; i < l; i++ {
				out = append(out, out[len(out)-back])
			}
			continue
		}
		for i := 0; i < l; i++ {
			out = append(out, byte(r.IntN(256)))
		}
	}
	return out[:n]
}

// benchRoom is a room's entries: chests, crystals, push blocks and
// enemies, about what a dungeon room places.
func benchRoom(v int) stagetest.Room {
	var es []stagetest.Actor
	for i := range 40 {
		switch i % 4 {
		case 0:
			es = append(es, tablestest.Chest(uint32(i%32), 0x01))
		case 1:
			es = append(es, tablestest.Crystal(uint32(i)))
		case 2:
			es = append(es, tablestest.Enemy("Bk", uint16(i)))
		case 3:
			es = append(es, tablestest.Enemy("c_green", uint16(i+v)))
		}
	}
	return tablestest.Room(v, es...)
}

func benchDisc(b *testing.B) (*stagetest.Disc, int64) {
	b.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	var rooms [][]byte
	stage := tablestest.Stage("bench", 3, stagedata.StageDungeon, nil, []stagetest.Actor{tablestest.KeyDoor(0x10, 0, 1)})
	for v := range benchVariants {
		room := benchRoom(v)
		room.Extra = []stagetest.File{{Path: "bdl/model.bdl", Data: filler(r, benchRoomFiller)}}
		rooms = append(rooms, stage.RoomArc(room))
	}
	stage.Extra = []stagetest.File{{Path: "bdl/stage.bdl", Data: filler(r, benchStageFill)}}
	stageArc := stage.StageArc()

	d := tablestest.Disc(nil)
	var size int64
	for s := range benchStages {
		dir := fmt.Sprintf("res/Stage/B%03d/", s)
		d.Add(dir+"Stage.arc", stageArc)
		size += int64(len(stageArc))
		for no := range benchRooms {
			arc := rooms[(s+no)%len(rooms)]
			d.Add(fmt.Sprintf("%sRoom%d.arc", dir, no), arc)
			size += int64(len(arc))
		}
	}
	return d, size
}

func BenchmarkGet(b *testing.B) {
	d, size := benchDisc(b)
	src := openISO(b, d.ISO(tablestest.DOL()))
	b.SetBytes(size)
	b.ResetTimer()
	var read, build float64
	for b.Loop() {
		_, st, err := Get(src, "")
		if err != nil {
			b.Fatal(err)
		}
		read += float64(st.Read.Milliseconds())
		build += float64(st.Build.Milliseconds())
	}
	b.ReportMetric(read/float64(b.N), "read-ms")
	b.ReportMetric(build/float64(b.N), "build-ms")
	b.ReportMetric(float64(size)/(1<<20), "disc-MiB")
	b.ReportMetric(float64(benchStages*(benchRoomFiller*benchRooms+benchStageFill))/(1<<20), "expanded-MiB")
}

func BenchmarkLoadCache(b *testing.B) {
	d, _ := benchDisc(b)
	src := openISO(b, d.ISO(tablestest.DOL()))
	dir := b.TempDir()
	tb, _, err := Get(src, dir)
	if err != nil {
		b.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, tb.DOL+".json"))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(fi.Size())
	b.ResetTimer()
	for b.Loop() {
		if _, st, err := Get(src, dir); err != nil || !st.Cached {
			b.Fatal(err, st.Why)
		}
	}
	b.ReportMetric(float64(fi.Size())/(1<<20), "cache-MiB")
}
