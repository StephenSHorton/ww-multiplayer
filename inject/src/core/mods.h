// The mods the core calls (Phase E3, docs/api/00-design.md §6.6; the
// WwModuleDesc table since co-op M7, wind#90).
//
// Each C mod is one WwModuleDesc in ww_modules[] (hooks.c), in priority
// order. The hooks walk the table: init once at boot (after the core's own
// ops_init, so a mod can register ops), frame_any and frame each frame,
// draw_pre before Link #1's draw in priority order and draw_post after it
// in REVERSE priority order, so brackets stay nested. A NULL entry is
// skipped. Today there is one C mod, the multiplayer mod (src/mp).
#ifndef WW_MODS_H
#define WW_MODS_H

#include "wwapi.h"

// src/mp, the multiplayer mod.
//   mp_frame_any  — every frame, Link or not (puppets.c: the actor-puppet
//                   reap, which must also run on the title screen and
//                   during a stage change).
//   mp_frame      — every frame once Link exists and the 300-frame boot
//                   gate has passed (minilink.c: actor puppets, the REL
//                   module's actor (relload.c), mini-Link models, the
//                   face hook's arm / disarm).
//   mp_draw_post  — after Link #1's draw and the core's post-draw steps
//                   (minilink.c: each mini-Link slot with its face and
//                   tunic bracket and the eye fix).
void mp_frame_any(fopAc_ac_c* link);
void mp_frame(fopAc_ac_c* link);
void mp_draw_post(void* link);

// src/ws, the widescreen mod (docs/design/widescreen-culling.md,
// native-widescreen.md). ws_init writes the WWWS block and registers
// WWAP_OP_WS_SET; ws_clip_aspect is the clipper hook's aspect (widens only
// in mode 1); ws_live_factor is the 3D picture's factor (mode 2, else 1).
void ws_init(void);
f32 ws_clip_aspect(f32 aspect);
f32 ws_live_factor(void);
// Stages 2-3 (native-widescreen.md section 5): ws_2d_factor is the latched
// 2D factor every 2D reader uses (1.0 unless 2D is compressed);
// ws_screen_name is hook A's stash of J2DScreen::set's name, consumed by
// ws_screen_loaded (hook B: the screen table, the latch on EPOCH).
f32 ws_2d_factor(void);
// Stage 4 (section 8): ws_frame_any is the WS module's frame_any step, the
// THP movies' polygon width (T-9); it runs every frame, Link or not.
void ws_frame_any(fopAc_ac_c* link);
extern const char* ws_screen_name;
void ws_screen_loaded(u8* scrn, int ok);

typedef struct {
    u32 id;                                 // four characters, e.g. 'MP  '
    void (*init)(void);                     // main01_init, after the core's ops_init
    void (*frame_any)(fopAc_ac_c* link);    // every frame, link may be 0
    void (*frame)(fopAc_ac_c* link);        // every frame past the boot gate, with Link
    void (*draw_pre)(void* link);           // before Link #1's draw (priority order)
    void (*draw_post)(void* link);          // after it (reverse priority order)
} WwModuleDesc;

#endif
