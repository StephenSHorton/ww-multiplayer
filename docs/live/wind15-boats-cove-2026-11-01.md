# wind#15 boats, the rows left: BT-4, BT-7, BT-9 (Mac rig cove15, 2026-11-01)

Rig `cove15`, lease `ww/dolphin-slot-2`. Dolphin 2603-342, `WW-mp.iso` (BlobHash 0x04765DD0, REL
0x1CE30685), wwdev and ww-multiplayer built at main b9d8b2a (entitled). Both from
`start-mac-1CE30685.sav` (28-Hyrule3: sail, Hookshot, Mirror Shield). `mac-rig.sh mp-local`, no
co-op, boats on (no `WW_BOATS`). Picks up ridge's wind#15 record (`wind15-soft-reset-mac.md`). POKE marks
a write standing in for play.

## BT-7 three boats, sails up: pass (sail colours)
- B boarded by POKE (`warp` onto its docked ship at Outset: Link lands seated, HUD "Get Out"/"Cruise"),
  then raised the sail with its own Y (the Sail, live item byte 0x78) and sailed off.
- `pose-fake-loop` Fake3 (`WW_COLOR=red`) and Fake4 (`blue`) captured from B with the sail up, each
  `face trailer 59 bytes`. On A, link slots 1 and 2 hold the fakes, and the boats block's drawn
  counter rises about 96 a second, 3 boats a frame (magic WWBT, flags 3, error 0).
- The fakes follow B's live position (+1000 X), not the capture point. A's ship was moved by POKE
  (`current.pos`, +0x1F8) 1100 units behind B along A's heading. A's frame (`bt7-A-three-boats.png`):
  PlayerB's boat with a green sail (B's colour), and at Fake3's spot the two fakes' sails, blue (Fake4)
  over red (Fake3, whose edge shows), each with its tag. Each sail is in its remote's colour. The
  fakes overlap by design (same position).

## BT-4 parked in Tower of the Gods: "only while A is in that room" pass at the stage level
- B entered `Siren 0 0` aboard (a hop made while aboard), got out (A press), then left for `M_NewD2`, so
  the parked snapshot froze. A was in `Siren` room 0, aboard.
- A's drawn counter: 32/s while B was in Siren, 31/s with B gone (B's boat parked), **0/s when A went
  to the sea**, 32/s again when A came back to Siren r0. A at 30 fps, boats error 0.
  `bt4-A-siren-parked.png`.
- Not judged: the lighting (B's parked boat sits exactly on A's own boat, the same spawn, so they
  overlap) and another room inside Siren. When B left aboard instead (no get-out), A drew nothing after
  B left: the boat goes with its sailor, as designed.

## BT-9: still in part
- A's drown-and-return (A got out and swam until stamina ran out) with three boats drawn: the drawn
  counter kept rising (3489 in about 40 s, about 2.9 a frame) through the fade, error 0, no crash. The
  return is a fade to black, not a letterboxed cutscene, and B's boat was ahead, not in A's camera, so
  "B's boat in view during a cutscene" is still not shown (`bt9-A-drown-swim.png`).
- "A reboot from play with the boat in view": a reset from play goes to the title (SR-1's path), and
  the reboot path is the logo's (SR-2). Not reachable from play on this build; recorded, not run.

## Found: OSPanic on `stage-hop Siren 1` (deterministic; carded as wind#43)
- Twice on A: after a long session (three boats, a drown-return and ship pokes), and again on a freshly
  booted A from start-mac with mp-local not running. `stage-hop Siren 1 --yes`: about 20 s later
  dolphin.log has `見積もりヒープサイズ(00003800)で登録失敗しました。[door10]` (door10's heap registration
  failed at its estimated size), then `Invalid write to 0x01234567, PC = 0x80006d64` (inside
  `OSPanic`), 0 fps. `Siren 0 0` loads fine. Same pattern as wind#23's kazeB panic ([door12]). Whether
  vanilla does this at Siren point 1 isn't known yet.

## Logs
- Both dolphin.logs, BT-4's boot: clean. A's two panics above are the only crash lines in A's log
  (boots 1 and 2). No mp-local log has a panic.
- BT-10 is left as it was (V6 by eye: see wind#17's V6 pass on c1278b3, `wind17-visuals-salvage-mac.md`,
  PR #239; P6 by eye not run here).
