package feed

import (
	"os"
	"strconv"
	"time"

	network "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
)

// The playout buffer for remote poses (wind#83). A remote's position and
// pose come over TCP, which shows a lost packet only as a stall and then a
// burst. Easing toward the newest sample, the copy stopped through the
// stall and jumped at the burst (wind#82: 11 to 32 stalls per 5 s walk at
// 1% loss). Instead each remote is shown playoutDelay behind its newest
// sample, at the sender's own pace: the sender sends one sample a frame,
// so a sample's number is its clock, and the arrival times are needed
// only to learn the frame period.
//
// The playhead runs a little faster while more than playoutDelay is
// buffered (after a burst) and a little slower while less is (as a stall
// starts), never fast enough to read as a jump. When the buffer runs out
// it extrapolates the last motion for up to extrapolateMax, then holds.
// The existing easing (lerpK, poseLerpK) still runs on top.
var (
	playoutDelay   = envMillis("WW_PLAYOUT_MS", 100*time.Millisecond)
	extrapolateMax = envMillis("WW_EXTRAPOLATE_MS", 200*time.Millisecond)
)

const (
	// catchUpTau: the rate is 1 + (buffered - playoutDelay) / catchUpTau,
	// within [rateMin, rateMax]. rateMax stays under smooth-steps' jump
	// bound (twice the walker's median step).
	catchUpTau = 0.4 // seconds
	rateMin    = 0.5
	rateMax    = 1.5
	// The sender's frame period: its loop is 16 ms plus its reads.
	periodDefault = 1.0 / 60
	periodMin     = 0.008
	periodMax     = 0.05
	// periodSpan is how many sample numbers an estimate of the period
	// needs between its two samples, so one late arrival moves it little.
	periodSpan = 16
)

// envMillis reads key as whole milliseconds; unset or unparseable gives
// def, and 0 is allowed (WW_PLAYOUT_MS=0 turns the playout buffer off).
func envMillis(key string, def time.Duration) time.Duration {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 0 {
		return def
	}
	return time.Duration(v) * time.Millisecond
}

// playout is one remote's playhead. The zero value starts on the next
// sample it is shown.
type playout struct {
	on     bool
	head   float64 // in sample numbers (network.Sample.Seq)
	last   time.Time
	period float64 // seconds a sample, estimated
	// pose is the playout's own interpolated pose buffer, reused.
	pose []byte
}

// view returns rp as it should be drawn at now: its position, pose and
// face played out from rp.Recent. Without samples (an old client, a test)
// or with the buffer off, it is rp as received.
func (p *playout) view(rp *network.RemotePlayer, now time.Time) network.RemotePlayer {
	v := *rp
	h := rp.Recent
	if len(h) == 0 || playoutDelay <= 0 {
		return v
	}
	newest := h[len(h)-1]
	p.estimatePeriod(h)
	delay := playoutDelay.Seconds() / p.period
	ahead := extrapolateMax.Seconds() / p.period
	top := float64(newest.Seq) + ahead

	switch {
	case !p.on || p.head > top+ahead || p.head < float64(h[0].Seq)-float64(network.RecentCap):
		// First sight, or the samples restarted (a new session): start
		// playoutDelay behind the newest.
		p.on = true
		p.head = max(float64(newest.Seq)-delay, float64(h[0].Seq))
	default:
		dt := now.Sub(p.last).Seconds()
		dt = min(max(dt, 0), 0.1)
		buffered := (float64(newest.Seq) - p.head) * p.period
		rate := 1 + (buffered-playoutDelay.Seconds())/catchUpTau
		rate = min(max(rate, rateMin), rateMax)
		p.head += dt / p.period * rate
		// Never more than the ring behind: a stall longer than it skips
		// ahead (the ring holds about 0.8 s).
		p.head = min(max(p.head, float64(h[0].Seq)), top)
	}
	p.last = now

	pos, a, b, frac := p.at(h, newest)
	v.Position = &pos
	// The pose: between the two samples around the playhead, or the
	// newest past the end. A sample whose pose never came lends the
	// nearest one that did.
	pa, pb := poseAtOrBefore(h, a), poseAtOrBefore(h, b)
	if pb < 0 {
		return v
	}
	sb := &h[pb]
	v.PoseJoints, v.FaceState = sb.PoseJoints, sb.FaceState
	if pa < 0 || pa == pb || frac <= 0 || len(h[pa].PoseMatrices) != len(sb.PoseMatrices) {
		v.PoseMatrices = sb.PoseMatrices
		return v
	}
	if len(p.pose) != len(sb.PoseMatrices) {
		p.pose = make([]byte, len(sb.PoseMatrices))
	}
	copy(p.pose, h[pa].PoseMatrices)
	avatar.Lerp(p.pose, sb.PoseMatrices, float32(frac))
	v.PoseMatrices = p.pose
	if frac < 0.5 {
		v.FaceState = h[pa].FaceState
	}
	return v
}

// at is the position at the playhead: interpolated between the samples
// h[a] and h[b] (frac of the way), or extrapolated past the newest along
// its last motion (a = b = the newest).
func (p *playout) at(h []network.Sample, newest network.Sample) (pos network.PlayerPosition, a, b int, frac float64) {
	last := len(h) - 1
	if p.head >= float64(newest.Seq) {
		pos = newest.Pos
		over := p.head - float64(newest.Seq)
		if over > 0 && last > 0 {
			k := max(last-4, 0)
			n := float64(newest.Seq - h[k].Seq)
			if n > 0 {
				s := float32(over / n)
				pos.PosX += (newest.Pos.PosX - h[k].Pos.PosX) * s
				pos.PosY += (newest.Pos.PosY - h[k].Pos.PosY) * s
				pos.PosZ += (newest.Pos.PosZ - h[k].Pos.PosZ) * s
			}
		}
		return pos, last, last, 0
	}
	b = last
	for b > 0 && float64(h[b-1].Seq) > p.head {
		b--
	}
	if b == 0 {
		return h[0].Pos, 0, 0, 0
	}
	a = b - 1
	sa, sb := h[a], h[b]
	frac = (p.head - float64(sa.Seq)) / float64(sb.Seq-sa.Seq)
	f := float32(frac)
	pos = network.PlayerPosition{
		PosX: sa.Pos.PosX + (sb.Pos.PosX-sa.Pos.PosX)*f,
		PosY: sa.Pos.PosY + (sb.Pos.PosY-sa.Pos.PosY)*f,
		PosZ: sa.Pos.PosZ + (sb.Pos.PosZ-sa.Pos.PosZ)*f,
		RotX: lerpAngle(sa.Pos.RotX, sb.Pos.RotX, f),
		RotY: lerpAngle(sa.Pos.RotY, sb.Pos.RotY, f),
		RotZ: lerpAngle(sa.Pos.RotZ, sb.Pos.RotZ, f),
	}
	return pos, a, b, frac
}

// estimatePeriod updates p.period from the arrival times across h.
func (p *playout) estimatePeriod(h []network.Sample) {
	if p.period == 0 {
		p.period = periodDefault
	}
	first, last := h[0], h[len(h)-1]
	n := last.Seq - first.Seq
	if n < periodSpan {
		return
	}
	got := last.At.Sub(first.At).Seconds() / float64(n)
	got = min(max(got, periodMin), periodMax)
	p.period += (got - p.period) * 0.05
}

// poseAtOrBefore is the index of the last sample at or before i that has
// a pose, or -1.
func poseAtOrBefore(h []network.Sample, i int) int {
	for ; i >= 0; i-- {
		if h[i].PoseMatrices != nil {
			return i
		}
	}
	return -1
}

// lerpAngle goes frac of the way from a to b, the short way round (the
// game's angles are int16, a full turn wrapping).
func lerpAngle(a, b int16, frac float32) int16 {
	d := b - a // wraps: the short way
	return a + int16(float32(d)*frac)
}
