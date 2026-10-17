// Package keys builds the M1 small-key table (docs/08-coop-design.md §6.4,
// §11 item 8) from the user's own stage files: per save table, every
// small-key source and every small-key door, and the invariant the
// derived key count keys(t) = |sources set| - |doors set| needs: as many
// sources as doors, the vanilla number of each, every one recorded in a
// saved bit, and key doors only in the dungeon tables 3-7.
//
// A key counts in the save table of the stage it is picked up in: the
// pickup adds to the pending count (item_func_small_key, d_item.cpp:
// 665-667) that the stage's memBit key byte takes (dSv_memBit_c::mKeyNum,
// d_save.h:688). A door spends one (keyInit, d_door.cpp:434-448) and sets
// its switch in the same table.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), as in wwapi/addr.
package keys

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
)

// Save tables (dSv_save_c::SaveStageTbl, d_save.h:888-905) the dungeons
// with small keys use.
const (
	TableDRC  = 0x3 // STAGE_DRC, d_save.h:892: Dragon Roost Cavern
	TableFW   = 0x4 // STAGE_FW, d_save.h:893: Forbidden Woods
	TableTotG = 0x5 // STAGE_TOTG, d_save.h:894: Tower of the Gods
	TableET   = 0x6 // STAGE_ET, d_save.h:895: Earth Temple
	TableWT   = 0x7 // STAGE_WT, d_save.h:896: Wind Temple
)

// ProcKddoor is the third door class with a key lock: fpcNm_KDDOOR_e,
// f_pc_name.h:318; "doorKD", "ZenshKD", "K_ZshKD", "doorSH", "doorSZ"
// (d_stage.cpp:460-464). door10 and door12 are stagedata.ProcDoor10 and
// stagedata.ProcDoor12.
const ProcKddoor stagedata.Proc = 0x132

// Dungeon is a save table that should hold small keys, with its vanilla
// key count.
type Dungeon struct {
	Table int    `json:"table"`
	Short string `json:"short"`
	Name  string `json:"name"`
	Keys  int    `json:"keys"`
}

// Dungeons are the tables with small keys and the vanilla counts (sources
// and doors alike), as data: the design's §6.4 and §11 item 8, which this
// table checks against the user's own stage files. The decomp has no such
// list.
var Dungeons = []Dungeon{
	{TableDRC, "DRC", "Dragon Roost Cavern", 4},
	{TableFW, "FW", "Forbidden Woods", 1},
	{TableTotG, "TotG", "Tower of the Gods", 2},
	{TableET, "ET", "Earth Temple", 3},
	{TableWT, "WT", "Wind Temple", 2},
}

// Class is a door class with a key lock.
type Class string

const (
	Door10 Class = "door10" // daDoor10_c
	Door12 Class = "door12" // daDoor12_c
	Kddoor Class = "kddoor" // daKddoor_c
)

// classes maps each door proc to its class and the types (dDoor_info_c::
// getType, (prm >> 8) & 0xF, d_door.cpp:26-28) whose chkMakeKey returns
// the small-key lock, the one created with keyCreate(0) (mbIsBossDoor 0,
// d_door.cpp:509-510): door10 4 and 5 (d_a_door10.cpp:13-24, created at
// :275-287), door12 1 (d_a_door12.cpp:39-49, :265-277), kddoor 2
// (d_a_kddoor.cpp:12-14, :578-579). door10 type 1 and door12 type 3 are
// boss-key doors (keyCreate(1)), which leave mKeyNum alone (d_door.cpp:
// 438-439); they are not in this table. No other class creates a key
// lock (dDoor_key2_c is used only by these three).
var classes = map[stagedata.Proc]struct {
	class Class
	types []int
}{
	stagedata.ProcDoor10: {Door10, []int{4, 5}},
	stagedata.ProcDoor12: {Door12, []int{1}},
	ProcKddoor:           {Kddoor, []int{2}},
}

// Switch ranges (dSv_info_c, d_save.h:982-984; isSwitch, d_save.cpp:
// 1586-1603).
const (
	memorySwitchEnd = 0x80 // 0x00-0x7F: the table's memory switches, saved
	danSwitchEnd    = 0xC0 // 0x80-0xBF: dungeon switches (mDan), not in the memBit
	noSwitch        = 0xFF // isSwitch is false and onSwitch does nothing (d_save.cpp:1544-1546, :1587-1589)
)

// Entry is one key-door entry of a stage file.
type Entry struct {
	itemsrc.Where
	Class     Class          `json:"class"`
	Proc      stagedata.Proc `json:"proc"`
	Type      int            `json:"type"`
	Switch    int            `json:"switch"`     // getSwbit, prm & 0xFF (d_door.cpp:16-18)
	FrontRoom int            `json:"front_room"` // getFRoomNo, angle x & 0x3F (d_door.cpp:44-46)
	BackRoom  int            `json:"back_room"`  // getBRoomNo, (angle x >> 6) & 0x3F (d_door.cpp:49-51)
}

// DecodeDoor reads p, an entry that creates proc, as a small-key door: ok
// is false for any other door type or class.
func DecodeDoor(s *stagedata.Stage, p stagedata.Placed, proc stagedata.Proc) (Entry, bool) {
	c, ok := classes[proc]
	if !ok {
		return Entry{}, false
	}
	typ := int(p.Params>>8) & 0xF
	if !slices.Contains(c.types, typ) {
		return Entry{}, false
	}
	x := uint16(p.Angle[0])
	return Entry{
		Where: itemsrc.At(s, p), Class: c.class, Proc: proc, Type: typ,
		Switch: int(p.Params & 0xFF), FrontRoom: int(x & 0x3F), BackRoom: int(x>>6) & 0x3F,
	}, true
}

// Door is one lock: the entries that share its saved switch (a door on
// several layers, say), or a single entry whose switch isn't saved.
type Door struct {
	Switch int     `json:"switch"`
	Saved  bool    `json:"saved"` // a memory switch, 0x00-0x7F
	At     []Entry `json:"at"`
}

// Table is one save table's keys and doors.
type Table struct {
	SaveTable int    `json:"save_table"`
	Dungeon   string `json:"dungeon,omitempty"` // Dungeon.Short
	Name      string `json:"name,omitempty"`
	// Expected is the vanilla key count, sources and doors alike: 0 for a
	// table that isn't one of Dungeons.
	Expected int              `json:"expected"`
	Stages   []string         `json:"stages"` // the stages on this table, disc order
	Sources  []itemsrc.Source `json:"sources"`
	Doors    []Door           `json:"doors"`
	Pass     bool             `json:"pass"`
	Problems []string         `json:"problems,omitempty"`
}

// Result is the whole key table: every table of Dungeons, and any other
// table with a small key or a key door, in table order.
type Result struct {
	Tables []Table `json:"tables"`
	Pass   bool    `json:"pass"` // every table passes
}

// Table returns save table no's entry, if the result has one.
func (r *Result) Table(no int) (*Table, bool) {
	for i := range r.Tables {
		if r.Tables[i].SaveTable == no {
			return &r.Tables[i], true
		}
	}
	return nil, false
}

// Build reads stages, as stagedata.Load returns them, with names, the
// user's l_objectName. It never fails: what it can't account for is a
// problem of the table it is in.
func Build(stages []*stagedata.Stage, names *stagedata.ObjectNames) *Result {
	type acc struct {
		stages  []string
		sources []itemsrc.Placement
		doors   []Entry
	}
	byTable := make(map[int]*acc)
	get := func(t int) *acc {
		a, ok := byTable[t]
		if !ok {
			a = new(acc)
			byTable[t] = a
		}
		return a
	}
	for _, d := range Dungeons {
		get(d.Table)
	}
	isKey := func(item uint8) bool { return item == itemsrc.ItemSmallKey }
	for _, s := range stages {
		if s == nil {
			continue
		}
		srcs := itemsrc.Find([]*stagedata.Stage{s}, names, isKey)
		var doors []Entry
		if names != nil {
			for _, p := range s.All() {
				if proc, ok := names.Proc(p.Name); ok {
					if e, ok := DecodeDoor(s, p, proc); ok {
						doors = append(doors, e)
					}
				}
			}
		}
		a, ok := byTable[s.SaveTable]
		if !ok && (len(srcs) > 0 || len(doors) > 0) {
			a, ok = get(s.SaveTable), true
		}
		if ok {
			a.sources = append(a.sources, srcs...)
			a.doors = append(a.doors, doors...)
		}
	}
	// Each table's stages, including those with no keys, once the tables
	// are known.
	for _, s := range stages {
		if s == nil {
			continue
		}
		if a, ok := byTable[s.SaveTable]; ok {
			a.stages = append(a.stages, s.Name)
		}
	}

	nums := make([]int, 0, len(byTable))
	for t := range byTable {
		nums = append(nums, t)
	}
	slices.Sort(nums)
	r := &Result{Pass: true}
	for _, t := range nums {
		a := byTable[t]
		tb := Table{SaveTable: t, Stages: a.stages, Sources: itemsrc.Group(a.sources), Doors: groupDoors(a.doors)}
		if i := slices.IndexFunc(Dungeons, func(d Dungeon) bool { return d.Table == t }); i >= 0 {
			tb.Dungeon, tb.Name, tb.Expected = Dungeons[i].Short, Dungeons[i].Name, Dungeons[i].Keys
		}
		tb.Problems = check(&tb)
		tb.Pass = len(tb.Problems) == 0
		r.Pass = r.Pass && tb.Pass
		r.Tables = append(r.Tables, tb)
	}
	return r
}

// groupDoors makes doors of entries, in the order each first appears.
func groupDoors(es []Entry) []Door {
	var out []Door
	index := make(map[int]int)
	for _, e := range es {
		saved := e.Switch < memorySwitchEnd
		if saved {
			if i, ok := index[e.Switch]; ok {
				out[i].At = append(out[i].At, e)
				continue
			}
			index[e.Switch] = len(out)
		}
		out = append(out, Door{Switch: e.Switch, Saved: saved, At: []Entry{e}})
	}
	return out
}

// check lists what keeps keys(t) from being derived for t.
func check(t *Table) []string {
	var p []string
	if t.Dungeon == "" && len(t.Doors) > 0 {
		p = append(p, fmt.Sprintf("%d key door(s) outside the dungeon tables 3-7", len(t.Doors)))
	}
	if len(t.Sources) != len(t.Doors) {
		p = append(p, fmt.Sprintf("%d small-key source(s) but %d key door(s)", len(t.Sources), len(t.Doors)))
	}
	if len(t.Sources) != t.Expected || len(t.Doors) != t.Expected {
		p = append(p, fmt.Sprintf("vanilla has %d of each; the stage files have %d source(s), %d door(s)",
			t.Expected, len(t.Sources), len(t.Doors)))
	}
	for i, s := range t.Sources {
		switch {
		case s.Flag.Halts():
			p = append(p, fmt.Sprintf("source %d: %s: the game halts on that save bit (a JUT_ASSERT), so the key can't be taken", i+1, s.Flag))
		case !s.Flag.Saved():
			p = append(p, fmt.Sprintf("source %d: %s: nothing saved records the pickup, so it can be taken again and keys(t) can't count it", i+1, s.Flag))
		case s.Flag.Kind == itemsrc.TboxFlag && s.Flag.Table != t.SaveTable:
			p = append(p, fmt.Sprintf("source %d: the chest records in save table %d, the key counts in %d", i+1, s.Flag.Table, t.SaveTable))
		}
	}
	for i, d := range t.Doors {
		switch {
		case d.Switch == noSwitch:
			p = append(p, fmt.Sprintf("door %d: no switch (0xFF): it never records being opened and locks again", i+1))
		case d.Switch >= danSwitchEnd:
			p = append(p, fmt.Sprintf("door %d: switch 0x%02X is a zone switch, which setKey reads for room -1 (the game asserts, d_save.cpp:1596)", i+1, d.Switch))
		case !d.Saved:
			p = append(p, fmt.Sprintf("door %d: switch 0x%02X is a dungeon switch; keyInit sets only switches below 0x80 (d_door.cpp:436-437), so the lock comes back", i+1, d.Switch))
		}
	}
	return p
}

// Report is the table as text: a verdict line, then each table's verdict,
// sources, doors and problems.
func (r *Result) Report() string {
	var b strings.Builder
	pass := 0
	for _, t := range r.Tables {
		if t.Pass {
			pass++
		}
	}
	fmt.Fprintf(&b, "small keys: %s (%d of %d save tables pass)\n", verdict(r.Pass), pass, len(r.Tables))
	for _, t := range r.Tables {
		name := "not a dungeon table"
		if t.Dungeon != "" {
			name = t.Dungeon + ", " + t.Name
		}
		fmt.Fprintf(&b, "\nsave table %d (%s): %s, %d source(s), %d door(s), vanilla %d\n",
			t.SaveTable, name, verdict(t.Pass), len(t.Sources), len(t.Doors), t.Expected)
		stages := strings.Join(t.Stages, " ")
		if stages == "" {
			stages = "(none)"
		}
		fmt.Fprintf(&b, "  stages: %s\n", stages)
		for i, s := range t.Sources {
			fmt.Fprintf(&b, "  source %d: %s, %s\n", i+1, s.Kind, s.Flag)
			for _, w := range s.At {
				fmt.Fprintf(&b, "    %s\n", w)
			}
		}
		for i, d := range t.Doors {
			fmt.Fprintf(&b, "  door %d: switch 0x%02X", i+1, d.Switch)
			if !d.Saved {
				b.WriteString(" (unsaved)")
			}
			b.WriteByte('\n')
			for _, e := range d.At {
				fmt.Fprintf(&b, "    %s, %s type %d, rooms %d/%d\n", e.Where, e.Class, e.Type, e.FrontRoom, e.BackRoom)
			}
		}
		for _, p := range t.Problems {
			fmt.Fprintf(&b, "  problem: %s\n", p)
		}
	}
	return b.String()
}

func verdict(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
