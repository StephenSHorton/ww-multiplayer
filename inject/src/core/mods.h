// The mods the core calls (Phase E3, docs/api/00-design.md §6.6).
//
// There is no function-pointer table: hooks.c calls each mod's entry
// points directly, in a fixed order, and that order is written down at
// each call site as if it were a dispatcher. Today there is one C mod,
// the multiplayer mod (src/mp). A second C mod adds its entry points
// here and its calls in hooks.c: pre-draw steps in priority order,
// post-draw steps in REVERSE priority order, so brackets stay nested.
#ifndef WW_MODS_H
#define WW_MODS_H

#include "wwapi.h"

// src/mp, the multiplayer mod.
//   mp_frame_any  — every frame, Link or not (puppets.c: the actor-puppet
//                   reap, which must also run on the title screen and
//                   during a stage change).
//   mp_frame      — every frame once Link exists and the 300-frame boot
//                   gate has passed (minilink.c: actor puppets, mini-Link
//                   models, the face hook's arm / disarm).
//   mp_draw_post  — after Link #1's draw and the core's post-draw steps
//                   (minilink.c: each mini-Link slot with its face and
//                   tunic bracket and the eye fix).
void mp_frame_any(fopAc_ac_c* link);
void mp_frame(fopAc_ac_c* link);
void mp_draw_post(void* link);

#endif
