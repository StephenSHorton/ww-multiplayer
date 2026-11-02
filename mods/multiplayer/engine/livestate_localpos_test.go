package engine

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// TestLiveState_LocalPos_RoundTrip verifies the #22 minimap bridge:
// setLocalPos publishes coords, localPos reads them back with ok=true, and a
// fresh liveState reports ok=false before any tick has landed.
func TestLiveState_LocalPos_RoundTrip(t *testing.T) {
	ls := &LiveState{}

	if _, _, _, ok := ls.LocalPos(); ok {
		t.Fatal("localPos() on a fresh liveState should report ok=false")
	}

	ls.SetLocalPos(1.5, 2.5, 3.5, network.Place{}, false)
	x, y, z, ok := ls.LocalPos()
	if !ok {
		t.Fatal("localPos() after setLocalPos should report ok=true")
	}
	if x != 1.5 || y != 2.5 || z != 3.5 {
		t.Errorf("localPos() = (%v,%v,%v), want (1.5,2.5,3.5)", x, y, z)
	}
}

// TestLiveState_LocalPos_ResetOnBegin verifies begin() (called at the start
// of every Host/Join session) clears any stale position left over from a
// prior session, so a fresh dashboard doesn't briefly show the old player's
// last coords as "self".
func TestLiveState_LocalPos_ResetOnBegin(t *testing.T) {
	ls := &LiveState{}
	ls.SetLocalPos(10, 20, 30, network.Place{Stage: "sea", Room: 11, InPlay: true}, true)

	ls.Begin("Host", "Link", nil)

	if _, _, _, ok := ls.LocalPos(); ok {
		t.Error("localPos() should report ok=false immediately after begin() resets the session")
	}
}

// TestLiveStateLocalPosSetter_NilSafe verifies the adapter mirrors
// liveStateClientSetter: nil ls -> nil callback (so CLI paths that pass
// ls=nil into runMultiplayerGoroutines get a genuinely nil onPos, not a
// callback that panics on a nil receiver).
func TestLiveStateLocalPosSetter_NilSafe(t *testing.T) {
	if f := LiveStateLocalPosSetter(nil); f != nil {
		t.Error("liveStateLocalPosSetter(nil) should return a nil func")
	}
}

// TestLiveStateLocalPosSetter_WiresIntoLiveState verifies the non-nil case
// actually publishes into the given liveState -- the plumbing
// feed.BroadcastPose's onPos callback depends on.
func TestLiveStateLocalPosSetter_WiresIntoLiveState(t *testing.T) {
	ls := &LiveState{}
	setter := LiveStateLocalPosSetter(ls)
	if setter == nil {
		t.Fatal("liveStateLocalPosSetter(non-nil) should return a non-nil func")
	}

	setter(7, 8, 9, network.Place{Stage: "M_NewD2", Room: 3, InPlay: true}, true)

	x, y, z, ok := ls.LocalPos()
	if !ok || x != 7 || y != 8 || z != 9 {
		t.Errorf("localPos() after setter callback = (%v,%v,%v,ok=%v), want (7,8,9,true)", x, y, z, ok)
	}
}

// TestLiveState_LocalPlace: the setter publishes this game's place with
// its position, a failed or empty place reads as unknown, and Begin clears it.
func TestLiveState_LocalPlace(t *testing.T) {
	ls := &LiveState{}
	if _, ok := ls.LocalPlace(); ok {
		t.Fatal("LocalPlace() on a fresh LiveState should report ok=false")
	}
	LiveStateLocalPosSetter(ls)(7, 8, 9, network.Place{Stage: "M_NewD2", Room: 3, InPlay: true}, true)
	if p, ok := ls.LocalPlace(); !ok || p.Stage != "M_NewD2" || p.Room != 3 || !p.InPlay {
		t.Errorf("LocalPlace() = %+v, %v; want M_NewD2 room 3 in play", p, ok)
	}
	ls.SetLocalPos(7, 8, 9, network.Place{Stage: "M_NewD2"}, false)
	if _, ok := ls.LocalPlace(); ok {
		t.Error("a failed place read should leave LocalPlace() unknown")
	}
	ls.SetLocalPos(7, 8, 9, network.Place{}, true)
	if _, ok := ls.LocalPlace(); ok {
		t.Error("an empty stage should leave LocalPlace() unknown")
	}
	ls.SetLocalPos(7, 8, 9, network.Place{Stage: "sea", Room: 11, InPlay: true}, true)
	ls.Begin("Join", "Link", nil)
	if _, ok := ls.LocalPlace(); ok {
		t.Error("Begin should clear the local place")
	}
}

// TestPlayerViewsPlace: each remote carries its last place trailer, its age
// and whether it is still believed (PlaceStale); none without a trailer,
// and none when no clock is given (PlayerCount).
func TestPlayerViewsPlace(t *testing.T) {
	now := time.Unix(1000, 0)
	pos := &network.PlayerPosition{PosX: 1, PosY: 2, PosZ: 3}
	in := []network.RemotePlayer{
		{ID: 1, Name: "Tetra", Position: pos, Place: network.Place{Stage: "M_NewD2", Room: 3, InPlay: true}, HasPlace: true, PlaceAt: now.Add(-time.Second)},
		{ID: 2, Name: "Medli", Position: pos, Place: network.Place{Stage: "sea", Room: 11, InPlay: true}, HasPlace: true, PlaceAt: now.Add(-10 * time.Second)},
		{ID: 3, Name: "Makar", Position: pos},
		{ID: 4, Name: "Link", Position: pos, Place: network.Place{Stage: "sea"}, HasPlace: true, PlaceAt: now},
	}
	got := playerViews(in, "Link", now)
	if len(got) != 3 {
		t.Fatalf("got %d views, want 3 (self dropped): %+v", len(got), got)
	}
	if p := got[0]; !p.HasPlace || !p.Fresh || p.Place.Stage != "M_NewD2" || p.Place.Room != 3 || !p.Place.InPlay || p.PlaceAge != time.Second {
		t.Errorf("fresh place: %+v", p)
	}
	if p := got[1]; !p.HasPlace || p.Fresh || p.Place.Stage != "sea" || p.Place.Room != 11 || p.PlaceAge != 10*time.Second {
		t.Errorf("stale place: %+v", p)
	}
	if p := got[2]; p.HasPlace || p.Fresh || p.Place.Stage != "" {
		t.Errorf("no trailer should give no place: %+v", p)
	}
	for _, p := range playerViews(in, "Link", time.Time{}) {
		if p.HasPlace {
			t.Errorf("a zero clock should skip places: %+v", p)
		}
	}
}

// TestDedupeKeepsPlace: collapsing a human's two connections keeps the
// place whichever entry carries it, and prefers a fresh one.
func TestDedupeKeepsPlace(t *testing.T) {
	placed := session.Player{ID: 1, Name: "Tetra", Place: session.Place{Stage: "kindan", Room: 2, InPlay: true}, HasPlace: true, Fresh: true}
	positioned := session.Player{ID: 2, Name: "Tetra", X: 5, Y: 6, Z: 7}
	for _, in := range [][]session.Player{{placed, positioned}, {positioned, placed}} {
		out := dedupePlayersByName(in)
		if len(out) != 1 {
			t.Fatalf("got %d, want 1: %+v", len(out), out)
		}
		p := out[0]
		if p.X != 5 || p.Place.Stage != "kindan" || p.Place.Room != 2 || !p.HasPlace || !p.Fresh {
			t.Errorf("dedupe(%v) = %+v; want the position and the place", in, p)
		}
	}
	stale := session.Player{ID: 1, Name: "Tetra", X: 5, Y: 6, Z: 7, Place: session.Place{Stage: "sea", Room: 4}, HasPlace: true}
	fresh := session.Player{ID: 2, Name: "Tetra", Place: session.Place{Stage: "kindan", Room: 2}, HasPlace: true, Fresh: true}
	if p := dedupePlayersByName([]session.Player{stale, fresh})[0]; p.Place.Stage != "kindan" || !p.Fresh || p.X != 5 {
		t.Errorf("a fresh place should win over a stale one: %+v", p)
	}
}
