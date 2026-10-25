package mailbox

import (
	"encoding/binary"
	"math"
	"testing"
)

// wwboatHeader is the C source of the peer boats block constants.
const wwboatHeader = "../../inject/include/wwboat.h"

// TestBoatHeaderPins: every Go constant equals its WWBOAT_* define, and
// every define has a Go constant.
func TestBoatHeaderPins(t *testing.T) {
	pinHeader(t, wwboatHeader, headerDefines(t, wwboatHeader), map[string]uint64{
		"WWBOAT_OFF": uint64(BoatOff), "WWBOAT_SIZE": uint64(BoatSize),
		"WWBOAT_MAGIC_OFF": BoatMagicOff, "WWBOAT_FLAGS_OFF": BoatFlagsOff,
		"WWBOAT_STAT_DRAWN_OFF": BoatStatDrawnOff, "WWBOAT_STAT_ERR_OFF": BoatStatErrOff, "WWBOAT_ENTRIES_OFF": BoatEntriesOff,
		"WWBOAT_MAGIC": uint64(BoatMagic), "WWBOAT_MAX": BoatMax, "WWBOAT_E_SIZE": BoatESize,
		"WWBOAT_F_SAIL": uint64(BoatFSail), "WWBOAT_F_PARTS": uint64(BoatFParts),
		"WWBOAT_E_FLAGS": BoatEFlags, "WWBOAT_E_PART": BoatEPart, "WWBOAT_E_MAST_FRAME": BoatEMastFrame,
		"WWBOAT_E_HEAD_BCK": BoatEHeadBck, "WWBOAT_E_HEAD_FRAME": BoatEHeadFrame,
		"WWBOAT_E_R": BoatER, "WWBOAT_E_G": BoatEG, "WWBOAT_E_B": BoatEB, "WWBOAT_E_POS": BoatEPos,
		"WWBOAT_E_ANGLE": BoatEAngle, "WWBOAT_E_SAIL_ANGLE": BoatESailAngle, "WWBOAT_E_TILLER": BoatETiller,
		"WWBOAT_E_PART_A": BoatEPartA, "WWBOAT_E_PART_B": BoatEPartB, "WWBOAT_E_PAD": BoatEPad,
		"WWBOAT_EF_SHOW": BoatEfShow, "WWBOAT_EF_MAST_ON": BoatEfMastOn, "WWBOAT_EF_MAST_HIDE": BoatEfMastHide,
		"WWBOAT_EF_SAIL_ON": BoatEfSailOn, "WWBOAT_EF_PARKED": BoatEfParked,
		"WWBOAT_ERR_NONE": BoatErrNone, "WWBOAT_ERR_HEAP": BoatErrHeap, "WWBOAT_ERR_MODEL": BoatErrModel, "WWBOAT_ERR_SAIL": BoatErrSail,
	})
}

// TestBoatLayout: the block follows the visuals block, its parts tile it,
// an entry's fields tile an entry, and the high canary starts at its end
// and stops at the eye packets.
func TestBoatLayout(t *testing.T) {
	if BoatOff != VisOff+VisSize || BoatAddr != 0x804138E4 || BoatOff+BoatSize > MpEyePacketsOff {
		t.Fatalf("boats block at +0x%X (0x%08X), size 0x%X", BoatOff, BoatAddr, BoatSize)
	}
	if BoatStatErrOff+4 != BoatEntriesOff || BoatEntriesOff+BoatMax*BoatESize != int(BoatSize) {
		t.Error("boats block parts do not tile it")
	}
	if BoatEPartB+2 != BoatEPad || BoatEPad+6 != BoatESize || BoatETiller+2 != BoatEPartA || BoatEAngle+6 != BoatESailAngle ||
		BoatEPos+12 != BoatEAngle || BoatEB+1 != BoatEPos || BoatEPos%4 != 0 {
		t.Error("entry fields do not tile an entry")
	}
	if CanaryArenaHigh.Start != BoatRegion.End || CanaryArenaHigh.End != EyePacketsRegion.Start ||
		CanaryArenaHigh.Contains(BoatAddr, 4) || CanaryArenaHigh.Contains(BoatRegion.End-4, 4) || VisRegion.End != BoatRegion.Start {
		t.Errorf("canaries cover the boats block: high %+v, block %+v", CanaryArenaHigh, BoatRegion)
	}
}

// TestBoatBytes: an entry encodes at the header's offsets, the span holds
// at most BoatMax and leaves the rest zero, and the header is magic+flags.
func TestBoatBytes(t *testing.T) {
	e := BoatEntry{MastOn: true, SailOn: true, Part: 2, MastFrame: 7, HeadBck: 0x0C, HeadFrame: 3, R: 0x10, G: 0x20, B: 0x30,
		Pos: [3]float32{1.5, -2, 300000}, Angle: [3]int16{-0x100, 0x7FFF, 5}, SailAngle: -0x2000, Tiller: 0x123, PartA: 0x1800, PartB: -1}
	b := e.Bytes()
	if len(b) != BoatESize || b[BoatEFlags] != BoatEfShow|BoatEfMastOn|BoatEfSailOn || b[BoatEPart] != 2 || b[BoatEMastFrame] != 7 ||
		b[BoatEHeadBck] != 0x0C || b[BoatEHeadFrame] != 3 || b[BoatER] != 0x10 || b[BoatEG] != 0x20 || b[BoatEB] != 0x30 {
		t.Fatalf("bytes % x", b)
	}
	be := binary.BigEndian
	if math.Float32frombits(be.Uint32(b[BoatEPos+8:])) != 300000 || int16(be.Uint16(b[BoatEAngle:])) != -0x100 ||
		int16(be.Uint16(b[BoatESailAngle:])) != -0x2000 || be.Uint16(b[BoatETiller:]) != 0x123 ||
		be.Uint16(b[BoatEPartA:]) != 0x1800 || be.Uint16(b[BoatEPartB:]) != 0xFFFF {
		t.Errorf("fields % x", b)
	}
	for _, x := range b[BoatEPad:] {
		if x != 0 {
			t.Errorf("pad % x", b[BoatEPad:])
		}
	}
	p := BoatEntry{Parked: true, MastHide: true}.Bytes()
	if p[BoatEFlags] != BoatEfShow|BoatEfParked|BoatEfMastHide {
		t.Errorf("parked flags 0x%02X", p[BoatEFlags])
	}
	blk := BoatBlock{Flags: BoatFSail | BoatFParts, Entries: []BoatEntry{e, e, e, e}}
	span := blk.EntryBytes()
	if len(span) != BoatMax*BoatESize || span[2*BoatESize] != b[0] {
		t.Errorf("entry span %d bytes", len(span))
	}
	if one := (BoatBlock{Entries: []BoatEntry{e}}).EntryBytes(); one[BoatESize] != 0 || one[0] != b[0] {
		t.Error("an unused entry is not zero")
	}
	h := blk.Header()
	if len(h) != BoatStatDrawnOff || be.Uint32(h) != BoatMagic || be.Uint32(h[BoatFlagsOff:]) != BoatFSail|BoatFParts {
		t.Errorf("header % x", h)
	}
}
