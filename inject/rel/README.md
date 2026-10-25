# The REL module (inject/rel)

Mod code shipped as a REL, loaded by the game's own dynamic linker,
instead of as T2 bytes in the DOL (T2 has 0x258 bytes left below the
mailbox). Plan: `ww-multiplayer-handoff/design/rel-module-plan.md`.
`rel_main.c` is the module (the profile, the actor, the prolog/epilog);
`enemy_mirror.c` is the Moblin mirror of the host-authoritative enemy
prototype (below), run from the actor's execute; `remote.c` is
remote-Link (below): the lite bodies, the pointer swap T2's exec shim
calls into, the host hit ring and the client apply; `fullbody.c` and
`shanm.c` are its stage-4 full body, a real `daPy_lk_c` behind
`WWREMOTE_FLAG_FULL_BODIES` (below, "The second profile").

## What it is

`rel_main.c` is an actor module. Its profile, `g_profile_WWREL`
(`.rodata`, 0x30 bytes, the decomp's `actor_process_profile_definition`),
describes proc 0xB5 with a five-method table (create, delete, execute,
is_delete, draw) and an actor of `WWREMOTE_ACTOR_SIZE` (0x600) bytes: the
keeper role (params 0) is `fopAc_ac_c` (0x290 in GZLE01) plus one u32 of
its own, the Moblin mirror's state (0x1C4, to +0x458) and remote.c's swap
state (from +0x458); the lite-body role (params `0x52000000 | slot << 8 |
gen`, remote.c) overlays the same size:

- `_prolog` runs the module's constructors (none) and the linker's
  `ModuleProlog`, then writes the mailbox slack words (below), the exec
  hook word last.
- create allocates nothing and registers no model or collision, so the
  actor is invisible and untouchable; it zeroes the mirror's state and
  returns `cPhs_COMPLEATE_e`.
- execute bumps the slack frame counter once per frame, then runs the
  Moblin mirror's per-frame loop (`enemy_mirror_frame`).
- delete releases every mirrored Moblin; draw and is_delete return TRUE.
- `_epilog` clears the exec hook word first, then the swap state word,
  releases too if delete never ran, clears the magic word, then
  `ModuleEpilog` and the destructors.

Game functions are called through absolute function-pointer casts, as
`wwapi.h` does, so those calls need no relocations. The only game
symbols the ELF references by address are the two method tables the
profile points at, `g_fpcLf_Method` and `g_fopAc_Method`, defined in
`ww_rel.ld` (addresses from the decomp's `config/GZLE01/symbols.txt`).

## The proc hijack

Proc 0xB5 is `PROC_RECTANGLE`, an unused dummy actor whose module,
`d_a_rectangle.rel` (module ID 0x58), lives in `RELS.arc`. The profile
list `g_fpcPfLst_ProfileList[]` is itself a REL in that archive,
`f_pc_profile_lst.rel`, and its slot 0xB5 is a relocation against module
0x58. The patcher (Go, `wwapi/rel`) converts `RELELF` to a REL, writes it
into `RELS.arc` as module 0x58 under the same file name (the loader opens
RELs by name), and edits that one relocation to point at
`g_profile_WWREL`. `mProcName` in the profile must stay 0xB5:
`scripts/extract_blob.py` checks the word in the ELF.

The game loads the module when the first actor of proc 0xB5 is created
(`fopAcM_create(0xB5, ...)` from T2, or `cDyl_Link(0xB5)` to keep it
resident) and unlinks it when the last one is deleted. In general that is
every stage transition and nothing in the module's `.bss` survives it;
live, the frame-hook actor keeps it linked across stage hops (it lands in
a layer the stage load keeps, "Who creates the actor" below), and the
respawn path in `relload.c` is the fallback. The mirror keeps all of its
state in the actor either way.

## The mailbox slack words

`Mailbox` (`inject/include/mailbox_core.h`) is 0x1B0 bytes at
`MAILBOX_ADDR` 0x80412F00; `__OSArenaLo` is 0x80414000, so 0xF50 bytes
past the struct belong to nobody. The module uses the first six words.
The struct and its size are unchanged; the offsets are `#define`s in
`rel_main.c`, read by `extract_blob.py` into `blob.go`.

| Offset from MAILBOX_ADDR | Address    | Word                | Writer                    |
|--------------------------|------------|---------------------|---------------------------|
| +0x1B0 `WWREL_MAGIC_OFF` | 0x804130B0 | `'WWRL'` 0x5757524C | `_prolog` sets, `_epilog` clears |
| +0x1B4 `WWREL_BUILD_ID_OFF` | 0x804130B4 | build id          | `_prolog`                 |
| +0x1B8 `WWREL_FRAME_OFF` | 0x804130B8 | frame counter       | `_prolog` zeroes, execute +1 |
| +0x1BC `WWREL_EXEC_HOOK_OFF` | 0x804130BC | `&wwrel_exec_hook` | `_prolog` sets last, `_epilog` clears first; T2's exec shim branches to it |
| +0x1C0 `WWREL_SWAP_STATE_OFF` | 0x804130C0 | keeper + 0x458 (remote.c's swap state) | keeper create sets, keeper delete and `_epilog` clear |
| +0x1C4 `WWREL_SHANM_OFF` | 0x804130C4 | the full bodies' shanm table (archive heap, `'WWSA'`) | T2's `main01_init` zeroes it at boot; the first full create sets it; never cleared after (not by `_epilog`), never freed |

The build id is a CRC-32 of `inject/rel`'s sources (`build.py`
`rel_build_id`, passed as `-DWWREL_BUILD_ID`), also exported as
`g_wwrel_build_id` in `.rodata` so `extract_blob.py` can read it from the
ELF and emit `RELBuildID`. A source hash rather than a timestamp keeps
the ELF byte-identical across rebuilds of the same source, which
`blob.stamp` relies on.

Go reads them as `inject.RELMagic`, `RELMagicOff`, `RELBuildIDOff`,
`RELFrameOff`, `RELBuildID`: magic present and build id equal means the
patched module is linked; the frame counter moving means its actor runs.

## enemy_mirror.c: the Moblin mirror

The REL side of the host-authoritative enemy prototype (handoff
`design/host-authoritative-prototype.md`, `moblin-mirror-design.json`
"rel_side", and for S2 and v2 `moblin-s2-v2-final.txt`). Go samples the
host's keyed Moblins (`d_a_mo2`, proc 0x00BD) and writes one 0x124-byte
block per tick into the mailbox slack at `MAILBOX_ADDR + 0x200`
(`inject/include/wwenemy.h`, layout 2: magic `'WWEN'`, seq_head/seq_tail,
epoch, stage, room, role, flags, four 0x34-byte slots named by pid with
the S2 animation bytes, the host's `hit_apply`); the module adopts those
actors and writes its status into the block's tail (+0x124..+0x198). The
state (`enemy_mirror_state`, `enemy_mirror.h`, 0x1C4 bytes) is part of the
module actor, so it dies with the actor and nothing survives an unlink in
`.bss`; the only exceptions are the host's two static collision objects
in the slack past the block (below), rebuilt lazily after a relink.

Per frame of the module actor (list 7: after Link's execute, before the
draw phase and the collision pass):

0. **Intake.** The block is live when its magic, layout (`WWENEMY_LAYOUT`)
   and `WWENEMY_FLAG_MIRROR` are there, else everything is released. An
   epoch change forgets every slot (release, dying latches dropped). A
   block is consumed when `seq_head == seq_tail` and differs from the
   last consumed seq (a torn Go write keeps the previous targets for one
   frame); the REL then writes `ack_seq` and `ack_frame` (the WWREL frame
   counter). A slot whose position is NaN or beyond +/-1e6 keeps its
   previous target and reports err 6. No new seq for 90 frames releases
   everything once (`watchdog_releases`++) until a new seq arrives.
1. **Scope.** The game's stage name (0x803C9D3C) and `mStayNo`
   (0x803F6A78) must equal the block's, and `NEXT_STAGE_ENABLE` must be 0,
   else everything is released before the stage teardown deletes the actors.
2. **Resolve and revalidate, every frame.** `fopAcM_SearchByID(pid)`;
   then proc 0x00BD at +0x08, initState 2 at +0x0C, a keyed setID (< 0x200
   at +0x1BC) whose `home.roomNo` (+0x1E2) is the block's room, mo2 `mMode`
   (+0x2BC) neither 5 (carried) nor 0xF (statue): mode 0 and the mode 1
   lantern/jail variant (a normal Moblin once any pearl is held; M_Dai
   room 1's keyed three are modes 1, 1, 0) are mirrored, `mpMorf`
   (+0x2C4) non-null and its model (+0x50) equal to the actor's (+0x24C).
   A failure releases the slot, reports `last_err` and counts
   `revalidate_failures`. The first good frame adopts: `pause_prev` = the
   pause byte at proc+0x0B, bit 1 set in it (a direct byte OR: for a leaf
   process `fpcPause_Enable` is exactly `mPauseFlag |= flag`, f_pc_pause.cpp
   :22-32, and its node recursion never applies to an actor), so
   `fpcEx_Execute` skips `daMo2_Execute`; the smoothed position and angle
   start at the actor's own, `speed` and `speedF` are zeroed, the first
   write snaps. Every later frame re-asserts bit 1, because
   `fopScnPause_Disable` clears it on every leaf of the scene layer at an
   overlap wipe (f_op_scene_pause.cpp:20-38 via fpcPause_Disable's
   recursion); a frame that found it clear counts in `pause_reasserts`.
3. **Smoothing.** `WWENEMY_FLAG_RAW`, a slot snap flag or the adopt snap
   take the target as is; else a quarter of the remaining distance per
   frame, snapping past 200 units; angle and tilt along the shortest arc.
   Writes: `current.pos` (+0x1F8), `old.pos` (+0x1E4, since the skipped
   `fopAc_Execute` is what normally copies it), `current.angle.y` (+0x206),
   `shape_angle.y` (+0x20E), the tilt `mDamageReaction.m48C.x` (+0xAA0, the
   x tilt `dr_matrix_set` rotates by; the block carries one tilt word, so
   `m48C.z` at +0xAA4 is left to the actor), and `mBtNowFrame` (+0x2A4) =
   100.0f, the value `daMo2_Execute` writes when there is no parry opening.
4. **Animation.** Without `WWENEMY_FLAG_ANIM` (S1):
   `mDoExt_McaMorf::play(NULL, 0, 0)` so the morf chases and the frame
   advances by its rate, with no footstep sounds (play skips the bas sound
   when its pos is NULL), and the blur mesh word `m0594` (+0x594) forced
   to 0. With the flag (S2), the stream replaces what the paused execute
   would have done to the morf. Each slot carries the host's bck resource
   index (Go resolves `mpAnm` against the Mo2 archive's resource table;
   0x3E..0x75, 0xFF unresolved), the J3DFrameCtrl attribute, an `anm_seq`
   byte Go bumps whenever the host restarted its animation (`setAnm`), the
   host's blend (`morf`), a second flag byte (`SF2_HAS_WEAPON` from
   `mbHasInnateWeapon` +0xDD4, `SF2_PLAY_SKIP` from `m05B2`, `SF2_BLUR_ON`
   from `m0594`, `SF2_ANM_GATE` from `m05B0`), the blur shape and scale,
   and the sampled frame and rate with the host sample seq. Per frame:
   - **select:** on an index, attribute or `anm_seq` change the bck comes
     from `dRes_getRes_byIdx("Mo2", idx, MOBJECT_INFO, 64)` (a fresh
     lookup every change, never cached) and
     `mDoExt_McaMorf::setAnm(bck, attr, morf, rate, 0, -1, NULL)` is called
     with the morf's sound pointer (+0x80) NULLed around it, so neither
     `initActorAnimSound` nor `stop` runs on the copy. An out-of-range
     index or attribute, an unresolved index or a NULL/bad resource
     pointer leaves the slot in `anm_state` 4, 2 or 3 (status +0x0B) and
     counts in `anm_unresolved`, `anm_range` or `anm_res_null`; a failed
     index is retried after 60 frames; such a slot degrades to S1.
   - **rate:** the streamed rate is written to the frame ctrl (+0x64)
     when it differs (a parry's `setPlaySpeed(-1)`, the stop at a
     once-animation's end).
   - **shapes:** `mbHasInnateWeapon` (the spear shape), `m2943` (bare
     handed, from `SF_BARE_HANDED`), `m0594`/`m0598`/`m0590` (the blur
     mesh) every frame.
   - **play:** `play(NULL, 0, 0)` unless the host skipped play (`m05B2`),
     so the blend advances as on the host.
   - **frame:** the sample's frame plus its rate per REL frame since the
     sample was consumed (at most 3), wrapped once for a loop (with
     `J3DFrameCtrl::update`'s own guards) or clamped to
     [start, end - 0.001] otherwise, stored as f32 at +0x68 after play
     (`McaMorf::setFrame` would truncate to s16). Only while the applied
     bck is the streamed one.
   The status entry carries `anm_state` (+0x0B), the bck applied (+0x0E)
   and a saturating `setAnm` count (+0x0F). Not mirrored: the ragdoll
   joint bend (`damagereaction` m088, applied in the joint callback) and
   the eye BTP; a knocked-back copy plays the streamed PAOMUKE/PUTSUBUSE
   with the streamed tilt.
5. **Base matrix.** `dr_matrix_set(&mDamageReaction)` (actor +0x614; it
   reads `current.pos`, `shape_angle` and `m48C`) then `PSMTXCopy(*calc_mtx,
   model + 0x24)`: the model's base matrix, which `fopAcM_SetMtx` made the
   cull matrix at create; then `McaMorf::calc`, so the joint callbacks
   refresh the head centre and the eye position.
6. **Colliders (S3, `WWENEMY_FLAG_COLLIDERS`).** What the end of
   `daMo2_Execute` does: the Co cylinder 35 units ahead along the angle
   (`cM3dGCyl::SetC` + `cCcS::Set` on `dComIfG_Ccsp()` = 0x803C72AC), the Tg
   cylinder at the position and the head sphere at the head joint (not
   while the slot is invulnerable, defending or ragdoll), and the shield
   sphere in defence; nothing while hidden or dying; never the weapon
   spheres (the mirror cannot hurt the client's Link); never `SetMass`.
   **Hits:** before re-submitting, a Tg hit bit (`(u32 at obj+0x1C) & 1`)
   on the head sphere or the Tg cylinder becomes the slot's hit entry
   `{hit_seq++, collider (1 head, 2 tg), pending 1}`; the other collider's
   hit is cleared (the same swing), and the hit collider is kept out of
   the collision system while `pending` is set, so `cCcS::ClrTgHitInf`
   (which walks only the objects submitted that frame) leaves its hit
   object (obj+0x20) and centre (obj+0x118) for Go to read. When Go clears
   `pending` the REL calls `dCcD_GObjInf::ClrTgHit` on it and re-submits;
   at most one entry per 8 frames per slot. Health is never touched.
7. **Release.** Bit 1 cleared (the leaf case of `fpcPause_Disable`; the
   other bits stay as they are now), `speed` and `speedF` zero, `old` =
   `current`, `mDamageReaction.mAction` (+0x61A) = 0 and `mMode` (+0x618) =
   -10: JYUNKAI's 60-frame wait with `speedF` forced to 0
   (d_a_mo2.cpp:1304-1313). A withheld Tg hit is cleared first. After S2
   the copy also gets its own wait loop back (`setAnm` of KWAIT/WAIT/NBWAIT
   by its weapon byte and variant, `wait_set`'s rows, blend 5, sound
   pointer NULLed), its `mbHasInnateWeapon` from adopt time, `m0594` = 0
   and its `m05B0`/`m05B2` timers cleared (they never decremented while
   paused, so `anm_init` would otherwise be a no-op for up to 25 frames).
   Release happens on pid 0, the slot's release flag, a revalidation
   failure, the actor vanishing, scope loss, the watchdog, a dead block, an
   epoch change, `wwrel_delete` and `_epilog`. A successful revalidation
   clears a sticky `last_err` (except a range error reported that frame).
8. **Death.** A slot flagged dying is released and in the same frame gets
   `health` (+0x285) = 0, `mEnemyIce.mLightShrinkTimer` (+0x2A5A) = 1 (the
   light-arrow shrink `enemy_ice` runs at the top of `daMo2_Execute` and
   ends in the actor's delete, death switch and item drop) and its
   attention flags (+0x280) cleared; state 4 and a 300-frame latch keep
   later samples from adopting the shrinking copy.

**Host role (`WWENEMY_ROLE_HOST`), the hit applier.** When the block's
`hit_apply.seq` differs from the last one taken and no request is open,
the REL resolves the pid Go put at `WWENEMY_HA_PID_OFF` and checks proc,
initState, the setID and the room (else result 2, no actor). With
`WWENEMY_HAF_FORCE_V1` in the request's flag byte the **v1** branch runs
as before: an invulnerable (+0x5B4 != 0), defending (action 10) or
ragdoll (mode <= -100) Moblin is dropped (details 2, 9, 3), else
`mLightShrinkTimer` = 1 and result 3. Otherwise **v2** lets the Moblin's
own damage code apply the hit, as if the host Link's sword had landed:

- A ragdolling Moblin is dropped at once (its colliders sit at y-20000,
  detail 3), a dying one too (health <= 0 or action 20, detail 4); an
  invulnerable one is waited for (up to 8 executes, then detail 2).
- The sphere hits as the host Link: the Link pointer is `play.mPlayerInfo[0]
  .mpPlayer` (0x803CA74C, the word `at_power_check` reads for the cut
  state), which must be a PLAYER (proc 0xA9) and equal `mpPlayerPtr[0]`
  (0x803CA754; they differ while Medli or Makar is controlled), else
  detail 5 and never a shrink. A REL-owned `dCcD_Stts` at mailbox +0x4D0
  is constructed (`__ct__9dCcD_SttsFv`) and `Init`'d with (120, 0xFF,
  Link), Link's own `mStts.Init` arguments, whenever that pointer changes.
- The At object is a `dCcD_Sph` image at mailbox +0x3A0, copied word by
  word from the target's own `mHeadSph` (+0x22EC) the first time (no
  `dCcD_Sph` constructor exists in the DOL), checked by its vptr
  (`__vt__8dCcD_Sph` 0x803899D0, else detail 6), with the At hit callback
  word and the At SPrm zeroed. Both objects are static because `cCcS::Set`
  keeps their pointers until `dScnPly_Draw`'s Move. The sphere image is
  rebuilt after a relink from a flag in the state; the stts is constructed
  and `Init`'d from the current Link on every request (a Link recreated at
  the same address after a stage load gets a new process id).
- Per request a `dCcD_SrcSph` is filled from the HIT: At type (0 = sword
  0x2), atp (0 = 1), At SPrm Set|VsEnemy, Se (0 = 1, the sword hit sound),
  hit mark (0 = 0xD, the normal particle and sword flash), Spl (1 = a kill
  blow, higher clamped to 1), the NoHitMark bit from `HAF_NO_HITMARK`,
  radius 30 and the centre from the host's own live Moblin: collider 1 the
  head joint (+0x28C8), 2 the position + 60 up (inside the Tg cylinder),
  else the client's centre. Then `dCcD_Sph::Set`, the stts store (+0x44),
  `dCcD_GObjInf::ClrAtHit`, `dCcD_Sph::StartCAt` (zero At vector + centre)
  and `cCcS::Set`, so that frame's Move tests it.
- One execute later, before any Set, the records are read: the Moblin's
  defence sphere Tg hit bit (+0x2418+0x1C) means result 4, parried; our
  sphere's At record (+0x04 bit 0 with the hit object +0x08 equal to the
  target's head sphere or Tg cylinder) means result 1, applied: the
  Moblin's `damage_check` runs the damage, reaction, sounds and hit-stop
  itself that frame. Another object's Tg crossing first is detail 7; no
  cross at all gets one more `StartCAt` + `cCcS::Set` with a recomputed
  centre, then detail 1 (`hit_nocross`++), or with `HAF_FALLBACK_SHRINK`
  the v1 shrink (result 3, detail 10) when the Moblin is neither
  invulnerable, ragdolling nor defending. The sphere is never re-Set after a cross:
  the Moblin's Tg NoConHit would dedupe it and its Move would clear the
  record (`cCcS::ClrAtHitInf` only walks that frame's objects).
- Any early return of the per-frame loop, a release of everything, delete
  or `_epilog` while a request is open acks it dropped with detail 8
  (`hit_aborted`++), unless the sphere was already Set and its At record
  shows the cross on the target (result 1: the Moblin took the hit).
- While a request is open the REL does not take another request's fields
  from the block; Go writes the next request only after the ack, and a
  request Go timed out is superseded by a cancel (a new seq with pid 0,
  acked no-actor).

The ack is the result word at +0x190 (byte 3 result, byte 2 detail, byte
1 executes from the first Set to the result, byte 0 Sets issued) followed
by `hit_apply_ack` = seq at +0x184. Results: 0 dropped, 1 applied, 2 no
actor, 3 shrink, 4 parried; details `WWENEMY_HD_*` in wwenemy.h.

**Status, every frame:** per slot the actor address, pid, state (0
unresolved, 1 mirrored, 2 released, 3 missing, 4 dying, 5 refused),
`pause_prev`, `last_err` (1 pid, 2 proc, 3 initState, 4 setID/room, 5
morf/model, 6 range, 7 mode 5 or 0xF), `anm_state` (0 S1, 1 synced, 2
unresolved, 3 getRes NULL, 4 out of range), the frames mirrored, the bck
applied and the `setAnm` count; `adopted_mask`, `epoch_seen`, the four
canaries (`pause_reasserts`, `anm_unresolved`, `revalidate_failures`,
`watchdog_releases`) and the second four (`anm_res_null`, `anm_range`,
`hit_nocross`, `hit_aborted`).

**Batch C (layout 4, `design/enemy-mirror-batch-c.json`).** Nothing in
the block or the annex moves. Block +0x22 is `knob_gen`, +0x28..+0x2F the
hook-disable bytes for kind ids 7..14 (BB +0x28, batch D's MT +0x29;
inside Go's seq span, read live by `em_hkd`), +0x194 the REL's
echo of the knob table it validated (0 when none or invalid, so Go
rewrites a clobbered table), +0x196 a saturating count of BB and MT fingerprint misses. The knob table at
mailbox +0x850 (0x80413750, 0x60 bytes, its own write) is valid when the
block's `knob_gen` is nonzero and equals its head and tail words, its
layout is 4 and it has a row per kind; then ids 3..15 take their quiet
frames, `tg_hit_up` (not TN: its annex byte 0x1FB stays) and invuln wait
from it, else the compiled values. `em_ext.kind_mask` is u16 (bit 8 is
batch D's MT).
- **Ambush Bokoblin.** `em_bk_gate` takes `mType` 0..4 (2 and 3 the pot
  ambushers, unconditionally; Go no longer writes 0 into them). While the
  host slot is hidden the copy's stick is parked at (-10000, -10000, 0) as
  the hidden host's is. A copy released still in its pot (its own `m02BA`
  0xFF, `mType` 2/3) gets `tubo_wait` (action 15, mode 0, `m0300[0]` and
  `m0310` 0, scale 1) instead of the create pair, so it can still leap.
  Both writes skip under `bk:hooks=ambush` (BK's 0x40).
- **Kargaroc (BB, proc 0xB6, kind 7).** Its base matrix is
  `EMK_MTX_TRYXZ` from `current.angle` (the smoothed yaw, the smoothed
  tilt in `current.angle.x` by `EMK_CAP_TILT_CUR`, the streamed roll in
  `current.angle.z` from slot +0x18), with `setBaseScale`; the head and beak
  words C00/C4E are zeroed before calc; after calc the tail root is copied
  to the tail chain and the retail `tail_control` is called through the
  copy's own profile (`sub_method->mpExecuteFunc` - 0x69A8 + 0x404) when
  its fingerprint matched once per adopt, else the chain collapses onto
  the root. Riders (types 5/6), mbDead, knockback and a hidden copy with a
  shown host are refused; the draw gate 0x2F2 is cleared while the host is
  shown; a release hands it back into the attack state with its timer
  seeded (450, `l_bbHIO.unk_16`).
- **Magtail (MT, proc 0xD9): cut in batch C.** The row was cut by R-20
  after the P0-MT live probe failed (`M_NewD2` room 8: the eye sphere sat
  at `eyePos`, but the body sphere centres did not equal the segment
  translations, segment 0 about 65 units off and segments 1..4 about 10);
  batch D brings it back on the copy's own retail builders (below).

**Batch D (`design/enemy-mirror-batch-d.json` R-D6, D8-D10; still layout
4).** Magtail is kind 8 again (`EMK_KIND_COUNT` 9), all of it in `em_mt.c`
under `EMK_WITH_MT` (the first cut: `EMK_WITH_MT 0` with `EMK_KIND_COUNT 8`
compiles it out) plus one guarded dispatch line per call site in
`enemy_mirror.c`; Go sets kind bit 8 only for `mt+u` and a matching REL
build id. Batch C's gate, hit forwarding (eye = collider 1, any body sphere
= collider 2, a thrown Magtail dropped), custody BALL (collider code 4) and
the respawn flag 0x1CBC are restored as they were. The hand-built body is
gone: once per pid `em_mt_fp_ok` checks `daMt_Execute`'s, `body_control1`'s
and `body_control2`'s first words and both builders' `blr` in the loaded
`d_a_mt.rel` (a miss bumps +0x196 and refuses the adoption), and every
frame `em_mt_body_step` writes the streamed head (shape x/y/z, the wiggle
+0x468 from slot +0x18), clears the latch +0xC01 and the hit cooldown
+0x460, ticks the sway clock +0x46A and calls `body_control1` (MOVE, with
+0x48E = 1 while the copy's head is within 5 units of the ring's last kept
entry, so the ring advances on the copy's own motion; batch E raised it
from 3, whose eased frames packed the segments to 21-25) or `body_control2`
(FIGHT, after +0x48E = 0 and +0xC04 = 0); PINNED calls nothing.
The builders write the eight segment matrices and enter the nine spheres
themselves; `em_mt_coll_post` then clears the body spheres' At, puts a
withheld collider at R -200, and in PINNED clears At and Co on the bodies
and enters the nine itself. The two mode edges copy the ring into the
chain (MOVE -> FIGHT) or reseed the ring from it (back to MOVE), as the
retail latch paths do. A dying copy calls nothing and shrinks through its
segment models' base scale.

Game functions called, all through absolute function-pointer casts
(`config/GZLE01/symbols.txt`): `fopAcM_SearchByID` 0x800241C0,
`mDoExt_McaMorf::play` 0x8001300C, `::calc` 0x800132E8 and `::setAnm`
0x80012D78, `dRes_control_c::getRes` 0x8006F208 (`dRes_getRes_byIdx` in
wwapi.h, with `mObjectInfo` 0x803E0BC8), `dr_matrix_set` 0x80022460,
`PSMTXCopy` 0x8030D0C8, `cM3dGCyl::SetC` 0x80251D88, `cM3dGSph::SetC`
0x8025238C, `cCcS::Set` 0x80242D04, `dCcD_GObjInf::ClrTgHit` 0x800AB958
and `::ClrAtHit` 0x800AB7C4, `dCcD_Sph::Set` 0x800ABFC0 and `::StartCAt`
0x800AC02C, `__ct__9dCcD_SttsFv` 0x80100EB8, `dCcD_Stts::Init`
0x800AB3DC, `cM_ssin` 0x800F0054, `cM_scos` 0x800F0038,
`dPa_smokeEcallBack::end` 0x8007BB80 (the Armos Knight's mouth smoke,
`mSmokeCbs[2]`, at the bomb swallow and at adopt), `cc_at_check`
0x800AEEF8 (the TN batch's AM2 direct apply, `em_hit_direct`); batch C:
BB's `tail_control` in the loaded `d_a_bb.rel` (fingerprinted); batch D:
MT's `body_control1` and `body_control2` in the loaded `d_a_mt.rel`
(fingerprinted). Data read:
`calc_mtx` 0x803F63A0, `mCurStage.mName` 0x803C9D3C, `mStayNo`
0x803F6A78, `mNextStage.mEnable` 0x803C9D54, `mPlayerInfo[0].mpPlayer`
0x803CA74C, `mpPlayerPtr[0]` 0x803CA754, `__vt__8dCcD_Sph` 0x803899D0
(compared, never called).

## remote.c: remote-Link

The REL side of remote-Link (handoff `design/remote-link-design.md`
sections 5, 7.1, 8.5, 8.6 and its "Step -1 results"; the mailbox contract
is `inject/include/wwremote.h`, the WWRP block at `MAILBOX_ADDR + 0x520`).
Every behaviour is behind a Go-written flag; with the block's magic 0 the
module does what it did before, except that the exec hook counts its calls
(`WWREMOTE_CTR_HOOK_CALLS`) and the keeper writes the status words.

- **Keeper** (after the Moblin mirror, every frame): block intake by the
  enemy block's rules (magic, layout, a role that agrees with the enemy
  block's, seq_head == seq_tail, stale after 90 frames); an epoch change
  resets the latches (their Moblins' m714 back on the host Link first) and
  the create tries; latches whose enemy is gone are reaped. **Host,
  `FLAG_BODIES`:** per slot EMPTY → QUEUED (`fopAcM_create(0xB5, params,
  ...)`, at most 3 tries per stage and per owner, cooldown after a failure)
  → LIVE (the pid resolves to a `'WWRB'` body of that slot) → DYING
  (slot released, owner gen changed, stage mismatch, block stale or off,
  BODIES cleared: m714 rewrite, `fopAcM_deleteByID`; a queued stage change
  or a Medli/Makar window only parks it and gates the create)
  → EMPTY; a pid that vanished (stage teardown) is GONE. **Client,
  `FLAG_APPLY`:** a new apply seq resolves the attacker setID among the
  mirror's adopted Moblins (none: NO_MIRROR), builds the static sphere
  (`enemy_mirror_sph_image`) with a stts naming that Moblin, the host's At
  values and At SPrm Set|VsPlayer, and registers it at the client Link's
  position + rel_hit with At vector rvec (StartCAt/MoveCAt). One execute
  later the At record says APPLIED/GUARDED (crossed Link's mCyl), NO_CROSS
  with detail IFRAMES (Link's Tg Set bit off) or one re-register; two
  executes later a cross without DAMAGE mode and without a life change is
  EARLY_OUT. Then the status: body_status (PARKED is reported for a LIVE
  body that parked itself), and the hook's fast-path byte.
- **Lite body** (host only): create checks the slot, the block, the
  `__vt__9daPy_py_c`/`lk_c` checkPlayerGuard fingerprints, the host Link
  and its mCyl vptr (`__vt__8dCcD_Cyl`), else `cPhs_ERROR_e` with the
  reason in the keeper's record; it copies the vtable (pure slots and
  checkPlayerGuard stubbed) and Link's mCyl (DOL-built, no vptr remap),
  neutralises the copy (no At, Tg Set|IsPlayer only, no Co, no callbacks,
  no shield bits, the shield-angle pointer at its own word), constructs and
  Inits its own stts and becomes a stage actor. Execute: park check (no
  keeper record, block, slot active and gen, stage and room, DEAD/NO_HIT,
  pose feed state 1 and its seq moving within 30 frames, host Link);
  `FLAG_DETECT` pushes last Move's Tg hit into the ring (the attacker's
  type/atp from the At object at +0x10/+0x14, Se/HitMark/Spl from its
  GObjAt at +0x6C/+0x6E/+0x6F) and clears it; parked bodies stand on the
  host Link and never Set; else pose, cut state, shield angle, i-frames,
  the guard bits and (`FLAG_HITTABLE`) the cylinder Set. Delete rewrites
  the Moblins that cached it and clears the keeper's pointer.
- **Hook** (`wwrel_exec_hook`): hook_calls++, then the swap state word and
  its fast-path byte (valid host block, `FLAG_SWAP`, a LIVE unparked
  candidate body, no stage change queued). Then PROC_POLICY (DENY wins;
  ALLOW, or `FLAG_GROUP_WIDE` over the ENEMY group), the host-Link gate,
  the FF denylist (unless `FLAG_FF_GATE_OFF`), mo2's jail gate
  (`(*(u8*)0x803C4CBC & 0x0F) == 0`, unless `FLAG_ROUYA_GATE_OFF`) and
  demo/mode fields, the target (attacker wins with `FLAG_ATTACKER`, else
  the latch with min_hold and margin, no switch mid-attack; a full latch
  table means no swap), and the swap of `mPlayerInfo[0].mpPlayer`
  (0x803CA74C; also 0x803CA754 for a BOTH_WORDS proc with
  `FLAG_BOTH_WORDS_ENABLE`; bodies named 0xA9 with `FLAG_NAME_A9`) around
  the one `fpcMtd_Execute` call, restored unconditionally.
- **ATTRIB** (enemy_mirror.c's v2, `FLAG_ATTRIB` and the request's
  `WWREMOTE_HAF_ATTRIB_BODY`): with the slot's body LIVE and unparked the
  stts names the body, the Moblin's latch is forced onto it for 3 executes
  and, when it was not the current target, the first Set waits one
  execute; otherwise the host-Link path with detail `WWREMOTE_HD_NO_BODY`.

Game functions remote.c calls (absolute casts, `config/GZLE01/symbols.txt`):
`fpcMtd_Execute` 0x8003EF38, `fopAcM_create` 0x8002451C, `fpcM_IsCreating`
0x8003E3B0, `fopAcM_SearchByID` 0x800241C0, `fopAcM_delete__FUi`
0x800244B8, `fopAcM_setStageLayer` 0x80024060,
`dCcD_GAtTgCoCommonBase::GetAc` 0x800AB2AC, `cM3dGCyl::SetC/SetH/SetR`
0x80251D88/0x80252020/0x8025214C, `cCcS::Set` 0x80242D04,
`dCcD_GObjInf::ClrTgHit/ClrAtHit` 0x800AB958/0x800AB7C4, `dCcD_Sph::Set/
StartCAt/MoveCAt` 0x800ABFC0/0x800AC02C/0x800AC078, `__ct__9dCcD_SttsFv`
0x80100EB8, `dCcD_Stts::Init` 0x800AB3DC. Data: 0x803CA74C, 0x803CA754,
`mCurStage.mName` 0x803C9D3C, `mStayNo` 0x803F6A78, `mNextStage.mEnable`
0x803C9D54, mLife 0x803C4C0A, mCollect[0] 0x803C4CBC, the vtables above
(read, never called).

## The second profile: the full body (fullbody.c, shanm.c)

Stage 4 of remote-Link (handoff `design/remote-link-stage4-plan.md`,
`design/remote-link-design.md` section 9; contract `wwremote.h`
`WWREMOTE_FULL_*`, `WWREMOTE_FP_*`, `WWREMOTE_SA_*`; addresses `dapy.h`).
Everything ships off: with `WWREMOTE_FLAG_FULL_BODIES` clear the keeper
creates only lite bodies and nothing below runs.

- **The profile.** `rel_main.c` exports a second profile,
  `g_profile_WWBODY` (`.rodata`, 0x30): `g_profile_WWREL`'s fields but
  mSize `WWREMOTE_FULL_SIZE` (0x4C80: `daPy_lk_c` 0x4C28 plus the
  `rl_full_tail`) and the `fullbody_*` methods; mProcName stays 0xB5. The
  patcher points profile slot 0x27 (Seatag, never placed on the GZLE01
  disc) at it and `build.py` patches proc 0x27's DynamicNameTable entry
  (0x80339938, DOL file 0x33693C) to the name proc 0xB5 uses,
  `"d_a_rectangle"`, so `DMC[0x27] == DMC[0xB5]`: both procs load this
  module through one DynamicModuleControl and a full body pins it like a
  keeper. The two patches are only safe together, so the keeper checks
  both (`DMC[0x27] == DMC[0xB5]`, list slot 0x27 == `&g_profile_WWBODY`)
  before every full create (else `WWREMOTE_BERR_PROFILE`, lite).
- **The keeper** (remote.c) picks the kind per create: FULL when
  `FULL_BODIES` (host) is set, the slot's `FULL_ALLOW` bit (Go's heap gate,
  latched per consumed seq and cleared by the create it starts) is set,
  the slot's full attempt is unspent this stage/epoch/gen, no full body is
  QUEUED and the profile checks. A clear allow bit waits up to 120 keeper
  frames while `HEAP_REASON` is transient (NONE, NO_SAMPLE, INFLIGHT,
  STAGE_SETTLE, QUEUED), then creates a lite body with `BERR_HEAP_DENIED`.
  Go gives the bit only to a slot the keeper can create in (active, a
  fresh PSTATE in the host's stage) and says QUEUED while another such
  slot also waits, so that one waits its turn. A full
  create that fails (create ERROR, LOST, GONE within 60 LIVE frames) spends
  the attempt: lite for the rest of the stage. Clearing `FULL_BODIES`
  retires a LIVE full body; no upgrade within a stage. body_status +0x0F is
  the kind. The new keeper fields live in the swap state's old pads
  (`full_allow` +0x19, `flags2` +0x1A, `full_denied` +0x79, `full_wait`
  +0x7A, `rl_body.kind` +0x13), so its size (0x17C) and every field the
  probe reads keep their offsets.
- **create** (`fullbody_create`, one call per frame in `fpcCt_Handler`):
  FP_NEW checks the params tag, caches slot/gen, allocates the shanm table
  if the boot has none (`BERR_SHANM_ALLOC`), becomes a stage actor and
  rewrites the params to 0xFF000040; FP_PARAMS waits for the host Link
  (created), the block and no queued stage change, runs
  `__ct__9daPy_lk_cFv` (vptr fingerprint 0x8038FDC0, `BERR_FULL_VTABLE`),
  places itself at the slot, then `playerInit` inside the window (linktex
  ResTIMG, shanm, global snapshot, `mLastMode` 0) and fails with
  `BERR_HEAP_ERRORS` if GameHeapErrors or solidHeapErrors moved across it;
  then ww-online's mAcch fixups and mDemo 0; FP_INIT runs `makeBgWait` in
  the window (cl.bdl joints 0x02/0x1D saved and restored around it) until it
  finds ground, makes the body inert (attention 0, mResetFlg0 0, no cut At,
  mCurProc WAIT, no damage/ship/crawl mode, Co off unless
  `FLAG2_FULL_CO`) and completes, magic last. Every phase but the last
  returns `cPhs_INIT_e` (any other value ends the create,
  `fpcBs_SubCreate`); past 120 calls it is `BERR_PHASE_TIMEOUT`. Vanilla
  phase_1 (setPlayer/setLinkPlayer) never runs.
- **execute**: the lite body's park check and hit read (shared, on
  `mCyl`), then the pose (position, angles, room, eyePos), the 42 cl.bdl
  node matrices from the pose feed into `mpCLModel->mpNodeMtx` and its base
  TR, cut bytes, mCurProc WAIT or CROUCH_DEFENSE (`FLAG_GUARD` + slot
  guard: the real `checkPlayerGuard` answers), mDamageWaitTimer = the
  longer of the slot's i-frames and the stamp, mResetFlg0 0, the shield
  bits and angle, Co, and (`FLAG_HITTABLE`) `setCollision` with the shared
  Deku Leaf anms held at their last frame. Never a proc, a calc or
  `daPy_lk_c::execute`.
- **draw** returns TRUE (the mini-Link renders the remote).
- **delete** (also after a create ERROR): the keeper's record and the m714
  rewrite, then, once `playerInit` ran, CASUAL cleared and `playerDelete`
  in the window; the shanm delete scan (`SA_DELETE_SCAN`, must be 0).
- **shanm.c** ports ww-online's `puppet_sharedanm.c` without the
  per-actor swap list: every animator slot of the "Link" archive's model
  data (joint 0 `mMtxCalc`, material `mMaterialAnm` and its 26 slots) is
  snapshotted when a window opens and every changed word is written back
  when it closes, so outside the windows the shared data is the local
  Link's. Its tables and variables live in one archive-heap block
  (`WWREMOTE_SHANM_BYTES`, header `WWREMOTE_SA_*`), allocated at the first
  full create of a boot and never freed, so shanm keeps nothing in `.bss`
  (the module's `.bss` is visuals.c's per-frame scratch: the j3dSys
  snapshot and the name tag packets, which no unlink needs to survive).
  `fullbody.c`, `fullbody.h`, `shanm.c` and `shanm.h` carry ww-online's
  MIT notice.

## The build

`cd inject && python build.py` builds T2 as before and then this module
(`build_rel`; `--no-rel` skips it):

```
powerpc-eabi-gcc -mcpu=750 -std=c99 -fno-inline -Og -fshort-enums
    -fno-jump-tables -ffreestanding -fno-builtin -G0
    -fno-asynchronous-unwind-tables -fno-unwind-tables -fno-ident
    -Wall -Wextra -Iinclude -DWWREL_BUILD_ID=0x... -c rel/rel_main.c
powerpc-eabi-ld --relocatable -T rel/ww_rel.ld -Map build/wwrel.map
    -o build/wwrel.elf
```

The flags are ww-online's (`WWOnline.Patcher/Tools/DevKitPpcToolchain.cs`)
plus this project's freestanding ones. `-fno-jump-tables` because a jump
table needs `R_PPC_REL32`, which the converter does not emit; `-Og` and
not `-Os` because `-Os` calls libgcc's `_restgpr_*_x`, which the DOL
lacks; `-G0` so nothing lands in `.sdata`/`.sbss`/`.sdata2`, which are
addressed off r13/r2 through `R_PPC_EMB_SDA21` relocations the game's
linker cannot apply (the T2 build uses `-G0` too); no unwind tables and
no `.comment`, so the ELF has only `.text`, `.rodata`, `.data`, `.bss`,
`.ctors`, `.dtors` and their `.rela.*`.

`build_rel` prints the section table, the relocation counts per type,
`_prolog`/`_epilog`/`_unresolved`/`g_profile_WWREL`/`g_profile_WWBODY`
with section and offset, and the `.text` size, and fails on an undefined symbol (the game
would route the call to `_unresolved`), an `R_PPC_REL32`, a small-data
section, or a missing symbol. After it, `main` patches proc 0x27's
name-table entry in `patched.dol` (`patch_body_name`: the entry's
vanilla bytes asserted first, the file offset from the DOL's section
table, `dol_file_offset`); `--no-rel` skips both, and the module's
`.bss` stays 0. `scripts/extract_blob.py` repeats those
checks on the ELF it embeds (pure Python, no devkitPPC), verifies the
profile's `mProcName`, and emits `RELELF` plus the constants into
`internal/inject/blob.go`; without the ELF it emits an empty `RELELF`.
`blob.stamp` carries the ELF's SHA-256 (`rel` line) and `inject/rel`'s
sources among its inputs, so CI catches a REL change without a regen,
while the `blob` line stays the T2 payload alone.

Expected relocations for this module: `R_PPC_ADDR32` for the profile's
five method pointers, its method-table pointer and the two `ww_rel.ld`
symbols; `R_PPC_ADDR16_HA`/`_LO` pairs for `&_ctors`/`&_dtors` in the
prolog and epilog, for `enemy_mirror.c`'s float constants (gcc merges
them into `.rodata.cst4`, which the converter copies), its `"Mo2"`
archive name in `.rodata` and its one `.data`
pointer, and for remote.c's vtable stub addresses and `&wwrel_exec_hook`
(`.text` self-relocations); and `R_PPC_REL24` for the calls between
`rel_main.c`, `enemy_mirror.c`, `remote.c`, `fullbody.c` and `shanm.c`
(a `bl` between objects; the
converter and `OSLink` apply them). Calls within one unit are resolved by the compiler; game calls go
through absolute pointers and need none.

## Who creates the actor

Nothing in the vanilla game creates proc 0xB5, so T2's `src/mp/relload.c` does:
every frame once Link exists it checks the magic word, and when the module is
not linked it creates one actor of proc 0xB5 at Link's position (invisible,
allocates nothing), at most three times per stage with a 60-frame cooldown. The
game's loader then loads and links the module and `_prolog` writes the magic;
a stage transition would delete the actor, unlink the module and clear the
magic, and the next stage would start over, but live (2026-10-18, a stage-hop
from Outset to A_mori) the actor survived: created from the frame hook, outside
any stage actor, it lives in a layer the stage load keeps (inferred: the root
layer) and the module stayed linked through the load. On a disc whose RELS.arc was
not patched the vanilla dummy is created instead and never writes the magic;
`wwapi.Info` reports the module as not linked.

## bosskill.c: the boss_kill relay op

Co-op's Gohma kill relay (`ww-multiplayer-handoff/design/research/C3-C6-gohma.md`,
"The relay op (B5, revised)"). The request is 16 bytes at
`MAILBOX_ADDR + 0x510` (`include/wwboss.h`), the gap between the enemy
region and the remote-Link block, which neither side of remote-Link writes:
Go writes the proc (0x00EB, Btd) and tag ('BK'), then the seq; the keeper
writes the result, then the ack. The keeper's create consumes whatever the
slack holds (ack = seq, result `WWBOSS_R_NONE`), so a stale seq from an
earlier boot never runs. Every frame, a new seq is run once: no stage change
pending, the stage named exactly `M_DragB` (never an X refight stage), then
`fopAcM_SearchByName(0xEB)`. Btd's `m02E4` 0xA (the intro) is answered
`waiting`, 0xB `already`, and 0, 1 or 2 with the armor broken (`m02E0` 1, the
only phase the lethal eye hit takes, d_a_btd.cpp:457) get that hit's two
writes, `m02E4 = 0xB` and `mGohmaState = 0x32` (d_a_btd.cpp:463-475); an
armored Gohma (`m02E0` 0) is answered `waiting`, since `end()` on phase 1
would play the death on the armored model with the phase-2 anims bound to
it (bosskill.c, "The armor gate"). The result's low byte carries `m02E0`.
On the eye phase Btd's own
`end()` runs its death demo, the floor drop, the heart container,
STAGE_BOSS_ENEMY and the warp flower. Nothing is deleted. The op is
stateless and adds nothing to the keeper's actor.

Kalle Demos (`research/C1-kalle-demos-relay.md`) is the second proc,
0x00EC (Bmd), taken only in the stage named exactly `kinBOSS`. `bk_bmd`
answers mMode 0xB `already`, applies only at the fallen, open flower (mMode
3 at `m302` 2..5, `m304` not 0, `m310` 0, `mB74` 0; `m304` alone can be
left over at `m302` 0..1) and answers every other state
`waiting`. The apply is all of `core_damage_check`'s lethal block
(d_a_bmd.cpp:609-617): `m306` 100, `mB74` 100, `m310` 20000, `mBE0` 2,
`mBDC` 1.0f, `m302` 0, `mMode` 0xB, then `mDoAud_bgmStop(30)` through
`zel_basic` (two fields alone would leave `end()` waiting on an `mB76` that
never counts). The result bytes for this proc are mMode, `m302`, `m304`.

Gohdan (batch D R-D1..R-D3, under `WWBOSS_WITH_BST`) is the third proc,
0x00F2 (Bst), taken only in the stage named exactly `SirenB`. Its head and
two hands are `.bss` statics of `d_a_bst.rel`, so `bk_bst` finds each part
with `fopAcIt_Judge` (0x80023FB4) and `bk_bst_judge`, the first module
function a DOL iterator calls back (build.py fails the build unless it is
a `.text` symbol). Any part missing, or the head's morf NULL, is `no_actor`;
END_DEMO, `m2E9A` >= 0x32 or `m2E7C` 100 is `already`; it applies only at
vanilla's own pre-death state (head HEAD_DAMAGE with `mDamage` 3, the
mouth open for a bomb, `m2E7C` 12, `m2E9A` 0, both hands SLEEP), else
`waiting`. The apply is `bom_eat_check`'s lethal block (d_a_bst.cpp
:1219-1229) without health, the bomb or the SE: head `m2E9A` 0x32 and
`mDamage` 0, the DEAD anim through `getRes` + `mDoExt_McaMorf::setAnm`
(behind `WWBOSS_BST_ANIM`), both hands END_DEMO with `mDamage` 0xA, the
head END_DEMO last, then `bgmStop(30)` and `bgmStreamPrepare(BST_CLEAR)`;
its own `end_demo` does the rest. The result bytes are the head's
mActionType, `mDamage`, `m2E7C`.

Molgera (`design/relay-molgera.md`, under `WWBOSS_WITH_BWD`) is the fourth
proc, 0x00DA (Bwd), taken only in the stage named exactly `kazeB` and found
by name. A NULL head morf is `no_actor`, `m18AE` 0xC or 0xD `already`. It
applies only in vanilla's own pre-death window: `m18AE` 3 (`sita_hit`),
`m18B0` 2 (the tongue pulled out and looped), the tongue timer `m1BB1` not
0 and the demo camera `m3C1E` 0; else `waiting`. The apply is two s8
counters, `m1BB4` = 3 and `m1BB5` = 3, so on Bwd's next `move()` its own
`m1BB5++` makes the fourth critical pull and its own lethal block, `end()`,
heart container, STAGE_BOSS_ENEMY and warp flower follow. No call, no
health. The result bytes are `m18AE`, `m18B0`, `m1BB5`.

Jalhalla (`design/relay-jalhalla.md`, under `WWBOSS_WITH_BPW`) is the fifth
proc, 0x00D4 (Bpw), taken only in the stage named exactly `M_DaiB`. Its
kantera and damage balls share the proc, so `bk_bpw` finds the body with
`fopAcIt_Judge` and `bk_bpw_judge` (mType 0, mInitState 2, not creating;
build.py checks it is `.text` too). No body is `no_actor`; health <= 0 or
mBodyAction 4 at mActionState >= 0x78 is `already`. It applies at
mBodyAction 3 with mActionState 0x52..0x55 (frozen, carried, thrown), 0x64
or 0x65 (the spike hit) and health > 0, before any Poe exists; else
`waiting`. The apply is one write, health = 0: the next spike landing
creates no Poe and its own split goes straight to the death demo (BGM
stop, clear stream, heart, STAGE_BOSS_ENEMY, warp flower). The result
bytes are mBodyAction, mActionState, health.

Helmaroc King (`design/relay-helmaroc.md`, under `WWBOSS_WITH_BDK`) is the
sixth proc, 0x00F0 (Bdk), taken only in the stage named exactly `M2tower`
(not `Mjtower` or `M2ganon`). It has its own arm in `bosskill_frame`, ahead
of the final `else`, which is Kalle Demos' lethal block. One Bdk, on layer
3 only, found by name; a NULL morf, or no Bdk on layers 0/1, is
`no_actor`. `mAction` 0xA (the death) or 0x6E (the inert create) is
`already`, and so is a fight action 0..8 with health <= 0 (the zero an
earlier request left). Any other action (the tower phase 0x64..0x67, whose
health is the create's zeroed 0, START 0xF, KAMEN_DEMO 9) is `waiting`. It
applies at a fight action with the mask broken (`m8F8` >= 4), no demo
camera (`m25A0` 0) and health > 0; else `waiting`. The apply is one write,
health = 0: health is read only by `damage_check`'s crest branch, so the
receiver's own next crest hit runs the pause, `end_set` and LAST_DAMAGE,
and the death camera sets STAGE_BOSS_ENEMY. The result bytes are
`mAction`, `m8F8`, health.

## refresh.c: the actor_refresh op

Batch D's Tier 3 stale-actor refresh (`design/enemy-mirror-batch-d.json`
R-D4, R-D5, D4-D7), under `WWREFRESH_ENABLE`. The request is 0x20 bytes at
`MAILBOX_ADDR + 0x8B0` (0x804137B0, `include/wwrefresh.h`), past the
enemy knob table; the keeper's create consumes a stale seq, as boss_kill's
does, and its execute runs a new one after boss_kill. It deletes one
actor of five allow-listed procs (Stone2, Obj_Leaves, daItem, Tbox, Saku)
this game placed before the partner's bit landed, and only after it has
itself re-derived the bit from the actor's params or fields, re-read it
with the DOL's `isSwitch` / `isTbox` / `isItem`, and passed the generic
gates (no stage change, no boss arena, created, no event command, not
carried, not paused or mirrored, Link not in a demo and farther than 300
units, the global event mode NONE) and the proc's own (Stone2 idle, daItem
lying normally, Tbox closed and waiting behind its `daTbox_Execute`
fingerprint, Saku not burning with every standing half's switch set).
The delete is `fopAcM_delete__FUi`. The result is `R_*`, the gate id and
the proc's state as found. Stateless; nothing is added to the keeper.

## visuals.c: the remote players' visuals

The block is `include/wwvis.h` at `MAILBOX_ADDR + 0x8D0` (0x804137D0,
0xC0 bytes, right after the actor_refresh request; Go's
`wwapi/mailbox/vis.go` mirrors it and `CanaryArenaHigh` now starts at its
end, 0x80413890). Its first word is `&wwvis_draw_hook`, published by
`_prolog` just before the exec hook word and cleared by `_epilog` first;
T2's `main01_init` zeroes it (and the model words) at boot. T2's
`mp_draw_post` calls it twice per drawn mini-Link slot: phase 0 before the
slot's body entry, phase 1 after it. Go (`mods/multiplayer/vissync`) writes
the magic, the flags and the per-slot equip bytes from each remote's pose
trailer.

- **Sword, shield, scabbard** (ww-online review idea 15). Phase 0 sets the
  shared `CL_PODA` scabbard shape from the remote's sword (shown with the
  hero's sword only, as Link does), phase 1 puts Link #1's value back and
  enters the remote's hilt (and blade when the sword is out) and hero's
  shield at the hand or scabbard joint of the remote's streamed pose,
  placed by Link's own matrices (d_a_player_main.cpp:1270-1340). The
  models (SWGRIPA, SWGRIPMS, SWA, SWMS, SHA from Link.arc) are made once
  per boot on the archive heap and kept in the block's model words. Link's
  joint and material animators on the shared model data are cleared around
  our calc and calcMaterial (they point into his actor and item heap), so
  the parts take their bind pose. Not drawn: the mirror shield (it needs a
  J3DSkinDeform), the Master Sword's own scabbard, glow and blur. The full
  bodies (fullbody.c) never draw, so this is mini-Links only.
- **Name tags** (ww-online review idea 6, adapted from their
  `puppet_nametag.c`; `visuals.c` carries their MIT notice). Phase 1 also
  projects a point 135 units above the slot's streamed root joint
  (`mDoLib_project`, 640x480) and enters a per-slot `dDlst_base_c` packet
  into the HUD's 2D opaque list (`dDlst_list_c::set` on `mp2DOpa`); its
  draw prints the slot's name (`WWVIS_NAMES`, Go writes the roster names)
  with `mDoExt_font0`, white over a black shadow, sized and faded by
  camera depth. Hidden when the HUD is (2D off, an event, the pause menu,
  the telescope or Picto Box view) and when `WWVIS_FLAG_NAMES` is clear
  (`WW_NAMETAGS=off`). The packet is drawn by the next frame's painter,
  which runs before process deletes (`fpcM_Management`), so the module is
  still linked when its draw runs.
- **Sea chart markers** (ww-online review idea 14, adapted from their
  `puppet_seachart.c` and `seachart_players.asm`, MIT). The block's second
  word is `&wwvis_seachart_draw`; T2's `seachart_shim` replaces
  `dDlst_FMAP_c::draw`'s `bl J2DScreen::draw` (0x801BB06C), makes that call
  and then calls the word with the menu's `fmapDl` (r30) and graf port
  (r31) while it is published. On the world map Link's own `lnk1` picture
  is redrawn once per `WWVIS_SEA` entry (Go: each remote whose pose
  trailer says it is on the sea stage, in its tunic colour), offset from
  Link by `(pos - Link's) * 56 / 100000`, turned to its heading; on a
  zoomed square (steady) `lnk2` is redrawn for that square's remotes as
  `setDspLargeMapLink` places Link. The pane must be a `J2DPicture`
  (vtable 0x8039D730) and its state (0x0C..0xB0 and both colours) is
  restored after. Only when the chart was opened on the sea stage, and
  only with `WWVIS_FLAG_SEACHART` (`WW_SEACHART=off` clears it).

## fx.c: the other players' bombs, cannonballs and arrows

ww-online review idea 5, adapted from their `puppet_fx.c` (`fx.c` carries
their MIT notice); design: handoff `archive/ww-online-review/
DESIGN-big-items.md` (a), option A. The block is `include/wwfx.h` at
`MAILBOX_ADDR + 0xE50` (0x80413D50, 0x2B0 bytes, the top of the slack up
to the arena; Go's `wwapi/mailbox/fx.go` mirrors it and `CanaryArenaHigh`
now ends at its start). `fx_frame` runs last in the keeper's execute. No
T2 code is involved, and its state (the trackers and the copy table) is
the module's `.bss`, emptied by `_prolog` (`fx_link`), so a new link
starts empty; its first frame takes
Go's read words as its own, so nothing old is sent or spawned.

- **Sender** (send magic set, broadcast-pose): the local Link's hand bombs
  (`mActorKeepGrab`, STATE_1..3, and a plucked Bomb Flower), the boat's
  cannonball (the BOMB with the ship's exact params on a
  `daSFLG_SHOOT_CANNON_e` frame, found with `fopAcIt_Judge` and
  `fx_cannon_judge`, a `REL_TEXT_CALLBACKS` entry) and the nocked arrow
  (`mActorKeepEquip`, reported once when it leaves the bow with params 1)
  go into the outbox as THROW / PICKUP / EXPLODE / REMOVE / CANNON / ARROW.
- **Viewer** (recv magic set, puppet-sync): the inbox's events become
  real `daBomb_c` and `daArrow_c` copies, made with `fopAcM_fastCreate`
  in the local Link's layer (the keeper's is the root layer, which
  outlives a stage). World-only: a bomb copy has `WWFX_BOMB_PRM_COPY` in
  its params, `field_0x6F0` cleared, `field_0x6F1` (getBombCheck_Flag)
  set so no Armos Knight or Gohdan swallows it (the host's real Knight
  would otherwise kill itself on a copy, d_a_am.cpp:929-955), and
  VsEnemy | VsPlayer cleared in its At SPrm; an arrow copy has VsEnemy cleared, its Co sphere off, the
  procMove PTMF and `arrowShooting`, params 0. A thrown copy's fuse is held
  until the sender's EXPLODE (their state machine), unless this world took
  it over. A stage change discards the inbox and deletes the live copies.
  With both magic words 0 the REL writes nothing in the block, except
  that the frame a session ends it deletes that session's live copies
  and counts them (the 6th counter).
- **Enemy mirror**: no copy can hit an enemy, so `em_hits` never sees
  one, and no copy can be swallowed (`em_am_swallow_client` asks the
  same BombCheck flag); its `fx_swallowed_copy` call stays as a tripwire
  (the 5th counter) that should read 0.
