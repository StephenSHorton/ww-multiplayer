package dolphinenv

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// BootstrapUserDir copies the user directory src to dst for a second
// Dolphin, skipping Cache/: Dolphin 1 holds Windows file locks on
// Cache/Shaders/*.cache and GZLE01.uidcache while it's running, which
// would fail the copy. Cache regenerates on first boot of Dolphin 2
// (~10–30s shader recompile) so we pay nothing after that.
func BootstrapUserDir(src, dst string) error {
	return CopyDirSkip(src, dst, []string{"Cache"})
}

// CopyDirSkip recursively copies src into dst, skipping any top-level
// child whose name is in skipNames.
func CopyDirSkip(src, dst string, skipNames []string) error {
	skip := map[string]bool{}
	for _, n := range skipNames {
		skip[n] = true
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		// First path component — used to gate the skip list.
		first := rel
		if i := strings.IndexAny(rel, `\/`); i >= 0 {
			first = rel[:i]
		}
		if skip[first] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return CopyFile(path, target)
	})
}

// CopyFile copies the file src to dst, creating or truncating dst.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}

// EnsureCheatsIni drops ini (the bundled cheats/GZLE01.ini) into
// <userDir>/GameSettings/GZLE01.ini if no GZLE01.ini exists there yet.
// If one already exists, we leave it alone — the user (or a previously
// installed cheat pack) owns it, and silently merging Gecko sections
// would surprise them. The user can manually merge from the repo's
// cheats/GZLE01.ini if they want.
//
// Best-effort: any failure is logged as a warning but does not abort the
// caller — cheats are a convenience, not a requirement. It does nothing
// for an empty ini or userDir.
func EnsureCheatsIni(rep report.Reporter, userDir string, ini []byte) {
	if len(ini) == 0 || userDir == "" {
		return
	}
	dir := filepath.Join(userDir, "GameSettings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		report.Logf(rep, report.Warn, "cheats: mkdir %s: %v", dir, err)
		return
	}
	target := filepath.Join(dir, "GZLE01.ini")
	if _, err := os.Stat(target); err == nil {
		report.Logf(rep, report.Info, "cheats: %s already exists, leaving it untouched (merge from cheats/GZLE01.ini if you want the bundled codes)", target)
		return
	} else if !os.IsNotExist(err) {
		report.Logf(rep, report.Warn, "cheats: stat %s: %v", target, err)
		return
	}
	if err := os.WriteFile(target, ini, 0o644); err != nil {
		report.Logf(rep, report.Warn, "cheats: write %s: %v", target, err)
		return
	}
	report.Logf(rep, report.OK, "cheats: installed bundled GZLE01.ini at %s (enable individual codes via Dolphin → game properties → Gecko Codes)", target)
}
