// Package bindtest is a FakeGame with a bound file, for the tests of the
// co-op session and of the co-op commands.
package bindtest

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// World is the world Game binds its file to.
var World = bind.WorldID{0xC0, 0x0B, 1, 2, 3, 4, 5, 6}

// Sea is the stage Game plays on.
var Sea = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}

// Game is a FakeGame playing a saved and loaded file "Link" in slot 0,
// bound to World when bound is set.
func Game(t *testing.T, bound bool) (*fakegame.FakeGame, *mem.FakeMem, func()) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.NewGame(0, "Link"))
	must(g.LoadStage(Sea))
	must(g.Save())
	g.EnterFileSelect()
	must(g.LoadFile(0))
	must(g.LoadStage(Sea))
	tick := func() { g.Frames(3) }
	if bound {
		var tr gate.Tracker
		var s *state.Snapshot
		var st gate.Status
		for range gate.StableTicks + 1 {
			tick()
			var err error
			s, err = state.Read(m, state.Options{})
			must(err)
			st = tr.Observe(s)
		}
		p, err := bind.PlanBind(s, st, bind.BindRequest{World: World, WorldSeq: 3})
		must(err)
		p.Confirm()
		_, err = bind.Bind(m, p, s, st)
		must(err)
	}
	return g, m, tick
}
