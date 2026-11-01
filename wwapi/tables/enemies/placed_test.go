package enemies

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// pe is an unkeyed entry of kindan room r for the placement tests.
func pe(room, layer int, proc stage.Proc, class string, params uint32, pos Vec, angle [3]int16) Entry {
	return Entry{Stage: "kindan", Room: room, Layer: layer, Tag: "ACTR", Proc: proc, Class: class,
		SetID: 0xFFFF, Pos: pos, Angle: angle, Params: params, Key: NoKey, Checked: true}
}

func placedFixture() *Table {
	bo := stage.Proc(0xD7)
	keyed := pe(1, -1, stage.ProcBK, "BK", 0x3, Vec{0, 0, 0}, [3]int16{})
	keyed.SetID, keyed.Key = 4, Keyed
	return &Table{Enemies: []Entry{
		pe(1, -1, bo, "BO", 0xFFFF01FF, Vec{10, 20, 30}, [3]int16{}), // 0: placed
		keyed, // 1: keyed, never placed
		pe(1, 0, stage.ProcKI, "KI", 1, Vec{5, 100, 5}, [3]int16{}),             // 2: Keese, y wildcard
		pe(1, 1, stage.ProcKI, "KI", 1, Vec{5, 200, 5}, [3]int16{}),             // 3: the same fingerprint on another layer
		pe(1, -1, stage.ProcBK, "BK", 0x6, Vec{1, 1, 1}, [3]int16{}),            // 4: searchlight Bokoblin
		pe(1, -1, stage.ProcPT, "PT", 0xE4120F90, Vec{2, 2, 2}, [3]int16{}),     // 5: Miniblin with a byte-2 switch
		pe(1, -1, stage.ProcPT, "PT", 0xE4FF0F90, Vec{3, 3, 3}, [3]int16{}),     // 6: Miniblin without
		pe(1, -1, stage.ProcKS, "KS", 0x901, Vec{4, 4, 4}, [3]int16{}),          // 7: Morth
		pe(1, -1, stage.ProcBB, "BB", 0xFFFFFF03, Vec{0, 10, 0}, [3]int16{}),    // 8: Kargaroc, y + 70
		pe(1, -1, stage.ProcFM, "FM", 0, Vec{7, 7, 7}, [3]int16{100, 200, 300}), // 9: Floormaster, angle x/z cleared
		pe(2, -1, bo, "BO", 0xFFFF01FF, Vec{10, 20, 30}, [3]int16{}),            // room 2's ordinal 0
		pe(-1, -1, bo, "BO", 0xFFFF01FF, Vec{10, 20, 30}, [3]int16{}),           // a stage.dzs entry
	}}
}

func bits(v Vec) [3]uint32 {
	return [3]uint32{math.Float32bits(v[0]), math.Float32bits(v[1]), math.Float32bits(v[2])}
}

// TestPlacedOrdinals: the ordinal is the entry's index among its room's
// entries in table order, keyed and excluded ones counted; only placed
// entries are kept.
func TestPlacedOrdinals(t *testing.T) {
	tbl := placedFixture()
	if got, want := tbl.PlacedIn("kindan", 1).Ordinals(), []int{0, 2, 3, 6, 8, 9}; !slices.Equal(got, want) {
		t.Errorf("room 1 ordinals %v, want %v", got, want)
	}
	if got := tbl.PlacedIn("kindan", 2).Ordinals(); !slices.Equal(got, []int{0}) {
		t.Errorf("room 2 ordinals %v, want [0]", got)
	}
	if tbl.PlacedIn("kindan", -1) != nil {
		t.Error("a stage.dzs entry is placed")
	}
	for _, ord := range []int{1, 4, 5, 7, 10, 512} {
		if tbl.PlacedOK("kindan", 1, ord) {
			t.Errorf("ordinal %d is placed", ord)
		}
	}
	if n := tbl.PlacedTotal(); n != 7 {
		t.Errorf("total %d, want 7", n)
	}
}

// TestPlacedMatch: a live actor matches its entry by what fopAc_Create
// copied, with the class rules.
func TestPlacedMatch(t *testing.T) {
	tbl := placedFixture()
	r := tbl.PlacedIn("kindan", 1)
	bo := stage.Proc(0xD7)
	for _, c := range []struct {
		name  string
		fp    Fingerprint
		class []int
	}{
		{"Boko Baba", ActorFingerprint(bo, 0xFFFF, 0xFFFF01FF, bits(Vec{10, 20, 30}), [3]int16{}), []int{0}},
		{"Boko Baba moved", ActorFingerprint(bo, 0xFFFF, 0xFFFF01FF, bits(Vec{10, 21, 30}), [3]int16{}), nil},
		{"Boko Baba other params", ActorFingerprint(bo, 0xFFFF, 0xFFFF02FF, bits(Vec{10, 20, 30}), [3]int16{}), nil},
		{"Keese on the ceiling: both layers' twins", ActorFingerprint(stage.ProcKI, 0xFFFF, 1, bits(Vec{5, 950, 5}), [3]int16{}), []int{2, 3}},
		{"Kargaroc raised 70", ActorFingerprint(stage.ProcBB, 0xFFFF, 0xFFFFFF03, bits(Vec{0, 80, 0}), [3]int16{}), []int{8}},
		{"Kargaroc at its entry y", ActorFingerprint(stage.ProcBB, 0xFFFF, 0xFFFFFF03, bits(Vec{0, 10, 0}), [3]int16{}), nil},
		{"Floormaster, angle x/z cleared", ActorFingerprint(stage.ProcFM, 0xFFFF, 0, bits(Vec{7, 7, 7}), [3]int16{0, 200, 0}), []int{9}},
		{"Miniblin without a switch", ActorFingerprint(stage.ProcPT, 0xFFFF, 0xE4FF0F90, bits(Vec{3, 3, 3}), [3]int16{}), []int{6}},
		{"Miniblin with a switch", ActorFingerprint(stage.ProcPT, 0xFFFF, 0xE4120F90, bits(Vec{2, 2, 2}), [3]int16{}), nil},
		{"searchlight Bokoblin", ActorFingerprint(stage.ProcBK, 0xFFFF, 0x6, bits(Vec{1, 1, 1}), [3]int16{}), nil},
		{"keyed Bokoblin", ActorFingerprint(stage.ProcBK, 4, 0x3, bits(Vec{0, 0, 0}), [3]int16{}), nil},
		{"-0 is 0", ActorFingerprint(bo, 0xFFFF, 0xFFFF01FF, [3]uint32{math.Float32bits(10), math.Float32bits(20), math.Float32bits(30)}, [3]int16{}), []int{0}},
	} {
		if got := r.Match(c.fp); !slices.Equal(got, c.class) {
			t.Errorf("%s: class %v, want %v", c.name, got, c.class)
		}
	}
	neg := pe(3, -1, bo, "BO", 0xFFFF01FF, Vec{float32(math.Copysign(0, -1)), 0, 0}, [3]int16{})
	tbl2 := &Table{Enemies: []Entry{neg}}
	if got := tbl2.PlacedIn("kindan", 3).Match(ActorFingerprint(bo, 0xFFFF, 0xFFFF01FF, [3]uint32{0, 0, 0}, [3]int16{})); !slices.Equal(got, []int{0}) {
		t.Errorf("an entry at x = -0 matched by +0: %v", got)
	}
	if p, ok := r.At(3); !ok || !slices.Equal(p.Class, []int{2, 3}) {
		t.Errorf("ordinal 3's class: %+v", p)
	}
}

// TestPlacedStableThroughCache: a table read back from the cache's JSON
// numbers its placements and fingerprints them as the one built from
// the disc does.
func TestPlacedStableThroughCache(t *testing.T) {
	tbl := placedFixture()
	b, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back Table
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	for _, room := range []int{1, 2} {
		a, z := tbl.PlacedIn("kindan", room), back.PlacedIn("kindan", room)
		if !slices.Equal(a.Ordinals(), z.Ordinals()) {
			t.Fatalf("room %d: ordinals %v, back %v", room, a.Ordinals(), z.Ordinals())
		}
		for _, o := range a.Ordinals() {
			pa, _ := a.At(o)
			pz, _ := z.At(o)
			if pa.FP != pz.FP || !slices.Equal(pa.Class, pz.Class) {
				t.Errorf("room %d ordinal %d: %+v, back %+v", room, o, pa, pz)
			}
		}
	}
}
