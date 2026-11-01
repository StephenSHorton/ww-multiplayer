# wind#12 LIVE-1: boss relay over Go on two Mac Dolphins (2026-11-01)

Rig: Mac rig (docs/mac-rig.md), Dolphin 2603-342, rig `wind12`, lease ww/dolphin-slot-1. A = u1 (pid 32063, host), B = u2 (pid 34808),
both from `fixtures/start-mac-1CE30685.sav` (file "Abahbob", Hyrule). Build: main c1278b3 + MP_LOCAL_PORT (this branch), BlobHash 0x04765DD0,
REL 0x1CE30685. mp-local: `--coop=new --coop-boss-kill=gohma,kalle,molgera,jalhalla,helmaroc --coop-refresh=none --yes`,
`MP_LOCAL_PORT=25601` (25565 was taken by another agent's session), `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u,mt+u`.

Setup: both files read identical boss words (t2-t7 0x38/0x38/0x38/0x3C/0x39/0x38). Before mp-local, bit 3 (boss dead) cleared on both
for t2-t7 (intro bit kept). Both bound world b83d8a075d861acb (`bound the file`, `armed in world`), enemy client adopted 4 ChuChus.

## Gohma (M_DragB 1 0): PASS
- Both hopped `M_DragB 1 0`: Btd proc 0x00EB hp 10/10 on both, boss block seq = ack = 0.
- A (killer): poke m02E0 = 1 (armor broken, rig shortcut) + A's own request `00EB424B`: APPLIED `0x01010101`, m02E4 -> 0xB,
  A's live t03 byte 0x30 -> 0x38 (bit 3).
- B: `holding t03 DRC boss defeated back ...`, `the partner killed Gohma and this game is in its arena: asking the REL ...`,
  `boss_kill 1: waiting (m02E4 0x0, state 0x1, m02E0 0)` logged ONCE across ~40 asks (seq 0x28).
- B armor broken by one poke (m02E0 = 1), no request poke: `boss_kill 51: applied (m02E4 0x1, state 0x1, m02E0 1)` once;
  B's Btd m02E4 0xB, at y -10000; B's own t03 byte 0x30 -> 0x38; Warpf (proc 0x006A) created; heart container on screen (shots/gohma-B.png).
- Sweep: no ISI/DSI/invalid access in either dolphin.log or the mp-local log.
- Pokes used: m02E0 on both (armor), A's request. Not a natural fight.

## Kalle Demos (kinBOSS 1 0): PASS
- Both hopped `kinBOSS 1 0`: Bmd proc 0x00EC hp 15/15, mMode 2 (vines up) on both.
- A: forced the fallen open flower (mMode 3, m302 2, m304 1, m310 0, mB74 0; rig/force.sh `kalle`) + A's request `00EC424B`:
  APPLIED `0x01030201`, mMode -> 0xB; A's live t04 byte 0x30 -> 0x38.
- B: `holding t04 FW boss defeated back ...`, `the partner killed Kalle Demos ... once its flower is down and open (boss_kill)`,
  `boss_kill 52: waiting (mMode 0x2, m302 1, m304 0)` once.
- B: same flower poke, no request poke: `boss_kill 92: applied (mMode 0x3, m302 2, m304 1)` once; B's mMode 0xB, own t04 byte
  0x30 -> 0x38, Warpf (proc 0x006A) created (shots/kalle-B.png). The force recipe (previously inferred) is confirmed on both.

## Jalhalla (M_DaiB 0 0): PASS
- Hop note: leaving kinBOSS needed Makar's rescue dialogue tapped through (`input 0x0100` x ~24) on both; `stage-hop` refuses
  ("an event is running") until it ends. Vanilla, not a co-op issue.
- Both: body (proc 0xD4) health 15/15, kantera + ball present, 0 Poes.
- A: JH-1 forced gate (`body+0x444 = 0x00030064`) + A's request `00D4424B`: APPLIED `0x0103650F`; health 0, death demo
  (mBodyAction 4, mActionState 0x79..0x88), A's live t06 byte 0x31 -> 0x39, then no body (NO_ACTOR).
- B: `holding t06 ET boss defeated back ...`, `the partner killed Jalhalla ... once it is frozen by light ...`,
  `boss_kill 93: waiting (mBodyAction 0, mActionState 0x3, health 15)` once.
- B: same gate poke, no request poke: `boss_kill 167: applied (mBodyAction 3, mActionState 0x65, health 15); this game's
  Jalhalla falls on this landing in the spikes ...` once; B's death demo, own t06 byte 0x31 -> 0x39, Warpf (0x006A).
- Caveat: the poked 0x65 is itself the spike landing, so the "next throw" was poked, not a real Mirror Shield freeze + throw
  (the real-item path passed on the PC, handoff real-items/LIVE-2.md:22).

## Molgera (kazeB 0 0): PASS
- Both: Bwd proc 0xDA, `m18AE` 1, intro-skip create `m18B0` -10 (table 7 bit 5 set).
- Field note: `m18AE` is the s16 at +0x18AE (low half of the +0x18AC word), `m18B0` the s16 at +0x18B0, `m1BB1` the byte at
  +0x1BB1. A first poke wrote `m18AE` as a byte (769): the REL answered WAITING `0x02010200` and wrote nothing (correct).
- A: faked pull (`m18AE` 3, `m18B0` 2, `m1BB1` 0x78) + A's request `00DA424B`, retried back to back because vanilla moves
  `m18AE` 3 -> 2 within a frame (try 1 WAITING `0x02020100`, try 2 APPLIED `0x01030200`); `m18AE` -> 0xC, A's live t07 byte 0x30 -> 0x38.
- B: `holding t07 WT boss defeated back ...`, `the partner killed Molgera ... once its tongue is pulled out (boss_kill)`,
  `boss_kill 168: waiting (m18AE 0x1, m18B0 -10, m1BB5 0)` once.
- B: same faked pull (no request poke), 2 tries: `boss_kill 185: applied (m18AE 0x3, m18B0 2, m1BB5 0)` once; `m18AE` 0xC,
  own t07 byte 0x30 -> 0x38, Warpf (0x006A) at (0, 300, 0). Pulls faked, not real Hookshot pulls (real pulls passed on the PC,
  handoff real-items/LIVE-2.md:21).

## Helmaroc King (M2tower 30 0 3): IN PROGRESS (A applied; A's crest hit not landed; B not reached)
- Both: Bdk proc 0xF0 in the tower phase (`mAction` 0x66, health 0/0 there).
- A: `rig/hkforce.sh` (Mac port) `down` -> intro (`m25A0` 0xB..0x10) -> `mAction` 0, health 20; `mask` -> `m8F8` 4,
  `m25A0` 2 -> 0; A's request `00F0424B`: APPLIED `0x01050414`, health 20 -> 0 (`bdk-probe`: ALREADY "a pending zero, the next
  crest hit kills").
- `rig/crest.py` (scripted crest hit: warp Link 110 from the head at stuck-beak, turn, B) ran ~5 min without landing the hit,
  and A's Link died (3 hearts after continue). **Rig mistake:** on the game-over "Would you like to save?" the highlighted
  button was Yes and one A press saved u1's card (rig wind12's own copy, not the shared fixtures; mp-local backed up both
  cards at start, `coop-world restore 20261101T065658Z`). No ISI/DSI in u1's dolphin.log.
