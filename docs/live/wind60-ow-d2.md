# wind#60: overworld D2 long-play rows on the Mac rig (2026-11-01)

Status: planned, waiting for a Dolphin pair slot. Left from wind#44 (`docs/live/wind44-gt-ow-2026-11-01.md` on
`mac/wind44-gt-ow`). Rows from the overworld slice (handoff `design/coop-slice-overworld.md`, Stream D, D2), on main
eb7948f (REL 0xA9DE0F1E; A1, A2 and A3 merged in #177). Fixtures: wind44's rig folder (`~/ww-shared/rigs/wind44/fixtures`),
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

(none yet)
