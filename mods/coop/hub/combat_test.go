package hub

import (
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
)

// TestHubKillFilter: KillFilter asks the verdict with the store's table,
// stage, room and each bit's setID, for KILL keys only; Filter routes the
// kinds; a nil verdict keeps nothing.
func TestHubKillFilter(t *testing.T) {
	type ask struct {
		table int
		stage string
		room  int
		setID int
	}
	var asked []ask
	kill := func(table int, stage string, room, setID int) bool {
		asked = append(asked, ask{table, stage, room, setID})
		return setID == 33 || setID == 511
	}
	f := Filter(syncAll, kill)
	for _, c := range []struct {
		k     world.Key
		w     int
		want  uint32
		first ask
	}{
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}, 1, 1 << 1, ask{3, drc, 1, 32}},
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}, 15, 1 << 31, ask{3, drc, 1, 480}},
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}, 0, 0, ask{3, drc, 1, 0}},
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}, 16, 0, ask{}},     // past the 512 bits
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 64}, 1, 0, ask{}},     // onActor asserts
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: -1}, 1, 0, ask{}},     // a stage.dzs entry, never recorded
		{world.Key{Kind: world.ScopeKill, Stage: drc, Table: 16, Room: 1}, 1, 0, ask{}},     // no save table
		{world.Key{Kind: world.ScopeZone, Stage: drc, Table: 3, Room: 1}, 0, 0xFFFF, ask{}}, // a switch: the scoped verdict's
	} {
		asked = nil
		if got := f(c.k, c.w); got != c.want {
			t.Errorf("filter(%s, %d) = 0x%X, want 0x%X", c.k, c.w, got, c.want)
		}
		switch {
		case c.first == (ask{}) && len(asked) != 0:
			t.Errorf("filter(%s, %d) asked the kill verdict %+v", c.k, c.w, asked)
		case c.first != (ask{}) && (len(asked) == 0 || asked[0] != c.first):
			t.Errorf("filter(%s, %d) first asked %+v, want %+v", c.k, c.w, asked, c.first)
		}
	}
	none := Filter(syncAll, nil)
	if got := none(world.Key{Kind: world.ScopeKill, Stage: drc, Table: 3, Room: 1}, 1); got != 0 {
		t.Errorf("a nil kill verdict keeps 0x%X", got)
	}
	if got := KillFilter(kill)(world.Key{Kind: world.ScopeDan, Stage: drc, Table: 3, Room: -1}, 1); got != 0 {
		t.Errorf("KillFilter keeps 0x%X of a dan word", got)
	}
}

// TestHubKillStoreLifetime: a kill bit the verdict refuses is dropped and
// counted, one it grants relayed and kept while anyone is in the stage
// (whatever room), and sent to a player entering it; a host without a kill
// verdict keeps none and says combat is not allowed.
func TestHubKillStoreLifetime(t *testing.T) {
	cfg := fullCfg()
	cfg.SyncedKill = func(table int, stage string, room, setID int) bool { return setID == 2 }
	r := newRig(t, cfg)
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 2))
	a.take(t)
	b.take(t)
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, wire.ScopedWord{Word: 0, Bits: 1<<2 | 1<<1}))
	got := pick[*wire.Scoped](b.take(t))
	if len(got) != 1 || got[0].Kind != wire.ScopeKill || !slices.Equal(got[0].Words, []wire.ScopedWord{{Word: 0, Bits: 1 << 2}}) {
		t.Fatalf("bob, in another room of the stage, got %+v, want setID 2 alone", got)
	}
	st := r.status()
	if !st.CombatAllowed || st.KillStores != 1 {
		t.Errorf("status: combat %v, %d kill stores", st.CombatAllowed, st.KillStores)
	}
	for _, p := range st.Peers {
		if want := map[string]int{"alice": 1, "bob": 0}[p.Name]; p.ScopedRefused != want {
			t.Errorf("%s: %d scoped bits refused, want %d", p.Name, p.ScopedRefused, want)
		}
	}
	// Alice leaves the stage, Bob stays: the store lives. A newcomer
	// entering the stage gets it.
	r.send(a, presence("sea", 0, 11))
	c := r.join(newConn(3, "carol"))
	c.take(t)
	r.send(c, presence(drc, 3, 5))
	if got := pick[*wire.Scoped](c.take(t)); len(got) != 1 || got[0].Kind != wire.ScopeKill || got[0].Room != 1 {
		t.Fatalf("the newcomer got %+v, want the kill store of room 1", got)
	}
	// Everyone gone: dropped.
	r.send(b, presence("sea", 0, 11))
	r.send(c, presence("sea", 0, 11))
	if st := r.status(); st.KillStores != 0 {
		t.Errorf("%d kill stores with the stage empty", st.KillStores)
	}

	cfg = fullCfg()
	cfg.SyncedKill = nil
	r = newRig(t, cfg)
	a = r.join(newConn(1, "alice"))
	b = r.join(newConn(2, "bob"))
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 1))
	a.take(t)
	b.take(t)
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, wire.ScopedWord{Word: 0, Bits: 1 << 2}))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
		t.Fatalf("a host without a kill verdict relayed %+v", got)
	}
	if st := r.status(); st.CombatAllowed || st.KillStores != 0 {
		t.Errorf("without a verdict: combat %v, %d kill stores", st.CombatAllowed, st.KillStores)
	}
}

// TestHubCombatNeedsEqualTables: FeatureCombat is a table feature, so a
// peer whose stage tables are not the host's is not granted it, neither
// relays nor gets kills, and its SCOPED KILL is refused.
func TestHubCombatNeedsEqualTables(t *testing.T) {
	if TableFeatures&FeatureCombat == 0 {
		t.Fatal("combat is no table feature")
	}
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := newConn(2, "bob")
	r.attach(b)
	m := hello(wire.FileBound, testWorld, 2)
	m.StageTblHash ^= 1
	w := welcomeOf(t, r.exchange(b, m))
	if w.Status != wire.StatusOK || Feature(w.Features)&FeatureCombat != 0 {
		t.Fatalf("bob with other tables: %s %s", w.Status, Feature(w.Features))
	}
	r.send(a, presence(drc, 3, 1))
	r.send(b, presence(drc, 3, 1))
	a.take(t)
	b.take(t)
	r.send(a, scoped(wire.ScopeKill, drc, 3, 1, wire.ScopedWord{Word: 0, Bits: 1}))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 0 {
		t.Fatalf("bob without combat got %+v", got)
	}
	if got := codes(r.exchange(b, scoped(wire.ScopeKill, drc, 3, 1, wire.ScopedWord{Word: 0, Bits: 2}))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
		t.Fatalf("bob's kill without combat: %v", got)
	}
	if got := pick[*wire.Scoped](a.take(t)); len(got) != 0 {
		t.Fatalf("alice got bob's refused kill: %+v", got)
	}
}
