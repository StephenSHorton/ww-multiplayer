// Shared memory mailbox for communication between the Go host and the
// injected PPC code. Lives in the orphan region between our T2 code
// (ends ~0x80410448) and __OSArenaLo (0x80411000): outside any DOL
// section, outside the game's arena, nothing else touches it.
#ifndef MAILBOX_H
#define MAILBOX_H

#include "game.h"

// History: 0x80410800 → 0x80410900 → 0x80410F00 → 0x80411F00. Every
// time we add injected code the mod end crept up and crossed the mailbox
// address, corrupting game instructions. 0x80410F00 fell inside .text
// once the mini-Link / echo-ring code pushed mod size past 0x0F00.
//
// 2026-04-19 late-late: mod grew to 0x11C8 (echo-ring added in
// docs/06 "Next Session Priority" track 1). .text now ends ~0x80411140.
// Moved mailbox to 0x80411F00 AND bumped __OSArenaLo to 0x80412000
// (see inject/build.py OSInit patch). Orphan region is now [mod_end,
// 0x80412000) and mailbox sits flush against the arena at 0x80411F00.
// Mailbox ends at 0x80411FAC with new echo diagnostics.
#define MAILBOX_ADDR 0x80411F00

// Up to this many remote players can be mirrored as puppet actors.
// Pick a humble number; each slot adds one actor allocation.
#define MAX_PUPPETS 4

// Up to this many remote players can be rendered as full Link puppets
// via the pose-feed pipeline (shadow_mode 5). Each slot allocates its
// own J3DModel + 42-joint pose buffer (~2 KB each) from ArchiveHeap +
// GameHeap.
//
// CURRENTLY 1: render plumbing is wired for N>1 (mailbox arrays, per-
// slot calc loop, per-slot modelEntryDL) but enabling it produces
// SHARED J3DModelData bucket-list pollution — material packets are
// shared between instances and the last entry() call wins, so all
// instances render with the most-recently-submitted slot's pose. Both
// puppets visible (separate base matrices) but stuck on one pose. Real
// fix needs either per-instance material packet allocation OR the
// alternate mDoExt_modelEntry-once + mDoExt_modelUpdateDL-per-frame
// submission path. Future work; two-Dolphin multiplayer only needs N=1
// (each side renders THE other player) so it's not blocking the MVP.
#define MAX_REMOTE_LINKS 1

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

// Total mailbox size = 0x10 + MAX_PUPPETS * 0x20 + 0x04 tail. With 4 slots: 0x94 B.
typedef struct {
    u32 spawn_trigger;   // +0x00: per-frame heartbeat counter
    u32 progress;        // +0x04: debug marker — see multiplayer.c
    u32 _pad0;           // +0x08: also reused by main01_init as CALLBACK_PTR slot (mailbox+0x08 = 0x80410F08)
    u32 draw_progress;   // +0x0C: draw-hook-only diagnostic — mirrors progress but written only from daPy_draw_hook
    Puppet puppets[MAX_PUPPETS];  // +0x10 .. +0x10 + 0x20*N
    // Shadow daPy_lk_c experiment (docs/06 "Next Session Priority" step 1).
    // Go writes shadow_mode to route Link #2's joint callbacks:
    //   0 = baseline: userArea = Link #1's actor (mirrors Link #1, proven recipe)
    //   1 = refresh:  copy Link #1 into our shadow each frame, userArea = shadow
    //                 (same visual as mode 0 if routing works)
    //   2 = freeze:   copy once on mode entry, userArea = shadow every frame
    //                 (decisive test: Link #2 should FREEZE while Link #1 moves)
    // C publishes shadow_latched=1 after it has taken the mode-2 snapshot.
    u8  shadow_mode;     // +0x90
    u8  shadow_latched;  // +0x91
    u8  _pad1[2];        // +0x92
    // Diagnostics for the mode-3 basicMtxCalc swap. Populated each frame
    // after Link's draw returns so Go can see what the pointers are.
    u32 dbg_model_data;      // +0x94 — value of mini_link_data
    u32 dbg_saved_basic;     // +0x98 — value of *(mini_link_data + 0x24) before our write
    // Echo-Link ring buffer (shadow_mode 4). See docs/06 "Next Session Priority".
    //   dbg_joint_num   — mJointNum read from mini_link_data + 0x28 each frame.
    //                     Used to size the per-frame mpNodeMtx copy. Expected ~42.
    //   dbg_node_mtx_ptr — value of *(mini_link_model + 0x8C). Must be non-NULL
    //                     after calc runs or replay writes nowhere.
    //   echo_delay      — Go writes number of frames to delay the replay.
    //                     0 = identity overwrite (sanity: same visual as mirror).
    //                     1..ECHO_BUF_FRAMES-1 = replay from that many frames ago.
    //   echo_ring_state — C publishes: 0 unalloc, 1 alloc ok, 0xFE bad jointNum,
    //                     0xFD alloc failed. Lets Go distinguish silent failures.
    u16 dbg_joint_num;       // +0x9C
    u16 _pad2;               // +0x9E
    u32 dbg_node_mtx_ptr;    // +0xA0
    u8  echo_delay;          // +0xA4
    u8  echo_ring_state;     // +0xA5
    u16 _pad3;               // +0xA6
    // Pose-sync v0 (shadow_mode 5). Same shape as mode 4 (double-calc with
    // no-op basicMtxCalc swapped in for the second pass), but the source
    // for mpNodeMtx is a fixed GameHeap-resident buffer that Go fills from
    // either a local capture (pose-test) or the network (broadcast-pose).
    //
    //   pose_buf_ptr     — C publishes after JKRHeap_alloc succeeds.
    //                      Go reads, then writes Mtx[joint_count] (=2016 B
    //                      for Link) directly via WriteProcessMemory.
    //   pose_joint_count — C publishes (Link = 42). Go must use this to
    //                      size its writes; if it ever changes mid-run
    //                      (joint-tree swap) the buffer would need realloc.
    //   pose_buf_state   — 0 unalloc, 1 ready, 0xFE bad joint count,
    //                      0xFD JKRHeap_alloc failed.
    //   pose_seq         — Go bumps each write so future C-side code can
    //                      detect freshness; v0 just always reads latest.
    // Pose-feed slots, parallel arrays sized MAX_REMOTE_LINKS:
    //   pose_buf_ptrs[i]      — C publishes after JKRHeap_alloc per slot.
    //   pose_joint_counts[i]  — currently always 42 (Link). Per-slot in
    //                           case we ever drive child / fairy rigs.
    //   pose_buf_states[i]    — 0 unalloc, 1 ready, 0xFE bad joint count,
    //                           0xFD GameHeap alloc failed.
    //   pose_seqs[i]          — Go bumps each write so future C-side
    //                           freshness gate can drop stale poses.
    // Stride keeps arrays naturally aligned (u32 first, then u16, u8, u8).
    u32 pose_buf_ptrs[MAX_REMOTE_LINKS];      // +0xA8
    u16 pose_joint_counts[MAX_REMOTE_LINKS];  // +0xB0
    u8  pose_buf_states[MAX_REMOTE_LINKS];    // +0xB4
    u8  pose_seqs[MAX_REMOTE_LINKS];          // +0xB6
    // Diagnostic for slot 0 only — every mode-5 draw frame C publishes
    // `*pose_buf[0]` (before copy) and `mpNodeMtx[0][0]` (after second
    // calc). Lets Go confirm the read/copy/calc round-trip.
    u32 dbg_pose_first_word;                  // +0xB8
    u32 dbg_node_mtx_first;                   // +0xBC
} Mailbox;

#define mailbox ((volatile Mailbox*)MAILBOX_ADDR)

#endif
