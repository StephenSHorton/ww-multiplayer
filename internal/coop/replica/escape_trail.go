package replica

import (
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
)

// The partner's trail (§6.7). A warp to the partner lands beside it, not
// on it: on its exact position the two Links overlap, and each game draws
// the partner's Link #2 inside its own Link. Pose sync carries only the
// partner's position and angle, with no word of the ground under it
// (network.PlayerPosition), so the replica keeps, each tick, the spots the
// partner's Link walked over in its stay in its stage: its own Link stood
// there, so its game had a floor there, and a same-stage warp goes only
// into a room this game has loaded (mpBgW).
//
// Only the partner's last walk counts: an unbroken run of samples, each
// one step of a walk on level ground from the last (walkStep), that ends
// where the partner stands now. A step no walk makes (a jump, a fall, a
// Deku Leaf glide, a hookshot pull, a rope swing, a jump of the position),
// a room change, and a gap in the samples across which the partner moved
// end the walk, and its spots are dropped. So a warp lands neither over a
// gap the partner crossed in one of those ways nor on the far side of it.
// It lands on the newest spot of the walk in the partner's room
// LandMin..LandMax from it across the ground and within LandMaxDY in
// height, else on the partner itself, as before.
//
// The walked-on test (walkStep, walkedOn) and every threshold here are
// inferred, to be calibrated on a live trail. Some spots off the ground
// still pass it, and a warp onto one can drop Link to the floor below, at
// worst a void-out: a spot on the path of a platform that carried the
// partner within walkDY a tick of level and at no more than walkSpeed a
// tick across the ground (a raft, a level lift), which has since moved
// away; a spot of a level hookshot pull short enough, a frame or two,
// that no step of it is faster than walkSpeed; and a spot on a ledge or
// wall the partner hung on or moved along, when it went on from there by
// steps of a walk. A spot on water it swam across, likewise, leaves Link
// swimming. On the sea the trail is not used (sameStageWarp).

const (
	// TrailLen bounds the trail: the newest TrailLen spots are kept.
	TrailLen = 64
	// TrailStep is how far apart across the ground the spots kept are. A
	// partner less than TrailStep from where it was stands still: the walk
	// goes on across a gap in the samples only then.
	TrailStep = 15
	// LandMin and LandMax bound how far across the ground from the
	// partner a warp lands: clear of Link's body cylinder, of radius 30
	// or 50 (d_a_player_main.cpp:9737-9741), and near enough to see it.
	LandMin = 100
	LandMax = 500
	// LandMaxDY bounds how far above or below the partner a warp lands.
	LandMaxDY = 100
)

const (
	// walkSpeed is the fastest a step of a walk goes across the ground,
	// in units a tick: about twice Link's run, of at most 17 units a frame
	// (mMaxNormalSpeed, d_a_player_main.cpp:3328; daPy_HIO_move_c0::m,
	// d_a_player_HIO_data.inc:9), 51 a tick at 30 frames a second, and
	// above his roll, of at most 26 a frame (procFrontRoll_init,
	// d_a_player_main.cpp:6813; daPy_HIO_roll_c0::m,
	// d_a_player_HIO_data.inc:98), 78 a tick. A hookshot pull moves him up
	// to 9 links of 7 units a frame (d_a_hookshot.cpp:320-333,
	// d_a_player_hook.inc:363), about 189 a tick, and a dev warp, or a
	// position still in another stage's frame, usually jumps further.
	walkSpeed = 100
	// walkDY is how far a step of a walk climbs or drops, in units a
	// tick: the trail keeps level ground. A Deku Leaf glide drops a steady
	// 6 a tick, at its top fall speed of 2 a frame (procFanGlide's
	// maxFallSpeed, d_a_player_fan.inc:410-447; daPy_HIO_fan_c0::m,
	// d_a_player_HIO_data.inc:257-260), and a jump or a fall drops more.
	// A ramp or a stair taken at more than that ends the walk too.
	walkDY = 3
	// walkAccel is how much the partner's vertical speed may change, in
	// units a tick, between the two steps around a spot kept: a jump's
	// changes every frame, and a fall's until it reaches its top speed.
	walkAccel = 4
	// walkSlope and walkStepY bound the climb or drop across the steps
	// around a spot kept: walkSlope units per unit across the ground,
	// plus walkStepY.
	walkSlope = 0.8
	walkStepY = 3
	// trailLag is how far the partner's PRESENCE room may lag or lead its
	// pose across a door, the two coming over different connections. A
	// room change ends the walk, with the spots of the ticks before it,
	// which can be in the new room, and no spot is kept for trailLag in
	// the new room, while the pose can still be in the last.
	trailLag = 3 * TickEvery
)

// stageTypeSea is the sea's STAG stage type, dStageType_SEA_e
// (d_stage.h:24): there the water's height comes from the sea's own map
// (d_a_sea.cpp:102), and a void-out restarts by the ship
// (d_a_player_main.cpp:10873-10880).
const stageTypeSea = 7

// partnerTrail is the spots of the partner's last walk in one stay in its
// stage: rest and since are the stage and point it is at rest in, and
// since when (partnerTrack).
type partnerTrail struct {
	rest      entryKey
	since     time.Time
	room      int8      // the room of the last sample
	roomSince time.Time // when the samples came into it; zero before the first
	raw       [3]trailPoint
	nraw      int          // samples in raw, oldest first, being judged; the last is the newest sample
	gap       bool         // a tick since the newest sample took none
	pts       []trailPoint // the walk's spots kept, oldest first, at most TrailLen
}

// trailPoint is the partner's position in room at a tick.
type trailPoint struct {
	p    Pos
	room int8
	at   time.Time
}

// noteTrail samples the partner's position for its trail (under r.mu, each
// tick): only while it is in game with no stage change pending, not paused
// and in no event, in a known room, and at rest in its stage for
// PartnerSettle, by when the position pose sync has is the one in this
// stage (item 82). A stage change, a reload of the stage and a lost Link
// start the trail over (notePartnerRest); any other missing sample is a
// gap in the samples, which ends the walk if the partner moved across it
// (add).
func (r *Replica) noteTrail() {
	t, k, p := &r.trail, &r.track, r.partner
	if !k.atRest {
		*t = partnerTrail{}
		return
	}
	if t.rest != k.restAt || !t.since.Equal(k.restSince) {
		*t = partnerTrail{rest: k.restAt, since: k.restSince}
	}
	now := r.now()
	switch {
	case p == nil || r.cfg.PartnerPos == nil,
		p.Flags&wire.PresenceInGame == 0,
		p.Flags&(wire.PresenceTransitioning|wire.PresencePaused|wire.PresenceInEvent) != 0,
		p.Room < 0,
		now.Sub(k.restSince) < PartnerSettle:
		t.gap = true
		return
	}
	pos, ok := r.cfg.PartnerPos()
	if !ok || !pos.valid() {
		t.gap = true
		return
	}
	t.add(pos, p.Room, now)
}

// add takes the partner's position p, in room, at now. A step from the
// newest sample that no walk makes, a gap in the samples across which the
// partner moved, and a room change end the walk. The middle one of the
// last three samples is then kept if it was walked over.
func (t *partnerTrail) add(p Pos, room int8, now time.Time) {
	s := trailPoint{p: p, room: room, at: now}
	if t.nraw > 0 {
		last := t.raw[t.nraw-1]
		switch dt := now.Sub(last.at); {
		case dt <= 0:
			return // this tick's sample is in
		case t.gap || dt > 2*TickEvery:
			// Samples are missing: the partner may have done anything
			// in between, unless it stood still.
			if !still(last.p, p) {
				t.pts = t.pts[:0]
			}
			t.nraw = 0
		case !walkStep(last, s):
			t.pts, t.nraw = t.pts[:0], 0
		}
	}
	t.gap = false
	if t.roomSince.IsZero() || room != t.room {
		t.pts = t.pts[:0]
		t.room, t.roomSince, t.nraw = room, now, 0
	}
	if t.nraw == len(t.raw) {
		copy(t.raw[:], t.raw[1:])
		t.nraw--
	}
	t.raw[t.nraw] = s
	t.nraw++
	if t.nraw < len(t.raw) {
		return
	}
	prev, mid, next := t.raw[0], t.raw[1], t.raw[2]
	switch n := len(t.pts); {
	case mid.at.Sub(t.roomSince) < trailLag:
		return
	case distXZ(prev.p, mid.p) == 0 || distXZ(mid.p, next.p) == 0:
		return // the partner stands still, or a pose came again: the walk goes on
	case !walkedOn(prev, mid, next):
		t.pts = t.pts[:0]
		return
	case n > 0 && distXZ(t.pts[n-1].p, mid.p) < TrailStep:
		return
	case n == TrailLen:
		copy(t.pts, t.pts[1:])
		t.pts = t.pts[:n-1]
	}
	t.pts = append(t.pts, mid)
}

// walkStep reports whether the partner's step from a to b, one sample
// apart, is one a walk makes: at most walkSpeed across the ground and
// walkDY up or down, a tick. A step is allowed at least a tick's walk:
// Link moves a frame at a time, so a tick that comes early can still see
// a whole frame of it.
func walkStep(a, b trailPoint) bool {
	n := stepTicks(a, b)
	return distXZ(a.p, b.p) <= walkSpeed*n && math.Abs(float64(b.p.Y)-float64(a.p.Y)) <= walkDY*n
}

// walkedOn reports whether the partner's Link looks to have walked over
// mid, coming from prev and going on to next by steps of a walk (add
// checks each step with walkStep as it comes), moving across the ground
// on both: its vertical speed changed by at most walkAccel a tick, and it
// climbed or dropped at most walkSlope per unit across the ground, plus
// walkStepY. Inferred: pose sync says nothing of the ground.
func walkedOn(prev, mid, next trailPoint) bool {
	vy1 := float64(mid.p.Y-prev.p.Y) / stepTicks(prev, mid)
	vy2 := float64(next.p.Y-mid.p.Y) / stepTicks(mid, next)
	climb := math.Abs(float64(next.p.Y) - float64(prev.p.Y))
	return math.Abs(vy2-vy1) <= walkAccel && climb <= walkSlope*distXZ(prev.p, next.p)+walkStepY
}

// still reports whether the partner at b stands where it stood at a: less
// than TrailStep from it across the ground and within walkDY of its
// height.
func still(a, b Pos) bool {
	return distXZ(a, b) < TrailStep && math.Abs(float64(b.Y)-float64(a.Y)) <= walkDY
}

// landing is where a warp to the partner at cur, in room, lands beside it:
// the newest spot of its walk in room LandMin..LandMax from cur across
// the ground and within LandMaxDY of its height. The walk must end where
// the partner stands: its newest sample is still from cur, so no step
// since, unsampled, can have taken the partner off it. ok is false when
// there is no such spot.
func (t *partnerTrail) landing(cur Pos, room int8) (Pos, bool) {
	if t.nraw == 0 || !still(t.raw[t.nraw-1].p, cur) {
		return Pos{}, false
	}
	for i := len(t.pts) - 1; i >= 0; i-- {
		q := &t.pts[i]
		d := distXZ(q.p, cur)
		if q.room == room && d >= LandMin && d <= LandMax && math.Abs(float64(q.p.Y)-float64(cur.Y)) <= LandMaxDY {
			return q.p, true
		}
	}
	return Pos{}, false
}

// distXZ is the distance from a to b across the ground.
func distXZ(a, b Pos) float64 {
	return math.Hypot(float64(a.X)-float64(b.X), float64(a.Z)-float64(b.Z))
}

// stepTicks is the time from a to b in ticks, at least one.
func stepTicks(a, b trailPoint) float64 { return max(ticksOf(b.at.Sub(a.at)), 1) }

// ticksOf is d in ticks.
func ticksOf(d time.Duration) float64 { return float64(d) / float64(TickEvery) }
