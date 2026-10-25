// fx.h: other players' bombs, boat-cannon shots and arrows made real in
// this world (fx.c). The slack contract is include/wwfx.h.
#ifndef WW_REL_FX_H
#define WW_REL_FX_H

#include "mailbox_core.h"

// _prolog: empty the trackers and the copy table for this link.
void fx_link(void);

// The keeper's execute, once per frame after refresh_frame: report the
// local Link's projectiles (send magic set), spawn and hold the peers'
// (recv magic set).
void fx_frame(void);

// enemy_mirror.c's Armos Knight swallow (em_am_swallow_client), on a live
// bomb it is about to delete: 1 when it is a copy of a peer's bomb (its
// params carry WWFX_BOMB_PRM_COPY), which is then counted and must not be
// reported to the host (that bomb was the sender's, and its own world
// relays whatever it hit there). A tripwire: a copy's BombCheck flag
// (fx.c) already makes the swallow refuse it, so this should never count.
int fx_swallowed_copy(void* bomb);

#endif
