// Wind Waker Multiplayer — injected code
// Two entry points, both hooked via Freighter's hook_branchlink:
//   main01_init — fires once from main01 (one-shot). Installs the address of
//     multiplayer_update at CALLBACK_PTR_ADDR so the per-frame shim can find it.
//   frame_shim  — naked asm. Fires every frame from a hooked `bl` site.
//     Saves LR, loads the callback pointer, calls through it via bctrl,
//     restores LR, returns. Intentionally tiny so that replacing a `bl`
//     instruction at the hook site is transparent (callers already assume
//     volatile-reg clobber from a real bl).
//
// Why the indirection? Freighter's hook_branchlink overwrites the target
// instruction outright — if we hook fapGm_Execute's first instruction the
// prologue's stwu is lost and the function crashes. Hooking a `bl` instead
// of a prologue instruction is safe, and the callback indirection lets us
// keep multiplayer_update as a normal C function (the naked shim handles the
// minimal ABI dance).

#include "game.h"
#include "mailbox.h"

// Where main01_init stashes the callback pointer. Must sit OUTSIDE our T2
// code section. History:
//   v1 (mod ~0x748 B): 0x80410700. Broke when mod grew past 0x700.
//   v2 (mod ~0x868 B): 0x80410808. Broke when mod grew past 0x800.
//   v3 (mod ~0x900 B): 0x80410908. Broke when mod grew past 0x900.
//   v4 (mod ~0x9A8 B): 0x80410F08 @ __OSArenaLo = 0x80411000.
//   v5 (mod ~0x11C8 B): 0x80411F08 @ __OSArenaLo = 0x80412000.
//   v6 (current, mod ~0x1FC8 B): 0x80412F08 @ __OSArenaLo = 0x80413000.
//       Eye-fix recipe pushed .text past 0x80411F00, so the whole
//       mailbox + __OSArenaLo pair shifted up by another 0x1000. See
//       inject/build.py for the matching OSInit patch.
// MUST match the `lwz 12, 0x2F08(12)` offset in frame_shim asm below.
#define CALLBACK_PTR_ADDR 0x80412F08

// Per-slot state. Parallel arrays to mailbox.puppets[]. pids[i] is the
// queued-spawn result from fopAcM_create; spawned[i] gates phase 2 sync.
static fpc_ProcID puppet_pids[MAX_PUPPETS];
static int puppet_spawned[MAX_PUPPETS];
// The Link actor the actor puppets were last checked against (see
// puppet_reap).
static fopAc_ac_c* puppet_owner = 0;
static int frame_count = 0;

// --- Mini-Link state (Option B from roadmap 06) ------------------------
// Render N independent Link models from OUR code, all sharing Link #1's
// already-loaded J3DModelData. No actors, no PROC_PLAYER spawn, no
// 704 KB heap carve-out per model. shadow_mode 5 (pose feed) drives all
// MAX_REMOTE_LINKS slots; every other shadow_mode value draws nothing
// (the mirror / freeze / echo dev modes 1-4 were deleted in Phase E0).
static J3DModelData* mini_link_data = 0;
static J3DModel* mini_link_models[MAX_REMOTE_LINKS] = {0};
// 0 = not tried yet, 1 = all models created + rendering, 0xFF = give up
static u8 mini_link_state = 0;

// No-op J3DMtxCalc for the pose feed's second calc. Swapped into
// J3DModelData->basicMtxCalc around that calc so it skips the joint walk
// (mpNodeMtx keeps the pose we copied in) while its envelope pass
// rebuilds mpDrawMtxBuf from that pose.
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
// each draw, then runs a second calc with the no-op walker to rebuild
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

// Forward decl so main01_init can take its address.
void multiplayer_update(void);
// Forward decl — hooked at 0x80108210 (the bl inside daPy_Draw), runs in
// draw phase. Calls the original Link draw impl, then submits mini-Link.
int daPy_draw_hook(void* this_);
// Forward decls for the face-state vtable hook (defined further down,
// search for "face-state network sync").
static int face_hook_install_for_model(void* model_v, int slot);
static int face_hook_uninstall_for_model(void* model_v);
static void face_hook_process_arm(void);

// face_emit_* statics live up here (not next to their definitions further
// down) because mini_link_reset_state references them on save reload, and
// C requires declarations to precede references.
static u8* face_emit_mat1_tev = 0;     // J3DTevBlock* for mat[1]  (left pupil)
static u8* face_emit_mat4_tev = 0;     // J3DTevBlock* for mat[4]  (right pupil)
static u8* face_emit_mat14_tev = 0;    // J3DTevBlock* for mat[14] (mouth A) — session 15
static u8* face_emit_mat15_tev = 0;    // J3DTevBlock* for mat[15] (mouth B) — session 15
static u32 face_emit_saved_mat1 = 0;   // 4 bytes at tev+0x08 (texNo[0]:u16 BE | texNo[1]:u16 BE)
static u32 face_emit_saved_mat4 = 0;
static u32 face_emit_saved_mat14 = 0;
static u32 face_emit_saved_mat15 = 0;
static int face_emit_swap_active = 0;
// Forward decl — defined alongside face_emit_swap_for_slot (search for
// "session 14"). Called from daPy_draw_hook entry to snapshot btp's
// natural value into the mailbox for race-free broadcast-pose capture.
static void face_emit_publish_local(void* model_data);

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
// face_seqs (Go owns those) and the face_emit tev_block cache (it points
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
    // The tev_blocks belong to the old J3DModelData's material table.
    // face_emit_resolve_tevblocks lazy-recaches on the next call.
    face_emit_mat1_tev = 0;
    face_emit_mat4_tev = 0;
    face_emit_mat14_tev = 0;
    face_emit_mat15_tev = 0;
    face_emit_swap_active = 0;
}

// Hooked to main01 (0x80006338) via hook_branchlink. Runs once.
// Publishes multiplayer_update's address for frame_shim to find.
void main01_init(void) {
    *(volatile u32*)CALLBACK_PTR_ADDR = (u32)&multiplayer_update;
    // WWMB header (Phase E1): magic, ABI, layout version and the
    // features word, for Go's contract check (wwapi.CheckContract).
    mailbox->hdr_magic = WWMB_MAGIC;
    mailbox->hdr_abi = WWMB_ABI;
    mailbox->hdr_layout = WWMB_LAYOUT;
    mailbox->features = WW_FEATURES;
    // Heartbeat: proves main01_init fired. frame_shim will start bumping
    // spawn_trigger once per-frame hook is wired.
    mailbox->spawn_trigger = 1;
}

// Naked asm per-frame dispatcher. Hooked at 0x80023204 inside fapGm_Execute,
// replacing `bl +0x2217A8` -> 0x802449AC. That call is critical (updates heap
// bookkeeping; first attempt that silently dropped it tripped
// `mDoExt_SaveCurrentHeap != 0`), so we REPLAY it here after running our own
// per-frame work. Caller set r3=0 before the replaced bl; we restore r3=0
// before replaying since multiplayer_update clobbers r3.
//
// Flow:
//   1. Save LR (will be clobbered by our internal bls)
//   2. Call multiplayer_update via callback pointer (null-safe until
//      main01_init publishes it)
//   3. Replay bl 0x802449AC with r3=0
//   4. Restore LR, return to caller (fapGm_Execute epilogue)
// Two-stage shim:
//   1. Call multiplayer_update via callback pointer (null-safe until main01_init
//      publishes it). Uses bctrl, which clobbers LR — but we saved the caller's
//      LR first so we can restore it.
//   2. Restore caller's LR (= 0x80023208, fapGm_Execute's post-bl address),
//      then TAIL-CALL 0x802449AC via bctr (does NOT touch LR). When 0x802449AC
//      blrs, control returns directly to 0x80023208 — byte-for-byte identical
//      to the original `bl +0x2217A8` semantics.
//
// Why the LR dance matters: 0x802449AC apparently does `mflr` internally and
// uses the value for heap bookkeeping. A bctrl-based replay made LR point into
// our shim, which tripped `mDoExt_SaveCurrentHeap != 0` within ~1 sec. The
// bare-tail-call variant (confirmed working) had no such issue because LR
// stayed = 0x80023208.
__attribute__((naked))
void frame_shim(void) {
    asm volatile(
        "mflr  0                \n"  // save caller's LR (0x80023208)
        "stwu  1, -0x20(1)      \n"
        "stw   0, 0x10(1)       \n"

        // --- multiplayer_update ---
        "lis   12, 0x8041       \n"
        "lwz   12, 0x2F08(12)   \n"  // = CALLBACK_PTR_ADDR (mailbox+0x08) = 0x80412F08
        "cmpwi 12, 0            \n"
        "beq-  1f               \n"
        "mtctr 12               \n"
        "bctrl                  \n"
        "1:                     \n"

        // --- restore frame + LR, tail-call 0x802449AC ---
        "lwz   0, 0x10(1)       \n"
        "addi  1, 1, 0x20       \n"
        "mtlr  0                \n"  // LR = 0x80023208 again
        "li    3, 0             \n"
        "lis   12, 0x8024       \n"
        "ori   12, 12, 0x49AC   \n"
        "mtctr 12               \n"
        "bctr                   \n"  // tail-call — 0x802449AC blrs to 0x80023208
    );
}

// Actor-puppet lifetime. Left alone, a puppet whose slot Go cleared
// lives on. Live, one left behind on Outset was gone after a stage
// change, but one spawned on the title screen outlived title -> file
// select -> the save's stage, where KAMOME's path_move read its spawn
// room's freed path (+0x304 mpPath, d_a_kamome.cpp:482) and the game
// halted ("Invalid read from 0x1a0d0013", PC 0x80f24230; docs/05). So
// we delete it ourselves: a slot's actor when Go clears the slot, and
// every actor when a stage change starts (NEXT_STAGE_ENABLE), when Link
// is gone (title, file select) or when Link's actor changes. A delete
// that can't happen yet (still being created) is retried every frame
// until it does; only then is the slot forgotten.
static void puppet_reap(int all) {
    int i;
    for (i = 0; i < MAX_PUPPETS; i++) {
        if (!puppet_spawned[i] || (!all && mailbox->puppets[i].active)) continue;
        fpc_ProcID pid = puppet_pids[i];
        if (fpcM_IsCreating(pid) || !fopAcM_deleteByID(pid)) continue;
        puppet_spawned[i] = 0;
        puppet_pids[i] = fpcM_ERROR_PROCESS_ID_e;
        mailbox->puppets[i].actor_ptr = 0;
    }
}

// Per-frame worker. Iterates the MAX_PUPPETS slots in the mailbox. For
// each slot whose `active` flag is set by Go, we either queue a spawn
// (phase 1) or sync pos/rot from the slot into the live actor (phase 2).
//
// Progress encodes "best state across slots this frame":
//    1 = entered, no active slot yet
//    3 = frame_count gate passed (~10 sec since boot)
//    5 = at least one spawn queued this frame
//    6 = a spawn returned ERROR_PROCESS_ID (queue full or proc not
//        registered)
//    8 = at least one slot is now spawned=1
//    9 = at least one live actor resolved and synced
//   10 = at least one slot's actor has been deleted (cleared spawn)
//
// Proxy-actor history (what's inside fopAcM_create for each slot):
//   PROC_PLAYER     -> OOMs GameHeap (~704 KB alloc vs ~245 KB free).
//   PROC_Obj_Barrel -> queued OK but froze game on next frame (archive
//                      not resident on Outset; actor loader spins).
//   PROC_GRASS      -> spawns and renders stably, but child sprites are
//                      baked at parent's birth pos.
//   PROC_TSUBO pm=0 -> "Always" archive. Needs m678=2 unhide poke.
//   PROC_KAMOME pm=0 -> "Always"-ish archive (resident on every island).
//                      Needs mSwitchNo=0 unhide poke. *Current default.*
//   PROC_NPC_FA1    -> fairy; renders briefly, self-deletes; respawn
//                      loops froze the game. Not pursued.
void multiplayer_update(void) {
    mailbox->spawn_trigger++;

    fopAc_ac_c* link = PLAYER_PTR_ARRAY[0];
    // Actor puppets: see puppet_reap.
    int stage_changing = NEXT_STAGE_ENABLE;
    puppet_reap(!link || link != puppet_owner || stage_changing);
    puppet_owner = link;
    if (!link) return;

    // Warp handler. Sits ABOVE the frame_count puppet-creation gate so
    // it fires the moment Link's actor pointer is valid (just after the
    // save-load-stable point). Triggered by Go bumping mailbox->warp_seq.
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

    frame_count++;
    if (frame_count < 300) {
        mailbox->progress = 1;
        return;
    }

    // Tunic colors (docs/09 §5.3): allocate the three per-slot images
    // ONCE, after Go has published their size. tunic_state never goes
    // back to 0, so a stage load or save reload never re-allocates (F18).
    if (mailbox->tunic_state == 0 && mailbox->tunic_buf_size != 0) {
        u8 st = 1;
        int t;
        for (t = 0; t < 3; t++) {
            if (!mailbox->tunic_bufs[t]) {
                void* p = JKRHeap_alloc(mailbox->tunic_buf_size, 0x20, mDoExt_getGameHeap());
                if (p) mailbox->tunic_bufs[t] = (u32)p;
                else   st = 0xFD;
            }
        }
        mailbox->tunic_state = st;
    }

    cXyz* link_pos = ACTOR_POS(link);
    csXyz* link_angle = ACTOR_ANGLE(link);
    s8 link_room = ACTOR_ROOM(link);

    u32 best_progress = 3;

    int i;
    for (i = 0; i < MAX_PUPPETS; i++) {
        volatile Puppet* slot = &mailbox->puppets[i];

        // Go cleared the slot: puppet_reap deleted its actor. No spawn
        // or sync while a stage change is under way either (the reap
        // deleted those actors; they respawn in the new stage).
        if (!slot->active || stage_changing) continue;

        // Phase 1: spawn on the first frame this slot is active. Seed the
        // slot's target pos with Link's current pos + a small offset (for
        // visibility before Go writes real coords), one slot to the side
        // per index so multiple puppets don't overlap on spawn.
        if (!puppet_spawned[i]) {
            slot->pos_x = link_pos->x + (f32)(100 + i * 50);
            slot->pos_y = link_pos->y;
            slot->pos_z = link_pos->z;

            // 2026-04-19 EXPERIMENT: slot 3 was PROC_TSUBO. Parked on
            // PROC_KAMOME for now so our probe J3DModel (built from the
            // same Tsubo J3DModelData) is the ONLY consumer of that data
            // — isolates the shared-ModelData confound in the sky-break
            // investigation. Revert alongside PROBE_ARCNAME when done.
            s16 proc;
            if (i == 1) proc = PROC_NPC_OB1;
            else        proc = PROC_KAMOME;
            fpc_ProcID pid = fopAcM_create(proc, 0, link_pos, link_room, link_angle, 0, -1, 0);
            if (pid == fpcM_ERROR_PROCESS_ID_e) {
                if (best_progress < 6) best_progress = 6;
                continue;
            }
            puppet_pids[i] = pid;
            puppet_spawned[i] = 1;
            slot->actor_ptr = (u32)pid;  // published as pid until phase 2 resolves
            if (best_progress < 8) best_progress = 8;
            continue;
        }

        // Phase 2: resolve pid to actor and write pos/rot.
        fopAc_ac_c* actor = 0;
        if (!fopAcM_SearchByID(puppet_pids[i], &actor) || !actor) {
            // Still being created: SearchByID can't see it yet. Wait;
            // forgetting its pid here would leak the actor.
            if (fpcM_IsCreating(puppet_pids[i])) continue;
            // Actor was deleted externally (or its create failed). Drop
            // our state; the next frame requests a fresh spawn.
            puppet_spawned[i] = 0;
            puppet_pids[i] = fpcM_ERROR_PROCESS_ID_e;
            slot->actor_ptr = 0;
            if (best_progress < 10) best_progress = 10;
            continue;
        }

        slot->actor_ptr = (u32)actor;

        cXyz* pos = ACTOR_POS(actor);
        pos->x = slot->pos_x;
        pos->y = slot->pos_y;
        pos->z = slot->pos_z;

        // Write rotation to both current.angle (AI/physics) and
        // shape_angle (visual). See rotation-sync notes in docs/06.
        csXyz* shape = ACTOR_SHAPE(actor);
        csXyz* angle = ACTOR_ANGLE(actor);
        shape->x = angle->x = slot->rot_x;
        shape->y = angle->y = slot->rot_y;
        shape->z = angle->z = slot->rot_z;

        if (best_progress < 9) best_progress = 9;
    }

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

    // Per-frame matrix write + modelEntryDL moved to daPy_draw_hook
    // (see end of file). modelEntryDL from this fapGm_Execute hook
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
#define MY_PACKET_SIZE 0x1C
static u32 my_packet_onCupOff1[MY_PACKET_SIZE / 4];
static u32 my_packet_onCupOff2[MY_PACKET_SIZE / 4];
static u32 my_packet_offCupOn1[MY_PACKET_SIZE / 4];
static u32 my_packet_offCupOn2[MY_PACKET_SIZE / 4];
static int my_packets_initialized = 0;

static void init_my_eye_packets(void) {
    if (my_packets_initialized) return;
    // Copy the FULL packet (vtable + display-list pointer + any other
    // inner fields) from each original. mpNextPacket (+0x04) gets reset
    // every time entryImm runs on the packet, so it doesn't matter
    // what's in there at copy time.
    volatile u32* src;
    int k;
    src = (volatile u32*)L_ON_CUP_OFF_AUP_PACKET1;
    for (k = 0; k < (int)(MY_PACKET_SIZE / 4); k++) my_packet_onCupOff1[k] = src[k];
    src = (volatile u32*)L_ON_CUP_OFF_AUP_PACKET2;
    for (k = 0; k < (int)(MY_PACKET_SIZE / 4); k++) my_packet_onCupOff2[k] = src[k];
    src = (volatile u32*)L_OFF_CUP_ON_AUP_PACKET1;
    for (k = 0; k < (int)(MY_PACKET_SIZE / 4); k++) my_packet_offCupOn1[k] = src[k];
    src = (volatile u32*)L_OFF_CUP_ON_AUP_PACKET2;
    for (k = 0; k < (int)(MY_PACKET_SIZE / 4); k++) my_packet_offCupOn2[k] = src[k];
    my_packets_initialized = 1;
}

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
// init-time eye-tex restore at multiplayer.c:533. Sequence: mat[1] is
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
// The actual face-state swap now happens via face_emit_swap_for_slot /
// face_emit_restore in daPy_draw_hook (below), where we have a clean FIFO
// context and can safely write to tev_block. See session_12 memory note.
static void face_hook_draw_shim(void* matpacket_v) {
    if (face_hook_orig_draw) {
        face_hook_orig_draw(matpacket_v);
    }
    // Diagnostic only — proves the shim is wired and firing.
    u32 cur = mailbox->face_hook_swaps;
    if (cur != 0xFFFFFFFFU) mailbox->face_hook_swaps = cur + 1;
}

// --- FACE-STATE per-slot swap (session 12) ----------------------------
// The per-frame BP emit (TX_SETIMAGE3 etc.) reads the SHARED tev_block at
// some point we haven't pinpointed but is BEFORE matpacket.draw. By
// bracketing the entire mini-link submission window (run_eye_fix + mDoExt)
// with a tev_block swap, mini-link's render captures face_state values
// while Link's earlier render (already submitted via daPy_lk_c_draw)
// keeps btp's values.
//
// Empirical proof of mechanism: face-tev-hammer 0x0006 at 60Hz from Go
// blacked out BOTH Link AND mini-link's eyes on the hammered Dolphin
// only. So tev_block IS the channel. The trick is timing — narrow swaps
// (session 9 around matpacket.draw) miss the read window.
//
// tev_block is shared across ALL J3DModel instances using the same
// modelData. Both mat[1].material+0x2C and mat[4].material+0x2C point at
// per-material singletons that are themselves shared. We resolve mat[1]
// and mat[4] tev_blocks once per session via the shared modelData and
// cache them in the face_emit_mat1_tev / face_emit_mat4_tev statics
// declared near the top of the file (alongside the other forward decls).
static void face_emit_resolve_tevblocks(void* model_data_v) {
    if (face_emit_mat1_tev && face_emit_mat4_tev
        && face_emit_mat14_tev && face_emit_mat15_tev) return;
    if (!model_data_v) return;
    u8* model_data = (u8*)model_data_v;
    // J3DModelData + 0x58 = mMaterialTable. mMaterialTable + 0x08 =
    // J3DMaterial** array (offset matches face-tev-hammer's path in
    // main.go: dataPtr + 0x58 + 0x08).
    u8** matarr = *(u8***)(model_data + 0x58 + 0x08);
    if (!matarr) return;
    u8* m1  = matarr[1];
    u8* m4  = matarr[4];
    u8* m14 = matarr[14];
    u8* m15 = matarr[15];
    if (m1)  face_emit_mat1_tev  = *(u8**)(m1  + J3DMATERIAL_TEV_BLOCK_OFFSET);
    if (m4)  face_emit_mat4_tev  = *(u8**)(m4  + J3DMATERIAL_TEV_BLOCK_OFFSET);
    if (m14) face_emit_mat14_tev = *(u8**)(m14 + J3DMATERIAL_TEV_BLOCK_OFFSET);
    if (m15) face_emit_mat15_tev = *(u8**)(m15 + J3DMATERIAL_TEV_BLOCK_OFFSET);
}

// Swap mat[1] / mat[4] tev_block.texno to face_state[slot] values.
// No-op if face_seqs[slot] is 0 (Go hasn't pushed first sample) or if
// face_hook_enable is 0 (master arm flag — keeps the swap disabled by
// default so a fresh boot doesn't render every mini-link with whatever
// stale face_state happens to be in mailbox memory).
static void face_emit_swap_for_slot(int slot, void* model_data) {
    if (!mailbox->face_hook_enable) return;
    if (slot < 0 || slot >= MAX_REMOTE_LINKS) return;
    if (mailbox->face_seqs[slot] == 0) return;
    if (face_emit_swap_active) return;  // defensive — should never nest
    face_emit_resolve_tevblocks(model_data);
    if (!face_emit_mat1_tev  || !face_emit_mat4_tev
     || !face_emit_mat14_tev || !face_emit_mat15_tev) return;

    face_emit_saved_mat1  = *(volatile u32*)(face_emit_mat1_tev  + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_emit_saved_mat4  = *(volatile u32*)(face_emit_mat4_tev  + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_emit_saved_mat14 = *(volatile u32*)(face_emit_mat14_tev + J3DTEVBLOCK_TEXNO0_OFFSET);
    face_emit_saved_mat15 = *(volatile u32*)(face_emit_mat15_tev + J3DTEVBLOCK_TEXNO0_OFFSET);

    u32 mat1_val  = ((u32)mailbox->face_state[slot].mat1_tex0  << 16) | (u32)mailbox->face_state[slot].mat1_tex1;
    u32 mat4_val  = ((u32)mailbox->face_state[slot].mat4_tex0  << 16) | (u32)mailbox->face_state[slot].mat4_tex1;
    u32 mat14_val = ((u32)mailbox->face_state[slot].mat14_tex0 << 16) | (u32)mailbox->face_state[slot].mat14_tex1;
    u32 mat15_val = ((u32)mailbox->face_state[slot].mat15_tex0 << 16) | (u32)mailbox->face_state[slot].mat15_tex1;
    *(volatile u32*)(face_emit_mat1_tev  + J3DTEVBLOCK_TEXNO0_OFFSET) = mat1_val;
    *(volatile u32*)(face_emit_mat4_tev  + J3DTEVBLOCK_TEXNO0_OFFSET) = mat4_val;
    *(volatile u32*)(face_emit_mat14_tev + J3DTEVBLOCK_TEXNO0_OFFSET) = mat14_val;
    *(volatile u32*)(face_emit_mat15_tev + J3DTEVBLOCK_TEXNO0_OFFSET) = mat15_val;

    face_emit_swap_active = 1;
    u32 cur = mailbox->face_hook_swaps;
    if (cur != 0xFFFFFFFFU) mailbox->face_hook_swaps = cur + 1;
}

static void face_emit_restore(void) {
    if (!face_emit_swap_active) return;
    if (!face_emit_mat1_tev  || !face_emit_mat4_tev
     || !face_emit_mat14_tev || !face_emit_mat15_tev) return;
    *(volatile u32*)(face_emit_mat1_tev  + J3DTEVBLOCK_TEXNO0_OFFSET) = face_emit_saved_mat1;
    *(volatile u32*)(face_emit_mat4_tev  + J3DTEVBLOCK_TEXNO0_OFFSET) = face_emit_saved_mat4;
    *(volatile u32*)(face_emit_mat14_tev + J3DTEVBLOCK_TEXNO0_OFFSET) = face_emit_saved_mat14;
    *(volatile u32*)(face_emit_mat15_tev + J3DTEVBLOCK_TEXNO0_OFFSET) = face_emit_saved_mat15;
    face_emit_swap_active = 0;
}

// --- Tunic colors (docs/09-player-colors.md §5.3) ---------------------
// Same save / swap / restore window as the face bracket above: every
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
static TUNIC_SMALL void tunic_swap(void* link, int slot) {
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

static TUNIC_SMALL void tunic_restore(void) {
    int k;
    if (!tunic_hdr) return;
    for (k = 0; k < 8; k++) tunic_hdr[k] = tunic_saved[k];
    tunic_hdr = 0;
}

// Publish the LOCAL Link's natural btp face state into the mailbox
// so broadcast-pose can read it cross-process without racing the
// bracket's tev_block save/swap/restore window. Called once per
// frame at the very top of daPy_draw_hook, BEFORE any swap fires —
// so tev_block holds btp's true value at this point.
//
// Resolves tev_blocks on first call and caches them in the same
// statics the swap path uses (so this is essentially free in steady
// state — just two volatile reads and 8 mailbox bytes).
static void face_emit_publish_local(void* model_data) {
    if (!model_data) return;
    face_emit_resolve_tevblocks(model_data);
    if (!face_emit_mat1_tev  || !face_emit_mat4_tev
     || !face_emit_mat14_tev || !face_emit_mat15_tev) return;
    u32 m1  = *(volatile u32*)(face_emit_mat1_tev  + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m4  = *(volatile u32*)(face_emit_mat4_tev  + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m14 = *(volatile u32*)(face_emit_mat14_tev + J3DTEVBLOCK_TEXNO0_OFFSET);
    u32 m15 = *(volatile u32*)(face_emit_mat15_tev + J3DTEVBLOCK_TEXNO0_OFFSET);
    mailbox->face_state_local.mat1_tex0  = (u16)(m1  >> 16);
    mailbox->face_state_local.mat1_tex1  = (u16)(m1  & 0xFFFF);
    mailbox->face_state_local.mat4_tex0  = (u16)(m4  >> 16);
    mailbox->face_state_local.mat4_tex1  = (u16)(m4  & 0xFFFF);
    mailbox->face_state_local.mat14_tex0 = (u16)(m14 >> 16);
    mailbox->face_state_local.mat14_tex1 = (u16)(m14 & 0xFFFF);
    mailbox->face_state_local.mat15_tex0 = (u16)(m15 >> 16);
    mailbox->face_state_local.mat15_tex1 = (u16)(m15 & 0xFFFF);
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
// guard means re-installing a patched matpacket is a no-op.
static u8 face_hook_prev_enable = 0;
static void face_hook_process_arm(void) {
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
                        J3DModelData* model_data) {
    if (!model || !link1_actor || !model_data) return;
    init_my_eye_packets();

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
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)my_packet_onCupOff2, 0);
    for (i = 0; i < 4; i++) {
        if (zoff_blend[i]) *(volatile u32*)((u8*)zoff_blend[i] + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
        if (zon[i])        *(volatile u32*)((u8*)zon[i]        + J3DSHAPE_FLAGS_OFFSET) |=  J3DSHAPE_FLAG_HIDE;
        if (zoff_none[i])  *(volatile u32*)((u8*)zoff_none[i]  + J3DSHAPE_FLAGS_OFFSET) &= ~J3DSHAPE_FLAG_HIDE;
    }
    J3DJoint_entryIn(cl_eye);
    J3DJoint_entryIn(cl_mayu);

    // Pass 2: packet 2 with cup-off/aup-on, swap to zOffBlendShape.
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)my_packet_offCupOn2, 0);
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
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)my_packet_onCupOff1, 0);
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
    J3DDrawBuffer_entryImm((J3DDrawBuffer*)opa_p1, (J3DPacket*)my_packet_offCupOn1, 0);
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
    for (i = 0; i < 32 && mtl >= 0x80000000u && mtl < 0x81800000u; i++) {
        u32 shape = *(volatile u32*)(mtl + J3DMATERIAL_SHAPE_OFFSET);
        if (shape >= 0x80000000u && shape < 0x81800000u) {
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
    for (i = 0; i < 32 && mtl >= 0x80000000u && mtl < 0x81800000u; i++) {
        u32 shape = *(volatile u32*)(mtl + J3DMATERIAL_SHAPE_OFFSET);
        if (shown & (1u << i)) {
            *(volatile u32*)(shape + J3DSHAPE_FLAGS_OFFSET) |= J3DSHAPE_FLAG_HIDE;
        }
        mtl = *(volatile u32*)(mtl + J3DMATERIAL_NEXT_OFFSET);
    }
}

// mDoExt_modelEntryDL for a mini-Link model, with the full link_root
// head shown (see above).
static void mini_link_entry_dl(J3DModel* model, J3DModelData* model_data) {
    u32 shown = mini_link_shapes_force_show(model_data);
    mDoExt_modelEntryDL(model);
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
static void mDoExt_skip_recipe_joints(J3DModel* model, J3DModelData* model_data) {
    void* joint_arr_void = *(void**)((u8*)model_data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    if (!joint_arr_void) {
        mini_link_entry_dl(model, model_data);
        return;
    }
    J3DJoint** joint_arr = (J3DJoint**)joint_arr_void;
    J3DJoint* cl_eye    = joint_arr[LINK_CL_EYE_JOINT_INDEX];
    J3DJoint* cl_mayu   = joint_arr[LINK_CL_MAYU_JOINT_INDEX];

    u32 saved_eye = 0, saved_mayu = 0;
    if (cl_eye)  { saved_eye  = *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET); *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET) = 0; }
    if (cl_mayu) { saved_mayu = *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET); *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET) = 0; }

    mini_link_entry_dl(model, model_data);

    if (cl_eye)  *(volatile u32*)((u8*)cl_eye  + J3DJOINT_MESH_OFFSET) = saved_eye;
    if (cl_mayu) *(volatile u32*)((u8*)cl_mayu + J3DJOINT_MESH_OFFSET) = saved_mayu;
}

// --- Draw-phase hook -------------------------------------------------
// Hooked at 0x80108210 via Freighter (replaces the `bl daPy_lk_c::draw`
// inside daPy_Draw). daPy_Draw runs inside fpcDw_Execute during the
// actor-draw iteration — i.e. the legitimate draw phase, same as every
// other actor's draw callback.
//
// Flow:
//   1. Call Link's real draw implementation at 0x80107308 with the
//      original `this` arg (r3). Preserves ALL existing Link rendering.
//   2. Publish Link #1's pose for broadcast-pose.
//   3. In shadow_mode 5 (pose feed), calc and submit each mini-Link slot
//      Go has written a pose to.
// Return value propagates Link's original draw result — daPy_Draw just
// tail-returns it to its caller.
int daPy_draw_hook(void* this_) {
    // SESSION 14: snapshot LOCAL Link's natural btp eye state into
    // mailbox.face_state_local for race-free broadcast-pose capture.
    // Must run BEFORE any face_emit_swap_for_slot call below — at this
    // point tev_block still holds btp's frame value (no swap has fired
    // yet this draw). No-op if mini_link_data isn't resolved yet.
    face_emit_publish_local(mini_link_data);

    tunic_swap(this_, TUNIC_SLOT_LOCAL);
    int result = daPy_lk_c_draw(this_);
    tunic_restore();

    // Publish Link #1's mpNodeMtx so Go-side broadcast-pose can read it
    // without racing calc. daPy_lk_c_draw has just completed for this
    // frame, so the joint walker has finished writing mpNodeMtx and the
    // buffer is stable until the next frame's calc. Lazy-alloc the
    // 2016 B publish buffer on the first frame, then memcpy every frame.
    // Allocated once per session: nothing resets pose_publish_state, so
    // the GameHeap block is never dropped and re-allocated (F18).
    // Runs unconditionally (not gated on shadow_mode) so Go can always
    // read a stable pose whether or not multiplayer is engaged — the
    // copy is a ~5 µs linear memcpy, trivial vs the 16 ms frame budget.
    if (mailbox->pose_publish_state != 0xFD) {
        J3DModel* link1_model = *(J3DModel**)((u8*)this_ + DAPY_LK_C_MPCLMODEL_OFFSET);
        J3DModelData* link1_data = *(J3DModelData**)((u8*)this_ + DAPY_LK_C_MPCLMODELDATA_OFFSET);
        if (link1_model && link1_data) {
            u16 jn = *(volatile u16*)((u8*)link1_data + J3DMODELDATA_JOINT_NUM_OFFSET);
            if (jn > 0 && jn <= 128) {
                if (mailbox->pose_publish_state == 0) {
                    u32 nbytes = (u32)jn * 48;
                    u8* buf = (u8*)JKRHeap_alloc(nbytes, 0x20, mDoExt_getGameHeap());
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

    // Stage-load safety (draw side). If this Link isn't built yet or
    // reads a model data other than the one our models were made from,
    // skip our submission this frame. Never reset here: the models are
    // still valid (see "Lifetimes" above mini_link_reset_state), and
    // multiplayer_update alone decides whether a real rebuild is due.
    if (mini_link_models[0] == 0 || mini_link_state != 1) {
        return result;
    }
    J3DModelData* current_data = *(J3DModelData**)((u8*)this_ + DAPY_LK_C_MPCLMODELDATA_OFFSET);
    if (current_data != mini_link_data || current_data == 0) {
        return result;
    }

    // shadow_mode 5 is the pose feed; 0 (the boot value) is off. The
    // mailbox is zero-initialized at boot, so a fresh patched-ISO game
    // looks vanilla until puppet-sync writes 5. The dev modes 1-4
    // (mirror, freeze, no-op calc, echo ring) were deleted in Phase E0
    // and now draw nothing, like 0.
    if (mailbox->shadow_mode != 5) {
        return result;
    }

    // Lazy-init the no-op J3DMtxCalc for the second calc below.
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
    // Per slot: first calc with Link's real walker (populates mpNodeMtx),
    // then copy Go's pose over mpNodeMtx and run a second calc with the
    // no-op walker, whose envelope pass rebuilds mpDrawMtxBuf from our
    // pose. Overwriting mpDrawMtxBuf directly isn't possible: its layout
    // is one entry per envelope combination, not per joint.
    volatile u32* bc_slot = (volatile u32*)((u8*)mini_link_data + J3DMODELDATA_BASIC_MTXCALC_OFFSET);
    u32 bc_saved = *bc_slot;
    cXyz* link_pos = ACTOR_POS(this_);
    int slot;
    for (slot = 0; slot < MAX_REMOTE_LINKS; slot++) {
        J3DModel* model = mini_link_models[slot];
        if (!model) continue;

        // Base matrix: a per-slot X offset from Link just so the seed
        // pose sits somewhere distinct — Go's pose overwrites it.
        Mtx* mtx = (Mtx*)((u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET);
        (*mtx)[0][0] = 1.0f; (*mtx)[0][1] = 0.0f; (*mtx)[0][2] = 0.0f;
        (*mtx)[0][3] = link_pos->x + (f32)(100 + slot * 100);
        (*mtx)[1][0] = 0.0f; (*mtx)[1][1] = 1.0f; (*mtx)[1][2] = 0.0f;
        (*mtx)[1][3] = link_pos->y;
        (*mtx)[2][0] = 0.0f; (*mtx)[2][1] = 0.0f; (*mtx)[2][2] = 1.0f;
        (*mtx)[2][3] = link_pos->z;
        // Link's joint callbacks (bound to the shared J3DModelData via
        // J3DJoint subclasses) recover the owning daPy_lk_c via mUserArea
        // (offset 0x14 in J3DModel). Without it, calc derefs NULL inside
        // checkEquipAnime at PC 0x8010C53C.
        *(volatile u32*)((u8*)model + J3DMODEL_USER_AREA_OFFSET) = (u32)this_;
        J3DModel_calc(model);

        volatile u32* node_mtx = (volatile u32*)(*(u32*)((u8*)model + J3DMODEL_MP_NODE_MTX_OFFSET));

        // Lazy-alloc this slot's pose buffer. Critical: seed it from the
        // live mpNodeMtx the first calc just populated, so a first-frame
        // overwrite is a real pose instead of uninitialized JKRHeap
        // bytes. Garbage = degenerate matrices = invisible Link + TEV
        // bucket pollution (sky goes gray).
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
            if (node_mtx) {
                volatile u32* seed_dst = (volatile u32*)pose_bufs[slot];
                int si;
                for (si = 0; si < pose_buf_u32s; si++) {
                    seed_dst[si] = node_mtx[si];
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
    // see docs/06-history.md "Eye-fix attempt 4 session 7"). The face
    // swap brackets the slot's render so its bake captures face_state
    // instead of btp's values (no-op unless face_hook_enable=1 AND
    // face_seqs[slot]!=0); the tunic swap recolors only this slot.
    for (slot = 0; slot < MAX_REMOTE_LINKS; slot++) {
        if (!mini_link_models[slot] || !pose_bufs[slot] || mailbox->pose_seqs[slot] == 0) continue;
        face_emit_swap_for_slot(slot, mini_link_data);
        tunic_swap(this_, slot);
        run_eye_fix(mini_link_models[slot], this_, mini_link_data);
        mDoExt_skip_recipe_joints(mini_link_models[slot], mini_link_data);
        tunic_restore();
        face_emit_restore();
    }

    return result;
}

// --- Input injection hook (synthetic PADStatus override) -------------
// Replaces the bl PADRead at 0x802C39A0 inside JUTGamePad::read. The
// shim calls real PADRead first so all four ports get populated as
// usual, then conditionally overrides port-0 fields from the mailbox.
// JUTGamePad::read then continues with its mPadButton/mPadMStick
// .update() calls and derives downstream state from our synthetic
// values — which is what game logic eventually reads. No race against
// Dolphin's SI poll because we're inside the PPC call chain, not
// hammering memory from outside the emulator.

typedef struct {
    u16 button;        // 0x00
    s8  stick_x;       // 0x02
    s8  stick_y;       // 0x03
    s8  substick_x;    // 0x04
    s8  substick_y;    // 0x05
    u8  trigger_l;     // 0x06
    u8  trigger_r;     // 0x07
    u8  analog_a;      // 0x08
    u8  analog_b;      // 0x09
    s8  err;           // 0x0A
    u8  _pad;          // 0x0B (alignment)
} PADStatus;

typedef u32 (*pad_read_fn)(PADStatus *buf);
#define PADRead_orig ((pad_read_fn)0x80315A20)

u32 pad_read_shim(PADStatus *buf) {
    u32 result = PADRead_orig(buf);
    if (mailbox->input_enable) {
        buf[0].button     = mailbox->input_buttons;
        buf[0].stick_x    = mailbox->input_stick_x;
        buf[0].stick_y    = mailbox->input_stick_y;
        buf[0].substick_x = mailbox->input_substick_x;
        buf[0].substick_y = mailbox->input_substick_y;
        buf[0].trigger_l  = mailbox->input_trigger_l;
        buf[0].trigger_r  = mailbox->input_trigger_r;
        buf[0].analog_a   = mailbox->input_analog_a;
        buf[0].analog_b   = mailbox->input_analog_b;
        // Force err=0 (PAD_ERR_NONE) so the game treats port 0 as a
        // valid responding controller, not a disconnected one.
        buf[0].err        = 0;
        u32 cur = mailbox->input_overrides;
        if (cur != 0xFFFFFFFFU) mailbox->input_overrides = cur + 1;
    }
    return result;
}
