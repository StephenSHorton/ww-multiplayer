package mem

import (
	"encoding/binary"
	"math"
)

// ReadS8 reads one signed byte.
func ReadS8(m Reader, a uint32) (int8, error) {
	v, err := ReadU8(m, a)
	return int8(v), err
}

// ReadS16 reads a big-endian s16.
func ReadS16(m Reader, a uint32) (int16, error) {
	v, err := ReadU16(m, a)
	return int16(v), err
}

// ReadF32 reads a big-endian IEEE-754 f32.
func ReadF32(m Reader, a uint32) (float32, error) {
	v, err := ReadU32(m, a)
	return math.Float32frombits(v), err
}

// WriteS8 writes one signed byte.
func WriteS8(m Mem, a uint32, v int8) error {
	return WriteU8(m, a, uint8(v))
}

// WriteS16 writes a big-endian s16.
func WriteS16(m Mem, a uint32, v int16) error {
	return WriteU16(m, a, uint16(v))
}

// WriteF32 writes a big-endian IEEE-754 f32.
func WriteF32(m Mem, a uint32, v float32) error {
	return WriteU32(m, a, math.Float32bits(v))
}

// Big-endian helpers over a byte slice, for code that has already read a
// block (a struct, a save image) and decodes fields out of it. Each reads
// or writes at the start of b and panics, as encoding/binary does, if b
// is too short.

// U16BE decodes a big-endian u16.
func U16BE(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

// U32BE decodes a big-endian u32.
func U32BE(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// S16BE decodes a big-endian s16.
func S16BE(b []byte) int16 { return int16(binary.BigEndian.Uint16(b)) }

// F32BE decodes a big-endian IEEE-754 f32.
func F32BE(b []byte) float32 { return math.Float32frombits(binary.BigEndian.Uint32(b)) }

// PutU16BE encodes v big-endian.
func PutU16BE(b []byte, v uint16) { binary.BigEndian.PutUint16(b, v) }

// PutU32BE encodes v big-endian.
func PutU32BE(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }

// PutS16BE encodes v big-endian.
func PutS16BE(b []byte, v int16) { binary.BigEndian.PutUint16(b, uint16(v)) }

// PutF32BE encodes v as a big-endian IEEE-754 f32.
func PutF32BE(b []byte, v float32) { binary.BigEndian.PutUint32(b, math.Float32bits(v)) }
