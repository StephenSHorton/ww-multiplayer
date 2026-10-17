package addr

import "fmt"

// The game keeps most flags as arrays of big-endian words and sets bit n
// with words[n / bits] |= 1 << (n % bits). The helpers below turn that
// into the one byte and mask Go reads or ORs, so a co-op write touches the
// same bit the game's own setter would. Every helper refuses the arguments
// the game's setter would JUT_ASSERT on: JUT_ASSERT is live in retail and
// halts the game through OSPanic (§3.1).

// Bit is one flag (or one register's field) as a byte offset and a mask
// within that byte. Off is relative to the struct the helper names.
type Bit struct {
	Off  int
	Mask byte
}

// beBit locates bit n of a big-endian array of size-byte words that starts
// at off.
func beBit(off, size, n int) Bit {
	bits := size * 8
	word, b := n/bits, n%bits
	return Bit{Off: off + word*size + size - 1 - b/8, Mask: 1 << (b % 8)}
}

// SwitchStore is where dSv_info_c::onSwitch routes a switch number
// (d_save.cpp:1540-1560; the ranges are MEMORY_SWITCH, DAN_SWITCH and
// ZONE_SWITCH, d_save.h:982-984).
type SwitchStore int

const (
	SwitchNone    SwitchStore = iota // -1 or 255: "no switch"; the setters return early (d_save.cpp:1544-1546)
	SwitchMemory                     // 0x00-0x7F: memBit mSwitch, saved per table
	SwitchDan                        // 0x80-0xBF: mDan, reset when the table changes (dSv_danBit_c::init, d_save.cpp:1225-1235)
	SwitchZone                       // 0xC0-0xDF: the room's zone, mSwitch[0..1]
	SwitchRoom                       // 0xE0-0xEF: zone mSwitch[2], zeroed on every room change (clearRoomSwitch, d_save.cpp:1273-1276)
	SwitchInvalid                    // anything else, 0xF0-0xFE included: the setter's JUT_ASSERT halts the game (d_save.cpp:1541-1542)
)

// Switch number boundaries.
const (
	SwitchDanFirst  = 0x80 // MEMORY_SWITCH
	SwitchZoneFirst = 0xC0 // MEMORY_SWITCH + DAN_SWITCH
	SwitchRoomFirst = 0xE0 // zone switch 0x20, the first bit of mSwitch[2]
	SwitchEnd       = 0xF0 // MEMORY_SWITCH + DAN_SWITCH + ZONE_SWITCH
	SwitchNoneByte  = 0xFF // the u8 spelling of -1
)

func (s SwitchStore) String() string {
	switch s {
	case SwitchNone:
		return "none"
	case SwitchMemory:
		return "memory"
	case SwitchDan:
		return "dan"
	case SwitchZone:
		return "zone"
	case SwitchRoom:
		return "room"
	default:
		return "invalid"
	}
}

// ClassifySwitch returns the store switch no routes to.
func ClassifySwitch(no int) SwitchStore {
	switch {
	case no == -1 || no == SwitchNoneByte:
		return SwitchNone
	case no < 0 || no >= SwitchEnd:
		return SwitchInvalid
	case no < SwitchDanFirst:
		return SwitchMemory
	case no < SwitchZoneFirst:
		return SwitchDan
	case no < SwitchRoomFirst:
		return SwitchZone
	default:
		return SwitchRoom
	}
}

// SwitchBit returns the store switch no lives in and its bit, with Off
// relative to that store's struct: dSv_memBit_c for SwitchMemory
// (dSv_memBit_c::onSwitch, d_save.cpp:1084-1087), dSv_danBit_c for
// SwitchDan (d_save.cpp:1238-1241), dSv_zone_c for SwitchZone and
// SwitchRoom (the zoneBit's u16 words, d_save.cpp:1279-1282). It fails for
// SwitchNone and SwitchInvalid.
func SwitchBit(no int) (SwitchStore, Bit, error) {
	s := ClassifySwitch(no)
	switch s {
	case SwitchMemory:
		return s, beBit(MemBitOffSwitch, 4, no), nil
	case SwitchDan:
		return s, beBit(DanOffSwitch, 4, no-SwitchDanFirst), nil
	case SwitchZone, SwitchRoom:
		return s, beBit(ZoneOffSwitch, 2, no-SwitchZoneFirst), nil
	}
	return s, Bit{}, fmt.Errorf("addr: switch 0x%X is %s", no, s)
}

// TboxBit returns chest no's bit in dSv_memBit_c (onTbox, d_save.cpp:1072-1075).
func TboxBit(no int) (Bit, error) {
	if no < 0 || no >= MemBitTboxCount {
		return Bit{}, fmt.Errorf("addr: tbox %d out of range 0..%d", no, MemBitTboxCount-1)
	}
	return beBit(MemBitOffTbox, 4, no), nil
}

// MemBitItemBit returns item no's bit in dSv_memBit_c (onItem,
// d_save.cpp:1112-1115). The game accepts 0..63 but declares one mItem
// word (d_save.h:686), so items 32-63 land in mVisitedRoom[0]; this mirrors
// the game rather than the declaration.
func MemBitItemBit(no int) (Bit, error) {
	if no < 0 || no >= MemBitItemCount {
		return Bit{}, fmt.Errorf("addr: item %d out of range 0..%d", no, MemBitItemCount-1)
	}
	return beBit(MemBitOffItem, 4, no), nil
}

// VisitedRoomBit returns room's visited bit in dSv_memBit_c
// (onVisitedRoom, d_save.cpp:1124-1127).
func VisitedRoomBit(room int) (Bit, error) {
	if room < 0 || room >= MemBitRoomCount {
		return Bit{}, fmt.Errorf("addr: visited room %d out of range 0..%d", room, MemBitRoomCount-1)
	}
	return beBit(MemBitOffVisited, 4, room), nil
}

// DungeonItemBit returns DungeonItem* bit i in dSv_memBit_c
// (onDungeonItem, d_save.cpp:1136-1139).
func DungeonItemBit(i int) (Bit, error) {
	if i < 0 || i >= DungeonItemCount {
		return Bit{}, fmt.Errorf("addr: dungeon item %d out of range 0..%d", i, DungeonItemCount-1)
	}
	return Bit{Off: MemBitOffDungeonItem, Mask: 1 << i}, nil
}

// ZoneActorBit returns actor setID id's death bit in dSv_zone_c
// (dSv_zoneActor_c::on, d_save.cpp:1326-1329).
func ZoneActorBit(id int) (Bit, error) {
	if id < 0 || id >= ZoneActorCount {
		return Bit{}, fmt.Errorf("addr: actor id %d out of range 0..%d", id, ZoneActorCount-1)
	}
	return beBit(ZoneOffActor, 4, id), nil
}

// OceanBit returns bit of sea grid's salvage word, relative to dSv_ocean_c
// (onOceanSvBit, d_save.cpp:1164-1168).
func OceanBit(grid, bit int) (Bit, error) {
	if grid < 0 || grid >= OceanGridCount || bit < 0 || bit >= 16 {
		return Bit{}, fmt.Errorf("addr: ocean grid %d bit %d out of range", grid, bit)
	}
	return beBit(2*grid, 2, bit), nil
}

// EventByte returns event bit (or register) id's byte and mask, relative
// to dSv_event_c: id 0xHHLL is mFlags[HH] & LL (d_save.cpp:1187-1210).
func EventByte(id uint16) Bit {
	return Bit{Off: int(id >> 8), Mask: byte(id)}
}

// ValidTable reports whether t is a save table index getSave accepts.
func ValidTable(t int) bool { return t >= 0 && t < TableCount }

// OffMemBit is the image offset of table t's saved memBit. t must satisfy
// ValidTable.
func OffMemBit(t int) int { return OffMemory + t*MemBitSize }

// SavedMemBitAddr is the absolute address of table t's saved memBit.
func SavedMemBitAddr(t int) (uint32, error) {
	if !ValidTable(t) {
		return 0, fmt.Errorf("addr: save table %d out of range 0..%d", t, TableCount-1)
	}
	return SavedMemBit + uint32(t*MemBitSize), nil
}

// ZoneAddr is the absolute address of mZone[z].
func ZoneAddr(z int) (uint32, error) {
	if z < 0 || z >= ZoneCount {
		return 0, fmt.Errorf("addr: zone %d out of range 0..%d", z, ZoneCount-1)
	}
	return Zones + uint32(z*ZoneSize), nil
}

// RoomStatusAddr is the absolute address of mStatus[room].
func RoomStatusAddr(room int) (uint32, error) {
	if room < 0 || room >= RoomCount {
		return 0, fmt.Errorf("addr: room %d out of range 0..%d", room, RoomCount-1)
	}
	return RoomStatus + uint32(room*RoomStatusStride), nil
}

// ZoneNoAddr is the absolute address of mStatus[room].mZoneNo, the zone
// the room's zone and actor switches live in (getZoneNo, d_stage.h:901).
// A zone write is valid only if that byte is 0..31 and the zone's mRoomNo
// is room (§3.1).
func ZoneNoAddr(room int) (uint32, error) {
	a, err := RoomStatusAddr(room)
	if err != nil {
		return 0, err
	}
	return a + RoomStatusOffZoneNo, nil
}
