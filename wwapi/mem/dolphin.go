package mem

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
)

// DolphinMem is Mem over a live Dolphin. dolphin.ReadAbsolute and
// WriteAbsolute (internal/dolphin/memory.go:117-135) turn any uint32 into
// an offset from Dolphin's RAM base without a bounds check: an address
// below 0x80000000 wraps, and one past 24 MiB lands in whatever Dolphin
// mapped next. DolphinMem checks the range first, exactly as FakeMem does,
// so a test against FakeMem holds on the real thing.
type DolphinMem struct {
	d *dolphin.Dolphin
}

var _ Mem = (*DolphinMem)(nil)

// NewDolphinMem wraps d. The caller still owns d (and closes it).
func NewDolphinMem(d *dolphin.Dolphin) *DolphinMem { return &DolphinMem{d: d} }

// Read implements Mem.
func (m *DolphinMem) Read(a uint32, n int) ([]byte, error) {
	if err := CheckRange(a, n); err != nil {
		return nil, err
	}
	b, err := m.d.ReadAbsolute(a, n)
	if err != nil {
		return nil, err
	}
	if len(b) != n {
		return nil, fmt.Errorf("mem: short read at 0x%08X: %d of %d bytes", a, len(b), n)
	}
	return b, nil
}

// Write implements Mem.
func (m *DolphinMem) Write(a uint32, b []byte) error {
	if err := CheckRange(a, len(b)); err != nil {
		return err
	}
	return m.d.WriteAbsolute(a, b)
}
