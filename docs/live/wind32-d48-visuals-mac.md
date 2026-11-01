# wind#32: D4-8 rest and the visuals sub-cases (Mac rig, 2026-11-01, maple)

Rig wind32 (slot 3), main ecfd46e, Dolphin 2603-342, REL 0x1CE30685. CryZe 4-WindWaker (pre-DRC) save state on both, `mp-local --coop=new --yes`.
A = host pid 44231, B = client pid 44326, 30 fps, both granted every feature. Both files start at max life 16 (4 hearts).

## D4-8: one container each

### Run 1: A first and B held, then B leaves without taking its container. PASS
- Both hopped `M_DragB 1 0`. A walked forward by stick, which started A's intro; A's Btd went m02E4 0xA -> fight (1/4).
- POKE (A's kill, as wind#17 did): A's Btd armor byte +0x2E0 = 1 and +0x2E4/+0x2E6 = 0x000B/0x0032 (the lethal eye hit, d_a_btd.cpp:463-475). A's own death ran: A's live t03 byte 0x20 -> 0x28. The warp flower appeared, and the heart container dropped (item 0x08, pid 537).
- B (still in its intro): "holding t03 DRC boss defeated back", "asking the REL to kill this game's Gohma once its armor is broken", "boss_kill 1: waiting (m02E4 0xA ...)". B walked forward by stick, which started B's intro and fight: "boss_kill 127: waiting (m02E4 0x0, state 0x1, m02E0 0)".
- POKE: B's armor byte +0x2E0 = 1 (standing in for B's own rock drop). B: "boss_kill 130: applied (m02E4 0x0, state 0x1, m02E0 1)". B's own death ran (B's byte 0x28) and dropped B's container.
- A took A's container (warp onto it; "You got a Heart Container!"): A max life 16 -> 20. B stayed at 16 and logged one line, "holding t03 DRC heart container taken back: this game's own boss drops its own container (d_a_btd.cpp:1082); written once Link leaves the boss stage, unless Link takes it first". No derive write on B while it stayed in M_DragB.
- B left WITHOUT taking its container: warped onto the warp flower (POKE `warp 10 -100 10`) and the flower took B to Adanmae. About 6-12 s after landing: "set t03 DRC heart container taken (0x803C5015 |= 0x10)", "the derive path wrote 1 of 1 writes", "wrote max-life counter +4 at 0x803CA77E: max life: 16 -> 20 quarter hearts". One write, as the row says.
- B sat through Adanmae's post-DRC story scene (A presses), then went back into M_DragB (`M_DragB 1 0`): only the warp flower, no item actor, no Btd. Max life stays 20, and there's no "duplicate pickup" line. PASS.

### Run 2: the mirror, B picks up first. PASS
- Fresh pair from the same state, new world. Both hopped `M_DragB 1 0` and walked forward by stick (both intros, then both fights).
- POKE: A's lethal hit as in run 1 (A's byte 0x28). B: "boss_kill 1: waiting (m02E4 0x0, state 0x1, m02E0 0)". POKE: B's armor byte = 1. B: "boss_kill 4: applied (m02E4 0x1, state 0x3, m02E0 1)", and B's own death ran (B's byte 0x28).
- B took B's container first (warp onto item 0x08, params 0x17FFFF08): B 16 -> 20. A, still in M_DragB with its own container on the floor, logged one "holding t03 DRC heart container taken back ... unless Link takes it first" and no derive write.
- A took A's container: A 16 -> 20. Both stay at 20 after A leaves for M_NewD2. No "a duplicate pickup" line in either log, and no max-life counter write: each file has one container.
- Each container was picked up by warping onto it (POKE; with blind stick taps, A's presses opened a hint stone instead).

## V3 sword, shield, scabbard (in part): PASS for the Hero's Sword and Shield and for WW_EQUIP=off
- B placed 200 units to A's side at the M_NewD2 entrance (POKE `warp`); A's screenshots cropped around B's mini-Link, compared with B's own screen.
- Sheathed (after B's "Put Away", A pressed by pad): the mini-Link carries the Hero's Shield on its back and the hilt over the shoulder, as B's own Link does (`v3-B-sheathed`, `crop-v3-A-sheathed2`).
- Drawn (B pressed B, real input): the blade is in the mini-Link's left hand. With the shield raised (R trigger poke 0x80413078 = 00FF0000), it crouches with the sword in its left hand and the shield up in its right (`crop-v3-A-shield2`). When B's own screen showed "Put Away" (sword drawn, shield in hand), the mini-Link matched (`v3-B-own`, `crop-v3-A-now`). The models light like Link: no black or full-bright parts.
- `WW_EQUIP=off` (mp-local restarted with it; it reaches both pipelines of the one process): the mini-Link shows no shield and no hilt, only the strap and the scabbard base, i.e. the old look (`crop-v3-A-equipoff`).
- Not run: the Master Sword (V8) and a B without a sword. Neither is on this file (4-WindWaker has the Hero's Sword); they need 16-AfterET-or-later and a pre-sword save.

## V4 a cutscene (not run)
- Two tries didn't produce a real cutscene with B on screen. `M_NewD2 0 7` (mode 6 demo) arrived without a demo, and `kinBOSS 0 0` on this pre-FW file played no intro. In both, the HUD stayed up and so did the tag, which is consistent (the tag hides with the HUD), but the cutscene case itself isn't judged.

## V5 (in part): dungeon no-marker PASS
- A in kinBOSS with B in the same room opened its map (D-pad Up): the Forbidden Woods dungeon map, with only A's own head marker and no marker for B (`v5-A-dungeon-map`). Last-error word 0x804138BC = 0.
- Not run: sailing (boarding isn't drivable by blind pad, see wind#31), the Fishman, the island case.

## V9 casual and hero side by side (not run)
- Needs a third real game (two remotes, one casual and one hero), so three Dolphins under two slots. Not taken this run.

## The yellow crown observation
- Not seen in any A frame this run: no sea frames were taken with B on screen.

## Logs
- Both dolphin.logs swept after each run: no ISI, DSI or invalid access.
