package hub

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

// The hub's shared vocabulary lives in package proto, which the replica
// imports without the hub. These names keep the hub's API as it was.

// Feature is proto.Feature: one bit of the feature words of CO_HELLO,
// CO_WELCOME and CTRL COOP_OFF's arg (§4.4).
type Feature = proto.Feature

// The feature bits and sets (proto.FeatureImage and on).
const (
	FeatureImage        = proto.FeatureImage
	FeaturePuzzle       = proto.FeaturePuzzle
	FeatureCombat       = proto.FeatureCombat
	FeatureLiveSwitches = proto.FeatureLiveSwitches
	FeatureSwitches     = proto.FeatureSwitches
	FeatureStory        = proto.FeatureStory
	FeatureInventory    = proto.FeatureInventory
	FeatureKeys         = proto.FeatureKeys
	FeatureLife         = proto.FeatureLife
	FeatureMagic        = proto.FeatureMagic
	FeatureMerge        = proto.FeatureMerge
	FeatureBind         = proto.FeatureBind
	AskUnsaved          = proto.AskUnsaved
	AskBind             = proto.AskBind

	M4Features      = proto.M4Features
	SessionFeatures = proto.SessionFeatures
	DefaultFeatures = proto.DefaultFeatures
	TableFeatures   = proto.TableFeatures
)

// DefaultPolicy is proto.DefaultPolicy: what a hub's world accepts unless
// told otherwise (Config.Policy).
func DefaultPolicy() *world.Policy { return proto.DefaultPolicy() }

// SessionPolicy is proto.SessionPolicy: DefaultPolicy with the latching
// memory switches synced reports opened.
func SessionPolicy(synced func(t, no int) bool) *world.Policy {
	return proto.SessionPolicy(synced)
}

// SwitchPolicy is proto.SwitchPolicy: base with its memory switch bytes
// replaced by the M1 table's verdict.
func SwitchPolicy(base *world.Policy, synced func(table, no int) bool) *world.Policy {
	return proto.SwitchPolicy(base, synced)
}

// IsSwitchByte is proto.IsSwitchByte: whether image byte off holds memory
// switches.
func IsSwitchByte(off int) bool { return proto.IsSwitchByte(off) }

// ScopeFilter is proto.ScopeFilter: the scoped stores' filter over the
// host's M1 verdict for DAN, ZONE and ROOMEDGE (Config.SyncedScoped).
func ScopeFilter(synced func(table int, stage string, room, no int) bool) func(world.Key, int) uint32 {
	return proto.ScopeFilter(synced)
}

// KillFilter is proto.KillFilter: the scoped stores' filter for KILL keys
// over the host's verdict (Config.SyncedKill).
func KillFilter(synced func(table int, stage string, room, setID int) bool) func(world.Key, int) uint32 {
	return proto.KillFilter(synced)
}

// Filter is proto.Filter: a session's scoped stores' filter, ScopeFilter
// for the switches and KillFilter for the kills.
func Filter(scoped func(table int, stage string, room, no int) bool, kill func(table int, stage string, room, setID int) bool) func(world.Key, int) uint32 {
	return proto.Filter(scoped, kill)
}
