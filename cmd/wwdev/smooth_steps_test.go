package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// walkTrace is a synthetic walk along X: standing for 15 frames, then
// pos(f) each frame up to n, then standing again; frame f at 33.3 ms.
func walkTrace(n int, pos func(f int) float32) []stepSample {
	var ss []stepSample
	x := float32(0)
	for f := 0; f < n+30; f++ {
		switch {
		case f < 15:
		case f < n+15:
			x = pos(f - 15)
		}
		ss = append(ss, stepSample{Frame: uint32(1000 + f), MS: float64(f) * 1000 / 30, P: vec{x, 0, 0}})
	}
	return ss
}

func TestAnalyzeStepsSmooth(t *testing.T) {
	walker := walkTrace(120, func(f int) float32 { return 16.5 * float32(f) })
	// The remote: the same walk, 4 frames late.
	remote := walkTrace(120, func(f int) float32 { return 16.5 * float32(max(f-4, 0)) })
	s := analyzeSteps(walker, remote)
	if math.Abs(s.Walker.Mean-16.5) > 0.01 || s.Walker.SD > 0.01 {
		t.Errorf("walker %.3f +- %.3f, want 16.5 +- 0", s.Walker.Mean, s.Walker.SD)
	}
	if math.Abs(s.Ratio-1) > 0.01 || s.Remote.CV > 0.01 || s.Remote.Stalls != 0 || s.Remote.Jumps != 0 {
		t.Errorf("smooth remote: %s", s)
	}
	if math.Abs(s.Behind-66) > 5 { // 4 frames at 16.5
		t.Errorf("behind %.1f, want about 66", s.Behind)
	}
}

// TestAnalyzeStepsBursty: a remote that holds for a frame and catches up
// the next (arrivals bunched by jitter) shows stalls, jumps, a large cv
// and jerk, at the walker's mean.
func TestAnalyzeStepsBursty(t *testing.T) {
	walker := walkTrace(120, func(f int) float32 { return 16.5 * float32(f) })
	remote := walkTrace(120, func(f int) float32 { return 16.5 * float32(f-f%3) })
	s := analyzeSteps(walker, remote)
	if s.Remote.Stalls < 20 || s.Remote.Jumps < 10 || s.Remote.CV < 1 || s.Remote.Jerk < 20 {
		t.Errorf("bursty remote not caught: %s", s)
	}
	if math.Abs(s.Ratio-1) > 0.1 {
		t.Errorf("ratio %.3f, want about 1 (it keeps up on average)", s.Ratio)
	}
}

func TestAnalyzeStepsMissedFrames(t *testing.T) {
	walker := walkTrace(120, func(f int) float32 { return 10 * float32(f) })
	// The sampler saw every other remote frame: each gap spreads evenly.
	var remote []stepSample
	for i, s := range walker {
		if i%2 == 0 {
			remote = append(remote, s)
		}
	}
	s := analyzeSteps(walker, remote)
	if s.Remote.Missed < 60 || math.Abs(s.Remote.Mean-10) > 0.01 || s.Remote.CV > 0.01 {
		t.Errorf("missed frames: %s (missed %d)", s, s.Remote.Missed)
	}
}

func TestAnalyzeStepsDegenerate(t *testing.T) {
	if s := analyzeSteps(nil, nil); s.Note == "" {
		t.Error("no samples: no note")
	}
	one := []stepSample{{Frame: 1}}
	if s := analyzeSteps(one, one); s.Note == "" {
		t.Error("one sample: no note")
	}
	standing := walkTrace(60, func(int) float32 { return 0 })
	if s := analyzeSteps(standing, standing); s.Note == "" || s.String() == "" {
		t.Errorf("a walker standing still: %+v", s)
	}
	walker := walkTrace(120, func(f int) float32 { return 16.5 * float32(f) })
	if s := analyzeSteps(walker, standing); s.Note == "" || s.Remote.Steps != 0 {
		t.Errorf("a remote standing still: %+v", s)
	}
	// A counter that went back (a save state loaded) is not a step.
	back := []stepSample{{Frame: 10, P: vec{0, 0, 0}}, {Frame: 5, P: vec{999, 0, 0}}, {Frame: 6, P: vec{1000, 0, 0}}}
	if steps, missed := framedSteps(back); len(steps) != 1 || steps[0] != 1 || missed != 0 {
		t.Errorf("counter back: %v %d", steps, missed)
	}
}

// TestRecordSteps: one sample per frame the counter shows, none for a
// frame seen twice.
func TestRecordSteps(t *testing.T) {
	m := mem.NewFakeMem()
	set := func(f uint32, x float32) {
		b := make([]byte, 4)
		mem.PutF32BE(b, x) // the position first, then the frame that shows it
		_ = m.Store(0x80700000, b)
		mem.PutU32BE(b, f)
		_ = m.Store(mailbox.SpawnTrigger, b)
	}
	set(1, 0)
	read := func(r mem.Reader) (vec, bool) {
		x, err := mem.ReadF32(r, 0x80700000)
		return vec{x, 0, 0}, err == nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []stepSample)
	go func() { done <- recordSteps(ctx, m, read, time.Now(), 100*time.Microsecond) }()
	for f := uint32(2); f <= 5; f++ {
		time.Sleep(50 * time.Millisecond)
		set(f, float32(f*10))
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	ss := <-done
	// Each frame once at most, in order; a loaded machine may miss one
	// (the analysis spreads a gap), but the first and last are there.
	if len(ss) < 2 || ss[0].Frame != 1 || ss[len(ss)-1].Frame != 5 {
		t.Fatalf("samples %+v, want frames 1..5", ss)
	}
	for i, s := range ss {
		if i > 0 && s.Frame <= ss[i-1].Frame {
			t.Errorf("frame %d seen twice or out of order: %+v", s.Frame, ss)
		}
		if s.Frame > 1 && s.P[0] != float32(s.Frame*10) {
			t.Errorf("frame %d at %v, want %d", s.Frame, s.P[0], s.Frame*10)
		}
	}
}
