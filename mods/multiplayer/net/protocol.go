package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// Message types
const (
	MsgJoin       byte = 'J' // Client -> Server: player name
	MsgWelcome    byte = 'W' // Server -> Client: assigned player ID (+ epoch)
	MsgPosition   byte = 'P' // Both: position update
	MsgPlayerList byte = 'L' // Server -> Client: list of connected players
	MsgLeave      byte = 'D' // Server -> Client: player disconnected
	MsgChat       byte = 'C' // Both: text message
	MsgPose       byte = 'M' // Both: full skeletal pose (Mtx[joints])

	// Heartbeat / liveness. A peer sends MsgPing carrying an 8-byte BE
	// monotonic-nanos timestamp; the receiver echoes the SAME bytes back as
	// MsgPong. The original sender then computes RTT = now - echoedTs. Pings
	// also serve as keepalives that reset the peer's read deadline, so a
	// quiet-but-live player (paused / in a menu) isn't reaped as an
	// ungraceful disconnect.
	//
	// NOTE: the design brief proposed MsgPong = 'P', but 'P' is already
	// MsgPosition. To avoid a wire collision that would corrupt position
	// sync, Pong uses 'Q' (next free byte). Ping is 'K' as specified. Old
	// peers ignore unknown types (no default case in any switch), so this
	// stays backward-compatible; framing is unchanged.
	MsgPing byte = 'K' // Both: liveness ping (8-byte BE monotonic-nanos)
	MsgPong byte = 'Q' // Both: liveness pong (echo of the ping's 8 bytes)

	// Version handshake (see handshake.go). A current client follows its
	// MsgJoin with a MsgHello; the server answers with MsgWelcome or, when
	// it won't take the client, with MsgReject and a close.
	MsgHello  byte = 'H' // Client -> Server: protocol version (after MsgJoin)
	MsgReject byte = 'R' // Server -> Client: [code:1][reason], then close

	// Co-op (protocol 2, docs/08-coop-design.md §4.4). Travels only on a
	// co-op connection (RoleCoop in the hello), both ways, between the
	// co-op pipeline and the server's CoopHandler; the payload is
	// [sub:1][body], encoded by package mods/coop/wire. The server
	// drops one that arrives on a player connection.
	MsgCoop byte = 'O' // Both: co-op sub-message

	// Enemy sync (the host-authoritative Moblin mirror, enemy.go). Travels
	// only on an enemy connection (RoleEnemyHost or RoleEnemyClient in the
	// hello); the payload is [sub:1][ver:1][body], encoded by package
	// mods/enemysync. The server relays a STATE from the host to every
	// enemy client, a HIT from an enemy client to the host and an ACK from
	// the host to the client it names, and drops one that arrives on a
	// player connection.
	MsgEnemy byte = 'E' // Both: enemy-sync sub-message

	// The status probe (status.go). A connection whose first frame is
	// MsgStatus, sent instead of MsgJoin with an empty payload, gets one
	// MsgStatus back (a JSON StatusBody) and is closed. Never sent on a
	// player connection; a server from before it closes the connection
	// unanswered.
	MsgStatus byte = 'S' // Client -> Server -> Client: status probe and answer
)

// Payload caps. The server enforces them at ingress so that every frame it
// builds from client input (chat and pose relays, the player list) stays far
// below MaxPayload: a relay too big to frame would fail to send to every
// recipient, and the broadcast path retires recipients whose writes fail.
const (
	// MaxPayload is the most data one frame can carry: the length field
	// is a u16.
	MaxPayload = 0xFFFF

	// MaxNameLen caps a player name in bytes (the TUI allows 20).
	MaxNameLen = 32

	// MaxChatLen caps one chat message's text in bytes (the TUI's chat
	// box allows 200 characters).
	MaxChatLen = 1024

	// MaxPoseJoints caps a pose frame's joint count. Link has 42, and the
	// C side refuses rigs over 128 (pose_joint_num in inject/src/mp/minilink.c).
	MaxPoseJoints = 128

	// MaxFaceBytes caps a pose frame's optional face-state trailer
	// (16 B today; faceStateWireBytes in package main).
	MaxFaceBytes = 64
)

// ErrFrameTooLarge is returned by WriteMessage for a payload over
// MaxPayload. Nothing is written, so the stream stays in sync.
var ErrFrameTooLarge = errors.New("frame payload too large")

// PlayerPosition is the core position data synced between players.
type PlayerPosition struct {
	PosX float32
	PosY float32
	PosZ float32
	RotX int16
	RotY int16
	RotZ int16
}

// Serialize writes position to 18 bytes (big-endian).
func (p *PlayerPosition) Serialize() []byte {
	buf := make([]byte, 18)
	binary.BigEndian.PutUint32(buf[0:4], math.Float32bits(p.PosX))
	binary.BigEndian.PutUint32(buf[4:8], math.Float32bits(p.PosY))
	binary.BigEndian.PutUint32(buf[8:12], math.Float32bits(p.PosZ))
	binary.BigEndian.PutUint16(buf[12:14], uint16(p.RotX))
	binary.BigEndian.PutUint16(buf[14:16], uint16(p.RotY))
	binary.BigEndian.PutUint16(buf[16:18], uint16(p.RotZ))
	return buf
}

// DeserializePosition reads 18 bytes into a PlayerPosition.
func DeserializePosition(buf []byte) *PlayerPosition {
	if len(buf) < 18 {
		return nil
	}
	return &PlayerPosition{
		PosX: math.Float32frombits(binary.BigEndian.Uint32(buf[0:4])),
		PosY: math.Float32frombits(binary.BigEndian.Uint32(buf[4:8])),
		PosZ: math.Float32frombits(binary.BigEndian.Uint32(buf[8:12])),
		RotX: int16(binary.BigEndian.Uint16(buf[12:14])),
		RotY: int16(binary.BigEndian.Uint16(buf[14:16])),
		RotZ: int16(binary.BigEndian.Uint16(buf[16:18])),
	}
}

// Message is a framed network message: [type:1][length:2][data:variable]
type Message struct {
	Type byte
	Data []byte
}

// WriteMessage sends a framed message to a writer. A payload over
// MaxPayload is refused with ErrFrameTooLarge before anything is written:
// the u16 length field would otherwise wrap, and the receiver would parse
// the rest of the payload as further frames.
func WriteMessage(w io.Writer, msgType byte, data []byte) error {
	if len(data) > MaxPayload {
		return fmt.Errorf("%w: type %q, %d bytes (max %d)", ErrFrameTooLarge, msgType, len(data), MaxPayload)
	}
	header := make([]byte, 3)
	header[0] = msgType
	binary.BigEndian.PutUint16(header[1:3], uint16(len(data)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if len(data) > 0 {
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write data: %w", err)
		}
	}
	return nil
}

// ReadMessage reads a framed message from a reader.
func ReadMessage(r io.Reader) (*Message, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	msgType := header[0]
	length := binary.BigEndian.Uint16(header[1:3])

	data := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("read data: %w", err)
		}
	}

	return &Message{Type: msgType, Data: data}, nil
}

// WelcomeMessage builds the server->client welcome payload:
//
//	[id:1][serverEpoch:8 BE][protocolVersion:2 BE]
//
// serverEpoch is constant per Server instance (its boot-time nanos) so a
// client can detect a server restart across a reconnect. protocolVersion is
// the version the session runs: always ProtocolVersion, since this server
// rejects any other. Older readers take only the fields they know from the
// front (v0.2.x reads Data[0], the first epoch-aware build Data[0:9]), so
// the tail stays backward-compatible. A welcome too short to carry the
// version tells a current client that the server predates the handshake.
func WelcomeMessage(id byte, serverEpoch uint64) []byte {
	data := make([]byte, 11)
	data[0] = id
	binary.BigEndian.PutUint64(data[1:9], serverEpoch)
	binary.BigEndian.PutUint16(data[9:11], ProtocolVersion)
	return data
}

// ParseWelcome extracts the assigned id and (optional) server epoch from a
// welcome payload. epochOK is false for a legacy 1-byte welcome (epoch 0).
func ParseWelcome(data []byte) (id byte, serverEpoch uint64, epochOK bool) {
	if len(data) < 1 {
		return 0, 0, false
	}
	id = data[0]
	if len(data) >= 9 {
		return id, binary.BigEndian.Uint64(data[1:9]), true
	}
	return id, 0, false
}

// WelcomeProtocolVersion extracts the protocol version from a welcome
// payload. ok is false for a welcome from a server that predates the
// version handshake (v0.2.x sends 1 byte, the first epoch-aware build 9).
func WelcomeProtocolVersion(data []byte) (version uint16, ok bool) {
	if len(data) < 11 {
		return 0, false
	}
	return binary.BigEndian.Uint16(data[9:11]), true
}

// PingPayload encodes an 8-byte BE timestamp for a MsgPing/MsgPong.
func PingPayload(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}

// ParsePingTimestamp decodes the 8-byte BE timestamp echoed in a ping/pong
// payload. ok is false if the payload is shorter than 8 bytes.
func ParsePingTimestamp(data []byte) (ts int64, ok bool) {
	if len(data) < 8 {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(data[:8])), true
}

// PositionMessage adds a player ID to a position update.
// Format: [playerID:1][position:18] = 19 bytes
func PositionMessage(playerID byte, pos *PlayerPosition) []byte {
	data := make([]byte, 19)
	data[0] = playerID
	copy(data[1:], pos.Serialize())
	return data
}

// ParsePositionMessage extracts player ID and position from a position message.
func ParsePositionMessage(data []byte) (byte, *PlayerPosition) {
	if len(data) < 19 {
		return 0, nil
	}
	return data[0], DeserializePosition(data[1:])
}

// Chat-message wire format:
//
//	client -> server:  [text]                     (plain UTF-8 bytes)
//	server -> client:  [nameLen:1][name][text]     (sender name + text)
//
// The client sends only the text; the server stamps the authoritative sender
// name onto the relay (a client can't forge another player's name) using the
// structured [nameLen][name][text] layout below so the receiver can split the
// sender from the text cleanly (a plain "name: text" string can't be un-joined
// reliably when a name itself contains ": "). nameLen is a single byte, so the
// name is clamped to 255 bytes — fine for the short display names this tool
// uses (the server only admits names up to MaxNameLen).

// ChatRelayMessage builds the server->client chat payload
// [nameLen:1][name][text]. The name is clamped to 255 bytes.
func ChatRelayMessage(name, text string) []byte {
	nb := []byte(name)
	if len(nb) > 255 {
		nb = nb[:255]
	}
	buf := make([]byte, 0, 1+len(nb)+len(text))
	buf = append(buf, byte(len(nb)))
	buf = append(buf, nb...)
	buf = append(buf, text...)
	return buf
}

// ParseChatRelay extracts the sender name and text from a chat relay payload.
// It returns an error for a malformed / too-short payload (never panics), so
// a garbled or foreign frame is simply ignored by the caller.
func ParseChatRelay(data []byte) (name, text string, err error) {
	if len(data) < 1 {
		return "", "", errors.New("chat relay: empty payload")
	}
	nameLen := int(data[0])
	if 1+nameLen > len(data) {
		return "", "", fmt.Errorf("chat relay: %d-byte name overruns the %d-byte payload", nameLen, len(data))
	}
	return string(data[1 : 1+nameLen]), string(data[1+nameLen:]), nil
}

// PlayerEntry is one row of a MsgPlayerList. Color is the player's tunic
// color (docs/09-player-colors.md); HasColor is false for a list from a
// server that predates colors.
type PlayerEntry struct {
	ID       byte
	Name     string
	Color    playercolor.Color
	HasColor bool
}

// Player-list wire format (server -> client, the full roster, sent after
// every join and leave):
//
//	[count:1] then count x [id:1][nameLen:1][name]
//	then, from docs/09, the color trailer:
//	[colorTag:1 = 0xC0][count:1] then count x [id:1][r:1][g:1][b:1]
//
// Older clients ignore the trailer (bytes after the last entry). The trailer
// is sent only when every entry has a color, with its IDs in entry order,
// and a client takes it only in exactly that form; anything else is
// ignored, and the roster still applies with no colors.

// playerListColorTag opens the color trailer of a MsgPlayerList.
const playerListColorTag = 0xC0

// PlayerListMessage encodes a MsgPlayerList payload. It fails rather than
// wrap a length byte: more than 255 entries, or a name over 255 bytes. The
// server can produce neither (IDs are 1..255, and names are capped at
// MaxNameLen when the player joins).
func PlayerListMessage(players []PlayerEntry) ([]byte, error) {
	if len(players) > 255 {
		return nil, fmt.Errorf("player list: %d entries (max 255)", len(players))
	}
	data := []byte{byte(len(players))}
	for _, p := range players {
		if len(p.Name) > 255 {
			return nil, fmt.Errorf("player list: player %d's name is %d bytes (max 255)", p.ID, len(p.Name))
		}
		data = append(data, p.ID, byte(len(p.Name)))
		data = append(data, p.Name...)
	}
	if len(players) == 0 {
		return data, nil
	}
	for _, p := range players {
		if !p.HasColor {
			return data, nil
		}
	}
	data = append(data, playerListColorTag, byte(len(players)))
	for _, p := range players {
		data = append(data, p.ID, p.Color.R, p.Color.G, p.Color.B)
	}
	return data, nil
}

// ParsePlayerList decodes a MsgPlayerList payload. It returns an error (never
// panics) for an empty payload or an entry that runs past the end. After the
// last entry it reads the color trailer if present; any other bytes there
// are ignored, so a later server can append fields.
func ParsePlayerList(data []byte) ([]PlayerEntry, error) {
	if len(data) < 1 {
		return nil, errors.New("player list: empty payload")
	}
	count := int(data[0])
	players := make([]PlayerEntry, 0, count)
	offset := 1
	for i := 0; i < count; i++ {
		if offset+2 > len(data) {
			return nil, fmt.Errorf("player list: entry %d of %d is cut off at byte %d", i+1, count, offset)
		}
		id, nameLen := data[offset], int(data[offset+1])
		offset += 2
		if offset+nameLen > len(data) {
			return nil, fmt.Errorf("player list: entry %d of %d has a %d-byte name that runs past the %d-byte payload", i+1, count, nameLen, len(data))
		}
		players = append(players, PlayerEntry{ID: id, Name: string(data[offset : offset+nameLen])})
		offset += nameLen
	}
	parseColorTrailer(players, data[offset:])
	return players, nil
}

// parseColorTrailer applies a MsgPlayerList color trailer to players when
// the trailer is in its one valid form (see the wire format above). It
// leaves players uncolored otherwise: colors are cosmetic, so a list with a
// bad trailer still applies.
func parseColorTrailer(players []PlayerEntry, tail []byte) {
	n := len(players)
	if n == 0 || len(tail) < 2+4*n || tail[0] != playerListColorTag || int(tail[1]) != n {
		return
	}
	rows := tail[2:]
	for i := range players {
		if rows[4*i] != players[i].ID {
			return
		}
	}
	for i := range players {
		r := rows[4*i:]
		players[i].Color = playercolor.Color{R: r[1], G: r[2], B: r[3]}
		players[i].HasColor = true
	}
}

// Pose-message wire format (game wants raw GameCube big-endian Mtx layout
// so the receiver can WriteProcessMemory straight into mpNodeMtx with no
// byteswap):
//
//   client -> server:  [joints:u16][_pad:u16][Mtx[joints]:48*N B][face?:N B]
//   server -> client:  [playerID:1][joints:u16][_pad:u16][Mtx[joints]:48*N B][face?:N B]
//
// For Link this is 4 + 2016 = 2020 B sender-side, +1 B = 2021 B relayed.
// The 2-byte pad keeps the matrices 4-byte aligned after the playerID.
//
// Joint count is shipped explicitly so the protocol survives if we ever
// drive a non-Link rig (e.g. a child puppet with a different skeleton).
//
// Session 9: an optional face-state suffix follows the matrix block.
// Length is implicit (= total payload - 4 - joints*48). If zero, the
// receiver leaves face_state untouched. Zero-length is the legacy
// format from v0.1.x; a v0.2 sender appends 8 bytes (eye-only) or
// more. Both ends share the layout via mailboxFaceState* constants.

// SerializePose builds a raw client->server pose payload. `matrices` must
// already be GameCube-format big-endian bytes (i.e. exactly what was just
// read out of mpNodeMtx) — no conversion happens here. `face` is an
// optional trailing payload (len 0 if unused). It returns nil unless
// 1 <= joints <= MaxPoseJoints, len(matrices) == joints*48 and
// len(face) <= MaxFaceBytes, the limits the server enforces.
func SerializePose(joints int, matrices []byte, face []byte) []byte {
	if joints <= 0 || joints > MaxPoseJoints || len(matrices) != joints*48 || len(face) > MaxFaceBytes {
		return nil
	}
	buf := make([]byte, 4+len(matrices)+len(face))
	binary.BigEndian.PutUint16(buf[0:2], uint16(joints))
	// buf[2:4] = padding (zero)
	copy(buf[4:], matrices)
	if len(face) > 0 {
		copy(buf[4+len(matrices):], face)
	}
	return buf
}

// DeserializePose parses the joints + matrix-bytes + (optional) face
// suffix from a payload (used both for client->server frames and the
// post-playerID slice of server->client frames). It returns an error for a
// truncated payload, a joint count outside 1..MaxPoseJoints, or a face
// trailer over MaxFaceBytes; the server drops such frames at ingress.
func DeserializePose(data []byte) (joints int, matrices, face []byte, err error) {
	if len(data) < 4 {
		return 0, nil, nil, fmt.Errorf("pose: %d-byte payload is shorter than its 4-byte header", len(data))
	}
	joints = int(binary.BigEndian.Uint16(data[0:2]))
	if joints <= 0 || joints > MaxPoseJoints {
		return 0, nil, nil, fmt.Errorf("pose: joint count %d outside 1..%d", joints, MaxPoseJoints)
	}
	need := 4 + joints*48
	if len(data) < need {
		return 0, nil, nil, fmt.Errorf("pose: %d joints need %d bytes, payload has %d", joints, need, len(data))
	}
	if len(data)-need > MaxFaceBytes {
		return 0, nil, nil, fmt.Errorf("pose: %d-byte face trailer (max %d)", len(data)-need, MaxFaceBytes)
	}
	return joints, data[4:need], data[need:], nil
}

// PoseRelayMessage builds the server->client pose frame: same payload
// as the client sent, with the sender's player ID prepended.
func PoseRelayMessage(playerID byte, joints int, matrices []byte, face []byte) []byte {
	body := SerializePose(joints, matrices, face)
	if body == nil {
		return nil
	}
	out := make([]byte, 1+len(body))
	out[0] = playerID
	copy(out[1:], body)
	return out
}

// ParsePoseRelayMessage decodes a server->client pose frame into
// (sender ID, joint count, raw matrix bytes ready to write straight
// into mpNodeMtx, optional face-state suffix), or an error under the
// same rules as DeserializePose.
func ParsePoseRelayMessage(data []byte) (playerID byte, joints int, matrices, face []byte, err error) {
	if len(data) < 1 {
		return 0, 0, nil, nil, errors.New("pose relay: empty payload")
	}
	joints, matrices, face, err = DeserializePose(data[1:])
	if err != nil {
		return 0, 0, nil, nil, err
	}
	return data[0], joints, matrices, face, nil
}
