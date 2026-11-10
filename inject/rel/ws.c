// ws.c: the widescreen mod's REL side (docs/design/native-widescreen.md
// sections 3.7, 4.3 H-12 and 8). Reads the WWWS block (include/wwws.h)
// T2 latches; never writes it.
//
// Stage 2: relws_2d_factor for the name tags and the lobby line
// (visuals.c). Stage 3: relws_shift and the cannon game's widgets: the
// vtable slots of dDlst_2DObject_c::draw (the bomb and ship icons, and
// Sploosh Kaboom's squid icons) and dDlst_2DBattery_c::draw (the angle
// and shot battery) point at wrappers here while the module is linked.
// Each wrapper moves the widget's x by the latched shift S around the
// vanilla draw and puts it back; with S = 0 it is the vanilla draw.
#include "mailbox_core.h"
#include "wwws.h"
#include "ws.h"

#define WS_U8(o)  (*(volatile u8*)(MAILBOX_ADDR + WWWS_OFF + (o)))
#define WS_U32(o) (*(volatile u32*)(MAILBOX_ADDR + WWWS_OFF + (o)))
#define WS_F32(o) (*(volatile f32*)(MAILBOX_ADDR + WWWS_OFF + (o)))

f32 relws_2d_factor(void) {
    f32 f;
    if (WS_U32(WWWS_MAGIC_OFF) != WWWS_MAGIC || WS_U8(WWWS_UI_MODE_OFF) != WWWS_MODE_NATIVE)
        return 1.0f;
    f = WS_F32(WWWS_UI_FACTOR_OFF);
    return (f >= 1.0f && f <= 3.0f) ? f : 1.0f;
}

f32 relws_shift(void) {
    f32 s;
    if (relws_2d_factor() == 1.0f)
        return 0.0f;
    s = WS_F32(WWWS_UI_SHIFT_OFF);
    return (s >= 0.0f && s <= 659.0f) ? s : 0.0f;
}

// The vtable slots (vtable + 0xC, draw) and their vanilla targets, from
// the DOL (d_2dnumber.cpp; plan H-12).
#define SLOT_2DOBJECT   ((volatile u32*)0x80389100)
#define SLOT_2DBATTERY  ((volatile u32*)0x80389110)
#define DRAW_2DOBJECT   0x800C946C
#define DRAW_2DBATTERY  0x800C90F0
typedef void (*dl_draw_fn)(void* self);

// dDlst_2DObject_c (d_2dnumber.h): mPos.x at +0x0C, the top-left x. Left
// of the centre moves left, else right (bombs at 30, ships at 540).
void relws_obj_draw(u8* self) {
    f32 s = relws_shift();
    f32* x = (f32*)(self + 0x0C);
    f32 x0 = *x;
    if (s != 0.0f)
        *x = x0 + (x0 < 320.5f ? -s : s);
    ((dl_draw_fn)DRAW_2DOBJECT)(self);
    *x = x0;
}

// dDlst_2DBattery_c: mPicturePos.x at +0x28 and mNumPos.x at +0x30 (the
// widget sits at the right).
void relws_battery_draw(u8* self) {
    f32 s = relws_shift();
    f32* p = (f32*)(self + 0x28);
    f32* n = (f32*)(self + 0x30);
    f32 p0 = *p, n0 = *n;
    *p = p0 + s;
    *n = n0 + s;
    ((dl_draw_fn)DRAW_2DBATTERY)(self);
    *p = p0;
    *n = n0;
}

// Only a slot that holds the vanilla draw is taken, and only a slot that
// holds ours is given back.
void relws_link(void) {
    if (*SLOT_2DOBJECT == DRAW_2DOBJECT)
        *SLOT_2DOBJECT = (u32)relws_obj_draw;
    if (*SLOT_2DBATTERY == DRAW_2DBATTERY)
        *SLOT_2DBATTERY = (u32)relws_battery_draw;
}

void relws_unlink(void) {
    if (*SLOT_2DOBJECT == (u32)relws_obj_draw)
        *SLOT_2DOBJECT = DRAW_2DOBJECT;
    if (*SLOT_2DBATTERY == (u32)relws_battery_draw)
        *SLOT_2DBATTERY = DRAW_2DBATTERY;
}
