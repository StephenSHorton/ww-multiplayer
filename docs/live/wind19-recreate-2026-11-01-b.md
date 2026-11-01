# wind#19 live record 2: the rest of the one-Dolphin rows (2026-11-01, Mac rig)

Follows docs/live/wind19-recreate-2026-11-01.md (record 1). Same rig and method: `mac-rig.sh one
wind19`, lease `ww/dolphin-slot-6`, Dolphin 2603-342, `WW-mp.iso`, `start-mac-1CE30685.sav`;
`wwdev dump`: code hash 0x04765DD0, REL 0x1CE30685. `wwdev rf-ask` from 5764ca6 (= main's since
#224). Placements found offline with `ww-multiplayer coop-tables WW-mp.iso --section switches
--json` (the switch users list: stage, room, params, switch per actor). Evidence (rf-ask outputs,
screenshots) is in the rig's `rec/` folder on the rig Mac, not committed.

After the session: `crash-context` caught nothing since boot; no ISI/DSI or invalid access in
`u1/Logs/dolphin.log`. Every switch poked was put back; the cutscene's own progress bits in GanonK
(0x08004000 of its memory word) stayed as the game set them. Nothing was saved to the card.

| Check | Result | Evidence |
|---|---|---|
| RC-8 wall (`Siren` r3, three WALL sw46-48) | **PASS** (no mid-break case) | All three: `NOT_SET`, state +0x5AC = 0 (idle). sw46 set (pid 410, Link ~1100 away): `APPLIED`; gone the next frame; after a reload the other two come back (new pids) and 410's wall does not. sw46 put back. |
| RC-8 ice (`sea` r29, Obj_Ice sw2 pid 829) | **PASS** (no melting case) | `NOT_SET`, state +0x458 = 0 (not melting). sw2 set, Link beside it: `GATE` 8; Link ~4400 away: `APPLIED`; gone the next frame. Its collision is gone: Link warped onto its spot dropped through the cave mouth it seals into `SubD42`. Back on the sea with sw2 still set: no Obj_Ice after the reload. sw2 cleared again: the block is back after a reload. "Stand Link on it": this block is rounded and Link slides off it (screenshot `rc8-ice-on.png`), so it holds nobody. |
| RC-8 mjdoor (`sea` r1, Obj_MjDoor sw87 pid 1620) | **PASS** (no mid-break case) | `NOT_SET`, state +0x290 = 0 (WAIT); sw87 set: `APPLIED`; gone the next frame and after a reload; sw87 put back. |
| RC-8 summary | all eight gone rows passed | With record 1 (floor, mknjd, mkie, mkiek, vfan): every row answers `NOT_SET` with its bit clear and `APPLIED` with it set, the actor is gone at once and stays gone after a reload, and every state field read the row's idle value first. Open: the mid-break / mid-event `GATE` 9 cases. |
| RC-4 warpt type 0 (`GanonK` r0, Obj_Warpt params 0x00003020, lid sw8) | **PASS** (the warp itself not run) | The other GanonK jar (params 0x2031) is type 1: no candidate, as designed. Lid on: state +0x290 = 1 (CLOSE), lid BG +0x340 set, `NOT_SET`. sw8 set, Link ~6600 away: `RECREATED`, new pid 2209: state 0 (OPEN), lid BG 0, `IN_STATE`; one jar at those params. A room reload with sw8 set also builds it open (`IN_STATE`). Link 111 XZ from it: `GATE` 8. Not run: jumping in to check the warp. |
| RC-9 event (the GanonK entry cutscene, dEvtMode 2, Link in it) | **GATE 7, not 10/11** | 70+ asks for the jar across the whole cutscene (driven by A presses through the pad override): every one answered `GATE` 7 (Link demo) promptly, then `GATE` 1 (stage change) as the cutscene ended. The REL runs during this event; it checks Link's demo (7) before event mode (10) and event order (11), so an event Link takes part in answers 7. Record 1's room-entry case (no ack at all during dEvtMode 2) is the other behaviour; together: the request is never acted on during an event, but which answer comes depends on the event. Also: right after a `warp` Link is in a short demo, so the first ask answers `GATE` 7. |

## For the box text

- RC-9's "GATE 10 or 11 until the event is over" should read "GATE 7 (Link in the event), 10 or
  11, or no ack until the event is over; never acted on during it". Gate 10/11 are reachable only
  in an event Link is not part of, which a single Dolphin does not drive by input here.
