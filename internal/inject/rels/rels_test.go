package rels

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// testREL is a small REL of module id with a .text of n bytes that
// Yaz0 can't shrink (pseudo-random) and a .data of 0x80 bytes, laid out
// by rel.Bytes.
func testREL(t testing.TB, id uint32, n int) []byte {
	t.Helper()
	text := make([]byte, n)
	x := uint64(0x9E3779B97F4A7C15) + uint64(id)
	for i := range text {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		text[i] = byte(x)
	}
	f := &rel.File{
		ID: id, Version: 3, Align: 8, BSSAlign: 1,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 4}, Unresolved: rel.Symbol{Section: 1, Offset: 8},
		Sections: []rel.Section{
			{},
			{Offset: 1, Exec: true, Data: text}, // Offset != 0: has data; Bytes places it
			{Offset: 1, Data: bytes.Repeat([]byte{0xAB}, 0x80)},
		},
		Imports: []rel.Import{{Module: 0, Relocs: []rel.Reloc{{Section: 1, Offset: 0, Type: rel.RelocRel24, Addend: 0x80001234}}}},
	}
	b, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testProfileList is a REL like f_pc_profile_lst.rel: module 1, a .data
// of pointers, one ADDR32 relocation against each actor module (0x42
// and 0x58 here) pointing at that module's profile in its .data, and a
// few against module 0.
func testProfileList(t testing.TB) []byte {
	t.Helper()
	f := &rel.File{
		ID: ProfileListModule, Version: 3, Align: 8, BSSAlign: 1,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 4}, Unresolved: rel.Symbol{Section: 1, Offset: 8},
		Sections: []rel.Section{
			{},
			{Offset: 1, Exec: true, Data: make([]byte, 0x20)},
			{Offset: 1, Data: make([]byte, 0x40)},
		},
		Imports: []rel.Import{
			{Module: 0x42, Relocs: []rel.Reloc{{Section: 2, Offset: 4, Type: rel.RelocAddr32, TargetSection: 2, Addend: 0x20}}},
			{Module: 0x58, Relocs: []rel.Reloc{{Section: 2, Offset: 8, Type: rel.RelocAddr32, TargetSection: 2, Addend: 0x30}}},
			{Module: 0, Relocs: []rel.Reloc{
				{Section: 1, Offset: 0, Type: rel.RelocRel24, Addend: 0x80001234},
				{Section: 2, Offset: 0, Type: rel.RelocAddr32, Addend: 0x80005678},
			}},
		},
	}
	b, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testRELSArc is a RELS.arc in miniature: the profile list, two actor
// RELs (0x58 is the one the puppet replaces) and an uncompressed file.
func testRELSArc(t testing.TB) ([]byte, []stagetest.File) {
	t.Helper()
	files := []stagetest.File{
		{Path: "mmem/f_pc_profile_lst.rel", Data: testProfileList(t), Yaz0: true},
		{Path: "amem/d_a_other.rel", Data: testREL(t, 0x42, 0x300), Yaz0: true},
		{Path: "amem/d_a_rectangle.rel", Data: testREL(t, 0x58, 0x200), Yaz0: true},
		{Path: "mmem/plain.bin", Data: []byte("not a REL at all, eight bytes and more")},
	}
	return stagetest.RARC("rels", files...), files
}

// checkRELSPatched asserts out is arc with module 0x58 replaced by
// newREL and the profile list's entry for it pointing at (section,
// offset): every other file reads as before, the entries are the same.
func checkRELSPatched(t *testing.T, arc, out, newREL []byte, module uint32, section uint8, offset uint32) {
	t.Helper()
	before, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	after, err := stage.ParseArchive(out)
	if err != nil {
		t.Fatalf("the patched archive doesn't parse: %v", err)
	}
	bf, af := before.Files(), after.Files()
	if len(bf) != len(af) {
		t.Fatalf("%d files, had %d", len(af), len(bf))
	}
	replaced, lists := 0, 0
	for i := range bf {
		b, a := bf[i], af[i]
		if a.Path != b.Path || a.Index != b.Index || a.ID != b.ID || a.Attr != b.Attr {
			t.Errorf("entry %d: %+v, was %+v", i, a, b)
		}
		wasB, err := before.Read(b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := after.Read(a)
		if err != nil {
			t.Fatalf("%s: %v", a.Path, err)
		}
		switch {
		case len(wasB) >= 4 && binary.BigEndian.Uint32(wasB) == module:
			replaced++
			if !bytes.Equal(got, newREL) {
				t.Errorf("%s: %d bytes, want the new REL (%d)", a.Path, len(got), len(newREL))
			}
		case len(wasB) >= 4 && binary.BigEndian.Uint32(wasB) == ProfileListModule:
			lists++
			lf, err := rel.Parse(got)
			if err != nil {
				t.Fatalf("%s: %v", a.Path, err)
			}
			r, err := lf.SoleReloc(module)
			if err != nil || r.Type != rel.RelocAddr32 || r.TargetSection != section || r.Addend != offset {
				t.Errorf("%s: relocation against 0x%x = %+v, %v; want section %d offset 0x%x", a.Path, module, r, err, section, offset)
			}
			// Only that entry's 8 bytes changed.
			diff := 0
			for k := range wasB {
				if len(got) != len(wasB) || got[k] != wasB[k] {
					diff++
				}
			}
			if len(got) != len(wasB) || diff == 0 || diff > 8 {
				t.Errorf("%s: %d bytes differ (%d, was %d), want one relocation entry", a.Path, diff, len(got), len(wasB))
			}
		default:
			if !bytes.Equal(got, wasB) {
				t.Errorf("%s changed", a.Path)
			}
			if a.Size != b.Size {
				t.Errorf("%s: stored size %d, was %d", a.Path, a.Size, b.Size)
			}
		}
	}
	if replaced != 1 || lists != 1 {
		t.Errorf("%d modules 0x%x and %d profile lists found", replaced, module, lists)
	}
}

func TestPatchRELSArc(t *testing.T) {
	arc, _ := testRELSArc(t)
	newREL := testREL(t, 0x58, 0x1000)
	out, err := PatchRELSArc(arc, 0x58, newREL, 2, 0x40, RELSArcMaxGrowth)
	if err != nil {
		t.Fatal(err)
	}
	checkRELSPatched(t, arc, out, newREL, 0x58, 2, 0x40)
	growth := len(out) - len(arc)
	if max := len(stage.EncodeYaz0(newREL)) + 0x20; growth <= 0 || growth > max {
		t.Errorf("grew by %d bytes, want between 1 and %d", growth, max)
	}
	// The same inputs give the same archive, and patching the patched
	// archive again gives it too (the REL and the profile entry are
	// already what they are set to).
	if again, err := PatchRELSArc(arc, 0x58, newREL, 2, 0x40, RELSArcMaxGrowth); err != nil || !bytes.Equal(again, out) {
		t.Errorf("a second patch differs: %v", err)
	}
	if again, err := PatchRELSArc(out, 0x58, newREL, 2, 0x40, RELSArcMaxGrowth); err != nil || !bytes.Equal(again, out) {
		t.Errorf("patching the patched archive differs: %v", err)
	}
	// A smaller REL shrinks it; growth is allowed to be negative.
	small := testREL(t, 0x58, 0x10)
	if out, err := PatchRELSArc(arc, 0x58, small, 1, 0, 0); err != nil || len(out) >= len(arc) {
		t.Errorf("with a smaller REL: %d bytes (was %d), %v", len(out), len(arc), err)
	} else {
		checkRELSPatched(t, arc, out, small, 0x58, 1, 0)
	}
	// The other actor module can be replaced the same way.
	other := testREL(t, 0x42, 0x400)
	if out, err := PatchRELSArc(arc, 0x42, other, 2, 0x10, RELSArcMaxGrowth); err != nil {
		t.Errorf("module 0x42: %v", err)
	} else {
		checkRELSPatched(t, arc, out, other, 0x42, 2, 0x10)
	}
}

func TestPatchRELSArcRejects(t *testing.T) {
	arc, files := testRELSArc(t)
	good := testREL(t, 0x58, 0x100)
	noList := stagetest.RARC("rels", files[1:]...)
	noReloc := func() []byte {
		lf, _ := rel.Parse(testProfileList(t))
		lf.Imports = lf.Imports[:1] // 0x42 only
		b, _ := lf.Bytes(false)
		f := slices.Clone(files)
		f[0].Data = b
		return stagetest.RARC("rels", f...)
	}()
	cases := []struct {
		name    string
		arc     []byte
		module  uint32
		rel     []byte
		section uint8
		offset  uint32
		max     int
		wantErr string
	}{
		{"module 0", arc, 0, good, 2, 0, 1 << 20, "module 0x0 can't be replaced"},
		{"the profile list itself", arc, 1, good, 2, 0, 1 << 20, "module 0x1 can't be replaced"},
		{"compressed REL", arc, 0x58, stage.EncodeYaz0(good), 2, 0, 1 << 20, "must be given uncompressed"},
		{"not a REL", arc, 0x58, []byte("hello"), 2, 0, 1 << 20, "new REL: rel: header truncated"},
		{"wrong module", arc, 0x58, testREL(t, 0x42, 0x100), 2, 0, 1 << 20, "the new REL is module 0x42, not 0x58"},
		{"profile section without data", arc, 0x58, good, 0, 0, 1 << 20, "no data in section 0"},
		{"profile section past the end", arc, 0x58, good, 3, 0, 1 << 20, "no data in section 3"},
		{"profile past the section", arc, 0x58, good, 2, 0x80, 1 << 20, "profile at 0x80 is past the end of the new REL's section 2 (0x80 bytes)"},
		{"not an archive", []byte("RARC but not really"), 0x58, good, 2, 0, 1 << 20, "rels: rarc:"},
		{"no such module", arc, 0x59, testREL(t, 0x59, 0x100), 2, 0, 1 << 20, "module 0x59: rarc: no file starts with the word 0x00000059"},
		{"no profile list", noList, 0x58, good, 2, 0, 1 << 20, "no file starts with the module ID 0x1"},
		{"profile list without the module", noReloc, 0x58, good, 2, 0, 1 << 20, "f_pc_profile_lst.rel: rel: 0 relocations against module 0x58"},
		{"too much growth", arc, 0x58, testREL(t, 0x58, 0x4000), 2, 0, 0x100, "RELS.arc would grow past the ARAM guard"},
	}
	for _, c := range cases {
		_, err := PatchRELSArc(c.arc, c.module, c.rel, c.section, c.offset, c.max)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
		if c.name == "no such module" && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: err = %v, want fs.ErrNotExist", c.name, err)
		}
		if c.name == "too much growth" {
			if !errors.Is(err, ErrRELSArcGrowth) || !strings.Contains(err.Error(), "limit 256") {
				t.Errorf("%s: err = %v, want ErrRELSArcGrowth with the limit", c.name, err)
			}
		}
	}
	// The input is never modified.
	orig, _ := testRELSArc(t)
	if !bytes.Equal(arc, orig) {
		t.Error("the input archive changed")
	}
}

// TestPatchRELSArcProfiles: the synthetic archive with relstest's list
// (entries at 4*proc): entry 0x27 moves from module 0xA6 into 0x58 and
// that import goes; the bad targets are refused by name.
func TestPatchRELSArcProfiles(t *testing.T) {
	arc := relstest.Arc()
	newREL := testREL(t, relstest.PuppetModule, 0x200)
	main := ProfileTarget{Proc: relstest.PuppetProc, Section: 2, Offset: 0x40}
	body := []ProfileTarget{{Proc: relstest.SeatagProc, Section: 2, Offset: 0x10}}
	out, err := PatchRELSArcProfiles(arc, relstest.PuppetModule, newREL, main, body, RELSArcMaxGrowth)
	if err != nil {
		t.Fatal(err)
	}
	a, err := stage.ParseArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	var lf *rel.File
	for _, f := range a.Files() {
		b, _ := a.Read(f)
		if len(b) >= 4 && binary.BigEndian.Uint32(b) == ProfileListModule {
			lf, _ = rel.Parse(b)
		}
	}
	if lf == nil {
		t.Fatal("no profile list")
	}
	imp := lf.Import(relstest.PuppetModule)
	if lf.Import(relstest.SeatagModule) != nil || imp == nil || len(imp.Relocs) != 2 {
		t.Fatalf("imports %+v", lf.Imports)
	}
	for _, c := range []struct {
		off  uint32
		want uint32
	}{{4 * relstest.PuppetProc, 0x40}, {4 * relstest.SeatagProc, 0x10}} {
		i, k := lf.RelocAt(2, c.off)
		if i == nil || i.Module != relstest.PuppetModule || i.Relocs[k].Addend != c.want || i.Relocs[k].TargetSection != 2 {
			t.Errorf("entry +0x%x: %+v", c.off, i)
		}
	}
	if r, _ := lf.SoleReloc(relstest.OtherModule); r == nil || r.Offset != 4*relstest.OtherProc || r.Addend != 0x20 {
		t.Errorf("module 0x42's entry %+v", r)
	}
	if again, err := PatchRELSArcProfiles(out, relstest.PuppetModule, newREL, main, body, RELSArcMaxGrowth); err != nil || !bytes.Equal(again, out) {
		t.Errorf("not a fixed point: %v", err)
	}

	for name, c := range map[string]struct {
		main  ProfileTarget
		extra []ProfileTarget
		want  string
	}{
		"proc 0":                {main, []ProfileTarget{{Proc: 0, Section: 2}}, "process 0x0 given twice (or 0)"},
		"twice":                 {main, []ProfileTarget{body[0], body[0]}, "process 0x27 given twice"},
		"no entry":              {main, []ProfileTarget{{Proc: 0x30, Section: 2}}, "no relocation at process 0x30's entry (section 2 offset 0xc0)"},
		"body past section":     {main, []ProfileTarget{{Proc: relstest.SeatagProc, Section: 2, Offset: 0x80}}, "profile at 0x80 is past the end"},
		"main not at its entry": {ProfileTarget{Proc: 0x30, Section: 2}, nil, "0 relocations against module 0x58 at process 0x30's entry"},
	} {
		if _, err := PatchRELSArcProfiles(arc, relstest.PuppetModule, newREL, c.main, c.extra, RELSArcMaxGrowth); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
