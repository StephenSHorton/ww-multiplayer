// ws.c: the widescreen mod's REL side (docs/design/native-widescreen.md
// sections 3.7, 4.3 H-12 and 8). Reads the WWWS block (include/wwws.h)
// T2 latches; never writes it.
//
// Stage 2: relws_2d_factor for the name tags and the lobby line
// (visuals.c).
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
