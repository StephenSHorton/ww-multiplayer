package main

// smooth-steps: smoothing as a number (ww-online review, the low list:
// "a per-frame step log under injected jitter"). After datstarkey/
// ww-online's COS_MP_TEST_STEPS=1, which logs how far the local Link and
// slot 0's puppet moved each frame, run under COS_MP_TEST_RATE's late
// sends (seaborne/native/src/mods/ww_online/README.md, "Smooth peers":
// their puppet's step 16.46 +- 0.07 a frame against 5-29 for the old
// easing). Idea only, no code taken.
//
// One game's Link walks straight (wwdev input, the pad shim) while both
// games are sampled every frame (each game's spawn_trigger is the frame
// clock): the walker's own Link position, and in the other game the
// puppet slot puppet-sync keeps for the walker (the smoothed root the
// mini-Link's pose is re-applied at). Each trace becomes per-frame
// steps; over the stretch where the walker is at speed, the remote's
// steps should look like the walker's: the same mean, a small spread, no
// stalls (a frame that drew no motion) and no jumps (a frame that caught
// up two). WW_POSE_TEST_RATE on mp-local (feed.TestRate) adds the jitter.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// stepSample is a position seen at one game frame.
type stepSample struct {
	Frame uint32  `json:"f"`  // that game's spawn_trigger
	MS    float64 `json:"ms"` // since the recording started
	P     vec     `json:"p"`
}

// stepStats are one trace's per-frame steps over the moving window.
type stepStats struct {
	Samples int     `json:"samples"`
	Steps   int     `json:"steps"`  // steps in the window
	Missed  int     `json:"missed"` // frames the sampler did not see (their motion is spread over the gap)
	Mean    float64 `json:"mean"`
	SD      float64 `json:"sd"`
	CV      float64 `json:"cv"` // SD / Mean
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Median  float64 `json:"median"`
	Stalls  int     `json:"stalls"` // steps under a tenth of the walker's median: a frame that drew no motion
	Jumps   int     `json:"jumps"`  // steps over twice the walker's median: a frame that caught up
	Jerk    float64 `json:"jerk"`   // RMS of the change from one step to the next
}

// stepSummary is smooth-steps' verdict.
type stepSummary struct {
	Rate   string    `json:"rate,omitempty"` // WW_POSE_TEST_RATE the run's mp-local had, as given
	Walker stepStats `json:"walker"`
	Remote stepStats `json:"remote"`
	// Ratio is the remote's mean step over the walker's: 1 when it keeps
	// up at speed.
	Ratio float64 `json:"ratio"`
	// Behind is the remote's mean distance behind the walker, matched by
	// wall time: the latency the smoothing costs, in units.
	Behind float64 `json:"behind"`
	Note   string  `json:"note,omitempty"`
}

// stepTrim is how many steps at each end of the moving window are left
// out: Link speeding up and slowing down.
const stepTrim = 8

// framedSteps turns ss into per-frame steps (a gap of n frames spreads
// its motion over n) and counts the frames it missed.
func framedSteps(ss []stepSample) (steps []float64, missed int) {
	for i := 1; i < len(ss); i++ {
		n := ss[i].Frame - ss[i-1].Frame
		if n == 0 || n > 1<<31 {
			continue // the same frame again, or a counter that went back (a save state)
		}
		d := vdist(ss[i].P, ss[i-1].P) / float64(n)
		for range n {
			steps = append(steps, d)
		}
		missed += int(n) - 1
	}
	return steps, missed
}

// median of xs (0 for none); xs is not changed.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// movingWindow is steps from the first to the last over half of speed,
// less stepTrim at each end; nil when that leaves nothing.
func movingWindow(steps []float64, speed float64) []float64 {
	first, last := -1, -1
	for i, s := range steps {
		if s > speed/2 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last-first+1 <= 2*stepTrim {
		return nil
	}
	return steps[first+stepTrim : last+1-stepTrim]
}

// statsOver fills stepStats from the window w; speed is the walker's
// median step, which the stall and jump bounds are fractions of.
func statsOver(w []float64, speed float64) stepStats {
	st := stepStats{Steps: len(w)}
	if len(w) == 0 {
		return st
	}
	st.Min, st.Max = math.Inf(1), math.Inf(-1)
	sum := 0.0
	for _, s := range w {
		sum += s
		st.Min, st.Max = math.Min(st.Min, s), math.Max(st.Max, s)
		if s < speed/10 {
			st.Stalls++
		}
		if s > 2*speed {
			st.Jumps++
		}
	}
	st.Mean = sum / float64(len(w))
	v, j := 0.0, 0.0
	for i, s := range w {
		v += (s - st.Mean) * (s - st.Mean)
		if i > 0 {
			j += (s - w[i-1]) * (s - w[i-1])
		}
	}
	st.SD = math.Sqrt(v / float64(len(w)))
	if len(w) > 1 {
		st.Jerk = math.Sqrt(j / float64(len(w)-1))
	}
	if st.Mean > 0 {
		st.CV = st.SD / st.Mean
	}
	st.Median = median(w)
	return st
}

// analyzeSteps compares the remote's per-frame steps with the walker's.
func analyzeSteps(walker, remote []stepSample) stepSummary {
	var sum stepSummary
	ws, wMissed := framedSteps(walker)
	rs, rMissed := framedSteps(remote)
	var moving []float64
	for _, s := range ws {
		if s > 0.01 {
			moving = append(moving, s)
		}
	}
	speed := median(moving)
	if speed <= 0 {
		sum.Walker = stepStats{Samples: len(walker), Missed: wMissed}
		sum.Remote = stepStats{Samples: len(remote), Missed: rMissed}
		sum.Note = "the walker never moved: nothing to measure"
		return sum
	}
	sum.Walker = statsOver(movingWindow(ws, speed), speed)
	sum.Remote = statsOver(movingWindow(rs, speed), speed)
	sum.Walker.Samples, sum.Walker.Missed = len(walker), wMissed
	sum.Remote.Samples, sum.Remote.Missed = len(remote), rMissed
	if sum.Remote.Steps == 0 {
		sum.Note = "the remote never moved at speed: is puppet-sync feeding this slot?"
		return sum
	}
	if sum.Walker.Mean > 0 {
		sum.Ratio = round4(sum.Remote.Mean / sum.Walker.Mean)
	}
	sum.Behind = round4(behind(walker, remote))
	roundStats(&sum.Walker)
	roundStats(&sum.Remote)
	return sum
}

// behind is the remote's mean distance from the walker at the same wall
// time (the walker's sample nearest each remote sample's), over the
// remote samples while the walker moved.
func behind(walker, remote []stepSample) float64 {
	if len(walker) < 2 {
		return 0
	}
	var total float64
	n := 0
	j := 0
	for _, r := range remote {
		for j+1 < len(walker) && math.Abs(walker[j+1].MS-r.MS) <= math.Abs(walker[j].MS-r.MS) {
			j++
		}
		if j == 0 || walker[j].P == walker[j-1].P {
			continue // the walker is standing: no lag to see
		}
		total += vdist(r.P, walker[j].P)
		n++
	}
	if n == 0 {
		return 0
	}
	return total / float64(n)
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

func roundStats(s *stepStats) {
	for _, f := range []*float64{&s.Mean, &s.SD, &s.CV, &s.Min, &s.Max, &s.Median, &s.Jerk} {
		*f = round4(*f)
	}
}

// String is the summary's one-line form.
func (s stepSummary) String() string {
	if s.Note != "" && s.Remote.Steps == 0 {
		return s.Note
	}
	return fmt.Sprintf("walker %.2f +- %.2f a frame; remote %.2f +- %.2f (cv %.3f, ratio %.3f, %d stalls, %d jumps, jerk %.2f, %.0f units behind; %d+%d frames missed)",
		s.Walker.Mean, s.Walker.SD, s.Remote.Mean, s.Remote.SD, s.Remote.CV, s.Ratio,
		s.Remote.Stalls, s.Remote.Jumps, s.Remote.Jerk, s.Behind, s.Walker.Missed, s.Remote.Missed)
}

// recordSteps samples m every poll until ctx ends: at each new frame
// (spawn_trigger moved) it reads read's position and keeps it, stamped
// from t0. A frame whose read fails is skipped.
func recordSteps(ctx context.Context, m mem.Reader, read func(mem.Reader) (vec, bool), t0 time.Time, poll time.Duration) []stepSample {
	var out []stepSample
	var last uint32
	have := false
	for ctx.Err() == nil {
		f, err := mem.ReadU32(m, mailbox.SpawnTrigger)
		if err == nil && (!have || f != last) {
			if p, ok := read(m); ok {
				out = append(out, stepSample{Frame: f, MS: float64(time.Since(t0).Microseconds()) / 1000, P: p})
				last, have = f, true
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
	return out
}

// stepRun is one smooth-steps recording: the summary and both traces.
type stepRun struct {
	Walker  string          `json:"walker"` // A or B
	Secs    float64         `json:"secs"`
	Summary stepSummary     `json:"summary"`
	Traces  [2][]stepSample `json:"traces"` // the walker's Link, the other game's slot for it
}

// measureSmoothing walks walker's Link forward (stick up) for d while
// recording it and observer's slot for it. The slot is the one nearest
// the walker's Link at the start (remoteSlotPos).
func measureSmoothing(ctx context.Context, walker, observer mem.Mem, d time.Duration) (stepRun, error) {
	var run stepRun
	start, err := linkPos(walker)
	if err != nil {
		return run, fmt.Errorf("the walking Link: %w", err)
	}
	slot, _, ok := remoteSlotPos(observer, start)
	if !ok {
		return run, errors.New("the other game has no puppet slot position: is mp-local running?")
	}
	readLink := func(m mem.Reader) (vec, bool) {
		p, err := linkPos(m)
		return p, err == nil
	}
	readSlot := func(m mem.Reader) (vec, bool) {
		b, err := m.Read(feed.SlotAddr(slot, mailbox.PuppetOffPosX), 12)
		if err != nil || len(b) != 12 {
			return vec{}, false
		}
		v := vec{mem.F32BE(b), mem.F32BE(b[4:]), mem.F32BE(b[8:])}
		return v, finite(v)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t0 := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); run.Traces[0] = recordSteps(rctx, walker, readLink, t0, time.Millisecond) }()
	go func() { defer wg.Done(); run.Traces[1] = recordSteps(rctx, observer, readSlot, t0, time.Millisecond) }()
	// Half a second standing first, so the window opens on a standstill,
	// and a second after, so the remote's tail is in.
	sleepCtx(ctx, 500*time.Millisecond)
	if err := player.Override(walker, player.Pad{StickY: 100}); err != nil {
		cancel()
		wg.Wait()
		return run, fmt.Errorf("input: %w", err)
	}
	sleepCtx(ctx, d)
	_ = player.Release(walker)
	sleepCtx(ctx, time.Second)
	cancel()
	wg.Wait()
	run.Secs = d.Seconds()
	run.Summary = analyzeSteps(run.Traces[0], run.Traces[1])
	return run, nil
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// runSmoothSteps is `smooth-steps [secs=5] [A|B]`: mp-local must be
// running on the two Dolphins (with WW_POSE_TEST_RATE for jitter). The
// walker (default A) walks forward for secs; the run (summary and both
// traces) goes to logs/latest/smooth-steps.json, or smooth-steps.json in
// the working directory with WW_DEV_LOGS=off.
func runSmoothSteps(_ string, args []string) {
	const usage = "smooth-steps [secs=5] [A|B]"
	secs, walker := 5.0, "A"
	for _, a := range args {
		switch a {
		case "A", "B", "a", "b":
			walker = string(a[0] &^ 0x20)
		default:
			v, err := strconv.ParseFloat(a, 64)
			if err != nil || v < 1 || v > 60 {
				exitUsage(fmt.Errorf("secs must be 1 to 60, not %q", a), usage)
			}
			secs = v
		}
	}
	pidA, pidB, err := smokePIDs(os.Getenv)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	dA, err := emu.FindByPID(pidA, "GZLE01")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	defer dA.Close()
	dB, err := emu.FindByPID(pidB, "GZLE01")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	defer dB.Close()
	w, o := mem.Mem(dA), mem.Mem(dB)
	if walker == "B" {
		w, o = o, w
	}
	fmt.Printf("Walking %s's Link for %.0f s and sampling both games every frame (WW_POSE_TEST_RATE on mp-local: %q)...\n", walker, secs, os.Getenv(envPoseTestRate))
	run, err := measureSmoothing(context.Background(), w, o, time.Duration(secs*float64(time.Second)))
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	run.Walker = walker
	run.Summary.Rate = os.Getenv(envPoseTestRate)
	fmt.Println(run.Summary)
	dir := "."
	if root := devLogsRoot(os.Getenv); root != "" {
		dir = filepath.Join(root, "latest")
	}
	path := filepath.Join(dir, "smooth-steps.json")
	if err := writeJSON(path, run); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("Step log:", path)
}

// writeJSON writes v indented to path, making its directory.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
