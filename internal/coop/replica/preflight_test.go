package replica

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// imageOf builds an image from bits: (off, mask) pairs ORed in.
func imageOf(pairs ...any) world.Image {
	var img world.Image
	for i := 0; i < len(pairs); i += 2 {
		img[pairs[i].(int)] |= pairs[i+1].(byte)
	}
	return img
}

func TestPreflight(t *testing.T) {
	c1, m1 := chest(addr.TableSea, 1)
	c2, m2 := chest(addr.TableDRC, 2)
	item := addr.OffGetItem + 3
	korl, korlMask := eventBit(evMetKorl)
	rode, rodeMask := eventBit(0x2A08)
	d10, d10Mask := eventBit(ev2D10)
	sw, swMask := func() (int, byte) {
		_, b, _ := addr.SwitchBit(0x20)
		return addr.OffMemBit(addr.TableSea) + b.Off, b.Mask
	}()
	wallet := addr.OffWalletSize
	second := imageOf(c1, m1)
	second[addr.OffClearCount] = 1

	cases := []struct {
		name        string
		file, world world.Image
		full        bool // world.FullPolicy instead of the default
		verdict     PreflightVerdict
		merge       bool
		fileGains   int
		worldGains  int
		held        int
		missing     []uint16
		worldCat    map[Category]int
	}{
		{name: "a fresh file and an empty world", verdict: PreflightSame},
		{name: "the same progress", file: imageOf(c1, m1, item, byte(0x01)), world: imageOf(c1, m1, item, byte(0x01)), verdict: PreflightSame},
		{name: "a subset of the world", file: imageOf(c1, m1), world: imageOf(c1, m1, c2, m2, item, byte(0x81)),
			verdict: PreflightSubset, worldGains: 3, worldCat: map[Category]int{CatStage: 1, CatInventory: 2}},
		{name: "ahead of the world (a host founding one)", file: imageOf(c1, m1, c2, m2), verdict: PreflightAhead, merge: true, fileGains: 2},
		{name: "divergent", file: imageOf(c1, m1), world: imageOf(c2, m2), verdict: PreflightDivergent, merge: true, fileGains: 1, worldGains: 1},
		{name: "a MAX byte counts by value", file: func() world.Image { var i world.Image; i[wallet] = 1; return i }(),
			world:   func() world.Image { var i world.Image; i[wallet] = 2; return i }(),
			verdict: PreflightSubset, worldGains: 1, worldCat: map[Category]int{CatCapacity: 1}},
		{name: "the default policy leaves memory switches out", file: imageOf(c1, m1), world: imageOf(c1, m1, sw, swMask), verdict: PreflightSame},
		{name: "a full policy counts them", file: imageOf(c1, m1), world: imageOf(c1, m1, sw, swMask), full: true,
			verdict: PreflightSubset, worldGains: 1, worldCat: map[Category]int{CatSwitch: 1}},
		{name: "far behind: the world rode with Korl", world: imageOf(rode, rodeMask), full: true,
			verdict: PreflightFarBehind, merge: true, worldGains: 1, missing: []uint16{0x2A08}},
		{name: "far behind, with MET_KORL held", world: imageOf(korl, korlMask), full: true,
			verdict: PreflightFarBehind, merge: true, worldGains: 1, held: 1, missing: []uint16{evMetKorl}},
		{name: "far behind even when the file has unique progress", file: imageOf(c1, m1), world: imageOf(rode, rodeMask), full: true,
			verdict: PreflightFarBehind, merge: true, fileGains: 1, worldGains: 1, missing: []uint16{0x2A08}},
		{name: "a boat lock is held, not a milestone", world: imageOf(d10, d10Mask), full: true,
			verdict: PreflightSubset, worldGains: 1, held: 1},
		{name: "a second-quest file is refused", file: second, world: imageOf(c2, m2), verdict: PreflightRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opt := PreflightOptions{}
			if c.full {
				opt.Policy = world.FullPolicy()
			}
			r := Preflight(&c.file, &c.world, opt)
			if r.Verdict != c.verdict || r.NeedsMerge != c.merge || len(r.FileGains) != c.fileGains ||
				len(r.WorldGains) != c.worldGains || len(r.Held) != c.held || !slices.Equal(r.MissingMilestones, c.missing) {
				t.Fatalf("report %+v", r)
			}
			for cat, n := range c.worldCat {
				if r.WorldByCategory[cat] != n {
					t.Errorf("world adds %d %s, want %d", r.WorldByCategory[cat], cat, n)
				}
			}
			if r.Why == "" {
				t.Error("no reason given")
			}
			sum := r.Summary()
			if !strings.Contains(sum, r.Verdict.String()) {
				t.Errorf("summary %q lacks the verdict", sum)
			}
			for _, h := range r.Held {
				if !strings.Contains(sum, h.Label) {
					t.Errorf("summary %q lacks hold %q", sum, h.Label)
				}
			}
		})
	}
}

// FuzzPreflight checks the report's invariants on arbitrary images: it is
// a pure function, each gain is a real one-sided difference, the counts
// add up, the holds are world gains, the verdict follows the lists, and
// the two directions mirror each other.
func FuzzPreflight(f *testing.F) {
	c1, m1 := chest(addr.TableSea, 1)
	korl, korlMask := eventBit(evMetKorl)
	f.Add([]byte{}, []byte{}, false)
	f.Add([]byte{byte(c1 >> 8), byte(c1), m1}, []byte{}, false)
	f.Add([]byte{byte(korl >> 8), byte(korl), korlMask}, []byte{0x00, 0x51, 0xFF, 0x00, 0x1A, 0x02}, true)
	f.Add([]byte{0x00, 0x1A, 0x03, 0x02, 0x00, 0x01}, []byte{0x00, 0x1A, 0x04}, true)
	f.Fuzz(func(t *testing.T, fileOps, worldOps []byte, full bool) {
		build := func(ops []byte) world.Image {
			var img world.Image
			for i := 0; i+2 < len(ops); i += 3 {
				off := (int(ops[i])<<8 | int(ops[i+1])) % addr.SaveImageSize
				img[off] |= ops[i+2]
			}
			return img
		}
		file, wimg := build(fileOps), build(worldOps)
		file[addr.OffClearCount], wimg[addr.OffClearCount] = 0, 0
		opt := PreflightOptions{}
		pol := world.DefaultPolicy()
		if full {
			pol = world.FullPolicy()
			opt.Policy = pol
		}
		r := Preflight(&file, &wimg, opt)
		if again := Preflight(&file, &wimg, opt); !reflect.DeepEqual(r, again) {
			t.Fatal("Preflight is not deterministic")
		}
		lf, lw := world.Shared(&file), world.Shared(&wimg)
		check := func(side string, gs []Bit, from, to *world.Image, byCat [numCategories]int) {
			total := 0
			for _, n := range byCat {
				total += n
			}
			if total != len(gs) {
				t.Fatalf("%s: categories count %d, list has %d", side, total, len(gs))
			}
			for _, g := range gs {
				off := int(g.Off)
				if pol[off] == 0 {
					t.Fatalf("%s: %s is outside the policy", side, g.Label)
				}
				if class.Of(off).Class == class.Max {
					if !g.Max || to[off] != g.Val || g.Val <= from[off] {
						t.Fatalf("%s: MAX %s = %d, from %d to %d", side, g.Label, g.Val, from[off], to[off])
					}
				} else if g.Max || to[off]&g.Val == 0 || from[off]&g.Val != 0 || g.Val&(g.Val-1) != 0 {
					t.Fatalf("%s: %s bit 0x%02X, from 0x%02X to 0x%02X", side, g.Label, g.Val, from[off], to[off])
				}
				if g.Category != CategoryOf(off) || g.Category == CatNone {
					t.Fatalf("%s: %s in category %s", side, g.Label, g.Category)
				}
			}
		}
		check("file gains", r.FileGains, &lw, &lf, r.FileByCategory)
		check("world gains", r.WorldGains, &lf, &lw, r.WorldByCategory)
		for _, h := range r.Held {
			if !slices.ContainsFunc(r.WorldGains, func(g Bit) bool { return g.Off == h.Off && g.Val == h.Val }) {
				t.Fatalf("held %s is not a world gain", h.Label)
			}
		}
		switch want := r.Verdict; {
		case len(r.MissingMilestones) > 0:
			if want != PreflightFarBehind {
				t.Fatalf("milestones missing but verdict %s", want)
			}
		case len(r.FileGains) == 0 && len(r.WorldGains) == 0:
			if want != PreflightSame {
				t.Fatalf("no gains but verdict %s", want)
			}
		case len(r.FileGains) == 0:
			if want != PreflightSubset {
				t.Fatalf("only world gains but verdict %s", want)
			}
		case len(r.WorldGains) == 0:
			if want != PreflightAhead {
				t.Fatalf("only file gains but verdict %s", want)
			}
		default:
			if want != PreflightDivergent {
				t.Fatalf("gains both ways but verdict %s", want)
			}
		}
		if r.NeedsMerge != (r.Verdict == PreflightAhead || r.Verdict == PreflightDivergent || r.Verdict == PreflightFarBehind) {
			t.Fatalf("verdict %s with NeedsMerge %v", r.Verdict, r.NeedsMerge)
		}
		mirror := Preflight(&wimg, &file, opt)
		if !reflect.DeepEqual(mirror.FileGains, r.WorldGains) || !reflect.DeepEqual(mirror.WorldGains, r.FileGains) {
			t.Fatal("the two directions do not mirror each other")
		}
		_ = r.Summary()
	})
}
