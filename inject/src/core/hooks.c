// Wind Waker Multiplayer — the core's hooks (src/core; Phase E3 split
// multiplayer.c into src/core and src/mp, docs/api/00-design.md §6.6).
// Five entry points, all hooked via Freighter's hook_branchlink
// (inject/build.py):
//   main01_init — fires once from main01 (one-shot). Installs the address of
//     multiplayer_update at CALLBACK_PTR_ADDR so the per-frame shim can find
//     it, and writes the WWMB header.
//   frame_shim  — naked asm. Fires every frame from a hooked `bl` site.
//     Saves LR, loads the callback pointer, calls through it via bctrl,
//     restores LR, returns. Intentionally tiny so that replacing a `bl`
//     instruction at the hook site is transparent (callers already assume
//     volatile-reg clobber from a real bl).
//   daPy_draw_hook — Link #1's draw, bracketed by the core services and
//     followed by the mods' post-draw steps.
//   pad_read_shim — the synthetic-input override after PADRead.
//   exec_shim   — naked asm. Fires per actor execute from fopAc_Execute's
//     `bl fpcMtd_Execute`; branches to the REL's exec hook (mailbox+0x1BC)
//     when it is published, else to the vanilla fpcMtd_Execute.
//
// Why the indirection? Freighter's hook_branchlink overwrites the target
// instruction outright — if we hook fapGm_Execute's first instruction the
// prologue's stwu is lost and the function crashes. Hooking a `bl` instead
// of a prologue instruction is safe, and the callback indirection lets us
// keep multiplayer_update as a normal C function (the naked shim handles the
// minimal ABI dance).
//
// The hooks are core: they call the core services (services.c) and the
// mods (mods.h) directly, in the fixed orders written at
// multiplayer_update and daPy_draw_hook.

#include "wwapi.h"
#include "mailbox_core.h"
#include "wwrel.h"
#include "core.h"
#include "mods.h"

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

static int frame_count = 0;

// Hooked to main01 (0x80006338) via hook_branchlink. Runs once.
// Publishes multiplayer_update's address for frame_shim to find.
void main01_init(void) {
    *(volatile u32*)CALLBACK_PTR_ADDR = (u32)&multiplayer_update;
    // The REL's remote-Link words (wwrel.h). The slack is orphan memory
    // nothing zeroes for us (relload.c clears the magic only once Link
    // exists), and exec_shim branches to WWREL_EXEC_HOOK_OFF on the first
    // actor execute: a stale word from a reset or an earlier boot must
    // read as "no module" before any actor runs. _prolog writes it again
    // (last) when the module links.
    *(volatile u32*)(MAILBOX_ADDR + WWREL_EXEC_HOOK_OFF) = 0;
    *(volatile u32*)(MAILBOX_ADDR + WWREL_SWAP_STATE_OFF) = 0;
    // The full bodies' shanm table (an archive-heap block the REL allocates
    // once per boot and never frees): a pointer left from before this boot
    // names a heap that no longer exists.
    *(volatile u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF) = 0;
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

// --- Actor-execute hook (remote-Link, design §4) ----------------------
// Hooked at 0x8002395C inside fopAc_Execute (0x8002362C), replacing
// `bl fpcMtd_Execute` (0x8003EF38) on the move-approved path
// (f_op_actor.cpp:200). At the bl, r3 = actor->sub_method (lwz 0xEC(r30))
// and r4 = the actor (mr r4,r30); DOL words at 0x80023954:
// 807E00EC 7FC4F378 4801B5DD 7C7F1B78. Paused or event-stopped actors
// never reach the bl.
//
// The shim reads WWREL_EXEC_HOOK (mailbox+0x1BC, wwrel.h): the REL's
// wwrel_exec_hook while the module is linked, else 0. Either target is
// tail-called through ctr with r3/r4 and LR (0x80023960) untouched, so the
// callee returns straight to fopAc_Execute and its r3 result is the
// original's. No frame and no LR replay (unlike frame_shim): r12, ctr and
// cr0 are volatile across a bl. 10 instructions, 0x28 bytes. All policy
// lives in the REL and in Go-written words (design D4); T2 only branches.
//
// Code lifetime: fpcM_Management runs fpcDt_Handler, fpcCt_Handler, then
// fpcEx_Handler (f_pc_manager.cpp:267-295), so the module cannot unlink
// while an execute is in flight; _prolog writes the word last and _epilog
// clears it first (rel_main.c).
//
// MUST match the `lwz 12, 0x30BC(12)` offset in exec_shim asm below.
#define EXEC_HOOK_PTR_ADDR 0x804130BC
_Static_assert(EXEC_HOOK_PTR_ADDR == MAILBOX_ADDR + WWREL_EXEC_HOOK_OFF,
               "exec_shim's lwz offset must load mailbox+WWREL_EXEC_HOOK_OFF");
_Static_assert((EXEC_HOOK_PTR_ADDR >> 16) == 0x8041 && (EXEC_HOOK_PTR_ADDR & 0x8000) == 0,
               "exec_shim's lis 12,0x8041 + positive lwz offset must reach EXEC_HOOK_PTR_ADDR");
#define FPCMTD_EXECUTE_ADDR 0x8003EF38   // fpcMtd_Execute (symbols.txt:1162)

__attribute__((naked))
void exec_shim(void) {
    asm volatile(
        "lis   12, 0x8041       \n"
        "lwz   12, 0x30BC(12)   \n"  // = EXEC_HOOK_PTR_ADDR (mailbox+0x1BC) = 0x804130BC
        "cmpwi 12, 0            \n"
        "beq-  1f               \n"
        "mtctr 12               \n"
        "bctr                   \n"  // wwrel_exec_hook(r3, r4); returns to 0x80023960
        "1:                     \n"
        "lis   12, 0x8003       \n"
        "ori   12, 12, 0xEF38   \n"  // = FPCMTD_EXECUTE_ADDR, the vanilla callee
        "mtctr 12               \n"
        "bctr                   \n"  // fpcMtd_Execute(r3, r4); returns to 0x80023960
    );
}

// Per-frame worker, called by frame_shim through CALLBACK_PTR_ADDR. The
// order, written down as if it were a dispatcher (§6.6):
//
//   core:  heartbeat (spawn_trigger++)
//   mods:  mp_frame_any(link)    every frame, Link or not
//                                [today: the actor-puppet reap]
//   core:  no Link -> stop; warp handler; 300-frame boot gate
//          (progress = 1 until it passes); tunic image allocation
//   mods:  mp_frame(link)        [today: actor puppets, mini-Link models,
//                                 the face hook's arm / disarm, progress]
//
// The warp handler sits ABOVE the frame_count gate so it fires the
// moment Link's actor pointer is valid (just after the save-load-stable
// point).
void multiplayer_update(void) {
    mailbox->spawn_trigger++;

    fopAc_ac_c* link = PLAYER_PTR_ARRAY[0];
    mp_frame_any(link);
    if (!link) return;

    warp_frame(link);

    frame_count++;
    if (frame_count < 300) {
        mailbox->progress = 1;
        return;
    }

    tunic_alloc();
    mp_frame(link);
}

// --- Draw-phase hook -------------------------------------------------
// Hooked at 0x80108210 via Freighter (replaces the `bl daPy_lk_c::draw`
// inside daPy_Draw). daPy_Draw runs inside fpcDw_Execute during the
// actor-draw iteration — i.e. the legitimate draw phase, same as every
// other actor's draw callback.
//
// The order (§6.6), written down as if it were a dispatcher:
//
//   core pre:   face_publish_local -> local tunic swap
//   mods pre:   (priority order)            [today: none]
//   original:   daPy_lk_c::draw 0x80107308
//   core post:  local tunic restore -> pose publish
//   mods post:  (REVERSE priority order)    [today: mp_draw_post: the
//               mini-Link per slot, its face and tunic bracket, the eye fix]
//
// Post-draw steps run in the reverse of the pre-draw order, so each
// bracket closes inside the one opened before it (the tunic swap opens
// after the face snapshot and closes before the pose publish). Drawing
// models stays a core service because the face and tunic swaps mutate
// the J3DModelData every Link shares.
//
// Link's real draw gets the original `this` arg (r3), so ALL existing
// Link rendering is preserved; its return value propagates — daPy_Draw
// just tail-returns it to its caller.
int daPy_draw_hook(void* this_) {
    // SESSION 14: snapshot LOCAL Link's natural btp eye state into
    // mailbox.face_state_local for race-free broadcast-pose capture.
    // Must run BEFORE any face swap (mp_draw_post) — at this point
    // tev_block still holds btp's frame value (no swap has fired yet
    // this draw). No-op until a mod has bound the model data
    // (face_bind; the multiplayer mod does when it makes its models).
    face_publish_local();

    tunic_swap(this_, TUNIC_SLOT_LOCAL);
    int result = daPy_lk_c_draw(this_);
    tunic_restore();
    pose_publish(this_);

    mp_draw_post(this_);
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
