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
// into a room this game has loaded (mpBgW). The warp lands on the newest
// spot in the partner's room LandMin..LandMax from it across the ground
// and within LandMaxDY in height, else on the partner itself, as before.
//
// The walked-on test (walkedOn) and every threshold here are inferred, to
// be calibrated on a live trail. A spot on a moving platform that has
// since moved (a lift, a raft), a ledge or wall the partner hung on or
// climbed along, or water it swam across passes the test, and a warp onto
// it can drop Link to the floor below, at worst a void-out. On the sea the
// trail is not used (sameStageWarp).

const (
	// TrailLen bounds the trail: the newest TrailLen spots are kept.
	TrailLen = 64
	// TrailStep is how far apart across the ground the spots kept are.
	TrailStep = 15
	// LandMin and LandMax bound how far across the ground from the
	// partner a warp lands: clear of Link's body cylinder, of radius 30
	// or 50 (d_a_player_main.cpp:9737-9741), and near enough to see it.
	LandMin = 100
	LandMax = 500
	// LandMaxDY bounds how far above or below the partner a warp lands.
	LandMaxDY = 100
	// TrailJump is the step across the ground between two samples past
	// which the partner's position jumped rather than moved: Link runs at
	// most 17 units a frame (mMaxNormalSpeed, d_a_player_main.cpp:3328;
	// daPy_HIO_move_c0::m, d_a_player_HIO_data.inc:9), about 51 a tick.
	// A dev warp, or a position still in another stage's frame, jumps.
	TrailJump = 400
)

const (
	// walkAccel is how much the partner's vertical speed may change, in
	// units a tick, between the two steps around a spot kept: a jump or a
	// fall changes it every frame.
	walkAccel = 4
	// walkSlope and walkStepY bound the climb or drop across the steps
	// around a spot kept: walkSlope units per unit across the ground,
	// plus walkStepY.
	walkSlope = 0.8
	walkStepY = 3
	// trailLag is how far the partner's PRESENCE room may lag or lead its
	// pose across a door, the two coming over different connections: a
	// room change drops the spots of the last trailLag, and keeps none for
	// trailLag in the new room. A jump drops the same spots.
	trailLag = 3 * TickEvery
)

// stageTypeSea is the sea's STAG stage type, dStageType_SEA_e
// (d_stage.h:24): there the water's height comes from the sea's own map
// (d_a_sea.cpp:102), and a void-out restarts by the ship
// (d_a_player_main.cpp:10873-10880).
const stageTypeSea = 7

// partnerTrail is the spots the partner walked over in one stay in its
// stage: rest and since are the stage and point it is at rest in, and
// since when (partnerTrack).
type partnerTrail struct {
	rest      entryKey
	since     time.Time
	room      int8      // the room of the last sample
	roomSince time.Time // when the samples came into it; zero before the first
	raw       [3]trailPoint
	nraw      int          // samples in raw, oldest first, being judged
	pts       []trailPoint // the spots kept, oldest first, at most TrailLen
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
// start the trail over (notePartnerRest); any other missing sample only
// drops the samples being judged.
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
		t.nraw = 0
		return
	}
	pos, ok := r.cfg.PartnerPos()
	if !ok || !pos.valid() {
		t.nraw = 0
		return
	}
	t.add(pos, p.Room, now)
}

// add takes the partner's position p, in room, at now, and keeps the
// middle one of the last three samples if it was walked over.
func (t *partnerTrail) add(p Pos, room int8, now time.Time) {
	if t.nraw > 0 {
		last := &t.raw[t.nraw-1]
		switch dt := now.Sub(last.at); {
		case dt <= 0:
			return // this tick's sample is in
		case distXZ(last.p, p) > TrailJump:
			t.nraw = 0
			t.dropRecent(now)
		case dt > 2*TickEvery:
			t.nraw = 0 // a sample is missing
		}
	}
	if t.roomSince.IsZero() || room != t.room {
		if !t.roomSince.IsZero() {
			t.dropRecent(now)
		}
		t.room, t.roomSince, t.nraw = room, now, 0
	}
	if t.nraw == len(t.raw) {
		copy(t.raw[:], t.raw[1:])
		t.nraw--
	}
	t.raw[t.nraw] = trailPoint{p: p, room: room, at: now}
	t.nraw++
	if t.nraw < len(t.raw) {
		return
	}
	mid := t.raw[1]
	switch n := len(t.pts); {
	case mid.at.Sub(t.roomSince) < trailLag, !walkedOn(t.raw[0], mid, t.raw[2]):
		return
	case n > 0 && distXZ(t.pts[n-1].p, mid.p) < TrailStep:
		return
	case n == TrailLen:
		copy(t.pts, t.pts[1:])
		t.pts = t.pts[:n-1]
	}
	t.pts = append(t.pts, mid)
}

// dropRecent drops the spots sampled within trailLag of now.
func (t *partnerTrail) dropRecent(now time.Time) {
	n := len(t.pts)
	for n > 0 && now.Sub(t.pts[n-1].at) <= trailLag {
		n--
	}
	t.pts = t.pts[:n]
}

// walkedOn reports whether the partner's Link looks to have walked over
// mid, coming from prev and going on to next, one sample apart (add keeps
// them at most two ticks apart): it moved across the ground on both
// sides, its vertical speed changed by at most walkAccel a tick, and it
// climbed or dropped at most walkSlope per unit across the ground, plus
// walkStepY. Inferred: pose sync says nothing of the ground.
func walkedOn(prev, mid, next trailPoint) bool {
	if distXZ(prev.p, mid.p) == 0 || distXZ(mid.p, next.p) == 0 {
		return false
	}
	vy1 := float64(mid.p.Y-prev.p.Y) / ticksOf(mid.at.Sub(prev.at))
	vy2 := float64(next.p.Y-mid.p.Y) / ticksOf(next.at.Sub(mid.at))
	climb := math.Abs(float64(next.p.Y) - float64(prev.p.Y))
	return math.Abs(vy2-vy1) <= walkAccel && climb <= walkSlope*distXZ(prev.p, next.p)+walkStepY
}

// landing is where a warp to the partner at cur, in room, lands beside it:
// the newest spot kept in room LandMin..LandMax from cur across the ground
// and within LandMaxDY of its height. ok is false when there is none.
func (t *partnerTrail) landing(cur Pos, room int8) (Pos, bool) {
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

// ticksOf is d in ticks.
func ticksOf(d time.Duration) float64 { return float64(d) / float64(TickEvery) }
