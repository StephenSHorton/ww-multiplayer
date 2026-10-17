package main

// Golden transcripts of the CLI commands App PR 3 moved code out of:
// patch (now internal/patch) and dolphin2 (now on internal/dolphinenv, wwapi/emu/launch since C10;
// wwdev's since Phase D6, its goldens in cmd/wwdev).
// Each case runs `ww-multiplayer <args>` in a child process with its own
// environment (config dir, Dolphin paths in temp dirs) and compares its
// stdout and exit code to a file under testdata/cli-golden. The files were
// recorded on the code before the move, so a diff here is a change in what
// a CLI user sees. Regenerate one only for an intended change:
// go test -run TestCLIGolden -update-cli-golden.
// The harness is internal/cliutil/clitest, which wwdev's goldens share.

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/cliutil/clitest"
	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

var updateCLIGolden = flag.Bool("update-cli-golden", false, "rewrite testdata/cli-golden from this run")

// TestCLIHelper is the child process of runCLI: it runs main with the
// arguments clitest.Run passed it.
func TestCLIHelper(t *testing.T) {
	args, ok := clitest.HelperArgs()
	if !ok {
		return
	}
	os.Args = append([]string{"ww-multiplayer"}, args...)
	main()
	os.Exit(0)
}

// runCLI runs `ww-multiplayer args...` with env and returns its stdout and
// exit code.
func runCLI(t *testing.T, env map[string]string, args ...string) (string, int) {
	t.Helper()
	return clitest.Run(t, "TestCLIHelper", env, args...)
}

// normalizeCLI replaces exeCmd by <exe>, root (a temp dir) by <TMP> in
// either slash form, and the co-op line's build time by "in N s".
func normalizeCLI(out, root string) string {
	return clitest.Normalize(out, root, exeCmd)
}

// checkCLIGolden compares one run with testdata/cli-golden/<name>.txt.
func checkCLIGolden(t *testing.T, name, out string, code int) {
	t.Helper()
	checkCLIGoldenNorm(t, name, out, code, nil)
}

// checkCLIGoldenNorm is checkCLIGolden that, when norm is not nil, passes
// both the run and the file through norm before comparing them.
func checkCLIGoldenNorm(t *testing.T, name, out string, code int, norm func(string) string) {
	t.Helper()
	clitest.CheckGolden(t, name, out, code, norm, *updateCLIGolden)
}

func writeCLIFile(t *testing.T, path string, b []byte) string {
	t.Helper()
	return clitest.WriteFile(t, path, b)
}

// configEnv points every OS's user config dir at dir.
func configEnv(dir string) map[string]string {
	return clitest.ConfigEnv(dir)
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

// TestCLIGoldenHelp: `help` and an unknown command print the help that
// printHelp generates from the command registry (commands.go). The files
// match what the hand-written printHelp printed before the registry
// replaced main's switch. The help differs with the app window in the
// build, so each build has its own file (-tags desktop,production: gui).
// The files are recorded on Windows and compared byte for byte there;
// elsewhere exeCmd is shorter, so the comparison ignores how far each
// row's description is padded (helpColumns) but not its text.
func TestCLIGoldenHelp(t *testing.T) {
	var norm func(string) string
	if runtime.GOOS != "windows" {
		if *updateCLIGolden {
			t.Skip("the help goldens are recorded on Windows (exeCmd sets their columns)")
		}
		norm = helpColumns
	}
	root := t.TempDir()
	env := configEnv(filepath.Join(root, "config"))
	build := "nogui"
	if gui.Available() {
		build = "gui"
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"help", []string{"help"}},
		{"unknown", []string{"no-such-command", "x"}},
	} {
		o, code := runCLI(t, env, c.args...)
		checkCLIGoldenNorm(t, c.name+"-"+build, normalizeCLI(o, root), code, norm)
	}
}

// helpPadding is the run of spaces printHelp may put between a command
// row's usage and its description.
var helpPadding = regexp.MustCompile(`(?m)^(  <exe> .*?\S) {2,}`)

// helpColumns collapses that run to one space. printHelp pads
// `exeCmd usage` to a fixed width, so the description column moves with
// the length of exeCmd (.\ww-multiplayer.exe on Windows, ./ww-multiplayer
// elsewhere), which normalizeCLI's <exe> does not undo. A row too long to
// pad has one space on every platform, so it compares equal too.
func helpColumns(s string) string {
	return helpPadding.ReplaceAllString(s, "$1 ")
}
