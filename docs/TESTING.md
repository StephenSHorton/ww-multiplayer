# Testing

How changes to this repo are checked: the automated matrix every pull request must pass, the golden files that pin what users see, the tests known to flake, and the checks that need two real Dolphins. Writing tests for a mod is in [api/03-writing-a-mod.md](api/03-writing-a-mod.md#6-testing-with-fakegame).

## The check matrix

Run all of it before pushing. CI (`.github/workflows/build.yml`) runs the same checks: a Linux runner vets and cross-builds the Windows target and runs vet and `go test -race` natively, and a macOS runner vets, tests and packages natively. The maintainers' pre-push script runs the table below in one go on Windows.

| Check | Command | Why |
|---|---|---|
| gofmt | `gofmt -l <changed .go files>` | Format the files you changed only. `internal/inject/dol.go` and `mods/multiplayer/net/backoff_test.go` are not gofmt-clean on main today: never run `gofmt -w` over them (or the module) wholesale, as it would bury your change in a reformat. |
| vet, Windows | `GOOS=windows GOARCH=amd64 go vet ./...` | `wwapi/emu` has a Win32 memory reader. |
| vet, Linux | `GOOS=linux GOARCH=amd64 go vet ./...` | |
| vet, macOS | `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet ./...` | The cgo memory reader (`wwapi/emu/memory_darwin.go`) is left out of a cgo-off cross-vet; the macOS CI job vets it natively. |
| vet, app window | `GOOS=windows GOARCH=amd64 go vet -tags desktop,production ./...` (and natively on macOS) | The release build compiles the Wails window (`internal/gui`); without the tags `internal/gui` is a stub. CI's `go-vet` action runs both vets on each target. |
| build | `go build ./cmd/ww-multiplayer` and `go build -tags desktop,production ./cmd/ww-multiplayer` | The CLI-only and the release build must both compile. |
| tests | `go test -count=1 ./...` | Everything, including the goldens below. |
| tests, race | `go test -race -count=1 ./...` | Needs cgo (a C compiler); the Linux CI job runs it on every push. |
| fuzz smoke | `go test -run '^$' -fuzz '^FuzzX$' -fuzztime 30s ./pkg` | For each fuzz target in a package you touched, e.g. `./wwapi:FuzzWatcherStep`. List them with `grep -rn "func Fuzz" --include=*_test.go`. |
| manifests | `go run ./tools/validate_manifests` and `go vet ./mods/_fixtures/` | Every `mods/**/mod.json` against the schema (CI's Build workflow). |

C changes (`inject/src`, `inject/include`, `inject/build.py`) also need a rebuilt and restamped blob; `go test ./internal/inject/... ./wwapi/mailbox/...` checks the stamp, the T2 budget and the mailbox layout (CLAUDE.md, "Build pipeline"). `inject/build.py` takes devkitPPC from `DEVKITPPC` (default `C:\devkitpro\devkitPPC`). For a live check of a rebuilt blob, patch a disc with `./ww-multiplayer.exe patch <vanilla> <out.iso>` (the old `inject/patch_iso.py` splicer is gone) and recapture the save states: a state taken on the old blob fails the strict contract check.

Never run the app or the CLI from a test or a check with your real settings: commands like `dolphin2` and the app write config. Point `APPDATA` and `LOCALAPPDATA` (or `XDG_CONFIG_HOME` / `HOME` elsewhere) at a temp directory first. The golden tests below do this for you (`configEnv`).

### Live-disc tests

No Nintendo data is in the repo, so a few tests read the user's own vanilla disc through `internal/vanillatest` and skip cleanly when there is none; `go test ./...` passes either way, and the disc is only ever read. Point `WW_VANILLA_DISC` at the vanilla ISO or CISO; unset, the conventional places are tried (`%USERPROFILE%\Desktop\Dolphin-x64\Roms\` and `~/Roms/`, the file named `Legend of Zelda, The - The Wind Waker (USA, Canada).ciso` or `.iso`). Run them on purpose with `-v` to see they ran rather than skipped:

```
WW_VANILLA_DISC=<path> go test -count=1 -v -run 'Vanilla' ./wwapi/stage ./wwapi/rel ./internal/inject/rels
```

What they pin: every REL in `RELS.arc` re-encodes with `stage.EncodeYaz0` to the exact bytes on the disc (`TestEncodeYaz0VanillaRELs`); `RELS.arc` parsed and saved unchanged by `stage.ArchiveEditor` is byte-identical, and with one REL replaced it parses and reads back (`TestArchiveEditorVanillaRELS`); every REL parsed and saved in place by `wwapi/rel` is byte-identical, repacks to the same module, and `f_pc_profile_lst.rel`'s relocation against the puppet module can be repointed in place (`TestVanillaRELs`); the disc's `RELS.arc` with a dummy REL in module 0x58's place (`rels.PatchRELSArc`) parses, reads the dummy back, has the profile list's entry repointed and every other entry byte-identical, and the ARAM growth guard refuses a guard under the growth (`TestPatchRELSArcVanilla`); the patcher's own path (`rels.PatchISOWithREL`, what `patch` and the app's Setup run) on the whole disc in memory: the output parses, its `RELS.arc` at the new offset parses, module 0x58 reads back as this build's REL with the Layout's prolog, the profile list is repointed, every other entry is the disc's, and patching the output again is `ErrAlreadyPatched` (`TestPatchISOWithRELVanilla`; it holds the decompressed disc and its patched copy, about 3 GB, and skips when `blob.go` has no REL).

## What the goldens pin

A golden file is the expected output, byte for byte. When a golden test fails, the diff is a change a user would see. Fix the code, unless the change is intended; then regenerate the file with the flag below, and say in the pull request which goldens changed and why. A refactor (a file move, a package split) must leave every golden unchanged.

### CLI and help goldens

`cmd/ww-multiplayer/cli_golden_test.go` and `mods_test.go` run `ww-multiplayer <args>` in a child process with its own config directory and Dolphin paths in temp dirs, and compare stdout and the exit code with `cmd/ww-multiplayer/testdata/cli-golden/<name>.txt`:

- `patch-*`: the patcher's lines for a good image, an already-patched one, another version, garbage, a missing file and the usage;
- `help-gui`, `help-nogui`, `unknown-*`: `help` and an unknown command, one file per build (with and without `-tags desktop,production`). They are recorded on Windows, where they compare byte for byte; elsewhere the column padding is ignored but not the text;
- `mods*`: the `mods` command and its flags (`mods`, `mods-json`, `mods-usage`, `mods-dev`, `mods-dev-needed`, `mods-no-multiplayer`, `mods-only`, `mods-unknown-mod`; `TestCLIGoldenMods` in `mods_test.go` drives them). `compiledMods()` lists multiplayer and coop; `mods/example` is in `devMods()` and shows only with `--dev`.

wwdev, the developer tools (`cmd/wwdev`), has its own: `cmd/wwdev/cli_golden_test.go` runs `wwdev <args>` the same way (the harness is `internal/cliutil/clitest`) against `cmd/wwdev/testdata/cli-golden/<name>.txt`:

- `dolphin2-*`: the two-Dolphin bootstrap's messages for each missing piece, `--reset`, and (Windows, no Dolphin running) the launch with `DOLPHIN_EXE` pointed at a directory, so nothing starts;
- `wwdev-none`, `wwdev-help`, `wwdev-unknown`: wwdev's help, with no arguments, `help` and an unknown command (Windows only).

Regenerate on purpose:

```
go test -run 'TestCLIGolden' -update-cli-golden ./cmd/ww-multiplayer
go test -tags desktop,production -run 'TestCLIGoldenHelp' -update-cli-golden ./cmd/ww-multiplayer   # help-gui
go test -run 'TestCLIGolden' -update-cli-golden ./cmd/wwdev                       # dolphin2-*, wwdev-*
```

Run both on Windows, so the help files keep Windows' columns. `cmd/ww-multiplayer/testdata/cli-subcommands.txt` pins the registry's subcommand names (`TestCLISubcommandsUnchanged`); regenerate it with `go test -run TestCLISubcommandsUnchanged -update-subcommands ./cmd/ww-multiplayer` only when a command is added or removed on purpose. wwdev's names are in `cmd/wwdev/testdata/cli-subcommands.txt` (`TestWWDevSubcommandsUnchanged`, `-update-subcommands ./cmd/wwdev`); the two lists share only `help`.

### Co-op golden and converge tests

- `mods/coop/session/golden_test.go` (`TestGolden*`) drives the co-op questions, offers and console flows and compares every logged line, with its level, with `mods/coop/session/testdata/coop-golden/*.txt`: the prompter, the offers (stdin and controller), and the console (stdin, host, two players, dry run, writing). Regenerate on purpose with `go test -run TestGolden -update-coop-golden ./mods/coop/session`
- `mods/coop/session/converge_test.go` (`TestCoopSessionConverges`, `TestCoopFreshBindRestart`) runs two fake games through a loopback session with out-of-order, repeated and dropped messages and a host restart, and checks invariants: the world is the merge of both games' shared progress, the replicas converge, clears never propagate, the only writes are the binding tags, and no per-player byte crosses the wire. There is no file to regenerate. A change to what they assert is a change to co-op's behaviour and needs the owner's M2-M6 rerun below.

A change that moves co-op code (the `mods/coop` packages, `wwapi/write`, `wwapi/state`, `wwapi/gate`) must leave both byte-identical: same golden files, same assertions.

## Known flakes

Two tests are known to fail now and then on timing, without a code change behind it. Rerun the job once. If it fails again, treat it as a real failure.

| Test | Where | Why |
|---|---|---|
| `TestControllerQuestions` (`mods/coop/session/controller_test.go`) | macOS CI | The co-op controller's question flow under a slow runner. |
| `TestLobbyKickClosesLobbyAndPlayerConns`, `TestLobbyKickSticks` (`mods/multiplayer/net`) | any | Loopback connections closing around the kick. |

Any other failure that passes on a rerun is a new flake: report it, do not just rerun it away.

## Live checks on two Dolphins

Unit tests cannot see a rendered frame. Changes to the pose pipeline, the puppets, the mailbox or the C side also need the owner's machine with the two-Dolphin harness (CLAUDE.md, "Standard session bootstrap"):

```
SAVE_STATE=$(pwd)/saves/start.sav ./wwdev.exe dolphin2
./wwdev.exe mp-local
```

Accept anything visual only on a screenshot pair (`WW_DOLPHIN_INDEX=0` and `=1`, `./wwdev.exe screenshot`), never on a memory readback.

### The smoke checklist (design decision 11)

After a change to the multiplayer pipeline (`mods/multiplayer/feed`, `wwapi/avatar`, `wwapi/player`, the tunic feed, the mailbox):

1. **Pose**: each Dolphin shows the other's Link where he stands, animating (walk, roll, swing), with no T-pose or frozen frame.
2. **Face**: the remote Link's eyes and mouth move (blinks, the look direction).
3. **Colors**: each remote Link wears his player's tunic color (`--color`), and stays in it after the steps below.
4. **Stage change**: one player goes through a loading zone. His Link leaves the other's screen, and both show again once they are back in the same stage.
5. **Leave and reset**: one player stops (Ctrl+C, or leaves the lobby). His Link disappears from the other's game within the despawn time, and `mp-local` on Ctrl+C resets `shadow_mode` on both Dolphins. A new session afterwards works without restarting Dolphin.

A third player (a `fake-client` or `pose-fake-loop` next to `mp-local`, drawn as an actor puppet or the second mini-Link) is not on the list: it currently freezes both Dolphins, on main too (F1 in [05-known-issues.md](05-known-issues.md)).

### Co-op M2-M6 rerun

After a change to co-op behaviour, or a move of its code that the goldens cannot fully cover (C13's `wwapi/write` split is still waiting for it), the owner reruns the co-op acceptance steps on two Dolphins with `mp-local --coop`, in the order of [08-coop-design.md §8.2](08-coop-design.md#82-owner-machine-two-dolphin-harness):

- **M2** (dry run, `--coop-dry-run`): binding and the planner's log;
- **M3** A1-A6 and the escapes R1-R4: chests, pickups, rooms, dungeon items, switches, ocean and charts;
- **M4** P1-P9: story bits, items, songs, capacities, keys, hearts and magic;
- **M5** Z1-Z6: live room puzzles;
- **M6.0** K1-K10: the kill union and the light-shrink.

Each section of §8.2 lists its fixtures (F1-F5 save states) and steps.

### Remote-Link Stage 4: the full bodies (S4)

After the Stage 4 REL lands (the real daPy_lk_c remote body; `ww-multiplayer-handoff/design/remote-link-stage4-plan.md` §6 has the detail), the owner runs these on the host Dolphin with `WW_ENEMY_REMOTE=step6`, adding `full_bodies` (or `step7`) where a step says so. The host uses a save with a sword (user-4, 15-StartET). The tools are `./wwdev.exe heap-free`, `remote-watch` (its `full`, `profile` and `shanm` lines and the FULL body read), `peek` and `screenshot`; the host's log prints the heap gate (`heap gate <reason> allow 0x..`) on every change and in its 5 s status line.

- [ ] **S4-0 boot, flag off**: DMC[0x27] == DMC[0xB5] and the profile list's slot 0x27 points at `g_profile_WWBODY` once the keeper links (`remote-watch` profile line); the shanm word is 0; body kind 0, FULL_ALLOW 0, HEAP_REASON 0; the Batch A Step 0-6 checks and S2/v2 LC1-LC4 pass unchanged; the Outset to A_mori hop keeps 'WWRL'.
- [ ] **S4-1 vanilla baseline**: no session, 5 stage hops; per hop the Game/Zelda/Archive largest and free and the seven counters (`heap-free`). Solid +1 per load, Game and Zelda +0.
- [ ] **S4-2 Outset, one create** (`+full_bodies`, one remote): GameHeap drop at most 0xCCF00, ZeldaHeap about 0x4C80, Archive about 0x4760 on the first create only; `remote-watch`: kind FULL, phase LIVE, vptr daPy_lk_c ok, small create_frames, heap_err 0, shanm words about 2598, create diff and joint diffs reported.
- [ ] **S4-3 globals untouched**: every plan §3.5 address equal before and after the create and the delete (`fullbody-globals snap before.json`, `snap after.json --wait full`, `snap del.json --wait empty`, then `diff before.json after.json` and `diff after.json del.json`, each with `--base` an idle snap; or `fullbody-globals watch` across the create and the delete; exit 0 PASS, 1 FAIL, 2 INVALID capture); the host Link is not dropped, his tunic texture and blink unchanged.
- [ ] **S4-4 entry variants**: as S4-3 after arriving by ship, after a void-out and after a respawn; the ship does not move, no startDemo 0xCB, event bits unchanged.
- [ ] **S4-5 M_Dai**: a Moblin spear hits the full body through its real mCyl (one hit ring entry, proc 0xBD) and the PHIT applies on the client; a guarding client gives mCurProc 0x0C on the body and the Moblin's guard recoil.
- [ ] **S4-6 visibility**: screenshots: no second Link model at the body's position, the mini-Link still drawn and posed, the host Link normal.
- [ ] **S4-7 Deku Leaf**: with the body LIVE and HITTABLE the leaf's wind lasts as without it; no fan sound from the body; no wind At at the world origin.
- [ ] **S4-8 delete and reload**: the remote leaves (DYING, then EMPTY), then 10 stage loads: no crash in `J3DJoint::entryIn`, shanm delete_scan 0, delete diff reported, GameHeap largest back to the S4-2 "before" value within one block.
- [ ] **S4-9 20 hops**: the remote follows the host over 20 hops (Outset, sea, A_mori, M_Dai): GameHeap largest does not trend down, archive largest stable after the first allocation, Game/Zelda counters flat, one FULL body per stage, kind FULL each time.
- [ ] **S4-10 fallback**: (a) `WW_ENEMY_REMOTE_HEAP=0xFFFFFFF,0,0`: a lite body with err heap_denied after the 120-frame wait; (b) a spawn over the void: phase_timeout, then lite; (c) `poke-u32 0x803B92B4` to another DMC pointer before the create (restored after): profile, then lite. No FULL retry in that stage; FULL again after the next stage load.
- [ ] **S4-11 flag flips**: clearing `full_bodies` mid-stage retires the FULL body cleanly (the S4-8 conditions), then lite; setting it again does not upgrade within the stage; the next stage gets FULL.
- [ ] **S4-12 regression**: `full_bodies` clear after all of the above: S4-0 again; the only heap change against the pre-Stage-4 baseline is the shanm table in the ArchiveHeap.
