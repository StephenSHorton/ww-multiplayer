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
- `TestWireSlotDormantAllow` (WFlagDormant, never carried or dying), `TestForwardHit` and `TestMirrorReadHit` (the AM eye drops the Grappling Hook, the AM2 weak spot the Hookshot, the AM2 body forwards only the Skull Hammer), `TestParseAnnex` (`ki:hooks=extra` accepted, `ki:quiet=5` refused) and the annex writers (`TestHostKinds`, `TestMirrorCatchUpPin`: the bytes at +0x1FC).
- `TestEnemyHeaderPins` and `TestEnemyBlockPlacement` (mailbox), `TestBlobFresh` and `TestT2EndsBelowMailbox` (the REL rebuild and restamp), and the REL's own static asserts (kind count 6, em_kind 0x44, PLAIN rows with FOPEN and a morf). RELS.arc growth stays under 0x10000.

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

The TN batch's B1 (`ww-multiplayer-handoff/design/enemy-mirror-tn.json`: D1-D14, R-1..R-16, test_plan P1 and T1-T6 with the P0 amendments; `archive/enemy-tn-b/P0-NOTES.md`) makes the Darknut kind row 6 (layout 3 holds it: the annex HKDX byte 0x1FF is id 6's; id 7 needs layout 4) and adds the Armos (AM2) direct apply. TN is off by default; a run turns it on with `tn` (keyed: kaze r8, M_Dai r17) or `tn+u` (the cave Darknuts) in `WW_ENEMY_MIRROR_KINDS` on both sides. BOSS Darknuts (D3), behaviour 12..15 and caped ones (equipment 4 and up, D5) are refused.

Offline, before any live run (`go test -count=1 ./mods/enemysync ./wwapi/mailbox ./wwapi/addr ./internal/inject/... ./cmd/wwdev`):

- `TestKindTablePins`, `TestColliderOffPins`, `TestKindHash` (the pin is 0x0F3064EE: a build from before B1 and one after refuse each other's STATE), `TestEnemyHeaderPins`, `TestEnemyBlockPlacement` (HD 18 direct, 19 front; the TN tg_hit_up byte 0x1FB; HAF 0x20/0x40/0x80 clear of 0x01..0x10), `TestBlobFresh` and `TestT2EndsBelowMailbox` (the REL rebuild and restamp), and the REL's own static asserts (kind count 7).
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

Rig (`ww-multiplayer-handoff/archive/`): host A user-4 (15-StartET card, sword), client B user-3 booted with `s2v2-live/boot_b_sword.sh`; `s2v2-live/healer.sh <bin> <pidA> <pidB>` on both PIDs for the whole run; `remote-link-live/warp_near.py` and `enemy-allow-a1/backstab.py` to place a Link. P1: one REL build, the restamp, then one Shift+F1 recapture (`./wwdev.exe auto-recapture`); `go test ./...` green first. Hop one room at a time (`./wwdev.exe stage-hop <stage> <point> <room> --yes`; `M_NewD2` once hung the game).

- [ ] **T1 kaze r8, adopt and look**: `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,tn` on both sides, `stage-hop kaze 8 8 --yes` on both. On B, `./wwdev.exe enemy-block-dump` (or the client log's slots line) shows three kind-6 slots; `WW_DOLPHIN_PID=<A> ./wwdev.exe enemy-match --dry --kinds tn` on A and on B diff clean before ` pid=`; nothing adopts while A's Darknut has m140C != 0 (`enemy-watch 0 --kind tn` on A). Screenshots of both: armor, helmet, shield and sword on the right joints, the left arm holding the shield, the shadow following the copy. The AGUARD shield follow is a rig gap (P0: the guard needs the host Link's cut while Z-locked; it holds only if Stephen plays the host). Bisect a wrong piece with B restarts: `tn:hooks=shield`, `attach`, `sword`, `coll`, `adopt_anm`.
- [ ] **T2 kaze r8, client hits**: B swings at an unguarded Darknut: the ACK's Health drops (the pass signal is the client ACK Health, never `applied_at` alone). B hits the front until the armor breaks: A's pieces go 0x0B -> 0x0A (or 0x0F -> 0x0E) and B's copy hides the armor when the bit clears, then streams the B* bcks (0x45..0x55, BRUN2, BSURI_SFB1). A back-stab from A with B watching: the copy's armor vanishes about 13 frames after the strike (no pop, no particles: design animation). A Tg-height miss is retuned live with `WW_ENEMY_ANNEX=tn:tg_hit_up=<n>` on A (R-13; 0 = the compiled 68). Guard-defence (A's Darknut in DEFENCE, action 10: the ACK's result is `parried` with detail `none`, from the defence sphere's Tg record on the v2 path; detail `defence`, 9, appears only under `WW_ENEMY_HIT_V1=1`) is a rig gap as in T1: it runs only if Stephen plays the host.
- [ ] **T3 kaze r8, quiet death (P0 death order)**: kill all three (some from A, some from B). Pass: at health 0 A's Darknut ragdolls (`enemy-watch` on A: mode -100, action 0, BDOWN_A1 then BDOWN_U1, about 1.6 s) and B's copy plays the same ragdoll at full size (it is DYING from the first ragdoll sample and holds scale through `EMK_TN_QUIET_HOLD`), then shrinks quietly; it is never handed back at health 0. Then `./wwdev.exe actors` on B shows no proc 0x00C0 and no orphan proc 0x01D5 sword; B's kill-all bars on switch 0x85 open (d_a_door12.cpp:154-170); death switches 0x86..0x88 are set on B; no miniboss sub-BGM change on either side (D4). Known gap: after a host sword drop and re-pickup the copy stays unarmed.
- [ ] **T4 caves, unkeyed**: `tn+u` on both sides, `stage-hop Cave11 0 9 --yes`: the four unkeyed Darknuts fill exactly the four slots; rerun the T1-T3 checks. Then `Cave10 0 16` with `mo2+u,tn+u` (two Darknuts among five enemies, four slots) last.
- [ ] **T5 AM2 direct apply**: `am2+u` both sides, `WW_ENEMY_REMOTE` unset, `stage-hop Cave09 0 7 --yes`. B positions with `backstab.py <bin> <armos> 6 90 back|front` (WW_DOLPHIN_PID=B); A stands with `warp_near.py` (or `backstab.py ... 1 90 back|front` on A for A's own swing). Pass: B back + A front: Health drops by the atp, ACK detail `direct`. B back + A back: one drop, no double damage. B front + A back: no drop, detail `front` (the negative control the old path got wrong). B front + A front: no drop, `front`. A alone back-stabbing: vanilla, unchanged. After an arrow stun (MAHI): hittable; carried, or MAHI mode 15: detail `invuln`. A hammer hit kills (health 0, m2CE 7/8). A hit during the Armos's invuln is retried, not dropped. Then `kaze 4 4` keyed with A at z >= 2900 (z < 2900 is void). Then once with `WW_ENEMY_REMOTE=step6` on both: no double apply, and the `attacker_overrides` counter does not tick on a direct hit. Then `WW_ENEMY_ANNEX=am2:hooks=special` on both: the old path returns (a B back-stab with A in front is `applied_at` and ignored, as on A1). Accepted divergences (am2_fix): the ITAI knockback heads away from A's Link; the hit SE and hit-stop use A's cut count; no crit flash particle on A. Rig gap: `backstab.py` has no warp-only mode in the archive copy, so placing A behind without a swing is a hand `warp` plus the angle pokes from the script.
- [ ] **T6 regression**: the default kinds and then every kind on (`mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u`): the A1 fixture rooms (the kinds probe goldens, one kill each for MO2, BK, CC, KI, AM), a `bk:tg_hit_up` override still applying, 8 hops with canaries flat apart from PauseReasserts. Sweep both players' full Player.log and the mplocal logs afterwards, not only the reported issues, and confirm no adopt while gated.

### Co-op: the Gohma kill relay (B5, A9)

Each game fights its own Gohma (`ww-multiplayer-handoff/design/research/C3-C6-gohma.md`, "The relay op (B5, revised)"). When one player kills theirs, the world gets DRC's STAGE_BOSS_ENEMY, but a partner still in M_DragB keeps the bit held (bossHold, unchanged). Its replica instead writes the boss_kill request (`mods/coop/replica/bosskill.go`; 16 bytes at 0x80413410, `inject/include/wwboss.h`), and the REL (`inject/rel/bosskill.c`) gives that game's own Btd the lethal-hit death: s16 +0x2E4 = 0xB and s16 +0x2E6 = 0x32 (d_a_btd.cpp:463-475). Its own death demo then drops the floor, spawns the heart container, sets the bit and creates the warp flower. The REL applies it only once the receiver has broken its own Gohma's armor (u8 +0x2E0 m02E0 = 1, the only phase vanilla's lethal eye hit takes, d_a_btd.cpp:457): an armored Gohma answers `waiting`, so the partner's kill ends the receiver's fight at the eye phase, never on the armored model. The REL takes only proc 0x00EB in the stage named exactly `M_DragB`, so never an X refight stage. Go asks only a REL of its own build (`inject.RELBuildID`); another build gets no request and one log line naming both ids. The receiver's log prints `boss_kill <seq>: <result> (m02E4 0x.., state 0x.., m02E0 n)` once per change of result, phase or action (the fight's m02E4 0..2 count as one), not once per seq: `waiting` and `no actor` are asked again every 500 ms, `none` (consumed at the keeper's create) too, `applied` and `already` not for 45 s.

Offline (`go test -count=1 ./mods/coop/replica ./wwapi/mailbox ./internal/inject/...`): `TestBossHeaderPins`, `TestBossRequestPlace` (the gap +0x510..+0x520 inside RemoteBlockRegion, outside the canary arenas), `TestBossReadAndFields`, `TestBossKillWanted` (only with the bit held and the game in M_DragB, a boss stage, with DRC loaded; never `Xboss0`; not once the local save has the bit), `TestBossGuardRules` (the guard takes the proc and tag only as 0x00EB and 'BK', on pathBoss alone), `TestBossKillRelay` (asked again while waiting and after a swallowed request, not during the death demo, stopped once the game has the bit), `TestBossKillNeverInRefight`, `TestBossKillNotWhenLocalHasIt`, `TestBossKillNoREL`, `TestBossKillWrongREL` (another REL build: no request, one line), `TestBossKillLogOnce` (an armored `waiting` with rising seqs and turning m02E4 logged once, the broken armor's again, `not acked` once), `TestBlobFresh`.

Rig: two Dolphins with `mp-local --coop` on one world, both players at the M_DragB boss door with the boss not yet killed on either file. Read the request and its answer with `WW_DOLPHIN_INDEX=<n> ./wwdev.exe peek 0x80413410 16` (seq, proc 00EB, tag 424B, ack, then the result byte, m02E4, mGohmaState and m02E0). Before the first live run: one REL build, the restamp and a recapture (the REL change rides in the same build as B1).

- [ ] **D4-0 boot**: on both Dolphins, before any request, ack equals seq at 0x80413410 (the keeper consumed whatever the slack held at its create); no `boss_kill` line in either log outside M_DragB.
- [ ] **D4-1a armored**: B in the fight with the armor intact (no rock hit yet, or one or two), A kills its Gohma. B's log: `waiting (m02E4 0x0..0x2, ..., m02E0 0)`, logged once (not once per seq) while B fights on; no death demo, BGM and materials unchanged. Drop the rock on B's Gohma meanwhile: the rock damage runs as vanilla. B breaks the armor: the next answer is `applied` with m02E0 1.
- [ ] **D4-1 forced from each pose**: B in the fight with the armor broken (m02E0 1, the eye phase), A kills its Gohma. B's log: `boss_kill N: applied`, and B's Gohma plays its death from wait, then (rerun) from attack and from the damage poses. Screenshot B: `end()` re-inits the DEAD bck, and the blend from a non-damage pose is unverified (design D4 (1)).
- [ ] **D4-2 receiver busy**: as D4-1 with B hanging on the grapple (Kui), then with B under Gohma's fire attack: the death runs, B is not stuck on the rope, no fire left burning on the boards.
- [ ] **D4-3 during the intro**: B is already in M_DragB and holds within 200 units of (-1534, 0, 909), which keeps Btd at m02E4 0xA without ordering its intro (d_a_btd.cpp:366-385), while A kills its Gohma; then B walks in. (B entering M_DragB after A's kill gets the bit live outside the boss stage, so no Gohma and no request: that is the Q6 path, not this check.) B's log shows `waiting (m02E4 0xA ...)` once, its intro plays out, then `waiting (m02E4 0x0.., m02E0 0)` through the armored fight, then `applied` once B breaks the armor. No death demo on a Gohma still at y -10000.
- [ ] **D4-4 the exit**: after D4-1 on B: the floor drops, the warp flower appears, the heart container spawns if STAGE_LIFE is clear (it does not if A already took A's), B's own save gets STAGE_BOSS_ENEMY (the planner's DRC hold line clears) and the flower warps B out (`Adanmae`).
- [ ] **D4-5 same frame**: both kill on the same frame (or within the demo): each log shows at most `already` and nothing applied twice; one heart container between the two files.
- [ ] **D4-6 never in a refight**: with the world's DRC boss bit set, stage-hop B to an X refight stage (`Xboss0`): no request (seq at 0x80413410 does not move), no `boss_kill` line.
- [ ] **D4-7 hop and recreate**: with a request answered `waiting`, hop B out of M_DragB and back. The keeper survives the hop (`inject/rel/README.md`, "Who creates the actor") and acks every request within a frame, so no `none` appears: the hold stays, the seq at 0x80413410 keeps rising, and after re-entry a new request gets its answer. `none` comes only from a keeper create with a seq in the slack: check it across a reset (D4-0's ack equals seq), not a hop.

Until a disc with this REL is in use, the playthrough rule stays: the second player waits outside M_DragB until the kill, then walks in and takes the warp; a player trapped inside dies and continues at the M_NewD2 entrance, or uses /warp.

### Enemy mirror: pot ambusher Bokoblins (DRC room 7)

Bokoblin types 2 and 3 hide in a pot (`tubo_wait`, d_a_bk.cpp:5052-5055: m02BA 0xFF, dr.mAction 15). They come out when the host's Link comes near or the pot goes; 3 leaps out (JATTACK1) and 2 jumps (JUMP1). Before this change the BK gate refused them. When the host killed one, the client's copy stayed alive at its spawn (hp 5/5) and ran its own AI. That was seen live in M_NewD2 room 7 (unkeyed 0xFFFF3203). Now `ParamsOK`/`LiveOK` take them, and the client writes mType 0 into its own copy before the block that offers it. The REL's `em_bk_gate` takes only a live 0/1/4, and after create mType 2/3 changes nothing but the leap animation. Once the REL reports the copy MIRRORED (and only then: a copy it does not hold runs its own AI, which a write would fight), the client sets its scale to 1 on the actor and the model's base scale (a copy that came out because its pot was gone is at half scale; only its own execute grows it back and copies it to the model, and the REL never does), and while the host's slot is HIDDEN it puts a copy that came out before the adoption back in its pot (m02BA 0xFF). A slot dropped before the REL ever took the copy gives its mType back. No REL change.

Offline (`go test -count=1 ./mods/enemysync ./cmd/wwdev`): `TestStaticGates` (types 2 and 3 taken, live 0 too; type 7 still refused), `TestBkAmbusher` (the DRC params; a switch-gated ambusher is never written 0xFF), `TestMirrorAmbusher` (mType 0 written once, before the one block write; nothing else before MIRRORED or when refused; once MIRRORED and HIDDEN, m02BA 0xFF and actor and model scale 1; no m02BA write after the reveal; a re-adoption with the host's out sets the scale only; the torch Bokoblin untouched), `TestMirrorAmbusherRestore` (a slot dropped before adoption writes the mType back), `TestClassify`, `TestEnemyMatchDry`, `TestEnemyKindWatch`.

Live, both sides `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u`, co-op on, hop `M_NewD2 0 7`:

- [ ] **Listed**: the host lists 4 enemies in room 7: the 2 keyed Fire Keese, BK `0xFF02FF40` and BK `0xFFFF3203`. Check with `./wwdev.exe enemy-match` on each side: the 0xFFFF3203 line reads `verdict=ok` with the same pkey on both.
- [ ] **Taken**: the client log has `enemy client: ... is a pot ambusher (mType 3); mType 0 written so the REL takes it` once. Its `enemy-match` then shows `bk_type=0`. The status line shows the slot adopted with no ERR_GATE.
- [ ] **Hidden**: with the host's Link away from pot #6, the client's Link walks up to its own pot and breaks it. No Bokoblin comes out on the client (the adopted copy is paused). Note where its stick is: see the stick gap below.
- [ ] **Reveal**: the host's Link goes near the pot (within 500 units: m02B5 0x32). The Bokoblin leaps out on the host and the copy comes out at the same spot on the client and follows the host's.
- [ ] **Kill**: the host kills it. The client's copy dies quietly (no item), and no hp 5/5 Bokoblin is left at the spawn. sw225 is set on both. The client reloads room 7: the Bokoblin is not created.
- [ ] **Client first**: `AmbushHides` counts only a copy that was already out when the REL took it, so the host must not list room 7 yet. Reload both with the host's Link outside room 7. The client's Link reaches pot #6 and breaks it: its own Bokoblin comes out (half size) and runs its own AI. Then the host enters room 7: when the client adopts the slot the copy goes back in (m02BA 0xFF; `AmbushHides` 1 in the stats) at full size, and comes out only when the host's does, full size. With the host already in room 7 the copy is adopted first and never comes out, and `AmbushHides` stays 0: that is the **Hidden** check, not a failure.
- [ ] **Regression**: rooms 0, 3 and 6 BKs (types 4 and 2), the r1 and r11 ambushers (`0xFFFF6443`, `0xFFFF1E03`, both unkeyed, no death switch): no ghost after a host kill. The room 6 pair (`0xFFFF0002`, setID 0 and 2) has m02B5 0, a reveal distance of 0: only the host breaking its own pot brings it out, and walking up does nothing.
- Room 7 fills the four block slots exactly (2 Keese, 2 BK). With a fifth enemy listed, the selection drops one.

Gaps:
- **Only the host's Link reveals an ambusher.** The client's Link near the pot, or the client breaking its own pot, does nothing until the host's comes out.
- **The stick shows while the copy hides (REL).** The host's hidden ambusher parks its stick at (-10000, -10000, 0) (d_a_bk.cpp:4219-4227) and its hidden execute returns before the stick placement. On the client the REL's `em_bk_attach` (enemy_mirror.c:2698) runs every frame on an adopted HIDDEN copy and puts the stick at its hand, and the stick draws by itself (daBoko), so it can float at the pot while the Bokoblin hides. Go cannot gate it per slot (the hook-disable bit is per kind and would freeze the torch Bokoblin's stick). Fix: skip `em_bk_attach`, or park the stick, while the slot is HIDDEN. Switch-gated hidden Bokoblins have the same issue.
- **A copy the REL does not take stays its own.** Refused (ERR_GATE, ERR_MORF, an early BOKO_ATTACH, ERR_ICE) or never given a slot, it runs its own AI and comes out on its own; Go does not write it.
- **Handback loses the leap.** A copy handed back while still hidden (the host left the room, a hold ran out) goes to action 0/0: em_handback knows only the create pairs. It comes out without the leap and patrols. A proper handback (action 15 for a still-hidden ambusher) is a REL change.
- **Pot-gone reveal scale.** When the host's pot is broken first, the host's Bokoblin comes out stunned at half scale and grows back. Scale is not streamed, so the copy shows full size.
- **Not covered here:** the M_Dra09 Kargaroc, the Moblin it carries, and the Magtails still have no mirror. Each game keeps its own copy, and an unkeyed one the host kills stays a ghost on the client (audit option B: a host tombstone for refused unkeyed enemies, a wire change).
