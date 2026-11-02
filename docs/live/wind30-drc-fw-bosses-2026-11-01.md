# wind#30 DRC and FW bosses by real input, two agents (Mac rig wind30, 2026-11-01)

Follow-up to `wind21-drc-fw-2026-11-01.md`. Two agents, one each side: oak drove A (host), orca drove B (client).
Rig wind30, lease `ww/dolphin-slot-4`, `mp-local --coop=new` with default features, REL 0xA9DE0F1E (main b9d8b2d).
Logs per pair: `rigs/wind30/run1..run3/` (mp-local.out, both dolphin.logs). Every pair: no ISI, DSI or invalid
access in either dolphin.log, no panic or fatal in mp-local.out.

## Fixtures

- Gohma: `fixtures/saves-new/f-5-BombsEarly-preGohma.gci`, CryZe 5-BombsEarly with DRC (table 3) dungeonItem
  0x38 -> 0x20 (STAGE_BOSS_ENEMY and STAGE_LIFE cleared, demo kept), made with `wwfixture -craft` (new op `di-=T:B`).
  Grappling Hook owned. Max life 20 (DRC's container already in the save, so D4-8 reads 20 -> 24).
- Kalle Demos: CryZe 6-StartFW. It has no Boomerang (item slot 5 = 0xFF).

## Pokes (setup only, none on bosses, rocks or boss bits)

- Life refills, many, on both: `0x803C4C08 = 00140014` (Gohma) / `00150015` (FW). Gohma and Kalle kept hitting Link
  while the scripts waited; a background loop held B's life while A fought.
- `stage-hop M_DragB 1 0` / `kinBOSS 0 0` to enter the arenas, and one re-hop of B to the M_DragB entry in pair 1
  before the first throw (B's spot by the wall capped the look-up angle).
- FW: item slot 5 = Boomerang, `0x803C4C48 = 0x242D3421`. It was then equipped to Z by input (Start, stick right x5,
  Z, Start). Poking mSelectItem (0x803C4C13) alone does not work: Z still drew the Deku Leaf.

## Real-input recipes (scripts on the rig and in `scripts/hookaim.py`)

- Gohma armor: `hookaim.py --no-walk --timeout 8`. It aims the camera's sight line (camera at
  `*0x803CA718`, eye +0xD8, centre +0xE4) about 800 above the KUI, because himo2's search_target takes 400 below to
  2000 above in pitch and about atan(60/dist) in yaw (d_a_himo2.cpp:507), and throws as the swaying tail passes the
  reticle. A catch reads KUI health 1 (the wrap) then 3 (hanging). Then wait for the rope (proc 0x01C4, byte +0x24D9)
  to reach state 5 and press A (Let Go): himo2 state 6 arms Valoo's rock (dr2 unk_4CA = 0x5A); swinging is not needed.
  The rock state (dr2 +0x4BA) runs 1, 2, -1 (hit). Three hits: Btd m02E0 (+0x2E0) 0 -> 1. 1-4 tries per catch.
- Gohma eye (oak): `eye.py` slashes when the eye (Btd +0x3E0) is under y 300 and near, Link held at radius 1420-1500.
- Kalle vines: the Boomerang is held to aim and released to throw (a tap throws at nothing). `hookaim.py --hold
  --button 10 --target-addr <vine+0x260>` with set-and-leave input (ms 0) so Z stays down between aim pulses;
  sweeping the sight with Z held locks up to 5 vines (daBoomerang mLockCnt +0xF31) and one release cut 5 (Bmd m331
  0x14 -> 0x0F).

## Gohma (s2)

Pair 1 (A 85691, B 85701):
- B broke its own armor by input (three drops): m02E0 1 at 19:03. A broke its own the same way.
- **D4-1 PASS**: A's kill by input; B's log `boss_kill 1: applied (m02E4 0x1, state 0x5, m02E0 1)`, from Gohma's
  attack state 0x5, not wait.
- **D4-4 PASS** on B by input: death demo, the lava cools to a floor, Warpf (0x006A) at (0,-100,0), heart container
  ("You got a Heart Container!", max life 0x14 -> 0x18), the flower warp. B's DRC dungeonItem held at 0x20 in M_DragB,
  0x38 on leaving. The landing is Dragon Roost Island (sea room 13), not Adanmae: 5-BombsEarly is past Valoo's
  first-clear event.
- **D4-8 PASS** on both files: +4 once each; A's STAGE_LIFE held while A was in M_DragB and landed on leaving.

Pair 2 (A 92660, B 92747):
- **D4-2 under fire PASS**: B armor broken by input, standing at the rim under Gohma's fire (life falling) when A's
  last hit landed. `applied (m02E4 0x0, state 0x1, m02E0 1)`. The death ran from the fire pose, B kept control after
  (HUD back, Crouch on R), no fire left on the boards (only ITEM 0x0101 pickups and the Warpf were new).
- D4-2 on the rope: not run. After B's armor broke the hook caught the tail once (KUI health [0,1]) but B never
  reached the hang (KUI 3, rope state 5), and 13 more tries timed out.

Pair 3 (A 52933, B 52976):
- D4-5: not run by input. Both Gohmas held at hp 1, two countdowns ("GO" on the card): real-input last hits land
  20 s or more apart (each waits on Gohma's lunge), outside the first death demo. GO 2: B killed first, and A's log
  `boss_kill 1: applied (m02E4 0x1, state 0x3, m02E0 1)` (mp-local.out 1650-1652): **D4-1 in reverse (the host
  receives) PASS**. A's death, heart (20 -> 24) and exit passed by input; B took its own heart and exit.

## Kalle Demos (s3)

Vine cutting by input does not outpace the regrowth (20 vines; best seen 8 left, with 5-vine Boomerang throws about 1 in 3
and a cycle of about 10 s). Stephen chose a recorded stand-in for the vine step only: Bmd +0x331 m331 = 0 and
+0x312 m312 = 3 (what a real cut sets, d_a_bmdhand.cpp:507), after which the game's own damage_check drops the flower
(mMode 3, m302 1..5). Every hit on the flower stays real input.

Pair 4 (A 8654, B 8685): A's Kalle killed by input (31 sword hits). B: not applied. A loop that kept re-dropping B's
flower cut short Kalle's grab sequence, whose camera event is only cleared at its end (d_a_bmd.cpp:391), so B's event
stayed running, B's IDLE gate stayed closed and the request was never written. The relay waits on IDLE silently:
bossSent drops ErrClosed without a line, so the log stops at "asking the REL" (a logging gap worth a fix).

Pair 5 (A 16485, B 16495): A's Kalle killed by input (28 hits). B died first (life refill off, Kalle reaches the
entrance), continued at kindan, and the held bit landed on leaving (`set t04 FW boss defeated`, 0x28): KD-6's path again,
not KD-2. No save written (No on the prompt).

Pair 6 (A 75334, B 75344), B's life refill on throughout, B idle at the entrance, one flower drop:
- **KD-2 PASS** (mp-local.out 512-519): `holding t04 FW boss defeated back`, `asking the REL ... once its flower is down
  and open`, `boss_kill 1: waiting (mMode 0x2, m302 1, m304 0)`, one drop at 00:57:40, `boss_kill 3: waiting (mMode 0x3,
  m302 1, m304 0)`, `boss_kill 4: applied (mMode 0x3, m302 2, m304 1)` at 00:57:42. Then by input on B: the death demo
  (mMode 0xB), Makar (0x0150) drops and talks, the heart container taken (max life 0x15 -> 0x19, +4 once), the Warpf
  (0x006A) at the body: B landed in Omori (point 214, layer 9). B's FW dungeonItem 0x00 held in kinBOSS, 0x38 after.

Pair 7: void. B's card (6-StartFW + `wwfixture di+=4:5`, FW STAGE_BOSS_DEMO) was "ahead" of the world founded from A's
file, so B never bound (NOT_BOUND) and no co-op ran on B.

Pair 8 (A 34535, B 34613), mp-local `--coop=new --coop-merge` (the merge also gave A the demo bit, line 171):
- **KD-3 PASS**: B bound and armed (lines 152, 156), FW dungeonItem 0x20 before the hop; `kinBOSS 1 0` played no
  intro (Kalle fight mode 0 at once, never 0xA). After A's kill (01:21:20, 26 hits): `boss_kill 1: waiting (mMode 0x2,
  m302 1, m304 0)`, one flower drop at 01:21:45, `boss_kill 3: waiting (mMode 0x3, m302 1, m304 0)`, `boss_kill 4:
  applied (mMode 0x3, m302 2, m304 1)` at 01:21:47. Then by input: the death demo, Makar drops and talks with no intro
  seen (the inferred m2DC path holds), the heart (0x15 -> 0x19), the Warpf to Omori (point 214, layer 9) and the Deku
  Tree scene. FW dungeonItem 0x20 in kinBOSS, 0x38 after.
- KD-5: not run by input (same timing problem as D4-5).

## FW r5 Kanat floor (s4, pair 8)

- POKE (as wind#21): A's sw28 (Kokiie fallen), A in kindan room 5. B in room 5 with the room in camera logged
  `set t04 FW switch 0x1C (0x803C501C, and its live copy |= 0x10)`; within 12 s B's Kokiie (0x0064) fell from y 6920 to
  y 1380 and B's Kanat (0x00A5, the solid vine floor over B1) was gone. B then walked to the Kanat spot and dropped
  through it to y 1150 (the floor stood at y 3877), landing in the water beside the fallen Kokiie: **visual PASS**.
