# wind#62 Gohma and Kalle Demos relay edge rows (Mac rig wind62, 2026-11-02)

One agent (marten) drove both sides. Rig wind62, lease `ww/dolphin-slot-5`, main cc7c512, REL 0xCAC6980B
(`WW-mp.iso`), Dolphin 2603-342, `mp-local --coop=new` with default features (A host, B client). Logs per pair:
`rigs/wind62/run<n>/` under ww-shared (mp-local.out, dolphin-A/B.log). Every pair: no ISI, DSI or invalid access in
either dolphin.log, no panic or fatal in mp-local.out.

## Fixtures

wind#30's save states are for REL 0xA9DE0F1E (before #289), so both were recaptured on this build
(`mac-rig.sh recapture`, unattended):
- `start-gohma.sav`: `fixtures/saves-new/f-5-BombsEarly-preGohma.gci` (DRC dungeonItem 0x20: STAGE_BOSS_DEMO kept,
  STAGE_BOSS_ENEMY and STAGE_LIFE clear), max life 20. Both cards hold the same GCI.
- `start-fw.sav`: `fixtures/saves/100pct-E/6-StartFW.gci`.

Because the pre-Gohma file has DRC's boss demo, its Btd starts in the fight (m02E4 0, mGohmaState 1), not at the intro
(m02E4 0xA). That suits D4-5, D4-6 and D4-7; D4-3 needs the demo bit cleared (below).

## How a kill is made (pokes)

`wwdev boss-end gohma` refuses M_DragB (its Stage is Xboss0). Its Gohma plan is one aligned word, so the kill here is
that word written by hand: Btd (proc 0x00EB, `wwdev actors`) +0x2E4 = `0x000B0032` (m02E4 0xB end, mGohmaState 0x32),
`poke-u32`. The game runs its own death demo, floor, Warpf and heart from there. Btd sat at 0x80AACC6C in every pair.

## Gohma

Pair 1 (A 18738, B 18748), both `stage-hop M_DragB 1 0`, B left standing at the entry (-1427, 2, 836):
- 04:23:44.537 UTC POKE A's Btd end word. A's death ran (m02E4 0xB, state 0x33 -> 0x39).
- B (mp-local.out 278-280): `holding t03 DRC boss defeated back`, `asking the REL ... once its armor is broken`,
  `boss_kill 1: waiting (m02E4 0x0, state 0x1, m02E0 0)`. Request 0x80413410: seq 9, 00EB 'BK', ack 9, result 02.
- **D4-7 hop** (in part): while waiting, B's seq kept rising (9 -> 0x16). `stage-hop M_NewD2 0 0` out: `boss_kill 23:
  gated (...)` (299), then `set t03 DRC boss defeated (0x803C5015, and its live copy |= 0x08)` (305). No `none` at any
  point. `stage-hop M_DragB 1 0` back: no Btd, the Warpf (0x006A) at (0, -100, 0), seq stays 0x17, no new request:
  the held bit lands as Link leaves, so re-entry is the entry-after-kill path (Q6), as wind#21 saw. The row's "after
  re-entry a new request gets its answer" cannot happen on this design; the parts that can (no `none`, the seq keeps
  rising while held, the hop's request answered) pass.
- **D4-6 PASS**: B (with the world's and its own DRC boss bit) `stage-hop Xboss0 0 0`: the refight Btd is there
  (hp 10/10), seq stays 0x17 for 35 s, no `boss_kill` line.

Pair 2 (A 79681, B 79770), both in M_DragB at the entry, both Btd in the fight, life 20/20:
- **D4-5 PASS** (within the demo): both end words written by two `poke-u32`s started together: A 04:27:21.5917 ->
  .6565, B .5916 -> .6315 (both landed within about 2 frames). Both deaths ran. B logged only the hold and `asking the
  REL` (256-257); the request was never written on either game (seq 0 throughout: each game's death demo keeps the idle
  gate shut), so there is no `already`, no `applied`, nothing twice. A logged no hold: its own kill had set its bit.
- Both heart containers (ITEM 0x0101, params 0x17FFFF08, at (-1508, 3, 590)) taken: POKE `ww-multiplayer warp -1508 3
  590` onto them, "You got a Heart Container!" (`run2/A-heart.png`), A pressed on both: max life 0x14 -> 0x18 on both.

Pair 3 (A 9476, B 9486), `start-gohma-intro.sav`: the pre-Gohma GCI with DRC's STAGE_BOSS_DEMO cleared
(`wwfixture -craft ... di-=3:5`, built from `fixtures/saves-new/src`; the prebuilt binary predates `di-=`). Both
`stage-hop M_DragB 1 0`: Link at (-1427, 2, 836), about 130 from (-1534, 0, 909); both Btd at y -10000, m02E4 0xA.
Btd was at 0x80AACC38 on A and 0x80AACC6C on B (read it per game).
- POKE (setup): A `warp -1100 3 640`, out of the hold radius. A's intro played (state 0xA/2) and A's fight began
  (m02E4 0, state 1) 26 s later. 04:35:59.104 POKE A's end word.
- **D4-3 PASS** (the armor break poked): B, still at the entry, logged (mp-local.out 277-279) the hold, `asking the
  REL`, and `boss_kill 1: waiting (m02E4 0xA, state 0x0, m02E0 0)` once; B also held A's `t03 DRC boss demo seen`
  (225), so B's intro could still play. B then walked in by stick input (forward, 2.5 s: B moved to (-1373, 3, 790)
  and its intro started; Btd 0xA/2). After the intro, through the armored fight: `boss_kill 16: waiting (m02E4 0x0,
  state 0x1, m02E0 0)` (322), once. POKE B's armor byte Btd+0x2E0 = 1 at 04:39:40.352: `boss_kill 253: applied (m02E4
  0x0, state 0x1, m02E0 1)` (506), once. B's Gohma died from the fight (m02E4 0xB), not at y -10000: floor, sparkles
  and the heart rising (`run3/B-after.png`), the Warpf at (0, -100, 0).
  Slip: a first armor poke at 04:38:18.846 wrote the half-word (byte +0x2E1 = 1, +0x2E0 still 0); the REL kept
  answering `waiting` (m02E0 0) and +0x2E1 was put back to 0 with the correct write 80 s later.
  After both hopped to `M_NewD2 0 0`: 0x18 on both, DRC dungeonItem 0x38 on both, no further line. One container each.
