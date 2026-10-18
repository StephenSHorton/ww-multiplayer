// Wind Waker Multiplayer — keep the REL module linked (src/mp).
//
// The mod's REL module (inject/rel, design: ww-multiplayer-handoff
// design/rel-module-plan.md) is RELS.arc's d_a_rectangle.rel, the actor
// module of proc 0xB5 (PROC_RECTANGLE: an unused dummy actor that does
// nothing, d_a_rectangle.cpp). The game's own linker loads a module when
// the first actor of its proc is created and unlinks it when the last one
// is deleted, which every stage transition does. So T2 keeps one actor
// of proc 0xB5 alive per stage: the module's _prolog writes WWREL_MAGIC
// into the mailbox slack (wwrel.h), its _epilog clears it, and this unit
// creates an actor whenever the magic is not there.
//
// Observed live (2026-10-18, a stage-hop from Outset to A_mori): the
// actor is created from the frame hook, outside any actor's execute, so
// it lands in the layer current there, which the stage load does not
// tear down (inferred: the game's root layer), and the module stayed
// linked straight through the load with its frame counter running. The
// stage-change handling below is the fallback for a game that does
// delete it; it costs nothing when the magic stays put.
//
// It is safe on a disc whose RELS.arc was not patched: the vanilla
// d_a_rectangle actor is created instead, allocates nothing, draws
// nothing and never writes the magic. REL_MAX_TRIES bounds the creates
// per stage so such a disc does not spawn one dummy a second forever;
// Go reads the magic (wwapi.Info) and says the module is missing.
#include "mp.h"
#include "wwrel.h"

#define REL_WORD(off) (*(volatile u32*)(MAILBOX_ADDR + (off)))

// Frames between creates: the DVD read and the link take a few frames,
// and a create right after a stage load would race the stage's own
// actor creates (the actor puppets wait too, puppets.c).
#define REL_COOLDOWN_FRAMES 60
// Creates per stage before giving up until the next stage change.
#define REL_MAX_TRIES 3

static fpc_ProcID rel_pid = fpcM_ERROR_PROCESS_ID_e;
static int rel_spawned = 0;
static u32 rel_cooldown = REL_COOLDOWN_FRAMES;
static u32 rel_tries = 0;
static int rel_inited = 0;

// mods.h: every frame once Link exists and the boot gate has passed.
void rel_frame(fopAc_ac_c* link) {
    if (!rel_inited) {
        // The slack is orphan memory nothing zeroes for us; a stale
        // magic from a save state or an earlier boot must not look like
        // a linked module.
        REL_WORD(WWREL_MAGIC_OFF) = 0;
        REL_WORD(WWREL_BUILD_ID_OFF) = 0;
        REL_WORD(WWREL_FRAME_OFF) = 0;
        rel_inited = 1;
    }

    // A stage change deletes every actor, ours included (the module
    // unlinks and its _epilog clears the magic). Start over once the new
    // stage has settled.
    if (NEXT_STAGE_ENABLE) {
        rel_spawned = 0;
        rel_pid = fpcM_ERROR_PROCESS_ID_e;
        rel_tries = 0;
        rel_cooldown = REL_COOLDOWN_FRAMES;
        return;
    }

    // Linked: nothing to do.
    if (REL_WORD(WWREL_MAGIC_OFF) == WWREL_MAGIC) return;

    if (rel_spawned) {
        fopAc_ac_c* actor = 0;
        if (fpcM_IsCreating(rel_pid)) return;               // loading / linking
        if (fopAcM_SearchByID(rel_pid, &actor) && actor) {
            // The actor lives and never wrote the magic: the disc's
            // d_a_rectangle.rel is the vanilla dummy, not our module.
            // Leave it be until the next stage.
            return;
        }
        // Gone (its create failed, or something deleted it): retry.
        rel_spawned = 0;
        rel_pid = fpcM_ERROR_PROCESS_ID_e;
    }

    if (rel_cooldown) {
        rel_cooldown--;
        return;
    }
    if (rel_tries >= REL_MAX_TRIES) return;

    cXyz* link_pos = ACTOR_POS(link);
    fpc_ProcID pid = fopAcM_create(PROC_RECTANGLE, 0, link_pos, ACTOR_ROOM(link), ACTOR_ANGLE(link), 0, -1, 0);
    rel_cooldown = REL_COOLDOWN_FRAMES;
    rel_tries++;
    if (pid == fpcM_ERROR_PROCESS_ID_e) return;              // queue full: try again after the cooldown
    rel_pid = pid;
    rel_spawned = 1;
}
