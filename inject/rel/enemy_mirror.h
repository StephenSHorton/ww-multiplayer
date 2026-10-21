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
// class has none. Offsets are u16 (every class here is < 0x10000 bytes).
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
    char arc[4];        // +0x08 the archive name, NUL-padded ("Mo2", "Bk", "CC")
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
    u16 pad;            // +0x42
} em_kind;              // 0x44
typedef char em_check_kind_size[(sizeof(em_kind) == 0x44) ? 1 : -1];

// em_ext slot entry: what em_consume caches per block slot.
#define EM_XF_ATTN        0x01  // flags_ext bit0: the streamed ATTN bit (BK flags2 b3, CC b5)

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
    em_kind_ovr ovr[WWENEMY_ANNEX_KINDS];       // +0x1A
    u8  invuln_wait[WWENEMY_ANNEX_KINDS];       // +0x26 0 = kind->invuln_wait
    u8  ovr_pad;        // +0x29
    u8  spare[2];       // +0x2A
} em_ext;               // 0x2C
typedef char em_check_ext_size[(sizeof(em_ext_slot) == 0x06 && sizeof(em_kind_ovr) == 0x04 && sizeof(em_ext) == 0x2C &&
                                __builtin_offsetof(em_ext, kind_mask) == 0x18 && __builtin_offsetof(em_ext, ovr) == 0x1A &&
                                __builtin_offsetof(em_ext, invuln_wait) == 0x26 && __builtin_offsetof(em_ext, spare) == 0x2A) ? 1 : -1];

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
