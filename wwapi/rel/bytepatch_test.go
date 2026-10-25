package rel

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// patchFixture is a small REL with a .text of words, a .data and a .bss,
// and relocations against main.dol, against itself and against another
// module: two REL24s on bls, an ADDR16_HA/LO pair, an ADDR32 in .data,
// one in the .bss (nothing in the file) and a NOP marker.
func patchFixture(t *testing.T) []byte {
	t.Helper()
	text := make([]byte, 0x40)
	for i := 0; i < len(text); i += 4 {
		copy(text[i:], []byte{0x60, 0x00, 0x00, byte(i)}) // distinct words
	}
	copy(text[0x08:], []byte{0x48, 0x00, 0x00, 0x01}) // bl (main.dol)
	copy(text[0x10:], []byte{0x38, 0xA0, 0x00, 0x00}) // li r5, 0
	copy(text[0x14:], []byte{0x48, 0x00, 0x00, 0x01}) // bl (main.dol)
	f := &File{
		ID: 0x77, Version: 3, Align: 8, BSSAlign: 8, BSSSize: 0x20,
		Prolog: Symbol{Section: 1}, Epilog: Symbol{Section: 1, Offset: 4}, Unresolved: Symbol{Section: 1, Offset: 8},
		Sections: []Section{
			{},
			{Offset: 1, Exec: true, Data: text},
			{Offset: 1, Data: bytes.Repeat([]byte{0xAB}, 0x10)},
			{Size: 0x20},
		},
		Imports: []Import{
			{Module: 0x42, Relocs: []Reloc{{Section: 2, Offset: 0x8, Type: RelocAddr32, TargetSection: 2, Addend: 0x10}}},
			{Module: 0x77, Relocs: []Reloc{
				{Section: 1, Offset: 0x1A, Type: RelocAddr16Ha, TargetSection: 2, Addend: 4},
				{Section: 1, Offset: 0x1E, Type: RelocAddr16Lo, TargetSection: 2, Addend: 4},
				{Section: 3, Offset: 0x0, Type: RelocAddr32, TargetSection: 2, Addend: 0},
			}},
			{Module: 0, Relocs: []Reloc{
				{Section: 1, Offset: 0x08, Type: RelocRel24, Addend: 0x802B0518},
				{Section: 1, Offset: 0x0C, Type: RelocNop},
				{Section: 1, Offset: 0x14, Type: RelocRel24, Addend: 0x80024D24},
				{Section: 2, Offset: 0x0, Type: RelocAddr32, Addend: 0x80005678},
			}},
		},
	}
	b, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var (
	nop   = []byte{0x60, 0x00, 0x00, 0x00}
	bl    = []byte{0x48, 0x00, 0x00, 0x01}
	liR5  = []byte{0x38, 0xA0, 0x00, 0x00}
	liEF0 = []byte{0x38, 0xA0, 0x0E, 0xF0}
)

// The patch writes its bytes, disables exactly the relocations under
// them (the REL24 under the nop, the ADDR16_LO half-word a write only
// touches the end of), keeps every other byte, and the result parses
// with the same relocation offsets: a NOP keeps its delta. Applying it
// again is Already and gives the same bytes back.
func TestPatchBytes(t *testing.T) {
	b := patchFixture(t)
	before, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	writes := []Write{
		{Section: 1, Offset: 0x08, Old: bl, New: nop},
		{Section: 1, Offset: 0x10, Old: liR5, New: liEF0},
		{Section: 1, Offset: 0x1C, Old: []byte{0x60, 0x00, 0x00, 0x1C}, New: []byte{0x60, 0x00, 0x11, 0x1C}}, // the ADDR16_LO at 0x1E, not the HA at 0x1A
	}
	out, st, err := PatchBytes(b, writes)
	if err != nil {
		t.Fatal(err)
	}
	if st.Already || st.Disabled != 2 {
		t.Fatalf("stats %+v, want 2 disabled", st)
	}
	if len(out) != len(b) {
		t.Fatalf("size %d, was %d", len(out), len(b))
	}
	after, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	text := after.Sections[1].Offset
	for i := range b {
		changed := out[i] != b[i]
		inWrite := false
		for _, w := range writes {
			s := uint32(text) + w.Offset
			if uint32(i) >= s && uint32(i) < s+uint32(len(w.New)) {
				inWrite = true
			}
		}
		if changed && !inWrite && out[i] != byte(RelocNop) {
			t.Errorf("byte 0x%x changed 0x%02x -> 0x%02x outside the writes", i, b[i], out[i])
		}
	}
	type key struct {
		mod uint32
		sec uint8
		off uint32
	}
	types := map[key]RelocType{}
	for _, imp := range after.Imports {
		for _, r := range imp.Relocs {
			types[key{imp.Module, r.Section, r.Offset}] = r.Type
		}
	}
	for _, imp := range before.Imports {
		for _, r := range imp.Relocs {
			k := key{imp.Module, r.Section, r.Offset}
			got, ok := types[k]
			if !ok {
				t.Errorf("relocation %+v moved", k)
				continue
			}
			want := r.Type
			if k == (key{0, 1, 0x08}) || k == (key{0x77, 1, 0x1E}) {
				want = RelocNop
			}
			if got != want {
				t.Errorf("relocation %+v: type 0x%x, want 0x%x", k, got, want)
			}
		}
	}
	if !bytes.Equal(after.Sections[1].Data[0x10:0x14], liEF0) {
		t.Errorf("li r5 not written: %x", after.Sections[1].Data[0x10:0x14])
	}

	again, st, err := PatchBytes(out, writes)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Already || st.Disabled != 0 || !bytes.Equal(again, out) {
		t.Errorf("second apply: %+v, same bytes %v", st, bytes.Equal(again, out))
	}
	if bytes.Equal(b, out) {
		t.Error("the input was modified")
	}
}

// Everything PatchBytes refuses, with nothing written.
func TestPatchBytesRejects(t *testing.T) {
	b := patchFixture(t)
	for _, c := range []struct {
		name   string
		in     []byte
		writes []Write
		want   string
	}{
		{"compressed", stage.EncodeYaz0(b), []Write{{Section: 1, Offset: 8, Old: bl, New: nop}}, "uncompressed"},
		{"bss", b, []Write{{Section: 3, Offset: 0, Old: nop, New: bl}}, "no data"},
		{"no such section", b, []Write{{Section: 9, Offset: 0, Old: nop, New: bl}}, "no data"},
		{"past the section", b, []Write{{Section: 1, Offset: 0x3E, Old: nop, New: bl}}, "runs past"},
		{"length", b, []Write{{Section: 1, Offset: 8, Old: bl, New: nop[:2]}}, "the same"},
		{"empty", b, []Write{{Section: 1, Offset: 8}}, "the same"},
		{"overlap", b, []Write{{Section: 1, Offset: 8, Old: bl, New: nop}, {Section: 1, Offset: 0xA, Old: bl[2:], New: nop[2:]}}, "overlap"},
		{"wrong vanilla", b, []Write{{Section: 1, Offset: 0x10, Old: bl, New: nop}}, "neither"},
		{"half applied", func() []byte {
			o, _, err := PatchBytes(b, []Write{{Section: 1, Offset: 8, Old: bl, New: nop}})
			if err != nil {
				t.Fatal(err)
			}
			return o
		}(), []Write{{Section: 1, Offset: 8, Old: bl, New: nop}, {Section: 1, Offset: 0x14, Old: bl, New: nop}}, "in part"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, _, err := PatchBytes(c.in, c.writes)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if out != nil {
				t.Error("bytes returned with the error")
			}
		})
	}
}

// A REL whose relocation under a write is already R_DOLPHIN_NOP (a
// patch that disabled it, with the bytes then put back by hand) is
// patched with that one left alone, and a REL patched by bytes alone
// (the relocation still live) has it disabled on the Already pass.
func TestPatchBytesAlreadyDisablesLiveRelocs(t *testing.T) {
	b := patchFixture(t)
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	byHand := bytes.Clone(b)
	copy(byHand[f.Sections[1].Offset+0x14:], nop)
	out, st, err := PatchBytes(byHand, []Write{{Section: 1, Offset: 0x14, Old: bl, New: nop}})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Already || st.Disabled != 1 {
		t.Fatalf("stats %+v, want Already with 1 disabled", st)
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, idx := g.RelocAt(1, 0x14); idx >= 0 {
		t.Error("the relocation under the nop is still live")
	}
}
