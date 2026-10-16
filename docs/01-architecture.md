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

### Front ends and `internal/session`

Every subcommand is a CLI face, and running with no arguments opens the Bubble Tea TUI (`internal/tui`, brought back in v0.1.5 after v0.1.2 removed the v0.0 one):

- `ww-multiplayer.exe host` / `ww-multiplayer.exe join <ip>` — the user-facing multiplayer entry points (one process per player; signal handler resets the mailbox on Ctrl+C).
- `ww-multiplayer.exe server` / `broadcast-pose` / `puppet-sync` — lower-level building blocks used by `scripts/mplay2.sh` for two-Dolphin local harness.
- `ww-multiplayer.exe debug`, `ww-multiplayer.exe dump`, `ww-multiplayer.exe shadow-mode` — diagnostic CLIs. Plain-text output — good for piping into agents.

The TUI does not call the session code directly. It drives a **session controller** (`internal/session`), the UI-free owner of a session's state that the planned desktop app will drive too:

```
TUI (internal/tui)            (later: GUI)
        │ commands: Host, Join, Leave, SendChat
        │ out: Snapshot() State, Subscribe() <-chan Event
session.Controller (internal/session)
        │ Engine interface, injected by main
sessionEngine (session_engine.go) ── runHostSession / runJoinSession + liveState
```

- **Commands in.** `Host(ctx, HostOptions)` and `Join(ctx, JoinOptions)` start a session in the background and return its `ID` (one session at a time; `ErrBusy` otherwise). `Leave` cancels it and returns once teardown is done (pipelines stopped, server unbound, mailbox reset), so the next Host can bind the port. `SendChat` forwards a line to the running session.
- **State out.** `Snapshot()` returns a copy of the current session's `State`: ID, phase (`Idle → InGame → Ended`), role, name, address, end reason, and the live values (players, player count, host addresses, latency, own position) the engine reads from `liveState`. The dashboard polls it every 250 ms.
- **Events out.** `Subscribe()` gives each UI its own buffered channel of `Event`s: `EventLog` (every `report.Reporter` line), `EventChat`, `EventEnded` (with the end reason: the newest Err-level line, or "no error was reported") and `EventState`. Every event carries the session `ID`, so a UI ignores late events from a session it has left. A full buffer drops its *oldest* event, so a slow UI never stalls broadcast-pose or puppet-sync and a session's last two events (`EventEnded`, then `EventState`) always get in (`New` keeps every buffer at 2 slots or more for that).
- **Engine.** `internal/session` imports neither `internal/dolphin` nor `internal/network`; package main injects the work as a `session.Engine`. `sessionEngine` runs the same `runHostSession` / `runJoinSession` the CLI runs, hands them a per-session `session.Output` (a `report.Reporter` plus a chat sink) and reads `liveState` for `Live()`. The controller is unit-tested on any OS with a fake engine.
- **Logging.** `report.Multi` fans one session's lines out to several reporters: the controller's event publisher and, optionally, `Options.Log` (a log file for the GUI later).

The CLI subcommands still call the session funcs directly with `report.Stdout`, so their output and exit codes are unchanged. Co-op questions and the `/console` verbs move onto the controller in a later step, which is when the CLI can start going through it too.
