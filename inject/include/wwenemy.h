// The enemy-mirror block in the mailbox slack: one 0x198-byte block at
// MAILBOX_ADDR + WWENEMY_OFF that the Go side writes (its first 0x124
// bytes, one WriteProcessMemory per tick) and the REL module reads, and
// whose tail (+0x124..+0x198) the REL writes and Go reads. Past the block
// the REL owns two static collision objects (the host's v2 At sphere and
// its stts) up to WWENEMY_REGION_END. Design:
// ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-s2-v2-final.txt (layout 2: the S2 animation stream and the host
// v2 At-sphere hit); enemy-mirror-general.json (layout 3: the kind table
// in wwenemy_kinds.h, the placement key at slot +0x14, the slot proc at
// +0x2E, kind_mask at +0x24 and the annex below the block);
// enemy-mirror-batch-c.json (layout 4, D1-D5: additive, nothing moves:
// knob_gen at +0x22, hook bytes for ids 7..14 at +0x28, the REL's echo at
// +0x194 and the knob table at mailbox +0x850).
//
// Shared by inject/rel/enemy_mirror.c (reader/writer in the game) and
// wwapi/mailbox/enemy.go (the Go mirror of these constants, pinned by a
// test that parses this file the way scripts/extract_blob.py parses
// wwrel.h), so keep every #define on one line in this exact form.
//
// Layout: the Mailbox struct ends at +0x1B0, the REL words live at
// +0x1B0..+0x1C8 (wwrel.h), the annex at +0x1C8..+0x200, this block at
// +0x200..+0x398, the collision objects to +0x510, and __OSArenaLo is
// 0x80414000 (inject/build.py). Go writes the annex and the block's Go
// span in one write, +0x1C8..+0x324 from MAILBOX_ADDR. The slack is not
// zeroed at boot: validity is magic + epoch + a moving seq_head ==
// seq_tail (and, for the annex, annex_seq == that seq). Layout 4's knob
// table (+0x850..+0x8B0) is a second, separate Go write before the block
// write; it is valid only by its own gen words (WWENEMY_KNOB_*), and it
// carries tuning only: every hook kill switch lives inside the seq span.
//
// Offsets are block-relative (from MAILBOX_ADDR + WWENEMY_OFF) unless the
// comment says "mailbox-relative".
#ifndef WW_WWENEMY_H
#define WW_WWENEMY_H

#define WWENEMY_MAGIC            0x5757454E   // 'WWEN'
#define WWENEMY_OFF              0x200        // block start, from MAILBOX_ADDR
#define WWENEMY_SIZE             0x198        // whole block
#define WWENEMY_LAYOUT           4            // u16 at +0x12: bump on any layout change (4: batch C, additive over 3)
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
#define WWENEMY_KNOB_GEN_OFF     0x22         // u16 layout 4 (D4): the knob table's gen this block names, 0 = no table (the former pad)
#define WWENEMY_KIND_MASK_OFF    0x24         // u32 bit k = kind k enabled (EMK_KIND_*, wwenemy_kinds.h); both roles write it (remote.c's HF_NOT_MIRROR reads the host's); ids < 16 (the REL caches it as u16, the wire as u16)
#define WWENEMY_HKD_HI_OFF       0x28         // u8[WWENEMY_HKD_HI_KINDS] layout 4 (D2): hook_disable (WWENEMY_HKD_*) for kind ids 7..14 (BB at +0x28, MT at +0x29), inside the seq span
#define WWENEMY_HKD_HI_KINDS     8            // ids 7..14; the former reserved u32[2], never read by layout 3
#define WWENEMY_SLOTS_OFF        0x30         // slot[4] x WWENEMY_SLOT_SIZE
#define WWENEMY_SLOT_SIZE        0x34
#define WWENEMY_HIT_APPLY_OFF    0x100        // host role: a HIT to apply (see WWENEMY_HA_*)
#define WWENEMY_SEQ_TAIL_OFF     0x120        // u32 = seq_head, the last Go-written word
#define WWENEMY_GO_SPAN          0x124        // bytes of the block Go writes per tick (the write itself starts at the annex)

#define WWENEMY_FLAG_MIRROR      0x01         // bit0: mirror on (clear = release everything)
#define WWENEMY_FLAG_COLLIDERS   0x02         // bit1: re-submit colliders (S3)
#define WWENEMY_FLAG_ANIM        0x04         // bit2: streamed animation (S2)
#define WWENEMY_FLAG_RAW         0x08         // bit3: no REL lerp (lag measurement)

#define WWENEMY_ROLE_HOST        1
#define WWENEMY_ROLE_CLIENT      2

// slot fields, from the slot start. Per-kind sources: wwenemy_kinds.h and
// enemy-mirror-general.json "flags".
#define WWENEMY_SL_PID_OFF       0x00         // u32 actor process id, 0 = empty -> release
#define WWENEMY_SL_POS_OFF       0x04         // f32 x, y, z
#define WWENEMY_SL_ANGLE_OFF     0x10         // s16 angle.y
#define WWENEMY_SL_TILT_OFF      0x12         // s16 tilt: MO2/BK dr.m48C.x (EMK_*_TILT), CC m316.x (written raw, no smoothing)
#define WWENEMY_SL_PKEY_OFF      0x14         // u32 the local actor's placement key (em_pkey), REL compares at consume (ERR_KEY)
#define WWENEMY_SL_ATTACK_OFF    0x18         // u8  MO2 attack type (+0x2060), Go logs only
#define WWENEMY_SL_PHASE_OFF     0x19         // u8  MO2 attack phase (+0x2064), Go logs only
#define WWENEMY_SL_CC_M316Z_OFF  0x18         // s16 "kind z", over attack/phase: CC m316.z (copy +0x31A), BB current.angle.z (batch C R-12), MT the wiggle +0x468 (batch D G-D6, EMK_MT_WIGGLE)
#define WWENEMY_SL_HEALTH_OFF    0x1A         // s8  health (+0x285); the REL reads it only for MT's custody handback (em_mt_handback, batch C MT-4)
#define WWENEMY_SL_FLAGS_OFF     0x1B         // u8  WWENEMY_SF_*
#define WWENEMY_SL_FRAME_OFF     0x1C         // f32 morf frame (mpMorf+0x68)
#define WWENEMY_SL_RATE_OFF      0x20         // f32 morf rate (mpMorf+0x64)
#define WWENEMY_SL_SAMPLE_OFF    0x24         // u32 host sample seq
#define WWENEMY_SL_BCK_OFF       0x28         // u8  the kind's archive bck index (EMK_*_BCK_FIRST..LAST), WWENEMY_BCK_NONE unresolved (S2)
#define WWENEMY_SL_ATTR_OFF      0x29         // u8  J3DFrameCtrl attribute 0..4, 0xFF unknown
#define WWENEMY_SL_ANM_SEQ_OFF   0x2A         // u8  bumped by Go when the host restarted the anim (setAnm)
#define WWENEMY_SL_MORF_OFF      0x2B         // u8  the host's anm_init morf (blend frames), 0 = snap
#define WWENEMY_SL_FLAGS2_OFF    0x2C         // u8  bits 0-5 per kind (WWENEMY_SF2_* for MO2, EMK_<K>_F2_* for BK CC KI AM AM2), bits 6-7 WWENEMY_SF2_LIGHT/QUIET
#define WWENEMY_SL_BLUR_SHAPE_OFF 0x2D        // u8  blur shape: MO2 m0598, BK m02F4, CC 0
#define WWENEMY_SL_PROC_OFF      0x2E         // u16 the slot's proc (EMK_*_PROC); the REL refuses an actor of another proc (ERR_KIND)
#define WWENEMY_SL_BLUR_SCALE_OFF 0x30        // f32 MO2 m0590, BK m02EC, CC m328 (the y offset)

#define WWENEMY_SF_SNAP          0x01
#define WWENEMY_SF_DYING         0x02
#define WWENEMY_SF_INVULN        0x04
#define WWENEMY_SF_DEFENCE       0x08
#define WWENEMY_SF_RAGDOLL       0x10
#define WWENEMY_SF_BARE_HANDED   0x20
#define WWENEMY_SF_HIDDEN        0x40
#define WWENEMY_SF_RELEASE       0x80

#define WWENEMY_BCK_NONE         0xFF         // slot bck: the host could not resolve mpAnm

// flags2 bits 0-3 as the Moblin uses them; every other kind's slot uses
// its own bits 0-5 (wwenemy_kinds.h EMK_BK_F2_*, EMK_CC_F2_*, EMK_KI_F2_*,
// EMK_AM_F2_*, EMK_AM2_F2_*), and the row's ATTN_F2 names its attention bit.
#define WWENEMY_SF2_HAS_WEAPON   0x01         // mo2 mbHasInnateWeapon (+0xDD4) != 0
#define WWENEMY_SF2_PLAY_SKIP    0x02         // mo2 m05B2 != 0: the host skips play()
#define WWENEMY_SF2_BLUR_ON      0x04         // mo2 m0594 != 0
#define WWENEMY_SF2_ANM_GATE     0x08         // mo2 m05B0 != 0 (informational)
#define WWENEMY_SF2_LIGHT        0x40         // every kind: the host's ice shrink timer ran at death (enemy_ice): the 90-frame quiet profile
#define WWENEMY_SF2_QUIET        0x80         // every kind: client config, die by em_die_quiet (no handback, no drop) instead of the legacy em_die

// hit_apply fields (host role), from WWENEMY_HIT_APPLY_OFF
#define WWENEMY_HA_SEQ_OFF       0x00         // u32 request seq (REL acks it at WWENEMY_HIT_APPLY_ACK_OFF)
#define WWENEMY_HA_SETID_OFF     0x04         // u16 the host actor's setID (0xFFFF for an unkeyed one; the REL compares it, 0xFFFF == 0xFFFF included)
#define WWENEMY_HA_COLLIDER_OFF  0x06         // u8 EMK_HIT_*: 1 the kind's head sph, 2 its tg cyl, 3 the AM bomb swallow (no v2 sphere; R-13)
#define WWENEMY_HA_FLAGS_OFF     0x07         // u8 WWENEMY_HAF_*
#define WWENEMY_HA_ATTYPE_OFF    0x08         // u32 cCcD_ObjAt mType
#define WWENEMY_HA_ATP_OFF       0x0C         // u8
#define WWENEMY_HA_SE_OFF        0x0D         // u8 dCcD_GObjAt mSe (0 = 1, the sword)
#define WWENEMY_HA_HITMARK_OFF   0x0E         // u8 dCcD_GObjAt mHitMark (0 = 0xD Nrm)
#define WWENEMY_HA_SPL_OFF       0x0F         // u8 dCcD_GObjAt mSpl (1 = kill blow)
#define WWENEMY_HA_POS_OFF       0x10         // f32 x, y, z
#define WWENEMY_HA_PID_OFF       0x1C         // u32 the host's pid of the target (Go resolves it from the wire handle; the REL checks proc via the kind table, setID, room and pid)
#define WWENEMY_HA_SIZE          0x20         // ends at +0x120 = WWENEMY_SEQ_TAIL_OFF

#define WWENEMY_HAF_FORCE_V1     0x01         // the v1 shrink branch (WW_ENEMY_HIT_V1=1)
#define WWENEMY_HAF_FALLBACK_SHRINK 0x02      // shrink after two Sets crossed nothing (WW_ENEMY_HIT_FALLBACK=shrink)
#define WWENEMY_HAF_NO_HITMARK   0x04         // dCcG_AtSPrm_NoHitMark on the sphere
// 0x08 and 0x10 are WWREMOTE_HAF_ATTRIB_BODY / _SLOT1 (wwremote.h). The TN
// batch's AM2 direct apply (enemy-mirror-tn.json R-15, D10, hit_path): the
// client REL's verdict, carried by Go from the hit entry's pending byte
// (WWENEMY_HP_*) through HIT byte 54. The host REL reads them only for AM2
// collider 1 while AM2's own WWENEMY_HKD_SPECIAL is clear; otherwise the
// request takes the sphere path as before.
#define WWENEMY_HAF_VERDICT      0x20         // the client judged this AM2 weak-spot hit (WWENEMY_HAF_BACK says which way)
#define WWENEMY_HAF_BACK         0x40         // with VERDICT: the client's Link was behind (>= EMK_AM2_BACK_MIN), else HD_FRONT
#define WWENEMY_HAF_CRIT         0x80         // with VERDICT: Go saw a crit cut type (d_a_am2.cpp:241-260; hammer: HAMMER_SIDESWING): m2CE 1 / 8

// --- REL-written span: +0x124..+0x198 -------------------------------------
#define WWENEMY_ACK_SEQ_OFF      0x124        // u32 last seq consumed
#define WWENEMY_ACK_FRAME_OFF    0x128        // u32 WWREL frame counter at consume
#define WWENEMY_ADOPTED_OFF      0x12C        // u32 bit n = slot n adopted
#define WWENEMY_EPOCH_SEEN_OFF   0x130        // u32
#define WWENEMY_STATUS_OFF       0x134        // status[4] x WWENEMY_STATUS_SIZE
#define WWENEMY_STATUS_SIZE      0x10
#define WWENEMY_HITS_OFF         0x174        // hit[4] x 4: u16 hit_seq, u8 collider, u8 pending
#define WWENEMY_HIT_SIZE         0x04
// the hit entry's pending byte (+3): any nonzero value is pending (Go
// clears it to 0); the REL writes PENDING alone, or for an AM2 collider-1
// hit with AM2's HKD_SPECIAL clear PENDING | VERDICT (| BACK) (TN batch R-14)
#define WWENEMY_HP_PENDING       0x01
#define WWENEMY_HP_VERDICT       0x02         // the client REL judged the bearing (AM2 weak spot)
#define WWENEMY_HP_BACK          0x04         // with VERDICT: cLib_distanceAngleS(shape.y, angle to this game's Link) >= EMK_AM2_BACK_MIN
#define WWENEMY_HIT_APPLY_ACK_OFF 0x184       // u32 hit_apply seq acked (result: WWENEMY_HIT_RESULT_OFF)
#define WWENEMY_CANARIES_OFF     0x188        // u8 pause_reasserts, u8 anm_unresolved, u8 revalidate_failures, u8 watchdog_releases
#define WWENEMY_CANARIES2_OFF    0x18C        // u8 anm_res_null, u8 anm_range, u8 hit_nocross, u8 hit_aborted
#define WWENEMY_HIT_RESULT_OFF   0x190        // u32 BE: byte3 WWENEMY_HR_*, byte2 WWENEMY_HD_*, byte1 REL executes from Set to result, byte0 Sets issued
#define WWENEMY_KNOB_SEEN_OFF    0x194        // u16 layout 4 (D4): the last knob_gen whose table the REL validated (0 none); Go rewrites the table while it differs
#define WWENEMY_FP_MISS_OFF      0x196        // u8  layout 4 (D10): saturating count of BB tail fingerprint misses (the collapsed tail ran) and, batch D R-D6, MT builder fingerprint misses (adoption refused)
#define WWENEMY_TAIL_SPARE_OFF   0x197        // u8  spare (the former reserved u32's last byte)

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
#define WWENEMY_STATE_DEAD       6            // the quiet death deleted the copy (fopAcM_delete__FUi); Go unpins the slot on it

#define WWENEMY_ERR_NONE         0
#define WWENEMY_ERR_PID          1            // fopAcM_SearchByID found nothing
#define WWENEMY_ERR_PROC         2            // layout 2's proc != 0x00BD; layout 3 reports WWENEMY_ERR_KIND instead
#define WWENEMY_ERR_INIT         3            // initState != 2
#define WWENEMY_ERR_SETID        4            // setID neither < 0x200 nor 0xFFFF, or home room != block room
#define WWENEMY_ERR_MORF         5            // mpMorf or model null
#define WWENEMY_ERR_RANGE        6            // NaN or |pos| > 1e6
#define WWENEMY_ERR_MODE         7            // mo2 mMode 5 (carried) or 0xF (statue); 0 and 1 (lantern/jail variant) are mirrored
#define WWENEMY_ERR_KIND         8            // no kind row for the proc, actor proc != slot proc, or the kind's bit clear in kind_mask
#define WWENEMY_ERR_GATE         9            // the kind's static/runtime gate refused (BK/CC; MO2 keeps ERR_MODE): may pass a frame later
#define WWENEMY_ERR_KEY          10           // em_pkey(actor) != slot pkey: treated as pid 0
#define WWENEMY_ERR_ICE          11           // adopt found the copy's own ice timers running (K1 or a local death owns it): final for Go

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
#define WWENEMY_HR_PARRIED       4            // the target's defence sphere took it (MO2, BK)

// hit_apply details (byte2 of WWENEMY_HIT_RESULT_OFF); 11..15 are Go-side drops that never reach the REL,
// 16 is WWREMOTE_HD_NO_BODY (wwremote.h, written by em_hit_publish)
#define WWENEMY_HD_NONE          0
#define WWENEMY_HD_NO_CROSS      1            // two Sets, nothing crossed
#define WWENEMY_HD_INVULN        2            // the kind's invuln still set after the wait, or v1's pre-drop
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
#define WWENEMY_HD_KIND          17           // the resolved actor's kind has no row or its bit is clear in the host's kind_mask (16 is WWREMOTE_HD_NO_BODY)
#define WWENEMY_HD_DIRECT        18           // with HR_APPLIED_AT: AM2 direct apply ran cc_at_check and the week_atari_check tail (no sphere Set)
#define WWENEMY_HD_FRONT         19           // with HR_DROPPED: an AM2 verdict hit from the front (HAF_VERDICT without HAF_BACK)

// remote-Link owner ids (remote.c rl_mirror_owner; apply +0x04, ring
// +0x08, PHIT [18] are all u16): < 0x200 a keyed local setID;
// WWENEMY_OWNER_SLOT_BASE + j (j < WWENEMY_MAX_SLOTS, exactly) block slot
// j; anything else (0xFFFF, 0xFE04..) the stand-in. Client Go writes
// these; a raw wire handle never reaches the REL.
#define WWENEMY_OWNER_SLOT_BASE  0xFE00

// --- the annex below the block (mailbox-relative) ---------------------------
// Go writes it in the same write as the block, first; the REL uses it only
// when annex_seq == seq_head == seq_tail of the block it consumed, and
// writes the aux values onto a copy only when the slot was adopted before
// this consume, apid == the new pid and the actor's proc == the cached
// proc (enemy_mirror.h em_ext). MO2 does not read it.
#define WWENEMY_ANNEX_OFF        0x1C8        // mailbox-relative: aux[4] x WWENEMY_ANNEX_AUX_SIZE, block slot j at +j*8
#define WWENEMY_ANNEX_SIZE       0x38         // the annex, +0x1C8..+0x200 (== WWENEMY_OFF)
#define WWENEMY_ANNEX_AUX_SIZE   0x08         // s16 aux[4]: BK [0] m48C.z -> +0x800, [1] jaw m11F4 -> +0x11F4; CC [0] shape_angle.x -> +0x20C, [1..3] m310 x/y/z -> +0x310/+0x312/+0x314
#define WWENEMY_ANNEX_SEQ_OFF    0x1E8        // mailbox-relative: u32 == the block seq written in the same span
#define WWENEMY_ANNEX_OVR_OFF    0x1EC        // mailbox-relative: per kind k < WWENEMY_ANNEX_KINDS, WWENEMY_ANNEX_OVR_SIZE bytes at +k*4
#define WWENEMY_ANNEX_OVR_SIZE   0x04         // u8 hook_disable (WWENEMY_HKD_*), u8 quiet_frames, s16 tg_hit_up; 0 = the compiled default
#define WWENEMY_ANNEX_OVR_HOOKS_OFF 0x00      // u8 WWENEMY_HKD_*
#define WWENEMY_ANNEX_OVR_QUIET_OFF 0x01      // u8 quiet-death frames (0 = EMK_QUIET_FRAMES, or EMK_QUIET_FRAMES_LIGHT under SF2_LIGHT)
#define WWENEMY_ANNEX_OVR_TG_UP_OFF 0x02      // s16 collider-2 centre height (0 = EMK_*_TG_HIT_UP)
#define WWENEMY_ANNEX_INVW_OFF   0x1F8        // mailbox-relative: u8 invuln_wait[WWENEMY_ANNEX_KINDS] (0 = EMK_*_INVULN_WAIT), u8 pad
#define WWENEMY_ANNEX_KINDS      3            // overrides cover kinds 0..2 (MO2, BK, CC); ids >= 3 use their compiled quiet frames, tg_hit_up and invuln_wait
// The TN batch's one live knob (R-13, layout_decision): the invuln waits'
// pad byte, 0 = compiled. Layout 3 stays: no other annex byte moves.
#define WWENEMY_ANNEX_TN_TGUP_OFF 0x1FB       // mailbox-relative: u8 TN collider-2 centre height (1..255, 0 = EMK_TN_TG_HIT_UP); == WWENEMY_ANNEX_INVW_OFF + WWENEMY_ANNEX_KINDS
// The allow batch's hook bytes (enemy-mirror-allow.json D3, R-1): kind id
// WWENEMY_ANNEX_KINDS + j's u8 hook_disable at +j, the annex's last four
// bytes. The REL reads it directly at the point of use (em_hkd), not
// cached in em_ext, so a change tears for at most one frame.
#define WWENEMY_ANNEX_HKDX_OFF   0x1FC        // mailbox-relative: u8 hook_disable[WWENEMY_ANNEX_HKDX_KINDS] for kind ids 3..6 (WWENEMY_HKD_*)
#define WWENEMY_ANNEX_HKDX_KINDS 4            // ids WWENEMY_ANNEX_KINDS .. +3 (KI, AM, AM2, TN at 0x1FF); ends at WWENEMY_OFF: ids 7..14 use WWENEMY_HKD_HI_OFF (layout 4)

// --- layout 4: the knob table (mailbox-relative; enemy-mirror-batch-c.json
// layout4.knob_table, D3) ------------------------------------------------------
// Live quiet / tg_hit_up / invuln_wait overrides for kind ids 3..15, which
// the annex has no room for. Go writes it as its own write, before the
// first block whose WWENEMY_KNOB_GEN_OFF names its gen, after every arm or
// epoch bump and on any knob change (a new gen each time), and again
// whenever the block's WWENEMY_KNOB_SEEN_OFF differs while acks advance
// (savestate load, clobber). The REL's em_consume takes it as valid (em_ext
// EM_X_KNOB_OK) only when knob_gen != 0 && gen_head == gen_tail == knob_gen
// && layout == WWENEMY_LAYOUT && kinds >= EMK_KIND_COUNT, then echoes
// knob_gen at WWENEMY_KNOB_SEEN_OFF. Invalid = the compiled values: it
// carries no kill switch (ids 7..14 hooks are WWENEMY_HKD_HI_OFF in the
// block; the hook byte here is read for id 15 only). Ids 0..2 keep the
// annex overrides and ids 3..6 the HKDX bytes; the table's rows 0..6 hook
// byte is ignored. TN's tg_hit_up keeps WWENEMY_ANNEX_TN_TGUP_OFF as its
// single source (a nonzero table value for id 6 is not read).
#define WWENEMY_KNOB_OFF         0x850        // mailbox-relative: 0x80413750, past the remote-Link block (wwremote.h) in the free +0x850..+0x1100
#define WWENEMY_KNOB_SIZE        0x60         // +0x850..+0x8B0
#define WWENEMY_KNOB_KINDS       16           // rows per field, indexed by kind id
#define WWENEMY_KNOB_GEN_HEAD_OFF 0x00        // u32 == knob_gen of the blocks that name this table
#define WWENEMY_KNOB_LAYOUT_OFF  0x04         // u16 WWENEMY_LAYOUT
#define WWENEMY_KNOB_COUNT_OFF   0x06         // u8  rows written (WWENEMY_KNOB_KINDS); the REL needs >= EMK_KIND_COUNT
#define WWENEMY_KNOB_OVR_OFF     0x08         // ovr[WWENEMY_KNOB_KINDS] x WWENEMY_ANNEX_OVR_SIZE: u8 hook (id 15 only), u8 quiet_frames, s16 tg_hit_up (WWENEMY_ANNEX_OVR_*_OFF); 0 = compiled
#define WWENEMY_KNOB_INVW_OFF    0x48         // u8 invuln_wait[WWENEMY_KNOB_KINDS], 0 = compiled
#define WWENEMY_KNOB_GEN_TAIL_OFF 0x58        // u32 == gen_head, the table's last word
#define WWENEMY_KNOB_ID_MIN      3            // the first id the table serves (ids < 3: the annex overrides)

// hook_disable bits (annex override byte 0): a set bit turns that kind's
// hook off without a rebuild. TN (byte 0x1FF) gives MODEL_EQ, ATTACH, EYE,
// LEGACY_DEATH, MTX_PLAIN and SPECIAL its own meanings (wwenemy_kinds.h
// EMK_TN_HKD_*, TN batch R-3); AM2's SPECIAL also turns its direct apply
// off on both sides (D10). Batch C (D7): BK's SPECIAL is "ambush"
// (EMK_BK_HKD_AMBUSH); BB and MT give the bits their own names
// (EMK_BB_HKD_*, EMK_MT_HKD_*) but keep ATTN 0x10 and EXTRA 0x80 at their
// generic meaning, whose readers fire for every kind.
#define WWENEMY_HKD_MODEL_EQ     0x01         // skip the actor->model == morf model check
#define WWENEMY_HKD_ATTACH       0x02         // skip the attach hook (EMK_ATTACH_BK)
#define WWENEMY_HKD_EYE          0x04         // skip the eye hook (EMK_EYE_CC, EMK_EYE_FIXED)
#define WWENEMY_HKD_LEGACY_DEATH 0x08         // never take the legacy em_die: always the quiet path
#define WWENEMY_HKD_ATTN         0x10         // skip the per-frame attention_info.flags write
#define WWENEMY_HKD_MTX_PLAIN    0x20         // skip EMK_MTX_PLAIN (KI, AM, AM2)
#define WWENEMY_HKD_SPECIAL      0x40         // skip the kind's special path (AM: the bomb swallow, EMK_HIT_SPECIAL, R-13; AM2: direct apply, TN batch D10)
#define WWENEMY_HKD_EXTRA        0x80         // skip the kind's extras: the btk/brk play (R-10) and the per-frame collider radius writes (R-5)

// --- REL-owned static collision objects past the block (from MAILBOX_ADDR) --
#define WWENEMY_SPH_OFF          0x3A0        // dCcD_Sph image, 0x12C bytes: the host's v2 At sphere
#define WWENEMY_STTS_OFF         0x4D0        // dCcD_Stts, 0x3C bytes: its stts, Init'd from the host Link
#define WWENEMY_REGION_END       0x510        // end of everything enemy sync owns (mailbox-relative)

#define WWENEMY_STALE_FRAMES     90           // REL releases all after this many frames without a new seq

#endif
