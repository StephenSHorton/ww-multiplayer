package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// The M5 receiver (§6.3, §9 M5): a co-present partner's latching stage
// switches, which the image does not carry, reach the receiver's game
// through the scoped stores the hub keeps (SCOPED, world.Scopes) and the
// scoped path (FrameWriter.ApplyScoped), every byte through the
// WriteGuard:
//
//   - the dan switches 0x80-0xBF (mDan.mSwitch, dSv_danBit_c::onSwitch,
//     d_save.cpp:1237-1241), from the DAN store of the stage name and save
//     table the receiver is in, live when IDLE, while the partner's last
//     PRESENCE puts it in that stage and table too;
//   - the zone switches 0xC0-0xDF (a zone's mSwitch[0..1],
//     dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282), from the ZONE store
//     of the room the receiver is in, into the zone its own game has for
//     that room (mStatus[room].mZoneNo, d_save.cpp:1552-1556), never the
//     sender's slot: slots are first-free per machine (createZone,
//     d_save.cpp:1525-1537). Live when IDLE, once the zone exists, while
//     the partner's last PRESENCE puts it in that room too;
//   - the room switches 0xE0-0xEF (the zone's mSwitch[2]), which every
//     room change clears (clearRoomSwitch, d_save.cpp:1273-1276;
//     zoneCountCheck, d_stage.cpp:250-267), only as edges: a ROOMEDGE the
//     hub relayed while both players were in the room is applied once, on
//     the first tick the gates open while Link is still in the room he
//     was in when it came (the same visit: stage, table, room, zone slot
//     and mOldStayNo, unbroken, with none of the room switches the visit
//     had gone), and then forgotten. It waits at most edgeTicks ticks of
//     closed gates. It is never kept, and never replayed after a room
//     change the receiver sees: one out of the room and back within a
//     tick, into the room Link came from and with no room switch set,
//     changes nothing a tick can read, so an edge waiting across it is
//     still written (Appendix C item 124).
//
// Every class waits for the room hold (reconcile_m5_rooms.go): a room's
// actors are given roomSettle IDLE frames after they are created before a
// switch they read is written, as a shutter ignores a switch that changes
// in its first 30 frames.
//
// Every class is OR only: a bit is set, never cleared. Only the switches
// the receiver's own M1 table marks latching for that scope are written
// (tables.Tables.SyncedScopedSwitch: every writer of the switch latches,
// §6.2), whatever the hub sent; without the table, none. And each bit is
// written only if the receiver's game never had it in that scope (its
// ever-had set: every bit its game had on a consistent tick, and every bit
// the scoped path wrote), so a bit the receiver's own game clears is never
// set again, and a scope entered again gets only the bits it never had.
// The dan bits' scope there is the save table alone, as the game keeps
// one mDan per table (dSv_danBit_c::init clears it only when the table
// changes, d_save.cpp:1225-1235): a bit initDan reset is not refilled from
// another stage of the table either. The ever-had set lives with the
// file, as the Planner's does: it starts over when another file is
// loaded, and, for a writing replica, when a save state is loaded or the
// game passes through the title or file select.
//
// The hub keeps a store only while someone is in its scope (world.Scopes),
// and sends a player entering a stage or room what is stored there, so a
// receiver entering where the partner is gets the store, applied once the
// zone exists, the room has settled and the gates open. The receiver keeps
// a store no longer than it is in its scope (pruneScoped): the hub sends
// it again on the next entry, if the partner is still there.
//
// Nothing is written unless the hub grants hub.FeaturePuzzle; a writing
// replica not granted it says so once. A dry run (--coop-dry-run) plans
// the same and logs what it would write; without M1 tables it logs every
// bit, as M2 did.

const (
	// edgeTicks is how many ticks of closed gates a room edge waits,
	// within the visit it came in, for the gates to open (a second). The
	// room hold's ticks, gates open, do not count.
	edgeTicks = 10
	// maxM5Ever bounds the scopes whose ever-had bits the receiver keeps:
	// far more than one session visits (a stage's dan bits and its rooms'
	// zone bits are 65 scopes at most). At the bound, other stages' are
	// dropped first.
	maxM5Ever = 64 * (1 + addr.RoomCount)
	// maxM5Logged bounds the lines m5 remembers having said.
	maxM5Logged = 512
)

// m5 is the replica's M5 receiver state.
type m5 struct {
	// The ever-had set, per scope (everKey: DAN by save table alone;
	// ZONE: words 0-1 in the low 16 bits), of the file last seen.
	have bool
	file fileKey
	ever map[scopeKey][2]uint32

	// The room Link is in, as the last tick saw it, and its visit: seq
	// moves on with every change of stage, table, room, zone slot or
	// mOldStayNo, with a stage change pending, with a room switch of the
	// visit gone, and with every tick that could not tell. roomHad is the
	// room switches the room had this visit.
	visit   m5Visit
	seq     uint64
	roomHad uint16
	edge    *m5Edge // a room edge waiting for the gates

	// rooms are the rooms' clocks for the room hold.
	rooms m5Rooms

	// This tick's writes, made once the lock is released and the merge
	// and derive paths' are made (Replica.applyScoped).
	writes []m5Write

	logged  map[string]bool // lines said while the replica stays armed
	toldOn  bool            // the "room puzzles" line was said since FeaturePuzzle was granted
	toldOff bool            // the "not written" line was said since it was taken away

	// What /status shows (PuzzleStatus): where Link is, as the last
	// consistent tick saw him, and what the receiver did this session.
	place   world.Place
	applied int
	refused map[string]bool // the switches not written as not latching here, per scope
	dropped int
}

// PuzzleStatus is how the M5 receiver stands (Status.Puzzle, §9 M5), for
// /status.
type PuzzleStatus struct {
	// Granted: the hub grants hub.FeaturePuzzle this session.
	Granted bool
	// Tables: the replica judges which switches latch by its own M1
	// switch table. Without one a writing replica writes no partner's
	// dan, zone or room switch.
	Tables bool
	// Place is where Link is, as the last consistent tick saw him
	// (world.Nowhere before one, and on the title or file select): the
	// stage and table of the DAN store he takes, and the room of the ZONE
	// store.
	Place world.Place
	// Zone is the zone slot his own game has for that room, -1 when it
	// has none or a stage change is pending: no zone or room switch is
	// written then.
	Zone int
	// Applied counts the partner's switches the scoped path set this
	// session; Refused the switches the hub sent that this side's table
	// does not mark latching in their scope, each once (a bounded set);
	// Dropped the room edges dropped unwritten (the game was not idle
	// within edgeTicks, or Link was no longer in the room).
	Applied, Refused, Dropped int
}

// m5Visit is the room Link is in, with its zone and the room he came
// from (mOldStayNo, which every room change moves: setStayNo,
// d_stage.cpp:171-173).
type m5Visit struct {
	ok    bool
	stage wire.Stage
	table int8
	room  int8
	slot  int8
	old   int8
}

// m5Edge is a room edge the hub relayed, for the visit seq.
type m5Edge struct {
	bits  uint16 // room switches 0xE0 + bit
	seq   uint64
	ticks int
}

// m5Write is one byte the receiver plans, and the store it is of.
type m5Write struct {
	ScopedWrite
	key scopeKey
}

// m5Writes are one tick's scoped writes.
type m5Writes struct {
	ws  []m5Write
	seq uint64 // the visit they were planned in
}

func newM5() m5 {
	return m5{ever: map[scopeKey][2]uint32{}, logged: map[string]bool{}, refused: map[string]bool{}, place: world.Nowhere}
}

// puzzleStatus is the M5 receiver's part of r's Status. r.mu is held.
func (r *Replica) puzzleStatus() PuzzleStatus {
	m := &r.m5
	st := PuzzleStatus{
		Granted: r.features&hub.FeaturePuzzle != 0,
		Tables:  r.cfg.Tables != nil,
		Place:   m.place,
		Zone:    -1,
		Applied: m.applied, Refused: len(m.refused), Dropped: m.dropped,
	}
	if m.visit.ok {
		st.Zone = int(m.visit.slot)
	}
	return st
}

// dropEdge forgets the room edge waiting, counting it if it still had
// bits to write.
func (m *m5) dropEdge() {
	if m.edge != nil && m.edge.bits != 0 {
		m.dropped++
	}
	m.edge = nil
}

// take returns this tick's writes and forgets them.
func (m *m5) take() m5Writes {
	ws := m5Writes{ws: m.writes, seq: m.seq}
	m.writes = nil
	return ws
}

// leave is a tick that does not show Link in a room of a known stage: the
// visit ends, and a room edge with it.
func (m *m5) leave() {
	m.dropEdge()
	m.visit, m.roomHad = m5Visit{}, 0
	m.seq++
}

// everKey is the ever-had set's key for scope k: a zone's scope as it is,
// and for the dan bits the save table alone. The game keeps one mDan,
// for the table initDan last ran for (dSv_danBit_c::init,
// d_save.cpp:1225-1235; dStage_stagInfoInit, d_stage.cpp:1668-1677), so
// a dan bit had in one stage of a table is had in all of them.
func everKey(k scopeKey) scopeKey {
	if k.kind == wire.ScopeDan {
		return scopeKey{kind: wire.ScopeDan, table: k.table, room: -1}
	}
	return k
}

// had records in the ever-had set bits w of scope k, a scope of the stage
// and table Link is in. At the bound the scopes of other stages and
// tables go first; the dan bits of k's table stay.
func (m *m5) had(k scopeKey, w [2]uint32) {
	ek := everKey(k)
	e, ok := m.ever[ek]
	if !ok {
		if w == [2]uint32{} {
			return
		}
		if len(m.ever) >= maxM5Ever {
			for o := range m.ever {
				if o.table != k.table || o.kind != wire.ScopeDan && o.stage != k.stage {
					delete(m.ever, o)
				}
			}
		}
	}
	e[0] |= w[0]
	e[1] |= w[1]
	m.ever[ek] = e
}

// everHad is what the ever-had set holds for scope k.
func (m *m5) everHad(k scopeKey) [2]uint32 { return m.ever[everKey(k)] }

// mark records in the ever-had set switch no of scope k, which the scoped
// path set.
func (m *m5) mark(k scopeKey, no int) {
	switch addr.ClassifySwitch(no) {
	case addr.SwitchDan:
		var w [2]uint32
		i := no - addr.SwitchDanFirst
		w[i/32] = 1 << (i % 32)
		m.had(k, w)
	case addr.SwitchZone:
		var w [2]uint32
		i := no - addr.SwitchZoneFirst
		w[i/16] = 1 << (i % 16)
		m.had(k, w)
	case addr.SwitchRoom:
		m.roomHad |= 1 << (no - addr.SwitchRoomFirst)
	}
}

// m5Say logs line once while the replica stays armed.
func (r *Replica) m5Say(key, line string) {
	m := &r.m5
	if m.logged[key] {
		return
	}
	if len(m.logged) >= maxM5Logged {
		clear(m.logged)
	}
	m.logged[key] = true
	r.logf("co-op: %s", line)
}

// m5Observe records a tick's dan and zone bits in the ever-had set and
// follows the room Link is in and the rooms' clocks, on every tick, armed
// or not, before the reconcilers run: M3's waits on the clocks too.
func (r *Replica) m5Observe(o *obs) {
	m := &r.m5
	if !r.writer.DryRun() && (o.st.Regressed || o.screen == screenOut) {
		m.have = false
	}
	stage, err := wire.MakeStage(o.s.StageName)
	if !o.consistent() || err != nil {
		if o.screen != screenGame {
			m.place = world.Nowhere
		}
		m.rooms.reset()
		m.leave()
		return
	}
	s := o.s
	m.place = world.Place{Stage: stage.String(), Table: int(s.Table), Room: int(s.StayNo)}
	if !m.have || m.file != o.key {
		m.have, m.file = true, o.key
		clear(m.ever)
		m.leave()
	}
	m.had(scopeKey{kind: wire.ScopeDan, stage: stage, table: s.Table, room: -1}, danWords(s))
	for room := range addr.RoomCount {
		if z, ok := s.ZoneFor(room); ok {
			zw := zoneWords(s, z)
			m.had(scopeKey{kind: wire.ScopeZone, stage: stage, table: s.Table, room: int8(room)}, [2]uint32{uint32(zw[0]), uint32(zw[1])})
		}
	}
	m.rooms.observe(s, stage, o.st, &o.rooms)
	// A stage change pending ends the visit: the play scene goes next, and
	// Link with it (dStage_Delete, d_stage.cpp:2248-2261), so a reload of
	// the same stage and room is another visit. So does a probe that saw
	// another mStayNo than the snapshot: a room change between the two
	// reads.
	var v m5Visit
	var zw [3]uint16
	p := &o.rooms
	if z, ok := s.ZoneFor(int(s.StayNo)); ok && s.NextEnable == 0 && p.ok && p.stay == s.StayNo {
		v = m5Visit{ok: true, stage: stage, table: s.Table, room: s.StayNo, slot: int8(z), old: p.oldStay}
		zw = zoneWords(s, z)
	}
	// Every room change clears every zone's room switches (zoneCountCheck,
	// d_stage.cpp:250-267), one out of the room and back between two
	// ticks too, which leaves the slot alone (the room Link came back to
	// is mOldStayNo, whose zone does not count down). A room switch the
	// visit had that is gone is such a change, or the game's own clear of
	// one that does not latch: either way the visit ends, and an edge
	// waiting with it.
	bounced := v.ok && v == m.visit && m.roomHad&^zw[2] != 0
	if v != m.visit || bounced {
		m.leave()
		m.visit = v
	}
	if v.ok {
		m.roomHad |= zw[2]
	}
}

// m5Edge takes a ROOMEDGE the hub relayed: its room switches are kept for
// the visit Link is in, if the last tick showed him in that room of that
// stage and table; otherwise they are dropped at once. onScoped has
// checked the welcome and the feature.
func (r *Replica) m5Edge(sc *wire.Scoped) {
	m := &r.m5
	var bits uint16
	for _, w := range sc.Words {
		if w.Word == 2 {
			bits |= uint16(w.Bits)
		}
	}
	v := m.visit
	if bits == 0 {
		return
	}
	if !v.ok || v.stage != sc.Stage || v.table != sc.Table || v.room != sc.Room {
		m.dropped++ // relayed for a room Link has left (or not reached)
		return
	}
	if e := m.edge; e != nil && e.seq == m.seq {
		e.bits |= bits
		return
	}
	m.edge = &m5Edge{bits: bits, seq: m.seq}
}

// m5Latches reports whether switch no of scope k may be written: the
// receiver's own M1 table marks it latching there. Without tables a dry
// run takes every switch to latch, as M2's did, and a writing replica none.
func (r *Replica) m5Latches(k scopeKey, no int) bool {
	if r.cfg.Tables == nil {
		return r.writer.DryRun()
	}
	return r.cfg.Tables.SyncedScopedSwitch(int(k.table), k.stage.String(), int(k.room), no)
}

// m5Granted reports whether the room puzzles are planned this session:
// granted by the hub, or a dry run, which plans whatever the hub sent, as
// M4's does, and writes none. A writing replica says once, while it stays
// armed, what is written, and once why nothing is when the grant is
// missing or taken away.
func (r *Replica) m5Granted() bool {
	m := &r.m5
	switch {
	case r.writer.DryRun():
		return true
	case r.features&hub.FeaturePuzzle != 0:
		m.toldOff = false
		if !m.toldOn {
			m.toldOn = true
			if r.cfg.Tables == nil {
				r.logf("co-op: room puzzles: this side has no M1 switch table, so no partner's dan, zone or room switch is written (which ones latch is unknown)")
			} else {
				r.logf("co-op: room puzzles: written to this game when idle: the partner's latching dan switches (same stage and save table) and zone switches (same room), and its room switches as it sets them, while both are in the room")
			}
		}
		return true
	}
	m.toldOn = false
	if !m.toldOff {
		m.toldOff = true
		if r.want()&hub.FeaturePuzzle == 0 {
			r.logf("co-op: a partner's dan, zone and room switches are not written to this game: this replica does not ask for %v", hub.FeaturePuzzle)
		} else {
			r.logf("co-op: a partner's dan, zone and room switches are not written to this game: the session does not grant %v (the host turned it off, or the two sides' co-op stage tables differ: the host's notice says which)", hub.FeaturePuzzle)
		}
	}
	return false
}

// reconcileM5 runs the M5 receiver for one tick: o's snapshot, read and
// observed (m5Observe has run). Its writes wait in r.m5 for Tick to make
// once the lock is released.
func (r *Replica) reconcileM5(o *obs, armed bool) {
	m := &r.m5
	m.writes = nil
	ready := armed && r.haveView && r.viewWorld == r.sessionWorld
	if !ready {
		m.dropEdge()
		m.toldOn, m.toldOff = false, false
		clear(m.logged)
		return
	}
	if !r.m5Granted() {
		m.dropEdge()
		return
	}
	if off, _ := r.writer.Disabled(); off {
		m.dropEdge()
		return
	}
	open := o.consistent() && o.st.CanWriteIdle()
	if e := m.edge; e != nil && !open {
		if e.ticks++; e.ticks > edgeTicks {
			r.m5Say(fmt.Sprintf("edge %d", e.seq), fmt.Sprintf("a partner's room switches (0x%04X) came while this game was not idle; dropped: room edges are never replayed", e.bits))
			m.dropEdge()
		}
	}
	if !open {
		return
	}
	s := o.s
	stage, err := wire.MakeStage(s.StageName)
	if err != nil {
		return
	}
	v := m.visit // this tick's: m5Observe saw the same snapshot
	dan := scopeKey{kind: wire.ScopeDan, stage: stage, table: s.Table, room: -1}
	if ws := r.scopedIn[dan]; ws != nil && r.partnerIn(dan) && m.rooms.calm() {
		local := danWords(s)
		ever := m.everHad(dan)
		for i := range local {
			r.m5Due(dan, -1, i, ws[i]&^local[i]&^ever[i])
		}
	}
	if !v.ok || !m.rooms.settled(int(v.room)) {
		return // no zone for the room Link is in, or it is settling: no zone or room switch
	}
	zw := zoneWords(s, int(v.slot))
	zone := scopeKey{kind: wire.ScopeZone, stage: stage, table: s.Table, room: v.room}
	if ws := r.scopedIn[zone]; ws != nil && r.partnerIn(zone) {
		ever := m.everHad(zone)
		for i := range 2 {
			r.m5Due(zone, v.slot, i, ws[i]&^uint32(zw[i])&^ever[i])
		}
	}
	if e := m.edge; e != nil && e.seq == m.seq {
		edge := scopeKey{kind: wire.ScopeRoomEdge, stage: stage, table: s.Table, room: v.room}
		n := len(m.writes)
		r.m5Due(edge, v.slot, 2, uint32(e.bits&^zw[2]&^m.roomHad))
		if len(m.writes) == n {
			m.edge = nil // nothing of it to write (or a dry run said it): forgotten
		}
	}
}

// partnerIn reports whether the partner's last PRESENCE, as the hub
// relayed it, puts it in scope k: in game, in k's stage and save table
// and, for a ZONE store, in its room. The hub keeps a store while either
// player is in its scope, so a bit it relayed while both were there is
// written only while they still are; one that came while this game was
// not idle waits for the partner to come back. The hub relays every
// PRESENCE as it comes (hub.onPresence) and a gone peer's as nowhere
// (hub.leaveView); a relayed PRESENCE names no sender, which is enough
// for two players (Appendix C item 42).
func (r *Replica) partnerIn(k scopeKey) bool {
	p := r.partner
	if p == nil || p.Flags&wire.PresenceInGame == 0 || p.Stage != k.stage || p.Table != k.table {
		return false
	}
	return k.kind != wire.ScopeZone || p.Room == k.room
}

// m5Due plans word w of store k's due bits: those the store has, the
// receiver's game lacks and never had. slot is the zone of k's room (-1
// for DAN). A dry run logs what it would set; a bit whose switch does not
// latch here is left, and said once.
func (r *Replica) m5Due(k scopeKey, slot int8, w int, due uint32) {
	m := &r.m5
	first, width := addr.SwitchDanFirst, 32
	when := "live when idle, while the stage and table match"
	where := ""
	switch k.kind {
	case wire.ScopeZone:
		first, width, when = addr.SwitchZoneFirst, 16, "live when idle, while in this room"
		where = fmt.Sprintf(" in room %d", k.room)
	case wire.ScopeRoomEdge:
		first, width, when = addr.SwitchZoneFirst, 16, "a room edge, now"
		where = fmt.Sprintf(" in room %d", k.room)
	}
	dry := r.writer.DryRun()
	var logged []uint32
	if dry && k.kind != wire.ScopeRoomEdge {
		if logged = r.loggedSc[k]; logged == nil {
			logged = make([]uint32, 2)
			r.loggedSc[k] = logged
		}
	}
	var said uint32
	for b := range width {
		bit := uint32(1) << b
		if due&bit == 0 {
			continue
		}
		no := first + w*width + b
		label := scopedLabel(k.kind, w, b)
		if !r.m5Latches(k, no) {
			if r.cfg.Tables == nil && !dry {
				continue // m5Granted said why
			}
			key := fmt.Sprintf("unsafe %v %s %d %d 0x%02X", k.kind, k.stage, k.table, k.room, no)
			if !m.refused[key] {
				if len(m.refused) >= maxM5Logged {
					clear(m.refused)
				}
				m.refused[key] = true
			}
			r.m5Say(key, fmt.Sprintf("not written: %s%s does not latch (this side's M1 switch table)", label, where))
			continue
		}
		if r.gbaHeld() && r.cfg.Tables.GBAScopedSwitch(int(k.table), k.stage.String(), int(k.room), no) {
			// Shared with the Tingle Tuner (gba.go): a dan or zone bit
			// waits in its store, a room edge is dropped with its edge.
			r.m5Say(fmt.Sprintf("gba %v %s %d %d 0x%02X", k.kind, k.stage, k.table, k.room, no),
				fmt.Sprintf("not written while a Game Boy Advance may be linked: %s%s is shared with a Tingle Tuner switch", label, where))
			continue
		}
		if dry {
			said |= bit
			if logged == nil || logged[w]&bit == 0 {
				r.logf("co-op: dry-run: would set %s%s (%s)", label, where, when)
			}
			continue
		}
		sw, err := scopedWriteOf(k.kind, no, int(k.room), int(slot))
		if err != nil {
			r.logf("co-op: bug: %s%s has no byte: %v; dropped", label, where, err)
			continue
		}
		m.writes = append(m.writes, m5Write{ScopedWrite: sw, key: k})
	}
	if logged != nil {
		logged[w] = said
	}
}

// applyScoped makes a tick's scoped writes, after the merge and derive
// paths' (rep.WriteErr is the first error those had): not at all when one
// stalled or the game moved on, since this tick's view of the zones is as
// stale. It takes r.mu for the result.
func (r *Replica) applyScoped(ctx context.Context, ws m5Writes, rep *Report) {
	if len(ws.ws) == 0 {
		return
	}
	var res Result
	err := rep.WriteErr
	if !errors.Is(err, ErrStall) && !errors.Is(err, ErrAborted) && !errors.Is(err, ErrOff) {
		sws := make([]ScopedWrite, len(ws.ws))
		for i, w := range ws.ws {
			sws[i] = w.ScopedWrite
		}
		res, err = r.writer.ApplyScoped(ctx, sws)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appliedScoped(ws, res, err, rep)
}

// appliedScoped records what the scoped path did: each bit written joins
// the ever-had set and is logged, and a room edge the batch tried (it got
// past the beat) is forgotten. r.mu is held.
func (r *Replica) appliedScoped(ws m5Writes, res Result, err error, rep *Report) {
	m := &r.m5
	rep.Wrote += len(res.Wrote)
	if rep.WriteErr == nil && err != nil {
		rep.WriteErr = err
	}
	by := map[uint32]m5Write{}
	var tried uint16
	for _, w := range ws.ws {
		by[w.Addr] = w
		if w.Kind == wire.ScopeRoomEdge {
			for _, no := range w.Switches() {
				tried |= 1 << (no - addr.SwitchRoomFirst)
			}
		}
	}
	var lines []string
	for _, b := range res.Wrote {
		w, ok := by[b.Addr]
		if !ok {
			continue
		}
		sw := w.ScopedWrite
		sw.Bits = b.New &^ b.Old
		for _, no := range sw.Switches() {
			m.mark(w.key, no)
			m.applied++
		}
		lines = append(lines, "set the partner's "+sw.String())
	}
	if e := m.edge; e != nil && e.seq == ws.seq && (res.Beat != 0 || errors.Is(err, ErrOff)) {
		if e.bits &^= tried; e.bits == 0 {
			m.edge = nil
		}
	}
	if len(lines) > 0 {
		r.logf("co-op: room puzzles: wrote %d switch bytes after heartbeat %d", len(lines), res.Beat)
		r.logLines(lines)
	}
}
