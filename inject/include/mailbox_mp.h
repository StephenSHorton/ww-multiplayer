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
// own J3DModel (ArchiveHeap) + 42-joint pose buffer (2,016 B, GameHeap)
// and its tunic image (TUNIC_SLOTS, mailbox_core.h).
//
// Unblocked 2026-04-20: J3DModel create flag 0x80000 was sharing the
// material display list across instances — every J3DModel::entry()'s
// makeDisplayList() patched the same shared buffer, so N>1 all
// rendered the last-submitted pose. Flipping the flag to 0 in
// src/mp/minilink.c's mDoExt_J3DModel__create call makes createMatPacket
// allocate private per-instance DLs (J3DModel.cpp:296-309). N>1 now
// works.
//
// 3 since WWMB_LAYOUT 3 (2026-10-25): F1 (docs/05, a second mini-Link
// froze both games) was the shared eye-fix packets, and per-slot packets
// cleared it live on cea51fe (two mini-Links drawn, 5+ minutes, two stage
// hops). Three remotes is four players. Past 3 needs relay scoping first
// (docs/05 F1, "Players past four") and another layout revision.
#define MAX_REMOTE_LINKS 3

// Layout-reserved slot capacity for the per-slot pose and face arrays in
// the Mailbox struct (mailbox_core.h). The Go offsets are generated from
// it (wwapi/mailbox/layout_gen.go), but the fields after those arrays sit
// at fixed offsets, so changing it is a Mailbox-layout revision (a
// WWMB_LAYOUT bump), not a constant edit. Runtime only iterates the
// first MAX_REMOTE_LINKS slots. Layout 3 sized it 3: the pose arrays
// took layout 2's _reserved2, face_state the tail of _reserved4.
#define MAILBOX_POSE_SLOT_CAP 3

// The mini-Links' own copies of the four eye-decal preset packets, one set
// per slot (src/mp/minilink.c, docs/05 F1), in the mailbox slack past the
// Mailbox struct: MAX_REMOTE_LINKS x 4 x 0x1C bytes, at the top of the
// slack so the blocks that grow up from +0x1B0 (wwrel.h, wwremote.h,
// wwvis.h, ...) keep their room. T2 writes them once per boot and the
// game's J3DDrawBuffer::entryImm their mpNextPacket every frame a slot
// draws; Go never touches them, and the region probe arms no canary over
// them (wwapi/mailbox CanaryArenaHigh ends at MP_EYE_PACKETS_OFF).
// They were T2 .bss until WWMB_LAYOUT 3.
#define MP_EYE_PACKETS_OFF 0xFB0
#define MP_EYE_PACKETS_SIZE 0x150

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
