package network

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- the place trailer ------------------------------------------------------

func TestPlaceTrailer(t *testing.T) {
	for _, p := range []Place{
		{Stage: "sea", Room: 26, InPlay: true},
		{Stage: "M_NewD2", Room: -1},
		{Stage: "Hyroom12", Room: 3, InPlay: true}, // all 8 bytes
	} {
		b := AppendPlace(nil, p)
		if len(b) != PlaceTrailerLen {
			t.Fatalf("%+v: %d bytes", p, len(b))
		}
		got, ok := ParsePlace(append(b, 0xEE, 0xEE)) // a later build's extra bytes
		if !ok || got != p {
			t.Errorf("%+v round trip: %+v %v", p, got, ok)
		}
	}
	if b := AppendPlace(nil, Place{Stage: "toolongname"}); string(b[3:]) != "toolongn" {
		t.Errorf("long stage not cut: %q", b[3:])
	}
	good := AppendPlace(nil, Place{Stage: "sea", InPlay: true})
	bad := map[string][]byte{
		"empty":     nil,
		"short":     good[:PlaceTrailerLen-1],
		"tag":       append([]byte{0x00}, good[1:]...),
		"control":   AppendPlace(nil, Place{Stage: "se\x01"}),
		"after nul": append(append([]byte(nil), good[:7]...), 'x', 0, 0, 0),
	}
	for name, b := range bad {
		if _, ok := ParsePlace(b); ok {
			t.Errorf("%s: parsed % x", name, b)
		}
	}
	// The relay form; a relay from an old server has no place.
	pos := &PlayerPosition{PosX: 1, PosY: 2, PosZ: 3, RotY: 4}
	relay := PositionPlaceMessage(9, pos, Place{Stage: "sea", Room: 26, InPlay: true})
	if id, got := ParsePositionMessage(relay); id != 9 || *got != *pos {
		t.Errorf("an old reader of the relay: id %d pos %+v", id, got)
	}
	if p, ok := ParsePositionPlace(relay); !ok || p.Stage != "sea" || p.Room != 26 || !p.InPlay {
		t.Errorf("place %+v %v", p, ok)
	}
	if _, ok := ParsePositionPlace(PositionMessage(9, pos)); ok {
		t.Error("a relay without the trailer has a place")
	}
}

func TestPoseInScope(t *testing.T) {
	sea := Place{Stage: "sea", Room: 26, InPlay: true}
	sea2 := Place{Stage: "sea", Room: 40, InPlay: true}
	dun := Place{Stage: "M_NewD2", Room: 1, InPlay: true}
	loading := Place{Stage: "M_NewD2", Room: 1}
	cases := []struct {
		from     Place
		fromOK   bool
		to       Place
		toOK     bool
		want     bool
		describe string
	}{
		{sea, true, sea2, true, true, "same stage, other room (the sea's squares)"},
		{sea, true, dun, true, false, "other stage"},
		{sea, false, dun, true, true, "sender's place unknown"},
		{sea, true, dun, false, true, "receiver's place unknown"},
		{sea, true, loading, true, true, "receiver between stages"},
		{loading, true, sea, true, true, "sender between stages"},
	}
	for _, c := range cases {
		if got := poseInScope(c.from, c.fromOK, c.to, c.toOK); got != c.want {
			t.Errorf("%s: %v", c.describe, got)
		}
	}
}

// --- the server -------------------------------------------------------------

// scopeServer is a started server with scoping set and a settable clock.
func scopeServer(t *testing.T, scope bool) (*Server, string, *fakeClock) {
	t.Helper()
	srv := NewServer(0)
	srv.scope = scope
	clk := &fakeClock{t: time.Unix(5000, 0)}
	srv.now = clk.now
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return srv, serverAddr(t, srv), clk
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// human is one player as host/join run it: a broadcast-pose connection
// that sends and a puppet-sync connection that receives, under one name.
type human struct {
	name      string
	bcast, ps *Client
}

func connectHuman(t *testing.T, addr, name string) *human {
	t.Helper()
	h := &human{name: name, bcast: NewClient(name), ps: NewClient(name)}
	for _, c := range []*Client{h.bcast, h.ps} {
		if err := c.Connect(addr); err != nil {
			t.Fatalf("%s connect: %v", name, err)
		}
		t.Cleanup(c.Disconnect)
	}
	return h
}

var testPose = make([]byte, 42*48)

// TestRelayScopeBandwidth measures the pose egress scoping saves. Four
// humans (eight player connections): two in one dungeon (different
// rooms), one on the sea, one in an island interior. Each broadcaster
// sends rounds poses. Unscoped, every pose goes to the other 7
// connections; scoped, only to the puppet-sync connections in the
// sender's stage (its own twin included, which drops it by name as
// before). Positions still reach everyone, with the place.
func TestRelayScopeBandwidth(t *testing.T) {
	fastNetTimers(t)
	const rounds = 30
	places := []Place{
		{Stage: "M_NewD2", Room: 1, InPlay: true},
		{Stage: "M_NewD2", Room: 4, InPlay: true},
		{Stage: "sea", Room: 26, InPlay: true},
		{Stage: "Abesso", Room: 0, InPlay: true},
	}
	run := func(scope bool) (ScopeStats, []*human) {
		srv, addr, _ := scopeServer(t, scope)
		var hs []*human
		for i := range places {
			hs = append(hs, connectHuman(t, addr, fmt.Sprintf("p%d", i)))
		}
		for i, h := range hs {
			if err := h.bcast.SendPositionAt(&PlayerPosition{PosX: float32(i)}, places[i]); err != nil {
				t.Fatal(err)
			}
		}
		// Every puppet-sync hears every broadcaster's position and place.
		if !waitFor(2*time.Second, func() bool {
			for _, h := range hs {
				n := 0
				for _, rp := range h.ps.GetRemotePlayers() {
					if rp.HasPlace {
						n++
					}
				}
				if n != len(hs) {
					return false
				}
			}
			return true
		}) {
			t.Fatal("positions with places did not reach every puppet-sync")
		}
		// A warm-up pose each, so every broadcaster is a known pose
		// sender before the measured rounds.
		for _, h := range hs {
			h.bcast.SendPose(42, testPose, nil)
		}
		warm := len(hs) * (2*len(hs) - 1)
		if !waitFor(2*time.Second, func() bool { st := srv.ScopeStats(); return st.PosesSent+st.PosesScoped == warm || !scope }) {
			t.Fatalf("warm-up: %+v", srv.ScopeStats())
		}
		time.Sleep(50 * time.Millisecond) // posed is set just after the relay
		base := srv.ScopeStats()
		for r := 0; r < rounds; r++ {
			for _, h := range hs {
				if err := h.bcast.SendPose(42, testPose, make([]byte, 19)); err != nil {
					t.Fatal(err)
				}
			}
		}
		want := warm + rounds*warm
		if !waitFor(5*time.Second, func() bool { st := srv.ScopeStats(); return st.PosesSent+st.PosesScoped == want || !scope }) {
			t.Fatalf("relayed %+v, want %d decisions", srv.ScopeStats(), want)
		}
		st := srv.ScopeStats()
		st.PosesSent -= base.PosesSent
		st.PosesScoped -= base.PosesScoped
		st.PoseBytesSent -= base.PoseBytesSent
		st.PoseBytesScoped -= base.PoseBytesScoped
		return st, hs
	}

	st, hs := run(true)
	frame := int64(3 + 1 + 4 + 42*48 + 19)
	total := st.PosesSent + st.PosesScoped
	// Per round: p0 and p1 reach their twins and each other's puppet-sync
	// (2 each); p2 and p3 their twins alone (1 each).
	if st.PosesSent != rounds*6 || total != rounds*4*7 {
		t.Fatalf("scoped: %+v, want %d sent of %d", st, rounds*6, rounds*28)
	}
	if st.PoseBytesSent != int64(st.PosesSent)*frame || st.PoseBytesScoped != int64(st.PosesScoped)*frame {
		// (the warm-up poses carry no face trailer, the measured ones 19 B)
		t.Errorf("byte counts %+v, frame %d", st, frame)
	}
	unscoped := int64(total) * frame
	saved := 100 * float64(st.PoseBytesScoped) / float64(unscoped)
	t.Logf("pose egress for %d rounds of 4 senders: %d B unscoped, %d B scoped (%.0f%% saved); at 60 Hz that is %.0f KB/s -> %.0f KB/s",
		rounds, unscoped, st.PoseBytesSent, saved, float64(unscoped)/rounds*60/1024, float64(st.PoseBytesSent)/rounds*60/1024)
	if saved < 75 {
		t.Errorf("saved only %.0f%%", saved)
	}
	// What each puppet-sync drew from: poses of its own stage only.
	if !waitFor(2*time.Second, func() bool {
		return hasPoseFrom(hs[0].ps, hs[1].bcast.MyID()) && hasPoseFrom(hs[1].ps, hs[0].bcast.MyID())
	}) {
		t.Error("the two dungeon players do not see each other's poses")
	}
	for _, from := range []*human{hs[0], hs[1], hs[3]} {
		if hasPoseFrom(hs[2].ps, from.bcast.MyID()) {
			t.Errorf("the sailor got %s's pose from another stage", from.name)
		}
	}
	// A broadcaster hears a warm-up pose at most (sent before it was
	// a known pose sender), never a measured one (they carry a face
	// trailer).
	for _, h := range hs {
		if n := measuredPosesHeard(h.bcast); n != 0 {
			t.Errorf("%s's broadcaster was relayed %d measured poses", h.name, n)
		}
	}

	// The kill switch: everything to everyone, as before.
	off, _ := run(false)
	if off.PosesSent+off.PosesScoped != 0 {
		t.Errorf("scoping off still counted: %+v", off)
	}
}

func hasPoseFrom(c *Client, id byte) bool {
	for _, rp := range c.GetRemotePlayers() {
		if rp.ID == id && rp.PoseMatrices != nil {
			return true
		}
	}
	return false
}

func measuredPosesHeard(c *Client) int {
	n := 0
	for _, rp := range c.GetRemotePlayers() {
		if len(rp.FaceState) != 0 {
			n++
		}
	}
	return n
}

func hasPlaceFor(c *Client, id byte) bool {
	for _, rp := range c.GetRemotePlayers() {
		if rp.ID == id && rp.HasPlace {
			return true
		}
	}
	return false
}

// TestPositionTrailerRejected: a position with a malformed trailer is
// relayed without a place, and counted.
func TestPositionTrailerRejected(t *testing.T) {
	fastNetTimers(t)
	srv, addr, _ := scopeServer(t, true)
	a := connectHuman(t, addr, "alice")
	b := connectHuman(t, addr, "bob")
	raw := append((&PlayerPosition{PosX: 5}).Serialize(), 0x00, 1, 2, 'x')
	if err := a.bcast.send(MsgPosition, raw); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool {
		for _, rp := range b.ps.GetRemotePlayers() {
			if rp.ID == a.bcast.MyID() && rp.Position != nil {
				return !rp.HasPlace
			}
		}
		return false
	}) {
		t.Fatal("position not relayed, or relayed with a place")
	}
	if srv.ScopeStats().PlacesRejected != 1 {
		t.Errorf("stats %+v", srv.ScopeStats())
	}
}

// --- enemy STATE ------------------------------------------------------------

// scopedState is a STATE in scope for stage/room with count slots.
func scopedState(count int, seq byte, stage string, room byte) []byte {
	b := enemyState(count, EnemyStateFlagInScope)
	b[8] = seq
	copy(b[EnemyStateStageOff:EnemyStateStageOff+EnemyStageLen], stage)
	b[EnemyStateRoomOff] = room
	return b
}

// placedPState is a PSTATE naming stage/room, loading or not.
func placedPState(stage string, room byte, loading bool) []byte {
	b := enemyPState(0)
	copy(b[EnemyPStateStageOff:EnemyPStateStageOff+EnemyStageLen], stage)
	b[EnemyPStateRoomOff] = room
	if loading {
		b[EnemyPStateFlagsOff] = EnemyPSLoading
	}
	return b
}

// TestEnemyStateThinned: the host's STATE goes whole to a client whose
// PSTATE names the STATE's stage and room, and to one with no PSTATE or a
// loading one; to a client in another room it goes as its header alone
// (same seq, count 0, the thin bit), and a keepalive goes whole to all.
// The measured saving is logged. A thin bit the host sets is cleared.
func TestEnemyStateThinned(t *testing.T) {
	fastNetTimers(t)
	srv, addr, clk := scopeServer(t, true)
	host, hostIn := connectEnemy(t, addr, "alice", RoleEnemyHost)
	here, inHere := connectEnemy(t, addr, "bob", RoleEnemyClient)
	away, inAway := connectEnemy(t, addr, "carol", RoleEnemyClient)
	quiet, inQuiet := connectEnemy(t, addr, "dave", RoleEnemyClient)
	load, inLoad := connectEnemy(t, addr, "erin", RoleEnemyClient)
	_ = quiet
	here.SendEnemy(placedPState("M_NewD2", 1, false))
	away.SendEnemy(placedPState("M_NewD2", 4, false))
	load.SendEnemy(placedPState("sea", 26, true))
	if !waitFor(2*time.Second, func() bool { return hostIn.len() == 3 }) {
		t.Fatal("PSTATEs not relayed")
	}

	const n = 60
	for i := 0; i < n; i++ {
		st := scopedState(16, byte(i), "M_NewD2", 1)
		if i == 0 {
			st[EnemyStateFlagsOff] |= EnemyStateFlagThin // a host never sets it
		}
		host.SendEnemy(st)
	}
	all := func(k int) bool {
		return inHere.len() == k && inAway.len() == k && inQuiet.len() == k && inLoad.len() == k
	}
	if !waitFor(3*time.Second, func() bool { return all(n) }) {
		t.Fatalf("STATEs: here %d away %d quiet %d loading %d", inHere.len(), inAway.len(), inQuiet.len(), inLoad.len())
	}
	for name, in := range map[string]*enemyInbox{"here": inHere, "no PSTATE": inQuiet, "loading": inLoad} {
		got := in.snapshot()
		if len(got[0]) != EnemyStateLen(16) || got[0][EnemyStateFlagsOff]&EnemyStateFlagThin != 0 {
			t.Errorf("%s: %d bytes, flags %#x", name, len(got[0]), got[0][EnemyStateFlagsOff])
		}
	}
	thin := inAway.snapshot()
	for i, b := range thin {
		if len(b) != EnemyStateHeaderLen || b[EnemyStateCountOff] != 0 || b[8] != byte(i) ||
			b[EnemyStateFlagsOff] != EnemyStateFlagInScope|EnemyStateFlagThin || string(b[EnemyStateStageOff:EnemyStateStageOff+7]) != "M_NewD2" {
			t.Fatalf("thinned STATE %d: % x", i, b)
		}
	}
	st := srv.ScopeStats()
	if st.StatesFull != 3*n || st.StatesThinned != n {
		t.Errorf("stats %+v", st)
	}
	whole := int64(3 + EnemyStateLen(16))
	t.Logf("enemy STATE egress, 16 slots at 60 Hz: %.1f KB/s per client in another room before, %.1f KB/s thinned",
		float64(whole)*60/1024, float64(3+EnemyStateHeaderLen)*60/1024)

	// A keepalive (not in scope) goes whole to everyone.
	host.SendEnemy(enemyState(0, 0))
	if !waitFor(2*time.Second, func() bool { return all(n + 1) }) {
		t.Fatal("keepalive not relayed to all")
	}
	if b := inAway.snapshot()[n]; b[EnemyStateFlagsOff]&EnemyStateFlagThin != 0 {
		t.Error("a keepalive was marked thin")
	}

	// The away client's PSTATE goes stale: it gets the whole STATE again.
	clk.add(EnemyPStateStale + time.Second)
	host.SendEnemy(scopedState(16, 99, "M_NewD2", 1))
	if !waitFor(2*time.Second, func() bool { return all(n + 2) }) {
		t.Fatal("STATE after the stale PSTATE")
	}
	if b := inAway.snapshot()[n+1]; len(b) != EnemyStateLen(16) {
		t.Errorf("stale PSTATE: got %d bytes, want the whole STATE", len(b))
	}
}

// TestEnemyStateScopeOff: WW_RELAY_SCOPE=off relays every STATE whole.
func TestEnemyStateScopeOff(t *testing.T) {
	fastNetTimers(t)
	t.Setenv("WW_RELAY_SCOPE", "off")
	if relayScopeFromEnv() {
		t.Fatal("WW_RELAY_SCOPE=off read as on")
	}
	t.Setenv("WW_RELAY_SCOPE", "")
	if !relayScopeFromEnv() {
		t.Fatal("unset read as off")
	}
	_, addr, _ := scopeServer(t, false)
	host, hostIn := connectEnemy(t, addr, "alice", RoleEnemyHost)
	away, inAway := connectEnemy(t, addr, "carol", RoleEnemyClient)
	away.SendEnemy(placedPState("M_NewD2", 4, false))
	if !waitFor(2*time.Second, func() bool { return hostIn.len() == 1 }) {
		t.Fatal("PSTATE not relayed")
	}
	host.SendEnemy(scopedState(2, 1, "M_NewD2", 1))
	if !waitFor(2*time.Second, func() bool { return inAway.len() == 1 }) {
		t.Fatal("STATE not relayed")
	}
	if b := inAway.snapshot()[0]; len(b) != EnemyStateLen(2) {
		t.Errorf("got %d bytes with scoping off", len(b))
	}
}

// TestEnemyOriginBinding: two enemy clients under one name. The first,
// live (PSTATEs), holds the name: its HIT and PSTATE carry the player id;
// the second's carry 0. Once the first goes quiet past EnemyPStateStale
// (an unclean drop whose connection lingers), the second takes the name
// over; it never steals it from a live first.
func TestEnemyOriginBinding(t *testing.T) {
	fastNetTimers(t)
	_, addr, clk := scopeServer(t, true)
	player := NewClient("bob")
	if err := player.Connect(addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(player.Disconnect)
	player.SendPositionAt(&PlayerPosition{}, Place{Stage: "sea", InPlay: true})
	_, hostIn := connectEnemy(t, addr, "alice", RoleEnemyHost)
	first, _ := connectEnemy(t, addr, "bob", RoleEnemyClient)
	second, _ := connectEnemy(t, addr, "BOB", RoleEnemyClient)

	got := func(k int) []byte {
		t.Helper()
		if !waitFor(2*time.Second, func() bool { return hostIn.len() >= k }) {
			t.Fatalf("host got %d frames, want %d", hostIn.len(), k)
		}
		return hostIn.snapshot()[k-1]
	}
	// The player position must have registered before the first stamp.
	stamped := false
	for i := 0; i < 50 && !stamped; i++ {
		k := hostIn.len()
		first.SendEnemy(enemyPState(1))
		stamped = got(k + 1)[EnemyPStatePlayerIDOff] == player.MyID()
		if !stamped {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !stamped {
		t.Fatal("the first bob's PSTATE never carried the player id")
	}
	k := hostIn.len()
	second.SendEnemy(enemyPState(2))
	if b := got(k + 1); b[EnemyPStatePlayerIDOff] != 0 || b[EnemyClientIDOff] != second.MyID() {
		t.Errorf("second bob's PSTATE stamped player %d client %d", b[EnemyPStatePlayerIDOff], b[EnemyClientIDOff])
	}
	second.SendEnemy(enemyHit(3))
	if b := got(k + 2); b[EnemyHitPlayerIDOff] != 0 {
		t.Errorf("second bob's HIT stamped player %d", b[EnemyHitPlayerIDOff])
	}
	// The first still holds it while live.
	first.SendEnemy(enemyHit(4))
	if b := got(k + 3); b[EnemyHitPlayerIDOff] != player.MyID() {
		t.Errorf("first bob's HIT stamped player %d", b[EnemyHitPlayerIDOff])
	}
	// The first goes quiet: the second takes the name over. (The player
	// position is refreshed so its place stays believed.)
	clk.add(EnemyPStateStale + time.Second)
	player.SendPositionAt(&PlayerPosition{}, Place{Stage: "sea", InPlay: true})
	time.Sleep(50 * time.Millisecond)
	second.SendEnemy(enemyHit(5))
	if b := got(k + 4); b[EnemyHitPlayerIDOff] != player.MyID() {
		t.Errorf("after the first went stale, the second's HIT stamped player %d", b[EnemyHitPlayerIDOff])
	}
}

// FuzzParsePlace: the place parser never panics, and a place it accepts
// re-encodes to the trailer's first PlaceTrailerLen bytes.
func FuzzParsePlace(f *testing.F) {
	for _, seed := range [][]byte{nil, AppendPlace(nil, Place{Stage: "sea", Room: 26, InPlay: true}), {placeTag}, AppendPlace(nil, Place{Stage: "Hyroom12"})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, ok := ParsePlace(data)
		if !ok {
			return
		}
		if enc := AppendPlace(nil, p); string(enc) != string(data[:PlaceTrailerLen]) {
			t.Fatalf("% x re-encodes as % x", data, enc)
		}
	})
}
