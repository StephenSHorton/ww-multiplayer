# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this project.

## Project Overview

**Wind Waker Multiplayer** — a Go-based tool that enables real-time visual
multiplayer in The Legend of Zelda: The Wind Waker on Dolphin emulator.
Each player sees the other's actual Link in-game, walking around at the
remote's real world coords with their real animations, ~50ms latency on
LAN. Two-Dolphin local play is wired up via `scripts/mplay2.sh`.

The mod is shipped as a standalone `ww-multiplayer.exe` patcher (`./ww-multiplayer.exe patch
<vanilla.iso>` produces the patched ISO from the user's own legitimate
Wind Waker disc image). Releases are cut on tag push via GitHub Actions.

## Repository Structure

```
ww-multiplayer/
├── main.go                      # Entry point: TUI + all CLI subcommands
├── internal/
│   ├── dolphin/                 # Dolphin process memory access (Win32)
│   │   ├── memory.go            # Core read/write, RAM scanner
│   │   ├── inject.go            # LEGACY runtime injection (vestigial; superseded by inject/ patched-ISO approach)
│   │   ├── inject_code.go       # LEGACY PPC blob (vestigial)
│   │   └── helpers.go
│   ├── inject/                  # Standalone ISO patcher (used by `./ww-multiplayer.exe patch`)
│   │   ├── blob.go              # AUTO-GENERATED via scripts/extract_blob.py
│   │   ├── dol.go               # DOL header editor + T2 splice + in-DOL patches
│   │   ├── iso.go               # ISO patcher with FST relocation
│   │   └── ciso.go              # CISO decompressor
│   ├── network/                 # TCP server/client + protocol
│   │   ├── protocol.go
│   │   ├── server.go
│   │   └── client.go
│   ├── report/                  # Reporter interface (Stdout / Discard / TUI impls)
│   │   └── report.go
│   └── tui/                     # Charm Bubble Tea UI (resurrected v0.1.5)
│       ├── app.go, splash.go, connect.go, dashboard.go, session.go, styles.go
├── inject/                      # C source for the injected PPC code
│   ├── src/multiplayer.c        # The mod's C side
│   ├── include/{game,mailbox}.h
│   ├── build.py                 # Freighter wrapper — builds patched.dol from original.dol
│   └── patch_iso.py             # Local-dev ISO splicer (relies on `wit copy`-prepped ISO)
├── cheats/
│   └── GZLE01.ini               # Curated Gecko code pack, copied into <USER_DIR>/GameSettings by `dolphin2`
├── scripts/
│   └── extract_blob.py          # Diffs original.dol vs patched.dol → internal/inject/blob.go
├── .github/workflows/
│   ├── build.yml                # CI: go vet + cross-build on push/PR
│   └── release.yml              # CI: build + release on tag push
└── docs/                        # IMPORTANT - read before debugging
    ├── 01-architecture.md, 02-dolphin-memory.md, 03-code-injection.md
    ├── 04-ww-addresses.md, 05-known-issues.md, 06-history.md
```

## Commands

```bash
# Build
go build -o ww-multiplayer.exe .

# End-user entry points
./ww-multiplayer.exe                                    # Launch TUI (host or join)
./ww-multiplayer.exe patch <iso|ciso> [out.iso]         # Splice mod into user's own vanilla WW ISO

# Multiplayer runtime CLIs (used by scripts/mplay2.sh)
./ww-multiplayer.exe server                             # Headless TCP server on :25565
./ww-multiplayer.exe broadcast-pose <name> <addr>       # Stream this Dolphin's Link pose+pos to server
./ww-multiplayer.exe puppet-sync <name> <addr>          # Receive remotes; render them as Link #2 / actor puppets
./ww-multiplayer.exe broadcast-link <name> <addr>       # Position-only broadcast (cheaper; no pose)
./ww-multiplayer.exe pose-fake-loop <name> <addr>       # Loopback dev: capture pose once, stream as a fake remote
./ww-multiplayer.exe pose-test [mirror|freeze] [secs]   # Single-Dolphin sanity test for the pose pipeline

# Diagnostics
./ww-multiplayer.exe screenshot [path]                  # PNG of the selected Dolphin's window (Win32; default path = dolphin-<pid>-<ts>.png)
./ww-multiplayer.exe input <btns-hex> <stickX> <stickY> [ms=1000]   # Drive synthetic controller input via pad_read_shim. ms=0 holds until input-release.
./ww-multiplayer.exe input-release                      # Disable pad_read_shim override (zero input_enable). Pair with `input ... 0`.
./ww-multiplayer.exe auto-recapture [out=saves/start.sav]   # Cold-boot Dolphin + drive menus + prompt for one Shift+F1 + cp the new state. Win-only.
./ww-multiplayer.exe send-shift-f1                      # Diagnostic: probe whether your Dolphin build accepts synthetic Shift+F1 hotkeys
./ww-multiplayer.exe debug                              # Print Link's position for 5 sec
./ww-multiplayer.exe dump                               # Dump mailbox state (shadow_mode, pose seqs, etc.)
./ww-multiplayer.exe check                              # Mailbox + player pointers + BSS sanity check
./ww-multiplayer.exe shadow-mode <0..5>                 # 0=off, 1/2=mirror dev, 3=freeze, 4=echo-ring, 5=pose-feed
./ww-multiplayer.exe echo-delay <N>                     # Mode-4 delay frames (for echo-link experiment)
./ww-multiplayer.exe poke-u32 <addr-hex> <val-hex>      # Direct memory write
./ww-multiplayer.exe scan-npcs                          # Find NPCs near Link
./ww-multiplayer.exe move-puppet <x> <y> <z> [slot]     # Manually drive a puppet actor slot
./ww-multiplayer.exe unhide-puppet                      # Apply per-proc unhide poke (mSwitchNo / m678)

# Multi-Dolphin selection
WW_DOLPHIN_INDEX=<n>                        # Pick the Nth GZLE01 Dolphin process (0 = first)
WW_DOLPHIN_PID=<pid>                        # Pick a specific Dolphin PID
WW_SELF_NAME=<name>                         # puppet-sync filter for co-located broadcaster twins
WW_POSE_RAW=1                               # Skip pose localization (debug)
WW_LINK2_OFFSET_{X,Y,Z}                     # Loopback render offset

# Local two-Dolphin harness (v0.1.6+: Go-native, replaces the bash scripts)
./ww-multiplayer.exe dolphin2 [--reset]     # Bootstrap & launch a 2nd Dolphin instance
./ww-multiplayer.exe mp-local [A] [B]       # Server + 2x broadcast-pose + 2x puppet-sync (one process)
# Env knobs honored by `dolphin2`:
#   DOLPHIN_EXE   path to Dolphin.exe (default: C:\Users\4step\Desktop\Dolphin-x64\Dolphin.exe)
#   ISO_PATH      path to patched ISO  (default: ...\Roms\WW_Multiplayer_Patched.iso)
#   USER_DIR_1    primary Dolphin user dir (default: %APPDATA%\Dolphin Emulator)
#   USER_DIR_2    second Dolphin user dir  (default: %APPDATA%\Dolphin Emulator 2)

# Legacy bash scripts (kept for now; prefer the Go subcommands above)
scripts/dolphin2.sh [--reset]               # Same as `ww-multiplayer.exe dolphin2`
scripts/mplay2.sh                           # Same as `ww-multiplayer.exe mp-local`, but via 5 subprocesses
```

## Build pipeline (C side → ww-multiplayer.exe)

```bash
# Iterate on multiplayer.c locally:
cd inject && rm -f build/temp/multiplayer.c.o && python build.py && python patch_iso.py
# Then quit + relaunch Dolphin to pick up the new patched ISO.

# When happy with C changes, regenerate the embedded blob the standalone
# patcher uses:
python scripts/extract_blob.py
# (Reads inject/{original,patched}.dol, writes internal/inject/blob.go.)
go build -o ww-multiplayer.exe .
```

`internal/inject/blob.go` is committed and treated as source for build
purposes. CI on push/PR doesn't rebuild the C side; tag releases include
the latest committed blob, so **regenerate blob.go before tagging** if
multiplayer.c has changed since the last release.

## Critical Knowledge

**Read `docs/` before starting any debugging session.** The docs capture hard-won learnings including:

- **docs/02-dolphin-memory.md** — Modern Dolphin (64-bit) memory scanner, address translation
- **docs/03-code-injection.md** — What works and what doesn't for runtime code injection
- **docs/05-known-issues.md** — The JIT cache wall we hit, and approaches tried
- **docs/04-ww-addresses.md** — Wind Waker GZLE01 addresses we've mapped

## Non-Obvious Gotchas

1. **Dolphin caches INI files at startup.** Edits to `GZLE01.ini` require restarting Dolphin entirely, not just the game. This wasted hours of debugging.
2. **Dolphin has dual memory mappings.** Writes via `WriteProcessMemory` don't always align with what the JIT reads. Reads generally work for game-written data, but writes to unused memory regions may not be visible to the JIT.
3. **OnFrame patches don't write to code sections.** AR codes can write there but don't invalidate the JIT cache. Only Gecko C2 hooks properly invalidate JIT for code changes.
4. **BSS zeroing overlaps with injected sections past the DOL end.** Putting code at `0x803FCF20+` fails because game initialization zeros that region.
5. **CISO files have block boundaries.** The Wind Waker DOL spans 2 blocks in the CISO — a naive patcher will corrupt block 2.

## Related Project

The old C# Windwaker-coop (progress sync only) lives at `C:\Users\4step\Desktop\Windwaker-coop\`. It was upgraded to .NET 9 with WPF UI and released as v0.8.0 at `StephenSHorton/Windwaker-coop`. This Go project supersedes it but the memory layout knowledge carried over.

## Working autonomously

**Claude can run the full stack end-to-end without asking the user.** The
ONE remaining human-in-loop step is *one keystroke* (Shift+F1) when
recapturing `saves/start.sav` after a C-blob change — kicked off by
`./ww-multiplayer.exe auto-recapture`, which handles everything else
(kill, boot, menu navigation, screenshot, file watch, cp). Everything
else — memory probing, chain dumping, building, patching, launching
Dolphins, running the multiplayer pipeline, **visually validating
renders via `./ww-multiplayer.exe screenshot`**, **driving Link with
`./ww-multiplayer.exe input`**, and tearing it all down — is scripted.

### Standard session bootstrap (no save-state cycle needed)

```bash
# Launch both Dolphins with the existing save state. Returns immediately;
# Dolphins boot in the background.
SAVE_STATE=$(pwd)/saves/start.sav ./ww-multiplayer.exe dolphin2

# Start the multiplayer pipeline (server + 2x broadcast + 2x puppet-sync
# in one process). Blocks; run with run_in_background. Its readiness gate
# waits until both Dolphins are in-game, then prints
# "Local multiplayer running." and the pipeline is live.
./ww-multiplayer.exe mp-local
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
mod blob at `0x80410000+`. Any change to `inject/src/multiplayer.c` or
`inject/include/mailbox.h` (which triggers a blob regen via `python
build.py`) invalidates `saves/start.sav` — loading the old state restores
the OLD blob over the freshly-patched ISO's new code.

After a C-side change, the recapture cycle reduces to:

1. Claude: rebuild the blob (`cd inject && rm -f build/temp/multiplayer.c.o && python build.py && python patch_iso.py && cd .. && python scripts/extract_blob.py && go build -o ww-multiplayer.exe .`)
2. Claude: `./ww-multiplayer.exe auto-recapture`. This kills running Dolphins,
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
`./ww-multiplayer.exe send-shift-f1` while Dolphin's in-game to probe your
build; if mtime advances, auto-recapture will run fully unattended.

### Visual validation (now self-serve)

Memory reads can lie under Dolphin's dual mapping. **Don't claim a
rendering test succeeded based only on memory reads.** Instead, capture
the actual frame and look at it yourself:

```bash
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe screenshot /tmp/ww-A.png
WW_DOLPHIN_INDEX=1 ./ww-multiplayer.exe screenshot /tmp/ww-B.png
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
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe input 0x0000 0 100 2000   # stick forward 2s
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe input 0x0000 100 0 1000   # stick right 1s
```

Menu navigation from a cold ISO boot also works (intro skip with Start,
file select with A); see how `auto-recapture` builds save states from
scratch.

## Testing reference

- Memory tests require Dolphin running with Wind Waker (GZLE01) loaded.
- For visual tests, see "Visual validation" above — capture via `screenshot` and look at the PNG yourself.
- **Two-Dolphin loop** is the default test pattern (see "Standard session
  bootstrap" above). Dolphin B's Link gets warped by (+50, 0, +50) so the
  two players don't visually overlap. Override via
  `MP_LOCAL_SHIFT_X/Y/Z` env, or set all three to 0 to disable.
- **Diagnostic toolkit** (`find-pos`, `scan-pos`, `peek`, `poke-vec3`,
  `track-pos`, `warp`, `warp-force[-off]`, `eye-fix-step`,
  `eye-fix-gates`, `eye-fix-chain`, `eye-fix-find-shape`, `j3dsys-probe`,
  `ppc-disasm`) is available for memory probing when something doesn't
  behave as expected. See main.go's switch table; not in `printHelp` to
  keep the user-facing help clean.
