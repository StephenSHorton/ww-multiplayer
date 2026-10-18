package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Enemy-sync connections (the host-authoritative Moblin mirror,
// ww-multiplayer-handoff/design/host-authoritative-prototype.md). The
// enemy pipelines do not ride a player's connection: the host's sampler
// opens one with RoleEnemyHost in its hello and every joiner's mirror one
// with RoleEnemyClient. The server welcomes each like a player (MsgWelcome
// with an ID, the epoch and the version), but the ID comes from an enemy
// ID space of its own and the connection never enters s.players: no
// roster, PlayerCount or relay sees it. It keeps the heartbeat both ways,
// so a quiet enemy client (the host sends count-0 keepalives, the client
// may send nothing for a long time) is kept alive by the server's pings
// and its pongs, exactly as a co-op connection is.
//
// Authority is the connection's role, so there is no election and no
// authority byte in the stream. The server takes at most one RoleEnemyHost
// at a time (a second gets RejectRole) and relays MsgEnemy by role:
//
//	STATE (sub 1)  host -> every enemy client
//	HIT   (sub 2)  enemy client -> the host, with the client's connection
//	               ID stamped into byte EnemyClientIDOff of the payload so
//	               the host's ACK can name it
//	ACK   (sub 3)  host -> the enemy client whose ID is at
//	               EnemyClientIDOff (the one the HIT was stamped with)
//
// Anything else on an enemy connection is dropped, as is a MsgEnemy on a
// player connection. The server checks only the frame shape at ingress
// (sub byte, exact length); package mods/enemysync encodes and decodes
// the fields.

// The MsgEnemy payload shapes the server checks at ingress. Package
// mods/enemysync builds and parses them; the sizes are pinned there too.
const (
	EnemySubState byte = 1
	EnemySubHit   byte = 2
	EnemySubAck   byte = 3

	// EnemyStateHeaderLen is a STATE's fixed header (sub, ver, flags,
	// count, epoch, seq, hostBeat, host_ms, stage[8], room, dead_mask,
	// pad u16); EnemySlotLen follows it EnemyMaxSlots times at most (48
	// since wire version 2 added the animation identity).
	EnemyStateHeaderLen = 32
	EnemySlotLen        = 48
	EnemyMaxSlots       = 4
	// EnemyHitLen and EnemyAckLen are the fixed HIT and ACK sizes (the HIT
	// 48 since version 2 added the sword object's Se/HitMark/Spl/SPrm).
	EnemyHitLen = 48
	EnemyAckLen = 16
	// EnemyClientIDOff is the payload byte of a HIT and an ACK that
	// carries the enemy client's connection ID: the server stamps it into
	// a HIT and routes an ACK by it.
	EnemyClientIDOff = 2
)

// EnemyStateLen is the length of a STATE with count slots.
func EnemyStateLen(count int) int { return EnemyStateHeaderLen + EnemySlotLen*count }

// enemyFrameShape reports why a MsgEnemy payload from a connection of role
// role is not one the server relays, or "".
func enemyFrameShape(role byte, data []byte) string {
	if len(data) < 2 {
		return fmt.Sprintf("%d-byte payload", len(data))
	}
	switch sub := data[0]; {
	case sub == EnemySubState && role == RoleEnemyHost:
		if len(data) < EnemyStateHeaderLen {
			return fmt.Sprintf("%d-byte STATE is shorter than its %d-byte header", len(data), EnemyStateHeaderLen)
		}
		count := int(data[3])
		if count > EnemyMaxSlots {
			return fmt.Sprintf("STATE with %d slots (max %d)", count, EnemyMaxSlots)
		}
		if len(data) != EnemyStateLen(count) {
			return fmt.Sprintf("%d-byte STATE with %d slots (want %d bytes)", len(data), count, EnemyStateLen(count))
		}
	case sub == EnemySubHit && role == RoleEnemyClient:
		if len(data) != EnemyHitLen {
			return fmt.Sprintf("%d-byte HIT (want %d)", len(data), EnemyHitLen)
		}
	case sub == EnemySubAck && role == RoleEnemyHost:
		if len(data) != EnemyAckLen {
			return fmt.Sprintf("%d-byte ACK (want %d)", len(data), EnemyAckLen)
		}
	default:
		return fmt.Sprintf("sub %d from role %d", sub, role)
	}
	return ""
}

// enemyHostTakenReason is the MsgReject reason for a second RoleEnemyHost.
const enemyHostTakenReason = "this host already has an enemy-sync host connection; only one is allowed"

// enemyConn is the server's side of one enemy-sync connection.
type enemyConn struct {
	id   byte
	name string
	role byte
	conn net.Conn
	srv  *Server

	sendMu    sync.Mutex // serializes writes: relays, heartbeat pings, pongs
	done      chan struct{}
	closeOnce sync.Once
}

// write sends one frame under sendMu, bounded by the server's write
// deadline.
func (c *enemyConn) write(msgType byte, data []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.srv.writeConn(c.conn, msgType, data)
}

// close disconnects the peer. Idempotent.
func (c *enemyConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// EnemyHostConnected reports whether an enemy-sync host connection is up.
func (s *Server) EnemyHostConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enemyHost != nil
}

// EnemyClientCount returns the number of live enemy-sync client
// connections. They are never in PlayerCount.
func (s *Server) EnemyClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.enemyClients)
}

// allocEnemyIDLocked is allocIDLocked for the enemy ID space (host and
// clients share it, so a HIT's stamp never names the host). The caller
// holds s.mu.
func (s *Server) allocEnemyIDLocked() (id byte, ok bool) {
	return allocFrom(&s.nextEnemyID, func(id byte) bool {
		_, taken := s.enemyClients[id]
		return taken || (s.enemyHost != nil && s.enemyHost.id == id)
	})
}

// handleEnemy runs an enemy-sync connection whose join and hello
// handlePlayer has read and checked (version, name). It returns when the
// connection ends; handlePlayer closes conn.
func (s *Server) handleEnemy(conn net.Conn, name string, role byte) {
	c := &enemyConn{name: name, role: role, conn: conn, srv: s, done: make(chan struct{})}
	s.mu.Lock()
	stopping := s.stopping()
	var id byte
	var ok, taken bool
	if !stopping {
		if role == RoleEnemyHost && s.enemyHost != nil {
			taken = true
		} else if id, ok = s.allocEnemyIDLocked(); ok {
			c.id = id
			if role == RoleEnemyHost {
				s.enemyHost = c
			} else {
				s.enemyClients[id] = c
			}
		}
	}
	s.mu.Unlock()
	if stopping {
		// Stop has closed the enemy connections already: close without
		// a reject (which would be fatal), so the client retries.
		return
	}
	if taken {
		s.refuse(conn, name, RejectRole, enemyHostTakenReason)
		return
	}
	if !ok {
		s.refuse(conn, name, RejectServerFull, "server full: all 255 enemy-sync connection IDs are in use")
		return
	}
	defer func() {
		s.mu.Lock()
		if s.enemyHost == c {
			s.enemyHost = nil
		}
		if s.enemyClients[id] == c {
			delete(s.enemyClients, id)
		}
		s.mu.Unlock()
	}()
	defer c.close()

	if err := c.write(MsgWelcome, WelcomeMessage(id, s.epoch)); err != nil {
		return
	}
	what := "enemy client"
	if role == RoleEnemyHost {
		what = "enemy host"
	}
	s.log(fmt.Sprintf("%s (%s %d) connected", name, what, id))
	go s.pingLoop(c.write, c.done)

	readTimeout := s.readTimeout
	shapeWarned := false
	for {
		if readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(readTimeout))
		}
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}
		switch msg.Type {
		case MsgEnemy:
			if why := enemyFrameShape(role, msg.Data); why != "" {
				if !shapeWarned {
					shapeWarned = true
					s.log(fmt.Sprintf("dropping bad enemy-sync frames from %s (%s %d): %s", name, what, id, why))
				}
				break
			}
			s.relayEnemy(c, msg.Data)
		case MsgPing:
			c.write(MsgPong, msg.Data)
		case MsgPong:
			// Liveness only, as on a player connection. Player frames
			// (position, pose, chat, co-op) are dropped.
		}
	}
	s.log(fmt.Sprintf("%s (%s %d) disconnected", name, what, id))
}

// relayEnemy forwards one well-shaped MsgEnemy payload from c by role:
// a STATE to every enemy client, a HIT to the host (stamped with c's ID),
// an ACK to the client it names. A recipient whose write fails is closed,
// which ends its handleEnemy.
func (s *Server) relayEnemy(c *enemyConn, data []byte) {
	var to []*enemyConn
	s.mu.RLock()
	switch data[0] {
	case EnemySubState:
		for _, ec := range s.enemyClients {
			to = append(to, ec)
		}
	case EnemySubHit:
		if s.enemyHost != nil {
			data[EnemyClientIDOff] = c.id
			to = append(to, s.enemyHost)
		}
	case EnemySubAck:
		if ec, ok := s.enemyClients[data[EnemyClientIDOff]]; ok {
			to = append(to, ec)
		}
	}
	s.mu.RUnlock()
	for _, ec := range to {
		if err := ec.write(MsgEnemy, data); err != nil {
			ec.close()
		}
	}
}

// closeEnemyLocked closes every enemy connection; the caller holds s.mu.
func (s *Server) closeEnemyLocked() {
	if s.enemyHost != nil {
		s.enemyHost.close()
	}
	for _, c := range s.enemyClients {
		c.close()
	}
}

// --- client side ------------------------------------------------------------

// NewEnemyClient creates the client side of an enemy-sync connection: a
// Client whose hello carries role (RoleEnemyHost or RoleEnemyClient). It
// takes part in no relay: it never receives positions, poses, chat or a
// roster. Inbound MsgEnemy payloads go to OnEnemy; SendEnemy sends one.
// Connect, ConnectContext, Disconnect, the heartbeat and ReconnectLoop
// behave as for a player, and RunEnemy drives all of it.
func NewEnemyClient(name string, role byte) *Client {
	c := NewClient(name)
	c.role = role
	return c
}

// isEnemyRole reports whether role is one of the enemy-sync roles.
func isEnemyRole(role byte) bool { return role == RoleEnemyHost || role == RoleEnemyClient }

// SendEnemy sends one MsgEnemy payload on an enemy-sync connection.
func (c *Client) SendEnemy(payload []byte) error {
	if !isEnemyRole(c.role) {
		return errors.New("not an enemy-sync connection")
	}
	return c.send(MsgEnemy, payload)
}

// RunEnemy keeps c, an enemy-sync client (NewEnemyClient), connected to
// addr until ctx ends, exactly as RunCoop keeps a co-op client: each
// attempt is c.ConnectContext under ReconnectLoop's backoff, flap floor
// and fatal-error stop (a server that refuses the role, or that already
// has an enemy host, answers RejectRole, which is fatal). session runs
// once per live connection with send, which writes one 'E' payload;
// inbound payloads reach c.OnEnemy from the read loop meanwhile.
func RunEnemy(ctx context.Context, c *Client, addr string, connLog *ConnectLog, session func(ctx context.Context, send func(payload []byte) error) error) error {
	if !isEnemyRole(c.role) {
		return errors.New("RunEnemy needs an enemy-sync client (NewEnemyClient)")
	}
	return runRoleSession(ctx, c, addr, connLog, c.SendEnemy, session)
}

// runRoleSession is RunCoop's and RunEnemy's shared lifecycle: connect
// under ReconnectLoop, then run session for the life of each connection,
// polling the connection every coopDropPoll so a drop ends the session.
func runRoleSession(ctx context.Context, c *Client, addr string, connLog *ConnectLog, sendFn func([]byte) error, session func(ctx context.Context, send func(payload []byte) error) error) error {
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
		go func() { done <- session(ctx, sendFn) }()
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
