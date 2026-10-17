// Package diag is what the co-op diagnostics (coop_diag.go in package
// main until D6) share with the co-op session and its commands: the 10 Hz
// snapshot tick, the gates' snapshots, the --flag splitter, the Dolphin
// the environment selects, and the save edges and their labels
// (coop-watch, save-diff).
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994.
package diag

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// TickInterval is the co-op snapshot rate, 10 Hz (§4.2).
const TickInterval = 100 * time.Millisecond

// Tick waits one co-op snapshot interval.
func Tick() { time.Sleep(TickInterval) }

// WithMem opens the Dolphin WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select
// with open (package main's openDolphin, pid 0), as peek and dump do, and
// runs f over it.
func WithMem(open func(pid uint32) (*emu.Dolphin, error), f func(m mem.Mem) error) error {
	d, err := open(0)
	if err != nil {
		return err
	}
	defer d.Close()
	return f(d)
}

// SplitFlags separates "--name" flags from positional arguments and
// refuses a flag not in allowed.
func SplitFlags(args []string, allowed ...string) (pos []string, flags map[string]bool, err error) {
	flags = make(map[string]bool)
	for _, a := range args {
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			pos = append(pos, a)
			continue
		}
		if !slices.Contains(allowed, name) {
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		}
		flags[name] = true
	}
	return pos, flags, nil
}

// tableNames are the save tables (dSv_save_c::SaveStageTbl, d_save.h:888-904).
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

// GateSnapshots takes gate.StableTicks+1 snapshots, calling tick between
// them, so the gates can judge STABLE's unchanged-ticks rule and whether
// the heartbeat moves. It returns the last snapshot, its gate status and
// the first snapshot's heartbeat.
func GateSnapshots(m mem.Mem, tick func()) (*state.Snapshot, gate.Status, uint32, error) {
	var tr gate.Tracker
	var s *state.Snapshot
	var st gate.Status
	var hb0 uint32
	for i := 0; i <= gate.StableTicks; i++ {
		if i > 0 {
			tick()
		}
		var err error
		if s, err = state.Read(m, state.Options{}); err != nil {
			return nil, gate.Status{}, 0, err
		}
		if i == 0 {
			hb0 = s.Heartbeat
		}
		st = tr.Observe(s)
	}
	return s, st, hb0, nil
}

// Where is the context a line of edges is read in.
func Where(s *state.Snapshot) string {
	return fmt.Sprintf("stage %q room %d, table %d %s, file %d",
		s.StageName, s.StayNo, s.Table, TableName(int(s.Table)), s.DataNum)
}

// SaveEdge is one change in the save between two snapshots: a flag bit
// rising or falling, or (Bit -1) a byte of a value changing.
type SaveEdge struct {
	Off      int // offset in dSv_info_c
	Bit      int
	Rise     bool
	Old, New byte
	Label    string
}

func (e SaveEdge) String() string {
	switch {
	case e.Bit < 0:
		return fmt.Sprintf("~ %s: 0x%02X -> 0x%02X", e.Label, e.Old, e.New)
	case e.Rise:
		return "+ " + e.Label
	}
	return "- " + e.Label
}

// DiffSave returns the edges from prev to cur, two dSv_info_c images, in
// offset order. liveTable is cur's mDan.mStageNo, the table the live
// memBit holds.
func DiffSave(prev, cur []byte, liveTable int) []SaveEdge {
	var out []SaveEdge
	for off := range min(len(prev), len(cur)) {
		a, b := prev[off], cur[off]
		if a == b {
			continue
		}
		if _, flags := SaveLabel(off, 0, liveTable); !flags {
			label, _ := SaveLabel(off, -1, liveTable)
			out = append(out, SaveEdge{Off: off, Bit: -1, Old: a, New: b, Label: label})
			continue
		}
		for bit := range 8 {
			mask := byte(1) << bit
			if (a^b)&mask == 0 {
				continue
			}
			label, _ := SaveLabel(off, bit, liveTable)
			out = append(out, SaveEdge{Off: off, Bit: bit, Rise: b&mask != 0, Old: a, New: b,
				Label: label + edgeNote(cur, off, bit, liveTable)})
		}
	}
	return out
}

// edgeNote adds context to a flag edge at off: for the live memBit,
// whether the live table's saved slot has the same bit (they differ from
// a game write to the live copy until putSave copies it back, §3.1); for
// a zone, the room it belongs to.
func edgeNote(cur []byte, off, bit, liveTable int) string {
	switch {
	case off >= addr.InfoOffMemory && off < addr.InfoOffDan && addr.ValidTable(liveTable):
		saved := addr.OffMemBit(liveTable) + off - addr.InfoOffMemory
		if cur[saved]&(1<<bit) != 0 {
			return " (saved slot set)"
		}
		return " (saved slot clear)"
	case off >= addr.InfoOffZone && off < addr.InfoOffRestart:
		z := (off - addr.InfoOffZone) / addr.ZoneSize
		return fmt.Sprintf(" (room %d)", int8(cur[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffRoomNo]))
	}
	return ""
}

// dSv_info_c members past mDataNum (d_save.h:998-1001).
const (
	infoOffNewFile        = 0x1291 // u8 mNewFile
	infoOffNoFile         = 0x1292 // u8 mNoFile
	infoOffMemCardCheckID = 0x1298 // u64 mMemCardCheckID
)

// SaveLabel names what bit (0-7) of byte off of dSv_info_c means, or the
// byte itself for bit -1, and reports whether the byte is a set of flags,
// whose edges coop-watch prints bit by bit, rather than part of a value.
// liveTable is mDan.mStageNo, the table the live memBit holds.
func SaveLabel(off, bit, liveTable int) (string, bool) {
	switch {
	case off < 0 || off >= addr.SaveInfoSize:
		return fmt.Sprintf("dSv_info_c+0x%X", off), false
	case off < addr.OffMemory:
		return playerLabel(off, bit)
	case off < addr.OffOcean:
		rel := off - addr.OffMemory
		return memBitLabel(fmt.Sprintf("saved t%02d", rel/addr.MemBitSize), rel%addr.MemBitSize, bit)
	case off < addr.OffEvent:
		// u16 per sea grid (onOceanSvBit, d_save.cpp:1164-1168).
		rel := off - addr.OffOcean
		return fmt.Sprintf("ocean grid %d bit %d", rel/2, beIndex(rel%2, 2, bit)), true
	case off < addr.OffReserve:
		return eventLabel("event", off-addr.OffEvent, bit, class.Of(off).Mask, true)
	case off < addr.OffImagePad:
		return fmt.Sprintf("mReserve+0x%02X", off-addr.OffReserve), false
	case off < addr.SaveImageSize:
		return "dSv_save_c padding", false
	case off < addr.InfoOffDan:
		t := "t??"
		if addr.ValidTable(liveTable) {
			t = fmt.Sprintf("t%02d", liveTable)
		}
		return memBitLabel("live "+t, off-addr.InfoOffMemory, bit)
	case off < addr.InfoOffZone:
		return danLabel(off-addr.InfoOffDan, bit)
	case off < addr.InfoOffRestart:
		rel := off - addr.InfoOffZone
		return zoneLabel(rel/addr.ZoneSize, rel%addr.ZoneSize, bit)
	case off < addr.InfoOffTmp:
		return fmt.Sprintf("mRestart+0x%02X", off-addr.InfoOffRestart), false
	case off < addr.InfoOffTurnRestart:
		// mTmp is a dSv_event_c of temporary bits (§3 "Temporary").
		return eventLabel("tmp", off-addr.InfoOffTmp, bit, 0, false)
	case off < addr.InfoOffDataNum:
		return fmt.Sprintf("mTurnRestart+0x%02X", off-addr.InfoOffTurnRestart), false
	case off == addr.InfoOffDataNum:
		return "mDataNum", false
	case off == infoOffNewFile:
		return "mNewFile", false
	case off == infoOffNoFile:
		return "mNoFile", false
	case off >= infoOffMemCardCheckID:
		return fmt.Sprintf("mMemCardCheckID+%d", off-infoOffMemCardCheckID), false
	}
	return "dSv_info_c padding", false
}

// beIndex inverts the game's bit math (addr.Bit): bit of byte rel of an
// array of big-endian size-byte words is flag number word*size*8 plus the
// bit's place in its word.
func beIndex(rel, size, bit int) int {
	word, b := rel/size, rel%size
	return word*size*8 + (size-1-b)*8 + bit
}

// memBitLabel names bit of byte rel of a dSv_memBit_c (d_save.h:684-689;
// its setters' bit math, d_save.cpp:1072-1139).
func memBitLabel(prefix string, rel, bit int) (string, bool) {
	switch {
	case rel < addr.MemBitOffSwitch:
		return fmt.Sprintf("%s chest %d", prefix, beIndex(rel-addr.MemBitOffTbox, 4, bit)), true
	case rel < addr.MemBitOffItem:
		return fmt.Sprintf("%s switch 0x%02X", prefix, beIndex(rel-addr.MemBitOffSwitch, 4, bit)), true
	case rel < addr.MemBitOffVisited:
		return fmt.Sprintf("%s item %d", prefix, beIndex(rel-addr.MemBitOffItem, 4, bit)), true
	case rel < addr.MemBitOffKeyNum:
		n := beIndex(rel-addr.MemBitOffVisited, 4, bit)
		l := fmt.Sprintf("%s visited room %d", prefix, n)
		if n < 32 {
			// onItem takes 0..63 into a one-word mItem, so items 32-63
			// share these bits (addr.MemBitItemBit).
			l += fmt.Sprintf(" or item %d", n+32)
		}
		return l, true
	case rel == addr.MemBitOffKeyNum:
		return prefix + " small keys", false
	case rel == addr.MemBitOffDungeonItem:
		return prefix + " " + dungeonItemName(bit), true
	}
	return prefix + " memBit padding", false
}

// dungeonItemName names mDungeonItem bit i (d_save.h:639-646).
func dungeonItemName(i int) string {
	names := [addr.DungeonItemCount]string{"map", "compass", "boss key", "boss defeated", "heart container taken", "boss demo seen"}
	if i >= 0 && i < len(names) {
		return names[i]
	}
	return fmt.Sprintf("dungeon item bit %d", i)
}

// danLabel names bit of byte rel of mDan (dSv_danBit_c, d_save.h:752-754;
// switches 0x80-0xBF, d_save.cpp:1238-1241).
func danLabel(rel, bit int) (string, bool) {
	switch {
	case rel == addr.DanOffStageNo:
		return "mDan.mStageNo (live table)", false
	case rel >= addr.DanOffSwitch:
		return fmt.Sprintf("dan switch 0x%02X", addr.SwitchDanFirst+beIndex(rel-addr.DanOffSwitch, 4, bit)), true
	}
	return fmt.Sprintf("mDan+0x%X", rel), false
}

// zoneLabel names bit of byte rel of mZone[z] (dSv_zone_c,
// d_save.h:785-798): switches 0xC0-0xEF as u16 words
// (dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282), one u16 of items
// (d_save.cpp:1307-1310) and 512 dead-actor bits (d_save.cpp:1326-1329).
func zoneLabel(z, rel, bit int) (string, bool) {
	p := fmt.Sprintf("zone %d", z)
	switch {
	case rel == addr.ZoneOffRoomNo:
		return p + " room", false
	case rel < addr.ZoneOffSwitch:
		return p + " padding", false
	case rel < addr.ZoneOffItem:
		return fmt.Sprintf("%s switch 0x%02X", p, addr.SwitchZoneFirst+beIndex(rel-addr.ZoneOffSwitch, 2, bit)), true
	case rel < addr.ZoneOffItem+2:
		return fmt.Sprintf("%s item %d", p, beIndex(rel-addr.ZoneOffItem, 2, bit)), true
	case rel < addr.ZoneOffActor:
		return p + " padding", false
	}
	return fmt.Sprintf("%s actor %d", p, beIndex(rel-addr.ZoneOffActor, 4, bit)), true
}

// eventLabel names bit of byte hh of a dSv_event_c: event bit id 0xHHLL
// in bytes 0x00-0x78, a register byte from 0x79 (onEventBit and
// setEventReg, d_save.cpp:1187-1210). With checkNames, a bit outside
// named, the byte's named-bit mask, is marked unnamed.
func eventLabel(prefix string, hh, bit int, named byte, checkNames bool) (string, bool) {
	if hh >= addr.EventRegFirst {
		return fmt.Sprintf("%s register byte 0x%02X", prefix, hh), false
	}
	if bit < 0 {
		return fmt.Sprintf("%s byte 0x%02X", prefix, hh), true
	}
	l := fmt.Sprintf("%s 0x%02X%02X", prefix, hh, 1<<bit)
	if checkNames && named&(1<<bit) == 0 {
		l += " (unnamed)"
	}
	return l, true
}

// coopSpans is the class table; coopSpanAt[off] indexes the span holding
// image byte off, or is -1.
var coopSpans, coopSpanAt = func() ([]class.Span, [addr.SaveImageSize]int) {
	spans := class.Spans()
	var at [addr.SaveImageSize]int
	for i := range at {
		at[i] = -1
	}
	for i, s := range spans {
		for off := s.Off; off < s.Off+s.Len && off < len(at); off++ {
			at[off] = i
		}
	}
	return spans, at
}()

// playerLabel names bit of byte off of dSv_player_c (image offsets below
// 0x380) by the class table's field names, with the charts and the sea
// map spelled out (dSv_player_map_c, d_save.cpp:851-933).
func playerLabel(off, bit int) (string, bool) {
	switch {
	case off >= addr.OffMapGet && off < addr.OffFmapBits:
		row := [...]string{"owned", "deciphered", "salvaged"}[(off-addr.OffMapGet)/16]
		return fmt.Sprintf("chart %d %s", beIndex((off-addr.OffMapGet)%16, 4, bit), row), true
	case off >= addr.OffFmapBits && off < addr.OffMap71:
		return fmt.Sprintf("sea map %d bit %d", off-addr.OffFmapBits, bit), true
	case off == addr.OffMapTriforce:
		return fmt.Sprintf("Triforce chart %d", bit), true
	case off >= addr.OffGetItem && off < addr.OffGetItem+addr.GetItemSize:
		// One array (d_save.h:131), which the class table splits at
		// [3]: a span-relative index would miscount.
		return fmt.Sprintf("mGetItem[%d] bit %d", off-addr.OffGetItem, bit), true
	case off >= addr.OffCollectFlags && off < addr.OffCollectFlags+5:
		return fmt.Sprintf("mCollect[%d] bit %d", off-addr.OffCollectFlags, bit), true
	}
	i := coopSpanAt[off]
	if i < 0 {
		return fmt.Sprintf("dSv_player_c+0x%X", off), false
	}
	sp := coopSpans[i]
	rel := off - sp.Off
	flags := sp.Class == class.OR || sp.Class == class.ORMasked
	switch {
	case !flags && sp.Len == 1:
		return sp.Field, false
	case !flags:
		return fmt.Sprintf("%s+0x%X", sp.Field, rel), false
	case sp.Len == 1:
		return fmt.Sprintf("%s bit %d", sp.Field, bit), true
	}
	return fmt.Sprintf("%s +%d bit %d", sp.Field, rel, bit), true
}
