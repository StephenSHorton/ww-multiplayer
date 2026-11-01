package enemies

import (
	"math"
	"slices"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// What M6.1's placement-matched kills read of the enemy table
// (docs/08-coop-design.md §7.2a): which unkeyed placed enemies of a room
// a partner's kill may be shared for, by what ordinal both sides name
// them, and how a live actor is matched to its entry.
//
// An unkeyed enemy (setID 0xFFFF) has no key the game records
// (d_save.cpp:1671-1674), so the two games name it by its placement: its
// ordinal is the index of its entry among the (stage, room)'s entries in
// Table.Enemies order, across layers, which is the same on both sides when
// their tables' identity matches (TableFeatures). A live actor matches an
// entry by its Fingerprint, what fopAc_Create copied from the entry
// (f_op_actor.cpp:270-283): proc, setID, params and home's raw bits, with
// three class rules (Fingerprint). Entries of one room with equal
// fingerprints, on any layer, form an interchangeable class.
//
// A placed entry is one whose kill is shared: an unkeyed K1 entry of a
// room 0..63, and none of:
//
//   - BOSS on creation (Entry.Boss);
//   - Morth: every placed KS is a generator (d_a_ks.cpp:1582-1610);
//   - a searchlight Bokoblin (params & 0xF == 6), whose stand2 keeps
//     home.pos on the light (d_a_bk.cpp:1455-1463), so it matches nothing;
//   - a Miniblin with a byte-2 switch (params>>16 & 0xFF != 0xFF): the
//     light-shrink skips the bookkeeping that sets it (pt.rel .text
//     0x3F08-0x3F40), so the receiver's Miniblin would keep respawning.
//
// The index is built on first use and kept out of the JSON the cache
// stores, as the kill index is.

// MaxOrdinal bounds the ordinals PKILL carries: its 16 u32 words.
const MaxOrdinal = 512

// Fingerprint is what a live actor keeps of its entry, by which M6.1
// matches the two. Pos and Angle are home.pos's f32 bits and home.angle,
// after the class rules:
//
//   - Keese: home y is a wildcard (0), since it moves to the ceiling
//     (d_a_ki.cpp:678-682);
//   - Kargaroc: an entry's home y is f32(y + 70), as daBb_Create raises
//     it (d_a_bb.cpp:2709-2711);
//   - Floormaster: angle x and z are 0, as its create clears them
//     (d_a_fm.cpp:3296-3299).
type Fingerprint struct {
	Proc   stage.Proc
	SetID  uint16
	Params uint32
	Pos    [3]uint32
	Angle  [3]int16
}

// EntryFingerprint is e's fingerprint, as its actor will have it.
func EntryFingerprint(e Entry) Fingerprint {
	fp := Fingerprint{Proc: e.Proc, SetID: e.SetID, Params: e.Params, Angle: e.Angle}
	for i, v := range e.Pos {
		fp.Pos[i] = math.Float32bits(v)
	}
	if e.Proc == stage.ProcBB {
		fp.Pos[1] = math.Float32bits(e.Pos[1] + 70)
	}
	return normalize(fp)
}

// ActorFingerprint is the fingerprint of a live actor by what it read:
// proc, setID, params (+0xB0), home.pos's raw bits and home.angle.
func ActorFingerprint(proc stage.Proc, setID uint16, params uint32, homePos [3]uint32, homeAngle [3]int16) Fingerprint {
	return normalize(Fingerprint{Proc: proc, SetID: setID, Params: params, Pos: homePos, Angle: homeAngle})
}

// normalize applies the class rules both sides share.
func normalize(fp Fingerprint) Fingerprint {
	switch fp.Proc {
	case stage.ProcKI:
		fp.Pos[1] = 0
	case stage.ProcFM:
		fp.Angle[0], fp.Angle[2] = 0, 0
	}
	// -0 and +0 are one position to the game's code; the entry's and the
	// actor's bits are the same float either way.
	for i, v := range fp.Pos {
		if v == 0x80000000 {
			fp.Pos[i] = 0
		}
	}
	return fp
}

// Placement is one placed entry of a room.
type Placement struct {
	Ordinal int
	Entry   Entry
	FP      Fingerprint
	// Class is the ordinals of the room's placed entries with FP, this
	// one's included, ascending: any live member stands for any of them.
	Class []int
}

// PlacedRoom is a room's placed entries.
type PlacedRoom struct {
	byOrd map[int]*Placement
	byFP  map[Fingerprint][]int
	ords  []int // ascending
}

// Ordinals returns the room's placed ordinals, ascending.
func (r *PlacedRoom) Ordinals() []int {
	if r == nil {
		return nil
	}
	return r.ords
}

// At returns the placement of ordinal, if it is placed.
func (r *PlacedRoom) At(ordinal int) (*Placement, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.byOrd[ordinal]
	return p, ok
}

// Match returns the class of a live actor's fingerprint: the ordinals it
// may stand for, ascending, or none.
func (r *PlacedRoom) Match(fp Fingerprint) []int {
	if r == nil {
		return nil
	}
	return r.byFP[normalize(fp)]
}

// PlacedEligible reports whether e's kill is shared (the list above),
// whatever its room holds; countsAsK1 and a room 0..63 come first.
func PlacedEligible(e Entry) bool {
	_, ok := countsAsK1(e)
	switch {
	case !ok || e.Key == Keyed || e.SetID != 0xFFFF || e.Room < 0 || e.Room >= 64:
		return false
	case e.Proc == stage.ProcKS:
		return false
	case e.Proc == stage.ProcBK && e.Params&0xF == 6:
		return false
	case e.Proc == stage.ProcPT && (e.Params>>16)&0xFF != 0xFF:
		return false
	}
	return true
}

type placedIndex struct {
	rooms map[killRoom]*PlacedRoom
	total int
}

type placedCache struct {
	once sync.Once
	idx  *placedIndex
}

func (t *Table) placed() *placedIndex {
	t.place.once.Do(func() { t.place.idx = buildPlacedIndex(t.Enemies) })
	return t.place.idx
}

// PlacedIn returns the placed entries of (stage, room), nil for none.
func (t *Table) PlacedIn(stage string, room int) *PlacedRoom {
	return t.placed().rooms[killRoom{stage, room}]
}

// PlacedOK reports whether ordinal names a placed entry of (stage, room).
func (t *Table) PlacedOK(stage string, room, ordinal int) bool {
	_, ok := t.PlacedIn(stage, room).At(ordinal)
	return ok
}

// PlacedTotal counts the placed entries of the table.
func (t *Table) PlacedTotal() int { return t.placed().total }

func buildPlacedIndex(es []Entry) *placedIndex {
	idx := &placedIndex{rooms: map[killRoom]*PlacedRoom{}}
	next := map[killRoom]int{}
	for _, e := range es {
		r := killRoom{e.Stage, e.Room}
		ord := next[r]
		next[r]++
		if ord >= MaxOrdinal || !PlacedEligible(e) {
			continue
		}
		pr := idx.rooms[r]
		if pr == nil {
			pr = &PlacedRoom{byOrd: map[int]*Placement{}, byFP: map[Fingerprint][]int{}}
			idx.rooms[r] = pr
		}
		fp := EntryFingerprint(e)
		pr.byOrd[ord] = &Placement{Ordinal: ord, Entry: e, FP: fp}
		pr.byFP[fp] = append(pr.byFP[fp], ord)
		pr.ords = append(pr.ords, ord)
		idx.total++
	}
	for _, pr := range idx.rooms {
		for _, ords := range pr.byFP {
			for _, o := range ords {
				pr.byOrd[o].Class = slices.Clone(ords)
			}
		}
	}
	return idx
}
