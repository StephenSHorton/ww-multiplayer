// enemy_mirror.h: the Moblin mirror's state and entry points (enemy_mirror.c,
// design: ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-s2-v2-final.txt "rel_side").
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

// One mirrored slot (the block's slot i, inject/include/wwenemy.h).
typedef struct {
    u32  pid;           // +0x00 the block's pid for this slot (0 = empty)
    u32  apid;          // +0x04 the pid adopted (the actor whose pause bit we own)
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

// create: zero the state and remember it for the epilog.
void enemy_mirror_init(enemy_mirror_state* s);
// execute, every frame of the module actor: the whole per-frame loop.
void enemy_mirror_frame(enemy_mirror_state* s, u32 frame);
// delete: release every adopted Moblin and forget the state.
void enemy_mirror_delete(enemy_mirror_state* s);
// _epilog: release_all on the live state if delete never ran.
void enemy_mirror_unlink(void);
// The static At sphere image at WWENEMY_SPH_OFF, built once per link from
// a live Moblin's mHeadSph with every vptr pointed at the DOL's tables:
// 1 when it is ready, 0 when the image's vptr fingerprint failed. The
// host's v2 submit and the client's remote-Link apply (remote.c) share it.
int enemy_mirror_sph_image(enemy_mirror_state* s, void* a);

#endif
