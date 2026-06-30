package network

import (
	"fmt"
	"net"
	"sync"
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
}

// Client connects to a server and syncs position data.
type Client struct {
	conn      net.Conn
	myID      byte
	name      string
	remotes   map[byte]*RemotePlayer
	mu        sync.RWMutex
	connected bool
	OnLog     func(string)
	OnPlayerList func([]RemotePlayer)
	// OnJoin / OnLeave fire once per distinct remote player name, the
	// first time a genuinely new id (≠ myID) appears under that name /
	// once the last remaining id under that name disappears. Both checks
	// matter: a single human occupies two connections under the same
	// name at once (one broadcast-pose, one puppet-sync; see
	// runMultiplayerGoroutines in main.go), so without the name dedup a
	// real join/leave would double-fire, and without comparing against
	// our own `name` we'd treat our own second connection as a stranger
	// walking in. Both optional (nil = no-op), nil-checked like OnLog.
	OnJoin  func(name string)
	OnLeave func(name string)
}

// NewClient creates a client with the given player name.
func NewClient(name string) *Client {
	return &Client{
		name:    name,
		remotes: make(map[byte]*RemotePlayer),
	}
}

func (c *Client) log(msg string) {
	if c.OnLog != nil {
		c.OnLog(msg)
	}
}

func (c *Client) fireJoin(name string) {
	if c.OnJoin != nil {
		c.OnJoin(name)
	}
}

func (c *Client) fireLeave(name string) {
	if c.OnLeave != nil {
		c.OnLeave(name)
	}
}

// Connect joins a server at the given address.
func (c *Client) Connect(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	c.conn = conn

	// Send join message
	if err := WriteMessage(conn, MsgJoin, []byte(c.name)); err != nil {
		conn.Close()
		return fmt.Errorf("send join: %w", err)
	}

	// Wait for welcome
	msg, err := ReadMessage(conn)
	if err != nil || msg.Type != MsgWelcome {
		conn.Close()
		return fmt.Errorf("expected welcome, got error or wrong type")
	}

	c.myID = msg.Data[0]
	c.connected = true
	c.log(fmt.Sprintf("Connected as player %d", c.myID))

	go c.readLoop()
	return nil
}

// Disconnect closes the connection.
func (c *Client) Disconnect() {
	c.connected = false
	if c.conn != nil {
		c.conn.Close()
	}
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool { return c.connected }

// MyID returns this client's player ID.
func (c *Client) MyID() byte { return c.myID }

// SendPosition sends the local player's position to the server.
func (c *Client) SendPosition(pos *PlayerPosition) error {
	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}
	return WriteMessage(c.conn, MsgPosition, pos.Serialize())
}

// SendChat sends a chat message to the server.
func (c *Client) SendChat(message string) error {
	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}
	return WriteMessage(c.conn, MsgChat, []byte(message))
}

// SendPose sends the local player's skeletal pose to the server. The
// matrices slice is wire-ready big-endian (i.e. raw mpNodeMtx bytes).
// `face` is an optional trailing payload — empty []byte for legacy /
// pose-only senders, or 8 B (eye-only face state) for session 9+.
func (c *Client) SendPose(joints int, matrices []byte, face []byte) error {
	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}
	body := SerializePose(joints, matrices, face)
	if body == nil {
		return fmt.Errorf("invalid pose: joints=%d len=%d", joints, len(matrices))
	}
	return WriteMessage(c.conn, MsgPose, body)
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

func (c *Client) readLoop() {
	for c.connected {
		msg, err := ReadMessage(c.conn)
		if err != nil {
			if c.connected {
				c.log("Disconnected from server")
				c.connected = false
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
					leftName := rp.Name
					c.log(fmt.Sprintf("%s left the game", leftName))
					delete(c.remotes, leaveID)
					// Only fire OnLeave once no remaining remote shares
					// this name (collapses a human's broadcast-pose +
					// puppet-sync sibling connections into one leave
					// event) and never for our own name.
					stillPresent := false
					for _, other := range c.remotes {
						if other.Name == leftName {
							stillPresent = true
							break
						}
					}
					if !stillPresent && leftName != c.name {
						c.fireLeave(leftName)
					}
				} else {
					delete(c.remotes, leaveID)
				}
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
				c.mu.Lock()
				rp, ok := c.remotes[playerID]
				if !ok {
					rp = &RemotePlayer{ID: playerID}
					c.remotes[playerID] = rp
				}
				rp.PoseJoints = joints
				rp.PoseMatrices = poseCopy
				rp.FaceState = faceCopy
				c.mu.Unlock()
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
				// Count existing remotes under this name BEFORE
				// inserting, so a human's second connection
				// (broadcast-pose + puppet-sync register under the same
				// display name) doesn't trigger a second join chime.
				firstForName := true
				for _, other := range c.remotes {
					if other.Name == name {
						firstForName = false
						break
					}
				}
				c.remotes[id] = &RemotePlayer{ID: id, Name: name}
				if firstForName && name != c.name {
					c.fireJoin(name)
				}
			}
			c.log(fmt.Sprintf("Player %s (ID:%d) in game", name, id))
		}
	}
}
