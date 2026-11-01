# What is left for full co-op (as of main c1278b3, v0.6.1)

This is the remaining-work list for a whole-game two-player co-op run. It was first written on 2026-10-26 against main e1b9086 (#199) in the owner's handoff folder (`ww-multiplayer-handoff/design/coop-remaining.md`). On 2026-11-01 it was brought into the repo and updated to main c1278b3 (#214): v0.6.0 and v0.6.1 shipped, plus #200-#214 and the live rounds LIVE-4 to LIVE-7. The ledger counts and section 2E also take in #216, #219 and #220, which landed on 2026-11-01 while this was written (tests, wind#10's macOS results, and the re-create tooling).

**Build at c1278b3.** REL 0x1CE30685 (`cmd/ww-multiplayer/testdata/cli-golden/patch-ok.txt`), BlobHash 0x04765DD0 (`internal/inject/blob.go`), network protocol 6 (`mods/multiplayer/net/handshake.go`), WWMB_LAYOUT 3 (`inject/include/mailbox_core.h`), coop-tables schema 12 (`wwapi/tables/tables.go`). The tag v0.6.1 is this commit. The game code is v0.6.0's (e4d2c6e); only Go changed between the two.

**Marks.** [V file] = read in this repo at c1278b3 for this update. [V live: record] = a recorded live run says so; records are in the handoff folder, cited as `archive/...` the way docs/TESTING.md cites them. [I] = inferred, never seen live. Items carried over unchanged from the e1b9086 pass keep that pass's judgement and were not re-read, unless a later PR or record touched them. TESTING.md boxes are named by their id (D4-8, RS1, ...), not by line, because the lines move.

**Kinds.** *Go* = Go-only, no REL build or recapture. *REL* = a REL build plus a save-state recapture. *research* = decomp or disassembly reading. *live* = a check on two Dolphins. *Stephen* = only the owner can do it: release decisions, tags, real controller play, a second machine or network, owner questions.

**Impact.** *BLOCKS* = without it (or its playthrough rule) a first whole-game run breaks or strands a player. *rule* = a playthrough rule or an off switch covers it for the first run. *polish* = the run works without it.

---

## Short answer

A first two-person run of the whole game still needs **no new REL work**. Since e1b9086:
- **Defaults (#211):** every boss now has a relay in the default rows: `BossKillDefault = "gohma,kalle,gohdan,molgera,jalhalla,helmaroc"` [V mods/coop/replica/bosskill.go]. Live switches and the projectile events are on by default [V mods/coop/session/session.go `startHub`; mods/multiplayer/fxsync/fxsync.go].
- **Fixes:** the soft reset no longer reboots (#202), and a parked peer boat floats on this game's water (#206). The dry-run bind refusal that LIVE-6 found is fixed (#213, passed in LIVE-7).
- **Ledger:** TESTING.md has 256 boxes, 116 of them ticked, against 64 of 256 at e1b9086. Most new ticks are "in part" under #183's convention.
- **Headroom:** RELS.arc has 7008 bytes left under the 64 KiB growth guard (+58528 bytes on the patcher golden fixture, not measured on the retail disc), so the next REL batch is tight.

What is left falls in six parts:

1. **Default-on code that never ran the way a player meets it.**
   - Kalle Demos has never applied through Go on two Dolphins. Gohma has, but only with its armor and A's lethal write poked (D4-8 [V live: archive/ww-online-review/live/LIVE-6.md:327]). Gohdan joined the default rows with GD-4, its natural fight, still owed; LIVE-7 saw its Go relay `waiting` after a poked kill bit [V live: LIVE-7.md:26].
   - These default-on features have rows in TESTING.md but no recorded run yet:
     - relay scoping (RS1-RS7);
     - salvage catch-up (SV1-SV7);
     - the soft reset that reboots (SR-2);
     - boats' regression sweep (BT-10);
     - the third remote with shared enemies (R3-3).
2. **Rule-gating live checks.** Still owed:
   - M_DaiMB (no save has its Stalfos switches);
   - GanonK D2(7) and GTower sw31;
   - M2tower from sea r1;
   - FF D2(2)/D3;
   - Medli (ET C6) and Makar (WT C3);
   - TotG r12 (T5b).
3. **The overworld after #177.** No overworld live check has run, TESTING.md has no rows for them, and the ZELDA_AWAKENED hold is an open design choice.
4. **The rules card.** It was re-verified on 2026-10-27 at 3de6e6b. S3-S6 now carry v0.6.1's defaults, but T3 and its §3 table still call Gohdan's row off by default [V `ww-multiplayer-handoff/design/coop-rules-card.md` T3, §3].
5. **Saves.** These are still missing:
   - a save with the M_DaiMB Stalfos killed on one side;
   - a GanonK-cleared save (for GTower);
   - a casual-clothes save;
   - a save with bombs and arrows equipped on buttons.
6. **Stephen-only items.**
   - Two machines (R1, the release gate), WAN (R11) and a Mac (R12).
   - Owner questions.
   - The playthrough itself.

---

## 0. Changes since e1b9086 (#200-#214)

| PR | What | Effect on this list |
|---|---|---|
| #200, #204, #212 | TESTING ticks from LIVE-3/4/6 and the ST1 record; stale texts | the testing-ticks-e1b9086 list is applied (ST1-2..ST1-7, L4-0, V5, V7, GF-1, BT-1 "in part", R8 protocol 6, F1, KI default, the two flakes) |
| #201 | REL: the light setup guarded on a null view; the Ship.arc getRes log flood stopped | the BR-3 zip's 4 MB of OSReport spam (LIVE-5) should be gone [I] |
| #202 | T2: pose and tunic buffers from the GameHeap tail | the soft reset no longer reboots: SR-1, SR-1b pass; SR-2 open |
| #203 | GUI Boss kills tooltip and comments | 1.5's stale tooltip fixed |
| #205, #206 | pose-fake-loop replays the ship and place trailers; a parked peer boat floats | BT-4..BT-9 passed or passed in part; BT-10 open |
| #207 | owner key redacted in bug reports; `status` exits 1 for another program; Help card and MSYS home | LIVE-5's four bugs fixed; DS6 and BR-3 want a rerun on this build |
| #208, #210 | v0.6.0 notes; v0.6.0 released 2026-10-27 (e4d2c6e) | section 3 |
| #209 | enemy sync pauses and resumes on a game that cannot be read (R7b) | R7's second half has a code path; the live check is open |
| #211 | defaults: live switches, projectiles, Gohdan's row | decisions A3, B1, C1 below are made |
| #213 | a dry-run bind judges story like the world; Setup flags a stale patched disc | M2 dry run binds again (LIVE-7) |
| #214 | v0.6.1 notes; v0.6.1 released 2026-10-28 (c1278b3) | section 3 |
| #216 (after the tag) | the TestUpdateInstall race fixed; gofmt on three test files | the Known flakes row is gone |
| #219 (after the tag) | wind#10's macOS results for #187, #189, #193 | DS6 passed; DS7, BR-2, GF-1 and No DolphinTool in part (2E) |
| #220 (after the tag) | `wwdev rf-ask`, the RefreshRows table check, docs/live/wind19-recreate-runbook.md | the tooling for RC-7..RC-10 and Tier 3 (2C) |

### Where the slice work lists stand

Carried from e1b9086 unless the row says otherwise.

| Slice item | State at c1278b3 |
|---|---|
| DRC A1, A2, A6, B5 Gohma relay; B0 Darknut row; A3-A5; A7 Keese on; B2 layout 4; B3 Kargaroc (off); B4 Magtail (`mt+u`) | landed. A6 never run (the rig can't trigger a host event) |
| Live switches on by default (every slice's A-item) | **landed in #211** (v0.6.1) |
| DRC A9 boss-hold release for a co-present partner | not built; overtaken by the boss_kill relay [I] |
| DRC A10 `CoopSwitchSync` wiring | not landed (still nil) [V mods/multiplayer/engine/game.go] |
| DRC B7 stale actors | Tiers 1-2 on with combat; Tier 3 opt-in (`--coop-refresh`); re-create rows opt-in; door rows compiled out (`WWRF_DOOR_ROWS 0`). The appear-camera suppression was never built |
| DRC B8 host health on handback | not landed |
| FW A1, A2, A4; A3 + B1 Kalle relay | landed; F1-F11 by poke |
| FW A6 readers, B2 Ss/GM refresh (not needed), B3 rows | not landed |
| TotG A1, A2 | landed; **T5b: B's sw14 dropped after a broken `Siren 0 12` load and was never rewritten** |
| TotG A3 EP torches, A6, A7, B2 | not landed; T7 cannot run |
| TotG A4 + B1 Gohdan relay | landed; **default row since #211**; GD-4 natural fight still owed |
| ET A1, A2; ET A4 + B1 Jalhalla; WT A4 + B1 Molgera; WT A1 Makar trees | landed; Jalhalla and Molgera passed with real items |
| ET A3, A6, A7; WT A2, A3, A5, A7, B3 | not landed |
| WT B2 Vmc grow refresh | the opt-in `vmc` re-create row; RC-7 not run |
| FF A1 sword hold | landed; **FF D2(2)/D3 never run, and TESTING.md has no row** |
| FF A3 + B1 Helmaroc relay | landed, default row |
| FF A2 decoders, A4 table-2 hold release | not landed (A4 out of scope by choice) |
| GT A2, A7 (+ #175, #185) | landed; GT R4 and R1 pass |
| GT A1, A3, A4, A5 | not landed |
| Overworld A1-A3 (#177) | landed; no live check |
| Overworld A1 option, A4 diagnostics | not landed |

---

## 1. Blockers for a whole-game two-person run

**1.1 Tier 1's live key-door lock write.** *rule at most.* The ST1 record (`archive/coop-batch2/live/LIVE-3-doors.md`, on e1b9086) ran ST1-1 to ST1-6 with no freeze on ww-online's own path, including live switches. It keeps the Tier 1 write and finds the DOOR10/DOOR12 re-create rows not needed for small-key doors. TESTING.md now carries those ticks (ST1-1..ST1-6) and ST1-7's rewrite.
- Still owed against ww-online's pass bar: one approach from the back side; B "comes back through"; ST1-2's `/status` hold text (needs mp-local with a console); a later-dungeon KDDOOR door, if Tier 1 is ever extended to it.
- `inject/include/wwrefresh.h` still says `WWRF_DOOR_ROWS 0` is "pending the ST1 key-door live check". ST1 has decided. The comment can change only with the next REL build, since any edit there moves blob.stamp [V CLAUDE.md].
- Stephen confirms the record's call (cluster A, section 3).

**1.2 Default-on relays and features not yet run as a player meets them.** *live.* Each has an off switch or a fallback rule, except salvage catch-up and the soft-reset gate.
- **Gohma.** D4-0, D4-1a, D4-1 by poke on one Dolphin. D4-8 passed in part through Go on two Dolphins (B's relay `applied`, with B's armor and A's lethal write poked) [V live: LIVE-6.md:327]. LIVE-6's R6 block saw the relay `waiting` through Go with a poked boss bit [V live: LIVE-6.md:280]. Open: D4-2..D4-7, a natural fight. Fallback rule: enter after the kill (passed in M_DragB).
- **Kalle Demos.** KD-0 only; KD-1 and KD-2 in part by poke on one Dolphin. **No Go-driven two-Dolphin apply on any REL.** Open: KD-1..KD-7. Fallback rule: enter after the kill (passed in kinBOSS).
- **Gohdan (default since #211).** GD-1..GD-3 passed; LIVE-7 saw the Go relay hold the bit and ask the REL, `waiting` for the mouth [V live: LIVE-7.md:26]. Open: GD-4, a natural fight (the rig never hit both hands in one window), and the apply itself in a real fight.
- **Molgera, Jalhalla, Helmaroc.** Passed with real items or a real hammer fight, on one or two Dolphins (carried). Open: B in the intro, B re-entering after a death, the Warpf targets, Helmaroc (b)-(e).
- **Same-build regression.** One pass of all six relays through Go on 0x1CE30685 closes the batch D/E/F regression boxes together (the tonight card "Boss relay regression via Go").
- **Relay scoping** (`WW_RELAY_SCOPE`, server, default on): RS1-RS7, RS2b and RS4b have no run. R10's hatch passed in part ("relay scope off", by the log) [V docs/TESTING.md R10].
- **Salvage catch-up** (#195): SV1-SV7 never ran. There is no feature bit and no env off switch; only a dry run or co-op off stops it [V mods/coop/replica/salvage.go]. It touches the Triforce hunt.
- **Soft reset:** SR-1 and SR-1b pass since #202; SR-2 (a reset that reboots) open.
- **Peer boats** (`WW_BOATS`, default on): BT-1..BT-9 passed or passed in part; BT-10, the regression sweep, is open.
- **Three remotes** (layout 3): R3-1 in part, R3-2 and R3-4 pass, R3-3 (the third remote with shared enemies) open. A two-person run uses one remote, so this is polish for that run.
- **Projectiles** (default on since #211): P0, P1, P2, P3, P7, P8 pass; P4, P5, P6, P9 in part; P10 in part (LIVE-7's smoke and one real bomb on 9d30831; the enemy mirror's B1 not run). The notes' "not yet checked live": breakable walls, boulders and crystal switches hit by a copy, and two machines.
- **#186 boss heart container once:** D4-8 passed in part (A first, B held); the leave-without-taking rerun and the mirror (B first) not run.

**1.3 The overworld after #177.** *live, research, Stephen.* rule for the first run. Carried unchanged:
- Landed: A1, A2, A3. Every overworld start event is per player except GanonK `kugutu_ganon` (sw30) and GTower `g2before` (sw31).
- Open: the ZELDA_AWAKENED hold (sea r26's Gdemo20 sets 0x1820, which syncs; "finish FF2 together" covers it); C1-C5; the bind's "far behind" milestones lack 0x0520; the slice's Q5 (a fresh file binding to a world with 0x0101/0x0E20 but no 0x2401 plays Outset alone up to Windfall); the slice's "still unverified" list; owner Q1 and Q2.
- D1 fixtures (a)-(f), D2 (1)-(9) and D3: none run, and **TESTING.md has no rows for them**.

**1.4 Rule-gating live checks.** *live.* BLOCKS through the rules card.
- Passed: entry after the kill in M_DragB, kinBOSS, SirenB, kazeB, M_DaiB and M2tower; SirenMB and kazeMB; GT R4 and R1.
- Owed: M_DaiMB (no save), GanonK D2(7), GTower sw31 (needs a GanonK-cleared save), M2tower from sea r1 (the forced-layer hop left the spikes up), FF D2(2)/D3, ET C6 Medli, WT C3 Makar, GT D2(1)-(8), TotG T5b (a clean r12 walk-in).

**1.5 The app path.** *Go + live.* rule (the CLI path works).
- Done: the per-player relays in the app (#176), the lobby's Live switches box, the host's rules mid-session (#198), and the Boss kills tooltip (#203).
- Left: the App window boxes and App-R1..R5 have never been run in the real window. The TUI has no relay options. Env-only knobs (`WW_ENEMY_MIRROR_KINDS`, `WW_ENEMY_REMOTE`, `WW_BOATS`, `WW_NAMETAGS/EQUIP/SEACHART/HAIR`, `WW_RELAY_SCOPE`, `WW_PROJECTILES`) are not a gap for a first run on the defaults [I].

**1.6 Live switches.** Done: on by default since #211. The camera steal (a partner's switch playing a door or camera event in your room) is real and expected: the rules card's G2.

**1.7 The rules card.** *doc, no build.* `ww-multiplayer-handoff/design/coop-rules-card.md` was rewritten at 3de6e6b on 2026-10-27, and S3-S6 already name v0.6.1's defaults. Left: T3 ("Without row `gohdan` (the default leaves it out)") and the §3 "Partner won while you were inside, row off (Gohdan by default ...)" line now describe a row left out by hand. The §5 PROV table wants the LIVE-6 and LIVE-7 records. Consider moving the card into the repo next to this file, so it is versioned with the code it describes.

**1.8 Saves and throwaway cards.** *rig.* BLOCKS the checks that need them.
- In use: the CryZe `28-Hyrule3` save on both fixture cards (md5 3162e4a9...), and `4-WindWaker` for DRC co-op (LIVE-6, LIVE-7).
- Still needed:
  - a save with the M_DaiMB Stalfos killed (sw63-65) on one side; check the slice's wider sw59-65 range;
  - a save where A has cleared GanonK, for GTower sw31;
  - a casual-clothes save, for V2 and V9;
  - a save with bombs and arrows equipped on buttons (an `mSelectItem` poke does not equip them), for the AM boxes and the rest of the projectile rows;
  - the overworld D1 fixtures (a)-(f);
  - walk-in routes for DRC r16 and TotG r12 (both hops void Link);
  - a non-fixture card for a real death or continue.
- The docs/08 F2-F5 fixtures are still uncaptured; they gate the rest of R6 (M3 A4-A6, M4 P3-P9, M5, M6 K3-K10).

**1.9 Two machines for two people.** *Stephen.* BLOCKS unless both play at one PC. No pass has run on two machines, over WAN or on macOS. R2 passed on loopback on one PC; R1 is the gate.

**1.10 The whole-game playthrough.** *Stephen + partner, or two agents on two Dolphins.* The goal; not started. No slice's D5 is on record; every live check so far is a rig check driven by pokes, scripts or keystrokes. Sweep both full logs and both dolphin.log files for ISI/DSI after each session. GT Q5 is answered and built (#218, docs/08 item 170): a player's game leaves co-op at the credits and the others play on.

---

## 2. Live checks owed

TESTING.md at this update: 256 boxes, 116 ticked, 140 open. Most ticks since e1b9086 are "in part": read the parenthetical before trusting one.

### 2A. Checks that gate a playthrough rule

| Check | Gates | State |
|---|---|---|
| Entry after the kill: M_DragB, kinBOSS, SirenB, kazeB, M_DaiB, M2tower | "enter after the kill" | pass in all six (M2tower by a forced-layer hop; the tower-top spikes stayed up) |
| M2tower from sea r1, ff-m2tower-exit (a)-(e) | the Helmaroc exit | not run |
| Miniboss entry: SirenMB, kazeMB | miniboss rules | pass |
| Miniboss entry: M_DaiMB (ET D2(7)) | card E4 | untestable until the save in 1.8 |
| GanonK after the kill (GT D2(7)); GTower sw31; GT R2/R3, D2(1)-(8), a real Puppet Ganon kill | GT rules | not run (GT D2(9) R4 and R1 pass) |
| FF D2(2) sword reproduction, D3 swordless majroom; FF D2(3)-(5), (7) | FF rules | not run; **no TESTING row** for D2(2)/D3 |
| WT C3 / D2(2) Makar; ET C6 / D2(9)-(10) Medli | card N2, E3 | open |
| TotG T5b, r12's swflat vs the synced sw14 | TotG A1's exception | open finding |
| Overworld D2 (1)-(9) | card G9/H1/H2 | none run; no TESTING rows |

### 2B. Landed Go table changes (the D3 checks)

Carried from e1b9086: the DRC, FW, TotG, ET and WT D3 checks synced on f9e1fe6, almost all by poke, many for sync only. Owed: DRC's real r16 AND puzzle; FW's real sword cuts (none registered on the rig); TotG T7 (cannot run), T1 reload, T4 lift; ET E2 stairs and the A2 walls; WT W1-W6 judged on screen; #191's side-quest registers (Orca, Koboli, the pig pen) live.

### 2C. Relays, enemy rows, refresh, re-create and doors

- **Boss relays:** see 1.2.
- **Tier 1-2 refresh:** ST1-1..ST1-6 and ST2-1 pass; ST2-2 in part (LIVE-6 saw the claimed hold keep Tier 2 out on the pot ambusher; `/status` not read), ST2-3 open.
- **Tier 3 refresh** (`--coop-refresh`, off by default): RF-1, RF-2, RF-5 pass; RF-3, RF-4, RF-6 in part; the two-Dolphin refresh box has stone2 and tbox; leaves, item, saku and `dry` over Go are open. #199 rewrote `refresh.c` into tables, so the delete rows want a rerun on this REL.
- **Re-create rows** (#199, opt-in): RC-1 and RC-3..RC-6 passed in part by poke, RC-2 passed on two Dolphins (LIVE-6). RC-7..RC-10 open, with a runbook and `wwdev rf-ask` for them (#220, docs/live/wind19-recreate-runbook.md); RC-11 doors not needed for small-key doors (1.1). The fixture switches for the poke recipe (TESTING "actor_refresh re-create") are unverified.
- **Enemy kinds:**
  - Keese (on by default): the wait-type wake, ceiling return and Fire Keese flame have no record.
  - Armos (AM2): the hammer one-hit and the statue handback have no record.
  - Armos Knight (AM): the eye arrow, light arrow, bomb swallow and hookshot have no record (no equipped bombs or arrows).
  - Darknut: T4 (the cave Darknuts, unkeyed) has no record.
  - Moblin guard and parry: never exercised.
  - T6 (every kind on): no run on a build with `bb` and `mt+u`.
  - Batch C: L4-2..L4-4, AMB-2, AMB-4, BB-2, BB-4, BB-5 have no record.
  - Magtail: MT-L2 in part, MT-L3 not run.
  - Research: what calls `fopScnPause_Disable` on a copy's arrival.
- **Remote bodies, Stage 4** (`full_bodies`, off): S4-1 no record; S4-12 in part.
- **Co-op M2-M6 acceptance (= R6):** in part. LIVE-6 covers M3 A1-A3, M4 P1-P2 and M6 K2 on DRC, and LIVE-7 covers M2 after #213. The rest needs F2-F5 and a console mp-local.

### 2D. Wave 3 and the visuals

| Area | Default | State | Owed |
|---|---|---|---|
| Projectiles | on (#211) | P0-P9 pass or in part; P10 in part | P10's B1 with projectiles on; breakable walls, boulders, crystal switches; two machines |
| Re-create rows | opt-in | RC-1..RC-6 (above) | RC-7..RC-10 |
| Soft reset | on, no switch | SR-1, SR-1b pass (#202) | SR-2 |
| Casual hair (V9), Master Sword scabbard (V8), outfit (V2), equipment (V3), sweep (V6) | on | not run | needs the casual-clothes save; V3's core case (Hero's Sword and Shield) |
| Name tags (V4), sea chart (V5), mirror shield (V7) | on | in part | V4's events and menus, V5's heading and sailing, V7 raised in hand |
| Three remotes | layout 3 | R3-1 in part, R3-2, R3-4 pass | R3-3 (a third real game) |
| Peer boats | on | BT-1..BT-9 pass or in part | BT-10 |
| Salvage catch-up | on | nothing | SV1-SV7 (real crane pulls on B) |
| Relay scoping | on (server) | nothing recorded | RS1-RS7, RS2b, RS4b; RS7 needs a real third game |
| Host rules mid-session, Room items, Warp to | app | nothing | App-R1..R5 in the real window |
| `wwdev smoke` | tool | SM1 pass (rerun on 9d30831 in LIVE-7), SM2 and SM4 in part | SM3 co-op |

### 2E. The #186-#199 product checks

| PR | State |
|---|---|
| #186 boss container once | D4-8 in part; D4-5 open |
| #187 RVZ/WIA/GCZ | the CLI RVZ rows and ISO-vs-RVZ pass (LIVE-6); No DolphinTool's macOS form passed (#219); the app rows, WIA and GCZ not run |
| #188 `--fix-sea-heap` | the patch line, mixed fix and the sea passed or in part (LIVE-6); the app re-patch not run |
| #189 bug report, game-files guard, notes | BR-2 in part on macOS (#219: the zip path and browser mode; Finder's Show folder not run); BR-3 in part (LIVE-5, before #207's fix); BR-1 not run; GF-1 in part (the failing half run locally, #219; no PR workflow exists to run it on Actions); RN-1 needs a prerelease tag |
| #190 the app window | never seen in the real window |
| #192 Dolphin file logs, run logs, crash-context | DT1-DT5 open; DT6 moot (F1 cleared) |
| #193 owner key and status | DS4 passed on one PC; DS1, DS2, DS5 in part on one PC, other machines owed (LIVE-5); DS6 passed on macOS against a real v0.5.0 server (#219); DS7 in part (Docker on macOS, DS1 against it not run); DS3, DS8, PG1, PG2 not run |
| #194 remote visuals | F1 cleared; V rows in 2D |

---

## 3. Release

**State.** v0.6.0 (e4d2c6e) was published 2026-10-27, and v0.6.1 (c1278b3) on 2026-10-28. The v0.6.1 notes describe main as it is: no game-code change since v0.6.0, the three new defaults, the dry-run fix and the stale-disc fix, and protocol 6, so v0.6.1 plays with v0.6.0 [V docs/release-notes/v0.6.1.md against the code, 2026-11-01]. The release list R1-R13 was never fully run. R3, R4 and R9 pass; R2, R5, R6, R8 and R10 are in part; R1, R7, R11, R12 and R13 are open.

**Decisions made since e1b9086** (by the owner, in #211): A3 live switches on by default, all stages; B1 Gohdan in the default rows with GD-4 still owed; C1 projectiles on by default, with `WW_PROJECTILES=off` named in the notes.

**Still open for Stephen:**
- **Cluster A:** confirm the ST1 record's call (Tier 1 stays; `WWRF_DOOR_ROWS` stays 0).
- **Cluster B:** `--coop-refresh` stays none (RC-* open); `tn`, `am`, `am2`, `bb`, `mt+u` and `full_bodies` stay off; Keese stay on.
- **Cluster C:**
  - C2 boats stay on now that BT-1..BT-9 have records;
  - C3 relay scoping: run RS1-RS4, or document `WW_RELAY_SCOPE=off`;
  - C4 `WW_ENEMY_STATE_30HZ` from R11's data;
  - C5 salvage catch-up has no off switch.
- **Process:** what gates the next release (R1 on two machines at least); whether to note relay scoping, boats and three remotes as lightly tested; the owner questions (docs/08 §10; FF Q4's ZELDA_AWAKENED residual; whether M6.1 is still wanted given the `+u` mirror).
- **Naming:** the release items R1-R13 share names with App-R1..R5, R3-1..R3-4 and GT R1/R4. Renaming them (RD1..RD13, say) is open; records and PR bodies cite the current names.

**Doc leftovers** (carried; not re-read for this update): docs/05's missing entries (projectiles, re-create, relay scoping, the Tier 1 door question, relay head-of-line blocking); docs/08 :3, :10, :59, :622, :1103; docs/10, docs/01, README, site. Optional: add `inject/rel` to release.yml's timestamp gate (TestBlobFresh already covers it).

---

## 4. Polish and later (state-sync gaps and the rest)

Carried from e1b9086. The rows that tonight's cards under "Full co-op state sync" (the Irori board "Wind Waker Multiplayer") take up are marked **(card)**.

| Item | Kind |
|---|---|
| **M6.1** placement-matched kills of unkeyed enemies (the room-clear blocker in the 14 mixed rooms, docs/08 :742, :749-750, :2087) **(card)** | Go |
| **M9** chest-claim arbitration (docs/08 §6.7 :621-626, :59, :1103; its text predates Tier 3 and re-create) **(card)** | REL / Go |
| Enemy rows never built: Stalfos ST 0xBF, the warship OSHIP 0xB4, Molgera larva, Kalle Demos tentacle, Puppet Ganon 2-3; AM2 eye-arrow paralysis, the AM/AM2 hookshot steal; allow-design still_open S1-S7 **(card)** | REL + research |
| ~~GT Q5: the session end at one player's credits~~ built (#218, docs/08 item 170; live Q5-1/Q5-2 owed) | Go |
| ~~Co-op for three or more~~ built (wind#9, schema 2, docs/08 item 171; live C3-1..C3-4 owed) | Go |
| GT A1 sw0 (needs C1), A3, A4, A5; C2-C6 | research → Go |
| TotG A3 EP torches, A6, A7, B2; ET C2 → A3 coffins and tapestries, A6 M_DaiMB hold of sw59-65, A7; WT A2, A3, A5, A7, B3 | Go (+ research) |
| FW A6, B3 rows; FF A2, C2, C6, C7 | Go / research |
| DRC A10 `CoopSwitchSync`; B8 host health on handback; the appear-camera suppression; push blocks | Go / REL |
| Overworld A1 option (0x0520 in the bind milestones), Q5, A4 diagnostics | Go, Stephen for Q5 |
| Default flips for `tn`, `am`, `am2`, `bb`, `mt+u`, unkeyed `mo2`, `full_bodies`, after each kind's live checks **(card)** | Go |
| **M7** C co-op pump (getSave/isActor shims, KILL op, `SET_NEXT_STAGE`, `/warp` beside the partner) | REL |
| **M8** TUI co-op panel, behind-by-N, soaks with a clean `save-diff` | Go + live |
| docs/08 §7.3 Post-1.0: a faithful per-class death, optional shared HP, a remote-sword flinch | REL |
| #191 left-outs: Maggie, the withered trees, Sploosh, barrel and Bird-Man records | research → Go |
| #193 left-outs: no TLS; member slots can be filled by anyone who reaches the port | Go |
| #189 left-outs: co-op journals not in the bug-report zip; the game-files guard reads outer headers only | Go |
| Relay scoping left-outs: sea-distance and room-level scoping, a server HIT gate, slot take-back, per-sender rate buckets; relay head-of-line blocking (`mods/multiplayer/net/enemy.go`) | Go |
| Dedicated server: the enemy-host role goes to the first enemy-host connection; no mods-hash check | Go |
| Boats left-outs: head look and mouth, hull recolour, crane rope, wake/shadow/collision, Ship.arc on a shipless stage | REL |
| Salvage: the crane-mode gate; same-square ocean-bit timing (the Tingle Tuner's per-frame bit-15 read) | Go / research |
| #187/#188/#192/#196/#198 left-outs (NKit, `--dolphin`, a TUI/app switch for `--fix-sea-heap`, Logger.ini fallback, REL symbols in crash-context, rules in the lobby before Start, shared wallet and bags, item icons) | Go |
| A fourth remote: a new layout, an ATTRIB byte with room, a co-op protocol change | REL |
| Internet play: R11 over a port forward or a VPN; no NAT traversal | live / Stephen |
| A one-line `co-op: boss-kill rows: ...` at session start, so a release check can read the active rows from the log (LIVE-7 note 1) | Go |

**Rig limits.** Live checks share a few Dolphin slots (Irori leases `ww/dolphin-slot-*` tonight). Other programs' memory use blocks boots. Hops into DRC r16 and TotG `Siren 0 12` void Link. Real sword cuts on FW plants did not register on the PC rig. Lock-on and guard need the analog trigger byte 0x80413078. `mSelectItem` pokes do not equip X items. A pose-fake sends no HITs and no place.

---

## 5. Order of work

Tonight's board (2026-11-01, "Wind Waker Multiplayer", parent "Full co-op state sync") splits this list into desk cards and live cards. At most one REL build per batch: every relay shares `inject/rel/bosskill.c`, and RELS.arc has 7008 bytes left.

**Desk (no Dolphin).**
1. The ledger (this file, the TESTING ticks).
2. M6.1, M9, the missing enemy rows research, GT Q5 and 3+ co-op.
3. The macOS product checks (#219 ran most): the app RVZ rows, PG1, PG2, DS7's DS1.
4. The rules card's T3/§3 Gohdan lines and its PROV table (1.7).
5. TESTING rows for FF D2(2)/D3 and the overworld D2 checks.

**Live (two Dolphins, Go-only fixes allowed).**
1. The saves in 1.8.
2. The boss relay regression through Go (all six, Kalle Demos first); then GD-4.
3. A PC host and a Mac joiner (R2, then R1).
4. Projectiles with equipped items; boats BT-10 and SR-2; three remotes R3-3 and relay scoping RS1-RS7; salvage SV1-SV7; the V rows.
5. Enemy kinds and the default-flip evidence; re-create RC-7..RC-10 and Tier 3.
6. The release list R5-R8, R10, and R6's rest.
7. Rule gates (M2tower from sea r1, GanonK D2(7), GTower sw31, M_DaiMB, FF D2(2)/D3, T5b, Makar, Medli), then the overworld D2.

**Then:** one REL batch if anything above needs it (with a recapture); the play-together runs, dungeon by dungeon (each slice's D5 rows); Stephen's two-machine, WAN and macOS items; the next release.
