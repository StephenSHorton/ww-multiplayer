package wwapi_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	fixtures "github.com/StephenSHorton/ww-multiplayer/mods/_fixtures"
)

// TestFixturesListed: every folder under mods/_fixtures is in
// fixtures.IDs, and each embedded mod.json is the file on disk.
func TestFixturesListed(t *testing.T) {
	dir := filepath.Join("..", "mods", "_fixtures")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, e := range ents {
		if e.IsDir() {
			onDisk = append(onDisk, e.Name())
		}
	}
	if !slices.Equal(onDisk, fixtures.IDs) {
		t.Fatalf("folders %v, fixtures.IDs %v", onDisk, fixtures.IDs)
	}
	for _, id := range fixtures.IDs {
		b, err := os.ReadFile(filepath.Join(dir, id, "mod.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, fixtures.ManifestJSON(id)) {
			t.Errorf("%s: the embedded mod.json differs from the file", id)
		}
		if m := fixtures.New(id, nil).Manifest(); m.ID != id {
			t.Errorf("%s: manifest id %q", id, m.ID)
		}
	}
}
