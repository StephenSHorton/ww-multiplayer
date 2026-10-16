package hub

import "github.com/StephenSHorton/ww-multiplayer/internal/coop/world"

// Live room puzzles (§6.3, M5). SCOPED carries the stage switches the
// image does not: a dungeon's dan switches 0x80-0xBF (dSv_danBit_c, kept
// per save table, d_save.cpp:1225-1241), a room's zone switches 0xC0-0xDF
// and its one-shot room switches 0xE0-0xEF (dSv_zoneBit_c,
// d_save.cpp:1273-1282). A bit of them that the scoped stores take is
// relayed to every partner in the scope and sent to each one entering it,
// and co-op never clears a bit, so, as for the memory switches (M3), only
// a switch every writer of which latches may travel (§6.2): a timed
// crystal clears its switch again when its timer runs out
// (daSwhit0_c::actionOnTimer, d_a_swhit0.cpp:346-362), and a partner's
// game would keep it set. The host's hub keeps exactly the bits of its M1
// verdict (Config.SyncedScoped), and a writing replica's sender proposes
// exactly those, with the same filter (ScopeFilter), so the hub refuses
// nothing of an honest replica's and nothing refused is proposed again
// every tick.

// ScopeFilter returns the scoped stores' filter (world.Scopes.Filter)
// over the host's M1 verdict synced (Config.SyncedScoped,
// tables.Tables.SyncedScopedSwitch): the bits of word w of store k whose
// switch synced grants for k's save table, stage and room. A nil synced
// grants none. It is world.SwitchFilter, so whatever synced says a bit is
// kept only if its switch number is one the game routes to k's store
// (0x80-0xBF for DAN, 0xC0-0xDF for ZONE, 0xE0-0xEF for ROOMEDGE), never
// one of 0xF0-0xFE or one of a room outside 0..63, on which the game's
// setters halt (§3.1; the JUT_ASSERTs of dSv_info_c::onSwitch,
// d_save.cpp:1541-1556, live in GZLE01, JUTAssert.h:9), and synced is not
// asked about those. Every kill bit passes: KILL is not a switch, and a
// session's filter judges it with KillFilter (Filter, combat.go).
//
// The hub builds its filter so, and a writing replica's sender filters its
// proposals with ScopeFilter of its own tables' verdict, which is the
// host's when it is granted FeaturePuzzle (TableFeatures): the hub then
// takes all it proposes, as SessionPolicy does for the memory switches.
func ScopeFilter(synced func(table int, stage string, room, no int) bool) func(world.Key, int) uint32 {
	return world.SwitchFilter(func(k world.Key, no int) bool {
		return synced != nil && synced(k.Table, k.Stage, k.Room, no)
	})
}
