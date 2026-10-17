package save

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
)

// View is the receiver's own applied flags and counts, read from one
// tick's snapshot: the saved dSv_save_c image, and the live memBit of the
// loaded table (mDan.mStageNo). A memBit flag of the loaded table counts
// when either copy has it. The game sets them in the live copy
// (dSv_info_c::onSwitch and the others write save.mMemory, d_save.cpp:
// 1548-1549), getSave filled it from the saved slot at the stage load
// (d_save.cpp:1506-1509), and a co-op write puts a bit in both (§3.1). So
// the saved slot has a bit the live copy lacks only where the game
// cleared it since the load (a key door's lock then comes back, and the
// key stays spent, as in the game alone) or a write reached one copy.
// Event bits, the ocean and the charts have one copy.
type View struct {
	s *state.Snapshot
}

// ViewOf returns the view of s.
func ViewOf(s *state.Snapshot) View { return View{s} }

// Table is the save table loaded into the live memBit, or -1.
func (v View) Table() int {
	if t := int(v.s.Table); addr.ValidTable(t) {
		return t
	}
	return -1
}

// memBit returns table t's memBit byte rel: the saved slot's, ORed with
// the live copy's when t is loaded.
func (v View) memBit(t, rel int) byte {
	b := v.s.Save[addr.InfoOffSavedata+addr.OffMemBit(t)+rel]
	if t == v.Table() {
		b |= v.s.Save[addr.InfoOffMemory+rel]
	}
	return b
}

// image returns saved image byte off.
func (v View) image(off int) byte { return v.s.Save[addr.InfoOffSavedata+off] }

// FlagSet reports whether source flag f is set. known is false for a
// flag no saved bit records (itemsrc.Flag.Saved), or one outside the
// arrays the game asserts on: such a source can't be counted (Known).
func (v View) FlagSet(f itemsrc.Flag) (set, known bool) {
	l, ok := locate(f)
	switch {
	case !ok:
		return false, false
	case l.table >= 0:
		return v.memBit(l.table, l.Off)&l.Mask != 0, true
	}
	return v.image(l.Off)&l.Mask != 0, true
}

// Known reports whether FlagSet can read flag f.
func Known(f itemsrc.Flag) bool {
	_, ok := locate(f)
	return ok
}

// flagBit is where a source flag is saved: bit Bit of save table table's
// memBit (Off relative to it), or, with table -1, of image byte Off.
type flagBit struct {
	addr.Bit
	table int
}

func locate(f itemsrc.Flag) (flagBit, bool) {
	var b addr.Bit
	var err error
	switch f.Kind {
	case itemsrc.TboxFlag, itemsrc.ItemFlag, itemsrc.StageLifeFlag:
		if !addr.ValidTable(f.Table) {
			return flagBit{}, false
		}
		switch f.Kind {
		case itemsrc.TboxFlag:
			b, err = addr.TboxBit(f.Bit) // onTbox, d_save.cpp:1072-1075
		case itemsrc.ItemFlag:
			b, err = addr.MemBitItemBit(f.Bit) // onItem, d_save.cpp:1112-1115
		default:
			b, err = addr.DungeonItemBit(addr.DungeonItemStageLife) // onStageLife, d_save.h:644, :677
		}
		return flagBit{b, f.Table}, err == nil
	case itemsrc.OceanFlag:
		b, err = addr.OceanBit(f.Room, f.Bit) // onOceanSvBit, d_save.cpp:1164-1168
		b.Off += addr.OffOcean
		return flagBit{b, -1}, err == nil
	case itemsrc.ChartFlag:
		// end_salvage's dComIfGs_onCompleteCollectMap(no) sets map bit
		// no-1 of the "complete" u32[4] (d_a_salvage.cpp:510;
		// d_com_inf_game.h:1212-1214; onCompleteMap, d_save.cpp:882-885).
		n := f.Bit - 1
		if n < 0 || n >= 128 {
			return flagBit{}, false
		}
		b = addr.Bit{Off: addr.OffMapComplete + n/32*4 + 3 - n%32/8, Mask: 1 << (n % 8)}
		return flagBit{b, -1}, true
	}
	return flagBit{}, false
}

// SwitchSet reports whether memory switch sw of table t is set. known is
// false for a switch that is no memory switch (0x80 and up, the dan,
// zone and room switches, which the memBit doesn't hold).
func (v View) SwitchSet(t, sw int) (set, known bool) {
	s, b, err := addr.SwitchBit(sw)
	if err != nil || s != addr.SwitchMemory || !addr.ValidTable(t) {
		return false, false
	}
	return v.memBit(t, b.Off)&b.Mask != 0, true
}

// EventSet reports whether event bit id is set (isEventBit,
// d_save.cpp:1196-1198).
func (v View) EventSet(id uint16) bool {
	b := addr.EventByte(id)
	return b.Mask != 0 && v.image(addr.OffEvent+b.Off)&b.Mask != 0
}

// RecordSet reports whether a heart Catalogue reward's record is set.
// known is false for RecordNone.
func (v View) RecordSet(r tables.Record) (set, known bool) {
	switch r.Kind {
	case tables.RecordEventBit:
		return v.EventSet(r.ID), true
	case tables.RecordLetterRead:
		b := addr.EventByte(r.ID)
		return v.image(addr.OffEvent+b.Off)&b.Mask == tables.LetterRead, true
	}
	return false, false
}

// KeyNum is the small-key count table t holds: the live mKeyNum for the
// loaded table, which is the only copy the game reads and writes while it
// is loaded (dComIfGs_getKeyNum and setKeyNum go through
// save.getMemory(), d_com_inf_game.h:2106-2111; putSave copies it back,
// d_save.cpp:1512-1515), the saved byte otherwise.
func (v View) KeyNum(t int) int {
	if t == v.Table() {
		return int(v.s.Save[addr.InfoOffMemory+addr.MemBitOffKeyNum])
	}
	return int(v.image(addr.OffMemBit(t) + addr.MemBitOffKeyNum))
}

// MaxLife is mMaxLife, in quarter hearts.
func (v View) MaxLife() int {
	return int(binary.BigEndian.Uint16(v.s.Save[addr.InfoOffSavedata+addr.OffMaxLife:]))
}

// MaxMagic is mMaxMagic.
func (v View) MaxMagic() int { return int(v.image(addr.OffMaxMagic)) }

// GetItem reports whether get-item flag bit of inventory slot slot is set
// (dSv_player_get_item_c::onItem, d_save.cpp:204-207).
func (v View) GetItem(slot, bit int) bool {
	if slot < 0 || slot >= addr.GetItemSize || bit < 0 || bit >= 8 {
		return false
	}
	return v.image(addr.OffGetItem+slot)&(1<<bit) != 0
}
