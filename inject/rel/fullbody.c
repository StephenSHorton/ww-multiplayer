// fullbody.c: the remote-Link full body, see fullbody.h. Design:
// ww-multiplayer-handoff/design/remote-link-stage4-plan.md D4-6 and
// sections 3.3-3.8 (the minimal ww-online puppet recipe), remote-link-
// design.md section 9; the contract is include/wwremote.h (WWREMOTE_FULL_*,
// WWREMOTE_FP_*, WWREMOTE_SA_*), the address surface rel/dapy.h. The
// create/delete windows (global snapshot, linktex ResTIMG, shared-animator
// window around playerInit, makeBgWait and playerDelete; the mAcch fixups;
// params 0xFF000040) port ww-online's GameMod/src/puppet_link/puppet.c
// (MIT notice below); execute and draw are this project's: no proc, no
// calc, no draw, ever.
//
// create   own three-phase machine, the phase byte in the tail (the
//          framework treats any create return but cPhs_INIT_e/LOADING as
//          final, f_pc_base.cpp fpcBs_SubCreate, so every phase but the
//          last returns cPhs_INIT_e): FP_NEW caches slot/gen, allocates the
//          shanm table and rewrites the params; FP_PARAMS gates on the host
//          Link, runs the ctor and playerInit in the window; FP_INIT runs
//          makeBgWait in the window until it finds ground, then completes.
//          Every call counts; past WWREMOTE_FULL_CREATE_FRAMES it is
//          cPhs_ERROR_e with BERR_PHASE_TIMEOUT. An ERROR always reaches
//          delete (fpcCtRq_Cancel -> fpcDt_Delete), which cleans up.
//          JKRHeap::sCurrentHeap and mDoExt_SaveCurrentHeap are saved
//          before playerInit and put back after it.
// execute  park check and hit read as the lite body; the 42 cl.bdl node
//          matrices from the pose feed into mpCLModel; combat bytes,
//          mCurProc WAIT / CROUCH_DEFENSE, mDamageWaitTimer, mResetFlg0 = 0;
//          setCollision with the Deku Leaf anm frames clamped; Co off unless
//          FLAG2_FULL_CO.
// draw     TRUE (never daPy_Draw: the mini-Link renders the remote).
// delete   the keeper's record and the m714 rewrite; then, once playerInit
//          ran, playerDelete inside the window; the delete scan. After a
//          playerInit that came back with the heap counters up (init_bad),
//          playerDelete only if all ten child heaps it destroys exist, else
//          the partial teardown (fb_teardown_partial).
//
// MIT License
//
// Copyright (c) 2026 datstarkey
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

#include "mailbox_core.h"
#include "wwrel.h"
#include "fullbody.h"
#include "shanm.h"
#include "dapy.h"

#define RB_ADDR      (MAILBOX_ADDR + WWREMOTE_OFF)
#define RB_U8(off)   (*(volatile u8*)(RB_ADDR + (off)))
#define RB_S8(off)   (*(volatile s8*)(RB_ADDR + (off)))
#define RB_S16(off)  (*(volatile s16*)(RB_ADDR + (off)))
#define RB_F32(off)  (*(volatile f32*)(RB_ADDR + (off)))
#define RB_SLOT(i)   (WWREMOTE_SLOTS_OFF + (i) * WWREMOTE_SLOT_SIZE)
#define AF(a, T, off) (*(T*)((u8*)(a) + (off)))
#define FB_TAIL(a)   ((rl_full_tail*)((u8*)(a) + WWREMOTE_FULL_TAIL_OFF))
#define FB_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + (off)))

// fopAc_ac_c (f_op_actor.h:268-321; remote.c has the same)
#define AC_PARAMS         0x0B0
#define AC_PROC_NAME      0x008
#define AC_OLD            0x1E4
#define AC_CURRENT        0x1F8
#define AC_CUR_POS        0x1F8
#define AC_CUR_ANGLE_Y    0x206
#define AC_CUR_ROOM       0x20A
#define AC_SPEEDF         0x254
#define AC_EYE_POS        0x260
#define PROC_PLAYER       0x00A9
// daPy_py_c combat bytes (d_a_player.h:479-482)
#define PY_CUT_TYPE       0x290
#define PY_CUT_COUNT      0x291
// collider (remote.c's CC_*: dCcD_Cyl, Step -1)
#define CC_TG_RPRM        0x1C
#define CC_CO_SPRM        0x2C
#define CC_GTG            0x94
#define CC_GTG_GUARD_BITS 0x09
#define FB_CYL(a)         ((u8*)(a) + WWREMOTE_FULL_CYL_OFF)
// mAcch (dBgS_LinkAcch at +0x46C): cBgS_Chk mPolyPassChk +0x00,
// mGrpPassChk +0x04, mActorPid +0x08 (c_bg_s_chk.h:16-18); dBgS_Chk's
// dBgS_PolyPassChk at +0x14 (mbLinkThrough +0x1A), dBgS_GrpPassChk +0x20
// (ww-online puppet.c:374-400)
#define ACCH_POLY_PASS    0x00
#define ACCH_GRP_PASS     0x04
#define ACCH_ACTOR_PID    0x08
#define ACCH_POLY_CHK     0x14
#define ACCH_GRP_CHK      0x20
#define ACCH_LINK_THROUGH 0x1A
#define AC_PID            0x004

#define cPhs_INIT_e       0       // SSystem/SComponent/c_phase.h: call again
#define cPhs_COMPLEATE_e  4
#define cPhs_ERROR_e      5

typedef void (*fb_clr_hit_t)(void* obj);                                // ClrTgHit__12dCcD_GObjInfFv
#define dCcD_GObjInf_ClrTgHit ((fb_clr_hit_t)0x800AB958)
typedef void (*fb_set_stage_layer_t)(void* proc);                       // fopAcM_setStageLayer__FPv
#define fopAcM_setStageLayer  ((fb_set_stage_layer_t)0x80024060)

typedef char fb_check_sizes[(WWREMOTE_FULL_TAIL_OFF + sizeof(rl_full_tail) <= WWREMOTE_FULL_SIZE &&
                             WWREMOTE_FULL_CYL_OFF + 0x130 <= WWREMOTE_FULL_DAPY_SIZE &&
                             __builtin_offsetof(Mailbox, pose_buf_ptrs) == 0xA8 &&
                             __builtin_offsetof(Mailbox, pose_joint_counts) == 0xB0 &&
                             __builtin_offsetof(Mailbox, pose_buf_states) == 0xB4 &&
                             __builtin_offsetof(Mailbox, pose_seqs) == 0xB6) ? 1 : -1];

static int fb_mem1(u32 p) {
    return p - 0x80000000u < 0x03000000u && (p & 3) == 0;
}

// The keeper's record of slot gets the reason a create failed.
static void fb_err(remote_swap_state* st, u32 slot, u8 err) {
    if (st && slot < MAX_REMOTE_LINKS) st->bodies[slot].err = err;
}

// --- The global snapshot (stage-4 plan section 3.5) ---------------------------
// Game globals daPy_lk_c writes as if the body were the one player: saved
// before every window, put back after (byte copies, any alignment). The
// JAIZel gear bytes hang off JAIZelBasic::zel_basic.
static const u32 l_fb_snap[][2] = {
    { DAPY_G_EVT_EXCEPTION, 0xC },
    { DAPY_G_PLAYER_STATUS, 8 },
    { DAPY_G_DIRECTION, 1 },
    { DAPY_G_METRONOME, 1 },
    { DAPY_G_LAST_MODE, 4 },
    { DAPY_G_KEEP_POS, 0xC },
    { DAPY_G_CUR_ANGLE, 6 },
    { DAPY_G_SHAPE_ANGLE, 6 },
    { DAPY_G_MATANM, 4 },
    { DAPY_G_DMECB_TIMER, 2 },
    { DAPY_G_DMECB_TYPE, 2 },
    { DAPY_G_PA_STATUS, 1 },
};
#define FB_SNAP_N      (sizeof(l_fb_snap) / sizeof(l_fb_snap[0]))
#define FB_SNAP_BYTES  59         // the sum of the lengths above

typedef struct {
    u8  b[FB_SNAP_BYTES];
    u8  gear[DAPY_ZEL_GEAR_LEN];
    u32 gear_addr;                // 0 = zel_basic was not in MEM1
} fb_snap;

static void fb_save(fb_snap* s) {
    u32 i, k, n = 0;
    u32 zel = *(volatile u32*)DAPY_G_ZEL_BASIC;
    for (i = 0; i < FB_SNAP_N; i++) {
        volatile const u8* p = (volatile const u8*)l_fb_snap[i][0];
        for (k = 0; k < l_fb_snap[i][1] && n < FB_SNAP_BYTES; k++) s->b[n++] = p[k];
    }
    s->gear_addr = fb_mem1(zel) ? zel + DAPY_ZEL_GEAR_OFF : 0;
    for (k = 0; k < DAPY_ZEL_GEAR_LEN && s->gear_addr; k++) s->gear[k] = ((volatile u8*)s->gear_addr)[k];
}

static void fb_restore(const fb_snap* s) {
    u32 i, k, n = 0;
    for (i = 0; i < FB_SNAP_N; i++) {
        volatile u8* p = (volatile u8*)l_fb_snap[i][0];
        for (k = 0; k < l_fb_snap[i][1] && n < FB_SNAP_BYTES; k++) p[k] = s->b[n++];
    }
    for (k = 0; k < DAPY_ZEL_GEAR_LEN && s->gear_addr; k++) ((volatile u8*)s->gear_addr)[k] = s->gear[k];
}

// The ten child solid heaps playerInit makes from the GameHeap and
// playerDelete destroys without a NULL check (DPM:12214-12250 / 11755-11766;
// the DIS addresses in dapy.h).
static const u16 l_fb_child_heaps[] = {
    DAPY_ANM_UNDER_HEAP, DAPY_ANM_UNDER_HEAP + 0x10,
    DAPY_ANM_UPPER_HEAP, DAPY_ANM_UPPER_HEAP + 0x10, DAPY_ANM_UPPER_HEAP + 0x20,
    DAPY_TEX_ANM_HEAP, DAPY_TEX_SCROLL_HEAP,
    DAPY_ITEM_HEAP0, DAPY_ITEM_HEAP1, DAPY_ITEM_ANIME_HEAP,
};
#define FB_CHILD_HEAPS_N (sizeof(l_fb_child_heaps) / sizeof(l_fb_child_heaps[0]))

// The shared linktexS3TC ResTIMG header (0x20 bytes; playerInit and
// playerDelete rewrite it, d_a_player_main.cpp:12320-12338, 11771-11772).
static void fb_tex_copy(u32* dst, const u32* src) {
    int i;
    for (i = 0; i < DAPY_RESTIMG_WORDS; i++) dst[i] = src[i];
}

// --- create ---------------------------------------------------------------------
// FP_NEW (stage-4 plan section 3.4 phase_1).
static int fb_phase1(void* a, rl_full_tail* ft, remote_swap_state* st) {
    u32 params = AF(a, u32, AC_PARAMS);
    u32 slot = (params >> 8) & 0xFF;
    // Only the keeper's create, with the body tag (the backstop against a
    // stray creator of proc 0x27).
    if ((params & WWREMOTE_BODY_PARAMS_MASK) != WWREMOTE_BODY_PARAMS_TAG || slot >= MAX_REMOTE_LINKS)
        return cPhs_ERROR_e;
    ft->t.slot = (u8)slot;
    ft->t.gen = (u8)(params & 0xFF);
    ft->t.state = WWREMOTE_BODY_PARKED;
    ft->kind = WWREMOTE_KIND_FULL;
    if (!st || !st->valid || st->role != WWREMOTE_ROLE_HOST) {
        fb_err(st, slot, WWREMOTE_BERR_BLOCK);
        return cPhs_ERROR_e;
    }
    if (!shanm_ensure()) {
        fb_err(st, slot, WWREMOTE_BERR_SHANM_ALLOC);
        return cPhs_ERROR_e;
    }
    // A stage actor: the stage teardown deletes it (as the lite body).
    fopAcM_setStageLayer(a);
    // Event 0xFF (no start demo), mode 0, room 0, 0x40: skip
    // setRestartRoom (ww-online puppet.c:100-104; makeBgWait 0x8012725C).
    AF(a, u32, AC_PARAMS) = WWREMOTE_FULL_PARAMS;
    ft->phase = WWREMOTE_FP_PARAMS;
    return cPhs_INIT_e;
}

// current/home/old pos = the slot's position (the host Link's if it is not
// finite), angle the slot's.
static void fb_place(void* a, u32 link, u32 slot) {
    u32 off = RB_SLOT(slot);
    f32 p[3];
    int i;
    for (i = 0; i < 3; i++) p[i] = RB_F32(off + WWREMOTE_SL_POS_OFF + 4 * i);
    if (rl_f32_bad(p[0]) || rl_f32_bad(p[1]) || rl_f32_bad(p[2])) {
        for (i = 0; i < 3; i++) p[i] = AF(link, f32, AC_CUR_POS + 4 * i);
    }
    for (i = 0; i < 3; i++) {
        AF(a, f32, AC_CUR_POS + 4 * i) = p[i];
        AF(a, f32, DAPY_HOME_POS + 4 * i) = p[i];
        AF(a, f32, DAPY_OLD_POS + 4 * i) = p[i];
    }
    AF(a, s16, AC_CUR_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    AF(a, s16, DAPY_SHAPE_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
}

// FP_PARAMS (section 3.4 phase_2): the gate, the ctor, playerInit in the
// window, the heap-counter check, the mAcch fixups.
static int fb_phase2(void* a, rl_full_tail* ft, remote_swap_state* st) {
    u32 link = rl_host_link();
    u32 tex, g0, s0, g1, s1, diff, cur_heap, save_heap;
    u32 saved_tex[DAPY_RESTIMG_WORDS];
    fb_snap snap;
    u8* acch = (u8*)a + DAPY_ACCH;
    // Gate (retry): the host Link created, the keeper's block live, no
    // stage change queued, the shared ResTIMG pointer sane.
    if (!link || !(AF(link, u32, DAPY_CONDITION) & DAPY_COND_INIT)) return cPhs_INIT_e;
    if (!st || !st->valid || st->role != WWREMOTE_ROLE_HOST || NEXT_STAGE_ENABLE != 0) return cPhs_INIT_e;
    tex = AF(link, u32, WWREMOTE_FULL_LINKTEX_OFF);
    if (!fb_mem1(tex)) return cPhs_INIT_e;

    // The ctor (installs __vt__9daPy_lk_c); alone it allocates nothing, so
    // a failure here needs no playerDelete.
    if (!(AF(a, u32, DAPY_CONDITION) & DAPY_COND_INIT)) {
        daPy_lk_ct(a);
        AF(a, u32, DAPY_CONDITION) |= DAPY_COND_INIT;
        if (AF(a, u32, WWREMOTE_FULL_VPTR_OFF) != DAPY_VT_LK) {
            fb_err(st, ft->t.slot, WWREMOTE_BERR_FULL_VTABLE);
            return cPhs_ERROR_e;
        }
    }
    // After the ctor, before playerInit (its mAcch.Set reads old.pos).
    fb_place(a, link, ft->t.slot);

    fb_tex_copy(saved_tex, (const u32*)tex);
    g0 = *(volatile u32*)DAPY_G_GAME_ERRORS;
    s0 = *(volatile u32*)DAPY_G_SOLID_ERRORS;
    shanm_refresh();
    shanm_begin();
    fb_save(&snap);
    *(volatile u32*)DAPY_G_LAST_MODE = 0;
    // The current heap and createSolidHeapToCurrent's saved one: a child
    // heap that fails leaves the save slot unset, and the restore after it
    // would make NULL current (m_Do_ext.cpp:1058-1071, 1109-1115). Both are
    // put back whatever playerInit did (a no-op when it balanced them).
    cur_heap = *(volatile u32*)DAPY_G_CURRENT_HEAP;
    save_heap = *(volatile u32*)DAPY_G_SAVE_CUR_HEAP;
    daPy_lk_playerInit(a);
    *(volatile u32*)DAPY_G_CURRENT_HEAP = cur_heap;
    *(volatile u32*)DAPY_G_SAVE_CUR_HEAP = save_heap;
    fb_restore(&snap);
    diff = shanm_end();
    fb_tex_copy((u32*)tex, saved_tex);
    // From here on delete must undo playerInit.
    ft->phase = WWREMOTE_FP_INIT;
    ft->shanm_diff = diff;

    // A heap counter that rose (or no mpCLModel) means playerInit may have
    // left child heaps NULL: delete checks them before playerDelete. (On
    // GZLE01 a failed child heap already OSPanics in playerInit's own
    // mDoExt_restoreCurrentHeap assert, DIS 0x80011E34-0x80011E74; the Go
    // gate's game_largest rule is what keeps that from happening. This is
    // the defense for what does return.)
    g1 = *(volatile u32*)DAPY_G_GAME_ERRORS;
    s1 = *(volatile u32*)DAPY_G_SOLID_ERRORS;
    if (g1 != g0 || s1 != s0 || !fb_mem1(AF(a, u32, WWREMOTE_FULL_MODEL_OFF))) {
        ft->init_bad = 1;
        ft->heap_err = (((g1 - g0) & 0xFF) << 24) | (((s1 - s0) & 0xFF) << 16);
        fb_err(st, ft->t.slot, WWREMOTE_BERR_HEAP_ERRORS);
        return cPhs_ERROR_e;
    }

    // mAcch, as ww-online fixes it after playerInit (puppet.c:374-400): the
    // pass checks, our own pid, mbLinkThrough.
    AF(acch, u32, ACCH_POLY_PASS) = (u32)(acch + ACCH_POLY_CHK);
    AF(acch, u32, ACCH_GRP_PASS) = (u32)(acch + ACCH_GRP_CHK);
    AF(acch, u32, ACCH_ACTOR_PID) = AF(a, u32, AC_PID);
    AF(acch, u8, ACCH_LINK_THROUGH) = 1;
    // mDemo: no demo type, mode DEMO_UNK00 (puppet.c:402-408).
    AF(a, u16, DAPY_DEMO_TYPE) = 0;
    AF(a, u32, DAPY_DEMO_MODE) = 0;
    return cPhs_INIT_e;
}

// The two cl.bdl joints makeBgWait sets whose mMtxCalc the shanm table does
// not track: their addresses, or 0.
static u32* fb_joint_word(void* a, u32 jnt) {
    u32 model = AF(a, u32, WWREMOTE_FULL_MODEL_OFF);
    u32 md, joints, j;
    if (!fb_mem1(model)) return 0;
    md = AF(model, u32, J3D_MODEL_DATA);
    if (!fb_mem1(md) || AF(md, u32, 0) != DAPY_VT_MODELDATA || AF(md, u16, J3D_MD_JOINT_NUM) <= jnt) return 0;
    joints = AF(md, u32, J3D_MD_JOINTS);
    if (!fb_mem1(joints)) return 0;
    j = ((u32*)joints)[jnt];
    if (!fb_mem1(j)) return 0;
    return (u32*)(j + J3D_JOINT_MTX_CALC);
}

// FP_INIT (section 3.4 phase_3): makeBgWait in the window until it finds
// ground, then the body is made inert and complete.
static int fb_phase3(void* a, rl_full_tail* ft, remote_swap_state* st) {
    u32* ja = fb_joint_word(a, CL_JNT_UNTRACKED_A);
    u32* jb = fb_joint_word(a, CL_JNT_UNTRACKED_B);
    u32 va = 0, vb = 0, jdiff = 0, frame;
    u8* t;
    int r;
    fb_snap snap;
    shanm_begin();
    fb_save(&snap);
    *(volatile u32*)DAPY_G_LAST_MODE = 0;
    if (ja) va = *ja;
    if (jb) vb = *jb;
    r = daPy_lk_makeBgWait(a);
    if (ja && *ja != va) {
        *ja = va;
        jdiff++;
    }
    if (jb && *jb != vb) {
        *jb = vb;
        jdiff++;
    }
    fb_restore(&snap);
    ft->shanm_diff += shanm_end();
    t = shanm_table();
    if (t && jdiff) AF(t, u32, WWREMOTE_SA_JOINT_DIFF_OFF) = AF(t, u32, WWREMOTE_SA_JOINT_DIFF_OFF) + jdiff;
    // No ground under the spawn yet, or a TBOX floor (DPM:12405-12424).
    if (r == cPhs_INIT_e) return cPhs_INIT_e;

    // Inert: not a lock-on target, no reset flags, no cut At, standing in
    // WAIT, none of the damage / ship / crawl modes.
    AF(a, u32, DAPY_ATTN_FLAGS) = 0;
    AF(a, u32, WWREMOTE_FULL_RESET0_OFF) = 0;
    AF(a, u32, DAPY_NO_RESET0) &= ~(u32)DAPY_CUT_AT_FLG;
    AF(a, u32, WWREMOTE_FULL_CUR_PROC_OFF) = DAPY_PROC_WAIT;
    AF(a, u32, WWREMOTE_FULL_MODE_FLG_OFF) &= ~DAPY_MODE_CLEAR;
    // Co off (the cyl source's 0x73 includes Set; setCollision never sets
    // it again) unless FLAG2_FULL_CO.
    if (!(st && (st->flags2 & WWREMOTE_FLAG2_FULL_CO))) AF(FB_CYL(a), u32, CC_CO_SPRM) &= ~1u;
    if (t) AF(t, u32, WWREMOTE_SA_CREATE_DIFF_OFF) = ft->shanm_diff;
    frame = FB_WORD(WWREL_FRAME_OFF);
    ft->t.state = WWREMOTE_BODY_PARKED;
    ft->t.last_pose_seq = 0xFFFFFFFFu;
    ft->t.pose_seq_frame = frame;
    ft->t.park_since = frame;
    ft->t.magic = WWREMOTE_BODY_MAGIC;
    ft->phase = WWREMOTE_FP_LIVE;
    return cPhs_COMPLEATE_e;
}

int fullbody_create(void* a) {
    rl_full_tail* ft = FB_TAIL(a);
    remote_swap_state* st = rl_state_checked();
    if (ft->create_frames != 0xFFFF) ft->create_frames++;
    if (ft->phase != WWREMOTE_FP_NEW && ft->create_frames > WWREMOTE_FULL_CREATE_FRAMES) {
        fb_err(st, ft->t.slot, WWREMOTE_BERR_PHASE_TIMEOUT);
        return cPhs_ERROR_e;
    }
    switch (ft->phase) {
    case WWREMOTE_FP_NEW:
        return fb_phase1(a, ft, st);
    case WWREMOTE_FP_PARAMS:
        return fb_phase2(a, ft, st);
    case WWREMOTE_FP_INIT:
        return fb_phase3(a, ft, st);
    default:
        return cPhs_ERROR_e;
    }
}

// --- delete -------------------------------------------------------------------
// Whether every child heap playerDelete destroys exists.
static int fb_child_heaps_ok(void* a) {
    u32 i;
    for (i = 0; i < FB_CHILD_HEAPS_N; i++) {
        if (!fb_mem1(AF(a, u32, l_fb_child_heaps[i]))) return 0;
    }
    return 1;
}

// After a playerInit that left a child heap NULL: what of playerDelete
// applies to a body that never ran a proc. The point light playerInit
// registered (DPM:12313) is cut, the child heaps that exist are destroyed;
// no emitter, sound or event was started, and the shared globals and the
// linktex header were restored after playerInit. The main solid heap is
// fopAc_Delete's.
static void fb_teardown_partial(void* a) {
    u32 i, h;
    dKy_plight_cut((u8*)a + DAPY_LIGHT_INFLUENCE);
    for (i = 0; i < FB_CHILD_HEAPS_N; i++) {
        h = AF(a, u32, l_fb_child_heaps[i]);
        if (fb_mem1(h)) mDoExt_destroySolidHeap((void*)h);
        AF(a, u32, l_fb_child_heaps[i]) = 0;
    }
}

int fullbody_delete(void* a) {
    rl_full_tail* ft = FB_TAIL(a);
    remote_swap_state* st = rl_state_checked();
    u32 heap, lo2 = 0, hi2 = 0, link, tex = 0, diff;
    u32 saved_tex[DAPY_RESTIMG_WORDS];
    fb_snap snap;
    u8* t;
    // The keeper's record and the cached m714s first, as the lite body.
    if (ft->t.magic == WWREMOTE_BODY_MAGIC ||
        (st && ft->t.slot < MAX_REMOTE_LINKS && st->bodies[ft->t.slot].actor == (u32)a))
        rl_body_gone(st, a, ft->t.slot);
    if (ft->phase < WWREMOTE_FP_INIT) {
        // Before playerInit nothing was allocated; the framework frees us.
        ft->t.magic = 0;
        return 1;
    }
    if (ft->init_bad && !fb_child_heaps_ok(a)) {
        fb_teardown_partial(a);
        ft->t.magic = 0;
        ft->phase = WWREMOTE_FP_NEW;
        return 1;
    }
    // playerDelete would write *mpCurrLinktex = mOtherLinktex for CASUAL
    // (DPM:11771-11772): the shared header, the local Link's clothes.
    AF(a, u32, WWREMOTE_FULL_NO_RESET1_OFF) &= ~(u32)DAPY_CASUAL;
    // The solid heap fopAc_Delete frees after us, for the scan.
    heap = AF(a, u32, DAPY_HEAP);
    if (fb_mem1(heap)) {
        lo2 = AF(heap, u32, 0x30);    // JKRHeap mStart (JKRHeap.h:111)
        hi2 = AF(heap, u32, 0x34);    // mEnd
    }
    link = *(volatile u32*)DAPY_G_PLAYER_PTR0;
    if (fb_mem1(link) && AF(link, s16, AC_PROC_NAME) == PROC_PLAYER) tex = AF(link, u32, WWREMOTE_FULL_LINKTEX_OFF);
    if (!fb_mem1(tex)) tex = 0;
    if (tex) fb_tex_copy(saved_tex, (const u32*)tex);
    shanm_refresh();
    shanm_begin();
    fb_save(&snap);
    daPy_lk_playerDelete(a);
    fb_restore(&snap);
    diff = shanm_end();
    if (tex) fb_tex_copy((u32*)tex, saved_tex);
    t = shanm_table();
    if (t) {
        AF(t, u32, WWREMOTE_SA_DELETE_DIFF_OFF) = diff;
        AF(t, u32, WWREMOTE_SA_DELETE_SCAN_OFF) = shanm_scan((u32)a, (u32)a + WWREMOTE_FULL_SIZE, lo2, hi2);
    }
    ft->t.magic = 0;
    ft->phase = WWREMOTE_FP_NEW;
    return 1;
}

// --- draw ---------------------------------------------------------------------
int fullbody_draw(void* a) {
    (void)a;
    return 1;
}

// --- execute (stage-4 plan section 3.7) ------------------------------------------
// The pose feed's 42 world-space cl.bdl node matrices into mpCLModel's
// mpNodeMtx (the matrices the mini-Link copies, minilink.c), so
// setCollision's root/neck/toe reads (DPM:9727-9772) see the remote's pose.
static int fb_pose(void* a, u32 ps) {
    u32 src = ps >= MAILBOX_POSE_SLOT_CAP ? 0 : *(volatile u32*)(MAILBOX_ADDR + __builtin_offsetof(Mailbox, pose_buf_ptrs) + 4 * ps);
    u16 n = *(volatile u16*)(MAILBOX_ADDR + __builtin_offsetof(Mailbox, pose_joint_counts) + 2 * ps);
    u8 seq = *(volatile u8*)(MAILBOX_ADDR + __builtin_offsetof(Mailbox, pose_seqs) + ps);
    u32 model = AF(a, u32, WWREMOTE_FULL_MODEL_OFF);
    u32 dst, i;
    if (!fb_mem1(src) || n != CL_JOINTS || seq == 0 || !fb_mem1(model)) return 0;
    dst = AF(model, u32, J3D_MODEL_NODE_MTX);
    if (!fb_mem1(dst)) return 0;
    for (i = 0; i < CL_JOINTS * 12; i++) ((u32*)dst)[i] = ((volatile u32*)src)[i];
    // Base TR = T(pos) Ry(shape.y): cullMtx points here (DPM:12095).
    {
        f32 (*m)[4] = (f32 (*)[4])(model + J3D_MODEL_BASE_TR);
        mDoMtx_YrotS(m, AF(a, s16, DAPY_SHAPE_ANGLE_Y));
        m[0][3] = AF(a, f32, AC_CUR_POS + 0);
        m[1][3] = AF(a, f32, AC_CUR_POS + 4);
        m[2][3] = AF(a, f32, AC_CUR_POS + 8);
    }
    return 1;
}

// setCollision with the shared Deku Leaf anms (mpYuchw00Brk, mpYbafo00Btk:
// archive resources the local Link uses too) held at their last frame, so
// its fan block reads "not drawing" and plays nothing (DPM:10077-10160);
// both frames are put back after.
static void fb_set_collision(void* a) {
    u32 brk = AF(a, u32, DAPY_YUCHW00_BRK);
    u32 btk = AF(a, u32, DAPY_YBAFO00_BTK);
    f32 fb = 0.0f, ft = 0.0f;
    int okb = fb_mem1(brk), okt = fb_mem1(btk);
    if (okb) {
        fb = AF(brk, f32, DAPY_ANM_FRAME);
        AF(brk, f32, DAPY_ANM_FRAME) = (f32)AF(brk, s16, DAPY_ANM_FRAME_MAX) - 0.001f;
    }
    if (okt) {
        ft = AF(btk, f32, DAPY_ANM_FRAME);
        AF(btk, f32, DAPY_ANM_FRAME) = (f32)AF(btk, s16, DAPY_ANM_FRAME_MAX) - 0.001f;
    }
    daPy_lk_setCollision(a);
    if (okb) AF(brk, f32, DAPY_ANM_FRAME) = fb;
    if (okt) AF(btk, f32, DAPY_ANM_FRAME) = ft;
}

int fullbody_execute(void* a) {
    rl_full_tail* ft = FB_TAIL(a);
    remote_swap_state* st = rl_state_checked();
    u32 frame = FB_WORD(WWREL_FRAME_OFF);
    u8* cyl = FB_CYL(a);
    u32 off, link;
    u8 sf;
    s16 dw;
    int park, i, guard;
    f32 p[3];

    if (ft->t.magic != WWREMOTE_BODY_MAGIC || ft->phase != WWREMOTE_FP_LIVE) return 1;
    park = rl_body_should_park(st, a, &ft->t, frame);

    // The hit read, before anything moves (the record is last Move's).
    if (st) {
        if (st->flags & WWREMOTE_FLAG_DETECT) rl_body_hits(st, a, &ft->t, cyl, frame);
        else if (AF(cyl, u32, CC_TG_RPRM) & 1) dCcD_GObjInf_ClrTgHit(cyl);
    }

    if (park) {
        // Parked: no pose, no setCollision (so mCyl is never Set), out of
        // the latch, standing on the host Link.
        link = *(volatile u32*)DAPY_G_PLAYER_PTR0;
        if (ft->t.state != WWREMOTE_BODY_PARKED) rl_body_park_enter(st, a, &ft->t, frame);
        if (fb_mem1(link)) {
            for (i = 0; i < 3; i++) {
                AF(a, f32, AC_CUR_POS + 4 * i) = AF(link, f32, AC_CUR_POS + 4 * i);
                AF(a, f32, AC_EYE_POS + 4 * i) = AF(link, f32, AC_EYE_POS + 4 * i);
            }
        }
        AF(a, f32, AC_SPEEDF) = 0.0f;
        return 1;
    }
    if (ft->t.state != WWREMOTE_BODY_LIVE) {
        ft->t.state = WWREMOTE_BODY_LIVE;
        ft->t.park_since = 0;
    }
    st->bodies[ft->t.slot].parked = 0;
    off = RB_SLOT(ft->t.slot);
    sf = RB_U8(off + WWREMOTE_SL_SFLAGS_OFF);

    // Pose: old = current, the slot's (smoothed) position, angles and room,
    // speedF from the XZ step, eyePos = pos + eye_dy. Everything finite
    // (fopAc_Execute asserts it, f_op_actor.cpp:169-172, 206-209).
    for (i = 0; i < 5; i++) AF(a, u32, AC_OLD + 4 * i) = AF(a, u32, AC_CURRENT + 4 * i);
    for (i = 0; i < 3; i++) p[i] = RB_F32(off + WWREMOTE_SL_POS_OFF + 4 * i);
    if (!rl_f32_bad(p[0]) && !rl_f32_bad(p[1]) && !rl_f32_bad(p[2])) {
        f32 dx = p[0] - AF(a, f32, AC_CUR_POS + 0);
        f32 dz = p[2] - AF(a, f32, AC_CUR_POS + 8);
        for (i = 0; i < 3; i++) AF(a, f32, AC_CUR_POS + 4 * i) = p[i];
        AF(a, f32, AC_SPEEDF) = rl_sqrt(dx * dx + dz * dz);
    }
    AF(a, s16, AC_CUR_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    AF(a, s16, DAPY_SHAPE_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    AF(a, s8, AC_CUR_ROOM) = RB_S8(off + WWREMOTE_SL_ROOM_OFF);
    {
        f32 eye = RB_F32(off + WWREMOTE_SL_EYE_DY_OFF);
        if (rl_f32_bad(eye) || eye < -500.0f || eye > 500.0f) eye = (f32)WWREMOTE_DEF_EYE_DY;
        AF(a, f32, AC_EYE_POS + 0) = AF(a, f32, AC_CUR_POS + 0);
        AF(a, f32, AC_EYE_POS + 4) = AF(a, f32, AC_CUR_POS + 4) + eye;
        AF(a, f32, AC_EYE_POS + 8) = AF(a, f32, AC_CUR_POS + 8);
    }

    // Combat bytes the Moblin reads (d_cc_uty.cpp:16-34,88-131); mCurProc
    // answers the real checkPlayerGuard (DPM:1037-1040).
    guard = (st->flags & WWREMOTE_FLAG_GUARD) && (sf & WWREMOTE_SF_GUARD);
    AF(a, u8, PY_CUT_TYPE) = RB_U8(off + WWREMOTE_SL_CUT_TYPE_OFF);
    AF(a, u8, PY_CUT_COUNT) = RB_U8(off + WWREMOTE_SL_CUT_COUNT_OFF);
    AF(a, u32, WWREMOTE_FULL_CUR_PROC_OFF) = guard ? DAPY_PROC_CROUCH_DEF : DAPY_PROC_WAIT;
    // i-frames: the client's mDamageWaitTimer or our stamp, whichever is
    // longer; setCollision turns the Tg off while it is nonzero (DPM:9775-9781).
    dw = RB_S16(off + WWREMOTE_SL_IFRAMES_OFF);
    if ((s16)ft->t.iframes_left > dw) dw = (s16)ft->t.iframes_left;
    if (dw < 0) dw = 0;
    AF(a, s16, WWREMOTE_FULL_DAMAGE_WAIT_OFF) = dw;
    if (ft->t.iframes_left) ft->t.iframes_left--;
    AF(a, u32, WWREMOTE_FULL_RESET0_OFF) = 0;                 // no sword At branch (DPM:9796+)
    AF(a, u32, WWREMOTE_FULL_MODE_FLG_OFF) &= ~DAPY_MODE_CLEAR;
    AF(a, f32, DAPY_GRAB_WEAR) = 0.0f;
    AF(a, u32, DAPY_ATTN_FLAGS) = 0;

    // Co: off unless FLAG2_FULL_CO (idempotent).
    if (st->flags2 & WWREMOTE_FLAG2_FULL_CO) AF(cyl, u32, CC_CO_SPRM) |= 1u;
    else AF(cyl, u32, CC_CO_SPRM) &= ~1u;

    // Guard (this frame's Move): the GObjTg shield bits and the angle the
    // cyl's shield pointer reads (+0x3546); neither the proc path nor
    // daPy execute runs on the body.
    ft->t.shield_angle = RB_S16(off + WWREMOTE_SL_SHIELD_ANGLE_OFF);
    if (guard) {
        AF(cyl, u32, CC_GTG) |= CC_GTG_GUARD_BITS;
        AF(a, s16, WWREMOTE_FULL_SHIELD_RANGE_OFF) = ft->t.shield_angle;
    } else {
        AF(cyl, u32, CC_GTG) &= ~(u32)CC_GTG_GUARD_BITS;
    }

    // Node matrices, then the Set: only with a pose this frame and HITTABLE.
    if (fb_pose(a, RB_U8(off + WWREMOTE_SL_POSE_SLOT_OFF)) && (st->flags & WWREMOTE_FLAG_HITTABLE))
        fb_set_collision(a);
    return 1;
}
