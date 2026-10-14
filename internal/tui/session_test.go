package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// waitDone fails the test instead of hanging if a session never returns.
// Every session here returns immediately, so the timeout is only a guard.
func waitDone(t *testing.T, s *session) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session goroutine did not return")
	}
}

// TestStartSession_NilHooksDoNotPanic: a missing session func ends the session
// with an error line instead of a nil-func panic in its goroutine.
func TestStartSession_NilHooksDoNotPanic(t *testing.T) {
	for _, role := range []string{"Host", "Join"} {
		s := startSession(Hooks{}, role, "Link", "10.0.0.5")
		waitDone(t, s)
		select {
		case e := <-s.logCh:
			if e.level != report.Err || !strings.Contains(e.msg, "isn't available") {
				t.Errorf("%s with a nil hook logged %v %q, want the missing-hook error", role, e.level, e.msg)
			}
		default:
			t.Errorf("%s with a nil hook logged nothing", role)
		}
	}
}
