package feed

import (
	"testing"
	"time"
)

func TestParseTestRate(t *testing.T) {
	for _, c := range []struct {
		in        string
		hz        float64
		jit, lat  time.Duration
		wantError bool
	}{
		{in: "20:40:20", hz: 20, jit: 40 * time.Millisecond, lat: 20 * time.Millisecond},
		{in: "30", hz: 30},
		{in: " 60 : 12.5 ", hz: 60, jit: 12500 * time.Microsecond},
		{in: "0", wantError: true},
		{in: "2000", wantError: true},
		{in: "20:-1", wantError: true},
		{in: "20:40:6000", wantError: true},
		{in: "20:40:20:1", wantError: true},
		{in: "fast", wantError: true},
	} {
		r, err := ParseTestRate(c.in)
		if c.wantError {
			if err == nil {
				t.Errorf("%q: no error, got %v", c.in, r)
			}
			continue
		}
		if err != nil || r == nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if r.Hz != c.hz || r.Jitter != c.jit || r.Latency != c.lat {
			t.Errorf("%q: got %v Hz, %v, %v", c.in, r.Hz, r.Jitter, r.Latency)
		}
	}
	if r, err := ParseTestRate(""); r != nil || err != nil {
		t.Errorf(`"": got %v, %v; want nil, nil`, r, err)
	}
	r, _ := ParseTestRate("20:40:20")
	if got := r.String(); got != "20:40:20" {
		t.Errorf("String: %q", got)
	}
}

// TestTestQueue: samples are taken once a period, each comes out
// Latency plus its jitter later, and never before the one before it.
func TestTestQueue(t *testing.T) {
	jit := []float64{0.9, 0.0, 0.5, 0.1}
	n := 0
	r := &TestRate{Hz: 20, Jitter: 40 * time.Millisecond, Latency: 20 * time.Millisecond,
		Rand: func() float64 { v := jit[n%len(jit)]; n++; return v }}
	q := newTestQueue[int](r)
	t0 := time.Unix(1000, 0)
	var took []time.Duration
	out := map[int]time.Duration{}
	var order []int
	for ms := 0; ms <= 400; ms++ {
		now := t0.Add(time.Duration(ms) * time.Millisecond)
		if q.sampleDue(now) {
			q.push(now, len(took))
			took = append(took, now.Sub(t0))
		}
		for _, v := range q.pop(now) {
			out[v] = now.Sub(t0)
			order = append(order, v)
		}
	}
	if len(took) != 9 { // 0, 50, ..., 400 ms
		t.Fatalf("took %d samples (%v), want 9", len(took), took)
	}
	for i, at := range took {
		if want := time.Duration(i) * 50 * time.Millisecond; at != want {
			t.Errorf("sample %d at %v, want %v", i, at, want)
		}
	}
	// Sample 0: 20 + 36 = 56 ms. Sample 1: 50 + 20 + 0 = 70. Sample 2:
	// 100 + 20 + 20 = 140. Sample 3: 150 + 20 + 4 = 174.
	for i, want := range []time.Duration{56, 70, 140, 174} {
		if got := out[i]; got != want*time.Millisecond {
			t.Errorf("sample %d sent at %v, want %v ms", i, got, want)
		}
	}
	for i := 1; i < len(order); i++ {
		if order[i] != order[i-1]+1 {
			t.Fatalf("out of order: %v", order)
		}
	}
	// A sample that would overtake the one before it waits for it.
	r2 := &TestRate{Hz: 100, Jitter: 50 * time.Millisecond, Rand: func() float64 { return 0 }}
	q2 := newTestQueue[int](r2)
	r2.Rand = func() float64 { return 1 }
	q2.push(t0, 0) // due +50 ms
	r2.Rand = func() float64 { return 0 }
	q2.push(t0.Add(10*time.Millisecond), 1) // would be due +10 ms
	if got := q2.pop(t0.Add(20 * time.Millisecond)); len(got) != 0 {
		t.Errorf("popped %v before the first was due", got)
	}
	if got := q2.pop(t0.Add(50 * time.Millisecond)); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("popped %v at +50 ms, want [0 1]", got)
	}
}
