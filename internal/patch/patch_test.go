package patch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels/relstest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

func TestDefaultOut(t *testing.T) {
	for in, want := range map[string]string{
		"ww.iso":                    "ww-multiplayer.iso",
		"ww.ciso":                   "ww-multiplayer.iso",
		"dir.v2/ww":                 "dir.v2/ww-multiplayer.iso",
		`C:\Roms.d\Wind Waker.ciso`: `C:\Roms.d\Wind Waker-multiplayer.iso`,
		`C:\Roms.d\noext`:           `C:\Roms.d\noext-multiplayer.iso`,
		"a.b.iso":                   "a.b-multiplayer.iso",
		"":                          "-multiplayer.iso",
	} {
		if got := DefaultOut(in); got != want {
			t.Errorf("DefaultOut(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeImage(t *testing.T, dir, name string, img []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, img, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// tablesSpy stands in for the co-op tables step.
type tablesSpy struct{ images []string }

func (s *tablesSpy) fn(w io.Writer, image string) {
	s.images = append(s.images, image)
	fmt.Fprintf(w, "  co-op : (tables of %s)\n", filepath.Base(image))
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	spy := &tablesSpy{}
	var w bytes.Buffer
	res, err := Run(context.Background(), in, "", &w, Options{PostPatch: []func(io.Writer, string){spy.fn}})
	out := filepath.Join(dir, "ww-multiplayer.iso")
	if err != nil || res.Out != out || res.AlreadyPatched || res.Image() != out {
		t.Fatalf("Run: %+v, %v", res, err)
	}
	// The REL line: this build's module in the disc's RELS.arc (an empty
	// RELELF, a blob generated without devkitPPC, says so instead).
	relLine := "  REL   : none (this build has no REL module)\n"
	if r := res.REL; r.HasREL {
		if r.ModuleID != inject.RELModuleID || r.BuildID != inject.RELBuildID || r.File != relstest.PuppetFile || r.RELSize == 0 || r.Growth <= 0 || !r.RELSArcReplaced() {
			t.Errorf("REL result %+v", r)
		}
		relLine = fmt.Sprintf("  REL   : module 0x%X (d_a_rectangle.rel) -> wwrel build 0x%08X, RELS.arc at 0x%X (+%d bytes)\n", r.ModuleID, r.BuildID, r.RELSArcOffset, r.Growth)
	} else if len(inject.RELELF) != 0 {
		t.Errorf("no REL in the result, but this build has one: %+v", res.REL)
	}
	want := "Wind Waker Multiplayer patcher\n  input : " + in + "\n  output: " + out + "\n" + relLine + "  co-op : (tables of ww-multiplayer.iso)\n\nDone. Boot " + out + " in Dolphin to play multiplayer.\n"
	if w.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", w.String(), want)
	}
	if res.FST != res.REL.Result {
		t.Errorf("FST %+v and REL.Result %+v differ", res.FST, res.REL.Result)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}

	// The output, patched again: nothing written, the tables of that disc.
	w.Reset()
	res, err = Run(context.Background(), out, filepath.Join(dir, "again.iso"), &w, Options{PostPatch: []func(io.Writer, string){spy.fn}})
	if err != nil || !res.AlreadyPatched || res.Image() != out {
		t.Fatalf("already patched: %+v, %v", res, err)
	}
	if !strings.Contains(w.String(), "already contains this version") || !strings.HasSuffix(w.String(), "(tables of ww-multiplayer.iso)\n") {
		t.Errorf("output:\n%s", w.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "again.iso")); !os.IsNotExist(err) {
		t.Errorf("already patched wrote an output: %v", err)
	}
	if len(spy.images) != 2 || spy.images[0] != out || spy.images[1] != out {
		t.Errorf("tables of %q", spy.images)
	}

	// Patched by another build.
	img, _ := os.ReadFile(out)
	img[bytes.Index(img, inject.T2Bytes)+len(inject.T2Bytes)-1] ^= 1
	other := writeImage(t, dir, "other.iso", img)
	w.Reset()
	if _, err := Run(context.Background(), other, "", &w, Options{}); !errors.Is(err, inject.ErrPatchedByOtherVersion) || !strings.Contains(w.String(), "patched by a different ww-multiplayer version") {
		t.Errorf("other version: %v\n%s", err, w.String())
	}

	// Errors are written and returned.
	w.Reset()
	if _, err := Run(context.Background(), in, in, &w, Options{}); !errors.Is(err, inject.ErrOutputIsInput) || !strings.Contains(w.String(), "\nERROR: output is the input file") {
		t.Errorf("out == in: %v\n%s", err, w.String())
	}
	w.Reset()
	if _, err := Run(context.Background(), filepath.Join(dir, "none.iso"), "", &w, Options{}); err == nil || !strings.Contains(w.String(), "\nERROR: read ") {
		t.Errorf("missing input: %v\n%s", err, w.String())
	}

	// A cancelled ctx reads nothing and writes nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Reset()
	if _, err := Run(ctx, in, filepath.Join(dir, "c.iso"), &w, Options{PostPatch: []func(io.Writer, string){spy.fn}}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "c.iso")); !os.IsNotExist(err) || len(spy.images) != 2 {
		t.Errorf("cancelled run wrote: %v, tables %q", err, spy.images)
	}
}

// TestRunPostPatchOrder: the post-patch steps run in order, each once,
// on the disc just written, between its lines and "Done.".
func TestRunPostPatchOrder(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	step := func(name string) func(io.Writer, string) {
		return func(w io.Writer, image string) { fmt.Fprintf(w, "  %s : %s\n", name, filepath.Base(image)) }
	}
	var w bytes.Buffer
	if _, err := Run(context.Background(), in, "", &w, Options{PostPatch: []func(io.Writer, string){step("one"), step("two")}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "  one : ww-multiplayer.iso\n  two : ww-multiplayer.iso\n\nDone. Boot ") {
		t.Errorf("output:\n%s", w.String())
	}
}
