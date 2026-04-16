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
					c.log(fmt.Sprintf("%s left the game", rp.Name))
				}
				delete(c.remotes, leaveID)
				c.mu.Unlock()
			}

		case MsgChat:
			c.log(string(msg.Data))
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
