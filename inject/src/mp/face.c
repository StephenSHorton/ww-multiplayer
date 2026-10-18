// Wind Waker Multiplayer — the multiplayer mod's face sync (src/mp;
// Phase E3 split multiplayer.c into src/core and src/mp): the
// J3DMatPacket vtable hook's arm / disarm and the per-slot face bracket
// around each mini-Link's entry. Link #1's own face snapshot
// (face_state_local) is the core's (services.c face_publish_local).

#include "mp.h"

// --- Face-state network sync (session 9) -----------------------------
// Mini-link's eye blink mirrors the LOCAL Link's btp because btp writes
// texno values into the SHARED J3DTevBlock at +0x08..+0x0B for mat[1]
// (left pupil) and mat[4] (right pupil). The shared block is one-per-
// J3DModelData, so any model rendering against Link.bdl reads the same
// values. We can't fix this by overwriting the shared block at frame
// time (the chain walker visits Link's matpackets and mini-link's
// matpackets serially against the same block, and we'd need different
// values for each).
//
// Solution: vtable-hook J3DMatPacket::draw on JUST our mini-link eye
// matpackets. Each J3DMatPacket has its own +0x00 vtable pointer; we
// point those at a private vtable copy whose [draw] entry calls our
// shim. The shim:
//   1. Identifies the owning model via mpInitShapePacket->mpModel.
//   2. Maps that to a mini-link slot (0..MAX_REMOTE_LINKS-1).
//   3. Saves the shared TevBlock+0x08..+0x0B bytes, overwrites them
//      with mailbox.face_state[slot] values.
//   4. Calls the original J3DMatPacket::draw (which calls
//      J3DMaterial::load → J3DTevBlock::load → loadTexNo → bakes the
//      texno into the per-instance differed shape DL).
//   5. Restores the shared block. Next matpacket sees the original.
//
// Single-threaded: the chain walker (J3DDrawBuffer::drawHead) calls
// each packet's draw() in sequence on the PPC thread. Our save +
// modify + draw + restore is atomic from the shared state's POV.
//
// Only mini-link's mat[1] and mat[4] matpackets are repointed. Link's
// own matpackets, every NPC's matpackets, and our mini-link's NON-eye
// matpackets keep the original vtable — zero overhead. Per-call cost
// of the shim itself is ~2 pointer derefs + a small loop = negligible.
//
// Gating: face_seqs[slot] != 0 means Go's puppet-sync has written at
// least one face state for this slot. Until then we no-op (don't
// overwrite, just call original draw) so the first frame's stale-zeros
// don't render closed-eye.
//
// Hook install: lazy. After mDoExt_J3DModel__create succeeds for each
// mini-link slot, we walk that model's matpacket array (model+0xB4)
// and patch entries [1] and [4] vtable pointers. Done once per slot;
// the patched vtable storage lives below as a static.
static u32 face_hook_vtable[J3D_MATPACKET_VTABLE_SIZE / 4]; // private vtable copy
static int face_hook_vtable_initialized = 0;
typedef void (*face_hook_orig_draw_t)(void*);
static face_hook_orig_draw_t face_hook_orig_draw = 0;

// SESSION 11: per-frame BP emit for mini-link's matpackets via manual
// J3DMaterial::vtable[7] invocation. mini-link's matpackets pass through
// vtable[5] (the trampoline executes a static DL) but never invoke
// vtable[7] (which preps the matpacket and tail-calls
// tev_block.vtable[10] = the actual TX_SETIMAGE3 / BP-register emit).
// As a result mini-link inherits whatever GX state Link's vtable[7]
// last set up (= btp's value) and "mirrors" Link's eye animation.
//
// Fix: in the shim, with tev_block contents temporarily swapped to
// face_state, manually call material.vtable[7]. That prepares the same
// state Link gets and emits BP commands using OUR swapped tev_block
// values. Restore tev_block before the original draw runs (which
// executes the static DL — geometry + already-emitted GX state), and
// before any other matpacket reads tev_block.

// Indices into mini_link_data's MaterialTable for the eye materials.
// Confirmed in session 8 via Go's tint-material diagnostic + the
// init-time eye-tex restore in minilink.c mp_frame. Sequence: mat[1] is
// the left pupil, mat[4] is the right pupil; both have texno[0] +
// texno[1] driven by btp (typically 0x0006 ↔ 0x0027 mid-blink).
#define FACE_HOOK_MAT_LEFT   1
#define FACE_HOOK_MAT_RIGHT  4

// The shim. Called via vtable indirection from J3DDrawBuffer::drawHead
// for mini-link's mat[1]/mat[4] J3DMatPackets. Standard PowerPC
// member-fn ABI: `this` (matpacket) in r3.
//
// SESSION 11 rewrite. Empirical findings (s10):
//   - Tev_block byte writes commit to RAM but don't move the rendered
//     pixel (verified by face-sync-tev-poll catching swap value 105/24120
//     polls; user reports zero visual change).
//   - DL byte writes at the matpacket+0x20 DLObj also commit but don't
//     change the render (verified by 8s 0x99 hammer — DL bytes show
//     0x99 stable, eye unchanged).
//   - All three matpackets (Link's + 2 mini-links) share the SAME
//     DLObj at 0x81542434 in this build. The bytes there are static,
//     not refreshed from tev_block per-frame.
//   - The actual texno emit comes from J3DMaterial::vtable[7] at
//     0x802DEBA0, which preps the matpacket then tail-calls
//     tev_block.vtable[10] (= the per-frame BP register emit).
//     Mini-link's matpackets miss this call, so the emitted GX state
//     they render against is whatever Link's vtable[7] last set
//     (= btp's value).
//
// Fix architecture:
//   1. Set j3dSys globals that vtable[7] reads (matpacket pointer at
//      +0x3C, matpacket+0x34 stash at +0x58 — same setup the matpacket
//      trampoline at 0x802DB494 does for vtable[5]).
//   2. Save tev_block's original texno bytes (btp's current value).
//   3. Overwrite tev_block with face_state[slot] values.
//   4. Manually call material.vtable[7] -> tail-calls
//      tev_block.vtable[10] -> WGPipe emits BP regs with face_state
//      values. GX state now reflects face_state.
//   5. Restore tev_block to btp's value (so subsequent matpackets and
//      next frame's btp see consistent state).
//   6. Call original draw. Geometry renders against the face_state-
//      configured GX state -> mini-link's eye uses face_state's texno.
// Session 12 update: the s11 implementation (manual material.vtable[7] call
// from inside matpacket.draw) corrupts the GP FIFO. Reverting to a true
// no-op so any code path that arms the hook just degrades to "call orig
// draw, bump a counter for diagnostics" instead of crashing.
//
// The actual face-state swap now happens via face_swap_for_slot /
// face_restore in mp_draw_post (minilink.c), where we have a clean FIFO
// context and can safely write to tev_block. See session_12 memory note.
static void face_hook_draw_shim(void* matpacket_v) {
    if (face_hook_orig_draw) {
        face_hook_orig_draw(matpacket_v);
    }
    // Diagnostic only — proves the shim is wired and firing.
    u32 cur = mailbox->face_hook_swaps;
    if (cur != 0xFFFFFFFFU) mailbox->face_hook_swaps = cur + 1;
}

// --- FACE-STATE per-slot swap (session 12; placed right in F20) --------
// Where the texno is read (zeldaret/tww): the mini-Link's entry is
// J3DModel::calcMaterial + diff + entry + viewCalc (minilink.c
// mini_link_entry_dl). calcMaterial runs every material's
// J3DMaterialAnm, which writes the LOCAL btp texno into the SHARED
// J3DTevBlock (J3DMaterialAnm.cpp:51-62); diff then bakes each tev block
// into THIS model's differed display list (J3DMaterial::diff ->
// J3DTevBlockPatched::diffTexNo -> loadTexNo, J3DMatBlock.cpp:1126),
// and nothing reads the tev block at draw time (J3DMatPacket::draw only
// replays display lists). So face_swap_for_slot runs after calcMaterial
// and face_restore after diff: the slot's face is what gets baked, and
// Link's own bake, earlier in his draw, keeps his. Until 2026-10-18 the
// swap bracketed the whole entry, so calcMaterial put the local face
// back a few instructions before the bake and every remote wore the
// local player's eyes and mouth (issue #5).
//
// Empirical proof of the channel: face-tev-hammer 0x0006 at 60Hz from Go
// blacked out BOTH Link AND mini-link's eyes on the hammered Dolphin
// only. So tev_block IS the channel; swaps around matpacket.draw
// (session 9) were simply the wrong window.
//
// The four tev_blocks are the core's (wwapi.h face_tev[], resolved by
// face_tevs_ready from the model data minilink.c binds with face_bind).
static u32 face_saved[FACE_TEV_COUNT];  // 4 bytes at tev+0x08 each (texNo[0]:u16 BE | texNo[1]:u16 BE)
static int face_swap_active = 0;

// Forget a swap in flight (minilink.c's mini_link_reset_state: the
// tev_blocks it would restore belong to the old J3DModelData).
void face_swap_reset(void) {
    face_swap_active = 0;
}

// Swap mat[1] / mat[4] / mat[14] / mat[15] tev_block.texno to
// face_state[slot] values.
// No-op if face_seqs[slot] is 0 (Go hasn't pushed first sample) or if
// face_hook_enable is 0 (master arm flag — keeps the swap disabled by
// default so a fresh boot doesn't render every mini-link with whatever
// stale face_state happens to be in mailbox memory).
void face_swap_for_slot(int slot) {
    if (!mailbox->face_hook_enable) return;
    if (slot < 0 || slot >= MAX_REMOTE_LINKS) return;
    if (mailbox->face_seqs[slot] == 0) return;
    if (face_swap_active) return;  // defensive — should never nest
    if (!face_tevs_ready()) return;

    face_saved[0] = *(volatile u32*)(face_tev[0] + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_saved[1] = *(volatile u32*)(face_tev[1] + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_saved[2] = *(volatile u32*)(face_tev[2] + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_saved[3] = *(volatile u32*)(face_tev[3] + J3DTEVBLOCK_TEXNO0_OFFSET);

    u32 mat1_val  = ((u32)mailbox->face_state[slot].mat1_tex0  << 16) | (u32)mailbox->face_state[slot].mat1_tex1;
    u32 mat4_val  = ((u32)mailbox->face_state[slot].mat4_tex0  << 16) | (u32)mailbox->face_state[slot].mat4_tex1;
    u32 mat14_val = ((u32)mailbox->face_state[slot].mat14_tex0 << 16) | (u32)mailbox->face_state[slot].mat14_tex1;
    u32 mat15_val = ((u32)mailbox->face_state[slot].mat15_tex0 << 16) | (u32)mailbox->face_state[slot].mat15_tex1;
    *(volatile u32*)(face_tev[0] + J3DTEVBLOCK_TEXNO0_OFFSET) = mat1_val;
    *(volatile u32*)(face_tev[1] + J3DTEVBLOCK_TEXNO0_OFFSET) = mat4_val;
    *(volatile u32*)(face_tev[2] + J3DTEVBLOCK_TEXNO0_OFFSET) = mat14_val;
    *(volatile u32*)(face_tev[3] + J3DTEVBLOCK_TEXNO0_OFFSET) = mat15_val;

    face_swap_active = 1;
    u32 cur = mailbox->face_hook_swaps;
    if (cur != 0xFFFFFFFFU) mailbox->face_hook_swaps = cur + 1;
}

void face_restore(void) {
    if (!face_swap_active) return;
    if (!face_tev[0] || !face_tev[1] || !face_tev[2] || !face_tev[3]) return;
    *(volatile u32*)(face_tev[0] + J3DTEVBLOCK_TEXNO0_OFFSET) = face_saved[0];
    *(volatile u32*)(face_tev[1] + J3DTEVBLOCK_TEXNO0_OFFSET) = face_saved[1];
    *(volatile u32*)(face_tev[2] + J3DTEVBLOCK_TEXNO0_OFFSET) = face_saved[2];
    *(volatile u32*)(face_tev[3] + J3DTEVBLOCK_TEXNO0_OFFSET) = face_saved[3];
    face_swap_active = 0;
}

// Initialize face_hook_vtable: copy the original J3DMatPacket vtable
// (0x18 B from 0x8039D910), verify [draw] still equals 0x802DB494,
// override [draw] with our shim. If the verification fails (game
// patched, or the address moved in some build), publish an error
// state and bail — leaving matpackets pointing at the original vtable
// keeps the game running without face sync.
static void face_hook_init_vtable(void) {
    if (face_hook_vtable_initialized) return;
    int k;
    for (k = 0; k < (int)(J3D_MATPACKET_VTABLE_SIZE / 4); k++) {
        face_hook_vtable[k] = J3D_MATPACKET_VTABLE[k];
    }
    if (face_hook_vtable[J3D_MATPACKET_VTABLE_DRAW_IX] != J3D_MATPACKET_DRAW_FN) {
        // Address mismatch — somebody else hooked it, or our address
        // is wrong. Don't risk dispatching to the wrong target.
        mailbox->face_hook_state = 0xFE;
        return;
    }
    face_hook_orig_draw =
        (face_hook_orig_draw_t)face_hook_vtable[J3D_MATPACKET_VTABLE_DRAW_IX];
    face_hook_vtable[J3D_MATPACKET_VTABLE_DRAW_IX] = (u32)&face_hook_draw_shim;
    face_hook_vtable_initialized = 1;
}

// Cached stride from the first successful probe. Stride is a build
// constant (0x3C for GZLE01), so once observed it can never change.
// Caching is load-bearing: after the first install rewrites mat[1]'s
// vtable, the probe's [+0x30, +0x60) scan no longer finds the original
// vtable at +0x3C and would return 0, flipping face_hook_state to 0xFC
// every subsequent frame even though the install was already good.
static u32 face_hook_cached_stride = 0;

// Self-test the matpacket layout by walking consecutive vtable
// pointers in the live matpacket array and computing the stride.
// Returns the observed stride (0x3C in the GZLE01 build) or 0 if
// the array doesn't look right. Used by face_hook_install_for_model
// to abort cleanly if the assumed J3DMATPACKET_SIZE diverges from
// what's actually in memory.
static u32 face_hook_probe_stride(u8* mp_array, int mat_count) {
    if (!mp_array || mat_count < 2) return 0;
    u32 first = *(u32*)mp_array;
    if (first != (u32)J3D_MATPACKET_VTABLE) return 0;
    // Search for the second matpacket's vtable in [+0x30, +0x60).
    // Both 0x3C (this build) and 0x40 (decomp default) fall in this
    // window. Any vtable in this range that matches the original is
    // very likely mat[1] (the chance of a coincidental 0x8039D910
    // word here is near-zero — that's a specific .data address, not
    // a value naturally occurring in matpacket bookkeeping fields).
    int off;
    for (off = 0x30; off < 0x60; off += 4) {
        if (*(u32*)(mp_array + off) == (u32)J3D_MATPACKET_VTABLE) {
            return (u32)off;
        }
    }
    return 0;
}

// Repoint mini_link_models[slot]'s mat[1] and mat[4] J3DMatPacket
// vtable pointers to our private vtable. Idempotent — calling twice
// on the same model is a no-op for already-patched slots, and the
// per-mat write is gated on the slot still holding the ORIGINAL
// vtable address (so a prior install survives).
//
// Returns the number of matpacket slots we wrote (0..2 per call).
//
// Session 11: slot index is unused (kept in the signature for symmetry
// with face_hook_uninstall_for_model and possible future per-slot
// cache; the manual vtable[7] invocation in the shim resolves the slot
// from the matpacket itself, not from any per-slot cache here).
static int face_hook_install_for_model(void* model_v, int slot) {
    if (!model_v) return 0;
    if (slot < 0 || slot >= MAX_REMOTE_LINKS) return 0;
    face_hook_init_vtable();
    if (!face_hook_vtable_initialized) return 0;

    u8* model = (u8*)model_v;
    u8* mp_array = *(u8**)(model + J3DMODEL_MP_MAT_PACKET_OFFSET);
    if (!mp_array) {
        if (mailbox->face_hook_state != 0xFE) mailbox->face_hook_state = 0xFD;
        return 0;
    }

    u32 stride = face_hook_cached_stride;
    if (stride == 0) {
        stride = face_hook_probe_stride(mp_array, 5);
        if (stride == J3DMATPACKET_SIZE) face_hook_cached_stride = stride;
    }
    if (stride != 0) mailbox->face_hook_mp_size = (u8)stride;
    if (stride != J3DMATPACKET_SIZE) {
        mailbox->face_hook_state = 0xFC;
        return 0;
    }

    int targets[2] = { FACE_HOOK_MAT_LEFT, FACE_HOOK_MAT_RIGHT };
    int t, written = 0;
    for (t = 0; t < 2; t++) {
        u8* mp = mp_array + (u32)targets[t] * J3DMATPACKET_SIZE;
        u32* vtbl_slot = (u32*)(mp + J3DMATPACKET_VTABLE_OFFSET);
        if (*vtbl_slot == (u32)J3D_MATPACKET_VTABLE) {
            *vtbl_slot = (u32)face_hook_vtable;
            written++;
        }
    }
    return written;
}

// Restore original vtable pointers on previously-patched matpackets.
// Safe to call when face_hook_enable is dropped to 0 (or when we
// detect the layout has drifted under us mid-flight).
static int face_hook_uninstall_for_model(void* model_v) {
    if (!model_v) return 0;
    u8* model = (u8*)model_v;
    u8* mp_array = *(u8**)(model + J3DMODEL_MP_MAT_PACKET_OFFSET);
    if (!mp_array) return 0;
    int targets[2] = { FACE_HOOK_MAT_LEFT, FACE_HOOK_MAT_RIGHT };
    int t, restored = 0;
    for (t = 0; t < 2; t++) {
        u8* mp = mp_array + (u32)targets[t] * J3DMATPACKET_SIZE;
        u32* vtbl_slot = (u32*)(mp + J3DMATPACKET_VTABLE_OFFSET);
        if (*vtbl_slot == (u32)face_hook_vtable) {
            *vtbl_slot = (u32)J3D_MATPACKET_VTABLE;
            restored++;
        }
    }
    return restored;
}

// Per-frame arm/disarm processor. Reads mailbox.face_hook_enable and
// installs/uninstalls the vtable hook on all live mini-link models
// accordingly. Idempotent each frame; the install's vtable-equality
// guard means re-installing a patched matpacket is a no-op. Called by
// mp_frame (minilink.c) once the models exist.
//
// no-unroll-loops: until Phase E3 this was inlined into the per-frame
// function; on its own, -O2's RTL unroller duplicates both slots'
// inlined install / uninstall paths and doubles it (0x5B4 bytes instead
// of 0x2DC). The two-slot loop costs nothing to keep rolled.
static u8 face_hook_prev_enable = 0;
__attribute__((optimize("no-unroll-loops")))
void face_hook_process_arm(void) {
    u8 want = mailbox->face_hook_enable;
    int total = 0;
    int li;
    for (li = 0; li < MAX_REMOTE_LINKS; li++) {
        if (!mini_link_models[li]) continue;
        if (want) {
            total += face_hook_install_for_model(mini_link_models[li], li);
        } else if (face_hook_prev_enable) {
            face_hook_uninstall_for_model(mini_link_models[li]);
        }
    }
    if (want) {
        // patched_count is 0..2*N; just publish what THIS frame's
        // install pass wrote. Steady state after first arm = 0 (all
        // already patched). Reading 2 means we just installed slot 0,
        // 4 means both slots. Reading 0 with face_hook_state == 1
        // means steady state.
        mailbox->face_hook_patched_count = (u8)total;
        if (mailbox->face_hook_state == 0) mailbox->face_hook_state = 1;
    } else {
        mailbox->face_hook_patched_count = 0;
        if (face_hook_prev_enable) mailbox->face_hook_state = 0;
    }
    face_hook_prev_enable = want;
}
