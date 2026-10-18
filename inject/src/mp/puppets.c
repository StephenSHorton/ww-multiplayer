// Wind Waker Multiplayer — the multiplayer mod's actor puppets (src/mp;
// Phase E3 split multiplayer.c into src/core and src/mp). The KAMOME /
// NPC_OB1 stand-ins draw every remote beyond the mini-Link slots
// (docs/api/00-design.md decision 5).

#include "mp.h"

// Per-slot state. Parallel arrays to mailbox.puppets[]. pids[i] is the
// queued-spawn result from fopAcM_create; spawned[i] gates phase 2 sync.
static fpc_ProcID puppet_pids[MAX_PUPPETS];
static int puppet_spawned[MAX_PUPPETS];
// The Link actor the actor puppets were last checked against (see
// puppet_reap).
static fopAc_ac_c* puppet_owner = 0;

// Actor-puppet lifetime. Left alone, a puppet whose slot Go cleared
// lives on. Live, one left behind on Outset was gone after a stage
// change, but one spawned on the title screen outlived title -> file
// select -> the save's stage, where KAMOME's path_move read its spawn
// room's freed path (+0x304 mpPath, d_a_kamome.cpp:482) and the game
// halted ("Invalid read from 0x1a0d0013", PC 0x80f24230; docs/05). So
// we delete it ourselves: a slot's actor when Go clears the slot, and
// every actor when a stage change starts (NEXT_STAGE_ENABLE), when Link
// is gone (title, file select) or when Link's actor changes. A delete
// that can't happen yet (still being created) is retried every frame
// until it does; only then is the slot forgotten.
static void puppet_reap(int all) {
    int i;
    for (i = 0; i < MAX_PUPPETS; i++) {
        if (!puppet_spawned[i] || (!all && mailbox->puppets[i].active)) continue;
        fpc_ProcID pid = puppet_pids[i];
        if (fpcM_IsCreating(pid) || !fopAcM_deleteByID(pid)) continue;
        puppet_spawned[i] = 0;
        puppet_pids[i] = fpcM_ERROR_PROCESS_ID_e;
        mailbox->puppets[i].actor_ptr = 0;
    }
}

// mods.h: every frame, Link or not. Actor puppets: see puppet_reap.
void mp_frame_any(fopAc_ac_c* link) {
    int stage_changing = NEXT_STAGE_ENABLE;
    puppet_reap(!link || link != puppet_owner || stage_changing);
    puppet_owner = link;
}

// One frame of the actor puppets, from mp_frame. Iterates the
// MAX_PUPPETS slots in the mailbox. For
// each slot whose `active` flag is set by Go, we either queue a spawn
// (phase 1) or sync pos/rot from the slot into the live actor (phase 2).
//
// Progress encodes "best state across slots this frame":
//    1 = entered, no active slot yet (written by multiplayer_update
//        during its boot gate)
//    3 = frame_count gate passed (~10 sec since boot)
//    5 = at least one spawn queued this frame
//    6 = a spawn returned ERROR_PROCESS_ID (queue full or proc not
//        registered)
//    8 = at least one slot is now spawned=1
//    9 = at least one live actor resolved and synced
//   10 = at least one slot's actor has been deleted (cleared spawn)
//
// Proxy-actor history (what's inside fopAcM_create for each slot):
//   PROC_PLAYER     -> OOMs GameHeap (~704 KB alloc vs ~245 KB free).
//   PROC_Obj_Barrel -> queued OK but froze game on next frame (archive
//                      not resident on Outset; actor loader spins).
//   PROC_GRASS      -> spawns and renders stably, but child sprites are
//                      baked at parent's birth pos.
//   PROC_TSUBO pm=0 -> "Always" archive. Needs m678=2 unhide poke.
//   PROC_KAMOME pm=0 -> "Always"-ish archive (resident on every island).
//                      Needs mSwitchNo=0 unhide poke. *Current default.*
//   PROC_NPC_FA1    -> fairy; renders briefly, self-deletes; respawn
//                      loops froze the game. Not pursued.
u32 puppets_frame(fopAc_ac_c* link) {
    int stage_changing = NEXT_STAGE_ENABLE;

    cXyz* link_pos = ACTOR_POS(link);
    csXyz* link_angle = ACTOR_ANGLE(link);
    s8 link_room = ACTOR_ROOM(link);

    u32 best_progress = 3;

    int i;
    for (i = 0; i < MAX_PUPPETS; i++) {
        volatile Puppet* slot = &mailbox->puppets[i];

        // Go cleared the slot: puppet_reap deleted its actor. No spawn
        // or sync while a stage change is under way either (the reap
        // deleted those actors; they respawn in the new stage).
        if (!slot->active || stage_changing) continue;

        // Phase 1: spawn on the first frame this slot is active. Seed the
        // slot's target pos with Link's current pos + a small offset (for
        // visibility before Go writes real coords), one slot to the side
        // per index so multiple puppets don't overlap on spawn.
        if (!puppet_spawned[i]) {
            slot->pos_x = link_pos->x + (f32)(100 + i * 50);
            slot->pos_y = link_pos->y;
            slot->pos_z = link_pos->z;

            // 2026-04-19 EXPERIMENT: slot 3 was PROC_TSUBO. Parked on
            // PROC_KAMOME for now so our probe J3DModel (built from the
            // same Tsubo J3DModelData) is the ONLY consumer of that data
            // — isolates the shared-ModelData confound in the sky-break
            // investigation. Revert alongside PROBE_ARCNAME when done.
            s16 proc;
            if (i == 1) proc = PROC_NPC_OB1;
            else        proc = PROC_KAMOME;
            fpc_ProcID pid = fopAcM_create(proc, 0, link_pos, link_room, link_angle, 0, -1, 0);
            if (pid == fpcM_ERROR_PROCESS_ID_e) {
                if (best_progress < 6) best_progress = 6;
                continue;
            }
            puppet_pids[i] = pid;
            puppet_spawned[i] = 1;
            slot->actor_ptr = (u32)pid;  // published as pid until phase 2 resolves
            if (best_progress < 8) best_progress = 8;
            continue;
        }

        // Phase 2: resolve pid to actor and write pos/rot.
        fopAc_ac_c* actor = 0;
        if (!fopAcM_SearchByID(puppet_pids[i], &actor) || !actor) {
            // Still being created: SearchByID can't see it yet. Wait;
            // forgetting its pid here would leak the actor.
            if (fpcM_IsCreating(puppet_pids[i])) continue;
            // Actor was deleted externally (or its create failed). Drop
            // our state; the next frame requests a fresh spawn.
            puppet_spawned[i] = 0;
            puppet_pids[i] = fpcM_ERROR_PROCESS_ID_e;
            slot->actor_ptr = 0;
            if (best_progress < 10) best_progress = 10;
            continue;
        }

        slot->actor_ptr = (u32)actor;

        cXyz* pos = ACTOR_POS(actor);
        pos->x = slot->pos_x;
        pos->y = slot->pos_y;
        pos->z = slot->pos_z;

        // Write rotation to both current.angle (AI/physics) and
        // shape_angle (visual). See rotation-sync notes in docs/06.
        csXyz* shape = ACTOR_SHAPE(actor);
        csXyz* angle = ACTOR_ANGLE(actor);
        shape->x = angle->x = slot->rot_x;
        shape->y = angle->y = slot->rot_y;
        shape->z = angle->z = slot->rot_z;

        if (best_progress < 9) best_progress = 9;
    }

    return best_progress;
}
