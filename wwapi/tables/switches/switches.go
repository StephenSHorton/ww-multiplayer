// Package switches classifies the stage switches of the user's ISO for
// co-op (design §6.2). For every switch some placed actor writes or
// reads, in the store and scope the game keeps it in, it lists the
// writers with what they do, and decides whether co-op may sync it: only
// when it has a latching setter and every other writer latches too, that
// is, sets it and never clears or flips it, writes only while a Game Boy
// Advance is linked (the Tingle Tuner's, class GBA), or is a type-1
// TAG_EVSW, which only sets it from a synced story bit. A switch with a
// GBA writer syncs only while no GBA is linked (Switch.GBA), which the
// runtime checks every tick. An AND switch's setter latches only while
// all its inputs are synced, transitively, except that a one-shot AND
// latches over momentary inputs too (momentary: floor, iron-boots, sun
// and timed crystal switches), and is GBA-dependent while an input is. Two
// passes count set-and-clear writers as latching: statueSlots, where the
// game makes them so (a twin statue holds the slot down), and
// sharedFloorSwitches, a provisional sync policy where the game does not
// (an unpressed swflat clears its bit every frame) that a live check
// must confirm.
//
// Build is a pure function of the stages and the l_objectName table;
// its Table marshals to JSON for the cache §6.2 keys by DOL hash.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package switches

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Class is how a writer treats the switch it writes.
type Class string

const (
	// Latching writers set the switch and never clear or flip it, from a
	// cause the partner's set can stand for (§6.2 "Latching setters").
	Latching Class = "latching"
	// Unsafe writers clear it, flip it, or set it from local state that
	// they later undo: timers, momentary switches, per-frame mirrors,
	// push-block path bits, AND switches fed by an unsynced input.
	Unsafe Class = "unsafe"
	// Event writers set it as a story event starts or from an event bit;
	// the event travels in §6.5's story batch instead.
	Event Class = "event"
	// Unknown writers take a switch param the decomp doesn't tell the
	// behaviour of.
	Unknown Class = "unknown"
	// GBA writers write it only on a frame where a Game Boy Advance is
	// linked (mDoGaC_GbaLink, m_Do_gba_com.cpp:258-270): the Tingle
	// Tuner's (agbsw0, and the GBA's own commands through the Tuner
	// actor). They don't stop a switch from syncing, but such a switch
	// syncs only while no GBA is linked (Switch.GBA, §6.2).
	GBA Class = "gba"
)

// Op is what a writer does to its switch.
type Op string

const (
	OpSet      Op = "set"
	OpClear    Op = "clear"
	OpSetClear Op = "set+clear"
	OpToggle   Op = "toggle"
)

// Store is addr.SwitchStore with a text form, for JSON.
type Store addr.SwitchStore

const (
	StoreMemory = Store(addr.SwitchMemory)
	StoreDan    = Store(addr.SwitchDan)
	StoreZone   = Store(addr.SwitchZone)
	StoreRoom   = Store(addr.SwitchRoom)
)

func (s Store) String() string { return addr.SwitchStore(s).String() }

// MarshalText writes the store's name: "memory", "dan", "zone" or "room".
func (s Store) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads what MarshalText writes.
func (s *Store) UnmarshalText(b []byte) error {
	for _, st := range []Store{StoreMemory, StoreDan, StoreZone, StoreRoom} {
		if string(b) == st.String() {
			*s = st
			return nil
		}
	}
	return fmt.Errorf("switches: unknown store %q", b)
}

// Scope is where the game keeps a switch (dSv_info_c::onSwitch,
// d_save.cpp:1540-1560):
//
//   - memory (0x00-0x7F): the save table's dSv_memBit_c. Table is the
//     stage's STAG save table; Stage is "" and Room -1.
//   - dan (0x80-0xBF): dSv_danBit_c, which dStage_stagInfoInit resets
//     only when the save table changes (dComIfGs_initDan(stageNo) with
//     the STAG table, d_stage.cpp:1669-1677; dSv_danBit_c::init,
//     d_save.cpp:1225-1235), so every stage of one table shares it.
//     Scoped like memory. (The design says dan switches are per stage;
//     the decomp keeps them per table.)
//   - zone (0xC0-0xDF): the zone of the room the setter passes
//     (dComIfGp_roomControl_getZoneNo), one per loaded room: Stage and
//     Room. Table is the stage's, for grouping.
//   - room (0xE0-0xEF): the same zone's mSwitch[2], cleared on every
//     room change (clearRoomSwitch, d_save.cpp:1273-1276). Scoped like
//     zone.
type Scope struct {
	Store Store  `json:"store"`
	Table int    `json:"table"`
	Stage string `json:"stage,omitempty"`
	Room  int    `json:"room"`
}

// Key is one switch: a number in a scope.
type Key struct {
	Scope
	No int `json:"no"`
}

func (k Key) String() string {
	switch k.Store {
	case StoreZone, StoreRoom:
		return fmt.Sprintf("%s %s r%d 0x%02X", k.Store, k.Stage, k.Room, k.No)
	}
	return fmt.Sprintf("%s t%d 0x%02X", k.Store, k.Table, k.No)
}

func compareKey(a, b Key) int {
	return cmp.Or(
		cmp.Compare(a.Store, b.Store),
		cmp.Compare(a.Table, b.Table),
		cmp.Compare(a.Stage, b.Stage),
		cmp.Compare(a.Room, b.Room),
		cmp.Compare(a.No, b.No),
	)
}

// ErrNoSwitch is KeyFor's error for -1 and 0xFF, which the game's setters
// ignore (d_save.cpp:1544-1546).
var ErrNoSwitch = errors.New("switches: no switch")

// KeyFor routes switch no, written by an actor of save table table, stage
// stageName and room room, as dSv_info_c::onSwitch does. It fails with
// ErrNoSwitch for -1 and 0xFF, and otherwise for what the game can't
// route: a number past 0xEF (the JUT_ASSERT at d_save.cpp:1541-1542; a
// release build writes past the zone's bits instead), a memory or dan
// switch in a save table past 15 (getSave asserts, d_save.cpp:1507), and
// a zone or room switch from a room outside 0-63, such as the -1 of a
// stage.dzs actor (the assert at d_save.cpp:1553).
func KeyFor(table int, stageName string, room, no int) (Key, error) {
	st := addr.ClassifySwitch(no)
	switch st {
	case addr.SwitchNone:
		return Key{}, ErrNoSwitch
	case addr.SwitchMemory, addr.SwitchDan:
		if !addr.ValidTable(table) {
			return Key{}, fmt.Errorf("switch 0x%02X in save table %d, past the %d tables", no, table, addr.TableCount)
		}
		return Key{Scope{Store: Store(st), Table: table, Room: -1}, no}, nil
	case addr.SwitchZone, addr.SwitchRoom:
		if room < 0 || room >= stage.MaxRooms {
			return Key{}, fmt.Errorf("%s switch 0x%02X from room %d, which has no zone", addr.SwitchStore(st), no, room)
		}
		return Key{Scope{Store: Store(st), Table: table, Stage: stageName, Room: room}, no}, nil
	}
	return Key{}, fmt.Errorf("switch 0x%X is past the stores (0x00-0x%02X)", no, addr.SwitchEnd-1)
}

// Ref is the placed entry behind a writer or reader.
type Ref struct {
	Stage  string     `json:"stage"`
	Room   int        `json:"room"`  // -1 for stage.dzs
	Layer  int        `json:"layer"` // stage.AllLayers or 0-11
	Tag    string     `json:"tag"`
	Index  int        `json:"index"`
	Name   string     `json:"name"`
	Proc   stage.Proc `json:"proc"`
	Params uint32     `json:"params"`
	Angle  [3]int16   `json:"angle"`
}

func (r Ref) String() string {
	room := "stage"
	if r.Room >= 0 {
		room = fmt.Sprintf("r%d", r.Room)
	}
	return fmt.Sprintf("%s %s %s#%d %s", r.Stage, room, r.Tag, r.Index, r.Name)
}

// Writer is one actor's write of one switch.
type Writer struct {
	Ref
	Op    Op     `json:"op"`
	Class Class  `json:"class"`
	Why   string `json:"why"`
	// Inputs are an AND's inputs, routed in the AND's room.
	Inputs []Key `json:"inputs,omitempty"`
}

// Reader is one actor that acts on a switch.
type Reader struct {
	Ref
	Why string `json:"why"`
}

// Switch is one switch with its writers and readers, in placement order.
type Switch struct {
	Key
	Synced bool `json:"synced"`
	// GBA: a synced switch that syncs only while no Game Boy Advance is
	// linked to the game, since a GBA writer (the Tingle Tuner's) shares
	// it, or a latching AND writer has an input that is GBA. While a GBA
	// may be linked, a replica neither proposes nor writes it.
	GBA     bool     `json:"gba,omitempty"`
	Reason  string   `json:"reason"`
	Writers []Writer `json:"writers,omitempty"`
	Readers []Reader `json:"readers,omitempty"`
}

// PushBlock is a push block that stores its path point in switches
// (design §6.2: OR-ing two players' path bits gives a third point). The
// switches are listed low bit first and are never synced.
type PushBlock struct {
	Ref
	Path     int   `json:"path"`
	Switches []Key `json:"switches"`
}

// Undecoded is an entry of a proc that takes switch params the decomp
// doesn't decode; the switches it writes, if any, are not in the table.
type Undecoded struct {
	Ref
	Why string `json:"why"`
}

// Invalid is a switch use the game can't route (KeyFor's errors).
type Invalid struct {
	Ref
	No    int    `json:"no"`
	Write bool   `json:"write"`
	Why   string `json:"why"`
}

// Table is the classification of every stage's switches.
type Table struct {
	Stages     int         `json:"stages"`
	Users      int         `json:"users"`   // entries of a proc the table reads
	Unnamed    int         `json:"unnamed"` // entries whose name isn't in l_objectName, which the game doesn't create
	Switches   []Switch    `json:"switches"`
	PushBlocks []PushBlock `json:"push_blocks,omitempty"`
	Undecoded  []Undecoded `json:"undecoded,omitempty"`
	Invalid    []Invalid   `json:"invalid,omitempty"`
}

// Lookup returns switch k.
func (t *Table) Lookup(k Key) (*Switch, bool) {
	i, ok := slices.BinarySearchFunc(t.Switches, k, func(s Switch, k Key) int { return compareKey(s.Key, k) })
	if !ok {
		return nil, false
	}
	return &t.Switches[i], true
}

// Synced reports whether co-op may sync switch k (while no GBA is
// linked, for a GBA one: GBADependent).
func (t *Table) Synced(k Key) bool {
	s, ok := t.Lookup(k)
	return ok && s.Synced
}

// GBADependent reports whether switch k is synced only while no Game Boy
// Advance is linked (Switch.GBA).
func (t *Table) GBADependent(k Key) bool {
	s, ok := t.Lookup(k)
	return ok && s.Synced && s.GBA
}

// Build classifies the switches of stages, naming procs through names.
// Entries whose name names has no proc for are counted in Unnamed; with a
// nil names, every entry is. A nil stage is skipped.
func Build(stages []*stage.Stage, names *stage.ObjectNames) *Table {
	b := &builder{t: &Table{}, byKey: make(map[Key]*Switch), pos: make(map[Ref][3]float32)}
	for _, st := range stages {
		if st == nil {
			continue
		}
		b.t.Stages++
		for _, p := range st.All() {
			b.place(st, p, names)
		}
	}
	b.statueSlots()
	b.sharedFloorSwitches()
	b.propagate()
	b.finish()
	return b.t
}

type builder struct {
	t     *Table
	byKey map[Key]*Switch
	pos   map[Ref][3]float32 // where each Obj_Try and Obj_Correct is placed, for statueSlots
}

// sw returns switch k, adding it.
func (b *builder) sw(k Key) *Switch {
	s, ok := b.byKey[k]
	if !ok {
		s = &Switch{Key: k}
		b.byKey[k] = s
	}
	return s
}

func (b *builder) place(st *stage.Stage, p stage.Placed, names *stage.ObjectNames) {
	var proc stage.Proc
	ok := false
	if names != nil {
		proc, ok = names.Proc(p.Name)
	}
	if !ok {
		b.t.Unnamed++
		return
	}
	sp, ok := specByProc[proc]
	if !ok {
		return
	}
	b.t.Users++
	ref := Ref{
		Stage: st.Name, Room: p.Room, Layer: p.Layer, Tag: p.Tag, Index: p.Index,
		Name: p.Name, Proc: proc, Params: p.Params, Angle: p.Angle,
	}
	if proc == procTry || proc == procCorrect {
		b.pos[ref] = p.Pos
	}
	if sp.undecoded != "" {
		b.t.Undecoded = append(b.t.Undecoded, Undecoded{Ref: ref, Why: sp.undecoded})
		return
	}
	var push *PushBlock
	for _, u := range sp.decode(entry{p.Actor}) {
		room := p.Room
		if u.roomSet {
			room = u.room
		}
		k, err := KeyFor(st.SaveTable, st.Name, room, u.no)
		if err != nil {
			if !errors.Is(err, ErrNoSwitch) {
				b.t.Invalid = append(b.t.Invalid, Invalid{Ref: ref, No: u.no, Write: u.write, Why: err.Error()})
			}
			continue
		}
		s := b.sw(k)
		if !u.write {
			s.Readers = append(s.Readers, Reader{Ref: ref, Why: u.why})
			continue
		}
		w := Writer{Ref: ref, Op: u.op, Class: u.class, Why: u.why}
		for _, in := range u.inputs {
			ik, err := KeyFor(st.SaveTable, st.Name, room, in)
			if err != nil {
				// An input the game can't route: isSwitch reads nothing
				// (or garbage) there, so the AND is not trusted.
				if w.Class == Latching {
					w.Class = Unsafe
					w.Why += fmt.Sprintf("; input 0x%X: %v", in, err)
				}
				continue
			}
			w.Inputs = append(w.Inputs, ik)
		}
		s.Writers = append(s.Writers, w)
		if u.path >= 0 {
			if push == nil {
				b.t.PushBlocks = append(b.t.PushBlocks, PushBlock{Ref: ref, Path: u.path})
				push = &b.t.PushBlocks[len(b.t.PushBlocks)-1]
			}
			push.Switches = append(push.Switches, k)
		}
	}
}

// synced is the verdict on s's writers as they stand: a latching setter,
// and every other writer latching, GBA (one that writes only while a GBA
// is linked) or a TAG_EVSW of type 1. That one only ever sets the switch,
// and from a story event bit §6.5 carries (d_a_tag_evsw.cpp:10-48), so
// next to a latching setter it only pre-sets what the partner's set
// already stands for: WT r15's tags that grow every Makar tree once
// 0x4004 is on, ET's on 0x3A02. A switch it alone writes stays unsynced
// and follows its event bit.
func synced(s *Switch) bool {
	latch := false
	for _, w := range s.Writers {
		switch {
		case w.Class == Latching:
			latch = true
		case w.Class == GBA, w.Class == Event && w.Proc == stage.ProcTagEvsw:
		default:
			return false
		}
	}
	return latch
}

// momentary reports whether s is a momentary input: it has a momentary
// writer, and every other writer is momentary too or GBA. A momentary
// writer sets the switch from a press or hit and clears it again on its
// own: a floor or iron-boots switch that clears on release (swpush type
// 1, swheavy types 0 and 1, swflat types 1 and 2, a sun switch, swlight
// type 1, that clears when the light leaves; swpush type 2 only clears,
// so it is decoded OpClear and is no momentary input) or a timed
// crystal switch, whose timer clears it (SWHIT0 with a timer,
// d_a_swhit0.cpp:337-365). A one-shot AND fed by such inputs only ever
// sets its output (andsw0 behaviours 0 and 3, d_a_andsw0.cpp:40-57,
// :129-135; andsw2 type 0, d_a_andsw2.cpp:87-135), so the partner's set
// stands for the solved puzzle as any latching setter's does, while the
// presses and hits never sync (DRC's r16 floor switches feeding sw41, the
// r14 barricade; FW's r13 five-crystal gate sw7 and kinMB's exit pair
// sw226; TotG's r0 pair feeding sw12 and r6 triple feeding sw32, type 2
// swflats that stay down once that AND's output is on; ET's r3 and r16
// sun-switch pairs feeding sw16, sw19 and sw98). An area switch's
// per-frame writer (SW_C00 type 0) is not momentary, so an AND it feeds
// still can't spring the partner's ambush (§6.2); nor is an input another
// clearer writes: a switch timer (Obj_Timer), a timed AND's clear of its
// inputs (andsw0 behaviour 2) or a timed torch. A GBA writer (the Tingle
// Tuner's) writes only while a GBA is linked, so it leaves the input
// momentary, and markGBA makes the AND's output GBA-dependent (FW sw9, an
// sw7 input the Tuner shares). It reads only decoded classes, which
// propagate never changes.
func momentary(s *Switch) bool {
	mom := false
	for _, w := range s.Writers {
		switch {
		case w.Class == GBA:
		case w.Op == OpSetClear && (w.Proc == stage.ProcSwpush || w.Proc == stage.ProcSwheavy || w.Proc == stage.ProcSwhit0 || w.Proc == procSwflat || w.Proc == procSwlight):
			mom = true
		default:
			return false
		}
	}
	return mom
}

// statueSlotRadius2 is the squared distance within which an Obj_Correct
// slot of type 4 or 5 holds an Obj_Try while its switch is on: mode_on
// searches with field_0x2A4 = L_attr[type].field_0x4, 9.0f for both
// (d_a_obj_correct.cpp:30-39, :289-292), through chk_try_actor2, which
// also wants the statue's swSave to be the slot's (:101-121).
const statueSlotRadius2 = 9.0

// statueTwin is the Obj_Try type an Obj_Correct slot of each type takes,
// where that type reads its switch (tryReadsSwitch): search_tryKeyGate
// (slot type 4) takes UNK_5 and search_tryKeyDoor (type 5) UNK_6
// (d_a_obj_correct.cpp:186-229, M_search_proc :267-276). The other slots
// take statues that don't read the switch, or push blocks.
var statueTwin = map[int]int{4: 5, 5: 6}

// statueSlots makes an Obj_Correct slot's set-and-clear latching where
// the slot has a twin statue: an Obj_Try of the type it takes, reading
// its switch with params bit 31 set (so it appears while the switch is
// on), placed in the slot's room within statueSlotRadius2 of it. The slot
// sets the switch while a statue sits in it and clears it when the
// statue leaves (mode_off, mode_on, d_a_obj_correct.cpp:279-302), and it
// starts in mode_on when the switch is on (:59-63). So in a game that
// receives the switch, the carried statue doesn't appear (bit 31 clear)
// and the twin does, in the slot, which keeps the switch on: the
// partner's set stands for the placed statue (TotG r0: CrTrM1 with Hmon1d
// on sw5, the iron grate; CrTrM2 with Hmon2d on sw6, the water spouts).
// A player who lifts the twin out clears it in his own game until it is
// next applied, and chk_appear refuses the twin while a statue of its
// type already exists (M_attr +0x71, d_a_obj_try.rel 0x4D0-0x508).
func (b *builder) statueSlots() {
	for _, s := range b.byKey {
		for i := range s.Writers {
			w := &s.Writers[i]
			if w.Proc != procCorrect || w.Op != OpSetClear || w.Class != Unsafe {
				continue
			}
			want, ok := statueTwin[int(w.Params&0xF)]
			if !ok {
				continue
			}
			wp := b.pos[w.Ref]
			for _, r := range s.Readers {
				if r.Proc != procTry || r.Stage != w.Stage || r.Room != w.Room || int(r.Params&0xF) != want || r.Params>>31 == 0 {
					continue
				}
				rp := b.pos[r.Ref]
				dx, dy, dz := float64(rp[0]-wp[0]), float64(rp[1]-wp[1]), float64(rp[2]-wp[2])
				if dx*dx+dy*dy+dz*dz >= statueSlotRadius2 {
					continue
				}
				w.Class = Latching
				w.Why += fmt.Sprintf("; latches: its twin statue %v appears in the slot while it is on", r.Ref)
				break
			}
		}
	}
}

// sharedFloorSwitches keeps a switch that a momentary swflat shares with
// a latching setter synced, as it was before Obj_Swflat was decoded: the
// swflat's writer is made latching when the switch has another latching
// writer that is neither an AND (whose class propagate may still lower)
// nor a swflat. TotG has four: sw0 (r0 #18, a type 2 that stays down once
// sw28 is on, beside the Hmlif and Kui setters; the r0-r4 door reads it),
// sw14 (r12 #0, beside the r8-r10 key door's keyInit), and sw16 and sw17
// (r12 #1 and #4, beside the boss-key chest and the r22 Joy Pendant
// chest). Unpressed, such a swflat clears the bit every frame in the game
// it stands in (d_a_obj_swflat.rel 0xF08), as in a single-player game;
// whether that relocks the key door or recloses a chest is for a live
// check (the TotG slice's C2), so the rule is provisional. Only swflat is
// treated so, which leaves every other floor switch's verdict as it was.
func (b *builder) sharedFloorSwitches() {
	for _, s := range b.byKey {
		var by *Writer
		for i := range s.Writers {
			if w := &s.Writers[i]; w.Class == Latching && len(w.Inputs) == 0 && w.Proc != procSwflat {
				by = w
				break
			}
		}
		if by == nil {
			continue
		}
		for i := range s.Writers {
			if w := &s.Writers[i]; w.Proc == procSwflat && w.Op == OpSetClear && w.Class == Unsafe {
				w.Class = Latching
				w.Why += fmt.Sprintf("; counted as latching: it shares the switch with latching setter %v (provisional until a live check)", by.Ref)
			}
		}
	}
}

// gbaInput reports whether AND input s may be written by a Game Boy
// Advance: it is synced only while none is linked (GBA), or it is a
// momentary input with a GBA writer.
func gbaInput(s *Switch) bool {
	return s != nil && (s.GBA || momentary(s) && slices.ContainsFunc(s.Writers, func(w Writer) bool { return w.Class == GBA }))
}

// propagate demotes every latching AND writer with an input that is not
// synced, until nothing changes: the greatest set of synced switches
// closed under "an AND is synced only if its inputs are". Inputs with no
// known writer are not synced. A one-shot AND (OpSet) keeps latching over
// a momentary input. Each switch is queued at most once, when it first
// stops being synced, so a cycle of ANDs ends; one with no unsynced input
// anywhere stays synced, as no unsafe writer can reach it.
func (b *builder) propagate() {
	type dep struct {
		s *Switch
		w int
	}
	keys := b.sortedKeys()
	deps := make(map[Key][]dep)
	for _, k := range keys {
		s := b.byKey[k]
		for i, w := range s.Writers {
			for _, in := range w.Inputs {
				deps[in] = append(deps[in], dep{s, i})
			}
		}
	}
	// An input nothing writes or reads otherwise still gets its entry.
	for in := range deps {
		if _, ok := b.byKey[in]; !ok {
			b.sw(in)
			keys = append(keys, in)
		}
	}
	slices.SortFunc(keys, compareKey)

	down := make(map[Key]bool)
	var queue []Key
	for _, k := range keys {
		if !synced(b.byKey[k]) {
			down[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		mom := momentary(b.byKey[k])
		for _, d := range deps[k] {
			w := &d.s.Writers[d.w]
			if w.Class != Latching || mom && w.Op == OpSet {
				continue
			}
			w.Class = Unsafe
			w.Why += fmt.Sprintf("; fed by %v, which is not synced", k)
			if !down[d.s.Key] {
				down[d.s.Key] = true
				queue = append(queue, d.s.Key)
			}
		}
	}
}

func (b *builder) sortedKeys() []Key {
	keys := make([]Key, 0, len(b.byKey))
	for k := range b.byKey {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareKey)
	return keys
}

// finish writes out the switches in key order with their verdicts.
func (b *builder) finish() {
	keys := b.sortedKeys()
	for _, k := range keys {
		s := b.byKey[k]
		s.Synced = synced(s)
	}
	b.markGBA(keys)
	b.t.Switches = make([]Switch, 0, len(keys))
	for _, k := range keys {
		s := b.byKey[k]
		s.Reason = reason(s)
		b.t.Switches = append(b.t.Switches, *s)
	}
}

// markGBA sets GBA on every synced switch that a GBA writer shares, and
// then, until nothing changes, on every synced switch a latching AND
// writer sets from an input that is GBA or a momentary input a GBA writer
// shares (gbaInput): while a GBA is linked that input may be the Tuner's,
// so the AND's output is too. Each pass marks at least one more switch or
// ends, so it ends.
func (b *builder) markGBA(keys []Key) {
	for _, k := range keys {
		s := b.byKey[k]
		s.GBA = s.Synced && slices.ContainsFunc(s.Writers, func(w Writer) bool { return w.Class == GBA })
	}
	for changed := true; changed; {
		changed = false
		for _, k := range keys {
			s := b.byKey[k]
			if !s.Synced || s.GBA {
				continue
			}
			for _, w := range s.Writers {
				if w.Class == Latching && slices.ContainsFunc(w.Inputs, func(in Key) bool { return gbaInput(b.byKey[in]) }) {
					s.GBA, changed = true, true
					break
				}
			}
		}
	}
}

func reason(s *Switch) string {
	if len(s.Writers) == 0 {
		return "no known setter"
	}
	var gba, evsw *Writer
	for i, w := range s.Writers {
		switch {
		case w.Class == Latching:
		case w.Class == GBA:
			if gba == nil {
				gba = &s.Writers[i]
			}
		case s.Synced && w.Class == Event && w.Proc == stage.ProcTagEvsw:
			if evsw == nil {
				evsw = &s.Writers[i]
			}
		default:
			return fmt.Sprintf("%s writer %v: %s", w.Class, w.Ref, w.Why)
		}
	}
	r := "every setter latches"
	switch {
	case !s.Synced:
		return fmt.Sprintf("no latching setter: only gba writers (%v: %s), which write it only while a GBA is linked", gba.Ref, gba.Why)
	case gba != nil:
		r = fmt.Sprintf("every setter latches; gba writer %v writes it only while a GBA is linked, so it syncs only while none is", gba.Ref)
	case s.GBA:
		r = "every setter latches; an AND input syncs only while no GBA is linked, so it does too"
	}
	if evsw != nil {
		r += fmt.Sprintf("; event writer %v only pre-sets it (%s)", evsw.Ref, evsw.Why)
	}
	return r
}
