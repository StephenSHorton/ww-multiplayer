// bosskill.c: the boss_kill relay op (bosskill.h, include/wwboss.h).
//
// Co-op (mods/coop/replica bosskill.go) asks for it when the partner's
// world has DRC's STAGE_BOSS_ENEMY and this game, still in M_DragB, lacks
// it: the replica holds the bit there (bossHold), because Gohma's arena
// polls it every frame. Instead of writing the bit, the op gives this
// game's own Btd the death its lethal eye hit gives it: damage_check
// writes m02E4 = 0xB and mGohmaState = 0x32 and returns (d_a_btd.cpp:
// 463-475), and daBtd_Execute's dispatch then runs end() (:1762-1764),
// whose case 0x32 starts the death demo (:905-915). That demo drops the
// floor through Dr2 (:1092-1094), spawns the heart container (:1082),
// sets STAGE_BOSS_ENEMY and creates the warp flower (:2186-2190), all in
// this game and in vanilla order. health is not written: nothing on the
// end() path reads it (it is read only by damage_check, :463-464).
//
// One request per new seq; the four branches of the design:
//   1. no Btd (fopAcM_SearchByName gives NULL, also while it is still
//      creating, f_op_actor_mng.cpp:65-74): WWBOSS_R_NO_ACTOR;
//   2. m02E4 0xA, the intro or Gohma still hidden at y -10000 (set at
//      create, :3003; startdemo, :1758-1760): WWBOSS_R_WAITING, nothing
//      written, so the receiver's intro plays out and no death demo runs on
//      a hidden Btd; Go asks again;
//   3. m02E4 0, 1 or 2, the fight, with the armor broken (m02E0 1): write
//      both fields, WWBOSS_R_APPLIED;
//   4. m02E4 0xB, already dying or dead: WWBOSS_R_ALREADY.
// Any other m02E4 is reported as waiting with the value seen.
//
// The armor gate (branch 3). Vanilla reaches end() only with m02E0 1: the
// lethal branch sits inside the eye hit that needs it (:457), and with
// m02E0 0 an eye hit only sets mActionState 10 (:452-455). get_anm, get_btk
// and get_brk pick the phase by m02E0 (:114-135), so a death forced with
// the armor intact plays BCK_DEAD on the armored BTD morf and re-inits the
// SOTAI-built brkS/btkS against BTD's model data (:911-930); at m02EC 0x1cc
// m02E0 goes 2 (:980) and the draw switches to SOTAI with those bindings.
// So m02E4 0, 1 or 2 with m02E0 not 1 is WWBOSS_R_WAITING: the receiver
// breaks its own armor (m02E0 = 1 at the end of the break, :833), then the
// next request applies. The same gate closes Dr2's rock: the break (state
// 0x29) parks Dr2 at unk_4BA 10 at m02EC 0x26 (:787), before its anim ends
// and sets m02E0 (:833), and Dr2's cases 10 and 11 never go back below 10
// (d_a_dr2.cpp:378-398), so Dr2's case 1, the only writer of
// m02F6 = 1 (d_a_dr2.cpp:304-311), cannot run once m02E0 is 1. m02F6 itself
// stays 2 through phase 2 (the m6190 >= 3 branch goes 0x16 -> 0x28, :705-706,
// past the only reset at :732), so only m02F6 1, a landed rock damage_check
// has not taken yet (:445-451), is also answered waiting, as a guard.
//
// Gates, before any search: no stage change pending (NEXT_STAGE_ENABLE),
// the proc WWBOSS_PROC_BTD and the tag WWBOSS_TAG, and the stage exactly
// "M_DragB". The refight stages are the 'X' ones (Xboss0..3), where end()
// takes the refight branch instead (d_a_btd.cpp:905-910); the exact name
// keeps them out, and so does the explicit first-letter test below.
//
// Kalle Demos (WWBOSS_PROC_BMD, the stage exactly "kinBOSS"; ww-multiplayer-
// handoff/design/research/C1-kalle-demos-relay.md). bk_bmd writes all of
// core_damage_check's lethal block (d_a_bmd.cpp:609-617), not just mMode
// and m302: mB76, the clock end() waits on for STAGE_BOSS_ENEMY and the
// warp flower (:561-565), counts only while mB74 is nonzero (:1523-1538),
// and the death camera's case 101 holds Link in place every frame
// (:1462-1470) until the head death (m306 100) reaches 110 (:760-764). So
// m306 = 100, mB74 = 100, m310 = 20000, mBE0 = 2, mBDC = 1.0f, m302 = 0,
// mMode = 0xB, then the boss music stopped as mDoAud_bgmStop(30) does.
// health, the 8-frame hit pause and the last-hit SE are skipped. end(),
// the head death and the death camera then give the heart container
// (:747), Makar's drop and NPC_CB1 (:1439, :1488), STAGE_BOSS_ENEMY and
// the warp flower (:562-563) in this game, in vanilla order.
//
// Bmd's branches:
//   1. no Bmd (none, still creating, or its create refused because this
//      game has the boss bit, :1970-1977): WWBOSS_R_NO_ACTOR;
//   2. any state but the open flower: WWBOSS_R_WAITING, nothing written;
//   3. mMode 3 (fallen) at m302 2..5 with m304 nonzero (the core out),
//      m310 0 and mB74 0: the lethal block, WWBOSS_R_APPLIED;
//   4. mMode 0xB: WWBOSS_R_ALREADY.
// The gate (branch 3) is vanilla's own reach: the intro shuts
// core_damage_check (m310 = 8 every frame, :427), and with the vines up
// the body sphere shields the core and m304 is 0 (:942, :1625-1629). It
// is also where the hands and feet are down: end() lays them down only
// through the one-frame m332 8 and 9 signals, which a foot takes only in
// its damage state (d_a_bmdfoot.cpp:567) and a hand only in its down state
// (d_a_bmdhand.cpp:558), both entered on the m332 = 3 fall signal (:160),
// always before m304 = 1 (:164). Killed with the vines up, the hands and
// feet would attack on through the death scene, and move() would keep the
// body hung by m331 (:1087-1095). m304 alone is not enough: it is
// nonzero in eat (:327), the intro (:433) and end() (:517), and wait()
// clears it only 60 frames after the flower closes (:938-943), while
// damage_check runs in every mode but 3 (:1096): vines cut inside that
// window enter mMode 3 at m302 0 with the old m304, before the m332 = 3
// fall signal (:158-161). So m302 must be 2..5 too. Never a
// delete: the hands and feet keep a static pointer to the boss
// (d_a_bmdhand.cpp:34, d_a_bmdfoot.cpp:35).
//
// Gohdan (WWBOSS_PROC_BST, the stage exactly "SirenB"; enemy-mirror-
// batch-d.json R-D1..R-D3, D2, D3, "gohdan"), under WWBOSS_WITH_BST.
// SirenB places three proc 0xF2 actors, the head (mBstPartType 0) and two
// hands (1, 2), and d_a_bst.rel keeps them in .bss statics (boss, hand[],
// d_a_bst.cpp:44-45) the mod cannot read, so bk_bst finds each part with
// fopAcIt_Judge (f_op_actor_iter.cpp:18-23) and bk_bst_judge, the first
// function of this module a DOL iterator calls back (the profile method
// tables are the same mechanism, rel_main.c; build.py checks it lies in
// .text). The judge is f_op_actor_mng.cpp:65-74's rule for a part: proc
// 0xF2, the part byte, mInitState 2 and not fpcM_IsCreating (an actor
// joins the tag queue on its first create step, f_op_actor.cpp:261-263).
// Its branches:
//   1. any part missing, or the head's morf NULL: WWBOSS_R_NO_ACTOR;
//   2. the head END_DEMO, m2E9A >= 0x32 or m2E7C 100 (the demo's end,
//      :2534): WWBOSS_R_ALREADY;
//   3. vanilla's own pre-death state, all of: head HEAD_DAMAGE with
//      mDamage 3 (KUTI_OPEN_LOOP, the mouth open for a bomb, :1159-1166;
//      2 is the KUTI_OPEN one-shot, D2), m2E7C 12 (the eyes open, both
//      hands asleep, which case 12 holds every frame, :1888-1905), m2E9A
//      0, both hands SLEEP: bom_eat_check's lethal block (:1219-1229),
//      WWBOSS_R_APPLIED;
//   4. anything else (the intro, the hands fighting, the eye phase, the
//      beam, the fall, a bomb in the mouth, HUKKI): WWBOSS_R_WAITING,
//      nothing written. Only damage_check's double eye kill enters
//      HEAD_DAMAGE (:1468-1472), so the receiver beats both hands and both
//      eyes itself first, as Gohma's armor and Kalle Demos's vines.
// The lethal block, in one keeper frame and this order (R-D3): head m2E9A
// 0x32, head mDamage 0, the DEAD anim (WWBOSS_BST_ANIM: anm_init's
// getRes + setAnm, :111-120, :1222), each hand END_DEMO with mDamage 0xA
// (main_cont stops forcing hand[0] asleep once m2E9A != 0, :1762, :1903,
// and sleep() wakes a hand when its timer runs out, :916-920, so hands
// left asleep would attack through the demo), the head END_DEMO last, then
// mDoAud_bgmStop(30) and mDoAud_bgmStreamPrepare(JA_STRM_BST_CLEAR), which
// demo case 51 plays (:2396). end_demo (:1604) and its camera cases 50-57
// then place Link, set STAGE_BOSS_ENEMY, create the warp flower
// (:2429-2436) and drop the heart (:2504-2510) in this game. Skipped:
// health-- (read only by beam_attack :1015-1021, damage :1062,
// damage_check's SE :1502 and main_cont :1837-1882, none of which drives
// END_DEMO), the bomb delete (no bomb at mDamage 3), the LAST_HIT SE, the
// particles and m10FC[3]. Never a delete. The result carries the head's
// mActionType, mDamage and m2E7C as found.
#include "mailbox_core.h"
#include "wwenemy.h"
#include "wwremote.h"
#include "bosskill.h"

#define BK_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + WWBOSS_OFF + (off)))
#define BK_HALF(off) (*(volatile u16*)(MAILBOX_ADDR + WWBOSS_OFF + (off)))

// The request lies in the gap between the enemy region and the remote-Link
// block (wwboss.h), and both words are aligned.
typedef char bk_check_place[(WWBOSS_OFF >= WWENEMY_REGION_END &&
                             WWBOSS_OFF + WWBOSS_SIZE <= WWREMOTE_OFF &&
                             WWBOSS_OFF >= WWREMOTE_REGION_START &&
                             (WWBOSS_OFF & 3) == 0 &&
                             WWBOSS_ACK_OFF == WWBOSS_GO_SPAN &&
                             WWBOSS_RESULT_OFF + 4 == WWBOSS_SIZE) ? 1 : -1];

// dComIfGp mCurStage.mName (char[8]), enemy_mirror.c EM_CUR_STAGE_NAME:
// what dComIfGp_getStartStageName returns (d_com_inf_game.h:283), the name
// Btd's own refight test reads.
#define BK_CUR_STAGE_NAME ((volatile const u8*)0x803C9D3C)

// fopAcM_SearchByName__FsPP10fopAc_ac_c (symbols.txt:639,
// f_op_actor_mng.cpp:65-74): TRUE with *out set for a live actor of the
// proc, TRUE with *out NULL for one still creating, FALSE for none.
typedef BOOL (*bk_search_name_t)(s16 proc, fopAc_ac_c** out);
#define bk_SearchByName ((bk_search_name_t)0x80024230)

// fpc base's mProcName (s16 at +0x08, f_pc_base.h), as enemy_mirror.c checks it.
#define BK_PROC_NAME_OFF 0x08

// mDoAud_bgmStop (m_Do_audio.h:107-109): zel_basic->bgmStop(fade, 0).
typedef void (*bk_bgm_stop_t)(void* self, u32 fade, s32 arg);
#define bk_bgmStop ((bk_bgm_stop_t)WWBOSS_BGM_STOP_FN)
#define BK_ZEL_BASIC (*(void* volatile*)WWBOSS_ZEL_BASIC)

// The stage proc's op may run in, NUL-padded to the name's 8 bytes: Btd
// only in M_DragB, Bmd only in kinBOSS, Bst only in SirenB, never an 'X'
// refight stage.
static int bk_stage_ok(u16 proc) {
    static const char drag_b[8] = { 'M', '_', 'D', 'r', 'a', 'g', 'B', 0 };
    static const char kin_boss[8] = { 'k', 'i', 'n', 'B', 'O', 'S', 'S', 0 };
#if WWBOSS_WITH_BST
    static const char siren_b[8] = { 'S', 'i', 'r', 'e', 'n', 'B', 0, 0 };
#endif
    const char* want;
    int i;
    if (proc == WWBOSS_PROC_BTD) {
        want = drag_b;
    } else if (proc == WWBOSS_PROC_BMD) {
        want = kin_boss;
#if WWBOSS_WITH_BST
    } else if (proc == WWBOSS_PROC_BST) {
        want = siren_b;
#endif
    } else {
        return 0;
    }
    if (BK_CUR_STAGE_NAME[0] == 'X') return 0;
    for (i = 0; i < 8; i++) {
        if (BK_CUR_STAGE_NAME[i] != (u8)want[i]) return 0;
        if (want[i] == 0) break;
    }
    return 1;
}

static void bk_ack(u32 seq, u32 result, s16 act, s16 state, u8 phase) {
    BK_WORD(WWBOSS_RESULT_OFF) = (result << 24) | ((u32)(u8)act << 16) | ((u32)(u8)state << 8) | phase;
    BK_WORD(WWBOSS_ACK_OFF) = seq;
}

void bosskill_link(void) {
    bk_ack(BK_WORD(WWBOSS_SEQ_OFF), WWBOSS_R_NONE, 0, 0, 0);
}

// bk_btd runs a request on Gohma (the header's branches 2-4).
static void bk_btd(u32 seq, fopAc_ac_c* a) {
    volatile s16* act = (volatile s16*)((u8*)a + WWBOSS_BTD_M02E4_OFF);
    volatile s16* state = (volatile s16*)((u8*)a + WWBOSS_BTD_STATE_OFF);
    s16 was = *act;
    s16 st = *state;
    u8 phase = *(volatile u8*)((u8*)a + WWBOSS_BTD_M02E0_OFF);

    switch (was) {
    case 0:
    case 1:
    case 2:
        // The armor gate (header): only with the armor broken, and no
        // landed rock pending.
        if (phase != WWBOSS_BTD_PHASE_BARE ||
            *(volatile u8*)((u8*)a + WWBOSS_BTD_M02F6_OFF) == WWBOSS_BTD_ROCK_LANDED) {
            bk_ack(seq, WWBOSS_R_WAITING, was, st, phase);
            return;
        }
        *act = WWBOSS_BTD_ACT_END;
        *state = WWBOSS_BTD_STATE_DEAD;
        bk_ack(seq, WWBOSS_R_APPLIED, was, st, phase);
        return;
    case WWBOSS_BTD_ACT_END:
        bk_ack(seq, WWBOSS_R_ALREADY, was, st, phase);
        return;
    default:
        bk_ack(seq, WWBOSS_R_WAITING, was, st, phase);
        return;
    }
}

#define BK_S16(a, off) (*(volatile s16*)((u8*)(a) + (off)))

// bk_bmd runs a request on Kalle Demos (the header's Bmd branches 2-4).
// The result carries mMode, m302 and m304 as found.
static void bk_bmd(u32 seq, fopAc_ac_c* a) {
    s16 mode = BK_S16(a, WWBOSS_BMD_MODE_OFF);
    s16 step = BK_S16(a, WWBOSS_BMD_M302_OFF);
    s16 core = BK_S16(a, WWBOSS_BMD_M304_OFF);
    void* zel;

    if (mode == WWBOSS_BMD_MODE_END) {
        bk_ack(seq, WWBOSS_R_ALREADY, mode, step, (u8)core);
        return;
    }
    // The open flower (header): fallen and landed, the core out, no hit
    // i-frames and no demo camera.
    if (mode != WWBOSS_BMD_MODE_FALLEN || step < WWBOSS_BMD_STEP_LANDED || core == 0 ||
        BK_S16(a, WWBOSS_BMD_M310_OFF) != 0 || BK_S16(a, WWBOSS_BMD_MB74_OFF) != 0) {
        bk_ack(seq, WWBOSS_R_WAITING, mode, step, (u8)core);
        return;
    }
    // core_damage_check's lethal block (d_a_bmd.cpp:609-617), mMode and
    // m302 last so end() starts on the full set (one frame on the main
    // thread either way).
    BK_S16(a, WWBOSS_BMD_M306_OFF) = WWBOSS_BMD_M306_DEAD;
    BK_S16(a, WWBOSS_BMD_MB74_OFF) = WWBOSS_BMD_MB74_DEATH;
    BK_S16(a, WWBOSS_BMD_M310_OFF) = WWBOSS_BMD_M310_DEAD;
    *(volatile u8*)((u8*)a + WWBOSS_BMD_MBE0_OFF) = WWBOSS_BMD_MBE0_DEAD;
    *(volatile f32*)((u8*)a + WWBOSS_BMD_MBDC_OFF) = 1.0f;
    BK_S16(a, WWBOSS_BMD_M302_OFF) = 0;
    BK_S16(a, WWBOSS_BMD_MODE_OFF) = WWBOSS_BMD_MODE_END;
    zel = BK_ZEL_BASIC;
    if (zel) bk_bgmStop(zel, WWBOSS_BGM_STOP_FADE, 0);
    bk_ack(seq, WWBOSS_R_APPLIED, mode, step, (u8)core);
}

#if WWBOSS_WITH_BST
// fopAcIt_Judge__FPFPvPv_PvPv (symbols.txt:632, f_op_actor_iter.cpp:18-23):
// judge(actor, data) over the actor tag queue, the first non-NULL return.
typedef void* (*bk_judge_t)(void* p, void* want);
typedef void* (*bk_iter_judge_t)(bk_judge_t judge, void* data);
#define bk_IterJudge ((bk_iter_judge_t)WWBOSS_ITER_JUDGE_FN)
// dRes_control_c::getRes(const char*, s32, dRes_info_c*, int), static
// (d_resorce.h:62): dComIfG_getObjectRes(arc, index)'s body
// (d_com_inf_game.h:4112-4114, d_resorce.h:76-79).
typedef void* (*bk_get_res_t)(const char* arc, s32 index, void* info, int count);
#define bk_getRes ((bk_get_res_t)WWBOSS_GETRES_FN)
// mDoExt_McaMorf::setAnm(J3DAnmTransform*, int loopMode, f32 morf, f32
// speed, f32 start, f32 end, void* bas) (symbols.txt:318; the ints and
// pointers in r3..r6, the floats in f1..f4, as d_a_bst.rel's anm_init
// passes them, .text 0x2C0-0x3E4).
typedef void (*bk_set_anm_t)(void* morf, void* anm, int loop, f32 morf_f, f32 speed, f32 start, f32 end, void* bas);
#define bk_setAnm ((bk_set_anm_t)WWBOSS_SETANM_FN)
// JAIZelBasic::bgmStreamPrepare(u32) (symbols.txt:10632), as
// mDoAud_bgmStreamPrepare calls it (m_Do_audio.h:99-101).
typedef void (*bk_strm_prepare_t)(void* self, u32 id);
#define bk_bgmStreamPrepare ((bk_strm_prepare_t)WWBOSS_STRM_PREPARE_FN)

// fpc base's mBsPcId (u32 +0x04) and mInitState (s8 +0x0C, 2 = executing,
// f_pc_executor.cpp:49), as enemy_mirror.c reads them.
#define BK_PROC_ID_OFF    0x04
#define BK_INIT_STATE_OFF 0x0C
#define BK_S8(a, off)     (*(volatile s8*)((u8*)(a) + (off)))

// The judge DOL code calls back (header): the Bst actor of part *want,
// once created, else NULL (keep iterating). Static and in .text: build.py
// checks the symbol's section after the link.
static void* bk_bst_judge(void* p, void* want) {
    if (*(volatile s16*)((u8*)p + BK_PROC_NAME_OFF) != (s16)WWBOSS_PROC_BST) return 0;
    if (*(volatile u8*)((u8*)p + WWBOSS_BST_PART_OFF) != *(const u8*)want) return 0;
    if (BK_S8(p, BK_INIT_STATE_OFF) != 2) return 0;
    if (fpcM_IsCreating(*(volatile u32*)((u8*)p + BK_PROC_ID_OFF))) return 0;
    return p;
}

static void* bk_bst_part(u8 part) {
    u8 want = part;
    return bk_IterJudge(bk_bst_judge, &want);
}

// bk_bst runs a request on Gohdan (the header's Bst branches 1-4). The
// result carries the head's mActionType, mDamage and m2E7C as found.
static void bk_bst(u32 seq) {
    void* head = bk_bst_part(WWBOSS_BST_PART_HEAD);
    void* hand1 = bk_bst_part(WWBOSS_BST_PART_HAND1);
    void* hand2 = bk_bst_part(WWBOSS_BST_PART_HAND2);
    void* morf;
    void* zel;
    s16 act, dmg;
    s8 phase, death;

    if (!head || !hand1 || !hand2) {
        bk_ack(seq, WWBOSS_R_NO_ACTOR, 0, 0, 0);
        return;
    }
    morf = *(void* volatile*)((u8*)head + WWBOSS_BST_MORF_OFF);
    act = BK_S16(head, WWBOSS_BST_ACT_OFF);
    dmg = BK_S16(head, WWBOSS_BST_DMG_OFF);
    phase = BK_S8(head, WWBOSS_BST_M2E7C_OFF);
    death = BK_S8(head, WWBOSS_BST_M2E9A_OFF);
    if (!morf) {
        bk_ack(seq, WWBOSS_R_NO_ACTOR, act, dmg, (u8)phase);
        return;
    }
    if (act == WWBOSS_BST_ACT_END_DEMO || death >= WWBOSS_BST_M2E9A_DEATH || phase == WWBOSS_BST_M2E7C_DONE) {
        bk_ack(seq, WWBOSS_R_ALREADY, act, dmg, (u8)phase);
        return;
    }
    if (act != WWBOSS_BST_ACT_HEAD_DMG || dmg != WWBOSS_BST_DMG_MOUTH || phase != WWBOSS_BST_M2E7C_EYES ||
        death != 0 || BK_S16(hand1, WWBOSS_BST_ACT_OFF) != WWBOSS_BST_ACT_SLEEP ||
        BK_S16(hand2, WWBOSS_BST_ACT_OFF) != WWBOSS_BST_ACT_SLEEP) {
        bk_ack(seq, WWBOSS_R_WAITING, act, dmg, (u8)phase);
        return;
    }
    // bom_eat_check's lethal block (d_a_bst.cpp:1219-1229), R-D3's order.
    BK_S8(head, WWBOSS_BST_M2E9A_OFF) = WWBOSS_BST_M2E9A_DEATH;
    BK_S16(head, WWBOSS_BST_DMG_OFF) = 0;
#if WWBOSS_BST_ANIM
    {
        // anm_init(i_this, BST_DEAD, 1.0f, EMode_NONE, 1.0f, -1)
        // (:111-120, :1222): no sound bas.
        static const char arc[4] = { 'B', 's', 't', 0 };
        void* res = bk_getRes(arc, WWBOSS_BST_ANM_DEAD, (void*)WWBOSS_OBJINFO, WWBOSS_OBJINFO_COUNT);
        if (res) bk_setAnm(morf, res, 0, 1.0f, 1.0f, 0.0f, -1.0f, 0);
    }
#endif
    BK_S16(hand1, WWBOSS_BST_ACT_OFF) = WWBOSS_BST_ACT_END_DEMO;
    BK_S16(hand1, WWBOSS_BST_DMG_OFF) = WWBOSS_BST_HAND_DMG_END;
    BK_S16(hand2, WWBOSS_BST_ACT_OFF) = WWBOSS_BST_ACT_END_DEMO;
    BK_S16(hand2, WWBOSS_BST_DMG_OFF) = WWBOSS_BST_HAND_DMG_END;
    BK_S16(head, WWBOSS_BST_ACT_OFF) = WWBOSS_BST_ACT_END_DEMO;
    zel = BK_ZEL_BASIC;
    if (zel) {
        bk_bgmStop(zel, WWBOSS_BGM_STOP_FADE, 0);
        bk_bgmStreamPrepare(zel, WWBOSS_STRM_BST_CLEAR);
    }
    bk_ack(seq, WWBOSS_R_APPLIED, act, dmg, (u8)phase);
}
#endif

// The procs bosskill_frame takes (each only in its stage, bk_stage_ok).
static int bk_proc_ok(u16 proc) {
#if WWBOSS_WITH_BST
    if (proc == WWBOSS_PROC_BST) return 1;
#endif
    return proc == WWBOSS_PROC_BTD || proc == WWBOSS_PROC_BMD;
}

void bosskill_frame(void) {
    u32 seq = BK_WORD(WWBOSS_SEQ_OFF);
    u16 proc;
    fopAc_ac_c* a = 0;

    if (seq == BK_WORD(WWBOSS_ACK_OFF)) return;
    proc = BK_HALF(WWBOSS_PROC_OFF);
    if (NEXT_STAGE_ENABLE != 0 || !bk_proc_ok(proc) ||
        BK_HALF(WWBOSS_TAG_OFF) != WWBOSS_TAG || !bk_stage_ok(proc)) {
        bk_ack(seq, WWBOSS_R_GATE, 0, 0, 0);
        return;
    }
#if WWBOSS_WITH_BST
    // Three parts, none findable by name (the header): the judge.
    if (proc == WWBOSS_PROC_BST) {
        bk_bst(seq);
        return;
    }
#endif
    if (!bk_SearchByName((s16)proc, &a) || !a ||
        *(volatile s16*)((u8*)a + BK_PROC_NAME_OFF) != (s16)proc) {
        bk_ack(seq, WWBOSS_R_NO_ACTOR, 0, 0, 0);
        return;
    }
    if (proc == WWBOSS_PROC_BTD) {
        bk_btd(seq, a);
    } else {
        bk_bmd(seq, a);
    }
}
