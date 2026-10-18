package wwapi

import (
	"slices"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// Features is what the patched blob provides, matched against each
// mod's "needs". The zero value means "not known": no mod gets
// NEEDS_REPATCH. The contract check (CheckContract) fills it in for a
// Game from the features word of the blob's WWMB header (FeaturesOf),
// and a game without a header provides nothing (a known, empty set), so
// every mod with needs gets NEEDS_REPATCH. ResolveOptions.Features takes
// it (a Game's Info().Features).
type Features struct {
	names []string
	known bool
}

// KnownFeatures returns a known feature set.
func KnownFeatures(names ...string) Features {
	n := slices.Clone(names)
	slices.Sort(n)
	return Features{names: slices.Compact(n), known: true}
}

// Known reports whether the set came from a blob.
func (f Features) Known() bool { return f.known }

// Has reports whether name is in a known set.
func (f Features) Has(name string) bool {
	_, ok := slices.BinarySearch(f.names, name)
	return ok
}

// Names returns the features, sorted.
func (f Features) Names() []string { return slices.Clone(f.names) }

// Missing returns the needs a known set lacks, sorted; nil when the set
// is not known.
func (f Features) Missing(needs []string) []string {
	if !f.known {
		return nil
	}
	var out []string
	for _, n := range needs {
		if !f.Has(n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (f Features) String() string {
	if !f.known {
		return "unknown"
	}
	return strings.Join(f.names, ",")
}

// featureBits names the WW_FEAT_* bits of the WWMB features word
// (inject/include/mailbox.h).
var featureBits = []struct {
	bit  uint32
	name string
}{
	{mailbox.WwFeatAvatar, "avatar"},
	{mailbox.WwFeatEyefix, "eyefix"},
	{mailbox.WwFeatFace, "face"},
	{mailbox.WwFeatInput, "input"},
	{mailbox.WwFeatTunic, "tunic"},
	{mailbox.WwFeatWarp, "warp"},
}

// FeaturesOf decodes a WWMB features word into a known set. A bit this
// build has no name for is named "bit<n>" (a newer blob's feature), so it
// still shows, and no mod of this build needs it.
func FeaturesOf(word uint32) Features {
	var names []string
	for _, f := range featureBits {
		if word&f.bit != 0 {
			names = append(names, f.name)
			word &^= f.bit
		}
	}
	for n := 0; word != 0; n++ {
		if word&1 != 0 {
			names = append(names, "bit"+strconv.Itoa(n))
		}
		word >>= 1
	}
	return KnownFeatures(names...)
}

// BlobFeatures returns the features this build's blob provides (blob.go's
// Features, what its main01_init writes into the header).
func BlobFeatures() Features { return FeaturesOf(inject.Features) }
