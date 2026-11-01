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
