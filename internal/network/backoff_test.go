package network

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

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
// grows across consecutive connect failures, the attempt counter resets to 0
// after a successful connect, and a ctx-cancel during run ends the loop with
// nil and no further sleep. Wrapped in a 1s deadline so a regression can't
// hang the suite.
func TestReconnectLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var connectCalls, runCalls int
	connect := func() error {
		connectCalls++
		// Fail attempts 1,2 and 4; succeed on 3 and 5. The fail at 4 proves
		// the attempt counter reset after the success at 3.
		switch connectCalls {
		case 1, 2, 4:
			return errors.New("connect failed")
		default:
			return nil
		}
	}
	run := func(ctx context.Context) error {
		runCalls++
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
	go func() { done <- ReconnectLoop(ctx, connect, run, backoff, sleep) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ReconnectLoop returned %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("ReconnectLoop did not return within 1s (hang?)")
	}

	if want := []int{1, 2, 1}; !reflect.DeepEqual(attempts, want) {
		t.Errorf("backoff attempts = %v, want %v (counter must reset to 0 after a success)", attempts, want)
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
