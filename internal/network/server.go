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
	nextID   byte // where allocIDLocked starts its search for a free ID
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
	helloTimeout   time.Duration
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
		helloTimeout:   helloTimeout,
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

	// Version handshake (handshake.go): a current client sends MsgHello
	// right behind its join. A client that doesn't predates the handshake,
	// and is told to upgrade instead of being admitted to a session it
	// can't keep (it never answers heartbeats).
	if s.helloTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(s.helloTimeout))
	}
	hello, err := ReadMessage(conn)
	if err != nil || hello.Type != MsgHello {
		if isTimeout(err) {
			s.refuseLegacy(conn, name)
		}
		return
	}
	if version, err := ParseHello(hello.Data); err != nil || version != ProtocolVersion {
		s.refuse(conn, name, RejectVersion, versionMismatchReason(version))
		return
	}
	// Reject rather than clean up a bad name: names are matched for
	// self-filtering, so a silently altered name would break it.
	if err := ValidatePlayerName(name); err != nil {
		s.refuse(conn, name, RejectBadName, "invalid player name: "+err.Error())
		return
	}

	// Assign an ID. Lock the new player's SendMu BEFORE it becomes visible
	// in s.players and hold it until the welcome is written: from the moment
	// it is in the map, other players' relays (broadcastExcept) can write to
	// this conn, and the client must read its welcome first.
	player := &Player{Name: name, Conn: conn}
	player.SendMu.Lock()
	s.mu.Lock()
	id, ok := s.allocIDLocked()
	if ok {
		player.ID = id
		s.players[id] = player
	}
	s.mu.Unlock()
	if !ok {
		player.SendMu.Unlock()
		s.refuse(conn, name, RejectServerFull, "server full: all 255 player IDs are in use")
		return
	}

	// Send welcome with assigned ID + the server epoch (for restart
	// detection) + the protocol version. Old readers take the fields they
	// know from the front and ignore the rest.
	s.writePlayerLocked(player, MsgWelcome, WelcomeMessage(id, s.epoch))
	player.SendMu.Unlock()

	s.log(fmt.Sprintf("%s (ID:%d) connected", name, id))

	// Broadcast player list to everyone
	s.broadcastPlayerList()

	// Per-player heartbeat: ping every heartbeatInterval so a quiet-but-
	// live client's read deadline stays alive, and so we'd notice a dead
	// peer even if it never sends. Stopped when handlePlayer returns.
	done := make(chan struct{})
	defer close(done)
	go s.heartbeatLoop(player, done)

	// Read loop
	poseWarned := false // log a stream of bad pose frames once, not per frame
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
			// Relay to every OTHER player, stamping the authoritative sender
			// name into a structured [nameLen][name][text] payload (a client
			// can't forge another player's name). broadcastExcept excludes the
			// sender's own connection and retires any writer that fails (#39).
			// Oversize text is dropped here so the relay stays in bounds.
			if len(msg.Data) > MaxChatLen {
				s.log(fmt.Sprintf("dropped a %d-byte chat message from %s (max %d)", len(msg.Data), name, MaxChatLen))
				break
			}
			text := string(msg.Data)
			s.broadcastExcept(id, MsgChat, ChatRelayMessage(name, text))
			s.log(fmt.Sprintf("[Chat] %s: %s", name, text))

		case MsgPose:
			// Reuse the sender's payload, prepended with their ID.
			// Server doesn't decode the matrices; it's just bytes.
			// Face state (optional trailer) passes through untouched.
			// DeserializePose rejects joint counts over MaxPoseJoints and
			// face trailers over MaxFaceBytes, which keeps the relay small.
			joints, matrices, face, err := DeserializePose(msg.Data)
			if err != nil {
				if !poseWarned {
					poseWarned = true
					s.log(fmt.Sprintf("dropping bad pose frames from %s: %v", name, err))
				}
				break
			}
			if relay := PoseRelayMessage(id, joints, matrices, face); relay != nil {
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

	// Player disconnected. IDs are unique among live players, so the entry
	// is ours; the check just keeps teardown from ever evicting another.
	s.mu.Lock()
	if s.players[id] == player {
		delete(s.players, id)
	}
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
	return s.writePlayerLocked(p, msgType, data)
}

// writePlayerLocked is writePlayer for a caller that already holds p.SendMu.
func (s *Server) writePlayerLocked(p *Player, msgType byte, data []byte) error {
	if s.writeTimeout > 0 {
		p.Conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
	return WriteMessage(p.Conn, msgType, data)
}

// allocIDLocked returns a free player ID, searching on from s.nextID so IDs
// are reused as late as possible. IDs are 1..255: 0 is never assigned (it
// marks the welcome of a refused pre-handshake client, see refuseLegacy). ok
// is false when all 255 are taken. The caller holds s.mu.
func (s *Server) allocIDLocked() (id byte, ok bool) {
	for i := 0; i < 256; i++ {
		id = s.nextID
		s.nextID++
		if id == 0 {
			continue
		}
		if _, taken := s.players[id]; !taken {
			return id, true
		}
	}
	return 0, false
}

func (s *Server) broadcastExcept(excludeID byte, msgType byte, data []byte) {
	// Ingress caps (handlePlayer) keep every relay far below MaxPayload.
	// Should one ever exceed it anyway, drop it here: WriteMessage would
	// fail for every recipient, and failed writers are retired below, so
	// one sender's bad frame would disconnect everyone else.
	if len(data) > MaxPayload {
		s.log(fmt.Sprintf("dropped a %d-byte %q relay (max %d)", len(data), msgType, MaxPayload))
		return
	}
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
	entries := make([]PlayerEntry, 0, len(s.players))
	for _, p := range s.players {
		entries = append(entries, PlayerEntry{ID: p.ID, Name: p.Name})
	}
	data, err := PlayerListMessage(entries)
	if err != nil {
		// Unreachable: IDs are 1..255 and names are capped at join.
		s.mu.RUnlock()
		s.log(fmt.Sprintf("not sending player list: %v", err))
		return
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
