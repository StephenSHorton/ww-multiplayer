package replica

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// Category is the row of the §3 class table a shared image byte belongs
// to. The planner picks a byte's apply rule by it, and the preflight
// counts gains by it.
type Category uint8

const (
	CatNone      Category = iota // not shared: DERIVED, PER_PLAYER, NEVER or TAG
	CatStage                     // stage progress: chests, pickups, visited rooms, dungeon items (dSv_memBit_c +0x00, +0x14, +0x18, +0x21)
	CatSwitch                    // memory switches 0x00-0x7F (dSv_memBit_c +0x04)
	CatOcean                     // salvage bits, charts and the sea map (mOcean, dSv_player_map_c)
	CatStory                     // story event bits, mEvent bytes 0x00-0x41
	CatRegister                  // the allowlisted event registers, mEvent bytes 0x79-0xFF
	CatInventory                 // mGetItem, mCollect, songs, Triforce, pearls, beast and bait flags
	CatCapacity                  // wallet size and arrow and bomb capacities (MAX)
	numCategories
)

func (c Category) String() string {
	switch c {
	case CatNone:
		return "none"
	case CatStage:
		return "stage progress"
	case CatSwitch:
		return "memory switches"
	case CatOcean:
		return "ocean and charts"
	case CatStory:
		return "story events"
	case CatRegister:
		return "event registers"
	case CatInventory:
		return "inventory"
	case CatCapacity:
		return "capacities"
	}
	return fmt.Sprintf("Category(%d)", uint8(c))
}

// Categories lists every category but CatNone, in report order.
func Categories() []Category {
	out := make([]Category, 0, numCategories-1)
	for c := CatStage; c < numCategories; c++ {
		out = append(out, c)
	}
	return out
}

// CategoryOf returns the category of image byte off: CatNone unless the
// byte is shared (world.SharedMask is non-zero).
func CategoryOf(off int) Category {
	if world.SharedMask(off) == 0 {
		return CatNone
	}
	switch {
	case off >= addr.OffMemory && off < addr.OffOcean:
		rel := (off - addr.OffMemory) % addr.MemBitSize
		if rel >= addr.MemBitOffSwitch && rel < addr.MemBitOffItem {
			return CatSwitch
		}
		return CatStage
	case off >= addr.OffOcean && off < addr.OffEvent,
		off >= addr.OffMap && off < addr.OffMap+addr.MapSize:
		return CatOcean
	case off >= addr.OffEvent && off < addr.OffEvent+addr.EventBitBytes:
		return CatStory
	case off >= addr.OffEvent+addr.EventRegFirst && off < addr.OffReserve:
		return CatRegister
	case class.Of(off).Class == class.Max:
		return CatCapacity
	}
	return CatInventory
}

// memBitTable returns the save table whose saved memBit holds image byte
// off and the byte's offset in it; ok is false outside mMemory[16].
func memBitTable(off int) (t, rel int, ok bool) {
	if off < addr.OffMemory || off >= addr.OffOcean {
		return 0, 0, false
	}
	return (off - addr.OffMemory) / addr.MemBitSize, (off - addr.OffMemory) % addr.MemBitSize, true
}

// tableNames are the save tables (dSv_save_c::SaveStageTbl,
// d_save.h:888-904), named as coop-watch names them.
var tableNames = [addr.TableCount]string{
	"sea", "sea2", "FF", "DRC", "FW", "TotG", "ET", "WT",
	"GT", "Hyrule", "ship", "misc", "subdungeon", "subdungeon-new", "blue-chu", "test",
}

// TableName names save table t, or "none".
func TableName(t int) string {
	if addr.ValidTable(t) {
		return tableNames[t]
	}
	return "none"
}

// beIndex inverts the game's bit math (addr.Bit): bit of byte rel of an
// array of big-endian size-byte words is flag number word*size*8 plus
// the bit's place in its word.
func beIndex(rel, size, bit int) int {
	word, b := rel/size, rel%size
	return word*size*8 + (size-1-b)*8 + bit
}

// dungeonItems names the mDungeonItem bits (d_save.h:639-646).
var dungeonItems = [addr.DungeonItemCount]string{
	"map", "compass", "boss key", "boss defeated", "heart container taken", "boss demo seen",
}

// DefaultLabel names bit (0-7) of image byte off, or the byte itself for
// bit -1, from the class table and the setters' bit math: chests,
// switches, pickups and visited rooms per table (d_save.cpp:1072-1139),
// sea grids (d_save.cpp:1164-1168), event ids 0xHHLL (d_save.cpp:
// 1187-1210), charts (d_save.cpp:851-933). coop-watch's labeller in
// package main says more; Config.Label can plug it in.
func DefaultLabel(off, bit int) string {
	if off < 0 || off >= addr.SaveImageSize {
		return fmt.Sprintf("image+0x%X", off)
	}
	if t, rel, ok := memBitTable(off); ok {
		p := fmt.Sprintf("t%02d %s", t, TableName(t))
		if bit < 0 {
			return fmt.Sprintf("%s memBit+0x%02X", p, rel)
		}
		switch {
		case rel < addr.MemBitOffSwitch:
			return fmt.Sprintf("%s chest %d", p, beIndex(rel-addr.MemBitOffTbox, 4, bit))
		case rel < addr.MemBitOffItem:
			return fmt.Sprintf("%s switch 0x%02X", p, beIndex(rel-addr.MemBitOffSwitch, 4, bit))
		case rel < addr.MemBitOffVisited:
			return fmt.Sprintf("%s item %d", p, beIndex(rel-addr.MemBitOffItem, 4, bit))
		case rel < addr.MemBitOffKeyNum:
			// onItem takes 0..63 into a one-word mItem, so items 32-63
			// share the first word's bits (addr.MemBitItemBit).
			n := beIndex(rel-addr.MemBitOffVisited, 4, bit)
			if n < 32 {
				return fmt.Sprintf("%s visited room %d (or item %d)", p, n, 32+n)
			}
			return fmt.Sprintf("%s visited room %d", p, n)
		case rel == addr.MemBitOffDungeonItem && bit < len(dungeonItems):
			return fmt.Sprintf("%s %s", p, dungeonItems[bit])
		}
		return fmt.Sprintf("%s memBit+0x%02X bit %d", p, rel, bit)
	}
	switch {
	case off >= addr.OffOcean && off < addr.OffEvent:
		rel := off - addr.OffOcean
		if bit < 0 {
			return fmt.Sprintf("ocean grid %d", rel/2)
		}
		return fmt.Sprintf("ocean grid %d bit %d", rel/2, beIndex(rel%2, 2, bit))
	case off >= addr.OffEvent && off < addr.OffReserve:
		hh := off - addr.OffEvent
		if hh >= addr.EventRegFirst || bit < 0 {
			return fmt.Sprintf("event byte 0x%02X", hh)
		}
		return fmt.Sprintf("event 0x%02X%02X", hh, 1<<bit)
	case off >= addr.OffMapGet && off < addr.OffFmapBits && bit >= 0:
		row := [...]string{"owned", "deciphered", "salvaged"}[(off-addr.OffMapGet)/16]
		return fmt.Sprintf("chart %d %s", beIndex((off-addr.OffMapGet)%16, 4, bit), row)
	case off >= addr.OffFmapBits && off < addr.OffMap71 && bit >= 0:
		return fmt.Sprintf("sea map %d bit %d", off-addr.OffFmapBits, bit)
	case off == addr.OffMapTriforce && bit >= 0:
		return fmt.Sprintf("Triforce chart %d", bit)
	}
	f := class.Of(off).Field
	if f == "" {
		f = fmt.Sprintf("image+0x%03X", off)
	}
	if bit < 0 {
		return f
	}
	return fmt.Sprintf("%s bit %d", f, bit)
}

// scopedLabel names bit b of word w of a SCOPED kind: switch number, room
// switch or setID, by the setters' bit math (wire.ScopeKind).
func scopedLabel(kind wire.ScopeKind, w, b int) string {
	switch kind {
	case wire.ScopeDan:
		return fmt.Sprintf("dan switch 0x%02X", addr.SwitchDanFirst+w*32+b)
	case wire.ScopeZone, wire.ScopeRoomEdge:
		return fmt.Sprintf("zone switch 0x%02X", addr.SwitchZoneFirst+w*16+b)
	case wire.ScopeKill:
		return fmt.Sprintf("actor setID %d", w*32+b)
	}
	return fmt.Sprintf("%s word %d bit %d", kind, w, b)
}
