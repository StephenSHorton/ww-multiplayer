package mailbox

import (
	"encoding/binary"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The peer boats block in the mailbox slack (inject/include/wwboat.h,
// whose WWBOAT_* defines these mirror; boat_test.go pins each one to the
// header): the other players' King of Red Lions, which the REL draws
// (inject/rel/boats.c). 0x88 bytes at Mailbox+BoatOff, right after the
// visuals block. Go (multiplayer's boatsync) writes the magic, the flags
// and the entries; the REL writes its two counters.
const (
	BoatOff  uint32 = 0x9E4 // block start, from Mailbox (0x804138E4)
	BoatSize uint32 = 0x88  // ends +0xA6C (0x8041396C), where CanaryArenaHigh starts

	BoatMagicOff     = 0x00 // u32 Go: BoatMagic, 0 = off
	BoatFlagsOff     = 0x04 // u32 Go: BoatF*
	BoatStatDrawnOff = 0x08 // u32 REL: boats entered (wraps)
	BoatStatErrOff   = 0x0C // u32 REL: last BoatErr*
	BoatEntriesOff   = 0x10 // entry[BoatMax]

	BoatMagic uint32 = 0x57574254 // 'WWBT'
	BoatMax          = 3
	BoatESize        = 0x28

	BoatFSail  uint32 = 0x01 // draw the sail cloth
	BoatFParts uint32 = 0x02 // draw the cannon or crane

	BoatEFlags     = 0x00
	BoatEPart      = 0x01
	BoatEMastFrame = 0x02
	BoatEHeadBck   = 0x03
	BoatEHeadFrame = 0x04
	BoatER         = 0x05
	BoatEG         = 0x06
	BoatEB         = 0x07
	BoatEPos       = 0x08
	BoatEAngle     = 0x14
	BoatESailAngle = 0x1A
	BoatETiller    = 0x1C
	BoatEPartA     = 0x1E
	BoatEPartB     = 0x20
	BoatEPad       = 0x22

	BoatEfShow     = 0x01
	BoatEfMastOn   = 0x02
	BoatEfMastHide = 0x04
	BoatEfSailOn   = 0x08
	BoatEfParked   = 0x10

	BoatErrNone  = 0
	BoatErrHeap  = 1
	BoatErrModel = 2
	BoatErrSail  = 3
)

// BoatAddr is the block's absolute address (0x804138E4).
const BoatAddr = Mailbox + BoatOff

// BoatRegion is the peer boats block. Go writes it every tick and the REL
// reads it every frame, so no canary may cover it (CanaryArenaHigh starts
// at its end).
var BoatRegion = addr.Region{Name: "peer boats block", Start: BoatAddr, End: BoatAddr + BoatSize}

// BoatEntry is one boat to draw: everything the REL poses it from.
type BoatEntry struct {
	MastOn, MastHide, SailOn, Parked bool
	Part                             uint8 // daShip_c::mPart: 2 cannon, 3 crane, else none
	MastFrame                        uint8
	HeadBck, HeadFrame               uint8
	R, G, B                          uint8 // the sail colour, 0,0,0 = vanilla
	Pos                              [3]float32
	Angle                            [3]int16 // shape_angle x, y, z
	SailAngle, Tiller                int16
	PartA, PartB                     int16
}

// BoatBlock is what Go writes into the block.
type BoatBlock struct {
	Flags   uint32
	Entries []BoatEntry // at most BoatMax; the rest are dropped
}

// Bytes encodes e as an entry (BoatESize bytes, big-endian), show set.
func (e BoatEntry) Bytes() []byte {
	out := make([]byte, BoatESize)
	be := binary.BigEndian
	f := byte(BoatEfShow)
	for _, b := range []struct {
		on  bool
		bit byte
	}{{e.MastOn, BoatEfMastOn}, {e.MastHide, BoatEfMastHide}, {e.SailOn, BoatEfSailOn}, {e.Parked, BoatEfParked}} {
		if b.on {
			f |= b.bit
		}
	}
	out[BoatEFlags], out[BoatEPart], out[BoatEMastFrame] = f, e.Part, e.MastFrame
	out[BoatEHeadBck], out[BoatEHeadFrame] = e.HeadBck, e.HeadFrame
	out[BoatER], out[BoatEG], out[BoatEB] = e.R, e.G, e.B
	for i, v := range e.Pos {
		be.PutUint32(out[BoatEPos+4*i:], math.Float32bits(v))
	}
	for i, v := range e.Angle {
		be.PutUint16(out[BoatEAngle+2*i:], uint16(v))
	}
	be.PutUint16(out[BoatESailAngle:], uint16(e.SailAngle))
	be.PutUint16(out[BoatETiller:], uint16(e.Tiller))
	be.PutUint16(out[BoatEPartA:], uint16(e.PartA))
	be.PutUint16(out[BoatEPartB:], uint16(e.PartB))
	return out
}

// Header encodes the magic and flags words (BoatMagicOff..BoatStatDrawnOff).
func (b BoatBlock) Header() []byte {
	out := make([]byte, BoatStatDrawnOff)
	binary.BigEndian.PutUint32(out[BoatMagicOff:], BoatMagic)
	binary.BigEndian.PutUint32(out[BoatFlagsOff:], b.Flags)
	return out
}

// EntryBytes encodes the entry span (BoatEntriesOff..BoatSize): the
// entries in order, unused ones zero.
func (b BoatBlock) EntryBytes() []byte {
	out := make([]byte, int(BoatSize)-BoatEntriesOff)
	for i, e := range b.Entries {
		if i >= BoatMax {
			break
		}
		copy(out[i*BoatESize:], e.Bytes())
	}
	return out
}
