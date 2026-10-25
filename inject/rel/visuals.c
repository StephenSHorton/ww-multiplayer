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

// --- the hook T2 calls ------------------------------------------------------
void wwvis_draw_hook(int slot, J3DModel* model, void* link, int phase) {
    if (slot < 0 || slot >= WWVIS_SLOTS || !model || !link) return;
    if (phase == WWVIS_PHASE_PRE) {
        vis_equip_pre(slot, model);
        return;
    }
    vis_equip_post(slot, model, link);
}

void wwvis_link(void) {
    VIS_WORD(WWVIS_DRAW_FN_OFF) = (u32)wwvis_draw_hook;
}

void wwvis_unlink(void) {
    VIS_WORD(WWVIS_DRAW_FN_OFF) = 0;
}
