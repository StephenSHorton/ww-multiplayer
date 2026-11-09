package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

func TestEnsureFreshSave(t *testing.T) {
	u := t.TempDir()
	p, err := EnsureFreshSave(u)
	want := filepath.Join(u, "GC", "USA", "Card A", FreshSaveName)
	if err != nil || p != want {
		t.Fatalf("got %q, %v; want %q", p, err, want)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := savefile.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Header.CheckStat(); err != nil {
		t.Fatal(err)
	}
	// Second call: the file is there, untouched.
	if p2, err := EnsureFreshSave(u); err != nil || p2 != "" {
		t.Fatalf("second call: %q, %v", p2, err)
	}
}

func TestEnsureFreshSaveLeavesCards(t *testing.T) {
	// An existing save of any game in the folder.
	u := t.TempDir()
	d := filepath.Join(u, "GC", "USA", "Card A")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "other.gci"), []byte("x"), 0o644)
	if p, err := EnsureFreshSave(u); err != nil || p != "" {
		t.Fatalf("%q, %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(d, FreshSaveName)); err == nil {
		t.Fatal("wrote beside an existing save")
	}
	// Slot A set to a raw card or to nothing.
	for _, dev := range []string{"1", "255"} {
		u := t.TempDir()
		os.MkdirAll(filepath.Join(u, "Config"), 0o755)
		os.WriteFile(filepath.Join(u, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = "+dev+"\n"), 0o644)
		if p, err := EnsureFreshSave(u); err != nil || p != "" {
			t.Fatalf("SlotA=%s: %q, %v", dev, p, err)
		}
		if _, err := os.Stat(filepath.Join(u, "GC")); err == nil {
			t.Fatalf("SlotA=%s: made GC", dev)
		}
	}
}
