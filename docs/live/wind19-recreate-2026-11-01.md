# wind#19 live record 1: re-create rows on one Dolphin (2026-11-01, Mac rig)

- Rig: `mac-rig.sh one wind19` (docs/mac-rig.md), lease `ww/dolphin-slot-4`, Dolphin 2603-342 on
  macOS, the shared `~/ww-shared/disc/WW-mp.iso`, start save `start-mac-1CE30685.sav`.
- Build: `wwdev dump`: code hash 0x04765DD0 (matches), REL `WWRL` build 0x1CE30685. wwdev from
  `mac/wind19-rfask-force` (5764ca6: #220's `rf-ask` plus #224's `--force`, switch line and walk note).
- Evidence: the rf-ask outputs and screenshots are in `~/ww-shared/rigs/wind19/rec/` on the rig Mac (not committed: game frames).
- Method: `wwdev rf-ask <pid>` (one request, body then seq, the row's state field before and after;
  docs/live/wind19-recreate-runbook.md). Bits set and cleared with `peek`/`poke-u32` on the word
  holding the byte rf-ask names. Each bit was put back to its starting value, except where noted;
  nothing was saved to the card. Room reloads were done with `stage-hop <stage> <point> 0 --yes`.
- After the session: `crash-context`: nothing caught since boot; `u1/Logs/dolphin.log` has no
  ISI/DSI or invalid access.

| Check | Result | Evidence |
|---|---|---|
| RC-7 vmc (`kaze` r0, VmcBS sw1 pid 404/489, sw2 pid 490/576/746) | **PASS in part** | The fixture card has sw1 set and the tree grown: `IN_STATE` (9), state +0x4B8 = 3 (done). sw1 cleared (0x803C5387 0xDF -> 0xDD) and the room reloaded: a bare mound, state 0 (idle), `NOT_SET` (5). sw1 set, Link ~2500 away: `RECREATED` (8), new pid 518; the old pid left the queue the next frame (rf-ask: no actor of pid 489, so `NO_ACTOR` could not be asked); new pid 518 `IN_STATE`, state 3; `actors`: one Obj_Vmc at (-4050, 754.5, 8900). sw2, Link 150 from the mound: `GATE` 8 (Link near), state 0. After a void-out reload with sw2 set: the rebuilt mound `IN_STATE`, state 3 (both mounds). sw2 again from the entrance: `RECREATED` new pid 777; screenshots `rc7-mound-before.png` (bare soil) and `rc7-tree-after2.png` (the grown tree standing where the mound was, no reload). New pid 777 with Link near: `GATE` 8, state 3. Not run: the hookshot latch (r16, needs the hookshot by real input) and mid-grow `GATE` 9 (needs Makar planting). |
| RC-8 floor (`kaze` r1 Hhyu1 sw131 pid 852; r5 sw132 pid 1394/1638) | **PASS in part** | r1, Link ~1100 away: bit clear `NOT_SET`, state +0x2D8 = 0 (idle); dan sw131 set (0x803C53AB 0x00 -> 0x08): `APPLIED` (1); gone from `actors` the next frame and after a room reload (bit still set). Collision: Link warped to (-3800, 1800, 4165) above where it stood fell through to y 991.8. r5 "far edge": Link at the floor's origin, standing on it (y 958.5): `GATE` 8. Warped +320 along x from its origin Link was not on it (fell to y 201), so this floor reaches less than 320 units along its axes, and wherever he can stand on it he is within the generic 300 (`GATE` 8). The corners (~350) were not measured: the warps there did not take. A Floormaster grabbed Link once in r5 (no warp was made during the drag). dan sw131/132 were left set (dan bits, not saved). |
| RC-8 mknjd (`kaze` r5 Obj_MknjD sw30 pid 2180) | **PASS** (no mid-event case) | Bit clear `NOT_SET`, state +0x43F = 0 (idle); sw30 set: `APPLIED`; gone the next frame and after a reload; sw30 put back. |
| RC-8 mkie (`M_Dai` r7, four Obj_Mkie, sw4/5/6/48) | **PASS** (no mid-event case) | All four: bit clear `NOT_SET`, state +0xF50 = 0 (idle). sw48 set (pid 3186, Link ~2000 away): `APPLIED`; gone the next frame; after a reload the other three come back (new pids) and 3186's statue does not. sw48 put back. |
| RC-8 mkiek (`M_Dai` r6, four Obj_Mkiek, sw36-39) | **PASS** (no mid-event case) | All four: `NOT_SET`, state +0x45C = 0 (idle). sw36 set (pid 3421): `APPLIED`; gone the next frame and not rebuilt on reload (the other three are). sw36 put back. |
| RC-8 vfan (`GanonM` r0, Obj_Vfan sw24 pid 3533) | **PASS** (screenshot inconclusive) | `NOT_SET`, state +0x440 = 0 (idle); sw24 set, Link ~2850 away: `APPLIED`; gone the next frame and after a reload. The before/after screenshots do not show the door clearly (far, dark, behind Link). sw24 put back. |
| RC-8 wall, ice, mjdoor | **NOT RUN** | No placement found in kaze r0-r5 or M_Dai's rooms by the `actors` scan (`rec/scan-mdai.txt`); find one with `coop-tables`' switch users list. |
| RC-8 state fields | **PASS** for every row run | Each `before` line read the row's idle value (vmc 0, floor 0, mknjd 0, mkie 0, mkiek 0, vfan 0) and vmc's done value 3 where the tree stood, so offset, width and sets agree with refresh.c's table here (and `TestRefreshRowsMatchC` holds the Go copy to it). |
| RC-9 event order (vfan, the room-entry event after a hop) | **DIFFERS** | Asked while dEvtMode was 2 (sw24 set): no ack within 2 s (rf-ask: not acked). Read again once the event was over: ack 30, result `APPLIED`, gate 0, and the door was gone. The REL did not answer `GATE` 10 or 11 during the event; it consumed the request after it. Nothing was deleted mid-event, which is what the gate is for. The relay copes (an unacked request is asked again after its ack wait). Gate 11 (a queued event order, a chest or a talk) not run: it needs real input. |
| RC-4 leftovers | not run | kaze r0's warp jar (pid 2333, params 0x02010032) is type 2 (three-way): no candidate, as designed. M_Dai r2 and r6 have jars (pids 2569, 3085), not asked. |

## Notes for the next run

- `stage-hop <stage> <point> <room>`: the point chooses the room (kaze 1..5 -> rooms 1..5).
- A `ww-multiplayer warp` straight after a hop is sometimes dropped; wait a few seconds and check
  `rf-probe`'s Link line before trusting a position.
- Memory switches are per stage (0x803C5384.. holds the current stage's copy), so restore a bit
  before hopping to another stage.
