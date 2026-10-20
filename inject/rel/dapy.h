// dapy.h: the daPy_lk_c address surface of the remote-Link full body
// (fullbody.c, shanm.c; ww-multiplayer-handoff/design/
// remote-link-stage4-plan.md D4-5). REL-private: every function is an
// absolute function-pointer cast (no relocation), C++ methods are written
// as `this`-first C signatures, and every data address is a #define. One
// definition per line, each followed by its source: SYM = zeldaret/tww
// config/GZLE01/symbols.txt line, DPM/DPMH = src/d/actor/d_a_player_main.cpp
// and its header, DIS = disassembled from inject/original.dol with
// `wwdev ppc-disasm` (WW_DOL_PATH) for this file.
#ifndef WW_DAPY_H
#define WW_DAPY_H

#include "wwapi.h"

// --- daPy_lk_c methods ----------------------------------------------------
typedef void (*dapy_void_t)(void* self);
typedef int  (*dapy_int_t)(void* self);
#define daPy_lk_ct            ((dapy_void_t)0x80125DB4) // __ct__9daPy_lk_cFv SYM:5064 (DIS: stwu r1,-48(r1))
#define daPy_lk_playerInit    ((dapy_void_t)0x80124C98) // playerInit__9daPy_lk_cFv SYM:5061 (DIS: stwu r1,-96(r1))
#define daPy_lk_makeBgWait    ((dapy_int_t)0x80127160)  // makeBgWait__9daPy_lk_cFv SYM:5067, returns cPhs_State (DIS: stwu r1,-112(r1))
#define daPy_lk_playerDelete  ((dapy_int_t)0x80122D58)  // playerDelete__9daPy_lk_cFv SYM:5050 (DIS: stwu r1,-32(r1))
#define daPy_lk_setCollision  ((dapy_void_t)0x8011D788) // setCollision__9daPy_lk_cFv SYM:5031 (DIS: stwu r1,-320(r1))

// --- other game functions ---------------------------------------------------
typedef void (*dapy_yrot_t)(Mtx m, s16 y);
#define mDoMtx_YrotS          ((dapy_yrot_t)0x8000CDC8) // mDoMtx_YrotS__FPA4_fs SYM:133: m = Ry(y), translation 0 (m_Do_mtx.cpp:116-136)
typedef void (*dapy_ptr_t)(void* p);
#define mDoExt_destroySolidHeap ((dapy_ptr_t)0x80011D48) // mDoExt_destroySolidHeap__FP12JKRSolidHeap SYM:306: heap->destroy(), no NULL check (m_Do_ext.cpp:1093-1095)
#define dKy_plight_cut        ((dapy_ptr_t)0x80195408)  // dKy_plight_cut__FP15LIGHT_INFLUENCE SYM:6292 (d_kankyo.cpp:2702-2715)

// --- vtables (fingerprints) ------------------------------------------------
#define DAPY_VT_LK            0x8038FDC0  // __vt__9daPy_lk_c SYM:15801, at +0x31C after the ctor
#define DAPY_VT_MODELDATA     0x8039EA34  // __vt__12J3DModelData SYM:17018 (J3DModelData +0x00)

// --- game data --------------------------------------------------------------
#define DAPY_G_GAMEINFO       0x803C4C08  // g_dComIfG_gameInfo SYM:17472 (play = +0x12A0)
#define DAPY_G_EVT_EXCEPTION  0x803C9EF8  // play.mEvtManager.mException, 0xC (d_event_manager.h:16-18)
#define DAPY_G_PLAYER_STATUS  0x803CA8D0  // play.mPlayerStatus[0][0..1], 8 (gameInfo+0x5CC8/+0x5CCC, DIS playerDelete 0x80123014-0x80123028)
#define DAPY_G_DIRECTION      0x803CA7E9  // play.mDirection, 1 (gameInfo+0x5BE1, DIS commonProcInit 0x8011292C)
#define DAPY_G_METRONOME      0x803CA7EF  // play.mMetronome, 1 (gameInfo+0x5BE7, DIS playerDelete 0x80123030)
#define DAPY_G_LAST_MODE      0x803C5D5C  // save.mRestart.mLastMode u32 (gameInfo+0x1154, DIS makeBgWait 0x80127298)
#define DAPY_G_KEEP_POS       0x803E440C  // l_debug_keep_pos, 0xC SYM:17675
#define DAPY_G_CUR_ANGLE      0x803F6F10  // l_debug_current_angle, 6 SYM:19001
#define DAPY_G_SHAPE_ANGLE    0x803F6F18  // l_debug_shape_angle, 6 SYM:19002
#define DAPY_G_MATANM         0x803F6F1E  // daPy_matAnm_c m_maba_timer/m_maba_flg/m_eye_move_flg/m_morf_frame, 4 SYM:19003-19006 (not init$4884 at 0x803F6F22)
#define DAPY_G_DMECB_TIMER    0x803F6F24  // daPy_dmEcallBack_c::m_timer, 2 SYM:19008
#define DAPY_G_DMECB_TYPE     0x803F62B8  // daPy_dmEcallBack_c::m_type, 2 SYM:18224
#define DAPY_G_PA_STATUS      0x803F6C44  // dPa_control_c::mStatus, 1 SYM:18811
#define DAPY_G_ZEL_BASIC      0x803F7710  // JAIZelBasic::zel_basic SYM:19554 (the `this` of mDoAud_setLink*Type, m_Do_audio.h:226-236)
#define DAPY_ZEL_GEAR_OFF     0x41        // setLinkSwordType/ShieldType/BootsType store this+0x41..+0x46 (DIS 0x802AB9F4, 0x802ABA44, 0x802ABA94)
#define DAPY_ZEL_GEAR_LEN     6
#define DAPY_G_SOLID_ERRORS   0x803F68F4  // solidHeapErrors (m_Do_machine.cpp:35-41)
#define DAPY_G_GAME_ERRORS    0x803F68F8  // GameHeapErrors
#define DAPY_G_CURRENT_HEAP   0x803F772C  // JKRHeap::sCurrentHeap SYM:19559 (DIS becomeCurrentHeap 0x802B03F8: r13-27060)
#define DAPY_G_SAVE_CUR_HEAP  0x803F6940  // mDoExt_SaveCurrentHeap SYM:18596 (DIS mDoExt_restoreCurrentHeap 0x80011E78: r13-30624)
#define DAPY_G_PLAYER_PTR0    0x803CA754  // play.mpPlayerPtr[0] = dComIfGp_getLinkPlayer() (wwapi.h PLAYER_PTR_ARRAY)

// --- fopAc_ac_c / daPy_py_c / daPy_lk_c fields ------------------------------
#define DAPY_HEAP             0x0F0       // JKRSolidHeap* heap (f_op_actor.h:275)
#define DAPY_CONDITION        0x1C8       // u32 actor_condition; fopAcCnd_INIT_e 0x08 (f_op_actor.h:51,285)
#define DAPY_COND_INIT        0x08
#define DAPY_HOME_POS         0x1D0       // cXyz home.pos
#define DAPY_OLD_POS          0x1E4       // cXyz old.pos
#define DAPY_SHAPE_ANGLE_Y    0x20E       // s16 shape_angle.y
#define DAPY_ATTN_FLAGS       0x280       // u32 attention_info.flags
#define DAPY_NO_RESET0        0x29C       // u32 mNoResetFlg0 (d_a_player.h:485)
#define DAPY_CUT_AT_FLG       0x00000040  // daPyFlg0_CUT_AT_FLG (d_a_player.h:172)
#define DAPY_NO_RESET1        0x2A0       // u32 mNoResetFlg1: daPyFlg1_CASUAL_CLOTHES 0x08 (d_a_player.h:202; DIS playerDelete 0x80122F4C)
#define DAPY_CASUAL           0x08
#define DAPY_GRAB_WEAR        0x2B0       // f32 field_0x2b0 (checkGrabWear)
#define DAPY_ITEM_HEAP0       0x2E90      // JKRSolidHeap* mpItemHeaps[0] (DPMH:2038; DIS playerDelete 0x80122F2C)
#define DAPY_ITEM_HEAP1       0x2E94      // mpItemHeaps[1] (DIS 0x80122F34)
#define DAPY_ITEM_ANIME_HEAP  0x2ECC      // JKRSolidHeap* mpItemAnimeHeap (DPMH:2049; DIS 0x80122F3C)
#define DAPY_ANM_UNDER_HEAP   0x2FE8      // m_anm_heap_under[0..1].mpAnimeHeap, stride 0x10 (DPMH:2079 +0xC; DIS 0x80122EDC)
#define DAPY_ANM_UPPER_HEAP   0x3008      // m_anm_heap_upper[0..2].mpAnimeHeap, stride 0x10 (DPMH:2080 +0xC; DIS 0x80122F00)
#define DAPY_TEX_ANM_HEAP     0x31C4      // m_tex_anm_heap.mpAnimeHeap (DPMH:2097 +0xC; DIS 0x80122F1C)
#define DAPY_TEX_SCROLL_HEAP  0x31D4      // m_tex_scroll_heap.mpAnimeHeap (DPMH:2098 +0xC; DIS 0x80122F24)
#define DAPY_LIGHT_INFLUENCE  0x3498      // LIGHT_INFLUENCE mLightInfluence (DPMH:2129; DIS 0x80122E9C), dKy_plight_set in playerInit (DPM:12313)
#define DAPY_DEMO_TYPE        0x304       // u16 mDemo.mDemoType (d_a_player.h:153)
#define DAPY_DEMO_MODE        0x314       // u32 mDemo.mDemoMode (d_a_player.h:159)
#define DAPY_ACCH             0x46C       // dBgS_LinkAcch mAcch (DPMH:2009)
#define DAPY_YUCHW00_BRK      0x2F34      // J3DAnmTevRegKey* mpYuchw00Brk (DPMH:2067)
#define DAPY_YBAFO00_BTK      0x2F3C      // J3DAnmTextureSRTKey* mpYbafo00Btk (DPMH:2069)
#define DAPY_ANM_FRAME_MAX    0x06        // J3DAnmBase s16 mFrameMax (J3DAnimation.h:308-333)
#define DAPY_ANM_FRAME        0x08        // J3DAnmBase f32 mFrame
#define DAPY_PROC_WAIT        0x04        // daPyProc_WAIT_e
#define DAPY_PROC_CROUCH_DEF  0x0C        // daPyProc_CROUCH_DEFENSE_e
#define DAPY_MODE_CLEAR       0x01002008u // mModeFlg DAMAGE 0x8 | IN_SHIP 0x2000 | CRAWL 0x01000000
#define DAPY_RESTIMG_WORDS    8           // ResTIMG, 0x20 bytes (the shared linktexS3TC header)

// --- J3D ----------------------------------------------------------------------
#define J3D_MODEL_DATA        0x04        // J3DModel: J3DModelData* mModelData
#define J3D_MODEL_BASE_TR     0x24        // J3DModel: Mtx mBaseTransformMtx (wwapi.h)
#define J3D_MODEL_NODE_MTX    0x8C        // J3DModel: Mtx* mpNodeMtx (J3DModel.h:75)
#define J3D_MD_JOINT_NUM      0x28        // J3DModelData: u16 mJointTree.mJointNum (J3DModelData.h:118)
#define J3D_MD_JOINTS         0x2C        // J3DModelData: J3DJoint** mJointTree.mJointNodePointer
#define J3D_MD_MAT_NUM        0x5C        // J3DModelData: u16 mMaterialTable.mMaterialNum (+0x58 + 0x04)
#define J3D_MD_MATS           0x60        // J3DModelData: J3DMaterial** mMaterialTable.mMaterialNodePointer
#define J3D_JOINT_MTX_CALC    0x58        // J3DJoint: J3DMtxCalc* mMtxCalc
#define J3D_MAT_ANM           0x3C        // J3DMaterial: J3DMaterialAnm* mMaterialAnm
#define J3D_MATANM_SLOTS      26          // J3DMaterialAnm +0x04..+0x68 (J3DMaterialAnm::initialize)
#define CL_JOINTS             42          // cl.bdl joints = the pose feed's (mailbox pose_joint_counts)
#define CL_JNT_UNTRACKED_A    0x02        // cl.bdl joints whose mMtxCalc makeBgWait sets and the shanm table
#define CL_JNT_UNTRACKED_B    0x1D        //   does not track (only joint 0 is): saved and restored around it

#endif
