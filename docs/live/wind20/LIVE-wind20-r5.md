# wind#20 block R5: T6 regression on the default kinds, macOS (2026-11-01)

**Build.** main c1278b3: code hash 04765DD0, REL 0x1CE30685 (`dump` on both games: "this build's"). Tools are the entitled `~/ww-shared/bin/wwdev` and `ww-multiplayer`.

**Rig.**
- Mac rig `wind20` (`mac-rig.sh pair`): Dolphin 2603-342, MEM1 override.
- Start state: the Mac start save `start-mac-1CE30685.sav` (Hyrule, file 1).
- A = 75194 (host), B = 75204 (client); both at 30 fps (`mac-rig.sh fps`). Irori lease `ww/dolphin-slot-4`.
- CLI loopback on one Mac: `host HostA --port 25640` on A, `join 127.0.0.1:25640 JoinB` on B. No `WW_*` var except `WW_DOLPHIN_PID`, plus `WW_ENEMY_ANNEX` for the override item.
- Healer on both games for the whole block. `ISO_PATH` set for every `stage-hop`.

**Settings, both sides:** `enemy host: host-authoritative enemies on (kinds mo2,bk+u,cc+u,ki+u), remote Link bodies step6`; `enemy client: mirroring the host's enemies (kinds mo2,bk+u,cc+u,ki+u), remote bodies step6`.

This block runs what LIVE-6 block 2 left: the KI and MO2 kills and the `bk:tg_hit_up` override. It also reruns the hops, the canaries, the gate check and the sweep on the Mac. The CC and BK kills passed in LIVE-6 b2 (PC, same build). BK hits are applied again here under the override.

| Item | Result | Evidence |
|---|---|---|
| **KI kill (fly type)** | **PASS** | **Room.** `Cave09 0 1`, ten unkeyed fly-type Keese. The probe goldens are equal on A and B (10 idents, identical pkeys). B adopted four (`adopted 1111`).<br>**Hit.** B's Link was warped beside copy pid 366 and swung (`rig/swing.py`). One client hit gave one ACK: `ACK setID 0xFFFF handle 8 hit 1 applied_at/none ... health -3`.<br>**Result.** Host Keese 10 → 9. On B the copy (pid 366) is gone, nine Keese remain, and the slots refilled with other copies. No item or rupee line. Canaries 0. |
| **MO2 kill (keyed)** | **PASS** | **Room.** `M_Dai 1 1`, three keyed Moblins (setID 0, 1, 2; hp 10/10). Goldens equal; B adopted all three (`adopted 0111`).<br>**Hits on setID 0.** health 8, 6, 4, 2; then two `parried/none` (the Moblin guarded); then `applied_at` health 0.<br>**Result.** The Moblin is deleted on the host and on B; sets 1 and 2 remain on both (`evidence/r5-acks.txt`). B's frame after the kill: `evidence/r5-mdai-75204.png`. |
| **`bk:tg_hit_up` override applying** | **PASS** | **Setup.** Host restarted with `WW_ENEMY_ANNEX=bk:tg_hit_up=60` (env checked in the process). The client was left at its defaults. Both games in `M_NewD2 0 0` (two Bokoblins, `adopted 0011`).<br>**Override in memory.** `enemy-block-dump` on A: the annex at 0x804130C8 has `override[1] TgHitUp:60` (BK's row); every other override is 0.<br>**Hits.** B's swings were applied on both Bokoblins: setID 0 health 3 → 1, setID 1 health 3 → 1.<br>**Result.** Host setID 0 was deleted (`actor changed since the walk ... walking again`, then `1 enemies`), and B's copy went with it.<br>**Observation (no bug claimed).** The last ACK for setID 0 reads `health 1` while the host deleted it. Read straight off the log, not looked into. |
| **8 hops with canaries** | **PASS** | **Hops:** `M_NewD2 0 0`, `6 3`, `7 4`, `0 7`, `14 8`, `15 10`, `3 12`, `M_Dai 1 1`. `M_NewD2 0 16` was skipped (LIVE-6's void loop).<br>**Goldens.** After each hop: equal on both games (2, 1, 5, 4, 0, 0, 0 and 3 idents). Both games were in the same room every time.<br>**Canaries.** PauseReasserts, AnmUnresolved and RevalidateFailures stayed 0 throughout, and so did canaries2 {AnmResNull, AnmRange, HitNoCross, HitAborted}. WatchdogReleases rose +1 per load (2 → 9), as on main.<br>**Heap error counters after the block:** `solid 10`, every other counter 0, on both games (solid +1 per load, as vanilla). File: `evidence/r5-hops.txt`. |
| **No adoption while gated** | **PASS** | None of the join logs' 431 periodic lines (158 + 273) shows an adoption while a gate is open (`not mirroring` / `the mirror waits` / `holding the copies`, until the next `mirroring: the host lists`). There were 33 gate lines in all. |
| **Full-log sweep** | **clean** | **Go logs.** All four host/join stderr files are empty. 0 `panic\|fatal\|exception\|bug:\|error` lines.<br>**WARNs.** Only `no loaded <kind> entry in mObjectInfo` (8, at stage loads) and `the REL module's actor is not running ...; the mirror waits` (5, during stage changes).<br>**Dolphin logs.** Both have only the vanilla `見積もりヒープサイズ(...)で登録失敗` heap report; 0 `ISI\|DSI\|Invalid read\|Invalid write\|xception\|panic\|OSReboot`.<br>**Crash context.** `crash-context` on both: `No crash context`. |

**Verdict.**
- R5 (T6 on the default kinds) now has every part.
  - CC and BK kills: LIVE-6 b2, PC.
  - KI kill, MO2 kill and the `bk:tg_hit_up` override: here.
  - Probe goldens, 8 hops with flat canaries, no adoption while gated, and the sweep: both rounds.
- T6's every-kind half belongs to wind#18 (tulip), which cites this record.

**Logs.** `evidence/r5-host.log`, `r5-join.log` (defaults), `r5o-host.log`, `r5o-join.log` (override). The periodic `seq` lines are stripped and the LAN address masked.
