package enemysync

import "sync"

// ClaimSet is the local pids the enemy client has claimed for the mirror
// (design death_and_drops.k1, D8): every local actor matched to a
// host-listed Ident, adopted or not, plus every dead-word catch-up. Co-op's
// K1 (mods/coop/combat) must not light-shrink a claimed pid: the host owns
// its death. One ClaimSet per game handle (the client's Dolphin), never a
// package global: the session wiring creates it, passes it to the client
// in Env.Claims and its Claimed method to that game's co-op replica as a
// combat.ClaimFunc.
//
// The client replaces the set once per tick; K1 reads it from its own
// goroutine.
type ClaimSet struct {
	mu   sync.RWMutex
	pids map[uint32]bool
}

// NewClaimSet returns an empty set.
func NewClaimSet() *ClaimSet { return &ClaimSet{pids: map[uint32]bool{}} }

// Replace makes pids the whole claimed set.
func (c *ClaimSet) Replace(pids []uint32) {
	if c == nil {
		return
	}
	m := make(map[uint32]bool, len(pids))
	for _, p := range pids {
		if p != 0 {
			m[p] = true
		}
	}
	c.mu.Lock()
	c.pids = m
	c.mu.Unlock()
}

// Claimed reports whether pid is claimed. A nil set claims nothing.
func (c *ClaimSet) Claimed(pid uint32) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pids[pid]
}

// ZoneEchoAllowed reports whether the client may OR keyed setID's
// dead-actor bit into its own zone for home room room of stage stage
// (design death_and_drops.k1 (a), quiet_steps[6]): only when host, the
// last STATE the client took, is of that stage and room and its Dead
// words already have the bit. The client never ORs a bit the host's
// dead[] lacks, so co-op's M6 on the host (gated only by combat.Eligible,
// mods/coop/replica reconcile_m6.go) never sees a client kill of a host
// actor that is still alive: the echo is a no-op by construction. False
// for a nil STATE, an unkeyed setID and setIDs past the Dead words (128
// and up: the host does not send them).
func ZoneEchoAllowed(host *State, stage string, room int8, setID uint16) bool {
	return host != nil && host.Stage == stage && host.Room == room && host.IsDead(setID)
}
