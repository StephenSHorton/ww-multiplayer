package diag

import (
	"fmt"
	"math/bits"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// bitNo is the bit number of a one-bit mask.
func bitNo(mask byte) int { return bits.TrailingZeros8(mask) }

// TestSaveLabelsMatchTheGamesBitMath labels every flag the addr helpers
// can place (the game's own setter math) and checks the label names the
// same flag back: coop-watch's labels invert addr.Bit exactly.
func TestSaveLabelsMatchTheGamesBitMath(t *testing.T) {
	type want struct {
		off   int
		bit   int
		label string
	}
	var cases []want
	add := func(off int, b addr.Bit, label string) {
		cases = append(cases, want{off, bitNo(b.Mask), label})
	}
	for _, tbl := range []int{addr.TableSea, addr.TableDRC, addr.TableTest} {
		saved := addr.OffMemBit(tbl)
		for no := 0; no < addr.MemBitSwitchCount; no++ {
			_, b, err := addr.SwitchBit(no)
			if err != nil {
				t.Fatal(err)
			}
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d switch 0x%02X", tbl, no))
			add(addr.InfoOffMemory+b.Off, b, fmt.Sprintf("live t%02d switch 0x%02X", addr.TableDRC, no))
		}
		for no := 0; no < addr.MemBitTboxCount; no++ {
			b, _ := addr.TboxBit(no)
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d chest %d", tbl, no))
		}
		for no := 0; no < 32; no++ {
			b, _ := addr.MemBitItemBit(no)
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d item %d", tbl, no))
		}
		for room := 0; room < addr.MemBitRoomCount; room++ {
			b, _ := addr.VisitedRoomBit(room)
			l := fmt.Sprintf("saved t%02d visited room %d", tbl, room)
			if room < 32 {
				// Items 32-63 land in the same bits (addr.MemBitItemBit).
				ib, _ := addr.MemBitItemBit(room + 32)
				if ib != b {
					t.Fatalf("item %d is not visited room %d's bit", room+32, room)
				}
				l += fmt.Sprintf(" or item %d", room+32)
			}
			add(saved+b.Off, b, l)
		}
		b, _ := addr.DungeonItemBit(addr.DungeonItemBossKey)
		add(saved+b.Off, b, fmt.Sprintf("saved t%02d boss key", tbl))
	}
	for no := addr.SwitchDanFirst; no < addr.SwitchZoneFirst; no++ {
		_, b, _ := addr.SwitchBit(no)
		add(addr.InfoOffDan+b.Off, b, fmt.Sprintf("dan switch 0x%02X", no))
	}
	for _, z := range []int{0, 7, addr.ZoneCount - 1} {
		base := addr.InfoOffZone + z*addr.ZoneSize
		for no := addr.SwitchZoneFirst; no < addr.SwitchEnd; no++ {
			_, b, _ := addr.SwitchBit(no)
			add(base+b.Off, b, fmt.Sprintf("zone %d switch 0x%02X", z, no))
		}
		for id := 0; id < addr.ZoneActorCount; id++ {
			b, _ := addr.ZoneActorBit(id)
			add(base+b.Off, b, fmt.Sprintf("zone %d actor %d", z, id))
		}
	}
	for grid := 0; grid < addr.OceanGridCount; grid++ {
		for bit := range 16 {
			b, _ := addr.OceanBit(grid, bit)
			add(addr.OffOcean+b.Off, b, fmt.Sprintf("ocean grid %d bit %d", grid, bit))
		}
	}
	for _, id := range []uint16{0x2D01, 0x0F80, 0x3D10, 0x0101} {
		b := addr.EventByte(id)
		add(addr.OffEvent+b.Off, b, fmt.Sprintf("event 0x%04X", id))
	}
	// Byte 0 has no named bit 0x40 (d_save_event_flag.inc), and bytes
	// 0x42-0x78 have none at all.
	add(addr.OffEvent+0x00, addr.Bit{Mask: 0x40}, "event 0x0040 (unnamed)")
	add(addr.OffEvent+0x60, addr.Bit{Mask: 0x01}, "event 0x6001 (unnamed)")
	add(addr.InfoOffTmp+0x12, addr.Bit{Mask: 0x08}, "tmp 0x1208")
	// mGetItem and mCollect[0..4] by their array index, though the class
	// table splits both: an empty bottle held (onBottleItem,
	// d_save.cpp:224-282), the boomerang, the Hero's Charm worn.
	add(addr.OffGetItem, addr.Bit{Mask: 0x04}, "mGetItem[0] bit 2")
	add(addr.OffGetItem+5, addr.Bit{Mask: 0x01}, "mGetItem[5] bit 0")
	add(addr.OffCollectFlags+4, addr.Bit{Mask: 0x02}, "mCollect[4] bit 1")

	for _, c := range cases {
		got, flags := SaveLabel(c.off, c.bit, addr.TableDRC)
		if got != c.label || !flags {
			t.Errorf("SaveLabel(0x%X, %d) = %q, %v; want %q, flags", c.off, c.bit, got, flags, c.label)
		}
	}

	// Values, not flags: their changes print as one byte.
	for _, c := range []struct {
		off   int
		label string
	}{
		{addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum, "saved t03 small keys"},
		{addr.InfoOffMemory + addr.MemBitOffKeyNum, "live t03 small keys"},
		{addr.OffMaxLife, "mMaxLife+0x0"},
		{addr.OffLife + 1, "mLife+0x1"},
		{addr.OffWalletSize, "mWalletSize"},
		{addr.OffEvent + 0x86, "event register byte 0x86"},
		{addr.OffReserve + 4, "mReserve+0x04"},
		{addr.InfoOffDan + addr.DanOffStageNo, "mDan.mStageNo (live table)"},
		{addr.InfoOffZone + 2*addr.ZoneSize, "zone 2 room"},
		{addr.InfoOffDataNum, "mDataNum"},
		{addr.InfoOffRestart + 3, "mRestart+0x03"},
	} {
		got, flags := SaveLabel(c.off, -1, addr.TableDRC)
		if got != c.label || flags {
			t.Errorf("SaveLabel(0x%X, -1) = %q, %v; want %q, a value", c.off, got, flags, c.label)
		}
	}

	// An invalid live table still labels, without a table. Byte 0 bit 0
	// of the big-endian first word is switch 0x18.
	if got, _ := SaveLabel(addr.InfoOffMemory+addr.MemBitOffSwitch, 0, -1); got != "live t?? switch 0x18" {
		t.Errorf("live switch with no table: %q", got)
	}
}
