package replica

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
)

// The M6 receiver (§7.2, M6.0): a partner's recorded deaths of keyed
// enemies, which the hub keeps per (stage, table, room) while anyone is in
// the stage (SCOPED KILL, world.Scopes), reach the receiver's game two
// ways, each only for keys its own M1 tables sync (tables.Tables.
// SyncedKill: a keyed K1 enemy, not BOSS, Morth or Shared):
//
//   - persistence: the bits are ORed into the zone of each loaded room of
//     the stage (ApplyKills), Link's or a neighbour's, hidden or shown, so
//     dStage_actorInit skips the dead enemies the next time the room's
//     actors are created (isActor, d_stage.cpp:1693). The zones are wiped
//     at a stage entry (d_stage.cpp:124-126) and the hub drops the store
//     when the stage empties, so an enemy stays dead while either player is
//     in the stage, as vanilla's own lifetime is per player;
//   - live: an enemy of a key the partner's store has that is alive here is
//     light-shrunk (ApplyActors): one byte, 1, into its enemyice timer.
//     Only the partner's store is asked, never this game's own zone: a
//     ReDead this game is killing records its death on every frame for
//     about ten seconds before it goes (d_a_rd.cpp:836-853).
//
// The light-shrink waits for every gate of combat.Eligible, Link's demo
// gate (combat.DemoTypeVerified, and Link in no demo), and the class's
// live verification; each target is read again just before its write
// (combat.Revalidate). An actor written is never written again (ever, by
// process ID, which the game never reuses, f_pc_base.cpp:36-40, except
// across a save-state load, which rewinds the counter: ever is forgotten
// then).
//
// Nothing is written unless the hub grants proto.FeatureCombat; a writing
// replica not granted it says so once. A dry run never asks for it.

const (
	// maxM6Ever bounds the process IDs the receiver remembers having
	// light-shrunk: more than one stage holds actors (MaxNodes).
	maxM6Ever = 2 * actors.MaxNodes
	// maxM6Held bounds the process IDs it remembers holding, with why.
	maxM6Held = actors.MaxNodes
)

// m6 is the replica's M6 receiver state.
type m6 struct {
	// This tick's writes, made once the lock is released, after the
	// scoped path's (applyCombat).
	kills   []KillWrite
	targets []ActorTarget
	demo    bool // the targets passed Link's demo gate

	// ever are the process IDs light-shrunk, which a complete walk prunes
	// to those still queued.
	ever map[uint32]bool
	// held are the process IDs held, with the last reason, for /status.
	held     map[uint32]string
	lastHeld string
	demoHeld map[uint32]bool // the targets held for Link's demo gate

	toldOn, toldOff bool

	// rf is the stale-actor refresh's state (refresh.go).
	rf refreshState

	// What /status shows (CombatStatus).
	place     world.Place
	storeBits int
	zoneSet   int
	shrunk    int
	released  int
	refused   int // the store's bits this side's tables do not sync, as the last tick counted them
}

// CombatStatus is how the M6 receiver stands (Status.Combat), for /status.
type CombatStatus struct {
	// Granted: the hub grants proto.FeatureCombat this session. Tables: the
	// replica judges which kills sync by its own M1 enemy table; without
	// one it writes none.
	Granted, Tables bool
	// Place is where Link is, as the last consistent tick saw him.
	Place world.Place
	// StoreBits counts the partner's kill bits held for Link's stage and
	// table (every room).
	StoreBits int
	// ZoneSet counts the dead-actor bits written into this game's zones,
	// Shrunk the light-shrinks written, Released the weapons let go, Held
	// the targets held (each actor once, with LastHeld the last reason
	// and DemoHeld those held for Link's demo gate), Refused the kill bits
	// the partner sent that this side's tables do not sync, and
	// VerifyFail the light-shrinks and zone bytes that did not hold.
	ZoneSet, Shrunk, Released, Held, DemoHeld, Refused, VerifyFail int
	LastHeld                                                       string
}

func newM6() m6 {
	return m6{ever: map[uint32]bool{}, held: map[uint32]string{}, demoHeld: map[uint32]bool{}, place: world.Nowhere, rf: newRefreshState()}
}

// combatStatus is the M6 receiver's part of r's Status. r.mu is held.
func (r *Replica) combatStatus() CombatStatus {
	m := &r.m6
	return CombatStatus{
		Granted: r.features&proto.FeatureCombat != 0, Tables: r.cfg.Tables != nil && r.cfg.Tables.Enemies != nil,
		Place: m.place, StoreBits: m.storeBits, ZoneSet: m.zoneSet, Shrunk: m.shrunk, Released: m.released,
		Held: len(m.held), DemoHeld: len(m.demoHeld), Refused: m.refused, VerifyFail: r.writer.Stats().CombatFailed, LastHeld: m.lastHeld,
	}
}

// hold records that pid was not light-shrunk this tick, and why.
func (m *m6) hold(pid uint32, why string) {
	if m.held[pid] == why {
		return
	}
	if len(m.held) >= maxM6Held {
		clear(m.held)
	}
	m.held[pid] = why
	m.lastHeld = why
}

// m6Granted reports whether the kill union runs this session: granted by
// the hub to a writing replica. It says once, while the replica stays
// armed, what is written, and once why nothing is.
func (r *Replica) m6Granted() bool {
	m := &r.m6
	switch {
	case r.writer.DryRun():
		return false
	case r.features&proto.FeatureCombat != 0:
		m.toldOff = false
		if !m.toldOn {
			m.toldOn = true
			if r.cfg.Tables == nil || r.cfg.Tables.Enemies == nil {
				r.logf("co-op: combat: this side has no M1 enemy table, so no partner's kill is written (which enemies sync is unknown)")
			} else {
				r.logf("co-op: combat: written to this game when idle: the partner's kills of keyed enemies (into each loaded room's zone, so they stay dead), and a live one of them light-shrinks; unkeyed enemies stay each player's own")
			}
		}
		return true
	}
	m.toldOn = false
	if !m.toldOff {
		m.toldOff = true
		if r.want()&proto.FeatureCombat == 0 {
			r.logf("co-op: a partner's kills are not written to this game: this replica does not ask for %v", proto.FeatureCombat)
		} else {
			r.logf("co-op: a partner's kills are not written to this game: the session does not grant %v (the host turned it off, or the two sides' co-op stage tables differ: the host's notice says which)", proto.FeatureCombat)
		}
	}
	return false
}

// killMask is room's synced keys in Link's stage by the receiver's own
// tables; none without them.
func (r *Replica) killMask(table int8, stage string, room int) enemies.KillSet {
	if r.cfg.Tables == nil {
		return enemies.KillSet{}
	}
	return synctables.KillMask(r.cfg.Tables, int(table), stage, room)
}

// reconcileM6 runs the M6 receiver for one tick: o's snapshot, read and
// observed. Its writes wait in r.m6 for Tick to make once the lock is
// released.
func (r *Replica) reconcileM6(o *obs, armed bool) {
	m := &r.m6
	m.kills, m.targets, m.demo = nil, nil, false
	m.rf.locks = nil
	clear(m.rf.deathPIDs)
	if o.st.Regressed {
		// A save state was loaded: the actor heap and the process-ID
		// counter (f_pc_base.cpp:36-40) went back with it, so an enemy
		// shrunk since is alive again with the same process ID and a
		// timer of 0, and a new actor can take a process ID already
		// remembered. Forget them all; Eligible's timer gate still keeps
		// a shrink under way from a second write.
		clear(m.ever)
		clear(m.held)
		clear(m.demoHeld)
		m.rf.forget()
	}
	if o.consistent() {
		if st, err := wire.MakeStage(o.s.StageName); err == nil {
			m.place = world.Place{Stage: st.String(), Table: int(o.s.Table), Room: int(o.s.StayNo)}
		}
	} else if o.screen != screenGame {
		m.place = world.Nowhere
	}
	if !armed || !r.haveView || r.viewWorld != r.sessionWorld {
		m.toldOn, m.toldOff = false, false
		return
	}
	if !r.m6Granted() {
		return
	}
	if off, _ := r.writer.Disabled(); off {
		return
	}
	s := o.s
	stage, err := wire.MakeStage(s.StageName)
	if !o.consistent() || err != nil {
		return
	}
	// The partner's stores of Link's stage and table, by room, copied:
	// the WriteGuard asks the plan's kills (CombatTick.Kills, Allow) after
	// r.mu is released, while Handle ORs new bits into r.scopedIn's words
	// in place (onScoped). The guard judges by this tick's plan, never by
	// bits that came after it.
	stores := map[int][]uint32{}
	m.storeBits = 0
	for k, ws := range r.scopedIn {
		if k.kind != wire.ScopeKill || k.stage != stage || k.table != s.Table || k.room < 0 || int(k.room) >= addr.RoomCount {
			continue
		}
		ws = slices.Clone(ws)
		stores[int(k.room)] = ws
		for _, w := range ws {
			m.storeBits += popcount(w)
		}
	}
	if !o.st.CanWriteIdle() {
		return
	}
	// The stale-actor refresh (refresh.go) needs the walk too, when a
	// switch landed, kill stores or none.
	rt := r.newRefreshTick(o)
	if len(stores) == 0 && !rt.any() {
		return
	}
	name := stage.String()
	rooms := make([]int, 0, len(stores))
	for room := range stores {
		rooms = append(rooms, room)
	}
	slices.Sort(rooms)
	masks := map[int]enemies.KillSet{}
	m.refused = 0
	live := false // some store bit is a synced key of a room loaded here
	for _, room := range rooms {
		ws := stores[room]
		mask := r.killMask(s.Table, name, room)
		masks[room] = mask
		for i, w := range ws {
			if i < len(mask) {
				m.refused += popcount(w &^ mask[i])
			}
		}
		z, ok := s.ZoneFor(room)
		if !ok || !o.rooms.ok {
			continue
		}
		if f := o.rooms.flags[room]; f&addr.RoomFlagLoaded == 0 || f&(addr.RoomFlagCreating|addr.RoomFlagDeleting) != 0 {
			continue
		}
		local := killWordsOf(s, z)
		for i, w := range ws {
			if i >= len(mask) {
				break
			}
			if w&mask[i] != 0 {
				live = true
			}
			for b := range 32 {
				id := 32*i + b
				if (w&mask[i]&^local[i])&(1<<b) == 0 {
					continue
				}
				kw, err := killWriteOf(room, z, id)
				if err != nil {
					r.logf("co-op: bug: setID %d of room %d has no byte: %v; dropped", id, room, err)
					continue
				}
				m.kills = append(m.kills, kw)
			}
		}
	}
	kills := func(room, setID int) bool {
		ws, mask := stores[room], masks[room]
		return setID >= 0 && setID < enemies.MaxSetID && ws != nil && ws[setID/32]&(1<<(setID%32)) != 0 && mask.Has(setID)
	}
	ct := CombatTick{Flags: o.rooms.flags, FlagsOK: o.rooms.ok, Kills: kills, Claimed: r.cfg.Claimed}
	if !live && !rt.any() {
		r.setCombat(ct)
		return
	}

	// The light-shrink of the store's live enemies, and the refresh's
	// candidates.
	w, err := combat.Read(r.m, 0)
	switch {
	case err != nil:
		m.lastHeld = "the actor walk could not be read: " + err.Error()
		r.setCombat(ct)
		return
	case w.Stop != "":
		m.lastHeld = "the actor walk stopped: " + w.Stop
		r.setCombat(ct)
		return
	}
	if w.Complete() {
		for pid := range m.ever {
			if _, ok := w.ByPID(pid); !ok {
				delete(m.ever, pid)
			}
		}
	}
	// Two rules take a light-shrink target: M6's, a keyed enemy whose
	// death the partner's store has, of a room loaded and settled (the walk
	// runs once a switch landed, not only when live says some store room
	// is); and the refresh's Tier 2, a K1 enemy whose death switch landed
	// (deathAllow), a keyed one only when its key syncs (rt.keys). The
	// guard asks the same allow of the actor Revalidate read again
	// (judgeActor).
	rt.keys = map[int]enemies.KillSet{}
	for i := range w.Actors {
		if a := &w.Actors[i]; a.Keyed() {
			room := int(a.HomeRoom)
			if _, ok := rt.keys[room]; ok {
				continue
			}
			if ks, ok := masks[room]; ok {
				rt.keys[room] = ks
			} else {
				rt.keys[room] = r.killMask(s.Table, name, room)
			}
		}
	}
	keyed := func(a *actors.Actor) bool { return a.Keyed() && kills(int(a.HomeRoom), int(a.SetID)) }
	allow := func(a *actors.Actor) bool {
		return keyed(a) && rt.roomSettled(int(a.HomeRoom)) == "" || rt.deathAllow(a)
	}
	demo, demoWhy := r.linkDemo(o)
	for i := range w.Actors {
		a := &w.Actors[i]
		if m.ever[a.PID] {
			continue
		}
		// hold records why a target waits: a death-switch target's reason
		// is the refresh's own (Status.Refresh), the keyed rule's M6's.
		hold := m.hold
		var death *DeathRecheck
		if keyed(a) {
			if why := rt.roomSettled(int(a.HomeRoom)); why != "" {
				hold(a.PID, why)
				continue
			}
		} else {
			why, cand := rt.deathWhy(a)
			if !cand {
				continue
			}
			hold = m.rf.holdDeath
			if why != "" {
				hold(a.PID, why)
				continue
			}
			if death = rt.deathRecheck(a); death == nil {
				hold(a.PID, "its death switch has no byte to read again")
				continue
			}
			m.rf.deathPIDs[a.PID] = true
		}
		if why := combat.Eligible(a, false, r.cfg.Claimed); why != "" {
			hold(a.PID, why)
			continue
		}
		if _, ok := s.ZoneFor(int(a.HomeRoom)); !ok {
			hold(a.PID, fmt.Sprintf("its home room %d has no zone (onActor asserts, d_save.cpp:1678-1679)", a.HomeRoom))
			continue
		}
		wp, why := combat.WeaponOf(&w, a, false)
		if why != "" {
			hold(a.PID, why)
			continue
		}
		if !demo {
			hold(a.PID, demoWhy)
			if len(m.demoHeld) < maxM6Held && !m.rf.deathPIDs[a.PID] {
				m.demoHeld[a.PID] = true
			}
			continue
		}
		t := ActorTarget{Carrier: *a, Death: death}
		if wp != nil {
			c := *wp
			t.Weapon = &c
		}
		m.targets = append(m.targets, t)
	}
	m.demo = demo
	ct.Walk, ct.Allow, ct.Demo = &w, allow, demo
	ct.Locks = r.planLocks(rt, &w, demo, demoWhy)
	r.setCombat(ct)
}

// setCombat gives the WriteGuard this tick's plan.
func (r *Replica) setCombat(c CombatTick) {
	if wg := r.writer.WriteGuard(); wg != nil {
		wg.SetCombat(c)
	}
}

// linkDemo reports whether Link's demo gate passes: its offset is
// verified (combat.DemoTypeVerified) and his mDemo.mDemoType reads 0.
// Otherwise it says why.
func (r *Replica) linkDemo(o *obs) (bool, string) {
	if !combat.DemoTypeVerified {
		return false, "link demo unverified (docs/08 §8.2 K1)"
	}
	p := o.s.PlayerPtr
	if p == 0 {
		return false, "no Link"
	}
	b, err := r.m.Read(p+addr.PlayerOffDemoType, 2)
	if err != nil {
		return false, "Link's demo type unreadable"
	}
	if t := uint16(b[0])<<8 | uint16(b[1]); t != 0 {
		return false, fmt.Sprintf("link demo %d", t)
	}
	return true, ""
}

func popcount(w uint32) int {
	n := 0
	for ; w != 0; w &= w - 1 {
		n++
	}
	return n
}

// m6Writes are one tick's M6 writes, the refresh's lock clears among them.
type m6Writes struct {
	kills   []KillWrite
	targets []ActorTarget
	demo    bool
	locks   []LockTarget
}

// take returns this tick's writes and forgets them.
func (m *m6) take() m6Writes {
	ws := m6Writes{kills: m.kills, targets: m.targets, demo: m.demo, locks: m.rf.locks}
	m.kills, m.targets, m.rf.locks = nil, nil, nil
	return ws
}

// applyCombat makes a tick's M6 writes, after the scoped path's
// (rep.WriteErr is the first error the tick's earlier paths had): not at
// all when one stalled or the game moved on. The zone bytes go first, then
// the light-shrinks, so onActor finds the bit set when the shrink ends,
// then the refresh's lock clears. It takes r.mu for the result.
func (r *Replica) applyCombat(ctx context.Context, ws m6Writes, rep *Report) {
	if len(ws.kills) == 0 && len(ws.targets) == 0 && len(ws.locks) == 0 {
		return
	}
	err := rep.WriteErr
	if errors.Is(err, ErrStall) || errors.Is(err, ErrAborted) || errors.Is(err, ErrOff) {
		return
	}
	var kres Result
	var kerr error
	if len(ws.kills) > 0 {
		kres, kerr = r.writer.ApplyKills(ctx, ws.kills)
	}
	var ares ActorResult
	var aerr error
	if len(ws.targets) > 0 && !errors.Is(kerr, ErrStall) && !errors.Is(kerr, ErrAborted) && !errors.Is(kerr, ErrOff) {
		ares, aerr = r.writer.ApplyActors(ctx, ws.targets, ws.demo, false)
	}
	var lres LockResult
	var lerr error
	if len(ws.locks) > 0 && !stopped(kerr) && !stopped(aerr) {
		lres, lerr = r.writer.ApplyLocks(ctx, ws.locks)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := &r.m6
	rep.Wrote += len(kres.Wrote) + len(ares.Wrote) + len(lres.Wrote)
	for _, e := range []error{kerr, aerr, lerr} {
		if rep.WriteErr == nil && e != nil {
			rep.WriteErr = e
		}
	}
	var lines []string
	by := map[uint32]KillWrite{}
	for _, kw := range ws.kills {
		by[kw.Addr] = kw
	}
	for _, b := range kres.Wrote {
		kw := by[b.Addr]
		kw.Bits = b.New &^ b.Old
		m.zoneSet += len(kw.SetIDs())
		lines = append(lines, "set the partner's "+kw.String())
	}
	for _, d := range ares.Done {
		if len(m.ever) >= maxM6Ever {
			clear(m.ever)
		}
		m.ever[d.Carrier.PID] = true
		delete(m.held, d.Carrier.PID)
		delete(m.demoHeld, d.Carrier.PID)
		m.shrunk++
		what := "light-shrunk the partner's kill: " + d.Carrier.String()
		if m.rf.deathPIDs[d.Carrier.PID] {
			delete(m.rf.swHeld, d.Carrier.PID)
			m.rf.shrunk++
			what = "refresh: light-shrunk a stale enemy whose death switch landed: " + d.Carrier.String()
		}
		if d.Released {
			m.released++
			what += ", its weapon let go"
		}
		lines = append(lines, what)
	}
	for _, h := range ares.Held {
		if m.rf.deathPIDs[h.Carrier.PID] {
			m.rf.holdDeath(h.Carrier.PID, h.Why)
			continue
		}
		m.hold(h.Carrier.PID, h.Why)
	}
	for _, d := range lres.Done {
		if len(m.rf.lockEver) >= maxM6Ever {
			clear(m.rf.lockEver)
		}
		m.rf.lockEver[d.Door.PID] = true
		delete(m.rf.lockHeld, d.Door.PID)
		m.rf.cleared++
		lines = append(lines, fmt.Sprintf("refresh: cleared the stale lock of %s: its switch 0x%02X landed", d.Door.String(), d.No))
	}
	for _, h := range lres.Held {
		m.rf.holdLock(h.Door.PID, h.Why)
	}
	if len(lines) > 0 {
		beat := kres.Beat
		if beat == 0 {
			beat = ares.Beat
		}
		if beat == 0 {
			beat = lres.Beat
		}
		r.logf("co-op: combat: wrote %d byte(s) after heartbeat %d", len(kres.Wrote)+len(ares.Wrote)+len(lres.Wrote), beat)
		r.logLines(lines)
	}
}

// stopped reports whether err stops the tick's later writes: a stall, the
// game moving on, or co-op off.
func stopped(err error) bool {
	return errors.Is(err, ErrStall) || errors.Is(err, ErrAborted) || errors.Is(err, ErrOff)
}
