// Wind Waker GZLE01 function addresses and types
#ifndef GAME_H
#define GAME_H

typedef unsigned int u32;
typedef unsigned short u16;
typedef unsigned char u8;
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

#endif
