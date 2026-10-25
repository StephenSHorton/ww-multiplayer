// visuals.h: the remote players' visuals drawn from the REL (visuals.c):
// sword, shield and scabbard on the mini-Links, their name tags and the
// sea chart markers. The slack contract is
// include/wwvis.h.
#ifndef WW_REL_VISUALS_H
#define WW_REL_VISUALS_H

#include "mailbox_core.h"

// T2's mp_draw_post calls this through WWVIS_DRAW_FN_OFF for every drawn
// mini-Link slot, twice: WWVIS_PHASE_PRE right before the slot's body
// entry and WWVIS_PHASE_POST right after it. model is the slot's J3DModel
// (its mpNodeMtx holds the remote's pose, world space), link is Link #1.
void wwvis_draw_hook(int slot, J3DModel* model, void* link, int phase);

// T2's seachart_shim calls this through WWVIS_SEACHART_FN_OFF from
// dDlst_FMAP_c::draw, after the sea chart's J2DScreen has drawn: fmap_dl
// is that dDlst_FMAP_c (dMenu_Fmap_c + 0x1C), graf the port it drew with.
void wwvis_seachart_draw(u8* fmap_dl, void* graf);

// _prolog: publish the function words (last); _epilog: clear them (first).
void wwvis_link(void);
void wwvis_unlink(void);

#endif
