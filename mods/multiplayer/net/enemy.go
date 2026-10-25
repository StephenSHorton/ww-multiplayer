package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"strings"
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
//	PHIT  (sub 4)  host -> the enemy client whose ID is at
//	               EnemyClientIDOff, as an ACK (an enemy hit on that
//	               player's remote body, for its client to replay)
//	PACK  (sub 5)  enemy client -> the host, stamped like a HIT (what the
//	               client did with a PHIT)
//	PSTATE (sub 6) enemy client -> the host, stamped like a HIT (the
//	               client Link's stage, room and combat state)
//
// Relay scoping (place.go): the server keeps each enemy client's last
// PSTATE place. A client whose fresh PSTATE (EnemyPStateStale) is not
// loading and names another stage or room than a STATE gets that STATE
// thinned: its 52-byte header alone, count 0, with EnemyStateFlagThin set.
// The header is what such a client's gate reads (the host's stage and
// room, epoch, seq, flags, kind hash: mods/enemysync Mirror.Tick disarms
// on the mismatch exactly as before); the slots, up to 1 KB at 60 Hz, are
// what it never uses. Withholding the STATE instead would read as a stall
// on the client (its StaleHold freezes the copies). A client with no
// fresh PSTATE, a loading one or the STATE's own place gets it whole, and
// so does everyone a STATE without EnemyStateFlagInScope (a keepalive)
// goes to. A client that still gets a thinned STATE naming its own place
// (its PSTATE lags its arrival) waits for the whole one (Mirror.Tick).
// The thin bit is the server's: it is cleared on what the host sends.
//
// Origin binding: the player id stamped into a HIT and a PSTATE names the
// player of the connection's name, so only one enemy client per name
// (lobbyKey) gets it: the earliest-connected one that is live (a PSTATE
// within EnemyPStateStale), or this one when no other is. Another client
// under the same name stamps 0 (the host attributes to its own Link and
// sends no PHIT), as an ambiguous name always did. After an unclean drop
// the old connection lingers until the read timeout but sends no PSTATE,
// so the reconnected one takes the name over once the old PSTATE is stale
// (datstarkey/ww-online's ClaimOrigin rule, WWOnline.Server/Hubs/
// PlayerEventRelay.cs:55-109).
//
// Since wire version 3 (the remote-Link design,
// ww-multiplayer-handoff/design/remote-link-design.md §8.4) the server
// also stamps the player id of the enemy connection's player into a HIT
// (EnemyHitPlayerIDOff) and a PSTATE (EnemyPStatePlayerIDOff): the host
// learns which player (whose pose it draws as a mini-Link) sent it. The
// pair is made by name: both enemy pipelines join with their player
// session's name, so the player connection whose name equals the enemy
// connection's (case-insensitively, the lobby's lobbyKey) is the one. No
// match, or two players sharing the name (CLI sessions without a lobby do
// not force unique names), stamps 0: the host then attributes to its own
// Link and sends no PHIT.
//
// Anything else on an enemy connection is dropped, as is a MsgEnemy on a
// player connection. The server checks only the frame shape at ingress
// (sub byte, exact length); package mods/enemysync encodes and decodes
// the fields.

// The MsgEnemy payload shapes the server checks at ingress. Package
// mods/enemysync builds and parses them; the sizes are pinned there too.
const (
	EnemySubState  byte = 1
	EnemySubHit    byte = 2
	EnemySubAck    byte = 3
	EnemySubPHit   byte = 4
	EnemySubPAck   byte = 5
	EnemySubPState byte = 6

	// EnemyStateHeaderLen is a STATE's fixed header (sub, ver, flags,
	// count, epoch, seq, hostBeat, host_ms, stage[8], room, dead_mask,
	// host kind mask, pad, dead[4] u32, kind hash u32: 52 since wire
	// version 4); EnemySlotLen follows it EnemyMaxSlots times at most (48
	// since wire version 2 added the animation identity, 64 since version
	// 4 added wflags, kind, pkey, handle and aux[4]). Version 4 lists up to
	// 16 candidates; each client picks the 4 its REL mirrors
	// (ww-multiplayer-handoff/design/enemy-mirror-general.json "wire").
	EnemyStateHeaderLen = 52
	EnemySlotLen        = 64
	EnemyMaxSlots       = 16
	// EnemyHitLen and EnemyAckLen are the fixed HIT and ACK sizes (the HIT
	// 48 since version 2 added the sword object's Se/HitMark/Spl/SPrm, 52
	// since version 3 added cut_type, cut_count and the player id, 56 and
	// the ACK 20 since version 4 added the target's handle).
	EnemyHitLen = 56
	EnemyAckLen = 20
	// The remote-Link subs' fixed sizes (wire version 3;
	// inject/include/wwremote.h's header comment carries the layouts).
	EnemyPHitLen   = 60
	EnemyPAckLen   = 16
	EnemyPStateLen = 24
	// EnemyClientIDOff is the payload byte of every sub but STATE that
	// carries the enemy client's connection ID: the server stamps it into
	// a HIT, PACK and PSTATE and routes an ACK and a PHIT by it.
	EnemyClientIDOff = 2
	// EnemyHitPlayerIDOff and EnemyPStatePlayerIDOff are the bytes the
	// server stamps with the sender's player id (enemyPlayerIDLocked; 0 =
	// unmapped).
	EnemyHitPlayerIDOff    = 48
	EnemyPStatePlayerIDOff = 19

	// The STATE and PSTATE fields relay scoping reads (mods/enemysync
	// wire.go encodes them; TestScopeOffsetsPinned there pins these):
	// a STATE's flags, count, stage[8] and room; a PSTATE's flags,
	// stage[8] and room.
	EnemyStateFlagsOff  = 2
	EnemyStateCountOff  = 3
	EnemyStateStageOff  = 20
	EnemyStateRoomOff   = 28
	EnemyPStateFlagsOff = 3
	EnemyPStateStageOff = 8
	EnemyPStateRoomOff  = 16
	EnemyStageLen       = 8
	// EnemyStateFlagInScope is enemysync.StateFlagInScope: the host is in
	// play in a room (else a count-0 keepalive). EnemyStateFlagThin is
	// the server's: this STATE was thinned to its header. EnemyPSLoading
	// is enemysync.PSLoading: the client's game is not in play.
	EnemyStateFlagInScope byte = 1 << 2
	EnemyStateFlagThin    byte = 1 << 3
	EnemyPSLoading        byte = 0x80
)

// EnemyPStateStale is how long the server believes an enemy client's
// PSTATE place: enemysync.PStateStale, the host's own bound. A client
// sends one every 50 ms.
const EnemyPStateStale = 3 * time.Second

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
	case sub == EnemySubPHit && role == RoleEnemyHost:
		if len(data) != EnemyPHitLen {
			return fmt.Sprintf("%d-byte PHIT (want %d)", len(data), EnemyPHitLen)
		}
	case sub == EnemySubPAck && role == RoleEnemyClient:
		if len(data) != EnemyPAckLen {
			return fmt.Sprintf("%d-byte PACK (want %d)", len(data), EnemyPAckLen)
		}
	case sub == EnemySubPState && role == RoleEnemyClient:
		if len(data) != EnemyPStateLen {
			return fmt.Sprintf("%d-byte PSTATE (want %d)", len(data), EnemyPStateLen)
		}
	default:
		return fmt.Sprintf("sub %d from role %d", sub, role)
	}
	return ""
}

// enemyHostTakenReason is the MsgReject reason for a second RoleEnemyHost
// under another name. A second one under the same name replaces the first
// (handleEnemy).
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

	// seq orders enemy connections by when they registered (origin
	// binding). Set before the connection is visible, then read-only.
	seq uint64
	// The last PSTATE's place (relay scoping), under placeMu: relayEnemy
	// records it while holding s.mu only for reading.
	placeMu sync.Mutex
	place   enemyPlace
}

// enemyPlace is an enemy client's last PSTATE place.
type enemyPlace struct {
	stage   [EnemyStageLen]byte
	room    byte
	loading bool
	at      time.Time // zero: no PSTATE yet
}

// lastPlace returns c's last PSTATE place.
func (c *enemyConn) lastPlace() enemyPlace {
	c.placeMu.Lock()
	defer c.placeMu.Unlock()
	return c.place
}

// live reports whether c sent a PSTATE within EnemyPStateStale of now.
func (c *enemyConn) live(now time.Time) bool {
	p := c.lastPlace()
	return !p.at.IsZero() && now.Sub(p.at) <= EnemyPStateStale
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
//
// One enemy host per server. A second RoleEnemyHost under the same name
// (case-insensitive) is that host reconnecting while its old connection is
// not yet reaped (a pipeline restart, or a stalled process whose old
// connection lingers until the read timeout): it replaces the old one,
// which is closed. Under another name it is refused with RejectRole.
func (s *Server) handleEnemy(conn net.Conn, name string, role byte) {
	c := &enemyConn{name: name, role: role, conn: conn, srv: s, done: make(chan struct{})}
	s.mu.Lock()
	s.enemySeq++
	c.seq = s.enemySeq
	stopping := s.stopping()
	var id byte
	var ok, taken bool
	var replaced *enemyConn
	if !stopping {
		if role == RoleEnemyHost && s.enemyHost != nil && !strings.EqualFold(s.enemyHost.name, name) {
			taken = true
		} else if id, ok = s.allocEnemyIDLocked(); ok {
			c.id = id
			if role == RoleEnemyHost {
				// The old host's defer clears the slot only while it is
				// still its own, so it leaves this one alone.
				replaced, s.enemyHost = s.enemyHost, c
			} else {
				s.enemyClients[id] = c
			}
		}
	}
	s.mu.Unlock()
	if replaced != nil {
		replaced.close()
		s.log(fmt.Sprintf("%s (enemy host %d) replaced by a new connection under the same name", replaced.name, replaced.id))
	}
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
// a STATE to every enemy client (thinned for one in another place: relay
// scoping, above); a HIT, PACK or PSTATE to the host, stamped with c's ID
// (and a HIT and PSTATE with c's player id while c holds its name: origin
// binding, above); an ACK or PHIT to the client it names. A recipient
// whose write fails is closed, which ends its handleEnemy.
func (s *Server) relayEnemy(c *enemyConn, data []byte) {
	type out struct {
		ec   *enemyConn
		data []byte
	}
	var to []out
	now := s.now()
	if data[0] == EnemySubPState {
		var p enemyPlace
		copy(p.stage[:], data[EnemyPStateStageOff:EnemyPStateStageOff+EnemyStageLen])
		p.room, p.loading, p.at = data[EnemyPStateRoomOff], data[EnemyPStateFlagsOff]&EnemyPSLoading != 0, now
		c.placeMu.Lock()
		c.place = p
		c.placeMu.Unlock()
	}
	s.mu.RLock()
	if c.role == RoleEnemyHost && s.enemyHost != c {
		s.mu.RUnlock()
		return // a replaced host's last frames
	}
	switch data[0] {
	case EnemySubState:
		data[EnemyStateFlagsOff] &^= EnemyStateFlagThin
		var thin []byte
		full, thinned := 0, 0
		for _, ec := range s.enemyClients {
			if !s.scope || !stateOutOfPlace(data, ec.lastPlace(), now) {
				to = append(to, out{ec, data})
				full++
				continue
			}
			if thin == nil {
				thin = append([]byte(nil), data[:EnemyStateHeaderLen]...)
				thin[EnemyStateCountOff] = 0
				thin[EnemyStateFlagsOff] |= EnemyStateFlagThin
			}
			to = append(to, out{ec, thin})
			thinned++
		}
		s.countScope(func(st *ScopeStats) {
			st.StatesFull += full
			st.StatesThinned += thinned
			st.StateBytesSent += int64(full*(len(data)+3) + thinned*(EnemyStateHeaderLen+3))
			st.StateBytesSaved += int64(thinned * (len(data) - EnemyStateHeaderLen))
		})
	case EnemySubHit, EnemySubPAck, EnemySubPState:
		if s.enemyHost != nil {
			data[EnemyClientIDOff] = c.id
			var pid byte
			if data[0] != EnemySubPAck && s.enemyHoldsNameLocked(c, now) {
				pid = s.enemyPlayerIDLocked(c.name)
			}
			switch data[0] {
			case EnemySubHit:
				data[EnemyHitPlayerIDOff] = pid
			case EnemySubPState:
				data[EnemyPStatePlayerIDOff] = pid
			}
			to = append(to, out{s.enemyHost, data})
		}
	case EnemySubAck, EnemySubPHit:
		if ec, ok := s.enemyClients[data[EnemyClientIDOff]]; ok {
			to = append(to, out{ec, data})
		}
	}
	s.mu.RUnlock()
	for _, o := range to {
		if err := o.ec.write(MsgEnemy, o.data); err != nil {
			o.ec.close()
		}
	}
}

// stateOutOfPlace reports whether a STATE (data, well-shaped) goes thinned
// to an enemy client last at p: the STATE is in scope (not a keepalive)
// and has slots, and p is a fresh, not-loading PSTATE place naming another
// stage or room.
func stateOutOfPlace(data []byte, p enemyPlace, now time.Time) bool {
	if data[EnemyStateFlagsOff]&EnemyStateFlagInScope == 0 || len(data) == EnemyStateHeaderLen {
		return false // a keepalive, or nothing to save
	}
	if p.at.IsZero() || now.Sub(p.at) > EnemyPStateStale || p.loading {
		return false
	}
	return !bytes.Equal(p.stage[:], data[EnemyStateStageOff:EnemyStateStageOff+EnemyStageLen]) || p.room != data[EnemyStateRoomOff]
}

// enemyHoldsNameLocked reports whether enemy client c holds its name for
// the player-id stamp (origin binding, above): no other enemy client under
// the same lobbyKey registered before it and is live. The caller holds
// s.mu.
func (s *Server) enemyHoldsNameLocked(c *enemyConn, now time.Time) bool {
	key := lobbyKey(c.name)
	for _, o := range s.enemyClients {
		if o != c && o.seq < c.seq && lobbyKey(o.name) == key && o.live(now) {
			return false
		}
	}
	return true
}

// enemyPlayerIDLocked is the id of the player connection an enemy
// connection named name belongs to: the one broadcasting player whose
// name equals it case-insensitively (lobbyKey, the lobby's own
// comparison), else 0 (no such player, or more than one). Each human runs
// two player connections under one name (broadcast-pose and puppet-sync;
// pickColorLocked), and only the broadcaster sends positions and poses
// (handlePlayer's MsgPosition sets Position, MsgPose sets posed), so the
// puppet-sync twin is never a candidate. The broadcaster's id is also the
// one the host's roster carries (feed.Feeder.Views skips a remote with no
// Position). Player ids are 1..255 (allocIDLocked), so 0 never names a
// player. The caller holds s.mu (read or write; Position is written under
// it).
func (s *Server) enemyPlayerIDLocked(name string) byte {
	key := lobbyKey(name)
	var id byte
	for pid, p := range s.players {
		if lobbyKey(p.Name) != key || (p.Position == nil && !p.posed.Load()) {
			continue
		}
		if id != 0 {
			return 0 // the name is ambiguous
		}
		id = pid
	}
	return id
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
// has an enemy host under another name, answers RejectRole, which is
// fatal; under the same name this connection replaces it). session runs
// once per live connection with send, which writes one 'E' payload;
// inbound payloads reach c.OnEnemy from the read loop meanwhile.
//
// session runs on a goroutine of its own, so a panic in it (the enemy
// layer's per-tick work) is recovered here: it ends RunEnemy with a
// *SessionPanicError, which is fatal, instead of the process.
func RunEnemy(ctx context.Context, c *Client, addr string, connLog *ConnectLog, session func(ctx context.Context, send func(payload []byte) error) error) error {
	if !isEnemyRole(c.role) {
		return errors.New("RunEnemy needs an enemy-sync client (NewEnemyClient)")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var panicked error
	guarded := func(ctx context.Context, send func(payload []byte) error) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = &SessionPanicError{Value: r, Stack: debug.Stack()}
				mu.Lock()
				panicked = err
				mu.Unlock()
				cancel() // ReconnectLoop ignores run errors: end it here
			}
		}()
		return session(ctx, send)
	}
	err := runRoleSession(ctx, c, addr, connLog, c.SendEnemy, guarded)
	mu.Lock()
	defer mu.Unlock()
	if panicked != nil {
		return panicked
	}
	return err
}

// SessionPanicError is a panic recovered from a RunEnemy session. It is
// fatal (errors.Is ErrFatal): the same code would panic again on the next
// connection. Stack is the panicking goroutine's stack.
type SessionPanicError struct {
	Value any
	Stack []byte
}

func (e *SessionPanicError) Error() string { return fmt.Sprintf("enemy session panic: %v", e.Value) }

// Is reports whether target is ErrFatal.
func (e *SessionPanicError) Is(target error) bool { return target == ErrFatal }

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
