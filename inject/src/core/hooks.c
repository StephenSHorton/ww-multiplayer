// Wind Waker Multiplayer — the core's hooks (src/core; Phase E3 split
// multiplayer.c into src/core and src/mp, docs/api/00-design.md §6.6).
// Six entry points, all hooked via Freighter's hook_branchlink
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
//   seachart_shim — naked asm. Fires from dDlst_FMAP_c::draw's `bl
//     J2DScreen::draw` (the pause menu's sea chart); runs it, then the
//     REL's sea chart draw (visuals block +0x04) when it is published.
//
// Why the indirection? Freighter's hook_branchlink overwrites the target
// instruction outright — if we hook fapGm_Execute's first instruction the
// prologue's stwu is lost and the function crashes. Hooking a `bl` instead
// of a prologue instruction is safe, and the callback indirection lets us
// keep multiplayer_update as a normal C function (the naked shim handles the
// minimal ABI dance).
//
// The hooks are core: they call the core services (services.c) and the
// API region's rings (ops.c) directly, and the mods (mods.h) through the
// ww_modules[] table, in the orders written at multiplayer_update and
// daPy_draw_hook.

#include "wwapi.h"
#include "mailbox_core.h"
#include "wwrel.h"
#include "wwvis.h"
#include "core.h"
#include "mods.h"

// Where main01_init stashes the callback pointer. Must sit OUTSIDE our T2
// code section. History:
//   v1 (mod ~0x748 B): 0x80410700. Broke when mod grew past 0x700.
//   v2 (mod ~0x868 B): 0x80410808. Broke when mod grew past 0x800.
//   v3 (mod ~0x900 B): 0x80410908. Broke when mod grew past 0x900.
//   v4 (mod ~0x9A8 B): 0x80410F08 @ __OSArenaLo = 0x80411000.
//   v5 (mod ~0x11C8 B): 0x80411F08 @ __OSArenaLo = 0x80412000.
//   v6 (mod ~0x1FC8 B): 0x80412F08 @ __OSArenaLo = 0x80413000 (0x80414000 later).
//       Eye-fix recipe pushed .text past 0x80411F00, so the whole
//       mailbox + __OSArenaLo pair shifted up by another 0x1000. See
//       inject/build.py for the matching OSInit patch.
//   v7 (T2 0x2D08 B): 0x80414F08 @ __OSArenaLo = 0x80416000
//       (wind#80: room for co-op M7's code below the mailbox).
//   v8 (current, T2 past 0x4000 B): 0x80415F08 @ __OSArenaLo = 0x80417000
//       (native widescreen stages 2-3: the API region and the mailbox
//       moved up 0x1000, native-widescreen.md section 6 lever 4).
// MUST match the `lwz 12, 0x5F08(12)` offset in frame_shim asm below.
#define CALLBACK_PTR_ADDR 0x80415F08
_Static_assert(CALLBACK_PTR_ADDR == MAILBOX_ADDR + 8, "frame_shim reads the callback at mailbox+0x08");

static int frame_count = 0;

// The C mods, in priority order (mods.h). The multiplayer mod has no init
// and no pre-draw step.
#define MOD_ID_MP 0x4D502020   // 'MP  '
#define MOD_ID_WS 0x57532020   // 'WS  '
static const WwModuleDesc ww_modules[] = {
    { MOD_ID_MP, 0, mp_frame_any, mp_frame, 0, mp_draw_post },
    { MOD_ID_WS, ws_init, ws_frame_any, 0, 0, 0 },
};
#define WW_MODULE_COUNT ((int)(sizeof(ww_modules) / sizeof(ww_modules[0])))

// main01_init's per-boot zeroing (below) clears these runs by word count.
_Static_assert(__builtin_offsetof(Mailbox, pose_publish_seq) + 1 == __builtin_offsetof(Mailbox, pose_buf_ptrs) + 8 * 4 &&
               __builtin_offsetof(Mailbox, features) == __builtin_offsetof(Mailbox, tunic_bufs) + 6 * 4 &&
               __builtin_offsetof(Mailbox, tunic_state) % 4 == 0 &&
               __builtin_offsetof(Mailbox, tunic_swaps) == __builtin_offsetof(Mailbox, tunic_state) + 4,
               "mailbox_core.h moved a run main01_init zeroes");

// view_setup's call to mDoLib_clipper::setup (0x8017BF90, the only caller;
// docs/design/widescreen-culling.md section 5). f1..f4 arrive in
// view->mFovy, mAspect, mNear and the far plane (EABI), so a plain C
// function can forward them: the clipper's aspect is widened by the
// widescreen mod, the camera's mAspect and the GPU projection are not.
typedef void (*clip_setup_fn)(f32 fovy, f32 aspect, f32 near_, f32 far_);
#define mDoLib_clipper_setup ((clip_setup_fn)0x800176BC)
void clip_setup_hook(f32 fovy, f32 aspect, f32 near_, f32 far_) {
    mDoLib_clipper_setup(fovy, ws_clip_aspect(aspect), near_, far_);
}

// The picto box judge's call to dSnap_packet::SetResult (0x800CE5E8, `bl
// 0x800CEB80`; r3 = the dSnap_packet* from `mr r3,r31`; docs/design/
// native-widescreen.md 3.6 option A). In native mode the 3D is widened by F,
// so each subject covers 1/F as many EFB pixels: scale the counted pixels
// back by F before the verdict. The ratios are left alone.
typedef void (*snap_setresult_fn)(void* pkt);
#define dSnap_packet_SetResult ((snap_setresult_fn)0x800CEB80)
__attribute__((optimize("Os"))) void ws_snap_setresult(void* pkt) {
    f32 f = ws_live_factor();
    if (f != 1.0f) {
        s32 n = *(s32*)((u8*)pkt + 0x14);
        s32 col;
        for (col = 0; col < n; col++) {
            s32* px = (s32*)((u8*)pkt + 0x38 + col * 0x3C);  // m_tbl[col].m_obj.mCapturedPixels
            *px = (s32)((f32)*px * f + 0.5f);
        }
    }
    dSnap_packet_SetResult(pkt);
}

// --- Native widescreen, stages 2-3 (docs/design/native-widescreen.md) ---
// Every hook below replaces one `bl` (build.py; each site pinned in
// internal/inject/t2_budget_test.go), calls the vanilla callee itself and
// reads the latched 2D factor F = ws_2d_factor() (src/ws): 1.0 unless 2D
// is compressed, and then each one does exactly what the vanilla call
// did. The game functions are build.py's GAME_SYMBOLS (plain `bl`s).
// Built for size (T2 budget, section 6).
#pragma GCC push_options
#pragma GCC optimize("Os")

typedef struct { f32 x0, y0, x1, y1; } WsBox;
extern void J2DOrthoGraph_setOrtho(void* graf, const WsBox* box, f32 far_, f32 near_);
extern void J2DOrthoGraph_setPort(void* graf);
extern void mDoLib_project(cXyz* src, cXyz* dst);
extern void* JKRFileLoader_getGlbResource(const char* name, void* loader);
extern int J2DScreen_set_stream(void* scrn, void* stream);
extern void J2DScreen_draw(void* scrn, f32 x, f32 y, void* graf);
extern void dMap_calcScissor(void);
extern void C_MTXPerspective(void* m, f32 fovy, f32 aspect, f32 near_, f32 far_);
extern void dPb_shutterLineDraw(void* pb, u8 alpha);
extern void J2DFillBox(f32 x, f32 y, f32 w, f32 h, const u32* rgba);

// x' = 320.5 + (x - 320.5) * F: a 2D x widened (or a box edge pushed out)
// about the 2D centre.
static f32 ws_wx(f32 x, f32 F) {
    return 320.5f + (x - 320.5f) * F;
}

// 3.2: the four `bl J2DOrthoGraph::setOrtho` (0x8000B06C, 0x8000BA30 in
// mDoGph_Painter; 0x80223CF4 dOvlpFd2; 0x8003EADC drawDvdCondition): the
// box's x widened about its centre by F, so after Dolphin's stretch all
// game 2D has its true proportions. setOrtho only stores the box.
void ws_ortho_hook(void* graf, const WsBox* box, f32 far_, f32 near_) {
    WsBox b = *box;
    f32 F = ws_2d_factor();
    if (F != 1.0f) {
        f32 c = 0.5f * (b.x0 + b.x1);
        b.x0 = c + (b.x0 - c) * F;
        b.x1 = c + (b.x1 - c) * F;
    }
    J2DOrthoGraph_setOrtho(graf, &b, far_, near_);
}

// 3.4: 2D drawn at a projected 3D point (7 sites: boomerang and aim
// reticles, the enemy HP bar, the magic bar's flight, the talk box's
// start, the baton sparkle). mDoLib_project returns EFB px, which follow
// the widened 3D; the 2D x that lands on the same pixel is ws_wx(p).
void ws_project_2d(cXyz* src, cXyz* dst) {
    f32 F;
    mDoLib_project(src, dst);
    F = ws_2d_factor();
    if (F != 1.0f)
        dst->x = ws_wx(dst->x, F);
}

// 3.3 hook A (0x802D0968, J2DScreen::set(name, archive)'s `bl
// JKRFileLoader::getGlbResource`): stash the name for hook B. Returns the
// resource unchanged (the caller tests it; on NULL it never reaches B and
// the next set overwrites the stash).
void* ws_set_name_hook(const char* name, void* loader) {
    ws_screen_name = name;
    return JKRFileLoader_getGlbResource(name, loader);
}

// 3.3 hook B (0x802D09DC, the only `bl J2DScreen::set(stream)`): build the
// panes, then apply the screen's row (ws_screen_loaded). Returns set's
// result unchanged: it becomes set(name)'s own.
int ws_set_stream_hook(u8* scrn, void* stream) {
    int ok = J2DScreen_set_stream(scrn, stream);
    ws_screen_loaded(scrn, ok);
    return ok;
}

// 2D-7: the minimap's icon scissor (dMap_c::calcScissor's only `bl`,
// 0x8004A64C). Vanilla maps the map's 2D x to EFB px with the vanilla box
// (x -9 over 659 units, 640 px) and a 116 px width; with the compressed
// box L' = ws_wx(-9) over 659F units, and 116/F px. Then the vanilla
// clamp (left only). Y and height stay.
#define MAP_DISP_X    (*(volatile s16*)0x803F6AD8)   // mDispPosLeftUpX
#define MAP_SCISSOR_X (*(volatile s32*)0x803F6AE4)
#define MAP_SCISSOR_W (*(volatile s32*)0x803F6AE8)
void ws_map_scissor(void) {
    f32 F;
    dMap_calcScissor();
    F = ws_2d_factor();
    if (F != 1.0f) {
        s32 x = (s32)(((f32)MAP_DISP_X - ws_wx(-9.0f, F)) * 640.0f / (659.0f * F));
        s32 w = (s32)(116.0f / F + 0.5f);
        if (x < 0) {
            w += x;
            if (w < 0)
                w = 0;
            x = 0;
        }
        MAP_SCISSOR_X = x;
        MAP_SCISSOR_W = w;
    }
}

// 3D-12: the file select's camera (dScnName_c::setView, 0x802306A0) and
// cloth panel (0x802324A8) `bl C_MTXPerspective`: their aspect follows the
// 2D, which they sit behind (their own 0.96 * w/h, never the 3D knob).
void ws_persp(void* m, f32 fovy, f32 aspect, f32 near_, f32 far_) {
    C_MTXPerspective(m, fovy, aspect * ws_2d_factor(), near_, far_);
}

// 3.6 (option A): the picto box's viewfinder and album (wipe_01_01,
// wipe_01_02) and its shutter lines draw uncompressed, so the frame
// matches the EFB rectangle the capture copies. While 2D is compressed the
// graf's box (+0xBC, which setPort reads) is set to the vanilla -9..650 x
// -21..503 around the draw and put back.
#define CURRENT_GRAF (*(void**)0x803CA908)
#define GRAF_BOX(g)  ((WsBox*)((u8*)(g) + 0xBC))
static const WsBox ws_vanilla_box = { -9.0f, -21.0f, 650.0f, 503.0f };
static void ws_full_draw(void* scrn, void* graf, void* pb, u8 alpha) {
    WsBox save;
    int wide = ws_2d_factor() != 1.0f;
    if (wide) {
        save = *GRAF_BOX(graf);
        *GRAF_BOX(graf) = ws_vanilla_box;
        J2DOrthoGraph_setPort(graf);
    }
    if (pb)
        dPb_shutterLineDraw(pb, alpha);
    else
        J2DScreen_draw(scrn, 0.0f, 0.0f, graf);
    if (wide) {
        *GRAF_BOX(graf) = save;
        J2DOrthoGraph_setPort(graf);
    }
}
// dJle_Pb_c::draw's four `bl J2DScreen::draw` (0x8022B510, 0x8022B538,
// 0x8022B778, 0x8022B9B8; each passes x = y = 0, verified in the DOL).
void ws_screen_draw_full(void* scrn, f32 x, f32 y, void* graf) {
    ws_full_draw(scrn, graf, 0, 0);
}
// Its two `bl shutterLineDraw` (0x8022B524, 0x8022B9CC): no graf
// argument, it draws with the current one.
void ws_shutter_draw_full(void* pb, u8 alpha) {
    ws_full_draw(0, CURRENT_GRAF, pb, alpha);
}

// 3.5: the opening storybook (dScnOpen's `bl J2DScreen::draw`, 0x8023452C)
// is 660 units wide and stops at the 4:3 edges: after its draw, black
// fills over the bands, x L'..-9 and 650..R' (y -21..503). setPort first:
// J2DFillBox draws in whatever projection and vertex state is current.
void ws_opening_draw(void* scrn, f32 x, f32 y, void* graf) {
    f32 F, l;
    J2DScreen_draw(scrn, x, y, graf);
    F = ws_2d_factor();
    if (F != 1.0f) {
        u32 black = 0x000000FF;
        l = ws_wx(-9.0f, F);
        J2DOrthoGraph_setPort(graf);
        J2DFillBox(l, -21.0f, -9.0f - l, 524.0f, &black);
        J2DFillBox(650.0f, -21.0f, -9.0f - l, 524.0f, &black);   // R' - 650 = -9 - L'
    }
}
#pragma GCC pop_options

// --- Native widescreen, stages 4-5 (docs/design/native-widescreen.md 3.4) ---
// Tests that measure a projected 3D point's distance or angle from the EFB
// centre (320, 240): the lens flare's streak angle (dKyr_lenzflare_move,
// 0x8008C6C0), the stars hidden around the moon (dKyr_drawStar: the moon
// 0x800994AC and each star 0x80099A6C, both or neither) and the telescope
// look-in (dKy_telescope_lookin_chk 0x8019744C; the radius is the caller's
// f1, 100 for the moon, Orion and Hokuto, so the point is moved, not the
// radius). In native mode the 3D is widened by F, so a point lands 1/F as
// far from the centre in x as it shows on screen: x is pushed back out by
// the live 3D factor about 320 (exact for these tests; ws_project_2d's 320.5
// is a 2D landing pivot). At F = 1 it is the vanilla call.
__attribute__((optimize("Os"))) void ws_project_live(cXyz* src, cXyz* dst) {
    f32 F;
    mDoLib_project(src, dst);
    F = ws_live_factor();
    if (F != 1.0f)
        dst->x = 320.0f + (dst->x - 320.0f) * F;
}

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
    // The visuals block's REL words (wwvis.h): the two function words T2
    // calls through, and the models the REL made on a heap of an earlier
    // boot (both model tables, with the counters between and after them).
    {
        int w;
        for (w = 0; w < 2; w++)
            *(volatile u32*)(MAILBOX_ADDR + WWVIS_OFF + WWVIS_DRAW_FN_OFF + 4 * w) = 0;
        for (w = 0; w < WWVIS_ZERO_WORDS; w++)
            *(volatile u32*)(MAILBOX_ADDR + WWVIS_OFF + WWVIS_MODELS_OFF + 4 * w) = 0;
        // The Mailbox's own GameHeap pointers, for the same reason (docs/05
        // "Soft reset"): a soft reset (mDoRst_reset -> OSResetSystem
        // restart, d_s_logo.cpp:503, m_Do_Reset.cpp:76) reloads the DOL,
        // which zeroes T2's statics, and builds a new GameHeap in main01's
        // mDoMch_Create, but leaves this RAM as it was. pose_publish and
        // tunic_alloc trust their state bytes, so the old boot's
        // pose_publish_ptr (2 KB copied into it every frame) and tunic_bufs
        // would name blocks of the new heap that belong to someone else.
        // This hook is main01's first instruction, before mDoMch_Create, so
        // the words are zero before any block of the new heap exists. Go
        // follows: it re-reads the states (and tunic_bufs) every tick.
        //   +0xA8..+0xC7  pose_buf_ptrs .. pose_publish_seq (C allocates
        //                 again; the pose seqs read "no pose yet")
        //   +0x188..+0x19F tunic_bufs, tunic_buf_size, tunic_enable (Go
        //                 writes the size again, C allocates, Go the images)
        //   +0x1A4        tunic_state's word (and its padding)
        for (w = 0; w < 8; w++)
            ((volatile u32*)&mailbox->pose_buf_ptrs[0])[w] = 0;
        for (w = 0; w < 6; w++)
            ((volatile u32*)&mailbox->tunic_bufs[0])[w] = 0;
        *(volatile u32*)&mailbox->tunic_state = 0;
    }
    // WWMB header (Phase E1): magic, ABI, layout version and the
    // features word, for Go's contract check (wwapi.CheckContract).
    mailbox->hdr_magic = WWMB_MAGIC;
    mailbox->hdr_abi = WWMB_ABI;
    mailbox->hdr_layout = WWMB_LAYOUT;
    mailbox->features = WW_FEATURES;
    // The API region (wwops.h): its rings and the core ops, then each
    // mod's init (which may register ops of its own).
    ops_init();
    {
        int i;
        for (i = 0; i < WW_MODULE_COUNT; i++)
            if (ww_modules[i].init)
                ww_modules[i].init();
    }
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
        "lwz   12, 0x5F08(12)   \n"  // = CALLBACK_PTR_ADDR (mailbox+0x08) = 0x80415F08
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
// MUST match the `lwz 12, 0x60BC(12)` offset in exec_shim asm below.
#define EXEC_HOOK_PTR_ADDR 0x804160BC
_Static_assert(EXEC_HOOK_PTR_ADDR == MAILBOX_ADDR + WWREL_EXEC_HOOK_OFF,
               "exec_shim's lwz offset must load mailbox+WWREL_EXEC_HOOK_OFF");
_Static_assert((EXEC_HOOK_PTR_ADDR >> 16) == 0x8041 && (EXEC_HOOK_PTR_ADDR & 0x8000) == 0,
               "exec_shim's lis 12,0x8041 + positive lwz offset must reach EXEC_HOOK_PTR_ADDR");
#define FPCMTD_EXECUTE_ADDR 0x8003EF38   // fpcMtd_Execute (symbols.txt:1162)

__attribute__((naked))
void exec_shim(void) {
    asm volatile(
        "lis   12, 0x8041       \n"
        "lwz   12, 0x60BC(12)   \n"  // = EXEC_HOOK_PTR_ADDR (mailbox+0x1BC) = 0x804160BC
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

// --- Sea chart hook (ww-online review idea 14) ------------------------
// Hooked at 0x801BB06C inside dDlst_FMAP_c::draw (0x801BB024, size 0x64,
// d_menu_fmap.cpp:3481-3485), replacing `bl draw__9J2DScreenFffPC14J2D
// GrafContext` (0x802D0F2C) with r3 = scrn, f1 = f2 = 0.0, r4 = the graf
// port; r30 = this (the menu's fmapDl) and r31 = the port, both
// callee-saved. DOL words at 0x801BB068: 7FE4FB78 48115EC1 83E1000C.
// The shim calls the real J2DScreen::draw with those arguments untouched,
// then the REL's wwvis_seachart_draw(r30, r31) through the visuals
// block's sea chart word (wwvis.h WWVIS_SEACHART_FN_OFF) when it is
// published, and returns to 0x801BB070. The menu runs while no actor
// does; _epilog clears the word before the module can unlink (in
// fpcDt_Handler, after the painter), and main01_init zeroes it at boot.
// ww-online patches 0x801BB070 (the lwz after this bl) with a branch to
// the same kind of call (GameMod/src/patches/seachart_players.asm, MIT);
// we replace the bl instead, as our other hooks do. 15 instructions.
//
// MUST match the `lwz 12, 0x67D4(12)` offset in seachart_shim asm below.
#define SEACHART_FN_PTR_ADDR 0x804167D4
_Static_assert(SEACHART_FN_PTR_ADDR == MAILBOX_ADDR + WWVIS_OFF + WWVIS_SEACHART_FN_OFF,
               "seachart_shim's lwz offset must load the visuals block's sea chart word");
_Static_assert((SEACHART_FN_PTR_ADDR >> 16) == 0x8041 && (SEACHART_FN_PTR_ADDR & 0x8000) == 0,
               "seachart_shim's lis 12,0x8041 + positive lwz offset must reach SEACHART_FN_PTR_ADDR");
#define J2DSCREEN_DRAW_ADDR 0x802D0F2C   // draw__9J2DScreenFffPC14J2DGrafContext (symbols.txt:11534)

__attribute__((naked))
void seachart_shim(void) {
    asm volatile(
        "mflr  0                \n"  // 0x801BB070
        "stwu  1, -0x10(1)      \n"
        "stw   0, 0x14(1)       \n"
        "lis   12, 0x802D       \n"
        "ori   12, 12, 0x0F2C   \n"  // = J2DSCREEN_DRAW_ADDR
        "mtctr 12               \n"
        "bctrl                  \n"  // scrn->draw(0, 0, port): r3/f1/f2/r4 as the caller set them
        "lis   12, 0x8041       \n"
        "lwz   12, 0x67D4(12)   \n"  // = SEACHART_FN_PTR_ADDR (mailbox+0x8D4) = 0x804167D4
        "cmpwi 12, 0            \n"
        "beq-  1f               \n"
        "mr    3, 30            \n"  // dDlst_FMAP_c* this
        "mr    4, 31            \n"  // the graf port
        "mtctr 12               \n"
        "bctrl                  \n"  // wwvis_seachart_draw(this, port)
        "1:                     \n"
        "lwz   0, 0x14(1)       \n"
        "addi  1, 1, 0x10       \n"
        "mtlr  0                \n"
        "blr                    \n"
    );
}

// Per-frame worker, called by frame_shim through CALLBACK_PTR_ADDR. The
// order, written down as if it were a dispatcher (§6.6):
//
//   core:  heartbeat (spawn_trigger++); the command drain (ops_frame)
//   mods:  frame_any(link)       every frame, Link or not, in table order
//                                [today: mp_frame_any, the actor-puppet reap]
//   core:  no Link -> stop; warp handler; 300-frame boot gate
//          (progress = 1 until it passes); tunic image allocation
//   mods:  frame(link)           in table order [today: mp_frame: actor
//                                 puppets, mini-Link models, the face hook's
//                                 arm / disarm, progress]
//
// The warp handler sits ABOVE the frame_count gate so it fires the
// moment Link's actor pointer is valid (just after the save-load-stable
// point).
void multiplayer_update(void) {
    int i;
    mailbox->spawn_trigger++;
    ops_frame();

    fopAc_ac_c* link = PLAYER_PTR_ARRAY[0];
    for (i = 0; i < WW_MODULE_COUNT; i++)
        if (ww_modules[i].frame_any)
            ww_modules[i].frame_any(link);
    if (!link) return;

    warp_frame(link);

    frame_count++;
    if (frame_count < 300) {
        mailbox->progress = 1;
        return;
    }

    tunic_alloc();
    for (i = 0; i < WW_MODULE_COUNT; i++)
        if (ww_modules[i].frame)
            ww_modules[i].frame(link);
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
//   mods pre:   draw_pre, table order       [today: none]
//   original:   daPy_lk_c::draw 0x80107308
//   core post:  local tunic restore -> pose publish
//   mods post:  draw_post, REVERSE order    [today: mp_draw_post: the
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
    int i;
    // SESSION 14: snapshot LOCAL Link's natural btp eye state into
    // mailbox.face_state_local for race-free broadcast-pose capture.
    // Must run BEFORE any face swap (mp_draw_post) — at this point
    // tev_block still holds btp's frame value (no swap has fired yet
    // this draw). No-op until a mod has bound the model data
    // (face_bind; the multiplayer mod does when it makes its models).
    face_publish_local();

    tunic_swap(this_, TUNIC_SLOT_LOCAL);
    for (i = 0; i < WW_MODULE_COUNT; i++)
        if (ww_modules[i].draw_pre)
            ww_modules[i].draw_pre(this_);
    int result = daPy_lk_c_draw(this_);
    tunic_restore();
    pose_publish(this_);

    for (i = WW_MODULE_COUNT - 1; i >= 0; i--)
        if (ww_modules[i].draw_post)
            ww_modules[i].draw_post(this_);
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
