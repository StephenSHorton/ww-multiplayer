// Package switches classifies the stage switches of the user's ISO for
// co-op (design §6.2). For every switch some placed actor writes or
// reads, in the store and scope the game keeps it in, it lists the
// writers with what they do, and decides whether co-op may sync it: only
// when it has a setter and every setter latches, that is, sets it and
// never clears or flips it. An AND switch's setter latches only while
// all its inputs are synced, transitively.
//
// Build is a pure function of the stages and the l_objectName table;
// its Table marshals to JSON for the cache §6.2 keys by DOL hash.
package switches

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
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
// stage and room room, as dSv_info_c::onSwitch does. It fails with
// ErrNoSwitch for -1 and 0xFF, and otherwise for what the game can't
// route: a number past 0xEF (the JUT_ASSERT at d_save.cpp:1541-1542; a
// release build writes past the zone's bits instead), a memory or dan
// switch in a save table past 15 (getSave asserts, d_save.cpp:1507), and
// a zone or room switch from a room outside 0-63, such as the -1 of a
// stage.dzs actor (the assert at d_save.cpp:1553).
func KeyFor(table int, stage string, room, no int) (Key, error) {
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
		if room < 0 || room >= stagedata.MaxRooms {
			return Key{}, fmt.Errorf("%s switch 0x%02X from room %d, which has no zone", addr.SwitchStore(st), no, room)
		}
		return Key{Scope{Store: Store(st), Table: table, Stage: stage, Room: room}, no}, nil
	}
	return Key{}, fmt.Errorf("switch 0x%X is past the stores (0x00-0x%02X)", no, addr.SwitchEnd-1)
}

// Ref is the placed entry behind a writer or reader.
type Ref struct {
	Stage  string         `json:"stage"`
	Room   int            `json:"room"`  // -1 for stage.dzs
	Layer  int            `json:"layer"` // stagedata.AllLayers or 0-11
	Tag    string         `json:"tag"`
	Index  int            `json:"index"`
	Name   string         `json:"name"`
	Proc   stagedata.Proc `json:"proc"`
	Params uint32         `json:"params"`
	Angle  [3]int16       `json:"angle"`
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
	Synced  bool     `json:"synced"`
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

// Synced reports whether co-op may sync switch k.
func (t *Table) Synced(k Key) bool {
	s, ok := t.Lookup(k)
	return ok && s.Synced
}

// Build classifies the switches of stages, naming procs through names.
// Entries whose name names has no proc for are counted in Unnamed; with a
// nil names, every entry is. A nil stage is skipped.
func Build(stages []*stagedata.Stage, names *stagedata.ObjectNames) *Table {
	b := &builder{t: &Table{}, byKey: make(map[Key]*Switch)}
	for _, st := range stages {
		if st == nil {
			continue
		}
		b.t.Stages++
		for _, p := range st.All() {
			b.place(st, p, names)
		}
	}
	b.propagate()
	b.finish()
	return b.t
}

type builder struct {
	t     *Table
	byKey map[Key]*Switch
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

func (b *builder) place(st *stagedata.Stage, p stagedata.Placed, names *stagedata.ObjectNames) {
	var proc stagedata.Proc
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

// synced is the verdict on s's writers as they stand: a setter, and every
// writer latching.
func synced(s *Switch) bool {
	if len(s.Writers) == 0 {
		return false
	}
	for _, w := range s.Writers {
		if w.Class != Latching {
			return false
		}
	}
	return true
}

// propagate demotes every latching AND writer with an input that is not
// synced, until nothing changes: the greatest set of synced switches
// closed under "an AND is synced only if its inputs are". Inputs with no
// known writer are not synced. Each switch is queued at most once, when
// it first stops being synced, so a cycle of ANDs ends; one with no
// unsynced input anywhere stays synced, as no unsafe writer can reach it.
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
		for _, d := range deps[k] {
			w := &d.s.Writers[d.w]
			if w.Class != Latching {
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
	b.t.Switches = make([]Switch, 0, len(keys))
	for _, k := range keys {
		s := b.byKey[k]
		s.Synced = synced(s)
		s.Reason = reason(s)
		b.t.Switches = append(b.t.Switches, *s)
	}
}

func reason(s *Switch) string {
	if len(s.Writers) == 0 {
		return "no known setter"
	}
	for _, w := range s.Writers {
		if w.Class != Latching {
			return fmt.Sprintf("%s writer %v: %s", w.Class, w.Ref, w.Why)
		}
	}
	return "every setter latches"
}
