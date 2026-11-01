# wind#35: R6 co-op rerun, the rest (Mac rig, 2026-11-01, maple)

Rig wind35 (slot 3), main ec7b02a, Dolphin 2603-342, REL 0x1CE30685. CryZe 15-StartET (pre-Earth-Temple) save state on both.
`mp-local --coop=new` (the rig script adds --coop-iso and --yes), with mp-local's console fed through a file (`tail -f console.in | mp-local`) for /status, /warp and /refresh.
World afc5ae4a7b4c80b5; both granted image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic|chest-claim.
A = host pid 38889, B = client pid 38906, 30 fps. Kills are `enemy-kill` light-shrinks on A (debug pokes, marked POKE).

Tables check (the card's first question): with --coop-iso the stage tables load (07fce914c47490e7), and /status shows "small keys of table 3..7" and "max hearts: derived". No separate `coop-tables` run is needed. The earlier "not derived" lines were sessions without --coop-iso while the per-user "last patched disc" pointed at a deleted scratch ISO.

## K9 A room that clears on B (M_Dai r8, three ReDeads): PASS
- Both at `M_Dai 10 8`. The ReDeads sat in MODE_SW_WAIT (class mode 10, enemyice mode 0) on the coffin dan switches 0x88/0x8A/0x8B, which the slice lists as not synced. POKE: those dan bits set on both games to wake them (test setup).
- POKE: A's three killed. B: "set the partner's dead setID 0/1/2 in room 8" and "light-shrunk the partner's kill" for each of B's own ReDeads. B's room emptied, and B's own ALLdie set sw52 (memBit word 1 bit 20) on B with no co-op write for it.

## K7 Holds (pause menu only): PASS in part
- B opened its pause menu (Start). POKE: A killed Poe setID 0 in r7. B's Poe stayed (timer 0) while paused, and light-shrank right after B closed the menu.
- Finding: during the pause `/status PlayerB` said "0 held". The pause closes the whole tick's IDLE gate before any target is planned, so no per-actor hold reason is recorded. K7's "(/status names the reason)" doesn't hold for the pause case. Doc or status wording to fix (carded).
- Not run: NPC talk, the Floormaster or ReDead grab, and `peek-demo 60` through them (no grab could be driven).

## K4 Another stage, then entry: PASS
- B at sea (sea 44). POKE: A killed Poe setID 1 in M_Dai r7. Nothing was written on B while at sea.
- B hopped into r7: both partner-killed Poes (setIDs 0 and 1) were created, then light-shrank (timer 1 -> 8, then gone): §7.2's spawn-then-shrink artefact.

## K10 A storm: PASS
- B stage-hopped through r8, r7, r4, r7 and r3 while A killed the two Moblins in r7. B's /status: "0 verify failures", 69 bytes written, 0 escapes. `zone-dump` on B: dead actors 0,1,2,3 only in room 7's zone slot. No halt.

## K5 Unkeyed enemies stay each player's own: PASS
- M_Dai r5, unkeyed black ChuChus (params 0xE0F40301, not adopted by the mirror at that moment). POKE: A killed one. B's matching ChuChu stayed (hp 4/4, timer 0). Host /status: "0 scoped bit(s) refused by the host's tables" for both players.

## K3 A hidden neighbour: PASS in part (the zone write)
- A in M_Dai r4, B in r3 with r4 loaded hidden on B (zone slot 1, no r4 actors created). POKE: A killed Floormaster setID 0. B: "set the partner's dead setID 0 in room 4 (zone slot 1)", and zone-dump shows dead actor 0 in room 4's slot.
- Not judged: B walking through the door into r4 without the Floormaster being created. My stage-hop into r4 reloads the stage (zones wiped), so it showed K4's spawn-then-shrink instead (FM 0 created, then light-shrunk). It needs a walk through the r3-r4 door.

## R2 /warp, same stage: PASS
- A in M_Dai r4, B in r3. `/warp PlayerB`: "room 4, where PlayerA is, is not loaded here: reloading M_Dai by point 5 into room 4 ..., then warping to PlayerA", then "warping to PlayerA at (398, 0, 2102) in room 4". B ended on A's exact position.

## R4 Refusals (pause menu): PASS
- B's pause menu open. `/warp PlayerB`: "not now: the pause menu is open; try again in a moment". `peek 0x803C9D48 14` unchanged (0x4D5F4461 0x69000000 0x000504FF before and after).
- `/refresh PlayerB`, sent in the same tick: "not now: a warp waits for its tick". Still refused, nothing written, but with the queued /warp's reason. After the menu closed, `/refresh` ran: "reloading M_Dai by point 5 into room 4". Cutscene refusal not run.

## R3 /warp, another stage: PASS
- A hopped to sea 44 (Outset) while B stayed in M_Dai r4. `/warp PlayerB`: "going to sea by point 0 into room 44, layer -1, PlayerA's entrance ..., then onto PlayerA", then "warping to PlayerA at (-201622, 168, 312243) in room 44". B ended on A's position.

## Z4 Players in different rooms see nothing until they enter: PASS
- The only latching zone switch the M1 verdict has in M_Dai is r0's 0xCB (set by SW_C00). B waited in r8. POKE: A's r0 zone slot 0xCB set (zone 0, +0x02 u16 bit 11). Nothing reached B's zones while B was in r8.
- B entered r0: "set the partner's zone switch 0xCB in room 0 (zone slot 0)", and zone-dump on B shows 0xCB in room 0's slot only.

## Z6 A storm: NOT RUN (properly)
- The scripted hops of B between r1, r0 and r8 were refused (events after the hops), so B never left r0/r1. The four zone-dump snapshots show 0xCB only in room 0's slot and no wrong slot, and B's /status shows 0 verify failures, 5 escapes. K10's storm (kill bits, hops verified) covers the kill side.

## Not run, with reasons
- Z1 (latching crystal), Z2 (cleared room shutter), Z3 (timed crystal): need a real crystal hit or a room clear by pad. The switch sync itself is covered: K9's ALLdie (sw52) on B, wind#21's DRC torch AND sw0, and Z4 here. A timed crystal can't be represented by a poke.
- K6 (mini-boss / BOSS untouched): `enemy-kill` refuses BOSS actors, so a BOSS kill needs a real fight.
- K7 grabs (Floormaster, ReDead, FF Moblin holdup) and `peek-demo 60`: no grab could be driven. Only the pause hold ran (see K7).
- K8 Kargaroc: DRC r3 Kargarocs not visited this run. The light-shrink path is the same as `enemy-kill`'s.
- P3-P9 (heart piece, song, bow, story bits at the next stage change, max magic, save-diff, a far-behind file), A4-A6 (ocean and charts, nothing-else audit, the off switches): need item gets this late file already has (CryZe 15-StartET owns the bow, Deku Leaf and songs), real pickups by pad, or saving in game on both. Max hearts and keys derive live (wind#32 D4-8: one derive write 16 -> 20; wind#21: keys 0 -> 1 -> 0 with the door hold). The ocean grid sync is wind#31's SV1.

## Logs
- Both dolphin.logs swept: no ISI, DSI or invalid access. mp-local: no panic.
