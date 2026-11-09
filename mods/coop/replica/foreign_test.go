package replica

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestForeignTagHook: a joiner whose file is tied to another world
// (docs/coop-just-works.md §5) says hello only after OnForeignTag cleared
// the tag, and then as an unbound file, so the hub never has to refuse it.
func TestForeignTagHook(t *testing.T) {
	t.Run("cleared before the hello", func(t *testing.T) {
		var rr *replicaRig
		var asked []bind.WorldID
		rr = newReplicaRig(t, Config{World: testWorld, OnForeignTag: func(w bind.WorldID) {
			asked = append(asked, w)
			if err := rr.m.Store(addr.Reserve, make([]byte, bind.TagSize)); err != nil {
				t.Error(err)
			}
		}})
		rr.tag(rr.tagFor(someWorld, testNonce))
		rr.r.Connect(rr.hub)
		sent := rr.settle()
		if len(asked) != 1 || asked[0] != someWorld {
			t.Fatalf("OnForeignTag calls %v, want one for %s", asked, someWorld)
		}
		hellos := all[*wire.CoHello](sent)
		if len(hellos) != 1 || hellos[0].FileState != wire.FileUnbound {
			t.Fatalf("hellos %+v, want one unbound hello and no refused bound one", hellos)
		}
	})
	t.Run("the tag of the session's own world is left alone", func(t *testing.T) {
		called := false
		rr := newReplicaRig(t, Config{World: testWorld, OnForeignTag: func(bind.WorldID) { called = true }})
		rr.tag(rr.tagFor(testWorld, testNonce))
		rr.r.Connect(rr.hub)
		sent := rr.settle()
		if called {
			t.Fatal("OnForeignTag was called for the session's own world")
		}
		if hs := all[*wire.CoHello](sent); len(hs) != 1 || hs[0].FileState != wire.FileBound {
			t.Fatalf("hellos %+v", hs)
		}
	})
	t.Run("a hook that never clears gives way to the hello", func(t *testing.T) {
		calls := 0
		rr := newReplicaRig(t, Config{World: testWorld, OnForeignTag: func(bind.WorldID) { calls++ }})
		rr.tag(rr.tagFor(someWorld, testNonce))
		clock := rr.r.now
		step := 0
		rr.r.now = func() time.Time { step++; return clock().Add(time.Duration(step) * ForeignRetry) }
		rr.r.Connect(rr.hub)
		var sent []wire.Msg
		for range 3*ForeignTries + 20 {
			rr.tick()
			sent = append(sent, rr.hub.take()...)
		}
		if calls != ForeignTries {
			t.Fatalf("%d calls, want %d", calls, ForeignTries)
		}
		if hs := all[*wire.CoHello](sent); len(hs) != 1 || hs[0].FileState != wire.FileForeign {
			t.Fatalf("hellos %+v, want the foreign one the host refuses", hs)
		}
	})
	t.Run("no hook, no hold", func(t *testing.T) {
		rr := newReplicaRig(t, Config{World: testWorld})
		rr.tag(rr.tagFor(someWorld, testNonce))
		rr.r.Connect(rr.hub)
		if hs := all[*wire.CoHello](rr.settle()); len(hs) != 1 || hs[0].FileState != wire.FileForeign {
			t.Fatalf("hellos %+v", hs)
		}
	})
}
