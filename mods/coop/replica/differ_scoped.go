package replica

import (
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The scoped sender of M5's live room puzzles (§6.3, §9 M5).
//
// M2's Differ sends every rising edge of the scoped words and nothing
// else. That is not enough to share a room's puzzles. The host's hub
// keeps a store only while a player is in its scope (world.Scopes), so a
// switch a player set while alone in a room, or in an earlier visit, or
// before a reconnect, is in its game and in no store when the partner
// arrives, and an edge is never sent again. And the hub takes only the
// latching switches of its M1 verdict (hub.Config.SyncedScoped), so an
// edge it refuses would be refused for nothing.
//
// A sharing Differ (ShareScopes) therefore:
//
//   - proposes only the bits the host's verdict takes, through the hub's
//     own filter (hub.ScopeFilter), as a writing replica's planner
//     proposes only what hub.SessionPolicy opens. A refused bit is in the
//     baseline all the same, so it is never proposed again;
//   - proposes the dan words whole on entering a stage (a new dan
//     baseline: another stage name or save table, d_save.cpp:1225-1235),
//     and the current room's zone switch words whole on entering the room
//     or taking its zone anew (another room or zone slot, createZone,
//     d_save.cpp:1525-1537): what the game holds for the scope it just
//     entered, which the partner's game lacks if it was not there to see
//     it set. Those words only latch (the verdict), and only Link's own
//     scope is read (state.Snapshot.ZoneFor), so nothing stale or cleared
//     goes;
//   - proposes both again every scopeRefresh ticks (one PRESENCE period),
//     for a store the hub dropped or never got: an entry tick the replica
//     did not send on (not armed, no world yet), a host restart, a
//     reconnect. The hub relays only what a store lacks, so a refresh
//     costs one SCOPED of each kind and no relay;
//   - never proposes a dan or zone bit HoldScopes held back as it rose,
//     or that its scope had when entered while held: the switches shared
//     with the Tingle Tuner while a GBA may be linked (gba.go). Such a
//     bit is tainted, and masked out of the whole words too, until the
//     game clears it or the Differ drops its baselines (Drop);
//   - sends the room switches 0xE0-0xEF and the kill words as edges only,
//     never whole: a room switch is applied on the edge and never
//     replayed (§6.3, Appendix C item 2), and the kills are M6's (§7.2).
//
// A Differ that does not share sends M2's edges, unfiltered.

// scopeRefresh is how many ticks apart a sharing Differ proposes the dan
// and zone words whole: one PRESENCE period.
const scopeRefresh = int(PresenceEvery / TickEvery)

// ShareScopes makes d a sharing Differ, which proposes the scoped bits
// keep leaves, of world.Key k and word w, and the latching dan and zone
// words whole on scope entry and every scopeRefresh ticks
// (differ_scoped.go). keep is the host's filter, hub.ScopeFilter over the
// replica's own M1 verdict (synctables.SyncedScopedSwitch), which is
// the host's when the hub grants FeaturePuzzle. Nil turns sharing off:
// M2's unfiltered edges.
func (d *Differ) ShareScopes(keep func(k world.Key, w int) uint32) {
	d.share = keep
}

// HoldScopes gives a sharing Differ the scoped bits it holds back this
// tick, of world.Key k and word w (nil: none): the replica's switches
// shared with the Tingle Tuner while a GBA may be linked (gba.go). keep
// leaves them out already; HoldScopes makes a dan or zone bit that rises
// while held (or that a scope entered while held has) stay out after,
// out of the whole words too, where keep alone would let the next
// refresh carry it.
func (d *Differ) HoldScopes(hold func(k world.Key, w int) uint32) {
	d.hold = hold
}

// taintRises taints the bits of a dan or zone scope's words that rose
// from was to cur while hold holds them back, and untaints those cur
// lacks (the game cleared them, or a new zone).
func (d *Differ) taintRises(kind wire.ScopeKind, s *state.Snapshot, room int8, was, cur [2]uint32) {
	if d.share == nil || d.hold == nil {
		return
	}
	k := worldKeyOf(kind, s, room)
	t, had := d.taint[k]
	for i := range t {
		t[i] &= cur[i]
		if r := cur[i] &^ was[i]; r != 0 {
			t[i] |= r & d.hold(k, i)
		}
	}
	switch {
	case t != [2]uint32{}:
		if d.taint == nil {
			d.taint = map[world.Key][2]uint32{}
		}
		d.taint[k] = t
	case had:
		delete(d.taint, k)
	}
}

// worldKeyOf is the world.Key of the words of kind in room of s's stage
// (-1 for DAN).
func worldKeyOf(kind wire.ScopeKind, s *state.Snapshot, room int8) world.Key {
	return world.Key{Kind: world.ScopeKind(kind), Stage: s.StageName, Table: int(s.Table), Room: int(room)}
}

// due reports whether a sharing Differ proposes the whole words this
// tick: on entering the stage, and every scopeRefresh ticks after.
func (sc *scopeBase) due(entered bool) bool {
	if entered || sc.refresh <= 1 {
		sc.refresh = scopeRefresh
		return true
	}
	sc.refresh--
	return false
}

// shared returns the bits of ws, words of kind in room of s's stage
// (-1 for DAN), that a sharing Differ proposes: those the filter keeps,
// and no tainted one (HoldScopes).
// ws itself is reused.
func (d *Differ) shared(kind wire.ScopeKind, s *state.Snapshot, room int8, ws []wire.ScopedWord) []wire.ScopedWord {
	if d.share == nil || len(ws) == 0 {
		return ws
	}
	k := worldKeyOf(kind, s, room)
	t := d.taint[k]
	out := ws[:0]
	for _, w := range ws {
		if w.Bits &= d.share(k, int(w.Word)); int(w.Word) < len(t) {
			w.Bits &^= t[w.Word]
		}
		if w.Bits != 0 {
			out = append(out, w)
		}
	}
	return out
}
