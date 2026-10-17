package replica

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
)

// gainsOf joins the GAINS in ms.
func gainsOf(ms []wire.Msg) world.Image {
	var img world.Image
	for _, g := range all[*wire.GainsUp](ms) {
		for _, x := range g.Gains {
			world.Apply(&img, []world.Gain{{Off: x.Off, Val: x.Val}})
		}
	}
	return img
}

// TestReplicaPace: a writing replica paces what it sends (§6.5, M4): a
// story bit set during an event waits for the game to be IDLE for
// story.BatchIdle, a reconnect's WORLD leaves the waiting bit out, and
// the bit then goes as a gain. A dry run is M2's, unpaced.
func TestReplicaPace(t *testing.T) {
	if dry := newRecRigNoWelcome(t, Config{}); dry.r.differ.Paced() {
		t.Fatal("a dry run's differ is paced")
	}
	rr := newRecRig(t, Config{Apply: true, Policy: proto.SessionPolicy(nil)}, proto.FeatureImage|proto.FeatureStory)
	if !rr.r.differ.Paced() {
		t.Fatal("a writing replica's differ is not paced")
	}
	off, m := syncedStory(t)
	rr.g.SetEvent(1, 0)
	rr.ticks(2)
	rr.set(off, m)
	var sent []wire.Msg
	for range gate.StableTicks + 2 {
		rr.tick()
		sent = append(sent, rr.hub.take()...)
	}
	if g := gainsOf(sent); g[off]&m != 0 {
		t.Fatalf("the story bit went during its event: %s", subs(sent))
	}
	rr.g.SetEvent(0, 0)
	rr.ticks(2)

	// A reconnect while the bit waits: its WORLD leaves it out.
	rr.r.Disconnect()
	rr.hub.take()
	rr.r.Connect(rr.hub)
	rr.ticks(2)
	say(t, rr.r, welcome(wire.StatusOK, uint32(proto.FeatureImage|proto.FeatureStory), false))
	rr.tick()
	sent = rr.hub.take()
	w, ok := find[*wire.World](sent)
	if !ok {
		t.Fatalf("no WORLD after the reconnect: %s", subs(sent))
	}
	if w.Image[off]&m != 0 {
		t.Fatal("the reconnect's WORLD carries the waiting story bit")
	}
	if g := gainsOf(sent); g[off]&m != 0 {
		t.Fatal("the story bit went before the game was IDLE for long")
	}

	// IDLE for story.BatchIdle: the bit goes, once.
	sent = nil
	for range int(2 * time.Second / TickEvery) {
		rr.tick()
		sent = append(sent, rr.hub.take()...)
	}
	if g := gainsOf(sent); g[off]&m != m {
		t.Fatalf("the story bit never went: %s\n%s", subs(sent), rr.log)
	}
	n := 0
	for _, g := range all[*wire.GainsUp](sent) {
		for _, x := range g.Gains {
			if int(x.Off) == off {
				n++
			}
		}
	}
	if n != 1 {
		t.Fatalf("the story bit went %d times", n)
	}
}
