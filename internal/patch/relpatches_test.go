package patch

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// lodBgISO is a patchable disc whose RELS.arc has relstest's stand-in
// d_a_lod_bg.rel, and the sea heap fix with that stand-in's hash.
func lodBgISO() ([]byte, rels.BytePatch) {
	d := tablestest.Disc(tablestest.Vanilla())
	d.Add("RELS.arc", relstest.ArcWithLodBg())
	img := d.ISO(append(tablestest.PatchableDOL(), make([]byte, len(inject.T2Bytes)+0x100)...))
	p := rels.SeaHeapFix
	h := sha1.Sum(relstest.LodBg())
	p.VanillaSHA1 = hex.EncodeToString(h[:])
	return img, p
}

func lodBgOn(t *testing.T, image string) []byte {
	t.Helper()
	img, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	arc, err := inject.RELSArcFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	a, err := stage.ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.ReadFile(relstest.LodBgFile)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Run with a REL patch: the line says so, the disc's d_a_lod_bg.rel is
// changed, and the output patched again with it is AlreadyPatched.
// Without it nothing changes there and no fix line is written. A disc
// patched without the fix and patched again with it is "another
// version": the archive differs from the one this run would write.
func TestRunRELPatches(t *testing.T) {
	dir := t.TempDir()
	img, fix := lodBgISO()
	in := writeImage(t, dir, "ww.iso", img)
	o := Options{RELPatches: []rels.BytePatch{fix}}

	var w strings.Builder
	res, err := Run(context.Background(), in, dir+"/fixed.iso", &w, o)
	if err != nil {
		t.Fatalf("%v\n%s", err, w.String())
	}
	if !strings.Contains(w.String(), "  fix   : sea heap fix (local only)\n") {
		t.Errorf("no fix line:\n%s", w.String())
	}
	if len(res.REL.Patches) != 1 {
		t.Errorf("Result.REL.Patches %q", res.REL.Patches)
	}
	if bytes.Equal(lodBgOn(t, res.Out), relstest.LodBg()) {
		t.Error("d_a_lod_bg.rel is unchanged")
	}
	again, err := Run(context.Background(), res.Out, dir+"/again.iso", &strings.Builder{}, o)
	if err != nil || !again.AlreadyPatched {
		t.Errorf("patching the fixed disc again: %v, already %v", err, again.AlreadyPatched)
	}

	w.Reset()
	plain, err := Run(context.Background(), in, dir+"/plain.iso", &w, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.String(), "fix   :") || !bytes.Equal(lodBgOn(t, plain.Out), relstest.LodBg()) {
		t.Errorf("without the option the REL changed, or a fix line:\n%s", w.String())
	}
	// The message says the fix may be why, not only "another version".
	w.Reset()
	if _, err := Run(context.Background(), plain.Out, dir+"/late.iso", &w, o); !errors.Is(err, inject.ErrPatchedByOtherVersion) {
		t.Errorf("the fix over a disc patched without it: %v", err)
	}
	if !strings.Contains(w.String(), "If this disc was patched by this version without --fix-sea-heap: the sea heap fix can't be added to a patched disc. Patch your vanilla disc with --fix-sea-heap.\n") {
		t.Errorf("no hint at the flag:\n%s", w.String())
	}
	// The other way round the disc is this build's: the fix stays.
	if r, err := Run(context.Background(), res.Out, dir+"/kept.iso", &strings.Builder{}, Options{}); err != nil || !r.AlreadyPatched {
		t.Errorf("a fixed disc patched without the option: %v, already %v", err, r.AlreadyPatched)
	}
}

// A disc whose d_a_lod_bg.rel isn't the one the fix knows fails the
// patch, with nothing written.
func TestRunRELPatchRefused(t *testing.T) {
	dir := t.TempDir()
	img, _ := lodBgISO()
	in := writeImage(t, dir, "ww.iso", img)
	out := dir + "/out.iso"
	var w strings.Builder
	_, err := Run(context.Background(), in, out, &w, Options{RELPatches: []rels.BytePatch{rels.SeaHeapFix}})
	if err == nil || !strings.Contains(w.String(), "not the GZLE01 vanilla REL") {
		t.Fatalf("err %v\n%s", err, w.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an output was written")
	}
}

// useFixes makes fix (the stand-in's) the one fix Run looks for on the
// discs it replaces.
func useFixes(t *testing.T, fix rels.BytePatch) {
	old := knownFixes
	knownFixes = []rels.BytePatch{fix}
	t.Cleanup(func() { knownFixes = old })
}

// A re-patch from the vanilla disc over a disc that has an optional fix
// keeps it with KeepFixes (the app's re-patch after an update), from out
// or from KeepFrom, and says so; without KeepFixes (the CLI) it leaves
// it off with a note naming the flag. A disc without the fix adds
// nothing, and the vanilla input is never taken for one.
func TestRunKeepsFixes(t *testing.T) {
	dir := t.TempDir()
	img, fix := lodBgISO()
	useFixes(t, fix)
	in := writeImage(t, dir, "ww.iso", img)
	fixed := func(path string) bool { return !bytes.Equal(lodBgOn(t, path), relstest.LodBg()) }
	out := dir + "/ww-multiplayer.iso"
	if _, err := Run(context.Background(), in, out, &strings.Builder{}, Options{RELPatches: []rels.BytePatch{fix}}); err != nil || !fixed(out) {
		t.Fatalf("the CLI's fixed disc: %v", err)
	}
	if on, err := FixesOn(out); err != nil || len(on) != 1 {
		t.Fatalf("FixesOn the fixed disc: %d, %v", len(on), err)
	}
	if on, err := FixesOn(in); err != nil || len(on) != 0 {
		t.Fatalf("FixesOn the vanilla disc: %d, %v", len(on), err)
	}

	// The app re-patches the vanilla disc over it.
	var w strings.Builder
	if _, err := Run(context.Background(), in, out, &w, Options{KeepFixes: true}); err != nil {
		t.Fatalf("%v\n%s", err, w.String())
	}
	if !fixed(out) || !strings.Contains(w.String(), "  keep  : sea heap fix, which "+out+" has\n") || !strings.Contains(w.String(), "  fix   : sea heap fix (local only)\n") {
		t.Errorf("the app's re-patch dropped the fix:\n%s", w.String())
	}

	// The CLI without the flag: left off, with a note.
	w.Reset()
	if _, err := Run(context.Background(), in, out, &w, Options{}); err != nil {
		t.Fatal(err)
	}
	if fixed(out) || !strings.Contains(w.String(), "  note  : "+out+" has the sea heap fix; this patch leaves it off (add --fix-sea-heap to keep it)\n") {
		t.Errorf("the CLI's re-patch without the flag:\n%s", w.String())
	}

	// out has no fix now: nothing to keep, no line.
	w.Reset()
	if _, err := Run(context.Background(), in, out, &w, Options{KeepFixes: true}); err != nil {
		t.Fatal(err)
	}
	if fixed(out) || strings.Contains(w.String(), "keep  :") || strings.Contains(w.String(), "fix   :") {
		t.Errorf("a fix appeared from nowhere:\n%s", w.String())
	}

	// The fix on the last patched disc (KeepFrom), a new out.
	last := dir + "/cli-fixed.iso"
	if _, err := Run(context.Background(), in, last, &strings.Builder{}, Options{RELPatches: []rels.BytePatch{fix}}); err != nil {
		t.Fatal(err)
	}
	other := dir + "/new-multiplayer.iso"
	w.Reset()
	if _, err := Run(context.Background(), in, other, &w, Options{KeepFixes: true, KeepFrom: []string{last, dir + "/missing.iso"}}); err != nil {
		t.Fatalf("%v\n%s", err, w.String())
	}
	if !fixed(other) || strings.Count(w.String(), "keep  :") != 1 {
		t.Errorf("the fix from KeepFrom:\n%s", w.String())
	}
}

// An RVZ converted and given the fix in one run: the ISO DolphinTool
// wrote is the one patched, so the output has the mod and the fixed
// d_a_lod_bg.rel, the fix line follows the convert lines, and no
// temporary ISO is left.
func TestRunConvertsWithRELPatches(t *testing.T) {
	dir := t.TempDir()
	img, fix := lodBgISO()
	in := writeImage(t, dir, "ww.rvz", rvzImage())
	f := &fakeDolphinTool{iso: img}
	var w strings.Builder
	o := Options{Converter: fakeConverter(f, ConvertNeed), RELPatches: []rels.BytePatch{fix}}
	res, err := Run(context.Background(), in, "", &w, o)
	if err != nil || res.ConvertedFrom != "RVZ" || res.AlreadyPatched {
		t.Fatalf("Run: %+v, %v\n%s", res, err, w.String())
	}
	if len(f.outs) != 1 {
		t.Errorf("converted to %q", f.outs)
	}
	noTemps(t, dir)
	if st, err := Inspect(res.Out); err != nil || st != Patched {
		t.Errorf("output: %v %v", st, err)
	}
	if len(res.REL.Patches) != 1 || bytes.Equal(lodBgOn(t, res.Out), relstest.LodBg()) {
		t.Errorf("the fix is not on the converted disc: %q", res.REL.Patches)
	}
	s := w.String()
	c, fx := strings.Index(s, "  convert: done"), strings.Index(s, "  fix   : sea heap fix (local only)\n")
	if c < 0 || fx < 0 || fx < c {
		t.Errorf("convert then fix lines missing or out of order:\n%s", s)
	}
}
