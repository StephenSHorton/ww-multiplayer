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

## Still open: the real kill on the first-visit save

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
