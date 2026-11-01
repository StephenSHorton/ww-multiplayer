package proto

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The kill union (§7.2, M6.0). SCOPED KILL carries a zone's dead-actor
// bits, the setIDs dSv_info_c::onActor has recorded dead in a room
// (d_save.cpp:1671-1682): bit b of word w is setID 32w+b
// (dSv_zoneActor_c::on, d_save.cpp:1326-1329). The scoped stores keep them
// per (stage, table, room) while anyone is in the stage, and relay them to
// the partners there, so a death one player's game records reaches the
// other's zone and the enemy there, if it lives, is light-shrunk.
//
// Only the keys of the host's M1 verdict travel (hub.Config.SyncedKill,
// synctables.SyncedKill): a keyed K1 enemy's, not BOSS, Morth or
// Shared. A writing replica's sender proposes only those, with the same
// filter (Filter), and its receiver writes only those its own tables
// grant, so an old hub that relays KILL unfiltered (Appendix C item 113)
// can't make it write a refused bit. A nil verdict keeps no kill bit:
// combat fails closed.

// KillFilter returns the scoped stores' filter for KILL keys over the
// host's verdict synced: the bits of word w of store k whose setID synced
// grants for k's save table, stage and room. A nil synced grants none, and
// so does a word outside 0..15, a table outside 0..15 or a room outside
// 0..63, on which onActor asserts (d_save.cpp:1676); synced is not asked
// about those. A key of another kind keeps nothing.
func KillFilter(synced func(table int, stage string, room, setID int) bool) func(world.Key, int) uint32 {
	return func(k world.Key, w int) uint32 {
		if k.Kind != world.ScopeKill || synced == nil || w < 0 || w >= addr.ZoneActorCount/32 ||
			!addr.ValidTable(k.Table) || k.Room < 0 || k.Room >= addr.RoomCount || k.Stage == "" {
			return 0
		}
		var keep uint32
		for b := range 32 {
			if synced(k.Table, k.Stage, k.Room, 32*w+b) {
				keep |= 1 << b
			}
		}
		return keep
	}
}

// PlacedFilter returns the scoped stores' filter for PKILL keys (§7.2a,
// M6.1) over the host's verdict placed: the bits of word w of store k
// whose placement ordinal placed grants for k's save table, stage and
// room. A nil placed grants none, and so does a word outside 0..15, a
// table outside 0..15 or a room outside 0..63. A key of another kind
// keeps nothing.
func PlacedFilter(placed func(table int, stage string, room, ordinal int) bool) func(world.Key, int) uint32 {
	return func(k world.Key, w int) uint32 {
		if k.Kind != world.ScopePlaced || placed == nil || w < 0 || w >= addr.ZoneActorCount/32 ||
			!addr.ValidTable(k.Table) || k.Room < 0 || k.Room >= addr.RoomCount || k.Stage == "" {
			return 0
		}
		var keep uint32
		for b := range 32 {
			if placed(k.Table, k.Stage, k.Room, 32*w+b) {
				keep |= 1 << b
			}
		}
		return keep
	}
}

// Filter is the scoped stores' filter of a session: the switches of
// scoped's verdict for DAN, ZONE and ROOMEDGE (ScopeFilter), the kills of
// kill's for KILL (KillFilter), and the placements of placed's for PKILL
// (PlacedFilter). The host's hub keeps exactly what it passes
// (hub.Config.SyncedScoped, SyncedKill, SyncedPlaced), and a writing
// replica's sender proposes exactly that over its own tables, which are
// the host's when it is granted the features (TableFeatures).
func Filter(scoped func(table int, stage string, room, no int) bool, kill, placed func(table int, stage string, room, id int) bool) func(world.Key, int) uint32 {
	sw, kf, pf := ScopeFilter(scoped), KillFilter(kill), PlacedFilter(placed)
	return func(k world.Key, w int) uint32 {
		switch k.Kind {
		case world.ScopeKill:
			return kf(k, w)
		case world.ScopePlaced:
			return pf(k, w)
		}
		return sw(k, w)
	}
}
