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
//
// 2026-04-25 session 6: mailbox grew to 0x130 B (mode-1 freeze
// diagnostics: dbg_pre_mclmodeldata + dbg_postswap_mclmodeldata +
// dbg_safety_check_current_data + dbg_safety_fired_count +
// dbg_mini_link_data_cached). Bumped __OSArenaLo to 0x80414000 to give
// the mailbox 0x1000 = 4 KB of headroom — mailbox is no longer flush
// against the arena, so we can add a few more diagnostic fields here
// without revisiting build.py. MAILBOX_ADDR itself stays at
// 0x80412F00 (no struct-offset breakage on the Go side).
#define MAILBOX_ADDR 0x80412F00

// The WWMB header (Phase E1) main01_init writes at mailbox +0x180, and
// the features word at +0x1A0. Bump WWMB_LAYOUT on any change to the
// Mailbox layout below and WWMB_ABI on any change to what a field means;
// Go's blob.go carries both (scripts/extract_blob.py) and its contract
// check refuses a game whose header differs.
#define WWMB_MAGIC 0x57574D42
#define WWMB_ABI 1
#define WWMB_LAYOUT 1

// Features: one bit per service the blob provides (wwapi Features).
#define WW_FEAT_AVATAR 0x01
#define WW_FEAT_EYEFIX 0x02
#define WW_FEAT_FACE 0x04
#define WW_FEAT_INPUT 0x08
#define WW_FEAT_TUNIC 0x10
#define WW_FEAT_WARP 0x20
#define WW_FEATURES 0x3F

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

// See "FACE-STATE NETWORK SYNC" block in Mailbox below for context.
//
// Session 15: extended from 8 B to 16 B to also carry mouth state.
// mat[14] and mat[15] are Link's two mouth materials (confirmed via
// per-material hammer probe — both visibly highlight the mouth area
// when their tev_block.texno is hammered to a foreign value). Layout
// matches J3DTevBlock+0x08..+0x0B for each material:
//   matN_tex0 = u16 BE @ tev+0x08
//   matN_tex1 = u16 BE @ tev+0x0A
typedef struct {
    u16 mat1_tex0;    // left pupil
    u16 mat1_tex1;
    u16 mat4_tex0;    // right pupil
    u16 mat4_tex1;
    u16 mat14_tex0;   // mouth A (texno[0]=0x001D in default state)
    u16 mat14_tex1;
    u16 mat15_tex0;   // mouth B (texno[0]=0x0025 in default state)
    u16 mat15_tex1;
} FaceState;

// Total mailbox size: 0x1B0 B (see the end of the struct).
//
// Phase E0 deleted the C experiments behind several fields (shadow modes
// 1-4, the echo ring, the eye-fix probe steps 1-7 and modes 1-2, and the
// dbg_* / warp_dbg_* / eye_fix_post_chain / draw_progress diagnostics)
// WITHOUT changing this layout: those fields keep their names and
// offsets but nothing reads or writes them any more ("UNUSED since E0").
// The E2 layout bump turns them into _reserved.
typedef struct {
    u32 spawn_trigger;   // +0x00: per-frame heartbeat counter
    u32 progress;        // +0x04: debug marker — see multiplayer.c
    u32 _pad0;           // +0x08: also reused by main01_init as CALLBACK_PTR slot (mailbox+0x08 = 0x80410F08)
    u32 draw_progress;   // +0x0C: UNUSED since E0 (was a draw-hook diagnostic)
    Puppet puppets[MAX_PUPPETS];  // +0x10 .. +0x10 + 0x20*N
    // Go writes shadow_mode to switch Link #2 rendering:
    //   0 = off (the boot value): no mini-Link is drawn
    //   5 = pose feed: each slot drawn from its pose buffer (below)
    // Values 1-4 were the mirror / freeze / no-op-calc / echo-ring dev
    // modes, deleted in E0; they now draw nothing, like 0.
    u8  shadow_mode;     // +0x90
    u8  shadow_latched;  // +0x91: UNUSED since E0 (mode-2 latch)
    u8  _pad1[2];        // +0x92
    // +0x94..+0xA7: UNUSED since E0 (mode-3/4 diagnostics and the
    // mode-4 echo ring's delay and state).
    u32 dbg_model_data;      // +0x94
    u32 dbg_saved_basic;     // +0x98
    u16 dbg_joint_num;       // +0x9C
    u16 _pad2;               // +0x9E
    u32 dbg_node_mtx_ptr;    // +0xA0
    u8  echo_delay;          // +0xA4
    u8  echo_ring_state;     // +0xA5
    u16 _pad3;               // +0xA6
    // Pose feed (shadow_mode 5). For each slot C runs the mini-Link calc
    // twice: once with Link's real walker, then, after copying the slot's
    // pose buffer (which Go fills from the network or pose-test) over
    // mpNodeMtx, with a no-op walker so the envelope pass rebuilds
    // mpDrawMtxBuf from that pose.
    //
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
    // UNUSED since E0 (slot-0 pose round-trip diagnostics).
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
    // UNUSED since E0 (warp diagnostics).
    u32 warp_dbg_link_addr;                   // +0xDC — value of (u32)link
    f32 warp_dbg_post_x;                      // +0xE0 — current.pos.x AFTER our write (proves write landed)
    // Force-warp mode: when nonzero, C writes (warp_x, warp_y, warp_z)
    // to all known position fields EVERY frame, ignoring warp_seq/ack.
    // Diagnostic tool — lets us tell whether one-shot warps are being
    // reverted by Link's per-frame execute() vs hitting a deeper reset.
    u8  warp_force;                           // +0xE4
    u8  _pad5[3];                             // +0xE5
    // UNUSED since E0. Was the eye-fix stepwise probe (0 = default 8,
    // 1-7 = partial recipes); C now always runs the full recipe (the old
    // step 8, multiplayer.c run_eye_fix).
    u8  eye_fix_step;                         // +0xE8
    u8  _pad6[3];                             // +0xE9
    // UNUSED since E0 (post-draw chain snapshot for the eye-fix probe).
    u32 eye_fix_post_chain[10];               // +0xEC..+0x113
    u8  eye_fix_post_chain_count;             // +0x114
    u8  _pad7[3];                             // +0x115
    // UNUSED since E0. Was the eye-fix mClModel-swap experiment (modes
    // 1-2); C always draws mini-Link the mode-0 way.
    u8  eye_fix_mode;                         // +0x118
    u8  _pad8[3];                             // +0x119
    // +0x11C..+0x12F: UNUSED since E0 (mode-1 freeze diagnostics).
    u32 dbg_mini_link_data_cached;            // +0x11C
    u32 dbg_pre_mclmodeldata;                 // +0x120
    u32 dbg_postswap_mclmodeldata;            // +0x124
    u32 dbg_safety_check_current;             // +0x128
    u32 dbg_safety_fired_count;               // +0x12C
    // --- FACE-STATE NETWORK SYNC (session 9) ---------------------------
    // Per-slot face animation state that mini-link should render with,
    // independent of the LOCAL Link's animation state.
    //
    // Background: J3DTevBlock is one-per-J3DModelData. btp animates eye
    // pupils each frame by writing texno values into the SHARED TevBlock
    // at +0x08..+0x0B for mat[1] (left pupil) and mat[4] (right pupil).
    // Without this channel, mini-link's render path bakes the same texno
    // values btp wrote for the LOCAL Link — i.e. mini-link blinks when
    // the local player blinks, not when the remote player blinks.
    //
    // Fix: vtable-hook J3DMatPacket::draw on mini-link's eye matpackets;
    // when those matpackets are drawn (during the chain walker's serial
    // pass), the hook temporarily swaps the shared TevBlock+0x08..+0x0B
    // bytes to the remote's values from this struct, calls the original
    // draw (which bakes those bytes into the per-instance differed shape
    // DL), then restores so subsequent matpackets see the original.
    // Single-threaded chain walker = no race.
    //
    // Wire: Go's broadcast-pose reads its local TevBlock+0x08..+0x0B
    // every tick and appends 8 B per slot to the pose message. puppet-sync
    // writes here + bumps face_seqs[slot]. C-side hook gates on
    // face_seqs[slot] != 0 (so first-frame stale zeros don't render
    // closed-eye).
    //
    // Field naming: matN_texT = J3DTevBlock+0x08+T*2 (u16) for material N.
    // FaceState is declared near the top of this file (before Mailbox)
    // so other TUs / tests can include it without unwrapping the Mailbox
    // struct.
    FaceState face_state[MAILBOX_POSE_SLOT_CAP];  // +0x130 (16 B each, session 15)
    u8  face_seqs[MAILBOX_POSE_SLOT_CAP];         // +0x150 (1 B each)
    u8  _pad9[2];                                 // +0x152 (align)
    // Diagnostics for the face-state hook so Go can confirm it fired.
    //   face_hook_state — 0 unset, 1 vtable copied + matpackets patched,
    //                     0xFE J3DMatPacket vtable[draw] mismatch (skipped),
    //                     0xFD matpackets array NULL (skipped).
    //   face_hook_swaps — saturating count of frames the shim actually
    //                     swapped tevBlock bytes for any slot. Bumped
    //                     once per draw call, not once per slot.
    u8  face_hook_state;                          // +0x154
    // Kill-switch / arm flag for the J3DMatPacket vtable patch.
    // Default 0 = don't touch matpackets at create time. Go writes 1
    // to ARM (next frame, the C side validates the layout and patches
    // mat[1]/mat[4] vtable pointers). Go writes 0 to DISARM (next
    // frame, the C side restores original vtable pointers and stops
    // patching).
    //
    // Why arm/disarm vs always-on: the matpacket layout assumption
    // (stride 0x3C, mpInitShapePacket@+0x24, mpMaterial@+0x2C) was
    // wrong-by-default in session 9 first attempt — install eagerly
    // from create would have corrupted neighboring matpacket fields
    // if the offsets were wrong. With the arm/disarm gate we can
    // re-verify the layout from Go (peek the matpacket array, confirm
    // consecutive vtable pointers, etc.) BEFORE flipping the switch.
    // Same gate also lets us toggle the hook off in-flight if a future
    // build's matpacket layout drifts.
    u8  face_hook_enable;                         // +0x155
    u8  _pad10[2];                                // +0x156
    u32 face_hook_swaps;                          // +0x158
    // Sanity-check fields published by the C-side install. Read by Go
    // to decide whether arming was successful.
    //   face_hook_mp_size      — observed matpacket stride (computed
    //                            by walking consecutive vtable ptrs in
    //                            the live matpacket array). 0 until
    //                            install runs. Should == 0x3C.
    //   face_hook_patched_count — how many matpacket vtable slots we
    //                            actually rewrote (target = 2 per
    //                            mini-link slot when armed).
    u8  face_hook_mp_size;                        // +0x15C
    u8  face_hook_patched_count;                  // +0x15D
    u8  _pad11[2];                                // +0x15E
    // --- FACE-STATE LOCAL SNAPSHOT (session 14) -------------------------
    // The LOCAL Link's natural btp face state, snapshotted from
    // tev_block at the top of daPy_draw_hook BEFORE any
    // face_emit_swap_for_slot fires. Layout matches FaceState above
    // (16 B as of session 15 — eyes + mouth, all u16 BE).
    //
    // Why: broadcast-pose used to read tev_block+0x08 directly from
    // Go-side via ReadProcessMemory. That read is async from the emu
    // CPU and could land inside the bracket's save→swap→bake→restore
    // window — shipping the swapped face_state value to the remote
    // instead of btp's natural value. Surfaced visually under
    // `face-sync-fake-loop 0xFFFF` testing 2026-05-06: the OTHER
    // Dolphin's mini-link occasionally rendered missing pupils too,
    // because broadcast-pose on the hammered Dolphin captured the
    // bracket's transient FFFF and shipped it cross-network.
    //
    // Fix: publish btp's value here at hook entry (before swaps
    // fire). broadcast-pose reads from this mailbox slot instead of
    // tev_block — no race possible because this slot is only written
    // at one point in the frame (hook entry, pre-swap), and Go-side
    // reads see the latest stable value.
    FaceState face_state_local;                   // +0x160 (16 B, session 15)
    // --- INPUT INJECTION (synthetic PADStatus override) ----------------
    // Go writes input_enable=1 to make pad_read_shim (hooked over the
    // bl PADRead inside JUTGamePad::read at 0x802C39A0) override
    // mPadStatus[0] right after PADRead returns. Derivation downstream
    // (mPadButton[0], mPadMStick[0], mPadSStick[0]) then picks up the
    // synthetic values cleanly — no race with Dolphin's SI poll.
    //
    // Set input_enable=0 to release; pad_read_shim then becomes a
    // pass-through. All input_* fields are zero in idle.
    //
    // Layout mirrors PADStatus from libogc/Dolphin (12 B total). We only
    // override port 0 — that's the active player. Other ports are
    // unchanged.
    u8  input_enable;                             // +0x170
    u8  _pad12[1];                                // +0x171
    u16 input_buttons;                            // +0x172 — PADStatus.button bitfield
    s8  input_stick_x;                            // +0x174 — main analog X (-128..+127, 0 centred)
    s8  input_stick_y;                            // +0x175 — main analog Y
    s8  input_substick_x;                         // +0x176 — C-stick X
    s8  input_substick_y;                         // +0x177 — C-stick Y
    u8  input_trigger_l;                          // +0x178 — L analog
    u8  input_trigger_r;                          // +0x179 — R analog
    u8  input_analog_a;                           // +0x17A — pressure on A
    u8  input_analog_b;                           // +0x17B — pressure on B
    // Diagnostic: bumped each time pad_read_shim performs an override.
    // Lets Go confirm the shim is wired and live; saturates at 0xFFFF_FFFF.
    u32 input_overrides;                          // +0x17C
    // --- WWMB HEADER (Phase E1, docs/api/00-design.md §5.1, §6.4) ----
    // main01_init writes these once at boot: the magic, the C ABI and
    // this layout's version. Go's contract check (wwapi.CheckContract)
    // refuses to write into a game whose header is missing or differs
    // from its blob.go: an ISO patched by another version, or a save
    // state taken on one. Every blob before E1 left these 8 bytes zero.
    u32 hdr_magic;                                // +0x180 (C) WWMB_MAGIC
    u16 hdr_abi;                                  // +0x184 (C) WWMB_ABI
    u16 hdr_layout;                               // +0x186 (C) WWMB_LAYOUT
    // --- TUNIC COLORS (docs/09-player-colors.md §5.3) -----------------
    // Per-player recolored copies of Link's hero's-clothes body texture
    // ("linktexS3TC", CMPR). Slots 0..1 are the mini-Link slots, slot 2
    // (TUNIC_SLOT_LOCAL) is Link #1 on this Dolphin.
    //
    // Go writes tunic_buf_size (bytes per image) once; the next
    // multiplayer_update allocates the three images from GameHeap ONCE
    // and publishes them in tunic_bufs. Nothing ever frees or resets
    // them (F18 lifetime rule: GameHeap blocks stay valid all session).
    // Go then writes each image (internal/tunic Recolor) and sets
    // tunic_enable[slot] = 1. To change a slot's color Go sets enable
    // to 0, waits two frames, rewrites the image, sets enable to 1.
    //
    // Around each Link's draw, C repoints mpCurrLinktex's imageOffset at
    // that slot's image (copying the hero header from mOtherLinktex when
    // Link #1 wears the casual clothes) and restores the header after.
    // Link #1 in the casual clothes is never swapped.
    // tunic_enable[slot] = 2 (TUNIC_ENABLE_CASUAL, docs/09 §5.6) marks a
    // remote in the casual clothes: C copies the casual header in
    // (mOtherLinktex, when Link #1 wears the hero's clothes) and uses no
    // image buffer.
    //   tunic_state — 0 not allocated, 1 all three allocated,
    //                 0xFD an allocation failed (the ones that worked
    //                 stay published).
    //   tunic_swaps — diagnostic: header swaps done (local + remotes).
    u32 tunic_bufs[3];                            // +0x188 (C)
    u32 tunic_buf_size;                           // +0x194 (Go)
    u8  tunic_enable[3];                          // +0x198 (Go)
    u8  tunic_state;                              // +0x19B (C)
    u32 tunic_swaps;                              // +0x19C (C)
    // WWMB header extension (E1): the WW_FEAT_* bits this blob provides,
    // written by main01_init with the header. Go matches each mod's
    // "needs" against it (NEEDS_REPATCH).
    u32 features;                                 // +0x1A0 (C) WW_FEATURES
    u8  _pad13[12];                               // +0x1A4 (pad to 0x1B0)
} Mailbox;   // 0x1B0 bytes

#define TUNIC_SLOT_LOCAL 2
#define TUNIC_ENABLE_CASUAL 2

#define mailbox ((volatile Mailbox*)MAILBOX_ADDR)

#endif
