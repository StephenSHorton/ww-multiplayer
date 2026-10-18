# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this project.

## Project Overview

**Wind Waker Multiplayer** — a Go-based tool that enables real-time visual
multiplayer in The Legend of Zelda: The Wind Waker on Dolphin emulator.
Each player sees the other's actual Link in-game, walking around at the
remote's real world coords with their real animations, ~50ms latency on
LAN. Two-Dolphin local play is wired up via `./wwdev.exe mp-local`
(see "Standard session bootstrap" below).

The mod is shipped as a standalone `ww-multiplayer.exe` patcher (`./ww-multiplayer.exe patch
<vanilla.iso>` produces the patched ISO from the user's own legitimate
Wind Waker disc image). Releases are cut on tag push via GitHub Actions.

## Repository Structure

```
ww-multiplayer/
├── cmd/
│   ├── ww-multiplayer/          # package main, the released exe (moved from the repo root in Phase D7; build ./cmd/ww-multiplayer)
│   │   ├── main.go, commands.go #   Entry point; commands.go: the CLI command registry + generated help (app_face.go: no args = app window, `tui`, `ui --browser`)
│   │   ├── *.go                 #   engine_config.go (newTUIEngine/newCLIEngine; sessionEnv, coopEnv = coopsession.Env),
│   │   │                        #   mods.go (compiledMods: multiplayer, coop; devMods: example; the loaded mods' commands and post-patch steps)
│   │   │                        #   app_face.go also runs the app's mods: --mods/--no-mod/--dev, appModConfig (flags over config.Mods via gui.ModConfig), startApp (wwapi.Runtime + session controller over its NetProvider's engine)
│   │   ├── ww_windows.syso      #   Icon + application manifest (go generate ./cmd/ww-multiplayer); the linker only takes a .syso from the main package's directory
│   │   └── testdata/            #   CLI and help goldens (cli-golden/), the subcommand lists
│   └── wwdev/                   # wwdev, the developer tools (Phase D6; CI builds it, releases don't ship it): registry.go (its command registry + help),
│                                #   commands.go (the command bodies), mp_local*.go (dolphin2, mp-local), coop_diag.go (co-op diagnostics), auto_recapture.go,
│                                #   tunic_diag.go (tunic dev commands), probes.go (the research probes: peek/poke, disasm, eye/face, input, screenshot, dump, ...)
├── internal/
│   ├── applog/                  # Per-session log files (TUI/GUI), redaction, Copy-diagnostics text
│   ├── cliutil/                 # What both binaries' command lines share: FindDolphin/OpenDolphin (WW_DOLPHIN_INDEX/PID), ExeCmd, ExitUsage, --color/WW_COLOR; clitest/ = the CLI golden harness
│   ├── config/                  # The app's saved settings (UserConfigDir/ww-multiplayer/config.json); the CLI never reads it
│   ├── gui/                     # The app window (Wails v2, -tags desktop,production only) + embedded HTML/CSS/JS frontend; first-run Setup (setup.go); the Mods page (mods.go); Bridge = the JS API; `ui --browser` server
│   ├── lan/                     # LAN discovery: the host's UDP beacon, broadcast addresses, the joiner's table (docs/10-lobby.md §10)
│   ├── mailboxgen/              # mailbox header parser (mailbox_core.h + the mailbox_mp.h it includes) + layout_gen.go emitter (used by tools/gen_mailbox and wwapi/mailbox TestMailboxLayout)
│   ├── inject/                  # Standalone ISO patcher (used by `./ww-multiplayer.exe patch`)
│   │   ├── blob.go              # AUTO-GENERATED via scripts/extract_blob.py
│   │   ├── dol.go               # DOL header editor + T2 splice + in-DOL patches
│   │   ├── iso.go               # ISO patcher with FST relocation
│   │   └── ciso.go              # CISO decompressor
│   ├── netutil/                 # Address parsing shared by host/join/server; join codes (WW-XXXX-XXXX, joincode.go)
│   ├── patch/                   # `patch` as patch.Run (writes the patcher's lines, returns errors)
│   ├── redact/                  # IP masking for what the GUI and TUI draw (fixed-width •••.•••.•••.42); the CLI prints addresses unmasked
│   ├── session/                 # UI-free session controller the CLI host/join, the TUI and the GUI drive (docs/01-architecture.md)
│   │   └── session.go, controller.go, event.go, coop.go, coopapp.go (the app's co-op state), lobby.go, launch.go, mods.go (EventMod: the adapter over the mod runtime's bus)
│   └── tui/                     # Charm Bubble Tea UI (resurrected v0.1.5)
│       ├── app.go, splash.go, connect.go, dashboard.go, styles.go
├── wwapi/                       # Modding API, v0 UNSTABLE (docs/api): emu/ (Attach(Options); *emu.Dolphin is a MEM1-bounded mem.Mem; memory.go = RAM scanner; launch/ = Dolphin paths/detection, user dirs, detached launch, cheats, macOS prepare (dolphin2, auto-recapture); host/ = window, screenshot, keys),
│                                #   log/ (Reporter, Stdout / Discard / Multi / Prefix), mem/ (Reader/Mem, FakeMem), addr/ (cited addresses), state/ (per-tick snapshot), gate/ (write gates), actors/ (actor queue walk, Layout reads, structural Reread), write/ (guarded write engine: Allowlist, Breaker, FrameWriter beat wait + rechecks, Field, StageRequest; co-op's replica builds on it),
│                                #   disc/ (DiscFile, FileSource), stage/ (stage readers + stagetest), save/ (Image, Layout, View, item/slot table), savefile/ (GCI/card I/O), backup/ (card backups), tables/ (M1 disc table builders + cache), player/ (Link: Ptr/Ready/Position/DemoType/Pose/Face; Warp, WarpForce; input Override/Release/Press; playertest = fake warp handler), avatar/ (remote Links: pose-feed slot writes, Clear/Rearm, pose math, Tunics = tunic slots; avatartest = fake RAM + C tunic allocation), gfx/texture/ (CMPR tunic recolor, ResTIMG), mailbox/ (T2 + mailbox layout, WWMB header read, BlobHash; layout_gen.go is generated from mailbox_core.h (+ mailbox_mp.h) by tools/gen_mailbox), events/ (bus + core game events), commands/ (command registry, write gate), fakegame/ (deterministic fake game for tests), schema/, semver/; the mod runtime (manifest, resolver, supervisor); Game (Attach/NewGame: watcher, strict contract check: WWMB header + hash of the blob's read-only sections, Phase E1)
├── mods/                        # Compiled-in mods (docs/api §6): example/ (D3), _fixtures/ (runtime test mods)
│   ├── coop/                    # The co-op mod (D2c: mod.go + mod.json, requires multiplayer; CLI commands + the stage tables post-patch step). Co-op engine (docs/08): replica, hub, world, wire, derive, class, story, inventory, bind, ...; proto/ (the Feature bits, policies and scoped filters hub and replica share)
│   │                            #   combat/ (M6: K1 classes, layouts, gates, Revalidate over wwapi/actors), savemerge/ (offline save merge), synctables/ (sync verdicts and acceptance over wwapi/tables)
│   │                            #   session/ (package coopsession, D2b: the --coop session, console, puzzles, combat status, the app's co-op (app.go) and the engine seam (hook.go: Env, App/CLIApp);
│   │                            #   the co-op golden + converge tests, testdata/coop-golden), stagetables/ (+ stagetablestest/), procstate/ (session marker process check),
│   │                            #   diag/ (what coop_diag.go shares: Tick, GateSnapshots, SplitFlags, Where, WithMem, DiffSave/SaveLabel), bind/bindtest/ (a FakeGame with a bound file),
│   │                            #   cli/ (package coopcli: coop-tables, coop-tag, coop-world, save-diff, save-merge)
│   └── multiplayer/             # The multiplayer mod (D1): mod.go + mod.json (provides net, PublicAPI ValidatePlayerName), listed in compiledMods()
│       ├── engine/              # Session core: Engine (session.Engine: host/join/lobby, NewTUIEngine/NewCLIEngine), lobby.go, game.go (the app's Start: Dolphin + pipelines), LiveState, RunHostSession/RunJoinSession, co-op via the seam in coop.go (Coop, CoopApp, LobbyCoop, CoopSide), RunPipelines, server + fake client, color-pref clients; enginetest/ = fake Dolphins
│       ├── feed/                # Pose pipelines: BroadcastPose, PuppetSync (Feeder: link/actor slot assignment, lerp, arming over wwapi/avatar), PoseFakeLoop
│       ├── net/                 # package network: TCP server/client, protocol + handshake, lobby, co-op channel, timing knobs (netconfig.go)
│       ├── playercolor/         # Tunic color palette + --color / WW_COLOR parsing (docs/09)
│       └── tunicsync/           # Session side of the tunic colors: SyncTunics, the appearance byte, the dump block (docs/09 §5.4)
├── inject/                      # C source for the injected PPC code (split into core + the multiplayer mod in Phase E3, docs/api §6.6)
│   ├── src/core/                # The core: hooks.c (main01_init, frame_shim + multiplayer_update, daPy_draw_hook, pad_read_shim; the fixed call
│   │                            #   order written as a dispatcher), services.c (warp, tunic images + swap, face tev blocks + snapshot, pose publish),
│   │                            #   core.h (core-only decls), mods.h (the mod entry points the hooks call)
│   ├── src/mp/                  # The multiplayer mod: mp.h (PROC_* list, eye joints), puppets.c (actor puppets), minilink.c (mini-Links, pose feed,
│   │                            #   eye fix; mp_frame, mp_draw_post), face.c (face hook arm/disarm, per-slot face bracket)
│   ├── include/                 # wwapi.h (game types/addresses + the core services a C mod calls), mailbox_core.h (MAILBOX_ADDR, WWMB header,
│   │                            #   the Mailbox struct), mailbox_mp.h (MAX_PUPPETS, MAX_REMOTE_LINKS, MAILBOX_POSE_SLOT_CAP, Puppet)
│   └── build.py                 # Freighter wrapper — builds patched.dol from original.dol (C_SOURCES: one add_c_file per unit;
│                                #   devkitPPC from $DEVKITPPC, default C:\devkitpro\devkitPPC; also writes build/WWMultiplayer.map, T2's Dolphin symbol map)
├── cheats/
│   ├── GZLE01.ini               # Curated Gecko code pack, copied into <USER_DIR>/GameSettings by `dolphin2` and the app's Dolphin launch
│   └── cheats.go                # package cheats: GZLE01.ini embedded for both binaries
├── scripts/
│   ├── extract_blob.py          # Diffs original.dol vs patched.dol → internal/inject/blob.go
│   ├── gen_event_masks.py       # Generates mods/coop/class/syncmask_gen.go from the decomp
│   └── build-mac.sh, setup-mac-dolphin.sh   # macOS release build (WW Multiplayer.app + ditto zip); one-time Dolphin re-sign
├── packaging/
│   └── ww-multiplayer.exe.manifest  # Windows app manifest (DPI, Common Controls 6, asInvoker), in cmd/ww-multiplayer/ww_windows.syso via go generate
├── tools/
│   ├── blobstamp/               # Writes/checks internal/inject/blob.stamp
│   ├── gen_mailbox/             # inject/include/mailbox_core.h (+ mailbox_mp.h) -> wwapi/mailbox/layout_gen.go (go generate ./wwapi/mailbox)
│   └── mkicns/                  # docs/img/icon.ico -> docs/img/icon.icns (macOS app icon; go generate ./cmd/ww-multiplayer)
├── .github/workflows/
│   ├── build.yml                # CI: go vet + cross-build on push/PR; macOS job also builds + zips the .app (smoke, no publish)
│   ├── release.yml              # CI: build + release on tag push (exe, macOS zip, SHA256SUMS; secrets-gated signing); dispatch = dry run
│   └── pages.yml                # Website (site/) deploy: on release published / dispatch, from the latest release's tag
└── docs/                        # IMPORTANT - read before debugging
    ├── 01-architecture.md, 02-dolphin-memory.md, 03-code-injection.md
    ├── 04-ww-addresses.md, 05-known-issues.md, 06-history.md
    ├── 07-bettersunshine-research.md (Kuribo/BetterSunshineEngine notes, input to docs/api Phase E)
    ├── 08-coop-design.md, 09-player-colors.md, 10-lobby.md
```

## Commands

```bash
# Build (CLI + TUI only; no-args opens the TUI)
go build -o ww-multiplayer.exe ./cmd/ww-multiplayer
# Build with the app window, as releases do (Windows needs no cgo; cross-builds from Linux with CGO_ENABLED=0)
go build -tags desktop,production -o ww-multiplayer.exe ./cmd/ww-multiplayer
# Build wwdev, the developer tools (dolphin2, mp-local, diagnostics, research probes; never released)
go build -o wwdev.exe ./cmd/wwdev

# End-user entry points
./ww-multiplayer.exe                                    # Open the app window (the TUI in a build without -tags desktop,production)
./ww-multiplayer.exe tui                                # Launch TUI (host or join)
./ww-multiplayer.exe ui --browser [--port N] [--no-open] # Serve the app's UI on 127.0.0.1 to a browser (dev/tests; Ctrl+C stops)
#   tui and ui also take --mods=a,b / --no-mod=x / --dev (the app's mods this run; `mods` takes them to show the effect)
./ww-multiplayer.exe patch <iso|ciso> [out.iso]         # Splice mod into user's own vanilla WW ISO

# Play: one process per player
./ww-multiplayer.exe host [name] [--port N]             # Host a session (server + this player's pipelines)
./ww-multiplayer.exe join <host-ip[:port]> [name]       # Join a host's session

# Multiplayer runtime CLIs (the pipelines mp-local runs in one process; they also run as separate processes).
# wwdev is the developer tools (cmd/wwdev); `./wwdev.exe help` lists its commands.
./ww-multiplayer.exe server [--port N]                  # Headless TCP server on :25565
./ww-multiplayer.exe broadcast-pose <name> <addr>       # Stream this Dolphin's Link pose+pos to server
./ww-multiplayer.exe puppet-sync <name> <addr>          # Receive remotes; render them as Link #2 / actor puppets
./wwdev.exe broadcast-link <name> <addr>                # Position-only broadcast (cheaper; no pose)
./wwdev.exe pose-fake-loop <name> <addr>                # Loopback dev: capture pose once, stream as a fake remote
./wwdev.exe pose-test [mirror|freeze] [secs]            # Single-Dolphin sanity test for the pose pipeline

# Diagnostics
./wwdev.exe screenshot [path]                           # PNG of the selected Dolphin's window (Win32; default path = dolphin-<pid>-<ts>.png)
./wwdev.exe input <btns-hex> <stickX> <stickY> [ms=1000]   # Drive synthetic controller input via pad_read_shim. ms=0 holds until input-release.
./wwdev.exe input-release                               # Disable pad_read_shim override (zero input_enable). Pair with `input ... 0`.
./wwdev.exe auto-recapture [out=saves/start.sav]   # Cold-boot Dolphin + drive menus + prompt for one Shift+F1 + cp the new state. Win-only.
./wwdev.exe auto-recapture-pair [out1=saves/start.sav] [out2=saves/start2.sav] [delay-secs=5]   # Same as auto-recapture, but captures TWO states with a delay between (distinct btp phases). Pair with SAVE_STATE_2 in dolphin2 to validate face-sync (#5).
./wwdev.exe send-shift-f1                               # Diagnostic: probe whether your Dolphin build accepts synthetic Shift+F1 hotkeys
./ww-multiplayer.exe debug                              # Print Link's position for 5 sec
./wwdev.exe dump                                        # Dump mailbox state (WWMB header + contract verdict, shadow_mode, pose seqs, etc.)
./wwdev.exe shadow-mode <0|5>                           # 0=off, 5=pose-feed (the dev modes 1-4 were deleted in Phase E0)
./wwdev.exe poke-u32 <addr-hex> <val-hex>               # Direct memory write
./wwdev.exe move-puppet <x> <y> <z> [slot]              # Manually drive a puppet actor slot
./wwdev.exe unhide-puppet                               # Apply per-proc unhide poke (mSwitchNo / m678)

# Multi-Dolphin selection
WW_DOLPHIN_INDEX=<n>                        # Pick the Nth GZLE01 Dolphin process (0 = first)
WW_DOLPHIN_PID=<pid>                        # Pick a specific Dolphin PID
WW_SELF_NAME=<name>                         # puppet-sync filter for co-located broadcaster twins
WW_POSE_RAW=1                               # Skip pose localization (debug)
WW_LINK2_OFFSET_{X,Y,Z}                     # Loopback render offset (default 0)
WW_COLOR=<color>                            # Default tunic color when --color isn't given (docs/09)
WW_TUNIC=off                                # Pose pipeline (puppet-sync, host/join) leaves the tunic slots to the tunic-slot dev command
WW_MINIMAP_SCALE=<f>                        # TUI minimap world units per cell (default 10)
WW_LISTEN_HOST=<addr>                       # App only: a hosted lobby's server listens on <addr> alone (local checks: 127.0.0.1)
                                            #   and LAN discovery stays on it (127.0.0.1: beacons sent to, and heard on, loopback only)
WW_LAN_PORT=<n>                             # App only: LAN discovery's UDP beacon port (default 25565; docs/10-lobby.md §10)
WW_LAN=off                                  # App only: no LAN discovery (no beacon, no listening)
WW_HEARTBEAT_SECS / WW_READ_TIMEOUT_SECS / WW_BACKOFF_MAX_SECS / WW_DIAL_TIMEOUT_SECS /
  WW_MIN_SESSION_SECS / WW_WRITE_TIMEOUT_SECS / WW_FREEZE_SECS / WW_DESPAWN_SECS
                                            # Network timing knobs; defaults 5/15/8/5/1/10/2/5 s (mods/multiplayer/net/netconfig.go)

# Local two-Dolphin harness
./wwdev.exe dolphin2 [--reset]              # Bootstrap & launch a 2nd Dolphin instance
./wwdev.exe mp-local [A] [B]                # Server + 2x broadcast-pose + 2x puppet-sync (one process)
# Env knobs honored by `dolphin2` and `auto-recapture` (defaults: wwapi/emu/launch Defaults;
# DOLPHIN_EXE, ISO_PATH and USER_DIR_1 also override the app's saved settings, as WW_COLOR does):
#   DOLPHIN_EXE   path to Dolphin (Windows default: %USERPROFILE%\Desktop\Dolphin-x64\Dolphin.exe;
#                 macOS: ~/Applications/Dolphin.app/Contents/MacOS/Dolphin, else /Applications/...; Linux: dolphin-emu)
#   ISO_PATH      path to patched ISO  (Windows default: %USERPROFILE%\Desktop\Dolphin-x64\Roms\WW_Multiplayer_Patched.iso;
#                 elsewhere: ~/Roms/WW_Multiplayer_Patched.iso)
#   USER_DIR_1    primary Dolphin user dir (Windows default: %APPDATA%\Dolphin Emulator; macOS: ~/Library/Application Support/Dolphin)
#   USER_DIR_2    second Dolphin user dir  (Windows default: %APPDATA%\Dolphin Emulator 2; macOS: .../Dolphin 2)
#                 (co-op's card backups read USER_DIR_1/2 too)
#   SAVE_STATE    .sav loaded into both Dolphins (skips menus). If unset, both boot to title screen.
#   SAVE_STATE_2  .sav loaded into Dolphin 2 only (requires SAVE_STATE). Produces distinct btp phases for face-sync visual proof (#5).
```

## Build pipeline (C side → ww-multiplayer.exe)

```bash
# Build the C side (inject/src, inject/include). build.py removes each
# unit's old object itself. devkitPPC comes from $DEVKITPPC (default
# C:\devkitpro\devkitPPC; the installer's MSYS form /opt/devkitpro/... is
# read as C:\devkitpro/...). Needs inject/original.dol (the vanilla DOL,
# untracked). Also writes inject/build/WWMultiplayer.map, T2's symbols as a
# Dolphin map (Symbols > Load Other Map File).
cd inject && python build.py && cd ..

# Regenerate the embedded blob the patcher uses:
python scripts/extract_blob.py
# (Reads inject/{original,patched}.dol, writes internal/inject/blob.go,
# then runs `go run ./tools/blobstamp` to rewrite internal/inject/blob.stamp.)
go build -o ww-multiplayer.exe ./cmd/ww-multiplayer && go build -o wwdev.exe ./cmd/wwdev

# Patch a disc with the new blob (the patcher players use; it replaced
# inject/patch_iso.py), then quit + relaunch Dolphin on it:
./ww-multiplayer.exe patch <vanilla.iso|ciso> <out.iso>
```

**Releases and the website.** A `v*` tag publishes `ww-multiplayer.exe`,
`ww-multiplayer-macos.zip` and `SHA256SUMS` (release.yml). Signing steps
run only when their secrets exist (README, "Releases, code signing and
the website"); without them the assets are unsigned and nothing fails.
`site/` describes the latest release, so pages.yml deploys it only when a
release is published (release.yml starts it) or by hand, never on a push
to main: a `site/` edit on main is live only after the next release.
After changing `docs/img/icon.ico` or `packaging/ww-multiplayer.exe.manifest`,
run `go generate ./cmd/ww-multiplayer` (needs `rsrc`) to rebuild
`cmd/ww-multiplayer/ww_windows.syso` and
`docs/img/icon.icns`.

`internal/inject/blob.go` is committed and treated as source for build
purposes. CI on push/PR doesn't rebuild the C side; tag releases include
the latest committed blob, so **regenerate blob.go before tagging** if
the C side (inject/src, inject/include) has changed since the last release.

CI checks the committed blob against the committed C sources on every PR
(`internal/inject/blobstamp`, `internal/inject/t2_budget_test.go`,
`wwapi/mailbox/layout_test.go`): `blob.stamp` records the hashes of
`inject/src`, `inject/include` and `inject/build.py` that blob.go was built
from, so any edit there, comments included, fails CI until the blob is
rebuilt and restamped; T2 must end below `MAILBOX_ADDR`; build.py's
inject address and OSInit patch must match blob.go; the Go mailbox
offsets (`wwapi/mailbox/layout_gen.go`, generated from mailbox_core.h and
the mailbox_mp.h it includes) must be fresh: after a layout change in either
run `go generate ./wwapi/mailbox`.
Restamp only via extract_blob.py after a real rebuild, never to silence
the check. A PR that touches those C
inputs, even comment-only, must carry the restamp in the same PR: the
owner rebuilds and runs extract_blob.py, and for a comment-only edit
confirms the `blob` payload line in blob.stamp is unchanged (only the
`input` lines move). A rebase alone cannot turn a stale stamp green.

## Critical Knowledge

**Read `docs/` before starting any debugging session.** The docs capture hard-won learnings including:

- **docs/02-dolphin-memory.md** — Modern Dolphin (64-bit) memory scanner, address translation
- **docs/03-code-injection.md** — What works and what doesn't for runtime code injection
- **docs/05-known-issues.md** — The JIT cache wall we hit, and approaches tried
- **docs/04-ww-addresses.md** — Wind Waker GZLE01 addresses we've mapped
- **docs/api/00-design.md** — Approved modding API plan (wwapi/, mods, phases A-E) and the T2/mailbox RAM map

## Non-Obvious Gotchas

1. **Dolphin caches INI files at startup.** Edits to `GZLE01.ini` require restarting Dolphin entirely, not just the game. This wasted hours of debugging.
2. **Dolphin has dual memory mappings.** Writes via `WriteProcessMemory` don't always align with what the JIT reads. Reads generally work for game-written data, but writes to unused memory regions may not be visible to the JIT.
3. **OnFrame patches don't write to code sections.** AR codes can write there but don't invalidate the JIT cache. Only Gecko C2 hooks properly invalidate JIT for code changes.
4. **BSS zeroing overlaps with injected sections past the DOL end.** Putting code at `0x803FCF20+` fails because game initialization zeros that region.
5. **CISO files have block boundaries.** The Wind Waker DOL spans 2 blocks in the CISO — a naive patcher will corrupt block 2.

## Related Project

The old C# Windwaker-coop (progress sync only) was upgraded to .NET 9 with WPF UI and released as v0.8.0 at `StephenSHorton/Windwaker-coop`. This Go project supersedes it but the memory layout knowledge carried over.

## Working autonomously

**Claude can run the full stack end-to-end without asking the user.** The
ONE remaining human-in-loop step is *one keystroke* (Shift+F1) when
recapturing `saves/start.sav` after a C-blob change — kicked off by
`./wwdev.exe auto-recapture`, which handles everything else
(kill, boot, menu navigation, screenshot, file watch, cp). Everything
else — memory probing, chain dumping, building, patching, launching
Dolphins, running the multiplayer pipeline, **visually validating
renders via `./wwdev.exe screenshot`**, **driving Link with
`./wwdev.exe input`**, and tearing it all down — is scripted.

### Standard session bootstrap (no save-state cycle needed)

```bash
# Launch both Dolphins with the existing save state. Returns immediately;
# Dolphins boot in the background.
SAVE_STATE=$(pwd)/saves/start.sav ./wwdev.exe dolphin2

# Start the multiplayer pipeline (server + 2x broadcast + 2x puppet-sync
# in one process). Blocks; run with run_in_background. Its readiness gate
# waits until both Dolphins are in-game, then prints
# "Local multiplayer running." and the pipeline is live.
./wwdev.exe mp-local
```

After "Local multiplayer running." appears, every Go-side diagnostic
(`eye-fix-gates`, `eye-fix-chain`, `j3dsys-probe`, `dump`, `peek`,
`ppc-disasm` against live Dolphin, etc.) works against the running
Dolphins. Use `WW_DOLPHIN_INDEX=0` / `=1` to pick which Dolphin to
talk to — index 0 = the first PID found, index 1 = the second.

For static-code analysis (disassembling the original DOL), set
`WW_DOL_PATH=inject/original.dol` so `ppc-disasm` reads from the file —
no Dolphin needed at all.

### Tearing down

`mp-local` runs in the foreground (or as a background task). A single
Ctrl+C stops the pipeline cleanly and resets `shadow_mode` on both
Dolphins. The Dolphin processes themselves keep running unless you kill
them — that's intentional, so you can re-run `mp-local` without rebooting.

### Save-state ↔ C-blob coupling (the one manual step)

Dolphin save states snapshot the entire PPC RAM, including our injected
mod blob at `0x80410000+`. Any change to `inject/src` or
`inject/include` (which triggers a blob regen via `python
build.py`) invalidates `saves/start.sav` — loading the old state restores
the OLD blob over the freshly-patched ISO's new code.

After a C-side change, the recapture cycle reduces to:

1. Claude: rebuild the blob and re-patch the ISO (`cd inject && python build.py && cd .. && python scripts/extract_blob.py && go build -o ww-multiplayer.exe ./cmd/ww-multiplayer && go build -o wwdev.exe ./cmd/wwdev && ./ww-multiplayer.exe patch <vanilla.iso|ciso> <ISO_PATH>`)
2. Claude: `./wwdev.exe auto-recapture`. This kills running Dolphins,
   cold-boots the patched ISO, drives the title/intro/file-select menus to
   in-game via input injection, screenshots the spawn spot, then prompts the
   user with a clearly-fenced message and watches `<USER_DIR>/StateSaves/GZLE01.s01`
   for up to 90 s.
3. **User**: focus the Dolphin window, press Shift+F1 once. Auto-recapture
   detects the file write and copies it to `saves/start.sav`.
4. Claude: relaunch with `SAVE_STATE=$(pwd)/saves/start.sav` for the next iterations.

Only step 3 needs the user, and only for one keystroke — menu navigation is
fully automated. Pure Go-side changes don't invalidate the save state, so
skip the whole cycle.

**Why is the Shift+F1 still manual?** Mainline Dolphin's hotkey handler
filters synthetic input (`LLKHF_INJECTED`) for hotkeys, so PostMessage,
SendInput-with-VK, SendInput-with-scancode, and foreground-locked variants
all silently drop. Some Dolphin forks accept synthetic events — run
`./wwdev.exe send-shift-f1` while Dolphin's in-game to probe your
build; if mtime advances, auto-recapture will run fully unattended.

### Visual validation (now self-serve)

Memory reads can lie under Dolphin's dual mapping. **Don't claim a
rendering test succeeded based only on memory reads.** Instead, capture
the actual frame and look at it yourself:

```bash
WW_DOLPHIN_INDEX=0 ./wwdev.exe screenshot /tmp/ww-A.png
WW_DOLPHIN_INDEX=1 ./wwdev.exe screenshot /tmp/ww-B.png
# then Read the PNGs back into your context
```

`screenshot` uses Win32 `PrintWindow` with `PW_RENDERFULLCONTENT`, so
DirectX 12 / Vulkan backends are captured correctly (no black PNGs).
For A/B comparisons (eye decals, mini-link visibility, leg morph, TEV
tint, the #3 flicker, etc.) take one screenshot before the change, one
after, and diff them by eye. Only ask the user to look manually if
something about the capture itself is broken — that's the new bar.

For non-rendering tests (mailbox state, position sync, network protocol),
memory reads + structured diagnostics are usually sufficient — no
screenshot needed.

### Driving the game with input

`input` writes a synthetic PADStatus to the mailbox; the C-side
`pad_read_shim` (hooked over `bl PADRead` at 0x802C39A0 inside
`JUTGamePad::read`) overwrites `mPadStatus[0]` each frame while
`input_enable` is set. Button masks: A=0x100 B=0x200 X=0x400 Y=0x800
Start=0x1000 Z=0x10 R=0x20 L=0x40 DPad L=1 R=2 D=4 U=8. Stick X/Y are
signed bytes (-128..+127). Don't write to `0x803ED84A` directly — that's
`mPadButton[0]+2`, a downstream cache the game ignores for live input
(only AR/Gecko *conditions* read it).

Typical scripted-walk pattern:
```bash
WW_DOLPHIN_INDEX=0 ./wwdev.exe input 0x0000 0 100 2000            # stick forward 2s
WW_DOLPHIN_INDEX=0 ./wwdev.exe input 0x0000 100 0 1000            # stick right 1s
```

Menu navigation from a cold ISO boot also works (intro skip with Start,
file select with A); see how `auto-recapture` builds save states from
scratch.

## Testing reference

- Memory tests require Dolphin running with Wind Waker (GZLE01) loaded.
- For visual tests, see "Visual validation" above — capture via `screenshot` and look at the PNG yourself.
- **Two-Dolphin loop** is the default test pattern (see "Standard session
  bootstrap" above). When both Links overlap at start (both Dolphins
  booted from one save state), Dolphin B's Link is moved by (+50, 0, +50)
  from where it stands so the two players don't visually overlap. It moves
  only when both are in the same stage, layer and room, idle, and within 5
  units, and never under `--coop`. Override via `MP_LOCAL_SHIFT_X/Y/Z`
  env, or set all three to 0 to disable.
- **Diagnostic toolkit** (`find-pos`, `scan-pos`, `peek`, `poke-vec3`,
  `track-pos`, `warp`, `warp-force[-off]`,
  `eye-fix-gates`, `eye-fix-chain`, `eye-fix-find-shape`, `j3dsys-probe`,
  `ppc-disasm`) is available for memory probing when something doesn't
  behave as expected. They are wwdev's (`./wwdev.exe <command>`; the
  registry is cmd/wwdev/registry.go), kept out of the shipped
  ww-multiplayer.exe. `warp` stays in ww-multiplayer.exe.
