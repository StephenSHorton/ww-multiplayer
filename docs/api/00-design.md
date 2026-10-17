# Wind Waker modding API: design (decided)

**Status:** approved plan. The owner approved every recommended option in the full design review. This page keeps what was decided and drops the alternatives that were turned down. No code has changed yet; this page is Phase A step A0.

**Baseline.** The design was checked against `origin/main` at 6afb07c. This page was written on top of 26f9bec, after #74 (shared tunic colors) and #75 (first-run setup) merged.

**Markers.**
- **(verified)**: checked in the code.
- **(inferred)**: a reasonable reading that nobody has measured or tested yet.
- **(unmeasured)**: a byte or size estimate that needs a build to confirm.

---

## 1. What we are building

Wind Waker Multiplayer already holds most of a modding API, but it is not arranged as one. Generic game knowledge sits under `internal/coop/` (addresses, a bounds-checked memory door, per-tick snapshots, write gates, actor walks, the save image, stage and disc tables, a fake game for tests). The link to the game is one hand-mirrored C mailbox. Everything else is in a 7,225-line `main.go` in package main, which no other package can import.

The plan:
1. Build a `wwapi/` Go package tree. Most of it is code that moves; the new parts are an event bus, a command layer and a mod runtime.
2. Turn multiplayer into `mods/multiplayer` and co-op into `mods/coop`. Both are compiled into the app.
3. Ship a small Go-only example mod.
4. Keep the injected C as one "firmware" blob the whole way through. It changes only at the end, in small separate PRs.

---

## 2. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Direction | **Go first, C last.** The API, mods and example are built in Go without touching the live blob. The C runtime is designed (§6.6) but waits for a second real C consumer. |
| 2 | Where the API lives | **Top-level `wwapi/`**, in the same `go.mod`, with a **v0 UNSTABLE** banner. 0.x minor versions may break. |
| 3 | Manifest format | **Embedded `mod.json`** (`go:embed`), validated against `wwapi/schema/mod.schema.json` by a CI tool. |
| 4 | Co-op's place | **Its own mod, `mods/coop`, which requires `multiplayer`.** If the network channel registry slips, it may sit under `mods/multiplayer/coop` for a while. |
| 5 | Dead C experiments (D2) | **Delete** shadow modes 1-4, the echo ring, the eye-fix probe steps 1-7 and modes 1-2 (the default step 8 eye fix is live and stays) and the `dbg_*` fields (Phase E). **Keep shadow_mode 5**, the live pose-feed mode. **Keep the legacy KAMOME/NPC_OB1 actor puppets** (`move-puppet`, `unhide-puppet`, `broadcast-link` paths): they still draw every remote beyond the 2 mini-Link slots, so they are what players 3 and 4 see. They go only when the mini-Link slot count covers the player count. |
| 6 | ~60 research subcommands | **Move to a separate `cmd/wwdev` binary**, built in CI and not shipped. Delete only the ones tied to removed C modes. |
| 7 | Timing | **Phase A now; Phase B after #74 (now merged); Phase C after app PR 9 and the owner's uncommitted `ww-multiplayer` work land.** One package per PR, landed quickly. |
| 8 | C runtime and arena | **Defer** the C dispatcher, event ring, command queue and arena move **until co-op M7 needs them.** Then use docs/08 §4.5 **Option A** (+8 KB, arenaLo 0x80416000), gated on the heap error counters. |
| 9 | Event model | **Polled snapshot edges only, 10 Hz by default, 30 Hz as an option.** The docs say in bold that game events are polled, not in-game callbacks, so short toggles can be missed. |
| 10 | Write safety | **Each mod declares the address set it may write.** The sets must not overlap (checked at Init). Every write goes through `wwapi/write` (gate, heartbeat, verify, breaker), and a backup is taken before a mod's first save write. |
| 11 | Splitting `runPuppetSyncCtx` | Do it inside C12 and D1, only after the probes are gone. Keep a two-Dolphin smoke checklist: pose, face, colors, stage change, leave and reset. |
| 12 | Root package-main tests | Move tests with their code. Cross-package tests (cli golden, coop converge, dolphinenv equivalence) go to `tests/`. `TestCLIReadsNoConfig`, `TestCLISubcommandsUnchanged` and `TestCoopDiagDispatched` read source files, so B1 and D7 rewrite them, not just move them. |
| 13 | `mods_hash` on the wire | A JSON field in the lobby join, plus a **player-role caps bit** with the hash after a `hasColor` byte for direct `join`. Caps mean different things per role, so it is not a global bit. No `ProtocolVersion` bump. |
| 14 | C in CI | No devkitPPC job for now. Freshness, budget and layout tests run on every PR (A3); the owner smoke-tests each C PR. |
| 15 | Two event systems | `wwapi/events` is the source of truth. `session.Controller` adapts to it through `EventMod` from D4 on. |
| 16 | What "mods" means | Compiled-in Go packages, not drop-in folders. Out-of-process mods over loopback RPC are the possible future path. The C side is an open question for Phase E (§10). |
| 17 | Moving package main (D7) | Keep package main at the repo root through D6. Move it to `cmd/ww-multiplayer` in **one dedicated PR (D7)** at the end of Phase D, with a dry-run release build. |
| 18 | Contract check before E1 | **Two modes.** Before E1: a missing header means ABI 0 with today's fixed Features, and writes are gated only by the Go-computed blob hash. From E1 on: strict. Main never refuses today's header-less blob. |

---

## 3. RAM map (GZLE01, patched DOL)

```
 0x80006338   bl main01_init       (hook: one-shot init)
 0x80023204   bl frame_shim        (hook: per frame, tail-calls 0x802449AC)
 0x80108210   bl daPy_draw_hook    (hook: Link #1 draw, wraps daPy_lk_c::draw 0x80107308)
 0x802C39A0   bl pad_read_shim     (hook: after PADRead in JUTGamePad::read)
 0x803CA754   Link actor pointer (PLAYER_PTR)

 0x8040EFC0   original __ArenaLo (vanilla)
 0x80410000   T2 code start (injected .text/.data/.bss)
 0x80412E28   T2 end                      -> 0xD8 bytes free
 0x80412F00   Mailbox start (0x1A0 bytes)
 0x804130A0   Mailbox end                 -> 0xF60 bytes slack
 0x80414000   patched __ArenaLo (OSInit patched by build.py)
```

(verified: `inject/build.py`, `inject/include/mailbox.h`, `internal/inject/blob.go` T2Bytes = 0x2E28, `t2_budget_test.go`)

**Mailbox layout** at 0x80412F00 (the source of truth is `inject/include/mailbox.h`; `tools/gen_mailbox` generates the Go copy, `wwapi/mailbox/layout_gen.go`, and `TestMailboxLayout` fails when it is stale):

| Offset | Field(s) | Owner | Future home |
|---|---|---|---|
| +0x00 | `spawn_trigger` (per-frame heartbeat) | C | core |
| +0x04 | `progress` (debug) | C | D2 delete |
| +0x08 | `_pad0`, reused as the frame callback pointer slot | C | core |
| +0x0C | `draw_progress` (debug) | C | D2 delete |
| +0x10..+0x8F | `puppets[4]` (legacy actor puppets, 0x20 each) | Go | **keep** (players 3+) |
| +0x90..+0x93 | `shadow_mode`, `shadow_latched` | Go/C | keep mode 5 only |
| +0x94..+0xA7 | `dbg_*`, `echo_delay`, `echo_ring_state` | C | D2 delete |
| +0xA8..+0xB7 | mini-Link pose feed: `pose_buf_ptrs[2]`, `pose_joint_counts[2]`, `pose_buf_states[2]`, `pose_seqs[2]` | Go/C | multiplayer |
| +0xB8..+0xBF | `dbg_pose_first_word`, `dbg_node_mtx_first` | C | D2 delete |
| +0xC0..+0xC7 | `pose_publish_*` (local pose out) | C | core |
| +0xC8..+0xDB | `warp_seq`, `warp_ack`, `warp_x/y/z` | Go/C | core |
| +0xDC..+0xE3 | `warp_dbg_*` | C | D2 delete |
| +0xE4 | `warp_force` | Go | core |
| +0xE8 | `eye_fix_step` | Go/C | multiplayer (live default step 8; D2 deletes steps 1-7 only) |
| +0xEC..+0x117 | `eye_fix_post_chain[10]`, `eye_fix_post_chain_count` | C | D2 delete |
| +0x118 | `eye_fix_mode` | Go/C | multiplayer (live default mode 0; D2 deletes modes 1/2 only) |
| +0x11C..+0x12F | `dbg_*` | C | D2 delete |
| +0x130..+0x14F | `face_state[2]` (remote faces) | Go | multiplayer |
| +0x150..+0x15F | `face_seqs[2]`, `face_hook_*` | Go/C | multiplayer |
| +0x160..+0x16F | `face_state_local` | C | core |
| +0x170..+0x17F | `input_*` override, `input_overrides` | Go/C | core |
| +0x180..+0x187 | `_reserved_hdr[2]` (zero today) | — | E1: `'WWMB'` + ABI u16 + layout u16 |
| +0x188..+0x19F | `tunic_bufs[3]`, `tunic_buf_size`, `tunic_enable[3]`, `tunic_state`, `tunic_swaps` | Go/C | slots 0-1 multiplayer, slot 2 core |
| +0x1A0 | end today | — | E1: Features u32, pad to 0x1B0 |

**Later (co-op M7, Option A):** arenaLo moves to 0x80416000, API RAM sits at 0x80415000, and T2 gets a 20 KB budget. Go reads the mailbox address from `blob.go` and, if no magic is there, probes the old address and reports `BlobMismatch` instead of reading garbage.

**Facts that shape the plan:**
- Only **0xD8 bytes** of code space are free. D2 is the only way to free T2 bytes without an arena bump.
- The 8 reserved header bytes hold the magic, ABI and layout version and nothing else. Features go past +0x1A0, inside the 0xF60 slack.
- The blob hash needs no C field. Go hashes T2's `.text` read back from RAM against `blob.go`. It must skip `.data`/`.bss`, which change at runtime (inferred: section layout not yet checked in the map).
- The 1 MB Outset arena bump failed. Arena growth is taken from ZeldaHeap and must stay small.

---

## 4. Target layout

One Go module. Paths in parentheses show where the code comes from.

```
ww-multiplayer/
  cmd/
    ww-multiplayer/   main.go (GUI/TUI/CLI/browser faces), mods.go (static mod list), commands.go (registry)
    wwdev/            research and dev binary (eye-fix-*, face-*, tint-*, disasm, peek/poke, coop-probe,
                      auto-recapture, mp-local, dolphin2). Built in CI, not shipped.
  wwapi/              THE API (v0, UNSTABLE)
    wwapi.go          Game, Attach, Info, Version
    mod.go manifest.go registry.go resolve.go context.go supervisor.go status.go contract.go
    schema/mod.schema.json
    commands/  events/  write/
    emu/ (launch/, host/)  mem/  addr/  mailbox/ (generated)  state/  gate/  actors/
    save/  savefile/  backup/  disc/  stage/  tables/  player/  avatar/  gfx/texture/
    patch/  log/  fakegame/  netutil/
  mods/
    multiplayer/      mod.json, mod.go; net/ session/ engine/ playercolor/ tunicsync/
    coop/             mod.json (requires multiplayer), mod.go; class/ derive/ inventory/ story/ bind/
                      world/ wire/ hub/ replica/ proto/ combat/ savemerge/ synctables/ cli/ console/
    example/          Go-only, local_only
    _fixtures/        bad manifest, api mismatch, missing dep, cycle, Init error, Run panic
  inject/             C, one blob (later split into src/core and src/mp with the same offsets)
  tools/              extract_blob.py, gen_mailbox/, gen_event_masks.py, validate_manifests,
                      check_no_game_code.py
  tests/              cross-package golden and e2e tests
  docs/               api/ (this page, C ABI, RAM map), mp/, coop/, map/, TESTING.md, history.md
```

`internal/{gui,tui,config,applog}` stay where they are until the app PRs finish. `internal/mailboxgen` (C9) is the mailbox.h parser and `layout_gen.go` emitter shared by `tools/gen_mailbox` and the `wwapi/mailbox` freshness test, and it stays under `internal/`.

---

## 5. The API

Mods see a `wwapi.Context` (§6.2), which exposes the `Game` below. A mod never holds a raw `*dolphin.Dolphin`. "Exists" means code is behind it today; "new" means it has to be written.

### 5.1 Root handle

```go
package wwapi

const Version = "0.1.0" // semver; 0.x minors may break

type Info struct {
    PID           int
    GameID        string   // "GZLE01"
    ABI           uint16   // from the WWMB header at +0x180 (0 = no header: every blob before E1)
    LayoutVersion uint16   // from the same 8-byte header
    BlobHash      uint32   // computed by Go over T2 .text read from RAM
    BlobMatches   bool     // BlobHash equals the hash of blob.go's T2 .text
    Features      Features // header extension past +0x1A0: Avatar, Face, Tunic, Warp, Input, EyeFix, CoopOps...
}

func Attach(ctx context.Context, o emu.Options, log log.Reporter) (*Game, error)
func (g *Game) Info() Info
func (g *Game) Mem() mem.Reader           // read-only, MEM1-bounded; writes go through commands/write
func (g *Game) Snapshot() *state.Snapshot // last snapshot taken by the watcher
func (g *Game) Gate() gate.Status
func (g *Game) Events() *events.Bus
func (g *Game) Commands() *commands.Registry
func (g *Game) Player() *player.Link
func (g *Game) Avatars() *avatar.Slots
func (g *Game) Actors() *actors.Reader
func (g *Game) Save() *save.Service
func (g *Game) Disc() *disc.Catalog       // stage and tables from the user's ISO, cached by DOL SHA-256
```

`mem.Reader` is new: the old `coop/mem.Mem` read and wrote in one interface, and C1 split it into `Reader` and `Mem` (Reader plus Write). Env (`WW_DOLPHIN_PID/INDEX`) is read only in `cmd/`.

### 5.2 Reads

| Surface | Backed by |
|---|---|
| `state.Read(m, Options{Sensitive})`: stage, point, room, layer, event mode, player pointer, HUD pending counters, zones, menu pause, heartbeat | exists: `wwapi/state` (was `coop/snap`). `Options.Sensitive` replaces the `class` import; nil means `DefaultSensitive()` |
| `gate.Tracker.Observe`, `Status.CanWrite / CanWriteIdle / CanWriteCounter / CanWriteOnTransition / Pending / Why` | exists: `wwapi/gate` (was `coop/gate`) |
| `player.Link`: Ptr, Ready, Position (pos and angle together), DemoType, `Pose()`, `Face()`; Room and Textures to come | exists: `wwapi/player` (was `emu.Dolphin` GetLinkPtr/IsInGame/ReadPlayerPosition and the read half of `runBroadcastPoseCtx`). Room and Textures come with C12/C14 (`readLinkTextures`); `peekDemo` still reads mDemoType itself |
| `actors.Read(m, limit)` → Walk{Actors, ByPID, ByAddr}; Revalidate | exists: `coop/actors` minus k1.go |
| `save.Image` (one 0x778 type), `save.Layout`, `save.View`, one item/slot table | exists: `wwapi/save` (`world.Image`/`wire.Image` are aliases until E4; `class` keys its merge rules by `save.Layout` offset; `View` was `derive/view.go`) |
| Bit math: SwitchBit, TboxBit, EventByte, ZoneAddr, ... | exists: `wwapi/addr/bits.go` |
| `stage.Load`, `tables.Get/Build/Identify` | exists: `wwapi/stage` (was `coop/stagedata`; it reads a `disc.FileSource`, `wwapi/disc`), `wwapi/tables` builders and cache (was `coop/tables`; its `Synced*`/`GBA*`/`KillMask`/`Acceptance`/`Report` are functions in `internal/coop/synctables`) |
| `savefile.Parse/ParseCard`, `backup.Locate/Create/Restore` | exists: `wwapi/savefile` (the I/O half; its merge and derive are `internal/coop/savemerge`) and `wwapi/backup` (were `coop/savefile`, `coop/backup`) |
| `mailbox.ReadHeader`, `mailbox.BlobHash`, `mailbox.FrameCounter` | header is new (E1); hash and counter need no C |

### 5.3 Events

```go
package events

type Source uint8 // SourceGame, SourceNet, SourceMod
type Meta struct { Seq uint64; At time.Time; Source Source; Origin string }
type Event interface{ EventName() string; EventMeta() Meta }

func On[T Event](b *Bus, fn func(T)) (off func())
func Chan[T Event](b *Bus, buf int) (<-chan T, func()) // drop-oldest
func Last[T Event](b *Bus) (T, bool)
func Wait[T Event](ctx context.Context, b *Bus, pred func(T) bool) (T, error)
func (b *Bus) Emit(e Event) error // through a mod's Context: only mod.<id>.* names
```

A watcher goroutine owned by `Game` runs `state.Read` and `gate.Observe` at 10 Hz (30 Hz optional) and diffs consecutive results. The diff logic is ported from `coop_diag.go` (`coopWatcher.step`, `diffSave`, `saveLabel`, `edgeNote`).

| Group | Events |
|---|---|
| Lifecycle | GameAttached, GameDetached, BlobPresent, BlobMismatch |
| Liveness | HeartbeatLost, HeartbeatResumed, Tick |
| Stage and session | FileLoaded, StageChanged, RoomChanged, LayerChanged, TransitionStarted/Ended, EventStarted/Ended, MenuPaused/Resumed, WriteGateChanged |
| Save edges | ItemGained/Lost, SwitchSet/Cleared, EventBitSet, ChestOpened, LifeChanged, RupeesChanged, MaxLifeChanged, DungeonItemGained, OceanVisited |
| Actors | ActorSpawned, ActorRemoved, EnemyKilled (inferred from actor and zone diffs) |
| Link | LinkMoved (rate-limited), LinkReplaced |
| Mods | `mod.multiplayer.*`, `mod.coop.*` |

**All game events are polled edges, not in-game callbacks. Order inside one tick is not defined.**

### 5.4 Commands

Every write to the game goes through `commands`, the single audited path. Typed helpers are thin wrappers over it.

```go
package commands

type Scope uint8 // Local, Authority
type Spec struct {
    Scope  Scope
    Cheat  bool
    Writes []write.Range // must sit inside the calling mod's declared address set
}
type Result struct { OK bool; Code string; Err error; Done <-chan struct{} }
type Handler func(ctx context.Context, args json.RawMessage) (Result, error)

func (r *Registry) Register(name string, s Spec, h Handler) error // core.* or mod.<id>.*
func (r *Registry) Execute(ctx context.Context, name string, args any) (Result, error)
```

| Command | Status |
|---|---|
| `player.warp` (Warp, WarpForce) | exists: `wwapi/player` (was `warp_link.go`) |
| `input.override` (Override, Release) | exists: `wwapi/player` Override/Release/Press (was `injectInput`, `runInput`) |
| `avatar.*` (Claim, SetPose, SetFace, SetTunic, Hide, Release; Cap = 2 today) | the slot writes exist: `wwapi/avatar` (WritePose, WriteFace, ClearSlot, Rearm, Clear, SetPoseFeedMode, Tunics; was `posefeed.go`, `runPuppetSyncCtx`, `tunicSync`). Claim/Release (slot ownership) are still puppet-sync's `pickLinkSlot`/`freeLinkSlot` |
| `save.*` (GiveItem, AddLife, AddRupees, SetMaxLife) | mechanism exists in co-op; helper is new |
| `flags.*` (SetSwitch, SetEventBit, SetTbox) | generic part of `replica/writer_fields.go`; helper is new |
| `stage.request` | exists: `replica/escape.go` |
| `actor.kill`, `actor.spawn` | **returns `ErrUnsupported`** until the C op ring exists |

`wwapi/write` is the generic half of `replica`: Writer, Allowlist, Breaker, FrameWriter, Field. Co-op keeps its own targets and byte rules and registers them as its address set.

### 5.5 Services

| Service | From |
|---|---|
| Logging: `log.Reporter`, `Multi`, `Prefix` | `internal/report` plus `prefixReporter` |
| Emulator: `emu.Attach`, `launch.*`, `host.*` | `internal/dolphin`, `internal/dolphinenv` |
| Patcher: `patch.Run(Options)` with `PostPatch []func(w io.Writer, image string)` in place of `CoopTables`. A post-patch step **never fails the patch**; `cli-golden/patch-*.txt` stays byte-identical | `internal/patch`, `internal/inject` |
| Textures: CMPR recolor, ResTIMG | `wwapi/gfx/texture` (was `internal/tunic`) |
| Test double: `fakegame.New()` | `coop/fakegame` |
| Mod supervisor | `runPipelines` semantics plus `recover` |

### 5.6 C side

**Now** (after the zero-cost split in E3): game bindings, `ww_heap_alloc` (allocate once, never free), `ww_j3dsys_save/restore`, `ww_player`, `ww_player_ready`, `ww_frame_counter`, and the warp, input, pose-publish, local-face and local-tunic services.

**Later** (the planned v1 C surface): a `WwModuleDesc` hook table, `ww_event_push`, `ww_cmd_register(op, fn)`.

---

## 6. The mod model

### 6.1 Interface

```go
type Mod interface {
    Manifest() Manifest            // parsed from the embedded mod.json
    Init(c *Context) error         // register events, commands, verbs, address set, config keys; must not block
    Run(ctx context.Context) error // optional (embed wwapi.NoRun); return nil on cancel
}

// Optional, found by type assertion:
type PublicAPI   interface{ API() any }
type NetProvider interface{ Engine() session.Engine }           // provides ["net"]; at most one
type CLI         interface{ Commands() []cli.Command }
type UI          interface{ Verbs() []Verb; Panels() []Panel }
type NetChannel  interface{ Channel() (id byte, h ChannelHandler) } // ride multiplayer's transport
```

Mods are listed statically in `cmd/ww-multiplayer/mods.go`. There are no `init()` side effects. Go's `plugin` package does not work on Windows, so there are no drop-in Go mods.

### 6.2 Context

```go
func (c *Context) ID() string
func (c *Context) Log() log.Reporter            // prefixed [id]
func (c *Context) Game() *Game                  // nil when there is no game (a pure server still runs)
func (c *Context) Events() *events.Bus          // Emit limited to mod.<id>.*
func (c *Context) Commands() *commands.Registry // Register limited to mod.<id>.*
func (c *Context) Allow(r ...write.Range) error // declare the address set (overlap check)
func (c *Context) Config() ConfigSection
func (c *Context) DataDir() string
func (c *Context) Has(id string) bool
func (c *Context) Mod(id string) (any, bool)    // another mod's PublicAPI
func (c *Context) Features() Features           // what the patched blob provides
```

Everything registered through a Context belongs to that mod, so quarantine can remove it.

### 6.3 Manifest

`mods/<id>/mod.json`, embedded with `go:embed` and validated against `wwapi/schema/mod.schema.json` in CI:

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
  "c": null
}
```

- `id` equals the folder name. A leading `_` marks a test fixture.
- `needs` is checked against the blob's Features. If something is missing, that mod alone gets `NEEDS_REPATCH`.
- `c` is reserved for future C modules.
- Co-op declares `"dependencies": {"multiplayer": "^1.0"}` and `needs: []`.

### 6.4 Lifecycle

1. **Collect** the static list.
2. **Filter** by config `mods.<id>.enabled`, CLI `--mods=` / `--no-mod=`, and `--dev`.
3. **Resolve**: the `api` range against `wwapi.Version`, dependency ranges, topological order, cycle detection, at most one `net` provider. A failure carries over to dependents.
4. **Attach** the game, lazily. Pure-network mods run without Dolphin.
5. **Contract check** (two modes, decision 18): GZLE01, the WWMB header, the blob hash against `blob.go`, and `needs` against Features. On a mismatch, writes are refused with "this save state or ISO is from another patch; cold-boot or recapture."
6. **Init** in dependency order.
7. **Run** under the supervisor. A panic or error moves only that mod to `QUARANTINED` and removes its handlers.
8. **Stop** in reverse order.

Statuses: `LOADED`, `DISABLED`, `FAILED(err)`, `QUARANTINED(err)`, `NEEDS_REPATCH(missing)`. They show in `ww-multiplayer mods [--json]`, the TUI and the GUI Mods page.

### 6.5 App, config and versioning

- `config.Mods map[string]ModConfig{Enabled *bool; Settings json.RawMessage}`. The CLI still never reads config. `wwapi` and `mods/*` must **never import `internal/config`**; `Context.Config()` is backed by an interface the faces fill in. `TestCLIReadsNoConfig` is extended to walk `wwapi/...` and `mods/...`.
- The GUI gets a **Mods page** (version, status, reason, toggle for the next session, and a Patch button next to `NEEDS_REPATCH`), plus Bridge methods `Mods`, `SetModEnabled`, `CallMod` and a mod event stream.
- Three version numbers: `wwapi.Version` (Go semver), the C ABI (WWMB header plus blob hash), and each mod's wire protocol.
- **`mods_hash`**: SHA-256 of the sorted `id@version` of `client_required` mods, sent as a JSON field in the lobby join (old hosts ignore unknown fields), and for direct `join` as a player-role caps bit with the form `[ver][role 0][caps][hasColor][rgb][hash:32]`. Tested against old-client and old-host stubs.

### 6.6 C-side mods

There is one blob. Go mods add no C. The blob advertises a Features word, and each mod's `needs` is checked against it.

The E3 split into core and multiplayer files uses plain direct calls in a fixed order, with no function-pointer table (zero extra bytes, inferred; the T2 budget test proves it). The draw order is written down as if it were a dispatcher:

```
daPy_draw_hook(link):
  core pre:   face_emit_publish_local -> local tunic swap
  mods pre:   (priority order)            [today: none]
  original:   daPy_lk_c::draw 0x80107308
  core post:  local tunic restore -> pose publish
  mods post:  (REVERSE priority order)    [today: mini-Link per slot, face and tunic bracket, eye fix]
```

Running post-draw callbacks in reverse keeps today's brackets nested. Drawing models stays a core service, because the face and tunic swaps mutate a `J3DModelData` that all Links share.

**Later, when co-op M7 needs it:** a generated `WwModuleDesc[]` dispatcher, API RAM with a directory of per-module regions, a C→Go event ring for one-frame edges, a Go→C command queue that generalises the warp handshake, and the Option A arena move.

---

## 7. Multiplayer and co-op as mods

| Today | Goes to |
|---|---|
| `internal/network` | `mods/multiplayer/net` (the `'O'` co-op channel becomes a `NetChannel` registry, same bytes on the wire) |
| `internal/session` | `mods/multiplayer/session` (co-op types move to `mods/coop`; gains `EventMod`) |
| `session_engine.go`, `lobby_engine.go`, liveState, host/join, server, fake client | `mods/multiplayer/engine` |
| `runMultiplayerGoroutines` | the multiplayer mod's `Run`, under the supervisor |
| `runBroadcastPoseCtx` | a multiplayer feeder over `player.Pose()`, `player.Face()`, `LinkMoved` |
| `runPuppetSyncCtx` | multiplayer feeder (slots, lerp) plus `wwapi/avatar` (slot writes, arming, face, tunic). Actor-puppet fallback for players 3+ stays |
| Pose math (`lerpPoseInPlace`, `applyPoseAt`, `localizePoseInPlace`) | `wwapi/avatar` |
| `internal/playercolor`, `playercolor_flag.go` | `mods/multiplayer/playercolor` |
| `tunic_sync.go` | `mods/multiplayer/tunicsync`; the core goes to `wwapi/avatar` |
| `mp_local.go`, `mp_local_shift.go` | `cmd/wwdev` |
| GUI's use of `network.ValidatePlayerName` | the multiplayer mod's PublicAPI |
| C: mini-Link, pose-feed slots, face hook, eye fix, remote tunic slots 0-1 | `inject/src/mp/*.c` (local tunic slot 2, pose publish and local face are core) |

**Co-op** (`mods/coop`, requires multiplayer): class, derive, inventory, story, bind, world, wire, hub, replica, and a new `proto/` so `replica` no longer imports `hub`. `k1.go` → `combat/`; savefile `merge.go`/`derive.go` → `savemerge/`; the `Synced*` table accessors → `synctables/`. The root `coop_*.go` files move in, renamed by feature. Its CLI commands come through the `CLI` capability and its table step through `patch.Options.PostPatch`.

---

## 8. Phased plan

Ground rules for every PR:
- One concern per PR. Package moves use `git mv` plus an import rewrite only.
- `go vet` (Windows, Linux, macOS, and the desktop tags) and `go test -race ./...` stay green.
- CLI output stays byte-identical unless the PR says otherwise. Co-op golden and converge tests stay byte-identical.
- C PRs come last. Each needs the owner's two-Dolphin smoke test and a save-state recapture, because CI cannot build C.
- Release builds stay green. Only D7 touches the build entry point.
- Tests that parse `main.go` or glob root `*.go` files are rewritten in the same PR that changes what they read (B1, D7).

### Phase A: now
- [x] **A0** `docs/api/00-design.md` (this page). No code.
- [ ] **A1** Delete the legacy injector (`internal/dolphin/inject.go`, `inject_code.go`, `helpers.go`) and the `inject`/`check` commands. Subcommand list 91 → 89 on purpose.
- [ ] **A2** Stale comment and docs sweep (comments and docs only, no code; `blob.go` byte-identical after re-running `extract_blob.py`).
- [ ] **A3** CI: blob freshness, T2 budget and mailbox layout tests on every PR; dedupe the vet steps; move generators to `tools/` with `go:generate`.
- [ ] **A4** Delete dead experiment commands: write-test, teleport-test, hijack-test, scan-npcs, scan-actors, tint-material, inspect-materials. **Do not touch** move-puppet, unhide-puppet or broadcast-link.

### Phase B: after #74 (merged)
- [ ] **B1** Command registry replaces the CLI switch; printHelp generated from it; `TestCLISubcommandsUnchanged` and `TestCoopDiagDispatched` rewritten to read the registry.
- [ ] **B2** `wwapi/events`: Bus, Meta, On/Chan/Last/Wait, drop-oldest, namespace rules.
- [ ] **B3** `wwapi` mod runtime: Mod, Manifest plus schema, resolver, Context, status, supervisor, `mods --json`, `tools/validate_manifests`, `mods/_fixtures`.

### Phase C: after app PR 9 and the owner's uncommitted work
Order: C1 first; C4 before C8; C6 before C7; C8 before C14; C5 any time after C1.
- [x] **C1** `internal/coop/{mem,addr}` → `wwapi/{mem,addr}`; split `mem.Reader` / `mem.Mem`; big-endian helpers.
- [x] **C2** Fold address literals into `addr` with decomp citations.
- [x] **C3** Cut snap→class with `state.Options{Sensitive}` (equivalence test); move snap → `wwapi/state`, gate → `wwapi/gate`.
- [x] **C4** `wwapi/disc` out of `internal/inject`; stagedata → `wwapi/stage`.
- [ ] **C5** k1.go → `internal/coop/combat`; the rest of actors → `wwapi/actors`.
- [x] **C6** `wwapi/save`: one `Image` (old names as aliases for one release), one item/slot table, `Layout`, `View`.
- [x] **C7** Split savefile (merge/derive → `savemerge`; I/O → `wwapi/savefile`); backup → `wwapi/backup`.
- [x] **C8** Split tables (builders → `wwapi/tables`; sync accessors → `synctables`).
- [x] **C9** Generated mailbox: `tools/gen_mailbox` → `wwapi/mailbox/layout_gen.go`. No C change. The parser and emitter live in `internal/mailboxgen`, which `tools/gen_mailbox` and the `wwapi/mailbox` freshness test (`TestMailboxLayout`) both import.
- [x] **C10** `wwapi/emu` (`Attach(Options)`, env read only in `cmd`); dolphinenv → `emu/launch`; report → `wwapi/log`. `ReadAbsolute`/`WriteAbsolute` are unexported: a `*emu.Dolphin` is itself a MEM1-bounded `mem.Mem` (the old `mem.DolphinMem` folded in, since `mem` cannot import `emu`), and package main reads `WW_DOLPHIN_PID/INDEX` in `dolphin_env.go`. Window, screenshot and keys → `emu/host`; `prefixReporter` → `log.Prefix`. "Env read only in `cmd`" holds for `emu` (WW_DOLPHIN_PID/INDEX); `launch.FromEnv`/`Detect` keep their `os.Getenv` default and `backup` still reads USER_DIR_1/2, both left for the §9 cleanup ("Env: read env only in `cmd/`").
- [x] **C11** `wwapi/player`: Warp, Input, Pose/Face reads, Link accessors. `player.Link` (over a `mem.Reader`) has Ptr, Ready, Position (pos and angle), DemoType, Pose and Face; `emu.Dolphin`'s GetLinkPtr/IsInGame/ReadPlayerPosition moved to it, so Link's position is read through the MEM1-bounded `Read`. Room and Textures are left for C12/C14 (`readLinkTextures` did not move in C12; see the C12 entry). Input is `Override`/`Release`/`Press`/`Overrides`; `playertest` is the fake C warp handler.
- [x] **C12** `wwapi/avatar` and `wwapi/gfx/texture`. `internal/tunic` → `wwapi/gfx/texture` (package `texture`). `wwapi/avatar` has the posefeed guards (`PoseUsable`, `PoseDstInMEM1`, `NextSeq`, `BumpSeq`), the pose math (`Localize`, `ApplyAt`, `Lerp`), the slot writes (`Off*` offsets, `WritePose`, `WriteFace`, `ClearSlot`, `SetPoseFeedMode`), `Rearm` (was `resetPuppetSyncMailbox`) and `Clear` (the body of `clearMultiplayerState`, which keeps only the env-selected Dolphin lookup), and `Tunics` (was `tunicSync`); `avatartest` is the fake RAM and C tunic allocation. `runPuppetSyncCtx`'s slot assignment, arming loop (the shadow_mode re-assert after a save-state load), lerp state and actor-puppet fallback stay in main (§10.11: split it in D1); `syncTunics`, the appearance byte, `tunic-slot` and the `dump` tunic block are `tunic_feed.go`. `wwapi/gfx/texture` and `Tunics` still import `internal/playercolor` until D1 decides where the color type lives. `readLinkTextures`/`readNameTab` (the `tunic-probe`/`tunic-recolor` diagnostics) stay in `tunic_diag.go` so `gfx/texture` stays pure bytes in, bytes out; they go with the probes (D6/E2) or C14.
- [ ] **C13** `wwapi/write` out of replica (**high risk**: co-op golden and converge byte-identical, owner's M2-M6 rerun).
- [ ] **C14** `wwapi.Game`, watcher, `commands`; `fakegame` → `wwapi/fakegame`; contract check in pre-E1 mode.

### Phase D: mods
- [ ] **D1** `mods/multiplayer` (3-4 PRs, one area each).
- [ ] **D2** `mods/coop` with `proto/` and `PostPatch` (2-3 PRs).
- [ ] **D3** `mods/example` (Go-only, `local_only`) plus `docs/api` guides and `TESTING.md`. Land early if possible, right after B3/C14.
- [ ] **D4** App integration: `config.Mods`, `--mods/--no-mod/--dev`, GUI Mods page, `EventMod`, session as a bus adapter.
- [ ] **D5** `mods_hash` in the lobby join and the player-role caps bit.
- [ ] **D6** `cmd/wwdev`: research probes, coop_diag, auto-recapture, mp-local, dolphin2; golden list split per binary.
- [ ] **D7** Move package main to `cmd/ww-multiplayer` in one PR: CI and build scripts, `ww_windows.syso` and its `go:generate` line, README/SETUP/CLAUDE.md build lines, and the source-reading tests.

### Phase E: C, last
- [ ] **E1** WWMB header written by `main01_init` (magic, ABI, layout in the 8 reserved bytes; Features at +0x1A0, mailbox padded to 0x1B0). Contract check switches to strict. **Lands before any other C layout change.**
- [ ] **E2** C D2 deletion (offsets kept as `_reserved`, layout version bumped). Actor puppets stay.
- [ ] **E3** Zero-cost C split into `src/core` and `src/mp`, `game.h` → `wwapi.h`, `mailbox.h` → `mailbox_core.h` + `mailbox_mp.h` with the same offsets.
- [ ] **E4** Retire `patch_iso.py`, `dolphin2.sh`, `mplay2.sh`; remove the Image aliases; build.py reads devkitPPC paths from env.
- [ ] *Later* (co-op M7): Option A arena move, `WwModuleDesc` dispatcher, event ring, command queue.

---

## 9. Cleanup list (summary)

This page is the in-repo summary; the A1, A2, A4, E2 and E4 PRs carry the item-level detail. In short:
- **Delete:** the legacy runtime injector and `inject`/`check`; the early-experiment and tint commands (A1, A4); the D2 C experiments (E2); `dolphin2.sh`, `mplay2.sh`, `patch_iso.py` after checking each (E4).
- **Keep:** the KAMOME/NPC_OB1 actor puppets and their commands while they draw players 3+; shadow_mode 5.
- **Deduplicate:** the mailbox and arena addresses (one header plus generated Go), the Link pointer and other address literals, big-endian helpers, `world.Image`/`wire.Image`, the item tables, and the inverted imports (snap→class, savefile→co-op, replica→hub, k1→tables, world/wire→class).
- **Fix stale text:** comments in build.py, mailbox.h (for example the "0x94 B" size, really 0x1A0), multiplayer.c and extract_blob.py; docs/08's "0x218-byte gap" (really 0xF8); docs/04's memory layout.
- **Env:** read env only in `cmd/` and pass it down.

---

## 10. Open question for Phase E: a boot-time C module loader

The plan above assumes that **a C mod always needs a blob rebuild and an ISO re-patch.** Before Phase E commits to that, it must look at the alternative described in the owner's research note on BetterSunshineEngine, [docs/07-bettersunshine-research.md](../07-bettersunshine-research.md).

**What that note describes.** On Super Mario Sunshine, the Kuribo loader reads compiled C modules (`.kxe` files) from a folder in the extracted disc (`/files/Kuribo!/Mods/`) at boot. BetterSunshineEngine is one such module: it owns the hook sites and offers lifecycle callbacks (stage init, update, draw, exit; player init and update). Other modules only register functions with it, so several modules can share one hook without fighting over the same branch.

**Why it matters here.** If a Kuribo-style loader works for Wind Waker on Dolphin, C mods could be added or removed by changing files on the disc image, with no blob rebuild. That would change:
- the "C mods need a re-patch" stance in §6.6 and decision 16;
- what the `c` field in `mod.json` means;
- whether the E3 split and the later `WwModuleDesc` dispatcher should be shaped around loaded modules instead of a static table.

**What Phase E must answer before E3:**
1. Does a boot-time loader run under Dolphin's JIT for GZLE01? Code loaded before the game first runs it may avoid the runtime-injection wall in docs/03 and docs/05, but nobody has tested this.
2. Where would the loader and its heap live, given 0xF8 free bytes of T2 and the failed 1 MB arena bump?
3. How would the contract check and blob hash cover code that is loaded at boot rather than patched into the DOL?
4. Is it worth the cost now, or only when an out-of-tree C mod appears? The research note itself advises not chasing Kuribo for multiplayer v1.

Until this is answered, the plan stays as written: one blob, Features advertised, C mods need a re-patch.
