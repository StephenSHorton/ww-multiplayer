package hub

import (
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// testSynced is a host's M1 verdict for the tests: in save table 3,
// switch 0x05 latches and 0x06 (in the same image byte) doesn't; in table
// 4, 0x05 latches too; table 0's switch 0 and table 15's 0x7F latch.
func testSynced(t, no int) bool {
	switch t {
	case 3, 4:
		return no == 0x05
	case 0:
		return no == 0
	case 15:
		return no == 0x7F
	}
	return false
}

// switchGain is the image byte and bit memory switch no of save table t
// sets (dSv_memBit_c::onSwitch, d_save.cpp:1084-1087).
func switchGain(t *testing.T, table, no int) world.Gain {
	t.Helper()
	store, b, err := addr.SwitchBit(no)
	if err != nil || store != addr.SwitchMemory {
		t.Fatalf("switch 0x%X: %v %v", no, store, err)
	}
	return world.Gain{Off: uint16(addr.OffMemBit(table) + b.Off), Val: b.Mask}
}

func TestIsSwitchByte(t *testing.T) {
	for _, c := range []struct {
		off  int
		want bool
	}{
		{addr.OffMemBit(0) + addr.MemBitOffTbox + 3, false},
		{addr.OffMemBit(0) + addr.MemBitOffSwitch, true},
		{addr.OffMemBit(0) + addr.MemBitOffItem - 1, true},
		{addr.OffMemBit(0) + addr.MemBitOffItem, false},
		{addr.OffMemBit(7) + addr.MemBitOffSwitch + 9, true},
		{addr.OffMemBit(15) + addr.MemBitOffItem - 1, true},
		{addr.OffMemBit(15) + addr.MemBitOffDungeonItem, false},
		{addr.OffMemBit(16) + addr.MemBitOffSwitch, false}, // mOcean
		{addr.OffMemory - 1, false},
		{addr.OffGetItem, false},
		{-1, false},
		{world.ImageSize, false},
	} {
		if got := IsSwitchByte(c.off); got != c.want {
			t.Errorf("IsSwitchByte(0x%X) = %v, want %v", c.off, got, c.want)
		}
	}
	n := 0
	for off := range world.ImageSize {
		if IsSwitchByte(off) {
			n++
		}
	}
	if want := addr.TableCount * addr.MemBitSwitchCount / 8; n != want {
		t.Errorf("%d switch bytes, want %d", n, want)
	}
}

// TestSwitchPolicy: the memory switch bytes open exactly the switches the
// verdict names; every other byte is the base policy's.
func TestSwitchPolicy(t *testing.T) {
	var want world.Policy
	for table := range addr.TableCount {
		for no := range addr.MemBitSwitchCount {
			if testSynced(table, no) {
				g := switchGain(t, table, no)
				want[g.Off] |= g.Val
			}
		}
	}
	full := world.FullPolicy()
	fullCopy := *full
	for _, c := range []struct {
		name   string
		base   *world.Policy
		synced func(int, int) bool
		other  *world.Policy // the bytes that are not memory switches
		sw     *world.Policy // the memory switch bytes
	}{
		{"default base", nil, testSynced, world.DefaultPolicy(), &want},
		{"a full base loses its switches", full, testSynced, full, &want},
		{"no verdict opens none", full, nil, full, &world.Policy{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := SwitchPolicy(c.base, c.synced)
			for off := range world.ImageSize {
				exp := c.other[off]
				if IsSwitchByte(off) {
					exp = c.sw[off]
				}
				if p[off] != exp {
					t.Errorf("byte 0x%03X (%s) = %02x, want %02x", off, classField(off), p[off], exp)
				}
			}
		})
	}
	if *full != fullCopy {
		t.Error("SwitchPolicy changed its base")
	}
}

// switchRig is a hub over a host whose M1 verdict is testSynced, with
// alice's tables the host's and bob's another disc's.
func switchRig(t *testing.T) (r *rig, alice, bob *fakeConn) {
	t.Helper()
	r = newRig(t, Config{Features: DefaultFeatures, SyncedSwitch: testSynced})
	alice = r.join(newConn(1, "alice"))
	bob = newConn(2, "bob")
	r.attach(bob)
	m := hello(wire.FileBound, testWorld, 2)
	m.StageTblHash = testTables ^ 1
	ms := r.exchange(bob, m)
	if w := welcomeOf(t, ms); w.Status != wire.StatusOK || Feature(w.Features) != DefaultFeatures&^TableFeatures {
		t.Fatalf("bob's welcome %+v", w)
	}
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeSchema}) {
		t.Fatalf("bob's notices %v, want SCHEMA", got)
	}
	if w := alice.take(t); len(w) != 0 {
		t.Fatalf("alice got %s", subs(w))
	}
	return r, alice, bob
}

func gainsOf(t *testing.T, ms []wire.Msg) []wire.Gain {
	t.Helper()
	gd := pick[*wire.GainsDown](ms)
	if len(gd) > 1 {
		t.Fatalf("%d GAINS in %s", len(gd), subs(ms))
	}
	if len(gd) == 0 {
		return nil
	}
	return gd[0].Gains
}

// up is a GAINS c->s of gs, in the ascending order wire requires.
func up(batch uint32, gs ...world.Gain) *wire.GainsUp { return gains(batch, sortedGains(gs...)...) }

func sortedGains(gs ...world.Gain) []wire.Gain {
	out := make([]wire.Gain, len(gs))
	for i, g := range gs {
		out[i] = wire.Gain(g)
	}
	slices.SortFunc(out, func(a, b wire.Gain) int { return int(a.Off) - int(b.Off) })
	return out
}

// TestSwitchSync: only the latching switches of the host's table enter
// the world, and only from and to players whose tables are the host's.
func TestSwitchSync(t *testing.T) {
	r, alice, bob := switchRig(t)
	sw5, sw6 := switchGain(t, 3, 0x05), switchGain(t, 3, 0x06)
	if sw5.Off != sw6.Off {
		t.Fatalf("switches 0x05 and 0x06 in bytes 0x%X and 0x%X", sw5.Off, sw6.Off)
	}
	tbox := world.Gain{Off: offTbox(3, 0), Val: 0x01}

	// Alice: 0x05 latches and merges; 0x06 is refused (quietly, counted).
	r.send(alice, up(1, world.Gain{Off: sw5.Off, Val: sw5.Val | sw6.Val}, tbox))
	if got, want := gainsOf(t, alice.take(t)), sortedGains(sw5, tbox); !slices.Equal(got, want) {
		t.Errorf("alice's relay %v, want %v", got, want)
	}
	if got, want := gainsOf(t, bob.take(t)), sortedGains(tbox); !slices.Equal(got, want) {
		t.Errorf("bob's relay %v, want %v (no switch byte)", got, want)
	}
	img := r.store.Image()
	if img[sw5.Off] != sw5.Val || img[tbox.Off] != tbox.Val {
		t.Errorf("world switch byte %02x, tbox %02x; want %02x %02x", img[sw5.Off], img[tbox.Off], sw5.Val, tbox.Val)
	}
	if p := r.peer(1); p.Rejected != 1 {
		t.Errorf("alice's rejected %d, want 1 (switch 0x06)", p.Rejected)
	}

	// Bob: his switch is dropped even though it latches; the rest merges.
	sw45 := switchGain(t, 4, 0x05)
	item := world.Gain{Off: uint16(addr.OffMemBit(4) + addr.MemBitOffItem), Val: 0x80}
	r.send(bob, up(1, sw45, item))
	if got, want := gainsOf(t, bob.take(t)), sortedGains(item); !slices.Equal(got, want) {
		t.Errorf("bob's relay %v, want %v", got, want)
	}
	if got, want := gainsOf(t, alice.take(t)), sortedGains(item); !slices.Equal(got, want) {
		t.Errorf("alice's relay %v, want %v", got, want)
	}
	if img := r.store.Image(); img[sw45.Off] != 0 {
		t.Errorf("bob's switch merged: %02x", img[sw45.Off])
	}
	if p := r.peer(2); p.Rejected != 1 {
		t.Errorf("bob's rejected %d, want 1", p.Rejected)
	}

	// A relay of switches alone reaches bob not at all.
	sw0 := switchGain(t, 0, 0)
	r.send(alice, gains(2, wire.Gain(sw0)))
	if got := gainsOf(t, alice.take(t)); !slices.Equal(got, sortedGains(sw0)) {
		t.Errorf("alice's relay %v", got)
	}
	if ms := bob.take(t); len(ms) != 0 {
		t.Errorf("bob got %s for a switch-only change", subs(ms))
	}

	// WORLD: bob's switch bytes are stripped before the merge, and his
	// view of the world never has any; alice's has them.
	r.send(bob, worldMsg(testWorld, r.store.Seq(), sw45, world.Gain{Off: offTbox(5, 1), Val: 0x02}))
	bw := pick[*wire.World](bob.take(t))
	if len(bw) != 1 {
		t.Fatal("bob got no WORLD back")
	}
	for off, b := range bw[0].Image {
		if IsSwitchByte(off) && b != 0 {
			t.Errorf("bob's WORLD has switch byte 0x%03X = %02x", off, b)
		}
	}
	if bw[0].Image[offTbox(5, 1)] != 0x02 {
		t.Error("bob's WORLD lacks his own chest")
	}
	if img := r.store.Image(); img[sw45.Off] != 0 {
		t.Errorf("bob's WORLD merged a switch: %02x", img[sw45.Off])
	}
	if got := gainsOf(t, alice.take(t)); !slices.Equal(got, []wire.Gain{{Off: offTbox(5, 1), Val: 0x02}}) {
		t.Errorf("alice's relay of bob's WORLD %v", got)
	}
	aw := pick[*wire.World](r.exchange(alice, &wire.Ctrl{Op: wire.CtrlResync}))
	if len(aw) != 1 || aw[0].Image[sw5.Off] != sw5.Val || aw[0].Image[sw0.Off] != sw0.Val {
		t.Errorf("alice's RESYNC lacks the switches: %+v", aw)
	}

	// The host turns switch sync off and on: alice is asked to resync, as
	// she missed the switch relays meanwhile.
	if err := r.h.SetFeatures(DefaultFeatures &^ FeatureSwitches); err != nil {
		t.Fatal(err)
	}
	if cs := pick[*wire.Ctrl](alice.take(t)); len(cs) != 1 || Feature(cs[0].Arg) != DefaultFeatures&^FeatureSwitches {
		t.Fatalf("alice after switches off: %+v", cs)
	}
	r.send(alice, gains(3, wire.Gain(switchGain(t, 15, 0x7F))))
	if img := r.store.Image(); img[switchGain(t, 15, 0x7F).Off] != 0 {
		t.Error("a switch merged with switch sync off")
	}
	if err := r.h.SetFeatures(DefaultFeatures); err != nil {
		t.Fatal(err)
	}
	cs := pick[*wire.Ctrl](alice.take(t))
	if len(cs) != 2 || cs[0].Op != wire.CtrlCoopOff || Feature(cs[0].Arg) != DefaultFeatures || cs[1].Op != wire.CtrlResync {
		t.Errorf("alice after switches back on: %+v, want COOP_OFF then RESYNC", cs)
	}
	if cs := pick[*wire.Ctrl](bob.take(t)); len(cs) != 2 || Feature(cs[0].Arg)&TableFeatures != 0 {
		t.Errorf("bob's regrants %+v: still no table features", cs)
	}
}

// TestM3Classes: the stage progress bits, ocean, charts and sea-map bits
// are OR bytes the hub takes under the image alone, from a player with
// switch sync and one without.
func TestM3Classes(t *testing.T) {
	r, alice, bob := switchRig(t)
	mem := func(t, f int) uint16 { return uint16(addr.OffMemBit(t) + f) }
	cases := []struct {
		name string
		g    world.Gain
		want byte
	}{
		{"chest", world.Gain{Off: mem(2, addr.MemBitOffTbox), Val: 0x81}, 0x81},
		{"pickup", world.Gain{Off: mem(6, addr.MemBitOffItem+3), Val: 0x10}, 0x10},
		{"visited room", world.Gain{Off: mem(9, addr.MemBitOffVisited+7), Val: 0xFF}, 0xFF},
		{"dungeon items 0-5", world.Gain{Off: mem(3, addr.MemBitOffDungeonItem), Val: addr.DungeonItemMask}, addr.DungeonItemMask},
		{"salvage", world.Gain{Off: addr.OffOcean + 0x20, Val: 0x44}, 0x44},
		{"charts owned", world.Gain{Off: addr.OffMapGet + 2, Val: 0x08}, 0x08},
		{"sea-map bits", world.Gain{Off: addr.OffFmapBits + 30, Val: 0x03}, 0x03},
		{"Triforce charts", world.Gain{Off: addr.OffMapTriforce, Val: 0x20}, 0x20},
	}
	for i, c := range cases {
		from, to := alice, bob
		if i%2 == 1 {
			from, to = bob, alice
		}
		r.send(from, gains(uint32(10+i), wire.Gain(c.g)))
		want := []wire.Gain{{Off: c.g.Off, Val: c.want}}
		if got := gainsOf(t, to.take(t)); !slices.Equal(got, want) {
			t.Errorf("%s from %s: %s got %v, want %v", c.name, from.name, to.name, got, want)
		}
		from.take(t)
		if got := r.store.Image()[c.g.Off]; got != c.want {
			t.Errorf("%s: world byte %02x, want %02x", c.name, got, c.want)
		}
	}
	for _, id := range []byte{1, 2} {
		if p := r.peer(id); p.Rejected != 0 {
			t.Errorf("%s's rejected %d, want 0", p.Name, p.Rejected)
		}
	}
	// (Dungeon item bits 6-7 can't even be proposed: wire refuses bits
	// outside a byte's class mask.)
	if _, err := wire.Encode(gains(99, wire.Gain{Off: mem(3, addr.MemBitOffDungeonItem), Val: 0xC0})); err == nil {
		t.Error("wire encoded dungeon item bits 6-7")
	}
}

// TestPreflightCountsSwitches: an unbound file's synced switches count
// toward "divergent" whether or not its player takes part in switch sync
// this session: once bound, a later session with the host's tables would
// merge them unjudged (judge). A switch the host's M1 verdict keeps out
// of the world never counts.
func TestPreflightCountsSwitches(t *testing.T) {
	for _, c := range []struct {
		name      string
		tables    uint64
		no        int
		divergent bool
	}{
		{"the host's tables", testTables, 0x05, true},
		{"other tables", testTables ^ 1, 0x05, true},
		{"no tables", 0, 0x05, true},
		{"a switch that doesn't sync", testTables, 0x06, false},
	} {
		sw := switchGain(t, 3, c.no)
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, Config{Features: DefaultFeatures, SyncedSwitch: testSynced})
			u := newConn(1, "unbound")
			r.attach(u)
			m := hello(wire.FileUnbound, world.ID{}, 0)
			m.StageTblHash = c.tables
			r.exchange(u, m)
			ms := r.exchange(u, worldMsg(world.ID{}, 0, sw))
			if got := slices.Contains(codes(ms), wire.NoticeDivergent); got != c.divergent {
				t.Errorf("divergent notice %v, want %v (got %s)", got, c.divergent, subs(ms))
			}
			if r.store.Seq() != 0 {
				t.Error("an unbound WORLD merged")
			}
		})
	}
}

// TestHostWithoutTables: a host with no stage tables shares no switch,
// and tells each player that wants them so, whole in one NOTICE. The hub
// knows only that the host has none, not why, so the NOTICE points to the
// host's log and gives no advice of its own.
func TestHostWithoutTables(t *testing.T) {
	r := startRig(t, Config{Features: DefaultFeatures}, true)
	a := newConn(1, "alice")
	r.attach(a)
	ms := r.exchange(a, hello(wire.FileBound, testWorld, 1))
	if w := welcomeOf(t, ms); w.Status != wire.StatusOK || Feature(w.Features) != DefaultFeatures&^TableFeatures {
		t.Fatalf("welcome %+v", w)
	}
	ns := pick[*wire.Notice](ms)
	if len(ns) != 1 || ns[0].Code != wire.NoticeSchema || !containsAll(ns[0].Text, "the host has no co-op stage tables", "the host's log says why", "memory switches") ||
		strings.HasSuffix(ns[0].Text, "...") || !strings.HasSuffix(ns[0].Text, "the rest do") || len(ns[0].Text) > wire.MaxNoticeLen ||
		strings.Contains(ns[0].Text, "run patch") || strings.Contains(ns[0].Text, "--coop-iso") {
		t.Fatalf("notices %+v", ns)
	}
	r.send(a, up(1, switchGain(t, 0, 0), world.Gain{Off: offOR, Val: 1}))
	if img := r.store.Image(); img[switchGain(t, 0, 0).Off] != 0 || img[offOR] != 1 {
		t.Errorf("world switch %02x, mGetItem %02x", img[switchGain(t, 0, 0).Off], img[offOR])
	}
	if !r.log.has("the host has no co-op stage tables") {
		t.Error("the host's log doesn't say why")
	}
}

// TestTablesNotice: the NOTICE goes once per reason, and not to a player
// that wants none of the table features or to a host that allows none.
func TestTablesNotice(t *testing.T) {
	r := newRig(t, Config{Features: DefaultFeatures, SyncedSwitch: testSynced})
	a := newConn(1, "alice")
	r.attach(a)
	other := func(tables uint64, want Feature) *wire.CoHello {
		m := hello(wire.FileBound, testWorld, 1)
		m.StageTblHash, m.Want = tables, uint32(want)
		return m
	}
	steps := []struct {
		hello  *wire.CoHello
		notice bool
		text   string
	}{
		{other(testTables^1, DefaultFeatures), true, "aren't the host's"},
		{other(testTables^1, DefaultFeatures), false, ""},
		{other(testTables^2, DefaultFeatures), true, "aren't the host's"},
		{other(0, DefaultFeatures), true, "you have no co-op stage tables for your disc (the co-op warning in your log says why and what to do)"},
		{other(0, FeatureImage), false, ""},
		{other(testTables, DefaultFeatures), false, ""},
	}
	for i, s := range steps {
		ms := r.exchange(a, s.hello)
		ns := pick[*wire.Notice](ms)
		if s.notice != (len(ns) == 1) || len(ns) > 1 || s.notice && !containsAll(ns[0].Text, s.text, "memory switches") {
			t.Errorf("step %d: notices %+v, want one: %v (%q)", i, ns, s.notice, s.text)
		}
		if len(ns) == 1 && (strings.HasSuffix(ns[0].Text, "...") || !strings.HasSuffix(ns[0].Text, "the rest do") || len(ns[0].Text) > wire.MaxNoticeLen) {
			t.Errorf("step %d: the notice was clipped: %q", i, ns[0].Text)
		}
		if len(ns) == 1 && (strings.Contains(ns[0].Text, "run patch") || strings.Contains(ns[0].Text, "--coop-iso")) {
			t.Errorf("step %d: the notice gives advice the hub can't know is right: %q", i, ns[0].Text)
		}
		wantF := Feature(s.hello.Want) & DefaultFeatures
		if s.hello.StageTblHash != testTables {
			wantF &^= TableFeatures
		}
		if w := welcomeOf(t, ms); Feature(w.Features) != wantF {
			t.Errorf("step %d: features %s, want %s", i, Feature(w.Features), wantF)
		}
	}
	// A host allowing the image alone says nothing of tables.
	r2 := newRig(t, Config{Features: FeatureImage})
	b := newConn(1, "bob")
	r2.attach(b)
	if ns := pick[*wire.Notice](r2.exchange(b, other(0, DefaultFeatures))); len(ns) != 0 {
		t.Errorf("notices %+v from a host that allows no table feature", ns)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func classField(off int) string { return class.Of(off).Field }
