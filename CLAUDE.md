# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this project.

## Project Overview

**Wind Waker Multiplayer** — a Go-based tool that enables real-time visual
multiplayer in The Legend of Zelda: The Wind Waker on Dolphin emulator.
Each player sees the other's actual Link in-game, walking around at the
remote's real world coords with their real animations, ~50ms latency on
LAN. Two-Dolphin local play is wired up via `./wwdev.exe mp-local`
(see "Standard session bootstrap" below).

It ships as `ww-multiplayer.exe` (Windows) and `WW Multiplayer.app` (the
macOS zip). With no arguments it opens the app window: first-run Setup
patches the user's own legitimate Wind Waker disc image, then the lobby,
Start (launches each player's Dolphin), optional co-op worlds, LAN
discovery and join codes, and tunic colors. The same exe has the TUI
(`tui`), the CLI (`host`/`join`/`patch`, ...) and the mods (multiplayer,
coop) that run on the wwapi modding API (docs/api). Developer tools are a
separate `wwdev.exe`. Releases are cut on tag push via GitHub Actions.

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
│                                #   commands.go (the command bodies), mp_local*.go (dolphin2, mp-local), smoke*.go + smooth_steps.go (the scripted smoke, the step log), coop_diag.go (co-op diagnostics), auto_recapture.go,
│                                #   tunic_diag.go (tunic dev commands), probes.go (the research probes: peek/poke, disasm, eye/face, input, screenshot, dump, ...)
├── internal/
│   ├── applog/                  # Per-session log files (TUI/GUI), redaction, Copy-diagnostics text
│   ├── bugreport/               # The bug-report zip (the app's Bug report button, `bug-report`): newest session logs, dolphin.log, about.txt; all redacted, no game files/saves
│   ├── cliutil/                 # What both binaries' command lines share: FindDolphin/OpenDolphin (WW_DOLPHIN_INDEX/PID), ExeCmd, ExitUsage, --color/WW_COLOR; clitest/ = the CLI golden harness
│   ├── gamefiles/               # Spots Nintendo game files and saves (names, extensions, magic, zip entries): tools/check_game_files, bugreport
│   ├── config/                  # The app's saved settings (UserConfigDir/ww-multiplayer/config.json); the CLI never reads it
│   ├── gui/                     # The app window (Wails v2, -tags desktop,production only) + embedded HTML/CSS/JS frontend (sidebar pages Play/Session, Setup, Mods, Settings; light/dark theme.js); first-run Setup (setup.go); the Mods page (mods.go); the update banner + re-patch after an update (update.go); room.go: the host's rules mid-session (SetCoopShares over hub.SetFeatures), the Room items card, the Players row's co-op chip and Warp to; Bridge = the JS API; `ui --browser` server
│   ├── lan/                     # LAN discovery: the host's UDP beacon, broadcast addresses, the joiner's table (docs/10-lobby.md §10)
│   ├── mailboxgen/              # mailbox header parser (mailbox_core.h + the mailbox_mp.h it includes) + layout_gen.go emitter (used by tools/gen_mailbox and wwapi/mailbox TestMailboxLayout)
│   ├── inject/                  # Standalone ISO patcher (used by `./ww-multiplayer.exe patch`)
│   │   ├── blob.go              # AUTO-GENERATED via scripts/extract_blob.py
│   │   ├── dol.go               # DOL header editor + T2 splice + in-DOL patches
│   │   ├── iso.go               # ISO patcher with FST relocation (+ a replacement RELS.arc placed in free space, PatchISOWith)
│   │   ├── ciso.go              # CISO decompressor
│   │   └── rels/                # RELS.arc patcher: the mod's REL in an actor module's place, the profile list repointed, the 64 KB ARAM growth guard;
│   │                            #   optional in-place byte patches to vanilla RELs (seaheap.go: BytePatch, SeaHeapFix over rel.PatchBytes, checked by inject.CheckOverlaps)
│   ├── netutil/                 # Address parsing shared by host/join/server; join codes (WW-XXXX-XXXX, joincode.go)
│   ├── dolphintool/             # Dolphin's DolphinTool beside its Dolphin: probe `convert --help`, RVZ/WIA/GCZ -> temporary ISO (patch's Converter), free space
│   ├── patch/                   # `patch` as patch.Run (writes the patcher's lines, returns errors; convert.go: an RVZ/WIA/GCZ input converted first)
│   ├── redact/                  # IP masking for what the GUI and TUI draw (fixed-width •••.•••.•••.42); the CLI prints addresses unmasked
│   ├── session/                 # UI-free session controller the CLI host/join, the TUI and the GUI drive (docs/01-architecture.md)
│   │   └── session.go, controller.go, event.go, coop.go, coopapp.go (the app's co-op state), lobby.go, launch.go, mods.go (EventMod: the adapter over the mod runtime's bus)
│   ├── update/                  # One-click updates: latest-release check (semver, cache, dev/prerelease skip), verified download (SHA256SUMS, HTTPS, size caps), Windows exe / macOS bundle swap, relaunch, the CLI/TUI notice
│   └── tui/                     # Charm Bubble Tea UI (resurrected v0.1.5)
│       ├── app.go, splash.go, connect.go, dashboard.go, styles.go
├── wwapi/                       # Modding API, v0 UNSTABLE (docs/api): emu/ (Attach(Options); *emu.Dolphin is a MEM1-bounded mem.Mem; memory.go = RAM scanner; launch/ = Dolphin paths/detection, user dirs, detached launch, cheats, macOS prepare (dolphin2, auto-recapture); host/ = window, screenshot, keys),
│                                #   log/ (Reporter, Stdout / Discard / Multi / Prefix), mem/ (Reader/Mem, FakeMem), addr/ (cited addresses), state/ (per-tick snapshot), gate/ (write gates), actors/ (actor queue walk, Layout reads, structural Reread), write/ (guarded write engine: Allowlist, Breaker, FrameWriter beat wait + rechecks, Field, StageRequest; co-op's replica builds on it),
│                                #   disc/ (DiscFile, FileSource), stage/ (stage readers + stagetest), save/ (Image, Layout, View, item/slot table), savefile/ (GCI/card I/O), backup/ (card backups), tables/ (M1 disc table builders + cache), player/ (Link: Ptr/Ready/Position/DemoType/Pose/Face; Warp, WarpForce; input Override/Release/Press; playertest = fake warp handler), avatar/ (remote Links: pose-feed slot writes, Clear/Rearm, pose math, Tunics = tunic slots; avatartest = fake RAM + C tunic allocation), gfx/texture/ (CMPR tunic recolor, ResTIMG), crash/ (the JUTException crash record: OSContext, stack walk, REL module list, symbols.txt + T2 map lookup; wwdev crash-context), heap/ (JKRExpHeap free-list walk, the heap error counters, the full-body heap gate's thresholds; heaptest = fake heaps), mailbox/ (T2 + mailbox layout, WWMB header read, BlobHash; layout_gen.go is generated from mailbox_core.h (+ mailbox_mp.h) by tools/gen_mailbox), events/ (bus + core game events), commands/ (command registry, write gate), fakegame/ (deterministic fake game for tests), schema/, semver/; the mod runtime (manifest, resolver, supervisor); Game (Attach/NewGame: watcher, strict contract check: WWMB header + hash of the blob's read-only sections, Phase E1)
├── mods/                        # Compiled-in mods (docs/api §6): example/ (D3), _fixtures/ (runtime test mods)
│   ├── coop/                    # The co-op mod (D2c: mod.go + mod.json, requires multiplayer; CLI commands + the stage tables post-patch step). Co-op engine (docs/08): replica, hub, world, wire, derive, class, story, inventory, bind, ...; proto/ (the Feature bits, policies and scoped filters hub and replica share)
│   │                            #   combat/ (M6: K1 classes, layouts, gates, Revalidate over wwapi/actors), savemerge/ (offline save merge), synctables/ (sync verdicts and acceptance over wwapi/tables),
│   │                            #   roomitems/ (what a world image holds: items, songs, charts, bags, hearts; the app's Room items card via replica.Status.Room)
│   │                            #   session/ (package coopsession, D2b: the --coop session, console, puzzles, combat status, the app's co-op (app.go) and the engine seam (hook.go: Env, App/CLIApp);
│   │                            #   the co-op golden + converge tests, testdata/coop-golden), stagetables/ (+ stagetablestest/), procstate/ (session marker process check),
│   │                            #   diag/ (what coop_diag.go shares: Tick, GateSnapshots, SplitFlags, Where, WithMem, DiffSave/SaveLabel), bind/bindtest/ (a FakeGame with a bound file),
│   │                            #   cli/ (package coopcli: coop-tables, coop-tag, coop-world, save-diff, save-merge)
│   └── multiplayer/             # The multiplayer mod (D1): mod.go + mod.json (provides net, PublicAPI ValidatePlayerName), listed in compiledMods()
│       ├── engine/              # Session core: Engine (session.Engine: host/join/lobby, NewTUIEngine/NewCLIEngine), lobby.go, game.go (the app's Start: Dolphin + pipelines), LiveState, RunHostSession/RunJoinSession, co-op via the seam in coop.go (Coop, CoopApp, LobbyCoop, CoopSide), RunPipelines, server + fake client, color-pref clients; enginetest/ = fake Dolphins
│       ├── feed/                # Pose pipelines: BroadcastPose, PuppetSync (Feeder: link/actor slot assignment, lerp, arming over wwapi/avatar), PoseFakeLoop
│       ├── net/                 # package network: TCP server/client, protocol + handshake, lobby, co-op channel, timing knobs (netconfig.go)
│       ├── fxsync/              # Projectile events: the REL's outbox -> MsgFx (broadcast-pose), MsgFx -> its inbox, scoped to stage and room (puppet-sync)
│       ├── boatsync/            # Peer boats: this game's ship as a pose-trailer run (broadcast-pose), the remotes' boats into the REL's boats block (puppet-sync)
│       ├── playercolor/         # Tunic color palette + --color / WW_COLOR parsing (docs/09)
│       └── tunicsync/           # Session side of the tunic colors: SyncTunics, the appearance byte, the dump block (docs/09 §5.4)
├── inject/                      # C source for the injected PPC code (split into core + the multiplayer mod in Phase E3, docs/api §6.6)
│   ├── src/core/                # The core: hooks.c (main01_init, frame_shim + multiplayer_update, daPy_draw_hook, pad_read_shim, exec_shim, seachart_shim; the fixed call
│   │                            #   order written as a dispatcher), services.c (warp, tunic images + swap, face tev blocks + snapshot, pose publish),
│   │                            #   core.h (core-only decls), mods.h (the mod entry points the hooks call)
│   ├── src/mp/                  # The multiplayer mod: mp.h (PROC_* list, eye joints), puppets.c (actor puppets), minilink.c (mini-Links, pose feed,
│   │                            #   eye fix; mp_frame, mp_draw_post), face.c (face hook arm/disarm, per-slot face bracket)
│   ├── include/                 # wwapi.h (game types/addresses + the core services a C mod calls), mailbox_core.h (MAILBOX_ADDR, WWMB header,
│   │                            #   the Mailbox struct), mailbox_mp.h (MAX_PUPPETS, MAX_REMOTE_LINKS, MAILBOX_POSE_SLOT_CAP, MP_EYE_PACKETS_*, Puppet)
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
│   ├── mkicns/                  # docs/img/icon.ico -> docs/img/icon.icns (macOS app icon; go generate ./cmd/ww-multiplayer)
│   ├── validate_manifests/      # Checks the mods' mod.json manifests (CI runs it)
│   ├── check_game_files/        # Fails on a tracked game file or save (CI) and on any release asset holding one (release.yml)
│   └── uipreview/               # The app's frontend with a fake Bridge (fake.js scenarios, ?s=home|lobby-host|ingame|..., ?theme=light|dark) for looking and headless-Chrome screenshots (docs/img/app-ui; docs/TESTING.md "The app window")
├── site/                        # The website (index.html), deployed by pages.yml
├── .github/workflows/
│   ├── build.yml                # CI: go vet + cross-build on push/PR; macOS job also builds + zips the .app (smoke, no publish)
│   ├── release.yml              # CI: build + release on tag push (exe, macOS zip, SHA256SUMS; secrets-gated signing; body = docs/release-notes/<tag>.md via scripts/release-notes.sh); dispatch = dry run
│   └── pages.yml                # Website (site/) deploy: on release published / dispatch, from the latest release's tag
└── docs/                        # IMPORTANT - read before debugging
    ├── 01-architecture.md, 02-dolphin-memory.md, 03-code-injection.md
    ├── 04-ww-addresses.md, 05-known-issues.md, 06-history.md
    ├── 07-bettersunshine-research.md (Kuribo/BetterSunshineEngine notes, input to docs/api Phase E)
    ├── 08-coop-design.md, 09-player-colors.md, 10-lobby.md, 11-self-hosting.md (the dedicated server: Tailscale, port forwarding, owner key, status, Docker)
    ├── api/                     # the modding API: 00-design, 01-events, 02-mods, 03-writing-a-mod
    ├── release-notes/           # v<version>.md per release (the release body); README.md = how; a "<!-- draft" first line blocks the tag
    └── TESTING.md               # the pre-push check matrix, goldens, flakes, live checks
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
./ww-multiplayer.exe patch <iso|ciso|rvz> [out.iso]     # Splice mod into user's own vanilla WW ISO (RVZ/WIA/GCZ: converted first with the first usable DolphinTool beside a found Dolphin, running ones first)
#   --fix-sea-heap also gives d_a_lod_bg.rel the sea heap fix (rels.SeaHeapFix; off by default, local only, docs/TESTING.md)

# Play: one process per player
./ww-multiplayer.exe host [name] [--port N] [--color C] [--coop[=new|<world-id>]] ...  # Host a session (server + this player's pipelines)
./ww-multiplayer.exe join <host-ip[:port]|join-code> [name] [--color C] [--coop] ...   # Join a host's session (join code: WW-XXXX-XXXX[-XXXX])
./ww-multiplayer.exe bug-report [--include FILE]... [--user-dir DIR]... [--out DIR]   # One redacted zip of the app's session logs + dolphin.log (the GUI's Dolphin folder: launch.Detect + UserDir, then USER_DIR_2) for an issue
./ww-multiplayer.exe mods [--json] [--dev] [--mods=a,b] [--no-mod=x]   # List the mods built into this version
./ww-multiplayer.exe version                            # Print the version (players must match)
#   with --coop (host, join, wwdev mp-local; each side its own): --coop-boss-kill=gohma,kalle,gohdan,molgera,jalhalla,helmaroc|none
#   picks the boss-dead relay's rows (default gohma,kalle,molgera,jalhalla,helmaroc; gohdan (SirenB)
#   is opt-in until its live checks pass, docs/TESTING.md "Batch D"), and
#   --coop-refresh=stone2,leaves,item,tbox,saku,wall,floor,ice,mjdoor,mkie,mkiek,mknjd,vfan,swhit0,warpt,mflft,kokiie,vmc[,dry]|none
#   turns on actor_refresh (default off: nothing asked), which asks the REL to bring up to date an actor this game placed before the
#   partner's bit landed (mods/coop/replica refresh_relay.go): delete a boulder, bush, pickup, barricade, wall, floor, ice block or
#   statue, or delete and re-create in its set state a chest, crystal switch, warp jar, flame lift, flower house or soil mound
#   (review idea 11; door10/door12 are refused until the ST1 key-door check). dry logs one line per candidate and writes nothing.
#   docs/TESTING.md "Batch D" and "actor_refresh re-create".
#   The app sets both per player too (host and joiner): the save-file panel's "Advanced: your game's relays" fields
#   (gui.Bridge.SetCoopRelays, checked by the same replica parsers, saved as config.Coop.BossKill/Refresh, read at the next
#   co-op start through CoopFn); the TUI leaves them CLI-only.
# Co-op's hidden CLI (mods/coop/cli): coop-world, coop-tables, coop-tag, save-diff, save-merge

# Multiplayer runtime CLIs (the pipelines mp-local runs in one process; they also run as separate processes).
# wwdev is the developer tools (cmd/wwdev); `./wwdev.exe help` lists its commands.
./ww-multiplayer.exe server [--port N] [--owner-key K]  # Headless TCP server on :25565 (docs/11-self-hosting.md); the key (better WW_OWNER_KEY) lets an app joiner host its lobby; no co-op there
./ww-multiplayer.exe status <host[:port]|code> [--json] [--timeout D]  # The status probe: is a server up, its version (exit 0 up, 1 unreachable or not a status answer, 3 an older ww-multiplayer or another protocol); the Dockerfile's HEALTHCHECK
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
./wwdev.exe crash-context                               # After a JUTException screen: OSError, registers, 20-frame stack, REL modules, named via the decomp's symbols.txt
                                                        #   (WW_TWW_SYMBOLS, default ~/projects/tww/config/GZLE01/symbols.txt), T2's map (WW_T2_MAP, default inject/build/WWMultiplayer.map) or REL module+offset
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
WW_NAMETAGS=off / WW_EQUIP=off / WW_SEACHART=off / WW_HAIR=off   # puppet-sync (mods/multiplayer/vissync): no name tags over the mini-Links / no remote
                                            #   sword, shield (hero's or mirror) and scabbard (hero's or Master Sword's) on them / no remotes on the pause menu's
                                            #   sea chart / no katsura hair on a casual remote (default all on; the REL draws them, wwvis.h)
WW_BOATS=off|nosail|noparts                 # puppet-sync (mods/multiplayer/boatsync): no peer boats / no sail cloth / no cannon or crane on them. Default on:
                                            #   each remote's King of Red Lions drawn under it while it sails and where it was left (sea, Tower of the Gods), sail
                                            #   in its tunic colour (inject/rel/boats.c, include/wwboat.h; docs/05 "Peer boats", docs/TESTING.md BT-*)
WW_PROJECTILES=on                           # broadcast-pose and puppet-sync (mods/multiplayer/fxsync): the other players' bombs, boat-cannon shots and arrows
                                            #   are made in this game as world-only copies (inject/rel/fx.c, include/wwfx.h; MsgFx, protocol 6). Default off until
                                            #   docs/TESTING.md "Projectiles" passes; set it on both sides
WW_ENEMY_SYNC=0                             # Enemy sync off (mods/enemysync, the enemy mirror: Moblins, Bokoblins, ChuChus, Keese; Armos Knights, Armos, Darknuts, Kargarocs, Magtails opt-in).
                                            #   Default on: host/join and the app run enemy-host / enemy-client, mp-local spawns enemy-A (host, Dolphin A) and
                                            #   enemy-B (client, Dolphin B); needs the REL module linked. A fatal enemy error (a reject, a panic) turns enemies off
                                            #   until the pipelines next start; pose sync keeps running (engine Degradable)
WW_ENEMY_RAW=1                              # enemy-client bypasses the smoother and sets the block's raw flag (the lag measurement)
WW_ENEMY_STALE_HOLD=<dur>                   # enemy-client: how long past 500 ms a STATE that stopped coming (a stall) holds the copies frozen before
                                            #   they are released to this game's AI (a Go duration or ms; default 2.5s; 0 = release at once)
WW_ENEMY_COLLIDERS=0                        # enemy-host's STATE stops asking the clients to submit their copies' colliders (default on)
WW_ENEMY_HIT_V1=1                           # enemy-host applies a client's hit as the v1 shrink kill instead of the v2 sphere hit
WW_ENEMY_HIT_FALLBACK=shrink                # enemy-host: a v2 hit that crossed nothing after two Sets falls back to the shrink kill
WW_ENEMY_HIT_DEMO=0                         # enemy-host: no cutscene gate on client hits (default: a hit during the host Link's demo is dropped)
WW_ENEMY_MIRROR_KINDS=<kinds>               # Enemy kinds mirrored (mods/enemysync/kinds.go): mo2, bk, cc, ki (Keese), am (Armos Knight), am2 (Armos), tn (Darknut),
                                            #   bb (Kargaroc), mt (Magtail) joined by "," (default mo2,bk+u,cc+u,ki+u: am, am2, tn, bb, mt are off until named); a "+u"
                                            #   suffix (bk+u, ki+u, tn+u, bb+u, mt+u) also takes that kind's unkeyed (setID 0xFFFF) placements (ki+u: fly-type Keese only,
                                            #   never on GanonK; tn+u: the cave Darknuts; BOSS, behaviour 12..15 and caped Darknuts are always refused; bb: the keyed
                                            #   DRC perches, bb+u also the sea birds, a rider carrying a Moblin/Bokoblin (type 5/6) is always refused; mt+u: opt-in,
                                            #   every DRC Magtail is unkeyed, so mt alone takes none; batch D restored the row, kind 8, with the retail body
                                            #   builders behind a fingerprint, and it needs a REL of this build); "none". Host and client each set theirs: a kind is
                                            #   mirrored only when both enable it (the STATE's host kind mask AND the client's; u16 since batch C)
WW_ENEMY_QUIET_DEATH=<kinds>                # enemy-client: kinds whose copies die quietly (stay paused, shrink in the REL, deleted; the client sets the zone bit and
                                            #   death switch), default mo2,bk,cc,ki,am,am2,tn,bb,mt; bk,cc brings back the Moblin's legacy em_die (which drops an item
                                            #   on the client too); ki, am, am2, tn, bb and mt always die quietly (the REL forces it for kind ids >= 3)
WW_ENEMY_SELECT=legacy                      # host and client: the prototype's lowest-setID slot order (default distance: the host lists by player distance, each
                                            #   client keeps its 4 block slots with KEEP_R/ENTER_R/MIN_HOLD hysteresis)
WW_ENEMY_STATE_30HZ=1                       # enemy-host: send a STATE only when the host REL's frame counter moved (and at least every 250 ms)
WW_ENEMY_ANNEX=<kind>:<field>=<v>,...       # Enemy block overrides (mods/enemysync ParseOverrides; 0 = the REL's compiled value), each side its own block's:
                                            #   host tg_hit_up (s16, collider-2 centre height: BK 56, CC 50) and invuln (u8 hit-applier wait: CC 8); client quiet
                                            #   (u8 quiet-death frames: 20, 90 light) and hooks (hook_disable: a number or model_eq attach eye legacy_death attn
                                            #   mtx_plain special extra joined by +). mo2, bk, cc: the annex. ki, am, am2, tn: hooks in their annex bytes
                                            #   +0x1FC..+0x1FF; quiet, invuln and tg_hit_up in layout 4's knob table (0x80413750; batch C, e.g. ki:quiet=60).
                                            #   bb, mt: hooks in the block's bytes +0x28/+0x29 (0x80413128/9), quiet/invuln/tg_hit_up in the knob table.
                                            #   special = AM's bomb swallow and AM2's direct apply (am2:hooks=special on either side restores the old sphere
                                            #   path), extra = btk/brk play and collider radii. Per-kind names (docs/TESTING.md B1, batch C): tn shield (=model_eq)
                                            #   attach sword (=eye) coll (=legacy_death) attn adopt_anm (=mtx_plain); bk ambush (=special: the hidden ambusher's
                                            #   stick park and potted handback); bb tail (=attach) beak (=eye) mtx (=mtx_plain) handback (=special); mt coll
                                            #   (=model_eq) chain (=attach) fire (=eye, no reader since batch D) respawn (=legacy_death) mtx (the retail
                                            #   body step) custody (=special). tn:tg_hit_up=1..255 stays
                                            #   the host's u8 at +0x1FB (compiled 68). e.g. bk:tg_hit_up=60,cc:invuln=10,am:hooks=special,tn:hooks=sword+coll,
                                            #   bb:hooks=tail,ki:quiet=60; "none" = no overrides
WW_ENEMY_REMOTE=<flags>                     # Remote-Link block (mods/enemysync/players.go, design remote-link-design.md): unset = step6 with DefaultPolicy (ALLOW only
                                            #   the mirrored kinds' procs, WW_ENEMY_MIRROR_KINDS, plus the seed's DENY list); off, none, false or 0 = never written.
                                            #   An explicit <flags> keeps SeedPolicy (the dev rig's table).
                                            #   <flags> = a number (0x2F) or names joined by , or +: bodies swap hittable detect apply attrib name_a9 guard
                                            #   attacker group_wide both_words iframe_stamp full_bodies ff_gate_off rouya_gate_off, or a step preset step0..step7
                                            #   (design §10; each includes the ones before; step0 = the block with every flag clear; step7 = step6 + full_bodies,
                                            #   the real daPy_lk_c bodies of remote-link-stage4-plan.md, host only, behind the Go heap gate). Set it on the host
                                            #   (bodies, swap, detect, attrib, ...) and on each joiner (apply); the joiner's PSTATE is sent either way
WW_ENEMY_REMOTE_MARGIN / _MIN_HOLD / _IFRAMES / _COOLDOWN   # latch switch margin (q8, default 164), latch min hold (30 frames),
                                            #   body i-frames stamped on a hit (30), frames after a failed body create (30)
WW_ENEMY_REMOTE_DY_MAX / _EYE_DY / _CYL_R / _CYL_H          # f32: latch |dy| limit (400), body eye height (110), body Tg cylinder radius (30) and height (120)
WW_ENEMY_REMOTE_POLICY=<proc=flags,...>     # PROC_POLICY edits over the table WW_ENEMY_REMOTE chose (DefaultPolicy, or the seed: enemy audit, mods/enemysync SeedPolicy: ALLOW Moblin, Bokoblin, Darknut, ChuChu, warship, Kargaroc, Armos(+Knight), Keese, Molgera larva, Kalle Demos tentacle, Puppet Ganon 2-3, Stalfos +no_latch; DENY Floormaster, ReDead, Poe, vine, Orca, PG orbs, the bosses): e.g. 0x1D5=allow+no_latch,0x79=off;
                                            #   flags allow deny both_words no_latch, off removes; "none" alone = an empty table
WW_ENEMY_REMOTE_FF=<stage,...>              # FF stage denylist, at most 4 (default MajyuE,majroom,Majroom,Mjtower); "none" = empty
WW_ENEMY_REMOTE_HEAP=off|<game_largest>,<game_free>,<zelda_largest>   # full_bodies heap gate's size rules (default 0xD0090,0x10CF00,0x14C90; the
                                            #   archive rule 0x24760 stays while the shanm table is unallocated). off = no size rule (the counter, in-flight and
                                            #   FULL_MAX rules still hold); 0xFFFFFFF,0,0 shuts the gate (live check S4-10a)
WW_ENEMY_REMOTE_FULL_MAX=<n>                # FULL bodies LIVE at once before the gate holds (default 1)
WW_ENEMY_REMOTE_FLAGS2=<n|full_co>          # the block's FLAGS2 (host): full_co keeps the full bodies' mCyl Co Set bit (default 0: Co off)
WW_MINIMAP_SCALE=<f>                        # TUI minimap world units per cell (default 10)
WW_LISTEN_HOST=<addr>                       # App only: a hosted lobby's server listens on <addr> alone (local checks: 127.0.0.1)
                                            #   and LAN discovery stays on it (127.0.0.1: beacons sent to, and heard on, loopback only)
WW_LAN_PORT=<n>                             # App only: LAN discovery's UDP beacon port (default 25565; docs/10-lobby.md §10)
WW_LAN=off                                  # App only: no LAN discovery (no beacon, no listening)
WW_RELAY_SCOPE=off                          # Server (host, server, mp-local): no relay scoping (mods/multiplayer/net/place.go). Default on: a pose goes only to the
                                            #   puppet-sync connections in the sender's stage (by the place trailer on its positions), never to a broadcaster; an enemy
                                            #   STATE goes as its header alone to an enemy client whose PSTATE names another stage or room. Unknown places fail open
WW_OWNER_KEY=<key>                          # `server` only: the lobby owner key (16-256 bytes, trimmed; --owner-key wins; with neither, `server` makes a 128-bit one and prints it once). Only a member that sends the key
                                            #   (the app's Join form, "Advanced: dedicated server owner key") can host, loopback no longer does (docs/10-lobby.md §3)
WW_UPDATE_URL=<url>                         # Release API every update check asks (default: GitHub's latest release); tests use a fake server on 127.0.0.1
WW_NO_UPDATE_CHECK=1                        # CLI (host/join/version) and TUI: no update check (the app has its "Check for updates" switch, which the TUI follows too)
WW_HEARTBEAT_SECS / WW_READ_TIMEOUT_SECS / WW_BACKOFF_MAX_SECS / WW_DIAL_TIMEOUT_SECS /
  WW_MIN_SESSION_SECS / WW_WRITE_TIMEOUT_SECS / WW_FREEZE_SECS / WW_DESPAWN_SECS
                                            # Network timing knobs; defaults 5/15/8/5/1/10/2/5 s (mods/multiplayer/net/netconfig.go)

# Local two-Dolphin harness
./wwdev.exe dolphin2 [--reset]              # Bootstrap & launch a 2nd Dolphin instance
./wwdev.exe mp-local [A] [B]                # Server + 2x broadcast-pose + 2x puppet-sync (one process)
./wwdev.exe smoke [--no-boot] [--hop "<stage> <point> [room]"|none] [--tol 50] [--switch N] [--smooth <rate>] [-- mp-local args]
                                            # The scripted smoke (review idea 23, cmd/wwdev/smoke.go): boots two fixture Dolphins (SAVE_STATE + explicit
                                            #   test USER_DIR_1/2, WW_DOLPHIN_DEVLOG's rules) or attaches to two on test dirs, runs mp-local as a child, checks
                                            #   heartbeats, REL, crash, pose seqs, remote tracking after scripted walks, a stage hop, optional co-op bind/switch;
                                            #   logs/latest/smoke.json + smoke.log; exit 0 pass, 1 a check failed, 2 could not run (docs/TESTING.md SM1-SM4)
./wwdev.exe smooth-steps [secs=5] [A|B]     # Per-frame step log of a remote against the walking Link (stalls, jumps, cv, ratio); logs/latest/smooth-steps.json
#   WW_POSE_TEST_RATE=<hz>[:<jitter ms>[:<latency ms>]]  wwdev's broadcasts (mp-local, broadcast-pose) sample at hz and send each sample late
#                 by latency + up to jitter, in order (feed.TestRate; the shipped exe has no such knob); smoke --smooth sets it on its mp-local
#   MP_LOCAL_STOP_FILE=<path>  mp-local stops, with its normal teardown, once that file exists (smoke's child run)
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
#   WW_DOLPHIN_DEVLOG=1  dolphin2 / auto-recapture: each Dolphin logs OSReport, MI and MASTER to <user dir>/Logs/dolphin.log and never
#                 stops on a panic popup (launch.DevLogArgs: session-only -C, no ini written; the app never sets it). Refused unless
#                 USER_DIR_1 (and USER_DIR_2 for dolphin2) are set to test dirs that are no player's profile (launch.DevLogUserDirError)
#   WW_DEV_LOGS   dolphin2's launch record (dolphins.json) and mp-local's run logs root (default logs; off = none): mp-local writes
#                 logs/latest/ (mp-local.log, session.json manifest, dolphin-A/B.log) and moves the run before to logs/sessions/<start>/
#   MP_LOCAL_DOLPHIN_LOG=1  mp-local also prints each Dolphin's new dolphin.log lines as [dolphin-A] / [dolphin-B]
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
- **docs/05-known-issues.md** — Solved walls (the JIT cache wall, ...) plus open bugs (F-numbers, e.g. F1 third-remote freeze)
- **docs/08-coop-design.md** — Co-op worlds: design, milestones, as-built notes
- **docs/09-player-colors.md** — Per-player tunic colors and the casual shirt
- **docs/10-lobby.md** — The app lobby, LAN discovery, join codes, the owner key
- **docs/11-self-hosting.md** — The dedicated server: Tailscale, port forwarding, `status`, Docker
- **docs/TESTING.md** — The pre-push check matrix, goldens, flakes and live checks
- **docs/04-ww-addresses.md** — Wind Waker GZLE01 addresses we've mapped
- **docs/api/00-design.md** — Approved modding API plan (wwapi/, mods, phases A-E) and the T2/mailbox RAM map

## Non-Obvious Gotchas

1. **Dolphin caches INI files at startup.** Edits to `GZLE01.ini` require restarting Dolphin entirely, not just the game. This wasted hours of debugging.
2. **Dolphin has dual memory mappings.** Writes via `WriteProcessMemory` don't always align with what the JIT reads. Reads generally work for game-written data, but writes to unused memory regions may not be visible to the JIT.
3. **OnFrame patches don't write to code sections.** AR codes can write there but don't invalidate the JIT cache. Only Gecko C2 hooks properly invalidate JIT for code changes.
4. **BSS zeroing overlaps with injected sections past the DOL end.** Putting code at `0x803FCF20+` fails because game initialization zeros that region.
5. **CISO files have block boundaries.** The Wind Waker DOL spans 2 blocks in the CISO — a naive patcher will corrupt block 2.
6. **The patched disc runs with 48 MB of MEM1** (docs/05 "48 MB MEM1"): the patcher sets bi2.bin's simulated size and the DOL's heap plan, and every Dolphin launch passes the RAM override. A save state captured at the other size refuses to load, and pointer bounds are `addr.MEM1End` = 0x83000000, not 0x81800000.

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
**It does work when the Dolphin's hotkeys are bound to the keyboard**
(found 2026-10-25): with `Config/Hotkeys.ini` `Device = DInput/0/Keyboard Mouse`
in the user dir auto-recapture boots, the synthetic Shift+F1 lands and the
recapture needs nobody. With hotkeys on a controller (e.g. `Device =
SDL/0/Steam Controller`) the keystroke is ignored and the owner has to
press it. Set the hotkey device to the keyboard in the test user dir
(Controllers > Hotkey Settings) before a recapture.

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

See docs/TESTING.md for the full pre-push matrix.

- Memory tests require Dolphin running with Wind Waker (GZLE01) loaded.
- For visual tests, see "Visual validation" above — capture via `screenshot` and look at the PNG yourself.
- **Two-Dolphin loop** is the default test pattern (see "Standard session
  bootstrap" above). When both Links overlap at start (both Dolphins
  booted from one save state), Dolphin B's Link is moved by (+50, 0, +50)
  from where it stands so the two players don't visually overlap. It moves
  only when both are in the same stage, layer and room, idle, and within 5
  units, and never under `--coop`. Override via `MP_LOCAL_SHIFT_X/Y/Z`
  env, or set all three to 0 to disable.
- `MP_LOCAL_PIDS=<A>,<B>` pins which running Dolphin is A (the host of
  every pipeline that has one, e.g. enemy sync) and which is B; without it
  the process enumeration order decides.
- **Diagnostic toolkit** (`find-pos`, `scan-pos`, `peek`, `poke-vec3`,
  `track-pos`, `warp`, `warp-force[-off]`,
  `eye-fix-gates`, `eye-fix-chain`, `eye-fix-find-shape`, `j3dsys-probe`,
  `ppc-disasm`) is available for memory probing when something doesn't
  behave as expected. They are wwdev's (`./wwdev.exe <command>`; the
  registry is cmd/wwdev/registry.go), kept out of the shipped
  ww-multiplayer.exe. `warp` stays in ww-multiplayer.exe.
