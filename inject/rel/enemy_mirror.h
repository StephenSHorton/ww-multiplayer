// enemy_mirror.h: the Moblin mirror's state and entry points (enemy_mirror.c,
// design: ww-multiplayer-handoff/design/host-authoritative-prototype.md).
//
// The state lives inside the module actor (rel_main.c's wwrel_actor embeds
// enemy_mirror_state), never in .bss across frames: the game allocates the
// actor with the profile's mSize and frees it with the actor, and the module
// is unlinked when the last actor of its proc is deleted.
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
    u16  _pad;          // +0x3A
} em_slot;              // 0x3C

typedef struct {
    u32 last_seq;       // +0x00 the block seq last consumed (0 = none)
    u32 stale_frames;   // +0x04 frames since, for the 90-frame watchdog
    u32 epoch_seen;     // +0x08 the block epoch the slots belong to
    u32 hit_apply_ack;  // +0x0C host role: the last hit_apply seq handled
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
    em_slot slots[WWENEMY_MAX_SLOTS]; // +0x2C
} enemy_mirror_state;   // 0x2C + 4 * 0x3C = 0x11C

// create: zero the state and remember it for the epilog.
void enemy_mirror_init(enemy_mirror_state* s);
// execute, every frame of the module actor: the whole per-frame loop.
void enemy_mirror_frame(enemy_mirror_state* s, u32 frame);
// delete: release every adopted Moblin and forget the state.
void enemy_mirror_delete(enemy_mirror_state* s);
// _epilog: release_all on the live state if delete never ran.
void enemy_mirror_unlink(void);

#endif
