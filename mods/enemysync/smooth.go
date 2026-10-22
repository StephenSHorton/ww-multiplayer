package enemysync

import (
	"math"
	"time"
)

// The client smoother, ww-online's native scheme (design "client_go_side"
// smoother): a ring of samples stamped with the host's monotonic ms and
// the local arrival time. The render time is now, less the quickest
// arrival lag seen (the host-to-client clock offset plus the fastest
// transit, so it needs no shared clock), less a delay of at least
// MinDelay or the arrival spread plus a frame. The output is the lerp
// between the two samples that bracket the render time; past the newest
// sample it extrapolates along the last two for up to MaxExtrapolate,
// and holds once no sample has arrived for MaxGap. History is dropped,
// and the next output flagged snap, on a jump over SnapDistance, a host
// snap, a ragdoll edge and an epoch change. Angles lerp as s16 on the
// shortest arc; the morf frame advances by the rate (frames per game
// frame at FrameMS) from the sample before the render time, whose
// action, mode and attack fields the output takes.
//
// The render time never steps back (renderHW): the quickest lag rising
// when the fastest sample rolls out of the ring, or a late sample growing
// the delay, would otherwise move it behind the sample a bck or AnmSeq
// change came from and replay the older anim's fields, and the REL would
// setAnm the old bck and then the new one again (a restart firing twice,
// anmseq.go). Holding the render time keeps Bck, AnmAttr, AnmSeq, Morf,
// Flags2, the blur, the frame and the rate from one sample, so the REL
// never gets one sample's bck with another's frame. Rebase drops the mark
// with the unwrap origin when the host's timeline restarts.
const (
	MinDelay       = 100 * time.Millisecond
	FrameMS        = 1000.0 / 30.0 // the game runs at 30 Hz
	MaxExtrapolate = 150 * time.Millisecond
	MaxGap         = 250 * time.Millisecond
	SnapDistance   = 300.0
	ringSize       = 32
	tickMS         = 16 // one host tick, the "+ frame" of the delay rule
)

type sample struct {
	hostMS    int64 // the host's ms, unwrapped relative to the smoother's first sample
	arrivalMS int64 // local ms since the smoother was made
	slot      Slot
}

// Smoother is one enemy's sample ring and output (the client keys them by
// match key). Its ragdoll edge is SlotRagdoll, which the Moblin and the
// Bokoblin set (dr mMode <= -100) and the ChuChu never does.
type Smoother struct {
	raw     bool
	start   time.Time
	base    uint32 // the first sample's host_ms, the unwrap origin
	haveAny bool
	ring    []sample
	epoch   uint32
	snap    bool // the next output carries SlotSnap
	hadRag  bool
	wasRag  bool
	// renderHW is the latest render time output (host ms, as hostMS);
	// haveHW is false until the first. Reset keeps it (the same host
	// timeline), Rebase drops it.
	renderHW int64
	haveHW   bool
}

// NewSmoother returns a smoother starting its local clock at start. raw
// bypasses it: At returns the newest sample as is (WW_ENEMY_RAW=1).
func NewSmoother(start time.Time, raw bool) *Smoother {
	return &Smoother{raw: raw, start: start}
}

// Reset drops the history; the next output snaps.
func (s *Smoother) Reset() {
	s.ring = s.ring[:0]
	s.snap = true
	s.hadRag = false
}

// Rebase drops the history, the host_ms unwrap origin and the render
// time high-water mark: the host's timeline restarted (a new host epoch,
// its host_ms back near 0), so every new sample would otherwise look
// older than the newest held and be dropped. The next output snaps.
func (s *Smoother) Rebase() {
	s.Reset()
	s.haveAny, s.base = false, 0
	s.haveHW, s.renderHW = false, 0
}

// Len is the number of samples held.
func (s *Smoother) Len() int { return len(s.ring) }

func (s *Smoother) localMS(t time.Time) int64 { return t.Sub(s.start).Milliseconds() }

func (s *Smoother) hostRel(hostMS uint32) int64 {
	if !s.haveAny {
		s.base, s.haveAny = hostMS, true
	}
	return int64(int32(hostMS - s.base))
}

// Push adds a sample stamped with the host's host_ms and its local
// arrival, under epoch. Out-of-order samples (an older host_ms than the
// newest held) are dropped.
func (s *Smoother) Push(hostMS uint32, arrival time.Time, sl Slot, epoch uint32) {
	h := s.hostRel(hostMS)
	if epoch != s.epoch {
		s.epoch = epoch
		s.Reset()
	}
	rag := sl.Flags&SlotRagdoll != 0
	if n := len(s.ring); n > 0 {
		last := &s.ring[n-1]
		if h < last.hostMS {
			return
		}
		if dist(last.slot.Pos, sl.Pos) > SnapDistance || sl.Flags&SlotSnap != 0 || (s.hadRag && rag != s.wasRag) {
			s.Reset()
		}
	} else if sl.Flags&SlotSnap != 0 {
		s.snap = true
	}
	s.hadRag, s.wasRag = true, rag
	if len(s.ring) == ringSize {
		copy(s.ring, s.ring[1:])
		s.ring = s.ring[:ringSize-1]
	}
	s.ring = append(s.ring, sample{hostMS: h, arrivalMS: s.localMS(arrival), slot: sl})
}

func dist(a, b [3]float32) float64 {
	dx, dy, dz := float64(a[0]-b[0]), float64(a[1]-b[1]), float64(a[2]-b[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// delay returns the quickest arrival lag and the render delay over the
// ring, in ms.
func (s *Smoother) delay() (quickest, delay int64) {
	quickest, slowest := int64(math.MaxInt64), int64(math.MinInt64)
	for i := range s.ring {
		lag := s.ring[i].arrivalMS - s.ring[i].hostMS
		quickest = min(quickest, lag)
		slowest = max(slowest, lag)
	}
	delay = max(MinDelay.Milliseconds(), slowest-quickest+tickMS)
	return quickest, delay
}

// At returns the slot to show at now. ok is false while no sample is
// held. The output's SlotSnap is set once after a reset; its SlotDying
// is set when the newest sample's is.
func (s *Smoother) At(now time.Time) (out Slot, ok bool) {
	n := len(s.ring)
	if n == 0 {
		return out, false
	}
	newest := &s.ring[n-1]
	defer func() {
		if s.snap {
			out.Flags |= SlotSnap
			s.snap = false
		} else {
			out.Flags &^= SlotSnap
		}
		if newest.slot.Flags&SlotDying != 0 {
			out.Flags |= SlotDying
		}
		out.Health = newest.slot.Health
	}()
	if s.raw {
		return newest.slot, true
	}
	nowMS := s.localMS(now)
	quickest, delay := s.delay()
	renderT := nowMS - quickest - delay
	if s.haveHW && renderT < s.renderHW {
		renderT = s.renderHW // never backwards (the header)
	}
	s.renderHW, s.haveHW = renderT, true
	if renderT <= s.ring[0].hostMS {
		return s.ring[0].slot, true
	}
	if renderT >= newest.hostMS {
		out = newest.slot
		if nowMS-newest.arrivalMS > MaxGap.Milliseconds() || n < 2 {
			return out, true // hold
		}
		prev := &s.ring[n-2]
		dt := min(renderT-newest.hostMS, MaxExtrapolate.Milliseconds())
		span := newest.hostMS - prev.hostMS
		if span > 0 && dt > 0 {
			k := float32(dt) / float32(span)
			for i := range 3 {
				out.Pos[i] = newest.slot.Pos[i] + (newest.slot.Pos[i]-prev.slot.Pos[i])*k
			}
			out.MorfFrame = advanceFrame(&newest.slot, dt)
		}
		return out, true
	}
	// Bracket: a.hostMS <= renderT < b.hostMS. The frame is a's advanced
	// by a's rate, never a blend toward b's: across a bck, attribute or
	// AnmSeq change b's frame belongs to another animation, and inside one
	// a loop wrap between a and b would lerp backwards through it. b's
	// frame takes over at b's render time with b's discrete fields.
	i := n - 2
	for i > 0 && s.ring[i].hostMS > renderT {
		i--
	}
	a, b := &s.ring[i], &s.ring[i+1]
	out = a.slot
	span := b.hostMS - a.hostMS
	if span <= 0 {
		return out, true
	}
	k := float32(renderT-a.hostMS) / float32(span)
	for j := range 3 {
		out.Pos[j] = a.slot.Pos[j] + (b.slot.Pos[j]-a.slot.Pos[j])*k
	}
	out.AngleY = lerpS16(a.slot.AngleY, b.slot.AngleY, k)
	if a.slot.Kind != KindCC {
		// A ChuChu's tilt is its m316.x ground tilt, which the REL writes
		// raw (no smoothing; CONTRACT slot table): a's, as the discrete
		// fields.
		out.Tilt = lerpS16(a.slot.Tilt, b.slot.Tilt, k)
	}
	out.MorfFrame = advanceFrame(&a.slot, renderT-a.hostMS)
	return out, true
}

// lerpS16 interpolates two s16 angles along the shortest arc (the
// difference wraps as s16 does).
func lerpS16(a, b int16, k float32) int16 {
	d := int16(b - a) // wraps: the shortest arc
	return int16(int32(a) + int32(float32(d)*k))
}

// advanceFrame is sl's morf frame advanced by its rate over ms of host
// time; held while the host skips play() (playSkip), which freezes the
// frame on the host too.
func advanceFrame(sl *Slot, ms int64) float32 {
	if playSkip(sl) {
		return sl.MorfFrame
	}
	return sl.MorfFrame + sl.MorfRate*float32(float64(ms)/FrameMS)
}

// playSkip reports the kind's PLAY_SKIP flags2 bit: the Moblin's
// SlotF2PlaySkip (m05B2, d_a_mo2.cpp:3488-3490), the Bokoblin's BkF2PlaySkip
// (m030C, play gated at d_a_bk.cpp:4362-4367), the Armos Knight's
// AmF2PlaySkip (mCountDownTimers[1], d_a_am.cpp:1034-1036), the Darknut's
// TnF2PlaySkip (m03EE, d_a_tn.cpp:3531-3536). A ChuChu, a
// Keese (D15) and an Armos have none (the ChuChu's bit 1 is CcF2Co, the
// Armos's Am2F2WeakTg).
func playSkip(sl *Slot) bool {
	switch sl.Kind {
	case KindMO2:
		return sl.Flags2&SlotF2PlaySkip != 0
	case KindBK:
		return sl.Flags2&BkF2PlaySkip != 0
	case KindAM:
		return sl.Flags2&AmF2PlaySkip != 0
	case KindTN:
		return sl.Flags2&TnF2PlaySkip != 0
	}
	return false
}
