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
//   v5 (current, mod ~0x11C8 B): 0x80411F08 @ __OSArenaLo = 0x80412000.
//       Echo-ring code pushed .text past 0x80411000, so the whole
//       mailbox + __OSArenaLo pair shifted up by 0x1000. See
//       inject/build.py for the matching OSInit patch.
// MUST match the `lwz 12, 0x1F08(12)` offset in frame_shim asm below.
#define CALLBACK_PTR_ADDR 0x80411F08

// Per-slot state. Parallel arrays to mailbox.puppets[]. pids[i] is the
// queued-spawn result from fopAcM_create; spawned[i] gates phase 2 sync.
static fpc_ProcID puppet_pids[MAX_PUPPETS];
static int puppet_spawned[MAX_PUPPETS];
static int frame_count = 0;

// --- Mini-Link state (Option B from roadmap 06) ------------------------
// Render N independent Link models from OUR code, all sharing Link #1's
// already-loaded J3DModelData. No actors, no PROC_PLAYER spawn, no
// 704 KB heap carve-out per model. Slot 0 is used by mirror / freeze /
// echo / single-pose modes (0..4); modes that source pose-per-slot
// (mode 5) iterate all MAX_REMOTE_LINKS slots.
static J3DModelData* mini_link_data = 0;
static J3DModel* mini_link_models[MAX_REMOTE_LINKS] = {0};
// 0 = not tried yet, 1 = all models created + rendering, 0xFF = give up
static u8 mini_link_state = 0;

// Shadow daPy_lk_c buffer for the "own the state" experiment (docs/06).
// sizeof(daPy_lk_c) per zeldaret/tww decomp (d_a_player_main.h) = 0x4C28.
// 19 KB is too big for static BSS — Freighter's linker placed shadow_link
// at 0x804101F8, which collides with the mailbox at 0x80410F00. Fixed by
// runtime-allocating from GameHeap on first use instead.
#define SHADOW_LINK_SIZE 0x4C28
static u32* shadow_link = 0;   // populated on first mode-1/2 frame
static u8 prev_shadow_mode = 0;
static int shadow_latched_local = 0;

// No-op J3DMtxCalc for mode 3 (and future custom-pose work).
// Mode 3 with NULL confirmed `calcAnmMtx` derefs basicMtxCalc (PC 0x802ee63c),
// but the crash stalls the draw phase. Replacing NULL with a valid pointer
// whose virtuals all return immediately lets calc() complete while skipping
// the joint walk — Link #2's mpNodeMtx stays from last real calc → freeze.
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

// --- Echo-Link state (shadow_mode 4) ----------------------------------
// Ring buffer of per-frame mpNodeMtx snapshots for the delayed-replay
// experiment (docs/06 "Next Session Priority" track 1).
//
// Flow each mode-4 frame inside daPy_draw_hook, AFTER our calc (which
// ran with the real basicMtxCalc and so populated mini-Link's mpNodeMtx
// with Link #1's current-frame pose as a byproduct), and BEFORE
// modelEntryDL (so viewCalc inside it projects our overwrite into
// mpDrawMtxBuf):
//   1. memcpy mpNodeMtx -> ring[write_idx]      (capture)
//   2. if frames_filled >= delay:
//        memcpy ring[(write_idx - delay + BUF) % BUF] -> mpNodeMtx  (replay)
//   3. write_idx = (write_idx + 1) % BUF; frames_filled = min(+1, BUF)
//
// delay == 0 is the sanity case: replay copies our just-captured frame
// back over itself. Link #2 should look identical to mirror. If he
// doesn't, the copy itself is damaging mpNodeMtx.
// delay > 0 replays a historical frame — the actual desync demo.
//
// Heap: GameHeap (same runtime allocator as shadow_link; ~245 KB free per
// docs/05). 60 frames * ~42 joints * 48 B = ~120 KB, so ECHO_BUF_FRAMES=60
// leaves enough headroom for normal Outset actor loads.
#define ECHO_BUF_FRAMES 60
static u8* echo_ring = 0;
static int echo_joint_num = 0;   // captured at first alloc; ring frame size = joint_num * 48
static int echo_frame_u32s = 0;  // joint_num * 12 (Mtx = 3*4 f32s = 12 u32s)
static int echo_write_idx = 0;
static int echo_frames_filled = 0;

// --- Pose feed state (shadow_mode 5) ----------------------------------
// Single GameHeap-resident buffer of joint matrices. Go writes directly to
// pose_buf via the published mailbox.pose_buf_ptr; C copies into mpNodeMtx
// each draw, then runs the same no-op-walker second calc as mode 4 to
// rebuild mpDrawMtxBuf from our pose. Hold-last semantics: C always reads
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

// Save-reload defensive reset. Called when we detect that Link's
// mpCLModelData pointer has changed (or our cached one was nulled by an
// earlier check this frame), meaning the game has torn down and rebuilt
// Link's J3DModel — typically because the user reloaded a save while
// mplay2 was running. Our cached J3DModel pointers now reference freed
// ArchiveHeap memory and the next J3DModel_calc will crash.
//
// Strategy: NULL all per-instance state and let the existing
// mini_link_state == 0 init path in multiplayer_update re-create
// against the new mpCLModelData. We do NOT call JKRHeap_free — on
// stage unload ArchiveHeap is reset wholesale, so the pointer is to
// freed-and-possibly-reallocated memory and freeing it would scramble
// the heap freelists. The "leak" framing is a misnomer; the heap was
// reset out from under us.
//
// Same logic for pose_bufs / echo_ring / shadow_link in GameHeap —
// unsure whether GameHeap survives a save reload, so null and skip the
// free either way (~few KB if it doesn't get reused).
static void mini_link_reset_state(void) {
    mini_link_data = 0;
    mini_link_state = 0;
    int k;
    for (k = 0; k < MAX_REMOTE_LINKS; k++) {
        mini_link_models[k] = 0;
        pose_bufs[k] = 0;
    }
    pose_joint_num = 0;
    pose_buf_u32s = 0;
    echo_ring = 0;
    echo_joint_num = 0;
    echo_frame_u32s = 0;
    echo_write_idx = 0;
    echo_frames_filled = 0;
    shadow_link = 0;
    shadow_latched_local = 0;

    // Mirror the reset into the mailbox so Go's view of the slots
    // matches the real C state. Clearing pose_seqs also fixes the
    // cosmetic "frozen decoy lingers" carryover noted in docs/06.
    int s;
    for (s = 0; s < MAILBOX_POSE_SLOT_CAP; s++) {
        mailbox->pose_buf_ptrs[s] = 0;
        mailbox->pose_joint_counts[s] = 0;
        mailbox->pose_buf_states[s] = 0;
        mailbox->pose_seqs[s] = 0;
    }
    mailbox->echo_ring_state = 0;
    mailbox->shadow_latched = 0;
    mailbox->dbg_model_data = 0;
    mailbox->dbg_saved_basic = 0;
    mailbox->dbg_joint_num = 0;
    mailbox->dbg_node_mtx_ptr = 0;
    mailbox->dbg_pose_first_word = 0;
    mailbox->dbg_node_mtx_first = 0;
    // Publish buffer: drop the pointer so next daPy_draw_hook re-allocs
    // against the rebuilt heaps. Don't free — see comment at the top of
    // this function re: ArchiveHeap/GameHeap reset behavior.
    mailbox->pose_publish_ptr = 0;
    mailbox->pose_publish_joint_count = 0;
    mailbox->pose_publish_state = 0;
    mailbox->pose_publish_seq = 0;
}

// Hooked to main01 (0x80006338) via hook_branchlink. Runs once.
// Publishes multiplayer_update's address for frame_shim to find.
void main01_init(void) {
    *(volatile u32*)CALLBACK_PTR_ADDR = (u32)&multiplayer_update;
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
        "lwz   12, 0x1F08(12)   \n"  // = CALLBACK_PTR_ADDR (mailbox+0x08) = 0x80411F08
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
        // mAcch.m_flags |= 0x1: makes dBgS_Acch::CrrPos early-return
        // for this frame. CrrPos runs AFTER posMove in execute() and
        // performs the heavy collision correction (MoveBgCrrPos +
        // WallCorrect + LineCheck) that snaps Link back to where the
        // collision system thinks he should be. Setting bit 0 makes
        // CrrPos skip entirely so our writes survive. We OR-set rather
        // than overwrite so other flag bits are preserved; gameplay's
        // own code can clear bit 0 when it next runs the controller.
        // mAcch is at +0x46C, m_flags at +0x28 of dBgS_Acch ⇒ +0x494.
        u32* mAcch_flags = (u32*)(link_bytes + 0x494);
        *mAcch_flags |= 0x1;
        // l_debug_keep_pos at 0x803E440C is the file-global cXyz that
        // execute() restores current.pos from at the START of every
        // frame (line 11322 in zeldaret/tww d_a_player_main.cpp,
        // gated `#if VERSION > VERSION_DEMO` which IS active in the
        // shipped retail build). Without writing here, our warp gets
        // immediately reverted next frame as Link's execute begins.
        // Address verified empirically via scan-pos + poke-vec3.
        *(volatile cXyz*)0x803E440C = target;
        // Diagnostic: publish (u32)link so Go can compare against
        // GetLinkPtr(), and read-back of current.pos.x so we can verify
        // our write actually landed in memory before something resets
        // it next frame.
        mailbox->warp_dbg_link_addr = (u32)link;
        mailbox->warp_dbg_post_x = ((cXyz*)(link_bytes + 0x1F8))->x;
        mailbox->warp_ack = mailbox->warp_seq;
    }

    frame_count++;
    if (frame_count < 300) {
        mailbox->progress = 1;
        return;
    }

    cXyz* link_pos = ACTOR_POS(link);
    csXyz* link_angle = ACTOR_ANGLE(link);
    s8 link_room = ACTOR_ROOM(link);

    u32 best_progress = 3;

    int i;
    for (i = 0; i < MAX_PUPPETS; i++) {
        volatile Puppet* slot = &mailbox->puppets[i];

        // Go cleared the slot — drop our book-keeping. Actor cleanup is
        // best-effort; in practice the actor sticks around until the game
        // unloads the stage. For now we just stop syncing it.
        if (!slot->active) {
            if (puppet_spawned[i]) {
                puppet_spawned[i] = 0;
                puppet_pids[i] = fpcM_ERROR_PROCESS_ID_e;
                slot->actor_ptr = 0;
            }
            continue;
        }

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
            // Actor was deleted externally. Drop our state; Go can re-
            // activate the slot to request a fresh spawn.
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
    // Save-reload safety: if the game has rebuilt Link's J3DModelData
    // (stage unload + reload swaps it out), our cached mini_link_data
    // points to freed ArchiveHeap memory and the next calc would crash.
    // Drop all per-instance state so the init block below re-creates
    // against the fresh mpCLModelData. Also covers the brief window
    // where mpCLModelData transiently goes NULL during reload.
    if (mini_link_state != 0) {
        J3DModelData* current_data = *(J3DModelData**)((u8*)link + DAPY_LK_C_MPCLMODELDATA_OFFSET);
        if (current_data != mini_link_data || current_data == 0) {
            mini_link_reset_state();
        }
    }

    // Progress encoding (reserved 20-29, beats puppet states 1-10):
    //   20 = getRes returned NULL (archive not resident yet — retry next frame)
    //   21 = mDoExt_J3DModel__create returned NULL (gave up)
    //   22 = J3DModel created; rendering each frame
    //   23 = modelEntryDL called this frame (rendering confirmed)
    if (mini_link_state == 0) {
        // Link's archive is loaded by daPy_createHeap as part of Link #1's
        // phase_2. We've already waited 300 frames + confirmed link != NULL
        // above, so the archive should be resident.
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
                // flag=0 (createMatPacket:J3DModel.cpp:296-309)
                // newDisplayList(size) allocates per-instance memory
                // and poses no longer collide. Second arg (0x11000022)
                // is a newDifferedDisplayList flag only consumed inside
                // the 0x80000 branch, so it becomes dead here — left
                // as-is to keep the diff tight.
                mini_link_models[li] = mDoExt_J3DModel__create(mini_link_data, 0, 0x11000022);
                if (mini_link_models[li]) created++;
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
                // Slot 0 is required for modes 0..4; without it nothing works.
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

    mailbox->progress = best_progress;
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
//   2. After Link finishes drawing, submit our mini-Link to j3dSys.
// Return value propagates Link's original draw result — daPy_Draw just
// tail-returns it to its caller.
//
// Status: hook is wired and firing each frame (progress=23 visible if
// multiplayer_update didn't overwrite it next tick), but submitting
// mini-Link from here STILL breaks sky textures — same symptom as the
// original execute-phase attempt. modelEntryDL is the issue, not the
// phase. See docs/05 "Mini-Link render pipeline" for open avenues.
int daPy_draw_hook(void* this_) {
    // draw_progress stages (diagnostic — only this hook writes this field,
    // so execute phase can't overwrite it):
    //   30 = hook entered
    //   31 = Link's real draw returned
    //   32 = mini_link_models[0] non-NULL
    //   33 = mini_link_state == 1 (full gate open)
    //   34 = matrix written (about to snapshot j3dSys)
    //   35 = j3dSys snapshot done (about to call calc)
    //   36 = calc returned (about to restore j3dSys)
    //   37 = j3dSys restored (about to modelEntryDL)
    //   38 = modelEntryDL returned — full path completed
    // Diagnostic table: stuck at 36 ⇒ calc itself crashed inside on
    // Link's data → step 3 (separate ModelData). Reached 38 then crash
    // later ⇒ pollution lives outside j3dSys (statics or shared MtxCalc).
    mailbox->draw_progress = 30;
    int result = daPy_lk_c_draw(this_);
    mailbox->draw_progress = 31;

    // Publish Link #1's mpNodeMtx so Go-side broadcast-pose can read it
    // without racing calc. daPy_lk_c_draw has just completed for this
    // frame, so the joint walker has finished writing mpNodeMtx and the
    // buffer is stable until the next frame's calc. Lazy-alloc the
    // 2016 B publish buffer on the first frame, then memcpy every frame.
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

    // Save-reload safety (draw side). Same compare as multiplayer_update
    // but enforced HERE because the crash site is J3DModel_calc below —
    // if a stage teardown landed between execute and draw of this frame,
    // mini_link_models[0] still references freed memory. Skip our
    // submission this frame; multiplayer_update next frame rebuilds.
    if (mini_link_models[0] != 0) {
        J3DModelData* current_data = *(J3DModelData**)((u8*)this_ + DAPY_LK_C_MPCLMODELDATA_OFFSET);
        if (current_data != mini_link_data || current_data == 0) {
            mini_link_reset_state();
            return result;
        }
    }

    if (mini_link_models[0] != 0) {
        mailbox->draw_progress = 32;
        if (mini_link_state == 1) {
            mailbox->draw_progress = 33;
            cXyz* link_pos = ACTOR_POS(this_);

            Mtx* mtx = (Mtx*)((u8*)mini_link_models[0] + J3DMODEL_BASE_TR_MTX_OFFSET);
            (*mtx)[0][0] = 1.0f; (*mtx)[0][1] = 0.0f; (*mtx)[0][2] = 0.0f;
            (*mtx)[0][3] = link_pos->x + 100.0f;
            (*mtx)[1][0] = 0.0f; (*mtx)[1][1] = 1.0f; (*mtx)[1][2] = 0.0f;
            (*mtx)[1][3] = link_pos->y;
            (*mtx)[2][0] = 0.0f; (*mtx)[2][1] = 0.0f; (*mtx)[2][2] = 1.0f;
            (*mtx)[2][3] = link_pos->z;

            // Link's joint callbacks (bound to the shared J3DModelData
            // via J3DJoint subclasses) recover the owning daPy_lk_c via
            // mUserArea (offset 0x14 in J3DModel). Without it, calc
            // derefs NULL inside checkEquipAnime at PC 0x8010C53C.
            //
            // Shadow-instance experiment (docs/06 "Next Session Priority"
            // step 1). mailbox->shadow_mode chooses the source:
            //   0 = userArea = this_ (Link #1, proven mirror recipe)
            //   1 = refresh: copy this_ → shadow_link each frame, userArea = shadow
            //   2 = freeze:  copy once on mode entry, userArea = shadow
            //                Observation (2026-04-19): Link #2 still mirrored
            //                Link #1 → pose does NOT flow through mUserArea.
            //                Per J3DModel::calcAnmMtx, the walker uses
            //                getModelData()->getBasicMtxCalc(), which is
            //                shared across both models via J3DModelData.
            //   3 = baseline userArea AND swap basicMtxCalc→NULL around calc
            //                to probe whether the shared controller is the
            //                actual pose source. Crash at recursiveCalc ⇒
            //                controller is the culprit (design replacement).
            //                Link #2 freezes at last pose ⇒ decoupling proven.
            u8 mode = mailbox->shadow_mode;
            if (mode != prev_shadow_mode) {
                shadow_latched_local = 0;
                mailbox->shadow_latched = 0;
            }
            prev_shadow_mode = mode;

            // Mode 0 = OFF: skip the entire Link #2 pipeline (no calc,
            // no entryDL). The mailbox is zero-initialized at boot, so
            // a fresh patched-ISO game looks vanilla until something
            // explicitly opts in (mplay2's puppet-sync writes mode 5;
            // dev/debug modes 1-4 are picked manually via
            // `./ww-multiplayer.exe shadow-mode <N>`). Saves a per-frame J3DModel
            // calc + modelEntryDL submission too.
            if (mode == 0) {
                return result;
            }

            u32 user_area = (u32)this_;
            if (mode == 1 || mode == 2) {
                // Lazy-allocate the shadow buffer from GameHeap. Runs once
                // the first time Go writes a non-zero shadow_mode. 0x4C28
                // out of ~245 KB GameHeap free (per docs/06) is safe.
                // shadow_latched in mailbox becomes 0xFF to signal alloc
                // failure so Go can distinguish from "latched snapshot".
                if (!shadow_link) {
                    shadow_link = (u32*)JKRHeap_alloc(
                        SHADOW_LINK_SIZE, 0x20, mDoExt_getGameHeap()
                    );
                }
                if (!shadow_link) {
                    mailbox->shadow_latched = 0xFF;
                    // fall back to baseline (user_area = this_)
                } else {
                    if (mode == 1 || !shadow_latched_local) {
                        volatile u32* src = (volatile u32*)this_;
                        int k;
                        for (k = 0; k < (int)(SHADOW_LINK_SIZE / 4); k++) {
                            shadow_link[k] = src[k];
                        }
                        if (mode == 2) {
                            shadow_latched_local = 1;
                            mailbox->shadow_latched = 1;
                        }
                    }
                    user_area = (u32)shadow_link;
                }
            }
            *(volatile u32*)((u8*)mini_link_models[0] + J3DMODEL_USER_AREA_OFFSET) = user_area;

            // Diagnostic: publish what we *think* J3DModelData and its
            // basicMtxCalc pointer are, every frame, regardless of mode.
            // If mode-3's NULL write had no effect, these tell us whether
            // our pointer/offset arithmetic is even landing on the right
            // field.
            volatile u32* basic_calc_slot = 0;
            u32 saved_basic_calc = 0;
            if (mini_link_data) {
                basic_calc_slot = (volatile u32*)((u8*)mini_link_data + J3DMODELDATA_BASIC_MTXCALC_OFFSET);
                saved_basic_calc = *basic_calc_slot;
                mailbox->dbg_model_data = (u32)mini_link_data;
                mailbox->dbg_saved_basic = saved_basic_calc;
                // Joint count for echo-ring sizing. Expected ~42 per decomp.
                mailbox->dbg_joint_num = *(volatile u16*)((u8*)mini_link_data + J3DMODELDATA_JOINT_NUM_OFFSET);
            }
            // mpNodeMtx pointer — calc() allocates + writes this. Published
            // every frame (read AFTER calc below) so Go can confirm non-NULL.
            mailbox->dbg_node_mtx_ptr = *(volatile u32*)((u8*)mini_link_models[0] + J3DMODEL_MP_NODE_MTX_OFFSET);

            // Lazy-init the no-op J3DMtxCalc used by mode 3 and mode 4.
            // Vtable of 16 blr stubs; object = {vtable_ptr, padding}.
            // Cheap to init once; modes that need it swap it into
            // J3DModelData->basicMtxCalc around J3DModel_calc.
            if (!noop_mtxcalc_initialized) {
                int vi;
                for (vi = 0; vi < 16; vi++) {
                    noop_mtxcalc_vtable[vi] = (u32)&noop_stub;
                }
                noop_mtxcalc_obj[0] = (u32)&noop_mtxcalc_vtable[0];
                noop_mtxcalc_initialized = 1;
            }

            // Mode 3 (verified 2026-04-19): PC 0x802ee63c inside calcAnmMtx
            // hits a NULL deref when basicMtxCalc is 0 — decoupling proven.
            // Swap basicMtxCalc to a no-op J3DMtxCalc so calc() completes
            // without walking the joint tree. Link #2's mpNodeMtx is left
            // untouched → he FREEZES at his last-calc'd pose while Link #1
            // keeps animating.
            if (mode == 3 && basic_calc_slot) {
                *basic_calc_slot = (u32)&noop_mtxcalc_obj[0];
            } else {
                basic_calc_slot = 0;   // don't restore if we didn't swap
            }

            // Full 0x128-byte j3dSys snapshot around calc(). Wraps BOTH
            // calls in mode 4's double-calc path.
            static u32 j3dsys_snapshot[J3D_SYS_SIZE / 4];
            volatile u32* j3dsys = (volatile u32*)J3D_SYS_ADDR;
            int n;
            mailbox->draw_progress = 34;
            for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
                j3dsys_snapshot[n] = j3dsys[n];
            }
            mailbox->draw_progress = 35;
            J3DModel_calc(mini_link_models[0]);
            // Restore basicMtxCalc immediately — Link #1's next-frame code
            // reads this pointer through the shared J3DModelData.
            if (basic_calc_slot) {
                *basic_calc_slot = saved_basic_calc;
            }
            mailbox->draw_progress = 36;

            // Echo-Link capture + delayed replay (shadow_mode 4). Sits
            // INSIDE the j3dSys bracket because the second calc below
            // re-pollutes j3dSys and must be rolled back too.
            //
            // First calc (above) ran with Link's real basicMtxCalc →
            // mpNodeMtx and mpDrawMtxBuf both reflect Link #1's current
            // pose. We snapshot mpNodeMtx into the ring, then overwrite
            // it from a delayed slot.
            //
            // Problem: the first-calc's mpDrawMtxBuf is still current-
            // pose (that's where skin envelopes read from). Running the
            // second calc with a no-op basicMtxCalc leaves mpNodeMtx
            // alone (the walker is stubbed) but calc's envelope/draw-
            // matrix pass rebuilds mpDrawMtxBuf from our delayed
            // mpNodeMtx → both buffers now carry the delayed pose.
            //
            // Why not just overwrite mpDrawMtxBuf directly? Its layout
            // is not 1:1 with joints — it has one entry per unique
            // envelope combination (count lives in J3DModelData, offset
            // not yet mapped). The double-calc sidesteps needing to
            // reverse-engineer that.
            if (mode == 4 && mini_link_data) {
                u16 jn = mailbox->dbg_joint_num;
                // Lazy-allocate ring on first mode-4 frame. Joint count
                // is captured here so resizes via joint-tree swap don't
                // blow up the ring mid-flight.
                if (!echo_ring) {
                    if (jn == 0 || jn > 128) {
                        mailbox->echo_ring_state = 0xFE;  // bad jointNum
                    } else {
                        echo_joint_num = (int)jn;
                        echo_frame_u32s = echo_joint_num * 12; // 12 u32 per Mtx
                        u32 total_bytes = (u32)(ECHO_BUF_FRAMES * echo_frame_u32s * 4);
                        echo_ring = (u8*)JKRHeap_alloc(total_bytes, 0x20, mDoExt_getGameHeap());
                        if (!echo_ring) {
                            mailbox->echo_ring_state = 0xFD;  // alloc failed
                        } else {
                            mailbox->echo_ring_state = 1;
                            echo_write_idx = 0;
                            echo_frames_filled = 0;
                        }
                    }
                }

                volatile u32* node_mtx = (volatile u32*)(*(u32*)((u8*)mini_link_models[0] + J3DMODEL_MP_NODE_MTX_OFFSET));
                if (echo_ring && node_mtx && echo_frame_u32s > 0 && basic_calc_slot == 0) {
                    // basic_calc_slot==0 here means we were not in mode 3;
                    // mode 4 uses it below to swap basicMtxCalc for the
                    // second calc. Re-derive a local slot pointer.
                    volatile u32* bc_slot = (volatile u32*)((u8*)mini_link_data + J3DMODELDATA_BASIC_MTXCALC_OFFSET);
                    u32 bc_saved = *bc_slot;

                    u8 delay = mailbox->echo_delay;
                    if (delay >= ECHO_BUF_FRAMES) delay = ECHO_BUF_FRAMES - 1;

                    // Capture: mpNodeMtx -> ring[write_idx] (current pose
                    // from the first calc that just ran with real walker).
                    volatile u32* cap_dst = (volatile u32*)(echo_ring + echo_write_idx * echo_frame_u32s * 4);
                    int ei;
                    for (ei = 0; ei < echo_frame_u32s; ei++) {
                        cap_dst[ei] = node_mtx[ei];
                    }
                    mailbox->draw_progress = 39;

                    // Replay: ring[(write_idx - delay + BUF) % BUF] -> mpNodeMtx.
                    // Gate on having at least `delay` historical frames so the
                    // first few frames after a mode change don't pull random
                    // pre-alloc bytes (delay == 0 always satisfies since the
                    // just-captured frame IS that slot — identity overwrite).
                    if (echo_frames_filled >= (int)delay) {
                        int replay_idx = echo_write_idx - (int)delay;
                        if (replay_idx < 0) replay_idx += ECHO_BUF_FRAMES;
                        volatile u32* rep_src = (volatile u32*)(echo_ring + replay_idx * echo_frame_u32s * 4);
                        for (ei = 0; ei < echo_frame_u32s; ei++) {
                            node_mtx[ei] = rep_src[ei];
                        }
                    }
                    mailbox->draw_progress = 40;

                    echo_write_idx++;
                    if (echo_write_idx >= ECHO_BUF_FRAMES) echo_write_idx = 0;
                    if (echo_frames_filled < ECHO_BUF_FRAMES) echo_frames_filled++;

                    // Second calc: no-op basicMtxCalc skips the skeleton
                    // walk (mpNodeMtx keeps our delayed overwrite), but
                    // calc's envelope/draw-matrix pass rebuilds
                    // mpDrawMtxBuf from the delayed mpNodeMtx. Without
                    // this, rigid joints follow the delayed pose but
                    // skin envelopes stay on current → rubber-banding.
                    *bc_slot = (u32)&noop_mtxcalc_obj[0];
                    J3DModel_calc(mini_link_models[0]);
                    *bc_slot = bc_saved;
                    mailbox->draw_progress = 41;
                }
            }

            // Mode 5: pose feed. Same double-calc shape as mode 4 but the
            // source for mpNodeMtx is mailbox.pose_buf_ptr (Go-populated
            // from local capture or network). Lazy-alloc the buffer the
            // first time mode 5 fires so Go doesn't have to set it up.
            //
            // Critical: seed pose_buf from the live mpNodeMtx (just
            // populated by the first calc with Link's real walker)
            // BEFORE we declare it "ready". Otherwise the very first
            // mode-5 frame copies uninitialized JKRHeap bytes into
            // mpNodeMtx and the second calc rebuilds mpDrawMtxBuf from
            // garbage → degenerate vertices → invisible Link #2 + TEV
            // bucket pollution → sky goes gray. Go's pose writes don't
            // arrive until ~50 ms after the alloc, by which point the
            // first-frame damage has already propagated.
            // Mode 5: pose feed for ALL N remote-Link slots. Each slot
            // owns its own J3DModel (mini_link_models[i]) and its own
            // GameHeap-resident pose buffer (pose_bufs[i]). The mailbox
            // arrays let Go write per-slot poses independently, so N
            // remote players each drive their own Link #2/#3/...
            //
            // Slot 0 is special only because it inherits the base matrix
            // and mUserArea writes done above (in the unconditional draw
            // path). Slots 1..N-1 get THEIR base matrix + userArea wired
            // here per-slot before their first/second calcs.
            if (mode == 5 && mini_link_data) {
                u16 jn = mailbox->dbg_joint_num;
                if (jn > 0 && jn <= 128 && pose_joint_num == 0) {
                    // Capture joint count once; same Link rig for every slot.
                    pose_joint_num = (int)jn;
                    pose_buf_u32s = pose_joint_num * 12;
                }

                volatile u32* bc_slot = (volatile u32*)((u8*)mini_link_data + J3DMODELDATA_BASIC_MTXCALC_OFFSET);
                u32 bc_saved = *bc_slot;
                int slot;
                cXyz* link_pos_for_slot = ACTOR_POS(this_);

                for (slot = 0; slot < MAX_REMOTE_LINKS; slot++) {
                    J3DModel* model = mini_link_models[slot];
                    if (!model) continue;

                    // Lazy-alloc this slot's pose buffer the first time
                    // we hit mode 5 for it. Critical: seed from a live
                    // mpNodeMtx (slot 0's, which the unconditional first
                    // calc just populated above) so first-frame overwrite
                    // is identity instead of garbage. Garbage = degenerate
                    // matrices = invisible Link + TEV bucket pollution.
                    if (!pose_bufs[slot]) {
                        if (pose_buf_u32s == 0) {
                            mailbox->pose_buf_states[slot] = 0xFE;
                            continue;
                        }
                        u32 nbytes = (u32)(pose_buf_u32s * 4);
                        pose_bufs[slot] = (u8*)JKRHeap_alloc(nbytes, 0x20, mDoExt_getGameHeap());
                        if (!pose_bufs[slot]) {
                            mailbox->pose_buf_states[slot] = 0xFD;
                            continue;
                        }
                        volatile u32* seed_src = (volatile u32*)(*(u32*)((u8*)mini_link_models[0] + J3DMODEL_MP_NODE_MTX_OFFSET));
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

                    // Slots 1..N-1 also need base matrix + userArea
                    // before their first calc, since the unconditional
                    // path above only handled slot 0. Use a per-slot
                    // X offset just so the lazy-alloc seed sits at a
                    // different starting point — Go's pose write will
                    // overwrite this on the next tick anyway.
                    if (slot > 0) {
                        Mtx* mtx2 = (Mtx*)((u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET);
                        (*mtx2)[0][0] = 1.0f; (*mtx2)[0][1] = 0.0f; (*mtx2)[0][2] = 0.0f;
                        (*mtx2)[0][3] = link_pos_for_slot->x + (f32)(100 + slot * 100);
                        (*mtx2)[1][0] = 0.0f; (*mtx2)[1][1] = 1.0f; (*mtx2)[1][2] = 0.0f;
                        (*mtx2)[1][3] = link_pos_for_slot->y;
                        (*mtx2)[2][0] = 0.0f; (*mtx2)[2][1] = 0.0f; (*mtx2)[2][2] = 1.0f;
                        (*mtx2)[2][3] = link_pos_for_slot->z;
                        *(volatile u32*)((u8*)model + J3DMODEL_USER_AREA_OFFSET) = (u32)this_;
                        // First calc: populates mpNodeMtx with current
                        // pose at the slot's offset position. Will be
                        // overwritten by the pose copy below.
                        J3DModel_calc(model);
                    }

                    // Skip render if Go has never written a pose to
                    // this slot. Otherwise we'd display the seed pose
                    // (a stale snapshot of slot 0's mpNodeMtx) as a
                    // frozen ghost. Go bumps pose_seqs[slot] each write.
                    if (mailbox->pose_seqs[slot] == 0) {
                        continue;
                    }

                    // Copy Go's pose → this slot's mpNodeMtx, then run
                    // the second calc with the no-op walker so calc's
                    // envelope pass rebuilds mpDrawMtxBuf from our pose.
                    volatile u32* node_mtx = (volatile u32*)(*(u32*)((u8*)model + J3DMODEL_MP_NODE_MTX_OFFSET));
                    if (pose_bufs[slot] && node_mtx && pose_buf_u32s > 0 && basic_calc_slot == 0) {
                        volatile u32* src = (volatile u32*)pose_bufs[slot];
                        int pi;
                        for (pi = 0; pi < pose_buf_u32s; pi++) {
                            node_mtx[pi] = src[pi];
                        }
                        if (slot == 0) {
                            mailbox->dbg_pose_first_word = src[0];
                        }
                        *bc_slot = (u32)&noop_mtxcalc_obj[0];
                        J3DModel_calc(model);
                        *bc_slot = bc_saved;
                        if (slot == 0) {
                            mailbox->dbg_node_mtx_first = node_mtx[0];
                        }
                    }
                }
                mailbox->draw_progress = 41;
            }

            // Restore j3dSys after ALL calcs (both first and optional
            // second for mode 4). Must come AFTER the mode-4 second
            // calc or that calc's pollution leaks to Link's post-draw
            // code.
            for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
                j3dsys[n] = j3dsys_snapshot[n];
            }
            mailbox->draw_progress = 37;

            // [v0.1.4 eye-fix attempts reverted. TWO attempts both froze
            // Dolphin once multiplayer engaged (pose_seqs[0] becomes
            // non-zero). Neither j3dSys swap + entryIn (attempt 1) nor
            // viewCalc + j3dSys swap + entryIn (attempt 2) worked. Full
            // design + addresses documented in docs/06 item #9; next
            // session targets differential-style attempts that don't
            // touch j3dSys state (see roadmap).]

            // Mode 5 (multiplayer): slot 0 only submits once Go has
            // written at least one network pose, matching how slots 1+
            // are gated below. Without this, Link #2 would mirror
            // Link #1 (from the first calc with the real walker)
            // during the brief window between mplay2 startup and the
            // first remote pose arrival — annoying flash visible to
            // the user. Other modes (1-4 dev/debug) want unconditional
            // render; they bypass the gate.
            if (mode != 5 || mailbox->pose_seqs[0] != 0) {
                mDoExt_modelEntryDL(mini_link_models[0]);
            }
            // In mode 5, also submit any other slots whose pose buffer
            // has been allocated (i.e. a remote is driving them).
            if (mode == 5) {
                int es;
                for (es = 1; es < MAX_REMOTE_LINKS; es++) {
                    // Same gate as the calc loop: a slot only renders
                    // once Go has written at least one pose to it.
                    if (mini_link_models[es] && pose_bufs[es] && mailbox->pose_seqs[es] != 0) {
                        mDoExt_modelEntryDL(mini_link_models[es]);
                    }
                }
            }

            mailbox->draw_progress = 38;

            mailbox->progress = 23;
        }
    }

    return result;
}
