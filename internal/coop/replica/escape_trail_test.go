package replica

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
)

// walk moves the partner through ps, one position a tick, as pose sync
// has it at each of the replica's ticks.
func (er *escRig) walk(ps ...Pos) {
	er.t.Helper()
	for _, p := range ps {
		er.clk.advance(TickEvery)
		er.pos.set(p)
		er.tick()
	}
}

// alongX is the positions from x0 to x1 in steps of 20 along X, at height
// y and Z 0.
func alongX(x0, x1, y float32) []Pos {
	var ps []Pos
	for x := x0; x <= x1; x += 20 {
		ps = append(ps, Pos{X: x, Y: y})
	}
	return ps
}

// ramp raises ps by one unit for each run units along X.
func ramp(ps []Pos, run float32) []Pos {
	for i := range ps {
		ps[i].Y += ps[i].X / run
	}
	return ps
}

// atZ moves ps to z.
func atZ(ps []Pos, z float32) []Pos {
	for i := range ps {
		ps[i].Z = z
	}
	return ps
}

// onThePartner is what a warp that lands on the partner at p, in room 44,
// says when the partner's walk gives no spot beside it.
func onThePartner(p Pos) string {
	return fmt.Sprintf("warping to the partner at (%.0f, %.0f, %.0f) in room 44 (it has not walked far enough in this room for a spot beside it)", p.X, p.Y, p.Z)
}

// landedOnThePartner checks that warp number seq landed on the partner at
// p, and that res says so.
func (er *escRig) landedOnThePartner(seq uint32, res EscapeResult, p Pos) {
	er.t.Helper()
	if got := er.landed(seq); got != p {
		er.t.Fatalf("landed at %+v, want on the partner, %+v", got, p)
	}
	if want := onThePartner(p); res.Did != want {
		er.t.Errorf("did %q, want %q", res.Did, want)
	}
}

// landed is where the warp request asks Link to go, after warp number seq.
func (er *escRig) landed(seq uint32) Pos {
	er.t.Helper()
	if got, _ := er.warpSeq(); got != seq {
		er.t.Fatalf("warp_seq %d, want %d\n%s", got, seq, er.log)
	}
	b := er.read(WarpPos, 12)
	f := func(i int) float32 { return math.Float32frombits(binary.BigEndian.Uint32(b[4*i:])) }
	return Pos{X: f(0), Y: f(1), Z: f(2)}
}

// warpTo asks for a warp, which must be made.
func (er *escRig) warpTo() EscapeResult {
	er.t.Helper()
	res := er.escape(EscapeWarp)
	if res.Err != nil {
		er.t.Fatalf("warp: %v\n%s", res.Err, er.log)
	}
	return res
}

// The partner walks across Outset: /warp lands on the newest spot it
// walked over 100 units or more from it, however long ago, not on it.
func TestEscapeWarpLandsOnThePartnersTrail(t *testing.T) {
	cases := []struct {
		name string
		walk []Pos
		want Pos
		did  string
	}{
		{"on level ground", alongX(0, 400, 5), Pos{X: 300, Y: 5}, "warping to the partner's side at (300, 5, 0), 100 units from it, in room 44"},
		// A climb of 2 a tick is within walkDY: a walk, not a glide.
		{"up a gentle slope", ramp(alongX(0, 400, 0), 10), Pos{X: 300, Y: 30}, "warping to the partner's side at (300, 30, 0), 100 units from it, in room 44"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			er.presence("sea", 0, 0, 44, inGame)
			er.loadRoom(44, true)
			er.settled()
			er.walk(c.walk...)
			// The user goes to the other console: the trail has no age
			// limit.
			for range 3 {
				er.clk.advance(time.Minute)
				er.tick()
			}
			res := er.warpTo()
			if got := er.landed(1); got != c.want {
				t.Fatalf("landed at %+v, want %+v", got, c.want)
			}
			if res.Did != c.did || !er.log.has("co-op: warp: "+c.did) {
				t.Errorf("did %q\n%s", res.Did, er.log)
			}
		})
	}
}

// A crossing that is no walk (a jump, a fall, a Deku Leaf glide, a
// hookshot pull, a climb) ends the partner's walk: no spot of it is landed on, nor
// one on the side the partner came from. The partner here crosses a gap
// and stops just past it, too near for a spot beside it on the far side,
// so the warp lands on it.
func TestEscapeWarpTrailEndsAtACrossing(t *testing.T) {
	cases := []struct {
		name  string
		walk  []Pos // up to the edge
		cross []Pos // the crossing
		on    []Pos // past it
	}{
		// Link's vertical speed falls by 25 a tick: 50, 25, 0, -25, -50.
		{"a jump across a gap", alongX(-400, 0, 0),
			[]Pos{{X: 20, Y: 50}, {X: 40, Y: 75}, {X: 60, Y: 75}, {X: 80, Y: 50}, {X: 100}},
			alongX(120, 180, 0)},
		// A floor 300 up, too far above to land on. Off its edge, falling
		// faster, then 60 a tick, far from level: the spots 80 and 20 up
		// are in reach of the floor below.
		{"a fall", alongX(-200, 0, 300),
			[]Pos{{X: 20, Y: 290}, {X: 40, Y: 260}, {X: 60, Y: 200}, {X: 80, Y: 140}, {X: 100, Y: 80}, {X: 120, Y: 20}, {X: 140}},
			alongX(160, 240, 0)},
		// Off a ledge 200 up, 12 a frame across and 2 down, 36 and 6 a
		// tick (procFanGlide), onto a floor 140 up across the gap.
		{"a glide across a gap", alongX(-400, 0, 200),
			[]Pos{{X: 36, Y: 194}, {X: 72, Y: 188}, {X: 108, Y: 182}, {X: 144, Y: 176}, {X: 180, Y: 170}, {X: 216, Y: 164}, {X: 252, Y: 158}, {X: 288, Y: 152}, {X: 324, Y: 146}, {X: 360, Y: 140}},
			[]Pos{{X: 370, Y: 140}, {X: 380, Y: 140}, {X: 380, Y: 140}}},
		// Pulled level at 189 a tick to a target past the gap, then on to
		// stand on the floor under it.
		{"a hookshot pull across a gap", alongX(-400, 0, 0),
			[]Pos{{X: 189}, {X: 378}, {X: 567}},
			[]Pos{{X: 610}, {X: 610}, {X: 610}}},
		// Up a wall onto a ledge 60 up, 3 a tick up and 1 across: no step
		// climbs more than walkDY, but the climb is too steep for a walk.
		{"a slow climb up a wall", alongX(-400, 0, 0),
			func() (ps []Pos) {
				for i := float32(1); i <= 20; i++ {
					ps = append(ps, Pos{X: i, Y: 3 * i})
				}
				return ps
			}(),
			alongX(40, 100, 60)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			er.presence("sea", 0, 0, 44, inGame)
			er.loadRoom(44, true)
			er.settled()
			er.walk(c.walk...)
			er.walk(c.cross...)
			er.walk(c.on...)
			er.landedOnThePartner(1, er.warpTo(), c.on[len(c.on)-1])
		})
	}
}

// A spot is landed on only in the partner's room, and only from its stay
// in the stage: a reload of the stage starts the trail over. A room
// change ends the partner's walk, and across a door the room PRESENCE
// names can lead the pose: no spot is kept from the first three ticks in
// the new room.
func TestEscapeWarpTrailIsPerRoomAndStay(t *testing.T) {
	t.Run("another room", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 5, inGame)
		er.loadRoom(5, true)
		er.loadRoom(6, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.presence("sea", 0, 0, 6, inGame)
		er.walk(alongX(420, 460, 0)...)
		res := er.warpTo()
		if got := er.landed(1); got != (Pos{X: 460}) {
			t.Fatalf("landed at %+v, want on the partner in room 6, (460, 0, 0)", got)
		}
		if want := "in room 6 (it has not walked far enough in this room"; !strings.Contains(res.Did, want) {
			t.Errorf("did %q, want %q", res.Did, want)
		}
	})
	t.Run("back in the room", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 5, inGame)
		er.loadRoom(5, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.presence("sea", 0, 0, 6, inGame)
		er.walk(alongX(420, 440, 0)...)
		er.presence("sea", 0, 0, 5, inGame)
		er.walk(Pos{X: 460})
		// The walk in room 5 ended when PRESENCE said room 6: none of its
		// spots is joined to where the partner stands.
		res := er.warpTo()
		if got := er.landed(1); got != (Pos{X: 460}) {
			t.Fatalf("landed at %+v, want on the partner, (460, 0, 0)", got)
		}
		if want := "in room 5 (it has not walked far enough in this room"; !strings.Contains(res.Did, want) {
			t.Errorf("did %q, want %q", res.Did, want)
		}
	})
	t.Run("the room ahead of the pose", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 5, inGame)
		er.loadRoom(6, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		// PRESENCE says room 6 while pose sync has the partner still in
		// room 5 for the next three ticks (inferred: the two come over
		// different connections).
		er.presence("sea", 0, 0, 6, inGame)
		er.walk(alongX(420, 560, 0)...)
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: 560}) {
			t.Fatalf("landed at %+v, want on the partner, (560, 0, 0)", got)
		}
	})
	t.Run("a reload of the stage", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		// A void-out or a refresh: the same stage and point again.
		er.presence("sea", 0, 0, 44, inGame|wire.PresenceTransitioning)
		er.presence("sea", 0, 0, 44, inGame)
		er.settled()
		er.walk(Pos{X: 400}, Pos{X: 400})
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: 400}) {
			t.Fatalf("landed at %+v, want on the partner, (400, 0, 0)", got)
		}
	})
}

// A jump of the partner's position, faster than Link runs, ends its
// walk: no spot from before it is landed on.
func TestEscapeWarpTrailJumps(t *testing.T) {
	t.Run("a warp mid-walk", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.walk(Pos{X: 400, Z: 450}) // 450 in one tick
		er.landedOnThePartner(1, er.warpTo(), Pos{X: 400, Z: 450})
	})
	t.Run("a warp, then a walk", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.walk(atZ(alongX(400, 600, 0), 450)...)
		res := er.warpTo()
		if got := er.landed(1); got != (Pos{X: 500, Z: 450}) {
			t.Fatalf("landed at %+v, want on the walk after the warp, (500, 0, 450)", got)
		}
		if want := "warping to the partner's side at (500, 0, 450), 100 units from it, in room 44"; res.Did != want {
			t.Errorf("did %q, want %q", res.Did, want)
		}
	})
	t.Run("a warp out of reach", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.walk(Pos{X: 400, Z: 600})
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: 400, Z: 600}) {
			t.Fatalf("landed at %+v, want on the partner, (400, 0, 600)", got)
		}
	})
	t.Run("a position from the last stage, then the partner's own", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		// Standing keeps no spot, and the jump to the partner's own
		// position ends the walk: the spots after it are too near.
		er.walk(Pos{}, Pos{}, Pos{}, Pos{}, Pos{}, Pos{}, Pos{}, Pos{})
		er.walk(Pos{X: 450}, Pos{X: 470}, Pos{X: 490})
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: 490}) {
			t.Fatalf("landed at %+v, want on the partner, (490, 0, 0)", got)
		}
	})
}

// No spot is kept while the partner is in an event, which can carry its
// Link where it never walked, or paused.
func TestEscapeWarpTrailNotSampledInEventOrPaused(t *testing.T) {
	for name, f := range map[string]wire.PresenceFlags{"in an event": wire.PresenceInEvent, "paused": wire.PresencePaused} {
		t.Run(name, func(t *testing.T) {
			er := newEscRig(t)
			er.presence("sea", 0, 0, 44, inGame|f)
			er.loadRoom(44, true)
			er.settled()
			er.walk(alongX(0, 400, 0)...)
			er.presence("sea", 0, 0, 44, inGame)
			er.walk(Pos{X: 400})
			er.warpTo()
			if got := er.landed(1); got != (Pos{X: 400}) {
				t.Fatalf("landed at %+v, want on the partner, (400, 0, 0)", got)
			}
		})
	}
}

// The partner's walk goes on across ticks with no sample (an event, a
// pause, a tick missed) only if it stood still across them: in between it
// may have done anything.
func TestEscapeWarpTrailAcrossMissingSamples(t *testing.T) {
	walked := func(t *testing.T) *escRig {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		return er
	}
	t.Run("an event it stood through", func(t *testing.T) {
		er := walked(t)
		er.presence("sea", 0, 0, 44, inGame|wire.PresenceInEvent)
		er.walk(Pos{X: 400}, Pos{X: 400})
		er.presence("sea", 0, 0, 44, inGame)
		er.walk(Pos{X: 400})
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: 300}) {
			t.Fatalf("landed at %+v, want (300, 0, 0)", got)
		}
	})
	t.Run("an event that carried it", func(t *testing.T) {
		er := walked(t)
		// One tick: 200 in two ticks would pass for a walk.
		er.presence("sea", 0, 0, 44, inGame|wire.PresenceInEvent)
		er.walk(Pos{X: 400, Z: 200})
		er.presence("sea", 0, 0, 44, inGame)
		er.walk(Pos{X: 400, Z: 200})
		er.landedOnThePartner(1, er.warpTo(), Pos{X: 400, Z: 200})
	})
	t.Run("a warp in an event that carried it", func(t *testing.T) {
		er := walked(t)
		er.presence("sea", 0, 0, 44, inGame|wire.PresenceInEvent)
		er.pos.set(Pos{X: 400, Z: 200})
		er.landedOnThePartner(1, er.warpTo(), Pos{X: 400, Z: 200})
	})
	t.Run("ticks missed while it walked", func(t *testing.T) {
		er := walked(t)
		er.clk.advance(2 * TickEvery)
		er.walk(alongX(460, 480, 0)...)
		er.landedOnThePartner(1, er.warpTo(), Pos{X: 480})
	})
}

// A tick that comes early, 10 ms after the last, can still see a whole
// frame of the partner's run, 17 units: a step is allowed a tick's walk
// at least, and the walk goes on.
func TestEscapeWarpTrailEarlyTick(t *testing.T) {
	er := newEscRig(t)
	er.presence("sea", 0, 0, 44, inGame)
	er.loadRoom(44, true)
	er.settled()
	er.walk(alongX(0, 300, 0)...)
	er.clk.advance(10 * time.Millisecond)
	er.pos.set(Pos{X: 317})
	er.tick()
	er.clk.advance(TickEvery - 10*time.Millisecond)
	er.pos.set(Pos{X: 340})
	er.tick()
	er.walk(alongX(360, 400, 0)...)
	er.warpTo()
	if got := er.landed(1); got != (Pos{X: 300}) {
		t.Fatalf("landed at %+v, want (300, 0, 0)", got)
	}
}

// The trail keeps at most TrailLen spots, the newest.
func TestPartnerTrailIsBounded(t *testing.T) {
	var tr partnerTrail
	at := time.Unix(1_800_000_000, 0)
	for i := range 10_000 {
		at = at.Add(TickEvery)
		tr.add(Pos{X: float32(i%1000) * 20}, 5, at)
		if len(tr.pts) > TrailLen {
			t.Fatalf("%d spots after %d samples", len(tr.pts), i+1)
		}
	}
	if n := len(tr.pts); n != TrailLen || at.Sub(tr.pts[n-1].at) != TickEvery {
		t.Fatalf("%d spots, the newest from %v before the last sample", n, at.Sub(tr.pts[n-1].at))
	}
}

// A slow walk keeps spots TrailStep apart.
func TestPartnerTrailStep(t *testing.T) {
	var tr partnerTrail
	at := time.Unix(1_800_000_000, 0)
	for i := range 200 {
		at = at.Add(TickEvery)
		tr.add(Pos{X: float32(i) * 4}, 5, at)
	}
	if len(tr.pts) < 2 {
		t.Fatalf("%d spots", len(tr.pts))
	}
	for i := 1; i < len(tr.pts); i++ {
		if d := distXZ(tr.pts[i-1].p, tr.pts[i].p); d < TrailStep {
			t.Fatalf("spots %d and %d are %v apart", i-1, i, d)
		}
	}
}

// The second step of a warp to another stage lands beside the partner
// too: its trail is sampled while Link waits in the stage for the
// partner's room to load.
func TestEscapeWarpSecondStepUsesTrail(t *testing.T) {
	er := newEscRig(t)
	er.presence("sea", 0, 0, 44, inGame)
	er.presence("sea", 0, 0, 44, inGame|wire.PresenceTransitioning)
	er.presence("M_NewD2", 2, 3, 5, 0)
	er.presence("M_NewD2", 2, 3, 5, inGame)
	er.presence("M_NewD2", 2, 3, 6, inGame)
	er.settled()
	er.warpTo()
	if err := er.g.SetNextStage(drc); err != nil {
		t.Fatal(err)
	}
	if err := er.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	// Room 6 is not loaded in this game yet: the second step waits, and
	// the partner walks.
	er.walk(alongX(0, 400, 0)...)
	if seq, _ := er.warpSeq(); seq != 0 {
		t.Fatalf("warped into an unloaded room (warp_seq %d)", seq)
	}
	er.loadRoom(6, true)
	er.ticks(gate.StableTicks + 2)
	if got := er.landed(1); got != (Pos{X: 300}) {
		t.Fatalf("landed at %+v, want (300, 0, 0)", got)
	}
	if !er.log.has("co-op: warp: warping to the partner's side at (300, 0, 0), 100 units from it, in room 6") {
		t.Errorf("log:\n%s", er.log)
	}
}

// With no spot beside the partner, or on the sea, /warp lands on the
// partner, as before, and says why.
func TestEscapeWarpFallsBackOntoThePartner(t *testing.T) {
	cases := []struct {
		name  string
		setup func(er *escRig)
		walk  []Pos
		did   string
	}{
		{"standing still", nil, []Pos{{X: 100, Y: 5, Z: -200}, {X: 100, Y: 5, Z: -200}, {X: 100, Y: 5, Z: -200}, {X: 100, Y: 5, Z: -200}, {X: 100, Y: 5, Z: -200}},
			"warping to the partner at (100, 5, -200) in room 44 (it has not walked far enough in this room for a spot beside it)"},
		{"a short walk", nil, alongX(0, 80, 5),
			"warping to the partner at (80, 5, 0) in room 44 (it has not walked far enough in this room for a spot beside it)"},
		// A climb of 10 a tick is more than walkDY: a ramp walked that
		// fast looks like a glide or a fall, and keeps no spot.
		{"up a steep slope", nil, ramp(alongX(0, 400, 0), 2),
			"warping to the partner at (400, 200, 0) in room 44 (it has not walked far enough in this room for a spot beside it)"},
		{"on the sea", func(er *escRig) { er.g.SetStageType(stageTypeSea) }, alongX(0, 400, 5),
			"warping to the partner at (400, 5, 0) in room 44 (on the sea a spot beside it can be open water)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			if c.setup != nil {
				c.setup(er)
			}
			er.presence("sea", 0, 0, 44, inGame)
			er.loadRoom(44, true)
			er.settled()
			er.walk(c.walk...)
			res := er.warpTo()
			if got, want := er.landed(1), c.walk[len(c.walk)-1]; got != want {
				t.Fatalf("landed at %+v, want on the partner, %+v", got, want)
			}
			if res.Did != c.did || !er.log.has("co-op: warp: "+c.did) {
				t.Errorf("did %q, want %q", res.Did, c.did)
			}
		})
	}
}
