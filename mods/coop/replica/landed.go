package replica

import (
	"fmt"
	"maps"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The landed set (design/stale-actor-refresh.md rule 1, G1): the bits this
// game's FrameWriter wrote that the next Observe verified held, each with
// when it was verified. The stale-actor refresh (refresh.go) acts only on
// an actor whose bit is in it, never on a bit that merely reads set: every
// local break or kill sets its bit a frame or more before the actor ends
// its own sequence (Stone2 damaged() calls on_switch() and then breaks,
// d_a_stone2.cpp:273; Bk sets m02B8 while it dies, d_a_bk.cpp:2644-2646),
// so a scan of raw memory would race the local player's own play.
//
// A bit is landed per bit, not per byte: a written byte holds 8 switches,
// and only those the write itself set, new &^ old by the fresh read just
// before it, count; a bit the game had set in the same byte does not. The
// sources are the three paths that write bits:
//
//   - the merge path (ApplyContext, writer.go:419): the live memBit's
//     chests, memory switches and pickups of the loaded table (a saved
//     slot is no copy the game reads while the table is loaded);
//   - the scoped path (ApplyScoped, writer_scoped.go:234): the dan
//     switches 0x80-0xBF, and a zone's switches 0xC0-0xEF of a room (Check
//     note 1: DRC r7's ambusher Bokoblins die on zone switches);
//   - the kill path (ApplyKills, writer_kill.go:132): a zone's dead setIDs.
//
// The set is the stage visit's: it is cleared when the stage, the loaded
// table or the file changes, and when a save state is loaded, as the zones
// are wiped at a stage entry and M6's own state is per visit
// (reconcile_m6.go:19-37). An actor created after its bit landed reads it
// at create (it is refused, or comes up done), so only one created before
// can be stale.

// LandKind is the kind of bit a LandedBit is.
type LandKind uint8

const (
	// LandSwitch is a switch by its number (addr.ClassifySwitch): memory,
	// dan, zone or room.
	LandSwitch LandKind = iota + 1
	// LandTbox is a chest of the loaded table (dSv_memBit_c mTbox).
	LandTbox
	// LandItem is a pickup of the loaded table, 0-31 (mItem; items 32-63
	// land in mVisitedRoom[0], addr.MemBitItemBit, and are not taken: a
	// visited-room bit there would read as one).
	LandItem
	// LandKill is a dead setID in a room's zone.
	LandKill
)

func (k LandKind) String() string {
	switch k {
	case LandSwitch:
		return "switch"
	case LandTbox:
		return "tbox"
	case LandItem:
		return "item"
	case LandKill:
		return "dead setID"
	}
	return fmt.Sprintf("LandKind(%d)", uint8(k))
}

// LandedBit is one landed bit. Its store decides its key (Check note 10):
// isSwitch takes the room only for a zone or room switch (d_save.cpp:
// 1586-1604), so a memory or dan switch, a chest and a pickup have Room -1;
// a zone or room switch and a dead setID have the room whose zone holds
// them. Table is the save table loaded as it landed (mDan.mStageNo).
type LandedBit struct {
	Kind  LandKind
	Table int8
	Room  int8
	No    int
}

// SwitchKey is the LandedBit of switch no, read as an actor of room
// reads it, with table loaded: Room is room only for a zone or room
// switch.
func SwitchKey(table int8, room int, no int) LandedBit {
	r := int8(-1)
	if st := addr.ClassifySwitch(no); st == addr.SwitchZone || st == addr.SwitchRoom {
		r = int8(room)
	}
	return LandedBit{Kind: LandSwitch, Table: table, Room: r, No: no}
}

func (b LandedBit) String() string {
	switch {
	case b.Kind == LandSwitch && b.Room >= 0:
		return fmt.Sprintf("%s switch 0x%02X of room %d", addr.ClassifySwitch(b.No), b.No, b.Room)
	case b.Kind == LandSwitch:
		return fmt.Sprintf("%s switch 0x%02X", addr.ClassifySwitch(b.No), b.No)
	case b.Kind == LandKill:
		return fmt.Sprintf("dead setID %d of room %d", b.No, b.Room)
	}
	return fmt.Sprintf("%s %d of %s", b.Kind, b.No, TableName(int(b.Table)))
}

// maxLanded bounds the landed set: more than a stage's memory, dan and
// zone switches, chests and pickups, with a dead-actor word set or two.
const maxLanded = 4096

// landedSet is the FrameWriter's landed set and the visit it is of.
type landedSet struct {
	ok      bool
	stage   string
	table   int8
	file    bind.File
	at      map[LandedBit]time.Time
	dropped int // bits not taken: the set was full
}

// Landed is a copy of the landed set (FrameWriter.Landed).
type Landed struct {
	Stage string
	Table int8
	At    map[LandedBit]time.Time
}

// Has reports whether b landed, and when.
func (l Landed) Has(b LandedBit) (time.Time, bool) {
	t, ok := l.At[b]
	return t, ok
}

// Landed returns a copy of the landed set: the bits this writer wrote and
// verified since the stage was entered. A dry run has none.
func (w *FrameWriter) Landed() Landed {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Landed{Stage: w.landed.stage, Table: w.landed.table, At: maps.Clone(w.landed.at)}
}

// landVisit starts the landed set over when s is another visit than the
// one it holds: the stage, the loaded table or the file changed, or a
// save state was loaded. A nil s (a failed read) keeps it. w.mu is held.
func (w *FrameWriter) landVisit(s *state.Snapshot, st gate.Status, f bind.File) {
	if s == nil {
		return
	}
	l := &w.landed
	if l.ok && !st.Regressed && s.StageName == l.stage && s.Table == l.table && f == l.file {
		return
	}
	*l = landedSet{ok: true, stage: s.StageName, table: s.Table, file: f, at: l.at}
	clear(l.at)
}

// land records b as landed now, if it is the visit's. w.mu is held.
func (w *FrameWriter) land(b LandedBit, now time.Time) {
	l := &w.landed
	if !l.ok || b.Table != l.table {
		return
	}
	if l.at == nil {
		l.at = map[LandedBit]time.Time{}
	}
	if _, ok := l.at[b]; ok {
		return
	}
	if len(l.at) >= maxLanded {
		l.dropped++
		return
	}
	l.at[b] = now
}

// beBits lists the bit numbers set bits sets in byte rel of a big-endian
// array of 4-byte words: addr.beBit inverted (word i's last byte holds
// bits 32i+0..7).
func beBits(rel int, set byte) []int {
	word, at := rel/4, rel%4
	var out []int
	for bit := range 8 {
		if set&(1<<bit) != 0 {
			out = append(out, 32*word+8*(3-at)+bit)
		}
	}
	return out
}

// landMemBit records the bits set, newly written and verified, at a, a byte
// of the live memBit while table is loaded: its chests, memory switches
// and pickups 0-31. w.mu is held.
func (w *FrameWriter) landMemBit(a uint32, table int8, set byte, now time.Time) {
	if set == 0 || a < addr.LiveMemBit || a >= addr.LiveMemBit+addr.MemBitSize {
		return
	}
	rel := int(a - addr.LiveMemBit)
	var kind LandKind
	switch {
	case rel >= addr.MemBitOffTbox && rel < addr.MemBitOffTbox+4:
		kind, rel = LandTbox, rel-addr.MemBitOffTbox
	case rel >= addr.MemBitOffSwitch && rel < addr.MemBitOffSwitch+16:
		kind, rel = LandSwitch, rel-addr.MemBitOffSwitch
	case rel >= addr.MemBitOffItem && rel < addr.MemBitOffItem+4:
		kind, rel = LandItem, rel-addr.MemBitOffItem
	default:
		return
	}
	for _, no := range beBits(rel, set) {
		w.land(LandedBit{Kind: kind, Table: table, Room: -1, No: no}, now)
	}
}

// landScoped records the switches sw set, newly written and verified (its
// Bits the written byte's new &^ old). w.mu is held.
func (w *FrameWriter) landScoped(sw ScopedWrite, table int8, now time.Time) {
	for _, no := range sw.Switches() {
		w.land(SwitchKey(table, int(sw.Room), no), now)
	}
}

// landKills records the setIDs kw sets, newly written and verified. w.mu
// is held.
func (w *FrameWriter) landKills(kw KillWrite, table int8, now time.Time) {
	for _, id := range kw.SetIDs() {
		w.land(LandedBit{Kind: LandKill, Table: table, Room: kw.Room, No: id}, now)
	}
}
