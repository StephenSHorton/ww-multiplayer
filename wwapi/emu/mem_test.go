package emu

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestReadWriteCheckRangeFirst: Read and Write refuse out-of-range access
// before they touch the Dolphin, so a nil one never gets dereferenced. On
// a live Dolphin those calls would read or write outside emulated RAM.
func TestReadWriteCheckRangeFirst(t *testing.T) {
	var m mem.Mem = (*Dolphin)(nil)
	if _, err := m.Read(0x00000000, 4); !errors.Is(err, mem.ErrOutOfRange) {
		t.Errorf("Read(0) = %v, want ErrOutOfRange", err)
	}
	if _, err := m.Read(addr.MEM1End-1, 2); !errors.Is(err, mem.ErrOutOfRange) {
		t.Errorf("straddling Read = %v, want ErrOutOfRange", err)
	}
	if err := m.Write(addr.MEM1End, []byte{1}); !errors.Is(err, mem.ErrOutOfRange) {
		t.Errorf("Write past MEM1 = %v, want ErrOutOfRange", err)
	}
	if err := m.Write(addr.MEM1Base, nil); !errors.Is(err, mem.ErrOutOfRange) {
		t.Errorf("empty Write = %v, want ErrOutOfRange", err)
	}
}
