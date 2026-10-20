// The REL module's words in the mailbox slack (inject/rel/README.md).
// Shared by the module (inject/rel/rel_main.c, which writes them) and
// T2 (src/mp/relload.c, which reads the magic to know the module is
// linked). scripts/extract_blob.py reads the four #defines below into
// internal/inject/blob.go (RELMagic, RELMagicOff, RELBuildIDOff,
// RELFrameOff), so keep each on one line in this exact form.
//
// The Mailbox struct (mailbox_core.h) ends at MAILBOX_ADDR + 0x1B0 and
// __OSArenaLo is 0x80414000 (inject/build.py), so 0xF50 bytes past the
// struct belong to nobody; the module uses the first six words. T2's
// exec_shim (the bl fpcMtd_Execute site in fopAc_Execute) reads the
// fourth, WWREL_EXEC_HOOK_OFF, and branches to it when non-zero (remote-
// Link, ww-multiplayer-handoff/design/remote-link-design.md sections 4
// and 6.1): _prolog writes it last, _epilog clears it first, Go never
// writes it, and the region probe never arms a canary over it
// (wwapi/mailbox CanaryArena ends before it). The sixth, WWREL_SHANM_OFF,
// holds the remote-Link full bodies' shanm table (an archive-heap block
// allocated once per boot, never freed; ww-multiplayer-handoff/design/
// remote-link-stage4-plan.md D4-4): T2's main01_init zeroes it, the module
// sets it at the first full create and never clears it (not even in
// _epilog), and it is never canary-armed either. wwapi/mailbox/remote.go
// mirrors the last three; extract_blob.py reads only the first four defines.
#ifndef WW_WWREL_H
#define WW_WWREL_H

#define WWREL_MAGIC        0x5757524C   // 'WWRL'
#define WWREL_MAGIC_OFF    0x1B0        // u32: WWREL_MAGIC while the module is linked
#define WWREL_BUILD_ID_OFF 0x1B4        // u32: WWREL_BUILD_ID of the linked module
#define WWREL_FRAME_OFF    0x1B8        // u32: +1 per execute of the module's actor
#define WWREL_EXEC_HOOK_OFF 0x1BC       // u32: &wwrel_exec_hook (ABI of fpcMtd_Execute) while linked, else 0
#define WWREL_SWAP_STATE_OFF 0x1C0      // u32: the keeper's swap state (actor + WWREMOTE_KEEPER_SWAP_OFF), else 0
#define WWREL_SHANM_OFF    0x1C4        // u32: the archive-heap shanm table (WWREMOTE_SHANM_MAGIC), once per boot; T2 main01_init zeroes it, _epilog never clears it

#endif
