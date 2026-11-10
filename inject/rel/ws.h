// ws.h: the widescreen mod's REL side (ws.c; docs/design/
// native-widescreen.md sections 3.7 and 8). The REL can't call T2, so it
// reads the WWWS block (include/wwws.h) with its own copy of T2's
// ws_2d_factor rule.
#ifndef WW_REL_WS_H
#define WW_REL_WS_H

#include "mailbox_core.h"

// The latched 2D factor F: ui_factor when the block's magic matches,
// ui_mode is 2 and 1 <= ui_factor <= 3, else 1.0 (T2's ws_2d_factor).
f32 relws_2d_factor(void);

#endif
