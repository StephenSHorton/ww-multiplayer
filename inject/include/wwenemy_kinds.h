// The enemy mirror's kind table: one row of class offsets, hooks and
// capabilities per enemy class the mirror knows (design:
// ww-multiplayer-handoff/design/enemy-mirror-general.json "type_table",
// "flags", "rel_changes"; decisions D1 and D15; the allow batch's KI, AM
// and AM2 rows and hooks: enemy-mirror-allow.json "type_table",
// "rel_changes" R-1..R-16, decisions D2-D16; the TN batch's Darknut row
// and AM2 direct apply: enemy-mirror-tn.json "rel_changes" R-1..R-16,
// decisions D1-D14, "p0_findings"; batch C's Kargaroc row and the ambush
// Bokoblin defines: enemy-mirror-batch-c.json "bb", "rel_changes"
// R-6..R-20, decisions D7-D11; its Magtail row was cut by R-20 after the
// P0-MT live probe, id 8 stays reserved). enemy_mirror.c builds its
// static const em_kind EM_KINDS[] (enemy_mirror.h) from these #defines,
// and static-asserts the Moblin row against the legacy MO2_* defines
// (EMK_MO2_TG_CYL == MO2_TG_CYL, ...): a const array element is not an
// integer constant expression, so the pins live on the #defines.
//
// Shared with Go: mods/enemysync's TestKindTablePins parses this file
// (internal/mailboxgen ParseDefines: one `#define NAME <dec|0xhex>` per
// line, an optional // comment; the _ARC strings by their own pattern)
// and compares every row with the Go Kinds table and wwapi/addr. Keep
// every #define on one line, no suffixes, no expressions, no negatives.
//
// Offsets are from the actor (fopAc_ac_c) start. 0 means "the class has
// none" for an offset field (no class puts any of these at +0x0).
// Decomp citations: zeldaret/tww include/d/actor/d_a_*.h and
// src/d/actor/d_a_*.cpp, as the type_table cites them.
//
// The per-kind hook-bit pins (batch C D7) are typedef asserts at the end
// of this file, so this header includes wwenemy.h for the WWENEMY_HKD_*
// bits; the Go parser skips every line that is not a #define.
#ifndef WW_WWENEMY_KINDS_H
#define WW_WWENEMY_KINDS_H

#include "wwenemy.h"

// --- kind ids (bit k of the block's kind_mask, WWENEMY_KIND_MASK_OFF) ----
#define EMK_KIND_MO2             0
#define EMK_KIND_BK              1
#define EMK_KIND_CC              2
#define EMK_KIND_KI              3            // allow batch A1, default off (WW_ENEMY_MIRROR_KINDS ki / ki+u)
#define EMK_KIND_AM              4            // default off (am / am+u)
#define EMK_KIND_AM2             5            // default off (am2 / am2+u)
#define EMK_KIND_TN              6            // TN batch B (D1), default off (tn / tn+u); the last id the annex's HKDX bytes hold (0x1FF)
#define EMK_KIND_BB              7            // batch C, default off (bb / bb+u); hook byte WWENEMY_HKD_HI_OFF + 0
#define EMK_KIND_MT              8            // reserved, no row: batch C's Magtail, cut by R-20 after P0-MT (body sphere centres off the segment translations); hook byte WWENEMY_HKD_HI_OFF + 1 stays unread
#define EMK_KIND_COUNT           8            // rows MO2, BK, CC, KI, AM, AM2, TN, BB (the Go Kinds table); id 8 is EMK_KIND_MT's reservation
#define EMK_KIND_NONE            0xFF

// --- capability bits (row caps) ------------------------------------------
#define EMK_CAP_DR               0x01         // has a damagereaction (dr_matrix_set, mMode/mAction)
#define EMK_CAP_MODEL_EQ         0x02         // em_validate requires actor->model == morf model (CC execute NULLs actor.model, d_a_cc.cpp:2093)
#define EMK_CAP_FOPEN            0x04         // an fopEn_enemy_c: Bt fields and enemyice exist
#define EMK_CAP_WAIT_MODE1       0x08         // MO2: handback picks KWAIT for mMode 1 (enemy_mirror.c:409-425)
#define EMK_CAP_ADDR_SUCCESS     0x10         // MO2: the host hit's success test is the address compare; others use the stts owner (remote.c:1545-1553)
#define EMK_CAP_PKEY_NO_POS      0x20         // KI (D9): em_pkey hashes home.pos as zero when setID < 0x200 (a wait-type Keese rewrites home.pos, d_a_ki.cpp:675-682); Go Kind.PKeyNoPos
#define EMK_CAP_SPH_COLL         0x40         // KI: co_cyl and tg_cyl are dCcD_Sph (d_a_ki.h:78-79): COLL_STD centres them with cM3dGSph_SetC, and they have no height
#define EMK_CAP_TILT_CUR         0x80         // batch C R-12 (BB): the smoothed tilt goes to the row's TILT (current.angle.x) without EMK_CAP_DR; em_adopt seeds cur_tilt from it too

// --- hook ids --------------------------------------------------------------
#define EMK_MTX_NONE             0
#define EMK_MTX_DR               1            // dr_matrix_set(a + dr) (0x80022460) + PSMTXCopy calc_mtx -> model+0x24 + McaMorf_calc
#define EMK_MTX_CC               2            // draw_SUB recipe (d_a_cc.cpp:80-104, D11) into a stack Mtx -> m2B4 model+0x24 + McaMorf_calc
#define EMK_MTX_PLAIN            3            // T(cur) * Ry(cur_angle) * Rx(s16 +0x20C) * Rz(s16 +0x210), no scale fold (D16) -> model+0x24 + McaMorf_calc (d_a_ki.cpp:1053-1061, d_a_am.cpp:97-112, d_a_am2.cpp:73-87); HKD_MTX_PLAIN skips it
#define EMK_MTX_MODEL            4            // reserved: a bare J3DModel (warship), not compiled in this build
#define EMK_MTX_TRYXZ            5            // batch C R-11 (BB): T(cur.x, cur.y + f32 ice+0x28, cur.z) * Ry(cur.y + s16 0x368 + C00[0].x/2) * Rx(cur.x + s16 0x366 + C00[0].z/2) * Rz(cur.z), setBaseScale(actor.scale) (d_a_bb.cpp:2313-2326); BB's HKD_MTX skips it
#define EMK_COLL_NONE            0
#define EMK_COLL_STD             1            // Co ahead by co_forward, Tg at cur, head sphere at head_center, defence at def_center (enemy_mirror.c:803-831); A1 (D7): no Tg Set when tg == co, no head/def when 0
#define EMK_COLL_CC              2            // mCyl only: SetC(cur), R 35, H 30 (HUSE_WALK) or 100, At stripped, Tg/Co from flags2
#define EMK_ATTACH_NONE          0
#define EMK_ATTACH_BK            1            // boko stick, shield, bouen (d_a_bk.cpp:4104-4136, 4371-4416)
#define EMK_ATTACH_TN            2            // TN (R-6, R-7): the shield pre-calc before the body calc, then parts, armor calc, sword, pieces AND, def centre after it
#define EMK_EYE_NONE             0
#define EMK_EYE_CC               1            // eyePos and attention position every frame (d_a_cc.cpp:2108-2122)
#define EMK_EYE_FIXED            2            // D5: a switch on the kind id after McaMorf_calc, per-kind EMK_<K>_EYE_* / ATTN_UP constants; HKD_EYE skips it

// --- hit collider codes (em_hits, WWENEMY_HA_COLLIDER_OFF) -----------------
#define EMK_HIT_HEAD             1            // the row's head_sph
#define EMK_HIT_TG               2            // the row's tg_cyl
#define EMK_HIT_SPECIAL          3            // AM only (D11, R-13): the bomb swallow through mMouthSph, behind WWENEMY_HKD_SPECIAL

// --- generic actor fields (f_op_actor.h) -----------------------------------
#define EMK_AC_PARAMS            0xB0         // u32 base_process_class.mParameters (f_pc_base.h:30)
#define EMK_AC_HOME_POS          0x1D0        // f32[3] home.pos (f_op_actor.h:285-287), hashed as raw bits
#define EMK_AC_HOME_ROOM         0x1E2        // s8 home.roomNo
#define EMK_AC_STATUS            0x1C4        // u32 actor_status (f_op_actor.h:284)
#define EMK_AC_STATUS_CARRY      0x2000       // fopAcStts_CARRY_e (f_op_actor.h:32): AM2 refuses adoption while carried
#define EMK_AC_STATUS_BOSS       0x04000000   // fopAcStts_BOSS_e (f_op_actor.h:43): TN refuses adoption (D3; wwapi/addr ActorStatusBoss)
#define EMK_AC_ANGLE_X           0x204        // s16 current.angle.x: BB's tilt (EMK_CAP_TILT_CUR, batch C R-12)
#define EMK_AC_ANGLE_Y           0x206        // s16 current.angle.y: the slot AngleY of MO2/BK/CC/TN/BB (Go Kind.AngleOff)
#define EMK_AC_ANGLE_Z           0x208        // s16 current.angle.z: BB's roll, streamed in slot +0x18 (WWENEMY_SL_CC_M316Z_OFF, "kind z"; batch C R-12)
#define EMK_AC_SUB_METHOD        0xEC         // profile_method_class* sub_method (f_op_actor.h); its mpExecuteFunc at EMK_METHOD_EXECUTE (f_pc_method.h:9-14): BB's tail fingerprint (R-14)
#define EMK_METHOD_EXECUTE       0x08
#define EMK_AC_SHAPE_X           0x20C        // s16 shape_angle.x (CC annex aux[0]; KI aux[0])
#define EMK_AC_SHAPE_Y           0x20E        // s16 shape_angle.y: the slot AngleY of KI/AM/AM2 (D13, Go Kind.AngleOff)
#define EMK_AC_SHAPE_Z           0x210        // s16 shape_angle.z (KI aux[1])
#define EMK_AC_EYE_POS           0x260        // cXyz eyePos
#define EMK_AC_ATTN_POS          0x274        // cXyz attention_info.position
#define EMK_AC_ATTN_FLAGS        0x280        // u32 attention_info.flags
#define EMK_ATTN_LOCKON_BATTLE   0x4          // fopAc_Attn_LOCKON_BATTLE_e (f_op_actor.h:76)
#define EMK_AC_HEALTH            0x285        // s8 health
#define EMK_AC_BT_START          0x29C        // f32 mBtStartFrame (f_op_actor.h:359-365)
#define EMK_AC_BT_END            0x2A0        // f32 mBtEndFrame
#define EMK_AC_BT_NOW            0x2A4        // f32 mBtNowFrame
#define EMK_AC_BT_TYPE           0x2A8        // u8  mBtAttackType
#define EMK_ICE_SHRINK           0x6          // s8 enemyice mLightShrinkTimer, from the row's ice (c_damagereaction.h:16)
#define EMK_ICE_FREEZE           0xE          // s16 enemyice freeze timer, from the row's ice (c_damagereaction.h:21)
#define EMK_FN_FOPACM_DELETE     0x800244B8   // fopAcM_delete__FUi (tww config/GZLE01/symbols.txt:644; no fopAcM_deleteByID exists)
#define EMK_FN_BASE_ANM_PLAY     0x8000DF24   // play__14mDoExt_baseAnmFv (symbols.txt:158): R-10's btk/brk play
#define EMK_QUIET_FRAMES         20           // em_die_quiet scale-down frames (annex override quiet_frames, ids < WWENEMY_ANNEX_KINDS)
#define EMK_QUIET_FRAMES_LIGHT   90           // with WWENEMY_SF2_LIGHT (the host died by enemy_ice), every kind

// --- the placement key (identity.pkey; mods/enemysync identity.go PKey) ----
// FNV-1a-32 over EMK_PKEY_LEN bytes: proc BE16, setID BE16, params BE32,
// home room s8, home.pos x/y/z raw BE32; a hash of 0 is 1. The test
// vector is M_NewD2 r0's Bokoblin setID 0 (live L0).
#define EMK_PKEY_LEN             21
#define EMK_PKEY_FNV_OFFSET      0x811C9DC5
#define EMK_PKEY_FNV_PRIME       0x01000193
#define EMK_PKEY_TV_PROC         0x00BE
#define EMK_PKEY_TV_SETID        0
#define EMK_PKEY_TV_PARAMS       0xFFFF8784
#define EMK_PKEY_TV_ROOM         0
#define EMK_PKEY_TV_HOME_X       0x44A00000
#define EMK_PKEY_TV_HOME_Y       0xC3480000
#define EMK_PKEY_TV_HOME_Z       0x45BB8000
#define EMK_PKEY_TV_OUT          0xD0264F5D
// The no-pos vector (D9, R-16; a row with EMK_CAP_PKEY_NO_POS, keyed):
// the same bytes with home.pos hashed as zero. M_NewD2 r7's fire Keese
// setID 0 (A0 live, ki-newd2-7: params 0x80FF8201, home room 7).
#define EMK_PKEY_TV2_PROC        0x00D8
#define EMK_PKEY_TV2_SETID       0
#define EMK_PKEY_TV2_PARAMS      0x80FF8201
#define EMK_PKEY_TV2_ROOM        7
#define EMK_PKEY_TV2_OUT         0x268F152E

// --- row MO2: Moblin, fpcNm_MO2_e (d_a_mo2.h) ------------------------------
// Every value equals the legacy MO2_* / EM_* define in enemy_mirror.c:251-347.
#define EMK_MO2_ID               0
#define EMK_MO2_PROC             0x00BD       // wwapi/addr/remote.go:64
#define EMK_MO2_CLASS_SIZE       0x3038       // d_a_mo2.h:178
#define EMK_MO2_ARC              "Mo2"
#define EMK_MO2_BCK_FIRST        0x3E         // Mo2.h:64-119
#define EMK_MO2_BCK_LAST         0x75
#define EMK_MO2_BCK_WAIT         0x73         // wait_set (d_a_mo2.cpp:1059-1072); KWAIT 0x5C for mMode 1 (EMK_CAP_WAIT_MODE1)
#define EMK_MO2_MORF             0x2C4
#define EMK_MO2_GATE             0x2BC        // u8 mMode: 5 carried and 0xF statue refused (ERR_MODE)
#define EMK_MO2_DR               0x614
#define EMK_MO2_DR_MODE          0x618        // s16 dr.mMode
#define EMK_MO2_DR_ACTION        0x61A        // s16 dr.mAction
#define EMK_MO2_TILT             0xAA0        // s16 dr.m48C.x
#define EMK_MO2_CO_CYL           0x208C       // d_a_mo2.h:109-113
#define EMK_MO2_TG_CYL           0x21BC
#define EMK_MO2_HEAD_SPH         0x22EC
#define EMK_MO2_DEF_SPH          0x2418
#define EMK_MO2_HEAD_CENTER      0x28C8
#define EMK_MO2_DEF_CENTER       0x2928
#define EMK_MO2_CO_FORWARD       35           // EM_CO_FORWARD (d_a_mo2.cpp:3571-3577)
#define EMK_MO2_TG_HIT_UP        60           // EM_TG_CENTER_UP
#define EMK_MO2_INVULN           0x5B4        // s16 m05B4
#define EMK_MO2_INVULN_WAIT      8            // EM_INVULN_WAIT (enemy_mirror.c:346)
#define EMK_MO2_ICE              0x2A54       // shrink timer 0x2A5A, freeze 0x2A62
#define EMK_MO2_WEAPON           0xDD4        // s8 mbHasInnateWeapon
#define EMK_MO2_BARE             0x2943       // u8 m2943
#define EMK_MO2_BLUR_ON          0x594        // int m0594
#define EMK_MO2_BLUR_SHAPE       0x598        // int m0598
#define EMK_MO2_BLUR_SCALE       0x590        // f32 m0590
#define EMK_MO2_ANM_GATE         0x5B0        // s16 m05B0
#define EMK_MO2_PLAY_SKIP        0x5B2        // s16 m05B2
#define EMK_MO2_SPH_SRC          0x22EC       // the head sphere is the At image source
#define EMK_MO2_CAPS             0x1F         // DR | MODEL_EQ | FOPEN | WAIT_MODE1 | ADDR_SUCCESS
#define EMK_MO2_MTX              1            // EMK_MTX_DR
#define EMK_MO2_COLL             1            // EMK_COLL_STD
#define EMK_MO2_ATTACH           0            // EMK_ATTACH_NONE
#define EMK_MO2_EYE              0            // EMK_EYE_NONE
#define EMK_MO2_ATTN_F2          0            // D4: no streamed attention bit (em_consume never sets EM_XF_ATTN for a Moblin)
#define EMK_MO2_DEATH_SWITCH     0x2C0        // u8 mDeathSwitch (d_a_mo2.h:42): client Go writes it under quiet death

// --- row BK: Bokoblin, fpcNm_BK_e (d_a_bk.h, verified :13-143) --------------
#define EMK_BK_ID                1
#define EMK_BK_PROC              0x00BE       // wwapi/addr/remote.go:66
#define EMK_BK_CLASS_SIZE        0x1848       // d_a_bk.h:143
#define EMK_BK_ARC               "Bk"
#define EMK_BK_BCK_FIRST         0x29         // AOMUKE (Bk.h:43)
#define EMK_BK_BCK_LAST          0x53         // WALK2 (Bk.h:85)
#define EMK_BK_BCK_WAIT          0x51         // BK_WAIT (Bk.h:83), every live type
#define EMK_BK_MORF              0x2BC        // d_a_bk.h:24
#define EMK_BK_GATE              0x2B4        // u8 mType (d_a_bk.h:16): live {0, 1, 2, 3, 4} (batch C R-6: 2/3 the pot ambushers, unconditionally)
#define EMK_BK_DR                0x370        // d_a_bk.h:72
#define EMK_BK_DR_MODE           0x374        // s16 dr.mMode (c_damagereaction.h:80)
#define EMK_BK_DR_ACTION         0x376        // s16 dr.mAction (c_damagereaction.h:81)
#define EMK_BK_TILT              0x7FC        // s16 dr.m48C.x
#define EMK_BK_CO_CYL            0xB88        // d_a_bk.h:89-93
#define EMK_BK_TG_CYL            0xCB8
#define EMK_BK_HEAD_SPH          0xDE8
#define EMK_BK_DEF_SPH           0xF14        // the shield sphere
#define EMK_BK_HEAD_CENTER       0x116C       // m116C (d_a_bk.h:94)
#define EMK_BK_DEF_CENTER        0x11CC       // m11CC (d_a_bk.h:102)
#define EMK_BK_CO_FORWARD        35           // d_a_bk.cpp:4421-4428
#define EMK_BK_TG_HIT_UP         56           // Tg cyl height 112.5 / 2 (d_a_bk.cpp:5168-5172), inferred: annex-overridable
#define EMK_BK_INVULN            0x30E        // s16 m030E (d_a_bk.h:52)
#define EMK_BK_INVULN_WAIT       12           // m030E reaches 10 (d_a_bk.cpp:2031, :3102)
#define EMK_BK_ICE               0x1264       // d_a_bk.h:140: shrink 0x126A, freeze 0x1272
#define EMK_BK_WEAPON            0xB30        // int m0B30 (d_a_bk.h:74)
#define EMK_BK_BARE              0
#define EMK_BK_BLUR_ON           0x2F0        // int m02F0 (d_a_bk.h:44-46)
#define EMK_BK_BLUR_SHAPE        0x2F4        // int m02F4
#define EMK_BK_BLUR_SCALE        0x2EC        // f32 m02EC
#define EMK_BK_ANM_GATE          0
#define EMK_BK_PLAY_SKIP         0x30C        // s16 m030C (d_a_bk.h:51)
#define EMK_BK_SPH_SRC           0xDE8        // head_sph_src: no At, no callback (d_a_bk.cpp:5176-5205, verified)
#define EMK_BK_CAPS              0x07         // DR | MODEL_EQ | FOPEN
#define EMK_BK_MTX               1            // EMK_MTX_DR
#define EMK_BK_COLL              1            // EMK_COLL_STD
#define EMK_BK_ATTACH            1            // EMK_ATTACH_BK
#define EMK_BK_EYE               0            // EMK_EYE_NONE
#define EMK_BK_ATTN_F2           0x08         // D4: EMK_BK_F2_ATTN (em_consume ATTN test, enemy_mirror.c:956)
// BK class fields the gates, hooks, handback and quiet death use
#define EMK_BK_DRAW_GATE         0x2BA        // u8 m02BA: hidden while != 0 (d_a_bk.h:22); the REL writes 0 when SF_HIDDEN is clear
#define EMK_BK_DEATH_SWITCH      0x2B8        // u8 m02B8 (d_a_bk.h:20): client Go writes it under quiet death without co-op switch sync
#define EMK_BK_SHIELD_MODEL      0x2D0        // J3DModel* (d_a_bk.h:32)
#define EMK_BK_SHIELD_GATE       0x2D4        // u8 m02D4
#define EMK_BK_BOUEN_MODEL       0x2D8        // J3DModel* (d_a_bk.h:36)
#define EMK_BK_BOUEN_GATE        0x2DC        // u8 m02DC
#define EMK_BK_HIDE2             0x2DE        // s8 m02DE (d_a_bk.h:39)
#define EMK_BK_DR_M488           0x7F8        // s16 dr.m488: ragdoll in progress (gate == 0; handback writes 0)
#define EMK_BK_DR_M48A           0x7FA        // s16 dr.m48A (handback writes 0, d_a_bk.cpp:3356-3366)
#define EMK_BK_TILT_Z            0x800        // s16 dr.m48C.z (annex aux[0])
#define EMK_BK_DR_SPAWN_Y        0xA54        // f32 dr.mSpawnY (handback = current.pos.y)
#define EMK_BK_SHIELD_R          0x1038       // f32 shield sphere R (0xF14 + 0x124): 60 up, -200 off (d_a_bk.cpp:2458-2460, 3531-3533)
#define EMK_BK_SHIELD_CY         0x11DC       // f32 m11D8.y: -10000 off
#define EMK_BK_JAW               0x11F4       // s16 m11F4 (annex aux[1])
#define EMK_BK_BOKO_PID          0x1200       // u32 m1200 (d_a_bk.h:112)
#define EMK_BK_BOKO_ATTACH       0x1214       // s8 m1214: gate == 0 (d_a_bk.cpp:4219-4227)
#define EMK_BK_DELETE_REQ        0x121C       // s8 m121C (d_a_bk.h:123)
#define EMK_BK_DEMO_GATE         0x1234       // s8 m1234: gate == 0 (d_a_bk.h:134)
#define EMK_BK_JOINT_BUKI        0x2C         // Bk.h:193-243
#define EMK_BK_JOINT_TATE        0x25
#define EMK_BK_JOINT_HEAD        0x12
#define EMK_BK_HAND_RY           0x3E80       // stick: nodeMtx[BUKI] * Ry(0x3E80) * T(0, 0, 65)
#define EMK_BK_HAND_TZ           65
#define EMK_BK_BOUEN_RY          0x4000       // bouen: nodeMtx[BUKI] * Ry(0x4000) * T(0, 0, 50)
#define EMK_BK_BOUEN_TZ          50
#define EMK_BK_ACTION_DEFENCE    10           // dr.mAction 10 -> defence() (d_a_bk.cpp:3403-3405)
#define EMK_BK_ACTION_DIE_A      20           // SF_DYING: dr.mAction 20, 21, 22 (d_a_bk.cpp:3424-3456)
#define EMK_BK_ACTION_DIE_C      22
#define EMK_BK_ACTION_REFUSE_A   19           // em_validate gate: dr.mAction not 19
#define EMK_BK_ACTION_REFUSE_B   31           // nor 31 (carry drop)
// The pot ambusher (batch C "ambush_cleanup", R-6..R-9; d_a_bk.cpp
// :3009-3036, 4219-4249, 5052-5068). The REL's em_bk_gate takes mType 2/3
// as it takes 0/1/4 (Go stops writing mType 0 into its copy in the same
// build); while the host slot is SF_HIDDEN the copy's stick is parked at
// (-EMK_BK_STICK_PARK, -EMK_BK_STICK_PARK, 0) as the host parks it; a copy
// handed back still in its pot (m02BA == EMK_BK_DRAW_GATE_POT, mType 2/3)
// gets dr.mAction EMK_BK_ACTION_TUBO, dr.mMode 0, m0300[0] 0, m0310 0 and
// scale 1.0 so tubo_wait can leap. Both writes skip under EMK_BK_HKD_AMBUSH.
#define EMK_BK_TYPE_AMBUSH_JUMP  2            // mType 2: JUMP1 out of the pot (Go BkTypeAmbushJump)
#define EMK_BK_TYPE_AMBUSH_LEAP  3            // mType 3: JATTACK1 at the Link (Go BkTypeAmbushLeap)
#define EMK_BK_TYPE_TORCH        4            // mType 4 (params 0xB at create)
#define EMK_BK_DRAW_GATE_POT     0xFF         // m02BA while hidden in a pot (d_a_bk.cpp:5052-5055); a switch-gated BK has m02B7 + 1 instead
#define EMK_BK_ACTION_TUBO       15           // dr.mAction tubo_wait (d_a_bk.cpp:3009-3036, 3433-3434)
#define EMK_BK_M0300             0x300        // s16 m0300[0] (d_a_bk.h:49): the client-first reveal writes 50; the hidden handback 0
#define EMK_BK_M0310             0x310        // s16 m0310 (d_a_bk.h:53): the reveal writes 20; the hidden handback 0
#define EMK_BK_STICK_PARK        10000        // the parked stick's x and y are minus this (d_a_bk.cpp:4219-4227)
#define EMK_BK_HKD_AMBUSH        0x40         // == WWENEMY_HKD_SPECIAL (bk:hooks=ambush): skip the hidden stick park (R-8) and the hidden handback 15 (R-9); never refuses 2/3 (D8)
#define EMK_BK_F2_HAS_WEAPON     0x01         // flags2 bits: host m0B30 != 0
#define EMK_BK_F2_PLAY_SKIP      0x02         // host m030C != 0
#define EMK_BK_F2_BLUR_ON        0x04         // host m02F0 != 0
#define EMK_BK_F2_ATTN           0x08         // host attention_info.flags != 0
// the boko (the stick the BK carries), fpcNm_BOKO_e (d_a_boko.h)
#define EMK_BOKO_PROC            0x01D5
#define EMK_BOKO_MODEL           0x298        // J3DModel* mpModel (d_a_boko.h:123)
#define EMK_BOKO_MODE            0x2B8        // u8 mNowMode (d_a_boko.h:128): 2 ENEMY_CARRY
#define EMK_BOKO_MODE_CARRY      2
#define EMK_BOKO_CARRY_BYTE      0x1C6        // the status byte holding fopAcStts_CARRY_e (0x2000 in +0x1C4)
#define EMK_BOKO_CARRY_BIT       0x20

// --- row CC: ChuChu, fpcNm_CC_e (d_a_cc.h, verified :14-95) ---------------
#define EMK_CC_ID                2
#define EMK_CC_PROC              0x00CF       // wwapi/addr/remote.go:89; f_pc_name.h:219
#define EMK_CC_CLASS_SIZE        0xE04        // d_a_cc.h:95
#define EMK_CC_ARC               "CC"
#define EMK_CC_BCK_FIRST         0x14         // ATACK01 (Cc.h:9-131)
#define EMK_CC_BCK_LAST          0x22         // TSTART02
#define EMK_CC_BCK_WAIT          0            // none: the handback setAnms the copy's m320
#define EMK_CC_MORF              0x2B4        // m2B4 (d_a_cc.h:17)
#define EMK_CC_GATE              0x2F0        // u8 mBehaviorType: live {0, 1, 3}
#define EMK_CC_DR                0
#define EMK_CC_DR_MODE           0
#define EMK_CC_DR_ACTION         0
#define EMK_CC_TILT              0x316        // s16 m316.x, written raw
#define EMK_CC_CO_CYL            0x6BC        // the one dCcD_Cyl mCyl (d_a_cc.h:90), Tg and Co
#define EMK_CC_TG_CYL            0x6BC
#define EMK_CC_HEAD_SPH          0
#define EMK_CC_DEF_SPH           0
#define EMK_CC_HEAD_CENTER       0
#define EMK_CC_DEF_CENTER        0
#define EMK_CC_CO_FORWARD        0
#define EMK_CC_TG_HIT_UP         50           // centre of H100; open against H30 (still_open), annex-overridable
#define EMK_CC_INVULN            0            // none: WAIT_INVULN while the mCyl Tg Set bit is clear
#define EMK_CC_INVULN_WAIT       8            // inferred (EM_INVULN_WAIT), annex-overridable
#define EMK_CC_ICE               0x81C        // d_a_cc.h:92: shrink 0x822, freeze 0x82A
#define EMK_CC_WEAPON            0
#define EMK_CC_BARE              0
#define EMK_CC_BLUR_ON           0
#define EMK_CC_BLUR_SHAPE        0
#define EMK_CC_BLUR_SCALE        0x328        // f32 m328, the y offset (the slot's blur_scale carries it)
#define EMK_CC_ANM_GATE          0
#define EMK_CC_PLAY_SKIP         0
#define EMK_CC_SPH_SRC           0            // none: Link's mFanWindSph (+0x49C4, d_a_player_main.h:2347)
#define EMK_CC_CAPS              0x04         // FOPEN
#define EMK_CC_MTX               2            // EMK_MTX_CC
#define EMK_CC_COLL              2            // EMK_COLL_CC
#define EMK_CC_ATTACH            0            // EMK_ATTACH_NONE
#define EMK_CC_EYE               1            // EMK_EYE_CC
#define EMK_CC_ATTN_F2           0x20         // D4: EMK_CC_F2_ATTN
// CC class fields the gates, hooks and handback use (d_a_cc.h)
#define EMK_CC_COLOR             0x2F1        // u8 mColorType: live {0, 1, 2, 4}
#define EMK_CC_ACTION            0x2F4        // u8 mCurrAction: gate not 5/6/8; 4 dying
#define EMK_CC_SUBSTATE          0x2F5        // u8 m2F5: 0x35 dying; [6, 11) low eye
#define EMK_CC_NO_SHADOW         0x2F9        // u8 m2F9 (written from CC_NO_SHADOW)
#define EMK_CC_STONE             0x2FE        // u8 m2FE: gate == 0
#define EMK_CC_SPLIT             0x301        // u8 m301: gate == 0
#define EMK_CC_INSTA_KILL        0x304        // u8 m304: gate == 0
#define EMK_CC_WOBBLE            0x310        // csXyz m310 (annex aux[1..3])
#define EMK_CC_TILT_Z            0x31A        // s16 m316.z (slot +0x18)
#define EMK_CC_SQUASH_PHASE      0x31C        // s16 m31C, local
#define EMK_CC_BCK_INDEX         0x320        // s32 m320 = the streamed bck on every REL setAnm
#define EMK_CC_SCALE             0x334        // f32 m334, read from the copy (1.0, D12)
#define EMK_CC_CYL_AT_SPRM       0x00         // from the cylinder: At SPrm word, bit0 Set
#define EMK_CC_CYL_TG_SPRM       0x18         // Tg SPrm
#define EMK_CC_CYL_CO_SPRM       0x2C         // Co SPrm
#define EMK_CC_CYL_R             0x124        // f32 radius, from the cylinder (cM3dGCyl at +0x118, c_m3d_g_cyl.h:19-21)
#define EMK_CC_CYL_H             0x128        // f32 height
#define EMK_CC_CYL_R_VAL         35
#define EMK_CC_CYL_H_LOW         30           // while the streamed bck is EMK_CC_BCK_HUSE_WALK
#define EMK_CC_CYL_H_HIGH        100
#define EMK_CC_BCK_HUSE_WALK     0x1C
#define EMK_CC_EYE_LOW           10           // eyePos y offset: low eye
#define EMK_CC_EYE_HIGH          70
#define EMK_CC_ATTN_UP           30           // attention = eye + 30
#define EMK_CC_BT_NOW            1000         // f32 mBtNowFrame = 1000.0f (d_a_cc.cpp:488)
#define EMK_CC_F2_TG             0x01         // flags2 bits: host mCyl Tg Set
#define EMK_CC_F2_CO             0x02         // host mCyl Co Set
#define EMK_CC_F2_SQUASH         0x04         // the squash runs (d_a_cc.cpp:88)
#define EMK_CC_F2_LOW_EYE        0x08         // m2F5 in [6, 11) or action 1
#define EMK_CC_F2_NO_SHADOW      0x10         // m2F9 != 0
#define EMK_CC_F2_ATTN           0x20         // host attention_info.flags != 0

// --- row KI: Keese and Fire Keese, fpcNm_KI_e (d_a_ki.h:14-87) -------------
// Allow batch A1 (enemy-mirror-allow.json type_table KI). Wire: AngleY is
// shape_angle.y (D13), aux {shape.x, shape.z, m326, m328} (R-6).
#define EMK_KI_ID                3
#define EMK_KI_PROC              0x00D8       // f_pc_name.h:228; wwapi/addr/remote.go ProcKI
#define EMK_KI_CLASS_SIZE        0xF04        // d_a_ki.h:87: mEnemyIce 0x924 + enemyice 0x3B8 + enemyfire 0x228 (c_damagereaction.h:43,69)
#define EMK_KI_ARC               "Ki"
#define EMK_KI_BCK_FIRST         0x0F         // ATTACK1 (res/Object/Ki.h:18-23)
#define EMK_KI_BCK_LAST          0x14         // WAIT1
#define EMK_KI_BCK_WAIT          0x14         // WAIT1 (d_a_ki.cpp:284, 1211)
#define EMK_KI_MORF              0x2B4        // mpMorf (d_a_ki.h:27)
#define EMK_KI_GATE              0x2D1        // u8 mAction (d_a_ki.h:34; Action_e :15-23)
#define EMK_KI_DR                0
#define EMK_KI_DR_MODE           0
#define EMK_KI_DR_ACTION         0
#define EMK_KI_TILT              0
#define EMK_KI_CO_CYL            0x6AC        // dCcD_Sph m6AC, the Co (d_a_ki.h:78; EMK_CAP_SPH_COLL)
#define EMK_KI_TG_CYL            0x7D8        // dCcD_Sph mDamageSphere, the Tg (d_a_ki.h:79)
#define EMK_KI_HEAD_SPH          0            // none: KI reports only EMK_HIT_TG
#define EMK_KI_DEF_SPH           0
#define EMK_KI_HEAD_CENTER       0
#define EMK_KI_DEF_CENTER        0
#define EMK_KI_CO_FORWARD        0
#define EMK_KI_TG_HIT_UP         0            // the Tg sphere is centred on current.pos (d_a_ki.cpp:1068-1070)
#define EMK_KI_INVULN            0x314        // s16 m314 (d_a_ki.h:55; set d_a_ki.cpp:739, 1091, 1110, counted down :1006-1008)
#define EMK_KI_INVULN_WAIT       16           // the knockback m31C decay (d_a_ki.cpp:793-794)
#define EMK_KI_ICE               0x924        // d_a_ki.h:85: shrink 0x92A, freeze 0x932
#define EMK_KI_WEAPON            0
#define EMK_KI_BARE              0
#define EMK_KI_BLUR_ON           0
#define EMK_KI_BLUR_SHAPE        0
#define EMK_KI_BLUR_SCALE        0
#define EMK_KI_ANM_GATE          0
#define EMK_KI_PLAY_SKIP         0            // D15: m338 is s8 (d_a_ki.h:70), the REL writes play_skip as s16
#define EMK_KI_SPH_SRC           0x7D8        // mDamageSphere (no hit callbacks in d_a_ki.cpp)
#define EMK_KI_CAPS              0x64         // FOPEN | PKEY_NO_POS | SPH_COLL; no MODEL_EQ: execute NULLs actor.model (d_a_ki.cpp:949), Draw restores it (:209)
#define EMK_KI_MTX               3            // EMK_MTX_PLAIN, x/z from aux[0..1] (d_a_ki.cpp:1053-1056)
#define EMK_KI_COLL              1            // EMK_COLL_STD over the two spheres (co the Co, tg the Tg)
#define EMK_KI_ATTACH            0            // EMK_ATTACH_NONE
#define EMK_KI_EYE               2            // EMK_EYE_FIXED
#define EMK_KI_ATTN_F2           0x01         // EMK_KI_F2_ATTN
#define EMK_KI_QUIET_FRAMES      20           // compiled (ids >= 3 have no live quiet override, D3)
// KI eye (D5, R-7): eyePos = cur + EYE_UP, attention = eye + ATTN_UP (d_a_ki.cpp:1064-1066)
#define EMK_KI_EYE_UP            0
#define EMK_KI_ATTN_UP           50
// KI collider radii (R-5, D8): written after STD every frame, INVULN frames
// included, behind HKD_EXTRA (d_a_ki.cpp:51, 578-580, 993-995; R at +0x124
// on a sphere too)
#define EMK_KI_AT_SPH            0x580        // dCcD_Sph m580, the At (d_a_ki.h:77): the host Sets it every frame (R 0, 30 in the lunge, d_a_ki.cpp:1072-1074, 578-584); the copy never does
#define EMK_KI_TG_R              40
#define EMK_KI_TG_R_LUNGE        20
#define EMK_KI_CO_R              80           // l_kiHIO.m58 (d_a_ki.cpp:51)
#define EMK_KI_CO_R_LUNGE        0
// KI class fields the gates, hooks and handback use (d_a_ki.h)
#define EMK_KI_PARAMS            0x2CC        // u8 mParameters = params & 0xFF (d_a_ki.h:29; d_a_ki.cpp:1372-1377)
#define EMK_KI_TYPE_FLY          1            // the only params low byte an unkeyed Keese may have (D10; the BGN swarms spawn 3, d_a_bgn.cpp:3343)
#define EMK_KI_BEHAVIOR          0x2D3        // s8 mBehaviorType (d_a_ki.h:36)
#define EMK_KI_DRAW_GATE         0x2D4        // u8 m2D4 (d_a_ki.h:37): the REL writes 0 when SF_HIDDEN is clear (R-9)
#define EMK_KI_SHRINK_DELETE     0x2DC        // u8 m2DC (d_a_ki.h:42): adoption refused while != 0 (D16)
#define EMK_KI_HEAD_BANK         0x322        // s16 m322, m324 (d_a_ki.h:60-61): handback writes 0
#define EMK_KI_AUX2              0x326        // s16 m326 (d_a_ki.h:62): aux[2]
#define EMK_KI_AUX3              0x328        // s16 m328 (d_a_ki.h:63): aux[3]
#define EMK_KI_M338              0x338        // s8 m338 (d_a_ki.h:70): handback writes 0
#define EMK_KI_SHADOW            0x540        // u8 m540 (d_a_ki.h:75): the REL writes 0 every frame (R-9, d_a_ki.cpp:246-249)
#define EMK_KI_DMG_TYPE          0x905        // u8 mDamageType (d_a_ki.h:81): nonzero is the Fire Keese, whose btk plays (d_a_ki.cpp:1037-1039)
#define EMK_KI_BTK               0x920        // mDoExt_btkAnm* m920 (d_a_ki.h:84)
#define EMK_KI_ACT_WAIT          0            // Action_e (d_a_ki.h:15-23)
#define EMK_KI_ACT_FIRE_SET      2            // refused (R-8); the handback leaves mBehaviorType alone in it
#define EMK_KI_ACT_ATTACK        3
#define EMK_KI_ACT_ATTACK_INDEX  10
#define EMK_KI_ACT_FAIL          30           // refused; DYING on the host
#define EMK_KI_BEHAVIOR_LUNGE    3            // with ACT_ATTACK or ATTACK_INDEX: the lunge (d_a_ki.cpp:502, 577-580)
#define EMK_KI_F2_ATTN           0x01         // flags2 bits: host attention_info.flags != 0
#define EMK_KI_F2_LUNGE          0x04         // the lunge: Tg R 20, Co R 0

// --- row AM: Armos Knight, fpcNm_AM_e (d_a_am.h:11-57) ----------------------
// Wire: AngleY is shape_angle.y (D13), aux {mEyeRot.x, mEyeRot.y} (R-6).
#define EMK_AM_ID                4
#define EMK_AM_PROC              0x00CB       // f_pc_name.h:215; wwapi/addr/remote.go ProcAM
#define EMK_AM_CLASS_SIZE        0x109C       // mEnemyFire 0xE74 (d_a_am.h:56) + enemyfire 0x228 (c_damagereaction.h:69)
#define EMK_AM_ARC               "AM"
#define EMK_AM_BCK_FIRST         0x14         // BOM_NOMI (res/Object/Am.h:25-36)
#define EMK_AM_BCK_LAST          0x1F         // SLEEP_LOOP
#define EMK_AM_BCK_WAIT          0x1F         // SLEEP_LOOP (d_a_am.cpp:498, 1128)
#define EMK_AM_MORF              0x2B8        // mpMorf (d_a_am.h:15)
#define EMK_AM_GATE              0x2C4        // u8 mAction (d_a_am.h:21; Action d_a_am.cpp:28-33)
#define EMK_AM_DR                0
#define EMK_AM_DR_MODE           0
#define EMK_AM_DR_ACTION         0
#define EMK_AM_TILT              0
#define EMK_AM_CO_CYL            0x85C        // dCcD_Cyl mBodyCyl (d_a_am.h:53), Co and Tg (co == tg: D7)
#define EMK_AM_TG_CYL            0x85C
#define EMK_AM_HEAD_SPH          0x604        // dCcD_Sph mEyeSph (d_a_am.h:51)
#define EMK_AM_DEF_SPH           0
#define EMK_AM_HEAD_CENTER       0x2F4        // cXyz mEyeballPos (d_a_am.h:35)
#define EMK_AM_DEF_CENTER        0
#define EMK_AM_CO_FORWARD        0
#define EMK_AM_TG_HIT_UP         150          // the body cylinder's half height (H 300, d_a_am.cpp:1071)
#define EMK_AM_INVULN            0            // none: em_hit_vulnerable falls back to the body Tg Set bit (enemy_mirror.c:1996-2000)
#define EMK_AM_INVULN_WAIT       8
#define EMK_AM_ICE               0xABC        // d_a_am.h:55: shrink 0xAC2, freeze 0xACA
#define EMK_AM_WEAPON            0
#define EMK_AM_BARE              0
#define EMK_AM_BLUR_ON           0
#define EMK_AM_BLUR_SHAPE        0
#define EMK_AM_BLUR_SCALE        0
#define EMK_AM_ANM_GATE          0
#define EMK_AM_PLAY_SKIP         0x2CA        // s16 mCountDownTimers[1] (d_a_am.h:25; d_a_am.cpp:862, 1034-1036), streamed as WWENEMY_SF2_PLAY_SKIP
#define EMK_AM_SPH_SRC           0x604        // mEyeSph (no hit callbacks in d_a_am.cpp)
#define EMK_AM_CAPS              0x04         // FOPEN; no MODEL_EQ (no actor->model write in d_a_am.cpp, fopAcM_SetMtx :1220)
#define EMK_AM_MTX               3            // EMK_MTX_PLAIN, x/z the copy's own (AM writes only shape.y, d_a_am.cpp:892, 1031)
#define EMK_AM_COLL              1            // EMK_COLL_STD
#define EMK_AM_ATTACH            0            // EMK_ATTACH_NONE
#define EMK_AM_EYE               2            // EMK_EYE_FIXED
#define EMK_AM_ATTN_F2           0x01         // EMK_AM_F2_ATTN
#define EMK_AM_QUIET_FRAMES      40           // the DEAD bck 0x1A length (A0 live: J3DAnmTransform mFrameMax 0x28)
// AM eye (D5, R-7): eyePos = cur + EYE_UP, attention = cur + ATTN_UP (d_a_am.cpp:1055-1058)
#define EMK_AM_EYE_UP            250
#define EMK_AM_ATTN_UP           330
// AM collider sizes: daAM_Execute sets them every frame (d_a_am.cpp:1062-1078);
// the create-time src tables carry R 15 (d_a_am.cpp:1237-1360). The REL
// writes the eye and body ones after STD every frame (R-5), the mouth's
// under R-13.
#define EMK_AM_EYE_R             60
#define EMK_AM_MOUTH_R           100
#define EMK_AM_BODY_R            80
#define EMK_AM_BODY_H            300
// AM class fields the gates, hooks and handback use (d_a_am.h)
#define EMK_AM_EYE_SPH           0x604        // == HEAD_SPH
#define EMK_AM_MOUTH_SPH         0x730        // dCcD_Sph mMouthSph (d_a_am.h:52): Co only; the copy Sets it only under R-13
#define EMK_AM_BODY_CYL          0x85C        // == TG_CYL
#define EMK_AM_NEEDLE_CYL        0x98C        // dCcD_Cyl mNeedleCyl (d_a_am.h:54), the At spike: the host Sets it every frame (d_a_am.cpp:1074-1078); the copy never does and clears its At Set bit at adopt
#define EMK_AM_MODE              0x2C5        // u8 mMode (d_a_am.h:22)
#define EMK_AM_HUGE_KB           0x2C6        // u8 mHugeKnockback (d_a_am.h:23): handback 0
#define EMK_AM_BODY_HIT          0x2C7        // bool mbIsBodyBeingHit (d_a_am.h:24): handback 0
#define EMK_AM_TIMERS            0x2C8        // s16 mCountDownTimers[4] (d_a_am.h:25): handback zeroes 0x2C8..0x2CF
#define EMK_AM_TIMERS_LEN        8
#define EMK_AM_TARGET_ANGLE      0x2DA        // s16 mTargetAngleY (d_a_am.h:27): handback = current.angle.y
#define EMK_AM_BCK_INDEX         0x2E0        // int mCurrBckIdx (d_a_am.h:30): = the bck on every REL setAnm (R-9)
#define EMK_AM_SWALLOW_PID       0x2E4        // fpc_ProcID mSwallowedActorPID (d_a_am.h:31): R-13 leaves it alone
#define EMK_AM_SMOKE_MOUTH       0x384        // dPa_smokeEcallBack mSmokeCbs[2] (d_a_am.h:46, 0x344 + 2 * 0x20): the mouth smoke DOUSA mode 7 starts on mWaistPos (d_a_am.cpp:632-642); R-13 and adopt remove it
#define EMK_AM_MOUTH_POS         0x300        // cXyz mMouthPos (d_a_am.h:36): the mouth sphere's centre
#define EMK_AM_EYE_ROT           0x330        // s16 mEyeRot.x (d_a_am.h:40): aux[0]
#define EMK_AM_EYE_ROT_Y         0x332        // s16 mEyeRot.y: aux[1]
#define EMK_AM_ACT_DOUSA         0            // Action (d_a_am.cpp:28-33): the handback writes mAction = mMode = 0
#define EMK_AM_ACT_ITAI          3
#define EMK_AM_MODE_SWALLOW      44           // ITAI modes: 44 swallow, 45 chew, 46 thrash (INVULN), 47 DEAD (d_a_am.cpp:849-935)
#define EMK_AM_MODE_THRASH       46
#define EMK_AM_MODE_DEAD         47
// AM bomb swallow (D11, R-13; bomb_nomi_check d_a_am.cpp:372-426)
#define EMK_AM_BCK_DAMAGE        0x17         // the mouth-open bcks the check takes (d_a_am.cpp:377-381): DAMAGE,
#define EMK_AM_BCK_DAMAGE_LOOP   0x19         // DAMAGE_LOOP,
#define EMK_AM_BCK_OPEN          0x1C         // OPEN,
#define EMK_AM_BCK_OPEN_LOOP     0x1D         // OPEN_LOOP
#define EMK_AM_SWALLOW_FACING    0x2000       // |shape.y - the angle to Link| <= this (d_a_am.cpp:383-387)
#define EMK_AM_SWALLOW_DY        20           // the bomb's y > mMouthPos.y - this (d_a_am.cpp:397, 412)
#define EMK_PROC_BOMB            0x0128       // fpcNm_BOMB_e (f_pc_name.h:308)
#define EMK_PROC_BOMB2           0x0129       // fpcNm_Bomb2_e (f_pc_name.h:309)
#define EMK_BOMB_CHECK_FLAG      0x6F1        // daBomb_c u8 field_0x6F1 = getBombCheck_Flag (d_a_bomb.h:215, d_a_bomb_static.cpp:22)
#define EMK_BOMB_REST_TIME       0x6FC        // daBomb_c s16 mRestTime = getBombRestTime (d_a_bomb.h:223)
#define EMK_BOMB2_TIMER          0x738        // daBomb2::Act_c int mBombTimer = get_time (d_a_bomb2.h:211)
#define EMK_BOMB2_EAT            0x745        // daBomb2::Act_c bool field_0x745 = chk_eat (d_a_bomb2.h:218)
#define EMK_AM_F2_ATTN           0x01         // flags2 bits: host attention_info.flags != 0
#define EMK_AM_F2_PLAY_SKIP      0x02         // host mCountDownTimers[1] != 0 (== WWENEMY_SF2_PLAY_SKIP)

// --- row AM2: Armos, fpcNm_AM2_e (d_a_am2.h:11-58) --------------------------
// Wire: AngleY is shape_angle.y (D13; ITAI keeps the shape facing while
// current turns, d_a_am2.cpp:867); no aux.
#define EMK_AM2_ID               5
#define EMK_AM2_PROC             0x00CC       // f_pc_name.h:216; wwapi/addr/remote.go ProcAM2
#define EMK_AM2_CLASS_SIZE       0xE44        // mEnemyIce 0xA8C (d_a_am2.h:57) + enemyice 0x3B8 (c_damagereaction.h:43)
#define EMK_AM2_ARC              "AM2"
#define EMK_AM2_BCK_FIRST        0x06         // BURUBURU (res/Object/Am2.h:11-20)
#define EMK_AM2_BCK_LAST         0x0F         // WAIT
#define EMK_AM2_BCK_WAIT         0x0F         // WAIT (d_a_am2.cpp:1227)
#define EMK_AM2_MORF             0x2B8        // mpMorf (d_a_am2.h:15)
#define EMK_AM2_GATE             0x2CC        // u8 mAction (d_a_am2.h:23; Action d_a_am2.cpp:25-31)
#define EMK_AM2_DR               0
#define EMK_AM2_DR_MODE          0
#define EMK_AM2_DR_ACTION        0
#define EMK_AM2_TILT             0
#define EMK_AM2_CO_CYL           0x5A0        // dCcD_Cyl mBodyCyl (d_a_am2.h:51), Co and Tg (co == tg: D7)
#define EMK_AM2_TG_CYL           0x5A0
#define EMK_AM2_HEAD_SPH         0x92C        // dCcD_Sph mWeakSph (d_a_am2.h:54)
#define EMK_AM2_DEF_SPH          0
#define EMK_AM2_HEAD_CENTER      0x2F8        // cXyz mWeakPos (d_a_am2.h:33)
#define EMK_AM2_DEF_CENTER       0
#define EMK_AM2_CO_FORWARD       0
#define EMK_AM2_TG_HIT_UP        75           // the body cylinder's half height (H 150, d_a_am2.cpp:1171-1174)
#define EMK_AM2_INVULN           0            // none: em_hit_vulnerable's AM2 case (R-12)
#define EMK_AM2_INVULN_WAIT      40           // ITAI knockback ignores weak hits (d_a_am2.cpp:852-988; inferred)
#define EMK_AM2_ICE              0xA8C        // d_a_am2.h:57: shrink 0xA92, freeze 0xA9A
#define EMK_AM2_WEAPON           0
#define EMK_AM2_BARE             0
#define EMK_AM2_BLUR_ON          0
#define EMK_AM2_BLUR_SHAPE       0
#define EMK_AM2_BLUR_SCALE       0            // no MODORU scale stream (D16)
#define EMK_AM2_ANM_GATE         0
#define EMK_AM2_PLAY_SKIP        0
#define EMK_AM2_SPH_SRC          0x92C        // mWeakSph
#define EMK_AM2_CAPS             0x06         // FOPEN | MODEL_EQ (actor->model = the morf model, d_a_am2.cpp:1331)
#define EMK_AM2_MTX              3            // EMK_MTX_PLAIN, x/z the copy's own (d_a_am2 writes only shape.y)
#define EMK_AM2_COLL             1            // EMK_COLL_STD
#define EMK_AM2_ATTACH           0            // EMK_ATTACH_NONE
#define EMK_AM2_EYE              2            // EMK_EYE_FIXED
#define EMK_AM2_ATTN_F2          0x01         // EMK_AM2_F2_ATTN
#define EMK_AM2_QUIET_FRAMES     20           // compiled; the quiet death starts from the tombstone of the host's mode-25 delete
// AM2 eye (D5, R-7): eyePos = *(EYE_NODE) less EYE_DOWN in y, attention = cur + ATTN_UP (d_a_am2.cpp:1166-1169)
#define EMK_AM2_EYE_NODE         0x2E0        // cXyz mEyeballPos (d_a_am2.h:31), written by the nodeCallBack during the copy's calc
#define EMK_AM2_EYE_DOWN         15
#define EMK_AM2_ATTN_UP          120
// AM2 collider sizes, set every frame (d_a_am2.cpp:1171-1187): the REL writes them after STD (R-5)
#define EMK_AM2_BODY_R           35
#define EMK_AM2_BODY_H           150
#define EMK_AM2_WEAK_R           30
// AM2 class fields the gates, hooks and handback use (d_a_am2.h)
#define EMK_AM2_BTK              0x2C0        // mDoExt_btkAnm* (d_a_am2.h:17), played by R-10
#define EMK_AM2_BRK              0x2C4        // mDoExt_brkAnm* (d_a_am2.h:18), played by R-10
#define EMK_AM2_STARTS_INACTIVE  0x2CA        // u8 mStartsInactive (d_a_am2.h:21)
#define EMK_AM2_SWITCH           0x2CB        // u8 mSwitch (d_a_am2.h:22)
#define EMK_AM2_MODE             0x2CD        // u8 mMode (d_a_am2.h:24)
#define EMK_AM2_M2CE             0x2CE        // u8 m2CE (d_a_am2.h:25): handback 0
#define EMK_AM2_WEAK_HIT         0x2CF        // bool mbIsWeakBeingHit (d_a_am2.h:26): handback 0
#define EMK_AM2_ABYSS            0x2D2        // u8 mInAbyssTimer (d_a_am2.h:29): adoption refused while != 0
#define EMK_AM2_STUN_TIMER       0x338        // s16 mCountDownTimers[2] (d_a_am2.h:38)
#define EMK_AM2_BCK_INDEX        0x34C        // int mCurrBckIdx (d_a_am2.h:43): = the bck on every REL setAnm (R-9)
#define EMK_AM2_NEEDLE_CYL       0x6D0        // dCcD_Cyl mNeedleCyl (d_a_am2.h:52): the host Sets it every frame (d_a_am2.cpp:1176-1179); the copy never does
#define EMK_AM2_EYE_SPH          0x800        // dCcD_Sph mEyeSph (d_a_am2.h:53): likewise (d_a_am2.cpp:1181-1183)
#define EMK_AM2_ACT_DOUSA        0            // Action (d_a_am2.cpp:25-31)
#define EMK_AM2_ACT_MAHI         1
#define EMK_AM2_ACT_ITAI         2
#define EMK_AM2_ACT_MODORU       4            // refused (D16: MODORU scales)
#define EMK_AM2_MODE_AWAKE       3            // DOUSA mMode >= 3: the weak spot is open (d_a_am2.cpp:624)
#define EMK_AM2_MODE_DANCE       22           // ITAI mMode >= 22: the death dance, refused at adopt and DYING on the host
#define EMK_AM2_HANDBACK_MODE    6            // awake handback: mAction DOUSA, mMode 6 (d_a_am2.cpp:589-607)
#define EMK_AM2_DORMANT_MODE     12           // dormant handback: mAction MAHI, mMode 12, stun 600 (d_a_am2.cpp:1494-1499)
#define EMK_AM2_DORMANT_STUN     600
#define EMK_AM2_F2_ATTN          0x01         // flags2 bits: host attention_info.flags != 0
#define EMK_AM2_F2_WEAK_TG       0x02         // host mWeakSph Tg Set: written to the copy's before STD (D6)
#define EMK_AM2_F2_BODY_CO       0x04         // host mBodyCyl Co Set (cleared while carried, d_a_am2.cpp:694, 709)
#define EMK_AM2_F2_CARRIED       0x08         // host actor_status CARRY (informational)
// AM2 direct apply (TN batch D10, D11, R-14..R-16, am2_fix, hit_path): the
// client REL judges back/front from its own Link and Go carries the
// verdict (WWENEMY_HP_VERDICT/BACK -> HIT byte 54 -> WWENEMY_HAF_VERDICT/
// BACK); the host REL runs week_atari_check's tail itself (d_a_am2.cpp
// :290-323) through cc_at_check, never week_atari_check's player-0 gate
// (:205-211). Both sides only while AM2's WWENEMY_HKD_SPECIAL is clear.
#define EMK_AM2_BACK_MIN         0x4000       // cLib_distanceAngleS(shape.y, searchPlayerAngleY) >= this is a back hit (d_a_am2.cpp:209-211, D11)
#define EMK_AM2_MODE_THROWN      15           // MAHI mMode 15 (thrown, d_a_am2.cpp:841): vanilla skips week_atari_check (:846); refused (R-16)
#define EMK_AM2_ITAI_MODE_HIT    20           // the tail's mAction ITAI, mMode 20 (d_a_am2.cpp:312-313)
#define EMK_AM2_M2CE_CRIT        1            // a crit cut (d_a_am2.cpp:241-260): sword-class At with WWENEMY_HAF_CRIT
#define EMK_AM2_M2CE_ARROW       5            // normal, fire or ice arrow (:287-290)
#define EMK_AM2_M2CE_BOMB        6            // (:282-284)
#define EMK_AM2_M2CE_HAMMER      7            // skull hammer (:274-277); 7 and 8 also write health 0 (:314-316)
#define EMK_AM2_M2CE_HAMMER_SIDE 8            // the hammer with WWENEMY_HAF_CRIT (CUT_TYPE_HAMMER_SIDESWING, :278-280); ITAI mode 30 reads it (:999)
// cCcD_ObjAt mType bits the tail switches on (d_cc_d.h:14-39), as masks
#define EMK_AT_SWORD_CLASS       0x14000C02   // SWORD 0x2 | MACHETE 0x400 | UNK800 0x800 | DARKNUT_SWORD 0x04000000 | MOBLIN_SPEAR 0x10000000 (d_a_am2.cpp:236-240)
#define EMK_AT_SKULL_HAMMER      0x10000      // (d_a_am2.cpp:274)
#define EMK_AT_BOMB              0x20         // (d_a_am2.cpp:282)
#define EMK_AT_ARROWS            0xC4000      // NORMAL_ARROW 0x4000 | FIRE_ARROW 0x40000 | ICE_ARROW 0x80000 (d_a_am2.cpp:285-287)
#define EMK_AT_OLD_PATH          0x300040     // LIGHT_ARROW 0x100000 | WIND 0x200000 | BOOMERANG 0x40: never applied directly (:227-233, 262-268), the sphere path keeps them
// cc_at_check__FP10fopAc_ac_cP8CcAtInfo (GZLE01 symbols.txt:3275) and CcAtInfo (d_cc_uty.h:7-21)
#define EMK_FN_CC_AT_CHECK       0x800AEEF8
#define EMK_CCATINFO_SIZE        0x1C         // zero-initialised: mbDead (+0x09) must start 0 (d_cc_uty.cpp:233-240)
#define EMK_CCATINFO_OBJ         0x00         // cCcD_Obj* mpObj = the host's static At sphere (WWENEMY_SPH_OFF)
#define EMK_CCATINFO_PARTICLE    0x14         // cXyz* pParticlePos = NULL (d_a_am2.cpp:296)

// --- row TN: Darknut, fpcNm_TN_e (d_a_tn.h:12-165) ---------------------------
// TN batch B (enemy-mirror-tn.json R-1; GZLE layout: no m0414_demo, m1520
// present, d_a_tn.h:69-72, 158-161; wwapi/addr/tn.go). A DR/STD row like
// BK. Wire: AngleY is current.angle.y (D8: daTn_Execute copies shape from
// it, d_a_tn.cpp:3551, 3615); the slot's blur_shape byte carries the host's
// pieces & EMK_TN_PIECE_MASK, never blur_scale; aux {dr.m48C.z, 0, 0, 0}.
#define EMK_TN_ID                6
#define EMK_TN_PROC              0x00C0       // f_pc_name.h:201; wwapi/addr/remote.go ProcTN
#define EMK_TN_CLASS_SIZE        0x1B08       // d_a_tn.h:165
#define EMK_TN_ARC               "Tn"
#define EMK_TN_BCK_FIRST         0x28         // AAT_KAITEN1 (res/Object/Tn.h:44)
#define EMK_TN_BCK_LAST          0x55         // S_DEMO_M2 (Tn.h:89); 0x56..0x67 are the Y* armor bcks the body never plays (D7)
#define EMK_TN_BCK_WAIT          0x3D         // AWAIT1 (Tn.h:65; d_a_tn.cpp:2205)
#define EMK_TN_MORF              0x2C4        // mpBodyMorf (d_a_tn.h:40)
#define EMK_TN_GATE              0x2BC        // u8 mBehaviorType (d_a_tn.h:33): 15 frozen-in-time refused; the s16 dr.mAction is read by the gate itself
#define EMK_TN_DR                0x474        // damagereaction mDamageReaction (d_a_tn.h:84)
#define EMK_TN_DR_MODE           0x478        // s16 dr.mMode (c_damagereaction.h:80)
#define EMK_TN_DR_ACTION         0x47A        // s16 dr.mAction (c_damagereaction.h:81; Action d_a_tn.cpp:25-42)
#define EMK_TN_TILT              0x900        // s16 dr.m48C.x (0x474 + 0x48C)
#define EMK_TN_CO_CYL            0xC74        // dCcD_Cyl mCoCyl (d_a_tn.h:94)
#define EMK_TN_TG_CYL            0xDA4        // dCcD_Cyl mTgCyl (d_a_tn.h:95)
#define EMK_TN_HEAD_SPH          0xED4        // dCcD_Sph mHeadSph (d_a_tn.h:96)
#define EMK_TN_DEF_SPH           0x1000       // dCcD_Sph mDefenceSph (d_a_tn.h:97): parries inside damage_check (v2 ACK result parried; detail defence 9 only under HIT_V1)
#define EMK_TN_HEAD_CENTER       0x1384       // cXyz m1384 (d_a_tn.h:100), the head sphere's centre (d_a_tn.cpp:3625)
#define EMK_TN_DEF_CENTER        0x13E4       // cXyz m13E4 (d_a_tn.h:108): the ITEM_L1 translation (R-7 e; d_a_tn.cpp:2831-2837)
#define EMK_TN_CO_FORWARD        35           // mCoCyl at pos + 35 along current.angle.y (d_a_tn.cpp:3614-3621)
#define EMK_TN_TG_HIT_UP         68           // R30 sphere at +68 bottoms at +38, over the DEFENCE-lowered Tg top +37.5 (d_a_tn.cpp:3631-3634); live knob WWENEMY_ANNEX_TN_TGUP_OFF (R-13)
#define EMK_TN_INVULN            0x3F0        // s16 m03F0 (d_a_tn.h:60): a hit sets 5 (d_a_tn.cpp:2049)
#define EMK_TN_INVULN_WAIT       8            // compiled (ids >= 3 have no annex override)
#define EMK_TN_ICE               0x1524       // enemyice mEnemyIce (d_a_tn.h:162): shrink 0x152A, freeze 0x1532
#define EMK_TN_WEAPON            0xC34        // int m0C34 (d_a_tn.h:86): nonzero while the sword is held
#define EMK_TN_BARE              0
#define EMK_TN_BLUR_ON           0            // none: em_animate's u32 blur_shape store would wipe the pieces byte 0x2D0 (every generic blur write is guarded on blur_on)
#define EMK_TN_BLUR_SHAPE        0
#define EMK_TN_BLUR_SCALE        0
#define EMK_TN_ANM_GATE          0
#define EMK_TN_PLAY_SKIP         0x3EE        // s16 m03EE (d_a_tn.h:59; d_a_tn.cpp:3531-3536), streamed as WWENEMY_SF2_PLAY_SKIP
#define EMK_TN_SPH_SRC           0xED4        // mHeadSph
#define EMK_TN_CAPS              0x05         // DR | FOPEN; no MODEL_EQ: d_a_tn never assigns actor->model (the em_validate check would be ERR_MORF)
#define EMK_TN_MTX               1            // EMK_MTX_DR (dr_matrix_set at 0x474, tilt 0x900)
#define EMK_TN_COLL              1            // EMK_COLL_STD
#define EMK_TN_ATTACH            2            // EMK_ATTACH_TN
#define EMK_TN_EYE               0            // EMK_EYE_NONE: the nodeCallBack writes eyePos/attention/m1384 during calc (d_a_tn.cpp:282-305)
#define EMK_TN_ATTN_F2           0x08         // EMK_TN_F2_ATTN
#define EMK_TN_DEATH_SWITCH      0x2C0        // u8 mDisableSpawnOnDeathSwitch (d_a_tn.h:37): client Go writes it under quiet death; 1..0x7F is BOSS (refused)
// TN quiet death (p0_findings DEATH ORDER): the host ragdolls at health 0
// (dr.mMode -100, BDOWN_A1 then BDOWN_U1, ~1.6 s) before dr.mAction 20.
// The sampler's DYING includes health <= 0 with the ragdoll, so the copy
// starts em_die_quiet at the ragdoll's first frame; it holds scale 1 for
// QUIET_HOLD frames (the streamed BDOWN bcks and position keep playing:
// em_animate and the smoothing run on dying frames), then shrinks over
// the remaining QUIET_FRAMES - QUIET_HOLD. SF2_LIGHT keeps the generic 90
// with no hold, and a death that starts RELEASE (a catch-up or a copy
// first adopted from a tombstone: no ragdoll streams) skips the hold and
// shrinks over the 20 at once. Compiled (no annex for ids >= 3).
#define EMK_TN_QUIET_FRAMES      68           // 48 held + 20 shrinking (EMK_QUIET_FRAMES)
#define EMK_TN_QUIET_HOLD        48           // ~1.6 s of ragdoll at 30 executes/s (P0-b: ~97 Go samples at 62.5 Hz)
// TN class fields the gates, hooks, handback and quiet death use (d_a_tn.h)
#define EMK_TN_M02C1             0x2C1        // u8 m02C1 (d_a_tn.h:38): != 0 hidden (Draw, d_a_tn.cpp:437-439): gate
#define EMK_TN_SHIELD_MORF       0x2C8        // mDoExt_McaMorf* mpShieldMorf (d_a_tn.h:41)
#define EMK_TN_ARMOR_MORF        0x2CC        // mDoExt_McaMorf* mpArmorMorf (d_a_tn.h:42): part 0's model
#define EMK_TN_PIECES            0x2D0        // s8 mRemainingEquipmentPieces (d_a_tn.h:43): 0x0F with a shield, 0x0B without at create (d_a_tn.cpp:4232-4236, P0-b)
#define EMK_TN_PIECE_ARMOR       0x01         // EQUIPMENT_PIECE_BODY_ARMOR (d_a_tn.cpp:62-67): part 0, the Tg Shield bit
#define EMK_TN_PIECE_HELMET      0x02         // EQUIPMENT_PIECE_HELMET: part 1, the head Shield bit + purple hitmark
#define EMK_TN_PIECE_SHIELD      0x04         // EQUIPMENT_PIECE_SHIELD: part 2 and the shield morf
#define EMK_TN_PIECE_MASK        0x07         // the streamed bits; the AND keeps the copy's others: pieces &= (blur_shape | ~MASK) (0x08 UNKNOWN, never read after create)
#define EMK_TN_PART0             0x2E0        // tn_p mParts[3] (d_a_tn.h:50, tn_p :12-27)
#define EMK_TN_PART_STRIDE       0x4C
#define EMK_TN_PART_MODEL        0x00         // J3DModel* mpPartModel: parts 1 and 2 (0x32C, 0x378); part 0 draws the armor morf's model (d_a_tn.cpp:3286-3290)
#define EMK_TN_PART_M08          0x08         // s8 m08: < 0 part_draw skips it (d_a_tn.cpp:398-427)
#define EMK_TN_PART_M09          0x09         // s8 m09: 0 = on the body (copy anmMtx[j_dt[i]], d_a_tn.cpp:3291-3305)
#define EMK_TN_EQUIP             0x3C5        // u8 mEquipmentType (d_a_tn.h:52): >= EMK_TN_EQUIP_CAPE refused (D5)
#define EMK_TN_EQUIP_CAPE        4            // EQUIPMENT_SHIELD_AND_CAPE (d_a_tn.cpp:53-60)
#define EMK_TN_TILT_Z            0x904        // s16 dr.m48C.z (c_damagereaction.h:124): annex aux[0] -> em_consume_owned, as BK
#define EMK_TN_DR_M088           0x4FC        // csXyz dr.m088[20] (0x474 + 0x88): zeroed at adopt (R-5)
#define EMK_TN_DR_M088_LEN       0x78
#define EMK_TN_DEF_R             0x1124       // f32 mDefenceSph radius (0x1000 + 0x124): 70 at adopt (d_a_tn.cpp:2831)
#define EMK_TN_DEF_R_VAL         70
#define EMK_TN_M1402             0x1402       // s16 m1402 (d_a_tn.h:116): the armor wobble, zeroed at adopt and handback
#define EMK_TN_M140C             0x140C       // s8 m140C (d_a_tn.h:120): != 0 the sword is not attached yet (d_a_tn.cpp:3490-3497): gate, sword guard
#define EMK_TN_BOKO_PID          0x1410       // fpc_ProcID mBokoPcId (d_a_tn.h:122): the carried sword (EMK_BOKO_PROC)
#define EMK_TN_M1444             0x1444       // s8 m1444 (d_a_tn.h:141): the parry window, 0 at adopt and handback
#define EMK_TN_M14F8             0x14F8       // s8 m14F8 (d_a_tn.h:148): != 0 the demo camera (d_a_tn.cpp:2551-2575): gate
#define EMK_TN_M1518             0x1518       // s8 m1518 (d_a_tn.h:154): != 0 hidden (d_a_tn.cpp:3499-3522): gate
#define EMK_TN_BEHAVIOR_FROZEN   15           // BEHAVIOR_FROZEN_IN_TIME (d_a_tn.cpp:51): refused
#define EMK_TN_ACT_JYUNKAI       0            // dr.mAction (d_a_tn.cpp:25-42): the handback's
#define EMK_TN_ACT_DEFENCE       10           // SF_DEFENCE (== EM_DR_DEFENCE)
#define EMK_TN_ACT_DIE_A         20           // DYING 20..22: fail, yogan fail, water fail (22 from dr_body_bg_check, c_damagereaction.cpp:669-676)
#define EMK_TN_ACT_DIE_C         22
#define EMK_TN_ACT_D_DOZOU       23           // the statue: refused per sample (wakes later)
#define EMK_TN_ACT_S_DEMO        25           // refused per sample
// TN joints (res/Object/Tn.h:259-277) and the sword hand
#define EMK_TN_JOINT_MUNE1       0x0A         // armor (part 0)
#define EMK_TN_JOINT_ITEM_L1     0x0F         // shield hand (part 2, the def centre)
#define EMK_TN_JOINT_ITEM_R1     0x17         // sword hand
#define EMK_TN_JOINT_ATAMA1      0x1C         // helmet (part 1)
#define EMK_TN_HAND_RZ           0x8000       // sword: nodeMtx[ITEM_R1] * Rz(0x8000) * T(0, 0, 85) (d_a_tn.cpp:3708-3719; REG8 offsets 0)
#define EMK_TN_HAND_TZ           85
// TN bcks the REL picks itself (Tn.h:52-104): the shield morf follows the
// streamed body bck (R-6; d_a_tn.cpp:1924-1939, 2511, 2872, 3458), the
// armor morf free-runs YKIHON1 from adopt (R-5)
#define EMK_TN_BCK_AGUARD1       0x30         // same index on the shield
#define EMK_TN_BCK_AGUARD2       0x31         // same index on the shield
#define EMK_TN_BCK_AHAKAI1       0x33         // same index on the shield
#define EMK_TN_BCK_TATE_ON       0x3C         // ATATE_ON1, the shield's loop for every other body bck
#define EMK_TN_BCK_BDOWN_A1      0x4A         // the death ragdoll's bcks (P0-b), in the body range
#define EMK_TN_BCK_BDOWN_U1      0x4B
#define EMK_TN_BCK_ARMOR_KIHON   0x64         // YKIHON1: the armor morf (outside the body range: em_bck_res reads any index)
// The collider bits R-12 writes (d_cc_d.h:56-60, 98-103; dCcD_GObjTg at
// +0x94 of a collider, its mSPrm +0x00, mHitMark +0x1E)
#define EMK_GTG_SPRM             0x94         // u32 dCcD_GObjTg mSPrm
#define EMK_GTG_HITMARK          0xB2         // u8 dCcD_GObjTg mHitMark
#define EMK_GTG_SPRM_SHIELD      0x01         // dCcG_TgSPrm_Shield_e (OnTgShield)
#define EMK_GTG_HITMARK_PURPLE   0x0C         // dCcg_TgHitMark_Purple_e (d_a_tn.cpp:3648)
#define EMK_GTG_HITMARK_UNK1     0x01         // dCcG_TgHitMark_Unk1_e (d_a_tn.cpp:3652)
#define EMK_TN_F2_HAS_WEAPON     0x01         // flags2 bits: host m0C34 != 0 (== WWENEMY_SF2_HAS_WEAPON)
#define EMK_TN_F2_PLAY_SKIP      0x02         // host m03EE != 0 (== WWENEMY_SF2_PLAY_SKIP)
#define EMK_TN_F2_ATTN           0x08         // host attention_info.flags != 0
// TN's hook_disable bits (R-3; annex byte WWENEMY_ANNEX_HKDX_OFF + 3 =
// 0x1FF): per-kind meanings of WWENEMY_HKD_* bits whose generic reads are
// dead for a DR, non-MODEL_EQ, non-PLAIN, eyeless row with id >= 3
// (MODEL_EQ enemy_mirror.c:1357, EYE :2302-2303, LEGACY_DEATH :2230-2231,
// MTX_PLAIN :1715). ATTN (0x10) keeps its generic meaning; EXTRA (0x80) is
// not reused (generic code returns on it, :1927, :2039).
#define EMK_TN_HKD_SHIELD        0x01         // == WWENEMY_HKD_MODEL_EQ (tn:hooks=model_eq): skip the shield pre-calc (R-6)
#define EMK_TN_HKD_ATTACH        0x02         // == WWENEMY_HKD_ATTACH (attach): skip parts, armor play/calc, pieces AND, def centre (R-7 a-c, e)
#define EMK_TN_HKD_SWORD         0x04         // == WWENEMY_HKD_EYE (eye): skip the sword carry/matrix/drop and the TN quiet drop (R-7 d, R-8)
#define EMK_TN_HKD_COLL          0x08         // == WWENEMY_HKD_LEGACY_DEATH (legacy_death): skip the TN em_colliders_pre case (R-12)
#define EMK_TN_HKD_ADOPT_ANM     0x20         // == WWENEMY_HKD_MTX_PLAIN (mtx_plain): skip the adopt-time armor setAnm and the armor play (R-5, R-7 c)
#define EMK_TN_HKD_DIRECT        0x40         // == WWENEMY_HKD_SPECIAL (special): reserved for batch C's TN direct apply, a no-op in B (D13)

// --- row BB: Kargaroc, fpcNm_BB_e (d_a_bb.h:13-108) -------------------------
// Batch C (enemy-mirror-batch-c.json "bb", R-10..R-18; D9, D10). An
// fopEn_enemy_c with no damagereaction, drawn from current.angle (never
// shape: d_a_bb.cpp:2313-2326 reads current.angle, and no shape_angle write
// exists in d_a_bb.cpp). Wire: AngleY is current.angle.y, tilt
// current.angle.x, angle.z in slot +0x18 (WWENEMY_SL_CC_M316Z_OFF, the CC
// m316.z bytes). Riders (types 5/6) are refused on both sides (D9). Default
// off; the row's hook byte is WWENEMY_HKD_HI_OFF + 0 (0x80413128).
#define EMK_BB_ID                7
#define EMK_BB_PROC              0x00B6       // f_pc_name.h:191; wwapi/addr/remote.go ProcBB
#define EMK_BB_CLASS_SIZE        0x1260       // d_a_bb.h:108
#define EMK_BB_ARC               "Bb"
#define EMK_BB_BCK_FIRST         0x15         // ATACK01 (res/Object/Bb.h:25)
#define EMK_BB_BCK_LAST          0x22         // WALK (Bb.h:38)
#define EMK_BB_BCK_WAIT          0x18         // FLY01 (Bb.h:28); WAIT is 0x21, not the handback loop
#define EMK_BB_MORF              0x2B4        // mpMorf (d_a_bb.h:16)
#define EMK_BB_GATE              0x2D8        // u8 unk_2D8, the type (params byte 0, d_a_bb.cpp:2689-2737): 5/6 riders refused (em_bb_gate, Go ParamsOK)
#define EMK_BB_DR                0
#define EMK_BB_DR_MODE           0
#define EMK_BB_DR_ACTION         0
#define EMK_BB_TILT              0x204        // s16 current.angle.x (EMK_CAP_TILT_CUR, R-12): the smoothed slot tilt
#define EMK_BB_CO_CYL            0x940        // dCcD_Sph mBodyCoSph (d_a_bb.h:80; R 87.5): EMK_CAP_SPH_COLL
#define EMK_BB_TG_CYL            0x814        // dCcD_Sph mBodyTgSph (d_a_bb.h:79; R 62.5): hit collider 2
#define EMK_BB_HEAD_SPH          0x6E8        // dCcD_Sph mHeadTgSph (d_a_bb.h:78): hit collider 1
#define EMK_BB_DEF_SPH           0
#define EMK_BB_HEAD_CENTER       0x260        // eyePos (EMK_AC_EYE_POS): the nodeCallBack writes it inside every calc (d_a_bb.cpp:133-197)
#define EMK_BB_DEF_CENTER        0
#define EMK_BB_CO_FORWARD        0
#define EMK_BB_TG_HIT_UP         0            // the body Tg sphere is centred on current.pos (knob table: bb:tg_hit_up)
#define EMK_BB_INVULN            0x340        // s16 unk_340: damage_check runs only while it is 0 (d_a_bb.cpp:2446)
#define EMK_BB_INVULN_WAIT       8            // inferred (EM_INVULN_WAIT); knob table bb:invuln
#define EMK_BB_ICE               0xC80        // enemyice mEnemyIce (d_a_bb.h:106): shrink 0xC86, freeze 0xC8E
#define EMK_BB_WEAPON            0
#define EMK_BB_BARE              0
#define EMK_BB_BLUR_ON           0
#define EMK_BB_BLUR_SHAPE        0
#define EMK_BB_BLUR_SCALE        0
#define EMK_BB_ANM_GATE          0
#define EMK_BB_PLAY_SKIP         0
#define EMK_BB_SPH_SRC           0x6E8        // mHeadTgSph (its hit callbacks are unverified: the REL implementer checks d_a_bb.cpp's create before R-10 ships)
#define EMK_BB_CAPS              0xC4         // FOPEN | SPH_COLL | TILT_CUR; no DR, no MODEL_EQ (BB never writes actor.model)
#define EMK_BB_MTX               5            // EMK_MTX_TRYXZ
#define EMK_BB_COLL              1            // EMK_COLL_STD over the spheres (SPH_COLL)
#define EMK_BB_ATTACH            0            // EMK_ATTACH_NONE (the tail step is BB's own, R-14)
#define EMK_BB_EYE               0            // EMK_EYE_NONE: the nodeCallBack writes eyePos and the attention position during calc
#define EMK_BB_ATTN_F2           0x01         // EMK_BB_F2_ATTN (d_a_bb.cpp:2251-2252: LOCKON_BATTLE | SHOWMAP every unhidden frame)
#define EMK_BB_QUIET_FRAMES      20           // compiled; knob table bb:quiet
// BB class fields the gates, hooks, handback and tail use (d_a_bb.h)
#define EMK_BB_TYPE_PERCH        4            // keyed perch type (M_NewD2 r3 set 0, p 0xFFFFFF04)
#define EMK_BB_TYPE_RIDER_MO2    5            // carries a Moblin (d_a_bb.cpp:2739-2754): refused (D9)
#define EMK_BB_TYPE_RIDER_BK     6            // carries a Bokoblin: refused (D9)
#define EMK_BB_TYPE_PERCH2       7            // keyed perch type (M_NewD2 r3 set 2, p 0xFFFF0507)
#define EMK_BB_EYE_BTP           0x2CC        // u8 unk_2CC: the eye btp frame (Draw :625), 0 behind EMK_BB_HKD_EXTRA
#define EMK_BB_STATE             0x2DD        // u8 unk_2DD: the handback writes EMK_BB_STATE_ATTACK (d_a_bb.cpp:2180-2184)
#define EMK_BB_STATE_ATTACK      3
#define EMK_BB_RIDER             0x2DF        // u8 unk_2DF: != 0 refused
#define EMK_BB_M2F0              0x2F0        // s8 unk_2F0: handback 0
#define EMK_BB_M2F1              0x2F1        // s8 unk_2F1: handback 0
#define EMK_BB_DRAW_GATE         0x2F2        // u8 unk_2F2 = byte3 + 1 while hidden (d_a_bb.cpp:611-614, 2242-2249): the REL writes 0 while the host slot is not SF_HIDDEN (R-15), never in the handback
#define EMK_BB_TIMERS            0x318        // s16 unk_318[6] (d_a_bb.h:45)
#define EMK_BB_ATTACK_TIMER      0x31A        // s16 unk_318[1]: the handback seeds it (d_a_bb.cpp:2182)
#define EMK_BB_ATTACK_SEED       450          // l_bbHIO.unk_16 = 0x1C2 (d_a_bb.cpp:41): vanilla seeds unk_16 + cM_rndF(unk_18 - unk_16); the REL writes this low end (resolves the design's open HIO range)
#define EMK_BB_ATTACK_SEED_MAX   750          // l_bbHIO.unk_18 = 0x2EE (d_a_bb.cpp:42)
#define EMK_BB_KNOCK             0x348        // f32 unk_348: knockback (d_a_bb.cpp:2269-2294); > 0.01 refused at adopt; handback 0
#define EMK_BB_M34C              0x34C        // f32 unk_34C: handback 0
#define EMK_BB_M35D              0x35D        // s8 unk_35D: handback 0 (d_a_bb.cpp:2183)
#define EMK_BB_M366              0x366        // s16 unk_366: added to Rx (d_a_bb.cpp:2319), demo-only writer: zeroed at adopt
#define EMK_BB_M368              0x368        // s16 unk_368: added to Ry (d_a_bb.cpp:2318): zeroed at adopt
#define EMK_BB_GROUND_H          0x44C        // f32 mAcch (0x3B8) m_ground_h (d_bg_s_acch.h:235): -1e9 before tail_control behind EMK_BB_HKD_EXTRA
#define EMK_BB_AT_SPH            0x5BC        // dCcD_Sph mHeadAtSph: the host Sets it on its own Link only; the copy never does
#define EMK_BB_TAIL_MODELS       0xA84        // J3DModel* unk_A84[9]: the tail models Draw places (d_a_bb.cpp:289-306)
#define EMK_BB_TAIL_POS          0xAA8        // cXyz unk_AA8[10] (d_a_bb.h:83): [0] = BD4[1] after calc; tail_control rebuilds 1..9
#define EMK_BB_TAIL_ROT          0xB20        // csXyz unk_B20[10]
#define EMK_BB_TAIL_VEL          0xB5C        // cXyz unk_B5C[10] (d_a_bb.h:85): seeded 0
#define EMK_BB_TAIL_ROOT         0xBD4        // cXyz unk_BD4[2]: written by the nodeCallBack inside calc; [1] (0xBE0) is the tail root
#define EMK_BB_TAIL_ROOT1        0xBE0        // unk_BD4[1]
#define EMK_BB_TAIL_LEN          10           // AA8 and B5C entries; tail_control loops 1..9 (d_a_bb.cpp:230), tail_draw 0..8 (:293)
#define EMK_BB_HEAD_ROT          0xC00        // csXyz unk_C00[11]: zeroed before calc behind EMK_BB_HKD_BEAK (d_a_bb.cpp:2410-2412); the matrix reads [0].x and [0].z
#define EMK_BB_HEAD_ROT_LEN      0x42         // 11 csXyz, 0xC00..0xC41
#define EMK_BB_BEAK              0xC4E        // s16 unk_C4E: the beak joints (nodeCallBack), zeroed with C00
#define EMK_BB_C58               0xC58        // f32 unk_C58: handback 0
#define EMK_BB_DEAD              0xC7E        // u8 unk_C7E: mbDead (d_a_bb.cpp:2138-2141): DYING, refused at adopt
#define EMK_BB_ICE_Y             0xCA8        // f32 mEnemyIce.m028 (0xC80 + 0x28): the matrix's y lift
// BB's tail_control call (D10, R-14 c): exec = *(*(a + EMK_AC_SUB_METHOD) +
// EMK_METHOD_EXECUTE) is daBb_Execute; tail_control = exec -
// EMK_BB_TEXT_EXEC + EMK_BB_TEXT_TAIL. Called only when exec and tc are in
// MEM1 and every fingerprint word matches (retail GZLE d_a_bb.rel SHA-1
// 4271e18f447d7b2d2b052a1ae4e0f0862052269f, tww config.yml:1158-1159;
// symbols.txt:8,10,11,44), checked once per adopt; a miss bumps
// WWENEMY_FP_MISS_OFF and collapses the tail onto BD4[1] each frame.
#define EMK_BB_TEXT_EXEC         0x69A8       // daBb_Execute .text offset
#define EMK_BB_TEXT_TAIL         0x404        // tail_control__FP8bb_class .text offset (size 0x5A0)
#define EMK_BB_TAIL_BLR_OFF      0x59C        // tc + this: its blr
#define EMK_BB_FP_EXEC0          0x9421FF70
#define EMK_BB_FP_EXEC1          0x7C0802A6
#define EMK_BB_FP_EXEC2          0x90010094
#define EMK_BB_FP_EXEC3          0xDBE10080
#define EMK_BB_FP_TAIL0          0x9421FEB0
#define EMK_BB_FP_TAIL1          0x7C0802A6
#define EMK_BB_FP_TAIL2          0x90010154
#define EMK_BB_FP_TAIL3          0xDBE10140
#define EMK_BB_FP_BLR            0x4E800020
#define EMK_BB_F2_ATTN           0x01         // flags2 bits: host attention_info.flags != 0
// BB's hook_disable bits (byte WWENEMY_HKD_HI_OFF + 0; D7): per-kind names
// for bits whose generic readers are dead for a non-DR, non-MODEL_EQ,
// non-PLAIN, eyeless, attach-less row; ATTN and EXTRA keep their generic
// readers and meanings. 0x01 and 0x08 are unused.
#define EMK_BB_HKD_TAIL          0x02         // == WWENEMY_HKD_ATTACH (bb:hooks=tail): skip the tail step (R-14: AA8[0] copy, tail_control, seeds)
#define EMK_BB_HKD_BEAK          0x04         // == WWENEMY_HKD_EYE (beak): skip the C00/C4E zeroing before calc
#define EMK_BB_HKD_ATTN          0x10         // == WWENEMY_HKD_ATTN (attn): generic
#define EMK_BB_HKD_MTX           0x20         // == WWENEMY_HKD_MTX_PLAIN (mtx): skip EMK_MTX_TRYXZ (the copy freezes, proving the bit)
#define EMK_BB_HKD_HANDBACK      0x40         // == WWENEMY_HKD_SPECIAL (handback): release with no BB writes (R-17)
#define EMK_BB_HKD_EXTRA         0x80         // == WWENEMY_HKD_EXTRA (extra): skip the eye btp 0, the ground clamp and the dying tail fold (R-18)

typedef char emk_check_bk_ambush[(EMK_BK_HKD_AMBUSH == WWENEMY_HKD_SPECIAL &&
                                  EMK_BK_TYPE_AMBUSH_JUMP + 1 == EMK_BK_TYPE_AMBUSH_LEAP &&
                                  EMK_BK_TYPE_AMBUSH_LEAP + 1 == EMK_BK_TYPE_TORCH) ? 1 : -1];
typedef char emk_check_bb_row[(EMK_BB_ID == EMK_KIND_BB && EMK_KIND_BB == 7 &&
                               EMK_KIND_BB < 7 + WWENEMY_HKD_HI_KINDS && EMK_KIND_BB < WWENEMY_KNOB_KINDS &&
                               EMK_BB_HKD_TAIL == WWENEMY_HKD_ATTACH && EMK_BB_HKD_BEAK == WWENEMY_HKD_EYE &&
                               EMK_BB_HKD_ATTN == WWENEMY_HKD_ATTN && EMK_BB_HKD_MTX == WWENEMY_HKD_MTX_PLAIN &&
                               EMK_BB_HKD_HANDBACK == WWENEMY_HKD_SPECIAL && EMK_BB_HKD_EXTRA == WWENEMY_HKD_EXTRA &&
                               EMK_BB_CAPS == (EMK_CAP_FOPEN | EMK_CAP_SPH_COLL | EMK_CAP_TILT_CUR) &&
                               EMK_BB_MTX == EMK_MTX_TRYXZ && EMK_BB_COLL == EMK_COLL_STD &&
                               EMK_BB_ATTACH == EMK_ATTACH_NONE && EMK_BB_EYE == EMK_EYE_NONE &&
                               EMK_BB_ATTN_F2 == EMK_BB_F2_ATTN && EMK_BB_TILT == EMK_AC_ANGLE_X &&
                               EMK_BB_ICE_Y == EMK_BB_ICE + 0x28 && EMK_BB_TAIL_ROOT1 == EMK_BB_TAIL_ROOT + 12 &&
                               EMK_BB_TAIL_POS + EMK_BB_TAIL_LEN * 12 == EMK_BB_TAIL_ROT &&
                               EMK_BB_TAIL_VEL + EMK_BB_TAIL_LEN * 12 == EMK_BB_TAIL_ROOT &&
                               EMK_BB_HEAD_ROT + EMK_BB_HEAD_ROT_LEN <= EMK_BB_BEAK &&
                               EMK_BB_ATTACK_TIMER == EMK_BB_TIMERS + 2 && EMK_BB_ATTACK_SEED < EMK_BB_ATTACK_SEED_MAX &&
                               EMK_BB_BCK_WAIT >= EMK_BB_BCK_FIRST && EMK_BB_BCK_WAIT <= EMK_BB_BCK_LAST &&
                               EMK_BB_TEXT_TAIL + EMK_BB_TAIL_BLR_OFF < EMK_BB_TEXT_EXEC) ? 1 : -1];

#endif
