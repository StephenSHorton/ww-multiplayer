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

// The latched HUD edge shift S: ui_shift (in [0, 659]) while
// relws_2d_factor() != 1, else 0.
f32 relws_shift(void);

// Stage 3 (H-12): _prolog points the minigames' 2D draw-list vtable
// slots at ws.c's wrappers (relws_link, before the exec hook word) and
// _epilog puts the vanilla draws back first thing (relws_unlink).
void relws_link(void);
void relws_unlink(void);

#endif
