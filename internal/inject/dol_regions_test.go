package inject

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// freighterClobbers are the GZLE01 DOL ranges (file offsets) Freighter
// overwrites to move the game's stack and arena for its own layout, which
// inject/build.py's "Revert Freighter's silent clobbers" step puts back
// (build.py freighter_clobbers): the patched DOL must hold the vanilla
// bytes there, so no DOLPatches run may touch them. A run here means the
// revert broke, or Freighter clobbers more than it did.
var freighterClobbers = []Span{
	{File: "main.dol", Start: 0x00002410, End: 0x00002410 + 8, Owner: "Freighter clobber (reverted)"},
	{File: "main.dol", Start: 0x000E82AC, End: 0x000E82AC + 8, Owner: "Freighter clobber (reverted)"},
	{File: "main.dol", Start: 0x000E82E4, End: 0x000E82E4 + 8, Owner: "Freighter clobber (reverted)"},
	{File: "main.dol", Start: 0x000EE7FC, End: 0x000EE7FC + 12, Owner: "Freighter clobber (reverted)"},
	{File: "main.dol", Start: 0x000EE80C, End: 0x000EE80C + 4, Owner: "Freighter clobber (reverted)"},
}

// dolProtected is what no in-DOL patch may write: the Freighter clobbers
// and the DOL header (PatchDOL edits the header's free text slot itself;
// a DOLPatches run there would fight it).
func dolProtected() []Span {
	return append([]Span{{File: "main.dol", Start: 0, End: dolHeaderSize, Owner: "DOL header (PatchDOL's)"}}, freighterClobbers...)
}

// dolSite is a place the build means to write in the DOL.
type dolSite struct {
	name   string
	off, n uint32 // file offset and length
}

// t1File is the file offset of the GZLE01 text-1 address addr, build.py's
// T1_FILE + (addr - T1_LOAD) (checked against build.py by buildPyFacts).
func t1File(addr uint32) uint32 { return osInitT1File + (addr - osInitT1Load) }

// dolSites are every in-DOL write this build intends, from build.py: the
// hook_branchlink sites Freighter rewrites (one bl each), the post-build
// patches list (OSInit arena-lo and the 48 MB heap plan), and the full
// body's name-table pointer (patch_body_name: the word at
// RELBodyNameEntryAddr + 4, in the data section that loads at file
// offset + 0x80003000, as internal/inject/rels' bodyNamePatchFileOff).
// A new site means a line here, which is the point: an unlisted write is
// a clobber until someone says otherwise.
func dolSites(t *testing.T) []dolSite {
	t.Helper()
	src, err := os.ReadFile(buildPy)
	if err != nil {
		t.Fatal(err)
	}
	var sites []dolSite
	for _, m := range regexp.MustCompile(`hook_branchlink\("(\w+)",\s*(0x[0-9A-Fa-f]+)\)`).FindAllSubmatch(src, -1) {
		a, _ := strconv.ParseUint(string(m[2]), 0, 32)
		sites = append(sites, dolSite{"hook " + string(m[1]), t1File(uint32(a)), 4})
	}
	if len(sites) == 0 {
		t.Fatal("build.py: no hook_branchlink sites")
	}
	_, patches := buildPyFacts(t)
	for _, p := range patches {
		sites = append(sites, dolSite{"build.py patch", t1File(p.addr), uint32(len(p.bytes))})
	}
	sites = append(sites, dolSite{"full-body name pointer", RELBodyNameEntryAddr + 4 - 0x80003000, 4})
	return sites
}

// unexplained returns the runs of patches not wholly inside one site.
func unexplained(patches []DOLPatch, sites []dolSite) []DOLPatch {
	var out []DOLPatch
	for _, p := range patches {
		end := uint64(p.FileOff) + uint64(len(p.Bytes))
		ok := false
		for _, s := range sites {
			if p.FileOff >= s.off && end <= uint64(s.off)+uint64(s.n) {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, p)
		}
	}
	return out
}

func dolSpans(patches []DOLPatch) []Span {
	spans := make([]Span, len(patches))
	for i, p := range patches {
		spans[i] = Span{File: "main.dol", Start: uint64(p.FileOff), End: uint64(p.FileOff) + uint64(len(p.Bytes)), Owner: "blob.go DOLPatches"}
	}
	return spans
}

// TestDOLPatchesStayInTheirSites validates the final DOL diff, blob.go's
// DOLPatches (scripts/extract_blob.py's byte diff of original.dol and
// patched.dol), against what the build means to write: no run touches a
// protected range (the Freighter clobbers build.py reverts, the DOL
// header), and every run lies inside a site build.py names. The next
// Freighter-style clobber, a write nobody asked for, fails here instead
// of as rendering corruption in a live session (build.py's history).
func TestDOLPatchesStayInTheirSites(t *testing.T) {
	if err := CheckOverlaps(dolSpans(DOLPatches), dolProtected()); err != nil {
		t.Error(err)
	}
	sites := dolSites(t)
	for _, p := range unexplained(DOLPatches, sites) {
		t.Errorf("DOLPatches writes %x at file 0x%X, inside no site build.py names (hooks, patches, the body name): a clobber?", p.Bytes, p.FileOff)
	}
	for _, s := range sites {
		if err := CheckOverlaps([]Span{{File: "main.dol", Start: uint64(s.off), End: uint64(s.off + s.n), Owner: s.name}}, dolProtected()); err != nil {
			t.Errorf("a build.py site is protected: %v", err)
		}
	}
}

// TestFreighterClobbersMatchBuildPy: the protected list is build.py's
// freighter_clobbers, entry for entry. Dropping the revert from build.py
// (or adding to it) must be matched here on purpose.
func TestFreighterClobbersMatchBuildPy(t *testing.T) {
	src, err := os.ReadFile(buildPy)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "freighter_clobbers = [")
	if i < 0 {
		t.Fatal("build.py: no freighter_clobbers list")
	}
	block := s[i : i+strings.Index(s[i:], "]")]
	var got []Span
	for _, m := range regexp.MustCompile(`\((0x[0-9A-Fa-f]+),\s*(\d+)\)`).FindAllStringSubmatch(block, -1) {
		off, _ := strconv.ParseUint(m[1], 0, 32)
		n, _ := strconv.ParseUint(m[2], 10, 32)
		got = append(got, Span{File: "main.dol", Start: off, End: off + n, Owner: "Freighter clobber (reverted)"})
	}
	if len(got) != len(freighterClobbers) {
		t.Fatalf("build.py lists %d clobbers, this test %d", len(got), len(freighterClobbers))
	}
	for i := range got {
		if got[i] != freighterClobbers[i] {
			t.Errorf("clobber %d: build.py %v, this test %v", i, got[i], freighterClobbers[i])
		}
	}
}

// The checks catch what they are for: a run over a reverted clobber is
// refused, and a run at a place no site names is reported.
func TestDOLSiteChecksCatchAClobber(t *testing.T) {
	clobber := append(DOLPatches[:len(DOLPatches):len(DOLPatches)], DOLPatch{FileOff: 0x000EE800, Bytes: []byte{0x60, 0x00, 0x00, 0x00}})
	if err := CheckOverlaps(dolSpans(clobber), dolProtected()); err == nil {
		t.Error("a run over a Freighter clobber passed")
	}
	stray := append(DOLPatches[:len(DOLPatches):len(DOLPatches)], DOLPatch{FileOff: 0x00100000, Bytes: []byte{0x60}})
	if got := unexplained(stray, dolSites(t)); len(got) != 1 || got[0].FileOff != 0x00100000 {
		t.Errorf("unexplained %v, want the stray run alone", got)
	}
	header := []DOLPatch{{FileOff: 0x90, Bytes: []byte{1}}}
	if err := CheckOverlaps(dolSpans(header), dolProtected()); err == nil {
		t.Error("a run in the DOL header passed")
	}
}

func TestCheckOverlaps(t *testing.T) {
	w := func(file string, s, e uint64, owner string) Span { return Span{File: file, Start: s, End: e, Owner: owner} }
	for _, c := range []struct {
		name            string
		writes, protect []Span
		ok              bool
	}{
		{"apart", []Span{w("a.rel", 0, 4, "x"), w("a.rel", 4, 8, "y")}, nil, true},
		{"other files", []Span{w("a.rel", 0, 4, "x"), w("b.rel", 0, 4, "y")}, nil, true},
		{"same owner overlapping", []Span{w("a.rel", 0, 8, "x"), w("a.rel", 4, 12, "x")}, nil, true},
		{"two owners", []Span{w("a.rel", 0, 8, "x"), w("a.rel", 4, 12, "y")}, nil, false},
		{"two owners, one far back", []Span{w("a.rel", 0, 100, "x"), w("a.rel", 10, 12, "x"), w("a.rel", 50, 52, "y")}, nil, false},
		{"whole file", []Span{w("a.rel", 0, WholeFile, "mod"), w("a.rel", 0x900, 0x904, "fix")}, nil, false},
		{"protected", []Span{w("main.dol", 0x10, 0x14, "x")}, []Span{w("main.dol", 0x12, 0x13, "p")}, false},
		{"protected elsewhere", []Span{w("main.dol", 0x10, 0x14, "x")}, []Span{w("a.rel", 0x10, 0x14, "p")}, true},
		{"empty", []Span{w("a.rel", 4, 4, "x")}, nil, false},
	} {
		err := CheckOverlaps(c.writes, c.protect)
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok %v", c.name, err, c.ok)
		}
	}
}
