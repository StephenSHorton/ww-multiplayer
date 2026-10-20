// enemy_mirror.c: the Moblin mirror (host-authoritative enemies, step S1 +
// the S2 animation stream, the S3 colliders and the host hit applier: the v2
// At sphere, or v1's shrink behind a flag).
// Design: ww-multiplayer-handoff/design/host-authoritative-prototype.md and
// moblin-s2-v2-final.txt (s2.rel_side, v2); docs: inject/rel/README.md
// "enemy_mirror.c".
//
// Go writes one block per tick into the mailbox slack at MAILBOX_ADDR +
// WWENEMY_OFF (inject/include/wwenemy.h): up to four Moblins (proc 0x00BD,
// d_a_mo2) named by pid, each with a target position, angle, tilt, flags
// and (S2) the host's bck index, attribute, restart seq, morf, frame and
// rate. Every frame of the module actor (rel_main.c's execute, list 7:
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
//   4 animate  S1: mDoExt_McaMorf::play(NULL, 0, 0) so the morf chases and
//              the frame advances without the host-only footstep sounds;
//              S2 (WWENEMY_FLAG_ANIM): setAnm the streamed bck on a change
//              (sound pointer NULLed around it), mirror the rate, the spear
//              and blur shapes, play unless the host skipped it, then write
//              the streamed frame (plus the lead since the sample) as f32;
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
//              :1304-1313), and after S2 restores its own wait loop, weapon
//              byte and timers; death = release + health 0 + the light-arrow
//              shrink timer (enemy_ice, c_damagereaction.cpp:117-168), the
//              dying copy keeping its last mirrored pose and weapon byte (no
//              wait-loop or weapon restore); RELEASE with DYING kills;
//   host role  hit_apply: v2 builds a one-off At sphere (a copy of the
//              target's own head sphere image, Set from a dCcD_SrcSph, with
//              a stts Init'd from the host Link) and registers it with
//              cCcS::Set so the Moblin's own damage_check runs next frame;
//              the At record is read one execute later for the result.
//              v1 (WWENEMY_HAF_FORCE_V1) writes the shrink timer instead.
// Every game call is an absolute function-pointer cast (no relocation);
// no libc, no float library calls; the state is in the actor struct except
// the two static collision objects in the mailbox slack.
//
// Offsets are the zeldaret/tww GZLE01 decomp's: include/f_pc/f_pc_base.h
// (base_process_class), include/f_op/f_op_actor.h (fopAc_ac_c,
// fopEn_enemy_c), include/d/actor/d_a_mo2.h (mo2_class),
// include/c/c_damagereaction.h (damagereaction, enemyice),
// include/m_Do/m_Do_ext.h (mDoExt_McaMorf) with
// include/JSystem/J3DGraphAnimator/J3DAnimation.h (J3DFrameCtrl),
// include/SSystem/SComponent/c_cc_d.h and include/d/d_cc_d.h (the collider
// objects), include/d/d_resorce.h (the resource table). Function addresses:
// config/GZLE01/symbols.txt.

#include "mailbox_core.h"
#include "wwrel.h"
#include "enemy_mirror.h"
#include "remote.h"

// --- The block ------------------------------------------------------------
#define EB_ADDR      (MAILBOX_ADDR + WWENEMY_OFF)
#define EB_U32(off)  (*(volatile u32*)(EB_ADDR + (off)))
#define EB_U16(off)  (*(volatile u16*)(EB_ADDR + (off)))
#define EB_U8(off)   (*(volatile u8*)(EB_ADDR + (off)))
#define EB_S8(off)   (*(volatile s8*)(EB_ADDR + (off)))
#define EB_S16(off)  (*(volatile s16*)(EB_ADDR + (off)))
#define EB_F32(off)  (*(volatile f32*)(EB_ADDR + (off)))

// The host's static collision objects: a dCcD_Sph image (0x12C) and a
// dCcD_Stts (0x3C) in the slack past the block. Static so the pointer
// cCcS keeps from Set until dScnPly_Draw's Move never dangles, whatever
// happens to the actor or the module in between.
#define EM_SPH       ((u8*)(MAILBOX_ADDR + WWENEMY_SPH_OFF))
#define EM_STTS      ((u8*)(MAILBOX_ADDR + WWENEMY_STTS_OFF))
#define EM_SPH_SIZE  0x12C
#define EM_STTS_SIZE 0x3C

// The block must lie past the REL words (wwrel.h) and below the arena
// (0x80414000, inject/build.py's OSInit patch), and its pieces must nest.
typedef char em_check_block_start[(WWENEMY_OFF >= WWREL_FRAME_OFF + 4 && WWENEMY_OFF >= sizeof(Mailbox)) ? 1 : -1];
typedef char em_check_block_end[(MAILBOX_ADDR + WWENEMY_OFF + WWENEMY_SIZE <= 0x80414000) ? 1 : -1];
typedef char em_check_slots[(WWENEMY_SLOTS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_SLOT_SIZE <= WWENEMY_HIT_APPLY_OFF) ? 1 : -1];
typedef char em_check_hit_apply[(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SIZE <= WWENEMY_SEQ_TAIL_OFF &&
                                 WWENEMY_HA_PID_OFF + 4 <= WWENEMY_HA_SIZE &&
                                 WWENEMY_HA_POS_OFF + 12 <= WWENEMY_HA_PID_OFF &&
                                 WWENEMY_HA_FLAGS_OFF + 1 <= WWENEMY_HA_ATTYPE_OFF &&
                                 WWENEMY_HA_ATP_OFF + 1 == WWENEMY_HA_SE_OFF &&
                                 WWENEMY_HA_SE_OFF + 1 == WWENEMY_HA_HITMARK_OFF &&
                                 WWENEMY_HA_HITMARK_OFF + 1 == WWENEMY_HA_SPL_OFF &&
                                 WWENEMY_HA_SPL_OFF < WWENEMY_HA_POS_OFF) ? 1 : -1];
typedef char em_check_go_span[(WWENEMY_SEQ_TAIL_OFF + 4 == WWENEMY_GO_SPAN &&
                               WWENEMY_ACK_SEQ_OFF == WWENEMY_GO_SPAN) ? 1 : -1];
typedef char em_check_status[(WWENEMY_STATUS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_STATUS_SIZE <= WWENEMY_HITS_OFF &&
                              WWENEMY_ST_FRAMES_OFF + 2 <= WWENEMY_STATUS_SIZE &&
                              WWENEMY_ST_ANM_STATE_OFF < WWENEMY_ST_FRAMES_OFF &&
                              WWENEMY_ST_BCK_OFF == WWENEMY_ST_FRAMES_OFF + 2 &&
                              WWENEMY_ST_ANM_CHANGES_OFF + 1 == WWENEMY_STATUS_SIZE) ? 1 : -1];
typedef char em_check_hits[(WWENEMY_HITS_OFF + WWENEMY_MAX_SLOTS * WWENEMY_HIT_SIZE <= WWENEMY_HIT_APPLY_ACK_OFF) ? 1 : -1];
typedef char em_check_tail[(WWENEMY_HIT_APPLY_ACK_OFF + 4 == WWENEMY_CANARIES_OFF &&
                            WWENEMY_CANARIES_OFF + 4 == WWENEMY_CANARIES2_OFF &&
                            WWENEMY_CANARIES2_OFF + 4 == WWENEMY_HIT_RESULT_OFF &&
                            WWENEMY_HIT_RESULT_OFF + 4 == WWENEMY_RESERVED2_OFF &&
                            WWENEMY_RESERVED2_OFF + 4 == WWENEMY_SIZE) ? 1 : -1];
typedef char em_check_slot_fields[(WWENEMY_SL_RATE_OFF + 4 == WWENEMY_SL_SAMPLE_OFF &&
                                   WWENEMY_SL_SAMPLE_OFF + 4 == WWENEMY_SL_BCK_OFF &&
                                   WWENEMY_SL_BLUR_SHAPE_OFF + 1 <= WWENEMY_SL_BLUR_SCALE_OFF &&
                                   WWENEMY_SL_BLUR_SCALE_OFF + 4 == WWENEMY_SLOT_SIZE) ? 1 : -1];
// The static collision objects: past the block, 4-aligned, nested, below the arena.
typedef char em_check_objects[(WWENEMY_OFF + WWENEMY_SIZE <= WWENEMY_SPH_OFF &&
                               WWENEMY_SPH_OFF + EM_SPH_SIZE <= WWENEMY_STTS_OFF &&
                               WWENEMY_STTS_OFF + EM_STTS_SIZE <= WWENEMY_REGION_END &&
                               MAILBOX_ADDR + WWENEMY_REGION_END <= 0x80414000 &&
                               (WWENEMY_SPH_OFF & 3) == 0 && (WWENEMY_STTS_OFF & 3) == 0) ? 1 : -1];
typedef char em_check_state[(sizeof(em_slot) == 0x5C &&
                             __builtin_offsetof(em_slot, recently_dead) == 0x38 &&
                             __builtin_offsetof(em_slot, weapon_prev) == 0x3A &&
                             __builtin_offsetof(em_slot, anm_state) == 0x41 &&
                             __builtin_offsetof(em_slot, tgt_blur_scale) == 0x48 &&
                             __builtin_offsetof(em_slot, anm_sample_seq) == 0x4C &&
                             __builtin_offsetof(em_slot, anm_rate) == 0x58 &&
                             __builtin_offsetof(enemy_mirror_state, stage) == 0x20 &&
                             __builtin_offsetof(enemy_mirror_state, canary2) == 0x2C &&
                             __builtin_offsetof(enemy_mirror_state, ha_state) == 0x30 &&
                             __builtin_offsetof(enemy_mirror_state, ha_attype) == 0x3C &&
                             __builtin_offsetof(enemy_mirror_state, ha_pos) == 0x40 &&
                             __builtin_offsetof(enemy_mirror_state, ha_actor) == 0x4C &&
                             __builtin_offsetof(enemy_mirror_state, stts_link) == 0x50 &&
                             __builtin_offsetof(enemy_mirror_state, slots) == 0x54 &&
                             sizeof(enemy_mirror_state) == 0x1C4) ? 1 : -1];

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
// play.mPlayerInfo[0].mpPlayer = dComIfGp_getPlayer(0), the word
// at_power_check reads for the cut state (d_cc_uty.cpp:87-88): play +
// 0x48A4 (d_com_inf_game.h:787-791).
#define EM_PLAYER_INFO0   (*(volatile u32*)0x803CA74C)
// play.mpPlayerPtr[0] = dComIfGp_getLinkPlayer(): play + 0x48AC
// (d_com_inf_game.h:791; wwapi.h PLAYER_PTR_ARRAY, addr.PlayerPtr).
// daPy_py_c::changePlayer writes only the first (d_a_player.cpp:13-24).
#define EM_PLAYER_PTR0    (*(volatile u32*)0x803CA754)
// The DOL's vtables for the four vptrs of a dCcD_Sph image (symbols.txt):
// __vt__8dCcD_Sph (:15249) at +0x3C; its cCcD_ShapeAttr part (+0x28 into
// it) at +0x114 and its cM3dGSph part (+0x7C) at +0x128; __vt__8cM3dGAab
// (:14741) at +0x110 (the shape's AABB, before the ShapeAttr vptr; MWCC
// puts a class's vptr after its members). dCcD_Sph's ctor is inline, so a
// sphere built in an actor REL (d_a_mo2.rel) points at that REL's own copy
// of these tables (live: 0x8132F794, +0x28 and +0x7C at +0x114/+0x128,
// the AABB table 0x8132F8BC): the same layout, its dtor slots REL copies
// of the same code. The static image must never keep those: the REL can
// be unloaded at a stage change while the image lives on.
#define EM_VT_DCCD_SPH    0x803899D0
#define EM_VT_SPH_SHAPE   0x803899F8    // EM_VT_DCCD_SPH + 0x28
#define EM_VT_SPH_GSPH    0x80389A4C    // EM_VT_DCCD_SPH + 0x7C
#define EM_VT_GAAB        0x80375CAC
#define EM_VT_SLOT_A      0x10          // two slots that hold DOL functions in
#define EM_VT_SLOT_B      0x1C          // both copies (0x800AB7C0, 0x800AB7C4)
#define CC_AAB_VPTR       0x110
#define CC_SHAPE_VPTR     0x114
#define CC_GSPH_VPTR      0x128
#define EM_MEM1_LO        0x80000000u
#define EM_MEM1_HI        0x83000000u   // 48 MB MEM1 (docs/05 "48 MB MEM1")

// Member functions take `this` in r3 (C++ EABI); float arguments go in
// f1.. and s16/u8 in r4.. as the prototypes say, so plain C function
// pointers with the same parameter list call them correctly.
typedef BOOL (*em_morf_play_t)(void* morf, void* pos, u32 a, s8 b);  // play__14mDoExt_McaMorfFP3VecUlSc
#define McaMorf_play      ((em_morf_play_t)0x8001300C)
typedef void (*em_morf_calc_t)(void* morf);                          // calc__14mDoExt_McaMorfFv
#define McaMorf_calc      ((em_morf_calc_t)0x800132E8)
// setAnm__14mDoExt_McaMorfFP15J3DAnmTransformiffffPv: r3 this, r4 bck, r5
// loopMode, f1 morf, f2 speed, f3 start, f4 end, r6 bas (m_Do_ext.cpp:1414).
typedef void (*em_morf_setanm_t)(void* self, void* bck, int loopMode, f32 morf, f32 speed, f32 start, f32 end, void* bas);
#define McaMorf_setAnm    ((em_morf_setanm_t)0x80012D78)
typedef void (*em_dr_matrix_set_t)(void* dr);                        // dr_matrix_set__FP14damagereaction
#define dr_matrix_set     ((em_dr_matrix_set_t)0x80022460)
typedef void (*em_mtx_copy_t)(const void* src, void* dst);           // PSMTXCopy
#define PSMTXCopy         ((em_mtx_copy_t)0x8030D0C8)
typedef void (*em_set_c_t)(void* shape, const cXyz* c);              // SetC__8cM3dGCylFRC4cXyz / SetC__8cM3dGSphFRC4cXyz
#define cM3dGCyl_SetC     ((em_set_c_t)0x80251D88)
#define cM3dGSph_SetC     ((em_set_c_t)0x8025238C)
typedef void (*em_ccs_set_t)(void* ccs, void* obj);                  // Set__4cCcSFP8cCcD_Obj
#define cCcS_Set          ((em_ccs_set_t)0x80242D04)
typedef void (*em_clr_hit_t)(void* obj);                             // ClrTgHit__12dCcD_GObjInfFv / ClrAtHit__12dCcD_GObjInfFv
#define dCcD_GObjInf_ClrTgHit ((em_clr_hit_t)0x800AB958)
#define dCcD_GObjInf_ClrAtHit ((em_clr_hit_t)0x800AB7C4)
typedef void (*em_sph_set_t)(void* sph, const void* src);            // Set__8dCcD_SphFRC11dCcD_SrcSph
#define dCcD_Sph_Set      ((em_sph_set_t)0x800ABFC0)
typedef void (*em_sph_start_cat_t)(void* sph, cXyz* pos);            // StartCAt__8dCcD_SphFR4cXyz
#define dCcD_Sph_StartCAt ((em_sph_start_cat_t)0x800AC02C)
typedef void (*em_stts_ct_t)(void* stts);                            // __ct__9dCcD_SttsFv
#define dCcD_Stts_ct      ((em_stts_ct_t)0x80100EB8)
typedef void (*em_stts_init_t)(void* stts, int weight, int p1, void* actor); // Init__9dCcD_SttsFiiP10fopAc_ac_c
#define dCcD_Stts_Init    ((em_stts_init_t)0x800AB3DC)
typedef f32 (*em_sincos_t)(s16 a);                                   // cM_ssin__Fs / cM_scos__Fs
#define cM_ssin           ((em_sincos_t)0x800F0054)
#define cM_scos           ((em_sincos_t)0x800F0038)
// dRes_getRes_byIdx (wwapi.h: getRes__14dRes_control_cFPCclP11dRes_info_ci
// 0x8006F208) with MOBJECT_INFO and OBJECT_INFO_COUNT: a 64-entry strcmp
// scan, no allocation (d_resorce.cpp:602-654).

// The archive name for getRes, in .rodata (an ADDR32 self-relocation;
// .sdata is refused by build.py, so no small data).
static const char EM_MO2_ARC[4] = "Mo2";

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
#define MO2_BLUR_SCALE    0x590   // f32 m0590: blur mesh scale (br_draw, d_a_mo2.cpp:601-638)
#define MO2_BLUR_ON       0x594   // int m0594: blur mesh drawn
#define MO2_BLUR_SHAPE    0x598   // int m0598: blur mesh shape
#define MO2_ANM_GATE      0x5B0   // s16 m05B0: anm_init is a no-op while set (d_a_mo2.cpp:138)
#define MO2_PLAY_SKIP     0x5B2   // s16 m05B2: play() skipped while set (d_a_mo2.cpp:3488-3490)
#define MO2_INVULN        0x5B4   // s16 m05B4: invulnerable frames
#define MO2_DR            0x614   // damagereaction mDamageReaction
#define MO2_DR_MODE       0x618   // s16 mDamageReaction.mMode
#define MO2_DR_ACTION     0x61A   // s16 mDamageReaction.mAction
#define MO2_TILT          0xAA0   // s16 mDamageReaction.m48C.x (+0x614 + 0x48C): the ground tilt dr_matrix_set rotates by
#define MO2_WEAPON        0xDD4   // s8  mbHasInnateWeapon: the spear shape is drawn iff == 1 (d_a_mo2.cpp:3882-3890)
#define MO2_CO_CYL        0x208C  // dCcD_Cyl mCoCyl
#define MO2_TG_CYL        0x21BC  // dCcD_Cyl mTgCyl
#define MO2_HEAD_SPH      0x22EC  // dCcD_Sph mHeadSph
#define MO2_DEF_SPH       0x2418  // dCcD_Sph mDefenseSph
#define MO2_HEAD_CENTER   0x28C8  // cXyz m28C8: the head joint position (joint callback, d_a_mo2.cpp:367)
#define MO2_DEF_CENTER    0x2928  // cXyz m2928: the shield centre (d_a_mo2.cpp:2220,2248,3035)
#define MO2_BARE          0x2943  // u8  m2943: bare-handed (wait_set/walk_set rows, d_a_mo2.cpp:1059-1100)
#define MO2_ICE_SHRINK    0x2A5A  // s8 mEnemyIce.mLightShrinkTimer (+0x2A54 + 6, c_damagereaction.h:16)
// mDoExt_McaMorf (m_Do_ext.h:368-380) and J3DFrameCtrl (J3DAnimation.h:868-876)
#define MORF_MODEL        0x50    // J3DModel* mpModel
#define MORF_ANM          0x54    // J3DAnmTransform* mpAnm
#define MORF_ATTR         0x5C    // u8  mFrameCtrl.mAttribute (0 NONE, 1 RESET, 2 LOOP, 3 REVERSE, 4 LOOP_REVERSE)
#define MORF_START        0x5E    // s16 mFrameCtrl.mStart
#define MORF_END          0x60    // s16 mFrameCtrl.mEnd
#define MORF_LOOP         0x62    // s16 mFrameCtrl.mLoop
#define MORF_RATE         0x64    // f32 mFrameCtrl.mRate
#define MORF_FRAME        0x68    // f32 mFrameCtrl.mFrame
#define MORF_SOUND        0x80    // mDoExt_zelAnime* mpSound (m_Do_ext.h:377)
#define MORF_ATTR_LOOP    2
#define MORF_ATTR_MAX     4
// collider objects: dCcD_Cyl/Sph = dCcD_GObjInf (0xF8) + cCcD_Cyl/SphAttr
// (cCcD_ShapeAttr 0x20 + cM3dGCyl/Sph, centre at +0); the cCcD_ObjHitInf
// base is at +0 with At at +0 (mSPrm +0x00, mRPrm +0x04, mHitObj +0x08)
// and Tg at +0x18 (mRPrm +0x1C, mHitObj +0x20).
#define CC_AT_SPRM        0x00    // u32 cCcD_ObjAt.mSPrm: bit 0 = cCcD_AtSPrm_Set_e
#define CC_AT_RPRM        0x04    // u32 cCcD_ObjAt.mRPrm: bit 0 = hit this Move (SetAtHit)
#define CC_AT_HIT_OBJ     0x08    // cCcD_Obj* cCcD_ObjAt.mHitObj
#define CC_TG_RPRM        0x1C    // u32: bit 0 = cCcD_TgRPrm_Hit_e
#define CC_VPTR           0x3C    // the primary vptr (__vt__8dCcD_Sph for a sphere)
#define CC_STTS           0x44    // cCcD_Stts* cCcD_Obj.mStts (SetStts is an inline store, c_cc_d.h:608)
#define CC_GAT_CALLBACK   0x58    // dCcD_GObjAt mHitCallback (dCcD_GAtTgCoCommonBase +0x08 at +0x50)
#define CC_SHAPE_CENTER   0x118   // cM3dGCyl/cM3dGSph (the SetC `this`)
#define CC_SHAPE_RADIUS   0x124   // f32 cM3dGSph.mRadius (+0x0C past the centre, c_m3d_g_sph.h:16-19)
// dCcD_SrcSph (0x40, d_cc_d.h:158-161; c_cc_d.h:286-314): the fill em_hit_submit writes
#define SRC_AT_TYPE       0x04    // u32
#define SRC_AT_ATP        0x08    // u8
#define SRC_AT_SPRM       0x0C    // u32
#define SRC_GAT_SE        0x1C    // u8
#define SRC_GAT_HITMARK   0x1D    // u8
#define SRC_GAT_SPL       0x1E    // u8
#define SRC_GAT_SPRM      0x20    // u32
#define SRC_CENTER        0x30    // f32 x, y, z
#define SRC_RADIUS        0x3C    // f32
#define SRC_WORDS         16      // 0x40 bytes
#define AT_TYPE_SWORD     0x2     // d_cc_d.h:14
#define CC_AT_SPRM_SET_VS_ENEMY 0x03 // cCcD_AtSPrm_Set_e | cCcD_AtSPrm_VsEnemy_e (c_cc_d.h:20-27)
#define DCCG_SE_SWORD     1       // dCcG_SE_UNK1 -> JA_SE_LK_SW_HIT_S (d_cc_uty.cpp:41-55)
#define DCCG_HITMARK_NRM  0xD     // dCcG_AtHitMark_Nrm_e (d_cc_d.h:91-96)
#define DCCG_AT_SPRM_NO_HITMARK 0x2 // dCcG_AtSPrm_NoHitMark_e
#define EM_AT_RADIUS      30.0f   // inside the head sphere r55 / Tg cylinder r45 h137.5
#define EM_TG_CENTER_UP   60.0f   // collider 2: current.pos + (0, 60, 0), inside the Tg cylinder
#define EM_STTS_WEIGHT    120     // Link's own mStts.Init(120, 0xFF, this) (d_a_player_main.cpp:12184)
#define EM_STTS_P1        0xFF

#define PROC_MO2          0x00BD
#define PROC_PLAYER       0x00A9  // fpcNm_PLAYER_e (f_pc_name.h:178)
#define MO2_ACTION_DEFENCE 10     // d_a_mo2.cpp:31
#define MO2_ACTION_FAIL   20      // ACTION_FAIL: death
#define MO2_MODE_HANDBACK (-10)   // JYUNKAI mode -10: 60-frame wait (d_a_mo2.cpp:1304-1313)
#define MO2_MODE_RAGDOLL  (-100)  // mDamageReaction.mMode <= -100 while knocked back (c_damagereaction.cpp:1057)
#define MO2_BCK_FIRST     0x3E    // Mo2.arc BCK indices, contiguous (Mo2.h:64-119)
#define MO2_BCK_LAST      0x75
#define MO2_BCK_WAIT      0x73    // wait_set's rows (d_a_mo2.cpp:1059-1072)
#define MO2_BCK_KWAIT     0x5C
#define MO2_BCK_NBWAIT    0x62
#define EM_KEYED_SETID_MAX 0x200  // keyed room actors (docs/08 §7.2)
#define EM_SNAP_DIST2     40000.0f // 200 units squared
#define EM_LERP           0.25f
#define EM_RANGE          1000000.0f
#define EM_RATE_MAX       8.0f    // |streamed rate| beyond this keeps the previous pair
#define EM_CO_FORWARD     35.0f   // mCoCyl centre: pos + 35 units along angle.y (d_a_mo2.cpp:3571-3577)
#define EM_BT_NO_OPENING  100.0f
#define EM_DEAD_LATCH     300
#define EM_HIT_COOLDOWN   8
#define EM_ANM_RETRY      60      // frames before a failed bck index is asked of getRes again
#define EM_FRAME_LEAD_CAP 3       // REL frames of rate added past the consumed sample
#define EM_HANDBACK_MORF  5.0f    // the blend into the restored wait loop
#define EM_INVULN_WAIT    8       // executes to wait for the Moblin's 5-frame invuln
#define EM_MAX_SETS       2       // cCcS::Set calls per request (N, and N+1 if nothing crossed)

#define AF(a, T, off)     (*(T*)((u8*)(a) + (off)))

// The live state, for _epilog: set at create, cleared at delete. A
// pointer only, valid for the module's own lifetime; forced into .data
// so the ELF carries no .bss.
static enemy_mirror_state* g_live __attribute__((section(".data"))) = (enemy_mirror_state*)0;

static void em_sat_inc(u8* c) {
    if (*c != 0xFF) (*c)++;
}

static int em_in_mem1(u32 p) {
    return p >= EM_MEM1_LO && p < EM_MEM1_HI && (p & 3) == 0;
}

static void* em_actor_of(u32 pid) {
    fopAc_ac_c* a = 0;
    if (pid == 0 || !fopAcM_SearchByID(pid, &a) || !a) return 0;
    return a;
}

// --- Animation helpers (S2) ------------------------------------------------
// getRes for a Mo2 bck index: NULL unless the pointer is sane. Never
// cached: a reloaded archive can never leave a stale pointer (I1).
static void* em_bck_res(u8 idx) {
    void* res = dRes_getRes_byIdx(EM_MO2_ARC, (s32)idx, MOBJECT_INFO, OBJECT_INFO_COUNT);
    if (!em_in_mem1((u32)res)) return 0;
    return res;
}

// setAnm with mpSound (+0x80) NULLed around it (I3): setAnm's whole
// `if (mpSound)` block (m_Do_ext.cpp:1438-1448: the bas assignment,
// initActorAnimSound or stop) is skipped, so no sound object changes on
// the copy; play(NULL, ..) then never calls setAnimSound (:1468-1470).
static void em_set_anm(void* morf, void* res, int attr, f32 morf_frames, f32 rate) {
    u32 snd = AF(morf, u32, MORF_SOUND);
    AF(morf, u32, MORF_SOUND) = 0;
    McaMorf_setAnm(morf, res, attr, morf_frames, rate, 0.0f, -1.0f, 0);
    AF(morf, u32, MORF_SOUND) = snd;
}

// --- Release ----------------------------------------------------------------
// Hand the Moblin back to its own code: clear our pause bit (the other
// bits stay as they are now: fpcPause_Disable's leaf case), no momentum,
// old = current so fopAc_Execute's velocity reads zero, and JYUNKAI mode
// -10 so daMo2_Execute waits 60 frames before patrolling. After S2 the
// copy also gets its own wait loop back (wait_set's rows keyed on its
// weapon and variant, d_a_mo2.cpp:1059-1072), its weapon byte, no blur,
// and its gate/skip timers cleared (they never decremented while paused,
// so anm_init would be a no-op for up to 25 frames after release).
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
    if (sl->anm_state != WWENEMY_ANM_S1) {
        void* morf = AF(a, void*, MO2_MORF);
        u8 idx = sl->weapon_prev ? (AF(a, u8, MO2_MODE) == 1 ? MO2_BCK_KWAIT : MO2_BCK_WAIT) : MO2_BCK_NBWAIT;
        void* res = morf ? em_bck_res(idx) : 0;
        if (res) em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
        AF(a, s8, MO2_WEAPON) = (s8)sl->weapon_prev;
        AF(a, u32, MO2_BLUR_ON) = 0;
        AF(a, s16, MO2_ANM_GATE) = 0;
        AF(a, s16, MO2_PLAY_SKIP) = 0;
        sl->anm_state = WWENEMY_ANM_S1;
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
    sl->anm_state = WWENEMY_ANM_S1;
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
        EB_U8(off + WWENEMY_ST_ANM_STATE_OFF) = sl->anm_state;
        EB_U16(off + WWENEMY_ST_FRAMES_OFF) = sl->frames;
        EB_U8(off + WWENEMY_ST_BCK_OFF) = sl->anm_bck;
        EB_U8(off + WWENEMY_ST_ANM_CHANGES_OFF) = sl->anm_changes;
        if (sl->adopted) mask |= 1u << i;
    }
    EB_U32(WWENEMY_ADOPTED_OFF) = mask;
    EB_U32(WWENEMY_EPOCH_SEEN_OFF) = s->epoch_seen;
    for (i = 0; i < 4; i++) EB_U8(WWENEMY_CANARIES_OFF + i) = s->canary[i];
    for (i = 0; i < 4; i++) EB_U8(WWENEMY_CANARIES2_OFF + i) = s->canary2[i];
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
        // S2: the streamed animation
        sl->tgt_bck = EB_U8(off + WWENEMY_SL_BCK_OFF);
        sl->tgt_attr = EB_U8(off + WWENEMY_SL_ATTR_OFF);
        sl->tgt_anm_seq = EB_U8(off + WWENEMY_SL_ANM_SEQ_OFF);
        sl->tgt_morf = EB_U8(off + WWENEMY_SL_MORF_OFF);
        sl->tgt_flags2 = EB_U8(off + WWENEMY_SL_FLAGS2_OFF);
        sl->tgt_blur_shape = EB_U8(off + WWENEMY_SL_BLUR_SHAPE_OFF);
        {
            f32 bs = EB_F32(off + WWENEMY_SL_BLUR_SCALE_OFF);
            sl->tgt_blur_scale = em_f32_bad(bs) ? 1.0f : bs;
        }
        {
            u32 sample = EB_U32(off + WWENEMY_SL_SAMPLE_OFF);
            if (sample != sl->anm_sample_seq) {
                f32 fr = EB_F32(off + WWENEMY_SL_FRAME_OFF);
                f32 rt = EB_F32(off + WWENEMY_SL_RATE_OFF);
                sl->anm_sample_seq = sample;
                sl->anm_sample_frame = frame;
                if (!em_f32_bad(fr) && !em_f32_bad(rt) && rt <= EM_RATE_MAX && rt >= -EM_RATE_MAX) {
                    sl->anm_frame = fr;
                    sl->anm_rate = rt;
                }                                      // else keep the previous pair
            }
        }
    }
    // The request is taken only while none is open: em_hit_tick,
    // em_hit_submit and em_hit_center read these fields until the open one
    // publishes, so a newer request Go writes meanwhile (after its ack
    // timeout) must not change its target, centre or At parameters. The
    // newer seq is read at the first consume (or keepalive) after that.
    if (s->role == WWENEMY_ROLE_HOST && s->ha_state == EM_HA_IDLE) {
        s->ha_seq = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SEQ_OFF);
        s->ha_pid = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_PID_OFF);
        s->ha_setid = EB_U16(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SETID_OFF);
        s->ha_collider = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_COLLIDER_OFF);
        s->ha_flags = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_FLAGS_OFF);
        s->ha_attype = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_ATTYPE_OFF);
        s->ha_atp = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_ATP_OFF);
        s->ha_se = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SE_OFF);
        s->ha_hitmark = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_HITMARK_OFF);
        s->ha_spl = EB_U8(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SPL_OFF);
        {
            f32 x = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF);
            f32 y = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF + 4);
            f32 z = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF + 8);
            if (!em_f32_bad(x) && !em_f32_bad(y) && !em_f32_bad(z)) {
                s->ha_pos.x = x;
                s->ha_pos.y = y;
                s->ha_pos.z = z;
            }
        }
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
    // S2 starts over on the copy's own animation.
    sl->anm_bck = WWENEMY_BCK_NONE;
    sl->anm_attr = 0xFF;
    sl->anm_state = WWENEMY_ANM_S1;
    sl->anm_seq_seen = 0;
    sl->anm_retry = 0;
    sl->anm_changes = 0;
    sl->anm_failed_bck = WWENEMY_BCK_NONE;
    sl->weapon_prev = (u8)AF(a, s8, MO2_WEAPON);
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
// adopting the shrinking copy. A dying copy never resumes its AI, so it
// keeps its last mirrored pose and weapon byte while it shrinks (enemy_ice
// plays the current morf at speed 3, d_a_mo2.cpp:3379-3387): the S2 part
// of em_handback (the wait-loop setAnm and the weapon_prev restore) is
// skipped, only its blur/gate/skip clears are kept.
static void em_die(em_slot* sl, void* a) {
    if (sl->anm_state != WWENEMY_ANM_S1) {
        AF(a, u32, MO2_BLUR_ON) = 0;
        AF(a, s16, MO2_ANM_GATE) = 0;
        AF(a, s16, MO2_PLAY_SKIP) = 0;
        sl->anm_state = WWENEMY_ANM_S1;
    }
    em_release(sl);
    AF(a, s8, AC_HEALTH) = 0;
    AF(a, s8, MO2_ICE_SHRINK) = 1;
    AF(a, u32, AC_ATTN_FLAGS) = 0;
    sl->state = WWENEMY_STATE_DYING;
    sl->recently_dead = EM_DEAD_LATCH;
}

// --- Animation (S2) ---------------------------------------------------------
// What the paused execute would have done to the morf, from the stream:
//   S2.1 select  setAnm the streamed bck (from getRes, fresh every change)
//                on an index / attribute / anm_seq change, bracketed by
//                mpSound = NULL; a failed index is retried after 60 frames;
//   S2.2 rate    the streamed rate written when it differs (a parry's
//                setPlaySpeed(-1), the stop at a once-anim's end, ...);
//   S2.3 shapes  the spear byte, the bare-handed byte, the blur mesh words;
//   S2.4 play    play(NULL, 0, 0) unless the host skipped play (m05B2);
//   S2.5 frame   the sample's frame plus its rate per REL frame since it
//                was consumed (capped), wrapped once for a loop or clamped
//                to [start, end) otherwise, stored as f32 after play().
// Anything but SYNCED degrades to S1 for the slot (I4): play, no frame
// write. A frame is written only when the applied bck is the streamed one
// (I2).
static void em_animate(enemy_mirror_state* s, em_slot* sl, void* a, void* morf, u32 frame) {
    u8 t = sl->tgt_bck;
    u8 at = sl->tgt_attr;
    int new_sample = (sl->anm_sample_frame == frame);

    // S2.1 select
    if (t == WWENEMY_BCK_NONE || at == 0xFF) {
        sl->anm_state = WWENEMY_ANM_UNRESOLVED;
        if (new_sample) em_sat_inc(&s->canary[1]);
    } else if (t < MO2_BCK_FIRST || t > MO2_BCK_LAST || at > MORF_ATTR_MAX) {
        sl->anm_state = WWENEMY_ANM_RANGE;
        if (new_sample) em_sat_inc(&s->canary2[1]);
    } else if (sl->anm_state != WWENEMY_ANM_SYNCED || t != sl->anm_bck || at != sl->anm_attr ||
               sl->tgt_anm_seq != sl->anm_seq_seen) {
        if (sl->anm_retry && t == sl->anm_failed_bck) {
            sl->anm_retry--;
        } else {
            void* res = em_bck_res(t);
            if (!res) {
                sl->anm_state = WWENEMY_ANM_RES_NULL;
                em_sat_inc(&s->canary2[0]);
                sl->anm_retry = EM_ANM_RETRY;
                sl->anm_failed_bck = t;
            } else {
                // setAnm: mpAnm, init(frameMax) resets attribute/state/
                // start/loop/rate/frame, setPlayMode(at), setPlaySpeed(rate),
                // frame 0 (or the end for a negative rate), setMorf(morf)
                // (m_Do_ext.cpp:1414-1449, J3DAnimation.cpp:13-21).
                em_set_anm(morf, res, (int)at, (f32)sl->tgt_morf, sl->anm_rate);
                sl->anm_bck = t;
                sl->anm_attr = at;
                sl->anm_seq_seen = sl->tgt_anm_seq;
                sl->anm_state = WWENEMY_ANM_SYNCED;
                em_sat_inc(&sl->anm_changes);
            }
        }
    }

    // S2.2 rate
    if (sl->anm_state == WWENEMY_ANM_SYNCED && sl->anm_rate != AF(morf, f32, MORF_RATE)) {
        AF(morf, f32, MORF_RATE) = sl->anm_rate;
    }

    // S2.3 shapes
    AF(a, s8, MO2_WEAPON) = (sl->tgt_flags2 & WWENEMY_SF2_HAS_WEAPON) ? 1 : 0;
    AF(a, u8, MO2_BARE) = (sl->tgt_flags & WWENEMY_SF_BARE_HANDED) ? 1 : 0;
    AF(a, u32, MO2_BLUR_ON) = (sl->tgt_flags2 & WWENEMY_SF2_BLUR_ON) ? 1 : 0;
    AF(a, u32, MO2_BLUR_SHAPE) = sl->tgt_blur_shape;
    AF(a, f32, MO2_BLUR_SCALE) = sl->tgt_blur_scale;

    // S2.4 play (pos NULL: no setAnimSound, m_Do_ext.cpp:1468-1470; the
    // morf chase happens here, :1464-1466)
    if (!(sl->tgt_flags2 & WWENEMY_SF2_PLAY_SKIP)) McaMorf_play(morf, 0, 0, 0);

    // S2.5 frame
    if (sl->anm_state == WWENEMY_ANM_SYNCED && sl->anm_bck == sl->tgt_bck) {
        u32 n = frame - sl->anm_sample_frame;
        f32 target = sl->anm_frame;
        f32 start = (f32)AF(morf, s16, MORF_START);
        f32 end = (f32)AF(morf, s16, MORF_END);
        f32 loop = (f32)AF(morf, s16, MORF_LOOP);
        f32 last = end - 0.001f;                    // EMode_NONE's stop value (J3DAnimation.cpp:154)
        if (n > EM_FRAME_LEAD_CAP) n = EM_FRAME_LEAD_CAP;
        // The host skipped play() (m05B2, d_a_mo2.cpp:3488-3490), so its
        // frame is held while the rate stays as it was: no lead.
        if (sl->tgt_flags2 & WWENEMY_SF2_PLAY_SKIP) n = 0;
        while (n-- > 0) target += sl->anm_rate;
        if (sl->anm_attr == MORF_ATTR_LOOP) {
            // One wrap with update()'s own guards (J3DAnimation.cpp:166-182),
            // then the clamp: a corrupt frame never spins here.
            if (end - loop > 0.0f && target >= end) target -= end - loop;
            if (loop - start > 0.0f && target < start) target += loop - start;
        }
        if (target < start) target = start;
        if (target > last) target = last;
        if (target == target) AF(morf, f32, MORF_FRAME) = target;   // a direct f32 store: McaMorf::setFrame truncates to s16
    }
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
static void em_slot_frame(enemy_mirror_state* s, int i, u32 frame) {
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
    // A plain release hands the copy back. RELEASE with DYING (Go's
    // late-joiner catch-up slot: a setID the host killed that is still
    // alive here) is a kill: resolve, validate and adopt as usual, then
    // em_die below.
    if ((sl->tgt_flags & (WWENEMY_SF_RELEASE | WWENEMY_SF_DYING)) == WWENEMY_SF_RELEASE) {
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
    // A good validate clears a sticky error (a bad position this frame,
    // ERR_RANGE, is em_consume's to report and clear).
    if (sl->last_err != WWENEMY_ERR_RANGE) sl->last_err = WWENEMY_ERR_NONE;
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

    // 4 animation: S2 behind the block flag, else S1 (the morf chases and
    // the frame advances, no sounds: play skips the bas sound when pos is
    // NULL, m_Do_ext.cpp:1468-1470) with the blur mesh off.
    morf = AF(a, void*, MO2_MORF);
    if (s->flags & WWENEMY_FLAG_ANIM) {
        em_animate(s, sl, a, morf, frame);
    } else {
        AF(a, u32, MO2_BLUR_ON) = 0;
        McaMorf_play(morf, 0, 0, 0);
    }
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

// --- Host role: the hit applier ----------------------------------------------
// Go hands over a client's HIT as (seq, the host's pid of that setID, the
// setID, the collider, the At parameters, flags). v2: the REL builds a
// one-off At sphere at the target's own collider centre, registers it for
// one Move (a second if nothing crossed) and reads its At record one
// execute later: the Moblin's own damage_check then applies the damage,
// reaction, sounds and hit-stop as for a sword hit by the host Link. v1
// (WWENEMY_HAF_FORCE_V1) refuses what the Moblin's own damage_check would
// drop (invulnerable, defending, ragdoll: d_a_mo2.cpp:3072, 3581-3586) and
// otherwise kills it the K1 way.

// The result word and the ack, the last two stores of a request. A request
// that asked for remote-Link attribution (WWREMOTE_HAF_ATTRIB_BODY) while
// the body was not LIVE ran the host-Link path: its otherwise empty detail
// says so (WWREMOTE_HD_NO_BODY, design section 8.6).
static void em_hit_publish(enemy_mirror_state* s, u8 result, u8 detail) {
    if (detail == WWENEMY_HD_NONE && s->ha_attrib == 2) detail = WWREMOTE_HD_NO_BODY;
    s->ha_attrib = 0;
    s->ha_detail = detail;
    EB_U32(WWENEMY_HIT_RESULT_OFF) = ((u32)result << 24) | ((u32)detail << 16) |
                                     ((u32)s->ha_frames << 8) | (u32)s->ha_sets;
    EB_U32(WWENEMY_HIT_APPLY_ACK_OFF) = s->hit_apply_ack;
    s->ha_state = EM_HA_IDLE;
    s->ha_actor = 0;
}

// The request's target: the pid's actor, a Moblin with the request's setID
// in the block's room (and, for an open request, the same actor as before).
static void* em_hit_actor(enemy_mirror_state* s) {
    void* a = em_actor_of(s->ha_pid);
    if (!a || AF(a, s16, AC_PROC_NAME) != PROC_MO2 || AF(a, s8, AC_INIT_STATE) != 2 ||
        AF(a, u16, AC_SETID) != s->ha_setid || AF(a, s8, AC_HOME_ROOM) != s->room) return 0;
    if (s->ha_state != EM_HA_IDLE && (u32)a != s->ha_actor) return 0;
    return a;
}

// The sphere's centre from the host's own live Moblin, never the client's
// lagged position: 1 = the head joint the host's head sphere is set to
// (d_a_mo2.cpp:3951-3955), 2 = inside the Tg cylinder at current.pos
// (:3980-3983), else the client's collider centre.
static void em_hit_center(enemy_mirror_state* s, void* a, cXyz* c) {
    if (s->ha_collider == 1) {
        c->x = AF(a, f32, MO2_HEAD_CENTER + 0);
        c->y = AF(a, f32, MO2_HEAD_CENTER + 4);
        c->z = AF(a, f32, MO2_HEAD_CENTER + 8);
    } else if (s->ha_collider == 2) {
        c->x = AF(a, f32, AC_CUR_POS + 0);
        c->y = AF(a, f32, AC_CUR_POS + 4) + EM_TG_CENTER_UP;
        c->z = AF(a, f32, AC_CUR_POS + 8);
    } else {
        c->x = s->ha_pos.x;
        c->y = s->ha_pos.y;
        c->z = s->ha_pos.z;
    }
}

// Register the sphere for this frame's Move: a clean At record, the
// centre (StartCAt zeroes the At vector: the hit-mark angle then falls back
// to tg.pos - Link.pos, d_cc_s.cpp:327-355), then cCcS::Set (registers in
// mpObjAt by the SPrm Set bit, c_cc_s.cpp:42-59).
static void em_hit_register(enemy_mirror_state* s, void* a) {
    cXyz c;
    em_hit_center(s, a, &c);
    dCcD_GObjInf_ClrAtHit(EM_SPH);
    dCcD_Sph_StartCAt(EM_SPH, &c);
    cCcS_Set(EM_CCS, EM_SPH);
    s->ha_sets++;
}

// The static sphere image (built lazily, after enemy_mirror_init's
// zeroing). A live image beats hand construction: no __ct__8dCcD_SphFv
// exists in the DOL. mHeadSph carries no At hit callback (mWeaponSph does:
// never that one); the callback word and the At SPrm are zeroed so the
// image is not an At until Set says so. Everything else stale in the copy
// is reset by ClrAtHit, CalcArea, StartCAt or Set. Shared with remote.c's
// client apply (the sphere is idle on a client).
int enemy_mirror_sph_image(enemy_mirror_state* s, void* a) {
    return enemy_mirror_sph_image_at(s, (const u8*)a + MO2_HEAD_SPH);
}

// The same from any live dCcD_Sph (remote.c's client apply falls back to
// Link's mFanWindSph when no mirrored Moblin exists): only the first call
// per relink copies, and the vtable fingerprint below must hold.
int enemy_mirror_sph_image_at(enemy_mirror_state* s, const void* sph) {
    u32* dst = (u32*)EM_SPH;
    const u32* img = (const u32*)sph;
    u32 vt;
    int i;
    if (s->sph_built) return 1;
    for (i = 0; i < EM_SPH_SIZE / 4; i++) dst[i] = img[i];
    // Fingerprint the image's dCcD_Sph table (the DOL's own, or a REL's
    // copy of it), then point every vptr at the DOL's tables.
    vt = AF(EM_SPH, u32, CC_VPTR);
    if (!em_in_mem1(vt) || AF(EM_SPH, u32, CC_SHAPE_VPTR) != vt + 0x28 ||
        AF(EM_SPH, u32, CC_GSPH_VPTR) != vt + 0x7C || !em_in_mem1(AF(EM_SPH, u32, CC_AAB_VPTR)) ||
        AF(vt, u32, EM_VT_SLOT_A) != AF(EM_VT_DCCD_SPH, u32, EM_VT_SLOT_A) ||
        AF(vt, u32, EM_VT_SLOT_B) != AF(EM_VT_DCCD_SPH, u32, EM_VT_SLOT_B)) return 0;
    AF(EM_SPH, u32, CC_VPTR) = EM_VT_DCCD_SPH;
    AF(EM_SPH, u32, CC_SHAPE_VPTR) = EM_VT_SPH_SHAPE;
    AF(EM_SPH, u32, CC_GSPH_VPTR) = EM_VT_SPH_GSPH;
    AF(EM_SPH, u32, CC_AAB_VPTR) = EM_VT_GAAB;
    AF(EM_SPH, u32, CC_GAT_CALLBACK) = 0;
    AF(EM_SPH, u32, CC_AT_SPRM) = 0;
    s->sph_built = 1;
    return 1;
}

// The first Set of a request: the host Link, the static stts and sphere
// image (built lazily, after enemy_mirror_init's zeroing), the dCcD_SrcSph
// fill and dCcD_Sph::Set, then the registration. Publishes on a failure.
static void em_hit_submit(enemy_mirror_state* s, void* a) {
    u32 link = EM_PLAYER_INFO0;
    u32 src[SRC_WORDS];
    u8* sb = (u8*)src;
    cXyz c;
    int i;

    // remote-Link ATTRIB (design section 8.6): the sphere hits as the
    // client's own body while it is still a LIVE, unparked body of this
    // module (it may have parked since em_hit_apply), so damage_check's
    // attacker, cut type and knockback bearing are the real attacker's;
    // else today's path.
    if (s->ha_attrib == 1) {
        link = remote_attrib_body(s->ha_flags);
        if (!link) {
            s->ha_attrib = 2;
            link = EM_PLAYER_INFO0;
        }
    }
    // The Link the sphere hits as: the word at_power_check reads, a PLAYER,
    // and equal to mpPlayerPtr[0] (during a Medli/Makar window the two
    // differ and the hit would be attributed to an NPC): never a shrink.
    if (s->ha_attrib != 1 &&
        (!em_in_mem1(link) || AF(link, s16, AC_PROC_NAME) != PROC_PLAYER || link != EM_PLAYER_PTR0)) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_LINK);
        return;
    }
    // Link's own mStts.Init(120, 0xFF, this) (the body's with ATTRIB): apid = fopAcM_GetID(link),
    // mp_actor = link, room (d_cc_d.cpp:66-88; c_cc_d.cpp:185-192). On
    // every submit, not keyed on the pointer: the module outlives a stage
    // load and a recreated Link can sit at the old address with a new
    // process id; a stale apid makes the Moblin's ChkTgHit fail
    // (fopAcM_SearchByID NULL, d_cc_d.cpp:14-24,188-197; d_cc_s.cpp:434)
    // while our At record still reads as a cross. No allocation; nothing
    // holds the stts between Moves. stts_link stays as a diagnostic.
    dCcD_Stts_ct(EM_STTS);
    dCcD_Stts_Init(EM_STTS, EM_STTS_WEIGHT, EM_STTS_P1, (void*)link);
    s->stts_link = link;
    if (!enemy_mirror_sph_image(s, a)) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_SPHERE);
        return;
    }

    // The dCcD_SrcSph (read only during Set): Link's sword literal with the
    // wire's values (d_a_player_main_data.inc:133-161; facts at-sphere
    // tables[0]).
    for (i = 0; i < SRC_WORDS; i++) src[i] = 0;
    AF(sb, u32, SRC_AT_TYPE) = s->ha_attype ? s->ha_attype : AT_TYPE_SWORD;
    AF(sb, u8, SRC_AT_ATP) = s->ha_atp ? s->ha_atp : 1;
    AF(sb, u32, SRC_AT_SPRM) = CC_AT_SPRM_SET_VS_ENEMY;
    AF(sb, u8, SRC_GAT_SE) = s->ha_se ? s->ha_se : DCCG_SE_SWORD;
    AF(sb, u8, SRC_GAT_HITMARK) = s->ha_hitmark ? s->ha_hitmark : DCCG_HITMARK_NRM;
    AF(sb, u8, SRC_GAT_SPL) = s->ha_spl > 1 ? 1 : s->ha_spl;   // >= 8 would be unguardable (d_cc_d.cpp:174-176)
    AF(sb, u32, SRC_GAT_SPRM) = (s->ha_flags & WWENEMY_HAF_NO_HITMARK) ? DCCG_AT_SPRM_NO_HITMARK : 0;
    em_hit_center(s, a, &c);
    AF(sb, f32, SRC_CENTER + 0) = c.x;
    AF(sb, f32, SRC_CENTER + 4) = c.y;
    AF(sb, f32, SRC_CENTER + 8) = c.z;
    AF(sb, f32, SRC_RADIUS) = EM_AT_RADIUS;
    // dCcD_GObjInf::Set + cCcD_SphAttr::Set (d_cc_d.cpp:369-373); the stts
    // store is an inline (c_cc_d.h:608) Set does not touch.
    dCcD_Sph_Set(EM_SPH, src);
    AF(EM_SPH, u32, CC_STTS) = (u32)EM_STTS;
    em_hit_register(s, a);
    s->ha_state = EM_HA_SET;
    s->ha_actor = (u32)a;
    s->ha_frames = 0;
}

// Every execute while a request is open; live == 0 on an early return,
// a release of everything, delete or unlink: the request is acked
// HD_ABORTED (a Set happens in execute and its Move in the same frame's
// draw, so no Set is left behind; the objects are static anyway). An
// abort in EM_HA_SET reads the static At record first: a cross on the
// target's head sphere or Tg cylinder in the last Move means the Moblin's
// damage_check takes the hit anyway, so it is APPLIED_AT. Only ha_actor's
// value is compared; the actor may be gone, so it is never dereferenced
// (no parry check here).
static void em_hit_tick(enemy_mirror_state* s, int live) {
    void* a;
    if (s->ha_state == EM_HA_IDLE) return;
    if (!live) {
        if (s->ha_state == EM_HA_SET && s->ha_actor != 0 && (AF(EM_SPH, u32, CC_AT_RPRM) & 1)) {
            u32 obj = AF(EM_SPH, u32, CC_AT_HIT_OBJ);
            if (obj == s->ha_actor + MO2_HEAD_SPH || obj == s->ha_actor + MO2_TG_CYL) {
                em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
                return;
            }
        }
        em_sat_inc(&s->canary2[3]);
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_ABORTED);
        return;
    }
    em_sat_inc(&s->ha_frames);
    a = em_hit_actor(s);
    if (!a) {
        em_hit_publish(s, WWENEMY_HR_NO_ACTOR, WWENEMY_HD_NONE);
        return;
    }
    if (s->ha_state == EM_HA_WAIT_INVULN) {
        // The Moblin's 5-frame invuln after any hit (d_a_mo2.cpp:3076): wait
        // for it instead of dropping, so a client's second combo hit lands.
        if (AF(a, s16, MO2_INVULN) == 0) {
            em_hit_submit(s, a);
        } else if (++s->ha_waited > EM_INVULN_WAIT) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
        }
        return;
    }
    // EM_HA_SET, one execute after a Set, BEFORE any Set: the records from
    // that frame's Move. The defence sphere's Tg hit bit (the Moblin Sets
    // it inside damage_check every defence frame, d_a_mo2.cpp:3034-3036,
    // so a cross shows here and is cleared only at the next Move); our At
    // record (mRPrm bit 0 + mHitObj), which no Move clears while the
    // sphere stays un-Set (ClrAtHitInf walks only that frame's objects,
    // c_cc_s.cpp:115-118). Never re-Set after a cross: the Moblin's Tg
    // NoConHit would dedupe it and its Move would clear the record.
    {
        u32 def = AF(a, u32, MO2_DEF_SPH + CC_TG_RPRM) & 1;
        u32 w = AF(EM_SPH, u32, CC_AT_RPRM);
        u32 obj = AF(EM_SPH, u32, CC_AT_HIT_OBJ);
        if (def) {
            em_hit_publish(s, WWENEMY_HR_PARRIED, WWENEMY_HD_NONE);
        } else if ((w & 1) && (obj == (u32)a + MO2_HEAD_SPH || obj == (u32)a + MO2_TG_CYL)) {
            em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
        } else if (w & 1) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_WRONG_OBJ);
        } else if (s->ha_sets < EM_MAX_SETS) {
            em_hit_register(s, a);               // the collider was not Set that frame, or the head moved
        } else {
            em_sat_inc(&s->canary2[2]);
            // Never on a defending Moblin: its head and Tg colliders are
            // lowered out of reach (d_a_mo2.cpp:3586-3588) and the shield
            // parries in the real game (v1 drops it as HD_DEFENCE). The
            // shield radius is set positive only in defence (:2219, :2249)
            // and to -200 otherwise (:3071).
            if ((s->ha_flags & WWENEMY_HAF_FALLBACK_SHRINK) && AF(a, s16, MO2_INVULN) == 0 &&
                AF(a, s16, MO2_DR_MODE) > MO2_MODE_RAGDOLL &&
                AF(a, s16, MO2_DR_ACTION) != MO2_ACTION_DEFENCE &&
                !(AF(a, f32, MO2_DEF_SPH + CC_SHAPE_RADIUS) > 0.0f)) {
                AF(a, s8, MO2_ICE_SHRINK) = 1;
                em_hit_publish(s, WWENEMY_HR_SHRINK, WWENEMY_HD_FALLBACK_SHRINK);
            } else {
                em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_CROSS);
            }
        }
    }
}

// A new request (ha_seq nonzero and != hit_apply_ack) while none is open.
static void em_hit_apply(enemy_mirror_state* s) {
    void* a;
    int delay = 0;
    s->hit_apply_ack = s->ha_seq;                // taken; the block's ack word carries the result
    s->ha_frames = 0;
    s->ha_sets = 0;
    s->ha_waited = 0;
    s->ha_actor = 0;
    s->ha_attrib = 0;
    a = em_hit_actor(s);
    if (!a) {
        em_hit_publish(s, WWENEMY_HR_NO_ACTOR, WWENEMY_HD_NONE);
        return;
    }
    if (s->ha_flags & WWENEMY_HAF_FORCE_V1) {
        // v1 verbatim: the pre-drops, else the K1 shrink.
        if (AF(a, s16, MO2_INVULN) != 0) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
        } else if (AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_DEFENCE) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DEFENCE);
        } else if (AF(a, s16, MO2_DR_MODE) <= MO2_MODE_RAGDOLL) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_RAGDOLL);
        } else {
            AF(a, s8, MO2_ICE_SHRINK) = 1;
            em_hit_publish(s, WWENEMY_HR_SHRINK, WWENEMY_HD_NONE);
        }
        return;
    }
    // remote-Link ATTRIB (design section 8.6): with the client's body LIVE
    // the Moblin's latch is forced onto it for 3 executes (honoured with
    // WWREMOTE_FLAG_ATTACKER), and when it was not the current target the
    // first Set waits one keeper execute (the WAIT_INVULN state), so the
    // consuming execute's damage_check bearing (m05D6) is computed toward
    // the body (d_a_mo2.cpp:3112-3163, 2903-2911). No LIVE body: today's
    // host-Link path, detail WWREMOTE_HD_NO_BODY.
    if (s->ha_flags & WWREMOTE_HAF_ATTRIB_BODY) {
        u32 body = remote_attrib_body(s->ha_flags);
        s->ha_attrib = body ? 1 : 2;
        if (body) delay = remote_attrib_force((u32)a, body);
    }
    // v2: the colliders sit at y-20000 while ragdolling (d_a_mo2.cpp:3580-3593)
    // and a dying Moblin takes no hit; defence is not pre-dropped (the
    // shield parries, the faithful outcome).
    if (AF(a, s16, MO2_DR_MODE) <= MO2_MODE_RAGDOLL) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_RAGDOLL);
    } else if (AF(a, s8, AC_HEALTH) <= 0 || AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_FAIL) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DYING);
    } else if (AF(a, s16, MO2_INVULN) != 0 || delay) {
        s->ha_state = EM_HA_WAIT_INVULN;
        s->ha_actor = (u32)a;
    } else {
        em_hit_submit(s, a);
    }
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
        em_hit_tick(s, 0);
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
        em_hit_tick(s, 0);
        em_write_status(s);
        return;
    }

    // 1 scope: release before the stage teardown deletes the actors.
    if (!em_in_scope(s)) {
        em_release_all(s, 0);
        em_hit_tick(s, 0);
        em_write_status(s);
        return;
    }

    // 2..8 per slot
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) em_slot_frame(s, i, frame);

    // host role: the open request first (its record from last frame's
    // Move), then a new one
    em_hit_tick(s, 1);
    // (seq 0 is never a request: a block written before the first hit)
    if (s->role == WWENEMY_ROLE_HOST && s->ha_state == EM_HA_IDLE && s->ha_seq != 0 && s->ha_seq != s->hit_apply_ack)
        em_hit_apply(s);

    em_write_status(s);
}

void enemy_mirror_delete(enemy_mirror_state* s) {
    em_release_all(s, 1);
    em_hit_tick(s, 0);
    em_write_status(s);
    if (g_live == s) g_live = 0;
}

void enemy_mirror_unlink(void) {
    if (g_live) {
        em_release_all(g_live, 1);
        em_hit_tick(g_live, 0);
        g_live = 0;
    }
}
