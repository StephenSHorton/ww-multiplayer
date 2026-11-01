# wind#18: enemy kinds on the Mac rig (2026-11-01)

Main 0c5927c (REL 0x1CE30685, BlobHash 0x04765DD0). Mac rig (docs/mac-rig.md): Dolphin 2603-342, MEM1 48 MB, the Mac start save, `mac-rig.sh pair` (two PIDs), `MP_LOCAL_PORT=0`, lease ww/dolphin-slot-3. A = host (enemy-A), B = client (enemy-B). Both Dolphins' logs swept after each run: no ISI, DSI or invalid access; no panic, fatal or reject line in mp-local's log.

## KI (Keese, default on): `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u` (the default)

- `M_NewD2 7 4`: the host lists 5 keyed Keese; B adopts 4 (setIDs 0, 2, 3, 4, `adopted 1111`), canaries 0 (WatchdogReleases 1 from the hop). Host `enemy-kill` on setID 0: the actor shrinks and is deleted on A, room 4's zone slot 0 records setID 0 dead; on B the copy is gone, no item actor appears, and the freed slot refills with setID 1.
- Ceiling return not reproduced: these are wait-type Keese hanging at y 3700; with A warped about 200 units under setID 1 for 6 s, none of the four woke (mode 1, unmoved). The wake needs another trigger (line of sight or a closer approach); not run.
- `M_Dai 15 14`: the host lists 6 keyed; B adopts 4 (setIDs 2, 3, 5, 6). Simultaneous 6 s `enemy-watch` on both: the copies follow the host's fliers through flying, attack (mode 3/4) and return (mode 1), within a few dozen units in steady flight and up to about 150 during fast turns. ERR_KEY 0 over the run.
- `Cave09 0 1` (`ki+u`): the host lists 10 unkeyed fliers; B fills all 4 slots with unkeyed ones (`u31D626A2`, ...), canaries flat, ERR_KEY 0.
- Not run: LUNGE's Tg/Co shrink readback, the Fire Keese pair (`M_NewD2 0 7` stays switch-hidden) and the flame btk (no verified hop), a client sword kill.

## AM2 (Armos) and AM (Armos Knight), opt-in: `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u,am+u,am2+u`

- `kaze 4 4`: the host lists 7 keyed Armos; B adopts 4 (setIDs 6, 0, 4, 3), canaries 0. Host `enemy-kill` on setID 0: deleted on A, zone slot 0 dead; B's copy gone (6 left), slot 1 refilled with setID 5, canaries flat.
- `kaze 2 2`: the host lists 2 Armos Knights; B adopts both (`am 0`, `am 1`), canaries 0.
- `Cave09 0 7` (`am2+u`): the host lists 4 unkeyed Armos; B fills all 4 slots. Host `enemy-kill` on one: 3 left on both games; B's slot 2 empty (nothing left to take it). No ERR_KEY, panic or reject over the run.
- Not run: a client back-stab (two hits, dance), the hammer one-hit, the front clink, statue handback, `kaze 1 1`, `Siren 0 8`; AM's eye/light arrow, bomb swallow, Grappling Hook and `am:hooks=special` (they need the wind#11 (d) bombs/arrows fixture and aimed client swings).

## TN (Darknut) T4, opt-in: `WW_ENEMY_MIRROR_KINDS=mo2+u,bk+u,cc+u,ki+u,tn+u`

- `Cave11 0 9`: the host lists 4 unkeyed Darknuts; B fills exactly the 4 slots with them (`u0553102A`, `uCFBE59C8`, `u3E8C8EA7`, `u01A79E15`), canaries flat, no ERR_KEY, panic or reject. B's hop was refused twice with "not now: an event is running" (Cave09's leftover event) and landed on the retry.
- Host kill not run: before it, A's Link (standing idle at the entrance) was killed by the four Darknuts and the game sat on "Would you like to save?". The pair was stopped by PID rather than answer it (no write to the fixture card). The T1-T3 reruns in the caves and `Cave10 0 16` (`mo2+u,tn+u`) are still owed; give A invincibility or a fight plan first.

Crash sweep after the Armos and Darknut runs: no ISI, DSI or invalid access in either dolphin.log.

## T6 every-kind half (the default-kinds half is fern's R5: docs/live/wind20/LIVE-wind20-r5.md on mac/wind20-release-list 1d21017)

`WW_ENEMY_MIRROR_KINDS=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u,bb+u,mt+u`, `WW_ENEMY_ANNEX=bk:tg_hit_up=60`, main 6c602d9 (rebased), pair 19228/19303. The first hop loop's results were discarded (a loop bug left both games in Hyrule, so every line repeated); M_Dai 1 1 was then hopped by hand (3 Moblins, `adopted 0111`) and the 8 hops below re-run with the PIDs written out.

8 hops, client line and slots after each (canaries 0 throughout apart from WatchdogReleases, +1 per load as in R5):

```
M_Dai 2 2: 6 enemies of ours in M_Dai room 2 | adopted 1111 slots [0:cc 4Q][1:cc 8Q][2:cc 6Q][3:cc 5Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:2}
M_NewD2 7 4: 5 enemies of ours in M_NewD2 room 4 | adopted 1111 slots [0:ki 0Q][1:ki 4Q][2:ki 3Q][3:ki 2Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:3}
kaze 4 4: 7 enemies of ours in kaze room 4 | adopted 1111 slots [0:am2 6Q][1:am2 0Q][2:am2 4Q][3:am2 3Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:4}
kaze 2 2: 2 enemies of ours in kaze room 2 | adopted 0011 slots [0:am 0Q][1:am 1Q][2:-][3:-] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:5}
Cave11 0 9: 4 enemies of ours in Cave11 room 9 | adopted 1111 slots [0:tn u3E8C8EA7Q][1:tn u0553102AQ][2:tn u01A79E15Q][3:tn uCFBE59C8Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:5}
M_NewD2 6 3: 3 enemies of ours in M_NewD2 room 3 | adopted 0111 slots [0:bk 1Q][1:bb 0Q][2:bb 2Q][3:-] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:6}
Cave09 0 7: 4 enemies of ours in Cave09 room 7 | adopted 1111 slots [0:am2 u6D62302AQ][1:am2 u58897131Q][2:am2 u3BB8E67AQ][3:am2 uD7EDB557Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:6}
Hyrule 0 0: 8 enemies of ours in Hyrule room 0 | adopted 1111 slots [0:mo2 u00BC3295Q][1:mo2 u746D6B82Q][2:cc uF8F8E57AQ][3:cc uAE9A87A4Q] canaries {PauseReasserts:0 AnmUnresolved:0 RevalidateFailures:0 WatchdogReleases:7}
```

One host `enemy-kill` per kind, every kind on (count before -> after on A and on B):

```
cc kill pid 1144: A 5 cc in room -> 4 cc in room, B 5 cc in room -> 4 cc in room | the actor is gone (the shrink ran its course)
bk kill pid 1214: A 1 bk in room -> , B 1 bk in room ->  | the actor is gone (the shrink ran its course); room 3's zone slot 0 records setID 1 dead: true
ki kill pid 1316: A 5 ki in room -> 4 ki in room, B 5 ki in room -> 4 ki in room | the actor is gone (the shrink ran its course); room 4's zone slot 0 records setID 0 dead: true
am kill pid 1442: A 2 am in room -> 1 am in room, B 2 am in room -> 1 am in room | the actor is gone (the shrink ran its course); room 2's zone slot 0 records setID 0 dead: true
```

The MO2 kill was not run here (the script's pid lookup missed the Moblin walk format); fern's R5 has an MO2 kill on M_Dai 1 1 setID 0. The BK kill left the room with no BK on either side (the zone slot records setID 1 dead).

`bk:tg_hit_up=60` applies: the host's annex (`enemy-block-dump`) shows `override [... {TgHitUp:60} ...]` in the BK slot (override[1]).

No adoption while gated: no `adopt` line mentions a gate. Sweep: no ISI, DSI or invalid access in either dolphin.log, no panic, fatal, reject or ERR_KEY in mp-local's log.
