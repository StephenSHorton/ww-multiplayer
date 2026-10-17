package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
)

// App PR 5 put the app window in front of main's dispatch (app_face.go).
// These tests pin that the CLI did not move: every subcommand main had
// before is still in the command registry (commands.go), which replaced
// main's switch in Phase B1, and only no arguments, `tui` and `ui` open
// something else.

var updateSubcommands = flag.Bool("update-subcommands", false, "rewrite testdata/cli-subcommands.txt from the command registry")

const subcommandsFile = "testdata/cli-subcommands.txt"

// registrySubcommands returns every name and alias in the command
// registry, sorted. A name used twice is an error.
func registrySubcommands(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var names []string
	for _, c := range commandTable() {
		for _, n := range append([]string{c.name}, c.aliases...) {
			if seen[n] {
				t.Errorf("%q is in the registry twice", n)
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// TestCLISubcommandsUnchanged: the registry has exactly the subcommands
// main's switch had before App PR 5 (testdata/cli-subcommands.txt,
// extracted from main.go at origin/main 56ce208), each of them dispatches
// to its own entry, and none of them opens the window or the TUI, with or
// without the window in the build.
func TestCLISubcommandsUnchanged(t *testing.T) {
	got := registrySubcommands(t)
	if *updateSubcommands {
		out := strings.Join(got, "\n") + "\n"
		if err := os.WriteFile(subcommandsFile, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(subcommandsFile)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := strings.Join(got, "\n")+"\n", strings.ReplaceAll(string(want), "\r\n", "\n"); g != w {
		t.Fatalf("main's subcommands changed:\ngot:\n%s\nwant:\n%s", g, w)
	}
	if len(got) < 50 {
		t.Fatalf("only %d subcommands found; the registry lost commands", len(got))
	}
	for _, name := range got {
		if name == "tui" || name == "ui" {
			t.Fatalf("%q was already a subcommand; the new face would shadow it", name)
		}
		if c, ok := lookupCommand(name); !ok || c.run == nil {
			t.Errorf("%q: lookupCommand = %v, run set %v", name, ok, c.run != nil)
		} else if c.name != name && !slices.Contains(c.aliases, name) {
			t.Errorf("%q dispatches to %q", name, c.name)
		}
		for _, avail := range []bool{true, false} {
			if f := appFace([]string{name, "x"}, avail); f != faceCLI {
				t.Errorf("appFace(%q, gui=%v) = %d, want the CLI", name, avail, f)
			}
		}
	}
}

// TestCommandRegistryHelp: every command printHelp lists has a section, a
// usage and a summary; the hidden ones are the developer group.
func TestCommandRegistryHelp(t *testing.T) {
	sections := map[cmdGroup]bool{}
	for _, s := range helpSections {
		sections[s.group] = true
	}
	listed := 0
	for _, c := range commandTable() {
		if c.hidden != (c.group == groupDev) {
			t.Errorf("%s: hidden %v in group %d", c.name, c.hidden, c.group)
		}
		if c.hidden {
			if c.usage != "" || c.summary != "" {
				t.Errorf("%s: hidden, but has help text", c.name)
			}
			continue
		}
		listed++
		if !sections[c.group] || c.usage == "" || c.summary == "" {
			t.Errorf("%s: group %d, usage %q, summary %q", c.name, c.group, c.usage, c.summary)
		}
		if !strings.HasPrefix(c.usage, c.name) {
			t.Errorf("%s: usage %q does not start with the name", c.name, c.usage)
		}
	}
	if listed != 14 {
		t.Errorf("printHelp lists %d commands, want 14", listed)
	}
}

func TestAppFace(t *testing.T) {
	for _, c := range []struct {
		args  []string
		avail bool
		want  face
	}{
		{nil, true, faceGUI}, // double-click: the window (owner decision 2)
		{nil, false, faceTUI},
		{[]string{"tui"}, true, faceTUI},
		{[]string{"tui"}, false, faceTUI},
		{[]string{"ui"}, true, faceGUI},
		{[]string{"ui"}, false, faceTUI},
		{[]string{"ui", "--browser"}, true, faceBrowser},
		{[]string{"ui", "--port", "1", "--browser"}, false, faceBrowser},
		{[]string{"host"}, true, faceCLI},
		{[]string{"help"}, true, faceCLI},
		{[]string{""}, true, faceCLI}, // an empty argument is still an (unknown) subcommand
		{[]string{"--browser"}, true, faceCLI},
	} {
		if got := appFace(c.args, c.avail); got != c.want {
			t.Errorf("appFace(%q, gui=%v) = %d, want %d", c.args, c.avail, got, c.want)
		}
	}
}

// TestStripPSN: a Finder launch of the .app that LaunchServices hands
// -psn_0_NNNN still opens the window on darwin; elsewhere args are as given.
func TestStripPSN(t *testing.T) {
	psn := []string{"-psn_0_12345"}
	if got := appFace(stripPSN(psn, "darwin"), true); got != faceGUI {
		t.Errorf("darwin %q: face %d, want faceGUI", psn, got)
	}
	if got := stripPSN([]string{"-psn_0_1", "join", "1.2.3.4", "-psn_0_2"}, "darwin"); strings.Join(got, " ") != "join 1.2.3.4" {
		t.Errorf("darwin: stripPSN = %q, want [join 1.2.3.4]", got)
	}
	if got := stripPSN(psn, "windows"); len(got) != 1 || got[0] != psn[0] {
		t.Errorf("windows: stripPSN = %q, want args unchanged", got)
	}
	if got := appFace(stripPSN(psn, "windows"), true); got != faceCLI {
		t.Errorf("windows %q: face %d, want faceCLI", psn, got)
	}
}

const faceHelperEnv = "WW_TEST_FACE_ARGS"

// TestFaceHelper is the child of runFaceMain: it runs main with the JSON
// argument list in WW_TEST_FACE_ARGS.
func TestFaceHelper(t *testing.T) {
	v, ok := os.LookupEnv(faceHelperEnv)
	if !ok {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(v), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"ww-multiplayer"}, args...)
	main()
	os.Exit(0)
}

func runFaceMain(t *testing.T, args ...string) (string, int) {
	t.Helper()
	j, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFaceHelper$")
	cmd.Env = append(os.Environ(), faceHelperEnv+"="+string(j), "APPDATA="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	cmd.Stdin = nil // no terminal: the TUI refuses and waits for Enter, which EOF answers
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("running main%q: %v", args, err)
	return "", 0
}

// TestTUIFace runs the real main: `tui` opens the TUI, which without a
// terminal says so and exits 1, as no arguments always did. No arguments
// does the same in a test binary without the window's tags (like plain
// `go build`); under `go test -tags desktop,production` it would open a
// real window, so that case runs only when gui.Available() is false.
func TestTUIFace(t *testing.T) {
	cases := [][]string{{"tui"}}
	if !gui.Available() {
		cases = append(cases, []string{})
	} else {
		t.Log("built with the app window: skipping the no-arguments case (it opens the window)")
	}
	for _, args := range cases {
		out, code := runFaceMain(t, args...)
		if code != 1 || !strings.Contains(out, "no interactive terminal detected on stdin") {
			t.Errorf("main%q: exit %d, output:\n%s", args, code, out)
		}
	}
}

// TestGUIBuildTags: Wails v2 starts a window only with the production tag,
// and internal/gui compiles the window only with desktop and production
// (gui_nogui.go), so every release and CI build of the Windows exe and the
// macOS binary must pass both, or it silently ships without the window.
func TestGUIBuildTags(t *testing.T) {
	for _, c := range []struct {
		file string
		min  int // go build lines that must carry the tags
	}{
		{".github/workflows/release.yml", 1},
		{".github/workflows/build.yml", 1}, // Windows; macOS runs build-mac.sh (below)
		{"scripts/build-mac.sh", 2},
	} {
		b, err := os.ReadFile(filepath.FromSlash(c.file))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "go build") && strings.Contains(line, "-tags desktop,production") {
				n++
			}
		}
		if n < c.min {
			t.Errorf("%s: %d go build lines with -tags desktop,production, want at least %d", c.file, n, c.min)
		}
		// The macOS builds link Wails' UTType use only with this (wails
		// build adds it; plain go build doesn't). Both workflows build the
		// macOS app through build-mac.sh, so that is where it must be.
		if c.file != "scripts/build-mac.sh" {
			if !strings.Contains(string(b), "./scripts/build-mac.sh") {
				t.Errorf("%s: the macOS job must build the app with ./scripts/build-mac.sh", c.file)
			}
		} else {
			const fw = "-framework UniformTypeIdentifiers"
			want := 2 // one per macOS go build (arm64, amd64)
			got := strings.Count(string(b), "CGO_LDFLAGS")
			if !strings.Contains(string(b), fw) || got < want {
				t.Errorf("%s: want %s in CGO_LDFLAGS on both macOS go builds (found %d CGO_LDFLAGS)", c.file, fw, got)
			}
		}
	}
}
