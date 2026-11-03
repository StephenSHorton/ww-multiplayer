package feed

import (
	"math"
	"testing"
	"time"

	network "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// walkRun plays a sender walking +X at speed units a second, sending every
// period, through a link that holds everything sent in [stallFrom,
// stallFrom+stall) until the stall ends (a TCP retransmit stall), and
// returns the X the receiver shows at each 60 Hz tick from 0.5 s to end.
func walkRun(t *testing.T, stallFrom, stall, end time.Duration) []float64 {
	t.Helper()
	const speed = 500.0
	period := 16 * time.Millisecond
	oneWay := 40 * time.Millisecond
	t0 := time.Unix(1000, 0)
	var rp network.RemotePlayer
	var p playout
	var shown []float64
	sent := time.Duration(0)
	var seq uint64
	for tick := time.Duration(0); tick <= end; tick += time.Second / 60 {
		// Deliver what has arrived by this tick.
		for {
			arrive := sent + oneWay
			if sent >= stallFrom && sent < stallFrom+stall {
				arrive = stallFrom + stall + oneWay
			}
			if arrive > tick {
				break
			}
			seq++
			pos := network.PlayerPosition{PosX: float32(speed * sent.Seconds())}
			keep := rp.Recent
			if len(keep) >= network.RecentCap {
				keep = keep[1:]
			}
			rp.Recent = append(append([]network.Sample(nil), keep...), network.Sample{Seq: seq, At: t0.Add(arrive), Pos: pos})
			rp.Position = &pos
			sent += period
		}
		if len(rp.Recent) == 0 {
			continue
		}
		v := p.view(&rp, t0.Add(tick))
		if tick >= 500*time.Millisecond {
			shown = append(shown, float64(v.Position.PosX))
		}
	}
	return shown
}

func stepsOf(xs []float64) (steps []float64, median float64) {
	for i := 1; i < len(xs); i++ {
		steps = append(steps, xs[i]-xs[i-1])
	}
	s := append([]float64(nil), steps...)
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return steps, s[len(s)/2]
}

// TestPlayoutSmoothThroughStall: a 250 ms stall (a lost segment at 150 ms
// RTT recovered by retransmit) makes no frame stop or jump: every step
// stays within smooth-steps' bounds (a tenth to twice the median).
func TestPlayoutSmoothThroughStall(t *testing.T) {
	for _, stall := range []time.Duration{0, 150 * time.Millisecond, 250 * time.Millisecond} {
		xs := walkRun(t, 2*time.Second, stall, 5*time.Second)
		steps, med := stepsOf(xs)
		want := 500.0 / 60
		if math.Abs(med-want) > want*0.1 {
			t.Errorf("stall %v: median step %.2f, want about %.2f", stall, med, want)
		}
		for i, s := range steps {
			if s < med/10 || s > 2*med {
				t.Errorf("stall %v: step %d is %.2f (median %.2f): a stall or a jump", stall, i, s, med)
			}
		}
	}
}

// TestPlayoutLongStallHoldsThenCatchesUp: past extrapolateMax the copy
// holds, and afterwards it catches up without a jump (no step over twice
// the median) and ends playoutDelay behind.
func TestPlayoutLongStallHoldsThenCatchesUp(t *testing.T) {
	xs := walkRun(t, 2*time.Second, 600*time.Millisecond, 6*time.Second)
	steps, med := stepsOf(xs)
	for i, s := range steps {
		if s > 2*med {
			t.Errorf("step %d is %.2f (median %.2f): a jump", i, s, med)
		}
		if s < -0.01 {
			t.Errorf("step %d is %.2f: moved backwards", i, s)
		}
	}
	last := xs[len(xs)-1]
	newest := 500 * (6.0 - 0.04) // what the sender had sent, less the one-way delay
	behind := newest - last
	if want := 500 * playoutDelay.Seconds(); math.Abs(behind-want) > 15 {
		t.Errorf("ends %.1f units behind the newest sample, want about %.1f", behind, want)
	}
}

func TestLerpAngleShortWay(t *testing.T) {
	if got := lerpAngle(32000, -32000, 0.5); got != -32768 && got != 32767 {
		t.Errorf("lerpAngle across the wrap = %d, want about ±32768", got)
	}
	if got := lerpAngle(0, 1000, 0.25); got != 250 {
		t.Errorf("lerpAngle(0, 1000, 0.25) = %d", got)
	}
}

// TestPlayoutOffWithoutSamples: no Recent (an older client) shows the
// remote as received.
func TestPlayoutOffWithoutSamples(t *testing.T) {
	pos := network.PlayerPosition{PosX: 7}
	rp := network.RemotePlayer{Position: &pos}
	var p playout
	if v := p.view(&rp, time.Now()); v.Position != &pos {
		t.Errorf("view changed a remote with no samples")
	}
}
