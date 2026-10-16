package main

// Golden transcripts of the CLI commands App PR 3 moved code out of:
// patch (now internal/patch) and dolphin2 (now on internal/dolphinenv).
// Each case runs `ww-multiplayer <args>` in a child process with its own
// environment (config dir, Dolphin paths in temp dirs) and compares its
// stdout and exit code to a file under testdata/cli-golden. The files were
// recorded on the code before the move, so a diff here is a change in what
// a CLI user sees. Regenerate one only for an intended change:
// go test -run TestCLIGolden -update-cli-golden.
//
// No case can start Dolphin: dolphin2's only launching case points
// DOLPHIN_EXE at a directory (which no OS executes) and runs only when no
// Dolphin process is running, since it would otherwise attach to it.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

var updateCLIGolden = flag.Bool("update-cli-golden", false, "rewrite testdata/cli-golden from this run")

const cliHelperEnv = "WW_TEST_CLI_ARGS"

// TestCLIHelper is the child process of runCLI: it runs main with the
// arguments in WW_TEST_CLI_ARGS (unit-separator 0x1F between them).
func TestCLIHelper(t *testing.T) {
	args, ok := os.LookupEnv(cliHelperEnv)
	if !ok {
		return
	}
	os.Args = append([]string{"ww-multiplayer"}, strings.Split(args, "\x1f")...)
	main()
	os.Exit(0)
}

// cliEnv is the environment of a child: the parent's without the variables
// the commands read, then env.
func cliEnv(env map[string]string) []string {
	drop := map[string]bool{
		"DOLPHIN_EXE": true, "ISO_PATH": true, "USER_DIR_1": true, "USER_DIR_2": true,
		"SAVE_STATE": true, "SAVE_STATE_2": true, "APPDATA": true, "XDG_CONFIG_HOME": true,
		"HOME": true, "WW_COLOR": true, "WW_COOP_TABLES": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return append(out, "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
}

// runCLI runs `ww-multiplayer args...` with env and returns its stdout and
// exit code.
func runCLI(t *testing.T, env map[string]string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIHelper$")
	cmd.Env = append(cliEnv(env), cliHelperEnv+"="+strings.Join(args, "\x1f"))
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("running %q: %v", args, err)
	return "", 0
}

var cliSeconds = regexp.MustCompile(`in [0-9]+\.[0-9] s,`)

// normalizeCLI replaces root (a temp dir) by <TMP> in either slash form,
// and the co-op line's build time by "in N s".
func normalizeCLI(out, root string) string {
	// The binary as the user types it differs by platform (exeCmd).
	out = strings.ReplaceAll(out, exeCmd+" ", "<exe> ")
	// %q-quoted error texts double each backslash.
	for _, r := range []string{strings.ReplaceAll(root, `\`, `\\`), root, filepath.ToSlash(root)} {
		out = strings.ReplaceAll(out, r, "<TMP>")
	}
	out = strings.ReplaceAll(out, `\`, "/")
	return cliSeconds.ReplaceAllString(out, "in N s,")
}

// checkCLIGolden compares one run with testdata/cli-golden/<name>.txt.
func checkCLIGolden(t *testing.T, name, out string, code int) {
	t.Helper()
	got := fmt.Sprintf("exit %d\n%s", code, out)
	path := filepath.Join("testdata", "cli-golden", name+".txt")
	if *updateCLIGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record it with -update-cli-golden)", err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
		t.Errorf("%s: output changed\n--- got\n%s\n--- want\n%s", name, got, w)
	}
}

func writeCLIFile(t *testing.T, path string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// configEnv points every OS's user config dir at dir.
func configEnv(dir string) map[string]string {
	return map[string]string{"APPDATA": dir, "XDG_CONFIG_HOME": dir, "HOME": dir}
}

func TestCLIGoldenPatch(t *testing.T) {
	root := t.TempDir()
	env := configEnv(filepath.Join(root, "config"))
	vanilla := writeCLIFile(t, filepath.Join(root, "in", "Wind Waker.iso"), tablestest.PatchableISO(tablestest.Vanilla()))
	garbage := writeCLIFile(t, filepath.Join(root, "in", "garbage.iso"), bytes.Repeat([]byte{0x5A}, 4096))
	out := filepath.Join(root, "out", "patched.iso")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(name string, args ...string) {
		t.Helper()
		o, code := runCLI(t, env, args...)
		checkCLIGolden(t, name, normalizeCLI(o, root), code)
	}
	run("patch-usage", "patch")
	if runtime.GOOS == "windows" { // the OS's "no such file" text
		run("patch-missing-windows", "patch", filepath.Join(root, "in", "nope.ciso"))
	}
	run("patch-garbage", "patch", garbage, filepath.Join(root, "out", "g.iso"))
	run("patch-ok", "patch", vanilla, out)
	run("patch-default-out", "patch", vanilla)
	run("patch-already", "patch", out)

	img, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(img, inject.T2Bytes)
	if i < 0 {
		t.Fatal("no T2 section in the patched image")
	}
	img[i+len(inject.T2Bytes)-1] ^= 0x01
	other := writeCLIFile(t, filepath.Join(root, "in", "other.iso"), img)
	run("patch-other-version", "patch", other, filepath.Join(root, "out", "o.iso"))
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
		for k, v := range map[string]string{"DOLPHIN_EXE": exe, "ISO_PATH": iso, "USER_DIR_1": ud1, "USER_DIR_2": ud2} {
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
	if pids, err := dolphin.ListPIDs(); err == nil && len(pids) > 0 {
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
