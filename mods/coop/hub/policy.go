package hub

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

// The policies themselves (DefaultPolicy, SessionPolicy, SwitchPolicy,
// IsSwitchByte) are package proto's, shared with the replica; hub names
// them in proto.go. What follows is the hub's own use of them: the
// feature gate on image bytes.

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
