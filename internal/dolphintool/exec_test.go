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
// fakeToolFailEnv, prints an error and exits 3).
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
	out := ""
	for i := range args {
		if args[i] == "-o" && i+1 < len(args) {
			out = args[i+1]
		}
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
	out := filepath.Join(t.TempDir(), "out.iso")
	var lines []string
	if err := tool.ToISO(context.Background(), "in.rvz", out, func(l string) { lines = append(lines, l) }); err != nil {
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
	err = tool.ToISO(context.Background(), "in.rvz", out2, nil)
	if err == nil || !strings.Contains(err.Error(), "exit code 3") || !strings.Contains(err.Error(), "Unable to open input disc image") {
		t.Errorf("fail: %v", err)
	}
}

func TestExecRunnerMissing(t *testing.T) {
	_, err := (Tool{Path: filepath.Join(t.TempDir(), "DolphinTool.exe")}).Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "can't run") {
		t.Errorf("missing tool: %v", err)
	}
}
