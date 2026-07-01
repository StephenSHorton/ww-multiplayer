package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

// Player represents a connected player on the server.
type Player struct {
	ID       byte
	Name     string
	Conn     net.Conn
	Position *PlayerPosition
	// SendMu serializes writes to Conn. Without it, concurrent
	// broadcastExcept calls (one per sender goroutine) interleave bytes
	// on the same TCP socket — receivers then see framed-message headers
	// whose bodies are actually the start of another message. Latent
	// before N=2 because a single broadcaster produced no concurrent
	// writes; two broadcasters + the player-list broadcaster guarantee
	// the race.
	SendMu sync.Mutex
}

// Server manages player connections and relays position data.
type Server struct {
	listener net.Listener
	players  map[byte]*Player
	nextID   byte
	mu       sync.RWMutex
	port     int
	// epoch is constant for the life of this Server instance (its boot-time
	// nanos). It rides along in every welcome so a reconnecting client can
	// tell that the server process restarted and trigger a full resync.
	epoch uint64
	// Timing captured at construction so the accept/handle/heartbeat
	// goroutines never read the package-level tunables directly (tests
	// retune those). Static in production, so capturing once is lossless.
	heartbeatEvery time.Duration
	readTimeout    time.Duration
	writeTimeout   time.Duration
	OnLog          func(string) // callback for log messages
}

// NewServer creates a server on the given port.
func NewServer(port int) *Server {
	return &Server{
		players:        make(map[byte]*Player),
		nextID:         1,
		port:           port,
		epoch:          uint64(time.Now().UnixNano()),
		heartbeatEvery: heartbeatInterval,
		readTimeout:    serverReadTimeout,
		writeTimeout:   writeTimeout,
	}
}

func (s *Server) log(msg string) {
	if s.OnLog != nil {
		s.OnLog(msg)
	}
}

// Start begins listening for connections.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln
	s.log(fmt.Sprintf("Server listening on port %d", s.port))

	go s.acceptLoop()
	return nil
}

// Stop shuts down the server.
func (s *Server) Stop() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for _, p := range s.players {
		p.Conn.Close()
	}
	s.mu.Unlock()
}

// Addr returns the listener's bound address (nil before Start). Handy for
// tests that listen on :0 and need the OS-assigned port.
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// PlayerCount returns the number of connected players.
func (s *Server) PlayerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.players)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handlePlayer(conn)
	}
}

func (s *Server) handlePlayer(conn net.Conn) {
	defer conn.Close()

	// s.readTimeout was captured at construction, so this goroutine never
	// reads the package var (tests retune it). Wait for the join message
	// (deadline-bounded so a connection that never sends a join doesn't pin
	// a goroutine forever).
	readTimeout := s.readTimeout
	if readTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
	}
	msg, err := ReadMessage(conn)
	if err != nil || msg.Type != MsgJoin {
		return
	}

	name := string(msg.Data)

	// Assign ID
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	player := &Player{
		ID:   id,
		Name: name,
		Conn: conn,
	}
	s.players[id] = player
	s.mu.Unlock()

	s.log(fmt.Sprintf("%s (ID:%d) connected", name, id))

	// Send welcome with assigned ID + the server epoch (for restart
	// detection). Old clients read Data[0] and ignore the 8-byte tail.
	// The player is already in s.players, so this write MUST hold SendMu:
	// another player's broadcastExcept pose relay could otherwise interleave
	// bytes on this conn and corrupt the welcome. writePlayer does that.
	s.writePlayer(player, MsgWelcome, WelcomeMessage(id, s.epoch))

	// Broadcast player list to everyone
	s.broadcastPlayerList()

	// Per-player heartbeat: ping every heartbeatInterval so a quiet-but-
	// live client's read deadline stays alive, and so we'd notice a dead
	// peer even if it never sends. Stopped when handlePlayer returns.
	done := make(chan struct{})
	defer close(done)
	go s.heartbeatLoop(player, done)

	// Read loop
	for {
		// Reset the read deadline each iteration. A net.Error Timeout from
		// an ungracefully-gone peer surfaces as an ordinary read error →
		// the normal teardown (delete + MsgLeave) runs below.
		if readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(readTimeout))
		}
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}

		switch msg.Type {
		case MsgPosition:
			pos := DeserializePosition(msg.Data)
			if pos != nil {
				s.mu.Lock()
				player.Position = pos
				s.mu.Unlock()

				// Relay to all other players
				relayData := PositionMessage(id, pos)
				s.broadcastExcept(id, MsgPosition, relayData)
			}

		case MsgChat:
			// Prefix with player name and broadcast
			chatData := append([]byte(name+": "), msg.Data...)
			s.broadcastExcept(id, MsgChat, chatData)
			s.log(fmt.Sprintf("[Chat] %s: %s", name, string(msg.Data)))

		case MsgPose:
			// Reuse the sender's payload, prepended with their ID.
			// Server doesn't decode the matrices; it's just bytes.
			// Face state (optional trailer) passes through untouched.
			joints, matrices, face := DeserializePose(msg.Data)
			if matrices != nil {
				relay := PoseRelayMessage(id, joints, matrices, face)
				s.broadcastExcept(id, MsgPose, relay)
			}

		case MsgPing:
			// Echo the timestamp straight back so the client can compute
			// RTT. The read deadline was already reset above for this
			// successful read, so the ping also served as a keepalive.
			s.writePlayer(player, MsgPong, msg.Data)

		case MsgPong:
			// Reply to one of our heartbeat pings — liveness only. The
			// read deadline reset above is the whole point.
		}
	}

	// Player disconnected
	s.mu.Lock()
	delete(s.players, id)
	s.mu.Unlock()

	s.log(fmt.Sprintf("%s disconnected", name))
	s.broadcastExcept(id, MsgLeave, []byte{id})
	s.broadcastPlayerList()
}

// heartbeatLoop pings one player every heartbeatInterval until done is
// closed (handlePlayer returning). The client echoes each ping as a MsgPong,
// which resets the server's read deadline for that player — so a live client
// that's gone quiet (paused / menu) is kept alive instead of being reaped as
// an ungraceful disconnect. A write error means the socket is already gone;
// the read loop will observe the same and tear down.
func (s *Server) heartbeatLoop(p *Player, done chan struct{}) {
	t := time.NewTicker(s.heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			ts := make([]byte, 8)
			binary.BigEndian.PutUint64(ts, uint64(monotonicNanos()))
			if err := s.writePlayer(p, MsgPing, ts); err != nil {
				return
			}
		}
	}
}

// writePlayer sends one framed message to a player under its SendMu (so
// writers never interleave on the socket) and bounds the write with a
// deadline (so a stalled peer errors instead of wedging every caller — in
// particular broadcastExcept, which writes while holding s.mu.RLock).
func (s *Server) writePlayer(p *Player, msgType byte, data []byte) error {
	p.SendMu.Lock()
	defer p.SendMu.Unlock()
	if s.writeTimeout > 0 {
		p.Conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
	return WriteMessage(p.Conn, msgType, data)
}

func (s *Server) broadcastExcept(excludeID byte, msgType byte, data []byte) {
	s.mu.RLock()
	var failed []*Player
	for _, p := range s.players {
		if p.ID != excludeID {
			if err := s.writePlayer(p, msgType, data); err != nil {
				failed = append(failed, p)
			}
		}
	}
	s.mu.RUnlock()

	s.retirePlayers(failed)
}

func (s *Server) broadcastPlayerList() {
	s.mu.RLock()

	// Format: [count:1][id:1][nameLen:1][name:variable]...
	var data []byte
	data = append(data, byte(len(s.players)))
	for _, p := range s.players {
		data = append(data, p.ID)
		data = append(data, byte(len(p.Name)))
		data = append(data, []byte(p.Name)...)
	}

	var failed []*Player
	for _, p := range s.players {
		if err := s.writePlayer(p, MsgPlayerList, data); err != nil {
			failed = append(failed, p)
		}
	}
	s.mu.RUnlock()

	s.retirePlayers(failed)
}

// retirePlayers closes the connection of every player whose write just
// failed (e.g. writePlayer's deadline tripped on a stalled peer). Closing
// unblocks that player's handlePlayer read loop, which runs the normal
// teardown (delete from s.players + MsgLeave broadcast) — so a stalled
// client is reaped immediately instead of limping along half-written until
// its ~15s read deadline expires. Must be called with s.mu NOT held: it may
// be invoked from broadcastExcept/broadcastPlayerList right after they
// release their RLock, and handlePlayer's teardown below needs s.mu.Lock().
func (s *Server) retirePlayers(failed []*Player) {
	for _, p := range failed {
		p.Conn.Close()
	}
}
