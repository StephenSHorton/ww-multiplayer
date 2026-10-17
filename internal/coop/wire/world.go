package wire

import (
	"encoding/binary"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Image is the canonical 0x778-byte dSv_save_c image (§3), save.Image.
// The name is kept for one release (docs/api/00-design.md, C6; E4
// removes it).
type Image = save.Image

// keep[off] is the bits of image byte off that travel in a WORLD: the class
// mask of a byte that merges, every bit of a DERIVED byte (receivers
// recompute those, but the hub's preflight reads them), nothing of a
// PER_PLAYER, NEVER, TAG or unassigned byte.
var keep = func() (k [addr.SaveImageSize]byte) {
	for off := range k {
		switch r := class.Of(off); {
		case r.Class.Merges():
			k[off] = r.Mask
		case r.Class == class.Derived:
			k[off] = 0xFF
		}
	}
	return k
}()

// StripLocal zeroes every bit of img that never leaves the player: the
// PER_PLAYER, NEVER and TAG bytes, and the bits of OR and MAX bytes
// outside their class mask. An image from a snapshot is ready for a WORLD
// after it.
func StripLocal(img *Image) {
	for off := range img {
		img[off] &= keep[off]
	}
}

// checkImage reports the first bit of img that StripLocal would clear.
func checkImage(img *Image) error {
	for off, b := range img {
		if extra := b &^ keep[off]; extra != 0 {
			r := class.Of(off)
			return bad("WORLD image byte 0x%03X (%s, %s) has local bits 0x%02X", off, r.Field, r.Class, extra)
		}
	}
	return nil
}

// World (0x03, both ways) carries a whole image:
//
//	[worldID:8][seq:u64][len:u16 = 0x778][image]   = 0x78A B
//
// A replica sends one at join, reconnect and resync; the hub merges it per
// class, answers with the merged image and relays the effective delta as
// GAINS. Only shared bits travel (StripLocal).
type World struct {
	WorldID ID
	Seq     uint64
	Image   Image
}

const worldHeaderLen = 18

func (*World) Sub() Sub { return SubWorld }

func (m *World) validate() error { return checkImage(&m.Image) }

func (m *World) appendBody(b []byte) []byte {
	b = append(b, m.WorldID[:]...)
	b = binary.BigEndian.AppendUint64(b, m.Seq)
	b = binary.BigEndian.AppendUint16(b, addr.SaveImageSize)
	return append(b, m.Image[:]...)
}

func decodeWorld(body []byte) (*World, error) {
	if len(body) < worldHeaderLen {
		return nil, bad("WORLD body is %d bytes, shorter than its %d-byte header", len(body), worldHeaderLen)
	}
	if n := binary.BigEndian.Uint16(body[16:18]); n != addr.SaveImageSize {
		return nil, bad("WORLD image length 0x%X, want 0x%X", n, addr.SaveImageSize)
	}
	if len(body) != worldHeaderLen+addr.SaveImageSize {
		return nil, bad("WORLD body is %d bytes, want %d", len(body), worldHeaderLen+addr.SaveImageSize)
	}
	m := &World{WorldID: getID(body[0:8]), Seq: binary.BigEndian.Uint64(body[8:16])}
	copy(m.Image[:], body[worldHeaderLen:])
	return validated(m)
}

// Gain is one image byte that gained bits: for an OR or OR_MASKED byte the
// bits gained, for a MAX byte its new value (within the class mask).
type Gain struct {
	Off uint16
	Val byte
}

// gainLen is one Gain on the wire: {off:u16, val:u8}.
const gainLen = 3

// MaxGains is the most gains one message can hold: offsets are distinct
// and inside the image. The frame limit, (0xFFFF - 12) / 3 = 21841 (§4.4),
// is far above it.
const MaxGains = addr.SaveImageSize

// checkGains reports why gs can't travel: an offset that is not shared
// (checkShared), or offsets not strictly ascending.
func checkGains(what string, gs []Gain) error {
	if len(gs) > MaxGains {
		return bad("%s has %d entries (max %d)", what, len(gs), MaxGains)
	}
	for i, g := range gs {
		if err := checkShared(what, g.Off, g.Val); err != nil {
			return err
		}
		if i > 0 && g.Off <= gs[i-1].Off {
			return bad("%s offsets 0x%03X then 0x%03X are not strictly ascending", what, gs[i-1].Off, g.Off)
		}
	}
	return nil
}

func appendGains(b []byte, gs []Gain) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(gs)))
	for _, g := range gs {
		b = binary.BigEndian.AppendUint16(b, g.Off)
		b = append(b, g.Val)
	}
	return b
}

// readGains decodes [n:u16] n x {off:u16, val:u8}, which must fill b.
func readGains(what string, b []byte) ([]Gain, error) {
	if len(b) < 2 {
		return nil, bad("%s is cut off before its count", what)
	}
	n := int(binary.BigEndian.Uint16(b))
	if len(b)-2 != n*gainLen {
		return nil, bad("%s count %d needs %d bytes, %d follow", what, n, n*gainLen, len(b)-2)
	}
	if n > MaxGains {
		return nil, bad("%s has %d entries (max %d)", what, n, MaxGains)
	}
	var gs []Gain
	if n > 0 {
		gs = make([]Gain, n)
	}
	for i := range gs {
		e := b[2+i*gainLen:]
		gs[i] = Gain{Off: binary.BigEndian.Uint16(e), Val: e[2]}
	}
	return gs, checkGains(what, gs)
}

// CanonicalGains sorts gs by offset and merges entries for the same offset
// by the class rule (OR the bits, or keep the larger MAX value), so a
// batch built in any order can be encoded. It does not validate.
func CanonicalGains(gs []Gain) []Gain {
	out := slices.Clone(gs)
	slices.SortStableFunc(out, func(a, b Gain) int { return int(a.Off) - int(b.Off) })
	n := 0
	for _, g := range out {
		if n > 0 && out[n-1].Off == g.Off {
			if class.Of(int(g.Off)).Class == class.Max {
				out[n-1].Val = max(out[n-1].Val, g.Val)
			} else {
				out[n-1].Val |= g.Val
			}
			continue
		}
		out[n] = g
		n++
	}
	return out[:n]
}

// GainsUp (0x04, c->s) proposes bytes a replica gained:
//
//	[batch:u32][n:u16] n x {off:u16, val:u8}   = 6 + 3n B
//
// Batch numbers a replica's proposals, so the hub can drop a duplicate.
type GainsUp struct {
	Batch uint32
	Gains []Gain
}

const gainsUpHeaderLen = 4

func (*GainsUp) Sub() Sub { return SubGains }

func (m *GainsUp) validate() error { return checkGains("GAINS c->s", m.Gains) }

func (m *GainsUp) appendBody(b []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, m.Batch)
	return appendGains(b, m.Gains)
}

func decodeGainsUp(body []byte) (*GainsUp, error) {
	if len(body) < gainsUpHeaderLen {
		return nil, bad("GAINS c->s body is %d bytes, shorter than its header", len(body))
	}
	gs, err := readGains("GAINS c->s", body[gainsUpHeaderLen:])
	if err != nil {
		return nil, err
	}
	return &GainsUp{Batch: binary.BigEndian.Uint32(body), Gains: gs}, nil
}

// GainsDown (0x04, s->c) relays bytes that changed the world:
//
//	[seq:u64][origin:u8][n:u16] n x {off:u16, val:u8}   = 11 + 3n B
//
// Origin is the co-op connection ID (network.CoopConn.ID) whose proposal
// made the change, or 0 for the hub itself.
type GainsDown struct {
	Seq    uint64
	Origin uint8
	Gains  []Gain
}

const gainsDownHeaderLen = 9

func (*GainsDown) Sub() Sub { return SubGains }

func (m *GainsDown) validate() error { return checkGains("GAINS s->c", m.Gains) }

func (m *GainsDown) appendBody(b []byte) []byte {
	b = binary.BigEndian.AppendUint64(b, m.Seq)
	b = append(b, m.Origin)
	return appendGains(b, m.Gains)
}

func decodeGainsDown(body []byte) (*GainsDown, error) {
	if len(body) < gainsDownHeaderLen {
		return nil, bad("GAINS s->c body is %d bytes, shorter than its header", len(body))
	}
	gs, err := readGains("GAINS s->c", body[gainsDownHeaderLen:])
	if err != nil {
		return nil, err
	}
	return &GainsDown{Seq: binary.BigEndian.Uint64(body), Origin: body[8], Gains: gs}, nil
}
