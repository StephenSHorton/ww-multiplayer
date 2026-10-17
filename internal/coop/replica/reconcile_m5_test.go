package replica

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The M5 fixture's switches, all set by actors in Dragon Roost Cavern's
// room 1: latching crystals (SW_HIT0 without a timer) for five of each
// store, and a timed crystal for one, which clears its switch again when
// its timer runs out (daSwhit0_c::actionOnTimer, d_a_swhit0.cpp:346-362),
// so its switch never syncs (§6.2). The unit tests use the first two of
// each; the loopback test the others too.
const (
	danOK, danOK2, danTimed    = 0x85, 0x87, 0x86
	zoneOK, zoneOK2, zoneTimed = 0xC3, 0xC5, 0xC4
	roomOK, roomOK2, roomTimed = 0xE1, 0xE3, 0xE2
)

var (
	m5Dan  = []int{danOK, danOK2, 0x88, 0x89, 0x8A}
	m5Zone = []int{zoneOK, zoneOK2, 0xC6, 0xC7, 0xC8}
	m5Room = []int{roomOK, roomOK2, 0xE4, 0xE5, 0xE6}
)

// timedCrystal is a crystal switch with a timer (switches' SW_HIT0
// decoder): a writer of sw that does not latch.
func timedCrystal(sw uint32) stagetest.Actor {
	return stagetest.Actor{Name: "SW_HIT0", Params: 0x10<<20 | sw, Angle: [3]int16{0, 0, 0xFF}, SetID: 0xFFFF}
}

// m5Stages is tablestest.Vanilla with Dragon Roost Cavern's room 1
// holding the M5 switches besides its vanilla entries.
func m5Stages() []stagetest.Stage {
	extra := m5Extra()
	stages := tablestest.Vanilla()
	for i, s := range stages {
		if s.Name == drc.Name {
			stages[i] = tablestest.Dungeon(drc.Name, keys.TableDRC, extra...)
		}
	}
	return stages
}

// m5Extra is DRC room 1's entries beyond the dungeon's key chests in the
// M5 fixture.
func m5Extra() []stagetest.Actor {
	extra := []stagetest.Actor{
		tablestest.Crystal(0x20), tablestest.PushBlock(1, 0x30, 0x31),
		tablestest.Enemy("Bk", 1), tablestest.Enemy("Bk", 1), tablestest.Enemy("c_green", 2), tablestest.Enemy("Bk", 0xFFFF),
		timedCrystal(danTimed), timedCrystal(zoneTimed), timedCrystal(roomTimed),
	}
	for _, sws := range [][]int{m5Dan, m5Zone, m5Room} {
		for _, no := range sws {
			extra = append(extra, tablestest.Crystal(uint32(no)))
		}
	}
	return extra
}

var m5Fixture = sync.OnceValues(func() (*tables.Tables, error) {
	img := tablestest.ISO(m5Stages())
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

// m5Tables are the M5 fixture's tables, checked to give the verdicts the
// tests rest on.
func m5Tables(t testing.TB) *tables.Tables {
	t.Helper()
	tb, err := m5Fixture()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		room, no int
		want     bool
	}{
		{-1, danOK, true}, {-1, danOK2, true}, {-1, danTimed, false},
		{1, zoneOK, true}, {1, zoneOK2, true}, {1, zoneTimed, false}, {0, zoneOK, false},
		{1, roomOK, true}, {1, roomOK2, true}, {1, roomTimed, false}, {0, roomOK, false},
	} {
		if got := tb.SyncedScopedSwitch(keys.TableDRC, drc.Name, c.room, c.no); got != c.want {
			t.Fatalf("the fixture's verdict on 0x%02X in room %d is %v, want %v", c.no, c.room, got, c.want)
		}
	}
	return tb
}

// m5Features are the features the M5 rigs are welcomed with: the image and
// the room puzzles, nothing that writes M3's or M4's classes.
const m5Features = hub.FeatureImage | hub.FeaturePuzzle

// zoneAt is the zone table as a write was made: the stage, mStayNo, every
// room's mZoneNo and every zone's mRoomNo; and whether a Game Boy Advance
// may have been linked (state.GBALink.MayLink).
type zoneAt struct {
	stage  string
	stay   int8
	zoneNo [addr.RoomCount]int8
	roomNo [addr.ZoneCount]int8
	gba    bool
}

func zoneAtOf(m *mem.FakeMem) (zoneAt, error) {
	var z zoneAt
	p, err := placeOf(m)
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
	g, err := m.Read(addr.GbaCom, addr.GbaComLinkReadLen)
	if err != nil {
		return z, err
	}
	z.gba = state.GBALink{Enable: g[addr.GbaComOffEnable], Context: g[addr.GbaComOffContext]}.MayLink()
	return z, nil
}

// zoneMem is a beatMem that also keeps, for each write it records, the
// zone table as the write was made: the write-time half of the M5 audit.
type zoneMem struct {
	*beatMem
	mu sync.Mutex
	at []zoneAt // by write, as beatMem.written has them
}

func (z *zoneMem) Write(a uint32, d []byte) error {
	at, err := zoneAtOf(z.FakeMem)
	if err != nil {
		return err
	}
	if err := z.beatMem.Write(a, d); err != nil {
		return err
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	z.at = append(z.at, at)
	return nil
}

func (z *zoneMem) zones() []zoneAt {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]zoneAt(nil), z.at...)
}

// m5Frames are the frames the M5 rigs' game runs before each tick: the
// game's 30 a second at TickEvery.
const m5Frames = 3

// m5Rig is a reconciling replica in Dragon Roost Cavern's room 1, bound,
// welcomed with the given features and armed, the hub's WORLD empty, the
// partner in room 1 too and the rooms' actors settled (the room hold).
type m5Rig struct {
	*recRig
	zm *zoneMem
	tb *tables.Tables // the fixture's, the audit's verdict whatever the replica has
}

func newM5Rig(t *testing.T, cfg Config, f hub.Feature) *m5Rig {
	t.Helper()
	rr := &replicaRig{rig: newRig(t, "Link"), hub: &hubEnd{t: t}, log: &logBuf{}}
	rr.load(drcKeys)
	bm := &beatMem{FakeMem: rr.m, game: rr.g}
	zm := &zoneMem{beatMem: bm}
	cfg.Mem, cfg.Logf = zm, rr.log.logf
	if cfg.Policy == nil {
		cfg.Policy = switch5Policy(t)
	}
	if cfg.BeatTimeout == 0 {
		cfg.BeatTimeout = time.Second
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rr.r = r
	rec := &recRig{replicaRig: rr, bm: bm, seq: 1, frames: m5Frames}
	rr.tag(rr.tagFor(testWorld, testNonce))
	r.Connect(rr.hub)
	var sent []wire.Msg
	for range gate.StableTicks + 2 {
		rec.tick()
		sent = append(sent, rr.hub.take()...)
	}
	if _, ok := find[*wire.CoHello](sent); !ok {
		t.Fatalf("no CO_HELLO: %s", subs(sent))
	}
	rec.welcome(f)
	say(t, r, worldOf(1, world.Image{}))
	rec.tick()
	rr.hub.take()
	m5 := &m5Rig{recRig: rec, zm: zm, tb: m5Tables(t)}
	m5.partnerAt(drc, 1)
	if n := m5.settleRooms(); n != 0 {
		t.Fatalf("%d bytes written before any store came", n)
	}
	rr.hub.take()
	return m5
}

// partnerMsg is the partner's PRESENCE as the hub relays it: in game, in
// room room of stage s.
func partnerMsg(t testing.TB, s fakegame.Stage, room int) *wire.Presence {
	t.Helper()
	st, err := wire.MakeStage(s.Name)
	if err != nil {
		t.Fatal(err)
	}
	return &wire.Presence{Stage: st, Table: int8(s.Table), Room: int8(room), Layer: -1, Flags: wire.PresenceInGame | wire.PresenceArmed}
}

func (rr *m5Rig) partnerAt(s fakegame.Stage, room int) {
	rr.t.Helper()
	say(rr.t, rr.r, partnerMsg(rr.t, s, room))
}

// meet is the partner's PRESENCE in room room of stage s, then ticks
// enough, a frame each, for the room hold: what a replicaRig needs before
// a dan or zone store is planned.
func (rr *replicaRig) meet(s fakegame.Stage, room int) {
	rr.t.Helper()
	say(rr.t, rr.r, partnerMsg(rr.t, s, room))
	for range roomSettle + 2 {
		rr.tick()
	}
	rr.hub.take()
}

// partnerGone is the PRESENCE the hub relays for a peer gone
// (hub.leaveView): nowhere.
func (rr *m5Rig) partnerGone() {
	rr.t.Helper()
	say(rr.t, rr.r, &wire.Presence{Table: -1, Room: -1, Layer: -1})
}

// scopedMsg is a SCOPED of kind for stage s, room room (-1 for DAN), whose
// words set switches nos.
func scopedMsg(t testing.TB, kind wire.ScopeKind, s fakegame.Stage, room int, nos ...int) *wire.Scoped {
	t.Helper()
	st, err := wire.MakeStage(s.Name)
	if err != nil {
		t.Fatal(err)
	}
	var words [3]uint32
	for _, no := range nos {
		if addr.ClassifySwitch(no) == addr.SwitchDan {
			i := no - addr.SwitchDanFirst
			words[i/32] |= 1 << (i % 32)
		} else {
			i := no - addr.SwitchZoneFirst
			words[i/16] |= 1 << (i % 16)
		}
	}
	m := &wire.Scoped{Kind: kind, Stage: st, Table: int8(s.Table), Room: int8(room)}
	for w, b := range words {
		if b != 0 {
			m.Words = append(m.Words, wire.ScopedWord{Word: uint8(w), Bits: b})
		}
	}
	return m
}

func danMsg(t testing.TB, s fakegame.Stage, nos ...int) *wire.Scoped {
	return scopedMsg(t, wire.ScopeDan, s, -1, nos...)
}

func zoneMsg(t testing.TB, s fakegame.Stage, room int, nos ...int) *wire.Scoped {
	return scopedMsg(t, wire.ScopeZone, s, room, nos...)
}

func edgeMsg(t testing.TB, s fakegame.Stage, room int, nos ...int) *wire.Scoped {
	return scopedMsg(t, wire.ScopeRoomEdge, s, room, nos...)
}

func (rr *m5Rig) send(ms ...*wire.Scoped) {
	rr.t.Helper()
	for _, m := range ms {
		say(rr.t, rr.r, m)
	}
}

// is reports whether switch no is set for room in the receiver's game.
func (rr *m5Rig) is(no, room int) bool {
	rr.t.Helper()
	on, err := rr.g.IsSwitch(no, room)
	if err != nil {
		rr.t.Fatalf("IsSwitch(0x%02X, %d): %v", no, room, err)
	}
	return on
}

// slot is room's zone slot in the receiver's game.
func (rr *m5Rig) slot(room int) int {
	rr.t.Helper()
	z, err := rr.g.ZoneOf(room)
	if err != nil {
		rr.t.Fatal(err)
	}
	return z
}

// scopedWrites are the writes the scoped path made, with the zone table
// each was made under.
func (rr *m5Rig) scopedWrites() ([]auditWrite, []zoneAt) {
	ws, zs := rr.bm.written(), rr.zm.zones()
	g := NewGuardFor(M3Targets | M4Targets | M5Targets)
	var out []auditWrite
	var at []zoneAt
	for i, w := range ws {
		if g.TargetOf(w.a) == TargetScoped {
			out = append(out, w)
			at = append(at, zs[i])
		}
	}
	return out, at
}

// settleM5 runs enough ticks for a store to be planned and written, and
// the gates and the room hold to settle after a change.
func (rr *m5Rig) settleM5() int { return rr.ticks(gate.StableTicks + 4 + roomSettle/m5Frames + 1) }

// audit is the M5 write audit over everything the replica wrote: the
// scoped path's writes by auditScoped, every other by auditM4Writes.
func (rr *m5Rig) audit() {
	rr.t.Helper()
	ws, zs := rr.bm.written(), rr.zm.zones()
	if len(ws) != len(zs) {
		rr.t.Fatalf("%d writes, %d zone tables recorded", len(ws), len(zs))
	}
	auditM5Writes(rr.t, ws, zs, rr.reps, rr.bm.placesSeen(), rr.tb, rr.r.Status().Writes.Derived)
}

// m5Place is where a dan or zone switch was written: its scope in the
// receiver's game. A dan switch's is its save table alone (stage ""), as
// the game keeps one mDan, which initDan clears only when the table
// changes (dSv_danBit_c::init, d_save.cpp:1225-1235).
type m5Place struct {
	stage string
	table int8
	room  int8 // -1 for a dan switch
	no    int
}

// auditM5Writes is M5's write audit (§9 M5) over every write a replica
// made, zs being the zone table each was made under: a write at a dan or
// zone switch byte (TargetScoped) is one byte, made on a tick that was
// ARMED and IDLE with the gates open, and sets bits, clearing none; each
// bit it sets is a switch tb marks latching for the scope it lands in (so
// none past 0xEF, on which the game asserts); a zone byte is in
// mSwitch[0..2] of the slot that, as it was written, was the zone of the
// room Link was in (the slot's mRoomNo is mStayNo, and mStayNo's mZoneNo
// names the slot); and no dan or zone switch is set twice in one scope
// (m5Place: a bit the game cleared is never set again); and none that
// tb shares with the Tingle Tuner is written while a GBA may be linked
// (zoneAt.gba). Every other write is audited as M4's (auditM4Writes), so the earlier classes' rules all still hold.
func auditM5Writes(t *testing.T, ws []auditWrite, zs []zoneAt, reps []Report, places []gamePlace, tb *tables.Tables, derived int) {
	t.Helper()
	g := NewGuardFor(M3Targets | M4Targets | M5Targets)
	var other []auditWrite
	set := map[m5Place]bool{}
	for i, w := range ws {
		if g.TargetOf(w.a) != TargetScoped {
			other = append(other, w)
			continue
		}
		z, rep := zs[i], reps[w.tick]
		at := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("tick %d, scoped write at 0x%08X (0x%02X -> 0x%02X, stage %q, table %d, room %d): "+format,
				append([]any{w.tick, w.a, w.old, w.val, z.stage, w.table, z.stay}, args...)...)
		}
		switch {
		case w.n != 1:
			at("%d bytes: writes are bytes", w.n)
		case w.old&^w.val != 0:
			at("a clear")
		case w.old == w.val:
			at("wrote what was there")
		case rep.State != StateArmed:
			at("not armed")
		case !rep.Gates.CanWriteIdle():
			at("not IDLE with the gates open: %s", rep.Gates.Why())
		}
		sw := ScopedWrite{Kind: wire.ScopeDan, Addr: w.a, Bits: w.val &^ w.old, Room: -1, Slot: -1}
		room := int8(-1)
		if w.a >= addr.Zones {
			slot := int(w.a-addr.Zones) / addr.ZoneSize
			rel := int(w.a-addr.Zones) % addr.ZoneSize
			switch {
			case rel < addr.ZoneOffSwitch || rel >= addr.ZoneOffItem:
				at("zone byte +%d is no switch byte", rel)
			case z.stay < 0 || int(z.stay) >= addr.RoomCount || int(z.zoneNo[z.stay]) != slot || int(z.roomNo[slot]) != int(z.stay):
				at("zone slot %d is not the zone of room %d (its mZoneNo %d, the slot's mRoomNo %d)",
					slot, z.stay, z.zoneNo[max(z.stay, 0)], z.roomNo[slot])
			}
			room = z.stay
			sw = ScopedWrite{Kind: wire.ScopeZone, Addr: w.a, Bits: w.val &^ w.old, Room: room, Slot: int8(slot)}
		}
		nos := sw.Switches()
		if len(nos) == 0 {
			at("no switch of a store")
		}
		for _, no := range nos {
			if !tb.SyncedScopedSwitch(int(w.table), z.stage, int(room), no) {
				at("switch 0x%02X does not latch there", no)
			}
			if z.gba && tb.GBAScopedSwitch(int(w.table), z.stage, int(room), no) {
				at("switch 0x%02X is shared with the Tingle Tuner, written while a GBA may be linked", no)
			}
			if addr.ClassifySwitch(no) == addr.SwitchRoom {
				continue // a room switch is cleared by every room change
			}
			p := m5Place{table: w.table, room: room, no: no}
			if room >= 0 {
				p.stage = z.stage
			}
			if set[p] {
				at("switch 0x%02X set twice in its scope", no)
			}
			set[p] = true
		}
	}
	auditM4Writes(t, other, reps, places, false, derived)
}

// sw is a switch of a room, for the tests' expectations.
type sw struct{ no, room int }

// TestReconcileM5Classes: each class of the partner's room puzzles is
// written under its gates and in its scope, and not otherwise: the dan
// switches of the stage and table the receiver is in, when IDLE; the zone
// switches of the room it is in, into its own zone for the room; a room
// edge once, in the room, when the gates open within edgeTicks. Switches
// the M1 table does not mark latching are never written.
func TestReconcileM5Classes(t *testing.T) {
	event := func(rr *m5Rig, on bool) {
		if on {
			rr.g.SetEvent(1, 0)
		} else {
			rr.g.SetEvent(0, 0)
		}
	}
	pause := func(rr *m5Rig, on bool) { rr.g.SetPauseMenu(on) }
	otherTable := fakegame.Stage{Name: drc.Name, Table: addr.TableFW}
	for _, c := range []struct {
		name   string
		before func(rr *m5Rig) // the receiver's game, before the store comes
		msgs   func(t *testing.T) []*wire.Scoped
		closed func(rr *m5Rig, on bool) // a gate closed while the store comes, then opened
		hold   int                      // ticks it stays closed
		after  func(rr *m5Rig)          // the receiver's game after the store came
		set    []sw                     // set at the end
		unset  []sw                     // not set at the end
		log    string                   // a line said
	}{
		{name: "a dan switch, in its stage and table",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			set:  []sw{{danOK, 1}}, unset: []sw{{danOK2, 1}}},
		{name: "a dan switch waits out an event",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK, danOK2)} },
			closed: event, hold: 3 * edgeTicks,
			set: []sw{{danOK, 1}, {danOK2, 1}}},
		{name: "a dan switch waits for the pause menu",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			closed: pause, hold: 5,
			set: []sw{{danOK, 1}}},
		{name: "a dan switch of another stage of the table",
			before: func(rr *m5Rig) { rr.splitLoad(drcBoss); rr.settleM5() },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			unset:  []sw{{danOK, 0}}},
		{name: "a dan switch of another save table",
			msgs:  func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, otherTable, danOK)} },
			unset: []sw{{danOK, 1}}},
		{name: "a dan switch that does not latch",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danTimed, danOK)} },
			set:  []sw{{danOK, 1}}, unset: []sw{{danTimed, 1}},
			log: "not written: dan switch 0x86 does not latch"},
		{name: "a zone switch, in its room",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			set:  []sw{{zoneOK, 1}}, unset: []sw{{zoneOK2, 1}}},
		{name: "a zone switch goes into the receiver's own zone for the room",
			before: func(rr *m5Rig) {
				rr.enter(1, 2) // room 2's zone takes slot 1
				if _, _, err := rr.g.MoveZone(1, 5); err != nil {
					rr.t.Fatal(err)
				}
				if rr.slot(1) != 2 || rr.slot(5) != 0 {
					rr.t.Fatalf("room 1 in slot %d, room 5 in %d", rr.slot(1), rr.slot(5))
				}
				rr.settleM5()
			},
			msgs:  func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			set:   []sw{{zoneOK, 1}},
			unset: []sw{{zoneOK, 5}, {zoneOK, 2}}},
		{name: "a zone switch waits out an event",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			closed: event, hold: 3 * edgeTicks,
			set: []sw{{zoneOK, 1}}},
		{name: "a zone switch of another room",
			before: func(rr *m5Rig) { rr.enter(1, 0) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 0, zoneOK)} },
			unset:  []sw{{zoneOK, 1}, {zoneOK, 0}}},
		// The hub keeps a store only while someone is in its scope, and
		// sends it to a player entering: the receiver keeps none for a room
		// it is not in, so it waits for the hub's.
		{name: "a zone switch of the room Link enters later",
			before: func(rr *m5Rig) { rr.enter(2, 1); rr.settleM5() },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			after: func(rr *m5Rig) {
				rr.settleM5()
				rr.enter(1, 2)
			},
			unset: []sw{{zoneOK, 1}}},
		{name: "a zone switch the hub sends as Link enters the room",
			before: func(rr *m5Rig) { rr.enter(2, 1); rr.settleM5(); rr.enter(1, 2) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			set:    []sw{{zoneOK, 1}}},
		{name: "a zone switch kept is dropped as Link leaves the room",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			closed: event, hold: 2,
			after: func(rr *m5Rig) {
				rr.enter(2, 1)
				rr.settleM5()
				rr.enter(1, 2)
			},
			unset: []sw{{zoneOK, 1}}},
		{name: "a zone switch with the partner in another room",
			before: func(rr *m5Rig) { rr.partnerAt(drc, 2) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			unset:  []sw{{zoneOK, 1}}},
		{name: "a zone switch whose sender left while the game was not idle",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneOK)} },
			closed: event, hold: 2,
			after: func(rr *m5Rig) { rr.partnerAt(drc, 2) },
			unset: []sw{{zoneOK, 1}}},
		{name: "a dan switch with the partner in another stage of the table",
			before: func(rr *m5Rig) { rr.partnerAt(drcBoss, 0) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			unset:  []sw{{danOK, 1}}},
		{name: "a dan switch with the partner gone",
			before: func(rr *m5Rig) { rr.partnerGone() },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			unset:  []sw{{danOK, 1}}},
		{name: "a dan switch with the partner in another room",
			before: func(rr *m5Rig) { rr.partnerAt(drc, 2) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{danMsg(t, drc, danOK)} },
			set:    []sw{{danOK, 1}}},
		{name: "a zone switch of the room in another stage",
			msgs:  func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drcBoss, 1, zoneOK)} },
			unset: []sw{{zoneOK, 1}}},
		{name: "a zone switch that does not latch",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{zoneMsg(t, drc, 1, zoneTimed, zoneOK)} },
			set:  []sw{{zoneOK, 1}}, unset: []sw{{zoneTimed, 1}},
			log: "not written: zone switch 0xC4 in room 1 does not latch"},
		{name: "a room edge, in its room",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{edgeMsg(t, drc, 1, roomOK)} },
			set:  []sw{{roomOK, 1}}, unset: []sw{{roomOK2, 1}}},
		{name: "a room edge for another room",
			before: func(rr *m5Rig) { rr.enter(1, 0) },
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{edgeMsg(t, drc, 0, roomOK)} },
			unset:  []sw{{roomOK, 1}, {roomOK, 0}}},
		{name: "a room edge in an event that ends in time",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{edgeMsg(t, drc, 1, roomOK)} },
			closed: event, hold: edgeTicks - 3,
			set: []sw{{roomOK, 1}}},
		{name: "a room edge in an event that outlasts it",
			msgs:   func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{edgeMsg(t, drc, 1, roomOK)} },
			closed: event, hold: edgeTicks + 3,
			unset: []sw{{roomOK, 1}},
			log:   "room edges are never replayed"},
		{name: "a room edge that does not latch",
			msgs: func(t *testing.T) []*wire.Scoped { return []*wire.Scoped{edgeMsg(t, drc, 1, roomTimed, roomOK)} },
			set:  []sw{{roomOK, 1}}, unset: []sw{{roomTimed, 1}},
			log: "not written: zone switch 0xE2 in room 1 does not latch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
			if c.before != nil {
				c.before(rr)
			}
			if c.closed != nil {
				c.closed(rr, true)
				rr.ticks(2)
			}
			n := len(rr.bm.written())
			rr.send(c.msgs(t)...)
			if c.closed != nil {
				if w := rr.ticks(c.hold); w != 0 {
					t.Fatalf("%d bytes written with the gate closed", w)
				}
				c.closed(rr, false)
			}
			if c.after != nil {
				c.after(rr)
			}
			rr.settleM5()
			for _, s := range c.set {
				if !rr.is(s.no, s.room) {
					t.Errorf("switch 0x%02X of room %d not set", s.no, s.room)
				}
			}
			for _, s := range c.unset {
				if rr.slot(s.room) >= 0 || addr.ClassifySwitch(s.no) == addr.SwitchDan {
					if rr.is(s.no, s.room) {
						t.Errorf("switch 0x%02X of room %d set", s.no, s.room)
					}
				}
			}
			if c.log != "" && !rr.log.has(c.log) {
				t.Errorf("the log lacks %q", c.log)
			}
			if len(c.set) == 0 && len(rr.bm.written()) != n {
				t.Errorf("%d bytes written", len(rr.bm.written())-n)
			}
			if t.Failed() {
				t.Fatalf("log:\n%s", rr.log)
			}
			rr.audit()
		})
	}
}

// TestReconcileM5LatchingLineOnce: a switch that does not latch is said
// once while the replica stays armed, however often the store comes.
func TestReconcileM5LatchingLineOnce(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
	for range 5 {
		rr.send(danMsg(t, drc, danTimed), zoneMsg(t, drc, 1, zoneTimed))
		rr.ticks(3)
	}
	for _, line := range []string{"dan switch 0x86 does not latch", "zone switch 0xC4 in room 1 does not latch"} {
		if n := rr.log.count(line); n != 1 {
			t.Fatalf("%q said %d times:\n%s", line, n, rr.log)
		}
	}
	if !rr.log.has("room puzzles: written to this game when idle") || rr.log.count("room puzzles: written") != 1 {
		t.Fatalf("the room puzzles line was not said once:\n%s", rr.log)
	}
	if n := len(rr.bm.written()); n != 0 {
		t.Fatalf("%d bytes written", n)
	}
}

// TestReconcileM5EverHad: a bit the receiver's game had in a scope is
// never written into it, whether the game cleared it after the scoped path
// set it or set and cleared it itself, and a scope entered again gets only
// the bits it never had.
func TestReconcileM5EverHad(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
	rr.send(danMsg(t, drc, danOK), zoneMsg(t, drc, 1, zoneOK))
	rr.settleM5()
	if !rr.is(danOK, 1) || !rr.is(zoneOK, 1) {
		t.Fatalf("the stores were not written:\n%s", rr.log)
	}

	// The game clears both, and sets and clears 0x87 itself: no store may
	// bring them back.
	for _, no := range []int{danOK, zoneOK} {
		if err := rr.g.OffSwitch(no, 1); err != nil {
			t.Fatal(err)
		}
	}
	rr.onSwitch(danOK2, 1)
	rr.ticks(2)
	if err := rr.g.OffSwitch(danOK2, 1); err != nil {
		t.Fatal(err)
	}
	n := len(rr.bm.written())
	for range 3 {
		rr.send(danMsg(t, drc, danOK, danOK2), zoneMsg(t, drc, 1, zoneOK))
		rr.settleM5()
	}
	if w := len(rr.bm.written()) - n; w != 0 || rr.is(danOK, 1) || rr.is(zoneOK, 1) || rr.is(danOK2, 1) {
		t.Fatalf("%d bytes written: bits the game had came back:\n%s", w, rr.log)
	}

	// Out of the room and back: the hub sends room 1's zone store again
	// (hub.onPresence), which has nothing the room never had.
	rr.enter(2, 1)
	rr.settleM5()
	rr.enter(1, 2)
	rr.send(zoneMsg(t, drc, 1, zoneOK))
	rr.settleM5()
	if w := len(rr.bm.written()) - n; w != 0 {
		t.Fatalf("re-entering the room wrote %d bytes:\n%s", w, rr.log)
	}

	// Out of the stage, into another table and back: initDan clears the
	// dan bits, the zones are created anew, and the hub sends the stores
	// again, now with a zone switch the partner set meanwhile. Only that
	// one, which the room never had, is written.
	rr.splitLoad(woods)
	rr.settleM5()
	rr.splitLoad(drcKeys)
	rr.settleM5()
	if rr.is(danOK2, 1) || rr.is(zoneOK, 1) {
		t.Fatal("initDan or the new zone kept old bits")
	}
	rr.send(danMsg(t, drc, danOK, danOK2), zoneMsg(t, drc, 1, zoneOK, zoneOK2))
	rr.settleM5()
	if w := len(rr.bm.written()) - n; w != 1 || !rr.is(zoneOK2, 1) || rr.is(zoneOK, 1) || rr.is(danOK, 1) || rr.is(danOK2, 1) {
		t.Fatalf("the scopes entered again took %d bytes, want the one switch they never had:\n%s", w, rr.log)
	}
	rr.audit()
}

// TestReconcileM5RoomEdge: a room edge is applied once, in the visit it
// came in, and then forgotten: never replayed after a room change, never
// set again within the visit once the game cleared it, dropped if Link is
// not in its room as it comes.
func TestReconcileM5RoomEdge(t *testing.T) {
	t.Run("once, then never replayed", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.settleM5()
		if !rr.is(roomOK, 1) {
			t.Fatalf("the edge was not applied:\n%s", rr.log)
		}
		n := len(rr.bm.written())

		// The game clears it in the same visit; the hub relays it again.
		if err := rr.g.OffSwitch(roomOK, 1); err != nil {
			t.Fatal(err)
		}
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.settleM5()
		if rr.is(roomOK, 1) {
			t.Fatal("an edge set again in the visit the game cleared it in")
		}

		// A room change clears the room switches (zoneCountCheck); coming
		// back, nothing is replayed.
		rr.enter(2, 1)
		rr.settleM5()
		rr.enter(1, 2)
		rr.settleM5()
		if rr.is(roomOK, 1) {
			t.Fatal("the edge was replayed after a room change")
		}
		if w := len(rr.bm.written()) - n; w != 0 {
			t.Fatalf("%d more bytes written", w)
		}

		// A new edge in the new visit is the partner's new press.
		rr.send(edgeMsg(t, drc, 1, roomOK, roomOK2))
		rr.settleM5()
		if !rr.is(roomOK, 1) || !rr.is(roomOK2, 1) {
			t.Fatalf("a new visit's edge was not applied:\n%s", rr.log)
		}
		rr.audit()
	})
	t.Run("dropped at a room change before the gates open", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.enter(1, 2)
		rr.g.SetEvent(1, 0)
		rr.ticks(2)
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.ticks(2)
		rr.enter(2, 1)
		rr.ticks(2)
		rr.enter(1, 2)
		rr.g.SetEvent(0, 0)
		rr.settleM5()
		if rr.is(roomOK, 1) || len(rr.bm.written()) != 0 {
			t.Fatalf("an edge of an earlier visit was applied:\n%s", rr.log)
		}
	})
	t.Run("dropped when Link is elsewhere as it comes", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.enter(2, 1)
		rr.settleM5()
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.ticks(1)
		rr.enter(1, 2)
		rr.settleM5()
		if rr.is(roomOK, 1) || len(rr.bm.written()) != 0 {
			t.Fatalf("an edge for a room Link was not in was applied:\n%s", rr.log)
		}
	})
	t.Run("dropped during a stage change", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		if err := rr.g.SetNextStage(drcKeys); err != nil {
			t.Fatal(err)
		}
		rr.ticks(1)
		rr.send(edgeMsg(t, drc, 1, roomOK))
		rr.ticks(2)
		if err := rr.g.ChangeStage(); err != nil {
			t.Fatal(err)
		}
		rr.settleM5()
		if rr.is(roomOK, 1) || len(rr.bm.written()) != 0 {
			t.Fatalf("an edge that came mid stage change was applied:\n%s", rr.log)
		}
	})
}

// TestReconcileM5ZoneMovesBeforeTheWrite: the zone of Link's room is
// freed and created again in another slot after the plan read it, while
// the write waits for the heartbeat; another room's zone takes the old
// slot. The write is aborted, nothing lands in either slot, and the next
// tick writes the room's new zone.
func TestReconcileM5ZoneMovesBeforeTheWrite(t *testing.T) {
	for _, c := range []struct {
		name  string
		storm func(rr *m5Rig) error
		abort bool // the write is aborted; else it lands in the room's zone as it is now
	}{
		{"the zone moves", func(rr *m5Rig) error {
			_, _, err := rr.g.MoveZone(1, 5)
			return err
		}, true},
		{"Link changes room", func(rr *m5Rig) error { return rr.g.EnterRoom(2, 1) }, true},
		// The same stage and room again: room 1's zone is wiped and made in
		// the same slot, which is still the zone of the room Link is in.
		{"the stage reloads", func(rr *m5Rig) error { return rr.g.LoadStage(drcKeys) }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
			old := rr.slot(1)
			oldAddr, _ := addr.ZoneAddr(old)
			before := rr.read(oldAddr, addr.ZoneSize)
			var once sync.Once
			var stormErr error
			rr.bm.onHeartbeat(func(n int) {
				if n >= 2 {
					once.Do(func() { stormErr = c.storm(rr) })
				}
			})
			rr.send(zoneMsg(t, drc, 1, zoneOK), danMsg(t, drc, danOK))
			rep := rr.tick()
			rr.bm.onHeartbeat(nil)
			if stormErr != nil {
				t.Fatal(stormErr)
			}
			if c.abort && (!errors.Is(rep.WriteErr, ErrAborted) || rep.Wrote != 0) {
				t.Fatalf("the tick of the storm: wrote %d, error %v; want ErrAborted\n%s", rep.Wrote, rep.WriteErr, rr.log)
			}
			if after := rr.read(oldAddr, addr.ZoneSize); !bytes.Equal(after[addr.ZoneOffSwitch:addr.ZoneOffItem], before[addr.ZoneOffSwitch:addr.ZoneOffItem]) && rr.slot(1) != old {
				t.Fatalf("the old slot %d's switches went % X -> % X", old, before, after)
			}
			if c.name == "Link changes room" {
				rr.enter(1, 2)
			}
			rr.settleM5()
			if !rr.is(zoneOK, 1) || !rr.is(danOK, 1) {
				t.Fatalf("the stores were not written after the storm:\n%s", rr.log)
			}
			if st := rr.r.Status(); st.Writes.Failed != 0 || st.WritesOff != "" {
				t.Fatalf("verify failures %d, writes off %q", st.Writes.Failed, st.WritesOff)
			}
			rr.audit()
		})
	}
}

// TestReconcileM5TransitionStorm: dan, zone and room stores keep coming
// while the receiver's game runs initDan (a table change), changes rooms
// (clearRoomSwitch, zones removed and created first-free) and moves a
// zone; nothing is cleared, written into a wrong slot, or set twice, and
// no write is judged a failure.
func TestReconcileM5TransitionStorm(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
	all := func() {
		rr.send(danMsg(t, drc, danOK, danOK2, danTimed),
			zoneMsg(t, drc, 1, zoneOK, zoneOK2, zoneTimed),
			zoneMsg(t, drc, 2, zoneOK),
			edgeMsg(t, drc, 1, roomOK, roomTimed))
	}
	steps := []func(){
		func() { rr.enter(2, 1) },
		func() { rr.enter(1, 2) },
		func() { rr.g.MoveZone(1, 7) },
		func() { rr.enter(3) },
		func() { rr.enter(1) },
		func() {
			if err := rr.g.LoadStage(woods); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if err := rr.g.LoadStage(drcKeys); err != nil {
				t.Fatal(err)
			}
		},
		func() { rr.g.SetEvent(1, 0) },
		func() { rr.g.SetEvent(0, 0) },
		func() { rr.splitLoad(drcBoss) },
		func() { rr.splitLoad(drcKeys) },
	}
	for i, step := range steps {
		all()
		rr.ticks(i % 3)
		step()
		all()
		rr.ticks(1 + i%4)
	}
	rr.settleM5()
	all()
	rr.settleM5()
	// What the storm's resets cleared stays cleared (ever-had), so only
	// that something was written is asked.
	if ws, _ := rr.scopedWrites(); len(ws) == 0 {
		t.Fatalf("nothing written in the storm:\n%s", rr.log)
	}
	if st := rr.r.Status(); st.Writes.Failed != 0 || st.WritesOff != "" {
		t.Fatalf("verify failures %d, writes off %q:\n%s", st.Writes.Failed, st.WritesOff, rr.log)
	}
	rr.audit()
}

// TestReconcileM5VerifyAfterResets: a scoped byte the game resets on the
// next tick, legitimately, is not judged: a room change clears the room
// switches and moves mStayNo, initDan clears the dan bits.
func TestReconcileM5VerifyAfterResets(t *testing.T) {
	for _, c := range []struct {
		name  string
		msg   func(t *testing.T) *wire.Scoped
		reset func(rr *m5Rig)
	}{
		{"a room edge, then a room change", func(t *testing.T) *wire.Scoped { return edgeMsg(t, drc, 1, roomOK) },
			func(rr *m5Rig) { rr.enter(2, 1) }},
		{"a zone switch, then the zone moves", func(t *testing.T) *wire.Scoped { return zoneMsg(t, drc, 1, zoneOK) },
			func(rr *m5Rig) { rr.g.MoveZone(1, 6) }},
		{"a dan switch, then initDan", func(t *testing.T) *wire.Scoped { return danMsg(t, drc, danOK) },
			func(rr *m5Rig) { rr.g.LoadStage(woods) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
			rr.send(c.msg(t))
			if at := rr.untilWrite(gate.StableTicks + 4); at < 0 {
				t.Fatalf("nothing written:\n%s", rr.log)
			}
			c.reset(rr)
			rep := rr.tick()
			if rep.Verify.Failed != 0 || rep.Verify.Unverified == 0 {
				t.Fatalf("verify after the reset: %+v", rep.Verify)
			}
			if st := rr.r.Status(); st.WritesOff != "" {
				t.Fatalf("writes off: %s", st.WritesOff)
			}
		})
	}
	t.Run("a write that holds is verified", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.send(zoneMsg(t, drc, 1, zoneOK), danMsg(t, drc, danOK), edgeMsg(t, drc, 1, roomOK))
		if at := rr.untilWrite(gate.StableTicks + 4); at < 0 {
			t.Fatalf("nothing written:\n%s", rr.log)
		}
		if rep := rr.tick(); rep.Verify.Verified != 3 || rep.Verify.Failed != 0 {
			t.Fatalf("verify: %+v", rep.Verify)
		}
	})
	t.Run("a write the game stores over fails", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.send(zoneMsg(t, drc, 1, zoneOK))
		if at := rr.untilWrite(gate.StableTicks + 4); at < 0 {
			t.Fatalf("nothing written:\n%s", rr.log)
		}
		if err := rr.g.OffSwitch(zoneOK, 1); err != nil {
			t.Fatal(err)
		}
		if rep := rr.tick(); rep.Verify.Failed != 1 {
			t.Fatalf("verify: %+v", rep.Verify)
		}
	})
}

// TestReconcileM5DryRun: a dry run with tables logs what it would write,
// once, the latching switches only, and writes nothing.
func TestReconcileM5DryRun(t *testing.T) {
	rr := newM5Rig(t, Config{Want: hub.FeatureImage | hub.FeaturePuzzle, Tables: m5Tables(t)}, m5Features)
	for range 3 {
		rr.send(danMsg(t, drc, danOK, danTimed), zoneMsg(t, drc, 1, zoneOK, zoneTimed), edgeMsg(t, drc, 1, roomOK, roomTimed))
		rr.settleM5()
	}
	for _, c := range []struct {
		line string
		n    int
	}{
		{"dry-run: would set dan switch 0x85 (live when idle", 1},
		{"dry-run: would set zone switch 0xC3 in room 1 (live when idle", 1},
		{"dry-run: would set zone switch 0xE1 in room 1 (a room edge", 3},
		{"would set dan switch 0x86", 0},
		{"would set zone switch 0xC4", 0},
		{"would set zone switch 0xE2", 0},
		{"zone switch 0xE2 in room 1 does not latch", 1},
		{"room puzzles:", 0},
	} {
		if got := rr.log.count(c.line); got != c.n {
			t.Errorf("%q said %d times, want %d", c.line, got, c.n)
		}
	}
	if n := len(rr.m.Writes()); n != 0 {
		t.Errorf("a dry run wrote %d times", n)
	}
	if t.Failed() {
		t.Fatalf("log:\n%s", rr.log)
	}
}

// TestReconcileM5NotGranted: without FeaturePuzzle nothing is written,
// and the log says why once; a grant taken away mid-session stops the
// writes of the stores already kept.
func TestReconcileM5NotGranted(t *testing.T) {
	const line = "a partner's dan, zone and room switches are not written to this game"
	t.Run("never granted", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, hub.FeatureImage)
		for range 3 {
			rr.send(danMsg(t, drc, danOK), zoneMsg(t, drc, 1, zoneOK), edgeMsg(t, drc, 1, roomOK))
			rr.settleM5()
		}
		if len(rr.bm.written()) != 0 || rr.log.count(line) != 1 || !rr.log.has("the session does not grant puzzle") {
			t.Fatalf("%d writes:\n%s", len(rr.bm.written()), rr.log)
		}
	})
	t.Run("not asked for", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t), Want: hub.FeatureImage}, hub.FeatureImage)
		rr.settleM5()
		if rr.log.count(line) != 1 || !rr.log.has("this replica does not ask for puzzle") {
			t.Fatalf("log:\n%s", rr.log)
		}
	})
	t.Run("taken away", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
		rr.g.SetEvent(1, 0)
		rr.ticks(2)
		rr.send(danMsg(t, drc, danOK), zoneMsg(t, drc, 1, zoneOK)) // kept while the event runs
		rr.ticks(2)
		say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(hub.FeatureImage)})
		rr.g.SetEvent(0, 0)
		for range 3 {
			rr.settleM5()
			say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(hub.FeatureImage | hub.FeatureCombat)})
		}
		if len(rr.bm.written()) != 0 || rr.is(danOK, 1) || rr.is(zoneOK, 1) || rr.log.count(line) != 1 {
			t.Fatalf("%d writes:\n%s", len(rr.bm.written()), rr.log)
		}
		say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(m5Features)})
		rr.settleM5()
		if !rr.is(danOK, 1) || !rr.is(zoneOK, 1) {
			t.Fatalf("the stores kept were not written once granted again:\n%s", rr.log)
		}
		rr.audit()
	})
	t.Run("no tables", func(t *testing.T) {
		rr := newM5Rig(t, Config{Apply: true}, m5Features)
		rr.send(danMsg(t, drc, danOK), zoneMsg(t, drc, 1, zoneOK), edgeMsg(t, drc, 1, roomOK))
		rr.settleM5()
		if len(rr.bm.written()) != 0 || !rr.log.has("this side has no M1 switch table") {
			t.Fatalf("%d writes:\n%s", len(rr.bm.written()), rr.log)
		}
	})
}

// TestReconcileM5ActorsSeeIt: a door polling a latching zone switch opens
// on the receiver once the partner's switch is written. A store that also
// carries the switch of SWC00 of type 0, which clears it every frame Link
// is away (a hostile hub's: the host's hub would not keep it), gets that
// switch nowhere: it does not latch.
func TestReconcileM5ActorsSeeIt(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: m5Tables(t)}, m5Features)
	door := &fakegame.Poller{Stage: drc.Name, Room: 1, No: zoneOK}
	plate := &fakegame.Swc00{Stage: drc.Name, Room: 1, No: zoneTimed, Enable: 0xFF}
	tick := func(n int) {
		for range n {
			if err := rr.g.FrameWith(door, plate); err != nil {
				t.Fatal(err)
			}
			rr.tick()
		}
	}
	tick(3)
	if ok, _ := door.Opened(); ok {
		t.Fatal("the door opened by itself")
	}
	rr.send(zoneMsg(t, drc, 1, zoneOK, zoneTimed))
	tick(gate.StableTicks + 4)
	if ok, _ := door.Opened(); !ok {
		t.Fatalf("the door did not open on the partner's switch:\n%s", rr.log)
	}
	if rr.is(zoneTimed, 1) {
		t.Fatal("the plate's switch is set with Link away")
	}
	rr.audit() // which holds that 0xC4, which does not latch, was never written
}

// FuzzReconcileM5 drives a writing replica with random stores and edges
// from the hub, random game transitions (rooms hidden and shown by doors
// among them) and the partner moving, and holds the M5 audit: no clear,
// no write into a slot that is not the zone of Link's room, no bit that
// does not latch, none set twice in its scope. The hub's WORLD gains
// memory switches too (an op with the high bit set), which M3's
// reconciler writes for the loaded table only at a room change and once
// the room hold allows: the audit checks that every room's actors had run
// a shutter's 30 frames as each was written.
//
// The tables are the Tingle Tuner guard's (gbaTables): danOK2, zoneOK2,
// roomOK2 and the sea's 0x05 sync only while no GBA is linked, and an op
// in 0x40-0x7F links or unlinks one. The audit holds that none is written
// while a GBA may be linked, and none is proposed (GAINS, SCOPED) then.
func FuzzReconcileM5(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Add([]byte{1, 0x13, 4, 1, 8, 2, 0x13, 3, 0x22, 8, 8})
	f.Add([]byte{0, 5, 6, 2, 7, 9, 1, 1, 5, 3, 0, 8, 8, 8})
	f.Add([]byte("2181"))
	f.Add([]byte{0x0B, 0x01, 1, 0x13, 0x0B, 0x00, 0, 5, 8, 0x30, 0x0A, 0x20, 1, 0x13, 8, 5})
	// A door onto room 2, Link through it, and the partner's memory
	// switch right after; then the door back, and another.
	f.Add([]byte{0x0B, 0x21, 3, 0x20, 0x80, 0x00, 8, 5, 0x0B, 0x11, 3, 0x10, 0x80, 0x01, 8, 5})
	// A GBA linked while the partner's stores and the sea's switch come
	// and Link sets one, a room change, then unlinked.
	f.Add([]byte{0x40, 1, 0, 1, 1, 1, 2, 1, 0x80, 0x10, 6, 2, 8, 5, 3, 0x20, 8, 3, 0x41, 0, 8, 5, 3, 0x10, 8, 5})
	// Linked across a split load and a zone move, unlinked mid-store.
	f.Add([]byte{0, 1, 0x40, 1, 7, 1, 8, 2, 7, 0, 4, 0x10, 1, 0x11, 0x40, 0, 8, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 48 {
			data = data[:48]
		}
		mems := []int{0x05, 0x06, 0x07} // latching in DRC's and the sea's tables (switchPolicy)
		tb := gbaTables(t)
		rr := newM5Rig(t, Config{Apply: true, Tables: tb, Policy: switchPolicy(t, mems...)}, m5Features|hub.FeatureSwitches)
		rr.tb = tb
		gbaOff, gbaBit := switchImageByte(addr.TableSea, gbaSea)
		linked := false
		// proposed checks what the replica sent since the last call: with
		// a GBA linked, nothing shared with the Tingle Tuner.
		proposed := func() {
			for _, m := range rr.hub.take() {
				if !linked {
					continue
				}
				switch m := m.(type) {
				case *wire.GainsUp:
					for _, g := range m.Gains {
						if int(g.Off) == gbaOff && g.Val&gbaBit != 0 {
							t.Fatalf("GAINS of sea 0x%02X with a GBA linked", gbaSea)
						}
					}
				case *wire.Scoped:
					k := world.Key{Kind: world.ScopeKind(m.Kind), Stage: m.Stage.String(), Table: int(m.Table), Room: int(m.Room)}
					for _, w := range m.Words {
						if held := hub.ScopeFilter(tb.GBAScopedSwitch)(k, int(w.Word)); m.Kind != wire.ScopeKill && w.Bits&held != 0 {
							t.Fatalf("SCOPED %+v with a GBA linked carries 0x%08X, shared with the Tingle Tuner", m, w.Bits&held)
						}
					}
				}
			}
		}
		var img world.Image
		dans := []int{danOK, danOK2, danTimed, 0xBF}
		zones := []int{zoneOK, zoneOK2, zoneTimed, 0xDF}
		rooms := []int{roomOK, roomOK2, roomTimed, 0xEF}
		nos := append(append(append([]int(nil), dans...), zones...), rooms...)
		stages := []fakegame.Stage{drcKeys, drcBoss, woods}
		next := func(i *int) byte {
			if *i >= len(data) {
				return 0
			}
			b := data[*i]
			*i++
			return b
		}
		for i := 0; i < len(data); {
			op, arg := next(&i), next(&i)
			room := int(arg>>4) % 4
			proposed()
			if op&0xC0 == 0x40 {
				// A Game Boy Advance linked (the Tingle Tuner) or unlinked.
				linked = arg&1 == 1
				rr.g.SetGBALink(linked)
				rr.ticks(1)
				continue
			}
			if op&0x80 != 0 {
				// The partner's memory switch, of DRC's table or the sea's.
				tb := []int{addr.TableDRC, addr.TableSea}[int(arg>>4)%2]
				off, m := switchImageByte(tb, mems[int(arg)%len(mems)])
				img[off] |= m
				rr.world(img)
				rr.ticks(1)
				continue
			}
			switch op % 12 {
			case 0:
				rr.send(danMsg(t, drc, dans[int(arg)%len(dans)]))
			case 1:
				rr.send(zoneMsg(t, drc, room, zones[int(arg)%len(zones)]))
			case 2:
				rr.send(edgeMsg(t, drc, room, rooms[int(arg)%len(rooms)]))
			case 3:
				if rr.g.StayNo() >= 0 {
					rr.g.EnterRoom(room, (room+1)%4)
				}
			case 4:
				// The zone of Link's room wiped and moved between two ticks.
				// One wiped into the same slot needs Link out of the room
				// for two room changes on the console, so it is moved again.
				if s := rr.g.StayNo(); s >= 0 {
					if from, to, err := rr.g.MoveZone(s, 8+room); err == nil && from == to {
						rr.g.MoveZone(s, 12+room)
					}
				}
			case 5:
				rr.g.SetEvent(arg&1, 0)
			case 6:
				// The game sets any switch; it clears only one whose writer
				// does not latch (a latching one is cleared only by the
				// resets above, which verify allows for).
				if s := rr.g.StayNo(); s >= 0 {
					if arg&1 == 0 {
						// arg>>1: an even arg reaches every switch.
						rr.g.OnSwitch(nos[int(arg>>1)%len(nos)], s)
					} else {
						rr.g.OffSwitch([]int{danTimed, zoneTimed, roomTimed}[int(arg>>1)%3], s)
					}
				}
			case 7:
				rr.splitLoad(stages[int(arg)%len(stages)])
			case 8:
				rr.ticks(1 + int(arg)%6)
			case 9:
				var once sync.Once
				rr.bm.onHeartbeat(func(n int) {
					if n >= 2 {
						once.Do(func() {
							if s := rr.g.StayNo(); s >= 0 {
								rr.g.MoveZone(s, 12+room)
							}
						})
					}
				})
				rr.ticks(1)
				rr.bm.onHeartbeat(nil)
			case 10:
				if arg&1 == 0 {
					rr.partnerAt(drc, room)
				} else {
					rr.partnerAt(stages[int(arg>>1)%len(stages)], room)
				}
			case 11:
				if arg&1 == 0 {
					rr.g.HideRoom(room)
				} else {
					rr.g.ShowRoom(room)
				}
			}
			rr.ticks(1)
		}
		rr.g.SetEvent(0, 0)
		rr.settleM5()
		proposed()
		rr.audit()
		// No write set the sea's switch shared with the Tingle Tuner while
		// a GBA may have been linked.
		ws, zs := rr.bm.written(), rr.zm.zones()
		for i, w := range ws {
			if !zs[i].gba {
				continue
			}
			for j := range w.n {
				if a := w.a + uint32(j); a == addr.SaveImage+uint32(gbaOff) && w.data[j]&^w.prev[j]&gbaBit != 0 {
					t.Fatalf("tick %d: sea 0x%02X written at 0x%08X while a GBA may be linked", w.tick, gbaSea, a)
				}
			}
		}
		if st := rr.r.Status(); st.Writes.Failed != 0 {
			t.Fatalf("%d verify failures:\n%s", st.Writes.Failed, rr.log)
		}
	})
}
