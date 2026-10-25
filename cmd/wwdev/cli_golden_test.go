package main

// Golden transcripts of wwdev: dolphin2's, moved from ww-multiplayer's
// cli_golden_test.go with the command (Phase D6), and wwdev's help. Each
// case runs `wwdev <args>` in a child process with its own environment
// and compares its stdout and exit code to a file under
// testdata/cli-golden (internal/cliutil/clitest). Regenerate one only for
// an intended change: go test ./cmd/wwdev -run TestCLIGolden -update-cli-golden.
//
// No case can start Dolphin: dolphin2's only launching case points
// DOLPHIN_EXE at a directory (which no OS executes) and runs only when no
// Dolphin process is running, since it would otherwise attach to it.

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/cliutil/clitest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

var updateCLIGolden = flag.Bool("update-cli-golden", false, "rewrite testdata/cli-golden from this run")

// TestWWDevHelper is the child process of runCLI: it runs main with the
// arguments clitest.Run passed it.
func TestWWDevHelper(t *testing.T) {
	args, ok := clitest.HelperArgs()
	if !ok {
		return
	}
	os.Args = append([]string{"wwdev"}, args...)
	main()
	os.Exit(0)
}

// runCLI runs `wwdev args...` with env and returns its stdout and exit
// code.
func runCLI(t *testing.T, env map[string]string, args ...string) (string, int) {
	t.Helper()
	return clitest.Run(t, "TestWWDevHelper", env, args...)
}

// normalizeCLI replaces exeCmd by <exe> and root (a temp dir) by <TMP>.
func normalizeCLI(out, root string) string {
	return clitest.Normalize(out, root, exeCmd)
}

// checkCLIGolden compares one run with testdata/cli-golden/<name>.txt.
func checkCLIGolden(t *testing.T, name, out string, code int) {
	t.Helper()
	clitest.CheckGolden(t, name, out, code, nil, *updateCLIGolden)
}

func writeCLIFile(t *testing.T, path string, b []byte) string {
	t.Helper()
	return clitest.WriteFile(t, path, b)
}

// configEnv points every OS's user config dir at dir.
func configEnv(dir string) map[string]string {
	return clitest.ConfigEnv(dir)
}

func TestCLIGoldenDolphin2(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "Dolphin-x64") // a directory: never executable
	iso := filepath.Join(root, "patched.iso")
	ud1 := filepath.Join(root, "ud1")
	ud2 := filepath.Join(root, "ud2")
	sav := filepath.Join(root, "start.sav")
	base := func(extra map[string]string) map[string]string {
		env := configEnv(filepath.Join(root, "config"))
		for k, v := range map[string]string{"DOLPHIN_EXE": exe, "ISO_PATH": iso, "USER_DIR_1": ud1, "USER_DIR_2": ud2,
			// dolphin2 records its launches there; nothing under cmd/wwdev.
			"WW_DEV_LOGS": filepath.Join(root, "logs")} {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}
	run := func(name string, env map[string]string, args ...string) {
		t.Helper()
		o, code := runCLI(t, env, append([]string{"dolphin2"}, args...)...)
		checkCLIGolden(t, name, normalizeCLI(o, root), code)
	}

	run("dolphin2-no-exe", base(nil))
	if err := os.MkdirAll(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	run("dolphin2-no-iso", base(nil))
	writeCLIFile(t, iso, []byte("not a disc"))
	run("dolphin2-no-save-state", base(map[string]string{"SAVE_STATE": sav}))
	run("dolphin2-no-save-state-2", base(map[string]string{"SAVE_STATE_2": sav}))
	writeCLIFile(t, sav, []byte("state"))
	run("dolphin2-save-state-2-alone", base(map[string]string{"SAVE_STATE_2": sav}))
	run("dolphin2-no-user-dir", base(nil))

	// --reset removes USER_DIR_2 before the checks.
	writeCLIFile(t, filepath.Join(ud2, "Config", "Dolphin.ini"), []byte("[Core]\n"))
	env := base(map[string]string{"DOLPHIN_EXE": filepath.Join(root, "missing.exe")})
	run("dolphin2-reset", env, "--reset")
	if _, err := os.Stat(ud2); !os.IsNotExist(err) {
		t.Errorf("--reset left %s: %v", ud2, err)
	}

	// The bootstrap copies USER_DIR_1 (but its Cache) to USER_DIR_2 and
	// installs the cheats in both; then the launch of the directory fails.
	writeCLIFile(t, filepath.Join(ud1, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = 8\n"))
	writeCLIFile(t, filepath.Join(ud1, "Cache", "GZLE01.uidcache"), []byte("cache"))
	if runtime.GOOS != "windows" {
		t.Skip("the launching case is recorded on Windows only (exec errors differ by OS)")
	}
	if pids, err := emu.ListPIDs(); err == nil && len(pids) > 0 {
		t.Skipf("a Dolphin is running (pids %v): dolphin2 would plan around it", pids)
	}
	run("dolphin2-bootstrap-windows", base(nil))
	if _, err := os.Stat(filepath.Join(ud2, "Cache")); !os.IsNotExist(err) {
		t.Errorf("bootstrap copied Cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ud2, "Config", "Dolphin.ini")); err != nil {
		t.Errorf("after bootstrap: %v", err)
	}
	for _, ud := range []string{ud1, ud2} {
		if b, err := os.ReadFile(filepath.Join(ud, "GameSettings", "GZLE01.ini")); err != nil || !bytes.Equal(b, bundledCheatsIni) {
			t.Errorf("cheats in %s: %d bytes, %v", ud, len(b), err)
		}
	}
}

// TestCLIGoldenHelp: wwdev's help, with no arguments, `help` and an
// unknown command. Its rows are padded to a column after exeCmd, so the
// files are recorded on Windows and compared there.
func TestCLIGoldenHelp(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("wwdev's help goldens are recorded on Windows (exeCmd sets their columns)")
	}
	root := t.TempDir()
	env := configEnv(filepath.Join(root, "config"))
	for _, c := range []struct {
		name string
		args []string
	}{
		{"wwdev-none", nil},
		{"wwdev-help", []string{"help"}},
		{"wwdev-unknown", []string{"host", "x"}},
	} {
		o, code := runCLI(t, env, c.args...)
		checkCLIGolden(t, c.name, normalizeCLI(o, root), code)
	}
}
