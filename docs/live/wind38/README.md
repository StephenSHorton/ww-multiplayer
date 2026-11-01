# wind#38: the M2tower -> M2ganon hang (Mac rig, 2026-11-01)

Follow-up to wind#37 (`docs/live/wind37/README.md`): walking through M2tower's door10 into M2ganon
hung both games (0 fps, `Invalid read from 0x00000000, PC = 0x802c1bf0`, JUTNameTab) after FF's
STAGE_BOSS_ENEMY was poked instead of a real Helmaroc kill.

**Rig.** Mac rig `wind19`, one Dolphin (2603-342), no co-op process, lease `ww/dolphin-slot-2`,
`WW-mp.iso` (REL 0x1CE30685). Two save states: the Mac start save (`start-mac-1CE30685.sav`,
CryZe `28-Hyrule3`, late game) and wind#37's `start-ff.sav` (CryZe `9-Hyrule1`, the story's first
FF2 visit). Link kept alive with `scripts/rig-helmaroc/healer.sh`. The fight helpers in
`scripts/rig-helmaroc/` are the PC's `enemy-batch-f/live/hammer` scripts ported to macOS
(`fight.sh <pid> <bdk> mask|crest|lunge`).

## Results

| Save | How FF's boss bit (0x08 of table 2) got set | door10 -> M2ganon |
|---|---|---|
| `start-ff` (9-Hyrule1, first visit) | poked into the live dungeon byte (0x803C53A1), as wind#37 | **hang**: 0 fps, invalid reads at PC 0x802c1bac and 0x802c1bf0 (same as wind#37) |
| Mac start (28-Hyrule3) | already set by CryZe's real playthrough; nothing poked | **plays**: M2ganon r0, 30 fps, clean dolphin.log |
| Mac start (28-Hyrule3) | cleared, then a fight: tower phase and mask by real hammer swings, then the bird's own death action (poke: mState 0, mAction 0xA) ran to END: the bird moved itself out, the spikes lowered (Ttoge -500.0), and the game set the bit | **plays**: M2ganon r0 with Link and Tetra, 30 fps, clean log |

So the hang follows the story point, not co-op and not (on this evidence) the way the bit was set:
on the late-game file, where M2ganon's first-visit scene (`attack_ganon`) has already played, the
exit never hangs; on the first-visit file it hangs. Likely [I]: `attack_ganon` plays on the first
visit and needs something the real tower-top death demo sets up (a resource or an event state),
which a poked bit skips.

## The real kill on the first-visit save (pine, 2026-11-01): still hangs

Rig `wind38` (own u1, Irori lease `ww/dolphin-slot-5`), one Dolphin at 30 fps, no co-op process,
`start-ff.sav` (CryZe `9-Hyrule1`). FF's boss bit was **not** poked. A `wwdev` built from main (with
`wwdev face`, wind#41) aimed every swing; the shared `~/ww-shared/bin/wwdev` lacks `face`.

1. **Setup pokes.** Skull Hammer into slot 20 and onto X (`0x803C4C58` = 0x33, `0x803C4C11` = 0x14,
   `0x803CA7DB` = 0x33). `stage-hop M2tower 30 0 3`. Healer on.
2. **Tower phase** by real X swings (warp to (2620, 9820, -3980), `face` the head, X at each peck).
3. **Mask** by real X swings: 2, then 1, then 1 hit over three runs (`fight.sh mask`). The pecks came only
   with Link back at the arena middle (4300, 9810, -4300) after each swing (`fight2.sh` below); against
   the wall the bird walks and never pecks. KAMEN_DEMO, `m8F8` 4.
4. **Crest.** One real sword hit landed (health 20 -> 19). Then **POKE**: health 19 -> 1 (s8 at Bdk +0x285),
   so the next real hit ends it the game's own way. The hit landed once Link had **put the hammer away**
   (A): with the hammer in hand, B never touched the crest (about 35 misses). This is very likely why
   wind#38's first 107 scripted crest tries missed.
5. **The death, vanilla.** `mAction` 0xA, the death camera (`m25A0` 0x65 ...), and at about +18 s the game set
   FF's bit itself: table 2 live byte `0x803C53A1` 0x20 -> 0x28. The bird moved out (100000, 100000), the
   Ttoge word read -500.0 (spikes down), and the heart container dropped. Link walked into it ("You got a
   Heart Container!", 8 hearts).
6. **The exit.** Warp in front of door10 (1891, 11124, -8450), `face` the door, stick forward and A.
   The stage name read `M2ganon`, then **0 fps** for the 20 s watched, with dolphin.log full of
   `Invalid read from 0x000000xx, PC = 0x802c1bac` / `PC = 0x802c1bf0` (JUTNameTab), as in wind#37.

**Verdict.** On the first-visit save the M2tower -> M2ganon exit hangs **after a real kill too**. The poked
bit is not the cause. What hangs is M2ganon's first-visit demo, `attack_ganon` (next section).

**Observed, not explained.** `start-ff` boots with the Hero's Sword equipped (`mSelectEquip[0]` 0x38) and
only it collected (`0x803C4CBC` byte 0x01). After the kill both read Master Sword 1 (0x39, collect 0x03).
What set them during the run was not traced. The late save has 0x3E (Master Sword 3).

Dumps of `0x803C4C00`..`+0x900` (boot, after the kill, late save), the logs and the screenshots are in
the rig's `rec/` folder on the rig Mac, not committed. Between the after-kill and late dumps, 168 event
bits differ (the late save is far later in the story), so the diff alone doesn't name a cause.

## What hangs: the `attack_ganon` demo (s5, pine, 2026-11-01)

Same rig and slot, fast repro (hop, poke the bit, door10; the same hang as after the real kill).

1. **The trigger is the demo.** M2ganon's stage.dzs has one event, `attack_ganon`, with spawn switch 61
   (stage switches: live word `0x803C5388`, bit 0x20000000). On `start-ff`, switch 61 on before door10
   gives M2ganon at **30 fps**, Tetra standing by, and no invalid reads (`ff-sw61.png`). Off (as the
   first visit has it), the demo starts and hangs.
2. **The late save was never a control for the demo.** On `28-Hyrule3` switch 61 is already on from the
   real playthrough, so M2ganon loads straight to free play (`late-m2ganon.png`): `attack_ganon` never
   runs there. "The late save plays" says nothing about the demo.
3. **Where it hangs (decomp).** PC 0x802c1bac is `JUTNameTab::getName` and 0x802c1bf0 is `calcKeyCode`
   (`config/GZLE01/symbols.txt`). The read pattern (getName from 0x7, 0xB, 0xF, ... and calcKeyCode
   on 0x1/0x0) fits a J3D material-animation bind, `searchUpdateMaterialID`
   (`J3DAnimation.cpp:699, 865, 881, 888, 1064, 1071`), on an animation whose name table is null, the
   usual result of binding a texture/colour animation that a `getObjectRes` didn't find. The reads stop
   after about 10k lines; then the main thread (`mainThread` 0x803A2960) sits in WAITING and the frame
   never advances. The bind's frames were gone by then (no return address into `searchUpdateMaterialID`, a DOL function,
   anywhere in MEM1; the scan resolved DOL text only, so REL callers were not looked for), so the calling actor is not identified yet.
4. **Archives.** The object table (`0x803E0BC8`) in M2ganon holds the same archives on both saves
   (System, Demo24, Always, Link, Agb, Search, Himo3, Zl2, Dmgroom, Ikari, Cloth, Ep), so no archive is
   missing on the first-visit save as such.
5. **Tetra** (`Zl1` params 6 -> arc `Zl2`) null-checks the demo's btp/btk (`d_a_npc_zl1.cpp:2566-2586`),
   so she is not the unchecked bind.
6. The sword (0x38 vs 0x39) doesn't matter: the fast repro hangs with the Hero's Sword equipped.

**Open: vanilla or the mod?** Every run here was on the patched `WW-mp.iso`, and a save state carries
the patched code in RAM, so swapping discs isn't a vanilla test. Next: boot `ww-vanilla.ciso` with the
fixture memory card, load the 9-Hyrule1 file, get to M2ganon with switch 61 off (needs a controller or a
person: `wwdev input` is a mod feature), and see whether `attack_ganon` plays. If vanilla plays it, the
mod's Link face/eye hooks (`eyefix`, `face`, which touch Link's material animations) are the first
suspects. If vanilla hangs too, it's the hop: the save skips FF2's entry story, which the demo expects.

## Earlier attempt (lynx): the scripted crest never landed

The card's check (a real Helmaroc kill on `start-ff`, then the exit) is **not done**:

- On `start-ff` the tower phase (2 swings) and the mask (12 + 3 swings) went down by real hammer
  input (the Skull Hammer poked into slot 20 and onto X: 0x803C4C58, 0x803C4C11, 0x803CA7DB; the
  save predates it).
- The crest never took a hit by script: 91 `lunge` tries (B with the stick toward the stuck head
  from 160 units) and, on the other save, 16 standing B swings from 110. The PC's LIVE-2 landed it
  in 3 tries; the difference was not found.
- A death-action poke on that bird left it gone with no bit set and the spikes up (not a death).

Next: a crest hit by a person (one keypress fight from the mask demo on), or by better scripted
aim (lock-on with the trigger poke, `0x80413078`, before B), then the exit. If it plays, the hang
is the stand-in's; if not, find what `attack_ganon` needs (diff the event bits of the two saves).

Evidence (screenshots, logs) is in the rig's `rec/` folder on the rig Mac, not committed.
