// bosskill.h: the boss_kill relay op (bosskill.c), run by the keeper
// (rel_main.c). The request's layout is include/wwboss.h; design:
// ww-multiplayer-handoff/design/research/C3-C6-gohma.md, "The relay op
// (B5, revised)". Stateless: everything it keeps is the request's ack and
// result in the mailbox slack, so the keeper's actor layout is unchanged.
#ifndef WW_BOSSKILL_H
#define WW_BOSSKILL_H

#include "wwapi.h"
#include "wwboss.h"

// From the keeper's create: consume whatever request the slack holds
// (ack = seq, result WWBOSS_R_NONE) so a stale seq is never run.
void bosskill_link(void);

// Once per keeper frame, after the enemy mirror and remote-Link: run a
// new request, if there is one, and ack it.
void bosskill_frame(void);

#endif
