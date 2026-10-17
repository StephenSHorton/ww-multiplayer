// Package example is the example mod: a small Go-only mod that shows the
// parts of wwapi a mod uses. docs/api/03-writing-a-mod.md walks through
// it.
//
// It logs two core events, StageChanged and ItemGained, and registers two
// commands:
//
//   - mod.example.mark remembers where Link stands;
//   - mod.example.goto warps Link back there through core.player.warp
//     (player.Warp, carried out by the C warp handler).
//
// It is a dev mod ("dev": true in mod.json), local_only, and needs the
// blob's "warp" feature. The app does not list it yet: compiledMods() in
// mods.go stays empty until the dev mods can be shown (D4).
//
// # v0 UNSTABLE
//
// Like wwapi v0, this mod may change in any 0.x minor version.
package example

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

//go:embed mod.json
var manifestJSON []byte

// The commands the mod registers.
const (
	CmdMark = "mod.example.mark"
	CmdGoto = "mod.example.goto"
)

var (
	// ErrNoGame is returned by both commands when the runtime has no Game.
	ErrNoGame = errors.New("example: no game attached")
	// ErrNoMark is returned by goto before the first mark.
	ErrNoMark = errors.New("example: no mark yet: run mod.example.mark first")
)

// Mod is the example mod. It has no long-running work, so it embeds
// wwapi.NoRun.
type Mod struct {
	wwapi.NoRun

	mu   sync.Mutex
	mark *[3]float32 // nil until mod.example.mark
}

// New returns the example mod.
func New() *Mod { return &Mod{} }

// Manifest is the embedded mod.json. ManifestFrom turns a bad one into a
// FAILED mod instead of a crash.
func (*Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifestJSON) }

// Init subscribes to the core events and registers the commands. It must
// not block: the handlers run later, on goroutines of the bus and of
// whoever executes the commands.
func (m *Mod) Init(c *wwapi.Context) error {
	rep := c.Log()
	events.On(c.Events(), func(e events.StageChanged) { rep.Log(log.Info, StageLine(e)) })
	events.On(c.Events(), func(e events.ItemGained) { rep.Log(log.Info, ItemLine(e)) })

	if err := c.Commands().Register(CmdMark, commands.Spec{Scope: commands.Local}, func(context.Context, json.RawMessage) (commands.Result, error) {
		return m.markHere(c)
	}); err != nil {
		return err
	}
	return c.Commands().Register(CmdGoto, commands.Spec{Scope: commands.Local}, func(ctx context.Context, _ json.RawMessage) (commands.Result, error) {
		return m.goBack(ctx, c)
	})
}

// markHere is mod.example.mark: it reads Link's position (a read: the
// Game's memory is read-only to mods) and remembers it.
func (m *Mod) markHere(c *wwapi.Context) (commands.Result, error) {
	g := c.Game()
	if g == nil {
		return fail("no_game", ErrNoGame)
	}
	p, err := g.Player().Position()
	if err != nil {
		return fail("no_link", fmt.Errorf("example: mark: %w", err))
	}
	pos := [3]float32{p.PosX, p.PosY, p.PosZ}
	m.mu.Lock()
	m.mark = &pos
	m.mu.Unlock()
	c.Log().Log(log.Info, "marked "+posString(pos))
	return commands.Result{OK: true}, nil
}

// goBack is mod.example.goto: it writes nothing itself, but runs the
// core command core.player.warp, which is checked against the game's
// contract (the write gate) before player.Warp writes the warp mailbox.
func (m *Mod) goBack(ctx context.Context, c *wwapi.Context) (commands.Result, error) {
	if c.Game() == nil {
		return fail("no_game", ErrNoGame)
	}
	m.mu.Lock()
	mark := m.mark
	m.mu.Unlock()
	if mark == nil {
		return fail("no_mark", ErrNoMark)
	}
	res, err := c.Commands().Execute(ctx, wwapi.CmdPlayerWarp, wwapi.WarpArgs{Pos: *mark})
	if err != nil {
		return res, fmt.Errorf("example: goto: %w", err)
	}
	c.Log().Log(log.Info, "warped to "+posString(*mark))
	return res, nil
}

func fail(code string, err error) (commands.Result, error) {
	return commands.Result{Code: code, Err: err}, err
}

// StageLine is the log line for a StageChanged.
func StageLine(e events.StageChanged) string {
	if e.First {
		return fmt.Sprintf("starting in %s", placeString(e.To))
	}
	return fmt.Sprintf("stage %s -> %s", placeString(e.From), placeString(e.To))
}

// ItemLine is the log line for an ItemGained.
func ItemLine(e events.ItemGained) string {
	if e.Prev == 0xFF {
		return fmt.Sprintf("item 0x%02X in slot %d", e.Item, e.Slot)
	}
	return fmt.Sprintf("item 0x%02X in slot %d (was 0x%02X)", e.Item, e.Slot, e.Prev)
}

func placeString(p events.Place) string {
	if p.Stage == "" {
		return "(no stage)"
	}
	return fmt.Sprintf("%s room %d", p.Stage, p.Room)
}

func posString(p [3]float32) string {
	return fmt.Sprintf("(%.1f, %.1f, %.1f)", p[0], p[1], p[2])
}
