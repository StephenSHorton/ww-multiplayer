package rel

import (
	"strings"
	"testing"
)

func TestRelocAt(t *testing.T) {
	f := synthFile()
	imp, i := f.RelocAt(2, 4)
	if imp == nil || imp.Module != 0x10 || i != 0 {
		t.Fatalf("RelocAt(2, 4) = %+v %d", imp, i)
	}
	// The first of several at one place, in file order.
	if imp, i := f.RelocAt(1, 0xC); imp == nil || imp.Module != 0x42 || i != 1 {
		t.Errorf("RelocAt(1, 0xC) = %+v %d", imp, i)
	}
	// A NOP marker is not a relocation; nothing there is nil.
	for _, at := range [][2]uint32{{1, 0x100C}, {1, 8}, {3, 0}} {
		if imp, i := f.RelocAt(uint8(at[0]), at[1]); imp != nil || i != -1 {
			t.Errorf("RelocAt(%d, 0x%x) = %+v %d", at[0], at[1], imp, i)
		}
	}
}

func TestMoveReloc(t *testing.T) {
	// Into a new module: appended, the source keeps its other two.
	f := synthFile()
	imp, i := f.RelocAt(1, 4)
	r, err := f.MoveReloc(imp, i, 0x58)
	if err != nil || r == nil || *r != (Reloc{Section: 1, Offset: 4, Type: RelocAddr32, TargetSection: 2, Addend: 0x20}) {
		t.Fatalf("move: %+v %v", r, err)
	}
	r.TargetSection, r.Addend = 4, 0x30
	if len(f.Imports) != 4 || f.Imports[3].Module != 0x58 || len(f.Import(0x42).Relocs) != 2 || f.Imports[3].Relocs[0].Addend != 0x30 {
		t.Fatalf("imports %+v", f.Imports)
	}
	// It writes and parses back with the move.
	b, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := g.SoleReloc(0x58); err != nil || *got != (Reloc{Section: 1, Offset: 4, Type: RelocAddr32, TargetSection: 4, Addend: 0x30}) {
		t.Errorf("parsed back: %+v %v", got, err)
	}

	// Emptying a module drops its import; into an existing one appends.
	f = synthFile()
	for range 3 {
		if _, err := f.MoveReloc(f.Import(0x42), 0, 0x10); err != nil {
			t.Fatal(err)
		}
	}
	if f.Import(0x42) != nil || len(f.Imports) != 2 || len(f.Import(0x10).Relocs) != 4 {
		t.Errorf("after emptying 0x42: %+v", f.Imports)
	}
	// Module 0 and the file's own module stay even when emptied.
	f = synthFile()
	if _, err := f.MoveReloc(f.Import(0x10), 0, 0x42); err != nil || f.Import(0x10) == nil || len(f.Import(0x10).Relocs) != 0 {
		t.Errorf("own module dropped: %+v %v", f.Imports, err)
	}
	f = synthFile()
	for _, idx := range []int{3, 1, 0} { // leaves only the NOP marker
		if _, err := f.MoveReloc(f.Import(0), idx, 0x42); err != nil {
			t.Fatal(err)
		}
	}
	if f.Import(0) == nil {
		t.Error("module 0 dropped")
	}
	// A source with only a NOP left is dropped.
	f = synthFile()
	f.Imports = append(f.Imports, Import{Module: 0x77, Relocs: []Reloc{{Section: 1, Offset: 0x10, Type: RelocNop}, {Section: 1, Offset: 0x20, Type: RelocAddr32}}})
	if _, err := f.MoveReloc(&f.Imports[3], 1, 0x42); err != nil || f.Import(0x77) != nil {
		t.Errorf("NOP-only import kept: %+v %v", f.Imports, err)
	}

	// Same module: in place.
	f = synthFile()
	if r, err := f.MoveReloc(f.Import(0x42), 2, 0x42); err != nil || r != &f.Import(0x42).Relocs[2] {
		t.Errorf("same module: %v", err)
	}
	// Errors.
	f = synthFile()
	other := Import{Module: 0x42, Relocs: []Reloc{{}}}
	for name, c := range map[string]struct {
		imp  *Import
		idx  int
		want string
	}{
		"foreign import": {&other, 0, "not one of the file's"},
		"index":          {f.Import(0x42), 3, "no relocation 3"},
		"negative":       {f.Import(0x42), -1, "no relocation -1"},
		"nop":            {f.Import(0), 2, "NOP marker"},
	} {
		if _, err := f.MoveReloc(c.imp, c.idx, 0x58); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
