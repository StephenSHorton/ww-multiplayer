package rels_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// stubSeaHeapFix is rels.SeaHeapFix for relstest.LodBg: the same writes
// and checks, with the stand-in's hash as the vanilla one.
func stubSeaHeapFix() rels.BytePatch {
	p := rels.SeaHeapFix
	h := sha1.Sum(relstest.LodBg())
	p.VanillaSHA1 = hex.EncodeToString(h[:])
	return p
}

func readArcFile(t *testing.T, arc []byte, path string) []byte {
	t.Helper()
	a, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The vanilla words at the decomp's call sites (.text offsets), as the
// disc's d_a_lod_bg.rel holds them: li r5, 0 before the bl
// fopAcM_entrySolidHeap, and deleteModelData's six bl JKRHeap::free,
// unlinked (branches to _unresolved at .text 0x58). Written out here,
// not computed, so a wrong encoder in the fix or the stand-in shows.
var lodBgVanillaWords = map[uint32]uint32{
	0xCE8: 0x38A00000,
	0xCEC: 0x4BFFF36D,
	0x394: 0x4BFFFCC5, 0x3B0: 0x4BFFFCA9, 0x3CC: 0x4BFFFC8D,
	0x3EC: 0x4BFFFC6D, 0x408: 0x4BFFFC51, 0x424: 0x4BFFFC35,
}

func word(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// checkSeaHeapApplied: the archive's d_a_lod_bg.rel is the stand-in with
// li r5, 0xEF0 and the two nops written, the relocations of those two
// bls disabled and every other byte as it was, and every other file's
// stored bytes are the vanilla archive's.
func checkSeaHeapApplied(t *testing.T, vanilla, arc []byte) {
	t.Helper()
	was, got := relstest.LodBg(), readArcFile(t, arc, relstest.LodBgFile)
	f, err := rel.Parse(was)
	if err != nil {
		t.Fatal(err)
	}
	g, err := rel.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	text := g.Sections[1].Data
	// The file offset betterww patches, through the stand-in's own
	// 19-entry table (.text at 0xE4), is .text 0xCE8.
	if w := got[0xDCC:0xDD0]; !bytes.Equal(w, []byte{0x38, 0xA0, 0x0E, 0xF0}) {
		t.Errorf("file 0xDCC holds %x, want li r5, 0xEF0", w)
	}
	if w := text[0xCE8:0xCEC]; !bytes.Equal(w, []byte{0x38, 0xA0, 0x0E, 0xF0}) {
		t.Errorf(".text 0xCE8 holds %x, want li r5, 0xEF0", w)
	}
	if w := word(text[0xCEC:]); w != lodBgVanillaWords[0xCEC] {
		t.Errorf("the bl fopAcM_entrySolidHeap is %08x, want %08x", w, lodBgVanillaWords[0xCEC])
	}
	for i, off := range relstest.LodBgFrees {
		want, live := lodBgVanillaWords[off], true
		if i == 0 || i == 3 {
			want, live = 0x60000000, false
		}
		if w := word(text[off:]); w != want {
			t.Errorf("free call %d at 0x%x holds %08x, want %08x", i, off, w, want)
		}
		if _, idx := g.RelocAt(1, off); (idx >= 0) != live {
			t.Errorf("free call %d at 0x%x: relocation live %v, want %v", i, off, idx >= 0, live)
		}
	}
	for _, off := range []uint32{0xCE2, 0xCE6, 0xCEC} {
		if _, idx := g.RelocAt(1, off); idx < 0 {
			t.Errorf("the relocation at .text 0x%x is gone", off)
		}
	}
	n := 0
	for i := range was {
		if was[i] != got[i] {
			n++
		}
	}
	// 2 bytes of the li's immediate, 2 x 4 of the nops (4bfffcc5 and
	// 4bfffc6d to 60000000), 2 relocation type bytes.
	if len(got) != len(was) || n != 2+8+2 {
		t.Errorf("%d bytes differ (sizes %d, %d), want 12", n, len(got), len(was))
	}
	if f.Sections[1].Offset != g.Sections[1].Offset {
		t.Error(".text moved")
	}

	va, err := stage.ParseArchive(vanilla)
	if err != nil {
		t.Fatal(err)
	}
	pa, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	for _, vf := range va.Files() {
		if vf.Path == relstest.LodBgFile || vf.Path == relstest.PuppetFile || vf.Path == "mmem/f_pc_profile_lst.rel" {
			continue
		}
		pf, ok := pa.File(vf.Path)
		if !ok {
			t.Fatalf("%s missing", vf.Path)
		}
		if !bytes.Equal(storedOf(t, vanilla, vf), storedOf(t, arc, pf)) {
			t.Errorf("%s changed", vf.Path)
		}
	}
}

// storedOf is f's stored bytes in arc: the bytes the archive holds.
func storedOf(t *testing.T, arc []byte, f stage.ArchiveFile) []byte {
	t.Helper()
	a, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Edit()
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The fix on its own (a build without a REL module): applied once, then
// found applied on its own output, which comes back byte for byte.
func TestSeaHeapFixPatchesOnly(t *testing.T) {
	vanilla := relstest.ArcWithLodBg()
	p := stubSeaHeapFix()
	arc, applied, err := rels.PatchesOnly(vanilla, []rels.BytePatch{p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != p.Name {
		t.Errorf("applied %q", applied)
	}
	checkSeaHeapApplied(t, vanilla, arc)
	again, applied, err := rels.PatchesOnly(arc, []rels.BytePatch{p}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, arc) || len(applied) != 1 {
		t.Errorf("patching the output again: same %v, applied %q", bytes.Equal(again, arc), applied)
	}
	if _, _, err := rels.PatchesOnly(vanilla, nil, 0); err != nil {
		t.Errorf("no patches: %v", err)
	}
}

// With this build's module: the module and the fix both go in, and the
// archive built again from the output is the output (patch's
// ErrAlreadyPatched rests on it).
func TestSeaHeapFixWithModule(t *testing.T) {
	m := blobModule(t)
	m.Patches = []rels.BytePatch{stubSeaHeapFix()}
	vanilla := relstest.ArcWithLodBg()
	b, err := m.Build(vanilla)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Patches) != 1 {
		t.Errorf("Built.Patches %q", b.Patches)
	}
	checkSeaHeapApplied(t, vanilla, b.Arc)
	checkModuleInArc(t, b.Arc, m)
	again, err := m.Build(b.Arc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Arc, b.Arc) {
		t.Error("building from the output is not a fixed point")
	}
}

// What the fix refuses: a REL that isn't the vanilla one, one whose
// call sites aren't where the fix was reasoned from, an archive without
// the REL, and a patch on a file the mod rewrites.
func TestSeaHeapFixRejects(t *testing.T) {
	arcWith := func(lod []byte) []byte {
		return stagetest.RARC("rels", append(relstest.Files(), stagetest.File{Path: relstest.LodBgFile, Data: lod, Yaz0: true})...)
	}
	t.Run("not vanilla", func(t *testing.T) {
		lod := relstest.LodBg()
		lod[len(lod)-9] ^= 1 // a relocation's addend byte: no write is touched
		_, _, err := rels.PatchesOnly(arcWith(lod), []rels.BytePatch{stubSeaHeapFix()}, 0)
		if err == nil || !strings.Contains(err.Error(), "not the GZLE01 vanilla") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("the real hash on the stand-in", func(t *testing.T) {
		_, _, err := rels.PatchesOnly(relstest.ArcWithLodBg(), []rels.BytePatch{rels.SeaHeapFix}, 0)
		if err == nil || !strings.Contains(err.Error(), "not the GZLE01 vanilla") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("call sites moved", func(t *testing.T) {
		f, err := rel.Parse(relstest.LodBg())
		if err != nil {
			t.Fatal(err)
		}
		imp := f.Import(0)
		imp.Relocs = slices.DeleteFunc(imp.Relocs, func(r rel.Reloc) bool { return r.Offset == relstest.LodBgFrees[5] && r.Section == 1 })
		lod, err := f.Bytes(false)
		if err != nil {
			t.Fatal(err)
		}
		p := rels.SeaHeapFix
		h := sha1.Sum(lod)
		p.VanillaSHA1 = hex.EncodeToString(h[:])
		_, _, err = rels.PatchesOnly(arcWith(lod), []rels.BytePatch{p}, 0)
		if err == nil || !strings.Contains(err.Error(), "JKRHeap::free") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no such REL", func(t *testing.T) {
		_, _, err := rels.PatchesOnly(relstest.Arc(), []rels.BytePatch{stubSeaHeapFix()}, 0)
		if err == nil || !strings.Contains(err.Error(), "no mmem/d_a_lod_bg.rel") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("two patches on one word", func(t *testing.T) {
		p := stubSeaHeapFix()
		q := p
		q.Name = "another"
		_, _, err := rels.PatchesOnly(relstest.ArcWithLodBg(), []rels.BytePatch{p, q}, 0)
		if err == nil || !strings.Contains(err.Error(), "write the same bytes") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("on the mod's own REL", func(t *testing.T) {
		m := blobModule(t)
		p := stubSeaHeapFix()
		p.File = relstest.PuppetFile
		m.Patches = []rels.BytePatch{p}
		_, err := m.Build(relstest.ArcWithLodBg())
		if err == nil || !strings.Contains(err.Error(), "write the same bytes") {
			t.Fatalf("err %v", err)
		}
	})
}
