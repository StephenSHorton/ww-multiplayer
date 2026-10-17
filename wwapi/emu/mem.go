package emu

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// A *Dolphin is a mem.Mem over a live Dolphin. readAbsolute and
// writeAbsolute (memory.go) turn any uint32 into an offset from Dolphin's
// RAM base without a bounds check: an address below 0x80000000 wraps, and
// one past 24 MiB lands in whatever Dolphin mapped next. Read and Write
// check the range first, exactly as mem.FakeMem does, so a test against
// FakeMem holds on the real thing. Read and Write are the bounds-checked
// doors; ReadU32 and ReadF32 (memory.go) still go through the unchecked
// readAbsolute. Link's pointer, position and pose are read through Read,
// by package player.
var _ mem.Mem = (*Dolphin)(nil)

// Read implements mem.Reader.
func (d *Dolphin) Read(a uint32, n int) ([]byte, error) {
	if err := mem.CheckRange(a, n); err != nil {
		return nil, err
	}
	b, err := d.readAbsolute(a, n)
	if err != nil {
		return nil, err
	}
	if len(b) != n {
		return nil, fmt.Errorf("mem: short read at 0x%08X: %d of %d bytes", a, len(b), n)
	}
	return b, nil
}

// Write implements mem.Mem.
func (d *Dolphin) Write(a uint32, b []byte) error {
	if err := mem.CheckRange(a, len(b)); err != nil {
		return err
	}
	return d.writeAbsolute(a, b)
}
