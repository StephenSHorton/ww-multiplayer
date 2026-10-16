// Package enemies builds the M1 enemy table (docs/08-coop-design.md §7.1-
// 7.2, §9 M1, §11 item 8) from the user's own stage files: every placed
// entry whose proc is an enemy (classes.go), with where it is, its proc,
// setID, position, angles and params, and the setID report that is M6's
// go/no-go: how many enemy entries the game can record dead by (stage,
// room, setID), and which (stage, room, setID) keys more than one entry
// shares.
//
// What the game does with a setID (all of it is the decomp's):
//
//   - dStage_actorInit copies an 0x20-byte entry's setID to the actor and
//     skips the entry when its room's zone has recorded that setID dead
//     (dComIfGs_isActor, d_stage.cpp:1688-1708). dStage_tgscInfoInit
//     copies a 0x24-byte entry's (TGSC, SCOB, DOOR, TGDR, SCOn) too but
//     never checks it (:1712-1730).
//   - fopAcM_onActor records setID dead in the zone of the actor's
//     home.roomNo (f_op_actor_mng.h:444-446; dSv_info_c::onActor,
//     d_save.cpp:1671-1682). Only the classes classes.go lists as
//     recording a death call it.
//   - Both ignore setID 0xFFFF and room -1 (d_save.cpp:1672-1674,
//     :1686-1688), which a stage.dzs entry has (stagedata.Placed). A
//     runtime spawn gets 0xFFFF (fopAcM_CreateAppend,
//     f_op_actor_mng.cpp:78-91).
//   - A zone holds bits 0-511 (dSv_zoneActor_c::ACTOR_MAX, d_save.h:780).
//     onActor and isActor assert on a larger setID (d_save.cpp:1676,
//     :1690-1692). JUT_ASSERT is compiled into GZLE01 (JUTAssert.h:9)
//     and its OSPanic halts the game (m_Do_printf.cpp:208-230), so such
//     a setID halts it when the entry's room loads, if the entry is dead-
//     checked, or when it records its death.
//   - The bits are per room, so one key is one bit whatever the actor:
//     a recording death of one entry keeps every dead-checked entry with
//     its (stage, room, setID) from being created until the zone goes
//     (dStage_roomControl_c::init clears the zones when a stage is
//     entered at a start point, d_stage.cpp:124-126). Only one layer's
//     chunks are read per room
//     (stagedata's dzx.go), so entries on different layers never meet
//     in one game; two players can load different layers (§7.2).
//   - NPC event cuts find their actor by setID ("SetId",
//     d_npc_event_cut.inc:106-111, :353), so a nonzero setID on an NPC
//     need not be a zone bit.
//
// Build is a pure function of the stages and the l_objectName table; its
// Table marshals to JSON for the cache §6.2 keys by DOL hash.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), as in internal/coop/addr.
package enemies

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// NoSetID is the setID of an entry the game never records (and of every
// runtime spawn).
const NoSetID uint16 = addr.ActorSetIDNone

// MaxSetID bounds the setIDs a zone holds: 0 to MaxSetID-1.
const MaxSetID = addr.ZoneActorCount

// KeyState is what an entry's setID gives co-op.
type KeyState string

const (
	// Keyed entries are recorded dead, and found, by (stage, room,
	// setID): a setID below MaxSetID in a room's file.
	Keyed KeyState = "keyed"
	// NoKey entries have setID 0xFFFF.
	NoKey KeyState = "none"
	// StageFile entries are in stage.dzs, created with room -1, whose
	// setID onActor and isActor ignore.
	StageFile KeyState = "stage_file"
	// OutOfRange entries have a setID of MaxSetID-0xFFFE, past the zone,
	// on which the game halts (see the package comment).
	OutOfRange KeyState = "out_of_range"
)

// keyState classifies a placed entry's setID.
func keyState(room int, setID uint16) KeyState {
	switch {
	case setID == NoSetID:
		return NoKey
	case room < 0:
		return StageFile
	case int(setID) >= MaxSetID:
		return OutOfRange
	}
	return Keyed
}

// Vec is a position. It marshals each coordinate as a JSON number, or,
// for the values JSON has no number for, as the string "NaN", "+Inf" or
// "-Inf", so a table built from any stage file encodes and reads back.
type Vec [3]float32

// MarshalJSON writes v as a three-element array.
func (v Vec) MarshalJSON() ([]byte, error) {
	b := []byte{'['}
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		switch g := float64(f); {
		case math.IsNaN(g):
			b = append(b, `"NaN"`...)
		case math.IsInf(g, 1):
			b = append(b, `"+Inf"`...)
		case math.IsInf(g, -1):
			b = append(b, `"-Inf"`...)
		default:
			b = strconv.AppendFloat(b, g, 'g', -1, 32)
		}
	}
	return append(b, ']'), nil
}

// UnmarshalJSON reads what MarshalJSON writes.
func (v *Vec) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != len(v) {
		return fmt.Errorf("enemies: a position has %d coordinates, not %d", len(raw), len(v))
	}
	for i, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			switch s {
			case "NaN":
				v[i] = float32(math.NaN())
			case "+Inf":
				v[i] = float32(math.Inf(1))
			case "-Inf":
				v[i] = float32(math.Inf(-1))
			default:
				return fmt.Errorf("enemies: coordinate %q", s)
			}
			continue
		}
		f, err := strconv.ParseFloat(string(r), 32)
		if err != nil {
			return fmt.Errorf("enemies: coordinate %s: %w", r, err)
		}
		v[i] = float32(f)
	}
	return nil
}

// Entry is one placed enemy.
type Entry struct {
	Stage  string         `json:"stage"`
	Room   int            `json:"room"`  // -1: the stage's own stage.dzs
	Layer  int            `json:"layer"` // stagedata.AllLayers (-1) or 0-11
	Tag    string         `json:"tag"`   // its chunk: "ACTR", "ACT3", "SCOB", ...
	Index  int            `json:"index"` // in the chunk
	Name   string         `json:"name"`  // its l_objectName name: "Bk"
	Proc   stagedata.Proc `json:"proc"`
	Class  string         `json:"class"` // Class.Name: "BK"
	SetID  uint16         `json:"set_id"`
	Pos    Vec            `json:"pos"`
	Angle  [3]int16       `json:"angle"`
	Params uint32         `json:"params"`
	Key    KeyState       `json:"key"`
	// Checked is false for a 0x24-byte entry, which the game creates
	// again however dead its setID is recorded.
	Checked bool `json:"checked"`
	// Boss is true when the game gives the actor BOSS status on creation
	// (Class.IsBoss), which §7.2's K1 gate refuses.
	Boss bool `json:"boss,omitempty"`
	// Shared is true for a Keyed entry another enemy on a common layer
	// shares its key with: §7.2's KILL by that key reaches both.
	Shared bool `json:"shared,omitempty"`
}

func (e Entry) String() string {
	return fmt.Sprintf("%s %s %s#%d %q (%s) setID 0x%04X", e.Stage, roomName(e.Room), e.Tag, e.Index, e.Name, e.Class, e.SetID)
}

// Ref is one member of a Duplicate.
type Ref struct {
	Layer   int            `json:"layer"`
	Tag     string         `json:"tag"`
	Index   int            `json:"index"`
	Name    string         `json:"name"`
	Proc    stagedata.Proc `json:"proc"`
	Class   string         `json:"class,omitempty"` // the enemy class; "" for a non-enemy
	Checked bool           `json:"checked"`         // dead-checked: an 0x20-byte entry
	Records bool           `json:"records"`         // its class may record its death (Death.MayRecord)
}

func (r Ref) String() string {
	s := fmt.Sprintf("%s#%d %s", r.Tag, r.Index, r.Name)
	if r.Class != "" {
		s += " (" + r.Class + ")"
	}
	return s
}

// Duplicate is a (stage, room, setID) key more than one created entry has.
type Duplicate struct {
	Stage   string `json:"stage"`
	Room    int    `json:"room"`
	SetID   uint16 `json:"set_id"`
	Entries []Ref  `json:"entries"` // in placement order
	// Enemies counts the enemy entries.
	Enemies int `json:"enemies"`
	// LayerOnly is true when no layer creates two of them, so they never
	// meet in one game (though two players' games can, §7.2).
	LayerOnly bool `json:"layer_only"`
	// SharedKey is true when two enemies are on a common layer.
	SharedKey bool `json:"shared_key"`
	// Collides is true when, on some layer, one entry's recorded death
	// keeps another, dead-checked, from being created; Why names a pair.
	Collides bool   `json:"collides"`
	Why      string `json:"why,omitempty"`
}

// Share counts enemy entries by what their setID gives co-op.
type Share struct {
	Entries    int `json:"entries"`
	WithSetID  int `json:"with_set_id"`  // setID != 0xFFFF
	StageFile  int `json:"stage_file"`   // of those, in stage.dzs: ignored
	OutOfRange int `json:"out_of_range"` // of those, past the zone's bits: the game halts
	Keyed      int `json:"keyed"`        // the rest: Keyed
	Unchecked  int `json:"unchecked"`    // keyed entries the game never dead-checks
	Shared     int `json:"shared"`       // keyed entries that share their key with another enemy
}

func (s *Share) add(e Entry) {
	s.Entries++
	if e.SetID == NoSetID {
		return
	}
	s.WithSetID++
	switch e.Key {
	case StageFile:
		s.StageFile++
	case OutOfRange:
		s.OutOfRange++
	case Keyed:
		s.Keyed++
		if !e.Checked {
			s.Unchecked++
		}
		if e.Shared {
			s.Shared++
		}
	}
}

// ClassShare is a Share for one class.
type ClassShare struct {
	Class string `json:"class"`
	Share
}

// StageShare is a Share for one stage.
type StageShare struct {
	Stage string `json:"stage"`
	Share
}

// Summary is the setID report's numbers.
type Summary struct {
	All Share `json:"all"`
	// K1 is the K1 classes' entries without BOSS status: what M6 can
	// shrink.
	K1      Share        `json:"k1"`
	ByClass []ClassShare `json:"by_class,omitempty"` // in Classes order
	ByStage []StageShare `json:"by_stage,omitempty"` // stages with enemies, in input order
}

// Table is the enemy table.
type Table struct {
	Stages  int `json:"stages"`
	Placed  int `json:"placed"`  // entries in every file
	Unnamed int `json:"unnamed"` // entries l_objectName has no name for, which the game never creates
	// Halting counts the dead-checked entries of rooms, named or not,
	// whose setID is MaxSetID-0xFFFE: isActor halts the game on each
	// when its room loads (dStage_actorInit checks before it looks the
	// name up, d_stage.cpp:1693-1702).
	Halting    int         `json:"halting,omitempty"`
	Enemies    []Entry     `json:"enemies,omitempty"`
	Duplicates []Duplicate `json:"duplicates,omitempty"`
	Summary    Summary     `json:"summary"`

	kill killCache // M6's index (killindex.go), built on first use
}

// Lookup returns the enemy entries of key (stage, room, setID), every
// layer, in table order: what §7.2 cross-checks a live enemy against. It
// scans the whole table.
func (t *Table) Lookup(stage string, room int, setID uint16) []Entry {
	var out []Entry
	for _, e := range t.Enemies {
		if e.Stage == stage && e.Room == room && e.SetID == setID {
			out = append(out, e)
		}
	}
	return out
}

// member is a created entry of a Duplicate candidate.
type member struct {
	ref   Ref
	enemy int // index in Table.Enemies; -1 for a non-enemy
}

// groupKey is a (stage, room, setID) key; the stage by its index in
// Build's input, so that two stages with one name stay apart.
type groupKey struct {
	stage, room int
	setID       uint16
}

// Build reads stages, as stagedata.Load returns them, with names, the
// user's l_objectName. Entries whose name names has no proc for are
// counted in Unnamed and otherwise left out, since the game never creates
// them (dStage_actorCreate, d_stage.cpp:1303-1304); with a nil names,
// every entry is. A nil stage is skipped.
func Build(stages []*stagedata.Stage, names *stagedata.ObjectNames) *Table {
	t := &Table{}
	groups := make(map[groupKey][]member)
	var keys []groupKey
	stageName := make(map[int]string)
	for si, st := range stages {
		if st == nil {
			continue
		}
		t.Stages++
		stageName[si] = st.Name
		for _, p := range st.All() {
			t.Placed++
			if p.Room >= 0 && p.DeadChecked() && keyState(p.Room, p.SetID) == OutOfRange {
				t.Halting++
			}
			var proc stagedata.Proc
			ok := false
			if names != nil {
				proc, ok = names.Proc(p.Name)
			}
			if !ok {
				t.Unnamed++
				continue
			}
			ref := Ref{Layer: p.Layer, Tag: p.Tag, Index: p.Index, Name: p.Name, Proc: proc, Checked: p.DeadChecked()}
			m := member{enemy: -1}
			if c, ok := ClassOf(proc); ok {
				ref.Class, ref.Records = c.Name, c.Death.MayRecord()
				m.enemy = len(t.Enemies)
				t.Enemies = append(t.Enemies, Entry{
					Stage: st.Name, Room: p.Room, Layer: p.Layer, Tag: p.Tag, Index: p.Index,
					Name: p.Name, Proc: proc, Class: c.Name, SetID: p.SetID,
					Pos: Vec(p.Pos), Angle: p.Angle, Params: p.Params,
					Key: keyState(p.Room, p.SetID), Checked: p.DeadChecked(),
					Boss: c.IsBoss(p.Params, p.Angle),
				})
			}
			if p.Room < 0 || p.SetID == NoSetID {
				continue // the game never records or checks it
			}
			k := groupKey{si, p.Room, p.SetID}
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			m.ref = ref
			groups[k] = append(groups[k], m)
		}
	}
	slices.SortFunc(keys, func(a, b groupKey) int {
		return cmp.Or(cmp.Compare(a.stage, b.stage), cmp.Compare(a.room, b.room), cmp.Compare(a.setID, b.setID))
	})
	for _, k := range keys {
		ms := groups[k]
		if len(ms) < 2 {
			continue
		}
		d := duplicate(ms)
		d.Stage, d.Room, d.SetID = stageName[k.stage], k.room, k.setID
		if d.SharedKey {
			markShared(t.Enemies, ms)
		}
		t.Duplicates = append(t.Duplicates, d)
	}
	t.Summary = summarize(t.Enemies)
	return t
}

// layerCounts counts the members of a group a layer of a room creates:
// those on every layer, plus each layer's own.
type layerCounts struct {
	all   int
	layer [stagedata.NumLayers]int
}

func (c *layerCounts) add(layer int) {
	if layer >= 0 && layer < stagedata.NumLayers {
		c.layer[layer]++
	} else {
		c.all++
	}
}

// on is how many members layer l creates.
func (c *layerCounts) on(l int) int { return c.all + c.layer[l] }

// most is the most members any one layer creates.
func (c *layerCounts) most() int {
	n := c.all
	for l := range stagedata.NumLayers {
		n = max(n, c.on(l))
	}
	return n
}

// created reports whether layer l creates an entry on layer layer.
func created(layer, l int) bool { return layer == stagedata.AllLayers || layer == l }

// duplicate fills in what a group of two or more members shares, in time
// linear in its size.
func duplicate(ms []member) Duplicate {
	d := Duplicate{Entries: make([]Ref, len(ms))}
	var every, enemies, recorders, checked, both layerCounts
	for i, m := range ms {
		d.Entries[i] = m.ref
		every.add(m.ref.Layer)
		if m.enemy >= 0 {
			d.Enemies++
			enemies.add(m.ref.Layer)
		}
		if m.ref.Records {
			recorders.add(m.ref.Layer)
		}
		if m.ref.Checked {
			checked.add(m.ref.Layer)
		}
		if m.ref.Records && m.ref.Checked {
			both.add(m.ref.Layer)
		}
	}
	d.LayerOnly = every.most() < 2
	d.SharedKey = enemies.most() >= 2
	for l := range stagedata.NumLayers {
		r, c := recorders.on(l), checked.on(l)
		// A pair needs two entries: not just one that both records and
		// is checked.
		if r == 0 || c == 0 || (r == 1 && c == 1 && both.on(l) == 1) {
			continue
		}
		d.Collides = true
		d.Why = collision(ms, l)
		break
	}
	return d
}

// collision names a recorder and another dead-checked member that layer
// l both creates; duplicate has found that there are such.
func collision(ms []member, l int) string {
	for i, r := range ms {
		if !r.ref.Records || !created(r.ref.Layer, l) {
			continue
		}
		for j, c := range ms {
			if j == i || !c.ref.Checked || !created(c.ref.Layer, l) {
				continue
			}
			may := "a recorded death of"
			if deathOf(r.ref.Proc) == DeathUnknown {
				may = "a death of" // perhaps recorded
			}
			on := fmt.Sprintf("on layer %d", l)
			if r.ref.Layer == stagedata.AllLayers && c.ref.Layer == stagedata.AllLayers {
				on = "on every layer"
			}
			return fmt.Sprintf("%s %v (death %s) keeps %v from being created, %s", may, r.ref, deathOf(r.ref.Proc), c.ref, on)
		}
	}
	return ""
}

func deathOf(p stagedata.Proc) Death {
	c, _ := ClassOf(p)
	return c.Death
}

// markShared marks the enemy members of a group that share a layer with
// another enemy member.
func markShared(es []Entry, ms []member) {
	var enemies layerCounts
	for _, m := range ms {
		if m.enemy >= 0 {
			enemies.add(m.ref.Layer)
		}
	}
	for _, m := range ms {
		if m.enemy < 0 {
			continue
		}
		others := enemies.most() - 1 // an every-layer member meets the fullest layer
		if l := m.ref.Layer; l >= 0 && l < stagedata.NumLayers {
			others = enemies.on(l) - 1
		}
		if others > 0 && es[m.enemy].Key == Keyed {
			es[m.enemy].Shared = true
		}
	}
}

// summarize counts es overall, for the K1 classes, by class and by stage
// name.
func summarize(es []Entry) Summary {
	var s Summary
	byClass := make(map[string]*Share)
	byStage := make(map[string]*Share)
	var stageOrder []string
	for _, e := range es {
		s.All.add(e)
		if c, _ := ClassOf(e.Proc); c.Kind == K1 && !e.Boss {
			s.K1.add(e)
		}
		cs := byClass[e.Class]
		if cs == nil {
			cs = new(Share)
			byClass[e.Class] = cs
		}
		cs.add(e)
		ss := byStage[e.Stage]
		if ss == nil {
			ss = new(Share)
			byStage[e.Stage] = ss
			stageOrder = append(stageOrder, e.Stage)
		}
		ss.add(e)
	}
	for _, c := range Classes {
		if cs := byClass[c.Name]; cs != nil {
			s.ByClass = append(s.ByClass, ClassShare{c.Name, *cs})
		}
	}
	for _, n := range stageOrder {
		s.ByStage = append(s.ByStage, StageShare{n, *byStage[n]})
	}
	return s
}

func roomName(room int) string {
	if room < 0 {
		return "stage"
	}
	return "r" + strconv.Itoa(room)
}
