package enemies

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// Fuzz targets for Build, in the style of mods/multiplayer/net/fuzz_test.go:
// one over whole stage files (the bytes stage.ParseDZx reads from an
// ISO), one over one entry of every class with a shared setID, spread
// over the chunks and layers. The seed corpora are built in code (no game
// data) and run as ordinary tests; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzBuild -fuzztime=30s ./wwapi/tables/enemies/

// chunkTags are every actor chunk tag the game reads.
var chunkTags = func() []string {
	t := []string{"ACTR", "TGOB", "TRES", "TGSC", "SCOB", "DOOR", "TGDR"}
	for n := range stage.NumLayers {
		t = append(t, stagetest.LayerTag("ACT", n), stagetest.LayerTag("SCO", n), stagetest.LayerTag("TRE", n))
	}
	return t
}()

// checkTable checks what Build must hold for any input of one stage: the
// entries and their states follow from the placements, every duplicate's
// flags match a pairwise check, the shares add up, the table's size is
// proportional to the entries, and the Report and a JSON round trip work.
func checkTable(t *testing.T, tbl *Table, st *stage.Stage, names *stage.ObjectNames) {
	t.Helper()
	all := st.All()
	if tbl.Stages != 1 || tbl.Placed != len(all) || tbl.Unnamed > tbl.Placed || len(tbl.Enemies) > tbl.Placed-tbl.Unnamed {
		t.Fatalf("stages %d, placed %d of %d, unnamed %d, %d enemies", tbl.Stages, tbl.Placed, len(all), tbl.Unnamed, len(tbl.Enemies))
	}
	enemies, halting := 0, 0
	for _, p := range all {
		if proc, ok := names.Proc(p.Name); ok && IsEnemy(proc) {
			enemies++
		}
		if p.Room >= 0 && !p.Scaled && p.SetID >= MaxSetID && p.SetID != NoSetID {
			halting++
		}
	}
	if enemies != len(tbl.Enemies) || halting != tbl.Halting {
		t.Fatalf("%d enemies, %d placed; halting %d, want %d", len(tbl.Enemies), enemies, tbl.Halting, halting)
	}
	for _, e := range tbl.Enemies {
		c, ok := ClassOf(e.Proc)
		if proc, _ := names.Proc(e.Name); !ok || c.Name != e.Class || proc != e.Proc {
			t.Fatalf("%v: proc 0x%X class %v", e, uint16(e.Proc), c.Name)
		}
		if e.Key != keyState(e.Room, e.SetID) || e.Checked == stagetest.Scaled(e.Tag) || e.Boss != c.IsBoss(e.Params, e.Angle) {
			t.Fatalf("%v: key %s checked %v boss %v", e, e.Key, e.Checked, e.Boss)
		}
		if e.Shared && e.Key != Keyed {
			t.Fatalf("%v shared but %s", e, e.Key)
		}
	}

	members := 0
	shared := make(map[groupKey]bool)
	for i, d := range tbl.Duplicates {
		if i > 0 {
			p := tbl.Duplicates[i-1]
			if cmp.Or(cmp.Compare(p.Room, d.Room), cmp.Compare(p.SetID, d.SetID)) >= 0 {
				t.Fatalf("duplicates out of order at %d: %s, %s", i, dupLine(p), dupLine(d))
			}
		}
		if d.Room < 0 || d.SetID == NoSetID || d.Stage != st.Name {
			t.Fatalf("duplicate of a key the game ignores: %s", dupLine(d))
		}
		checkDuplicate(t, tbl, d)
		members += len(d.Entries)
		if d.SharedKey {
			shared[groupKey{0, d.Room, d.SetID}] = true
		}
	}
	if members > tbl.Placed-tbl.Unnamed {
		t.Fatalf("%d duplicate members of %d named entries", members, tbl.Placed-tbl.Unnamed)
	}
	for _, e := range tbl.Enemies {
		if e.Shared && !shared[groupKey{0, e.Room, e.SetID}] {
			t.Fatalf("%v shared, but no duplicate of its key is", e)
		}
	}

	s := tbl.Summary
	sum := func(sh Share) bool {
		return sh.WithSetID == sh.StageFile+sh.OutOfRange+sh.Keyed && sh.Unchecked <= sh.Keyed && sh.Shared <= sh.Keyed &&
			sh.WithSetID <= sh.Entries
	}
	byClass, byStage := 0, 0
	for _, c := range s.ByClass {
		byClass += c.Entries
		if !sum(c.Share) {
			t.Fatalf("class share %+v", c)
		}
	}
	for _, st := range s.ByStage {
		byStage += st.Entries
	}
	if s.All.Entries != len(tbl.Enemies) || !sum(s.All) || !sum(s.K1) || s.K1.Entries > s.All.Entries ||
		byClass != s.All.Entries || (len(tbl.Enemies) > 0 && byStage != s.All.Entries) {
		t.Fatalf("summary %+v", s)
	}

	checkKills(t, tbl)

	if r := tbl.Report(); !strings.HasPrefix(r, "Enemy table: ") {
		t.Fatalf("report %q", r)
	}
	b1, err := json.Marshal(tbl)
	if err != nil {
		t.Fatal(err)
	}
	var back Table
	if err := json.Unmarshal(b1, &back); err != nil {
		t.Fatal(err)
	}
	if b2, _ := json.Marshal(&back); !bytes.Equal(b1, b2) {
		t.Fatalf("JSON round trip differs:\n%s\n%s", b1, b2)
	}
}

func FuzzBuild(f *testing.F) {
	for _, s := range fixture() {
		f.Add(s.DZS)
		for _, r := range s.Rooms {
			f.Add(r.DZR)
		}
	}
	// One setID on every layer, in every kind of chunk.
	var chunks []stagetest.Chunk
	for i, tag := range chunkTags {
		name := []string{"Bk", "takara", "pow", "Bb", "Stal"}[i%5]
		chunks = append(chunks, stagetest.Actors(tag, stagetest.Actor{Name: name, SetID: 3, Angle: [3]int16{0, 0, int16(i)}}))
	}
	f.Add(stagetest.DZx(chunks...))
	f.Add(stagetest.DZx())
	f.Add([]byte(nil))
	names := testNames(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := stage.ParseDZx(b)
		if err != nil {
			return
		}
		st := &stage.Stage{Name: "F", DZS: d, Rooms: []stage.Room{{No: 0, DZR: d}, {No: 63, DZR: d}}}
		checkTable(t, Build([]*stage.Stage{st}, names), st, names)
	})
}

// FuzzEntry places one entry of every class and a chest, all with the
// same params, angle and setID, each in a chunk picked from shift, in one
// room (or the stage's own file for a room outside 0-63).
func FuzzEntry(f *testing.F) {
	for _, s := range []struct {
		params     uint32
		ax, ay, az int16
		setID      uint16
		shift      uint8
		room       int8
	}{
		{0, 0, 0, 0, 0, 0, 0},
		{0xA, 0, 0, 5, 1, 1, 1},
		{0xFFFFFFFF, -1, -1, -1, 0xFFFF, 2, 63},
		{0x0000000D, 0x1234, 0, 0x7F, 0x1FF, 3, -1},
		{0x80FF010E, 0, 0, 0x80, 0x200, 200, 5},
	} {
		f.Add(s.params, s.ax, s.ay, s.az, s.setID, s.shift, s.room)
	}
	names := testNames(f)
	f.Fuzz(func(t *testing.T, params uint32, ax, ay, az int16, setID uint16, shift uint8, room int8) {
		var as []stage.Actor
		for i, c := range append(slices.Clone(Classes), Class{Name: "takara"}) {
			name := c.Name
			if c.Proc != 0 {
				name = synthName(c.Proc)
			}
			a := at(chunkTags[(int(shift)+i*int(shift|1))%len(chunkTags)], name, setID)
			a.Params, a.Angle = params, [3]int16{ax, ay, az}
			as = append(as, a)
		}
		st := &stage.Stage{Name: "F"}
		if room >= 0 && int(room) < stage.MaxRooms {
			st.Rooms = []stage.Room{{No: int(room), DZR: &stage.DZx{Actors: index(as)}}}
		} else {
			st.DZS = &stage.DZx{Actors: index(as)}
		}
		tbl := Build([]*stage.Stage{st}, names)
		if len(tbl.Enemies) != len(Classes) || tbl.Unnamed != 0 {
			t.Fatalf("%d enemies of %d classes, %d unnamed", len(tbl.Enemies), len(Classes), tbl.Unnamed)
		}
		if st.DZS == nil && setID != NoSetID && len(tbl.Duplicates) != 1 {
			t.Fatalf("%d duplicates of one key", len(tbl.Duplicates))
		}
		checkTable(t, tbl, st, names)
	})
}

// checkKills checks M6's kill index against the entries: every bit of
// every room's KillMask is a Keyed K1 entry's, not BOSS, Morth or Shared,
// with no key a BOSS, Morth or Shared entry has; and the coverage adds up.
func checkKills(t *testing.T, tbl *Table) {
	t.Helper()
	ok := map[killKey]bool{}
	refused := map[killKey]bool{}
	rooms := map[killRoom]bool{}
	for _, e := range tbl.Enemies {
		c, _ := ClassOf(e.Proc)
		if c.Kind != K1 || e.Key != Keyed {
			continue
		}
		k := killKey{killRoom{e.Stage, e.Room}, e.SetID}
		rooms[k.killRoom] = true
		if e.Boss || e.Proc == stage.ProcKS || e.Shared {
			refused[k] = true
		} else {
			ok[k] = true
		}
	}
	for r := range rooms {
		m := tbl.KillMask(r.stage, r.room)
		for id := range MaxSetID {
			k := killKey{r, uint16(id)}
			if want := ok[k] && !refused[k]; m.Has(id) != want || tbl.KeyedK1(r.stage, r.room, id) != want {
				t.Fatalf("%s r%d setID 0x%X: in the kill mask %v, want %v", r.stage, r.room, id, m.Has(id), want)
			}
		}
	}
	if m := tbl.KillMask("F", -1); m != (KillSet{}) || tbl.KeyedK1("F", 64, 0) {
		t.Fatal("a room outside 0..63 has kills")
	}
	c := tbl.Coverage()
	byClass := 0
	for _, s := range c.ByClass {
		byClass += s.Entries
		if s.Synced > s.Keyed || s.Keyed > s.Entries {
			t.Fatalf("class coverage %+v", s)
		}
	}
	if c.Entries != c.Keyed+c.Unkeyed || c.Keyed != c.Synced()+c.NeverLive+c.Shared || c.Entries != tbl.Summary.K1.Entries ||
		c.Keyed != tbl.Summary.K1.Keyed || c.Rooms != c.AllKeyed+c.Mixed+c.NoneKeyed || byClass != c.Entries {
		t.Fatalf("coverage %+v against the summary's K1 %+v", c, tbl.Summary.K1)
	}
	if l := c.Line(); !strings.HasPrefix(l, "K1 classes not BOSS: ") {
		t.Fatalf("coverage line %q", l)
	}
}
