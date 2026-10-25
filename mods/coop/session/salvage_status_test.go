package coopsession

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
)

// TestSalvageStatus: /status's line for the salvage catch-up, which says
// nothing until it has marked or held a point.
func TestSalvageStatus(t *testing.T) {
	for _, c := range []struct {
		name string
		st   replica.Status
		want string
	}{
		{"idle", replica.Status{}, ""},
		{"dry run", replica.Status{DryRun: true, Salvage: replica.SalvageStatus{Marked: 1}}, ""},
		{"marked", replica.Status{Salvage: replica.SalvageStatus{Marked: 2}}, "salvage: 2 point(s) marked emptied, 0 held"},
		{"held", replica.Status{Salvage: replica.SalvageStatus{Marked: 1, Held: 1, LastHeld: "no daSalvage_c executes"}},
			"salvage: 1 point(s) marked emptied, 1 held (last: no daSalvage_c executes)"},
	} {
		if got := salvageStatus(c.st); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
