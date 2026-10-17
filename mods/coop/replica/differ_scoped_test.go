package replica

import (
	"math/rand/v2"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// shareVerdict is an M1 verdict for the sharing tests: DRC's dan switches
// 0x80 and 0xA1, its zone switches 0xC2 and 0xD0 and its room switch 0xE4
// latch, in every stage and room of the table.
func shareVerdict(table int, stage string, room, no int) bool {
	switch no {
	case 0x80, 0xA1, 0xC2, 0xD0, 0xE4:
		return table == addr.TableDRC
	}
	return false
}

func scopedOf(s fakegame.Stage, kind wire.ScopeKind, room int8, ws ...wire.ScopedWord) wire.Scoped {
	st, err := wire.MakeStage(s.Name)
	if err != nil {
		panic(err)
	}
	return wire.Scoped{Kind: kind, Stage: st, Table: int8(s.Table), Room: room, Words: ws}
}

func word(w uint8, bits uint32) wire.ScopedWord { return wire.ScopedWord{Word: w, Bits: bits} }

// TestDifferShareScopes: a sharing Differ proposes only what the verdict
// takes, the dan and zone words whole as Link enters their scope, and the
// room switches and kills as edges only. (The steps stay within one
// refresh period of each stage entry: TestDifferShareRefresh has those.)
func TestDifferShareScopes(t *testing.T) {
	dr := newDifferRig(t, nil)
	dr.load(drc)
	dr.d.ShareScopes(hub.ScopeFilter(shareVerdict))
	dr.onSwitch(0x80, 0)
	dr.onSwitch(0x81, 0) // refused
	dr.onSwitch(0xC2, 0)
	dr.onSwitch(0xE4, 0) // set before the first look: never an edge
	kill := func(id, room int) func() {
		return func() {
			if err := dr.g.OnActor(id, room); err != nil {
				t.Fatal(err)
			}
		}
	}
	steps := []struct {
		name string
		do   func()
		want []wire.Scoped
	}{
		{"entering DRC: the dan and zone words whole, as the verdict takes them", nil, []wire.Scoped{
			scopedOf(drc, wire.ScopeDan, -1, word(0, 1)),
			scopedOf(drc, wire.ScopeZone, 0, word(0, 1<<2)),
		}},
		{"refused dan, zone and room switches, and 0xE4 again: nothing", func() {
			dr.onSwitch(0x82, 0)
			dr.onSwitch(0xC3, 0)
			dr.onSwitch(0xE5, 0)
			dr.onSwitch(0xE4, 0)
		}, nil},
		{"dan switch 0xA1", func() { dr.onSwitch(0xA1, 0) }, []wire.Scoped{scopedOf(drc, wire.ScopeDan, -1, word(1, 1<<1))}},
		{"zone switch 0xD0", func() { dr.onSwitch(0xD0, 0) }, []wire.Scoped{scopedOf(drc, wire.ScopeZone, 0, word(1, 1))}},
		{"a kill: an edge", kill(37, 0), []wire.Scoped{scopedOf(drc, wire.ScopeKill, 0, word(1, 1<<5))}},
		{"into room 1, whose new zone is empty: nothing", func() { dr.enter(1, 0) }, nil},
		{"room switch 0xE4 in room 1: an edge", func() { dr.onSwitch(0xE4, 1) }, []wire.Scoped{scopedOf(drc, wire.ScopeRoomEdge, 1, word(2, 1<<4))}},
		{"zone switch 0xC2 in room 1", func() { dr.onSwitch(0xC2, 1) }, []wire.Scoped{scopedOf(drc, wire.ScopeZone, 1, word(0, 1<<2))}},
		{"back in room 0: its zone words whole, and no room switch", func() { dr.enter(0, 1) }, []wire.Scoped{
			scopedOf(drc, wire.ScopeZone, 0, word(0, 1<<2), word(1, 1)),
		}},
		{"the table's boss stage keeps the dan words: whole again", func() { dr.load(drcBoss) }, []wire.Scoped{
			scopedOf(drcBoss, wire.ScopeDan, -1, word(0, 1), word(1, 1<<1)),
		}},
		{"another table: initDan zeroes them, and the verdict takes none", func() {
			dr.load(woods)
			dr.onSwitch(0x80, 0)
		}, nil},
	}
	for _, s := range steps {
		if s.do != nil {
			s.do()
		}
		e := dr.tick()
		if !scopedEqual(e.Scoped, s.want) {
			t.Errorf("%s: scoped %v, want %v", s.name, e.Scoped, s.want)
		}
	}
}

// TestDifferShareRefresh: a sharing Differ proposes the dan and zone words
// whole every scopeRefresh ticks, never the room switches; without sharing
// nothing repeats.
func TestDifferShareRefresh(t *testing.T) {
	for _, share := range []bool{true, false} {
		dr := newDifferRig(t, nil)
		dr.load(drc)
		if share {
			dr.d.ShareScopes(hub.ScopeFilter(shareVerdict))
		}
		dr.tick()
		dr.onSwitch(0x80, 0)
		dr.onSwitch(0xC2, 0)
		dr.onSwitch(0xE4, 0)
		edges := []wire.Scoped{
			scopedOf(drc, wire.ScopeDan, -1, word(0, 1)),
			scopedOf(drc, wire.ScopeZone, 0, word(0, 1<<2)),
			scopedOf(drc, wire.ScopeRoomEdge, 0, word(2, 1<<4)),
		}
		if e := dr.tick(); !scopedEqual(e.Scoped, edges) {
			t.Fatalf("share %v: the edges %v, want %v", share, e.Scoped, edges)
		}
		for i := 2; i <= 3*scopeRefresh; i++ {
			e := dr.tick()
			var want []wire.Scoped
			if share && i%scopeRefresh == 0 {
				want = edges[:2]
			}
			if !scopedEqual(e.Scoped, want) {
				t.Fatalf("share %v, tick %d: scoped %v, want %v", share, i, e.Scoped, want)
			}
		}
		// Another stage is an entry, and the count starts again from it.
		dr.tick()
		dr.load(drcBoss)
		for i := range 2*scopeRefresh + 1 {
			e := dr.tick()
			var want []wire.Scoped
			if share && i%scopeRefresh == 0 {
				want = []wire.Scoped{scopedOf(drcBoss, wire.ScopeDan, -1, word(0, 1))}
			}
			if !scopedEqual(e.Scoped, want) {
				t.Fatalf("share %v, tick %d in the boss stage: scoped %v, want %v", share, i, e.Scoped, want)
			}
		}
	}
}

// sharedCheck tracks what a game sets between ticks and fails t when a
// sharing Differ's tick proposes a scoped bit it may not: one the filter
// refuses, one of a scope Link is not in, one the game does not hold now,
// or, for a room switch or a kill, one the game did not set since the
// last tick (those go as edges only). So no clear, reset, stale zone slot
// or replayed room switch ever travels.
type sharedCheck struct {
	keep  func(world.Key, int) uint32
	edges map[world.Key][maxScopedWords]uint32 // room switches and kills set since the last tick
}

const maxScopedWords = addr.ZoneActorCount / 32

func newSharedCheck(keep func(world.Key, int) uint32) *sharedCheck {
	return &sharedCheck{keep: keep, edges: map[world.Key][maxScopedWords]uint32{}}
}

// set records that the game set switch no of room, or kill id of room
// (no < 0), between ticks.
func (c *sharedCheck) set(s *state.Snapshot, no, room, id int) {
	k := world.Key{Kind: world.ScopeRoomEdge, Stage: s.StageName, Table: int(s.Table), Room: room}
	w, b := 2, no-0xE0
	if no < 0 {
		k.Kind, w, b = world.ScopeKill, id/32, id%32
	}
	e := c.edges[k]
	e[w] |= 1 << b
	c.edges[k] = e
}

func (c *sharedCheck) tick(t *testing.T, dr *differRig, e Edges) {
	t.Helper()
	defer clear(c.edges)
	s, err := state.Read(dr.m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range e.Scoped {
		k := world.Key{Kind: world.ScopeKind(m.Kind), Stage: m.Stage.String(), Table: int(m.Table), Room: int(m.Room)}
		if k.Stage != s.StageName || k.Table != int(s.Table) {
			t.Fatalf("%s proposed in %s (table %d)", k, s.StageName, s.Table)
		}
		var now []uint32
		switch k.Kind {
		case world.ScopeDan:
			if k.Room != -1 {
				t.Fatalf("%s: a dan proposal has a room", k)
			}
			d := danWords(s)
			now = d[:]
		default:
			z, ok := s.ZoneFor(k.Room)
			if !ok || k.Kind != world.ScopeKill && k.Room != int(s.StayNo) {
				t.Fatalf("%s proposed with Link in room %d", k, s.StayNo)
			}
			if k.Kind == world.ScopeKill {
				kw := killWordsOf(s, z)
				now = kw[:]
			} else {
				for _, w := range zoneWords(s, z) {
					now = append(now, uint32(w))
				}
			}
		}
		edges := c.edges[k]
		edgeOnly := k.Kind == world.ScopeRoomEdge || k.Kind == world.ScopeKill
		for _, w := range m.Words {
			switch {
			case w.Bits&^c.keep(k, int(w.Word)) != 0:
				t.Fatalf("%s word %d bits 0x%X: the filter refuses 0x%X", k, w.Word, w.Bits, w.Bits&^c.keep(k, int(w.Word)))
			case int(w.Word) >= len(now) || w.Bits&^now[w.Word] != 0:
				t.Fatalf("%s word %d bits 0x%X: the game holds %v", k, w.Word, w.Bits, now)
			case edgeOnly && w.Bits&^edges[w.Word] != 0:
				t.Fatalf("%s word %d bits 0x%X: only 0x%X were set since the last tick", k, w.Word, w.Bits, edges[w.Word])
			}
		}
	}
}

// shareGame drives dr through one random op of a storm: a switch or a
// kill in Link's room, a room change, a stage load (sometimes at a
// negative start point, which keeps the zones), a trip through file
// select, or frames.
func shareGame(t *testing.T, dr *differRig, c *sharedCheck, op, a, b int) {
	t.Helper()
	stages := []fakegame.Stage{outset, drc, drcBoss, woods}
	s, err := state.Read(dr.m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	room := int(s.StayNo)
	switch op % 6 {
	case 0, 1:
		no := 0x80 + a%0x70
		_, bit, _ := addr.SwitchBit(no)
		if had := dr.read(switchAddr(dr, no, room), 1); had != nil && had[0]&bit.Mask == 0 {
			if dr.g.OnSwitch(no, room) == nil && no >= 0xE0 {
				c.set(s, no, room, 0)
			}
		}
	case 2:
		id := a % addr.ZoneActorCount
		if dr.g.OnActor(id, room) == nil {
			c.set(s, -1, room, id)
		}
	case 3:
		dr.enter(a%addr.RoomCount, b%addr.RoomCount)
	case 4:
		st := stages[a%len(stages)]
		if b%4 == 0 {
			st.Point = -1
		}
		if b%7 == 0 {
			if err := dr.g.Save(); err != nil {
				t.Fatal(err)
			}
			dr.g.EnterFileSelect()
			if err := dr.g.LoadFile(0); err != nil {
				t.Fatal(err)
			}
		}
		dr.load(st)
	default:
		dr.g.Frames(a % 5)
	}
}

// shareStormVerdict grants about two switches in three, by table and
// switch.
func shareStormVerdict(table int, stage string, room, no int) bool {
	return (table+no)%3 != 0
}

// TestDifferShareStorm: through a random walk of switches, kills and
// transitions, a sharing Differ proposes only bits the game holds now in
// Link's scope and the filter takes, and room switches and kills only as
// they rise (sharedCheck).
func TestDifferShareStorm(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	dr := newDifferRig(t, world.FullPolicy())
	keep := hub.ScopeFilter(shareStormVerdict)
	dr.d.ShareScopes(keep)
	c := newSharedCheck(keep)
	dr.load(drc)
	proposed := 0
	for range 3000 {
		shareGame(t, dr, c, rng.IntN(12), rng.IntN(256), rng.IntN(256))
		if rng.IntN(3) == 0 {
			e := dr.tick()
			proposed += len(e.Scoped)
			c.tick(t, dr, e)
		}
	}
	if proposed == 0 {
		t.Fatal("the storm proposed nothing: the test tests nothing")
	}
}

// FuzzDifferShare is TestDifferShareStorm on arbitrary ops: [op][a][b]
// per game op, op 0xFF a tick.
func FuzzDifferShare(f *testing.F) {
	f.Add([]byte{0, 0x40, 0, 0xFF, 0, 0, 0, 0, 0x60, 0, 0xFF, 3, 1, 0, 0xFF, 0, 0x42, 0, 0xFF, 4, 1, 4, 0xFF, 0xFF})
	f.Add([]byte{4, 1, 0, 0xFF, 0, 0x01, 0, 0, 0x41, 0, 2, 9, 0, 0xFF, 3, 2, 1, 0xFF, 3, 0, 2, 0xFF, 4, 2, 0, 0xFF})
	f.Add([]byte{4, 1, 7, 0xFF, 0, 0x05, 0, 0xFF, 4, 0, 4, 0xFF, 4, 1, 4, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, ops []byte) {
		dr := newDifferRig(t, world.FullPolicy())
		keep := hub.ScopeFilter(shareStormVerdict)
		dr.d.ShareScopes(keep)
		c := newSharedCheck(keep)
		for i := 0; i < len(ops); i++ {
			if ops[i] == 0xFF {
				c.tick(t, dr, dr.tick())
				continue
			}
			arg := func(j int) int {
				if j < len(ops) {
					return int(ops[j])
				}
				return 0
			}
			shareGame(t, dr, c, int(ops[i]), arg(i+1), arg(i+2))
			i += 2
		}
	})
}
