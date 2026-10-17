package main

// patch's co-op line end to end (mods/coop/stagetables.CacheForPatch):
// the test binary runs main's patch as a child.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/stagetables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/stagetables/stagetablestest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// TestPatchHelper's input image, output image and tables directory.
const (
	patchHelperIn  = "WW_TEST_PATCH_IN"
	patchHelperOut = "WW_TEST_PATCH_OUT"
	patchHelperDir = "WW_TEST_PATCH_TABLES"
)

// TestPatchHelper is not a real test: runPatchCmd re-executes the test
// binary with it selected and its variables set, and it runs `patch` as
// main() does, so the tests see its output and exit code.
func TestPatchHelper(t *testing.T) {
	in, ok := os.LookupEnv(patchHelperIn)
	if !ok {
		return
	}
	dir := os.Getenv(patchHelperDir)
	stagetables.CacheDir = func() (string, error) { return dir, nil }
	os.Args = []string{"ww-multiplayer", "patch", in, os.Getenv(patchHelperOut)}
	main()
	os.Exit(0) // keep the testing package's "PASS" off stdout
}

// runPatchCmd runs `ww-multiplayer patch in out` with the co-op tables
// kept in dir, and returns its stdout and exit code.
func runPatchCmd(t *testing.T, in, out, dir string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPatchHelper$")
	cmd.Env = append(os.Environ(), patchHelperIn+"="+in, patchHelperOut+"="+out, patchHelperDir+"="+dir,
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	stdout, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(stdout), 0
	case errors.As(err, &exitErr):
		return string(stdout), exitErr.ExitCode()
	}
	t.Fatalf("running patch: %v", err)
	return "", 0
}

// TestPatchCoopTables runs `patch` end to end on synthetic discs: the
// image it writes is byte for byte what the patcher makes of the input,
// whether or not the co-op tables can be built from it, its output is the
// old output plus one co-op line, and it exits 0 either way. The image is
// recorded either way too. Patching the patched image again records that
// image.
func TestPatchCoopTables(t *testing.T) {
	for _, c := range []struct {
		name   string
		img    []byte
		tables bool
	}{
		{"a disc with stages", tablestest.PatchableISO(tablestest.Vanilla()), true},
		{"a disc without stages", tablestest.PatchableISO(nil), false},
		{"another disc", tablestest.PatchableISO([]stagetest.Stage{tablestest.Dungeon("M_NewD2", keys.TableDRC)}), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			in := stagetablestest.WriteImage(t, "ww.iso", c.img)
			out := filepath.Join(t.TempDir(), "ww-multiplayer.iso")
			want, err := inject.PatchISOInMemory(c.img)
			if err != nil {
				t.Fatal(err)
			}
			stdout, code := runPatchCmd(t, in, out, dir)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, stdout)
			}
			got, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("the patched image differs from PatchISOInMemory's (%d bytes, want %d; %v)", len(got), len(want), err)
			}
			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			co := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  co-op : ") })
			if co < 0 || slices.ContainsFunc(lines[co+1:], func(l string) bool { return strings.HasPrefix(l, "  co-op : ") }) {
				t.Fatalf("not one co-op line:\n%s", stdout)
			}
			old := slices.Delete(slices.Clone(lines), co, co+1)
			wantOld := []string{"Wind Waker Multiplayer patcher", "  input : " + in, "  output: " + out}
			if res := old[3]; strings.HasPrefix(res, "  FST   : moved ") {
				wantOld = append(wantOld, res)
			}
			wantOld = append(wantOld, "", "Done. Boot "+out+" in Dolphin to play multiplayer.")
			if !slices.Equal(old, wantOld) || co != len(wantOld)-2 {
				t.Errorf("output without the co-op line:\n%s\nwant\n%s", strings.Join(old, "\n"), strings.Join(wantOld, "\n"))
			}
			rec, recErr := tables.ReadLastPatched(dir)
			if c.tables {
				if !strings.Contains(lines[co], "stage tables built and cached") || recErr != nil || rec.Image != out {
					t.Errorf("co-op line %q, record %+v, %v", lines[co], rec, recErr)
				}
			} else if !strings.Contains(lines[co], "WARNING: this build can't make the co-op stage tables") || recErr != nil || rec.Image != out {
				t.Errorf("co-op line %q, record %+v, %v", lines[co], rec, recErr)
			}
			if !c.tables {
				return
			}

			// Patching the patched image: nothing to do, exit 0, and the
			// record now names that image.
			again := filepath.Join(t.TempDir(), "again.iso")
			stdout, code = runPatchCmd(t, out, again, dir)
			if code != 0 || !strings.Contains(stdout, "already contains this version") || !strings.Contains(stdout, "  co-op : stage tables already cached") {
				t.Fatalf("patching the patched image: exit %d\n%s", code, stdout)
			}
			if _, err := os.Stat(again); err == nil {
				t.Error("patching the patched image wrote an image")
			}
			if rec, err := tables.ReadLastPatched(dir); err != nil || rec.Image != out {
				t.Errorf("record after patching the patched image: %+v, %v", rec, err)
			}
		})
	}

	// A disc patched by another version still fails, without a co-op line.
	patched, err := inject.PatchISOInMemory(tablestest.PatchableISO(tablestest.Vanilla()))
	if err != nil {
		t.Fatal(err)
	}
	t2 := bytes.Index(patched, inject.T2Bytes)
	if t2 < 0 {
		t.Fatal("no T2 section in the patched image")
	}
	patched[t2+len(inject.T2Bytes)/2] ^= 0xFF
	stdout, code := runPatchCmd(t, stagetablestest.WriteImage(t, "other.iso", patched), filepath.Join(t.TempDir(), "o.iso"), t.TempDir())
	if code != 1 || !strings.Contains(stdout, "patched by a different ww-multiplayer version") || strings.Contains(stdout, "co-op") {
		t.Errorf("another version's image: exit %d\n%s", code, stdout)
	}
}
