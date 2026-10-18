// Package vanillatest gives tests the user's own vanilla Wind Waker disc
// (docs/TESTING.md, "Live-disc tests"). No Nintendo data is in the repo:
// a test that needs the real thing (the RELs in RELS.arc, say) asks here
// and is skipped when there is no disc, so `go test ./...` passes on any
// machine. The disc is read, never written.
package vanillatest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// EnvVar names the disc image (ISO or CISO) to use. When it is unset, the
// conventional places are tried: the vanilla image under the two-Dolphin
// harness's Roms directory (CLAUDE.md, ISO_PATH's default) and ~/Roms.
const EnvVar = "WW_VANILLA_DISC"

// RELSArc is the disc's RELS.arc, the archive of every REL module.
const RELSArc = "RELS.arc"

// Path returns the path of the vanilla disc image to use, or "" when
// there is none.
func Path() string {
	if p := os.Getenv(EnvVar); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	const name = "Legend of Zelda, The - The Wind Waker (USA, Canada)"
	for _, dir := range []string{filepath.Join(home, "Desktop", "Dolphin-x64", "Roms"), filepath.Join(home, "Roms")} {
		for _, ext := range []string{".ciso", ".iso"} {
			p := filepath.Join(dir, name+ext)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// Open opens the vanilla disc, or skips t when there is none or it
// can't be read. The disc is closed when t ends.
func Open(t testing.TB) *inject.Disc {
	t.Helper()
	p := Path()
	if p == "" {
		t.Skipf("no vanilla disc: set %s to the ISO or CISO", EnvVar)
	}
	d, err := inject.OpenDisc(p)
	if err != nil {
		t.Skipf("vanilla disc %s: %v", p, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// ReadFile returns the file at path on the vanilla disc ("RELS.arc",
// "res/Stage/sea/Stage.arc"), or skips t when there is no disc. A disc
// without the file fails t.
func ReadFile(t testing.TB, path string) []byte {
	t.Helper()
	d := Open(t)
	b, err := d.ReadFile(path)
	if err != nil {
		t.Fatalf("vanilla disc: %v", err)
	}
	return b
}
