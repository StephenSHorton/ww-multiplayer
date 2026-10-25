package dolphintool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The test binary doubles as a fake DolphinTool.exe when fakeToolEnv is
// set, so ExecRunner and ToISO are tested against a real process: it
// prints RealConvertHelp for `convert --help`, and for a convert writes
// "ISO" at -o after a line on stdout and one on stderr (or, with
// fakeToolFailEnv, prints an error and exits 3). Like the real tool, it
// resolves -i, -o and -u against its own working directory (ExecRunner
// runs it in its folder), fails when -i can't be opened, and requires
// the -u folder to exist.
const (
	fakeToolEnv     = "WW_TEST_FAKE_DOLPHINTOOL"
	fakeToolFailEnv = "WW_TEST_FAKE_DOLPHINTOOL_FAIL"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeToolEnv) == "1" {
		os.Exit(fakeDolphinTool(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeDolphinTool(args []string) int {
	if slices.Equal(args, HelpArgs) {
		os.Stdout.WriteString(RealConvertHelp)
		return 0
	}
	if len(args) == 0 || args[0] != "convert" {
		fmt.Println("usage: dolphin-tool COMMAND -h")
		return 1
	}
	if os.Getenv(fakeToolFailEnv) == "1" {
		fmt.Fprintln(os.Stderr, "Error: Unable to open input disc image")
		return 3
	}
	flag := func(f string) string {
		for i := range args {
			if args[i] == f && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	out := flag("-o")
	if _, err := os.Stat(flag("-i")); err != nil {
		fmt.Fprintln(os.Stderr, "Error: Unable to open input disc image")
		return 3
	}
	if fi, err := os.Stat(flag("-u")); err != nil || !fi.IsDir() {
		fmt.Fprintln(os.Stderr, "Error: no user folder")
		return 4
	}
	fmt.Print("Converting\r50%\n")
	fmt.Fprintln(os.Stderr, "done")
	if err := os.WriteFile(out, []byte("ISO"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func TestExecRunnerFakeTool(t *testing.T) {
	t.Setenv(fakeToolEnv, "1")
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	tool := Tool{Path: exe}
	if c, err := tool.Probe(context.Background()); err != nil || !c.User {
		t.Fatalf("Probe: %+v %v", c, err)
	}
	in := filepath.Join(t.TempDir(), "in.rvz")
	if err := os.WriteFile(in, []byte("RVZ\x01"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.iso")
	var lines []string
	if err := tool.ToISO(context.Background(), in, out, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ISO" {
		t.Errorf("out = %q", b)
	}
	for _, want := range []string{"Converting", "50%", "done"} {
		if !slices.Contains(lines, want) {
			t.Errorf("lines %q lack %q", lines, want)
		}
	}

	t.Setenv(fakeToolFailEnv, "1")
	out2 := filepath.Join(t.TempDir(), "out.iso")
	err = tool.ToISO(context.Background(), in, out2, nil)
	if err == nil || !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "Unable to open input disc image") {
		t.Errorf("fail: %v", err)
	}
}

// TestToISORelativePaths: `patch game.rvz` from the disc's folder hands
// ToISO relative paths (and a relative DOLPHIN_EXE a relative tool), but
// the tool runs in its own folder. Its input must still be found, its ISO
// must land in the caller's folder and nothing may be written in the
// tool's folder.
func TestToISORelativePaths(t *testing.T) {
	t.Setenv(fakeToolEnv, "1")
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile("game.rvz", []byte("RVZ\x01"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Dir(exe))
	tools := []Tool{{Path: exe}}
	if rel, err := filepath.Rel(work, exe); err == nil {
		tools = append(tools, Tool{Path: rel})
	}
	for i, tool := range tools {
		out := fmt.Sprintf("game-%d.converting.iso", i)
		if err := tool.ToISO(context.Background(), "game.rvz", out, nil); err != nil {
			t.Fatalf("tool %q: %v", tool.Path, err)
		}
		if b, err := os.ReadFile(filepath.Join(work, out)); err != nil || string(b) != "ISO" {
			t.Errorf("tool %q: ISO in the caller's folder: %q %v", tool.Path, b, err)
		}
	}
	after, _ := os.ReadDir(filepath.Dir(exe))
	if len(after) != len(before) {
		t.Errorf("the tool's folder changed: %d -> %d entries", len(before), len(after))
	}
}

func TestExecRunnerMissing(t *testing.T) {
	_, err := (Tool{Path: filepath.Join(t.TempDir(), "DolphinTool.exe")}).Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "can't run") {
		t.Errorf("missing tool: %v", err)
	}
}
