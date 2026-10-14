package world

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

const (
	playerA Player = 1
	playerB Player = 2
)

func at(stage string, table, room int) Place { return Place{Stage: stage, Table: table, Room: room} }

func danKey(stage string, table int) Key {
	return Key{Kind: ScopeDan, Stage: stage, Table: table, Room: -1}
}

func roomKey(kind ScopeKind, stage string, table, room int) Key {
	return Key{Kind: kind, Stage: stage, Table: table, Room: room}
}

func mustApply(t *testing.T, s *Scopes, origin Player, k Key, words ...Word) Scoped {
	t.Helper()
	r, err := s.Apply(origin, k, words)
	if err != nil {
		t.Fatalf("Apply(%d, %s): %v", origin, k, err)
	}
	return r
}

// TestScopeLifetimes walks two players through Dragon Roost Cavern and
// its boss stage (both save table 3) and checks what each store keeps,
// relays, sends on entry and drops, per the game's own lifetimes (see
// scope.go).
func TestScopeLifetimes(t *testing.T) {
	const drc, boss = "M_NewD2", "M_DragB"
	s := NewScopes()
	s.SetPresence(playerA, at(drc, 3, 1))
	s.SetPresence(playerB, at(drc, 3, 1))

	if r := mustApply(t, s, playerA, danKey(drc, 3), Word{0, 1 << 1}); !slices.Equal(r.To, []Player{playerB}) {
		t.Errorf("DAN relayed to %v", r.To)
	}
	mustApply(t, s, playerA, roomKey(ScopeZone, drc, 3, 1), Word{0, 1 << 2})
	// A kill in the next room: the killer reports every loaded room's
	// zone, and the stage's players get it.
	if r := mustApply(t, s, playerA, roomKey(ScopeKill, drc, 3, 2), Word{3, 1 << 5}); !slices.Equal(r.To, []Player{playerB}) {
		t.Errorf("KILL relayed to %v", r.To)
	}

	// B steps into room 2: no zone there yet, the stage's kills already
	// reached B, and A keeps room 1's zone.
	if m := s.SetPresence(playerB, at(drc, 3, 2)); len(m.Enter) != 0 || len(m.Dropped) != 0 {
		t.Errorf("B to room 2: %+v", m)
	}
	// A leaves room 1, the last one in it: its zone store goes.
	if m := s.SetPresence(playerA, at(drc, 3, 3)); !slices.Equal(m.Dropped, []Key{roomKey(ScopeZone, drc, 3, 1)}) {
		t.Errorf("A to room 3 dropped %v", m.Dropped)
	}
	// A takes the boss door: same table, so the dan store stays, and B
	// still keeps the stage's kills.
	if m := s.SetPresence(playerA, at(boss, 3, 0)); len(m.Enter) != 0 || len(m.Dropped) != 0 {
		t.Errorf("A to the boss stage: %+v", m)
	}
	// B follows: nobody is in M_NewD2 any more, so its kills go, but the
	// table (and its dan switches) is still loaded.
	if m := s.SetPresence(playerB, at(boss, 3, 0)); !slices.Equal(m.Dropped, []Key{roomKey(ScopeKill, drc, 3, 2)}) {
		t.Errorf("B to the boss stage dropped %v", m.Dropped)
	}
	// Back in M_NewD2, B gets the stage's dan switches on entry.
	m := s.SetPresence(playerB, at(drc, 3, 1))
	if want := []Entry{{Key: danKey(drc, 3), Words: []Word{{0, 1 << 1}}}}; !slices.EqualFunc(m.Enter, want, entryEqual) {
		t.Errorf("B back in DRC got %+v, want %+v", m.Enter, want)
	}
	// Both on the sea: table 0 clears the dungeon's switches in the game
	// (dSv_danBit_c::init), and the store goes with them.
	s.SetPresence(playerA, at("sea", 0, 11))
	if m := s.SetPresence(playerB, at("sea", 0, 11)); !slices.Equal(m.Dropped, []Key{danKey(drc, 3)}) {
		t.Errorf("both on the sea dropped %v", m.Dropped)
	}
	if ks := s.Keys(); len(ks) != 0 {
		t.Errorf("stores left: %v", ks)
	}

	// A disconnect is a leave.
	mustApply(t, s, playerA, roomKey(ScopeKill, "sea", 0, 11), Word{0, 1})
	if d := s.Leave(playerA); len(d) != 0 {
		t.Errorf("B still keeps the sea's kills, dropped %v", d)
	}
	if d := s.Leave(playerB); !slices.Equal(d, []Key{roomKey(ScopeKill, "sea", 0, 11)}) {
		t.Errorf("last leave dropped %v", d)
	}
}

func entryEqual(a, b Entry) bool { return a.Key == b.Key && slices.Equal(a.Words, b.Words) }

// TestScopeEntryContent: a player entering a stage gets its dan store and
// every room's kills; entering a room gets that room's zone.
func TestScopeEntryContent(t *testing.T) {
	s := NewScopes()
	s.SetPresence(playerA, at("M_Dai", 6, 4))
	mustApply(t, s, playerA, danKey("M_Dai", 6), Word{1, 0x80000000})
	mustApply(t, s, playerA, roomKey(ScopeZone, "M_Dai", 6, 4), Word{1, 0x8000})
	mustApply(t, s, playerA, roomKey(ScopeKill, "M_Dai", 6, 5), Word{15, 1})
	mustApply(t, s, playerA, roomKey(ScopeKill, "M_Dai", 6, 4), Word{0, 1}, Word{0, 2})

	m := s.SetPresence(playerB, at("M_Dai", 6, 4))
	want := []Entry{
		{Key: danKey("M_Dai", 6), Words: []Word{{1, 0x80000000}}},
		{Key: roomKey(ScopeKill, "M_Dai", 6, 4), Words: []Word{{0, 3}}},
		{Key: roomKey(ScopeKill, "M_Dai", 6, 5), Words: []Word{{15, 1}}},
		{Key: roomKey(ScopeZone, "M_Dai", 6, 4), Words: []Word{{1, 0x8000}}},
	}
	if !slices.EqualFunc(m.Enter, want, entryEqual) {
		t.Errorf("entry got %+v\nwant %+v", m.Enter, want)
	}
	// Re-sending a presence that did not move sends nothing again.
	if m := s.SetPresence(playerB, at("M_Dai", 6, 4)); len(m.Enter) != 0 {
		t.Errorf("same place again sent %+v", m.Enter)
	}
	// Idempotent: the same words again change nothing and go nowhere.
	if r := mustApply(t, s, playerA, roomKey(ScopeKill, "M_Dai", 6, 4), Word{0, 3}); len(r.Delta) != 0 || r.To != nil {
		t.Errorf("repeat kill: %+v", r)
	}
	if r := mustApply(t, s, playerB, roomKey(ScopeKill, "M_Dai", 6, 4), Word{0, 7}); !slices.Equal(r.Delta, []Word{{0, 4}}) || !slices.Equal(r.To, []Player{playerA}) {
		t.Errorf("new kill bit: %+v", r)
	}
}

// TestScopeRoomEdge: the one-shot room switches go to the players in that
// room at that moment and are never kept.
func TestScopeRoomEdge(t *testing.T) {
	s := NewScopes()
	s.SetPresence(playerA, at("M_NewD2", 3, 7))
	s.SetPresence(playerB, at("M_NewD2", 3, 8))
	k := roomKey(ScopeRoomEdge, "M_NewD2", 3, 7)
	if r := mustApply(t, s, playerA, k, Word{2, 0x0001}); !slices.Equal(r.Delta, []Word{{2, 1}}) || r.To != nil {
		t.Errorf("edge with nobody else in the room: %+v", r)
	}
	s.SetPresence(playerB, at("M_NewD2", 3, 7))
	if r := mustApply(t, s, playerA, k, Word{2, 0x0001}); !slices.Equal(r.To, []Player{playerB}) {
		t.Errorf("edge relayed to %v", r.To)
	}
	if ks := s.Keys(); len(ks) != 0 {
		t.Errorf("an edge was stored: %v", ks)
	}
}

// TestScopeApplyRefuses: a malformed key or word fails with ErrScope
// (these would halt the game's setters, d_save.cpp:1540-1701), and a
// player reporting for a scope it is not in fails with ErrNotPresent.
func TestScopeApplyRefuses(t *testing.T) {
	s := NewScopes()
	s.SetPresence(playerA, at("M_NewD2", 3, 1))
	for _, c := range []struct {
		name  string
		k     Key
		words []Word
		want  error
	}{
		{"unknown kind", Key{Kind: 9, Stage: "M_NewD2", Table: 3, Room: 1}, nil, ErrScope},
		{"no stage", roomKey(ScopeZone, "", 3, 1), nil, ErrScope},
		{"stage too long", roomKey(ScopeZone, "M_NewD2x", 3, 1), nil, ErrScope},
		{"stage with a space", roomKey(ScopeZone, "M New", 3, 1), nil, ErrScope},
		{"table 16", roomKey(ScopeZone, "M_NewD2", 16, 1), nil, ErrScope},
		{"dan with a room", Key{Kind: ScopeDan, Stage: "M_NewD2", Table: 3, Room: 1}, nil, ErrScope},
		{"zone without a room", roomKey(ScopeZone, "M_NewD2", 3, -1), nil, ErrScope},
		{"room 64", roomKey(ScopeKill, "M_NewD2", 3, 64), nil, ErrScope},
		{"dan word 2", danKey("M_NewD2", 3), []Word{{2, 1}}, ErrScope},
		{"zone word 2 is the room edge", roomKey(ScopeZone, "M_NewD2", 3, 1), []Word{{2, 1}}, ErrScope},
		{"room edge word 0", roomKey(ScopeRoomEdge, "M_NewD2", 3, 1), []Word{{0, 1}}, ErrScope},
		{"kill word 16", roomKey(ScopeKill, "M_NewD2", 3, 1), []Word{{16, 1}}, ErrScope},
		{"zone words are u16", roomKey(ScopeZone, "M_NewD2", 3, 1), []Word{{0, 0x10000}}, ErrScope},
		{"another stage's dan", danKey("M_DragB", 3), []Word{{0, 1}}, ErrNotPresent},
		{"another room's zone", roomKey(ScopeZone, "M_NewD2", 3, 2), []Word{{0, 1}}, ErrNotPresent},
		{"another table", roomKey(ScopeKill, "M_NewD2", 4, 1), []Word{{0, 1}}, ErrNotPresent},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Apply(playerA, c.k, c.words); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
	if _, err := s.Apply(playerB, roomKey(ScopeKill, "M_NewD2", 3, 1), []Word{{0, 1}}); !errors.Is(err, ErrNotPresent) {
		t.Errorf("unknown player: %v", err)
	}
	for _, p := range []Place{at("sea_T", 0, 0), at("Name", 0, 0), at("sea", -1, 0), at("sea", 16, 0), Nowhere} {
		s.SetPresence(playerB, p)
		if _, err := s.Apply(playerB, roomKey(ScopeKill, p.Stage, 0, 0), []Word{{0, 1}}); err == nil {
			t.Errorf("a player at %+v stored a kill", p)
		}
	}
	if ks := s.Keys(); len(ks) != 0 {
		t.Errorf("refused applies left stores: %v", ks)
	}
}

// TestScopeFilter: bits the filter leaves out are refused and never kept.
func TestScopeFilter(t *testing.T) {
	s := NewScopes()
	s.Filter = func(k Key, w int) uint32 {
		if k.Kind == ScopeDan && w == 0 {
			return 0x0000FFFF
		}
		return 0xFFFFFFFF
	}
	s.SetPresence(playerA, at("M_NewD2", 3, 1))
	r := mustApply(t, s, playerA, danKey("M_NewD2", 3), Word{0, 0x00030003}, Word{1, 1})
	if !slices.Equal(r.Delta, []Word{{0, 3}, {1, 1}}) || !slices.Equal(r.Filtered, []Word{{0, 0x00030000}}) {
		t.Errorf("filtered apply: %+v", r)
	}
	if got := s.Get(danKey("M_NewD2", 3)); !slices.Equal(got, []Word{{0, 3}, {1, 1}}) {
		t.Errorf("store holds %v", got)
	}
	s.Filter = func(Key, int) uint32 { return 0 }
	if r := mustApply(t, s, playerA, roomKey(ScopeKill, "M_NewD2", 3, 1), Word{0, 1}); len(r.Delta) != 0 {
		t.Errorf("a fully filtered apply: %+v", r)
	}
	if got := s.Get(roomKey(ScopeKill, "M_NewD2", 3, 1)); got != nil {
		t.Errorf("an empty store was kept: %v", got)
	}
}

// TestScopeInvariants moves random players around random places and
// checks, after every step, that each store is kept alive by someone,
// holds no empty word, and relays go to exactly the other players it
// reaches.
func TestScopeInvariants(t *testing.T) {
	stages := []struct {
		name  string
		table int
	}{{"M_NewD2", 3}, {"M_DragB", 3}, {"sea", 0}, {"kindan", 4}}
	r := rand.New(rand.NewPCG(3, 4))
	s := NewScopes()
	players := []Player{1, 2, 3}
	for step := 0; step < 5000; step++ {
		p := players[r.IntN(len(players))]
		switch r.IntN(10) {
		case 0:
			s.Leave(p)
		case 1, 2, 3:
			st := stages[r.IntN(len(stages))]
			s.SetPresence(p, at(st.name, st.table, r.IntN(4)))
		default:
			place, ok := s.PlaceOf(p)
			if !ok || !place.inStage() {
				continue
			}
			kind := []ScopeKind{ScopeDan, ScopeZone, ScopeRoomEdge, ScopeKill}[r.IntN(4)]
			k := Key{Kind: kind, Stage: place.Stage, Table: place.Table, Room: place.Room}
			first, last, width, _ := kind.words()
			if kind == ScopeDan {
				k.Room = -1
			} else if kind == ScopeKill {
				k.Room = r.IntN(4) // any loaded room of the stage
			}
			res, err := s.Apply(p, k, []Word{{uint8(first + r.IntN(last-first+1)), r.Uint32() & width}})
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			for _, q := range players {
				qa, ok := s.PlaceOf(q)
				want := ok && q != p && k.reaches(qa) && len(res.Delta) > 0
				if slices.Contains(res.To, q) != want {
					t.Fatalf("step %d: %s relay to %d at %s: %v", step, k, q, qa, res.To)
				}
			}
		}
		for _, k := range s.Keys() {
			alive := false
			for _, q := range players {
				if qa, ok := s.PlaceOf(q); ok && k.keeps(qa) {
					alive = true
				}
			}
			ws := s.Get(k)
			if !alive || len(ws) == 0 {
				t.Fatalf("step %d: store %s alive %v words %v", step, k, alive, ws)
			}
		}
		if err := checkIndex(s); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
}

// checkIndex reports how s's indexes disagree with its stores: every store
// is in the set of its stage (ZONE, KILL) or table (DAN), every key in a
// set is a store, and no set is empty.
func checkIndex(s *Scopes) error {
	n := 0
	for g, ix := range s.byStage {
		if len(ix) == 0 {
			return fmt.Errorf("an empty index for %s table %d", g.stage, g.table)
		}
		for k := range ix {
			if s.entries[k] == nil || k.Kind == ScopeDan || k.Stage != g.stage || k.Table != g.table {
				return fmt.Errorf("%s indexed under %s table %d", k, g.stage, g.table)
			}
		}
		n += len(ix)
	}
	for t, ix := range s.byTable {
		if ix != nil && len(ix) == 0 {
			return fmt.Errorf("an empty index for table %d", t)
		}
		for k := range ix {
			if s.entries[k] == nil || k.Kind != ScopeDan || k.Table != t {
				return fmt.Errorf("%s indexed under table %d", k, t)
			}
		}
		n += len(ix)
	}
	if n != len(s.entries) {
		return fmt.Errorf("%d stores, %d indexed", len(s.entries), n)
	}
	return nil
}

// TestScopeDanBound: a player naming stage after stage within one table
// makes at most MaxDanStores DAN stores there. Past that a new one is
// refused (ErrScopesFull), while the stores held go on taking bits and
// other tables are unaffected.
func TestScopeDanBound(t *testing.T) {
	s := NewScopes()
	s.SetPresence(playerB, at("M_NewD2", 3, 1)) // keeps table 3's stores alive
	for i := range MaxDanStores + 50 {
		st := fmt.Sprintf("Z%06d", i)
		s.SetPresence(playerA, at(st, 3, 0))
		_, err := s.Apply(playerA, danKey(st, 3), []Word{{0, 1}})
		if want := i < MaxDanStores; want != (err == nil) || err != nil && !errors.Is(err, ErrScopesFull) {
			t.Fatalf("DAN store %d: %v", i+1, err)
		}
	}
	if n := len(s.Keys()); n != MaxDanStores {
		t.Fatalf("%d stores held, want %d", n, MaxDanStores)
	}
	s.SetPresence(playerA, at("Z000000", 3, 0))
	if r := mustApply(t, s, playerA, danKey("Z000000", 3), Word{0, 2}); !slices.Equal(r.Delta, []Word{{0, 2}}) || len(r.To) != 0 {
		t.Fatalf("a store held refused a new bit: %+v", r)
	}
	s.SetPresence(playerA, at("kindan", 4, 0))
	mustApply(t, s, playerA, danKey("kindan", 4), Word{0, 1})
	// Once nobody is in table 3 its stores go, and it takes new ones.
	if m := s.SetPresence(playerB, at("kindan", 4, 0)); len(m.Dropped) != MaxDanStores {
		t.Fatalf("leaving table 3 dropped %d stores, want %d", len(m.Dropped), MaxDanStores)
	}
	s.SetPresence(playerA, at("M_NewD2", 3, 1))
	mustApply(t, s, playerA, danKey("M_NewD2", 3), Word{0, 1})
	if err := checkIndex(s); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkScopePresence moves one player between two rooms while every
// table holds MaxDanStores DAN stores: a move costs what is near it, not
// what is held.
func BenchmarkScopePresence(b *testing.B) {
	s := NewScopes()
	for tbl := range 16 {
		s.SetPresence(Player(tbl+1), at("M_NewD2", tbl, 1))
		for i := range MaxDanStores {
			st := fmt.Sprintf("Z%06d", i)
			s.SetPresence(Player(tbl+1), at(st, tbl, 0))
			if _, err := s.Apply(Player(tbl+1), danKey(st, tbl), []Word{{0, 1}}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for i := range b.N {
		s.SetPresence(100, at("sea", 0, i%2))
	}
}

// TestSwitchFilter: bit b of each word is the switch number the game's
// setter uses, and only synced switches pass, room edges included.
func TestSwitchFilter(t *testing.T) {
	synced := map[int]bool{0x81: true, 0xBF: true, 0xC3: true, 0xDF: true, 0xE1: true}
	var asked []Key
	f := SwitchFilter(func(k Key, no int) bool {
		if no == 0x80 {
			asked = append(asked, k)
		}
		return synced[no]
	})
	dan, zone, edge, kill := danKey("M_NewD2", 3), roomKey(ScopeZone, "M_NewD2", 3, 1), roomKey(ScopeRoomEdge, "M_NewD2", 3, 1), roomKey(ScopeKill, "M_NewD2", 3, 1)
	for _, c := range []struct {
		k    Key
		w    int
		want uint32
	}{
		{dan, 0, 1 << 1}, {dan, 1, 1 << 31}, {zone, 0, 1 << 3}, {zone, 1, 1 << 15}, {edge, 2, 1 << 1}, {kill, 7, 0xFFFFFFFF},
	} {
		if got := f(c.k, c.w); got != c.want {
			t.Errorf("%s word %d: 0x%08X, want 0x%08X", c.k, c.w, got, c.want)
		}
	}
	if !slices.Equal(asked, []Key{dan}) {
		t.Errorf("switch 0x80 asked for %v", asked)
	}

	s := NewScopes()
	s.Filter = f
	s.SetPresence(playerA, at("M_NewD2", 3, 1))
	s.SetPresence(playerB, at("M_NewD2", 3, 1))
	if r := mustApply(t, s, playerA, edge, Word{2, 0x0003}); !slices.Equal(r.Delta, []Word{{2, 2}}) || !slices.Equal(r.Filtered, []Word{{2, 1}}) {
		t.Errorf("room edge through the filter: %+v", r)
	}
}
