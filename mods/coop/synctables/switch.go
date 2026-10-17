package synctables

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/switches"
)

// What M3's memory switches and the Tingle Tuner guard read of the switch
// table: the verdict on a switch, by the key the game routes it to, and
// whether it syncs only while no Game Boy Advance is linked.

// SyncedSwitch reports whether co-op may sync switch no as an actor of
// save table table, stage stage and room room writes it: the switch
// table's verdict, a setter and every setter latching (§6.2), for the key
// the game routes it to (switches.KeyFor; a memory or dan switch ignores
// stage and room). False for what the game can't route, for a switch no
// placed actor writes, and without a switch table.
func SyncedSwitch(t *tables.Tables, table int, stage string, room, no int) bool {
	if t == nil || t.Switches == nil {
		return false
	}
	k, err := switches.KeyFor(table, stage, room, no)
	return err == nil && t.Switches.Synced(k)
}

// SyncedMemorySwitch reports whether co-op may sync memory switch no
// (0x00-0x7F, dSv_memBit_c::onSwitch, d_save.cpp:1084-1087) of save table
// table: SyncedSwitch for the memory store, the verdict the hub's policy
// and the replica's planner grant memory switches by (hub.SwitchPolicy).
func SyncedMemorySwitch(t *tables.Tables, table, no int) bool {
	return no >= 0 && no < addr.MemBitSwitchCount && SyncedSwitch(t, table, "", -1, no)
}

// GBASwitch reports whether switch no, as an actor of save table table,
// stage stage and room room writes it, syncs only while no Game Boy
// Advance is linked (switches.Switch.GBA): SyncedSwitch, and a Tingle
// Tuner writer (agbsw0) shares it, or an AND input of it is such. A
// replica neither proposes nor writes it while a GBA may be linked to its
// game (snap.GBALink.MayLink).
func GBASwitch(t *tables.Tables, table int, stage string, room, no int) bool {
	if t == nil || t.Switches == nil {
		return false
	}
	k, err := switches.KeyFor(table, stage, room, no)
	return err == nil && t.Switches.GBADependent(k)
}

// GBAMemorySwitch is GBASwitch for the memory switches SyncedMemorySwitch
// opens.
func GBAMemorySwitch(t *tables.Tables, table, no int) bool {
	return SyncedMemorySwitch(t, table, no) && GBASwitch(t, table, "", -1, no)
}
