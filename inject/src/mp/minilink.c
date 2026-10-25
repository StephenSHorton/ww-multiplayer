// Wind Waker Multiplayer — the multiplayer mod's mini-Links (src/mp;
// Phase E3 split multiplayer.c into src/core and src/mp): the remote
// Links drawn as extra J3DModels of Link's own model data (the pose
// feed, shadow_mode 5), with the eye-fix recipe and the casual-clothes
// head fix. Also mp_frame, the mod's per-frame entry.

#include "mp.h"

// --- Mini-Link state (Option B from roadmap 06) ------------------------
// Render N independent Link models from OUR code, all sharing Link #1's
// already-loaded J3DModelData. No actors, no PROC_PLAYER spawn, no
// 704 KB heap carve-out per model. shadow_mode 5 (pose feed) drives all
// MAX_REMOTE_LINKS slots; every other shadow_mode value draws nothing
// (the mirror / freeze / echo dev modes 1-4 were deleted in Phase E0).
J3DModelData* mini_link_data = 0;
J3DModel* mini_link_models[MAX_REMOTE_LINKS] = {0};
// 0 = not tried yet, 1 = all models created + rendering, 0xFF = give up
u8 mini_link_state = 0;

// No-op J3DMtxCalc for the pose feed's calc. Swapped into
// J3DModelData->basicMtxCalc around that calc so it skips the joint walk
// (mpNodeMtx keeps the pose we copied in) while its envelope pass
// rebuilds mpDrawMtxBuf from that pose. Link's real walker never runs
// on a mini-Link (F19: its joint callbacks write into Link #1).
//
// Vtable layout: 16 slots (over-provisioned — base J3DMtxCalc has 4; derived
// subclasses may have more if any downstream code pretends our object is a
// subclass). Every slot points at noop_stub (a bare `blr`).
static u32 noop_mtxcalc_vtable[16];
static u32 noop_mtxcalc_obj[16];     // +0x00 = vtable ptr, rest unused
static int noop_mtxcalc_initialized = 0;

__attribute__((naked))
static void noop_stub(void) {
    asm volatile("blr\n");
}

// --- Pose feed state (shadow_mode 5) ----------------------------------
// Single GameHeap-resident buffer of joint matrices. Go writes directly to
// pose_buf via the published mailbox.pose_buf_ptr; C copies into mpNodeMtx
// each draw, then runs one calc with the no-op walker to rebuild
// mpDrawMtxBuf from our pose. Hold-last semantics: C always reads
// whatever bytes are in pose_buf, no freshness gate yet.
static u8* pose_bufs[MAX_REMOTE_LINKS] = {0};
static int pose_joint_num = 0;     // captured at first slot 0 alloc; same for all slots (Link rig)
static int pose_buf_u32s = 0;      // joint_num * 12 (Mtx = 12 u32s)
// Archive + BDL index for the model we hand to mDoExt_J3DModel__create.
// Stored in rodata (lives in T2) so the compiler emits a real pointer
// getRes can dereference.
//
// 2026-04-19 evening: rigid path (Tsubo, ALWAYS_BDL_MPM_TUBO=0x31)
// proven end-to-end with 2-field j3dSys save/restore. Tracked Link
// in real time, sky clean, no crashes.
//
// 2026-04-19+ step 1 of docs/06 "Next Session Priority": flip back
// to Link with EXPANDED 5-field save/restore (mModel, mCurrentMtxCalc,
// mMatPacket, mShapePacket, mShape). Hypothesis: skinned-model
// calc() walks Link's skeleton via mCurrentMtxCalc and writes the
// three additional fields, leaving mini-Link pointers behind for
// Link's post-draw checkEquipAnime to deref (PC 0x8010C53C, r3=NULL).
// If still crashes there → step 2 (full 0x128-byte j3dSys snapshot).
// If renders but looks wrong → mDoExt_J3DModel__create flags need
// tuning for skinned/skeleton models. To revert to the rigid baseline,
// flip both constants back to "Always" / ALWAYS_BDL_MPM_TUBO.
static const char PROBE_ARCNAME[] = "Link";
#define PROBE_BDL_IDX LINK_BDL_CL

// Lifetimes (2026-10-16, F18 — docs/05 "Mini-Link J3DModels leaked
// on every stage load"). Nothing here is torn down by a stage load:
//   - mini_link_data is Link.arc's J3DModelData. Link.arc is mounted
//     once at boot (d_s_logo.cpp:876) and stays resident; every Link
//     actor gets the SAME pointer from getObjectRes (d_a_player_main.cpp
//     :12059), so mpCLModelData never changes once Link is built.
//   - mini_link_models live in ArchiveHeap and pose_publish / pose_bufs
//     in GameHeap. The game never resets
//     either heap wholesale (no freeAll on them in the decomp), so our
//     blocks stay valid for the whole session — and stay ALLOCATED if
//     we drop the pointer. Create / allocate once, keep forever.
// What DOES change on a stage load is Link's actor: phase_1 publishes a
// zeroed actor (f_pc_base.cpp:98, d_a_player_main.cpp:12361) and
// playerInit only fills mpCLModelData in phase_2, after the camera
// exists (:12374-12386). So mpCLModelData == 0 means "Link not ready
// yet": multiplayer_update waits; it never resets.
//
// Since Phase E0 (the mirror / freeze / echo modes are gone) no
// mini-Link state holds the old Link actor or old-stage history, so a
// stage load needs no per-stage reset: allocations, pose_seqs /
// face_seqs (Go owns those) and the core's face tev_block cache (it points
// into the resident J3DModelData) are all kept.

// Rebuild against a DIFFERENT J3DModelData. Only reached when Link's
// mpCLModelData is non-NULL, differs from ours, and is what Link.arc
// itself now returns — i.e. Link.arc was really re-mounted. With
// Link.arc resident since boot that never happens; this is a guard,
// not a code path we expect to run.
//
// The old models are NOT freed. J3DModel::~J3DModel is empty
// (J3DModel.cpp:26) and entryModelData / createMatPacket /
// newDifferedDisplayList make a dozen-plus sub-allocations per model
// that the game only ever releases by destroying the owning heap, so
// JKRHeap::free on the J3DModel would reclaim ~0xDC bytes and leave the
// rest stranded anyway. Leaking one set per real re-mount is bounded;
// leaking one set per frame of every stage load (the F18 bug) was not.
// GameHeap buffers (pose_bufs, pose_publish) don't depend on the model
// data and are kept.
static void mini_link_reset_state(void) {
    mini_link_data = 0;
    mini_link_state = 0;
    int k;
    for (k = 0; k < MAX_REMOTE_LINKS; k++) {
        mini_link_models[k] = 0;
    }
    // The tev_blocks belong to the old J3DModelData's material table:
    // unbind them (the core re-resolves from the next face_bind) and
    // forget any face swap in flight.
    face_bind(0);
    face_swap_reset();
}

// mods.h: every frame once Link exists and multiplayer_update's boot
// gate has passed. Runs the actor puppets (puppets.c), creates the
// mini-Link models once, processes the face hook's arm / disarm and
// publishes the frame's best progress value.
void mp_frame(fopAc_ac_c* link) {
    u32 best_progress = puppets_frame(link);
    rel_frame(link);

    // --- Mini-Link render path ----------------------------------------
    // Stage-load safety (see "Lifetimes" above mini_link_reset_state).
    // While Link's playerInit hasn't run yet (mpCLModelData == 0) we
    // skip mini-Link work entirely. The J3DModels are kept — they were
    // created against Link.arc's resident J3DModelData, which a new Link
    // uses too.
    J3DModelData* current_data = *(J3DModelData**)((u8*)link + DAPY_LK_C_MPCLMODELDATA_OFFSET);
    if (current_data == 0) {
        mailbox->progress = best_progress;
        return;
    }
    // A non-NULL mismatch is a rebuild only if Link.arc itself now
    // hands out that pointer. Anything else (e.g. a stale actor read
    // mid-teardown) is treated like "not ready": wait, keep the models.
    if (mini_link_state != 0 && current_data != mini_link_data) {
        if (current_data != (J3DModelData*)dRes_getRes_byIdx(
                PROBE_ARCNAME, PROBE_BDL_IDX, MOBJECT_INFO, OBJECT_INFO_COUNT)) {
            mailbox->progress = best_progress;
            return;
        }
        mini_link_reset_state();
    }

    // Progress encoding (reserved 20-29, beats puppet states 1-10):
    //   20 = getRes returned NULL (archive not resident yet — retry next frame)
    //   21 = mDoExt_J3DModel__create returned NULL (gave up)
    //   22 = J3DModel created; rendering each frame
    if (mini_link_state == 0) {
        // Link.arc is mounted once at boot (d_s_logo.cpp:876) and stays
        // resident. We've already waited 300 frames + confirmed Link's
        // mpCLModelData != NULL above. Runs once per session (state 0 is
        // only re-entered by mini_link_reset_state on a real re-mount).
        mini_link_data = (J3DModelData*)dRes_getRes_byIdx(
            PROBE_ARCNAME, PROBE_BDL_IDX,
            MOBJECT_INFO, OBJECT_INFO_COUNT
        );
        // The core's face snapshot (face_publish_local) and our face
        // bracket read Link's face tev_blocks from this model data.
        face_bind(mini_link_data);
        if (!mini_link_data) {
            if (best_progress < 20) best_progress = 20;
        } else {
            // ArchiveHeap was a guess — both ZeldaHeap and ArchiveHeap
            // were confirmed by bisect to starve sky rendering when
            // modelEntryDL subsequently submits, and the allocation
            // itself (without any submission) was proven harmless.
            // So the heap pick probably doesn't matter for the sky bug —
            // the real culprit is modelEntryDL. Leaving ArchiveHeap as
            // the destination for now because it's at least the same
            // heap Link.arc itself lives in. See docs/05 "Mini-Link
            // render pipeline" for the full investigation.

            // Eye-fix (v0.1.4 attempt): force open-pupil texNo on Link's
            // eye materials BEFORE creating mini-Link's private matpackets,
            // so the baked material DLs capture a valid "eyes open" state.
            // Without this, mini-Link's private DL bakes whatever texNo
            // btp happened to have set at creation time — often a closed
            // or mid-blink value that renders as "no pupils" on remote
            // Links. Link #1 itself is unaffected because Link #1 reads
            // the shared DL which btp patches live; only mini-Link's
            // private DLs see the forced value. Restore immediately
            // after create so Link #1's eyes keep blinking correctly.
            //
            // Material/texture mapping discovered 2026-04-22 via Go's
            // tint-material diagnostic:
            //   mat[1] stage 1 = left pupil
            //   mat[4] stage 1 = right pupil
            //   tex 0x0027 = open-pupil texture
            //
            // Layout offsets (zeldaret/tww commit 6aa7ba91):
            //   J3DModelData + 0x58 = J3DMaterialTable
            //     + 0x08 = J3DMaterial** (material pointer array)
            //   J3DMaterial + 0x2C = J3DTevBlock*
            //   J3DTevBlock + 0x08 + stage*2 = mTexNo[stage] (u16)
            u32 matArrPtrEye = *(u32*)((u8*)mini_link_data + 0x58 + 0x08);
            u16 saved_eye_tex[2] = {0, 0};
            u16* eye_tex_ptrs[2] = {0, 0};
            {
                int eye_mat_idx[2] = {1, 4};
                int em;
                for (em = 0; em < 2; em++) {
                    u32 matPtr = *(u32*)(matArrPtrEye + (u32)eye_mat_idx[em] * 4);
                    if (!matPtr) continue;
                    u32 tevBlock = *(u32*)(matPtr + 0x2C);
                    if (!tevBlock) continue;
                    u16* tex1 = (u16*)(tevBlock + 0x0A); // stage 1
                    saved_eye_tex[em] = *tex1;
                    eye_tex_ptrs[em] = tex1;
                    *tex1 = 0x0027;
                }
            }

            JKRHeap* targetHeap = mDoExt_getArchiveHeap();
            JKRHeap* oldHeap = JKRHeap_becomeCurrentHeap(targetHeap);
            int li;
            int created = 0;
            for (li = 0; li < MAX_REMOTE_LINKS; li++) {
                // flag=0 (not 0x80000): make createMatPacket allocate a
                // PRIVATE display list per material per instance. Flag
                // 0x80000 ("Unk80000" in tww decomp) points every
                // instance's mpMatPacket[i].mpDisplayListObj at the
                // same shared object owned by J3DModelData — each
                // J3DModel::entry() call's makeDisplayList() patches
                // that single buffer, so the LAST writer wins and all
                // N instances render the last-submitted pose. With
                // Args: (modelData, modelFlag=0, differedFlag=0x37221222).
                //
                // modelFlag=0 looks like it should give private per-instance
                // material DLs (J3DModel::createMatPacket:296-309), but
                // mDoExt_J3DModel__create internally FORCES modelFlag to
                // 0x80000 (shared) when modelData->getModelDataType() == 1
                // (which Link is). So we always end up in the shared-DL
                // branch — that's fine: the shared material DL is a shape
                // template, not where animated state lives.
                //
                // The differedFlag is what actually matters for animation.
                // It's a packed encoding of LightObjNum / TexGenNum /
                // TexNoNum / TevStageNum that sizes the per-instance
                // DOUBLE-BUFFERED SHAPE DL (where J3DMaterial::load writes
                // each frame's TX_SETIMAGE3 / TEV / mtx BP commands).
                //
                // Session 7 used 0x11000022 (the ubiquitous WW actor
                // default — see d_a_*.cpp). That has TexNoNum=0, meaning
                // ZERO space allocated for per-frame texno writes. Static
                // actors are fine, but Link's eyes (btp animates 2 texnos:
                // stage 0 + stage 1) had nowhere to write — so eye decals
                // and mouth froze on bake-time values.
                //
                // Real Link's body uses 0x37221222 (d_a_player_main.cpp:
                // 11985 — initModel(&mpCLModel, LINK_BDL_CL, 0x37221222)).
                // That has TexNoNum=2, TexGenNum=2, TevStageNum=2,
                // LightObjNum=2, plus extra sDifferedRegister bits. Using
                // the same flag for mini-link gives the differed buffer
                // enough space for the per-frame texno writes that drive
                // eye / mouth animation. Confirmed via decomp matching.
                mini_link_models[li] = mDoExt_J3DModel__create(mini_link_data, 0, 0x37221222);
                if (mini_link_models[li]) created++;
                // NOTE: face-state vtable hook installation happens
                // lazily, gated on mailbox.face_hook_enable. See the
                // face-state hook block further down. Eager install
                // here was removed in session 9 v2 after the matpacket
                // stride assumption (0x40 vs actual 0x3C) was caught
                // by the install's own guard but left an ambiguous
                // failure mode.
            }
            JKRHeap_becomeCurrentHeap(oldHeap);

            // Restore the eye texNos so Link #1's btp can keep blinking
            // naturally. Our force-open write was ONLY for mini-Link's
            // private-DL bake to capture "open pupils"; Link #1 reads the
            // shared DL which btp still patches every frame.
            {
                int em;
                for (em = 0; em < 2; em++) {
                    if (eye_tex_ptrs[em]) {
                        *eye_tex_ptrs[em] = saved_eye_tex[em];
                    }
                }
            }

            if (!mini_link_models[0]) {
                // Slot 0 is required: the draw hook bails without it.
                // (Higher slots failing just means fewer remote Links — non-fatal.)
                mini_link_state = 0xFF;
                if (best_progress < 21) best_progress = 21;
            } else {
                mini_link_state = 1;
            }
        }
    }

    // Per-frame matrix write + modelEntryDL moved to the draw phase
    // (mp_draw_post, see end of file). modelEntryDL from this fapGm_Execute hook
    // corrupts sky rendering — moving it to the proper draw phase was
    // the plausible fix, but bisect showed modelEntryDL still breaks
    // sky even from draw phase. Cause is still open (see docs/05
    // "Mini-Link render pipeline"). This function now just creates the
    // J3DModel once; the per-frame matrix+submit happens inside Link's
    // own daPy_Draw callback where it at least doesn't crash.
    if (mini_link_state == 1) {
        if (best_progress < 22) best_progress = 22;
    }

    // Session 9: process face-state vtable hook arm/disarm. Cheap each
    // frame (just walks MAX_REMOTE_LINKS mini-link models and checks
    // a flag). The install function probes the matpacket layout
    // before writing — if the build's stride differs from what we
    // expect, it bails with an error code in face_hook_state.
    if (mini_link_state == 1) {
        face_hook_process_arm();
    }

    mailbox->progress = best_progress;
}

// --- Our-own copies of the eye-decal preset packets ------------------
// The static l_*Packet1/2 objects are SINGLETONS shared across the
// entire process. Link #1's daPy_lk_c::draw entries them once per
// frame as part of his recipe; if WE entryImm them too, Link #1's
// already-entered chain bottom (the first matpacket entered after
// l_onCupOffAupPacket2 in his Pass 1) still references that packet,
// and OUR re-entry overwrites the packet's mpNextPacket from NULL
// to our current bucket head — which forms a CYCLE when the chain
// walks reach the bottom and follow back through our overwrite.
// drawHead loops infinitely → game freezes.
//
// Fix: maintain our own J3DPacket-shaped objects with the SAME contents
// as the originals (so .draw() dispatches to the same setup AND any
// inner field reads — display list pointers, command stream pointers —
// see real game data, not BSS zeros). They have independent mpNextPacket
// fields once entryImm runs on them; re-entering them doesn't touch
// Link #1's static instances.
//
// SESSION 6: bumped buffer size from 0x10 to 0x1C bytes per packet, to
// match the originals' actual size in memory (the 4 statics are spaced
// exactly 0x1C apart at 0x803E46A4..0x803E46F8). The 0x10-byte version
// only copied the vtable; vtable-method reads of fields past +0x10
// (display-list pointer, GX command stream, etc.) read uninitialized
// BSS = zeros = GPU hangs at first chain walk through our packet.
// init_my_eye_packets now memcpys the FULL 0x1C bytes from each original.
//
// F1 (docs/05 "A third remote freezes both games"): ONE PER SLOT. The
// copies used to be four statics shared by every mini-Link, and
// run_eye_fix runs once per drawn slot, so with two remote Links each
// packet was entryImm'd into opa_p1 twice in one frame: the second
// entry rewrites the packet's mpNextPacket to the bucket head, which by
// then is a chain that already contains the same packet. That is the
// same cycle the paragraph above describes, between our own slots. The
// rule (each puppet owns its packets) is datstarkey/ww-online's
// (GameMod/CLAUDE.md, puppet_draw.c), MIT; no code was taken.
//
// The copies live in the mailbox slack (MP_EYE_PACKETS_OFF, mailbox_mp.h),
// not in T2's .bss: three slots are 0x150 bytes, more than T2 had free
// (WWMB_LAYOUT 3). Only this file writes them, and the game's entryImm
// their mpNextPacket; init_my_eye_packets copies them again every boot
// (my_packets_initialized is a T2 static, zeroed by the DOL load).
#define MY_PACKET_SIZE 0x1C
enum { MY_PKT_ON_CUP_OFF1, MY_PKT_ON_CUP_OFF2, MY_PKT_OFF_CUP_ON1, MY_PKT_OFF_CUP_ON2, MY_PKT_COUNT };
#define my_packets ((u32 (*)[MY_PKT_COUNT][MY_PACKET_SIZE / 4])(MAILBOX_ADDR + MP_EYE_PACKETS_OFF))
_Static_assert(MAX_REMOTE_LINKS * MY_PKT_COUNT * MY_PACKET_SIZE <= MP_EYE_PACKETS_SIZE &&
               (MP_EYE_PACKETS_OFF & 3) == 0 && MAILBOX_ADDR + MP_EYE_PACKETS_OFF + MP_EYE_PACKETS_SIZE <= 0x80414000,
               "mailbox_mp.h MP_EYE_PACKETS_* must hold every slot's eye packets below __OSArenaLo");
static int my_packets_initialized = 0;

static void init_my_eye_packets(void) {
    if (my_packets_initialized) return;
    // Copy the FULL packet (vtable + display-list pointer + any other
    // inner fields) from each original, once per slot. mpNextPacket
    // (+0x04) gets reset every time entryImm runs on the packet, so it
    // doesn't matter what's in there at copy time.
    static J3DPacket* const originals[MY_PKT_COUNT] = {
        L_ON_CUP_OFF_AUP_PACKET1, L_ON_CUP_OFF_AUP_PACKET2,
        L_OFF_CUP_ON_AUP_PACKET1, L_OFF_CUP_ON_AUP_PACKET2,
    };
    int s, p, k;
    for (s = 0; s < MAX_REMOTE_LINKS; s++) {
        for (p = 0; p < MY_PKT_COUNT; p++) {
            volatile u32* src = (volatile u32*)originals[p];
            for (k = 0; k < (int)(MY_PACKET_SIZE / 4); k++) my_packets[s][p][k] = src[k];
        }
    }
    my_packets_initialized = 1;
}

// --- Eye-decal recipe (item #9, attempt 4) ----------------------------
// Replicates daPy_lk_c::draw lines 1827-1881 (zeldaret/tww @ 6aa7ba91)
// for our mini-Link model so the face decals — pupils, eye outline,
// eyelids, eyebrows — render. mDoExt_modelEntryDL alone does a single-
// pass submission that puts the body in P1 and skips the four-pass
// Z-compare setup the eye decals need to overcome face self-occlusion.
//
// The recipe in summary (see d_a_player_main.cpp:1827-1881):
//   setListP0
//   Pass 1: l_onCupOffAupPacket2.entryOpa  + cl_eye/cl_mayu entryIn
//           (zOffBlend hide, zOn hide, zOffNone show)
//   Pass 2: l_offCupOnAupPacket2.entryOpa  + cl_eye/cl_mayu entryIn
//           (zOffBlend show, zOffNone hide)
//   Pass 3: hide all link_root mtls except face(2)+hair(5),
//           link_root.entryIn, re-set j3dSys.{mModel,mTexture}
//   Pass 4: l_onCupOffAupPacket1.entryOpa  + cl_eye/cl_mayu entryIn
//           (zOffBlend hide, zOn show, zOffNone hide)
//   Pass 5: l_offCupOnAupPacket1.entryOpa  (zOn hide)
//   restore link_root mtl vis
//   setListP1
//
// This is the full recipe, the old eye_fix_step 8. The stepwise probe
// levels 1-7 (and mailbox.eye_fix_step, which picked one) were deleted
// in Phase E0; docs/06-history.md has how they were used.
//
// Caller-provided invariants (must hold for safety):
//   - Link #1's daPy_lk_c::draw has already returned this frame
//     (otherwise his shape-vis state isn't at the post-restore values
//     we're toggling from).
//   - The j3dSys global has been restored to its pre-our-calc state
//     (so we're free to clobber + we'll restore to P1 + Link #1's
//     mModel at the end).
//   - mini-Link's calc has already run (mpDrawMtx is current for
//     entryIn's GX submission).
static void run_eye_fix(J3DModel* model, void* link1_actor,
                        J3DModelData* model_data, int slot) {
    if (!model || !link1_actor || !model_data) return;
    if (slot < 0 || slot >= MAX_REMOTE_LINKS) return;
    init_my_eye_packets();
    u32 (*pk)[MY_PACKET_SIZE / 4] = my_packets[slot];

    // j3dSys field slots.
    volatile u32* j3d_model_slot   = (volatile u32*)(J3D_SYS_ADDR + 0x38);
    volatile u32* j3d_texture_slot = (volatile u32*)(J3D_SYS_ADDR + J3D_SYS_M_TEXTURE_OFFSET);
    volatile u32* j3d_opabuf_slot  = (volatile u32*)(J3D_SYS_ADDR + J3D_SYS_DRAWBUFFER_OPA_OFFSET);
    volatile u32* j3d_xlubuf_slot  = (volatile u32*)(J3D_SYS_ADDR + J3D_SYS_DRAWBUFFER_XLU_OFFSET);

    // Drawlist pointers — what dComIfGd_setListP0/P1 inlines read from.
    u32 opa_p0 = *(volatile u32*)DRAWLIST_OPA_LIST_P0_PTR;
    u32 opa_p1 = *(volatile u32*)DRAWLIST_OPA_LIST_P1_PTR;
    u32 xlu_p1 = *(volatile u32*)DRAWLIST_XLU_LIST_P1_PTR;

    // Full j3dSys snapshot. run_eye_fix mutates several j3dSys fields
    // (mModel, mTexture, mDrawBuffer OPA/XLU). Plus J3DJoint::entryIn
    // writes drawbuffer+0x1C (matrix-ptr) on both OPA and XLU buffers.
    // Diagnostic at step=4 showed Link #1's NEXT-FRAME draw silently
    // skipping his entire eye-decal four-pass recipe (no l_*AupPacket1/2
    // entries in opa_p0, and a totally different set of his mat-packets
    // submitted) — which points at residual j3dSys state being
    // load-bearing across frames. Snapshot at start, restore at end so
    // run_eye_fix is fully non-destructive.
    //
    // Also restore the three drawbuffer+0x1C matrix-ptr fields entryIn
    // clobbered (those live in heap, not in j3dSys, so the j3dSys
    // memcpy doesn't catch them).
    static u32 eye_fix_j3dsys_snapshot[J3D_SYS_SIZE / 4];
    volatile u32* j3dsys_words = (volatile u32*)J3D_SYS_ADDR;
    int eye_n;
    for (eye_n = 0; eye_n < (int)(J3D_SYS_SIZE / 4); eye_n++) {
        eye_fix_j3dsys_snapshot[eye_n] = j3dsys_words[eye_n];
    }
    u32 saved_opa_p0_mtx = *(volatile u32*)(opa_p0 + 0x1C);
    u32 saved_opa_p1_mtx = *(volatile u32*)(opa_p1 + 0x1C);
    u32 saved_xlu_p1_mtx = *(volatile u32*)(xlu_p1 + 0x1C);

    // J3DTexture* shared via J3DModelData → J3DMaterialTable → mTexture.
    u32 model_tex = *(volatile u32*)((u8*)model_data + J3DMODELDATA_TEXTURE_OFFSET);

    // Joint pointers (shared modelData).
    void* joint_arr_void = *(void**)((u8*)model_data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    if (!joint_arr_void) return;
    J3DJoint** joint_arr = (J3DJoint**)joint_arr_void;
    J3DJoint* link_root = joint_arr[0x00];
    J3DJoint* cl_eye    = joint_arr[LINK_CL_EYE_JOINT_INDEX];
    J3DJoint* cl_mayu   = joint_arr[LINK_CL_MAYU_JOINT_INDEX];
    if (!link_root || !cl_eye || !cl_mayu) return;

    // Shape-vis arrays (4 J3DShape* each) on Link #1's actor instance.
    J3DShape** zoff_blend = (J3DShape**)((u8*)link1_actor + DAPY_MP_Z_OFF_BLEND_SHAPE_OFFSET);
    J3DShape** zoff_none  = (J3DShape**)((u8*)link1_actor + DAPY_MP_Z_OFF_NONE_SHAPE_OFFSET);
    J3DShape** zon        = (J3DShape**)((u8*)link1_actor + DAPY_MP_Z_ON_SHAPE_OFFSET);

    // SESSION 6: route the recipe to opa_p1 instead of opa_p0. Session 5
    // diagnosed that run_eye_fix's entryIn calls clobber opa_p0's bucket
    // chain (`reset bucket[0] head and rewire the chain destructively`),
    // dropping 9 of Link's 18 matpackets — including face and hair —
    // from his draw. j3dSys+0x48 (= mDrawBuffer OPA slot) is what
    // entryIn writes drawbuffer+0x1C through, AND what entryImm
    // implicitly targets when called via the variant that doesn't take
    // a drawbuffer arg. By pointing j3dSys.opa at opa_p1 instead, the
    // recipe's chain mutations land in opa_p1's bucket — completely
    // separate from Link's opa_p0 chain. Mini-link's body (submitted
    // via mDoExt_modelEntryDL after run_eye_fix) ALSO already goes to
    // opa_p1 (set explicitly at the end of this function), so the
    // recipe + body now share the same drawlist. Local Link is
    // untouched.
    *j3d_model_slot   = (u32)model;
    *j3d_texture_slot = model_tex;
    *j3d_opabuf_slot  = opa_p1;   // route entryImm/entryIn into P1
    *j3d_xlubuf_slot  = opa_p1;

    int i;

    // Pass 1: packet 2 with cup-on/aup-off; hide zOffBlend and zOn, show
    // zOffNone; enter the eye and brow joints.
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)pk[MY_PKT_ON_CUP_OFF2], 0);
    for (i = 0; i < 4; i++) {
        if (zoff_blend[i]) *(volatile u32*)((u8*)zoff_blend[i] + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
        if (zon[i])        *(volatile u32*)((u8*)zon[i]        + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
        if (zoff_none[i])  *(volatile u32*)((u8*)zoff_none[i]  + J3DSHAPE_FLAGS_OFFSET) &= ~J3DSHAPE_FLAG_HIDE;
    }
    J3DJoint_entryIn(cl_eye);
    J3DJoint_entryIn(cl_mayu);

    // Pass 2: packet 2 with cup-off/aup-on, swap to zOffBlendShape.
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)pk[MY_PKT_OFF_CUP_ON2], 0);
    for (i = 0; i < 4; i++) {
        if (zoff_blend[i]) *(volatile u32*)((u8*)zoff_blend[i] + J3DSHAPE_FLAGS_OFFSET) &= ~J3DSHAPE_FLAG_HIDE;
        if (zoff_none[i])  *(volatile u32*)((u8*)zoff_none[i]  + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
    }
    J3DJoint_entryIn(cl_eye);
    J3DJoint_entryIn(cl_mayu);

    // Pass 3 (link_root entryIn) is SKIPPED in our setup. The decomp uses
    // it to enter face+hair into the chain at the recipe-middle position
    // for proper Z-state interaction with eye decals. But re-entering
    // link_root via mDoExt later (which we need for body parts: tunic,
    // sleeves, boots, hat, ears, etc.) creates a cycle on the 6 mtls
    // Pass 3 entered. Trade-off: face+hair render via mDoExt with normal
    // chain position instead of recipe-middle.

    // Pass 4: packet 1 with cup-on/aup-off, swap to zOnShape.
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)pk[MY_PKT_ON_CUP_OFF1], 0);
    for (i = 0; i < 4; i++) {
        if (zoff_blend[i]) *(volatile u32*)((u8*)zoff_blend[i] + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
        if (zon[i])        *(volatile u32*)((u8*)zon[i]        + J3DSHAPE_FLAGS_OFFSET) &= ~J3DSHAPE_FLAG_HIDE;
        if (zoff_none[i])  *(volatile u32*)((u8*)zoff_none[i]  + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
    }
    J3DJoint_entryIn(cl_eye);
    J3DJoint_entryIn(cl_mayu);

    // Pass 5: packet 1 with cup-off/aup-on, then hide zOn so the
    // shape-vis state at exit matches Link #1's post-recipe state
    // (everything hidden, ready for next frame's first toggle).
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)pk[MY_PKT_OFF_CUP_ON1], 0);
    for (i = 0; i < 4; i++) {
        if (zon[i]) *(volatile u32*)((u8*)zon[i] + J3DSHAPE_FLAGS_OFFSET) |= J3DSHAPE_FLAG_HIDE;
    }

    // Restore j3dSys to its pre-our-work state. mDoExt_modelEntryDL
    // (called right after this function returns) takes the model as an
    // argument and will set j3dSys.mModel itself via its internal
    // J3DModel::entry call, so we don't need to leave mini-Link in
    // j3dSys for that. Restoring also keeps NEXT-FRAME Link #1's draw
    // from seeing stale state from us.
    for (eye_n = 0; eye_n < (int)(J3D_SYS_SIZE / 4); eye_n++) {
        j3dsys_words[eye_n] = eye_fix_j3dsys_snapshot[eye_n];
    }

    // After the restore, j3dSys.drawbuffer is whatever Link #1 left it
    // at — empirically that's NOT opa_p1 (it's some other OPA list like
    // dl@0x803CA940 = 0x8068BF20). For mini-Link's body submission via
    // mDoExt_modelEntryDL to land in the standard P1 list (where normal
    // actor bodies render), force-set the drawbuffer slots to P1 here.
    *j3d_opabuf_slot = opa_p1;
    *j3d_xlubuf_slot = xlu_p1;

    // Restore drawbuffer+0x1C matrix pointers that entryIn clobbered
    // (these live in the J3DDrawBuffer heap objects, not in j3dSys).
    *(volatile u32*)(opa_p0 + 0x1C) = saved_opa_p0_mtx;
    *(volatile u32*)(opa_p1 + 0x1C) = saved_opa_p1_mtx;
    *(volatile u32*)(xlu_p1 + 0x1C) = saved_xlu_p1_mtx;
}

// --- Casual-clothes head fix (issue #43) ------------------------------
// mini-Link shares Link #1's J3DModelData, so it also shares every
// J3DShape hide flag, and J3DJoint::entryIn skips a hidden shape when
// the model is entered (J3DJoint.cpp:235). daPy_lk_c::draw shows all
// link_root materials again after its body entry (d_a_player_main.cpp
// :1888-1892), but drawShadow runs after that, at the end of draw
// (:2024), and in casual clothes it hides link_root material 4, the hat
// "ear(3)" (:1566-1573), and leaves it hidden until the next frame's
// draw shows it (:1749-1755). daPy_draw_hook enters mini-Link after
// draw returns, so the hat is still hidden, and the CL head has no
// back without it: Link fills that with the separate mpKatsuraModel
// hair (:1897-1903), which mini-Link does not draw.
//
// So around mini-Link's entry, show every link_root material and put
// back the ones that were hidden. The shadow draws later in the frame
// and still sees Link's hat hidden. Only link_root: the other joints
// carry state Link sets on purpose (the scabbard CL_PODA is hidden with
// no sword, :1869-1879), and mini-Link keeps mirroring it.
static u32 mini_link_shapes_force_show(J3DModelData* model_data) {
    u32 shown = 0;
    void* joint_arr_void = *(void**)((u8*)model_data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    if (!joint_arr_void) return 0;
    J3DJoint* link_root = ((J3DJoint**)joint_arr_void)[LINK_CL_ROOT_JOINT_INDEX];
    if (!link_root) return 0;
    u32 mtl = *(volatile u32*)((u8*)link_root + J3DJOINT_MESH_OFFSET);
    int i;
    for (i = 0; i < 32 && mtl >= 0x80000000u && mtl < MEM1_END; i++) {
        u32 shape = *(volatile u32*)(mtl + J3DMATERIAL_SHAPE_OFFSET);
        if (shape >= 0x80000000u && shape < MEM1_END) {
            volatile u32* flags = (volatile u32*)(shape + J3DSHAPE_FLAGS_OFFSET);
            if (*flags & J3DSHAPE_FLAG_HIDE) {
                *flags &= ~J3DSHAPE_FLAG_HIDE;
                shown |= 1u << i;
            }
        }
        mtl = *(volatile u32*)(mtl + J3DMATERIAL_NEXT_OFFSET);
    }
    return shown;
}

// Hide again the link_root materials mini_link_shapes_force_show showed.
static void mini_link_shapes_restore(J3DModelData* model_data, u32 shown) {
    if (!shown) return;
    J3DJoint* link_root = ((J3DJoint**)*(void**)((u8*)model_data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET))[LINK_CL_ROOT_JOINT_INDEX];
    u32 mtl = *(volatile u32*)((u8*)link_root + J3DJOINT_MESH_OFFSET);
    int i;
    for (i = 0; i < 32 && mtl >= 0x80000000u && mtl < MEM1_END; i++) {
        u32 shape = *(volatile u32*)(mtl + J3DMATERIAL_SHAPE_OFFSET);
        if (shown & (1u << i)) {
            *(volatile u32*)(shape + J3DSHAPE_FLAGS_OFFSET) |= J3DSHAPE_FLAG_HIDE;
        }
        mtl = *(volatile u32*)(mtl + J3DMATERIAL_NEXT_OFFSET);
    }
}

// mDoExt_modelEntryDL for a mini-Link model, with the full link_root
// head shown (see above), opened up into the game's own four steps so
// the slot's face can go in where it takes (F20, docs/05 "Remote faces
// were the local player's"). On Link's model data (type 1, never
// locked: J3DModelLoader locks only a BDL without a MAT3 section, and
// daPy_lk_c::draw takes this branch every frame) mDoExt_modelEntryDL is
// mDoExt_modelDiff + viewCalc = calcMaterial, diff, entry, viewCalc
// (m_Do_ext.cpp:509-513, 563-576). calcMaterial runs every material's
// J3DMaterialAnm, which writes the LOCAL btp texno into the SHARED tev
// blocks (J3DMaterialAnm.cpp:51-62); diff then bakes the tev blocks
// into THIS model's double-buffered differed display lists
// (J3DMaterial::diff -> J3DTevBlockPatched::diffTexNo -> loadTexNo).
// Nothing reads the tev blocks after that for this model, and Link's
// own bake already happened in his draw. So: swap after calcMaterial,
// restore after diff. The old bracket swapped before mDoExt, and
// calcMaterial put the local face back a few instructions before the
// bake, which is why every remote wore the local player's face.
static void mini_link_entry_dl(J3DModel* model, J3DModelData* model_data, int slot) {
    u32 shown = mini_link_shapes_force_show(model_data);
    J3DModel_calcMaterial(model);
    face_swap_for_slot(slot);
    J3DModel_diff(model);
    face_restore();
    J3DModel_entry(model);
    J3DModel_viewCalc(model);
    mini_link_shapes_restore(model_data, shown);
}

// Lock-bit approach (J3DModel_lock + skip-unlock + mDoExt) was tried and
// did NOT prevent the cycle: J3DJoint::entryIn at 0x802F5964 reads the
// lock bit but only branches to a DIFFERENT rendering path (0x802F59CC),
// which still pushes the matpacket onto the drawbuffer chain. So both
// locked and unlocked code paths add to the chain — the lock bit selects
// pipeline state, not skip-vs-enter. Diagnostic kept here as a note so
// future sessions don't re-investigate.
//
// What DOES gate entry per-joint: J3DModel::entry at 0x802EAADC reads
// joint+0x60 (= J3DJOINT_MESH_OFFSET, the mMesh material chain ptr) and
// SKIPS the joint if zero (`beq → 0x802EAAF4` advances loop counter).
// So zeroing cl_eye/cl_mayu/link_root's mMesh before mDoExt makes
// J3DModel::entry's joint loop skip exactly the joints whose matpackets
// the recipe already entered — no double-entry, no cycle, body still
// renders via the other joints (arms, legs, hat, ears, nose, mouth).
//
// Joint pointers come from J3DModelData → joint array (shared across
// all instances using the same modelData), so this temporarily affects
// Link's joints too. Safe because daPy_draw_hook runs AFTER Link's draw
// has completed for this frame; the restore happens before any other
// code can read these joints.
// Pass 3 (link_root entryIn) is skipped in run_eye_fix, so link_root
// never enters via the recipe and only cl_eye/cl_mayu need skipping here.
static void mDoExt_skip_recipe_joints(J3DModel* model, J3DModelData* model_data, int slot) {
    void* joint_arr_void = *(void**)((u8*)model_data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    if (!joint_arr_void) {
        mini_link_entry_dl(model, model_data, slot);
        return;
    }
    J3DJoint** joint_arr = (J3DJoint**)joint_arr_void;
    J3DJoint* cl_eye    = joint_arr[LINK_CL_EYE_JOINT_INDEX];
    J3DJoint* cl_mayu   = joint_arr[LINK_CL_MAYU_JOINT_INDEX];

    u32 saved_eye = 0, saved_mayu = 0;
    if (cl_eye)  { saved_eye  = *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET); *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET) = 0; }
    if (cl_mayu) { saved_mayu = *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET); *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET) = 0; }

    mini_link_entry_dl(model, model_data, slot);

    if (cl_eye)  *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET) = saved_eye;
    if (cl_mayu) *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET) = saved_mayu;
}

// The REL's visuals hook (include/wwvis.h, inject/rel/visuals.c): the
// remote's scabbard shape before the body entry (phase 0), its sword and
// shield and name tag after it (phase 1). The word is the REL's while it
// is linked (_prolog writes it, _epilog clears it first; main01_init
// zeroes it at boot), so T2 only branches. noinline + Os: T2 budget.
typedef void (*mp_vis_fn)(int slot, J3DModel* model, void* link, int phase);
// Every slot T2 hands the hook must have its names, equip and models row
// (the REL drops slot >= WWVIS_SLOTS; TestVisSlotsCoverRemoteLinks is the Go side).
_Static_assert(WWVIS_SLOTS >= MAX_REMOTE_LINKS, "wwvis.h WWVIS_SLOTS must cover every mini-Link slot");
__attribute__((noinline, optimize("Os")))
static void mp_vis_hook(int slot, void* link, int phase) {
    mp_vis_fn fn = (mp_vis_fn)*(volatile u32*)(MAILBOX_ADDR + WWVIS_OFF + WWVIS_DRAW_FN_OFF);
    if (fn) fn(slot, mini_link_models[slot], link, phase);
}

// --- Draw phase (mods post) ------------------------------------------
// mods.h: called by daPy_draw_hook after Link #1's draw and the core's
// post-draw steps; `this_` is Link #1. In shadow_mode 5 (pose feed),
// calc and submit each mini-Link slot Go has written a pose to.
void mp_draw_post(void* this_) {
    // Stage-load safety (draw side). If this Link isn't built yet or
    // reads a model data other than the one our models were made from,
    // skip our submission this frame. Never reset here: the models are
    // still valid (see "Lifetimes" above mini_link_reset_state), and
    // multiplayer_update alone decides whether a real rebuild is due.
    if (mini_link_models[0] == 0 || mini_link_state != 1) {
        return;
    }
    J3DModelData* current_data = *(J3DModelData**)((u8*)this_ + DAPY_LK_C_MPCLMODELDATA_OFFSET);
    if (current_data != mini_link_data || current_data == 0) {
        return;
    }

    // shadow_mode 5 is the pose feed; 0 (the boot value) is off. The
    // mailbox is zero-initialized at boot, so a fresh patched-ISO game
    // looks vanilla until puppet-sync writes 5. The dev modes 1-4
    // (mirror, freeze, no-op calc, echo ring) were deleted in Phase E0
    // and now draw nothing, like 0.
    if (mailbox->shadow_mode != 5) {
        return;
    }

    // Lazy-init the no-op J3DMtxCalc for the calc below.
    // Vtable of 16 blr stubs; object = {vtable_ptr, padding}.
    if (!noop_mtxcalc_initialized) {
        int vi;
        for (vi = 0; vi < 16; vi++) {
            noop_mtxcalc_vtable[vi] = (u32)&noop_stub;
        }
        noop_mtxcalc_obj[0] = (u32)&noop_mtxcalc_vtable[0];
        noop_mtxcalc_initialized = 1;
    }
    // Capture the joint count once; same Link rig for every slot.
    if (pose_joint_num == 0) {
        u16 jn = *(volatile u16*)((u8*)mini_link_data + J3DMODELDATA_JOINT_NUM_OFFSET);
        if (jn > 0 && jn <= 128) {
            pose_joint_num = (int)jn;
            pose_buf_u32s = pose_joint_num * 12;
        }
    }

    // Full 0x128-byte j3dSys snapshot around every calc below: calc()
    // leaves mini-Link pointers in j3dSys that Link's post-draw code
    // would otherwise deref.
    static u32 j3dsys_snapshot[J3D_SYS_SIZE / 4];
    volatile u32* j3dsys = (volatile u32*)J3D_SYS_ADDR;
    int n;
    for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
        j3dsys_snapshot[n] = j3dsys[n];
    }

    // Pose feed for ALL N remote-Link slots. Each slot owns its own
    // J3DModel (mini_link_models[i]) and its own GameHeap-resident pose
    // buffer (pose_bufs[i]); the mailbox arrays let Go write per-slot
    // poses independently, so N remote players each drive their own Link.
    //
    // Per slot: copy Go's pose over mpNodeMtx and run ONE calc with the
    // no-op walker, whose envelope pass rebuilds mpDrawMtxBuf from our
    // pose. Overwriting mpDrawMtxBuf directly isn't possible: its layout
    // is one entry per envelope combination, not per joint.
    //
    // No calc with Link's REAL walker here (F19, 2026-10-18, docs/05
    // "Local Link's feet squished on slopes"). Through v0.4.1 a first
    // calc ran Link's walker over the shared J3DModelData to "populate
    // mpNodeMtx" before the copy. That walk runs Link's joint callbacks
    // with mUserArea = Link #1, and they write into LINK #1, not into
    // the model being calc'd: daPy_lk_c::jointCB1 (the RTOE callback,
    // d_a_player_main.cpp:488-545) re-applies the leg IK rotation onto
    // Link #1's own mpCLModel leg matrices, and jointAfterCB (:390-413)
    // overwrites mFootData[].field_0x088 with the post-IK matrices that
    // next frame's footBgCheck / setLegAngle solve from. Flat ground hid
    // it (IK angles near 0); on slopes the LOCAL Link's feet and legs
    // squished, with no remote needed: shadow_mode 5 alone did it, since
    // the walk ran for every created model before the pose_seqs gate.
    // Verified live on the Outset pier ramp: with shadow_mode 5 Link #1's
    // LFOOT mpNodeMtx read after this hook differed from the copy
    // pose_publish took before it (a large rotation, foot 7 units up),
    // and matched again at shadow_mode 0. Go's pose overwrites every
    // joint anyway, so the walk was wasted work as well as harmful.
    volatile u32* bc_slot = (volatile u32*)((u8*)mini_link_data + J3DMODELDATA_BASIC_MTXCALC_OFFSET);
    u32 bc_saved = *bc_slot;
    cXyz* link_pos = ACTOR_POS(this_);
    J3DModel* link1_model = *(J3DModel**)((u8*)this_ + DAPY_LK_C_MPCLMODEL_OFFSET);
    int slot;
    for (slot = 0; slot < MAX_REMOTE_LINKS; slot++) {
        J3DModel* model = mini_link_models[slot];
        if (!model) continue;

        // Base matrix: a per-slot X offset from Link. Nothing walks it
        // now (the no-op walker skips init too) and Go's pose carries
        // the remote's world position, but draw-side code reads the
        // model's base matrix, so keep it sane.
        Mtx* mtx = (Mtx*)((u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET);
        (*mtx)[0][0] = 1.0f; (*mtx)[0][1] = 0.0f; (*mtx)[0][2] = 0.0f;
        (*mtx)[0][3] = link_pos->x + (f32)(100 + slot * 100);
        (*mtx)[1][0] = 0.0f; (*mtx)[1][1] = 1.0f; (*mtx)[1][2] = 0.0f;
        (*mtx)[1][3] = link_pos->y;
        (*mtx)[2][0] = 0.0f; (*mtx)[2][1] = 0.0f; (*mtx)[2][2] = 1.0f;
        (*mtx)[2][3] = link_pos->z;
        // Link's joint callbacks (bound to the shared J3DModelData via
        // J3DJoint subclasses) recover the owning daPy_lk_c via mUserArea
        // (offset 0x14 in J3DModel). The no-op walker never fires them,
        // but the draw-time entry (J3DJoint::entryIn, the eye-fix recipe)
        // runs against this model with j3dSys pointing at it, so keep
        // Link #1 reachable rather than a NULL (the old crash at PC
        // 0x8010C53C inside checkEquipAnime).
        *(volatile u32*)((u8*)model + J3DMODEL_USER_AREA_OFFSET) = (u32)this_;

        volatile u32* node_mtx = (volatile u32*)(*(u32*)((u8*)model + J3DMODEL_MP_NODE_MTX_OFFSET));

        // Lazy-alloc this slot's pose buffer. Seed it from Link #1's own
        // mpNodeMtx (same rig, and Link's draw has just computed it this
        // frame): a copy, no calc. Nothing renders from the seed (the
        // copy below and the submit loop both wait for pose_seqs[slot]
        // != 0, and Go writes the whole buffer before bumping the seq),
        // so this only keeps the buffer sane instead of uninitialized
        // JKRHeap bytes.
        if (!pose_bufs[slot]) {
            if (pose_buf_u32s == 0) {
                mailbox->pose_buf_states[slot] = 0xFE;
                continue;
            }
            pose_bufs[slot] = (u8*)JKRHeap_alloc((u32)(pose_buf_u32s * 4), 0x20, mDoExt_getGameHeap());
            if (!pose_bufs[slot]) {
                mailbox->pose_buf_states[slot] = 0xFD;
                continue;
            }
            volatile u32* seed_src = 0;
            if (link1_model) {
                seed_src = (volatile u32*)(*(u32*)((u8*)link1_model + J3DMODEL_MP_NODE_MTX_OFFSET));
            }
            if (seed_src) {
                volatile u32* seed_dst = (volatile u32*)pose_bufs[slot];
                int si;
                for (si = 0; si < pose_buf_u32s; si++) {
                    seed_dst[si] = seed_src[si];
                }
            }
            mailbox->pose_buf_ptrs[slot] = (u32)pose_bufs[slot];
            mailbox->pose_joint_counts[slot] = (u16)pose_joint_num;
            mailbox->pose_buf_states[slot] = 1;
        }

        // Skip if Go has never written a pose to this slot (it bumps
        // pose_seqs[slot] each write); the render loop below skips it too.
        if (mailbox->pose_seqs[slot] == 0 || !node_mtx) continue;

        volatile u32* src = (volatile u32*)pose_bufs[slot];
        int pi;
        for (pi = 0; pi < pose_buf_u32s; pi++) {
            node_mtx[pi] = src[pi];
        }
        *bc_slot = (u32)&noop_mtxcalc_obj[0];
        J3DModel_calc(model);
        *bc_slot = bc_saved;
    }

    // Restore j3dSys after ALL calcs, so no calc's pollution leaks to
    // Link's post-draw code.
    for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
        j3dsys[n] = j3dsys_snapshot[n];
    }

    // Submit every slot Go is driving. A slot renders only once Go has
    // written at least one pose to it; before that it would show the
    // seed pose (Link #1 mirrored) as a ghost.
    //
    // Eye-fix runs after the j3dSys restore so it operates on Link #1's
    // post-draw state (the recipe assumes that's the baseline, since
    // that's what daPy_lk_c::draw runs from), then mDoExt submits the
    // body with cl_eye+cl_mayu skipped (the recipe already entered them;
    // see docs/06-history.md "Eye-fix attempt 4 session 7"). The tunic
    // swap recolors only this slot; the face swap sits inside
    // mini_link_entry_dl, between calcMaterial and diff (F20), and is a
    // no-op unless face_hook_enable=1 AND face_seqs[slot]!=0. The
    // recipe's own entryIn calls bake nothing (the mini-Link's packets
    // are locked, so entryIn only chains them), so they may run first.
    for (slot = 0; slot < MAX_REMOTE_LINKS; slot++) {
        if (!mini_link_models[slot] || !pose_bufs[slot] || mailbox->pose_seqs[slot] == 0) continue;
        mp_vis_hook(slot, this_, WWVIS_PHASE_PRE);
        tunic_swap(this_, slot);
        run_eye_fix(mini_link_models[slot], this_, mini_link_data, slot);
        mDoExt_skip_recipe_joints(mini_link_models[slot], mini_link_data, slot);
        tunic_restore();
        mp_vis_hook(slot, this_, WWVIS_PHASE_POST);
    }

}
