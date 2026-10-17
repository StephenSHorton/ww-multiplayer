# wwapi: mods, manifests and the supervisor

**Status: v0 UNSTABLE.** Phase B step B3 of [00-design.md](00-design.md). The package is new and no real mod uses it yet: the app's mod list is empty until the multiplayer and co-op mods exist (Phase D). Its API can change in any 0.x minor version.

Import paths: `github.com/StephenSHorton/ww-multiplayer/wwapi` (the runtime), `.../wwapi/semver` (versions and ranges), `.../wwapi/schema` (the manifest schema and its validator). Events are in [01-events.md](01-events.md).

## A mod

A mod is a Go package compiled into the app. It embeds a `mod.json` and implements `wwapi.Mod`:

```go
//go:embed mod.json
var manifest []byte

type Mod struct{ wwapi.NoRun } // NoRun: no long-running work

func (Mod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom(manifest) }

func (Mod) Init(c *wwapi.Context) error {
    events.On(c.Events(), func(e StageChanged) { c.Log().Log(report.Info, "now in "+e.To) })
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

`CLI`, `UI` and `NetChannel` (design §6.1) come with the packages they need.

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
| `game_ids` | | The discs it is for (`GZLE01`). Parsed now; the contract check (C14) will use it. |
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
4. `needs` against the blob's Features, when they are known (`NEEDS_REPATCH`). Nothing reads them from the blob yet, so this check is skipped until the contract check exists (C14, E1).
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
rt := wwapi.New(compiledMods(), wwapi.Options{Log: rep, Config: src, DataRoot: dir})
err := rt.Run(ctx) // Init in load order, then every Run
```

- `rt.Bus()` is the core view of the event bus. The runtime sets the bus's `OnPanic`.
- `Init` runs each loading mod's `Init` once, in load order. Every call, including the one inside `Run`, returns only after the first call has finished, so `Run` never starts a mod whose `Init` is still running on another goroutine.
- `Run` is a copy of `runPipelines`' semantics (main.go), changed for mods. `runPipelines` itself is unchanged.
  - Each mod's `Run` gets its own goroutine and its own context.
  - Returning `nil` or `context.Canceled` means the mod has finished. The other mods go on.
  - An error returned after the runtime stopped the mod (`ctx` ended, or another mod's `ErrFatal`) is the mod shutting down, like the error a closed socket returns. It is logged at Info and the mod stays `LOADED`. A panic at that point still quarantines.
  - A panic is recovered. A panic or any other error **quarantines only that mod**: its context is cancelled, its event subscriptions end, `wwapi.ModQuarantined` (`core.mod_quarantined`) is emitted on the core bus, and the error, plus the stack for a panic, is logged. A panic in one of the mod's `On` handlers does the same. A mod that depends on a quarantined mod keeps running.
  - An error wrapping `wwapi.ErrFatal` from the **net provider** ends the session, as `network.ErrFatal` does in `runPipelines`. Every mod stops, and `Run` returns that error unchanged without logging it, because the provider already reported it. From any other mod, `ErrFatal` is an ordinary error.
  - When `ctx` ends (or on `ErrFatal`), the mods stop in **reverse load order**: each mod's context is cancelled and its `Run` awaited before the next one. Then every mod's subscriptions end and the bus closes. A runtime runs once.
- `rt.Mods()` lists every mod with its current status, loading mods first in load order.

### Context

Each mod's `*wwapi.Context` is bound to that mod:

| Method | |
|---|---|
| `ID()`, `Manifest()` | The mod's id and parsed manifest. |
| `Log()` | The runtime's `log.Reporter`, with every line prefixed `[id] `. |
| `Events()` | The mod's view of the bus. It emits only `mod.<id>.*`, and its subscriptions end on quarantine. |
| `Config()` | The mod's settings (`Decode`, `Get(key)`, `Raw`) from `Options.Config`, a `ConfigSource` the faces fill. wwapi and mods never import `internal/config`, and `TestCLIReadsNoConfig` checks this for `wwapi/` and `mods/`. The CLI passes nil and uses manifest defaults. |
| `DataDir()` | `Options.DataRoot/<id>`, not created. |
| `Has(id)`, `Mod(id)` | Whether a mod is loaded, and its `PublicAPI`. |
| `Features()` | The blob's Features (unknown for now). |

`Game`, `Commands` and `Allow` (design §6.2) come with C14 and `wwapi/write`.

## `mods` command

```
ww-multiplayer mods            # one line per mod: id, version, status
ww-multiplayer mods --json     # the same as JSON (wwapi.ModInfo); [] today
```

`mods` resolves the compiled-in mods with manifest defaults, as the CLI would load them. It never reads the app's settings, never attaches to Dolphin, and never runs a mod.

## Fixtures and CI

`mods/_fixtures` holds test mods, one folder each: `_ok_base` (public API, a `Run` that waits), `_ok_dependent` (requires `_ok_base ^1.0` and reads its API), `_bad_manifest`, `_bad_range`, `_api_mismatch`, `_missing_dep`, `_cycle_a`/`_cycle_b`, `_init_error`, `_run_panic` and `_handler_panic`. Go tools skip folders whose names start with `_`, so neither the app nor `./...` builds the fixtures. wwapi's tests import them by path.

`go run ./tools/validate_manifests` checks every `mods/**/mod.json`: it must parse, its id must equal its folder name, and only fixtures (under `_fixtures`) have ids that start with `_`. A folder with an `invalid.txt` must hold a manifest that is refused with an error containing that text. CI runs it, and runs `go vet ./mods/_fixtures/`, in the Build workflow.
