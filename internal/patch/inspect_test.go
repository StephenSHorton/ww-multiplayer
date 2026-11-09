package patch

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	if s, err := Inspect(in); err != nil || s != Vanilla {
		t.Fatalf("vanilla: %v, %v", s, err)
	}
	res, err := Run(context.Background(), in, "", io.Discard, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Inspect(res.Out); err != nil || s != Patched {
		t.Fatalf("patched: %v, %v", s, err)
	}
	img, _ := os.ReadFile(res.Out)
	img[bytes.Index(img, inject.T2Bytes)+len(inject.T2Bytes)-1] ^= 1
	other := writeImage(t, dir, "other.iso", img)
	if s, err := Inspect(other); err != nil || s != OtherVersion {
		t.Fatalf("other version: %v, %v", s, err)
	}
	junk := writeImage(t, dir, "junk.iso", make([]byte, 4096))
	if _, err := Inspect(junk); err == nil {
		t.Error("a non-disc file inspected fine")
	}
	if _, err := Inspect(filepath.Join(dir, "none.iso")); err == nil {
		t.Error("a missing file inspected fine")
	}
	if Patched.String() != "patched" || OtherVersion.String() == "" {
		t.Error("DiscState names")
	}
}

// A disc whose DOL is this build's but whose RELS.arc is another
// build's (a REL-only release) reads Patched by Inspect and
// OtherVersion by InspectFull, and so through every caller.
func TestInspectFullRELOnly(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	if s, err := InspectFull(in); err != nil || s != Vanilla {
		t.Fatalf("vanilla: %v, %v", s, err)
	}
	res, err := Run(context.Background(), in, "", io.Discard, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := InspectFull(res.Out); err != nil || s != Patched {
		t.Fatalf("patched: %v, %v", s, err)
	}
	img, _ := os.ReadFile(res.Out)
	d, err := inject.OpenDisc(res.Out)
	if err != nil {
		t.Fatal(err)
	}
	arc, err := d.ReadFile("RELS.arc")
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(img, arc)
	if at < 0 {
		t.Fatal("RELS.arc not found in the image")
	}
	img[at+len(arc)-1] ^= 1 // the last byte: inside the archive, DOL untouched
	stale := writeImage(t, dir, "stale.iso", img)
	if s, err := Inspect(stale); err != nil || s != Patched {
		t.Fatalf("DOL-only check: %v, %v", s, err)
	}
	if s, err := InspectFull(stale); err != nil || s != OtherVersion {
		t.Fatalf("full check: %v, %v", s, err)
	}
}
