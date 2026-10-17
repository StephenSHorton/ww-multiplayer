package wwapi

import (
	"iter"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// Watcher turns successive snapshots into core events (design §5.3). It
// is the diff of coop-watch (coop_diag.go: coopWatcher.step, diffSave,
// edgeNote, saveLabel) with typed events in place of text lines: the same
// "where" comparison, the same byte-by-byte save diff and the same
// table-reload rule. coop-watch itself is unchanged.
//
// A Game owns one and runs it once a tick; tests drive Step directly
// with fakegame snapshots. The zero Watcher is ready to use. It is not
// safe for concurrent use.
type Watcher struct {
	prev    *state.Snapshot
	beating bool // the HEARTBEAT gate was open on the last tick
}

// Step diffs snapshot s (nil for a tick whose read failed) and the gate
// status of the same tick against the last snapshot, and returns the
// events, in this order: HeartbeatLost, StageChanged, then the save
// edges in offset order. Consumers must not rely on that order (§5.3).
func (w *Watcher) Step(s *state.Snapshot, st gate.Status) []events.Event {
	var out []events.Event
	if w.beating && !st.Heartbeat {
		out = append(out, events.HeartbeatLost{Why: st.HeartbeatWhy})
	}
	w.beating = st.Heartbeat
	if s == nil {
		// Keep the last good snapshot, so what changed while the read
		// failed shows as edges on the next good one.
		return out
	}
	prev := w.prev
	w.prev = s
	if prev == nil {
		// Always, even before the first stage load (To.Stage ""), so a
		// subscriber waiting for First is never left waiting.
		return append(out, events.StageChanged{To: placeOf(s), First: true})
	}
	from, to := placeOf(prev), placeOf(s)
	if from.Stage != to.Stage || from.Table != to.Table || from.File != to.File {
		out = append(out, events.StageChanged{From: from, To: to})
	}
	if menu(prev) || menu(s) {
		// The title screen and file select: card_to_memory, New Game and
		// the file menu rewrite the whole save, which is a load, not the
		// game giving anything.
		return out
	}
	return append(out, saveEvents(prev, s)...)
}

// Last is the last good snapshot Step saw, or nil.
func (w *Watcher) Last() *state.Snapshot { return w.prev }

func placeOf(s *state.Snapshot) events.Place {
	return events.Place{Stage: s.StageName, Room: s.StayNo, Table: s.Table, File: s.DataNum}
}

// menu reports whether s is on the title screen or file select, or has
// no stage at all.
func menu(s *state.Snapshot) bool {
	return s.StageName == "" || s.StageName == addr.StageTitle || s.StageName == addr.StageFileSelect
}

// saveEvents is diffSave and edgeNote reduced to the edges the core
// events name: inventory slots taking an item, switch bits rising and
// story event bits rising. On a table change getSave and initDan reload
// the live memBit and mDan in bulk (d_stage.cpp:1675-1677), which is not
// the game setting anything, so those edges are skipped, as coop-watch
// does.
func saveEvents(prev, cur *state.Snapshot) []events.Event {
	var out []events.Event
	reload := prev.Table != cur.Table
	a, b := prev.Save[:], cur.Save[:]
	for off := range min(len(a), len(b)) {
		if a[off] == b[off] {
			continue
		}
		if reload && off >= addr.InfoOffMemory && off < addr.InfoOffZone {
			continue
		}
		out = append(out, byteEvents(off, a[off], b[off], cur)...)
	}
	return out
}

// byteEvents names the events one changed byte of dSv_info_c gives: the
// offset classification of saveLabel, limited to the stores the core
// events cover.
func byteEvents(off int, old, cur byte, s *state.Snapshot) []events.Event {
	switch {
	case off >= addr.OffItems && off < addr.OffItems+addr.ItemsSize:
		if cur == save.ItemNone {
			return nil
		}
		if slot := off - addr.OffItems; slot >= addr.SlotBottle0 && slot < addr.SlotBottle0+addr.SlotBottleCount && old != save.ItemNone {
			// A bottle's contents changing (drinking, filling, emptying)
			// is not a gain; only a new bottle is.
			return nil
		}
		return []events.Event{events.ItemGained{Slot: off - addr.OffItems, Item: cur, Prev: old}}
	case off >= addr.OffMemory && off < addr.OffOcean:
		rel := off - addr.OffMemory
		t := int8(rel / addr.MemBitSize)
		return memBitSwitches(rel%addr.MemBitSize, old, cur, events.SwitchSaved, t)
	case off >= addr.OffEvent && off < addr.OffReserve:
		hh := off - addr.OffEvent
		if hh >= addr.EventRegFirst {
			return nil // a register byte, a value rather than flags
		}
		var out []events.Event
		for bit := range rises(old, cur) {
			out = append(out, events.EventBitSet{ID: uint16(hh)<<8 | uint16(1)<<bit})
		}
		return out
	case off >= addr.InfoOffMemory && off < addr.InfoOffDan:
		return memBitSwitches(off-addr.InfoOffMemory, old, cur, events.SwitchMemory, s.Table)
	case off >= addr.InfoOffDan+addr.DanOffSwitch && off < addr.InfoOffZone:
		var out []events.Event
		for bit := range rises(old, cur) {
			no := addr.SwitchDanFirst + beIndex(off-addr.InfoOffDan-addr.DanOffSwitch, 4, bit)
			out = append(out, events.SwitchSet{Kind: events.SwitchDan, Switch: no, Table: -1, Zone: -1, Room: -1})
		}
		return out
	case off >= addr.InfoOffZone && off < addr.InfoOffRestart:
		rel := off - addr.InfoOffZone
		z, zr := rel/addr.ZoneSize, rel%addr.ZoneSize
		if zr < addr.ZoneOffSwitch || zr >= addr.ZoneOffItem {
			return nil
		}
		room := int8(s.Save[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffRoomNo])
		var out []events.Event
		for bit := range rises(old, cur) {
			no := addr.SwitchZoneFirst + beIndex(zr-addr.ZoneOffSwitch, 2, bit)
			out = append(out, events.SwitchSet{Kind: events.SwitchZone, Switch: no, Table: -1, Zone: z, Room: room})
		}
		return out
	}
	return nil
}

// memBitSwitches is the switch part of memBitLabel: the memory switches
// 0x00-0x7F in the u32[4] at MemBitOffSwitch of a dSv_memBit_c.
func memBitSwitches(rel int, old, cur byte, kind events.SwitchKind, table int8) []events.Event {
	if rel < addr.MemBitOffSwitch || rel >= addr.MemBitOffItem {
		return nil
	}
	var out []events.Event
	for bit := range rises(old, cur) {
		no := beIndex(rel-addr.MemBitOffSwitch, 4, bit)
		out = append(out, events.SwitchSet{Kind: kind, Switch: no, Table: table, Zone: -1, Room: -1})
	}
	return out
}

// rises yields the bits, low first, set in cur and clear in old.
func rises(old, cur byte) iter.Seq[int] {
	return func(yield func(int) bool) {
		up := cur &^ old
		for bit := range 8 {
			if up&(1<<bit) != 0 && !yield(bit) {
				return
			}
		}
	}
}

// beIndex inverts the game's bit math (addr.Bit): bit of byte rel of an
// array of big-endian size-byte words is flag number word*size*8 plus the
// bit's place in its word. It is coop_diag.go's beIndex.
func beIndex(rel, size, bit int) int {
	word, b := rel/size, rel%size
	return word*size*8 + (size-1-b)*8 + bit
}
