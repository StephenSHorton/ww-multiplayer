package hub

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

const drc = "M_NewD2" // Dragon Roost Cavern, save table 3; its boss stage is M_DragB

// sw is one SCOPED word.
func sw(word uint8, bits uint32) wire.ScopedWord { return wire.ScopedWord{Word: word, Bits: bits} }

// gateVerdict is an M1 verdict for the gate tests: dan switch 0x85 of
// DRC's save table, zone switch 0xC3 and room switch 0xE1 of its room 1.
// It fails t when it is asked about a switch SCOPED cannot carry, or about
// a dan switch with a room.
func gateVerdict(t *testing.T) func(table int, stage string, room, no int) bool {
	return func(table int, stage string, room, no int) bool {
		if no < 0x80 || no > 0xEF {
			t.Errorf("the verdict was asked about switch 0x%X", no)
		}
		if no < 0xC0 && room != -1 {
			t.Errorf("the verdict was asked about dan switch 0x%X with room %d", no, room)
		}
		switch no {
		case 0x85:
			return table == 3
		case 0xC3, 0xE1:
			return table == 3 && stage == drc && room == 1
		}
		return false
	}
}

// stored returns store k's words.
func (r *rig) stored(k world.Key) []world.Word {
	r.t.Helper()
	var ws []world.Word
	if err := r.h.do(func() { ws = r.h.scopes.Get(k) }); err != nil {
		r.t.Fatal(err)
	}
	return ws
}

func keyOfScoped(m *wire.Scoped) world.Key {
	return world.Key{Kind: world.ScopeKind(m.Kind), Stage: m.Stage.String(), Table: int(m.Table), Room: int(m.Room)}
}

// TestScopedGate: the hub keeps and relays only the dan, zone and room
// switches the host's M1 verdict grants for the scope, per switch; every
// other bit of a SCOPED is dropped, counted and not complained of. Kill
// bits are not switches.
func TestScopedGate(t *testing.T) {
	for _, c := range []struct {
		name        string
		stage       string
		table, room int8
		msg         *wire.Scoped
		relay       []wire.ScopedWord // what bob gets; nil: nothing
		refused     int
	}{
		{"a latching dan switch", drc, 3, 1, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1<<5)), []wire.ScopedWord{sw(0, 1<<5)}, 0},
		{"the dan switch from another room", drc, 3, 4, scoped(wire.ScopeDan, drc, 3, 4, sw(0, 1<<5)), []wire.ScopedWord{sw(0, 1<<5)}, 0},
		{"the dan switch from the table's boss stage", "M_DragB", 3, 0, scoped(wire.ScopeDan, "M_DragB", 3, -1, sw(0, 1<<5)), []wire.ScopedWord{sw(0, 1<<5)}, 0},
		{"the dan switch's number in another table", "kindan", 4, 1, scoped(wire.ScopeDan, "kindan", 4, -1, sw(0, 1<<5)), nil, 1},
		{"a dan switch that doesn't latch", drc, 3, 1, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1<<6)), nil, 1},
		{"a latching dan switch among others", drc, 3, 1, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1<<5|1<<6), sw(1, 0xFFFFFFFF)), []wire.ScopedWord{sw(0, 1<<5)}, 33},
		{"a latching zone switch", drc, 3, 1, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 1<<3)), []wire.ScopedWord{sw(0, 1<<3)}, 0},
		{"the zone switch in another room", drc, 3, 2, scoped(wire.ScopeZone, drc, 3, 2, sw(0, 1<<3)), nil, 1},
		{"the zone switch in the table's boss stage", "M_DragB", 3, 1, scoped(wire.ScopeZone, "M_DragB", 3, 1, sw(0, 1<<3)), nil, 1},
		{"a zone switch that doesn't latch", drc, 3, 1, scoped(wire.ScopeZone, drc, 3, 1, sw(1, 1<<3)), nil, 1},
		{"every zone switch", drc, 3, 1, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 0xFFFF), sw(1, 0xFFFF)), []wire.ScopedWord{sw(0, 1<<3)}, 31},
		{"a latching room switch", drc, 3, 1, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 1<<1)), []wire.ScopedWord{sw(2, 1<<1)}, 0},
		{"a room switch that doesn't latch", drc, 3, 1, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 1<<2)), nil, 1},
		{"the room switch in another room", drc, 3, 2, scoped(wire.ScopeRoomEdge, drc, 3, 2, sw(2, 1<<1)), nil, 1},
		{"every room switch", drc, 3, 1, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 0xFFFF)), []wire.ScopedWord{sw(2, 1<<1)}, 15},
		{"kills are not switches", drc, 3, 1, scoped(wire.ScopeKill, drc, 3, 1, sw(0, 1), sw(15, 1<<31)), []wire.ScopedWord{sw(0, 1), sw(15, 1<<31)}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := fullCfg()
			cfg.SyncedScoped = gateVerdict(t)
			r := newRig(t, cfg)
			a := r.join(newConn(1, "alice"))
			b := r.join(newConn(2, "bob"))
			r.send(a, presence(c.stage, c.table, c.room))
			r.send(b, presence(c.stage, c.table, c.room))
			a.take(t)
			b.take(t)

			r.send(a, c.msg)
			if ms := a.take(t); len(ms) != 0 {
				t.Errorf("alice got %s: a refused switch is only counted", subs(ms))
			}
			got := pick[*wire.Scoped](b.take(t))
			switch {
			case c.relay == nil && len(got) != 0:
				t.Errorf("bob got %+v, want nothing", got)
			case c.relay != nil && (len(got) != 1 || !slices.Equal(got[0].Words, c.relay)):
				t.Errorf("bob got %+v, want words %+v", got, c.relay)
			}
			if p := r.peer(1); p.ScopedRefused != c.refused || p.Rejected != 0 {
				t.Errorf("alice's scoped refused %d (rejected %d), want %d (0)", p.ScopedRefused, p.Rejected, c.refused)
			}
			k := keyOfScoped(c.msg)
			if k.Kind == world.ScopeDan {
				k.Room = -1
			}
			var want []world.Key
			if c.relay != nil && k.Kind != world.ScopeRoomEdge {
				want = []world.Key{k}
				ws := r.stored(k)
				if len(ws) != len(c.relay) {
					t.Errorf("store %s holds %+v, want %+v", k, ws, c.relay)
				}
				for i := range min(len(ws), len(c.relay)) {
					if ws[i] != world.Word(c.relay[i]) {
						t.Errorf("store %s holds %+v, want %+v", k, ws, c.relay)
					}
				}
			}
			if keys := r.status().Scopes; !slices.Equal(keys, want) {
				t.Errorf("stores %v, want %v", keys, want)
			}
		})
	}
}

// TestScopedGateFailsClosed: a hub without the M1 verdict keeps no dan,
// zone or room switch at all; kill bits pass.
func TestScopedGateFailsClosed(t *testing.T) {
	cfg := fullCfg()
	cfg.SyncedScoped = nil
	r := newRig(t, cfg)
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 1))
	a.take(t)
	b.take(t)
	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1), sw(1, 1<<31)))
	r.send(a, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 0xFFFF)))
	r.send(a, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 1)))
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, sw(4, 0x10)))
	got := pick[*wire.Scoped](b.take(t))
	if len(got) != 1 || got[0].Kind != wire.ScopeKill {
		t.Fatalf("bob got %+v, want only the kill", got)
	}
	if p := r.peer(1); p.ScopedRefused != 2+16+1 {
		t.Errorf("alice's scoped refused %d, want 19", p.ScopedRefused)
	}
	want := []world.Key{{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}}
	if keys := r.status().Scopes; !slices.Equal(keys, want) {
		t.Errorf("stores %v, want %v", keys, want)
	}
}

// TestScopedRelayMatrix: who gets a SCOPED of each kind (§4.4, §6.3):
// DAN the bound peers in the stage and table, ZONE and ROOMEDGE those in
// the room at that moment, KILL those in the stage; never an unbound peer,
// one without the kind's feature, one nowhere, or one in another stage or
// table. A room switch is never stored, so a player coming back to the
// room gets the zone store but not it.
func TestScopedRelayMatrix(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	type who struct {
		c    *fakeConn
		want []wire.ScopeKind
	}
	all := []wire.ScopeKind{wire.ScopeDan, wire.ScopeZone, wire.ScopeRoomEdge, wire.ScopeKill}
	bound := func(id byte, name string, f Feature) *fakeConn {
		c := newConn(id, name)
		r.attach(c)
		m := hello(wire.FileBound, testWorld, id)
		m.Want = uint32(f)
		r.send(c, m)
		return c
	}
	frank := newConn(6, "frank")
	r.attach(frank)
	r.send(frank, hello(wire.FileUnbound, world.ID{}, 0))
	peers := []who{
		{r.join(newConn(2, "bob")), all},
		{r.join(newConn(3, "carol")), []wire.ScopeKind{wire.ScopeDan, wire.ScopeKill}},
		{r.join(newConn(4, "dave")), nil},
		{r.join(newConn(5, "erin")), nil},
		{frank, nil},
		{bound(7, "gina", DefaultFeatures&^FeaturePuzzle), []wire.ScopeKind{wire.ScopeKill}},
		{r.join(newConn(8, "hank")), nil},
		{bound(9, "ivan", DefaultFeatures&^FeatureCombat), []wire.ScopeKind{wire.ScopeDan, wire.ScopeZone, wire.ScopeRoomEdge}},
	}
	r.send(a, presence(drc, 3, 1))
	for _, at := range []struct {
		c *fakeConn
		m *wire.Presence
	}{
		{peers[0].c, presence(drc, 3, 1)},                            // bob: the room
		{peers[1].c, presence(drc, 3, 2)},                            // carol: another room of the stage
		{peers[2].c, presence(drc, 4, 1)},                            // dave: the stage's name in another table
		{peers[3].c, presence("M_DragB", 3, 1)},                      // erin: another stage of the table
		{frank, presence(drc, 3, 1)},                                 // frank: unbound
		{peers[5].c, presence(drc, 3, 1)},                            // gina: no puzzle sync
		{peers[6].c, &wire.Presence{Table: -1, Room: -1, Layer: -1}}, // hank: nowhere
		{peers[7].c, presence(drc, 3, 1)},                            // ivan: no combat sync
	} {
		r.send(at.c, at.m)
	}
	a.take(t)
	for _, p := range peers {
		p.c.take(t)
	}

	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 1)))
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, sw(0, 1)))
	for _, p := range peers {
		var kinds []wire.ScopeKind
		for _, m := range pick[*wire.Scoped](p.c.take(t)) {
			kinds = append(kinds, m.Kind)
		}
		if !slices.Equal(kinds, p.want) {
			t.Errorf("%s got %v, want %v", p.c.name, kinds, p.want)
		}
	}

	// Bob steps into room 2: the next room switch of room 1 is not his.
	// Back in room 1 he gets the zone store, once, and no room switch.
	b := peers[0].c
	r.send(b, presence(drc, 3, 2))
	b.take(t)
	r.send(a, scoped(wire.ScopeRoomEdge, drc, 3, 1, sw(2, 2)))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
		t.Fatalf("bob, in room 2, got %+v", got)
	}
	if got := pick[*wire.Scoped](peers[7].c.take(t)); len(got) != 1 || got[0].Kind != wire.ScopeRoomEdge {
		t.Fatalf("ivan, in room 1, got %+v, want the room switch", got)
	}
	r.send(b, presence(drc, 3, 1))
	got := pick[*wire.Scoped](b.take(t))
	if len(got) != 1 || got[0].Kind != wire.ScopeZone || !slices.Equal(got[0].Words, []wire.ScopedWord{sw(0, 1)}) {
		t.Fatalf("bob back in room 1 got %+v, want the zone store alone", got)
	}
	r.send(b, presence(drc, 3, 1))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
		t.Fatalf("bob's repeated PRESENCE brought %+v", got)
	}
}

// TestScopedStoreReentry: a store goes when the last player in its scope
// leaves; a player entering a scope gets its store once; and a store that
// went comes back only from a player in the scope proposing it again.
func TestScopedStoreReentry(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	dan := world.Key{Kind: world.ScopeDan, Stage: drc, Table: 3, Room: -1}
	zone := world.Key{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 1}
	kinds := func(c *fakeConn) []wire.ScopeKind {
		var ks []wire.ScopeKind
		for _, m := range pick[*wire.Scoped](c.take(t)) {
			ks = append(ks, m.Kind)
		}
		return ks
	}
	keys := func(want ...world.Key) {
		t.Helper()
		if got := r.status().Scopes; !slices.Equal(got, want) {
			t.Fatalf("stores %v, want %v", got, want)
		}
	}
	r.send(a, presence(drc, 3, 1))
	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 1)))
	keys(dan, zone)

	// Alice moves to room 2: room 1 is empty, its zone store goes.
	r.send(a, presence(drc, 3, 2))
	keys(dan)
	if !r.log.has("dropped the ZONE") {
		t.Error("no log line for the dropped zone store")
	}

	// Bob enters the stage in room 1: the dan store, once; no zone.
	b.take(t)
	r.send(b, presence(drc, 3, 1))
	if got := kinds(b); !slices.Equal(got, []wire.ScopeKind{wire.ScopeDan}) {
		t.Fatalf("bob entering got %v, want DAN", got)
	}
	for _, at := range []*wire.Presence{presence(drc, 3, 1), presence(drc, 3, 2), presence(drc, 3, 1)} {
		r.send(b, at)
		if got := kinds(b); len(got) != 0 {
			t.Fatalf("bob moving in the stage got %v again", got)
		}
	}

	// Everyone leaves: the dan store goes. Coming back brings nothing
	// until a player there proposes the words again, as alice's sender
	// does on entering the stage (replica.Differ.ShareScopes).
	r.send(a, presence("sea", 0, 11))
	keys(dan)
	r.send(b, presence("sea", 0, 11))
	keys()
	r.send(b, presence(drc, 3, 1))
	if got := kinds(b); len(got) != 0 {
		t.Fatalf("bob back in an empty stage got %v", got)
	}
	r.send(a, presence(drc, 3, 1))
	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 1)))
	if got := kinds(b); !slices.Equal(got, []wire.ScopeKind{wire.ScopeDan, wire.ScopeZone}) {
		t.Fatalf("bob got %v once alice proposed again, want DAN and ZONE", got)
	}
	keys(dan, zone)
}

// TestScopedRegrant: a peer that gets puzzle sync back mid-session is
// sent the puzzle stores its place reaches, once, and nothing it already
// had through another feature.
func TestScopedRegrant(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := newConn(2, "bob")
	r.attach(b)
	m := hello(wire.FileBound, testWorld, 2)
	m.Want = uint32(DefaultFeatures &^ FeaturePuzzle)
	r.send(b, m)
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 1))
	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeZone, drc, 3, 1, sw(0, 1)))
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, sw(0, 1)))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 1 || got[0].Kind != wire.ScopeKill {
		t.Fatalf("bob without puzzle sync got %+v, want the kill", got)
	}
	scopedKinds := func(ms []wire.Msg) []wire.ScopeKind {
		var ks []wire.ScopeKind
		for _, m := range pick[*wire.Scoped](ms) {
			ks = append(ks, m.Kind)
		}
		return ks
	}

	// Bob's replica turns puzzle sync on: the dan and zone stores, no kill.
	ms := r.exchange(b, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(DefaultFeatures)})
	if got := scopedKinds(ms); !slices.Equal(got, []wire.ScopeKind{wire.ScopeDan, wire.ScopeZone}) {
		t.Fatalf("bob regranted puzzle sync got %v, want DAN and ZONE", got)
	}
	if got := scopedKinds(r.exchange(b, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(DefaultFeatures)})); len(got) != 0 {
		t.Fatalf("a regrant that changes nothing sent %v", got)
	}

	// Bob's replica turns it off and on again: he missed alice's next dan
	// switch, and gets the store with it.
	r.send(b, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(DefaultFeatures &^ FeaturePuzzle)})
	r.send(a, scoped(wire.ScopeDan, drc, 3, -1, sw(0, 2)))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
		t.Fatalf("bob without puzzle sync got %s", scopedString(got))
	}
	r.send(b, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(DefaultFeatures)})
	want := "[DAN M_NewD2 3 -1 [{Word:0 Bits:3}]][ZONE M_NewD2 3 1 [{Word:0 Bits:1}]]"
	if got := scopedString(pick[*wire.Scoped](b.take(t))); got != want {
		t.Fatalf("bob got %s, want %s", got, want)
	}

	// The host turns puzzle sync off and on: everyone gets the puzzle
	// stores where they are again.
	if err := r.h.SetFeatures(DefaultFeatures &^ FeaturePuzzle); err != nil {
		t.Fatal(err)
	}
	if err := r.h.SetFeatures(DefaultFeatures); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*fakeConn{a, b} {
		if got := scopedString(pick[*wire.Scoped](c.take(t))); got != want {
			t.Fatalf("%s got %s after the host's regrant, want %s", c.name, got, want)
		}
	}

	// An unbound peer regranted gets no store: it is in no scope.
	c := newConn(3, "carol")
	r.attach(c)
	um := hello(wire.FileUnbound, world.ID{}, 0)
	um.Want = uint32(DefaultFeatures &^ FeaturePuzzle)
	r.send(c, um)
	r.send(c, presence(drc, 3, 1))
	c.take(t)
	if got := scopedKinds(r.exchange(c, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(DefaultFeatures)})); len(got) != 0 {
		t.Fatalf("unbound carol regranted got %v", got)
	}
}

// TestScopeFilter: hub.ScopeFilter asks the verdict with the store's
// table, stage and room, and the switch number of each bit, only for a
// switch the store holds; nil grants nothing.
func TestScopeFilter(t *testing.T) {
	type ask struct {
		table int
		stage string
		room  int
		no    int
	}
	var asked []ask
	f := ScopeFilter(func(table int, stage string, room, no int) bool {
		asked = append(asked, ask{table, stage, room, no})
		return no == 0xA1 || no == 0xD0 || no == 0xEF
	})
	for _, c := range []struct {
		k     world.Key
		w     int
		want  uint32
		first ask // the first switch asked about; zero: none
	}{
		{world.Key{Kind: world.ScopeDan, Stage: drc, Table: 3, Room: -1}, 1, 1 << 1, ask{3, drc, -1, 0xA0}},
		{world.Key{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 7}, 1, 1 << 0, ask{3, drc, 7, 0xD0}},
		{world.Key{Kind: world.ScopeRoomEdge, Stage: drc, Table: 3, Room: 7}, 2, 1 << 15, ask{3, drc, 7, 0xE0}},
		{world.Key{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 7}, 2, 0, ask{}},     // the room switches are ROOMEDGE's
		{world.Key{Kind: world.ScopeRoomEdge, Stage: drc, Table: 3, Room: 7}, 3, 0, ask{}}, // 0xF0-0xFF
		{world.Key{Kind: world.ScopeDan, Stage: drc, Table: 3, Room: -1}, 2, 0, ask{}},     // 0xC0-0xDF are a zone's
		{world.Key{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 64}, 0, 0, ask{}},
		{world.Key{Kind: world.ScopeDan, Stage: drc, Table: 16, Room: -1}, 0, 0, ask{}},
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 7}, 9, 0xFFFFFFFF, ask{}},
	} {
		asked = nil
		if got := f(c.k, c.w); got != c.want {
			t.Errorf("filter(%s, %d) = 0x%X, want 0x%X", c.k, c.w, got, c.want)
		}
		switch {
		case c.first == (ask{}) && len(asked) != 0:
			t.Errorf("filter(%s, %d) asked the verdict %+v", c.k, c.w, asked)
		case c.first != (ask{}) && (len(asked) == 0 || asked[0] != c.first):
			t.Errorf("filter(%s, %d) first asked %+v, want %+v", c.k, c.w, asked, c.first)
		}
	}
	none := ScopeFilter(nil)
	for _, k := range []world.Key{
		{Kind: world.ScopeDan, Stage: drc, Table: 3, Room: -1},
		{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 1},
		{Kind: world.ScopeRoomEdge, Stage: drc, Table: 3, Room: 1},
	} {
		for w := range 3 {
			if got := none(k, w); got != 0 {
				t.Errorf("ScopeFilter(nil)(%s, %d) = 0x%X, want 0", k, w, got)
			}
		}
	}
}

func scopedString(ms []*wire.Scoped) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "[%s %s %d %d %+v]", m.Kind, m.Stage, m.Table, m.Room, m.Words)
	}
	return b.String()
}

// TestScopedHazard: a SCOPED naming a switch the game's setters halt on
// (0xF0-0xFE, the assert at d_save.cpp:1541-1542), a room outside 0..63
// (:1553) or a save table outside 0..15 (getSave's, :1507) is refused
// whole, before any store or relay; so is a word outside its kind's store.
func TestScopedHazard(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cfg := fullCfg()
	cfg.Now = func() time.Time { now = now.Add(2 * complainEvery); return now }
	r := newRig(t, cfg)
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 1))
	a.take(t)
	b.take(t)
	raw := func(kind wire.ScopeKind, table, room int8, word uint8, bits uint32) []byte {
		st, _ := wire.MakeStage(drc)
		p := append([]byte{byte(wire.SubScoped), byte(kind)}, st[:]...)
		p = append(p, byte(table), byte(room), 1, word)
		return binary.BigEndian.AppendUint32(p, bits)
	}
	for _, c := range []struct {
		name string
		p    []byte
	}{
		{"room switch word 3: 0xF0-0xFF", raw(wire.ScopeRoomEdge, 3, 1, 3, 0x0001)},
		{"zone word 2: the room switches", raw(wire.ScopeZone, 3, 1, 2, 0x0001)},
		{"dan word 2: the zone switches", raw(wire.ScopeDan, 3, -1, 2, 1)},
		{"a zone word's bits past 16", raw(wire.ScopeZone, 3, 1, 1, 1<<16)},
		{"room 64", raw(wire.ScopeZone, 3, 64, 0, 1)},
		{"room -1 for a zone", raw(wire.ScopeZone, 3, -1, 0, 1)},
		{"room -2 for a room switch", raw(wire.ScopeRoomEdge, 3, -2, 2, 1)},
		{"save table 16", raw(wire.ScopeDan, 16, -1, 0, 1)},
		{"save table -1", raw(wire.ScopeZone, -1, 1, 0, 1)},
	} {
		r.sendRaw(a, c.p)
		if got := codes(a.take(t)); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
			t.Errorf("%s: alice got %v, want REJECTED", c.name, got)
		}
		if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
			t.Errorf("%s: relayed %s", c.name, scopedString(got))
		}
	}
	if keys := r.status().Scopes; len(keys) != 0 {
		t.Errorf("stores %v, want none", keys)
	}
	if a.isClosed() {
		t.Error("alice was disconnected for a malformed SCOPED")
	}
}
