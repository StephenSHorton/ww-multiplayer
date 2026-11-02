# wind#65: enemy mirror rows still open (Mac rig, 2026-11-02, grove)

**Build.** main 93e74b6's tools (built from cc7c512; no inject/ change since #289), REL 0xCAC6980B, the shared `WW-mp.iso` and
`start-mac-CAC6980B.sav`. Tools entitled in the rig's own bin.

**Rig.** `mac-rig.sh pair wind65`: A = 15349 (host), B = 15359 (client), lease `ww/dolphin-slot-3` (from harbor), both 30 fps
start to end. Three `mp-local` runs (`MP_LOCAL_PORT=0`):
1. every kind on, `mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u,bb+u,mt+u` (Mixed, KI, AM2, T4, T6);
2. `--coop=new` with `WW_ENEMY_SYNC=0` (P2);
3. `mo2,bk+u,cc+u,ki+u,am2+u,tn+u,st+u,oship+u` (ST Q8, OSHIP). A first try listed `st,st+u`: the parser refuses a kind
   listed twice and fell back to the default kinds with a WARN on both sides; `st+u` alone takes the keyed Stalfos too.

## Results

| Row | Result | Record |
|---|---|---|
| Mixed: Siren 8 | **Pass.** 4 AM2 + 2 KI, goldens equal; B's 4 slots hold the AM2s. A host kill of one AM2: both games go to 5, and the freed slot takes Keese setID 1. | `s2-mixed-siren8.txt` |
| Mixed: kaze 9 | **Pass.** 3 dormant AM2 statues + 10 ChuChus, goldens equal. By distance B holds 4 ChuChus; a host ChuChu kill refills from the ChuChus (12 on both). With B by the statues, two statues take slots (mode 12, action 1 on both: still statues) and two ChuChus keep theirs. | `s2-mixed-kaze9.txt`, `s2-kaze9-B.png` |
| Mixed: kaze 11 | **Pass.** 2 AM + 4 Bokoblins, goldens equal; B holds the 4 Bokoblins. A host Bokoblin kill: 5 on both, and Armos Knight setID 1 takes the slot. | `s2-mixed-kaze11.txt` |
| KI LUNGE | **Pass.** `M_Dai 15 14` (6 keyed + a Stalfos), A walking under them: the host flags LUNGE (242 samples) on setIDs 2 and 5 with Tg r 20 and Co r 0; B's copies of the same two read Tg 20 / Co 0 (109 and 154 samples). | `s4-ki-lunge.txt` |
| KI Fire Keese | **Verified hop: `Cave09 0 12`.** 4 unkeyed fire Keese (params 0xFFFF8101, `fire 128` on both) + 2 Magtails, goldens equal. With B's own two removed locally, only mirrored copies are left on B, and one is drawn with its flame wings. A still frame: the btk's animation is not judged. | `s4-firekeese.txt`, `s4-firekeese-copy-B.png` |
| KI unkeyed | **Pass.** `Cave09 0 1`: 10 fliers, goldens equal, B adopts 4; B's sword kills one copy (ACK `applied_at ... health -3`), the host goes 10 -> 9 and B's copy goes. B's own unadopted Keese swarmed B and died to the swings (not mirrored; no co-op, so not relayed): expected. | `s4-ki-cave09r1.txt` |
| AM2 `Cave09 0 7` | **In part.** 4 unkeyed, goldens equal, B adopts all 4. The host's Armos are awake and crowd B: their hits on B's remote body apply (PACK -1 each), but `behind.py` could not land a back-stab (wall, then knocked away). The dance spin on B is still not on screen. | `s3-am2-cave09.txt` |
| T4 caves | **Pass.** `Cave10 0 16`: 3 Moblins + 2 Darknuts, goldens equal; a host Moblin kill frees a slot and the fifth (Darknut 0xD5DCC00D) takes it; B's sword on a Moblin copy: ACKs 8 -> 6 -> 4 -> 2 -> 0, the host deletes it, 3 on both. | `s5-t4-cave10.txt` |
| T6 | **Pass.** The fixture hops M_Dai 2 2, M_NewD2 7 4, kaze 4 4, kaze 2 2, Cave11 0 9, M_NewD2 6 3, Hyrule 0 0 under every kind: goldens equal in all 7, each room's kinds adopted. The MO2 kill with every kind on is the T4 cave kill above (and a host MO2 kill in Cave10). | `s5-t6-goldens.txt` |
| P2 Keese home | **Pass.** `--coop`, mirror off, Hyrule room 0's two wait-type unkeyed Keese (layer 4/5, params 0xFFFF0000): after a room reload they appear at their disc entries and move to their ceiling spots a second later; home x/z then still equal the entries (12735.43/-20390.51, 12670.83/-20428.30) on both games; `/status` reads 13 tracked on each. | `s6-p2-keese-home.txt` |
| P1 | **Not run.** As wind#29 found, the Boko Baba and Armos sword kills need a person; nothing new here. | - |
| ST Q8 | **Pass.** `kaze 10`, B's sword breaks the host's Stalfos setID 1; while B's copy is hidden (m02b9 1, about 4 s) B stands on its centre 10 times: sphere 1's Co RPrm 0 and HitObj null every time, Link not pushed (d 0-9 units, one 80). Control, the same copy intact: the hit bit is set in 4 of 8. | `s7-st-q8.txt`, `q8.py` |
| OSHIP P3 gold ship | **Pass.** The start save has sea switch 0x17 set, so the gold ship never loads. With 0x17 cleared (the state of a save <= 20) all 3 load: the gold ship reads model 0 and is refused at the live gate on both (`verdict=live-gate oship_live=gold`); B mirrors the other two. | `s8-oship-p3-gold.txt` |
| OSHIP client cannon | **In part.** Both boats poked beside the host's ship (switch A 0x16), B fired with X, X. With A far away, B's balls are dropped on A ("too far on the sea"). With A beside it, A spawns B's cannonballs (0 -> 8 -> 12) and the host's ship goes 2 -> 1 -> 0 within 2 s of B's first shot; B's copy reads 0 at the same moment and both games delete it. But the ship had already lost 2 hp to fire that was not B's (the other ships' or the gold ship's cannonballs), so the sinking hit is not isolated to B's ball. | `s8-oship-cannon-salvage.txt`, `cannon.py`, `moveboat.py` |
| OSHIP salvage light | **In part.** At night (mTime poked to 0): after the sink, switch A 0x16 is set on both and the room's kind-2 salvage point moved to the sink spot with the same x/z on both games, usable (flag 0): the copy's own modeDelete ran onSalvageForOship on B. The light itself is not in the frames (the camera was not aimed at it). | `s8-oship-cannon-salvage.txt`, `salv.py`, `s8-salvage-*.png` |

## Notes

- **RevalidateFailures 1.** It ticked in `Cave09 0 12` exactly when `wwdev enemy-kill` on B hit a copy that was paused for the
  mirror (the tool refused it). That came from my B-side poke, not the mirror; it stayed 1 after.
- **A died once** (kaze 10, the Stalfos) and answered the game-over save prompt: that save went to the rig's own card copy.
- **Sea switches poked** on the rig's games: 0x17 cleared (P3), 0x16 then set by the sink.

## Sweep

- Both Dolphin logs: 0 ISI, DSI, invalid read/write, exception or OSReboot lines (13-14 lines each: Dolphin buffers the file).
  `crash-context`: nothing caught on either game.
- The three `mp-local` logs: 0 panic, ERR_KEY, reject, ERROR or fatal lines.
- Canaries at the end: PauseReasserts, AnmUnresolved 0, RevalidateFailures 1 (above), WatchdogReleases +1 per load;
  canaries2 all 0. 30 fps on both games at the end.
