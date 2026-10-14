package stagedata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// stageFiles is a Stage.arc-shaped file set: the dzs chunk file, a nested
// directory, a Yaz0-compressed entry and a file at the root.
func stageFiles() []stagetest.File {
	return []stagetest.File{
		{Path: "dzs/stage.dzs", Data: []byte("STAG chunk data stand-in")},
		{Path: "dat/event_list.dat", Data: bytes.Repeat([]byte("evnt"), 100)},
		{Path: "bdl/model.bdl", Data: bytes.Repeat([]byte("J3D2bdl4"), 300), Yaz0: true},
		{Path: "readme.txt", Data: []byte("root file")},
		{Path: "dat/sub/deep/x.bin", Data: []byte{0, 1, 2, 3}},
		{Path: "dzs/empty.dzs"},
	}
}

// rarcLayout locates an archive's tables, for tests that corrupt them.
type rarcLayout struct{ info, nodes, entries, strs, data int }

func layoutOf(b []byte) rarcLayout {
	be := binary.BigEndian
	info := int(be.Uint32(b[8:]))
	return rarcLayout{
		info:    info,
		nodes:   info + int(be.Uint32(b[info+0x04:])),
		entries: info + int(be.Uint32(b[info+0x0C:])),
		strs:    info + int(be.Uint32(b[info+0x14:])),
		data:    info + int(be.Uint32(b[0x0C:])),
	}
}

func (l rarcLayout) entry(i int) int { return l.entries + i*rarcEntrySize }
func (l rarcLayout) node(k int) int  { return l.nodes + k*rarcNodeSize }

func TestParseArchive(t *testing.T) {
	files := stageFiles()
	arc := stagetest.RARC("Stage", files...)
	// Nodes breadth first: 0 ROOT {dzs dat bdl readme.txt . ..}, 1 dzs
	// {stage.dzs empty.dzs . ..}, 2 dat {event_list.dat sub . ..}, 3 bdl
	// {model.bdl . ..}, 4 sub {deep . ..}, 5 deep {x.bin . ..}.
	want := []ArchiveFile{
		{Path: "readme.txt", Index: 3, ID: 3, Attr: 0x01, Type: "ROOT", Size: 9},
		{Path: "dzs/stage.dzs", Index: 6, ID: 6, Attr: 0x01, Type: "DZS ", Size: 24},
		{Path: "dzs/empty.dzs", Index: 7, ID: 7, Attr: 0x01, Type: "DZS ", Size: 0},
		{Path: "dat/event_list.dat", Index: 10, ID: 10, Attr: 0x01, Type: "DAT ", Size: 400},
		{Path: "bdl/model.bdl", Index: 14, ID: 14, Attr: 0x85, Type: "BDL ", Size: uint32(len(stagetest.Yaz0(files[2].Data)))},
		{Path: "dat/sub/deep/x.bin", Index: 20, ID: 20, Attr: 0x01, Type: "DEEP", Size: 4},
	}
	for _, c := range []struct {
		name string
		b    []byte
	}{{"plain", arc}, {"Yaz0-compressed, as on the disc", stagetest.Yaz0(arc)}} {
		a, err := ParseArchive(c.b)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := a.Files(); !slices.Equal(got, want) {
			t.Fatalf("%s: Files() =\n%+v\nwant\n%+v", c.name, got, want)
		}
		for _, f := range files {
			got, err := a.ReadFile(f.Path)
			if err != nil || !bytes.Equal(got, f.Data) {
				t.Errorf("%s: ReadFile(%q) = %q, %v", c.name, f.Path, got, err)
			}
		}
	}
	if !want[4].Compressed() || want[0].Compressed() {
		t.Error("Compressed() doesn't follow attribute 0x04")
	}
}

func TestArchiveLookup(t *testing.T) {
	a, err := ParseArchive(stagetest.RARC("Room0",
		stagetest.File{Path: "dzr/Room.dzr", Data: []byte("first")},
		stagetest.File{Path: "dzr/room.dzr", Data: []byte("second")},
		stagetest.File{Path: "room.dzr", Data: []byte("root")},
		stagetest.File{Path: "dzb/room.dzb", Data: []byte("collision")},
	))
	if err != nil {
		t.Fatal(err)
	}
	read := func(f ArchiveFile, ok bool) string {
		if !ok {
			return "<none>"
		}
		b, err := a.Read(f)
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}

	// By path: per directory, case-insensitive, first entry wins.
	for path, want := range map[string]string{
		"dzr/room.dzr":    "first",
		"DZR/ROOM.DZR":    "first",
		"/dzr/Room.dzr":   "first",
		"room.dzr":        "root",
		"dzb/room.dzb":    "collision",
		"dzr":             "<none>", // a directory
		"dzr/":            "<none>",
		"room.dzr/x":      "<none>", // a file isn't a directory
		"dzr/./room.dzr":  "<none>",
		"dzr/../room.dzr": "<none>",
		".":               "<none>",
		"":                "<none>",
		"dzb/room.dzr":    "<none>",
	} {
		if got := read(a.File(path)); got != want {
			t.Errorf("File(%q) reads %q, want %q", path, got, want)
		}
	}

	// By name, the way the game finds "room.dzr": exact, archive-wide, in
	// entry order (root first here: node 0's entries come first).
	for name, want := range map[string]string{
		"room.dzr": "root",
		"Room.dzr": "first",
		"ROOM.DZR": "<none>",
		"room.dzb": "collision",
		"dzr":      "<none>",
		".":        "<none>",
	} {
		if got := read(a.FindName(name)); got != want {
			t.Errorf("FindName(%q) reads %q, want %q", name, got, want)
		}
	}

	if _, err := a.ReadFile("missing.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(missing) err = %v, want fs.ErrNotExist", err)
	}
	if _, err := a.Read(ArchiveFile{Index: 0}); err == nil { // entry 0 is the dzr directory
		t.Error("Read of a directory entry succeeded")
	}
	if _, err := a.Read(ArchiveFile{Index: 1 << 20}); err == nil {
		t.Error("Read of an out-of-range entry succeeded")
	}
}

func TestParseArchiveRejects(t *testing.T) {
	arc := stagetest.RARC("Stage", stageFiles()...)
	l := layoutOf(arc)
	be := binary.BigEndian
	mutate := func(f func(b []byte)) []byte {
		b := bytes.Clone(arc)
		f(b)
		return b
	}
	lastName := bytes.LastIndex(arc, []byte("x.bin\x00"))
	cases := []struct {
		name    string
		b       []byte
		wantErr string
	}{
		{"empty", nil, "header truncated"},
		{"header truncated", arc[:0x1F], "header truncated"},
		{"bad magic", mutate(func(b []byte) { copy(b, "CRAR") }), `bad magic "CRAR"`},
		{"truncated", arc[:len(arc)-1], "truncated: the header says"},
		{"info block past the end", mutate(func(b []byte) { be.PutUint32(b[8:], uint32(len(b)-0x10)) }), "info block"},
		{"nodes past the end", mutate(func(b []byte) { be.PutUint32(b[l.info:], 0x10000000) }), "nodes at"},
		{"entries past the end", mutate(func(b []byte) { be.PutUint32(b[l.info+0x08:], 0xFFFFFFFF) }), "file entries at"},
		{"strings past the end", mutate(func(b []byte) { be.PutUint32(b[l.info+0x14:], 0xFFFFFFF0) }), "bytes of strings"},
		{"no root node", mutate(func(b []byte) { be.PutUint32(b[l.info:], 0) }), "no root node"},
		{"name past the strings", mutate(func(b []byte) { be.PutUint32(b[l.entry(3)+4:], 0x01FFFFFF) }), "file entry 3: name offset 0xffffff"},
		{"empty name", mutate(func(b []byte) { be.PutUint32(b[l.entry(3)+4:], 0x01000001) }), "file entry 3: name at 0x1 is empty"},
		{"slash in a name", mutate(func(b []byte) { b[lastName+1] = '/' }), `file entry 20: name "x/bin" has a '/'`},
		{"unterminated name", mutate(func(b []byte) { be.PutUint32(b[l.info+0x10:], uint32(lastName+5-l.strs)) }), "is empty or unterminated"},
		{"node name bad", mutate(func(b []byte) { be.PutUint32(b[l.node(2)+4:], 0x7FFFFFFF) }), "node 2: name offset"},
		{"node range past the entries", mutate(func(b []byte) { be.PutUint32(b[l.node(5)+0x0C:], 21) }), "node 5 lists entries 21..24"},
		{"overlapping nodes", mutate(func(b []byte) { be.PutUint32(b[l.node(1)+0x0C:], 4) }), "both list entry"},
		{"directory to a missing node", mutate(func(b []byte) { be.PutUint32(b[l.entry(0)+8:], 6) }), `directory "dzs" (entry 0) points at node 6`},
		{"directory to the root", mutate(func(b []byte) { be.PutUint32(b[l.entry(1)+8:], 0) }), `directory "dat" (entry 1) points at node 0`},
		{"two directories, one node", mutate(func(b []byte) { be.PutUint32(b[l.entry(2)+8:], 1) }), "already reached"},
		{"unreachable node", mutate(func(b []byte) { b[l.entry(2)+4] = 0x01 }), "node 3 is not reachable"}, // bdl/ made a file
		{"bad outer Yaz0", stagetest.Yaz0(arc)[:0x20], "rarc: yaz0:"},
	}
	for _, c := range cases {
		_, err := ParseArchive(c.b)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	// Paths may be maxArchivePath bytes long, no longer.
	long := strings.Repeat("n", maxArchivePath-4)
	if _, err := ParseArchive(stagetest.RARC("a", stagetest.File{Path: "dir/" + long})); err != nil {
		t.Errorf("a %d-byte path: %v", maxArchivePath, err)
	}
	if _, err := ParseArchive(stagetest.RARC("a", stagetest.File{Path: "dir/" + long + "n"})); err == nil ||
		!strings.Contains(err.Error(), "longer than 255 bytes") {
		t.Errorf("a %d-byte path: err = %v", maxArchivePath+1, err)
	}
}

// sharedNameRARC is an archive whose root node lists nothing and whose n
// file entries, like the root, all name the string at offset 0 of an
// l-byte string table: l-1 'a's and a NUL.
func sharedNameRARC(n, l int) []byte {
	be := binary.BigEndian
	const info, nodes, ents = rarcHeaderSize, rarcInfoSize, rarcInfoSize + rarcNodeSize
	strs := ents + n*rarcEntrySize
	b := make([]byte, info+strs+l)
	copy(b, "RARC")
	be.PutUint32(b[4:], uint32(len(b)))
	be.PutUint32(b[8:], info)
	be.PutUint32(b[0x0C:], uint32(strs+l)) // no file data, after the strings
	ib := b[info:]
	be.PutUint32(ib[0x00:], 1)
	be.PutUint32(ib[0x04:], nodes)
	be.PutUint32(ib[0x08:], uint32(n))
	be.PutUint32(ib[0x0C:], ents)
	be.PutUint32(ib[0x10:], uint32(l))
	be.PutUint32(ib[0x14:], uint32(strs))
	copy(ib[nodes:], "ROOT")
	for i := range n {
		ib[ents+i*rarcEntrySize+4] = 0x01 // a file named at offset 0
	}
	copy(ib[strs:], bytes.Repeat([]byte{'a'}, l-1))
	return b
}

// Many entries naming one long string cost no more than short names: a
// name is searched for its NUL only as far as a path can be long. Searched
// to the end, this archive took minutes.
func TestParseArchiveSharedNames(t *testing.T) {
	start := time.Now()
	if _, err := ParseArchive(sharedNameRARC(200000, 4<<20)); err == nil || !strings.Contains(err.Error(), "file entry 0: name at 0x0 is longer than 255 bytes") {
		t.Errorf("a 4 MiB name: err = %v", err)
	}
	a, err := ParseArchive(sharedNameRARC(200000, maxArchivePath+1))
	if err != nil {
		t.Fatalf("a %d-byte name: %v", maxArchivePath, err)
	}
	if len(a.Files()) != 0 {
		t.Errorf("unlisted entries listed: %v", a.Files())
	}
	if _, err := ParseArchive(sharedNameRARC(1, maxArchivePath+2)); err == nil || !strings.Contains(err.Error(), "longer than 255 bytes") {
		t.Errorf("a %d-byte name: err = %v", maxArchivePath+1, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

// An uncompressed archive the game couldn't hold is refused, as a Yaz0
// one declaring that much is (TestDecodeYaz0Rejects).
func TestParseArchiveSizeCap(t *testing.T) {
	arc := stagetest.RARC("Stage", stageFiles()...)
	big := append(bytes.Clone(arc), make([]byte, MaxYaz0Size+1-len(arc))...)
	if _, err := ParseArchive(big); err == nil || !strings.Contains(err.Error(), "25165825 bytes, more than the 25165824-byte cap") {
		t.Errorf("a %d-byte archive: err = %v", len(big), err)
	}
	if _, err := ParseArchive(big[:MaxYaz0Size]); err != nil {
		t.Errorf("a %d-byte archive: %v", MaxYaz0Size, err)
	}

	// MaxArchiveSize is the longest Yaz0 stream of MaxYaz0Size bytes: all
	// literals, a code byte per eight.
	literal := func(n int) int { return len(yaz0Stream(uint32(n), lits(make([]byte, n))...)) }
	for _, n := range []int{1, 8, 9, 64, 1000} {
		if got, want := literal(n), yaz0HeaderSize+n+(n+7)/8; got != want {
			t.Fatalf("an all-literal stream of %d bytes is %d long, want %d", n, got, want)
		}
	}
	if want := yaz0HeaderSize + MaxYaz0Size + (MaxYaz0Size+7)/8; MaxArchiveSize != want {
		t.Errorf("MaxArchiveSize = %d, want %d", MaxArchiveSize, want)
	}
}

// Entries no node lists, like the "." and ".." entries, are never files.
func TestArchiveIgnoresUnlistedEntries(t *testing.T) {
	arc := stagetest.RARC("a", stagetest.File{Path: "f", Data: []byte("x")})
	l := layoutOf(arc)
	// One entry (f), then "." and "..": shrink the root to list only f
	// and turn ".." into a file, which nothing lists any more.
	binary.BigEndian.PutUint16(arc[l.node(0)+0x0A:], 1)
	arc[l.entry(2)+4] = 0x01
	a, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Files(); len(got) != 1 || got[0].Path != "f" {
		t.Fatalf("Files() = %+v, want just f", got)
	}
	if _, ok := a.FindName(".."); ok {
		t.Error("FindName found an entry no node lists")
	}
}

func TestArchiveReadErrors(t *testing.T) {
	files := []stagetest.File{
		{Path: "plain.bin", Data: []byte("plain data")},
		{Path: "packed.bin", Data: bytes.Repeat([]byte("packed "), 20), Yaz0: true},
	}
	arc := stagetest.RARC("a", files...)
	l := layoutOf(arc)
	be := binary.BigEndian
	plainData, packedData := l.data+int(be.Uint32(arc[l.entry(0)+8:])), l.data+int(be.Uint32(arc[l.entry(1)+8:]))
	cases := []struct {
		name, path string
		mutate     func(b []byte)
		wantErr    string
	}{
		{"data past the end", "plain.bin", func(b []byte) { be.PutUint32(b[l.entry(0)+0x0C:], 0x10000) }, "runs past the end of the archive"},
		{"offset past the end", "plain.bin", func(b []byte) { be.PutUint32(b[l.entry(0)+0x08:], 0xFFFFFFFF) }, "runs past the end of the archive"},
		{"compressed without a header", "plain.bin", func(b []byte) { b[l.entry(0)+4] = 0x85 }, "marked compressed but has no Yaz0 header"},
		{"Yay0", "plain.bin", func(b []byte) { b[l.entry(0)+4] = 0x05; copy(b[plainData:], "Yay0") }, "Yay0 compression is not supported"},
		{"corrupt Yaz0 entry", "packed.bin", func(b []byte) { be.PutUint32(b[packedData+4:], 0xFFFF) }, "packed.bin\": yaz0:"},
	}
	for _, c := range cases {
		b := bytes.Clone(arc)
		c.mutate(b)
		a, err := ParseArchive(b)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := a.ReadFile(c.path); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	// The attribute decides, not the bytes: an uncompressed file that
	// happens to start "Yaz0" is returned as stored.
	b := bytes.Clone(arc)
	copy(b[plainData:], "Yaz0")
	a, err := ParseArchive(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.ReadFile("plain.bin"); err != nil || string(got) != "Yaz0n data" {
		t.Errorf("plain file starting Yaz0 = %q, %v", got, err)
	}
}

// Read returns a copy: changing it doesn't change the archive.
func TestArchiveReadCopies(t *testing.T) {
	a, err := ParseArchive(stagetest.RARC("a", stagetest.File{Path: "f", Data: []byte("abc")}))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := a.ReadFile("f")
	got[0] = 'X'
	if again, _ := a.ReadFile("f"); string(again) != "abc" {
		t.Fatalf("archive changed to %q", again)
	}
}
