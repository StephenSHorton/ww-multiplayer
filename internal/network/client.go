package network

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
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

	OnLog        func(string)
	OnPlayerList func([]RemotePlayer)
}

// NewClient creates a client with the given player name.
func NewClient(name string) *Client {
	return &Client{
		name:           name,
		remotes:        make(map[byte]*RemotePlayer),
		heartbeatEvery: heartbeatInterval,
		readTimeout:    clientReadTimeout,
		writeTimeout:   writeTimeout,
	}
}

func (c *Client) log(msg string) {
	if c.OnLog != nil {
		c.OnLog(msg)
	}
}

// Connect joins a server at the given address. Safe to call again on the
// same Client after a drop (the reconnect loop does exactly that): it spins
// up a fresh read loop + heartbeat ticker for the new connection.
//
// Invariant: Connect and Disconnect must alternate — every Connect is
// preceded by a completed Disconnect of the prior session (ReconnectLoop's
// run closure guarantees this by waiting for its ctx watcher's Disconnect
// before returning). Connect is not safe to call concurrently, nor twice
// without an intervening Disconnect (that would orphan the previous conn +
// heartbeat goroutine).
func (c *Client) Connect(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Send join message
	if err := WriteMessage(conn, MsgJoin, []byte(c.name)); err != nil {
		conn.Close()
		return fmt.Errorf("send join: %w", err)
	}

	// Wait for welcome. Bound the handshake read so a half-dead server
	// doesn't wedge a reconnect attempt forever.
	if c.readTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	msg, err := ReadMessage(conn)
	if err != nil || msg.Type != MsgWelcome || len(msg.Data) < 1 {
		conn.Close()
		return fmt.Errorf("expected welcome, got error or wrong type")
	}

	id, epoch, _ := ParseWelcome(msg.Data)
	c.serverEpoch.Store(epoch)

	stop := make(chan struct{})
	c.mu.Lock()
	c.myID = id
	c.conn = conn
	c.stopHeartbeat = stop
	c.mu.Unlock()
	c.connected.Store(true)
	c.log(fmt.Sprintf("Connected as player %d", id))

	go c.readLoop(conn)
	go c.heartbeatLoop(conn, stop)
	return nil
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

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool { return c.connected.Load() }

// MyID returns this client's player ID.
func (c *Client) MyID() byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.myID
}

// LastRTT returns the most recent client<->server round-trip time measured
// from a ping/pong exchange, or 0 if none has completed yet. Exposed for the
// future status panel (#19).
func (c *Client) LastRTT() time.Duration { return time.Duration(c.lastRTT.Load()) }

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

// SendChat sends a chat message to the server.
func (c *Client) SendChat(message string) error {
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
			if c.connected.Load() {
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
				if rp, ok := c.remotes[leaveID]; ok {
					c.log(fmt.Sprintf("%s left the game", rp.Name))
				}
				delete(c.remotes, leaveID)
				c.mu.Unlock()
			}

		case MsgChat:
			c.log(string(msg.Data))

		case MsgPose:
			playerID, joints, matrices, face := ParsePoseRelayMessage(msg.Data)
			if matrices != nil {
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
				if rtt := monotonicNanos() - ts; rtt >= 0 {
					c.lastRTT.Store(rtt)
				}
			}
		}
	}
}

func (c *Client) parsePlayerList(data []byte) {
	if len(data) < 1 {
		return
	}
	count := int(data[0])
	offset := 1

	c.mu.Lock()
	defer c.mu.Unlock()

	for i := 0; i < count && offset < len(data); i++ {
		id := data[offset]
		offset++
		nameLen := int(data[offset])
		offset++
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		if id != c.myID {
			if rp, ok := c.remotes[id]; ok {
				rp.Name = name
			} else {
				c.remotes[id] = &RemotePlayer{ID: id, Name: name}
			}
			c.log(fmt.Sprintf("Player %s (ID:%d) in game", name, id))
		}
	}
}
