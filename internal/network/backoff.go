package network

import (
	"context"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// RunSession runs fn(ctx) for one connection lifetime with a ctx-scoped
// teardown watcher, and GUARANTEES the watcher's teardown has completed
// before RunSession returns.
//
// This is what makes a reconnect loop safe to call in a tight loop: teardown
// is typically a session-agnostic Client.Disconnect(), so a stale watcher
// from the prior session that ran AFTER the next connect would close the
// freshly-dialed conn — a self-chaining flap. RunSession derives a child ctx,
// lets the watcher be the SOLE teardown caller, and on return cancels the
// child (unblocking the watcher's teardown of THIS session) then waits for
// it, so no teardown ever straddles the next session.
func RunSession(ctx context.Context, teardown func(), fn func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		teardown()
	}()
	defer func() {
		cancel()
		wg.Wait()
	}()
	return fn(ctx)
}

// NextBackoff returns the delay for the given (1-based or 0-based) retry
// attempt: min(base*2^attempt, capDur), then ±20% jitter drawn from rng.
//
// Pass a nil rng to get the un-jittered, deterministic value (handy for
// tests and for callers that don't care about thundering-herd dispersion).
// The jitter is intentionally applied via an injected *rand.Rand so two
// co-located processes (e.g. a host running both a sender and receiver)
// don't reconnect in lockstep.
func NextBackoff(attempt int, base, capDur time.Duration, rng *rand.Rand) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= capDur {
			d = capDur
			break
		}
	}
	if d > capDur {
		d = capDur
	}
	if rng != nil {
		// jitter factor in [0.8, 1.2): ±20% around the nominal delay.
		factor := 1 + (rng.Float64()*0.4 - 0.2)
		d = time.Duration(float64(d) * factor)
	}
	return d
}

// backoffSeedCounter disambiguates two NewBackoff() calls that happen in the
// same nanosecond (the sender and receiver loops are spun up back-to-back).
var backoffSeedCounter atomic.Int64

// NewBackoff returns a backoff function bound to its OWN seeded *rand.Rand.
// Each call mints an independent generator so concurrent reconnect loops in
// the same process can call their backoff funcs without sharing a (non
// concurrency-safe) rng — and so co-located processes, seeded off wall clock
// + PID, don't march in lockstep. It uses the package backoffBase/backoffCap
// defaults (the latter env-overridable via WW_BACKOFF_MAX_SECS).
func NewBackoff() func(attempt int) time.Duration {
	seed := time.Now().UnixNano() ^ (int64(os.Getpid()) << 32) ^ backoffSeedCounter.Add(1)
	rng := rand.New(rand.NewSource(seed))
	return func(attempt int) time.Duration {
		return NextBackoff(attempt, backoffBase, backoffCap, rng)
	}
}

// SleepCtx sleeps for d or until ctx is cancelled, whichever comes first.
// Returns ctx.Err() if cancelled (so callers can distinguish a real wait
// from a shutdown), nil if the full duration elapsed. Injected into
// ReconnectLoop so tests can fast-forward without wall-clock waits.
func SleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ReconnectLoop drives the connect → run → (drop) → reconnect lifecycle with
// exponential backoff between failed connects and a floor against flap storms.
//
//   - connect establishes a session (dial + handshake). On failure the loop
//     increments the attempt counter and sleeps backoff(attempt).
//   - run owns the live session and blocks until the connection drops (it
//     returns) or ctx is cancelled. Its return value (drop vs. error) is not
//     distinguished — either way the loop reconnects, UNLESS ctx is done.
//   - A run that lived at least minHealthySession counts as a healthy session
//     and resets the attempt counter to 0. A shorter run is treated as a
//     failed attempt (attempt++ then backoff) so a server that accepts-then-
//     drops can't drive a hot connect→run→connect spin.
//   - ctx cancellation ends the loop and returns nil (a clean shutdown).
//
// sleep is injectable purely for testability; production callers pass
// SleepCtx. backoff is typically a NewBackoff() closure.
func ReconnectLoop(
	ctx context.Context,
	connect func() error,
	run func(context.Context) error,
	backoff func(attempt int) time.Duration,
	sleep func(context.Context, time.Duration) error,
) error {
	return reconnectLoop(ctx, connect, run, backoff, sleep, time.Now)
}

// reconnectLoop is the clock-injectable core of ReconnectLoop. Tests pass a
// fake `now` so they can control session duration without wall-clock waits.
func reconnectLoop(
	ctx context.Context,
	connect func() error,
	run func(context.Context) error,
	backoff func(attempt int) time.Duration,
	sleep func(context.Context, time.Duration) error,
	now func() time.Time,
) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := connect(); err != nil {
			attempt++
			if serr := sleep(ctx, backoff(attempt)); serr != nil {
				// ctx cancelled during the backoff wait → clean shutdown.
				return nil
			}
			continue
		}
		start := now()
		_ = run(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if now().Sub(start) >= minHealthySession {
			// Healthy session that dropped → reconnect promptly, no backoff.
			attempt = 0
			continue
		}
		// Flap: run returned almost immediately. Treat it as a failed
		// attempt so the loop backs off instead of hot-spinning.
		attempt++
		if serr := sleep(ctx, backoff(attempt)); serr != nil {
			return nil
		}
	}
}

// PoseLiveness classifies how stale a remote's last-received pose is.
//
//   - frozen   == age >= freezeAfter   (stop advancing the pose; the C side
//     holds the last frame so the remote Link visually freezes)
//   - despawned == age >= despawnAfter (clear the slot; the remote vanishes)
//
// A zero lastPose (no pose ever received) is neither frozen nor despawned —
// there's nothing to freeze yet. Pure + table-tested.
func PoseLiveness(lastPose, now time.Time, freezeAfter, despawnAfter time.Duration) (frozen, despawned bool) {
	if lastPose.IsZero() {
		return false, false
	}
	age := now.Sub(lastPose)
	return age >= freezeAfter, age >= despawnAfter
}
