package main

import (
	"slices"
	"testing"
)

// patch's --fix-sea-heap stands anywhere among the paths and is taken
// out of them once; without it there are no fixes.
func TestPatchFlags(t *testing.T) {
	for _, c := range []struct {
		args, rest []string
		fixes      int
	}{
		{[]string{"in.iso"}, []string{"in.iso"}, 0},
		{[]string{"in.iso", "out.iso"}, []string{"in.iso", "out.iso"}, 0},
		{[]string{"--fix-sea-heap", "in.iso", "out.iso"}, []string{"in.iso", "out.iso"}, 1},
		{[]string{"in.iso", "--fix-sea-heap"}, []string{"in.iso"}, 1},
		{[]string{"in.iso", "--fix-sea-heap", "out.iso", "--fix-sea-heap"}, []string{"in.iso", "out.iso"}, 1},
		{[]string{"--fix-sea-heap"}, nil, 1},
	} {
		rest, fixes := patchFlags(c.args)
		if !slices.Equal(rest, c.rest) || len(fixes) != c.fixes {
			t.Errorf("%q: %q and %d fixes, want %q and %d", c.args, rest, len(fixes), c.rest, c.fixes)
		}
		for _, f := range fixes {
			if f.Name != "sea heap fix" {
				t.Errorf("%q: fix %q", c.args, f.Name)
			}
		}
	}
}
