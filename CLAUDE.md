# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this project.

## Project Overview

**Wind Waker Multiplayer** — a Go-based tool that enables real-time visual
multiplayer in The Legend of Zelda: The Wind Waker on Dolphin emulator.
Each player sees the other's actual Link in-game, walking around at the
remote's real world coords with their real animations, ~50ms latency on
LAN. Two-Dolphin local play is wired up via `scripts/mplay2.sh`.

The mod is shipped as a standalone `ww.exe` patcher (`./ww.exe patch
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
│   ├── inject/                  # Standalone ISO patcher (used by `./ww.exe patch`)
│   │   ├── blob.go              # AUTO-GENERATED via scripts/extract_blob.py
│   │   ├── dol.go               # DOL header editor + T2 splice + in-DOL patches
│   │   ├── iso.go               # ISO patcher with FST relocation
│   │   └── ciso.go              # CISO decompressor
│   ├── network/                 # TCP server/client + protocol
│   │   ├── protocol.go
│   │   ├── server.go
│   │   └── client.go
│   └── tui/                     # Charm Bubble Tea UI
│       ├── app.go, splash.go, connect.go, dashboard.go, styles.go
├── inject/                      # C source for the injected PPC code
│   ├── src/multiplayer.c        # The mod's C side
│   ├── include/{game,mailbox}.h
│   ├── build.py                 # Freighter wrapper — builds patched.dol from original.dol
│   └── patch_iso.py             # Local-dev ISO splicer (relies on `wit copy`-prepped ISO)
├── scripts/
│   └── extract_blob.py          # Diffs original.dol vs patched.dol → internal/inject/blob.go
├── .github/workflows/
│   ├── build.yml                # CI: go vet + cross-build on push/PR
│   └── release.yml              # CI: build + release on tag push
└── docs/                        # IMPORTANT - read before debugging
    ├── 01-architecture.md, 02-dolphin-memory.md, 03-code-injection.md
    ├── 04-ww-addresses.md, 05-known-issues.md, 06-roadmap.md
```

## Commands

```bash
# Build
go build -o ww.exe .

# End-user entry points
./ww.exe                                    # Launch TUI (host or join)
./ww.exe patch <iso|ciso> [out.iso]         # Splice mod into user's own vanilla WW ISO

# Multiplayer runtime CLIs (used by scripts/mplay2.sh)
./ww.exe server                             # Headless TCP server on :25565
./ww.exe broadcast-pose <name> <addr>       # Stream this Dolphin's Link pose+pos to server
./ww.exe puppet-sync <name> <addr>          # Receive remotes; render them as Link #2 / actor puppets
./ww.exe broadcast-link <name> <addr>       # Position-only broadcast (cheaper; no pose)
./ww.exe pose-fake-loop <name> <addr>       # Loopback dev: capture pose once, stream as a fake remote
./ww.exe pose-test [mirror|freeze] [secs]   # Single-Dolphin sanity test for the pose pipeline

# Diagnostics
./ww.exe debug                              # Print Link's position for 5 sec
./ww.exe dump                               # Dump mailbox state (shadow_mode, pose seqs, etc.)
./ww.exe check                              # Mailbox + player pointers + BSS sanity check
./ww.exe shadow-mode <0..5>                 # 0=off, 1/2=mirror dev, 3=freeze, 4=echo-ring, 5=pose-feed
./ww.exe echo-delay <N>                     # Mode-4 delay frames (for echo-link experiment)
./ww.exe poke-u32 <addr-hex> <val-hex>      # Direct memory write
./ww.exe scan-npcs                          # Find NPCs near Link
./ww.exe move-puppet <x> <y> <z> [slot]     # Manually drive a puppet actor slot
./ww.exe unhide-puppet                      # Apply per-proc unhide poke (mSwitchNo / m678)

# Multi-Dolphin selection
WW_DOLPHIN_INDEX=<n>                        # Pick the Nth GZLE01 Dolphin process (0 = first)
WW_DOLPHIN_PID=<pid>                        # Pick a specific Dolphin PID
WW_SELF_NAME=<name>                         # puppet-sync filter for co-located broadcaster twins
WW_POSE_RAW=1                               # Skip pose localization (debug)
WW_LINK2_OFFSET_{X,Y,Z}                     # Loopback render offset

# Local two-Dolphin harness
scripts/dolphin2.sh [--reset]               # Boot a 2nd Dolphin instance against the patched ISO
scripts/mplay2.sh                           # server + broadcast/puppet pairs for both Dolphins
```

## Build pipeline (C side → ww.exe)

```bash
# Iterate on multiplayer.c locally:
cd inject && rm -f build/temp/multiplayer.c.o && python build.py && python patch_iso.py
# Then quit + relaunch Dolphin to pick up the new patched ISO.

# When happy with C changes, regenerate the embedded blob the standalone
# patcher uses:
python scripts/extract_blob.py
# (Reads inject/{original,patched}.dol, writes internal/inject/blob.go.)
go build -o ww.exe .
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

## Testing

- Memory tests require Dolphin running with Wind Waker (GZLE01) loaded from a save file.
- Don't claim a test succeeded based only on memory reads — verify observable in-game effects (rupee count change, Link movement, etc.) since the dual-mapping issue can mask failures.
