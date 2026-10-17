// Package mem is a bounds-checked door to emulated memory: small Reader
// and Mem interfaces over absolute GameCube addresses, and FakeMem, an
// in-memory MEM1 for tests (a live *emu.Dolphin is a Mem too). Nothing in it is
// co-op specific: the co-op packages use it, and so do wwapi/player
// (Link reads, warp, input) and mp-local shift (mp_local_shift.go). Every implementation
// confines access to MEM1, so a bad pointer read out of the game (or a bad
// offset in Go) becomes an error instead of a read or write somewhere else
// in Dolphin's process.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package mem

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Reader reads emulated MEM1 at absolute addresses (0x80000000 based, as
// the game's own pointers are). Read returns exactly n bytes or an error.
// It refuses a range that isn't inside MEM1 with an error matching
// ErrOutOfRange, and a non-positive length.
type Reader interface {
	Read(addr uint32, n int) ([]byte, error)
}

// Mem is a Reader that also writes. Write refuses the same ranges Read
// does.
type Mem interface {
	Reader
	Write(addr uint32, b []byte) error
}

// ErrOutOfRange is matched (errors.Is) by every access that leaves MEM1.
var ErrOutOfRange = errors.New("mem: access outside MEM1")

// RangeError is the error CheckRange returns.
type RangeError struct {
	Addr uint32
	N    int
}

func (e *RangeError) Error() string {
	return fmt.Sprintf("mem: %d bytes at 0x%08X are outside MEM1 0x%08X-0x%08X",
		e.N, e.Addr, addr.MEM1Base, addr.MEM1End)
}

// Is makes errors.Is(err, ErrOutOfRange) hold for a *RangeError.
func (e *RangeError) Is(target error) bool { return target == ErrOutOfRange }

// CheckRange returns nil if [a, a+n) is a non-empty range inside MEM1.
func CheckRange(a uint32, n int) error {
	if n <= 0 || a < addr.MEM1Base || uint64(a)+uint64(n) > uint64(addr.MEM1End) {
		return &RangeError{Addr: a, N: n}
	}
	return nil
}

// ReadU8 reads one byte.
func ReadU8(m Reader, a uint32) (uint8, error) {
	b, err := m.Read(a, 1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// ReadU16 reads a big-endian u16.
func ReadU16(m Reader, a uint32) (uint16, error) {
	b, err := m.Read(a, 2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

// ReadU32 reads a big-endian u32.
func ReadU32(m Reader, a uint32) (uint32, error) {
	b, err := m.Read(a, 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

// ReadString reads the NUL-terminated string in the n bytes at a, such as
// a stage name (char[8]). Without a NUL it returns all n bytes.
func ReadString(m Reader, a uint32, n int) (string, error) {
	b, err := m.Read(a, n)
	if err != nil {
		return "", err
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

// WriteU8 writes one byte.
func WriteU8(m Mem, a uint32, v uint8) error {
	return m.Write(a, []byte{v})
}

// WriteU16 writes a big-endian u16.
func WriteU16(m Mem, a uint32, v uint16) error {
	return m.Write(a, binary.BigEndian.AppendUint16(nil, v))
}

// WriteU32 writes a big-endian u32.
func WriteU32(m Mem, a uint32, v uint32) error {
	return m.Write(a, binary.BigEndian.AppendUint32(nil, v))
}
