package tui

import (
	"context"
	"errors"
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
// with an error line instead of a nil-func panic in its goroutine, and that
// line is the session-ended reason.
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
		if got := s.endReason(); !strings.Contains(got, "isn't available") {
			t.Errorf("%s with a nil hook: endReason = %q, want the missing-hook message", role, got)
		}
	}
}

// TestWaitSessionEnd_HostError: the end message names its session and carries
// the error HostSession returned.
func TestWaitSessionEnd_HostError(t *testing.T) {
	hooks := Hooks{HostSession: func(context.Context, context.CancelFunc, string, report.Reporter) error {
		return errors.New("server start: bind: address already in use")
	}}
	s := startSession(hooks, "Host", "Link", "")
	msg, ok := waitSessionEnd(s)().(sessionEndedMsg)
	if !ok {
		t.Fatal("waitSessionEnd did not produce a sessionEndedMsg")
	}
	if msg.sess != s {
		t.Error("sessionEndedMsg is not tagged with its own session")
	}
	if msg.reason != "server start: bind: address already in use" {
		t.Errorf("reason = %q, want the HostSession error", msg.reason)
	}
}

// TestEndReason_NewestErrLine: the reason is the newest Err-level line, which
// is how runMultiplayerGoroutines reports a goroutine failure; a session that
// logged no error gets the neutral fallback.
func TestEndReason_NewestErrLine(t *testing.T) {
	hooks := Hooks{JoinSession: func(_ context.Context, _ context.CancelFunc, _, _ string, rep report.Reporter) {
		rep.Log(report.Err, "broadcast-pose: first failure")
		rep.Log(report.Err, "puppet-sync: dolphin not running")
		rep.Log(report.Info, "cleanup done")
	}}
	s := startSession(hooks, "Join", "Link", "10.0.0.5")
	waitDone(t, s)
	if got := s.endReason(); got != "puppet-sync: dolphin not running" {
		t.Errorf("endReason = %q, want the newest Err line", got)
	}

	quiet := startSession(Hooks{JoinSession: func(context.Context, context.CancelFunc, string, string, report.Reporter) {}}, "Join", "Link", "10.0.0.5")
	waitDone(t, quiet)
	if got := quiet.endReason(); got != "no error was reported" {
		t.Errorf("endReason with no errors = %q, want the fallback", got)
	}
}

// TestWaitSessionEnd_NilSession: no session, no watcher Cmd.
func TestWaitSessionEnd_NilSession(t *testing.T) {
	if waitSessionEnd(nil) != nil {
		t.Error("waitSessionEnd(nil) should return a nil Cmd")
	}
}
