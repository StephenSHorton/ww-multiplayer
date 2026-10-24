package stage

import (
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// sampleDZx is a DZR/DZS-shaped file with a STAG, every every-layer actor
// chunk, three layers' chunks, a chunk the placement code doesn't read, a
// second ACTR (which the game never reads) and a lowercase "actr" (which
// it doesn't match). The file order differs from the creation order.
func sampleDZx() []byte {
	a := func(name string, setID uint16) stagetest.Actor {
		return stagetest.Actor{Name: name, Params: 0xFF00 | uint32(setID), Pos: [3]float32{float32(setID), -1, 2.5},
			Angle: [3]int16{1, -2, 3}, SetID: setID, Scale: [3]uint8{10, 20, 30}}
	}
	stag := stagetest.StagInfo(5, int(StageDungeon), true)
	stag.Near, stag.Far, stag.CameraType, stag.ParticleScene = 10, 50000, 3, 0x1234
	stag.SchbitAndFar, stag.Rest = 0xAABBCCDD, [3]uint32{1, 2, 3}
	return stagetest.DZx(
		stagetest.Actors("DOOR", a("door12", 7)),
		stagetest.Chunk{Tag: "RTBL", Count: 3, Data: []byte{1, 2, 3, 4}},
		stagetest.Actors("ACTR", a("Bk", 1), a("Stal", 2)),
		stagetest.StagChunk(stag),
		stagetest.Actors(stagetest.LayerTag("SCO", 3), a("Kbota_A", 30)),
		stagetest.Actors("TGOB", a("ALLdie", 3)),
		stagetest.Actors("TRES", a("takara", 4)),
		stagetest.Actors("TGSC", a("SW_C00", 5)),
		stagetest.Actors("SCOB", a("osiBLK0", 6)),
		stagetest.Actors("TGDR", a("keyS12", 8)),
		stagetest.Actors(stagetest.LayerTag("ACT", 3), a("Bk", 31)),
		stagetest.Actors(stagetest.LayerTag("TRE", 3), a("takara2", 32)),
		stagetest.Actors(stagetest.LayerTag("ACT", 11), a("ki", 111)),
		stagetest.Actors("ACTR", a("never", 99)),
		stagetest.Actors("actr", a("never", 98)),
	)
}

func TestParseDZx(t *testing.T) {
	d, err := ParseDZx(sampleDZx())
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, c := range d.Chunks {
		tags = append(tags, c.Tag)
	}
	if want := []string{"DOOR", "RTBL", "ACTR", "STAG", "SCO3", "TGOB", "TRES", "TGSC", "SCOB", "TGDR",
		"ACT3", "TRE3", "ACTb", "ACTR", "actr"}; !slices.Equal(tags, want) {
		t.Errorf("chunk tags %q, want %q", tags, want)
	}
	if c := d.Chunks[1]; c.Count != 3 || c.Offset == 0 {
		t.Errorf("RTBL header = %+v", c)
	}

	type row struct {
		tag   string
		layer int
		index int
		name  string
		setID uint16
	}
	want := []row{
		{"ACTR", -1, 0, "Bk", 1}, {"ACTR", -1, 1, "Stal", 2}, {"TGOB", -1, 0, "ALLdie", 3},
		{"TRES", -1, 0, "takara", 4}, {"TGSC", -1, 0, "SW_C00", 5}, {"SCOB", -1, 0, "osiBLK0", 6},
		{"DOOR", -1, 0, "door12", 7}, {"TGDR", -1, 0, "keyS12", 8},
		{"ACT3", 3, 0, "Bk", 31}, {"SCO3", 3, 0, "Kbota_A", 30}, {"TRE3", 3, 0, "takara2", 32},
		{"ACTb", 11, 0, "ki", 111},
	}
	var got []row
	for _, a := range d.Actors {
		got = append(got, row{a.Tag, a.Layer, a.Index, a.Name, a.SetID})
		scaled := stagetest.Scaled(a.Tag)
		if a.Scaled != scaled || a.DeadChecked() == scaled {
			t.Errorf("%s %q: Scaled %v, DeadChecked %v", a.Tag, a.Name, a.Scaled, a.DeadChecked())
		}
		wantScale := [3]uint8{}
		if scaled {
			wantScale = [3]uint8{10, 20, 30}
		}
		if a.Params != 0xFF00|uint32(a.SetID) || a.Pos != [3]float32{float32(a.SetID), -1, 2.5} ||
			a.Angle != [3]int16{1, -2, 3} || a.Scale != wantScale {
			t.Errorf("%s %q: %+v", a.Tag, a.Name, a)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("actors\n%+v\nwant\n%+v", got, want)
	}

	s := d.Stag
	if s == nil {
		t.Fatal("no STAG")
	}
	if s.SaveTable() != 5 || !s.KeyHUD() || s.StageType() != StageDungeon || s.Near != 10 || s.Far != 50000 ||
		s.CameraType != 3 || s.ParticleScene != 0x1234 || s.SchbitAndFar != 0xAABBCCDD || s.Rest != [3]uint32{1, 2, 3} {
		t.Errorf("STAG = %+v", *s)
	}
	if c, ok := d.Chunk("ACTR"); !ok || c != d.Chunks[2] {
		t.Errorf("Chunk(ACTR) = %+v, %v; want the first", c, ok)
	}
	if _, ok := d.Chunk("PLYR"); ok {
		t.Error("Chunk(PLYR) found a chunk")
	}
}

// TestParseDZxEntryLayout checks the field offsets against bytes laid out
// by hand, not by stagetest: an 0x20-byte ACTR entry (setID at +0x1E) and
// an 0x24-byte SCOB entry (scale at +0x20).
func TestParseDZxEntryLayout(t *testing.T) {
	b := make([]byte, 0x28+0x20+0x24+0x20) // count, 3 headers, the entries
	be := binary.BigEndian
	be.PutUint32(b, 3)
	copy(b[0x04:], "ACTR")
	be.PutUint32(b[0x08:], 1)
	be.PutUint32(b[0x0C:], 0x28)
	copy(b[0x10:], "SCOB")
	be.PutUint32(b[0x14:], 1)
	be.PutUint32(b[0x18:], 0x48)
	copy(b[0x1C:], "STAG")
	be.PutUint32(b[0x20:], 1)
	be.PutUint32(b[0x24:], 0x6C)

	e := b[0x28:]
	copy(e, "Kbota_A\x00")
	be.PutUint32(e[0x08:], 0x12345678)
	be.PutUint32(e[0x0C:], math.Float32bits(1.5))
	be.PutUint32(e[0x10:], math.Float32bits(-2))
	be.PutUint32(e[0x14:], math.Float32bits(3))
	be.PutUint16(e[0x18:], 0x0102)
	be.PutUint16(e[0x1A:], 0xFFFF)
	be.PutUint16(e[0x1C:], 0x8000)
	be.PutUint16(e[0x1E:], 0x00AB)

	e = b[0x48:]
	copy(e, "door12AB") // all eight bytes: no NUL
	be.PutUint32(e[0x08:], 0xCAFEF00D)
	be.PutUint16(e[0x1E:], 0xFFFF)
	copy(e[0x20:], []byte{10, 20, 30, 0xEE})

	e = b[0x6C:]
	be.PutUint32(e[0x00:], math.Float32bits(100))
	be.PutUint32(e[0x04:], math.Float32bits(200000))
	e[0x08], e[0x09] = 7, 0x0D // save table 6, key HUD
	be.PutUint16(e[0x0A:], 0x0042)
	be.PutUint32(e[0x0C:], 0x00030C00) // boss, time 0x0C
	be.PutUint32(e[0x10:], 0x00FF1234)

	d, err := ParseDZx(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []Actor{
		{Tag: "ACTR", Layer: -1, Name: "Kbota_A", Params: 0x12345678, Pos: [3]float32{1.5, -2, 3},
			Angle: [3]int16{0x0102, -1, -0x8000}, SetID: 0xAB},
		{Tag: "SCOB", Layer: -1, Name: "door12AB", Params: 0xCAFEF00D, SetID: 0xFFFF,
			Scale: [3]uint8{10, 20, 30}, Scaled: true},
	}
	if !slices.Equal(d.Actors, want) {
		t.Errorf("actors\n%+v\nwant\n%+v", d.Actors, want)
	}
	if s := d.Stag; s == nil || s.Near != 100 || s.Far != 200000 || s.CameraType != 7 || s.SaveTable() != 6 ||
		!s.KeyHUD() || s.ParticleScene != 0x42 || s.StageType() != StageBoss || s.SchbitAndFar != 0x00FF1234 {
		t.Errorf("STAG = %+v", s)
	}
}

func TestStag(t *testing.T) {
	for _, c := range []struct {
		prop  uint8
		typ   uint32
		table int
		hud   bool
		st    StageType
	}{
		{0x00, 0x00000000, 0, false, StageOutdoors},
		{0x01, 0x00010000, 0, true, StageDungeon},
		{0x06, 0x00020000, 3, false, StageMisc},
		{0xFF, 0x00070000, 127, true, StageSea},
		{0x0B, 0xFFFDFFFF, 5, true, StageFF1}, // only bits 16-18 count
		{0x0E, 0x00080000, 7, false, StageOutdoors},
	} {
		s := Stag{Prop: c.prop, TypeAndSchbit: c.typ}
		if s.SaveTable() != c.table || s.KeyHUD() != c.hud || s.StageType() != c.st {
			t.Errorf("prop 0x%02X type 0x%08X: table %d, HUD %v, type %v; want %d, %v, %v",
				c.prop, c.typ, s.SaveTable(), s.KeyHUD(), s.StageType(), c.table, c.hud, c.st)
		}
	}
	names := []string{"outdoors", "dungeon", "misc", "boss", "subdungeon", "FF1", "miniboss", "sea", "StageType(8)"}
	for i, want := range names {
		if got := StageType(i).String(); got != want {
			t.Errorf("StageType(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestDZxLayers(t *testing.T) {
	d, err := ParseDZx(sampleDZx())
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Layers(); !slices.Equal(got, []int{3, 11}) {
		t.Errorf("Layers() = %v", got)
	}
	count := func(as []Actor) (all, own int) {
		for _, a := range as {
			if a.Layer == AllLayers {
				all++
			} else {
				own++
			}
		}
		return
	}
	for layer, wantOwn := range map[int]int{0: 0, 3: 3, 11: 1} {
		as := d.OnLayer(layer)
		all, own := count(as)
		if all != 8 || own != wantOwn {
			t.Errorf("OnLayer(%d): %d every-layer, %d own; want 8, %d", layer, all, own, wantOwn)
		}
		for _, a := range as {
			if !a.OnLayer(layer) {
				t.Errorf("OnLayer(%d) returned %+v", layer, a)
			}
		}
	}
	empty, _ := ParseDZx(stagetest.DZx())
	if len(empty.Chunks) != 0 || empty.Actors != nil || empty.Stag != nil || empty.Layers() != nil {
		t.Errorf("empty file parsed to %+v", empty)
	}
}

// TestParseDZxReadsWhatTheGameReads: chunks the placement code doesn't
// read, and second chunks of a tag, aren't checked; zero-count chunks may
// point anywhere.
func TestParseDZxReadsWhatTheGameReads(t *testing.T) {
	b := stagetest.DZx(
		stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"}),
		stagetest.Chunk{Tag: "SOND", Count: -5},
		stagetest.Chunk{Tag: "RCAM", Count: 1000},
		stagetest.Chunk{Tag: "ACTR", Count: 1000},
		stagetest.Chunk{Tag: "TGOB", Count: 0},
		stagetest.Chunk{Tag: "STAG"}, // no entry yet: patched below
	)
	be := binary.BigEndian
	be.PutUint32(b[4+2*0xC+8:], 0xFFFFFFF0) // RCAM offset past the end
	be.PutUint32(b[4+4*0xC+8:], 0)          // TGOB: zero count at offset 0
	be.PutUint32(b[4+5*0xC+8:], 4)          // STAG over the chunk table, in bounds
	d, err := ParseDZx(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Actors) != 1 || d.Actors[0].Name != "Bk" || len(d.Chunks) != 6 || d.Stag == nil {
		t.Errorf("parsed %+v", d)
	}
}

// TestParseDZSEvents: stage.dzs's first EVNT chunk's entries are the
// event list, each with its name from +0x01 and its spawn switch; no
// chunk is none, and a chunk running past the end is refused. A room's
// EVNT, which the game never reads, is listed but neither read nor
// checked.
func TestParseDZSEvents(t *testing.T) {
	d, err := ParseDZS(stagetest.DZx(
		stagetest.Events(stagetest.Event{Name: "kugutu_ganon", SpawnSwitch: 0x1E}, stagetest.Event{Name: "fifteen_chars_x", SpawnSwitch: 0xFF}),
		stagetest.Events(stagetest.Event{Name: "never", SpawnSwitch: 1}), // never read
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{{Index: 0, Name: "kugutu_ganon", SpawnSwitch: 0x1E}, {Index: 1, Name: "fifteen_chars_x", SpawnSwitch: 0xFF}}
	if !slices.Equal(d.Events, want) || len(d.Actors) != 0 {
		t.Errorf("events %+v, actors %+v; want %+v", d.Events, d.Actors, want)
	}
	none, err := ParseDZS(stagetest.DZx(stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"})))
	if err != nil || none.Events != nil {
		t.Errorf("no EVNT: %+v, %v", none, err)
	}
	bad := stagetest.DZx(stagetest.Actors("PLYR", stagetest.Actor{Name: "Link"}), stagetest.Chunk{Tag: "EVNT", Count: 2, Data: make([]byte, 0x18)})
	if _, err := ParseDZS(bad); err == nil || !strings.Contains(err.Error(), "EVNT") {
		t.Errorf("an EVNT chunk past the end: %v", err)
	}
	room, err := ParseDZx(bad)
	if err != nil || room.Events != nil || len(room.Players) != 1 || len(room.Chunks) != 2 {
		t.Errorf("a room's EVNT past the end: %+v, %v", room, err)
	}
	if room, err := ParseDZx(stagetest.DZx(stagetest.Events(stagetest.Event{Name: "x"}))); err != nil || room.Events != nil {
		t.Errorf("a room's EVNT: %+v, %v", room, err)
	}
}

// TestParseDZxPlayers: the first PLYR chunk's entries are the player
// starts, kept out of Actors; a start's point is the low byte of its
// angle z; an empty chunk is a list (the game asserts on it) and no chunk
// is none; a PLYR chunk running past the end is refused.
func TestParseDZxPlayers(t *testing.T) {
	plyr := func(point uint16, params uint32) stagetest.Actor {
		return stagetest.Actor{Name: "Link", Params: params, Pos: [3]float32{1, 2, 3}, Angle: [3]int16{0, 0x4000, int16(point)}, SetID: 0xFFFF}
	}
	d, err := ParseDZx(stagetest.DZx(
		stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"}),
		stagetest.Actors("PLYR", plyr(0xFF07, 0x1004), plyr(0x0012, 0x7004)),
		stagetest.Actors("PLYR", plyr(9, 0)), // never read
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Actors) != 1 || !d.HasPlayers() || len(d.Players) != 2 {
		t.Fatalf("actors %+v, players %+v", d.Actors, d.Players)
	}
	if p := d.Players[0]; p.StartPoint() != 7 || p.Tag != "PLYR" || p.Name != "Link" || p.Params != 0x1004 || p.Pos != [3]float32{1, 2, 3} || p.Index != 0 {
		t.Errorf("first start %+v, point %d", p, p.StartPoint())
	}
	if p := d.Players[1]; p.StartPoint() != 0x12 || p.Index != 1 {
		t.Errorf("second start %+v, point %d", p, p.StartPoint())
	}

	empty, err := ParseDZx(stagetest.DZx(stagetest.Chunk{Tag: "PLYR"}))
	if err != nil || !empty.HasPlayers() || len(empty.Players) != 0 {
		t.Errorf("empty PLYR: %+v, %v", empty, err)
	}
	none, err := ParseDZx(stagetest.DZx(stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"})))
	if err != nil || none.HasPlayers() {
		t.Errorf("no PLYR: %+v, %v", none, err)
	}
	if _, err := ParseDZx(stagetest.DZx(stagetest.Chunk{Tag: "PLYR", Count: 2, Data: make([]byte, 0x20)})); err == nil || !strings.Contains(err.Error(), "PLYR") {
		t.Errorf("a PLYR chunk past the end: %v", err)
	}
}

func TestParseDZxRejects(t *testing.T) {
	be := binary.BigEndian
	one := stagetest.DZx(stagetest.Actors("ACTR", stagetest.Actor{Name: "Bk"}))
	patch := func(b []byte, off int, v uint32) []byte {
		b = slices.Clone(b)
		be.PutUint32(b[off:], v)
		return b
	}
	// Two chunks over the same entries: each fits, together they claim
	// twice the bytes the file has.
	overlap := func() []byte {
		b := stagetest.DZx(
			stagetest.Actors("ACTR", make([]stagetest.Actor, 8)...),
			stagetest.Chunk{Tag: "ACT0", Count: 8},
		)
		return patch(b, 4+0xC+8, be.Uint32(b[4+8:]))
	}()
	for _, c := range []struct {
		name, want string
		b          []byte
	}{
		{"short header", "header truncated", []byte{0, 0, 1}},
		{"negative chunk count", "negative chunk count", patch(one, 0, 0xFFFFFFFF)},
		{"chunk table past the end", "chunk headers run past", patch(one, 0, 4)},
		{"entries past the end", "run past the end", patch(one, 8, 2)},
		{"huge count", "run past the end", patch(one, 8, 0x7FFFFFFF)},
		{"negative entry count", "negative entry count", patch(one, 8, 0xFFFFFFFF)},
		{"entries at offset 0", "at offset 0", patch(one, 12, 0)},
		{"offset past the end", "run past the end", patch(one, 12, 0xFFFFFFF0)},
		{"STAG past the end", "STAG entry", stagetest.DZx(stagetest.Chunk{Tag: "STAG", Count: 1, Data: make([]byte, 0x1C)})},
		{"STAG at offset 0", "STAG entry", patch(stagetest.DZx(stagetest.StagChunk(stagetest.Stag{})), 12, 0)},
		{"scaled entries past the end", "0x24 bytes", stagetest.DZx(stagetest.Chunk{Tag: "DOOR", Count: 1, Data: make([]byte, 0x20)})},
		{"overlapping chunks", "overlap", overlap},
	} {
		d, err := ParseDZx(c.b)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ParseDZx = %+v, %v; want an error containing %q", c.name, d, err, c.want)
		}
	}
}

func TestCName(t *testing.T) {
	for in, want := range map[string]string{
		"Bk\x00\x00\x00\x00\x00\x00": "Bk", "\x00abc\x00\x00\x00\x00": "", "door12AB": "door12AB", "a\x00b\x00\x00\x00\x00\x00": "a",
	} {
		if got := cName(in); got != want {
			t.Errorf("cName(%q) = %q, want %q", in, got, want)
		}
	}
}
