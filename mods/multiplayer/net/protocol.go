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

// Pose-message wire format. The game wants raw GameCube big-endian Mtx
// layout (3 rows x 4 float32: rotation in columns 0..2, translation in
// column 3) so the receiver can write straight into mpNodeMtx; on the wire
// it is usually quantized (wind#85):
//
//   client -> server:  [joints:u16][format:u8][_pad:u8][block][face?:N B]
//   server -> client:  [playerID:1] + the client's payload, byte for byte
//
// format 0 (PoseRaw):   block = Mtx[joints], 48*N B, as read from mpNodeMtx.
// format 1 (PoseInt16): block = [origin: 3 x f32] + 24*N B. Per joint,
//   9 x i16 rotation elements at 1/PoseRotScale, then 3 x i16 translation
//   at 1/PoseTransScale unit, relative to origin (joint 0's translation).
//   For Link: 4 + 12 + 1008 = 1024 B against 2020 B raw.
//
// A sender quantizes when every value fits (|rotation| < 4, every joint
// within 1024 units of joint 0, all finite) and sends that frame raw
// otherwise. Receivers always get raw Mtx bytes back from DeserializePose.
// Deltas against the previous frame do not fit: relay scoping drops frames
// per receiver, so each frame must stand alone.
//
// Joint count is shipped explicitly so the protocol survives if we ever
// drive a non-Link rig (e.g. a child puppet with a different skeleton).
//
// Session 9: an optional face-state suffix follows the matrix block; its
// length is what is left of the payload. If zero, the receiver leaves
// face_state untouched. Both ends share the layout via mailboxFaceState*
// constants.

const (
	// PoseRaw and PoseInt16 are the pose block formats (header byte 2).
	PoseRaw   byte = 0
	PoseInt16 byte = 1

	// PoseRotScale: a rotation element v travels as round(v*PoseRotScale),
	// a step of 1/8192 (~0.00012) with |v| < 4 (room for model scale).
	PoseRotScale = 8192
	// PoseTransScale: a translation offset d travels as round(d*32), a
	// step of 1/32 unit with |d| < 1024 units of joint 0.
	PoseTransScale = 32

	poseRawJoint   = 48
	poseInt16Joint = 24
	poseOriginLen  = 12
)

// SerializePose builds a client->server pose payload. `matrices` must be
// GameCube-format big-endian Mtx bytes (exactly what was read out of
// mpNodeMtx). It quantizes them when every value fits and sends them raw
// otherwise. `face` is an optional trailing payload (len 0 if unused). It
// returns nil unless 1 <= joints <= MaxPoseJoints, len(matrices) ==
// joints*48 and len(face) <= MaxFaceBytes, the limits the server enforces.
func SerializePose(joints int, matrices []byte, face []byte) []byte {
	if joints <= 0 || joints > MaxPoseJoints || len(matrices) != joints*poseRawJoint || len(face) > MaxFaceBytes {
		return nil
	}
	if q := quantizePose(joints, matrices); q != nil {
		buf := make([]byte, 4, 4+len(q)+len(face))
		binary.BigEndian.PutUint16(buf[0:2], uint16(joints))
		buf[2] = PoseInt16
		buf = append(buf, q...)
		return append(buf, face...)
	}
	return serializePoseRaw(joints, matrices, face)
}

func serializePoseRaw(joints int, matrices []byte, face []byte) []byte {
	buf := make([]byte, 4+len(matrices)+len(face))
	binary.BigEndian.PutUint16(buf[0:2], uint16(joints))
	buf[2] = PoseRaw
	copy(buf[4:], matrices)
	copy(buf[4+len(matrices):], face)
	return buf
}

func mtxFloat(m []byte, joint, i int) float64 {
	return float64(math.Float32frombits(binary.BigEndian.Uint32(m[joint*poseRawJoint+i*4:])))
}

// quantizePose returns the PoseInt16 block, or nil when a value does not
// fit (the frame then goes raw).
func quantizePose(joints int, m []byte) []byte {
	var origin [3]float64
	for r := 0; r < 3; r++ {
		origin[r] = mtxFloat(m, 0, r*4+3)
	}
	out := make([]byte, poseOriginLen, poseOriginLen+joints*poseInt16Joint)
	for r := 0; r < 3; r++ {
		binary.BigEndian.PutUint32(out[r*4:], math.Float32bits(float32(origin[r])))
	}
	put := func(v float64) bool {
		if math.IsNaN(v) || v < -32767.5 || v > 32767.5 {
			return false
		}
		out = binary.BigEndian.AppendUint16(out, uint16(int16(math.Round(v))))
		return true
	}
	for j := 0; j < joints; j++ {
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				if !put(mtxFloat(m, j, r*4+c) * PoseRotScale) {
					return nil
				}
			}
		}
		for r := 0; r < 3; r++ {
			if !put((mtxFloat(m, j, r*4+3) - origin[r]) * PoseTransScale) {
				return nil
			}
		}
	}
	// A NaN or infinite origin made every offset NaN, which put refused.
	return out
}

// dequantizePose turns a PoseInt16 block back into raw Mtx bytes.
func dequantizePose(joints int, q []byte) []byte {
	var origin [3]float64
	for r := 0; r < 3; r++ {
		origin[r] = float64(math.Float32frombits(binary.BigEndian.Uint32(q[r*4:])))
	}
	q = q[poseOriginLen:]
	out := make([]byte, joints*poseRawJoint)
	for j := 0; j < joints; j++ {
		in := q[j*poseInt16Joint:]
		at := func(k int) float64 { return float64(int16(binary.BigEndian.Uint16(in[k*2:]))) }
		dst := out[j*poseRawJoint:]
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				binary.BigEndian.PutUint32(dst[(r*4+c)*4:], math.Float32bits(float32(at(r*3+c)/PoseRotScale)))
			}
			binary.BigEndian.PutUint32(dst[(r*4+3)*4:], math.Float32bits(float32(origin[r]+at(9+r)/PoseTransScale)))
		}
	}
	return out
}

// DeserializePose parses a client->server pose payload (also the
// post-playerID slice of server->client frames) into the joint count, raw
// Mtx bytes ready for mpNodeMtx (dequantized if it came as PoseInt16), and
// the optional face suffix. It returns an error for a truncated payload, an
// unknown format, a joint count outside 1..MaxPoseJoints, or a face trailer
// over MaxFaceBytes; the server drops such frames at ingress.
func DeserializePose(data []byte) (joints int, matrices, face []byte, err error) {
	if len(data) < 4 {
		return 0, nil, nil, fmt.Errorf("pose: %d-byte payload is shorter than its 4-byte header", len(data))
	}
	joints = int(binary.BigEndian.Uint16(data[0:2]))
	if joints <= 0 || joints > MaxPoseJoints {
		return 0, nil, nil, fmt.Errorf("pose: joint count %d outside 1..%d", joints, MaxPoseJoints)
	}
	var block int
	switch data[2] {
	case PoseRaw:
		block = joints * poseRawJoint
	case PoseInt16:
		block = poseOriginLen + joints*poseInt16Joint
	default:
		return 0, nil, nil, fmt.Errorf("pose: unknown format %d", data[2])
	}
	need := 4 + block
	if len(data) < need {
		return 0, nil, nil, fmt.Errorf("pose: %d joints need %d bytes, payload has %d", joints, need, len(data))
	}
	if len(data)-need > MaxFaceBytes {
		return 0, nil, nil, fmt.Errorf("pose: %d-byte face trailer (max %d)", len(data)-need, MaxFaceBytes)
	}
	if data[2] == PoseInt16 {
		return joints, dequantizePose(joints, data[4:need]), data[need:], nil
	}
	return joints, data[4:need], data[need:], nil
}

// PoseRelayFrame builds the server->client pose frame from a payload the
// server has already checked with DeserializePose: the sender's ID, then
// its bytes unchanged, so a quantized pose stays quantized on the way out.
func PoseRelayFrame(playerID byte, payload []byte) []byte {
	out := make([]byte, 1+len(payload))
	out[0] = playerID
	copy(out[1:], payload)
	return out
}

// PoseRelayMessage builds the server->client pose frame: same payload
// as the client sent, with the sender's player ID prepended.
func PoseRelayMessage(playerID byte, joints int, matrices []byte, face []byte) []byte {
	body := SerializePose(joints, matrices, face)
	if body == nil {
		return nil
	}
	return PoseRelayFrame(playerID, body)
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
