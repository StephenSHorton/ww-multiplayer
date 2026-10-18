package example_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/example"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player/playertest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// lines records what the runtime logs, one "level msg" string a line.
type lines struct {
	mu sync.Mutex
	l  []string
}

func (r *lines) Log(level log.Level, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.l = append(r.l, fmt.Sprintf("%d %s", level, msg))
}

// mod returns the lines the example logged, without the prefix.
func (r *lines) mod() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.l {
		if rest, ok := strings.CutPrefix(s, fmt.Sprintf("%d [example] ", log.Info)); ok {
			out = append(out, rest)
		}
	}
	return out
}

// waitLines waits until the example has logged want, in order.
func (r *lines) waitLines(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Equal(r.mod(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("example logged\n %q\nwant\n %q", r.mod(), want)
}

// booted is a fake game's RAM as the patched ISO leaves it (the disc ID,
// this build's T2 and its WWMB header), so the contract check allows
// writes.
func booted(t *testing.T) (*mem.FakeMem, *fakegame.FakeGame, *wwapi.Game) {
	t.Helper()
	m := mem.NewFakeMem()
	fg := fakegame.New(m)
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	g, err := wwapi.NewGame(m, wwapi.GameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return m, fg, g
}

// start runs the example in a runtime over g, dev mods shown and the
// blob's features known, and stops the runtime when the test ends.
func start(t *testing.T, g *wwapi.Game, rep log.Reporter) *wwapi.Runtime {
	t.Helper()
	o := wwapi.Options{Game: g, Log: rep, Resolve: wwapi.ResolveOptions{Dev: true}}
	if g != nil {
		o.Resolve.Features = g.Features()
	}
	rt := wwapi.New([]wwapi.Mod{example.New()}, o)
	rt.Init()
	if s, _ := rt.Status("example"); s.State != wwapi.Loaded {
		t.Fatalf("example is %s", s)
	}
	return rt
}

func stop(t *testing.T, rt *wwapi.Runtime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestManifest: the embedded mod.json parses to what the docs say.
func TestManifest(t *testing.T) {
	m := example.New().Manifest()
	if err := m.Err(); err != nil {
		t.Fatal(err)
	}
	if m.ID != "example" || !m.Dev || m.Multiplayer != wwapi.LocalOnly ||
		!slices.Equal(m.Needs, []string{"warp"}) || len(m.Dependencies) != 0 || len(m.Writes) != 0 {
		t.Errorf("manifest %+v", m)
	}
	// A dev mod: off unless dev mods are shown.
	if infos := wwapi.Resolve([]wwapi.Mod{example.New()}, wwapi.ResolveOptions{}).Infos(); infos[0].StatusString() != "DISABLED" {
		t.Errorf("without Dev: %s", infos[0].StatusString())
	}
	// It needs the warp feature.
	infos := wwapi.Resolve([]wwapi.Mod{example.New()}, wwapi.ResolveOptions{Dev: true, Features: wwapi.KnownFeatures("face")}).Infos()
	if got := infos[0].StatusString(); !strings.HasPrefix(got, "NEEDS_REPATCH") || !strings.Contains(got, "warp") {
		t.Errorf("without warp: %s", got)
	}
}

// TestEventsLogged: a stage change and an item gain, seen by the Game's
// watcher, reach the example's handlers and are logged.
func TestEventsLogged(t *testing.T) {
	m, fg, g := booted(t)
	rec := &lines{}
	rt := start(t, g, rec)
	defer stop(t, rt)

	tick := func() {
		t.Helper()
		fg.Frames(3)
		if err := g.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	tick() // the first snapshot: the game has no stage yet
	rec.waitLines(t, "starting in (no stage)")

	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	tick()
	rec.waitLines(t, "starting in (no stage)", "stage (no stage) -> sea room 44")

	before, err := mem.ReadU8(m, addr.SaveImage+uint32(addr.OffItems+save.SlotBow))
	if err != nil || before != 0xFF {
		t.Fatalf("bow slot 0x%02X, %v: want empty", before, err)
	}
	if err := fg.GetItem(save.SlotBow, 0, save.ItemBow); err != nil {
		t.Fatal(err)
	}
	tick()
	rec.waitLines(t, "starting in (no stage)", "stage (no stage) -> sea room 44",
		fmt.Sprintf("item 0x%02X in slot %d", save.ItemBow, save.SlotBow))
}

// TestMarkAndGoto: mark remembers Link's position; goto asks the C warp
// handler (playertest's fake) to put him back there.
func TestMarkAndGoto(t *testing.T) {
	m, fg, g := booted(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	rec := &lines{}
	rt := start(t, g, rec)
	ctx := context.Background()
	cmds := rt.Commands()

	if got := cmds.Names(); !slices.Contains(got, example.CmdMark) || !slices.Contains(got, example.CmdGoto) {
		t.Fatalf("commands %v", got)
	}
	if res, err := cmds.Execute(ctx, example.CmdGoto, nil); !errors.Is(err, example.ErrNoMark) || res.Code != "no_mark" {
		t.Errorf("goto before mark: %+v, %v", res, err)
	}

	here := [3]float32{-200050, 12.5, 300020}
	linkPos := fakegame.PlayerActor + addr.ActorOffCurPos
	if err := m.Store(linkPos, playertest.PosBytes(here)); err != nil {
		t.Fatal(err)
	}
	if res, err := cmds.Execute(ctx, example.CmdMark, nil); err != nil || !res.OK {
		t.Fatalf("mark: %+v, %v", res, err)
	}
	if err := m.Store(linkPos, playertest.PosBytes([3]float32{1, 2, 3})); err != nil { // Link walks off
		t.Fatal(err)
	}

	warps := playertest.RunWarpHandler(t, m)
	m.Record(true)
	res, err := cmds.Execute(ctx, example.CmdGoto, nil)
	m.Record(false)
	if err != nil || !res.OK {
		t.Fatalf("goto: %+v, %v", res, err)
	}
	// The warp request: the coordinates, then warp_seq = 1.
	ws := m.Writes()
	if len(ws) != 2 ||
		ws[0].Addr != mailbox.Mailbox+mailbox.OffWarpX || !bytes.Equal(ws[0].Data, playertest.PosBytes(here)) ||
		ws[1].Addr != mailbox.Mailbox+mailbox.OffWarpSeq || !bytes.Equal(ws[1].Data, []byte{0, 0, 0, 1}) {
		t.Errorf("writes %+v, want the mark's 12 bytes then warp_seq = 1", ws)
	}
	if got := playertest.LinkPos(t, m, fakegame.PlayerActor); got != here {
		t.Errorf("Link at %v, want %v", got, here)
	}
	if n := warps(); n != 1 {
		t.Errorf("%d warps, want 1", n)
	}
	rec.waitLines(t, "marked (-200050.0, 12.5, 300020.0)", "warped to (-200050.0, 12.5, 300020.0)")

	// Once the runtime stops, the mod's commands are gone; the core ones
	// stay with the Game.
	stop(t, rt)
	if _, err := cmds.Execute(ctx, example.CmdMark, nil); !errors.Is(err, commands.ErrUnknown) {
		t.Errorf("mark after the runtime stopped: %v", err)
	}
	if !slices.Contains(g.Commands().Names(), wwapi.CmdPlayerWarp) {
		t.Errorf("core commands %v", g.Commands().Names())
	}
}

// TestNoGame: without a Game the mod still loads, and its commands say
// there is no game.
func TestNoGame(t *testing.T) {
	rt := start(t, nil, nil)
	defer stop(t, rt)
	for _, name := range []string{example.CmdMark, example.CmdGoto} {
		if res, err := rt.Commands().Execute(context.Background(), name, nil); !errors.Is(err, example.ErrNoGame) || res.Code != "no_game" {
			t.Errorf("%s: %+v, %v", name, res, err)
		}
	}
}
