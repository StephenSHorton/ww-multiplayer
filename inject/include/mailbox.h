// Shared memory mailbox for communication between the Go host and the
// injected PPC code. Lives in the orphan region between our T2 code
// (ends ~0x80410448) and __OSArenaLo (0x80411000): outside any DOL
// section, outside the game's arena, nothing else touches it.
#ifndef MAILBOX_H
#define MAILBOX_H

#include "game.h"

// History: 0x80410800 → 0x80410900 → 0x80410F00 → 0x80411F00 → 0x80412F00.
// Every time we add injected code the mod end crept up and crossed the
// mailbox address, corrupting game instructions. 0x80410F00 fell inside
// .text once the mini-Link / echo-ring code pushed mod size past 0x0F00.
//
// 2026-04-19 late-late: mod grew to 0x11C8 (echo-ring added in
// docs/06 "Next Session Priority" track 1). .text now ends ~0x80411140.
// Moved mailbox to 0x80411F00 AND bumped __OSArenaLo to 0x80412000.
//
// 2026-04-25: eye-fix recipe (item #9 attempt 4) added the run_eye_fix
// helper plus its decomp lookups, growing mod to 0x1FC8 (~8 KiB). Mod
// end at 0x80411FC8 crossed into the old mailbox at 0x80411F00. Bumped
// mailbox to 0x80412F00 and __OSArenaLo to 0x80413000 (see
// inject/build.py OSInit patch). Mailbox sits flush against the arena.
#define MAILBOX_ADDR 0x80412F00

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
// multiplayer.c's mDoExt_J3DModel__create call makes createMatPacket
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
    // Sized by MAILBOX_POSE_SLOT_CAP (not MAX_REMOTE_LINKS) so the
    // offsets below are frozen against the runtime slot count.
    u32 pose_buf_ptrs[MAILBOX_POSE_SLOT_CAP];      // +0xA8
    u16 pose_joint_counts[MAILBOX_POSE_SLOT_CAP];  // +0xB0
    u8  pose_buf_states[MAILBOX_POSE_SLOT_CAP];    // +0xB4
    u8  pose_seqs[MAILBOX_POSE_SLOT_CAP];          // +0xB6
    // Diagnostic for slot 0 only — every mode-5 draw frame C publishes
    // `*pose_buf[0]` (before copy) and `mpNodeMtx[0][0]` (after second
    // calc). Lets Go confirm the read/copy/calc round-trip.
    u32 dbg_pose_first_word;                  // +0xB8
    u32 dbg_node_mtx_first;                   // +0xBC
    // --- SENDER SIDE publish buffer (v0.1.3) ---------------------------
    // Go's broadcast-pose reads Link #1's live mpNodeMtx (2016 B) via
    // ReadProcessMemory at 20 Hz while the game runs basicMtxCalc each
    // frame (60 Hz). These reads race — if caught mid-calc we see upper-
    // body joints from frame N + lower-body joints from frame N-1. On
    // flat ground the per-frame mpNodeMtx delta is near-zero so torn
    // reads are invisible; on slopes Link's foot IK re-solves each frame
    // with large leg-angle swings, so a torn read renders as a leg
    // flapping 0-90° on the remote's screen (observed v0.1.2 live test).
    //
    // Fix: C copies Link #1's mpNodeMtx into this dedicated publish
    // buffer ONCE per frame inside daPy_draw_hook, AFTER daPy_lk_c_draw
    // returns (so calc has definitely finished). Go reads from the
    // publish buffer instead. Publish writes don't race calc (they're
    // sequential on the PPC thread); Go's read races publish writes
    // instead, but publish is a fast linear memcpy (2 KB ≈ 5 µs on
    // Broadway's dcache) with no intermediate "partially valid" state
    // that resembles a well-formed pose.
    //
    // State machine:
    //   0     = not allocated yet (boot state)
    //   1     = buffer allocated and published; copy runs every frame
    //   0xFD  = JKRHeap_alloc failed (no point retrying — give up)
    //
    // Seq wraps at 256; Go uses it only as a "did this frame publish"
    // gate to reject stale reads, not as an ordered sequence number.
    u32 pose_publish_ptr;                     // +0xC0
    u16 pose_publish_joint_count;             // +0xC4
    u8  pose_publish_state;                   // +0xC6
    u8  pose_publish_seq;                     // +0xC7
    // --- WARP REQUEST (v0.1.8) -----------------------------------------
    // Go bumps warp_seq when it wants Link teleported to (warp_x, warp_y,
    // warp_z). C compares warp_seq to warp_ack each frame; on mismatch
    // it writes the target into Link's home.pos (+0x1D0), old.pos
    // (+0x1E4), and current.pos (+0x1F8) — overwriting all three so the
    // game's velocity-from-(current-old) computation reads zero, then
    // also zeros mOldSpeed (+0x3694) so last-frame momentum doesn't snap
    // Link back. After the writes, C copies warp_seq into warp_ack.
    //
    // Both fields are u32 so a wrap is impossible across a single test
    // session. Go can poll warp_ack to know the warp landed.
    u32 warp_seq;                             // +0xC8
    u32 warp_ack;                             // +0xCC
    f32 warp_x;                               // +0xD0
    f32 warp_y;                               // +0xD4
    f32 warp_z;                               // +0xD8
    // Warp diagnostics — populated each time the warp handler fires so
    // Go can verify the write actually reached memory and identify
    // which actor we're writing to.
    u32 warp_dbg_link_addr;                   // +0xDC — value of (u32)link
    f32 warp_dbg_post_x;                      // +0xE0 — current.pos.x AFTER our write (proves write landed)
    // Force-warp mode: when nonzero, C writes (warp_x, warp_y, warp_z)
    // to all known position fields EVERY frame, ignoring warp_seq/ack.
    // Diagnostic tool — lets us tell whether one-shot warps are being
    // reverted by Link's per-frame execute() vs hitting a deeper reset.
    u8  warp_force;                           // +0xE4
    u8  _pad5[3];                             // +0xE5
    // --- EYE-FIX STEPWISE PROBE (v0.1.7+) -------------------------------
    // Mini-Link's face is blank: pupils, eye outline, eyelids, eyebrows
    // missing. daPy_lk_c::draw runs a 5-pass eye-decal recipe (see
    // d_a_player_main.cpp:1827-1881 in zeldaret/tww @ 6aa7ba91) that
    // mDoExt_modelEntryDL alone doesn't reproduce. Three previous
    // attempts (A: entryIn-only, B: viewCalc + entryIn, C: bake texNo)
    // all crashed or were inert.
    //
    // To dodge the per-attempt save-state regen friction, all 5 passes
    // are wired into the C blob ONCE behind this byte: Go flips the
    // value, observation is immediate. Each step is cumulative.
    //   0 = baseline (current behavior — single mDoExt_modelEntryDL)
    //   1 = + j3dSys.setModel(mini_link) + setListP0 / setListP1 swap
    //       (no entryOpa / entryIn yet — pure list state probe)
    //   2 = + Pass 1's l_onCupOffAupPacket2.entryOpa ONLY
    //       (no shape vis, no entryIn — isolates entryOpa side effects)
    //   3 = + Pass 1's shape vis toggle (zOffBlend/zOn hide, zOffNone show)
    //       (still no entryIn — isolates shape-vis side effects)
    //   4 = + Pass 1's cl_eye/cl_mayu entryIn (= old step 2 state)
    //   5 = + Pass 2 (l_offCupOnAupPacket2 + flipped shape vis + entryIn)
    //   6 = + Pass 3 (link_root entryIn with face+hair-only material vis)
    //   7 = + Pass 4 (l_onCupOffAupPacket1 + zOn-shown + entryIn)
    //   8 = + Pass 5 (l_offCupOnAupPacket1 + zOn-hidden) = full recipe
    // If step N crashes Dolphin, kill + restart with save state, set step
    // to N-1 from Go, iterate. ONE save-state cycle covers all 8 levels.
    u8  eye_fix_step;                         // +0xE8
    u8  _pad6[3];                             // +0xE9
    // --- EYE-FIX POST-DRAW CHAIN SNAPSHOT (session 5) ------------------
    // Right after daPy_lk_c_draw returns and BEFORE run_eye_fix touches
    // anything, snapshot opa_p0 bucket[0] head plus first 10 chain
    // entries via .next walk. The 4 static packets (l_*AupPacket1/2
    // at 0x803E46A4/C0/DC/F8) are entered by daPy_lk_c::draw's
    // four-pass eye-decal recipe; their presence/absence in the
    // post-draw chain is the definitive test for whether the four-pass
    // body actually executed. Resolves the session-4-part-2 anomaly:
    // eye-fix-gates says four-pass should run at step=4, but find-shape
    // never sees the statics. Either gates are misanalyzed (eliminated
    // by session-5 disasm) or entries are vanishing post-submission.
    u32 eye_fix_post_chain[10];               // +0xEC..+0x113
    u8  eye_fix_post_chain_count;             // +0x114
    u8  _pad7[3];                             // +0x115
} Mailbox;

#define mailbox ((volatile Mailbox*)MAILBOX_ADDR)

#endif
