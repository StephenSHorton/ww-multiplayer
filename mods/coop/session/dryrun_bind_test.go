package coopsession

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// dryRunMilestones are the hub's resume milestones (hub.resumeMilestones):
// a file with story has them, and the hub's bind check counts them for
// every bind, a --coop-dry-run one included (docs/08 item 111).
var dryRunMilestones = []uint16{0x2A08, 0x0F80, 0x0801, 0x0808, 0x2401}

// setEvents sets the event bits ids in cg's loaded file, as the game's
// onEventBit would.
func (cg *coGame) setEvents(ids ...uint16) {
	cg.t.Helper()
	for _, id := range ids {
		b := addr.EventByte(id)
		off := addr.OffEvent + b.Off
		cg.put(off, cg.get(off)|b.Mask)
	}
}

// TestCoopDryRunBindsStoryFile: the LIVE-6 block 4 refusal. With
// --coop-dry-run on both sides (M2 as it ran), the host founds a world
// from A's file, which has story (the resume milestones). A's own file
// binds: its preflight says same, and the hub agrees. Before the fix a
// dry-run replica sent its bind WORLD without the story bits (it runs
// without the session policy, so its policy shared none), and the hub's
// check, which counts story for every bind, judged the very file the
// world was founded from "far ahead of your file". B, whose file really
// lacks the milestones, is still refused far behind.
func TestCoopDryRunBindsStoryFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	ga.setEvents(dryRunMilestones...)
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})

	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
	}}
	h.start(Flags{on: true, world: coopWorldNew, yes: true, dryRun: true})
	defer h.stop()
	jcs := startCoopSession(&coLog{}, Flags{on: true, yes: true, dryRun: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}})
	a := newCoPlayer(t, ctx, h, h.cs, ga, "Link", &coLog{}, &coAudit{}, 1)
	b := newCoPlayer(t, ctx, h, jcs, gb, "Tetra", &coLog{}, &coAudit{}, 2)

	deadline := time.Now().Add(15 * time.Second)
	refused := func(name, why string) bool {
		h.rep.mu.Lock()
		defer h.rep.mu.Unlock()
		for _, l := range h.rep.lines {
			if strings.Contains(l, name+" (co-op") && strings.Contains(l, "may not bind: "+why) {
				return true
			}
		}
		return false
	}
	for a.s.r.Status().State != replica.StateArmed || !refused("Tetra", "") {
		if time.Now().After(deadline) {
			t.Fatalf("A %s, B %s\nhost:\n%s\nA:\n%s\nB:\n%s", a.s.r.Status().State, b.s.r.Status().State, h.rep, a.rep, b.rep)
		}
		a.step()
		b.step()
		time.Sleep(5 * time.Millisecond)
	}
	if refused("Link", "") {
		t.Errorf("A's own file, the world's founder, was refused:\n%s", h.rep)
	}
	if !refused("Tetra", "the world is far ahead of your file") {
		t.Errorf("B's file, behind the world, was not refused far behind:\n%s", h.rep)
	}
	if b.s.r.Status().State == replica.StateArmed {
		t.Error("B's file, behind the world, bound")
	}
}
