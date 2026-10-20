// remote.c: remote-Link in the REL. Host Moblins target and hit every
// player: a lite body per remote player, a pointer swap around each
// eligible enemy's execute, the host's hit ring, the client's "v2 in
// reverse" apply and the attribution of a client's sword hit to its body.
// Design: ww-multiplayer-handoff/design/remote-link-design.md sections 5,
// 6, 7.1, 8.5, 8.6 and its "Step -1 results" (they override the inferred
// offsets); the mailbox contract is include/wwremote.h; docs:
// inject/rel/README.md "remote.c".
//
// Pieces, all behind Go-written flags in the WWRP block (magic 0 = none):
//   keeper   (rel_main.c's actor, params 0) every frame after the Moblin
//            mirror: block intake (enemy-block rules: magic, layout, role,
//            seq_head == seq_tail, 90-frame stale), latch reaping, the
//            body lifecycle (host, FLAG_BODIES: create through
//            fopAcM_create, LIVE, delete on slot release / stage change /
//            gen change), the client apply (client, FLAG_APPLY) and the
//            status words; it publishes its swap state at
//            WWREL_SWAP_STATE_OFF for the hook and the bodies;
//   body     (params WWREMOTE_BODY_PARAMS_TAG | slot << 8 | gen, host
//            only): create copies __vt__9daPy_py_c and the host Link's
//            mCyl and neutralises them; execute parks or poses itself from
//            the slot, reads its Tg hit into the ring (FLAG_DETECT) and
//            Sets its cylinder (FLAG_HITTABLE);
//   full     (FLAG_FULL_BODIES, host only; stage-4 plan D4-6, D4-7): the
//            keeper may create the slot's body as the real daPy_lk_c of
//            the second profile (proc WWREMOTE_FULL_PROC, fullbody.c)
//            when Go's heap gate set the slot's FULL_ALLOW bit; any full
//            failure falls back to a lite body for the rest of the stage.
//            With the flag clear nothing here differs from the lite path;
//   hook     wwrel_exec_hook, T2's exec shim target for every actor
//            execute: counts, then for an eligible enemy (PROC_POLICY,
//            gates) writes mPlayerInfo[0].mpPlayer = the chosen Link
//            around the original fpcMtd_Execute and restores it
//            unconditionally.
// Every game call is an absolute function-pointer cast (no relocation);
// no libc and no float library calls; no state outside the actors except
// the static collision objects enemy_mirror.c owns in the slack.
//
// Offsets are the zeldaret/tww GZLE01 decomp's: include/f_pc/f_pc_base.h,
// include/f_op/f_op_actor.h, include/d/actor/d_a_player.h and
// d_a_player_main.h (daPy_py_c, daPy_lk_c), include/d/actor/d_a_mo2.h,
// include/c/c_damagereaction.h, include/SSystem/SComponent/c_cc_d.h and
// include/d/d_cc_d.h (colliders; the Link mCyl offsets were peeked live in
// Step -1). Function and data addresses: config/GZLE01/symbols.txt.

#include "mailbox_core.h"
#include "wwrel.h"
#include "remote.h"

// --- The block ------------------------------------------------------------
#define RB_ADDR      (MAILBOX_ADDR + WWREMOTE_OFF)
#define RB_U32(off)  (*(volatile u32*)(RB_ADDR + (off)))
#define RB_U16(off)  (*(volatile u16*)(RB_ADDR + (off)))
#define RB_U8(off)   (*(volatile u8*)(RB_ADDR + (off)))
#define RB_S8(off)   (*(volatile s8*)(RB_ADDR + (off)))
#define RB_S16(off)  (*(volatile s16*)(RB_ADDR + (off)))
#define RB_F32(off)  (*(volatile f32*)(RB_ADDR + (off)))
#define RB_SLOT(i)   (WWREMOTE_SLOTS_OFF + (i) * WWREMOTE_SLOT_SIZE)
#define RL_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + (off)))

// The block lies past the enemy region, nests, and stays below the arena
// (0x80414000, inject/build.py's OSInit patch).
typedef char rl_check_block[(WWREMOTE_REGION_START >= WWENEMY_REGION_END &&
                             WWREMOTE_OFF >= WWREMOTE_REGION_START &&
                             WWREMOTE_OFF + WWREMOTE_SIZE == WWREMOTE_REGION_END &&
                             MAILBOX_ADDR + WWREMOTE_REGION_END <= 0x80414000 &&
                             (WWREMOTE_OFF & 3) == 0) ? 1 : -1];
typedef char rl_check_go_span[(WWREMOTE_SLOTS_OFF + WWREMOTE_MAX_SLOTS * WWREMOTE_SLOT_SIZE <= WWREMOTE_APPLY_OFF &&
                               WWREMOTE_APPLY_OFF + WWREMOTE_APPLY_SIZE <= WWREMOTE_FF_DENY_OFF &&
                               WWREMOTE_FF_DENY_OFF + WWREMOTE_FF_DENY_MAX * WWREMOTE_STAGE_LEN <= WWREMOTE_POLICY_OFF &&
                               WWREMOTE_POLICY_OFF + WWREMOTE_PROC_POLICY_MAX * WWREMOTE_POLICY_SIZE <= WWREMOTE_SEQ_TAIL_OFF &&
                               WWREMOTE_SEQ_TAIL_OFF + 4 == WWREMOTE_GO_SPAN &&
                               WWREMOTE_GO_SPAN + 4 <= WWREMOTE_ACK_SEQ_OFF) ? 1 : -1];
typedef char rl_check_rel_span[(WWREMOTE_ACK_SEQ_OFF + 4 == WWREMOTE_ACK_FRAME_OFF &&
                                WWREMOTE_ACK_FRAME_OFF + 4 <= WWREMOTE_BODY_STATUS_OFF &&
                                WWREMOTE_BODY_STATUS_OFF + WWREMOTE_MAX_SLOTS * WWREMOTE_BODY_STATUS_SIZE <= WWREMOTE_COUNTERS_OFF &&
                                WWREMOTE_COUNTERS_OFF + 4 * WWREMOTE_COUNTERS <= WWREMOTE_HIT_RING_HEAD_OFF &&
                                WWREMOTE_HIT_RING_HEAD_OFF + 4 <= WWREMOTE_HIT_RING_OFF &&
                                WWREMOTE_HIT_RING_OFF + WWREMOTE_HIT_RING_LEN * WWREMOTE_HIT_SIZE <= WWREMOTE_APPLY_ACK_OFF &&
                                WWREMOTE_APPLY_ACK_OFF + 4 == WWREMOTE_APPLY_RESULT_OFF &&
                                WWREMOTE_APPLY_RESULT_OFF + 4 == WWREMOTE_LIFE_BEFORE_OFF &&
                                WWREMOTE_LIFE_BEFORE_OFF + 2 == WWREMOTE_LIFE_AFTER_OFF &&
                                WWREMOTE_LIFE_AFTER_OFF + 2 <= WWREMOTE_SIZE &&
                                (WWREMOTE_HIT_RING_OFF & 3) == 0 && (WWREMOTE_COUNTERS_OFF & 3) == 0) ? 1 : -1];
typedef char rl_check_fields[(WWREMOTE_SL_STAGE_OFF + WWREMOTE_STAGE_LEN <= WWREMOTE_SL_POS_OFF &&
                              WWREMOTE_SL_POS_OFF + 12 <= WWREMOTE_SL_ANGLE_OFF &&
                              WWREMOTE_SL_CYL_H_OFF + 4 <= WWREMOTE_SL_POSE_SLOT_OFF &&
                              WWREMOTE_SL_PAD_OFF + 2 == WWREMOTE_SLOT_SIZE &&
                              WWREMOTE_AP_RVEC_OFF + 12 <= WWREMOTE_APPLY_SIZE &&
                              WWREMOTE_HE_FRAME_OFF + 4 == WWREMOTE_HIT_SIZE &&
                              WWREMOTE_BS_KIND_OFF + 1 == WWREMOTE_BODY_STATUS_SIZE &&
                              WWREMOTE_HIT_RING_LEN == 4 &&
                              WWREMOTE_ROLE_HOST == WWENEMY_ROLE_HOST &&
                              WWREMOTE_ROLE_CLIENT == WWENEMY_ROLE_CLIENT &&
                              MAX_REMOTE_LINKS <= WWREMOTE_MAX_SLOTS &&
                              MAX_REMOTE_LINKS <= MAILBOX_POSE_SLOT_CAP &&
                              MAX_REMOTE_LINKS <= 8) ? 1 : -1];
// The ATTRIB bits extend WWENEMY_HAF_* (0x01, 0x02, 0x04) without overlap,
// and HD_NO_BODY sits past the enemy block's details (0..15).
typedef char rl_check_attrib[(((WWENEMY_HAF_FORCE_V1 | WWENEMY_HAF_FALLBACK_SHRINK | WWENEMY_HAF_NO_HITMARK) &
                               (WWREMOTE_HAF_ATTRIB_BODY | WWREMOTE_HAF_SLOT1)) == 0 &&
                              WWREMOTE_HD_NO_BODY > WWENEMY_HD_DEMO) ? 1 : -1];
// The keeper's swap state and the body overlay.
typedef char rl_check_state[(sizeof(rl_latch) == 0x10 && sizeof(rl_body) == 0x14 &&
                             __builtin_offsetof(rl_body, kind) == 0x13 &&
                             __builtin_offsetof(remote_swap_state, flags) == 0x10 &&
                             __builtin_offsetof(remote_swap_state, full_allow) == 0x19 &&
                             __builtin_offsetof(remote_swap_state, flags2) == 0x1A &&
                             __builtin_offsetof(remote_swap_state, dy_max) == 0x1C &&
                             __builtin_offsetof(remote_swap_state, full_denied) == 0x79 &&
                             __builtin_offsetof(remote_swap_state, full_wait) == 0x7A &&
                             __builtin_offsetof(remote_swap_state, latch) == 0x7C &&
                             sizeof(remote_swap_state) == 0x17C &&
                             MAX_REMOTE_LINKS <= 2 && MAX_REMOTE_LINKS <= 8 &&
                             __builtin_offsetof(remote_swap_state, stage) == 0x20 &&
                             __builtin_offsetof(remote_swap_state, bodies) == 0x28 &&
                             __builtin_offsetof(remote_swap_state, ap_seq) == 0x28 + MAX_REMOTE_LINKS * 0x14 &&
                             __builtin_offsetof(remote_swap_state, latch) % 4 == 0 &&
                             WWREMOTE_KEEPER_SWAP_OFF + sizeof(remote_swap_state) <= WWREMOTE_ACTOR_SIZE) ? 1 : -1];
typedef char rl_check_body[(__builtin_offsetof(remote_body, cut_type) == WWREMOTE_BODY_CUT_TYPE_OFF &&
                            __builtin_offsetof(remote_body, cut_count) == WWREMOTE_BODY_CUT_COUNT_OFF &&
                            __builtin_offsetof(remote_body, damage_wait) == WWREMOTE_BODY_DAMAGE_WAIT_OFF &&
                            __builtin_offsetof(remote_body, grab_wear) == WWREMOTE_BODY_GRAB_WEAR_OFF &&
                            __builtin_offsetof(remote_body, demo) == WWREMOTE_BODY_DEMO_OFF &&
                            __builtin_offsetof(remote_body, vptr) == WWREMOTE_BODY_VPTR_OFF &&
                            __builtin_offsetof(remote_body, vt) == WWREMOTE_BODY_VT_OFF &&
                            __builtin_offsetof(remote_body, cyl) == WWREMOTE_BODY_CYL_OFF &&
                            __builtin_offsetof(remote_body, stts) == WWREMOTE_BODY_STTS_OFF &&
                            __builtin_offsetof(remote_body, magic) == WWREMOTE_BODY_MAGIC_OFF &&
                            __builtin_offsetof(remote_body, slot) == WWREMOTE_BODY_SLOT_OFF &&
                            __builtin_offsetof(remote_body, gen) == WWREMOTE_BODY_GEN_OFF &&
                            __builtin_offsetof(remote_body, iframes_left) == WWREMOTE_BODY_IFRAMES_LEFT_OFF &&
                            __builtin_offsetof(remote_body, state) == WWREMOTE_BODY_STATE_OFF &&
                            __builtin_offsetof(remote_body, shield_angle) == WWREMOTE_BODY_SHIELD_ANGLE_OFF &&
                            __builtin_offsetof(remote_body, hand_mtx) == WWREMOTE_BODY_HAND_MTX_OFF &&
                            __builtin_offsetof(remote_body, last_pose_seq) == WWREMOTE_BODY_LAST_POSE_SEQ_OFF &&
                            __builtin_offsetof(remote_body, park_since) == WWREMOTE_BODY_PARK_SINCE_OFF &&
                            __builtin_offsetof(remote_body, pose_seq_frame) == WWREMOTE_BODY_END &&
                            WWREMOTE_BODY_STTS_OFF + WWREMOTE_BODY_STTS_SIZE <= WWREMOTE_BODY_MAGIC_OFF &&
                            WWREMOTE_BODY_VT_OFF + WWREMOTE_BODY_VT_SIZE <= WWREMOTE_BODY_CYL_OFF &&
                            sizeof(remote_body) <= WWREMOTE_ACTOR_SIZE) ? 1 : -1];
// rl_btail is the lite body's +0x530..+0x578, field for field.
#define RL_BT(f) (__builtin_offsetof(remote_body, magic) + __builtin_offsetof(rl_btail, f) == __builtin_offsetof(remote_body, f))
typedef char rl_check_btail[(RL_BT(magic) && RL_BT(slot) && RL_BT(gen) && RL_BT(iframes_left) && RL_BT(state) &&
                             RL_BT(shield_angle) && RL_BT(hand_mtx) && RL_BT(last_pose_seq) && RL_BT(park_since) &&
                             RL_BT(pose_seq_frame) && sizeof(rl_btail) == 0x48 &&
                             __builtin_offsetof(remote_body, magic) + sizeof(rl_btail) == sizeof(remote_body)) ? 1 : -1];
#undef RL_BT
// The full body's tail keeps that order at WWREMOTE_FULL_TAIL_OFF.
#define RL_FT(f, off) (WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_btail, f) == (off))
typedef char rl_check_full_tail[(RL_FT(magic, WWREMOTE_FULL_MAGIC_OFF) && RL_FT(slot, WWREMOTE_FULL_SLOT_OFF) &&
                                 RL_FT(gen, WWREMOTE_FULL_GEN_OFF) && RL_FT(iframes_left, WWREMOTE_FULL_IFRAMES_LEFT_OFF) &&
                                 RL_FT(state, WWREMOTE_FULL_STATE_OFF) && RL_FT(shield_angle, WWREMOTE_FULL_SHIELD_ANGLE_OFF) &&
                                 RL_FT(last_pose_seq, WWREMOTE_FULL_LAST_POSE_SEQ_OFF) &&
                                 RL_FT(park_since, WWREMOTE_FULL_PARK_SINCE_OFF) &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, phase) == WWREMOTE_FULL_PHASE_OFF &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, kind) == WWREMOTE_FULL_KIND_OFF &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, create_frames) == WWREMOTE_FULL_CREATE_FRAMES_OFF &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, shanm_diff) == WWREMOTE_FULL_SHANM_DIFF_OFF &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, heap_err) == WWREMOTE_FULL_HEAP_ERR_OFF &&
                                 WWREMOTE_FULL_TAIL_OFF + __builtin_offsetof(rl_full_tail, init_bad) == WWREMOTE_FULL_END &&
                                 WWREMOTE_FULL_TAIL_OFF == WWREMOTE_FULL_DAPY_SIZE &&
                                 WWREMOTE_FULL_TAIL_OFF + sizeof(rl_full_tail) <= WWREMOTE_FULL_SIZE &&
                                 (WWREMOTE_FULL_SIZE & 0x1F) == 0 && sizeof(rl_full_tail) == 0x58) ? 1 : -1];
#undef RL_FT
// The pose-feed arrays the park check reads (mailbox_core.h: +0xB4, +0xB6).
typedef char rl_check_pose[(__builtin_offsetof(Mailbox, pose_buf_states) == 0xB4 &&
                            __builtin_offsetof(Mailbox, pose_seqs) == 0xB6) ? 1 : -1];

// --- Game data and functions (symbols.txt; absolute, no relocations) -----
// play.mPlayerInfo[0].mpPlayer = dComIfGp_getPlayer(0), the word every
// mo2 player read resolves to (play + 0x48A4, d_com_inf_game.h:787-791)
#define RL_PLAYER_INFO0   (*(volatile u32*)0x803CA74C)
// play.mpPlayerPtr[0] = dComIfGp_getLinkPlayer() (wwapi.h PLAYER_PTR_ARRAY)
#define RL_PLAYER_PTR0    (*(volatile u32*)0x803CA754)
#define RL_CUR_STAGE_NAME ((volatile const u8*)0x803C9D3C) // mCurStage.mName (enemy_mirror.c EM_CUR_STAGE_NAME)
#define RL_STAY_NO        (*(volatile s8*)0x803F6A78)      // dStage_roomControl_c::mStayNo, symbols.txt:18667
#define RL_CCS            ((void*)0x803C72AC)              // dComIfG_Ccsp() (enemy_mirror.c EM_CCS)
#define RL_LIFE           (*(volatile u16*)0x803C4C0A)     // dSv_player_status_a_c mLife (wwapi/addr Life)
#define RL_COLLECT        (*(volatile u8*)WWREMOTE_COLLECT_ADDR) // mCollect[0] (d_save.cpp:756-759)
// The vtables the body create fingerprints (symbols.txt:15805,15801,15347,
// 4449,4808): checkPlayerGuard is slot 5, vt+0x1C (MWCC: RTTI, offset,
// then the 37 virtuals of d_a_player.h:614-650 from +0x08).
#define RL_VT_DAPY_PY     0x8039044C
#define RL_VT_DAPY_LK     0x8038FDC0
#define RL_VT_DCCD_CYL    0x8038AF08
#define RL_GUARD_PY_FN    0x800F0BE0      // checkPlayerGuard__9daPy_py_cCFv (returns FALSE)
#define RL_GUARD_LK_FN    0x8010552C      // checkPlayerGuard__9daPy_lk_cCFv
#define RL_VT_LEFT_HAND   0x08            // getLeftHandMatrix (pure)
#define RL_VT_RIGHT_HAND  0x0C            // getRightHandMatrix (pure)
#define RL_VT_GROUND_Y    0x10            // getGroundY (pure)
#define RL_VT_GUARD       0x1C            // checkPlayerGuard
#define RL_VT_ANM_RATE    0x48            // getBaseAnimeFrameRate (pure)
#define RL_VT_ANM_FRAME   0x4C            // getBaseAnimeFrame (pure)
#define RL_MEM1_LO        0x80000000u
#define RL_MEM1_HI        0x83000000u     // 48 MB MEM1 (docs/05 "48 MB MEM1")

typedef int (*rl_mtd_execute_t)(void* mtd, void* data);                 // fpcMtd_Execute__FP20process_method_classPv
#define fpcMtd_Execute        ((rl_mtd_execute_t)0x8003EF38)
typedef void (*rl_set_stage_layer_t)(void* proc);                       // fopAcM_setStageLayer__FPv
#define fopAcM_setStageLayer  ((rl_set_stage_layer_t)0x80024060)
typedef void* (*rl_get_ac_t)(void* gobj);                               // GetAc__22dCcD_GAtTgCoCommonBaseFv
#define dCcD_GAtTgCo_GetAc    ((rl_get_ac_t)0x800AB2AC)
typedef void (*rl_set_c_t)(void* shape, const cXyz* c);                 // SetC__8cM3dGCylFRC4cXyz
#define cM3dGCyl_SetC         ((rl_set_c_t)0x80251D88)
typedef void (*rl_set_f_t)(void* shape, f32 v);                         // SetH__8cM3dGCylFf / SetR__8cM3dGCylFf
#define cM3dGCyl_SetH         ((rl_set_f_t)0x80252020)
#define cM3dGCyl_SetR         ((rl_set_f_t)0x8025214C)
typedef void (*rl_ccs_set_t)(void* ccs, void* obj);                     // Set__4cCcSFP8cCcD_Obj
#define cCcS_Set              ((rl_ccs_set_t)0x80242D04)
typedef void (*rl_clr_hit_t)(void* obj);                                // ClrTgHit / ClrAtHit__12dCcD_GObjInfFv
#define dCcD_GObjInf_ClrTgHit ((rl_clr_hit_t)0x800AB958)
#define dCcD_GObjInf_ClrAtHit ((rl_clr_hit_t)0x800AB7C4)
typedef void (*rl_sph_set_t)(void* sph, const void* src);               // Set__8dCcD_SphFRC11dCcD_SrcSph
#define dCcD_Sph_Set          ((rl_sph_set_t)0x800ABFC0)
typedef void (*rl_sph_cat_t)(void* sph, cXyz* pos);                     // StartCAt / MoveCAt__8dCcD_SphFR4cXyz
#define dCcD_Sph_StartCAt     ((rl_sph_cat_t)0x800AC02C)
#define dCcD_Sph_MoveCAt      ((rl_sph_cat_t)0x800AC078)
typedef void (*rl_stts_ct_t)(void* stts);                               // __ct__9dCcD_SttsFv
#define dCcD_Stts_ct          ((rl_stts_ct_t)0x80100EB8)
typedef void (*rl_stts_init_t)(void* stts, int weight, int p1, void* actor); // Init__9dCcD_SttsFiiP10fopAc_ac_c
#define dCcD_Stts_Init        ((rl_stts_init_t)0x800AB3DC)

// --- Actor offsets ----------------------------------------------------------
// base_process_class (f_pc_base.h:13-32)
#define AC_PID            0x004   // fpc_ProcID mBsPcId
#define AC_PROC_NAME      0x008   // s16 mProcName (mProfName +0x0E stays 0xB5: module refcounting)
#define AC_PARAMS         0x0B0   // u32 mParameters
// fopAc_ac_c (f_op_actor.h:268-321)
#define AC_SETID          0x1BC   // u16 setID
#define AC_GROUP          0x1BE   // u8 group: 2 = fopAc_ENEMY_e
#define AC_OLD            0x1E4   // actor_place old (0x14 bytes)
#define AC_CURRENT        0x1F8   // actor_place current
#define AC_CUR_POS        0x1F8   // cXyz current.pos
#define AC_CUR_ANGLE_Y    0x206   // s16 current.angle.y
#define AC_CUR_ROOM       0x20A   // s8 current.roomNo
#define AC_SHAPE_ANGLE_Y  0x20E   // s16 shape_angle.y
#define AC_SPEEDF         0x254   // f32 speedF
#define AC_EYE_POS        0x260   // cXyz eyePos (f_op_actor.h:304)
#define AC_ATTN_FLAGS     0x280   // u32 attention_info.flags
#define GROUP_ENEMY       2
#define PROC_PLAYER       0x00A9  // fpcNm_PLAYER_e
#define PROC_WWREL        0x00B5  // fpcNm_RECTANGLE_e, the hijacked proc
// daPy_lk_c (d_a_player_main.h:2302,2340-2341)
#define LK_MODE_FLG       0x3618  // u32 mModeFlg
#define LK_MODE_DAMAGE    0x08    // daPyModeFlg ModeFlg_DAMAGE (d_a_player_main.h:899)
#define LK_CYL            0x4024  // dCcD_Cyl mCyl (0x130)
// mo2_class (d_a_mo2.h; actions d_a_mo2.cpp:23-43, Step -1)
#define MO2_MODE          0x2BC   // u8 mMode: 5 carried, 0xF statue (enemy_mirror.c)
#define MO2_DR_ACTION     0x61A   // s16 mDamageReaction.mAction
#define MO2_M714          0xD28   // fopAc_ac_c* mDamageReaction.m714 (+0x614 + 0x714, c_damagereaction.h:170)
#define MO2_TG_CYL        0x21BC  // dCcD_Cyl mTgCyl
#define MO2_HEAD_SPH      0x22EC  // dCcD_Sph mHeadSph
#define MO2_DEF_SPH       0x2418  // dCcD_Sph mDefenseSph
#define MO2_WEAPON_SPH    0x2544  // dCcD_Sph mWeaponSph (d_a_mo2.h:113, Step -1)
#define MO2_WEAPON2_SPH   0x2670  // dCcD_Sph mWeapon2Sph (d_a_mo2.h:114)
#define MO2_2A1D          0x2A1D  // s8  demo camera / jail step (d_a_mo2.cpp:3272-3309)
#define MO2_2A48          0x2A48  // s16 jail timer (:1813, 3413-3417)
#define MO2_2A4A          0x2A4A  // u8  (:1814, 3405)
#define MO2_2A4B          0x2A4B  // u8  e3_demo end (:2836, 3391)
#define MO2_2A4C          0x2A4C  // s16 (:1815, 2789, 3395-3397)
#define MO2_MODE_CARRIED  5
#define MO2_MODE_STATUE   0xF
#define MO2_ACTION_FIGHT  5       // ACTION_FIGHT (FIGHT_RUN is 4)
#define MO2_ACTION_E3_DEMO 40     // ACTION_E3_DEMO
#define PROC_MO2          WWREMOTE_PROC_MO2
// Other ALLOW candidates with a damagereaction (c_damagereaction.h:170
// m714 = the cached target) or a cutscene path the swap must skip
// (enemy audit 2026-10-20, ww-multiplayer-handoff archive).
#define PROC_BK           0x00BE  // Bokoblin (f_pc_name.h)
#define PROC_TN           0x00C0  // Darknut
#define PROC_CC           0x00CF  // ChuChu
#define PROC_OSHIP        0x00B4  // enemy warship
#define BK_M714           0xA84   // dr (+0x370, d_a_bk.h:72) + 0x714
#define BK_TYPE           0x2B4   // u8 mType: 0xA = the sword-guard demo (d_a_bk.cpp:3911-4100)
#define BK_TYPE_DEMO      0xA
#define BK_1234           0x1234  // s8 demo step (d_a_bk.cpp:3921-3931)
#define TN_M714           0xB88   // mDamageReaction (+0x474, d_a_tn.h:84) + 0x714
#define TN_14F8           0x14F8  // s8 boss demo camera (d_a_tn.cpp:2551-2565 orderPotentialEvent)
#define CC_BEHAVIOR       0x2F0   // u8 mBehaviorType: 2 = Deku Tree ChuChu (events :1483, :1533)
#define CC_ACTION         0x2F4   // u8 mCurrAction: 5 = the Deku demo action (:886, :1034)
#define CC_304            0x304   // u8: "not the player" insta-kill variant (:477)
#define OSHIP_PROC        0x2AC   // int mCurrentProc: 3 = MODE_DELETE (GOLD_SHIP_DELETE event, :736)
#define OSHIP_MODEL       0x2B6   // u8 mModelType: != 0xFF = the gold ship
#define AT_TYPE_UNK800    0x800   // the spear's At type during a thrust (Step -1)
// Collider objects: dCcD_Cyl/Sph = dCcD_GObjInf (0xF8) + cCcD_ShapeAttr
// (0x20) + the shape at +0x118. cCcD_ObjAt at +0x00 (mSPrm, mRPrm, mHitObj,
// vptr +0x0C, mType +0x10, mAtp +0x14: c_cc_d.h:385-431), Tg at +0x18,
// Co at +0x2C; dCcD_GObjInf's GObjAt at +0x50, GObjTg at +0x94, GObjCo at
// +0xDC (d_cc_d.h:249-345). Every value below was peeked on Link's mCyl in
// Step -1.
#define CC_AT_SPRM        0x00    // u32 bit 0 = Set
#define CC_AT_RPRM        0x04    // u32 bit 0 = hit this Move
#define CC_AT_HIT_OBJ     0x08    // cCcD_Obj*
#define CC_AT_TYPE        0x10    // u32 cCcD_ObjAt mType (the design's +0x0C was the vptr)
#define CC_AT_ATP         0x14    // u8  cCcD_ObjAt mAtp
#define CC_TG_SPRM        0x18    // u32 Link: 0x05 Set|IsPlayer
#define CC_TG_RPRM        0x1C    // u32 bit 0 = hit this Move
#define CC_TG_HIT_OBJ     0x20    // cCcD_Obj* the At that crossed
#define CC_CO_SPRM        0x2C    // u32 Link: 0x73
#define CC_VPTR           0x3C    // __vt__8dCcD_Cyl on Link's mCyl
#define CC_STTS           0x44    // cCcD_Stts*
#define CC_STTS_ACTOR     0x0C    // stts +0x0C: the owner actor (c_cc_d.h:329, Step -1)
#define CC_GAT_RPRM       0x54    // u32 dCcD_GObjAt mRPrm: bit 0 = shield hit (dCcG_AtRPrm_ShieldHit_e)
#define CC_GAT_CALLBACK   0x58    // dCcD_GObjAt mHitCallback
#define CC_GAT_SE         0x6C    // u8 dCcD_GObjAt mSe
#define CC_GAT_HITMARK    0x6E    // u8 mHitMark
#define CC_GAT_SPL        0x6F    // u8 mSpl
#define CC_GTG            0x94    // dCcD_GObjTg (its mSPrm first)
#define CC_GTG_RPRM       0x98    // u32 bit 1 = shield hit (dCcG_TgRPrm_ShieldHit_e)
#define CC_GTG_CALLBACK   0x9C    // dCcD_GObjTg mHitCallback
#define CC_GTG_RVEC       0xC0    // cXyz mRVec
#define CC_GTG_HIT_POS    0xCC    // cXyz mHitPos
#define CC_GTG_SHIELD_PTR 0xD8    // s16* mpShieldFrontRangeYAngle (ChkShieldFrontRange dereferences it, d_cc_s.cpp:82-108)
#define CC_GCO_CALLBACK   0xE4    // dCcD_GObjCo mHitCallback
#define CC_SHAPE          0x118   // cM3dGCyl / cM3dGSph (the SetC `this`)
#define CC_TG_SET_ISPLAYER 0x05   // cCcD_TgSPrm_Set_e | cCcD_TgSPrm_IsPlayer_e
#define CC_GTG_GUARD_BITS 0x09    // dCcG_TgSPrm_Shield_e | dCcG_TgSPrm_ShieldFrontRange_e (d_cc_d.h:56-61)
#define CC_GTG_SHIELD_HIT 0x02
// dCcD_SrcSph (0x40, d_cc_d.h:158-161; enemy_mirror.c SRC_*)
#define SRC_AT_TYPE       0x04
#define SRC_AT_ATP        0x08
#define SRC_AT_SPRM       0x0C
#define SRC_GAT_SE        0x1C
#define SRC_GAT_HITMARK   0x1D
#define SRC_GAT_SPL       0x1E
#define SRC_GAT_SPRM      0x20
#define SRC_CENTER        0x30
#define SRC_RADIUS        0x3C
#define SRC_WORDS         16

// The client apply reuses enemy_mirror.c's static At sphere and stts in
// the slack (idle on a client: the v2 submit is host-only).
#define RL_SPH            ((u8*)(MAILBOX_ADDR + WWENEMY_SPH_OFF))
#define RL_STTS           ((u8*)(MAILBOX_ADDR + WWENEMY_STTS_OFF))
#define RL_AT_RADIUS      30.0f
#define RL_STTS_WEIGHT    120     // Link's own mStts.Init(120, 0xFF, this) (d_a_player_main.cpp:12184)
#define RL_STTS_P1        0xFF
#define RL_AP_MAX_SETS    2       // N, and N+1 if Link's Tg was Set but nothing crossed
#define RL_MAX_TRIES      3       // body creates per stage
#define RL_FULL_WAIT      120     // keeper frames a slot waits for its FULL_ALLOW bit on a transient HEAP_REASON
#define RL_FULL_EARLY     60      // a full body GONE within this many LIVE frames failed (stage-4 plan section 3.2)
// The profile slot the full body is created through (stage-4 plan D4-1,
// D4-7): DMC[0x27] must be DMC[0xB5] (the DOL name-table patch) and the
// profile list's slot 0x27 must be g_profile_WWBODY (the RELS.arc patch).
#define RL_DMC_FULL       (*(volatile u32*)WWREMOTE_DMC_FULL_ADDR)
#define RL_DMC_REL        (*(volatile u32*)WWREMOTE_DMC_REL_ADDR)
#define RL_PROFILE_LIST   (*(volatile u32*)WWREMOTE_PROFILE_LIST_PTR)
#define RL_FORCE_FRAMES   3       // ATTRIB's force latch
#define RL_RANGE          1000000.0f
#define RL_DY_MAX_DEF     ((f32)WWREMOTE_DEF_DY_MAX)
#define RL_EYE_DY_DEF     ((f32)WWREMOTE_DEF_EYE_DY)
#define RL_CYL_R_DEF      ((f32)WWREMOTE_DEF_CYL_R)
#define RL_CYL_H_DEF      ((f32)WWREMOTE_DEF_CYL_H)
#define RL_CYL_MAX        500.0f  // a sane bound for the Go-written radius/height

#define cPhs_COMPLEATE_e  4       // SSystem/SComponent/c_phase.h
#define cPhs_ERROR_e      5       // the game deletes the process

#define AF(a, T, off)     (*(T*)((u8*)(a) + (off)))

// --- Helpers ----------------------------------------------------------------
static remote_swap_state* rl_state(void) {
    return (remote_swap_state*)RL_WORD(WWREL_SWAP_STATE_OFF);
}

static int rl_in_mem1(u32 p);

remote_swap_state* rl_state_checked(void) {
    remote_swap_state* st = rl_state();
    return (st && rl_in_mem1((u32)st)) ? st : 0;
}

static int rl_in_mem1(u32 p) {
    return p >= RL_MEM1_LO && p < RL_MEM1_HI && (p & 3) == 0;
}

static void rl_ctr(int i) {
    RB_U32(WWREMOTE_COUNTERS_OFF + 4 * i) = RB_U32(WWREMOTE_COUNTERS_OFF + 4 * i) + 1;
}

static void rl_sat_inc(u8* c) {
    if (*c != 0xFF) (*c)++;
}

int rl_f32_bad(f32 v) {
    return v != v || v > RL_RANGE || v < -RL_RANGE;
}

static void* rl_actor_of(u32 pid) {
    fopAc_ac_c* a = 0;
    if (pid == 0 || pid == fpcM_ERROR_PROCESS_ID_e || !fopAcM_SearchByID(pid, &a) || !a) return 0;
    return a;
}

// The host Link the swap and the bodies hang on (section 5.2 create 3):
// mpPlayerPtr[0], in MEM1, a PLAYER, and equal to mPlayerInfo[0] (they
// differ while Medli or Makar is controlled, d_a_player.cpp:13-24).
u32 rl_host_link(void) {
    u32 l = RL_PLAYER_PTR0;
    if (!rl_in_mem1(l) || AF(l, s16, AC_PROC_NAME) != PROC_PLAYER || RL_PLAYER_INFO0 != l) return 0;
    return l;
}

// The current stage equals an 8-byte NUL-padded name in the block.
static int rl_stage_is(u32 block_off) {
    int i;
    for (i = 0; i < WWREMOTE_STAGE_LEN; i++) {
        u8 c = RL_CUR_STAGE_NAME[i];
        if (c != RB_U8(block_off + i)) return 0;
        if (c == 0) break;
    }
    return 1;
}

// The current stage equals an 8-byte NUL-padded name in the state.
static int rl_stage_same(const u8* name) {
    int i;
    for (i = 0; i < WWREMOTE_STAGE_LEN; i++) {
        u8 c = RL_CUR_STAGE_NAME[i];
        if (c != name[i]) return 0;
        if (c == 0) break;
    }
    return 1;
}

// sqrt for speedF only: a bit-trick seed and three Newton steps (no
// libm; fdivs is hardware).
f32 rl_sqrt(f32 x) {
    union { f32 f; u32 u; } v;
    f32 y;
    if (!(x > 0.0f)) return 0.0f;
    v.f = x;
    v.u = 0x1FBD1DF5u + (v.u >> 1);
    y = v.f;
    y = 0.5f * (y + x / y);
    y = 0.5f * (y + x / y);
    y = 0.5f * (y + x / y);
    return y;
}

// The body of slot i if the keeper knows it as LIVE (parked or not).
static u32 rl_body_actor(remote_swap_state* st, int i) {
    rl_body* bd = &st->bodies[i];
    if (bd->state != WWREMOTE_BODY_LIVE || !bd->actor) return 0;
    return bd->actor;
}

// The slot of a body actor, or -1.
static int rl_body_slot_of(remote_swap_state* st, u32 a) {
    int i;
    if (!a) return -1;
    for (i = 0; i < MAX_REMOTE_LINKS; i++) {
        if (st->bodies[i].actor == a) return i;
    }
    return -1;
}

// --- Latch table and the m714 rewrite (sections 5.4, 5.5) -------------------
static rl_latch* rl_latch_find(remote_swap_state* st, u32 pid, int alloc) {
    rl_latch* free_e = 0;
    int i;
    for (i = 0; i < RL_LATCH_MAX; i++) {
        rl_latch* e = &st->latch[i];
        if (e->enemy_pid == pid) return e;
        if (!free_e && e->enemy_pid == 0) free_e = e;
    }
    if (!alloc || !free_e) return 0;
    free_e->enemy_pid = pid;
    free_e->target_pid = 0;
    free_e->force_pid = 0;
    free_e->hold = 0;
    free_e->force_frames = 0;
    free_e->is_mo2 = 0;
    return free_e;
}

// Before a body goes (delete, park entry, gone) or the table is reset:
// every tracked Moblin whose cached m714 points at it (or, for body 0, at
// any body) gets the host Link back, and latches/forces naming its pid
// fall back to the host. The deletion itself lands at the next
// fpcDt_Handler, before the next execute. Only procs with a known m714
// offset are written (rl_m714_off).
static u32 rl_m714_off(s16 proc) {
    if (proc == PROC_MO2) return MO2_M714;
    if (proc == PROC_BK) return BK_M714;
    if (proc == PROC_TN) return TN_M714;
    return 0;
}

static void rl_forget_body(remote_swap_state* st, u32 body, u32 body_pid) {
    u32 host = RL_PLAYER_PTR0;
    int i;
    for (i = 0; i < RL_LATCH_MAX; i++) {
        rl_latch* e = &st->latch[i];
        if (e->enemy_pid == 0) continue;
        if (rl_in_mem1(host)) {
            void* m = rl_actor_of(e->enemy_pid);
            u32 off = m ? rl_m714_off(AF(m, s16, AC_PROC_NAME)) : 0;
            if (off) {
                u32 p = AF(m, u32, off);
                if (body ? p == body : rl_body_slot_of(st, p) >= 0) {
                    AF(m, u32, off) = host;
                    rl_ctr(WWREMOTE_CTR_PARK_REWRITES);
                }
            }
        }
        if (body == 0 || e->target_pid == body_pid) {
            e->target_pid = 0;
            e->hold = 0;
        }
        if (body == 0 || e->force_pid == body_pid) {
            e->force_pid = 0;
            e->force_frames = 0;
        }
    }
}

static void rl_latch_reset(remote_swap_state* st) {
    u32* w = (u32*)st->latch;
    u32 n;
    rl_forget_body(st, 0, 0);
    for (n = 0; n < sizeof(st->latch) / 4; n++) w[n] = 0;
}

// --- Keeper: full-body bookkeeping (stage-4 plan D4-7) -------------------------
// The slots in mask get their full attempt back: a new stage, epoch or gen.
static void rl_full_reset(remote_swap_state* st, u8 mask) {
    int i;
    st->full_denied &= (u8)~mask;
    for (i = 0; i < MAX_REMOTE_LINKS; i++) {
        if (mask & (1u << i)) st->full_wait[i] = 0;
    }
}

// --- Keeper: intake -----------------------------------------------------------
static void rl_consume(remote_swap_state* st, u32 seq, u32 frame) {
    f32 dy = RB_F32(WWREMOTE_DY_MAX_OFF);
    st->flags = RB_U16(WWREMOTE_FLAGS_OFF);
    st->role = RB_U8(WWREMOTE_ROLE_OFF);
    st->margin_q8 = RB_U8(WWREMOTE_MARGIN_Q8_OFF);
    st->min_hold = RB_U8(WWREMOTE_MIN_HOLD_OFF);
    st->body_iframes = RB_U8(WWREMOTE_BODY_IFRAMES_OFF);
    // FULL_ALLOW is latched per consumed seq; issuing a full create clears
    // the slot's bit, so one seq starts at most one (stage-4 plan D4-3).
    st->full_allow = (st->role == WWREMOTE_ROLE_HOST && (st->flags & WWREMOTE_FLAG_FULL_BODIES))
                         ? RB_U8(WWREMOTE_FULL_ALLOW_OFF) : 0;
    st->flags2 = RB_U16(WWREMOTE_FLAGS2_OFF);
    st->dy_max = (rl_f32_bad(dy) || !(dy > 0.0f)) ? RL_DY_MAX_DEF : dy;
    st->last_seq = seq;
    st->stale_frames = 0;
    RB_U32(WWREMOTE_ACK_SEQ_OFF) = seq;
    RB_U32(WWREMOTE_ACK_FRAME_OFF) = frame;
}

// The WWRP block: magic, layout, a role that agrees with the enemy block
// when that one is live, then a consumed seq that is not stale.
static void rl_intake(remote_swap_state* st, u32 frame) {
    u32 eb = MAILBOX_ADDR + WWENEMY_OFF;
    u8 role = RB_U8(WWREMOTE_ROLE_OFF);
    u32 epoch, head, tail;
    int ok = RB_U32(WWREMOTE_MAGIC_OFF) == WWREMOTE_MAGIC && RB_U16(WWREMOTE_LAYOUT_OFF) == WWREMOTE_LAYOUT &&
             (role == WWREMOTE_ROLE_HOST || role == WWREMOTE_ROLE_CLIENT);
    if (ok && *(volatile u32*)(eb + WWENEMY_MAGIC_OFF) == WWENEMY_MAGIC &&
        *(volatile u8*)(eb + WWENEMY_ROLE_OFF) != role) ok = 0;
    if (!ok) {
        st->valid = 0;
        st->flags = 0;
        return;
    }
    epoch = RB_U32(WWREMOTE_EPOCH_OFF);
    if (epoch != st->epoch_seen) {
        // A new epoch starts over: the latches go (their Moblins back on the
        // host Link first), the create tries too.
        rl_latch_reset(st);
        st->epoch_seen = epoch;
        st->last_seq = 0;
        {
            int i;
            for (i = 0; i < MAX_REMOTE_LINKS; i++) st->bodies[i].tries = 0;
        }
        rl_full_reset(st, 0xFF);
    }
    head = RB_U32(WWREMOTE_SEQ_HEAD_OFF);
    tail = RB_U32(WWREMOTE_SEQ_TAIL_OFF);
    if (head != 0 && head == tail && head != st->last_seq) {
        rl_consume(st, head, frame);
    } else if (st->stale_frames <= WWREMOTE_STALE_FRAMES) {
        st->stale_frames++;
    }
    st->valid = st->last_seq != 0 && st->stale_frames <= WWREMOTE_STALE_FRAMES;
    if (!st->valid) st->flags = 0;
}

// --- Keeper: body lifecycle (section 5.6, host role) --------------------------
// Whether slot i keeps a body: the block valid with role host and BODIES,
// the slot active and its stage the current one (section 5.6's DYING
// conditions). A Medli/Makar window or a queued stage change only parks a
// LIVE body (its execute); they gate the create (rl_slot_may_create).
static int rl_slot_keeps_body(remote_swap_state* st, int i) {
    u32 off = RB_SLOT(i);
    if (!st->valid || st->role != WWREMOTE_ROLE_HOST || !(st->flags & WWREMOTE_FLAG_BODIES)) return 0;
    if (!RB_U8(off + WWREMOTE_SL_ACTIVE_OFF)) return 0;
    return rl_stage_is(off + WWREMOTE_SL_STAGE_OFF);
}

// EMPTY -> QUEUED also needs no stage change queued and the host gate of
// section 5.2 create step 3.
static int rl_slot_may_create(remote_swap_state* st, int i) {
    return rl_slot_keeps_body(st, i) && NEXT_STAGE_ENABLE == 0 && rl_host_link() != 0;
}

static void rl_body_create(remote_swap_state* st, int i, u8 kind, u8 note) {
    rl_body* bd = &st->bodies[i];
    u32 off = RB_SLOT(i);
    u32 link = rl_host_link();
    u8 gen = RB_U8(off + WWREMOTE_SL_GEN_OFF);
    cXyz pos;
    csXyz ang;
    u32 pid;
    pos.x = RB_F32(off + WWREMOTE_SL_POS_OFF + 0);
    pos.y = RB_F32(off + WWREMOTE_SL_POS_OFF + 4);
    pos.z = RB_F32(off + WWREMOTE_SL_POS_OFF + 8);
    if (rl_f32_bad(pos.x) || rl_f32_bad(pos.y) || rl_f32_bad(pos.z)) {
        pos.x = AF(link, f32, AC_CUR_POS + 0);
        pos.y = AF(link, f32, AC_CUR_POS + 4);
        pos.z = AF(link, f32, AC_CUR_POS + 8);
    }
    ang.x = 0;
    ang.y = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    ang.z = 0;
    // fopAcM_create only queues (wwapi.h): the body's create runs in the
    // next fpcCt_Handler, outside any execute. A full body is the second
    // profile (fullbody.c), same params.
    pid = fopAcM_create(kind == WWREMOTE_KIND_FULL ? WWREMOTE_FULL_PROC : PROC_WWREL,
                        WWREMOTE_BODY_PARAMS_TAG | ((u32)i << 8) | gen, &pos,
                        (int)RL_STAY_NO, &ang, 0, -1, 0);
    bd->kind = kind;
    if (kind == WWREMOTE_KIND_FULL) st->full_allow &= (u8)~(1u << i);
    bd->tries++;
    bd->gen = gen;
    bd->del_sent = 0;
    bd->parked = 1;
    bd->actor = 0;
    if (pid == fpcM_ERROR_PROCESS_ID_e) {
        bd->err = WWREMOTE_BERR_CREATE;
        bd->cooldown = RB_U16(WWREMOTE_CREATE_COOLDOWN_OFF);
        bd->pid = 0;
        if (kind == WWREMOTE_KIND_FULL) st->full_denied |= (u8)(1u << i);
        return;
    }
    bd->pid = pid;
    // A lite body that stands in for a refused full one keeps the reason
    // (BERR 10-15) visible; with FULL_BODIES clear note is always NONE.
    bd->err = note;
    bd->state = WWREMOTE_BODY_QUEUED;
}

// The full body's profile slot is usable (stage-4 plan D4-7): the keeper
// is alive, so module 0x58 is linked and OSLink applied the list's
// relocation.
static int rl_full_profile_ok(void) {
    u32 list = RL_PROFILE_LIST;
    return RL_DMC_FULL != 0 && RL_DMC_FULL == RL_DMC_REL && rl_in_mem1(list) &&
           AF(list, u32, 4 * WWREMOTE_FULL_PROC) == wwrel_body_profile();
}

// A FULL body is being created (QUEUED) in some slot.
static int rl_full_queued(remote_swap_state* st) {
    int k;
    for (k = 0; k < MAX_REMOTE_LINKS; k++) {
        if (st->bodies[k].state == WWREMOTE_BODY_QUEUED && st->bodies[k].kind == WWREMOTE_KIND_FULL) return 1;
    }
    return 0;
}

// The heap-gate reasons the keeper waits on (Go reopens by itself).
static int rl_hg_transient(u8 r) {
    return r == WWREMOTE_HG_NONE || r == WWREMOTE_HG_NO_SAMPLE || r == WWREMOTE_HG_INFLIGHT ||
           r == WWREMOTE_HG_STAGE_SETTLE || r == WWREMOTE_HG_QUEUED;
}

// EMPTY -> QUEUED, with the kind of stage-4 plan D4-7: FULL when
// FULL_BODIES is set, the slot's latched allow bit is set, its full
// attempt is unspent, no FULL body is QUEUED and the profile checks; a
// clear allow bit waits up to RL_FULL_WAIT frames on a transient
// HEAP_REASON (read live), then (or at once) LITE with HEAP_DENIED.
// FULL_BODIES clear: LITE, as Batch A.
static void rl_body_issue(remote_swap_state* st, int i) {
    u8 bit = (u8)(1u << i);
    u8 note = WWREMOTE_BERR_NONE;
    if ((st->flags & WWREMOTE_FLAG_FULL_BODIES) && st->role == WWREMOTE_ROLE_HOST && !(st->full_denied & bit)) {
        if (st->full_allow & bit) {
            st->full_wait[i] = 0;
            if (rl_full_queued(st)) return;
            if (rl_full_profile_ok()) {
                rl_body_create(st, i, WWREMOTE_KIND_FULL, WWREMOTE_BERR_NONE);
                return;
            }
            st->full_denied |= bit;
            note = WWREMOTE_BERR_PROFILE;
        } else if (rl_hg_transient(RB_U8(WWREMOTE_HEAP_REASON_OFF)) && st->full_wait[i] < RL_FULL_WAIT) {
            st->full_wait[i]++;
            return;
        } else {
            st->full_wait[i] = RL_FULL_WAIT;
            note = WWREMOTE_BERR_HEAP_DENIED;
        }
    }
    rl_body_create(st, i, WWREMOTE_KIND_LITE, note);
}

// LIVE -> DYING: the m714 rewrite first, then the delete (retried while
// the process is still being created).
static void rl_body_retire(remote_swap_state* st, int i) {
    rl_body* bd = &st->bodies[i];
    if (bd->actor) rl_forget_body(st, bd->actor, bd->pid);
    bd->parked = 1;
    bd->state = WWREMOTE_BODY_DYING;
    bd->del_sent = fopAcM_deleteByID(bd->pid) ? 1 : 0;
}

static void rl_body_empty(rl_body* bd, u8 err, u16 cooldown) {
    bd->actor = 0;
    bd->pid = 0;
    bd->state = WWREMOTE_BODY_EMPTY;
    bd->parked = 1;
    bd->del_sent = 0;
    bd->frames_live = 0;
    if (err) bd->err = err;
    bd->cooldown = cooldown;
}

static void rl_lifecycle(remote_swap_state* st, int i) {
    rl_body* bd = &st->bodies[i];
    u16 cool = RB_U16(WWREMOTE_CREATE_COOLDOWN_OFF);
    int keep = rl_slot_keeps_body(st, i);
    u8 gen = RB_U8(RB_SLOT(i) + WWREMOTE_SL_GEN_OFF);
    void* a;
    switch (bd->state) {
    case WWREMOTE_BODY_EMPTY:
        if (bd->cooldown) {
            bd->cooldown--;
        } else if (rl_slot_may_create(st, i)) {
            if (bd->tries >= RL_MAX_TRIES) bd->err = WWREMOTE_BERR_TRIES;
            else rl_body_issue(st, i);
        }
        break;
    case WWREMOTE_BODY_QUEUED:
        if (fpcM_IsCreating(bd->pid)) break;
        a = rl_actor_of(bd->pid);
        if (a && (bd->kind == WWREMOTE_KIND_FULL ?
                  (AF(a, u32, WWREMOTE_FULL_MAGIC_OFF) == WWREMOTE_BODY_MAGIC &&
                   AF(a, u8, WWREMOTE_FULL_SLOT_OFF) == (u8)i) :
                  (AF(a, u32, WWREMOTE_BODY_MAGIC_OFF) == WWREMOTE_BODY_MAGIC &&
                   AF(a, u8, WWREMOTE_BODY_SLOT_OFF) == (u8)i))) {
            bd->actor = (u32)a;
            bd->state = WWREMOTE_BODY_LIVE;
            bd->frames_live = 0;
        } else {
            // The body's own create may have said why (bd->err); else lost.
            // A full one spent the slot's full attempt: lite next.
            if (bd->kind == WWREMOTE_KIND_FULL) st->full_denied |= (u8)(1u << i);
            rl_body_empty(bd, bd->err ? 0 : WWREMOTE_BERR_LOST, cool);
        }
        break;
    case WWREMOTE_BODY_LIVE:
        a = rl_actor_of(bd->pid);
        if (!a || (u32)a != bd->actor) {
            // Gone under us (stage teardown); its delete did the rewrite.
            if (bd->actor) rl_forget_body(st, bd->actor, bd->pid);
            if (bd->kind == WWREMOTE_KIND_FULL && bd->frames_live < RL_FULL_EARLY) st->full_denied |= (u8)(1u << i);
            rl_body_empty(bd, WWREMOTE_BERR_GONE, cool);
        } else if (!keep || gen != bd->gen) {
            // A new owner (gen) gets its own create tries (and full attempt).
            if (gen != bd->gen) {
                bd->tries = 0;
                rl_full_reset(st, (u8)(1u << i));
            }
            rl_body_retire(st, i);
        } else if (bd->kind == WWREMOTE_KIND_FULL && !(st->flags & WWREMOTE_FLAG_FULL_BODIES)) {
            // FULL_BODIES cleared: retire the full body, lite next; no full
            // again in this stage (no upgrades, stage-4 plan D4-7).
            st->full_denied |= (u8)(1u << i);
            rl_body_retire(st, i);
        } else if (bd->frames_live != 0xFFFF) {
            bd->frames_live++;
        }
        break;
    case WWREMOTE_BODY_DYING:
        if (!rl_actor_of(bd->pid)) {
            rl_body_empty(bd, 0, cool);
        } else if (!bd->del_sent) {
            bd->del_sent = fopAcM_deleteByID(bd->pid) ? 1 : 0;
        }
        break;
    default:
        rl_body_empty(bd, 0, 0);
        break;
    }
}

// Retire every body (the keeper's delete; BODIES / block off is the
// per-slot `keep`).
static void rl_retire_all(remote_swap_state* st) {
    int i;
    for (i = 0; i < MAX_REMOTE_LINKS; i++) {
        rl_body* bd = &st->bodies[i];
        if (bd->state == WWREMOTE_BODY_LIVE) rl_body_retire(st, i);
        else if (bd->state == WWREMOTE_BODY_QUEUED || bd->state == WWREMOTE_BODY_DYING) fopAcM_deleteByID(bd->pid);
    }
}

// --- Keeper: client apply ("v2 in reverse", section 8.5) ----------------------
// The result word and the ack, the last two stores of a request.
static void rl_ap_publish(remote_swap_state* st, u8 result, u8 detail) {
    RB_U32(WWREMOTE_APPLY_RESULT_OFF) = ((u32)result << 24) | ((u32)detail << 16) |
                                        ((u32)st->ap_frames << 8) | (u32)st->ap_sets;
    RB_U32(WWREMOTE_APPLY_ACK_OFF) = st->ap_seq;
    st->ap_state = RL_AP_IDLE;
    st->ap_link = 0;
}

// The local mirrored Moblin with that setID among enemy_mirror's adopted
// slots (resolved this frame), or 0: no NULL-actor fallback (D9: a NULL At
// actor makes a guard impossible, d_cc_s.cpp:83-91).
static void* rl_mirrored_by_setid(enemy_mirror_state* mirror, u16 setid) {
    int i;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &mirror->slots[i];
        if (sl->adopted && sl->actor && AF(sl->actor, s16, AC_PROC_NAME) == PROC_MO2 &&
            AF(sl->actor, u16, AC_SETID) == setid) return (void*)sl->actor;
    }
    return 0;
}

// Register the sphere for this frame's Move: c = the client Link's
// position + rel_hit; StartCAt(c - rvec) then MoveCAt(c), so the At vector
// is rvec and becomes Link's TgRVec for getDamageVec (d_cc_d.cpp:381-394;
// d_a_player_main.cpp:5558-5583); then cCcS::Set.
static void rl_ap_register(remote_swap_state* st, u32 link) {
    cXyz c, s;
    c.x = AF(link, f32, AC_CUR_POS + 0) + st->ap_rel.x;
    c.y = AF(link, f32, AC_CUR_POS + 4) + st->ap_rel.y;
    c.z = AF(link, f32, AC_CUR_POS + 8) + st->ap_rel.z;
    s.x = c.x - st->ap_rvec.x;
    s.y = c.y - st->ap_rvec.y;
    s.z = c.z - st->ap_rvec.z;
    dCcD_GObjInf_ClrAtHit(RL_SPH);
    dCcD_Sph_StartCAt(RL_SPH, &s);
    dCcD_Sph_MoveCAt(RL_SPH, &c);
    cCcS_Set(RL_CCS, RL_SPH);
    st->ap_sets++;
}

static int rl_vec_bad(const cXyz* v) {
    return rl_f32_bad(v->x) || rl_f32_bad(v->y) || rl_f32_bad(v->z);
}

static void rl_ap_start(remote_swap_state* st, enemy_mirror_state* mirror, u32 seq) {
    u32 link = rl_host_link();
    u32 src[SRC_WORDS];
    u8* sb = (u8*)src;
    void* m;
    int i;
    st->ap_ack = seq;
    st->ap_seq = seq;
    st->ap_frames = 0;
    st->ap_sets = 0;
    if (!link) {
        rl_ap_publish(st, WWREMOTE_AR_LINK_ABSENT, WWREMOTE_AD_NONE);
        return;
    }
    m = rl_mirrored_by_setid(mirror, RB_U16(WWREMOTE_APPLY_OFF + WWREMOTE_AP_SETID_OFF));
    if (!m) {
        rl_ap_publish(st, WWREMOTE_AR_NO_MIRROR, WWREMOTE_AD_NONE);
        return;
    }
    st->ap_rel.x = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_REL_HIT_OFF + 0);
    st->ap_rel.y = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_REL_HIT_OFF + 4);
    st->ap_rel.z = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_REL_HIT_OFF + 8);
    st->ap_rvec.x = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_RVEC_OFF + 0);
    st->ap_rvec.y = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_RVEC_OFF + 4);
    st->ap_rvec.z = RB_F32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_RVEC_OFF + 8);
    if (rl_vec_bad(&st->ap_rel) || rl_vec_bad(&st->ap_rvec) || !enemy_mirror_sph_image(mirror, m)) {
        rl_ap_publish(st, WWREMOTE_AR_DROPPED, WWREMOTE_AD_NONE);
        return;
    }
    // The stts names the mirrored Moblin: not Link (cCcS::ChkNoHitAtTg
    // rejects the same actor, c_cc_s.cpp:100-111), not NULL (no guard).
    dCcD_Stts_ct(RL_STTS);
    dCcD_Stts_Init(RL_STTS, RL_STTS_WEIGHT, RL_STTS_P1, m);
    // The dCcD_SrcSph: the host's At values; At SPrm Set|VsPlayer, so the
    // sphere can never hit a mirrored Moblin; GObjAt SPrm 0.
    for (i = 0; i < SRC_WORDS; i++) src[i] = 0;
    AF(sb, u32, SRC_AT_TYPE) = RB_U32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_ATTYPE_OFF);
    AF(sb, u8, SRC_AT_ATP) = RB_U8(WWREMOTE_APPLY_OFF + WWREMOTE_AP_ATP_OFF);
    AF(sb, u32, SRC_AT_SPRM) = WWREMOTE_APPLY_AT_SPRM;
    AF(sb, u8, SRC_GAT_SE) = RB_U8(WWREMOTE_APPLY_OFF + WWREMOTE_AP_SE_OFF);
    AF(sb, u8, SRC_GAT_HITMARK) = RB_U8(WWREMOTE_APPLY_OFF + WWREMOTE_AP_HITMARK_OFF);
    AF(sb, u8, SRC_GAT_SPL) = RB_U8(WWREMOTE_APPLY_OFF + WWREMOTE_AP_SPL_OFF);
    AF(sb, f32, SRC_CENTER + 0) = AF(link, f32, AC_CUR_POS + 0) + st->ap_rel.x;
    AF(sb, f32, SRC_CENTER + 4) = AF(link, f32, AC_CUR_POS + 4) + st->ap_rel.y;
    AF(sb, f32, SRC_CENTER + 8) = AF(link, f32, AC_CUR_POS + 8) + st->ap_rel.z;
    AF(sb, f32, SRC_RADIUS) = RL_AT_RADIUS;
    if (AF(sb, u8, SRC_AT_ATP) == 0) AF(sb, u8, SRC_AT_ATP) = 1;
    dCcD_Sph_Set(RL_SPH, src);
    AF(RL_SPH, u32, CC_STTS) = (u32)RL_STTS;
    st->life_before = RL_LIFE;
    RB_U16(WWREMOTE_LIFE_BEFORE_OFF) = st->life_before;
    st->ap_link = link;
    rl_ap_register(st, link);
    st->ap_state = RL_AP_SET;
}

// Every keeper execute while a request is open; live == 0 aborts it
// (magic 0, APPLY cleared, the keeper's delete or _epilog).
static void rl_ap_tick(remote_swap_state* st, int live) {
    u32 link;
    if (st->ap_state == RL_AP_IDLE) return;
    if (!live) {
        rl_ap_publish(st, WWREMOTE_AR_DROPPED, WWREMOTE_AD_ABORTED);
        return;
    }
    rl_sat_inc(&st->ap_frames);
    link = rl_host_link();
    if (!link || link != st->ap_link) {
        rl_ap_publish(st, WWREMOTE_AR_LINK_ABSENT, WWREMOTE_AD_NONE);
        return;
    }
    if (st->ap_state == RL_AP_DAMAGE) {
        // +2 executes: changeDamageProc ran in Link's execute after the
        // cross; no DAMAGE mode and no life change means one of its early
        // outs (event, demo, no control, ship; d_a_player_main.cpp:5079-5093).
        u16 life = RL_LIFE;
        RB_U16(WWREMOTE_LIFE_AFTER_OFF) = life;
        if (!(AF(link, u32, LK_MODE_FLG) & LK_MODE_DAMAGE) && life == st->life_before)
            rl_ap_publish(st, WWREMOTE_AR_EARLY_OUT, WWREMOTE_AD_NONE);
        else
            rl_ap_publish(st, WWREMOTE_AR_APPLIED, WWREMOTE_AD_NONE);
        return;
    }
    // RL_AP_SET, +1 execute: the At record of the last Move (no Move clears
    // it while the sphere stays un-Set, c_cc_s.cpp:115-118).
    {
        u32 w = AF(RL_SPH, u32, CC_AT_RPRM);
        u32 obj = AF(RL_SPH, u32, CC_AT_HIT_OBJ);
        if ((w & 1) && obj == link + LK_CYL) {
            // Guarded: SetAtTgGObjInf marks both sides (d_cc_s.cpp:418,437);
            // our sphere's record survives un-Set, Link's Tg one may already
            // be cleared by its own execute.
            if ((AF(RL_SPH, u32, CC_GAT_RPRM) & 1) ||
                (AF(link, u32, LK_CYL + CC_GTG_RPRM) & CC_GTG_SHIELD_HIT)) {
                RB_U16(WWREMOTE_LIFE_AFTER_OFF) = RL_LIFE;
                rl_ap_publish(st, WWREMOTE_AR_GUARDED, WWREMOTE_AD_NONE);
            } else {
                st->ap_state = RL_AP_DAMAGE;
            }
        } else if (!(AF(link, u32, LK_CYL + CC_TG_SPRM) & 1)) {
            // Link's Tg is off: mDamageWaitTimer (setCollision,
            // d_a_player_main.cpp:9775-9781).
            rl_ap_publish(st, WWREMOTE_AR_NO_CROSS, WWREMOTE_AD_IFRAMES);
        } else if (st->ap_sets < RL_AP_MAX_SETS) {
            rl_ap_register(st, link);
        } else {
            rl_ap_publish(st, WWREMOTE_AR_NO_CROSS, WWREMOTE_AD_NONE);
        }
    }
}

static void rl_apply(remote_swap_state* st, enemy_mirror_state* mirror) {
    int on = st->valid && st->role == WWREMOTE_ROLE_CLIENT && (st->flags & WWREMOTE_FLAG_APPLY);
    rl_ap_tick(st, on);
    if (on && st->ap_state == RL_AP_IDLE) {
        u32 seq = RB_U32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_SEQ_OFF);
        if (seq != 0 && seq != st->ap_ack) rl_ap_start(st, mirror, seq);
    }
}

// --- Keeper: status -------------------------------------------------------------
static void rl_write_status(remote_swap_state* st) {
    int i, k;
    for (i = 0; i < WWREMOTE_MAX_SLOTS; i++) {
        u32 off = WWREMOTE_BODY_STATUS_OFF + i * WWREMOTE_BODY_STATUS_SIZE;
        if (i < MAX_REMOTE_LINKS) {
            rl_body* bd = &st->bodies[i];
            u8 latched = 0;
            for (k = 0; k < RL_LATCH_MAX; k++) {
                if (bd->pid && st->latch[k].enemy_pid && st->latch[k].target_pid == bd->pid) latched++;
            }
            RB_U32(off + WWREMOTE_BS_ACTOR_OFF) = bd->actor;
            RB_U32(off + WWREMOTE_BS_PID_OFF) = bd->pid;
            RB_U8(off + WWREMOTE_BS_STATE_OFF) =
                (bd->state == WWREMOTE_BODY_LIVE && bd->parked) ? WWREMOTE_BODY_PARKED : bd->state;
            RB_U8(off + WWREMOTE_BS_ERR_OFF) = bd->err;
            RB_U8(off + WWREMOTE_BS_HITS_OFF) = bd->hits;
            RB_U8(off + WWREMOTE_BS_LATCHED_BY_OFF) = latched;
            RB_U16(off + WWREMOTE_BS_FRAMES_LIVE_OFF) = bd->frames_live;
            RB_U8(off + WWREMOTE_BS_TRIES_OFF) = bd->tries;
            RB_U8(off + WWREMOTE_BS_KIND_OFF) = bd->kind;
        } else {
            for (k = 0; k < WWREMOTE_BODY_STATUS_SIZE; k += 4) RB_U32(off + k) = 0;
        }
    }
}

// --- Keeper: entry points ---------------------------------------------------------
void remote_keeper_init(remote_swap_state* st) {
    u32* w = (u32*)st;
    u32 n;
    int i;
    for (n = 0; n < sizeof(remote_swap_state) / 4; n++) w[n] = 0;
    for (i = 0; i < MAX_REMOTE_LINKS; i++) st->bodies[i].parked = 1;
    // An apply request left in the block from before this link is not ours.
    st->ap_ack = RB_U32(WWREMOTE_APPLY_OFF + WWREMOTE_AP_SEQ_OFF);
    st->dy_max = RL_DY_MAX_DEF;
    RL_WORD(WWREL_SWAP_STATE_OFF) = (u32)st;
}

void remote_keeper_frame(remote_swap_state* st, enemy_mirror_state* mirror, u32 frame) {
    int i;
    st->frame = frame;
    rl_intake(st, frame);

    // A new stage name starts the create tries over (section 5.6 "tries < 3
    // this stage"); the stage teardown deleted the bodies and the latched
    // Moblins.
    if (!rl_stage_same(st->stage)) {
        for (i = 0; i < WWREMOTE_STAGE_LEN; i++) st->stage[i] = RL_CUR_STAGE_NAME[i];
        for (i = 0; i < MAX_REMOTE_LINKS; i++) st->bodies[i].tries = 0;
        rl_full_reset(st, 0xFF);
    }

    // Reap latches whose enemy is gone (section 5.4).
    for (i = 0; i < RL_LATCH_MAX; i++) {
        rl_latch* e = &st->latch[i];
        if (e->enemy_pid && !rl_actor_of(e->enemy_pid)) e->enemy_pid = 0;
    }

    // Bodies (host). The lifecycle runs on the client too, so a block that
    // turns client while bodies exist retires them (want is 0 there).
    for (i = 0; i < MAX_REMOTE_LINKS; i++) rl_lifecycle(st, i);

    // Client apply.
    rl_apply(st, mirror);

    // The hook's fast-path inputs.
    {
        u8 mask = 0;
        for (i = 0; i < MAX_REMOTE_LINKS; i++) {
            rl_body* bd = &st->bodies[i];
            u8 sf = RB_U8(RB_SLOT(i) + WWREMOTE_SL_SFLAGS_OFF);
            if (bd->state == WWREMOTE_BODY_LIVE && bd->actor && !bd->parked &&
                (sf & WWREMOTE_SF_TARGETABLE) && !(sf & WWREMOTE_SF_NO_HIT)) mask |= (u8)(1u << i);
        }
        st->live_mask = mask;
        st->swap_ready = (st->valid && st->role == WWREMOTE_ROLE_HOST && (st->flags & WWREMOTE_FLAG_SWAP) &&
                          mask != 0 && NEXT_STAGE_ENABLE == 0) ? 1 : 0;
    }
    rl_write_status(st);
}

void remote_keeper_delete(remote_swap_state* st) {
    st->swap_ready = 0;
    st->live_mask = 0;
    rl_ap_tick(st, 0);
    rl_retire_all(st);
    rl_latch_reset(st);
    rl_write_status(st);
    if (rl_state() == st) RL_WORD(WWREL_SWAP_STATE_OFF) = 0;
}

// The keeper's delete already cleared the word and aborted its request;
// the keeper may be freed by now, so nothing here touches it.
void remote_unlink(void) {
    RL_WORD(WWREL_SWAP_STATE_OFF) = 0;
}

// --- Lite body: vtable stubs (section 5.2) ---------------------------------------
// Called through the copied vtable with `this` in r3 (C++ EABI); f32
// returns in f1, so plain C functions match.
static void* rl_vt_hand_mtx(void* self) {
    return ((remote_body*)self)->hand_mtx;
}

static f32 rl_vt_ground_y(void* self) {
    return AF(self, f32, AC_CUR_POS + 4);
}

static f32 rl_vt_zero(void* self) {
    (void)self;
    return 0.0f;
}

// checkPlayerGuard: the remote's streamed guard, behind FLAG_GUARD.
static BOOL rl_vt_guard(void* self) {
    remote_body* b = (remote_body*)self;
    remote_swap_state* st = rl_state();
    if (!st || !(st->flags & WWREMOTE_FLAG_GUARD) || b->slot >= MAX_REMOTE_LINKS) return 0;
    return (RB_U8(RB_SLOT(b->slot) + WWREMOTE_SL_SFLAGS_OFF) & WWREMOTE_SF_GUARD) ? 1 : 0;
}

// --- Lite body: create -------------------------------------------------------------
int remote_is_body(void* actor) {
    return (AF(actor, u32, AC_PARAMS) & WWREMOTE_BODY_PARAMS_MASK) == WWREMOTE_BODY_PARAMS_TAG;
}

static void rl_body_hand(remote_body* b) {
    int r, c;
    for (r = 0; r < 3; r++) {
        for (c = 0; c < 4; c++) b->hand_mtx[r][c] = (r == c) ? 1.0f : 0.0f;
    }
    b->hand_mtx[0][3] = AF(b, f32, AC_CUR_POS + 0);
    b->hand_mtx[1][3] = AF(b, f32, AC_CUR_POS + 4);
    b->hand_mtx[2][3] = AF(b, f32, AC_CUR_POS + 8);
}

// Runs in fpcCt_Handler, outside any execute. cPhs_ERROR_e (the game
// deletes the process) with the reason in the keeper's body record unless
// every check of section 5.2 holds.
int remote_body_create(void* actor) {
    remote_body* b = (remote_body*)actor;
    remote_swap_state* st = rl_state();
    u32 params = AF(actor, u32, AC_PARAMS);
    u32 slot = (params >> 8) & 0xFF;
    u32 link = rl_host_link();
    u8 err = WWREMOTE_BERR_NONE;
    int i;

    if (slot >= MAX_REMOTE_LINKS) err = WWREMOTE_BERR_SLOT;
    else if (!st || !rl_in_mem1((u32)st) || !st->valid || st->role != WWREMOTE_ROLE_HOST) err = WWREMOTE_BERR_BLOCK;
    else if (AF(RL_VT_DAPY_PY, u32, RL_VT_GUARD) != RL_GUARD_PY_FN ||
             AF(RL_VT_DAPY_LK, u32, RL_VT_GUARD) != RL_GUARD_LK_FN) {
        err = WWREMOTE_BERR_VTABLE;
        rl_ctr(WWREMOTE_CTR_VT_REFUSALS);
    } else if (!link) err = WWREMOTE_BERR_HOST_LINK;
    else if (AF(link, u32, LK_CYL + CC_VPTR) != RL_VT_DCCD_CYL) err = WWREMOTE_BERR_CYL;
    if (err) {
        if (st && rl_in_mem1((u32)st) && slot < MAX_REMOTE_LINKS) st->bodies[slot].err = err;
        return cPhs_ERROR_e;
    }

    // 1 the vtable: __vt__9daPy_py_c's defaults, the pure slots and
    // checkPlayerGuard stubbed (R_PPC_ADDR16 self-relocations against .text).
    {
        const u32* src = (const u32*)RL_VT_DAPY_PY;
        for (i = 0; i < WWREMOTE_BODY_VT_SIZE / 4; i++) b->vt[i] = src[i];
        b->vt[RL_VT_LEFT_HAND / 4] = (u32)rl_vt_hand_mtx;
        b->vt[RL_VT_RIGHT_HAND / 4] = (u32)rl_vt_hand_mtx;
        b->vt[RL_VT_GROUND_Y / 4] = (u32)rl_vt_ground_y;
        b->vt[RL_VT_GUARD / 4] = (u32)rl_vt_guard;
        b->vt[RL_VT_ANM_RATE / 4] = (u32)rl_vt_zero;
        b->vt[RL_VT_ANM_FRAME / 4] = (u32)rl_vt_zero;
        b->vptr = (u32)b->vt;
    }
    // 2 the Tg cylinder: Link's mCyl image (built in the DOL, so its
    // vptrs need no remap, Step -1), then neutralised: no At, Tg
    // Set|IsPlayer only, no Co (a network-placed body never pushes), no
    // callbacks, no shield bits, and the shield-angle pointer at our own
    // word (never Link's: ChkShieldFrontRange dereferences it).
    {
        const u32* src = (const u32*)(link + LK_CYL);
        u32* dst = (u32*)b->cyl;
        for (i = 0; i < WWREMOTE_BODY_CYL_SIZE / 4; i++) dst[i] = src[i];
        AF(b->cyl, u32, CC_AT_SPRM) = 0;
        AF(b->cyl, u32, CC_GAT_CALLBACK) = 0;
        AF(b->cyl, u32, CC_TG_SPRM) = CC_TG_SET_ISPLAYER;
        AF(b->cyl, u32, CC_CO_SPRM) = 0;
        AF(b->cyl, u32, CC_GTG) &= ~(u32)CC_GTG_GUARD_BITS;
        AF(b->cyl, u32, CC_GTG_CALLBACK) = 0;
        AF(b->cyl, u32, CC_GTG_SHIELD_PTR) = (u32)&b->shield_angle;
        AF(b->cyl, u32, CC_GCO_CALLBACK) = 0;
        dCcD_GObjInf_ClrTgHit(b->cyl);
        dCcD_GObjInf_ClrAtHit(b->cyl);
    }
    // 3 its own stts: Link's Init arguments with the body as the actor
    // (d_a_player_main.cpp:12184-12189).
    dCcD_Stts_ct(b->stts);
    dCcD_Stts_Init(b->stts, RL_STTS_WEIGHT, RL_STTS_P1, b);
    AF(b->cyl, u32, CC_STTS) = (u32)b->stts;
    // 4 a stage actor: the stage teardown deletes it, while the root-layer
    // keeper pins the module (f_op_actor_mng.cpp:34-39).
    fopAcM_setStageLayer(b);
    // 5 never a lock-on target (group stays ACTOR from the profile).
    AF(b, u32, AC_ATTN_FLAGS) = 0;
    b->grab_wear = 0.0f;
    b->cut_type = 0;
    b->cut_count = 0;
    b->damage_wait = 0;
    b->shield_angle = 0;
    b->iframes_left = 0;
    b->slot = (u8)slot;
    b->gen = (u8)(params & 0xFF);
    b->state = WWREMOTE_BODY_PARKED;
    b->last_pose_seq = 0xFFFFFFFFu;
    b->pose_seq_frame = RL_WORD(WWREL_FRAME_OFF);
    b->park_since = RL_WORD(WWREL_FRAME_OFF);
    rl_body_hand(b);
    b->magic = WWREMOTE_BODY_MAGIC;
    // A lite body standing in for a refused full one keeps that reason
    // (WWREMOTE_BERR_HEAP_DENIED.., only ever set with FULL_BODIES).
    if (st->bodies[slot].err < WWREMOTE_BERR_HEAP_DENIED) st->bodies[slot].err = WWREMOTE_BERR_NONE;
    return cPhs_COMPLEATE_e;
}

// The game deletes the body (the keeper's delete or the stage teardown):
// the Moblins it was cached in go back to the host Link, and the keeper's
// record forgets the pointer before any later execute.
int remote_body_delete(void* actor) {
    remote_body* b = (remote_body*)actor;
    remote_swap_state* st = rl_state();
    if (b->magic != WWREMOTE_BODY_MAGIC) return 1;
    if (st && rl_in_mem1((u32)st) && b->slot < MAX_REMOTE_LINKS) {
        rl_body* bd = &st->bodies[b->slot];
        if (bd->actor == (u32)b) {
            rl_forget_body(st, (u32)b, bd->pid);
            bd->actor = 0;
            bd->parked = 1;
            st->live_mask &= (u8)~(1u << b->slot);
            if (!st->live_mask) st->swap_ready = 0;
        }
    }
    b->magic = 0;
    return 1;
}

// The full body's delete (fullbody.c): the same as the lite one above for
// the keeper's record.
void rl_body_gone(remote_swap_state* st, void* a, u8 slot) {
    if (st && rl_in_mem1((u32)st) && slot < MAX_REMOTE_LINKS) {
        rl_body* bd = &st->bodies[slot];
        if (bd->actor == (u32)a) {
            rl_forget_body(st, (u32)a, bd->pid);
            bd->actor = 0;
            bd->parked = 1;
            st->live_mask &= (u8)~(1u << slot);
            if (!st->live_mask) st->swap_ready = 0;
        }
    }
}

// The full body's park entry (fullbody.c): the lite execute's own, below.
void rl_body_park_enter(remote_swap_state* st, void* a, rl_btail* t, u32 frame) {
    t->state = WWREMOTE_BODY_PARKED;
    t->park_since = frame;
    if (st && rl_in_mem1((u32)st) && t->slot < MAX_REMOTE_LINKS && st->bodies[t->slot].actor == (u32)a) {
        st->bodies[t->slot].parked = 1;
        st->live_mask &= (u8)~(1u << t->slot);
        if (!st->live_mask) st->swap_ready = 0;
        rl_forget_body(st, (u32)a, st->bodies[t->slot].pid);
    }
}

// --- Lite body: execute (section 5.2) ----------------------------------------------
// The hit read (section 7.1, FLAG_DETECT): the Tg record from the previous
// Move (it survives until the next Move's ClrTgHitInf, c_cc_s.cpp:74-85),
// read before this frame's pose and Set. The attacker side (type, atp, Se,
// HitMark, Spl) comes from the At object that crossed, not the body's own
// Tg. The ring entry's seq and the head are written last. Both kinds: a
// is the body, t its tail, cyl its Tg cylinder (lite b->cyl, full mCyl).
void rl_body_hits(remote_swap_state* st, void* a, rl_btail* t, u8* cyl, u32 frame) {
    void* att;
    u32 obj, n, e;
    u8 hf = 0;
    u16 setid = 0;
    s16 proc = 0;
    u32 pid = 0;
    if (!(AF(cyl, u32, CC_TG_RPRM) & 1)) return;
    att = dCcD_GAtTgCo_GetAc(cyl + CC_GTG);
    obj = AF(cyl, u32, CC_TG_HIT_OBJ);
    if (att && rl_in_mem1((u32)att)) {
        pid = AF(att, u32, AC_PID);
        proc = AF(att, s16, AC_PROC_NAME);
        setid = AF(att, u16, AC_SETID);
    }
    if (AF(cyl, u32, CC_GTG_RPRM) & CC_GTG_SHIELD_HIT) hf |= WWREMOTE_HF_SHIELD;
    if (!att || proc != PROC_MO2) hf |= WWREMOTE_HF_NOT_MO2;
    if (!att || setid >= WWREMOTE_KEYED_SETID_MAX) hf |= WWREMOTE_HF_NOT_KEYED;
    n = RB_U32(WWREMOTE_HIT_RING_HEAD_OFF) + 1;
    e = WWREMOTE_HIT_RING_OFF + ((n - 1) & (WWREMOTE_HIT_RING_LEN - 1)) * WWREMOTE_HIT_SIZE;
    RB_U8(e + WWREMOTE_HE_SLOT_OFF) = t->slot;
    RB_U8(e + WWREMOTE_HE_HFLAGS_OFF) = hf;
    RB_U32(e + WWREMOTE_HE_PID_OFF) = pid;
    RB_U16(e + WWREMOTE_HE_SETID_OFF) = setid;
    RB_S16(e + WWREMOTE_HE_PROC_OFF) = proc;
    if (rl_in_mem1(obj)) {
        RB_U32(e + WWREMOTE_HE_ATTYPE_OFF) = AF(obj, u32, CC_AT_TYPE);
        RB_U8(e + WWREMOTE_HE_ATP_OFF) = AF(obj, u8, CC_AT_ATP);
        RB_U8(e + WWREMOTE_HE_SE_OFF) = AF(obj, u8, CC_GAT_SE);
        RB_U8(e + WWREMOTE_HE_HITMARK_OFF) = AF(obj, u8, CC_GAT_HITMARK);
        RB_U8(e + WWREMOTE_HE_SPL_OFF) = AF(obj, u8, CC_GAT_SPL);
    } else {
        RB_U32(e + WWREMOTE_HE_ATTYPE_OFF) = 0;
        RB_U32(e + WWREMOTE_HE_ATP_OFF) = 0;     // atp, se, hitmark, spl
    }
    RB_F32(e + WWREMOTE_HE_REL_HIT_OFF + 0) = AF(cyl, f32, CC_GTG_HIT_POS + 0) - AF(a, f32, AC_CUR_POS + 0);
    RB_F32(e + WWREMOTE_HE_REL_HIT_OFF + 4) = AF(cyl, f32, CC_GTG_HIT_POS + 4) - AF(a, f32, AC_CUR_POS + 4);
    RB_F32(e + WWREMOTE_HE_REL_HIT_OFF + 8) = AF(cyl, f32, CC_GTG_HIT_POS + 8) - AF(a, f32, AC_CUR_POS + 8);
    RB_F32(e + WWREMOTE_HE_RVEC_OFF + 0) = AF(cyl, f32, CC_GTG_RVEC + 0);
    RB_F32(e + WWREMOTE_HE_RVEC_OFF + 4) = AF(cyl, f32, CC_GTG_RVEC + 4);
    RB_F32(e + WWREMOTE_HE_RVEC_OFF + 8) = AF(cyl, f32, CC_GTG_RVEC + 8);
    RB_U32(e + WWREMOTE_HE_FRAME_OFF) = frame;
    RB_U16(e + WWREMOTE_HE_SEQ_OFF) = (u16)n;
    RB_U32(WWREMOTE_HIT_RING_HEAD_OFF) = n;
    // A body that skips its Set is not in Move's ClrTgHitInf walk: clear
    // the record ourselves. The stamp dedupes wepon and wepon2 (separate
    // At spheres of one thrust).
    dCcD_GObjInf_ClrTgHit(cyl);
    if (st->flags & WWREMOTE_FLAG_IFRAME_STAMP) t->iframes_left = st->body_iframes;
    if (t->slot < MAX_REMOTE_LINKS) rl_sat_inc(&st->bodies[t->slot].hits);
}

// Why the body parks this frame (0 = it may stand), section 5.2 step 1.
// Both kinds: a is the body, t its tail.
int rl_body_should_park(remote_swap_state* st, void* a, rl_btail* t, u32 frame) {
    u32 off;
    u8 ps, sf;
    if (!st || !rl_in_mem1((u32)st) || !st->valid || st->role != WWREMOTE_ROLE_HOST ||
        !(st->flags & WWREMOTE_FLAG_BODIES)) return 1;
    if (t->slot >= MAX_REMOTE_LINKS || st->bodies[t->slot].actor != (u32)a ||
        st->bodies[t->slot].state != WWREMOTE_BODY_LIVE) return 1;
    off = RB_SLOT(t->slot);
    if (!RB_U8(off + WWREMOTE_SL_ACTIVE_OFF) || RB_U8(off + WWREMOTE_SL_GEN_OFF) != t->gen) return 1;
    if (NEXT_STAGE_ENABLE != 0 || !rl_stage_is(off + WWREMOTE_SL_STAGE_OFF)) return 1;
    if (RB_S8(off + WWREMOTE_SL_ROOM_OFF) != RL_STAY_NO) return 1;
    sf = RB_U8(off + WWREMOTE_SL_SFLAGS_OFF);
    if (sf & (WWREMOTE_SF_DEAD | WWREMOTE_SF_NO_HIT)) return 1;
    // The pose feed: the slot's buffer ready and its seq moving.
    ps = RB_U8(off + WWREMOTE_SL_POSE_SLOT_OFF);
    if (ps >= MAX_REMOTE_LINKS) return 1;
    if (*(volatile u8*)(MAILBOX_ADDR + __builtin_offsetof(Mailbox, pose_buf_states) + ps) != 1) return 1;
    {
        u32 seq = *(volatile u8*)(MAILBOX_ADDR + __builtin_offsetof(Mailbox, pose_seqs) + ps);
        if (seq != t->last_pose_seq) {
            t->last_pose_seq = seq;
            t->pose_seq_frame = frame;
        } else if (frame - t->pose_seq_frame > WWREMOTE_POSE_STALE_FRAMES) {
            return 1;
        }
    }
    return rl_host_link() == 0;
}

static f32 rl_slot_f32(u32 off, f32 def, f32 lo, f32 hi) {
    f32 v = RB_F32(off);
    if (rl_f32_bad(v) || v < lo || v > hi) return def;
    return v;
}

int remote_body_execute(void* actor) {
    remote_body* b = (remote_body*)actor;
    remote_swap_state* st = rl_state();
    u32 frame = RL_WORD(WWREL_FRAME_OFF);
    u32 off, link;
    u8 sf;
    int park, skip_set;
    cXyz p;
    f32 dx, dz;
    int i;

    if (b->magic != WWREMOTE_BODY_MAGIC) return 1;
    park = rl_body_should_park(st, b, (rl_btail*)&b->magic, frame);

    // 2 the hit read, before anything moves (the record is last Move's).
    if (st && rl_in_mem1((u32)st)) {
        if (st->flags & WWREMOTE_FLAG_DETECT) rl_body_hits(st, b, (rl_btail*)&b->magic, b->cyl, frame);
        else if (AF(b->cyl, u32, CC_TG_RPRM) & 1) dCcD_GObjInf_ClrTgHit(b->cyl);
    }

    if (park) {
        // Parked: no Set, out of the latch, standing on the host Link (its
        // pos and eyePos), so a cached pointer never leads anywhere odd.
        link = RL_PLAYER_PTR0;
        if (b->state != WWREMOTE_BODY_PARKED) {
            b->state = WWREMOTE_BODY_PARKED;
            b->park_since = frame;
            if (st && rl_in_mem1((u32)st) && b->slot < MAX_REMOTE_LINKS && st->bodies[b->slot].actor == (u32)b) {
                st->bodies[b->slot].parked = 1;
                st->live_mask &= (u8)~(1u << b->slot);
                if (!st->live_mask) st->swap_ready = 0;
                rl_forget_body(st, (u32)b, st->bodies[b->slot].pid);
            }
        }
        if (rl_in_mem1(link)) {
            for (i = 0; i < 3; i++) {
                AF(b, f32, AC_CUR_POS + 4 * i) = AF(link, f32, AC_CUR_POS + 4 * i);
                AF(b, f32, AC_EYE_POS + 4 * i) = AF(link, f32, AC_EYE_POS + 4 * i);
            }
        }
        AF(b, f32, AC_SPEEDF) = 0.0f;
        rl_body_hand(b);
        return 1;
    }
    if (b->state != WWREMOTE_BODY_LIVE) {
        b->state = WWREMOTE_BODY_LIVE;
        b->park_since = 0;
    }
    st->bodies[b->slot].parked = 0;
    off = RB_SLOT(b->slot);
    sf = RB_U8(off + WWREMOTE_SL_SFLAGS_OFF);

    // 3 pose: old = current, then the slot's (smoothed) position, angle and
    // room; speedF from the XZ step; eyePos = pos + eye_dy.
    for (i = 0; i < 5; i++) AF(b, u32, AC_OLD + 4 * i) = AF(b, u32, AC_CURRENT + 4 * i);
    p.x = RB_F32(off + WWREMOTE_SL_POS_OFF + 0);
    p.y = RB_F32(off + WWREMOTE_SL_POS_OFF + 4);
    p.z = RB_F32(off + WWREMOTE_SL_POS_OFF + 8);
    if (!rl_vec_bad(&p)) {
        dx = p.x - AF(b, f32, AC_CUR_POS + 0);
        dz = p.z - AF(b, f32, AC_CUR_POS + 8);
        AF(b, f32, AC_CUR_POS + 0) = p.x;
        AF(b, f32, AC_CUR_POS + 4) = p.y;
        AF(b, f32, AC_CUR_POS + 8) = p.z;
        AF(b, f32, AC_SPEEDF) = rl_sqrt(dx * dx + dz * dz);
    }
    AF(b, s16, AC_CUR_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    AF(b, s16, AC_SHAPE_ANGLE_Y) = RB_S16(off + WWREMOTE_SL_ANGLE_OFF);
    AF(b, s8, AC_CUR_ROOM) = RB_S8(off + WWREMOTE_SL_ROOM_OFF);
    {
        f32 eye = rl_slot_f32(off + WWREMOTE_SL_EYE_DY_OFF, RL_EYE_DY_DEF, -RL_CYL_MAX, RL_CYL_MAX);
        AF(b, f32, AC_EYE_POS + 0) = AF(b, f32, AC_CUR_POS + 0);
        AF(b, f32, AC_EYE_POS + 4) = AF(b, f32, AC_CUR_POS + 4) + eye;
        AF(b, f32, AC_EYE_POS + 8) = AF(b, f32, AC_CUR_POS + 8);
    }
    rl_body_hand(b);

    // 4 combat state the Moblin reads (d_cc_uty.cpp:16-34,88-131).
    b->cut_type = RB_U8(off + WWREMOTE_SL_CUT_TYPE_OFF);
    b->cut_count = RB_U8(off + WWREMOTE_SL_CUT_COUNT_OFF);
    b->shield_angle = RB_S16(off + WWREMOTE_SL_SHIELD_ANGLE_OFF);
    b->damage_wait = RB_S16(off + WWREMOTE_SL_IFRAMES_OFF);

    // 5 i-frames: our own stamp, or the client's mDamageWaitTimer (the
    // Tg-off mechanism of setCollision, d_a_player_main.cpp:9775-9781).
    skip_set = 0;
    if (b->iframes_left) {
        b->iframes_left--;
        skip_set = 1;
    }
    if (b->damage_wait > 0) skip_set = 1;

    // 7 guard (before the Set: it applies to this frame's Move).
    if ((st->flags & WWREMOTE_FLAG_GUARD) && (sf & WWREMOTE_SF_GUARD))
        AF(b->cyl, u32, CC_GTG) |= CC_GTG_GUARD_BITS;
    else
        AF(b->cyl, u32, CC_GTG) &= ~(u32)CC_GTG_GUARD_BITS;

    // 6 Set: the cylinder at the position, Go's radius and height; no
    // SetMass (Co is off).
    if ((st->flags & WWREMOTE_FLAG_HITTABLE) && !skip_set) {
        u8* shape = b->cyl + CC_SHAPE;
        cM3dGCyl_SetC(shape, (const cXyz*)((u8*)b + AC_CUR_POS));
        cM3dGCyl_SetH(shape, rl_slot_f32(off + WWREMOTE_SL_CYL_H_OFF, RL_CYL_H_DEF, 1.0f, RL_CYL_MAX));
        cM3dGCyl_SetR(shape, rl_slot_f32(off + WWREMOTE_SL_CYL_R_OFF, RL_CYL_R_DEF, 1.0f, RL_CYL_MAX));
        cCcS_Set(RL_CCS, b->cyl);
    }
    return 1;
}

// --- The swap (sections 5.3, 5.4) ------------------------------------------------------
// The PROC_POLICY flags for a proc (0 = unlisted), read from the block.
static u8 rl_policy(s16 proc) {
    u32 n = RB_U8(WWREMOTE_POLICY_COUNT_OFF);
    u32 i;
    if (n > WWREMOTE_PROC_POLICY_MAX) n = WWREMOTE_PROC_POLICY_MAX;
    for (i = 0; i < n; i++) {
        u32 e = WWREMOTE_POLICY_OFF + i * WWREMOTE_POLICY_SIZE;
        if (RB_S16(e + WWREMOTE_PP_PROC_OFF) == proc) return RB_U8(e + WWREMOTE_PP_FLAGS_OFF);
    }
    return 0;
}

// The current stage is on Go's FF denylist.
static int rl_ff_denied(void) {
    u32 n = RB_U8(WWREMOTE_FF_COUNT_OFF);
    u32 i;
    if (n > WWREMOTE_FF_DENY_MAX) n = WWREMOTE_FF_DENY_MAX;
    for (i = 0; i < n; i++) {
        u32 e = WWREMOTE_FF_DENY_OFF + i * WWREMOTE_STAGE_LEN;
        if (RB_U8(e) != 0 && rl_stage_is(e)) return 1;
    }
    return 0;
}

// mo2's jail and demo gate (section 5.3 step 4): 1 = leave this execute on
// the host Link. Jail: rouya_mode is 1 only when none of isCollect(0,
// 0..3) is set (d_a_mo2.cpp:4134-4143; Step -1: a direct byte read). The
// fields cover e3_demo's player writes, the orderPotentialEvent partners
// (:3302, :3409) and setNextStage("majroom") (:3466-3471).
static int rl_mo2_gated(remote_swap_state* st, void* a) {
    u8 mode;
    if (!(st->flags & WWREMOTE_FLAG_ROUYA_GATE_OFF) && (RL_COLLECT & WWREMOTE_COLLECT_JAIL_MASK) == 0) return 1;
    if (AF(a, s8, MO2_2A1D) || AF(a, s16, MO2_2A48) || AF(a, u8, MO2_2A4A) || AF(a, u8, MO2_2A4B) ||
        AF(a, s16, MO2_2A4C)) return 1;
    if (AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_E3_DEMO) return 1;
    mode = AF(a, u8, MO2_MODE);
    return mode == MO2_MODE_CARRIED || mode == MO2_MODE_STATUE;
}

// The other procs' demo/event gates (enemy audit): 1 = leave this execute
// on the host Link, because it would order an event or move the player
// with the swapped getPlayer(0) as partner.
static int rl_proc_gated(s16 proc, void* a) {
    switch (proc) {
    case PROC_BK:
        return AF(a, u8, BK_TYPE) == BK_TYPE_DEMO || AF(a, s8, BK_1234) != 0;
    case PROC_TN:
        return AF(a, s8, TN_14F8) != 0;
    case PROC_CC:
        return AF(a, u8, CC_BEHAVIOR) == 2 || AF(a, u8, CC_ACTION) == 5 || AF(a, u8, CC_304) != 0;
    case PROC_OSHIP:
        return AF(a, s32, OSHIP_PROC) == 3 || AF(a, u8, OSHIP_MODEL) != 0xFF;
    }
    return 0;
}

// The actor that owns the collider at obj (obj+0x44 = stts, stts+0x0C =
// actor), or 0.
static u32 rl_obj_actor(u32 obj) {
    u32 stts;
    if (!rl_in_mem1(obj)) return 0;
    stts = AF(obj, u32, CC_STTS);
    if (!rl_in_mem1(stts)) return 0;
    return AF(stts, u32, CC_STTS_ACTOR);
}

// A spear sphere mid-thrust: At Set with the thrust's type (Step -1).
static int rl_spear_thrust(void* a, u32 sph) {
    return (AF(a, u32, sph + CC_AT_SPRM) & 1) && (AF(a, u32, sph + CC_AT_TYPE) & AT_TYPE_UNK800);
}

typedef struct {
    u32 actor[1 + MAX_REMOTE_LINKS];   // [0] host, then candidate bodies
    u32 pid[1 + MAX_REMOTE_LINKS];     // [0] 0
    f32 d2[1 + MAX_REMOTE_LINKS];
    int n;
} rl_cands;

static int rl_cand_index(const rl_cands* c, u32 actor_or_pid, int by_pid) {
    int k;
    for (k = 0; k < c->n; k++) {
        if ((by_pid ? c->pid[k] : c->actor[k]) == actor_or_pid) return k;
    }
    return -1;
}

// Section 5.4: the host Link and every LIVE unparked TARGETABLE body in
// the enemy's room within dy_max; d2 on the XZ plane.
static void rl_candidates(remote_swap_state* st, void* a, u32 host, rl_cands* c) {
    f32 ax = AF(a, f32, AC_CUR_POS + 0);
    f32 ay = AF(a, f32, AC_CUR_POS + 4);
    f32 az = AF(a, f32, AC_CUR_POS + 8);
    s8 room = AF(a, s8, AC_CUR_ROOM);
    int i;
    c->n = 0;
    for (i = -1; i < MAX_REMOTE_LINKS; i++) {
        u32 t;
        f32 dx, dy, dz;
        if (i < 0) {
            t = host;
        } else {
            if (!(st->live_mask & (1u << i))) continue;
            t = rl_body_actor(st, i);
            if (!t || AF(t, s8, AC_CUR_ROOM) != room) continue;
        }
        dx = AF(t, f32, AC_CUR_POS + 0) - ax;
        dy = AF(t, f32, AC_CUR_POS + 4) - ay;
        dz = AF(t, f32, AC_CUR_POS + 8) - az;
        if (i >= 0 && (dy > st->dy_max || dy < -st->dy_max)) continue;
        c->actor[c->n] = t;
        c->pid[c->n] = i < 0 ? 0 : st->bodies[i].pid;
        c->d2[c->n] = dx * dx + dz * dz;
        c->n++;
    }
}

// The attacker-wins target (FLAG_ATTACKER) as a candidate index, or -1:
// ATTRIB's force, the body the spear crossed (so fight's
// GetName(hitActor)==PLAYER && getPlayer(0)->checkPlayerGuard(),
// d_a_mo2.cpp:1905-1919, asks the body that was hit), then the actor
// behind a Tg hit on the defence sphere, head sphere or Tg cylinder (the
// order damage_check reads them, :3036, :3072).
static int rl_attacker(rl_latch* e, void* a, const rl_cands* c, int is_mo2) {
    static const u16 tg[3] = { MO2_DEF_SPH, MO2_HEAD_SPH, MO2_TG_CYL };
    int k, j;
    if (e->force_frames) {
        e->force_frames--;
        k = rl_cand_index(c, e->force_pid, 1);
        if (e->force_pid && k >= 0) return k;
    }
    if (!is_mo2) return -1;
    for (j = 0; j < 2; j++) {
        u32 sph = j ? MO2_WEAPON2_SPH : MO2_WEAPON_SPH;
        if (rl_spear_thrust(a, sph) && (AF(a, u32, sph + CC_AT_RPRM) & 1)) {
            k = rl_cand_index(c, rl_obj_actor(AF(a, u32, sph + CC_AT_HIT_OBJ)), 0);
            if (k > 0) return k;
        }
    }
    for (j = 0; j < 3; j++) {
        if (AF(a, u32, tg[j] + CC_TG_RPRM) & 1) {
            k = rl_cand_index(c, rl_obj_actor(AF(a, u32, tg[j] + CC_TG_HIT_OBJ)), 0);
            if (k >= 0) return k;
        }
    }
    return -1;
}

// The Link this enemy's execute runs against (an actor pointer; the host
// means no swap).
static u32 rl_target(remote_swap_state* st, void* a, u8 pf, u32 host, int is_mo2) {
    rl_latch* e = rl_latch_find(st, AF(a, u32, AC_PID), 1);
    rl_cands c;
    int best, cur, k, t;
    // A full table means no swap: an untracked enemy's m714 could not be
    // rewritten when a body goes (section 5.5).
    if (!e) return host;
    e->is_mo2 = (u8)is_mo2;
    rl_candidates(st, a, host, &c);
    best = 0;
    for (k = 1; k < c.n; k++) {
        if (c.d2[k] < c.d2[best]) best = k;
    }
    cur = rl_cand_index(&c, e->target_pid, 1);
    t = -1;
    if (st->flags & WWREMOTE_FLAG_ATTACKER) {
        t = rl_attacker(e, a, &c, is_mo2);
        if (t >= 0) rl_ctr(WWREMOTE_CTR_ATTACKER_OVERRIDES);
    }
    if (t < 0) {
        if ((pf & WWREMOTE_PF_NO_LATCH) || cur < 0) {
            t = best;
        } else if (best != cur && e->hold >= st->min_hold &&
                   c.d2[best] * 256.0f < (f32)(s32)st->margin_q8 * c.d2[cur] &&
                   !(is_mo2 && (AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_FIGHT ||
                                rl_spear_thrust(a, MO2_WEAPON_SPH) || rl_spear_thrust(a, MO2_WEAPON2_SPH)))) {
            t = best;
        } else {
            t = cur;
        }
    }
    if (c.pid[t] != e->target_pid) {
        rl_ctr(WWREMOTE_CTR_SWITCHES);
        e->target_pid = c.pid[t];
        e->hold = 0;
    } else {
        rl_sat_inc(&e->hold);
    }
    return c.actor[t];
}

// The pass-through-or-swap decision for one eligible-looking execute.
static int rl_swap_execute(remote_swap_state* st, void* mtd, void* a) {
    s16 proc = AF(a, s16, AC_PROC_NAME);
    u8 pf = rl_policy(proc);
    int enemy = AF(a, u8, AC_GROUP) == GROUP_ENEMY;
    int both, r, i;
    u32 host, t, saved0, saved1 = 0;
    u32 named[MAX_REMOTE_LINKS];

    // 2 policy: ALLOW, or GROUP_WIDE over the ENEMY group; DENY always wins.
    if ((pf & WWREMOTE_PF_DENY) ||
        !((pf & WWREMOTE_PF_ALLOW) || ((st->flags & WWREMOTE_FLAG_GROUP_WIDE) && enemy))) {
        if (enemy) rl_ctr(WWREMOTE_CTR_DENIED);
        return fpcMtd_Execute(mtd, a);
    }
    // 3 the host gate.
    host = rl_host_link();
    if (!host) {
        rl_ctr(WWREMOTE_CTR_GATE_SKIPS);
        return fpcMtd_Execute(mtd, a);
    }
    // 4a the FF denylist (belt and braces, every eligible proc). Section
    // 5.3 step 4 puts it in the jail/demo gate, so it counts rouya_skips
    // (Step 2's FF check).
    if (!(st->flags & WWREMOTE_FLAG_FF_GATE_OFF) && rl_ff_denied()) {
        rl_ctr(WWREMOTE_CTR_ROUYA_SKIPS);
        return fpcMtd_Execute(mtd, a);
    }
    // 4b the jail/demo gate (mo2) and the other procs' demo gates.
    if ((proc == PROC_MO2 && rl_mo2_gated(st, a)) || rl_proc_gated(proc, a)) {
        rl_ctr(WWREMOTE_CTR_ROUYA_SKIPS);
        return fpcMtd_Execute(mtd, a);
    }
    // 5 the target.
    t = rl_target(st, a, pf, host, proc == PROC_MO2);
    if (t == host) return fpcMtd_Execute(mtd, a);

    // 6 the swap: mPlayerInfo[0] (and, per proc, mpPlayerPtr[0]); the
    // bodies named PLAYER only inside this execute (D3).
    saved0 = RL_PLAYER_INFO0;
    RL_PLAYER_INFO0 = t;
    both = (pf & WWREMOTE_PF_BOTH_WORDS) && (st->flags & WWREMOTE_FLAG_BOTH_WORDS_ENABLE);
    if (both) {
        saved1 = RL_PLAYER_PTR0;
        RL_PLAYER_PTR0 = t;
        rl_ctr(WWREMOTE_CTR_BOTH_WORD_SWAPS);
    }
    for (i = 0; i < MAX_REMOTE_LINKS; i++) {
        named[i] = 0;
        if (st->flags & WWREMOTE_FLAG_NAME_A9) {
            u32 b = rl_body_actor(st, i);
            if (b && AF(b, s16, AC_PROC_NAME) == PROC_WWREL) {
                AF(b, s16, AC_PROC_NAME) = PROC_PLAYER;
                named[i] = b;
            }
        }
    }
    // 7 the original, exactly once; then everything back unconditionally.
    r = fpcMtd_Execute(mtd, a);
    for (i = 0; i < MAX_REMOTE_LINKS; i++) {
        if (named[i]) AF(named[i], s16, AC_PROC_NAME) = PROC_WWREL;
    }
    RL_PLAYER_INFO0 = saved0;
    if (both) RL_PLAYER_PTR0 = saved1;
    rl_ctr(WWREMOTE_CTR_SWAPS);
    return r;
}

// T2's exec shim lands here for every actor execute on the move-approved
// path (fopAc_Execute, f_op_actor.cpp:200), with fpcMtd_Execute's
// arguments. hook_calls counts before any early-out, magic 0 or not.
int wwrel_exec_hook(void* mtd, void* a) {
    remote_swap_state* st;
    rl_ctr(WWREMOTE_CTR_HOOK_CALLS);
    st = rl_state();
    if (!st || !st->swap_ready) return fpcMtd_Execute(mtd, a);
    return rl_swap_execute(st, mtd, a);
}

// --- ATTRIB (section 8.6), called from enemy_mirror.c's v2 path ----------------------------
u32 remote_attrib_body(u8 ha_flags) {
    remote_swap_state* st = rl_state();
    int i = (ha_flags & WWREMOTE_HAF_SLOT1) ? 1 : 0;
    u32 b;
    if (!(ha_flags & WWREMOTE_HAF_ATTRIB_BODY)) return 0;
    if (!st || !rl_in_mem1((u32)st) || !st->valid || st->role != WWREMOTE_ROLE_HOST ||
        !(st->flags & WWREMOTE_FLAG_ATTRIB) || i >= MAX_REMOTE_LINKS) return 0;
    b = rl_body_actor(st, i);
    if (!b || st->bodies[i].parked ||
        AF(b, u32, st->bodies[i].kind == WWREMOTE_KIND_FULL ? WWREMOTE_FULL_MAGIC_OFF : WWREMOTE_BODY_MAGIC_OFF) !=
            WWREMOTE_BODY_MAGIC ||
        AF(b, u32, AC_PID) != st->bodies[i].pid) return 0;
    return b;
}

int remote_attrib_force(u32 moblin, u32 body) {
    remote_swap_state* st = rl_state();
    rl_latch* e;
    u32 bpid;
    if (!st || !rl_in_mem1((u32)st) || !moblin || !body) return 0;
    bpid = AF(body, u32, AC_PID);
    e = rl_latch_find(st, AF(moblin, u32, AC_PID), 1);
    if (!e) return 0;
    e->is_mo2 = AF(moblin, s16, AC_PROC_NAME) == PROC_MO2;
    e->force_pid = bpid;
    e->force_frames = RL_FORCE_FRAMES;
    return (st->flags & WWREMOTE_FLAG_ATTACKER) && e->target_pid != bpid;
}
