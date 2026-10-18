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
// What it does, and nothing more:
//   _prolog   writes WWREL_MAGIC and the build id into the mailbox slack
//             (the words just past the Mailbox struct) and zeroes the
//             frame counter there;
//   execute   bumps that frame counter once per actor frame;
//   _epilog   clears the magic.
// create allocates nothing and registers no model or collision, so the
// actor is invisible; draw, delete and is_delete just return TRUE.
//
// Build (inject/build.py build_rel): powerpc-eabi-gcc -mcpu=750 -std=c99
// -fno-inline -Og -fshort-enums -fno-jump-tables -ffreestanding
// -fno-builtin -G0 ..., then powerpc-eabi-ld --relocatable -T ww_rel.ld.
// Every game function is called through an absolute function-pointer
// cast (wwapi.h style), which needs no relocation; only the two method
// tables the profile points at come from ww_rel.ld.

#include "mailbox_core.h"

// --- Mailbox slack words -------------------------------------------------
// The Mailbox struct (mailbox_core.h) ends at MAILBOX_ADDR + 0x1B0 and
// __OSArenaLo is 0x80414000 (inject/build.py), so 0xF50 bytes past the
// struct belong to nobody. The REL uses the first three words of that
// slack; nothing in T2 reads or writes them. scripts/extract_blob.py
// reads the three #defines below into internal/inject/blob.go
// (RELMagic, RELMagicOff, RELBuildIDOff, RELFrameOff), so keep each on
// one line in this exact form.
#define WWREL_MAGIC        0x5757524C   // 'WWRL'
#define WWREL_MAGIC_OFF    0x1B0        // u32: WWREL_MAGIC while the module is linked
#define WWREL_BUILD_ID_OFF 0x1B4        // u32: WWREL_BUILD_ID of the linked module
#define WWREL_FRAME_OFF    0x1B8        // u32: +1 per execute of the module's actor

// The build id: build.py passes -DWWREL_BUILD_ID=<CRC-32 of inject/rel's
// sources>, so it changes with the source and never with the clock
// (the ELF stays byte-identical across rebuilds of the same source).
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
// before our create runs. The module's own fields follow it, as
// d_a_*.h classes do, and mSize covers both.
#define FOPAC_AC_C_SIZE 0x290
typedef struct {
    u8  base[FOPAC_AC_C_SIZE];           // fopAc_ac_c
    u32 frames;                          // +0x290 this actor's execute count
} wwrel_actor;

// --- Profile constants (zeldaret/tww) ------------------------------------
#define fpcLy_CURRENT_e        0xFFFFFFFDu // f_pc/f_pc_layer_tag.h
#define fpcPi_CURRENT_e        0xFFFD      // f_pc/f_pc_priority.h
#define fpcNm_RECTANGLE_e      0x00B5      // f_pc/f_pc_name.h: PROC_RECTANGLE, the hijacked proc
#define fpcDwPi_RECTANGLE_e    0x009F      // f_pc/f_pc_draw_priority.h
#define fopAcStts_UNK40000_e   0x00040000  // f_op/f_op_actor.h, d_a_rectangle.cpp's status
#define fopAc_ACTOR_e          0x0         // f_op/f_op_actor.h fopAc_Group_e
#define fopAc_CULLBOX_0_e      0x0         // f_op/f_op_actor.h
#define cPhs_COMPLEATE_e       4           // SSystem/SComponent/c_phase.h
#define TRUE                   1

// --- Actor methods -------------------------------------------------------
// Signatures as the decomp's d_a_rectangle.cpp: create takes the
// fopAc_ac_c, the others the actor class; all are called through
// process_method_func, so they take void*.

// Allocates nothing, registers no model and no collision: the actor is
// invisible and untouchable. cPhs_COMPLEATE_e ends the create phase.
static int wwrel_create(void* actor) {
    wwrel_actor* a = (wwrel_actor*)actor;
    a->frames = 0;
    return cPhs_COMPLEATE_e;
}

static int wwrel_delete(void* actor) {
    (void)actor;
    return TRUE;
}

// Once per frame while the actor lives: the proof the module runs.
static int wwrel_execute(void* actor) {
    wwrel_actor* a = (wwrel_actor*)actor;
    a->frames++;
    WWREL_WORD(WWREL_FRAME_OFF) = WWREL_WORD(WWREL_FRAME_OFF) + 1;
    return TRUE;
}

static int wwrel_is_delete(void* actor) {
    (void)actor;
    return TRUE;
}

// Draws nothing.
static int wwrel_draw(void* actor) {
    (void)actor;
    return TRUE;
}

static const actor_method_class l_wwrel_method = {
    { { wwrel_create, wwrel_delete, wwrel_execute, wwrel_is_delete }, wwrel_draw },
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
    sizeof(wwrel_actor),                 // mSize
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

// --- Module entry points -------------------------------------------------
// The REL header's prolog/epilog/unresolved point at these (the
// converter finds them by name). As ww-online's puppet.c and the
// decomp's REL/executor.c: run the static constructors, then the
// linker's ModuleProlog; the epilog mirrors it. A C module has no
// constructors, so .ctors/.dtors hold one NULL terminator each.
static void* _ctors __attribute__((section(".ctors"))) = 0;
static void* _dtors __attribute__((section(".dtors"))) = 0;

void _prolog(void) {
    DynamicLink_ModuleConstructorsX(&_ctors);
    DynamicLink_ModuleProlog();
    WWREL_WORD(WWREL_FRAME_OFF) = 0;
    WWREL_WORD(WWREL_BUILD_ID_OFF) = g_wwrel_build_id;
    WWREL_WORD(WWREL_MAGIC_OFF) = WWREL_MAGIC;
}

void _epilog(void) {
    WWREL_WORD(WWREL_MAGIC_OFF) = 0;
    DynamicLink_ModuleEpilog();
    DynamicLink_ModuleDestructorsX(&_dtors);
}

void _unresolved(void) {
    DynamicLink_ModuleUnresolved();
}
