package main

import (
	"encoding/json"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
)

// App PR 5 put the app window in front of main's switch (app_face.go). These
// tests pin that the CLI did not move: every subcommand the switch had
// before is still there and still dispatched to it, and only no arguments,
// `tui` and `ui` open something else.

var updateSubcommands = flag.String("update-subcommands", "", "rewrite testdata/cli-subcommands.txt from this main.go")

const subcommandsFile = "testdata/cli-subcommands.txt"

// mainSubcommands returns the string cases of the switch in func main of
// the Go source src, sorted.
func mainSubcommands(t *testing.T, src []byte) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if sel, ok := sw.Tag.(*ast.IndexExpr); !ok || !isOSArgs(sel.X) {
				return true
			}
			for _, s := range sw.Body.List {
				for _, e := range s.(*ast.CaseClause).List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						names = append(names, v)
					}
				}
			}
			return false
		})
	}
	sort.Strings(names)
	return names
}

func isOSArgs(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Args" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "os"
}

// TestCLISubcommandsUnchanged: main's switch has exactly the subcommands it
// had before App PR 5 (testdata/cli-subcommands.txt, extracted from main.go
// at origin/main 56ce208 with -update-subcommands), and none of them opens
// the window or the TUI, with or without the window in the build.
func TestCLISubcommandsUnchanged(t *testing.T) {
	if *updateSubcommands != "" {
		src, err := os.ReadFile(*updateSubcommands)
		if err != nil {
			t.Fatal(err)
		}
		out := strings.Join(mainSubcommands(t, src), "\n") + "\n"
		if err := os.WriteFile(subcommandsFile, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(subcommandsFile)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	got := mainSubcommands(t, src)
	if g, w := strings.Join(got, "\n")+"\n", strings.ReplaceAll(string(want), "\r\n", "\n"); g != w {
		t.Fatalf("main's subcommands changed:\ngot:\n%s\nwant:\n%s", g, w)
	}
	if len(got) < 50 {
		t.Fatalf("only %d subcommands found; the extractor lost the switch", len(got))
	}
	for _, name := range got {
		if name == "tui" || name == "ui" {
			t.Fatalf("%q was already a subcommand; the new face would shadow it", name)
		}
		for _, avail := range []bool{true, false} {
			if f := appFace([]string{name, "x"}, avail); f != faceCLI {
				t.Errorf("appFace(%q, gui=%v) = %d, want the CLI", name, avail, f)
			}
		}
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
