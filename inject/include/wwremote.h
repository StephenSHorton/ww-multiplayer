// The remote-Link block in the mailbox slack (WWRP): the contract between
// Go (mods/enemysync players.go, wwapi/mailbox/remote.go) and the REL
// module (inject/rel: the keeper's swap/latch/body lifecycle/client apply
// and the lite-body role of the 0xB5 actor). Design:
// ww-multiplayer-handoff/design/remote-link-design.md sections 6 (mailbox),
// 5.2 (lite body), 8 (wire v3) and its "Step -1 results", which override
// the inferred values (isCollect is a direct byte read; the Link cyl
// offsets, the mo2 spear spheres and their At values are peeked).
//
// Shared by the REL (reader/writer in the game) and wwapi/mailbox/remote.go
// (the Go mirror of every #define here, pinned by TestRemoteHeaderPins,
// which parses this file the way scripts/extract_blob.py parses wwrel.h),
// so keep every #define on one line as `#define NAME value // comment`,
// a plain decimal or hex number: no expressions, no negatives.
//
// Slack map (mailbox-relative; MAILBOX_ADDR 0x80412F00, __OSArenaLo
// 0x80414000, inject/build.py):
//
//   +0x000..+0x1B0  Mailbox struct (mailbox_core.h)
//   +0x1B0..+0x1BC  REL words: magic, build id, frame (wwrel.h)
//   +0x1BC..+0x1C4  REL words: WWREL_EXEC_HOOK_OFF (T2's exec_shim
//                   branches to it), WWREL_SWAP_STATE_OFF (wwrel.h); never
//                   canary-armed
//   +0x1C4..+0x1C8  WWREL_SHANM_OFF (wwrel.h): the full bodies' shanm table
//                   (an archive-heap block, once per boot; T2 zeroes it at
//                   boot, the REL never clears it); never canary-armed
//   +0x200..+0x510  enemy-mirror block + v2 sphere/stts (wwenemy.h)
//   +0x510..+0x520  the boss_kill request (wwboss.h; co-op), inside the
//                   remote region, which never writes it
//   +0x520..+0x850  this block (WWREMOTE_OFF, WWREMOTE_SIZE)
//   +0x850..+0x8B0  the enemy knob table (wwenemy.h WWENEMY_KNOB_OFF, layout 4)
//   +0x8B0..+0x8D0  the actor_refresh request (wwrefresh.h WWRF_OFF; batch D)
//   +0x8D0..+0x1100 free (wwapi/mailbox CanaryArenaHigh)
//
// The block (offsets from WWREMOTE_OFF; absolute = 0x80413420 + off):
//
//   Go-written span +0x000..+0x1EC, one write per tick, seq_head == seq_tail
//   +0x000 u32 magic 'WWRP' (0 = off: vanilla-equivalent, the REL creates
//              no body and swaps nothing; only hook_calls still counts)
//   +0x004 u32 seq_head            +0x008 u16 layout (1)
//   +0x00A u16 flags (WWREMOTE_FLAG_*)   +0x00C u32 epoch
//   +0x010 u8 margin_q8  +0x011 u8 min_hold  +0x012 u8 body_iframes
//   +0x013 u8 role (1 host, 2 client: must equal the enemy block's role)
//   +0x014 f32 dy_max
//   +0x018 u8 proc_policy_count  +0x019 u8 ff_deny_count
//   +0x01A u16 create_cooldown
//   +0x01C u8 full_allow (bit i: slot i may begin one full create)
//   +0x01D u8 heap_reason (WWREMOTE_HG_*)  +0x01E u16 flags2 (WWREMOTE_FLAG2_*)
//              (the word was reserved, 0; 0 in all three is today's behaviour)
//   +0x020 slot[4] x 0x30 (runtime uses MAX_REMOTE_LINKS = 2)
//   +0x0E0 client apply record, 0x28
//   +0x108 char[8] x 4 FF stage denylist
//   +0x128 PROC_POLICY[48] x 4: s16 proc, u8 WWREMOTE_PF_*, u8 pad
//   +0x1E8 u32 seq_tail (the last Go-written word)
//   +0x1EC u32 unused (neither side writes it)
//   REL-written span +0x1F0..+0x330
//   +0x1F0 u32 ack_seq (the last valid seq the REL consumed)
//   +0x1F4 u32 WWREL frame at that consume
//   +0x1F8 body_status[4] x 0x10 (+0x0F kind: WWREMOTE_KIND_*, was pad)
//   +0x238 counters u32[10], cumulative (WWREMOTE_CTR_* index)
//   +0x260 u32 hit_ring_head   +0x264 hit_ring[4] x 0x30
//   +0x324 u32 apply_ack (the apply seq consumed)
//   +0x328 u32 apply_result: byte3 WWREMOTE_AR_*, byte2 WWREMOTE_AD_*,
//              byte1 executes from Set to result, byte0 Sets issued
//              (packed like WWENEMY_HIT_RESULT_OFF)
//   +0x32C u16 life_before, +0x32E u16 life_after (client Link life)
//
// Validity (as the enemy block): magic + layout + role + seq_head ==
// seq_tail; stale (bodies park, then delete; swaps stop) after
// WWREMOTE_STALE_FRAMES REL frames without a new seq. The slack is not
// zeroed at boot; Go writes magic 0 when it stops. The REL writes the
// counters (hook_calls first) whatever the magic says, so Step 0 can see
// the hook run with the block off.
//
// Flags. Every bit defaults OFF and OFF is today's behaviour, so the two
// safety gates are opt-OUTs: WWREMOTE_FLAG_FF_GATE_OFF and
// WWREMOTE_FLAG_ROUYA_GATE_OFF (the design's NO_FF / ROUYA "default on",
// same bit positions, inverted) disable the FF denylist gate and the jail
// gate; clear means the gate runs. Clearing BODIES or SWAP returns to
// today's behaviour within one frame. ATTRIB's force latch is honoured only
// with ATTACKER also set. FULL_BODIES (host, with BODIES) makes a slot's
// body the real daPy_lk_c (the proc 0x27 profile) when Go's heap gate set
// the slot's FULL_ALLOW bit; clear, every body is the lite one, exactly as
// before. WWREMOTE_LAYOUT stays 1: FULL_ALLOW, HEAP_REASON and FLAGS2 at 0,
// and body_status kind 0, are today's behaviour.
//
// Jail gate (Step -1): isCollect(0, i) is bit i of mCollect[0] (d_save.cpp:
// 756-759) at WWREMOTE_COLLECT_ADDR (wwapi/addr Collect); mo2's jail mode
// is on when none of bits 0..3 is set (d_a_mo2.cpp:4134-4143), so the REL
// skips the swap when (*(u8*)WWREMOTE_COLLECT_ADDR &
// WWREMOTE_COLLECT_JAIL_MASK) == 0. No collect pointer is in the block.
//
// Slot sflags (WWREMOTE_SF_*) are Go-derived, not a copy of the wire byte:
// PSTATE carries the client's own state bits (WWREMOTE_PS_*); Go sets
// TARGETABLE only for an in-play remote in the host's stage and room, and
// the rest from PSTATE and the pose feed.
//
// Hit ring: hit_ring_head counts pushes (0 = none yet). Push n (1-based)
// goes to entry (n - 1) % WWREMOTE_HIT_RING_LEN with seq = (u16)n; Go
// reads the entries whose seq is newer than the last it saw.
//
// Lite body (host only, section 5.2): a second role of the 0xB5 actor,
// mParameters WWREMOTE_BODY_PARAMS_TAG | slot << 8 | gen (the keeper is
// params 0); profile mSize WWREMOTE_ACTOR_SIZE for both roles. The keeper's
// swap state starts at WWREMOTE_KEEPER_SWAP_OFF and ends at or before
// WWREMOTE_ACTOR_SIZE (static-asserted in the REL); WWREL_SWAP_STATE_OFF
// points at it. WWREMOTE_BODY_* offsets are from the body actor.
//
// Full body (FLAG_FULL_BODIES; ww-multiplayer-handoff/design/
// remote-link-stage4-plan.md D4-1, D4-6): a daPy_lk_c of the second profile
// g_profile_WWBODY in the same REL, profile slot WWREMOTE_FULL_PROC (0x27,
// Seatag) aliased to module 0x58 by a DOL name-table patch and a RELS.arc
// profile-list patch; mProcName 0xB5, mSize WWREMOTE_FULL_SIZE (daPy_lk_c
// plus a 0x58 tail at WWREMOTE_FULL_TAIL_OFF whose first 0x48 bytes keep
// the lite body's +0x530..+0x578 field order). WWREMOTE_FULL_* offsets are
// from the body actor. The shanm table (WWREMOTE_SA_* header) lives at
// *(u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF).
//
// Wire v3 (Go only: mods/multiplayer/net/enemy.go checks the shapes,
// mods/enemysync/wire.go the fields; WireVersion 2 -> 3, so old and new
// builds refuse each other; big-endian; encoders refuse NaN/Inf):
//
//   HIT    sub 2, client -> host, 48 -> 52 bytes: [0..45] as v2, [46] u8
//          cut_type, [47] u8 cut_count (were pad), [48] u8 player id
//          (server-stamped, 0 = unmapped), [49..51] pad.
//   PHIT   sub 4, host -> client, 60 bytes, routed by [2] like ACK:
//          [0] sub [1] ver [2] target enemy client id [3] flags (b0 host
//          shield-hit, b1 host guard flag) [4] u32 epoch [8] char[8] stage
//          [16] s8 room [17] u8 atp [18] u16 attacker setID [20] u16 seq
//          [22] u8 se [23] u8 hitmark [24] u32 attype [28] u8 spl [29..31]
//          pad [32] f32 rel_hit xyz [44] f32 rvec xyz [56] u32 host frame.
//   PACK   sub 5, client -> host, 16 bytes, [2] server-stamped client id:
//          [3] result (WWREMOTE_AR_*) [4] u16 seq [6] u8 detail
//          (WWREMOTE_AD_*) [7] u8 life_before (clamped to 255) [8] u32
//          client frame [12] s16 life delta [14] u16 pad.
//   PSTATE sub 6, client -> host, 24 bytes, [2] server-stamped client id:
//          [3] u8 WWREMOTE_PS_* [4] u32 epoch [8] char[8] stage [16] s8
//          room [17] u8 cut_type [18] u8 cut_count [19] u8 player id
//          (server-stamped, 0 = unmapped) [20] s16 iframes [22] s16
//          shield_angle_y.
//   Ingress: PHIT only from RoleEnemyHost; PACK and PSTATE only from
//   RoleEnemyClient, stamped like HIT.
//
// Server mapping (design section 8.4, question Q5), chosen: pair by name.
// The enemy pipelines already join with the player session's own name
// (mods/multiplayer/engine/session.go passes it to HostPipeline and
// ClientPipeline; handleEnemy(conn, name, role) has it), so no hello
// changes. The server pairs an enemy connection with the player connection
// whose name is equal under the lobby's case-insensitive key (lobbyKey)
// and stamps that player's id into HIT [48] and PSTATE [19]. No match, or
// more than one player with that key (a CLI session without a lobby does
// not enforce unique names): stamp 0. The host learns enemy client id <->
// player id from PSTATE [2] and [19] itself; the server keeps no table
// beyond its connections. The host routes a PHIT by the client id of the
// PSTATE whose player id owns the slot. Player id 0: the host attributes
// to its own Link (WWREMOTE_HD_NO_BODY) and sends no PHIT.
#ifndef WW_WWREMOTE_H
#define WW_WWREMOTE_H

#define WWREMOTE_MAGIC              0x57575250   // 'WWRP'
#define WWREMOTE_OFF                0x520        // block start, from MAILBOX_ADDR (0x80413420)
#define WWREMOTE_SIZE               0x330        // whole block, ends +0x850 (0x80413750)
#define WWREMOTE_LAYOUT             1            // u16 at +0x08: bump on any layout change
#define WWREMOTE_REGION_START       0x510        // what remote-Link owns, from MAILBOX_ADDR: WWENEMY_REGION_END..
#define WWREMOTE_REGION_END         0x850        // ..WWREMOTE_OFF + WWREMOTE_SIZE
#define WWREMOTE_MAX_SLOTS          4            // slot / body_status entries in the layout (runtime: MAX_REMOTE_LINKS)
#define WWREMOTE_PROC_POLICY_MAX    48
#define WWREMOTE_FF_DENY_MAX        4
#define WWREMOTE_STAGE_LEN          8            // char[8] NUL-padded, as dStage_startStage_c::mName
#define WWREMOTE_STALE_FRAMES       90           // REL frames without a new seq: block stale
#define WWREMOTE_POSE_STALE_FRAMES  30           // pose seq frozen this long: the body parks

// --- Go-written span: +0x000..+0x1EC ---------------------------------------
#define WWREMOTE_MAGIC_OFF          0x000        // u32 WWREMOTE_MAGIC (0 = off)
#define WWREMOTE_SEQ_HEAD_OFF       0x004        // u32 ++ per tick, never 0
#define WWREMOTE_LAYOUT_OFF         0x008        // u16 WWREMOTE_LAYOUT
#define WWREMOTE_FLAGS_OFF          0x00A        // u16 WWREMOTE_FLAG_*
#define WWREMOTE_EPOCH_OFF          0x00C        // u32 enemy-block rules (bumped on arm/stage change)
#define WWREMOTE_MARGIN_Q8_OFF      0x010        // u8 latch switch margin: d2_best < margin/256 * d2_cur
#define WWREMOTE_MIN_HOLD_OFF       0x011        // u8 latch min hold, frames
#define WWREMOTE_BODY_IFRAMES_OFF   0x012        // u8 body i-frames stamped on detection (FLAG_IFRAME_STAMP)
#define WWREMOTE_ROLE_OFF           0x013        // u8 WWREMOTE_ROLE_*
#define WWREMOTE_DY_MAX_OFF         0x014        // f32 latch candidate |dy| limit
#define WWREMOTE_POLICY_COUNT_OFF   0x018        // u8 PROC_POLICY entries in use (0..48)
#define WWREMOTE_FF_COUNT_OFF       0x019        // u8 FF denylist entries in use (0..4)
#define WWREMOTE_CREATE_COOLDOWN_OFF 0x01A       // u16 frames after a failed body create
#define WWREMOTE_FULL_ALLOW_OFF     0x01C        // u8 bit i: slot i may begin one daPy_lk_c create (FLAG_FULL_BODIES only; Go heap gate)
#define WWREMOTE_HEAP_REASON_OFF    0x01D        // u8 WWREMOTE_HG_* why the gate is shut (REL: wait vs fall back only)
#define WWREMOTE_FLAGS2_OFF         0x01E        // u16 WWREMOTE_FLAG2_*; 0 = today
#define WWREMOTE_SLOTS_OFF          0x020        // slot[4] x WWREMOTE_SLOT_SIZE
#define WWREMOTE_SLOT_SIZE          0x30
#define WWREMOTE_APPLY_OFF          0x0E0        // client apply record (WWREMOTE_AP_*)
#define WWREMOTE_APPLY_SIZE         0x28
#define WWREMOTE_FF_DENY_OFF        0x108        // char[8] x WWREMOTE_FF_DENY_MAX
#define WWREMOTE_POLICY_OFF         0x128        // PROC_POLICY[48] x WWREMOTE_POLICY_SIZE
#define WWREMOTE_POLICY_SIZE        4
#define WWREMOTE_SEQ_TAIL_OFF       0x1E8        // u32 = seq_head, the last Go-written word
#define WWREMOTE_GO_SPAN            0x1EC        // bytes Go writes per tick
#define WWREMOTE_UNUSED_OFF         0x1EC        // u32 nobody writes

// flags (+0x00A); all clear = today's behaviour
#define WWREMOTE_FLAG_BODIES        0x0001       // host: create lite bodies
#define WWREMOTE_FLAG_SWAP          0x0002       // pointer swap for eligible procs
#define WWREMOTE_FLAG_HITTABLE      0x0004       // body Tg Set each frame
#define WWREMOTE_FLAG_DETECT        0x0008       // host hit ring
#define WWREMOTE_FLAG_APPLY         0x0010       // client apply ("v2 in reverse")
#define WWREMOTE_FLAG_ATTRIB        0x0020       // client v2 hits attributed to bodies
#define WWREMOTE_FLAG_NAME_A9       0x0040       // transient proc name 0xA9 on bodies inside eligible executes
#define WWREMOTE_FLAG_GUARD         0x0080       // body Tg shield + checkPlayerGuard stub
#define WWREMOTE_FLAG_FF_GATE_OFF   0x0100       // disable the FF stage denylist gate (design NO_FF inverted: clear = gate on)
#define WWREMOTE_FLAG_ROUYA_GATE_OFF 0x0200      // disable the isCollect jail gate (design ROUYA inverted: clear = gate on)
#define WWREMOTE_FLAG_ATTACKER      0x0400       // attacker wins: force latch, mo2 Tg colliders, spear hit
#define WWREMOTE_FLAG_GROUP_WIDE    0x0800       // ENEMY group eligible unless DENY (else ALLOW only)
#define WWREMOTE_FLAG_BOTH_WORDS_ENABLE 0x1000   // honour per-proc WWREMOTE_PF_BOTH_WORDS (also swap 0x803CA754)
#define WWREMOTE_FLAG_IFRAME_STAMP  0x2000       // stamp body_iframes on detection
#define WWREMOTE_FLAG_FULL_BODIES   0x4000       // host: bodies are the real daPy_lk_c (proc 0x27 profile) when the slot's FULL_ALLOW bit is set; else lite

// flags2 (+0x01E); all clear = today's behaviour
#define WWREMOTE_FLAG2_FULL_CO      0x0001       // full bodies keep mCyl's Co Set bit (default: Co off)

// heap-gate reasons (+0x01D, Go-written; the REL waits on the transient ones)
#define WWREMOTE_HG_NONE            0            // gate not run (FULL_BODIES off, client, or no sample yet this session)
#define WWREMOTE_HG_OPEN            1
#define WWREMOTE_HG_NO_SAMPLE       2            // no fresh sample in this stage (transient)
#define WWREMOTE_HG_WALK_PROBLEM    3            // walker reported a Problem (retry next sample)
#define WWREMOTE_HG_GAME_LARGEST    4
#define WWREMOTE_HG_GAME_FREE       5
#define WWREMOTE_HG_ZELDA_LARGEST   6
#define WWREMOTE_HG_COUNTER_ROSE    7            // Game/Zelda error counter rose this stage (or solid during a FULL create)
#define WWREMOTE_HG_INFLIGHT        8            // a FULL body is QUEUED (transient)
#define WWREMOTE_HG_STAGE_SETTLE    9            // stage change, fewer than 2 good samples (transient)
#define WWREMOTE_HG_STICKY          10           // two stages closed by counters: shut for the session
#define WWREMOTE_HG_ARCHIVE_LARGEST 11           // shanm table not allocated yet and the archive heap is short
#define WWREMOTE_HG_FULL_MAX        12           // FullMax FULL bodies already LIVE
#define WWREMOTE_HG_QUEUED          13           // open, the bit is another slot's; this one waits its turn (transient)

#define WWREMOTE_ROLE_HOST          1            // = WWENEMY_ROLE_HOST
#define WWREMOTE_ROLE_CLIENT        2            // = WWENEMY_ROLE_CLIENT

// tunable seeds Go writes (the f32 ones as whole numbers here)
#define WWREMOTE_DEF_MARGIN_Q8      164          // ~0.64
#define WWREMOTE_DEF_MIN_HOLD       30
#define WWREMOTE_DEF_BODY_IFRAMES   30           // vanilla mDam field_0x0 (d_a_player_HIO_data.inc:214-216)
#define WWREMOTE_DEF_DY_MAX         400          // f32
#define WWREMOTE_DEF_CREATE_COOLDOWN 30
#define WWREMOTE_DEF_EYE_DY         110          // f32, tuned in Step 1 from host Link +0x260 - +0x1F8
#define WWREMOTE_DEF_CYL_R          30           // f32
#define WWREMOTE_DEF_CYL_H          120          // f32

// slot fields, from the slot start
#define WWREMOTE_SL_ACTIVE_OFF      0x00         // u8 1 = this slot has a remote
#define WWREMOTE_SL_SFLAGS_OFF      0x01         // u8 WWREMOTE_SF_*
#define WWREMOTE_SL_ROOM_OFF        0x02         // s8 the remote's room
#define WWREMOTE_SL_CUT_TYPE_OFF    0x03         // u8 remote Link +0x290 (d_a_player.h:479)
#define WWREMOTE_SL_CUT_COUNT_OFF   0x04         // u8 remote Link +0x291
#define WWREMOTE_SL_PLAYER_ID_OFF   0x05         // u8 server player id (diagnostic)
#define WWREMOTE_SL_IFRAMES_OFF     0x06         // s16 remote mDamageWaitTimer (+0x294) from PSTATE
#define WWREMOTE_SL_STAGE_OFF       0x08         // char[8] the remote's stage
#define WWREMOTE_SL_POS_OFF         0x10         // f32 x, y, z (Go-smoothed, as the mini-Link)
#define WWREMOTE_SL_ANGLE_OFF       0x1C         // s16 angle.y
#define WWREMOTE_SL_SHIELD_ANGLE_OFF 0x1E        // s16 shield_angle_y
#define WWREMOTE_SL_EYE_DY_OFF      0x20         // f32 eyePos = pos + (0, eye_dy, 0)
#define WWREMOTE_SL_CYL_R_OFF       0x24         // f32 body Tg cylinder radius
#define WWREMOTE_SL_CYL_H_OFF       0x28         // f32 body Tg cylinder height
#define WWREMOTE_SL_POSE_SLOT_OFF   0x2C         // u8 pose-feed slot (index into mailbox +0xA8/+0xB4/+0xB6)
#define WWREMOTE_SL_GEN_OFF         0x2D         // u8 bumped by Go when the slot changes owner: body recreated
#define WWREMOTE_SL_PAD_OFF         0x2E         // u16 0

#define WWREMOTE_SF_TARGETABLE      0x01
#define WWREMOTE_SF_GUARD           0x02
#define WWREMOTE_SF_NO_HIT          0x04
#define WWREMOTE_SF_CROUCH_DEF      0x08
#define WWREMOTE_SF_DEAD            0x10
#define WWREMOTE_SF_IN_EVENT        0x20
#define WWREMOTE_SF_SWIM            0x40
#define WWREMOTE_SF_ON_SHIP         0x80

// PSTATE [3] bits (wire only; Go derives the slot sflags from them)
#define WWREMOTE_PS_GUARD           0x01         // mCurProc CROUCH_DEFENSE 0x0C or upper ATNG anime
#define WWREMOTE_PS_CROUCH_DEF      0x02
#define WWREMOTE_PS_DAMAGE_MODE     0x04         // mModeFlg (+0x3618) DAMAGE bit
#define WWREMOTE_PS_DEAD            0x08
#define WWREMOTE_PS_IN_EVENT        0x10
#define WWREMOTE_PS_SWIM            0x20
#define WWREMOTE_PS_ON_SHIP         0x40
#define WWREMOTE_PS_LOADING         0x80

// client apply record (client role), from WWREMOTE_APPLY_OFF
#define WWREMOTE_AP_SEQ_OFF         0x00         // u32 ++ per PHIT Go accepted (the REL acks at WWREMOTE_APPLY_ACK_OFF)
#define WWREMOTE_AP_SETID_OFF       0x04         // u16 attacker setID (the client's mirrored Moblin)
#define WWREMOTE_AP_ATP_OFF         0x06         // u8 At power
#define WWREMOTE_AP_SE_OFF          0x07         // u8 dCcD_GObjAt mSe
#define WWREMOTE_AP_HITMARK_OFF     0x08         // u8 dCcD_GObjAt mHitMark
#define WWREMOTE_AP_SPL_OFF         0x09         // u8 dCcD_GObjAt mSpl
#define WWREMOTE_AP_AT_SPRM_OFF     0x0A         // u8 At SPrm, always WWREMOTE_APPLY_AT_SPRM
#define WWREMOTE_AP_FLAGS_OFF       0x0B         // u8 WWREMOTE_AF_*
#define WWREMOTE_AP_ATTYPE_OFF      0x0C         // u32 cCcD_ObjAt mType
#define WWREMOTE_AP_REL_HIT_OFF     0x10         // f32 x, y, z: hit point - body pos on the host
#define WWREMOTE_AP_RVEC_OFF        0x1C         // f32 x, y, z: the host Tg RVec

#define WWREMOTE_APPLY_AT_SPRM      0x05         // Set|VsPlayer: cannot hit mirrored Moblins
#define WWREMOTE_AF_HOST_SHIELD     0x01         // PHIT flags b0: the host saw a shield hit
#define WWREMOTE_AF_HOST_GUARD      0x02         // PHIT flags b1: the host's guard flag for this remote

// PROC_POLICY entry, from its start
#define WWREMOTE_PP_PROC_OFF        0x00         // s16 proc name (fpcNm_*)
#define WWREMOTE_PP_FLAGS_OFF       0x02         // u8 WWREMOTE_PF_*
#define WWREMOTE_PP_PAD_OFF         0x03         // u8 0

#define WWREMOTE_PF_DENY            0x01
#define WWREMOTE_PF_ALLOW           0x02
#define WWREMOTE_PF_BOTH_WORDS      0x04         // also swap 0x803CA754 (needs FLAG_BOTH_WORDS_ENABLE)
#define WWREMOTE_PF_NO_LATCH        0x08         // pure nearest, no hysteresis

#define WWREMOTE_PROC_MO2           0x00BD       // the only seed ALLOW (design section 3 audit)

// --- REL-written span: +0x1F0..+0x330 --------------------------------------
#define WWREMOTE_ACK_SEQ_OFF        0x1F0        // u32 last valid seq consumed
#define WWREMOTE_ACK_FRAME_OFF      0x1F4        // u32 WWREL frame at that consume
#define WWREMOTE_BODY_STATUS_OFF    0x1F8        // body_status[4] x WWREMOTE_BODY_STATUS_SIZE
#define WWREMOTE_BODY_STATUS_SIZE   0x10
#define WWREMOTE_COUNTERS_OFF       0x238        // u32[WWREMOTE_COUNTERS], cumulative
#define WWREMOTE_COUNTERS           10
#define WWREMOTE_HIT_RING_HEAD_OFF  0x260        // u32 pushes so far
#define WWREMOTE_HIT_RING_OFF       0x264        // hit[4] x WWREMOTE_HIT_SIZE
#define WWREMOTE_HIT_RING_LEN       4
#define WWREMOTE_HIT_SIZE           0x30
#define WWREMOTE_APPLY_ACK_OFF      0x324        // u32 apply seq consumed
#define WWREMOTE_APPLY_RESULT_OFF   0x328        // u32 BE: byte3 AR, byte2 AD, byte1 executes, byte0 Sets
#define WWREMOTE_LIFE_BEFORE_OFF    0x32C        // u16 client Link life at submit
#define WWREMOTE_LIFE_AFTER_OFF     0x32E        // u16 client Link life two executes later

// body_status fields, from the entry start
#define WWREMOTE_BS_ACTOR_OFF       0x00         // u32 body actor address (0 while unresolved or EMPTY)
#define WWREMOTE_BS_PID_OFF         0x04         // u32 body pid (fopAcM_create's result)
#define WWREMOTE_BS_STATE_OFF       0x08         // u8 WWREMOTE_BODY_*
#define WWREMOTE_BS_ERR_OFF         0x09         // u8 WWREMOTE_BERR_* (the last one)
#define WWREMOTE_BS_HITS_OFF        0x0A         // u8 hits detected (saturating)
#define WWREMOTE_BS_LATCHED_BY_OFF  0x0B         // u8 latch entries targeting this body
#define WWREMOTE_BS_FRAMES_LIVE_OFF 0x0C         // u16 executes LIVE (saturating)
#define WWREMOTE_BS_TRIES_OFF       0x0E         // u8 creates this stage
#define WWREMOTE_BS_KIND_OFF        0x0F         // u8 WWREMOTE_KIND_* (was pad)

#define WWREMOTE_KIND_LITE          0
#define WWREMOTE_KIND_FULL          1

#define WWREMOTE_BODY_EMPTY         0
#define WWREMOTE_BODY_QUEUED        1
#define WWREMOTE_BODY_LIVE          2
#define WWREMOTE_BODY_PARKED        3
#define WWREMOTE_BODY_DYING         4

#define WWREMOTE_BERR_NONE          0
#define WWREMOTE_BERR_SLOT          1            // slot >= MAX_REMOTE_LINKS
#define WWREMOTE_BERR_BLOCK         2            // WWRP invalid, stale or not the host role
#define WWREMOTE_BERR_VTABLE        3            // __vt__9daPy_py_c / lk_c fingerprint failed
#define WWREMOTE_BERR_HOST_LINK     4            // 0x803CA754 not an in-MEM1 0xA9, or != 0x803CA74C
#define WWREMOTE_BERR_CYL           5            // Link mCyl vptr != __vt__8dCcD_Cyl 0x8038AF08
#define WWREMOTE_BERR_CREATE        6            // fopAcM_create returned 0xFFFFFFFF
#define WWREMOTE_BERR_LOST          7            // creating ended and SearchByID found no 'WWRB'
#define WWREMOTE_BERR_TRIES         8            // 3 tries this stage spent
#define WWREMOTE_BERR_GONE          9            // the pid vanished (stage teardown)
#define WWREMOTE_BERR_HEAP_DENIED   10           // FULL wanted, allow bit clear (fell back to lite)
#define WWREMOTE_BERR_PHASE_TIMEOUT 11           // full create still INIT after WWREMOTE_FULL_CREATE_FRAMES
#define WWREMOTE_BERR_HEAP_ERRORS   12           // GameHeapErrors or solidHeapErrors rose across playerInit
#define WWREMOTE_BERR_PROFILE       13           // DMC[0x27] != DMC[0xB5] or profile slot 0x27 != g_profile_WWBODY
#define WWREMOTE_BERR_SHANM_ALLOC   14           // archive-heap shanm table alloc failed or invalid
#define WWREMOTE_BERR_FULL_VTABLE   15           // after __ct__9daPy_lk_cFv, vptr +0x31C != 0x8038FDC0

// counters (u32 index from WWREMOTE_COUNTERS_OFF)
#define WWREMOTE_CTR_HOOK_CALLS     0            // every exec hook entry, even with magic 0
#define WWREMOTE_CTR_SWAPS          1
#define WWREMOTE_CTR_SWITCHES       2            // latch target changes
#define WWREMOTE_CTR_ATTACKER_OVERRIDES 3
#define WWREMOTE_CTR_DENIED         4            // ENEMY-group policy misses only
#define WWREMOTE_CTR_ROUYA_SKIPS    5            // jail/demo gate skips (mo2) and FF denylist skips
#define WWREMOTE_CTR_PARK_REWRITES  6            // m714 rewrites
#define WWREMOTE_CTR_BOTH_WORD_SWAPS 7
#define WWREMOTE_CTR_VT_REFUSALS    8
#define WWREMOTE_CTR_GATE_SKIPS     9            // host-Link gate skips

// hit ring entry fields, from the entry start
#define WWREMOTE_HE_SEQ_OFF         0x00         // u16 (u16)push number, 0 = never written
#define WWREMOTE_HE_SLOT_OFF        0x02         // u8 body slot hit
#define WWREMOTE_HE_HFLAGS_OFF      0x03         // u8 WWREMOTE_HF_*
#define WWREMOTE_HE_PID_OFF         0x04         // u32 attacker pid
#define WWREMOTE_HE_SETID_OFF       0x08         // u16 attacker setID
#define WWREMOTE_HE_PROC_OFF        0x0A         // s16 attacker proc name
#define WWREMOTE_HE_ATTYPE_OFF      0x0C         // u32 attacker cCcD_ObjAt mType (spear thrust 0x800)
#define WWREMOTE_HE_ATP_OFF         0x10         // u8 attacker mAtp (spear 1/2/4)
#define WWREMOTE_HE_SE_OFF          0x11         // u8 attacker GObjAt mSe (collider +0x6C, Step -1; spear 3)
#define WWREMOTE_HE_HITMARK_OFF     0x12         // u8 attacker GObjAt mHitMark (collider +0x6E; spear 0x0D)
#define WWREMOTE_HE_SPL_OFF         0x13         // u8 attacker GObjAt mSpl (collider +0x6F; spear 0/6/7)
#define WWREMOTE_HE_REL_HIT_OFF     0x14         // f32 x, y, z: Tg HitPos (cyl+0xCC) - body pos
#define WWREMOTE_HE_RVEC_OFF        0x20         // f32 x, y, z: Tg RVec (cyl+0xC0)
#define WWREMOTE_HE_FRAME_OFF       0x2C         // u32 WWREL frame at detection

#define WWREMOTE_HF_SHIELD          0x01         // Tg RPrm shield-hit bit (cyl+0x98 & 2)
#define WWREMOTE_HF_NOT_MO2         0x02         // attacker proc != WWREMOTE_PROC_MO2 (Go sends it with setID 0xFFFF: the client stand-in owner)
#define WWREMOTE_HF_NOT_KEYED       0x04         // attacker setID >= WWREMOTE_KEYED_SETID_MAX (likewise)
#define WWREMOTE_KEYED_SETID_MAX    0x200        // = EM_KEYED_SETID_MAX (enemy_mirror.c:333)

// apply results (byte3 of WWREMOTE_APPLY_RESULT_OFF; PACK [3])
#define WWREMOTE_AR_NONE            0            // no result yet
#define WWREMOTE_AR_DROPPED         1            // dropped before a Set; the detail says why
#define WWREMOTE_AR_APPLIED         2
#define WWREMOTE_AR_GUARDED         3            // crossed Link's Tg with its shield-hit bit
#define WWREMOTE_AR_NO_CROSS        4
#define WWREMOTE_AR_NO_MIRROR       5            // no mirrored Moblin with that setID (no NULL fallback, D9)
#define WWREMOTE_AR_LINK_ABSENT     6            // no client Link (0x803CA754 not an in-MEM1 0xA9)
#define WWREMOTE_AR_EARLY_OUT       7            // crossed, but changeDamageProc took an early out

// apply details (byte2 of WWREMOTE_APPLY_RESULT_OFF; PACK [6])
#define WWREMOTE_AD_NONE            0
#define WWREMOTE_AD_IFRAMES         1            // Link's Tg Set bit clear (mDamageWaitTimer)
#define WWREMOTE_AD_GO_GATE         2            // Go: epoch/stage/room mismatch (never reaches the REL)
#define WWREMOTE_AD_NOT_IN_PLAY     3            // Go: Link not in play
#define WWREMOTE_AD_STALE_EPOCH     4            // Go: the PHIT's epoch is older than the block's
#define WWREMOTE_AD_ABORTED         5            // REL: delete, epilog or magic 0 mid-request

// ATTRIB extensions of the enemy block's hit_apply (wwenemy.h), section 8.6
#define WWREMOTE_HAF_ATTRIB_BODY    0x08         // bit in WWENEMY_HA_FLAGS_OFF: attribute to the slot's body
#define WWREMOTE_HAF_SLOT1          0x10         // bit in WWENEMY_HA_FLAGS_OFF: body slot 1 (clear = slot 0)
#define WWREMOTE_HD_NO_BODY         16           // WWENEMY_HIT_RESULT detail: ATTRIB asked, the body not LIVE (host-Link path ran)

// --- the actor (both roles of the 0xB5 profile) -----------------------------
#define WWREMOTE_ACTOR_SIZE         0x600        // profile mSize, max(keeper, lite body)
#define WWREMOTE_KEEPER_SWAP_OFF    0x458        // keeper swap state start (after enemy_mirror_state), ends <= 0x600
#define WWREMOTE_BODY_PARAMS_TAG    0x52000000   // body mParameters = tag | slot << 8 | gen; keeper = 0
#define WWREMOTE_BODY_PARAMS_MASK   0xFF000000
#define WWREMOTE_BODY_MAGIC         0x57575242   // 'WWRB' at WWREMOTE_BODY_MAGIC_OFF

// lite body fields, from the body actor (section 5.2)
#define WWREMOTE_BODY_CUT_TYPE_OFF  0x290        // u8 daPy_py_c mCutType image
#define WWREMOTE_BODY_CUT_COUNT_OFF 0x291        // u8 mCutCount image
#define WWREMOTE_BODY_DAMAGE_WAIT_OFF 0x294      // s16 mDamageWaitTimer image (diagnostic)
#define WWREMOTE_BODY_GRAB_WEAR_OFF 0x2B0        // f32 0.0 (checkGrabWear false)
#define WWREMOTE_BODY_DEMO_OFF      0x304        // daPy_demo_c, stays zero
#define WWREMOTE_BODY_VPTR_OFF      0x31C        // daPy_py_c vptr = body + WWREMOTE_BODY_VT_OFF
#define WWREMOTE_BODY_VT_OFF        0x320        // copy of __vt__9daPy_py_c, stubbed
#define WWREMOTE_BODY_VT_SIZE       0x9C
#define WWREMOTE_BODY_CYL_OFF       0x3C0        // dCcD_Cyl copied from Link +0x4024
#define WWREMOTE_BODY_CYL_SIZE      0x130
#define WWREMOTE_BODY_STTS_OFF      0x4F0        // dCcD_Stts
#define WWREMOTE_BODY_STTS_SIZE     0x3C
#define WWREMOTE_BODY_MAGIC_OFF     0x530        // u32 WWREMOTE_BODY_MAGIC
#define WWREMOTE_BODY_SLOT_OFF      0x534        // u8
#define WWREMOTE_BODY_GEN_OFF       0x535        // u8
#define WWREMOTE_BODY_IFRAMES_LEFT_OFF 0x536     // u8 provisional i-frames left (the body's own counter)
#define WWREMOTE_BODY_STATE_OFF     0x537        // u8 WWREMOTE_BODY_* as the body sees itself
#define WWREMOTE_BODY_SHIELD_ANGLE_OFF 0x538     // s16 the cyl's mpShieldFrontRangeYAngle target
#define WWREMOTE_BODY_HAND_MTX_OFF  0x53C        // Mtx 0x30, hand matrices (vt+0x08/+0x0C stubs)
#define WWREMOTE_BODY_LAST_POSE_SEQ_OFF 0x56C    // u32
#define WWREMOTE_BODY_PARK_SINCE_OFF 0x570       // u32 WWREL frame parked at
#define WWREMOTE_BODY_END           0x574        // <= WWREMOTE_ACTOR_SIZE

// --- the full body (FLAG_FULL_BODIES; stage-4 plan D4-1, D4-6) -------------
#define WWREMOTE_FULL_PROC          0x0027       // hijacked proc/profile slot (Seatag; never placed on the GZLE01 disc)
#define WWREMOTE_FULL_NAME_ENTRY    0x80339938   // DynamicNameTable entry of proc 0x27 (SYM:13851 + 12*8)
#define WWREMOTE_FULL_NAME_PTR      0x8033AE39   // "d_a_rectangle": patched into WWREMOTE_FULL_NAME_ENTRY + 4 (was 0x8033A6F3)
#define WWREMOTE_DMC_FULL_ADDR      0x803B92B4   // DMC[0x27] (DMC 0x803B9218, SYM:17371)
#define WWREMOTE_DMC_REL_ADDR       0x803B94EC   // DMC[0xB5]
#define WWREMOTE_PROFILE_LIST_PTR   0x803F6A68   // g_fpcPf_ProfileList_p (SYM:18664); slot = list + 4 * proc
#define WWREMOTE_FULL_SIZE          0x4C80       // profile mSize: sizeof(daPy_lk_c) 0x4C28 + tail 0x58
#define WWREMOTE_FULL_DAPY_SIZE     0x4C28       // d_a_player_main.h:2375
#define WWREMOTE_FULL_TAIL_OFF      0x4C28
#define WWREMOTE_FULL_MAGIC_OFF     0x4C28       // u32 WWREMOTE_BODY_MAGIC, written last in create
#define WWREMOTE_FULL_SLOT_OFF      0x4C2C       // u8
#define WWREMOTE_FULL_GEN_OFF       0x4C2D       // u8
#define WWREMOTE_FULL_IFRAMES_LEFT_OFF 0x4C2E    // u8
#define WWREMOTE_FULL_STATE_OFF     0x4C2F       // u8 WWREMOTE_BODY_LIVE / _PARKED
#define WWREMOTE_FULL_SHIELD_ANGLE_OFF 0x4C30    // s16 slot shield angle (the live one is +0x3546)
#define WWREMOTE_FULL_LAST_POSE_SEQ_OFF 0x4C64   // u32 (= lite +0x56C - 0x530 + tail)
#define WWREMOTE_FULL_PARK_SINCE_OFF 0x4C68      // u32
#define WWREMOTE_FULL_PHASE_OFF     0x4C70       // u8 WWREMOTE_FP_*
#define WWREMOTE_FULL_KIND_OFF      0x4C71       // u8 WWREMOTE_KIND_FULL
#define WWREMOTE_FULL_CREATE_FRAMES_OFF 0x4C72   // u16 create calls so far
#define WWREMOTE_FULL_SHANM_DIFF_OFF 0x4C74      // u32 shared words restored after the create windows
#define WWREMOTE_FULL_HEAP_ERR_OFF  0x4C78       // u32 byte3 game-errors delta, byte2 solid delta across playerInit
#define WWREMOTE_FULL_END           0x4C7C       // <= WWREMOTE_FULL_SIZE
#define WWREMOTE_FULL_CREATE_FRAMES 120          // phase_2 gate + makeBgWait INIT retries before cPhs_ERROR_e
#define WWREMOTE_FP_NEW             0            // phase_1 not run
#define WWREMOTE_FP_PARAMS          1            // phase_1 done (params cached, then rewritten to 0xFF000040)
#define WWREMOTE_FP_INIT            2            // ctor + playerInit done: delete must run playerDelete
#define WWREMOTE_FP_LIVE            3            // makeBgWait returned NEXT: create complete
#define WWREMOTE_FULL_PARAMS        0xFF000040   // params after phase_1: event 0xFF, mode 0, room 0, 0x40 skip setRestartRoom

// daPy_lk_c fields the REL and the probe touch (DPMH / d_a_player.h)
#define WWREMOTE_FULL_DAMAGE_WAIT_OFF 0x294      // s16 mDamageWaitTimer (d_a_player.h:482)
#define WWREMOTE_FULL_NO_RESET1_OFF 0x2A0        // u32 mNoResetFlg1 (CASUAL 0x08 cleared before delete)
#define WWREMOTE_FULL_RESET0_OFF    0x2A4        // u32 mResetFlg0 = 0 every execute
#define WWREMOTE_FULL_VPTR_OFF      0x31C        // == 0x8038FDC0 after the ctor
#define WWREMOTE_FULL_MODEL_OFF     0x32C        // J3DModel* mpCLModel (DPMH:1993)
#define WWREMOTE_FULL_LINKTEX_OFF   0x338        // ResTIMG* mpCurrLinktex
#define WWREMOTE_FULL_STTS_OFF      0x3FE8       // dCcD_Stts mStts
#define WWREMOTE_FULL_CYL_OFF       0x4024       // dCcD_Cyl mCyl (Step -1 offsets apply from here)
#define WWREMOTE_FULL_CUR_PROC_OFF  0x31D8       // int mCurProc (WAIT 0x04, CROUCH_DEFENSE 0x0C)
#define WWREMOTE_FULL_SHIELD_RANGE_OFF 0x3546    // s16 mShieldFrontRangeYAngle (mCyl's shield-angle pointer target)
#define WWREMOTE_FULL_MODE_FLG_OFF  0x3618       // u32 mModeFlg (clear DAMAGE 0x8, IN_SHIP 0x2000, CRAWL 0x01000000)

// shanm table header (archive block at *(u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF))
#define WWREMOTE_SHANM_MAGIC        0x57575341   // 'WWSA'
#define WWREMOTE_SHANM_BYTES        0x4760       // header 0x60 + joint_addr[64] + mat_addr[256] + val[4224]
#define WWREMOTE_SA_MAGIC_OFF       0x00         // u32
#define WWREMOTE_SA_SIZE_OFF        0x04         // u32 WWREMOTE_SHANM_BYTES
#define WWREMOTE_SA_SELF_OFF        0x08         // u32 the block's own address
#define WWREMOTE_SA_WORDS_OFF       0x0C         // u32 words tracked at the last refresh (live ~2598, cap 4224)
#define WWREMOTE_SA_CREATE_DIFF_OFF 0x10         // u32 words restored after the last create window
#define WWREMOTE_SA_DELETE_DIFF_OFF 0x14         // u32 words restored after the last delete window
#define WWREMOTE_SA_DELETE_SCAN_OFF 0x18         // u32 tracked words pointing into a deleted body's instance or heaps (must be 0)
#define WWREMOTE_SA_WINDOWS_OFF     0x1C         // u32 windows opened (cumulative)
#define WWREMOTE_SA_JOINT_DIFF_OFF  0x20         // u32 cl.bdl joints 0x02/0x1D mMtxCalc restores (cumulative)

// jail gate (Step -1): the save's mCollect[0], a direct byte read
#define WWREMOTE_COLLECT_ADDR       0x803C4CBC   // wwapi/addr Collect (d_save.cpp:756-759)
#define WWREMOTE_COLLECT_JAIL_MASK  0x0F         // none of isCollect(0, 0..3) set -> mo2 rouya mode

#endif
