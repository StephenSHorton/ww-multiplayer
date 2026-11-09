// The widescreen mod (src/ws): one real aspect for the camera, the clipper
// and (later stages) the 2D layers. docs/design/widescreen-culling.md and
// docs/design/native-widescreen.md. The block is wwws.h's, in the mailbox
// slack; only this file writes it, Go changes it through WWAP_OP_WS_SET.
//
// Stage 1: the clipper hook's aspect (ws_clip_aspect, called from
// hooks.c's clip_setup_hook), the 3D knob, ws_live_factor, the op.
#include "wwapi.h"
#include "mailbox_core.h"
#include "wwops.h"
#include "wwws.h"
#include "wwboat.h"
#include "mailbox_mp.h"
#include "../core/mods.h"

_Static_assert(WWWS_OFF >= WWBOAT_OFF + WWBOAT_SIZE && WWWS_OFF + WWWS_SIZE <= MP_EYE_PACKETS_OFF &&
               (WWWS_OFF & 3) == 0 && (WWWS_FACTOR_OFF & 3) == 0 && WWWS_FLAGS_OFF + 1 <= WWWS_UI_FACTOR_OFF &&
               WWWS_UI_SHIFT_OFF + 4 <= WWWS_SIZE,
               "wwws.h: the widescreen block overlaps the boats block or the eye packets");

#define WS8(o)  (*(volatile u8*)(MAILBOX_ADDR + WWWS_OFF + (o)))
#define WS32(o) (*(volatile u32*)(MAILBOX_ADDR + WWWS_OFF + (o)))
#define WSF(o)  (*(volatile f32*)(MAILBOX_ADDR + WWWS_OFF + (o)))
#define WS_KNOB (*(volatile u32*)WWWS_KNOB_ADDR)

static f32 ws_f32(u32 bits) {
    union { u32 u; f32 f; } v;
    v.u = bits;
    return v.f;
}

// factor in [1, 3]; a NaN fails both compares.
static int ws_factor_ok(f32 f) {
    return f >= 1.0f && f <= 3.0f;
}

// The 3D knob: (4*factor)/3 in mode 2, the vanilla 4/3 otherwise.
static void ws_write_knob(u32 mode, f32 factor) {
    if (mode == WWWS_MODE_NATIVE) {
        union { u32 u; f32 f; } v;
        v.f = (4.0f * factor) / 3.0f;
        WS_KNOB = v.u;
    } else {
        WS_KNOB = WWWS_KNOB_VANILLA;
    }
}

// The factor the 3D picture is widened by: factor in mode 2 (valid block),
// else 1.0.
f32 ws_live_factor(void) {
    f32 f;
    if (WS32(WWWS_MAGIC_OFF) != WWWS_MAGIC || WS8(WWWS_MODE_OFF) != WWWS_MODE_NATIVE)
        return 1.0f;
    f = WSF(WWWS_FACTOR_OFF);
    return ws_factor_ok(f) ? f : 1.0f;
}

// The clipper's aspect: widened only in mode 1 with a valid block, else
// untouched, so a torn or garbage block can never shrink or zero the
// frustum. In mode 2 view->mAspect is already wide (the knob).
f32 ws_clip_aspect(f32 aspect) {
    f32 f;
    if (WS32(WWWS_MAGIC_OFF) != WWWS_MAGIC || WS8(WWWS_MODE_OFF) != WWWS_MODE_CLIP)
        return aspect;
    f = WSF(WWWS_FACTOR_OFF);
    return ws_factor_ok(f) ? aspect * f : aspect;
}

// WS_SET: a = mode, b = factor bits, c = flags.
static u32 ws_op_set(u32 a, u32 b, u32 c) {
    f32 f = ws_f32(b);
    if (a > WWWS_MODE_NATIVE || !ws_factor_ok(f))
        return WWAP_R_REFUSED;
    WSF(WWWS_FACTOR_OFF) = f;
    WS8(WWWS_MODE_OFF) = (u8)a;
    WS8(WWWS_FLAGS_OFF) = (u8)(c & WWWS_FLAGS_SUPPORTED);
    ws_write_knob(a, f);
    return WWAP_R_OK;
}

// From main01_init (after ops_init) on every boot: mode 1, factor 4/3 (the
// Dolphin hack at 16:9), the knob vanilla, the 2D latch off. The usual soft
// reset to the title runs no init and keeps the block as it is.
void ws_init(void) {
    WS32(WWWS_MAGIC_OFF) = 0;   // never half valid
    WS8(WWWS_VERSION_OFF) = WWWS_VERSION;
    WS8(WWWS_MODE_OFF) = WWWS_MODE_CLIP;
    WS8(WWWS_MODE_OFF + 1) = 0;
    WS8(WWWS_MODE_OFF + 2) = 0;
    WSF(WWWS_FACTOR_OFF) = ws_f32(WWWS_FACTOR_DEFAULT);
    WS8(WWWS_FLAGS_OFF) = 0;
    WS8(WWWS_UI_MODE_OFF) = 0;
    WS8(WWWS_UI_MODE_OFF + 1) = 0;
    WS8(WWWS_UI_MODE_OFF + 2) = 0;
    WSF(WWWS_UI_FACTOR_OFF) = 1.0f;
    WSF(WWWS_UI_SHIFT_OFF) = 0.0f;
    ws_write_knob(WWWS_MODE_CLIP, 1.0f);
    WS32(WWWS_MAGIC_OFF) = WWWS_MAGIC;
    ww_cmd_register(WWAP_OP_WS_SET, ws_op_set);
}
