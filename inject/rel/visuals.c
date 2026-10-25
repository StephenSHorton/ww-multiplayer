// visuals.c: what the remote players look like beyond the mini-Link body,
// drawn from the REL so T2 only holds the two call sites (include/wwvis.h).
//
// Sword, shield and scabbard (ww-online review idea 15). A mini-Link is
// cl.bdl alone; Link draws his sword and shield as separate models
// (daPy_lk_c::draw, d_a_player_main.cpp:1945-1965) and toggles the
// scabbard shape CL_PODA on the shared model data (:1869-1879). The
// remote's equip bytes come from Go (WWVIS_EQUIP: mSelectEquip[0..1] and
// two in-hand bits from its pose trailer); the placement is Link's own
// (the matrix block at :1270-1340), applied to the hand and scabbard
// joints of the remote's streamed pose:
//   sword out (mEquipItem == daPyItem_SWORD_e): hilt and blade at CL_LHANDA;
//   sheathed: hilt at podA * T(-11.25, 4.5, 0.45) * Rz(-0x1C71), no blade;
//   shield in hand: CL_RHANDA; else podA * T(15.5, 4.75, 0) * Rz(-0x4000).
// The hero's sword and the Master Sword (hilt + blade) and the hero's
// shield are drawn. Not drawn: the mirror shield (Link gives it a
// J3DSkinDeform, :11924-11928), the Master Sword's own scabbard model
// (mpPodmsModel), the sword glow and blur. The idea (each peer's own
// equipment from its slot bytes) is datstarkey/ww-online's
// (GameMod/src/puppet_link/puppet_draw.c, MIT); their puppets are real
// daPy_lk_c actors, so no code of theirs applies here.
//
// Our models (WWVIS_MODELS) are made once per boot on the archive heap
// from Link.arc's resident model data, like the mini-Links (F18 rule:
// keep forever), with Link's own flag 0x37221222. They share the model
// data with Link's own sword and shield, so two things Link leaves on it
// are not used: each joint's mMtxCalc (Link's bck entries point it at
// animators in his actor and item heap, :1213, :1342; m_Do_ext.cpp:469-
// 472) is cleared around our calc, so the joints take their bind pose
// from the basic calc, and each material's mMaterialAnm (the Master
// Sword's btk lives in his item heap, d_a_player_sword.inc) is cleared
// around calcMaterial. The lighting is set from Link #1's tevStr, as his
// entryDLSetLight does for his own. j3dSys is snapshotted and restored
// around all of it.
//
// Name tags (ww-online review idea 6): each drawn mini-Link slot's name
// (WWVIS_NAMES, from the roster) over the remote's head in the HUD's 2D
// list, hidden with the HUD; see vis_tag_queue. Adapted from ww-online's
// puppet_nametag.c, whose MIT notice is at the end of this file.
//
// Sea chart markers (ww-online review idea 14): the remotes on the sea
// drawn on the pause menu's sea chart; see wwvis_seachart_draw.

#include "mailbox_core.h"
#include "wwvis.h"
#include "visuals.h"

#define VIS_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + WWVIS_OFF + (off)))
#define VIS_BYTE(off) (*(volatile u8*)(MAILBOX_ADDR + WWVIS_OFF + (off)))

// --- game addresses (GZLE01, zeldaret/tww config/GZLE01/symbols.txt) ----
typedef void (*vis_light_t)(void* env_light, J3DModel* model, void* tevstr);
#define dScnKy_setLightTevColorType ((vis_light_t)0x80193A34) // setLightTevColorType__18dScnKy_env_light_cFP8J3DModelP12dKy_tevstr_c
#define G_ENV_LIGHT                 ((void*)0x803E4AB4)       // g_env_light
#define FOPAC_TEVSTR_OFF            0x10C                     // fopAc_ac_c::tevStr (f_op_actor.h:277)

// Link.arc indices (tww assets/GZLE01/res/Object/Link.h).
#define LINK_BDL_SHA      0x24
#define LINK_BDL_SWA      0x25
#define LINK_BDL_SWGRIPA  0x26
#define LINK_BDL_SWGRIPMS 0x45
#define LINK_BDL_SWMS     0x46
#define CL_JNT_LHANDA     0x08
#define CL_JNT_RHANDA     0x0C
#define CL_JNT_PODA       0x0D

// dItemNo_* (d_item_data.h:63-69).
#define ITEM_SWORD        0x38
#define ITEM_MASTER_1     0x39
#define ITEM_MASTER_2     0x3A
#define ITEM_SHIELD       0x3B
#define ITEM_MASTER_3     0x3E

// Layouts (decomp offsets).
#define J3DMODEL_DATA_OFF        0x04   // J3DModel::mModelData (J3DModel.h:143)
#define J3DJOINT_MTXCALC_OFF     0x58   // J3DJoint::mMtxCalc (J3DJoint.h:174)
#define J3DMODELDATA_MAT_NUM_OFF 0x5C   // mMaterialTable (+0x58) .mMaterialNum (+0x04)
#define J3DMODELDATA_MAT_PTR_OFF 0x60   // mMaterialTable .mMaterialNodePointer (+0x08)
#define J3DMATERIAL_ANM_OFF      0x3C   // J3DMaterial::mMaterialAnm (J3DMaterial.h:147)
#define VIS_SAVE_MAX             16     // joints / materials cleared at most (the five models have fewer)

static const char vis_arc[] = "Link";
static const u16 vis_model_idx[WWVIS_MODEL_KINDS] = {
    LINK_BDL_SWGRIPA, LINK_BDL_SWGRIPMS, LINK_BDL_SWA, LINK_BDL_SWMS, LINK_BDL_SHA,
};

static int vis_on(u32 flag) {
    return VIS_WORD(WWVIS_MAGIC_OFF) == WWVIS_MAGIC && (VIS_WORD(WWVIS_FLAGS_OFF) & flag) != 0;
}

static int vis_in_mem1(u32 p) {
    return p >= 0x80000000u && p < MEM1_END;
}

static void vis_err(u32 e) {
    VIS_WORD(WWVIS_STAT_ERR_OFF) = e;
}

// --- matrices -------------------------------------------------------------
static f32* vis_joint(J3DModel* model, int j) {
    u32 node = *(volatile u32*)((u8*)model + J3DMODEL_MP_NODE_MTX_OFFSET);
    if (!vis_in_mem1(node)) return 0;
    return (f32*)(node + (u32)j * 0x30);
}

static void vis_mtx_copy(f32* dst, const f32* src) {
    int i;
    for (i = 0; i < 12; i++) dst[i] = src[i];
}

// dst = a * [r | t]: Link's transS(t), ZrotM, revConcat(a) (r is the
// 2x2 of a Z rotation: c = cos, s = sin of the angle).
static void vis_mtx_place(f32* dst, const f32* a, f32 c, f32 s, f32 tx, f32 ty, f32 tz) {
    int row;
    for (row = 0; row < 3; row++) {
        const f32* ar = a + row * 4;
        f32* d = dst + row * 4;
        // Rz(angle) = [[c, -s, 0], [s, c, 0], [0, 0, 1]]
        d[0] = ar[0] * c + ar[1] * s;
        d[1] = -ar[0] * s + ar[1] * c;
        d[2] = ar[2];
        d[3] = ar[0] * tx + ar[1] * ty + ar[2] * tz + ar[3];
    }
}

// --- models ---------------------------------------------------------------
static J3DModel* vis_model(int slot, int kind) {
    volatile u32* word = &VIS_WORD(WWVIS_MODELS_OFF + (u32)(slot * WWVIS_MODEL_KINDS + kind) * 4);
    J3DModelData* data = (J3DModelData*)dRes_getRes_byIdx(vis_arc, vis_model_idx[kind], MOBJECT_INFO, OBJECT_INFO_COUNT);
    if (!data) {
        vis_err(WWVIS_ERR_RES);
        return 0;
    }
    u32 have = *word;
    if (vis_in_mem1(have) && *(volatile u32*)(have + J3DMODEL_DATA_OFF) == (u32)data) {
        return (J3DModel*)have;
    }
    // None yet, or made from model data Link.arc no longer hands out (a
    // re-mount; the old model is leaked, as minilink.c does).
    JKRHeap* old = JKRHeap_becomeCurrentHeap(mDoExt_getArchiveHeap());
    J3DModel* m = mDoExt_J3DModel__create(data, 0, 0x37221222);
    JKRHeap_becomeCurrentHeap(old);
    if (!m) {
        vis_err(WWVIS_ERR_CREATE);
        *word = 0;
        return 0;
    }
    *word = (u32)m;
    return m;
}

// Calc and enter one model at base matrix mtx, lit like Link #1.
static void vis_enter(J3DModel* model, const f32* mtx, void* link) {
    u8* data = *(u8**)((u8*)model + J3DMODEL_DATA_OFF);
    u32 saved[VIS_SAVE_MAX];
    int n, i;

    vis_mtx_copy((f32*)((u8*)model + J3DMODEL_BASE_TR_MTX_OFFSET), mtx);

    // Joints: no animator of Link's (bind pose).
    u16 jn = *(volatile u16*)(data + J3DMODELDATA_JOINT_NUM_OFFSET);
    u8** joints = *(u8***)(data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    n = (jn < VIS_SAVE_MAX) ? jn : VIS_SAVE_MAX;
    if (!vis_in_mem1((u32)joints)) n = 0;
    for (i = 0; i < n; i++) {
        saved[i] = 0;
        if (joints[i]) {
            saved[i] = *(volatile u32*)(joints[i] + J3DJOINT_MTXCALC_OFF);
            *(volatile u32*)(joints[i] + J3DJOINT_MTXCALC_OFF) = 0;
        }
    }
    J3DModel_calc(model);
    for (i = 0; i < n; i++) {
        if (joints[i]) *(volatile u32*)(joints[i] + J3DJOINT_MTXCALC_OFF) = saved[i];
    }

    dScnKy_setLightTevColorType(G_ENV_LIGHT, model, (u8*)link + FOPAC_TEVSTR_OFF);

    // Materials: no material animator of Link's.
    u16 mn = *(volatile u16*)(data + J3DMODELDATA_MAT_NUM_OFF);
    u8** mats = *(u8***)(data + J3DMODELDATA_MAT_PTR_OFF);
    n = (mn < VIS_SAVE_MAX) ? mn : VIS_SAVE_MAX;
    if (!vis_in_mem1((u32)mats) || mn > VIS_SAVE_MAX) n = -1;
    if (n >= 0) {
        for (i = 0; i < n; i++) {
            saved[i] = 0;
            if (mats[i]) {
                saved[i] = *(volatile u32*)(mats[i] + J3DMATERIAL_ANM_OFF);
                *(volatile u32*)(mats[i] + J3DMATERIAL_ANM_OFF) = 0;
            }
        }
        J3DModel_calcMaterial(model);
        for (i = 0; i < n; i++) {
            if (mats[i]) *(volatile u32*)(mats[i] + J3DMATERIAL_ANM_OFF) = saved[i];
        }
    } else {
        // More materials than we can park: skip calcMaterial and point
        // j3dSys at this model's textures for the bake ourselves.
        *J3D_SYS_M_MODEL = model;
        *(volatile u32*)(J3D_SYS_ADDR + J3D_SYS_M_TEXTURE_OFFSET) = *(volatile u32*)(data + J3DMODELDATA_TEXTURE_OFFSET);
    }
    J3DModel_diff(model);
    J3DModel_entry(model);
    J3DModel_viewCalc(model);
    VIS_WORD(WWVIS_STAT_EQUIP_OFF)++;
}

// --- scabbard (phase PRE / POST) --------------------------------------------
// CL_PODA's shape flags on the shared cl.bdl data: Link shows it with the
// hero's sword only (:1869-1879). saved_poda: -1 untouched, else the
// hide bit we found.
static int vis_poda_saved = -1;

static volatile u32* vis_poda_flags(J3DModel* model) {
    u8* data = *(u8**)((u8*)model + J3DMODEL_DATA_OFF);
    u8** joints = *(u8***)(data + J3DMODELDATA_JOINT_NODE_PTR_OFFSET);
    if (!vis_in_mem1((u32)joints) || !joints[CL_JNT_PODA]) return 0;
    u32 mesh = *(volatile u32*)(joints[CL_JNT_PODA] + J3DJOINT_MESH_OFFSET);
    if (!vis_in_mem1(mesh)) return 0;
    u32 shape = *(volatile u32*)(mesh + J3DMATERIAL_SHAPE_OFFSET);
    if (!vis_in_mem1(shape)) return 0;
    return (volatile u32*)(shape + J3DSHAPE_FLAGS_OFFSET);
}

static void vis_equip_pre(int slot, J3DModel* model) {
    vis_poda_saved = -1;
    if (!vis_on(WWVIS_FLAG_EQUIP)) return;
    u32 eq = WWVIS_EQUIP_OFF + (u32)slot * 4;
    if (!(VIS_BYTE(eq + WWVIS_EQ_FLAGS) & WWVIS_EQF_VALID)) return;
    volatile u32* flags = vis_poda_flags(model);
    if (!flags) return;
    vis_poda_saved = (*flags & J3DSHAPE_FLAG_HIDE) ? 1 : 0;
    if (VIS_BYTE(eq + WWVIS_EQ_SWORD) == ITEM_SWORD) *flags &= ~J3DSHAPE_FLAG_HIDE;
    else                                             *flags |= J3DSHAPE_FLAG_HIDE;
}

static void vis_equip_post(int slot, J3DModel* model, void* link) {
    if (vis_poda_saved >= 0) {
        volatile u32* flags = vis_poda_flags(model);
        if (flags) {
            if (vis_poda_saved) *flags |= J3DSHAPE_FLAG_HIDE;
            else                *flags &= ~J3DSHAPE_FLAG_HIDE;
        }
        vis_poda_saved = -1;
    }
    if (!vis_on(WWVIS_FLAG_EQUIP)) return;
    u32 eq = WWVIS_EQUIP_OFF + (u32)slot * 4;
    u8 ef = VIS_BYTE(eq + WWVIS_EQ_FLAGS);
    if (!(ef & WWVIS_EQF_VALID)) return;
    u8 sword = VIS_BYTE(eq + WWVIS_EQ_SWORD);
    u8 shield = VIS_BYTE(eq + WWVIS_EQ_SHIELD);
    f32* lhand = vis_joint(model, CL_JNT_LHANDA);
    f32* rhand = vis_joint(model, CL_JNT_RHANDA);
    f32* poda = vis_joint(model, CL_JNT_PODA);
    if (!lhand || !rhand || !poda) return;

    // j3dSys around everything below (calc / calcMaterial / entry write it).
    static u32 snap[J3D_SYS_SIZE / 4];
    volatile u32* sys = (volatile u32*)J3D_SYS_ADDR;
    int k;
    for (k = 0; k < (int)(J3D_SYS_SIZE / 4); k++) snap[k] = sys[k];

    f32 m[12];
    int grip = -1, blade = -1;
    if (sword == ITEM_SWORD) {
        grip = WWVIS_MK_GRIP_A;
        blade = WWVIS_MK_BLADE_A;
    } else if (sword == ITEM_MASTER_1 || sword == ITEM_MASTER_2 || sword == ITEM_MASTER_3) {
        grip = WWVIS_MK_GRIP_MS;
        blade = WWVIS_MK_BLADE_MS;
    }
    if (grip >= 0) {
        J3DModel* g = vis_model(slot, grip);
        if (ef & WWVIS_EQF_SWORD_HAND) {
            J3DModel* b = vis_model(slot, blade);
            if (g) vis_enter(g, lhand, link);
            if (b) vis_enter(b, lhand, link);
        } else if (g) {
            // ZrotM(-0x1C71): -0x1C71 / 0x8000 * 180 = -39.996 degrees.
            vis_mtx_place(m, poda, 0.76606f, -0.64280f, -11.25f, 4.5f, 0.45f);
            vis_enter(g, m, link);
        }
    }
    if (shield == ITEM_SHIELD) {
        J3DModel* s = vis_model(slot, WWVIS_MK_SHIELD_A);
        if (s) {
            if (ef & WWVIS_EQF_SHIELD_HAND) {
                vis_enter(s, rhand, link);
            } else {
                // ZrotM(-0x4000): -90 degrees.
                vis_mtx_place(m, poda, 0.0f, -1.0f, 15.5f, 4.75f, 0.0f);
                vis_enter(s, m, link);
            }
        }
    }

    for (k = 0; k < (int)(J3D_SYS_SIZE / 4); k++) sys[k] = snap[k];
}

// --- name tags (ww-online review idea 6) -----------------------------------
// Adapted from datstarkey/ww-online GameMod/src/puppet_link/puppet_nametag.c
// (MIT, notice at the end of this file): the pattern the game's own
// world-anchored 2D uses (dMeter_zakoEnemyMove's HP bar, d_meter.cpp:3704).
// A point above the remote's head is projected with mDoLib_project
// (640x480 2D-port coordinates) and a per-slot dDlst_base_c packet is
// entered into the 2D opaque list (dComIfGd_set2DOpa, the HUD's list); the
// painter draws that list with its J2DOrthoGraph as the current graf port
// and calls the packet's draw, which prints the name with the message font
// (mDoExt_font0) as JUTDbPrint::flush does. Changed for us: the anchor is
// the remote's streamed root joint (a mini-Link's base matrix is not the
// remote's position), the names come from WWVIS_NAMES, and the gate is
// WWVIS_FLAG_NAMES. Lifetime: the painter runs at the start of the next
// frame, before process deletes (fpcM_Management: cAPIGph_Painter, then
// fpcDt_Handler, f_pc_manager.cpp:277-280), so this module (and the
// packet in its .bss) is still linked when it is drawn.
typedef void (*vis_vec2_t)(cXyz* src, cXyz* dst);
typedef int (*vis_dlst_set_t)(void* list, void*** top, void*** end, void* item);
typedef void (*vis_port_t)(void* graf);
typedef f32 (*vis_drawstr_t)(void* font, f32 x, f32 y, f32 w, f32 h, const char* str, u32 len, int visible);
typedef int (*vis_font_int_t)(void* font);
typedef void (*vis_font_void_t)(void* font);
typedef void (*vis_font_width_t)(void* font, int code, u8* width);
#define mDoLib_project          ((vis_vec2_t)0x80017748)    // mDoLib_project__FP3VecP3Vec
#define mDoLib_pos2camera       ((vis_vec2_t)0x80017924)    // mDoLib_pos2camera__FP3VecP3Vec
#define dDlst_list_set          ((vis_dlst_set_t)0x80086540) // set__12dDlst_list_cFRPP12dDlst_base_cRPP12dDlst_base_cP12dDlst_base_c
#define J2DOrthoGraph_setPort   ((vis_port_t)0x802CDCB4)    // setPort__13J2DOrthoGraphFv
#define JUTFont_drawString_size_scale ((vis_drawstr_t)0x802C1F24) // drawString_size_scale__7JUTFontFffffPCcUlb
#define MDOEXT_FONT0            0x803F6944                   // JUTFont* mDoExt_font0
#define DMENU_PAUSE             0x803F7097                   // u8 dMenu_pause
#define GAMEINFO                0x803C4C08                   // g_dComIfG_gameInfo
#define GI_2D_SHOW              (GAMEINFO + 0x5C19)          // play (+0x12A0) .m2dShow (+0x4979)
#define GI_EVT_MODE             (GAMEINFO + 0x529A)          // play .mEvtCtrl (+0x3F38) .mMode (+0xC2)
#define GI_PLAYER_STATUS0       (GAMEINFO + 0x5CC8)          // play .mPlayerStatus[0][0] (+0x4A28)
#define GI_PLAYER_STATUS1       (GAMEINFO + 0x5CCC)          // [0][1]
#define GI_GRAF_PORT            (GAMEINFO + 0x5D00)          // play .mCurrentGrafPort (+0x4A60)
#define GI_DRAWLIST             (GAMEINFO + 0x5D1C)          // drawlist (dDlst_list_c)
#define DL_2DOPA                0x19C                        // dDlst_list_c::mp2DOpa
#define DL_2DOPA_END            0x1A0                        // mp2DOpaEnd
#define DL_VIEWPORT             0x230                        // mpViewPort
#define DL_VIEW                 0x234                        // mpView
#define STTS0_TELESCOPE_LOOK    0x00200000                   // daPyStts0_TELESCOPE_LOOK_e
#define STTS1_PICTO_BOX_AIM     0x00000008                   // daPyStts1_PICTO_BOX_AIM_e
#define JUTFONT_VALID           0x04                         // JUTFont::mValid
#define JUTFONT_COLORS          0x0C                         // mColor1..4
#define JUTFONT_VT_SET_GX       (0x0C / 4)
#define JUTFONT_VT_WIDTH_ENTRY  (0x2C / 4)
#define JUTFONT_VT_CELL_W       (0x30 / 4)
#define JUTFONT_VT_CELL_H       (0x34 / 4)

#define TAG_HEAD_Y       135.0f   // above the root: Link's attention point (92.5) plus his cap
#define TAG_BASELINE_UP  4.0f
#define TAG_DEPTH_MIN    60.0f
#define TAG_DEPTH_NEAR   400.0f
#define TAG_DEPTH_SMALL  4000.0f
#define TAG_DEPTH_FADE   4500.0f
#define TAG_DEPTH_MAX    6000.0f
#define TAG_PX_NEAR      22.0f
#define TAG_PX_FAR       13.0f
#define TAG_MARGIN       80.0f
#define TAG_SHADOW       1.5f

typedef struct {
    const void* vtbl;          // dDlst_base_c: dDlst_list_c::draw calls vtable[3]
    f32 x, y, px;
    u32 alpha;
    char name[WWVIS_NAME_BYTES];
} VisTag;

void vis_tag_draw(VisTag* tag);
void vis_tag_noop(VisTag* tag);
void vis_tag_noop(VisTag* tag) { (void)tag; }
// CodeWarrior vtable: {RTTI, this-offset, dtor, draw} (__vt__12dDlst_base_c 0x80375900, 0x10 bytes).
static const void* const vis_tag_vtbl[4] = {0, 0, (const void*)vis_tag_noop, (const void*)vis_tag_draw};
static VisTag vis_tags[WWVIS_SLOTS];

// What dMeter_statusCheck hides the HUD for (d_meter.cpp:746-824): 2D
// off, an event, the pause menu, the telescope or Picto Box view.
static int vis_hud_visible(void) {
    if (*(volatile u8*)GI_2D_SHOW == 0 || *(volatile u8*)GI_EVT_MODE != 0) return 0;
    if (*(volatile u8*)DMENU_PAUSE != 0) return 0;
    if ((*(volatile u32*)GI_PLAYER_STATUS0 & STTS0_TELESCOPE_LOOK) ||
        (*(volatile u32*)GI_PLAYER_STATUS1 & STTS1_PICTO_BOX_AIM)) return 0;
    return 1;
}

static void vis_tag_queue(int slot, J3DModel* model) {
    if (!vis_on(WWVIS_FLAG_NAMES) || !vis_hud_visible()) return;
    VisTag* tag = &vis_tags[slot];
    int n = 0, i;
    for (i = 0; i < WWVIS_NAME_BYTES - 1; i++) {
        char c = (char)VIS_BYTE(WWVIS_NAMES_OFF + (u32)slot * WWVIS_NAME_BYTES + (u32)i);
        if (c == 0) break;
        if (c >= 0x20 && c <= 0x7E) tag->name[n++] = c;
    }
    tag->name[n] = 0;
    if (n == 0) return;

    u8* dl = (u8*)GI_DRAWLIST;
    if (!*(volatile u32*)(dl + DL_VIEW) || !*(volatile u32*)(dl + DL_VIEWPORT)) return;
    f32* root = vis_joint(model, 0);
    if (!root) return;
    cXyz anchor, cam, screen;
    anchor.x = root[3];
    anchor.y = root[7] + TAG_HEAD_Y;
    anchor.z = root[11];

    mDoLib_pos2camera(&anchor, &cam);
    f32 depth = -cam.z;
    if (!(depth > TAG_DEPTH_MIN && depth < TAG_DEPTH_MAX)) return;
    mDoLib_project(&anchor, &screen);
    if (screen.x < -TAG_MARGIN || screen.x > 640.0f + TAG_MARGIN ||
        screen.y < -TAG_MARGIN || screen.y > 480.0f + TAG_MARGIN) return;

    f32 t = (depth - TAG_DEPTH_NEAR) / (TAG_DEPTH_SMALL - TAG_DEPTH_NEAR);
    t = t < 0.0f ? 0.0f : (t > 1.0f ? 1.0f : t);
    f32 alpha = 255.0f;
    if (depth > TAG_DEPTH_FADE) alpha = 255.0f * (TAG_DEPTH_MAX - depth) / (TAG_DEPTH_MAX - TAG_DEPTH_FADE);
    tag->vtbl = vis_tag_vtbl;
    tag->x = screen.x;
    tag->y = screen.y - TAG_BASELINE_UP;
    tag->px = TAG_PX_NEAR + (TAG_PX_FAR - TAG_PX_NEAR) * t;
    tag->alpha = (u32)alpha;
    if (tag->alpha == 0) return;
    // false (ignored) when the list is full
    dDlst_list_set(dl, (void***)(dl + DL_2DOPA), (void***)(dl + DL_2DOPA_END), tag);
    VIS_WORD(WWVIS_STAT_TAGS_OFF)++;
}

static void vis_font_color(u8* font, u32 rgba) {
    u32* c = (u32*)(font + JUTFONT_COLORS);
    c[0] = c[1] = c[2] = c[3] = rgba;
}

// dDlst_base_c::draw, in the painter's 2D pass.
void vis_tag_draw(VisTag* tag) {
    u8* font = *(u8**)MDOEXT_FONT0;
    void* graf = *(void**)GI_GRAF_PORT;
    if (!vis_in_mem1((u32)font) || !vis_in_mem1((u32)graf) || !font[JUTFONT_VALID]) {
        vis_err(WWVIS_ERR_FONT);
        return;
    }
    void** vt = *(void***)font;
    if (!vis_in_mem1((u32)vt)) return;
    int cw = ((vis_font_int_t)vt[JUTFONT_VT_CELL_W])(font);
    int ch = ((vis_font_int_t)vt[JUTFONT_VT_CELL_H])(font);
    if (cw <= 0 || ch <= 0) return;
    // drawChar_scale advances a glyph by TWidth.width * (scaleX / cellWidth) (JUTResFont.cpp:244-251).
    f32 scale = tag->px / (f32)ch;
    f32 width = 0.0f;
    u32 len;
    for (len = 0; tag->name[len] != 0; len++) {
        u8 w[2];
        ((vis_font_width_t)vt[JUTFONT_VT_WIDTH_ENTRY])(font, (u8)tag->name[len], w);
        width += (f32)w[1] * scale;
    }
    f32 x = tag->x - width * 0.5f;
    f32 gw = (f32)cw * scale;
    f32 gh = (f32)ch * scale;

    J2DOrthoGraph_setPort(graf);
    ((vis_font_void_t)vt[JUTFONT_VT_SET_GX])(font);
    u32 saved[4];
    int i;
    for (i = 0; i < 4; i++) saved[i] = ((u32*)(font + JUTFONT_COLORS))[i];
    vis_font_color(font, (tag->alpha * 3) / 4);            // black shadow
    JUTFont_drawString_size_scale(font, x + TAG_SHADOW, tag->y + TAG_SHADOW, gw, gh, tag->name, len, 1);
    vis_font_color(font, 0xFFFFFF00u | tag->alpha);         // white
    JUTFont_drawString_size_scale(font, x, tag->y, gw, gh, tag->name, len, 1);
    for (i = 0; i < 4; i++) ((u32*)(font + JUTFONT_COLORS))[i] = saved[i];
    // drawChar_scale leaves POS as F32 (JUTResFont.cpp:264): give the next 2D drawer the port's state.
    J2DOrthoGraph_setPort(graf);
}

// --- sea chart markers (ww-online review idea 14) ----------------------------
// Adapted from datstarkey/ww-online GameMod/src/puppet_link/
// puppet_seachart.c and GameMod/src/patches/seachart_players.asm (MIT,
// notice at the end of this file). While the pause menu's sea chart
// (dMenu_Fmap_c, d_menu_fmap.cpp) is up no actor runs, so T2's
// seachart_shim (src/core/hooks.c) calls this through WWVIS_SEACHART_FN
// from dDlst_FMAP_c::draw (0x801BB024) right after the chart's J2DScreen
// has drawn, with the menu's fmapDl and the graf port it drew with. Link's
// own marker picture is redrawn once per remote on the sea (WWVIS_SEA,
// from Go), moved by the remote's offset from Link, turned to its heading
// and tinted its tunic colour; every byte of the pane we touch is put
// back. Changed for us: the entries are Go's (wwvis.h), the pane must be
// a J2DPicture (vtable check) before its colours are written, and the
// whole pane state J2DPane::draw and calcMtx write (0x0C..0xB0) is
// snapshotted and restored, not only the fields we set.
typedef void (*vis_pane_fn_t)(void* pane);
typedef void (*vis_pane_draw_t)(void* pane, f32 x, f32 y, void* graf, int clip);
#define J2DPane_calcMtx   ((vis_pane_fn_t)0x8002D088)    // calcMtx__7J2DPaneFv
#define J2DPane_draw      ((vis_pane_draw_t)0x802D0078)  // draw__7J2DPaneFffPC14J2DGrafContextb
#define J2DPICTURE_VTBL   0x8039D730                     // __vt__10J2DPicture

// dMenu_Fmap_c (d_menu_fmap.h; offsets from the decomp's member comments).
#define FMAP_OFF_DL        0x001C   // dDlst_FMAP_c fmapDl: dDlst_FMAP_c::draw's this
#define FMAP_SV            0x2878   // dMenu_FmapSv_c* fmapSv
#define FMAP_LNK1_PANE     0x2E04   // mLnk1Pane.pane: Link on the world map
#define FMAP_LNK2_PANE     0x2EE4   // mLnk2Pane.pane: Link on a zoomed square
#define FMAP_LNK2_SIZE_X   0x2F08   // mLnk2Pane.mSizeOrig (fopMsgM_pane_class +0x24)
#define FMAP_LNK2_SIZE_Y   0x2F0C
#define FMAP_CLB_PANE      0x30A4   // mClbPane.pane: the zoomed square (lnk2's parent)
#define FMAP_CLB_SIZE_X    0x30C8   // mClbPane.mSizeOrig
#define FMAP_CLB_SIZE_Y    0x30CC
#define FMAP_PROC_IDX      0x5114   // u8 mFmapProcIdx (ZoomProcIdx, d_menu_fmap.cpp:271)
#define FMAP_PLAYER_X      0x511C   // mPlayerPos.x = Link's current.pos.x (:623)
#define FMAP_PLAYER_Z      0x5120   // mPlayerPos.y = Link's current.pos.z (:624)
#define FMAP_ON_SEA        0x517F   // bool mFishmanActive: opened on the "sea" stage (:637-638)
#define FMAPSV_CUR_X       0x04     // s8 dMenu_FmapSv_c::curX (d_menu_fmapSv.h)
#define FMAPSV_CUR_Y       0x05
#define FMAP_PROC_SELECT_GRID 0     // FMAP_ZOOM_PROC_SELECT_GRID: the world map, steady
#define FMAP_PROC_LV1         2     // FMAP_ZOOM_PROC_LV1: a zoomed square, steady
#define FMAP_GRID_MAX      3        // squares -3..3 (dMap_point2Grid)
#define FMAP_NORMAL_SCALE  (56.0f / 100000.0f)  // setDspNormalMapLink (:1529-1530)
#define FMAP_LARGE_MARGIN  50.0f                // setDspLargeMapLink (:1566-1569)
#define FMAP_SQUARE        100000.0f
#define SEA_MAX_COORD      400000.0f

// J2DPane / J2DPicture (J2DPane.h:106-123, J2DPicture.h:144-145).
#define PANE_BOUNDS        0x0C     // TBox2<f32> mBounds
#define PANE_BASE_POS      0x9C     // TVec2<f32> mBasePosition (the rotation pivot)
#define PANE_ROTATION      0xA4     // f32 mRotation (degrees)
#define PANE_VISIBLE       0xAA     // bool mVisible
#define PANE_PARENT_TREE   0xC0     // mPaneTree (+0xB0) link's mList: the parent's mPaneTree
#define TREE_PANE          0x0C     // a JSUTree's link mObject: its pane
#define PANE_SAVE_START    0x0C     // mBounds .. mPaneTree: what draw / calcMtx write
#define PANE_SAVE_END      0xB0
#define PICTURE_WHITE      0x104    // TColor mColorWhite
#define PICTURE_BLACK      0x108    // TColor mColorBlack
#define ANY_SQUARE         0x7F

typedef struct {
    f32 origin_x, origin_z, base_x, base_y, scale_x, scale_y, half_w, half_h;
    int square_x, square_y;         // only the remotes on this square, unless square_x is ANY_SQUARE
} VisSeaPlace;

// The pane and every parent visible (J2DPane::draw stops at a hidden pane).
static int vis_pane_shown(u8* pane) {
    int depth;
    for (depth = 0; pane && depth < 16; depth++) {
        if (!vis_in_mem1((u32)pane) || !pane[PANE_VISIBLE]) return 0;
        u8* tree = *(u8**)(pane + PANE_PARENT_TREE);
        pane = (tree && vis_in_mem1((u32)tree)) ? *(u8**)(tree + TREE_PANE) : 0;
    }
    return 1;
}

// floor(v / 100000 + 0.5), unclamped (dMap_point2Grid's square).
static int vis_sea_grid(f32 v) {
    f32 s = v / FMAP_SQUARE + 0.5f;
    int g = (int)s;
    if ((f32)g > s) g--;
    return g;
}

static void vis_sea_draw_on(u8* pane, const VisSeaPlace* pl, void* graf) {
    u8 saved[PANE_SAVE_END - PANE_SAVE_START];
    u8 white[4], black[4];
    f32* b = (f32*)(pane + PANE_BOUNDS);
    f32* pivot = (f32*)(pane + PANE_BASE_POS);
    int i, j;
    for (j = 0; j < (int)sizeof(saved); j++) saved[j] = pane[PANE_SAVE_START + j];
    for (j = 0; j < 4; j++) {
        white[j] = pane[PICTURE_WHITE + j];
        black[j] = pane[PICTURE_BLACK + j];
    }
    // Turned about its centre, as the chart's rotate(mSize * 0.5) does.
    pivot[0] = pl->half_w;
    pivot[1] = pl->half_h;
    pane[PANE_VISIBLE] = 1;          // lnk2 is hidden off Link's own square
    for (i = 0; i < WWVIS_SEA_MAX; i++) {
        u32 e = WWVIS_SEA_OFF + (u32)i * WWVIS_SEA_SIZE;
        if (!(VIS_BYTE(e + WWVIS_SE_FLAGS) & WWVIS_SEF_SHOW)) continue;
        union { u32 u; f32 f; } xv, zv;
        xv.u = VIS_WORD(e + WWVIS_SE_X);
        zv.u = VIS_WORD(e + WWVIS_SE_Z);
        f32 x = xv.f, z = zv.f;
        if (!(x == x) || !(z == z) || x < -SEA_MAX_COORD || x > SEA_MAX_COORD ||
            z < -SEA_MAX_COORD || z > SEA_MAX_COORD) continue;
        if (pl->square_x != ANY_SQUARE && (vis_sea_grid(x) != pl->square_x || vis_sea_grid(z) != pl->square_y)) continue;
        f32 cx = pl->base_x + (x - pl->origin_x) * pl->scale_x;
        f32 cy = pl->base_y + (z - pl->origin_z) * pl->scale_y;
        b[0] = cx - pl->half_w;
        b[1] = cy - pl->half_h;
        b[2] = cx + pl->half_w;
        b[3] = cy + pl->half_h;
        // As the menu turns Link's: (s16)(shape_angle.y + 0x8000) * 180 / 32768 degrees (:625).
        s16 ang = *(volatile s16*)(MAILBOX_ADDR + WWVIS_OFF + e + WWVIS_SE_ANGLE);
        *(f32*)(pane + PANE_ROTATION) = (f32)(s16)(ang + 0x8000) * 180.0f / 32768.0f;
        J2DPane_calcMtx(pane);
        u8 r = VIS_BYTE(e + WWVIS_SE_R), g = VIS_BYTE(e + WWVIS_SE_G), bl = VIS_BYTE(e + WWVIS_SE_B);
        pane[PICTURE_WHITE + 0] = r;
        pane[PICTURE_WHITE + 1] = g;
        pane[PICTURE_WHITE + 2] = bl;
        pane[PICTURE_BLACK + 0] = r >> 2;
        pane[PICTURE_BLACK + 1] = g >> 2;
        pane[PICTURE_BLACK + 2] = bl >> 2;
        J2DPane_draw(pane, 0.0f, 0.0f, graf, 0);
        VIS_WORD(WWVIS_STAT_SEA_OFF)++;
    }
    for (j = 0; j < (int)sizeof(saved); j++) pane[PANE_SAVE_START + j] = saved[j];
    for (j = 0; j < 4; j++) {
        pane[PICTURE_WHITE + j] = white[j];
        pane[PICTURE_BLACK + j] = black[j];
    }
}

static int vis_is_picture(u8* pane) {
    return pane && vis_in_mem1((u32)pane) && *(volatile u32*)pane == J2DPICTURE_VTBL;
}

void wwvis_seachart_draw(u8* fmap_dl, void* graf) {
    if (!vis_on(WWVIS_FLAG_SEACHART) || !vis_in_mem1((u32)fmap_dl) || !vis_in_mem1((u32)graf)) return;
    u8* menu = fmap_dl - FMAP_OFF_DL;
    // Link's marker only shows on the sea stage (setDspNormalMapLink), and ours go with it.
    if (!menu[FMAP_ON_SEA]) return;
    u8 proc = menu[FMAP_PROC_IDX];
    VisSeaPlace pl;

    // The world map: Link at the area pane's centre + pos * 56 / 100000,
    // so the others are placed from his marker.
    u8* pane = *(u8**)(menu + FMAP_LNK1_PANE);
    if (proc == FMAP_PROC_SELECT_GRID) {
        if (!vis_is_picture(pane) || !vis_pane_shown(pane)) return;
        f32* b = (f32*)(pane + PANE_BOUNDS);
        pl.origin_x = *(f32*)(menu + FMAP_PLAYER_X);
        pl.origin_z = *(f32*)(menu + FMAP_PLAYER_Z);
        pl.base_x = (b[0] + b[2]) * 0.5f;
        pl.base_y = (b[1] + b[3]) * 0.5f;
        pl.scale_x = pl.scale_y = FMAP_NORMAL_SCALE;
        pl.half_w = (b[2] - b[0]) * 0.5f;
        pl.half_h = (b[3] - b[1]) * 0.5f;
        pl.square_x = ANY_SQUARE;
        pl.square_y = 0;
        vis_sea_draw_on(pane, &pl, graf);
        return;
    }

    // A zoomed square, steady: lnk2 is Link only on his own square, so
    // the others are placed from the square itself, as setDspLargeMapLink
    // places Link: size / 2 + (pos - the square's centre) * (size - 50) /
    // 100000. Only that square's remotes.
    pane = *(u8**)(menu + FMAP_LNK2_PANE);
    u8* clb = *(u8**)(menu + FMAP_CLB_PANE);
    u8* sv = *(u8**)(menu + FMAP_SV);
    if (proc != FMAP_PROC_LV1 || !vis_is_picture(pane) || !vis_in_mem1((u32)clb) || !vis_in_mem1((u32)sv) ||
        !vis_pane_shown(clb)) return;
    pl.square_x = (s8)sv[FMAPSV_CUR_X];
    pl.square_y = (s8)sv[FMAPSV_CUR_Y];
    if (pl.square_x < -FMAP_GRID_MAX || pl.square_x > FMAP_GRID_MAX ||
        pl.square_y < -FMAP_GRID_MAX || pl.square_y > FMAP_GRID_MAX) return;
    f32 sx = *(f32*)(menu + FMAP_CLB_SIZE_X), sy = *(f32*)(menu + FMAP_CLB_SIZE_Y);
    pl.origin_x = FMAP_SQUARE * (f32)pl.square_x;
    pl.origin_z = FMAP_SQUARE * (f32)pl.square_y;
    pl.base_x = sx * 0.5f;
    pl.base_y = sy * 0.5f;
    pl.scale_x = (sx - FMAP_LARGE_MARGIN) / FMAP_SQUARE;
    pl.scale_y = (sy - FMAP_LARGE_MARGIN) / FMAP_SQUARE;
    pl.half_w = *(f32*)(menu + FMAP_LNK2_SIZE_X) * 0.5f;
    pl.half_h = *(f32*)(menu + FMAP_LNK2_SIZE_Y) * 0.5f;
    vis_sea_draw_on(pane, &pl, graf);
}

// --- the hook T2 calls ------------------------------------------------------
void wwvis_draw_hook(int slot, J3DModel* model, void* link, int phase) {
    if (slot < 0 || slot >= WWVIS_SLOTS || !model || !link) return;
    if (phase == WWVIS_PHASE_PRE) {
        vis_equip_pre(slot, model);
        return;
    }
    vis_equip_post(slot, model, link);
    vis_tag_queue(slot, model);
}

void wwvis_link(void) {
    VIS_WORD(WWVIS_DRAW_FN_OFF) = (u32)wwvis_draw_hook;
    VIS_WORD(WWVIS_SEACHART_FN_OFF) = (u32)wwvis_seachart_draw;
}

void wwvis_unlink(void) {
    VIS_WORD(WWVIS_SEACHART_FN_OFF) = 0;
    VIS_WORD(WWVIS_DRAW_FN_OFF) = 0;
}

// The name tags (vis_tag_*) and the sea chart markers (vis_sea_*,
// wwvis_seachart_draw) are adapted from datstarkey/ww-online
// GameMod/src/puppet_link/puppet_nametag.c and puppet_seachart.c, under
// this notice:
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
