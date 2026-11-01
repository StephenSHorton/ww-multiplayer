# wind#12 LIVE-2: Gohdan over Go on two Mac Dolphins (2026-11-01, maple)

Rig wind12g (slot 1), Dolphin 2603-342. Binaries from main b9d8b2a, REL 0x1CE30685 (unchanged since #206).
CryZe 8-StartTogs (pre-TotG) save state on both (wind#22's start-totg.sav). Both sides ran `stage-hop SirenB 1 0`.
A is the host and B the client: pids 49704 and 49720 in run 2, 89760 and 89770 in run 1. `mp-local --coop=new --coop-refresh=none`.

## Run 1: v0.6.0's rows leave Gohdan out: PASS
- Flags: `--coop-boss-kill=gohma,kalle,molgera,jalhalla,helmaroc`, world b30f0ebc3c9dad27.
- POKE: A's live dungeonItem |= 0x08 (`0x803C53A0` = 0x00080000), standing in for A's kill.
- B logged `holding t05 TotG boss defeated back ...`, then this once (one line over 20 s):
  `the partner killed Gohdan, but --coop-boss-kill (gohma,kalle,molgera,jalhalla,helmaroc) leaves it out: this game's arena keeps its Gohdan ...`
- B's request block stayed all zero (no request). No ERROR or panic.

## Run 2: default rows (all six since v0.6.1): PASS
- World 8413e1f364a5cf28. Fresh state on both.
- A's kill used `rig/gdforce.sh <A> 1`, the P0-BST recipe, then A's request `00F2424B`.
  - A's REL answered APPLIED `0x0107030C`.
  - A's head reached act 22 (dead), and A's Warpf (0x006A) appeared at (0, 0, 0).
  - A's own t05 dungeonItem went 0x20 -> 0x28 from the real death; no poke.
- B logged:
  - `holding t05 TotG boss demo seen back`
  - `the partner killed Gohdan and this game is in its arena: asking the REL to kill this game's Gohdan once its mouth is open for a bomb (boss_kill)`
  - `boss_kill 1: waiting (action 0x1, mDamage 2, m2E7C 11)`
- B's mouth phase was forced by `rig/gdforce.sh <B> 0` (the recipe, no request poke). B logged:
  - `boss_kill 59: waiting (action 0x7, mDamage 1, m2E7C 12)`
  - `boss_kill 62: applied (action 0x7, mDamage 3, m2E7C 12)`, once.
  - The request block read seq 0x3E, `00F2424B`, ack 0x3E, APPLIED `0x0107030C`. Afterwards `bst-probe` reads ALREADY.
- On B: the death ran, Warpf (0x006A) appeared at (0, 0, 0) with the heart container (`shots/gohdan-B-after.png`), and B's own t05 byte reads 0x28.
- Sweep: one `applied` line, no ERROR or panic in mp-local, and no ISI, DSI, invalid access or JUTException in either dolphin.log.

**Caveat:** both kill windows were forced by the P0-BST recipe, as on the PC (TESTING "Two-Dolphin Gohdan"). A natural fight is GD-4, still owed.
