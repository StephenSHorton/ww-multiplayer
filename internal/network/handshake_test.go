package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers ----------------------------------------------------------------

// scriptConn is a net.Conn that reads a fixed byte stream (EOF at the end)
// and discards writes, so a read loop can run synchronously over crafted
// frames.
type scriptConn struct {
	r      *bytes.Reader
	closed atomic.Bool
}

func newScriptConn(stream []byte) *scriptConn { return &scriptConn{r: bytes.NewReader(stream)} }

func (s *scriptConn) Read(b []byte) (int, error)         { return s.r.Read(b) }
func (s *scriptConn) Write(b []byte) (int, error)        { return len(b), nil }
func (s *scriptConn) Close() error                       { s.closed.Store(true); return nil }
func (s *scriptConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (s *scriptConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (s *scriptConn) SetDeadline(t time.Time) error      { return nil }
func (s *scriptConn) SetReadDeadline(t time.Time) error  { return nil }
func (s *scriptConn) SetWriteDeadline(t time.Time) error { return nil }

// countingConn is an okConn that counts Write calls.
type countingConn struct {
	okConn
	writes atomic.Int64
}

func (c *countingConn) Write(b []byte) (int, error) { c.writes.Add(1); return len(b), nil }

// v021Join is the exact join a v0.2.1 client sends: one MsgJoin frame with
// the bare name, and nothing more until it has its welcome.
func v021Join(name string) []byte {
	return append([]byte{MsgJoin, byte(len(name) >> 8), byte(len(name))}, name...)
}

// rawJoin writes a join and a hello carrying version, bypassing writeJoin's
// client-side name check (so tests can send names the server must refuse).
func rawJoin(t *testing.T, conn net.Conn, name string, version uint16) {
	t.Helper()
	if _, err := conn.Write(append(frame(MsgJoin, []byte(name)), frame(MsgHello, HelloMessage(version))...)); err != nil {
		t.Fatalf("write join+hello: %v", err)
	}
}

func dialRaw(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	return conn
}

func mustRead(t *testing.T, conn net.Conn) *Message {
	t.Helper()
	msg, err := ReadMessage(conn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return msg
}

// expectReject reads a MsgReject with the given code, then the server's close.
func expectReject(t *testing.T, conn net.Conn, wantCode byte) string {
	t.Helper()
	msg := mustRead(t, conn)
	if msg.Type != MsgReject {
		t.Fatalf("got message type %q, want MsgReject", msg.Type)
	}
	code, reason, err := ParseReject(msg.Data)
	if err != nil || code != wantCode {
		t.Fatalf("ParseReject = (%d, %q, %v), want code %d", code, reason, err, wantCode)
	}
	if _, err := ReadMessage(conn); err != io.EOF {
		t.Fatalf("after the reject: err = %v, want io.EOF (server closes)", err)
	}
	return reason
}

// serveOnce runs handle on the first connection to a fresh loopback listener
// and returns the listener's address. handle runs on its own goroutine, so it
// reports through channels rather than t.
func serveOnce(t *testing.T, handle func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}()
	return ln.Addr().String()
}

// drain reads and discards frames until the peer goes away.
func drain(conn net.Conn) {
	conn.SetReadDeadline(time.Time{})
	for {
		if _, err := ReadMessage(conn); err != nil {
			return
		}
	}
}

// v021Server mimics v0.2.1's server handshake byte for byte: it closes the
// connection unless the first frame is a join, answers with the given
// welcome payload and a player list, then reads and ignores everything
// (v0.2.1's read loop has no default case) until the client hangs up. It
// reports the first frame it read on first.
func v021Server(welcome []byte, first chan<- *Message) func(net.Conn) {
	return func(conn net.Conn) {
		msg, err := ReadMessage(conn)
		first <- msg
		if err != nil || msg.Type != MsgJoin {
			return
		}
		conn.Write(append([]byte{MsgWelcome, 0, byte(len(welcome))}, welcome...))
		WriteMessage(conn, MsgPlayerList, append([]byte{1, welcome[0], byte(len(msg.Data))}, msg.Data...))
		drain(conn)
	}
}

// --- version handshake: old client / new server -----------------------------

// TestOldClientToldToUpgrade: a v0.2.1 client (join only, no hello) is not
// admitted. It gets a welcome it can parse (v0.2.1 reads Data[0]) carrying
// the never-assigned ID 0, then a plain-text chat line it prints verbatim
// telling the player to upgrade, then a clean close. It never appears in
// anyone's player list.
func TestOldClientToldToUpgrade(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	defer func(d time.Duration) { helloTimeout = d }(helloTimeout)
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second
	helloTimeout = 50 * time.Millisecond

	srv, addr := mustListen(t)
	defer srv.Stop()

	host := NewClient("host")
	if err := host.Connect(addr); err != nil {
		t.Fatalf("host connect: %v", err)
	}
	defer host.Disconnect()

	old := dialRaw(t, addr)
	if _, err := old.Write(v021Join("oldie")); err != nil {
		t.Fatalf("v0.2.1 join: %v", err)
	}

	welcome := mustRead(t, old)
	if welcome.Type != MsgWelcome || len(welcome.Data) < 1 {
		t.Fatalf("first frame = type %q, %d bytes; v0.2.1 needs a welcome with Data[0]", welcome.Type, len(welcome.Data))
	}
	if welcome.Data[0] != 0 {
		t.Errorf("refusal welcome carries ID %d, want 0 (never assigned)", welcome.Data[0])
	}
	notice := mustRead(t, old)
	if notice.Type != MsgChat {
		t.Fatalf("second frame = type %q, want MsgChat", notice.Type)
	}
	// v0.2.1 logs string(msg.Data) verbatim, so this is what the player sees.
	if got := string(notice.Data); got != legacyUpgradeNotice || !strings.Contains(got, "upgrade") {
		t.Errorf("notice = %q, want %q", got, legacyUpgradeNotice)
	}
	if _, err := ReadMessage(old); err != io.EOF {
		t.Errorf("after the notice: err = %v, want io.EOF (server closes)", err)
	}

	if pc := srv.PlayerCount(); pc != 1 {
		t.Errorf("PlayerCount = %d, want 1 (only the host)", pc)
	}
	if rps := host.GetRemotePlayers(); len(rps) != 0 {
		t.Errorf("host sees %d remote(s) %+v; the refused client must never join", len(rps), rps)
	}
}

// --- version handshake: new client / old server -----------------------------

// TestNewClientStopsOnOldServer: against a server that predates the handshake
// (v0.2.1's 1-byte welcome, or the first epoch-aware build's 9-byte one) the
// client fails with a fatal, explanatory error after ONE attempt instead of
// retrying forever, and the old server read a normal join: the hello comes
// after it, so the name is intact.
func TestNewClientStopsOnOldServer(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	clientReadTimeout = 1 * time.Second // bounds a (buggy) second attempt

	cases := []struct {
		name    string
		welcome []byte
	}{
		{"v0.2.1 1-byte welcome", []byte{3}},
		{"9-byte epoch welcome", WelcomeMessage(3, 0xABCD)[:9]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := make(chan *Message, 1)
			addr := serveOnce(t, v021Server(tc.welcome, first))

			client := NewClient("joiner")
			connects, sleeps := 0, 0
			connect := func(context.Context) error { connects++; return client.Connect(addr) }
			run := func(context.Context) error { t.Error("run called: the session must not start"); return nil }
			backoff := func(int) time.Duration { return 0 }
			sleep := func(ctx context.Context, _ time.Duration) error { sleeps++; return ctx.Err() }

			err := ReconnectLoop(context.Background(), connect, run, backoff, sleep)
			if !errors.Is(err, ErrFatal) {
				t.Fatalf("ReconnectLoop = %v, want an ErrFatal error", err)
			}
			if !strings.Contains(err.Error(), "older ww-multiplayer") {
				t.Errorf("error %q doesn't say the host is older", err)
			}
			if connects != 1 || sleeps != 0 {
				t.Errorf("connects=%d sleeps=%d, want 1 and 0 (no retry)", connects, sleeps)
			}
			if client.IsConnected() {
				t.Error("client reports connected after refusing an old server")
			}
			if msg := <-first; msg == nil || msg.Type != MsgJoin || string(msg.Data) != "joiner" {
				t.Errorf("old server's first frame = %+v, want MsgJoin \"joiner\"", msg)
			}
		})
	}
}

// --- rejects -----------------------------------------------------------------

// TestServerRejectsProtocolMismatch: a hello with another version (or a
// malformed one) gets MsgReject(RejectVersion) with a reason that says which
// side is newer, and the client is never admitted.
func TestServerRejectsProtocolMismatch(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()

	for _, tc := range []struct {
		version uint16
		want    string
	}{
		{0, "newer"},
		{ProtocolVersion + 1, "older"},
	} {
		conn := dialRaw(t, addr)
		rawJoin(t, conn, "tester", tc.version)
		if reason := expectReject(t, conn, RejectVersion); !strings.Contains(reason, tc.want) {
			t.Errorf("hello v%d: reason %q doesn't say the host is %s", tc.version, reason, tc.want)
		}
	}

	// A hello too short to carry a version is a mismatch too.
	conn := dialRaw(t, addr)
	conn.Write(append(frame(MsgJoin, []byte("tester")), frame(MsgHello, []byte{1})...))
	expectReject(t, conn, RejectVersion)

	if pc := srv.PlayerCount(); pc != 0 {
		t.Fatalf("PlayerCount = %d, want 0", pc)
	}
}

// TestClientReportsReject: a MsgReject in place of the welcome becomes a
// *RejectError carrying the server's code and reason, and it is fatal.
func TestClientReportsReject(t *testing.T) {
	addr := serveOnce(t, func(conn net.Conn) {
		ReadMessage(conn) // join
		ReadMessage(conn) // hello
		WriteMessage(conn, MsgReject, RejectMessage(RejectServerFull, "server full"))
	})
	err := NewClient("late").Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectServerFull {
		t.Fatalf("Connect = %v, want a *RejectError with code %d", err, RejectServerFull)
	}
	if !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "server full") {
		t.Fatalf("Connect = %v, want a fatal error naming the reason", err)
	}
}

// TestLateHelloIsRetryable: the ID-0 welcome a server sends when the hello
// arrived after helloTimeout is a transient failure, not a fatal one.
func TestLateHelloIsRetryable(t *testing.T) {
	addr := serveOnce(t, func(conn net.Conn) {
		ReadMessage(conn)
		WriteMessage(conn, MsgWelcome, WelcomeMessage(0, 1))
		drain(conn)
	})
	err := NewClient("slow").Connect(addr)
	if err == nil || errors.Is(err, ErrFatal) {
		t.Fatalf("Connect = %v, want a non-fatal error", err)
	}
}

// TestServerRejectsBadNames: names that are empty, too long, not UTF-8 or
// that carry control characters are refused, not cleaned up. The client
// refuses them up front, fatally.
func TestServerRejectsBadNames(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()

	bad := []string{"", strings.Repeat("n", MaxNameLen+1), "esc\x1b[2J", "two\nlines", "tab\tname", "del\x7f", "bad\xffutf8"}
	for _, name := range bad {
		conn := dialRaw(t, addr)
		rawJoin(t, conn, name, ProtocolVersion)
		expectReject(t, conn, RejectBadName)

		if err := NewClient(name).Connect(addr); !errors.Is(err, ErrFatal) {
			t.Errorf("Connect(%q) = %v, want a fatal error", name, err)
		}
	}
	if pc := srv.PlayerCount(); pc != 0 {
		t.Fatalf("PlayerCount = %d after only bad names, want 0", pc)
	}

	for _, name := range []string{strings.Repeat("n", MaxNameLen), "Zelda ゼルダ", "Player 2"} {
		c := NewClient(name)
		if err := c.Connect(addr); err != nil {
			t.Errorf("Connect(%q) = %v, want accepted", name, err)
			continue
		}
		c.Disconnect()
	}
}

// --- player IDs ----------------------------------------------------------------

func TestAllocIDLocked(t *testing.T) {
	live := func(ids ...int) map[byte]*Player {
		m := map[byte]*Player{}
		for _, id := range ids {
			m[byte(id)] = &Player{ID: byte(id)}
		}
		return m
	}
	all := make([]int, 0, 255)
	for id := 1; id <= 255; id++ {
		all = append(all, id)
	}
	cases := []struct {
		name    string
		next    byte
		players map[byte]*Player
		want    byte
		ok      bool
	}{
		{"fresh", 1, live(), 1, true},
		{"skips 0", 0, live(), 1, true},
		{"skips live IDs", 5, live(5, 6), 7, true},
		{"wraps past 255 and 0", 255, live(255), 1, true},
		{"full", 9, live(all...), 0, false},
	}
	for _, c := range cases {
		srv := NewServer(0)
		srv.nextID, srv.players = c.next, c.players
		got, ok := srv.allocIDLocked()
		if got != c.want || ok != c.ok {
			t.Errorf("%s: allocIDLocked = (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestIDAllocationSkipsLiveIDs is the collision regression: with nextID
// forced onto a live player's ID (as after the byte counter wraps), the new
// player gets a different ID, and the first player's teardown doesn't evict
// the second.
func TestIDAllocationSkipsLiveIDs(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	a := NewClient("a")
	if err := a.Connect(addr); err != nil {
		t.Fatalf("a connect: %v", err)
	}
	srv.mu.Lock()
	srv.nextID = a.MyID()
	srv.mu.Unlock()

	b := NewClient("b")
	if err := b.Connect(addr); err != nil {
		t.Fatalf("b connect: %v", err)
	}
	defer b.Disconnect()
	if b.MyID() == a.MyID() {
		t.Fatalf("b got live ID %d", b.MyID())
	}
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 2 }) {
		t.Fatalf("PlayerCount = %d, want 2", srv.PlayerCount())
	}

	a.Disconnect()
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 1 }) {
		t.Fatalf("PlayerCount = %d after a left, want 1", srv.PlayerCount())
	}
	srv.mu.RLock()
	p := srv.players[b.MyID()]
	srv.mu.RUnlock()
	if p == nil || p.Name != "b" {
		t.Fatalf("players[%d] = %+v, want b (a's teardown evicted the wrong player?)", b.MyID(), p)
	}
}

// TestServerFullRejected: with all 255 IDs live, a new client is refused
// with RejectServerFull (fatal) and nobody is evicted; once an ID frees up,
// the next client gets it.
func TestServerFullRejected(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()

	srv.mu.Lock()
	for id := 1; id <= 255; id++ {
		srv.players[byte(id)] = &Player{ID: byte(id), Name: "p", Conn: &okConn{}}
	}
	srv.mu.Unlock()

	err := NewClient("one-too-many").Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectServerFull || !errors.Is(err, ErrFatal) {
		t.Fatalf("Connect = %v, want a fatal RejectServerFull", err)
	}
	if pc := srv.PlayerCount(); pc != 255 {
		t.Fatalf("PlayerCount = %d, want 255", pc)
	}

	srv.mu.Lock()
	delete(srv.players, 77)
	srv.mu.Unlock()
	c := NewClient("lucky")
	if err := c.Connect(addr); err != nil {
		t.Fatalf("Connect with one free ID: %v", err)
	}
	defer c.Disconnect()
	if c.MyID() != 77 {
		t.Fatalf("got ID %d, want the free ID 77", c.MyID())
	}
}

// TestWelcomeIsFirstFrameUnderRelayLoad: while another player streams poses,
// every new connection still reads its welcome first (the new player's SendMu
// is held from before it is visible to relays until the welcome is out).
func TestWelcomeIsFirstFrameUnderRelayLoad(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv := NewServer(0)
	// A slow log sink (like the host's console) widens the window in which
	// a relay would beat the welcome if the new player became visible to
	// relays before its welcome was written.
	srv.OnLog = func(string) { time.Sleep(time.Millisecond) }
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	defer srv.Stop()
	addr := serverAddr(t, srv)

	streamer := NewClient("streamer")
	if err := streamer.Connect(addr); err != nil {
		t.Fatalf("streamer connect: %v", err)
	}
	defer streamer.Disconnect()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		matrices := make([]byte, 42*48)
		for {
			select {
			case <-stop:
				return
			default:
				streamer.SendPose(42, matrices, nil)
				streamer.SendPosition(&PlayerPosition{})
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	for i := 0; i < 20; i++ {
		conn := dialRaw(t, addr)
		if err := writeJoin(conn, fmt.Sprintf("joiner%d", i)); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
		if msg := mustRead(t, conn); msg.Type != MsgWelcome {
			t.Fatalf("join %d: first frame is type %q, want MsgWelcome", i, msg.Type)
		}
		conn.Close()
	}
}

// --- ingress caps ---------------------------------------------------------------

// TestIngressCapsKeepRelaysFlowing: oversize chat, an over-max joint count,
// and a face trailer that would push the relay past 64 KiB are dropped at
// the server. Nothing reaches the other player, nobody is disconnected, and
// later traffic from the same sender still flows.
func TestIngressCapsKeepRelaysFlowing(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	var rec chatRecorder
	alice := NewClient("alice")
	alice.OnChat = rec.fn()
	if err := alice.Connect(addr); err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Disconnect()

	mallory := dialRaw(t, addr)
	if err := writeJoin(mallory, "mallory"); err != nil {
		t.Fatalf("mallory join: %v", err)
	}
	welcome := mustRead(t, mallory)
	malloryID, _, _ := ParseWelcome(welcome.Data)
	go drain(mallory)

	bigJoints := make([]byte, 4+(MaxPoseJoints+1)*48)
	bigJoints[0], bigJoints[1] = 0, MaxPoseJoints+1
	hugeFace := make([]byte, MaxPayload) // joints=1, then a 65483-byte face trailer
	hugeFace[1] = 1
	for _, f := range [][]byte{
		frame(MsgChat, bytes.Repeat([]byte{'x'}, MaxChatLen+1)),
		frame(MsgPose, bigJoints),
		frame(MsgPose, hugeFace),
		frame(MsgChat, []byte("still here")),
	} {
		if _, err := mallory.Write(f); err != nil {
			t.Fatalf("mallory write: %v", err)
		}
	}

	if !waitFor(2*time.Second, func() bool { return rec.len() >= 1 }) {
		t.Fatal("alice never got mallory's valid chat")
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "mallory|still here" {
		t.Fatalf("alice got chats %v, want only [mallory|still here]", got)
	}
	for _, rp := range alice.GetRemotePlayers() {
		if rp.ID == malloryID && rp.PoseMatrices != nil {
			t.Errorf("alice received a pose from mallory (%d joints); the oversize poses must be dropped", rp.PoseJoints)
		}
	}
	if !alice.IsConnected() || srv.PlayerCount() != 2 {
		t.Fatalf("alice connected=%v PlayerCount=%d, want true and 2", alice.IsConnected(), srv.PlayerCount())
	}

	if err := alice.SendChat(strings.Repeat("y", MaxChatLen+1)); err == nil {
		t.Error("SendChat accepted text over MaxChatLen")
	}
}

// TestBroadcastExceptDropsOversizeWithoutRetiring: a relay too big to frame
// is dropped before any write, so no recipient is retired over it.
func TestBroadcastExceptDropsOversizeWithoutRetiring(t *testing.T) {
	srv := NewServer(0)
	rcpt := &countingConn{}
	srv.players[2] = &Player{ID: 2, Name: "innocent", Conn: rcpt}

	srv.broadcastExcept(0, MsgChat, make([]byte, MaxPayload+1))

	if n := rcpt.writes.Load(); n != 0 {
		t.Errorf("recipient saw %d writes, want 0", n)
	}
	if rcpt.closed.Load() {
		t.Error("recipient was retired over another sender's oversize relay")
	}
}

func TestWriteMessageFrameLimit(t *testing.T) {
	var buf bytes.Buffer
	max := bytes.Repeat([]byte{0xAB}, MaxPayload)
	if err := WriteMessage(&buf, MsgChat, max); err != nil {
		t.Fatalf("WriteMessage(%d bytes) = %v", MaxPayload, err)
	}
	msg, err := ReadMessage(&buf)
	if err != nil || !bytes.Equal(msg.Data, max) {
		t.Fatalf("a %d-byte frame didn't round-trip: %v", MaxPayload, err)
	}

	buf.Reset()
	err = WriteMessage(&buf, MsgChat, make([]byte, MaxPayload+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteMessage(%d bytes) = %v, want ErrFrameTooLarge", MaxPayload+1, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("an oversize frame wrote %d bytes; it must write nothing", buf.Len())
	}
}

// --- client roster ----------------------------------------------------------------

// TestPlayerListAuthoritativeAfterReconnect: a player who left while we were
// disconnected (so we never saw their MsgLeave) is dropped by the first
// player list after we reconnect; a player still connected is kept.
func TestPlayerListAuthoritativeAfterReconnect(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	x, y, z := NewClient("x"), NewClient("y"), NewClient("z")
	for _, c := range []*Client{x, y, z} {
		if err := c.Connect(addr); err != nil {
			t.Fatalf("connect %s: %v", c.name, err)
		}
	}
	defer z.Disconnect()
	yID, zID := y.MyID(), z.MyID()
	if !waitFor(2*time.Second, func() bool { return hasRemote(x, yID) && hasRemote(x, zID) }) {
		t.Fatal("x never saw y and z")
	}

	x.Disconnect()
	y.Disconnect() // leaves while x is away
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 1 }) {
		t.Fatalf("PlayerCount = %d, want 1 (z)", srv.PlayerCount())
	}

	if err := x.Connect(addr); err != nil {
		t.Fatalf("x reconnect: %v", err)
	}
	defer x.Disconnect()
	if !waitFor(2*time.Second, func() bool { return !hasRemote(x, yID) }) {
		t.Fatal("x still lists y, who left while x was disconnected")
	}
	if !hasRemote(x, zID) {
		t.Fatal("x dropped z, who is still connected")
	}
}

// TestServerRestartClearsRemotes: reconnecting to the same server (same
// epoch) keeps remotes until a list says otherwise, so slots survive a
// reconnect; a new epoch (server restart) drops them at once, before any
// list arrives, since the new server's IDs mean nothing to the old roster.
func TestServerRestartClearsRemotes(t *testing.T) {
	fake := func(epoch uint64, list []byte) string {
		return serveOnce(t, func(conn net.Conn) {
			ReadMessage(conn) // join
			ReadMessage(conn) // hello
			WriteMessage(conn, MsgWelcome, WelcomeMessage(1, epoch))
			if list != nil {
				WriteMessage(conn, MsgPlayerList, list)
			}
			drain(conn)
		})
	}
	list, _ := PlayerListMessage([]PlayerEntry{{ID: 1, Name: "me"}, {ID: 5, Name: "old-friend"}})

	c := NewClient("me")
	if err := c.Connect(fake(100, list)); err != nil {
		t.Fatalf("connect 1: %v", err)
	}
	if !waitFor(2*time.Second, func() bool { return hasRemote(c, 5) }) {
		t.Fatal("never saw remote 5")
	}
	c.Disconnect()

	if err := c.Connect(fake(100, nil)); err != nil {
		t.Fatalf("connect 2 (same epoch): %v", err)
	}
	if !hasRemote(c, 5) {
		t.Fatal("a same-server reconnect dropped remote 5 before any player list")
	}
	c.Disconnect()

	if err := c.Connect(fake(200, nil)); err != nil {
		t.Fatalf("connect 3 (new epoch): %v", err)
	}
	defer c.Disconnect()
	if rps := c.GetRemotePlayers(); len(rps) != 0 {
		t.Fatalf("after a server restart still holding %+v", rps)
	}
}

// TestMalformedPlayerListIgnored: the truncated list that used to panic
// (index out of range) is ignored whole; the roster is left untouched.
func TestMalformedPlayerListIgnored(t *testing.T) {
	c := NewClient("me")
	c.remotes[4] = &RemotePlayer{ID: 4, Name: "keep"}
	var logs []string
	c.OnLog = func(s string) { logs = append(logs, s) }

	for _, bad := range [][]byte{nil, {1, 5}, {2, 4, 4, 'k', 'e', 'e', 'p', 7}, {1, 4, 9, 'k'}} {
		c.parsePlayerList(bad)
	}
	if !hasRemote(c, 4) {
		t.Fatal("a malformed list removed a remote")
	}
	if len(logs) != 4 || !strings.Contains(logs[0], "malformed") {
		t.Fatalf("logs = %q, want one 'malformed' line per bad list", logs)
	}
}

// --- read loop safety net -------------------------------------------------------

// TestReadLoopRecoversFromPanic: a panic while handling a server frame ends
// the session (connected=false, conn closed, reconnect path) instead of
// crashing the process, including when the log callback is what panics
// while a remote is being removed, which must not leave c.mu locked.
func TestReadLoopRecoversFromPanic(t *testing.T) {
	c := NewClient("me")
	var mu sync.Mutex
	var logs []string
	c.OnLog = func(s string) { mu.Lock(); logs = append(logs, s); mu.Unlock() }
	c.OnChat = func(string, string) { panic("chat sink exploded") }
	sc := newScriptConn(frame(MsgChat, ChatRelayMessage("other", "boom")))
	c.conn = sc
	c.connected.Store(true)

	c.readLoop(sc) // must return, not crash the test binary

	if c.IsConnected() {
		t.Error("still connected after a recovered panic")
	}
	if !sc.closed.Load() {
		t.Error("conn not closed after a recovered panic")
	}
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "internal error") || !strings.Contains(joined, "chat sink exploded") {
		t.Errorf("logs = %q, want the recovered panic reported", joined)
	}

	c2 := NewClient("me")
	c2.remotes[3] = &RemotePlayer{ID: 3, Name: "leaver"}
	c2.OnLog = func(string) { panic("log sink exploded") }
	sc2 := newScriptConn(frame(MsgLeave, []byte{3}))
	c2.conn = sc2
	c2.connected.Store(true)
	c2.readLoop(sc2)

	done := make(chan struct{})
	go func() { c2.GetRemotePlayers(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("c.mu left locked after a recovered panic")
	}
	if c2.IsConnected() {
		t.Error("still connected after a recovered panic in the log callback")
	}
}

// --- reconnect loop ---------------------------------------------------------------

// TestReconnectLoopStopsOnFatal: a connect error matching ErrFatal, even
// wrapped, ends the loop with that error and no backoff sleep; an ordinary
// error still backs off and retries.
func TestReconnectLoopStopsOnFatal(t *testing.T) {
	fatal := fmt.Errorf("send join: %w", &RejectError{Code: RejectVersion, Reason: "version mismatch"})
	calls, sleeps := 0, 0
	connect := func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("connection refused") // transient
		}
		return fatal
	}
	run := func(context.Context) error { t.Error("run must not be called"); return nil }
	backoff := func(int) time.Duration { return 0 }
	sleep := func(ctx context.Context, _ time.Duration) error { sleeps++; return ctx.Err() }

	err := ReconnectLoop(context.Background(), connect, run, backoff, sleep)
	if err != fatal {
		t.Fatalf("ReconnectLoop = %v, want the fatal connect error", err)
	}
	if calls != 2 || sleeps != 1 {
		t.Fatalf("calls=%d sleeps=%d, want 2 and 1 (one retry after the transient error, none after the fatal one)", calls, sleeps)
	}
}
