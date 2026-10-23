// enemy_mirror.c: the enemy mirror (host-authoritative enemies): the
// Moblin of step S1 + the S2 animation stream, the S3 colliders and the
// host hit applier (the v2 At sphere, or v1's shrink behind a flag), and,
// from block layout 3, any enemy class with a row in the kind table
// (wwenemy_kinds.h: Moblin, Bokoblin, ChuChu; the allow batch's Keese,
// Armos Knight and Armos; the TN batch's Darknut; batch C's Kargaroc;
// batch D's Magtail, batch C's cut row over its retail body builders,
// under EMK_WITH_MT), the per-class hooks and the quiet death.
// Design: ww-multiplayer-handoff/design/host-authoritative-prototype.md,
// moblin-s2-v2-final.txt (s2.rel_side, v2), enemy-mirror-general.json
// (layout 3: "rel_changes", "closed_before_build", "critic_applied"),
// enemy-mirror-allow.json (KI, AM, AM2: "rel_changes" R-1..R-16, decisions
// D2-D16), enemy-mirror-tn.json (TN and AM2's direct apply: "rel_changes"
// R-1..R-16, decisions D1-D14, "p0_findings") and enemy-mirror-batch-c.json
// (layout 4, the ambush Bokoblin, BB and MT: "layout4", "rel_changes"
// R-1..R-20, "ambush_cleanup", "bb", "mt", decisions D1-D14; facts in
// enemy-mirror-batch-c-facts.json) and enemy-mirror-batch-d.json (MT's
// retail-call body: R-D6, D8-D10, "magtail"; research/C1b-magtail-redo.md);
// docs: inject/rel/README.md "enemy_mirror.c".
//
// Go writes one block per tick into the mailbox slack at MAILBOX_ADDR +
// WWENEMY_OFF (inject/include/wwenemy.h), with the annex below it in the
// same write: up to four enemies named by pid, each with its proc, its
// placement key, a target position, angle, tilt, flags and (S2) the
// host's bck index, attribute, restart seq, morf, frame and rate. Every
// frame of the module actor (rel_main.c's execute, list 7: after Link's
// execute, before the draw phase and dScnPly's Ccsp()->Move) this unit:
//   0 intake   consumes the block when seq_head == seq_tail != last_seq
//              (a torn Go write keeps the previous targets for a frame),
//              caches the kind mask, each slot's proc and kind and the
//              annex overrides (em_ext), drops a slot whose kind is off
//              (ERR_KIND) or whose actor's placement key differs
//              (ERR_KEY), writes the annex aux values onto copies it
//              already owns, acks it, forgets everything on an epoch
//              change, and releases everything after
//              WWENEMY_STALE_FRAMES frames without a new seq;
//   1 scope    releases everything unless the game is in the block's stage
//              and room and no stage change is queued;
//   2 resolve  fopAcM_SearchByID(pid) and revalidates the actor every frame
//              (kind, initState, setID + home room, the kind's gates, morf
//              and model); adopts it by setting mPauseFlag bit 1 at
//              proc+0x0B so fpcEx_Execute skips its execute
//              (f_pc_executor.cpp), and re-asserts the bit every frame
//              because fopScnPause_Disable clears it on every leaf at
//              overlap wipes (f_op_scene_pause.cpp);
//   3 smooth   lerps toward the target (or snaps) and writes current.pos,
//              old.pos, current.angle.y, shape_angle.y and (damagereaction
//              kinds) the tilt;
//   4 animate  S1: mDoExt_McaMorf::play(NULL, 0, 0) so the morf chases and
//              the frame advances without the host-only footstep sounds;
//              S2 (WWENEMY_FLAG_ANIM): setAnm the streamed bck on a change
//              (sound pointer NULLed around it), mirror the rate, the
//              kind's shapes, play unless the host skipped it, then write
//              the streamed frame (plus the lead since the sample) as f32;
//   5 matrix   the kind's base matrix (dr_matrix_set, ChuChu's draw_SUB
//              recipe, or the plain T*Ry*Rx*Rz of KI/AM/AM2) into the
//              model's base matrix (also its cull matrix), then
//              McaMorf::calc so the joint callbacks run; then the
//              Bokoblin's stick, shield and bouen, the ChuChu's or fixed
//              eye and the Z-target flag;
//   6 collide  (WWENEMY_FLAG_COLLIDERS) re-submits the kind's Co/Tg/head
//              (and in defence the shield) colliders the way its execute
//              does, and turns a Tg hit on the Tg cylinder or head sphere
//              (or the Armos Knight's swallowed bomb) into a hit entry for
//              Go; never a weapon, never health;
//   7 parry    the Bt fields the kind's execute writes (no opening);
//   8 release  clears bit 1, zeroes speed/speedF, old = current, and puts
//              the copy back into a state its own execute resumes from
//              (em_handback, per kind); death = release + health 0 + the
//              light-arrow shrink timer (enemy_ice, c_damagereaction.cpp
//              :117-168), or with SF2_QUIET the quiet death: the copy
//              stays paused, shrinks in the REL and is deleted
//              (fopAcM_delete), so it never drops an item; a quiet-dying
//              copy is deleted, never handed back, on every release path;
//   host role  hit_apply: v2 builds a one-off At sphere (a copy of a
//              sphere image, Set from a dCcD_SrcSph, with a stts Init'd
//              from the host Link) and registers it with cCcS::Set so the
//              target's own damage_check runs next frame; the At record is
//              read one execute later for the result. v1
//              (WWENEMY_HAF_FORCE_V1) writes the shrink timer instead.
//              Collider 3 (the Armos Knight's bomb swallow) builds no
//              sphere: the swallow mode is written directly. An Armos
//              weak-spot hit the client judged (WWENEMY_HAF_VERDICT) Sets
//              no sphere either: the same sphere goes straight through
//              cc_at_check and week_atari_check's tail (em_hit_direct).
// Every game call is an absolute function-pointer cast (no relocation);
// no libc, no float library calls; the state is in the actor struct except
// the two static collision objects in the mailbox slack.
//
// Offsets are the zeldaret/tww GZLE01 decomp's: include/f_pc/f_pc_base.h
// (base_process_class), include/f_op/f_op_actor.h (fopAc_ac_c,
// fopEn_enemy_c), include/d/actor/d_a_mo2.h (mo2_class), d_a_bk.h
// (bk_class), d_a_cc.h (cc_class), d_a_boko.h (daBoko_c), d_a_ki.h
// (ki_class), d_a_am.h (am_class), d_a_am2.h (am2_class),
// include/c/c_damagereaction.h (damagereaction, enemyice),
// include/m_Do/m_Do_ext.h (mDoExt_McaMorf) with
// include/JSystem/J3DGraphAnimator/J3DAnimation.h (J3DFrameCtrl) and
// J3DModel.h, include/SSystem/SComponent/c_cc_d.h and include/d/d_cc_d.h
// (the collider objects), include/d/d_resorce.h (the resource table). The
// per-class offsets are wwenemy_kinds.h's. Function addresses:
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
// The annex below the block: its offsets are mailbox-relative
// (WWENEMY_ANNEX_*), not block-relative.
#define AX_U32(off)  (*(volatile u32*)(MAILBOX_ADDR + (off)))
#define AX_U16(off)  (*(volatile u16*)(MAILBOX_ADDR + (off)))
#define AX_U8(off)   (*(volatile u8*)(MAILBOX_ADDR + (off)))
#define AX_S16(off)  (*(volatile s16*)(MAILBOX_ADDR + (off)))
// Layout 4's knob table (WWENEMY_KNOB_*, mailbox-relative like the annex):
// a field of kind id's row, read live at the point of use, only while
// em_ext's EM_X_KNOB_OK says the last consume validated it.
#define KN_OVR(id, f) (WWENEMY_KNOB_OFF + WWENEMY_KNOB_OVR_OFF + (id) * WWENEMY_ANNEX_OVR_SIZE + (f))
#define KN_INVW(id)   (WWENEMY_KNOB_OFF + WWENEMY_KNOB_INVW_OFF + (id))

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
                            // layout 4 (D4, D10): the former reserved u32 holds
                            // the knob echo, the BB fingerprint misses and a spare
                            WWENEMY_HIT_RESULT_OFF + 4 == WWENEMY_KNOB_SEEN_OFF &&
                            WWENEMY_KNOB_SEEN_OFF + 2 == WWENEMY_FP_MISS_OFF &&
                            WWENEMY_FP_MISS_OFF + 1 == WWENEMY_TAIL_SPARE_OFF &&
                            WWENEMY_TAIL_SPARE_OFF + 1 == WWENEMY_SIZE) ? 1 : -1];
typedef char em_check_slot_fields[(WWENEMY_SL_RATE_OFF + 4 == WWENEMY_SL_SAMPLE_OFF &&
                                   WWENEMY_SL_SAMPLE_OFF + 4 == WWENEMY_SL_BCK_OFF &&
                                   WWENEMY_SL_BLUR_SHAPE_OFF + 1 <= WWENEMY_SL_BLUR_SCALE_OFF &&
                                   WWENEMY_SL_BLUR_SCALE_OFF + 4 == WWENEMY_SLOT_SIZE) ? 1 : -1];
// Layout 3's slot words: the placement key over the old attack/phase/
// action/mode bytes' end, the proc between blur_shape and blur_scale, and
// CC's m316.z over attack/phase (BB's angle.z in layout 4, R-12). Layout
// 4's header words (D2, D4): knob_gen in the pad after the role, the hook
// bytes for ids 7..14 in the former reserved u32[2], all in the Go span.
typedef char em_check_slot_l3[(WWENEMY_SL_PKEY_OFF + 4 <= WWENEMY_SL_ATTACK_OFF &&
                               WWENEMY_SL_TILT_OFF + 2 <= WWENEMY_SL_PKEY_OFF &&
                               WWENEMY_SL_CC_M316Z_OFF + 2 <= WWENEMY_SL_HEALTH_OFF &&
                               WWENEMY_SL_BLUR_SHAPE_OFF + 1 <= WWENEMY_SL_PROC_OFF &&
                               WWENEMY_SL_PROC_OFF + 2 <= WWENEMY_SL_BLUR_SCALE_OFF &&
                               WWENEMY_ROLE_OFF + 1 == WWENEMY_KNOB_GEN_OFF &&
                               WWENEMY_KNOB_GEN_OFF + 2 == WWENEMY_KIND_MASK_OFF &&
                               WWENEMY_KIND_MASK_OFF + 4 == WWENEMY_HKD_HI_OFF &&
                               WWENEMY_HKD_HI_OFF + WWENEMY_HKD_HI_KINDS == WWENEMY_SLOTS_OFF) ? 1 : -1];
// The annex: aux[4] per block slot, then its seq, then the per-kind
// overrides and invuln waits, all below the block.
typedef char em_check_annex[(WWENEMY_ANNEX_OFF + WWENEMY_MAX_SLOTS * WWENEMY_ANNEX_AUX_SIZE <= WWENEMY_ANNEX_SEQ_OFF &&
                             WWENEMY_ANNEX_SEQ_OFF + 4 <= WWENEMY_ANNEX_OVR_OFF &&
                             WWENEMY_ANNEX_OVR_OFF + WWENEMY_ANNEX_KINDS * WWENEMY_ANNEX_OVR_SIZE <= WWENEMY_ANNEX_INVW_OFF &&
                             WWENEMY_ANNEX_INVW_OFF + WWENEMY_ANNEX_KINDS <= WWENEMY_ANNEX_OFF + WWENEMY_ANNEX_SIZE &&
                             WWENEMY_ANNEX_OFF + WWENEMY_ANNEX_SIZE == WWENEMY_OFF &&
                             WWENEMY_ANNEX_AUX_SIZE == 4 * 2 &&
                             WWENEMY_ANNEX_OVR_TG_UP_OFF + 2 == WWENEMY_ANNEX_OVR_SIZE &&
                             (WWENEMY_ANNEX_OFF & 3) == 0 && (WWENEMY_ANNEX_SEQ_OFF & 3) == 0) ? 1 : -1];
// The allow batch's hook bytes (D3, R-1): the annex's last four bytes,
// past the invuln waits and their pad, one per kind id 3..6. Layout 4 (R-5):
// ids 7..14 take the block's WWENEMY_HKD_HI_OFF bytes (the first is BB's),
// and the knob table has a row for every kind.
typedef char em_check_annex_hkdx[(WWENEMY_ANNEX_INVW_OFF + WWENEMY_ANNEX_KINDS + 1 <= WWENEMY_ANNEX_HKDX_OFF &&
                                  WWENEMY_ANNEX_HKDX_OFF + WWENEMY_ANNEX_HKDX_KINDS == WWENEMY_OFF &&
                                  EMK_KIND_KI == WWENEMY_ANNEX_KINDS &&
                                  EMK_KIND_TN + 1 == EMK_KIND_BB &&
                                  WWENEMY_ANNEX_KINDS + WWENEMY_ANNEX_HKDX_KINDS == EMK_KIND_BB &&
                                  EMK_KIND_BB + WWENEMY_HKD_HI_KINDS >= EMK_KIND_COUNT &&
                                  WWENEMY_KNOB_KINDS >= EMK_KIND_COUNT && WWENEMY_KNOB_KINDS <= 16 &&
                                  WWENEMY_KNOB_ID_MIN == EMK_KIND_KI) ? 1 : -1];
// The knob table (layout4.knob_table): past the remote-Link block
// (wwremote.h WWREMOTE_REGION_END) inside the free slack below +0x1100 and
// the arena, 4-aligned, its fields nested; each row is an annex override
// (WWENEMY_ANNEX_OVR_*) so one sub-offset set serves both.
typedef char em_check_knob[(WWENEMY_KNOB_OFF >= WWREMOTE_REGION_END &&
                            WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE <= 0x1100 &&
                            MAILBOX_ADDR + WWENEMY_KNOB_OFF + WWENEMY_KNOB_SIZE <= 0x80414000 &&
                            (WWENEMY_KNOB_OFF & 3) == 0 && WWENEMY_KNOB_GEN_HEAD_OFF == 0 &&
                            WWENEMY_KNOB_GEN_HEAD_OFF + 4 <= WWENEMY_KNOB_LAYOUT_OFF &&
                            WWENEMY_KNOB_LAYOUT_OFF + 2 <= WWENEMY_KNOB_COUNT_OFF &&
                            WWENEMY_KNOB_COUNT_OFF + 1 <= WWENEMY_KNOB_OVR_OFF &&
                            WWENEMY_KNOB_OVR_OFF + WWENEMY_KNOB_KINDS * WWENEMY_ANNEX_OVR_SIZE <= WWENEMY_KNOB_INVW_OFF &&
                            WWENEMY_KNOB_INVW_OFF + WWENEMY_KNOB_KINDS <= WWENEMY_KNOB_GEN_TAIL_OFF &&
                            WWENEMY_KNOB_GEN_TAIL_OFF + 4 <= WWENEMY_KNOB_SIZE &&
                            (WWENEMY_KNOB_GEN_TAIL_OFF & 3) == 0 && (WWENEMY_KNOB_OVR_OFF & 1) == 0 &&
                            WWENEMY_ANNEX_OVR_SIZE == 4 && WWENEMY_ANNEX_OVR_TG_UP_OFF == 2) ? 1 : -1];
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
// daPy_lk_c mFanWindSph (d_a_player_main.h:2347; remote.c LK_FAN_WIND_SPH):
// the At image source of a kind without a sphere of its own (sph_src 0).
// DOL-constructed (d_a_player_main.cpp:12206-12207), no hit callback.
#define EM_LK_FAN_WIND_SPH 0x49C4
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
// mDoMtx_XrotM/YrotM/ZrotM(Mtx, s16): mtx = mtx * R (m_Do_mtx.cpp:109-169,
// symbols.txt:132,134,136), what cMtx_*rotM and mDoMtx_stack_c::*rotM
// call (m_Do_mtx.h:58-66, 316-334): the game's own sine table and concat.
typedef void (*em_mtx_rot_t)(f32 (*m)[4], s16 a);
#define mDoMtx_XrotM      ((em_mtx_rot_t)0x8000CD88)
#define mDoMtx_YrotM      ((em_mtx_rot_t)0x8000CE28)
#define mDoMtx_ZrotM      ((em_mtx_rot_t)0x8000CEC8)
typedef void (*em_set_c_t)(void* shape, const cXyz* c);              // SetC__8cM3dGCylFRC4cXyz / SetC__8cM3dGSphFRC4cXyz
#define cM3dGCyl_SetC     ((em_set_c_t)0x80251D88)
#define cM3dGSph_SetC     ((em_set_c_t)0x8025238C)
typedef void (*em_ccs_set_t)(void* ccs, void* obj);                  // Set__4cCcSFP8cCcD_Obj
#define cCcS_Set          ((em_ccs_set_t)0x80242D04)
typedef void (*em_clr_hit_t)(void* obj);                             // ClrTgHit__12dCcD_GObjInfFv / ClrAtHit__12dCcD_GObjInfFv / ClrCoHit__12dCcD_GObjInfFv
#define dCcD_GObjInf_ClrTgHit ((em_clr_hit_t)0x800AB958)
#define dCcD_GObjInf_ClrAtHit ((em_clr_hit_t)0x800AB7C4)
#define dCcD_GObjInf_ClrCoHit ((em_clr_hit_t)0x800ABB00)
// end__18dPa_smokeEcallBackFv (symbols.txt:2545), what remove() calls
// (d_particle.h:84): returns at once when no emitter is attached
// (d_particle.cpp). AM's mouth smoke mSmokeCbs[2] (EMK_AM_SMOKE_MOUTH).
typedef void (*em_smoke_end_t)(void* cb);
#define dPa_smokeEcallBack_end ((em_smoke_end_t)0x8007BB80)
// ChkCoHit__12dCcD_GObjInfFv (symbols.txt:3193) and GetCoHitObj (:3194):
// the Co record from the last Move, NULL unless its hit actor is known
// (d_cc_d.cpp:238-256). AM's mouth sphere (R-13).
typedef u32 (*em_chk_co_hit_t)(void* obj);
#define dCcD_GObjInf_ChkCoHit ((em_chk_co_hit_t)0x800ABB4C)
typedef void* (*em_get_co_hit_obj_t)(void* obj);
#define dCcD_GObjInf_GetCoHitObj ((em_get_co_hit_obj_t)0x800ABBA4)
// fopAcM_searchPlayerAngleY__FP10fopAc_ac_c (symbols.txt:4460): the
// angle from the actor to dComIfGp_getPlayer(0); cLib_distanceAngleS__Fss
// (:8495): |a - b| as an s16 distance. bomb_nomi_check's facing test
// (d_a_am.cpp:383-387).
typedef s16 (*em_player_angle_t)(void* a);
#define fopAcM_searchPlayerAngleY ((em_player_angle_t)0x800F0CCC)
typedef s16 (*em_dist_angle_t)(s16 a, s16 b);
#define cLib_distanceAngleS ((em_dist_angle_t)0x8025397C)
// cc_at_check__FP10fopAc_ac_cP8CcAtInfo (EMK_FN_CC_AT_CHECK, symbols.txt
// :3275): at_power_check on info->mpObj (its attacker through the obj's
// stts, d_cc_uty.cpp:87-114), then the target's health, hit SE and
// hit-stop (:188-260). AM2's week_atari_check calls it after its own
// bearing test (d_a_am2.cpp:290-296); em_hit_direct calls it in its place.
typedef void* (*em_cc_at_check_t)(void* tg, void* info);
#define cc_at_check ((em_cc_at_check_t)EMK_FN_CC_AT_CHECK)
// play__14mDoExt_baseAnmFv (EMK_FN_BASE_ANM_PLAY): mFrameCtrl->update()
// (m_Do_ext.cpp:116-120); btk and brk inherit it without an override.
typedef BOOL (*em_anm_play_t)(void* anm);
#define mDoExt_baseAnm_play ((em_anm_play_t)EMK_FN_BASE_ANM_PLAY)
#define ANM_FRAME_CTRL    0x04    // J3DFrameCtrl* mDoExt_baseAnm::mFrameCtrl (vptr at +0, m_Do_ext.h:21-46)
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
// fopAcM_delete__FUi(fpc_ProcID), symbols.txt:644 (f_op_actor_mng.h:465):
// queues the process for fpcDt_Handler, which has no pause check
// (f_pc_deletor.cpp), so a paused copy is deleted; nothing runs its
// execute again, so nothing drops.
typedef BOOL (*em_delete_t)(u32 pid);
#define fopAcM_delete_id  ((em_delete_t)EMK_FN_FOPACM_DELETE)
// dRes_getRes_byIdx (wwapi.h: getRes__14dRes_control_cFPCclP11dRes_info_ci
// 0x8006F208) with MOBJECT_INFO and OBJECT_INFO_COUNT: a 64-entry strcmp
// scan, no allocation (d_resorce.cpp:602-654).

// --- Actor offsets --------------------------------------------------------
// base_process_class (f_pc_base.h:13-32)
#define AC_PROC_ID        0x004   // fpc_ProcID mBsPcId (fopAcM_GetID)
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
#define AC_SHAPE_ANGLE_Y  0x20E   // s16 shape_angle.y (shape_angle x 0x20C, z 0x210)
#define AC_SHAPE_ANGLE_Z  0x210
#define AC_SCALE          0x214   // cXyz scale (f_op_actor.h:291): the BK ambush handback's 1.0, BB's setBaseScale
#define AC_SPEED          0x220   // cXyz speed
#define AC_MODEL          0x24C   // J3DModel* model (d_a_mo2.cpp:4189 sets it to mpMorf's model)
#define AC_SPEEDF         0x254   // f32 speedF
#define AC_ATTN_FLAGS     0x280   // u32 attention_info.flags (+0x26C + 0x14)
#define AC_HEALTH         0x285   // s8 health
// fopEn_enemy_c (f_op_actor.h:329-365)
#define EN_BT_NOW_FRAME   0x2A4   // f32 mBtNowFrame; daMo2_Execute writes 100.0f when no parry opening (d_a_mo2.cpp:3668)
// mo2_class (d_a_mo2.h): the legacy names, pinned against the kind
// table's Moblin row below; outside those pins they appear only in a
// kind == EMK_KIND_MO2 case.
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
// J3DModel (J3DModel.h:149-153): mBaseTransformMtx at +0x24
// (J3DMODEL_BASE_TR_MTX_OFFSET), Mtx* mpNodeMtx at +0x8C
// (J3DMODEL_MP_NODE_MTX_OFFSET, getAnmMtx(i) = mpNodeMtx[i]).
#define EM_MTX_SIZE       0x30
#define J3DMODEL_BASE_SCALE 0x18  // Vec mBaseScale (J3DModel.h:148): setBaseScale (BB, d_a_bb.cpp:2324; MT's segments, mt.rel 0x2024-0x2038)
// collider objects: dCcD_Cyl/Sph = dCcD_GObjInf (0xF8) + cCcD_Cyl/SphAttr
// (cCcD_ShapeAttr 0x20 + cM3dGCyl/Sph, centre at +0); the cCcD_ObjHitInf
// base is at +0 with At at +0 (mSPrm +0x00, mRPrm +0x04, mHitObj +0x08),
// Tg at +0x18 (mSPrm +0x18, mRPrm +0x1C, mHitObj +0x20) and Co at +0x2C
// (mSPrm; remote.c:290-319).
#define CC_AT_SPRM        0x00    // u32 cCcD_ObjAt.mSPrm: bit 0 = cCcD_AtSPrm_Set_e
#define CC_AT_RPRM        0x04    // u32 cCcD_ObjAt.mRPrm: bit 0 = hit this Move (SetAtHit)
#define CC_AT_HIT_OBJ     0x08    // cCcD_Obj* cCcD_ObjAt.mHitObj
#define CC_TG_SPRM        0x18    // u32 cCcD_ObjTg.mSPrm: bit 0 = Set
#define CC_TG_RPRM        0x1C    // u32: bit 0 = cCcD_TgRPrm_Hit_e
#define CC_TG_HIT_OBJ     0x20    // cCcD_Obj* cCcD_ObjTg.mHitObj (Go's Mo2CcTgHitObjOff)
#define CC_CO_SPRM        0x2C    // u32 cCcD_ObjCo.mSPrm: bit 0 = Set
#define CC_VPTR           0x3C    // the primary vptr (__vt__8dCcD_Sph for a sphere)
#define CC_STTS           0x44    // cCcD_Stts* cCcD_Obj.mStts (SetStts is an inline store, c_cc_d.h:608)
#define CC_STTS_ACTOR     0x0C    // stts +0x0C: the owner actor (c_cc_d.h:329; remote.c rl_obj_actor)
#define CC_GAT_CALLBACK   0x58    // dCcD_GObjAt mHitCallback (dCcD_GAtTgCoCommonBase +0x08 at +0x50)
#define CC_GTG_CALLBACK   0x9C    // dCcD_GObjTg mHitCallback (GObjTg at +0x94)
#define CC_GCO_CALLBACK   0xE4    // dCcD_GObjCo mHitCallback (GObjCo at +0xDC)
#define CC_SHAPE_CENTER   0x118   // cM3dGCyl/cM3dGSph (the SetC `this`)
#define CC_SHAPE_RADIUS   0x124   // f32 cM3dGSph/cM3dGCyl mRadius (+0x0C past the centre, c_m3d_g_sph.h:16-19, c_m3d_g_cyl.h:19-21)
#define CC_SHAPE_HEIGHT   0x128   // f32 cM3dGCyl mHeight (+0x10)
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
#define EM_TG_CENTER_UP   60      // collider 2: current.pos + (0, 60, 0), inside the Tg cylinder (EMK_MO2_TG_HIT_UP)
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
// Any damagereaction kind (MO2, BK): the ragdoll mode bound
// (c_damagereaction.cpp:1057) and the defence action (d_a_mo2.cpp:31;
// d_a_bk.cpp:3403-3405, closed_before_build "BK dr.mAction == 10").
#define EM_DR_RAGDOLL     (-100)
#define EM_DR_DEFENCE     10
// damagereaction::mpEnemy (c_damagereaction.h:79, +0x000): dr_matrix_set
// reads current.pos and shape_angle through it (c_damagereaction.cpp
// :1866-1880). Only the kind's execute sets it, after its hidden early
// returns (d_a_bk.cpp:4230-4281 then :4300; d_a_mo2.cpp:3473-3481 then
// :3501), and process memory starts zeroed (sBs_ClearArea), so a copy that
// never came out of hiding still has NULL here. The actor is at offset 0
// of bk_class / mo2_class, so the value is the actor itself.
#define EM_DR_MPENEMY     0x000
#define EM_KEYED_SETID_MAX 0x200  // keyed room actors (docs/08 §7.2)
#define EM_UNKEYED_SETID  0xFFFF  // fopAcM_CreateAppend's setID (f_op_actor_mng.cpp:81): an unkeyed actor (design D4)
#define EM_SNAP_DIST2     40000.0f // 200 units squared
#define EM_LERP           0.25f
#define EM_RANGE          1000000.0f
#define EM_RATE_MAX       8.0f    // |streamed rate| beyond this keeps the previous pair
#define EM_CO_FORWARD     35      // mCoCyl centre: pos + 35 units along angle.y (d_a_mo2.cpp:3571-3577; EMK_MO2_CO_FORWARD)
#define EM_BT_NO_OPENING  100.0f
#define EM_DEAD_LATCH     300
#define EM_HIT_COOLDOWN   8
#define EM_ANM_RETRY      60      // frames before a failed bck index is asked of getRes again
#define EM_FRAME_LEAD_CAP 3       // REL frames of rate added past the consumed sample
#define EM_HANDBACK_MORF  5.0f    // the blend into the restored wait loop
#define EM_TN_TATE_MORF   10.0f   // tate_anm_init's blend into the shield's ATATE_ON1 loop (d_a_tn.cpp:1939, 3458)
#define EM_INVULN_WAIT    8       // executes to wait for the Moblin's 5-frame invuln (EMK_MO2_INVULN_WAIT)
#define EM_MAX_SETS       2       // cCcS::Set calls per request (N, and N+1 if nothing crossed)
// em_ext.spare[0]: the kind id of the open hit_apply request, so the
// abort path can range-test mHitObj against a target it must not touch.
#define EM_X_HA_KIND      0

#define AF(a, T, off)     (*(T*)((u8*)(a) + (off)))

// --- The kind table (wwenemy_kinds.h; design D1, D15) ----------------------
// One row per live kind, indexed by kind id, in .rodata (the allow batch's
// KI, AM and AM2 at ids 3-5, enemy-mirror-allow.json R-2; TN at id 6,
// enemy-mirror-tn.json R-2, D2; batch C's BB at 7 and MT at 8, layout 4,
// R-10, R-19: MT last, so its cut drops the last row).
#define EM_ROW(K) { EMK_##K##_PROC, EMK_##K##_ID, EMK_##K##_CAPS, EMK_##K##_MTX, EMK_##K##_COLL,           \
                    EMK_##K##_ATTACH, EMK_##K##_EYE, EMK_##K##_ARC, EMK_##K##_BCK_FIRST, EMK_##K##_BCK_LAST, \
                    EMK_##K##_BCK_WAIT, EMK_##K##_INVULN_WAIT, EMK_##K##_CLASS_SIZE, EMK_##K##_MORF,        \
                    EMK_##K##_GATE, EMK_##K##_DR, EMK_##K##_DR_MODE, EMK_##K##_DR_ACTION, EMK_##K##_TILT,   \
                    EMK_##K##_CO_CYL, EMK_##K##_TG_CYL, EMK_##K##_HEAD_SPH, EMK_##K##_DEF_SPH,              \
                    EMK_##K##_HEAD_CENTER, EMK_##K##_DEF_CENTER, EMK_##K##_INVULN, EMK_##K##_ICE,           \
                    EMK_##K##_WEAPON, EMK_##K##_BARE, EMK_##K##_BLUR_ON, EMK_##K##_BLUR_SHAPE,              \
                    EMK_##K##_BLUR_SCALE, EMK_##K##_ANM_GATE, EMK_##K##_PLAY_SKIP, EMK_##K##_SPH_SRC,       \
                    EMK_##K##_CO_FORWARD, EMK_##K##_TG_HIT_UP, EMK_##K##_ATTN_F2, 0 }
static const em_kind EM_KINDS[EMK_KIND_COUNT] = { EM_ROW(MO2), EM_ROW(BK), EM_ROW(CC),
                                                  EM_ROW(KI), EM_ROW(AM), EM_ROW(AM2),
                                                  EM_ROW(TN), EM_ROW(BB),
#if EMK_WITH_MT
                                                  EM_ROW(MT)
#endif
};

// The rows sit at their ids; a kind id is a bit of the u16 kind_mask
// em_ext caches (layout 4, D5: bit 8 is MT, batch D; with EMK_WITH_MT 0
// em_kind_of has no row for it and Go never sets it); the annex overrides cover ids
// < WWENEMY_ANNEX_KINDS only, the knob table ids WWENEMY_KNOB_ID_MIN..15
// (R-1, R-4, em_hkd and friends), so no term ties the count to them.
typedef char em_check_kind_ids[(EMK_MO2_ID == EMK_KIND_MO2 && EMK_BK_ID == EMK_KIND_BK && EMK_CC_ID == EMK_KIND_CC &&
                                EMK_KI_ID == EMK_KIND_KI && EMK_AM_ID == EMK_KIND_AM && EMK_AM2_ID == EMK_KIND_AM2 &&
                                EMK_TN_ID == EMK_KIND_TN && EMK_BB_ID == EMK_KIND_BB && EMK_KIND_BB == 7 &&
                                EMK_KIND_MT == 8 && EMK_KIND_COUNT == EMK_KIND_MT + EMK_WITH_MT &&
                                EMK_KIND_COUNT <= 16 &&
                                sizeof(EMK_MO2_ARC) <= 4 && sizeof(EMK_BK_ARC) <= 4 && sizeof(EMK_CC_ARC) <= 4 &&
                                sizeof(EMK_KI_ARC) <= 4 && sizeof(EMK_AM_ARC) <= 4 && sizeof(EMK_AM2_ARC) <= 4 &&
                                sizeof(EMK_TN_ARC) <= 4 && sizeof(EMK_BB_ARC) <= 4) ? 1 : -1];
// EMK_MTX_PLAIN (R-2, R-3): only on an fopEn_enemy_c with a morf (the
// matrix goes into its model; enemyice exists for the ice gates).
#define EM_PLAIN_OK(K)    (EMK_##K##_MTX != EMK_MTX_PLAIN || ((EMK_##K##_CAPS & EMK_CAP_FOPEN) && EMK_##K##_MORF != 0))
typedef char em_check_plain_rows[(EM_PLAIN_OK(MO2) && EM_PLAIN_OK(BK) && EM_PLAIN_OK(CC) && EM_PLAIN_OK(KI) &&
                                  EM_PLAIN_OK(AM) && EM_PLAIN_OK(AM2) && EM_PLAIN_OK(TN) && EM_PLAIN_OK(BB)) ? 1 : -1];
// The allow rows' hooks and the fields their cases name (wwenemy_kinds.h
// KI/AM/AM2; d_a_ki.h, d_a_am.h, d_a_am2.h): spheres in KI's cylinder
// fields, one shared body for AM/AM2 (D7), the head sphere = the eye or
// weak spot, every row with enemyice (D12), the D4 attention bits.
typedef char em_check_allow_rows[(EMK_KI_MTX == EMK_MTX_PLAIN && EMK_AM_MTX == EMK_MTX_PLAIN && EMK_AM2_MTX == EMK_MTX_PLAIN &&
                                  EMK_KI_EYE == EMK_EYE_FIXED && EMK_AM_EYE == EMK_EYE_FIXED && EMK_AM2_EYE == EMK_EYE_FIXED &&
                                  EMK_KI_COLL == EMK_COLL_STD && EMK_AM_COLL == EMK_COLL_STD && EMK_AM2_COLL == EMK_COLL_STD &&
                                  (EMK_KI_CAPS & EMK_CAP_SPH_COLL) && (EMK_KI_CAPS & EMK_CAP_PKEY_NO_POS) &&
                                  !(EMK_KI_CAPS & EMK_CAP_DR) && !(EMK_AM_CAPS & EMK_CAP_DR) && !(EMK_AM2_CAPS & EMK_CAP_DR) &&
                                  (EMK_AM2_CAPS & EMK_CAP_MODEL_EQ) && !(EMK_KI_CAPS & EMK_CAP_MODEL_EQ) &&
                                  !(EMK_AM_CAPS & EMK_CAP_MODEL_EQ) &&
                                  !((EMK_MO2_CAPS | EMK_BK_CAPS | EMK_CC_CAPS | EMK_AM_CAPS | EMK_AM2_CAPS | EMK_TN_CAPS) &
                                    (EMK_CAP_SPH_COLL | EMK_CAP_PKEY_NO_POS)) &&
                                  EMK_KI_HEAD_SPH == 0 && EMK_KI_DEF_SPH == 0 && EMK_KI_CO_CYL != EMK_KI_TG_CYL &&
                                  EMK_AM_CO_CYL == EMK_AM_TG_CYL && EMK_AM_TG_CYL == EMK_AM_BODY_CYL &&
                                  EMK_AM_HEAD_SPH == EMK_AM_EYE_SPH && EMK_AM_DEF_SPH == 0 &&
                                  EMK_AM2_CO_CYL == EMK_AM2_TG_CYL && EMK_AM2_DEF_SPH == 0 &&
                                  EMK_KI_ICE != 0 && EMK_AM_ICE != 0 && EMK_AM2_ICE != 0 &&
                                  EMK_KI_ATTN_F2 == EMK_KI_F2_ATTN && EMK_AM_ATTN_F2 == EMK_AM_F2_ATTN &&
                                  EMK_AM2_ATTN_F2 == EMK_AM2_F2_ATTN && EMK_BK_ATTN_F2 == EMK_BK_F2_ATTN &&
                                  EMK_CC_ATTN_F2 == EMK_CC_F2_ATTN && EMK_MO2_ATTN_F2 == 0 &&
                                  EMK_BB_ATTN_F2 == EMK_BB_F2_ATTN && !(EMK_BB_CAPS & (EMK_CAP_DR | EMK_CAP_PKEY_NO_POS)) &&
                                  EMK_AM_GATE + 1 == EMK_AM_MODE && EMK_AM2_GATE + 1 == EMK_AM2_MODE &&
                                  EMK_AM_TIMERS + EMK_AM_TIMERS_LEN == 0x2D0 && EMK_AM_PLAY_SKIP == EMK_AM_TIMERS + 2 &&
                                  EMK_AM2_STARTS_INACTIVE + 1 == EMK_AM2_SWITCH && EMK_AM_SWALLOW_PID == EMK_AM_BCK_INDEX + 4 &&
                                  EMK_KI_PLAY_SKIP == 0 && EMK_AM2_PLAY_SKIP == 0 &&
                                  EMK_KI_QUIET_FRAMES > 0 && EMK_AM_QUIET_FRAMES > 0 && EMK_AM2_QUIET_FRAMES > 0 &&
                                  EMK_KI_QUIET_FRAMES < 0xFF && EMK_AM_QUIET_FRAMES < 0xFF && EMK_AM2_QUIET_FRAMES < 0xFF) ? 1 : -1];
// The Moblin row is today's constants (D6, D15): every legacy define.
typedef char em_check_mo2_row[(EMK_MO2_PROC == PROC_MO2 && EMK_MO2_MORF == MO2_MORF && EMK_MO2_GATE == MO2_MODE &&
                               EMK_MO2_DR == MO2_DR && EMK_MO2_DR_MODE == MO2_DR_MODE &&
                               EMK_MO2_DR_ACTION == MO2_DR_ACTION && EMK_MO2_TILT == MO2_TILT &&
                               EMK_MO2_CO_CYL == MO2_CO_CYL && EMK_MO2_TG_CYL == MO2_TG_CYL &&
                               EMK_MO2_HEAD_SPH == MO2_HEAD_SPH && EMK_MO2_DEF_SPH == MO2_DEF_SPH &&
                               EMK_MO2_HEAD_CENTER == MO2_HEAD_CENTER && EMK_MO2_DEF_CENTER == MO2_DEF_CENTER &&
                               EMK_MO2_INVULN == MO2_INVULN && EMK_MO2_ICE + EMK_ICE_SHRINK == MO2_ICE_SHRINK &&
                               EMK_MO2_WEAPON == MO2_WEAPON && EMK_MO2_BARE == MO2_BARE &&
                               EMK_MO2_BLUR_ON == MO2_BLUR_ON && EMK_MO2_BLUR_SHAPE == MO2_BLUR_SHAPE &&
                               EMK_MO2_BLUR_SCALE == MO2_BLUR_SCALE && EMK_MO2_ANM_GATE == MO2_ANM_GATE &&
                               EMK_MO2_PLAY_SKIP == MO2_PLAY_SKIP && EMK_MO2_SPH_SRC == MO2_HEAD_SPH &&
                               EMK_MO2_BCK_FIRST == MO2_BCK_FIRST && EMK_MO2_BCK_LAST == MO2_BCK_LAST &&
                               EMK_MO2_BCK_WAIT == MO2_BCK_WAIT && EMK_MO2_INVULN_WAIT == EM_INVULN_WAIT &&
                               EMK_MO2_CO_FORWARD == EM_CO_FORWARD && EMK_MO2_TG_HIT_UP == EM_TG_CENTER_UP &&
                               EMK_MO2_CAPS == (EMK_CAP_DR | EMK_CAP_MODEL_EQ | EMK_CAP_FOPEN | EMK_CAP_WAIT_MODE1 | EMK_CAP_ADDR_SUCCESS) &&
                               EMK_MO2_MTX == EMK_MTX_DR && EMK_MO2_COLL == EMK_COLL_STD &&
                               EMK_MO2_ATTACH == EMK_ATTACH_NONE && EMK_MO2_EYE == EMK_EYE_NONE &&
                               MO2_ACTION_DEFENCE == EM_DR_DEFENCE && MO2_MODE_RAGDOLL == EM_DR_RAGDOLL &&
                               EMK_BK_ACTION_DEFENCE == EM_DR_DEFENCE) ? 1 : -1];
// The generic actor fields are the ones this file already uses.
typedef char em_check_ac_fields[(EMK_AC_HOME_POS == AC_HOME && EMK_AC_HOME_ROOM == AC_HOME_ROOM &&
                                 EMK_AC_ATTN_FLAGS == AC_ATTN_FLAGS && EMK_AC_HEALTH == AC_HEALTH &&
                                 EMK_AC_BT_NOW == EN_BT_NOW_FRAME && EMK_AC_SHAPE_X + 2 == AC_SHAPE_ANGLE_Y &&
                                 AC_SHAPE_ANGLE_Y + 2 == AC_SHAPE_ANGLE_Z && EMK_AC_ANGLE_Y == AC_CUR_ANGLE_Y &&
                                 EMK_AC_SHAPE_Y == AC_SHAPE_ANGLE_Y && EMK_AC_SHAPE_Z == AC_SHAPE_ANGLE_Z &&
                                 EMK_AC_EYE_POS + 0x14 == EMK_AC_ATTN_POS && EMK_AC_ATTN_POS + 0xC == AC_ATTN_FLAGS &&
                                 EMK_AC_STATUS_CARRY == 0x2000) ? 1 : -1];
// Bokoblin and ChuChu derived fields, from their own class headers
// (d_a_bk.h, d_a_cc.h, c_damagereaction.h: dr +0x4 mMode, +0x6 mAction,
// +0x488 m488, +0x48A m48A, +0x48C m48C, +0x6E4 mSpawnY).
typedef char em_check_bk_cc[(EMK_BK_DR_MODE == EMK_BK_DR + 0x4 && EMK_BK_DR_ACTION == EMK_BK_DR + 0x6 &&
                             EMK_BK_DR_M488 == EMK_BK_DR + 0x488 && EMK_BK_DR_M48A == EMK_BK_DR + 0x48A &&
                             EMK_BK_TILT == EMK_BK_DR + 0x48C && EMK_BK_TILT_Z == EMK_BK_TILT + 4 &&
                             EMK_BK_DR_SPAWN_Y == EMK_BK_DR + 0x6E4 &&
                             EMK_BK_SHIELD_R == EMK_BK_DEF_SPH + CC_SHAPE_RADIUS &&
                             EMK_BK_SHIELD_CY == EMK_BK_DEF_CENTER + 0xC + 4 &&
                             EMK_CC_TILT_Z == EMK_CC_TILT + 4 && EMK_CC_TG_CYL == EMK_CC_CO_CYL &&
                             EMK_CC_CYL_AT_SPRM == CC_AT_SPRM && EMK_CC_CYL_TG_SPRM == CC_TG_SPRM &&
                             EMK_CC_CYL_CO_SPRM == CC_CO_SPRM && EMK_CC_CYL_R == CC_SHAPE_RADIUS &&
                             EMK_CC_CYL_H == CC_SHAPE_HEIGHT &&
                             EMK_BOKO_CARRY_BYTE == 0x1C4 + 2 && EMK_BOKO_CARRY_BIT == (0x2000 >> 8)) ? 1 : -1];
// flags2: MO2 and BK share bits 0-2 (has-weapon, play-skip, blur-on), so
// the S2 code reads one bit for both; bits 6-7 are generic and no kind
// bit reaches them.
typedef char em_check_flags2[(EMK_BK_F2_HAS_WEAPON == WWENEMY_SF2_HAS_WEAPON &&
                              EMK_BK_F2_PLAY_SKIP == WWENEMY_SF2_PLAY_SKIP &&
                              EMK_BK_F2_BLUR_ON == WWENEMY_SF2_BLUR_ON &&
                              EMK_BK_F2_ATTN < WWENEMY_SF2_LIGHT && EMK_CC_F2_ATTN < WWENEMY_SF2_LIGHT &&
                              WWENEMY_SF2_LIGHT == 0x40 && WWENEMY_SF2_QUIET == 0x80 &&
                              // AM's play-skip is the S2 bit em_animate reads with
                              // k->play_skip; KI and AM2 have no play_skip field,
                              // so their bit 1 (AM2 WEAK_TG) is never read as one.
                              EMK_AM_F2_PLAY_SKIP == WWENEMY_SF2_PLAY_SKIP &&
                              EMK_KI_F2_LUNGE < WWENEMY_SF2_LIGHT && EMK_AM2_F2_CARRIED < WWENEMY_SF2_LIGHT &&
                              EMK_AM2_F2_WEAK_TG < WWENEMY_SF2_LIGHT && EMK_AM2_F2_BODY_CO < WWENEMY_SF2_LIGHT &&
                              // TN shares BK's bits 0-1: em_quiet_drop reads one
                              // has-weapon bit for both, em_animate one play-skip.
                              EMK_TN_F2_HAS_WEAPON == WWENEMY_SF2_HAS_WEAPON &&
                              EMK_TN_F2_HAS_WEAPON == EMK_BK_F2_HAS_WEAPON &&
                              EMK_TN_F2_PLAY_SKIP == WWENEMY_SF2_PLAY_SKIP &&
                              EMK_TN_F2_ATTN < WWENEMY_SF2_LIGHT &&
                              // BB streams only its attention bit; with no play_skip,
                              // blur or attach no S2 reader takes its bits 0-2.
                              EMK_BB_F2_ATTN < WWENEMY_SF2_LIGHT && EMK_BB_PLAY_SKIP == 0 &&
                              EMK_BB_BLUR_ON == 0 && EMK_BB_ATTACH == EMK_ATTACH_NONE) ? 1 : -1];
// The TN row and the fields its hooks name (wwenemy_kinds.h TN; d_a_tn.h,
// c_damagereaction.h: dr +0x4 mMode, +0x6 mAction, +0x88 m088, +0x48C
// m48C, +0x6E4 mSpawnY). No MODEL_EQ (d_a_tn never assigns actor->model,
// R-1); no blur words (em_animate's u32 store would wipe the pieces byte);
// the per-kind HKDX bits reuse only generic bits that are dead for this
// row (R-3, D9); AM2's direct apply and the 0x1FB knob sit in the annex
// and HIT_APPLY bytes layout 3 left free.
#define EM_DR_SPAWN_Y     0x6E4   // f32 damagereaction mSpawnY: the generic fall test reads it before dr_body_bg_check refreshes it (c_damagereaction.cpp:1049, 1232)
typedef char em_check_tn_row[(EMK_TN_CAPS == (EMK_CAP_DR | EMK_CAP_FOPEN) && EMK_TN_MTX == EMK_MTX_DR &&
                              EMK_TN_COLL == EMK_COLL_STD && EMK_TN_ATTACH == EMK_ATTACH_TN && EMK_TN_EYE == EMK_EYE_NONE &&
                              EMK_TN_DR_MODE == EMK_TN_DR + 4 && EMK_TN_DR_ACTION == EMK_TN_DR + 6 &&
                              EMK_TN_TILT == EMK_TN_DR + 0x48C && EMK_TN_TILT_Z == EMK_TN_TILT + 4 &&
                              EMK_TN_DR_M088 == EMK_TN_DR + 0x88 && (EMK_TN_DR_M088 & 3) == 0 && (EMK_TN_DR_M088_LEN & 3) == 0 &&
                              EMK_BK_DR_SPAWN_Y == EMK_BK_DR + EM_DR_SPAWN_Y &&
                              EMK_TN_DEF_R == EMK_TN_DEF_SPH + CC_SHAPE_RADIUS && EMK_TN_ATTN_F2 == EMK_TN_F2_ATTN &&
                              EMK_TN_BLUR_ON == 0 && EMK_TN_BLUR_SHAPE == 0 && EMK_TN_BLUR_SCALE == 0 &&
                              EMK_TN_ANM_GATE == 0 && EMK_TN_ICE != 0 && EMK_TN_WEAPON != 0 &&
                              EMK_TN_ACT_DEFENCE == EM_DR_DEFENCE && EMK_TN_ACT_DIE_A < EMK_TN_ACT_DIE_C &&
                              EMK_TN_HKD_SHIELD == WWENEMY_HKD_MODEL_EQ && EMK_TN_HKD_ATTACH == WWENEMY_HKD_ATTACH &&
                              EMK_TN_HKD_SWORD == WWENEMY_HKD_EYE && EMK_TN_HKD_COLL == WWENEMY_HKD_LEGACY_DEATH &&
                              EMK_TN_HKD_ADOPT_ANM == WWENEMY_HKD_MTX_PLAIN && EMK_TN_HKD_DIRECT == WWENEMY_HKD_SPECIAL &&
                              EMK_TN_QUIET_HOLD < EMK_TN_QUIET_FRAMES && EMK_TN_QUIET_FRAMES < 0xFF &&
                              EMK_TN_PIECE_MASK == (EMK_TN_PIECE_ARMOR | EMK_TN_PIECE_HELMET | EMK_TN_PIECE_SHIELD) &&
                              EMK_TN_BCK_AGUARD1 >= EMK_TN_BCK_FIRST && EMK_TN_BCK_AHAKAI1 <= EMK_TN_BCK_LAST &&
                              EMK_TN_BCK_TATE_ON >= EMK_TN_BCK_FIRST && EMK_TN_BCK_TATE_ON <= EMK_TN_BCK_LAST &&
                              EMK_TN_BCK_WAIT >= EMK_TN_BCK_FIRST && EMK_TN_BCK_WAIT <= EMK_TN_BCK_LAST &&
                              EMK_TN_BCK_ARMOR_KIHON > EMK_TN_BCK_LAST &&
                              EMK_GTG_SPRM == CC_GTG_CALLBACK - 0x08 && EMK_GTG_HITMARK == EMK_GTG_SPRM + 0x1E &&
                              EMK_AC_STATUS_BOSS == 0x04000000 &&
                              WWENEMY_ANNEX_TN_TGUP_OFF == WWENEMY_ANNEX_INVW_OFF + WWENEMY_ANNEX_KINDS &&
                              WWENEMY_ANNEX_TN_TGUP_OFF + 1 == WWENEMY_ANNEX_HKDX_OFF &&
                              EMK_KIND_TN == WWENEMY_ANNEX_KINDS + WWENEMY_ANNEX_HKDX_KINDS - 1) ? 1 : -1];
// AM2's direct apply (TN batch R-14..R-16): the pending byte's bits, the
// HIT_APPLY flags past ATTRIB (remote.c rl_check_attrib), the details past
// HD_KIND, and CcAtInfo's fields (d_cc_uty.h:7-21).
typedef char em_check_am2_direct[(WWENEMY_HP_PENDING == 1 && !(WWENEMY_HP_VERDICT & WWENEMY_HP_PENDING) &&
                                  !(WWENEMY_HP_BACK & (WWENEMY_HP_PENDING | WWENEMY_HP_VERDICT)) &&
                                  !((WWENEMY_HAF_VERDICT | WWENEMY_HAF_BACK | WWENEMY_HAF_CRIT) &
                                    (WWENEMY_HAF_FORCE_V1 | WWENEMY_HAF_FALLBACK_SHRINK | WWENEMY_HAF_NO_HITMARK)) &&
                                  WWENEMY_HD_DIRECT == WWENEMY_HD_KIND + 1 && WWENEMY_HD_FRONT == WWENEMY_HD_DIRECT + 1 &&
                                  EMK_CCATINFO_SIZE == 0x1C && (EMK_CCATINFO_SIZE & 3) == 0 &&
                                  EMK_CCATINFO_OBJ == 0 && EMK_CCATINFO_PARTICLE == 0x14 &&
                                  EMK_AM2_ITAI_MODE_HIT < EMK_AM2_MODE_DANCE &&
                                  !(EMK_AT_OLD_PATH & (EMK_AT_SWORD_CLASS | EMK_AT_SKULL_HAMMER | EMK_AT_BOMB | EMK_AT_ARROWS)) &&
                                  (EMK_AT_SWORD_CLASS & AT_TYPE_SWORD)) ? 1 : -1];

// The placement key (identity.pkey; Go mods/enemysync PKey): FNV-1a-32
// over the actor's bytes as stored (big-endian, as Go hashes them), 0 ->
// 1. The test vector is checked here at compile time with the same step
// em_pkey uses, so the byte order and constants match Go's pin
// (TestKindTablePins / TestPKey) without any runtime cost.
#define EM_FNV(h, b)      ((((u32)(h)) ^ ((u32)(b) & 0xFFu)) * (u32)EMK_PKEY_FNV_PRIME)
#define EM_TVB(v, sh)     (((u32)(v) >> (sh)) & 0xFFu)
#define EM_FNV16(h, v)    EM_FNV(EM_FNV(h, EM_TVB(v, 8)), EM_TVB(v, 0))
#define EM_FNV32(h, v)    EM_FNV16(EM_FNV16(h, (u32)(v) >> 16), (u32)(v) & 0xFFFFu)
#define EM_TV_HASH        EM_FNV32(EM_FNV32(EM_FNV32(EM_FNV(EM_FNV32(EM_FNV16(EM_FNV16((u32)EMK_PKEY_FNV_OFFSET,       \
                              EMK_PKEY_TV_PROC), EMK_PKEY_TV_SETID), EMK_PKEY_TV_PARAMS), EMK_PKEY_TV_ROOM),                \
                              EMK_PKEY_TV_HOME_X), EMK_PKEY_TV_HOME_Y), EMK_PKEY_TV_HOME_Z)
typedef char em_check_pkey_tv[(EM_TV_HASH == (u32)EMK_PKEY_TV_OUT && 2 + 2 + 4 + 1 + 12 == EMK_PKEY_LEN) ? 1 : -1];
// The no-pos vector (D9, R-16; Go TestPKeyNoPosVector): a keyed row with
// EMK_CAP_PKEY_NO_POS hashes home.pos as twelve zero bytes.
#define EM_TV2_HASH       EM_FNV32(EM_FNV32(EM_FNV32(EM_FNV(EM_FNV32(EM_FNV16(EM_FNV16((u32)EMK_PKEY_FNV_OFFSET,      \
                              EMK_PKEY_TV2_PROC), EMK_PKEY_TV2_SETID), EMK_PKEY_TV2_PARAMS), EMK_PKEY_TV2_ROOM),            \
                              0), 0), 0)
typedef char em_check_pkey_tv2[(EM_TV2_HASH == (u32)EMK_PKEY_TV2_OUT && EMK_PKEY_TV2_PROC == EMK_KI_PROC &&
                                EMK_PKEY_TV2_SETID < EM_KEYED_SETID_MAX) ? 1 : -1];

// The live state, for _epilog: set at create, cleared at delete. Pointers
// only, valid for the module's own lifetime; forced into .data so the ELF
// carries no .bss.
static enemy_mirror_state* g_live __attribute__((section(".data"))) = (enemy_mirror_state*)0;
static em_ext* g_live_ext __attribute__((section(".data"))) = (em_ext*)0;

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

// --- Kinds ------------------------------------------------------------------
const em_kind* em_kind_of(u16 proc) {
    int i;
    for (i = 0; i < EMK_KIND_COUNT; i++) {
        if (EM_KINDS[i].proc == proc) return &EM_KINDS[i];
    }
    return 0;
}

// The row for a cached kind id (EMK_KIND_NONE: NULL).
static const em_kind* em_kind_at(u8 kind) {
    return kind < EMK_KIND_COUNT ? &EM_KINDS[kind] : 0;
}

static const em_kind* em_kind_of_actor(const void* a) {
    return em_kind_of((u16)AF(a, s16, AC_PROC_NAME));
}

// --- Per-kind overrides (R-1; layout 4 R-3, R-4) ----------------------------
// The annex overrides exist for ids < WWENEMY_ANNEX_KINDS only (em_ext's
// ovr[] and invuln_wait[] have that many entries; never index them with a
// larger id). Ids 3..6 have a live hook byte, read straight from the annex
// at the point of use, not seq-cached (D3: a change tears for one frame at
// most). Layout 4: ids 7..14 have theirs in the block (WWENEMY_HKD_HI_OFF,
// inside Go's seq span, D2), read as live; ids 3..15 take quiet frames,
// tg_hit_up and invuln wait from the knob table (mailbox +WWENEMY_KNOB_OFF)
// while the last consume found it valid (EM_X_KNOB_OK, em_knob_validate),
// else their compiled values. The table carries no kill switch but id 15's
// (D3): a torn or stale table only reverts tuning.

// R-3, at every consume after the annex cache: the table is the one the
// block names when knob_gen != 0, its head and tail words both equal it,
// it is layout 4's and it has a row for every kind. The echo
// (WWENEMY_KNOB_SEEN_OFF) is knob_gen when valid, else 0, so Go's
// "seen != gen" rewrite also fires after a clobber or a savestate load of
// a table that had validated before.
int em_knob_validate(em_ext* x) {
    u16 gen = EB_U16(WWENEMY_KNOB_GEN_OFF);
    int ok = gen != 0 && AX_U32(WWENEMY_KNOB_OFF + WWENEMY_KNOB_GEN_HEAD_OFF) == (u32)gen &&
             AX_U32(WWENEMY_KNOB_OFF + WWENEMY_KNOB_GEN_TAIL_OFF) == (u32)gen &&
             AX_U16(WWENEMY_KNOB_OFF + WWENEMY_KNOB_LAYOUT_OFF) == WWENEMY_LAYOUT &&
             AX_U8(WWENEMY_KNOB_OFF + WWENEMY_KNOB_COUNT_OFF) >= EMK_KIND_COUNT;
    x->spare[EM_X_KNOB_OK] = (u8)ok;
    EB_U16(WWENEMY_KNOB_SEEN_OFF) = ok ? gen : 0;
    return ok;
}

static int em_knob_row(const em_ext* x, const em_kind* k) {
    return x->spare[EM_X_KNOB_OK] && k->id >= WWENEMY_KNOB_ID_MIN && k->id < WWENEMY_KNOB_KINDS;
}

u8 em_knob_quiet(const em_ext* x, const em_kind* k) {
    return em_knob_row(x, k) ? AX_U8(KN_OVR(k->id, WWENEMY_ANNEX_OVR_QUIET_OFF)) : 0;
}

// TN keeps WWENEMY_ANNEX_TN_TGUP_OFF as its single source: its row is not read.
s16 em_knob_tg_hit_up(const em_ext* x, const em_kind* k) {
    if (k->id == EMK_KIND_TN || !em_knob_row(x, k)) return 0;
    return AX_S16(KN_OVR(k->id, WWENEMY_ANNEX_OVR_TG_UP_OFF));
}

u8 em_knob_invuln(const em_ext* x, const em_kind* k) {
    return em_knob_row(x, k) ? AX_U8(KN_INVW(k->id)) : 0;
}

// The hook_disable bits for kind k (WWENEMY_HKD_*; per-kind names in
// wwenemy_kinds.h for TN, BB and MT).
u8 em_hkd(const em_ext* x, const em_kind* k) {
    if (k->id < WWENEMY_ANNEX_KINDS) return x->ovr[k->id].hook_disable;
    if (k->id < WWENEMY_ANNEX_KINDS + WWENEMY_ANNEX_HKDX_KINDS)
        return AX_U8(WWENEMY_ANNEX_HKDX_OFF + (k->id - WWENEMY_ANNEX_KINDS));
    if (k->id < EMK_KIND_BB + WWENEMY_HKD_HI_KINDS) return EB_U8(WWENEMY_HKD_HI_OFF + (k->id - EMK_KIND_BB));
    if (k->id < WWENEMY_KNOB_KINDS && x->spare[EM_X_KNOB_OK])
        return AX_U8(KN_OVR(k->id, WWENEMY_ANNEX_OVR_HOOKS_OFF));
    return 0;
}

// The quiet death's frame count: the annex's (ids < 3) or the knob table's
// (ids 3..15), else 90 when the host died by enemy_ice (SF2_LIGHT), else
// the row's compiled count (AM: its DEAD bck, 40 frames, A0) or
// EMK_QUIET_FRAMES.
u8 em_quiet_frames(const em_ext* x, const em_kind* k, u8 flags2) {
    u8 q;
    if (k->id < WWENEMY_ANNEX_KINDS && x->ovr[k->id].quiet_frames) return x->ovr[k->id].quiet_frames;
    if ((q = em_knob_quiet(x, k)) != 0) return q;
    if (flags2 & WWENEMY_SF2_LIGHT) return EMK_QUIET_FRAMES_LIGHT;
    switch (k->id) {
    case EMK_KIND_KI:
        return EMK_KI_QUIET_FRAMES;
    case EMK_KIND_AM:
        return EMK_AM_QUIET_FRAMES;
    case EMK_KIND_AM2:
        return EMK_AM2_QUIET_FRAMES;
    case EMK_KIND_TN:
        return EMK_TN_QUIET_FRAMES;
    case EMK_KIND_BB:
        return EMK_BB_QUIET_FRAMES;
#if EMK_WITH_MT
    case EMK_KIND_MT:
        return EMK_MT_QUIET_FRAMES;
#endif
    }
    return EMK_QUIET_FRAMES;
}

// The TN batch's hold (p0_findings DEATH ORDER): the frames em_die_quiet
// keeps scale 1 before its shrink. A Darknut's DYING starts with its host
// ragdoll (health <= 0, dr.mMode <= -100), whose BDOWN_A1/BDOWN_U1 the copy
// plays while it holds; enemy_ice's light death (SF2_LIGHT) keeps the
// generic 90 frames with no hold. Every other row: 0 (scale 1 - f/N).
u8 em_quiet_hold(const em_kind* k, u8 flags2) {
    if (k->id == EMK_KIND_TN && !(flags2 & WWENEMY_SF2_LIGHT)) return EMK_TN_QUIET_HOLD;
    return 0;
}

// Collider 2's centre height above current.pos (the host's v2 sphere). TN's
// one live knob is the invuln waits' pad byte (R-13, layout_decision: 0 =
// compiled), read straight from the annex as em_hkd reads the hook bytes;
// the other ids >= 3 take the knob table's (layout 4, R-4).
s16 em_tg_hit_up(const em_ext* x, const em_kind* k) {
    s16 up;
    if (k->id < WWENEMY_ANNEX_KINDS && x->ovr[k->id].tg_hit_up) return x->ovr[k->id].tg_hit_up;
    if (k->id == EMK_KIND_TN && AX_U8(WWENEMY_ANNEX_TN_TGUP_OFF)) return AX_U8(WWENEMY_ANNEX_TN_TGUP_OFF);
    if ((up = em_knob_tg_hit_up(x, k)) != 0) return up;
    return k->tg_hit_up;
}

// Executes the hit applier waits on the target's invulnerability.
u8 em_invuln_wait(const em_ext* x, const em_kind* k) {
    u8 w;
    if (k->id < WWENEMY_ANNEX_KINDS && x->invuln_wait[k->id]) return x->invuln_wait[k->id];
    if ((w = em_knob_invuln(x, k)) != 0) return w;
    return k->invuln_wait;
}

static u32 em_fnv_bytes(u32 h, const u8* p, int n) {
    while (n-- > 0) h = EM_FNV(h, *p++);
    return h;
}

// proc BE16, setID BE16, params BE32, home room s8, home.pos x/y/z raw
// BE32 (EMK_PKEY_LEN bytes): the fields as the actor stores them. A keyed
// (setID < 0x200) actor of a row with EMK_CAP_PKEY_NO_POS (KI, D9: a
// wait-type Keese rewrites home.pos on its ceiling return, d_a_ki.cpp
// :675-682) hashes home.pos as twelve zero bytes; an unkeyed one keeps it
// (D10). Go's PKey takes the same rule (EMK_PKEY_TV2_*).
u32 em_pkey(const void* a) {
    const u8* b = (const u8*)a;
    const em_kind* k = em_kind_of_actor(a);
    u32 h = (u32)EMK_PKEY_FNV_OFFSET;
    int n;
    h = em_fnv_bytes(h, b + AC_PROC_NAME, 2);
    h = em_fnv_bytes(h, b + AC_SETID, 2);
    h = em_fnv_bytes(h, b + EMK_AC_PARAMS, 4);
    h = em_fnv_bytes(h, b + EMK_AC_HOME_ROOM, 1);
    if (k && (k->caps & EMK_CAP_PKEY_NO_POS) && AF(a, u16, AC_SETID) < EM_KEYED_SETID_MAX) {
        for (n = 0; n < 12; n++) h = EM_FNV(h, 0);
    } else {
        h = em_fnv_bytes(h, b + EMK_AC_HOME_POS, 12);
    }
    return h ? h : 1;
}

// --- Matrix helpers ------------------------------------------------------------
// m = m * T(x, y, z): MtxTrans(x, y, z, 1) / mDoMtx_stack_c::transM.
static void em_mtx_trans_m(f32 (*m)[4], f32 x, f32 y, f32 z) {
    int r;
    for (r = 0; r < 3; r++) m[r][3] += m[r][0] * x + m[r][1] * y + m[r][2] * z;
}

// m = m * S(x, y, z): mDoMtx_stack_c::scaleM.
static void em_mtx_scale_m(f32 (*m)[4], f32 x, f32 y, f32 z) {
    int r;
    for (r = 0; r < 3; r++) {
        m[r][0] *= x;
        m[r][1] *= y;
        m[r][2] *= z;
    }
}

// m = T(x, y, z): MtxTrans(x, y, z, 0) / mDoMtx_stack_c::transS.
static void em_mtx_trans_s(f32 (*m)[4], f32 x, f32 y, f32 z) {
    int r, c;
    for (r = 0; r < 3; r++)
        for (c = 0; c < 3; c++) m[r][c] = (r == c) ? 1.0f : 0.0f;
    m[0][3] = x;
    m[1][3] = y;
    m[2][3] = z;
}

// The copy model's joint matrix j (getAnmMtx), or NULL.
static void* em_node_mtx(void* model, int j) {
    u32 nodes = AF(model, u32, J3DMODEL_MP_NODE_MTX_OFFSET);
    if (!em_in_mem1(nodes)) return 0;
    return (u8*)nodes + j * EM_MTX_SIZE;
}

// --- Animation helpers (S2) ------------------------------------------------
// getRes for a bck index of the kind's archive: NULL unless the pointer
// is sane. Never cached: a reloaded archive can never leave a stale
// pointer (I1).
static void* em_bck_res(const em_kind* k, u8 idx) {
    void* res = dRes_getRes_byIdx(k->arc, (s32)idx, MOBJECT_INFO, OBJECT_INFO_COUNT);
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

// --- Bokoblin and Darknut: the weapon they carry ----------------------------
// The HeldBy guard (k1.go:304-337; closed_before_build "boko CARRY
// clear"): the copy holds a weapon (the row's weapon word: BK m0B30, TN
// m0C34, != 0) and the row's pid (BK m1200, TN mBokoPcId m1410) names a
// live boko (proc 0x1D5) that is carried (fopAcStts_CARRY_e) by an enemy
// (mNowMode 2 ENEMY_CARRY, d_a_boko.h:128). A stale pid may name a weapon
// on the ground or in Link's hands: never touched. A Darknut whose m140C
// is still set has not attached its sword yet (daTn_Execute does it on
// its first live frame, d_a_tn.cpp:3490-3497; the gate refuses it too).
void* em_carried_boko(void* a, const em_kind* k) {
    void* b;
    u16 pid;
    if (k->attach == EMK_ATTACH_BK) {
        pid = EMK_BK_BOKO_PID;
    } else if (k->attach == EMK_ATTACH_TN) {
        if (AF(a, s8, EMK_TN_M140C) != 0) return 0;
        pid = EMK_TN_BOKO_PID;
    } else {
        return 0;
    }
    if (AF(a, s32, k->weapon) == 0) return 0;
    b = em_actor_of(AF(a, u32, pid));
    if (!b || AF(b, s16, AC_PROC_NAME) != EMK_BOKO_PROC || AF(b, u8, EMK_BOKO_MODE) != EMK_BOKO_MODE_CARRY ||
        !(AF(b, u8, EMK_BOKO_CARRY_BYTE) & EMK_BOKO_CARRY_BIT)) return 0;
    return b;
}

static void* em_bk_boko(void* a) {
    return em_carried_boko(a, &EM_KINDS[EMK_KIND_BK]);
}

// The stick in the hand: nodeMtx[BUKI] * Ry(0x3E80) * T(0, 0, 65) into
// the stick model's base matrix (daBk_Execute's m0B7B == 0 branch with
// the REG8 debug offsets 0, d_a_bk.cpp:4371-4395; daBoko_c::setMatrix,
// d_a_boko.h:87-91).
static void em_bk_hand(void* boko, void* model) {
    f32 m[3][4];
    void* bm = AF(boko, void*, EMK_BOKO_MODEL);
    void* n = em_node_mtx(model, EMK_BK_JOINT_BUKI);
    if (!n || !em_in_mem1((u32)bm)) return;
    PSMTXCopy(n, m);
    mDoMtx_YrotM(m, EMK_BK_HAND_RY);
    em_mtx_trans_m(m, 0.0f, 0.0f, (f32)EMK_BK_HAND_TZ);
    PSMTXCopy(m, (u8*)bm + J3DMODEL_BASE_TR_MTX_OFFSET);
}

// The Darknut's sword in the hand: nodeMtx[ITEM_R1] * Rz(0x8000) * T(0, 0,
// 85) (daTn_Execute with the REG8 debug offsets 0, d_a_tn.cpp:3708-3719;
// its Y and X rotations by REG8_S(1)/(2) are by 0).
static void em_tn_hand(void* boko, void* model) {
    f32 m[3][4];
    void* bm = AF(boko, void*, EMK_BOKO_MODEL);
    void* n = em_node_mtx(model, EMK_TN_JOINT_ITEM_R1);
    if (!n || !em_in_mem1((u32)bm)) return;
    PSMTXCopy(n, m);
    mDoMtx_ZrotM(m, (s16)EMK_TN_HAND_RZ);
    em_mtx_trans_m(m, 0.0f, 0.0f, (f32)EMK_TN_HAND_TZ);
    PSMTXCopy(m, (u8*)bm + J3DMODEL_BASE_TR_MTX_OFFSET);
}

// Let the stick fall: the raw CARRY clear (fopAcM_cancelCarryNow's
// setRoomLayer and shape_angle.z = 0 skipped: the BK attached it with
// setCarryNow(boko, FALSE), d_a_bk.cpp:4219-4227, and procCarry/landing
// rebuild the angle, d_a_boko.cpp:637-708, :548-550). procCarry then
// re-enters procMove with gravity and lands in procWait, a normal ground
// stick (closed_before_build).
static void em_bk_drop(void* boko) {
    AF(boko, u8, EMK_BOKO_CARRY_BYTE) &= (u8)~EMK_BOKO_CARRY_BIT;
}

// --- Batch C: the ambush Bokoblin (ambush_cleanup, R-6..R-9) -----------------
// R-8: where a hidden host Bokoblin keeps its stick: the first execute
// attaches it at MtxTrans(-10000, -10000, 0) (d_a_bk.cpp:4219-4227) and
// every hidden execute returns before the hand matrix (:4230-4281), so the
// copy's stick is parked there too while the slot is SF_HIDDEN instead of
// floating at an undrawn hand. The shield and bouen need nothing: daBk_Draw
// returns while m02BA != 0 (:520-528).
void em_bk_ambush_park(void* boko) {
    f32 m[3][4];
    void* bm = AF(boko, void*, EMK_BOKO_MODEL);
    if (!em_in_mem1((u32)bm)) return;
    em_mtx_trans_s(m, -(f32)EMK_BK_STICK_PARK, -(f32)EMK_BK_STICK_PARK, 0.0f);
    PSMTXCopy(m, (u8*)bm + J3DMODEL_BASE_TR_MTX_OFFSET);
}

// R-9: a copy handed back while still in its pot (its own m02BA 0xFF, set
// at create for mType 2/3, d_a_bk.cpp:5052-5055; a switch-gated hidden BK
// has m02B7 + 1 instead and keeps the create pair) resumes tubo_wait (action
// 15, :3433-3434) at mode 0, whose case 0 sits (SUWARI) while m0300[0] != 0
// and else leaps (mType 3 JATTACK1, 2 JUMP1, :3009-3036). A copy that once
// revealed itself (client-first, then re-hidden by Go) kept that reveal's
// m0300[0] 50, m0310 20 and scale 0.5 (:4240-4249): all reset. m030E is
// left alone (create's 0xA; the reveal needs 0, and the copy's own execute
// counts it down, :4205-4207). Keyed on the copy's own fields, never the
// slot flags: at release the slot may already be pid 0 or RELEASE.
int em_bk_ambush_handback(void* a, const em_kind* k, u8 hkd) {
    u8 t = AF(a, u8, k->gate);
    if ((hkd & EMK_BK_HKD_AMBUSH) || AF(a, u8, EMK_BK_DRAW_GATE) != EMK_BK_DRAW_GATE_POT ||
        (t != EMK_BK_TYPE_AMBUSH_JUMP && t != EMK_BK_TYPE_AMBUSH_LEAP))
        return 0;
    AF(a, s16, k->dr_action) = EMK_BK_ACTION_TUBO;
    AF(a, s16, k->dr_mode) = 0;
    AF(a, s16, EMK_BK_M0300) = 0;
    AF(a, s16, EMK_BK_M0310) = 0;
    AF(a, f32, AC_SCALE + 0) = 1.0f;
    AF(a, f32, AC_SCALE + 4) = 1.0f;
    AF(a, f32, AC_SCALE + 8) = 1.0f;
    return 1;
}

// --- Release ----------------------------------------------------------------
// Hand the copy back to its own code: clear our pause bit (the other bits
// stay as they are now: fpcPause_Disable's leaf case), no momentum, old =
// current so fopAc_Execute's velocity reads zero, then per kind:
//   MO2  JYUNKAI mode -10 so daMo2_Execute waits 60 frames before
//        patrolling; after S2 its own wait loop (wait_set's rows keyed on
//        its weapon and variant, d_a_mo2.cpp:1059-1072), its weapon byte,
//        no blur, and its gate/skip timers cleared (they never decremented
//        while paused, so anm_init would be a no-op for up to 25 frames);
//   BK   the create pair (mType 4: action 1 / mode -1, else 0/0,
//        d_a_bk.cpp:5029-5040), or for a pot ambusher still in its pot
//        (batch C R-9, unless its AMBUSH bit) tubo_wait's (action 15, mode
//        0, m0300[0] / m0310 0, scale 1, d_a_bk.cpp:3009-3036, 5052-5055)
//        with no wait-loop setAnm, so it can leap from where the host's
//        stood; no ragdoll (m488/m48A 0: Bk_move skips its
//        action switch while m48A != 0, :3356-3366), mSpawnY = pos.y
//        (dr_damage_set's fall test runs before dr_body_bg_check refreshes
//        it, c_damagereaction.cpp:1021,1049,1232), the shield off (R -200,
//        m11D8.y -10000: the state damage_check leaves, d_a_bk.cpp
//        :3530-3533) and its three Tg hits cleared (damage_check reads them
//        first on the live frame), no blur, play not skipped; after S2 the
//        WAIT loop (0x51);
//   CC   its own mCyl At/Tg/Co Set bits back, its Tg hit cleared, and after
//        S2 a setAnm of m320 (the last streamed bck) so its anim restarts
//        clean;
//   KI   its Tg and Co spheres' hits cleared, mBehaviorType 0 (each
//        action's case 0 re-runs anm_init and tex_anm_set, d_a_ki.cpp
//        :282-285, 385-391, 505-511, 743-746) unless in FIRE_SET, the
//        wake stare m338 and the head-bank targets m322/m324 cleared;
//   AM   DOUSA_INIT (mAction = mMode = 0: it re-inits SLEEP_LOOP and its
//        up-timers, d_a_am.cpp:494-524), its down-timers, mTargetAngleY =
//        angle.y, the knockback/body-hit bytes, the eye and body hits, the
//        needle's At bit and the Z-target cleared; after S2 SLEEP_LOOP
//        with mCurrBckIdx;
//   AM2  the body, weak and eye hits cleared, m2CE/mbIsWeakBeingHit 0,
//        the body Co on, angle.y = shape.y, mCurrBckIdx = the last setAnm;
//        then the dormant statue its create would pick (mStartsInactive 1
//        with a switch: MAHI 12, stun 600, no Z-target, weak spot off,
//        d_a_am2.cpp:1494-1499) or the awake SLEEP (DOUSA 6, :589-607);
//   TN   JYUNKAI (dr.mAction 0, mMode 0), mSpawnY = pos.y (the generic fall
//        test in dr_damage_set reads it before dr_body_bg_check refreshes
//        it, c_damagereaction.cpp:1049, 1232: a copy carried uphill would
//        die on its first live frame), its Tg, head and defence hits
//        cleared, the parry window m1444, play-skip m03EE and armor wobble
//        m1402 cleared; after S2 the AWAIT1 loop (R-11). No blur words: the
//        row has none (BK's lines would write actor+0);
//   BB   (batch C R-17, em_bb_handback, unless its HANDBACK bit) damage_
//        check's stun recovery: the attack state with its timer seeded, the
//        knockback, bank and head words cleared, the tail re-seeded; never
//        the draw gate 0x2F2 (a hidden copy keeps its own byte3 + 1);
//   MT   (batch C MT-4, batch D R-D6, under EMK_WITH_MT) every sphere's Tg
//        hit cleared and the lunge blur off; when the host curled
//        (EM_XF_MT_BALL) and its CUSTODY bit is clear, the BALL its own
//        execute curls on from here (em_mt_handback), else the last
//        streamed mode and action.
// hkd is em_hkd(x, k); slot the block slot (MT's custody handback reads its
// annex aux and health live).
static void em_handback(void* a, em_slot* sl, const em_ext_slot* xs, const em_kind* k, u8 hkd, int slot) {
    u32* src = (u32*)((u8*)a + AC_CURRENT);
    u32* dst = (u32*)((u8*)a + AC_OLD);
    void* morf = AF(a, void*, k->morf);
    void* res = 0;
    int i;
    AF(a, u8, AC_PAUSE_FLAG) &= (u8)~1u;
    AF(a, f32, AC_SPEED + 0) = 0.0f;
    AF(a, f32, AC_SPEED + 4) = 0.0f;
    AF(a, f32, AC_SPEED + 8) = 0.0f;
    AF(a, f32, AC_SPEEDF) = 0.0f;
    for (i = 0; i < 5; i++) dst[i] = src[i];     // actor_place: pos, angle, roomNo
    switch (k->id) {
    case EMK_KIND_MO2:
        AF(a, s16, MO2_DR_ACTION) = 0;
        AF(a, s16, MO2_DR_MODE) = MO2_MODE_HANDBACK;
        if (sl->hit_withheld) {
            // A Tg hit kept for Go must not look like a fresh hit to the
            // Moblin's own damage_check.
            dCcD_GObjInf_ClrTgHit((u8*)a + MO2_TG_CYL);
            dCcD_GObjInf_ClrTgHit((u8*)a + MO2_HEAD_SPH);
        }
        if (sl->anm_state != WWENEMY_ANM_S1) {
            u8 idx = sl->weapon_prev ? (AF(a, u8, MO2_MODE) == 1 ? MO2_BCK_KWAIT : MO2_BCK_WAIT) : MO2_BCK_NBWAIT;
            res = morf ? em_bck_res(k, idx) : 0;
            if (res) em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
            AF(a, s8, MO2_WEAPON) = (s8)sl->weapon_prev;
            AF(a, u32, MO2_BLUR_ON) = 0;
            AF(a, s16, MO2_ANM_GATE) = 0;
            AF(a, s16, MO2_PLAY_SKIP) = 0;
        }
        break;
    case EMK_KIND_BK: {
        // Batch C R-9: a copy still in its pot gets tubo_wait instead of
        // the create pair (em_bk_ambush_handback, keyed on its own m02BA).
        int amb = em_bk_ambush_handback(a, k, hkd);
        u8 type4 = AF(a, u8, k->gate) == EMK_BK_TYPE_TORCH;
        if (!amb) {
            AF(a, s16, k->dr_action) = type4 ? 1 : 0;
            AF(a, s16, k->dr_mode) = type4 ? -1 : 0;
        }
        AF(a, s16, EMK_BK_DR_M488) = 0;
        AF(a, s16, EMK_BK_DR_M48A) = 0;
        AF(a, f32, EMK_BK_DR_SPAWN_Y) = AF(a, f32, AC_CUR_POS + 4);
        AF(a, f32, EMK_BK_SHIELD_R) = -200.0f;
        AF(a, f32, EMK_BK_SHIELD_CY) = -10000.0f;
        dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->def_sph);
        AF(a, u32, k->blur_on) = 0;
        AF(a, u32, k->blur_shape) = 0;
        AF(a, s16, k->play_skip) = 0;
        // tubo_wait owns the anim of a copy handed back in its pot.
        if (!amb && sl->anm_state != WWENEMY_ANM_S1) {
            res = morf ? em_bck_res(k, k->bck_wait) : 0;
            if (res) em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
        }
        break;
    }
    case EMK_KIND_CC: {
        u8* cyl = (u8*)a + k->tg_cyl;
        AF(cyl, u32, CC_AT_SPRM) = (AF(cyl, u32, CC_AT_SPRM) & ~1u) | ((xs->cc_saved_bits >> 0) & 1u);
        AF(cyl, u32, CC_TG_SPRM) = (AF(cyl, u32, CC_TG_SPRM) & ~1u) | ((xs->cc_saved_bits >> 1) & 1u);
        AF(cyl, u32, CC_CO_SPRM) = (AF(cyl, u32, CC_CO_SPRM) & ~1u) | ((xs->cc_saved_bits >> 2) & 1u);
        dCcD_GObjInf_ClrTgHit(cyl);
        if (sl->anm_state != WWENEMY_ANM_S1) {
            s32 idx = AF(a, s32, EMK_CC_BCK_INDEX);
            if (idx >= k->bck_first && idx <= k->bck_last && morf) res = em_bck_res(k, (u8)idx);
            if (res) em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
        }
        break;
    }
    case EMK_KIND_KI:
        dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->co_cyl);
        if (AF(a, u8, k->gate) != EMK_KI_ACT_FIRE_SET) AF(a, s8, EMK_KI_BEHAVIOR) = 0;
        AF(a, s8, EMK_KI_M338) = 0;
        AF(a, s16, EMK_KI_HEAD_BANK + 0) = 0;
        AF(a, s16, EMK_KI_HEAD_BANK + 2) = 0;
        break;
    case EMK_KIND_AM:
        AF(a, u8, k->gate) = EMK_AM_ACT_DOUSA;
        AF(a, u8, EMK_AM_MODE) = 0;
        for (i = 0; i < EMK_AM_TIMERS_LEN; i++) AF(a, u8, EMK_AM_TIMERS + i) = 0;
        AF(a, s16, EMK_AM_TARGET_ANGLE) = AF(a, s16, AC_CUR_ANGLE_Y);
        AF(a, u8, EMK_AM_HUGE_KB) = 0;
        AF(a, u8, EMK_AM_BODY_HIT) = 0;
        dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
        AF(a, u32, EMK_AM_NEEDLE_CYL + CC_AT_SPRM) &= ~1u;
        AF(a, u32, AC_ATTN_FLAGS) = 0;
        if (sl->anm_state != WWENEMY_ANM_S1) {
            res = morf ? em_bck_res(k, k->bck_wait) : 0;
            if (res) {
                em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
                AF(a, s32, EMK_AM_BCK_INDEX) = k->bck_wait;
            }
        }
        break;
    case EMK_KIND_AM2:
        dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
        dCcD_GObjInf_ClrTgHit((u8*)a + EMK_AM2_EYE_SPH);
        AF(a, u8, EMK_AM2_M2CE) = 0;
        AF(a, u8, EMK_AM2_WEAK_HIT) = 0;
        AF(a, u32, k->co_cyl + CC_CO_SPRM) |= 1u;
        AF(a, s16, AC_CUR_ANGLE_Y) = AF(a, s16, AC_SHAPE_ANGLE_Y);
        if (sl->anm_state != WWENEMY_ANM_S1 && sl->anm_bck >= k->bck_first && sl->anm_bck <= k->bck_last)
            AF(a, s32, EMK_AM2_BCK_INDEX) = sl->anm_bck;
        if (AF(a, u8, EMK_AM2_STARTS_INACTIVE) == 1 && AF(a, u8, EMK_AM2_SWITCH) != 0xFF) {
            AF(a, u8, k->gate) = EMK_AM2_ACT_MAHI;
            AF(a, u8, EMK_AM2_MODE) = EMK_AM2_DORMANT_MODE;
            AF(a, s16, EMK_AM2_STUN_TIMER) = EMK_AM2_DORMANT_STUN;
            AF(a, u32, AC_ATTN_FLAGS) = 0;
            AF(a, u32, k->head_sph + CC_TG_SPRM) &= ~1u;
        } else {
            AF(a, u8, k->gate) = EMK_AM2_ACT_DOUSA;
            AF(a, u8, EMK_AM2_MODE) = EMK_AM2_HANDBACK_MODE;
        }
        break;
    case EMK_KIND_TN:
        AF(a, s16, k->dr_action) = EMK_TN_ACT_JYUNKAI;
        AF(a, s16, k->dr_mode) = 0;
        AF(a, f32, k->dr + EM_DR_SPAWN_Y) = AF(a, f32, AC_CUR_POS + 4);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
        dCcD_GObjInf_ClrTgHit((u8*)a + k->def_sph);
        AF(a, s8, EMK_TN_M1444) = 0;
        AF(a, s16, k->play_skip) = 0;
        AF(a, s16, EMK_TN_M1402) = 0;
        if (sl->anm_state != WWENEMY_ANM_S1) {
            void* sh = AF(a, void*, EMK_TN_SHIELD_MORF);
            res = morf ? em_bck_res(k, k->bck_wait) : 0;
            if (res) em_set_anm(morf, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
            // The shield morf may hold a guard's last frame (em_tn_shield):
            // back to the ATATE_ON1 loop wait_set's callers give it
            // (d_a_tn.cpp:1939, 3458).
            if ((AF(a, s8, EMK_TN_PIECES) & EMK_TN_PIECE_SHIELD) && em_in_mem1((u32)sh) &&
                (res = em_bck_res(k, EMK_TN_BCK_TATE_ON)) != 0)
                em_set_anm(sh, res, MORF_ATTR_LOOP, EM_HANDBACK_MORF, 1.0f);
        }
        break;
    case EMK_KIND_BB:
        if (!(hkd & EMK_BB_HKD_HANDBACK)) em_bb_handback(a);
        break;
#if EMK_WITH_MT
    case EMK_KIND_MT:
        // xs is em_ext's own (never a const object): the custody bit is
        // the copy's, kept past the release (em_mt_custody_watch). Only
        // while the slot still names the copy: on a reassignment em_consume
        // has already put the next actor's mode, aux and health there.
        em_mt_handback(a, (em_ext_slot*)xs, hkd, slot, sl->apid == sl->pid);
        break;
#endif
    }
    (void)slot;
    sl->anm_state = WWENEMY_ANM_S1;
}

// A quiet-dying slot (em_die_quiet): adopted, DYING and SF2_QUIET (kept
// sticky by em_consume while it dies). Never handed back.
static int em_quiet_dying(const em_slot* sl) {
    return sl->adopted && sl->state == WWENEMY_STATE_DYING && (sl->tgt_flags2 & WWENEMY_SF2_QUIET);
}

// The end of a quiet death: the copy is gone (or going), the slot reports
// STATE_DEAD for Go and latches for EM_DEAD_LATCH frames. apid is kept:
// unadopted, it names the dead pid the latch belongs to (em_slot_frame
// drops the latch as soon as Go names another actor, so STATE_DEAD is
// never published next to a pid the REL did not kill).
static void em_dead(em_slot* sl) {
    sl->adopted = 0;
    sl->state = WWENEMY_STATE_DEAD;
    sl->recently_dead = EM_DEAD_LATCH;
}

static void em_base_matrix(em_slot* sl, const em_kind* k, void* a, void* morf, f32 scale, u8 hkd);

// Every release path (pid change, pid 0, RELEASE, scope, epoch,
// watchdog, delete, unlink) comes through here: a living copy is handed
// back, a quiet-dying one is deleted (fopAcM_delete only queues it, so
// this is safe from _epilog too) and reports STATE_DEAD. A quiet-dying
// Bokoblin or Darknut lets its weapon go first, as em_die_quiet's last
// frame does (closed_before_build "boko CARRY clear"): daBk_Delete and
// daTn_Delete never cancel the carry and procCarry only reads back a base
// matrix nobody writes any more (d_a_bk.cpp:4509-4524, d_a_tn.cpp
// :3744-3784, d_a_boko.cpp:638-642), so the weapon would hang in mid-air.
// A quiet-dying Magtail with its respawn bit is marked killed first (batch
// C MT-5, em_mt_mark_respawn, under EMK_WITH_MT), as both of its own
// delete paths do.
static void em_release(em_slot* sl, const em_ext* x, const em_ext_slot* xs) {
    if (sl->adopted) {
        void* a = em_actor_of(sl->apid);
        const em_kind* k = a ? em_kind_of_actor(a) : 0;
        if (em_quiet_dying(sl)) {
            if (k) em_quiet_drop(sl, k, a, AF(a, void*, k->morf), em_hkd(x, k));
#if EMK_WITH_MT
            if (k && k->id == EMK_KIND_MT)
                em_mt_mark_respawn(a, em_hkd(x, k));
#endif
            if (k) fopAcM_delete_id(sl->apid);
            em_dead(sl);                         // keeps apid (the latch's pid)
        } else {
            if (k) em_handback(a, sl, xs, k, em_hkd(x, k), (int)(xs - x->slots));
            sl->adopted = 0;
            sl->state = WWENEMY_STATE_RELEASED;
            sl->apid = 0;
        }
    }
    sl->actor = 0;
    sl->hit_withheld = 0;
    sl->hit_cooldown = 0;
    sl->anm_state = WWENEMY_ANM_S1;
}

// forget: also drop the dying latches (an epoch change starts over). A
// quiet-dying copy is deleted by em_release before the reset.
static void em_release_all(enemy_mirror_state* s, em_ext* x, int forget) {
    int i;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &s->slots[i];
        em_release(sl, x, &x->slots[i]);
        if (forget) {
            sl->recently_dead = 0;
            sl->apid = 0;
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
        // Under a dying latch the pid is the dead one (apid), never Go's
        // current one: a latch held over pid 0 (Go disarmed: a count-0 block)
        // still names the actor that died, which Go's unpin matches.
        EB_U32(off + WWENEMY_ST_PID_OFF) = sl->recently_dead ? sl->apid : sl->pid;
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

// The consumed slot's values that go straight onto a copy this mirror
// already owns (adopted before this consume, same pid, same proc: it is
// paused and was validated, so its gates cannot have changed): BK's tilt z
// and jaw and CC's shape.x and wobble from the annex (only when annex_seq
// matched), CC's ground tilt m316.x/z from the slot (raw, no smoothing);
// KI's shape.x/z (its EMK_MTX_PLAIN tilt) and m326/m328, AM's mEyeRot.x/y
// from the annex (R-6; AM2 streams none), TN's dr.m48C.z from the annex
// (aux[0], as BK); batch C: BB's current.angle.z from the slot (R-12, the CC
// m316.z bytes, raw as CC's); batch D: MT's wiggle from the slot and its
// stream from the annex (em_mt_consume, under EMK_WITH_MT). A
// fresh adopt gets them one consume later. MO2 reads nothing here.
static void em_consume_owned(em_slot* sl, const em_kind* k, void* a, u32 off, u32 aux, int annex, u8 hkd) {
    switch (k->id) {
    case EMK_KIND_BK:
        if (annex) {
            AF(a, s16, EMK_BK_TILT_Z) = AX_S16(aux + 0);
            AF(a, s16, EMK_BK_JAW) = AX_S16(aux + 2);
        }
        break;
    case EMK_KIND_CC:
        AF(a, s16, EMK_CC_TILT) = sl->tgt_tilt;
        AF(a, s16, EMK_CC_TILT_Z) = EB_S16(off + WWENEMY_SL_CC_M316Z_OFF);
        if (annex) {
            AF(a, s16, EMK_AC_SHAPE_X) = AX_S16(aux + 0);
            AF(a, s16, EMK_CC_WOBBLE + 0) = AX_S16(aux + 2);
            AF(a, s16, EMK_CC_WOBBLE + 2) = AX_S16(aux + 4);
            AF(a, s16, EMK_CC_WOBBLE + 4) = AX_S16(aux + 6);
        }
        break;
    case EMK_KIND_KI:
        if (annex) {
            AF(a, s16, EMK_AC_SHAPE_X) = AX_S16(aux + 0);
            AF(a, s16, EMK_AC_SHAPE_Z) = AX_S16(aux + 2);
            AF(a, s16, EMK_KI_AUX2) = AX_S16(aux + 4);
            AF(a, s16, EMK_KI_AUX3) = AX_S16(aux + 6);
        }
        break;
    case EMK_KIND_AM:
        if (annex) {
            AF(a, s16, EMK_AM_EYE_ROT) = AX_S16(aux + 0);
            AF(a, s16, EMK_AM_EYE_ROT_Y) = AX_S16(aux + 2);
        }
        break;
    case EMK_KIND_TN:
        // dr.m48C.z, the ground roll dr_matrix_set reads (as BK's; its x
        // is the slot tilt).
        if (annex) AF(a, s16, EMK_TN_TILT_Z) = AX_S16(aux + 0);
        break;
    case EMK_KIND_BB:
        AF(a, s16, EMK_AC_ANGLE_Z) = EB_S16(off + WWENEMY_SL_CC_M316Z_OFF);
        break;
#if EMK_WITH_MT
    case EMK_KIND_MT:
        em_mt_consume(sl, a, off, aux, annex, hkd);
        break;
#endif
    }
    (void)hkd;
}

static void em_consume(enemy_mirror_state* s, em_ext* x, u32 seq, u32 frame) {
    int i;
    u8 count = EB_U8(WWENEMY_COUNT_OFF);
    // Go writes the annex first in the same span, with annex_seq = seq: a
    // match means it belongs to this block.
    int annex = AX_U32(WWENEMY_ANNEX_SEQ_OFF) == seq;
    if (count > WWENEMY_MAX_SLOTS) count = WWENEMY_MAX_SLOTS;
    s->count = count;
    s->flags = EB_U8(WWENEMY_FLAGS_OFF);
    s->role = EB_U8(WWENEMY_ROLE_OFF);
    s->room = EB_S8(WWENEMY_ROOM_OFF);
    for (i = 0; i < 8; i++) s->stage[i] = EB_U8(WWENEMY_STAGE_OFF + i);
    x->kind_mask = (u16)EB_U32(WWENEMY_KIND_MASK_OFF);   // layout 4 (D5): ids < 16
    if (annex) {
        // The overrides are kept from the last matching annex otherwise.
        for (i = 0; i < WWENEMY_ANNEX_KINDS; i++) {
            u32 o = WWENEMY_ANNEX_OVR_OFF + i * WWENEMY_ANNEX_OVR_SIZE;
            x->ovr[i].hook_disable = AX_U8(o + WWENEMY_ANNEX_OVR_HOOKS_OFF);
            x->ovr[i].quiet_frames = AX_U8(o + WWENEMY_ANNEX_OVR_QUIET_OFF);
            x->ovr[i].tg_hit_up = AX_S16(o + WWENEMY_ANNEX_OVR_TG_UP_OFF);
            x->invuln_wait[i] = AX_U8(WWENEMY_ANNEX_INVW_OFF + i);
        }
    }
    // Layout 4 (R-3): the knob table, judged at every consume (its own gen
    // words, not annex_seq: Go writes it separately and only on a change).
    em_knob_validate(x);
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) {
        em_slot* sl = &s->slots[i];
        em_ext_slot* xs = &x->slots[i];
        u32 off = WWENEMY_SLOTS_OFF + i * WWENEMY_SLOT_SIZE;
        const em_kind* k;
        u8 f2;
        if (i >= count) {
            sl->pid = 0;
#if EMK_WITH_MT
            xs->kind_bits &= (u8)~EM_XB_MT_CUSTODY;      // MT-4: custody is one pid's
#endif
            xs->proc = 0;
            xs->kind = EMK_KIND_NONE;
            xs->flags_ext = 0;
            continue;
        }
#if EMK_WITH_MT
        {
            // Batch C MT-4: the custody bit belongs to the pid the slot
            // named at the handback; a new pid (Go's distance selection
            // reassigns slots) never inherits the watch.
            u32 pid = EB_U32(off + WWENEMY_SL_PID_OFF);
            if (pid != sl->pid) xs->kind_bits &= (u8)~EM_XB_MT_CUSTODY;
            sl->pid = pid;
        }
#else
        sl->pid = EB_U32(off + WWENEMY_SL_PID_OFF);
#endif
        sl->tgt_flags = EB_U8(off + WWENEMY_SL_FLAGS_OFF);
        xs->proc = EB_U16(off + WWENEMY_SL_PROC_OFF);
        k = em_kind_of(xs->proc);
        xs->kind = k ? k->id : EMK_KIND_NONE;
        {
            f32 x0 = EB_F32(off + WWENEMY_SL_POS_OFF);
            f32 y0 = EB_F32(off + WWENEMY_SL_POS_OFF + 4);
            f32 z0 = EB_F32(off + WWENEMY_SL_POS_OFF + 8);
            if (em_f32_bad(x0) || em_f32_bad(y0) || em_f32_bad(z0)) {
                sl->last_err = WWENEMY_ERR_RANGE;      // keep the previous target
            } else {
#if EMK_WITH_MT
                // Batch D D9: an MT target that moved since the last consume
                // (position or angle). em_mt_body_step holds the ring
                // (EMK_MT_DIVE 1) on every frame without one, so a stream
                // gap or Go's hold never compresses the body.
                if (k && k->id == EMK_KIND_MT &&
                    (x0 != sl->tgt.x || y0 != sl->tgt.y || z0 != sl->tgt.z ||
                     EB_S16(off + WWENEMY_SL_ANGLE_OFF) != sl->tgt_angle))
                    xs->kind_bits |= EM_XB_MT_FRESH;
#endif
                sl->tgt.x = x0;
                sl->tgt.y = y0;
                sl->tgt.z = z0;
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
        // A quiet death, once started, stays quiet whatever Go sends next:
        // the release paths must delete the copy, never hand it back.
        f2 = EB_U8(off + WWENEMY_SL_FLAGS2_OFF);
        if (em_quiet_dying(sl)) f2 |= WWENEMY_SF2_QUIET;
        sl->tgt_flags2 = f2;
#if EMK_WITH_MT
        {
            // Batch C MT-4: the host's Magtail is curled (aux[0]'s mode
            // byte); a consume without a matching annex keeps the last word.
            u8 ball = xs->flags_ext & EM_XF_MT_BALL;
            xs->flags_ext = 0;
            if (k && k->id == EMK_KIND_MT)
                xs->flags_ext = annex ? ((u8)AX_S16(WWENEMY_ANNEX_OFF + i * WWENEMY_ANNEX_AUX_SIZE) == EMK_MT_MODE_BALL
                                             ? EM_XF_MT_BALL : 0)
                                      : ball;
        }
#else
        xs->flags_ext = 0;
#endif
        // The row's attention bit (D4: MO2 none, BK 0x08, CC 0x20, KI/AM/AM2 0x01).
        if (k && k->attn_f2 && (f2 & k->attn_f2)) xs->flags_ext |= EM_XF_ATTN;
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
        // The kind and the key (identity.rel_side): a slot whose proc has
        // no row or whose kind is off is empty (ERR_KIND); an actor of the
        // slot's proc whose placement key is not the one Go matched is not
        // the actor Go meant (a reused pid, an unkeyed twin): empty too
        // (ERR_KEY). An actor of another proc is em_validate's ERR_KIND.
        if (sl->pid != 0) {
            void* a = 0;
            if (!k || !(x->kind_mask & (1u << k->id))) {
                sl->pid = 0;
                sl->last_err = WWENEMY_ERR_KIND;
                if (!sl->recently_dead) sl->state = WWENEMY_STATE_REFUSED;   // a DYING/DEAD latch keeps its state for Go
            } else {
                a = em_actor_of(sl->pid);
                if (a && (u16)AF(a, s16, AC_PROC_NAME) == xs->proc &&
                    em_pkey(a) != EB_U32(off + WWENEMY_SL_PKEY_OFF)) {
                    sl->pid = 0;
                    sl->last_err = WWENEMY_ERR_KEY;
                    if (!sl->recently_dead) sl->state = WWENEMY_STATE_REFUSED;
                }
            }
            if (sl->pid != 0 && a && sl->adopted && sl->apid == sl->pid &&
                (u16)AF(a, s16, AC_PROC_NAME) == xs->proc)
                em_consume_owned(sl, k, a, off, WWENEMY_ANNEX_OFF + i * WWENEMY_ANNEX_AUX_SIZE, annex, em_hkd(x, k));
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
            f32 x0 = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF);
            f32 y0 = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF + 4);
            f32 z0 = EB_F32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_POS_OFF + 8);
            if (!em_f32_bad(x0) && !em_f32_bad(y0) && !em_f32_bad(z0)) {
                s->ha_pos.x = x0;
                s->ha_pos.y = y0;
                s->ha_pos.z = z0;
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
// The static/runtime gates of a Bokoblin (identity.per_kind BK_0xBE,
// closed_before_build item 1): a live type the mirror knows (0, 1, 4 and,
// batch C R-6, the pot ambushers 2 and 3, unconditionally: no hook bit
// refuses them, D8, since Go no longer writes mType 0 into its copy; 6
// rewrites home, 9 runners and 5 riders are refused by Go's nibble), no
// demo, its stick already attached (the first execute has run,
// d_a_bk.cpp:4219-4227; a hidden ambusher attaches it before its m02BA
// return, :4230-4281), not in the carry-drop actions (19, 31), no ragdoll
// in progress (so the create-pair handback is safe).
static int em_bk_gate(void* a, const em_kind* k) {
    u8 t = AF(a, u8, k->gate);
    s16 act = AF(a, s16, k->dr_action);
    if (t > EMK_BK_TYPE_TORCH) return 0;
    if (AF(a, s8, EMK_BK_DEMO_GATE) != 0 || AF(a, s8, EMK_BK_BOKO_ATTACH) != 0) return 0;
    if (act == EMK_BK_ACTION_REFUSE_A || act == EMK_BK_ACTION_REFUSE_B) return 0;
    if (AF(a, s16, k->dr_mode) <= EM_DR_RAGDOLL || AF(a, s16, EMK_BK_DR_M488) != 0) return 0;
    return 1;
}

// A ChuChu (identity.per_kind CC_0xCF, type_table): behaviour 0/1/3,
// colour 0/1/2/4, not insta-kill, split or stone, and not in actions 5/6/8.
static int em_cc_gate(void* a, const em_kind* k) {
    u8 b = AF(a, u8, k->gate);
    u8 c = AF(a, u8, EMK_CC_COLOR);
    u8 act = AF(a, u8, EMK_CC_ACTION);
    if (b != 0 && b != 1 && b != 3) return 0;
    if (c != 0 && c != 1 && c != 2 && c != 4) return 0;
    if (AF(a, u8, EMK_CC_INSTA_KILL) != 0 || AF(a, u8, EMK_CC_SPLIT) != 0 || AF(a, u8, EMK_CC_STONE) != 0) return 0;
    if (act == 5 || act == 6 || act == 8) return 0;
    return 1;
}

// The allow rows' gates (R-8, D10, D16): a Keese not in FIRE_SET or FAIL
// (DYING on the host), not fading out (m2DC: the scale change MTX_PLAIN
// does not stream, d_a_ki.cpp:961-967), and unkeyed only as a fly type
// (the Puppet Ganon swarms spawn type 3, d_a_bgn.cpp:3343); an Armos
// Knight not in the swallow sequence (ITAI mode >= 44); an Armos not
// carried, not in MODORU (it scales, d_a_am2.cpp:1053-1085), not falling
// into an abyss and not in the death dance (ITAI mode >= 22).
static int em_allow_gate(void* a, const em_kind* k, u16 setid) {
    u8 act = AF(a, u8, k->gate);
    switch (k->id) {
    case EMK_KIND_KI:
        if (act == EMK_KI_ACT_FIRE_SET || act == EMK_KI_ACT_FAIL || AF(a, u8, EMK_KI_SHRINK_DELETE) != 0) return 0;
        if (setid == EM_UNKEYED_SETID && AF(a, u8, EMK_KI_PARAMS) != EMK_KI_TYPE_FLY) return 0;
        return 1;
    case EMK_KIND_AM:
        return !(act == EMK_AM_ACT_ITAI && AF(a, u8, EMK_AM_MODE) >= EMK_AM_MODE_SWALLOW);
    case EMK_KIND_AM2:
        if ((AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY) || act == EMK_AM2_ACT_MODORU ||
            AF(a, u8, EMK_AM2_ABYSS) != 0)
            return 0;
        return !(act == EMK_AM2_ACT_ITAI && AF(a, u8, EMK_AM2_MODE) >= EMK_AM2_MODE_DANCE);
    }
    return 1;
}

// R-4, a Darknut (D3, D5; the TN batch's gate): no BOSS status (death
// switch 1..0x7F and behaviours 13/14, d_a_tn.cpp:4211-4215, 4244-4245),
// not frozen in time (behaviour 15), not the statue or the demo (dr.mAction
// 23 / 25: it adopts once awake, :2331, 2536), not hidden (m02C1, m1518:
// Draw returns early and the sword and cape are parked, :437-439,
// 3499-3522), no demo camera (m14F8, :2551-2575), its sword attached
// (m140C, :3490-3497), no cape (mEquipmentType >= 4: daMant is an
// undecompiled stub, D5), and, as em_bk_gate, no ragdoll and not dying
// (dr.mAction 20..22). em_validate re-runs this every frame on an adopted
// copy, but on the copy's own fields, frozen under the pause: no TN hook
// writes dr.mMode/mAction (only em_handback, at release) or the gate's
// bytes, and no term reads health (em_die_quiet zeroes it), so a host
// ragdoll or death never releases a copy (p0_findings DEATH ORDER: it
// refuses adoption only).
int em_tn_gate(void* a, const em_kind* k) {
    s16 act = AF(a, s16, k->dr_action);
    if (AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_BOSS) return 0;
    if (AF(a, u8, k->gate) == EMK_TN_BEHAVIOR_FROZEN) return 0;
    if (act == EMK_TN_ACT_D_DOZOU || act == EMK_TN_ACT_S_DEMO) return 0;
    if (AF(a, u8, EMK_TN_M02C1) != 0 || AF(a, s8, EMK_TN_M1518) != 0 || AF(a, s8, EMK_TN_M14F8) != 0 ||
        AF(a, s8, EMK_TN_M140C) != 0) return 0;
    if (AF(a, u8, EMK_TN_EQUIP) >= EMK_TN_EQUIP_CAPE) return 0;
    if (AF(a, s16, k->dr_mode) <= EM_DR_RAGDOLL || (act >= EMK_TN_ACT_DIE_A && act <= EMK_TN_ACT_DIE_C)) return 0;
    return 1;
}

// WWENEMY_ERR_NONE or the first failing check, in the design's order
// (rel_changes em_validate): kind, initState, setID + home room, the
// Moblin's mode, the kind's gates, the morf and its model.
static u8 em_validate(const em_ext* x, const em_ext_slot* xs, const em_slot* sl, const em_kind* k, void* a, s8 room) {
    void* morf;
    void* model;
    u16 setid;
    if (!k || (u16)AF(a, s16, AC_PROC_NAME) != xs->proc || !(x->kind_mask & (1u << k->id))) return WWENEMY_ERR_KIND;
    if (AF(a, s8, AC_INIT_STATE) != 2) return WWENEMY_ERR_INIT;
    setid = AF(a, u16, AC_SETID);
    if ((setid >= EM_KEYED_SETID_MAX && setid != EM_UNKEYED_SETID) || AF(a, s8, AC_HOME_ROOM) != room) return WWENEMY_ERR_SETID;
    switch (k->id) {
    case EMK_KIND_MO2: {
        u8 mode = AF(a, u8, MO2_MODE);
        if (mode == MO2_MODE_CARRIED || mode == MO2_MODE_STATUE) return WWENEMY_ERR_MODE;
        break;
    }
    case EMK_KIND_BK:
        if (!em_bk_gate(a, k)) return WWENEMY_ERR_GATE;
        break;
    case EMK_KIND_CC:
        if (!em_cc_gate(a, k)) return WWENEMY_ERR_GATE;
        break;
    case EMK_KIND_KI:
    case EMK_KIND_AM:
    case EMK_KIND_AM2:
        if (!em_allow_gate(a, k, setid)) return WWENEMY_ERR_GATE;
        break;
    case EMK_KIND_TN:
        if (!em_tn_gate(a, k)) return WWENEMY_ERR_GATE;
        break;
    case EMK_KIND_BB:
        if (!em_bb_gate(sl, a, k)) return WWENEMY_ERR_GATE;
        break;
    case EMK_KIND_MT:
        // MT-4: a curled host Magtail is the client's own custody (a
        // copy is handed back on that edge, em_handback), unless its
        // CUSTODY bit keeps the BALL mirrored and paused.
        if (!em_mt_gate(a, k) || ((xs->flags_ext & EM_XF_MT_BALL) && !(em_hkd(x, k) & EMK_MT_HKD_CUSTODY)))
            return WWENEMY_ERR_GATE;
        break;
    }
    morf = AF(a, void*, k->morf);
    if (!morf) return WWENEMY_ERR_MORF;
    model = AF(morf, void*, MORF_MODEL);
    if (!model) return WWENEMY_ERR_MORF;
    // CC's and KI's executes NULL actor.model and their Draws set it back
    // (d_a_cc.cpp:171, 2093; d_a_ki.cpp:209, 949), AM never writes it: no
    // MODEL_EQ there (of the allow rows only AM2 has it, d_a_am2.cpp:1331).
    if ((k->caps & EMK_CAP_MODEL_EQ) && !(em_hkd(x, k) & WWENEMY_HKD_MODEL_EQ) && model != AF(a, void*, AC_MODEL))
        return WWENEMY_ERR_MORF;
    return WWENEMY_ERR_NONE;
}

// R-5, em_adopt's TN tail: what the paused copy's own execute would decay
// and never will (d_a_tn.cpp:3480-3482, 3293-3298 the m1402 armor wobble;
// the damagereaction joint offsets m088 the body nodeCallBack rotates
// every joint by, :276-281), the parry window m1444 and the defence
// sphere's R 70 (damage_check sets it every defence frame, :2831); unless
// EMK_TN_HKD_ADOPT_ANM, the armor morf on the YKIHON1 loop, free-running
// from here (yoroi_anm_init's, which skips a broken armor, :108-114).
void em_tn_adopt(void* a, const em_kind* k, u8 hkd) {
    void* armor = AF(a, void*, EMK_TN_ARMOR_MORF);
    void* res;
    int n;
    AF(a, s16, EMK_TN_M1402) = 0;
    for (n = 0; n < EMK_TN_DR_M088_LEN; n += 4) AF(a, u32, EMK_TN_DR_M088 + n) = 0;
    AF(a, s8, EMK_TN_M1444) = 0;
    AF(a, f32, EMK_TN_DEF_R) = (f32)EMK_TN_DEF_R_VAL;
    if (!(hkd & EMK_TN_HKD_ADOPT_ANM) && (AF(a, s8, EMK_TN_PIECES) & EMK_TN_PIECE_ARMOR) && em_in_mem1((u32)armor) &&
        em_in_mem1(AF(armor, u32, MORF_MODEL)) && (res = em_bck_res(k, EMK_TN_BCK_ARMOR_KIHON)) != 0)
        em_set_anm(armor, res, MORF_ATTR_LOOP, 0.0f, 1.0f);
}

// Adopt a validated actor, or WWENEMY_ERR_ICE (not paused) when its own
// enemy_ice is already running (light shrink or freeze timer): K1 or a
// local death got there first and owns it (design D7). Its own code, not
// ERR_GATE: Go's unpin takes it as final, while em_validate's gates pass
// again a moment later.
static u8 em_adopt(enemy_mirror_state* s, em_slot* sl, em_ext_slot* xs, const em_kind* k, void* a, int i, u8 hkd) {
    u32 off = WWENEMY_HITS_OFF + i * WWENEMY_HIT_SIZE;
    u8 pb;
    // A row without enemyice (ice 0, none today) has no timers to read (D12).
    if (k->ice && (AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0 || AF(a, s16, k->ice + EMK_ICE_FREEZE) != 0))
        return WWENEMY_ERR_ICE;
    pb = AF(a, u8, AC_PAUSE_FLAG);
    sl->pause_prev = pb;                 // bit 1 already set here = an orphan of an earlier unlink
    AF(a, u8, AC_PAUSE_FLAG) = pb | 1;
    sl->cur.x = AF(a, f32, AC_CUR_POS + 0);
    sl->cur.y = AF(a, f32, AC_CUR_POS + 4);
    sl->cur.z = AF(a, f32, AC_CUR_POS + 8);
    // The slot angle of an EMK_MTX_PLAIN row is shape.y (D13), which a
    // flying Keese, a knocked-back Armos Knight (d_a_am.cpp:777-784) or a
    // dancing Armos holds apart from current.y; a tombstone's quiet death
    // keeps this seed (em_slot_frame), so it starts facing where it stood.
    // BB's EMK_MTX_TRYXZ reads current.angle (batch C R-11: its shape.y is
    // never written after create), so it takes the current.y seed, and with
    // EMK_CAP_TILT_CUR its tilt seed is current.angle.x. MT's segments read
    // shape_angle (body_control1, mt.rel 0x1F68-0x1F78).
    sl->cur_angle = AF(a, s16, (k->mtx == EMK_MTX_PLAIN || k->mtx == EMK_MTX_MT) ? AC_SHAPE_ANGLE_Y : AC_CUR_ANGLE_Y);
    sl->cur_tilt = (k->caps & (EMK_CAP_DR | EMK_CAP_TILT_CUR)) ? AF(a, s16, k->tilt) : 0;
    AF(a, f32, AC_SPEED + 0) = 0.0f;
    AF(a, f32, AC_SPEED + 4) = 0.0f;
    AF(a, f32, AC_SPEED + 8) = 0.0f;
    AF(a, f32, AC_SPEEDF) = 0.0f;
    // A damagereaction kind's dr.mpEnemy, as its execute would set it every
    // frame (d_a_bk.cpp:4300, d_a_mo2.cpp:3501; EM_DR_MPENEMY): a hidden
    // copy (BK m02BA, MO2 m02C1) never reached that line, and paused it never
    // will, so em_base_matrix's dr_matrix_set would read through NULL.
    if (k->caps & EMK_CAP_DR) AF(a, u32, k->dr + EM_DR_MPENEMY) = (u32)a;
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
    sl->weapon_prev = 0;
    xs->cc_saved_bits = 0;
    xs->kind_bits = 0;
    switch (k->id) {
    case EMK_KIND_MO2:
        sl->weapon_prev = (u8)AF(a, s8, MO2_WEAPON);
        break;
    case EMK_KIND_CC: {
        // Its own mCyl Set bits, restored at handback (em_colliders writes
        // At off and Tg/Co from the host while mirrored).
        u8* cyl = (u8*)a + k->tg_cyl;
        xs->cc_saved_bits = (u8)((AF(cyl, u32, CC_AT_SPRM) & 1u) | ((AF(cyl, u32, CC_TG_SPRM) & 1u) << 1) |
                                 ((AF(cyl, u32, CC_CO_SPRM) & 1u) << 2));
        break;
    }
    case EMK_KIND_AM:
        // The needle is the host's At (d_a_am.cpp:1074-1078); the copy never
        // Sets it, and its At bit stays off while mirrored (R-5).
        AF(a, u32, EMK_AM_NEEDLE_CYL + CC_AT_SPRM) &= ~1u;
        // The mouth smoke DOUSA mode 7 starts; mode 8's timer removes it
        // (d_a_am.cpp:623-643, 655-666), and paused that timer never runs,
        // nor does the handback's mode 0 reach it.
        dPa_smokeEcallBack_end((u8*)a + EMK_AM_SMOKE_MOUTH);
        break;
    case EMK_KIND_TN:
        em_tn_adopt(a, k, hkd);
        break;
    case EMK_KIND_BB:
        em_bb_adopt(sl, xs, a);
        break;
    case EMK_KIND_MT:
        em_mt_adopt(a, sl->tgt.x - sl->cur.x, sl->tgt.y - sl->cur.y, sl->tgt.z - sl->cur.z);
        break;
    }
    // A clean hit entry and no stale Tg hit from the copy's own last frame.
    EB_U16(off + 0) = sl->hit_seq;
    EB_U8(off + 2) = 0;
    EB_U8(off + 3) = 0;
    if (k->tg_cyl) dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
    if (k->head_sph) dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
    (void)s;
    return WWENEMY_ERR_NONE;
}

// --- Death ----------------------------------------------------------------
// The legacy (K1) kill: release, then health 0 and the light-arrow shrink
// (enemy_ice runs at the top of the copy's execute, d_a_mo2.cpp:3379-3388,
// and c_damagereaction.cpp:117-168 shrinks, drops the item, sets the
// death switch and deletes the actor). The latch keeps later samples from
// adopting the shrinking copy. A dying copy never resumes its AI, so it
// keeps its last mirrored pose and shapes while it shrinks (enemy_ice
// plays the current morf at speed 3, d_a_mo2.cpp:3379-3387): the S2 part
// of em_handback (the wait-loop setAnm and the weapon restore) is skipped,
// only its blur/gate/skip clears are kept. Only a row with enemyice comes
// here (the death select sends ice 0 to the quiet path, D12).
static void em_die(em_slot* sl, const em_ext* x, const em_ext_slot* xs, const em_kind* k, void* a) {
    if (sl->anm_state != WWENEMY_ANM_S1) {
        if (k->blur_on) AF(a, u32, k->blur_on) = 0;
        if (k->anm_gate) AF(a, s16, k->anm_gate) = 0;
        if (k->play_skip) AF(a, s16, k->play_skip) = 0;
        sl->anm_state = WWENEMY_ANM_S1;
    }
    em_release(sl, x, xs);
    AF(a, s8, AC_HEALTH) = 0;
    if (k->ice) AF(a, s8, k->ice + EMK_ICE_SHRINK) = 1;
    AF(a, u32, AC_ATTN_FLAGS) = 0;
    sl->state = WWENEMY_STATE_DYING;
    sl->recently_dead = EM_DEAD_LATCH;
    sl->apid = sl->pid;                  // unadopted: the latch's pid, as em_dead keeps it
}

// The quiet death (death_and_drops.quiet_steps, design D7): the copy stays
// paused (its execute, where every drop site is, never runs again), so it
// never spawns an item or sets a switch. The first frame takes health and
// the Z-target away and marks the slot DYING (SF2_QUIET kept); every frame
// counts hit_cooldown up (no collider runs while dying, so em_hits never
// counts it down) and returns the base-matrix scale: 1 for the first H
// frames (em_quiet_hold: TN's hold, else 0; a death that starts RELEASE
// starts its count at H), then 1 - (f - H)/(N - H), so
// with H 0 it is 1 - f/N as before; at f == N the copy is deleted
// (fopAcM_delete), STATE_DEAD, and 0 is returned. N is em_quiet_frames'
// (the annex's quiet_frames for ids < 3, else EMK_QUIET_FRAMES_LIGHT when
// the host's own death was enemy_ice's (SF2_LIGHT), else the row's compiled
// count). A Bokoblin's carried stick or a Darknut's sword is let go first
// (em_quiet_drop). apid stays the dead pid (em_dead).
static int em_die_quiet(em_slot* sl, const em_ext* x, const em_ext_slot* xs, const em_kind* k, void* a,
                        f32* scale) {
    u8 n = em_quiet_frames(x, k, sl->tgt_flags2);
    u8 h = em_quiet_hold(k, sl->tgt_flags2);
    (void)xs;
    if (sl->state != WWENEMY_STATE_DYING) {
        AF(a, s8, AC_HEALTH) = 0;
        AF(a, u32, AC_ATTN_FLAGS) = 0;
        sl->state = WWENEMY_STATE_DYING;
        sl->tgt_flags2 |= WWENEMY_SF2_QUIET;
        // A death that starts RELEASE (a catch-up, or a copy first adopted
        // from a tombstone) streams no host ragdoll for the hold to show:
        // the count starts past it, so the copy shrinks at once as the
        // other rows do. hit_cooldown is the latch: a later RELEASE (the
        // host's delete after action 20) leaves a running hold alone.
        sl->hit_cooldown = (sl->tgt_flags & WWENEMY_SF_RELEASE) ? h : 0;
    }
    if (sl->hit_cooldown != 0xFF) sl->hit_cooldown++;
    if (sl->hit_cooldown < n) {
        *scale = sl->hit_cooldown < h ? 1.0f : 1.0f - (f32)(sl->hit_cooldown - h) / (f32)(n - h);
        return 1;
    }
    em_quiet_drop(sl, k, a, AF(a, void*, k->morf), em_hkd(x, k));
    if (k->id == EMK_KIND_MT)
        em_mt_mark_respawn(a, em_hkd(x, k));      // MT-5: before the delete, as em_release
    fopAcM_delete_id(sl->apid);
    em_dead(sl);
    sl->actor = 0;
    sl->hit_withheld = 0;
    sl->hit_cooldown = 0;
    sl->anm_state = WWENEMY_ANM_S1;
    return 0;
}

// The quiet death's last frame for a Bokoblin holding its stick or a
// Darknut holding its sword (under the HeldBy guard, em_carried_boko; a
// Darknut not under EMK_TN_HKD_SWORD): the weapon's base matrix is written
// once from the unscaled hand (the copy's base rebuilt at scale 1 and
// calc'd, then its shrunken base put back and calc'd again so it never
// pops to full size on its last drawn frame), so procCarry's MtxToRot
// never reads a degenerate matrix and the weapon falls from the hand; then
// CARRY is cleared. With the streamed weapon bit clear it falls from its
// stale matrix (accepted, closed_before_build item 4). No other row has a
// carried weapon.
void em_quiet_drop(em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd) {
    void* boko;
    void* model;
    f32 save[3][4];
    if (k->attach != EMK_ATTACH_BK && k->attach != EMK_ATTACH_TN) return;
    if (k->attach == EMK_ATTACH_TN && (hkd & EMK_TN_HKD_SWORD)) return;
    boko = em_carried_boko(a, k);
    if (!boko) return;
    model = morf ? AF(morf, void*, MORF_MODEL) : 0;
    if (model && (sl->tgt_flags2 & EMK_BK_F2_HAS_WEAPON)) {   // == EMK_TN_F2_HAS_WEAPON (em_check_flags2)
        PSMTXCopy((u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET, save);
        em_base_matrix(sl, k, a, morf, 1.0f, 0);
        McaMorf_calc(morf);
        if (k->attach == EMK_ATTACH_TN) em_tn_hand(boko, model);
        else em_bk_hand(boko, model);
        PSMTXCopy(save, (u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET);
        McaMorf_calc(morf);
    }
    em_bk_drop(boko);
}

// --- Animation (S2) ---------------------------------------------------------
// What the paused execute would have done to the morf, from the stream:
//   S2.1 select  setAnm the streamed bck (from getRes of the kind's
//                archive, fresh every change) on an index / attribute /
//                anm_seq change, bracketed by mpSound = NULL; a failed
//                index is retried after 60 frames; a ChuChu's m320 follows
//                (its Draw's shadow radius and the handback read it,
//                d_a_cc.cpp:183-190), and the Armos' and Armos Knight's
//                mCurrBckIdx (em_bck_index_off: their code compares it
//                after the handback; the swallow reads it on the host);
//   S2.2 rate    the streamed rate written when it differs (a parry's
//                setPlaySpeed(-1), the stop at a once-anim's end, ...);
//   S2.3 shapes  MO2: the spear byte and the bare-handed byte; MO2/BK: the
//                blur mesh words;
//   S2.4 play    play(NULL, 0, 0) unless the host skipped play (MO2
//                m05B2, BK m030C);
//   S2.5 frame   the sample's frame plus its rate per REL frame since it
//                was consumed (capped), wrapped once for a loop or clamped
//                to [start, end) otherwise, stored as f32 after play().
// Anything but SYNCED degrades to S1 for the slot (I4): play, no frame
// write. A frame is written only when the applied bck is the streamed one
// (I2).
static void em_animate(enemy_mirror_state* s, em_slot* sl, const em_kind* k, void* a, void* morf, u32 frame) {
    u16 bck_off;
    u8 t = sl->tgt_bck;
    u8 at = sl->tgt_attr;
    int new_sample = (sl->anm_sample_frame == frame);
    // flags2 bit 1 is play-skip only for a kind with that field (on a
    // ChuChu it is CC_CO).
    int skip = k->play_skip && (sl->tgt_flags2 & WWENEMY_SF2_PLAY_SKIP);

    // S2.1 select
    if (t == WWENEMY_BCK_NONE || at == 0xFF) {
        sl->anm_state = WWENEMY_ANM_UNRESOLVED;
        if (new_sample) em_sat_inc(&s->canary[1]);
    } else if (t < k->bck_first || t > k->bck_last || at > MORF_ATTR_MAX) {
        sl->anm_state = WWENEMY_ANM_RANGE;
        if (new_sample) em_sat_inc(&s->canary2[1]);
    } else if (sl->anm_state != WWENEMY_ANM_SYNCED || t != sl->anm_bck || at != sl->anm_attr ||
               sl->tgt_anm_seq != sl->anm_seq_seen) {
        if (sl->anm_retry && t == sl->anm_failed_bck) {
            sl->anm_retry--;
        } else {
            void* res = em_bck_res(k, t);
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
                if (k->id == EMK_KIND_CC) AF(a, s32, EMK_CC_BCK_INDEX) = t;   // anm_init's m320 (d_a_cc.cpp:250)
                else if ((bck_off = em_bck_index_off(k)) != 0) AF(a, s32, bck_off) = t;   // AM/AM2 mCurrBckIdx (R-9)
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

    // S2.3 shapes (a BK's stick follows m0B30 and the attach hook, never
    // the stream; a ChuChu has none)
    if (k->id == EMK_KIND_MO2) {
        AF(a, s8, MO2_WEAPON) = (sl->tgt_flags2 & WWENEMY_SF2_HAS_WEAPON) ? 1 : 0;
        AF(a, u8, MO2_BARE) = (sl->tgt_flags & WWENEMY_SF_BARE_HANDED) ? 1 : 0;
    }
    if (k->blur_on) {
        AF(a, u32, k->blur_on) = (sl->tgt_flags2 & WWENEMY_SF2_BLUR_ON) ? 1 : 0;
        AF(a, u32, k->blur_shape) = sl->tgt_blur_shape;
        AF(a, f32, k->blur_scale) = sl->tgt_blur_scale;
    }

    // S2.4 play (pos NULL: no setAnimSound, m_Do_ext.cpp:1468-1470; the
    // morf chase happens here, :1464-1466)
    if (!skip) McaMorf_play(morf, 0, 0, 0);

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
        if (skip) n = 0;
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

// --- Matrix (step 5) --------------------------------------------------------
// ChuChu's draw_SUB recipe (d_a_cc.cpp:80-104, design D11), which its
// paused execute no longer runs: T(pos.x, pos.y - m328, pos.z) *
// Rx(m316.x) * Rz(m316.z) * Ry(shape.y) * Rx(shape.x) * Rz(shape.z) *
// Ry(m31C) * S * Ry(-m31C), S = (1.1, 1.0, 0.9) with m31C += 1000 while the
// host squashes (EMK_CC_F2_SQUASH), else the copy's own m334 (1.0, D12)
// with m31C = 0. m316, shape.x and m328 are the host's (em_consume and the
// slot's blur_scale); m31C is the copy's own phase.
static void em_cc_matrix(em_slot* sl, void* a, f32 (*m)[4]) {
    s16 ph;
    AF(a, f32, EMK_CC_BLUR_SCALE) = sl->tgt_blur_scale;     // m328, the y offset
    em_mtx_trans_s(m, AF(a, f32, AC_CUR_POS + 0), AF(a, f32, AC_CUR_POS + 4) - AF(a, f32, EMK_CC_BLUR_SCALE),
                   AF(a, f32, AC_CUR_POS + 8));
    mDoMtx_XrotM(m, AF(a, s16, EMK_CC_TILT));
    mDoMtx_ZrotM(m, AF(a, s16, EMK_CC_TILT_Z));
    mDoMtx_YrotM(m, AF(a, s16, AC_SHAPE_ANGLE_Y));
    mDoMtx_XrotM(m, AF(a, s16, EMK_AC_SHAPE_X));
    mDoMtx_ZrotM(m, AF(a, s16, AC_SHAPE_ANGLE_Z));
    if (sl->tgt_flags2 & EMK_CC_F2_SQUASH) {
        ph = (s16)(AF(a, s16, EMK_CC_SQUASH_PHASE) + 1000);
        AF(a, s16, EMK_CC_SQUASH_PHASE) = ph;
        mDoMtx_YrotM(m, ph);
        em_mtx_scale_m(m, 1.1f, 1.0f, 0.9f);
    } else {
        f32 u = AF(a, f32, EMK_CC_SCALE);
        ph = 0;
        AF(a, s16, EMK_CC_SQUASH_PHASE) = 0;
        mDoMtx_YrotM(m, ph);
        em_mtx_scale_m(m, u, u, u);
    }
    mDoMtx_YrotM(m, (s16)-ph);
}

// EMK_MTX_PLAIN (R-3): the draw_SUB / execute recipe of KI, AM and AM2
// (d_a_ki.cpp:1053-1061, d_a_am.cpp:97-112, d_a_am2.cpp:73-87):
// T(pos) * Ry(shape.y) * Rx(shape.x) * Rz(shape.z), from the smoothed
// position and angle (the copy's shape.y is the slot angle: the wire
// AngleY of these rows is shape.y, D13) and the copy's shape.x/z (KI's
// streamed through aux, AM's and AM2's its own). No scale fold and no
// base-scale write (D16: adoption is refused while the host scales).
void em_plain_matrix(const em_slot* sl, void* a, f32 (*m)[4]) {
    em_mtx_trans_s(m, sl->cur.x, sl->cur.y, sl->cur.z);
    mDoMtx_YrotM(m, sl->cur_angle);
    mDoMtx_XrotM(m, AF(a, s16, EMK_AC_SHAPE_X));
    mDoMtx_ZrotM(m, AF(a, s16, EMK_AC_SHAPE_Z));
}

// The kind's base matrix into the model's base matrix (also its cull
// matrix, d_a_mo2.cpp:4126), scaled by `scale` for a quiet death:
//   EMK_MTX_DR     dr_matrix_set on the copy's damagereaction (position,
//                  tilt, shape angle), then PSMTXCopy of *calc_mtx (MO2,
//                  BK);
//   EMK_MTX_CC     em_cc_matrix;
//   EMK_MTX_PLAIN  em_plain_matrix, unless hkd has HKD_MTX_PLAIN (the
//                  model keeps its last base matrix);
//   EMK_MTX_TRYXZ  (BB, batch C R-11) unless EMK_BB_HKD_BEAK, the head and
//                  beak words zeroed first (em_bb_pre_calc), then unless
//                  EMK_BB_HKD_MTX em_tryxz_matrix and setBaseScale(actor
//                  scale) (d_a_bb.cpp:2313-2326);
//   EMK_MTX_MT     (MT-2) unless EMK_MT_HKD_MTX em_mt_matrix: the eight
//                  segment models, scaled through their base scale (their
//                  own matrices, not this one).
// The caller runs McaMorf::calc after it.
static void em_base_matrix(em_slot* sl, const em_kind* k, void* a, void* morf, f32 scale, u8 hkd) {
    void* model = AF(morf, void*, MORF_MODEL);
    void* dst = (u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET;
    f32 m[3][4];
    switch (k->mtx) {
    case EMK_MTX_DR:
        dr_matrix_set((u8*)a + k->dr);
        if (scale == 1.0f) {
            PSMTXCopy((const void*)EM_CALC_MTX, dst);
            return;
        }
        PSMTXCopy((const void*)EM_CALC_MTX, m);
        break;
    case EMK_MTX_CC:
        em_cc_matrix(sl, a, m);
        break;
    case EMK_MTX_PLAIN:
        if (hkd & WWENEMY_HKD_MTX_PLAIN) return;
        em_plain_matrix(sl, a, m);
        break;
    case EMK_MTX_TRYXZ:
        if (!(hkd & EMK_BB_HKD_BEAK)) em_bb_pre_calc(a);
        if (hkd & EMK_BB_HKD_MTX) return;
        em_tryxz_matrix(sl, a, m);
        AF(model, f32, J3DMODEL_BASE_SCALE + 0) = AF(a, f32, AC_SCALE + 0);
        AF(model, f32, J3DMODEL_BASE_SCALE + 4) = AF(a, f32, AC_SCALE + 4);
        AF(model, f32, J3DMODEL_BASE_SCALE + 8) = AF(a, f32, AC_SCALE + 8);
        break;
    case EMK_MTX_MT:
        if (!(hkd & EMK_MT_HKD_MTX)) em_mt_matrix(sl, a, hkd, scale);
        return;
    default:
        return;
    }
    if (scale != 1.0f) em_mtx_scale_m(m, scale, scale, scale);
    PSMTXCopy(m, dst);
}

// --- Per-kind hooks after calc (step 5) --------------------------------------
// EMK_ATTACH_BK: what daBk_Execute does after its calc (d_a_bk.cpp
// :4371-4416, 4104-4136): the carried stick at the hand (only under the
// HeldBy guard, and only while the host still holds one), the shield at
// the TATE joint with its centre m11CC (the defence sphere's), the bouen
// at BUKI * Ry(0x4000) * T(0, 0, 50). When the host dropped its stick
// (yari_off_check, d_a_bk.cpp:101-139: the streamed weapon bit falls) the
// copy lets its own go the same way: hand matrix, CARRY clear, m0B30 = 0
// (critic_applied "Per-type redo list gaps"); a frozen hand would hang it
// in the air (boko setMatrix only sets the base TR, d_a_boko.h:87-91).
// Batch C R-8: while the host slot is SF_HIDDEN (a pot ambusher, or a
// switch-gated BK) the stick is parked as the hidden host's is
// (em_bk_ambush_park) and no drop is tested, unless BK's AMBUSH bit; the
// park lives here, so ATTACH (which skips this whole hook) turns it off
// too.
static void em_bk_attach(const enemy_mirror_state* s, em_slot* sl, void* a, void* morf, int dying, u8 hkd) {
    void* model = AF(morf, void*, MORF_MODEL);
    void* boko = em_bk_boko(a);
    void* n;
    f32 m[3][4];
    if (boko) {
        if ((sl->tgt_flags & WWENEMY_SF_HIDDEN) && !(hkd & EMK_BK_HKD_AMBUSH)) {
            em_bk_ambush_park(boko);
        } else {
            em_bk_hand(boko, model);
            if (!dying && (s->flags & WWENEMY_FLAG_ANIM) && !(sl->tgt_flags2 & EMK_BK_F2_HAS_WEAPON)) {
                em_bk_drop(boko);
                AF(a, s32, EMK_BK_WEAPON) = 0;
            }
        }
    }
    if (AF(a, u8, EMK_BK_SHIELD_GATE) != 0 && em_in_mem1(AF(a, u32, EMK_BK_SHIELD_MODEL)) &&
        (n = em_node_mtx(model, EMK_BK_JOINT_TATE)) != 0) {
        PSMTXCopy(n, (u8*)AF(a, void*, EMK_BK_SHIELD_MODEL) + J3DMODEL_BASE_TR_MTX_OFFSET);
        AF(a, f32, EMK_BK_DEF_CENTER + 0) = ((f32*)n)[3];        // MtxPosition((0,0,0)): the translation
        AF(a, f32, EMK_BK_DEF_CENTER + 4) = ((f32*)n)[7];
        AF(a, f32, EMK_BK_DEF_CENTER + 8) = ((f32*)n)[11];
    }
    if (AF(a, u8, EMK_BK_BOUEN_GATE) != 0 && em_in_mem1(AF(a, u32, EMK_BK_BOUEN_MODEL)) &&
        (n = em_node_mtx(model, EMK_BK_JOINT_BUKI)) != 0) {
        PSMTXCopy(n, m);
        mDoMtx_YrotM(m, EMK_BK_BOUEN_RY);
        em_mtx_trans_m(m, 0.0f, 0.0f, (f32)EMK_BK_BOUEN_TZ);
        PSMTXCopy(m, (u8*)AF(a, void*, EMK_BK_BOUEN_MODEL) + J3DMODEL_BASE_TR_MTX_OFFSET);
    }
}

// EMK_ATTACH_TN, R-6, between em_base_matrix and the body calc (dying
// frames too), unless EMK_TN_HKD_SHIELD, only while the copy still has its
// shield: what daTn_Execute does before mpBodyMorf->calc() (d_a_tn.cpp
// :3603-3612). The shield morf's model takes the body's base matrix (the
// shrunken one on a dying frame) and is calc'd first, so the body calc's
// nodeCallBack copies its fresh left-arm joints (KATA_L1..YUBI_L2, :259-281)
// and the body's own callbacks (head centre, eye) win. The shield's bck
// follows the streamed body bck: AGUARD1, AGUARD2 and AHAKAI1 play the same
// index with the same attribute and rate (anm_init and tate_anm_init in
// pairs, :1924-1928, 2871-2872), its frame then locked to the body's;
// everything else the ATATE_ON1 loop (:1939, 2511, 3458). setAnm runs only
// when the shield's mpAnm is not that bck already, and play is skipped
// while the host skips it (m03EE, :3531-3536). No wire bytes.
void em_tn_shield(em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd) {
    void* sh = AF(a, void*, EMK_TN_SHIELD_MORF);
    void* bm = AF(morf, void*, MORF_MODEL);
    void* sm;
    void* res;
    u8 b = sl->anm_bck;
    int same;
    if (k->attach != EMK_ATTACH_TN || (hkd & EMK_TN_HKD_SHIELD) || !(AF(a, s8, EMK_TN_PIECES) & EMK_TN_PIECE_SHIELD))
        return;
    if (!em_in_mem1((u32)sh) || !em_in_mem1((u32)bm)) return;
    sm = AF(sh, void*, MORF_MODEL);
    if (!em_in_mem1((u32)sm)) return;
    PSMTXCopy((u8*)bm + J3DMODEL_BASE_TR_MTX_OFFSET, (u8*)sm + J3DMODEL_BASE_TR_MTX_OFFSET);
    // The body's mpAnm is that bck's resource while SYNCED (em_animate set
    // it from em_bck_res on the change that made anm_bck).
    same = sl->anm_state == WWENEMY_ANM_SYNCED &&
           (b == EMK_TN_BCK_AGUARD1 || b == EMK_TN_BCK_AGUARD2 || b == EMK_TN_BCK_AHAKAI1);
    res = same ? AF(morf, void*, MORF_ANM) : em_bck_res(k, EMK_TN_BCK_TATE_ON);
    if (em_in_mem1((u32)res) && AF(sh, void*, MORF_ANM) != res) {
        if (same) em_set_anm(sh, res, sl->anm_attr, (f32)sl->tgt_morf, sl->anm_rate);
        else em_set_anm(sh, res, MORF_ATTR_LOOP, EM_TN_TATE_MORF, 1.0f);
    }
    if (!(sl->tgt_flags2 & EMK_TN_F2_PLAY_SKIP)) McaMorf_play(sh, 0, 0, 0);
    if (same) {
        AF(sh, f32, MORF_RATE) = AF(morf, f32, MORF_RATE);
        AF(sh, f32, MORF_FRAME) = AF(morf, f32, MORF_FRAME);
    }
    McaMorf_calc(sh);
}

// EMK_ATTACH_TN, R-7, after the body calc (dying frames too, so the parts
// follow the shrink): what daTn_Execute does after it (d_a_tn.cpp
// :3708-3732) and part_move's on-the-body case (:3143-3318).
//   (a) pieces, unless EMK_TN_HKD_ATTACH: the copy's mRemainingEquipment-
//       Pieces ANDed with the host's (the slot's blur_shape byte carries
//       its low three bits; the AND keeps the copy's others and only ever
//       clears: a broken piece never comes back), and a cleared armor
//       (part 0) or helmet (part 1) hidden at once (m08 = -1: part_draw
//       skips it, :398-427; the host's break flies it off over ~13 frames,
//       which the copy does not mirror). A catch-up / tombstone (RELEASE)
//       carries no host sample: no AND;
//   (b) parts, unless ATTACH: every part with m08 >= 0 still on the body
//       (m09 0) takes the body's anmMtx[MUNE1 / ATAMA1 / ITEM_L1]
//       (j_dt, :3140) as its base matrix (part 0 is the armor morf's model,
//       1 and 2 their mpPartModel, :3286-3305; part 2 only with the
//       shield, :415); no m1402 wobble (zeroed at adopt);
//   (c) armor, unless ATTACH and while part 0 is shown: play (not under
//       the host's play-skip or EMK_TN_HKD_ADOPT_ANM), then calc, whose
//       nodeCallBack_kata copies the body's joints (:387-397, 3316);
//   (d) sword, unless EMK_TN_HKD_SWORD: the carried boko (em_carried_boko)
//       at the hand (em_tn_hand), let go as em_bk_attach lets the stick go
//       when the streamed weapon bit falls (yari_off_check, the host's
//       m0C34 -> 0), never on a shrinking dying frame (the hold's are
//       unscaled);
//   (e) defence centre, unless ATTACH: m13E4 = the ITEM_L1 translation (the
//       defence sphere's centre, :2831-2837).
void em_tn_attach(const enemy_mirror_state* s, em_slot* sl, const em_kind* k, void* a, void* morf, u8 hkd, int dying) {
    static const u8 joint[3] = { EMK_TN_JOINT_MUNE1, EMK_TN_JOINT_ATAMA1, EMK_TN_JOINT_ITEM_L1 };
    void* bm = AF(morf, void*, MORF_MODEL);
    void* armor = AF(a, void*, EMK_TN_ARMOR_MORF);
    void* am = em_in_mem1((u32)armor) ? AF(armor, void*, MORF_MODEL) : 0;
    void* boko;
    void* n;
    u8* part;
    u8 p;
    int i;
    if (k->attach != EMK_ATTACH_TN || !em_in_mem1((u32)bm)) return;
    if (!em_in_mem1((u32)am)) am = 0;
    if (!(hkd & EMK_TN_HKD_ATTACH)) {
        p = AF(a, u8, EMK_TN_PIECES);
        if (!(sl->tgt_flags & WWENEMY_SF_RELEASE)) p &= (u8)(sl->tgt_blur_shape | (u8)~EMK_TN_PIECE_MASK);
        AF(a, u8, EMK_TN_PIECES) = p;
        if (!(p & EMK_TN_PIECE_ARMOR)) AF(a, s8, EMK_TN_PART0 + EMK_TN_PART_M08) = -1;
        if (!(p & EMK_TN_PIECE_HELMET)) AF(a, s8, EMK_TN_PART0 + EMK_TN_PART_STRIDE + EMK_TN_PART_M08) = -1;
        for (i = 0; i < 3; i++) {
            void* pm;
            part = (u8*)a + EMK_TN_PART0 + i * EMK_TN_PART_STRIDE;
            if (AF(part, s8, EMK_TN_PART_M08) < 0 || AF(part, s8, EMK_TN_PART_M09) != 0) continue;
            if (i == 2 && !(p & EMK_TN_PIECE_SHIELD)) continue;
            pm = i == 0 ? am : AF(part, void*, EMK_TN_PART_MODEL);
            n = em_node_mtx(bm, joint[i]);
            if (!n || !em_in_mem1((u32)pm)) continue;
            PSMTXCopy(n, (u8*)pm + J3DMODEL_BASE_TR_MTX_OFFSET);
        }
        if (am && AF(a, s8, EMK_TN_PART0 + EMK_TN_PART_M08) >= 0) {
            if (!(sl->tgt_flags2 & EMK_TN_F2_PLAY_SKIP) && !(hkd & EMK_TN_HKD_ADOPT_ANM)) McaMorf_play(armor, 0, 0, 0);
            McaMorf_calc(armor);
        }
        if ((n = em_node_mtx(bm, EMK_TN_JOINT_ITEM_L1)) != 0) {
            AF(a, f32, EMK_TN_DEF_CENTER + 0) = ((f32*)n)[3];
            AF(a, f32, EMK_TN_DEF_CENTER + 4) = ((f32*)n)[7];
            AF(a, f32, EMK_TN_DEF_CENTER + 8) = ((f32*)n)[11];
        }
    }
    if (!(hkd & EMK_TN_HKD_SWORD) && (boko = em_carried_boko(a, k)) != 0) {
        u8 hold = em_quiet_hold(k, sl->tgt_flags2);
        em_tn_hand(boko, bm);
        // A dying frame inside the quiet death's hold is still at scale 1
        // (em_die_quiet counted it in hit_cooldown): the host's ragdoll lets
        // its sword go there (daTn_Execute's m0C38 at health 0, d_a_tn.cpp
        // :3558-3561, then yari_off_check), and the copy can drop it from an
        // unscaled hand. Past the hold the hand shrinks: em_quiet_drop's.
        // A death that starts RELEASE starts its count past the hold
        // (em_die_quiet): its sword goes on that first frame (count H + 1,
        // scale 1 - 1/(N - H)), never from a shrunken hand at the end.
        if ((!dying || sl->hit_cooldown < hold ||
             ((sl->tgt_flags & WWENEMY_SF_RELEASE) && sl->hit_cooldown == hold + 1)) &&
            (s->flags & WWENEMY_FLAG_ANIM) && !(sl->tgt_flags2 & EMK_TN_F2_HAS_WEAPON)) {
            em_bk_drop(boko);
            AF(a, s32, k->weapon) = 0;
        }
    }
}

// EMK_EYE_CC: daCC_Execute's eye and attention positions (d_a_cc.cpp
// :2108-2122), from the mirrored position: +10 low (m2F5 in [6, 11) or
// action 1 on the host, EMK_CC_F2_LOW_EYE), else +70; attention 30 above
// (action 5 is refused by the gate).
static void em_cc_eye(em_slot* sl, void* a) {
    f32 y = sl->cur.y + ((sl->tgt_flags2 & EMK_CC_F2_LOW_EYE) ? (f32)EMK_CC_EYE_LOW : (f32)EMK_CC_EYE_HIGH);
    AF(a, f32, EMK_AC_EYE_POS + 0) = sl->cur.x;
    AF(a, f32, EMK_AC_EYE_POS + 4) = y;
    AF(a, f32, EMK_AC_EYE_POS + 8) = sl->cur.z;
    AF(a, f32, EMK_AC_ATTN_POS + 0) = sl->cur.x;
    AF(a, f32, EMK_AC_ATTN_POS + 4) = y + (f32)EMK_CC_ATTN_UP;
    AF(a, f32, EMK_AC_ATTN_POS + 8) = sl->cur.z;
}

// EMK_EYE_FIXED (D5, R-7): the eye and attention positions the allow
// rows' executes write every frame, from the mirrored position: KI eye =
// pos, attention 50 above it (d_a_ki.cpp:1064-1066); AM eye 250 and
// attention 330 above pos (d_a_am.cpp:1055-1058); AM2 eye = mEyeballPos
// (its nodeCallBack wrote it during this frame's calc) 15 lower, attention
// 120 above pos (d_a_am2.cpp:1166-1169).
void em_fixed_eye(const em_slot* sl, const em_kind* k, void* a) {
    f32 ex = sl->cur.x, ey, ez = sl->cur.z, ay;
    switch (k->id) {
    case EMK_KIND_KI:
        ey = sl->cur.y + (f32)EMK_KI_EYE_UP;
        ay = ey + (f32)EMK_KI_ATTN_UP;
        break;
    case EMK_KIND_AM:
        ey = sl->cur.y + (f32)EMK_AM_EYE_UP;
        ay = sl->cur.y + (f32)EMK_AM_ATTN_UP;
        break;
    case EMK_KIND_AM2:
        ex = AF(a, f32, EMK_AM2_EYE_NODE + 0);
        ey = AF(a, f32, EMK_AM2_EYE_NODE + 4) - (f32)EMK_AM2_EYE_DOWN;
        ez = AF(a, f32, EMK_AM2_EYE_NODE + 8);
        ay = sl->cur.y + (f32)EMK_AM2_ATTN_UP;
        break;
    default:
        return;
    }
    AF(a, f32, EMK_AC_EYE_POS + 0) = ex;
    AF(a, f32, EMK_AC_EYE_POS + 4) = ey;
    AF(a, f32, EMK_AC_EYE_POS + 8) = ez;
    AF(a, f32, EMK_AC_ATTN_POS + 0) = sl->cur.x;
    AF(a, f32, EMK_AC_ATTN_POS + 4) = ay;
    AF(a, f32, EMK_AC_ATTN_POS + 8) = sl->cur.z;
}

// Step 7: the Bt fields the kind's execute writes every frame, so the
// player's parry window never opens on a copy: MO2 mBtNowFrame 100
// (d_a_mo2.cpp:3668); BK setBtAttackData(100, 100, 10000, 0) +
// setBtNowFrame(0) (d_a_bk.cpp:4291-4292; mBtMaxDis is not needed: type 0
// returns first, d_a_player_battle.inc:44-49); CC mBtNowFrame 1000
// (d_a_cc.cpp:488). Plus the per-kind live fields: a shown BK's draw gate
// m02BA = 0, a CC's m2F9 (shadow/BG skip) from the host; a shown KI's
// switch gate m2D4 = 0 and its m540 = 0 as its execute writes it every
// frame (d_a_ki.cpp:997-998; read by Draw's shadow, :246-249, and the BG
// check, :1079-1089) (R-9). KI, AM and AM2 write no Bt fields (their
// executes do not either); TN writes BK's (the TN batch's R-10).
static void em_kind_fields(em_slot* sl, const em_kind* k, void* a) {
    switch (k->id) {
    case EMK_KIND_MO2:
        AF(a, f32, EN_BT_NOW_FRAME) = EM_BT_NO_OPENING;
        break;
    case EMK_KIND_BK:
        AF(a, f32, EMK_AC_BT_START) = 100.0f;
        AF(a, f32, EMK_AC_BT_END) = 100.0f;
        AF(a, f32, EMK_AC_BT_NOW) = 0.0f;
        AF(a, u8, EMK_AC_BT_TYPE) = 0;
        if (!(sl->tgt_flags & WWENEMY_SF_HIDDEN)) AF(a, u8, EMK_BK_DRAW_GATE) = 0;
        break;
    case EMK_KIND_CC:
        AF(a, f32, EMK_AC_BT_NOW) = (f32)EMK_CC_BT_NOW;
        AF(a, u8, EMK_CC_NO_SHADOW) = (sl->tgt_flags2 & EMK_CC_F2_NO_SHADOW) ? 1 : 0;
        break;
    case EMK_KIND_KI:
        if (!(sl->tgt_flags & WWENEMY_SF_HIDDEN)) AF(a, u8, EMK_KI_DRAW_GATE) = 0;
        AF(a, u8, EMK_KI_SHADOW) = 0;
        break;
    case EMK_KIND_TN:
        // daTn_Execute's setBtAttackData(100, 100, 10000, OPENING_NONE) +
        // setBtNowFrame(0) (d_a_tn.cpp:3539-3540), BK's values; its own
        // case, since BK's also writes BK's draw gate (R-10).
        AF(a, f32, EMK_AC_BT_START) = 100.0f;
        AF(a, f32, EMK_AC_BT_END) = 100.0f;
        AF(a, f32, EMK_AC_BT_NOW) = 0.0f;
        AF(a, u8, EMK_AC_BT_TYPE) = 0;
        break;
    case EMK_KIND_BB:
        // Batch C R-15: Draw and Execute return while u8 0x2F2 != 0
        // (d_a_bb.cpp:611-614, 2242-2249); shown with the host, as BK's and
        // KI's draw gates. While the host slot is HIDDEN the copy keeps its
        // own byte3 + 1 (0x2F2 is not streamed). No Bt fields: daBb_Execute
        // writes none.
        if (!(sl->tgt_flags & WWENEMY_SF_HIDDEN)) AF(a, u8, EMK_BB_DRAW_GATE) = 0;
        break;
    }
}

// --- Colliders (S3) and hits ---------------------------------------------
// The hit entry for slot i: {u16 hit_seq, u8 collider, u8 pending}. The
// REL writes it once per hit; Go clears pending after reading the hit
// object (obj+0x20) and the collider centre (obj+0x118). While pending the
// hit collider is kept out of cCcS, so cCcS::ClrTgHitInf (which only walks
// the objects Set this frame) leaves its hit info for Go to read; when Go
// clears pending the REL clears the hit (ClrTgHit) and re-submits it.
// Collider 1 (EMK_HIT_HEAD) is the kind's head sphere, 2 (EMK_HIT_TG) its
// Tg cylinder (a kind without a head sphere only ever reports 2), 3
// (EMK_HIT_SPECIAL) the Armos Knight's swallowed bomb (R-13, a Co hit the
// REL already consumed: no Tg record to keep or clear, the mouth simply
// stays un-Set while it is pending). The swallow test runs only where a
// hit can be reported (no cooldown, nothing pending), so a deleted bomb is
// always reported. An Armos (AM2) weak-spot hit with its HKD_SPECIAL clear
// also carries the client's bearing verdict in the pending byte
// (WWENEMY_HP_VERDICT, WWENEMY_HP_BACK; em_am2_verdict).
static void em_hits(enemy_mirror_state* s, em_slot* sl, const em_kind* k, void* a, int i, u8 hkd) {
    u32 off = WWENEMY_HITS_OFF + i * WWENEMY_HIT_SIZE;
    if (sl->hit_withheld) {
        if (EB_U8(off + 3) == 0) {
            if (k->id == EMK_KIND_MT)
                em_mt_clr_hits(a, sl->hit_withheld == EMK_HIT_HEAD, sl->hit_withheld == EMK_HIT_TG);   // MT-3: all 8 bodies
            else if (sl->hit_withheld != EMK_HIT_SPECIAL)
                dCcD_GObjInf_ClrTgHit((u8*)a + (sl->hit_withheld == EMK_HIT_HEAD ? k->head_sph : k->tg_cyl));
            sl->hit_withheld = 0;
        }
    }
    // A frame that skips the swallow test leaves the mouth un-Set, and
    // cCcS only clears the Co records of Set objects: drop the record the
    // last Move left (a bomb that touched the mouth on the frame a hit was
    // reported) so the next test never reads a stale contact.
    if (k->id == EMK_KIND_AM && (sl->hit_cooldown || sl->hit_withheld || (hkd & WWENEMY_HKD_SPECIAL)))
        dCcD_GObjInf_ClrCoHit((u8*)a + EMK_AM_MOUTH_SPH);
    if (sl->hit_cooldown) {
        sl->hit_cooldown--;
        return;
    }
    if (sl->hit_withheld) return;
    {
        u8 col = 0;
        if (k->id == EMK_KIND_MT) col = (u8)em_mt_hit_collider(sl, a);   // MT-3: the eye or any body sphere
        else if (k->id == EMK_KIND_AM && !(hkd & WWENEMY_HKD_SPECIAL) && em_am_swallow_client(sl, k, a)) col = EMK_HIT_SPECIAL;
        else if (k->head_sph && (AF(a, u32, k->head_sph + CC_TG_RPRM) & 1)) col = EMK_HIT_HEAD;
        else if (k->tg_cyl && (AF(a, u32, k->tg_cyl + CC_TG_RPRM) & 1)) col = EMK_HIT_TG;
        if (!col) return;
        sl->hit_seq++;
        EB_U16(off + 0) = sl->hit_seq;
        EB_U8(off + 2) = col;
        // The TN batch's R-14: an Armos weak-spot hit carries this client's
        // own bearing verdict for the host's direct apply (any nonzero
        // pending byte is pending to Go and to the test above).
        EB_U8(off + 3) = (k->id == EMK_KIND_AM2 && col == EMK_HIT_HEAD && !(hkd & WWENEMY_HKD_SPECIAL))
                             ? (u8)(WWENEMY_HP_PENDING | em_am2_verdict(a))
                             : (u8)WWENEMY_HP_PENDING;
        sl->hit_withheld = col;
        sl->hit_cooldown = EM_HIT_COOLDOWN;
        // The other collider's hit (the same swing) must not fire next frame.
        if (col == EMK_HIT_HEAD && k->tg_cyl) dCcD_GObjInf_ClrTgHit((u8*)a + k->tg_cyl);
        if (col == EMK_HIT_TG && k->head_sph) dCcD_GObjInf_ClrTgHit((u8*)a + k->head_sph);
    }
    (void)s;
}

// --- The allow rows' collider hooks (R-5, R-13) ------------------------------
// R-5 before STD (D6, D8): the Set bits the host streams. AM2's weak spot
// takes hits only while the host's does (its Tg Set bit, not SF_INVULN,
// which would also drop the body a sleeping Armos' hammer HANDOU needs),
// and its body pushes only while the host's does (Co off while carried,
// d_a_am2.cpp:694, 709). AM's needle At stays off (the host's spike hurts,
// never the copy's).
void em_colliders_pre(const em_slot* sl, const em_kind* k, void* a) {
    switch (k->id) {
    case EMK_KIND_AM:
        AF(a, u32, EMK_AM_NEEDLE_CYL + CC_AT_SPRM) &= ~1u;
        break;
    case EMK_KIND_AM2:
        AF(a, u32, k->head_sph + CC_TG_SPRM) =
            (AF(a, u32, k->head_sph + CC_TG_SPRM) & ~1u) | ((sl->tgt_flags2 & EMK_AM2_F2_WEAK_TG) ? 1u : 0u);
        AF(a, u32, k->co_cyl + CC_CO_SPRM) =
            (AF(a, u32, k->co_cyl + CC_CO_SPRM) & ~1u) | ((sl->tgt_flags2 & EMK_AM2_F2_BODY_CO) ? 1u : 0u);
        break;
    }
}

// The TN batch's R-12, right after em_colliders_pre, unless EMK_TN_HKD_COLL:
// what daTn_Execute writes on its colliders before Setting them (d_a_tn.cpp
// :3636-3654), from the copy's pieces (em_tn_attach ANDed them with the
// host's): the Tg cylinder clanks while the armor is on (GObjTg Shield),
// the head sphere while the helmet is on (Shield, Set, the purple hit
// mark), else Shield off and hit mark UNK1. The Set bit's CUT_KESA test
// reads the host Link's cut, which the copy has no use for (skipped). A
// client-local clank only: the hit itself is forwarded either way. STD
// then places and Sets them.
void em_tn_colliders_pre(const em_slot* sl, const em_kind* k, void* a, u8 hkd) {
    u8* tg = (u8*)a + k->tg_cyl;
    u8* head = (u8*)a + k->head_sph;
    u8 p;
    (void)sl;
    if (k->id != EMK_KIND_TN || (hkd & EMK_TN_HKD_COLL)) return;
    p = AF(a, u8, EMK_TN_PIECES);
    if (p & EMK_TN_PIECE_ARMOR) AF(tg, u32, EMK_GTG_SPRM) |= EMK_GTG_SPRM_SHIELD;
    else AF(tg, u32, EMK_GTG_SPRM) &= ~(u32)EMK_GTG_SPRM_SHIELD;
    if (p & EMK_TN_PIECE_HELMET) {
        AF(head, u32, CC_TG_SPRM) |= 1u;
        AF(head, u32, EMK_GTG_SPRM) |= EMK_GTG_SPRM_SHIELD;
        AF(head, u8, EMK_GTG_HITMARK) = EMK_GTG_HITMARK_PURPLE;
    } else {
        AF(head, u32, EMK_GTG_SPRM) &= ~(u32)EMK_GTG_SPRM_SHIELD;
        AF(head, u8, EMK_GTG_HITMARK) = EMK_GTG_HITMARK_UNK1;
    }
}

// The TN batch's R-14 (D10, D11), client: week_atari_check's bearing test
// (d_a_am2.cpp:205-211) on this game's Link, player 0 here (f_op_actor_mng
// .h:637-639), never the host's: >= EMK_AM2_BACK_MIN is a back hit.
u8 em_am2_verdict(void* a) {
    if (cLib_distanceAngleS(AF(a, s16, AC_SHAPE_ANGLE_Y), fopAcM_searchPlayerAngleY(a)) >= EMK_AM2_BACK_MIN)
        return WWENEMY_HP_VERDICT | WWENEMY_HP_BACK;
    return WWENEMY_HP_VERDICT;
}

// R-5 after STD, every frame STD runs (INVULN frames included, D8), behind
// HKD_EXTRA: the sizes the executes set each frame, which the create-time
// src tables do not carry (R 15): KI Tg 40 and Co 80 (the lunge: 20 and 0,
// d_a_ki.cpp:51, 578-580, 993-995; a Keese has health 1, so its Tg must
// never take the Co's 80); AM eye R 60, body R 80 H 300 (d_a_am.cpp
// :1062-1072); AM2 body R 35 H 150, weak spot R 30 (d_a_am2.cpp:1171-1187).
// The radius sits at +0x124 on a sphere too, the height only on a cylinder.
void em_colliders_post(const em_slot* sl, const em_ext* x, const em_kind* k, void* a) {
    if (em_hkd(x, k) & WWENEMY_HKD_EXTRA) return;
    switch (k->id) {
    case EMK_KIND_KI: {
        int lunge = (sl->tgt_flags2 & EMK_KI_F2_LUNGE) != 0;
        AF(a, f32, k->tg_cyl + CC_SHAPE_RADIUS) = lunge ? (f32)EMK_KI_TG_R_LUNGE : (f32)EMK_KI_TG_R;
        AF(a, f32, k->co_cyl + CC_SHAPE_RADIUS) = lunge ? (f32)EMK_KI_CO_R_LUNGE : (f32)EMK_KI_CO_R;
        break;
    }
    case EMK_KIND_AM:
        AF(a, f32, k->head_sph + CC_SHAPE_RADIUS) = (f32)EMK_AM_EYE_R;
        AF(a, f32, k->tg_cyl + CC_SHAPE_RADIUS) = (f32)EMK_AM_BODY_R;
        AF(a, f32, k->tg_cyl + CC_SHAPE_HEIGHT) = (f32)EMK_AM_BODY_H;
        break;
    case EMK_KIND_AM2:
        AF(a, f32, k->tg_cyl + CC_SHAPE_RADIUS) = (f32)EMK_AM2_BODY_R;
        AF(a, f32, k->tg_cyl + CC_SHAPE_HEIGHT) = (f32)EMK_AM2_BODY_H;
        AF(a, f32, k->head_sph + CC_SHAPE_RADIUS) = (f32)EMK_AM2_WEAK_R;
        break;
    }
}

// R-13 client (D11): bomb_nomi_check (d_a_am.cpp:372-426) on the copy,
// from the stream. The mouth sphere's Co record from the last Move is
// read, then cleared (a record is only ever one Move old: em_hits clears
// it on every frame the mouth stays un-Set), then the mouth is Set
// Co-only at mMouthPos (its nodeCallBack wrote it during this frame's
// calc) with R 100 (d_a_am.cpp:1066-1068; Co Set | IsPlayer | VsOther |
// NoCrr, :1268-1296: no push). The hit actor qualifies when it is a live
// bomb (fpcNm_BOMB_e or fpcNm_Bomb2_e, re-resolved by its pid so a stale
// record never names freed memory), the host's bck is a mouth-open one
// (DAMAGE, DAMAGE_LOOP, OPEN, OPEN_LOOP: the decomp's set, CONTRACT.md),
// this client's Link is within 0x2000 of the copy's facing, and the bomb
// is above mMouthPos.y - 20, and, as bomb_nomi_check asks (d_a_am.cpp:
// 397, 409), not already eaten with more than one fuse frame left (BOMB:
// !getBombCheck_Flag, getBombRestTime > 1; Bomb2: !chk_eat, get_time > 1).
// The bomb is then deleted here (the host's
// own swallow eats its bomb, d_a_am.cpp:400-405; the client's bomb never
// reaches the host) and 1 is returned: em_hits reports EMK_HIT_SPECIAL.
int em_am_swallow_client(em_slot* sl, const em_kind* k, void* a) {
    u8* mouth = (u8*)a + EMK_AM_MOUTH_SPH;
    u8 b = sl->tgt_bck;
    int hit = 0;
    (void)k;
    if ((b == EMK_AM_BCK_DAMAGE || b == EMK_AM_BCK_DAMAGE_LOOP || b == EMK_AM_BCK_OPEN || b == EMK_AM_BCK_OPEN_LOOP) &&
        dCcD_GObjInf_ChkCoHit(mouth)) {
        u32 obj = (u32)dCcD_GObjInf_GetCoHitObj(mouth);
        u32 stts = em_in_mem1(obj) ? AF(obj, u32, CC_STTS) : 0;
        u32 bomb = em_in_mem1(stts) ? AF(stts, u32, CC_STTS_ACTOR) : 0;
        if (em_in_mem1(bomb)) {
            s16 proc = AF(bomb, s16, AC_PROC_NAME);
            u32 pid = AF(bomb, u32, AC_PROC_ID);
            int fuse_ok = proc == EMK_PROC_BOMB ? (AF(bomb, u8, EMK_BOMB_CHECK_FLAG) == 0 && AF(bomb, s16, EMK_BOMB_REST_TIME) > 1)
                        : proc == EMK_PROC_BOMB2 ? (AF(bomb, u8, EMK_BOMB2_EAT) == 0 && AF(bomb, s32, EMK_BOMB2_TIMER) > 1) : 0;
            if (fuse_ok && (u32)em_actor_of(pid) == bomb &&
                cLib_distanceAngleS(AF(a, s16, AC_SHAPE_ANGLE_Y), fopAcM_searchPlayerAngleY(a)) <= EMK_AM_SWALLOW_FACING &&
                AF(bomb, f32, AC_CUR_POS + 4) > AF(a, f32, EMK_AM_MOUTH_POS + 4) - (f32)EMK_AM_SWALLOW_DY) {
                fopAcM_delete_id(pid);
                hit = 1;
            }
        }
    }
    dCcD_GObjInf_ClrCoHit(mouth);
    cM3dGSph_SetC(mouth + CC_SHAPE_CENTER, (const cXyz*)((u8*)a + EMK_AM_MOUTH_POS));
    AF(mouth, f32, CC_SHAPE_RADIUS) = (f32)EMK_AM_MOUTH_R;
    AF(mouth, u32, CC_AT_SPRM) &= ~1u;
    AF(mouth, u32, CC_TG_SPRM) &= ~1u;
    AF(mouth, u32, CC_CO_SPRM) |= 1u;
    cCcS_Set(EM_CCS, mouth);
    return hit;
}

// R-13 host, in execute context (em_hit_apply, collider EMK_HIT_SPECIAL):
// the swallow bomb_nomi_check would start (d_a_am.cpp:413-415), re-checked
// on the host's own mCurrBckIdx. mSwallowedActorPID is left alone: no bomb
// exists here, and bomb_move_set returns early without one (:320-322) and
// mode 47 skips it (:936). 0: no mouth-open bck, or already swallowing.
int em_am_swallow_apply(void* a) {
    s32 b = AF(a, s32, EMK_AM_BCK_INDEX);
    if (b != EMK_AM_BCK_DAMAGE && b != EMK_AM_BCK_DAMAGE_LOOP && b != EMK_AM_BCK_OPEN && b != EMK_AM_BCK_OPEN_LOOP)
        return 0;
    if (AF(a, u8, EMK_AM_GATE) == EMK_AM_ACT_ITAI && AF(a, u8, EMK_AM_MODE) >= EMK_AM_MODE_SWALLOW) return 0;
    // The swallow branch's mouth-smoke remove (d_a_am.cpp:696-705): with
    // mAction ITAI neither it nor mode 8's timer (:655-666) runs again, and
    // mode 44 removes only mSmokeCbs[3] (:850).
    dPa_smokeEcallBack_end((u8*)a + EMK_AM_SMOKE_MOUTH);
    AF(a, u8, EMK_AM_GATE) = EMK_AM_ACT_ITAI;
    AF(a, u8, EMK_AM_MODE) = EMK_AM_MODE_SWALLOW;
    return 1;
}

// R-9: where em_animate writes the streamed bck as the class's own
// mCurrBckIdx on every setAnm (AM, AM2; CC's m320 has its own line).
u16 em_bck_index_off(const em_kind* k) {
    switch (k->id) {
    case EMK_KIND_AM:
        return EMK_AM_BCK_INDEX;
    case EMK_KIND_AM2:
        return EMK_AM2_BCK_INDEX;
    }
    return 0;
}

static void em_anm_play(u32 anm) {
    if (em_in_mem1(anm) && em_in_mem1(AF(anm, u32, ANM_FRAME_CTRL))) mDoExt_baseAnm_play((void*)anm);
}

// R-10, behind HKD_EXTRA, never on a dying frame (the caller): the texture
// and colour anims the executes play next to the morf, which the paused
// copy would freeze: the Fire Keese's flame btk (mDamageType != 0,
// d_a_ki.cpp:1037-1039) and AM2's btk and brk (d_a_am2.cpp:1149-1150).
void em_extra_anm(const em_slot* sl, const em_ext* x, const em_kind* k, void* a) {
    (void)sl;
    if (em_hkd(x, k) & WWENEMY_HKD_EXTRA) return;
    switch (k->id) {
    case EMK_KIND_KI:
        if (AF(a, u8, EMK_KI_DMG_TYPE) != 0) em_anm_play(AF(a, u32, EMK_KI_BTK));
        break;
    case EMK_KIND_AM2:
        em_anm_play(AF(a, u32, EMK_AM2_BTK));
        em_anm_play(AF(a, u32, EMK_AM2_BRK));
        break;
    case EMK_KIND_BB:
        em_bb_extra(a);                     // batch C R-18: the eye btp frame
        break;
    }
}

// EMK_COLL_STD: what daMo2_Execute (d_a_mo2.cpp:3565-3593) and
// daBk_Execute (d_a_bk.cpp:4418-4445) do at their end: the Co cylinder
// co_forward units ahead, the Tg cylinder at the position and the head
// sphere at the head joint unless invulnerable or defending, and the
// shield in defence (MO2 damage_check :3034-3035; BK defence() R 60 and
// centre m11CC, d_a_bk.cpp:2458-2460, whose damage_check otherwise leaves
// it off at R -200 / y -10000, :3530-3533). SetMass is left out
// (dCcS-specific, tunes the push weight only). Never the weapon spheres.
// The allow rows (D7): KI's Co and Tg are spheres (EMK_CAP_SPH_COLL); AM's
// and AM2's Co and Tg are one body cylinder, Set once (its Tg-branch SetC
// still runs: co_forward is 0), and kept out of cCcS while its Tg hit is
// pending, as CC's mCyl is (a Set would let ClrTgHitInf wipe the record
// Go reads); no head without a head sphere, no shield without one. Their
// Set bits (em_colliders_pre) come first, their radii (em_colliders_post)
// last, and the copy never Sets the colliders only the host registers
// (KI's At m580, AM's needle and mouth but for R-13, AM2's needle and
// eye: d_a_ki.cpp:1072-1074, d_a_am.cpp:1074-1078, d_a_am2.cpp:1176-1183).
// MO2/BK/CC take the same path as before (co != tg, head and def set).
// EMK_COLL_CC: the one mCyl at the position, R 35 and H 30 while the
// streamed bck is HUSE_WALK else 100, At off (the copy never hurts
// anyone: the host's PHIT does), Tg and Co as the host's.
static void em_colliders(enemy_mirror_state* s, em_slot* sl, const em_ext* x, const em_kind* k, void* a, int i,
                         u8 hkd) {
    cXyz c;
    u8 f = sl->tgt_flags;
    u8* co;
    u8* tg;
    int sph = (k->caps & EMK_CAP_SPH_COLL) != 0;
    em_hits(s, sl, k, a, i, hkd);
    if (k->coll == EMK_COLL_CC) {
        u8* cyl = (u8*)a + k->tg_cyl;
        if (sl->hit_withheld == 2) return;
        cM3dGCyl_SetC(cyl + CC_SHAPE_CENTER, &sl->cur);
        AF(cyl, f32, CC_SHAPE_RADIUS) = (f32)EMK_CC_CYL_R_VAL;
        AF(cyl, f32, CC_SHAPE_HEIGHT) = (sl->tgt_bck == EMK_CC_BCK_HUSE_WALK) ? (f32)EMK_CC_CYL_H_LOW : (f32)EMK_CC_CYL_H_HIGH;
        AF(cyl, u32, CC_AT_SPRM) &= ~1u;
        AF(cyl, u32, CC_TG_SPRM) = (AF(cyl, u32, CC_TG_SPRM) & ~1u) | ((sl->tgt_flags2 & EMK_CC_F2_TG) ? 1u : 0u);
        AF(cyl, u32, CC_CO_SPRM) = (AF(cyl, u32, CC_CO_SPRM) & ~1u) | ((sl->tgt_flags2 & EMK_CC_F2_CO) ? 1u : 0u);
        cCcS_Set(EM_CCS, cyl);
        return;
    }
    if (k->coll == EMK_COLL_MT) {
        if (!(hkd & EMK_MT_HKD_COLL)) em_mt_colliders(sl, a);   // MT-3
        return;
    }
    if (k->coll != EMK_COLL_STD) return;
    em_colliders_pre(sl, k, a);
    em_tn_colliders_pre(sl, k, a, hkd);
    co = (u8*)a + k->co_cyl;
    tg = (u8*)a + k->tg_cyl;
    c.x = sl->cur.x + (f32)k->co_forward * cM_ssin(sl->cur_angle);
    c.y = sl->cur.y;
    c.z = sl->cur.z + (f32)k->co_forward * cM_scos(sl->cur_angle);
    if (sph) cM3dGSph_SetC(co + CC_SHAPE_CENTER, &c);
    else cM3dGCyl_SetC(co + CC_SHAPE_CENTER, &c);
    if (!(co == tg && sl->hit_withheld == EMK_HIT_TG)) cCcS_Set(EM_CCS, co);
    if (!(f & (WWENEMY_SF_INVULN | WWENEMY_SF_DEFENCE | WWENEMY_SF_RAGDOLL))) {
        if (sl->hit_withheld != 2) {
            if (sph) cM3dGSph_SetC(tg + CC_SHAPE_CENTER, &sl->cur);
            else cM3dGCyl_SetC(tg + CC_SHAPE_CENTER, &sl->cur);
            if (tg != co) cCcS_Set(EM_CCS, tg);
        }
        if (k->head_sph && sl->hit_withheld != 1) {
            cM3dGSph_SetC((u8*)a + k->head_sph + CC_SHAPE_CENTER, (const cXyz*)((u8*)a + k->head_center));
            cCcS_Set(EM_CCS, (u8*)a + k->head_sph);
        }
    }
    if (f & WWENEMY_SF_DEFENCE) {
        if (k->id == EMK_KIND_BK) AF(a, f32, EMK_BK_SHIELD_R) = 60.0f;
        if (k->def_sph) {
            cM3dGSph_SetC((u8*)a + k->def_sph + CC_SHAPE_CENTER, (const cXyz*)((u8*)a + k->def_center));
            cCcS_Set(EM_CCS, (u8*)a + k->def_sph);
        }
    } else if (k->id == EMK_KIND_BK) {
        AF(a, f32, EMK_BK_SHIELD_R) = -200.0f;
        AF(a, f32, EMK_BK_SHIELD_CY) = -10000.0f;
    }
    em_colliders_post(sl, x, k, a);
}

// --- One slot, one frame --------------------------------------------------
static void em_slot_frame(enemy_mirror_state* s, em_ext* x, int i, u32 frame) {
    em_slot* sl = &s->slots[i];
    em_ext_slot* xs = &x->slots[i];
    const em_kind* k;
    void* a;
    void* morf;
    u8 err;
    u8 hkd;
    int dying = 0;
    f32 scale = 1.0f;

    sl->actor = 0;
    if (sl->adopted && sl->apid != sl->pid)
        em_release(sl, x, xs);                   // the slot now names another actor (a quiet-dying copy: deleted, STATE_DEAD)
    if (sl->recently_dead) {
        // The dying latch (apid = the dead pid) holds only while Go still
        // names that pid or none. A quiet death's (STATE_DEAD): the copy is
        // deleted, so nothing is left to re-adopt, and a latch held over the
        // slot's next actor would publish STATE_DEAD beside its pid, which
        // Go's unpin takes for that actor's death. A legacy em_die's (DYING,
        // the copy still shrinking under its own enemy_ice): Go frees the slot
        // once its walk loses the deleted copy and may name the next enemy
        // at once, which the latch would leave unmirrored for the rest of its
        // frames; the shrinking copy itself is never re-adopted (em_adopt's
        // ERR_ICE refuses a running ice timer).
        if (sl->pid != 0 && sl->pid != sl->apid) sl->recently_dead = 1;
        sl->recently_dead--;
        if (sl->recently_dead) return;
        sl->state = WWENEMY_STATE_UNRESOLVED;
        sl->apid = 0;
    }
    if (sl->pid == 0) {
        em_release(sl, x, xs);
        return;
    }
    // A plain release hands the copy back (a quiet-dying one is deleted).
    // RELEASE with DYING (Go's catch-up or tombstone slot: an enemy the
    // host killed that is still alive here) is a kill: resolve, validate
    // and adopt as usual, then die below.
    if ((sl->tgt_flags & (WWENEMY_SF_RELEASE | WWENEMY_SF_DYING)) == WWENEMY_SF_RELEASE) {
        em_release(sl, x, xs);
        return;
    }
    a = em_actor_of(sl->pid);
    if (!a) {
        if (em_quiet_dying(sl)) {
            // Gone mid-shrink (deleted by someone else): it is dead all
            // the same, and Go pins the slot until it hears so.
            em_dead(sl);
            sl->hit_withheld = 0;
            sl->hit_cooldown = 0;
            sl->anm_state = WWENEMY_ANM_S1;
            return;
        }
        em_release(sl, x, xs);                   // the actor is gone: nothing to restore
        sl->state = WWENEMY_STATE_MISSING;
        sl->last_err = WWENEMY_ERR_PID;
        return;
    }
    k = em_kind_at(xs->kind);
    err = em_validate(x, xs, sl, k, a, s->room);
    if (err) {
        // MT-4: the same pid, now this game's custody BALL, is watched for
        // its own death (collider code EMK_HIT_MT_KILL); its refusals past
        // the handback edge are by design, not revalidate_failures.
        int edge = sl->adopted, custody = 0;
        em_release(sl, x, xs);
        if (sl->state != WWENEMY_STATE_DEAD) sl->state = WWENEMY_STATE_REFUSED;
        sl->last_err = err;
        if (k && k->id == EMK_KIND_MT) custody = em_mt_custody_watch(sl, xs, a, i);
        if (edge || !custody) em_sat_inc(&s->canary[2]);
        return;
    }
    // A good validate clears a sticky error (a bad position this frame,
    // ERR_RANGE, is em_consume's to report and clear).
    if (sl->last_err != WWENEMY_ERR_RANGE) sl->last_err = WWENEMY_ERR_NONE;
    sl->actor = (u32)a;
    hkd = em_hkd(x, k);

    if (!sl->adopted) {
        err = em_adopt(s, sl, xs, k, a, i, hkd);
        if (err) {
            sl->actor = 0;
            sl->state = WWENEMY_STATE_REFUSED;
            sl->last_err = err;
            em_sat_inc(&s->canary[2]);
            return;
        }
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

    // Death: the legacy kill (MO2's default), or the quiet one with
    // SF2_QUIET, with the annex's LEGACY_DEATH hook off, or for a catch-up
    // / tombstone (RELEASE with DYING), Moblin included (design D6 (4)).
    // Always quiet for the allow rows (ids >= 3, enemy-mirror-allow.json
    // D2/R-15: em_die's released copy would run its own enemy_ice and drop
    // a second item, whatever WW_ENEMY_QUIET_DEATH says) and for a row
    // without enemyice (em_die needs its shrink timer, D12/R-14).
    // A quiet death goes on with the steps below at a shrinking scale.
    if ((sl->tgt_flags & WWENEMY_SF_DYING) || em_quiet_dying(sl)) {
        int quiet = em_quiet_dying(sl) || (sl->tgt_flags2 & WWENEMY_SF2_QUIET) || (hkd & WWENEMY_HKD_LEGACY_DEATH) ||
                    (sl->tgt_flags & WWENEMY_SF_RELEASE) || k->id >= EMK_KIND_KI || k->ice == 0;
        if (!quiet) {
            em_die(sl, x, xs, k, a);
            return;
        }
        if (!em_die_quiet(sl, x, xs, k, a, &scale)) return;
        dying = 1;
        // A catch-up / tombstone is not mirrored (Go has no host pose for
        // it): it dies where it stands. A fresh catch-up's target would be
        // whatever Go wrote, and at adopt snap would carry the copy there.
        if (sl->tgt_flags & WWENEMY_SF_RELEASE) {
            sl->tgt = sl->cur;
            sl->tgt_angle = sl->cur_angle;
            sl->tgt_tilt = sl->cur_tilt;
        }
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
    // The smoothed tilt is the damagereaction's m48C.x (MO2, BK), or with
    // EMK_CAP_TILT_CUR current.angle.x (BB, batch C R-12); a CC's m316 is
    // written raw at consume.
    if (k->caps & (EMK_CAP_DR | EMK_CAP_TILT_CUR)) AF(a, s16, k->tilt) = sl->cur_tilt;
    if (!dying) em_kind_fields(sl, k, a);    // 7 no stale parry prompt

    // 4 animation: S2 behind the block flag, else S1 (the morf chases and
    // the frame advances, no sounds: play skips the bas sound when pos is
    // NULL, m_Do_ext.cpp:1468-1470) with the blur mesh off.
    morf = AF(a, void*, k->morf);
    if (s->flags & WWENEMY_FLAG_ANIM) {
        em_animate(s, sl, k, a, morf, frame);
    } else {
        if (k->blur_on) AF(a, u32, k->blur_on) = 0;
        McaMorf_play(morf, 0, 0, 0);
    }
    if (!dying) em_extra_anm(sl, x, k, a);  // R-10: KI's flame btk, AM2's btk/brk
    // 5 base matrix from the position, tilt and shape angle, into the
    // model's base matrix, which is also the cull matrix (d_a_mo2.cpp:4126);
    // then calc so the joint callbacks (head centre, eye position) follow;
    // then the kind's hooks.
    em_base_matrix(sl, k, a, morf, scale, hkd);
    if (k->attach == EMK_ATTACH_TN) em_tn_shield(sl, k, a, morf, hkd);       // R-6: before the body calc
    McaMorf_calc(morf);
    if (k->attach == EMK_ATTACH_BK && !(hkd & WWENEMY_HKD_ATTACH)) em_bk_attach(s, sl, a, morf, dying, hkd);
    else if (k->attach == EMK_ATTACH_TN) em_tn_attach(s, sl, k, a, morf, hkd, dying);   // R-7: its bits gate inside
    if (k->id == EMK_KIND_BB) em_bb_tail(sl, xs, a, hkd, dying);   // batch C R-14: after calc wrote BD4
    if (k->eye == EMK_EYE_CC && !(hkd & WWENEMY_HKD_EYE)) em_cc_eye(sl, a);
    else if (k->eye == EMK_EYE_FIXED && !(hkd & WWENEMY_HKD_EYE)) em_fixed_eye(sl, k, a);
    // The Z-target flag (BK, CC): a CC is created with flags 0 and gets
    // LOCKON only in execute (d_a_cc.cpp:2645, 785-1053), a hidden BK
    // returns before :4283 sets it; the host's flag is streamed (ATTN).
    // A dying copy is never a target. MO2 is left alone.
    if (k->id != EMK_KIND_MO2 && (dying || !(hkd & WWENEMY_HKD_ATTN)))
        AF(a, u32, AC_ATTN_FLAGS) = (!dying && (xs->flags_ext & EM_XF_ATTN)) ? EMK_ATTN_LOCKON_BATTLE : 0;
    if (dying) return;

    // 6 colliders (S3): not while hidden or dying.
    if ((s->flags & WWENEMY_FLAG_COLLIDERS) && !(sl->tgt_flags & WWENEMY_SF_HIDDEN)) {
        em_colliders(s, sl, x, k, a, i, hkd);
    }

    if (sl->frames != 0xFFFF) sl->frames++;
    sl->state = WWENEMY_STATE_MIRRORED;
}

// --- Host role: the hit applier ----------------------------------------------
// Go hands over a client's HIT as (seq, the host's pid of that enemy, its
// setID (0xFFFF unkeyed), the collider, the At parameters, flags). v2:
// the REL builds a one-off At sphere at the target's own collider centre,
// registers it for one Move (a second if nothing crossed) and reads its At
// record one execute later: the target's own damage_check then applies the
// damage, reaction, sounds and hit-stop as for a sword hit by the host
// Link. v1 (WWENEMY_HAF_FORCE_V1) refuses what the target's own
// damage_check would drop (invulnerable, defending, ragdoll: d_a_mo2.cpp
// :3072, 3581-3586) and otherwise kills it the K1 way. The target's kind
// must be on in the host's own block (kind_mask), else WWENEMY_HD_KIND.

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

// The request's target: the pid's actor, of a kind that is on, with the
// request's setID in the block's room (and, for an open request, the same
// actor as before).
static void* em_hit_actor(enemy_mirror_state* s, const em_ext* x) {
    void* a = em_actor_of(s->ha_pid);
    const em_kind* k;
    if (!a) return 0;
    k = em_kind_of_actor(a);
    if (!k || !(x->kind_mask & (1u << k->id)) || AF(a, s8, AC_INIT_STATE) != 2 ||
        AF(a, u16, AC_SETID) != s->ha_setid || AF(a, s8, AC_HOME_ROOM) != s->room) return 0;
    if (s->ha_state != EM_HA_IDLE && (u32)a != s->ha_actor) return 0;
    return a;
}

// The sphere's centre from the host's own live target, never the client's
// lagged position: 1 = the head joint the host's head sphere is set to
// (d_a_mo2.cpp:3951-3955; BK m116C), 2 = inside the Tg cylinder at
// current.pos + tg_hit_up (the annex's for ids < 3, else the row's: MO2
// 60, BK 56, CC 50, KI 0, AM 150, AM2 75; em_tg_hit_up), else the client's
// collider centre (collider 3 never gets here: em_hit_apply answers it). A
// Magtail's collider 2 is centred on its own body sphere 4 (MT-3,
// em_mt_hit_center): its head sphere carries the Shield bit in FIGHT.
static void em_hit_center(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a, cXyz* c) {
    if (k->id == EMK_KIND_MT && s->ha_collider == EMK_HIT_TG) {
        em_mt_hit_center(a, c);
    } else if (s->ha_collider == 1 && k->head_center) {
        c->x = AF(a, f32, k->head_center + 0);
        c->y = AF(a, f32, k->head_center + 4);
        c->z = AF(a, f32, k->head_center + 8);
    } else if (s->ha_collider == 2) {
        s16 up = em_tg_hit_up(x, k);
        c->x = AF(a, f32, AC_CUR_POS + 0);
        c->y = AF(a, f32, AC_CUR_POS + 4) + (f32)up;
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
static void em_hit_register(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a) {
    cXyz c;
    em_hit_center(s, x, k, a, &c);
    dCcD_GObjInf_ClrAtHit(EM_SPH);
    dCcD_Sph_StartCAt(EM_SPH, &c);
    cCcS_Set(EM_CCS, EM_SPH);
    s->ha_sets++;
}

// The static sphere image (built lazily, after enemy_mirror_init's
// zeroing). A live image beats hand construction: no __ct__8dCcD_SphFv
// exists in the DOL. The source is the target kind's sph_src (MO2 and BK:
// their head spheres, which carry no At hit callback: d_a_mo2.cpp
// :4185-4186 sets callbacks only on the weapon spheres, d_a_bk.cpp
// :5176-5205 none at all), or for a kind without one (CC) the host Link's
// mFanWindSph. The callback words and the At SPrm are zeroed so the image
// is not an At until Set says so. Everything else stale in the copy is
// reset by ClrAtHit, CalcArea, StartCAt or Set. Shared with remote.c's
// client apply (the sphere is idle on a client).
int enemy_mirror_sph_image(enemy_mirror_state* s, void* a) {
    const em_kind* k = em_kind_of_actor(a);
    u32 link;
    if (k && k->sph_src) return enemy_mirror_sph_image_at(s, (const u8*)a + k->sph_src);
    if (s->sph_built) return 1;
    link = EM_PLAYER_PTR0;
    if (!em_in_mem1(link) || AF(link, s16, AC_PROC_NAME) != PROC_PLAYER) return 0;
    return enemy_mirror_sph_image_at(s, (const u8*)link + EM_LK_FAN_WIND_SPH);
}

// The same from any live dCcD_Sph (remote.c's client apply falls back to
// Link's mFanWindSph when no mirrored copy owns the hit): only the first
// call per relink copies, and the vtable fingerprint below must hold.
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
    AF(EM_SPH, u32, CC_GTG_CALLBACK) = 0;       // hardening for later rows whose spheres carry Tg/Co callbacks
    AF(EM_SPH, u32, CC_GCO_CALLBACK) = 0;
    AF(EM_SPH, u32, CC_AT_SPRM) = 0;
    s->sph_built = 1;
    return 1;
}

// The sphere of a request, before its first Set or a direct apply: the
// host Link, the static stts and sphere image (built lazily, after
// enemy_mirror_init's zeroing), the dCcD_SrcSph fill and dCcD_Sph::Set.
// Publishes and returns 0 on a failure.
static int em_hit_build(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a) {
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
        return 0;
    }
    // Link's own mStts.Init(120, 0xFF, this) (the body's with ATTRIB): apid = fopAcM_GetID(link),
    // mp_actor = link, room (d_cc_d.cpp:66-88; c_cc_d.cpp:185-192). On
    // every submit, not keyed on the pointer: the module outlives a stage
    // load and a recreated Link can sit at the old address with a new
    // process id; a stale apid makes the target's ChkTgHit fail
    // (fopAcM_SearchByID NULL, d_cc_d.cpp:14-24,188-197; d_cc_s.cpp:434)
    // while our At record still reads as a cross. No allocation; nothing
    // holds the stts between Moves. stts_link stays as a diagnostic.
    dCcD_Stts_ct(EM_STTS);
    dCcD_Stts_Init(EM_STTS, EM_STTS_WEIGHT, EM_STTS_P1, (void*)link);
    s->stts_link = link;
    if (!enemy_mirror_sph_image(s, a)) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_SPHERE);
        return 0;
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
    em_hit_center(s, x, k, a, &c);
    AF(sb, f32, SRC_CENTER + 0) = c.x;
    AF(sb, f32, SRC_CENTER + 4) = c.y;
    AF(sb, f32, SRC_CENTER + 8) = c.z;
    AF(sb, f32, SRC_RADIUS) = EM_AT_RADIUS;
    // dCcD_GObjInf::Set + cCcD_SphAttr::Set (d_cc_d.cpp:369-373); the stts
    // store is an inline (c_cc_d.h:608) Set does not touch.
    dCcD_Sph_Set(EM_SPH, src);
    AF(EM_SPH, u32, CC_STTS) = (u32)EM_STTS;
    return 1;
}

// The first Set of a request: the sphere (em_hit_build), then the
// registration. Publishes on a failure.
static void em_hit_submit(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a) {
    if (!em_hit_build(s, x, k, a)) return;
    em_hit_register(s, x, k, a);
    s->ha_state = EM_HA_SET;
    s->ha_actor = (u32)a;
    s->ha_frames = 0;
}

// --- AM2 direct apply (the TN batch's R-15/R-16, hit_path, am2_fix) ---------
// An Armos weak-spot hit the client judged itself (WWENEMY_HAF_VERDICT,
// em_am2_verdict on the client's own Link) never takes the sphere path,
// whose consumer week_atari_check measures the bearing to the host Link
// (player 0, d_a_am2.cpp:205-211), so a client's back-stab counted only
// with the host Link behind too. The host's AM2 HKD_SPECIAL turns it off
// (D10): the request then takes the sphere path as before, VERDICT ignored.
// Light arrow, wind and boomerang keep the sphere path (vanilla returns
// before cc_at_check for them, :227-233, 262-268).
int em_hit_direct_ok(const enemy_mirror_state* s, const em_ext* x, const em_kind* k) {
    return k->id == EMK_KIND_AM2 && s->ha_collider == EMK_HIT_HEAD && (s->ha_flags & WWENEMY_HAF_VERDICT) &&
           !(s->ha_attype & EMK_AT_OLD_PATH) && !(em_hkd(x, k) & WWENEMY_HKD_SPECIAL);
}

// R-16 on the direct path only: where vanilla never reaches
// week_atari_check: carried (the gate refuses a carried Armos, but the
// host may pick it up after adoption), or MAHI with mMode 15, thrown
// (d_a_am2.cpp:841-846). em_hit_vulnerable stays as it was, so the sphere
// path is unchanged.
int em_am2_direct_refused(void* a) {
    return (AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY) ||
           (AF(a, u8, EMK_AM2_GATE) == EMK_AM2_ACT_MAHI && AF(a, u8, EMK_AM2_MODE) == EMK_AM2_MODE_THROWN);
}

// week_atari_check's m2CE for the request's At type (d_a_am2.cpp:234-290;
// 0 = Link's sword, as em_hit_build fills it). Its switch compares the At
// type for equality, so a type names a class only when all its bits are in
// that class's mask. The crit cut types (sword :241-260, the hammer's side
// swing :278-280) are Go's to judge from the HIT's CutType: one bit,
// WWENEMY_HAF_CRIT.
u8 em_am2_m2ce(u32 attype, u8 ha_flags) {
    u32 t = attype ? attype : AT_TYPE_SWORD;
    int crit = (ha_flags & WWENEMY_HAF_CRIT) != 0;
    if (t == EMK_AT_SKULL_HAMMER) return crit ? EMK_AM2_M2CE_HAMMER_SIDE : EMK_AM2_M2CE_HAMMER;
    if (t == EMK_AT_BOMB) return EMK_AM2_M2CE_BOMB;
    if (!(t & ~(u32)EMK_AT_ARROWS)) return EMK_AM2_M2CE_ARROW;
    if (!(t & ~(u32)EMK_AT_SWORD_CLASS)) return crit ? EMK_AM2_M2CE_CRIT : 0;
    return 0;
}

// The direct apply on a vulnerable, back-hit Armos (em_hit_apply's direct
// branch, or the WAIT_INVULN retry): the sphere built exactly as for a Set
// (the stts on the host Link, so at_power_check sees a PLAYER attacker,
// d_cc_uty.cpp:111-134) but never registered with cCcS; a zeroed CcAtInfo
// (mbDead must start 0, d_cc_uty.cpp:226-244) naming it, no particle
// position; cc_at_check(a, &info): the health, hit SE and hit-stop; then
// week_atari_check's tail (d_a_am2.cpp:212-213, 290-316): m2CE, the weak
// spot marked hit, ITAI mode 20, health 0 for the hammer. Its own
// mStts.Move() and the crit particles are skipped (accepted, am2_fix).
// Published HR_APPLIED_AT / HD_DIRECT at once: there is no At record to
// read next execute.
void em_hit_direct(enemy_mirror_state* s, const em_ext* x, const em_kind* k, void* a) {
    u32 info[EMK_CCATINFO_SIZE / 4];
    u8 m;
    int i;
    if (!em_hit_build(s, x, k, a)) return;
    for (i = 0; i < EMK_CCATINFO_SIZE / 4; i++) info[i] = 0;
    info[EMK_CCATINFO_OBJ / 4] = (u32)EM_SPH;
    info[EMK_CCATINFO_PARTICLE / 4] = 0;
    m = em_am2_m2ce(s->ha_attype, s->ha_flags);
    AF(a, u8, EMK_AM2_M2CE) = m;
    AF(a, u8, EMK_AM2_WEAK_HIT) = 1;
    cc_at_check(a, info);
    AF(a, u8, k->gate) = EMK_AM2_ACT_ITAI;
    AF(a, u8, EMK_AM2_MODE) = EMK_AM2_ITAI_MODE_HIT;
    if (m == EMK_AM2_M2CE_HAMMER || m == EMK_AM2_M2CE_HAMMER_SIDE) AF(a, s8, AC_HEALTH) = 0;
    em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_DIRECT);
}

// The target's invulnerability is over: its invuln timer is 0 (MO2
// m05B4, BK m030E, KI m314), or for a kind without one (CC, AM) its own Tg
// cylinder is Set again (it clears the Set bit while it cannot be hit).
// AM2's weak spot (collider 1, R-12) takes a hit only while its Tg is Set,
// the Armos is awake (DOUSA mode >= 3, d_a_am2.cpp:624) or stunned (MAHI,
// :846) and nobody carries it; its body (collider 2) only needs its Tg Set
// bit, so a hammer on a sleeping Armos still lands (D6). A Magtail (MT-3)
// takes hits while its hit cooldown is 0 (em_mt_vulnerable).
static int em_hit_vulnerable(const enemy_mirror_state* s, const em_kind* k, void* a) {
    if (k->id == EMK_KIND_MT) return em_mt_vulnerable(a);
    if (k->id == EMK_KIND_AM2 && s->ha_collider == EMK_HIT_HEAD) {
        u8 act = AF(a, u8, k->gate);
        return (AF(a, u32, k->head_sph + CC_TG_SPRM) & 1) &&
               ((act == EMK_AM2_ACT_DOUSA && AF(a, u8, EMK_AM2_MODE) >= EMK_AM2_MODE_AWAKE) || act == EMK_AM2_ACT_MAHI) &&
               !(AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY);
    }
    if (k->invuln) return AF(a, s16, k->invuln) == 0;
    return (AF(a, u32, k->tg_cyl + CC_TG_SPRM) & 1) != 0;
}

// The target is dying on the host (hit_path.host_prechecks): MO2 health
// <= 0 or ACTION_FAIL; BK dr.mAction 20-22 (health <= 0 alone is not a
// BK death, c_damagereaction.cpp:1146-1150); CC action 4; KI health <= 0
// or ACT_FAIL (d_a_ki.cpp:1121, 1147-1149); AM swallowing (ITAI mode >= 44,
// d_a_am.cpp:849-956) or shrinking; AM2 in the death dance (ITAI mode >=
// 22, or ITAI with no health: :873-971) or shrinking (R-12); TN dr.mAction
// 20-22 (22 from dr_body_bg_check, c_damagereaction.cpp:669-676), shrinking,
// or health <= 0 (p0_findings DEATH ORDER: the host ragdolls ~1.6 s at
// health 0 before action 20; the ragdoll test before this one already
// drops most of that window as HD_RAGDOLL); batch C R-16: BB health <= 0,
// mbDead 0xC7E (its knockback before bb_fail_move, d_a_bb.cpp:2138-2141) or
// shrinking; MT mode DEAD, the bakuha flag 0x18F8 >= 2 (mt.rel 0x747C-0x7498)
// or shrinking.
static int em_hit_dying(const em_kind* k, void* a) {
    switch (k->id) {
    case EMK_KIND_MO2:
        return AF(a, s8, AC_HEALTH) <= 0 || AF(a, s16, MO2_DR_ACTION) == MO2_ACTION_FAIL;
    case EMK_KIND_BK: {
        s16 act = AF(a, s16, k->dr_action);
        return act >= EMK_BK_ACTION_DIE_A && act <= EMK_BK_ACTION_DIE_C;
    }
    case EMK_KIND_CC:
        return AF(a, u8, EMK_CC_ACTION) == 4;
    case EMK_KIND_KI:
        return AF(a, s8, AC_HEALTH) <= 0 || AF(a, u8, k->gate) == EMK_KI_ACT_FAIL;
    case EMK_KIND_AM:
        return (AF(a, u8, k->gate) == EMK_AM_ACT_ITAI && AF(a, u8, EMK_AM_MODE) >= EMK_AM_MODE_SWALLOW) ||
               AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0;
    case EMK_KIND_AM2:
        return (AF(a, u8, k->gate) == EMK_AM2_ACT_ITAI &&
                (AF(a, u8, EMK_AM2_MODE) >= EMK_AM2_MODE_DANCE || AF(a, s8, AC_HEALTH) <= 0)) ||
               AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0;
    case EMK_KIND_TN: {
        s16 act = AF(a, s16, k->dr_action);
        return (act >= EMK_TN_ACT_DIE_A && act <= EMK_TN_ACT_DIE_C) || AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0 ||
               AF(a, s8, AC_HEALTH) <= 0;
    }
    case EMK_KIND_BB:
        return AF(a, s8, AC_HEALTH) <= 0 || AF(a, u8, EMK_BB_DEAD) != 0 || AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0;
    case EMK_KIND_MT:
        return AF(a, u8, k->gate) == EMK_MT_MODE_DEAD || AF(a, s8, EMK_MT_DEATH) >= 2 ||
               AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0;
    }
    return 0;
}

// The fallback shrink is only for a target that would take the hit
// (hit_path.fallback_shrink): never invulnerable, ragdolling or defending
// (MO2/BK: their head and Tg colliders are lowered out of reach in
// defence, d_a_mo2.cpp:3586-3588, d_a_bk.cpp:4436-4441, and the shield
// parries; its radius is positive only in defence, d_a_mo2.cpp:2219,
// 2249, 3071; d_a_bk.cpp:2458-2460, 3533); a CC only with its Tg Set and
// not in actions 4 or 6; never KI, AM or AM2 (R-12: no fallback kill).
static int em_hit_shrink_ok(const em_kind* k, void* a) {
    switch (k->id) {
    case EMK_KIND_MO2:
        return AF(a, s16, MO2_INVULN) == 0 && AF(a, s16, MO2_DR_MODE) > MO2_MODE_RAGDOLL &&
               AF(a, s16, MO2_DR_ACTION) != MO2_ACTION_DEFENCE && !(AF(a, f32, MO2_DEF_SPH + CC_SHAPE_RADIUS) > 0.0f);
    case EMK_KIND_BK:
        return AF(a, s16, k->invuln) == 0 && AF(a, s16, k->dr_mode) > EM_DR_RAGDOLL &&
               AF(a, s16, k->dr_action) != EM_DR_DEFENCE && !(AF(a, f32, EMK_BK_SHIELD_R) > 0.0f);
    case EMK_KIND_CC: {
        u8 act = AF(a, u8, EMK_CC_ACTION);
        return (AF(a, u32, k->tg_cyl + CC_TG_SPRM) & 1) && act != 4 && act != 6;
    }
    }
    return 0;
}

// The At record names one of the target's colliders: for MO2 the head
// sphere or Tg cylinder by address (EMK_CAP_ADDR_SUCCESS, today's test),
// for the others the collider's stts owner (obj+0x44 -> stts+0x0C, remote.c
// rl_obj_actor) is the target (design D9).
static int em_hit_on_target(const em_kind* k, u32 a, u32 obj) {
    u32 stts;
    if (k->caps & EMK_CAP_ADDR_SUCCESS) return obj == a + k->head_sph || obj == a + k->tg_cyl;
    if (!em_in_mem1(obj)) return 0;
    stts = AF(obj, u32, CC_STTS);
    if (!em_in_mem1(stts)) return 0;
    return AF(stts, u32, CC_STTS_ACTOR) == a;
}

// Every execute while a request is open; live == 0 on an early return,
// a release of everything, delete or unlink: the request is acked
// HD_ABORTED (a Set happens in execute and its Move in the same frame's
// draw, so no Set is left behind; the objects are static anyway). An
// abort in EM_HA_SET reads the static At record first: a cross on the
// target in the last Move means its damage_check takes the hit anyway, so
// it is APPLIED_AT. Only ha_actor's value is compared (MO2: its two
// colliders' addresses; others: ha_actor <= mHitObj < ha_actor +
// class_size); the actor may be gone, so it is never dereferenced (no
// parry check here).
static void em_hit_tick(enemy_mirror_state* s, em_ext* x, int live) {
    void* a;
    const em_kind* k;
    if (s->ha_state == EM_HA_IDLE) return;
    if (!live) {
        k = em_kind_at(x->spare[EM_X_HA_KIND]);
        if (s->ha_state == EM_HA_SET && s->ha_actor != 0 && k && (AF(EM_SPH, u32, CC_AT_RPRM) & 1)) {
            u32 obj = AF(EM_SPH, u32, CC_AT_HIT_OBJ);
            int on = (k->caps & EMK_CAP_ADDR_SUCCESS) ? (obj == s->ha_actor + k->head_sph || obj == s->ha_actor + k->tg_cyl)
                                                      : (obj >= s->ha_actor && obj < s->ha_actor + k->class_size);
            if (on) {
                em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
                return;
            }
        }
        em_sat_inc(&s->canary2[3]);
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_ABORTED);
        return;
    }
    em_sat_inc(&s->ha_frames);
    a = em_hit_actor(s, x);
    if (!a) {
        em_hit_publish(s, WWENEMY_HR_NO_ACTOR, WWENEMY_HD_NONE);
        return;
    }
    k = em_kind_of_actor(a);
    if (s->ha_state == EM_HA_WAIT_INVULN) {
        // The target's invuln after any hit (MO2 5 frames, d_a_mo2.cpp:3076;
        // BK m030E = 10, d_a_bk.cpp:2031, 3102): wait for it instead of
        // dropping, so a client's second combo hit lands. The wait is the
        // annex's, else the row's.
        u8 wait = em_invuln_wait(x, k);
        if (em_hit_vulnerable(s, k, a)) {
            // A parked direct-apply hit (AM2 verdict) retries on the direct
            // path, never into week_atari_check's player-0 gate. Dying is
            // re-tested as em_hit_apply's first pass does: a light arrow can
            // start enemy_ice's shrink while the hit waits, and daAM2_Execute
            // then returns early (d_a_am2.cpp:1120), leaving mode and the
            // weak sphere's Tg Set bit as they were.
            if (!em_hit_direct_ok(s, x, k)) em_hit_submit(s, x, k, a);
            else if (em_hit_dying(k, a)) em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DYING);
            else if (em_am2_direct_refused(a)) em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
            else em_hit_direct(s, x, k, a);
        } else if (++s->ha_waited > wait) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
        }
        return;
    }
    // EM_HA_SET, one execute after a Set, BEFORE any Set: the records from
    // that frame's Move. The defence sphere's Tg hit bit (the target Sets
    // it inside damage_check every defence frame, d_a_mo2.cpp:3034-3036,
    // d_a_bk.cpp:3530-3534, so a cross shows here and is cleared only at
    // the next Move); our At record (mRPrm bit 0 + mHitObj), which no Move
    // clears while the sphere stays un-Set (ClrAtHitInf walks only that
    // frame's objects, c_cc_s.cpp:115-118). Never re-Set after a cross:
    // the target's Tg NoConHit would dedupe it and its Move would clear
    // the record. A kind without a defence sphere (CC) never parries.
    {
        u32 def = k->def_sph ? (AF(a, u32, k->def_sph + CC_TG_RPRM) & 1) : 0;
        u32 w = AF(EM_SPH, u32, CC_AT_RPRM);
        u32 obj = AF(EM_SPH, u32, CC_AT_HIT_OBJ);
        if (def) {
            em_hit_publish(s, WWENEMY_HR_PARRIED, WWENEMY_HD_NONE);
        } else if ((w & 1) && em_hit_on_target(k, (u32)a, obj)) {
            em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
        } else if (w & 1) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_WRONG_OBJ);
        } else if (s->ha_sets < EM_MAX_SETS) {
            em_hit_register(s, x, k, a);         // the collider was not Set that frame, or the head moved
        } else {
            em_sat_inc(&s->canary2[2]);
            if ((s->ha_flags & WWENEMY_HAF_FALLBACK_SHRINK) && k->ice && em_hit_shrink_ok(k, a)) {
                AF(a, s8, k->ice + EMK_ICE_SHRINK) = 1;
                em_hit_publish(s, WWENEMY_HR_SHRINK, WWENEMY_HD_FALLBACK_SHRINK);
            } else {
                em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_CROSS);
            }
        }
    }
}

// A new request (ha_seq nonzero and != hit_apply_ack) while none is open.
static void em_hit_apply(enemy_mirror_state* s, em_ext* x) {
    void* a;
    const em_kind* k;
    int delay = 0;
    s->hit_apply_ack = s->ha_seq;                // taken; the block's ack word carries the result
    s->ha_frames = 0;
    s->ha_sets = 0;
    s->ha_waited = 0;
    s->ha_actor = 0;
    s->ha_attrib = 0;
    x->spare[EM_X_HA_KIND] = EMK_KIND_NONE;
    // A resolved actor of a kind without a row, or whose kind is off in
    // this host's block: not ours to hit.
    a = em_actor_of(s->ha_pid);
    if (a) {
        k = em_kind_of_actor(a);
        if (!k || !(x->kind_mask & (1u << k->id))) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_KIND);
            return;
        }
    }
    a = em_hit_actor(s, x);
    if (!a) {
        em_hit_publish(s, WWENEMY_HR_NO_ACTOR, WWENEMY_HD_NONE);
        return;
    }
    k = em_kind_of_actor(a);
    x->spare[EM_X_HA_KIND] = k->id;
    // Collider 3 (R-13, D11): the client's copy swallowed a bomb. No sphere,
    // no wait: the swallow starts here or not at all (a kind other than AM
    // has no code 3; the host's own HKD_SPECIAL refuses it as well).
    if (s->ha_collider == EMK_HIT_SPECIAL) {
        if (k->id != EMK_KIND_AM)
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_KIND);
        else if (em_hkd(x, k) & WWENEMY_HKD_SPECIAL)
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NONE);
        else if (em_am_swallow_apply(a))
            em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
        else
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_CROSS);
        return;
    }
    // Collider 4 (batch C MT-4): a client's custody BALL died; the host's
    // Magtail takes its own bakuha only while still curled with no death
    // flag (em_mt_kill_apply), else the report is stale (NO_CROSS).
    if (s->ha_collider == EMK_HIT_MT_KILL) {
        if (k->id != EMK_KIND_MT)
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_KIND);
        else if (em_mt_kill_apply(a))
            em_hit_publish(s, WWENEMY_HR_APPLIED_AT, WWENEMY_HD_NONE);
        else
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NO_CROSS);
        return;
    }
    // The TN batch's AM2 direct apply (hit_path): before FORCE_V1 and
    // ATTRIB, so remote_attrib_force never runs for it and ha_attrib stays
    // 0 (no double apply with WW_ENEMY_REMOTE's step6). An Armos is not a
    // damagereaction kind: no ragdoll test. A front verdict is dropped here
    // (HD_FRONT), never re-judged against the host Link; an invulnerable
    // weak spot parks the hit (WAIT_INVULN) as the sphere path does.
    if (em_hit_direct_ok(s, x, k)) {
        if (em_hit_dying(k, a)) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DYING);
        } else if (em_am2_direct_refused(a)) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
        } else if (!(s->ha_flags & WWENEMY_HAF_BACK)) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_FRONT);
        } else if (!em_hit_vulnerable(s, k, a)) {
            s->ha_state = EM_HA_WAIT_INVULN;
            s->ha_actor = (u32)a;
        } else {
            em_hit_direct(s, x, k, a);
        }
        return;
    }
    if (s->ha_flags & WWENEMY_HAF_FORCE_V1) {
        // v1: the pre-drops, else the K1 shrink (defence and ragdoll exist
        // only on a damagereaction kind; no shrink without enemyice, D12).
        if (!em_hit_vulnerable(s, k, a)) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_INVULN);
        } else if ((k->caps & EMK_CAP_DR) && AF(a, s16, k->dr_action) == EM_DR_DEFENCE) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DEFENCE);
        } else if ((k->caps & EMK_CAP_DR) && AF(a, s16, k->dr_mode) <= EM_DR_RAGDOLL) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_RAGDOLL);
        } else if (!k->ice) {
            em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_NONE);
        } else {
            AF(a, s8, k->ice + EMK_ICE_SHRINK) = 1;
            em_hit_publish(s, WWENEMY_HR_SHRINK, WWENEMY_HD_NONE);
        }
        return;
    }
    // remote-Link ATTRIB (design section 8.6): with the client's body LIVE
    // the target's latch is forced onto it for 3 executes (honoured with
    // WWREMOTE_FLAG_ATTACKER), and when it was not the current target the
    // first Set waits one keeper execute (the WAIT_INVULN state), so the
    // consuming execute's damage_check bearing (MO2 m05D6) is computed
    // toward the body (d_a_mo2.cpp:3112-3163, 2903-2911). No LIVE body:
    // today's host-Link path, detail WWREMOTE_HD_NO_BODY.
    if (s->ha_flags & WWREMOTE_HAF_ATTRIB_BODY) {
        u32 body = remote_attrib_body(s->ha_flags);
        s->ha_attrib = body ? 1 : 2;
        if (body) delay = remote_attrib_force((u32)a, body);
    }
    // v2: the colliders sit at y-20000 while ragdolling (d_a_mo2.cpp
    // :3580-3593) and a dying target takes no hit; defence is not
    // pre-dropped (the shield parries, the faithful outcome).
    if ((k->caps & EMK_CAP_DR) && AF(a, s16, k->dr_mode) <= EM_DR_RAGDOLL) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_RAGDOLL);
    } else if (em_hit_dying(k, a)) {
        em_hit_publish(s, WWENEMY_HR_DROPPED, WWENEMY_HD_DYING);
    } else if (!em_hit_vulnerable(s, k, a) || delay) {
        s->ha_state = EM_HA_WAIT_INVULN;
        s->ha_actor = (u32)a;
    } else {
        em_hit_submit(s, x, k, a);
    }
}

// --- Entry points -----------------------------------------------------------
void enemy_mirror_init(enemy_mirror_state* s, em_ext* x) {
    u32* w = (u32*)s;
    u8* b = (u8*)x;
    u32 n;
    int i;
    for (n = 0; n < sizeof(enemy_mirror_state) / 4; n++) w[n] = 0;
    for (n = 0; n < sizeof(em_ext); n++) b[n] = 0;
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) x->slots[i].kind = EMK_KIND_NONE;
    x->spare[EM_X_HA_KIND] = EMK_KIND_NONE;
    // A hit_apply request left in the block from before this link is not
    // ours to apply: start acked.
    s->hit_apply_ack = EB_U32(WWENEMY_HIT_APPLY_OFF + WWENEMY_HA_SEQ_OFF);
    g_live = s;
    g_live_ext = x;
}

void enemy_mirror_frame(enemy_mirror_state* s, em_ext* x, u32 frame) {
    u32 magic = EB_U32(WWENEMY_MAGIC_OFF);
    u16 layout = EB_U16(WWENEMY_LAYOUT_OFF);
    u8 flags = EB_U8(WWENEMY_FLAGS_OFF);
    u32 epoch, head, tail;
    int i;

    // 0 intake
    if (magic != WWENEMY_MAGIC || layout != WWENEMY_LAYOUT || !(flags & WWENEMY_FLAG_MIRROR)) {
        em_release_all(s, x, 0);
        em_hit_tick(s, x, 0);
        em_write_status(s);
        return;
    }
    epoch = EB_U32(WWENEMY_EPOCH_OFF);
    if (epoch != s->epoch_seen) {
        em_release_all(s, x, 1);
        s->epoch_seen = epoch;
        s->last_seq = 0;
    }
    head = EB_U32(WWENEMY_SEQ_HEAD_OFF);
    tail = EB_U32(WWENEMY_SEQ_TAIL_OFF);
    if (head != 0 && head == tail && head != s->last_seq) {
        em_consume(s, x, head, frame);
    } else {
        s->stale_frames++;
        if (s->stale_frames > WWENEMY_STALE_FRAMES && !s->stale_released) {
            em_release_all(s, x, 0);
            s->stale_released = 1;
            em_sat_inc(&s->canary[3]);
        }
    }
    if (s->stale_released || s->last_seq == 0) {
        em_hit_tick(s, x, 0);
        em_write_status(s);
        return;
    }

    // 1 scope: release before the stage teardown deletes the actors.
    if (!em_in_scope(s)) {
        em_release_all(s, x, 0);
        em_hit_tick(s, x, 0);
        em_write_status(s);
        return;
    }

    // 2..8 per slot
    for (i = 0; i < WWENEMY_MAX_SLOTS; i++) em_slot_frame(s, x, i, frame);

    // host role: the open request first (its record from last frame's
    // Move), then a new one
    em_hit_tick(s, x, 1);
    // (seq 0 is never a request: a block written before the first hit)
    if (s->role == WWENEMY_ROLE_HOST && s->ha_state == EM_HA_IDLE && s->ha_seq != 0 && s->ha_seq != s->hit_apply_ack)
        em_hit_apply(s, x);

    em_write_status(s);
}

void enemy_mirror_delete(enemy_mirror_state* s, em_ext* x) {
    em_release_all(s, x, 1);
    em_hit_tick(s, x, 0);
    em_write_status(s);
    if (g_live == s) {
        g_live = 0;
        g_live_ext = 0;
    }
}

void enemy_mirror_unlink(void) {
    if (g_live && g_live_ext) {
        em_release_all(g_live, g_live_ext, 1);
        em_hit_tick(g_live, g_live_ext, 0);
    }
    g_live = 0;
    g_live_ext = 0;
}

// --- Batch C: Kargaroc BB (enemy-mirror-batch-c.json "bb", R-10..R-18) -------
// An fopEn_enemy_c with no damagereaction (d_a_bb.h:13-108). Its execute
// builds the base matrix from current.angle, never shape_angle (R-11,
// d_a_bb.cpp:2313-2326), its head and beak joints from C00[]/C4E inside
// every calc (nodeCallBack, :133-197, which also writes eyePos, the
// attention position and the tail root BD4), and after the calc its nine
// tail models' chain (AA8[0] = BD4[1], then tail_control, :2328-2332), all
// of which the paused copy no longer runs. Every hook bit is BB's own name
// for a generic bit no generic reader takes for this row (D7,
// wwenemy_kinds.h EMK_BB_HKD_*); ATTN and EXTRA keep their meaning.

// em_ext_slot.kind_bits for a BB slot (zeroed at adopt).
#define EM_XB_BB_SEED     0x01  // seed the tail after this frame's calc (adopt, the hidden->shown edge)
#define EM_XB_BB_HIDDEN   0x02  // the host slot was SF_HIDDEN last frame
#define EM_XB_BB_FP_DONE  0x04  // tail_control's fingerprint was checked for this adopt
#define EM_XB_BB_FP_OK    0x08  // ... and matched (else the collapsed tail, D10)
#define EM_BB_KNOCK_MIN   0.01f    // the knockback runs while unk_348 > this (d_a_bb.cpp:2269)
#define EM_BB_GROUND_NONE (-1.0e9f) // tail_control's floor (mAcch.GetGroundH() + 5, :222) out of reach
// tail_control__FP8bb_class(bb_class*) (d_a_bb.rel .text EMK_BB_TEXT_TAIL):
// a plain C++ function of the actor, r3 = the actor (C++ EABI).
typedef void (*em_bb_tail_t)(void* a);
typedef char em_check_bb_rel[(EMK_BB_MTX == EMK_MTX_TRYXZ && EMK_BB_COLL == EMK_COLL_STD &&
                              (EMK_BB_CAPS & EMK_CAP_TILT_CUR) && (EMK_BB_CAPS & EMK_CAP_SPH_COLL) &&
                              (EMK_BB_CAPS & EMK_CAP_FOPEN) && EMK_BB_TILT == EMK_AC_ANGLE_X &&
                              EMK_AC_ANGLE_X + 2 == EMK_AC_ANGLE_Y && EMK_AC_ANGLE_Y + 2 == EMK_AC_ANGLE_Z &&
                              EMK_AC_ANGLE_Y == AC_CUR_ANGLE_Y && EMK_BB_CO_FORWARD == 0 &&
                              EMK_BB_ICE != 0 && EMK_BB_INVULN != 0 && EMK_BB_HEAD_SPH == EMK_BB_SPH_SRC &&
                              EMK_BB_GROUND_H == 0x3B8 + 0x94 && EMK_BB_TAIL_LEN == 10 &&
                              EMK_BB_TAIL_ROOT1 == EMK_BB_TAIL_ROOT + 12 && (EMK_BB_HEAD_ROT_LEN & 1) == 0 &&
                              EMK_BB_HEAD_ROT + EMK_BB_HEAD_ROT_LEN <= EMK_BB_BEAK &&
                              EMK_BB_ATTACK_TIMER == EMK_BB_TIMERS + 2 && EMK_BB_ATTACK_SEED < 0x8000 &&
                              EMK_AC_SUB_METHOD == 0xEC && EMK_METHOD_EXECUTE == 0x08 &&
                              EMK_BB_TEXT_TAIL < EMK_BB_TEXT_EXEC && (EMK_BB_TAIL_BLR_OFF & 3) == 0 &&
                              EMK_BB_CLASS_SIZE > EMK_BB_ICE + 0x3B8) ? 1 : -1];

// R-13 (D9), em_validate's BB case: no rider (type 5 carries a Moblin, 6 a
// Bokoblin, d_a_bb.cpp:2739-2754, both local on each game), not the carried
// state 0x2DF, not mbDead (0xC7E), not in a knockback (unk_348, :2269-2294).
// Re-run every frame on an adopted copy, but on its own fields, which the
// pause freezes and no BB hook writes (em_bb_handback only at release): as
// em_tn_gate, no health term (em_die_quiet zeroes it on its first frame,
// before this gate would run again; Go's listing refuses a dead host bird
// and SF_DYING carries the death). The ice timers are em_adopt's ERR_ICE.
// Draw and Execute return while 0x2F2 != 0 (:611-614, 2242-2249): a copy
// still hidden here while the host's is shown is refused at adoption (its
// switch differs on this game); one adopted hidden is kept when the host
// reveals (em_kind_fields then clears the byte).
int em_bb_gate(const em_slot* sl, void* a, const em_kind* k) {
    u8 t = AF(a, u8, k->gate);
    if (t == EMK_BB_TYPE_RIDER_MO2 || t == EMK_BB_TYPE_RIDER_BK) return 0;
    if (AF(a, u8, EMK_BB_RIDER) != 0 || AF(a, u8, EMK_BB_DEAD) != 0 || AF(a, f32, EMK_BB_KNOCK) > EM_BB_KNOCK_MIN)
        return 0;
    if (!sl->adopted && AF(a, u8, EMK_BB_DRAW_GATE) != 0 && !(sl->tgt_flags & WWENEMY_SF_HIDDEN)) return 0;
    return 1;
}

// R-11, em_adopt's BB case: unk_366/unk_368 are written only by the
// demo-only bb_key_move (d_a_bb.cpp:1973-2059, #if VERSION_DEMO), so 0 on
// retail; zeroed rather than trusted. The tail is seeded after the first
// calc (em_bb_tail: BD4 is the nodeCallBack's, a hidden-at-create copy
// has it zero until then).
void em_bb_adopt(const em_slot* sl, em_ext_slot* xs, void* a) {
    AF(a, s16, EMK_BB_M366) = 0;
    AF(a, s16, EMK_BB_M368) = 0;
    xs->kind_bits = EM_XB_BB_SEED | ((sl->tgt_flags & WWENEMY_SF_HIDDEN) ? EM_XB_BB_HIDDEN : 0);
}

// R-11, EMK_MTX_TRYXZ: T(cur.x, cur.y + mEnemyIce.m028, cur.z) *
// Ry(angle.y + unk_368 + C00[0].x / 2) * Rx(angle.x + unk_366 + C00[0].z / 2) *
// Rz(angle.z) (d_a_bb.cpp:2313-2322: the y rotation takes C00[0].x and the x
// rotation C00[0].z), from the smoothed position, yaw and tilt (current.y and
// current.x, written by em_slot_frame) and the streamed roll (current.z,
// em_consume_owned). em_base_matrix adds setBaseScale(actor.scale).
void em_tryxz_matrix(const em_slot* sl, void* a, f32 (*m)[4]) {
    em_mtx_trans_s(m, sl->cur.x, sl->cur.y + AF(a, f32, EMK_BB_ICE_Y), sl->cur.z);
    mDoMtx_YrotM(m, (s16)(sl->cur_angle + AF(a, s16, EMK_BB_M368) + AF(a, s16, EMK_BB_HEAD_ROT + 0) / 2));
    mDoMtx_XrotM(m, (s16)(sl->cur_tilt + AF(a, s16, EMK_BB_M366) + AF(a, s16, EMK_BB_HEAD_ROT + 4) / 2));
    mDoMtx_ZrotM(m, AF(a, s16, EMK_AC_ANGLE_Z));
}

// R-11 pre_calc, behind EMK_BB_HKD_BEAK, before the matrix and calc: the
// execute zeroes C00[0..10] after its calc and rebuilds them (wobble, head
// shake, look, jaw) for the next one (d_a_bb.cpp:2410-2500); paused, the
// adoption frame's values would hold the head and beak. Zero is the rest
// pose: C00 and the beak's C4E (KUCHIA/KUCHIB, :182-188).
void em_bb_pre_calc(void* a) {
    int n;
    for (n = 0; n < EMK_BB_HEAD_ROT_LEN; n += 2) AF(a, s16, EMK_BB_HEAD_ROT + n) = 0;
    AF(a, s16, EMK_BB_BEAK) = 0;
}

// daBb_Execute through the copy's own profile (sub_method -> mpExecuteFunc,
// f_op_actor.h:274, f_pc_method.h:13), tail_control at its fixed distance
// in the same .text (symbols.txt:8,10,11,44), or 0.
static u32 em_bb_tc(const void* a) {
    u32 sm = AF(a, u32, EMK_AC_SUB_METHOD);
    u32 exec;
    if (!em_in_mem1(sm)) return 0;
    exec = AF(sm, u32, EMK_METHOD_EXECUTE);
    if (!em_in_mem1(exec) || exec < EM_MEM1_LO + EMK_BB_TEXT_EXEC) return 0;
    return exec - EMK_BB_TEXT_EXEC + EMK_BB_TEXT_TAIL;
}

// R-14 c (D10): the retail GZLE d_a_bb.rel only (SHA-1 4271e18f...,
// tww config.yml:1158-1159): daBb_Execute's and tail_control's prologue
// words and tail_control's blr (offline: the batch C facts, "Synthesis
// verifications"). Another revision (D44J01's Execute is at 0x6C10) fails
// and gets the collapsed tail.
int em_bb_fp_ok(const void* a) {
    u32 tc = em_bb_tc(a);
    u32 exec;
    if (!tc || !em_in_mem1(tc) || !em_in_mem1(tc + EMK_BB_TAIL_BLR_OFF)) return 0;
    exec = tc - EMK_BB_TEXT_TAIL + EMK_BB_TEXT_EXEC;
    return AF(exec, u32, 0x0) == EMK_BB_FP_EXEC0 && AF(exec, u32, 0x4) == EMK_BB_FP_EXEC1 &&
           AF(exec, u32, 0x8) == EMK_BB_FP_EXEC2 && AF(exec, u32, 0xC) == EMK_BB_FP_EXEC3 &&
           AF(tc, u32, 0x0) == EMK_BB_FP_TAIL0 && AF(tc, u32, 0x4) == EMK_BB_FP_TAIL1 &&
           AF(tc, u32, 0x8) == EMK_BB_FP_TAIL2 && AF(tc, u32, 0xC) == EMK_BB_FP_TAIL3 &&
           AF(tc, u32, EMK_BB_TAIL_BLR_OFF) == EMK_BB_FP_BLR;
}

// AA8[from..9] = BD4[1], and B5C[0..9] = 0 when vel: the seed (d) and the
// collapsed tail; AA8[0] alone is the execute's own step (a).
static void em_bb_tail_fill(void* a, int all, int vel) {
    const f32* root = (const f32*)((u8*)a + EMK_BB_TAIL_ROOT1);
    f32* pos = (f32*)((u8*)a + EMK_BB_TAIL_POS);
    f32* v = (f32*)((u8*)a + EMK_BB_TAIL_VEL);
    int n;
    for (n = 0; n < (all ? EMK_BB_TAIL_LEN : 1) * 3; n++) pos[n] = root[n % 3];
    if (vel)
        for (n = 0; n < EMK_BB_TAIL_LEN * 3; n++) v[n] = 0.0f;
}

// R-14, after McaMorf_calc (BD4 is fresh), dying frames too, unless
// EMK_BB_HKD_TAIL: (a) AA8[0] = BD4[1] (d_a_bb.cpp:2328-2330); (d) the seed
// AA8[1..9] = BD4[1], B5C[0..9] = 0 (tail_control loops 1..9, :230; tail_
// draw 0..8, :293) on the first frame after adopt and on the host's
// hidden->shown edge (a reveal elsewhere would whip the chain); (c) once
// per adopt the fingerprint (em_bb_fp_ok; a miss bumps the block's
// saturating WWENEMY_FP_MISS_OFF), then tail_control(a) when it matched,
// after (b) the ground clamp m_ground_h = -1e9 unless EMK_BB_HKD_EXTRA (the
// copy's mAcch is the adoption frame's, :222; the real floor is the host's
// business); else the collapsed tail onto BD4[1]. (e) A dying copy's tail
// collapses too unless EXTRA: tail_draw keeps its fixed tial_scale while
// the body shrinks (:288-306).
void em_bb_tail(const em_slot* sl, em_ext_slot* xs, void* a, u8 hkd, int dying) {
    u8 b = xs->kind_bits;
    int collapse;
    u32 tc;
    if (hkd & EMK_BB_HKD_TAIL) return;
    if (sl->tgt_flags & WWENEMY_SF_HIDDEN) {
        b |= EM_XB_BB_HIDDEN;
    } else {
        if (b & EM_XB_BB_HIDDEN) b |= EM_XB_BB_SEED;
        b &= (u8)~EM_XB_BB_HIDDEN;
    }
    if (!(b & EM_XB_BB_FP_DONE)) {
        b |= EM_XB_BB_FP_DONE;
        if (em_bb_fp_ok(a)) b |= EM_XB_BB_FP_OK;
        else em_sat_inc((u8*)(EB_ADDR + WWENEMY_FP_MISS_OFF));
    }
    collapse = !(b & EM_XB_BB_FP_OK) || (dying && !(hkd & EMK_BB_HKD_EXTRA));
    em_bb_tail_fill(a, collapse || (b & EM_XB_BB_SEED), collapse || (b & EM_XB_BB_SEED));
    xs->kind_bits = b & (u8)~EM_XB_BB_SEED;
    if (collapse || (tc = em_bb_tc(a)) == 0) return;
    if (!(hkd & EMK_BB_HKD_EXTRA)) AF(a, f32, EMK_BB_GROUND_H) = EM_BB_GROUND_NONE;
    ((em_bb_tail_t)tc)(a);
}

// R-17, em_handback's BB case unless EMK_BB_HKD_HANDBACK: damage_check's
// stun recovery (d_a_bb.cpp:2180-2184): the attack state with its timer
// seeded (vanilla l_bbHIO.unk_16 + cM_rndF(unk_18 - unk_16), :41-42; the
// HIO is the module's .bss, so its low end, EMK_BB_ATTACK_SEED), the path
// flag 0x35D and the attack step 0x2F1 (0: bb_atack_move's case 0 sets its
// own FLY02 anim, :1376-1381), 0x2F0, the knockback 0x348, the bank 0x34C,
// the head shake 0xC58, C00[]/C4E, both Tg spheres' hits (damage_check
// reads them on the first live frame, :2071) and the tail seed. Never the
// draw gate 0x2F2: a hidden copy keeps its own byte3 + 1 for its own
// switch test (em_kind_fields clears it only while the host is shown).
void em_bb_handback(void* a) {
    AF(a, u8, EMK_BB_STATE) = EMK_BB_STATE_ATTACK;
    AF(a, s16, EMK_BB_ATTACK_TIMER) = EMK_BB_ATTACK_SEED;
    AF(a, s8, EMK_BB_M35D) = 0;
    AF(a, s8, EMK_BB_M2F1) = 0;
    AF(a, s8, EMK_BB_M2F0) = 0;
    AF(a, f32, EMK_BB_KNOCK) = 0.0f;
    AF(a, f32, EMK_BB_M34C) = 0.0f;
    AF(a, f32, EMK_BB_C58) = 0.0f;
    em_bb_pre_calc(a);
    dCcD_GObjInf_ClrTgHit((u8*)a + EMK_BB_HEAD_SPH);
    dCcD_GObjInf_ClrTgHit((u8*)a + EMK_BB_TG_CYL);
    em_bb_tail_fill(a, 1, 1);
}

// R-18, em_extra_anm's BB case (behind HKD_EXTRA, never dying): the eye
// btp frame Draw enters (d_a_bb.cpp:625), which bb_eye_tex_anm (an execute
// step) would advance, back to the open eye instead of a frozen blink.
void em_bb_extra(void* a) {
    AF(a, u8, EMK_BB_EYE_BTP) = 0;
}

// --- Batch C: Magtail MT (enemy-mirror-batch-c.json "mt", R-19) -- THE FIRST CUT
// Everything MT is in this section, the EM_XF_MT_BALL bit and one
// `case EMK_KIND_MT:` / `k->id == EMK_KIND_MT` line per dispatch point; the
// cut (R-20) deletes them with EM_ROW(MT) (CONTRACT.md section 7). Offsets
// are the retail d_a_mt.rel's (SHA-1 9d7de862..., config.yml:1509-1510; the
// decomp has stubs only, research/C1-magtail.md), "mt.rel 0xNNNN" its .text
// offsets; they are unverified live until P0-MT. mt_class: eight segment
// models (head 0, tail 7) at the morfs EMK_MT_MORF + 4i, of which only the
// head's is animated; Execute writes all eight base matrices
// (body_control1..5) and Draw only enters them (mt.rel 0x33C0-0x3460), so a
// paused copy is rigid without em_mt_matrix. One dCcD_Stts (0xE0C) owns 8
// body spheres and the eye sphere.

// Execute-side fields and the values its recipe takes (mt.rel, annotated).
#define EM_MT_HIT_COOLDOWN 0x460  // s16: 5 after any hit; the body spheres sit at R -200 meanwhile (mt.rel 0x21A0-0x21C4)
#define EM_MT_WIGGLE      0x468   // s16 the head's yaw wiggle: pushed into the ring (0x1F80-0x1F90), the head's Ry in FIGHT (0x1210)
#define EM_MT_M330        0x330   // f32, 0 at the curl (0x4E80)
#define EM_MT_M478        0x478   // f32, 0 at the curl (0x4E74)
#define EM_MT_M48A        0x48A   // s16, 0 at the curl (0x4E7C)
#define EM_MT_TAIL_SCALE  0x18F0  // f32 FIGHT's extra scale on segment 7 (0x1248-0x125C)
#define EM_MT_TYPE        0x2B4   // u8 type (params & 0xFF): bit0 clear = the fire Magtail (0x71DC-0x71E4)
#define EM_MT_HEAD_SCALE  0.8f    // l_HIO +0x1C (HIO ctor 0x0148-0x014C, rodata 0xC): segment 0's scaleM
#define EM_MT_EYE_FWD     30.0f   // eyePos = head * (0, 0, 30) (rodata 0x18, 0x20EC-0x2118)
#define EM_MT_HEAD_FWD    100.0f  // body sph[0] centre = head * (0, 0, 100) (rodata 0x48, 0x2140-0x2178)
#define EM_MT_HEAD_R      50.0f   // body sph[0] R (rodata 0x44, 0x2180); EMK_MT_BODY_R 25 is sph[1..7]'s (l_HIO +0x48)
#define EM_MT_EYE_CENTER  (EMK_MT_HEAD_SPH + CC_SHAPE_CENTER)   // 0x18C0 (0x211C-0x2124)
#define EM_MT_RING_MASK   (EMK_MT_RING_LEN - 1)
// The trail offsets per segment (.data move_ad / move_ad2, s32 in the REL).
static const s8 EM_MT_MOVE_AD[EMK_MT_SEGS] = { 0, -6, -12, -18, -24, -30, -36, -42 };
static const s8 EM_MT_MOVE_AD2[EMK_MT_SEGS] = { 0, -3, -6, -9, -12, -15, -18, -21 };
// cM_atan2s__Fff (symbols.txt:8315): FIGHT's chain angles (body_control2,
// 0x1034, 0x10B0), as cM_atan2s(x, z).
typedef s16 (*em_atan2s_t)(f32 y, f32 x);
#define cM_atan2s         ((em_atan2s_t)0x802460D0)
// setSimple__13dPa_control_cFUsPC4cXyzUcRC8_GXColorRC8_GXColori
// (symbols.txt:2581) on dComIfGp_getParticle() (play +0x4824 =
// g_dComIfG_gameInfo +0x5AC4, d_com_inf_game.h:775) with g_whiteColor
// (symbols.txt:18179) twice and 0: the fire Magtail's per-segment flames
// (mt.rel 0x7288-0x72F4).
typedef void (*em_set_simple_t)(void* pa, u16 name, const cXyz* pos, u8 alpha, const void* prm, const void* env, int x);
#define dPa_control_setSimple ((em_set_simple_t)0x8007DBC4)
#define EM_PARTICLE       (*(volatile u32*)0x803CA6CC)
#define EM_WHITE_COLOR    ((const void*)0x803F61A8)
// em_ext_slot.kind_bits for an MT slot: EM_XB_MT_CUSTODY (enemy_mirror.h),
// handed back curled and watched for its own death (collider code
// EMK_HIT_MT_KILL).
typedef char em_check_mt_rel[(EMK_MT_ID == EMK_KIND_MT && sizeof(EMK_MT_ARC) <= 4 && EM_PLAIN_OK(MT) &&
                              EMK_MT_MTX == EMK_MTX_MT && EMK_MT_COLL == EMK_COLL_MT && EMK_MT_ATTACH == EMK_ATTACH_NONE &&
                              EMK_MT_MTX != EMK_MTX_TRYXZ && EMK_MT_COLL != EMK_COLL_CC && EMK_MT_COLL != EMK_COLL_STD &&
                              !(EMK_MT_CAPS & (EMK_CAP_DR | EMK_CAP_TILT_CUR | EMK_CAP_MODEL_EQ)) &&
                              EMK_MT_TILT == EMK_AC_SHAPE_X && EM_MT_EYE_CENTER == 0x18C0 &&
                              EMK_MT_BODY_SPH_CENTER == EMK_MT_BODY_SPH + CC_SHAPE_CENTER &&
                              EMK_MT_TG_CYL == EMK_MT_BODY_SPH && EMK_MT_SEGS == 8 &&
                              EMK_MT_RING_LEN == 64 && EM_MT_RING_MASK == 0x3F &&
                              EMK_HIT_MT_KILL > EMK_HIT_SPECIAL && EMK_HIT_MT_KILL < 0x100 &&
                              !(EM_XB_MT_CUSTODY & (EM_XB_BB_SEED | EM_XB_BB_HIDDEN | EM_XB_BB_FP_DONE | EM_XB_BB_FP_OK)) &&
                              EMK_MT_MODE_MOVE == 0 && EMK_MT_MODE_FIGHT == 1 && EMK_MT_MODE_PINNED == 6 &&
                              EMK_MT_GATE + 1 == EMK_MT_ACTION && EMK_MT_COLOR_A + 4 == EMK_MT_COLOR_B &&
                              // flags2 bits 0-1 are MT's colour phase, which the S2
                              // readers of SF2_HAS_WEAPON / SF2_PLAY_SKIP / blur
                              // never see: no play_skip, blur or attach.
                              EMK_MT_F2_ATTN < WWENEMY_SF2_LIGHT && EMK_MT_F2_DIVE < WWENEMY_SF2_LIGHT &&
                              EMK_MT_PLAY_SKIP == 0 && EMK_MT_BLUR_ON == 0 && EMK_MT_ATTACH == EMK_ATTACH_NONE) ? 1 : -1];

// m * (0, 0, z): the translation of m * T(0, 0, z) (MtxPosition /
// PSMTXMultVec on a point on the local z axis).
static void em_mtx_z(f32 (*m)[4], f32 z, cXyz* out) {
    out->x = m[0][2] * z + m[0][3];
    out->y = m[1][2] * z + m[1][3];
    out->z = m[2][2] * z + m[2][3];
}

static u8* em_mt_body(void* a, int i) {
    return (u8*)a + EMK_MT_BODY_SPH + i * EMK_MT_BODY_SPH_STRIDE;
}

// MT-1, em_validate's MT case (0 = ERR_GATE; the AM2 precedent): not
// hidden until its appear switch (0x2BB != 0: Execute returns before any
// logic, Draw checks it too, mt.rel 0x61DC-0x6218, 0x338C), in a mode the
// mirror builds (MOVE, FIGHT, PINNED: 0x6460-0x6538), no death flag
// (0x18F8, 0x747C-0x7498) or killed flag (0x1CBC, 0x7CF4-0x7DF8), not
// shrinking (0x1906, the light arrow, 0x5CD8-0x5D10), not carried. All the
// copy's own fields: em_mt_consume writes only modes the gate admits.
int em_mt_gate(void* a, const em_kind* k) {
    u8 mode = AF(a, u8, k->gate);
    if (AF(a, u8, EMK_MT_APPEAR) != 0) return 0;
    if (mode != EMK_MT_MODE_MOVE && mode != EMK_MT_MODE_FIGHT && mode != EMK_MT_MODE_PINNED) return 0;
    if (AF(a, s8, EMK_MT_DEATH) != 0 || AF(a, u8, EMK_MT_KILLED) != 0 || AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0)
        return 0;
    return !(AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY);
}

// Every hit record of the eye and / or the eight body spheres.
void em_mt_clr_hits(void* a, int eye, int body) {
    int i;
    if (eye) dCcD_GObjInf_ClrTgHit((u8*)a + EMK_MT_HEAD_SPH);
    if (body)
        for (i = 0; i < EMK_MT_SEGS; i++) dCcD_GObjInf_ClrTgHit(em_mt_body(a, i));
}

// MT-1 adopt: no lunge blur (u8 0x18D4: the br models draw while != 0,
// mt.rel 0x3028-0x3030), no hit from its own last frame on any sphere, and
// the ring (64) and chain (8) moved by the snap delta, so the first frame's
// segments trail from where the host's head is, not from the copy's spot.
void em_mt_adopt(void* a, f32 dx, f32 dy, f32 dz) {
    f32* r = (f32*)((u8*)a + EMK_MT_RING_POS);
    f32* c = (f32*)((u8*)a + EMK_MT_CHAIN_POS);
    int i;
    AF(a, u8, EMK_MT_BLUR) = 0;
    em_mt_clr_hits(a, 1, 1);
    for (i = 0; i < EMK_MT_RING_LEN * 3; i += 3) {
        r[i + 0] += dx;
        r[i + 1] += dy;
        r[i + 2] += dz;
    }
    for (i = 0; i < EMK_MT_SEGS * 3; i += 3) {
        c[i + 0] += dx;
        c[i + 1] += dy;
        c[i + 2] += dz;
    }
}

// The engage latch's snapshot (body_control1 with +0xC01, mt.rel
// 0x21D8-0x2218): each segment's ring entry becomes its FIGHT chain link.
// The ring's last push is idx - 1 (em_mt_matrix advanced past it).
static void em_mt_snapshot(void* a) {
    const s8* ad = AF(a, u8, EMK_MT_MOVE2_GATE) ? EM_MT_MOVE_AD2 : EM_MT_MOVE_AD;
    u32 idx = AF(a, u32, EMK_MT_RING_IDX) - 1;
    int i, n;
    for (i = 0; i < EMK_MT_SEGS; i++) {
        u32 j = (idx + (u32)(s32)ad[i]) & EM_MT_RING_MASK;
        for (n = 0; n < 3; n++) {
            AF(a, f32, EMK_MT_CHAIN_POS + i * 12 + n * 4) = AF(a, f32, EMK_MT_RING_POS + j * 12 + n * 4);
            AF(a, s16, EMK_MT_CHAIN_ANGLE + i * 6 + n * 2) = AF(a, s16, EMK_MT_RING_ANGLE + j * 6 + n * 2);
        }
    }
}

// em_consume_owned's MT case (the annex matched): aux[0] mode | action <<
// 8, written only for a mode the gate admits (MOVE, FIGHT, PINNED: a host
// curling is EM_XF_MT_BALL, its death SF_DYING; either way the copy keeps
// its last mode), and on MOVE -> FIGHT/PINNED the engage snapshot (other
// Magtails' mt_check counts FIGHT copies, mt.rel 0x02D8-0x038C); aux[1] the
// ball timer 0x466; aux[3] shape_angle.z; unless EMK_MT_HKD_EXTRA the colour
// phase (flags2 bits 0-1) and the s32 btk/brk frames 0x2E8/0x2EC from aux[2]
// (Draw enters them, mt.rel 0x3464-0x3574).
void em_mt_consume(const em_slot* sl, void* a, u32 aux, u8 hkd) {
    u16 w0 = (u16)AX_S16(aux + 0);
    u16 w2 = (u16)AX_S16(aux + 4);
    u8 mode = (u8)w0;
    if (mode == EMK_MT_MODE_MOVE || mode == EMK_MT_MODE_FIGHT || mode == EMK_MT_MODE_PINNED) {
        if (AF(a, u8, EMK_MT_GATE) == EMK_MT_MODE_MOVE && mode != EMK_MT_MODE_MOVE) em_mt_snapshot(a);
        AF(a, u8, EMK_MT_GATE) = mode;
        AF(a, u8, EMK_MT_ACTION) = (u8)(w0 >> 8);
    }
    AF(a, s16, EMK_MT_BALL_TIMER) = AX_S16(aux + 2);
    AF(a, s16, EMK_AC_SHAPE_Z) = AX_S16(aux + 6);
    if (!(hkd & EMK_MT_HKD_EXTRA)) {
        AF(a, u8, EMK_MT_COLOR_PHASE) = sl->tgt_flags2 & EMK_MT_F2_PHASE;
        AF(a, s32, EMK_MT_COLOR_A) = (u8)w2;
        AF(a, s32, EMK_MT_COLOR_B) = (u8)(w2 >> 8);
    }
}

// FIGHT's chain link (body_control2, mt.rel 0x0F50-0x1164) without its
// velocity, wiggle offset or LineCross: link p is pulled to 35 units from
// prev along prev -> p; its angles face prev (y = yaw + 0x8000, x = -pitch,
// 0x110C-0x111C). The xz length is the projection onto the yaw (no sqrt in
// the REL: dx = L sin(yaw), dz = L cos(yaw)).
static void em_mt_link(f32* p, s16* r, const f32* prev) {
    f32 dx = p[0] - prev[0], dy = p[1] - prev[1], dz = p[2] - prev[2];
    s16 yaw = cM_atan2s(dx, dz);
    f32 sy = cM_ssin(yaw), cy = cM_scos(yaw);
    s16 pitch = (s16)-cM_atan2s(dy, dx * sy + dz * cy);
    f32 cp = cM_scos(pitch) * (f32)EMK_MT_CHAIN_LINK;
    p[0] = prev[0] + sy * cp;
    p[1] = prev[1] - cM_ssin(pitch) * (f32)EMK_MT_CHAIN_LINK;
    p[2] = prev[2] + cy * cp;
    r[0] = (s16)-pitch;
    r[1] = (s16)(yaw + 0x8000);
}

// MT-2, em_base_matrix's EMK_MTX_MT case unless EMK_MT_HKD_MTX. The head
// is the smoothed position and shape_angle (y the slot angle, x the slot
// tilt written here: the row has no tilt cap; z from aux[3]). MOVE
// (body_control1, mt.rel 0x1F30-0x2258): the head is pushed into the ring
// at idx with the copy's wiggle, segment i reads ring (idx + move_ad[i]) &
// 63 (move_ad2 while u8 0xC00 != 0), and idx advances unless the host dives
// (EMK_MT_F2_DIVE: 0x48E != 0). FIGHT and PINNED: chain[0] = the head,
// links 1..7 em_mt_link (body_control2's shape; PINNED's body_control5 is
// unverified and takes the same). Each matrix: T(p) * Ry * Rx * Rz *
// Ry(wiggle: every segment's ring entry in MOVE, the head's 0x468 in FIGHT)
// * S(head 0.8, else (s, s * 0x620[i], 1) with s = 0x600[i]; FIGHT's tail
// also 0x18F0) * T(0, 0, f32 0x470) into its model, whose base scale is the
// actor's times the quiet death's scale. The eye centre and eyePos are head
// * (0, 0, 30), body sph[0] head * (0, 0, 100) R 50, sph[1..7] at their
// segment's p, R 25 (0x20EC-0x21C4), and the anchors 0x350[i] the
// translations (+10000 y while PINNED, 0x724C-0x7284), the attention
// position eyePos (0x6E88-0x6E9C). EMK_MT_HKD_CHAIN
// leaves segments 1..7 where they are. Unless EMK_MT_HKD_FIRE, on a living
// fire Magtail (type bit0 clear), the per-segment flames 0x8058 at
// anchors 0..6 and 0x8059 at 1..6 (0x71DC-0x72F4).
void em_mt_matrix(const em_slot* sl, void* a, u8 hkd, f32 scale) {
    u8 mode = AF(a, u8, EMK_MT_GATE);
    int chain = mode != EMK_MT_MODE_MOVE;
    u32 idx = AF(a, u32, EMK_MT_RING_IDX) & EM_MT_RING_MASK;
    const s8* ad = AF(a, u8, EMK_MT_MOVE2_GATE) ? EM_MT_MOVE_AD2 : EM_MT_MOVE_AD;
    f32* cpos = (f32*)((u8*)a + EMK_MT_CHAIN_POS);
    s16* cang = (s16*)((u8*)a + EMK_MT_CHAIN_ANGLE);
    f32 m[3][4];
    cXyz c;
    int i;
    AF(a, s16, EMK_AC_SHAPE_X) = sl->cur_tilt;
    if (!chain) {
        AF(a, f32, EMK_MT_RING_POS + idx * 12 + 0) = sl->cur.x;
        AF(a, f32, EMK_MT_RING_POS + idx * 12 + 4) = sl->cur.y;
        AF(a, f32, EMK_MT_RING_POS + idx * 12 + 8) = sl->cur.z;
        AF(a, s16, EMK_MT_RING_ANGLE + idx * 6 + 0) = AF(a, s16, EMK_AC_SHAPE_X);
        AF(a, s16, EMK_MT_RING_ANGLE + idx * 6 + 2) = AF(a, s16, EMK_AC_SHAPE_Y);
        AF(a, s16, EMK_MT_RING_ANGLE + idx * 6 + 4) = AF(a, s16, EMK_AC_SHAPE_Z);
        AF(a, s16, EMK_MT_RING_WIGGLE + idx * 2) = AF(a, s16, EM_MT_WIGGLE);
    } else {
        cpos[0] = sl->cur.x;
        cpos[1] = sl->cur.y;
        cpos[2] = sl->cur.z;
        cang[0] = AF(a, s16, EMK_AC_SHAPE_X);
        cang[1] = AF(a, s16, EMK_AC_SHAPE_Y);
        cang[2] = AF(a, s16, EMK_AC_SHAPE_Z);
    }
    for (i = 0; i < EMK_MT_SEGS; i++) {
        void* morf = AF(a, void*, EMK_MT_MORF + i * 4);
        u8* model;
        const f32* p;
        const s16* r;
        s16 wig;
        if (i > 0 && (hkd & EMK_MT_HKD_CHAIN)) break;
        if (!em_in_mem1((u32)morf) || !em_in_mem1(AF(morf, u32, MORF_MODEL))) continue;
        model = AF(morf, u8*, MORF_MODEL);
        if (!chain) {
            u32 j = (idx + (u32)(s32)ad[i]) & EM_MT_RING_MASK;
            p = (const f32*)((u8*)a + EMK_MT_RING_POS + j * 12);
            r = (const s16*)((u8*)a + EMK_MT_RING_ANGLE + j * 6);
            wig = AF(a, s16, EMK_MT_RING_WIGGLE + j * 2);
        } else {
            if (i > 0) em_mt_link(cpos + i * 3, cang + i * 3, cpos + (i - 1) * 3);
            p = cpos + i * 3;
            r = cang + i * 3;
            wig = i == 0 ? AF(a, s16, EM_MT_WIGGLE) : 0;
        }
        em_mtx_trans_s(m, p[0], p[1], p[2]);
        mDoMtx_YrotM(m, r[1]);
        mDoMtx_XrotM(m, r[0]);
        mDoMtx_ZrotM(m, r[2]);
        if (wig) mDoMtx_YrotM(m, wig);
        if (i == 0) {
            em_mtx_scale_m(m, EM_MT_HEAD_SCALE, EM_MT_HEAD_SCALE, EM_MT_HEAD_SCALE);
        } else {
            f32 sa = AF(a, f32, EMK_MT_SCALE_A + i * 4);
            em_mtx_scale_m(m, sa, sa * AF(a, f32, EMK_MT_SCALE_B + i * 4), 1.0f);
            if (chain && i == EMK_MT_SEGS - 1) {
                f32 t = AF(a, f32, EM_MT_TAIL_SCALE);
                em_mtx_scale_m(m, t, t, t);
            }
        }
        em_mtx_trans_m(m, 0.0f, 0.0f, AF(a, f32, EMK_MT_SEG_Z));
        PSMTXCopy(m, model + J3DMODEL_BASE_TR_MTX_OFFSET);
        AF(model, f32, J3DMODEL_BASE_SCALE + 0) = AF(a, f32, AC_SCALE + 0) * scale;
        AF(model, f32, J3DMODEL_BASE_SCALE + 4) = AF(a, f32, AC_SCALE + 4) * scale;
        AF(model, f32, J3DMODEL_BASE_SCALE + 8) = AF(a, f32, AC_SCALE + 8) * scale;
        AF(a, f32, EMK_MT_ANCHOR + i * 12 + 0) = m[0][3];
        AF(a, f32, EMK_MT_ANCHOR + i * 12 + 4) = m[1][3] + (mode == EMK_MT_MODE_PINNED ? (f32)EMK_MT_HIDE_Y : 0.0f);
        AF(a, f32, EMK_MT_ANCHOR + i * 12 + 8) = m[2][3];
        if (i == 0) {
            em_mtx_z(m, EM_MT_EYE_FWD, (cXyz*)((u8*)a + EMK_AC_EYE_POS));
            cM3dGSph_SetC((u8*)a + EM_MT_EYE_CENTER, (const cXyz*)((u8*)a + EMK_AC_EYE_POS));
            AF(a, f32, EMK_MT_HEAD_SPH + CC_SHAPE_RADIUS) = (f32)EMK_MT_EYE_R;
            em_mtx_z(m, EM_MT_HEAD_FWD, &c);
            cM3dGSph_SetC(em_mt_body(a, 0) + CC_SHAPE_CENTER, &c);
            AF(em_mt_body(a, 0), f32, CC_SHAPE_RADIUS) = EM_MT_HEAD_R;
            // Execute's attention position is eyePos (mt.rel 0x6E88-0x6E9C).
            AF(a, f32, EMK_AC_ATTN_POS + 0) = AF(a, f32, EMK_AC_EYE_POS + 0);
            AF(a, f32, EMK_AC_ATTN_POS + 4) = AF(a, f32, EMK_AC_EYE_POS + 4);
            AF(a, f32, EMK_AC_ATTN_POS + 8) = AF(a, f32, EMK_AC_EYE_POS + 8);
        } else {
            cM3dGSph_SetC(em_mt_body(a, i) + CC_SHAPE_CENTER, (const cXyz*)p);
            AF(em_mt_body(a, i), f32, CC_SHAPE_RADIUS) = (f32)EMK_MT_BODY_R;
        }
    }
    if (!chain && !(sl->tgt_flags2 & EMK_MT_F2_DIVE)) AF(a, u32, EMK_MT_RING_IDX) = (idx + 1) & EM_MT_RING_MASK;
    if (!(hkd & EMK_MT_HKD_FIRE) && scale == 1.0f && !(AF(a, u8, EM_MT_TYPE) & 1) && em_in_mem1(EM_PARTICLE)) {
        for (i = 0; i < EMK_MT_SEGS - 1; i++) {
            const cXyz* at = (const cXyz*)((u8*)a + EMK_MT_ANCHOR + i * 12);
            dPa_control_setSimple((void*)EM_PARTICLE, EMK_MT_FIRE_A, at, 0xFF, EM_WHITE_COLOR, EM_WHITE_COLOR, 0);
            if (i > 0)
                dPa_control_setSimple((void*)EM_PARTICLE, EMK_MT_FIRE_B, at, 0xFF, EM_WHITE_COLOR, EM_WHITE_COLOR, 0);
        }
    }
}

// MT-3, em_colliders' EMK_COLL_MT case unless EMK_MT_HKD_COLL (centres and
// radii are em_mt_matrix's): the eight body spheres (Tg + Co) and the eye
// (Tg) into cCcS as body_control1 Sets them (mt.rel 0x2134-0x21D4), every
// body At Set bit off (the bite is the host's: its PHIT hurts, never the
// copy), the host head's Shield bit (EMK_MT_F2_SHIELD: on in FIGHT,
// 0x682C-0x68A0) on body sph[0] (0xEDC). A pending hit withholds its
// collider: all eight bodies for code 2 (em_hits clears them all), the eye
// for code 1; the eye also while the host is invulnerable.
void em_mt_colliders(const em_slot* sl, void* a) {
    u8* s0 = em_mt_body(a, 0);
    int i;
    AF(s0, u32, EMK_GTG_SPRM) =
        (AF(s0, u32, EMK_GTG_SPRM) & ~(u32)EMK_GTG_SPRM_SHIELD) | ((sl->tgt_flags2 & EMK_MT_F2_SHIELD) ? EMK_GTG_SPRM_SHIELD : 0);
    for (i = 0; i < EMK_MT_SEGS; i++) {
        u8* sph = em_mt_body(a, i);
        AF(sph, u32, CC_AT_SPRM) &= ~1u;
        if (sl->hit_withheld != EMK_HIT_TG) cCcS_Set(EM_CCS, sph);
    }
    if (sl->hit_withheld != EMK_HIT_HEAD && !(sl->tgt_flags & WWENEMY_SF_INVULN))
        cCcS_Set(EM_CCS, (u8*)a + EMK_MT_HEAD_SPH);
}

// A Tg hit whose attacker collider belongs to a Magtail (the client's own
// carried ball thrown at the copy: damage_check's type 6, mt.rel
// 0x5D58-0x5D98) is not forwarded (design mt "gaps": not modelled).
static int em_mt_thrown(const u8* sph) {
    u32 obj = AF(sph, u32, CC_TG_HIT_OBJ);
    u32 stts = em_in_mem1(obj) ? AF(obj, u32, CC_STTS) : 0;
    u32 owner = em_in_mem1(stts) ? AF(stts, u32, CC_STTS_ACTOR) : 0;
    return em_in_mem1(owner) && (u16)AF(owner, s16, AC_PROC_NAME) == EMK_MT_PROC;
}

// MT-3, em_hits' MT case: the eye (EMK_HIT_HEAD) or the first body sphere
// with a Tg hit (EMK_HIT_TG; Go's mtHitCollider finds it by its hit object),
// a thrown Magtail's hit dropped. The rest of the same swing is cleared
// (every other body sphere and the eye), so the reported sphere is the only
// one with a hit object while it is pending; nothing reported: all cleared.
int em_mt_hit_collider(em_slot* sl, void* a) {
    u8* eye = (u8*)a + EMK_MT_HEAD_SPH;
    int i, hit = -1;
    (void)sl;
    if ((AF(eye, u32, CC_TG_RPRM) & 1) && !em_mt_thrown(eye)) {
        em_mt_clr_hits(a, 0, 1);
        return EMK_HIT_HEAD;
    }
    for (i = 0; i < EMK_MT_SEGS && hit < 0; i++) {
        u8* sph = em_mt_body(a, i);
        if ((AF(sph, u32, CC_TG_RPRM) & 1) && !em_mt_thrown(sph)) hit = i;
    }
    dCcD_GObjInf_ClrTgHit(eye);
    for (i = 0; i < EMK_MT_SEGS; i++)
        if (i != hit) dCcD_GObjInf_ClrTgHit(em_mt_body(a, i));
    return hit < 0 ? 0 : EMK_HIT_TG;
}

// Host, em_hit_vulnerable's MT case: the hit cooldown (5 after any hit,
// while the body spheres sit at R -200, mt.rel 0x21A0-0x21C4; C1 "timers")
// is over. The row has no invuln word (EMK_MT_INVULN 0).
int em_mt_vulnerable(void* a) {
    return AF(a, s16, EM_MT_HIT_COOLDOWN) == 0;
}

// Host, em_hit_center's MT case: collider 2 at body sph[EMK_MT_HIT_CENTER_SEG]'s
// centre (the head sphere clanks off its FIGHT Shield bit).
void em_mt_hit_center(void* a, cXyz* c) {
    const u8* ctr = em_mt_body(a, EMK_MT_HIT_CENTER_SEG) + CC_SHAPE_CENTER;
    c->x = AF(ctr, f32, 0);
    c->y = AF(ctr, f32, 4);
    c->z = AF(ctr, f32, 8);
}

// MT-4, em_handback's MT case: every sphere's hit cleared and the lunge blur
// off; then, when the host has curled (EM_XF_MT_BALL) and EMK_MT_HKD_CUSTODY
// is clear, the copy curls on this game (the BALL entry of FIGHT sub 20's
// landing, mt.rel 0x4E44-0x4F34): mode BALL, action 0, the host's ball timer
// (aux[1]), colour phase 1 and frames (aux[2]), the host's health (when
// streamed), eye HP 0, the head's Shield bit off, 0x478/0x48A/0x330 0, and
// the slot watches it (EM_XB_MT_CUSTODY). Otherwise, and always when the
// release is a reassignment (same 0: xs, the annex aux and the slot health
// are already the next actor's, em_consume), it resumes its last streamed
// mode and action (em_mt_consume).
void em_mt_handback(void* a, em_ext_slot* xs, u8 hkd, int slot, int same) {
    u32 aux = WWENEMY_ANNEX_OFF + slot * WWENEMY_ANNEX_AUX_SIZE;
    s8 hp = EB_S8(WWENEMY_SLOTS_OFF + slot * WWENEMY_SLOT_SIZE + WWENEMY_SL_HEALTH_OFF);
    u16 w2 = (u16)AX_S16(aux + 4);
    em_mt_clr_hits(a, 1, 1);
    AF(a, u8, EMK_MT_BLUR) = 0;
    if (!same || !(xs->flags_ext & EM_XF_MT_BALL) || (hkd & EMK_MT_HKD_CUSTODY)) return;
    AF(a, u8, EMK_MT_GATE) = EMK_MT_MODE_BALL;
    AF(a, u8, EMK_MT_ACTION) = 0;
    AF(a, s16, EMK_MT_BALL_TIMER) = AX_S16(aux + 2);
    AF(a, u8, EMK_MT_COLOR_PHASE) = 1;
    AF(a, s32, EMK_MT_COLOR_A) = (u8)w2;
    AF(a, s32, EMK_MT_COLOR_B) = (u8)(w2 >> 8);
    if (hp > 0) AF(a, s8, AC_HEALTH) = hp;
    AF(a, s8, EMK_MT_EYE_HP) = 0;
    AF(a, u32, EMK_MT_BODY_SHIELD_SPRM) &= ~(u32)EMK_GTG_SPRM_SHIELD;
    AF(a, f32, EM_MT_M478) = 0.0f;
    AF(a, s16, EM_MT_M48A) = 0;
    AF(a, f32, EM_MT_M330) = 0.0f;
    xs->kind_bits |= EM_XB_MT_CUSTODY;
}

// MT-4, em_slot_frame's refusal path: while the copy handed back curled is
// the slot's actor (same pid, EM_XB_MT_CUSTODY: em_consume drops it when
// the slot's pid changes, and re-adoption, once both have uncurled, zeroes
// kind_bits), the slot reports it as its actor (Go's
// readHit needs the status pid and actor), and its own death (mode DEAD,
// the bakuha flag 0x18F8 >= 2, or shrinking) once as a hit entry with
// collider EMK_HIT_MT_KILL (no Tg record; written when no other entry is
// pending). A copy that left BALL alive but is still refused (the host
// still curled) stays watched. Returns 1 while the slot watches one.
int em_mt_custody_watch(em_slot* sl, em_ext_slot* xs, void* a, int slot) {
    u32 off = WWENEMY_HITS_OFF + slot * WWENEMY_HIT_SIZE;
    if (!(xs->kind_bits & EM_XB_MT_CUSTODY)) return 0;
    sl->actor = (u32)a;
    if (AF(a, u8, EMK_MT_GATE) != EMK_MT_MODE_DEAD && AF(a, s8, EMK_MT_DEATH) < 2 &&
        AF(a, s8, EMK_MT_ICE + EMK_ICE_SHRINK) == 0)
        return 1;
    if (EB_U8(off + 3) != 0) return 1;
    xs->kind_bits &= (u8)~EM_XB_MT_CUSTODY;
    sl->hit_seq++;
    EB_U16(off + 0) = sl->hit_seq;
    EB_U8(off + 2) = EMK_HIT_MT_KILL;
    EB_U8(off + 3) = WWENEMY_HP_PENDING;
    return 1;
}

// MT-4 host, em_hit_apply's code-4 branch: the client's custody BALL died;
// the host's Magtail runs its own bakuha (flag 2 at the end of its next
// execute, mt.rel 0x747C-0x7498) only while it is still curled with no death
// flag. 0 = stale (it uncurled, or is dying already).
int em_mt_kill_apply(void* a) {
    if (AF(a, u8, EMK_MT_GATE) != EMK_MT_MODE_BALL || AF(a, s8, EMK_MT_DEATH) != 0) return 0;
    AF(a, s8, EMK_MT_DEATH) = 2;
    return 1;
}

// MT-5, before both fopAcM_delete sites (em_release's quiet-dying branch,
// em_die_quiet's last frame) unless EMK_MT_HKD_RESPAWN (inverted: set =
// skip): daMt_Delete re-creates a Magtail at home only when 0x1CBC != 0
// and its respawn bit 0x2B6 is set (mt.rel 0x7CF4-0x7DF8); its own deaths
// write it before their deletes (0x68A8-0x68F4, 0x041C-0x0454), so the
// copy respawns as the host's does.
void em_mt_mark_respawn(void* a, u8 hkd) {
    if (!(hkd & EMK_MT_HKD_RESPAWN) && AF(a, u8, EMK_MT_RESPAWN_BIT) != 0) AF(a, u8, EMK_MT_KILLED) = 1;
}
