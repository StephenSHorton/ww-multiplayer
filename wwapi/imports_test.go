package wwapi_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWwapiImportsNoMods: no wwapi package depends on a mod, on any target
// OS. Mods build on wwapi (docs/api/00-design.md §4), never the other way
// round; a type both need (texture.Color) lives in wwapi. Test files may
// still import mods (the runtime tests use mods/_fixtures), so this walks
// the non-test dependency graph only.
func TestWwapiImportsNoMods(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(runtime.GOROOT(), "bin", "go")
		if _, err := os.Stat(goBin); err != nil {
			t.Skip("no go tool")
		}
	}
	// go test caches on the files the test itself touches, not go list's,
	// so stat every Go source in the module: an import change anywhere
	// reruns this test.
	if err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != ".." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".go") || d.Name() == "go.mod") {
			_, err = os.Stat(p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	const mods = "github.com/StephenSHorton/ww-multiplayer/mods"
	for _, goos := range []string{"windows", "linux", "darwin"} {
		cmd := exec.Command(goBin, "list", "-deps",
			"-f", "{{.ImportPath}}{{range .Imports}} {{.}}{{end}}", "./wwapi/...")
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOFLAGS=")
		out, err := cmd.Output()
		if err != nil {
			msg := ""
			if ee, ok := err.(*exec.ExitError); ok {
				msg = string(ee.Stderr)
			}
			t.Fatalf("GOOS=%s go list: %v\n%s", goos, err, msg)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) < 10 {
			t.Fatalf("GOOS=%s: go list returned %d packages", goos, len(lines))
		}
		for _, line := range lines {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if f[0] == mods || strings.HasPrefix(f[0], mods+"/") {
				t.Errorf("GOOS=%s: wwapi depends on %s", goos, f[0])
			}
			for _, imp := range f[1:] {
				if imp == mods || strings.HasPrefix(imp, mods+"/") {
					t.Errorf("GOOS=%s: %s imports %s", goos, f[0], imp)
				}
			}
		}
	}
}
