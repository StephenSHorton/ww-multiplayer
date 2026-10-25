package rels_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// The stand-in has the disc REL's layout, read off the disc's
// d_a_lod_bg.rel header (19 section entries; .text at 0xE4, .ctors
// 0x1420, .dtors 0x1424, .rodata 0x1428, .data 0x1608, a .bss of 0x20)
// and the disc's words at the call sites (lodBgVanillaWords). The first
// version of the fix assumed 17 entries (.text at 0xD4) and passed
// against a stand-in built on the same assumption.
func TestLodBgStandInLayout(t *testing.T) {
	f := mustParse(t, relstest.LodBg())
	if len(f.Sections) != 19 {
		t.Fatalf("%d section entries, want 19", len(f.Sections))
	}
	for i, want := range map[int]uint32{1: 0xE4, 2: 0x1420, 3: 0x1424, 4: 0x1428, 5: 0x1608} {
		if f.Sections[i].Offset != want {
			t.Errorf("section %d at file 0x%x, want 0x%x", i, f.Sections[i].Offset, want)
		}
	}
	if !f.Sections[6].BSS() || f.BSSSize != 0x20 {
		t.Errorf("section 6 bss %v, size 0x%x", f.Sections[6].BSS(), f.BSSSize)
	}
	for off, want := range lodBgVanillaWords {
		if w := word(f.Sections[1].Data[off:]); w != want {
			t.Errorf(".text 0x%x holds %08x, want %08x", off, w, want)
		}
	}
}

// SeaHeapFix's writes are betterww's: file offsets 0xDCC, 0x478 and
// 0x4D0 (fix_vanilla_bugs.asm's .org in d_a_lod_bg.rel), with the
// disc's own words there as Old, each its own.
func TestSeaHeapFixWritesAreBetterwws(t *testing.T) {
	want := []rels.FileWrite{
		{At: 0xDCC, Old: []byte{0x38, 0xA0, 0x00, 0x00}, New: []byte{0x38, 0xA0, 0x0E, 0xF0}},
		{At: 0x478, Old: []byte{0x4B, 0xFF, 0xFC, 0xC5}, New: []byte{0x60, 0, 0, 0}},
		{At: 0x4D0, Old: []byte{0x4B, 0xFF, 0xFC, 0x6D}, New: []byte{0x60, 0, 0, 0}},
	}
	got := rels.SeaHeapFix.Writes
	if len(got) != len(want) {
		t.Fatalf("%d writes", len(got))
	}
	for i := range want {
		if got[i].At != want[i].At || !bytes.Equal(got[i].Old, want[i].Old) || !bytes.Equal(got[i].New, want[i].New) {
			t.Errorf("write %d: at 0x%x %x -> %x, want at 0x%x %x -> %x", i, got[i].At, got[i].Old, got[i].New, want[i].At, want[i].Old, want[i].New)
		}
	}
}

// A REL with the right .text but 17 section entries (.text at 0xD4,
// the first version's assumption) is refused: the checks say where the
// writes landed, and without the checks the Old bytes refuse it, so
// nothing is written into the wrong instructions.
func TestSeaHeapFixLayoutMismatch(t *testing.T) {
	lod := relstest.LodBgLayout(17)
	if f := mustParse(t, lod); f.Sections[1].Offset != 0xD4 {
		t.Fatalf(".text at 0x%x, want 0xD4", f.Sections[1].Offset)
	}
	arc := stagetest.RARC("rels", append(relstest.Files(), stagetest.File{Path: relstest.LodBgFile, Data: lod, Yaz0: true})...)
	p := rels.SeaHeapFix
	h := sha1.Sum(lod)
	p.VanillaSHA1 = hex.EncodeToString(h[:])
	_, _, err := rels.PatchesOnly(arc, []rels.BytePatch{p}, 0)
	if err == nil || !strings.Contains(err.Error(), "17 section entries") || !strings.Contains(err.Error(), ".text 0xce8") {
		t.Fatalf("with the checks: %v", err)
	}
	p.Check = nil
	_, _, err = rels.PatchesOnly(arc, []rels.BytePatch{p}, 0)
	if err == nil || !strings.Contains(err.Error(), "neither the vanilla") {
		t.Fatalf("without the checks: %v", err)
	}
	// The 19-entry stand-in, the same way, passes.
	if _, _, err := rels.PatchesOnly(relstest.ArcWithLodBg(), []rels.BytePatch{stubSeaHeapFix()}, 0); err != nil {
		t.Fatal(err)
	}
}

// A write whose file offset is in no section (the header, the section
// table, the relocations) is refused before anything is written.
func TestSeaHeapFixOffsetOutsideSections(t *testing.T) {
	p := stubSeaHeapFix()
	p.Writes = []rels.FileWrite{{At: 0x50, Old: []byte{0, 0, 0, 0}, New: []byte{1, 1, 1, 1}}}
	p.Check = nil
	_, _, err := rels.PatchesOnly(relstest.ArcWithLodBg(), []rels.BytePatch{p}, 0)
	if err == nil || !strings.Contains(err.Error(), "in no section with data") {
		t.Fatalf("err %v", err)
	}
}
