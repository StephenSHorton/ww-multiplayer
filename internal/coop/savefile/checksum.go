package savefile

// The game's checksums (m_Do_MemCardRWmng.cpp:300-358) and the card
// SDK's (CARDCheck.c:11-29), bit for bit. The game is big-endian, so a
// 16-bit word is two bytes high first.

// GameDataSum is mDoMemCdRWm_CalcCheckSumGameData (m_Do_MemCardRWmng.cpp:
// 336-345) over a slot's packed image: the sum of its bytes in the high
// word, and in the low word the sum of their complements. `c1 += ~v` with
// v a u8 complements it after promotion to int, so each byte adds
// 0xFFFFFFFF - v to the u32 c1.
func GameDataSum(p []byte) uint64 {
	var c0, c1 uint32
	for _, v := range p {
		c0 += uint32(v)
		c1 += ^uint32(v)
	}
	return uint64(c0)<<32 | uint64(c1)
}

// SaveDataSum is mDoMemCdRWm_CalcCheckSum (m_Do_MemCardRWmng.cpp:301-311)
// over card_savedata less its checksum, as Store computes it
// (m_Do_MemCardRWmng.cpp:56): u16 sums of the big-endian words and of
// their complements, kept in u16 (c1 is a u16, so ~*p wraps to
// 0xFFFF - word), the first in the high half.
func SaveDataSum(p []byte) uint32 {
	var c0, c1 uint16
	for i := 0; i+1 < len(p); i += 2 {
		w := be16(p[i:])
		c0 += w
		c1 += ^w
	}
	return uint32(c0)<<16 | uint32(c1)
}

// PictDataSum is mDoMemCdRWm_CalcCheckSumPictData (m_Do_MemCardRWmng.cpp:
// 314-320): a u16 sum of the bytes. A photo block keeps it in its last
// two bytes (card_pictdata.csum, m_Do_MemCardRWmng.h:24).
func PictDataSum(p []byte) uint16 {
	var c uint16
	for _, v := range p {
		c += uint16(v)
	}
	return c
}

// cardCheckSum is __CARDCheckSum (CARDCheck.c:11-29), the card SDK's sum
// over a system block: the u16 sums of the words and of their
// complements, each 0xFFFF stored as 0.
func cardCheckSum(p []byte) (sum, inv uint16) {
	for i := 0; i+1 < len(p); i += 2 {
		w := be16(p[i:])
		sum += w
		inv += ^w
	}
	if sum == 0xFFFF {
		sum = 0
	}
	if inv == 0xFFFF {
		inv = 0
	}
	return sum, inv
}
