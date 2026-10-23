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
#define EM_XF_MT_BALL     0x02  // flags_ext bit1 (batch C MT-4): the host's Magtail is curled (annex aux[0] mode EMK_MT_MODE_BALL)
// kind_bits bit4 (batch C MT-4): the copy was handed back curled and the
// slot watches it for its own death (em_mt_custody_watch). It belongs to
// the pid the slot named at that handback: em_consume clears it when the
// slot's pid changes, so a reassigned slot never watches its new actor.
#define EM_XB_MT_CUSTODY  0x10
// Batch D (R-D6), MT's other kind_bits (BB's EM_XB_BB_* use the same
// values, enemy_mirror.c; a slot holds one kind): the builders' fingerprint
// was checked for this pid (em_mt_fp_ok, before the adoption it gates;
// em_consume drops both bits on a pid change, em_adopt keeps them), and
// matched; em_consume took a sample that moved (D9: without it the body
// step holds EMK_MT_DIVE 1, so a stream gap does not compress the ring;
// zeroed at adopt).
#define EM_XB_MT_FP_DONE  0x04
#define EM_XB_MT_FP_OK    0x08
#define EM_XB_MT_FRESH    0x20
// The bits that belong to one pid (em_consume drops them on a pid change).
#define EM_XB_MT_PID_BITS (EM_XB_MT_CUSTODY | EM_XB_MT_FP_DONE | EM_XB_MT_FP_OK)

typedef struct {
    u16 proc;           // +0x00 the slot's proc (WWENEMY_SL_PROC_OFF) at the last consume, 0 none
    u8  kind;           // +0x02 EMK_KIND_* of that proc, EMK_KIND_NONE without a row
    u8  cc_saved_bits;  // +0x03 CC: the copy's own mCyl At/Tg/Co Set bits at adopt (bit0 At, bit1 Tg, bit2 Co), restored at handback
    u8  flags_ext;      // +0x04 EM_XF_*
    u8  kind_bits;      // +0x05 (was pad) the adopted copy's own per-kind bits, not block-derived: BB EM_XB_* (enemy_mirror.c), MT EM_XB_MT_* (above; CUSTODY cleared on a pid change); zeroed at adopt
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
    u16 kind_mask;      // +0x18 the consumed block's kind_mask, bits 0..15 (layout 4, D5: was u8 + pad; bit 8 is MT)
    em_kind_ovr ovr[WWENEMY_ANNEX_KINDS];       // +0x1A ids < WWENEMY_ANNEX_KINDS only: never index it with an id >= 3 (R-1)
    u8  invuln_wait[WWENEMY_ANNEX_KINDS];       // +0x26 0 = kind->invuln_wait
    u8  ovr_pad;        // +0x29
    u8  spare[2];       // +0x2A [EM_X_HA_KIND] (enemy_mirror.c), [EM_X_KNOB_OK]
} em_ext;               // 0x2C
// em_ext.spare[1] (layout 4, R-3): 1 when the knob table at WWENEMY_KNOB_OFF
// validated at the last consume (em_knob_validate), else 0. Every knob read
// is live at the point of use and gated on it; nothing else is cached.
#define EM_X_KNOB_OK      1
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
// directly for ids 3..6; layout 4 (batch C R-4): the block byte
// WWENEMY_HKD_HI_OFF + (id - 7) for ids 7..14, the knob table for 15.
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

// --- Batch C: layout 4 (enemy-mirror-batch-c.json layout4, R-1..R-5) -------
// Defined in enemy_mirror.c and called only from there; declared here so
// the contract names them (ww-mp-batchc CONTRACT.md). Every new read is
// live at the point of use; MO2..TN stay byte-identical with an absent or
// invalid knob table and all hook bytes 0.
//
// R-3, em_consume after the annex cache: knob_ok = knob_gen (u16 block
// +WWENEMY_KNOB_GEN_OFF) != 0 && the table's gen_head == gen_tail ==
// knob_gen && its layout == WWENEMY_LAYOUT && its count >= EMK_KIND_COUNT.
// Stores knob_ok in x->spare[EM_X_KNOB_OK]; when valid, writes knob_gen to
// block +WWENEMY_KNOB_SEEN_OFF (the echo Go compares). Returns knob_ok.
int em_knob_validate(em_ext* x);
// R-4, em_hkd's new arms: ids 0..2 x->ovr (unchanged), 3..6 the annex HKDX
// bytes (unchanged), 7..14 the block byte WWENEMY_HKD_HI_OFF + id - 7 (live,
// inside the seq span: no knob gate), 15 the table's ovr[15].hook only when
// knob_ok, else 0. em_quiet_frames, em_tg_hit_up and em_invuln_wait gain,
// for ids WWENEMY_KNOB_ID_MIN..15, the table value when knob_ok and nonzero,
// before their compiled fallbacks (em_tg_hit_up: TN's 0x1FB stays first for
// id 6, the table's row 6 tg is not read). Helpers the three call:
u8 em_knob_quiet(const em_ext* x, const em_kind* k);     // 0 = none
s16 em_knob_tg_hit_up(const em_ext* x, const em_kind* k); // 0 = none
u8 em_knob_invuln(const em_ext* x, const em_kind* k);    // 0 = none

// --- Batch C: the ambush Bokoblin (ambush_cleanup, R-6..R-9) ---------------
// One unit with Go's ambushType/ambushRestore deletion (atomic, D8).
// R-6: em_bk_gate takes mType {0, 1, 2, 3, 4} unconditionally (no hook
// bit refuses 2/3). R-8, em_bk_attach while sl->tgt_flags & SF_HIDDEN and
// !(hkd & EMK_BK_HKD_AMBUSH): instead of em_bk_hand, the boko's model base
// TR is the translation-only (-EMK_BK_STICK_PARK, -EMK_BK_STICK_PARK, 0)
// identity, and the em_bk_drop test is skipped (d_a_bk.cpp:4219-4227).
void em_bk_ambush_park(void* boko);
// R-9, em_handback's BK case, before its type4 (1,-1) / (0,0) write: when
// !(hkd & EMK_BK_HKD_AMBUSH), the copy's u8 EMK_BK_DRAW_GATE ==
// EMK_BK_DRAW_GATE_POT and its mType is EMK_BK_TYPE_AMBUSH_JUMP/LEAP:
// dr.mAction EMK_BK_ACTION_TUBO, dr.mMode 0, s16 EMK_BK_M0300 0, s16
// EMK_BK_M0310 0, actor scale 1.0 (x, y, z), EMK_BK_INVULN untouched; the
// caller then skips the bck_wait setAnm (tubo_wait owns the anim). Returns
// 1 when it wrote (keyed on the copy's own m02BA, never on slot flags).
int em_bk_ambush_handback(void* a, const em_kind* k, u8 hkd);

// --- Batch C: Kargaroc BB (R-10..R-18; "bb") -------------------------------
// hkd is em_hkd(x, k) (block byte WWENEMY_HKD_HI_OFF + 0); bits EMK_BB_HKD_*.
// xs->kind_bits holds the slot's EM_XB_BB_* (enemy_mirror.c), zeroed at adopt.
// R-13, em_validate's EMK_KIND_BB case (0 = ERR_GATE): type 5/6, rider
// 0x2DF != 0, dead 0xC7E != 0, knockback f32 0x348 > 0.01, and (before
// adoption only) 0x2F2 != 0 while the host slot is not SF_HIDDEN. No health
// term (em_die_quiet zeroes it; the em_tn_gate rule), no ice (em_adopt's
// ERR_ICE).
int em_bb_gate(const em_slot* sl, void* a, const em_kind* k);
// R-11, em_adopt's BB case: EMK_BB_M366/M368 zeroed, the tail seed armed
// (after the first calc). em_adopt seeds cur_angle from current.angle.y and,
// by EMK_CAP_TILT_CUR, cur_tilt from current.angle.x.
void em_bb_adopt(const em_slot* sl, em_ext_slot* xs, void* a);
// R-11, em_base_matrix's EMK_MTX_TRYXZ case unless EMK_BB_HKD_MTX: the
// d_a_bb.cpp:2313-2322 matrix from the copy's current.angle (x = cur_tilt,
// y = cur_angle, z = the consumed slot z) into m; the caller copies it to
// the morf model's base TR with setBaseScale(actor.scale).
void em_tryxz_matrix(const em_slot* sl, void* a, f32 (*m)[4]);
// Behind EMK_BB_HKD_BEAK, in em_base_matrix's TRYXZ case before the matrix
// and McaMorf_calc: zero EMK_BB_HEAD_ROT[0..10] and s16 EMK_BB_BEAK (the
// copy's paused execute no longer re-zeroes them, d_a_bb.cpp:2410-2412).
void em_bb_pre_calc(void* a);
// R-14, em_slot_frame right after McaMorf_calc, dying frames too, unless
// EMK_BB_HKD_TAIL: AA8[0] = BD4[1]; the seed AA8[1..9] = BD4[1], B5C[0..9] =
// 0 on the first frame after adopt and on the host's hidden->shown edge;
// the fingerprint once per adopt (a miss bumps WWENEMY_FP_MISS_OFF); then
// with EXTRA clear ground_h = -1e9 and tail_control(a) when it matched, else
// the collapsed tail (AA8[0..9] = BD4[1], B5C = 0), which a dying copy also
// takes with EXTRA clear.
void em_bb_tail(const em_slot* sl, em_ext_slot* xs, void* a, u8 hkd, int dying);
// R-14 c: 1 when exec = *(*(a + EMK_AC_SUB_METHOD) + EMK_METHOD_EXECUTE)
// and tc = exec - EMK_BB_TEXT_EXEC + EMK_BB_TEXT_TAIL are inside MEM1 and
// exec[0..3], tc[0..3] and tc[EMK_BB_TAIL_BLR_OFF / 4] equal EMK_BB_FP_*.
int em_bb_fp_ok(const void* a);
// R-15, em_kind_fields' BB case: u8 EMK_BB_DRAW_GATE = 0 while the host
// slot is not SF_HIDDEN; while HIDDEN the copy's own byte stays.
// R-16: em_hit_dying's BB case (health <= 0, EMK_BB_DEAD, ice shrink) and
// em_hit_shrink_ok returning 0 for BB need no prototype.
// R-17, em_handback's BB case unless EMK_BB_HKD_HANDBACK: STATE =
// EMK_BB_STATE_ATTACK, ATTACK_TIMER = EMK_BB_ATTACK_SEED, M35D, M2F1, M2F0,
// KNOCK, M34C, C58, HEAD_ROT[] and BEAK = 0, both Tg spheres' hits cleared,
// then the tail seed; never writes EMK_BB_DRAW_GATE.
void em_bb_handback(void* a);
// R-18, em_extra_anm's BB case unless EMK_BB_HKD_EXTRA: u8 EMK_BB_EYE_BTP = 0.
void em_bb_extra(void* a);

// --- Batch D: Magtail MT (R-D6, D8-D10; "magtail") -- THE FIRST CUT ---------
// Batch C's cut row (archive/enemy-batch-c/mt-row-restore.patch, the
// em_mt_* section of enemy_mirror.c) restored in rel/em_mt.c without
// em_mt_matrix and em_mt_colliders: the copy's body is its own retail
// body_control1/2, called through em_mt_fp_ok's fingerprint. Everything MT
// is in these functions, under #if EMK_WITH_MT, and the dispatch lines that call them
// (and EM_XF_MT_BALL, EM_XB_MT_* above); the cut (EMK_WITH_MT 0,
// EMK_KIND_COUNT 8) drops this block's code, those lines, EM_ROW(MT) and
// the MT section of wwenemy_kinds.h. hkd is em_hkd(x, k) (block byte
// WWENEMY_HKD_HI_OFF + 1).
// MT-1, em_validate's MT case (0 = ERR_GATE): EMK_MT_APPEAR != 0, mode not
// in {MOVE, FIGHT, PINNED}, EMK_MT_DEATH != 0, EMK_MT_KILLED != 0, ice
// shrink running, actor_status CARRY. em_validate also refuses while the
// host is curled (EM_XF_MT_BALL) unless EMK_MT_HKD_CUSTODY, and (batch D)
// when em_mt_fp_ok is 0.
int em_mt_gate(void* a, const em_kind* k);
// R-D6, once per adopt (EM_XB_MT_FP_DONE caches it, EM_XB_MT_FP_OK the
// answer): 1 when exec = *(*(a + EMK_AC_SUB_METHOD) + EMK_METHOD_EXECUTE),
// body_control1 and body_control2 (EMK_MT_TEXT_*) are in MEM1 and their
// EMK_MT_FP_* words and blrs match; a miss bumps WWENEMY_FP_MISS_OFF
// (saturating, shared with BB) and the adoption is refused.
int em_mt_fp_ok(void* a, em_ext_slot* xs);
// MT-1 adopt: clear EMK_MT_BLUR, every sphere's hit, and At Set (bit 0 of
// the At SPrm) on the 8 body spheres once; translate the ring and chain by
// the snap delta (dx, dy, dz) so the body does not whip. f470 stays the
// copy's own.
void em_mt_adopt(void* a, f32 dx, f32 dy, f32 dz);
// em_consume_owned's MT case: every consume slot +0x18 EMK_MT_WIGGLE (off
// is the slot's block offset); with the annex matched (annex != 0) aux[0]
// mode/action (MOVE, FIGHT, PINNED only), its edge first (em_mt_edge,
// unless EMK_MT_HKD_CHAIN); aux[1] the ball timer, aux[3] shape.z; EXTRA
// clear: phase (flags2) and colour frames (aux[2]). aux is the slot's
// annex offset (mailbox-relative). em_consume itself sets xs->kind_bits
// EM_XB_MT_FRESH when the slot's position or angle moved.
void em_mt_consume(const em_slot* sl, void* a, u32 off, u32 aux, int annex, u8 hkd);
// R-D6 the mode edges, unless EMK_MT_HKD_CHAIN (wwenemy_kinds.h "The mode
// edges"): MOVE -> FIGHT copies the ring into the chain (mt.rel
// 0x21D8-0x2218), FIGHT -> MOVE reseeds the ring from the chain (mt.rel
// 0x1404-0x1524) and writes EMK_MT_RING_IDX 0. from and to are modes.
void em_mt_edge(void* a, u8 from, u8 to);
// R-D6 em_slot_frame's step 5 for EMK_MTX_MT (in em_base_matrix's place,
// which has no xs) unless EMK_MT_HKD_MTX, the body step, only with
// EM_XB_MT_FP_OK: pos +0x1F8 and shape +0x20C/E/10 (the host's post-sway
// values) are the smoothed slot's; then EMK_MT_LATCH 0, EMK_MT_HIT_COOL 0,
// EMK_MT_SWAY_FRAME += 1; mode MOVE: EMK_MT_DIVE 1 when EM_XB_MT_FRESH is
// clear or flags2 has EMK_MT_F2_DIVE, else 0 (D9), then body_control1(a);
// FIGHT: EMK_MT_BITE 0, then body_control2(a); PINNED (D8): no builder (the
// matrices hold). Clears EM_XB_MT_FRESH. dying (the quiet death): no
// builder, each segment model's base scale = actor scale * scale.
void em_mt_body_step(const em_slot* sl, em_ext_slot* xs, void* a, u8 hkd, f32 scale, int dying);
// R-D6/D8 em_colliders' EMK_COLL_MT case unless EMK_MT_HKD_COLL, after the
// body step (the builders Set the 9 spheres themselves): At Set cleared on
// the 8 body spheres (after body_control2); in PINNED At and Co cleared on
// the 8 and the eye kept Set with Tg (body_control5's effect, mt.rel
// 0x2EFC-0x2F30), and, no builder having run, the 9 spheres entered into
// cCcS by the REL as em_colliders' other cases do (centres held); the
// Shield bit onto body sph[0] from EMK_MT_F2_SHIELD; a
// pending code withholds its collider (code 2: all 8) at the builders' own
// disabled R -200 (the builders have already entered them). off 1: the
// slot's colliders are off (FLAG_COLLIDERS clear or SF_HIDDEN), every
// sphere at R -200 and none entered. em_hits' MT case
// (em_mt_hit_collider): eye -> EMK_HIT_HEAD, the first body sphere hit ->
// EMK_HIT_TG (every other sphere's hit cleared), a thrown Magtail's hit
// dropped.
void em_mt_coll_post(const em_slot* sl, void* a, int off);
int em_mt_hit_collider(em_slot* sl, void* a);   // 0 none, EMK_HIT_HEAD, EMK_HIT_TG
void em_mt_clr_hits(void* a, int eye, int body);
// Host em_hit_center's MT case: collider 2 at body sph[EMK_MT_HIT_CENTER_SEG];
// em_hit_vulnerable's: the hit cooldown EMK_MT_HIT_COOL is 0.
void em_mt_hit_center(void* a, cXyz* c);
int em_mt_vulnerable(void* a);
// MT-4, em_handback's MT case: hits and blur cleared; when the host curled
// (EM_XF_MT_BALL) and EMK_MT_HKD_CUSTODY is clear, the custody BALL (mode
// BALL, action 0, ball timer aux[1], phase 1 and frames aux[2], the streamed
// health, eye HP 0, Shield off) and xs->kind_bits EM_XB_MT_CUSTODY. slot is
// the block slot; same is 0 when the release is a reassignment (the slot
// already names another actor, whose state xs and the slot now carry): no
// custody then.
void em_mt_handback(void* a, em_ext_slot* xs, u8 hkd, int slot, int same);
// MT-4, em_slot_frame's refusal of an MT slot: while EM_XB_MT_CUSTODY (set
// for this pid: em_consume drops it on a pid change), the slot reports the
// copy as its actor, and its death (EMK_MT_DEATH >= 2, mode DEAD or ice
// shrink) once as a hit entry with EMK_HIT_MT_KILL. Returns 1 while the
// slot watches one (its refusals then skip revalidate_failures).
int em_mt_custody_watch(em_slot* sl, em_ext_slot* xs, void* a, int slot);
// MT-4 host, em_hit_apply's code-4 branch: EMK_MT_DEATH = 2 only if its
// copy is still BALL with EMK_MT_DEATH 0 (returns 0 = stale).
int em_mt_kill_apply(void* a);
// MT-5, before both fopAcM_delete sites (em_release and em_die_quiet)
// unless EMK_MT_HKD_RESPAWN: u8 EMK_MT_KILLED = 1 when EMK_MT_RESPAWN_BIT
// is set (daMt_Delete then re-creates it at home).
void em_mt_mark_respawn(void* a, u8 hkd);

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
