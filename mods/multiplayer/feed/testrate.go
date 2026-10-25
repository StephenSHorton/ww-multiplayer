package feed

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// TestRate is a dev knob for measuring the receiver's smoothing (wwdev's
// WW_POSE_TEST_RATE; the shipped binary never sets it): BroadcastPose
// samples this Dolphin's Link Hz times a second instead of every 16 ms,
// and sends each sample Latency plus up to Jitter after it was taken,
// still in order (a sample never overtakes the one before), the way a
// network with that latency and jitter delivers a TCP stream. After
// datstarkey/ww-online's COS_MP_TEST_RATE=<hz>:<jitter ms>:<latency ms>
// (seaborne/native/src/mods/ww_online/README.md, "Smooth peers"; idea
// only, no code). wwdev's smooth-steps and smoke --smooth measure what
// the remote's game draws under it.
type TestRate struct {
	Hz      float64
	Jitter  time.Duration
	Latency time.Duration
	// Rand returns a number in [0, 1) for each sample's jitter; nil is
	// math/rand/v2's Float64.
	Rand func() float64
}

// Limits on a test rate: at least one sample a second, at most one a
// millisecond (the send loop's poll); a delay past 5 s is a stall, which
// the freeze and despawn knobs already cover.
const (
	testRateMinHz    = 1
	testRateMaxHz    = 1000
	testRateMaxDelay = 5 * time.Second
)

// ParseTestRate parses `<hz>[:<jitter ms>[:<latency ms>]]`, e.g. 20:40:20
// (their proof run's). "" is no test rate (nil, nil).
func ParseTestRate(s string) (*TestRate, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return nil, fmt.Errorf("pose test rate %q: want <hz>[:<jitter ms>[:<latency ms>]]", s)
	}
	hz, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || hz < testRateMinHz || hz > testRateMaxHz {
		return nil, fmt.Errorf("pose test rate %q: the rate must be %d to %d Hz", s, testRateMinHz, testRateMaxHz)
	}
	r := &TestRate{Hz: hz}
	ms := func(i int, name string) (time.Duration, error) {
		if i >= len(parts) {
			return 0, nil
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
		d := time.Duration(v * float64(time.Millisecond))
		if err != nil || v < 0 || d > testRateMaxDelay {
			return 0, fmt.Errorf("pose test rate %q: the %s must be 0 to %d ms", s, name, testRateMaxDelay.Milliseconds())
		}
		return d, nil
	}
	if r.Jitter, err = ms(1, "jitter"); err != nil {
		return nil, err
	}
	if r.Latency, err = ms(2, "latency"); err != nil {
		return nil, err
	}
	return r, nil
}

// String is r as ParseTestRate reads it.
func (r *TestRate) String() string {
	return fmt.Sprintf("%g:%g:%g", r.Hz, float64(r.Jitter)/float64(time.Millisecond), float64(r.Latency)/float64(time.Millisecond))
}

// period is the time between samples.
func (r *TestRate) period() time.Duration {
	return time.Duration(float64(time.Second) / r.Hz)
}

// testQueue holds a TestRate's samples until they are due. It is not
// safe for concurrent use.
type testQueue[T any] struct {
	rate    *TestRate
	next    time.Time // when the next sample is taken; zero: now
	lastDue time.Time
	items   []testItem[T]
}

type testItem[T any] struct {
	due time.Time
	v   T
}

func newTestQueue[T any](r *TestRate) *testQueue[T] { return &testQueue[T]{rate: r} }

// sampleDue reports whether a sample should be taken at now.
func (q *testQueue[T]) sampleDue(now time.Time) bool {
	return q.next.IsZero() || !now.Before(q.next)
}

// push queues v, taken at now: due Latency plus up to Jitter later, but
// never before the sample before it. The next sample is one period after
// the last one was due to be taken (or now, after a stall).
func (q *testQueue[T]) push(now time.Time, v T) {
	rnd := q.rate.Rand
	if rnd == nil {
		rnd = rand.Float64
	}
	due := now.Add(q.rate.Latency + time.Duration(rnd()*float64(q.rate.Jitter)))
	if due.Before(q.lastDue) {
		due = q.lastDue
	}
	q.lastDue = due
	q.items = append(q.items, testItem[T]{due: due, v: v})
	p := q.rate.period()
	if q.next.IsZero() || now.Sub(q.next) > p {
		q.next = now.Add(p)
	} else {
		q.next = q.next.Add(p)
	}
}

// pop returns the samples due at now, oldest first, and drops them.
func (q *testQueue[T]) pop(now time.Time) []T {
	n := 0
	for n < len(q.items) && !now.Before(q.items[n].due) {
		n++
	}
	if n == 0 {
		return nil
	}
	out := make([]T, n)
	for i := range n {
		out[i] = q.items[i].v
	}
	q.items = append(q.items[:0], q.items[n:]...)
	return out
}
