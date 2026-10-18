package rel

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// synthREL is a small version-3 REL laid out the way the vanilla ones
// are: the section table at 0x4C, .text right after it, .data 8-aligned
// (a 4-byte gap), the import table at the end of the data, and the
// relocations module by module, others first, then the module itself,
// then module 0, whose run has an explicit NOP.
func synthREL() []byte {
	be := binary.BigEndian
	text := bytes.Repeat([]byte{0x60, 0, 0, 0}, 12) // 0x30 bytes
	data := []byte("0123456789abcdef")
	b := make([]byte, 0xB0)
	be.PutUint32(b[0x00:], 0x10) // module ID
	be.PutUint32(b[0x0C:], 4)
	be.PutUint32(b[0x10:], 0x4C)
	be.PutUint32(b[0x14:], 0x1234) // name offset and size, meaningless here
	be.PutUint32(b[0x18:], 0x2A)
	be.PutUint32(b[0x1C:], 3)
	be.PutUint32(b[0x20:], 0x40) // .bss
	b[0x30], b[0x31], b[0x32] = 1, 1, 1
	be.PutUint32(b[0x34:], 0)
	be.PutUint32(b[0x38:], 0x10)
	be.PutUint32(b[0x3C:], 0x20)
	be.PutUint32(b[0x40:], 8)
	be.PutUint32(b[0x44:], 1)
	for i, s := range [][2]uint32{{0, 0}, {0x6C | 1, 0x30}, {0xA0, 0x10}, {0, 0x40}} {
		be.PutUint32(b[0x4C+i*8:], s[0])
		be.PutUint32(b[0x4C+i*8+4:], s[1])
	}
	copy(b[0x6C:], text)
	copy(b[0xA0:], data)
	// Three modules: their import entries, then their relocations.
	runs := [][][]byte{
		{entry(0, 0xCA, 1, 0), entry(4, 1, 2, 0x20), entry(8, 0x0A, 1, 0x100), entry(0, 0xCA, 2, 0), entry(0, 1, 4, 0), entry(0, 0xCB, 0, 0)},
		{entry(0, 0xCA, 2, 0), entry(4, 1, 1, 8), entry(0, 0xCB, 0, 0)},
		{entry(0, 0xCA, 1, 0), entry(0xC, 6, 0, 0x80001234), entry(0, 4, 0, 0x80001234), entry(0x1000, 0xC9, 0, 0), entry(4, 1, 0, 0x80005678), entry(0, 0xCB, 0, 0)},
	}
	imp := make([]byte, 3*8)
	var relocs []byte
	off := 0xB0 + len(imp)
	for i, m := range []uint32{0x42, 0x10, 0} {
		be.PutUint32(imp[i*8:], m)
		be.PutUint32(imp[i*8+4:], uint32(off+len(relocs)))
		for _, e := range runs[i] {
			relocs = append(relocs, e...)
		}
		if i == 0 {
			be.PutUint32(b[0x48:], uint32(off+len(relocs))) // fix size: after the other module's run
		}
	}
	be.PutUint32(b[0x24:], uint32(off))
	be.PutUint32(b[0x28:], 0xB0)
	be.PutUint32(b[0x2C:], uint32(len(imp)))
	return append(append(b, imp...), relocs...)
}

func entry(delta uint16, typ, sec uint8, addend uint32) []byte {
	e := make([]byte, 8)
	binary.BigEndian.PutUint16(e, delta)
	e[2], e[3] = typ, sec
	binary.BigEndian.PutUint32(e[4:], addend)
	return e
}

// synthFile is what synthREL parses to.
func synthFile() *File {
	return &File{
		ID: 0x10, Version: 3, NameOffset: 0x1234, NameSize: 0x2A, BSSSize: 0x40,
		Prolog: Symbol{1, 0}, Epilog: Symbol{1, 0x10}, Unresolved: Symbol{1, 0x20},
		Align: 8, BSSAlign: 1, FixSize: 0xF8,
		Sections: []Section{
			{},
			{Offset: 0x6C, Exec: true, Data: bytes.Repeat([]byte{0x60, 0, 0, 0}, 12), Size: 0x30},
			{Offset: 0xA0, Data: []byte("0123456789abcdef"), Size: 0x10},
			{Size: 0x40},
		},
		Imports: []Import{
			{Module: 0x42, Relocs: []Reloc{
				{Section: 1, Offset: 4, Type: RelocAddr32, TargetSection: 2, Addend: 0x20},
				{Section: 1, Offset: 0xC, Type: RelocRel24, TargetSection: 1, Addend: 0x100},
				{Section: 2, Offset: 0, Type: RelocAddr32, TargetSection: 4},
			}},
			{Module: 0x10, Relocs: []Reloc{{Section: 2, Offset: 4, Type: RelocAddr32, TargetSection: 1, Addend: 8}}},
			{Module: 0, Relocs: []Reloc{
				{Section: 1, Offset: 0xC, Type: RelocAddr16Ha, Addend: 0x80001234},
				{Section: 1, Offset: 0xC, Type: RelocAddr16Lo, Addend: 0x80001234},
				{Section: 1, Offset: 0x100C, Type: RelocNop},
				{Section: 1, Offset: 0x1010, Type: RelocAddr32, Addend: 0x80005678},
			}},
		},
	}
}

func TestParse(t *testing.T) {
	b := synthREL()
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := synthFile(); !reflect.DeepEqual(f, want) {
		t.Fatalf("Parse() =\n%+v\nwant\n%+v", f, want)
	}
	if !f.Sections[3].BSS() || f.Sections[0].BSS() || f.Sections[1].BSS() {
		t.Error("BSS() is wrong")
	}
	// A Yaz0-compressed REL (as RELS.arc stores them) parses the same.
	if g, err := Parse(stage.EncodeYaz0(b)); err != nil || !reflect.DeepEqual(g, f) {
		t.Errorf("Parse(Yaz0): %v", err)
	}
}

// Saved with its offsets preserved, the REL is byte for byte what was
// parsed; saved without, the 8-alignment gap goes, and it parses to the
// same module.
func TestBytesRoundTrip(t *testing.T) {
	b := synthREL()
	f, _ := Parse(b)
	out, err := f.Bytes(true)
	if err != nil || !bytes.Equal(out, b) {
		t.Errorf("Bytes(true) = %d bytes, %v; want the %d parsed:\n%x\n%x", len(out), err, len(b), out, b)
	}
	packed, err := f.Bytes(false)
	if err != nil || len(packed) != len(b)-4 {
		t.Fatalf("Bytes(false) = %d bytes, %v; want %d", len(packed), err, len(b)-4)
	}
	g, err := Parse(packed)
	if err != nil {
		t.Fatal(err)
	}
	if g.Sections[2].Offset != 0x9C || f.Sections[2].Offset != 0x9C || f.FixSize != 0xF4 || g.FixSize != 0xF4 {
		t.Errorf(".data at 0x%x (recorded 0x%x), fix size 0x%x (recorded 0x%x)", g.Sections[2].Offset, f.Sections[2].Offset, g.FixSize, f.FixSize)
	}
	if !sameModule(f, g) {
		t.Errorf("the packed REL parses differently:\n%+v\n%+v", g, f)
	}
	// Packed, it saves the same way again (with or without preserving
	// the offsets it now has).
	for _, p := range []bool{true, false} {
		if again, err := g.Bytes(p); err != nil || !bytes.Equal(again, packed) {
			t.Errorf("Bytes(%v) of the packed REL differs: %v", p, err)
		}
	}
}

// sameModule reports whether a and b are the same module, wherever
// their sections and relocations sit in the file.
func sameModule(a, b *File) bool {
	norm := func(f *File) *File {
		g := *f
		g.FixSize = 0
		g.Sections = slices.Clone(f.Sections)
		for i := range g.Sections {
			g.Sections[i].Offset = 0
		}
		return &g
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// The import table is written others first, then the module itself,
// then module 0, whatever order it was read in, and the fix size is the
// end of the others' relocations.
func TestBytesOrdersModules(t *testing.T) {
	f := synthFile()
	f.Imports[0], f.Imports[2] = f.Imports[2], f.Imports[0] // 0, self, 0x42
	out, err := f.Bytes(true)
	if err != nil || !bytes.Equal(out, synthREL()) {
		t.Errorf("Bytes() with the modules in another order differs: %v", err)
	}
	f = synthFile()
	f.Imports = f.Imports[1:] // self and 0 only
	out, _ = f.Bytes(true)
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint32(0xB0 + 2*8); g.FixSize != want || f.FixSize != want {
		t.Errorf("fix size 0x%x, want the relocation table's start 0x%x", g.FixSize, want)
	}
	if got := []uint32{g.Imports[0].Module, g.Imports[1].Module}; !slices.Equal(got, []uint32{0x10, 0}) {
		t.Errorf("modules %v, want [0x10 0]", got)
	}
}

// Relocations are written in (section, offset) order with a section
// marker per change and NOPs over gaps longer than 0xFFFF; the parse of
// the result lists those NOPs as entries.
func TestBytesSortsAndBridges(t *testing.T) {
	f := synthFile()
	f.Imports[0].Relocs = []Reloc{
		{Section: 2, Offset: 8, Type: RelocAddr32},
		{Section: 1, Offset: 0x2_0010, Type: RelocAddr32, Addend: 1},
		{Section: 1, Offset: 0, Type: RelocAddr32, Addend: 2},
		{Section: 2, Offset: 0, Type: RelocAddr32, Addend: 3},
	}
	out, err := f.Bytes(true)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Reloc{
		{Section: 1, Offset: 0, Type: RelocAddr32, Addend: 2},
		{Section: 1, Offset: 0xFFFF, Type: RelocNop},
		{Section: 1, Offset: 0x1FFFE, Type: RelocNop},
		{Section: 1, Offset: 0x2_0010, Type: RelocAddr32, Addend: 1},
		{Section: 2, Offset: 0, Type: RelocAddr32, Addend: 3},
		{Section: 2, Offset: 8, Type: RelocAddr32},
	}
	if !slices.Equal(g.Imports[0].Relocs, want) {
		t.Errorf("relocations against 0x42 =\n%+v\nwant\n%+v", g.Imports[0].Relocs, want)
	}
	if again, err := g.Bytes(true); err != nil || !bytes.Equal(again, out) {
		t.Errorf("saving the parse again differs: %v", err)
	}
}

// Editing the one relocation against a module, the way the patcher
// repoints f_pc_profile_lst's entry for the puppet's module, changes
// only that entry's bytes.
func TestSoleReloc(t *testing.T) {
	f, _ := Parse(synthREL())
	if _, err := f.SoleReloc(0x42); err == nil || !strings.Contains(err.Error(), "3 relocations") {
		t.Errorf("SoleReloc(0x42) = %v", err)
	}
	if _, err := f.SoleReloc(0x99); err == nil || !strings.Contains(err.Error(), "0 relocations") {
		t.Errorf("SoleReloc(0x99) = %v", err)
	}
	if f.Import(0x99) != nil || f.Import(0).Module != 0 {
		t.Error("Import() is wrong")
	}
	r, err := f.SoleReloc(0x10)
	if err != nil {
		t.Fatal(err)
	}
	r.Addend, r.TargetSection = 0x1C, 4
	out, err := f.Bytes(true)
	if err != nil {
		t.Fatal(err)
	}
	orig := synthREL()
	diff := 0
	for i := range orig {
		if out[i] != orig[i] {
			diff++
		}
	}
	if len(out) != len(orig) || diff == 0 || diff > 8 {
		t.Errorf("%d bytes differ (%d written, %d parsed), want the one entry", diff, len(out), len(orig))
	}
	g, _ := Parse(out)
	if r, err := g.SoleReloc(0x10); err != nil || *r != (Reloc{Section: 2, Offset: 4, Type: RelocAddr32, TargetSection: 4, Addend: 0x1C}) {
		t.Errorf("after the edit: %+v, %v", r, err)
	}
}

func TestParseRejects(t *testing.T) {
	b := synthREL()
	be := binary.BigEndian
	mutate := func(f func(b []byte)) []byte {
		c := bytes.Clone(b)
		f(c)
		return c
	}
	cases := []struct {
		name    string
		b       []byte
		wantErr string
	}{
		{"empty", nil, "header truncated"},
		{"truncated", b[:0x3F], "header truncated"},
		{"version 3 cut", b[:0x48], "version 3 header truncated"},
		{"version 0", mutate(func(b []byte) { be.PutUint32(b[0x1C:], 0) }), "unknown format version 0"},
		{"version 4", mutate(func(b []byte) { be.PutUint32(b[0x1C:], 4) }), "unknown format version 4"},
		{"sections past the end", mutate(func(b []byte) { be.PutUint32(b[0x0C:], 1000) }), "1000 sections at 0x4c run past"},
		{"section data past the end", mutate(func(b []byte) { be.PutUint32(b[0x4C+8+4:], 0x1000) }), "section 1: data"},
		{"import size not a multiple of 8", mutate(func(b []byte) { be.PutUint32(b[0x2C:], 0x14) }), "not a multiple of 8"},
		{"imports past the end", mutate(func(b []byte) { be.PutUint32(b[0x28:], 0xFFFF) }), "3 imports at 0xffff run past"},
		{"relocations past the end", mutate(func(b []byte) { be.PutUint32(b[0xB0+4:], uint32(len(b)-4)) }), "without R_DOLPHIN_END"},
		{"no END", mutate(func(b []byte) { b[len(b)-6] = 1 }), "without R_DOLPHIN_END"},
		{"relocation before a section marker", mutate(func(b []byte) { b[0xC8+2] = 1 }), "before any R_DOLPHIN_SECTION"},
		{"corrupt Yaz0", []byte("Yaz0\xff\xff\xff\xff\x00\x00\x00\x00\x00\x00\x00\x00"), "rel: yaz0:"},
	}
	for _, c := range cases {
		_, err := Parse(c.b)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
	// Two imports pointing at one run read it twice; a run that loops
	// back over itself can't outnumber the file's bytes.
	loop := mutate(func(b []byte) { be.PutUint32(b[0xB0+4:], 0xB0+8) })
	be.PutUint32(loop[0x2C:], 8)
	copy(loop[0xB0+8:], entry(0, 0xCA, 1, 0))
	loop = loop[:0xB0+16]
	be.PutUint32(loop[0xB0+4:], 0xB0+8)
	if _, err := Parse(loop); err == nil {
		t.Error("a relocation run that never ends parsed")
	}
}

func TestBytesRejects(t *testing.T) {
	f, _ := Parse(synthREL())
	f.Sections[2].Offset = 0x6C // under .text
	if _, err := f.Bytes(true); err == nil || !strings.Contains(err.Error(), "section 2 at 0x6c overlaps") {
		t.Errorf("overlapping sections: %v", err)
	}
	if _, err := f.Bytes(false); err != nil {
		t.Errorf("without preserving offsets: %v", err)
	}
	f.Version = 7
	if _, err := f.Bytes(false); err == nil || !strings.Contains(err.Error(), "unknown format version 7") {
		t.Errorf("version 7: %v", err)
	}
}

// Versions 1 and 2 have shorter headers; the section table follows.
func TestVersions(t *testing.T) {
	for v, hdr := range map[uint32]int{1: 0x40, 2: 0x48, 3: 0x4C} {
		f := synthFile()
		f.Version = v
		out, err := f.Bytes(false)
		if err != nil {
			t.Fatal(err)
		}
		if got := binary.BigEndian.Uint32(out[0x10:]); int(got) != hdr || f.Sections[1].Offset != uint32(hdr+4*8) {
			t.Errorf("version %d: section table at 0x%x, .text at 0x%x; want 0x%x and 0x%x", v, got, f.Sections[1].Offset, hdr, hdr+4*8)
		}
		g, err := Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		want := synthFile()
		want.Version = v
		if v < 2 {
			want.Align, want.BSSAlign = 0, 0
		}
		if !sameModule(g, want) {
			t.Errorf("version %d parses to\n%+v\nwant\n%+v", v, g, want)
		}
	}
}

// Every REL on the disc, parsed and saved in place, is byte for byte
// what RELS.arc holds; packed, it is the same module. The profile
// list's relocation against the puppet module can be repointed in
// place.
func TestVanillaRELs(t *testing.T) {
	a, err := stage.ParseArchive(vanillatest.ReadFile(t, vanillatest.RELSArc))
	if err != nil {
		t.Fatal(err)
	}
	nops, versions := 0, map[uint32]int{}
	for _, af := range a.Files() {
		b, err := a.Read(af)
		if err != nil {
			t.Fatal(err)
		}
		f, err := Parse(b)
		if err != nil {
			t.Errorf("%s: %v", af.Path, err)
			continue
		}
		versions[f.Version]++
		for _, imp := range f.Imports {
			for _, r := range imp.Relocs {
				if r.Type == RelocNop {
					nops++
				}
			}
		}
		out, err := f.Bytes(true)
		if err != nil {
			t.Errorf("%s: %v", af.Path, err)
			continue
		}
		if !bytes.Equal(out, b) {
			i := 0
			for i < len(out) && i < len(b) && out[i] == b[i] {
				i++
			}
			t.Errorf("%s: saved %d bytes differ from the %d parsed, from 0x%x", af.Path, len(out), len(b), i)
			continue
		}
		packed, err := f.Bytes(false)
		if err != nil {
			t.Errorf("%s: %v", af.Path, err)
			continue
		}
		g, err := Parse(packed)
		if err != nil || !sameModule(f, g) {
			t.Errorf("%s: packed, parses differently (%v)", af.Path, err)
		}
	}
	t.Logf("%d RELs, versions %v, %d NOP entries", len(a.Files()), versions, nops)

	lst, err := a.ReadFile("mmem/f_pc_profile_lst.rel")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(lst)
	if err != nil {
		t.Fatal(err)
	}
	const puppet = 0x58
	r, err := f.SoleReloc(puppet)
	if err != nil || r.Type != RelocAddr32 {
		t.Fatalf("SoleReloc(0x%x) = %+v, %v", puppet, r, err)
	}
	r.Addend, r.TargetSection = 0x1234, 5
	out, err := f.Bytes(true)
	if err != nil {
		t.Fatal(err)
	}
	diff := 0
	for i := range lst {
		if len(out) != len(lst) || out[i] != lst[i] {
			diff++
		}
	}
	if len(out) != len(lst) || diff == 0 || diff > 8 {
		t.Errorf("%d bytes differ (%d written, %d parsed), want the one entry", diff, len(out), len(lst))
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r2, err := g.SoleReloc(puppet); err != nil || *r2 != *r {
		t.Errorf("after the edit: %+v, %v", r2, err)
	}
}

// Parse never panics, and what it parses saves the same way twice.
func FuzzParse(f *testing.F) {
	f.Add(synthREL())
	f.Add(synthREL()[:0x100])
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := Parse(b)
		if err != nil {
			return
		}
		for _, preserve := range []bool{false, true} {
			out, err := r.Bytes(preserve)
			if err != nil {
				continue
			}
			r2, err := Parse(out)
			if err != nil {
				t.Fatalf("Bytes(%v) doesn't parse: %v", preserve, err)
			}
			again, err := r2.Bytes(preserve)
			if err != nil || !bytes.Equal(again, out) {
				t.Fatalf("Bytes(%v) isn't stable: %v", preserve, err)
			}
		}
	})
}
