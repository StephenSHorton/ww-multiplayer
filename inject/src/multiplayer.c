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

// Where main01_init stashes the callback pointer. Orphan memory between T2
// code (~0x80410000) and __OSArenaLo (0x80411000). Must match CALLBACK_PTR
// in the shim asm below.
#define CALLBACK_PTR_ADDR 0x80410700

static fpc_ProcID player2_pid = fpcM_ERROR_PROCESS_ID_e;
static int spawned = 0;
static int frame_count = 0;

// Forward decl so main01_init can take its address.
void multiplayer_update(void);

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
        "lwz   12, 0x0700(12)   \n"
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

// Per-frame worker. Two phases:
//   Phase 1 (until spawned): queue the proxy actor via fopAcM_create. Construction
//     happens on the NEXT frame inside fpcM_Management's heap-bracketed flow.
//   Phase 2 (every frame after): resolve pid -> actor ptr via fopAcM_SearchByID,
//     copy mailbox->p2_pos_{x,y,z} into the actor's pos field (+0x1F8). This
//     turns the spawned proxy into a remote-controlled puppet; Go writes new
//     coords to the mailbox and the actor teleports each frame.
//
// Progress markers:
//    1 = entered, not yet spawned
//    2 = PLAYER_PTR_ARRAY[0] is non-null
//    3 = frame_count gate passed (~10 sec since boot)
//    4 = got link_pos/angle/room
//    5 = fopAcM_create returned
//    6 = got ERROR_PROCESS_ID (queue full or proc not registered)
//    7 = got valid pid (queued successfully)
//    8 = pid published to mailbox, spawned=1, initial target seeded
//    9 = per-frame sync: resolved actor, wrote pos
//   10 = per-frame sync: actor has been deleted (SearchByID failed)
//
// Proxy-actor history:
//   PROC_PLAYER     -> OOMs GameHeap (~704 KB alloc vs ~245 KB free).
//   PROC_Obj_Barrel -> queued OK but froze game on next frame (archive not
//                      resident on Outset; actor loader spins).
//   PROC_GRASS      -> spawns and renders stably, but child sprites are baked
//                      at parent's birth position — moving the parent does
//                      nothing visible.
//   PROC_TSUBO pm=0 -> uses the "Always" archive (resident everywhere per
//                      d_a_tsubo.cpp M_arcname[0]). Single movable entity.
void multiplayer_update(void) {
    mailbox->spawn_trigger++;

    fopAc_ac_c* link = PLAYER_PTR_ARRAY[0];
    if (!link) return;

    if (!spawned) {
        mailbox->progress = 1;
        frame_count++;
        if (frame_count < 300) return;
        mailbox->progress = 3;

        cXyz* link_pos = ACTOR_POS(link);
        csXyz* link_angle = ACTOR_ANGLE(link);
        s8 room = ACTOR_ROOM(link);
        mailbox->progress = 4;

        // Seed target pos with a point next to Link so the puppet is visible
        // before Go starts writing coords. +100 units on X puts it off to
        // the side rather than inside Link's bounding box.
        mailbox->p2_pos_x = link_pos->x + 100.0f;
        mailbox->p2_pos_y = link_pos->y;
        mailbox->p2_pos_z = link_pos->z;

        // PROC_KAMOME (seagull): archive is guaranteed resident on Outset
        // (seagulls fly over every island). Invisible by default because
        // kamome_class has `s8 mbNoDraw` at +0x2BC — zero it post-spawn
        // via `./ww.exe poke-u32 <actor_ptr+0x2BC> 0`. Fallback: PROC_TSUBO
        // (0x01CB) which uses the +0x678 mode_hide recipe.
        fpc_ProcID pid = fopAcM_create(PROC_KAMOME, 0, link_pos, room, link_angle, 0, -1, 0);
        mailbox->progress = 5;

        if (pid == fpcM_ERROR_PROCESS_ID_e) {
            mailbox->progress = 6;
            return;
        }

        player2_pid = pid;
        spawned = 1;
        mailbox->actor2_ptr = (u32)pid;
        mailbox->progress = 8;
        return;
    }

    // Phase 2: actor exists — resolve pid to ptr and write mailbox pos.
    fopAc_ac_c* actor = 0;
    if (!fopAcM_SearchByID(player2_pid, &actor) || !actor) {
        mailbox->progress = 10;
        return;
    }

    // Publish the live actor pointer (overwrites the pid value from phase 1).
    mailbox->actor2_ptr = (u32)actor;

    // NOTE: the pot stays in mode_hide (m678 == 0) until something pokes
    // m678 = 2. Doing that from here (every frame, starting immediately after
    // SearchByID returns) FREEZES construction — we clobber state that the
    // actor's own init sequence hasn't finished writing yet. The working
    // approach is to let the pot sit quietly in mode_hide for a while, then
    // poke m678 = 2 once from the host side (Go's `unhide-puppet` command).
    // See docs/05 "TSUBO puppet: mode_hide wake-up timing" for details.

    cXyz* pos = ACTOR_POS(actor);
    pos->x = mailbox->p2_pos_x;
    pos->y = mailbox->p2_pos_y;
    pos->z = mailbox->p2_pos_z;

    // Rotation sync. shape_angle is the VISUAL rotation (what the model
    // shows); current.angle is what physics/AI read. Write both so the
    // puppet faces the remote player's direction regardless of which field
    // the actor reads.
    csXyz* shape = ACTOR_SHAPE(actor);
    csXyz* angle = ACTOR_ANGLE(actor);
    shape->x = angle->x = mailbox->p2_rot_x;
    shape->y = angle->y = mailbox->p2_rot_y;
    shape->z = angle->z = mailbox->p2_rot_z;

    mailbox->progress = 9;
}
