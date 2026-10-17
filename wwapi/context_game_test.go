package wwapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	fixtures "github.com/StephenSHorton/ww-multiplayer/mods/_fixtures"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player/playertest"
)

func okCmd(context.Context, json.RawMessage) (commands.Result, error) {
	return commands.Result{OK: true}, nil
}

// TestContextGameAndCommands: Context.Game is Options.Game (nil without
// one), and Context.Commands registers mod.<id>.* in the Game's registry,
// or in the runtime's own without a Game, and runs core commands too.
func TestContextGameAndCommands(t *testing.T) {
	m, _ := bootedRAM(t)
	g := newGame(t, m)
	var seen *wwapi.Game
	var regErr, coreErr error
	a := mk(t, "a", "1.0.0", "")
	a.init = func(c *wwapi.Context) error {
		seen = c.Game()
		regErr = c.Commands().Register("core.a.cmd", commands.Spec{}, okCmd)
		_, coreErr = c.Commands().Execute(context.Background(), wwapi.CmdActorKill, nil)
		return c.Commands().Register("mod.a.cmd", commands.Spec{}, okCmd)
	}
	rt := wwapi.New([]wwapi.Mod{a}, wwapi.Options{Game: g})
	rt.Init()
	if seen != g || rt.Commands() != g.Commands() {
		t.Errorf("Game %p, Commands %p: want the Game's", seen, rt.Commands())
	}
	if !errors.Is(regErr, commands.ErrNamespace) {
		t.Errorf("a registering core.*: %v", regErr)
	}
	if !errors.Is(coreErr, commands.ErrUnsupported) {
		t.Errorf("a running a core command: %v", coreErr)
	}
	if res, err := g.Commands().Execute(context.Background(), "mod.a.cmd", nil); err != nil || !res.OK {
		t.Errorf("mod.a.cmd through the Game: %+v, %v", res, err)
	}

	var none *wwapi.Game = g
	b := mk(t, "b", "1.0.0", "")
	b.init = func(c *wwapi.Context) error {
		none = c.Game()
		return c.Commands().Register("mod.b.cmd", commands.Spec{}, okCmd)
	}
	rt2 := wwapi.New([]wwapi.Mod{b}, wwapi.Options{})
	rt2.Init()
	if none != nil {
		t.Error("Context.Game without a Game is not nil")
	}
	if got := rt2.Commands().Names(); !slices.Equal(got, []string{"mod.b.cmd"}) {
		t.Errorf("the runtime's own registry: %v", got)
	}
}

// TestSupervisorQuarantineRemovesCommands: a quarantined mod loses its
// commands and can run none through its view, a mod whose Init fails keeps none, and the other mods' and
// the core commands stay until the runtime stops.
func TestSupervisorQuarantineRemovesCommands(t *testing.T) {
	m, _ := bootedRAM(t)
	g := newGame(t, m)
	var badView *commands.Registry
	bad := mk(t, "bad", "1.0.0", "")
	bad.init = func(c *wwapi.Context) error {
		badView = c.Commands()
		if err := c.Commands().Register("mod.bad.cmd", commands.Spec{}, okCmd); err != nil {
			return err
		}
		events.On(c.Events(), func(fixtures.Ping) { panic("handler") })
		return nil
	}
	good := mk(t, "good", "1.0.0", "")
	good.init = func(c *wwapi.Context) error {
		return c.Commands().Register("mod.good.cmd", commands.Spec{}, okCmd)
	}
	failing := mk(t, "failing", "1.0.0", "")
	failing.init = func(c *wwapi.Context) error {
		if err := c.Commands().Register("mod.failing.cmd", commands.Spec{}, okCmd); err != nil {
			return err
		}
		return errors.New("no")
	}
	rt := wwapi.New([]wwapi.Mod{bad, good, failing}, wwapi.Options{Game: g})
	rt.Init()
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(rt, ctx)
	if got := state(rt, "failing"); got != "FAILED(Init: no)" {
		t.Errorf("failing = %s", got)
	}
	want := []string{wwapi.CmdActorKill, wwapi.CmdActorSpawn, wwapi.CmdPlayerWarp, "mod.bad.cmd", "mod.good.cmd"}
	if got := g.Commands().Names(); !slices.Equal(got, want) {
		t.Errorf("before: %v, want %v", got, want)
	}

	if err := rt.Bus().Emit(fixtures.Ping{N: 1}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bad to be quarantined", func() bool { return state(rt, "bad") != "LOADED" })
	want = []string{wwapi.CmdActorKill, wwapi.CmdActorSpawn, wwapi.CmdPlayerWarp, "mod.good.cmd"}
	if got := g.Commands().Names(); !slices.Equal(got, want) {
		t.Errorf("after the quarantine: %v, want %v", got, want)
	}
	if _, err := g.Commands().Execute(context.Background(), "mod.bad.cmd", nil); !errors.Is(err, commands.ErrUnknown) {
		t.Errorf("a quarantined mod's command ran: %v", err)
	}
	if err := badView.Register("mod.bad.again", commands.Spec{}, okCmd); !errors.Is(err, commands.ErrClosed) {
		t.Errorf("a quarantined mod registered: %v", err)
	}
	if _, err := badView.Execute(context.Background(), wwapi.CmdActorKill, nil); !errors.Is(err, commands.ErrClosed) {
		t.Errorf("a quarantined mod executed a command: %v", err)
	}

	cancel()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	want = []string{wwapi.CmdActorKill, wwapi.CmdActorSpawn, wwapi.CmdPlayerWarp}
	if got := g.Commands().Names(); !slices.Equal(got, want) {
		t.Errorf("after Run: %v, want %v", got, want)
	}
}

// TestRuntimeForwardsGameEvents: the Game's watcher emits on its own bus,
// and a mod subscribed through its Context gets those core events, until
// Run ends.
func TestRuntimeForwardsGameEvents(t *testing.T) {
	m, fg := bootedRAM(t)
	g := newGame(t, m)
	got := make(chan events.StageChanged, 8)
	a := mk(t, "a", "1.0.0", "")
	a.init = func(c *wwapi.Context) error {
		events.On(c.Events(), func(e events.StageChanged) { got <- e })
		return nil
	}
	rt := wwapi.New([]wwapi.Mod{a}, wwapi.Options{Game: g})
	rt.Init()
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	fg.Frames(3)
	if err := g.Tick(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		meta := e.EventMeta()
		if !e.First || e.To.Stage != "sea" || meta.Source != events.SourceGame || meta.Origin != "core" {
			t.Errorf("forwarded %+v (meta %+v)", e, meta)
		}
	case <-time.After(waitFor):
		t.Fatal("the mod did not get the Game's StageChanged")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// The Game's bus is still open; the runtime's is closed.
	if err := g.Events().Emit(events.StageChanged{To: events.Place{Stage: "x"}}); err != nil {
		t.Errorf("the Game's bus: %v", err)
	}
	if err := rt.Bus().Emit(fixtures.Ping{}); !errors.Is(err, events.ErrClosed) {
		t.Errorf("the runtime's bus after Run: %v", err)
	}
}

// TestPlayerWarpCommand: core.player.warp is player.Warp over the Game's
// memory, behind the contract's write gate.
func TestPlayerWarpCommand(t *testing.T) {
	m, fg := bootedRAM(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	g := newGame(t, m)
	spec, ok := g.Commands().Lookup(wwapi.CmdPlayerWarp)
	wantRange := mailbox.Mailbox + mailbox.OffWarpSeq
	if !ok || spec.Scope != commands.Local || len(spec.Writes) != 1 || spec.Writes[0].Addr != wantRange || spec.Writes[0].Len != 0x14 {
		t.Errorf("spec %+v", spec)
	}
	warps := playertest.RunWarpHandler(t, m)
	p := [3]float32{10, 20, 30}
	if res, err := g.Commands().Execute(context.Background(), wwapi.CmdPlayerWarp, wwapi.WarpArgs{Pos: p}); err != nil || !res.OK {
		t.Fatalf("warp: %+v, %v", res, err)
	}
	if got := playertest.LinkPos(t, m, fakegame.PlayerActor); got != p || warps() != 1 {
		t.Errorf("Link at %v after %d warps", got, warps())
	}
	for _, args := range []any{nil, json.RawMessage(`{"pos":"x"}`), wwapi.WarpArgs{TimeoutMS: -1}} {
		if res, err := g.Commands().Execute(context.Background(), wwapi.CmdPlayerWarp, args); err == nil || res.Code != "bad_args" {
			t.Errorf("args %v: %+v, %v", args, res, err)
		}
	}

	// No Link: the request is withdrawn after the timeout.
	if err := mem.WriteU32(m, addr.PlayerPtr, 0); err != nil {
		t.Fatal(err)
	}
	res, err := g.Commands().Execute(context.Background(), wwapi.CmdPlayerWarp, wwapi.WarpArgs{Pos: p, TimeoutMS: 20})
	if err == nil || res.Code != "warp_failed" {
		t.Errorf("warp with no Link: %+v, %v", res, err)
	}
}
