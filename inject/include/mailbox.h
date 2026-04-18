// Shared memory mailbox for communication with the Go app.
// The Go app writes position data here via WriteProcessMemory.
// The injected code reads it and applies to the spawned actor.
#ifndef MAILBOX_H
#define MAILBOX_H

#include "game.h"

// Mailbox lives in the gap between our T2 code (ends 0x80410448) and
// __OSArenaLo (0x80411000). This range is outside any DOL section AND outside
// the game's arena, so nothing touches it except us.
#define MAILBOX_ADDR 0x80410800

typedef struct {
    u32 spawn_trigger;     // +0x00: Go app writes 1 to request spawn / per-frame heartbeat counter
    u32 actor2_ptr;        // +0x04: We store the spawned actor pointer here
    f32 p2_pos_x;          // +0x08: Player 2 X position (from network)
    f32 p2_pos_y;          // +0x0C: Player 2 Y position
    f32 p2_pos_z;          // +0x10: Player 2 Z position
    s16 p2_rot_x;          // +0x14: Player 2 rotation X
    s16 p2_rot_y;          // +0x16: Player 2 rotation Y
    s16 p2_rot_z;          // +0x18: Player 2 rotation Z
    u16 _pad;              // +0x1A: padding
    u32 p2_anim;           // +0x1C: Player 2 animation state
    u32 progress;          // +0x20: debug progress marker — last step reached this frame
} Mailbox;

#define mailbox ((volatile Mailbox*)MAILBOX_ADDR)

#endif
