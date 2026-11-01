# wind#12: boss relay regression on two Mac Dolphins over Go

Card wind#12 (board "Wind Waker Multiplayer", parent wind#1). Build: main c1278b3 (v0.6.1, BlobHash 0x04765DD0, REL 0x1CE30685).
Retires, where they pass: docs/TESTING.md "Regressions on the same build" rows at :515 (batch D: Go-driven Gohma + Kalle Demos),
:571 (batch E: + Gohdan) and :653 (+ Molgera, Jalhalla), plus the Helmaroc two-Dolphin relay (HK rows).
Waits on wind#3 (Mac Dolphin rig): patched disc, fixtures in ~/ww-shared, re-signed Dolphin, two-Dolphin loopback.

## Rig (rig/)
Use wind#3's `scripts/mac-rig.sh` (docs/mac-rig.md) for launch/mp-local/shot/stop: `setup wind12`, lease a slot,
`pair wind12`, `mp-local wind12 --coop=new --coop-boss-kill=<rows> --coop-refresh=none --yes` (background), `stop wind12`.
The start save is `fixtures/start-mac-1CE30685.sav` (PC states do not load on Mac Dolphin 2603). `boot.sh`/`mpl.sh`/`stop.sh`
below predate it and stay only as a fallback. `bits.sh`/`force.sh` read `rigs/wind12/pair.pids` (A = first, the host).
- `env.sh` paths (override by env). `boot.sh A|B` copies the fixture dir to `$RUN/ud-A|B` and launches through
  `open -n -a` (LaunchServices; a direct exec of Contents/MacOS/Dolphin loads the hardened binary and attach fails,
  see wwapi/emu/launch/launch_darwin.go). PIDs go to `$RUN/A.pid`, `B.pid`.
- `mpl.sh <log> [rows] [refresh]` = the PC's mpl.ps1: mp-local `--coop=new --coop-boss-kill=<rows> --coop-refresh=none --yes`.
- `bits.sh` (source it): `on A|B`, `pk`, `clr`, `set1`, `stage`, `req`, `dword t`, `boss_alive t`.
- `force.sh A|B <boss> [req]` rig shortcuts (Btd armor, Bmd open flower [I], Bpw JH-1). Real fights are preferred; log every poke.
- `sweep.sh <log>` relay lines + ISI/DSI sweep across mp-local, Player and dolphin logs. `stop.sh` kills by recorded PID only.
- Screenshots: `mac-rig.sh shot <pid> <png>` (Screen Recording is granted on the rig Mac).
- Lease one `ww/dolphin-slot-N` for the pair. No `ww/rel-blob` (no REL change on this card).

## Setup (once per session)
1. `boot.sh A; boot.sh B` on 28-Hyrule3 fixtures (or the start state wind#3 confirms), get both in play.
2. `. rig/bits.sh; on A; boss_alive 3; boss_alive 4; boss_alive 5; boss_alive 6; boss_alive 7; boss_alive 2` then the same `on B`
   (bit 3 cleared, intro bit 5 kept, both files identical: re-peek and compare words) **before** mp-local starts.
3. `coop-tag clear --yes` on each if a tag shows. `mpl.sh w12-run1.log` (default rows, no gohdan).
4. Bind pass: `bound the file`, `armed in world`; no `can't bind` / `far ahead`.

## Per boss (both hop the arena; A kills; B is the receiver)
| boss | table | hop (both) | A kills by | B reaches the gate by | B's expected lines |
|---|---|---|---|---|---|
| Gohma (0xEB) | 3 | `M_DragB 1 0` | `force.sh A gohma req` | real armor break, or `force.sh B gohma` | `the partner killed ...`, `boss_kill N: waiting (m02E4 .., m02E0 0)` once, then `applied` once; floor drops, Warpf, heart, B's t03 bit |
| Kalle Demos (0xEC) | 4 | `kinBOSS 1 0` | `force.sh A kalle req` | flower falls open, or `force.sh B kalle` | `waiting (mMode ..)` once per change, `applied (mMode 0x3, m302 n, m304 n)`; Makar's drop / Warpf to `Omori 6 0` |
| Gohdan (0xF2) | 5 | `SirenB 1 0` | gdforce recipe + req (opt-in run with `gohdan` in rows) | gdforce recipe, no req | `waiting (action 0x1 ..)`, `applied (action 0x7, mDamage 3, m2E7C 12)`; default-row run: `leaves it out` once |
| Jalhalla (0xD4) | 6 | `M_DaiB 0 0` | health 0 + `force.sh A jalhalla req` | real freeze / `force.sh B jalhalla` | `waiting (fight ..)` once, `applied`, next spike throw kills; t06 bit lands |
| Molgera (0xDA) | 7 | `kazeB 0 0` | real Hookshot pull + req | real pull | `waiting (m18AE 0x1 ..)`, `applied (m18AE 0x3, m18B0 2, ..)` |
| Helmaroc (0xF0) | 2 | `sea 4 1` -> `M2tower 30 0 3` | hammer fight / hkforce `req` | hammer, crest hit | per HK rows; t02 holds on any table-2 stage |

After each row: `req` on both (seq == ack once settled), `actors | grep "proc 0x00<id>"`, the probe for that boss, `sweep.sh`.
Exit through the Warpf; note where it sends each player. Never save in game; check card md5s after.

## Record
Write `LIVE-<n>.md` here per session: date, PIDs, build hashes (BlobHash, REL id from `dump`), rows run, exact log lines,
probe/peek before-after, pokes used, pass/fail per row, sweep result. Then tick TESTING.md with the record's path.
