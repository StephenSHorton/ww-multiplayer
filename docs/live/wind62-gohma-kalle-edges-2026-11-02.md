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
  590` onto them, "You got a Heart Container!" (`run2/A-heart.png`), A pressed through: max life 0x14 -> 0x18 on both.
  After both hopped to `M_NewD2 0 0`: 0x18 on both, DRC dungeonItem 0x38 on both, no further line. One container each.
