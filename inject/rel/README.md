# The REL module (inject/rel)

Mod code shipped as a REL, loaded by the game's own dynamic linker,
instead of as T2 bytes in the DOL (T2 has 0x258 bytes left below the
mailbox). Plan: `ww-multiplayer-handoff/design/rel-module-plan.md`.
`rel_main.c` is the module (the profile, the actor, the prolog/epilog);
`enemy_mirror.c` is the Moblin mirror of the host-authoritative enemy
prototype (below), run from the actor's execute.

## What it is

`rel_main.c` is an actor module. Its profile, `g_profile_WWREL`
(`.rodata`, 0x30 bytes, the decomp's `actor_process_profile_definition`),
describes proc 0xB5 with a five-method table (create, delete, execute,
is_delete, draw) and an actor of `sizeof(fopAc_ac_c)` (0x290 in GZLE01)
plus one u32 of its own and the Moblin mirror's state (`mSize` = 0x294 +
`sizeof(enemy_mirror_state)`, 0x458 bytes with layout 2):

- `_prolog` runs the module's constructors (none) and the linker's
  `ModuleProlog`, then writes the mailbox slack words (below).
- create allocates nothing and registers no model or collision, so the
  actor is invisible and untouchable; it zeroes the mirror's state and
  returns `cPhs_COMPLEATE_e`.
- execute bumps the slack frame counter once per frame, then runs the
  Moblin mirror's per-frame loop (`enemy_mirror_frame`).
- delete releases every mirrored Moblin; draw and is_delete return TRUE.
- `_epilog` releases too if delete never ran, clears the magic word, then
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
past the struct belong to nobody. The module uses the first three words.
The struct and its size are unchanged; the offsets are `#define`s in
`rel_main.c`, read by `extract_blob.py` into `blob.go`.

| Offset from MAILBOX_ADDR | Address    | Word                | Writer                    |
|--------------------------|------------|---------------------|---------------------------|
| +0x1B0 `WWREL_MAGIC_OFF` | 0x804130B0 | `'WWRL'` 0x5757524C | `_prolog` sets, `_epilog` clears |
| +0x1B4 `WWREL_BUILD_ID_OFF` | 0x804130B4 | build id          | `_prolog`                 |
| +0x1B8 `WWREL_FRAME_OFF` | 0x804130B8 | frame counter       | `_prolog` zeroes, execute +1 |

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
  keeps their pointers until `dScnPly_Draw`'s Move; both are rebuilt after
  a relink from flags in the state.
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
  invulnerable nor ragdolling. The sphere is never re-Set after a cross:
  the Moblin's Tg NoConHit would dedupe it and its Move would clear the
  record (`cCcS::ClrAtHitInf` only walks that frame's objects).
- Any early return of the per-frame loop, a release of everything, delete
  or `_epilog` while a request is open acks it dropped with detail 8
  (`hit_aborted`++).

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

Game functions called, all through absolute function-pointer casts
(`config/GZLE01/symbols.txt`): `fopAcM_SearchByID` 0x800241C0,
`mDoExt_McaMorf::play` 0x8001300C, `::calc` 0x800132E8 and `::setAnm`
0x80012D78, `dRes_control_c::getRes` 0x8006F208 (`dRes_getRes_byIdx` in
wwapi.h, with `mObjectInfo` 0x803E0BC8), `dr_matrix_set` 0x80022460,
`PSMTXCopy` 0x8030D0C8, `cM3dGCyl::SetC` 0x80251D88, `cM3dGSph::SetC`
0x8025238C, `cCcS::Set` 0x80242D04, `dCcD_GObjInf::ClrTgHit` 0x800AB958
and `::ClrAtHit` 0x800AB7C4, `dCcD_Sph::Set` 0x800ABFC0 and `::StartCAt`
0x800AC02C, `__ct__9dCcD_SttsFv` 0x80100EB8, `dCcD_Stts::Init`
0x800AB3DC, `cM_ssin` 0x800F0054, `cM_scos` 0x800F0038. Data read:
`calc_mtx` 0x803F63A0, `mCurStage.mName` 0x803C9D3C, `mStayNo`
0x803F6A78, `mNextStage.mEnable` 0x803C9D54, `mPlayerInfo[0].mpPlayer`
0x803CA74C, `mpPlayerPtr[0]` 0x803CA754, `__vt__8dCcD_Sph` 0x803899D0
(compared, never called).

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
`_prolog`/`_epilog`/`_unresolved`/`g_profile_WWREL` with section and
offset, and the `.text` size, and fails on an undefined symbol (the game
would route the call to `_unresolved`), an `R_PPC_REL32`, a small-data
section, or a missing symbol. `scripts/extract_blob.py` repeats those
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
pointer; and `R_PPC_REL24` for `rel_main.c`'s four calls into
`enemy_mirror.c` (a `bl` between objects; the converter and `OSLink` apply
them). Calls within one unit are resolved by the compiler; game calls go
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
