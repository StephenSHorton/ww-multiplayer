# wind#20 block R6: co-op M2-M6 rerun, in part, macOS (2026-11-01)

**Build.** main c1278b3: code hash 04765DD0, REL 0x1CE30685.

**Rig.**
- Fresh Mac pair `wind20` from the Mac start save (`start-mac-1CE30685.sav`, Hyrule, file 1 "Abahbob" on both cards): A = 20006, B = 20115, 30 fps, healer on. Irori lease `ww/dolphin-slot-4`.
- Each card is the rig's own copy (`~/ww-shared/rigs/wind20/u1`, `u2`). `USER_DIR_1/2` were exported, so the backups named those cards, never a real Dolphin profile.

**Why not `mp-local --coop`.** `wwdev mp-local` hardcodes port 25565 (`cmd/wwdev/mp_local.go:358`). Another agent's mp-local held it, so mine died with `listen tcp :25565: bind: address already in use` after founding the world. §8.2 also allows the CLI pair, which is what ran:
- `host HostA --port 25642 --coop=1490569c486b64cf --coop-iso ~/ww-shared/disc/WW-mp.iso --yes` on A;
- `join 127.0.0.1:25642 JoinB --coop --coop-iso ... --yes` on B;
- each with its stdin fed from a file for the console (`/status`, `/refresh`).
Reported to flint (the rig card), with the fix idea: an `MP_LOCAL_PORT` env.

**Stage tables.** The first founding line built the tables from another agent's disc, because the "disc last patched" record is shared on this HOME. From then on every run passed `--coop-iso`: `stage tables 07fce914c47490e7, .../WW-mp.iso ...: 360 latching memory switch(es) in 13 save table(s) sync (76 of them, shared with Tingle Tuner switches ...)`.

**World.** `1490569c486b64cf`, founded from A's file ("Abahbob", clear count 0, 305 shared bytes). Both files bound; `coop-tag show` on both: `BOUND: bound to world 1490569c486b64cf`.

| Item | Result | Evidence (`evidence/coop-*.log`) |
|---|---|---|
| **M2.1-2 found and bind** | **PASS** (bind only) | **Backups.** The bind backed up both rig cards (`slot A GCI folder .../rigs/wind20/u1/GC/USA/Card A (default, in use)`, and the same for u2).<br>**World and binds.** The world was founded. Both files bound after the yes (host nonce b839f3c9155d3728, joiner nonce 145800c6849b98c2), both "a subset of the world" / preflight `same`.<br>**Not run.** The in-game save and reload of the file, so the card keeps the tag and V5's "saved and loaded" line shows. |
| **M2.3-4 dry run writes nothing** | **PASS** | `--coop-dry-run` on both. `/status` on the host: `armed; world 1490569c486b64cf at seq 1; a dry run (--coop-dry-run); 0 bytes written, 0 escapes, 0 verify failures`, and combat/puzzles `none in a dry run`. `coop-world list`: `seq 1, in use: pid ...`. A's keyed Bokoblin kill in the dry run gave no plan line on either side (the kill union is M6, not planned in a dry run). No `would set` line was produced: no chest or pickup progress was made. |
| **M2.5 recovery drills, M2.6 non-co-op run** | **NOT RUN** | |
| **R6's own item: a quiet-death kill with live switches and enemies on (defaults)** | **PASS** | **Grants.** `--coop` (writes) on both, no other co-op flag. Both joined with `image\|puzzle\|combat\|live-switches\|switches\|story\|inventory\|keys\|life\|magic`.<br>**The kill.** In `M_NewD2 0 0`, B hit its copy of keyed Bokoblin setID 1. ACK `applied_at/none ... health -1`; the host deleted it; 0 Bokoblins on both.<br>**The switch is set.** Zone byte 0x803C53BF reads 0x02 (setID 1) on both games, later 0x03 together with setID 0.<br>**Nothing doubles.** B logged exactly one `co-op: set the partner's dead setID 1 in room 0 (zone slot 0) (0x803C53BF \|= 0x02)`. The host (whose own game made the kill) logged no co-op set line.<br>**The room reloads clean.** After a second kill, `/refresh` on B (`co-op: refresh: reloading M_NewD2 by point 0 into room 0`). The reload briefly re-created B's setID 1 (`2 enemies ... of ours`); co-op re-wrote the partner's kill in one write (zone 0x02); setID 1 stayed dead, and only setID 0 was left and adopted (`adopted 0010`). B's frame: `evidence/r6-refresh-B.png`. |
| **Stage reload (observation)** | as vanilla | Hopping out (`M_NewD2 6 3`) and back (`0 0`) is a full stage load. It cleared the zone byte on both games (0x00) and both Bokoblins came back on both, matching each other (goldens equal, `adopted 0011`). Zone kills don't outlive a stage load in vanilla either, so no claim. |
| **Live switches on (default): a partner's switch in your room** | **PASS** | Both in `M_NewD2` room 0, B idle. A's memory switch t3 0x36 (keyshut, synced) set by **poke** on A: `poke-u32 803C5388` with bit 22 added. B logged `set t03 DRC switch 0x36 (0x803C4FFD, and its live copy \|= 0x40)`; the bit was on B's live memBit 2 s after the poke, with no room change. (0x3C was tried first but was already set on the save.) |
| **R10: `--coop-live-switches=off` on the host** | **PASS** | **Setup.** Host restarted with the flag. Neither side's grants include `live-switches`.<br>**Hold.** A's switch t3 0x37 (bonbori, synced) set by **poke** (bit 23). B logged `t03 DRC switch 0x37 waits for the next room or stage change`, and its live memBit stayed without the bit for 15 s while idle in the room.<br>**Land.** `/refresh` on B (a room change): `set t03 DRC switch 0x37 (0x803C4FFD, and its live copy \|= 0x80)`, 8 s after the refresh. |
| **R10: `--coop-boss-kill=gohma,kalle,molgera,jalhalla,helmaroc` leaves Gohdan alone** | **NOT RUN** | The host started with the list and ran, but neither its startup lines nor `/status` name the boss rows, so only a Gohdan fight in SirenB can show it. No pre-Gohdan save for this Mac. wind#22 (maple) runs TotG with the gohdan row on. |
| **M3 A1-A6 + R1-R4, M4 P1-P9, M5 Z1-Z6, M6.0 K1-K10** | **NOT RUN** | Only R1 (`/refresh`) ran, as part of the reload above. |

**Observations (no bug claimed).**
- **AnmUnresolved went 0 → 19** on B in the second before and after `/refresh`'s reload, then stayed at 19. It counts copies whose host sent `EnemyBckNone` (`wwapi/mailbox/enemy.go:197`): plausibly the host's Bokoblin animation entry while B's copy re-created. Every other canary stayed 0, and RevalidateFailures 0. Not looked into.
- **"small keys: not derived: no key table (coop-tables)"** and **"max life: not derived: no heart table"** at every co-op start, even with `--coop-iso`. The tables line lists switches only; whether the key and heart tables need `coop-tables` run once on the Mac (with its cache) was not checked. M4's keys and hearts need them.

**Sweep.**
- Go logs: no `ERROR|panic|fatal|bug:` except the mp-local bind failure above.
- Both Dolphin logs: 0 `ISI|DSI|Invalid read|Invalid write|xception|OSReboot`.
- `crash-context` on both: `No crash context`.

**Verdict.**
- R6's own sentence passes on the CLI path: the quiet-death kill under defaults, the switch set, nothing doubled, and the room reloaded clean (`/refresh`).
- The M2-M6 rerun it names is not finished: M2 in part, M3-M6 not run. R6 stays unticked.
- R10 gains `--coop-live-switches=off`. Its boss-kill item is still owed.
