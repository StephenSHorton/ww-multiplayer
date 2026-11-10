// The widescreen mod (src/ws): one real aspect for the camera, the clipper
// and the 2D layers. docs/design/widescreen-culling.md and
// docs/design/native-widescreen.md. The block is wwws.h's, in the mailbox
// slack; only this file writes it, Go changes it through WWAP_OP_WS_SET.
//
// Stage 1: the clipper hook's aspect (ws_clip_aspect, called from
// hooks.c's clip_setup_hook), the 3D knob, ws_live_factor, the op.
// Stages 2 and 3 (native-widescreen.md sections 3.2-3.5, 5, 8): the 2D
// latch (ws_latch, ws_2d_factor, ws_2d_shift), the data words it rewrites
// (ws_apply) and the J2D screen walker (ws_screen_loaded) over the
// generated table (wstable_gen.h). The hook sites are hooks.c's.
#include "wwapi.h"
#include "mailbox_core.h"
#include "wwops.h"
#include "wwws.h"
#include "wwboat.h"
#include "mailbox_mp.h"
#include "../core/mods.h"
#include "wstable_gen.h"

// T2 is short of room (native-widescreen.md section 6): this unit is built
// for size. Check the map for _savegpr/_restgpr calls (not in the DOL)
// after a change.
#pragma GCC optimize("Os")

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

// --- the 2D latch (section 5) ----------------------------------------------

// The latched 2D factor every 2D reader uses: ui_factor when the magic
// matches, ui_mode is 2 and 1 <= ui_factor <= 3 (NaN fails), else 1.0.
f32 ws_2d_factor(void) {
    f32 f;
    if (WS32(WWWS_MAGIC_OFF) != WWWS_MAGIC || WS8(WWWS_UI_MODE_OFF) != WWWS_MODE_NATIVE)
        return 1.0f;
    f = WSF(WWWS_UI_FACTOR_OFF);
    return ws_factor_ok(f) ? f : 1.0f;
}

// ws_apply's table (section 5; stage 2 item 1, stage 3 item 4): each word
// gets a + b*X, X = the 2D factor F, or the HUD shift S on WSA_S rows.
// WSA_S16 rows are s16 HIO words, rounded half away from zero; the rest
// f32. At F = 1 and S = 0 every row gives back its vanilla value, so
// leaving native mode needs no restore. The address keeps its low 24 bits
// (all are 0x80xxxxxx); the type sits in the top byte.
#define WSA_S16 0x01000000
#define WSA_S   0x02000000
typedef struct { u32 at; f32 a, b; } WsApply;
static const WsApply ws_apply_tbl[] = {
    { 0x3F89B8, 320.5f, -329.5f },                // cutscene wipe box x (-9)
    { 0x3F89BC, 241.0f, -262.0f },                //                  y (-21)
    { 0x3F89C0, 0.0f, 659.0f },                   //                  w (659)
    { 0x3F89C4, 0.0f, 524.0f },                   //                  h (524)
    { 0x3FB78C, 320.5f, -329.5f },                // OVERLAP4/5 blit x0 (-9)
    { 0x3FB77C, 0.0f, 1.0296875f },               //   its x scale (0x3F83CCCD)
    { WSA_S16 | 0x3E68E8, 149.5f, -329.5f },      // minimap hide target (-180)
    { WSA_S16 | WSA_S | 0x3E68E4, 35.0f, -1.0f }, // minimap corner x (35)
    { WSA_S16 | WSA_S | 0x3E68F0, 590.0f, 1.0f }, // free-camera icon x (590)
    { WSA_S16 | WSA_S | 0x3E6B06, 0.0f, -1.0f },  // minigame timer icon x (g_menuHIO+0x9A, 0)
};

static void ws_apply(f32 F, f32 S) {
    const WsApply* e;
    for (e = ws_apply_tbl; e < ws_apply_tbl + sizeof(ws_apply_tbl) / sizeof(ws_apply_tbl[0]); e++) {
        f32 v = e->a + e->b * ((e->at & WSA_S) ? S : F);
        u32 a = 0x80000000u | (e->at & 0xFFFFFF);
        if (e->at & WSA_S16)
            *(volatile s16*)a = (s16)(v < 0.0f ? v - 0.5f : v + 0.5f);
        else
            *(volatile f32*)a = v;
    }
}

// Latch the 2D mode from the live one (at boot and when an EPOCH screen
// loads: section 3.3), then rewrite the data words to match. ui_shift is
// S = 329.5 * (F - 1) with WWWS_F_HUD, else 0.
static void ws_latch(void) {
    f32 F = 1.0f, S = 0.0f;
    u8 m = 0;
    if (ws_live_factor() != 1.0f && (WS8(WWWS_FLAGS_OFF) & WWWS_F_2D)) {
        m = WWWS_MODE_NATIVE;
        F = WSF(WWWS_FACTOR_OFF);
        if (WS8(WWWS_FLAGS_OFF) & WWWS_F_HUD)
            S = 329.5f * (F - 1.0f);
    }
    WS8(WWWS_UI_MODE_OFF) = m;
    WSF(WWWS_UI_FACTOR_OFF) = F;
    WSF(WWWS_UI_SHIFT_OFF) = S;
    ws_apply(F, S);
}

// --- the J2D screen walker (section 3.3) ---------------------------------

// The name J2DScreen::set passed to getGlbResource (hooks.c's hook A);
// ws_screen_loaded consumes it.
const char* ws_screen_name;

// J2DPane fields and calls (section 3.3; J2DPane.cpp).
#define PANE_MAGIC(p)   (*(u32*)((p) + 0x04))
#define PANE_TAG(p)     (*(u32*)((p) + 0x08))
#define PANE_BOX(p)     ((f32*)((p) + 0x0C))   // mBounds {i.x, i.y, f.x, f.y}
#define PANE_BASEPOS(p) (*(u8*)((p) + 0xA9))
#define PANE_KIDS(p)    (*(u8**)((p) + 0xB0))  // the first child link
#define LINK_PANE(l)    (*(u8**)((l) + 0x0))
#define LINK_NEXT(l)    (*(u8**)((l) + 0xC))
#define PIC1            0x50494331
extern void J2DPane_add(void* p, f32 dx, f32 dy);           // build.py GAME_SYMBOLS
extern void J2DPane_setBasePosition(void* p, int base);

// The walker compares a row's kind with the rule actions.
_Static_assert(WST_KIND_ALL_L == WST_ACT_L && WST_KIND_ALL_R == WST_ACT_R && WST_KIND_NONE == WST_ACT_NONE,
               "wstable_gen.h: the shift kinds must equal the L/R/NONE actions");

#define WST_NO_RULE 0xFF

// The walk's context: the row (0: none), its flags (WST_F_GEOM alone with
// no row: the global GEOM rule, section 6 lever 1), the anchor parent and
// the latched factor and shift.
static const WsRow* ws_row;
static u8 ws_flags;
static u8* ws_anchor;
static f32 ws_F, ws_S;

static u8 ws_rule(u32 tag) {
    int i;
    if (ws_row)
        for (i = ws_row->first; i < ws_row->first + ws_row->n; i++)
            if (WST_RULE_TAG[i] == tag)
                return WST_RULE_ACT[i];
    return WST_NO_RULE;
}

// An edge at absolute x, widened about 320.5 and made relative to the
// parent again.
static f32 ws_widen(f32 abs_x, f32 parent_x) {
    return 320.5f + (abs_x - 320.5f) * ws_F - parent_x;
}

// Walk pane's children; px, py are pane's absolute position (the screen
// is the origin; rotation and scale ignored). Shifts act on the anchor's
// direct children (their tag rule, else the row's kind) and on any pane
// with an L or R rule; a shifted pane is not walked into. GEOM and EDGE
// act at any depth (no row combines them with a shift).
static void ws_walk(u8* pane, f32 px, f32 py) {
    u8* l;
    for (l = PANE_KIDS(pane); l; l = LINK_NEXT(l)) {
        u8* c = LINK_PANE(l);
        f32* b = PANE_BOX(c);
        f32 x0 = px + b[0], y0 = py + b[1], x1 = px + b[2];
        u8 act = ws_rule(PANE_TAG(c));
        if (ws_S != 0.0f) {
            u8 sh = act;
            if (sh == WST_NO_RULE && pane == ws_anchor) {
                sh = ws_flags & WST_KIND_MASK;
                if (sh == WST_KIND_BY_CX)
                    sh = (x0 + x1) * 0.5f < 320.5f ? WST_ACT_L : WST_ACT_R;
            }
            if (sh == WST_ACT_L || sh == WST_ACT_R) {
                J2DPane_add(c, sh == WST_ACT_L ? -ws_S : ws_S, 0.0f);
                continue;
            }
        }
        if (act == WST_ACT_EDGE ||
            ((ws_flags & WST_F_GEOM) && PANE_MAGIC(c) == PIC1 && !PANE_KIDS(c) &&
             x0 <= 0.0f && x1 >= 640.0f && y0 <= 0.0f && py + b[3] >= 480.0f)) {
            if (x0 <= 0.0f)
                b[0] = ws_widen(x0, px);
            if (x1 >= 640.0f)
                b[2] = ws_widen(x1, px);
            J2DPane_setBasePosition(c, PANE_BASEPOS(c));
        }
        ws_walk(c, x0, y0);
    }
}

// Hook B's work after J2DScreen::set(stream) returned ok: look up the
// stashed name's row (none: GEOM alone), latch on EPOCH, then apply the
// row with the new latch. The stash is always cleared.
void ws_screen_loaded(u8* scrn, int ok) {
    const char* name = ws_screen_name;
    u32 h;
    u8* l;
    int i;
    ws_screen_name = 0;
    if (!name || !(u8)ok)
        return;
    h = ws_name_hash(name);
    ws_row = 0;
    ws_flags = WST_F_GEOM;
    for (i = 0; i < WST_ROWS; i++)
        if (WST_ROW[i].hash == h) {
            ws_row = &WST_ROW[i];
            ws_flags = ws_row->flags;
            break;
        }
    if (ws_flags & WST_F_EPOCH)
        ws_latch();
    ws_F = ws_2d_factor();
    if (ws_F == 1.0f)
        return;
    ws_S = WSF(WWWS_UI_SHIFT_OFF);
    if (!(ws_S >= 0.0f && ws_S <= 659.0f))
        ws_S = 0.0f;
    if (ws_S != 0.0f && (ws_flags & (WST_F_SCR_L | WST_F_SCR_R))) {
        // A scissored screen moves whole, its clip box with it.
        J2DPane_add(scrn, (ws_flags & WST_F_SCR_L) ? -ws_S : ws_S, 0.0f);
        return;
    }
    ws_anchor = scrn;
    for (l = PANE_KIDS(scrn); l; l = LINK_NEXT(l))
        if (PANE_TAG(LINK_PANE(l)) == WST_ROOT_TAG)
            ws_anchor = LINK_PANE(l);
    ws_walk(scrn, 0.0f, 0.0f);
}

// WS_SET: a = mode, b = factor bits, c = flags. The 2D follows at the
// next EPOCH screen (ws_latch), never here.
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
// Dolphin hack at 16:9), flags = what this build supports, the knob
// vanilla, the 2D latch off (ws_latch: mode 1). The usual soft reset to
// the title runs no init and keeps the block as it is.
void ws_init(void) {
    WS32(WWWS_MAGIC_OFF) = 0;   // never half valid
    WS8(WWWS_VERSION_OFF) = WWWS_VERSION;
    WS8(WWWS_MODE_OFF) = WWWS_MODE_CLIP;
    WS8(WWWS_MODE_OFF + 1) = 0;
    WS8(WWWS_MODE_OFF + 2) = 0;
    WSF(WWWS_FACTOR_OFF) = ws_f32(WWWS_FACTOR_DEFAULT);
    WS8(WWWS_FLAGS_OFF) = WWWS_FLAGS_SUPPORTED;
    WS8(WWWS_UI_MODE_OFF + 1) = 0;
    WS8(WWWS_UI_MODE_OFF + 2) = 0;
    ws_write_knob(WWWS_MODE_CLIP, 1.0f);
    ws_latch();
    WS32(WWWS_MAGIC_OFF) = WWWS_MAGIC;
    ww_cmd_register(WWAP_OP_WS_SET, ws_op_set);
}
