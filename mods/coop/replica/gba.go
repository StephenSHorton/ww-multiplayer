package replica

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// The Tingle Tuner guard (§6.2, §6.3). The stage tables sync a switch a
// Tingle Tuner switch (agbsw0) shares with latching setters, since every
// Tuner write needs a Game Boy Advance linked on the frame it is made
// (tables/switches, AGBSW0). They mark it GBA-dependent
// (synctables.GBASwitch). While a GBA may be linked to this replica's
// game (state.GBALink.MayLink, read every tick), such a switch is neither
// proposed nor written:
//
//   - the memory switches leave the replica's policy, which the differ
//     (GAINS), the WORLD (a bind's too, and the join preflight), the
//     planner and the M4 receiver share, and the WriteGuard shuts them
//     (Tick.GBA), with the FrameWriter's recheck aborting a batch planned
//     before a link it didn't see;
//   - the dan, zone and room switches leave the scoped sender's filter
//     (ShareScopes) and the M5 receiver's (m5Due), where a dan or zone bit
//     waits in its store and a room edge is dropped, as edges are.
//
// A switch that rose while a GBA may have been linked is never proposed
// later (it may be the Tuner's):
//
//   - a memory switch's rise is in the differ's baseline, so it is never
//     a gain;
//   - a dan or zone bit that rose in Link's scope while held, or that a
//     scope entered while held already had, is tainted in the sharing
//     Differ (HoldScopes), which masks it out of every SCOPED, the whole
//     words of the scope refresh and of a room entry among them, until
//     the game clears it or the Differ drops its baselines;
//   - a room edge that rose while held is in the scoped baseline and is
//     never sent, as edges are.
//
// A Differ drop (the title, another file, a save state, an unreadable
// snapshot) forgets both, as it forgets every baseline: the WORLD after
// it carries a memory switch with the rest of the file, and the next
// whole dan or zone words a tainted bit. A bit the Tuner sets outside
// Link's scope (the zone of a room Link is not in) is not seen to rise:
// it is proposed when Link enters that room once no GBA may be linked.
// Nothing else changes: the hub keeps its policy, and a partner's game
// with no GBA proposes and writes these switches as usual.

// gbaGuard is the replica's Tingle Tuner guard state.
type gbaGuard struct {
	// full is the policy as configured; mask the memory switch bits of it
	// that are GBA-dependent. While held, the replica's policy is full
	// without mask.
	full, mask world.Policy
	// switches counts the GBA-dependent switches of the tables the
	// replica syncs: the memory switches the policy opens, and the dan,
	// zone and room switches. Non-zero, the FrameWriter rechecks the link
	// after the beat (WriterConfig.GBA).
	switches int
	held     bool // the last good snapshot said a GBA may be linked
	seen     bool // a snapshot was read
}

// newGBAGuard builds the guard for policy p (the replica's own copy) and
// tables t (nil: no switch is GBA-dependent).
func newGBAGuard(p *world.Policy, t *tables.Tables) gbaGuard {
	g := gbaGuard{full: *p}
	if t == nil || t.Switches == nil {
		return g
	}
	var m world.Policy
	m.GrantSwitches(func(table, no int) bool { return synctables.GBAMemorySwitch(t, table, no) })
	for off := range m {
		m[off] &= p[off]
	}
	g.mask = m
	for _, s := range t.Switches.Switches {
		if !s.Synced || !s.GBA {
			continue
		}
		if synctables.GBAMemorySwitch(t, s.Table, s.No) {
			if _, b, err := addr.SwitchBit(s.No); err == nil && addr.ValidTable(s.Table) && p[addr.OffMemBit(s.Table)+b.Off]&b.Mask != 0 {
				g.switches++
			}
		} else if synctables.GBAScopedSwitch(t, s.Table, s.Stage, s.Room, s.No) {
			g.switches++
		}
	}
	return g
}

// observeGBA takes this tick's GBA link (s is a good snapshot) and, when
// it changes, swaps the replica's policy and says so once. r.mu is held.
func (r *Replica) observeGBA(s *state.Snapshot) {
	g := &r.gba
	may := s.GBA.MayLink()
	first := !g.seen
	g.seen = true
	if may == g.held && !first {
		return
	}
	g.held = may
	if may {
		for off := range r.policy {
			r.policy[off] = g.full[off] &^ g.mask[off]
		}
		r.logf("co-op: a Game Boy Advance may be linked to this game (the Tingle Tuner; g_mDoGaC_gbaCom mEnable %d, context %d): "+
			"the %d switch(es) the stage tables share with Tingle Tuner switches are neither proposed nor written until the game drops the link (a reset, or calling Tingle again)",
			s.GBA.Enable, s.GBA.Context, g.switches)
		return
	}
	*r.policy = g.full
	if !first {
		r.logf("co-op: no Game Boy Advance is linked to this game any more: the %d switch(es) shared with Tingle Tuner switches sync again", g.switches)
	}
}

// gbaHeld reports whether a switch shared with the Tingle Tuner is held
// back this tick. r.mu is held.
func (r *Replica) gbaHeld() bool { return r.gba.held }

// scopedSynced is the verdict the scoped sender proposes by: the tables'
// (synctables.SyncedScopedSwitch), without the switches shared with
// the Tingle Tuner while a GBA may be linked. It runs under r.mu, in the
// differ.
func (r *Replica) scopedSynced(table int, stage string, room, no int) bool {
	t := r.cfg.Tables
	return synctables.SyncedScopedSwitch(t, table, stage, room, no) && !(r.gba.held && synctables.GBAScopedSwitch(t, table, stage, room, no))
}

// scopedHeld reports whether a scoped switch is held back this tick: it
// is shared with the Tingle Tuner and a GBA may be linked. The sharing
// Differ taints such a bit when it rises (HoldScopes). It runs under r.mu,
// in the differ.
func (r *Replica) scopedHeld(table int, stage string, room, no int) bool {
	return r.gba.held && synctables.GBAScopedSwitch(r.cfg.Tables, table, stage, room, no)
}

// GBAStatus is the Tingle Tuner guard's part of Status.
type GBAStatus struct {
	// MayLink: the last snapshot said a Game Boy Advance may be linked
	// (state.GBALink.MayLink), so the switches below are held back.
	MayLink bool
	// Switches counts the switches the stage tables share with Tingle
	// Tuner switches (synctables.GBASwitch) that this replica would
	// sync: held back while MayLink.
	Switches int
}
