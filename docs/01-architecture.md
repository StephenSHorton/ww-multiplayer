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

The front ends do not call the session code directly. They drive a **session controller** (`internal/session`), the UI-free owner of a session's state that the planned desktop app will drive too:

```
CLI host / join        TUI (internal/tui)        (later: GUI)
        │ commands: Host, Join, Leave, SendChat; co-op: Answer, Command, CommandLine
        │ out: Snapshot() State, Subscribe() <-chan Event, Wait(id) State
session.Controller (internal/session)
        │ Engine (+ CoopEngine) interface, injected by main
sessionEngine (session_engine.go) ── runHostSession / runJoinSession (+ liveState for the TUI)
```

- **Commands in.** `Host(ctx, HostOptions)` and `Join(ctx, JoinOptions)` start a session in the background and return its `ID` (one session at a time; `ErrBusy` otherwise). The options carry the port (host) and the co-op choices (`CoopOptions`, the `--coop` flags). `Leave` cancels it and returns once teardown is done (pipelines stopped, server unbound, mailbox reset), so the next Host can bind the port. `SendChat` forwards a line to the running session. `Wait(id)` blocks until a session has ended and returns its final state.
- **State out.** `Snapshot()` returns a copy of the current session's `State`: ID, phase (`Idle → InGame → Ended`), role, name, address, end reason, `Failed` (it ended on an error), its co-op options, its open co-op questions, and the live values (players, player count, host addresses, latency, own position, each co-op pipeline's `CoopStatus`) the engine reads from `liveState` and the co-op console. The dashboard polls it every 250 ms.
- **Events out.** `Subscribe()` gives each UI its own buffered channel of `Event`s: `EventLog` (every `report.Reporter` line), `EventChat`, `EventEnded` (with the end reason: the newest Err-level line, or "no error was reported"), `EventState`, and for co-op `EventQuestion`, `EventQuestionClosed` and `EventAdvice`. Every event carries the session `ID`, so a UI ignores late events from a session it has left. A full buffer drops its *oldest* event, so a slow UI never stalls broadcast-pose or puppet-sync and a session's last two events (`EventEnded`, then `EventState`) always get in (`New` keeps every buffer at 2 slots or more for that).
- **Engine.** `internal/session` imports neither `internal/dolphin` nor `internal/network`; package main injects the work as a `session.Engine`. `sessionEngine` runs `runHostSession` / `runJoinSession`, hands them a per-session `session.Output` (a `report.Reporter`, a chat sink, `Ask` and `Advise`) and reads `liveState` for `Live()`. An engine that also implements `session.CoopEngine` (`Command`, `CommandLine`, `CoopWorlds`) takes the co-op console verbs; the controller finds it by a type assertion, so an engine without co-op needn't. An engine error that the session already logged (a fatal connect error) wraps `ErrReported`: the controller doesn't log it again but still marks the session `Failed`. The controller is unit-tested on any OS with a fake engine.
- **Logging.** `report.Multi` fans one session's lines out to several reporters: the controller's event publisher and, optionally, `Options.Log` (stdout for the CLI, a log file for the GUI later).

#### Co-op through the controller

A `--coop` session's questions and console (docs/08-coop-design.md §5, §6.7; Appendix C items 64, 70, 162, 163, 164) go through two seams in package main:

- **Questions: `coopAsker`.** A co-op session asks its bind and scrub questions through a `coopAsker` (`question(ctx, rep, session.Question) bool`). A `session.Question` has the kind (bind or scrub), the player, a one-line title, the whole prompt as the CLI prints it, and a bind's preflight lines. Two askers exist:
  - `coopPrompter`, the CLI's: today's stdin prompter, unchanged (y/N, `--yes`, EOF or a closed input is no, the 50 ms settle, Appendix C item 64). Its reader also takes the console commands.
  - `outputAsker`, the TUI's (and the GUI's later): it logs the question as the CLI does, then calls `Output.Ask`, which opens the question in the controller (`EventQuestion`, `State.Questions`) and waits for `Controller.Answer(id, yes)`. The session ending is a no (`EventQuestionClosed`). stdin is never read.
  - The far-behind, needs-merge and no-backup advice is logged as before and also published as `EventAdvice` (`session.Advice`).
- **Console: `coopConsole.exec`.** The verbs (`/refresh`, `/warp`, `/coop off|on`, `/status`, `/help`) run in `coopConsole.exec`. A line typed on stdin reaches it through `do`, as before; `Controller.Command(session.Command)` and `Controller.CommandLine(line)` reach it through `sessionEngine`. `Command` returns the refusal that kept a command from every pipeline (none runs yet, no such player, which player?), which is also logged; `CommandLine` only logs it. `/status`'s text is `formatStatus`, which `Snapshot().CoopStatus` also carries for a UI.
- **Front ends.** `coopEnv.front` (a `coopFront`) says which asker a session uses, who hears its advice, and hands the session to the engine (`attach`) for the console commands. Its zero value is the CLI's.

**The CLI.** `host` and `join` run on a controller over `newCLIEngine()`: no `liveState`, `Options.Log` is `report.Stdout`, and the stdin prompter answers the questions. `runCLISession` waits for the session and exits 1 when it ended `Failed`. The output, prompts, flags and exit codes are those of the direct calls it replaced: `coop_golden_test.go` compares every co-op prompt and console flow with transcripts recorded before the change (`testdata/coop-golden`), and `TestRunCLISession` compares the driver with the old `logSessionErr` + `os.Exit(1)` path. `mp-local` (two Dolphins in one process) still runs its pipelines directly, with the same stdin prompter and console.

**The TUI.** The connect screen has a **Co-op** row (left/right): for a host Off, New world (from the file you load) or Continue *world* (from `CoopWorlds`, as `coop-world list` shows them); for a joiner Off or On. With co-op on, a dashboard line starting with `/` is a console command, the status panel gets a co-op line per pipeline, and an open question replaces the log panel until it is answered with `y` or `n`. A `y` or `n` counts only once the question has been shown, and the keyboard quiet, for 600 ms, so a key typed for the chat never answers it (Appendix C item 164). The dashboard's open questions are `Snapshot().Questions`, re-read on each status tick: a subscription may drop question events like any other.
