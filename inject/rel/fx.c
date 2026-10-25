// fx.c: other players' bombs, boat-cannon shots and arrows made real in
// this world (ww-online review idea 5; design: ww-multiplayer-handoff/
// archive/ww-online-review/DESIGN-big-items.md section (a), option A).
// The slack contract is include/wwfx.h; Go's side is mods/multiplayer/
// fxsync (broadcast-pose drains the outbox, puppet-sync fills the inbox).
// Adapted from datstarkey/ww-online GameMod/src/puppet_link/puppet_fx.c,
// MIT (notice at the end of this file): the sender's tracker, the copy
// table and its fuse-holding state machine, the detached arrow.
//
// SENDER (send magic set). Once per frame from the keeper's execute (list
// 7, after Link's), the local Link (mpPlayerPtr[0], never
// dComIfGp_getPlayer(0), which is the NPC the player controls) is watched:
//   - a hand bomb is the daBomb_c in mActorKeepGrab (+0x318C) in STATE_1..3
//     with a fuse left (d_a_bomb3.inc procCarry_init / procWait_init), or a
//     Bomb Flower's daBomb2::Act_c (fuse mBombTimer +0x738, exploding at
//     mState 2, d_a_bomb2.cpp:1088); tracked from the first frame he
//     carries it. BOMB_THROW the frame it left his hands, BOMB_PICKUP when
//     he carries it again, EXPLODE when its fuse is 0 (procExplode_init
//     zeroes mRestTime, :873), REMOVE when it is gone without exploding;
//   - the boat's cannonball is the one BOMB with exactly the ship's params
//     (prm_make(STATE_4, FALSE, TRUE) = 0x80020004, d_a_ship.cpp:4021,
//     d_a_bomb_static.cpp:91-93; enemy cannons set cheapEff) on a frame
//     the ship's mStateFlag (+0x358, d_a_ship.h:313) has
//     daSFLG_SHOOT_CANNON_e (0x20000, :38; set at d_a_ship.cpp:4035,
//     cleared at its next execute, :3594): CANNON, then EXPLODE / REMOVE;
//   - an arrow is the daArrow_c nocked in mActorKeepEquip (+0x317C): the
//     frame it left the bow with params 1 (the shot, d_a_player_bow.inc:142)
//     it is reported once, ARROW with its type (mArrowType +0x601), its
//     position and its aim (current.angle).
// Every event carries the projectile's own current.roomNo, so the
// receiver scopes it to the room where it happened.
//
// VIEWER (recv magic set). The inbox's events become real actors made by
// fopAcM_fastCreate in the local Link's layer (fx_create; the keeper lives
// in the root layer, so a copy made in the keeper's own layer would
// outlive the stage), as the player's bombs and the ship's cannonball are.
// Option A: every copy is world-only.
//   - Bombs: a lit STATE_1 bomb at the sender's position with their speeds
//     and fuse, the ship's STATE_4 cannonball with its no-gravity frames,
//     or a STATE_0 bomb (it explodes at create, ATTR_STATE_1) for an
//     EXPLODE with no copy. Each has WWFX_BOMB_PRM_COPY in its params
//     (wwfx.h: never reported back), field_0x6F0 cleared and VsEnemy |
//     VsPlayer cleared in its At SPrm (+0x5A0, mSph's cCcD_ObjAt; the
//     source is GrpAll, d_a_bomb3.inc:1384, and procExplode_init only sets
//     the Set bit, :853, so the cleared bits hold through the explosion).
//     The At check runs at the next dCcS Move, after this execute, so a
//     STATE_0 copy's create-time registration already sees them cleared.
//     Its fuse is held at FX_HOLD_FUSE (after its own count reached it)
//     for FX_EXPLODE_GRACE frames past the sender's, so it explodes when
//     the sender's EXPLODE arrives, snapped to the sender's position
//     (procExplode_init, as theirs). Only while it is as spawned: once
//     something here carries it, changes its state (an Armos Knight's
//     swallow) or sets its fuse lower, it is this world's bomb and its own
//     fuse ends it. It keeps the copy bit then (their rule; the design's
//     note to clear it would make this world's tracker send it back to its
//     sender as a new bomb), so it never hurts an enemy here either.
//   - Arrows: a real daArrow_c, detached (no parent, so checkCreater finds
//     no Zelda), with the peer's type in daArrow_c::m_keep_type and the
//     magic raised to 2 around the synchronous create only (setTypeByPlayer
//     -> checkRestMp would make it a normal arrow, d_a_arrow.cpp:1024-1041;
//     the light arrow's heap and model are chosen at create). createInit
//     put it in the local Link's hand (setKeepMatrix), so it is moved to
//     the sender's launch point and aim; its Co sphere's Set bit is cleared
//     (a stuck arrow touched by the local Link would give him one,
//     procStop_BG), VsEnemy is cleared in its At SPrm (+0x39C; the source
//     is Set | VsEnemy | VsOther, :28), its mCurrProcFunc (+0x68C) is the
//     procMove PTMF procWait copies (@5221, 0x803898B4 in the DOL: 0, -1,
//     0x800D5B20) and arrowShooting runs, as procWait does on the shot
//     (:602-611). Its params stay 0, so procWait (which spends the local
//     magic) never runs and its effect child (d_a_arrow_lighteff.cpp:
//     318-330) never sets the local Link's USE_ARROW_EFFECT. Fire and
//     forget: its own procMove ends it.
// A stage change (NEXT_STAGE_ENABLE) discards the inbox and deletes the
// live bomb copies once; the copies die with the stage layer anyway.
//
// Enemy damage stays on the host-authoritative relay: no copy has VsEnemy,
// so em_hits never sees a copy's hit, and the Armos Knight swallow skips
// the report for a copy (fx_swallowed_copy).

#include "mailbox_core.h"
#include "wwfx.h"
#include "fx.h"

#define FX_W(off)           (*(volatile u32*)(MAILBOX_ADDR + WWFX_OFF + (off)))
#define FX_AF(a, t, off)    (*(volatile t*)((u8*)(a) + (off)))

typedef char fx_check_block[(WWFX_OUT_RING_OFF + WWFX_RING * WWFX_EV_SIZE == WWFX_IN_RING_OFF &&
                             WWFX_IN_RING_OFF + WWFX_RING * WWFX_EV_SIZE == WWFX_SIZE &&
                             MAILBOX_ADDR + WWFX_OFF + WWFX_SIZE <= 0x80414000 &&
                             (WWFX_RING & (WWFX_RING - 1)) == 0) ? 1 : -1];
// The copy bit is none of daBomb_c's Prm_e fields (d_a_bomb.h:149-160:
// state 0-7, cheapEff 16, angXZero 17, version 31).
typedef char fx_check_copy_bit[((WWFX_BOMB_PRM_COPY & 0x800300FFu) == 0) ? 1 : -1];

// --- game functions (GZLE01, zeldaret/tww config/GZLE01/symbols.txt) ----
typedef void* (*fx_fast_create_t)(s16 proc, u32 prm, cXyz* pos, int room, csXyz* angle, cXyz* scale, s8 arg, void* cb, void* data);
#define fx_fastCreate        ((fx_fast_create_t)0x80024614)  // fopAcM_fastCreate__FsUlP4cXyziP5csXyzP4cXyzScPFPv_iPv (sym:647)
typedef void* (*fx_layer_get_t)(void);
typedef void (*fx_layer_set_t)(void* layer);
#define fx_CurrentLayer      ((fx_layer_get_t)0x8003D96C)    // fpcLy_CurrentLayer__Fv (sym:1119)
#define fx_SetCurrentLayer   ((fx_layer_set_t)0x8003D964)    // fpcLy_SetCurrentLayer__FP11layer_class (sym:1118)
typedef void* (*fx_judge_t)(void* p, void* data);
typedef void* (*fx_iter_judge_t)(fx_judge_t judge, void* data);
#define fx_IterJudge         ((fx_iter_judge_t)0x80023FB4)   // fopAcIt_Judge__FPFPvPv_PvPv (sym:632)
typedef int (*fx_this_t)(void* a);
#define fx_procExplode_init  ((fx_this_t)0x800DB024)         // procExplode_init__8daBomb_cFv (sym:4133)
#define fx_arrowShooting     ((fx_this_t)0x800D4B70)         // arrowShooting__9daArrow_cFv (sym:4020)
typedef void (*fx_trans_t)(void* m, f32 x, f32 y, f32 z);
#define fx_PSMTXTrans        ((fx_trans_t)0x8030D618)        // PSMTXTrans (sym:13034)
typedef void (*fx_rot_t)(void* m, s16 x, s16 y, s16 z);
#define fx_ZXYrotM           ((fx_rot_t)0x8000CC84)          // mDoMtx_ZXYrotM__FPA4_fsss (sym:130)
#define FX_PROC_MOVE_FN      0x800D5B20                      // procMove__9daArrow_cFv (sym:4031)

// --- game globals ---------------------------------------------------------
#define FX_MAGIC_NOW         (*(volatile u8*)0x803C4C1C)     // dComIfGs_getMagic: save status A +0x14 (d_save.h:49; wwapi/addr Magic)
#define FX_ARROW_KEEP_TYPE   (*(volatile u8*)0x803F6B7C)     // daArrow_c::m_keep_type (sym:18763)
#define FX_LINK              ((u32)PLAYER_PTR_ARRAY[0])      // mpPlayerPtr[0]
#define FX_SHIP              ((u32)PLAYER_PTR_ARRAY[2])      // mpPlayerPtr[2]

// --- layouts (decomp offsets) ---------------------------------------------
#define AC_PROC_ID           0x004   // fpc_ProcID mBsPcId (f_pc_base.h)
#define AC_PROC_NAME         0x008   // s16 mProcName
#define AC_INIT_STATE        0x00C   // s8 mInitState: 3 = being deleted (f_pc_deletor.cpp:89)
#define AC_LAYER             0x02C   // layer_class* mLyTg.mpLayer (+0x18 + create_tag_class 0x14)
#define AC_PARAMS            0x0B0   // u32 mParameters
#define AC_STATUS            0x1C4   // u32 actor_status (f_op_actor.h:284)
#define AC_STTS_CARRY        0x2000  // fopAcStts_CARRY_e (fopAcM_checkCarryNow)
#define AC_OLD_POS           0x1E4   // cXyz old.pos
#define AC_CUR_POS           0x1F8   // cXyz current.pos
#define AC_CUR_ANGLE         0x204   // csXyz current.angle
#define AC_CUR_ROOM          0x20A   // s8 current.roomNo (actor_place +0x12)
#define AC_SHAPE_ANGLE       0x20C   // csXyz shape_angle
#define AC_SPEED_Y           0x224   // f32 speed.y
#define AC_SPEED_F           0x254   // f32 speedF
#define AC_GRAVITY           0x258   // f32 gravity
#define LK_EQUIP_ID          0x317C  // daPy_lk_c mActorKeepEquip.mID (d_a_player_main.h:2086)
#define LK_GRAB_ID           0x318C  // daPy_lk_c mActorKeepGrab.mID (:2088)
#define SHIP_STATE_FLAG      0x358   // daShip_c mStateFlag (d_a_ship.h:313)
#define SHIP_SHOOT_CANNON    0x00020000 // daSFLG_SHOOT_CANNON_e (d_a_ship.h:38)
#define BOMB_AT_SPRM         0x5A0   // daBomb_c mSph (d_a_bomb.h:205) cCcD_ObjAt.mSPrm
#define BOMB_OWNED_BY_LINK   0x6F0   // u8 field_0x6F0 (:214)
#define BOMB_REST_TIME       0x6FC   // s16 mRestTime (:223)
#define BOMB_NO_GRAVITY      0x700   // s16 mNoGravityTime (:225)
#define BOMB2_STATE          0x694   // int daBomb2::Act_c mState (d_a_bomb2.h:203)
#define BOMB2_TIMER          0x738   // int mBombTimer (:211)
#define ARROW_MODEL          0x294   // J3DModel* mpModel (d_a_arrow.h:73)
#define ARROW_AT_SPRM        0x39C   // dCcD_Cps mAtCps (:79) cCcD_ObjAt.mSPrm
#define ARROW_CO_SPRM        0x500   // dCcD_Sph mCoSph (+0x4D4, :80) cCcD_ObjCo.mSPrm (+0x2C)
#define ARROW_TYPE           0x601   // u8 mArrowType (:82)
#define ARROW_PROC_FUNC      0x68C   // daArrow_c_ProcFunc mCurrProcFunc (:101), a 12-byte PTMF
#define J3D_BASE_MTX         0x24    // J3DModel::mBaseTransformMtx

#define PROC_PLAYER          0x00A9  // fpcNm_PLAYER_e (f_pc_name.h:178)
#define PROC_SHIP            0x00A7  // fpcNm_SHIP_e (:176)
#define PROC_BOMB            0x0128  // fpcNm_BOMB_e (:308)
#define PROC_BOMB2           0x0129  // fpcNm_Bomb2_e (:309)
#define PROC_ARROW           0x01DE  // fpcNm_ARROW_e (:491)

#define CC_AT_VS_ENEMY_PLAYER 0x06   // cCcD_AtSPrm_VsEnemy_e | cCcD_AtSPrm_VsPlayer_e (c_cc_d.h:22-23)
#define CC_AT_VS_ENEMY       0x02
#define CC_CO_SET            0x01    // cCcD_CoSPrm_Set_e (c_cc_d.h:47)

#define BOMB_PRM_STATE_MASK  0xFF
#define BOMB_PRM_VERSION     0x80000000 // prm_make's version bit (d_a_bomb_static.cpp:92)
#define BOMB_STATE_EXPLODE   0       // STATE_0: explodes at create (L_attrState A1, d_a_bomb3.inc:42)
#define BOMB_STATE_LIT       1       // STATE_1: a lit bomb in the world
#define BOMB_STATE_NEW       3       // STATE_3: fresh from the bag (sets field_0x6F0, :1462-1466)
#define BOMB_SHIP_CANNON_PRM 0x80020004 // prm_make(STATE_4, FALSE, TRUE)
#define BOMB2_STATE_EXPLODE  2       // daBomb2 mode_explode_init (d_a_bomb2.cpp:1088)
#define ARROW_SHOT_PARAM     1       // d_a_player_bow.inc:142, d_a_arrow.cpp:602

#define FX_TRACK_MAX         6       // the local Link's bombs and cannonballs in the air
#define FX_COPY_MAX          8
#define FX_EVENTS_PER_FRAME  4
#define FX_HOLD_FUSE         3       // a held copy's mRestTime: its execute counts it to 2; lower was the game
#define FX_MAX_FUSE          600
#define FX_EXPLODE_GRACE     60      // frames past the sender's fuse a copy waits for their EXPLODE
#define FX_LEAVE_FRAMES      30      // REMOVE: the copy gets this long to sink here too, then it goes
#define FX_DONE_FRAMES       300     // a finished entry is remembered this long (late duplicates)
#define FX_MAX_NO_GRAVITY    60
#define FX_MAX_COORD         1.0e6f
#define FX_MAX_SPEED         1000.0f
#define FX_MAX_GRAVITY       50.0f

enum { FX_TRACK_FREE = 0, FX_TRACK_CARRIED, FX_TRACK_FLYING };
enum { FX_COPY_FREE = 0, FX_COPY_LIVE, FX_COPY_LEAVING, FX_COPY_EXPLODED, FX_COPY_GONE };

typedef struct {
    u32 pid;
    s16 name;      // PROC_BOMB or PROC_BOMB2
    u8  state;     // FX_TRACK_*
    s8  room;      // its current.roomNo when last seen (for REMOVE)
} fx_track;

typedef struct {
    u32 id;        // the sender's projectile
    u32 pid;       // our copy
    u16 age;       // frames in this state
    s16 hold;      // a LIVE hand bomb's fuse is held while age < hold
    s16 name;      // PROC_BOMB or PROC_ARROW
    u8  slot;      // the sender's player id
    u8  state;     // FX_COPY_*
    u8  cannon;    // a cannonball: no fuse (ATTR_STATE_200 skips checkExplodeTimer)
    u8  local;     // this world took it over: its own fuse ends it
    u8  pad[2];
} fx_copy;

typedef struct {
    cXyz pos;
    f32 speedF, speedY, gravity;
    csXyz angle;
    s16 timer;
    u32 id;
    u8 kind, slot, variant;
} fx_event;

// REL data: a new link starts empty (old copies finish as plain bombs).
static fx_track l_fx_track[FX_TRACK_MAX];
static fx_copy l_fx_copy[FX_COPY_MAX];
static u32 l_fx_arrow;      // the local Link's nocked arrow, 0 = none
static u8 l_fx_linked;      // the read words were adopted
static u8 l_fx_stage;       // this stage change was handled

static int fx_mem1(u32 p) {
    return p >= 0x80000000u && p < 0x83000000u;
}

// A live actor of that proc by process id, or 0.
static u32 fx_find(u32 pid, s16 name) {
    void* a = 0;
    if (pid == 0 || pid == fpcM_ERROR_PROCESS_ID_e) return 0;
    if (!fopAcM_SearchByID(pid, &a) || !fx_mem1((u32)a)) return 0;
    if (FX_AF(a, s8, AC_INIT_STATE) == 3 || FX_AF(a, s16, AC_PROC_NAME) != name) return 0;
    return (u32)a;
}

// Frames left on a tracked bomb's fuse, 0 once it is exploding.
static s16 fx_fuse(u32 a) {
    s32 t;
    if (FX_AF(a, s16, AC_PROC_NAME) != PROC_BOMB2) return FX_AF(a, s16, BOMB_REST_TIME);
    t = FX_AF(a, s32, BOMB2_TIMER);
    if (FX_AF(a, s32, BOMB2_STATE) == BOMB2_STATE_EXPLODE || t <= 0) return 0;
    return t > 0x7FFF ? 0x7FFF : (s16)t;
}

int fx_swallowed_copy(void* bomb) {
    if (FX_AF(bomb, s16, AC_PROC_NAME) != PROC_BOMB || (FX_AF(bomb, u32, AC_PARAMS) & WWFX_BOMB_PRM_COPY) == 0) return 0;
    FX_W(WWFX_STAT_SWALLOW_OFF)++;
    return 1;
}

// --- sender: the local Link's projectiles -> outbox -----------------------

// One event from actor a (position, speeds, angles, timer, room), or with
// no actor at at in room.
static void fx_emit(u8 kind, u32 id, u32 a, const cXyz* at, u8 variant, s8 room) {
    u32 w = FX_W(WWFX_OUT_WRITE_OFF);
    u8* ev;
    const cXyz* pos;
    int i;
    if (w - FX_W(WWFX_OUT_READ_OFF) >= WWFX_RING) {
        FX_W(WWFX_STAT_DROPPED_OFF)++;  // Go isn't draining
        return;
    }
    ev = (u8*)(MAILBOX_ADDR + WWFX_OFF + WWFX_OUT_RING_OFF + (w % WWFX_RING) * WWFX_EV_SIZE);
    for (i = 0; i < WWFX_EV_SIZE; i += 4) FX_AF(ev, u32, i) = 0;
    pos = a ? (const cXyz*)(a + AC_CUR_POS) : at;
    FX_AF(ev, u8, WWFX_EV_KIND) = kind;
    FX_AF(ev, u8, WWFX_EV_VARIANT) = variant;
    FX_AF(ev, s8, WWFX_EV_ROOM) = a ? FX_AF(a, s8, AC_CUR_ROOM) : room;
    FX_AF(ev, u32, WWFX_EV_ID) = id;
    FX_AF(ev, f32, WWFX_EV_POS) = pos->x;
    FX_AF(ev, f32, WWFX_EV_POS + 4) = pos->y;
    FX_AF(ev, f32, WWFX_EV_POS + 8) = pos->z;
    if (a) {
        FX_AF(ev, f32, WWFX_EV_SPEED_F) = FX_AF(a, f32, AC_SPEED_F);
        FX_AF(ev, f32, WWFX_EV_SPEED_Y) = FX_AF(a, f32, AC_SPEED_Y);
        FX_AF(ev, f32, WWFX_EV_GRAVITY) = FX_AF(a, f32, AC_GRAVITY);
        FX_AF(ev, s16, WWFX_EV_ANGLE) = FX_AF(a, s16, AC_CUR_ANGLE);
        FX_AF(ev, s16, WWFX_EV_ANGLE + 2) = FX_AF(a, s16, AC_CUR_ANGLE + 2);
        FX_AF(ev, s16, WWFX_EV_ANGLE + 4) = FX_AF(a, s16, AC_SHAPE_ANGLE + 4);
        if (kind != WWFX_KIND_ARROW)  // (an arrow has no timer; these are daBomb_c fields)
            FX_AF(ev, s16, WWFX_EV_TIMER) = kind == WWFX_KIND_CANNON ? FX_AF(a, s16, BOMB_NO_GRAVITY) : fx_fuse(a);
    }
    FX_W(WWFX_OUT_WRITE_OFF) = w + 1;  // the entry first, then the seq
}

static fx_track* fx_track_of(u32 pid) {
    int i;
    for (i = 0; i < FX_TRACK_MAX; i++)
        if (l_fx_track[i].state != FX_TRACK_FREE && l_fx_track[i].pid == pid) return &l_fx_track[i];
    return 0;
}

static fx_track* fx_track_add(u32 pid, s16 name, u8 state) {
    int i;
    for (i = 0; i < FX_TRACK_MAX; i++) {
        fx_track* t = &l_fx_track[i];
        if (t->state == FX_TRACK_FREE) {
            t->pid = pid;
            t->name = name;
            t->state = state;
            t->room = -1;
            return t;
        }
    }
    return 0;  // full: this one isn't shared
}

static void fx_track_clear(void) {
    int i;
    for (i = 0; i < FX_TRACK_MAX; i++) l_fx_track[i].state = FX_TRACK_FREE;
    l_fx_arrow = 0;
}

// A lit hand bomb of ours: STATE_1..3 (not a Tingle bomb, a bomb nut or a
// cannonball), with a fuse, not a copy.
static int fx_is_hand_bomb(u32 a) {
    u32 prm = FX_AF(a, u32, AC_PARAMS);
    u32 st = prm & BOMB_PRM_STATE_MASK;
    return (prm & WWFX_BOMB_PRM_COPY) == 0 && st >= BOMB_STATE_LIT && st <= BOMB_STATE_NEW &&
           FX_AF(a, s16, BOMB_REST_TIME) > 0;
}

// fopAcIt_Judge callback: the boat's new cannonball (exact params: never a
// copy or an enemy cannon's), not tracked yet.
void* fx_cannon_judge(void* p, void* data) {
    u32 a = (u32)p;
    (void)data;
    if (!fx_mem1(a) || FX_AF(a, s16, AC_PROC_NAME) != PROC_BOMB || FX_AF(a, u32, AC_PARAMS) != BOMB_SHIP_CANNON_PRM ||
        FX_AF(a, s8, AC_INIT_STATE) == 3 || FX_AF(a, s16, BOMB_REST_TIME) == 0 || fx_track_of(FX_AF(a, u32, AC_PROC_ID)))
        return 0;
    return p;
}

static void fx_track_local(u32 link) {
    u32 grab = FX_AF(link, u32, LK_GRAB_ID);
    u32 equip = FX_AF(link, u32, LK_EQUIP_ID);
    u32 nocked, ship;
    int i;

    if (grab != 0 && grab != fpcM_ERROR_PROCESS_ID_e && !fx_track_of(grab)) {
        u32 b = fx_find(grab, PROC_BOMB);
        if (b && fx_is_hand_bomb(b)) {
            fx_track_add(grab, PROC_BOMB, FX_TRACK_CARRIED);
        } else if (!b) {
            // A Bomb Flower's bomb, lit when plucked: the viewers' copy is a
            // plain daBomb_c (the same model).
            u32 f = fx_find(grab, PROC_BOMB2);
            if (f && fx_fuse(f) > 0) fx_track_add(grab, PROC_BOMB2, FX_TRACK_CARRIED);
        }
    }

    // The nocked arrow is no longer in the bow: shot (params 1), or put
    // away / swapped (deleted, or params 0).
    nocked = fx_find(equip, PROC_ARROW);
    if (l_fx_arrow != 0 && (!nocked || equip != l_fx_arrow)) {
        u32 shot = fx_find(l_fx_arrow, PROC_ARROW);
        if (shot && FX_AF(shot, u32, AC_PARAMS) == ARROW_SHOT_PARAM && FX_AF(shot, u8, ARROW_TYPE) <= WWFX_ARROW_MAX)
            fx_emit(WWFX_KIND_ARROW, l_fx_arrow, shot, 0, FX_AF(shot, u8, ARROW_TYPE), 0);
    }
    l_fx_arrow = nocked ? equip : 0;

    ship = FX_SHIP;
    if (fx_mem1(ship) && FX_AF(ship, s16, AC_PROC_NAME) == PROC_SHIP && (FX_AF(ship, u32, SHIP_STATE_FLAG) & SHIP_SHOOT_CANNON)) {
        u32 ball = (u32)fx_IterJudge(fx_cannon_judge, 0);
        if (fx_mem1(ball) && fx_track_add(FX_AF(ball, u32, AC_PROC_ID), PROC_BOMB, FX_TRACK_FLYING))
            fx_emit(WWFX_KIND_CANNON, FX_AF(ball, u32, AC_PROC_ID), ball, 0, 0, 0);
    }

    for (i = 0; i < FX_TRACK_MAX; i++) {
        fx_track* t = &l_fx_track[i];
        u32 a;
        int carried;
        if (t->state == FX_TRACK_FREE) continue;
        a = fx_find(t->pid, t->name);
        if (!a) {
            // Gone without exploding (sank, fell out of the world). A
            // carried one had no copy.
            if (t->state == FX_TRACK_FLYING)
                fx_emit(WWFX_KIND_REMOVE, t->pid, 0, (const cXyz*)(link + AC_CUR_POS), 0, t->room);
            t->state = FX_TRACK_FREE;
            continue;
        }
        t->room = FX_AF(a, s8, AC_CUR_ROOM);
        if (fx_fuse(a) == 0) {
            fx_emit(WWFX_KIND_EXPLODE, t->pid, a, 0, 0, 0);
            t->state = FX_TRACK_FREE;
            continue;
        }
        carried = grab == t->pid;
        if (t->state == FX_TRACK_CARRIED && !carried) {
            fx_emit(WWFX_KIND_BOMB_THROW, t->pid, a, 0, 0, 0);
            t->state = FX_TRACK_FLYING;
        } else if (t->state == FX_TRACK_FLYING && carried) {
            fx_emit(WWFX_KIND_BOMB_PICKUP, t->pid, a, 0, 0, 0);
            t->state = FX_TRACK_CARRIED;
        }
    }
}

// --- viewer: inbox -> copies ----------------------------------------------

static fx_copy* fx_copy_of(u8 slot, u32 id) {
    int i;
    for (i = 0; i < FX_COPY_MAX; i++) {
        fx_copy* c = &l_fx_copy[i];
        if (c->state != FX_COPY_FREE && c->slot == slot && c->id == id) return c;
    }
    return 0;
}

// A free entry, else the oldest finished one; 0 while every entry is a
// copy still in the air.
static fx_copy* fx_copy_alloc(u8 slot, u32 id) {
    fx_copy* best = 0;
    int i;
    for (i = 0; i < FX_COPY_MAX; i++) {
        fx_copy* c = &l_fx_copy[i];
        if (c->state == FX_COPY_FREE) {
            best = c;
            break;
        }
        if ((c->state == FX_COPY_EXPLODED || c->state == FX_COPY_GONE) && (!best || c->age > best->age)) best = c;
    }
    if (best) {
        best->slot = slot;
        best->id = id;
        best->pid = 0;
        best->age = 0;
        best->hold = 0;
        best->name = PROC_BOMB;
        best->cannon = 0;
        best->local = 0;
        best->state = FX_COPY_GONE;
    }
    return best;
}

// fopAcM_fastCreate in the local Link's layer (the stage's), not the
// keeper's (the root layer: a copy there would outlive the stage). The
// create runs synchronously (fpcFCtRq_Request, f_pc_fstcreate_req.cpp:
// 28-53), which sets the current layer and does not restore it.
static u32 fx_create(s16 proc, u32 prm, cXyz* pos, csXyz* angle) {
    u32 layer = FX_AF(FX_LINK, u32, AC_LAYER);
    void* cur;
    u32 a;
    if (!fx_mem1(layer)) return 0;
    cur = fx_CurrentLayer();
    fx_SetCurrentLayer((void*)layer);
    a = (u32)fx_fastCreate(proc, prm, pos, -1, angle, 0, -1, 0, 0);
    fx_SetCurrentLayer(cur);
    return fx_mem1(a) ? a : 0;
}

static u32 fx_spawn(u32 prm, cXyz* pos, csXyz* angle) {
    u32 a = fx_create(PROC_BOMB, prm | WWFX_BOMB_PRM_COPY, pos, angle);
    if (!a) return 0;
    FX_AF(a, u8, BOMB_OWNED_BY_LINK) = 0;
    FX_AF(a, u32, BOMB_AT_SPRM) &= ~(u32)CC_AT_VS_ENEMY_PLAYER;
    FX_W(WWFX_STAT_SPAWNED_OFF)++;
    return a;
}

// Put a copy where the sender's projectile is, moving as it moves.
static void fx_place(u32 a, const fx_event* e) {
    FX_AF(a, cXyz, AC_CUR_POS) = e->pos;
    FX_AF(a, cXyz, AC_OLD_POS) = e->pos;
    FX_AF(a, f32, AC_SPEED_F) = e->speedF;
    FX_AF(a, f32, AC_SPEED_Y) = e->speedY;
    FX_AF(a, f32, AC_GRAVITY) = e->gravity;
    FX_AF(a, csXyz, AC_CUR_ANGLE) = e->angle;
    FX_AF(a, s16, AC_SHAPE_ANGLE + 2) = e->angle.y;
}

// A real arrow in flight from the sender's launch point (see the top).
static int fx_spawn_arrow(const fx_event* e) {
    u8 keep = FX_ARROW_KEEP_TYPE;
    u8 magic = FX_MAGIC_NOW;
    cXyz pos = e->pos;
    csXyz aim = e->angle;
    u32 a, model;
    volatile u32* proc;
    FX_ARROW_KEEP_TYPE = e->variant;
    if (magic < 2) FX_MAGIC_NOW = 2;  // use_mp: light 2, fire / ice 1 (d_a_arrow.cpp:1026-1031)
    a = fx_create(PROC_ARROW, 0, &pos, &aim);
    FX_MAGIC_NOW = magic;
    FX_ARROW_KEEP_TYPE = keep;
    if (!a) return 0;
    model = FX_AF(a, u32, ARROW_MODEL);
    if (!fx_mem1(model)) return 0;
    FX_AF(a, cXyz, AC_CUR_POS) = pos;
    FX_AF(a, cXyz, AC_OLD_POS) = pos;
    FX_AF(a, s16, AC_CUR_ANGLE) = e->angle.x;  // up is +x here, as setKeepMatrix sets it (d_a_arrow.cpp:552)
    FX_AF(a, s16, AC_CUR_ANGLE + 2) = e->angle.y;
    FX_AF(a, s16, AC_CUR_ANGLE + 4) = 0;
    FX_AF(a, s16, AC_SHAPE_ANGLE) = -e->angle.x;
    FX_AF(a, s16, AC_SHAPE_ANGLE + 2) = e->angle.y;
    FX_AF(a, s16, AC_SHAPE_ANGLE + 4) = 0;
    FX_AF(a, u32, ARROW_CO_SPRM) &= ~(u32)CC_CO_SET;
    FX_AF(a, u32, ARROW_AT_SPRM) &= ~(u32)CC_AT_VS_ENEMY;
    proc = (volatile u32*)(a + ARROW_PROC_FUNC);  // = the procMove PTMF procWait copies
    proc[0] = 0;
    proc[1] = 0xFFFFFFFFu;
    proc[2] = FX_PROC_MOVE_FN;
    // Its At capsule's only registration this frame: the arrow itself
    // first executes next frame (fpcCt_Handler queues it after this).
    fx_arrowShooting((void*)a);
    // Its model is still in the local Link's hand until its first
    // procMove: draw it at the launch point.
    fx_PSMTXTrans((void*)(model + J3D_BASE_MTX), pos.x, pos.y, pos.z);
    fx_ZXYrotM((void*)(model + J3D_BASE_MTX), -e->angle.x, e->angle.y, 0);
    FX_W(WWFX_STAT_SPAWNED_OFF)++;
    return 1;
}

static int fx_ok(f32 v, f32 limit) {
    return v > -limit && v < limit;  // false for NaN and infinity
}

static s16 fx_clamp(s16 v, s16 lo, s16 hi) {
    return v < lo ? lo : (v > hi ? hi : v);
}

static void fx_reject(void) {
    FX_W(WWFX_STAT_REJECTED_OFF)++;
}

static void fx_apply(const u8* ev) {
    fx_event e;
    fx_copy* c;
    u32 a;
    e.kind = FX_AF(ev, u8, WWFX_EV_KIND);
    e.slot = FX_AF(ev, u8, WWFX_EV_SLOT);
    e.variant = FX_AF(ev, u8, WWFX_EV_VARIANT);
    e.id = FX_AF(ev, u32, WWFX_EV_ID);
    e.pos.x = FX_AF(ev, f32, WWFX_EV_POS);
    e.pos.y = FX_AF(ev, f32, WWFX_EV_POS + 4);
    e.pos.z = FX_AF(ev, f32, WWFX_EV_POS + 8);
    e.speedF = FX_AF(ev, f32, WWFX_EV_SPEED_F);
    e.speedY = FX_AF(ev, f32, WWFX_EV_SPEED_Y);
    e.gravity = FX_AF(ev, f32, WWFX_EV_GRAVITY);
    e.angle.x = FX_AF(ev, s16, WWFX_EV_ANGLE);
    e.angle.y = FX_AF(ev, s16, WWFX_EV_ANGLE + 2);
    e.angle.z = FX_AF(ev, s16, WWFX_EV_ANGLE + 4);
    e.timer = FX_AF(ev, s16, WWFX_EV_TIMER);

    // Go checked it and that it happened in our stage and room; check the
    // values again.
    if (e.kind < WWFX_KIND_BOMB_THROW || e.kind > WWFX_KIND_MAX ||
        e.variant > (e.kind == WWFX_KIND_ARROW ? WWFX_ARROW_MAX : 0) ||
        !fx_ok(e.pos.x, FX_MAX_COORD) || !fx_ok(e.pos.y, FX_MAX_COORD) || !fx_ok(e.pos.z, FX_MAX_COORD) ||
        !fx_ok(e.speedF, FX_MAX_SPEED) || !fx_ok(e.speedY, FX_MAX_SPEED) || !fx_ok(e.gravity, FX_MAX_GRAVITY)) {
        fx_reject();
        return;
    }

    c = fx_copy_of(e.slot, e.id);
    a = c ? fx_find(c->pid, c->name) : 0;
    switch (e.kind) {
    case WWFX_KIND_BOMB_THROW:
        if (c && (c->state == FX_COPY_EXPLODED || c->state == FX_COPY_LEAVING || (a && c->local))) return;  // late, or this world's bomb now
        if (!a) {
            if (!c) c = fx_copy_alloc(e.slot, e.id);
            a = c ? fx_spawn(BOMB_PRM_VERSION | BOMB_STATE_LIT, &e.pos, &e.angle) : 0;
            if (!a) {
                if (c) c->state = FX_COPY_FREE;
                fx_reject();
                return;
            }
            c->pid = FX_AF(a, u32, AC_PROC_ID);
            c->name = PROC_BOMB;
            c->cannon = 0;
            c->local = 0;
        }
        fx_place(a, &e);
        FX_AF(a, s16, BOMB_REST_TIME) = fx_clamp(e.timer, FX_HOLD_FUSE, FX_MAX_FUSE);
        c->hold = FX_AF(a, s16, BOMB_REST_TIME) + FX_EXPLODE_GRACE;
        c->state = FX_COPY_LIVE;
        c->age = 0;
        return;

    case WWFX_KIND_CANNON:
        if (c) return;  // a duplicate
        c = fx_copy_alloc(e.slot, e.id);
        a = c ? fx_spawn(BOMB_SHIP_CANNON_PRM, &e.pos, &e.angle) : 0;
        if (!a) {
            if (c) c->state = FX_COPY_FREE;
            fx_reject();
            return;
        }
        fx_place(a, &e);  // d_a_ship.cpp:4026-4033: no-gravity frames, speeds, gravity
        FX_AF(a, s16, BOMB_NO_GRAVITY) = fx_clamp(e.timer, 0, FX_MAX_NO_GRAVITY);
        c->pid = FX_AF(a, u32, AC_PROC_ID);
        c->cannon = 1;
        c->state = FX_COPY_LIVE;
        c->age = 0;
        return;

    case WWFX_KIND_BOMB_PICKUP:
        // The sender carries it again.
        if (c && a && c->state == FX_COPY_LIVE && !c->local && !(FX_AF(a, u32, AC_STATUS) & AC_STTS_CARRY) &&
            FX_AF(a, s16, BOMB_REST_TIME) != 0) {
            fopAcM_deleteByID(c->pid);
            c->state = FX_COPY_FREE;
        }
        return;

    case WWFX_KIND_EXPLODE:
        if (c && c->state == FX_COPY_EXPLODED) return;
        if (a && c->local) return;  // this world's bomb now: its own fuse decides
        if (a && FX_AF(a, s16, BOMB_REST_TIME) != 0) {
            FX_AF(a, cXyz, AC_CUR_POS) = e.pos;
            FX_AF(a, cXyz, AC_OLD_POS) = e.pos;
            fx_procExplode_init((void*)a);
        } else if (!a) {
            // Nothing of ours to explode (it blew up in the sender's hands,
            // or ours sank): an explosion there.
            if (!c) c = fx_copy_alloc(e.slot, e.id);
            a = fx_spawn(BOMB_PRM_VERSION | BOMB_STATE_EXPLODE, &e.pos, 0);
            if (c) c->pid = a ? FX_AF(a, u32, AC_PROC_ID) : 0;
        }
        if (c) {
            c->state = FX_COPY_EXPLODED;
            c->age = 0;
        }
        return;

    case WWFX_KIND_REMOVE:
        if (c && c->state == FX_COPY_LIVE && !c->local) {  // (this world's bomb now: not the sender's to remove)
            c->state = FX_COPY_LEAVING;
            c->age = 0;
        }
        return;

    case WWFX_KIND_ARROW:
        if (c) return;  // a duplicate
        c = fx_copy_alloc(e.slot, e.id);
        if (!c || !fx_spawn_arrow(&e)) {
            if (c) c->state = FX_COPY_FREE;
            fx_reject();
            return;
        }
        c->name = PROC_ARROW;
        c->state = FX_COPY_EXPLODED;  // fire and forget: kept only so a repeat does nothing
        c->age = 0;
        return;
    }
}

static void fx_update_copies(void) {
    int i;
    for (i = 0; i < FX_COPY_MAX; i++) {
        fx_copy* c = &l_fx_copy[i];
        u32 a;
        s16 rest;
        if (c->state == FX_COPY_FREE) continue;
        if (c->age < 0xFFFF) c->age++;
        if (c->state == FX_COPY_EXPLODED || c->state == FX_COPY_GONE) {
            if (c->age > FX_DONE_FRAMES) c->state = FX_COPY_FREE;
            continue;
        }
        a = fx_find(c->pid, PROC_BOMB);
        if (!a || FX_AF(a, s16, BOMB_REST_TIME) == 0) {
            c->state = a ? FX_COPY_EXPLODED : FX_COPY_GONE;  // exploded here (something hit it) / sank here
            c->age = 0;
            continue;
        }
        if (c->state == FX_COPY_LEAVING && c->age > FX_LEAVE_FRAMES && !(FX_AF(a, u32, AC_STATUS) & AC_STTS_CARRY)) {
            fopAcM_deleteByID(c->pid);
            c->state = FX_COPY_GONE;
            c->age = 0;
            continue;
        }
        if (c->cannon || c->local) continue;
        // Taken over here: carried, swallowed or caught (an enemy changes
        // its state), or its fuse set lower than the hold leaves it.
        rest = FX_AF(a, s16, BOMB_REST_TIME);
        if ((FX_AF(a, u32, AC_STATUS) & AC_STTS_CARRY) ||
            (FX_AF(a, u32, AC_PARAMS) & BOMB_PRM_STATE_MASK) != BOMB_STATE_LIT || rest < FX_HOLD_FUSE - 1) {
            c->local = 1;
            FX_W(WWFX_STAT_LOCAL_OFF)++;
            continue;
        }
        // Wait for the sender's explosion.
        if ((s32)c->age < c->hold && rest < FX_HOLD_FUSE) FX_AF(a, s16, BOMB_REST_TIME) = FX_HOLD_FUSE;
    }
}

// The stage is going: forget the trackers, delete the live bomb copies
// (they would die with the stage layer anyway) and free the table.
static void fx_stage_clear(void) {
    int i;
    fx_track_clear();
    for (i = 0; i < FX_COPY_MAX; i++) {
        fx_copy* c = &l_fx_copy[i];
        if ((c->state == FX_COPY_LIVE || c->state == FX_COPY_LEAVING) && fx_find(c->pid, PROC_BOMB)) {
            fopAcM_deleteByID(c->pid);
            FX_W(WWFX_STAT_CLEARED_OFF)++;
        }
        c->state = FX_COPY_FREE;
    }
}

void fx_frame(void) {
    u32 link = FX_LINK;
    u32 r, w;
    int n;
    if (!l_fx_linked) {
        // The slack outlives the module: nothing written before this link
        // is sent or spawned.
        FX_W(WWFX_OUT_WRITE_OFF) = FX_W(WWFX_OUT_READ_OFF);
        FX_W(WWFX_IN_READ_OFF) = FX_W(WWFX_IN_WRITE_OFF);
        l_fx_linked = 1;
    }
    if (NEXT_STAGE_ENABLE || !fx_mem1(link) || FX_AF(link, s16, AC_PROC_NAME) != PROC_PLAYER) {
        // No spawning or reporting across a stage change; what arrived
        // meanwhile belongs to the old stage.
        FX_W(WWFX_IN_READ_OFF) = FX_W(WWFX_IN_WRITE_OFF);
        if (NEXT_STAGE_ENABLE && !l_fx_stage) {
            fx_stage_clear();
            l_fx_stage = 1;
        }
        fx_track_clear();
        return;
    }
    l_fx_stage = 0;

    fx_update_copies();

    r = FX_W(WWFX_IN_READ_OFF);
    w = FX_W(WWFX_IN_WRITE_OFF);
    if (FX_W(WWFX_RECV_MAGIC_OFF) != WWFX_MAGIC) {
        r = w;
    } else {
        if (w - r > WWFX_RING) r = w - WWFX_RING;  // (Go never overruns the ring; if it did, the oldest are lost)
        for (n = 0; r != w && n < FX_EVENTS_PER_FRAME; n++, r++)
            fx_apply((const u8*)(MAILBOX_ADDR + WWFX_OFF + WWFX_IN_RING_OFF + (r % WWFX_RING) * WWFX_EV_SIZE));
    }
    FX_W(WWFX_IN_READ_OFF) = r;

    if (FX_W(WWFX_SEND_MAGIC_OFF) == WWFX_MAGIC)
        fx_track_local(link);
    else
        fx_track_clear();
}

// Adapted from datstarkey/ww-online GameMod/src/puppet_link/puppet_fx.c,
// under this notice:
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
