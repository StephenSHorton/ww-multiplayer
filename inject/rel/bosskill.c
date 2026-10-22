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

// The stage the op may run in, NUL-padded to the name's 8 bytes.
static int bk_stage_ok(void) {
    static const char want[8] = { 'M', '_', 'D', 'r', 'a', 'g', 'B', 0 };
    int i;
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

void bosskill_frame(void) {
    u32 seq = BK_WORD(WWBOSS_SEQ_OFF);
    fopAc_ac_c* a = 0;
    volatile s16* act;
    volatile s16* state;
    s16 was, st;
    u8 phase;

    if (seq == BK_WORD(WWBOSS_ACK_OFF)) return;
    if (NEXT_STAGE_ENABLE != 0 || BK_HALF(WWBOSS_PROC_OFF) != WWBOSS_PROC_BTD ||
        BK_HALF(WWBOSS_TAG_OFF) != WWBOSS_TAG || !bk_stage_ok()) {
        bk_ack(seq, WWBOSS_R_GATE, 0, 0, 0);
        return;
    }
    if (!bk_SearchByName((s16)WWBOSS_PROC_BTD, &a) || !a ||
        *(volatile s16*)((u8*)a + BK_PROC_NAME_OFF) != (s16)WWBOSS_PROC_BTD) {
        bk_ack(seq, WWBOSS_R_NO_ACTOR, 0, 0, 0);
        return;
    }
    act = (volatile s16*)((u8*)a + WWBOSS_BTD_M02E4_OFF);
    state = (volatile s16*)((u8*)a + WWBOSS_BTD_STATE_OFF);
    was = *act;
    st = *state;
    phase = *(volatile u8*)((u8*)a + WWBOSS_BTD_M02E0_OFF);
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
