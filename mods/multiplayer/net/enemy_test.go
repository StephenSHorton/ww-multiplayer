package network

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- helpers ----------------------------------------------------------------

// enemyInbox collects an enemy client's OnEnemy payloads.
type enemyInbox struct {
	mu  sync.Mutex
	got [][]byte
}

func (b *enemyInbox) fn() func([]byte) {
	return func(p []byte) {
		b.mu.Lock()
		b.got = append(b.got, append([]byte(nil), p...))
		b.mu.Unlock()
	}
}

func (b *enemyInbox) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.got)
}

func (b *enemyInbox) snapshot() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), b.got...)
}

// enemyState is a well-shaped STATE with count zero slots.
func enemyState(count int, tag byte) []byte {
	b := make([]byte, EnemyStateLen(count))
	b[0], b[1], b[2], b[3] = EnemySubState, 2, tag, byte(count)
	return b
}

// enemyHit is a well-shaped HIT; the client-ID byte is left 0, as a
// client sends it.
func enemyHit(tag byte) []byte {
	b := make([]byte, EnemyHitLen)
	b[0], b[1], b[3] = EnemySubHit, 2, tag
	return b
}

// enemyAck is a well-shaped ACK naming enemy client id.
func enemyAck(id, tag byte) []byte {
	b := make([]byte, EnemyAckLen)
	b[0], b[1], b[EnemyClientIDOff], b[3] = EnemySubAck, 2, id, tag
	return b
}

// connectEnemy connects an enemy client of role to addr with an inbox.
func connectEnemy(t *testing.T, addr, name string, role byte) (*Client, *enemyInbox) {
	t.Helper()
	c := NewEnemyClient(name, role)
	var in enemyInbox
	c.OnEnemy = in.fn()
	if err := c.Connect(addr); err != nil {
		t.Fatalf("%s connect: %v", name, err)
	}
	t.Cleanup(c.Disconnect)
	return c, &in
}

// --- shape ------------------------------------------------------------------

func TestEnemyFrameShape(t *testing.T) {
	cases := []struct {
		name string
		role byte
		data []byte
		ok   bool
	}{
		{"state 0 slots", RoleEnemyHost, enemyState(0, 0), true},
		{"state 4 slots", RoleEnemyHost, enemyState(4, 0), true},
		{"state 5 slots", RoleEnemyHost, append(enemyState(4, 0), make([]byte, EnemySlotLen)...), false},
		{"state wrong length", RoleEnemyHost, enemyState(2, 0)[:EnemyStateLen(2)-1], false},
		{"state count lies", RoleEnemyHost, func() []byte { b := enemyState(1, 0); b[3] = 2; return b }(), false},
		{"state from client", RoleEnemyClient, enemyState(0, 0), false},
		{"hit", RoleEnemyClient, enemyHit(0), true},
		{"hit short", RoleEnemyClient, enemyHit(0)[:EnemyHitLen-1], false},
		{"hit from host", RoleEnemyHost, enemyHit(0), false},
		{"ack", RoleEnemyHost, enemyAck(1, 0), true},
		{"ack long", RoleEnemyHost, append(enemyAck(1, 0), 0), false},
		{"ack from client", RoleEnemyClient, enemyAck(1, 0), false},
		// Wire version 1's sizes (36-byte slots, a 44-byte HIT) no longer
		// pass; the ACK kept its 16 bytes.
		{"state v1 slots", RoleEnemyHost, enemyState(2, 0)[:EnemyStateHeaderLen+2*36], false},
		{"state one v1 slot", RoleEnemyHost, enemyState(1, 0)[:EnemyStateHeaderLen+36], false},
		{"state four v1 slots", RoleEnemyHost, enemyState(4, 0)[:EnemyStateLen(3)], false},
		{"hit v1", RoleEnemyClient, enemyHit(0)[:44], false},
		{"ack short", RoleEnemyHost, enemyAck(1, 0)[:EnemyAckLen-1], false},
		{"empty", RoleEnemyHost, nil, false},
		{"one byte", RoleEnemyHost, []byte{EnemySubState}, false},
		{"unknown sub", RoleEnemyHost, []byte{9, 1, 0, 0}, false},
	}
	for _, c := range cases {
		if why := enemyFrameShape(c.role, c.data); (why == "") != c.ok {
			t.Errorf("%s: enemyFrameShape = %q, want ok=%v", c.name, why, c.ok)
		}
	}
	if EnemySlotLen != 48 || EnemyStateLen(0) != 32 || EnemyStateLen(4) != 32+4*48 || EnemyHitLen != 48 || EnemyAckLen != 16 {
		t.Errorf("sizes: slot=%d state(0)=%d state(4)=%d hit=%d ack=%d", EnemySlotLen, EnemyStateLen(0), EnemyStateLen(4), EnemyHitLen, EnemyAckLen)
	}
}

// --- relay by role --------------------------------------------------------------

// TestEnemyRelay: a STATE from the host reaches every enemy client and
// nothing else; a HIT from a client reaches the host alone, stamped with
// the client's ID; an ACK from the host reaches the client it names; a
// player connection and a co-op connection see none of it.
func TestEnemyRelay(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	srv, addr := listenCoop(t, rec)

	player := NewClient("alice")
	if err := player.Connect(addr); err != nil {
		t.Fatal(err)
	}
	defer player.Disconnect()
	var playerIn enemyInbox
	player.OnEnemy = playerIn.fn()

	host, hostIn := connectEnemy(t, addr, "alice", RoleEnemyHost)
	ca, inA := connectEnemy(t, addr, "bob", RoleEnemyClient)
	cb, inB := connectEnemy(t, addr, "carol", RoleEnemyClient)
	if !srv.EnemyHostConnected() || srv.EnemyClientCount() != 2 || srv.PlayerCount() != 1 {
		t.Fatalf("host %v clients %d players %d", srv.EnemyHostConnected(), srv.EnemyClientCount(), srv.PlayerCount())
	}
	if host.MyID() == ca.MyID() || host.MyID() == cb.MyID() || ca.MyID() == cb.MyID() {
		t.Fatalf("enemy IDs collide: host %d a %d b %d", host.MyID(), ca.MyID(), cb.MyID())
	}

	// STATE host -> both clients.
	if err := host.SendEnemy(enemyState(2, 7)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return inA.len() == 1 && inB.len() == 1 }) {
		t.Fatalf("STATE not relayed: a %d b %d", inA.len(), inB.len())
	}
	if got := inA.snapshot()[0]; got[0] != EnemySubState || got[2] != 7 || len(got) != EnemyStateLen(2) {
		t.Errorf("client a got %x", got)
	}

	// HIT client a -> host, stamped with a's ID.
	if err := ca.SendEnemy(enemyHit(9)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return hostIn.len() == 1 }) {
		t.Fatal("HIT not relayed to the host")
	}
	hit := hostIn.snapshot()[0]
	if hit[0] != EnemySubHit || hit[EnemyClientIDOff] != ca.MyID() || hit[3] != 9 {
		t.Errorf("host got HIT %x, want client ID %d stamped at byte %d", hit, ca.MyID(), EnemyClientIDOff)
	}

	// ACK host -> client a only.
	if err := host.SendEnemy(enemyAck(ca.MyID(), 5)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return inA.len() == 2 }) {
		t.Fatal("ACK not relayed to client a")
	}
	if got := inA.snapshot()[1]; got[0] != EnemySubAck || got[3] != 5 {
		t.Errorf("client a got %x", got)
	}
	// An ACK naming nobody goes nowhere; a HIT from the host is dropped.
	host.SendEnemy(enemyAck(200, 1))
	host.SendEnemy(enemyHit(1))
	cb.SendEnemy(enemyState(0, 1))
	host.SendEnemy(enemyState(0, 3)) // a keepalive STATE, to show order held
	if !waitFor(2*time.Second, func() bool { return inA.len() == 3 && inB.len() == 2 }) {
		t.Fatalf("keepalive STATE not relayed: a %d b %d", inA.len(), inB.len())
	}
	time.Sleep(100 * time.Millisecond)
	if inA.len() != 3 || inB.len() != 2 || hostIn.len() != 1 || playerIn.len() != 0 {
		t.Errorf("stray relays: a %d b %d host %d player %d", inA.len(), inB.len(), hostIn.len(), playerIn.len())
	}
	for _, e := range rec.snapshot() {
		if strings.HasPrefix(e, "frame") {
			t.Errorf("co-op handler got %q", e)
		}
	}
	if srv.PlayerCount() != 1 {
		t.Errorf("PlayerCount = %d, want 1", srv.PlayerCount())
	}

	// A client leaving frees its ID; the host leaving frees the host slot.
	ca.Disconnect()
	if !waitFor(2*time.Second, func() bool { return srv.EnemyClientCount() == 1 }) {
		t.Fatal("client a not reaped")
	}
	host.Disconnect()
	if !waitFor(2*time.Second, func() bool { return !srv.EnemyHostConnected() }) {
		t.Fatal("host not reaped")
	}
	// With no host, a HIT goes nowhere and the client stays connected.
	if err := cb.SendEnemy(enemyHit(2)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !cb.IsConnected() {
		t.Error("client b dropped after a HIT with no host")
	}
}

// TestEnemySecondHostRefused: one enemy host per server; the second gets a
// fatal RejectRole, and after the first leaves a new one is taken.
func TestEnemySecondHostRefused(t *testing.T) {
	fastNetTimers(t)
	srv, addr := mustListen(t)
	defer srv.Stop()

	first := NewEnemyClient("alice", RoleEnemyHost)
	if err := first.Connect(addr); err != nil {
		t.Fatal(err)
	}
	defer first.Disconnect()

	err := NewEnemyClient("bob", RoleEnemyHost).Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectRole || !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "already has an enemy-sync host") {
		t.Fatalf("second host: Connect = %v, want a fatal RejectRole", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessions := 0
	err = RunEnemy(ctx, NewEnemyClient("bob", RoleEnemyHost), addr, nil, func(context.Context, func([]byte) error) error { sessions++; return nil })
	if !errors.As(err, &rej) || rej.Code != RejectRole || ctx.Err() != nil || sessions != 0 {
		t.Fatalf("RunEnemy = %v (ctx %v, sessions %d), want the RejectRole error before the timeout", err, ctx.Err(), sessions)
	}

	first.Disconnect()
	if !waitFor(2*time.Second, func() bool { return !srv.EnemyHostConnected() }) {
		t.Fatal("first host not reaped")
	}
	second := NewEnemyClient("bob", RoleEnemyHost)
	if err := second.Connect(addr); err != nil {
		t.Fatalf("host after the first left: %v", err)
	}
	second.Disconnect()
}

// TestEnemyFramesDroppedOnPlayerConnection: an 'E' on a player connection
// is dropped (logged once) and the player keeps its session; an 'E' of the
// wrong shape on an enemy connection is dropped the same way.
func TestEnemyFramesDroppedOnPlayerConnection(t *testing.T) {
	fastNetTimers(t)
	var logs logRecorder
	srv, addr := listenCoopLog(t, nil, logs.fn())

	_, in := connectEnemy(t, addr, "watcher", RoleEnemyClient)
	var chats chatRecorder
	watcher := NewClient("watcher")
	watcher.OnChat = chats.fn()
	if err := watcher.Connect(addr); err != nil {
		t.Fatal(err)
	}
	defer watcher.Disconnect()

	p := dialRaw(t, addr)
	if err := writeJoin(p, "mallory"); err != nil {
		t.Fatal(err)
	}
	mustRead(t, p) // welcome
	go drain(p)
	for i := 0; i < 3; i++ {
		p.Write(frame(MsgEnemy, enemyState(0, byte(i))))
	}
	p.Write(frame(MsgChat, []byte("still here")))
	if !waitFor(2*time.Second, func() bool { return chats.len() == 1 }) {
		t.Fatal("the player's chat after its 'E' frames never arrived")
	}
	if n := strings.Count(logs.joined(), "dropping enemy-sync frames from mallory"); n != 1 {
		t.Errorf("logged the player drop %d times, want once", n)
	}

	// A malformed 'E' from an enemy host: dropped, logged once, the
	// connection lives on and a good frame after it is relayed.
	h := dialRaw(t, addr)
	if err := writeJoinRole(h, "host", RoleEnemyHost, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	mustRead(t, h) // welcome
	go drain(h)
	h.Write(frame(MsgEnemy, enemyState(3, 0)[:10]))
	h.Write(frame(MsgEnemy, enemyHit(0)))
	h.Write(frame(MsgEnemy, enemyState(1, 42)))
	if !waitFor(2*time.Second, func() bool { return in.len() == 1 }) {
		t.Fatal("the good STATE after the bad frames never arrived")
	}
	if got := in.snapshot()[0]; got[2] != 42 {
		t.Errorf("relayed %x", got)
	}
	if n := strings.Count(logs.joined(), "dropping bad enemy-sync frames from host"); n != 1 {
		t.Errorf("logged the host drop %d times, want once:\n%s", n, logs.joined())
	}
	if srv.PlayerCount() != 2 || srv.EnemyClientCount() != 1 || !srv.EnemyHostConnected() {
		t.Errorf("players %d clients %d host %v", srv.PlayerCount(), srv.EnemyClientCount(), srv.EnemyHostConnected())
	}
}

// TestPlayerClientIgnoresEnemy: a player client never hands an 'E' frame to
// OnEnemy and refuses to send one; RunEnemy refuses a player client.
func TestPlayerClientIgnoresEnemy(t *testing.T) {
	c := NewClient("me")
	called := false
	c.OnEnemy = func([]byte) { called = true }
	sc := newScriptConn(frame(MsgEnemy, enemyState(0, 0)))
	c.conn = sc
	c.connected.Store(true)
	c.readLoop(sc)
	if called {
		t.Error("a player client passed an 'E' frame to OnEnemy")
	}
	if err := c.SendEnemy([]byte{1}); err == nil {
		t.Error("SendEnemy on a player client succeeded")
	}
	if err := RunEnemy(context.Background(), c, "127.0.0.1:1", nil, nil); err == nil {
		t.Error("RunEnemy accepted a player client")
	}
}

// TestEnemyKeepalive: a quiet enemy client outlives the server's read
// deadline (the server pings, the client pongs), and a quiet host too.
func TestEnemyKeepalive(t *testing.T) {
	saved := saveNetTimers()
	t.Cleanup(func() { restoreNetTimers(saved) })
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 300 * time.Millisecond
	clientReadTimeout = 300 * time.Millisecond
	srv, addr := mustListen(t)
	defer srv.Stop()

	host, _ := connectEnemy(t, addr, "h", RoleEnemyHost)
	client, in := connectEnemy(t, addr, "c", RoleEnemyClient)
	time.Sleep(4 * serverReadTimeout)
	if !host.IsConnected() || !client.IsConnected() || !srv.EnemyHostConnected() || srv.EnemyClientCount() != 1 {
		t.Fatalf("a quiet enemy connection was reaped: host %v client %v srv host %v clients %d",
			host.IsConnected(), client.IsConnected(), srv.EnemyHostConnected(), srv.EnemyClientCount())
	}
	if err := host.SendEnemy(enemyState(0, 1)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return in.len() == 1 }) {
		t.Fatal("STATE after the quiet period not relayed")
	}
	if host.LastRTT() == 0 || client.LastRTT() == 0 {
		t.Errorf("no RTT measured: host %v client %v", host.LastRTT(), client.LastRTT())
	}
}

// TestEnemyStopClosesConnections: Stop closes the enemy connections with
// the others.
func TestEnemyStopClosesConnections(t *testing.T) {
	fastNetTimers(t)
	srv, addr := mustListen(t)
	host, _ := connectEnemy(t, addr, "h", RoleEnemyHost)
	client, _ := connectEnemy(t, addr, "c", RoleEnemyClient)
	srv.Stop()
	if !waitFor(2*time.Second, func() bool { return !host.IsConnected() && !client.IsConnected() }) {
		t.Fatal("enemy connections survived Stop")
	}
}

// TestRunEnemySession: RunEnemy runs the session once per connection with
// a working send, and ends when ctx does.
func TestRunEnemySession(t *testing.T) {
	fastNetTimers(t)
	srv, addr := mustListen(t)
	defer srv.Stop()
	_, in := connectEnemy(t, addr, "c", RoleEnemyClient)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunEnemy(ctx, NewEnemyClient("h", RoleEnemyHost), addr, nil, func(ctx context.Context, send func([]byte) error) error {
			if err := send(enemyState(0, 77)); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})
	}()
	if !waitFor(2*time.Second, func() bool { return in.len() == 1 }) {
		t.Fatal("the session's STATE never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunEnemy = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunEnemy did not return after cancel")
	}
}
