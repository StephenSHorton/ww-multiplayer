package derive

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// snapAt returns a snapshot with table loaded (-1: none).
func snapAt(table int) *state.Snapshot { return &state.Snapshot{Table: int8(table)} }

// setSaved sets bit b of table t's saved memBit in s.
func setSaved(s *state.Snapshot, t int, b addr.Bit) {
	s.Save[addr.InfoOffSavedata+addr.OffMemBit(t)+b.Off] |= b.Mask
}

// setLive sets bit b of the live memBit in s.
func setLive(s *state.Snapshot, b addr.Bit) { s.Save[addr.InfoOffMemory+b.Off] |= b.Mask }

func must(b addr.Bit, err error) addr.Bit {
	if err != nil {
		panic(err)
	}
	return b
}
