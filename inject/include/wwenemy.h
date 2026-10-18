// The enemy-mirror block in the mailbox slack: one 0x160-byte block at
// MAILBOX_ADDR + WWENEMY_OFF that the Go side writes (its first 0xF4
// bytes, one WriteProcessMemory per tick) and the REL module reads, and
// whose tail (+0xF4..+0x160) the REL writes and Go reads. Design:
// ww-multiplayer-handoff/design/host-authoritative-prototype.md
// (and moblin-mirror-design.json, "mailbox_or_slack_block").
//
// Shared by inject/rel/enemy_mirror.c (reader/writer in the game) and
// wwapi/mailbox/enemy.go (the Go mirror of these constants, pinned by a
// test that parses this file the way scripts/extract_blob.py parses
// wwrel.h), so keep every #define on one line in this exact form.
//
// Layout: the Mailbox struct ends at +0x1B0, the REL words live at
// +0x1B0..+0x1BC (wwrel.h), this block at +0x200..+0x360, and
// __OSArenaLo is 0x80414000 (inject/build.py). The slack is not zeroed
// at boot: validity is magic + epoch + a moving seq_head == seq_tail.
#ifndef WW_WWENEMY_H
#define WW_WWENEMY_H

#define WWENEMY_MAGIC            0x5757454E   // 'WWEN'
#define WWENEMY_OFF              0x200        // block start, from MAILBOX_ADDR
#define WWENEMY_SIZE             0x160        // whole block
#define WWENEMY_LAYOUT           1            // u16 at +0x12: bump on any layout change
#define WWENEMY_MAX_SLOTS        4

// --- Go-written span: +0x00..+0xF4, one write per tick -------------------
#define WWENEMY_MAGIC_OFF        0x00         // u32 WWENEMY_MAGIC (0 = block off)
#define WWENEMY_SEQ_HEAD_OFF     0x04         // u32 ++ per tick, never 0
#define WWENEMY_HOST_BEAT_OFF    0x08         // u32 host spawn_trigger at sample time
#define WWENEMY_GO_WRITE_MS_OFF  0x0C         // u32 Go monotonic ms at write (lag probe)
#define WWENEMY_COUNT_OFF        0x10         // u8  slots in use (0..4)
#define WWENEMY_FLAGS_OFF        0x11         // u8  WWENEMY_FLAG_*
#define WWENEMY_LAYOUT_OFF       0x12         // u16 WWENEMY_LAYOUT
#define WWENEMY_EPOCH_OFF        0x14         // u32 bumped on arm/stage change/heartbeat regression
#define WWENEMY_STAGE_OFF        0x18         // char[8] NUL-padded stage name
#define WWENEMY_ROOM_OFF         0x20         // s8  room (mStayNo)
#define WWENEMY_ROLE_OFF         0x21         // u8  WWENEMY_ROLE_*
#define WWENEMY_RESERVED_OFF     0x24         // u32[3]
#define WWENEMY_SLOTS_OFF        0x30         // slot[4] x WWENEMY_SLOT_SIZE
#define WWENEMY_SLOT_SIZE        0x28
#define WWENEMY_HIT_APPLY_OFF    0xD0         // host role: a HIT to apply (see WWENEMY_HA_*)
#define WWENEMY_SEQ_TAIL_OFF     0xF0         // u32 = seq_head, the last Go-written word
#define WWENEMY_GO_SPAN          0xF4         // bytes Go writes per tick

#define WWENEMY_FLAG_MIRROR      0x01         // bit0: mirror on (clear = release everything)
#define WWENEMY_FLAG_COLLIDERS   0x02         // bit1: re-submit colliders (S3)
#define WWENEMY_FLAG_ANIM        0x04         // bit2: streamed animation (S2)
#define WWENEMY_FLAG_RAW         0x08         // bit3: no REL lerp (lag measurement)

#define WWENEMY_ROLE_HOST        1
#define WWENEMY_ROLE_CLIENT      2

// slot fields, from the slot start
#define WWENEMY_SL_PID_OFF       0x00         // u32 actor process id, 0 = empty -> release
#define WWENEMY_SL_POS_OFF       0x04         // f32 x, y, z
#define WWENEMY_SL_ANGLE_OFF     0x10         // s16 angle.y
#define WWENEMY_SL_TILT_OFF      0x12         // s16 mo2 tilt (+0xAA0)
#define WWENEMY_SL_ACTION_OFF    0x14         // s16 mDamageReaction.mAction (+0x61A)
#define WWENEMY_SL_MODE_OFF      0x16         // s16 mDamageReaction.mMode (+0x618)
#define WWENEMY_SL_ATTACK_OFF    0x18         // u8  attack type (+0x2060)
#define WWENEMY_SL_PHASE_OFF     0x19         // u8  attack phase (+0x2064)
#define WWENEMY_SL_HEALTH_OFF    0x1A         // s8  health (+0x285)
#define WWENEMY_SL_FLAGS_OFF     0x1B         // u8  WWENEMY_SF_*
#define WWENEMY_SL_FRAME_OFF     0x1C         // f32 morf frame (mpMorf+0x68)
#define WWENEMY_SL_RATE_OFF      0x20         // f32 morf rate (mpMorf+0x64)
#define WWENEMY_SL_SAMPLE_OFF    0x24         // u32 host sample seq

#define WWENEMY_SF_SNAP          0x01
#define WWENEMY_SF_DYING         0x02
#define WWENEMY_SF_INVULN        0x04
#define WWENEMY_SF_DEFENCE       0x08
#define WWENEMY_SF_RAGDOLL       0x10
#define WWENEMY_SF_BARE_HANDED   0x20
#define WWENEMY_SF_HIDDEN        0x40
#define WWENEMY_SF_RELEASE       0x80

// hit_apply fields (host role), from WWENEMY_HIT_APPLY_OFF
#define WWENEMY_HA_SEQ_OFF       0x00         // u32 request seq (REL acks it at WWENEMY_HIT_APPLY_ACK_OFF)
#define WWENEMY_HA_SETID_OFF     0x04         // u16
#define WWENEMY_HA_COLLIDER_OFF  0x06         // u8 1 head sph, 2 tg cyl
#define WWENEMY_HA_ATTYPE_OFF    0x08         // u32 cCcD_ObjAt mType
#define WWENEMY_HA_ATP_OFF       0x0C         // u8
#define WWENEMY_HA_POS_OFF       0x10         // f32 x, y, z
#define WWENEMY_HA_PID_OFF       0x1C         // u32 the host's pid of that setID's Moblin (Go resolves it; the REL checks setID and room against it)
#define WWENEMY_HA_SIZE          0x20         // ends at +0xF0 = WWENEMY_SEQ_TAIL_OFF

// --- REL-written span: +0xF4..+0x160 --------------------------------------
#define WWENEMY_ACK_SEQ_OFF      0xF4         // u32 last seq consumed
#define WWENEMY_ACK_FRAME_OFF    0xF8         // u32 WWREL frame counter at consume
#define WWENEMY_ADOPTED_OFF      0xFC         // u32 bit n = slot n adopted
#define WWENEMY_EPOCH_SEEN_OFF   0x100        // u32
#define WWENEMY_STATUS_OFF       0x104        // status[4] x WWENEMY_STATUS_SIZE
#define WWENEMY_STATUS_SIZE      0x10
#define WWENEMY_HITS_OFF         0x144        // hit[4] x 4: u16 hit_seq, u8 collider, u8 pending
#define WWENEMY_HIT_SIZE         0x04
#define WWENEMY_HIT_APPLY_ACK_OFF 0x154       // u32 hit_apply seq acked (result: low byte of WWENEMY_RESERVED2_OFF)
#define WWENEMY_CANARIES_OFF     0x158        // u8 pause_reasserts, u8 anm_unresolved, u8 revalidate_failures, u8 watchdog_releases
#define WWENEMY_RESERVED2_OFF    0x15C        // u32: low byte = last hit_apply result (WWENEMY_HR_*)

// status fields, from the status start
#define WWENEMY_ST_ACTOR_OFF     0x00         // u32 actor address (0 when unresolved)
#define WWENEMY_ST_PID_OFF       0x04         // u32 pid resolved
#define WWENEMY_ST_STATE_OFF     0x08         // u8 WWENEMY_STATE_*
#define WWENEMY_ST_PAUSE_PREV_OFF 0x09        // u8 pause byte before adopt
#define WWENEMY_ST_LAST_ERR_OFF  0x0A         // u8 WWENEMY_ERR_*
#define WWENEMY_ST_FRAMES_OFF    0x0C         // u16 frames mirrored

#define WWENEMY_STATE_UNRESOLVED 0
#define WWENEMY_STATE_MIRRORED   1
#define WWENEMY_STATE_RELEASED   2
#define WWENEMY_STATE_MISSING    3
#define WWENEMY_STATE_DYING      4
#define WWENEMY_STATE_REFUSED    5

#define WWENEMY_ERR_NONE         0
#define WWENEMY_ERR_PID          1            // fopAcM_SearchByID found nothing
#define WWENEMY_ERR_PROC         2            // proc != 0x00BD
#define WWENEMY_ERR_INIT         3            // initState != 2
#define WWENEMY_ERR_SETID        4            // setID / home room mismatch
#define WWENEMY_ERR_MORF         5            // mpMorf or model null
#define WWENEMY_ERR_RANGE        6            // NaN or |pos| > 1e6
#define WWENEMY_ERR_MODE         7            // mo2 mMode != 0

#define WWENEMY_HR_DROPPED       0
#define WWENEMY_HR_APPLIED_AT    1
#define WWENEMY_HR_NO_ACTOR      2
#define WWENEMY_HR_SHRINK        3

#define WWENEMY_STALE_FRAMES     90           // REL releases all after this many frames without a new seq

#endif
