package inject

// The second hijacked profile: the remote-Link full body, a daPy_lk_c the
// same REL module creates under proc RELBodyProcID (design
// ww-multiplayer-handoff/design/remote-link-stage4-plan.md D4-1). Mirrors
// inject/include/wwremote.h (WWREMOTE_FULL_PROC, WWREMOTE_FULL_NAME_ENTRY,
// WWREMOTE_FULL_NAME_PTR; relbody_test.go pins them through
// wwapi/mailbox's copies). Hand-written, not generated: kept out of
// blob.go so the patcher's RELS.arc work compiles and tests before the
// REL that exports RELBodyProfileSymbol is rebuilt.
//
// Two static patches make proc 0x27 load module RELModuleID: inject/build.py
// rewrites the DynamicNameTable entry's name pointer (RELBodyNameEntryAddr
// + 4) from "d_a_seatag" to RELBodyNamePtr ("d_a_rectangle"), which
// extract_blob.py carries into DOLPatches, and the RELS.arc patcher moves
// the profile list's slot-0x27 relocation into the module's import, at
// RELBodyProfileSymbol. The two are only safe together.
const (
	RELBodyProcID        uint16 = 0x0027 // WWREMOTE_FULL_PROC (Seatag; never placed on the GZLE01 disc)
	RELBodyProfileSymbol        = "g_profile_WWBODY"
	RELBodyNameEntryAddr uint32 = 0x80339938 // WWREMOTE_FULL_NAME_ENTRY: {s16 0x0027, pad, char* name}
	RELBodyNamePtr       uint32 = 0x8033AE39 // WWREMOTE_FULL_NAME_PTR: "d_a_rectangle" (vanilla 0x8033A6F3 "d_a_seatag")
)
