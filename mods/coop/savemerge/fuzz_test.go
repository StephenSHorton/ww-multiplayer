package savemerge

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// checkMerge is what FuzzMerge asks of two slot images, a and b, the
// merge half of what savefile's fuzz targets asked of a decoded file's
// slots 0 and 1 before the split: the merge with the table-free derives
// never fails and is idempotent, and with key and heart tables too it is
// idempotent, sets small keys at the live derive's target wherever the
// merge moved a source or door, and never lowers max life.
func checkMerge(t *testing.T, a, b *world.Image) {
	opt := MergeOptions{Policy: world.FullPolicy(), Derive: Derive(nil, nil)}
	m, err := Merge(a, b, opt)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if again, err := Merge(&m.Image, b, opt); err != nil || again.Image != m.Image {
		t.Fatalf("Merge again: %v", err)
	}
	kr := keyTable(true)
	opt.Derive = Derive(kr, heartTable(true))
	if m, err = Merge(a, b, opt); err != nil {
		t.Fatalf("Merge with tables: %v", err)
	}
	if again, err := Merge(&m.Image, b, opt); err != nil || again.Image != m.Image {
		t.Fatalf("Merge with tables again: %v", err)
	}
	src, doors := keyCount(&m.Image, &kr.Tables[0])
	src0, doors0 := keyCount(a, &kr.Tables[0])
	keys := m.Image[addr.OffMemBit(3)+addr.MemBitOffKeyNum]
	if want := derive.KeyTarget(derive.KeyFacts{Taken: src, Open: doors}, 0); (src != src0 || doors != doors0) && int(keys) != want {
		t.Fatalf("keys %d, the live derive's target %d", keys, want)
	}
	if be16(m.Image[addr.OffMaxLife:]) < be16(a[addr.OffMaxLife:]) {
		t.Fatal("the merge lowered max life")
	}
}

// FuzzMerge merges two slot images made from arbitrary packed bytes, as
// savefile.Unpack makes a slot's image from whatever its card holds,
// valid checksum or not (checkMerge). Short input is zero-filled.
func FuzzMerge(f *testing.F) {
	img := randImage(31, "Link")
	link := savefile.Pack(&img)
	empty := savefile.New().AllSlots()[0].Packed
	f.Add(empty[:], empty[:])
	f.Add(link[:], empty[:])
	f.Add(empty[:], link[:])
	f.Add(link[:], link[:])
	f.Fuzz(func(t *testing.T, pa, pb []byte) {
		var a, b savefile.Packed
		copy(a[:], pa)
		copy(b[:], pb)
		ia, ib := savefile.Unpack(&a), savefile.Unpack(&b)
		checkMerge(t, &ia, &ib)
	})
}
