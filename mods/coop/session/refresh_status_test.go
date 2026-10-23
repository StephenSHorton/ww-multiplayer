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
		{"per store and Tier 3", replica.Status{Combat: granted, Refresh: replica.RefreshStatus{Landed: 3,
			ByKind: map[replica.LandKind]int{replica.LandSwitch: 2, replica.LandTbox: 1},
			Actors: replica.ActorRefreshStatus{On: "stone2,tbox", Candidates: 1, Requests: 2, Applied: 1, Seq: 9, Result: "applied", Held: 1, LastHeld: "Link within 300 units"}}},
			"refresh: 3 landed bit(s) this visit (2 switch, 1 tbox); 0 lock(s) cleared, 0 held; 0 death-switch light-shrink(s), 0 held; actor_refresh (stone2,tbox): 1 candidate(s), 2 request(s), 1 applied, last 9 applied, 1 held (Link within 300 units)"},
		{"Tier 3 dry run", replica.Status{Combat: granted, Refresh: replica.RefreshStatus{Landed: 1, ByKind: map[replica.LandKind]int{replica.LandSwitch: 1},
			Actors: replica.ActorRefreshStatus{On: "stone2,dry", Dry: true, Candidates: 1}}},
			"refresh: 1 landed bit(s) this visit (1 switch); 0 lock(s) cleared, 0 held; 0 death-switch light-shrink(s), 0 held; actor_refresh (stone2,dry, dry run): 1 candidate(s), 0 request(s), 0 applied, 0 held"},
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
