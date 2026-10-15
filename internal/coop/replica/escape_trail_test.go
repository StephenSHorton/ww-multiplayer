package replica

import (
	"encoding/binary"
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
		y    func(x float32) float32
		want Pos
		did  string
	}{
		{"on level ground", func(float32) float32 { return 5 }, Pos{X: 300, Y: 5}, "warping to the partner's side at (300, 5, 0), 100 units from it, in room 44"},
		// A climb of 10 a step of 20 is a walk, not a jump.
		{"up a slope", func(x float32) float32 { return x / 2 }, Pos{X: 300, Y: 150}, "warping to the partner's side at (300, 150, 0), 100 units from it, in room 44"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			er := newEscRig(t)
			er.presence("sea", 0, 0, 44, inGame)
			er.loadRoom(44, true)
			er.settled()
			ps := alongX(0, 400, 0)
			for i := range ps {
				ps[i].Y = c.y(ps[i].X)
			}
			er.walk(ps...)
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

// A jump's or a fall's arc is no walk: no spot of it is landed on.
func TestEscapeWarpTrailSkipsJumpsAndFalls(t *testing.T) {
	t.Run("a jump", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(-400, 0, 0)...)
		// Link's vertical speed falls by 25 a tick: 50, 25, 0, -25, -50.
		er.walk(Pos{X: 20, Y: 50}, Pos{X: 40, Y: 75}, Pos{X: 60, Y: 75}, Pos{X: 80, Y: 50}, Pos{X: 100})
		er.walk(alongX(120, 180, 0)...)
		er.warpTo()
		if got := er.landed(1); got != (Pos{X: -20}) {
			t.Fatalf("landed at %+v, want the last spot before the jump, (-20, 0, 0)", got)
		}
	})
	t.Run("a fall", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(-200, 0, 300)...) // a floor 300 up, too far above to land on
		// Off its edge, falling faster, then 60 a tick, steeper than a
		// slope: the spots 80 and 20 up are in reach of the floor below.
		er.walk(Pos{X: 20, Y: 290}, Pos{X: 40, Y: 260}, Pos{X: 60, Y: 200}, Pos{X: 80, Y: 140}, Pos{X: 100, Y: 80}, Pos{X: 120, Y: 20}, Pos{X: 140})
		er.walk(alongX(160, 240, 0)...)
		res := er.warpTo()
		if got := er.landed(1); got != (Pos{X: 240}) {
			t.Fatalf("landed at %+v, want on the partner, (240, 0, 0)", got)
		}
		if want := "warping to the partner at (240, 0, 0) in room 44 (it has not walked far enough in this room for a spot beside it)"; res.Did != want {
			t.Errorf("did %q, want %q", res.Did, want)
		}
	})
}

// A spot is landed on only in the partner's room, and only from its stay
// in the stage: a reload of the stage starts the trail over. Across a
// door the room PRESENCE names can lag the pose or lead it: the spots of
// the last three ticks before a room change are dropped, and none is kept
// from the first three in the new room.
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
		er.warpTo()
		// 360 and 380 were walked over in the last three ticks before
		// PRESENCE said room 6.
		if got := er.landed(1); got != (Pos{X: 340}) {
			t.Fatalf("landed at %+v, want (340, 0, 0)", got)
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

// A jump of the partner's position, faster than Link runs, drops the
// spots of the last three ticks, and no spot is judged across it.
func TestEscapeWarpTrailJumps(t *testing.T) {
	t.Run("a warp mid-walk", func(t *testing.T) {
		er := newEscRig(t)
		er.presence("sea", 0, 0, 44, inGame)
		er.loadRoom(44, true)
		er.settled()
		er.walk(alongX(0, 400, 0)...)
		er.walk(Pos{X: 400, Z: 450}) // 450 in one tick
		res := er.warpTo()
		if got := er.landed(1); got != (Pos{X: 340}) {
			t.Fatalf("landed at %+v, want (340, 0, 0)", got)
		}
		if want := "warping to the partner's side at (340, 0, 0), 454 units from it, in room 44"; res.Did != want {
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
		// Standing is no walk: no spot is kept there, so the jump has none
		// older than three ticks to leave.
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
// too: its trail is sampled while Link changes stage.
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
