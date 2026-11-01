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

## V4 name tags, the rest: pass in part

A in Hyrule, B warped into A's view. Item pokes: A's X button set to the telescope (save
`mSelectItem` slot 0, live 0x803CA7DB = 0x20) and then the deluxe Picto Box (slot 8, 0x26), pressed
with `wwdev input 0x0400`. Restored to 0x22 afterwards.

- **Telescope:** B warped straight ahead, so it is inside the telescope view. B's mini-Link is drawn
  and has no tag. `nt-A-telescope-B-in-view-no-tag.png`. After putting it away (B), the tag is back
  over B. `nt-A-tag-back-after-telescope.png`.
- **Picto Box:** B is in the viewfinder, with no tag. `nt-A-pictobox-B-in-view-no-tag.png`.
- **`WW_NAMETAGS=off`** (mp-local restarted with it): B is drawn with no tag. `nt-A-nametags-off.png`.
- **Not run:** a cutscene or event.

## V5 sea chart, the rest: pass in part

Both games hopped to `sea 0 44` (Outset), both in hero clothes. A opened the chart with D-pad Up
(`input 0x0008`), not D-pad Right (which only toggles the minimap zoom), and zoomed with A.

- **Heading:** at zoom 2, A's own yellow arrow points north and B's green marker points another way,
  matching where B faced. `sc-A-zoom2-B-heading.png`.
- **Live turn:** with A's chart open, B turned (stick right, 300 ms), and B's arrow on A's chart
  turned with it. `sc-A-zoom2-B-turned-live.png`.
- **Zoom:** the marker stays on Outset's island through zoom 1 -> 2. `sc-A-chart-open.png`.
- **`WW_SEACHART=off`:** only A's own arrow, no marker for B. `sc-A-seachart-off.png`.
- The chart closed cleanly each time (B twice). Visuals block (`include/wwvis.h` +0xE0..+0xEF: tags queued, equipment models,
  sea markers drawn, last error) on A after the chart: `3E0D EDDA 046D 0000`. The last-error word
  is 0, which is a pass, and it was still 0 on both games at the end (smoke's `rel last error` checks).
- **Not run:** a move while B sails (V5's core run moved B by warp), chart comparison, the Fishman,
  and the island and dungeon no-marker cases.

## Observation: a yellow crown on B's cap for one frame

On the sea at Outset's pier, one frame of A (`obs-A-yellow-crown-one-frame.png`) showed B's mini-Link
(hero clothes, 0x2A80 set on B) with yellow on its crown above the cap rim, as if the katsura were drawn
over the cap. Four later frames from the same spot show a normal green cap, and B's own view of A is
normal. This rig switched B casual -> hero -> casual -> hero earlier (V2/V9). It was not seen again, so
no card yet. The V2/V9 hero frames (`v2-A-B-back-to-hero.png`, the V8 crops) have a green crown.

## V6 regression sweep: pass

`wwdev smoke --no-boot --hop "Hyrule 0 0"` attached to the pair (`MP_LOCAL_PIDS`, `USER_DIR_1/2` = the
rig's u1/u2) ran its own mp-local: 54 checks, 0 failed. That covers heartbeats at about 30 fps, the
REL linked, no JUTException, visuals last error 0 on both, pose publish and feed, A tracked on B
(0.9 off) and B tracked on A (4.0 off), the hop and back, and mp-local exiting 0. The first try,
`--hop "sea 0 44"`, was refused because A was already in sea. Both dolphin.logs have no ISI, DSI or
invalid access across the whole session, and no mp-local log has a panic.

## Co-op: side-quest registers (#191) and the owner's tables

`mp-local --coop=new --yes`, with `USER_DIR_1/2` set to the rig's u1/u2 and `ISO_PATH` set to the
shared `WW-mp.iso`. Both files bound to world 22956478a60a3827. Note: `mac-rig.sh mp-local` doesn't
export the rig's user dirs. The first co-op try backed up (read only) the Mac's default Dolphin
cards and declined the bind with no TTY, so that run is ignored. `--yes` answers the bind.

Event base 0x803C522C. Before: both files Orca 0xD0 = 3, Koboli 0xC2 = 3, pig pen 0xBF = 0.
Pokes: A's 0xD0 3 -> 4 and 0xBF |= 0x01, B's 0xC2 3 -> 1 (so A's level wins the MAX). Then A hopped,
then B hopped.

- B's log: `a stage change starts: writing 0 story bits and 3 registers of the world`, then
  `set event byte 0xBF (... |= 0x01)`, `0xC2 (... |= 0x02)`, `0xD0 (... |= 0x04)`.
- B reads 0xD0 = 0x04 (not 0x07, so the write masks and sets the level field, as MAX, even though the
  log says `|=`), 0xC2 = 3, 0xBF = 0x01. A is unchanged.
- **Orca, Koboli, pig pen: the register half passes, by poke.** B playing Orca, B's post office
  (Baito), and B's Outset pig after 0x0520 were not run.
- **The owner's tables** (`ww-multiplayer coop-tables --no-cache` on `WW-mp.iso`): `hearts: PASS`,
  `heart pieces: 44, vanilla 44: 28 in stage files (chest 12, dig 1, item 1, salvage 14), 11 rewards
  (auction 1, letter 3, npc 6, shop 1), 5 unverified (drop 1, minigame 3, npc 1)`, both
  `left out Cave06: ...` and `left out I_SubAN: ...` lines, small keys PASS (5 of 5), heart
  containers 6/6. Not compared line by line with a schema-11 report.

## Salvage catch-up SV1-SV7: SV1 form (b) pass by poke, SV7 in part, the rest carded

Same co-op session, both games on `sea 0 44` (Outset's square, ocean grid 44). B's salvage table
(`*0x803F6B44`, parsed per `wwapi/addr/salvage.go`) listed room-44 points saves 0-3 (kind 3, flag 0),
the Big Octo save 15 (kind 2, already flag 1), and the no-save kind-5 points.

- **SV1 form (b), standing in for A's crane by poke:** A's ocean grid 44 bit 2 (0x803C5221 |= 0x04)
  stands in for A pulling up point save 2 at (-211024, 0, 309091). A hopped. B, in the light's square,
  was warped across the border to (-95000, 0, 320000), a room change with no stage change. B's log:
  `ocean grid 44 bit 2 waits for the next room or stage change`, then `set ocean grid 44 bit 2
  (0x803C5221 |= 0x04)`. There was no `salvage: marked` line (a recorded outcome, as the row allows).
  B was warped back into square 44: its rebuilt salvage table no longer lists save 2 (saves 0, 1, 3 and
  15 remain), so the light is gone and B's crane has nothing there.
- **SV7, in part:** no verify failure, no co-op shutdown across the session's 42 co-op lines. Only SV1
  ran, so this is in part.
- **Not run, carded on wind#1:** SV1 form (a), SV2 (a chart treasure), SV3 (a Big Octo point),
  SV4-SV6 (B's own crane pulls, the window, the crane gate). They need B to board and sail the King of
  Red Lions and pull with the crane by pad, which this rig can't script reliably yet.
