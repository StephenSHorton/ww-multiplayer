// The enemy-mirror block in the mailbox slack: one 0x198-byte block at
// MAILBOX_ADDR + WWENEMY_OFF that the Go side writes (its first 0x124
// bytes, one WriteProcessMemory per tick) and the REL module reads, and
// whose tail (+0x124..+0x198) the REL writes and Go reads. Past the block
// the REL owns two static collision objects (the host's v2 At sphere and
// its stts) up to WWENEMY_REGION_END. Design:
// ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-s2-v2-final.txt (layout 2: the S2 animation stream and the host
// v2 At-sphere hit).
//
// Shared by inject/rel/enemy_mirror.c (reader/writer in the game) and
// wwapi/mailbox/enemy.go (the Go mirror of these constants, pinned by a
// test that parses this file the way scripts/extract_blob.py parses
// wwrel.h), so keep every #define on one line in this exact form.
//
// Layout: the Mailbox struct ends at +0x1B0, the REL words live at
// +0x1B0..+0x1BC (wwrel.h), this block at +0x200..+0x398, the collision
// objects to +0x510, and __OSArenaLo is 0x80414000 (inject/build.py). The
// slack is not zeroed at boot: validity is magic + epoch + a moving
// seq_head == seq_tail.
#ifndef WW_WWENEMY_H
#define WW_WWENEMY_H

#define WWENEMY_MAGIC            0x5757454E   // 'WWEN'
#define WWENEMY_OFF              0x200        // block start, from MAILBOX_ADDR
#define WWENEMY_SIZE             0x198        // whole block
#define WWENEMY_LAYOUT           2            // u16 at +0x12: bump on any layout change
#define WWENEMY_MAX_SLOTS        4

// --- Go-written span: +0x00..+0x124, one write per tick ------------------
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
#define WWENEMY_SLOT_SIZE        0x34
#define WWENEMY_HIT_APPLY_OFF    0x100        // host role: a HIT to apply (see WWENEMY_HA_*)
#define WWENEMY_SEQ_TAIL_OFF     0x120        // u32 = seq_head, the last Go-written word
#define WWENEMY_GO_SPAN          0x124        // bytes Go writes per tick

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
#define WWENEMY_SL_BCK_OFF       0x28         // u8  Mo2 bck resource index (0x3E..0x75), WWENEMY_BCK_NONE unresolved (S2)
#define WWENEMY_SL_ATTR_OFF      0x29         // u8  J3DFrameCtrl attribute 0..4, 0xFF unknown
#define WWENEMY_SL_ANM_SEQ_OFF   0x2A         // u8  bumped by Go when the host restarted the anim (setAnm)
#define WWENEMY_SL_MORF_OFF      0x2B         // u8  the host's anm_init morf (blend frames), 0 = snap
#define WWENEMY_SL_FLAGS2_OFF    0x2C         // u8  WWENEMY_SF2_*
#define WWENEMY_SL_BLUR_SHAPE_OFF 0x2D        // u8  mo2 m0598 blur shape
#define WWENEMY_SL_BLUR_SCALE_OFF 0x30        // f32 mo2 m0590 blur scale (+0x2E u16 pad)

#define WWENEMY_SF_SNAP          0x01
#define WWENEMY_SF_DYING         0x02
#define WWENEMY_SF_INVULN        0x04
#define WWENEMY_SF_DEFENCE       0x08
#define WWENEMY_SF_RAGDOLL       0x10
#define WWENEMY_SF_BARE_HANDED   0x20
#define WWENEMY_SF_HIDDEN        0x40
#define WWENEMY_SF_RELEASE       0x80

#define WWENEMY_BCK_NONE         0xFF         // slot bck: the host could not resolve mpAnm

#define WWENEMY_SF2_HAS_WEAPON   0x01         // mo2 mbHasInnateWeapon (+0xDD4) != 0
#define WWENEMY_SF2_PLAY_SKIP    0x02         // mo2 m05B2 != 0: the host skips play()
#define WWENEMY_SF2_BLUR_ON      0x04         // mo2 m0594 != 0
#define WWENEMY_SF2_ANM_GATE     0x08         // mo2 m05B0 != 0 (informational)

// hit_apply fields (host role), from WWENEMY_HIT_APPLY_OFF
#define WWENEMY_HA_SEQ_OFF       0x00         // u32 request seq (REL acks it at WWENEMY_HIT_APPLY_ACK_OFF)
#define WWENEMY_HA_SETID_OFF     0x04         // u16
#define WWENEMY_HA_COLLIDER_OFF  0x06         // u8 1 head sph, 2 tg cyl
#define WWENEMY_HA_FLAGS_OFF     0x07         // u8 WWENEMY_HAF_*
#define WWENEMY_HA_ATTYPE_OFF    0x08         // u32 cCcD_ObjAt mType
#define WWENEMY_HA_ATP_OFF       0x0C         // u8
#define WWENEMY_HA_SE_OFF        0x0D         // u8 dCcD_GObjAt mSe (0 = 1, the sword)
#define WWENEMY_HA_HITMARK_OFF   0x0E         // u8 dCcD_GObjAt mHitMark (0 = 0xD Nrm)
#define WWENEMY_HA_SPL_OFF       0x0F         // u8 dCcD_GObjAt mSpl (1 = kill blow)
#define WWENEMY_HA_POS_OFF       0x10         // f32 x, y, z
#define WWENEMY_HA_PID_OFF       0x1C         // u32 the host's pid of that setID's Moblin (Go resolves it; the REL checks setID and room against it)
#define WWENEMY_HA_SIZE          0x20         // ends at +0x120 = WWENEMY_SEQ_TAIL_OFF

#define WWENEMY_HAF_FORCE_V1     0x01         // the v1 shrink branch (WW_ENEMY_HIT_V1=1)
#define WWENEMY_HAF_FALLBACK_SHRINK 0x02      // shrink after two Sets crossed nothing (WW_ENEMY_HIT_FALLBACK=shrink)
#define WWENEMY_HAF_NO_HITMARK   0x04         // dCcG_AtSPrm_NoHitMark on the sphere

// --- REL-written span: +0x124..+0x198 -------------------------------------
#define WWENEMY_ACK_SEQ_OFF      0x124        // u32 last seq consumed
#define WWENEMY_ACK_FRAME_OFF    0x128        // u32 WWREL frame counter at consume
#define WWENEMY_ADOPTED_OFF      0x12C        // u32 bit n = slot n adopted
#define WWENEMY_EPOCH_SEEN_OFF   0x130        // u32
#define WWENEMY_STATUS_OFF       0x134        // status[4] x WWENEMY_STATUS_SIZE
#define WWENEMY_STATUS_SIZE      0x10
#define WWENEMY_HITS_OFF         0x174        // hit[4] x 4: u16 hit_seq, u8 collider, u8 pending
#define WWENEMY_HIT_SIZE         0x04
#define WWENEMY_HIT_APPLY_ACK_OFF 0x184       // u32 hit_apply seq acked (result: WWENEMY_HIT_RESULT_OFF)
#define WWENEMY_CANARIES_OFF     0x188        // u8 pause_reasserts, u8 anm_unresolved, u8 revalidate_failures, u8 watchdog_releases
#define WWENEMY_CANARIES2_OFF    0x18C        // u8 anm_res_null, u8 anm_range, u8 hit_nocross, u8 hit_aborted
#define WWENEMY_HIT_RESULT_OFF   0x190        // u32 BE: byte3 WWENEMY_HR_*, byte2 WWENEMY_HD_*, byte1 REL executes from Set to result, byte0 Sets issued
#define WWENEMY_RESERVED2_OFF    0x194        // u32

// status fields, from the status start
#define WWENEMY_ST_ACTOR_OFF     0x00         // u32 actor address (0 when unresolved)
#define WWENEMY_ST_PID_OFF       0x04         // u32 pid resolved
#define WWENEMY_ST_STATE_OFF     0x08         // u8 WWENEMY_STATE_*
#define WWENEMY_ST_PAUSE_PREV_OFF 0x09        // u8 pause byte before adopt
#define WWENEMY_ST_LAST_ERR_OFF  0x0A         // u8 WWENEMY_ERR_*
#define WWENEMY_ST_ANM_STATE_OFF 0x0B         // u8 WWENEMY_ANM_* (S2)
#define WWENEMY_ST_FRAMES_OFF    0x0C         // u16 frames mirrored
#define WWENEMY_ST_BCK_OFF       0x0E         // u8 last bck index setAnm'd on the copy, 0xFF none
#define WWENEMY_ST_ANM_CHANGES_OFF 0x0F       // u8 saturating setAnm count

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
#define WWENEMY_ERR_MODE         7            // mo2 mMode 5 (carried) or 0xF (statue); 0 and 1 (lantern/jail variant) are mirrored

// per-slot animation state (S2), status +0x0B
#define WWENEMY_ANM_S1           0            // not streaming: play+calc only
#define WWENEMY_ANM_SYNCED       1            // the copy plays the streamed bck
#define WWENEMY_ANM_UNRESOLVED   2            // the host sent WWENEMY_BCK_NONE
#define WWENEMY_ANM_RES_NULL     3            // getRes returned NULL or a bad pointer
#define WWENEMY_ANM_RANGE        4            // index or attribute out of range

// hit_apply results (byte3 of WWENEMY_HIT_RESULT_OFF)
#define WWENEMY_HR_DROPPED       0
#define WWENEMY_HR_APPLIED_AT    1            // the At sphere crossed the target's head sphere or Tg cylinder
#define WWENEMY_HR_NO_ACTOR      2
#define WWENEMY_HR_SHRINK        3            // the v1 shrink was written
#define WWENEMY_HR_PARRIED       4            // the Moblin's defence sphere took it

// hit_apply details (byte2 of WWENEMY_HIT_RESULT_OFF); 11..15 are Go-side drops that never reach the REL
#define WWENEMY_HD_NONE          0
#define WWENEMY_HD_NO_CROSS      1            // two Sets, nothing crossed
#define WWENEMY_HD_INVULN        2            // m05B4 still set after the wait, or v1's pre-drop
#define WWENEMY_HD_RAGDOLL       3
#define WWENEMY_HD_DYING         4
#define WWENEMY_HD_NO_LINK       5            // 0x803CA74C not a PLAYER, or != 0x803CA754
#define WWENEMY_HD_NO_SPHERE     6            // the sphere image's vptr sanity failed
#define WWENEMY_HD_WRONG_OBJ     7            // another object's Tg crossed first
#define WWENEMY_HD_ABORTED       8            // REL early return / release / delete mid-request
#define WWENEMY_HD_DEFENCE       9            // v1's pre-drop only
#define WWENEMY_HD_FALLBACK_SHRINK 10
#define WWENEMY_HD_GO_GATE       11           // epoch/stage/room/scope/replayed hit_seq/no REL
#define WWENEMY_HD_RATE          12
#define WWENEMY_HD_BUSY          13
#define WWENEMY_HD_GO_FLAGS      14           // slot flags dropped it before the REL
#define WWENEMY_HD_DEMO          15           // the host Link is in a cutscene

// --- REL-owned static collision objects past the block (from MAILBOX_ADDR) --
#define WWENEMY_SPH_OFF          0x3A0        // dCcD_Sph image, 0x12C bytes: the host's v2 At sphere
#define WWENEMY_STTS_OFF         0x4D0        // dCcD_Stts, 0x3C bytes: its stts, Init'd from the host Link
#define WWENEMY_REGION_END       0x510        // end of everything enemy sync owns (mailbox-relative)

#define WWENEMY_STALE_FRAMES     90           // REL releases all after this many frames without a new seq

#endif
