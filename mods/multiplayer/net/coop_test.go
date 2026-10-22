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

// coopRecorder is a CoopHandler that records every call as a line
// ("attach 1 name caps", "frame 1 <hex>", "detach 1") and can echo frames.
type coopRecorder struct {
	mu        sync.Mutex
	events    []string
	conns     map[byte]*CoopConn
	attached  int
	detached  int
	attachErr error                            // returned by Attach when set
	onFrame   func(c *CoopConn, p []byte)      // called from Frame when set
	onAttach  func(c *CoopConn)                // called from Attach when set
	onDetach  func(c *CoopConn)                // called from Detach when set
	detachCh  chan byte                        // receives each detached ID when set
	frameHook func(c *CoopConn, p []byte) bool // when it returns true, Frame panics
}

func newCoopRecorder() *coopRecorder { return &coopRecorder{conns: map[byte]*CoopConn{}} }

func (r *coopRecorder) Attach(c *CoopConn) error {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf("attach %d %s %d", c.ID(), c.Name(), c.Caps()))
	err := r.attachErr
	if err == nil {
		r.attached++
		r.conns[c.ID()] = c
	}
	hook := r.onAttach
	r.mu.Unlock()
	if hook != nil && err == nil {
		hook(c)
	}
	return err
}

func (r *coopRecorder) Frame(c *CoopConn, p []byte) {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf("frame %d %x", c.ID(), p))
	hook, panicHook := r.onFrame, r.frameHook
	r.mu.Unlock()
	if panicHook != nil && panicHook(c, p) {
		panic("handler exploded")
	}
	if hook != nil {
		hook(c, p)
	}
}

func (r *coopRecorder) Detach(c *CoopConn) {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf("detach %d", c.ID()))
	r.detached++
	if r.conns[c.ID()] == c {
		delete(r.conns, c.ID())
	}
	hook, ch := r.onDetach, r.detachCh
	r.mu.Unlock()
	if hook != nil {
		hook(c)
	}
	if ch != nil {
		ch <- c.ID()
	}
}

func (r *coopRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *coopRecorder) counts() (attached, detached int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attached, r.detached
}

func (r *coopRecorder) conn(id byte) *CoopConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns[id]
}

// coopInbox collects a co-op client's OnCoop payloads.
type coopInbox struct {
	mu  sync.Mutex
	got [][]byte
}

func (b *coopInbox) fn() func([]byte) {
	return func(p []byte) { b.mu.Lock(); b.got = append(b.got, p); b.mu.Unlock() }
}

func (b *coopInbox) snapshot() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), b.got...)
}

// logRecorder collects log lines from any goroutine.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) fn() func(string) {
	return func(s string) { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }
}

func (l *logRecorder) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// listenCoop starts a server with h as its CoopHandler.
func listenCoop(t *testing.T, h CoopHandler) (*Server, string) {
	t.Helper()
	return listenCoopLog(t, h, nil)
}

// listenCoopLog is listenCoop with an OnLog, set before Start.
func listenCoopLog(t *testing.T, h CoopHandler, onLog func(string)) (*Server, string) {
	t.Helper()
	srv := NewServer(0)
	srv.Coop = h
	srv.OnLog = onLog
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv, serverAddr(t, srv)
}

// dialCoopRaw opens a co-op connection by hand and returns it with its
// welcome read.
func dialCoopRaw(t *testing.T, addr, name string, caps uint16) (net.Conn, byte) {
	t.Helper()
	conn := dialRaw(t, addr)
	if err := writeJoinRole(conn, name, RoleCoop, caps, nil, nil); err != nil {
		t.Fatalf("co-op join: %v", err)
	}
	msg := mustRead(t, conn)
	if err := checkWelcome(msg); err != nil {
		t.Fatalf("co-op welcome: %v", err)
	}
	return conn, msg.Data[0]
}

// fastNetTimers shortens the heartbeat and read deadlines for one test.
func fastNetTimers(t *testing.T) {
	t.Helper()
	saved := saveNetTimers()
	t.Cleanup(func() { restoreNetTimers(saved) })
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second
}

// --- hello ------------------------------------------------------------------

func TestParseHelloRole(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		version uint16
		role    byte
		caps    uint16
		wantErr bool
	}{
		{"protocol-1 hello", []byte{0, 1}, 1, RolePlayer, 0, false},
		{"protocol-2 player", HelloMessage(ProtocolVersion), ProtocolVersion, RolePlayer, 0, false},
		{"co-op", []byte{0, 2, 1, 0xAB, 0xCD}, 2, RoleCoop, 0xABCD, false},
		{"trailing fields ignored", []byte{0, 2, 1, 0, 3, 9, 9}, 2, RoleCoop, 3, false},
		{"unknown role parses", []byte{0, 2, 7, 0, 0}, 2, 7, 0, false},
		{"empty", nil, 0, 0, 0, true},
		{"1 byte", []byte{0}, 0, 0, 0, true},
		{"role without caps", []byte{0, 2, 1}, 0, 0, 0, true},
		{"half the caps", []byte{0, 2, 1, 0}, 0, 0, 0, true},
	}
	for _, c := range cases {
		v, role, caps, err := ParseHelloRole(c.data)
		if (err != nil) != c.wantErr || v != c.version || role != c.role || caps != c.caps {
			t.Errorf("%s: ParseHelloRole(%x) = (%d, %d, %#x, %v), want (%d, %d, %#x, err=%v)",
				c.name, c.data, v, role, caps, err, c.version, c.role, c.caps, c.wantErr)
		}
	}
}

// TestHelloGoldenBytes pins both hello forms and the join frames each
// connection kind sends: a player's are protocol 1's but for the version.
func TestHelloGoldenBytes(t *testing.T) {
	if got, want := HelloRoleMessage(2, RoleCoop, 0x0102), []byte{0x00, 0x02, 0x01, 0x01, 0x02}; !bytes.Equal(got, want) {
		t.Errorf("HelloRoleMessage = %x, want %x", got, want)
	}

	var player bytes.Buffer
	if err := writeJoin(&player, "Link"); err != nil {
		t.Fatalf("writeJoin: %v", err)
	}
	want := []byte{'J', 0, 4, 'L', 'i', 'n', 'k', 'H', 0, 2, 0x00, 0x04}
	if !bytes.Equal(player.Bytes(), want) {
		t.Errorf("player join = %x, want %x", player.Bytes(), want)
	}

	var coop bytes.Buffer
	if err := writeJoinRole(&coop, "Link", RoleCoop, 0x8001, nil, nil); err != nil {
		t.Fatalf("writeJoinRole: %v", err)
	}
	want = []byte{'J', 0, 4, 'L', 'i', 'n', 'k', 'H', 0, 5, 0x00, 0x04, 0x01, 0x80, 0x01}
	if !bytes.Equal(coop.Bytes(), want) {
		t.Errorf("co-op join = %x, want %x", coop.Bytes(), want)
	}

	if err := writeJoinRole(io.Discard, "bad\nname", RoleCoop, 0, nil, nil); !errors.Is(err, ErrFatal) {
		t.Errorf("co-op join with a bad name = %v, want a fatal error", err)
	}
}

func TestMessageTypesDistinctWithCoop(t *testing.T) {
	types := map[byte]string{
		MsgJoin: "Join", MsgWelcome: "Welcome", MsgPosition: "Position",
		MsgPlayerList: "PlayerList", MsgLeave: "Leave", MsgChat: "Chat",
		MsgPose: "Pose", MsgPing: "Ping", MsgPong: "Pong",
		MsgHello: "Hello", MsgReject: "Reject", MsgCoop: "Coop",
	}
	if len(types) != 12 || MsgCoop != 0x4F {
		t.Fatalf("got %d distinct message types and MsgCoop %#x, want 12 and 0x4F", len(types), MsgCoop)
	}
}

// TestOldFormatHelloIsPlayer: a 2-byte hello (the protocol-1 form, now a
// player's) is admitted as a player, never a co-op connection, even with a
// CoopHandler installed.
func TestOldFormatHelloIsPlayer(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	srv, addr := listenCoop(t, rec)

	conn := dialRaw(t, addr)
	rawJoin(t, conn, "plain", ProtocolVersion)
	if msg := mustRead(t, conn); msg.Type != MsgWelcome {
		t.Fatalf("first frame = type %q, want MsgWelcome", msg.Type)
	}
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 1 }) {
		t.Fatalf("PlayerCount = %d, want 1", srv.PlayerCount())
	}
	if n := srv.CoopCount(); n != 0 {
		t.Errorf("CoopCount = %d, want 0", n)
	}
	if ev := rec.snapshot(); len(ev) != 0 {
		t.Errorf("handler saw %q for a player hello", ev)
	}
}

// --- version and role rejects ---------------------------------------------------

// TestVersion1PeerRejected: a protocol-1 (v0.3.0) client is refused with the
// version reject its build already shows ("host refused the connection: ..."),
// saying the host is newer; a co-op hello carrying version 1 gets the same.
func TestVersion1PeerRejected(t *testing.T) {
	srv, addr := listenCoop(t, newCoopRecorder())
	for _, hello := range [][]byte{HelloMessage(1), HelloRoleMessage(1, RoleCoop, 0)} {
		conn := dialRaw(t, addr)
		conn.Write(append(frame(MsgJoin, []byte("v030")), frame(MsgHello, hello)...))
		reason := expectReject(t, conn, RejectVersion)
		if !strings.Contains(reason, "newer") || !strings.Contains(reason, fmt.Sprintf("network protocol %d, yours is 1", ProtocolVersion)) {
			t.Errorf("hello %x: reason %q, want the newer-host message naming protocols %d and 1", hello, reason, ProtocolVersion)
		}
	}
	if srv.PlayerCount() != 0 || srv.CoopCount() != 0 {
		t.Fatalf("PlayerCount=%d CoopCount=%d, want 0 and 0", srv.PlayerCount(), srv.CoopCount())
	}
}

// v030Server mimics a v0.3.0 (protocol 1) server's handshake: it reads the
// join and hello and refuses any version but 1 with the reject that build
// sends.
func v030Server(conn net.Conn) {
	ReadMessage(conn)
	hello, err := ReadMessage(conn)
	if err != nil {
		return
	}
	v, _ := ParseHello(hello.Data)
	WriteMessage(conn, MsgReject, RejectMessage(RejectVersion,
		fmt.Sprintf("this host runs an older ww-multiplayer (network protocol 1, yours is %d); both players need the same version", v)))
	drain(conn)
}

// TestClientsStopOnVersion1Server: player and co-op clients alike fail
// fatally against a protocol-1 host, with its reason, and against a host
// whose welcome carries version 1.
func TestClientsStopOnVersion1Server(t *testing.T) {
	for _, mk := range []func() *Client{
		func() *Client { return NewClient("joiner") },
		func() *Client { return NewCoopClient("joiner", 0) },
	} {
		err := mk().Connect(serveOnce(t, v030Server))
		if !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), fmt.Sprintf("older ww-multiplayer (network protocol 1, yours is %d)", ProtocolVersion)) {
			t.Errorf("against a v0.3.0 host: Connect = %v, want its fatal version reject", err)
		}

		addr := serveOnce(t, func(conn net.Conn) {
			ReadMessage(conn)
			ReadMessage(conn)
			w := WelcomeMessage(4, 1)
			w[9], w[10] = 0, 1
			WriteMessage(conn, MsgWelcome, w)
			drain(conn)
		})
		err = mk().Connect(addr)
		if !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), fmt.Sprintf("host speaks network protocol 1 and this build speaks %d", ProtocolVersion)) {
			t.Errorf("against a version-1 welcome: Connect = %v, want the fatal mismatch", err)
		}
	}
}

// TestVersion2PeersRefused: a protocol-2 (v0.4.x) hello of every role it
// could send is refused at the version check, before its role is read,
// with the newer-host version reject (the app's version dialog); nothing
// is admitted. An old joiner's role-4 hello used to end its whole session
// with a RejectRole from an old host; under 3 both directions are a
// version mismatch instead.
func TestVersion2PeersRefused(t *testing.T) {
	srv, addr := listenCoop(t, newCoopRecorder())
	for _, hello := range [][]byte{
		HelloMessage(2),
		HelloRoleMessage(2, RoleCoop, 0),
		HelloRoleMessage(2, RoleLobby, LobbyCapJSON),
		HelloRoleMessage(2, RoleEnemyClient, 0),
		HelloRoleMessage(2, RoleEnemyHost, 0),
	} {
		conn := dialRaw(t, addr)
		conn.Write(append(frame(MsgJoin, []byte("v041")), frame(MsgHello, hello)...))
		reason := expectReject(t, conn, RejectVersion)
		if !strings.Contains(reason, "newer") || !strings.Contains(reason, fmt.Sprintf("network protocol %d, yours is 2", ProtocolVersion)) {
			t.Errorf("hello %x: reason %q, want the newer-host message naming protocols %d and 2", hello, reason, ProtocolVersion)
		}
	}
	if srv.PlayerCount() != 0 || srv.CoopCount() != 0 || srv.EnemyClientCount() != 0 || srv.EnemyHostConnected() {
		t.Fatalf("PlayerCount=%d CoopCount=%d EnemyClientCount=%d EnemyHost=%v, want none admitted",
			srv.PlayerCount(), srv.CoopCount(), srv.EnemyClientCount(), srv.EnemyHostConnected())
	}
}

// TestVersion3PeersRefused: a protocol-3 (v0.5.0) hello of every role it
// could send is refused at the version check (batch C D13: its kind table
// differs, which would otherwise surface only as the enemy KindHash
// disarm), and nothing is admitted.
func TestVersion3PeersRefused(t *testing.T) {
	srv, addr := listenCoop(t, newCoopRecorder())
	for _, hello := range [][]byte{
		HelloMessage(3),
		HelloRoleMessage(3, RoleCoop, 0),
		HelloRoleMessage(3, RoleLobby, LobbyCapJSON),
		HelloRoleMessage(3, RoleEnemyClient, 0),
		HelloRoleMessage(3, RoleEnemyHost, 0),
	} {
		conn := dialRaw(t, addr)
		conn.Write(append(frame(MsgJoin, []byte("v050")), frame(MsgHello, hello)...))
		reason := expectReject(t, conn, RejectVersion)
		if !strings.Contains(reason, "newer") || !strings.Contains(reason, fmt.Sprintf("network protocol %d, yours is 3", ProtocolVersion)) {
			t.Errorf("hello %x: reason %q, want the newer-host message naming protocols %d and 3", hello, reason, ProtocolVersion)
		}
	}
	if srv.PlayerCount() != 0 || srv.CoopCount() != 0 || srv.EnemyClientCount() != 0 || srv.EnemyHostConnected() {
		t.Fatalf("PlayerCount=%d CoopCount=%d EnemyClientCount=%d EnemyHost=%v, want none admitted",
			srv.PlayerCount(), srv.CoopCount(), srv.EnemyClientCount(), srv.EnemyHostConnected())
	}
}

// TestClientsStopOnVersion2Welcome: a player, co-op or enemy client of this
// build refuses a protocol-2 host's welcome itself, fatally, with a
// RejectVersion that says both players need the same version.
func TestClientsStopOnVersion2Welcome(t *testing.T) {
	for _, mk := range []func() *Client{
		func() *Client { return NewClient("joiner") },
		func() *Client { return NewCoopClient("joiner", 0) },
		func() *Client { return NewEnemyClient("joiner", RoleEnemyClient) },
	} {
		addr := serveOnce(t, func(conn net.Conn) {
			ReadMessage(conn)
			ReadMessage(conn)
			w := WelcomeMessage(4, 1)
			w[9], w[10] = 0, 2
			WriteMessage(conn, MsgWelcome, w)
			drain(conn)
		})
		err := mk().Connect(addr)
		var rej *RejectError
		if !errors.As(err, &rej) || rej.Code != RejectVersion || !errors.Is(err, ErrFatal) ||
			!strings.Contains(err.Error(), fmt.Sprintf("host speaks network protocol 2 and this build speaks %d", ProtocolVersion)) ||
			!strings.Contains(err.Error(), "same ww-multiplayer version") {
			t.Errorf("against a version-2 welcome: Connect = %v, want the fatal version mismatch", err)
		}
	}
}

// TestCoopRefusedWhenCoopOff: without a CoopHandler a co-op hello is refused
// with RejectRole and a reason that says why, fatally, so RunCoop stops at
// once; nothing is admitted.
func TestCoopRefusedWhenCoopOff(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()

	err := NewCoopClient("alice", 0).Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectRole || !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "not running co-op") {
		t.Fatalf("Connect = %v, want a fatal RejectRole saying co-op is off", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewCoopClient("alice", 0)
	sessions := 0
	err = RunCoop(ctx, c, addr, nil, func(context.Context, func([]byte) error) error { sessions++; return nil })
	if !errors.As(err, &rej) || rej.Code != RejectRole || ctx.Err() != nil {
		t.Fatalf("RunCoop = %v (ctx %v), want the RejectRole error before the timeout", err, ctx.Err())
	}
	if sessions != 0 {
		t.Errorf("session ran %d times on a refused connection", sessions)
	}
	if srv.PlayerCount() != 0 || srv.CoopCount() != 0 {
		t.Fatalf("PlayerCount=%d CoopCount=%d, want 0 and 0", srv.PlayerCount(), srv.CoopCount())
	}
}

// TestBadRoleHellosRefused: an unknown role is a RejectRole, and a hello cut
// off inside role and caps a RejectVersion, with or without co-op on.
func TestBadRoleHellosRefused(t *testing.T) {
	for _, h := range []CoopHandler{nil, newCoopRecorder()} {
		srv, addr := listenCoop(t, h)
		cases := []struct {
			hello []byte
			code  byte
			want  string
		}{
			{HelloRoleMessage(ProtocolVersion, 7, 0), RejectRole, "unknown connection role 7"},
			{append(HelloMessage(ProtocolVersion), 1), RejectVersion, "malformed hello"},
			{append(HelloMessage(ProtocolVersion), 1, 0), RejectVersion, "malformed hello"},
		}
		for _, c := range cases {
			conn := dialRaw(t, addr)
			conn.Write(append(frame(MsgJoin, []byte("tester")), frame(MsgHello, c.hello)...))
			if reason := expectReject(t, conn, c.code); !strings.Contains(reason, c.want) {
				t.Errorf("hello %x: reason %q, want %q", c.hello, reason, c.want)
			}
		}
		if srv.PlayerCount() != 0 || srv.CoopCount() != 0 {
			t.Fatalf("PlayerCount=%d CoopCount=%d, want 0 and 0", srv.PlayerCount(), srv.CoopCount())
		}
	}
}

// TestCoopRejectsBadName: a co-op connection's name is checked like a
// player's.
func TestCoopRejectsBadName(t *testing.T) {
	_, addr := listenCoop(t, newCoopRecorder())
	conn := dialRaw(t, addr)
	conn.Write(append(frame(MsgJoin, []byte("esc\x1b[2J")), frame(MsgHello, HelloRoleMessage(ProtocolVersion, RoleCoop, 0))...))
	expectReject(t, conn, RejectBadName)
}

// --- mixed roles ----------------------------------------------------------------

// TestMixedRoleConnections: two players and two co-op connections on one
// server. The players see only each other (roster, count, relays); the co-op
// connections get none of the player traffic, only the heartbeat and the
// handler's frames, and their IDs come from a space of their own.
func TestMixedRoleConnections(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	srv, addr := listenCoop(t, rec)

	var chats chatRecorder
	a, b := NewClient("alice"), NewClient("bob")
	b.OnChat = chats.fn()
	for _, c := range []*Client{a, b} {
		if err := c.Connect(addr); err != nil {
			t.Fatalf("%s connect: %v", c.name, err)
		}
		defer c.Disconnect()
	}

	// One co-op connection through the client, one by hand so every frame
	// it receives can be checked.
	var inbox coopInbox
	var coopLogs logRecorder
	cc := NewCoopClient("alice", 0x0003)
	cc.OnCoop = inbox.fn()
	cc.OnLog = coopLogs.fn()
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("co-op client connect: %v", err)
	}
	defer cc.Disconnect()
	raw, rawID := dialCoopRaw(t, addr, "bob", 0)

	if srv.PlayerCount() != 2 || srv.CoopCount() != 2 {
		t.Fatalf("PlayerCount=%d CoopCount=%d, want 2 and 2", srv.PlayerCount(), srv.CoopCount())
	}
	if cc.MyID() != 1 || rawID != 2 {
		t.Errorf("co-op IDs = %d and %d, want 1 and 2 (their own space; players hold 1 and 2 too)", cc.MyID(), rawID)
	}
	if !waitFor(2*time.Second, func() bool { return hasRemote(a, b.MyID()) && hasRemote(b, a.MyID()) }) {
		t.Fatal("the players never saw each other")
	}

	// Player traffic, then a marker from the handler once bob has it all.
	a.SendChat("hi bob")
	a.SendPosition(&PlayerPosition{PosX: 1})
	a.SendPose(42, make([]byte, 42*48), nil)
	if !waitFor(2*time.Second, func() bool {
		rps := b.GetRemotePlayers()
		return chats.len() == 1 && len(rps) == 1 && rps[0].Position != nil && rps[0].PoseMatrices != nil
	}) {
		t.Fatal("bob never got alice's chat, position and pose")
	}
	marker := []byte{0x07, 0xAA}
	raw.Write(frame(MsgPing, PingPayload(1))) // keeps the server's read deadline at bay
	if err := rec.conn(rawID).Send(marker); err != nil {
		t.Fatalf("Send to the raw co-op conn: %v", err)
	}
	raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		msg := mustRead(t, raw)
		if msg.Type == MsgCoop {
			if !bytes.Equal(msg.Data, marker) {
				t.Fatalf("co-op conn got 'O' %x, want the marker %x", msg.Data, marker)
			}
			break
		}
		if msg.Type != MsgPing && msg.Type != MsgPong {
			t.Fatalf("co-op conn got a %q frame; it must get no player traffic", msg.Type)
		}
	}
	raw.Close()
	if !waitFor(2*time.Second, func() bool { return srv.CoopCount() == 1 }) {
		t.Fatalf("CoopCount = %d after the raw co-op conn closed, want 1", srv.CoopCount())
	}

	// Co-op frames reach the handler from the client, tagged with its
	// connection, and the handler's answer reaches OnCoop.
	rec.mu.Lock()
	rec.onFrame = func(c *CoopConn, p []byte) { c.Send(append([]byte{0xEE}, p...)) }
	rec.mu.Unlock()
	if err := cc.SendCoop([]byte{0x05, 1, 2, 3}); err != nil {
		t.Fatalf("SendCoop: %v", err)
	}
	if !waitFor(2*time.Second, func() bool { return len(inbox.snapshot()) == 1 }) {
		t.Fatal("the handler's answer never reached OnCoop")
	}
	if got := inbox.snapshot()[0]; !bytes.Equal(got, []byte{0xEE, 0x05, 1, 2, 3}) {
		t.Errorf("OnCoop got %x", got)
	}
	if ev := rec.snapshot(); !contains(ev, "attach 1 alice 3") || !contains(ev, "frame 1 05010203") {
		t.Errorf("handler events %q, want alice's attach (caps 3) and frame on co-op ID 1", ev)
	}

	// Rosters: each player lists only the other; co-op connections list
	// nobody and log nothing but their connect line.
	for _, c := range []struct {
		cl    *Client
		other byte
	}{{a, b.MyID()}, {b, a.MyID()}} {
		if rps := c.cl.GetRemotePlayers(); len(rps) != 1 || rps[0].ID != c.other {
			t.Errorf("%s's roster = %+v, want only player %d", c.cl.name, rps, c.other)
		}
	}
	if rps := cc.GetRemotePlayers(); len(rps) != 0 {
		t.Errorf("the co-op client holds remotes %+v", rps)
	}
	if logs := coopLogs.joined(); logs != "Connected for co-op (co-op ID 1)" {
		t.Errorf("co-op client logs %q, want only its connect line", logs)
	}

	// A co-op connection leaving changes no roster and sends no MsgLeave.
	cc.Disconnect()
	if !waitFor(2*time.Second, func() bool { return srv.CoopCount() == 0 }) {
		t.Fatalf("CoopCount = %d after the co-op disconnects, want 0", srv.CoopCount())
	}
	if srv.PlayerCount() != 2 || !hasRemote(a, b.MyID()) || !hasRemote(b, a.MyID()) {
		t.Fatal("a co-op disconnect changed the player side")
	}
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// TestCoopNotInPlayerLimit: with all 255 player IDs taken a co-op connection
// still gets in, and it takes no player ID.
func TestCoopNotInPlayerLimit(t *testing.T) {
	fastNetTimers(t)
	srv, addr := listenCoop(t, newCoopRecorder())
	srv.mu.Lock()
	for id := 1; id <= 255; id++ {
		srv.players[byte(id)] = &Player{ID: byte(id), Name: "p", Conn: &okConn{}}
	}
	srv.mu.Unlock()

	cc := NewCoopClient("late", 0)
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("co-op connect with the player side full: %v", err)
	}
	defer cc.Disconnect()
	if srv.PlayerCount() != 255 || srv.CoopCount() != 1 {
		t.Fatalf("PlayerCount=%d CoopCount=%d, want 255 and 1", srv.PlayerCount(), srv.CoopCount())
	}
}

// TestCoopFramesDroppedOnPlayerConnection: 'O' frames from a player reach no
// handler; the player stays connected and its other traffic still flows.
func TestCoopFramesDroppedOnPlayerConnection(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	var logs logRecorder
	srv, addr := listenCoopLog(t, rec, logs.fn())

	var chats chatRecorder
	watcher := NewClient("watcher")
	watcher.OnChat = chats.fn()
	if err := watcher.Connect(addr); err != nil {
		t.Fatalf("watcher connect: %v", err)
	}
	defer watcher.Disconnect()
	cc := NewCoopClient("watcher", 0)
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("co-op connect: %v", err)
	}
	defer cc.Disconnect()

	p := dialRaw(t, addr)
	if err := writeJoin(p, "mallory"); err != nil {
		t.Fatalf("join: %v", err)
	}
	mustRead(t, p) // welcome
	go drain(p)
	for i := 0; i < 3; i++ {
		p.Write(frame(MsgCoop, []byte{0x04, byte(i)}))
	}
	p.Write(frame(MsgChat, []byte("still here")))

	if !waitFor(2*time.Second, func() bool { return chats.len() == 1 }) {
		t.Fatal("the player's chat after its 'O' frames never arrived")
	}
	for _, e := range rec.snapshot() {
		if strings.HasPrefix(e, "frame") {
			t.Errorf("handler got %q from a player connection", e)
		}
	}
	if n := strings.Count(logs.joined(), "dropping co-op frames from mallory"); n != 1 {
		t.Errorf("logged the drop %d times, want once", n)
	}
	if srv.PlayerCount() != 2 {
		t.Errorf("PlayerCount = %d, want 2", srv.PlayerCount())
	}
}

// TestPlayerClientIgnoresCoop: a player client never hands an 'O' frame to
// OnCoop and refuses to send one.
func TestPlayerClientIgnoresCoop(t *testing.T) {
	c := NewClient("me")
	called := false
	c.OnCoop = func([]byte) { called = true }
	sc := newScriptConn(frame(MsgCoop, []byte{1, 2}))
	c.conn = sc
	c.connected.Store(true)
	c.readLoop(sc)
	if called {
		t.Error("a player client passed an 'O' frame to OnCoop")
	}
	if err := c.SendCoop([]byte{1}); err == nil {
		t.Error("SendCoop on a player client succeeded")
	}
	if err := RunCoop(context.Background(), c, "127.0.0.1:1", nil, nil); err == nil {
		t.Error("RunCoop accepted a player client")
	}
}

// --- handler lifecycle ------------------------------------------------------------

// TestCoopHandlerLifecycle: Attach, then the frames in order, then exactly
// one Detach when the client goes; Send fails after it; the ID is reused
// only once Detach is done; an Attach error refuses the connection without a
// Detach; the server stopping detaches everyone.
func TestCoopHandlerLifecycle(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	rec.detachCh = make(chan byte, 4)
	attached := make(chan *CoopConn, 4)
	rec.onAttach = func(c *CoopConn) {
		// Queued during Attach: must arrive after the welcome.
		c.Send([]byte{0x02, 0x00})
		attached <- c
	}
	srv, addr := listenCoop(t, rec)

	var inbox coopInbox
	c1 := NewCoopClient("one", 9)
	c1.OnCoop = inbox.fn()
	if err := c1.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	conn := <-attached
	for i := 0; i < 5; i++ {
		c1.SendCoop([]byte{0x04, byte(i)})
	}
	if !waitFor(2*time.Second, func() bool { return len(rec.snapshot()) == 6 && len(inbox.snapshot()) == 1 }) {
		t.Fatalf("events %q, inbox %x", rec.snapshot(), inbox.snapshot())
	}
	want := []string{"attach 1 one 9", "frame 1 0400", "frame 1 0401", "frame 1 0402", "frame 1 0403", "frame 1 0404"}
	if got := rec.snapshot(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events %q, want %q", got, want)
	}

	c1.Disconnect()
	select {
	case id := <-rec.detachCh:
		if id != 1 {
			t.Fatalf("detached %d, want 1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Detach after the client disconnected")
	}
	if err := conn.Send([]byte{1}); !errors.Is(err, ErrCoopClosed) {
		t.Errorf("Send after Detach = %v, want ErrCoopClosed", err)
	}
	if !waitFor(2*time.Second, func() bool { return srv.CoopCount() == 0 }) {
		t.Fatalf("CoopCount = %d after Detach, want 0", srv.CoopCount())
	}

	// An Attach error refuses with its reason; no Detach follows.
	rec.mu.Lock()
	rec.attachErr = errors.New("world is full")
	rec.mu.Unlock()
	err := NewCoopClient("two", 0).Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectRole || !strings.Contains(err.Error(), "co-op: world is full") {
		t.Fatalf("Connect with Attach failing = %v, want RejectRole with the reason", err)
	}
	rec.mu.Lock()
	rec.attachErr = nil
	rec.mu.Unlock()

	// Stopping the server detaches every live connection.
	c3, c4 := NewCoopClient("three", 0), NewCoopClient("four", 0)
	for _, c := range []*Client{c3, c4} {
		if err := c.Connect(addr); err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer c.Disconnect()
		<-attached
	}
	srv.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-rec.detachCh:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop left a co-op connection attached")
		}
	}
	if at, de := rec.counts(); at != 3 || de != 3 {
		t.Errorf("attached %d, detached %d; want 3 and 3", at, de)
	}
}

// TestCoopIDReusedOnlyAfterDetach: while Detach for an ID is still running,
// a new connection gets another ID.
func TestCoopIDReusedOnlyAfterDetach(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	inDetach := make(chan struct{})
	var once sync.Once
	rec.onDetach = func(*CoopConn) { once.Do(func() { close(inDetach); <-release }) }
	srv, addr := listenCoop(t, rec)

	c1 := NewCoopClient("one", 0)
	if err := c1.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	c1.Disconnect()
	<-inDetach
	srv.mu.Lock()
	srv.nextCoopID = 1 // aim the allocator at the ID being detached
	srv.mu.Unlock()

	c2 := NewCoopClient("two", 0)
	if err := c2.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c2.Disconnect()
	if c2.MyID() == 1 {
		t.Fatal("ID 1 was reused while its Detach was still running")
	}
	releaseOnce.Do(func() { close(release) })
	if !waitFor(2*time.Second, func() bool { return srv.CoopCount() == 1 }) {
		t.Fatalf("CoopCount = %d once the Detach finished, want 1", srv.CoopCount())
	}
}

// TestCoopHandlerPanicRecovered: a handler that panics costs its connection
// (closed and detached), not the server.
func TestCoopHandlerPanicRecovered(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	rec.detachCh = make(chan byte, 1)
	rec.frameHook = func(_ *CoopConn, p []byte) bool { return len(p) > 0 && p[0] == 0xFF }
	var logs logRecorder
	_, addr := listenCoopLog(t, rec, logs.fn())

	cc := NewCoopClient("boom", 0)
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	cc.SendCoop([]byte{0xFF})
	select {
	case <-rec.detachCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no Detach after the handler panicked")
	}
	if !waitFor(2*time.Second, func() bool { return !cc.IsConnected() }) {
		t.Error("the co-op client is still connected after the handler panicked")
	}
	if !waitFor(2*time.Second, func() bool {
		return strings.Contains(logs.joined(), "internal error in the co-op handler: handler exploded")
	}) {
		t.Errorf("logs %q, want the recovered panic", logs.joined())
	}
	p := NewClient("after")
	if err := p.Connect(addr); err != nil {
		t.Fatalf("the server stopped serving after a handler panic: %v", err)
	}
	p.Disconnect()
}

// TestCoopAttachPanicRecovered: a panic in Attach refuses that connection
// (no welcome, no Detach) and the server keeps serving.
func TestCoopAttachPanicRecovered(t *testing.T) {
	fastNetTimers(t)
	rec := newCoopRecorder()
	rec.onAttach = func(c *CoopConn) {
		if c.Name() == "boom" {
			panic("attach exploded")
		}
	}
	var logs logRecorder
	srv, addr := listenCoopLog(t, rec, logs.fn())

	err := NewCoopClient("boom", 0).Connect(addr)
	if err == nil || errors.Is(err, ErrFatal) {
		t.Fatalf("Connect = %v, want a non-fatal handshake error", err)
	}
	if !waitFor(2*time.Second, func() bool {
		return strings.Contains(logs.joined(), "internal error in the co-op handler: attach exploded") && srv.CoopCount() == 0
	}) {
		t.Fatalf("logs %q, CoopCount %d; want the recovered panic and 0", logs.joined(), srv.CoopCount())
	}
	if _, de := rec.counts(); de != 0 {
		t.Errorf("Detach ran %d times for an Attach that panicked", de)
	}
	cc := NewCoopClient("fine", 0)
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("the server stopped serving after an Attach panic: %v", err)
	}
	cc.Disconnect()
}

// --- heartbeat --------------------------------------------------------------------

// TestCoopHeartbeat: a quiet co-op client is kept alive by the heartbeat
// past the server's read timeout and measures an RTT; a co-op peer that
// never answers is reaped and detached.
func TestCoopHeartbeat(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 30 * time.Millisecond
	serverReadTimeout = 200 * time.Millisecond
	clientReadTimeout = 2 * time.Second

	rec := newCoopRecorder()
	rec.detachCh = make(chan byte, 2)
	srv, addr := listenCoop(t, rec)

	cc := NewCoopClient("quiet", 0)
	if err := cc.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cc.Disconnect()

	silent, silentID := dialCoopRaw(t, addr, "silent", 0)
	_ = silent // never reads or answers: the server's pings go unanswered
	select {
	case id := <-rec.detachCh:
		if id != silentID {
			t.Fatalf("detached %d, want the silent connection %d", id, silentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a co-op peer that never answers was not reaped")
	}

	time.Sleep(3 * serverReadTimeout)
	if !cc.IsConnected() || srv.CoopCount() != 1 {
		t.Fatalf("connected=%v CoopCount=%d after %v of quiet, want true and 1", cc.IsConnected(), srv.CoopCount(), 3*serverReadTimeout)
	}
	if cc.LastRTT() <= 0 {
		t.Error("no RTT measured on the co-op connection")
	}
}

// --- send queue -------------------------------------------------------------------

// TestCoopSendQueue: Send never blocks. It refuses an oversize payload
// without touching the connection, closes a connection whose queue is full,
// and fails once the connection is closed.
func TestCoopSendQueue(t *testing.T) {
	oc := &okConn{}
	c := &CoopConn{id: 1, name: "slow", conn: oc, srv: NewServer(0), out: make(chan []byte, coopSendQueue), done: make(chan struct{})}

	if err := c.Send(make([]byte, MaxPayload+1)); !errors.Is(err, ErrFrameTooLarge) || oc.closed.Load() {
		t.Fatalf("oversize Send = %v (closed %v), want ErrFrameTooLarge and the conn left open", err, oc.closed.Load())
	}
	for i := 0; i < coopSendQueue; i++ {
		if err := c.Send([]byte{byte(i)}); err != nil {
			t.Fatalf("Send %d with room in the queue: %v", i, err)
		}
	}
	if err := c.Send([]byte{0}); !errors.Is(err, ErrCoopBacklog) || !oc.closed.Load() {
		t.Fatalf("Send on a full queue = %v (closed %v), want ErrCoopBacklog and the conn closed", err, oc.closed.Load())
	}
	if err := c.Send([]byte{0}); !errors.Is(err, ErrCoopClosed) {
		t.Fatalf("Send after close = %v, want ErrCoopClosed", err)
	}
	c.Close() // idempotent
}

// TestCoopWriterRetiresFailedConn: a write the peer can't take closes the
// connection instead of wedging the queue.
func TestCoopWriterRetiresFailedConn(t *testing.T) {
	fc := &failWriteConn{}
	c := &CoopConn{id: 1, name: "dead", conn: fc, srv: NewServer(0), out: make(chan []byte, coopSendQueue), done: make(chan struct{})}
	done := make(chan struct{})
	go func() { c.writeLoop(); close(done) }()
	c.Send([]byte{1})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop kept running after a failed write")
	}
	if !fc.closed.Load() {
		t.Fatal("a failed write left the conn open")
	}
}

// --- RunCoop ----------------------------------------------------------------------

// TestRunCoop: RunCoop runs session per live connection with a working
// send, delivers inbound frames to OnCoop, reconnects after the host drops
// the connection, and returns nil once ctx ends.
func TestRunCoop(t *testing.T) {
	fastNetTimers(t)
	defer func(d time.Duration) { minHealthySession = d }(minHealthySession)
	minHealthySession = 10 * time.Millisecond

	rec := newCoopRecorder()
	rec.onFrame = func(c *CoopConn, p []byte) { c.Send(append([]byte{0xEE}, p...)) }
	srv, addr := listenCoop(t, rec)

	var inbox coopInbox
	var logs logRecorder
	cc := NewCoopClient("runner", 0)
	cc.OnCoop = inbox.fn()
	connLog := NewConnectLog(addr, logs.fn(), logs.fn())

	var sessions atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- RunCoop(ctx, cc, addr, connLog, func(ctx context.Context, send func([]byte) error) error {
			n := sessions.Add(1)
			if err := send([]byte{0x01, byte(n)}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})
	}()

	if !waitFor(2*time.Second, func() bool { return len(inbox.snapshot()) == 1 }) {
		t.Fatal("session 1's frame never came back")
	}
	// The host drops the connection: RunCoop dials again and runs a new
	// session.
	if c := rec.conn(cc.MyID()); c != nil {
		c.Close()
	}
	if !waitFor(5*time.Second, func() bool { return len(inbox.snapshot()) == 2 }) {
		t.Fatalf("no second session after the drop (sessions %d)", sessions.Load())
	}
	if got := inbox.snapshot(); !bytes.Equal(got[0], []byte{0xEE, 0x01, 1}) || !bytes.Equal(got[1], []byte{0xEE, 0x01, 2}) {
		t.Errorf("OnCoop got %x", got)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("RunCoop = %v after cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunCoop did not return after cancel")
	}
	if cc.IsConnected() {
		t.Error("still connected after RunCoop returned")
	}
	if !waitFor(2*time.Second, func() bool { return srv.CoopCount() == 0 }) {
		t.Errorf("CoopCount = %d after RunCoop returned, want 0", srv.CoopCount())
	}
	if l := logs.joined(); l != "" {
		t.Errorf("connect log %q, want nothing (no failed attempt)", l)
	}
}

// TestHandshakeDuringStopClosesWithoutReply: a connection the listener
// accepted just before Stop, still in its handshake, is closed with no
// welcome and no reject, so its client retries against the restarted host.
// It used to register after Stop had closed the connections and live on
// with the stopped server (a player or lobby connection, kept up by its
// pings) or, for co-op, meet the hub Stop's caller stops next and take a
// RejectRole, which is fatal: the client's co-op ended for good. That is
// how TestCoopSessionConverges (package main) left a replica armed
// offline after its host restart (issue #68).
func TestHandshakeDuringStopClosesWithoutReply(t *testing.T) {
	coopHello := HelloRoleMessage(ProtocolVersion, RoleCoop, 0)
	// stoppedHub refuses as the co-op hub does once its caller has
	// stopped it, right after the server.
	stoppedHub := newCoopRecorder()
	stoppedHub.attachErr = errors.New("co-op hub stopped")
	cases := []struct {
		name  string
		hello []byte
		coop  CoopHandler
		lobby bool
	}{
		{"player", HelloMessage(ProtocolVersion), nil, false},
		{"co-op", coopHello, newCoopRecorder(), false},
		{"co-op, hub stopped", coopHello, stoppedHub, false},
		{"lobby", HelloRoleMessage(ProtocolVersion, RoleLobby, LobbyCapJSON), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(0)
			if tc.coop != nil {
				srv.SetCoop(tc.coop)
			}
			srv.Stop()
			cli, sconn := net.Pipe()
			defer cli.Close()
			served := make(chan struct{})
			go func() { defer close(served); srv.handlePlayer(sconn) }()
			cli.SetDeadline(time.Now().Add(5 * time.Second))
			in := append(frame(MsgJoin, []byte("Late")), frame(MsgHello, tc.hello)...)
			if tc.lobby {
				hello, _ := EncodeLobby(LobbyHello, LobbyHelloBody{})
				in = append(in, frame(MsgLobby, hello)...)
			}
			go cli.Write(in)
			if msg, err := ReadMessage(cli); err == nil {
				t.Fatalf("got a %q frame from a stopped server, want a bare close", msg.Type)
			} else if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("read = %v, want the server's close", err)
			}
			<-served
			if srv.PlayerCount() != 0 || srv.CoopCount() != 0 || srv.LobbyCount() != 0 {
				t.Fatalf("PlayerCount=%d CoopCount=%d LobbyCount=%d after Stop", srv.PlayerCount(), srv.CoopCount(), srv.LobbyCount())
			}
		})
	}
}

// TestStaleReadLoopKeepsNewSession: the read loop of a connection that was
// disconnected and already replaced (a fast reconnect) ends without
// marking the new session disconnected.
func TestStaleReadLoopKeepsNewSession(t *testing.T) {
	c := NewClient("me")
	old, cur := newScriptConn(nil), newScriptConn(nil)
	c.conn = cur
	c.connected.Store(true)
	c.readLoop(old) // its read fails at once, as a closed conn's does
	if !c.IsConnected() {
		t.Fatal("a stale read loop marked the new session disconnected")
	}
}
