# wwapi: mods, manifests and the supervisor

**Status: v0 UNSTABLE.** Phase B step B3 of [00-design.md](00-design.md), with `Context.Game` and `Context.Commands` from D3. The one real mod so far is the example, `mods/example` (a dev mod); the app's mod list is still empty until the multiplayer and co-op mods exist and dev mods can be shown (Phase D). Its API can change in any 0.x minor version.

Import paths: `github.com/StephenSHorton/ww-multiplayer/wwapi` (the runtime), `.../wwapi/semver` (versions and ranges), `.../wwapi/schema` (the manifest schema and its validator). Events are in [01-events.md](01-events.md). For a walk through a whole mod, its commands and its tests, see [03-writing-a-mod.md](03-writing-a-mod.md).

## A mod

A mod is a Go package compiled into the app. It embeds a `mod.json` and implements `wwapi.Mod`:

```go
//go:embed mod.json
var manifest []byte

type Mod struct{ wwapi.NoRun } // NoRun: no long-running work

func (Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifest) }

func (Mod) Init(c *wwapi.Context) error {
    events.On(c.Events(), func(e events.StageChanged) { c.Log().Log(log.Info, "now in "+e.To.Stage) })
    return nil
}
```

| Method | Contract |
|---|---|
| `Manifest()` | The parsed `mod.json`. Use `ManifestFrom`: a bad manifest then makes the mod `FAILED` instead of crashing the app. |
| `Init(c)` | Register what the mod needs through `c`. It runs once, in load order, and must not block. An error or a panic makes the mod `FAILED`, and mods that require it fail too. |
| `Run(ctx)` | Long-running work until `ctx` ends; return `nil` then. Embed `wwapi.NoRun` when there is none. A panic or an error **quarantines** the mod. |

Optional interfaces, found by type assertion:

| Interface | Meaning |
|---|---|
| `PublicAPI` (`API() any`) | What other mods get from `Context.Mod(id)`. |
| `NetProvider` (`Engine() session.Engine`) | The network session provider. The manifest must say `"provides": ["net"]`, and a mod that provides `net` must implement it. |
| `CLI` (`Commands() []Command`) | CLI subcommands. A `wwapi.Command` has a name, aliases, `Hidden`, a help group label (`GroupPlay`, `GroupPatch`, `GroupLocal`, `GroupLower`, `GroupDev`), the help's usage and summary, and `Run(name, args)`. The app appends each loaded mod's commands to its own, in load order (co-op's `coop-tables`, `coop-tag`, `coop-world`, `save-diff`, `save-merge`). |
| `PostPatcher` (`PostPatch(w io.Writer, image string)`) | A step after `patch` (and the app's setup patch) on the disc Dolphin will run: it writes its line on `w` and never fails the patch. Loaded mods' steps run in load order (co-op builds its stage tables). |

`UI` and `NetChannel` (design §6.1) are not defined yet. What they build on partly exists: a mod registers commands (`mod.<id>.*`) through `Context.Commands()` today, and anything that can reach the registry runs them ([03-writing-a-mod.md](03-writing-a-mod.md)). The app integration (D4) added the `--mods`, `--no-mod` and `--dev` flags (for `tui`, `ui` and `mods`) and the window's Mods page, which runs a mod's commands (`Bridge.CallMod`); mod console verbs in the TUI and mod panels are not built yet, and `NetChannel` waits on the multiplayer mod (D1).

The app lists its mods statically in `compiledMods()` (`mods.go` at the repo root, moving to `cmd/ww-multiplayer/mods.go` in D7). There is no `init()` registration and no drop-in loading: Go's `plugin` package does not work on Windows.

## mod.json

`mods/<id>/mod.json`, checked against [`wwapi/schema/mod.schema.json`](../../wwapi/schema/mod.schema.json) by `wwapi.ParseManifest` at run time and by `go run ./tools/validate_manifests` in CI.

```json
{
  "id": "multiplayer",
  "name": "Multiplayer",
  "version": "1.0.0",
  "api": "^0.1",
  "dependencies": {},
  "optional_dependencies": {},
  "load_order": 0,
  "enabled_by_default": true,
  "dev": false,
  "game_ids": ["GZLE01"],
  "multiplayer": "client_required",
  "provides": ["net"],
  "needs": ["avatar", "face", "tunic", "input"],
  "writes": [],
  "c": null
}
```

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Equals the folder name. Lowercase letters, digits and `_`, starting with a letter; a leading `_` marks a test fixture. It is the mod's event namespace (`mod.<id>.*`), so it cannot be `core`. At most 64 bytes. |
| `name` | yes | What people see (1 to 64 characters). |
| `version` | yes | The mod's version, semver 2.0.0. |
| `api` | yes | The wwapi versions it works with, as a range. `wwapi.Version` is `0.1.0`, so use `^0.1`. |
| `dependencies` | | `id -> range`. Each must be installed, enabled, in range and loading, or this mod fails. |
| `optional_dependencies` | | `id -> range`. Used when present and loading; then the version must be in range and it loads first. |
| `load_order` | | Ties between mods that do not depend on each other: lower first, then by id. -1000 to 1000, default 0. |
| `enabled_by_default` | | Default `true`. |
| `dev` | | A development mod, off unless dev mods are shown. Default `false`. |
| `game_ids` | | The discs it is for (`GZLE01`). Parsed now. The contract check exists (`wwapi.CheckContract`, pre-E1 mode, C14); the runtime checks `game_ids` against it once a step attaches a `wwapi.Game` (D4 left that open). |
| `multiplayer` | | `client_required` (every player needs it; it will count in `mods_hash`), `host_only`, or `local_only` (the default). |
| `provides` | | Roles. Only `net` exists. At most one loading mod per role. |
| `needs` | | Features of the patched blob it needs. A missing one gives `NEEDS_REPATCH`. |
| `writes` | | **Placeholder** for the declared write set (decision 10): `[{"start":"0x80410000","end":"0x80410010","why":"..."}]`, `[start, end)` in MEM1. Ranges may not overlap within the manifest or with another loading mod's. Nothing enforces writes yet; `wwapi/write` comes later. |
| `c` | | Reserved for C modules. Must be `null`. |

Unknown fields are refused, and so are duplicate keys.

### Version ranges

`wwapi/semver` parses the npm syntax that manifests need:

| Range | Means |
|---|---|
| `*`, `x` | any release |
| `1.2.3`, `=1.2.3` | exactly that version |
| `1.2`, `1.2.x` | `>=1.2.0 <1.3.0` |
| `^1.2.3`, `^1.0` | `>=1.2.3 <2.0.0`, `>=1.0.0 <2.0.0` |
| `^0.1`, `^0.0.3` | `>=0.1.0 <0.2.0`, `>=0.0.3 <0.0.4` (0.x minors may break) |
| `~1.2.3`, `~1` | `>=1.2.3 <1.3.0`, `>=1.0.0 <2.0.0` |
| `>=1.0.0 <2.0.0` | both (space-separated) |
| `<1.0.0 \|\| >=2.0.0` | either |

Hyphen ranges are not supported. As in npm, a prerelease such as `1.3.0-rc.1` satisfies a range only when the range names a prerelease of `1.3.0` itself.

### The schema validator

`wwapi/schema` has a small JSON Schema validator for the draft 2020-12 keywords the schema uses (type, enum, const, not, local `$ref`, properties, required, additionalProperties, propertyNames, item and length limits, pattern, minimum, maximum), so the module needs no new dependency. It refuses a schema with any other keyword, so the schema cannot use a rule the validator would silently skip.

## Resolving

`wwapi.Resolve(mods, opts)` never fails. Every problem becomes one mod's status, and a mod whose dependency does not load fails too. The checks, in order:

1. The manifest parses, and no two mods share an id (all of them fail).
2. The enable filter: `opts.Enabled` (settings `mods.<id>.enabled`, CLI flags later), else `enabled_by_default`, and `dev` mods only with `opts.Dev`.
3. The `api` range contains the wwapi version.
4. `needs` against the blob's Features, when they are known (`NEEDS_REPATCH`). `wwapi.CheckContract` reads them (pre-E1 mode: the fixed `PreE1Features` until E1 puts a header in the blob), but the runtime does not pass them to `Resolve` until a step attaches a `wwapi.Game` (D4 left that open), so for now this check is skipped.
5. `NetProvider` and `"provides": ["net"]` agree.
6. Dependencies, then dependency cycles (`dependency cycle: a -> b -> a`; optional dependencies count).
7. At most one loading mod per `provides` role. When two mods claim `net`, both fail and the user turns one off.
8. Declared write sets do not overlap; the later mod in load order fails.

The load order puts every mod after the mods it depends on, and otherwise goes by `load_order`, then id.

## Statuses

| Status | Meaning |
|---|---|
| `LOADED` | Resolved, and initialised once a runtime has run `Init`. In `mods`, which never runs anything, it means "would load". |
| `DISABLED` | Turned off by settings, a flag, `enabled_by_default` or `dev`. |
| `FAILED(reason)` | Refused before it ran: bad manifest, api or dependency range, missing, disabled or failed dependency, cycle, a second provider, overlapping writes, or an `Init` error. |
| `QUARANTINED(reason)` | Its `Run` or one of its event handlers panicked or returned an error. |
| `NEEDS_REPATCH(missing)` | The patched ISO lacks a feature in `needs`. |

## The runtime and the supervisor

```go
rt := wwapi.New(compiledMods(), wwapi.Options{Log: rep, Config: src, DataRoot: dir, Game: g})
err := rt.Run(ctx) // Init in load order, then every Run
```

- `rt.Bus()` is the core view of the event bus. The runtime sets the bus's `OnPanic`.
- `Options.Game` is the `*wwapi.Game` the mods see, or nil when there is none (a pure server still runs its mods). A Game is made before the runtime, so its watcher emits on its own bus; the runtime forwards the Game's `core.*` events onto `rt.Bus()` from `Init` until `Run` ends. A mod therefore subscribes through `Context.Events()`, and its subscriptions stay its own (they end on quarantine, and a panic in one quarantines the mod). Forwarded events keep their type and payload; the runtime bus stamps its own `Seq`, `At`, `Source` (`SourceGame`) and `Origin` (`core`), so a `core.*` event emitted through the Game bus's `Net()` view reaches mods as `SourceGame`.
- `rt.Commands()` is the core view of the command registry the mods register in: `Game.Commands()`, or the runtime's own registry when there is no Game.
- `Init` runs each loading mod's `Init` once, in load order. Every call, including the one inside `Run`, returns only after the first call has finished, so `Run` never starts a mod whose `Init` is still running on another goroutine.
- `Run` is a copy of `engine.RunPipelines`' semantics (mods/multiplayer/engine/pipelines.go), changed for mods. `engine.RunPipelines` itself is unchanged.
  - Each mod's `Run` gets its own goroutine and its own context.
  - Returning `nil` or `context.Canceled` means the mod has finished. The other mods go on.
  - An error returned after the runtime stopped the mod (`ctx` ended, or another mod's `ErrFatal`) is the mod shutting down, like the error a closed socket returns. It is logged at Info and the mod stays `LOADED`. A panic at that point still quarantines.
  - A panic is recovered. A panic or any other error **quarantines only that mod**: its context is cancelled, its event subscriptions end, its commands are removed and its `Commands()` view runs nothing more (`commands.ErrClosed`), `wwapi.ModQuarantined` (`core.mod_quarantined`) is emitted on the core bus, and the error, plus the stack for a panic, is logged. A panic in one of the mod's `On` handlers does the same. A mod that depends on a quarantined mod keeps running.
  - An error wrapping `wwapi.ErrFatal` from the **net provider** ends the session, as `network.ErrFatal` does in `engine.RunPipelines`. Every mod stops, and `Run` returns that error unchanged without logging it, because the provider already reported it. From any other mod, `ErrFatal` is an ordinary error.
  - When `ctx` ends (or on `ErrFatal`), the mods stop in **reverse load order**: each mod's context is cancelled and its `Run` awaited before the next one. Then the Game's events stop being forwarded, every mod's subscriptions end and its commands are removed, and the bus closes. The Game, its bus and its core commands are the caller's and stay. A runtime runs once.
- `rt.Mods()` lists every mod with its current status, loading mods first in load order.

### Context

Each mod's `*wwapi.Context` is bound to that mod:

| Method | |
|---|---|
| `ID()`, `Manifest()` | The mod's id and parsed manifest. |
| `Log()` | The runtime's `log.Reporter`, with every line prefixed `[id] `. |
| `Game()` | `Options.Game`, or nil when the runtime has no game. Its `Mem()` is read-only: a mod writes through commands. |
| `Events()` | The mod's view of the bus. It emits only `mod.<id>.*`, sees the Game's `core.*` events, and its subscriptions end on quarantine. |
| `Commands()` | The mod's view of the command registry (`rt.Commands()`). It registers only `mod.<id>.*` names and runs any registered name, `core.*` included. A command whose `Spec.Writes` is not empty is refused while the Game's contract check refuses writes. The mod's commands are removed when its `Init` fails, on quarantine, and when the runtime stops; the view then refuses `Register` and `Execute` with `commands.ErrClosed`. |
| `Config()` | The mod's settings (`Decode`, `Get(key)`, `Raw`) from `Options.Config`, a `ConfigSource` the faces fill. wwapi and mods never import `internal/config`, and `TestCLIReadsNoConfig` checks this for `wwapi/` and `mods/`. The CLI passes nil and uses manifest defaults. |
| `DataDir()` | `Options.DataRoot/<id>`, not created. |
| `Has(id)`, `Mod(id)` | Whether a mod is loaded, and its `PublicAPI`. |
| `Features()` | The blob's Features: `Options.Resolve.Features`, unknown until a step attaches a `wwapi.Game` and the runtime passes `Game.Features()` in (D4 left that open). |

The app's faces (the window, `tui`, `ui --browser`) each make one runtime when they open (package main's `startApp`): the saved settings (`config.Mods`, through `gui.ModConfig`) under the mod flags are its `Config`, its bus feeds the session controller (`EventMod`), and the session engine is its loaded `NetProvider`'s. A toggle on the Mods page is saved and applies the next time the app starts. The app attaches no `Game` yet, so `Options.Game` is nil and `Resolve.Features` unknown there. `Allow` (design §6.2) comes with `wwapi/write` enforcement.

## `mods` command

```
ww-multiplayer mods            # one line per mod: id, version, status
ww-multiplayer mods --json     # the same as JSON (wwapi.ModInfo)
ww-multiplayer mods --dev --no-mod=coop   # what the app's mod flags would load
```

`mods` resolves the compiled-in mods with manifest defaults, as the CLI would load them. It never reads the app's settings, never attaches to Dolphin, and never runs a mod. It takes the app's mod flags to show their effect: `--mods=a,b` loads exactly those, `--no-mod=x` turns x off (both repeat and take comma lists), `--dev` adds the dev mods. The app takes the same flags after `tui` and `ui` (`ui --browser --dev`); they override the saved settings for that run.

## Fixtures and CI

`mods/_fixtures` holds test mods, one folder each: `_ok_base` (public API, a `Run` that waits), `_ok_dependent` (requires `_ok_base ^1.0` and reads its API), `_bad_manifest`, `_bad_range`, `_api_mismatch`, `_missing_dep`, `_cycle_a`/`_cycle_b`, `_init_error`, `_run_panic` and `_handler_panic`. Go tools skip folders whose names start with `_`, so neither the app nor `./...` builds the fixtures. wwapi's tests import them by path.

`mods/example` is an ordinary mod folder, so `./...` builds and tests it; it is not in `compiledMods()` but in `devMods()`, so `mods` lists it only with `--dev` (`mods --dev`; in the app, `ui --dev` or `tui --dev`).

`go run ./tools/validate_manifests` checks every `mods/**/mod.json`: it must parse, its id must equal its folder name, and only fixtures (under `_fixtures`) have ids that start with `_`. A folder with an `invalid.txt` must hold a manifest that is refused with an error containing that text. CI runs it, and runs `go vet ./mods/_fixtures/`, in the Build workflow.
