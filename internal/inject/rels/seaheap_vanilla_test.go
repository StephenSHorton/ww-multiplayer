package rels_test

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// TestSeaHeapFixVanilla checks rels.SeaHeapFix against the disc's own
// d_a_lod_bg.rel, which is what no offline test can: the decomp's hash
// matches it (expanded or stored), its checks pass (the li r5 and the
// six JKRHeap::free calls where the fix was reasoned from), the vanilla
// words are the Old the writes expect (each bl unlinked, a branch to
// _unresolved, so each its own word), and the patched archive changes
// that one file only, with exactly the two free calls' relocations
// disabled. Patching the result again gives it back. It also holds the
// offline stand-in (relstest.LodBg) to the disc: the same section table
// (entries, file offsets, sizes) and the same words at the call sites,
// so the offline tests can't drift back to a layout of their own.
func TestSeaHeapFixVanilla(t *testing.T) {
	arc := vanillatest.ReadFile(t, vanillatest.RELSArc)
	a, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := a.File(rels.SeaHeapFix.File)
	if !ok {
		t.Fatalf("no %s in the disc's RELS.arc", rels.SeaHeapFix.File)
	}
	raw, err := a.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	h := sha1.Sum(raw)
	hs := sha1.Sum(stage.EncodeYaz0(raw))
	t.Logf("%s: SHA-1 %s expanded, %s stored; .text at file 0x%x", f.Path, hex.EncodeToString(h[:]), hex.EncodeToString(hs[:]), mustParse(t, raw).Sections[1].Offset)
	if hex.EncodeToString(h[:]) != rels.SeaHeapFix.VanillaSHA1 && hex.EncodeToString(hs[:]) != rels.SeaHeapFix.VanillaSHA1 {
		t.Errorf("neither hash is the decomp's %s", rels.SeaHeapFix.VanillaSHA1)
	}

	disc, stand := mustParse(t, raw), mustParse(t, relstest.LodBg())
	if len(disc.Sections) != len(stand.Sections) {
		t.Errorf("the disc's REL has %d section entries, the stand-in %d", len(disc.Sections), len(stand.Sections))
	} else {
		for i := range disc.Sections {
			d, s := disc.Sections[i], stand.Sections[i]
			if d.Offset != s.Offset || d.Exec != s.Exec || len(d.Data) != len(s.Data) || d.Size != s.Size {
				t.Errorf("section %d: disc at 0x%x (%d bytes, size 0x%x, exec %v), stand-in at 0x%x (%d bytes, size 0x%x, exec %v)",
					i, d.Offset, len(d.Data), d.Size, d.Exec, s.Offset, len(s.Data), s.Size, s.Exec)
			}
		}
	}
	if disc.Unresolved != stand.Unresolved {
		t.Errorf("_unresolved: disc %+v, stand-in %+v", disc.Unresolved, stand.Unresolved)
	}
	for off, want := range lodBgVanillaWords {
		if w := word(disc.Sections[1].Data[off:]); w != want {
			t.Errorf("disc .text 0x%x holds %08x, the tests expect %08x", off, w, want)
		}
	}
	for i, w := range rels.SeaHeapFix.Writes {
		if got := raw[w.At : w.At+uint32(len(w.Old))]; !bytes.Equal(got, w.Old) {
			t.Errorf("write %d: disc file 0x%x holds %x, Old is %x", i, w.At, got, w.Old)
		}
	}

	out, applied, err := rels.PatchesOnly(arc, []rels.BytePatch{rels.SeaHeapFix}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Errorf("applied %q", applied)
	}
	b, err := stage.ParseArchive(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, vf := range a.Files() {
		pf, _ := b.File(vf.Path)
		x, _ := a.Read(vf)
		y, err := b.Read(pf)
		if err != nil {
			t.Fatalf("%s: %v", vf.Path, err)
		}
		if vf.Path == f.Path {
			before, after := mustParse(t, x), mustParse(t, y)
			disabled := 0
			for i, imp := range before.Imports {
				for j, r := range imp.Relocs {
					if r.Type != rel.RelocNop && after.Imports[i].Relocs[j].Type == rel.RelocNop {
						disabled++
					}
				}
			}
			if disabled != 2 {
				t.Errorf("%d relocations disabled, want 2", disabled)
			}
			continue
		}
		if !bytes.Equal(x, y) {
			t.Errorf("%s changed", vf.Path)
		}
	}
	again, _, err := rels.PatchesOnly(out, []rels.BytePatch{rels.SeaHeapFix}, 0)
	if err != nil || !bytes.Equal(again, out) {
		t.Errorf("patching the output again: %v, same %v", err, bytes.Equal(again, out))
	}
}

func mustParse(t *testing.T, b []byte) *rel.File {
	t.Helper()
	f, err := rel.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
