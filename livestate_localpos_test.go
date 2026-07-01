package main

import "testing"

// TestLiveState_LocalPos_RoundTrip verifies the #22 minimap bridge:
// setLocalPos publishes coords, localPos reads them back with ok=true, and a
// fresh liveState reports ok=false before any tick has landed.
func TestLiveState_LocalPos_RoundTrip(t *testing.T) {
	ls := &liveState{}

	if _, _, _, ok := ls.localPos(); ok {
		t.Fatal("localPos() on a fresh liveState should report ok=false")
	}

	ls.setLocalPos(1.5, 2.5, 3.5)
	x, y, z, ok := ls.localPos()
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
	ls := &liveState{}
	ls.setLocalPos(10, 20, 30)

	ls.begin("Host", "Link", nil)

	if _, _, _, ok := ls.localPos(); ok {
		t.Error("localPos() should report ok=false immediately after begin() resets the session")
	}
}

// TestLiveStateLocalPosSetter_NilSafe verifies the adapter mirrors
// liveStateClientSetter: nil ls -> nil callback (so CLI paths that pass
// ls=nil into runMultiplayerGoroutines get a genuinely nil onPos, not a
// callback that panics on a nil receiver).
func TestLiveStateLocalPosSetter_NilSafe(t *testing.T) {
	if f := liveStateLocalPosSetter(nil); f != nil {
		t.Error("liveStateLocalPosSetter(nil) should return a nil func")
	}
}

// TestLiveStateLocalPosSetter_WiresIntoLiveState verifies the non-nil case
// actually publishes into the given liveState -- the plumbing
// runBroadcastPoseCtx's onPos callback depends on.
func TestLiveStateLocalPosSetter_WiresIntoLiveState(t *testing.T) {
	ls := &liveState{}
	setter := liveStateLocalPosSetter(ls)
	if setter == nil {
		t.Fatal("liveStateLocalPosSetter(non-nil) should return a non-nil func")
	}

	setter(7, 8, 9)

	x, y, z, ok := ls.localPos()
	if !ok || x != 7 || y != 8 || z != 9 {
		t.Errorf("localPos() after setter callback = (%v,%v,%v,ok=%v), want (7,8,9,true)", x, y, z, ok)
	}
}
