// refresh.h: the actor_refresh op (refresh.c), run by the keeper
// (rel_main.c) beside the boss_kill op. The request's layout is
// include/wwrefresh.h; design: ww-multiplayer-handoff/design/
// enemy-mirror-batch-d.json R-D4, R-D5, D4-D7, "actor_refresh".
// Stateless: everything it keeps is the request's ack and result in the
// mailbox slack, so the keeper's actor layout is unchanged. With
// WWREFRESH_ENABLE 0 both entry points compile to nothing.
#ifndef WW_REFRESH_H
#define WW_REFRESH_H

#include "wwapi.h"
#include "wwrefresh.h"

// From the keeper's create, beside bosskill_link: consume whatever request
// the block holds (ack = seq, result WWRF_R_NONE) so a stale seq is never
// run.
void refresh_link(void);

// Once per keeper frame, after bosskill_frame: run a new request, if there
// is one (steps 1-10 of R-D5: stage, boss stage, tag and allow-list,
// lookup, identity, re-derived bit, DOL re-read, generic gates, per-proc
// gate, fopAcM_deleteByID, and for a re-create class fopAcM_create in the
// actor's own layer), then write the result, the new pid and ack = seq.
void refresh_frame(void);

#endif
