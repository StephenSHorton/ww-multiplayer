# Testing

How changes to this repo are checked: the automated matrix every pull request must pass, the golden files that pin what users see, the tests known to flake, and the checks that need two real Dolphins. Writing tests for a mod is in [api/03-writing-a-mod.md](api/03-writing-a-mod.md#6-testing-with-fakegame).

## The check matrix

Run all of it before pushing. CI (`.github/workflows/build.yml`) runs the same checks: a Linux runner vets and cross-builds the Windows target and runs vet and `go test -race` natively, and a macOS runner vets, tests and packages natively. The maintainers' pre-push script runs the table below in one go on Windows.

| Check | Command | Why |
|---|---|---|
| gofmt | `gofmt -l <changed .go files>` | Format the files you changed only. `main.go` is not gofmt-clean on main today: never run `gofmt -w main.go` wholesale, as it would bury your change in a reformat. |
| vet, Windows | `GOOS=windows GOARCH=amd64 go vet ./...` | `wwapi/emu` has a Win32 memory reader. |
| vet, Linux | `GOOS=linux GOARCH=amd64 go vet ./...` | |
| vet, macOS | `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet ./internal/...` | The macOS memory reader needs cgo, so a cross-vet with cgo off covers `internal/...`; the macOS CI job vets everything natively. |
| vet, app window | `GOOS=windows GOARCH=amd64 go vet -tags desktop,production ./...` (and natively on macOS) | The release build compiles the Wails window (`internal/gui`); without the tags `internal/gui` is a stub. CI's `go-vet` action runs both vets on each target. |
| build | `go build .` and `go build -tags desktop,production .` | The CLI-only and the release build must both compile. |
| tests | `go test -count=1 ./...` | Everything, including the goldens below. |
| tests, race | `go test -race -count=1 ./...` | Needs cgo (a C compiler); the Linux CI job runs it on every push. |
| fuzz smoke | `go test -run '^$' -fuzz '^FuzzX$' -fuzztime 30s ./pkg` | For each fuzz target in a package you touched, e.g. `./wwapi:FuzzWatcherStep`. List them with `grep -rn "func Fuzz" --include=*_test.go`. |
| manifests | `go run ./tools/validate_manifests` and `go vet ./mods/_fixtures/` | Every `mods/**/mod.json` against the schema (CI's Build workflow). |

C changes (`inject/src`, `inject/include`, `inject/build.py`) also need a rebuilt and restamped blob; `go test ./internal/inject/... ./wwapi/mailbox/...` checks the stamp, the T2 budget and the mailbox layout (CLAUDE.md, "Build pipeline").

Never run the app or the CLI from a test or a check with your real settings: commands like `dolphin2` and the app write config. Point `APPDATA` and `LOCALAPPDATA` (or `XDG_CONFIG_HOME` / `HOME` elsewhere) at a temp directory first. The golden tests below do this for you (`configEnv`).

## What the goldens pin

A golden file is the expected output, byte for byte. When a golden test fails, the diff is a change a user would see. Fix the code, unless the change is intended; then regenerate the file with the flag below, and say in the pull request which goldens changed and why. A refactor (a file move, a package split) must leave every golden unchanged.

### CLI and help goldens

`cli_golden_test.go` and `mods_test.go` run `ww-multiplayer <args>` in a child process with its own config directory and Dolphin paths in temp dirs, and compare stdout and the exit code with `testdata/cli-golden/<name>.txt`:

- `patch-*`: the patcher's lines for a good image, an already-patched one, another version, garbage, a missing file and the usage;
- `dolphin2-*`: the two-Dolphin bootstrap's messages for each missing piece, `--reset`, and (Windows, no Dolphin running) the launch with `DOLPHIN_EXE` pointed at a directory, so nothing starts;
- `help-gui`, `help-nogui`, `unknown-*`: `help` and an unknown command, one file per build (with and without `-tags desktop,production`). They are recorded on Windows, where they compare byte for byte; elsewhere the column padding is ignored but not the text;
- `mods`, `mods-json`, `mods-usage`: the `mods` command, which lists `compiledMods()` (empty today; `mods/example` is not in it).

Regenerate on purpose:

```
go test -run 'TestCLIGolden' -update-cli-golden .
go test -tags desktop,production -run 'TestCLIGoldenHelp' -update-cli-golden .   # help-gui
```

Run both on Windows, so the help files keep Windows' columns. `testdata/cli-subcommands.txt` pins the registry's subcommand names (`TestCLISubcommandsUnchanged`); regenerate it with `go test -run TestCLISubcommandsUnchanged -update-subcommands .` only when a command is added or removed on purpose.

### Co-op golden and converge tests

- `coop_golden_test.go` (`TestGolden*`) drives the co-op questions, offers and console flows and compares every logged line, with its level, with `testdata/coop-golden/*.txt`: the prompter, the offers (stdin and controller), and the console (stdin, host, two players, dry run, writing). Regenerate on purpose with `go test -run TestGolden -update-coop-golden .`
- `coop_converge_test.go` (`TestCoopSessionConverges`, `TestCoopFreshBindRestart`) runs two fake games through a loopback session with out-of-order, repeated and dropped messages and a host restart, and checks invariants: the world is the merge of both games' shared progress, the replicas converge, clears never propagate, the only writes are the binding tags, and no per-player byte crosses the wire. There is no file to regenerate. A change to what they assert is a change to co-op's behaviour and needs the owner's M2-M6 rerun below.

A change that moves co-op code (the `internal/coop` packages, `wwapi/write`, `wwapi/state`, `wwapi/gate`) must leave both byte-identical: same golden files, same assertions.

## Known flakes

Two tests are known to fail now and then on timing, without a code change behind it. Rerun the job once. If it fails again, treat it as a real failure.

| Test | Where | Why |
|---|---|---|
| `TestControllerQuestions` (`coop_controller_test.go`) | macOS CI | The co-op controller's question flow under a slow runner. |
| `TestLobbyKickClosesLobbyAndPlayerConns`, `TestLobbyKickSticks` (`mods/multiplayer/net`) | any | Loopback connections closing around the kick. |

Any other failure that passes on a rerun is a new flake: report it, do not just rerun it away.

## Live checks on two Dolphins

Unit tests cannot see a rendered frame. Changes to the pose pipeline, the puppets, the mailbox or the C side also need the owner's machine with the two-Dolphin harness (CLAUDE.md, "Standard session bootstrap"):

```
SAVE_STATE=$(pwd)/saves/start.sav ./ww-multiplayer.exe dolphin2
./ww-multiplayer.exe mp-local
```

Accept anything visual only on a screenshot pair (`WW_DOLPHIN_INDEX=0` and `=1`, `./ww-multiplayer.exe screenshot`), never on a memory readback.

### The smoke checklist (design decision 11)

After a change to the multiplayer pipeline (`runPuppetSyncCtx`, `wwapi/avatar`, `wwapi/player`, the tunic feed, the mailbox):

1. **Pose**: each Dolphin shows the other's Link where he stands, animating (walk, roll, swing), with no T-pose or frozen frame.
2. **Face**: the remote Link's eyes and mouth move (blinks, the look direction).
3. **Colors**: each remote Link wears his player's tunic color (`--color`), and stays in it after the steps below.
4. **Stage change**: one player goes through a loading zone. His Link leaves the other's screen, and both show again once they are back in the same stage.
5. **Leave and reset**: one player stops (Ctrl+C, or leaves the lobby). His Link disappears from the other's game within the despawn time, and `mp-local` on Ctrl+C resets `shadow_mode` on both Dolphins. A new session afterwards works without restarting Dolphin.

### Co-op M2-M6 rerun

After a change to co-op behaviour, or a move of its code that the goldens cannot fully cover (C13's `wwapi/write` split is still waiting for it), the owner reruns the co-op acceptance steps on two Dolphins with `mp-local --coop`, in the order of [08-coop-design.md §8.2](08-coop-design.md#82-owner-machine-two-dolphin-harness):

- **M2** (dry run, `--coop-dry-run`): binding and the planner's log;
- **M3** A1-A6 and the escapes R1-R4: chests, pickups, rooms, dungeon items, switches, ocean and charts;
- **M4** P1-P9: story bits, items, songs, capacities, keys, hearts and magic;
- **M5** Z1-Z6: live room puzzles;
- **M6.0** K1-K10: the kill union and the light-shrink.

Each section of §8.2 lists its fixtures (F1-F5 save states) and steps.
