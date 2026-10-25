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
	if _, err := Run(context.Background(), plain.Out, dir+"/late.iso", &strings.Builder{}, o); !errors.Is(err, inject.ErrPatchedByOtherVersion) {
		t.Errorf("the fix over a disc patched without it: %v", err)
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
