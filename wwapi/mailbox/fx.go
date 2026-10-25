package mailbox

import (
	"encoding/binary"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The projectile events block in the mailbox slack (inject/include/
// wwfx.h, whose WWFX_* defines these mirror; fx_test.go pins each one to
// the header): other players' bombs, boat-cannon shots and arrows made
// real in this world (inject/rel/fx.c). 0x2B0 bytes at Mailbox+FxOff, the
// top of the free slack up to the arena. The REL writes the outbox (the
// local Link's projectiles) and its counters; Go (multiplayer's fxsync)
// writes the two magic words, the outbox read word and the inbox.
const (
	FxOff  uint32 = 0xE50 // block start, from Mailbox (0x80413D50)
	FxSize uint32 = 0x2B0 // ends +0x1100, the arena

	FxSendMagicOff    = 0x00 // u32 Go (broadcast-pose): FxMagic = report, 0 = off
	FxRecvMagicOff    = 0x04 // u32 Go (puppet-sync): FxMagic = spawn, 0 = off
	FxOutWriteOff     = 0x08 // u32 REL
	FxOutReadOff      = 0x0C // u32 Go
	FxInWriteOff      = 0x10 // u32 Go
	FxInReadOff       = 0x14 // u32 REL
	FxStatSpawnedOff  = 0x18 // u32 REL counters
	FxStatRejectedOff = 0x1C
	FxStatDroppedOff  = 0x20
	FxStatLocalOff    = 0x24
	FxStatSwallowOff  = 0x28
	FxStatClearedOff  = 0x2C
	FxOutRingOff      = 0x30
	FxInRingOff       = 0x170

	FxMagic  uint32 = 0x57574658 // 'WWFX'
	FxRing          = 8
	FxEvSize        = 0x28

	FxEvKind    = 0x00
	FxEvSlot    = 0x01
	FxEvVariant = 0x02
	FxEvRoom    = 0x03
	FxEvID      = 0x04
	FxEvPos     = 0x08
	FxEvSpeedF  = 0x14
	FxEvSpeedY  = 0x18
	FxEvGravity = 0x1C
	FxEvAngle   = 0x20
	FxEvTimer   = 0x26

	FxKindBombThrow  = 1
	FxKindBombPickup = 2
	FxKindExplode    = 3
	FxKindRemove     = 4
	FxKindCannon     = 5
	FxKindArrow      = 6
	FxKindMax        = 6

	FxArrowMax = 3

	FxBombPrmCopy uint32 = 0x01000000
)

// FxAddr is the block's absolute address (0x80413D50).
const FxAddr = Mailbox + FxOff

// FxRegion is the projectile events block. Go and the REL write it every
// tick and frame they run, so no canary may cover it (CanaryArenaHigh ends
// at its start).
var FxRegion = addr.Region{Name: "projectile events block", Start: FxAddr, End: FxAddr + FxSize}

// FxEvent is one projectile event, as a ring entry holds it.
type FxEvent struct {
	Kind    uint8
	Slot    uint8 // inbox: the sender's player id; outbox: 0
	Variant uint8 // an arrow's type, else 0
	Room    int8  // the projectile's own room
	ID      uint32
	Pos     [3]float32
	SpeedF  float32
	SpeedY  float32
	Gravity float32
	Angle   [3]int16
	Timer   int16
}

// Bytes encodes e as a ring entry (FxEvSize bytes, big-endian).
func (e FxEvent) Bytes() []byte {
	out := make([]byte, FxEvSize)
	be := binary.BigEndian
	out[FxEvKind], out[FxEvSlot], out[FxEvVariant], out[FxEvRoom] = e.Kind, e.Slot, e.Variant, byte(e.Room)
	be.PutUint32(out[FxEvID:], e.ID)
	for i, v := range e.Pos {
		be.PutUint32(out[FxEvPos+4*i:], math.Float32bits(v))
	}
	be.PutUint32(out[FxEvSpeedF:], math.Float32bits(e.SpeedF))
	be.PutUint32(out[FxEvSpeedY:], math.Float32bits(e.SpeedY))
	be.PutUint32(out[FxEvGravity:], math.Float32bits(e.Gravity))
	for i, v := range e.Angle {
		be.PutUint16(out[FxEvAngle+2*i:], uint16(v))
	}
	be.PutUint16(out[FxEvTimer:], uint16(e.Timer))
	return out
}

// ParseFxEvent decodes a ring entry; ok is false for a short one.
func ParseFxEvent(b []byte) (e FxEvent, ok bool) {
	if len(b) < FxEvSize {
		return e, false
	}
	be := binary.BigEndian
	e.Kind, e.Slot, e.Variant, e.Room = b[FxEvKind], b[FxEvSlot], b[FxEvVariant], int8(b[FxEvRoom])
	e.ID = be.Uint32(b[FxEvID:])
	for i := range e.Pos {
		e.Pos[i] = math.Float32frombits(be.Uint32(b[FxEvPos+4*i:]))
	}
	e.SpeedF = math.Float32frombits(be.Uint32(b[FxEvSpeedF:]))
	e.SpeedY = math.Float32frombits(be.Uint32(b[FxEvSpeedY:]))
	e.Gravity = math.Float32frombits(be.Uint32(b[FxEvGravity:]))
	for i := range e.Angle {
		e.Angle[i] = int16(be.Uint16(b[FxEvAngle+2*i:]))
	}
	e.Timer = int16(be.Uint16(b[FxEvTimer:]))
	return e, true
}

// FxOutEntry / FxInEntry are the absolute addresses of the ring entry that
// holds event n (a 0-based count, the write and read words' unit).
func FxOutEntry(n uint32) uint32 { return FxAddr + FxOutRingOff + (n%FxRing)*FxEvSize }
func FxInEntry(n uint32) uint32  { return FxAddr + FxInRingOff + (n%FxRing)*FxEvSize }
