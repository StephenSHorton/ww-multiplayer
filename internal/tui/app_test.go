package tui

import (
	"os"
	"reflect"
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

	err := Run("test", nil)
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

// TestBackMsg_KeepsConnectFields: Esc back from the dashboard returns to the
// connect screen with the role, IP, name and focus the user had, minus any
// stale error, so a retry is one Enter.
func TestBackMsg_KeepsConnectFields(t *testing.T) {
	m := newModel(nil, "test")
	m.screen = screenDashboard
	m.connect = connectModel{role: 1, ipInput: "10.0.0.9", nameInput: "Bob", focused: 3, err: "old error", joinCoop: 1}

	next, _ := m.Update(backMsg{})
	got := next.(model)

	if got.screen != screenConnect {
		t.Fatalf("screen = %v after backMsg, want the connect screen", got.screen)
	}
	want := connectModel{role: 1, ipInput: "10.0.0.9", nameInput: "Bob", focused: 3, joinCoop: 1}
	if !reflect.DeepEqual(got.connect, want) {
		t.Errorf("connect after backMsg = %+v, want %+v", got.connect, want)
	}
}

// TestConnectedMsg_NilControllerShowsInlineError: with no session controller wired,
// the app stays on the connect screen with an inline error instead of
// starting a session that would panic.
func TestConnectedMsg_NilControllerShowsInlineError(t *testing.T) {
	for _, role := range []string{"Host", "Join"} {
		m := newModel(nil, "test")
		m.screen = screenConnect
		m.connect = newConnect()

		next, cmd := m.Update(connectedMsg{role: role, addr: "10.0.0.5", name: "Link"})
		got := next.(model)

		if got.screen != screenConnect || cmd != nil {
			t.Errorf("%s with a nil hook left the connect screen (screen=%v, cmd=%v)", role, got.screen, cmd != nil)
		}
		if !strings.Contains(got.connect.err, "isn't available") {
			t.Errorf("%s with a nil hook: connect error = %q, want the missing-hook message", role, got.connect.err)
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
