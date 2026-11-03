// The multiplayer mod's C side (src/mp; Phase E3 split multiplayer.c
// into src/core and src/mp, docs/api/00-design.md §6.6): what its files
// share. The core calls the mod through the entry points in
// ../core/mods.h; the mod calls the core services in wwapi.h.
#ifndef WW_MP_H
#define WW_MP_H

#include "wwapi.h"
#include "mailbox_core.h"
#include "wwvis.h"
#include "../core/mods.h"

// Actor procs the actor puppets spawn (puppets.c) or tried to.
#define PROC_PLAYER     0x00A9
#define PROC_RECTANGLE  0x00B5  // the unused dummy actor whose module the REL replaces (relload.c)
#define PROC_KAMOME     0x00C3
#define PROC_NPC_KO1    0x0141
#define PROC_NPC_OB1    0x014D  // Rose (Outset villager)
#define PROC_NPC_FA1    0x016A
#define PROC_GRASS      0x01B8
#define PROC_TSUBO      0x01CB
#define PROC_Obj_Barrel 0x01CE

// The actor puppets' KAMOME parameter (daKamome_Create, d_a_kamome.cpp:1482-1507):
// byte 0 type 0 (a plain gull), byte 1 mKoMaxCount 0, byte 2 path 0xFF (no
// path: path 0 of the spawn room is NULL in rooms without one, M_Dai, and
// the create fails, which was the respawn churn of #131), byte 3 switch 0xBF.
// The switch keeps the gull hidden (daKamome_Draw returns while mSwitchNo !=
// 0): a drawn gull hangs the game in J3DDrawBuffer::draw on non-sea stages
// (Hyrule, dungeons), so the puppet stays invisible, as it always was. 0xBF
// is the last dungeon switch (0x80-0xBF, cleared on each stage change), which
// stages rarely set; param 0 used memory switch 0. Not a zone switch (0xC0+):
// those look the room's zone up, and a puppet spawned with Link in room -1
// (Outset arrival) read mStatus[-1] every frame and froze the game. wind#76.
#define KAMOME_PUPPET_PARAM 0xBFFF0000U

// "Always" archive constants (used as a non-shared model probe for the
// mini-Link modelEntryDL sky-breakage investigation). mpm_tubo is the
// small-pot BDL. Rigid, non-skinned — doesn't need J3DModel::calc().
#define ALWAYS_BDL_MPM_TUBO  0x31

// Link's CL eye and eyebrow joints, which the eye-fix recipe enters
// itself (minilink.c run_eye_fix, mDoExt_skip_recipe_joints).
#define LINK_CL_EYE_JOINT_INDEX  0x13
#define LINK_CL_MAYU_JOINT_INDEX 0x15

// minilink.c: the mini-Link models (see "Lifetimes" there).
extern J3DModelData* mini_link_data;
extern J3DModel* mini_link_models[MAX_REMOTE_LINKS];
extern u8 mini_link_state;   // 0 = not tried yet, 1 = created, 0xFF = gave up

// puppets.c: one frame of the actor puppets; returns the best progress
// value across the slots (3-10, see there).
u32 puppets_frame(fopAc_ac_c* link);

// face.c: the face-state hook's arm / disarm (each frame) and the
// per-slot face bracket around a mini-Link's entry.
void face_hook_process_arm(void);
void face_swap_for_slot(int slot);
void face_restore(void);
void face_swap_reset(void);

// relload.c: keep the REL module linked by owning one actor of its proc
// per stage (called by mp_frame every frame once Link exists).
void rel_frame(fopAc_ac_c* link);

#endif
