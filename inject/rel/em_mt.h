// em_mt.h: what em_mt.c, batch D's Magtail row (enemy-mirror-batch-d.json
// R-D6, D8-D10, "magtail"), takes from enemy_mirror.c. The em_mt_*
// prototypes are enemy_mirror.h's (they take em_slot / em_ext_slot); the
// row's class offsets are wwenemy_kinds.h's MT section. This header holds
// the private copies of enemy_mirror.c's accessors and DOL entry points
// em_mt.c calls, each the same value as its enemy_mirror.c twin (cited
// there), so the whole MT unit drops with one REL_SOURCES line and
// EMK_WITH_MT 0. THE FIRST CUT (cut_order 1).
#ifndef WW_EM_MT_H
#define WW_EM_MT_H

#include "mailbox_core.h"
#include "enemy_mirror.h"

// The enemy block and the annex in the mailbox slack (enemy_mirror.c EB_*,
// AX_*).
#define EMT_EB_ADDR     (MAILBOX_ADDR + WWENEMY_OFF)
#define EMT_EB_U8(off)  (*(volatile u8*)(EMT_EB_ADDR + (off)))
#define EMT_EB_U16(off) (*(volatile u16*)(EMT_EB_ADDR + (off)))
#define EMT_EB_S8(off)  (*(volatile s8*)(EMT_EB_ADDR + (off)))
#define EMT_EB_S16(off) (*(volatile s16*)(EMT_EB_ADDR + (off)))
#define EMT_AX_S16(off) (*(volatile s16*)(MAILBOX_ADDR + (off)))
#define EMT_AF(a, T, off) (*(T*)((u8*)(a) + (off)))
#define EMT_MEM1_LO     0x80000000u
#define EMT_MEM1_HI     0x83000000u   // 48 MB MEM1 (docs/05 "48 MB MEM1")

// Actor fields (enemy_mirror.c AC_*).
#define EMT_AC_PROC_NAME 0x008        // s16 mProcName
#define EMT_AC_HEALTH    0x285        // s8 health
#define EMT_AC_SCALE     0x214        // cXyz scale

// Collider objects (enemy_mirror.c CC_*): cCcD_Obj At/Tg/Co SPrm and the
// sphere shape past the dCcD_GObjInf.
#define EMT_CC_AT_SPRM      0x00      // u32: bit 0 = Set
#define EMT_CC_TG_RPRM      0x1C      // u32: bit 0 = hit
#define EMT_CC_TG_HIT_OBJ   0x20      // cCcD_Obj* the Tg hit's attacker
#define EMT_CC_CO_SPRM      0x2C      // u32: bit 0 = Set
#define EMT_CC_STTS         0x44      // cCcD_Stts*
#define EMT_CC_STTS_ACTOR   0x0C      // stts +0x0C: the owner actor
#define EMT_CC_SHAPE_CENTER 0x118     // cM3dGSph centre
#define EMT_CC_SHAPE_RADIUS 0x124     // f32 cM3dGSph mRadius
#define EMT_MORF_MODEL      0x50      // J3DModel* mDoExt_McaMorf::mpModel
#define EMT_J3D_BASE_SCALE  0x18      // Vec J3DModel::mBaseScale (J3DModel.h:148)

// The DOL (enemy_mirror.c, the same symbols.txt addresses).
typedef void (*emt_ccs_set_t)(void* ccs, void* obj);   // Set__4cCcSFP8cCcD_Obj
#define emt_cCcS_Set    ((emt_ccs_set_t)0x80242D04)
#define EMT_CCS         ((void*)0x803C72AC)             // g_dComIfG_Ccsp (dComIfG_Ccsp())
typedef void (*emt_clr_hit_t)(void* obj);              // ClrTgHit__12dCcD_GObjInfFv
#define emt_ClrTgHit    ((emt_clr_hit_t)0x800AB958)

// MT-private values (mt.rel, C1b-magtail-redo.md).
#define EMT_SPH_OFF_R   (-200.0f)     // the R body_control1/2 give a body sphere while +0x460 != 0 (rodata 0x50): no contact
#define EMT_RING_MASK   (EMK_MT_RING_LEN - 1)
#define EMT_RESEED_STEPS 6            // the FIGHT -> MOVE reseed's j = 0..5 per segment (mt.rel 0x1458-0x1524)
#define EMT_RESEED_DIV  5.0f          // rodata 0x70
#define EMT_AC_POS      0x1F8         // cXyz current.pos: the copy's smoothed head
// How far the copy's head must move from the ring's last kept entry before
// the body step lets body_control1 advance the ring. The host head travels
// 6.0-6.4 per ring advance in MOVE and drops to 0 at a dive in one frame
// (archive/enemy-batch-d/live/two/mtA3.txt), but the copy's smoothed head
// eases in and out at 1-5 per frame, and at 3.0 those frames kept entries
// about 3.5 apart: six-entry segment spacing fell to 21-25 at every stop
// and restart (LIVE-3, host 30-41). Replaying the hold rule on LIVE-3's
// copy head trace (two/mtB4.txt, one sample per frame) gives, for the
// six-entry spacing min / p95 / max and the share outside the host's
// 30.2-45.7: 3.0 -> 19.2 / 39.4 / 47.3, 7.7%; 4.0 -> 23.6 / 41.6 / 50.7,
// 4.4%; 5.0 -> 30.7 / 45.0 / 55.1, 4.3%; 5.5 -> 33.8 / 47.6 / 56.8,
// 10.8%; 6.0 holds the copy's steady 5-6 frames too (kept entries 469 ->
// 362, 85% above 42). 4.75-5.25 give the same minimum. The spans above 46
// left at 5.0 are the copy's own 7-10 per frame catch-up steps after a
// stream gap, which 3.0 shows too, not the hold.
#define EMT_RING_STEP_MIN 5.0f

#endif
