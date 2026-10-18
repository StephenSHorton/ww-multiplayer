// enemy_mirror.c: the Moblin mirror (host-authoritative enemies, step S1 +
// the S3 colliders and the host v1 hit applier behind the block flags).
// Design: ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-mirror-design.json (rel_side.per_frame_wwrel_execute); docs:
// inject/rel/README.md "enemy_mirror.c".
//
// Go writes one block per tick into the mailbox slack at MAILBOX_ADDR +
// WWENEMY_OFF (inject/include/wwenemy.h): up to four Moblins (proc 0x00BD,
// d_a_mo2) named by pid, each with a target position, angle, tilt and
// flags. Every frame of the module actor (rel_main.c's execute, list 7:
// after Link's execute, before the draw phase and dScnPly's Ccsp()->Move)
// this unit:
//   0 intake   consumes the block when seq_head == seq_tail != last_seq
//              (a torn Go write keeps the previous targets for a frame),
//              acks it, forgets everything on an epoch change, and releases
//              everything after WWENEMY_STALE_FRAMES frames without a new seq;
//   1 scope    releases everything unless the game is in the block's stage
//              and room and no stage change is queued;
//   2 resolve  fopAcM_SearchByID(pid) and revalidates the actor every frame
//              (proc, initState, keyed setID + home room, mo2 mMode not carried
//              or statue, morf and model); adopts it by setting mPauseFlag bit 1 at proc+0x0B so
//              fpcEx_Execute skips daMo2_Execute (f_pc_executor.cpp), and
//              re-asserts the bit every frame because fopScnPause_Disable
//              clears it on every leaf at overlap wipes (f_op_scene_pause.cpp);
//   3 smooth   lerps toward the target (or snaps) and writes current.pos,
//              old.pos, current.angle.y, shape_angle.y and the tilt;
//   4 animate  mDoExt_McaMorf::play(NULL, 0, 0) so the morf chases and the
//              frame advances without the host-only footstep sounds;
//   5 matrix   dr_matrix_set on the Moblin's damagereaction, then PSMTXCopy
//              of *calc_mtx into the model's base matrix (also its cull
//              matrix), then McaMorf::calc so the joint callbacks run;
//   6 collide  (WWENEMY_FLAG_COLLIDERS) re-submits the Co/Tg/head (and in
//              defence the shield) colliders the way daMo2_Execute does, and
//              turns a Tg hit on the Tg cylinder or head sphere into a hit
//              entry for Go; never the weapon spheres, never health;
//   7 parry    mBtNowFrame = 100 (no opening) every mirrored frame;
//   8 release  clears bit 1, zeroes speed/speedF, old = current, puts the
//              Moblin into JYUNKAI mode -10 (a 60-frame wait, d_a_mo2.cpp
//              :1304-1313); death = release + health 0 + the light-arrow
//              shrink timer (enemy_ice, c_damagereaction.cpp:117-168);
//   host role  hit_apply: resolve the pid, validate, write the shrink
//              timer, ack with a result byte.
// Every game call is an absolute function-pointer cast (no relocation);
// no libc, no float library calls; the state is in the actor struct.
//
// Offsets are the zeldaret/tww GZLE01 decomp's: include/f_pc/f_pc_base.h
// (base_process_class), include/f_op/f_op_actor.h (fopAc_ac_c,
// fopEn_enemy_c), include/d/actor/d_a_mo2.h (mo2_class),
// include/c/c_damagereaction.h (damagereaction, enemyice),
// include/m_Do/m_Do_ext.h (mDoExt_McaMorf) with
// include/JSystem/J3DGraphAnimator/J3DAnimation.h (J3DFrameCtrl),
// include/SSystem/SComponent/c_cc_d.h and include/d/d_cc_d.h (the collider
// objects). Function addresses: config/GZLE01/symbols.txt.

#include "mailbox_core.h"
#include "wwrel.h"
#include "enemy_mirror.h"

// --- The block ------------------------------------------------------------
#define EB_ADDR      (MAILBOX_ADDR + WWENEMY_OFF)
#define EB_U32(off)  (*(volatile u32*)(EB_ADDR + (off)))
#define EB_U16(off)  (*(volatile u16*)(EB_ADDR + (off)))
#define EB_U8(off)   (*(volatile u8*)(EB_ADDR + (off)))
#define EB_S8(off)   (*(volatile s8*)(EB_ADDR + (off)))
#define EB_S16(off)  (*(volatile s16*)(EB_ADDR + (off)))
#define EB_F32(off)  (*(volatile f32*)(EB_ADDR + (off)))

// The block must lie past the REL words (wwrel.h) and below the arena
// (0x80414000, inject/build.py's OSInit patch), and its pieces must nest.
typedef char em_check_block_start[(WWENEMY_OFF >= WWREL_FRAME_OFF + 4 && WWENEMY_OFF >= sizeof(Mailbox)) ? 1 : -1];
typedef char em_check_block_end[(MAILBOX_ADDR + WWENEMY_OFF + WWENEMY_SIZE <= 0x80414000) ? 1 : -1];
typedef char em_check_slots[(WWENEMY_SLOTS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_SLOT_SIZE <= WWENEMY_HIT_APPLY_OFF) ? 1 : -1];
typedef char em_check_hit_apply[(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SIZE <= WWENEMY_SEQ_TAIL_OFF &&
                                 WWENEMY_HA_PID_OFF + 4 <= WWENEMY_HA_SIZE &&
                                 WWENEMY_HA_POS_OFF + 12 <= WWENEMY_HA_PID_OFF) ? 1 : -1];
typedef char em_check_go_span[(WWENEMY_SEQ_TAIL_OFF + 4 == WWENEMY_GO_SPAN &&
                               WWENEMY_ACK_SEQ_OFF == WWENEMY_GO_SPAN) ? 1 : -1];
typedef char em_check_status[(WWENEMY_STATUS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_STATUS_SIZE <= WWENEMY_HITS_OFF &&
                              WWENEMY_ST_FRAMES_OFF + 2 <= WWENEMY_STATUS_SIZE) ? 1 : -1];
typedef char em_check_hits[(WWENEMY_HITS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_HIT_SIZE <= WWENEMY_HIT_APPLY_ACK_OFF) ? 1 : -1];
typedef char em_check_tail[(WWENEMY_HIT_APPLY_ACK_OFF + 4 == WWENEMY_CANARIES_OFF &&
                            WWENEMY_CANARIES_OFF + 4 == WWENEMY_RESERVED2_OFF &&
                            WWENEMY_RESERVED2_OFF + 4 == WWENEMY_SIZE) ? 1 : -1];
typedef char em_check_slot_fields[(WWENEMY_SL_SAMPLE_OFF + 4 == WWENEMY_SLOT_SIZE &&
                                   WWENEMY_SL_RATE_OFF + 4 == WWENEMY_SL_SAMPLE_OFF) ? 1 : -1];
typedef char em_check_state[(sizeof(em_slot) == 0x3C &&
                             __builtin_offsetof(em_slot, recently_dead) == 0x38 &&
                             __builtin_offsetof(enemy_mirror_state, stage) == 0x20 &&
                             __builtin_offsetof(enemy_mirror_state, slots) == 0x2C &&
                             sizeof(enemy_mirror_state) == 0x11C) ? 1 : -1];

// --- Game data and functions (symbols.txt; absolute, no relocations) -----
// dComIfGp mCurStage.mName (char[8]): g_dComIfG_gameInfo 0x803C4C08 + play
// 0x12A0 + 0x3E94 (d_com_inf_game.h:723, docs/04 "mCurStage"; wwapi/addr
// CurStageName).
#define EM_CUR_STAGE_NAME ((volatile const u8*)0x803C9D3C)
// dStage_roomControl_c::mStayNo (s8), symbols.txt:18667.
#define EM_STAY_NO        (*(volatile s8*)0x803F6A78)
// calc_mtx (Mtx*), symbols.txt:18270: the matrix dr_matrix_set builds.
#define EM_CALC_MTX       (*(volatile Mtx**)0x803F63A0)
// dComIfG_Ccsp(): &g_dComIfG_gameInfo.play.mCcS = 0x803C4C08 + 0x12A0 +
// 0x1404 (d_com_inf_game.h:721,915,4420).
#define EM_CCS            ((void*)0x803C72AC)

// Member functions take `this` in r3 (C++ EABI); float arguments go in
// f1.. and s16/u8 in r4.. as the prototypes say, so plain C function
// pointers with the same parameter list call them correctly.
typedef BOOL (*em_morf_play_t)(void* morf, void* pos, u32 a, s8 b);  // play__14mDoExt_McaMorfFP3VecUlSc
#define McaMorf_play      ((em_morf_play_t)0x8001300C)
typedef void (*em_morf_calc_t)(void* morf);                          // calc__14mDoExt_McaMorfFv
#define McaMorf_calc      ((em_morf_calc_t)0x800132E8)
typedef void (*em_dr_matrix_set_t)(void* dr);                        // dr_matrix_set__FP14damagereaction
#define dr_matrix_set     ((em_dr_matrix_set_t)0x80022460)
typedef void (*em_mtx_copy_t)(const void* src, void* dst);           // PSMTXCopy
#define PSMTXCopy         ((em_mtx_copy_t)0x8030D0C8)
typedef void (*em_set_c_t)(void* shape, const cXyz* c);              // SetC__8cM3dGCylFRC4cXyz / SetC__8cM3dGSphFRC4cXyz
#define cM3dGCyl_SetC     ((em_set_c_t)0x80251D88)
#define cM3dGSph_SetC     ((em_set_c_t)0x8025238C)
typedef void (*em_ccs_set_t)(void* ccs, void* obj);                  // Set__4cCcSFP8cCcD_Obj
#define cCcS_Set          ((em_ccs_set_t)0x80242D04)
typedef void (*em_clr_tg_hit_t)(void* obj);                          // ClrTgHit__12dCcD_GObjInfFv
#define dCcD_GObjInf_ClrTgHit ((em_clr_tg_hit_t)0x800AB958)
typedef f32 (*em_sincos_t)(s16 a);                                   // cM_ssin__Fs / cM_scos__Fs
#define cM_ssin           ((em_sincos_t)0x800F0054)
#define cM_scos           ((em_sincos_t)0x800F0038)

// --- Actor offsets --------------------------------------------------------
// base_process_class (f_pc_base.h:13-32)
#define AC_PROC_NAME      0x008   // s16 mProcName
#define AC_PAUSE_FLAG     0x00B   // u8  mPauseFlag: bit 1 = execute paused (fpcPause_IsEnable)
#define AC_INIT_STATE     0x00C   // s8  mInitState: 2 = created
// fopAc_ac_c (f_op_actor.h:268-321)
#define AC_SETID          0x1BC   // u16 setID
#define AC_HOME           0x1D0   // actor_place home
#define AC_HOME_ROOM      0x1E2   // s8  home.roomNo (actor_place +0x12)
#define AC_OLD            0x1E4   // actor_place old (pos, angle, roomNo: 0x14 bytes)
#define AC_CURRENT        0x1F8   // actor_place current
#define AC_CUR_POS        0x1F8   // cXyz current.pos
#define AC_CUR_ANGLE_Y    0x206   // s16 current.angle.y
#define AC_SHAPE_ANGLE_Y  0x20E   // s16 shape_angle.y
#define AC_SPEED          0x220   // cXyz speed
#define AC_MODEL          0x24C   // J3DModel* model (d_a_mo2.cpp:4189 sets it to mpMorf's model)
#define AC_SPEEDF         0x254   // f32 speedF
#define AC_ATTN_FLAGS     0x280   // u32 attention_info.flags (+0x26C + 0x14)
#define AC_HEALTH         0x285   // s8 health
// fopEn_enemy_c (f_op_actor.h:329-358)
#define EN_BT_NOW_FRAME   0x2A4   // f32 mBtNowFrame; daMo2_Execute writes 100.0f when no parry opening (d_a_mo2.cpp:3668)
// mo2_class (d_a_mo2.h)
#define MO2_MODE          0x2BC   // u8  mMode = the low param byte at create (d_a_mo2.cpp:4080): 0 normal, 1 the lantern/jail
                                  //     variant (a normal Moblin once any pearl is held, rouya_mode 0, :4134-4146),
                                  //     5 carried (:4147), 0xF statue (:4163); M_Dai room 1's keyed three are modes 1, 1, 0
#define MO2_MODE_CARRIED  5
#define MO2_MODE_STATUE   0xF
#define MO2_MORF          0x2C4   // mDoExt_McaMorf* mpMorf
#define MO2_INVULN        0x5B4   // s16 m05B4: invulnerable frames
#define MO2_DR            0x614   // damagereaction mDamageReaction
#define MO2_DR_MODE       0x618   // s16 mDamageReaction.mMode
#define MO2_DR_ACTION     0x61A   // s16 mDamageReaction.mAction
#define MO2_TILT          0xAA0   // s16 mDamageReaction.m48C.x (+0x614 + 0x48C): the ground tilt dr_matrix_set rotates by
#define MO2_CO_CYL        0x208C  // dCcD_Cyl mCoCyl
#define MO2_TG_CYL        0x21BC  // dCcD_Cyl mTgCyl
#define MO2_HEAD_SPH      0x22EC  // dCcD_Sph mHeadSph
#define MO2_DEF_SPH       0x2418  // dCcD_Sph mDefenseSph
#define MO2_HEAD_CENTER   0x28C8  // cXyz m28C8: the head joint position (joint callback, d_a_mo2.cpp:367)
#define MO2_DEF_CENTER    0x2928  // cXyz m2928: the shield centre (d_a_mo2.cpp:2220,2248,3035)
#define MO2_ICE_SHRINK    0x2A5A  // s8 mEnemyIce.mLightShrinkTimer (+0x2A54 + 6, c_damagereaction.h:16)
// mDoExt_McaMorf (m_Do_ext.h:369-371) and J3DFrameCtrl (J3DAnimation.h:869-875)
#define MORF_MODEL        0x50    // J3DModel* mpModel
// collider objects: dCcD_Cyl/Sph = dCcD_GObjInf (0xF8) + cCcD_Cyl/SphAttr
// (cCcD_ShapeAttr 0x20 + cM3dGCyl/Sph, centre at +0); the cCcD_ObjHitInf
// base is at +0 with Tg at +0x18 (mRPrm +0x1C, mHitObj +0x20).
#define CC_TG_RPRM        0x1C    // u32: bit 0 = cCcD_TgRPrm_Hit_e
#define CC_SHAPE_CENTER   0x118   // cM3dGCyl/cM3dGSph (the SetC `this`)

#define PROC_MO2          0x00BD
#define MO2_ACTION_DEFENCE 10     // d_a_mo2.cpp:31
#define MO2_MODE_HANDBACK (-10)   // JYUNKAI mode -10: 60-frame wait (d_a_mo2.cpp:1304-1313)
#define EM_KEYED_SETID_MAX 0x200  // keyed room actors (docs/08 §7.2)
#define EM_SNAP_DIST2     40000.0f // 200 units squared
#define EM_LERP           0.25f
#define EM_RANGE          1000000.0f
#define EM_CO_FORWARD     35.0f   // mCoCyl centre: pos + 35 units along angle.y (d_a_mo2.cpp:3571-3577)
#define EM_BT_NO_OPENING  100.0f
#define EM_DEAD_LATCH     300
#define EM_HIT_COOLDOWN   8

#define AF(a, T, off)     (*(T*)((u8*)(a) + (off)))

// The live state, for _epilog: set at create, cleared at delete. A
// pointer only, valid for the module's own lifetime; forced into .data
// so the ELF carries no .bss.
static enemy_mirror_state* g_live __attribute__((section(".data"))) = (enemy_mirror_state*)0;

static void em_sat_inc(u8* c) {
    if (*c != 0xFF) (*c)++;
}

static void* em_actor_of(u32 pid) {
    fopAc_ac_c* a = 0;
    if (pid == 0 || !fopAcM_SearchByID(pid, &a) || !a) return 0;
    return a;
}

// --- Release ----------------------------------------------------------------
// Hand the Moblin back to its own code: clear our pause bit (the other
// bits stay as they are now: fpcPause_Disable's leaf case), no momentum,
// old = current so fopAc_Execute's velocity reads zero, and JYUNKAI mode
// -10 so daMo2_Execute waits 60 frames before patrolling.
static void em_handback(void* a, em_slot* sl) {
    u32* src = (u32*)((u8*)a + AC_CURRENT);
    u32* dst = (u32*)((u8*)a + AC_OLD);
    int i;
    AF(a, u8, AC_PAUSE_FLAG) &= (u8)~1u;
    AF(a, f32, AC_SPEED + 0) = 0.0f;
    AF(a, f32, AC_SPEED + 4) = 0.0f;
    AF(a, f32, AC_SPEED + 8) = 0.0f;
    AF(a, f32, AC_SPEEDF) = 0.0f;
    for (i = 0; i < 5; i++) dst[i] = src[i];     // actor_place: pos, angle, roomNo
    AF(a, s16, MO2_DR_ACTION) = 0;
    AF(a, s16, MO2_DR_MODE) = MO2_MODE_HANDBACK;
    if (sl->hit_withheld) {
        // A Tg hit kept for Go must not look like a fresh hit to the
        // Moblin's own damage_check.
        dCcD_GObjInf_ClrTgHit((u8*)a + MO2_TG_CYL);
        dCcD_GObjInf_ClrTgHit((u8*)a + MO2_HEAD_SPH);
    }
}

static void em_release(em_slot* sl) {
    if (sl->adopted) {
        void* a = em_actor_of(sl->apid);
        if (a && AF(a, s16, AC_PROC_NAME) == PROC_MO2) em_handback(a, sl);
        sl->adopted = 0;
        sl->state = WWENEMY_STATE_RELEASED;
    }
    sl->apid = 0;
    sl->actor = 0;
    sl->hit_withheld = 0;
    sl->hit_cooldown = 0;
}

// forget: also drop the dying latches (an epoch change starts over).
static void em_release_all(enemy_mirror_state* s, int forget) {
    int i;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &s->slots[i];
        em_release(sl);
        if (forget) {
            sl->recently_dead = 0;
            sl->state = WWENEMY_STATE_UNRESOLVED;
            sl->last_err = WWENEMY_ERR_NONE;
        }
    }
}

// --- Status ---------------------------------------------------------------
static void em_write_status(enemy_mirror_state* s) {
    u32 mask = 0;
    int i;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &s->slots[i];
        u32 off = WWENEMY_STATUS_OFF + i * WWENEMY_STATUS_SIZE;
        EB_U32(off + WWENEMY_ST_ACTOR_OFF) = sl->actor;
        EB_U32(off + WWENEMY_ST_PID_OFF) = sl->pid;
        EB_U8(off + WWENEMY_ST_STATE_OFF) = sl->state;
        EB_U8(off + WWENEMY_ST_PAUSE_PREV_OFF) = sl->pause_prev;
        EB_U8(off + WWENEMY_ST_LAST_ERR_OFF) = sl->last_err;
        EB_U8(off + WWENEMY_ST_LAST_ERR_OFF + 1) = 0;
        EB_U16(off + WWENEMY_ST_FRAMES_OFF) = sl->frames;
        EB_U16(off + WWENEMY_ST_FRAMES_OFF + 2) = 0;
        if (sl->adopted) mask |= 1u << i;
    }
    EB_U32(WWENEMY_ADOPTED_OFF) = mask;
    EB_U32(WWENEMY_EPOCH_SEEN_OFF) = s->epoch_seen;
    for (i = 0; i < 4; i++) EB_U8(WWENEMY_CANARIES_OFF + i) = s->canary[i];
}

// --- Intake ---------------------------------------------------------------
static int em_f32_bad(f32 v) {
    return v != v || v > EM_RANGE || v < -EM_RANGE;
}

static void em_consume(enemy_mirror_state* s, u32 seq, u32 frame) {
    int i;
    u8 count = EB_U8(WWENEMY_COUNT_OFF);
    if (count > WWENEMY_MAX_SLOTS) count = WWENEMY_MAX_SLOTS;
    s->count = count;
    s->flags = EB_U8(WWENEMY_FLAGS_OFF);
    s->role = EB_U8(WWENEMY_ROLE_OFF);
    s->room = EB_S8(WWENEMY_ROOM_OFF);
    for (i = 0; i < 8; i++) s->stage[i] = EB_U8(WWENEMY_STAGE_OFF + i);
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &s->slots[i];
        u32 off = WWENEMY_SLOTS_OFF + i * WWENEMY_SLOT_SIZE;
        if (i >= count) {
            sl->pid = 0;
            continue;
        }
        sl->pid = EB_U32(off + WWENEMY_SL_PID_OFF);
        sl->tgt_flags = EB_U8(off + WWENEMY_SL_FLAGS_OFF);
        {
            f32 x = EB_F32(off + WWENEMY_SL_POS_OFF);
            f32 y = EB_F32(off + WWENEMY_SL_POS_OFF + 4);
            f32 z = EB_F32(off + WWENEMY_SL_POS_OFF + 8);
            if (em_f32_bad(x) || em_f32_bad(y) || em_f32_bad(z)) {
                sl->last_err = WWENEMY_ERR_RANGE;      // keep the previous target
            } else {
                sl->tgt.x = x;
                sl->tgt.y = y;
                sl->tgt.z = z;
                sl->tgt_angle = EB_S16(off + WWENEMY_SL_ANGLE_OFF);
                sl->tgt_tilt = EB_S16(off + WWENEMY_SL_TILT_OFF);
                if (sl->last_err == WWENEMY_ERR_RANGE) sl->last_err = WWENEMY_ERR_NONE;
            }
        }
    }
    if (s->role == WWENEMY_ROLE_HOST) {
        s->ha_seq = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SEQ_OFF);
        s->ha_pid = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_PID_OFF);
        s->ha_setid = EB_U16(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SETID_OFF);
        s->ha_collider = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_COLLIDER_OFF);
    }
    s->last_seq = seq;
    s->stale_frames = 0;
    s->stale_released = 0;
    EB_U32(WWENEMY_ACK_SEQ_OFF) = seq;
    EB_U32(WWENEMY_ACK_FRAME_OFF) = frame;
}

// --- Scope ----------------------------------------------------------------
static int em_in_scope(const enemy_mirror_state* s) {
    int i;
    if (NEXT_STAGE_ENABLE != 0) return 0;
    if (EM_STAY_NO != s->room) return 0;
    for (i = 0; i < 8; i++) {
        u8 c = EM_CUR_STAGE_NAME[i];
        if (c != s->stage[i]) return 0;
        if (c == 0) break;
    }
    return 1;
}

// --- Resolve and revalidate ----------------------------------------------
// WWENEMY_ERR_NONE or the first failing check, in the design's order.
static u8 em_validate(void* a, s8 room) {
    void* morf;
    if (AF(a, s16, AC_PROC_NAME) != PROC_MO2) return WWENEMY_ERR_PROC;
    if (AF(a, s8, AC_INIT_STATE) != 2) return WWENEMY_ERR_INIT;
    if (AF(a, u16, AC_SETID) >= EM_KEYED_SETID_MAX || AF(a, s8, AC_HOME_ROOM) != room) return WWENEMY_ERR_SETID;
    {
        u8 mode = AF(a, u8, MO2_MODE);
        if (mode == MO2_MODE_CARRIED || mode == MO2_MODE_STATUE) return WWENEMY_ERR_MODE;
    }
    morf = AF(a, void*, MO2_MORF);
    if (!morf) return WWENEMY_ERR_MORF;
    if (!AF(morf, void*, MORF_MODEL) || AF(morf, void*, MORF_MODEL) != AF(a, void*, AC_MODEL)) return WWENEMY_ERR_MORF;
    return WWENEMY_ERR_NONE;
}

static void em_adopt(enemy_mirror_state* s, em_slot* sl, void* a, int i) {
    u32 off = WWENEMY_HITS_OFF + i * WWENEMY_HIT_SIZE;
    u8 pb = AF(a, u8, AC_PAUSE_FLAG);
    sl->pause_prev = pb;                 // bit 1 already set here = an orphan of an earlier unlink
    AF(a, u8, AC_PAUSE_FLAG) = pb | 1;
    sl->cur.x = AF(a, f32, AC_CUR_POS + 0);
    sl->cur.y = AF(a, f32, AC_CUR_POS + 4);
    sl->cur.z = AF(a, f32, AC_CUR_POS + 8);
    sl->cur_angle = AF(a, s16, AC_CUR_ANGLE_Y);
    sl->cur_tilt = AF(a, s16, MO2_TILT);
    AF(a, f32, AC_SPEED + 0) = 0.0f;
    AF(a, f32, AC_SPEED + 4) = 0.0f;
    AF(a, f32, AC_SPEED + 8) = 0.0f;
    AF(a, f32, AC_SPEEDF) = 0.0f;
    sl->snap = 1;
    sl->adopted = 1;
    sl->apid = sl->pid;
    sl->frames = 0;
    sl->hit_withheld = 0;
    sl->hit_cooldown = 0;
    // A clean hit entry and no stale Tg hit from the Moblin's own last frame.
    EB_U16(off + 0) = sl->hit_seq;
    EB_U8(off + 2) = 0;
    EB_U8(off + 3) = 0;
    dCcD_GObjInf_ClrTgHit((u8*)a + MO2_TG_CYL);
    dCcD_GObjInf_ClrTgHit((u8*)a + MO2_HEAD_SPH);
    (void)s;
}

// --- Death ----------------------------------------------------------------
// The proven K1 kill: release, then health 0 and the light-arrow shrink
// (enemy_ice runs at the top of daMo2_Execute, d_a_mo2.cpp:3379-3388, and
// c_damagereaction.cpp:117-168 shrinks, drops the item, sets the death
// switch and deletes the actor). The latch keeps later samples from
// adopting the shrinking copy.
static void em_die(em_slot* sl, void* a) {
    em_release(sl);
    AF(a, s8, AC_HEALTH) = 0;
    AF(a, s8, MO2_ICE_SHRINK) = 1;
    AF(a, u32, AC_ATTN_FLAGS) = 0;
    sl->state = WWENEMY_STATE_DYING;
    sl->recently_dead = EM_DEAD_LATCH;
}

// --- Colliders (S3) and hits ---------------------------------------------
// The hit entry for slot i: {u16 hit_seq, u8 collider, u8 pending}. The
// REL writes it once per hit; Go clears pending after reading the hit
// object (obj+0x20) and the collider centre (obj+0x118). While pending the
// hit collider is kept out of cCcS, so cCcS::ClrTgHitInf (which only walks
// the objects Set this frame) leaves its hit info for Go to read; when Go
// clears pending the REL clears the hit (ClrTgHit) and re-submits it.
static void em_hits(enemy_mirror_state* s, em_slot* sl, void* a, int i) {
    u32 off = WWENEMY_HITS_OFF + i * WWENEMY_HIT_SIZE;
    if (sl->hit_withheld) {
        if (EB_U8(off + 3) == 0) {
            dCcD_GObjInf_ClrTgHit((u8*)a + (sl->hit_withheld == 1 ? MO2_HEAD_SPH : MO2_TG_CYL));
            sl->hit_withheld = 0;
        }
    }
    if (sl->hit_cooldown) {
        sl->hit_cooldown--;
        return;
    }
    if (sl->hit_withheld) return;
    {
        u8 col = 0;
        if (AF(a, u32, MO2_HEAD_SPH + CC_TG_RPRM) & 1) col = 1;
        else if (AF(a, u32, MO2_TG_CYL + CC_TG_RPRM) & 1) col = 2;
        if (!col) return;
        sl->hit_seq++;
        EB_U16(off + 0) = sl->hit_seq;
        EB_U8(off + 2) = col;
        EB_U8(off + 3) = 1;
        sl->hit_withheld = col;
        sl->hit_cooldown = EM_HIT_COOLDOWN;
        // The other collider's hit (the same swing) must not fire next frame.
        dCcD_GObjInf_ClrTgHit((u8*)a + (col == 1 ? MO2_TG_CYL : MO2_HEAD_SPH));
    }
    (void)s;
}

// What daMo2_Execute does at its end (d_a_mo2.cpp:3565-3593): the Co
// cylinder 35 units ahead, the Tg cylinder at the position and the head
// sphere at the head joint unless invulnerable or defending, and the
// shield in defence (damage_check, :3034-3035). SetMass is left out
// (dCcS-specific, tunes the push weight only). Never the weapon spheres.
static void em_colliders(enemy_mirror_state* s, em_slot* sl, void* a, int i) {
    cXyz c;
    u8 f = sl->tgt_flags;
    em_hits(s, sl, a, i);
    c.x = sl->cur.x + EM_CO_FORWARD * cM_ssin(sl->cur_angle);
    c.y = sl->cur.y;
    c.z = sl->cur.z + EM_CO_FORWARD * cM_scos(sl->cur_angle);
    cM3dGCyl_SetC((u8*)a + MO2_CO_CYL + CC_SHAPE_CENTER, &c);
    cCcS_Set(EM_CCS, (u8*)a + MO2_CO_CYL);
    if (!(f & (WWENEMY_SF_INVULN | WWENEMY_SF_DEFENCE | WWENEMY_SF_RAGDOLL))) {
        if (sl->hit_withheld != 2) {
            cM3dGCyl_SetC((u8*)a + MO2_TG_CYL + CC_SHAPE_CENTER, &sl->cur);
            cCcS_Set(EM_CCS, (u8*)a + MO2_TG_CYL);
        }
        if (sl->hit_withheld != 1) {
            cM3dGSph_SetC((u8*)a + MO2_HEAD_SPH + CC_SHAPE_CENTER, (const cXyz*)((u8*)a + MO2_HEAD_CENTER));
            cCcS_Set(EM_CCS, (u8*)a + MO2_HEAD_SPH);
        }
    }
    if (f & WWENEMY_SF_DEFENCE) {
        cM3dGSph_SetC((u8*)a + MO2_DEF_SPH + CC_SHAPE_CENTER, (const cXyz*)((u8*)a + MO2_DEF_CENTER));
        cCcS_Set(EM_CCS, (u8*)a + MO2_DEF_SPH);
    }
}

// --- One slot, one frame --------------------------------------------------
static void em_slot_frame(enemy_mirror_state* s, int i) {
    em_slot* sl = &s->slots[i];
    void* a;
    void* morf;
    u8 err;

    sl->actor = 0;
    if (sl->recently_dead) {
        sl->recently_dead--;
        if (sl->recently_dead) return;           // the dying latch
        sl->state = WWENEMY_STATE_UNRESOLVED;
    }
    if (sl->adopted && sl->apid != sl->pid) em_release(sl);   // the slot now names another actor
    if (sl->pid == 0) {
        em_release(sl);
        return;
    }
    if (sl->tgt_flags & WWENEMY_SF_RELEASE) {
        em_release(sl);
        return;
    }
    a = em_actor_of(sl->pid);
    if (!a) {
        em_release(sl);                          // the actor is gone: nothing to restore
        sl->state = WWENEMY_STATE_MISSING;
        sl->last_err = WWENEMY_ERR_PID;
        return;
    }
    err = em_validate(a, s->room);
    if (err) {
        em_release(sl);
        sl->state = WWENEMY_STATE_REFUSED;
        sl->last_err = err;
        em_sat_inc(&s->canary[2]);
        return;
    }
    sl->actor = (u32)a;

    if (!sl->adopted) {
        em_adopt(s, sl, a, i);
    } else {
        // fopScnPause_Disable clears bit 1 on every leaf of the scene layer
        // (f_op_scene_pause.cpp:20-38 via fpcPause_Disable's recursion):
        // re-assert it, and count the frames it was found clear.
        u8 pb = AF(a, u8, AC_PAUSE_FLAG);
        if (!(pb & 1)) {
            em_sat_inc(&s->canary[0]);
            AF(a, u8, AC_PAUSE_FLAG) = pb | 1;
        }
    }

    if (sl->tgt_flags & WWENEMY_SF_DYING) {
        em_die(sl, a);
        return;
    }

    // 3 smoothing: raw or snap -> the target; else a quarter of the way,
    // snapping past 200 units; angles along the shortest arc.
    if ((s->flags & WWENEMY_FLAG_RAW) || sl->snap || (sl->tgt_flags & WWENEMY_SF_SNAP)) {
        sl->cur = sl->tgt;
        sl->cur_angle = sl->tgt_angle;
        sl->cur_tilt = sl->tgt_tilt;
        sl->snap = 0;
    } else {
        f32 dx = sl->tgt.x - sl->cur.x;
        f32 dy = sl->tgt.y - sl->cur.y;
        f32 dz = sl->tgt.z - sl->cur.z;
        s32 da = (s16)(sl->tgt_angle - sl->cur_angle);
        s32 dt = (s16)(sl->tgt_tilt - sl->cur_tilt);
        if (dx * dx + dy * dy + dz * dz > EM_SNAP_DIST2) {
            sl->cur = sl->tgt;
        } else {
            sl->cur.x += EM_LERP * dx;
            sl->cur.y += EM_LERP * dy;
            sl->cur.z += EM_LERP * dz;
        }
        sl->cur_angle = (s16)(sl->cur_angle + (s16)(da / 4));
        sl->cur_tilt = (s16)(sl->cur_tilt + (s16)(dt / 4));
    }
    AF(a, f32, AC_CUR_POS + 0) = sl->cur.x;
    AF(a, f32, AC_CUR_POS + 4) = sl->cur.y;
    AF(a, f32, AC_CUR_POS + 8) = sl->cur.z;
    AF(a, f32, AC_OLD + 0) = sl->cur.x;          // old.pos: fopAc_Execute is skipped, so we keep it
    AF(a, f32, AC_OLD + 4) = sl->cur.y;
    AF(a, f32, AC_OLD + 8) = sl->cur.z;
    AF(a, s16, AC_CUR_ANGLE_Y) = sl->cur_angle;
    AF(a, s16, AC_SHAPE_ANGLE_Y) = sl->cur_angle;
    AF(a, s16, MO2_TILT) = sl->cur_tilt;
    AF(a, f32, EN_BT_NOW_FRAME) = EM_BT_NO_OPENING;   // 7 no stale parry prompt

    // 4 animation (S1): the morf chases and the frame advances, no sounds
    // (play skips the bas sound when pos is NULL, m_Do_ext.cpp:1468-1470).
    morf = AF(a, void*, MO2_MORF);
    McaMorf_play(morf, 0, 0, 0);
    // 5 base matrix from the position, tilt and shape angle, into the
    // model's base matrix, which is also the cull matrix (d_a_mo2.cpp:4126);
    // then calc so the joint callbacks (head centre, eye position) follow.
    dr_matrix_set((u8*)a + MO2_DR);
    PSMTXCopy((const void*)EM_CALC_MTX, (u8*)AF(morf, void*, MORF_MODEL) + J3DMODEL_BASE_TR_MTX_OFFSET);
    McaMorf_calc(morf);

    // 6 colliders (S3): not while hidden or dying.
    if ((s->flags & WWENEMY_FLAG_COLLIDERS) && !(sl->tgt_flags & WWENEMY_SF_HIDDEN)) {
        em_colliders(s, sl, a, i);
    }

    if (sl->frames != 0xFFFF) sl->frames++;
    sl->state = WWENEMY_STATE_MIRRORED;
}

// --- Host role: the v1 hit applier -----------------------------------------
// Go hands over a client's HIT as (seq, the host's pid of that setID, the
// setID, the collider); the REL resolves and validates the actor, refuses
// what the Moblin's own damage_check would drop (invulnerable, defending,
// ragdoll: d_a_mo2.cpp:3072, 3581-3586) and otherwise kills it the K1 way.
static void em_hit_apply(enemy_mirror_state* s) {
    u8 result = WWENEMY_HR_DROPPED;
    void* a = em_actor_of(s->ha_pid);
    if (!a || AF(a, s16, AC_PROC_NAME) != PROC_MO2 || AF(a, s8, AC_INIT_STATE) != 2 ||
        AF(a, u16, AC_SETID) != s->ha_setid || AF(a, s8, AC_HOME_ROOM) != s->room) {
        result = WWENEMY_HR_NO_ACTOR;
    } else if (AF(a, s16, MO2_INVULN) != 0 || AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_DEFENCE ||
               AF(a, s16, MO2_DR_MODE) <= -100) {
        result = WWENEMY_HR_DROPPED;
    } else {
        AF(a, s8, MO2_ICE_SHRINK) = 1;
        result = WWENEMY_HR_SHRINK;
    }
    s->hit_apply_ack = s->ha_seq;
    EB_U32(WWENEMY_RESERVED2_OFF) = result;      // low byte = the result
    EB_U32(WWENEMY_HIT_APPLY_ACK_OFF) = s->ha_seq;
}

// --- Entry points -----------------------------------------------------------
void enemy_mirror_init(enemy_mirror_state* s) {
    u32* w = (u32*)s;
    u32 n;
    for (n = 0; n < sizeof(enemy_mirror_state) / 4; n++) w[n] = 0;
    // A hit_apply request left in the block from before this link is not
    // ours to apply: start acked.
    s->hit_apply_ack = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SEQ_OFF);
    g_live = s;
}

void enemy_mirror_frame(enemy_mirror_state* s, u32 frame) {
    u32 magic = EB_U32(WWENEMY_MAGIC_OFF);
    u16 layout = EB_U16(WWENEMY_LAYOUT_OFF);
    u8 flags = EB_U8(WWENEMY_FLAGS_OFF);
    u32 epoch, head, tail;
    int i;

    // 0 intake
    if (magic != WWENEMY_MAGIC || layout != WWENEMY_LAYOUT || !(flags & WWENEMY_FLAG_MIRROR)) {
        em_release_all(s, 0);
        em_write_status(s);
        return;
    }
    epoch = EB_U32(WWENEMY_EPOCH_OFF);
    if (epoch != s->epoch_seen) {
        em_release_all(s, 1);
        s->epoch_seen = epoch;
        s->last_seq = 0;
    }
    head = EB_U32(WWENEMY_SEQ_HEAD_OFF);
    tail = EB_U32(WWENEMY_SEQ_TAIL_OFF);
    if (head != 0 && head == tail && head != s->last_seq) {
        em_consume(s, head, frame);
    } else {
        s->stale_frames++;
        if (s->stale_frames > WWENEMY_STALE_FRAMES && !s->stale_released) {
            em_release_all(s, 0);
            s->stale_released = 1;
            em_sat_inc(&s->canary[3]);
        }
    }
    if (s->stale_released || s->last_seq == 0) {
        em_write_status(s);
        return;
    }

    // 1 scope: release before the stage teardown deletes the actors.
    if (!em_in_scope(s)) {
        em_release_all(s, 0);
        em_write_status(s);
        return;
    }

    // 2..8 per slot
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) em_slot_frame(s, i);

    // host role
    if (s->role == WWENEMY_ROLE_HOST && s->ha_seq != s->hit_apply_ack) em_hit_apply(s);

    em_write_status(s);
}

void enemy_mirror_delete(enemy_mirror_state* s) {
    em_release_all(s, 1);
    em_write_status(s);
    if (g_live == s) g_live = 0;
}

void enemy_mirror_unlink(void) {
    if (g_live) {
        em_release_all(g_live, 1);
        g_live = 0;
    }
}
