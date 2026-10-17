package hub

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// DefaultPolicy is what a hub's world accepts unless told otherwise
// (Config.Policy), from M4 on: world.DefaultPolicy, which takes every
// class-mask bit of the OR and MAX bytes (the stage progress bits, the
// ocean, charts and sea map, the inventory and collection flags, the
// capacities and the allowlisted event registers, §3) and nothing of the
// OR_MASKED ones, with the story bits opened (story.Policy, §6.5): the
// SyncMask, every named event bit but the LocalOnly, game-cleared (D8)
// and refight ones. So a figurine bitfield merges by OR, a letter, Beedle
// points or Ghost Ship register by the MAX of its field, and a register
// outside the allowlist, being PER_PLAYER, not at all. The memory switches
// stay shut until the host's M1 verdict opens them (SwitchPolicy), and the
// DERIVED bytes (small keys, max life and magic, item slots and
// equipment) never enter a world: each receiver derives its own from the
// flags. The policy is the hub's, not the world file's, and it only
// filters what comes in: a bit the world took stays (§4.4).
func DefaultPolicy() *world.Policy { return story.Policy(world.DefaultPolicy()) }

// SessionPolicy is the policy of a --coop session over the host's M1
// verdict synced (nil: no tables): DefaultPolicy with the latching memory
// switches synced reports opened, which is the host hub's own
// (Config.SyncedSwitch over a nil Config.Policy). A writing replica
// proposes and plans with it, so it proposes only what its host takes.
func SessionPolicy(synced func(t, no int) bool) *world.Policy {
	return SwitchPolicy(DefaultPolicy(), synced)
}

// Memory switch sync (§6.2-6.3, M3). The memory switches 0x00-0x7F of
// every save table are OR_MASKED image bytes, mMemory[t].mSwitch (bit n is
// mSwitch[n>>5] |= 1<<(n&31), dSv_memBit_c::onSwitch, d_save.cpp:
// 1084-1087). M2's default policy kept every one of them out of the world;
// from M3 a switch enters it only when the host's M1 switch table says
// every setter of it latches (switches.Table.Synced), and a player takes
// part in switch sync only when its stage tables are the host's
// (FeatureSwitches, TableFeatures).

// SwitchPolicy returns base with its memory switch bytes replaced by the
// M1 table's verdict: exactly the switches synced reports for (save table,
// switch number) are open, every other bit of those bytes is refused. A
// nil base is world.DefaultPolicy, a nil synced opens none. The hub builds
// its policy so (Config.SyncedSwitch), and a replica's planner can use
// the same policy to propose and plan only what the hub accepts.
func SwitchPolicy(base *world.Policy, synced func(table, no int) bool) *world.Policy {
	var p world.Policy
	if base != nil {
		p = *base
	} else {
		p = *world.DefaultPolicy()
	}
	for t := 0; t < addr.TableCount; t++ {
		off := addr.OffMemBit(t) + addr.MemBitOffSwitch
		clear(p[off : off+addr.MemBitOffItem-addr.MemBitOffSwitch])
	}
	if synced != nil {
		p.GrantSwitches(synced)
	}
	return &p
}

// IsSwitchByte reports whether image byte off holds memory switches: one
// of the 16 bytes of mMemory[t].mSwitch for some save table t
// (d_save.h:684-689).
func IsSwitchByte(off int) bool {
	r := off - addr.OffMemory
	if r < 0 || r >= addr.TableCount*addr.MemBitSize {
		return false
	}
	i := r % addr.MemBitSize
	return i >= addr.MemBitOffSwitch && i < addr.MemBitOffItem
}

// gated are the session features that carry image bytes of their own
// (gateOf).
const gated = FeatureSwitches | FeatureStory

// gateOf is the session feature image byte off needs beyond FeatureImage:
// FeatureSwitches for a memory switch byte, FeatureStory for a story bit
// byte (mEvent 0x00-0x41), 0 for every other byte. The hub neither takes
// such a byte from a player without its feature nor sends it one.
func gateOf(off int) Feature {
	switch {
	case IsSwitchByte(off):
		return FeatureSwitches
	case story.IsStoryByte(off):
		return FeatureStory
	}
	return 0
}

// strip returns img with every byte zeroed whose feature f lacks
// (gateOf), and how many of them had bits: what a player granted f
// neither proposes nor is sent.
func strip(img *world.Image, f Feature) (world.Image, int) {
	out, n := *img, 0
	if f&gated == gated {
		return out, 0
	}
	for off := range out {
		if g := gateOf(off); g != 0 && f&g == 0 && out[off] != 0 {
			out[off] = 0
			n++
		}
	}
	return out, n
}

// dropGated returns gs without the bytes whose feature f lacks, and how
// many it dropped. gs itself is not changed.
func dropGated(gs []wire.Gain, f Feature) ([]wire.Gain, int) {
	out := make([]wire.Gain, 0, len(gs))
	for _, g := range gs {
		if gt := gateOf(int(g.Off)); gt == 0 || f&gt != 0 {
			out = append(out, g)
		}
	}
	return out, len(gs) - len(out)
}
