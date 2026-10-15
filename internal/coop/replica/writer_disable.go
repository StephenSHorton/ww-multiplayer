package replica

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// The auto-disable thresholds (§4.2). More than MaxFaultsPerMinute verify
// failures and WriteGuard refusals together in a minute means the writes
// are not landing or the planner is wrong; more than
// MaxRegressionsPerMinute heartbeat regressions means save states are
// being loaded faster than the replica can follow (the bindNonce stays
// the same through each, §5.5, so only the heartbeat shows it).
const (
	MaxFaultsPerMinute      = 8
	MaxRegressionsPerMinute = 4
)

// ErrOff is matched (errors.Is) by every write refused because co-op is
// off, whether the user, the session or the Breaker turned it off.
var ErrOff = errors.New("replica: co-op is off")

// DisabledError is what a write gets once co-op is off: Why is the reason
// the session prints.
type DisabledError struct {
	Why string
}

func (e *DisabledError) Error() string { return fmt.Sprintf("%v: %s", ErrOff, e.Why) }

// Is makes errors.Is(err, ErrOff) hold.
func (e *DisabledError) Is(target error) bool { return target == ErrOff }

// FaultKind is what went wrong with a write.
type FaultKind uint8

const (
	FaultVerify  FaultKind = iota + 1 // a written byte did not read back on the next snapshot
	FaultRefusal                      // the WriteGuard refused a write
)

func (k FaultKind) String() string {
	switch k {
	case FaultVerify:
		return "verify failure"
	case FaultRefusal:
		return "WriteGuard refusal"
	}
	return fmt.Sprintf("FaultKind(%d)", uint8(k))
}

// BreakerConfig sets up a Breaker.
type BreakerConfig struct {
	// MaxFaults: more than this many faults (verify failures and
	// refusals together) within Window turn co-op off. 0 is
	// MaxFaultsPerMinute.
	MaxFaults int
	// MaxRegressions: more than this many heartbeat regressions within
	// Window turn co-op off. 0 is MaxRegressionsPerMinute.
	MaxRegressions int
	// Window is the sliding window both count over; 0 is a minute.
	Window time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Logf gets one line when co-op turns off; nil discards it.
	Logf func(format string, args ...any)
}

// Breaker is co-op's off switch (§4.2 auto-disable). It trips, turning
// co-op off with a reason the session can print, on:
//
//   - more than MaxFaults verify failures and WriteGuard refusals in the
//     window;
//   - a table-hash or layout mismatch (Mismatch), at once;
//   - a pending counter the derive path wrote that the HUD never took
//     (Undrained), at once: the write went where the meter does not read;
//   - more than MaxRegressions heartbeat regressions in the window: a
//     storm of save-state loads;
//   - TurnOff: the user's or the session's "coop off".
//
// Once off it stays off, with the first reason, until Reset. The
// WriteGuard refuses every write while it is off and a FrameWriter stops
// at its next byte. A Breaker is safe for concurrent use.
type Breaker struct {
	maxFaults, maxRegressions int
	window                    time.Duration
	now                       func() time.Time
	logf                      func(format string, args ...any)

	off atomic.Bool // mirrors why != "", for the writer's per-byte check

	mu          sync.Mutex
	why         string
	done        chan struct{}
	faults      []fault
	regressions []time.Time
}

type fault struct {
	at     time.Time
	kind   FaultKind
	detail string
}

// NewBreaker returns a Breaker with co-op on.
func NewBreaker(cfg BreakerConfig) *Breaker {
	b := &Breaker{
		maxFaults:      cfg.MaxFaults,
		maxRegressions: cfg.MaxRegressions,
		window:         cfg.Window,
		now:            cfg.Now,
		logf:           cfg.Logf,
		done:           make(chan struct{}),
	}
	if b.maxFaults <= 0 {
		b.maxFaults = MaxFaultsPerMinute
	}
	if b.maxRegressions <= 0 {
		b.maxRegressions = MaxRegressionsPerMinute
	}
	if b.window <= 0 {
		b.window = time.Minute
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.logf == nil {
		b.logf = func(string, ...any) {}
	}
	return b
}

// Fault records a verify failure or a refusal, detail saying which, and
// reports whether co-op is now off.
func (b *Breaker) Fault(kind FaultKind, detail string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why != "" {
		return true
	}
	now := b.now()
	b.faults = append(prune(b.faults, now.Add(-b.window), func(f fault) time.Time { return f.at }),
		fault{at: now, kind: kind, detail: detail})
	if len(b.faults) <= b.maxFaults {
		return false
	}
	var verify, refused int
	for _, f := range b.faults {
		if f.kind == FaultVerify {
			verify++
		} else {
			refused++
		}
	}
	b.trip(fmt.Sprintf("co-op turned itself off: %d write faults in %v (%d verify failures, %d WriteGuard refusals); the last: %s",
		len(b.faults), b.window, verify, refused, detail))
	return true
}

// Regressed records a heartbeat regression, a save-state load (gate
// Status.Regressed), and reports whether co-op is now off.
func (b *Breaker) Regressed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why != "" {
		return true
	}
	now := b.now()
	b.regressions = append(prune(b.regressions, now.Add(-b.window), func(t time.Time) time.Time { return t }), now)
	if len(b.regressions) <= b.maxRegressions {
		return false
	}
	b.trip(fmt.Sprintf("co-op turned itself off: %d save-state loads in %v (the heartbeat went backwards each time); "+
		"load one state, then restart co-op", len(b.regressions), b.window))
	return true
}

// Mismatch turns co-op off at once for a table-hash or layout mismatch:
// what says which.
func (b *Breaker) Mismatch(what string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why == "" {
		b.trip("co-op turned itself off: " + what)
	}
}

// Undrained turns co-op off at once for a pending counter the derive path
// wrote that the HUD did not take (FrameWriter.ApplyDerived's drain
// watch): detail says which. The meter takes a counter in the frame after
// a write, so one left standing means the address, or the game's own
// state, is not what co-op took it for, and every later write is
// suspect.
func (b *Breaker) Undrained(detail string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why == "" {
		b.trip("co-op turned itself off: " + detail)
	}
}

// TurnOff turns co-op off for why ("coop off"). A Breaker already off
// keeps its first reason.
func (b *Breaker) TurnOff(why string) {
	if why == "" {
		why = "co-op was turned off"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why == "" {
		b.trip(why)
	}
}

// trip turns co-op off. b.mu is held and b.why is "".
func (b *Breaker) trip(why string) {
	b.why = why
	b.off.Store(true)
	close(b.done)
	b.logf("%s", why)
}

// Reset turns co-op back on with empty windows: the user's "coop on".
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why == "" {
		return
	}
	b.why, b.faults, b.regressions = "", nil, nil
	b.done = make(chan struct{})
	b.off.Store(false)
}

// Off reports whether co-op is off. It takes no lock.
func (b *Breaker) Off() bool { return b.off.Load() }

// Why returns why co-op is off, or "" while it is on.
func (b *Breaker) Why() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.why
}

// Err returns a *DisabledError while co-op is off, nil while it is on.
func (b *Breaker) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.why == "" {
		return nil
	}
	return &DisabledError{Why: b.why}
}

// Done returns a channel closed when co-op turns off. After a Reset, call
// it again for the new one.
func (b *Breaker) Done() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.done
}

// prune drops the entries of s at or before cutoff. s is in time order
// and reused.
func prune[T any](s []T, cutoff time.Time, at func(T) time.Time) []T {
	i := 0
	for i < len(s) && !at(s[i]).After(cutoff) {
		i++
	}
	return append(s[:0], s[i:]...)
}
