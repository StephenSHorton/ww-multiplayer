// The boss_kill request in the mailbox slack (WWBK): the contract between
// Go (mods/coop/replica bosskill.go, wwapi/mailbox/boss.go) and the REL
// module (inject/rel/bosskill.c). Design: ww-multiplayer-handoff/design/
// research/C3-C6-gohma.md, "The relay op (B5, revised)": when the partner
// has killed Gohma and this game is still in its own fight, Go asks the
// keeper to give this game's Btd the death a lethal eye hit gives it
// (d_a_btd.cpp:463-475), so the death demo, the floor drop, the heart
// container, STAGE_BOSS_ENEMY and the warp flower all run in this game,
// in vanilla order. Nothing is deleted. Kalle Demos (Bmd, kinBOSS) is the
// second boss the op takes (research/C1-kalle-demos-relay.md): its own
// core_damage_check lethal block (d_a_bmd.cpp:609-617) is written, so
// end() sets STAGE_BOSS_ENEMY and creates the warp flower, and the head
// death the heart container and Makar, all in this game. Gohdan (Bst,
// SirenB) is the third (enemy-mirror-batch-d.json R-D1..R-D3, "gohdan"):
// from vanilla's own pre-death state (the mouth open for a bomb, both
// hands asleep) the keeper writes bom_eat_check's lethal block
// (d_a_bst.cpp:1219-1229) on the head and both hands, so end_demo's camera
// cases 50-57 set STAGE_BOSS_ENEMY, create the warp flower and drop the
// heart container in this game. Nothing is deleted.
//
// Shared by the REL and wwapi/mailbox/boss.go (the Go mirror of every
// #define here, pinned by TestBossHeaderPins, which parses this file the
// way TestRemoteHeaderPins parses wwremote.h), so keep every #define on
// one line as `#define NAME value // comment`, a plain decimal or hex
// number: no expressions, no negatives.
//
// Where: the 16 bytes at MAILBOX_ADDR + 0x510 (0x80413410), between the
// enemy region's end (WWENEMY_REGION_END) and the remote-Link block
// (WWREMOTE_OFF). They were the alignment gap at the head of the
// remote-Link region (wwremote.h's slack map), which neither remote-Link
// side ever wrote and the canary probe never arms (wwapi/mailbox
// RemoteBlockRegion). The hit_apply request it sits beside (wwenemy.h
// WWENEMY_HIT_APPLY_OFF) is part of the enemy block, packed to its
// seq_tail with no free word.
//
//   Go-written, in two writes, the seq last:
//   +0x00 u32 seq   (Go bumps it for every request, never 0)
//   +0x04 u16 proc  (WWBOSS_PROC_BTD, WWBOSS_PROC_BMD or WWBOSS_PROC_BST;
//                    any other proc is refused)
//   +0x06 u16 tag   (WWBOSS_TAG)
//   REL-written, the result first:
//   +0x08 u32 ack   (the seq last consumed)
//   +0x0C u32 result: byte3 (+0x0C) WWBOSS_R_*, then per proc, each as
//                     found and 0 with no actor:
//                     Btd: byte2 (+0x0D) m02E4's low byte, byte1 (+0x0E)
//                     mGohmaState's low byte, byte0 (+0x0F) m02E0 (the
//                     armor phase);
//                     Bmd: byte2 mMode's low byte, byte1 m302's low byte,
//                     byte0 m304's low byte;
//                     Bst: byte2 the head's mActionType low byte, byte1
//                     its mDamage low byte, byte0 its m2E7C
//
// The slack is not zeroed at boot, so the keeper's create consumes
// whatever seq is there (ack = seq, result WWBOSS_R_NONE) before it ever
// runs a request: a stale seq from an earlier boot is never executed, and
// Go reads ack == its seq with WWBOSS_R_NONE as "swallowed at link" and
// asks again. A save state restores the request and its ack together.
#ifndef WW_WWBOSS_H
#define WW_WWBOSS_H

#define WWBOSS_OFF               0x510        // request start, from MAILBOX_ADDR (0x80413410)
#define WWBOSS_SIZE              0x10         // ends at +0x520 = WWREMOTE_OFF

#define WWBOSS_SEQ_OFF           0x00         // u32 request seq, never 0 (Go)
#define WWBOSS_PROC_OFF          0x04         // u16 the boss's proc (Go)
#define WWBOSS_TAG_OFF           0x06         // u16 WWBOSS_TAG (Go)
#define WWBOSS_ACK_OFF           0x08         // u32 the seq last consumed (REL)
#define WWBOSS_RESULT_OFF        0x0C         // u32 BE: byte3 WWBOSS_R_*, bytes 2..0 per proc: Btd m02E4, mGohmaState, m02E0; Bmd mMode, m302, m304; Bst head mActionType, mDamage, m2E7C (REL)
#define WWBOSS_GO_SPAN           0x08         // bytes Go writes: seq, proc, tag

#define WWBOSS_TAG               0x424B       // 'BK': with the proc, what a request must carry
#define WWBOSS_PROC_BTD          0x00EB       // fpcNm_BTD_e, Gohma (f_pc_name.h:247), only in M_DragB
#define WWBOSS_PROC_BMD          0x00EC       // fpcNm_BMD_e, Kalle Demos (f_pc_name.h:248), only in kinBOSS
#define WWBOSS_PROC_BST          0x00F2       // fpcNm_BST_e, Gohdan (f_pc_name.h:254), only in SirenB (batch D R-D1)

// Results (byte3 of WWBOSS_RESULT_OFF).
#define WWBOSS_R_NONE            0            // consumed at the keeper's create (a stale or racing seq): ask again
#define WWBOSS_R_APPLIED         1            // Btd: m02E4 was 0, 1 or 2 with m02E0 1, m02E4 = 0xB, mGohmaState = 0x32 written; Bmd: the flower down and open, the lethal block written; Bst: the mouth open for a bomb, the hands asleep, the lethal block written
#define WWBOSS_R_WAITING         2            // Btd: m02E4 0xA (hidden or in its intro), the armor intact (m02E0 not 1) or another state; Bmd: not the open flower; Bst: any state but the APPLIED gate's; nothing written, ask again
#define WWBOSS_R_ALREADY         3            // m02E4 (Btd) or mMode (Bmd) 0xB, Bst head mActionType 0x16, m2E9A >= 0x32 or m2E7C 100: already dying or dead, nothing written
#define WWBOSS_R_NO_ACTOR        4            // no actor of the proc, or one still creating (Bst: any of the three parts, or the head's morf NULL): nothing written
#define WWBOSS_R_GATE            5            // a stage change pending, not the proc's stage (an X refight stage included), or a bad proc or tag

// Btd (d_a_btd.h:44-45, btd_class).
#define WWBOSS_BTD_M02E0_OFF     0x2E0        // u8 the armor phase: 0 armored, 1 broken (d_a_btd.cpp:833), 2 in the death demo (:980)
#define WWBOSS_BTD_M02E4_OFF     0x2E4        // s16 the action dispatched by daBtd_Execute (d_a_btd.cpp:1740-1764)
#define WWBOSS_BTD_STATE_OFF     0x2E6        // s16 mGohmaState
#define WWBOSS_BTD_ACT_INTRO     0xA          // startdemo (d_a_btd.cpp:1758-1760, set at create :3003)
#define WWBOSS_BTD_ACT_END       0xB          // end (d_a_btd.cpp:1762-1764)
#define WWBOSS_BTD_STATE_DEAD    0x32         // end's first case (d_a_btd.cpp:905)
#define WWBOSS_BTD_M02F6_OFF     0x2F6        // u8 Dr2's rock handshake (d_a_btd.h:54)
#define WWBOSS_BTD_PHASE_BARE    1            // m02E0 with the armor broken: the only phase the lethal eye hit takes (d_a_btd.cpp:457)
#define WWBOSS_BTD_ROCK_LANDED   1            // m02F6 set by Dr2's landing rock, before damage_check takes it (d_a_dr2.cpp:311, d_a_btd.cpp:445-451)

// Bmd (d_a_bmd.h:31-100, bmd_class), research/C1-kalle-demos-relay.md.
// The gate is the fallen, open flower: mMode 3 at m302 2..5 with m304 not
// 0, m310 0 and mB74 0 (m304 alone can be stale at m302 0..1, bosskill.c).
// The applied writes are core_damage_check's lethal
// block (d_a_bmd.cpp:609-617) without the hit pause and the SE; mBDC is
// written as 1.0f in bosskill.c, not pinned here.
#define WWBOSS_BMD_MODE_OFF      0x300        // s16 mMode, dispatched by move() (d_a_bmd.cpp:1064-1084)
#define WWBOSS_BMD_M302_OFF      0x302        // s16 m302, the mode's step
#define WWBOSS_BMD_M304_OFF      0x304        // s16 m304, nonzero with the core out (d_a_bmd.cpp:164, :209, :239)
#define WWBOSS_BMD_M306_OFF      0x306        // s16 m306, core_move's head state (d_a_bmd.cpp:652-765)
#define WWBOSS_BMD_M310_OFF      0x310        // s16 m310, core_damage_check returns while nonzero (d_a_bmd.cpp:580)
#define WWBOSS_BMD_MB74_OFF      0xB74        // s16 mB74, the demo camera's state (d_a_bmd.cpp:1419-1538)
#define WWBOSS_BMD_MBDC_OFF      0xBDC        // f32 mBDC, the room light fade (d_a_bmd.cpp:1556-1560)
#define WWBOSS_BMD_MBE0_OFF      0xBE0        // u8 mBE0, the room light fade's state
#define WWBOSS_BMD_MODE_FALLEN   3            // damage(): the flower down (d_a_bmd.cpp:140-312, entered at :914-920)
#define WWBOSS_BMD_STEP_LANDED   2            // damage()'s first m302 after the landing, after the m332 = 3 fall signal (d_a_bmd.cpp:158-166)
#define WWBOSS_BMD_MODE_INTRO    10           // start(), the intro (set at create, d_a_bmd.cpp:2014-2019)
#define WWBOSS_BMD_MODE_END      0xB          // end() (d_a_bmd.cpp:1082-1084)
#define WWBOSS_BMD_M306_DEAD     100          // the head death (d_a_bmd.cpp:609)
#define WWBOSS_BMD_MB74_DEATH    100          // the death camera's event (d_a_bmd.cpp:612)
#define WWBOSS_BMD_M310_DEAD     20000        // core_damage_check shut (d_a_bmd.cpp:614)
#define WWBOSS_BMD_MBE0_DEAD     2            // the light fade's death case (d_a_bmd.cpp:615)

// mDoAud_bgmStop(30) (d_a_bmd.cpp:617): zel_basic->bgmStop(30, 0)
// (m_Do_audio.h:107-109, JAIZelBasic.h:179).
#define WWBOSS_BGM_STOP_FN       0x802A4658   // bgmStop__11JAIZelBasicFUll (symbols.txt:10635)
#define WWBOSS_ZEL_BASIC         0x803F7710   // zel_basic__11JAIZelBasic, a JAIZelBasic* (symbols.txt:19554)
#define WWBOSS_BGM_STOP_FADE     30           // the fade vanilla passes

// Bst (d_a_bst.h, bst_class), Gohdan: batch D R-D1..R-D3, "gohdan". SirenB
// places three proc 0xF2 actors, the head (part 0) and two hands (1, 2);
// boss/hand[] are statics in d_a_bst.rel's .bss (d_a_bst.cpp:44-45), so
// bk_bst finds each part through fopAcIt_Judge with a REL judge callback
// (bk_bst_judge: proc 0xF2, part == *want, mInitState 2 and not
// fpcM_IsCreating, as f_op_actor_mng.cpp:65-74). The APPLIED gate is
// vanilla's own pre-death state, all of: head mActionType HEAD_DAMAGE and
// mDamage MOUTH_OPEN (KUTI_OPEN_LOOP, d_a_bst.cpp:1159-1166), m2E7C EYES,
// m2E9A 0, both hands mActionType SLEEP (case 12 holds them,
// :1897-1905). The writes, in one keeper frame and this order: head m2E9A
// = M2E9A_DEATH, head mDamage = 0, the DEAD anim (WWBOSS_BST_ANIM:
// getRes("Bst", ANM_DEAD, OBJINFO, OBJINFO_COUNT), then on a non-NULL res
// setAnm(morf, res, 0, 1.0f, 1.0f, 0.0f, -1.0f, NULL), anm_init's args,
// :111-120, :1222), each hand mActionType END_DEMO and mDamage
// HAND_DMG_END (:1223-1226), the head mActionType END_DEMO last, then
// bgmStop(zel, 30, 0) and bgmStreamPrepare(zel, STRM_BST_CLEAR) (:1228;
// demo case 51 plays it, :2396). health is not decremented and nothing is
// deleted (R-D3).
#define WWBOSS_WITH_BST          1            // compile switch (D10): 0 builds the op without Bst (bk_stage_ok and bosskill_frame refuse proc 0xF2 as any other)
#define WWBOSS_BST_ANIM          1            // compile switch (D3, cut_order 5): 0 skips the DEAD anim's getRes + setAnm; the relay still runs
#define WWBOSS_BST_PART_OFF      0x2B4        // u8 mBstPartType (d_a_bst.h:38; retail lbz 0x2b4 at d_a_bst.rel .text 0x5EFC/0xA2AC)
#define WWBOSS_BST_PART_HEAD     0            // the head
#define WWBOSS_BST_PART_HAND1    1            // part 1, a hand
#define WWBOSS_BST_PART_HAND2    2            // part 2, the other hand
#define WWBOSS_BST_MORF_OFF      0x2B8        // mDoExt_McaMorf* (d_a_bst.h): NULL on the head is NO_ACTOR
#define WWBOSS_BST_ACT_OFF       0x10D6       // s16 mActionType (d_a_bst.h:63)
#define WWBOSS_BST_DMG_OFF       0x10DA       // s16 mDamage (d_a_bst.h:65)
#define WWBOSS_BST_M2E7C_OFF     0x2E7C       // s8 m2E7C, the fight's phase (d_a_bst.h:101)
#define WWBOSS_BST_M2E9A_OFF     0x2E9A       // s8 m2E9A, the death count (d_a_bst.h:112)
#define WWBOSS_BST_ACT_SLEEP     6            // ACTION_SLEEP_e (d_a_bst.h:20-34)
#define WWBOSS_BST_ACT_HEAD_DMG  7            // ACTION_HEAD_DAMAGE_e
#define WWBOSS_BST_ACT_END_DEMO  0x16         // ACTION_END_DEMO_e: end_demo (d_a_bst.cpp:1604)
#define WWBOSS_BST_DMG_MOUTH     3            // KUTI_OPEN_LOOP, waiting for a bomb (d_a_bst.cpp:1159-1166); 2 is the KUTI_OPEN one-shot (D2)
#define WWBOSS_BST_M2E7C_EYES    12           // both hands asleep, the eyes open (d_a_bst.cpp:1888-1905)
#define WWBOSS_BST_M2E7C_DONE    100          // after the demo (d_a_bst.cpp:2534)
#define WWBOSS_BST_M2E9A_DEATH   0x32         // the lethal block's write (d_a_bst.cpp:1220)
#define WWBOSS_BST_HAND_DMG_END  0xA          // each hand's mDamage (d_a_bst.cpp:1224)
#define WWBOSS_BST_ANM_DEAD      0xB          // BST_BCK_DEAD (res/Object/Bst.h:16)
#define WWBOSS_STRM_PREPARE_FN   0x802A334C   // bgmStreamPrepare__11JAIZelBasicFUl (symbols.txt:10632), on WWBOSS_ZEL_BASIC
#define WWBOSS_STRM_BST_CLEAR    0xC0000019   // JA_STRM_BST_CLEAR (JAZelAudio_BGM.h:133; retail lis/addi at d_a_bst.rel .text 0x473C)
#define WWBOSS_GETRES_FN         0x8006F208   // dRes_control_c::getRes(const char*, s32, dRes_info_c*, int) (symbols.txt:2309)
#define WWBOSS_OBJINFO           0x803E0BC8   // mObjectInfo: 0x803C4C08 + mResControl 0x1BFC0 (d_com_inf_game.h:918; d_resorce.h:119)
#define WWBOSS_OBJINFO_COUNT     64           // its entries, getRes's last argument (0x40)
#define WWBOSS_SETANM_FN         0x80012D78   // mDoExt_McaMorf::setAnm (symbols.txt:318)
#define WWBOSS_ITER_JUDGE_FN     0x80023FB4   // fopAcIt_Judge(judge, data) (symbols.txt:632; f_op_actor_iter.cpp:18-23)

#endif
