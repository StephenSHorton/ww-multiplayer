package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// setVersionForTest overrides the build-time version string the way
// `-ldflags "-X main.version=..."` does in release.yml / build-mac.sh.
// Assigning to it here also guards that version stays a package-level
// string var: -X silently does nothing to a const or a renamed var.
func setVersionForTest(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestVersionDefaultsToDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("unstamped build: version = %q, want %q", version, "dev")
	}
	if got, want := versionLine(), "ww-multiplayer dev"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
}

func TestVersionLineUsesStampedVersion(t *testing.T) {
	setVersionForTest(t, "v0.3.0-rc1")
	if got, want := versionLine(), "ww-multiplayer v0.3.0-rc1"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
}

func TestPrintHelpHeaderShowsVersion(t *testing.T) {
	setVersionForTest(t, "v9.9.9")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	read := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r) // drain concurrently so a small pipe buffer can't block printHelp
		read <- b
	}()
	stdout := os.Stdout
	os.Stdout = w
	printHelp()
	os.Stdout = stdout
	w.Close()
	out := <-read

	lines := strings.Split(string(out), "\n")
	if got, want := lines[0], "Wind Waker Multiplayer v9.9.9"; got != want {
		t.Fatalf("help header = %q, want %q", got, want)
	}
	if !strings.Contains(string(out), exeCmd+" version") {
		t.Fatalf("help does not list the version subcommand:\n%s", out)
	}
}

// mainHelperEnv carries the subcommand for TestMainHelper. When it is set,
// the test binary is acting as the ww-multiplayer CLI for runMain.
const mainHelperEnv = "WW_TEST_MAIN_ARG"

// TestMainHelper is not a real test. runMain re-executes the test binary
// with only this test selected and mainHelperEnv set, and this then calls
// main() with that subcommand. Running main() in a child process lets the
// tests see its exit code, which os.Exit would otherwise take down with
// the test binary. Without the env var it does nothing.
func TestMainHelper(t *testing.T) {
	arg, ok := os.LookupEnv(mainHelperEnv)
	if !ok {
		return
	}
	os.Args = []string{"ww-multiplayer", arg}
	main()
	os.Exit(0) // keep the testing package's "PASS" off stdout
}

// runMain runs `ww-multiplayer <arg>` through TestMainHelper and returns
// its stdout and exit code.
func runMain(t *testing.T, arg string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$")
	cmd.Env = append(os.Environ(), mainHelperEnv+"="+arg,
		// Under -race a clean exit otherwise sleeps 1s for the detector.
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	default:
		t.Fatalf("running main(%q): %v", arg, err)
		return "", 0
	}
}

// The release notes tell both players to compare `ww-multiplayer version`,
// so the dispatch itself is pinned here, not just versionLine().
func TestVersionSubcommand(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		t.Run(arg, func(t *testing.T) {
			out, code := runMain(t, arg)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stdout:\n%s", code, out)
			}
			// The child is an unstamped build, so it prints the default.
			if want := "ww-multiplayer dev\n"; out != want {
				t.Fatalf("stdout = %q, want %q", out, want)
			}
		})
	}
}

// Control for TestVersionSubcommand: a subcommand main() doesn't know must
// still fail through the same helper, or the test above proves nothing.
func TestUnknownSubcommandFails(t *testing.T) {
	out, code := runMain(t, "no-such-command")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout:\n%s", code, out)
	}
	if !strings.HasPrefix(out, "Unknown command: no-such-command\n") {
		t.Fatalf("stdout does not start with the unknown-command line:\n%s", out)
	}
}
