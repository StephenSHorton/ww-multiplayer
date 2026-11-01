# wind#20 runbook: release list R3-R10 + co-op M2-M6 (R6) on main's build, macOS

Card: wind#20 on the Irori "Wind Waker Multiplayer" board (parent wind#1). Build under test: main
c1278b3 (v0.6.1), BlobHash 0x04765DD0, REL 0x1CE30685. Rig: the Mac (Apple M5 Max, 128 GB), Dolphin
2603-342 in /Applications (the PC ran 2509). The rig itself comes from wind#3 (`docs/mac-rig.md`);
this folder only holds what wind#20 adds on top.

## What is still open (docs/TESTING.md "Release defaults")

| Row | State on main | What this card runs |
|---|---|---|
| R3 armed block | passed (LIVE-6 b1) | nothing |
| R4 unmirrored enemies | passed (LIVE-6 b1) | nothing |
| R5 T6 regression, defaults | in part: CC/BK kills, goldens, 8 hops, no adopt while gated | the KI kill (fly-type room `Cave09 0 1`, not the ceiling Keese of `M_NewD2 7 4`), the MO2 kill (a keyed Moblin: `M_Dai`), a `bk:tg_hit_up` override applying (`WW_ENEMY_ANNEX=bk:tg_hit_up` on a host restart), full-log sweep |
| R6 co-op M2-M6 | open | `mp-local --coop` per docs/08 §8.2: M2 dry run, M3 A1-A6 + R1-R4, M4 P1-P9, M5 Z1-Z6, M6.0 K1-K10, with live switches (default) and enemies on (default), plus one quiet-death kill: switch set, nothing doubles, room reloads clean |
| R7a stale connection | not run | suspend the joiner (`stop.sh -STOP join-B`) past 15 s, resume; pose never drops on A, enemies resume or the one `enemy sync off` line, lobby never ends. Then the host suspended (CLI host carries its server: check pose and enemies come back) |
| R7b Dolphin restart | CLI: FAIL in LIVE-6 b2 (flood since fixed per TESTING) | CLI host: restart A's Dolphin and the CLI host; expect the single `enemy host paused` + 30 s `still paused` lines, no flood. App variant needs the app window: `ui --browser` on 127.0.0.1 may stand in |
| R8 mixed builds | in part (hosting app showed no dialog) | v0.5.0's `ww-multiplayer-macos.zip` (release asset) against this build, both directions, CLI refusal + `ui --browser` pair; whether the host needs a notice stays Stephen's call |
| R9 upgrade path | passed | nothing |
| R10 escape hatches | in part (five run on loopback) | B -> A bomb with `WW_PROJECTILES=off`; `--coop-live-switches=off` on the host (a partner's switch in your room waits for your next room change); `--coop-boss-kill=gohma,kalle,molgera,jalhalla,helmaroc` leaves Gohdan's arena alone |

R1 (two machines, app) and R2's two-machine half are wind#13 (owl on the PC + a Mac joiner).

## Order

1. R5 rest and R10 rest on CLI loopback (`cli.sh`): one Dolphin pair, one boot.
2. R7a then R7b on the same pair (R7b restarts A's Dolphin last).
3. R8 with a scratch copy of the v0.5.0 macOS zip (`xattr -dr com.apple.quarantine` on the scratch copy only).
4. R6 on `mp-local --coop` with fresh fixture user dirs (binding writes a tag into the card: never on the shared originals).

## Mac caveats to check first

- Save states: F1 `saves/start.sav` and the fixture StateSaves were made by the PC's Dolphin 2509; 2603 may refuse
  them. Fallback (rules): cold boot + `wwdev auto-recapture`, which needs the `ww/rel-blob` lease. Ask wind#3
  for its result before trying.
- `ISO_PATH` must be set before any `stage-hop` (LIVE-6 b2: a bad point with no disc check hung the game).
  `env.sh` sets it to `~/ww-shared/disc/WW-mp.iso`.
- The CLI `host` listens on every interface (server.go `ListenHost` ""): `env.sh` exports `WW_LISTEN_HOST=127.0.0.1`.
  The Mac's application firewall is off (`socketfilterfw --getglobalstate`: State = 0), so no dialog either way.
- `M_NewD2 0 16` loops Link through void-outs on 28-Hyrule3 (LIVE-6 b2); skip it for the 8 hops.

## Scripts (rig/)

Ported from the PC's `archive/rig-scripts-2026-10-27` and `archive/ww-online-review/live/l6-b2-cli.ps1`.
Every process's PID is recorded in `$RIG_DIR/pids/` and stopped by that PID (never by command-line match).

- `env.sh`: paths (`WW_REPO`, `WW_SHARED`, `ISO_PATH`, `RIG_DIR`, `DOLPHIN_BIN`) and `WW_LISTEN_HOST`.
- `fresh-user.sh user-4|user-3`: this card's own copy of a fixture Dolphin user dir (also the card reset).
- `boot.sh user-N`: Dolphin with the MEM1 override and file logs (boot1k.sh), Start through the title, screenshot, PID.
- `drive.sh <pid>`: press A until Link is in play (driveg.sh).
- `healer.sh <pidA> <pidB>`: Link's life kept at max (s2v2-live/healer.sh).
- `cli.sh host|join <log> <dolphin-pid> <name> [port]`: one CLI host/join with only the `WW_*` vars given on its command line.
- `stop.sh [-STOP|-CONT|-KILL] <name>...`: stop, suspend or resume by recorded PID.
- `sweep.sh`: the full-log sweep (Go logs + both Dolphin logs) after every block.

Records of each block go next to this file as `LIVE-wind20-<block>.md`, with the build hashes.
