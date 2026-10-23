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

### Enemy mirror: Keese, Armos Knight and Armos (A1)

The allow batch (`ww-multiplayer-handoff/design/enemy-mirror-allow.json`, batch A1) adds kind rows 3 KI, 4 AM and 5 AM2. They are off by default; a run turns them on with `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u,am+u,am2+u` on both sides.

Offline, before any live run (`go test -count=1 ./mods/enemysync ./wwapi/mailbox ./wwapi/addr ./internal/inject/... ./cmd/wwdev`):

- `TestKindTablePins`, `TestColliderOffPins` and `TestKindHash` (the pin is 0x97EDB0F9: a build from before A1 and one after refuse each other's STATE); `TestKindRows`, `TestAllowRows`, `TestStaticGates` (the allow kinds' LiveOK), `TestBckName`.
- `TestClassifyWalkAllow` and `TestClassifyWalkAllowLive`: an unkeyed Keese only with params low byte 1, never on GanonK; the live gates of R-8 (Keese mAction 2/30 or m2DC; Armos Knight ITAI from mMode 44; Armos carried, MODORU, in the abyss, or ITAI from mMode 22). `TestClassifyWalkAllowSticky` and `TestHostAllowSticky`: those gates refuse only a new enemy; one the host's previous walk listed stays listed through the swallow, the dance, the carry and FAIL (a carried Moblin is still refused).
- The samplers (`TestReadSampleKI*`, `TestReadSampleAM*`, `TestReadSampleAM2*`, five Reads, the Keese six) and `TestPKeyNoPosVector`: a keyed Keese's pkey hashes no home.pos, the same bytes as the REL's `EMK_PKEY_TV2_*` vector, for any home.
- `TestWireSlotDormantAllow` (WFlagDormant, never carried or dying), `TestForwardHit` and `TestMirrorReadHit` (the AM eye drops the Grappling Hook, the AM2 weak spot the Hookshot, the AM2 body forwards only the Skull Hammer), `TestParseAnnex` (`ki:hooks=extra` accepted; `ki:quiet=5` was refused until batch C's layout 4 took it into the knob table, `TestParseAnnexL4`) and the annex writers (`TestHostKinds`, `TestMirrorCatchUpPin`: the bytes at +0x1FC).
- `TestEnemyHeaderPins` and `TestEnemyBlockPlacement` (mailbox), `TestBlobFresh` and `TestT2EndsBelowMailbox` (the REL rebuild and restamp), and the REL's own static asserts (kind count 6 at A1, 8 since batch C; em_kind 0x44, PLAIN rows with FOPEN and a morf). RELS.arc growth stays under 0x10000.

Live, on the user-4 fixture: regression first with the default kinds (the #156-#158 Moblin, Bokoblin and ChuChu checks unchanged, a `bk:tg_hit_up` annex override still applies). Then per kind, at these verified hops (stage point room; `M_NewD2 0 4` does not exist and hangs the game):

- [ ] **KI**: `M_NewD2 0 7` (two Fire Keese, switch-hidden), `M_NewD2 7 4` (five keyed), `M_Dai 15 14` (six keyed), `Cave09 0 1` (ten unkeyed fliers, `ki+u`). The copy flies with the host's pitch and bank, a client sword hit kills it on the host and the copy shrinks quietly with no item; a woken wait-type Keese returns to the ceiling with no ERR_KEY churn; LUNGE shrinks the Tg to 20 and the Co to 0. The Fire Keese flame btk (R-10) has no verified hop: the `M_NewD2 0 7` pair stays switch-hidden (never drawn), and the other hops have plain Keese only. The disc's other Fire Keese rooms (M_NewD2 r2 and r15, Cave09 r12 and r13) need a verified hop before the flame can be checked; until then it is untested.
- [ ] **AM2**: `kaze 4 4` (seven keyed), `kaze 1 1`, `Cave09 0 7` (unkeyed), `Siren 0 8` (four Armos and two Keese). The kaze room 9 Armos are switch-inactive statues (params 0xC1010AFF). A back-stab takes two hits, then the dance, the host delete and the client's quiet death; one hammer hit kills; a front sword hit only clinks; a statue hands back as a statue.
- [ ] **AM**: `kaze 2 2`, `kaze 11 11`, `Cave09 0 8` (two unkeyed, asleep). The eye tracks; a client eye arrow stuns it on both sides; a light arrow kills it; a client bomb in the open mouth gives collider code 3 and the host swallow (44-47); a client Grappling Hook on the eye does nothing on the host; `WW_ENEMY_ANNEX=am:hooks=special` turns the swallow off live.
- [ ] **Mixed**: Siren room 8 (KI and AM2), kaze room 9 (three dormant Armos and ten ChuChus: WFlagDormant slot pressure), kaze room 11 (AM and four Bokoblins).


First run (REL 0x345EFD80, `ww-multiplayer-handoff/archive/enemy-allow-a1/`), item by item. The PR's review then fixed the AM swallow's stale mouth record and fuse checks (REL 0xCEC08E63); on that build the AM wake and copy, the AM sword hits and the Moblin kill were rerun and pass:

- Regression, default kinds: passed. M_Dai room 2 ChuChus adopt; M_Dai room 1 Moblin set 0 dies to five client hits (10 to 0), deleted on both, no client drop; 8 hops with every kind on: solid +1 per load, canaries flat apart from PauseReasserts, WatchdogReleases +1 per hop as on main.
- KI, fly type (`Cave09 0 1`, `ki+u`): passed. Identical pkeys, four copies fly with the host's, five client hits gave five ACKs and the host went from 10 Keese to 5, the copies vanished with no item and no rupees. Wait type (`M_NewD2 7 4`): pkeys match and the five ceiling Keese adopt, but none woke with the host Link under them, so the ceiling return and LUNGE are untested. Flame btk: untested (above).
- AM2 (`kaze 4 4`): passed with a caveat. A client weak-spot hit counts only when the host Link is behind the Armos too: `week_atari_check` (d_a_am2.cpp:209-212) measures the angle to player 0, the host Link, and the Armos faces the Link it chases, so a client back-stab on an Armos that faces the host is applied but ignored (ACK `applied_at`, health unchanged). With the host behind it, two client hits took set 6 from 2 to 0 and it was deleted on both sides with no client drop; the dance itself was not watched. A front sword hit is applied and does nothing; a sword hit on the body is filtered on the client. With `WW_ENEMY_REMOTE=step6` (swap) the check was inconclusive (no weak-spot hit landed on the copy). Hammer, statue handback: untested.
- AM (`kaze 2 2`): pkeys match, the knight wakes on the host and its copy matches its pose (screenshots), four client sword hits applied with health 10 unchanged. Eye arrow, light arrow, the bomb swallow (collider 3, `em_am_swallow_apply`), the Grappling Hook and `am:hooks=special`: untested (the client has no bombs or arrows on the fixture).
- Mixed: kaze room 9 passed (four ChuChus hold the slots, the three dormant Armos take none). Siren room 8 and kaze room 11: untested.

Rig: never warp a Link again while its void-out respawn runs (within about 10 s): Link sticks in its create (`actors` shows the player `init 1` at (0, 5, ~300), a black screen), with or without a session. Kill that Dolphin.

Every run: sweep both players' full Player.log, not only the reported issues, and confirm no adopt while gated.

### Enemy mirror: Darknut probes (B0)

The TN batch's first step (`ww-multiplayer-handoff/design/enemy-mirror-tn.json`, test_plan P0-b and P0-c) was Go only: the Darknut was a staged row (id 6), read by the probes and by nothing else. B1 (next section) made it row 6 of `Kinds`; the probes below work unchanged on the B1 build, except that `enemy-match` now refuses a Darknut the live gate refuses (`verdict=live-gate`), and `enemy-watch --kind tn` samples only the taken ones.

Offline: `TestTnOffsets` and `TestTnBckRange` (`wwapi/addr`), `TestTnLiveRefusal` and `TestReadSampleTN*` (`mods/enemysync`, five Reads), and `TestEnemyMatchDryTN` and `TestEnemyKindWatchTN` (`cmd/wwdev`).

Live, host only, on the current build (P0-b), in `kaze 8 8` and `Cave11 0 9`:

```
./wwdev.exe enemy-match --dry --kinds tn        # kaze r8: keyed; the unkeyed ones show unkeyed-off
./wwdev.exe enemy-match --dry --kinds tn+u      # Cave11 r9: the four unkeyed Darknuts
./wwdev.exe enemy-watch 0 --kind tn             # per tick: action (dr.mAction), mode, pieces, equip, m03F0, m140C, m1444, the sword
```

`enemy-match` prints the live gate after ` pid=` (`tn_action`, `tn_boss`, `tn_equip`, `tn_m02c1`, `tn_m1518`, `tn_m14f8`, `tn_m140c`, and `tn_live=ok` or the first refusal). B0 refused on placement and params (behaviour nibble 12..15) only; on B1 the gate refuses too. The sword line resolves `mBokoPcId` (0x1410) to its actor: pass when it is `proc 0x01D5`. Pieces (0x2D0) start at 0x0F with a shield (equipment 2 and up) and 0x0B without one (d_a_tn.cpp:4232-4236), then lose bit 1 (armor), 2 (helmet) or 4 (shield) as they break.

P0-c: every `enemy-watch --kind tn` walk line ends with `exec <daTn_Execute> part_move <exec-0x1040> yoroi_break <exec-0x224C>`, read as `*(*(actor+0xEC)+8)`. Dump the words there with the existing tools; no new command is needed:

```
./wwdev.exe ppc-disasm <part_move> 2            # expect an stwu r1,-N(r1) prologue
./wwdev.exe ppc-disasm <yoroi_break> 2
# or by hand from the actor address (enemy-watch's walk line, or `actors`):
./wwdev.exe peek <actor+0xEC>                   # the actor_method_class*
./wwdev.exe peek <method+0x8>                   # daTn_Execute
./wwdev.exe peek <exec-0x1040> 8                # two words (n is in decimal bytes)
```

Record the two words at each address for the batch C choice (design D6).

### Enemy mirror: Darknut row and Armos direct apply (B1)

The TN batch's B1 (`ww-multiplayer-handoff/design/enemy-mirror-tn.json`: D1-D14, R-1..R-16, test_plan P1 and T1-T6 with the P0 amendments; `archive/enemy-tn-b/P0-NOTES.md`) makes the Darknut kind row 6 (layout 3 holds it: the annex HKDX byte 0x1FF is id 6's; ids 7..14 have layout 4's block bytes since batch C) and adds the Armos (AM2) direct apply. TN is off by default; a run turns it on with `tn` (keyed: kaze r8, M_Dai r17) or `tn+u` (the cave Darknuts) in `WW_ENEMY_MIRROR_KINDS` on both sides. BOSS Darknuts (D3), behaviour 12..15 and caped ones (equipment 4 and up, D5) are refused.

Offline, before any live run (`go test -count=1 ./mods/enemysync ./wwapi/mailbox ./wwapi/addr ./internal/inject/... ./cmd/wwdev`):

- `TestKindTablePins`, `TestColliderOffPins`, `TestKindHash` (the pin was 0x0F3064EE at B1, 0x587E10C2 since batch C (0xB826567A before its MT cut): a build from before a pin change and one after refuse each other's STATE), `TestEnemyHeaderPins`, `TestEnemyBlockPlacement` (HD 18 direct, 19 front; the TN tg_hit_up byte 0x1FB; HAF 0x20/0x40/0x80 clear of 0x01..0x10), `TestBlobFresh` and `TestT2EndsBelowMailbox` (the REL rebuild and restamp), and the REL's own static asserts (kind count 7 at B1, 8 since batch C: 9 until its MT cut, id 8 now reserved).
- `TestKindTNRow` (row, sets, defaults: off in `DefaultMirrorKinds`, on in `DefaultQuietKinds`), `TestTnLiveRefusal`, `TestClassifyWalkTN` (the live gate refuses an unlisted Darknut; a listed one stays through S_DEMO, m14F8 and m140C but not BOSS), `TestReadSampleTNFlags` (DYING from dr.mAction 20..22, the ice shrink, or health <= 0 with dr.mMode <= -100; health 0 alone is not dying), `TestDormantPlaySkipTN`, `TestForwardHitTN` (head and Tg, everything but the Hookshot).
- AM2 direct apply: `TestHitClientFlags` and `TestMirrorReadHitVerdict` (the client's pending byte 0x02/0x04 into HIT byte 54), `TestHitClientFlagsWire`, `TestHitApplierTN` (TN never ATTRIB, D12; HAF_VERDICT/HAF_BACK only for AM2 collider 1, from the host slot's kind), `TestHitApplierAM2Crit` and `TestAm2CritPins` (HAF_CRIT: a crit sword cut or the hammer's side swing, d_a_am2.cpp:236-280).
- `TestParseAnnexTN` and `TestAnnexTnTgHitUpWritten` (`tn:tg_hit_up` reaches byte 0x1FB on both sides), `TestEnemyMatchDryTN` and `TestEnemyKindWatchTN` (`cmd/wwdev`).

`WW_ENEMY_ANNEX=tn:hooks=...` bisects the TN hooks on a client, byte 0x1FF (design R-3, D9). TN reuses the generic bits whose generic meaning is dead for it, under its own names (the generic names work too):

| bit | generic name | tn name | turns off |
|---|---|---|---|
| 0x01 | model_eq | shield | the shield morf pre-calc (AGUARD/ATATE_ON on the shield, the left arm's guard pose) |
| 0x02 | attach | attach | the pieces mask, the armor/helmet/shield parts, the armor morf play and calc, the defence sphere centre |
| 0x04 | eye | sword | the sword carry, its hand matrix, the HAS_WEAPON drop and the TN quiet drop |
| 0x08 | legacy_death | coll | the TN colliders case (shield bits by pieces, the helmet hit mark) |
| 0x10 | attn | attn | the per-frame attention write (generic) |
| 0x20 | mtx_plain | adopt_anm | the adopt-time armor YKIHON1 setAnm and the armor play |
| 0x40 | special | special | reserved for batch C (TN armor direct apply); no-op |
| 0x80 | extra | extra | not reused (generic code returns on it) |

The AM2 direct apply is switched by AM2's own `special` bit (byte 0x1FE): `WW_ENEMY_ANNEX=am2:hooks=special` on a client sends no verdict, on the host ignores one, and the hit takes the old sphere path either way (D10).

Rig (`ww-multiplayer-handoff/archive/`): host A user-4 (15-StartET card, sword), client B user-3 booted with `s2v2-live/boot_b_sword.sh`; `s2v2-live/healer.sh <bin> <pidA> <pidB>` on both PIDs for the whole run; `remote-link-live/warp_near.py` and `enemy-allow-a1/backstab.py` to place a Link. P1: one REL build, the restamp, then one Shift+F1 recapture (`./wwdev.exe auto-recapture`); `go test ./...` green first. Hop one room at a time (`./wwdev.exe stage-hop <stage> <point> <room> --yes`; it reads the stage from `ISO_PATH`'s disc and refuses a start point the room lacks, listing the valid ones, since `M_NewD2 0 4` hung the game on d_stage.cpp:1463's assert).

- [ ] **T1 kaze r8, adopt and look**: `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,tn` on both sides, `stage-hop kaze 8 8 --yes` on both. On B, `./wwdev.exe enemy-block-dump` (or the client log's slots line) shows three kind-6 slots; `WW_DOLPHIN_PID=<A> ./wwdev.exe enemy-match --dry --kinds tn` on A and on B diff clean before ` pid=`; nothing adopts while A's Darknut has m140C != 0 (`enemy-watch 0 --kind tn` on A). Screenshots of both: armor, helmet, shield and sword on the right joints, the left arm holding the shield, the shadow following the copy. The AGUARD shield follow is a rig gap (P0: the guard needs the host Link's cut while Z-locked; it holds only if Stephen plays the host). Bisect a wrong piece with B restarts: `tn:hooks=shield`, `attach`, `sword`, `coll`, `adopt_anm`.
- [ ] **T2 kaze r8, client hits**: B swings at an unguarded Darknut: the ACK's Health drops (the pass signal is the client ACK Health, never `applied_at` alone). B hits the front until the armor breaks: A's pieces go 0x0B -> 0x0A (or 0x0F -> 0x0E) and B's copy hides the armor when the bit clears, then streams the B* bcks (0x45..0x55, BRUN2, BSURI_SFB1). A back-stab from A with B watching: the copy's armor vanishes about 13 frames after the strike (no pop, no particles: design animation). A Tg-height miss is retuned live with `WW_ENEMY_ANNEX=tn:tg_hit_up=<n>` on A (R-13; 0 = the compiled 68). Guard-defence (A's Darknut in DEFENCE, action 10: the ACK's result is `parried` with detail `none`, from the defence sphere's Tg record on the v2 path; detail `defence`, 9, appears only under `WW_ENEMY_HIT_V1=1`) is a rig gap as in T1: it runs only if Stephen plays the host.
- [ ] **T3 kaze r8, quiet death (P0 death order)**: kill all three (some from A, some from B). Pass: at health 0 A's Darknut ragdolls (`enemy-watch` on A: mode -100, action 0, BDOWN_A1 then BDOWN_U1, about 1.6 s) and B's copy plays the same ragdoll at full size (it is DYING from the first ragdoll sample and holds scale through `EMK_TN_QUIET_HOLD`), then shrinks quietly; it is never handed back at health 0. Then `./wwdev.exe actors` on B shows no proc 0x00C0 and no orphan proc 0x01D5 sword; B's kill-all bars on switch 0x85 open (d_a_door12.cpp:154-170); death switches 0x86..0x88 are set on B; no miniboss sub-BGM change on either side (D4). Known gap: after a host sword drop and re-pickup the copy stays unarmed.
- [ ] **T4 caves, unkeyed**: `tn+u` on both sides, `stage-hop Cave11 0 9 --yes`: the four unkeyed Darknuts fill exactly the four slots; rerun the T1-T3 checks. Then `Cave10 0 16` with `mo2+u,tn+u` (two Darknuts among five enemies, four slots) last.
- [ ] **T5 AM2 direct apply**: `am2+u` both sides, `WW_ENEMY_REMOTE=off` (unset is now step6 with the default policy), `stage-hop Cave09 0 7 --yes`. B positions with `backstab.py <bin> <armos> 6 90 back|front` (WW_DOLPHIN_PID=B); A stands with `warp_near.py` (or `backstab.py ... 1 90 back|front` on A for A's own swing). Pass: B back + A front: Health drops by the atp, ACK detail `direct`. B back + A back: one drop, no double damage. B front + A back: no drop, detail `front` (the negative control the old path got wrong). B front + A front: no drop, `front`. A alone back-stabbing: vanilla, unchanged. After an arrow stun (MAHI): hittable; carried, or MAHI mode 15: detail `invuln`. A hammer hit kills (health 0, m2CE 7/8). A hit during the Armos's invuln is retried, not dropped. Then `kaze 4 4` keyed with A at z >= 2900 (z < 2900 is void). Then once with `WW_ENEMY_REMOTE=step6` on both: no double apply, and the `attacker_overrides` counter does not tick on a direct hit. Then `WW_ENEMY_ANNEX=am2:hooks=special` on both: the old path returns (a B back-stab with A in front is `applied_at` and ignored, as on A1). Accepted divergences (am2_fix): the ITAI knockback heads away from A's Link; the hit SE and hit-stop use A's cut count; no crit flash particle on A. Rig gap: `backstab.py` has no warp-only mode in the archive copy, so placing A behind without a swing is a hand `warp` plus the angle pokes from the script.
- [ ] **T6 regression**: the default kinds and then every kind on (`mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u`): the A1 fixture rooms (the kinds probe goldens, one kill each for MO2, BK, CC, KI, AM), a `bk:tg_hit_up` override still applying, 8 hops with canaries flat apart from PauseReasserts. Sweep both players' full Player.log and the mplocal logs afterwards, not only the reported issues, and confirm no adopt while gated.

### Co-op: the Gohma kill relay (B5, A9)

Each game fights its own Gohma (`ww-multiplayer-handoff/design/research/C3-C6-gohma.md`, "The relay op (B5, revised)"). When one player kills theirs, the world gets DRC's STAGE_BOSS_ENEMY, but a partner still in M_DragB keeps the bit held (bossHold, unchanged). Its replica instead writes the boss_kill request (`mods/coop/replica/bosskill.go`; 16 bytes at 0x80413410, `inject/include/wwboss.h`), and the REL (`inject/rel/bosskill.c`) gives that game's own Btd the lethal-hit death: s16 +0x2E4 = 0xB and s16 +0x2E6 = 0x32 (d_a_btd.cpp:463-475). Its own death demo then drops the floor, spawns the heart container, sets the bit and creates the warp flower. The REL applies it only once the receiver has broken its own Gohma's armor (u8 +0x2E0 m02E0 = 1, the only phase vanilla's lethal eye hit takes, d_a_btd.cpp:457): an armored Gohma answers `waiting`, so the partner's kill ends the receiver's fight at the eye phase, never on the armored model. The REL takes proc 0x00EB only in the stage named exactly `M_DragB` (and 0x00EC only in `kinBOSS`, the Kalle Demos section below), so never an X refight stage. Go asks only a REL of its own build (`inject.RELBuildID`); another build gets no request and one log line naming both ids. The receiver's log prints `boss_kill <seq>: <result> (m02E4 0x.., state 0x.., m02E0 n)` once per change of result, phase or action (the fight's m02E4 0..2 count as one), not once per seq: `waiting` and `no actor` are asked again every 500 ms, `none` (consumed at the keeper's create) too, `applied` and `already` not for 45 s.

Offline (`go test -count=1 ./mods/coop/replica ./wwapi/mailbox ./internal/inject/...`): `TestBossHeaderPins`, `TestBossRequestPlace` (the gap +0x510..+0x520 inside RemoteBlockRegion, outside the canary arenas), `TestBossReadAndFields`, `TestBossKillWanted` (only with the bit held and the game in M_DragB, a boss stage, with DRC loaded; never `Xboss0`; not once the local save has the bit), `TestBossGuardRules` (the guard takes the proc and tag only as a `bossKillRows` proc, 0x00EB or 0x00EC, and 'BK', on pathBoss alone), `TestBossKillRelay` (asked again while waiting and after a swallowed request, not during the death demo, stopped once the game has the bit), `TestBossKillNeverInRefight`, `TestBossKillNotWhenLocalHasIt`, `TestBossKillNoREL`, `TestBossKillWrongREL` (another REL build: no request, one line), `TestBossKillLogOnce` (an armored `waiting` with rising seqs and turning m02E4 logged once, the broken armor's again, `not acked` once), `TestBlobFresh`.

Rig: two Dolphins with `mp-local --coop` on one world, both players at the M_DragB boss door with the boss not yet killed on either file. Read the request and its answer with `WW_DOLPHIN_INDEX=<n> ./wwdev.exe peek 0x80413410 16` (seq, proc 00EB, tag 424B, ack, then the result byte, m02E4, mGohmaState and m02E0). Before the first live run: one REL build, the restamp and a recapture (the REL change rides in the same build as B1).

- [ ] **D4-0 boot**: on both Dolphins, before any request, ack equals seq at 0x80413410 (the keeper consumed whatever the slack held at its create); no `boss_kill` line in either log outside M_DragB (and kinBOSS).
- [ ] **D4-1a armored**: B in the fight with the armor intact (no rock hit yet, or one or two), A kills its Gohma. B's log: `waiting (m02E4 0x0..0x2, ..., m02E0 0)`, logged once (not once per seq) while B fights on; no death demo, BGM and materials unchanged. Drop the rock on B's Gohma meanwhile: the rock damage runs as vanilla. B breaks the armor: the next answer is `applied` with m02E0 1.
- [ ] **D4-1 forced from each pose**: B in the fight with the armor broken (m02E0 1, the eye phase), A kills its Gohma. B's log: `boss_kill N: applied`, and B's Gohma plays its death from wait, then (rerun) from attack and from the damage poses. Screenshot B: `end()` re-inits the DEAD bck, and the blend from a non-damage pose is unverified (design D4 (1)).
- [ ] **D4-2 receiver busy**: as D4-1 with B hanging on the grapple (Kui), then with B under Gohma's fire attack: the death runs, B is not stuck on the rope, no fire left burning on the boards.
- [ ] **D4-3 during the intro**: B is already in M_DragB and holds within 200 units of (-1534, 0, 909), which keeps Btd at m02E4 0xA without ordering its intro (d_a_btd.cpp:366-385), while A kills its Gohma; then B walks in. (B entering M_DragB after A's kill gets the bit live outside the boss stage, so no Gohma and no request: that is the Q6 path, not this check.) B's log shows `waiting (m02E4 0xA ...)` once, its intro plays out, then `waiting (m02E4 0x0.., m02E0 0)` through the armored fight, then `applied` once B breaks the armor. No death demo on a Gohma still at y -10000.
- [ ] **D4-4 the exit**: after D4-1 on B: the floor drops, the warp flower appears, the heart container spawns if STAGE_LIFE is clear (it does not if A already took A's), B's own save gets STAGE_BOSS_ENEMY (the planner's DRC hold line clears) and the flower warps B out (`Adanmae`).
- [ ] **D4-5 same frame**: both kill on the same frame (or within the demo): each log shows at most `already` and nothing applied twice; one heart container between the two files.
- [ ] **D4-6 never in a refight**: with the world's DRC boss bit set, stage-hop B to an X refight stage (`Xboss0`): no request (seq at 0x80413410 does not move), no `boss_kill` line.
- [ ] **D4-7 hop and recreate**: with a request answered `waiting`, hop B out of M_DragB and back. The keeper survives the hop (`inject/rel/README.md`, "Who creates the actor") and acks every request within a frame, so no `none` appears: the hold stays, the seq at 0x80413410 keeps rising, and after re-entry a new request gets its answer. `none` comes only from a keeper create with a seq in the slack: check it across a reset (D4-0's ack equals seq), not a hop.

Until a disc with this REL is in use, the playthrough rule stays: the second player waits outside M_DragB until the kill, then walks in and takes the warp; a player trapped inside dies and continues at the M_NewD2 entrance, or uses /warp.

### Co-op: the Kalle Demos kill relay (C1, B1, A3)

The same relay for Forbidden Woods (`ww-multiplayer-handoff/design/research/C1-kalle-demos-relay.md`): `bossKillRows` in `mods/coop/replica/bosskill.go` adds `{kinBOSS, table 4, proc 0x00EC}`. When the partner has FW's STAGE_BOSS_ENEMY and this game is in `kinBOSS` without it, the replica asks the REL for boss_kill with proc 0x00EC, and `inject/rel/bosskill.c` (`bk_bmd`) writes the whole lethal block of `core_damage_check` (d_a_bmd.cpp:609-617) into this game's own Bmd: s16 +0x306 m306 = 100, s16 +0xB74 mB74 = 100, s16 +0x310 m310 = 20000, u8 +0xBE0 mBE0 = 2, f32 +0xBDC mBDC = 1.0f, s16 +0x302 m302 = 0, s16 +0x300 mMode = 0xB, then `zel_basic->bgmStop(30, 0)` (0x802A4658, `this` from 0x803F7710). It applies only at the fallen, open flower: mMode 3 at m302 2..5, m304 != 0, m310 0, mB74 0 (m304 can be left over from the last close at m302 0..1, so m302 is gated too); everything else answers `waiting`. Only in the stage named exactly `kinBOSS`, never an X stage. The result word for this proc is mMode, m302, m304, and the log reads `boss_kill <seq>: <result> (mMode 0x.., m302 n, m304 n)`, once per change of result, of m304 being nonzero, and of mMode (0..2, vines up, count as one).

Offline: `TestBossHeaderPins` (the Bmd defines and the audio addresses), `TestBossDetailPerProc`, `TestBossKillWantedRows` (each row in its own arena with its own table's bit; neither with the other's table or bit, in kindan or kinMB, in an X stage, or with the local bit set), `TestBossGuardRules` (every proc byte value taken exactly when a row's proc has it), `TestBossKillRelayBmd`, `TestBossKillBmdNeverInRefight`, `TestBossKillBmdNotWhenLocalHasIt`, `TestBossKillBmdNoREL`, `TestBlobFresh`.

Rig: two Dolphins with `mp-local --coop` on one world, the FW boss not killed on either file, both with the REL of this build (recapture first). Hops (`coop-slice-fw.md`, "Valid stage-hop points"): `wwdev stage-hop kinBOSS 0 0 --yes` plays the intro, `kinBOSS 1 0 --yes` stands at (-14, 0, 1643) without it; leaving is the Warpf to `Omori 6 0`. Read the request with `WW_DOLPHIN_INDEX=<n> ./wwdev.exe peek 0x80413410 16` (seq, proc 00EC, tag 424B, ack, then the result byte, mMode, m302, m304), and B's Bmd with `peek <bmd>+0x300 8` (mMode, m302, m304, m306).

- [ ] **KD-0 boot**: as D4-0; no `boss_kill` line outside kinBOSS and M_DragB.
- [ ] **KD-1 waiting**: A kills its Kalle Demos while B is (a) in its intro (`kinBOSS 0 0`), (b) at the vines up (mode 0..2), (c) with the flower falling (mode 3, m302 0..1, m304 0, or m304 left over from a close less than 60 frames before), (d) swallowed (mode 5, eat). Each is `waiting` with those fields, logged once per change; nothing written (mMode, m306 and mB74 unchanged on B).
- [ ] **KD-2 applied at each step**: B cuts its own vines, the flower lands and opens; A's kill is applied at m302 2, then (reruns) 3, 4 and 5. B's log shows `applied (mMode 0x3, m302 n, m304 n)`; B's Kalle Demos plays HANA_DEAD, the hands and feet lie down, the head dies and the heart container appears (only if STAGE_LIFE is clear on B's file), Makar drops and talks, STAGE_BOSS_ENEMY lands in B's own save and the Warpf appears at the body; the boss music cuts at the apply (note whether it does). Screenshot B at the camera and at the Warpf.
- [ ] **KD-3 never saw the intro**: B enters with the world's STAGE_BOSS_DEMO already set (`kinBOSS 1 0`, no intro): it fights, and an apply still gives Makar's drop (inferred: the death camera sets m2DC 5 itself, d_a_bmd.cpp:1439) and the warp out.
- [ ] **KD-4 the exit**: after KD-2, B takes the Warpf to `Omori 6 0`; the hold on FW's boss bits is gone and no request follows.
- [ ] **KD-5 same frame**: both kill on the same frame: at most `already` in each log, one heart container between the two files.
- [ ] **KD-6 entry after the kill**: B enters kinBOSS after the world's bit has landed on B's save: Bmd's create refuses (Makar spawns if Farore's Pearl is missing), `no actor` or no request at all, the placed Warpf stands.
- [ ] **KD-7 never in a refight**: with FW's boss bit in the world, an X stage gets no request.

Until a disc with this REL is in use, the kinBOSS rule stays: the second player waits outside kinBOSS until the kill, then walks in; a player trapped inside dies, continues at `kinBOSS 1 0`, or uses /warp.

### Enemy mirror: pot ambusher Bokoblins (DRC room 7)

Bokoblin types 2 and 3 hide in a pot (`tubo_wait`, d_a_bk.cpp:5052-5055: m02BA 0xFF, dr.mAction 15). They come out when the host's Link comes near or the pot goes; 3 leaps out (JATTACK1) and 2 jumps (JUMP1). Before this change the BK gate refused them. When the host killed one, the client's copy stayed alive at its spawn (hp 5/5) and ran its own AI. That was seen live in M_NewD2 room 7 (unkeyed 0xFFFF3203). Now `ParamsOK`/`LiveOK` take them. Until batch C the client wrote mType 0 into its own copy before the block that offers it, because the REL's `em_bk_gate` took only a live 0/1/4 (after create mType 2/3 changes nothing but the leap animation). Batch C's REL takes a live 2/3, parks a hidden copy's stick and hands a still-potted copy back into tubo_wait; the mType write and its restore are gone (the batch C section, AMB). Once the REL reports the copy MIRRORED (and only then: a copy it does not hold runs its own AI, which a write would fight), the client sets its scale to 1 on the actor and the model's base scale (a copy that came out because its pot was gone is at half scale; only its own execute grows it back and copies it to the model, and the REL never does), and while the host's slot is HIDDEN it puts a copy that came out before the adoption back in its pot (m02BA 0xFF). A slot dropped before the REL ever took the copy gave its mType back (until batch C). This change itself made no REL change; batch C's AMB unit is the REL half.

Offline (`go test -count=1 ./mods/enemysync ./cmd/wwdev`): `TestStaticGates` (types 2 and 3 taken, live 0 too; type 7 still refused), `TestBkAmbusher` (the DRC params; a switch-gated ambusher is never written 0xFF), `TestMirrorAmbusher` (batch C: no mType write, ever; nothing else before MIRRORED or when refused; once MIRRORED and HIDDEN, m02BA 0xFF and actor and model scale 1; no m02BA write after the reveal; a re-adoption with the host's out sets the scale only; the torch Bokoblin untouched), `TestMirrorAmbusherReoffer` (batch C: a slot dropped before adoption and offered again is never written), `TestClassify`, `TestEnemyMatchDry`, `TestEnemyKindWatch`.

Live, both sides `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u`, co-op on, hop `M_NewD2 0 7`:

- [ ] **Listed**: the host lists 4 enemies in room 7: the 2 keyed Fire Keese, BK `0xFF02FF40` and BK `0xFFFF3203`. Check with `./wwdev.exe enemy-match` on each side: the 0xFFFF3203 line reads `verdict=ok` with the same pkey on both.
- [ ] **Taken**: on a batch C build the client writes no mType: its `enemy-match` keeps `bk_type=3` (before batch C the client log had `... is a pot ambusher (mType 3); mType 0 written so the REL takes it` once and `enemy-match` showed `bk_type=0`). The status line shows the slot adopted with no ERR_GATE.
- [ ] **Hidden**: with the host's Link away from pot #6, the client's Link walks up to its own pot and breaks it. No Bokoblin comes out on the client (the adopted copy is paused). Note where its stick is: see the stick gap below.
- [ ] **Reveal**: the host's Link goes near the pot (within 500 units: m02B5 0x32). The Bokoblin leaps out on the host and the copy comes out at the same spot on the client and follows the host's.
- [ ] **Kill**: the host kills it. The client's copy dies quietly (no item), and no hp 5/5 Bokoblin is left at the spawn. sw225 is set on both. The client reloads room 7: the Bokoblin is not created.
- [ ] **Client first**: `AmbushHides` counts only a copy that was already out when the REL took it, so the host must not list room 7 yet. Reload both with the host's Link outside room 7. The client's Link reaches pot #6 and breaks it: its own Bokoblin comes out (half size) and runs its own AI. Then the host enters room 7: when the client adopts the slot the copy goes back in (m02BA 0xFF; `AmbushHides` 1 in the stats) at full size, and comes out only when the host's does, full size. With the host already in room 7 the copy is adopted first and never comes out, and `AmbushHides` stays 0: that is the **Hidden** check, not a failure.
- [ ] **Regression**: rooms 0, 3 and 6 BKs (types 4 and 2), the r1 and r11 ambushers (`0xFFFF6443`, `0xFFFF1E03`, both unkeyed, no death switch): no ghost after a host kill. The room 6 pair (`0xFFFF0002`, setID 0 and 2) has m02B5 0, a reveal distance of 0: only the host breaking its own pot brings it out, and walking up does nothing.
- Room 7 fills the four block slots exactly (2 Keese, 2 BK). With a fifth enemy listed, the selection drops one.

Gaps:
- **Only the host's Link reveals an ambusher.** The client's Link near the pot, or the client breaking its own pot, does nothing until the host's comes out.
- **The stick shows while the copy hides (REL; batch C's AMB unit parks it, R-8, check AMB-2).** The host's hidden ambusher parks its stick at (-10000, -10000, 0) (d_a_bk.cpp:4219-4227) and its hidden execute returns before the stick placement. On the client the REL's `em_bk_attach` (enemy_mirror.c:2698) runs every frame on an adopted HIDDEN copy and puts the stick at its hand, and the stick draws by itself (daBoko), so it can float at the pot while the Bokoblin hides. Go cannot gate it per slot (the hook-disable bit is per kind and would freeze the torch Bokoblin's stick). Fix: skip `em_bk_attach`, or park the stick, while the slot is HIDDEN. Switch-gated hidden Bokoblins have the same issue.
- **A copy the REL does not take stays its own.** Refused (ERR_GATE, ERR_MORF, an early BOKO_ATTACH, ERR_ICE) or never given a slot, it runs its own AI and comes out on its own; Go does not write it.
- **Handback loses the leap (batch C's AMB unit hands a still-hidden copy back into action 15, R-9, check AMB-4).** A copy handed back while still hidden (the host left the room, a hold ran out) goes to action 0/0: em_handback knows only the create pairs. It comes out without the leap and patrols. A proper handback (action 15 for a still-hidden ambusher) is a REL change.
- **Pot-gone reveal scale.** When the host's pot is broken first, the host's Bokoblin comes out stunned at half scale and grows back. Scale is not streamed, so the copy shows full size.
- **Not covered here:** the M_Dra09 Kargaroc and the Moblin it carries have no mirror (batch C refuses the rider, D9), and the Magtails have none (batch C cut its MT row after P0-MT). Each game keeps its own copy, and an unkeyed one the host kills stays a ghost on the client (audit option B: a host tombstone for refused unkeyed enemies, a wire change).

### Enemy mirror: layout 4, the ambusher REL path and the Kargaroc (batch C)

Batch C (`ww-multiplayer-handoff/design/enemy-mirror-batch-c.json`: D1-D14, layout4, R-1..R-21, go_changes, ambush_cleanup, bb, mt, test_plan, cut_order; `enemy-mirror-batch-c-facts.json`) has three parts; its fourth, the Magtail, was cut after P0-MT:

- **L4, the enemy block's layout 4** (additive: the layout-3 annex and block bytes do not move). It adds hook-disable bytes for kind ids 7..14 at block +0x28 (BB 0x80413128; 0x80413129, the reserved MT id 8, is unread), inside the seq span. It adds a knob table at 0x80413750, written separately, that carries live quiet, tg_hit_up and invuln for ids 3..15. Its gen is a u16 at block +0x22, the REL echoes it at +0x194 (0x80413294), and the BB fingerprint misses count at +0x196.
- **AMB**: the REL takes the pot ambushers' live mType 2/3, parks a hidden copy's stick and hands a still-potted copy back into tubo_wait. The client's mType-0 write is gone in the same build.
- **BB**: the Kargaroc is kind row 7. Tokens `bb` (keyed) and `bb+u` (the sea birds).
- **MT (cut)**: the Magtail was kind row 8, the first cut. P0-MT disproved its sphere model, so R-20 deleted the row before the recapture; id 8 stays reserved and `mt` / `mt+u` are refused with the reason (the MT note below).

BB is off by default (`DefaultMirrorKinds` stays `mo2,bk+u,cc+u`). The kind table hash is 0x587E10C2 (0xB826567A with the MT row). The network protocol is 4: a v0.5.0 peer is refused at the hello, and the LAN list shows it as incompatible.

Offline, before any live run (`go test -count=1 ./mods/enemysync ./wwapi/mailbox ./wwapi/addr ./mods/multiplayer/net ./internal/inject/... ./cmd/wwdev`):

- **L4 block**: `TestEnemyHeaderPins`, `TestEnemyBlockPlacement` (the annex bytes identical to layout 3), `TestEnemyBlockEncode` (the golden moved only at +0x12, 0003 -> 0004), `TestEnemyBlockL4`, `TestEnemyKnobsEncode`.
- **L4 Go writers**: `TestKnobWriterGens` (a new nonzero gen on the first block, on a value change, an epoch change and after a disarm), `TestKnobRewriteOnEcho` (exactly one rewrite on an echo mismatch once the REL acked a block naming the gen, and one log line `knob table rewritten: seen G<n> want G<m>`), `TestMirrorKnobTable` and `TestHitApplierKnobTable` (the table written before the first block of each side, the gen and the hook bytes in every block), `TestParseAnnexL4` (`bb:hooks=tail` at +0x28 bit 0x02; `ki:quiet=5` accepted into the table; `tn:tg_hit_up` still the annex u8; `mt:hooks` refused since the MT cut).
- **Kind table and wire**: `TestKindTablePins` (every row's 37 fields and the batch's offsets against `wwenemy_kinds.h`), `TestKindsBatchC` (also: `mt`, `mt+u` and `mt:` overrides refused with the cut's reason), `TestKindHash` (0x587E10C2), `TestWireHostKindMask16` (bit 8, the reserved id, round-trips in byte 31; an old frame with byte 31 0 is unchanged), `TestHelloGoldenBytes` and `TestVersion3PeersRefused`.
- **AMB**: `TestMirrorAmbusher` and `TestMirrorAmbusherReoffer` (no mType write, ever; the pot and scale writes as before).
- **BB**: `TestBbOffsets` (`wwapi/addr`), `TestBbLiveRefusal`, `TestStaticGatesBB` (types 5/6 refused keyed and unkeyed; unk_2DF and a knockback refuse an unlisted bird; a listed one stays through the knockback), `TestReadSampleBB*` (five Reads; DYING from health, mbDead or the shrink; HIDDEN from unk_2F2), `TestHostBBWireSlot` (current.angle.z in slot +0x18), `TestHostTombstoneBBVanish` (a bird that vanishes alive is a tombstone; a Moblin is not).
- **MT (cut)**: only `TestMtOffsets` (`wwapi/addr`: the Magtail offsets stay for research) remains; the samplers, hit, custody and probe tests went with the row.
- **Probes** (`cmd/wwdev`): `TestEnemyMatchDryBatchC`, `TestEnemyKindWatchBB` (fp=ok, then fp=miss after one word changes), `TestEnemyBlockDump` (knob_gen, hkd_hi, the knob table line, knob_gen_seen and fp_miss).
- **REL**: `TestBlobFresh` and `TestT2EndsBelowMailbox`, after the one REL build and restamp (R-21), plus build.py's static asserts (kind count 8 after the MT cut). The patcher's RELS.arc growth line stays under 0x10000 with at least 0x800 of headroom after each commit. v0.5.0 is at +33856 (0x7BC0 headroom).

Rig (`ww-multiplayer-handoff/archive/`): host A user-4 (sword), client B user-3 booted with `s2v2-live/boot_b_sword.sh`, and `s2v2-live/healer.sh` on both pids under `mp-local`. Take hops only from `archive/coop-drc/HOPS.md`. In M_NewD2, room 5 has no PLYR and `M_NewD2 0 4` and `0 6` hang. M_Dra09 needs room 9. Never warp a Link during its void-out respawn. "B restart" means restarting B's client with a new `WW_ENEMY_ANNEX`. No step needs a human player. After every step, sweep both players' full Player.log, not only the reported issues.

P0 runs on the v0.5.0 build, before the REL code (rig only):

- [ ] **P0-L4 region proof**: `region-probe arm --yes`, then hop `M_NewD2 0 0`, `6 3`, `7 4`, `0 7`, `14 8`, `15 10`, `3 12`, `0 16` and `M_Dra09 0 9`, then `region-probe check`. Pass: every canary word in 0x80413750..0x804137AF is intact. Fail: move the table into a canary-clean span of +0x8B0..+0x1100 before L4. Run it with the v0.5.0 `wwdev.exe`: from batch C on, `CanaryArenaHigh` starts past the table (0x804137B0, mailbox/mod.go) and `canaryForbidden` lists `EnemyKnobRegion` (coop_diag.go), so this build's probe no longer arms the table's span. If the table moves, both follow it (`CanaryArenaHigh` is derived from `EnemyKnobOff` + `EnemyKnobSize`).
- [ ] **P0-BB**: `stage-hop M_NewD2 6 3 --yes`, then `./wwdev.exe enemy-watch 0 --kind bb` on A. Pass: each walk line ends `fp=ok`, meaning the four words at exec and at tc and the blr at tc+0x59C are the batch's fingerprint. Record `type` for sets 0 and 2 (expect 4 and 7) and `m2f2 0x00`.
- [x] **P0-MT gate**: failed on `M_NewD2 14 8` (body sphere centres off the segment translations), so the MT row was cut (the MT note below). The `--kind mt` probe went with the row.
- [ ] **P0-PATH** (offline, information only): read path 0 of M_Dra09 room 9 for mArg3 5 vs 6. The rider is refused either way.

L4 (never cut):

- [ ] **L4-0 offline**: everything above green, build.py's asserts, `extract_blob.py`, `-update-cli-golden` (only the build id and the size move), gofmt and vet. Record the growth line. Then `./wwdev.exe auto-recapture` and one Shift+F1.
- [ ] **L4-1 boot and echo** (`M_NewD2 0 0`):
  - `peek 0x80413100 48` shows layout 0004 at +0x12 and a nonzero u16 at +0x22;
  - on both Dolphins, `peek 0x80413294 2` equals that u16;
  - `peek 0x80413750 96` shows gen_head == gen_tail == knob_gen and layout 4.

  `enemy-block-dump` prints all of it (`knob_gen`, the knob table line `valid for knob_gen`, `knob_gen_seen`).
- [ ] **L4-2 annex unchanged**:
  - With the same `WW_ENEMY_ANNEX` string, `peek 0x804130C8 56` matches the v0.5.0 bytes recorded before the recapture (aux and seq aside).
  - Then run the B1 T6 regression with every old kind on: one kill each of MO2, BK, CC, KI and AM, a `bk:tg_hit_up` override still applying, and 8 hops from P0-L4's list.
  - The canaries stay flat apart from PauseReasserts.
- [ ] **L4-3 live override for id >= 3**: B restart with `WW_ENEMY_ANNEX=ki:quiet=60`, `stage-hop M_NewD2 7 4 --yes`, A `enemy-kill <keese pid> --yes`. Pass: B's copy shrinks over the overridden length (`enemy-watch` frames on B). Run once more without the override as the control.
- [ ] **L4-4 freshness**: on B, `poke-u32 0x80413750 DEADBEEF`. Pass:
  - within one tick, B's log shows `enemy client: knob table rewritten: seen G0 want G<n>`;
  - the table and 0x80413294 recover.

  Repeat after a save-state load on B. During the clobber, a `bb:hooks` bit at 0x80413128 stays in force.
- [ ] **L4-5 wire, mixed builds**:
  - With `bb` on A, the HostKindMask in B's client log includes bit 7 (`enemy-block-dump` kind_mask). Bit 8 (the reserved MT id) never shows; `TestWireHostKindMask16` covers it.
  - A v0.5.0 exe against a batch-C disc logs the RELBuildID refusal.
  - A batch-C peer against a v0.5.0 peer is refused at the hello (protocol 4 vs 3, D13).

AMB (never cut; `M_NewD2 0 7`, the pot ambusher room):

- [ ] **AMB-1 taken**: both sides `mo2,bk+u,cc+u,ki+u`, `stage-hop M_NewD2 0 7 --yes`. Pass: `enemy-match` on B shows `bk_type=3` for 0xFFFF3203, never 0. B's log has no mType write. The slot is adopted with no ERR_GATE.
- [ ] **AMB-2 hidden stick**: keep A's Link away from the pot. Pass: `peek <boko model + J3DMODEL_BASE_TR_MTX_OFFSET> 48` on B shows the translation (-10000, -10000, 0), and B's screenshot shows no stick. Then B restart with `bk:hooks=ambush`: the stick returns to the hand, which proves the bit.
- [ ] **AMB-3 reveal and kill**: the pot ambusher section's Reveal and Kill steps above, unchanged. sw225 is equal on both (`swdiff.sh A B wwdev --zones`).
- [ ] **AMB-4 hidden handback leap**: with the copy adopted and hidden, A runs `stage-hop M_NewD2 0 0 --yes`. Pass on B:
  - `enemy-watch --kind bk` shows dr.mAction 15, mode 0, m0300 0, m0310 0, scale 1.0 and mType 3;
  - B warps within 500 of the pot (`warp_near.py`) and the BCK is JATTACK1;
  - with `bk:hooks=ambush`, the handback goes to (0, 0) instead.

  Regression: no ghost after a host kill for the room 6 pair 0xFFFF0002 (`M_NewD2 9 6`) or the r1 and r11 ambushers (`20 1`, `2 11`). The torch Bokoblin keeps its stick.

BB (`M_NewD2 6 3`, `M_Dra09 1 9`):

- [ ] **BB-1 adopt and look**: `bb` on both sides, `stage-hop M_NewD2 6 3 --yes`. Pass:
  - `enemy-match --dry --kinds bb` on A and on B diff clean before ` pid=`, with two kind-7 slots plus BK setID 1;
  - in the screenshot pairs, yaw and tilt match in flight, the 9 tail models trail, and the beak is closed;
  - `peek 0x80413296` (fp_miss) is 0.

  Bisect with B restarts using `bb:hooks=mtx|beak|tail|extra|handback`.
- [ ] **BB-2 client hits**: patch `backstab.py` to read current.angle 0x206 (BB's shape.y is its create yaw) and to warp to the bird's y. B swings at the perched bird. Pass: ACK Health goes 2 -> 1 -> 0 (ACK Health is the signal). If B cannot reach the bird, record it as a rig gap.
- [ ] **BB-3 deaths**:
  - A runs `enemy-kill <bb pid> --yes` (BB is K1-verified, Ice 0xC80). B's copy takes SF2_LIGHT and is deleted with no item and the tail folded.
  - A kills one with the sword. B keeps the knockback for about 23 frames, then shrinks quietly. `actors` on B shows no 0x00B6 left.
  - The keyed death persists when B reloads room 3.
- [ ] **BB-4 synthetic hidden gate and handback**:
  - On A, poke u8 0x2F2 = 0xE6 (an unset zone switch + 1). A's bird stops drawing and the slot shows HIDDEN in `enemy-block-dump`. B's copy, adopted shown in BB-1, keeps drawing, frozen: R-15 only clears the copy's 0x2F2 while the host is shown and never sets it (0x2F2 is not streamed), so only a copy that is hidden on B too stays hidden. To see both hidden, also poke u8 0x2F2 = 0xE6 on B's copy while A's is hidden (the REL leaves it alone then): both birds stop drawing (screenshot pair).
  - Poke 0 on A. The REL clears B's 0x2F2 on that edge: both birds reveal at the same spot with no tail whip (the hidden->shown edge re-seeds the tail after the first calc).
  - Then A runs `stage-hop M_NewD2 0 0 --yes`. B's copy gets 0x2DD 3 and 0x31A set, and attacks B's Link after the timer. Perch type 7 does not snap (watch 0x2DD, d_a_bb.cpp:1594-1621).
- [ ] **BB-5 rider refusal**: `bb+u,bk+u,mo2+u` on both sides, `stage-hop M_Dra09 1 9 --yes`. A kills the two unkeyed BKs with `enemy-kill`. sw224@r9 is equal via swdiff. Pass:
  - the type-5 BB and its mode-5 Moblin appear on both games;
  - `enemy-match` shows the BB `verdict=params-gate` (`bb_live=rider`) and the Moblin's mode refused;
  - neither log shows ERR churn.

  A Kargaroc that vanishes alive on the host (a path end, the forest's andsw0) is listed as a tombstone and ends quietly on the client.

MT: cut after P0-MT. On `M_NewD2 14 8` the eye sphere sat at `eyePos`, but the body sphere centres were not the segment translations (segment 0 about 65 units off, segments 1..4 about 10; samples in `archive/enemy-batch-c/p0mt-room8.txt`), so the segment and sphere model the REL's MT code was built on (offline, from the retail `d_a_mt.rel`) was wrong. By `cut_order` and CONTRACT.md section 7, R-20 deleted the Magtail row before the recapture: `EMK_KIND_MT` / `KindMT` 8 stays a reserved id with no row (`EMK_KIND_COUNT` 8), the block's hook byte +0x29 is unread, and `mt`, `mt+u`, `WW_ENEMY_QUIET_DEATH=...,mt` and `WW_ENEMY_ANNEX=mt:...` are refused with the reason. MT-1..MT-4 do not run. The Magtail stays a local enemy on each game, and `classes.go` has it as `Other` again (no `enemy-kill`).

Not covered by the rig:

- a BB hidden at create revealing in I_SubAN r9, and the sea Kargaroc (I_SubAN and the sea are not in HOPS.md: run `verify_plyr.py` first, because a missing (point, room) halts in d_stage.cpp:1463);
- real light arrows (`boot_b_sword` has none; `enemy-kill` writes instead).

### Co-op: the stale-actor refresh, Tiers 1 and 2

Design: `ww-multiplayer-handoff/design/stale-actor-refresh.md` (Tiers 1 and 2, G1-G4, with its Check notes). An actor that reads its bit only in create stays stale when the partner's bit lands after it was created. The replica now keeps a **landed set** (`mods/coop/replica/landed.go`): the bits its own writes set and the next Observe verified, per bit (new &^ old), from the merge path (live memBit chests, memory switches, pickups 0-31), the scoped path (dan, zone and room switches) and the kill path. It is cleared on a stage, table or file change and on a save state. The refresh (`refresh.go`) acts only on an actor whose bit is in that set, never on a bit that just reads set, so the receiver's own play is never touched. It runs inside the M6 tick and needs FeatureCombat, not a dry run.

- **Tier 1, the key-door lock** (`writer_lock.go`): a DOOR10 or DOOR12 whose lock switch (a small-key door, or a boss door with swbit < 0x80) landed gets one byte, `dDoor_key2_c.mbEnabled` 1 to 0 (DOOR10 +0x308, DOOR12 +0x2E4). This is held while the unlock animation plays (m20, lock +0x20), while an event has ordered the door, while neither of its rooms is loaded and settled, while Link is in a demo or within 300 units (x-z) of the door, and for the settle time (1 s). Each door is cleared at most once. Log: `co-op: refresh: cleared the stale lock of actor 0x... (pid n, proc 0x012E , ...): its switch 0x36 landed`.
- **Tier 2, the death switch**: a K1 enemy whose death switch (home.angle.z's low byte) landed is light-shrunk by a second allow rule of M6, with all of M6's gates. In practice only Bokoblins: a Moblin, Stalfos or Darknut with a memory death switch is BOSS from create, which Eligible refuses. A pid the enemy mirror claims is held (`claimed`). A keyed enemy is taken only when M1's verdict syncs its key (`synctables.KillMask`), since its death records the setID in this game's zone (fopAcM_onActor, c_damagereaction.cpp:159); a Shared or unsynced key is held (`does not sync`). Just before the write the death switch and Link's position are read again (`deathHolds`), as Tier 1 reads the lock switch and Link. Once a switch landed the walk runs with no live kill store too, so M6's keyed rule now holds a target whose home room is not loaded and settled. Log: `co-op: refresh: light-shrunk a stale enemy whose death switch landed: actor ...`.
- `/status` shows `refresh: <n> landed bit(s) this visit; <n> lock(s) cleared, <n> held (<last reason>); <n> death-switch light-shrink(s), <n> held (<last reason>)` once anything has landed.

Offline (`go test -count=1 ./mods/coop/replica ./mods/coop/session ./wwapi/tables/switches`): `TestLandedSetDecode` (per bit, keyed by store, cleared on a new visit), `TestLandedSetFromVerify` (a switch the game set in the same byte does not land), `TestRefreshLockClear`, `TestRefreshLockHeld` (unlock animation, event, mirror pause, Link demo, rooms not settled), `TestRefreshLockNotLanded` (a local key, an open lock), `TestRefreshSettle`, `TestLockDoorRoles` (boss door swbit >= 0x80 never), `TestRefreshGuardLock`, `TestRefreshDeathSwitch`, `TestRefreshDeathSwitchHeld` (set locally, claimed, Link near), `TestRefreshDeathKeyed` (a keyed Bokoblin outside the mask held, a synced one shrunk), `TestRefreshKeyedRoomSettled` (a kill of a room being deleted waits), `TestRefreshDeathLinkRecheck` (Link near by the writer's heartbeat holds it), `TestRefreshDeathFromHomeAngle`, `TestLiveUses`, `TestRefreshStatus`.

Rig: two Dolphins with `mp-local --coop` on one world. Run the Tier 1 checks once with the default switch timing (a memory switch lands at the receiver's next room change, after the room hold) and once with `--coop-live-switches` (it lands while the receiver stands in the room). Peek a door's lock with `WW_DOLPHIN_INDEX=<n> ./wwdev.exe peek <door>+0x308 1` (the door's address from `actors`). The DRC and FW key doors are in the stage file (room -1), per `logs/m6/tables4.json`. Their rooms come from home.angle.x (front & 0x3F, back >> 6):

| Stage | Door | Lock switch | Rooms | Syncs |
|---|---|---|---|---|
| M_NewD2 | keyshut TGDR 8 | sw54 (0x36) | 14 / 3 | yes |
| M_NewD2 | keyshut TGDR 0 | sw60 (0x3C) | 0 / 2 | yes |
| M_NewD2 | keyshut TGDR 1, 9 | sw61, sw62 | 2 / 11, 3 / 4 | no (Tingle Tuner `agbA`): never refreshed |
| M_NewD2 | door20 TGDR 11 (boss) | sw64 (0x40) | 10 / 10 | yes |
| kindan | keyshut TGDR 5 | sw5 | 5 / 8 | yes |
| kindan | door20 TGDR 13 (boss) | sw38 (0x26) | 16 / 16 | yes |

- [ ] **ST1-1 DRC sw60, receiver away**: both at the r0/r2 key door. B steps more than 300 units away, still in r0 or r2, with 0 small keys on B's derived count. A opens the door with a key. Default timing: B changes room (r0 to r2 or back) and walks back. The lock is gone within a second of the switch landing, B's log has the `cleared the stale lock` line once, and B walks through with no key consumed (B's key count does not drop again). With `--coop-live-switches` the same happens without B's room change.
- [ ] **ST1-2 DRC sw60, receiver at the door**: as ST1-1, but B stands at the door (within 300 units). Nothing is written and `/status` holds with `Link within 300 units`. B steps back past 300: the lock clears.
- [ ] **ST1-3 DRC sw54 (r3/r14)**: ST1-1 on the other synced small-key door.
- [ ] **ST1-4 FW sw5 (kindan r5/r8)**: ST1-1 in Forbidden Woods.
- [ ] **ST1-5 the receiver's own key**: B opens a key door itself. No refresh line, `/status` refresh shows the door neither cleared nor held (the switch never landed: B's own game set it).
- [ ] **ST1-6 mid-unlock race**: B starts its own unlock event at the door (it takes B's key) just as A's switch lands. Either B's own event finishes (m20 runs to 0 and keyOff, with no refresh line) or the refresh clears first and B's keyInit skips the switch write and the key decrement (d_door.cpp:433-447). One key is spent at most, and B is not left stuck in the event.
- [ ] **ST1-7 never sw61/sw62**: A opens the r2/r11 or r3/r4 door: B's lock stays until B's room change (these switches do not sync), and no refresh line appears.
- [ ] **ST2-1 DRC r7 ambusher, mirror off for it**: both sides `WW_ENEMY_MIRROR_KINDS=mo2,bk,cc+u,ki+u` (no `bk+u`), both in M_NewD2 r7, B more than 300 units from the unkeyed ambusher `0xFFFF3203` (death switch: room switch 0xE1). A kills its ambusher. Within a second of the room switch landing (the room-edge scoped path), B's copy light-shrinks once: one item ball on B, `refresh: light-shrunk a stale enemy whose death switch landed` once in B's log.
- [ ] **ST2-2 claimed stays the mirror's**: ST2-1 with the default kinds (`bk+u` on). The client's copy dies through the mirror (quiet death, no item), and `/status` refresh on B shows it held `claimed: the enemy mirror's host owns its death` or nothing at all: never a second death.
- [ ] **ST2-3 receiver's own kill**: B kills its own r7 ambusher. No refresh line and no second shrink (the switch is B's own).
- [ ] **ST-N stage hop**: hop B out of M_NewD2 with a lock or a shrink pending: no write lands in the new stage, `/status` refresh restarts at 0 landed bits, nothing faults.

Not covered: Tier 3 (barricades, boulders, leaf piles, freestanding items, chests, the door plants and Mothulas), the `--coop-refresh` knob (G6) and a dry-run log per candidate. Tiers 1 and 2 are on whenever combat is granted.

### Batch D P0

Design: `ww-multiplayer-handoff/design/enemy-mirror-batch-d.json`, key `p0` (offsets in `rel_changes`, `actor_refresh.procs` and `magtail`). P0 runs on today's build, before any batch D REL code, one Dolphin at a time. The three probes only read memory (`cmd/wwdev/enemy_batchd_probe.go`); the only writes are `stage-hop`, `region-probe arm` and the hand-poked BST recipe. Pick the Dolphin with `WW_DOLPHIN_PID=<pid>` on every command. Take hops only from `archive/coop-drc/HOPS.md` and the list below.

Offline: `go test -count=1 ./cmd/wwdev -run 'Bst|Rf|Mt2|MtExec|RegionProbeCovers'`. `TestBstProbe` and `TestBstVerdictMissing` cover the parts sorted by +0x2B4 and APPLIED, WAITING, ALREADY and NO_ACTOR. `TestRfProbe` covers each proc's line, the Tbox and Saku fingerprints (the Saku one is information only), and the FAIL cases. `TestMt2Probe`, `TestMt2Verdict`, `TestMt2F470Eased`, `TestMt2Torn` and `TestMtExecTextMiss` cover the residuals, the build f470, a torn read, OVER, INCOMPLETE, PASS, FAIL, a missing matrix and fp=miss. `TestRegionProbeCoversRefresh` checks that `region-probe` arms 0x804137B0..0x804137D0.

- [ ] **P0-L5 region proof** (the refresh block +0x8B0, 0x804137B0..0x804137CF). `CanaryArenaHigh` already starts at 0x804137B0 (past the enemy knob table), so the existing probe covers it. No change is needed until G-D5 moves it to +0x8D0; run this with a wwdev from before that move.

  ```
  WW_DOLPHIN_PID=<pid> ./wwdev.exe region-probe arm --yes
  WW_DOLPHIN_PID=<pid> ./wwdev.exe stage-hop M_NewD2 0 0 --yes
  ```

  Then hop the rest the same way: `M_NewD2 20 1`, `M_NewD2 1 2`, `M_NewD2 6 3`, `M_NewD2 9 6`, `M_NewD2 14 8`, `kindan 0 1`, `Siren 0 0`, `SirenB 1 0`, `kinBOSS 1 0`. Finish with `WW_DOLPHIN_PID=<pid> ./wwdev.exe region-probe check`.
  - Pass: the "below arena (past the enemy knob table)" region reports every word intact.
  - Fail (any changed word in 0x804137B0..0x804137CF): move the refresh block to a clean span below +0x1100.
- [ ] **P0-BST** (`stage-hop SirenB 1 0 --yes`):
  - `WW_DOLPHIN_PID=<pid> ./wwdev.exe bst-probe` reads once. It prints the three proc 0xF2 actors sorted by +0x2B4 (head 0, hand1 1, hand2 2) with their addresses and raw words: `+0x10D4` (8 bytes: m10D4/act, m10D8/mDamage), `+0x2E7C` (m2E7C in the top byte), `+0x2E98` (m2E98, m2E9A) and `+0x10FC` (m10FC[0], the sleep timer). It decodes each word and ends with the answer bk_bst would give (`APPLIED`, `WAITING` with the terms that miss, `ALREADY` or `NO_ACTOR`, plus the result word).
  - Through a real fight, run `WW_DOLPHIN_PID=<pid> ./wwdev.exe bst-probe 600`. It reads every tick and prints only on change. Confirm head m2E7C 1 -> 10 -> 11 -> 12, and head act 7 with mDamage 0 -> 3.
  - Forced recipe. `poke-u32` takes a plain hex address, so add each offset to the part's address from bst-probe's line (for example, head 0x80B03000 + 0x2E7C = `poke-u32 0x80B05E7C 0x0C000000`):
    - hands: `poke-u32 <hand>+0x10D4 0x00000006`, `poke-u32 <hand>+0x10D8 0`, `poke-u32 <hand>+0x10FC 0x0000012C`;
    - head: `poke-u32 <head>+0x10D4 0x00000007`, `poke-u32 <head>+0x10D8 0`, `poke-u32 <head>+0x2E7C 0x0C000000`.

    Then `bst-probe 30`.
  - Pass: vanilla parks at head act 7, mDamage 3, m2E7C 12 with both hands act 6 (asleep), and the last line reads `bk_bst would answer APPLIED 0x0107030C`.
- [ ] **P0-RF**: one `WW_DOLPHIN_PID=<pid> ./wwdev.exe rf-probe` after each hop. It prints every Stone2, Saku, daItem, Obj_Leaves and Tbox in the queue: params, the bit as the REL re-derives it, the design's fields, the generic gates the walk sees (`init`, `evtcmd`, `CARRY`, `pause`, dEvtMode, Link within 300 units) and the per-proc gate. Static checks print PASS or FAIL, and the last line is `P0-RF PASS|FAIL: <n> refresh actors, <k> static check(s) failed`. Any miss cuts that proc before the build.
  - `M_NewD2 1 2` Ebrock2 sw17: the stone2 line with `sw 17` reads `m648 0 (idle)`. Lift it and re-run: `m648 1 (lifted)`, proc gate `m648 != 0`.
  - `M_NewD2 20 1` Dsaku: `saku sw1 sw2 params&0xF +0xEF4 +0xEF8 +0xF00 +0xF04`; `+0xF00==sw1`, `+0xF04==sw2` and `half2-matches-params` PASS. `broken-fp(v2)` is still_open S3's fingerprint, information only.
  - `M_NewD2 0 0` purple rupee: `item +0x630 (bit) +0x63A +0x669 +0x66B`. Expect `+0x66B 6` and `+0x669 & 0x48` clear (proc gate ok). Bit 0x7F or item 0x4B shows REFUSED.
  - `kindan 0 1` Vochi: `leaves sw <(params>>13)&0xFF>`.
  - `M_NewD2 9 6` closed chest: `tbox funcType bit +0x2BC +0x2C4 (closed) +0x348 +0x3F0`, the execute words `+0x0C 4BFFFA71 +0x1C 4E800020` (`execute-fp PASS`), the three derived action addresses and `+0x348 is actionOpenWait` (or SwOnWait/SwOnWait2) with `closed-chest-action PASS`.
- [ ] **P0-MT2** (`stage-hop M_NewD2 14 8 --yes`): `WW_DOLPHIN_PID=<pid> ./wwdev.exe mt2-probe 120`, with at least one FIGHT stretch (walk Link into a Magtail's range while it runs).
  - The walk line per Magtail records the live `exec`/`bc1`/`bc2` words and blr words with `fp=ok|miss` against R-D6.
  - Each changed sample prints mode, `ring` (+0xBF4 & 63, read in the same class read as the sphere centres), f470, wiggle (+0x468), hit_cool (+0x460), bite (+0xC04), pos, then per segment `tr` and `col2` (morf[i] -> model +0x24), `sph` and the residual `d`. It ends with `eye`, `de`, `max`, the height spread `slope` and `ok`, `OVER` or `skip`. Residuals: sph_i - (tr_i - f470*col2_i) for i >= 1; sph_0 - (tr_0 + 100*col2_0); eye - (tr_0 + 30*col2_0). The game eases f470 by up to 1 per frame after it builds the matrices, so for i >= 1 the probe uses the build f470 (`built`, the least-squares fit over segments 1..7, at most 1 from the f470 read); without that every MOVE -> FIGHT stretch would read about 1.0 and FAIL.
  - The probe reads the class again after the matrices and compares mode, ring idx, f470, wiggle, pos, the morfs and every sphere centre (the idx alone does not move in FIGHT or a dive). A change retries and, after three tries, is printed `TORN` and left out (the p0mt-room8.txt:195 tear).
  - The run ends with a per-Magtail summary (samples per mode, torn, max residual MOVE and FIGHT, sloped samples) and `P0-MT2 PASS|INCOMPLETE|FAIL`. PASS means MOVE and FIGHT samples, all within 0.2. INCOMPLETE means no FIGHT sample (re-run). FAIL means EMK_WITH_MT=0 and the Go row reverted. A `sloped` count above 0 answers still_open S2.

### Release defaults (pre-release live checklist)

Enemy sync and the remote bodies are on by default (docs/05-known-issues.md "Shared enemies"), and the network protocol is 4 (3 in v0.5.0; batch C bumped it, D13), so this build refuses v0.5.0 and older at the hello. Every enemy-layer live pass on record ran under `wwdev mp-local` (one process, two Dolphins, one machine); none has run through the app's GameRunner or the CLI's `host` / `join`, between two processes or two machines, over WAN, or on macOS. The owner runs this list on the release candidate binary before the tag. `mp-local` now runs the enemy pipelines too unless `WW_ENEMY_SYNC=0`, so the smoke and co-op checks above include them by default.

Run every item with no `WW_*` env vars unless the item says otherwise. Sweep both players' full Player.log (and the app's log pane) after each item, not only the reported issue. Tick only the boxes a run actually passes.

- [ ] **R1 release gate: the app path, two machines.** App host and app joiner on two machines over LAN. The lobby leads to START. The logs show `enemy host: host-authoritative enemies on (kinds mo2,bk+u,cc+u), remote Link bodies step6 ...` on the host and `enemy client: mirroring the host's enemies (kinds mo2,bk+u,cc+u), remote bodies step6 ...` on the joiner. Moblins, Bokoblins and ChuChus are shared; the joiner's hit counts on the host; host enemies latch on the joiner's body, and the joiner takes PHIT damage. A failure here blocks the tag.
- [ ] **R2 the CLI path**: R1 again with `ww-multiplayer host` and `join`, including a direct `join` to a host without a lobby.
- [ ] **R3 armed block, no remotes**: the host plays alone with the step6 block written and the hit applier's keepalive running. Stage hops, a boss room (Gohma) and a cutscene: the game behaves as vanilla.
- [ ] **R4 unmirrored enemies ignore bodies**: a DRC room with Keese (unmirrored, ALLOW in SeedPolicy but not in the default policy) and Bokoblins (mirrored). The Keese and Kargarocs never latch on or damage the joiner; the Bokoblins do. The reverse holds too: the joiner's local Keese never latch onto the host's body.
- [ ] **R5 T6 regression** (B1 above) on the default settings, with the full-log sweep.
- [ ] **R6 co-op M2-M6 rerun** with `--coop-live-switches` and enemies on by default, including one quiet-death kill: the switch is set, nothing doubles, and the room reloads clean.
- [ ] **R7 enemy-host reconnect**: mid-session, suspend the host's app process (it holds the enemy connection, not Dolphin) past the 15 s read timeout, so the stale connection races the reconnect; separately, stop and restart the host's Dolphin, which restarts the pipelines. Pose never drops; enemies resume, or the log says `enemy sync off for this session: <reason>; multiplayer is unaffected`; the lobby never ends. The server logs the same-name host replacing the old connection.
- [ ] **R8 mixed builds**, both directions, against a real v0.4.1 binary: the version dialog appears on both sides, and the LAN list shows the other build as incompatible.
- [ ] **R9 upgrade path**: on a disc patched by v0.4.1 the first start re-patches it; with no saved vanilla disc the `pick` plan opens Setup; a v0.4.1 save state is refused.
- [ ] **R10 escape hatches**: `WW_ENEMY_SYNC=0` on both sides: no enemy connections, pose fine. `WW_ENEMY_REMOTE=off` on both sides: enemies mirrored, no bodies, and no Warn about the value.
- [ ] **R11 WAN** (a port forward or a VPN) in a crowded room: TyuTyu with 18 ChuChus, or Cave10 r9 with 16 Bokoblins. Watch for mirror and local-AI flipping (`StaleAfter` 500 ms). Capture the host's CPU and the RPM-per-tick counters: that data decides `WW_ENEMY_STATE_30HZ`.
- [ ] **R12 macOS**: the app's launch passes the `-C` MEM1 override, and R1 passes with one Mac.
- [ ] **R13 release pipeline dry run**: `workflow_dispatch`, or an `-rc1` tag of the release's version. A prerelease does not deploy the site or auto-update; testers download it by hand.
