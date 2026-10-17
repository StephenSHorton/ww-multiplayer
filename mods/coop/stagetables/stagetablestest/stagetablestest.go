// Package stagetablestest is what tests outside mods/coop/stagetables
// share to load stage tables: a cache directory of their own and disc
// images on disk.
package stagetablestest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/stagetables"
)

// UseDir points stagetables.CacheDir at dir for the test (an error for
// dir "").
func UseDir(t *testing.T, dir string) {
	t.Helper()
	saved := stagetables.CacheDir
	t.Cleanup(func() { stagetables.CacheDir = saved })
	stagetables.CacheDir = func() (string, error) {
		if dir == "" {
			return "", errors.New("tables: no config directory for the cache: $HOME is not defined")
		}
		return dir, nil
	}
}

// WriteImage writes img to a new file and returns its path.
func WriteImage(t *testing.T, name string, img []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
