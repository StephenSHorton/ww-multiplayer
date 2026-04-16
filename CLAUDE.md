# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this project.

## Project Overview

**Wind Waker Multiplayer** — a Go-based tool that aims to enable real-time visual multiplayer in The Legend of Zelda: The Wind Waker on Dolphin emulator. Unlike the predecessor project (a progress-sync tool), this one targets actual player rendering so players can SEE each other in-game.

## Repository Structure

```
ww-multiplayer/
├── main.go                      # Entry point + debug CLI commands
├── internal/
│   ├── dolphin/                 # Dolphin process memory access
│   │   ├── memory.go            # Core read/write, RAM scanner
│   │   ├── inject.go            # Code injection constants
│   │   ├── inject_code.go       # Generated PPC machine code bytes
│   │   └── helpers.go
│   ├── network/                 # TCP server/client + protocol
│   │   ├── protocol.go
│   │   ├── server.go
│   │   └── client.go
│   └── tui/                     # Charm Bubble Tea UI
│       ├── app.go               # Screen router
│       ├── splash.go            # Animated Triforce splash
│       ├── connect.go           # Server/Client + IP config
│       ├── dashboard.go         # Live position + log + commands
│       └── styles.go
└── docs/                        # IMPORTANT - read before debugging
    ├── 01-architecture.md
    ├── 02-dolphin-memory.md
    ├── 03-code-injection.md
    ├── 04-ww-addresses.md
    ├── 05-known-issues.md
    └── 06-roadmap.md
```

## Commands

```bash
# Build
go build -o ww.exe .

# Launch TUI (for end users)
./ww.exe

# Debug commands (headless — useful for Claude)
./ww.exe debug         # Test memory access, print Link's position for 5 sec
./ww.exe server        # Headless TCP server on :25565
./ww.exe fake-client   # Connect a fake client that walks in circles
./ww.exe check         # Dump mailbox, BSS, player pointers
./ww.exe dump          # Dump memory at debug addresses
./ww.exe write-test    # Write 999 to rupees (proves memory write works)
./ww.exe teleport-test # Move Link up 500 units (proves position control)
./ww.exe inject        # Inject PPC code into Dolphin memory
./ww.exe scan-npcs     # Find NPCs near Link
./ww.exe help          # Show all commands
```

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
