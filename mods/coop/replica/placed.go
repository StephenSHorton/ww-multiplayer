package replica

import (
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
)

// M6.1, placement-matched kills of unkeyed enemies (docs/08 §7.2a). No
// game records the death of an unkeyed enemy (setID 0xFFFF,
// d_save.cpp:1671-1674), so M6.0's zone diff never sees one. Instead:
//
//   - the sender tracks the matched, eligible unkeyed K1 enemies of Link's
//     room while the partner is in it, latches each one's death signature
//     (health <= 0, or a nonzero light-shrink timer; a freeze latches
//     "frozen", which is never sent, and a thaw clears both), and, when a
//     latched track is gone from a complete walk with the stage, table and
//     room unchanged, sends its placement ordinal as SCOPED PKILL. The
//     per-class signatures of the plan (dr actions, the Stalfos split,
//     the Armos Knight's 44-47 and the rest) stay off until the live check
//     P1 confirms each, so a class whose death never lowers its health
//     (the Armos Knight, d_a_am.cpp:1217-1218) sends nothing yet;
//   - the receiver keeps each PKILL bit for Link's stage, table and room,
//     with the partner there, as a pending kill with a cutoff (the process
//     ID counter, addr.ProcessIDCounter, when it is first seen: an enemy
//     created after the kill is never taken) and a TTL, and light-shrinks
//     the lowest-pid live member of the ordinal's interchangeable class
//     below the cutoff, through M6.0's gates, Revalidate and the
//     WriteGuard (CombatTick.Allow asks the tick's placed targets).
//
// Both need proto.FeaturePlacedKill and, since they ride on M6.0's walk
// and applier, proto.FeatureCombat. A mirror copy or a pid the enemy
// mirror claims is neither sent nor taken: the mirror's host owns it.

const (
	// placedTTL is how many ticks a pending kill waits for a target
	// (inferred: about 10 s at TickEvery).
	placedTTL = 100
	// maxPlacedPending bounds the pending kills.
	maxPlacedPending = 64
	// maxPlacedTracks bounds the sender's tracks.
	maxPlacedTracks = actors.MaxNodes
)

// placedTrack is one tracked enemy of the sender.
type placedTrack struct {
	stage   wire.Stage
	table   int8
	room    int8
	class   []int // its interchangeable class's ordinals
	dying   bool  // health <= 0 or the timer was seen
	frozen  bool  // ice mode 2 or 3 was seen, with no thaw since
	missing int   // complete walks in a row that did not see it
}

// placedPending is one partner kill the receiver waits to apply.
type placedPending struct {
	stage   wire.Stage
	table   int8
	room    int8
	ordinal int
	cutoff  uint32 // 0 until the next tick reads the counter
	ttl     int
	seen    bool   // a member below the cutoff was seen
	target  uint32 // the pid planned this tick, 0 for none
}

// placedState is the replica's M6.1 state, in r.m6.
type placedState struct {
	tracks  map[uint32]*placedTrack
	pending []*placedPending
	// remote are the pids this game light-shrank for a partner's PKILL:
	// their vanishing is never sent back (echo).
	remote map[uint32]bool
	// targets are this tick's planned pids (CombatTick.Allow).
	targets map[uint32]*placedPending
	// out is what the sender sends at the end of the tick.
	out []wire.Scoped

	toldOn bool

	// What /status shows (PlacedStatus).
	sent, taken, gone, expired, dropped, refused int
	unsent                                       map[string]int
}

func newPlacedState() placedState {
	return placedState{tracks: map[uint32]*placedTrack{}, remote: map[uint32]bool{}, targets: map[uint32]*placedPending{}, unsent: map[string]int{}}
}

// forget drops what a save-state load makes stale: every pid.
func (p *placedState) forget() {
	clear(p.tracks)
	clear(p.remote)
	clear(p.targets)
	for _, k := range p.pending {
		k.cutoff, k.seen, k.target = 0, false, 0
	}
}

// PlacedStatus is how M6.1 stands (CombatStatus.Placed), for /status.
type PlacedStatus struct {
	// Granted: the hub grants proto.FeaturePlacedKill (and combat).
	Granted bool
	// Tracked counts the enemies the sender tracks, Sent the PKILL bits
	// sent, Unsent the gone tracks not sent, by reason: nosig (no death
	// signature), frozen, offroom (Link left its room), room (its room
	// not settled), stage (stage or table changed), echo (one this game
	// shrank for the partner).
	Tracked, Sent int
	Unsent        map[string]int
	// Pending counts the partner's kills waiting for a target, Taken those
	// light-shrunk, Gone those dropped because no member was left alive
	// (a simultaneous kill), Expired those whose TTL ran out, Dropped those
	// dropped by a stage, table or room change or the partner leaving, and
	// Refused the bits this side's tables do not place.
	Pending, Taken, Gone, Expired, Dropped, Refused int
}

func (r *Replica) placedStatus() PlacedStatus {
	p := &r.m6.pl
	u := make(map[string]int, len(p.unsent))
	for k, v := range p.unsent {
		u[k] = v
	}
	return PlacedStatus{Granted: r.placedGranted(), Tracked: len(p.tracks), Sent: p.sent, Unsent: u,
		Pending: len(p.pending), Taken: p.taken, Gone: p.gone, Expired: p.expired, Dropped: p.dropped, Refused: p.refused}
}

// placedGranted reports whether M6.1 runs: the hub grants placed-kill and
// combat, and this side has an enemy table.
func (r *Replica) placedGranted() bool {
	return r.features&proto.FeaturePlacedKill != 0 && r.features&proto.FeatureCombat != 0 &&
		r.cfg.Tables != nil && r.cfg.Tables.Enemies != nil && !r.writer.DryRun()
}

// placedRoom is (stage, room)'s placements by this side's tables.
func (r *Replica) placedRoom(stage string, room int) *enemies.PlacedRoom {
	if r.cfg.Tables == nil || r.cfg.Tables.Enemies == nil {
		return nil
	}
	return r.cfg.Tables.Enemies.PlacedIn(stage, room)
}

// partnerInRoom reports whether the partner is in game in (stage, table,
// room).
func (r *Replica) partnerInRoom(st wire.Stage, table, room int8) bool {
	p := r.partner
	return p != nil && p.Flags&wire.PresenceInGame != 0 && p.Stage == st && p.Table == table && p.Room == room
}

// onPlaced takes a partner's PKILL (onScoped): each bit for Link's room
// becomes a pending kill. r.mu is held.
func (r *Replica) onPlaced(m *wire.Scoped) {
	p := &r.m6.pl
	if !r.placedGranted() {
		return
	}
	pr := r.placedRoom(m.Stage.String(), int(m.Room))
	for _, w := range m.Words {
		for b := range 32 {
			if w.Bits&(1<<b) == 0 {
				continue
			}
			ord := 32*int(w.Word) + b
			if !synctables.SyncedPlaced(r.cfg.Tables, int(m.Table), m.Stage.String(), int(m.Room), ord) {
				p.refused++
				continue
			}
			if _, ok := pr.At(ord); !ok {
				p.refused++
				continue
			}
			if len(p.pending) >= maxPlacedPending {
				p.dropped++
				continue
			}
			p.pending = append(p.pending, &placedPending{stage: m.Stage, table: m.Table, room: m.Room, ordinal: ord, ttl: placedTTL})
		}
	}
}

// placedSig reads a's death signature: dying (health <= 0 or the light
// shrink timer runs) or frozen (ice mode 2 or 3).
func placedSig(a *actors.Actor) (dying, frozen bool) {
	if a.Ice.Read && (a.Ice.Mode == 2 || a.Ice.Mode == 3) {
		return false, true
	}
	return a.Health <= 0 || a.Ice.Read && a.Ice.Timer != 0, false
}

// placedMatch returns the class of unkeyed placed enemy a in room, or nil:
// a K1 class, setID 0xFFFF, placed (no parent), not BOSS, not a mirror
// copy, and its fingerprint an entry's.
func placedMatch(pr *enemies.PlacedRoom, a *actors.Actor) []int {
	if _, ok := combat.K1(a.Proc); !ok || a.SetID != addr.ActorSetIDNone || a.ParentID != 0xFFFFFFFF || a.Boss() ||
		a.Pause&addr.ActorPauseMirrorBit != 0 {
		return nil
	}
	return pr.Match(enemies.ActorFingerprint(stage.Proc(uint16(a.Proc)), a.SetID, a.Params, a.HomePos, a.HomeAngle))
}

// placedTick runs M6.1 over the tick's walk w (complete or not): the
// sender's tracks and the receiver's targets, which it appends to
// r.m6.targets. st, table and room are Link's; settled says why a room is
// not settled ("" when it is); demo and demoWhy are Link's demo gate.
// r.mu is held.
func (r *Replica) placedTick(w *actors.Walk, st wire.Stage, table, room int8, settled func(room int) string, demo bool, demoWhy string) {
	p := &r.m6.pl
	clear(p.targets)
	pr := r.placedRoom(st.String(), int(room))
	partner := r.partnerInRoom(st, table, room)
	complete := w.Complete()

	// The sender. A track lives while the walk sees it, in any room; only
	// an enemy of Link's room becomes one.
	switch {
	case !partner || pr == nil:
		clear(p.tracks)
	default:
		seen := map[uint32]bool{}
		for i := range w.Actors {
			a := &w.Actors[i]
			t := p.tracks[a.PID]
			if t == nil {
				if a.HomeRoom != room || a.InitState != 2 || !a.Ice.Read || !a.Ice.SelfOK ||
					(r.cfg.Claimed != nil && r.cfg.Claimed(a.PID)) || len(p.tracks) >= maxPlacedTracks {
					continue
				}
				class := placedMatch(pr, a)
				if class == nil {
					continue
				}
				t = &placedTrack{stage: st, table: table, room: room, class: class}
				p.tracks[a.PID] = t
			}
			seen[a.PID] = true
			if r.cfg.Claimed != nil && r.cfg.Claimed(a.PID) {
				delete(p.tracks, a.PID) // the mirror took it since
				continue
			}
			switch dying, frozen := placedSig(a); {
			case frozen:
				t.frozen = true
			case dying:
				t.dying = true
			case a.Ice.Read && a.Ice.Mode == 1 && t.frozen:
				t.frozen, t.dying = false, false // thawed: whatever was latched is off
			}
		}
		if complete {
			r.placedGone(seen, st, table, room, settled)
		}
	}

	// The receiver.
	keep := p.pending[:0]
	for _, k := range p.pending {
		switch {
		case k.stage != st || k.table != table || k.room != room || !partner:
			p.dropped++
			continue
		case k.ttl <= 0:
			p.expired++
			continue
		}
		k.ttl--
		keep = append(keep, k)
	}
	p.pending = keep
	if len(p.pending) == 0 || pr == nil {
		return
	}
	counter, err := mem.ReadU32(r.m, addr.ProcessIDCounter)
	if err != nil {
		r.m6.lastHeld = "placed: the process ID counter is unreadable"
		return
	}
	taken := map[uint32]bool{}
	for _, t := range r.m6.targets {
		taken[t.Carrier.PID] = true
	}
	keep = p.pending[:0]
	for _, k := range p.pending {
		if k.cutoff == 0 {
			k.cutoff = counter
		}
		var best *actors.Actor
		alive := false
		for i := range w.Actors {
			a := &w.Actors[i]
			if int8(a.HomeRoom) != room || a.PID >= k.cutoff {
				continue
			}
			class := placedMatch(pr, a)
			if !slices.Contains(class, k.ordinal) {
				continue
			}
			k.seen = true
			if dying, _ := placedSig(a); dying {
				continue
			}
			alive = true
			if r.m6.ever[a.PID] || taken[a.PID] || (best != nil && a.PID > best.PID) {
				continue
			}
			best = a
		}
		if !alive {
			if complete && k.seen {
				p.gone++ // the partner's kill and this game's own, at once
				continue
			}
			keep = append(keep, k)
			continue
		}
		keep = append(keep, k)
		if best == nil {
			continue
		}
		hold := r.m6.hold
		if why := settled(int(best.HomeRoom)); why != "" {
			hold(best.PID, why)
			continue
		}
		if why := combat.Eligible(best, false, r.cfg.Claimed); why != "" {
			hold(best.PID, "placed: "+why)
			continue
		}
		wp, why := combat.WeaponOf(w, best, false)
		if why != "" {
			hold(best.PID, "placed: "+why)
			continue
		}
		if !demo {
			hold(best.PID, demoWhy)
			continue
		}
		t := ActorTarget{Carrier: *best}
		if wp != nil {
			c := *wp
			t.Weapon = &c
		}
		r.m6.targets = append(r.m6.targets, t)
		taken[best.PID] = true
		k.target = best.PID
		p.targets[best.PID] = k
	}
	p.pending = keep
}

// placedGone judges the tracks a complete walk did not see: each sent as
// its class's lowest ordinal not yet set this tick, or counted by why not.
func (r *Replica) placedGone(seen map[uint32]bool, st wire.Stage, table, room int8, settled func(room int) string) {
	p := &r.m6.pl
	var gone []uint32
	for pid := range p.tracks {
		if !seen[pid] {
			gone = append(gone, pid)
		}
	}
	slices.Sort(gone)
	send := map[int]bool{}
	for _, pid := range gone {
		t := p.tracks[pid]
		delete(p.tracks, pid)
		why := ""
		switch {
		case p.remote[pid]:
			why = "echo"
		case t.frozen:
			why = "frozen"
		case !t.dying:
			why = "nosig"
		case t.stage != st || t.table != table:
			why = "stage"
		case t.room != room:
			why = "offroom"
		case settled(int(t.room)) != "":
			why = "room"
		}
		delete(p.remote, pid)
		if why != "" {
			p.unsent[why]++
			continue
		}
		for _, o := range t.class {
			if !send[o] {
				send[o] = true
				break
			}
		}
	}
	if len(send) == 0 {
		return
	}
	var words [killWords]uint32
	for o := range send {
		words[o/32] |= 1 << (o % 32)
	}
	sc := wire.Scoped{Kind: wire.ScopePlaced, Stage: st, Table: table, Room: room}
	for i, v := range words {
		if v != 0 {
			sc.Words = append(sc.Words, wire.ScopedWord{Word: uint8(i), Bits: v})
		}
	}
	p.out = append(p.out, sc)
	p.sent += len(send)
}

// placedAllow reports whether a is one of the tick's placed targets
// (CombatTick.Allow).
func (p *placedState) placedAllow(a *actors.Actor) bool {
	_, ok := p.targets[a.PID]
	return ok && a.SetID == addr.ActorSetIDNone
}

// placedDone records that pid, a placed target, was light-shrunk: its
// pending kill is done, and its vanishing is never sent back. r.mu is
// held.
func (p *placedState) placedDone(pid uint32) (bool, string) {
	k, ok := p.targets[pid]
	if !ok {
		return false, ""
	}
	delete(p.targets, pid)
	p.pending = slices.DeleteFunc(p.pending, func(q *placedPending) bool { return q == k })
	p.remote[pid] = true
	p.taken++
	return true, fmt.Sprintf("placed: light-shrunk the partner's kill of %s room %d ordinal %d (table %d)", k.stage, k.room, k.ordinal, k.table)
}

// takePlacedOut returns the PKILLs the sender has for this tick.
func (p *placedState) takePlacedOut() []wire.Scoped {
	out := p.out
	p.out = nil
	return out
}
