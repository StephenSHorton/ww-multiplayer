// refresh.c: the actor_refresh op (refresh.h, include/wwrefresh.h), Tier 3
// of research/stale-actor-refresh.md. Design: ww-multiplayer-handoff/
// design/enemy-mirror-batch-d.json R-D4, R-D5, D4-D7, "actor_refresh".
//
// Co-op (mods/coop/replica, the G-D2 landed set and the G-D4 relay) asks
// for it when a bit the partner set has landed in this game while an
// actor this game placed before it still stands: a boulder (Stone2), a
// bush (Obj_Leaves), an item (daItem), a chest (Tbox) or a barricade
// (Saku). The op deletes that one actor. Each create reads its own bit, so
// a reload never creates the boulder, bush, item or barricade again
// (d_a_stone2.cpp, chk_appear in d_a_obj_leaves.cpp:158-165,
// d_a_item.cpp:227-233, d_a_saku.cpp:709-724) and creates the chest
// already open (checkOpen, d_a_tbox.cpp:314-320, in CreateInit at
// :453-457, and its open BG at :342-344), so the stage ends as vanilla's
// would after the same progress. It never trusts Go's candidate: the REL
// re-derives the bit from the actor's own params and fields, re-reads it
// through the
// DOL's own save queries, and runs every gate below itself, so a wrong
// candidate can cost NOT_SET, MISMATCH, GATE or REFUSED, never a wrong
// delete (actor_refresh "safety_rule"). The one case it cannot tell apart
// is a bit this game set itself; Go's per-bit landed set is the defense.
//
// One request per new seq, in this order (R-D5's steps; the gate ids are
// wwrefresh.h's, in the order checked):
//   1. NEXT_STAGE_ENABLE (wwapi.h): GATE 1;
//   2. the stage M_DragB, kinBOSS, SirenB or X* (the boss arenas, where
//      nothing is ever deleted): GATE 2;
//   3. the tag and the allow-list (Stone2, Obj_Leaves, daItem, Tbox, Saku):
//      REFUSED; Link 0xA9, the keeper and bodies 0xB5, the bosses, doors
//      and switch logic are never touched;
//   4. fopAcM_SearchByID: none, or still creating: NO_ACTOR;
//   5. the identity, proc, params +0xB0 and home room +0x1E2, against the
//      request: MISMATCH;
//   6. the bit re-derived from the actor (the per-proc formulas below) and
//      the bit kind, against the request: MISMATCH; a kind this op never
//      deletes (Tbox funcType 6..8, daItem bit 0x7F or Blue Jelly, a zone
//      bit with no room, a bit no query takes): REFUSED;
//   7. the bit re-read with isSwitch / isTbox / isItem (this =
//      g_dComIfG_gameInfo.info, or its stage memBit for isTbox): clear in
//      this game: NOT_SET;
//   8. the generic gates: mInitState 2 (GATE 3), EvtCmd 0 (4), no CARRY
//      (5), mPauseFlag 0 and the pid in no enemy-mirror slot (6), Link's
//      mDemoType 0 (7), Link farther than WWRF_LINK_FAR (8);
//   9. the per-proc gate (9), Tbox's fingerprint first (FP_MISS);
//  10. the global event mode NONE (10);
// then fopAcM_delete__FUi (fopAcM_deleteByID), queued for the next
// fpcM_Management as enemy_mirror.c's quiet death queues it, and APPLIED.
// The result's bytes 1..0 carry the proc's state as found (wwrefresh.h).
//
// The per-proc gates (actor_refresh "procs"):
//   Stone2     s32 m648 == 0, mode_wait (d_a_stone2.h:138). damaged() is
//              never called: it drops an item (:250-275);
//   Obj_Leaves none beyond the generic ones;
//   daItem     mItemStatus STATUS_UNK0 or UNK1, the pickup lying in the
//              world (execWaitMain, d_a_item.cpp:275-284), and no
//              boomerang or hook in mFlag (d_a_item.h:14-25,31-34,121-123;
//              D5 as re-derived after P0-RF: MAIN_NORMAL is the local
//              pickup flight, which the design had as the pass state);
//   Tbox       BG closed (mpBgWCurrent != mpBgWOpen), mFlags & 0x38 clear,
//              and the PTMF's function word one of actionOpenWait,
//              SwOnWait, SwOnWait2 behind the daTbox_Execute fingerprint
//              (D6: all of them, so the flags and the PTMF back each other
//              up);
//   Saku       v1 delete-only (D7): not burning (+0xEF4), no half in state
//              2, its copied switches equal the params', at least one half
//              standing, and every standing half's own switch set (a
//              barricade with no standing half has nothing stale; one with
//              a half whose switch has not landed stays stale until a
//              reload). The delete releases each half's BG (d_a_saku.cpp
//              :793-797), which the Link gate keeps away from Link.
//
// Everything is under WWREFRESH_ENABLE; with 0 both entry points are empty
// and the block stays reserved.
#include "mailbox_core.h"
#include "wwenemy.h"
#include "wwenemy_kinds.h"
#include "dapy.h"
#include "refresh.h"

// The block lies past the enemy knob table, inside the free slack, and its
// words are aligned (wwrefresh.h "Where").
typedef char rf_check_place[(WWRF_OFF >= WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE && (WWRF_OFF & 3) == 0 &&
                             WWRF_OFF + WWRF_SIZE <= WWRF_SLACK_END && WWRF_ACK_OFF == WWRF_GO_SPAN &&
                             WWRF_RESULT_OFF == WWRF_ACK_OFF + 4 && WWRF_RESERVED_OFF + 4 == WWRF_SIZE) ? 1 : -1];
// The generic fields are wwenemy_kinds.h's EMK_AC_* where both name them.
typedef char rf_check_fields[(WWRF_AC_PROC_OFF == 0x08 && WWRF_AC_INIT_OFF == 0x0C && EMK_AC_PARAMS == 0xB0 &&
                              EMK_AC_HOME_ROOM == 0x1E2 && EMK_AC_STATUS == 0x1C4 &&
                              EMK_AC_STATUS_CARRY == 0x2000 && WWRF_AC_POS_OFF == 0x1F8 &&
                              WWRF_SV_MEMBIT == WWRF_SV_INFO + 0x778 &&
                              WWRF_TBOX_ACT_FN_OFF == 0x340 + 8 && WWRF_SAKU_SW_OFF == WWRF_SAKU_STATE_OFF + 8) ? 1 : -1];

#if WWREFRESH_ENABLE

#define RF_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + WWRF_OFF + (off)))
#define RF_HALF(off) (*(volatile u16*)(MAILBOX_ADDR + WWRF_OFF + (off)))
#define RF_BYTE(off) (*(volatile u8*)(MAILBOX_ADDR + WWRF_OFF + (off)))
#define RF_AF(a, T, off) (*(volatile T*)((u8*)(a) + (off)))

// dComIfGp mCurStage.mName (char[8]), as bosskill.c and enemy_mirror.c read it.
#define RF_CUR_STAGE_NAME ((volatile const u8*)0x803C9D3C)
#define RF_MEM1_LO        0x80000000u
#define RF_MEM1_HI        0x83000000u   // 48 MB MEM1 (docs/05 "48 MB MEM1")

// The save's bit spaces (d_save.h:982-987): switches 0..0x7F memory,
// 0x80..0xBF dan, 0xC0..0xEF zone (WWRF_ZONE_SW_MIN), 0xFF none; items
// 0..0x3F memory, 0x40..0x4F zone, 0x7F none. A zone bit reads
// dComIfGp_roomControl_getZoneNo(room), which asserts 0 <= room < 64
// (d_save.cpp:1585-1604, 1650-1668): such a bit with a room outside that
// range is REFUSED, never queried.
#define RF_SW_ZONE_END    0xF0
#define RF_SW_NONE        0xFF
#define RF_ITEM_ZONE_MIN  0x40
#define RF_ITEM_ZONE_END  0x50
#define RF_ROOM_MAX       64

// isSwitch__10dSv_info_cFii, isItem__10dSv_info_cFii (this, no, room) and
// isTbox__12dSv_memBit_cFi (memBit, no), symbols.txt:1945, 1948, 1892.
typedef BOOL (*rf_is_bit_room_t)(void* info, int no, int room);
typedef BOOL (*rf_is_bit_t)(void* membit, int no);
#define rf_isSwitch ((rf_is_bit_room_t)WWRF_IS_SWITCH_FN)
#define rf_isItem   ((rf_is_bit_room_t)WWRF_IS_ITEM_FN)
#define rf_isTbox   ((rf_is_bit_t)WWRF_IS_TBOX_FN)

static int rf_in_mem1(u32 p) {
    return p >= RF_MEM1_LO && p < RF_MEM1_HI;
}

static void rf_ack(u32 seq, u32 result, u32 gate, u32 state) {
    RF_WORD(WWRF_RESULT_OFF) = (result << 24) | ((gate & 0xFF) << 16) | (state & 0xFFFF);
    RF_WORD(WWRF_RESERVED_OFF) = 0;
    RF_WORD(WWRF_ACK_OFF) = seq;
}

// Step 2: the boss arenas (D4) and every 'X' refight stage.
static int rf_boss_stage(void) {
    static const char names[3][8] = { { 'M', '_', 'D', 'r', 'a', 'g', 'B', 0 },
                                      { 'k', 'i', 'n', 'B', 'O', 'S', 'S', 0 },
                                      { 'S', 'i', 'r', 'e', 'n', 'B', 0, 0 } };
    int n, i;
    if (RF_CUR_STAGE_NAME[0] == 'X') return 1;
    for (n = 0; n < 3; n++) {
        for (i = 0; i < 8; i++) {
            if (RF_CUR_STAGE_NAME[i] != (u8)names[n][i]) break;
            if (names[n][i] == 0) return 1;
        }
        if (i == 8) return 1;
    }
    return 0;
}

// The proc's state bytes for the result (wwrefresh.h +0x18).
static u32 rf_state(u16 proc, void* a) {
    switch (proc) {
    case WWRF_PROC_STONE2:
        return (u32)RF_AF(a, s32, WWRF_STONE2_M648_OFF) & 0xFFFF;
    case WWRF_PROC_ITEM:
        return ((u32)RF_AF(a, u8, WWRF_ITEM_STATUS_OFF) << 8) | RF_AF(a, u8, WWRF_ITEM_FLAG_OFF);
    case WWRF_PROC_TBOX:
        return RF_AF(a, u16, WWRF_TBOX_FLAGS_OFF);
    case WWRF_PROC_SAKU:
        return (((u32)RF_AF(a, s32, WWRF_SAKU_STATE_OFF) & 0xFF) << 8) |
               ((u32)RF_AF(a, s32, WWRF_SAKU_STATE_OFF + 4) & 0xFF);
    }
    return 0;
}

// Step 6: the actor's own bit and its kind, or -1 with *result REFUSED for
// a kind this op never deletes.
static int rf_derive(u16 proc, void* a, u32 params, u8* kind, u32* result) {
    *result = WWRF_R_NONE;
    switch (proc) {
    case WWRF_PROC_STONE2:
        *kind = WWRF_BIT_SW;
        return (int)((params >> WWRF_STONE2_SW_SHIFT) & WWRF_STONE2_SW_MASK);
    case WWRF_PROC_LEAVES:
        *kind = WWRF_BIT_SW;
        return (int)((params >> WWRF_LEAVES_SW_SHIFT) & WWRF_LEAVES_SW_MASK);
    case WWRF_PROC_SAKU:
        *kind = WWRF_BIT_SW;
        return (int)((params >> WWRF_SAKU_SW1_SHIFT) & WWRF_SAKU_SW_MASK);
    case WWRF_PROC_ITEM: {
        // create's own derivation (d_a_item.cpp:227-233): Blue Jelly keeps
        // the bit as a switch and is never treated as an item bit.
        int bit = RF_AF(a, s32, WWRF_ITEM_BITNO_OFF) & WWRF_ITEM_BITNO_MASK;
        *kind = WWRF_BIT_ITEM;
        if (bit == WWRF_ITEM_BIT_NONE || RF_AF(a, u8, WWRF_ITEM_NO_OFF) == WWRF_ITEM_NO_BLUE_JELLY) {
            *result = WWRF_R_REFUSED;
            return -1;
        }
        return bit;
    }
    case WWRF_PROC_TBOX: {
        // checkOpen's isTbox (d_a_tbox.cpp:314-320); funcType 6 (the tact
        // chest) and 7/8 (the sea's STAGE_SEA2 bits) never.
        u32 func = params & WWRF_TBOX_FUNC_MASK;
        *kind = WWRF_BIT_TBOX;
        if (func >= WWRF_TBOX_FUNC_REFUSE_LO && func <= WWRF_TBOX_FUNC_REFUSE_HI) {
            *result = WWRF_R_REFUSED;
            return -1;
        }
        return (int)((params >> WWRF_TBOX_BIT_SHIFT) & WWRF_TBOX_BIT_MASK);
    }
    }
    *result = WWRF_R_REFUSED;
    return -1;
}

// Step 7: 1 set, 0 clear, -1 a bit no query may take (REFUSED).
static int rf_read_bit(u8 kind, int no, s8 room) {
    switch (kind) {
    case WWRF_BIT_SW:
        if (no == RF_SW_NONE) return 0;
        if (no >= RF_SW_ZONE_END) return -1;
        if (no >= WWRF_ZONE_SW_MIN && (room < 0 || room >= RF_ROOM_MAX)) return -1;
        return rf_isSwitch((void*)WWRF_SV_INFO, no, room) != 0;
    case WWRF_BIT_ITEM:
        if (no >= RF_ITEM_ZONE_END) return -1;
        if (no >= RF_ITEM_ZONE_MIN && (room < 0 || room >= RF_ROOM_MAX)) return -1;
        return rf_isItem((void*)WWRF_SV_INFO, no, room) != 0;
    case WWRF_BIT_TBOX:
        return rf_isTbox((void*)WWRF_SV_MEMBIT, no) != 0;
    }
    return -1;
}

// Step 8's mirror test: the pid in any enemy-block slot (a mirrored copy is
// enemy_mirror.c's, and none of the allow-list is mirrored today).
static int rf_mirrored(u32 pid) {
    int i;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++)
        if (*(volatile u32*)(MAILBOX_ADDR + WWENEMY_OFF + WWENEMY_SLOTS_OFF + i * WWENEMY_SLOT_SIZE +
                             WWENEMY_SL_PID_OFF) == pid)
            return 1;
    return 0;
}

// Step 9, Tbox: daTbox_Execute through the copy's own profile
// (sub_method -> mpExecuteFunc, as enemy_mirror.c's em_bb_tc), behind the
// retail fingerprint (P0-RF matched it live), or 0.
static u32 rf_tbox_exec(void* a) {
    u32 sm = RF_AF(a, u32, EMK_AC_SUB_METHOD);
    u32 exec;
    if (!rf_in_mem1(sm)) return 0;
    exec = RF_AF(sm, u32, EMK_METHOD_EXECUTE);
    if (!rf_in_mem1(exec) || exec < RF_MEM1_LO + WWRF_TBOX_TEXT_EXEC || !rf_in_mem1(exec + WWRF_TBOX_FP_OFF1)) return 0;
    if (RF_AF(exec, u32, WWRF_TBOX_FP_OFF0) != WWRF_TBOX_FP_WORD0 || RF_AF(exec, u32, WWRF_TBOX_FP_OFF1) != WWRF_TBOX_FP_WORD1)
        return 0;
    return exec;
}

// Step 9: WWRF_R_NONE when the proc's gate holds, else GATE (9), FP_MISS.
static u32 rf_proc_gate(u16 proc, void* a, s8 room) {
    switch (proc) {
    case WWRF_PROC_STONE2:
        return RF_AF(a, s32, WWRF_STONE2_M648_OFF) == 0 ? WWRF_R_NONE : WWRF_R_GATE;
    case WWRF_PROC_LEAVES:
        return WWRF_R_NONE;
    case WWRF_PROC_ITEM:
        return (RF_AF(a, u8, WWRF_ITEM_STATUS_OFF) <= WWRF_ITEM_STATUS_WAIT_MAX &&
                (RF_AF(a, u8, WWRF_ITEM_FLAG_OFF) & WWRF_ITEM_FLAG_BUSY) == 0)
                   ? WWRF_R_NONE
                   : WWRF_R_GATE;
    case WWRF_PROC_TBOX: {
        u32 exec = rf_tbox_exec(a);
        u32 fn, base;
        if (!exec) return WWRF_R_FP_MISS;
        if (RF_AF(a, u32, WWRF_TBOX_BG_CUR_OFF) == RF_AF(a, u32, WWRF_TBOX_BG_OPEN_OFF)) return WWRF_R_GATE;
        if (RF_AF(a, u16, WWRF_TBOX_FLAGS_OFF) & WWRF_TBOX_FLAGS_BUSY) return WWRF_R_GATE;
        fn = RF_AF(a, u32, WWRF_TBOX_ACT_FN_OFF);
        base = exec - WWRF_TBOX_TEXT_EXEC;
        if (fn != base + WWRF_TBOX_TEXT_OPEN_WAIT && fn != base + WWRF_TBOX_TEXT_SW_WAIT &&
            fn != base + WWRF_TBOX_TEXT_SW_WAIT2)
            return WWRF_R_GATE;
        return WWRF_R_NONE;
    }
    case WWRF_PROC_SAKU: {
        u32 params = RF_AF(a, u32, EMK_AC_PARAMS);
        int i, standing = 0;
        if (RF_AF(a, u8, WWRF_SAKU_BURN_OFF) != 0) return WWRF_R_GATE;
        for (i = 0; i < 2; i++) {
            s32 st = RF_AF(a, s32, WWRF_SAKU_STATE_OFF + 4 * i);
            u32 sw = (params >> (i ? WWRF_SAKU_SW2_SHIFT : WWRF_SAKU_SW1_SHIFT)) & WWRF_SAKU_SW_MASK;
            if (st == WWRF_SAKU_STATE_BURNING) return WWRF_R_GATE;
            if (st != 0 && st != WWRF_SAKU_STATE_STANDING && st != WWRF_SAKU_STATE_BROKEN) return WWRF_R_GATE;
            // Half 1 exists only for checkSaku2 (d_a_saku.cpp:717-724).
            if (i == 1 && (params & WWRF_SAKU_HALF1_MASK) == 0 && st != 0) return WWRF_R_GATE;
            if (RF_AF(a, u32, WWRF_SAKU_SW_OFF + 4 * i) != sw) return WWRF_R_GATE;
            if (st != WWRF_SAKU_STATE_STANDING) continue;
            standing++;
            if (rf_read_bit(WWRF_BIT_SW, (int)sw, room) != 1) return WWRF_R_GATE;
        }
        return standing ? WWRF_R_NONE : WWRF_R_GATE;
    }
    }
    return WWRF_R_GATE;
}

static int rf_allowed(u16 proc) {
    return proc == WWRF_PROC_STONE2 || proc == WWRF_PROC_LEAVES || proc == WWRF_PROC_ITEM || proc == WWRF_PROC_TBOX ||
           proc == WWRF_PROC_SAKU;
}

#endif

void refresh_link(void) {
#if WWREFRESH_ENABLE
    rf_ack(RF_WORD(WWRF_SEQ_OFF), WWRF_R_NONE, 0, 0);
#endif
}

void refresh_frame(void) {
#if WWREFRESH_ENABLE
    u32 seq = RF_WORD(WWRF_SEQ_OFF);
    u16 proc;
    u32 pid, params, r, st;
    s8 room;
    u8 kind = 0;
    int bit, set;
    fopAc_ac_c* a = 0;
    fopAc_ac_c* link;

    if (seq == RF_WORD(WWRF_ACK_OFF)) return;
    if (NEXT_STAGE_ENABLE != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_STAGE_CHANGE, 0);
        return;
    }
    if (rf_boss_stage()) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_BOSS_STAGE, 0);
        return;
    }
    proc = RF_HALF(WWRF_PROC_OFF);
    if (RF_HALF(WWRF_TAG_OFF) != WWRF_TAG || !rf_allowed(proc)) {
        rf_ack(seq, WWRF_R_REFUSED, 0, 0);
        return;
    }
    pid = RF_WORD(WWRF_PID_OFF);
    if (fpcM_IsCreating(pid) || !fopAcM_SearchByID(pid, &a) || !a || !rf_in_mem1((u32)a)) {
        rf_ack(seq, WWRF_R_NO_ACTOR, 0, 0);
        return;
    }
    params = RF_AF(a, u32, EMK_AC_PARAMS);
    room = RF_AF(a, s8, EMK_AC_HOME_ROOM);
    if ((u16)RF_AF(a, s16, WWRF_AC_PROC_OFF) != proc || params != RF_WORD(WWRF_PARAMS_OFF) ||
        room != (s8)RF_BYTE(WWRF_ROOM_OFF)) {
        rf_ack(seq, WWRF_R_MISMATCH, 0, 0);
        return;
    }
    st = rf_state(proc, a);
    bit = rf_derive(proc, a, params, &kind, &r);
    if (bit < 0) {
        rf_ack(seq, r, 0, st);
        return;
    }
    if (kind != RF_BYTE(WWRF_BIT_KIND_OFF) || (u16)bit != RF_HALF(WWRF_BIT_NO_OFF)) {
        rf_ack(seq, WWRF_R_MISMATCH, 0, st);
        return;
    }
    set = rf_read_bit(kind, bit, room);
    if (set < 0) {
        rf_ack(seq, WWRF_R_REFUSED, 0, st);
        return;
    }
    if (!set) {
        rf_ack(seq, WWRF_R_NOT_SET, 0, st);
        return;
    }
    if (RF_AF(a, s8, WWRF_AC_INIT_OFF) != WWRF_AC_INIT_RUNNING) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_INIT, st);
        return;
    }
    if (RF_AF(a, u16, WWRF_AC_EVTCMD_OFF) != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_EVENT_CMD, st);
        return;
    }
    if (RF_AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_CARRY, st);
        return;
    }
    if (RF_AF(a, u8, WWRF_AC_PAUSE_OFF) != 0 || rf_mirrored(pid)) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_MIRROR, st);
        return;
    }
    link = PLAYER_PTR_ARRAY[0];
    if (!rf_in_mem1((u32)link) || RF_AF(link, u16, DAPY_DEMO_TYPE) != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_LINK_DEMO, st);
        return;
    }
    {
        f32 dx = RF_AF(link, f32, WWRF_AC_POS_OFF + 0) - RF_AF(a, f32, WWRF_AC_POS_OFF + 0);
        f32 dy = RF_AF(link, f32, WWRF_AC_POS_OFF + 4) - RF_AF(a, f32, WWRF_AC_POS_OFF + 4);
        f32 dz = RF_AF(link, f32, WWRF_AC_POS_OFF + 8) - RF_AF(a, f32, WWRF_AC_POS_OFF + 8);
        // Not "> far^2": a NaN distance fails the gate too.
        if (!(dx * dx + dy * dy + dz * dz > (f32)WWRF_LINK_FAR * (f32)WWRF_LINK_FAR)) {
            rf_ack(seq, WWRF_R_GATE, WWRF_GATE_LINK_NEAR, st);
            return;
        }
    }
    r = rf_proc_gate(proc, a, room);
    if (r != WWRF_R_NONE) {
        rf_ack(seq, r, r == WWRF_R_GATE ? WWRF_GATE_PROC : 0, st);
        return;
    }
    if (*(volatile u8*)WWRF_EVT_MODE != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_EVENT_MODE, st);
        return;
    }
    // Queued, not run here (wwapi.h fopAcM_deleteByID): FALSE while the
    // process is creating or already deleting, which is no actor to take.
    if (!fopAcM_deleteByID(pid)) {
        rf_ack(seq, WWRF_R_NO_ACTOR, 0, st);
        return;
    }
    rf_ack(seq, WWRF_R_APPLIED, 0, st);
#endif
}
