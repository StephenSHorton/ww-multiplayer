package coopsession

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
)

// TestRefreshStatus: /status's line for the stale-actor refresh, which
// says nothing until a bit landed or a target was held, and nothing in a
// dry run or without combat.
func TestRefreshStatus(t *testing.T) {
	granted := replica.CombatStatus{Granted: true, Tables: true}
	busy := replica.RefreshStatus{Landed: 3, Locks: 1, LockHeld: 1, LockLastHeld: "Link within 300 units", Shrunk: 2, Held: 1, LastHeld: "claimed: the enemy mirror's host owns its death"}
	for _, c := range []struct {
		name string
		st   replica.Status
		want string
	}{
		{"busy", replica.Status{Combat: granted, Refresh: busy},
			"refresh: 3 landed bit(s) this visit; 1 lock(s) cleared, 1 held (Link within 300 units); 2 death-switch light-shrink(s), 1 held (claimed: the enemy mirror's host owns its death)"},
		{"landed only", replica.Status{Combat: granted, Refresh: replica.RefreshStatus{Landed: 1}},
			"refresh: 1 landed bit(s) this visit; 0 lock(s) cleared, 0 held; 0 death-switch light-shrink(s), 0 held"},
		{"nothing", replica.Status{Combat: granted}, ""},
		{"not granted", replica.Status{Refresh: busy}, ""},
		{"dry run", replica.Status{DryRun: true, Combat: granted, Refresh: busy}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := refreshStatus(c.st); got != c.want {
				t.Errorf("got\n\t%s\nwant\n\t%s", got, c.want)
			}
		})
	}
}
