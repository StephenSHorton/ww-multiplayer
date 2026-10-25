package network

import (
	"crypto/sha256"
	"errors"
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
		{"0123456789abcdef", ""},
		{strings.Repeat("k", MaxOwnerKeyLen), ""},
		{"12345678", "at least 16"}, // the old minimum is too short now
		{"0123456789abcde", "at least 16"},
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

// TestGenerateOwnerKey: a generated key is 128 random bits as 32 hex
// digits, passes ValidateOwnerKey, and two are never the same.
func TestGenerateOwnerKey(t *testing.T) {
	a, err := GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateOwnerKey()
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("generated key %q is not 32 hex digits", a)
	}
	if err := ValidateOwnerKey(a); err != nil {
		t.Fatalf("a generated key fails validation: %v", err)
	}
	if a == b {
		t.Fatal("two generated keys are the same")
	}
}

// TestOwnerKeyDelay: the throttle doubles from ownerKeyDelayBase and stops
// at ownerKeyDelayMax, which stays under the client's HELLO deadline.
func TestOwnerKeyDelay(t *testing.T) {
	for _, c := range []struct {
		n    int
		want time.Duration
	}{
		{0, 0}, {1, 250 * time.Millisecond}, {2, 500 * time.Millisecond}, {3, time.Second},
		{6, 8 * time.Second}, {7, 8 * time.Second}, {1000, 8 * time.Second},
	} {
		if got := ownerKeyDelay(c.n); got != c.want {
			t.Errorf("ownerKeyDelay(%d) = %v, want %v", c.n, got, c.want)
		}
	}
	if ownerKeyDelayMax >= 15*time.Second {
		t.Fatalf("ownerKeyDelayMax %v reaches the client's 15 s HELLO deadline", ownerKeyDelayMax)
	}
}

// TestOwnerKeyLimiter: wrong keys throttle per address, not per
// connection, and never lock the right key out: after any number of
// wrong keys from an address, the right key from it is still accepted
// once its (capped) wait has passed, and clears the address. Another
// address is not slowed, and wrong keys further apart than
// ownerKeyForget start over.
func TestOwnerKeyLimiter(t *testing.T) {
	want := testKeyHash()
	a, b := netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("198.51.100.2")
	t0 := time.Unix(1000, 0)
	l := newOwnerKeyLimiter()

	if v := judgeOwnerKey(want, ""); v != keyNone {
		t.Fatalf("no key: %v", v)
	}
	if v := judgeOwnerKey(nil, "guess"); v != keyUnset {
		t.Fatalf("no server key: %v", v)
	}
	l.record(a, keyUnset, t0)
	l.record(a, keyNone, t0)
	if len(l.fails) != 0 {
		t.Fatalf("a key with nothing to guess was counted: %d", len(l.fails))
	}
	// 50 wrong keys, far past the old lockout's five.
	now := t0
	for i := 1; i <= 50; i++ {
		w := l.wait(a, now)
		if w > ownerKeyDelayMax {
			t.Fatalf("wrong key %d waited %v, over the cap", i, w)
		}
		now = now.Add(w)
		if v := judgeOwnerKey(want, fmt.Sprintf("guess%d", i)); v != keyWrong {
			t.Fatalf("wrong key %d: %v", i, v)
		}
		l.record(a, keyWrong, now)
	}
	if w := l.wait(a, now); w != ownerKeyDelayMax {
		t.Fatalf("after 50 wrong keys the wait is %v, want the cap %v", w, ownerKeyDelayMax)
	}
	if w := l.wait(b, now); w != 0 {
		t.Fatalf("another address waits %v", w)
	}
	// The right key from the throttled address: it waits, then is
	// accepted (judging never looks at the count), and clears it.
	if v := judgeOwnerKey(want, testOwnerKey); v != keyAccepted {
		t.Fatalf("the right key from a throttled address: %v", v)
	}
	l.record(a, keyAccepted, now.Add(ownerKeyDelayMax))
	if len(l.fails) != 0 || l.wait(a, now) != 0 {
		t.Fatalf("the right key left %d entries", len(l.fails))
	}
	// The wait runs from the last wrong key: one that long ago costs
	// nothing now.
	l.record(a, keyWrong, now)
	if w := l.wait(a, now.Add(time.Second)); w != 0 {
		t.Fatalf("a wrong key a second ago (delay %v) still waits %v", ownerKeyDelay(1), w)
	}
	// Wrong keys further apart than ownerKeyForget start over each time.
	for i := 0; i < 10; i++ {
		now = now.Add(ownerKeyForget)
		l.record(a, keyWrong, now)
		if f := l.fails[ownerKeyPrefix(a)]; f == nil || f.n != 1 {
			t.Fatalf("slow guess %d: %+v", i, f)
		}
	}
	// One /64 is one bucket.
	l2 := newOwnerKeyLimiter()
	l2.record(netip.MustParseAddr("2001:db8:1:2::1"), keyWrong, t0)
	if w := l2.wait(netip.MustParseAddr("2001:db8:1:2:ffff::9"), t0); w != ownerKeyDelay(1) {
		t.Fatalf("same /64 waits %v", w)
	}
	if w := l2.wait(netip.MustParseAddr("2001:db8:1:3::1"), t0); w != 0 {
		t.Fatalf("another /64 waits %v", w)
	}
}

// TestOwnerKeyLimiterBounded: the limiter remembers at most
// ownerKeyMaxAddrs addresses; past it the forgotten ones go first, then
// the ones with the fewest wrong keys, so a spray of fresh addresses
// (an IPv6 /48 rotating /64s) never pushes out an active throttle.
func TestOwnerKeyLimiterBounded(t *testing.T) {
	l := newOwnerKeyLimiter()
	t0 := time.Unix(1000, 0)
	addr := func(i int) netip.Addr { return netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}) }
	hot := netip.MustParseAddr("203.0.113.7")
	for i := 0; i < 6; i++ {
		l.record(hot, keyWrong, t0) // the oldest entry, and the hottest
	}
	for i := 0; i < 3*ownerKeyMaxAddrs; i++ {
		l.record(addr(i), keyWrong, t0.Add(time.Duration(i+1)*time.Millisecond))
		if len(l.fails) > ownerKeyMaxAddrs {
			t.Fatalf("over the cap: %d entries", len(l.fails))
		}
	}
	if f := l.fails[ownerKeyPrefix(hot)]; f == nil || f.n != 6 {
		t.Fatalf("the active throttle was evicted by a spray: %+v", f)
	}
	if w := l.wait(hot, t0.Add(4*time.Second)); w == 0 {
		t.Fatal("the hot address is no longer slowed")
	}
	// Past ownerKeyForget every old entry is forgotten: one sweep drops
	// them all.
	l.record(addr(9_000_001), keyWrong, t0.Add(time.Hour))
	l.record(addr(9_000_002), keyWrong, t0.Add(2*time.Hour))
	if len(l.fails) > 3 {
		t.Fatalf("forgotten entries kept: %d", len(l.fails))
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

// TestLobbyOwnerKeyPerAddress: wrong keys slow that address across
// connections (each wrong-key HELLO is a new connection and member), and
// the right key from the same address, after many wrong ones, still
// makes its member host: someone sharing the owner's address (a NAT, a
// Docker gateway, a local proxy) can't lock the owner out.
func TestLobbyOwnerKeyPerAddress(t *testing.T) {
	srv, addr := listenLobby(t, 2*time.Second)
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	var waits []time.Duration
	srv.lobby.keyWait = func(d time.Duration, _ <-chan struct{}) bool {
		<-mu
		waits = append(waits, d)
		mu <- struct{}{}
		return true // recorded, not slept: the test stays fast
	}
	const guesses = 8 // past the old lockout's five
	for i := 0; i < guesses; i++ {
		l := NewLobbyClient(fmt.Sprintf("G%d", i), LobbyHelloBody{OwnerKey: "wrong key, wrong key"})
		w := watchLobby(l)
		if err := l.Connect(t.Context(), addr); err != nil {
			t.Fatal(err)
		}
		if !waitFor(5*time.Second, func() bool { n, _, _, _, _ := w.lens(); return n > 0 }) {
			t.Fatalf("guess %d: no notice", i)
		}
		if n := w.lastNotice(); n.Code != NoticeOwnerKey {
			t.Fatalf("guess %d notice %+v", i, n)
		}
		l.Disconnect()
	}
	l := NewLobbyClient("Owner", LobbyHelloBody{OwnerKey: testOwnerKey})
	if err := l.Connect(t.Context(), addr); err != nil {
		t.Fatal(err)
	}
	defer l.Disconnect()
	if !waitFor(5*time.Second, func() bool { st, ok := l.State(); r, _ := rowOf(st, "Owner"); return ok && r.Host }) {
		t.Fatal("the right key from an address with many wrong keys did not make a host")
	}
	<-mu
	got := append([]time.Duration(nil), waits...)
	mu <- struct{}{}
	// Guesses 2..8 and the owner's key were slowed (the first guess had
	// no wrong key before it); none past the cap.
	if len(got) != guesses {
		t.Fatalf("%d slowed HELLOs, want %d: %v", len(got), guesses, got)
	}
	for _, d := range got {
		if d <= 0 || d > ownerKeyDelayMax {
			t.Fatalf("a wait of %v (cap %v): %v", d, ownerKeyDelayMax, got)
		}
	}
	srv.lobby.mu.Lock()
	n := len(srv.lobby.keyFails.fails)
	srv.lobby.mu.Unlock()
	if n != 0 {
		t.Fatalf("the accepted key left %d throttles", n)
	}
}

// TestLobbyOwnerKeyRefusedHelloNotCounted: a HELLO the lobby refuses
// (here name_taken, as the app's suggested-name retry meets it) is not
// counted, so a mistyped key costs one count per admitted join, not one
// per refused try.
func TestLobbyOwnerKeyRefusedHelloNotCounted(t *testing.T) {
	srv, addr := listenLobby(t, 2*time.Second)
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}
	first := NewLobbyClient("Taken", LobbyHelloBody{})
	runLobby(t, first, addr)
	waitState(t, first, "taken", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Taken"); return ok })

	dup := NewLobbyClient("Taken", LobbyHelloBody{OwnerKey: "a mistyped owner key"})
	err := dup.Connect(t.Context(), addr)
	var ne *LobbyNoticeError
	if !errors.As(err, &ne) || ne.Notice.Code != NoticeNameTaken {
		t.Fatalf("duplicate name: %v, want name_taken", err)
	}
	srv.lobby.mu.Lock()
	n := len(srv.lobby.keyFails.fails)
	srv.lobby.mu.Unlock()
	if n != 0 {
		t.Fatalf("a refused HELLO's key was counted (%d entries)", n)
	}
	// The retry under the suggested name is admitted and counts once.
	retry := NewLobbyClient(ne.Notice.Suggest, LobbyHelloBody{OwnerKey: "a mistyped owner key"})
	if err := retry.Connect(t.Context(), addr); err != nil {
		t.Fatal(err)
	}
	defer retry.Disconnect()
	srv.lobby.mu.Lock()
	var count int
	for _, f := range srv.lobby.keyFails.fails {
		count += f.n
	}
	srv.lobby.mu.Unlock()
	if count != 1 {
		t.Fatalf("one mistyped key over a refusal and a retry counted %d times, want 1", count)
	}
}

// TestLobbyOwnerKeyRealWait: the throttle's real sleep (sleepOrDone, not a
// stub). With wrong keys already counted from loopback, the right key
// from loopback still makes a host, only after the wait; and during the
// wait the hub's lock is free: another member joins and gets its STATE
// while the keyed HELLO sleeps.
func TestLobbyOwnerKeyRealWait(t *testing.T) {
	srv, addr := listenLobby(t, 2*time.Second)
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}
	const wrong = 3 // ownerKeyDelay(3) = 1 s, under the tests' 2 s read timeouts
	start := time.Now()
	srv.lobby.mu.Lock()
	for i := 0; i < wrong; i++ {
		srv.lobby.keyFails.record(netip.MustParseAddr("127.0.0.1"), keyWrong, srv.lobby.now())
	}
	srv.lobby.mu.Unlock()

	owner := NewLobbyClient("Owner", LobbyHelloBody{OwnerKey: testOwnerKey})
	connected := make(chan error, 1)
	go func() { connected <- owner.Connect(t.Context(), addr) }()
	defer owner.Disconnect()

	// While Owner sleeps, a plain member (no key: no wait) joins at once.
	time.Sleep(100 * time.Millisecond)
	plain := NewLobbyClient("Plain", LobbyHelloBody{})
	runLobby(t, plain, addr)
	waitState(t, plain, "plain joined", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Plain"); return ok })
	if el := time.Since(start); el >= ownerKeyDelay(wrong) {
		t.Fatalf("a member with no key waited %v behind a throttled HELLO (the hub lock held across the sleep?)", el)
	}

	if err := <-connected; err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < ownerKeyDelay(wrong) {
		t.Fatalf("the keyed HELLO was judged after %v, before its %v wait", el, ownerKeyDelay(wrong))
	}
	waitState(t, plain, "owner is host", func(st LobbyStateBody) bool { r, ok := rowOf(st, "Owner"); return ok && r.Host })
}

// TestSleepOrDone: the wait ends early (false) when the connection closes.
func TestSleepOrDone(t *testing.T) {
	if !sleepOrDone(time.Millisecond, make(chan struct{})) {
		t.Fatal("a finished wait reported closed")
	}
	done := make(chan struct{})
	close(done)
	t0 := time.Now()
	if sleepOrDone(time.Hour, done) || time.Since(t0) > time.Second {
		t.Fatal("a closed connection didn't end the wait")
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
