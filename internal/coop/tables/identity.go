package tables

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/switches"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// identityDomain starts what Identity hashes, so the hash can't be taken
// for another FNV-1a over the same bytes (wire.ClassHash is one).
const identityDomain = "ww-multiplayer co-op stage tables\x00"

// Identity is the tables' identity, which a co-op replica sends as
// CO_HELLO's stageTblHash and the host's hub compares with its own (§4.4,
// §6.2): FNV-1a 64, as wire.ClassHash, over the schema and every section
// in the JSON the cache holds, leaving out the binary that built them
// (Builder) and the disc they were read from (ID).
//
// So two discs whose stages give the same tables have the same identity
// whatever their main.dol: a vanilla disc and its patched copy, an ISO and
// its CISO. Other stage data (a modded disc), or a build whose builders
// decide or explain anything differently, gives another; the hub then
// turns switch sync off for that player (hub.TableFeatures).
//
// It is never 0, which CO_HELLO keeps for "no tables". It fails for tables
// that lack a section, and for tables that don't encode as JSON (a NaN
// position in a damaged stage file), which can't be cached either.
func (t *Tables) Identity() (uint64, error) {
	if t == nil || !t.complete() {
		return 0, errors.New("tables: only whole tables have an identity")
	}
	c := *t
	c.Builder, c.ID = "", ID{}
	b, err := json.Marshal(&c)
	if err != nil {
		return 0, fmt.Errorf("tables: %w", err)
	}
	h := fnv.New64a()
	h.Write([]byte(identityDomain))
	h.Write(b)
	if id := h.Sum64(); id != 0 {
		return id, nil
	}
	return 1, nil
}

// SyncedSwitch reports whether co-op may sync switch no as an actor of
// save table table, stage stage and room room writes it: the switch
// table's verdict, a setter and every setter latching (§6.2), for the key
// the game routes it to (switches.KeyFor; a memory or dan switch ignores
// stage and room). False for what the game can't route, for a switch no
// placed actor writes, and without a switch table.
func (t *Tables) SyncedSwitch(table int, stage string, room, no int) bool {
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
func (t *Tables) SyncedMemorySwitch(table, no int) bool {
	return no >= 0 && no < addr.MemBitSwitchCount && t.SyncedSwitch(table, "", -1, no)
}

// GBASwitch reports whether switch no, as an actor of save table table,
// stage stage and room room writes it, syncs only while no Game Boy
// Advance is linked (switches.Switch.GBA): SyncedSwitch, and a Tingle
// Tuner writer (agbsw0) shares it, or an AND input of it is such. A
// replica neither proposes nor writes it while a GBA may be linked to its
// game (snap.GBALink.MayLink).
func (t *Tables) GBASwitch(table int, stage string, room, no int) bool {
	if t == nil || t.Switches == nil {
		return false
	}
	k, err := switches.KeyFor(table, stage, room, no)
	return err == nil && t.Switches.GBADependent(k)
}

// GBAMemorySwitch is GBASwitch for the memory switches SyncedMemorySwitch
// opens.
func (t *Tables) GBAMemorySwitch(table, no int) bool {
	return t.SyncedMemorySwitch(table, no) && t.GBASwitch(table, "", -1, no)
}
