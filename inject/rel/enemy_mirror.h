// enemy_mirror.h: the enemy mirror's state and entry points (enemy_mirror.c,
// design: ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-s2-v2-final.txt "rel_side"; layout 3, the kind table and em_ext:
// enemy-mirror-general.json "block_layout".rel_state).
//
// The state lives inside the module actor (rel_main.c's wwrel_actor embeds
// enemy_mirror_state), never in .bss across frames: the game allocates the
// actor with the profile's mSize and frees it with the actor, and the module
// is unlinked when the last actor of its proc is deleted. The only state
// outside the actor is the host's two static collision objects in the
// mailbox slack (WWENEMY_SPH_OFF, WWENEMY_STTS_OFF): the sphere image is
// rebuilt lazily after a relink (sph_built below), the stts is re-Init'd
// from the current Link on every request.
#ifndef WW_ENEMY_MIRROR_H
#define WW_ENEMY_MIRROR_H

#include "wwapi.h"
#include "wwenemy.h"
#include "wwenemy_kinds.h"

// One mirrored slot (the block's slot i, inject/include/wwenemy.h).
typedef struct {
    u32  pid;           // +0x00 the block's pid for this slot (0 = empty)
    u32  apid;          // +0x04 the pid adopted (the actor whose pause bit we own); unadopted under a dying latch (quiet or legacy), the dead pid
    u32  actor;         // +0x08 the actor resolved this frame (0 = none), for status
    cXyz cur;           // +0x0C the smoothed position written to the actor
    s16  cur_angle;     // +0x18 smoothed angle.y
    s16  cur_tilt;      // +0x1A smoothed tilt (mDamageReaction.m48C.x)
    cXyz tgt;           // +0x1C the last accepted target (NaN/range-checked)
    s16  tgt_angle;     // +0x28
    s16  tgt_tilt;      // +0x2A
    u8   tgt_flags;     // +0x2C WWENEMY_SF_* of the last consumed block
    u8   state;         // +0x2D WWENEMY_STATE_*
    u8   pause_prev;    // +0x2E the pause byte (proc+0x0B) before adopt
    u8   last_err;      // +0x2F WWENEMY_ERR_*
    u16  frames;        // +0x30 frames mirrored (saturating)
    u16  hit_seq;       // +0x32 per-slot hit counter (S3)
    u8   adopted;       // +0x34 1 while the pause bit is ours
    u8   snap;          // +0x35 snap on the next write (set at adopt)
    u8   hit_withheld;  // +0x36 S3: collider kept out of cCcS while its hit entry is pending (0 none, 1 head, 2 tg)
    u8   hit_cooldown;  // +0x37 S3: frames before the next hit entry
    u16  recently_dead; // +0x38 the dying latch: frames left before this slot may be adopted again
    // S2: the streamed animation
    u8   weapon_prev;   // +0x3A the copy's own mbHasInnateWeapon (+0xDD4) at adopt, restored at release
    u8   tgt_bck;       // +0x3B the slot's bck index (WWENEMY_BCK_NONE = unresolved)
    u8   tgt_attr;      // +0x3C the slot's J3DFrameCtrl attribute (0xFF unknown)
    u8   tgt_anm_seq;   // +0x3D the slot's anm_seq (bumped by Go at each host restart)
    u8   tgt_morf;      // +0x3E the slot's morf (blend frames)
    u8   tgt_flags2;    // +0x3F WWENEMY_SF2_*
    u8   tgt_blur_shape;// +0x40
    u8   anm_state;     // +0x41 WWENEMY_ANM_*
    u8   anm_bck;       // +0x42 the index setAnm'd on the copy (0xFF none)
    u8   anm_attr;      // +0x43 the attribute setAnm'd with
    u8   anm_seq_seen;  // +0x44 the anm_seq the last setAnm consumed
    u8   anm_retry;     // +0x45 frames before a failed index may be retried
    u8   anm_changes;   // +0x46 saturating setAnm count
    u8   anm_failed_bck;// +0x47 the index getRes failed on
    f32  tgt_blur_scale;// +0x48 the slot's blur scale (bad floats -> 1.0)
    u32  anm_sample_seq;// +0x4C the block sample_seq last seen
    u32  anm_sample_frame; // +0x50 the WWREL frame at which that sample was first consumed
    f32  anm_frame;     // +0x54 that sample's morf frame
    f32  anm_rate;      // +0x58 that sample's morf rate
} em_slot;              // 0x5C

// The host's hit applier (v2) state machine.
#define EM_HA_IDLE        0
#define EM_HA_WAIT_INVULN 1
#define EM_HA_SET         2

typedef struct {
    u32 last_seq;       // +0x00 the block seq last consumed (0 = none)
    u32 stale_frames;   // +0x04 frames since, for the 90-frame watchdog
    u32 epoch_seen;     // +0x08 the block epoch the slots belong to
    u32 hit_apply_ack;  // +0x0C host role: the last hit_apply seq taken (the block's ack word is written with the result)
    u32 ha_seq;         // +0x10 host role: the consumed hit_apply request
    u32 ha_pid;         // +0x14
    u16 ha_setid;       // +0x18
    u8  ha_collider;    // +0x1A
    u8  count;          // +0x1B slots in use (<= WWENEMY_MAX_SLOTS)
    u8  flags;          // +0x1C WWENEMY_FLAG_* of the last consumed block
    u8  role;           // +0x1D WWENEMY_ROLE_*
    s8  room;           // +0x1E the block's room
    u8  stale_released; // +0x1F 1 after the watchdog released everything, until a new seq
    u8  stage[8];       // +0x20 the block's stage name, NUL-padded
    u8  canary[4];      // +0x28 pause_reasserts, anm_unresolved, revalidate_failures, watchdog_releases
    u8  canary2[4];     // +0x2C anm_res_null, anm_range, hit_nocross, hit_aborted
    u8  ha_state;       // +0x30 EM_HA_*
    u8  ha_flags;       // +0x31 WWENEMY_HAF_* of the request
    u8  ha_frames;      // +0x32 REL executes from the first Set to the result (saturating)
    u8  ha_sets;        // +0x33 cCcS::Set calls issued for the request
    u8  ha_atp;         // +0x34
    u8  ha_se;          // +0x35
    u8  ha_hitmark;     // +0x36
    u8  ha_spl;         // +0x37
    u8  ha_detail;      // +0x38 WWENEMY_HD_* of the last result
    u8  sph_built;      // +0x39 1 once the static sphere image was copied and checked
    u8  ha_waited;      // +0x3A executes spent in EM_HA_WAIT_INVULN
    u8  ha_attrib;      // +0x3B remote-Link ATTRIB (remote.c): 0 none, 1 the stts names the client's body, 2 asked but no LIVE body (detail WWREMOTE_HD_NO_BODY)
    u32 ha_attype;      // +0x3C
    cXyz ha_pos;        // +0x40 the client's collider centre
    u32 ha_actor;       // +0x4C the target actor of the open request (0 = none)
    u32 stts_link;      // +0x50 the Link pointer the static stts was last Init'd with (a diagnostic: it is re-Init'd on every request)
    em_slot slots[WWENEMY_MAX_SLOTS]; // +0x54
} enemy_mirror_state;   // 0x54 + 4 * 0x5C = 0x1C4

// --- Layout 3: the kind table and the per-slot cache (enemy-mirror-general.json) ---

// One row of the kind table (wwenemy_kinds.h). enemy_mirror.c holds
// static const em_kind EM_KINDS[EMK_KIND_COUNT] in .rodata, each field
// initialised from its EMK_<KIND>_* define; 0 in an offset field means the
// class has none (KI's co_cyl/tg_cyl are spheres: EMK_CAP_SPH_COLL). Offsets are u16 (every class here is < 0x10000 bytes).
// Width per field: gate/bare u8; morf/blur_on/blur_shape/weapon (BK) s32
// or pointer; tilt/invuln/anm_gate/play_skip s16; blur_scale f32; MO2's
// weapon is s8 (its accesses sit in a kind == MO2 case).
typedef struct {
    u16 proc;           // +0x00 EMK_*_PROC
    u8  id;             // +0x02 EMK_KIND_* (the kind_mask bit)
    u8  caps;           // +0x03 EMK_CAP_*
    u8  mtx;            // +0x04 EMK_MTX_*
    u8  coll;           // +0x05 EMK_COLL_*
    u8  attach;         // +0x06 EMK_ATTACH_*
    u8  eye;            // +0x07 EMK_EYE_*
    char arc[4];        // +0x08 the archive name, NUL-padded ("Mo2", "Bk", "CC", "Ki", "AM", "AM2")
    u8  bck_first;      // +0x0C
    u8  bck_last;       // +0x0D
    u8  bck_wait;       // +0x0E the handback row (0: CC setAnms its own m320)
    u8  invuln_wait;    // +0x0F executes the hit applier waits on the invuln field
    u16 class_size;     // +0x10 the abort path's address range (ha_actor .. + class_size)
    u16 morf;           // +0x12
    u16 gate;           // +0x14 the u8 the static gate reads (mo2 mMode, bk mType, cc mBehaviorType)
    u16 dr;             // +0x16 damagereaction (EMK_CAP_DR)
    u16 dr_mode;        // +0x18
    u16 dr_action;      // +0x1A
    u16 tilt;           // +0x1C s16 the slot tilt goes to
    u16 co_cyl;         // +0x1E
    u16 tg_cyl;         // +0x20 hit collider 2
    u16 head_sph;       // +0x22 hit collider 1 (0: none)
    u16 def_sph;        // +0x24 PARRIED source (0: never parries)
    u16 head_center;    // +0x26 cXyz, collider 1's centre
    u16 def_center;     // +0x28 cXyz
    u16 invuln;         // +0x2A s16 (0: CC waits on the Tg Set bit)
    u16 ice;            // +0x2C enemyice (+EMK_ICE_SHRINK, +EMK_ICE_FREEZE)
    u16 weapon;         // +0x2E
    u16 bare;           // +0x30
    u16 blur_on;        // +0x32
    u16 blur_shape;     // +0x34
    u16 blur_scale;     // +0x36
    u16 anm_gate;       // +0x38
    u16 play_skip;      // +0x3A
    u16 sph_src;        // +0x3C the At image source (0: Link's mFanWindSph +0x49C4)
    s16 co_forward;     // +0x3E Co centre ahead along angle.y
    s16 tg_hit_up;      // +0x40 collider 2's centre height (annex override wins when non-zero)
    u8  attn_f2;        // +0x42 EMK_*_ATTN_F2: the flags2 bit that is this kind's streamed attention (D4; 0 none)
    u8  spare;          // +0x43
} em_kind;              // 0x44
typedef char em_check_kind_size[(sizeof(em_kind) == 0x44 && __builtin_offsetof(em_kind, attn_f2) == 0x42) ? 1 : -1];

// em_ext slot entry: what em_consume caches per block slot.
#define EM_XF_ATTN        0x01  // flags_ext bit0: the streamed ATTN bit (flags2 & kind->attn_f2)

typedef struct {
    u16 proc;           // +0x00 the slot's proc (WWENEMY_SL_PROC_OFF) at the last consume, 0 none
    u8  kind;           // +0x02 EMK_KIND_* of that proc, EMK_KIND_NONE without a row
    u8  cc_saved_bits;  // +0x03 CC: the copy's own mCyl At/Tg/Co Set bits at adopt (bit0 At, bit1 Tg, bit2 Co), restored at handback
    u8  flags_ext;      // +0x04 EM_XF_*
    u8  pad;            // +0x05
} em_ext_slot;          // 0x06

// One kind's annex override, cached at consume when annex_seq == seq
// (WWENEMY_ANNEX_OVR_*); 0 in a field = the compiled default.
typedef struct {
    u8  hook_disable;   // +0x00 WWENEMY_HKD_*
    u8  quiet_frames;   // +0x01 0 = EMK_QUIET_FRAMES / EMK_QUIET_FRAMES_LIGHT
    s16 tg_hit_up;      // +0x02 0 = kind->tg_hit_up
} em_kind_ovr;          // 0x04

// The layout-3 cache the keeper keeps past remote.c's swap state:
// wwrel_actor.ext at +0x5D4 (rel_main.c pins it), up to
// WWREMOTE_ACTOR_SIZE 0x600. enemy_mirror_state and em_slot are packed
// (0x1C4 / 0x5C, pinned), so everything layout 3 adds per slot lives
// here. Every block-derived field is written only in em_consume (the
// seq-protected read); enemy_mirror_init zeroes it (kinds EMK_KIND_NONE).
typedef struct {
    em_ext_slot slots[WWENEMY_MAX_SLOTS];       // +0x00
    u8  kind_mask;      // +0x18 the consumed block's kind_mask (low byte)
    u8  pad;            // +0x19
    em_kind_ovr ovr[WWENEMY_ANNEX_KINDS];       // +0x1A ids < WWENEMY_ANNEX_KINDS only: never index it with an id >= 3 (R-1)
    u8  invuln_wait[WWENEMY_ANNEX_KINDS];       // +0x26 0 = kind->invuln_wait
    u8  ovr_pad;        // +0x29
    u8  spare[2];       // +0x2A
} em_ext;               // 0x2C
typedef char em_check_ext_size[(sizeof(em_ext_slot) == 0x06 && sizeof(em_kind_ovr) == 0x04 && sizeof(em_ext) == 0x2C &&
                                __builtin_offsetof(em_ext, kind_mask) == 0x18 && __builtin_offsetof(em_ext, ovr) == 0x1A &&
                                __builtin_offsetof(em_ext, invuln_wait) == 0x26 && __builtin_offsetof(em_ext, spare) == 0x2A) ? 1 : -1];

// --- The allow batch's hooks (enemy-mirror-allow.json rel_changes; A1) -----
// Defined in enemy_mirror.c and called only from there; declared here so
// the contract names them (ww-mp-allow CONTRACT.md). x is the live em_ext,
// k the slot's row, a the adopted copy, morf its mpMorf. Each is a no-op
// for a kind it has no case for, so MO2/BK/CC stay byte-identical.
//
// R-1: hook_disable for k: x->ovr[id] for id < WWENEMY_ANNEX_KINDS, the
// annex byte WWENEMY_ANNEX_HKDX_OFF + (id - WWENEMY_ANNEX_KINDS) read
// directly for ids 3..6, else 0.
u8 em_hkd(const em_ext* x, const em_kind* k);
// R-1: the quiet-death frame count: the annex override (ids < 3 only),
// else EMK_QUIET_FRAMES_LIGHT under SF2_LIGHT, else the compiled
// EMK_<K>_QUIET_FRAMES (KI 20, AM 40, AM2 20) or EMK_QUIET_FRAMES.
u8 em_quiet_frames(const em_ext* x, const em_kind* k, u8 flags2);
// R-1: collider 2's centre height (annex override for ids < 3, else
// k->tg_hit_up) and the hit applier's invuln wait (likewise, else
// k->invuln_wait).
s16 em_tg_hit_up(const em_ext* x, const em_kind* k);
u8 em_invuln_wait(const em_ext* x, const em_kind* k);
// R-3: EMK_MTX_PLAIN into m: T(sl->cur) * Ry(sl->cur_angle) * Rx(s16
// a+0x20C) * Rz(s16 a+0x210), no scale fold (D16). em_base_matrix's case
// copies m to the morf model's base TR (+0x24) unless HKD_MTX_PLAIN.
void em_plain_matrix(const em_slot* sl, void* a, f32 (*m)[4]);
// R-7: EMK_EYE_FIXED, after McaMorf_calc (where em_cc_eye runs) unless
// HKD_EYE: KI eye = cur, attn = eye + 50; AM eye = cur + 250, attn = cur +
// 330; AM2 eye = *(a+0x2E0) - (0, 15, 0), attn = cur + 120.
void em_fixed_eye(const em_slot* sl, const em_kind* k, void* a);
// R-5 before STD (D6, D8): AM2's weak Tg Set bit from EMK_AM2_F2_WEAK_TG
// and body Co Set bit from EMK_AM2_F2_BODY_CO; AM's needle At Set bit off.
void em_colliders_pre(const em_slot* sl, const em_kind* k, void* a);
// R-5 after STD, every frame (INVULN frames included), behind HKD_EXTRA:
// the radii the class's execute sets each frame (KI Tg 40/20 and Co 80/0
// by EMK_KI_F2_LUNGE; AM eye 60, body R 80 H 300; AM2 body R 35 H 150,
// weak R 30).
void em_colliders_post(const em_slot* sl, const em_ext* x, const em_kind* k, void* a);
// R-13 client (AM, HKD_SPECIAL clear): Set mMouthSph Co-only at mMouthPos
// R 100; return 1 when its Co hit is a bomb (EMK_PROC_BOMB/BOMB2) the
// host's bomb_nomi_check would take (streamed bck in the mouth-open set,
// client Link within EMK_AM_SWALLOW_FACING of shape.y, bomb y above
// mMouthPos.y - EMK_AM_SWALLOW_DY), after fopAcM_delete'ing that bomb.
// em_hits then reports EMK_HIT_SPECIAL.
int em_am_swallow_client(em_slot* sl, const em_kind* k, void* a);
// R-13 host, in execute context from em_hit_apply for collider
// EMK_HIT_SPECIAL: if AM's mCurrBckIdx is in the mouth-open set and it is
// not ITAI with mMode >= 44, write mAction 3 / mMode 44 and return 1;
// else 0 (the caller publishes NO_CROSS). No v2 sphere is built.
int em_am_swallow_apply(void* a);
// R-9: the per-kind frame writes (em_kind_fields gains the cases): KI
// m2D4 = 0 when SF_HIDDEN is clear and m540 = 0; AM/AM2 mCurrBckIdx is
// written by em_animate on every setAnm (em_bck_index_off gives where).
u16 em_bck_index_off(const em_kind* k);
// R-10, behind HKD_EXTRA, never on a dying frame: play__14mDoExt_baseAnmFv
// on the KI btk (a+0x920) when mDamageType (a+0x905) != 0, and on the
// AM2 btk (a+0x2C0) and brk (a+0x2C4). The first cut (implementation
// batch A1 cut order).
void em_extra_anm(const em_slot* sl, const em_ext* x, const em_kind* k, void* a);

// --- The TN batch's hooks (enemy-mirror-tn.json rel_changes; B1) ----------
// Defined in enemy_mirror.c and called only from there; declared here so
// the contract names them (ww-mp-tn CONTRACT.md). x, k, a and morf as
// above; hkd is em_hkd(x, k) for the frame. Each is a no-op for a kind it
// has no case for, so MO2..AM2 stay byte-identical except where AM2's
// direct apply is named (behind AM2's HKD_SPECIAL).
//
// R-4 (em_validate's EMK_KIND_TN case; 0 = ERR_GATE): BOSS status, behaviour
// 15, dr.mAction 23 or 25, m02C1, m1518, m14F8 or m140C nonzero, a cape
// (mEquipmentType >= 4), and, as em_bk_gate does, dr.mMode <= -100 or
// dr.mAction 20..22. em_validate re-runs it every frame on an adopted copy,
// but on the copy's own fields, which stay frozen under the pause and which
// no TN hook writes: the ragdoll and dying terms refuse adoption only and
// never release a copy whose host ragdolls (p0_findings DEATH ORDER).
int em_tn_gate(void* a, const em_kind* k);
// R-5, em_adopt's tail: m1402 = 0, dr.m088[20] zeroed, m1444 = 0, the
// defence sphere R 70; unless EMK_TN_HKD_ADOPT_ANM, the armor morf
// em_set_anm'd to YKIHON1 loop (it free-runs from there).
void em_tn_adopt(void* a, const em_kind* k, u8 hkd);
// R-6, between em_base_matrix and McaMorf_calc(body), dying frames too,
// unless EMK_TN_HKD_SHIELD and only with pieces & EMK_TN_PIECE_SHIELD: the
// body model's base TR (scaled on a dying frame) into the shield morf's
// model, the shield bck from sl->anm_bck (AGUARD1/AGUARD2/AHAKAI1 the same
// index, else ATATE_ON1 loop; setAnm only on a change), play(NULL) unless
// the host skips play (EMK_TN_F2_PLAY_SKIP), McaMorf_calc(shield).
void em_tn_shield(em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd);
// R-7, after McaMorf_calc(body) where em_bk_attach runs, dying frames too:
// (a) unless EMK_TN_HKD_ATTACH, pieces &= (sl->tgt_blur_shape |
// ~EMK_TN_PIECE_MASK), and m08 = -1 for a cleared armor (part 0) or helmet
// (part 1); (b) unless ATTACH, parts with m08 >= 0 and m09 == 0 take body
// anmMtx[MUNE1 / ATAMA1 / ITEM_L1] as their base TR (part 0 the armor
// morf's model, 1 and 2 mpPartModel); (c) unless ATTACH, the armor morf's
// play(NULL) (not under PLAY_SKIP or EMK_TN_HKD_ADOPT_ANM) and calc; (d)
// unless EMK_TN_HKD_SWORD, the carried sword (em_carried_boko) at
// anmMtx[ITEM_R1] * Rz(0x8000) * T(0, 0, 85), dropped as em_bk_attach drops
// on the streamed HAS_WEAPON falling edge (not while dying); (e) unless
// ATTACH, m13E4 = the ITEM_L1 translation.
void em_tn_attach(const enemy_mirror_state* s, em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd, int dying);
// R-7 d, R-8: em_bk_boko generalised on k->id: the BOKO the row's weapon
// (BK m0B30, TN m0C34) and pid (BK m1200, TN m1410) name under the HeldBy
// guard (proc 0x1D5, ENEMY_CARRY, CARRY set); TN also needs m140C == 0.
void* em_carried_boko(void* a, const em_kind* k);
// R-8: the quiet death's sword drop for EMK_ATTACH_BK or EMK_ATTACH_TN
// (em_release, em_die_quiet's last frame), em_bk_quiet_drop generalised
// with the row's hand matrix; a TN row skips it under EMK_TN_HKD_SWORD.
void em_quiet_drop(em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd);
// R-9 amendment: the frames em_die_quiet holds scale 1 before its shrink
// (TN EMK_TN_QUIET_HOLD without SF2_LIGHT, else 0: scale 1 - f/n as
// before; em_die_quiet starts a RELEASE death's count past it);
// em_quiet_frames gains the EMK_TN_QUIET_FRAMES case.
u8 em_quiet_hold(const em_kind* k, u8 flags2);
// R-12, from em_colliders right after em_colliders_pre, unless
// EMK_TN_HKD_COLL: the Tg cylinder's GObjTg Shield bit iff pieces & ARMOR;
// the head sphere's Shield bit and purple hitmark iff pieces & HELMET, else
// Shield off and hitmark UNK1 (d_a_tn.cpp:3636-3654, no CUT_KESA test).
void em_tn_colliders_pre(const em_slot* sl, const em_kind* k, void* a, u8 hkd);
// R-14, client, in em_hits when AM2 reports EMK_HIT_HEAD with its
// HKD_SPECIAL clear: WWENEMY_HP_VERDICT | (WWENEMY_HP_BACK when
// cLib_distanceAngleS(shape.y, fopAcM_searchPlayerAngleY(a)) >=
// EMK_AM2_BACK_MIN), ORed into the pending byte with WWENEMY_HP_PENDING.
u8 em_am2_verdict(void* a);
// R-15 host: 1 when the open request takes the direct path: AM2, collider
// EMK_HIT_HEAD, WWENEMY_HAF_VERDICT, no EMK_AT_OLD_PATH bit in ha_attype,
// and AM2's own HKD_SPECIAL clear. em_hit_apply (after the code-3 branch,
// before FORCE_V1) and em_hit_tick's WAIT_INVULN arm both ask it.
int em_hit_direct_ok(const enemy_mirror_state* s, const em_ext* x, const em_kind* k);
// R-15 host: the direct apply on a vulnerable AM2: the static At sphere
// Set from the request exactly as em_hit_submit builds it (stts Init'd from
// the host Link, no em_hit_register), a zeroed CcAtInfo with mpObj = the
// sphere, cc_at_check(a, &info), then week_atari_check's tail: m2CE
// (em_am2_m2ce; 7/8 also health 0), mbIsWeakBeingHit 1, mAction ITAI,
// mMode 20; publishes HR_APPLIED_AT / HD_DIRECT (or HD_NO_LINK /
// HD_NO_SPHERE as em_hit_submit does).
void em_hit_direct(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a);
// R-15: m2CE for the request's At type (EMK_AT_*) and WWENEMY_HAF_CRIT.
u8 em_am2_m2ce(u32 attype, u8 ha_flags);
// R-16, on the direct path only (em_hit_apply's direct branch and the
// WAIT_INVULN retry before em_hit_direct): 1 when vanilla would skip
// week_atari_check (d_a_am2.cpp:846): carried, or MAHI with mMode
// EMK_AM2_MODE_THROWN. em_hit_vulnerable is NOT changed, so the sphere
// path (AM2's HKD_SPECIAL set, or no verdict) stays byte-identical.
int em_am2_direct_refused(void* a);

// create: zero the state and the cache, and remember both for the epilog.
void enemy_mirror_init(enemy_mirror_state* s, em_ext* x);
// execute, every frame of the module actor: the whole per-frame loop.
void enemy_mirror_frame(enemy_mirror_state* s, em_ext* x, u32 frame);
// delete: release every adopted copy (a quiet-dying one is deleted, not
// handed back) and forget the state.
void enemy_mirror_delete(enemy_mirror_state* s, em_ext* x);
// _epilog: release_all on the live state (g_live, g_live_ext) if delete
// never ran.
void enemy_mirror_unlink(void);
// The kind table's row for proc, NULL when there is none (a linear scan of
// EM_KINDS). remote.c uses it for rl_mirror_owner and HF_NOT_MIRROR.
const em_kind* em_kind_of(u16 proc);
// The placement key of actor a: FNV-1a-32 over EMK_PKEY_LEN bytes read
// from the actor (proc, setID, params, home room, home.pos bits), 0 -> 1.
// Must equal mods/enemysync PKey (test vector EMK_PKEY_TV_*).
u32 em_pkey(const void* a);
// The static At sphere image at WWENEMY_SPH_OFF, built once per link with
// every vptr pointed at the DOL's tables: 1 when it is ready, 0 when the
// image's vptr fingerprint failed. The host's v2 submit and the client's
// remote-Link apply (remote.c) share it. Layout 3: _image takes the
// source from em_kind_of(proc of a)->sph_src, or Link's mFanWindSph
// (+0x49C4) when that is 0; _image_at also zeroes the image's Tg/Co
// callbacks (+0x9C, +0xE4).
int enemy_mirror_sph_image(enemy_mirror_state* s, void* a);
int enemy_mirror_sph_image_at(enemy_mirror_state* s, const void* sph);

#endif
