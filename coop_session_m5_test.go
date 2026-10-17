package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// M5 through the session plumbing (§9 M5): live room puzzles between two
// writing replicas of a --coop session, the dan, zone and room switches a
// co-present partner sets, with the write audit over every byte the
// pipelines wrote and a tap on every SCOPED either sent or got.
//
// The fixture is tablestest's vanilla disc with Dragon Roost Cavern
// (M_NewD2, save table 3) holding the M5 switches: latching crystals
// (SW_HIT0 without a timer) in room 1 for eight dan, eight zone and eight
// room switches, a timed crystal for one of each, which clears its switch
// when its timer runs out (daSwhit0_c::actionOnTimer,
// d_a_swhit0.cpp:346-362) and so never syncs (§6.2), and a room 2 with two
// latching zone crystals and one room crystal of its own. Room 0 has none:
// a zone or room switch set there does not latch.

// The fixture's switches. Of the eight latching ones of each store, the
// scripted play uses the first three (coM5Scripted), the random play the
// next three, and the last two are left for the end, so a partner's
// switch is new to both games in each part: a game gets a dan or zone
// switch once per scope at most (its ever-had set).
const (
	coDanOK, coDanOK2, coDanTimed    = 0x85, 0x87, 0x86
	coZoneOK, coZoneOK2, coZoneTimed = 0xC3, 0xC5, 0xC4
	coRoomOK, coRoomOK2, coRoomTimed = 0xE1, 0xE3, 0xE2
)

var (
	coM5Dan   = []int{coDanOK, coDanOK2, 0x88, 0x89, 0x8A, 0x8B, 0x8C, 0x8D}
	coM5Zone  = []int{coZoneOK, coZoneOK2, 0xC6, 0xC7, 0xC8, 0xCA, 0xCB, 0xCC}
	coM5Room  = []int{coRoomOK, coRoomOK2, 0xE4, 0xE5, 0xE6, 0xE7, 0xE8, 0xE9}
	coM5Zone2 = []int{coZoneOK, 0xC9} // room 2's latching zone switches
	coM5Room2 = []int{coRoomOK}       // and its room switch
)

// coM5Scripted, coM5Random and coM5End are the indexes of coM5Dan,
// coM5Zone and coM5Room each part of the test plays.
const (
	coM5Scripted = 0
	coM5Random   = 3
	coM5End      = 6
)

// coM5Stages is tablestest.Vanilla with DRC's rooms 1 and 2 holding the M5
// switches.
func coM5Stages() []stagetest.Stage {
	timed := func(sw int) stagetest.Actor {
		return stagetest.Actor{Name: "SW_HIT0", Params: 0x10<<20 | uint32(sw), Angle: [3]int16{0, 0, 0xFF}, SetID: 0xFFFF}
	}
	extra := []stagetest.Actor{
		tablestest.Crystal(0x20), tablestest.PushBlock(1, 0x30, 0x31),
		tablestest.Enemy("Bk", 1), tablestest.Enemy("Bk", 1), tablestest.Enemy("c_green", 2), tablestest.Enemy("Bk", 0xFFFF),
		timed(coDanTimed), timed(coZoneTimed), timed(coRoomTimed),
	}
	for _, sws := range [][]int{coM5Dan, coM5Zone, coM5Room} {
		for _, no := range sws {
			extra = append(extra, tablestest.Crystal(uint32(no)))
		}
	}
	var room2 []stagetest.Actor
	for _, no := range append(append([]int(nil), coM5Zone2...), coM5Room2...) {
		room2 = append(room2, tablestest.Crystal(uint32(no)))
	}
	stages := tablestest.Vanilla()
	for i, s := range stages {
		if s.Name == coDRC.Name {
			stages[i] = tablestest.Dungeon(coDRC.Name, keys.TableDRC, extra...)
			stages[i].Rooms = append(stages[i].Rooms, tablestest.Room(2, room2...))
		}
	}
	return stages
}

// coSwitchAt is a stage switch in its scope, as the tests record it: a
// dan switch's stage and table (room -1), a zone or room switch's stage,
// table and room.
type coSwitchAt struct {
	stage string
	table int8
	room  int8
	no    int
}

func (a coSwitchAt) String() string {
	if a.room < 0 {
		return fmt.Sprintf("dan switch 0x%02X of %s (table %d)", a.no, a.stage, a.table)
	}
	return fmt.Sprintf("switch 0x%02X of %s (table %d, room %d)", a.no, a.stage, a.table, a.room)
}

// coZoneAt is the stage and zone table as a write was made: the stage,
// mStayNo, every room's mZoneNo and every zone's mRoomNo.
type coZoneAt struct {
	stage  string
	stay   int8
	zoneNo [addr.RoomCount]int8
	roomNo [addr.ZoneCount]int8
}

func coZoneAtOf(m mem.Mem) (coZoneAt, error) {
	var z coZoneAt
	p, err := coPlaceOf(m)
	if err != nil {
		return z, err
	}
	z.stage, z.stay = p.stage, p.room
	for room := range addr.RoomCount {
		a, _ := addr.ZoneNoAddr(room)
		b, err := mem.ReadU8(m, a)
		if err != nil {
			return z, err
		}
		z.zoneNo[room] = int8(b)
	}
	for slot := range addr.ZoneCount {
		a, _ := addr.ZoneAddr(slot)
		b, err := mem.ReadU8(m, a+addr.ZoneOffRoomNo)
		if err != nil {
			return z, err
		}
		z.roomNo[slot] = int8(b)
	}
	return z, nil
}

// coScopedRegion reports whether a is in mDan.mSwitch or the zones, the
// bytes whose writes coBeat records the zone table for.
func coScopedRegion(a uint32) bool {
	return a >= addr.DanSwitch && a < addr.Zones+addr.ZoneCount*addr.ZoneSize
}

// coAuditM5Write checks write w at a dan or zone switch byte
// (TargetScoped) by M5's rules (§9 M5), against its tick's report, and
// returns the switches it set in their scopes:
//
//   - one byte, setting bits and clearing none, ARMED and IDLE with the
//     gates open;
//   - a zone byte only in mSwitch[0..2] of the slot that, as it was made,
//     was the zone of the room Link was in: mStayNo's mZoneNo names the
//     slot and the slot's mRoomNo is mStayNo (never another room's slot,
//     a freed one, or one for an invalid mZoneNo);
//   - every switch it sets one tb marks latching for its scope, so no
//     timed switch, nothing a room without the switch's writers holds, and
//     no number past 0xEF, on which the game asserts.
func coAuditM5Write(w coWrite, rep replica.Report, tb *tables.Tables, bad func(format string, args ...any)) []coSwitchAt {
	if len(w.val) != 1 || w.zone == nil {
		bad("%d bytes (zone table recorded: %v): the scoped path writes bytes", len(w.val), w.zone != nil)
		return nil
	}
	old, val := w.old[0], w.val[0]
	switch {
	case old&^val != 0:
		bad("a clear")
	case old == val:
		bad("wrote what was there")
	case rep.State != replica.StateArmed:
		bad("a scoped write while %s", rep.State)
	case !rep.Gates.CanWriteIdle():
		bad("a scoped write, not IDLE with the gates open: %s", rep.Gates.Why())
	}
	z := w.zone
	sw := replica.ScopedWrite{Kind: wire.ScopeDan, Addr: w.a, Bits: val &^ old, Room: -1, Slot: -1}
	if w.a >= addr.Zones {
		slot := int(w.a-addr.Zones) / addr.ZoneSize
		rel := int(w.a-addr.Zones) % addr.ZoneSize
		switch {
		case rel < addr.ZoneOffSwitch || rel >= addr.ZoneOffItem:
			bad("zone byte +%d is no switch byte", rel)
			return nil
		case z.stay < 0 || int(z.stay) >= addr.RoomCount:
			bad("a zone write with mStayNo %d", z.stay)
			return nil
		case int(z.zoneNo[z.stay]) != slot || int(z.roomNo[slot]) != int(z.stay):
			bad("zone slot %d is not the zone of room %d (its mZoneNo %d, the slot's mRoomNo %d)", slot, z.stay, z.zoneNo[z.stay], z.roomNo[slot])
			return nil
		}
		sw = replica.ScopedWrite{Kind: wire.ScopeZone, Addr: w.a, Bits: val &^ old, Room: z.stay, Slot: int8(slot)}
	}
	nos := sw.Switches()
	if len(nos) == 0 {
		bad("no switch of a store")
	}
	var out []coSwitchAt
	for _, no := range nos {
		if !tb.SyncedScopedSwitch(int(w.table), z.stage, int(sw.Room), no) {
			bad("switch 0x%02X does not latch in %s (table %d, room %d)", no, z.stage, w.table, sw.Room)
		}
		out = append(out, coSwitchAt{stage: z.stage, table: w.table, room: sw.Room, no: no})
	}
	return out
}

// coM5Seen is what a game's stage switches have been, by its own memory,
// read after every change the test makes and every step: the dan and zone
// switches it ever had in each scope (the test's own ever-had set), and
// the rising edges of its room switches, which are what a sender relays.
type coM5Seen struct {
	had   map[coSwitchAt]bool
	edges map[coSwitchAt]int
	last  map[coSwitchAt]bool // the room switches set at the last look (no -1 marks the scope)
}

func newCoM5Seen() *coM5Seen {
	return &coM5Seen{had: map[coSwitchAt]bool{}, edges: map[coSwitchAt]int{}, last: map[coSwitchAt]bool{}}
}

// coM5Now is the dan, zone and room switches cg's game has set now, in
// their scopes: nil off a stage.
func coM5Now(cg *coGame) []coSwitchAt {
	p, err := coPlaceOf(cg.m)
	if err != nil {
		cg.t.Fatal(err)
	}
	if p.stage == "" || p.stage == "sea_T" || p.stage == "Name" || !addr.ValidTable(int(p.table)) {
		return nil
	}
	var out []coSwitchAt
	dan := cg.read(addr.DanSwitch, 8)
	for w := range 2 {
		bits := binary.BigEndian.Uint32(dan[4*w:])
		for b := range 32 {
			if bits&(1<<b) != 0 {
				out = append(out, coSwitchAt{stage: p.stage, table: p.table, room: -1, no: addr.SwitchDanFirst + 32*w + b})
			}
		}
	}
	for room := range addr.RoomCount {
		a, _ := addr.ZoneNoAddr(room)
		slot := int(int8(cg.read(a, 1)[0]))
		if slot < 0 || slot >= addr.ZoneCount {
			continue
		}
		za, _ := addr.ZoneAddr(slot)
		if int8(cg.read(za+addr.ZoneOffRoomNo, 1)[0]) != int8(room) {
			continue
		}
		words := cg.read(za+addr.ZoneOffSwitch, 6)
		for w := range 3 {
			bits := binary.BigEndian.Uint16(words[2*w:])
			for b := range 16 {
				if bits&(1<<b) != 0 {
					out = append(out, coSwitchAt{stage: p.stage, table: p.table, room: int8(room), no: addr.SwitchZoneFirst + 16*w + b})
				}
			}
		}
	}
	return out
}

// look records what cg's game has now.
func (s *coM5Seen) look(cg *coGame) {
	now := map[coSwitchAt]bool{}
	for _, at := range coM5Now(cg) {
		s.had[at] = true
		if addr.ClassifySwitch(at.no) == addr.SwitchRoom {
			now[at] = true
			if !s.last[at] {
				s.edges[at]++
			}
		}
	}
	s.last = now
}

// coM5Has reports whether cg's game has switch no of room (-1: a dan
// switch) set now; false if the room has no zone.
func coM5Has(cg *coGame, no, room int) bool {
	cg.t.Helper()
	if addr.ClassifySwitch(no) != addr.SwitchDan {
		if z, err := cg.g.ZoneOf(room); err != nil || z < 0 {
			return false
		}
	}
	on, err := cg.g.IsSwitch(no, room)
	if err != nil {
		cg.t.Fatalf("IsSwitch(0x%02X, %d): %v", no, room, err)
	}
	return on
}

// coScopedTap checks every SCOPED that crosses the wire, either way: each
// bit of a dan, zone or room-edge word must be a switch the fixture's
// tables mark latching in the scope it names. A timed or otherwise unsafe
// switch never travels.
type coScopedTap struct {
	tb *tables.Tables

	mu   sync.Mutex
	n    map[wire.ScopeKind]int
	bad  []string
	seen int
}

func (tp *coScopedTap) check(who string, dir wire.Dir, p []byte) {
	if len(p) == 0 || wire.Sub(p[0]) != wire.SubScoped {
		return
	}
	m, err := wire.Decode(p, dir)
	sc, ok := m.(*wire.Scoped)
	var bad []string
	switch {
	case err != nil || !ok:
		bad = append(bad, fmt.Sprintf("%s: a %s SCOPED that does not decode: %v", who, dir, err))
	case sc.Kind == wire.ScopeKill:
	default:
		for _, w := range sc.Words {
			first, width, room := addr.SwitchDanFirst, 32, int(sc.Room)
			if sc.Kind == wire.ScopeDan {
				room = -1
			} else {
				first, width = addr.SwitchZoneFirst, 16
			}
			for b := range width {
				if w.Bits&(1<<b) == 0 {
					continue
				}
				no := first + int(w.Word)*width + b
				if (sc.Kind == wire.ScopeRoomEdge) != (addr.ClassifySwitch(no) == addr.SwitchRoom) ||
					!tp.tb.SyncedScopedSwitch(int(sc.Table), sc.Stage.String(), room, no) {
					bad = append(bad, fmt.Sprintf("%s: a %s %v carries switch 0x%02X of %s (table %d, room %d), which does not latch there", who, dir, sc.Kind, no, sc.Stage, sc.Table, sc.Room))
				}
			}
		}
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if ok {
		tp.n[sc.Kind]++
	}
	tp.bad = append(tp.bad, bad...)
}

// newCoPlayerM5 is newCoPlayerOver with every SCOPED the pipeline sends
// or gets checked by tap.
func newCoPlayerM5(t *testing.T, ctx context.Context, h *coHost, cs *coopSession, cg *coGame, name string, audit *coAudit, tap *coScopedTap, seed uint64) *coPlayer {
	t.Helper()
	beat := newCoBeat(cg)
	p := &coPlayer{coGame: cg, name: name, rep: &coLog{}, beat: beat}
	s, err := cs.newSync(ctx, name, beat, p.rep)
	if err != nil {
		t.Fatal(err)
	}
	p.s = s
	p.u = &coWire{rng: rand.New(rand.NewPCG(seed, 7)), r: s.r, audit: audit}
	s.client.OnCoop = func(b []byte) {
		tap.check(name, wire.Down, b)
		p.u.onCoop(b)
	}
	session := func(ctx context.Context, send func([]byte) error) error {
		return p.u.session(ctx, func(b []byte) error {
			tap.check(name, wire.Up, b)
			return send(b)
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, s.client, h.addr, nil, session)
	}()
	t.Cleanup(func() { <-done })
	return p
}

// TestCoopSessionM5 is M5's acceptance test through the --coop session
// plumbing: the host founds a world from A's file and runs A's pipeline in
// its own session; B joins from a session of its own; both take their
// stage tables from --coop-iso (the M5 fixture) and write. Scripted play
// first, in DRC:
//
//   - both in room 1, A sets a latching dan, zone and room switch and a
//     timed one of each: B gets the three latching ones, never the timed;
//   - B in room 2 gets nothing of what A sets in room 1, then on coming
//     back the zone switch, but not the room switch, whose edge passed;
//   - B in the boss stage, same table, gets no dan switch A sets in
//     M_NewD2 until it comes back; the reload wiped B's zones, and A's zone
//     switches B had are not set again (B's own game cleared them);
//   - B's room-1 zone moves to another slot, with room 5's zone taking the
//     old one: A's next zone switch lands in the new slot only;
//   - B's game clears a switch it got: it is never set again.
//
// Then both play at random: latching, timed and unsafe switches in rooms
// 0-2, games clearing them, room changes, zones moving, stage changes and
// reloads with and without wiping the zones, events, and M3 progress,
// while the connections deliver out of order, repeat messages and drop,
// and the host restarts from its world file. At the end both are in room 1
// and set switches new to both games, and:
//
//   - co-present players converge: each has every latching dan and zone
//     switch of the other's in their scope that its own game never had,
//     and the fresh ones, room switches included;
//   - nothing B's pipeline set was a switch A's game never had in that
//     scope, nor the other way round; no room switch was set more often
//     than the partner's game raised it, and a room edge is not replayed
//     after a room change;
//   - the write audit (coAuditM3 with coAuditM5Write) holds over every
//     byte either pipeline wrote: no clear, no wrong slot, no switch set
//     twice in a scope, nothing while a gate was closed, and every earlier
//     rule; no SCOPED carried an unsafe switch either way;
//   - the startup lines and /status say what the room puzzles do, where
//     each player is, and what was written and refused.
func TestCoopSessionM5(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(coM5Stages()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    strings.NewReader(""),
		tables:   loadCoopStageTables,
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true, iso: iso})
	defer h.stop()
	cs := h.cs
	tb := cs.m1()
	if tb == nil {
		t.Fatalf("the host has no stage tables:\n%s", h.rep)
	}
	for _, c := range []struct {
		room, no int
		want     bool
	}{
		{-1, coDanOK, true}, {-1, coDanTimed, false},
		{1, coZoneOK, true}, {1, coZoneTimed, false}, {0, coZoneOK, false}, {2, coZoneOK, true}, {2, coZoneOK2, false},
		{1, coRoomOK, true}, {1, coRoomTimed, false}, {0, coRoomOK, false}, {2, coRoomOK, true},
	} {
		if got := tb.SyncedScopedSwitch(addr.TableDRC, coDRC.Name, c.room, c.no); got != c.want {
			t.Fatalf("the fixture's verdict on 0x%02X in room %d is %v, want %v", c.no, c.room, got, c.want)
		}
	}
	pol := cs.policy()
	m4 := newCoM4(t, tb, pol)
	m3 := newCoM3(pol)
	m3.without(&m4.derived)

	joinIn, joinKeys := coInput(t)
	jlog := &coLog{}
	jcs := startCoopSession(jlog, coopFlags{on: true, yes: true, iso: iso}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: joinIn, tables: loadCoopStageTables})
	for _, l := range []*coLog{h.rep, jlog} {
		if !l.has("co-op: room puzzles this session: a partner's latching dan switches (8 in 1 save table(s)) sync while both are in the same stage, its zone switches (10) while both are in the same room, and its room switches (9) as it sets them, in 2 room(s)") {
			t.Errorf("the session did not say what of M5 it writes:\n%s", l)
		}
	}
	audit := &coAudit{}
	tap := &coScopedTap{tb: tb, n: map[wire.ScopeKind]int{}}
	a := newCoPlayerM5(t, ctx, h, cs, ga, "Link", audit, tap, 3)
	b := newCoPlayerM5(t, ctx, h, jcs, gb, "Tetra", audit, tap, 4)
	players := []*coPlayer{a, b}
	other := map[*coPlayer]*coPlayer{a: b, b: a}
	seen := map[*coPlayer]*coM5Seen{a: newCoM5Seen(), b: newCoM5Seen()}
	logs := func() string { return fmt.Sprintf("host:\n%s\nA:\n%s\nB:\n%s", h.rep, a.rep, b.rep) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// do is a change the test makes to p's game, which p's record of its
	// switches then looks at.
	do := func(p *coPlayer, err error) {
		t.Helper()
		must(err)
		seen[p].look(p.coGame)
	}

	prevWorld := h.world()
	stepAll := func() {
		t.Helper()
		for _, p := range players {
			p.step()
			seen[p].look(p.coGame)
		}
		cur := h.world()
		if why := coMonotone(&prevWorld, &cur); why != "" {
			t.Fatalf("the world went backwards: %s", why)
		}
		prevWorld = cur
	}
	run := func(n int) {
		t.Helper()
		for range n {
			stepAll()
			time.Sleep(time.Millisecond)
		}
	}
	until := func(why string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("%s\n%s", why, logs())
			}
			stepAll()
			time.Sleep(2 * time.Millisecond)
		}
	}
	welcomes := func(p *coPlayer) int { return p.rep.count("welcome to world") }
	need := map[*coPlayer]int{}
	const want = hub.FeaturePuzzle | hub.FeatureSwitches | hub.M4Features
	waitArmed := func(why string) {
		t.Helper()
		until(why+": not armed with M5's features", func() bool {
			for _, p := range players {
				if st := p.s.r.Status(); st.State != replica.StateArmed || st.Features&want != want || welcomes(p) < need[p] {
					return false
				}
			}
			return true
		})
	}
	// scopedWrites counts the scoped bytes p's pipeline has written.
	scopedWrites := func(p *coPlayer) int {
		n := 0
		for _, w := range p.beat.written() {
			if w.zone != nil {
				n++
			}
		}
		return n
	}
	// settled reports whether p's last tick wrote no scoped byte. A zone
	// wiped or moved while Link stays in its room takes the console a
	// stage reload, seconds of frames (or a save state, which the replica
	// sees), so the scoped path's verify, on the next tick, sees the
	// write; the fake makes one in a call, which the test makes only on a
	// settled tick.
	settled := func(p *coPlayer) bool {
		ws := p.beat.written()
		return len(ws) == 0 || ws[len(ws)-1].zone == nil || ws[len(ws)-1].tick < len(p.reps)-1
	}
	// together waits until the hub and both replicas have both players in
	// room room of DRC, its zone up: what a partner's room edge needs.
	drcRoom := func(room int) world.Place {
		return world.Place{Stage: coDRC.Name, Table: addr.TableDRC, Room: room}
	}
	together := func(room int) {
		t.Helper()
		at := drcRoom(room)
		until(fmt.Sprintf("the players are never together in room %d", room), func() bool {
			hs, err := h.cs.hub.Status()
			if err != nil || len(hs.Peers) != 2 {
				return false
			}
			for _, ps := range hs.Peers {
				if ps.Place != at {
					return false
				}
			}
			for _, p := range players {
				if st := p.s.r.Status().Puzzle; st.Place != at || st.Zone < 0 {
					return false
				}
			}
			return true
		})
		run(gate.StableTicks + 2)
	}
	transit := func(p *coPlayer, s fakegame.Stage) {
		t.Helper()
		run(gate.StableTicks + 1)
		do(p, p.g.SetNextStage(s))
		run(3)
		do(p, p.g.ChangeStage())
	}
	waitArmed("binding")
	for _, p := range players {
		if !p.rep.has("co-op: room puzzles: written to this game when idle: the partner's latching dan switches (same stage and save table) and zone switches (same room), and its room switches as it sets them, while both are in the room") {
			t.Errorf("%s did not say the room puzzles are written:\n%s", p.name, p.rep)
		}
	}
	for _, p := range players {
		do(p, p.g.LoadStage(coDRCKeys))
	}
	together(1)

	// Latching switches cross; timed ones never do.
	for _, no := range []int{coDanOK, coDanTimed} {
		do(a, a.g.OnSwitch(no, 1))
	}
	for _, no := range []int{coZoneOK, coZoneTimed, coRoomOK, coRoomTimed} {
		do(a, a.g.OnSwitch(no, 1))
	}
	until("A's latching dan, zone and room switches never reached B", func() bool {
		return coM5Has(b.coGame, coDanOK, -1) && coM5Has(b.coGame, coZoneOK, 1) && coM5Has(b.coGame, coRoomOK, 1)
	})
	run(3 * 10)
	for _, no := range []int{coDanTimed, coZoneTimed, coRoomTimed} {
		if coM5Has(b.coGame, no, 1) {
			t.Fatalf("B got A's timed switch 0x%02X", no)
		}
	}

	// Apart: B in room 2 gets nothing of room 1, then the zone switch on
	// coming back, but not the room switch, whose edge has passed.
	do(b, b.g.EnterRoom(2, 1))
	until("B is never in room 2", func() bool { return b.s.r.Status().Puzzle.Place == drcRoom(2) })
	run(gate.StableTicks + 2)
	before := scopedWrites(b)
	do(a, a.g.OnSwitch(coZoneOK2, 1))
	do(a, a.g.OnSwitch(coRoomOK2, 1))
	run(40)
	if n := scopedWrites(b) - before; n != 0 || coM5Has(b.coGame, coZoneOK2, 1) {
		t.Fatalf("B, in room 2, wrote %d scoped bytes of A's room-1 switches", n)
	}
	do(b, b.g.EnterRoom(1, 2))
	until("A's zone switch never reached B back in room 1", func() bool { return coM5Has(b.coGame, coZoneOK2, 1) })
	run(40)
	if coM5Has(b.coGame, coRoomOK, 1) || coM5Has(b.coGame, coRoomOK2, 1) {
		t.Fatal("a room edge was replayed into B after its room change")
	}
	if !coM5Has(a.coGame, coRoomOK, 1) || !coM5Has(a.coGame, coRoomOK2, 1) {
		t.Fatal("A lost its room switches")
	}

	// A dan switch of M_NewD2 does not reach B in the boss stage, though
	// the table and so mDan are the same, until B is back. The reload
	// wiped B's zones (a start point >= 0, d_stage.cpp:124-126): A's zone
	// switches that B had are not set again.
	transit(b, coDRCBoss)
	until("B is never in the boss stage", func() bool { return b.s.r.Status().Puzzle.Place.Stage == coDRCBoss.Name })
	run(gate.StableTicks + 2)
	if !coM5Has(b.coGame, coDanOK, -1) {
		t.Fatal("B's dan switch did not survive a stage of the same table")
	}
	before = scopedWrites(b)
	do(a, a.g.OnSwitch(coDanOK2, 1))
	run(40)
	if n := scopedWrites(b) - before; n != 0 || coM5Has(b.coGame, coDanOK2, -1) {
		t.Fatalf("B, in another stage, wrote %d scoped bytes of A's dan switch", n)
	}
	transit(b, coDRCKeys)
	until("A's dan switch never reached B back in its stage", func() bool { return coM5Has(b.coGame, coDanOK2, -1) })
	together(1)
	run(40)
	if coM5Has(b.coGame, coZoneOK, 1) || coM5Has(b.coGame, coZoneOK2, 1) {
		t.Fatal("A's zone switches were set again in B, whose own game had cleared them")
	}

	// B's room-1 zone moves to another slot, room 5's taking the old one:
	// A's next zone switch lands in the new slot.
	from, to, err := b.g.MoveZone(1, 5)
	do(b, err)
	if from == to {
		t.Fatalf("room 1's zone stayed in slot %d", from)
	}
	run(gate.StableTicks + 2)
	do(a, a.g.OnSwitch(coM5Zone[2], 1))
	until("A's zone switch never reached B's moved zone", func() bool { return coM5Has(b.coGame, coM5Zone[2], 1) })
	if coM5Has(b.coGame, coM5Zone[2], 5) {
		t.Fatal("A's zone switch landed in room 5's zone")
	}
	run(3) // the write's verify, on the next tick

	// B's game clears it: it is never set again.
	do(b, b.g.OffSwitch(coM5Zone[2], 1))
	run(60)
	if coM5Has(b.coGame, coM5Zone[2], 1) {
		t.Fatal("a switch B's own game cleared was set again")
	}

	// Random play, with drops and a host restart.
	rng := rand.New(rand.NewPCG(2026, 1015))
	pend := map[*coPlayer]int{}
	busy := map[*coPlayer]int{}
	keep := fakegame.Stage{Name: coDRC.Name, Point: -1, Room: 1, Table: addr.TableDRC, KeyHUD: true} // keeps the zones, clearing their room switches (d_stage.cpp:124-126, 134-138)
	stages := []fakegame.Stage{coDRCKeys, coDRCKeys, keep, coDRCBoss, coSea}
	back := []fakegame.Stage{coDRCKeys, keep}
	rnd := func(sws []int) []int { return slices.Clip(sws[coM5Random:coM5End]) }
	press := map[int][]int{
		-1: slices.Concat(rnd(coM5Dan), []int{coDanTimed}),
		0:  {coZoneOK, coRoomOK, coM5Dan[coM5Random]}, // nothing in room 0 latches but the dan switch
		1:  slices.Concat(rnd(coM5Zone), rnd(coM5Room), []int{coZoneTimed, coRoomTimed, coM5Dan[coM5Random+1], coDanTimed}),
		2:  {coM5Zone2[0], coM5Zone2[1], coM5Room2[0], coZoneOK2, coM5Dan[coM5Random+2]},
	}
	play := func(p *coPlayer) {
		t.Helper()
		if pend[p] > 0 {
			return
		}
		stay := p.g.StayNo()
		at, err := coPlaceOf(p.m)
		must(err)
		inDRC := at.stage == coDRC.Name
		zone := false
		if stay >= 0 {
			z, err := p.g.ZoneOf(stay)
			zone = err == nil && z >= 0
		}
		switch k := rng.IntN(24); {
		case k < 4 && !inDRC:
			// Back to the dungeon, where the partner mostly is.
			pend[p] = 4
			do(p, p.g.SetNextStage(back[rng.IntN(len(back))]))
		case k < 8 && p.table() == addr.TableDRC && zone:
			nos := press[-1]
			if inDRC {
				nos = press[stay]
			}
			if nos == nil {
				return
			}
			do(p, p.g.OnSwitch(nos[rng.IntN(len(nos))], stay))
		case k < 9 && p.table() == addr.TableDRC && zone:
			// A timer runs out (d_a_swhit0.cpp:346-362), or a switch
			// that does not latch in this room is cleared. A latching
			// switch only the game's resets clear (initDan,
			// clearRoomSwitch, a zone wiped or moved), which the rest
			// of the play makes; the scripted part cleared one.
			nos := []int{coDanTimed, coZoneTimed, coRoomTimed, coZoneOK2, coRoomOK}
			do(p, p.g.OffSwitch(nos[rng.IntN(len(nos))], stay))
		case k < 11 && inDRC:
			r := rng.IntN(3)
			do(p, p.g.EnterRoom(r, (r+1)%3))
		case k < 14 && inDRC:
			// Follow the partner, if it is in a room of this stage.
			q := other[p]
			if at, err := coPlaceOf(q.m); err == nil && at.stage == coDRC.Name && at.room >= 0 && int(at.room) != stay {
				do(p, p.g.EnterRoom(int(at.room), stay))
			}
		case k < 15 && zone:
			free := 0
			for z := range addr.ZoneCount {
				if r, err := p.g.ZoneRoom(z); err == nil && r < 0 {
					free++
				}
			}
			if free < 2 || !settled(p) {
				return // MoveZone needs a slot for each room
			}
			for o := 10 + rng.IntN(20); o < addr.RoomCount; o++ {
				if z, err := p.g.ZoneOf(o); err == nil && z < 0 {
					_, _, err := p.g.MoveZone(stay, o)
					do(p, err)
					break
				}
			}
		case k < 16:
			pend[p] = 4
			do(p, p.g.SetNextStage(stages[rng.IntN(len(stages))]))
		case k < 17:
			if busy[p] == 0 {
				busy[p] = 5 + rng.IntN(10)
				p.g.SetEvent(1, 0)
			}
		case k < 18:
			m3.play(p, rng)
			seen[p].look(p.coGame)
		}
	}
	tickDown := func(p *coPlayer) {
		if pend[p] > 0 {
			if pend[p]--; pend[p] == 0 {
				do(p, p.g.ChangeStage())
			}
		}
		if busy[p] > 0 {
			if busy[p]--; busy[p] == 0 {
				p.g.SetEvent(0, 0)
			}
		}
	}
	drop := func(ps ...*coPlayer) {
		for _, p := range ps {
			need[p] = welcomes(p) + 1
		}
	}
	const steps = 1000
	for i := range steps {
		for _, p := range players {
			if rng.IntN(2) == 0 {
				play(p)
			}
			tickDown(p)
		}
		stepAll()
		switch i {
		case steps / 4:
			drop(a)
			a.s.client.Disconnect()
		case steps / 2:
			drop(a, b)
			h.restart()
		case 3 * steps / 4:
			drop(b)
			b.s.client.Disconnect()
		}
		time.Sleep(time.Millisecond)
	}
	for _, p := range players {
		for pend[p] > 0 || busy[p] > 0 {
			tickDown(p)
			stepAll()
		}
	}

	// Quiesce: M3's bytes converge first, as in the M4 test, with room
	// changes and reloads; then both in room 1 set switches new to both
	// games, and every latching dan and zone switch either has converges
	// into the other unless its own game once had it.
	waitArmed("after the play")
	for _, p := range players {
		do(p, p.g.SetNextStage(coDRCKeys))
	}
	run(3)
	for _, p := range players {
		do(p, p.g.ChangeStage())
	}
	deadline := time.Now().Add(40 * time.Second)
	for i := 0; ; i++ {
		for k, p := range players {
			switch (i + 37*k) % 80 {
			case 20:
				do(p, p.g.EnterRoom(2, 1))
			case 30:
				do(p, p.g.EnterRoom(1, 2))
			case 50:
				do(p, p.g.SetNextStage(coDRCKeys))
			case 54:
				do(p, p.g.ChangeStage())
			}
		}
		stepAll()
		w := h.world()
		ok := true
		for _, p := range players {
			ok = ok && m3.of(p.image()) == m3.of(w)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			for _, p := range players {
				got, mw := m3.of(p.image()), m3.of(w)
				t.Errorf("%s: M3 %s", p.name, coDiff(&got, &mw))
			}
			t.Fatalf("M3 did not converge\n%s", logs())
		}
		time.Sleep(time.Millisecond)
	}
	for _, p := range players {
		if at, err := coPlaceOf(p.m); err != nil || at.pending {
			do(p, p.g.ChangeStage())
		}
		if p.g.StayNo() != 1 {
			do(p, p.g.EnterRoom(1, 2))
		}
	}
	together(1)
	for _, f := range []struct {
		p *coPlayer
		i int
	}{{a, coM5End}, {b, coM5End + 1}} {
		do(f.p, f.p.g.OnSwitch(coM5Dan[f.i], 1))
		do(f.p, f.p.g.OnSwitch(coM5Zone[f.i], 1))
		do(f.p, f.p.g.OnSwitch(coM5Room[f.i], 1))
	}
	// lacks lists the latching dan and zone switches of q's game in p's
	// scopes that p lacks and never had, and the fresh switches p lacks.
	lacks := func(p, q *coPlayer) []string {
		var out []string
		has := map[coSwitchAt]bool{}
		for _, at := range coM5Now(p.coGame) {
			has[at] = true
		}
		for _, at := range coM5Now(q.coGame) {
			// p takes q's dan switches and the zone of the room both are
			// in; a room switch is an edge, checked with the fresh ones.
			if at.room != -1 && at.room != 1 || addr.ClassifySwitch(at.no) == addr.SwitchRoom || has[at] || seen[p].had[at] ||
				!tb.SyncedScopedSwitch(int(at.table), at.stage, int(at.room), at.no) {
				continue
			}
			out = append(out, at.String())
		}
		i := coM5End
		if p == a {
			i = coM5End + 1
		}
		for _, no := range []int{coM5Dan[i], coM5Zone[i], coM5Room[i]} {
			room := 1
			if addr.ClassifySwitch(no) == addr.SwitchDan {
				room = -1
			}
			if !coM5Has(p.coGame, no, room) {
				out = append(out, fmt.Sprintf("the fresh switch 0x%02X", no))
			}
		}
		return out
	}
	until("the room puzzles never converged", func() bool { return len(lacks(a, b)) == 0 && len(lacks(b, a)) == 0 })
	run(40)
	for _, p := range players {
		if l := lacks(p, other[p]); len(l) > 0 {
			t.Errorf("%s lacks %s", p.name, strings.Join(l, "; "))
		}
	}
	// The fresh room switches were edges: B leaving and coming back does
	// not get A's again.
	do(b, b.g.EnterRoom(2, 1))
	run(gate.StableTicks + 2)
	do(b, b.g.EnterRoom(1, 2))
	run(40)
	if coM5Has(b.coGame, coM5Room[coM5End], 1) {
		t.Error("A's room switch was replayed into B after its room change")
	}

	// /status: each player's scope and counts, and the host's hub.
	coType(t, joinKeys, "/status")
	until("/status said nothing of M5", func() bool {
		return b.rep.has("Tetra:   room puzzles: on; Link is in M_NewD2 (table 3, room 1), zone slot ")
	})
	for _, s := range []string{"partner switch(es) set, 0 refused as not latching here, "} {
		if !b.rep.has(s) {
			t.Errorf("/status lacks %q:\n%s", s, b.rep)
		}
	}
	hs, err := h.cs.hub.Status()
	must(err)
	for _, l := range hubPuzzleStatus(hs) {
		t.Logf("host /status: %s", l)
	}
	if st := hubPuzzleStatus(hs); len(st) < 3 || !strings.HasPrefix(st[0], "room puzzles allowed; scoped stores held: 1 dan, 1 zone, ") ||
		!slices.ContainsFunc(st, func(l string) bool {
			return strings.Contains(l, "Tetra (bound): in M_NewD2 (table 3, room 1); puzzles granted; 0 scoped bit(s) refused")
		}) {
		t.Errorf("the host's /status lines: %q", st)
	}

	// The audits.
	for _, p := range players {
		st := p.s.r.Status()
		if st.WritesOff != "" || st.Writes.Failed != 0 {
			t.Errorf("%s: writes off %q, %+v\n%s", p.name, st.WritesOff, st.Writes, p.rep)
		}
		n := coAuditM3(t, p)
		if n.tags != 1 {
			t.Errorf("%s: %d tag writes, want the bind", p.name, n.tags)
		}
		q := other[p]
		sets := map[coSwitchAt]int{}
		kinds := map[addr.SwitchStore]int{}
		for _, at := range n.scoped {
			sets[at]++
			kinds[addr.ClassifySwitch(at.no)]++
			switch {
			case addr.ClassifySwitch(at.no) == addr.SwitchRoom:
				if sets[at] > seen[q].edges[at] {
					t.Errorf("%s: %s set %d time(s), but %s's game raised it %d time(s)", p.name, at, sets[at], q.name, seen[q].edges[at])
				}
			case !seen[q].had[at]:
				t.Errorf("%s: %s set, which %s's game never had there", p.name, at, q.name)
			}
		}
		for _, k := range []addr.SwitchStore{addr.SwitchDan, addr.SwitchZone, addr.SwitchRoom} {
			if kinds[k] == 0 {
				t.Errorf("%s set no %v switch: %v", p.name, k, kinds)
			}
		}
		if pz := st.Puzzle; pz.Applied != len(n.scoped) || pz.Refused != 0 {
			t.Errorf("%s: /status counts %d set and %d refused; the audit saw %d set", p.name, pz.Applied, pz.Refused, len(n.scoped))
		}
		if welcomes(p) < 2 || !p.rep.has("armed offline") {
			t.Errorf("%s: %d welcomes, offline %v: the reconnects did not happen", p.name, welcomes(p), p.rep.has("armed offline"))
		}
		t.Logf("%s: %d scoped switches set (%v), %d edges dropped; M3 %v, M4 %v", p.name, len(n.scoped), kinds, st.Puzzle.Dropped, n.cats, n.m4)
	}
	for _, ps := range hs.Peers {
		if ps.ScopedRefused != 0 {
			t.Errorf("the hub refused %d scoped bits of %s, whose sender proposes latching ones only", ps.ScopedRefused, ps.Name)
		}
	}
	tap.mu.Lock()
	for _, s := range tap.bad {
		t.Error(s)
	}
	if tap.n[wire.ScopeDan] == 0 || tap.n[wire.ScopeZone] == 0 || tap.n[wire.ScopeRoomEdge] == 0 {
		t.Errorf("the tap saw SCOPED %v: the wire was not watched", tap.n)
	}
	t.Logf("SCOPED on the wire: %v", tap.n)
	tap.mu.Unlock()
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, s := range audit.bad {
		t.Error(s)
	}
}

// TestLogCoopPuzzles: the startup line says whether this side's replicas
// write the room puzzles, and how many switches its tables mark latching;
// without tables, that none is written.
func TestLogCoopPuzzles(t *testing.T) {
	useTablesDir(t, t.TempDir())
	st, err := loadCoopStageTables(writeImage(t, "ww.iso", tablestest.ISO(coM5Stages())))
	if err != nil {
		t.Fatal(err)
	}
	vanilla, err := loadCoopStageTables(writeImage(t, "vanilla.iso", tablestest.ISO(tablestest.Vanilla())))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		t    *tables.Tables
		want string
	}{
		{"M5 fixture", st.t, "co-op: room puzzles this session: a partner's latching dan switches (8 in 1 save table(s)) sync while both are in the same stage, its zone switches (10) while both are in the same room, and its room switches (9) as it sets them, in 2 room(s); timed and other switches that clear never travel."},
		{"vanilla fixture", vanilla.t, "latching dan switches (0 in 0 save table(s)) sync while both are in the same stage, its zone switches (0) while both are in the same room, and its room switches (0) as it sets them, in 0 room(s)"},
		{"no tables", nil, "co-op: room puzzles are off this session: without co-op stage tables no partner's dan, zone or room switch is known to latch, so none is written."},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &coLog{}
			logCoopPuzzles(l, c.t)
			if !l.has(c.want) || l.count("room puzzles") != 1 {
				t.Errorf("want %q in:\n%s", c.want, l)
			}
		})
	}
	var none *coopStageTables
	if none.scopedVerdict() != nil || (&coopStageTables{}).scopedVerdict() != nil {
		t.Error("a verdict without tables: the hub would keep scoped switches")
	}
	if v := st.scopedVerdict(); v == nil || !v(addr.TableDRC, coDRC.Name, 1, coZoneOK) || v(addr.TableDRC, coDRC.Name, 1, coZoneTimed) {
		t.Error("the session's verdict is not the tables'")
	}
}

// TestPuzzleStatus: /status's M5 line for each way the room puzzles stand.
func TestPuzzleStatus(t *testing.T) {
	in := world.Place{Stage: coDRC.Name, Table: addr.TableDRC, Room: 1}
	on := replica.PuzzleStatus{Granted: true, Tables: true, Place: in, Zone: 2, Applied: 5, Refused: 1, Dropped: 2}
	for _, c := range []struct {
		name string
		st   replica.Status
		want string
	}{
		{"on", replica.Status{Puzzle: on}, "room puzzles: on; Link is in M_NewD2 (table 3, room 1), zone slot 2; 5 partner switch(es) set, 1 refused as not latching here, 2 room edge(s) dropped"},
		{"no zone", replica.Status{Puzzle: replica.PuzzleStatus{Granted: true, Tables: true, Place: in, Zone: -1}},
			"room puzzles: on; Link is in M_NewD2 (table 3, room 1), no zone for the room yet; 0 partner switch(es) set, 0 refused as not latching here, 0 room edge(s) dropped"},
		{"nowhere", replica.Status{Puzzle: replica.PuzzleStatus{Granted: true, Tables: true, Place: world.Nowhere, Zone: -1}},
			"room puzzles: on; Link is in nowhere; 0 partner switch(es) set, 0 refused as not latching here, 0 room edge(s) dropped"},
		{"not granted", replica.Status{Puzzle: replica.PuzzleStatus{Tables: true, Place: in, Zone: 0}},
			"room puzzles: not granted by the host (its co-op stage tables are not this side's, or it turned them off); Link is in M_NewD2 (table 3, room 1), zone slot 0"},
		{"no tables", replica.Status{Puzzle: replica.PuzzleStatus{Granted: true, Place: in, Zone: 0}},
			"room puzzles: granted, but this side has no co-op stage tables, so none is written; Link is in M_NewD2 (table 3, room 1), zone slot 0"},
		{"dry run", replica.Status{DryRun: true, Puzzle: replica.PuzzleStatus{Place: in, Zone: 0}},
			"room puzzles: none in a dry run, which asks the host for the shared image alone; Link is in M_NewD2 (table 3, room 1), zone slot 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := puzzleStatus(c.st); got != c.want {
				t.Errorf("got\n\t%s\nwant\n\t%s", got, c.want)
			}
		})
	}
}

// TestHubPuzzleStatus: a host's /status adds its hub's lines, whether or
// not a pipeline runs: whether the room puzzles are allowed, the scoped
// stores it holds, and each peer's place, grant and refused bits.
func TestHubPuzzleStatus(t *testing.T) {
	in := world.Place{Stage: coDRC.Name, Table: addr.TableDRC, Room: 1}
	hs := hub.Status{
		Features: hub.DefaultFeatures,
		Scopes: []world.Key{
			{Kind: world.ScopeDan, Stage: coDRC.Name, Table: addr.TableDRC, Room: -1},
			{Kind: world.ScopeZone, Stage: coDRC.Name, Table: addr.TableDRC, Room: 1},
			{Kind: world.ScopeZone, Stage: coDRC.Name, Table: addr.TableDRC, Room: 2},
		},
		Peers: []hub.PeerStatus{
			{Name: "Link", State: hub.StateBound, Features: hub.DefaultFeatures, Place: in},
			{Name: "Tetra", State: hub.StateBound, Features: hub.DefaultFeatures &^ hub.TableFeatures, Place: world.Nowhere, ScopedRefused: 3},
		},
	}
	want := []string{
		"room puzzles allowed; scoped stores held: 1 dan, 2 zone, 0 kill",
		"  Link (bound): in M_NewD2 (table 3, room 1); puzzles granted; 0 scoped bit(s) refused by the host's tables",
		"  Tetra (bound): in nowhere; puzzles not granted; 3 scoped bit(s) refused by the host's tables",
	}
	if got := hubPuzzleStatus(hs); !slices.Equal(got, want) {
		t.Errorf("got\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
	off := hs
	off.Features &^= hub.FeaturePuzzle
	off.Scopes, off.Peers = nil, nil
	if got := hubPuzzleStatus(off); !slices.Equal(got, []string{"room puzzles off; scoped stores held: 0 dan, 0 zone, 0 kill"}) {
		t.Errorf("off: %q", got)
	}

	ctx := context.Background()
	l := &coLog{}
	con := newCoopConsole(l, nil)
	con.do(ctx, "/status")
	if l.has("co-op: host:") {
		t.Errorf("a joiner's /status has host lines:\n%s", l)
	}
	con.setHub(func() (hub.Status, error) { return hs, nil })
	con.do(ctx, "/status")
	con.do(ctx, "/warp")
	for _, s := range []string{
		"co-op: /status: no Dolphin runs co-op yet",
		"co-op: host: room puzzles allowed; scoped stores held: 1 dan, 2 zone, 0 kill",
		"co-op: host:   Tetra (bound): in nowhere; puzzles not granted; 3 scoped bit(s) refused by the host's tables",
	} {
		if !l.has(s) {
			t.Errorf("no %q in:\n%s", s, l)
		}
	}
	if n := l.count("co-op: host: room puzzles"); n != 1 {
		t.Errorf("the host lines were said %d times for one /status:\n%s", n, l)
	}
	con.setHub(func() (hub.Status, error) { return hub.Status{}, hub.ErrStopped })
	con.do(ctx, "/status")
	if !l.has("co-op: host: " + hub.ErrStopped.Error()) {
		t.Errorf("a stopped hub's error is not said:\n%s", l)
	}
}

// TestGBAStatus: /status's Tingle Tuner line says whether a Game Boy
// Advance may be linked and what that holds back, and is left out when
// the stage tables share no switch with the Tuner.
func TestGBAStatus(t *testing.T) {
	for _, c := range []struct {
		g    replica.GBAStatus
		want string
	}{
		{replica.GBAStatus{}, ""},
		{replica.GBAStatus{MayLink: true}, ""},
		{replica.GBAStatus{Switches: 61}, "Tingle Tuner: no Game Boy Advance linked; the 61 switch(es) shared with Tingle Tuner switches sync"},
		{replica.GBAStatus{MayLink: true, Switches: 61}, "Tingle Tuner: a Game Boy Advance may be linked to this game, so the 61 switch(es) shared with Tingle Tuner switches are neither proposed nor written until the game drops the link (a reset, or calling Tingle again)"},
	} {
		if got := gbaStatus(replica.Status{GBA: c.g}); got != c.want {
			t.Errorf("%+v: %q, want %q", c.g, got, c.want)
		}
	}
}
