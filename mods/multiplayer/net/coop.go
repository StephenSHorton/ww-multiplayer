package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Co-op connections (protocol 2, docs/08-coop-design.md §4.4).
//
// The co-op pipeline (coop-sync) opens a connection of its own next to a
// player's two: MsgJoin(name), then a MsgHello with RoleCoop. The server
// welcomes it like a player (MsgWelcome with an ID, the epoch and the
// version), but the ID comes from a separate co-op ID space and the
// connection never enters s.players: it is not in the roster, PlayerCount
// or the 255-player limit, and it gets no position, pose, chat, leave or
// player-list relay. It keeps the heartbeat both ways. Its MsgCoop frames go
// to the server's CoopHandler, and the handler answers through
// CoopConn.Send. Any other frame on it is dropped.
//
// The network layer gives the 'O' payload no meaning: package
// mods/coop/wire encodes and validates it.

// coopSendQueue is how many outbound 'O' payloads a co-op connection may
// have waiting for its writer. Send never blocks the handler: a peer that
// falls this far behind is disconnected instead, and its replica resyncs on
// reconnect (CO_HELLO, then WORLD both ways).
const coopSendQueue = 256

// coopDropPoll is how often RunCoop checks whether its connection dropped.
const coopDropPoll = 50 * time.Millisecond

// coopHoldTimeout bounds how long a co-op hello waits for SetCoop while
// the lobby expects co-op (handleCoop). It stays under the client's
// default handshake read deadline (clientReadTimeout, 15 s), which the held
// client is waiting out for its welcome, so the hold ends with our close, a
// transient error the client retries, rather than with its own timeout
// (TestCoopHoldUnderClientReadTimeout pins it). A user who sets
// WW_READ_TIMEOUT_SECS under 10 s ends the hold on the client side instead;
// that is a transient error too, so the client still retries. A package
// var so tests can shorten it; NewServer captures it.
var coopHoldTimeout = 10 * time.Second

// coopOffReason is the MsgReject reason for a co-op hello to a server that
// has no CoopHandler.
const coopOffReason = "this host is not running co-op (the host starts it with --coop); pose sync still works"

// Errors from CoopConn.Send.
var (
	// ErrCoopClosed: the connection is gone. The handler's Detach for it
	// is on its way, if it has not run already.
	ErrCoopClosed = errors.New("co-op connection closed")
	// ErrCoopBacklog: coopSendQueue payloads were already waiting, so
	// the connection was closed rather than let the handler block or a
	// frame be lost.
	ErrCoopBacklog = errors.New("co-op connection too far behind; disconnected")
)

// CoopHandler is the server side of co-op: the CoopHub, in package
// mods/coop/hub. The server calls it from each co-op connection's own
// goroutine, so calls for different connections can run concurrently; calls
// for one connection never overlap and come in order:
//
//	Attach, then Frame per inbound MsgCoop in arrival order, then Detach
//
// Detach is called exactly once for every Attach that returned nil, after
// the connection is closed and after its last Frame, also when the server
// stops. The handler must not block for long in any of them: the
// connection's heartbeat replies wait behind a Frame.
type CoopHandler interface {
	// Attach is called for a new co-op connection before its welcome is
	// written. A non-nil error refuses the connection (MsgReject with
	// RejectRole and the error as the reason); Detach is not called.
	// Payloads Send queues during Attach go out right after the welcome.
	Attach(c *CoopConn) error
	// Frame delivers one inbound MsgCoop payload. The payload is the
	// handler's to keep.
	Frame(c *CoopConn, payload []byte)
	// Detach ends c. Send on c fails from now on.
	Detach(c *CoopConn)
}

// CoopConn is the server's side of one co-op connection. Its methods are
// safe for concurrent use.
type CoopConn struct {
	id   byte
	name string
	caps uint16
	conn net.Conn
	srv  *Server

	sendMu    sync.Mutex    // serializes writes to conn: the queue writer, heartbeat pings, pongs
	out       chan []byte   // queued 'O' payloads, drained by writeLoop
	done      chan struct{} // closed by Close
	closeOnce sync.Once
}

// ID is the connection's co-op ID, 1..255, unique among live co-op
// connections and unrelated to player IDs. The client reads it from its
// welcome, so it can serve as a GAINS origin.
func (c *CoopConn) ID() byte { return c.id }

// Name is the player name the connection joined with (validated as any
// player name is).
func (c *CoopConn) Name() string { return c.name }

// Caps is the caps word of the connection's hello, as sent.
func (c *CoopConn) Caps() uint16 { return c.caps }

// RemoteAddr is the peer's address.
func (c *CoopConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Send queues one MsgCoop payload for the peer and returns without waiting
// for the write. The payload is written as is, later, so the caller must
// not modify it after Send. A payload over MaxPayload is refused with
// ErrFrameTooLarge (and nothing else happens); a full queue closes the
// connection (ErrCoopBacklog); a closed connection returns ErrCoopClosed.
func (c *CoopConn) Send(payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: type %q, %d bytes (max %d)", ErrFrameTooLarge, MsgCoop, len(payload), MaxPayload)
	}
	select {
	case <-c.done:
		return ErrCoopClosed
	default:
	}
	select {
	case c.out <- payload:
		return nil
	default:
		c.srv.log(fmt.Sprintf("%s (co-op %d): %d co-op frames waiting; disconnecting", c.name, c.id, coopSendQueue))
		c.Close()
		return ErrCoopBacklog
	}
}

// Close disconnects the peer. Idempotent. The handler's Detach follows.
func (c *CoopConn) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// write sends one frame under sendMu, bounded by the server's write
// deadline.
func (c *CoopConn) write(msgType byte, data []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.srv.writeConn(c.conn, msgType, data)
}

// writeLoop writes queued payloads until the connection closes. A failed
// write closes it, which ends the read loop in handleCoop.
func (c *CoopConn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case p := <-c.out:
			if err := c.write(MsgCoop, p); err != nil {
				c.Close()
				return
			}
		}
	}
}

// SetCoop sets (or, with nil, clears) the server's CoopHandler, before or
// after Start. Co-op hellos already held for it (handleCoop) go on to the
// new handler. Connections the old handler had are not moved.
func (s *Server) SetCoop(h CoopHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Coop = h
	if h != nil {
		select {
		case <-s.coopSet:
		default:
			close(s.coopSet)
		}
	} else {
		select {
		case <-s.coopSet:
			s.coopSet = make(chan struct{})
		default:
		}
	}
}

// coopHandler returns the CoopHandler, and a channel closed once one is
// set.
func (s *Server) coopHandler() (CoopHandler, <-chan struct{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Coop, s.coopSet
}

// CoopCount returns the number of live co-op connections. They are never
// in PlayerCount.
func (s *Server) CoopCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.coops)
}

// allocCoopIDLocked is allocIDLocked for the co-op ID space. The caller
// holds s.mu.
func (s *Server) allocCoopIDLocked() (id byte, ok bool) {
	return allocFrom(&s.nextCoopID, func(id byte) bool { _, taken := s.coops[id]; return taken })
}

// handleCoop runs a co-op connection whose join and hello handlePlayer has
// read and checked (version, name). It returns when the connection ends;
// handlePlayer closes conn.
func (s *Server) handleCoop(conn net.Conn, name string, caps uint16) {
	h, set := s.coopHandler()
	if h == nil && s.lobby.ExpectsCoop() {
		// The lobby's host chose co-op but its world isn't open yet (the
		// app sets the hub just before GO). Refusing now would be fatal
		// for the client (RejectRole stops RunCoop), so a CLI join --coop
		// that arrived early would never get co-op. Hold the hello for
		// the hub instead; if it doesn't come in time, close without a
		// reject so the client backs off and tries again.
		t := time.NewTimer(s.coopHold)
		select {
		case <-set:
			h, _ = s.coopHandler()
		case <-t.C:
		case <-s.stopped:
			// The server is stopping: close without a reject, so the
			// client retries (against a restarted server, say).
			t.Stop()
			return
		}
		t.Stop()
		if h == nil {
			if s.lobby.ExpectsCoop() {
				s.log(fmt.Sprintf("%s (co-op): the host's co-op world isn't open yet; the client will retry", name))
				return
			}
		}
	}
	if h == nil {
		s.refuse(conn, name, RejectRole, coopOffReason)
		return
	}
	c := &CoopConn{
		name: name,
		caps: caps,
		conn: conn,
		srv:  s,
		out:  make(chan []byte, coopSendQueue),
		done: make(chan struct{}),
	}
	s.mu.Lock()
	stopping := s.stopping()
	var id byte
	var ok bool
	if !stopping {
		id, ok = s.allocCoopIDLocked()
	}
	if ok {
		c.id = id
		s.coops[id] = c
	}
	s.mu.Unlock()
	if stopping {
		// Stop has closed the co-op connections already, and its
		// caller stops the handler next: close without a reject (which
		// would be fatal), so the client retries (see Stop).
		return
	}
	if !ok {
		s.refuse(conn, name, RejectServerFull, "server full: all 255 co-op connection IDs are in use")
		return
	}
	// The ID is freed only after Detach, so a new connection can't reuse
	// it while the handler still holds the old one.
	defer func() {
		s.mu.Lock()
		if s.coops[id] == c {
			delete(s.coops, id)
		}
		s.mu.Unlock()
	}()

	// A bug in the handler costs this connection, not the host process
	// (which also runs the TUI and the pose pipeline). The deferred Close
	// and Detach below still run while a panic unwinds, before this.
	defer func() {
		if r := recover(); r != nil {
			c.Close()
			s.log(fmt.Sprintf("%s (co-op %d): internal error in the co-op handler: %v; disconnected", name, id, r))
		}
	}()
	if err := h.Attach(c); err != nil {
		if s.stopping() {
			// The handler refused because it is stopping with the
			// server: not the client's fault, so no (fatal) reject.
			c.Close()
			return
		}
		s.refuse(conn, name, RejectRole, "co-op: "+err.Error())
		c.Close() // Send fails from now on, should the handler have kept c
		return
	}
	defer h.Detach(c)
	defer c.Close()

	// The welcome goes first; the queue writer starts after it, so what
	// Attach queued follows it.
	if err := c.write(MsgWelcome, WelcomeMessage(id, s.epoch)); err != nil {
		return
	}
	s.log(fmt.Sprintf("%s (co-op %d) connected", name, id))
	go c.writeLoop()
	go s.pingLoop(c.write, c.done)

	readTimeout := s.readTimeout
	for {
		if readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(readTimeout))
		}
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}
		switch msg.Type {
		case MsgCoop:
			h.Frame(c, msg.Data)
		case MsgPing:
			c.write(MsgPong, msg.Data)
		case MsgPong:
			// Liveness only, as on a player connection.

			// Player frames (position, pose, chat) are dropped: a co-op
			// connection takes part in no relay.
		}
	}
	s.log(fmt.Sprintf("%s (co-op %d) disconnected", name, id))
}

// NewCoopClient creates the client side of a co-op connection: a Client
// whose hello carries RoleCoop and caps. It takes part in no relay: it never
// receives positions, poses, chat or a roster, so GetRemotePlayers stays
// empty. Inbound MsgCoop payloads go to OnCoop; SendCoop sends one. Connect,
// ConnectContext, Disconnect, the heartbeat and ReconnectLoop behave as for
// a player, and RunCoop drives all of it.
func NewCoopClient(name string, caps uint16) *Client {
	c := NewClient(name)
	c.role = RoleCoop
	c.caps = caps
	return c
}

// SendCoop sends one MsgCoop payload on a co-op connection.
func (c *Client) SendCoop(payload []byte) error {
	if c.role != RoleCoop {
		return errors.New("not a co-op connection")
	}
	return c.send(MsgCoop, payload)
}

// RunCoop keeps c, a co-op client (NewCoopClient), connected to addr until
// ctx ends. It is the player connection's lifecycle unchanged: each attempt
// is c.ConnectContext (dial timeout, cancellable at any stage), reported
// through connLog when it is not nil, under ReconnectLoop's backoff, flap
// floor and fatal-error stop. A host without co-op refuses the hello with
// RejectRole, which is fatal, so RunCoop then returns that error at once.
//
// session runs once per live connection, with a ctx that ends when the
// connection drops or the outer ctx ends, and with send, which writes one
// 'O' payload (c.SendCoop). Inbound 'O' payloads reach c.OnCoop from the
// read loop meanwhile. When session returns, the connection is closed and
// dialled again, so a session that has nothing to do should wait on ctx; it
// must return promptly once ctx ends. RunCoop returns nil when ctx ends,
// else the fatal connect error.
func RunCoop(ctx context.Context, c *Client, addr string, connLog *ConnectLog, session func(ctx context.Context, send func(payload []byte) error) error) error {
	if c.role != RoleCoop {
		return errors.New("RunCoop needs a co-op client (NewCoopClient)")
	}
	connect := func(ctx context.Context) error {
		err := c.ConnectContext(ctx, addr)
		if connLog != nil {
			return connLog.Observe(ctx, err)
		}
		return err
	}
	live := func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- session(ctx, c.SendCoop) }()
		t := time.NewTicker(coopDropPoll)
		defer t.Stop()
		for {
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return <-done
			case <-t.C:
				if !c.IsConnected() {
					cancel()
					return <-done
				}
			}
		}
	}
	run := func(ctx context.Context) error {
		return RunSession(ctx, c.Disconnect, live)
	}
	return ReconnectLoop(ctx, connect, run, NewBackoff(), SleepCtx)
}
