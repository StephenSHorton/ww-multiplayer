package network

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// Lobby connections (docs/10-lobby.md). The desktop app holds one lobby
// connection (RoleLobby) from Connect until it quits. Like a co-op
// connection it never becomes a player: it is not in the roster,
// PlayerCount or the 255-player limit, and gets no position or pose
// relay. It carries MsgLobby sub-messages, chat both ways, and the
// heartbeat. Every server has a LobbyHub; with no lobby connection it is
// inert, and player and co-op connections behave exactly as before.

// lobbySendQueue is how many frames a lobby connection may have waiting for
// its writer; a peer that falls this far behind is disconnected (its slot
// is held, so it reconnects into it). STATE takes at most one place in it
// (lobbyConn.queueState) and chat relays are dropped rather than overflow
// it, so only a peer that stops reading gets there.
const lobbySendQueue = 64

// The rate a lobby connection may send MsgLobby and MsgChat frames at: a
// token bucket of lobbyRateBurst refilled at lobbyRatePerSec. The excess is
// dropped, with one NoticeSlowDown per run of drops. Every SET that changes
// something sends STATE to every member, so this bounds what one member
// can make the server send.
const (
	lobbyRatePerSec = 20
	lobbyRateBurst  = 40
)

// lobbyFrame is one queued frame; closeAfter closes the connection once it
// is written. state marks the place of a STATE, whose body the writer
// takes from the lobby when it gets there. droppable frames (chat relays)
// are dropped when the queue is full instead of closing the connection.
type lobbyFrame struct {
	msgType    byte
	data       []byte
	closeAfter bool
	state      bool
	droppable  bool
}

// lobbyConn is the server's side of one lobby connection.
type lobbyConn struct {
	handle   int  // the lobby machine's name for it
	id       byte // the ID its welcome carried, 1..255 among live lobby connections
	name     string
	loopback bool
	conn     net.Conn
	srv      *Server
	hub      *LobbyHub

	sendMu       sync.Mutex
	out          chan lobbyFrame
	done         chan struct{}
	closeOnce    sync.Once
	closing      atomic.Bool // a close was queued: inbound frames are ignored
	statePending atomic.Bool // a STATE place is in out and not written yet
}

// queueState queues a STATE unless one is waiting already: STATE is the
// whole lobby, so the waiting one, filled in when it is written, carries
// this change too. A burst of changes costs a slow reader one frame.
func (c *lobbyConn) queueState() {
	if c.statePending.CompareAndSwap(false, true) {
		c.enqueue(lobbyFrame{msgType: MsgLobby, state: true})
	}
}

// tokenBucket is a simple rate limiter; not safe for concurrent use.
type tokenBucket struct {
	rate, burst, tokens float64
	last                time.Time
}

func newTokenBucket(rate, burst float64, now time.Time) *tokenBucket {
	return &tokenBucket{rate: rate, burst: burst, tokens: burst, last: now}
}

// take takes one token at now; false when there is none.
func (b *tokenBucket) take(now time.Time) bool {
	if d := now.Sub(b.last); d > 0 {
		b.tokens = min(b.burst, b.tokens+d.Seconds()*b.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (c *lobbyConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

func (c *lobbyConn) write(msgType byte, data []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.srv.writeConn(c.conn, msgType, data)
}

// enqueue queues one frame without blocking; a full queue closes the
// connection.
func (c *lobbyConn) enqueue(f lobbyFrame) {
	if c.closing.Load() {
		return
	}
	if f.closeAfter {
		c.closing.Store(true)
	}
	select {
	case <-c.done:
	case c.out <- f:
	default:
		if f.droppable {
			return
		}
		c.srv.log(fmt.Sprintf("%s (lobby): %d frames waiting; disconnecting", c.name, lobbySendQueue))
		c.close()
	}
}

func (c *lobbyConn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case f := <-c.out:
			if f.state {
				// Cleared first: a change from here on queues another
				// STATE, one before is in this one.
				c.statePending.Store(false)
				data, ok := c.hub.stateFrame()
				if !ok {
					continue
				}
				f.data = data
			}
			if err := c.write(f.msgType, f.data); err != nil {
				c.close()
				return
			}
			if f.closeAfter {
				// Let the frame reach the peer before the close (see
				// lingerClose): FIN, then the read loop drains until
				// the peer hangs up or lingerTimeout passes.
				if cw, ok := c.conn.(interface{ CloseWrite() error }); ok {
					cw.CloseWrite()
				}
				c.conn.SetReadDeadline(time.Now().Add(lingerTimeout))
				return
			}
		}
	}
}

// LobbyHub is the server's lobby: the members, their colors, ready flags,
// the host's co-op settings and the phase, in a lobbyMachine it
// serializes. Its methods are safe for concurrent use.
//
// Lock order: hub.mu, then Server.mu, then hub.colorMu. The server reads
// colorTab (a copy of the members' colors) under its own mu, so it never
// needs hub.mu.
type LobbyHub struct {
	srv *Server

	mu       sync.Mutex
	m        *lobbyMachine
	conns    map[int]*lobbyConn
	nextConn int
	nextID   byte

	// colorMu guards colorTab and kickTab, copies of the members' colors
	// and of the kicked names that the server reads under its own mu.
	colorMu  sync.RWMutex
	colorTab map[string]playercolor.Color
	kickTab  map[string]bool

	// The last STATE encoded, by rev (h.mu): every member's writer sends
	// the same bytes.
	stateRev  uint64
	stateData []byte

	now func() time.Time
	// local says whether a lobby connection comes from the host's own
	// machine (the first such member becomes the host). isLoopback in
	// production; tests, whose clients are all on loopback, pick by name.
	local func(addr net.Addr, name string) bool
}

func newLobbyHub(s *Server, hold time.Duration) *LobbyHub {
	return &LobbyHub{srv: s, m: newLobbyMachine(hold), conns: map[int]*lobbyConn{}, nextID: 1,
		colorTab: map[string]playercolor.Color{}, kickTab: map[string]bool{}, now: time.Now,
		local: func(addr net.Addr, _ string) bool { return isLoopback(addr) }}
}

// Lobby returns the server's lobby.
func (s *Server) Lobby() *LobbyHub { return s.lobby }

// LobbyCount returns the number of live lobby connections. They are never
// in PlayerCount.
func (s *Server) LobbyCount() int {
	h := s.lobby
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// State returns the lobby's current STATE.
func (h *LobbyHub) State() LobbyStateBody {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m.snapshot()
}

// ExpectsCoop reports whether the lobby's host chose co-op and the session
// is not in game yet: a co-op hello that arrives before SetCoop is then
// held instead of refused.
func (h *LobbyHub) ExpectsCoop() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m.expectsCoop()
}

// colorOf is rule 0 of pickColorLocked: the color of the member named name.
func (h *LobbyHub) colorOf(name string) (playercolor.Color, bool) {
	h.colorMu.RLock()
	defer h.colorMu.RUnlock()
	c, ok := h.colorTab[lobbyKey(name)]
	return c, ok
}

// otherColors are the members' colors but name's.
func (h *LobbyHub) otherColors(name string) []playercolor.Color {
	h.colorMu.RLock()
	defer h.colorMu.RUnlock()
	k := lobbyKey(name)
	var out []playercolor.Color
	for n, c := range h.colorTab {
		if n != k {
			out = append(out, c)
		}
	}
	return out
}

// isKicked reports whether the host kicked name (any case) from this
// session: its player and co-op connections are refused.
func (h *LobbyHub) isKicked(name string) bool {
	h.colorMu.RLock()
	defer h.colorMu.RUnlock()
	return h.kickTab[lobbyKey(name)]
}

// stateFrame is the MsgLobby payload of the lobby's current STATE.
func (h *LobbyHub) stateFrame() ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stateData != nil && h.stateRev == h.m.rev {
		return h.stateData, true
	}
	data, err := EncodeLobby(LobbyState, h.m.snapshot())
	if err != nil {
		h.srv.log(fmt.Sprintf("lobby: not sending STATE: %v", err))
		return nil, false
	}
	h.stateRev, h.stateData = h.m.rev, data
	return data, true
}

// publishTablesLocked copies the members' colors and the kicked names to
// the tables the server reads. The caller holds h.mu.
func (h *LobbyHub) publishTablesLocked() {
	tab, kicked := h.m.colors(), h.m.kickedNames()
	h.colorMu.Lock()
	h.colorTab, h.kickTab = tab, kicked
	h.colorMu.Unlock()
}

// dispatchLocked delivers outs and refreshes the color and kick tables.
// The caller holds h.mu; it returns the player names to kick, which the
// caller does after unlocking (kickPlayers takes Server.mu).
func (h *LobbyHub) dispatchLocked(outs []lobbyOut) (kicks []string) {
	h.publishTablesLocked()
	for _, o := range outs {
		if o.kickName != "" {
			kicks = append(kicks, o.kickName)
		}
		if o.sub == 0 {
			continue
		}
		if o.sub == LobbyState {
			// The writer fills in the current STATE (queueState).
			for handle, c := range h.conns {
				if (o.to == lobbyAll || o.to == handle) && h.m.memberByConn(handle) != nil {
					c.queueState()
				}
			}
			continue
		}
		data, err := EncodeLobby(o.sub, o.body)
		if err != nil {
			h.srv.log(fmt.Sprintf("lobby: not sending %s: %v", LobbySubName(o.sub), err))
			continue
		}
		f := lobbyFrame{msgType: MsgLobby, data: data, closeAfter: o.closeConn}
		if o.to == lobbyAll {
			for handle, c := range h.conns {
				if h.m.memberByConn(handle) != nil {
					c.enqueue(f)
				}
			}
		} else if c := h.conns[o.to]; c != nil {
			c.enqueue(f)
		}
	}
	return kicks
}

// run applies one input to the machine and delivers what it returns.
func (h *LobbyHub) run(input func(m *lobbyMachine) []lobbyOut) {
	h.mu.Lock()
	kicks := h.dispatchLocked(input(h.m))
	h.mu.Unlock()
	for _, n := range kicks {
		h.srv.kickPlayers(n)
	}
}

// runWithPlayers is run for an input that may pick or change a member's
// color (HELLO, SET). It holds Server.mu across the machine step and the
// color table's update, after refreshing the machine's player list from
// s.players under it. A player connection's color pick (pickColorLocked,
// under Server.mu) then sees either the member's new color or a list
// that already has the player, never neither, so the two can't take the
// same color at once.
func (h *LobbyHub) runWithPlayers(input func(m *lobbyMachine) []lobbyOut) {
	h.mu.Lock()
	h.srv.mu.RLock()
	outs := h.m.setPlayers(h.srv.lobbyPlayersLocked())
	outs = append(outs, input(h.m)...)
	h.publishTablesLocked()
	h.srv.mu.RUnlock()
	kicks := h.dispatchLocked(outs)
	h.mu.Unlock()
	for _, n := range kicks {
		h.srv.kickPlayers(n)
	}
}

// playersChanged gives the lobby the current player connections (a join,
// a leave, a first pose).
func (h *LobbyHub) playersChanged() {
	h.mu.Lock()
	if len(h.m.members) == 0 && len(h.conns) == 0 {
		// Nobody to tell; STATE is built from the current list at the
		// next HELLO.
		h.m.players = h.srv.lobbyPlayers()
		h.mu.Unlock()
		return
	}
	ps := h.srv.lobbyPlayers()
	kicks := h.dispatchLocked(h.m.setPlayers(ps))
	h.mu.Unlock()
	for _, n := range kicks {
		h.srv.kickPlayers(n)
	}
}

// relayChat sends a chat relay to every member's lobby connection but
// except's.
func (h *LobbyHub) relayChat(except int, relay []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for handle, c := range h.conns {
		if handle != except && h.m.memberByConn(handle) != nil {
			c.enqueue(lobbyFrame{msgType: MsgChat, data: relay, droppable: true})
		}
	}
}

func (h *LobbyHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		c.close()
	}
}

// lobbyPlayers is the player connections as the lobby sees them.
func (s *Server) lobbyPlayers() []lobbyPlayer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lobbyPlayersLocked()
}

// lobbyPlayersLocked is lobbyPlayers for a caller holding s.mu.
func (s *Server) lobbyPlayersLocked() []lobbyPlayer {
	ps := make([]lobbyPlayer, 0, len(s.players))
	for _, p := range s.players {
		ps = append(ps, lobbyPlayer{id: p.ID, name: p.Name, color: p.Color, posed: p.posed.Load()})
	}
	return ps
}

// kickPlayers closes every player connection named name (any case); their
// teardown broadcasts the leave as usual.
func (s *Server) kickPlayers(name string) {
	k := lobbyKey(name)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.players {
		if lobbyKey(p.Name) == k {
			p.Conn.Close()
		}
	}
}

// isLoopback reports whether addr is this machine.
func isLoopback(addr net.Addr) bool {
	ta, ok := addr.(*net.TCPAddr)
	return ok && ta.IP.IsLoopback()
}

// handleLobby runs a lobby connection whose join and hello handlePlayer has
// read and checked (version, name). It returns when the connection ends;
// handlePlayer closes conn.
func (s *Server) handleLobby(conn net.Conn, name string, caps uint16) {
	if caps&LobbyCapJSON == 0 {
		s.refuse(conn, name, RejectRole, "lobby connection without JSON bodies (caps bit 0); both players need the same ww-multiplayer version")
		return
	}
	h := s.lobby
	c := &lobbyConn{name: name, loopback: h.local(conn.RemoteAddr(), name), conn: conn, srv: s, hub: h,
		out: make(chan lobbyFrame, lobbySendQueue), done: make(chan struct{})}
	h.mu.Lock()
	id, ok := allocFrom(&h.nextID, func(id byte) bool {
		for _, o := range h.conns {
			if o.id == id {
				return true
			}
		}
		return false
	})
	if ok {
		h.nextConn++
		c.handle, c.id = h.nextConn, id
		h.conns[c.handle] = c
	}
	h.mu.Unlock()
	if !ok {
		s.refuse(conn, name, RejectServerFull, "server full: all 255 lobby connection IDs are in use")
		return
	}
	defer func() {
		h.mu.Lock()
		delete(h.conns, c.handle)
		h.mu.Unlock()
		h.run(func(m *lobbyMachine) []lobbyOut { return m.drop(c.handle, h.now()) })
		if hold := h.m.hold; hold > 0 {
			time.AfterFunc(hold, func() { h.run(func(m *lobbyMachine) []lobbyOut { return m.expire(h.now()) }) })
		}
	}()
	defer c.close()

	if err := c.write(MsgWelcome, WelcomeMessage(id, s.epoch)); err != nil {
		return
	}
	s.log(fmt.Sprintf("%s (lobby %d) connected", name, id))
	go c.writeLoop()
	go s.pingLoop(func(t byte, d []byte) error { c.enqueue(lobbyFrame{msgType: t, data: d}); return nil }, c.done)

	member := false
	readTimeout := s.readTimeout
	bucket := newTokenBucket(lobbyRatePerSec, lobbyRateBurst, time.Now())
	warned := false // a slow_down was sent for the current run of drops
	limited := func() bool {
		if bucket.take(time.Now()) {
			warned = false
			return false
		}
		if !warned {
			warned = true
			s.log(fmt.Sprintf("%s (lobby %d): over %d messages a second; dropping the excess", name, id, lobbyRatePerSec))
			h.run(func(m *lobbyMachine) []lobbyOut {
				return []lobbyOut{notice(c.handle, NoticeSlowDown, "too many lobby messages; some were dropped")}
			})
		}
		return true
	}
	for {
		if readTimeout > 0 && !c.closing.Load() {
			conn.SetReadDeadline(time.Now().Add(readTimeout))
		}
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}
		if c.closing.Load() {
			continue // draining after a refusal or a kick
		}
		switch msg.Type {
		case MsgLobby:
			if limited() {
				break
			}
			sub, body, err := ParseLobby(msg.Data)
			if err != nil {
				h.run(func(m *lobbyMachine) []lobbyOut {
					return []lobbyOut{notice(c.handle, NoticeBadMessage, err.Error())}
				})
				break
			}
			if sub == LobbyHello && !member {
				var hb LobbyHelloBody
				if DecodeLobby(body, &hb) != nil {
					hb = LobbyHelloBody{}
				}
				h.runWithPlayers(func(m *lobbyMachine) []lobbyOut {
					mb, outs := m.hello(c.handle, name, c.loopback, hb, h.now())
					member = mb != nil
					return outs
				})
				continue
			}
			if sub == LobbySet {
				h.runWithPlayers(func(m *lobbyMachine) []lobbyOut { return m.message(c.handle, sub, body) })
			} else {
				h.run(func(m *lobbyMachine) []lobbyOut { return m.message(c.handle, sub, body) })
			}
		case MsgChat:
			if !member || len(msg.Data) > MaxChatLen || limited() {
				break
			}
			relay := ChatRelayMessage(name, string(msg.Data))
			s.broadcastExcept(0, MsgChat, relay)
			h.relayChat(c.handle, relay)
			s.log(fmt.Sprintf("[Chat] %s: %s", name, msg.Data))
		case MsgPing:
			c.enqueue(lobbyFrame{msgType: MsgPong, data: msg.Data})
		case MsgPong:
			if ts, ok := ParsePingTimestamp(msg.Data); ok {
				if rtt, ok := measuredRTT(monotonicNanos(), ts); ok {
					h.mu.Lock()
					h.m.setRTT(c.handle, int((time.Duration(rtt)+time.Millisecond/2)/time.Millisecond))
					h.mu.Unlock()
				}
			}
		}
	}
	s.log(fmt.Sprintf("%s (lobby %d) disconnected", name, id))
}
