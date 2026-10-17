package write

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// MaxStageLayer is the highest layer mNextStage may name: ACT0..ACTb
// (l_layerFuncTable_p, d_stage.cpp:2144-2150). MaxWipe is the highest
// mWipe: dScnPly_Draw JUT_ASSERTs it indexes its 12-entry l_wipeType
// (d_s_play.cpp:291-307).
const (
	MaxStageLayer = 11
	MaxWipe       = 11
)

// Field is one write of an escape (§6.7): Data at Addr, made in one memory
// write.
type Field struct {
	Addr uint32
	Data []byte
}

// StageNameByte reports whether b may be a byte of a stage name in
// mNextStage: NUL, or a letter, digit or '_', as every stage name is
// (and stage-hop takes).
func StageNameByte(b byte) bool {
	return b == 0 || b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// StageRequest is a stage change as mNextStage holds it
// (dStage_startStage_c, d_stage.h:949-952).
type StageRequest struct {
	Name  string
	Point int16
	Room  int8
	Layer int8
}

func (h StageRequest) String() string {
	return fmt.Sprintf("%s by point %d into room %d, layer %d", h.Name, h.Point, h.Room, h.Layer)
}

// Check reports whether the game takes h, as the WriteGuard's mNextStage
// rules have it (escapeRules): a name of 1-7 letters, digits or '_', a
// point 0..255, a room 0..63 and a layer -1..MaxStageLayer.
func (h StageRequest) Check() error {
	switch {
	case h.Name == "" || len(h.Name) >= addr.StageNameLen:
		return fmt.Errorf("stage name %q: want 1-%d characters", h.Name, addr.StageNameLen-1)
	case h.Point < 0 || h.Point > 0xFF:
		return fmt.Errorf("point %d: an entrance is 0..255 (a PLYR entry's angle.z, d_stage.cpp:1455-1463)", h.Point)
	case h.Room < 0 || int(h.Room) >= addr.RoomCount:
		return fmt.Errorf("room %d: want 0..%d", h.Room, addr.RoomCount-1)
	case h.Layer < -1 || h.Layer > MaxStageLayer:
		return fmt.Errorf("layer %d: want -1..%d", h.Layer, MaxStageLayer)
	}
	for i := range len(h.Name) {
		if b := h.Name[i]; b == 0 || !StageNameByte(b) {
			return fmt.Errorf("stage name %q: only letters, digits and '_'", h.Name)
		}
	}
	return nil
}

// Bytes is h's dStage_startStage_c: name[8] (NUL-padded, as
// dStage_startStage_c::set strcpys it, d_stage.cpp:112-117), point s16,
// room s8, layer s8.
func (h StageRequest) Bytes() [addr.StageSize]byte {
	var b [addr.StageSize]byte
	copy(b[addr.StageOffName:addr.StageOffName+addr.StageNameLen-1], h.Name)
	binary.BigEndian.PutUint16(b[addr.StageOffPoint:], uint16(h.Point))
	b[addr.StageOffRoomNo] = byte(h.Room)
	b[addr.StageOffLayer] = byte(h.Layer)
	return b
}

// Fields are the writes that ask the game for h, as ApplyFields makes
// them: mNextStage's twelve bytes, mWipe 0 (l_wipeType[0], the plain fade,
// d_s_play.cpp:291-303), then mEnable 1 last, the byte dScnPly_Draw acts
// on (d_s_play.cpp:291-314). M0's stage-hop writes the same bytes in the
// same order (coop_diag.go).
func (h StageRequest) Fields() []Field {
	b := h.Bytes()
	return []Field{
		{Addr: addr.NextStage, Data: b[:]},
		{Addr: addr.NextStage + addr.NextOffWipe, Data: []byte{0}},
		{Addr: addr.NextStage + addr.NextOffEnable, Data: []byte{1}},
	}
}
