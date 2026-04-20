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
//   v1 (mod ~0x748 B): 0x80410700. Worked because the linker left 0x700
//       as padding. Broke when the mod grew past 0x700 — real instructions
//       landed there and the write corrupted them (crash: invalid read
//       0x24 @ PC 0x80410704).
//   v2 (mod ~0x868 B): 0x80410808 = old mailbox+0x08. Broke because the
//       old mailbox (0x80410800) overlapped the code section — main01_init
//       was STILL writing inside our code.
//   v3 (0x80410908 = mailbox+0x08): broke when mod grew past 0x900.
//   v4 (current):  0x80410F08 = mailbox+0x08 with mailbox now at
//       0x80410F00 — just below __OSArenaLo (0x80411000), maximum
//       headroom before we'd ever collide again.
// MUST match the `lwz 12, 0x0F08(12)` offset in frame_shim asm below.
#define CALLBACK_PTR_ADDR 0x80410F08

// Per-slot state. Parallel arrays to mailbox.puppets[]. pids[i] is the
// queued-spawn result from fopAcM_create; spawned[i] gates phase 2 sync.
static fpc_ProcID puppet_pids[MAX_PUPPETS];
static int puppet_spawned[MAX_PUPPETS];
static int frame_count = 0;

// --- Mini-Link state (Option B from roadmap 06) ------------------------
// Render a second Link from OUR code by creating a J3DModel that shares
// Link #1's already-loaded J3DModelData. No actor, no PROC_PLAYER spawn,
// no 704 KB heap carve-out. First prototype: T-pose Link drawn each frame
// at link_pos + (100, 0, 0). Animation comes later.
static J3DModelData* mini_link_data = 0;
static J3DModel* mini_link_model = 0;
// 0 = not tried yet, 1 = model created + rendering, 0xFF = give up
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
        "lwz   12, 0x0F08(12)   \n"  // = CALLBACK_PTR_ADDR (mailbox+0x08)
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
            JKRHeap* targetHeap = mDoExt_getArchiveHeap();
            JKRHeap* oldHeap = JKRHeap_becomeCurrentHeap(targetHeap);
            mini_link_model = mDoExt_J3DModel__create(mini_link_data, 0x80000, 0x11000022);
            JKRHeap_becomeCurrentHeap(oldHeap);

            if (!mini_link_model) {
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
    //   32 = mini_link_model non-NULL
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

    if (mini_link_model != 0) {
        mailbox->draw_progress = 32;
        if (mini_link_state == 1) {
            mailbox->draw_progress = 33;
            cXyz* link_pos = ACTOR_POS(this_);

            Mtx* mtx = (Mtx*)((u8*)mini_link_model + J3DMODEL_BASE_TR_MTX_OFFSET);
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
            *(volatile u32*)((u8*)mini_link_model + J3DMODEL_USER_AREA_OFFSET) = user_area;

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
            }

            // Mode 3 (verified 2026-04-19): PC 0x802ee63c inside calcAnmMtx
            // hits a NULL deref when basicMtxCalc is 0 — decoupling proven.
            // Now swap basicMtxCalc to a no-op J3DMtxCalc so calc() completes
            // without walking the joint tree. Link #2's mpNodeMtx is left
            // untouched → he should FREEZE at his last-calc'd pose while
            // Link #1 keeps animating.
            if (mode == 3 && basic_calc_slot) {
                if (!noop_mtxcalc_initialized) {
                    int vi;
                    for (vi = 0; vi < 16; vi++) {
                        noop_mtxcalc_vtable[vi] = (u32)&noop_stub;
                    }
                    noop_mtxcalc_obj[0] = (u32)&noop_mtxcalc_vtable[0];
                    noop_mtxcalc_initialized = 1;
                }
                *basic_calc_slot = (u32)&noop_mtxcalc_obj[0];
            } else {
                basic_calc_slot = 0;   // don't restore if we didn't swap
            }

            // Full 0x128-byte j3dSys snapshot around calc().
            static u32 j3dsys_snapshot[J3D_SYS_SIZE / 4];
            volatile u32* j3dsys = (volatile u32*)J3D_SYS_ADDR;
            int n;
            mailbox->draw_progress = 34;
            for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
                j3dsys_snapshot[n] = j3dsys[n];
            }
            mailbox->draw_progress = 35;
            J3DModel_calc(mini_link_model);
            // Restore basicMtxCalc immediately — Link #1's next-frame code
            // reads this pointer through the shared J3DModelData.
            if (basic_calc_slot) {
                *basic_calc_slot = saved_basic_calc;
            }
            mailbox->draw_progress = 36;
            for (n = 0; n < (int)(J3D_SYS_SIZE / 4); n++) {
                j3dsys[n] = j3dsys_snapshot[n];
            }
            mailbox->draw_progress = 37;

            mDoExt_modelEntryDL(mini_link_model);
            mailbox->draw_progress = 38;

            mailbox->progress = 23;
        }
    }

    return result;
}
