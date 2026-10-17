package engine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// fakeCoop is an engine.Coop that records what the session asked of it.
// Its one pipeline fails at once with fail, which ends the session.
type fakeCoop struct {
	mu       sync.Mutex
	calls    []string
	srv      *network.Server
	startErr error
	fail     error
}

func (f *fakeCoop) call(c string) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
}

func (f *fakeCoop) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeCoop) StartHost(_ context.Context, _ log.Reporter, srv *network.Server) error {
	f.call("start-host")
	f.srv = srv
	return f.startErr
}

func (f *fakeCoop) StartJoin(log.Reporter) { f.call("start-join") }

func (f *fakeCoop) WithPoseClient(name string, next func(*network.Client)) func(*network.Client) {
	f.call("pose-client " + name)
	return next
}

func (f *fakeCoop) Pipelines(name, addr string, _ log.Reporter) []Pipeline {
	f.call("pipelines " + name + " " + addr)
	return []Pipeline{{Name: "coop-sync", Run: func(context.Context) error { return f.fail }}}
}

func (f *fakeCoop) Stop() { f.call("stop") }

// idleEnv is an Env whose pose pipelines wait for a Dolphin until the
// session is cancelled, and which counts the mailbox resets.
func idleEnv(ctx context.Context, clears *int) Env {
	return Env{
		Feed: feed.Env{
			Open: func(uint32) (*emu.Dolphin, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			NewClient: NewPlayerClientCtx,
		},
		Clear: func() { *clears++ },
	}
}

// runWithin runs a session and fails the test if it doesn't return
// within five seconds.
func runWithin(t *testing.T, run func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not return within 5s")
		return nil
	}
}

// TestRunJoinSessionCoop: a join session starts its Coop, hears the
// puppet-sync client through it, runs its pipelines next to the pose
// pipelines (one failing ends the session with that failure), and resets
// the mailbox and the status bridge when it ends.
func TestRunJoinSessionCoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clears := 0
	coop := &fakeCoop{fail: errors.New("boom")}
	ls := &LiveState{}
	err := runWithin(t, func() error {
		return RunJoinSession(ctx, cancel, idleEnv(ctx, &clears), "127.0.0.1", "Link", log.Discard{}, ls, coop)
	})
	if err == nil || err.Error() != "coop-sync: boom" {
		t.Fatalf("RunJoinSession = %v, want the coop-sync failure", err)
	}
	want := []string{"start-join", "pose-client Link", "pipelines Link 127.0.0.1:25565"}
	if got := coop.got(); !slices.Equal(got, want) {
		t.Errorf("coop calls %q, want %q", got, want)
	}
	if clears != 1 {
		t.Errorf("mailbox reset %d times, want once", clears)
	}
	if ls.PlayerCount() != 0 || ls.HostIPList() != nil {
		t.Error("the status bridge was not reset")
	}
}

// TestRunHostSessionCoop: a host session starts its Coop before the
// server with the server to set its hub on, and stops it after the
// server; a failed start ends the session before the server starts.
func TestRunHostSessionCoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clears := 0
	coop := &fakeCoop{fail: errors.New("boom")}
	err := runWithin(t, func() error {
		return RunHostSession(ctx, cancel, idleEnv(ctx, &clears), "Host", 0, log.Discard{}, nil, coop)
	})
	if err == nil || err.Error() != "coop-sync: boom" {
		t.Fatalf("RunHostSession = %v, want the coop-sync failure", err)
	}
	want := []string{"start-host", "pose-client Host", "pipelines Host localhost:0", "stop"}
	if got := coop.got(); !slices.Equal(got, want) || coop.srv == nil {
		t.Errorf("coop calls %q (server %v), want %q with the server", got, coop.srv != nil, want)
	}
	if clears != 1 {
		t.Errorf("mailbox reset %d times, want once", clears)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	refused := &fakeCoop{startErr: errors.New("no world")}
	err = runWithin(t, func() error {
		return RunHostSession(ctx2, cancel2, idleEnv(ctx2, &clears), "Host", 0, log.Discard{}, nil, refused)
	})
	if err == nil || err.Error() != "no world" {
		t.Fatalf("RunHostSession with a failed co-op start = %v, want it", err)
	}
	if got := refused.got(); !slices.Equal(got, []string{"start-host"}) || clears != 1 {
		t.Errorf("after a failed start: coop calls %q, resets %d; want only the start, no reset", got, clears)
	}
}

// TestRunJoinSessionNoCoop: without co-op (a nil Coop) a session runs the
// two pose pipelines alone and ends cleanly on cancel.
func TestRunJoinSessionNoCoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clears := 0
	time.AfterFunc(50*time.Millisecond, cancel)
	err := runWithin(t, func() error {
		return RunJoinSession(ctx, cancel, idleEnv(ctx, &clears), "127.0.0.1:25570", "", log.Discard{}, nil, nil)
	})
	if err != nil {
		t.Fatalf("RunJoinSession after cancel = %v, want nil", err)
	}
	if clears != 1 {
		t.Errorf("mailbox reset %d times, want once", clears)
	}
}
