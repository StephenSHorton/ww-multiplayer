# Enemy mirror: the rows never built (ST, OSHIP, boss parts) and S1-S7

Research and design only, no code (wind#8, 2026-11-01, base main c1278b3). It replaces the `deferred` and `still_open` entries of the handoff's `design/enemy-mirror-allow.json` for these classes, and the "Enemy rows never built" line of `design/coop-remaining.md` §4.

Sources: the zeldaret/tww decomp (citations `d_a_*.cpp:N` are its `src/d/actor/`), this repo, the handoff design and archive notes, the USA disc (`res/Object/*.arc`, every `room.dzr`), and the fixture memory cards. Claims marked "offline" were read from the disc or the saves on the Mac rig; nothing here was run live.

## Verdict and ranking

| Rank | Row | Verdict | Stored REL cost | Playthrough impact |
|---|---|---|---|---|
| 1 | **ST Stalfos 0xBF**, kind 9 | **Build, intact phase only.** `st` keyed and `st+u` opt-in, off by default, BOSS refused (keeps the three M_DaiMB minibosses out) | ~1.2-1.9 KB | 9 main-path fights (Earth Temple 4, Wind Temple 3, both keyed; GT Earth trial 2 unkeyed), plus 7 in Savage Labyrinth |
| 2 | **OSHIP warship 0xB4**, kind 10 | **Build, `oship+u` only, no hit relay** (damage already crosses through fxsync), own-delete death. Keep it out of `DefaultPolicy`'s ALLOW until fxsync carries the warships' own cannonballs | ~1.1-2.0 KB (+0.15-0.3 KB for the cannonball fx kind) | Sea crossings together: 7 ships always, 19 from Hyrule2 on, 9 one-shot, 5 per save in the two Triforce rooms. Without it each player's ships sail independently, which is playable |
| - | **BWDS Molgera larva 0xDB** | **Never.** A host larva can't match a client one (home.pos is the tongue's, per run), and a free copy jams the client Molgera's larva count, spawn cap and BGM | - | none lost: the `molgera` boss_kill relay ends the partner's fight |
| - | **BMDHAND / BMDFOOT Kalle Demos tentacles 0xED / 0xEE** | **Never.** Refused today as `not-keyed` (room -1). Mirroring would freeze the client's vines, which drive its own flower fall, so its fight could not finish | - | none lost: the `kalle` relay |
| - | **BGN2 / BGN3 Puppet Ganon 2-3** | **Never as mirror rows** (they are the boss). Puppet Ganon has **no boss_kill row**: a partner's win does not end the receiver's fight. That is a relay-design gap, filed as a separate item | - | - |

ST ranks above OSHIP: it is on the main path in two dungeons and GT, while the ships are optional fights whose damage already crosses through fxsync.

## Budget: measure before building

`internal/inject/rels.RELSArcMaxGrowth` is 0x10000 stored bytes. The patcher golden on c1278b3 (`cmd/ww-multiplayer/testdata/cli-golden/patch-ok.txt:5`, REL 0x1CE30685) reads **+58528**, which leaves **7008 bytes, or about 4960 after the 0x800 reserve**. The last retail-disc reading was +58400 (docs/TESTING.md:708, 64ff61b), so about 5088. Batch D's 27 KB budget is long gone.

ST plus OSHIP plus the cannonball fx kind is about 2.4-4.2 KB, so both fit only at the low end. Build them as **two REL builds, ST first**. Measure the retail growth line before each, and defer OSHIP if under ~2.5 KB is left after ST. Both rows need `EMK_KIND_COUNT` to stop depending on `EMK_WITH_MT`: with ST at 9, an MT cut leaves a hole at 8 (`wwenemy_kinds.h:49-50`, `emk_check_mt_count`). KindHash changes with each row, so peers on different builds refuse each other's enemy streams, as before.

## Follow-up build card

One card under wind#1: "Build ST Stalfos mirror row (kind 9, intact phase)". Steps: re-measure growth on the retail disc → live Q1/Q2/Q4 (below) → REL + Go row → offline checks (TestBlobFresh, kind asserts, KindHash re-pin) → live ET r19 / kaze r10 → PR. OSHIP is its second phase: P1/P2 first, and the fxsync enemy-cannonball kind before turning it on by default.

## S1-S7: the allow design's open questions

The allow design (`ww-multiplayer-handoff/design/enemy-mirror-allow.json`, `still_open`) left seven questions. Six are settled here from source, the disc and the fixture saves (offline, 2026-11-01, main c1278b3). Two still need a live run.

| # | Question | Answer | How |
|---|---|---|---|
| S1 | OSHIP identity: keyed or unkeyed? | **Closed: unkeyed.** d_stage.cpp:816 is only the `OBJNAME("Oship", fpcNm_OSHIP_e, 255, 56)` name-table entry, not a keyed reader. All 55 `Oship` placements on the disc have setID 0xFFFF, so the row is `oship+u` only (see the OSHIP section). A live `enemy-watch` at `sea 0 9` can still confirm it. | Offline scan of every room.dzr |
| S2 | Is a PLAIN kind's `actor.scale` always 1 after adoption (D16)? | **Yes for KI, AM, BB, MT; no for AM2.** KI changes scale only in its death shrink (`m2DC`, d_a_ki.cpp:962-967), which is already DYING. AM and MT never write `actor.scale`. BB's `scale` writes are particle locals (d_a_bb.cpp:1541-1542, :2123-2125). AM2's `action_modoru_move` (after it falls into an abyss) shrinks it to < 0.1, moves it to its spawn point and grows it back (d_a_am2.cpp:1038-1085, modes 40-41). A copy stays full size and slides to the spawn point. | Decomp grep of every `scale` write in d_a_ki/am/am2/bb/mt.cpp |
| S3 | Does `play__14mDoExt_baseAnmFv` on a paused copy's btk/brk advance right? | **Still open.** R-10 shipped behind `HKD_EXTRA`. The Fire Keese flame btk and the AM2 brk were never watched live (docs/TESTING.md:165 "Flame btk: untested"). | Needs live: `M_NewD2 0 7` (two Fire Keese) with `ki` on both sides; watch the copy's flame against the host's for 10 s, then repeat with `HKD_EXTRA` set |
| S4 | Does retail `d_a_bb.rel` .text match the decomp (`tail_control` 0x404, `daBb_Execute` 0x69A8)? | **Closed.** Batch C took a multi-word fingerprint from the disc REL offline (`enemy-mirror-batch-c.json` `judge_errors_resolved`, design 1). Live, #166 read `fp_miss` 0 at `M_NewD2 6 3` (docs/TESTING.md:372). | Records |
| S5 | AM DEAD bck 0x1A length | **Closed: 40 frames.** `bck/dead.bck` in the disc's `Am.arc` has an ANK1 duration of 40, play once. `anm_init` plays it at speed 1.0 (d_a_am.cpp:911, :1006). That matches the A0 live read (`mFrameMax` 0x28) already in `EMK_AM_QUIET_FRAMES 40` (inject/include/wwenemy_kinds.h:470). | Read from the disc (ISO -> RELS-free `res/Object/Am.arc` -> BCK header) |
| S6 | Re-verify "16 Keese placement collisions refused by the duplicate rule" | **Confirmed, and they are all one place.** Every Keese placement (`keeth`, `Fkeeth`) on the disc: 209, 41 keyed and 168 unkeyed. Grouped per (stage, room, layer) by `enemysync.PKeyFor`, there are 8 duplicate groups, all pairs, all unkeyed (`setID 0xFFFF`, params `0xFFFF0101`), all in `sea` room 44 (Outset): 6 on layer 6 and 2 on layer 7, 16 actors. Each pair is two entries at the same position, which no key can split. Keyed Keese have no collisions under D9 (pos zeroed, setID unique per room). The duplicate rule refuses both actors of each pair under `ki+u`. Each game keeps its own Keese there, which is the right result. | Offline scan of every stage's room.dzr with wwapi/stage + `enemysync.PKeyFor` |
| S7 | Are the fixture saves' sea switch 0x2A, Triforce charts and COLORS_IN_HYRULE set? | **Set on both rig cards.** Slot 0 of `dolphin-user-3` and `dolphin-user-4` (both "Abahbob") reads sea memory switch 0x2A on, Triforce charts 0xFF, shards 0xFF, COLORS_IN_HYRULE (event 0x3802) on, the same as CryZe `28-Hyrule3`. So every switch-0x2A warship and both Triforce-chart warship rooms spawn on the fixtures (unless salvaged; see OSHIP). In the CryZe chain, switch 0x2A and COLORS first appear at `10-Hyrule2`; Triforce charts first appear at `24-AfterPawprint` (0x8F). The earlier note that user-4 is `15-StartET` is stale: the fixtures on this Mac read like a 100% file. | Offline read of the GCIs with wwapi/savefile |

What still needs a live Dolphin: S3 (and an optional S1 confirmation).


## Enemy mirror row: ST Stalfos (proc 0xBF), design

Base: main c1278b3. Decomp: tww  (`d_a_st` is Matching, configure.py:1846).
Citations without a path are `src/d/actor/d_a_st.cpp` (`.cpp:`) or `include/d/actor/d_a_st.h` (`.h:`).
`St.h` means `assets/GZLJ01/res/Object/St.h` (generated from St.arc; the only copy in the tree, so the
GZLE01 indices are assumed to match: see Q7). "EM" means `inject/rel/enemy_mirror.c`.

### 0. The deferral note, checked

The note is in enemy-mirror-allow.json `.deferred["ST Stalfos 0xBF (batch C)"]`. Each claim checked against source:

| Claim | Verdict |
|---|---|
| MTX_PLAIN + yaw m0304 (.cpp:2591-2603) | **Right.** Base = T(cur) * Ry(shape.y + m0304), setBaseScale(scale) (.cpp:2592-2595). m0304 is driven during the KAITEN spin attacks (.cpp:894) and decays (.cpp:2607), so it must be streamed. **Missing from the note:** every placement has shape.x = 0x1F and shape.z = the death-switch byte (tables dump, §1). PLAIN's Rx(0x20C) * Rz(0x210) (EM:2177-2182) would roll the copy by up to 1.4°, so those angles must be fed 0. |
| "25 part models" | **Wrong.** `st_part_bdl[26]` has 0 at [0] KOSI and [18] HIMO (.cpp:2647-2674), so 24 models are created (:2721-2723). `st_part_draw` skips i == 0xB, the BUKI model (.cpp:371-374), so **23 are drawn**. The body morf model (`mpMorf`, ST.bdl) is never drawn: Draw draws only the parts (.cpp:415-438). |
| Mace pid 0x1DCC, joint 0xB | **Right.** `mHeldWeaponEntityId` (.h:103) is a BOKO actor (proc 0x1D5, f_pc_name.h:482), Type_STALFOS_MACE 2 (d_a_boko.h:30), created by daSt_Create (.cpp:2898). It is carried at m1DD0 == 10 (.cpp:2271-2273) and placed at anmMtx(BUKI) * Ry(16000) * T(0,0,65) (.cpp:2274-2282; the REG8 terms are 0 in retail). |
| The eye | **Right, but from a joint.** eyePos = the head part's position, which is anmMtx(HEAD 0xF)'s origin in the intact state. attention = eyePos + 30 y (state 0 sets mPartPos from anmMtx, .cpp:1896-1898; eye/attn .cpp:2318-2328). It is not cur + a constant. |
| 7-sphere collider scan with per-sphere codes | **Overkill.** In the intact actions (0, 5) m0ED0 is 0, so all 7 Tg spheres are on (cc_set .cpp:2370-2388). A body-sphere hit takes the same path for spheres 1..6 (.cpp:1636-1735). Two codes are enough: HEAD (sphere 0, R 45) and BODY (spheres 1..6, R 25) (.cpp:2397-2401). |
| Hide hair through mTimers[2] 0x2F0 (.cpp:433) | **Right**, and it is the only cheap option. `ke_control` is an undecompiled stub (.cpp:145-147), so the hair physics can't be ported. The only faithful alternative is a retail call behind a fingerprint (BB tail_control precedent). |
| Copy hidden in break/head/split/nunchaku | **Right.** The hide byte must be m02B9 (§6). |
| A client cannot land the kill (.cpp:1580-1584) | **Right**, and worse: see §5 and §6. |
| No nodeCallBack in retail (.cpp:394-412) | **Right** (`#if VERSION == VERSION_DEMO`). |

### 1. Identity and placements

- **Spawned vs placed.** Only placed Stalfos are mirrored. The one spawn is the split's upper half: `fopAcM_create(ST, 0xE, ...)`, behaviour 14, unkeyed (.cpp:1681). The gate refuses it on both peers.
- **home.pos is never written.** It is only read (.cpp:1475-1478, 2005, 2513). So ST uses the standard pkey (proc|setID|params|room|home.pos) and needs no PKEY_NO_POS.
- **Keyed rows** match by setID. **Unkeyed rows (`+u`)** match by pkey (enemy-mirror-general.json `.identity`).
- **No collisions.** No two ST placements share (stage, room, params, pos) (tables dump, checked).
- **Each game spawns its own mace.** Because the BOKO is not an ENEMY-group actor, it is never in the walk, so each mace is local.

Placements come from the coop-tables cache (the local coop-tables cache, `.enemies.enemies`, proc 191). Save tables are from coop-slice-overworld.md:68-71.

| Stage (room) | n | Key | Behaviour / switches | Mirror? |
|---|---|---|---|---|
| M_Dai r14 (Earth Temple) | 1 | set4 | beh 3 vertical coffin, ambush 0xEA, no death switch (slice-et:95, :394) | yes, `st` |
| M_Dai r19 | 3 | set0-2 | beh 3 coffin, ambush dan 0x93-95, death dan 151-153 | yes, `st` |
| M_DaiMB r12 (ET miniboss) | 3 | unkeyed | beh 1/2/2, death sw63-65, so **BOSS** (.cpp:2838-2841) | **no**, refused |
| kaze r0 (Wind Temple) | 1 | set1 | beh 1 underground, sight 0x46, death dan 137 | yes, `st` |
| kaze r10 | 2 | set1-2 | params 0xFFFFFFFF, so beh 15 = plain walker (no special case, .cpp:2860-2880) | yes, `st` |
| GanonE r0 (GT Earth trial) | 2 | unkeyed | beh 1, ambush sw4, an event bit (slice-gt:110); one entry on layer 11 | `st+u` |
| Cave10 r10/r18, Cave11 r14 (Savage Labyrinth, .cpp n/a; f_op_actor_mng.cpp:1238-1243) | 2+3+2 | unkeyed | beh 0 / beh 1 sight 0x78 / beh 1 sight 0x64 | `st+u` |
| TF_02 r5 (stage id only) | 2 | unkeyed | beh 1, dan death 136/137 (not BOSS) | `st+u` |
| PShip2 r0, PShip3 r0, SubD45 r0-2, SubD71 r1-2 (stage ids only) | 6+6+16+12 | unkeyed | beh 0/1/3, 32 of the 40 on layer 8 (ACT8) | `st+u`; reachability is Q6 |

**Playthrough impact.** The main path meets ST in three places:
- Earth Temple: 4 mirrorable, plus 3 BOSS that are out of scope.
- Wind Temple: 3 mirrorable.
- Ganon's Tower: 2 mirrorable, unkeyed.

That is **9 mirrorable fights on the main path, 7 of them keyed.** Savage Labyrinth adds 7, and the rest is side content. Stalfos is mid-frequency: rarer than BK, MO2, CC or KI, about TN's level.

### 2. Row fields (type_table format)

Kind id **9** (ids 0-8 are taken, kinds.go:37-45). Layout 4 already covers it: the kind_mask is u16, the hook-disable byte is block +0x2A (ids 7..14 at +0x28..+0x2F), and the knob-table row (ids 3..15) is present. **No layout change.**

| Field | Value |
|---|---|
| PROC | 0x00BF (f_pc_name.h:200) |
| CLASS_SIZE | 0x2368 (.h:150, retail layout: VERSION > DEMO fields present) |
| CAPS | 0x04 FOPEN only. Not MODEL_EQ: actor.model is unused. Not SPH_COLL: COLL is its own (below). Not TILT_CUR. |
| MTX | EMK_MTX_PLAIN 3, with yaw = cur_angle + aux[0] (m0304, raw, not smoothed) and Rx = Rz = 0. Built as an ST case in em_plain_matrix (EM:2177) or by streaming aux[1..2] = 0 into 0x20C/0x210 (the KI recipe, EM:1431-1437). setBaseScale(actor scale) as retail (.cpp:2591-2592), so the quiet shrink reaches the parts through anmMtx (Q3). |
| COLL | new EMK_COLL_ST 3: 7 Tg spheres `m054C[i]` at 0x54C + i*0x12C (.h:88), centre = anmMtx(cc_j_no[i]) origin (cc_j_no {0xF,2,1,4,9,0x15,0x18}, .cpp:2352), R 45 for i 0 and 25 for the others, OnTgSetBit, Ccsp Set (.cpp:2389-2405). The weapon At `m0D80` (0xD80, .h:89) is never Set on the copy. |
| ATTACH | new EMK_ATTACH_ST 3 (see §4) |
| EYE | inside ATTACH: eye = anmMtx(0xF) origin, attn = eye + (0,30,0); also written to mParts[15].mPartPos 0x1530 (.cpp:1896-1898, 2318-2328) |
| ARC | "St" (.cpp:125) |
| BCK_FIRST/LAST/WAIT | 0x04 ATTACKL1 / 0x35 YOKO_START / 0x33 WAIT (St.h:11-60). WAIT always, because m02CC is never written (read only at .cpp:445, 459, 553), so LWAIT 0x21 never plays. HEADB_* 0x0F-0x13 are in the range but play only on mpMorf2. |
| MORF | 0x2BC mpMorf (.h:56). mpMorf2 0x2C0 (headb) is never shown intact. |
| GATE | 0x2B4 u8 mBehaviorType (.h:49), with 14 refused. mActionState is an **s16** at 0x2C6 (.h:59) that em_st_gate reads itself (TN precedent: a u8 GATE read would take the high byte). |
| DR/DR_MODE/DR_ACTION/TILT | 0 (no damagereaction) |
| CO_CYL / TG_CYL | 0x678 (sphere 1, the em_hits BODY test target) / 0x678. The real Co is all 7 spheres (Set by COLL_ST). |
| HEAD_SPH / DEF_SPH | 0x54C (sphere 0) / 0 |
| HEAD_CENTER / DEF_CENTER | 0x260 eyePos (the host's own part_move writes it every frame, .cpp:2318-2321) / 0 |
| CO_FORWARD / TG_HIT_UP | 0 / a fixed belly height from Q2 (inferred around 100), set by the generic em_hit_center collider-2 path (EM:3133-3137). Not the live sphere-2 centre 0x8BC (= 0x7A4 + 0x118): see R3. |
| INVULN | 0x2F6 s16 m02F6 (.h:72). The gate at .cpp:1565 is set 5 per hit (:1579), 0xF at create (:2916), and 10 every frame in the ambush waits, fight2 and the break (:1044, 1098, 1154-1156, 945-1036), decremented at :2574-2576. |
| INVULN_WAIT | 5 (.cpp:1579) |
| ICE | 0x1FA8 (.h:145; classes.go:195): shrink 0x1FAE, freeze 0x1FB6 |
| SPH_SRC | 0x54C (head_sph_src; the only one whose Tg takes LIGHT_ARROW, .cpp:2737-2763 vs body_sph_src :2764-2792) |
| WEAPON/BARE/BLUR_ON/BLUR_SHAPE/BLUR_SCALE/ANM_GATE/PLAY_SKIP | 0 |
| ATTN_F2 | 0x08 (LOCKON_BATTLE from the host; ST toggles it per action, .cpp:509, 1048, 1070, 1102, 1133, 1213) |
| PKEY_KEYED_NO_POS | 0 |
| extra_offsets | m02B9 u8 0x2B9 draw/ambush gate (.h:54; Draw .cpp:420); mDeathSwitch u8 0x2B8 (.h:53); m02C4 s16 0x2C4; mTimers[5] s16 0x2EC (hair gate [2] = 0x2F0); m02F8 0x2F8; m02FA 0x2FA; m0304 s16 0x304; m0ED0..m0ED3 0xED0-0xED3 (m0ED2 = attached parts, 0x19 when whole); mParts 0xED4 + i*0x6C (st_p: model +0, mPartState s8 +4, m05 +5, mPartPos +8; .h:11-30); head part state 0x152C, pos 0x1530; m1DC8 nunchaku 0x1DC8; m1DD0 mace state 0x1DD0; mUpperBodyEntityId 0x1DD4 (0xFFF = none); m1DD8 head-detached 0x1DD8; m1DE4 hit shake 0x1DE4; m1E84 body HP s8 0x1E84 (16, .cpp:2902); mCountdownToDeath 0x1E85; mShadowId 0x2360 |

Count and naming:
- **EMK_KIND_COUNT becomes 10 unconditionally.** Today COUNT is pinned to EMK_WITH_MT: 9 with MT, 8 cut (wwenemy_kinds.h:49-50, `emk_check_mt_count`). With ST at 9, an MT cut leaves a hole at 8. So the cut comment, that assert and kinds.go:108-113 change, and the Go `Kinds` array keeps a zero row at index 8 if MT is ever cut.
- **Go names.** `Sample.St` is already the allow-kinds field (block.go:367-368). Name the Go file and functions `stalfos.go` / `ReadSampleStalfos`, `KindST`; the user token stays `st`.

go_row: `{ID KindST 9, Name "st", Proc 0xBF, Arc "St", BckFirst 0x04, BckLast 0x35, BckWait 0x33, MorfOff 0x2BC, IceOff 0x1FA8, GateOff 0x2B4, AngleOff 0x20E}`. DeathSwitchOff 0x2B8 (u8). ColliderOff {1: 0x54C, 2: 0x678}. KindHash re-pinned.

### 3. Sampler (Go, `ReadSampleStalfos`)

Five reads: readKindActor (pos, angles, health, attn, status), 0x2B4..0x30C (0x58 B), 0xED0..0xED4 plus the head part 0x1528..0x153C, 0x1DC8..0x1E88, ice 0x1FA8 + 0x10, and the morf anim.

- **AngleY** = shape.y 0x20E (AngleOff). **Aux** = {m0304, 0, 0, m1DE4}. The REL adds aux[0] raw after smoothing. A combined shape.y + m0304 in AngleY would cost no REL code, but at 5000/frame (|m0308| ≤ 1, .cpp:741-822) it aliases backwards after a ~6-frame sample gap. aux[3] is optional (the hit shake, .cpp:2255-2261) and is 0 unless that is built.
- **DYING** = mCountdownToDeath 0x1E85 != 0 || head part state 0x152C == 8 (.cpp:1592, :2195-2226) || m02FA != 0 || ice shrink 0x1FAE != 0. **Never health**: health is the detached head's HP and stays 16 while intact (.cpp:2900, 1583).
- **INVULN** = m02F6 != 0.
- **HIDDEN** = any of the following, together the "not intact" test:
  - m02B9 != 0 (ambush, not risen)
  - mActionState ∉ {0, 5, 0xF, 0x10, 0x1F}
  - m0ED2 != 0x19
  - m1DC8 != 0
  - m1DD8 != 0
  - mUpperBodyEntityId != 0xFFF
  - m1DD0 != 10

  Action 0x1F, head_damage, is intact: a head spin on a whole body, parts in state 0 (.cpp:1250-1302).
- **Flags2**: 0x01 HAS_WEAPON (m1DD0 == 10; must equal EMK_BK/TN_F2_HAS_WEAPON for em_quiet_drop's shared test, EM:2016), 0x08 ATTN (attn flags != 0), 0x40 LIGHT under shrink, 0x80 QUIET.
- **WFlagDormant** = action ∈ {0xF, 0x10} && m02C4 == 0 (the ambush waits before the trigger, .cpp:1045-1062, 1099-1116).
- **Refuse in StaticOK**: BOSS (death switch 1..0x7F; reuse classes.go deathSwitchBoss) and params & 0xF == 14.

### 4. REL code and cost

New code:
1. **Row and asserts.** EM_KINDS entry 0x44 B, asserts, plus ST in em_check_plain_rows and the attn pin list.
2. **em_st_gate** in em_validate. Refuses:
   - BOSS status (em_tn_gate's test)
   - behaviour 14
   - s16 action ∉ {0, 5, 0xF, 0x10}
   - m1DC8, m1DD8 or m0ED2 != 0x19
   - mUpperBodyEntityId != 0xFFF
   - mCountdownToDeath, m02FA
   - **m1DD0 != 10** (the mace is not carried yet. It is carried from the second execute at the earliest: the BOKO create is queued (.cpp:2898) and create's own Execute (:2917) finds none. This is the BK m1214 precedent, EM:1667.)
   - ice running (generic).

   As with TN, the gate runs on the copy's own frozen bytes, so it acts at adoption only.
3. **em_st_attach** after McaMorf_calc:
   - for i 0..25 with model && i != 0xB: `setBaseTRMtx(part, anmMtx(i))` (.cpp:2252-2268)
   - the head-joint eye and attn (above)
   - u16 mTimers[2] = 1 (hair hidden)
   - while not HIDDEN and HAS_WEAPON with the BOKO under CARRY (em_carried_boko): the mace matrix
   - while HIDDEN: the mace parked at T(20000,20000,20000), as retail does under m02B9 (.cpp:2279-2281; em_bk_ambush_park precedent EM:1051). The death case is in item 7.
   - m1DE4 zeroed at adopt.

   Parts draw through mDoExt_modelUpdateDL in the copy's own Draw, so no extra calc is needed.
4. **COLL_ST.** 7 SetC/SetR/OnTgSetBit/Set calls. em_hits gets an ST scan: sphere 0 → HEAD; any of 1..6 → BODY; ClrTgHit all 7 on withhold (MT-3 shape, smaller). Host side: nothing new; the generic collider-2 centre with the row's TG_HIT_UP (R3).
5. **em_kind_fields ST.** Copy m02B9 = 0 while the slot is shown, 1 while HIDDEN (the KI/BB draw-gate precedent R-9/R-15). m02B9 is the only safe hide byte: mCountdownToDeath and m02FA make the copy's own execute delete it after release (.cpp:2503-2516).
6. **em_handback ST.**
   - Writes: action 0, m02C4 1, mTimers[1] 0, mTimers[2] 0, m0304 0, m1DE4 0, m02B9 restored (0 if the copy was ever shown, else its own byte saved at adopt in an em_ext_slot byte)
   - the generic BCK_WAIT setAnm
   - **Without the m02B9 restore, a released copy stays invisible forever**: state 0 never re-reads it (jyunkai .cpp:505-573).
7. **Small cases.**
   - **em_quiet_drop ST.** Most client deaths start hidden (§5), so the mace is usually parked at 20000. The ST case therefore first writes the mace's base translation to the copy's cur pos, then cancels CARRY, so it falls where the Stalfos stood. Vanilla also lets go of it on every death (.cpp:1242, 2225; it lands at the hand in a break, :2023-2024). This was preferred over dropping it at the hidden edge and re-grabbing at the shown edge, which races with the client's Link picking the mace up (re-grab .cpp:2049-2051).
   - Also: em_hit_dying ST (the DYING terms above), em_hit_shrink_ok ST (0), the aux[0] yaw term.

**Cost (stored Yaz0 bytes).**

Calibration:
- Batch C: 2208 stored for about 2280 B of new .text (BB 1572 + knob 484 + ambush 224; archive/enemy-batch-c/IMPL-NOTES.md:60-68).
- MT: 3552 stored for 4096 B of .text (archive/enemy-batch-d/IMPL-NOTES.md:36).

So this kind of code stores at about 0.85-0.97 per .text byte, not the general 0.6.

| Unit | .text est. | stored |
|---|---|---|
| row, asserts, gate | 250-350 | 220-330 |
| attach (26-part loop, eye, hair, mace hand/park) | 450-700 | 400-650 |
| COLL_ST + hit scan | 350-550 | 320-520 |
| kind_fields, handback, quiet drop, hit_dying, yaw aux | 250-450 | 220-420 |
| **Total, intact-phase row** | 1300-2050 | **~1.2-1.9 KB** |
| Optional hair retail call (ke_move behind a multi-word fingerprint, BB R-14 shape) | | +0.6-0.9 KB |
| Optional head-stream phase (§6) | | +0.8-1.2 KB |

**Budget.** The newest recorded growth is **+58400** (docs/TESTING.md:708, main 64ff61b, 2026-10-27). That leaves 7136 B (0x1BE0) under 0x10000, or **about 5.1 KB after the 0x800 reserve**. The often-quoted 29 KB (batch D .budget) is stale. c1278b3 may be higher still: **re-measure before committing.** The intact row fits; the row plus both options (about 4 KB) does not leave a comfortable margin.

### 5. Quiet death

How the host dies (no BGM anywhere in d_a_st: the only audio call is bgmHitSound .cpp:1668, and the M_DaiMB trio is refused as BOSS):
- **Head kill.** The head is hit while detached → part 15 state 8 → it falls → IBALL disappear per part, death switch, onActor, cancelCarry (.cpp:2195-2226) → mCountdownToDeath 5 → delete (:2506-2512).
- **Reassembly timeout.** m02F8 runs out (500 frames) → IBALL, switch, onActor, cancelCarry, delete (.cpp:1227-1244).
- **Light arrow to the head sphere.** mLightShrinkTimer = 1, mace dropped (.cpp:1570-1577) → enemy_ice, which sets the death switch from mEnemyIce.mDeathSwitch (.cpp:2913).
- **Fall.** More than 4000 below home → delete with no drop (.cpp:2513-2515).

On the client:
- **A slot going DYING (or a vanish)** takes the existing quiet path for ids ≥ KI (EM:2960-2995, `k->id >= EMK_KIND_KI` at :2966). The copy stays paused, so none of the drop, switch or onActor sites run (they are all inside its Execute). em_die_quiet zeroes health and attn, shrinks it over 20 frames (90 with SF2_LIGHT), runs em_quiet_drop (the ST case lets the mace fall), then fopAcM_delete (EM:1959-2002).
- **The death switch** (dan 137/151-153, and TF_02's 136/137) reaches the client through the switch sync and Tier 2 refresh (slice-et:220, slice-wt:203), never from the copy.
- **Items.** Each game's IBALL is the host's only. Savage Labyrinth never drops IBALL at all (f_op_actor_mng.cpp:1238-1243).
- **K1 light-shrink** skips a paused copy (combat.Revalidate refuses the mirror pause bit, enemy-mirror-general-IMPL-NOTES "Fix made at integration").

The client must NOT:
- run daSt_Execute, its damage_check, or part 15 state 8
- create the split half (.cpp:1681)
- set mCountdownToDeath or m02FA (either self-deletes after handback)
- call `fopAcM_createStealItem`. A client grappling-hook steal (.cpp:1640-1644) is not reproduced, because a forwarded hit is not HIMO2. Accepted gap.

**What the client sees.** The head kill only ever happens in a hidden phase. So on the client the Stalfos **vanishes at the break and never visibly dies**: a hidden copy goes quietly. Only a light-arrow kill (SF2_LIGHT, 90 frames) and a death while whole are visible.

### 6. Phases, risks, recommendation

Phases (mActionState, .cpp:1481-1517):
- **Intact** (0 jyunkai, 5 fight with KAITEN spins, 0x1F head spin): mirrored.
- **Ambush** (0xF underground, 0x10 coffin): adopted hidden, shown on the host's rise. START/TATE/YOKO play from the stream, but without smoke or hair (mTimers[2] is set there anyway, .cpp:1128).
- **Out of scope, copy HIDDEN with the mace parked:**
  - 0x1E break: parts and head flying, head hops, reassembly .cpp:1147-1248, part states 1-8 .cpp:1880-2240
  - 0x1D fight2: arm pulled off as a nunchaku, .cpp:933-1038, nun_move
  - 0x21 split legs, with the spawned 0x20 upper half refused
  - light shrink, as a death.

  A head-stream phase would show only the head. It would stream head part pos 0x1530 and rot as the slot pos/angle in a new flags2 phase bit. The REL would set every part state except 15 to ≥ 10 (Draw skips them, .cpp:367), set m1DD8 = 2 so Draw shows mpMorf2 (.cpp:376-381), set headb base TR, and put sphere 0 on it (cc_set .cpp:2390-2391). Cost: +0.8-1.2 KB, plus the BCK table would need the HEADB rows on mpMorf2. That is a second morf the S2 animator does not have.

Risks:
- **R1, the client cannot finish.** Body hits forwarded as HEAD/BODY land on the host's own damage_check. Body damage counts only when the attacker is named PLAYER or the attack type is 2 (.cpp:1645-1650). Otherwise a body hit is a flinch with no damage (:1724-1735) and a head hit a harmless spin (:1708-1723).
  - **Default attribution: damage counts.** The one-off At's stts owner is host Link (`EM_PLAYER_INFO0`, proc PLAYER; EM:3212, 3236-3248), so .cpp:1646 passes and m1E84 drops.
  - **ATTRIB without NAME_A9: no damage.** The owner is the remote body (proc WWREL, renamed to PLAYER only by NAME_A9 inside a swapped execute, remote.c:1772-1777).
  - **Either way, the break is the most the client can do.** The finish (a head hit while detached) is host-only.
- **R2, split from the wrong Link.** Split needs mPlCutBit 0x80 (a spin or roll cut, d_cc_uty.cpp:15-37). That comes from `daPy_getPlayerActorClass()`'s cut type (d_cc_uty.cpp:16, 119), i.e. the **host** Link's at that instant (or a swapped one). A client body hit while the host is mid-spin can split the host's Stalfos, and a client spin never splits.
- **R3, the one-off At hits the wrong sphere.** damage_check takes sphere 0 first (.cpp:1567). An At centred near the head sphere becomes a head spin.
  - A live sphere centre is also unsafe while the host is in action 0x1F: head_damage sets m0ED0 = 1 (.cpp:1260), so cc_set parks spheres 1..6 at (-50000,-50000) R -500 (.cpp:2403-2406). A BODY forward then misses, a dropped swing for about 1 s.
  - **Prefer centring BODY on cur + (0, TG_HIT_UP, 0)** with a fixed belly height measured once in Q2, over a live sphere. That needs no ST case in em_hit_center: TG_HIT_UP takes the measured value.
- **R4, the coffin lid.** The coffin (MKanoke) is a separate local actor whose opening depends on each game's ambush switch (slice-et:57, :161). A copy shown on the host's rise may appear through a closed lid on the client. This matters in ET r14/r19.
- **R5, cosmetic staleness.** The paused copy keeps its adoption-time tevStr and ground shadow (.cpp:334-358 reads mAcch; :2616 tevStr), the AM/AM2 precedent. There is no hair, no particles (walk and START smoke, STASE), no hit shake unless aux[3] is built, and the client is immune to the copy's mace (At stripped: m0ED1 is cleared at the end of every cc_set, .cpp:2457-2458, so it can't be streamed reliably). Damage comes through the remote-Link path instead (policy `0xBF=allow+no_latch`, archive/enemy-audit table).
- **R6, budget.** About 5 KB of headroom is left (§4); ST takes a third of it.

**Recommendation: build the intact-phase row**, kind 9, off by default (`st` keyed, `st+u` opt-in), BOSS refused, no hair call, no head-stream.
- It covers the 7 keyed main-path fights (WT and ET) and GanonE at about 1.2-1.9 KB, with no layout change.
- It reuses three precedents (TN gate, BK park and quiet drop, MT-style sphere scan).
- What the client gets is honest: the Stalfos walking, swinging and spinning in sync, Z-targetable, taking body hits (given Q1), vanishing at the break and reappearing whole if the host's reassembles.

Defer instead if the re-measured headroom after the pending batches is under about 3 KB. Cut order inside the row: aux m1DE4, then the ATTN bit, then handback-to-wait (plain release instead).

### 7. Open questions (each needs a live probe)

| # | Question | Probe |
|---|---|---|
| Q1 | Confirm only (source says yes for the default, R1): does a forwarded client hit decrement m1E84? Also: is ATTRIB + NAME_A9 the same, and is ATTRIB alone zero? | kaze r10, client sword on the copy's chest; read host s8 0x1E84 (16 → 16-dmg) and m02F6 each frame, in all three modes. |
| Q2 | Which belly height for TG_HIT_UP registers on a body sphere and never sphere 0, across WAIT, WALK and the attack anims? | Same room. Read the host's sphere 2 centre (0x8BC) minus cur.y over a minute of play; then forward hits at that height and log the TgHit Rprm of all 7 spheres (0x54C + i*0x12C). Expect a body hit (m1DE4 = 8) and no head spin (action ≠ 0x1F). |
| Q3 | Does setBaseScale on mpMorf shrink the part models through anmMtx (the quiet shrink), and does the copy's shadow follow? | Kill the host Stalfos with a light arrow while the copy is whole. Watch the client copy over its 90 frames; read part 1's base TR scale column. |
| Q4 | Is the host's mace always carried (m1DD0 == 10) by the time a client adopts on room entry, or does the m1DD0 != 10 gate leave a window of refusals? | Enter kaze r10 on both; log the slot's last_err and the copy's 0x1DD0 for the first 10 frames. |
| Q5 | Is the sample cadence enough for a KAITEN spin (m0304 up to 5000/frame) with aux[0] raw, with no visible reverse spin under lag? | ET r19 with 100 ms of added latency; compare host and client yaw traces during 0x0C/0x11 (.cpp:741-822). |
| Q6 | Are the 32 layer-8 placements (PShip2/3, SubD45, SubD71) and TF_02 reachable in a retail playthrough? Which islands, which layer conditions? | Stage-layer probe: hop to each and dump `dComIfG_play_c` layer plus the actor walk for proc 0xBF. |
| Q7 | Do GZLE01 St.arc BCK indices match the GZLJ01-generated St.h? | **Closed offline:** the USA disc's St.arc has 0x04 attackl1 (39 frames), 0x0F headb_damage, 0x21 lwait, 0x2B start (89), 0x33 wait (40, loops), 0x35 yoko_start (139), the same indices as St.h. |
| Q8 | Does a hidden copy (m02B9 = 1) also drop its Co spheres? COLL_ST is skipped while HIDDEN (EM:3058-3060), but are stale Co records cleared so the client's Link is not blocked by an invisible Stalfos? | Break on host; client walks through the copy's spot; read sphere 1's Co ResultHit. |
| Q9 | M_DaiMB: does refusing BOSS leave the slice-et hazard (partner sw63 opening coffin #1 early) unchanged? | Already open as slice-et Q4/A6; no ST-row change. Confirm only. |

## Enemy mirror row: Warship (OSHIP, proc 0xB4)

Design only. Sources: tww decomp (`tww `), main c1278b3, handoff design/archive, the local placement cache (the local coop-tables cache, `enemies.enemies[]`), fixture GCIs (`the CryZe 100pct-E saves`). Line numbers in enemy_mirror.c are the c1278b3 ones; the deferral note's :697/:1102-1109/:1415/:1764-1776 are stale.

### 1. Identity (settles S1)

**Unkeyed. `oship+u` only, like `mt+u`.**

- The deferral's "keyed stage actor (d_stage.cpp:816)" misread the source. Line 816 is `OBJNAME("Oship", fpcNm_OSHIP_e, 255, 56)` in `l_objectName` (d_stage.cpp:437, macro :432-435). That table only maps a name to a proc, subtype and gba name. It carries no setID.
- setID comes from the placement entry itself. `dStage_actorInit` copies `actor_data->base.setID` as is (d_stage.cpp:1700). The u16 sits at ACTR entry +0x1E (`stage_actor_data_class` name[8] + `fopAcM_prmBase_class.setID` at +0x16, d_stage.h:193-196, f_op_actor_mng.h:38-43). On the actor it lands at +0x1BC (f_op_actor.h:278).
- Cache check (done here, not inferred): all 55 OSHIP placements are `sea` ACTR, layer -1 (no layer), `set_id` 65535. So "55/0/55" means 0 keyed. `dSv_info_c::isActor` treats 0xFFFF as no key (d_save.cpp:1686), and no zone dead-actor bit exists for these ships.
- pkey is stable. `getArg` zeroes only home.angle.x (d_a_oship.cpp:1000), which is not hashed. createInit copies pos into `mOrigPos` 0x2B8 (cpp:892, h:100), never into home. Nothing in d_a_oship.cpp writes home.pos or params, so no `PKEY_NO_POS`.
- Duplicate Idents: none possible. Each placement has its own pos, and no other code creates proc 0xB4.
- Scope: home room must equal mStayNo (identity.rel_side ERR_SETID). Both players must be in the same sea square, and crossing a square releases every copy (hands back alive).

**Where they are** (decoded from cache params: A = params>>24, B = home.angle.x & 0xFF, tri = (p>>8)&0xF, rsp = (p>>12)&0xF, model = home.angle.x>>8; getArg cpp:986-1003; spawn gates cpp:1014-1029):

| sea room | ships | gate |
|---|---|---|
| r3, r9 (x2) | 3 | none (A, B 0xFF), back on every load |
| r22, r24, r25, r46 | 1 each | none |
| r4, r7 (2), r12, r20, r28, r40, r43, r45, r49 (2) | 11 | B 0x2A |
| r8 (A E0/E1), r22 (E6), r24 (E8), r25 (E6/E7), r46 (E4/E5) | 8 | B 0x2A, plus a **zone** switch A (≥0xC0, d_save.cpp:1548-1558): it resets with the zone, so these ships return |
| r16 (A 13/14), r29 (A 15/16/17), r30 (A 3A, 4C-4E) | 9 | one-shot: memory switch A, set when sunk |
| r6 | 8 | tri 6 → `isTriforce(6)` (cpp:1023, d_com_inf_game.h:1220-1221: bit `tri`), i.e. chart 7; rsp 0-3 → 2 of the 8 spawn per save |
| r31 | 12 | tri 3 → chart 4; rsp 0-3 → 3 of the 12 spawn per save |

- **The r29 ship with A 0x17 has model 00. It is the special (gold) ship** (`isSpecial`, h:42; GOLD_SHIP_DELETE event cpp:711-737). The gate must refuse it. So `sea 0 29` mirrors 2 of its 3 ships, not 3.

**S7, read from the fixture GCIs here** (slot 0; packed layout `card_savedata` at GCI 0x2040, `gamedata[i]` at +8 + i*0x770, mMemory at +0x374, mMap at +0xBF; m_Do_MemCardRWmng.h:28-44, d_save.h:614-635, 922-932; checksum not verified):

- sea switch 0x2A is set from **10-Hyrule2** on. This is earlier than the route-order guess, and user-4's 15-StartET has it.
- Triforce chart byte (map +0x81): 0x00 through 23, **0x8F at 24-AfterPawprint** (bit 3 set: the r31 ships spawn; bit 6 clear), **0xFF at 28-Hyrule3** (the r6 ships spawn).
- One-shot switches: 0x17 (the r29 gold ship) is set from 21, 0x13 (r16) from 22. 0x14-0x16, 0x3A and 0x4C-0x4E are clear in all 28 saves.

### 2. Row fields (type_table format)

Kind id: the next free id, **9** (10 if the ST row lands first). Hook byte `WWENEMY_HKD_HI_OFF + (id-7)` (wwenemy.h:57-58), knob row id (< WWENEMY_KNOB_KINDS 16, :289). `EMK_KIND_COUNT` 10 breaks the `emk_check_mt_count` pin (wwenemy_kinds.h:1042-1043) and changes KindHash, so v4 peers on different builds refuse each other.

| field | value |
|---|---|
| PROC | 0x00B4 (f_pc_name.h:189) |
| CLASS_SIZE | 0xE00 (d_a_oship.h:151); retail pad u8[0x2AC-0x290] at 0x290 (h:88-90): no Bt fields, never write EMK_AC_BT_* |
| CAPS | 0 or a new `EMK_CAP_MODEL_ONLY`. No FOPEN, no DR, no MODEL_EQ (actor.model 0x24C is never set by the class), no SPH_COLL |
| MORF | 0x4B0 = `J3DModel* mpModel` (h:123), read as the model under `EMK_MTX_MODEL` (not a morf) |
| MTX | `EMK_MTX_MODEL` 4 (reserved, wwenemy_kinds.h:67). Recipe from setMtx (cpp:466-501): M = T(cur.pos) · `mDoMtx_XYZrotM(x,0,z)` (0x8000CB48, symbols.txt:128; it composes Rz·Ry·Rx) · Ry(shape.y) (cpp:481-483). Write M to model+0x24 (base TR = cullMtx, cpp:914) and to `mFlagMtx` 0xD20 (cpp:484). Write s16 aimY 0xD84 and aimX 0xD88 (h:144,146) **before** calc: the class's own `nodeControl_CB` reads them for joints HEAD 1 and CANON 2 (cpp:96-125; Oship.h:21-24). Then call `calc__8J3DModelFv` 0x802EE8C0 (symbols.txt:12386) instead of McaMorf_calc. Leave base scale as is (cpp:480) |
| COLL | new `EMK_COLL_OSHIP`: each frame, 5 `dCcD_Cyl` at 0x4F0 + j·0x130 (h:125). Centre cur.pos + (sin,cos)(angle.y)·{50,-50,0,180,-180}, y −30; R {170,170,290,200,200}; H {500,500,190,190,190}; then `Set` (setCollision cpp:405-436). Bearing = shape.y: em_apply writes cur_angle to both current and shape y (enemy_mirror.c:3012-3013), so no aux[2] is needed. Tg on (only so the client's own ball explodes on the hull), no hit relay (§6) |
| HEAD_SPH / TG_CYL / DEF_SPH / CO_CYL | 0 / 0 / 0 / 0. With both 0, em_hits never reports (enemy_mirror.c:2553-2554), which is option B in §6 |
| ATTACH | 0 |
| EYE | folded into MTX_MODEL (a new eye recipe), after calc: attention 0x274 = M·(0,900,0), eyePos 0x260 = M·(0,300,0) (HIO cpp:62-63, setMtx :489-493); mWavePos 0x468 = M·(0,0,380), mWaveRot.y 0x476 = shape.y, mTrackPos 0x480 = M·(0,0,0) (:495-500, h:114-118); mSmokePos 0xD08 = anmMtx[HEAD] translation (:840-841). Majuu flag: pid u32 0xD50 → if proc 0x00AF (f_pc_name.h:184) write flag+0xA98 = a+0xD20 and flag+0xA9C = a+0xD14 (d_a_majuu_flag.h:77-78; cpp:860-866) |
| ARC | "Oship" (cpp:18). Only BDL 3 (VBTSP) and 4 (VBTST) (Oship.h:11-12), no BCK |
| BCK_FIRST/LAST/WAIT | none: slot Bck 0xFF, attr 0xFF |
| MORF (anim) | none: step 4 (em_animate / McaMorf_play, enemy_mirror.c:3023-3030) is skipped for the row |
| GATE | 0x2B6 u8 `mModelType` (h:98): adopt only 0xFF. The REL gate also refuses s32 0x2AC `mCurrentProc` == 3 (h:91; mode_tbl cpp:765-774), matching remote.c:1583-1584. Do **not** point a u8 gate at 0x2AC: that is the high byte of an int and always 0 (use 0x2AF for the low byte) |
| DR / DR_MODE / DR_ACTION / TILT | 0. Tilt is shape.x 0x20C and shape.z 0x210 from the slot, raw. The host's values already include dLib_waveRot and the attack/damage sway (cpp:467-478): never call waveRot on the client |
| INVULN | 0xD72: the low s16 of int `mHitTimer` 0xD70 (h:138; set 5 at cpp:378). Wire flag only, because there is no relay (§6) |
| INVULN_WAIT | 0 |
| ICE | 0. Already guarded at c1278b3: adopt :1825, em_die :1938, :2966. em_hit_dying reads `k->ice + 6` unguarded for other rows (:3404-3419), and OSHIP needs no case there under option B |
| SPH_SRC | 0 |
| CO_FORWARD / TG_HIT_UP / PLAY_SKIP / WEAPON / BARE / BLUR_* / ANM_GATE | 0 |
| ATTN_F2 | 0x01: host attention flags != 0. Execute sets LOCKON_BATTLE every frame (cpp:788), and em_apply writes flags only under EM_XF_ATTN (enemy_mirror.c:1562, 3054) |
| PKEY_KEYED_NO_POS | 0 |
| extra_offsets | mSubMode 0x2B0, mSwitchA 0x2B1, mPathId 0x2B3 (h:92-96); mOrigPos 0x2B8; m2D4 0x2D4 (path index); mStts 0x4B4; mFlagOffset 0xD14; mBombPcId 0xD54[5]; mVelocityFwdTarget 0xD68; mAttackTimer 0xD74; mPlFireTimer 0xD7C; mAttackSwayAmount 0xD82; mBombAlloc 0xD8C[5] (h:92-148) |

**Handback** (em_handback :1140; generic pause/speed/old writes :1146-1150): mCurrentProc 0x2AC = 0 (WAIT re-runs changeModeByRange, which re-arms the timers; cpp:504-516), mPlFireTimer = -1 (cpp:897), mHitTimer = 0, mAttackSwayAmount = 0, mOrigPos = cur.pos (pathMove chases it, cpp:169-171), health 0x285 = the host's last sample (otherwise a ship with 1 HP comes back at 3; max 3, cpp:894), and ClrTgHit on all five cylinders (the client's own ball hit the copy's Tg). Skip the morf/anm block (`morf` read at :1143).

**Generic sites that assume a morf** and need a MODEL branch: em_validate :1784-1792 (model = `*(a+0x4B0)`, not `morf->model`); em_apply step 4 :3023-3030 (skip) and step 5 :3038-3043 (calc via J3DModel); em_base_matrix :2197-2215 (new case, dst = model+0x24); em_adopt anim seeding; em_quiet_drop :2004-2009 (already returns, since attach 0).

### 3. Sampler (Go side)

`ReadSampleOSHIP`, 3 Reads, no morf, no colliders:
- R1 the actor (`readKindActor`): pos, shape 0x20C..0x211, current.angle, health 0x285, attention flags.
- R2 0x2AC..0x2B7: mCurrentProc, mSubMode, A/B, path, tri, rsp, mModelType.
- R3 0xD70..0xD8B: mHitTimer..mAimRotXTarget.

Fields:
- `AngleOff` 0x20E (shape.y). Tilt = shape.x, TiltZ = shape.z, sent in slot +0x18 (`Slot.SetCcM316Z`, the BB precedent, bb.go:96-108).
- Aux = {aimY 0xD84, aimX 0xD88}.
- Mode = int32 0x2AC; Action = mSubMode.

Flags:
- DYING = mCurrentProc == 3 || health ≤ 0.
- INVULN = s16 0xD72 != 0.
- HIDDEN never.
- Flags2 0x01 ATTN.
- Dormant never.

Go row:

```
{ID KindOSHIP 9, Name "oship", Proc 0xB4, Arc "Oship", NoAnim true (BckFirst 0, BckLast -1),
 MorfOff 0x4B0 (model), IceOff 0, GateOff 0x2B6, AngleOff 0x20E}
```

- ParamsOK: true. LiveGate: model == 0xFF && proc != 3.
- `Kind` grows a `NoAnim` flag (kinds.go:50-79). `NewArcRes` (restable.go:192-193), `NewKindTracker` (:316-323), Derive (:326) and the host's per-kind `Resolve` loop (host.go:276-291) skip it. Otherwise Resolve errors, and the host tick returns an error for every enabled no-BCK kind.
- `ColliderOff` row `{0,0,0}` (kinds.go:960-982).
- `DeathSwitchOff` 0 (own-delete sets switch A; :996-1010).
- `KindHookNames`: none needed at first.
- `ReadKindSample` case (block.go:760).
- `SeedPolicy` already lists ProcOship (players.go:258). `DefaultPolicy` would ALLOW it once the kind is on (players.go:286-293). See §6.

### 4. REL cost (stored Yaz0 bytes)

Calibration:
- Batch C (BB row + tail call + ambush) cost 2208.
- The old hand-built MT row cost 3680; batch D's MT cost 3552 (TESTING.md:493).
- Bdk: +332 .text → 192 stored, about 0.58 (TESTING.md:594).

| item | raw | stored |
|---|---|---|
| row + asserts | ~0x60 data | 50-80 |
| MTX_MODEL (XYZrotM + YrotM + 2 copies + J3DModel calc) | 150-200 | 90-130 |
| eye/wave/smoke/flag step (5 multVec, SearchByID, proc check) | 280-380 | 170-230 |
| COLL_OSHIP (3×5 table + loop, SetC/R/H, Set) | 260-340 | 160-210 |
| model-only guards (validate, step 4/5 skip, adopt, handback morf) | 200-320 | 120-190 |
| gate (model, proc 3) | 50-70 | 30-45 |
| handback case (7 writes + 5 ClrTgHit) | 140-200 | 85-120 |
| own-delete death (die / die_quiet / release branches) | 160-260 | 95-155 |

- **Option B (recommended, no hit relay): low 1100, high 2000.**
- Option A (relay through em_hits) adds a 5-way RPrm OR with per-cylinder withhold, an em_hit_dying/vulnerable case (:3364-3419) and an **At-group change**: the host's v2 sphere is Set|VsEnemy only (enemy_mirror.c:480, 3260), which never meets the warship's IsOther Tg (cpp:28; `ChkNoHitAtTg` c_cc_s.cpp:105). That is +500..+900, giving **1800-3000**.

**Headroom is the binding constraint.**
- The latest recorded growth is +58400 (TESTING.md:708, 2026-10-27, main 64ff61b), leaving 7136 under the 0x10000 cap (rels.go:35), or **5088 after the 0x800 reserve**. Batch D's 27424 is long gone.
- Option B takes 22-39% of what's left; option A takes 35-59%.
- c1278b3 (v0.6.1, colors/tunic since) has not been measured here. Re-measure first.
- The enemy-cannonball fxsync extension (§6) is a further ~150-300.

### 5. Quiet death

What the host does at 0 HP:
- At 0 HP (bomb hits only, cpp:349-356, 380-383): `modeDeleteInit` plays the sink SE, ends the 3 smoke callbacks and calls `fopAcM_createDisappear(…, 5, IBALL)` (cpp:699-707). That spawns DISAPPEAR with itemTableIdx "Oship" (cpp:890; f_op_actor_mng.cpp:1297-1307), so the host drops items.
- `modeDelete` (non-special) then pitches to −15000 and sinks 1000 below the water.
- When nearly down, if A != 0xFF it calls `onSwitch(A, room)` and `salvage->onSalvageForOship(this)` (cpp:744-752). After that it deletes the MAJUU_FLAG child and itself (:754-758).
- No onActor (unkeyed anyway).

Why the generic quiet death is wrong here:
- **(a)** It deletes the ship but leaves the flag. The flag's `mpParentMtx`/`mpParentPos` (0xA98/0xA9C) then point into freed memory, and they are read every execute and draw (majuu_flag.cpp:72-74, 778-781).
- **(b)** No switch A and no salvage call on the client.
- **(c)** It shrinks the hull via scale, which does not match a sinking ship.

**Own-delete (recommended).** On the slot's DYING edge (host 0x2AC == 3), and also on a quiet-dying release:
1. Run the OSHIP handback.
2. Write s32 0x2AC = 3 and s8 health 0x285 = 0.
3. Unpause, latch DEAD, and do **not** call `fopAcM_delete`.

From there the copy's own execute runs only `modeDelete` (`modeProc(EXEC)` :781-783). It sinks with its own code, sets A, calls `onSalvageForOship`, deletes the flag, then deletes itself. The em_validate gate (proc 3) keeps the sinking copy from being re-adopted.

What the client must not run:
- `modeDeleteInit`: the IBALL disappear would double the drop. Writing mCurrentProc directly instead of `modeProcInit(3)` skips it.
- `em_die`'s shrink/delete, or a Go DeathSwitch write (DeathSwitchOff 0).
- The gold ship's GOLD_SHIP_DELETE: never adopted (gate).

Salvage:
- `onSalvageForOship` (d_a_salvage.cpp:472-495) moves the room salvage point whose switch == A (and whose ocean bit is clear) to the ship's XZ and makes it visible. It sets no save bit.
- Without it, once the synced switch lands, `proc_wait` makes the kind-2 point usable at its **placed** position (d_a_salvage.cpp:288-290; registration d_salvage.cpp:95-97), while the host's sits where the ship sank. That is a visible mismatch, and own-delete fixes it.
- Double salvage is not a new class. Pulling it up sets the ocean bit (end_salvage :503-516). Co-op syncs that bit as CatOcean, and the salvage catch-up marks a stale registered point unusable (mods/coop/replica/salvage.go header). The window where both players crane the same point before the bit lands is that file's existing accepted race.
- `fopAcM_SearchByName(Salvage)` has no NULL check (cpp:750-751). That is the same exposure the host already has.

Switch A:
- The client setting it itself is idempotent with co-op's latch (switches/actors.go:738, "Latching… set when sunk").
- For the zone-switch ships (E0-E8) it is the only way the client's zone sees it.

### 6. Risks and recommendation

- **Hit path: use fxsync, not em_hits (option B).**
  - fxsync copies clear only VsEnemy|VsPlayer (fx.c:470), so a bomb or cannonball copy keeps VsOther. The warship's Tg is Set|IsOther (cpp:28), and `ChkNoHitAtTg` passes on AtGrp & TgGrp (c_cc_s.cpp:105) with At type BOMB (d_a_bomb3.inc:1362).
  - So **today** a client's cannonball, replicated to the host, already damages the host's real warship (on by default since v0.6.1, fxsync.go:27).
  - A mirror relay on top would count each hit twice. And the relay cannot land anyway without a per-kind At group (§4).
  - Option B keeps the copy's Tg on, so the client's ball visibly explodes on the hull, and never reports.
  - Cost: with `WW_PROJECTILES=off` the client cannot hurt mirrored ships.
  - Probe P2 must confirm.
- **Enemy cannonballs are invisible to the client.**
  - fxsync tracks only the local Link's ship ball: exact params 0x80020004 plus the SHOOT_CANNON flag (fx.c:336-377). The warship's ball is `prm_make(STATE_4,true,true)` = 0x80030004 (cpp:444; d_a_bomb_static.cpp:90-92).
  - With the kind on, `DefaultPolicy` ALLOWs proc 0xB4 (players.go:286-293). `SeedPolicy` already does (:258). Host ships may then target the client's body through the remote swap, and the client takes PHIT damage from unseen balls.
  - Fix (separate item): a host-only WWFX kind emitted from a mirrored ship's new `mBombPcId[j]` (0xD54, alloc 0xD8C) with the no-gravity timer. Copies stay world-only, so damage stays on the PHIT relay.
  - Otherwise keep OSHIP out of DefaultPolicy's ALLOW.
- **Budget:** §4. This row plus the cannonball item could use about half of the remaining headroom.
- **Cosmetic:**
  - Wake emitters keep their creation speeds (no setWave on the copy, cpp:258-320). Re-pointing mWavePos/mTrackPos keeps them attached.
  - Hit smoke and crit flashes appear on the host only (cpp:358-377).
- **Square crossings:** each one releases the copies, so ships near borders flip between mirrored and local. Handback re-arms the attack timers, which is safe.
- **Recommendation: build (option B), sized after re-measuring headroom.**
  - Order: re-measure → P1/P2 → row → cannonball fx item (or drop OSHIP from DefaultPolicy until then).
  - Cut order: below Gohdan. If headroom is under ~2500 after measuring, defer.
  - Playthrough impact: the sea crossings with the partner beside you (r3/r9 near Windfall and the Mother & Child Isles; 11+8 switch-0x2A ships from Hyrule2 on), two Triforce rooms late, and 4 one-shot rooms where a desynced sink would leave the salvage light misplaced.
  - Without the row: each player fights their own ships. Cannon damage already crosses through fxsync, so both games' ships take both players' hits, but they sail and aim independently. That is playable.

### 7. Open questions and probes

- **P1 (S1, layout):** `stage-hop sea 0 9`, then `wwdev actors` → the two proc 0xB4 pids. For each, `wwdev peek`: u16 +0x1BC (expect 0xFFFF), s8 +0x1E2 (expect 9), f32×3 +0x1D0 at t and t+10 s (expect equal while +0x1F8 moves), u32 +0xB0 (expect 0xFF00FF02 / 0xFF01FF02: A 0xFF, path 0/1, rsp/tri 0xF, subMode 2), u8 +0x2B6 (0xFF). `enemy-watch --kind` does not accept oship until the row exists (enemy_probe.go:47).
- **P2 (fxsync damage, decides option B):** two instances, `WW_PROJECTILES` default, both in `sea 0 9`. The client fires the boat cannon at a ship; on the host, peek s8 +0x285 before and after, and u32 +0x4F0+0x20+… (cyl Tg hit obj). Expect health to drop 3→2 on the host from the client's shot. If it does not, option A plus a VsOther At group is required.
- **P3 (gold ship):** fixture ≤20 in `sea 0 29`: confirm the A 0x17 ship reads +0x2B6 == 0x00 and is refused.
- **P4 (flag lifetime):** after own-delete completes on the client, confirm the MAJUU_FLAG pid (+0xD50) no longer resolves, and that no fault occurs over 600 frames.
- **P5 (salvage point):** a one-shot ship in r16 (fixture ≤21): host sinks it, the client sees the light at the sink XZ. Read the `dSalvage_control_c` entry for switch 0x13/0x14.
- **P6 (budget):** build c1278b3 as is and record the patcher's RELS.arc growth line before any OSHIP work.
- **Open:** does co-op sync zone switches (≥0xC0) at all? It matters only for the E0-E8 ships if own-delete is cut.

## Enemy mirror: boss-spawned rows (BWDS, BMDHAND, BMDFOOT; BGN2/BGN3 note)

Read-only research, 2026-11-01. Decomp `tww `; mirror `` main c1278b3.
It replaces the deferral line "Molgera larva, Kalle Demos tentacle, Puppet Ganon 2-3: Spawned identity; no reader facts this round" (`enemy-mirror-allow.json` .deferred).

**Verdict: no row for any of them. Mark BWDS, BMDHAND and BMDFOOT as never mirrored.** The `boss_kill` relay covers Molgera and Kalle Demos. Puppet Ganon 2-3 are the boss itself, and the gap there belongs to the relay design, not the mirror.

### 0. Framing: the mirror in boss arenas

- **No boss-stage gate exists.** The only stage rule in enemysync is `StageOK`. It refuses unkeyed Keese on `GanonK` (`mods/enemysync/identity.go:137-149`). Host and client scope by (stage, room) only (`host.go:346-353`, `client.go:182-184`). So the mirror would run in `kinBOSS`, `kazeB` or `GanonK` for any enabled kind placed there. Today these actors stay out of the mirror only because they have no kind (default `mo2,bk+u,cc+u,ki+u`, `kinds.go:248`).
- **Each game fights its own boss.** Both players in an arena share one scope, but each game runs its own boss. The partner's kill reaches the receiver as `boss_kill` on the receiver's own boss (`mods/coop/replica/bosskill.go:143-148`: gohma, kalle, gohdan, molgera, jalhalla, helmaroc; docs/08 item 85). The boss bodies are never mirrored: BWD, BMD and BGN are DENY in the remote-body seed (`players.go:269-272`) and have no mirror kind.
- **Mirroring the parts without the body cannot work.** All three spawned classes take their pose, count or phase from their own game's boss, found by proc name through `fpcM_Search`. Each boss in turn counts or reads them. A copy of the host's parts on the client would either cut a client part from the client's boss (if it adopted a local actor) or feed into the client's boss (if it were created free). Section 3 of each class shows how.
- **Not to be confused with the remote-body seed.** `SeedPolicy` ALLOWs BWDS, BMDFOOT, BGN2 and BGN3, and leaves BMDHAND out (`players.go:258-262`). That is the remote-Link body policy (which procs may react to the partner's body), not mirror kinds. These actors are not half-mirrored.
- **The free-copy precedent is already refused.** `GanonK`'s unkeyed-Keese refusal (D10) exists because Puppet Ganon's swarms are spawned children and BGN2 counts Keese by proc (`ki_check`, `d_a_bgn2.cpp:809-821`, used at `:1044-1048`). The same reasoning applies to every class below.

---

### A. Molgera larva, BWDS proc 0xDB (`d_a_bwds.cpp`, Matching, configure.py:1660)

#### A1. Identity
- **Spawn.** Molgera creates them in the sand-dive step: `fopAcM_create(fpcNm_BWDS_e, 0x23, &local_2c, roomNo)` (`d_a_bwd.cpp:605-612`). Up to `l_HIO.m24` = 3 per dive (frames 0x30-0x33; HIO defaults `:67-69`), and only while `ko_count < l_HIO.m26` = 5 (`:608`). `createAppend` gives setID 0xFFFF, parent_id ERROR (no parent pointer) and room = Molgera's room (`f_op_actor_mng.cpp:78-91, 94-104, 154-155`).
- **Position.** home.pos is the spawn position: tongue segment 0's x/z (`mTongueSegments[0].m04`), with y = REG0_F(9) - 600 + `l_HIO.m28` (800) = 200 (`d_a_bwd.cpp:609-611`; home set at `f_op_actor.cpp:271`). So home.pos depends on the run, and the unkeyed pkey (proc, 0xFFFF, params, room, home xyz; `enemy-mirror-general.json` .identity.pkey) differs between the two games. **A host larva can never match a client actor.** The client's larvae are spawned by the client's own Molgera at its own tongue position.
- **Placed larvae.** The stage table has a `Bwds` OBJNAME (`d_stage.cpp:1192`). The boss-spawned marker is params 0x23, which create maps to `m02F8 = -10` (`d_a_bwds.cpp:1033, 1058-1060`). The WT inventory has no placed `Bwds` (open question Q3).
- **Coupling to the boss, both ways:**
  - Molgera counts every BWDS by proc name every frame (`ko_s_sub`, `d_a_bwd.cpp:112-125`; reset and search `:1910-1914`). That count feeds the spawn cap (`:608`) and the music: `ko_count == 0` with `m1710 == 1` restarts the battle BGM (`:1918-1925`).
  - Molgera's death sets `m04F4 = 1` on every BWDS (`ko_delete_sub`, `:128-135`, called `:1035`). Each larva then deletes itself in `damage_check` (`d_a_bwds.cpp:544-546`).
- **A free client copy, created with no local counterpart (no contract path exists today; `enemy-mirror-general-CONTRACT.md` matches only):**
  - The client's own Molgera would count it. With up to 5 host copies, `ko_count` reaches `m26` and **the client's own larva spawns stop**.
  - The client's dive-phase music would never return to `JA_BGM_RANE_BATTLE`, because `ko_count` stays above 0.
  - `ko_delete_sub` would tear the copy down on the client boss's death, not the host's.
- **An adopted copy is impossible:** there is no pkey match (above).

#### A2. Row fields
None: no row is recommended. For the record, a row would need:
- `CLASS_SIZE` 0x18CC (`d_a_bwds.h:72`); MORF `mpMorf` 0x2B8 (h:16);
- a body chain of 14 models `mp02BC[0xE]` 0x2BC with segment pos/angle arrays 0x32C/0x3E0/0x43C [0xF] (h:17, 31-33);
- 15 spheres `m0670[0xF]` 0x670 (TG on [0] cc_sph, [3..14] defence, `d_a_bwds.cpp:505-543`) plus the hook sphere 0x544;
- action `m02F6` 0x2F6 (0 ug_move, 1 hook_on, 2 hook_chance, 5 fail; `:551-566`), the hit timer `m031E`, the die flag `m04F4`, HOOK_CARRY status (`:506-516`).

That is MT-shaped (a segment chain, many models and spheres), not KI-shaped.

#### A3. Sampler fields
None. A row would need roughly 5 reads: actor, 0x2F4..0x504, the segment arrays 0x32C..0x4F0 (0x1C4 B), the sphere centres and morf anim.

#### A4. REL cost (stored Yaz0)
Hypothetical only. Low 3200 / high 4800: the MT row was 3680 for an 8-segment ring and 9 spheres, and BWDS has 15 segments, 14 models and a hook-carry state. It would also need the 7th-row layout gate (u16 kind_mask, the annex relocation; `.deferred` "Layout gate").

#### A5. Quiet death; what the client must not run
Not applicable with no row. If ever built:
- the copy must not run `fail()` (heart drop via `fopAcM_createDisappear`, `d_a_bwds.cpp:479-481`), `hook_on` (`setHookshotCarryOffset` on the client's Link, `:509-511`) or `damage_check`'s self-delete (`:544-546`);
- it must be invisible to the client's `ko_s_sub`. That cannot be done without patching d_a_bwd.rel, because the count goes by proc name.

#### A6. Recommendation: **NEVER.** The `boss_kill` relay covers this.
- **Gameplay effect:** none lost. Each player already fights the larvae their own Molgera spawns. When the partner wins, the relay's apply runs the receiver's death sequence, and "the larvae vanish" (`docs/TESTING.md:565` MB-2; `relay-molgera.md:220-227, 266`). That is the only moment where the two games' larvae matter.
- **What mirroring would add:** host larvae on the client with no host Molgera behind them (BWD is never mirrored), double the larvae on screen, the client's own spawns and music jammed by `ko_count`, and hits on a copy whose boss does not exist on the client.

#### A7. Open questions (live probe)
- **Q1.** Does a `kazeB` session today show any cross-game effect from the mirror? Expected none: no enabled kind is placed in kazeB r0. Check `enemy host: 0 enemies ... kazeB room 0` on both logs.
- **Q2.** Not needed unless someone revisits: the larva count on a real fight (cap 5 under default HIO).
- **Q3.** Is any non-0x23 `Bwds` placed on the disc? The OBJNAME exists, but the WT inventory (`coop-slice-wt.md` S0 `ann.txt`) should confirm none. A placed one would be keyed and could pair, but nothing suggests one exists.

---

### B. Kalle Demos tentacles: BMDHAND 0xED (ceiling vines) and BMDFOOT 0xEE (floor roots)

#### B1. Identity
- **Spawn.** `daBmd_Create` makes 20 hands with params i = 0..19 and 8 feet with params i = 0..7 through `fopAcM_CreateAppend` + `fopAcM_Create(proc, NULL, params)` (`d_a_bmd.cpp:1991-2000`). That gives setID 0xFFFF, parent_id ERROR, base.position (0,0,0) and **room_no -1** (`f_op_actor_mng.cpp:78-91`). Create copies room_no into home.roomNo with no fixup (`f_op_actor.cpp:280`), so home.roomNo = -1.
- **Classification.** The general identity requires home room 0..63, and the host walk requires home room == mStayNo (`enemy-mirror-general.json` .identity.ident and host_side). So **both classes are refused as `not-keyed`** (`identity.go:127`) before any kind check. If home.roomNo -1 were relaxed, the pkey (proc, 0xFFFF, i, -1, 0,0,0) would be deterministic and identical in both games, so a host tentacle *would* pair with the client tentacle of the same index. Identity is not the blocker; behaviour is.
- **Coupling to the local boss.** Neither class has a parent pid. Each finds its boss by proc name (`s_a_d_sub`/`fpcM_Search`, `d_a_bmdhand.cpp:659-671`; `d_a_bmdfoot.cpp:589-599`).
  - **Pose comes from the boss's skeleton every frame.**
    - Hand: `current.pos` comes from `boss->mpBodyMorf` joint `boss_joint_d[param & 0x1F]` plus the x offset `boss_joint_xad[param & 3]`, and yaw from `boss->shape_angle.y` (`d_a_bmdhand.cpp:470-479`).
    - Foot: pos = boss pos + an offset rotated by the boss's shape angles, with yaw (param & 0xF) << 13 (`d_a_bmdfoot.cpp:544-554`).
  - **The tentacles write the boss's fight state.**
    - A hand latching to the ceiling does `boss->m331++` (`d_a_bmdhand.cpp:364-367`). A cut hand does `boss->m331--` and sets `boss->m312 = 3` (`:498-507`). A regrow does `m331 + 1` (`:578-581`).
    - The flower falls when `m331 == 0 && m312 == 1` (`d_a_bmd.cpp:916`), and `m331` scales the bulb's height (`:965, 1089-1090`).
    - A foot's attack sets `boss->m334 = 10` (`d_a_bmdfoot.cpp:336`).
  - **The phase comes from the boss.** `boss->m332` drives both classes' states (hand `:481, 554-596`; foot `:149-162, 493, 515, 567, 579`).
- **No death.** Health is 2 at create (`d_a_bmdhand.cpp:806`, `d_a_bmdfoot.cpp:798`) and is never tested. A cut hand regrows after `l_HIO.m0C` = 1000 frames (`d_a_bmdhand.cpp:509, 532-537`). Both lie down on the boss's own death (KD-2, `docs/TESTING.md:269`).

#### B2. Row fields
None: no row is recommended. For the record: BMDHAND `CLASS_SIZE` 0x828, MORF 0x2B4, states `m2BA` 0x2BA / `m2BC` 0x2BC, 20-node chain `m324[20]` drawn as `mLineMat` 0x554, spheres 0x5CC/0x6F8 (`d_a_bmdhand.h:14-47`). BMDFOOT `CLASS_SIZE` 0xBD4, two morfs 0x2B4/0xAF4, 18-point chain 0x2CC, spheres `mSph[5]` 0x50C (`d_a_bmdfoot.h:8-48`; NonMatching, configure.py:1651).

#### B3. Sampler fields
None.

#### B4. REL cost
Hypothetical: low 2400 / high 4000 per class (a 3D-line chain plus a joint-follow pose), so about 5000-8000 for the pair. Both would also need the layout gate.

#### B5. Quiet death; what the client must not run
No death exists. The decisive "must not" is the adopt itself. A paired copy has its execute paused and is driven from host samples, so on the client it would:
- **(a)** leave its own Kalle Demos's skeleton and sit where the host's boss is;
- **(b)** stop doing `m331++`/`--` on the client's boss, so client vine cuts would no longer bring the client's flower down (`d_a_bmd.cpp:916`) and the client's fight could not progress. The host's cuts would also never reach the client's `m331`;
- **(c)** ignore the client's `m332` phases (the intro, the eat, the death lie-down).

#### B6. Recommendation: **NEVER.** The `boss_kill` relay (`kalle`) covers this.
- **Gameplay effect:** none lost. Each player cuts their own vines. When the partner wins, `bk_bmd` writes the lethal block into the receiver's own Bmd once its flower is down and open (`docs/TESTING.md:261`, KD-1/KD-2 `:268-269`). The receiver's hands and feet then lie down by themselves.
- **What mirroring would add:** a softlock risk in the client's own fight (point (b) above) for purely cosmetic gain.
- **The table rows can stay.** Keep `classes.go:283-287` as `Kind: Boss, BossAlways`. The BOSS profile status (`d_a_bmdhand.cpp:842`, `d_a_bmdfoot.cpp:839`) already keeps them out of the K1 gates (docs/08 item 36).

#### B7. Open questions (live probe)
- **Q4.** Confirm home.roomNo == -1 on live tentacles: `peek <hand>+0x1E2 1` in kinBOSS reads 0xFF. If a later stage fixup sets it to 0, the refusal moves from `not-keyed` to "no kind". The recommendation stands either way.
- **Q5.** With both players in kinBOSS r0, check that neither log lists a BMDHAND/BMDFOOT in a mirror walk, and that a `coop-tables` or probe classification reports `not-keyed` for all 28.

---

### C. Puppet Ganon phases 2-3 (BGN2 0xF6, BGN3 0xF7)

When phase 1 ends, BGN creates both at its own position with params 0 (`d_a_bgn.cpp:3667-3674`): setID 0xFFFF, room = BGN's room, home.pos = BGN's current pos. That pos is the same each run only if the puppet ends at the same point, so the pkey is unreliable across games. Both carry BOSS profile status (`d_a_bgn2.cpp:1274`, `d_a_bgn3.cpp:1354`). They are the boss, not its parts: BGN2 searches for `bgn` and `bgn3` and writes `bgn3`'s position (`d_a_bgn2.cpp:594, 858-862`). Each spawns its own minions: BGN2 spawns Keese gated by `ki_check`, which counts every KI by proc (`:809-821, 1044-1048`); BGN3 spawns KS (`d_a_bgn3.cpp:1125`). So they fall under the same rule as the Molgera and Kalle Demos bodies: never mirror a boss body, and kill it by relay.

Unlike Molgera and Kalle Demos, **Puppet Ganon has no `boss_kill` row** (`bosskill.go:143-148` lists six bosses, none in GanonK). "Covered by relay" does not hold here. Today each player beats their own Puppet Ganon, and a partner's win does not end the receiver's fight. That is a relay-design gap (a seventh row keyed on GanonK and the GT table, with a phase-aware gate across BGN, BGN2 and BGN3), not a mirror row. Recommendation: **NEVER as mirror rows; file it as a relay candidate** next to `coop-slice-gt.md`.

---

### Summary table

| Class | Proc | Identity outcome | Why not | Recommendation |
|---|---|---|---|---|
| BWDS larva | 0xDB | unkeyed, run-dependent home.pos, so it never pairs | a free copy jams the client's `ko_count` spawn cap and BGM (`d_a_bwd.cpp:118, 608, 1918`) | never; covered by `molgera` relay |
| BMDHAND | 0xED | home room -1, refused `not-keyed`; would pair by index if relaxed | pose from the local boss's joints; writes `boss->m331`/`m312` (flower fall) | never; covered by `kalle` relay |
| BMDFOOT | 0xEE | same, params 0..7 | pose from the local boss; phase from `boss->m332`; writes `m334` | never; covered by `kalle` relay |
| BGN2/BGN3 | 0xF6/0xF7 | unkeyed, home = BGN pos | they are the boss | never as rows; Puppet Ganon relay row is a gap |

**Suggested `.deferred` replacement text:**

> "Molgera larva, Kalle Demos tentacles: never. Boss-spawned parts whose pose, count or phase come from this game's own boss (d_a_bwd.cpp:112-125, 608, 1918; d_a_bmdhand.cpp:364-367, 470-507; d_a_bmdfoot.cpp:544-554). Covered by the molgera/kalle boss_kill rows. Puppet Ganon 2-3: never as mirror rows (they are the boss); a GanonK boss_kill row is the open item."
