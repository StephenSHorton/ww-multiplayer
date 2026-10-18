// The multiplayer mod's part of the mailbox (Phase E3, split from
// mailbox.h): its slot counts and the actor-puppet slot. mailbox_core.h
// includes this before its Mailbox struct, which places these fields at
// their fixed offsets; nothing here moves an offset.
#ifndef MAILBOX_MP_H
#define MAILBOX_MP_H

#include "wwapi.h"

// Up to this many remote players can be mirrored as puppet actors.
// Pick a humble number; each slot adds one actor allocation.
#define MAX_PUPPETS 4

// Up to this many remote players can be rendered as full Link puppets
// via the pose-feed pipeline (shadow_mode 5). Each slot allocates its
// own J3DModel + 42-joint pose buffer (~2 KB each) from ArchiveHeap +
// GameHeap.
//
// Unblocked 2026-04-20: J3DModel create flag 0x80000 was sharing the
// material display list across instances — every J3DModel::entry()'s
// makeDisplayList() patched the same shared buffer, so N>1 all
// rendered the last-submitted pose. Flipping the flag to 0 in
// src/mp/minilink.c's mDoExt_J3DModel__create call makes createMatPacket
// allocate private per-instance DLs (J3DModel.cpp:296-309). N>1 now
// works. Bump in lockstep with MAILBOX_POSE_SLOT_CAP if needed.
#define MAX_REMOTE_LINKS 2

// Layout-reserved slot capacity for the per-slot pose arrays in the
// Mailbox struct below. Keep this FIXED so Go's hardcoded mailbox
// offsets (main.go: mailboxPoseBufPtr etc.) stay correct regardless of
// MAX_REMOTE_LINKS. Runtime only iterates the first MAX_REMOTE_LINKS
// slots; the rest stays zero. Bumping MAX_REMOTE_LINKS to 2 (once the
// shared-J3DModelData blocker is unlocked) is free; bumping past
// MAILBOX_POSE_SLOT_CAP needs a coordinated Mailbox-layout + Go-offset
// revision.
#define MAILBOX_POSE_SLOT_CAP 2

// Per-slot state. Size is 0x20 bytes (nice round hex).
//   active   — Go sets 1 when a remote player owns this slot, 0 to
//              release it. While it is 1, C spawns the actor, and
//              respawns it after the game deletes it or after a stage
//              change. When it goes to 0, C deletes the actor
//              (puppet_reap). A stage change, Link going away or a new
//              Link actor deletes every slot's actor whatever active
//              is; slots still at 1 respawn in the new stage.
//   actor_ptr — C publishes the live actor pointer after SearchByID
//              succeeds so Go can read/poke the actor if needed
//              (e.g. unhide-puppet writes mSwitchNo for KAMOME).
//   pos_*     — big-endian f32s Go writes each tick (already lerp-
//              smoothed if puppet-sync applies). C copies into the
//              actor's current.pos (+0x1F8) every frame.
//   rot_*     — big-endian s16s. C copies into both current.angle
//              (+0x204) and shape_angle (+0x20C) every frame.
typedef struct {
    u32 actor_ptr;       // +0x00
    u32 active;          // +0x04
    f32 pos_x;           // +0x08
    f32 pos_y;           // +0x0C
    f32 pos_z;           // +0x10
    s16 rot_x;           // +0x14
    s16 rot_y;           // +0x16
    s16 rot_z;           // +0x18
    u16 _pad0;           // +0x1A
    u32 _reserved;       // +0x1C — keep zero; room for anim state later
} Puppet;

#endif
