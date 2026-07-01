package network

import (
	"net"
	"testing"
	"time"
)

// --- test helpers -----------------------------------------------------------

type netTimers struct{ hb, crt, srt time.Duration }

func saveNetTimers() netTimers {
	return netTimers{heartbeatInterval, clientReadTimeout, serverReadTimeout}
}

func restoreNetTimers(n netTimers) {
	heartbeatInterval = n.hb
	clientReadTimeout = n.crt
	serverReadTimeout = n.srt
}

func serverAddr(t *testing.T, s *Server) string {
	t.Helper()
	a := s.Addr()
	if a == nil {
		t.Fatal("server has no bound address")
	}
	_, port, err := net.SplitHostPort(a.String())
	if err != nil {
		t.Fatalf("split host/port %q: %v", a.String(), err)
	}
	return "127.0.0.1:" + port
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func hasRemote(c *Client, id byte) bool {
	for _, rp := range c.GetRemotePlayers() {
		if rp.ID == id {
			return true
		}
	}
	return false
}

func mustListen(t *testing.T) (*Server, string) {
	t.Helper()
	srv := NewServer(0) // OS-assigned port
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	return srv, serverAddr(t, srv)
}

// --- tests ------------------------------------------------------------------

// TestSilentClientReapedAndMsgLeaveBroadcast: a client that completes the
// handshake then goes silent (never pings, never pongs the server's
// heartbeat) trips the server's read deadline and is torn down within
// serverReadTimeout, with a MsgLeave broadcast to a still-connected client.
func TestSilentClientReapedAndMsgLeaveBroadcast(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 30 * time.Millisecond // keeps the watcher alive
	serverReadTimeout = 100 * time.Millisecond
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	// Watcher: a real client kept alive by its 30ms heartbeat; its read loop
	// processes the MsgLeave + player-list updates.
	watcher := NewClient("watcher")
	if err := watcher.Connect(addr); err != nil {
		t.Fatalf("watcher connect: %v", err)
	}
	defer watcher.Disconnect()

	// Silent player: raw conn, handshake then quiet.
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("silent dial: %v", err)
	}
	defer silent.Close()
	if err := WriteMessage(silent, MsgJoin, []byte("silent")); err != nil {
		t.Fatalf("silent join: %v", err)
	}
	wmsg, err := ReadMessage(silent)
	if err != nil || wmsg.Type != MsgWelcome {
		t.Fatalf("silent welcome: %v (type %v)", err, wmsg)
	}
	silentID, _, _ := ParseWelcome(wmsg.Data)

	// Watcher must first observe the silent player.
	if !waitFor(2*time.Second, func() bool { return hasRemote(watcher, silentID) }) {
		t.Fatal("watcher never saw the silent player join")
	}

	// ...then observe it leave once the read deadline trips.
	if !waitFor(2*time.Second, func() bool { return !hasRemote(watcher, silentID) }) {
		t.Fatal("silent player not reaped / MsgLeave not broadcast within timeout")
	}
}

// TestPingingClientNotReaped: a client that keeps pinging faster than the
// read timeout is NOT timed out — each ping resets the server's deadline and
// is answered with a pong. heartbeatInterval is set high so the ONLY traffic
// keeping the client alive is its own pings.
func TestPingingClientNotReaped(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 10 * time.Second // server won't heartbeat in-window
	serverReadTimeout = 100 * time.Millisecond
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := WriteMessage(conn, MsgJoin, []byte("pinger")); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := ReadMessage(conn); err != nil {
		t.Fatalf("welcome: %v", err)
	}

	// Ping every 30ms for ~300ms (3x the 100ms read timeout).
	deadline := time.Now().Add(300 * time.Millisecond)
	rounds := 0
	for time.Now().Before(deadline) {
		if err := WriteMessage(conn, MsgPing, PingPayload(monotonicNanos())); err != nil {
			t.Fatalf("ping #%d write failed (reaped early?): %v", rounds, err)
		}
		// Drain to the next pong, skipping the buffered MsgPlayerList (and
		// any other server-initiated frame) that may arrive first.
		conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			msg, err := ReadMessage(conn)
			if err != nil {
				t.Fatalf("pong #%d read failed (reaped early?): %v", rounds, err)
			}
			if msg.Type == MsgPong {
				break
			}
		}
		rounds++
		time.Sleep(30 * time.Millisecond)
	}
	if rounds < 3 {
		t.Fatalf("only %d ping/pong rounds; expected >= 3", rounds)
	}
	if pc := srv.PlayerCount(); pc != 1 {
		t.Fatalf("PlayerCount = %d, want 1 (pinger should still be connected)", pc)
	}
}

// TestClientCapturesServerEpoch: over real loopback the client captures the
// server's epoch from the extended welcome and stays connected (its heartbeat
// + the server's heartbeat keep both read deadlines alive). Deterministic.
func TestClientCapturesServerEpoch(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 20 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	client := NewClient("epoch")
	if err := client.Connect(addr); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	if got, want := client.ServerEpoch(), srv.epoch; got != want || got == 0 {
		t.Fatalf("ServerEpoch = %d, want %d (non-zero)", got, want)
	}

	// Well past several heartbeat intervals the client must still be up.
	time.Sleep(150 * time.Millisecond)
	if !client.IsConnected() {
		t.Fatal("client dropped despite heartbeats keeping the read deadline alive")
	}
	if pc := srv.PlayerCount(); pc != 1 {
		t.Fatalf("PlayerCount = %d, want 1", pc)
	}
}

// TestClientMeasuresRTT: the real client's heartbeat elicits pongs from which
// it derives a POSITIVE RTT. Loopback round-trips (~µs) are below the
// monotonic-clock granularity (~1ms on Windows) and would flakily read as 0,
// so the echo server here delays each pong past that granularity, making the
// measured RTT deterministically > 0.
func TestClientMeasuresRTT(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 20 * time.Millisecond
	clientReadTimeout = 2 * time.Second

	const epoch uint64 = 0xABCDEF0123456789
	const pongDelay = 5 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Hand-rolled echo server: welcome then delayed pong echoes.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		join, err := ReadMessage(conn)
		if err != nil || join.Type != MsgJoin {
			return
		}
		if err := WriteMessage(conn, MsgWelcome, WelcomeMessage(1, epoch)); err != nil {
			return
		}
		for {
			msg, err := ReadMessage(conn)
			if err != nil {
				return
			}
			if msg.Type == MsgPing {
				time.Sleep(pongDelay) // push RTT above clock granularity
				if err := WriteMessage(conn, MsgPong, msg.Data); err != nil {
					return
				}
			}
		}
	}()

	client := NewClient("rtt")
	if err := client.Connect(ln.Addr().String()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	if got := client.ServerEpoch(); got != epoch {
		t.Fatalf("ServerEpoch = %#x, want %#x", got, epoch)
	}
	if !waitFor(2*time.Second, func() bool { return client.LastRTT() > 0 }) {
		t.Fatalf("client never measured a positive RTT (connected=%v, rtt=%v)",
			client.IsConnected(), client.LastRTT())
	}
	if rtt := client.LastRTT(); rtt < pongDelay/2 {
		t.Errorf("measured RTT %v implausibly small for a %v echo delay", rtt, pongDelay)
	}
}

// TestServerRestartChangesEpoch: two Server instances have distinct epochs, so
// a client reconnecting to a fresh instance can detect the restart.
func TestServerRestartChangesEpoch(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 50 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv1, addr1 := mustListen(t)
	c1 := NewClient("a")
	if err := c1.Connect(addr1); err != nil {
		t.Fatalf("connect 1: %v", err)
	}
	epoch1 := c1.ServerEpoch()
	c1.Disconnect()
	srv1.Stop()

	// A second instance: its boot epoch must differ (so a reconnect across a
	// restart is detectable). Guard the unlikely same-nanos collision.
	time.Sleep(2 * time.Millisecond)
	srv2, addr2 := mustListen(t)
	defer srv2.Stop()
	c2 := NewClient("a")
	if err := c2.Connect(addr2); err != nil {
		t.Fatalf("connect 2: %v", err)
	}
	defer c2.Disconnect()
	epoch2 := c2.ServerEpoch()

	if epoch1 == 0 || epoch2 == 0 {
		t.Fatalf("epochs must be non-zero: %d, %d", epoch1, epoch2)
	}
	if epoch1 == epoch2 {
		t.Fatalf("server restart did not change epoch (%d == %d)", epoch1, epoch2)
	}
}
