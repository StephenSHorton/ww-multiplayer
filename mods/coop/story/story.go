// Package story is the story-bit sync of §6.5 and the event register
// allowlist of §3, as pure functions over the canonical image (no memory
// access; the replica reads and writes the game):
//
//   - the hub's policy: exactly class.SyncMask of the event bit bytes
//     enters the world (Policy);
//   - the sender's Batcher: the story bits and registers its game gains
//     go out as one batch once the game has been IDLE for BatchIdle, so a
//     cutscene's side-effect bits travel together, and a memory switch
//     waits for a batch too (one rising alone starts one), so a
//     cutscene's trigger switch goes with that cutscene's bits;
//   - the receiver's plan: the world's story bits its game lacks, minus
//     its ever-had history and the bits class.Holds keeps back until their
//     condition holds in its own game, plus the allowlisted registers it
//     lacks (Pending);
//   - when the receiver writes them before M7: only on the first tick of
//     each stage change (Transition, Receiver), so the next stage's rooms
//     and actors read them as they are created;
//   - the register apply rules: a figurine bitfield by OR, a MAX register
//     by the larger field value with the byte's other bits kept local
//     (ApplyRegister).
//
// Image offsets are addr.Off* offsets into dSv_save_c; an event byte i is
// image offset addr.OffEvent+i. Decomp citations are zeldaret/tww at
// 4d78c994, as in wwapi/addr.
package story

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Policy returns base (nil: world.DefaultPolicy) with its event bit bytes
// set to exactly the SyncMask: every story bit that syncs is accepted
// into the world, and no other bit of mEvent bytes 0x00-0x41. The register
// bytes keep base's verdict (the default policy accepts the allowlist's
// class masks). The hub builds its policy so, and a replica's planner can
// use the same one to propose and plan only what the hub accepts.
func Policy(base *world.Policy) *world.Policy {
	var p world.Policy
	if base != nil {
		p = *base
	} else {
		p = *world.DefaultPolicy()
	}
	for i := range addr.EventBitBytes {
		p[addr.OffEvent+i] = class.SyncMaskOf(i)
	}
	return &p
}

// IsStoryByte reports whether image byte off is an event bit byte
// (mEvent 0x00-0x41).
func IsStoryByte(off int) bool {
	return off >= addr.OffEvent && off < addr.OffEvent+addr.EventBitBytes
}

// IsRegisterByte reports whether image byte off is an allowlisted event
// register: one of mEvent 0x79-0xFF whose class merges (§3 "Event
// registers"). The other registers are each player's own.
func IsRegisterByte(off int) bool {
	return off >= addr.OffEvent+addr.EventRegFirst && off < addr.OffEvent+addr.EventSize &&
		class.Of(off).Class.Merges()
}

// isSwitchByte reports whether image byte off holds memory switches: the
// 16 bytes of mMemory[t].mSwitch of some save table t (d_save.h:684-689).
func isSwitchByte(off int) bool {
	r := off - addr.OffMemory
	if r < 0 || r >= addr.TableCount*addr.MemBitSize {
		return false
	}
	i := r % addr.MemBitSize
	return i >= addr.MemBitOffSwitch && i < addr.MemBitOffItem
}

// gainsOf lists img's non-zero bytes as gains, in ascending offset order.
func gainsOf(img *save.Image) []world.Gain {
	var gs []world.Gain
	for off, v := range img {
		if v != 0 {
			gs = append(gs, world.Gain{Off: uint16(off), Val: v})
		}
	}
	return gs
}
