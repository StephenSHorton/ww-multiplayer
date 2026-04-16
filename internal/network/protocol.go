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
