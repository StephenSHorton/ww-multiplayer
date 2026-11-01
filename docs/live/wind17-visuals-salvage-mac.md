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
