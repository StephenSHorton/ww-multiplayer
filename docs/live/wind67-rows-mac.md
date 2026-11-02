# wind#67: GT D2(3), D2(6) C3, overworld D2(3), Q5-2, Kreeb and the Goron trade on the Mac rig (2026-11-01)

Rig `wind67` (moss), lease `ww/dolphin-slot-4`, main cc7c512 (REL 0xCAC6980B; no `inject/` change since #289), Dolphin 2603-342,
the shared patched disc, wwdev and ww-multiplayer built from cc7c512 and entitled. `mp-local --coop=new --coop-live-switches`, both 30 fps.

## GT pair: both CryZe `28-Hyrule3`

Both cards `28-Hyrule3`, both from `state-28-Hyrule3-CAC6980B.sav` (wind#61's recapture). World ad47547f founded from A, both bound
(B a subset). A = Aryll (u1), B = Tetra (u2). A's coop-probe gates, bow slot and X/Y/Z logged every ~1.5 s (`A-poll.log`).

### GT D2(3): a refight with a snapshot (pass)

A has its own DRC snapshot (0x3D80 set); A's item block 0x803C4C08..+0xC0 read before, during and after.
1. 21:24:30 A `stage-hop Xboss0 0 0`. The swap ran: bow slot 0x35 -> 0xFF, X/Y/Z 02 01 03 -> FF FF FF, the bag, capacities and
   collection replaced (17 words differ). A's probe read `IDLE closed: a boss refight is in progress` (refight 1) on every line
   from 21:24:33 to the warp.
2. 21:24:48 B gains the Light Arrow (stand-in for the chest: B's mGetItem[12] |= 0x04, bow slot 0x36). The world took it
   (journal seq 3 `gains 05d:04` from co-op 2). Nothing was written to A for the rest of the refight: A's mGetItem[12] stayed 0x03,
   bow slot 0xFF, and A's log has no line for it.
3. 21:25:46 A `boss-end gohma` (the game's own end: 0x3240, tmp 0x0480, warp to `GanonA` 5/1). At 21:25:48 (still Xboss0, the
   restore) A's items came back: every word of the block equals the before read except life (A took hits) and X/Y/Z, which stay
   FF FF FF (vanilla restore, `d_com_inf_game.cpp:1674-1676`, as wind#44 found).
4. 21:25:51, A in GanonA and idle: `set mGetItem bit 2 (0x803C4C65 |= 0x04)`, then `wrote item slot 0x803C4C50 35 -> 36: bow slot:
   fire and ice arrows -> light arrows`. So the held write landed only after the restore, on A's own items.

### GT D2(6) C3: B walks past its own third Phantom Ganon (answered: yes)

1. A `stage-hop GanonM 0 0`, two stick pushes, `boss-end pg3`: last_end, A's t8 sw0 (0x803C50AC 0x01000000 -> 0x01000001 after the
   door). `boss-end pg3 --step door`: 0x3A08 and sw24. B (in Hyrule): `set t08 GT switch 0x18` live.
2. A `stage-hop GanonA 0 1`; B `stage-hop GanonM 0 0`: `set event 0x3240`, `0x3904`, `0x3A08` at B's stage change; B's sw0 stays
   clear (0x803C50AC = 0x01000000), and B's own third-fight Phantom Ganon is there (`fganon` 0xFF006402, y 10000, hp 100/100).
3. C3: B is run by stick straight from the spawn (0, 0, 1301) toward the GanonL door. Phantom Ganon stays parked at y 10000 until
   B is at z -1410 (past its home z -1000), then materializes at (0, 30, -574) with its four copies. B keeps running, reaches the
   door at z -1706 and loads `GanonL` (0, 1773, -18800) less than a second later, at full life. No door locked. Run twice
   (21:28:13 and 21:28:50), the same both times (`B-walk.log`). Screenshot: `wind67/c3-B-ganonl.png` (B in GanonL).
   So before A1 the partner can walk past its own third fight to the GanonL door; the slice's guess (B will almost certainly
   fight, item 6) does not hold at a straight run.

Crash sweep: no ISI/DSI or invalid access in either dolphin.log; no panic in mp-local. Pair stopped by PID, mp-local by its stop file.
