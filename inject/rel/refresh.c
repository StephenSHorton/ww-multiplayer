// refresh.c: the actor_refresh op (refresh.h, include/wwrefresh.h), Tier 3
// of research/stale-actor-refresh.md. Design: ww-multiplayer-handoff/
// design/enemy-mirror-batch-d.json R-D4, R-D5, D4-D7, "actor_refresh";
// the re-create and the table rows: archive/ww-online-review/
// DESIGN-big-items.md (b), review idea 11.
//
// Co-op (mods/coop/replica, the G-D2 landed set and the G-D4 relay) asks
// for it when a bit the partner set has landed in this game while an
// actor this game placed before it still stands. Each class reads its bit
// only in create, so the op brings that one actor up to date the way a
// reload of its room would, by one of two modes:
//   - delete, for a class whose create refuses to come up once the bit is
//     set: a boulder (Stone2, d_a_stone2.cpp), a bush (Obj_Leaves,
//     chk_appear d_a_obj_leaves.cpp:158-165), an item (daItem,
//     d_a_item.cpp:227-233), a barricade (Saku, d_a_saku.cpp:709-724),
//     and the rows' WALL, FLOOR, Obj_Ice, MJDOOR, MKIE, MKIEK, MKNJD and
//     VFAN (each cites its create in wwrefresh.h);
//   - re-create, for a class whose create builds it in its set state: a
//     chest (Tbox: checkOpen, d_a_tbox.cpp:314-320, in CreateInit at
//     :453-457, comes up open and empty in actionWait, with its open BG
//     :342-344; a delete alone left no chest at all until the reload), and
//     the rows' SWHIT0, WARPT, MFLFT, KOKIIE, VMC, DOOR10 and DOOR12. The
//     actor is deleted and created again from its own create params
//     (proc, params, home pos, home room, home angle, scale, argument), in
//     its own layer ("Re-create" below).
// It never trusts Go's candidate: the REL re-derives the bit from the
// actor's own params and fields, re-reads it through the DOL's own save
// queries, and runs every gate below itself, so a wrong candidate can cost
// NOT_SET, MISMATCH, GATE, IN_STATE or REFUSED, never a wrong delete
// (actor_refresh "safety_rule"). The one case it cannot tell apart is a bit
// this game set itself; Go's per-bit landed set is the defense.
//
// One request per new seq, in this order (R-D5's steps; the gate ids are
// wwrefresh.h's, in the order checked):
//   1. NEXT_STAGE_ENABLE (wwapi.h): GATE 1;
//   2. the stage M_DragB, kinBOSS, SirenB or X* (the boss arenas, where
//      nothing is ever deleted): GATE 2;
//   3. the tag and the allow-list (Stone2, Obj_Leaves, daItem, Tbox, Saku
//      and rf_rows): REFUSED; Link 0xA9, the keeper and bodies 0xB5, the
//      bosses and every other door or switch class are never touched;
//   4. fopAcM_SearchByID: none, or still creating: NO_ACTOR;
//   5. the identity, proc, params +0xB0 and home room +0x1E2, against the
//      request: MISMATCH;
//   6. the bit re-derived from the actor (the per-proc formulas below) and
//      the bit kind, against the request: MISMATCH; a kind this op never
//      touches (Tbox funcType 6..8, daItem bit 0x7F or Blue Jelly, a zone
//      bit with no room, a bit no query takes, a row's refusals): REFUSED;
//   7. the bit re-read with isSwitch / isTbox / isItem (this =
//      g_dComIfG_gameInfo.info, or its stage memBit for isTbox): clear in
//      this game: NOT_SET;
//   8. the generic gates: mInitState 2 (GATE 3), EvtCmd 0 (4), no CARRY
//      (5), mPauseFlag 0 and the pid in no enemy-mirror slot (6), Link's
//      mDemoType 0 (7), Link farther than WWRF_LINK_FAR (8);
//   9. the per-proc gate (9), Tbox's fingerprint first (FP_MISS), the
//      actor already in its set state (IN_STATE), a row's Link XZ reach
//      (8);
//  10. the global event mode NONE (10) and no event order queued (11):
//      an order holds raw actor pointers (d_event.h:51-52), and the
//      keeper runs after Link's execute, so Link's order of this frame is
//      already counted;
// then fopAcM_delete__FUi (fopAcM_deleteByID), queued for the next
// fpcM_Management as enemy_mirror.c's quiet death queues it, and APPLIED;
// for a re-create class the create follows (RECREATED, +0x1C the new
// pid; CREATE_FAILED when fopAcM_create refused, which leaves the actor
// gone until its room reloads and builds it in its set state). The
// result's bytes 1..0 carry the proc's state as found (wwrefresh.h).
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
//   Tbox       BG closed (mpBgWCurrent != mpBgWOpen; open is IN_STATE: the
//              chest is open already), mFlags & 0x38 clear, and the PTMF's
//              function word one of actionOpenWait, SwOnWait, SwOnWait2
//              behind the daTbox_Execute fingerprint (D6: all of them, so
//              the flags and the PTMF back each other up). Re-created: an
//              accepted delete is the only way the create runs, so two
//              chests never stand at once with the item in both (ww-online
//              ww_world.cpp:299-300's warning), and the new one comes up
//              open and empty: nothing gives the item twice;
//   Saku       v1 delete-only (D7): not burning (+0xEF4), no half in state
//              2, its copied switches equal the params', at least one half
//              standing, and every standing half's own switch set (a
//              barricade with no standing half has nothing stale; one with
//              a half whose switch has not landed stays stale until a
//              reload). The delete releases each half's BG (d_a_saku.cpp
//              :793-797), which the Link gate keeps away from Link;
//   rf_rows    the row's state field: a value in its idle set is the stale
//              state it acts on, one in its done set (or a zero need
//              field) is IN_STATE, any other (mid-break, mid-event,
//              mid-melt) is GATE; then Link farther than its XZ reach, and
//              KOKIIE's hands not still creating (rf_rows lists each).
//
// Re-create (adapted from datstarkey/ww-online GameMod/src/puppet_link/
// puppet_liveworld.c lw_recreate :188-206, MIT, notice below; its typed
// port seaborne/native/src/mods/ww_online/ww_world.cpp recreate :300-320):
//   - the layer is read BEFORE the delete: an accepted delete runs
//     fpcLyTg_QueueTo, which sets mLyTg.mpLayer (+0x2C: mLyTg +0x18 plus
//     create_tag_class's 0x14) to NULL (f_pc_layer_tag.cpp:36-44, DOL
//     0x8003E044), and a create into NULL writes through NULL. So are the
//     home pos, angle and room, the scale and the argument;
//   - the create runs only if the delete was accepted (fopAcM_delete__FUi
//     refuses a process still creating or already deleting): otherwise two
//     actors would stand, two chests with one item each;
//   - it is made in the actor's own layer, its room scene's (or the
//     stage's for a stage-file actor of room -1), with fpcLy_SetCurrentLayer
//     0x8003D964 around it and fpcLy_CurrentLayer 0x8003D96C restored after
//     (f_pc_layer.cpp:92-100; fpcM_Create takes fpcLy_CurrentLayer(),
//     f_pc_manager.h:42-44): an actor created in another layer would
//     outlive the room and stand twice after a reload;
//   - fopAcM_create (0x8002451C) queues it (wwapi.h): the new actor's own
//     create reads the bit and builds the set state. createAppend keeps the
//     scale as (u8)(10 * scale) (f_op_actor_mng.cpp:111-114), which every
//     stage-placed actor's scale already is, and a new setID (none of the
//     re-create classes reads one). Every re-create class's IsDelete is an
//     unconditional TRUE (d_a_tbox.cpp:1201, d_a_swhit0.cpp:457,
//     d_a_obj_warpt.cpp:871, d_a_mflft.cpp:514, d_a_kokiie.cpp:336,
//     d_a_obj_vmc.cpp:390, d_a_door10.cpp:841, d_a_door12.cpp:858), so the
//     old actor is gone in the same fpcM_Management that runs the new one's
//     create.
//
// Everything is under WWREFRESH_ENABLE; with 0 both entry points are empty
// and the block stays reserved.
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
#include "wwenemy.h"
#include "wwenemy_kinds.h"
#include "dapy.h"
#include "refresh.h"

// The block lies past the enemy knob table, inside the free slack, and its
// words are aligned (wwrefresh.h "Where").
typedef char rf_check_place[(WWRF_OFF >= WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE && (WWRF_OFF & 3) == 0 &&
                             WWRF_OFF + WWRF_SIZE <= WWRF_SLACK_END && WWRF_ACK_OFF == WWRF_GO_SPAN &&
                             WWRF_RESULT_OFF == WWRF_ACK_OFF + 4 && WWRF_NEW_PID_OFF + 4 == WWRF_SIZE) ? 1 : -1];
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

// The re-create's fields of fopAc_ac_c (f_op_actor.h:281-291, f_pc_base.h
// :24; the layer tag f_pc_layer_tag.h:16-21 after c_tag.h's 0x14-byte
// create_tag_class) and the layer calls (symbols.txt:1118-1119).
#define RF_AC_LAYER_OFF      0x2C   // layer_class* mLyTg.mpLayer: NULL once the delete is accepted
#define RF_AC_ARGUMENT_OFF   0x1C1  // s8 argument
#define RF_AC_HOME_POS_OFF   0x1D0  // cXyz home.pos
#define RF_AC_HOME_ANGLE_OFF 0x1DC  // csXyz home.angle
#define RF_AC_CUR_ROOM_OFF   0x20A  // s8 current.roomNo (fopAcM_GetRoomNo)
#define RF_AC_SCALE_OFF      0x214  // cXyz scale
typedef void* (*rf_cur_layer_t)(void);
typedef void (*rf_set_layer_t)(void* layer);
#define rf_cur_layer ((rf_cur_layer_t)0x8003D96C)   // fpcLy_CurrentLayer__Fv
#define rf_set_layer ((rf_set_layer_t)0x8003D964)   // fpcLy_SetCurrentLayer__FP11layer_class

// isSwitch__10dSv_info_cFii, isItem__10dSv_info_cFii (this, no, room) and
// isTbox__12dSv_memBit_cFi (memBit, no), symbols.txt:1945, 1948, 1892.
typedef BOOL (*rf_is_bit_room_t)(void* info, int no, int room);
typedef BOOL (*rf_is_bit_t)(void* membit, int no);
#define rf_isSwitch ((rf_is_bit_room_t)WWRF_IS_SWITCH_FN)
#define rf_isItem   ((rf_is_bit_room_t)WWRF_IS_ITEM_FN)
#define rf_isTbox   ((rf_is_bit_t)WWRF_IS_TBOX_FN)

// The table rows (review idea 11; their rules adapted from ww-online
// puppet_liveworld.c l_lwRules :46-81, each re-derived from the decomp).
// The bit: (params >> shift) & 0xFF, or home.angle.x's (RF_ROW_ANGLE_X).
// The state field st_off (st_w bytes, read unsigned): a value v < 32 with
// bit v of idle is the stale state the row acts on, with bit v of done
// the set state already (IN_STATE), anything else GATE. need_off != 0: a
// field that must be non-zero (a door's lock, a jar's lid BG), else
// IN_STATE. far10: Link farther than far10 * 10 XZ units (their near
// column, puppet_liveworld.c:96-106: a dropped lift or house, a grown
// tree, a jar's mouth or a door's reach must not land on him), besides
// the generic 3D WWRF_LINK_FAR.
#define RF_ROW_RECREATE 0x01  // delete, then create again (else delete only)
#define RF_ROW_ANGLE_X  0x02  // the bit is home.angle.x's, not the params'
#define RF_ROW_CUR_ROOM 0x04  // create reads it with fopAcM_GetRoomNo: a zone bit needs current.roomNo == the home room
#define RF_ROW_NO_ZONE  0x08  // create reads it with the stay room: memory or dan only (< WWRF_ZONE_SW_MIN)
#define RF_ROW_MEM_ONLY 0x10  // create reads it with room -1: memory only (< WWRF_MEM_SW_END)
#define RF_ROW_NEED32   0x20  // need_off is a u32 (else a u8)
#define RF_ROW_KIDS     0x40  // KOKIIE: its SHAND hands' pids, deleted with it

// KOKIIE's hands (d_a_kokiie.h:28-29; himo_create d_a_kokiie.cpp:180-205):
// fpc_ProcID m2D4[5] and u8 m2E8[5], 0 = not created yet. Each SHAND finds
// its house by pid (field_308, d_a_shand.cpp:235-237) and touches its
// pointers into the house only while that pid resolves, so a hand outlives
// the house harmlessly, but stays hanging in the air: the re-created house
// (switch set) makes none (himo_create returns at once, :173-175), so the
// old five are deleted with it.
#define RF_KOKIIE_KIDS_OFF   0x2D4
#define RF_KOKIIE_KSTATE_OFF 0x2E8
#define RF_KOKIIE_KIDS       5

typedef struct {
    u16 proc;
    u16 st_off;
    u16 need_off;
    u8 shift;
    u8 flags;
    u8 st_w;
    u8 far10;
    u32 idle;
    u32 done;
} rf_row;

#define RF_V(v) (1u << (v))

static const rf_row rf_rows[] = {
    // Gone once set: delete. Idle = not breaking (each class's own break
    // releases its BG and deletes itself).
    // WALL: home room (d_a_wall.cpp:141-145); mState u8 +0x5AC 0 standing,
    // 1 broken (d_a_wall.h:42; :301).
    { WWRF_PROC_WALL, 0x5AC, 0, 0, 0, 1, 0, RF_V(0), 0 },
    // FLOOR: home room (d_a_floor.cpp:59-61); u8 +0x2D8 set when the
    // Iron Boots break starts (rideCallBack :21-24, d_a_floor.h:22).
    { WWRF_PROC_FLOOR, 0x2D8, 0, 0, 0, 1, 0, RF_V(0), 0 },
    // Obj_Ice: chk_appear via fopAcM_isSwitch (d_a_obj_ice.cpp:105-115,
    // PRM_SWSAVE 8 at 0, d_a_obj_ice.h:15-16); s32 m458 +0x458 is the
    // melt countdown, 0 until a fire hit sets 0x5A (:333).
    { WWRF_PROC_ICE, 0x458, 0, 0, RF_ROW_CUR_ROOM, 4, 0, RF_V(0), 0 },
    // MJDOOR: field_0x2CC = (u8)params, current room
    // (d_a_obj_majyuu_door.cpp:114-116, 146-148); int mMode +0x290 0 WAIT,
    // 1 DELETE (breaking; d_a_obj_majyuu_door.h:13-14, 40).
    { WWRF_PROC_MJDOOR, 0x290, 0, 0, RF_ROW_CUR_ROOM, 4, 0, RF_V(0), 0 },
    // MKIE: prm_get_swSave (params >> 16) (d_a_obj_mkie.cpp:208); int
    // mModeProc +0xF50 0 mode_wait, 1 demoWait, 2 demo (:390-394,
    // d_a_obj_mkie.h:95).
    { WWRF_PROC_MKIE, 0xF50, 0, WWRF_MKIE_SW_SHIFT, RF_ROW_CUR_ROOM, 4, 0, RF_V(0), 0 },
    // MKIEK: PRM_SWSAVE 8 at 0 (d_a_obj_mkiek.cpp:89-91); mState +0x45C
    // STATE_0 until lit (:139, d_a_obj_mkiek.h:9-13, 56).
    { WWRF_PROC_MKIEK, 0x45C, 0, 0, RF_ROW_CUR_ROOM, 4, 0, RF_V(0), 0 },
    // MKNJD: PRM_SWITCH 8 at 0 (d_a_obj_mknjd.cpp:303, d_a_obj_mknjd.h:17-24);
    // u8 m043F +0x43F 0 (idle, talkable) or 8 (idle with the song-lesson
    // callbacks, :281-287); 1..7 and 9..12 are its events (Execute :719-).
    { WWRF_PROC_MKNJD, 0x43F, 0, 0, RF_ROW_CUR_ROOM, 1, 0, RF_V(0) | RF_V(8), 0 },
    // VFAN: PRM_SWSAVE 8 at 0 (d_a_obj_vfan.cpp:90, d_a_obj_vfan.h:16-17);
    // int mState +0x440 0 until hit (:153-163).
    { WWRF_PROC_VFAN, 0x440, 0, 0, RF_ROW_CUR_ROOM, 4, 0, RF_V(0), 0 },
    // Built in the set state: re-create.
    // SWHIT0: getSwNo (u8)params, current room (d_a_swhit0.cpp:73-75,
    // 166-172); u8 mState +0x551: 0 off (the stale state), 1..3 being lit
    // here, 4 on, 5 on with a timer (d_a_swhit0.h:51; Execute :424-446).
    { WWRF_PROC_SWHIT0, 0x551, 0, 0, RF_ROW_RECREATE | RF_ROW_CUR_ROOM, 1, 0, RF_V(0), RF_V(4) | RF_V(5) },
    // WARPT: m2AC = home.angle.x & 0xFF read with the stay room
    // (d_a_obj_warpt.cpp:707-725); s32 m290 +0x290 mode 1 CLOSE (the lid
    // on; modeClose with a real lid waits for a hit, :455-474), 0 OPEN;
    // mpLidBgW +0x340 NULL for a jar with no real lid (_createHeap
    // :143-151), which polls the switch itself. Their near 12 (lid radius
    // 70, body 85).
    { WWRF_PROC_WARPT, 0x290, 0x340, 0, RF_ROW_RECREATE | RF_ROW_ANGLE_X | RF_ROW_NO_ZONE | RF_ROW_NEED32, 4, 12,
      RF_V(1), RF_V(0) },
    // MFLFT: m2A1 = params >> 24, current room (d_a_mflft.cpp:604, 650-666:
    // set, all ropes cut and the lift on the lava's ground); s16 m29A
    // +0x29A 1 once the last rope is cut (:199-200). Their near 50.
    { WWRF_PROC_MFLFT, 0x29A, 0, WWRF_MFLFT_SW_SHIFT, RF_ROW_RECREATE | RF_ROW_CUR_ROOM, 2, 50, RF_V(0), RF_V(1) },
    // KOKIIE: m2A2 = params >> 24, fopAcM_isSwitch (d_a_kokiie.cpp:393,
    // 438-441: set, it comes up fallen, m29A 1, no hands); s16 m29A +0x29A
    // (d_a_kokiie.h:12). Their near 100 (cull box 1000).
    { WWRF_PROC_KOKIIE, 0x29A, 0, WWRF_KOKIIE_SW_SHIFT, RF_ROW_RECREATE | RF_ROW_CUR_ROOM | RF_ROW_KIDS, 2, 100, RF_V(0),
      RF_V(1) },
    // VMC: prm_get_swSave (params >> 8) (d_a_obj_vmc.h:17-22; CreateInit
    // d_a_obj_vmc.cpp:145-157: set, the tree grown with its BG); s32
    // mState +0x4B8 (the sizes chain from mCyl +0x304: the header's two
    // "0x434" comments are a typo) 0 BASE_MAIN, 1..2 the grow demo, 3
    // TREE_MAIN (:11-16). Their near 30 (cull box 300).
    { WWRF_PROC_VMC, 0x4B8, 0, WWRF_VMC_SW_SHIFT, RF_ROW_RECREATE | RF_ROW_CUR_ROOM, 4, 30, RF_V(0), RF_V(3) },
#if WWRF_DOOR_ROWS
    // Pending ST1 (archive/coop-batch2/live/LIVE-3-doors.md): ww-online
    // froze on its live lock write and moved to this re-create; ours keeps
    // the Tier 1 write until the key-door check decides. getSwbit (u8)params
    // read with room -1 (d_door.cpp:16-18; setKey d_a_door10.cpp:27-51,
    // d_a_door12.cpp:52-72): a re-created door's setKey sees the switch and
    // gives keyOff. u8 action +0x354 / +0x314: 0 Init (its actionInit runs
    // setKey itself: IN_STATE), 1 Wait (l_action, d_a_door10.cpp:796-802,
    // d_a_door12.cpp:816-821); dDoor_key2_c::mbEnabled +0x308 / +0x2E4
    // (d_a_door10.h:46, d_a_door12.h:51; d_door.h:89), 0 = unlocked
    // already. Their near 25 (checkArea's reach, d_a_door10.cpp:375).
    { WWRF_PROC_DOOR10, 0x354, 0x308, 0, RF_ROW_RECREATE | RF_ROW_MEM_ONLY, 1, 25, RF_V(1), RF_V(0) },
    { WWRF_PROC_DOOR12, 0x314, 0x2E4, 0, RF_ROW_RECREATE | RF_ROW_MEM_ONLY, 1, 25, RF_V(1), RF_V(0) },
#endif
};

#define RF_NROWS ((int)(sizeof(rf_rows) / sizeof(rf_rows[0])))

static const rf_row* rf_row_of(u16 proc) {
    int i;
    for (i = 0; i < RF_NROWS; i++)
        if (rf_rows[i].proc == proc) return &rf_rows[i];
    return 0;
}

static u32 rf_field(void* a, u32 off, u32 w) {
    if (w == 1) return RF_AF(a, u8, off);
    if (w == 2) return RF_AF(a, u16, off);
    return RF_AF(a, u32, off);
}

static int rf_in_mem1(u32 p) {
    return p >= RF_MEM1_LO && p < RF_MEM1_HI;
}

static void rf_ack_pid(u32 seq, u32 result, u32 gate, u32 state, u32 pid) {
    RF_WORD(WWRF_RESULT_OFF) = (result << 24) | ((gate & 0xFF) << 16) | (state & 0xFFFF);
    RF_WORD(WWRF_NEW_PID_OFF) = pid;
    RF_WORD(WWRF_ACK_OFF) = seq;
}

static void rf_ack(u32 seq, u32 result, u32 gate, u32 state) {
    rf_ack_pid(seq, result, gate, state, 0);
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
    const rf_row* row;
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
    row = rf_row_of(proc);
    return row ? rf_field(a, row->st_off, row->st_w) & 0xFFFF : 0;
}

// Step 6 for a row: its switch, or -1 with *result REFUSED.
static int rf_derive_row(const rf_row* row, void* a, u32 params, s8 room, u32* result) {
    u32 src = (row->flags & RF_ROW_ANGLE_X) ? (u16)RF_AF(a, s16, RF_AC_HOME_ANGLE_OFF) : params;
    int no = (int)((src >> row->shift) & WWRF_ROW_SW_MASK);
    u32 t;
    switch (row->proc) {
    case WWRF_PROC_SWHIT0:
        t = (params >> WWRF_SWHIT0_TIMER_SHIFT) & 0xFF;
        if (t != 0 && t != 0xFF) goto refuse;
        break;
    case WWRF_PROC_WARPT:
        t = params & WWRF_WARPT_TYPE_MASK;
        if (t == WWRF_WARPT_TYPE_ON || (t >= WWRF_WARPT_TYPE_SP_LO && t <= WWRF_WARPT_TYPE_SP_HI)) goto refuse;
        break;
    case WWRF_PROC_DOOR12:
        if ((((u16)RF_AF(a, s16, RF_AC_HOME_ANGLE_OFF + 4) >> 8) & 0xFF) == WWRF_DOOR12_ARG1_TMP) goto refuse;
        break;
    }
    if (no == RF_SW_NONE) return no;
    if ((row->flags & RF_ROW_MEM_ONLY) && no >= WWRF_MEM_SW_END) goto refuse;
    if ((row->flags & RF_ROW_NO_ZONE) && no >= WWRF_ZONE_SW_MIN) goto refuse;
    if ((row->flags & RF_ROW_CUR_ROOM) && no >= WWRF_ZONE_SW_MIN && RF_AF(a, s8, RF_AC_CUR_ROOM_OFF) != room)
        goto refuse;
    return no;
refuse:
    *result = WWRF_R_REFUSED;
    return -1;
}

// Step 6: the actor's own bit and its kind, or -1 with *result REFUSED for
// a kind this op never touches.
static int rf_derive(u16 proc, void* a, u32 params, s8 room, u8* kind, u32* result) {
    const rf_row* row;
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
    row = rf_row_of(proc);
    if (row) {
        *kind = WWRF_BIT_SW;
        return rf_derive_row(row, a, params, room, result);
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

// The pid of KOKIIE's hand i, 0 for none yet.
static u32 rf_kid(void* a, int i) {
    if (RF_AF(a, u8, RF_KOKIIE_KSTATE_OFF + i) == 0) return 0;
    return RF_AF(a, u32, RF_KOKIIE_KIDS_OFF + 4 * i);
}

// Step 9 for a row: NONE, IN_STATE, or GATE with *gate its id.
static u32 rf_row_gate(const rf_row* row, void* a, void* link, u32* gate) {
    u32 v = rf_field(a, row->st_off, row->st_w);
    int i;
    if (v < 32 && (row->done & RF_V(v))) return WWRF_R_IN_STATE;
    if (row->need_off && ((row->flags & RF_ROW_NEED32) ? RF_AF(a, u32, row->need_off) : RF_AF(a, u8, row->need_off)) == 0)
        return WWRF_R_IN_STATE;
    if (!(v < 32 && (row->idle & RF_V(v)))) return WWRF_R_GATE;
    if (row->far10) {
        f32 dx = RF_AF(link, f32, WWRF_AC_POS_OFF + 0) - RF_AF(a, f32, WWRF_AC_POS_OFF + 0);
        f32 dz = RF_AF(link, f32, WWRF_AC_POS_OFF + 8) - RF_AF(a, f32, WWRF_AC_POS_OFF + 8);
        f32 far = (f32)(row->far10 * 10);
        if (!(dx * dx + dz * dz > far * far)) {
            *gate = WWRF_GATE_LINK_NEAR;
            return WWRF_R_GATE;
        }
    }
    if (row->flags & RF_ROW_KIDS)
        for (i = 0; i < RF_KOKIIE_KIDS; i++) {
            u32 kid = rf_kid(a, i);
            if (kid && kid != fpcM_ERROR_PROCESS_ID_e && fpcM_IsCreating(kid)) return WWRF_R_GATE;
        }
    return WWRF_R_NONE;
}

// Step 9: WWRF_R_NONE when the proc's gate holds, else GATE (*gate its id),
// IN_STATE or FP_MISS.
static u32 rf_proc_gate(u16 proc, void* a, s8 room, void* link, u32* gate) {
    const rf_row* row;
    *gate = WWRF_GATE_PROC;
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
        if (RF_AF(a, u32, WWRF_TBOX_BG_CUR_OFF) == RF_AF(a, u32, WWRF_TBOX_BG_OPEN_OFF)) return WWRF_R_IN_STATE;
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
    row = rf_row_of(proc);
    return row ? rf_row_gate(row, a, link, gate) : WWRF_R_GATE;
}

static int rf_allowed(u16 proc) {
    return proc == WWRF_PROC_STONE2 || proc == WWRF_PROC_LEAVES || proc == WWRF_PROC_ITEM || proc == WWRF_PROC_TBOX ||
           proc == WWRF_PROC_SAKU || rf_row_of(proc) != 0;
}

static int rf_recreates(u16 proc) {
    const rf_row* row;
    if (proc == WWRF_PROC_TBOX) return 1;
    row = rf_row_of(proc);
    return row && (row->flags & RF_ROW_RECREATE);
}

// The delete, and for a re-create class the create after it ("Re-create"
// in the header): the result, with *pid the new actor's.
static u32 rf_apply(fopAc_ac_c* a, u32 pid, u16 proc, u32 params, s8 room, u32* pid_out) {
    void* layer = (void*)RF_AF(a, u32, RF_AC_LAYER_OFF);
    void* saved;
    cXyz pos, scale;
    csXyz angle;
    s8 arg;
    u32 kids[RF_KOKIIE_KIDS];
    int i, nkids = 0;
    *pid_out = 0;
    if (!rf_recreates(proc)) {
        // Queued, not run here (wwapi.h fopAcM_deleteByID): FALSE while the
        // process is creating or already deleting, which is no actor to take.
        return fopAcM_deleteByID(pid) ? WWRF_R_APPLIED : WWRF_R_NO_ACTOR;
    }
    if (!rf_in_mem1((u32)layer)) return WWRF_R_NO_ACTOR;
    pos = *(cXyz*)((u8*)a + RF_AC_HOME_POS_OFF);
    angle = *(csXyz*)((u8*)a + RF_AC_HOME_ANGLE_OFF);
    scale = *(cXyz*)((u8*)a + RF_AC_SCALE_OFF);
    arg = RF_AF(a, s8, RF_AC_ARGUMENT_OFF);
    if (proc == WWRF_PROC_KOKIIE)
        for (i = 0; i < RF_KOKIIE_KIDS; i++) {
            u32 kid = rf_kid(a, i);
            if (kid && kid != fpcM_ERROR_PROCESS_ID_e) kids[nkids++] = kid;
        }
    if (!fopAcM_deleteByID(pid)) return WWRF_R_NO_ACTOR;
    for (i = 0; i < nkids; i++) fopAcM_deleteByID(kids[i]);
    saved = rf_cur_layer();
    rf_set_layer(layer);
    *pid_out = fopAcM_create((s16)proc, params, &pos, room, &angle, &scale, arg, 0);
    rf_set_layer(saved);
    return *pid_out == fpcM_ERROR_PROCESS_ID_e ? WWRF_R_CREATE_FAILED : WWRF_R_RECREATED;
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
    u32 pid, params, r, st, gate, new_pid;
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
    bit = rf_derive(proc, a, params, room, &kind, &r);
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
    r = rf_proc_gate(proc, a, room, link, &gate);
    if (r != WWRF_R_NONE) {
        rf_ack(seq, r, r == WWRF_R_GATE ? gate : 0, st);
        return;
    }
    if (*(volatile u8*)WWRF_EVT_MODE != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_EVENT_MODE, st);
        return;
    }
    if (*(volatile s8*)WWRF_EVT_ORDER_COUNT != 0) {
        rf_ack(seq, WWRF_R_GATE, WWRF_GATE_EVENT_ORDER, st);
        return;
    }
    r = rf_apply(a, pid, proc, params, room, &new_pid);
    rf_ack_pid(seq, r, 0, st, new_pid);
#endif
}
