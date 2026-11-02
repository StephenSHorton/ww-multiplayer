# wind#67: GT D2(3), D2(6) C3, overworld D2(3), Q5-2, Kreeb and the Goron trade on the Mac rig (2026-11-01, moss)

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

## Overworld D2(3): FIND_SISTER by A with B on Outset (pass)

Pair: A `ow-d2-3-A-1-SpoilsBag` (CryZe 1-Spoil's Bag, MajyuE 18; recaptured on CAC6980B), B a new game made by input on an empty
card (create the save file, Quest Log 1, name "AA", the legend, `awake`). `mp-local --coop=new --coop-merge --coop-live-switches`:
world b42fdd40 founded from A, B `far behind, merged under --coop-merge, 4 holds, a fresh bind`, holding 0x0808, 0x2401, 0x0101 and
0x0E20 (A1). Both 30 fps.
1. B's first bound Outset load (`stage-hop sea 206 44`): `writing 12 story bits` (none of the held four), 187 actors.
2. A, by a hop to the start point the fortress's exit uses (`stage-hop Mjtower 16`, MajyuE SCLS #16; the walk up the fortress was not
   played): `FIND_SISTER` ran for real. 21:54:56 A's byte 0x25 went 0x01 -> 0x81 (0x2580, `find_sister.stb`); 21:56:38 the Director's
   NEXT loaded `sea` 200 r11 layer 8 (Windfall, MEETSHISHIOH). The world took 0x2580 at that stage change (journal seq 4 `gains 649:80`).
   A's MEETSHISHIOH did not finish by A presses (A sat in its event), so A's own MET_KORL and the release of B's holds were not reached.
3. B reloads Outset: `a stage change starts: writing 1 story bits`, `set event 0x2580`. B's bytes 0x01 = 0x08 (0x0101 clear),
   0x08 = 0x02 (0x0808 clear), 0x0E = 0x00 (0x0E20 clear), 0x24 = 0x00 (0x2401 clear). Outset loads 187 actors, the same as step 1,
   and Aryll is beside B on the lookout ("Speak"): `wind67/fs-B-outset.png`.
An earlier try hopped A to `Mjtower 0 0` and then to `MajyuE 18`; the second hop stayed pending on a black screen (that start point
is not one the game uses), so A was restarted from its state and mp-local resumed the same world (`--coop=b42fdd40...`).

## Q5-2: after the clear save (in part: the refusal live, the cleared file from NAMEEX's image)

Pair: both `b-28-Hyrule3-GanonK-cleared`, world 8b742e74 founded from A, both bound.
1. 22:05 A `stage-hop GTower 2 0 9`, pressed until the ending request: 22:08:19 A's STABLE `closed: the credits are playing`.
   The movie ran untouched; 22:16:55 NAMEEX (`Name`).
2. Yes on NAMEEX's save, Quest Log 1, Yes on "Save over this data?": the game went back to Choose a Quest Log, the slot still read
   2015 and the GCI on disk was unchanged (same md5, same time), as on wind#9. Dolphin did write the card for B's new game earlier
   ("Wrote save contents to GCI Folder"), so the folder flushes; NAMEEX's save did not reach it by `wwdev input`. Not solved here.
3. `coop-tag show` on A at that point (RAM: clear count 1, the world tag still in mReserve):
   `state LEAKED: the tag leaked from another file (V3: a second quest)`, `V3 FAIL tag clear count 0, file clear count 1`,
   `next co-op scrubs this tag (after a prompt)`.
4. So the cleared file was built from A's live image: the 0x778-byte save image read from 0x803C4C08 put into slot 0 of the fixture
   GCI (savefile WithSlot, Verify passes). Cold boot with it: the file select shows the Triforce mark; Start loads it (in Hyrule,
   since NAMEEX's own save step, which would also reset the resume point, never ran).
5. `mp-local --coop=8b742e74...`: `[server] co-op: Aryll (co-op 1) refused: your file has clear count 1: second-quest files can't join
   co-op`, A: `the host says (CLEARCOUNT)`, `the host refused co-op for this session (CLEARCOUNT); pose sync is unaffected`, then
   `the loaded file carries a leaked co-op tag ... asking to scrub it` and `zeroed the leaked co-op tag (0x30 bytes at 0x803C532C)`.
   B joined unbound as usual.

## Kreeb's piece (in part: the register half)

Pair: both CryZe `19-WindfallCleanup` (max life 51, 0x1B20 and 0x1C02 clear; `20-AfterWindfallCleanup` has both, so CryZe got
the piece between them). World e01a9f62.
- Kreeb (`NPC_PEOPLE` type UM1, proc 0x0177) was not in Windfall's actor list (sea r11) by day or at night (time poked to 22:00
  and 12:00, each with a reload), so his reward talk could not be played. The one NPC there at night was the dancer (proc 0x016E).
- Stand-in: A's 0x1B20 poked (21:37:52). By mistake the first poke set 0x1A20 (byte 0x1A, a Tingle talk flag,
  `d_a_npc_tc_msg_red.inc:37`); it was cleared 8 s later, but the world had already taken it (journal seq 2, `gains 63e:20`), and it
  reached B. It is not a heart record and does not touch the count.
- A: `wrote max-life counter +1 ... max life: 51 -> 52 quarter hearts: 9 of 28 piece(s), 2 of 10 reward(s)` (A's own flag now
  credits Kreeb; a real reward would have given A the piece itself).
- B's stage change: `writing 2 story bits`, `set event 0x1A20`, `set event 0x1B20`, then `wrote max-life counter +1 ... 51 -> 52`.
- Not checked: B's Kreeb after-talk (Kreeb not spawned).

## The Goron trade (in part: 0x3E04 not set in the talk that was played)

Pair: both CryZe `24-AfterPawprint` (max life 68, 0x3E04 clear; `25-AfterStonewatcher` has it). World 6652177d. A sea-room scan
for `NPC_ROTEN` (proc 0x0176) found the three merchants in sea r9 (params 0x02FF0000), r23 (0x01FF0000) and r34 (0x00070000);
merchant 1 (`getPrmNpcNo`, params >> 24) is r23's.
- Aids on A (none of them a save bit): the delivery bag's slot 0 0x96 -> 0x97 (Shop Guru Statue), X bound to inventory slot 0x30
  (that bag slot) and the HUD's X item (0x803CA7DB) set to 0x97, since the menu did not refresh it.
- A faces merchant 1 and presses X (real input): the XY talk plays a statue line ("It's said to be a statue of the most incredible
  of men ... why would you have it?", then the Mother & Child Isles merchants): `wind67/goron-statue-talk.png`.
- Through the whole talk and after it: A's byte 0x3E stayed 0x93 (0x3E04 clear), the statue stayed in the bag, A's max life stayed
  68, and B's too. Against B's untouched file only event byte 0xBB differs (A 0x01, B 0x02).
- So the bit was not set on this path. The decomp's `isGetMap` branch (merchant 1, statue, no 0x3E04) would set it and empty the
  bag; this talk was a different branch, maybe because the preview item came from somewhere else. Either way 0x3E04 stays unrecorded.

Crash sweep (every pair): no ISI/DSI or invalid access in any dolphin.log; no panic in any mp-local log.
Fixtures were rig-local copies; nothing from them is committed. Slot 4 released at the end.
