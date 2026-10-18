// The core's own declarations (Phase E3, docs/api/00-design.md §6.6):
// the hooks in hooks.c and the services in services.c that only the
// hooks call. The services a mod calls are in wwapi.h; the mods the
// hooks call are in mods.h.
#ifndef WW_CORE_H
#define WW_CORE_H

#include "wwapi.h"

// hooks.c: the per-frame entry frame_shim calls through CALLBACK_PTR_ADDR.
void multiplayer_update(void);

// services.c, called by multiplayer_update:
//   warp_frame  — the warp handler (mailbox warp_seq / warp_ack / warp_force)
//   tunic_alloc — allocates the three tunic images once Go set their size
void warp_frame(fopAc_ac_c* link);
void tunic_alloc(void);

// services.c, called by daPy_draw_hook:
//   face_publish_local — Link #1's btp face state into face_state_local
//   pose_publish       — Link #1's mpNodeMtx into the pose publish buffer
void face_publish_local(void);
void pose_publish(void* link);

#endif
