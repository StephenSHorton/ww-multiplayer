package vissync

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Vitals trailer (wind#55): the sender's hearts, magic and rupees, for the
// app's party rows. It follows the boat run (boatsync, 40 bytes at 19),
// which a sender always puts in once the equip bytes are there (all zero
// with boats off), so the vitals sit at a fixed offset and the trailer is
// 64 bytes, network.MaxFaceBytes, the most the server relays (it drops a
// longer frame, old hosts too). So the five bytes are packed:
//
//	[59] u8 mLife, quarter hearts (the game caps mMaxLife at 0x50)
//	[60] u8 mMaxLife, quarter hearts
//	[61] mMagic in the low 6 bits (capped at 0x20), mMaxMagic/16 in the top 2
//	[62..64) u16 mRupee
//
// mWalletSize doesn't fit and isn't sent. A receiver shows nothing for a
// shorter trailer (an older sender) or a max life of 0 (a sender that
// couldn't read them).
const (
	// VitalsOffset is where the vitals start: boatsync.RunOffset +
	// boatsync.RunBytes (boatsync's test holds the two together).
	VitalsOffset = player.FaceBytes + 1 + TrailerEquipBytes + boatRunBytes
	// VitalsBytes is their length.
	VitalsBytes = 5

	boatRunBytes = 40
)

// Vitals are one player's hearts, magic and rupees, as the save's player
// status holds them (dSv_player_status_a_c, d_save.h:40-52).
type Vitals struct {
	Life, MaxLife   int // quarter hearts
	Magic, MaxMagic int
	Rupees          int
}

// statusABytes covers mMaxLife (0x00) to mMagic (0x14).
const statusABytes = int(addr.Magic-addr.MaxLife) + 1

// LocalVitals reads this game's vitals. ok is false when they can't be
// read or there is no file loaded (a max life of 0).
func LocalVitals(m mem.Reader) (v Vitals, ok bool) {
	b, err := m.Read(addr.MaxLife, statusABytes)
	if err != nil || len(b) != statusABytes {
		return v, false
	}
	at := func(a uint32) int { return int(a - addr.MaxLife) }
	be := binary.BigEndian
	v = Vitals{
		MaxLife:  int(be.Uint16(b[at(addr.MaxLife):])),
		Life:     int(be.Uint16(b[at(addr.Life):])),
		Rupees:   int(be.Uint16(b[at(addr.Rupee):])),
		MaxMagic: int(b[at(addr.MaxMagic)]),
		Magic:    int(b[at(addr.Magic)]),
	}
	return v, v.MaxLife > 0
}

// EncodeVitals is the vitals' five trailer bytes (ok false: all zero,
// which receivers read as unknown).
func EncodeVitals(v Vitals, ok bool) []byte {
	out := make([]byte, VitalsBytes)
	if !ok || v.MaxLife <= 0 {
		return out
	}
	out[0] = clampU8(v.Life, 0xFF)
	out[1] = clampU8(v.MaxLife, 0xFF)
	out[2] = clampU8(v.Magic, 0x3F) | clampU8(v.MaxMagic/16, 3)<<6
	binary.BigEndian.PutUint16(out[3:], uint16(min(max(v.Rupees, 0), 0xFFFF)))
	return out
}

// DecodeVitals reads the vitals in a pose trailer. ok is false for a
// trailer without them (an older sender) or with a max life of 0.
func DecodeVitals(face []byte) (v Vitals, ok bool) {
	if len(face) < VitalsOffset+VitalsBytes {
		return v, false
	}
	b := face[VitalsOffset : VitalsOffset+VitalsBytes]
	if b[1] == 0 {
		return v, false
	}
	v = Vitals{
		Life:     int(b[0]),
		MaxLife:  int(b[1]),
		Magic:    int(b[2] & 0x3F),
		MaxMagic: int(b[2]>>6) * 16,
		Rupees:   int(binary.BigEndian.Uint16(b[3:])),
	}
	v.Life = min(v.Life, v.MaxLife)
	v.Magic = min(v.Magic, v.MaxMagic)
	return v, true
}

func clampU8(n, hi int) byte { return byte(min(max(n, 0), hi)) }
