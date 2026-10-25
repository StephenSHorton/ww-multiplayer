// boats.h: the other players' King of Red Lions, drawn by the REL
// (boats.c). The slack contract is include/wwboat.h.
#ifndef WW_REL_BOATS_H
#define WW_REL_BOATS_H

#include "mailbox_core.h"

// _prolog: forget every boat (a new link owns no heap yet).
void boats_link(void);

// The keeper's execute, after fx_frame: build, free and pose the boats.
void boats_frame(void);

// The keeper's draw: enter the posed boats into the draw lists.
void boats_draw(void);

// The keeper's delete and _epilog: destroy every boat heap.
void boats_unlink(void);

#endif
