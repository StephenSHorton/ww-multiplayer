package replica

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// breakerClock is a clock tests move by hand.
type breakerClock struct {
	mu sync.Mutex
	t  time.Time
}

func newBreakerClock() *breakerClock {
	return &breakerClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *breakerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *breakerClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func chanClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestBreakerThresholds(t *testing.T) {
	type step struct {
		after time.Duration // the clock moves this much first
		do    string        // "verify", "refuse", "regress"
		off   bool          // co-op is off after the step
	}
	n := func(k int, do string, every time.Duration, offAtLast bool) []step {
		var s []step
		for i := range k {
			s = append(s, step{after: every, do: do, off: offAtLast && i == k-1})
		}
		return s
	}
	cat := func(parts ...[]step) []step {
		var s []step
		for _, p := range parts {
			s = append(s, p...)
		}
		return s
	}
	cases := []struct {
		name  string
		steps []step
		why   string
	}{
		{"eight verify failures are tolerated", n(8, "verify", time.Second, false), ""},
		{"the ninth in a minute trips", n(9, "verify", time.Second, true), "9 write faults in 1m0s (9 verify failures, 0 WriteGuard refusals)"},
		{"verify failures and refusals count together",
			cat(n(5, "verify", time.Second, false), n(4, "refuse", time.Second, true)),
			"9 write faults in 1m0s (5 verify failures, 4 WriteGuard refusals)"},
		{"faults older than a minute drop out",
			cat(n(8, "refuse", time.Second, false), []step{{after: 61 * time.Second, do: "refuse"}}, n(7, "refuse", time.Second, false)), ""},
		{"a fault exactly a minute old has dropped out",
			cat(n(1, "verify", 0, false), n(7, "verify", time.Second, false), []step{{after: 53 * time.Second, do: "verify"}},
				[]step{{do: "verify", off: true}}),
			"9 write faults"},
		{"four save-state loads are tolerated", n(4, "regress", time.Second, false), ""},
		{"the fifth in a minute is a storm", n(5, "regress", time.Second, true), "5 save-state loads in 1m0s"},
		{"loads spread over minutes are no storm", n(20, "regress", 16*time.Second, false), ""},
		{"loads and faults do not mix", cat(n(4, "regress", 0, false), n(8, "verify", 0, false)), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := newBreakerClock()
			var log logLines
			b := NewBreaker(BreakerConfig{Now: clk.Now, Logf: log.logf})
			done := b.Done()
			for i, s := range c.steps {
				clk.advance(s.after)
				var off bool
				switch s.do {
				case "verify":
					off = b.Fault(FaultVerify, "a byte did not hold")
				case "refuse":
					off = b.Fault(FaultRefusal, "mLife")
				case "regress":
					off = b.Regressed()
				}
				if off != s.off || b.Off() != s.off {
					t.Fatalf("step %d (%s): off %v, want %v (%s)", i, s.do, off, s.off, b.Why())
				}
			}
			why := b.Why()
			if c.why == "" {
				if why != "" || b.Err() != nil || chanClosed(done) {
					t.Fatalf("off: %s", why)
				}
				return
			}
			if !strings.Contains(why, c.why) || !strings.HasPrefix(why, "co-op turned itself off: ") {
				t.Fatalf("why %q, want it to say %q", why, c.why)
			}
			var de *DisabledError
			if err := b.Err(); !errors.Is(err, ErrOff) || !errors.As(err, &de) || de.Why != why {
				t.Fatalf("Err = %v", err)
			}
			if !chanClosed(done) || log.count("turned itself off") != 1 {
				t.Fatalf("done closed %v; log:\n%s", chanClosed(done), &log)
			}
		})
	}
}

func TestBreakerOffStaysOff(t *testing.T) {
	cases := []struct {
		name string
		trip func(b *Breaker)
		why  string
	}{
		{"a table-hash mismatch", func(b *Breaker) { b.Mismatch("the host's tables differ from this build's") },
			"co-op turned itself off: the host's tables differ from this build's"},
		{"coop off", func(b *Breaker) { b.TurnOff("co-op turned off by the user") }, "co-op turned off by the user"},
		{"coop off without a reason", func(b *Breaker) { b.TurnOff("") }, "co-op was turned off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewBreaker(BreakerConfig{})
			done := b.Done()
			c.trip(b)
			if !b.Off() || b.Why() != c.why || !chanClosed(done) {
				t.Fatalf("off %v, why %q", b.Off(), b.Why())
			}
			// Later trips keep the first reason, and count nothing.
			b.TurnOff("again")
			b.Mismatch("layout")
			for range 20 {
				if !b.Fault(FaultVerify, "x") || !b.Regressed() {
					t.Fatal("a fault while off reports on")
				}
			}
			if b.Why() != c.why {
				t.Fatalf("why became %q", b.Why())
			}

			b.Reset()
			if b.Off() || b.Why() != "" || b.Err() != nil || chanClosed(b.Done()) || !chanClosed(done) {
				t.Fatal("Reset did not turn co-op back on with a fresh Done")
			}
			// The windows start empty again.
			for range MaxFaultsPerMinute {
				if b.Fault(FaultRefusal, "x") {
					t.Fatal("tripped on old faults after Reset")
				}
			}
			b.Reset() // on: a no-op
			if b.Off() {
				t.Fatal("Reset of a breaker that is on turned it off")
			}
		})
	}
}

func TestBreakerConfig(t *testing.T) {
	clk := newBreakerClock()
	b := NewBreaker(BreakerConfig{MaxFaults: 2, MaxRegressions: 1, Window: 10 * time.Second, Now: clk.Now})
	b.Fault(FaultVerify, "a")
	b.Fault(FaultVerify, "b")
	clk.advance(11 * time.Second)
	if b.Fault(FaultVerify, "c") || b.Fault(FaultVerify, "d") {
		t.Fatal("tripped on faults outside the window")
	}
	if !b.Fault(FaultVerify, "e") || !strings.Contains(b.Why(), "3 write faults in 10s") || !strings.Contains(b.Why(), "the last: e") {
		t.Fatalf("why %q", b.Why())
	}
	b.Reset()
	if b.Regressed() || !b.Regressed() {
		t.Fatalf("MaxRegressions 1: why %q", b.Why())
	}
}

// TestBreakerConcurrentFaults trips the breaker from many goroutines at
// once: it trips once, logs once, and every caller sees it off after.
func TestBreakerConcurrentFaults(t *testing.T) {
	var log logLines
	b := NewBreaker(BreakerConfig{Logf: log.logf})
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				b.Fault(FaultVerify, "v")
			} else {
				b.Regressed()
			}
			_ = b.Off()
		}()
	}
	wg.Wait()
	if !b.Off() || log.count("turned itself off") != 1 || !chanClosed(b.Done()) {
		t.Fatalf("off %v; log:\n%s", b.Off(), &log)
	}
}
