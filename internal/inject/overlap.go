package inject

import (
	"cmp"
	"fmt"
	"slices"
)

// Span is a byte range [Start, End) of one file the patcher writes or
// must leave alone: "main.dol" (file offsets in the DOL) or a REL by
// its RELS.arc path (file offsets in the expanded REL). Owner names the
// patch that writes it, or what a protected range holds.
type Span struct {
	File       string
	Start, End uint64
	Owner      string
}

// WholeFile is the End of a Span over all of a file: a REL the patcher
// replaces or rebuilds rather than edits byte by byte.
const WholeFile = ^uint64(0)

func (s Span) String() string {
	if s.End == WholeFile {
		return fmt.Sprintf("%s (all of %s)", s.Owner, s.File)
	}
	return fmt.Sprintf("%s (%s 0x%x..0x%x)", s.Owner, s.File, s.Start, s.End)
}

func (s Span) overlaps(o Span) bool {
	return s.File == o.File && s.Start < o.End && o.Start < s.End
}

// CheckOverlaps refuses a patch set in which a write lands in a
// protected span of its file, or two writes by different owners overlap
// in one file (one would silently win). An empty span (Start >= End) is
// an error too. The DOL and the RELS.arc entries are checked alike: the
// WW Online patcher's validator (adapted from datstarkey/ww-online
// WWOnline.Patcher/Patches/GamePatchApplier.cs Validate, MIT) checks
// patches against each other and against protected main.dol ranges
// only; this one also covers REL writes.
func CheckOverlaps(writes, protected []Span) error {
	for _, w := range writes {
		if w.Start >= w.End {
			return fmt.Errorf("patch: %s is empty", w)
		}
		for _, p := range protected {
			if w.overlaps(p) {
				return fmt.Errorf("patch: %s writes inside %s", w, p)
			}
		}
	}
	sorted := slices.Clone(writes)
	slices.SortStableFunc(sorted, func(a, b Span) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start))
	})
	for i := 1; i < len(sorted); i++ {
		for j := i - 1; j >= 0 && sorted[j].File == sorted[i].File; j-- {
			if sorted[j].overlaps(sorted[i]) && sorted[j].Owner != sorted[i].Owner {
				return fmt.Errorf("patch: %s and %s write the same bytes", sorted[j], sorted[i])
			}
		}
	}
	return nil
}
