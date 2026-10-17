package player

import (
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// store writes b at a as the game would (never recorded).
func store(t *testing.T, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
