package synctables

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
)

// What M6's kill union (§7.2, M6.0) reads of the enemy table: the verdict
// on the kill bits SCOPED carries, a room's dead-actor bits by setID. The
// host's hub keeps and relays only the bits this grants
// (hub.Config.SyncedKill), a writing replica's sender proposes only those
// (hub.Filter), and its receiver writes and light-shrinks only those
// (replica.Config.Tables), as SyncedScopedSwitch does for the switches.

// SyncedKill reports whether co-op may sync the death of setID in room of
// stage, as the zone actor bit dSv_info_c::onActor sets in save table
// table's game (d_save.cpp:1671-1682): a synced key of the enemy table
// (enemies.Table.KeyedK1: a Keyed K1 entry, not BOSS, Morth or Shared).
//
// False for a table outside 0..15, a room outside 0..63 (onActor's
// assert, d_save.cpp:1676), a setID of 0x200 or more (the same assert;
// 0xFFFF is never recorded, :1672-1674), and without an enemy table.
func SyncedKill(t *tables.Tables, table int, stage string, room, setID int) bool {
	if t == nil || t.Enemies == nil || !addr.ValidTable(table) || room < 0 || room >= addr.RoomCount ||
		setID < 0 || setID >= enemies.MaxSetID || stage == "" {
		return false
	}
	return t.Enemies.KeyedK1(stage, room, setID)
}

// KillMask is room's synced keys in stage, as SyncedKill grants them: the
// bits M6 may write into the room's zone actor words. Empty where
// SyncedKill is false for every setID.
func KillMask(t *tables.Tables, table int, stage string, room int) enemies.KillSet {
	if t == nil || t.Enemies == nil || !addr.ValidTable(table) || room < 0 || room >= addr.RoomCount || stage == "" {
		return enemies.KillSet{}
	}
	return t.Enemies.KillMask(stage, room)
}

// SyncedPlaced reports whether co-op may share a kill of the unkeyed
// enemy of placement ordinal ordinal in room of stage, in save table
// table's game (§7.2a, M6.1): a placed entry of the enemy table
// (enemies.Table.PlacedOK: an unkeyed K1 entry, not BOSS, Morth, a
// searchlight Bokoblin or a Miniblin with a byte-2 switch). Nothing
// records it; the hub relays it and the receiver light-shrinks one live
// instance.
//
// False for a table outside 0..15, a room outside 0..63, an ordinal
// outside 0..511 and without an enemy table.
func SyncedPlaced(t *tables.Tables, table int, stage string, room, ordinal int) bool {
	if t == nil || t.Enemies == nil || !addr.ValidTable(table) || room < 0 || room >= addr.RoomCount ||
		ordinal < 0 || ordinal >= enemies.MaxOrdinal || stage == "" {
		return false
	}
	return t.Enemies.PlacedOK(stage, room, ordinal)
}
