// The boss_kill request in the mailbox slack (WWBK): the contract between
// Go (mods/coop/replica bosskill.go, wwapi/mailbox/boss.go) and the REL
// module (inject/rel/bosskill.c). Design: ww-multiplayer-handoff/design/
// research/C3-C6-gohma.md, "The relay op (B5, revised)": when the partner
// has killed Gohma and this game is still in its own fight, Go asks the
// keeper to give this game's Btd the death a lethal eye hit gives it
// (d_a_btd.cpp:463-475), so the death demo, the floor drop, the heart
// container, STAGE_BOSS_ENEMY and the warp flower all run in this game,
// in vanilla order. Nothing is deleted.
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
//   +0x04 u16 proc  (WWBOSS_PROC_BTD; any other proc is refused)
//   +0x06 u16 tag   (WWBOSS_TAG)
//   REL-written, the result first:
//   +0x08 u32 ack   (the seq last consumed)
//   +0x0C u32 result: byte3 (+0x0C) WWBOSS_R_*, byte2 (+0x0D) the low
//                     byte of Btd's m02E4 as found, byte1 (+0x0E) the low
//                     byte of its mGohmaState as found, byte0 (+0x0F) its
//                     m02E0 (the armor phase) as found, 0 with no Btd
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
#define WWBOSS_RESULT_OFF        0x0C         // u32 BE: byte3 WWBOSS_R_*, byte2 m02E4 low byte, byte1 mGohmaState low byte, byte0 m02E0 (REL)
#define WWBOSS_GO_SPAN           0x08         // bytes Go writes: seq, proc, tag

#define WWBOSS_TAG               0x424B       // 'BK': with the proc, what a request must carry
#define WWBOSS_PROC_BTD          0x00EB       // fpcNm_BTD_e, Gohma (f_pc_name.h:247)

// Results (byte3 of WWBOSS_RESULT_OFF).
#define WWBOSS_R_NONE            0            // consumed at the keeper's create (a stale or racing seq): ask again
#define WWBOSS_R_APPLIED         1            // m02E4 was 0, 1 or 2 with m02E0 1: m02E4 = 0xB, mGohmaState = 0x32 written
#define WWBOSS_R_WAITING         2            // m02E4 0xA (hidden or in its intro), the armor intact (m02E0 not 1) or another state: nothing written, ask again
#define WWBOSS_R_ALREADY         3            // m02E4 0xB: already dying or dead, nothing written
#define WWBOSS_R_NO_ACTOR        4            // no Btd, or one still creating: nothing written
#define WWBOSS_R_GATE            5            // a stage change pending, the stage not M_DragB (an X refight stage included), or a bad proc or tag

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

#endif
