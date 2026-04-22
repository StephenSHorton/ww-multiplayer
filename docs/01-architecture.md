# Architecture

## Goal

Enable two players to see each other in Wind Waker on Dolphin — each player runs their own Dolphin instance with their own save, and sees the other player's Link walking around in real-time.

## Three-Layer Design

```
┌──────────────────────────────────────────────────────────┐
│                    Player A's Machine                    │
│                                                          │
│  ┌─────────────┐     ┌──────────────┐     ┌──────────┐  │
│  │   Dolphin   │◄───►│  ww-mult Go  │◄───►│  Server  │  │
│  │ + WW (GZLE01)│    │              │     │(TCP:25565)│  │
│  └─────────────┘     └──────────────┘     └────┬─────┘  │
│         ▲                    │                 │        │
│         │                    │                 │        │
│         └─ ReadProcessMemory │                 │        │
│         └─ WriteProcessMemory│                 │        │
│                                                │        │
│                                                │        │
└────────────────────────────────────────────────┼────────┘
                                                 │
                                          TCP over LAN
                                                 │
┌────────────────────────────────────────────────┼────────┐
│                    Player B's Machine          │        │
│                                                 │        │
│  ┌─────────────┐     ┌──────────────┐         │        │
│  │   Dolphin   │◄───►│  ww-mult Go  │◄────────┘        │
│  │ + WW (GZLE01)│    │ (client mode)│                  │
│  └─────────────┘     └──────────────┘                  │
└──────────────────────────────────────────────────────────┘
```

## Data Flow

1. Each Go client reads its local player's position from Dolphin memory at ~60fps (50ms intervals).
2. Position data is sent to the server via TCP.
3. Server broadcasts position to all other clients.
4. Each client writes the received position to a "second Link" actor in its Dolphin memory.
5. The game renders the second actor at the received position, creating the illusion of another player.

## Components

### `internal/dolphin/`

Windows process memory access via `kernel32.dll` P/Invoke (ReadProcessMemory / WriteProcessMemory). Handles:
- Process discovery (find Dolphin.exe)
- Memory base scanning (locate emulated GameCube RAM in Dolphin's process — works on any Dolphin version including modern 64-bit builds)
- Big-endian / little-endian conversion (GameCube is big-endian, x86 is little-endian)
- Read/write at absolute GC addresses

### `internal/network/`

Simple TCP-based protocol with message framing. Message types:
- `J` (Join) — client announces name
- `W` (Welcome) — server assigns player ID
- `P` (Position) — 18-byte position update (3 floats + 3 int16s)
- `L` (PlayerList) — server announces who's connected
- `C` (Chat) — text messages

Server runs on port `25565`. UDP would be lower-latency for position data but TCP is fine for LAN play.

### No TUI

The v0.0 Bubble Tea TUI in `internal/tui/` was removed in v0.1.2 (it predated the pose-feed protocol and didn't actually engage the rendering pipeline, which had new users thinking the tool was broken). Everything is now CLI:

- `ww.exe host` / `ww.exe join <ip>` — the user-facing multiplayer entry points (one process per player; signal handler resets the mailbox on Ctrl+C).
- `ww.exe server` / `broadcast-pose` / `puppet-sync` — lower-level building blocks used by `scripts/mplay2.sh` for two-Dolphin local harness.
- `ww.exe debug`, `ww.exe dump`, `ww.exe shadow-mode` — diagnostic CLIs. Plain-text output — good for piping into agents.

A successor TUI built on top of `host` / `join` could land later (status panel + Ctrl+C-safe shutdown button); see `docs/06-roadmap.md`.
