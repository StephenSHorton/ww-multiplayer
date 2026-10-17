package stage

import (
	"encoding/binary"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// sampleNames is a small l_objectName for stagetest.MainDOL: two names of
// one proc, a name taken twice (the first wins, as with "Boko" in the
// real table) and every field set.
func sampleNames() []stagetest.ObjectName {
	return []stagetest.ObjectName{
		{Name: "door10", Proc: int16(ProcDoor10), Subtype: -1},
		{Name: "door12", Proc: int16(ProcDoor12), Subtype: 0},
		{Name: "keyS12", Proc: int16(ProcDoor12), Subtype: 1},
		{Name: "Boko", Proc: 0x1D5, Subtype: -1, GBAName: 59},
		{Name: "Bk", Proc: int16(ProcBK), Subtype: -1},
		{Name: "Boko", Proc: int16(ProcDoor12), Subtype: 7},
		{Name: "kusax21", Proc: 0x1B8, Subtype: 2, GBAName: -3},
	}
}

func TestReadObjectNames(t *testing.T) {
	o, err := ReadObjectNames(stagetest.MainDOL(sampleNames()...))
	if err != nil {
		t.Fatal(err)
	}
	if o.Len() != ObjectNameCount || ObjectNameCount != 825 || stagetest.ObjectNameCount != ObjectNameCount ||
		stagetest.ObjectNameAddr != ObjectNameAddr {
		t.Fatalf("Len() = %d, ObjectNameCount %d; stagetest's count %d, address 0x%x",
			o.Len(), ObjectNameCount, stagetest.ObjectNameCount, stagetest.ObjectNameAddr)
	}
	for _, c := range []struct {
		name string
		want ObjectName
		ok   bool
	}{
		{"door10", ObjectName{"door10", ProcDoor10, -1, 0}, true},
		{"keyS12", ObjectName{"keyS12", ProcDoor12, 1, 0}, true},
		{"Boko", ObjectName{"Boko", 0x1D5, -1, 59}, true},
		{"kusax21", ObjectName{"kusax21", 0x1B8, 2, -3}, true},
		{"Pad000", ObjectName{"Pad000", 0, -1, 0}, true},
		{"EndCode", ObjectName{"EndCode", ProcPlayScene, -1, 0}, true},
		{"boko", ObjectName{}, false}, // strcmp: case matters
		{"door10\x00", ObjectName{}, false},
		{"", ObjectName{}, false},
	} {
		got, ok := o.Lookup(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("Lookup(%q) = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.ok)
		}
		if p, ok := o.Proc(c.name); p != c.want.Proc || ok != c.ok {
			t.Errorf("Proc(%q) = %v, %v", c.name, p, ok)
		}
	}
	// ByProc leaves out the second "Boko", which Lookup never returns.
	var names []string
	for _, e := range o.ByProc(ProcDoor12) {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"door12", "keyS12"}) {
		t.Errorf("ByProc(DOOR12) = %q", names)
	}
	list := o.List()
	if len(list) != ObjectNameCount || list[5].Name != "Boko" || list[5].Proc != ProcDoor12 ||
		list[ObjectNameCount-1].Name != "EndCode" {
		t.Errorf("List() = %d entries, [5] %+v, last %+v", len(list), list[5], list[len(list)-1])
	}
	list[0].Name = "changed"
	if o.List()[0].Name != "door10" {
		t.Error("List() shares its backing array")
	}
}

// TestObjectNameEntryLayout checks the entry layout against bytes laid out
// by hand: name[8], s16 proc, s8 subtype, s8 gbaName.
func TestObjectNameEntryLayout(t *testing.T) {
	b := []byte("SW_HIT0\x00\x01\xC9\xFF\x3BBk\x00\x00\x00\x00\x00\x00\x00\xBE\x02\x80")
	o, err := ParseObjectNames(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []ObjectName{{"SW_HIT0", ProcSwhit0, -1, 59}, {"Bk", ProcBK, 2, -128}}
	if got := o.List(); !slices.Equal(got, want) {
		t.Errorf("parsed %+v, want %+v", got, want)
	}
}

func TestReadObjectNamesRejects(t *testing.T) {
	be := binary.BigEndian
	good := stagetest.MainDOL(sampleNames()...)
	table := func(dol []byte) int { // the table's file offset in a MainDOL
		return int(be.Uint32(dol[0x1C:])) + int(ObjectNameAddr-be.Uint32(dol[0x64:]))
	}
	entry := func(i int, raw string) []byte {
		b := slices.Clone(good)
		copy(b[table(b)+i*objectNameEntrySize:], raw)
		return b
	}
	short := slices.Clone(good)
	be.PutUint32(short[0xAC:], be.Uint32(short[0xAC:])-0x30) // the data section ends inside the table
	for _, c := range []struct {
		name, want string
		dol        []byte
	}{
		{"no DOL", "no l_objectName in main.dol", nil},
		{"data elsewhere", "not inside one text or data section", stagetest.DOL(0x80400000, make([]byte, 0x3000))},
		{"section too short", "not inside one text or data section", short},
		{"truncated DOL", "runs past the end", good[:len(good)-0x100]},
		{"unprintable name", "entry 3", entry(3, "Bo\x01o")},
		{"space in a name", "entry 0", entry(0, "door 10\x00")},
		{"unterminated name", "entry 1", entry(1, "door12AB")},
		{"empty name", "entry 2", entry(2, "\x00eyS12")},
		{"proc out of range", "proc 0x01F6", entry(4, "Bk\x00\x00\x00\x00\x00\x00\x01\xF6")},
		{"negative proc", "proc 0xFFFF", entry(4, "Bk\x00\x00\x00\x00\x00\x00\xFF\xFF")},
		{"wrong last entry", `ends in "EndCodf"`, entry(ObjectNameCount-1, "EndCodf")},
		{"last entry's proc", "proc 0x008", entry(ObjectNameCount-1, "EndCode\x00\x00\x08")},
	} {
		o, err := ReadObjectNames(c.dol)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "GZLE01") {
			t.Errorf("%s: ReadObjectNames = %v, %v; want an error naming GZLE01 and containing %q", c.name, o, err, c.want)
		}
	}
	for _, n := range []int{0, 11, 13, 0x26AB} {
		if _, err := ParseObjectNames(make([]byte, n)); err == nil {
			t.Errorf("ParseObjectNames(%d bytes) succeeded", n)
		}
	}
}

// TestProcs pins the proc numbers §7.2 lists (from research briefs)
// against the constants, which come from the fpcNm enum: they agree.
func TestProcs(t *testing.T) {
	design := map[Proc]Proc{
		ProcBK: 0xBE, ProcMO2: 0xBD, ProcST: 0xBF, ProcRD: 0xE1, ProcCC: 0xCF, ProcKI: 0xD8, ProcAM: 0xCB,
		ProcAM2: 0xCC, ProcBB: 0xB6, ProcFM: 0x79, ProcKS: 0xCE, ProcTN: 0xC0, ProcPH: 0xD2, ProcPT: 0xF9,
		ProcPW: 0xD5, ProcBO: 0xD7,
	}
	for c, v := range design {
		if c != v {
			t.Errorf("%v = 0x%03X, §7.2 says 0x%03X", c, uint16(c), uint16(v))
		}
	}
	seen := map[Proc]bool{}
	for p, name := range procNames {
		if p < 0 || p >= ProcMaxNum || seen[p] || name == "" {
			t.Errorf("proc %v (0x%03X): out of range or repeated", name, uint16(p))
		}
		seen[p] = true
		if p.String() != name {
			t.Errorf("String() = %q, want %q", p.String(), name)
		}
	}
	// Every Proc constant but ProcMaxNum has a name.
	f, err := parser.ParseFile(token.NewFileSet(), "objname.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := 0
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, s := range g.Specs {
				if v := s.(*ast.ValueSpec); v.Type != nil && fmt.Sprint(v.Type) == "Proc" && v.Names[0].Name != "ProcMaxNum" {
					consts++
				}
			}
		}
	}
	if consts != len(procNames) || consts < 50 {
		t.Errorf("%d Proc constants, %d named in procNames", consts, len(procNames))
	}
	if got := Proc(0x1B8).String(); got != "proc 0x1B8" {
		t.Errorf("unnamed proc String() = %q", got)
	}
}
