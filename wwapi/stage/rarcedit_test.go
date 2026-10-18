package stage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// editOf parses b and opens an editor on it.
func editOf(t *testing.T, b []byte) *ArchiveEditor {
	t.Helper()
	a, err := ParseArchive(b)
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Edit()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// saved writes e out and parses the result.
func saved(t *testing.T, e *ArchiveEditor) ([]byte, *Archive) {
	t.Helper()
	out, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	a, err := ParseArchive(out)
	if err != nil {
		t.Fatalf("the saved archive doesn't parse: %v", err)
	}
	return out, a
}

// An unchanged archive saves byte for byte: stagetest.RARC writes the
// same layout, and so does the disc (TestArchiveEditorVanillaRELS).
func TestArchiveEditorUnchanged(t *testing.T) {
	for _, c := range []struct {
		name string
		arc  []byte
	}{
		{"stage", stagetest.RARC("Stage", stageFiles()...)},
		{"one file", stagetest.RARC("a", stagetest.File{Path: "f", Data: []byte("x")})},
		{"empty file only", stagetest.RARC("a", stagetest.File{Path: "d/e"})},
		{"no files", stagetest.RARC("a")},
	} {
		e := editOf(t, c.arc)
		out, _ := saved(t, e)
		if !bytes.Equal(out, c.arc) {
			t.Errorf("%s: saved %d bytes differ from the %d parsed", c.name, len(out), len(c.arc))
		}
		if g := e.Growth(); g != 0 {
			t.Errorf("%s: Growth() = %d", c.name, g)
		}
		if a, _ := ParseArchive(c.arc); !slices.Equal(e.Files(), a.Files()) {
			t.Errorf("%s: Files() differ", c.name)
		}
	}
}

func TestArchiveEditorReplace(t *testing.T) {
	files := stageFiles()
	e := editOf(t, stagetest.RARC("Stage", files...))
	plain := bytes.Repeat([]byte("new plain contents "), 10)
	packed := bytes.Repeat([]byte("new packed contents "), 50)
	if err := e.ReplaceFile("dat/event_list.dat", plain); err != nil {
		t.Fatal(err)
	}
	f, _ := e.a.File("bdl/model.bdl")
	if err := e.Replace(f, packed); err != nil {
		t.Fatal(err)
	}
	packed[0] = 'X' // the editor copied it

	out, a := saved(t, e)
	want := map[string][]byte{"dat/event_list.dat": plain, "bdl/model.bdl": bytes.Clone(packed)}
	want["bdl/model.bdl"][0] = 'n'
	for _, f := range files {
		w, ok := want[f.Path]
		if !ok {
			w = f.Data
		}
		for who, read := range map[string]func(string) ([]byte, error){
			"saved": a.ReadFile,
			"editor": func(p string) ([]byte, error) {
				f, ok := e.a.File(p)
				if !ok {
					return nil, fs.ErrNotExist
				}
				return e.Read(f)
			},
		} {
			got, err := read(f.Path)
			if err != nil || !bytes.Equal(got, w) {
				t.Errorf("%s: %s reads %d bytes, %v; want %d", f.Path, who, len(got), err, len(w))
			}
		}
	}
	// The replaced compressed file is stored Yaz0 under its attribute;
	// the plain one as given; sizes follow.
	bdl, _ := a.File("bdl/model.bdl")
	dat, _ := a.File("dat/event_list.dat")
	if bdl.Attr != 0x85 || bdl.Size != uint32(len(EncodeYaz0(want["bdl/model.bdl"]))) {
		t.Errorf("bdl/model.bdl: attr 0x%02x size %d", bdl.Attr, bdl.Size)
	}
	if dat.Attr != 0x01 || dat.Size != uint32(len(plain)) {
		t.Errorf("dat/event_list.dat: attr 0x%02x size %d", dat.Attr, dat.Size)
	}
	if got := e.Files(); !slices.Equal(got, a.Files()) {
		t.Errorf("editor Files() =\n%+v\nsaved archive\n%+v", got, a.Files())
	}
	if g := e.Growth(); g != len(out)-len(stagetest.RARC("Stage", files...)) {
		t.Errorf("Growth() = %d, the archive grew %d", g, len(out)-len(stagetest.RARC("Stage", files...)))
	}
	// Saving again gives the same bytes; the tables didn't move.
	if again, _ := saved(t, e); !bytes.Equal(again, out) {
		t.Error("a second save differs")
	}

	// Errors: a missing path, a directory, a Yay0 entry, a bad index.
	if err := e.ReplaceFile("missing.bin", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReplaceFile(missing) = %v", err)
	}
	if err := e.ReplaceFile("dzs", nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReplaceFile(directory) = %v", err)
	}
	if err := e.Replace(ArchiveFile{Index: 0}, nil); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Errorf("Replace(directory entry) = %v", err)
	}
	e.a.entries[bdl.Index].attr = 0x05 // Yay0
	if err := e.Replace(bdl, nil); err == nil || !strings.Contains(err.Error(), "Yay0") {
		t.Errorf("Replace(Yay0 entry) = %v", err)
	}
}

// ReplaceByFirstWord finds a file by the first word of its contents,
// looking through compression, and leaves every other file stored as it
// was.
func TestArchiveEditorReplaceByFirstWord(t *testing.T) {
	rel := func(id uint32, n int) []byte {
		b := make([]byte, n)
		binary.BigEndian.PutUint32(b, id)
		for i := 4; i < n; i++ {
			b[i] = byte(i * 7 % 13)
		}
		return b
	}
	files := []stagetest.File{
		{Path: "amem/a.rel", Data: rel(0x78, 0x300), Yaz0: true},
		{Path: "amem/b.rel", Data: rel(0x58, 0x4000), Yaz0: true},
		{Path: "mmem/c.rel", Data: rel(0x58, 0x100), Yaz0: true}, // a second 0x58: the first wins
		{Path: "mmem/plain.rel", Data: rel(0x01, 0x40)},
		{Path: "mmem/tiny", Data: []byte{0, 0}},
	}
	e := editOf(t, stagetest.RARC("rels", files...))
	before := slices.Clone(e.blobs)
	fake := rel(0x58, 0x2000)
	f, err := e.ReplaceByFirstWord(0x58, fake)
	if err != nil || f.Path != "amem/b.rel" || f.Size != uint32(len(EncodeYaz0(fake))) {
		t.Fatalf("ReplaceByFirstWord(0x58) = %+v, %v", f, err)
	}
	if f, err := e.ReplaceByFirstWord(0x01, rel(1, 8)); err != nil || f.Path != "mmem/plain.rel" || f.Size != 8 {
		t.Errorf("ReplaceByFirstWord(0x01) = %+v, %v", f, err)
	}
	if _, err := e.ReplaceByFirstWord(0xFFFF, nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReplaceByFirstWord(unknown) = %v", err)
	}
	for i, b := range e.blobs {
		changed := i == f.Index || e.a.entries[i].name == "plain.rel"
		if same := bytes.Equal(b, before[i]); same == changed {
			t.Errorf("entry %d (%s): stored bytes changed: %v", i, e.a.entries[i].name, !same)
		}
	}
	_, a := saved(t, e)
	if got, err := a.ReadFile("amem/b.rel"); err != nil || !bytes.Equal(got, fake) {
		t.Errorf("amem/b.rel reads %d bytes, %v", len(got), err)
	}
	if got, err := a.ReadFile("mmem/c.rel"); err != nil || !bytes.Equal(got, files[2].Data) {
		t.Errorf("mmem/c.rel reads %d bytes, %v", len(got), err)
	}

	// A file that can't be expanded is reported, not skipped.
	e.blobs[e.a.files[0].Index] = []byte("Yaz0\xff\xff\xff\xff")
	if _, err := e.ReplaceByFirstWord(0x58, nil); err == nil || !strings.Contains(err.Error(), "yaz0") {
		t.Errorf("with a corrupt entry: %v", err)
	}
}

// File data is grouped by preload attribute (main RAM, ARAM, DVD, none),
// each file 0x20-aligned, and the header carries the main RAM and ARAM
// sizes.
func TestArchiveEditorPreloadGroups(t *testing.T) {
	files := []stagetest.File{
		{Path: "dvd", Data: bytes.Repeat([]byte("d"), 0x21)},
		{Path: "none", Data: []byte("n")},
		{Path: "aram", Data: bytes.Repeat([]byte("a"), 0x40)},
		{Path: "mram", Data: []byte("m")},
		{Path: "aram2", Data: []byte("a2")},
	}
	arc := stagetest.RARC("g", files...)
	l := layoutOf(arc)
	for i, attr := range []byte{0x41, 0x01, 0x21, 0x11, 0x21} {
		arc[l.entry(i)+4] = attr
	}
	e := editOf(t, arc)
	out, a := saved(t, e)
	be := binary.BigEndian
	// mram at 0 (size 1 -> 0x20), aram at 0x20 (0x40 -> 0x60) and 0x60
	// (2 -> 0x80), dvd at 0x80 (0x21 -> 0xC0), none at 0xC0 (1 -> 0xE0).
	wantOff := map[string]uint32{"mram": 0, "aram": 0x20, "aram2": 0x60, "dvd": 0x80, "none": 0xC0}
	ol := layoutOf(out)
	for _, f := range a.Files() {
		if got := be.Uint32(out[ol.entry(f.Index)+8:]); got != wantOff[f.Path] {
			t.Errorf("%s at data offset 0x%x, want 0x%x", f.Path, got, wantOff[f.Path])
		}
		if got, err := a.ReadFile(f.Path); err != nil || !bytes.Equal(got, files[slices.IndexFunc(files, func(x stagetest.File) bool { return x.Path == f.Path })].Data) {
			t.Errorf("%s reads %q, %v", f.Path, got, err)
		}
	}
	if mram, aram, total := be.Uint32(out[0x14:]), be.Uint32(out[0x18:]), be.Uint32(out[0x10:]); mram != 0x20 || aram != 0x60 || total != 0xE0 {
		t.Errorf("header sizes: main RAM 0x%x ARAM 0x%x total 0x%x, want 0x20 0x60 0xE0", mram, aram, total)
	}
	if got, want := be.Uint32(out[4:]), uint32(ol.data+0xE0); got != want || int(got) != len(out) {
		t.Errorf("header size 0x%x, want 0x%x (%d bytes written)", got, want, len(out))
	}
}

// Edit refuses an archive whose file data runs past its end, and Bytes
// an archive over the cap.
func TestArchiveEditorRejects(t *testing.T) {
	arc := stagetest.RARC("a", stagetest.File{Path: "f", Data: []byte("abc")})
	l := layoutOf(arc)
	binary.BigEndian.PutUint32(arc[l.entry(0)+0x0C:], 0x1000)
	a, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Edit(); err == nil || !strings.Contains(err.Error(), "runs past the end") {
		t.Errorf("Edit() = %v", err)
	}

	e := editOf(t, stagetest.RARC("a", stagetest.File{Path: "f", Data: []byte("abc")}))
	e.blobs[0] = make([]byte, MaxYaz0Size)
	if _, err := e.Bytes(); err == nil || !strings.Contains(err.Error(), "more than the") {
		t.Errorf("Bytes() over the cap = %v", err)
	}
	if g := e.Growth(); g < MaxYaz0Size-len(e.a.data) {
		t.Errorf("Growth() = %d", g)
	}
}

// The disc's RELS.arc: parsed and saved unchanged it is byte for byte
// the same; with one REL replaced it parses, the REL reads back, every
// other entry is stored as it was, and the archive hasn't grown by more
// than the replacement.
func TestArchiveEditorVanillaRELS(t *testing.T) {
	arc := vanillatest.ReadFile(t, vanillatest.RELSArc)
	e := editOf(t, arc)
	out, _ := saved(t, e)
	if !bytes.Equal(out, arc) {
		i := 0
		for i < len(out) && i < len(arc) && out[i] == arc[i] {
			i++
		}
		t.Fatalf("saved RELS.arc (%d bytes) differs from the disc's (%d) from byte 0x%x", len(out), len(arc), i)
	}

	const profileLst = 0x01 // f_pc_profile_lst.rel's module ID
	fake := make([]byte, 0x4000)
	binary.BigEndian.PutUint32(fake, profileLst)
	for i := 4; i < len(fake); i++ {
		fake[i] = byte(i * 7 % 13)
	}
	before := slices.Clone(e.blobs)
	f, err := e.ReplaceByFirstWord(profileLst, fake)
	if err != nil || f.Path != "mmem/f_pc_profile_lst.rel" {
		t.Fatalf("ReplaceByFirstWord(0x%x) = %+v, %v", profileLst, f, err)
	}
	out, a := saved(t, e)
	if got, err := a.ReadFile(f.Path); err != nil || !bytes.Equal(got, fake) {
		t.Errorf("%s reads %d bytes, %v", f.Path, len(got), err)
	}
	for i, b := range e.blobs {
		if i != f.Index && !bytes.Equal(b, before[i]) {
			t.Errorf("entry %d (%s) changed", i, e.a.entries[i].name)
		}
	}
	if g, max := e.Growth(), len(EncodeYaz0(fake))+0x20; g != len(out)-len(arc) || g > max {
		t.Errorf("Growth() = %d (saved %d, disc %d), want at most %d", g, len(out), len(arc), max)
	}
}
