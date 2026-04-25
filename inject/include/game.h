// Wind Waker GZLE01 function addresses and types
#ifndef GAME_H
#define GAME_H

typedef unsigned int u32;
typedef unsigned short u16;
typedef unsigned char u8;
typedef signed int s32;
typedef signed short s16;
typedef signed char s8;
typedef float f32;

// Position vector (12 bytes)
typedef struct {
    f32 x, y, z;
} cXyz;

// Rotation vector (6 bytes)
typedef struct {
    s16 x, y, z;
} csXyz;

// Actor base class (opaque — we just use it as a pointer)
typedef void fopAc_ac_c;

// Function pointer types
typedef void* createFunc;
typedef u32 fpc_ProcID;

// fopAcM_create — QUEUES a spawn request. Actor is constructed later, inside
// fpcM_Management's proper heap-bracketed flow. Returns fpc_ProcID (u32); use
// fopAcM_SearchByID to resolve to a pointer once construction completes.
//
// We prefer this over fopAcM_fastCreate because fastCreate runs fpcBs_SubCreate
// SYNCHRONOUSLY inside our call. That synchronous construction path trips
// `mDoExt_restoreCurrentHeap: mDoExt_SaveCurrentHeap != NULL` when invoked
// mid-frame from our shim (heap state is no longer NULL-balanced at the
// post-fpcM_Management point where frame_shim runs).
//
// 8 args (not 9 like fastCreate — no createFuncData).
// Address: 0x8002451C (per TWW decomp GZLE01 symbol map)
#define fpcM_ERROR_PROCESS_ID_e 0xFFFFFFFFU

typedef fpc_ProcID (*fopAcM_create_t)(
    s16 procName,
    u32 parameter,
    cXyz* pos,
    int roomNo,
    csXyz* angle,
    cXyz* scale,
    s8 argument,
    createFunc create
);

#define fopAcM_create ((fopAcM_create_t)0x8002451C)

// Resolve a queued-spawn pid to an actor pointer. 2-arg form at
// 0x800241C0 per symbols.txt (`fopAcM_SearchByID__FUiPP10fopAc_ac_c`):
//   BOOL fopAcM_SearchByID(fpc_ProcID id, fopAc_ac_c** out)
// Returns TRUE and writes the actor ptr via out; FALSE if the pid is
// unknown or the actor has been deleted.
typedef int BOOL;
typedef BOOL (*fopAcM_SearchByID_t)(fpc_ProcID id, fopAc_ac_c** out);
#define fopAcM_SearchByID ((fopAcM_SearchByID_t)0x800241C0)

#define PROC_PLAYER     0x00A9
#define PROC_KAMOME     0x00C3
#define PROC_NPC_KO1    0x0141
#define PROC_NPC_OB1    0x014D  // Rose (Outset villager)
#define PROC_NPC_FA1    0x016A
#define PROC_GRASS      0x01B8
#define PROC_TSUBO      0x01CB
#define PROC_Obj_Barrel 0x01CE

// Player pointer array: g_dComIfG_gameInfo + 0x12A0 + 0x48AC
// mpPlayerPtr[0] = Link, [1] = companion, [2] = ship
#define PLAYER_PTR_ARRAY ((fopAc_ac_c**)0x803CA754)

// daPy_lk_c offsets (zeldaret/tww include/d/actor/d_a_player_main.h):
//   +0x0328 = J3DModelData* mpCLModelData
//   +0x032C = J3DModel*     mpCLModel
// Used by the save-reload defensive re-fetch in daPy_draw_hook /
// multiplayer_update: the game tears down + rebuilds Link's J3DModel
// (and its J3DModelData) when reloading a save, so our cached
// mini_link_data must be compared against the live mpCLModelData each
// frame. Mismatch ⇒ drop our state and re-init.
#define DAPY_LK_C_MPCLMODELDATA_OFFSET 0x0328
#define DAPY_LK_C_MPCLMODEL_OFFSET     0x032C

// Actor position offset from actor base
#define ACTOR_POS_OFFSET   0x1F8
#define ACTOR_ANGLE_OFFSET 0x204
#define ACTOR_SHAPE_OFFSET 0x20C
#define ACTOR_ROOM_OFFSET  0x20A

// Get position pointer from actor
#define ACTOR_POS(actor)   ((cXyz*)((u8*)(actor) + ACTOR_POS_OFFSET))
#define ACTOR_ANGLE(actor) ((csXyz*)((u8*)(actor) + ACTOR_ANGLE_OFFSET))
#define ACTOR_SHAPE(actor) ((csXyz*)((u8*)(actor) + ACTOR_SHAPE_OFFSET))
#define ACTOR_ROOM(actor)  (*((s8*)((u8*)(actor) + ACTOR_ROOM_OFFSET)))

// --- Mini-Link rendering path (Option B, roadmap 06) --------------------

// Dolphin Mtx = f32[3][4], 48 bytes, row-major. Rows 0..2, cols 0..3
// (m03/m13/m23 = translation). J3DModel::mBaseTransformMtx lives at
// offset 0x24.
typedef f32 Mtx[3][4];
#define J3DMODEL_BASE_TR_MTX_OFFSET 0x24
// J3DModel::mUserArea (u32) at offset 0x14 — actors stash their
// `this` pointer here so joint callbacks bound to the J3DModelData
// can recover the owning actor instance. Link's joint callbacks
// crash with r3=NULL at PC 0x8010C53C if our mini-Link model leaves
// mUserArea = 0. See zeldaret/tww J3DModel.h and the dozens of
// `model->setUserArea((u32)this)` call sites in d_a_*.cpp.
#define J3DMODEL_USER_AREA_OFFSET   0x14

// J3DModelData::mJointTree.mBasicMtxCalc (J3DMtxCalc*). The *pose* walker
// lives HERE, not in mUserArea. J3DModel::calc → calcAnmMtx reads
// `getModelData()->getBasicMtxCalc()` and runs `recursiveCalc(rootJoint)`
// through it. Since our mini-Link and Link #1 share the same J3DModelData,
// whatever animated controller Link #1 installs drives *both* skeletons.
// Shadowing the daPy_lk_c only controls the peripheral fields read via
// mUserArea (equip/anim status), not the joint transforms.
//
// Layout from zeldaret/tww J3DModelData.h + J3DJointTree.h:
//   J3DModelData + 0x10 = J3DJointTree (inlined)
//   J3DJointTree  + 0x14 = J3DMtxCalc* mBasicMtxCalc
//   J3DJointTree  + 0x18 = u16 mJointNum
#define J3DMODELDATA_BASIC_MTXCALC_OFFSET 0x24
#define J3DMODELDATA_JOINT_NUM_OFFSET     0x28

// J3DModel per-instance bone buffers. calc() walks the skeleton through
// basicMtxCalc and writes joint world-space matrices into mpNodeMtx[0..N-1]
// (Mtx = f32[3][4] = 48 bytes). A subsequent viewCalc step inside
// modelEntryDL projects these into mpDrawMtxBuf which GX uploads. Thus
// overwriting mpNodeMtx between calc() and modelEntryDL propagates through
// to the GPU — this is the hook for driving Link #2's pose independently.
#define J3DMODEL_MP_NODE_MTX_OFFSET  0x8C
#define J3DMODEL_MP_DRAW_MTX_OFFSET  0x94

// Opaque — we never inspect the contents, only hold pointers.
typedef void J3DModel;
typedef void J3DModelData;

// dRes_control_c::getRes(const char* arcName, s32 resIdx, dRes_info_c* pInfo,
// int infoNum) — STATIC member function (no `this`!). The header declares it
// `static`; the mangled name has no this-adjust. Earlier I typed it as a
// member function and passed an extra leading pointer — which shifted every
// real arg by one register. The game then treated `this` as arcName,
// dereferenced it as bytes, and printed those bytes in the "res nothing"
// OSReport error. The bytes at &mObjectInfo[0] = "System", which explained
// the "<System.arc>" log flood.
//
// pInfo = &mObjectInfo[0] = 0x803E0BC8 (mObjectInfo sits at offset 0 inside
// dRes_control_c; the roadmap's "MRES_CONTROL address" was correct, but its
// usage was wrong). ARRAY_SIZE(mObjectInfo) = 64.
typedef void* (*dRes_getRes_byIdx_t)(
    const char* arcName,
    s32 resIdx,
    void* pInfo,
    int infoNum
);
#define dRes_getRes_byIdx ((dRes_getRes_byIdx_t)0x8006F208)
#define MOBJECT_INFO      ((void*)0x803E0BC8)
#define OBJECT_INFO_COUNT 64
#define LINK_BDL_CL       0x18

// "Always" archive constants (used as a non-shared model probe for the
// mini-Link modelEntryDL sky-breakage investigation). mpm_tubo is the
// small-pot BDL. Rigid, non-skinned — doesn't need J3DModel::calc().
#define ALWAYS_BDL_MPM_TUBO  0x31

typedef J3DModel* (*mDoExt_J3DModel_create_t)(
    J3DModelData* modelData,
    u32 modelFlag,
    u32 differedDlistFlag
);
#define mDoExt_J3DModel__create ((mDoExt_J3DModel_create_t)0x80016BB8)

typedef void (*mDoExt_modelEntryDL_t)(J3DModel* model);
#define mDoExt_modelEntryDL ((mDoExt_modelEntryDL_t)0x8000F974)

// --- Heap control -------------------------------------------------------
// `new J3DModel()` inside mDoExt_J3DModel__create allocates from the
// CURRENT heap. At our fapGm_Execute hook site, the current heap happens
// to be whatever the game last set (often ArchiveHeap during resource
// loads). Allocating ~5-10 KB there corrupts unrelated assets (observed:
// missing sky textures with huge gray patches). Fix: switch to ZeldaHeap
// — the same heap Link #1's own J3DModel lives in — around the create
// call, then restore.
typedef void JKRHeap;
typedef JKRHeap* (*mDoExt_getZeldaHeap_t)(void);
#define mDoExt_getZeldaHeap    ((mDoExt_getZeldaHeap_t)0x800118C0)
#define mDoExt_getGameHeap     ((mDoExt_getZeldaHeap_t)0x800117E4)
#define mDoExt_getArchiveHeap  ((mDoExt_getZeldaHeap_t)0x80011AB4)
// Non-static member: `heap->becomeCurrentHeap()` returns the previous
// current heap. `this` is r3; there are no other args.
typedef JKRHeap* (*JKRHeap_becomeCurrentHeap_t)(JKRHeap* self);
#define JKRHeap_becomeCurrentHeap ((JKRHeap_becomeCurrentHeap_t)0x802B03F8)

// STATIC allocator. void* JKRHeap::alloc(u32 size, int align, JKRHeap* heap).
// Passing NULL uses the current heap; we pass the heap explicitly so we
// don't depend on whatever heap happens to be current at call time.
// Symbol per zeldaret/tww GZLE01 symbols: alloc__7JKRHeapFUliP7JKRHeap.
typedef void* (*JKRHeap_alloc_t)(u32 size, int align, JKRHeap* heap);
#define JKRHeap_alloc ((JKRHeap_alloc_t)0x802B0434)

// --- Per-frame bone computation ----------------------------------------
// J3DModel::calc is virtual; our model is a plain J3DModel (no derived
// class), so calling the base implementation directly is equivalent to
// a vtable dispatch. calc() is required to propagate mBaseTransformMtx
// (offset 0x24) into mpNodeMtx / mpDrawMtxBuf (offsets 0x8C / 0x94).
// Without calc(), GX uploads uninitialized draw matrices and the model
// renders at origin with degenerate matrices (invisible).
//
// The catch: calc() writes j3dSys.mModel and j3dSys.mCurrentMtxCalc
// globals, which Link's post-draw code reads expecting its own pointers.
// See docs/05 "Mini-Link render pipeline" Blocker 2. Wrap calls with
// a save/restore of those two fields.
typedef void (*J3DModel_calc_t)(J3DModel* self);
#define J3DModel_calc ((J3DModel_calc_t)0x802EE8C0)

// j3dSys global + the fields polluted by J3DModel::calc().
// Layout per JSystem/J3DGraphBase/J3DSys.h:
//   0x030 mCurrentMtxCalc (J3DMtxCalc*)
//   0x038 mModel          (J3DModel*)
//   0x03C mMatPacket      (J3DMatPacket**)
//   0x040 mShapePacket    (J3DShapePacket**)
//   0x044 mShape          (J3DShape*)
// First two are sufficient for rigid models (Tsubo). Skinned models
// (Link) walk the skeleton via mCurrentMtxCalc and write the next
// three as well; without saving them, Link's post-draw checkEquipAnime
// dereferences mini-Link's pointers and crashes at PC 0x8010C53C.
#define J3D_SYS_ADDR                 0x803EDA58
#define J3D_SYS_M_CURRENT_MTX_CALC   ((void**)    (J3D_SYS_ADDR + 0x030))
#define J3D_SYS_M_MODEL              ((J3DModel**)(J3D_SYS_ADDR + 0x038))
#define J3D_SYS_M_MAT_PACKET         ((void**)    (J3D_SYS_ADDR + 0x03C))
#define J3D_SYS_M_SHAPE_PACKET       ((void**)    (J3D_SYS_ADDR + 0x040))
#define J3D_SYS_M_SHAPE              ((void**)    (J3D_SYS_ADDR + 0x044))
// Full struct size per JSystem/J3DGraphBase/J3DSys.h. Used by daPy_draw_hook
// for a memcpy-style snapshot around J3DModel_calc(): 5-field save/restore
// was insufficient for skinned models, so we save the whole thing.
#define J3D_SYS_SIZE                 0x128

// Alternate submission. mDoExt_modelEntryDL calls entry() every frame
// (re-registers packets into j3dSys buckets). For a freshly created
// model, that's correct. Kept here as a backup — see multiplayer.c for
// which we're using currently. Tsubo's own _draw uses modelUpdateDL
// which calls update() (no re-entry). Trying entryDL first because
// our model goes through fewer lifecycle hooks than a real actor.
typedef void (*mDoExt_modelUpdateDL_t)(J3DModel* model);
#define mDoExt_modelUpdateDL ((mDoExt_modelUpdateDL_t)0x8000F84C)

// --- Link draw hook infrastructure -------------------------------------
// `daPy_Draw` at 0x80108204 is Link #1's draw thunk; at 0x80108210 it bls
// the real draw implementation `daPy_lk_c::draw @ 0x80107308`. We hook
// that bl via Freighter so our shim runs IN THE DRAW PHASE (inside the
// actor-draw iterator, same context as Kamome/Tsubo/etc draw callbacks).
// modelEntryDL submissions from here land in j3dSys packet lists
// correctly. Calling it from our fapGm_Execute hook instead corrupts
// downstream rendering — observed as missing sky textures with giant
// gray patches.
typedef int (*daPy_lk_c_draw_t)(void* this_);
#define daPy_lk_c_draw ((daPy_lk_c_draw_t)0x80107308)

// --- Eye-fix recipe (item #9, daPy_lk_c::draw 1827-1881) ---------------
// J3D opaque types — fields accessed via byte offset since the game is
// C++ and we don't want to mirror class layouts in C.
typedef void J3DJoint;
typedef void J3DMaterial;
typedef void J3DShape;
typedef void J3DTexture;
typedef void J3DDrawBuffer;
typedef void J3DPacket;

// J3DDrawBuffer::entryImm(packet, idx) — submits a packet's GX-state
// commands into the draw buffer. Used by mDoExt_*Packet::entryOpa()
// inlines as `j3dSys.getDrawBuffer(0)->entryImm(this, 0)`.
typedef int (*J3DDrawBuffer_entryImm_t)(J3DDrawBuffer* self, J3DPacket* packet, int idx);
#define J3DDrawBuffer_entryImm ((J3DDrawBuffer_entryImm_t)0x802ECCC4)

// J3DJoint::entryIn() — submits the joint's mesh chain (with current
// shape-vis flags) into j3dSys's currently-set draw buffer. Reads
// j3dSys.mModel for the model whose mpDrawMtx feeds the GX cmds, so
// callers MUST set j3dSys.mModel = target before calling.
typedef void (*J3DJoint_entryIn_t)(J3DJoint* self);
#define J3DJoint_entryIn ((J3DJoint_entryIn_t)0x802F58D8)

// dDlst_list_c field storage. Read these to get the J3DDrawBuffer*
// for each list, then write into j3dSys+0x48/+0x4C to switch lists
// (= what dComIfGd_setListP0()/setListP1() inlines do).
//   setListP0 = write opa_p0 to BOTH j3dSys+0x48 (OPA) and +0x4C (XLU).
//   setListP1 = write opa_p1 to +0x48, xlu_p1 to +0x4C.
#define DRAWLIST_OPA_LIST_P0_PTR  0x803CA92C
#define DRAWLIST_OPA_LIST_P1_PTR  0x803CA930
#define DRAWLIST_XLU_LIST_P1_PTR  0x803CA934

// j3dSys offsets (per JSystem/J3DGraphBase/J3DSys.h).
#define J3D_SYS_DRAWBUFFER_OPA_OFFSET 0x48
#define J3D_SYS_DRAWBUFFER_XLU_OFFSET 0x4C
#define J3D_SYS_M_TEXTURE_OFFSET      0x58

// Eye-decal Z-compare preset packets in BSS (mDoExt_offCupOnAupPacket /
// mDoExt_onCupOffAupPacket instances). Used in pairs: packet 2 first
// (passes 1+2 in P0), packet 1 after link_root (passes 4+5).
#define L_OFF_CUP_ON_AUP_PACKET1  ((J3DPacket*)0x803E46A4)
#define L_OFF_CUP_ON_AUP_PACKET2  ((J3DPacket*)0x803E46C0)
#define L_ON_CUP_OFF_AUP_PACKET1  ((J3DPacket*)0x803E46DC)
#define L_ON_CUP_OFF_AUP_PACKET2  ((J3DPacket*)0x803E46F8)

// J3D struct field offsets.
#define J3DJOINT_MESH_OFFSET            0x60   // J3DMaterial* mMesh
#define J3DMATERIAL_NEXT_OFFSET         0x04   // J3DMaterial* mNext
#define J3DMATERIAL_SHAPE_OFFSET        0x08   // J3DShape* mShape
#define J3DSHAPE_FLAGS_OFFSET           0x0C   // u32 mFlags
#define J3DSHAPE_FLAG_HIDE              0x0001 // J3DShpFlag_Hide

// J3DModelData::getTexture() = mMaterialTable.mTexture, with
// mMaterialTable @ ModelData+0x58 and mTexture @ MaterialTable+0x18.
#define J3DMODELDATA_TEXTURE_OFFSET     0x70

// J3DJointTree starts at ModelData+0x10; mJointNodePointer @ tree+0x1C.
// So joint(i) = (*(J3DJoint***)(ModelData + 0x2C))[i].
#define J3DMODELDATA_JOINT_NODE_PTR_OFFSET 0x2C

// daPy_lk_c shape arrays (4 J3DShape* each). All three live on the
// daPy_lk_c instance; pointers are SHARED with mini-Link via
// J3DModelData (toggling mFlags affects both renders, so any toggle
// must be undone before frame end).
#define DAPY_MP_Z_OFF_BLEND_SHAPE_OFFSET 0x0374
#define DAPY_MP_Z_OFF_NONE_SHAPE_OFFSET  0x0384
#define DAPY_MP_Z_ON_SHAPE_OFFSET        0x0394
#define LINK_CL_EYE_JOINT_INDEX  0x13
#define LINK_CL_MAYU_JOINT_INDEX 0x15

#endif
