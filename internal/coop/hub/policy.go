package hub

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

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

// withoutSwitches returns img with every memory switch byte zeroed, and
// how many of them had bits: what a player without switch sync neither
// proposes nor is sent.
func withoutSwitches(img *world.Image) (world.Image, int) {
	out, n := *img, 0
	for t := 0; t < addr.TableCount; t++ {
		off := addr.OffMemBit(t) + addr.MemBitOffSwitch
		for i := off; i < off+addr.MemBitOffItem-addr.MemBitOffSwitch; i++ {
			if out[i] != 0 {
				out[i] = 0
				n++
			}
		}
	}
	return out, n
}

// dropSwitches returns gs without its memory switch bytes, and how many it
// dropped. gs itself is not changed.
func dropSwitches(gs []wire.Gain) ([]wire.Gain, int) {
	out := make([]wire.Gain, 0, len(gs))
	for _, g := range gs {
		if !IsSwitchByte(int(g.Off)) {
			out = append(out, g)
		}
	}
	return out, len(gs) - len(out)
}
