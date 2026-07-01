package network

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunSessionWaitsForTeardown is the #1 regression: RunSession must not
// return until its ctx watcher's teardown has fully completed. Otherwise a
// stale watcher from session N can run its (session-agnostic) teardown after
// the reconnect loop has dialed session N+1, closing the fresh conn — a
// self-chaining flap. The teardown sleeps so a missing wait is observable.
func TestRunSessionWaitsForTeardown(t *testing.T) {
	var teardownDone atomic.Bool
	var teardownCount atomic.Int64
	teardown := func() {
		time.Sleep(10 * time.Millisecond) // make a missing wg.Wait observable
		teardownCount.Add(1)
		teardownDone.Store(true)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = RunSession(context.Background(), teardown, func(ctx context.Context) error {
			return nil // session drops immediately
		})
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("RunSession did not return within 1s (hang?)")
	}

	if !teardownDone.Load() {
		t.Fatal("RunSession returned before teardown completed (stale-watcher window open)")
	}
	if got := teardownCount.Load(); got != 1 {
		t.Errorf("teardown ran %d times, want exactly 1", got)
	}
}

// TestRunSessionParentCancelTearsDown proves the Ctrl+C path: a parent cancel
// unblocks fn (via the child ctx) and triggers teardown.
func TestRunSessionParentCancelTearsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var tornDown atomic.Bool

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = RunSession(ctx, func() { tornDown.Store(true) }, func(ctx context.Context) error {
			<-ctx.Done() // block until the parent cancel propagates
			return ctx.Err()
		})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("RunSession did not return after parent cancel")
	}
	if !tornDown.Load() {
		t.Fatal("parent cancel did not trigger teardown")
	}
}

// TestRunSessionNoOverlapAcrossReconnects models the exact production loop
// (ReconnectLoop driving a RunSession-wrapped run) against a fake client
// whose teardown closes the CURRENT session. It asserts that after several
// reconnects a stale watcher never closes the session that's currently live.
func TestRunSessionNoOverlapAcrossReconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	openSession := 0    // id of the session currently "open" (0 = none)
	nextID := 0
	sessions := 0
	killedLive := false // set if teardown ever closed the live session

	connect := func() error {
		mu.Lock()
		nextID++
		openSession = nextID
		mu.Unlock()
		return nil
	}
	// teardown is session-agnostic (like Client.Disconnect): it closes
	// whatever session is currently open.
	teardown := func() {
		mu.Lock()
		openSession = 0
		mu.Unlock()
	}
	run := func(ctx context.Context) error {
		return RunSession(ctx, teardown, func(ctx context.Context) error {
			mu.Lock()
			myID := openSession
			mu.Unlock()
			time.Sleep(2 * time.Millisecond) // let a stale watcher (if any) race
			mu.Lock()
			// If a stale watcher had closed us and a new session opened,
			// openSession would differ from myID.
			if openSession != myID {
				killedLive = true
			}
			sessions++
			stop := sessions >= 5
			mu.Unlock()
			if stop {
				cancel()
			}
			return nil
		})
	}
	backoff := func(int) time.Duration { return 0 }
	sleep := func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	doneCh := make(chan struct{})
	go func() { defer close(doneCh); _ = ReconnectLoop(ctx, connect, run, backoff, sleep) }()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect loop hung")
	}
	if killedLive {
		t.Fatal("a stale watcher closed the currently-live session (overlap race)")
	}
}

func TestNextBackoffUnjittered(t *testing.T) {
	base := 100 * time.Millisecond
	capDur := 8000 * time.Millisecond
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3200 * time.Millisecond,
		6400 * time.Millisecond,
		8000 * time.Millisecond, // 12800 capped
		8000 * time.Millisecond,
		8000 * time.Millisecond,
	}
	for attempt, w := range want {
		got := NextBackoff(attempt, base, capDur, nil)
		if got != w {
			t.Errorf("NextBackoff(%d) = %v, want %v", attempt, got, w)
		}
	}
}

func TestNextBackoffJitterWithinTwentyPercent(t *testing.T) {
	base := 100 * time.Millisecond
	capDur := 8000 * time.Millisecond
	rng := rand.New(rand.NewSource(1)) // fixed seed → deterministic
	for attempt := 0; attempt < 12; attempt++ {
		nominal := NextBackoff(attempt, base, capDur, nil)
		lo := time.Duration(float64(nominal) * 0.8)
		hi := time.Duration(float64(nominal) * 1.2)
		// A handful of draws per attempt to exercise the jitter range.
		for i := 0; i < 50; i++ {
			got := NextBackoff(attempt, base, capDur, rng)
			if got < lo || got > hi {
				t.Fatalf("attempt %d: jittered %v outside ±20%% of %v ([%v,%v])",
					attempt, got, nominal, lo, hi)
			}
		}
	}
}

// TestReconnectLoop verifies the connect→run→reconnect lifecycle: backoff
// grows across consecutive connect failures, a HEALTHY session (run that
// lived >= minHealthySession) resets the attempt counter to 0, and a
// ctx-cancel during run ends the loop with nil and no further sleep. The
// fake clock is advanced by run() to make each session look healthy without
// wall-clock waits. Wrapped in a 1s deadline so a regression can't hang.
func TestReconnectLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var clock time.Time
	now := func() time.Time { return clock }

	var connectCalls, runCalls int
	connect := func() error {
		connectCalls++
		// Fail attempts 1,2 and 4; succeed on 3 and 5. The fail at 4 proves
		// the attempt counter reset after the healthy session at 3.
		switch connectCalls {
		case 1, 2, 4:
			return errors.New("connect failed")
		default:
			return nil
		}
	}
	run := func(ctx context.Context) error {
		runCalls++
		clock = clock.Add(time.Hour) // healthy session (>> minHealthySession)
		if runCalls == 2 {
			cancel() // end the loop after the second live session
		}
		return nil
	}
	var attempts []int
	backoff := func(attempt int) time.Duration {
		attempts = append(attempts, attempt)
		return time.Duration(attempt) * time.Millisecond
	}
	sleeps := 0
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleeps++
		return ctx.Err() // never actually waits
	}

	done := make(chan error, 1)
	go func() { done <- reconnectLoop(ctx, connect, run, backoff, sleep, now) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reconnectLoop returned %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("reconnectLoop did not return within 1s (hang?)")
	}

	if want := []int{1, 2, 1}; !reflect.DeepEqual(attempts, want) {
		t.Errorf("backoff attempts = %v, want %v (counter must reset to 0 after a healthy session)", attempts, want)
	}
	if runCalls != 2 {
		t.Errorf("runCalls = %d, want 2", runCalls)
	}
	if connectCalls != 5 {
		t.Errorf("connectCalls = %d, want 5", connectCalls)
	}
	if sleeps != 3 {
		t.Errorf("sleeps = %d, want 3 (no sleep after ctx cancel)", sleeps)
	}
}

// TestReconnectLoopStormFloor is the #3 regression: connect always succeeds
// but run() returns immediately (a flap — server accepts-then-RSTs, or the
// >5-send-error bail). Each near-zero session must be treated as a FAILED
// attempt (attempt++ + backoff) rather than resetting to 0 and hot-spinning.
// The clock never advances, so every session is below minHealthySession.
func TestReconnectLoopStormFloor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := func() time.Time { return time.Time{} } // clock never advances

	connectCalls := 0
	connect := func() error { connectCalls++; return nil } // always succeeds
	runCalls := 0
	run := func(ctx context.Context) error {
		runCalls++
		if runCalls >= 4 {
			cancel() // stop after enough flaps to prove escalation
		}
		return nil // 0-duration session
	}
	var attempts []int
	backoff := func(attempt int) time.Duration {
		attempts = append(attempts, attempt)
		return 0
	}
	sleeps := 0
	sleep := func(ctx context.Context, _ time.Duration) error {
		sleeps++
		return ctx.Err()
	}

	done := make(chan error, 1)
	go func() { done <- reconnectLoop(ctx, connect, run, backoff, sleep, now) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reconnectLoop returned %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("reconnectLoop hot-spun or hung (storm floor missing?)")
	}

	// Each flap → one escalating backoff attempt (never a hot reset-to-0
	// spin). run #4 cancels, so its post-run branch returns before sleeping.
	if want := []int{1, 2, 3}; !reflect.DeepEqual(attempts, want) {
		t.Errorf("backoff attempts = %v, want %v (each flap must back off, escalating)", attempts, want)
	}
	if sleeps != 3 {
		t.Errorf("sleeps = %d, want 3", sleeps)
	}
	if runCalls != 4 {
		t.Errorf("runCalls = %d, want 4", runCalls)
	}
}

func TestReconnectLoopReturnsNilOnPreCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	connect := func() error { t.Fatal("connect should not be called"); return nil }
	run := func(context.Context) error { t.Fatal("run should not be called"); return nil }
	backoff := func(int) time.Duration { return 0 }
	sleep := func(context.Context, time.Duration) error { return nil }

	if err := ReconnectLoop(ctx, connect, run, backoff, sleep); err != nil {
		t.Fatalf("ReconnectLoop on cancelled ctx = %v, want nil", err)
	}
}

func TestPoseLiveness(t *testing.T) {
	const freeze = 2 * time.Second
	const despawn = 5 * time.Second
	base := time.Now()

	cases := []struct {
		name                      string
		last                      time.Time
		now                       time.Time
		wantFrozen, wantDespawned bool
	}{
		{"fresh", base, base, false, false},
		{"3s_frozen", base, base.Add(3 * time.Second), true, false},
		{"6s_despawned", base, base.Add(6 * time.Second), true, true},
		{"refresh", base.Add(6 * time.Second), base.Add(6 * time.Second), false, false},
		{"exactly_freeze", base, base.Add(freeze), true, false},
		{"exactly_despawn", base, base.Add(despawn), true, true},
		{"zero_lastpose", time.Time{}, base, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frozen, despawned := PoseLiveness(c.last, c.now, freeze, despawn)
			if frozen != c.wantFrozen || despawned != c.wantDespawned {
				t.Errorf("PoseLiveness = (%v,%v), want (%v,%v)",
					frozen, despawned, c.wantFrozen, c.wantDespawned)
			}
		})
	}
}
