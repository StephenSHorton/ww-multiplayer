package save

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestLayoutCoversImage checks that Layout names every image byte exactly
// once, in image order, with no empty span or nameless field.
func TestLayoutCoversImage(t *testing.T) {
	next := 0
	for _, s := range Layout() {
		if s.Off != next {
			t.Fatalf("%s: span starts at 0x%03X, want 0x%03X", s.Field, s.Off, next)
		}
		if s.Len <= 0 || s.Field == "" {
			t.Fatalf("span 0x%03X+%d %q: empty span or field", s.Off, s.Len, s.Field)
		}
		next = s.Off + s.Len
	}
	if next != ImageSize {
		t.Fatalf("Layout ends at 0x%03X, want 0x%X", next, ImageSize)
	}
}

// TestLayoutSpots checks representative spans, located through addr.
func TestLayoutSpots(t *testing.T) {
	at := map[int]Span{}
	for _, s := range Layout() {
		at[s.Off] = s
	}
	for _, want := range []Span{
		{addr.OffMaxLife, 2, "mMaxLife"},
		{addr.OffItems + addr.SlotBottle0, addr.SlotBottleCount, "mItems bottles"},
		{addr.OffGetItem, 3, "mGetItem[0..2]"},
		{addr.OffMemBit(3) + addr.MemBitOffSwitch, addr.MemBitOffItem - addr.MemBitOffSwitch, "mMemory[3].mSwitch"},
		{addr.OffEvent + 0x42, 1, "mEvent[0x42]"},
		{addr.OffReserve, addr.ReserveSize, "mReserve"},
	} {
		if got := at[want.Off]; got != want {
			t.Errorf("span at 0x%03X = %+v, want %+v", want.Off, got, want)
		}
	}
	l := Layout()
	l[0].Field = "changed"
	if Layout()[0].Field != "mMaxLife" {
		t.Error("Layout returns the package's own slice")
	}
}

// TestSlots checks the slot table against addr: every item slot but the
// bottles, the equipment bytes from mCollect, each variant's bit unique.
func TestSlots(t *testing.T) {
	items := ItemSlots()
	if len(items) != addr.SlotCount-addr.SlotBottleCount {
		t.Errorf("%d item slots, want %d", len(items), addr.SlotCount-addr.SlotBottleCount)
	}
	for _, s := range items {
		if s.Index >= addr.SlotBottle0 && s.Index < addr.SlotBottle0+addr.SlotBottleCount {
			t.Errorf("%s: slot %d is a bottle", s.Name, s.Index)
		}
		if s.Off != addr.OffItems+s.Index || s.FlagOff != addr.OffGetItem+s.Index {
			t.Errorf("%s: Off 0x%03X, FlagOff 0x%03X", s.Name, s.Off, s.FlagOff)
		}
	}
	for i, s := range EquipSlots() {
		if s.Index != i || s.Off != addr.OffSelectEquip+i || s.FlagOff != addr.OffCollectFlags+i {
			t.Errorf("%s: Index %d, Off 0x%03X, FlagOff 0x%03X", s.Name, s.Index, s.Off, s.FlagOff)
		}
	}
	for _, s := range append(items, EquipSlots()...) {
		seen := map[uint8]bool{}
		for _, v := range s.Variants {
			if seen[v.Bit] || v.Item == ItemNone {
				t.Errorf("%s: variant %+v repeats a bit or is ItemNone", s.Name, v)
			}
			seen[v.Bit] = true
		}
	}
}
