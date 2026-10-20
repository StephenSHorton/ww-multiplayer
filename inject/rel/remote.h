// remote.h: remote-Link in the REL (remote.c): the keeper's swap state,
// the lite body's layout and the entry points rel_main.c and
// enemy_mirror.c call. Design: ww-multiplayer-handoff/design/
// remote-link-design.md sections 5 (REL), 6 (mailbox), 7.1 (detection),
// 8.5 (client apply), 8.6 (attribution) and its "Step -1 results"; the
// mailbox contract is include/wwremote.h.
//
// One profile, two roles of the 0xB5 actor, chosen by mParameters
// (+0xB0, f_pc_base.h:30): 0 is the keeper (rel_main.c's wwrel_actor: the
// Moblin mirror plus remote_swap_state at WWREMOTE_KEEPER_SWAP_OFF);
// WWREMOTE_BODY_PARAMS_TAG | slot << 8 | gen is a lite body (host only,
// remote_body below). The profile's mSize is WWREMOTE_ACTOR_SIZE for both.
//
// The swap state lives in the keeper, never in .bss: the T2 exec shim's
// target (wwrel_exec_hook) and the bodies find it through the mailbox word
// WWREL_SWAP_STATE_OFF, which the keeper's create writes and its delete
// (and _epilog) clear.
#ifndef WW_REMOTE_H
#define WW_REMOTE_H

#include "wwapi.h"
#include "mailbox_mp.h"
#include "wwremote.h"
#include "enemy_mirror.h"

#define RL_LATCH_MAX 16          // design section 5.4: 16 tracked enemies

// One tracked enemy (section 5.4). Targets are named by pid; 0 = the
// host Link.
typedef struct {
    u32 enemy_pid;      // +0x00 0 = free entry
    u32 target_pid;     // +0x04 the latched target (0 = host Link)
    u32 force_pid;      // +0x08 ATTRIB's forced target (0 = none)
    u8  hold;           // +0x0C frames on the current target (saturating)
    u8  force_frames;   // +0x0D executes the force still holds
    u8  is_mo2;         // +0x0E 1 = proc 0x00BD: its m714 may be rewritten
    u8  _pad;           // +0x0F
} rl_latch;             // 0x10

// One body slot's lifecycle (section 5.6), runtime slots only
// (MAX_REMOTE_LINKS); body_status entries past them stay zero.
typedef struct {
    u32 actor;          // +0x00 the body while LIVE (0 otherwise, or once its delete ran)
    u32 pid;            // +0x04 fopAcM_create's result
    u8  state;          // +0x08 WWREMOTE_BODY_* (PARKED is reported from `parked`)
    u8  err;            // +0x09 WWREMOTE_BERR_* (the last one)
    u8  tries;          // +0x0A creates issued in this stage
    u8  gen;            // +0x0B the slot gen the body was created for
    u16 cooldown;       // +0x0C frames before the next create
    u16 frames_live;    // +0x0E keeper executes while LIVE (saturating)
    u8  hits;           // +0x10 hits detected (saturating)
    u8  del_sent;       // +0x11 fopAcM_deleteByID accepted the delete
    u8  parked;         // +0x12 the body's own verdict (its execute, section 5.2 step 1)
    u8  _pad;           // +0x13
} rl_body;              // 0x14

// Client apply ("v2 in reverse", section 8.5)
#define RL_AP_IDLE   0
#define RL_AP_SET    1           // the sphere was registered; read its At record next execute
#define RL_AP_DAMAGE 2           // crossed and not guarded; read Link's mode and life next execute

typedef struct {
    // intake (the block, enemy-block rules)
    u32 last_seq;       // +0x00 the block seq last consumed (0 = none)
    u32 stale_frames;   // +0x04 keeper executes since
    u32 epoch_seen;     // +0x08
    u32 frame;          // +0x0C the WWREL frame of the last keeper execute
    u16 flags;          // +0x10 WWREMOTE_FLAG_* while the block is valid, else 0
    u8  valid;          // +0x12 magic + layout + role, consumed, not stale
    u8  role;           // +0x13 WWREMOTE_ROLE_*
    u8  swap_ready;     // +0x14 the hook's fast-path byte: valid host, SWAP, a candidate body, no stage change
    u8  live_mask;      // +0x15 bit i: body i LIVE, unparked, TARGETABLE and not NO_HIT
    u8  margin_q8;      // +0x16 tunables of the last consume
    u8  min_hold;       // +0x17
    u8  body_iframes;   // +0x18
    u8  _pad0[3];       // +0x19
    f32 dy_max;         // +0x1C
    u8  stage[8];       // +0x20 the stage the create tries belong to
    rl_body bodies[MAX_REMOTE_LINKS]; // +0x28
    // client apply
    u32 ap_seq;         // +0x50 the open request's seq
    u32 ap_ack;         // +0x54 the last seq taken
    u32 ap_link;        // +0x58 the client Link the request was Set against
    cXyz ap_rel;        // +0x5C hit point - body pos on the host
    cXyz ap_rvec;       // +0x68 the host Tg RVec
    u16 life_before;    // +0x74
    u8  ap_state;       // +0x76 RL_AP_*
    u8  ap_frames;      // +0x77 executes from the first Set (saturating)
    u8  ap_sets;        // +0x78 cCcS::Set calls for the request
    u8  _pad1[3];       // +0x79
    rl_latch latch[RL_LATCH_MAX]; // +0x7C
} remote_swap_state;    // 0x17C

// The lite body (section 5.2), host only: the fopAc_ac_c head, the few
// daPy_py_c fields the Moblin reads (section 3 audit), a REL-owned copy of
// __vt__9daPy_py_c with stubs, a dCcD_Cyl Tg copied from the host Link and
// its own dCcD_Stts, then the REL's fields. Past WWREMOTE_BODY_END are the
// REL's private words (not part of the Go contract).
typedef struct {
    u8  base[0x290];    // +0x000 fopAc_ac_c
    u8  cut_type;       // +0x290 daPy_py_c mCutType (d_a_player.h:479)
    u8  cut_count;      // +0x291 mCutCount
    u8  _p0[2];
    s16 damage_wait;    // +0x294 mDamageWaitTimer image (diagnostic)
    u8  _p1[0x2B0 - 0x296];
    f32 grab_wear;      // +0x2B0 field_0x2b0 = 0.0 (checkGrabWear false, d_a_player.h:596)
    u8  _p2[0x304 - 0x2B4];
    u8  demo[0x31C - 0x304]; // +0x304 daPy_demo_c mDemo, stays zero
    u32 vptr;           // +0x31C daPy_py_c vptr (d_a_player.h:500) = &vt
    u32 vt[WWREMOTE_BODY_VT_SIZE / 4]; // +0x320 copy of __vt__9daPy_py_c, stubbed
    u8  _p3[0x3C0 - 0x3BC];
    u8  cyl[WWREMOTE_BODY_CYL_SIZE];   // +0x3C0 dCcD_Cyl, Link's mCyl image
    u8  stts[WWREMOTE_BODY_STTS_SIZE]; // +0x4F0 dCcD_Stts
    u8  _p4[0x530 - 0x52C];
    u32 magic;          // +0x530 WWREMOTE_BODY_MAGIC
    u8  slot;           // +0x534
    u8  gen;            // +0x535
    u8  iframes_left;   // +0x536 provisional i-frames (FLAG_IFRAME_STAMP)
    u8  state;          // +0x537 WWREMOTE_BODY_LIVE or _PARKED as the body sees itself
    s16 shield_angle;   // +0x538 the cyl's mpShieldFrontRangeYAngle target
    u8  _p5[2];
    f32 hand_mtx[3][4]; // +0x53C identity rotation at the position (vt+0x08/+0x0C stubs)
    u32 last_pose_seq;  // +0x56C the pose-feed seq byte last seen
    u32 park_since;     // +0x570 the WWREL frame parked at (0 while LIVE)
    // REL-private, past WWREMOTE_BODY_END
    u32 pose_seq_frame; // +0x574 the WWREL frame last_pose_seq changed at
} remote_body;          // 0x578

// --- rel_main.c ------------------------------------------------------------
// The role of an instance of the profile: 1 = a lite body, 0 = the keeper.
int  remote_is_body(void* actor);
// keeper create / execute (after enemy_mirror_frame) / delete
void remote_keeper_init(remote_swap_state* st);
void remote_keeper_frame(remote_swap_state* st, enemy_mirror_state* mirror, u32 frame);
void remote_keeper_delete(remote_swap_state* st);
// _epilog: forget everything if the keeper's delete never ran
void remote_unlink(void);
// the lite body's methods
int  remote_body_create(void* actor);
int  remote_body_delete(void* actor);
int  remote_body_execute(void* actor);
// The T2 exec shim's target (ABI of fpcMtd_Execute, 0x8003EF38, which it
// calls exactly once); _prolog publishes its address at WWREL_EXEC_HOOK_OFF.
int  wwrel_exec_hook(void* mtd, void* actor);

// --- enemy_mirror.c (host v2 hit, section 8.6 ATTRIB) -----------------------
// The LIVE, unparked body of the slot ha_flags names when ATTRIB is on and
// the request asks for it (WWREMOTE_HAF_ATTRIB_BODY), else 0.
u32  remote_attrib_body(u8 ha_flags);
// Force the Moblin's latch onto the body for 3 executes; 1 when the first
// Set should wait one keeper execute (ATTACKER on and the body is not the
// current target, so damage_check's bearing is computed toward it).
int  remote_attrib_force(u32 moblin, u32 body);

#endif
