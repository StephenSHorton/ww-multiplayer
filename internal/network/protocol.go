package network

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// Message types
const (
	MsgJoin       byte = 'J' // Client -> Server: player name
	MsgWelcome    byte = 'W' // Server -> Client: assigned player ID
	MsgPosition   byte = 'P' // Both: position update
	MsgPlayerList byte = 'L' // Server -> Client: list of connected players
	MsgLeave      byte = 'D' // Server -> Client: player disconnected
	MsgChat       byte = 'C' // Both: text message
	MsgPose       byte = 'M' // Both: full skeletal pose (Mtx[joints])
)

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

// WriteMessage sends a framed message to a writer.
func WriteMessage(w io.Writer, msgType byte, data []byte) error {
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

// Pose-message wire format (game wants raw GameCube big-endian Mtx layout
// so the receiver can WriteProcessMemory straight into mpNodeMtx with no
// byteswap):
//
//   client -> server:  [joints:u16][_pad:u16][Mtx[joints]:48*N B]
//   server -> client:  [playerID:1][joints:u16][_pad:u16][Mtx[joints]:48*N B]
//
// For Link this is 4 + 2016 = 2020 B sender-side, +1 B = 2021 B relayed.
// The 2-byte pad keeps the matrices 4-byte aligned after the playerID.
//
// Joint count is shipped explicitly so the protocol survives if we ever
// drive a non-Link rig (e.g. a child puppet with a different skeleton).

// SerializePose builds a raw client->server pose payload. `matrices` must
// already be GameCube-format big-endian bytes (i.e. exactly what was just
// read out of mpNodeMtx) — no conversion happens here.
func SerializePose(joints int, matrices []byte) []byte {
	if joints <= 0 || joints > 0xFFFF || len(matrices) != joints*48 {
		return nil
	}
	buf := make([]byte, 4+len(matrices))
	binary.BigEndian.PutUint16(buf[0:2], uint16(joints))
	// buf[2:4] = padding (zero)
	copy(buf[4:], matrices)
	return buf
}

// DeserializePose parses the joints + matrix-bytes pair from a payload
// (used both for client->server frames and the post-playerID slice of
// server->client frames).
func DeserializePose(data []byte) (int, []byte) {
	if len(data) < 4 {
		return 0, nil
	}
	joints := int(binary.BigEndian.Uint16(data[0:2]))
	need := 4 + joints*48
	if joints <= 0 || len(data) < need {
		return 0, nil
	}
	return joints, data[4:need]
}

// PoseRelayMessage builds the server->client pose frame: same payload
// as the client sent, with the sender's player ID prepended.
func PoseRelayMessage(playerID byte, joints int, matrices []byte) []byte {
	body := SerializePose(joints, matrices)
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
// into mpNodeMtx).
func ParsePoseRelayMessage(data []byte) (byte, int, []byte) {
	if len(data) < 5 {
		return 0, 0, nil
	}
	joints, matrices := DeserializePose(data[1:])
	return data[0], joints, matrices
}
