# wind#60: overworld D2 long-play rows on the Mac rig (2026-11-01)

Status: done. The D2(4) race passes; the other rows are deferred to real play-testing (Stephen, wind#60 b1, 2026-11-01). Left from wind#44 (`docs/live/wind44-gt-ow-2026-11-01.md` on
`mac/wind44-gt-ow`). Rows from the overworld slice (handoff `design/coop-slice-overworld.md`, Stream D, D2), on main
eb7948f (REL 0xA9DE0F1E; A1, A2 and A3 merged in #177). Fixtures: wind44's rig folder (`ww-shared/rigs/wind44/fixtures`),
recaptured as Mac start states on this REL before use. Event bytes live at 0x803C522C + byte.

Blind input can't sail, aim at a moving target or win a fight on this rig (wind#44 needed stand-in pokes for every boss win).
So each row below drives what input can drive, and stands in for the rest with a poke of the bits the real event sets, the
way wind#44's GT rows did. Every stand-in is named as one in the result.

## Plan per row

| row | fixture (A / B) | what A does | what B should show | how |
|---|---|---|---|---|
| D2(4) race | `ow-d2-4-AB-9-Hyrule1` both | pulls the Master Sword (input, as wind#44) | B idles 2 s or more at the pedestal on kenroom layer 0/1 after A's batch, then pulls: with A2 B plays its own chain (sw13 per player) | input; `--coop-live-switches` |
| D2(5) tower rising | `ow-d2-5-AB-7-AfterFW-nayru` both | the last pearl's `towerd/f/n` sets 0x2E80 (byte 0x2E: 0x803C525A) | B's next `sea` r26 load: the tower risen, no rising event | stand-in poke of 0x2E80 on A (placing pearls needs sailing), then A's stage change; B `stage-hop sea 0 26`, screenshot + actor diff against a control load |
| D2(7) charts | `ow-d2-7-AB-27-AfterSavage` both | Tingle deciphers a Triforce chart; A salvages its shard | B owns the deciphered chart after its next load; B gets the shard live when IDLE; B's salvage spot is gone | stand-in pokes of A's chart bits (decipher) and `mTriforce` + the ocean salvage bit (salvage); read B's chart/ocean bytes and `mTriforce` |
| D2(8) Cyclos | `ow-d2-8-AB-9-Hyrule1` both | Cyclos's `TORNADE_OFF` sets 0x2710; the song is `mTact` | B's cyclones gone (`Hr2` `rt_search` returns at once on 0x2710, `d_a_npc_hr.cpp:1514-1516`) and B has the Ballad of Gales | stand-in pokes of 0x2710 and the Ballad of Gales bit on A; B loads a Cyclos sea room (r9/r18/r38) and its `Hr2` is checked; B's `mTact` read |
| D2(1) Outset strand | crafted pre-departure pair (no fixture is that early) | A has 0x0520 (post-departure) | B on Outset before departure (0x0E20, no MET_KORL): B's Outset still layer 2 with Tetra and the ship; 0x0520 held (A1) | craft from 1-Spoil's Bag by clearing MET_KORL 0x0F80, 0x2401, 0x0520 (and the 0x0801/0x0808 pair) on B, a GCI checked offline; else person-played |
| D3 | none | Outset to the ending, two players | | Stephen's call (wind#60 b1); recommended person-played |

## Results

### D2(4) the race: PASS (A2 holds sw13 per player)

Pair: both `ow-d2-4-AB-9-Hyrule1` from wind44's `state-ow-d2-4-AB-9-Hyrule1.sav` (REL 0xA9DE0F1E), rig `wind60`
(slot 2), `mac-rig.sh mp-local wind60 --coop=new --coop-live-switches`. World 734236e7 founded from A, both bound, same progress.
t9 switch word read at 0x803C50D0 (sw11 0x0800, sw12 0x1000, sw13 0x2000, sw14 0x4000); event 0x2D at 0x803C5259, 0x38..0x3B at 0x803C5264.

1. B first: `stage-hop kenroom 0 0`, greyscale, the pedestal tag (proc 0x019C at -124.7, -3198.5, -7823.4) present: layer 0/1.
   B warped in front of the pedestal; its own `TagHt2` hint played (pressed through). A's game: `set t09 Hyrule switch 0x0E`
   (sw14, live), so A's pedestal was armed without its hint.
2. A: `stage-hop kenroom 0 0`, warped to the same spot, faced the pedestal, one step of stick: `master_sword` started. A pressed
   through `master_sword`, `rebirth_hyral` and `swing_sword`: A's 0x2D04 set (0x2D 0x10 -> 0x14), then 0x3802 and 0x3A04
   (0x803C5264 0x40280000 -> 0x62280400), and A's t9 word ended 0x7804 (sw11..14). `wind60/d24-A-pull.png`.
3. B idled at the pedestal on layer 0/1 the whole time, well over 2 s after A's batch (`wind60/d24-B-waits.png`: both Links at
   the pedestal, the sword still in it, greyscale). In that time B got only the Master Sword by inventory (`set mCollect[0..3]
   bit 1`, live). **B's t9 word stayed 0x0004: A's sw13 (and sw11/sw12) was never written to B**, live switches on.
4. B pulled: B's own `master_sword` played and set B's 0x2D04 itself (no co-op write of it). At that stage change B's log:
   `writing 2 story bits`: `set event 0x3802`, `set event 0x3A04`, then `wrote equipment 0x803C4C16 38 -> 39: sword: the Hero's
   Sword -> the Master Sword`. B went on through its own `rebirth_hyral` (B's sw12 set by B) and `swing_sword` (B's sw13 set by B,
   t9 0x5804 -> 0x7804), and ended idle in the coloured kenroom with the Master Sword on B and A's mini-Link beside it
   (`wind60/d24-B-after.png`).

So with A2 (on main since #177) the race the slice feared (B skipping `swing_sword` because A's sw13 landed first) does not
happen: sw13 stays each player's, and B plays its whole chain. Logs: rig `wind60`, `mpl-d24.log`. Both Dolphins stopped
cleanly; no ISI/DSI, invalid access or JUTException in either dolphin.log; no ERROR or panic in mp-local.

### Deferred to real play-testing

Stephen (wind#60 b1): the scope of a scripted overworld playthrough is too much; these rows get picked up when the game is
actually played together. Not run on the rig:
- D2(1) the Outset strand (needs a pre-departure pair no fixture has; A1 is on main);
- D2(5) the tower rising, D2(7) charts and salvage, D2(8) Cyclos (sailing, aiming, a fight: only stand-in pokes on the rig);
- D3 the two-person overworld playthrough.
The plan above keeps the recipes for anyone who wants a rig version later.
