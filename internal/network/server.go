package network

import (
	"fmt"
	"net"
	"sync"
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
	OnLog    func(string) // callback for log messages
}

// NewServer creates a server on the given port.
func NewServer(port int) *Server {
	return &Server{
		players: make(map[byte]*Player),
		nextID:  1,
		port:    port,
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

	// Wait for join message
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

	// Send welcome with assigned ID
	WriteMessage(conn, MsgWelcome, []byte{id})

	// Broadcast player list to everyone
	s.broadcastPlayerList()

	// Read loop
	for {
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
			joints, matrices := DeserializePose(msg.Data)
			if matrices != nil {
				relay := PoseRelayMessage(id, joints, matrices)
				s.broadcastExcept(id, MsgPose, relay)
			}
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

func (s *Server) broadcastExcept(excludeID byte, msgType byte, data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.players {
		if p.ID != excludeID {
			p.SendMu.Lock()
			WriteMessage(p.Conn, msgType, data)
			p.SendMu.Unlock()
		}
	}
}

func (s *Server) broadcastPlayerList() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Format: [count:1][id:1][nameLen:1][name:variable]...
	var data []byte
	data = append(data, byte(len(s.players)))
	for _, p := range s.players {
		data = append(data, p.ID)
		data = append(data, byte(len(p.Name)))
		data = append(data, []byte(p.Name)...)
	}

	for _, p := range s.players {
		p.SendMu.Lock()
		WriteMessage(p.Conn, MsgPlayerList, data)
		p.SendMu.Unlock()
	}
}
