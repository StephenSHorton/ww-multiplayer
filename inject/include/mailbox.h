// Shared memory mailbox for communication between the Go host and the
// injected PPC code. Lives in the orphan region between our T2 code
// (ends ~0x80410448) and __OSArenaLo (0x80411000): outside any DOL
// section, outside the game's arena, nothing else touches it.
#ifndef MAILBOX_H
#define MAILBOX_H

#include "game.h"

#define MAILBOX_ADDR 0x80410800

// Up to this many remote players can be mirrored as puppet actors.
// Pick a humble number; each slot adds one actor allocation.
#define MAX_PUPPETS 4

// Per-slot state. Size is 0x20 bytes (nice round hex).
//   active   — Go sets 1 when a remote player owns this slot, 0 to
//              release it. C only spawns when active flips 0 -> 1.
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

// Total mailbox size = 0x10 + MAX_PUPPETS * 0x20. With 4 slots: 0x90 B.
typedef struct {
    u32 spawn_trigger;   // +0x00: per-frame heartbeat counter
    u32 progress;        // +0x04: debug marker — see multiplayer.c
    u32 _pad0;           // +0x08
    u32 _pad1;           // +0x0C
    Puppet puppets[MAX_PUPPETS];  // +0x10 .. +0x10 + 0x20*N
} Mailbox;

#define mailbox ((volatile Mailbox*)MAILBOX_ADDR)

#endif
