# wind#17: remote visuals, name tags, sea chart, salvage, registers, D4-8 on the Mac rig (2026-11-01)

Rig `cove17` (docs/mac-rig.md), lease `ww/dolphin-slot-2`. Dolphin 2603-342 (macOS, 48 MB MEM1),
`WW-mp.iso` from main c1278b3 (BlobHash 0x04765DD0, REL 0x1CE30685), wwdev and ww-multiplayer at main
c1278b3 (entitled). `wwdev mp-local` with `MP_LOCAL_PIDS` pinned (`mac-rig.sh mp-local`). The PNGs
stay on the rig Mac (`~/ww-shared/rigs/cove17/shots/`) and are not committed.

Pokes used (noted per the play rules):
- `ww-multiplayer warp` to put B in A's view. The pad moves the camera, and an agent can't
  frame a shot by stick alone.
- Event bit 0x2A80 (byte 0x2A of `dSv_event_c`, 0x803C5256, mask 0x80), set or cleared to change
  clothes, then `wwdev stage-hop Hyrule 0 0 --yes` so Link is created again with the new outfit
  (playerInit reads it, docs/09 §2). In the real game the clothes change through story events.

## V2 the remote's own clothes, and V9 casual hair: pass (one sub-case not run)

A from `start-mac-1CE30685.sav` (hero clothes), B from `state-mac-1CE30685-c.sav` (the wind#11
casual fixture, 0x2A80 clear). Both in Hyrule room 0, no `WW_*` set.

- **B casual, A hero:** A's mini-Link for B wears the casual shirt (blue top, orange trousers)
  with the katsura (blond hair, no cap, no belt buckle). The name tag `PlayerB` is above it.
  B's own Link looks the same on B. `v2v9-A-heroviewer-B-casual.png`, `v2v9-B-own-casual.png`.
- **Stage hop and REL relink (V9):** B hopped `Hyrule 0 0`. While B reloaded, A drew no mini-Link
  (no bare scalp, and no hat frame, because nothing was drawn). Once B was back, A drew it casual
  again, and from behind the full katsura shows. mp-local logged the enemy pipelines'
  `REL module linked` again. `v9-A-after-hop-relink.png`.
- **B back to hero clothes (V2):** 0x2A80 set on B (0x6B -> 0xEB) and B hopped. A's mini-Link wears
  the green tunic and cap again. `v2-A-B-back-to-hero.png`.
- **A casual viewer (V9):** 0x2A80 cleared on both, both hopped. A's own Link is casual (its own
  hair). B's mini-Link is casual with the katsura. Each keeps its own look. `v9-A-casual-viewer.png`.
- **`WW_HAIR=off`** (mp-local restarted with it): the casual remote wears the cap in the casual blue
  texture, with no katsura. `v9-A-hair-off.png`.
- **Not run:** V9's two-remote case (a casual and a hero remote side by side). It needs a third game
  with a real outfit. A `pose-fake-loop` clone of B carries B's outfit.

## V8 Master Sword scabbard: pass

Both saves (28-Hyrule3) carry the fully powered Master Sword and the Mirror Shield (`mSelectEquip` 3E 3C at
0x803C4C16). Both games were in hero clothes and hopped to `Hyrule 0 0`, so each spawned facing the
courtyard. B was warped to (-150, 0, 2500), so A's camera sees B's back on open lawn. Judged on crops
of A's frames.

- **Sheathed:** the Mirror Shield is on B's back, and below it is the blue-and-gold `podms` scabbard,
  not the hero's brown `CL_PODA`. The hilt sits behind the shield from this angle.
  `v8-A-B-masterscabbard-sheathed.png`.
- **Drawn** (one B tap on B): the Master Sword (blue hilt, blade) is in the left hand, and the empty
  `podms` scabbard stays on the back. The Mirror Shield is in the right hand. The models light like
  Link (no black or full-bright parts). The glow and swing blur aren't drawn, as designed.
  `v8-A-B-drawn-empty-podms.png`.

## V3 sword, shield, scabbard: pass in part

The Hero's Sword and Hero's Shield were poked into B's `mSelectEquip` (38 3B), then B hopped.

- **Sheathed:** the Hero's Shield (Triforce crest) is on the back. `v3-sheathed-A.png`.
- **Drawn:** the Hero's Sword is in the left hand, and the brown hero's scabbard (no `podms`) is on
  the back, empty. The shield is in the right hand. `v3-drawn-A.png`.
- **R held** (`input 0x0020` plus the trigger poke 0x80413078 00FF0000): B's guard pose, shield
  brought forward in the right hand. `v3-guard-A.png`.
- **Not judged:** sheathing again (B kept the sword out after a short walk), and **no sword**
  (sword byte 0xFF). From behind, the shield covers where the hilt and scabbard would be, so that
  frame looks the same as the sheathed one (`v3-nosword-A.png`). A side view is needed. Also not run:
  `WW_EQUIP=off`, and checking that Link #1's own sword, shield and scabbard are unchanged frame by
  frame (they looked normal in every shot).
- B's equipment was restored to 3E 3C afterwards. Neither dolphin.log has an ISI, DSI or invalid
  access.
