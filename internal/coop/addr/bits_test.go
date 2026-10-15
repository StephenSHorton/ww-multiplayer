package addr

import (
	"encoding/binary"
	"testing"
)

// gameSet reproduces the game's setter literally: load the size-byte
// big-endian word words[n/bits] of a struct image, OR in 1 << (n%bits),
// store it back. Indexing runs past the declared array the way C does, so
// mItem[1] lands in mVisitedRoom[0] exactly as on the console.
func gameSet(buf []byte, off, size, n int) {
	bits := size * 8
	at := off + (n/bits)*size
	switch size {
	case 2:
		w := binary.BigEndian.Uint16(buf[at:])
		binary.BigEndian.PutUint16(buf[at:], w|1<<(n%bits))
	case 4:
		w := binary.BigEndian.Uint32(buf[at:])
		binary.BigEndian.PutUint32(buf[at:], w|1<<(n%bits))
	}
}

// onlyBit returns the single set bit of buf, failing on zero or several.
func onlyBit(t *testing.T, buf []byte) Bit {
	t.Helper()
	var got []Bit
	for i, b := range buf {
		for m := 0; m < 8; m++ {
			if b&(1<<m) != 0 {
				got = append(got, Bit{Off: i, Mask: 1 << m})
			}
		}
	}
	if len(got) != 1 {
		t.Fatalf("game setter set %d bits, want 1", len(got))
	}
	return got[0]
}

// TestSwitchBitMatchesOnSwitch: for every valid switch number, the byte and
// mask SwitchBit returns are the one bit dSv_info_c::onSwitch would set in
// the store it routes to (d_save.cpp:1540-1560, 1084-1087, 1238-1241,
// 1279-1282).
func TestSwitchBitMatchesOnSwitch(t *testing.T) {
	for no := 0; no < SwitchEnd; no++ {
		store, bit, err := SwitchBit(no)
		if err != nil {
			t.Fatalf("SwitchBit(0x%X): %v", no, err)
		}
		var buf []byte
		switch {
		case no < SwitchDanFirst:
			buf = make([]byte, MemBitSize)
			gameSet(buf, MemBitOffSwitch, 4, no)
		case no < SwitchZoneFirst:
			buf = make([]byte, DanSize)
			gameSet(buf, DanOffSwitch, 4, no-SwitchDanFirst)
		default:
			buf = make([]byte, ZoneSize)
			gameSet(buf, ZoneOffSwitch, 2, no-SwitchZoneFirst)
		}
		if want := onlyBit(t, buf); bit != want {
			t.Errorf("SwitchBit(0x%X) = %+v (%s), onSwitch sets %+v", no, bit, store, want)
		}
	}

	// Spot checks worked by hand: switch 0 is the low bit of the last byte
	// of mSwitch[0]; 0x1F the high bit of its first byte; 0x7F the high
	// bit of mSwitch[3]'s first byte.
	for _, c := range []struct {
		no   int
		want Bit
	}{
		{0x00, Bit{MemBitOffSwitch + 3, 0x01}},
		{0x1F, Bit{MemBitOffSwitch + 0, 0x80}},
		{0x20, Bit{MemBitOffSwitch + 7, 0x01}},
		{0x7F, Bit{MemBitOffSwitch + 12, 0x80}},
		{0x80, Bit{DanOffSwitch + 3, 0x01}},
		{0xBF, Bit{DanOffSwitch + 4, 0x80}},
		{0xC0, Bit{ZoneOffSwitch + 1, 0x01}},
		{0xDF, Bit{ZoneOffSwitch + 2, 0x80}},
		{0xE0, Bit{ZoneOffSwitch + 5, 0x01}}, // mSwitch[2], cleared by clearRoomSwitch
		{0xEF, Bit{ZoneOffSwitch + 4, 0x80}},
	} {
		if _, got, _ := SwitchBit(c.no); got != c.want {
			t.Errorf("SwitchBit(0x%X) = %+v, want %+v", c.no, got, c.want)
		}
	}
}

func TestClassifySwitch(t *testing.T) {
	for no, want := range map[int]SwitchStore{
		-1: SwitchNone, 0xFF: SwitchNone,
		0x00: SwitchMemory, 0x7F: SwitchMemory,
		0x80: SwitchDan, 0xBF: SwitchDan,
		0xC0: SwitchZone, 0xDF: SwitchZone,
		0xE0: SwitchRoom, 0xEF: SwitchRoom,
		0xF0: SwitchInvalid, 0xFE: SwitchInvalid, 0x100: SwitchInvalid, -2: SwitchInvalid,
	} {
		if got := ClassifySwitch(no); got != want {
			t.Errorf("ClassifySwitch(0x%X) = %s, want %s", no, got, want)
		}
	}
	// The halting range and the sentinels have no bit.
	for _, no := range []int{-1, 0xF0, 0xFE, 0xFF, 0x100} {
		if _, _, err := SwitchBit(no); err == nil {
			t.Errorf("SwitchBit(0x%X) returned a bit", no)
		}
	}
}

// TestMemBitBitsMatchSetters covers tbox, item, visited room, dungeon item,
// zone actor and ocean bits the same way.
func TestMemBitBitsMatchSetters(t *testing.T) {
	cases := []struct {
		name  string
		count int
		size  int // struct image size
		word  int // word size
		off   int // array start
		fn    func(int) (Bit, error)
	}{
		{"tbox", MemBitTboxCount, MemBitSize, 4, MemBitOffTbox, TboxBit},
		{"item", MemBitItemCount, MemBitSize, 4, MemBitOffItem, MemBitItemBit},
		{"visited", MemBitRoomCount, MemBitSize, 4, MemBitOffVisited, VisitedRoomBit},
		{"zone actor", ZoneActorCount, ZoneSize, 4, ZoneOffActor, ZoneActorBit},
	}
	for _, c := range cases {
		for n := 0; n < c.count; n++ {
			got, err := c.fn(n)
			if err != nil {
				t.Fatalf("%s %d: %v", c.name, n, err)
			}
			buf := make([]byte, c.size)
			gameSet(buf, c.off, c.word, n)
			if want := onlyBit(t, buf); got != want {
				t.Errorf("%s %d = %+v, setter sets %+v", c.name, n, got, want)
			}
		}
		if _, err := c.fn(c.count); err == nil {
			t.Errorf("%s %d accepted", c.name, c.count)
		}
		if _, err := c.fn(-1); err == nil {
			t.Errorf("%s -1 accepted", c.name)
		}
	}

	// The item / visited-room alias the game really has.
	item40, _ := MemBitItemBit(40)
	room8, _ := VisitedRoomBit(8)
	if item40 != room8 {
		t.Errorf("item 40 = %+v, visited room 8 = %+v: onItem's mItem[1] is mVisitedRoom[0]", item40, room8)
	}

	for i := 0; i < DungeonItemCount; i++ {
		b, err := DungeonItemBit(i)
		if err != nil || b.Off != MemBitOffDungeonItem || b.Mask != 1<<i || b.Mask&DungeonItemMask == 0 {
			t.Errorf("DungeonItemBit(%d) = %+v, %v", i, b, err)
		}
	}
	if _, err := DungeonItemBit(DungeonItemCount); err == nil {
		t.Error("DungeonItemBit(6) accepted")
	}

	for grid := 0; grid < OceanGridCount; grid++ {
		for bit := 0; bit < 16; bit++ {
			got, err := OceanBit(grid, bit)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, OceanSize)
			gameSet(buf, 2*grid, 2, bit)
			if want := onlyBit(t, buf); got != want {
				t.Errorf("ocean %d/%d = %+v, want %+v", grid, bit, got, want)
			}
		}
	}
	if _, err := OceanBit(OceanGridCount, 0); err == nil {
		t.Error("OceanBit(50, 0) accepted")
	}
}

func TestEventByte(t *testing.T) {
	for _, c := range []struct {
		id   uint16
		want Bit
	}{
		{0x2A08, Bit{0x2A, 0x08}}, // RODE_KORL (§5.3)
		{0x0F80, Bit{0x0F, 0x80}}, // MET_KORL
		{0x8803, Bit{0x88, 0x03}}, // GHOST_SHIP register (d_save_event_flag.inc:513)
		{0x4180, Bit{0x41, 0x80}}, // the last named bit (d_save_event_flag.inc:493)
	} {
		if got := EventByte(c.id); got != c.want {
			t.Errorf("EventByte(0x%04X) = %+v, want %+v", c.id, got, c.want)
		}
	}
}

func TestStagSaveTable(t *testing.T) {
	// mProp = table << 1 | key HUD bit (d_stage.h:1023-1029).
	for prop, want := range map[byte]int{0x00: 0, 0x01: 0, 0x07: TableDRC, 0x16: TableMisc, 0xFF: 0x7F} {
		if got := StagSaveTable(prop); got != want {
			t.Errorf("StagSaveTable(0x%02X) = %d, want %d", prop, got, want)
		}
	}
	if 0x07&StagPropKeyDisp == 0 || 0x16&StagPropKeyDisp != 0 {
		t.Error("StagPropKeyDisp is not bit 0")
	}
}
