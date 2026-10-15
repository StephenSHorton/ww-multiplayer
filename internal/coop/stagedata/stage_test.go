package stagedata

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// *inject.Disc is what Load reads in production.
var _ FileSource = (*inject.Disc)(nil)

// act is a one-actor chunk, its setID doubling as an identity.
func act(tag, name string, setID uint16) stagetest.Chunk {
	return stagetest.Actors(tag, stagetest.Actor{Name: name, SetID: setID, Pos: [3]float32{float32(setID), 0, 0}})
}

// sampleDisc is a disc with two stages and the files Load must skip:
//
//   - M_NewD2 (Yaz0, as the disc stores it): a dungeon with the key HUD on
//     table 3; a stage actor; rooms 0, 1 and 10, and room 2 with no
//     room.dzr; room 1 has a layer-2 chunk.
//   - sea (plain): table 0; rooms 1 and 44, and room 7 spelled "room7.ARC".
//
// Skipped: LODALL.arc, Room01.arc, Room64.arc, a nested Room3.arc, a
// directory with no Stage.arc, a directory name too long to load, a
// Stage.arc right under res/Stage, files outside it, and "SEA/Room5.arc",
// which the "sea" directory added first shadows.
func sampleDisc() *stagetest.Disc {
	d := new(stagetest.Disc)
	d.Add("opening.bnr", []byte("banner"))
	d.AddStage(stagetest.Stage{
		Name: "M_NewD2", Yaz0: true,
		DZS: stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(3, int(StageDungeon), true)),
			act("ACTR", "Stage1", 900)),
		Rooms: []stagetest.Room{
			{No: 0, DZR: stagetest.DZx(act("ACTR", "Bk", 1), act("DOOR", "door12", 2))},
			{No: 1, DZR: stagetest.DZx(act("ACTR", "Stal", 3), act(stagetest.LayerTag("ACT", 2), "ki", 4))},
			{No: 2, Extra: []stagetest.File{{Path: "bdl/model.bdl", Data: []byte("model")}}},
			{No: 10, DZR: stagetest.DZx(act("TRES", "takara", 5))},
		},
		Extra: []stagetest.File{{Path: "dat/event_list.dat", Data: []byte("events")}},
	})
	sea := stagetest.Stage{
		Name: "sea",
		DZS:  stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(0, int(StageSea), false))),
		Rooms: []stagetest.Room{
			{No: 44, DZR: stagetest.DZx(act("ACTR", "kani", 44))},
			{No: 1, DZR: stagetest.DZx(act("SCOB", "Kbota_A", 11))},
		},
	}
	d.AddStage(sea)
	d.Add("res/Stage/sea/room7.ARC", sea.RoomArc(stagetest.Room{No: 7, DZR: stagetest.DZx(act("ACTR", "Bk", 7))}))
	for _, p := range []string{
		"res/Stage/sea/LODALL.arc", "res/Stage/sea/Room01.arc", "res/Stage/sea/Room64.arc",
		"res/Stage/sea/sub/Room3.arc", "res/Stage/Empty/Room0.arc", "res/Stage/TooLongX/Stage.arc",
		"res/Stage/Stage.arc", "res/Object/Link.arc", "res/Stage/SEA/Room5.arc",
	} {
		d.Add(p, []byte("not an archive"))
	}
	return d
}

func TestLoad(t *testing.T) {
	stages, _, err := Load(sampleDisc())
	if err != nil {
		t.Fatal(err)
	}
	type room struct {
		no     int
		path   string
		hasDZR bool
		names  []string
	}
	type stage struct {
		name, path string
		table      int
		typ        StageType
		hud        bool
		names      []string
		rooms      []room
	}
	names := func(as []Actor) []string {
		var n []string
		for _, a := range as {
			n = append(n, a.Name)
		}
		return n
	}
	var got []stage
	for _, s := range stages {
		g := stage{name: s.Name, path: s.Path, table: s.SaveTable, typ: s.StageType, hud: s.KeyHUD, names: names(s.DZS.Actors)}
		if s.SaveTable != s.Stag.SaveTable() || s.StageType != s.Stag.StageType() || s.KeyHUD != s.Stag.KeyHUD() {
			t.Errorf("%s: fields disagree with its Stag %+v", s.Name, s.Stag)
		}
		for _, r := range s.Rooms {
			g.rooms = append(g.rooms, room{r.No, r.Path, r.DZR != nil, names(r.Actors())})
		}
		got = append(got, g)
	}
	want := []stage{
		{"M_NewD2", "res/Stage/M_NewD2/Stage.arc", 3, StageDungeon, true, []string{"Stage1"}, []room{
			{0, "res/Stage/M_NewD2/Room0.arc", true, []string{"Bk", "door12"}},
			{1, "res/Stage/M_NewD2/Room1.arc", true, []string{"Stal", "ki"}},
			{2, "res/Stage/M_NewD2/Room2.arc", false, nil},
			{10, "res/Stage/M_NewD2/Room10.arc", true, []string{"takara"}},
		}},
		{"sea", "res/Stage/sea/Stage.arc", 0, StageSea, false, nil, []room{
			{1, "res/Stage/sea/Room1.arc", true, []string{"Kbota_A"}},
			{7, "res/Stage/sea/Room7.arc", true, []string{"Bk"}},
			{44, "res/Stage/sea/Room44.arc", true, []string{"kani"}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load =\n%+v\nwant\n%+v", got, want)
	}
}

// TestLoadFromISO reads the same stages out of a disc image through
// inject.Disc, the way coop-tables will, and l_objectName out of its DOL.
func TestLoadFromISO(t *testing.T) {
	src := sampleDisc()
	want, _, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	iso := src.ISO(stagetest.MainDOL(sampleNames()...))
	disc, err := inject.NewDisc(bytes.NewReader(iso), int64(len(iso)))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(disc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the image:\n%+v\nfrom the files:\n%+v", got, want)
	}
	dol, err := disc.ReadDOL()
	if err != nil {
		t.Fatal(err)
	}
	o, err := ReadObjectNames(dol)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := o.Proc("door12"); !ok || p != ProcDoor12 {
		t.Errorf("Proc(door12) = %v, %v", p, ok)
	}
}

func TestLoadStage(t *testing.T) {
	src := sampleDisc()
	s, err := LoadStage(src, "m_newd2")
	if err != nil {
		t.Fatal(err)
	}
	all, _, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, all[0]) {
		t.Errorf("LoadStage = %+v, want %+v", s, all[0])
	}
	for _, name := range []string{"Empty", "TooLongX", "nope", ""} {
		if s, err := LoadStage(src, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("LoadStage(%q) = %v, %v; want fs.ErrNotExist", name, s, err)
		}
	}
	// A stage whose Stage.arc another spelling of the directory shadows
	// isn't there for the game either.
	d := new(stagetest.Disc)
	d.Add("res/Stage/abc/Room0.arc", []byte("x"))
	d.AddStage(stagetest.Stage{Name: "ABC", DZS: stagetest.DZx(stagetest.StagChunk(stagetest.Stag{}))})
	if s, err := LoadStage(d, "abc"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("shadowed stage: LoadStage = %v, %v; want fs.ErrNotExist", s, err)
	}
}

func TestStageViews(t *testing.T) {
	stages, _, err := Load(sampleDisc())
	if err != nil {
		t.Fatal(err)
	}
	s := stages[0]
	type row struct {
		room  int
		name  string
		layer int
	}
	rows := func(ps []Placed) []row {
		var out []row
		for _, p := range ps {
			out = append(out, row{p.Room, p.Name, p.Layer})
		}
		return out
	}
	all := []row{{-1, "Stage1", -1}, {0, "Bk", -1}, {0, "door12", -1}, {1, "Stal", -1}, {1, "ki", 2}, {10, "takara", -1}}
	if got := rows(s.All()); !slices.Equal(got, all) {
		t.Errorf("All() = %+v", got)
	}
	if got, want := rows(s.OnLayer(0)), slices.Delete(slices.Clone(all), 4, 5); !slices.Equal(got, want) {
		t.Errorf("OnLayer(0) = %+v, want %+v", got, want)
	}
	if got := rows(s.OnLayer(2)); !slices.Equal(got, all) {
		t.Errorf("OnLayer(2) = %+v", got)
	}
	if got := s.Layers(); !slices.Equal(got, []int{2}) {
		t.Errorf("Layers() = %v", got)
	}
	if r, ok := s.Room(10); !ok || r.No != 10 || r.DZR == nil {
		t.Errorf("Room(10) = %+v, %v", r, ok)
	}
	if r, ok := s.Room(2); !ok || r.DZR != nil || r.Actors() != nil || r.OnLayer(0) != nil {
		t.Errorf("Room(2) = %+v, %v", r, ok)
	}
	if got := len(s.Rooms[1].OnLayer(2)); got != 2 {
		t.Errorf("room 1 OnLayer(2): %d entries", got)
	}
	if _, ok := s.Room(3); ok {
		t.Error("Room(3) found a room")
	}
}

// failingSource fails ReadFile for one path.
type failingSource struct {
	FileSource
	path string
	err  error
}

func (f failingSource) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		return nil, f.err
	}
	return f.FileSource.ReadFile(path)
}

func TestLoadErrors(t *testing.T) {
	stag := stagetest.StagChunk(stagetest.StagInfo(1, 0, false))
	disc := func(stages ...stagetest.Stage) *stagetest.Disc {
		d := new(stagetest.Disc)
		for _, s := range stages {
			d.AddStage(s)
		}
		return d
	}
	ok := stagetest.Stage{Name: "ok", DZS: stagetest.DZx(stag), Rooms: []stagetest.Room{{No: 0, DZR: stagetest.DZx()}}}
	ioErr := errors.New("device on fire")
	for _, c := range []struct {
		name, want string
		src        FileSource
	}{
		{"bad stage.dzs", "res/Stage/a/Stage.arc: dzs/stage.dzs: dzx:", disc(stagetest.Stage{Name: "a", DZS: []byte{0, 0}})},
		{"bad room.dzr", "res/Stage/a/Room3.arc: dzr/room.dzr: dzx:",
			disc(stagetest.Stage{Name: "a", DZS: stagetest.DZx(stag), Rooms: []stagetest.Room{{No: 3, DZR: []byte{0xFF}}}})},
		{"bad Stage.arc", "res/Stage/a/Stage.arc: rarc:", func() FileSource {
			d := new(stagetest.Disc)
			d.Add("res/Stage/a/Stage.arc", []byte("RARC"))
			return d
		}()},
		{"bad room archive", "res/Stage/ok/Room9.arc: rarc:", func() FileSource {
			d := disc(ok)
			d.Add("res/Stage/ok/Room9.arc", stagetest.Yaz0([]byte("not RARC inside")))
			return d
		}()},
		{"read error", "res/Stage/ok/Room0.arc: device on fire", failingSource{disc(ok), "res/Stage/ok/Room0.arc", ioErr}},
	} {
		for range 20 {
			stages, skipped, err := Load(c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%s: Load = %v, %v, %v; want an error containing %q", c.name, stages, skipped, err, c.want)
			}
			if c.name == "read error" && !errors.Is(err, ioErr) {
				t.Errorf("read error: %v doesn't wrap the source's error", err)
			}
		}
	}

	// Every failing archive is named, in listing order (the Stage.arc
	// files, then the rooms of the stages whose Stage.arc loaded), the same
	// whatever the workers' timing. A skipped stage's rooms are never read.
	bad := []stagetest.Room{{No: 0, DZR: []byte{1}}, {No: 1, DZR: []byte{1}}}
	src := disc(ok,
		stagetest.Stage{Name: "b", DZS: stagetest.DZx(stag), Rooms: bad},
		stagetest.Stage{Name: "c", DZS: []byte{1}, Rooms: bad},
		stagetest.Stage{Name: "d", Rooms: bad},
		stagetest.Stage{Name: "e", DZS: []byte{1}})
	want := []string{
		"stagedata: res/Stage/c/Stage.arc: dzs/stage.dzs: dzx:",
		"stagedata: res/Stage/e/Stage.arc: dzs/stage.dzs: dzx:",
		"stagedata: res/Stage/b/Room0.arc: dzr/room.dzr: dzx:",
		"stagedata: res/Stage/b/Room1.arc: dzr/room.dzr: dzx:",
	}
	var first string
	for i := range 20 {
		_, _, err := Load(src)
		if err == nil {
			t.Fatal("every error: Load succeeded")
		}
		lines := strings.Split(err.Error(), "\n")
		if len(lines) != len(want) {
			t.Fatalf("every error: %d lines, want %d:\n%v", len(lines), len(want), err)
		}
		for k, l := range lines {
			if !strings.HasPrefix(l, want[k]) {
				t.Fatalf("every error: line %d is %q, want it to start %q", k, l, want[k])
			}
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("every error: run %d gave\n%v\nnot\n%s", i, err, first)
		}
	}

	// Many failures: the first maxLoadErrors are named, and the rest
	// counted.
	var many []stagetest.Stage
	for i := range 40 {
		s := ok
		s.Name = fmt.Sprintf("s%02d", i)
		if i >= 3 {
			s.DZS = []byte{1}
		}
		many = append(many, s)
	}
	for range 20 {
		_, _, err := Load(disc(many...))
		if err == nil {
			t.Fatal("many stages: Load succeeded")
		}
		msg := err.Error()
		lines := strings.Split(msg, "\n")
		if len(lines) != maxLoadErrors+1 || !strings.HasPrefix(lines[0], "stagedata: res/Stage/s03/Stage.arc:") ||
			!strings.HasPrefix(lines[maxLoadErrors-1], "stagedata: res/Stage/s12/Stage.arc:") ||
			lines[maxLoadErrors] != "stagedata: and 27 more archives failed" {
			t.Fatalf("many stages:\n%s", msg)
		}
	}
}

// TestLoadSkips: a stage the play scene can't load (no stage.dzs, as the
// name scene's "Name"; or no STAG) is left out with its rooms and
// reported, in listing order, and LoadStage says why it can't have it.
func TestLoadSkips(t *testing.T) {
	stag := stagetest.StagChunk(stagetest.StagInfo(1, 0, false))
	ok := stagetest.Stage{Name: "ok", DZS: stagetest.DZx(stag), Rooms: []stagetest.Room{{No: 0, DZR: stagetest.DZx()}}}
	ok2 := ok
	ok2.Name = "ok2"
	d := new(stagetest.Disc)
	d.AddStage(ok)
	d.AddStage(stagetest.Stage{Name: "Name", Rooms: []stagetest.Room{{No: 0, DZR: []byte{0xFF}}}})
	d.AddStage(stagetest.Stage{Name: "nostag", DZS: stagetest.DZx(), Rooms: []stagetest.Room{{No: 2, DZR: stagetest.DZx()}}})
	d.AddStage(ok2)
	// Reading a skipped stage's room fails the load.
	src := failingSource{failingSource{d, "res/Stage/Name/Room0.arc", errors.New("read a skipped stage's room")},
		"res/Stage/nostag/Room2.arc", errors.New("read a skipped stage's room")}
	for range 20 {
		stages, skipped, err := Load(src)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, s := range stages {
			names = append(names, s.Name)
			if len(s.Rooms) != 1 {
				t.Errorf("%s has %d rooms, want 1", s.Name, len(s.Rooms))
			}
		}
		if !slices.Equal(names, []string{"ok", "ok2"}) {
			t.Errorf("stages %v, want [ok ok2]", names)
		}
		if want := []Skipped{{"Name", SkipNoDZS}, {"nostag", SkipNoStag}}; !slices.Equal(skipped, want) {
			t.Errorf("skipped %v, want %v", skipped, want)
		}
	}
	for name, why := range map[string]string{"name": SkipNoDZS, "NOSTAG": SkipNoStag} {
		s, err := LoadStage(src, name)
		if err == nil || errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), why+", so the play scene can't load it") {
			t.Errorf("LoadStage(%q) = %v, %v; want the reason it is skipped", name, s, err)
		}
	}
	if s, err := LoadStage(src, "ok2"); err != nil || s.Name != "ok2" {
		t.Errorf("LoadStage(ok2) = %v, %v", s, err)
	}
	if _, skipped, err := Load(sampleDisc()); err != nil || skipped != nil {
		t.Errorf("sample disc: skipped %v, %v", skipped, err)
	}
}

// listedAs makes the disc list the file at path as size bytes long, and
// fails the test if Load reads it when that is over the cap.
type listedAs struct {
	FileSource
	t    *testing.T
	path string
	size uint32
}

func (l listedAs) Stat(path string) (inject.DiscFile, error) {
	f, err := l.FileSource.Stat(path)
	if err == nil && path == l.path {
		f.Size = l.size
	}
	return f, err
}

func (l listedAs) ReadFile(path string) ([]byte, error) {
	if path == l.path && l.size > MaxArchiveSize {
		l.t.Errorf("read %s, listed at %d bytes", path, l.size)
	}
	return l.FileSource.ReadFile(path)
}

// An archive listed larger than MaxArchiveSize is refused before it is
// read: ReadFile would allocate whatever the disc says.
func TestLoadRefusesHugeArchives(t *testing.T) {
	ok := stagetest.Stage{Name: "ok", DZS: stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(1, 0, false))),
		Rooms: []stagetest.Room{{No: 0, DZR: stagetest.DZx()}}}
	d := new(stagetest.Disc)
	d.AddStage(ok)
	for _, path := range []string{"res/Stage/ok/Stage.arc", "res/Stage/ok/Room0.arc"} {
		for _, size := range []uint32{MaxArchiveSize + 1, 0xFFFFFFFF} {
			_, _, err := Load(listedAs{d, t, path, size})
			if want := fmt.Sprintf("%s: the disc lists %d bytes, more than the %d-byte cap", path, size, MaxArchiveSize); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s listed at %d bytes: err = %v, want %q", path, size, err, want)
			}
		}
		if _, _, err := Load(listedAs{d, t, path, MaxArchiveSize}); err != nil {
			t.Errorf("%s listed at the cap: %v", path, err)
		}
	}
}

func TestRoomNumber(t *testing.T) {
	for file, want := range map[string]int{
		"Room0.arc": 0, "Room9.arc": 9, "Room10.arc": 10, "Room63.arc": 63, "room12.ARC": 12, "ROOM1.Arc": 1,
		"Room64.arc": -1, "Room01.arc": -1, "Room00.arc": -1, "Room.arc": -1, "Room-1.arc": -1, "Room+1.arc": -1,
		"Roomx.arc": -1, "Room100.arc": -1, "Room1.arc.bak": -1, "Stage.arc": -1, "Room1": -1, "xRoom1.arc": -1,
	} {
		n, ok := roomNumber(file)
		if !ok {
			n = -1
		}
		if n != want {
			t.Errorf("roomNumber(%q) = %d, %v; want %d", file, n, ok, want)
		}
	}
}

func TestListStages(t *testing.T) {
	var got []string
	for _, l := range listStages(sampleDisc().Files()) {
		got = append(got, fmt.Sprintf("%s%v", l.name, l.rooms))
	}
	// The listing has SEA's room 5 under "sea"; reading it then finds
	// nothing, as the game wouldn't.
	if want := []string{"M_NewD2[0 1 2 10]", "sea[1 5 7 44]"}; !slices.Equal(got, want) {
		t.Errorf("listStages = %q, want %q", got, want)
	}
}

// BenchmarkLoad loads 16 Yaz0 stages of 8 rooms, each room archive
// carrying 1 MiB of model-like filler beside its room.dzr, and reports
// the expanded archive bytes per second: the real disc's res/Stage is on
// the order of a gigabyte expanded, and coop-tables should load it in
// seconds.
func BenchmarkLoad(b *testing.B) {
	model := make([]byte, 1<<20)
	x := uint32(1)
	for i := range model { // a xorshift stream over a small alphabet: compresses like geometry, not like zeros
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		model[i] = byte(x%7) * 37
	}
	dzr := sampleDZx()
	d := new(stagetest.Disc)
	var total int64
	for i := range 16 {
		s := stagetest.Stage{Name: fmt.Sprintf("st%02d", i), Yaz0: true,
			DZS: stagetest.DZx(stagetest.StagChunk(stagetest.StagInfo(i, 1, true)))}
		for no := range 8 {
			s.Rooms = append(s.Rooms, stagetest.Room{No: no, DZR: dzr,
				Extra: []stagetest.File{{Path: "bdl/model.bdl", Data: model}}})
			total += int64(len(model) + len(dzr))
		}
		d.AddStage(s)
	}
	b.SetBytes(total)
	for b.Loop() {
		if _, _, err := Load(d); err != nil {
			b.Fatal(err)
		}
	}
}
