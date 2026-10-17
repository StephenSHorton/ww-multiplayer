package wwapi

import (
	"slices"
	"strings"
)

// Features is what the patched blob provides, matched against each
// mod's "needs". The zero value means "not known": no mod gets
// NEEDS_REPATCH. The contract check (CheckContract) fills it in for a
// Game: before E1 with the fixed PreE1Features. The mod runtime is not
// handed it yet (D4 wires Game into Options.Resolve).
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
