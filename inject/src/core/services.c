// Wind Waker Multiplayer — the core services (src/core; Phase E3 split
// multiplayer.c into src/core and src/mp, docs/api/00-design.md §6.6).
// What the core does for every mod: the warp handler, the tunic images
// and the tunic header swap, the face tev blocks and Link #1's face
// snapshot, and Link #1's pose publish. The hooks (hooks.c) call them in
// the order written there; a mod calls the ones wwapi.h declares.

#include "wwapi.h"
#include "mailbox_core.h"
#include "core.h"

// --- Warp request (v0.1.8, mailbox warp_seq / warp_ack / warp_force) ---
void warp_frame(fopAc_ac_c* link) {
    // Triggered by Go bumping mailbox->warp_seq; multiplayer_update
    // calls this ahead of its boot gate (see there).
    // We write the target into all three actor_place position fields
    // (home, old, current) — offsets verified against zeldaret/tww
    // include/f_op/f_op_actor.h:
    //   home.pos     +0x1D0
    //   old.pos      +0x1E4
    //   current.pos  +0x1F8
    // Setting old==current makes the game's velocity-from-frame-delta
    // computation (current - old) read zero, so Link doesn't snap back.
    // Then we zero daPy_lk_c::mOldSpeed at +0x3694 so any cached momentum
    // from before the warp is dropped — without this, applying the warp
    // mid-walk would send Link sliding from the target in his pre-warp
    // direction. Ack the consume so Go can poll for completion.
    // Force-warp mode (diagnostic): re-apply target every frame so we can
    // distinguish "one-shot warp gets reverted by execute()" from "warp
    // hits a deeper reset". Treat exactly like a fresh warp_seq bump.
    int do_warp = (mailbox->warp_seq != mailbox->warp_ack) || (mailbox->warp_force != 0);
    if (do_warp) {
        u8* link_bytes = (u8*)link;
        cXyz target;
        target.x = mailbox->warp_x;
        target.y = mailbox->warp_y;
        target.z = mailbox->warp_z;
        cXyz zero = { 0.0f, 0.0f, 0.0f };
        // Position fields, found via the find-pos diagnostic — every
        // 4-byte-aligned (x,y,z) triplet in Link's actor memory that
        // tracks live world position. Writing only some of them lets
        // others drag Link back via posMove's collision-correction or
        // foot-pos snap; we write all six.
        //
        //   0x1D0  fopAc_ac_c::home.pos
        //   0x1E4  fopAc_ac_c::old.pos
        //   0x1F8  fopAc_ac_c::current.pos      (the visible one)
        //   0x4AC  daPy_lk_c (unnamed cached pos — likely render or pre-step)
        //   0x3498 daPy_lk_c (unnamed cached pos)
        //   0x3748 daPy_lk_c::m3748              (last unnamed cXyz block)
        *(cXyz*)(link_bytes + 0x01D0) = target;
        *(cXyz*)(link_bytes + 0x01E4) = target;
        *(cXyz*)(link_bytes + 0x01F8) = target;
        *(cXyz*)(link_bytes + 0x04AC) = target;
        *(cXyz*)(link_bytes + 0x3498) = target;
        *(cXyz*)(link_bytes + 0x3748) = target;
        // Clear all velocity / momentum so posMoveFromFootPos's
        // `current.pos += speed` doesn't slide Link out of the warp:
        //   0x220  fopAc_ac_c::speed   (cXyz)  — primary velocity
        //   0x254  fopAc_ac_c::speedF  (f32)   — forward speed scalar
        //   0x3694 daPy_lk_c::mOldSpeed (cXyz) — last frame's velocity
        *(cXyz*)(link_bytes + 0x0220) = zero;
        *(f32*)(link_bytes + 0x0254) = 0.0f;
        *(cXyz*)(link_bytes + 0x3694) = zero;
        // mStts.m_cc_move at +0x3FE8 (mStts) + 0x00 (m_cc_move offset in
        // cCcD_Stts) = +0x3FE8. posMove() does
        // `current.pos += *mStts.GetCCMoveP()` and m_cc_move is the
        // collision-correction displacement that pulls Link back toward
        // his pre-warp physics-body position. Zeroing it kills that
        // pull-back. Verified offsets against zeldaret/tww
        // include/SSystem/SComponent/c_cc_d.h.
        *(cXyz*)(link_bytes + 0x3FE8) = zero;
        // l_debug_keep_pos at 0x803E440C is the file-global cXyz that
        // execute() restores current.pos from at the START of every
        // frame (line 11322 in zeldaret/tww d_a_player_main.cpp,
        // gated `#if VERSION > VERSION_DEMO` which IS active in the
        // shipped retail build). Without writing here, our warp gets
        // immediately reverted next frame as Link's execute begins.
        // Address verified empirically via scan-pos + poke-vec3.
        *(volatile cXyz*)0x803E440C = target;
        mailbox->warp_ack = mailbox->warp_seq;
    }
}

// --- Tunic images ----------------------------------------------------
void tunic_alloc(void) {
    // Tunic colors (docs/09 §5.3): allocate the TUNIC_SLOTS per-slot
    // images ONCE, after Go has published their size. tunic_state never
    // goes back to 0 within a boot, so a stage load or save reload never
    // re-allocates (F18); main01_init zeroes it once per boot.
    if (mailbox->tunic_state == 0 && mailbox->tunic_buf_size != 0) {
        u8 st = 1;
        int t;
        for (t = 0; t < TUNIC_SLOTS; t++) {
            if (!mailbox->tunic_bufs[t]) {
                // From the heap's tail, as pose_publish (below).
                void* p = JKRHeap_alloc(mailbox->tunic_buf_size, -0x20, mDoExt_getGameHeap());
                if (p) mailbox->tunic_bufs[t] = (u32)p;
                else   st = 0xFD;
            }
        }
        mailbox->tunic_state = st;
    }
}

// --- Tunic colors (docs/09-player-colors.md §5.3) ---------------------
// Same save / swap / restore window as the face bracket (src/mp/face.c): every
// Link shares one J3DModelData and so one texture table, and each Link's
// diff bakes (u8*)hdr + hdr->imageOffset into its own differed DL
// (loadTexNo, J3DTevs.cpp:138-152). Repointing the worn linktexS3TC
// header around one Link's entry therefore recolors only that Link.
// `link` is Link #1 (the draw hook's this_), whose mpCurrLinktex is the
// shared header. When Link #1 wears the casual clothes the worn header
// holds the casual texture; the hero header is then mOtherLinktex
// (raw copy, offsets relative to the worn header), copied in for the
// remote slots. Link #1 himself is never swapped while casual.
static u32* tunic_hdr = 0;       // header currently swapped, 0 = none
static u32 tunic_saved[8];       // its original 0x20 bytes

// noinline + Os: these sit in a ~0x200-byte code budget (docs/09 §6);
// -O2 unrolls the 8-word copies and inlines the restore at three sites.
#define TUNIC_SMALL __attribute__((noinline, optimize("Os")))
// enable TUNIC_ENABLE_CASUAL (a remote in the casual clothes) copies the
// casual header in unchanged instead: while Link #1 wears the hero's
// clothes mOtherLinktex holds it (raw copy, image and palette offsets
// relative to the worn header), and while he is casual the worn header
// already is it, so nothing is swapped.
TUNIC_SMALL void tunic_swap(void* link, int slot) {
    u32 buf = mailbox->tunic_bufs[slot];
    u8 en = mailbox->tunic_enable[slot];
    u32* hdr = *(u32**)((u8*)link + DAPY_LK_C_MPCURRLINKTEX_OFFSET);
    if (tunic_hdr || !hdr || !en) return;
    u32* src = hdr;              // the header copied in: hero or casual
    u32* other = (u32*)((u8*)link + DAPY_LK_C_OTHERLINKTEX_OFFSET);
    int casual = *(u32*)((u8*)link + DAPY_LK_C_NORESETFLG1_OFFSET) & DAPY_FLG1_CASUAL_CLOTHES;
    if (en == TUNIC_ENABLE_CASUAL) {
        if (casual) return;      // the worn header already is the casual one
        src = other;
        buf = 0;
    } else {
        if (casual) {
            if (slot == TUNIC_SLOT_LOCAL) return;
            src = other;
        }
        if (!buf || (src[0] >> 24) != RESTIMG_FORMAT_CMPR) return;
    }
    int k;
    for (k = 0; k < 8; k++) {
        tunic_saved[k] = hdr[k];
        hdr[k] = src[k];
    }
    if (buf) hdr[7] = buf - (u32)hdr;  // imageOffset: wraps like loadTexNo's add
    tunic_hdr = hdr;
    mailbox->tunic_swaps++;
}

TUNIC_SMALL void tunic_restore(void) {
    int k;
    if (!tunic_hdr) return;
    for (k = 0; k < 8; k++) tunic_hdr[k] = tunic_saved[k];
    tunic_hdr = 0;
}

// --- Face tev blocks -------------------------------------------------
// tev_block is shared across ALL J3DModel instances using the same
// modelData. mat[1].material+0x2C and mat[4].material+0x2C (and the
// mouth's mat[14] and mat[15]) point at per-material singletons that
// are themselves shared. We resolve the four tev_blocks once per
// session via the shared modelData (the one a mod bound with face_bind)
// and cache them in face_tev[], in FaceState's order: mat[1] (left
// pupil), mat[4] (right pupil), mat[14] (mouth A), mat[15] (mouth B).
static J3DModelData* face_model_data = 0;
u8* face_tev[FACE_TEV_COUNT];

void face_bind(J3DModelData* model_data) {
    int k;
    face_model_data = model_data;
    for (k = 0; k < FACE_TEV_COUNT; k++) face_tev[k] = 0;
}

int face_tevs_ready(void) {
    if (!face_tev[0] || !face_tev[1] || !face_tev[2] || !face_tev[3]) {
        if (!face_model_data) return 0;
        // J3DModelData + 0x58 = mMaterialTable. mMaterialTable + 0x08 =
        // J3DMaterial** array (offset matches face-tev-hammer's path in
        // main.go: dataPtr + 0x58 + 0x08).
        u8** matarr = *(u8***)((u8*)face_model_data + 0x58 + 0x08);
        if (!matarr) return 0;
        u8* m1  = matarr[1];
        u8* m4  = matarr[4];
        u8* m14 = matarr[14];
        u8* m15 = matarr[15];
        if (m1)  face_tev[0] = *(u8**)(m1  + J3DMATERIAL_TEV_BLOCK_OFFSET);
        if (m4)  face_tev[1] = *(u8**)(m4  + J3DMATERIAL_TEV_BLOCK_OFFSET);
        if (m14) face_tev[2] = *(u8**)(m14 + J3DMATERIAL_TEV_BLOCK_OFFSET);
        if (m15) face_tev[3] = *(u8**)(m15 + J3DMATERIAL_TEV_BLOCK_OFFSET);
    }
    return face_tev[0] && face_tev[1] && face_tev[2] && face_tev[3];
}

// Publish the LOCAL Link's natural btp face state into the mailbox
// so broadcast-pose can read it cross-process without racing the
// bracket's tev_block save/swap/restore window. Called once per
// frame at the very top of daPy_draw_hook, BEFORE any swap fires —
// so tev_block holds btp's true value at this point.
//
// Resolves tev_blocks on first call and caches them in face_tev[],
// which the swap path uses too (so this is essentially free in steady
// state — just four volatile reads and 16 mailbox bytes).
void face_publish_local(void) {
    if (!face_model_data) return;
    if (!face_tevs_ready()) return;
    u32 m1  = *(volatile u32*)(face_tev[0] + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m4  = *(volatile u32*)(face_tev[1] + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m14 = *(volatile u32*)(face_tev[2] + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m15 = *(volatile u32*)(face_tev[3] + J3DTEVBLOCK_TEXNO0_OFFSET);
    mailbox->face_state_local.mat1_tex0  = (u16)(m1  >> 16);
    mailbox->face_state_local.mat1_tex1  = (u16)(m1  & 0xFFFF);
    mailbox->face_state_local.mat4_tex0  = (u16)(m4  >> 16);
    mailbox->face_state_local.mat4_tex1  = (u16)(m4  & 0xFFFF);
    mailbox->face_state_local.mat14_tex0 = (u16)(m14 >> 16);
    mailbox->face_state_local.mat14_tex1 = (u16)(m14 & 0xFFFF);
    mailbox->face_state_local.mat15_tex0 = (u16)(m15 >> 16);
    mailbox->face_state_local.mat15_tex1 = (u16)(m15 & 0xFFFF);
}

// --- Pose publish (v0.1.3) -------------------------------------------
// Called by daPy_draw_hook right after Link #1's draw (`link` is its
// this_).
void pose_publish(void* link) {
    // Publish Link #1's mpNodeMtx so Go-side broadcast-pose can read it
    // without racing calc. daPy_lk_c_draw has just completed for this
    // frame, so the joint walker has finished writing mpNodeMtx and the
    // buffer is stable until the next frame's calc. Lazy-alloc the
    // 2016 B publish buffer on the first frame, then memcpy every frame.
    // Allocated once per boot: only main01_init resets pose_publish_state,
    // so within a boot the GameHeap block is never dropped and
    // re-allocated (F18). It comes from the heap's tail (negative
    // alignment, JKRExpHeap.cpp:110): a soft reset keeps the heap, and the
    // title's heapSizeCheck (d_s_play.cpp:1143-1177) wants the largest
    // free GameHeap block >= 0.7 of all free. A head block left among the
    // stage's freed ones split it (0.68 live) and the game rebooted.
    // Runs unconditionally (not gated on shadow_mode) so Go can always
    // read a stable pose whether or not multiplayer is engaged — the
    // copy is a ~5 µs linear memcpy, trivial vs the 16 ms frame budget.
    if (mailbox->pose_publish_state != 0xFD) {
        J3DModel* link1_model = *(J3DModel**)((u8*)link + DAPY_LK_C_MPCLMODEL_OFFSET);
        J3DModelData* link1_data = *(J3DModelData**)((u8*)link + DAPY_LK_C_MPCLMODELDATA_OFFSET);
        if (link1_model && link1_data) {
            u16 jn = *(volatile u16*)((u8*)link1_data + J3DMODELDATA_JOINT_NUM_OFFSET);
            if (jn > 0 && jn <= 128) {
                if (mailbox->pose_publish_state == 0) {
                    u32 nbytes = (u32)jn * 48;
                    u8* buf = (u8*)JKRHeap_alloc(nbytes, -0x20, mDoExt_getGameHeap());
                    if (buf) {
                        mailbox->pose_publish_ptr = (u32)buf;
                        mailbox->pose_publish_joint_count = jn;
                        mailbox->pose_publish_state = 1;
                    } else {
                        mailbox->pose_publish_state = 0xFD;
                    }
                }
                if (mailbox->pose_publish_state == 1) {
                    u32 src_ptr = *(volatile u32*)((u8*)link1_model + J3DMODEL_MP_NODE_MTX_OFFSET);
                    if (src_ptr) {
                        volatile u32* src = (volatile u32*)src_ptr;
                        volatile u32* dst = (volatile u32*)mailbox->pose_publish_ptr;
                        int pub_u32s = (int)jn * 12;
                        int k;
                        for (k = 0; k < pub_u32s; k++) {
                            dst[k] = src[k];
                        }
                        mailbox->pose_publish_seq++;
                    }
                }
            }
        }
    }
}
