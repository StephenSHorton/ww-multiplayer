package network

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const testOwnerKey = "correct horse battery staple"

func testKeyHash() *[sha256.Size]byte {
	h := sha256.Sum256([]byte(testOwnerKey))
	return &h
}

func TestOwnerKeyMatches(t *testing.T) {
	want := testKeyHash()
	for _, c := range []struct {
		key string
		ok  bool
	}{
		{testOwnerKey, true},
		{"", false},
		{testOwnerKey + " ", false},
		{strings.ToUpper(testOwnerKey), false},
		{testOwnerKey[:len(testOwnerKey)-1], false},
		{strings.Repeat("k", MaxOwnerKeyLen+1), false},
	} {
		if got := ownerKeyMatches(want, c.key); got != c.ok {
			t.Errorf("ownerKeyMatches(%.20q) = %v, want %v", c.key, got, c.ok)
		}
	}
	if ownerKeyMatches(nil, testOwnerKey) {
		t.Error("a key matched a server with none")
	}
	// A key exactly MaxOwnerKeyLen long is compared.
	long := strings.Repeat("k", MaxOwnerKeyLen)
	h := sha256.Sum256([]byte(long))
	if !ownerKeyMatches(&h, long) {
		t.Error("a MaxOwnerKeyLen key did not match itself")
	}
}

func TestValidateOwnerKey(t *testing.T) {
	for _, c := range []struct {
		key string
		err string
	}{
		{"12345678", ""},
		{strings.Repeat("k", MaxOwnerKeyLen), ""},
		{"1234567", "at least 8"},
		{strings.Repeat("k", MaxOwnerKeyLen+1), "at most 256"},
	} {
		err := ValidateOwnerKey(c.key)
		if (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("ValidateOwnerKey(%d bytes) = %v, want %q", len(c.key), err, c.err)
		}
	}
}

func TestOwnerKeyPrefix(t *testing.T) {
	for _, c := range []struct{ ip, want string }{
		{"203.0.113.7", "203.0.113.7/32"},
		{"::ffff:203.0.113.7", "203.0.113.7/32"}, // v4-in-v6 counts as the v4 address
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"}, // same /64, same bucket
	} {
		if got := ownerKeyPrefix(netip.MustParseAddr(c.ip)); got.String() != c.want {
			t.Errorf("ownerKeyPrefix(%s) = %s, want %s", c.ip, got, c.want)
		}
	}
	if p := ownerKeyPrefix(netip.Addr{}); p.IsValid() {
		t.Errorf("an invalid address gave prefix %s", p)
	}
	if got := remoteIP(&net.TCPAddr{IP: net.ParseIP("::ffff:198.51.100.2"), Port: 1}); got.String() != "198.51.100.2" {
		t.Errorf("remoteIP = %s", got)
	}
}

// TestOwnerKeyLimiter: wrong keys count per address, not per connection;
// the fifth locks the address out, and then even the right key is not
// compared until ownerKeyLockout after the last wrong one. Another
// address is not affected, the right key clears the count, and a run of
// wrong keys spread wider than the lockout never locks.
func TestOwnerKeyLimiter(t *testing.T) {
	want := testKeyHash()
	a, b := netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("198.51.100.2")
	t0 := time.Unix(1000, 0)
	l := newOwnerKeyLimiter()

	if v := l.check(want, a, "", t0); v != keyNone {
		t.Fatalf("no key: %v", v)
	}
	if v := l.check(nil, a, "guess", t0); v != keyUnset || len(l.fails) != 0 {
		t.Fatalf("no server key: %v, %d counted", v, len(l.fails))
	}
	for i := 1; i < ownerKeyMaxFails; i++ {
		if v := l.check(want, a, fmt.Sprintf("guess%d", i), t0.Add(time.Duration(i)*time.Second)); v != keyWrong {
			t.Fatalf("wrong key %d: %v", i, v)
		}
	}
	if v := l.check(want, a, "guess5", t0.Add(5*time.Second)); v != keyLockedNow {
		t.Fatalf("fifth wrong key: %v", v)
	}
	if v := l.check(want, a, testOwnerKey, t0.Add(6*time.Second)); v != keyLocked {
		t.Fatalf("the right key while locked out: %v (must not be compared)", v)
	}
	if v := l.check(want, b, testOwnerKey, t0.Add(6*time.Second)); v != keyAccepted {
		t.Fatalf("another address: %v", v)
	}
	// Locked attempts don't extend the lockout: it runs from the last
	// wrong key (t0+5s).
	if v := l.check(want, a, testOwnerKey, t0.Add(5*time.Second+ownerKeyLockout-time.Second)); v != keyLocked {
		t.Fatalf("just before the lockout ends: %v", v)
	}
	if v := l.check(want, a, testOwnerKey, t0.Add(5*time.Second+ownerKeyLockout)); v != keyAccepted {
		t.Fatalf("after the lockout: %v", v)
	}
	if len(l.fails) != 0 {
		t.Fatalf("the right key left %d entries", len(l.fails))
	}

	// Wrong keys further apart than the lockout start over each time.
	now := t0
	for i := 0; i < 3*ownerKeyMaxFails; i++ {
		now = now.Add(ownerKeyLockout)
		if v := l.check(want, a, "slow guess", now); v != keyWrong {
			t.Fatalf("slow guess %d: %v", i, v)
		}
	}
}

// TestOwnerKeyLimiterBounded: the limiter remembers at most
// ownerKeyMaxAddrs addresses, dropping stale ones first, then the oldest.
func TestOwnerKeyLimiterBounded(t *testing.T) {
	want := testKeyHash()
	l := newOwnerKeyLimiter()
	t0 := time.Unix(1000, 0)
	addr := func(i int) netip.Addr { return netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}) }
	for i := 0; i < ownerKeyMaxAddrs; i++ {
		l.check(want, addr(i), "x", t0.Add(time.Duration(i)*time.Millisecond))
	}
	if len(l.fails) != ownerKeyMaxAddrs {
		t.Fatalf("%d entries", len(l.fails))
	}
	l.check(want, addr(ownerKeyMaxAddrs), "x", t0.Add(5*time.Second)) // none stale yet
	if len(l.fails) != ownerKeyMaxAddrs {
		t.Fatalf("over the cap: %d entries", len(l.fails))
	}
	if _, ok := l.fails[ownerKeyPrefix(addr(0))]; ok {
		t.Fatal("the oldest entry was kept")
	}
	// Past the lockout every old entry is stale: one sweep drops them all.
	l.check(want, addr(ownerKeyMaxAddrs+1), "x", t0.Add(time.Hour))
	l.check(want, addr(ownerKeyMaxAddrs+2), "x", t0.Add(2*time.Hour))
	if len(l.fails) > 3 {
		t.Fatalf("stale entries kept: %d", len(l.fails))
	}
}

// --- the machine -----------------------------------------------------------

// TestLobbyMachineOwnerKey: with the lobby keyed, the first member that
// presented the key is host wherever it is, a loopback member is not, the
// role passes to the next keyed member when the host's slot is gone, and
// a reclaim keeps the owner flag without the key.
func TestLobbyMachineOwnerKey(t *testing.T) {
	m := testMachine(time.Minute)
	m.keyed = true
	t0 := time.Unix(0, 0)
	local := mustJoin(t, m, 1, "Local", true, LobbyHelloBody{})
	if local.host {
		t.Fatal("a loopback member became host on a keyed lobby")
	}
	o1, _ := m.helloAs(2, "Owner", false, true, LobbyHelloBody{}, t0)
	if o1 == nil || !o1.host || !o1.owner {
		t.Fatalf("the keyed member: %+v", o1)
	}
	o2, _ := m.helloAs(3, "Owner2", false, true, LobbyHelloBody{}, t0)
	if o2.host {
		t.Fatal("two hosts")
	}
	// START from the remote keyed host is accepted.
	if outs := m.message(2, LobbyStart, nil); !hasSub(outs, LobbyStart) {
		t.Fatalf("keyed host's START: %+v", outs)
	}

	// The host drops and its slot expires: Owner2 takes over, not Local.
	m.drop(2, t0)
	m.expire(t0.Add(2 * time.Minute))
	if !o2.host || local.host {
		t.Fatalf("after expiry: owner2 %v local %v", o2.host, local.host)
	}

	// Owner2 drops and comes back with its token but no key: still the
	// owner, still host (nobody else could take it meanwhile).
	m.drop(3, t0)
	back, _ := m.helloAs(4, "Owner2", false, false, LobbyHelloBody{Token: o2.token}, t0.Add(time.Second))
	if back != o2 || !back.owner || !back.host {
		t.Fatalf("reclaim: %+v", back)
	}
}

// TestLobbyMachineOwnerKeyReclaimAfterExpiry: a keyed member away while
// the host's slot expired comes back with its token (no key) and takes
// the role.
func TestLobbyMachineOwnerKeyReclaimAfterExpiry(t *testing.T) {
	m := testMachine(time.Minute)
	m.keyed = true
	t0 := time.Unix(0, 0)
	o1, _ := m.helloAs(1, "Owner", false, true, LobbyHelloBody{}, t0)
	o2, _ := m.helloAs(2, "Owner2", false, true, LobbyHelloBody{}, t0)
	m.drop(1, t0)                      // the host away, held until t0+60s
	m.drop(2, t0.Add(30*time.Second))  // Owner2 away, held until t0+90s
	m.expire(t0.Add(61 * time.Second)) // the host's slot goes; nobody live can host
	if m.member(o1.id) != nil || m.hasHost() {
		t.Fatalf("after the host's expiry: host held %v, a host %v", m.member(o1.id) != nil, m.hasHost())
	}
	back, _ := m.helloAs(3, "Owner2", false, false, LobbyHelloBody{Token: o2.token}, t0.Add(70*time.Second))
	if back != o2 || !back.host {
		t.Fatalf("reclaim: %+v", back)
	}
}

// TestLobbyMachineNoKeyUnchanged: without a key the owner flag counts for
// nothing and loopback decides, as before.
func TestLobbyMachineNoKeyUnchanged(t *testing.T) {
	m := testMachine(time.Minute)
	remote, _ := m.helloAs(1, "Remote", false, true, LobbyHelloBody{}, time.Unix(0, 0))
	if remote.host {
		t.Fatal("an owner flag made a remote host on an unkeyed lobby")
	}
	if local := mustJoin(t, m, 2, "Local", true, LobbyHelloBody{}); !local.host {
		t.Fatal("the loopback member is not host")
	}
}

func TestLobbyMachineNoCoop(t *testing.T) {
	m := testMachine(time.Minute)
	m.keyed = true
	m.noCoop = "dedicated"
	m.helloAs(1, "Owner", false, true, LobbyHelloBody{}, time.Unix(0, 0))
	outs := m.message(1, LobbySettings, []byte(`{"coop":"new"}`))
	if len(outs) != 1 || outs[0].body.(LobbyNoticeBody).Code != NoticeNoCoop {
		t.Fatalf("co-op on a dedicated server: %+v", outs)
	}
	if outs := m.message(1, LobbySettings, []byte(`{"coop":"off","merge":true}`)); !hasSub(outs, LobbyState) {
		t.Fatalf("co-op off: %+v", outs)
	}
}

// --- the hub, end to end ---------------------------------------------------

// TestLobbyOwnerKeyLoopback: a keyed server over real connections, every
// client on loopback (which no longer counts). The key holder is host and
// can start; a wrong key and no key join as players, the wrong one with
// the owner_key notice; a reconnect keeps the host role without the key
// being checked again; the HELLO that carried the key is the only place
// it went (not STATE, not the log).
func TestLobbyOwnerKeyLoopback(t *testing.T) {
	srv, addr := listenLobby(t, 2*time.Second)
	srv.lobby.local = func(net.Addr, string) bool { return true }
	var logs []string
	var logMu = make(chan struct{}, 1)
	logMu <- struct{}{}
	srv.OnLog = func(s string) { <-logMu; logs = append(logs, s); logMu <- struct{}{} }
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}
	srv.DenyCoop("dedicated")

	plain := NewLobbyClient("Plain", LobbyHelloBody{})
	runLobby(t, plain, addr)
	waitState(t, plain, "plain", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Plain"); return ok })

	owner := NewLobbyClient("Owner", LobbyHelloBody{OwnerKey: testOwnerKey})
	ow := watchLobby(owner)
	runLobby(t, owner, addr)
	st := waitState(t, plain, "owner host", func(st LobbyStateBody) bool { r, _ := rowOf(st, "Owner"); return r.Host })
	if r, _ := rowOf(st, "Plain"); r.Host {
		t.Fatal("two hosts")
	}

	guess := NewLobbyClient("Guess", LobbyHelloBody{OwnerKey: "not the key"})
	gw := watchLobby(guess)
	runLobby(t, guess, addr)
	if !waitFor(5*time.Second, func() bool { n, _, _, _, _ := gw.lens(); return n > 0 }) {
		t.Fatal("no notice for a wrong key")
	}
	if n := gw.lastNotice(); n.Code != NoticeOwnerKey {
		t.Fatalf("wrong key notice %+v", n)
	}
	st = waitState(t, plain, "guess", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Guess"); return ok })
	if r, _ := rowOf(st, "Guess"); r.Host {
		t.Fatal("a wrong key made a host")
	}

	// The key holder reconnects: its token gives the role back.
	if !waitFor(5*time.Second, func() bool { _, ok := owner.Welcome(); return ok }) {
		t.Fatal("no WELCOME for the owner")
	}
	owner.Client().Disconnect()
	waitState(t, plain, "owner back", func(st LobbyStateBody) bool { r, _ := rowOf(st, "Owner"); return r.Host && !r.Away })

	// Co-op is refused on this server; START from the remote host works.
	if !waitFor(5*time.Second, func() bool { return owner.SetSettings(LobbySettingsBody{Coop: CoopNew}) == nil }) {
		t.Fatal("the owner's client did not reconnect")
	}
	if !waitFor(5*time.Second, func() bool { n, _, _, _, _ := ow.lens(); return n > 0 && ow.lastNotice().Code == NoticeNoCoop }) {
		t.Fatal("no no_coop notice")
	}
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, plain, "started", func(st LobbyStateBody) bool { return st.Phase == PhaseStarting })

	<-logMu
	defer func() { logMu <- struct{}{} }()
	joined := strings.Join(logs, "\n")
	if strings.Contains(joined, testOwnerKey) || strings.Contains(joined, "not the key") {
		t.Fatalf("a key reached the log:\n%s", joined)
	}
	if !strings.Contains(joined, "Owner (lobby") || !strings.Contains(joined, "owner key accepted") {
		t.Fatalf("no acceptance line:\n%s", joined)
	}
	if strings.Count(joined, "owner key accepted") != 1 {
		t.Fatalf("the reconnect's key was checked again:\n%s", joined)
	}
}

// TestLobbyOwnerKeyPerAddress: the lockout follows the address across
// connections (each wrong-key HELLO is a new connection and member).
func TestLobbyOwnerKeyPerAddress(t *testing.T) {
	srv, addr := listenLobby(t, 2*time.Second)
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < ownerKeyMaxFails; i++ {
		l := NewLobbyClient(fmt.Sprintf("G%d", i), LobbyHelloBody{OwnerKey: "wrong key"})
		w := watchLobby(l)
		if err := l.Connect(t.Context(), addr); err != nil {
			t.Fatal(err)
		}
		if !waitFor(5*time.Second, func() bool { n, _, _, _, _ := w.lens(); return n > 0 }) {
			t.Fatalf("guess %d: no notice", i)
		}
		l.Disconnect()
	}
	l := NewLobbyClient("Late", LobbyHelloBody{OwnerKey: testOwnerKey})
	w := watchLobby(l)
	if err := l.Connect(t.Context(), addr); err != nil {
		t.Fatal(err)
	}
	defer l.Disconnect()
	if !waitFor(5*time.Second, func() bool { n, _, _, _, _ := w.lens(); return n > 0 }) {
		t.Fatal("no notice for the locked-out right key")
	}
	if n := w.lastNotice(); n.Code != NoticeOwnerKey || !strings.Contains(n.Text, "too many") {
		t.Fatalf("locked-out notice %+v", n)
	}
	if wl, _ := l.Welcome(); wl.Host {
		t.Fatal("a locked-out address got the host role")
	}
}

func TestSetOwnerKeyRefusals(t *testing.T) {
	srv := NewServer(0)
	if err := srv.SetOwnerKey("short"); err == nil {
		t.Fatal("a short key was taken")
	}
	if err := srv.SetOwnerKey(testOwnerKey); err != nil || !srv.HasOwnerKey() {
		t.Fatalf("SetOwnerKey: %v", err)
	}
	if err := srv.SetOwnerKey(""); err != nil || srv.HasOwnerKey() {
		t.Fatalf("clearing: %v", err)
	}
	srv.lobby.m.members = append(srv.lobby.m.members, &lobbyMember{id: 1})
	if err := srv.SetOwnerKey(testOwnerKey); err != errOwnerKeyAfterStart {
		t.Fatalf("with members: %v", err)
	}
}

// TestLobbyHelloOwnerKeyWire: ownerKey is omitted when empty (the golden
// bytes don't move) and an old decoder ignores it.
func TestLobbyHelloOwnerKeyWire(t *testing.T) {
	b, err := EncodeLobby(LobbyHello, LobbyHelloBody{App: "x"})
	if err != nil || strings.Contains(string(b), "ownerKey") {
		t.Fatalf("empty ownerKey on the wire: %q %v", b, err)
	}
	b, _ = EncodeLobby(LobbyHello, LobbyHelloBody{App: "x", OwnerKey: testOwnerKey})
	var old struct {
		App string `json:"app"`
	}
	if err := DecodeLobby(b[1:], &old); err != nil || old.App != "x" {
		t.Fatalf("an old decoder: %+v %v", old, err)
	}
	var back LobbyHelloBody
	if err := DecodeLobby(b[1:], &back); err != nil || back.OwnerKey != testOwnerKey {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}
