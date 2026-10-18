package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// Player represents a connected player on the server.
type Player struct {
	ID       byte
	Name     string
	Conn     net.Conn
	Position *PlayerPosition
	// Color is the tunic color the server assigned this player's name
	// (docs/09-player-colors.md). Set at join, before the player is
	// visible in s.players, and never changed after.
	Color playercolor.Color
	// SendMu serializes writes to Conn. Without it, concurrent
	// broadcastExcept calls (one per sender goroutine) interleave bytes
	// on the same TCP socket — receivers then see framed-message headers
	// whose bodies are actually the start of another message. Latent
	// before N=2 because a single broadcaster produced no concurrent
	// writes; two broadcasters + the player-list broadcaster guarantee
	// the race.
	SendMu sync.Mutex
	// posed is set by the first valid pose on the connection: the lobby
	// lists a CLI player's Dolphin as in game from then on.
	posed atomic.Bool
}

// Server manages player connections and relays position data.
type Server struct {
	listener net.Listener
	players  map[byte]*Player
	nextID   byte // where allocIDLocked starts its search for a free ID
	// colorByName remembers the color each player name last had, so a
	// player who reconnects without a preference keeps it (guarded by mu).
	colorByName map[string]playercolor.Color
	// coops are the co-op connections (RoleCoop), by co-op ID. They are
	// never in players: no relay, roster or player count sees them.
	coops      map[byte]*CoopConn
	nextCoopID byte
	// enemyHost and enemyClients are the enemy-sync connections
	// (enemy.go), by enemy ID (one space for both). Never in players.
	enemyHost    *enemyConn
	enemyClients map[byte]*enemyConn
	nextEnemyID  byte
	mu           sync.RWMutex
	port         int
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
	coopHold       time.Duration // coopHoldTimeout
	OnLog          func(string)  // callback for log messages

	// ListenHost is the address Start listens on; "" means every
	// interface, which host needs so LAN peers can reach it. A server on
	// port 0 (an OS-assigned port, which only tests use: the CLI takes
	// 1-65535) with no ListenHost listens on loopback alone, so test runs
	// never open a port on the network (and Windows never asks whether
	// the test binary may). Set it before Start.
	ListenHost string

	// Coop receives the co-op connections (coop.go). Set it before Start,
	// or later with SetCoop. Nil means co-op is off: a co-op hello is then
	// refused with RejectRole (or, while the lobby expects co-op, held for
	// up to coopHoldTimeout; see handleCoop), and player connections
	// behave exactly as without it.
	Coop CoopHandler
	// coopSet is closed when a non-nil CoopHandler is set (guarded by mu).
	coopSet chan struct{}

	// mods is the host's Mods (SetMods): a player hello or lobby HELLO
	// with a different hash is refused (guarded by mu).
	mods Mods

	// lobby is the pre-game lobby (lobby.go), inert until a lobby
	// connection arrives.
	lobby *LobbyHub

	// stopped is closed by Stop, first and under mu, for what waits
	// outside the connection maps (a co-op hello held for SetCoop) and for
	// a connection that registers in them after Stop closed theirs
	// (stopping).
	stopped  chan struct{}
	stopOnce sync.Once
}

// NewServer creates a server on the given port.
func NewServer(port int) *Server {
	s := &Server{
		players:        make(map[byte]*Player),
		nextID:         1,
		colorByName:    make(map[string]playercolor.Color),
		coops:          make(map[byte]*CoopConn),
		nextCoopID:     1,
		enemyClients:   make(map[byte]*enemyConn),
		nextEnemyID:    1,
		port:           port,
		epoch:          uint64(time.Now().UnixNano()),
		heartbeatEvery: heartbeatInterval,
		readTimeout:    serverReadTimeout,
		writeTimeout:   writeTimeout,
		helloTimeout:   helloTimeout,
		coopHold:       coopHoldTimeout,
		coopSet:        make(chan struct{}),
		stopped:        make(chan struct{}),
	}
	s.lobby = newLobbyHub(s, LobbyHoldTimeout)
	return s
}

func (s *Server) log(msg string) {
	if s.OnLog != nil {
		s.OnLog(msg)
	}
}

// Start begins listening for connections.
func (s *Server) Start() error {
	host := s.ListenHost
	if host == "" && s.port == 0 {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(s.port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln
	s.log(fmt.Sprintf("Server listening on port %d", s.port))

	go s.acceptLoop()
	return nil
}

// Stop shuts down the server.
//
// A connection the listener accepted just before it closed can still be in
// its handshake: it checks stopping when it registers (under mu, or the
// lobby's mu for a lobby connection) and closes without a welcome or a
// reject, so its client retries, against a restarted server say. Without
// that it would register after the loops below, and either live on with a
// stopped server or, refused by the co-op hub Stop's caller stops next,
// take a fatal RejectRole that ends the client's co-op for good.
func (s *Server) Stop() {
	s.mu.Lock()
	s.stopOnce.Do(func() { close(s.stopped) })
	s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for _, p := range s.players {
		p.Conn.Close()
	}
	for _, c := range s.coops {
		c.Close()
	}
	s.closeEnemyLocked()
	s.mu.Unlock()
	s.lobby.closeAll()
}

// stopping reports whether Stop has begun.
func (s *Server) stopping() bool {
	select {
	case <-s.stopped:
		return true
	default:
		return false
	}
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
	_, role, caps, err := ParseHelloRole(hello.Data)
	if err != nil {
		s.refuse(conn, name, RejectVersion, "malformed "+err.Error())
		return
	}
	// Reject rather than clean up a bad name: names are matched for
	// self-filtering, so a silently altered name would break it.
	if err := ValidatePlayerName(name); err != nil {
		s.refuse(conn, name, RejectBadName, "invalid player name: "+err.Error())
		return
	}
	if (role == RolePlayer || role == RoleCoop) && s.lobby.isKicked(name) {
		// The lobby's host kicked this name: its player and co-op
		// connections stay out for the session. A reject is fatal, so a
		// reconnecting CLI join stops instead of coming back. (A lobby
		// hello gets the kicked notice from the lobby itself.)
		s.refuse(conn, name, RejectBadName, kickedText)
		return
	}
	switch role {
	case RolePlayer:
		if hash, ok := ParseHelloModsHash(hello.Data); !s.modsAdmit(hash, ok) {
			s.refuse(conn, name, RejectMods, modsDifferReason(s.Mods().List))
			return
		}
	case RoleCoop:
		// A co-op connection never becomes a player (coop.go).
		s.handleCoop(conn, name, caps)
		return
	case RoleLobby:
		// Nor does a lobby connection (lobby.go).
		s.handleLobby(conn, name, caps)
		return
	case RoleEnemyHost, RoleEnemyClient:
		// Nor an enemy-sync connection (enemy.go).
		s.handleEnemy(conn, name, role)
		return
	default:
		s.refuse(conn, name, RejectRole, fmt.Sprintf("unknown connection role %d; both players need the same ww-multiplayer version", role))
		return
	}

	// Assign an ID. Lock the new player's SendMu BEFORE it becomes visible
	// in s.players and hold it until the welcome is written: from the moment
	// it is in the map, other players' relays (broadcastExcept) can write to
	// this conn, and the client must read its welcome first.
	player := &Player{Name: name, Conn: conn}
	player.SendMu.Lock()
	s.mu.Lock()
	stopping := s.stopping()
	var id byte
	var ok bool
	if !stopping {
		id, ok = s.allocIDLocked()
	}
	if ok {
		player.ID = id
		player.Color = s.pickColorLocked(name, ParseHelloColor(hello.Data))
		s.players[id] = player
	}
	s.mu.Unlock()
	if stopping {
		// Stop has closed the players already: close without a
		// welcome, so the client retries (see Stop).
		player.SendMu.Unlock()
		return
	}
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
	s.lobby.playersChanged()

	// Per-player heartbeat: ping every heartbeatInterval so a quiet-but-
	// live client's read deadline stays alive, and so we'd notice a dead
	// peer even if it never sends. Stopped when handlePlayer returns.
	done := make(chan struct{})
	defer close(done)
	go s.heartbeatLoop(player, done)

	// Read loop
	poseWarned := false  // log a stream of bad pose frames once, not per frame
	coopWarned := false  // and co-op frames on this player connection
	enemyWarned := false // and enemy-sync frames on this player connection
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
			relay := ChatRelayMessage(name, text)
			s.broadcastExcept(id, MsgChat, relay)
			s.lobby.relayChat(0, relay) // the apps' lobby connections chat too
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
			if !player.posed.Swap(true) {
				s.lobby.playersChanged()
			}

		case MsgPing:
			// Echo the timestamp straight back so the client can compute
			// RTT. The read deadline was already reset above for this
			// successful read, so the ping also served as a keepalive.
			s.writePlayer(player, MsgPong, msg.Data)

		case MsgPong:
			// Reply to one of our heartbeat pings — liveness only. The
			// read deadline reset above is the whole point.

		case MsgCoop:
			// Co-op frames belong on a co-op connection (handleCoop),
			// never on a player's: drop them.
			if !coopWarned {
				coopWarned = true
				s.log(fmt.Sprintf("dropping co-op frames from %s: not a co-op connection", name))
			}

		case MsgEnemy:
			// Enemy-sync frames belong on an enemy connection
			// (handleEnemy), never on a player's: drop them.
			if !enemyWarned {
				enemyWarned = true
				s.log(fmt.Sprintf("dropping enemy-sync frames from %s: not an enemy-sync connection", name))
			}
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
	s.lobby.playersChanged()
}

// heartbeatLoop pings one player every heartbeatInterval until done is
// closed (handlePlayer returning). The client echoes each ping as a MsgPong,
// which resets the server's read deadline for that player — so a live client
// that's gone quiet (paused / menu) is kept alive instead of being reaped as
// an ungraceful disconnect. A write error means the socket is already gone;
// the read loop will observe the same and tear down.
func (s *Server) heartbeatLoop(p *Player, done chan struct{}) {
	s.pingLoop(func(msgType byte, data []byte) error { return s.writePlayer(p, msgType, data) }, done)
}

// pingLoop sends a MsgPing through write every heartbeatInterval until done
// is closed or a write fails. Player and co-op connections share it.
func (s *Server) pingLoop(write func(msgType byte, data []byte) error, done <-chan struct{}) {
	t := time.NewTicker(s.heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			ts := make([]byte, 8)
			binary.BigEndian.PutUint64(ts, uint64(monotonicNanos()))
			if err := write(MsgPing, ts); err != nil {
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

// maxRememberedColors bounds colorByName on a long-lived server.
const maxRememberedColors = 256

// pickColorLocked chooses the tunic color for a player joining as name
// (docs/09-player-colors.md). Rule 0 (docs/10-lobby.md): a name that is a
// lobby member gets the member's color, which the member chose. Every
// connection under one name gets the same color: each human runs two
// (broadcast-pose and puppet-sync), and a reconnect is a new connection.
// Otherwise it is the player's preference, else the color this name had
// before, else classic green. Players may share a color: nothing is ever
// moved off one because someone else wears it. The caller holds s.mu.
func (s *Server) pickColorLocked(name string, pref *playercolor.Color) playercolor.Color {
	if c, ok := s.lobby.colorOf(name); ok {
		return c
	}
	for _, p := range s.players {
		if p.Name == name {
			return p.Color
		}
	}
	prev, hadPrev := s.colorByName[name]
	var c playercolor.Color
	switch {
	case pref != nil:
		c = *pref
	case hadPrev:
		c = prev
	default:
		c = playercolor.Vanilla()
	}
	if !hadPrev && len(s.colorByName) >= maxRememberedColors {
		clear(s.colorByName)
	}
	s.colorByName[name] = c
	return c
}

// allocIDLocked returns a free player ID, searching on from s.nextID so IDs
// are reused as late as possible. IDs are 1..255: 0 is never assigned (it
// marks the welcome of a refused pre-handshake client, see refuseLegacy). ok
// is false when all 255 are taken. The caller holds s.mu.
func (s *Server) allocIDLocked() (id byte, ok bool) {
	return allocFrom(&s.nextID, func(id byte) bool { _, taken := s.players[id]; return taken })
}

// allocFrom returns the first ID in 1..255 at or after *next (wrapping)
// that is not taken, and moves *next past it. ok is false when all 255 are
// taken.
func allocFrom(next *byte, taken func(byte) bool) (id byte, ok bool) {
	for i := 0; i < 256; i++ {
		id = *next
		*next++
		if id == 0 {
			continue
		}
		if !taken(id) {
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
		entries = append(entries, PlayerEntry{ID: p.ID, Name: p.Name, Color: p.Color, HasColor: true})
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
