package enemysync

import (
	"math"
	"testing"
	"time"
)

// feed pushes samples of a point moving along x at 1 unit per host ms,
// every period, with a transit of lag (plus jitter(i)) into s, starting
// at host ms 0 / local t0, for n samples. It returns the local time of
// the last arrival.
func feed(s *Smoother, t0 time.Time, n int, period, lag time.Duration, jitter func(i int) time.Duration) time.Time {
	var last time.Time
	for i := range n {
		hostMS := uint32(time.Duration(i) * period / time.Millisecond)
		arrival := t0.Add(time.Duration(i)*period + lag + jitter(i))
		s.Push(hostMS, arrival, Slot{Pos: [3]float32{float32(hostMS), 0, 0}, AngleY: int16(hostMS), MorfRate: 1, MorfFrame: float32(float64(hostMS) / FrameMS)}, 1)
		last = arrival
	}
	return last
}

func noJitter(int) time.Duration { return 0 }

// renderX is where the moving point should be shown at now: now less
// the lag and the delay, in host ms.
func renderX(t0, now time.Time, lag, delay time.Duration) float32 {
	return float32((now.Sub(t0) - lag - delay).Milliseconds())
}

// TestSmoother60Hz: with samples every 16 ms at a fixed lag, the output
// lags by MinDelay (the spread is tiny) and tracks the motion, between
// brackets and extrapolating past the newest sample.
func TestSmoother60Hz(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	lag := 40 * time.Millisecond
	last := feed(s, t0, 30, 16*time.Millisecond, lag, noJitter)
	// The frame advances by the rate (1 per game frame). (Each At call is
	// later than the one before: the render time never steps back.)
	out, _ := s.At(last)
	if wantF := float32(renderX(t0, last, lag, MinDelay)) / float32(FrameMS); math.Abs(float64(out.MorfFrame-wantF)) > 1.5 {
		t.Errorf("frame %.2f, want ~%.2f", out.MorfFrame, wantF)
	}
	for _, dt := range []time.Duration{0, 5 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond} {
		now := last.Add(dt)
		out, ok := s.At(now)
		want := renderX(t0, now, lag, MinDelay)
		if !ok || math.Abs(float64(out.Pos[0]-want)) > 2 {
			t.Errorf("at +%v: x %.1f, want %.1f (ok %v)", dt, out.Pos[0], want, ok)
		}
		if a := int16(want); out.AngleY < a-2 || out.AngleY > a+2 {
			t.Errorf("at +%v: angle %d, want ~%d", dt, out.AngleY, a)
		}
	}
	// Extrapolation stops at MaxExtrapolate past the newest sample, and
	// holds once no sample arrived for MaxGap.
	newestHost := float32(29 * 16)
	out, _ = s.At(last.Add(240 * time.Millisecond))
	if max := newestHost + float32(MaxExtrapolate.Milliseconds()); out.Pos[0] > max+0.5 || out.Pos[0] < newestHost {
		t.Errorf("extrapolated to %.1f, want at most %.1f", out.Pos[0], max)
	}
	out, _ = s.At(last.Add(400 * time.Millisecond))
	if out.Pos[0] != newestHost {
		t.Errorf("after a gap: x %.1f, want the newest sample %.1f (held)", out.Pos[0], newestHost)
	}
}

// TestSmoother20Hz: 50 ms apart, the output still tracks between brackets.
func TestSmoother20Hz(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	lag := 30 * time.Millisecond
	last := feed(s, t0, 20, 50*time.Millisecond, lag, noJitter)
	for _, dt := range []time.Duration{0, 10 * time.Millisecond, 25 * time.Millisecond, 49 * time.Millisecond} {
		now := last.Add(dt)
		out, ok := s.At(now)
		want := renderX(t0, now, lag, MinDelay)
		if !ok || math.Abs(float64(out.Pos[0]-want)) > 2 {
			t.Errorf("at +%v: x %.1f, want %.1f", dt, out.Pos[0], want)
		}
	}
}

// TestSmootherJitter: with arrival jitter the delay grows to the spread
// plus a tick, and the output never runs backwards.
func TestSmootherJitter(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	jit := func(i int) time.Duration { return time.Duration((i*37)%120) * time.Millisecond }
	last := feed(s, t0, 32, 16*time.Millisecond, 20*time.Millisecond, jit)
	lo, hi := time.Hour, time.Duration(0)
	for i := range 32 {
		lo, hi = min(lo, jit(i)), max(hi, jit(i))
	}
	spread := (hi - lo).Milliseconds()
	quickest, delay := s.delay()
	if quickest != 20+lo.Milliseconds() || delay != spread+tickMS {
		t.Errorf("quickest %d ms, delay %d ms, want 20+%d and the %d ms spread + a tick", quickest, delay, lo.Milliseconds(), spread)
	}
	prev := float32(-1)
	for dt := time.Duration(0); dt < 200*time.Millisecond; dt += 7 * time.Millisecond {
		out, ok := s.At(last.Add(dt))
		if !ok || out.Pos[0] < prev {
			t.Fatalf("at +%v: x %.1f went backwards from %.1f", dt, out.Pos[0], prev)
		}
		prev = out.Pos[0]
	}
}

// TestSmootherTeleport: a 400-unit jump drops the history and the next
// output snaps to the new place, once.
func TestSmootherTeleport(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	last := feed(s, t0, 10, 16*time.Millisecond, 20*time.Millisecond, noJitter)
	if out, _ := s.At(last); out.Flags&SlotSnap == 0 {
		t.Error("the first output after construction does not snap")
	}
	if out, _ := s.At(last); out.Flags&SlotSnap != 0 {
		t.Error("the second output snaps")
	}
	s.Push(10*16, last.Add(16*time.Millisecond), Slot{Pos: [3]float32{10*16 + 400, 0, 0}}, 1)
	if s.Len() != 1 {
		t.Errorf("history kept %d samples across the teleport", s.Len())
	}
	out, ok := s.At(last.Add(20 * time.Millisecond))
	if !ok || out.Flags&SlotSnap == 0 || out.Pos[0] != 10*16+400 {
		t.Errorf("after the teleport: %+v ok %v", out, ok)
	}
	if out, _ := s.At(last.Add(40 * time.Millisecond)); out.Flags&SlotSnap != 0 {
		t.Error("snap flag stuck")
	}
	// A host snap and an epoch change do the same.
	s.Push(11*16, last.Add(32*time.Millisecond), Slot{Pos: [3]float32{1, 0, 0}, Flags: SlotSnap}, 1)
	if out, _ := s.At(last.Add(48 * time.Millisecond)); out.Flags&SlotSnap == 0 || s.Len() != 1 {
		t.Errorf("host snap: %+v len %d", out, s.Len())
	}
	s.Push(12*16, last.Add(48*time.Millisecond), Slot{Pos: [3]float32{2, 0, 0}}, 2)
	if out, _ := s.At(last.Add(64 * time.Millisecond)); out.Flags&SlotSnap == 0 || s.Len() != 1 {
		t.Errorf("epoch change: %+v len %d", out, s.Len())
	}
}

// TestSmootherS16Wrap: an angle crossing +-32768 lerps the short way.
func TestSmootherS16Wrap(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	// Two samples 100 ms apart in host time, delivered with a 0 lag; the
	// render time lands between them.
	s.Push(0, t0, Slot{AngleY: 32000}, 1)
	s.Push(100, t0.Add(100*time.Millisecond), Slot{AngleY: -32000}, 1)
	out, _ := s.At(t0.Add(100*time.Millisecond + MinDelay + tickMS*time.Millisecond - 50*time.Millisecond))
	// Halfway along the short arc (1536 units through the wrap) is near
	// +-32768, far from 0.
	if a := int(out.AngleY); a > -32300 && a < 32300 {
		t.Errorf("angle %d lerped the long way", a)
	}
	if lerpS16(32000, -32000, 0.5) != -32768 || lerpS16(-32000, 32000, 0.5) != -32768 || lerpS16(0, 100, 0.25) != 25 {
		t.Errorf("lerpS16: %d %d %d", lerpS16(32000, -32000, 0.5), lerpS16(-32000, 32000, 0.5), lerpS16(0, 100, 0.25))
	}
}

// TestSmootherRagdollEdge: the ragdoll flag turning on or off drops the
// history and snaps; dying rides the newest sample.
func TestSmootherRagdollEdge(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	last := feed(s, t0, 10, 16*time.Millisecond, 20*time.Millisecond, noJitter)
	s.At(last)
	s.Push(10*16, last.Add(16*time.Millisecond), Slot{Pos: [3]float32{10 * 16, 0, 0}, Flags: SlotRagdoll}, 1)
	if s.Len() != 1 {
		t.Errorf("ragdoll on kept %d samples", s.Len())
	}
	if out, _ := s.At(last.Add(20 * time.Millisecond)); out.Flags&SlotSnap == 0 {
		t.Error("no snap at the ragdoll edge")
	}
	s.Push(11*16, last.Add(32*time.Millisecond), Slot{Pos: [3]float32{11 * 16, 0, 0}, Flags: SlotRagdoll}, 1)
	if s.Len() != 2 {
		t.Errorf("a second ragdoll sample reset the ring (%d)", s.Len())
	}
	s.Push(12*16, last.Add(48*time.Millisecond), Slot{Pos: [3]float32{12 * 16, 0, 0}, Flags: SlotDying}, 1)
	out, _ := s.At(last.Add(50 * time.Millisecond))
	if s.Len() != 1 || out.Flags&SlotSnap == 0 || out.Flags&SlotDying == 0 {
		t.Errorf("ragdoll off: len %d flags %08b", s.Len(), out.Flags)
	}
}

// TestSmootherRaw: raw mode returns the newest sample as is.
func TestSmootherRaw(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, true)
	if _, ok := s.At(t0); ok {
		t.Error("ok with no samples")
	}
	last := feed(s, t0, 5, 16*time.Millisecond, 20*time.Millisecond, noJitter)
	out, ok := s.At(last.Add(time.Second))
	if !ok || out.Pos[0] != 4*16 || out.Flags&SlotSnap == 0 {
		t.Errorf("raw: %+v ok %v", out, ok)
	}
	// Out-of-order samples are dropped.
	s.Push(1, last, Slot{Pos: [3]float32{99, 0, 0}}, 1)
	if out, _ := s.At(last); out.Pos[0] != 4*16 || s.Len() != 5 {
		t.Errorf("an old sample was taken: %+v len %d", out, s.Len())
	}
}

// TestSmootherAnimSwitch: the discrete animation fields come from the
// bracket's earlier sample, so a new bck shows from the later sample's
// render time on, not before (design "Unit, client").
func TestSmootherAnimSwitch(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	a := Slot{Pos: [3]float32{0, 0, 0}, Bck: 0x73, AnmAttr: AnmAttrLoop, AnmSeq: 1, Morf: 15, Flags2: 0x01, BlurShape: 0, BlurScale: 0}
	b := Slot{Pos: [3]float32{10, 0, 0}, Bck: 0x45, AnmAttr: AnmAttrNone, AnmSeq: 2, Morf: 5, Flags2: 0x05, BlurShape: 2, BlurScale: 1.5}
	// 100 ms apart with a 0 lag: the delay is MinDelay, so b's render
	// time is its arrival plus MinDelay.
	s.Push(0, t0, a, 1)
	s.Push(100, t0.Add(100*time.Millisecond), b, 1)
	bAt := t0.Add(100*time.Millisecond + MinDelay)
	anim := func(sl Slot) [7]float32 {
		return [7]float32{float32(sl.Bck), float32(sl.AnmAttr), float32(sl.AnmSeq), float32(sl.Morf), float32(sl.Flags2), float32(sl.BlurShape), sl.BlurScale}
	}
	for _, dt := range []time.Duration{-50 * time.Millisecond, -time.Millisecond} {
		out, ok := s.At(bAt.Add(dt))
		if !ok || anim(out) != anim(a) {
			t.Errorf("at b%v: bck %02X anim %v, want a's %v", dt, out.Bck, anim(out), anim(a))
		}
		if dt == -50*time.Millisecond && (out.Pos[0] <= 0 || out.Pos[0] >= 10) {
			t.Errorf("between a and b: x %.1f not lerped", out.Pos[0])
		}
	}
	for _, dt := range []time.Duration{0, 50 * time.Millisecond} {
		out, ok := s.At(bAt.Add(dt))
		if !ok || anim(out) != anim(b) {
			t.Errorf("at b+%v: bck %02X anim %v, want b's %v", dt, out.Bck, anim(out), anim(b))
		}
	}
}

// TestSmootherFrameAcrossBck: across a bck change between the bracket's
// samples the frame is never blended toward b's (another animation's):
// up to b's render time it is a's frame advanced by a's rate, with a's
// rate; from b's render time on it is b's.
func TestSmootherFrameAcrossBck(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	a := Slot{Bck: 0x73, AnmAttr: AnmAttrLoop, AnmSeq: 1, MorfFrame: 10, MorfRate: 1}
	b := Slot{Bck: 0x45, AnmAttr: AnmAttrNone, AnmSeq: 2, MorfFrame: 0, MorfRate: 0.5}
	s.Push(0, t0, a, 1)
	s.Push(100, t0.Add(100*time.Millisecond), b, 1)
	bAt := t0.Add(100*time.Millisecond + MinDelay) // render time 100
	near := func(x, want float32) bool { return math.Abs(float64(x-want)) < 0.01 }
	for _, renderMS := range []int64{50, 99} {
		out, _ := s.At(bAt.Add(time.Duration(renderMS-100) * time.Millisecond))
		want := a.MorfFrame + a.MorfRate*float32(float64(renderMS)/FrameMS)
		if out.Bck != a.Bck || !near(out.MorfFrame, want) || out.MorfRate != a.MorfRate {
			t.Errorf("render %d: bck %02X frame %.3f rate %.2f, want a's %02X %.3f %.2f", renderMS, out.Bck, out.MorfFrame, out.MorfRate, a.Bck, want, a.MorfRate)
		}
	}
	if out, _ := s.At(bAt); out.Bck != b.Bck || out.MorfFrame != b.MorfFrame || out.MorfRate != b.MorfRate {
		t.Errorf("at b's render time: bck %02X frame %.3f rate %.2f, want b's", out.Bck, out.MorfFrame, out.MorfRate)
	}
}

// TestSmootherNoAnimRegress: the quickest arrival lag rising (the one
// fast sample rolling out of the ring) would move the render time back
// behind the sample a bck and AnmSeq change came from; the render time
// holds instead, so Bck and AnmSeq never go back to the older anim and
// the position never runs backwards.
func TestSmootherNoAnimRegress(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	const change = 27 // samples from here on carry bck 0x45, seq 2
	push := func(i int) time.Time {
		lag := 50 * time.Millisecond
		if i == 0 {
			lag = 0 // the one fast sample: quickest 0 while it is held
		}
		host := time.Duration(i) * 16 * time.Millisecond
		sl := Slot{Pos: [3]float32{float32(i * 16), 0, 0}, Bck: 0x73, AnmSeq: 1, MorfRate: 1}
		if i >= change {
			sl.Bck, sl.AnmSeq = 0x45, 2
		}
		at := t0.Add(host + lag)
		s.Push(uint32(host/time.Millisecond), at, sl, 1)
		return at
	}
	var now time.Time
	for i := range ringSize {
		now = push(i)
	}
	// Render time 546-0-100 = 446: past the change's 432.
	out, _ := s.At(now)
	if out.Bck != 0x45 || out.AnmSeq != 2 {
		t.Fatalf("before: bck %02X seq %d, want the new anim", out.Bck, out.AnmSeq)
	}
	prevX := out.Pos[0]
	// The next sample pushes the fast one out: quickest 50, so the raw
	// render time would be 562-50-100 = 412, before the change.
	for i := ringSize; i < ringSize+8; i++ {
		now = push(i)
		out, _ = s.At(now)
		if out.Bck != 0x45 || out.AnmSeq != 2 {
			t.Fatalf("sample %d: bck %02X seq %d went back to the old anim", i, out.Bck, out.AnmSeq)
		}
		if out.Pos[0] < prevX {
			t.Fatalf("sample %d: x %.1f went backwards from %.1f", i, out.Pos[0], prevX)
		}
		prevX = out.Pos[0]
	}
}

// TestSmootherRebase: a restarted host's host_ms (back near 0) is dropped
// as older than the newest held until Rebase; after it the smoother
// takes the new timeline, snaps once and lerps it.
func TestSmootherRebase(t *testing.T) {
	t0 := time.Unix(100, 0)
	s := NewSmoother(t0, false)
	for i := range 10 {
		s.Push(500000+uint32(i)*16, t0.Add(time.Duration(i)*16*time.Millisecond), Slot{Pos: [3]float32{1000, 0, 0}}, 1)
	}
	now := t0.Add(200 * time.Millisecond)
	s.At(now)
	s.Push(16, now, Slot{Pos: [3]float32{5, 0, 0}}, 1)
	if s.Len() != 10 {
		t.Fatalf("a restarted host's sample was taken without a rebase (len %d)", s.Len())
	}
	s.Rebase()
	for i := range 10 {
		s.Push(uint32(i)*16, now.Add(time.Duration(i)*16*time.Millisecond), Slot{Pos: [3]float32{float32(i * 16), 0, 0}}, 1)
	}
	if s.Len() != 10 {
		t.Fatalf("after the rebase: len %d", s.Len())
	}
	last := now.Add(9 * 16 * time.Millisecond)
	out, ok := s.At(last)
	want := float32(9*16) - float32(MinDelay.Milliseconds())
	if !ok || out.Flags&SlotSnap == 0 || math.Abs(float64(out.Pos[0]-want)) > 2 {
		t.Errorf("after the rebase: x %.1f flags %08b, want ~%.1f snapped (the old render time dropped)", out.Pos[0], out.Flags, want)
	}
}

// TestSmootherPlaySkip: while the host skips play() the frame is held,
// not advanced by the rate.
func TestSmootherPlaySkip(t *testing.T) {
	sl := Slot{MorfFrame: 10, MorfRate: 1}
	if got := advanceFrame(&sl, 100); got != 13 {
		t.Errorf("playing: frame %v, want 13", got)
	}
	sl.Flags2 = SlotF2PlaySkip
	if got := advanceFrame(&sl, 100); got != 10 {
		t.Errorf("play skipped: frame %v, want 10", got)
	}
}
