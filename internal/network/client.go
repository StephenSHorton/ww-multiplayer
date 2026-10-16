package network

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// RemotePlayer represents another player received from the server.
type RemotePlayer struct {
	ID       byte
	Name     string
	Position *PlayerPosition
	// PoseJoints + PoseMatrices is the latest received skeletal pose
	// (raw mpNodeMtx bytes, big-endian, ready to memcpy into a Link
	// J3DModel's mpNodeMtx via WriteAbsolute). Nil until the remote
	// has sent a pose at least once.
	PoseJoints   int
	PoseMatrices []byte
	// FaceState is the optional trailing payload appended by v0.2+
	// senders to a pose message (session 9). Empty if the remote is
	// running an older build that didn't ship face data. Layout
	// matches inject/include/mailbox.h FaceState (8 B = mat1_tex0,
	// mat1_tex1, mat4_tex0, mat4_tex1, all u16 BE) with room for
	// future face mats appended at the tail.
	FaceState []byte
	// LastPose is the wall-clock time the most recent MsgPose for this
	// remote was received. The receiver loop uses it (via PoseLiveness)
	// to freeze, then despawn, a remote whose pose stream goes silent —
	// a local safety net faster than the server's read-timeout MsgLeave.
	// Zero until the first pose arrives.
	LastPose time.Time
	// Color is the tunic color the server assigned this player
	// (docs/09-player-colors.md). HasColor is false when the server sent
	// none (a build from before colors); TunicColor then falls back to
	// playercolor.ForID.
	Color    playercolor.Color
	HasColor bool
}

// TunicColor is the color to draw this remote's tunic in: the server's
// assignment, or the by-ID fallback for a server without colors.
func (rp *RemotePlayer) TunicColor() playercolor.Color {
	if rp.HasColor {
		return rp.Color
	}
	return playercolor.ForID(rp.ID)
}

// Client connects to a server and syncs position data.
type Client struct {
	// mu guards conn, remotes, myID, and stopHeartbeat. connected/lastRTT/
	// serverEpoch are atomics so the hot send/receive loops can read them
	// without taking the lock.
	mu      sync.RWMutex
	conn    net.Conn
	myID    byte
	name    string
	remotes map[byte]*RemotePlayer

	// writeMu serializes socket writes. Before heartbeats, the only writer
	// was the broadcast loop; now the heartbeat ticker (MsgPing) and the
	// read loop (MsgPong echo) also write, so concurrent writers would
	// interleave framed-message bytes without this lock.
	writeMu sync.Mutex

	connected     atomic.Bool   // session liveness (lock-free reads)
	lastRTT       atomic.Int64  // last measured round-trip, nanoseconds
	serverEpoch   atomic.Uint64 // server boot epoch from the welcome
	stopHeartbeat chan struct{} // closed by Disconnect to stop the ticker

	// Timing captured at construction so the read/heartbeat goroutines never
	// read the package-level tunables directly (which tests retune). The
	// package vars are static in production, so capturing once is lossless.
	heartbeatEvery time.Duration
	readTimeout    time.Duration
	writeTimeout   time.Duration
	dialTimeout    time.Duration

	// dialContext opens the TCP connection. It is net.Dialer.DialContext in
	// production; a field only so tests can substitute a dial that never
	// completes and check that dialTimeout and ctx cancellation still end it.
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)

	OnLog        func(string)
	OnPlayerList func([]RemotePlayer)

	// OnChat is invoked from the read loop for each incoming chat frame,
	// with the authoritative sender name and text the server relayed. Nil
	// for clients that don't surface chat (CLI broadcast-pose / puppet-sync,
	// which fall back to logging it via OnLog). Mirrors the OnLog /
	// OnPlayerList callback pattern.
	OnChat func(from, text string)

	// role and caps go in the hello: RolePlayer for NewClient, RoleCoop
	// for NewCoopClient (coop.go).
	role byte
	caps uint16

	// colorPref is the tunic color this player asks for in its hello
	// (SetColorPreference); nil lets the server pick. myColor is what the
	// server assigned us, from the player list (guarded by mu).
	colorPref   *playercolor.Color
	myColor     playercolor.Color
	haveMyColor bool

	// OnCoop is invoked from the read loop with each MsgCoop payload a
	// co-op client (NewCoopClient) receives; the payload is the
	// callback's to keep. A player client drops MsgCoop. Like OnChat, it
	// runs on the read loop, so it must hand slow work off.
	OnCoop func(payload []byte)

	// OnLobby is invoked from the read loop with each MsgLobby payload a
	// lobby client (NewLobbyClient) receives. A player or co-op client
	// drops MsgLobby.
	OnLobby func(payload []byte)
}

// NewClient creates a client with the given player name.
func NewClient(name string) *Client {
	return &Client{
		name:           name,
		remotes:        make(map[byte]*RemotePlayer),
		heartbeatEvery: heartbeatInterval,
		readTimeout:    clientReadTimeout,
		writeTimeout:   writeTimeout,
		dialTimeout:    dialTimeout,
		dialContext:    (&net.Dialer{}).DialContext,
	}
}

func (c *Client) log(msg string) {
	if c.OnLog != nil {
		c.OnLog(msg)
	}
}

// Connect joins a server at the given address. It is ConnectContext with a
// context that is never cancelled, for the one-shot CLI tools that have no
// shutdown context to pass.
func (c *Client) Connect(addr string) error {
	return c.ConnectContext(context.Background(), addr)
}

// ConnectContext joins a server at the given address. Safe to call again on
// the same Client after a drop (the reconnect loop does exactly that): it
// spins up a fresh read loop + heartbeat ticker for the new connection.
//
// The dial is bounded by dialTimeout, and cancelling ctx ends the attempt
// promptly at any stage: during the dial, or during the join/welcome
// handshake (the conn is closed under the blocked write or read). That is
// what lets a TUI Esc or a Ctrl+C tear down a session that is still trying
// to reach an unreachable or silent host, instead of waiting out the OS
// connect timeout or the 15 s handshake read deadline.
//
// Invariant: ConnectContext and Disconnect must alternate — every connect is
// preceded by a completed Disconnect of the prior session (ReconnectLoop's
// run closure guarantees this by waiting for its ctx watcher's Disconnect
// before returning). It has one caller at a time: it is not safe to call
// concurrently, nor twice without an intervening Disconnect (that would
// orphan the previous conn + heartbeat goroutine).
func (c *Client) ConnectContext(ctx context.Context, addr string) error {
	dialCtx := ctx
	if c.dialTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, c.dialTimeout)
		defer cancel()
	}
	conn, err := c.dialContext(dialCtx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Cancelling ctx mid-handshake closes conn, which fails the blocked join
	// write or welcome read below at once.
	stopWatch := context.AfterFunc(ctx, func() { conn.Close() })
	msg, err := c.handshake(conn)
	if !stopWatch() {
		// The watcher already ran: ctx was cancelled and conn is closed.
		return fmt.Errorf("connect: %w", ctx.Err())
	}
	if err != nil {
		conn.Close()
		return err
	}

	id, epoch, _ := ParseWelcome(msg.Data)
	prevEpoch := c.serverEpoch.Swap(epoch)

	stop := make(chan struct{})
	c.mu.Lock()
	c.myID = id
	c.conn = conn
	c.stopHeartbeat = stop
	if prevEpoch != 0 && epoch != prevEpoch {
		// The server restarted: it hands out IDs afresh, so every remote
		// we hold is stale, and one may even carry the ID we now have.
		c.remotes = make(map[byte]*RemotePlayer)
	}
	c.mu.Unlock()
	c.connected.Store(true)
	if c.role == RoleCoop {
		c.log(fmt.Sprintf("Connected for co-op (co-op ID %d)", id))
	} else if c.role == RoleLobby {
		c.log("Connected to the lobby")
	} else {
		c.log(fmt.Sprintf("Connected as player %d", id))
	}

	go c.readLoop(conn)
	go c.heartbeatLoop(conn, stop)
	return nil
}

// handshake sends our join on a freshly dialed conn and waits for the
// server's welcome.
func (c *Client) handshake(conn net.Conn) (*Message, error) {
	// Send join message (+ the version hello; see handshake.go). Bound it
	// with the same write deadline every other client write goes through
	// (via writeOn) so a half-dead server with a full send buffer errors
	// out here instead of wedging the handshake.
	if c.writeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	if err := writeJoinRole(conn, c.name, c.role, c.caps, c.colorPref); err != nil {
		return nil, fmt.Errorf("send join: %w", err)
	}

	// Wait for welcome. Bound the handshake read so a half-dead server
	// doesn't wedge a reconnect attempt forever.
	if c.readTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	msg, err := ReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("expected welcome: %w", err)
	}
	// A reject or a version mismatch comes back as a *RejectError, which
	// ends ReconnectLoop (errors.Is ErrFatal) instead of retrying.
	if err := checkWelcome(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// Disconnect closes the connection and stops the heartbeat ticker.
// Idempotent: a second call (e.g. from the ctx watcher and the reconnect
// loop both tearing down) is a no-op.
func (c *Client) Disconnect() {
	c.connected.Store(false)
	c.mu.Lock()
	conn := c.conn
	stop := c.stopHeartbeat
	c.conn = nil
	c.stopHeartbeat = nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if conn != nil {
		conn.Close()
	}
}

// SetColorPreference sets the tunic color this player asks the server for
// (docs/09-player-colors.md); nil lets the server pick. It takes effect at
// the next connect, so call it before Connect.
func (c *Client) SetColorPreference(pref *playercolor.Color) {
	if pref != nil {
		p := *pref
		pref = &p
	}
	c.colorPref = pref
}

// MyColor returns the tunic color the server assigned this player. ok is
// false until a player list with colors has arrived (or never, against a
// server from before colors: use playercolor.ForID(MyID()) then).
func (c *Client) MyColor() (color playercolor.Color, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.myColor, c.haveMyColor
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool { return c.connected.Load() }

// MyID returns this client's player ID (a co-op client's co-op ID).
func (c *Client) MyID() byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.myID
}

// LastRTT returns the most recent client<->server round-trip time measured
// from a ping/pong exchange, or 0 if none has completed yet. Exposed for the
// future status panel (#19).
func (c *Client) LastRTT() time.Duration { return time.Duration(c.lastRTT.Load()) }

// measuredRTT is the round trip of a ping stamped sent and answered at
// now (monotonicNanos), in nanoseconds; false for a stamp from the future,
// which no ping of this process has. It is at least 1, since LastRTT's 0
// means "none yet": on Windows the monotonic clock can read the same
// before and after a loopback round trip. Go reads it from the interrupt
// time (runtime.nanotime1, sys_windows_amd64.s), which advances only on
// the clock tick, about 0.5 ms apart or more (time/time.go's resolution
// note), so TestCoopHeartbeat read 0 there on every run.
func measuredRTT(now, sent int64) (int64, bool) {
	if rtt := now - sent; rtt >= 0 {
		return max(rtt, 1), true
	}
	return 0, false
}

// ServerEpoch returns the server's boot epoch as reported in the welcome
// (0 for a legacy server). A change across a reconnect means the server
// restarted, so the caller should do a full resync.
func (c *Client) ServerEpoch() uint64 { return c.serverEpoch.Load() }

// writeOn serializes a framed write to the given connection. All client
// writers funnel through here so they never interleave on the socket. A write
// deadline bounds the write so a stalled peer (full TCP send buffer) surfaces
// as an error instead of blocking the writer (and, via writeMu, every other
// writer) indefinitely.
func (c *Client) writeOn(conn net.Conn, msgType byte, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	return WriteMessage(conn, msgType, data)
}

// send writes to the current connection, snapshotting it under the lock so a
// concurrent Disconnect()/Connect() can't race the conn field.
func (c *Client) send(msgType byte, data []byte) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if !c.connected.Load() || conn == nil {
		return fmt.Errorf("not connected")
	}
	return c.writeOn(conn, msgType, data)
}

// SendPosition sends the local player's position to the server.
func (c *Client) SendPosition(pos *PlayerPosition) error {
	return c.send(MsgPosition, pos.Serialize())
}

// SendChat sends a chat message to the server. Text over MaxChatLen bytes is
// refused here, since the server would drop it.
func (c *Client) SendChat(message string) error {
	if len(message) > MaxChatLen {
		return fmt.Errorf("chat message is %d bytes long (max %d)", len(message), MaxChatLen)
	}
	return c.send(MsgChat, []byte(message))
}

// SendPose sends the local player's skeletal pose to the server. The
// matrices slice is wire-ready big-endian (i.e. raw mpNodeMtx bytes).
// `face` is an optional trailing payload — empty []byte for legacy /
// pose-only senders, or 8 B (eye-only face state) for session 9+.
func (c *Client) SendPose(joints int, matrices []byte, face []byte) error {
	body := SerializePose(joints, matrices, face)
	if body == nil {
		return fmt.Errorf("invalid pose: joints=%d len=%d", joints, len(matrices))
	}
	return c.send(MsgPose, body)
}

// GetRemotePlayers returns a snapshot of all remote players.
func (c *Client) GetRemotePlayers() []RemotePlayer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]RemotePlayer, 0, len(c.remotes))
	for _, rp := range c.remotes {
		result = append(result, *rp)
	}
	return result
}

// heartbeatLoop pings the server every heartbeatInterval. The ping keeps the
// server's read deadline alive (so a silent-but-live receiver like puppet-
// sync isn't reaped) and elicits a MsgPong the read loop turns into an RTT.
// Captures its own conn so it never touches the c.conn field concurrently.
func (c *Client) heartbeatLoop(conn net.Conn, stop chan struct{}) {
	t := time.NewTicker(c.heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if !c.connected.Load() {
				return
			}
			if err := c.writeOn(conn, MsgPing, PingPayload(monotonicNanos())); err != nil {
				return
			}
		}
	}
}

func (c *Client) readLoop(conn net.Conn) {
	// Safety net: a bug in handling one server frame (a parser, or an
	// OnChat/OnLog callback) must cost this session, not the whole exe,
	// which also runs the TUI and our other connection. Ending the session
	// hands over to the reconnect path like any other drop. c.mu is never
	// held when this runs: no callback is called under it below.
	defer func() {
		if r := recover(); r != nil {
			c.abandonSession(conn)
			func() {
				defer func() { recover() }() // the log callback may be what panicked
				c.log(fmt.Sprintf("Disconnected: internal error handling a server message: %v", r))
			}()
		}
	}()
	for c.connected.Load() {
		// Read deadline = the ungraceful-disconnect detector. A peer that
		// stops sending (crash, cable pull, hang) trips this, which is
		// handled exactly like any other read error: connected=false, the
		// loop returns, and the reconnect loop takes over.
		if c.readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(c.readTimeout))
		}
		msg, err := ReadMessage(conn)
		if err != nil {
			// Only while conn is still the live connection: after a
			// Disconnect and a fast reconnect, this stale loop's read
			// error must not mark the new session disconnected.
			c.mu.RLock()
			current := c.conn == conn
			c.mu.RUnlock()
			if current && c.connected.Load() {
				c.log("Disconnected from server")
				c.connected.Store(false)
			}
			return
		}

		switch msg.Type {
		case MsgPosition:
			playerID, pos := ParsePositionMessage(msg.Data)
			if pos != nil {
				c.mu.Lock()
				if rp, ok := c.remotes[playerID]; ok {
					rp.Position = pos
				} else {
					c.remotes[playerID] = &RemotePlayer{
						ID:       playerID,
						Position: pos,
					}
				}
				c.mu.Unlock()
			}

		case MsgPlayerList:
			c.parsePlayerList(msg.Data)

		case MsgLeave:
			if len(msg.Data) > 0 {
				leaveID := msg.Data[0]
				c.mu.Lock()
				rp, ok := c.remotes[leaveID]
				delete(c.remotes, leaveID)
				c.mu.Unlock()
				// Log outside c.mu: a callback that panicked while we held
				// the lock would leave it locked past the recover above.
				if ok {
					c.log(fmt.Sprintf("%s left the game", rp.Name))
				}
			}

		case MsgChat:
			from, text, err := ParseChatRelay(msg.Data)
			if err != nil {
				break // garbled or foreign frame
			}
			// Self-filter: each human runs TWO connections under one name
			// (broadcast-pose + puppet-sync). The server relays a message to
			// our OTHER connection, so drop anything whose sender is us —
			// mirroring the render loop's selfFilter (name == self). The
			// sending connection is already excluded server-side by
			// broadcastExcept; this covers only the co-located twin.
			if from == c.name {
				break
			}
			if c.OnChat != nil {
				c.OnChat(from, text)
			} else {
				// No chat sink (CLI): preserve the pre-#20 behavior of
				// surfacing incoming chat as a "name: text" log line.
				c.log(from + ": " + text)
			}

		case MsgPose:
			playerID, joints, matrices, face, err := ParsePoseRelayMessage(msg.Data)
			if err == nil {
				// Copy out so the matrix bytes aren't tied to the
				// next ReadMessage's buffer (defensive — current
				// ReadMessage allocates fresh per call, but this
				// keeps the contract local).
				poseCopy := make([]byte, len(matrices))
				copy(poseCopy, matrices)
				var faceCopy []byte
				if len(face) > 0 {
					faceCopy = make([]byte, len(face))
					copy(faceCopy, face)
				}
				now := time.Now()
				c.mu.Lock()
				rp, ok := c.remotes[playerID]
				if !ok {
					rp = &RemotePlayer{ID: playerID}
					c.remotes[playerID] = rp
				}
				rp.PoseJoints = joints
				rp.PoseMatrices = poseCopy
				rp.FaceState = faceCopy
				rp.LastPose = now
				c.mu.Unlock()
			}

		case MsgPing:
			// Echo the timestamp straight back so the server (or any peer
			// that pinged us) can measure RTT and keep its read deadline
			// alive. Harmless if unused.
			c.writeOn(conn, MsgPong, msg.Data)

		case MsgPong:
			// Response to one of OUR pings: compute round-trip time.
			if ts, ok := ParsePingTimestamp(msg.Data); ok {
				if rtt, ok := measuredRTT(monotonicNanos(), ts); ok {
					c.lastRTT.Store(rtt)
				}
			}

		case MsgCoop:
			// Only a co-op connection takes them (coop.go); the server
			// never sends one to a player.
			if c.role == RoleCoop && c.OnCoop != nil {
				c.OnCoop(msg.Data)
			}

		case MsgLobby:
			// Only a lobby connection takes them (lobby_client.go).
			if c.role == RoleLobby && c.OnLobby != nil {
				c.OnLobby(msg.Data)
			}
		}
	}
}

// parsePlayerList applies a MsgPlayerList. The list is authoritative: the
// server sends the full roster after every join and leave, including right
// after our own welcome, so a remote missing from it is gone. That drops,
// for example, a player who left while we were reconnecting (we never got
// their MsgLeave). A malformed list is ignored whole, not half-applied.
func (c *Client) parsePlayerList(data []byte) {
	entries, err := ParsePlayerList(data)
	if err != nil {
		c.log(fmt.Sprintf("Ignoring a malformed player list: %v", err))
		return
	}

	var logs []string
	c.mu.Lock()
	listed := make(map[byte]bool, len(entries))
	for _, e := range entries {
		if e.ID == c.myID {
			c.myColor, c.haveMyColor = e.Color, e.HasColor
			continue
		}
		listed[e.ID] = true
		if rp, ok := c.remotes[e.ID]; ok {
			rp.Name = e.Name
			rp.Color, rp.HasColor = e.Color, e.HasColor
		} else {
			c.remotes[e.ID] = &RemotePlayer{ID: e.ID, Name: e.Name, Color: e.Color, HasColor: e.HasColor}
		}
		logs = append(logs, fmt.Sprintf("Player %s (ID:%d) in game", e.Name, e.ID))
	}
	for id, rp := range c.remotes {
		if !listed[id] {
			delete(c.remotes, id)
			logs = append(logs, fmt.Sprintf("Player %s (ID:%d) is no longer in game", rp.Name, id))
		}
	}
	c.mu.Unlock()

	// Log outside c.mu, as in the MsgLeave case.
	for _, l := range logs {
		c.log(l)
	}
}

// abandonSession ends the session on conn after its read loop gave up on
// it. connected is cleared only while conn is still the live connection, so
// a stale loop can't mark a newer session disconnected; conn is closed so
// the server notices at once.
func (c *Client) abandonSession(conn net.Conn) {
	c.mu.RLock()
	current := c.conn == conn
	c.mu.RUnlock()
	if current {
		c.connected.Store(false)
	}
	conn.Close()
}
