package patch

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
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
