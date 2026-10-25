// The actor_refresh request in the mailbox slack (WWRF): the contract
// between Go (mods/coop/replica refresh.go, wwapi/mailbox/refresh.go) and
// the REL module (inject/rel/refresh.c). Design: ww-multiplayer-handoff/
// design/enemy-mirror-batch-d.json R-D4, R-D5, D4-D7, "actor_refresh";
// research/stale-actor-refresh.md Tier 3. When the partner's progress has
// set a bit this game had not (a broken boulder, a cut bush, a collected
// item, an opened chest, a burnt barricade), the actor this game placed
// before the bit landed is stale. Go asks the keeper to delete it; the
// REL re-derives the bit from the actor's own params or fields, re-reads
// it through the DOL's own save queries, runs the generic and per-proc
// gates, and only then calls fopAcM_delete. A reload then never creates
// it, as vanilla. Stateless: everything is this request's ack and result.
// Off by default (Go's --coop-refresh token set is empty), and the whole
// op compiles out with WWREFRESH_ENABLE 0.
//
// Shared by the REL and wwapi/mailbox/refresh.go (the Go mirror of every
// #define here, pinned by TestRefreshHeaderPins, which parses this file the
// way TestBossHeaderPins parses wwboss.h), so keep every #define on one
// line as `#define NAME value // comment`, a plain decimal or hex number:
// no expressions, no negatives.
//
// Where: the 0x20 bytes at MAILBOX_ADDR + 0x8B0 (0x804137B0), right after
// the enemy knob table (wwenemy.h WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE), in
// the slack that was free (+0x8B0..+0x1100, wwremote.h's slack map).
// P0-L5 armed canaries over it across ten stage hops and found every word
// intact (archive/enemy-batch-d/P0.md), so no game code writes it. The
// visuals block (wwvis.h, +0x8D0..+0x9E4) follows it, and the region
// probe's CanaryArenaHigh starts past that (+0x9E4).
//
//   Go-written, the seq last (two writes: +0x04..+0x14, then the seq):
//   +0x00 u32 seq     (Go bumps it for every request, never 0)
//   +0x04 u16 proc    (one of the WWRF_PROC_*; any other is REFUSED)
//   +0x06 u16 tag     (WWRF_TAG)
//   +0x08 u32 pid     (the actor Go found; the REL looks it up itself)
//   +0x0C u32 params  (its +0xB0, compared: MISMATCH)
//   +0x10 s8  room    (its home room +0x1E2, compared: MISMATCH)
//   +0x11 u8  bit kind (WWRF_BIT_SW, _TBOX or _ITEM)
//   +0x12 u16 bit no  (the bit Go decoded, compared with the REL's own
//                      derivation: MISMATCH; a Saku request names sw1)
//   REL-written, the result first:
//   +0x14 u32 ack     (the seq last consumed)
//   +0x18 u32 result: byte3 (+0x18) WWRF_R_*, byte2 (+0x19) the WWRF_GATE_*
//                     id for a GATE (0 otherwise), bytes 1..0 (+0x1A) the
//                     proc's state as found: Stone2 m648, Obj_Leaves 0,
//                     daItem mItemStatus << 8 | mFlag, Tbox mFlags, Saku
//                     state[0] << 8 | state[1] (each low byte)
//   +0x1C u32 reserved (0)
//
// The slack is not zeroed at boot, so the keeper's create consumes
// whatever seq is there (ack = seq, result WWRF_R_NONE) before it runs a
// request (refresh_link), as the boss_kill op does.
#ifndef WW_WWREFRESH_H
#define WW_WWREFRESH_H

#define WWREFRESH_ENABLE         1            // compile switch (D10): 0 leaves refresh_link/refresh_frame empty (the block stays reserved)

#define WWRF_OFF                 0x8B0        // request start, from MAILBOX_ADDR (0x804137B0): WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE
#define WWRF_SIZE                0x20         // ends at +0x8D0, where the visuals block starts (wwvis.h WWVIS_OFF)
#define WWRF_SLACK_END           0x1100       // the free slack's end (wwremote.h); the block must end at or below it

#define WWRF_SEQ_OFF             0x00         // u32 request seq, never 0 (Go)
#define WWRF_PROC_OFF            0x04         // u16 the actor's proc (Go)
#define WWRF_TAG_OFF             0x06         // u16 WWRF_TAG (Go)
#define WWRF_PID_OFF             0x08         // u32 the actor's process id (Go)
#define WWRF_PARAMS_OFF          0x0C         // u32 its base params +0xB0 (Go)
#define WWRF_ROOM_OFF            0x10         // s8 its home room +0x1E2 (Go)
#define WWRF_BIT_KIND_OFF        0x11         // u8 WWRF_BIT_* (Go)
#define WWRF_BIT_NO_OFF          0x12         // u16 the bit's number (Go)
#define WWRF_ACK_OFF             0x14         // u32 the seq last consumed (REL)
#define WWRF_RESULT_OFF          0x18         // u32 BE: byte3 WWRF_R_*, byte2 WWRF_GATE_* (GATE only), bytes 1..0 the proc's state (REL)
#define WWRF_RESERVED_OFF        0x1C         // u32 0
#define WWRF_GO_SPAN             0x14         // bytes Go writes: seq .. bit no

#define WWRF_TAG                 0x5246       // 'RF': with an allow-listed proc, what a request must carry

// Bit kinds (+0x11) and the DOL query the REL re-reads each with.
#define WWRF_BIT_SW              1            // a switch: isSwitch__10dSv_info_cFii(this, no, room)
#define WWRF_BIT_TBOX            2            // a room's chest bit: isTbox__12dSv_memBit_cFi(memBit, no)
#define WWRF_BIT_ITEM            3            // an item bit: isItem__10dSv_info_cFii(this, no, room)

// Results (byte3 of WWRF_RESULT_OFF).
#define WWRF_R_NONE              0            // consumed at the keeper's create (a stale or racing seq): ask again
#define WWRF_R_APPLIED           1            // every check passed: fopAcM_delete queued
#define WWRF_R_NO_ACTOR          2            // no actor of the pid, or one still creating: nothing done
#define WWRF_R_MISMATCH          3            // proc, params, room or the re-derived bit differ from the request: nothing done
#define WWRF_R_GATE              4            // a gate held (byte2 its WWRF_GATE_* id): nothing done, ask again later
#define WWRF_R_NOT_SET           5            // the DOL re-read the bit clear in this game: nothing done
#define WWRF_R_REFUSED           6            // a bad tag, a proc off the allow-list, or a kind this op never deletes (zone bit with no room, Tbox funcType 6..8, daItem bit 0x7F or Blue Jelly)
#define WWRF_R_FP_MISS           7            // Tbox: the Execute fingerprint did not match the retail d_a_tbox.rel

// Gate ids (byte2 with WWRF_R_GATE), in the order the REL checks them.
#define WWRF_GATE_STAGE_CHANGE   1            // NEXT_STAGE_ENABLE (wwapi.h)
#define WWRF_GATE_BOSS_STAGE     2            // mCurStage.mName M_DragB, kinBOSS, SirenB or X*
#define WWRF_GATE_INIT           3            // mInitState +0x0C != 2
#define WWRF_GATE_EVENT_CMD      4            // the actor's EvtCmd +0xF8 != 0
#define WWRF_GATE_CARRY          5            // actor_status CARRY (EMK_AC_STATUS_CARRY)
#define WWRF_GATE_MIRROR         6            // mPauseFlag +0x0B != 0, or the pid is in an enemy-mirror slot (WWENEMY_SL_PID_OFF x4)
#define WWRF_GATE_LINK_DEMO      7            // Link's mDemoType (dapy.h DAPY_DEMO_TYPE) != 0
#define WWRF_GATE_LINK_NEAR      8            // Link within WWRF_LINK_FAR of the actor's pos
#define WWRF_GATE_PROC           9            // the per-proc gate (below)
#define WWRF_GATE_EVENT_MODE     10           // the global event mode WWRF_EVT_MODE != 0

// The allow-list (f_pc_name.h). Nothing else is ever deleted: not Link
// 0xA9, the keeper/bodies 0xB5, a boss, a door, switch logic.
#define WWRF_PROC_LEAVES         0x0092       // fpcNm_Obj_Leaves_e (f_pc_name.h:155), "Vochi"
#define WWRF_PROC_ITEM           0x0101       // fpcNm_ITEM_e (f_pc_name.h:269), daItem_c
#define WWRF_PROC_TBOX           0x0126       // fpcNm_TBOX_e (f_pc_name.h:306), daTbox_c
#define WWRF_PROC_SAKU           0x0191       // fpcNm_SAKU_e (f_pc_name.h:413), "Ksaku", "Dsaku"
#define WWRF_PROC_STONE2         0x01CD       // fpcNm_Stone2_e (f_pc_name.h:473), "Ebrock2"

// Generic actor fields the gates read (f_op_actor.h, f_pc_base.h); params
// +0xB0, home room +0x1E2 and actor_status +0x1C4 are wwenemy_kinds.h's
// EMK_AC_*.
#define WWRF_AC_PROC_OFF         0x08         // s16 mProcName (f_pc_base.h)
#define WWRF_AC_PAUSE_OFF        0x0B         // u8 mPauseFlag
#define WWRF_AC_INIT_OFF         0x0C         // s8 mInitState: 2 = executing (f_pc_executor.cpp:49)
#define WWRF_AC_INIT_RUNNING     2
#define WWRF_AC_EVTCMD_OFF       0xF8         // u16 eventInfo mCommand (f_op_actor.h:202,276)
#define WWRF_AC_POS_OFF          0x1F8        // cXyz current.pos

// The DOL's own bit queries, this = g_dComIfG_gameInfo.info (0x803C4C08).
#define WWRF_SV_INFO             0x803C4C08   // dSv_info_c (g_dComIfG_gameInfo + 0)
#define WWRF_SV_MEMBIT           0x803C5380   // the current stage's dSv_memBit_c (d_save.h:992)
#define WWRF_IS_SWITCH_FN        0x8005DE98   // isSwitch__10dSv_info_cFii (symbols.txt)
#define WWRF_IS_TBOX_FN          0x8005C188   // isTbox__12dSv_memBit_cFi
#define WWRF_IS_ITEM_FN          0x8005E324   // isItem__10dSv_info_cFii
#define WWRF_ZONE_SW_MIN         0xC0         // isSwitch: bits >= this are zone bits, which need a room >= 0 (d_save.cpp:1585-1604); with room < 0 REFUSED
#define WWRF_EVT_MODE            0x803C9EA2   // u8 dEvt mMode: 0x803C4C08 + 0x12A0 + 0x3F38 + 0xC2 (d_com_inf_game.h:728,915; d_event.h:154)
#define WWRF_LINK_FAR            300          // units: Link must be farther than this from the actor (D4; Saku and Tbox release BG on delete)

// Stone2 (d_a_stone2.h): bit = (params >> 8) & 0xFF (:50-51); gate s32
// m648 == 0, mode_wait (:138). damaged() is never called: it drops an item.
#define WWRF_STONE2_SW_SHIFT     8
#define WWRF_STONE2_SW_MASK      0xFF
#define WWRF_STONE2_M648_OFF     0x648        // s32 m648: 0 idle, 1 lifted (P0-RF)

// Obj_Leaves (d_a_obj_leaves.h:22-29; chk_appear .cpp:158-165): bit =
// (params >> 13) & 0xFF; no gate beyond the generic ones.
#define WWRF_LEAVES_SW_SHIFT     13
#define WWRF_LEAVES_SW_MASK      0xFF

// daItem (d_a_itembase.h:91-94, d_a_item.h:14-25,31-34,121-123; d_a_item
// .cpp:227-233): bit = mItemBitNo & 0x7F, read with isItem; REFUSED for bit
// 0x7F and Blue Jelly; gate mItemStatus <= WWRF_ITEM_STATUS_WAIT_MAX and no
// boomerang/hook. A pickup lying in the world is STATUS_UNK0 (create :160)
// or UNK1 (moving, checkActionNow), both running execWaitMain (:275-284);
// P0-RF read 0 on the M_NewD2 r0 rupee. Everything above is held: 2
// setLock (d_a_item_static.cpp:58-62), 3 a rat carrying it, 5/6 the
// pickup flight over the local Link's head (itemGetExecute :587-590,
// execMainNormalDirection :381-397, which deletes it itself), 7..9 the get
// demo, 0xA/0xB a boss drop.
#define WWRF_ITEM_BITNO_OFF      0x630        // s32 mItemBitNo
#define WWRF_ITEM_BITNO_MASK     0x7F
#define WWRF_ITEM_BIT_NONE       0x7F         // no bit: REFUSED
#define WWRF_ITEM_NO_OFF         0x63A        // u8 m_itemNo
#define WWRF_ITEM_NO_BLUE_JELLY  0x4B         // REFUSED
#define WWRF_ITEM_FLAG_OFF       0x669        // u8 mFlag
#define WWRF_ITEM_FLAG_BUSY      0x48         // FLAG_BOOMERANG | FLAG_HOOK: GATE
#define WWRF_ITEM_STATUS_OFF     0x66B        // u8 mItemStatus
#define WWRF_ITEM_STATUS_WAIT_MAX 1           // STATUS_UNK1: the highest status the gate takes (0 at rest, 1 moving)

// Tbox (d_a_tbox.h:35,102,104; d_a_tbox.cpp:29-31,314-320): funcType =
// params & 0x7F, REFUSED for 6..8; bit = (params >> 7) & 0x1F on the
// stage's memBit (isTbox). Gate, all of: BG closed (mpBgWCurrent !=
// mpBgWOpen), mFlags & BUSY clear, and the PTMF's function word one of the
// three wait actions at exec - TEXT_EXEC + TEXT_*, exec being the copy's
// daTbox_Execute behind the fingerprint (else FP_MISS; rels/d_a_tbox/
// symbols.txt:44-51; P0-RF matched it live).
#define WWRF_TBOX_FUNC_MASK      0x7F
#define WWRF_TBOX_FUNC_REFUSE_LO 6            // funcTypes 6, 7, 8: REFUSED
#define WWRF_TBOX_FUNC_REFUSE_HI 8
#define WWRF_TBOX_BIT_SHIFT      7
#define WWRF_TBOX_BIT_MASK       0x1F
#define WWRF_TBOX_BG_OPEN_OFF    0x2BC        // dBgW* mpBgWOpen
#define WWRF_TBOX_BG_CUR_OFF     0x2C4        // dBgW* mpBgWCurrent
#define WWRF_TBOX_ACT_FN_OFF     0x348        // ActionFunc mActionFunc (+0x340), its function word
#define WWRF_TBOX_FLAGS_OFF      0x3F0        // u16 mFlags
#define WWRF_TBOX_FLAGS_BUSY     0x38         // UNK_08, OPENING, APPEARING
#define WWRF_TBOX_TEXT_EXEC      0x2FB0       // daTbox_Execute .text offset (symbols.txt:51)
#define WWRF_TBOX_TEXT_OPEN_WAIT 0x2634       // actionOpenWait (:44)
#define WWRF_TBOX_TEXT_SW_WAIT   0x27C8       // actionSwOnWait (:45)
#define WWRF_TBOX_TEXT_SW_WAIT2  0x28A0       // actionSwOnWait2 (:46)
#define WWRF_TBOX_FP_OFF0        0x0C         // exec + this
#define WWRF_TBOX_FP_WORD0       0x4BFFFA71
#define WWRF_TBOX_FP_OFF1        0x1C         // exec + this: its blr
#define WWRF_TBOX_FP_WORD1       0x4E800020

// Saku (d_a_saku.h:137-146; d_a_saku.cpp:344-381), v1 delete-only (D7):
// sw1 = (params >> 8) & 0xFF, sw2 = (params >> 16) & 0xFF; half 1 exists
// only when params & 0xF != 0. Gate: not burning, no half in state 2, and
// every standing half (state 1) reads its own switch set. One-half
// barricades stay stale (v2: the retail broken(i), still_open S3).
#define WWRF_SAKU_SW1_SHIFT      8
#define WWRF_SAKU_SW2_SHIFT      16
#define WWRF_SAKU_SW_MASK        0xFF
#define WWRF_SAKU_HALF1_MASK     0xF          // params & this != 0: half 1 exists
#define WWRF_SAKU_BURN_OFF       0xEF4        // u8: != 0 burning (GATE)
#define WWRF_SAKU_STATE_OFF      0xEF8        // s32[2] per half: 0 none, 1 standing, 2 burning (GATE), 3 broken
#define WWRF_SAKU_STATE_STANDING 1
#define WWRF_SAKU_STATE_BURNING  2
#define WWRF_SAKU_STATE_BROKEN   3
#define WWRF_SAKU_SW_OFF         0xF00        // u32[2] the halves' switches as the actor copied them (P0-RF: equal to the params bytes)

#endif
