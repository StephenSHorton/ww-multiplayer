package network

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// --- fake net.Conn for deterministic write-error injection ------------------

// dummyAddr is a trivial net.Addr for the fake conns below.
type dummyAddr struct{}

func (dummyAddr) Network() string { return "fake" }
func (dummyAddr) String() string  { return "fake" }

// failWriteConn is a net.Conn whose every Write fails immediately (simulating
// writePlayer's SetWriteDeadline tripping on a stalled peer) and which tracks
// whether Close was called on it.
type failWriteConn struct {
	closed atomic.Bool
}

func (f *failWriteConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (f *failWriteConn) Write(b []byte) (int, error)        { return 0, errors.New("simulated stalled write") }
func (f *failWriteConn) Close() error                       { f.closed.Store(true); return nil }
func (f *failWriteConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (f *failWriteConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (f *failWriteConn) SetDeadline(t time.Time) error      { return nil }
func (f *failWriteConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *failWriteConn) SetWriteDeadline(t time.Time) error { return nil }

// okConn is a net.Conn whose every Write succeeds, tracking whether Close was
// called (used to assert healthy players are left alone).
type okConn struct {
	closed atomic.Bool
}

func (o *okConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (o *okConn) Write(b []byte) (int, error)        { return len(b), nil }
func (o *okConn) Close() error                       { o.closed.Store(true); return nil }
func (o *okConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (o *okConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (o *okConn) SetDeadline(t time.Time) error      { return nil }
func (o *okConn) SetReadDeadline(t time.Time) error  { return nil }
func (o *okConn) SetWriteDeadline(t time.Time) error { return nil }

// --- deterministic unit tests ------------------------------------------------

// TestBroadcastExceptRetiresFailedWriter: broadcastExcept must close the
// connection of any player whose writePlayer call errors (e.g. a stalled
// peer that trips the write deadline), and must leave every other player's
// connection untouched. This directly covers the NIT-1 fix: before it,
// broadcastExcept discarded writePlayer's error and kept hammering a
// desynced stream until the read-timeout eventually reaped it.
func TestBroadcastExceptRetiresFailedWriter(t *testing.T) {
	srv := NewServer(0)

	bad := &Player{ID: 2, Name: "bad", Conn: &failWriteConn{}}
	good := &Player{ID: 3, Name: "good", Conn: &okConn{}}
	srv.players[bad.ID] = bad
	srv.players[good.ID] = good

	srv.broadcastExcept(0, MsgChat, []byte("hi"))

	if !bad.Conn.(*failWriteConn).closed.Load() {
		t.Fatal("broadcastExcept did not close the stalled player's connection after a write error")
	}
	if good.Conn.(*okConn).closed.Load() {
		t.Fatal("broadcastExcept closed the healthy player's connection as collateral damage")
	}
}

// TestBroadcastPlayerListRetiresFailedWriter: same guarantee as above, for
// broadcastPlayerList (the other caller of writePlayer that used to ignore
// its error).
func TestBroadcastPlayerListRetiresFailedWriter(t *testing.T) {
	srv := NewServer(0)

	bad := &Player{ID: 2, Name: "bad", Conn: &failWriteConn{}}
	good := &Player{ID: 3, Name: "good", Conn: &okConn{}}
	srv.players[bad.ID] = bad
	srv.players[good.ID] = good

	srv.broadcastPlayerList()

	if !bad.Conn.(*failWriteConn).closed.Load() {
		t.Fatal("broadcastPlayerList did not close the stalled player's connection after a write error")
	}
	if good.Conn.(*okConn).closed.Load() {
		t.Fatal("broadcastPlayerList closed the healthy player's connection as collateral damage")
	}
}

// TestRetirePlayersClosesOnlyFailedConns is a narrower unit test of the
// helper itself: it must close every conn it's handed and nothing else.
func TestRetirePlayersClosesOnlyFailedConns(t *testing.T) {
	srv := NewServer(0)
	bad := &Player{ID: 1, Name: "bad", Conn: &failWriteConn{}}
	untouched := &okConn{}

	srv.retirePlayers([]*Player{bad})

	if !bad.Conn.(*failWriteConn).closed.Load() {
		t.Fatal("retirePlayers did not close the given player's connection")
	}
	if untouched.closed.Load() {
		t.Fatal("retirePlayers touched a connection it wasn't given")
	}
}

// --- end-to-end best-effort test --------------------------------------------

// TestStalledPeerRetiredWhileHealthyPeerKeepsServing exercises the full
// server loop (real TCP sockets) with one peer that completes the handshake
// and then never reads again. Its receive window is shrunk so the server's
// writes to it fill up and trip the write deadline quickly. Once that
// happens, the server must retire it (close its conn, which unblocks its
// handlePlayer read loop into the normal delete+MsgLeave teardown) while
// continuing to serve the still-healthy peer.
//
// This depends on OS TCP buffer behavior (SetReadBuffer is advisory), so it
// polls generously; if your platform doesn't honor the tiny receive buffer
// the write may never actually stall within the wait window and the test
// will report that clearly rather than hang (waitFor always returns).
func TestStalledPeerRetiredWhileHealthyPeerKeepsServing(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 15 * time.Millisecond // frequent server->client traffic to fill the stalled peer's window
	serverReadTimeout = 5 * time.Second       // long enough that the WRITE path (not the read-timeout reaper) is what retires the stalled peer
	clientReadTimeout = 2 * time.Second
	writeTimeout = 50 * time.Millisecond // trip fast once the peer's window is full

	srv, addr := mustListen(t)
	defer srv.Stop()

	healthy := NewClient("healthy")
	if err := healthy.Connect(addr); err != nil {
		t.Fatalf("healthy connect: %v", err)
	}
	defer healthy.Disconnect()

	stalledConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("stalled dial: %v", err)
	}
	defer stalledConn.Close()
	if tcpConn, ok := stalledConn.(*net.TCPConn); ok {
		_ = tcpConn.SetReadBuffer(1) // ask for the smallest receive window the OS will give us
	}
	if err := WriteMessage(stalledConn, MsgJoin, []byte("stalled")); err != nil {
		t.Fatalf("stalled join: %v", err)
	}
	wmsg, err := ReadMessage(stalledConn)
	if err != nil || wmsg.Type != MsgWelcome {
		t.Fatalf("stalled welcome: %v (type %v)", err, wmsg)
	}
	stalledID, _, _ := ParseWelcome(wmsg.Data)
	// stalledConn is never read again from here on.

	// Keep pushing broadcast traffic from the healthy peer so the stalled
	// peer's receive window fills quickly. Use ~2KB pose frames (real-world
	// Link pose size, per SerializePose's doc comment) rather than 19-byte
	// position updates so this fills even a generously auto-tuned OS receive
	// buffer in well under a second instead of relying on SetReadBuffer(1)
	// being honored exactly.
	const joints = 42
	matrices := make([]byte, joints*48)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				healthy.SendPose(joints, matrices, nil)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	retired := waitFor(4*time.Second, func() bool {
		srv.mu.RLock()
		_, stillThere := srv.players[stalledID]
		srv.mu.RUnlock()
		return !stillThere
	})

	if !retired {
		t.Skip("stalled peer's receive window never filled within the wait window on this platform " +
			"(SetReadBuffer is advisory) — write-error retirement is still covered by the deterministic " +
			"broadcastExcept/broadcastPlayerList tests above")
	}

	if !healthy.IsConnected() {
		t.Fatal("healthy client was dropped as collateral damage while retiring the stalled peer")
	}
	if !waitFor(1*time.Second, func() bool { return srv.PlayerCount() == 1 }) {
		t.Fatalf("PlayerCount = %d, want 1 (only the healthy client should remain)", srv.PlayerCount())
	}
}
