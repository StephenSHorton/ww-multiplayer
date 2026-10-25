// boats.c: the other players' King of Red Lions (include/wwboat.h; Go's
// side is mods/multiplayer/boatsync). Adapted from datstarkey/ww-online
// GameMod/src/puppet_link/puppet_boat.c, MIT (notice at the end of this
// file): the hull and head morfs on the remote's mast and head bcks, the
// joint-callback swap around our calc, the cannon and crane on the mast
// joint, and the sail cloth as an imitation daGrid_c run through the
// game's own ho_move and packet draw, recoloured with the tunic colour.
//
// The hard rule (theirs and ours): never a second daShip_c. Its create
// calls dComIfGp_setShipActor (d_a_ship.cpp:4746, mpPlayerPtr[2]) and its
// execute writes this player's sail status and audio. A boat here is
// models the keeper makes, poses in its execute and enters in its draw.
//
// Changed for us:
//   - Position, heading, pitch and roll are the remote's own (Go puts an
//     aboard boat at the remote's smoothed mini-Link position plus the
//     streamed ship-to-Link offset), not this game's sea: the mini-Link is
//     drawn at the remote's streamed height, so a hull on our waves would
//     leave it floating or sunk. No dead reckoning, no wave sampling.
//   - Models only while this game's "Ship" archive is resident (the sea
//     and the stages that carry a Ship actor), checked every frame against
//     dRes_getRes_byIdx; no dComIfG_resLoad of our own. Parked boats are
//     only ever on such a stage (the remote's ship was there).
//   - One solid heap per entry from GameHeap (as daShip_c::createHeap's
//     own actor heap), destroyed when the entry empties, the archive goes
//     or changes, a stage change starts, the magic drops, the keeper is
//     deleted or the module unlinks. Never the archive heap: Ship.arc
//     unmounts on every sea -> island -> sea trip, so a keep-forever model
//     would leak once per trip (docs/05 F18).
//   - Not drawn: the head's look angles (m03A0/m03A2, headJointCallBack1)
//     and its talk mouth, the hull recolour, the crane's rope and hook
//     (setRopePos's physics; the arm alone is drawn), the wake, the
//     shadow and the water-effect texture matrix. No collision.
//   - The sail's SAIL_ON comes from the remote's mStateFlag (Go streams the
//     bit), not a mast-frame guess.
//
// Shared model data (d_a_ship.cpp:4573-4629): daShip_c hangs its joint
// callbacks on fn_body, fn_head_h, vfncn and vfncr's joints and finds its
// ship through model->getUserArea(), which is 0 for our models and is not
// checked by the head, cannon and crane callbacks. mDoExt_McaMorf::calc
// also leaves its morf in joint 0's mMtxCalc (m_Do_ext.cpp:1547). So
// around every calc of ours each joint's callback becomes ours and its
// mMtxCalc 0, and both are put back after (bt_calc).
#include "mailbox_core.h"
#include "wwboat.h"
#include "boats.h"

#define BT_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + WWBOAT_OFF + (off)))
#define BT_BYTE(off) (*(volatile u8*)(MAILBOX_ADDR + WWBOAT_OFF + (off)))
#define BT_AT(p, t, off) (*(t*)((u8*)(p) + (off)))

// --- game addresses (GZLE01, zeldaret/tww config/GZLE01/symbols.txt) ----
typedef void* (*bt_heap_new_t)(u32 size, u32 align);
typedef s32 (*bt_heap_fn_t)(void* heap);
typedef void (*bt_heap_void_t)(void* heap);
typedef void* (*bt_new_t)(u32 size);
typedef void* (*bt_ctor_t)(void* self);
typedef s32 (*bt_set_skin_t)(J3DModel* model, void* skin, u32 flags);
typedef void* (*bt_morf_ct_t)(void* self, int complete, void* data, void* cb1, void* cb2, void* anm, int mode,
                              f32 morf, int start, int end, int interp, void* snd, u32 flags, u32 diff);
typedef void (*bt_morf_set_t)(void* self, void* anm, int mode, f32 morf, f32 speed, f32 start, f32 end, void* bas);
typedef void (*bt_fn_t)(void* self);
typedef void (*bt_rot_t)(f32* m, s16 a);
typedef void (*bt_rot3_t)(f32* m, s16 x, s16 y, s16 z);
typedef void (*bt_mtx2_t)(const f32* a, const f32* b, f32* ab);
typedef f32 (*bt_mag_t)(const cXyz* v);
typedef void (*bt_tev_set_t)(void* env, int type, cXyz* pos, void* tev);
typedef void (*bt_tev_light_t)(void* env, J3DModel* model, void* tev);
typedef void (*bt_tev_init_t)(void* tev, s8 room, u8 override);
typedef int (*bt_zsort_t)(void* buf, void* packet);
typedef void (*bt_store_t)(void* p, u32 n);
#define bt_heap_from_game   ((bt_heap_new_t)0x80011B54)   // mDoExt_createSolidHeapFromGame__FUlUl
#define bt_heap_adjust      ((bt_heap_fn_t)0x80011D0C)    // mDoExt_adjustSolidHeap__FP12JKRSolidHeap
#define bt_heap_destroy     ((bt_heap_void_t)0x80011D48)  // mDoExt_destroySolidHeap__FP12JKRSolidHeap
#define bt_op_new           ((bt_new_t)0x802B0C38)        // __nw__FUl (the current heap)
#define bt_skin_ct          ((bt_ctor_t)0x802F40C0)       // __ct__13J3DSkinDeformFv
#define bt_set_skin         ((bt_set_skin_t)0x802EE4BC)   // setSkinDeform__8J3DModelFP13J3DSkinDeformUl
#define bt_morf_ct          ((bt_morf_ct_t)0x80012650)    // __ct__14mDoExt_McaMorf...: r4 = complete-object flag (extsh./beq at 0x800126A0)
#define bt_morf_set         ((bt_morf_set_t)0x80012D78)   // setAnm__14mDoExt_McaMorfFP15J3DAnmTransformiffffPv
#define bt_morf_calc        ((bt_fn_t)0x800132E8)         // calc__14mDoExt_McaMorfFv
#define bt_zxy_rot          ((bt_rot3_t)0x8000CBEC)       // mDoMtx_ZXYrotS__FPA4_fsss: Ry(y) Rx(x) Rz(z)
#define bt_xrot             ((bt_rot_t)0x8000CD28)        // mDoMtx_XrotS__FPA4_fs
#define bt_yrot             ((bt_rot_t)0x8000CDC8)        // mDoMtx_YrotS__FPA4_fs
#define bt_yrot_m           ((bt_rot_t)0x8000CE28)        // mDoMtx_YrotM__FPA4_fs: m = m * Ry
#define bt_zrot             ((bt_rot_t)0x8000CE68)        // mDoMtx_ZrotS__FPA4_fs
#define bt_concat           ((bt_mtx2_t)0x8030D0FC)       // PSMTXConcat
#define bt_vec_mag          ((bt_mag_t)0x8030DE68)        // PSVECMag
#define bt_tev_setting      ((bt_tev_set_t)0x80193028)    // settingTevStruct__18dScnKy_env_light_cFiP4cXyzP12dKy_tevstr_c
#define bt_tev_light        ((bt_tev_light_t)0x80193A34)  // setLightTevColorType__18dScnKy_env_light_cFP8J3DModelP12dKy_tevstr_c
#define bt_tev_init         ((bt_tev_init_t)0x80196EB4)   // dKy_tevstr_init__FP12dKy_tevstr_cScUc
#define bt_ho_move          ((bt_fn_t)0x800E9C0C)         // ho_move__FP8daGrid_c
#define bt_ho_draw          ((bt_fn_t)0x800E93B8)         // draw__13daHo_packet_cFv
#define bt_matpk_same       0x802DB950                    // isSame__12J3DMatPacketCFP12J3DMatPacket (daHo_packet_c's vtable[2], 0x8038BBE8)
#define bt_matpk_entry      0x800EB328                    // entry__12J3DMatPacketFP13J3DDrawBuffer (vtable[3])
#define bt_zsort            ((bt_zsort_t)0x802ECAF0)      // entryZSort__13J3DDrawBufferFP12J3DMatPacket (reads mpZMtx +0x1C)
#define bt_store            ((bt_store_t)0x80303108)      // DCStoreRange
#define BT_L_SHIP           (*(volatile u32*)0x803F6EC0)  // d_a_grid.cpp's l_ship (.sbss; ho_move: lwz r3,-0x7220(r13), r13 0x803FE0E0)
#define BT_ENV_LIGHT        ((void*)0x803E4AB4)           // g_env_light
#define BT_J3DSYS           0x803EDA58                    // j3dSys: mViewMtx at +0, the opa/xlu draw buffers at +0x48/+0x4C
#define BT_CUR_MTX          ((f32*)0x803EDB80)            // J3DSys::mCurrentMtx
#define BT_DL_OPA_P1        0x803CA930                    // g_dComIfG_gameInfo.drawlist .mpOpaListP1 (+0x0C), daShip_c::draw's list
#define BT_DL_XLU_P1        0x803CA934                    // .mpXluListP1 (+0x10)
#define BT_DL_XLU           0x803CA944                    // .mpXluList (+0x20): daGrid_c::_draw's sail list
#define BT_STAY_NO          (*(volatile s8*)0x803F6A78)   // dStage_roomControl_c::mStayNo

// "Ship" (assets/GZLE01/res/Object/Ship.h), "Cloth" (Cloth.h).
static const char bt_arc[] = "Ship";
static const char bt_cloth[] = "Cloth";
#define SHIP_BCK_FIRST    0x05    // AKIBI1
#define SHIP_BCK_LOOK_L   0x07
#define SHIP_BCK_MAST_OFF 0x0A
#define SHIP_BCK_MAST_ON  0x0B
#define SHIP_BCK_LAST     0x0E    // KYAKKAN1
#define SHIP_BDL_BODY     0x11
#define SHIP_BDL_HEAD     0x12
#define SHIP_BDL_CANNON   0x13
#define SHIP_BDL_CRANE    0x14
#define SHIP_BTI_HO1      0x17
#define CLOTH_BTI_TOON    0x03
#define JNT_GATTAI        0x04    // FN_BODY: the head's base (d_a_ship.cpp:3966)
#define JNT_KAJI          0x05
#define JNT_MAST          0x06
#define JNT_SAIL1         0x07
#define JNT_SAIL2         0x08
#define JNT_STEER1        0x0A
#define JNT_CANON1        0x01    // VFNCN
#define JNT_CANON2        0x02
#define JNT_CRANE_ROT     0x01    // VFNCR V_CRANE_ROTATION
#define PART_CANNON       2       // daShip_c::PART_CANNON_e
#define PART_CRANE        3       // PART_CRANE_e

// Layouts (decomp offsets; mDoExt_McaMorf checked in the DOL: new(0xB4) at
// 0x800E639C, the ctor's mpModel at +0x50).
#define MORF_SIZE         0xB4
#define MORF_MODEL        0x50    // J3DModel* mpModel
#define MORF_FRAME_END    0x60    // mFrameCtrl (+0x58) .mEnd (+0x08), s16
#define MORF_FRAME        0x68    // mFrameCtrl .mFrame (+0x10), f32
#define MDL_DATA          0x04    // J3DModel::mModelData
#define MDL_BASE          0x24    // mBaseTransformMtx
#define MDL_NODE          0x8C    // mpNodeMtx
#define MDL_SKIN          0xC0    // mpSkinDeform
#define MDATA_JNT_NUM     0x28    // J3DModelData joint tree mJointNum (u16)
#define MDATA_JNT_PTR     0x2C    // mJointNodePointer
#define NODE_CB           0x08    // J3DNode::mCallBack
#define JNT_NO            0x18    // J3DJoint::mJntNo
#define JNT_MTXCALC       0x58    // J3DJoint::mMtxCalc
#define SKIN_SIZE         0x18    // J3DSkinDeform (li r3,0x18 at 0x80123A8C)
#define SKIN_POS_USE      0x04
#define BT_MAX_JOINTS     20      // fn_head_h has 18, fn_body 11
#define BT_HEAP_SIZE      0x20000 // daShip_c-sized, shrunk by adjust once built
#define TEV_SIZE          0xB0    // dKy_tevstr_c
#define TEV_ROOM          0xA9    // s8 mRoomNo
#define TEV_ENVR_OVR      0xAA    // u8 mEnvrIdxOverride: 0xFF none
#define ACTOR_TEVSTR      0x10C   // fopAc_ac_c::tevStr
#define BT_MODEL_FLAG     0x80000
#define BT_BODY_DIFF      0x11200202  // daShip_c::createHeap (d_a_ship.cpp:4408-4412)
#define BT_PART_DIFF      0x11000002
#define BT_RETRY_FRAMES   120     // after a failed build

// The sail (d_a_grid.cpp; d_a_grid.h). The cloth is not in fn_body.bdl:
// daShip_c spawns a daGrid_c (d_a_ship.cpp:4693) whose daHo_packet_c draws
// an 85-vertex mesh with new_ho1.bti and Cloth's toon ramp, moved by
// ho_move from the wind and l_ship's mSailAngle / SAIL_ON (the only two
// l_ship reads in ho_move: 0x800E9CF8, 0x800E9DB0 lha +0x364; 0x800E9F80
// lwz +0x358). We keep a zeroed daGrid_c in the boat heap and point
// l_ship at a stub for the one ho_move call.
#define GRID_SIZE         0x221C
#define GRID_POS          0x1F8   // fopAc current.pos
#define GRID_ANGLE        0x204   // current.angle (ho_move reads .y at +0x206)
#define GRID_PACKET       0x2A0   // daHo_packet_c mPacket
#define GRID_AMP          0x1B54  // f32[85] field_0x1b54: per-vertex flutter
#define GRID_FOLD         0x2200  // f32 field_0x2200: 1 - SAIL2's X length
#define GRID_VTX          85
#define HO_SHAPE_PTR      0x28    // ctor: stw (packet+0x3C),0x28(packet) at 0x800EAA9C; entryZSort clears its +4/+8
#define HO_SHAPE          0x3C
#define HO_MTX            0x80    // mMtx: view * model
#define HO_TEVSTR         0xB0    // dKy_tevstr_c* mpTevStr
#define HO_ALPHA          0x18A3  // ctor: li r0,0xFF; stb 0x18A3 (0x800EAA94)
#define SAIL_SHOW         0x221C  // u8* (the grid's tail pad): the palette the draw shows, 0 = vanilla
#define SAIL_TIMG         0x2220  // u8* new_ho1's ResTIMG, 0 = no recolour
#define SAIL_PAL          0x2240  // 32-aligned: two u16[256] palettes
#define SAIL_PAL_BYTES    0x200
#define SAIL_BYTES        (SAIL_PAL + 2 * SAIL_PAL_BYTES)
#define SHIP_STATE_FLAG   0x358   // daShip_c::mStateFlag (d_a_ship.h:313)
#define SHIP_SAIL_ANGLE   0x364   // mSailAngle (:316)
#define SHIP_SAIL_ON      0x200   // daSFLG_SAIL_ON_e
#define SAIL_MIN_SCALE    0.06f   // daGrid_c::_execute/_draw skip below it (d_a_grid.cpp:813, :823)
#define DRAWBUF_ZMTX      0x1C    // J3DDrawBuffer::mpZMtx
#define TIMG_FMT          0x00
#define TIMG_PAL_FMT      0x09
#define TIMG_NCOL         0x0A
#define TIMG_PAL_OFF      0x0C

typedef struct {
    void* heap;          // JKRSolidHeap*, 0 = no boat
    u32 data;            // the Ship fn_body model data it was made from
    u8* body_anm;        // mDoExt_McaMorf*
    u8* head_anm;
    J3DModel* body;
    J3DModel* head;
    J3DModel* cannon;
    J3DModel* crane;
    u8* sail;            // imitation daGrid_c + palettes, 0 = none
    u32 sail_key;        // the recoloured palette's RGB | 0x01000000, 0 = none yet
    f32 sail_scale;
    cXyz pos;
    s16 ax, ay, az, sail_angle, tiller, part_a, part_b;
    u16 retry;
    u8 mast_bck, head_bck, flags, part, posed, sail_show, sail_cur, sail_want;
    u32 tev[TEV_SIZE / 4];
} bt_boat;

static bt_boat bt_boats[WWBOAT_MAX];

static int bt_mem1(u32 p) {
    return p >= 0x80000000u && p < MEM1_END;
}

static void bt_err(u32 e) {
    BT_WORD(WWBOAT_STAT_ERR_OFF) = e;
}

static void* bt_res(const char* arc, u32 idx) {
    return dRes_getRes_byIdx(arc, (s32)idx, MOBJECT_INFO, OBJECT_INFO_COUNT);
}

static void bt_zero(void* p, u32 n) {
    u8* b = (u8*)p;
    u32 i;
    for (i = 0; i < n; i++) b[i] = 0;
}

static void bt_copy(void* d, const void* s, u32 n) {
    u8* a = (u8*)d;
    const u8* b = (const u8*)s;
    u32 i;
    for (i = 0; i < n; i++) a[i] = b[i];
}

static void bt_free(bt_boat* b) {
    if (b->heap) bt_heap_destroy(b->heap);
    bt_zero(b, sizeof(*b) - sizeof(b->tev));
}

// --- models ---------------------------------------------------------------
// new mDoExt_McaMorf(data, 0, 0, bck, EMode_NONE, 0, 0, -1, 0, 0, flags, diff)
// in the current heap, as daShip_c::createHeap makes mpBodyAnm / mpHeadAnm.
static u8* bt_morf(u32 bdl, u32 bck, u32 diff) {
    void* data = bt_res(bt_arc, bdl);
    void* anm = bt_res(bt_arc, bck);
    u8* m;
    if (!data || !anm) return 0;
    m = (u8*)bt_op_new(MORF_SIZE);
    if (!m) return 0;
    bt_morf_ct(m, 1, data, 0, 0, anm, 0, 0.0f, 0, -1, 0, 0, BT_MODEL_FLAG, diff);
    return BT_AT(m, u32, MORF_MODEL) ? m : 0;
}

// The hull is CPU-skinned like the local ship's (d_a_ship.cpp:4414-4424):
// setSkinDeform rewrites the SHARED shapes' skin flags at entry, so a copy
// without one would flip the local ship's shapes to the other path.
static int bt_skin(J3DModel* m) {
    u8* skin = (u8*)bt_op_new(SKIN_SIZE);
    if (!skin) return 0;
    bt_skin_ct(skin);
    return bt_set_skin(m, skin, 1) == 0 && BT_AT(skin, u32, SKIN_POS_USE) != 0;
}

static J3DModel* bt_part(u32 bdl) {
    void* data = bt_res(bt_arc, bdl);
    return data ? mDoExt_J3DModel__create(data, BT_MODEL_FLAG, BT_PART_DIFF) : 0;
}

// The sail block: the imitation daGrid_c as a new actor leaves it (zeroed),
// daHo_packet_c's ctor fields (the vtable is ours, the shape packet pointer,
// alpha 0xFF) and _create's field_0x1b54. 0 if new_ho1 is not what the
// recolour expects (C8, RGB565, at most 256 colours): no recolour then.
void bt_sail_draw(u8* packet);
static void bt_noop(u8* packet) { (void)packet; }
// J3DPacket vtable {RTTI, this-offset, isSame, entry, draw, dtor}, as
// __vt__13daHo_packet_c (0x8038BBE0: 0, 0, 0x802DB950, 0x800EB328,
// 0x800E93B8, 0x800EB520) with our draw. Only draw is called; the block
// goes with the heap, never through the dtor.
static const void* const bt_sail_vtbl[6] = {0, 0, (const void*)bt_matpk_same, (const void*)bt_matpk_entry,
                                            (const void*)bt_sail_draw, (const void*)bt_noop};
// _create's field_0x1b54 (d_a_grid.cpp:674-759), computed from GZLE01's l_pos
// (0x8038B1D8) and stored x64 (ww-online's table, puppet_boat.c, agrees to
// 4 decimals).
static const u16 bt_sail_amp[GRID_VTX] = {
    0, 1338, 2281, 2552, 2281, 1338, 0, 898, 2874, 4394, 4927, 4652, 3470, 2158,
    1645, 4104, 5717, 6342, 6202, 5255, 4053, 2116, 4837, 6382, 7004, 6989, 6333, 5325,
    2233, 4990, 6491, 7142, 7220, 6765, 5866, 2116, 4730, 6180, 6883, 7042, 6761, 5964,
    1645, 3912, 5418, 6311, 6611, 6450, 5682, 898, 2681, 4237, 5319, 5801, 5760, 5145,
    0, 1755, 3297, 4440, 5045, 5119, 4771, 983, 1666, 2755, 3764, 4553, 5051, 5218,
    1250, 1531, 2151, 2851, 3524, 4121, 4611, 1012, 1100, 1343, 1666, 2022, 2390, 2755,
    0,
};

static u8* bt_sail_make(void* heap) {
    // 32-aligned: the palettes are GX TLUTs (and ho_move flushes the
    // vertex arrays in the same block); new would give 4.
    u8* s = (u8*)JKRHeap_alloc(SAIL_BYTES, 32, heap);
    u8* t;
    int i;
    if (!s) return 0;
    bt_zero(s, SAIL_BYTES);
    BT_AT(s + GRID_PACKET, const void*, 0) = bt_sail_vtbl;
    BT_AT(s + GRID_PACKET, u8*, HO_SHAPE_PTR) = s + GRID_PACKET + HO_SHAPE;
    s[GRID_PACKET + HO_ALPHA] = 0xFF;
    for (i = 0; i < GRID_VTX; i++) BT_AT(s, f32, GRID_AMP + i * 4) = (f32)bt_sail_amp[i] * (1.0f / 64.0f);
    t = (u8*)bt_res(bt_arc, SHIP_BTI_HO1);
    if (t && t[TIMG_FMT] == 0x09 && t[TIMG_PAL_FMT] == 0x01 && BT_AT(t, u16, TIMG_NCOL) <= 0x100)
        BT_AT(s, u8*, SAIL_TIMG) = t;
    return s;
}

// Build the boat in a solid heap of its own. 1 on success.
static int bt_build(bt_boat* b, u32 data, int sail) {
    void* heap = bt_heap_from_game(BT_HEAP_SIZE, 0x20);
    void* old;
    if (!heap) {
        bt_err(WWBOAT_ERR_HEAP);
        return 0;
    }
    old = JKRHeap_becomeCurrentHeap(heap);
    b->heap = heap;
    b->data = data;
    b->sail_want = (u8)(sail != 0);
    b->body_anm = bt_morf(SHIP_BDL_BODY, SHIP_BCK_MAST_OFF, BT_BODY_DIFF);
    if (b->body_anm && !bt_skin(BT_AT(b->body_anm, J3DModel*, MORF_MODEL))) b->body_anm = 0;
    if (b->body_anm) b->head_anm = bt_morf(SHIP_BDL_HEAD, SHIP_BCK_LOOK_L, BT_PART_DIFF);
    if (b->head_anm) {
        b->cannon = bt_part(SHIP_BDL_CANNON);
        b->crane = bt_part(SHIP_BDL_CRANE);
        if (sail) {
            b->sail = bt_sail_make(heap);
            if (!b->sail || !BT_AT(b->sail, u8*, SAIL_TIMG)) bt_err(WWBOAT_ERR_SAIL);
        }
    }
    JKRHeap_becomeCurrentHeap(old);
    if (!b->head_anm) {
        bt_err(WWBOAT_ERR_MODEL);
        bt_free(b);
        return 0;
    }
    bt_heap_adjust(heap);
    b->body = BT_AT(b->body_anm, J3DModel*, MORF_MODEL);
    b->head = BT_AT(b->head_anm, J3DModel*, MORF_MODEL);
    b->mast_bck = SHIP_BCK_MAST_OFF;
    b->head_bck = SHIP_BCK_LOOK_L;
    bt_tev_init(b->tev, -1, 0xFF);
    return 1;
}

// --- pose -------------------------------------------------------------------
// Which model bt_calc is calculating, for the callback (actors run on one
// thread; 0 outside bt_calc).
#define CALC_BODY   1
#define CALC_CANNON 2
#define CALC_CRANE  3
static bt_boat* bt_cur;
static J3DModel* bt_cur_model;
static u8 bt_cur_which;

static void bt_mtx_copy(f32* d, const f32* s) {
    int i;
    for (i = 0; i < 12; i++) d[i] = s[i];
}

// d = anm * r, then anm = d and J3DSys::mCurrentMtx = d (what daShip_c's
// callbacks end with, d_a_ship.cpp:127-129).
static void bt_apply(f32* anm, const f32* r) {
    f32 d[12];
    bt_concat(anm, r, d);
    bt_mtx_copy(anm, d);
    bt_mtx_copy(BT_CUR_MTX, d);
}

// daShip_c's joint callbacks with the remote's values (d_a_ship.cpp:110-179):
//   hull: STEER1 / KAJI anm * Rz(m0366); SAIL1 anm * Rz(-mSailAngle); MAST:
//     the crane's base anm * Rz(0xC000), the cannon's that * Ry(-0x8000),
//     then anm * scale(m03E8) (0.001 with a part out);
//   cannon: CANON1 anm * Rx(m0394), CANON2 anm * Ry(-m0396);
//   crane: V_CRANE_ROTATION anm * Rz(-(m0398 + m039C)).
int bt_jnt_cb(u8* node, int timing) {
    bt_boat* b = bt_cur;
    f32 r[12];
    f32* anm;
    int j, i;
    if (timing != 0 || !b || !bt_cur_model) return 1;
    j = BT_AT(node, u16, JNT_NO);
    anm = BT_AT(bt_cur_model, f32*, MDL_NODE) + j * 12;
    if (bt_cur_which == CALC_BODY) {
        if (j == JNT_STEER1 || j == JNT_KAJI) {
            bt_zrot(r, b->tiller);
        } else if (j == JNT_SAIL1) {
            bt_zrot(r, (s16)-b->sail_angle);
        } else if (j == JNT_MAST) {
            bt_zrot(r, (s16)0xC000);
            if (b->crane) bt_concat(anm, r, (f32*)((u8*)b->crane + MDL_BASE));
            if (b->cannon) {
                bt_concat(anm, r, (f32*)((u8*)b->cannon + MDL_BASE));
                bt_yrot_m((f32*)((u8*)b->cannon + MDL_BASE), (s16)0x8000);
            }
            if (b->flags & WWBOAT_EF_MAST_HIDE) {
                for (i = 0; i < 12; i++) if ((i & 3) != 3) anm[i] *= 0.001f;
            }
            bt_mtx_copy(BT_CUR_MTX, anm);
            return 1;
        } else {
            return 1;
        }
    } else if (bt_cur_which == CALC_CANNON) {
        if (j == JNT_CANON1) bt_xrot(r, b->part_a);
        else if (j == JNT_CANON2) bt_yrot(r, (s16)-b->part_b);
        else return 1;
    } else if (bt_cur_which == CALC_CRANE && j == JNT_CRANE_ROT) {
        bt_zrot(r, (s16)-b->part_a);
    } else {
        return 1;
    }
    bt_apply(anm, r);
    return 1;
}

// Calc one model (a morf's, or a plain one) with every joint's callback
// ours (cb) or none, and every mMtxCalc cleared, put back after.
static void bt_calc(bt_boat* b, J3DModel* model, u8* morf, u8 which) {
    u8* data = BT_AT(model, u8*, MDL_DATA);
    u8** jnt = BT_AT(data, u8**, MDATA_JNT_PTR);
    int n = BT_AT(data, u16, MDATA_JNT_NUM), i;
    u32 cb[BT_MAX_JOINTS], mc[BT_MAX_JOINTS];
    if (!bt_mem1((u32)jnt) || n > BT_MAX_JOINTS) return;   // can't guard the ship's hooks: unposed
    for (i = 0; i < n; i++) {
        cb[i] = BT_AT(jnt[i], u32, NODE_CB);
        mc[i] = BT_AT(jnt[i], u32, JNT_MTXCALC);
        BT_AT(jnt[i], u32, NODE_CB) = which ? (u32)bt_jnt_cb : 0;
        BT_AT(jnt[i], u32, JNT_MTXCALC) = 0;
    }
    bt_cur = b;
    bt_cur_model = model;
    bt_cur_which = which;
    if (morf) bt_morf_calc(morf);
    else J3DModel_calc(model);
    bt_cur = 0;
    bt_cur_model = 0;
    for (i = 0; i < n; i++) {
        BT_AT(jnt[i], u32, NODE_CB) = cb[i];
        BT_AT(jnt[i], u32, JNT_MTXCALC) = mc[i];
    }
}

// The remote's bck at its frame (speed 0, no blend: the frames stream in),
// below the end as a finished bck sits (play() leaves it at end - 0.001).
static void bt_bck(u8* morf, u8* cur, u32 bck, u8 frame) {
    s16 end;
    if (bck != *cur) {
        void* anm = bt_res(bt_arc, bck);
        if (!anm) return;
        bt_morf_set(morf, anm, 0, 0.0f, 0.0f, 0.0f, -1.0f, 0);
        *cur = (u8)bck;
    }
    end = BT_AT(morf, s16, MORF_FRAME_END);
    BT_AT(morf, f32, MORF_FRAME) = frame < end ? (f32)frame : (f32)end - 0.001f;
}

static f32 bt_col_len(const f32* m, int c) {
    cXyz v;
    v.x = m[c];
    v.y = m[4 + c];
    v.z = m[8 + c];
    return bt_vec_mag(&v);
}

static void bt_sail_color(bt_boat* b, u32 rgb);

// daShip_c's grid placement (d_a_ship.cpp:4049-4070) on our posed hull,
// then daGrid_c::_execute's ho_move with l_ship standing for the remote's
// ship (mStateFlag and mSailAngle, all ho_move reads through it).
static void bt_sail_move(bt_boat* b, u32 rgb) {
    u8* s = b->sail;
    f32* node = BT_AT(b->body, f32*, MDL_NODE);
    f32* s1 = node + JNT_SAIL1 * 12;
    u32 ship[4];
    u32 saved;
    b->sail_show = 0;
    if (!s || !bt_res(bt_cloth, CLOTH_BTI_TOON)) return;
    b->sail_scale = bt_col_len(s1, 2);
    if (b->sail_scale < SAIL_MIN_SCALE) return;            // mast down, or hidden for a part
    BT_AT(s, f32, GRID_FOLD) = 1.0f - bt_col_len(node + JNT_SAIL2 * 12, 0);
    BT_AT(s, f32, GRID_POS) = s1[3];
    BT_AT(s, f32, GRID_POS + 4) = s1[7];
    BT_AT(s, f32, GRID_POS + 8) = s1[11];
    BT_AT(s, s16, GRID_ANGLE) = b->ax;
    BT_AT(s, s16, GRID_ANGLE + 2) = b->ay;
    BT_AT(s, s16, GRID_ANGLE + 4) = b->az;
    ship[0] = (b->flags & WWBOAT_EF_SAIL_ON) ? SHIP_SAIL_ON : 0;
    ship[1] = ship[2] = ship[3] = 0;
    *(s16*)((u8*)ship + (SHIP_SAIL_ANGLE - SHIP_STATE_FLAG)) = b->sail_angle;
    saved = BT_L_SHIP;
    BT_L_SHIP = (u32)ship - SHIP_STATE_FLAG;
    bt_ho_move(s);
    BT_L_SHIP = saved;
    bt_sail_color(b, rgb);
    b->sail_show = 1;
}

static void bt_pose(bt_boat* b, u32 e, u32 bflags) {
    f32* base = (f32*)((u8*)b->body + MDL_BASE);
    f32* node;
    u8 hb;
    union { u32 u; f32 f; } x, y, z;
    x.u = BT_WORD(e + WWBOAT_E_POS);
    y.u = BT_WORD(e + WWBOAT_E_POS + 4);
    z.u = BT_WORD(e + WWBOAT_E_POS + 8);
    if (!(x.f == x.f && y.f == y.f && z.f == z.f) || x.f < -1.0e6f || x.f > 1.0e6f || y.f < -1.0e6f ||
        y.f > 1.0e6f || z.f < -1.0e6f || z.f > 1.0e6f) return;
    b->pos.x = x.f;
    b->pos.y = y.f;
    b->pos.z = z.f;
    b->flags = BT_BYTE(e + WWBOAT_E_FLAGS);
    b->part = (bflags & WWBOAT_F_PARTS) ? BT_BYTE(e + WWBOAT_E_PART) : 0;
    b->ax = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_ANGLE);
    b->ay = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_ANGLE + 2);
    b->az = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_ANGLE + 4);
    b->sail_angle = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_SAIL_ANGLE);
    b->tiller = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_TILLER);
    b->part_a = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_PART_A);
    b->part_b = *(volatile s16*)(MAILBOX_ADDR + WWBOAT_OFF + e + WWBOAT_E_PART_B);

    // T(pos) * ZXYrot(shape_angle), as daShip_c::execute (d_a_ship.cpp:3960-3962).
    bt_zxy_rot(base, b->ax, b->ay, b->az);
    base[3] = b->pos.x;
    base[7] = b->pos.y;
    base[11] = b->pos.z;
    bt_bck(b->body_anm, &b->mast_bck, (b->flags & WWBOAT_EF_MAST_ON) ? SHIP_BCK_MAST_ON : SHIP_BCK_MAST_OFF,
           BT_BYTE(e + WWBOAT_E_MAST_FRAME));
    hb = BT_BYTE(e + WWBOAT_E_HEAD_BCK);
    if (hb < SHIP_BCK_FIRST || hb > SHIP_BCK_LAST || hb == SHIP_BCK_MAST_OFF || hb == SHIP_BCK_MAST_ON) hb = b->head_bck;
    bt_bck(b->head_anm, &b->head_bck, hb, BT_BYTE(e + WWBOAT_E_HEAD_FRAME));
    bt_calc(b, b->body, b->body_anm, CALC_BODY);
    node = BT_AT(b->body, f32*, MDL_NODE);
    if (!bt_mem1((u32)node)) return;
    bt_mtx_copy((f32*)((u8*)b->head + MDL_BASE), node + JNT_GATTAI * 12);
    bt_calc(b, b->head, b->head_anm, 0);
    if (b->part == PART_CANNON && b->cannon) bt_calc(b, b->cannon, 0, CALC_CANNON);
    else if (b->part == PART_CRANE && b->crane) bt_calc(b, b->crane, 0, CALC_CRANE);
    else b->part = 0;
    b->sail_show = 0;
    if (bflags & WWBOAT_F_SAIL)
        bt_sail_move(b, ((u32)BT_BYTE(e + WWBOAT_E_R) << 16) | ((u32)BT_BYTE(e + WWBOAT_E_G) << 8) | BT_BYTE(e + WWBOAT_E_B));
    b->posed = 1;
}

void boats_frame(void) {
    u32 data = 0, flags, i;
    int on = BT_WORD(WWBOAT_MAGIC_OFF) == WWBOAT_MAGIC && NEXT_STAGE_ENABLE == 0;
    if (on) data = (u32)bt_res(bt_arc, SHIP_BDL_BODY);
    flags = BT_WORD(WWBOAT_FLAGS_OFF);
    for (i = 0; i < WWBOAT_MAX; i++) {
        bt_boat* b = &bt_boats[i];
        u32 e = WWBOAT_ENTRIES_OFF + i * WWBOAT_E_SIZE;
        b->posed = 0;
        if (!data || !(BT_BYTE(e + WWBOAT_E_FLAGS) & WWBOAT_EF_SHOW) || (b->heap && b->data != data) ||
            (b->heap && ((flags & WWBOAT_F_SAIL) != 0) != b->sail_want)) {
            if (b->heap) bt_free(b);
            if (!data || !(BT_BYTE(e + WWBOAT_E_FLAGS) & WWBOAT_EF_SHOW)) continue;
        }
        if (!b->heap) {
            if (b->retry) {
                b->retry--;
                continue;
            }
            if (!bt_build(b, data, (flags & WWBOAT_F_SAIL) != 0)) {
                b->retry = BT_RETRY_FRAMES;
                continue;
            }
        }
        bt_pose(b, e, flags);
    }
}

// --- draw -------------------------------------------------------------------
// daGrid_c::_draw (d_a_grid.cpp:822-843): T(pos) * YXZrot(shape_angle) *
// Yrot(mSailAngle) * scale(1, scale.y, 1), lit by a copy of the ship's
// tevStr, Z-sorted into the translucent list.
static void bt_sail_entry(bt_boat* b) {
    u8* s = b->sail;
    u8* pk = s + GRID_PACKET;
    u32 xlu = *(volatile u32*)BT_DL_XLU;
    f32 m[12];
    u32 saved;
    int i;
    if (!bt_mem1(xlu) || !bt_res(bt_cloth, CLOTH_BTI_TOON) || !bt_res(bt_arc, SHIP_BTI_HO1)) return;
    bt_zxy_rot(m, b->ax, b->ay, b->az);
    bt_yrot_m(m, b->sail_angle);
    for (i = 0; i < 3; i++) m[i * 4 + 1] *= b->sail_scale;
    m[3] = BT_AT(s, f32, GRID_POS);
    m[7] = BT_AT(s, f32, GRID_POS + 4);
    m[11] = BT_AT(s, f32, GRID_POS + 8);
    bt_concat((const f32*)BT_J3DSYS, m, (f32*)(pk + HO_MTX));
    bt_copy(s + ACTOR_TEVSTR, b->tev, TEV_SIZE);
    BT_AT(pk, u8*, HO_TEVSTR) = s + ACTOR_TEVSTR;
    saved = BT_AT(xlu, u32, DRAWBUF_ZMTX);
    BT_AT(xlu, u32, DRAWBUF_ZMTX) = (u32)m;     // entryZSort reads it at once
    bt_zsort((void*)xlu, pk);
    BT_AT(xlu, u32, DRAWBUF_ZMTX) = saved;
}

void boats_draw(void) {
    u32 i;
    for (i = 0; i < WWBOAT_MAX; i++) {
        bt_boat* b = &bt_boats[i];
        u32 opa, xlu;
        u8* tev = (u8*)b->tev;
        if (!b->heap || !b->posed) continue;
        if (BT_STAY_NO < 0) continue;     // a stage's first frames: no room to light it for yet
        // Lit for the room this game stands in (on the sea the remote's
        // square may not be loaded here), as daShip_c::draw (d_a_ship.cpp:253-255).
        tev[TEV_ROOM] = (u8)BT_STAY_NO;
        tev[TEV_ENVR_OVR] = 0xFF;
        bt_tev_setting(BT_ENV_LIGHT, 0, &b->pos, tev);
        bt_tev_light(BT_ENV_LIGHT, b->body, tev);
        bt_tev_light(BT_ENV_LIGHT, b->head, tev);
        opa = *(volatile u32*)(BT_J3DSYS + 0x48);
        xlu = *(volatile u32*)(BT_J3DSYS + 0x4C);
        *(volatile u32*)(BT_J3DSYS + 0x48) = *(volatile u32*)BT_DL_OPA_P1;   // dComIfGd_setListP1
        *(volatile u32*)(BT_J3DSYS + 0x4C) = *(volatile u32*)BT_DL_XLU_P1;
        mDoExt_modelEntryDL(b->body);
        mDoExt_modelEntryDL(b->head);
        if (b->part == PART_CANNON) {
            bt_tev_light(BT_ENV_LIGHT, b->cannon, tev);
            mDoExt_modelEntryDL(b->cannon);
        } else if (b->part == PART_CRANE) {
            bt_tev_light(BT_ENV_LIGHT, b->crane, tev);
            mDoExt_modelEntryDL(b->crane);
        }
        *(volatile u32*)(BT_J3DSYS + 0x48) = opa;
        *(volatile u32*)(BT_J3DSYS + 0x4C) = xlu;
        if (b->sail_show) bt_sail_entry(b);
        BT_WORD(WWBOAT_STAT_DRAWN_OFF)++;
    }
}

// --- the tunic-coloured sail --------------------------------------------------
// new_ho1's canvas is cream (231,215,189; luminance 216: that entry becomes
// the picked colour), the emblem teal (33,109,107), the corners orange
// (206,101,33). An entry's canvas weight is how far it is from teal along
// R-G times how far from orange along R-B. Adapted from ww-online's
// boat_sailRecolor.
static int bt_ramp(int v, int span) {
    if (v <= 0) return 0;
    if (v >= span) return 256;
    return (v * 256) / span;
}

static void bt_recolor(u16* dst, const u16* src, u32 count, u32 rgb) {
    u32 i;
    int k;
    for (i = 0; i < 0x100; i++) {
        u32 c = i < count ? src[i] : 0;
        int ch[3], lum, w, t;
        ch[0] = (c >> 11) & 0x1F;
        ch[1] = (c >> 5) & 0x3F;
        ch[2] = c & 0x1F;
        ch[0] = (ch[0] << 3) | (ch[0] >> 2);
        ch[1] = (ch[1] << 2) | (ch[1] >> 4);
        ch[2] = (ch[2] << 3) | (ch[2] >> 2);
        w = (bt_ramp(ch[0] - ch[1] + 70, 70) * bt_ramp(96 - (ch[0] - ch[2]), 32)) / 256;
        lum = (77 * ch[0] + 150 * ch[1] + 29 * ch[2]) >> 8;
        for (k = 0; k < 3; k++) {
            t = (int)((rgb >> (16 - 8 * k)) & 0xFF) * lum / 216;
            if (t > 255) t = 255;
            ch[k] += ((t - ch[k]) * w) / 256;
        }
        dst[i] = (u16)(((ch[0] >> 3) << 11) | ((ch[1] >> 2) << 5) | (ch[2] >> 3));
    }
}

// The palette this frame's sail draw shows: 0 (the live, vanilla one) for
// no colour, else a recoloured copy, double-buffered so a change never
// rewrites the one the GPU may still be loading.
static void bt_sail_color(bt_boat* b, u32 rgb) {
    u8* s = b->sail;
    u8* t = BT_AT(s, u8*, SAIL_TIMG);
    u16* pal;
    BT_AT(s, u8*, SAIL_SHOW) = 0;
    if (!t || rgb == 0) return;
    if (b->sail_key != (rgb | 0x01000000)) {
        if (b->sail_key) b->sail_cur ^= 1;
        pal = (u16*)(s + SAIL_PAL + b->sail_cur * SAIL_PAL_BYTES);
        bt_recolor(pal, (const u16*)(t + BT_AT(t, u32, TIMG_PAL_OFF)), BT_AT(t, u16, TIMG_NCOL), rgb);
        bt_store(pal, SAIL_PAL_BYTES);
        b->sail_key = rgb | 0x01000000;
    }
    BT_AT(s, u8*, SAIL_SHOW) = s + SAIL_PAL + b->sail_cur * SAIL_PAL_BYTES;
}

// Our packet's draw (the painter's xlu pass): the game's, with new_ho1's
// header pointed at this boat's palette and put back right after, so the
// local sail, drawn by its own packet, never sees it (the draw loads the
// palette through the header, d_a_grid.cpp:244-252).
void bt_sail_draw(u8* packet) {
    u8* s = packet - GRID_PACKET;
    u8* t = BT_AT(s, u8*, SAIL_TIMG);
    u8* pal = BT_AT(s, u8*, SAIL_SHOW);
    u32 saved;
    if (!t || !pal) {
        bt_ho_draw(packet);
        return;
    }
    saved = BT_AT(t, u32, TIMG_PAL_OFF);
    BT_AT(t, u32, TIMG_PAL_OFF) = (u32)pal - (u32)t;    // wraps when below: the GPU adds it back
    bt_ho_draw(packet);
    BT_AT(t, u32, TIMG_PAL_OFF) = saved;
}

// --- lifetime -------------------------------------------------------------------
void boats_link(void) {
    bt_zero(bt_boats, sizeof(bt_boats));
    bt_cur = 0;
    bt_cur_model = 0;
}

void boats_unlink(void) {
    u32 i;
    for (i = 0; i < WWBOAT_MAX; i++) bt_free(&bt_boats[i]);
}

// The boats are adapted from datstarkey/ww-online
// GameMod/src/puppet_link/puppet_boat.c, under this notice:
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
