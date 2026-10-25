package feed

import (
	"context"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar/avatartest"
)

// TestFeederStageGate: a partner whose PRESENCE puts it in another stage
// (or on its way to one, or out of game) has its mini-Link cleared, its
// link slot kept and its view not live; Expire leaves the slot alone.
// Back in this stage it is re-armed and drawn again, snapped to where it
// is. A stale place, or no place, gates nobody.
func TestFeederStageGate(t *testing.T) {
	f, c, rep := newTestFeeder(t) // this game is in "sea"
	f.partner = &PartnerPlace{}
	ctx := context.Background()
	now := time.Unix(1000, 0)
	feed := func(x float32) []network.RemotePlayer {
		rs := []network.RemotePlayer{remote(1, x, true, now)}
		f.Feed(ctx, rs, now)
		f.Expire(now)
		return rs
	}

	f.partner.Publish("sea", true, false, now)
	feed(100)
	if !c.wrotePoseOf(0, 1) {
		t.Fatal("same stage: no pose drawn")
	}

	for _, tc := range []struct {
		name           string
		stage          string
		inGame, moving bool
	}{
		{"other stage", "M_NewD2", true, false},
		{"stage change", "sea", true, true},
		{"out of game", "sea", false, false},
	} {
		now = now.Add(16 * time.Millisecond)
		f.partner.Publish(tc.stage, tc.inGame, tc.moving, now)
		rs := feed(5000)
		if c.poseSeq(0) != 0 {
			t.Errorf("%s: mini-Link still drawn (pose_seq %d)", tc.name, c.poseSeq(0))
		}
		if s, ok := f.LinkSlots()[1]; !ok || s != 0 {
			t.Errorf("%s: link slot not kept: %v", tc.name, f.LinkSlots())
		}
		if vs := f.Views(rs, now); len(vs) != 1 || vs[0].Live {
			t.Errorf("%s: views = %+v, want one not live", tc.name, vs)
		}
		if c.actorActive(0) {
			t.Errorf("%s: actor puppet up for a hidden Link", tc.name)
		}
		now = now.Add(despawnReserve + time.Second)
		f.partner.Publish(tc.stage, tc.inGame, tc.moving, now)
		feed(5000)
		if _, ok := f.LinkSlots()[1]; !ok {
			t.Errorf("%s: Expire freed a hidden slot", tc.name)
		}

		now = now.Add(16 * time.Millisecond)
		f.partner.Publish("sea", true, false, now)
		feed(200)
		if !c.wrotePoseOf(0, 1) {
			t.Errorf("%s: back in this stage: no pose drawn (seq %d)", tc.name, c.poseSeq(0))
		}
		if x := c.actorX(0); x != 200 {
			t.Errorf("%s: back in this stage at X %v, want a snap to 200", tc.name, x)
		}
	}
	if !rep.has("out of this stage (M_NewD2; here sea)") || !rep.has("is in this stage (sea): shown") {
		t.Errorf("gate not logged: %q", rep.lines)
	}

	// A stale place gates nobody.
	now = now.Add(16 * time.Millisecond)
	f.partner.Publish("M_NewD2", true, false, now)
	feed(100)
	if c.poseSeq(0) != 0 {
		t.Fatal("other stage: mini-Link still drawn")
	}
	now = now.Add(PartnerStale + time.Millisecond)
	feed(100)
	if !c.wrotePoseOf(0, 1) {
		t.Error("stale place: mini-Link still hidden")
	}
}

// TestFeederStageGateHolds: while this game is between stages the gate
// keeps its decision; with two remotes (PRESENCE names no sender) or no
// published place it hides nobody.
func TestFeederStageGateHolds(t *testing.T) {
	f, c, _ := newTestFeeder(t)
	f.partner = &PartnerPlace{}
	ctx := context.Background()
	now := time.Unix(1000, 0)
	one := func() []network.RemotePlayer { return []network.RemotePlayer{remote(1, 100, true, now)} }

	f.partner.Publish("M_NewD2", true, false, now)
	f.Feed(ctx, one(), now)
	c.stage("sea", avatartest.Link, true) // this game's stage change
	now = now.Add(16 * time.Millisecond)
	f.partner.Publish("M_NewD2", true, false, now)
	f.Feed(ctx, one(), now)
	if !f.away || c.poseSeq(0) != 0 {
		t.Fatalf("between stages: gate let go (away %v, pose_seq %d)", f.away, c.poseSeq(0))
	}
	c.stage("M_NewD2", avatartest.Link, false)
	now = now.Add(16 * time.Millisecond)
	f.Feed(ctx, one(), now)
	if f.away || !c.wrotePoseOf(0, 1) {
		t.Fatalf("same stage after the change: away %v, pose_seq %d", f.away, c.poseSeq(0))
	}

	// Two remotes: whose PRESENCE it is is unknown, so nobody is hidden.
	f.partner.Publish("sea", true, false, now)
	now = now.Add(16 * time.Millisecond)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, true, now)}, now)
	if f.away || !c.wrotePoseOf(0, 1) || !c.wrotePoseOf(1, 2) {
		t.Errorf("two remotes: away %v, pose_seqs %d %d", f.away, c.poseSeq(0), c.poseSeq(1))
	}

	// No registry at all (no co-op): as before the gate.
	g, gc, _ := newTestFeeder(t)
	g.Feed(ctx, one(), now)
	if g.away || !gc.wrotePoseOf(0, 1) {
		t.Error("no partner place: mini-Link not drawn")
	}
}

// TestPartnerFor: one place per (name, pid), shared by every caller.
func TestPartnerFor(t *testing.T) {
	a := PartnerFor("partner-test", 1)
	if PartnerFor("partner-test", 1) != a || PartnerFor("partner-test", 2) == a || PartnerFor("partner-other", 1) == a {
		t.Error("PartnerFor is not keyed by (name, pid)")
	}
	now := time.Unix(1000, 0)
	a.Publish("sea", true, false, now)
	if away, known, _ := a.elsewhere("sea", now); away || !known {
		t.Errorf("same stage: away %v known %v", away, known)
	}
	if away, known, there := a.elsewhere("M_NewD2", now); !away || !known || there != "sea" {
		t.Errorf("other stage: away %v known %v there %q", away, known, there)
	}
	a.Publish("", false, false, now) // the hub's PRESENCE for a peer that left
	if _, known, _ := a.elsewhere("sea", now); known {
		t.Error("a stageless PRESENCE is known")
	}
}

// placed is remote with a position place as of now.
func placed(id byte, x float32, now time.Time, stage string, inPlay bool) network.RemotePlayer {
	rp := remote(id, x, true, now)
	rp.Place, rp.HasPlace, rp.PlaceAt = network.Place{Stage: stage, InPlay: inPlay}, true, now
	return rp
}

// TestFeederPlaceGate (relay scoping): each remote whose positions carry
// a place is gated by its own stage, with two remotes as with one (the
// PRESENCE gate stood down at two). A remote in another stage, or between
// stages, is hidden at once, its slot kept; back here it is drawn again.
// A stale place falls back to PRESENCE (none here: shown).
func TestFeederPlaceGate(t *testing.T) {
	f, c, rep := newTestFeeder(t) // this game is in "sea"
	ctx := context.Background()
	now := time.Unix(1000, 0)
	feed := func(rs ...network.RemotePlayer) {
		f.Feed(ctx, rs, now)
		f.Expire(now)
		now = now.Add(16 * time.Millisecond)
	}

	feed(placed(1, 100, now, "sea", true), placed(2, 200, now, "M_NewD2", true))
	if !c.wrotePoseOf(0, 1) {
		t.Fatal("the sailor is not drawn")
	}
	if c.poseSeq(1) != 0 {
		t.Errorf("the remote in M_NewD2 is drawn (pose_seq %d)", c.poseSeq(1))
	}
	rs := []network.RemotePlayer{placed(1, 100, now, "sea", true), placed(2, 200, now, "M_NewD2", true)}
	if vs := f.Views(rs, now); len(vs) != 2 || !vs[0].Live && !vs[1].Live || vs[0].Live && vs[1].Live {
		t.Errorf("views %+v, want exactly the sailor live", vs)
	}

	// Remote 2 between stages: still hidden. Then on the sea: drawn.
	feed(placed(1, 100, now, "sea", true), placed(2, 200, now, "sea", false))
	if c.poseSeq(1) != 0 {
		t.Error("a remote between stages is drawn")
	}
	feed(placed(1, 100, now, "sea", true), placed(2, 300, now, "sea", true))
	if !c.wrotePoseOf(1, 2) {
		t.Errorf("remote 2 back on the sea is not drawn (pose_seq %d)", c.poseSeq(1))
	}
	if !rep.has("is in another stage (M_NewD2; here sea)") || !rep.has("is in this stage (sea): shown") {
		t.Errorf("place gate not logged: %q", rep.lines)
	}

	// Remote 1 to M_NewD2, then its place goes stale with no newer
	// position (a long load: its Link is gone): still hidden. Its next
	// position without a place (a sender of an older build) puts it
	// back under PRESENCE (none here: shown, as before the gate).
	feed(placed(1, 100, now, "M_NewD2", true), placed(2, 300, now, "sea", true))
	if c.poseSeq(0) != 0 {
		t.Fatal("remote 1 in M_NewD2 is drawn")
	}
	old := placed(1, 100, now, "M_NewD2", true)
	now = now.Add(network.PlaceStale + time.Second)
	old.LastPose = now
	feed(old, placed(2, 300, now, "sea", true))
	if c.poseSeq(0) != 0 {
		t.Error("a stale place that said elsewhere let remote 1 show")
	}
	plain := remote(1, 100, true, now)
	feed(plain, placed(2, 300, now, "sea", true))
	if !c.wrotePoseOf(0, 1) {
		t.Error("a remote without a place is still hidden")
	}
}

// TestFeederPlaceGateNoFalseSilence (relay scoping, review fix): the
// scoped server relays no pose of a remote in another stage, so its
// LastPose here stays as old as the time apart. Walking into its stage
// (this game's place changes at once; its poses come back a round trip
// later) must not log it frozen or despawned, nor clear its slot: it is
// measured from when it was shown. With no pose after that for
// freezeAfter, it is a silent sender again and freezes as before.
func TestFeederPlaceGateNoFalseSilence(t *testing.T) {
	f, c, rep := newTestFeeder(t) // this game is in "sea"
	ctx := context.Background()
	now := time.Unix(1000, 0)
	lastPose := now
	b := func(stage string) network.RemotePlayer {
		rp := placed(1, 100, now, stage, true) // positions keep coming
		rp.LastPose = lastPose                 // poses do not
		return rp
	}
	feed := func(stage string) {
		f.Feed(ctx, []network.RemotePlayer{b(stage)}, now)
		f.Expire(now)
	}

	feed("M_NewD2")
	for end := now.Add(f.despawnAfter + 3*time.Second); now.Before(end); now = now.Add(100 * time.Millisecond) {
		feed("M_NewD2")
	}
	if c.poseSeq(0) != 0 {
		t.Fatal("the remote in another stage is drawn")
	}
	c.stage("M_NewD2", avatartest.Link, false) // this game walks in
	for end := now.Add(300 * time.Millisecond); now.Before(end); now = now.Add(16 * time.Millisecond) {
		feed("M_NewD2")
	}
	if rep.has("frozen") || rep.has("despawned") {
		t.Errorf("walking into its stage logged a silent sender: %q", rep.lines)
	}
	if !c.wrotePoseOf(0, 1) {
		t.Error("the remote is not drawn on walking into its stage")
	}
	if vs := f.Views([]network.RemotePlayer{b("M_NewD2")}, now); len(vs) != 1 || !vs[0].Live {
		t.Errorf("views %+v, want it live", vs)
	}

	// Its poses arrive: drawn, no resume line (it never froze).
	lastPose = now
	feed("M_NewD2")
	if rep.has("resumed") {
		t.Errorf("a fresh pose logged a resume: %q", rep.lines)
	}

	// Then truly silent: frozen after freezeAfter, as on main.
	now = now.Add(f.freezeAfter + 100*time.Millisecond)
	feed("M_NewD2")
	if !rep.has("frozen: player 1 pose stalled") {
		t.Errorf("a silent remote did not freeze: %q", rep.lines)
	}
}

// TestFeederPresenceOnlyForOneDrawn (review fix): with two remotes drawn,
// one placed and one not, PRESENCE (which names no sender, and may be the
// placed one's) does not gate the unplaced one; alone, it does.
func TestFeederPresenceOnlyForOneDrawn(t *testing.T) {
	f, c, _ := newTestFeeder(t) // this game is in "sea"
	f.partner = &PartnerPlace{}
	ctx := context.Background()
	now := time.Unix(1000, 0)

	f.partner.Publish("M_NewD2", true, false, now) // remote 2's, say
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now), placed(2, 200, now, "M_NewD2", true)}, now)
	if f.away || !c.wrotePoseOf(0, 1) {
		t.Errorf("an unplaced remote beside a placed one was gated by PRESENCE (away %v, pose_seq %d)", f.away, c.poseSeq(0))
	}
	if c.poseSeq(1) != 0 {
		t.Error("the placed remote in M_NewD2 is drawn")
	}

	now = now.Add(16 * time.Millisecond)
	f.partner.Publish("M_NewD2", true, false, now)
	f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now)}, now)
	if !f.away {
		t.Error("PRESENCE does not gate the one remote drawn")
	}
}
