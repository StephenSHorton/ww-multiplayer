package mailbox

import (
	"encoding/binary"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The remote players' visuals block in the mailbox slack (inject/include/
// wwvis.h, whose WWVIS_* defines these mirror; vis_test.go pins each one
// to the header): name tags, sword and shield on the mini-Links and the
// sea chart markers. 0xF0 bytes at Mailbox+VisOff (0xC0 before WWMB_LAYOUT
// 3 gave it a third mini-Link slot), right after the
// actor_refresh request. The REL (inject/rel/visuals.c) publishes the two
// function words T2 calls and owns the models and counters; Go writes the
// span VisGoStart..VisGoEnd (multiplayer's vissync), magic included.
const (
	VisOff  uint32 = 0x8D0 // block start, from Mailbox (0x804137D0)
	VisSize uint32 = 0xF0  // ends +0x9C0, where CanaryArenaHigh starts

	VisDrawFnOff     = 0x00 // u32 REL: the draw hook
	VisSeachartFnOff = 0x04 // u32 REL: the sea chart draw
	VisMagicOff      = 0x08 // u32 Go: VisMagic, 0 = off
	VisFlagsOff      = 0x0C // u32 Go: VisFlag*
	VisNamesOff      = 0x10 // char[VisSlots][VisNameBytes]
	VisEquipOff      = 0x58 // u8[VisSlots][4]
	VisSeaOff        = 0x64 // entry[VisSeaMax] x VisSeaSize
	VisModelsOff     = 0xA4 // u32[VisSlots][VisModelKinds] (REL)
	VisStatTagsOff   = 0xE0 // u32 REL counters
	VisStatEquipOff  = 0xE4
	VisStatSeaOff    = 0xE8
	VisStatErrOff    = 0xEC

	VisGoStart = VisMagicOff // what Go writes: magic .. the sea entries
	VisGoEnd   = VisModelsOff

	VisMagic     uint32 = 0x57575653 // 'WWVS'
	VisSlots            = 3
	VisNameBytes        = 24
	VisFlagNames uint32 = 0x01
	VisFlagEquip uint32 = 0x02
	VisFlagSea   uint32 = 0x04

	VisEqSword  = 0x00
	VisEqShield = 0x01
	VisEqFlags  = 0x02

	VisEqfValid      = 0x01
	VisEqfSwordHand  = 0x02
	VisEqfShieldHand = 0x04

	VisSeaMax     = 4
	VisSeaSize    = 0x10
	VisSeX        = 0x00
	VisSeZ        = 0x04
	VisSeAngle    = 0x08
	VisSeR        = 0x0A
	VisSeG        = 0x0B
	VisSeB        = 0x0C
	VisSeFlags    = 0x0D
	VisSefShow    = 0x01
	VisModelKinds = 5

	VisMkGripA   = 0
	VisMkGripMS  = 1
	VisMkBladeA  = 2
	VisMkBladeMS = 3
	VisMkShieldA = 4

	VisErrNone   = 0
	VisErrRes    = 1
	VisErrCreate = 2
	VisErrFont   = 3

	VisPhasePre  = 0
	VisPhasePost = 1
)

// VisAddr is the block's absolute address (0x804137D0).
const VisAddr = Mailbox + VisOff

// VisRegion is the visuals block. Go writes it every tick it changes and
// T2 branches through its first two words, so no canary may cover it
// (CanaryArenaHigh starts at its end).
var VisRegion = addr.Region{Name: "visuals block", Start: VisAddr, End: VisAddr + VisSize}

// VisEquip is one mini-Link slot's equip entry.
type VisEquip struct {
	Valid      bool
	Sword      uint8 // mSelectEquip[0], 0xFF none
	Shield     uint8 // mSelectEquip[1], 0xFF none
	SwordHand  bool
	ShieldHand bool
}

// VisSea is one sea chart marker.
type VisSea struct {
	X, Z    float32
	Angle   int16
	R, G, B uint8
}

// VisBlock is what Go writes into the visuals block.
type VisBlock struct {
	Flags uint32
	Names [VisSlots]string
	Equip [VisSlots]VisEquip
	Sea   []VisSea // at most VisSeaMax; the rest are dropped
}

// Bytes encodes b as the Go span (VisGoStart..VisGoEnd), magic first.
// A name is cut to VisNameBytes-1 printable ASCII bytes; anything else in
// it becomes '?'.
func (b VisBlock) Bytes() []byte {
	out := make([]byte, VisGoEnd-VisGoStart)
	at := func(off int) []byte { return out[off-VisGoStart:] }
	binary.BigEndian.PutUint32(at(VisMagicOff), VisMagic)
	binary.BigEndian.PutUint32(at(VisFlagsOff), b.Flags)
	for s := 0; s < VisSlots; s++ {
		dst := at(VisNamesOff + s*VisNameBytes)[:VisNameBytes-1]
		n := 0
		for i := 0; i < len(b.Names[s]) && n < len(dst); i++ {
			c := b.Names[s][i]
			if c < 0x20 || c > 0x7E {
				if c >= 0x80 && c < 0xC0 {
					continue // a UTF-8 continuation byte: one '?' per rune
				}
				c = '?'
			}
			dst[n] = c
			n++
		}
		e := b.Equip[s]
		eq := at(VisEquipOff + s*4)
		if e.Valid {
			eq[VisEqSword] = e.Sword
			eq[VisEqShield] = e.Shield
			f := byte(VisEqfValid)
			if e.SwordHand {
				f |= VisEqfSwordHand
			}
			if e.ShieldHand {
				f |= VisEqfShieldHand
			}
			eq[VisEqFlags] = f
		} else {
			eq[VisEqSword], eq[VisEqShield] = 0xFF, 0xFF
		}
	}
	for i, s := range b.Sea {
		if i >= VisSeaMax {
			break
		}
		e := at(VisSeaOff + i*VisSeaSize)
		binary.BigEndian.PutUint32(e[VisSeX:], math.Float32bits(s.X))
		binary.BigEndian.PutUint32(e[VisSeZ:], math.Float32bits(s.Z))
		binary.BigEndian.PutUint16(e[VisSeAngle:], uint16(s.Angle))
		e[VisSeR], e[VisSeG], e[VisSeB] = s.R, s.G, s.B
		e[VisSeFlags] = VisSefShow
	}
	return out
}
