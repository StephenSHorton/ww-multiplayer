# wind#45: aimed projectile halves by input (Mac rig, 2026-11-01)

Rig `wind45` (slot 2), Dolphin 2603-342, `WW-mp.iso` (REL 0xA9DE0F1E, main b9d8b2d), the Mac
start save. wwdev built from main (has `face`, #271), signed with `scripts/cs-debugger.entitlements`.

## 1. A fire arrow lights Siren r5's torch by input (one Dolphin): PASS

The recipe, all by `wwdev` (`WW_DOLPHIN_PID` pinned, `ISO_PATH=~/ww-shared/disc/WW-mp.iso`):

1. `stage-hop Siren 0 5 --yes` lands Link at (6748, -1617, 1612) in r5's entry hall.
2. The torches stand on a raised ledge at the far end, above the flooded box pit. Walking there drops
   Link into the water; a warp in front of the ledge (z 4000) does too. The ledge's floor is at
   y -1542.2, between the torches: `warp-force 6700 -1520 4185`, 0.5 s, `warp-force-off`.
3. Bow on X: `poke-u32 0x803C4C10 0x000C0103` (X slot index 0x0C; the other bytes as read) and
   `poke-u32 0x803CA7D8 0x00000035`.
4. `face 6918 4185 --stay` (torch 0xE0, 218 units away), then `input 0x0400 0 0 0` to draw.
5. Fire arrow: `input 0x0420 0 0 0`, `poke-u32 0x80413078 0x00FF0000`, 0.2 s, poke 0, back to
   `input 0x0400 0 0 0`. One tap from normal.
6. Pitch: **in first-person aim the stick's Y is inverted: negative Y pitches up.** `-60` for 0.4 s
   looks at the ceiling; `+60` for 0.28 s back down; then `-45` for 0.1 s nudges of a few degrees.
   The shot goes to the centre of the letterboxed view (about y 535 of the 1280x1016 window
   capture), not the arrow's tip. Aim with the basket at the centre (`wind45/1-aim-e0.png`).
7. `input-release` shoots. A shot with the basket's lower rim at the centre missed (count 99 -> 98,
   nothing lit); one more nudge put the basket's middle there and lit it (98 -> 97).

Check: the room switch word `0x803C53B4` went 0 -> 1 (0xE0 is bit 0 of byte 0x803C53B7) and the
torch burns (`wind45/1-lit-e0.png`).

Why wind#41's shots missed: the bowl sits about two Link heights above the ledge, and wind#41
shot level from the pit side; the ledge is the only footing within a few hundred units.
