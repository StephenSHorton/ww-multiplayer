package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

// TestReplicaWorldBeforeWelcome: a joiner (no Config.World) learns the
// session's world from CO_WELCOME, which the hub sends before the WORLD
// (hub.onUnboundHello). A WORLD a reordering transport hands over first is
// kept for the welcome, which takes it if it names the welcome's world:
// the join preflight then runs as it does in order.
func TestReplicaWorldBeforeWelcome(t *testing.T) {
	other := worldOf(1, world.Image{})
	other.WorldID = wire.ID(bind.WorldID{0xEE, 1})
	unbound := welcome(wire.StatusUnbound, uint32(proto.FeatureImage), false)
	refused := &wire.CoWelcome{Status: wire.StatusRefused, Reason: wire.NoticeRejected, WorldID: wire.ID(testWorld)}
	cases := []struct {
		name      string
		msgs      []wire.Msg
		preflight bool
	}{
		{"in order", []wire.Msg{unbound, worldOf(1, world.Image{})}, true},
		{"the WORLD first", []wire.Msg{worldOf(1, world.Image{}), unbound}, true},
		{"the WORLD first, twice", []wire.Msg{worldOf(1, world.Image{}), worldOf(1, world.Image{}), unbound}, true},
		{"another world's WORLD first", []wire.Msg{other, unbound}, false},
		{"the WORLD before a refusal", []wire.Msg{worldOf(1, world.Image{}), refused}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := newReplicaRig(t, Config{})
			rr.r.Connect(rr.hub)
			rr.settle()
			say(t, rr.r, c.msgs...)
			rr.tick()
			if got := len(rr.pfs) == 1; got != c.preflight {
				t.Fatalf("%d preflights, want one: %v\n%s", len(rr.pfs), c.preflight, rr.log)
			}
			if c.preflight && rr.pfs[0].Verdict != PreflightSame {
				t.Fatalf("preflight %+v", rr.pfs[0])
			}
		})
	}
}
