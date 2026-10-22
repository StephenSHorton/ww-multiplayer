package feed

import (
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// The roster is what PuppetSync tells the rest of the process about the
// remote players it draws: which pose-feed (mini-Link) slot each one holds
// and where its mini-Link stands. The enemy host pipeline
// (mods/enemysync/players.go, remote-link-design.md §7.2) reads it to put
// each remote's body where its mini-Link is: an enemy-sync connection
// receives no positions or poses, and nothing in the game's RAM maps a
// pose slot to a player.
//
// A roster is per (player name, Dolphin PID) and process-wide (RosterFor),
// so the pipelines of one session find each other however they were
// started (host/join, the app's Start, mp-local) without a shared Env:
// each passes the same name and PID to both. PID 0 is "the Dolphin the
// environment selects" for both, which is the same Dolphin.

// RemoteView is one remote player as PuppetSync last drew it.
type RemoteView struct {
	ID       byte   // the server's player id
	Name     string // the remote's player name
	LinkSlot int    // its pose-feed slot (0..avatar.Cap-1), -1 while it holds none
	// Pos is where its mini-Link stands: the lerp-smoothed position the
	// pose is re-applied at, plus WW_LINK2_OFFSET (feedPose). RotY is the
	// remote's raw current angle.y (moveActor writes it unsmoothed too).
	Pos [3]float32
	// Target is where the remote's last position puts it (unsmoothed,
	// plus the same offset): Pos closes on it every tick, so a big gap is
	// the smoother still crossing a jump (a stage change, a warp, a
	// void-out).
	Target [3]float32
	RotY   int16
	// Live is false while its pose stream is frozen or despawned
	// (network.PoseLiveness): the mini-Link holds a stale pose; and while
	// the stage gate hides it (partner.go: it is in another stage).
	Live bool
}

// Roster is one session's published RemoteViews. It is safe for
// concurrent use: PuppetSync publishes, other pipelines read.
type Roster struct {
	mu    sync.Mutex
	views []RemoteView
	at    time.Time
}

// Publish replaces the views (nil: no remotes, as when PuppetSync stops)
// as of now.
func (r *Roster) Publish(views []RemoteView, now time.Time) {
	r.mu.Lock()
	r.views, r.at = views, now
	r.mu.Unlock()
}

// Snapshot returns a copy of the views and when they were published (the
// zero time before the first Publish).
func (r *Roster) Snapshot() ([]RemoteView, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RemoteView(nil), r.views...), r.at
}

type rosterKey struct {
	name string
	pid  uint32
}

var (
	rostersMu sync.Mutex
	rosters   = map[rosterKey]*Roster{}
)

// RosterFor returns the roster of the session that runs as name on
// Dolphin pid, creating it on first use. Rosters are never dropped: a
// process runs a handful of sessions at most.
func RosterFor(name string, pid uint32) *Roster {
	rostersMu.Lock()
	defer rostersMu.Unlock()
	k := rosterKey{name, pid}
	r := rosters[k]
	if r == nil {
		r = &Roster{}
		rosters[k] = r
	}
	return r
}

// Views is the Feeder's RemoteViews after a Feed of remotes at now: every
// remote it holds an actor or link slot for, with the smoothed position
// moveActor keeps for it.
func (f *Feeder) Views(remotes []network.RemotePlayer, now time.Time) []RemoteView {
	var out []RemoteView
	for i := range remotes {
		rp := &remotes[i]
		idx, ok := f.remoteToSlot[rp.ID]
		if !ok || rp.Position == nil || !f.slots[idx].haveCur {
			continue
		}
		st := &f.slots[idx]
		v := RemoteView{ID: rp.ID, Name: rp.Name, LinkSlot: -1, RotY: rp.Position.RotY,
			Pos:    [3]float32{st.curX + f.offX, st.curY + f.offY, st.curZ + f.offZ},
			Target: [3]float32{rp.Position.PosX + f.offX, rp.Position.PosY + f.offY, rp.Position.PosZ + f.offZ}}
		if ls, ok := f.remoteToLinkSlot[rp.ID]; ok {
			v.LinkSlot = ls
			frozen, despawned := network.PoseLiveness(rp.LastPose, now, f.freezeAfter, f.despawnAfter)
			sl := &f.linkSlots[ls]
			v.Live = !frozen && !despawned && !sl.frozen && !sl.despawned && !sl.hidden
		}
		out = append(out, v)
	}
	return out
}
