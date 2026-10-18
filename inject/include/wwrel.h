// The REL module's words in the mailbox slack (inject/rel/README.md).
// Shared by the module (inject/rel/rel_main.c, which writes them) and
// T2 (src/mp/relload.c, which reads the magic to know the module is
// linked). scripts/extract_blob.py reads the four #defines below into
// internal/inject/blob.go (RELMagic, RELMagicOff, RELBuildIDOff,
// RELFrameOff), so keep each on one line in this exact form.
//
// The Mailbox struct (mailbox_core.h) ends at MAILBOX_ADDR + 0x1B0 and
// __OSArenaLo is 0x80414000 (inject/build.py), so 0xF50 bytes past the
// struct belong to nobody; the module uses the first three words.
#ifndef WW_WWREL_H
#define WW_WWREL_H

#define WWREL_MAGIC        0x5757524C   // 'WWRL'
#define WWREL_MAGIC_OFF    0x1B0        // u32: WWREL_MAGIC while the module is linked
#define WWREL_BUILD_ID_OFF 0x1B4        // u32: WWREL_BUILD_ID of the linked module
#define WWREL_FRAME_OFF    0x1B8        // u32: +1 per execute of the module's actor

#endif
