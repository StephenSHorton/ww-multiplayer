package tui

import (
	"os"
	"strings"
	"testing"
)

// TestRun_NoInteractiveStdin_ReturnsClearError verifies the headless guard
// added for #33: when stdin isn't attached to anything interactive, Run
// must return a clear, actionable error instead of ever constructing the
// Bubble Tea program (which would otherwise either hang waiting on input
// in a test process or die with the cryptic "error making raw" crash).
func TestRun_NoInteractiveStdin_ReturnsClearError(t *testing.T) {
	orig := hasInteractiveStdin
	hasInteractiveStdin = func() bool { return false }
	defer func() { hasInteractiveStdin = orig }()

	err := Run("test", Hooks{})
	if err == nil {
		t.Fatal("Run() with no interactive stdin: expected an error, got nil")
	}

	msg := err.Error()
	for _, want := range []string{"interactive terminal", "host", "join"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Run() error %q missing expected guidance substring %q", msg, want)
		}
	}
}

// TestHasInteractiveStdin_RegularFile exercises the real (non-stubbed)
// detection against stdin redirected to a plain file, which is neither a
// console/tty nor a MinTTY/Cygwin pty pipe — the genuinely-headless case
// from #33 (e.g. `ww-multiplayer.exe < input.txt`).
func TestHasInteractiveStdin_RegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	origStdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = origStdin }()

	if hasInteractiveStdin() {
		t.Error("hasInteractiveStdin() = true for a regular file redirected to stdin; want false")
	}
}
