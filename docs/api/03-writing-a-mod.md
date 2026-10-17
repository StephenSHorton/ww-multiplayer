# Writing a mod: a walk through `mods/example`

**Status: v0 UNSTABLE.** Phase D step D3 of [00-design.md](00-design.md). Everything here can change in any 0.x minor version of wwapi.

`mods/example` is the smallest real mod. It is Go only, `local_only` and a dev mod. It does three things:

- logs the core events `StageChanged` and `ItemGained`;
- registers `mod.example.mark`, which remembers where Link stands;
- registers `mod.example.goto`, which warps Link back to the mark.

Its tests run it against a fake game, with no Dolphin. This page goes through it file by file. The reference pages are [02-mods.md](02-mods.md) (manifests, the resolver, the supervisor, `Context`) and [01-events.md](01-events.md) (the bus and the core events).

The example is not in the app's mod list (`compiledMods()` in `mods.go`), so `ww-multiplayer mods` does not show it and nothing runs it outside its tests. The app gets a runtime and a way to show dev mods in D4.

## 1. The folder

```
mods/example/
  mod.json          the manifest, embedded
  example.go        package example: the Mod
  example_test.go   the tests, over wwapi/fakegame
```

A mod is a Go package compiled into the app. There are no drop-in mods: Go's `plugin` package does not work on Windows. The folder name is the mod id. A folder starting with `_` is only for test fixtures (`mods/_fixtures`), which Go tools skip.

## 2. The manifest

```json
{
  "$schema": "../../wwapi/schema/mod.schema.json",
  "id": "example",
  "name": "Example",
  "version": "0.1.0",
  "api": "^0.1",
  "dependencies": {},
  "dev": true,
  "game_ids": ["GZLE01"],
  "multiplayer": "local_only",
  "provides": [],
  "needs": ["warp"],
  "writes": [],
  "c": null
}
```

(The file also has `description`, `optional_dependencies`, `load_order` and `enabled_by_default`; [02-mods.md](02-mods.md#modjson) lists every field.)

- `id` is the folder name and the mod's namespace: its events and commands are `mod.example.*`.
- `api` is the range of wwapi versions it works with. `wwapi.Version` is `0.1.0`, and 0.x minors may break, so `^0.1`.
- `dev: true` keeps it off unless dev mods are shown (`ResolveOptions.Dev`; in the app, D4's `--dev`).
- `multiplayer: local_only`: it never touches the network, and other players do not need it.
- `needs: ["warp"]`: the patched blob must have the C warp handler. With the blob's features known and `warp` missing, the mod is `NEEDS_REPATCH(warp)` and does not load.
- `writes: []`: the mod writes no game memory of its own. Its one write, the warp, is a core command's (section 5).

The manifest is embedded and parsed with `ManifestFrom`, which turns a bad `mod.json` into a `FAILED` mod instead of a crash:

```go
//go:embed mod.json
var manifestJSON []byte

func (*Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifestJSON) }
```

`go run ./tools/validate_manifests` checks every `mods/**/mod.json` against the schema; CI runs it.

## 3. The Mod

```go
type Mod struct {
    wwapi.NoRun

    mu   sync.Mutex
    mark *[3]float32 // nil until mod.example.mark
}

func New() *Mod { return &Mod{} }
```

`wwapi.Mod` has three methods: `Manifest`, `Init` and `Run`. The example has no long-running work, so it embeds `wwapi.NoRun`, whose `Run` returns at once. A mod that polls or keeps a connection does that in `Run` until its context ends, and returns `nil` then.

`Init` runs once, in load order, and must not block. It registers everything the mod needs through its `*wwapi.Context`:

```go
func (m *Mod) Init(c *wwapi.Context) error {
    rep := c.Log()
    events.On(c.Events(), func(e events.StageChanged) { rep.Log(log.Info, StageLine(e)) })
    events.On(c.Events(), func(e events.ItemGained) { rep.Log(log.Info, ItemLine(e)) })

    if err := c.Commands().Register(CmdMark, commands.Spec{Scope: commands.Local}, ...); err != nil {
        return err
    }
    return c.Commands().Register(CmdGoto, commands.Spec{Scope: commands.Local}, ...)
}
```

An error or a panic from `Init` makes the mod `FAILED`. Whatever it registered is undone: its subscriptions end and its commands are removed.

The `Context` is bound to this mod. What a mod registers through it is the mod's, so the supervisor can take it all back when it quarantines the mod. The methods the example uses:

| Method | Here |
|---|---|
| `Log()` | The runtime's reporter. Every line is prefixed `[example] `. |
| `Events()` | The mod's view of the event bus. The Game's core events arrive on it. |
| `Commands()` | The mod's view of the command registry. It registers `mod.example.*` only. |
| `Game()` | The game, or nil when the runtime has none. |

## 4. Events

```go
events.On(c.Events(), func(e events.StageChanged) { rep.Log(log.Info, StageLine(e)) })
```

`events.On` calls the function on a goroutine of its own, one event at a time, in emit order. The events come from the `wwapi.Game`'s watcher, which reads a snapshot at 10 Hz and compares it with the last one. They are polled edges, not in-game callbacks. A flag set and cleared between two polls is never seen, and the order of one tick's events is not defined ([01-events.md](01-events.md)).

- `StageChanged` comes once at the start with `First` set (where the game is), then on every change of stage, save table or file. A room change alone is not one.
- `ItemGained` is an inventory slot taking a new item. `Prev` is `0xFF` for an empty slot.

The example logs one line each:

```
[example] starting in (no stage)
[example] stage (no stage) -> sea room 44
[example] item 0x27 in slot 12
```

Subscribe through `c.Events()`, never through `c.Game().Events()`. The runtime forwards the Game's core events onto its own bus, where the mod's view is. A subscription made through the view belongs to the mod: it ends when the mod is quarantined, and a panic in the handler quarantines the mod instead of being logged as a core panic. A subscription on the Game's bus would outlive the mod.

A mod can also emit its own events, `mod.<id>.*` only: define a struct that embeds `events.Base` and has an `EventName` like `"mod.example.marked"`, then call `c.Events().Emit`.

## 5. Commands, and how a mod writes

```go
c.Commands().Register(CmdMark, commands.Spec{Scope: commands.Local}, func(context.Context, json.RawMessage) (commands.Result, error) {
    return m.markHere(c)
})
```

A command is a name, a `Spec` and a handler. The handler gets its arguments as JSON and returns a `Result` (`OK`, a short `Code`, `Err`). Anyone holding the registry runs it with `Execute(ctx, name, args)`; `args` is marshalled to JSON. Today that is tests and other mods. The app's console verbs come with D4.

**mark only reads.** `c.Game().Player().Position()` reads Link's position through the Game's memory, which is read-only to mods:

```go
p, err := g.Player().Position()
pos := [3]float32{p.PosX, p.PosY, p.PosZ}
```

**goto writes through a core command.** `Game().Mem()` cannot be turned back into a writable `mem.Mem`, so a mod never writes game memory directly. Writes go through commands, the single audited path (design §5.4). The Game registers `core.player.warp`, which runs `player.Warp`: it writes the warp coordinates and bumps `warp_seq` in the mailbox, then waits for the C warp handler to acknowledge. goto calls it:

```go
res, err := c.Commands().Execute(ctx, wwapi.CmdPlayerWarp, wwapi.WarpArgs{Pos: *mark})
```

`core.player.warp` declares what it writes in its `Spec`: `Writes: []write.Range{{Addr: mailbox+0xC8, Len: 0x14}}` (`warp_seq`, `warp_ack`, `warp_x/y/z`). `write.Range` is from `wwapi/write`. A command with `Writes` goes through the registry's write gate, which the Game sets to its contract check. When the game is not GZLE01, or the blob in RAM is not this build's, the command is refused with `commands.ErrWritesRefused` before its handler runs, and goto returns that error.

What `wwapi/write` gives a mod today is that vocabulary: `write.Range` for a `Spec`, and the manifest's `writes` for the address set the resolver checks for overlaps between mods. Its guarded write engine (`Allowlist`, `Breaker`, `FrameWriter`) is co-op's and has no mod-facing `Writer` yet. `Context.Allow` (design §6.2), which declares a mod's address set at run time, comes with write enforcement. A mod that needs to write something no core command covers needs a new core command first.

When the mod is quarantined, or the runtime stops, its two commands are removed from the registry, and `Execute` of them returns `commands.ErrUnknown`. The mod's own `c.Commands()` view is closed too: `Register` and `Execute` through it, `core.*` included, return `commands.ErrClosed`, so a goroutine the mod left running cannot warp Link. The core commands stay with the Game.

## 6. Testing with fakegame

`example_test.go` runs the real mod in a real runtime against `wwapi/fakegame`, a deterministic fake of the game's memory, with no Dolphin.

**A game whose contract allows writes.** The fake RAM gets the disc ID and this build's T2, as the patched ISO leaves them, then `wwapi.NewGame` runs the contract check:

```go
m := mem.NewFakeMem()
fg := fakegame.New(m)
m.Store(addr.MEM1Base, []byte(wwapi.GameID))
m.Store(inject.T2Address, inject.T2Bytes)
g, _ := wwapi.NewGame(m, wwapi.GameOptions{})
```

`NewGame` does not start the watcher. The test calls `g.Tick()` itself, one poll at a time, so nothing depends on timing.

**The runtime.** Dev mods shown, and the blob's features from the contract check, so the `needs` check runs:

```go
rt := wwapi.New([]wwapi.Mod{example.New()}, wwapi.Options{
    Game: g, Log: rec,
    Resolve: wwapi.ResolveOptions{Dev: true, Features: g.Features()},
})
rt.Init()
```

**Events.** The fake game does what the game does: `fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea})` changes stage, and `fg.GetItem(save.SlotBow, 0, save.ItemBow)` gives the bow. `fg.Frames(3)` moves the heartbeat, then `g.Tick()` emits the edges. The handlers run on their own goroutines, so the test polls the log until the lines are there.

**Commands and the warp.** `wwapi/player/playertest` stands in for the C side: `RunWarpHandler` runs the game's warp handler over the fake memory about every millisecond, moving Link to `warp_x/y/z` and copying `warp_seq` into `warp_ack`. The test puts Link somewhere, runs mark, moves him, runs goto, and checks:

- the writes `mem.FakeMem.Record` saw: the 12 coordinate bytes, then `warp_seq = 1`;
- that Link is back at the mark (`playertest.LinkPos`), after exactly one warp;
- that after `rt.Run` returns, `mod.example.mark` is gone and `core.player.warp` is still there.

Other tests cover the manifest (off without `Dev`, `NEEDS_REPATCH` without `warp`) and a runtime with no Game (both commands say `no_game`).

Run them with:

```
go test ./mods/...
go run ./tools/validate_manifests
```

[../TESTING.md](../TESTING.md) has the full check matrix and the live checks on two Dolphins.

## 7. Checklist for a new mod

1. `mods/<id>/mod.json`: id equal to the folder, `api: "^0.1"`, the `needs` and `multiplayer` scope you need, `dev: true` while it is experimental.
2. A `Mod` with `Manifest` (`ManifestFrom` over the embedded file), `Init` (register only, never block) and `Run` (or embed `wwapi.NoRun`).
3. Events through `c.Events()`, commands through `c.Commands()`, named `mod.<id>.*`.
4. Reads through `c.Game()` (nil-check it); writes only through commands.
5. Never import `internal/config`: settings come from `c.Config()`. `TestCLIReadsNoConfig` checks this for `mods/`.
6. Tests over `wwapi/fakegame` (and `player/playertest` or `avatar/avatartest` for the C side), `go run ./tools/validate_manifests`, then the check matrix.
7. Listing it in `compiledMods()` changes the `mods` output, so the `mods` CLI goldens change on purpose ([../TESTING.md](../TESTING.md#cli-and-help-goldens)).
