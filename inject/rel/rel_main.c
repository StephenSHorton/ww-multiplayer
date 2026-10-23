// rel_main.c: a minimal REL module for GZLE01 that proves the REL
// pipeline (design: ww-multiplayer-handoff/design/rel-module-plan.md,
// docs: inject/rel/README.md).
//
// The patcher ships this module as RELS.arc's d_a_rectangle.rel (module
// ID 0x58) and points the profile list's slot for proc 0xB5
// (PROC_RECTANGLE, an unused dummy actor) at g_profile_WWREL below. From
// then on the game's own dynamic linker (fpcLd_Load -> cDyl_LinkASync ->
// OSLink) loads, relocates and cache-flushes this code the first time an
// actor of proc 0xB5 is created, calls _prolog, and calls _epilog before
// unlinking it when the last such actor is deleted.
//
// What it does:
//   _prolog   writes WWREL_MAGIC and the build id into the mailbox slack
//             (the words just past the Mailbox struct) and zeroes the
//             frame counter there;
//   execute   bumps that frame counter once per actor frame, then runs
//             the Moblin mirror (enemy_mirror.c) over the block Go writes
//             at MAILBOX_ADDR + WWENEMY_OFF (include/wwenemy.h);
//   delete    hands every mirrored Moblin back;
//   _epilog   does the same if delete never ran, then clears the magic.
// create allocates nothing and registers no model or collision, so the
// actor is invisible; draw, delete and is_delete just return TRUE.
//
// Remote-Link (remote.c; design: ww-multiplayer-handoff/design/
// remote-link-design.md section 5.1) gives the profile two roles, chosen
// by mParameters: 0 is the keeper above (plus remote.c's swap state, body
// lifecycle and client apply), WWREMOTE_BODY_PARAMS_TAG | slot << 8 | gen
// is a lite body (remote.c's remote_body_*), created only by the keeper.
// mSize is WWREMOTE_ACTOR_SIZE (0x600) for both. _prolog also publishes
// wwrel_exec_hook (T2's exec shim target) at WWREL_EXEC_HOOK_OFF, last;
// _epilog clears it first.
//
// Stage 4 (ww-multiplayer-handoff/design/remote-link-stage4-plan.md D4-1)
// adds a second profile in the same module, g_profile_WWBODY: the full
// body (fullbody.c), a real daPy_lk_c of WWREMOTE_FULL_SIZE bytes. The
// patcher points profile slot WWREMOTE_FULL_PROC (0x27) at it and the DOL
// name-table patch (build.py) makes proc 0x27 load this module too, so
// DMC[0x27] is DMC[0xB5]. Its mProcName is 0xB5 like the keeper's; only
// the keeper creates it (remote.c), only with FLAG_FULL_BODIES.
//
// Build (inject/build.py build_rel): powerpc-eabi-gcc -mcpu=750 -std=c99
// -fno-inline -Og -fshort-enums -fno-jump-tables -ffreestanding
// -fno-builtin -G0 ..., then powerpc-eabi-ld --relocatable -T ww_rel.ld.
// Every game function is called through an absolute function-pointer
// cast (wwapi.h style), which needs no relocation; only the two method
// tables the profile points at come from ww_rel.ld.

#include "mailbox_core.h"

// --- Mailbox slack words -------------------------------------------------
// WWREL_MAGIC and the three offsets are in include/wwrel.h, shared with
// T2's src/mp/relload.c (which creates this module's actor and reads the
// magic); scripts/extract_blob.py reads them from there.
#include "wwrel.h"

// The Moblin mirror (enemy_mirror.c): its state lives in the actor below
// and it runs from execute; delete and _epilog release what it adopted.
#include "enemy_mirror.h"

// Remote-Link (remote.c): the keeper's swap state follows the mirror's
// state in the actor; the lite body is the profile's second role.
#include "remote.h"

// The full body (fullbody.c): the second profile's methods.
#include "fullbody.h"

// The boss_kill relay op (bosskill.c): stateless, its words are in the
// slack (wwboss.h); the keeper's create consumes a stale request and its
// execute runs a new one.
#include "bosskill.h"

// The actor_refresh op (refresh.c, batch D R-D5): stateless as boss_kill,
// its block is in the slack (wwrefresh.h); create consumes a stale
// request, execute runs a new one after boss_kill's.
#include "refresh.h"

// The build id: build.py passes -DWWREL_BUILD_ID=<CRC-32 of inject/rel's
// sources and the slack headers>, so it changes with the source and never
// with the clock (the ELF stays byte-identical across rebuilds of the
// same source).
#ifndef WWREL_BUILD_ID
#define WWREL_BUILD_ID 0
#endif

#define WWREL_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + (off)))

// The slack words must lie past the Mailbox struct and below the arena
// (0x80414000, inject/build.py's OSInit patch).
typedef char wwrel_check_mailbox_size[(sizeof(Mailbox) == 0x1B0) ? 1 : -1];
typedef char wwrel_check_slack_start[(WWREL_MAGIC_OFF >= sizeof(Mailbox)) ? 1 : -1];
typedef char wwrel_check_slack_end[(MAILBOX_ADDR + WWREL_FRAME_OFF + 4 <= 0x80414000) ? 1 : -1];

// Exported so scripts/extract_blob.py can read the id the ELF was built
// with (it looks the symbol up in .rodata) and emit it as RELBuildID.
const u32 g_wwrel_build_id = WWREL_BUILD_ID;

// --- Game functions (absolute, no relocations) ---------------------------
// zeldaret/tww config/GZLE01/symbols.txt; src/REL/executor.c shows what a
// vanilla REL's _prolog/_epilog/_unresolved call.
typedef void (*wwrel_void_fn)(void);
typedef void (*wwrel_ctors_fn)(void* list);
#define DynamicLink_ModuleProlog       ((wwrel_void_fn)0x802410B4)   // DynamicLink::ModuleProlog (blr)
#define DynamicLink_ModuleEpilog       ((wwrel_void_fn)0x802410B8)   // DynamicLink::ModuleEpilog (blr)
#define DynamicLink_ModuleUnresolved   ((wwrel_void_fn)0x802410BC)   // DynamicLink::ModuleUnresolved
#define DynamicLink_ModuleConstructorsX ((wwrel_ctors_fn)0x80241178) // DynamicLink::ModuleConstructorsX
#define DynamicLink_ModuleDestructorsX  ((wwrel_ctors_fn)0x802411F8) // DynamicLink::ModuleDestructorsX

// --- Game types (zeldaret/tww include/f_pc, include/f_op) ----------------
// Layouts are transcribed with their decomp offsets and checked below.
typedef int (*process_method_func)(void*);

// f_pc/f_pc_method.h
typedef struct {
    process_method_func mpCreateFunc;    // 0x00
    process_method_func mpDeleteFunc;    // 0x04
    process_method_func mpExecuteFunc;   // 0x08
    process_method_func mpIsDeleteFunc;  // 0x0C
} process_method_class;                  // 0x10

// f_pc/f_pc_leaf.h
typedef struct {
    process_method_class base;           // 0x00
    process_method_func mpDrawFunc;      // 0x10
} leafdraw_method_class;                 // 0x14

// f_op/f_op_actor.h
typedef struct {
    leafdraw_method_class base;          // 0x00
    u8 field_0x14[0xC];                  // 0x14 "Likely padding"
} actor_method_class;                    // 0x20

// f_pc/f_pc_profile.h process_profile_definition (0x1C), f_pc/f_pc_leaf.h
// leaf_process_profile_definition (0x24) and f_op/f_op_actor.h
// actor_process_profile_definition (0x30), flattened.
typedef struct {
    u32 mLayerID;                        // 0x00 fpcLy_*
    u16 mListID;                         // 0x04
    u16 mListPrio;                       // 0x06 fpcPi_*
    s16 mProcName;                       // 0x08 fpcNm_*: MUST be the hijacked proc id
    u8  field_0x0A[2];                   // 0x0A
    const void* sub_method;              // 0x0C &g_fpcLf_Method
    s32 mSize;                           // 0x10 bytes allocated for the actor
    s32 mSizeOther;                      // 0x14
    u32 mParameters;                     // 0x18
    const void* leaf_sub_method;         // 0x1C &g_fopAc_Method
    s16 mPriority;                       // 0x20 draw priority fpcDwPi_*
    u8  field_0x22[2];                   // 0x22
    const actor_method_class* actor_sub_method; // 0x24
    u32 status;                          // 0x28 fopAcStts_*
    u8  group;                           // 0x2C fopAc_Group_e
    u8  cullType;                        // 0x2D fopAc_CullType
    u8  field_0x2E[2];                   // 0x2E
} actor_process_profile_definition;      // 0x30

typedef char wwrel_check_method_size[(sizeof(process_method_class) == 0x10 &&
                                      sizeof(leafdraw_method_class) == 0x14 &&
                                      sizeof(actor_method_class) == 0x20) ? 1 : -1];
typedef char wwrel_check_profile_size[(sizeof(actor_process_profile_definition) == 0x30) ? 1 : -1];
typedef char wwrel_check_profile_offs[(__builtin_offsetof(actor_process_profile_definition, sub_method) == 0x0C &&
                                       __builtin_offsetof(actor_process_profile_definition, leaf_sub_method) == 0x1C &&
                                       __builtin_offsetof(actor_process_profile_definition, mPriority) == 0x20 &&
                                       __builtin_offsetof(actor_process_profile_definition, actor_sub_method) == 0x24 &&
                                       __builtin_offsetof(actor_process_profile_definition, status) == 0x28 &&
                                       __builtin_offsetof(actor_process_profile_definition, cullType) == 0x2D) ? 1 : -1];

// The game data the profile points at (addresses in ww_rel.ld).
extern const u8 g_fpcLf_Method[];        // leafdraw_method_class, f_pc_leaf.cpp
extern const u8 g_fopAc_Method[];        // actor_method_class, f_op_actor.cpp

// The actor. f_op/f_op_actor.h fopAc_ac_c is 0x290 bytes in GZLE01
// (the decomp's "Size: 0x290"); the game's fopAc create fills that part
// before our create runs. The keeper's own fields follow it, as d_a_*.h
// classes do: the frame count, the enemy mirror's state (enemy_mirror.h),
// remote-Link's swap state (remote.h) and the enemy mirror's layout-3
// cache (em_ext, which fills the allocation to 0x600), so nothing lives
// in .bss. The lite body (remote.h remote_body) overlays the same
// allocation; mSize is
// WWREMOTE_ACTOR_SIZE, the larger of the two roles, so the keeper pays
// 0x600 - 0x458 = 0x1A8 bytes of ZeldaHeap more than before (section 5.1).
#define FOPAC_AC_C_SIZE 0x290
typedef struct {
    u8  base[FOPAC_AC_C_SIZE];           // fopAc_ac_c
    u32 frames;                          // +0x290 this actor's execute count
    enemy_mirror_state mirror;           // +0x294 enemy_mirror.c's slots and counters
    remote_swap_state swap;              // +0x458 remote.c's latches, body records, client apply
    em_ext ext;                          // +0x5D4 the enemy mirror's layout-3 cache (enemy_mirror.h), to 0x600
} wwrel_actor;
typedef char wwrel_check_actor_layout[(__builtin_offsetof(wwrel_actor, frames) == 0x290 &&
                                       __builtin_offsetof(wwrel_actor, mirror) == 0x294 &&
                                       __builtin_offsetof(wwrel_actor, swap) == 0x294 + sizeof(enemy_mirror_state) &&
                                       __builtin_offsetof(wwrel_actor, swap) == WWREMOTE_KEEPER_SWAP_OFF) ? 1 : -1];
// em_ext takes the keeper's free tail: right after the swap state (0x458 +
// 0x17C, remote.c:113,119), 0x2C bytes, ending at WWREMOTE_ACTOR_SIZE. A
// lite body ends at WWREMOTE_BODY_END (0x574) and never reaches it.
typedef char wwrel_check_actor_ext[(__builtin_offsetof(wwrel_actor, ext) == WWREMOTE_KEEPER_SWAP_OFF + sizeof(remote_swap_state) &&
                                    __builtin_offsetof(wwrel_actor, ext) == 0x5D4 &&
                                    sizeof(em_ext) == 0x2C &&
                                    __builtin_offsetof(wwrel_actor, ext) + sizeof(em_ext) <= WWREMOTE_ACTOR_SIZE &&
                                    WWREMOTE_BODY_END <= __builtin_offsetof(wwrel_actor, ext)) ? 1 : -1];
// The REL's hit detail for a kind refusal must not reuse remote-Link's
// NO_BODY (both reach the same result byte, em_hit_publish).
typedef char wwrel_check_hd_kind[(WWENEMY_HD_KIND != WWREMOTE_HD_NO_BODY && WWENEMY_HD_KIND > WWREMOTE_HD_NO_BODY) ? 1 : -1];
typedef char wwrel_check_actor_size[(sizeof(wwrel_actor) <= WWREMOTE_ACTOR_SIZE &&
                                     sizeof(remote_body) <= WWREMOTE_ACTOR_SIZE &&
                                     (WWREMOTE_ACTOR_SIZE & 3) == 0) ? 1 : -1];
// The full body: daPy_lk_c (0x4C28, d_a_player_main.h:2375), then its tail.
typedef char wwrel_check_full_size[(WWREMOTE_FULL_TAIL_OFF == WWREMOTE_FULL_DAPY_SIZE &&
                                    WWREMOTE_FULL_TAIL_OFF + sizeof(rl_full_tail) <= WWREMOTE_FULL_SIZE &&
                                    WWREMOTE_FULL_END <= WWREMOTE_FULL_SIZE &&
                                    (WWREMOTE_FULL_SIZE & 0x1F) == 0 &&
                                    WWREMOTE_FULL_PROC != 0x00B5) ? 1 : -1];
// The remote-Link slack words (exec hook, swap state, shanm table) follow
// the frame counter and stay below the enemy block and its annex
// (wwenemy.h WWENEMY_ANNEX_OFF, layout 3).
typedef char wwrel_check_hook_words[(WWREL_EXEC_HOOK_OFF == WWREL_FRAME_OFF + 4 &&
                                     WWREL_SWAP_STATE_OFF == WWREL_EXEC_HOOK_OFF + 4 &&
                                     WWREL_SWAP_STATE_OFF + 4 <= WWENEMY_OFF &&
                                     WWREL_SHANM_OFF == WWREL_SWAP_STATE_OFF + 4 &&
                                     WWREL_SHANM_OFF + 4 <= WWENEMY_OFF &&
                                     WWREL_SHANM_OFF + 4 <= WWENEMY_ANNEX_OFF &&
                                     WWENEMY_ANNEX_OFF + WWENEMY_ANNEX_SIZE == WWENEMY_OFF) ? 1 : -1];

// --- Profile constants (zeldaret/tww) ------------------------------------
#define fpcLy_CURRENT_e        0xFFFFFFFDu // f_pc/f_pc_layer_tag.h
#define fpcPi_CURRENT_e        0xFFFD      // f_pc/f_pc_priority.h
#define fpcNm_RECTANGLE_e      0x00B5      // f_pc/f_pc_name.h: PROC_RECTANGLE, the hijacked proc
#define fpcDwPi_RECTANGLE_e    0x009F      // f_pc/f_pc_draw_priority.h
#define fopAcStts_UNK40000_e   0x00040000  // f_op/f_op_actor.h, d_a_rectangle.cpp's status
#define fopAc_ACTOR_e          0x0         // f_op/f_op_actor.h fopAc_Group_e
#define fopAc_CULLBOX_0_e      0x0         // f_op/f_op_actor.h
#define cPhs_COMPLEATE_e       4           // SSystem/SComponent/c_phase.h
#define cPhs_ERROR_e           5           // the game deletes the process
#define TRUE                   1

// --- Actor methods -------------------------------------------------------
// Signatures as the decomp's d_a_rectangle.cpp: create takes the
// fopAc_ac_c, the others the actor class; all are called through
// process_method_func, so they take void*.

// The keeper (params 0) allocates nothing, registers no model and no
// collision: the actor is invisible and untouchable. cPhs_COMPLEATE_e
// ends the create phase. A lite body (remote_is_body) is remote.c's;
// any other params is not ours.
static int wwrel_create(void* actor) {
    wwrel_actor* a = (wwrel_actor*)actor;
    if (remote_is_body(actor)) return remote_body_create(actor);
    if (*(u32*)((u8*)actor + 0xB0) != 0) return cPhs_ERROR_e;   // mParameters (f_pc_base.h:30)
    a->frames = 0;
    bosskill_link();
    refresh_link();
    enemy_mirror_init(&a->mirror, &a->ext);
    remote_keeper_init(&a->swap);
    return cPhs_COMPLEATE_e;
}

// The keeper hands every mirrored Moblin back and retires its bodies
// before it goes.
static int wwrel_delete(void* actor) {
    wwrel_actor* a = (wwrel_actor*)actor;
    if (remote_is_body(actor)) return remote_body_delete(actor);
    enemy_mirror_delete(&a->mirror, &a->ext);
    remote_keeper_delete(&a->swap);
    return TRUE;
}

// Once per frame while the keeper lives: the frame counter Go watches,
// then the Moblin mirror's whole per-frame loop, then remote-Link's
// (after the mirror, so the client apply sees this frame's adopted
// Moblins), then a new boss_kill request, if any (bosskill.c), then a new
// actor_refresh request (refresh.c). A body
// poses itself (remote.c).
static int wwrel_execute(void* actor) {
    wwrel_actor* a = (wwrel_actor*)actor;
    u32 frame;
    if (remote_is_body(actor)) return remote_body_execute(actor);
    frame = WWREL_WORD(WWREL_FRAME_OFF) + 1;
    a->frames++;
    WWREL_WORD(WWREL_FRAME_OFF) = frame;
    enemy_mirror_frame(&a->mirror, &a->ext, frame);
    remote_keeper_frame(&a->swap, &a->mirror, frame);
    bosskill_frame();
    refresh_frame();
    return TRUE;
}

static int wwrel_is_delete(void* actor) {
    (void)actor;
    return TRUE;
}

// Draws nothing, in either role (the mini-Link keeps rendering the
// remote player).
static int wwrel_draw(void* actor) {
    (void)actor;
    return TRUE;
}

static const actor_method_class l_wwrel_method = {
    { { wwrel_create, wwrel_delete, wwrel_execute, wwrel_is_delete }, wwrel_draw },
    { 0 },
};

// The full body (fullbody.c), proc WWREMOTE_FULL_PROC's profile: draw
// returns TRUE, is_delete too.
static const actor_method_class l_wwbody_method = {
    { { fullbody_create, fullbody_delete, fullbody_execute, wwrel_is_delete }, fullbody_draw },
    { 0 },
};

// The profile the patcher points the profile list's slot 0xB5 at. In
// .rodata (const) so the patcher finds it by symbol in a read-only
// section; layer, list, priorities, status, group and cull type are
// d_a_rectangle.cpp's own (g_profile_RECTANGLE), a small vanilla actor.
const actor_process_profile_definition g_profile_WWREL = {
    fpcLy_CURRENT_e,                     // mLayerID
    0x0007,                              // mListID
    fpcPi_CURRENT_e,                     // mListPrio
    fpcNm_RECTANGLE_e,                   // mProcName = 0xB5
    { 0, 0 },
    g_fpcLf_Method,                      // sub_method
    WWREMOTE_ACTOR_SIZE,                 // mSize: keeper or lite body (0x600)
    0,                                   // mSizeOther
    0,                                   // mParameters
    g_fopAc_Method,                      // leaf_sub_method
    fpcDwPi_RECTANGLE_e,                 // mPriority
    { 0, 0 },
    &l_wwrel_method,                     // actor_sub_method
    fopAcStts_UNK40000_e,                // status
    fopAc_ACTOR_e,                       // group
    fopAc_CULLBOX_0_e,                   // cullType
    { 0, 0 },
};

// The profile the patcher points the profile list's slot WWREMOTE_FULL_PROC
// (0x27) at (stage-4 plan D4-1): g_profile_WWREL's but for mSize (a
// daPy_lk_c plus its tail; fpcBs_Create is mSize's only reader,
// f_pc_base.cpp:93) and the methods. mProcName stays 0xB5 (fpcBs_Create
// copies it, mProfName becomes 0x27), so remote.c's proc checks, NAME_A9
// and Go's classification see the same proc as a lite body. Status
// 0x00040000 has no NOCULLEXEC: culling never skips the execute.
const actor_process_profile_definition g_profile_WWBODY = {
    fpcLy_CURRENT_e,                     // mLayerID
    0x0007,                              // mListID
    fpcPi_CURRENT_e,                     // mListPrio
    fpcNm_RECTANGLE_e,                   // mProcName = 0xB5
    { 0, 0 },
    g_fpcLf_Method,                      // sub_method
    WWREMOTE_FULL_SIZE,                  // mSize: daPy_lk_c + rl_full_tail (0x4C80)
    0,                                   // mSizeOther
    0,                                   // mParameters
    g_fopAc_Method,                      // leaf_sub_method
    fpcDwPi_RECTANGLE_e,                 // mPriority
    { 0, 0 },
    &l_wwbody_method,                    // actor_sub_method
    fopAcStts_UNK40000_e,                // status
    fopAc_ACTOR_e,                       // group
    fopAc_CULLBOX_0_e,                   // cullType
    { 0, 0 },
};

u32 wwrel_body_profile(void) {
    return (u32)&g_profile_WWBODY;
}

// --- Module entry points -------------------------------------------------
// The REL header's prolog/epilog/unresolved point at these (the
// converter finds them by name). As ww-online's puppet.c and the
// decomp's REL/executor.c: run the static constructors, then the
// linker's ModuleProlog; the epilog mirrors it. A C module has no
// constructors, so .ctors/.dtors hold one NULL terminator each.
static void* _ctors __attribute__((section(".ctors"))) = 0;
static void* _dtors __attribute__((section(".dtors"))) = 0;

// The exec hook word is written last: T2's exec shim branches to it on the
// next actor execute (fpcEx_Handler runs after fpcCt_Handler, so never
// while this runs). The swap state word is the keeper create's.
void _prolog(void) {
    DynamicLink_ModuleConstructorsX(&_ctors);
    DynamicLink_ModuleProlog();
    WWREL_WORD(WWREL_FRAME_OFF) = 0;
    WWREL_WORD(WWREL_BUILD_ID_OFF) = g_wwrel_build_id;
    WWREL_WORD(WWREL_SWAP_STATE_OFF) = 0;
    WWREL_WORD(WWREL_MAGIC_OFF) = WWREL_MAGIC;
    WWREL_WORD(WWREL_EXEC_HOOK_OFF) = (u32)wwrel_exec_hook;
}

// The exec hook word is cleared first, before this code can go away
// (the unlink follows in fpcDt_Handler, never during an execute). The
// shanm word (WWREL_SHANM_OFF) is kept: its archive-heap table outlives
// the module (full bodies deleted at a stage teardown still need it, and
// the next link reuses it).
void _epilog(void) {
    WWREL_WORD(WWREL_EXEC_HOOK_OFF) = 0;
    remote_unlink();                     // clears WWREL_SWAP_STATE_OFF
    enemy_mirror_unlink();               // release_all if delete never ran
    WWREL_WORD(WWREL_MAGIC_OFF) = 0;
    DynamicLink_ModuleEpilog();
    DynamicLink_ModuleDestructorsX(&_dtors);
}

void _unresolved(void) {
    DynamicLink_ModuleUnresolved();
}
