package rel

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A relocatable ELF built in the test, the way powerpc-eabi-ld
// --relocatable lays one out: the null section, the unit's sections in
// order, a .rela<name> per section that has relocations, then .symtab,
// .strtab and .shstrtab. Sections and symbols are referred to by name so
// the cases read like a linker map.

type elfSection struct {
	name  string
	typ   elf.SectionType
	flags elf.SectionFlag
	data  []byte
	size  uint32 // for a NOBITS section
}

type elfSym struct {
	name    string
	section string // a section name, "ABS", "UNDEF" or "COMMON"
	value   uint32
	bind    elf.SymBind
	typ     elf.SymType
}

type elfRela struct {
	section string // the section patched
	off     uint32
	sym     string
	typ     elf.R_PPC
	addend  int32
}

type elfSpec struct {
	sections []elfSection
	syms     []elfSym
	relas    []elfRela
	class    elf.Class   // 0 = ELFCLASS32
	data     elf.Data    // 0 = ELFDATA2MSB
	etype    elf.Type    // 0 = ET_REL
	machine  elf.Machine // 0 = EM_PPC
}

func (s elfSpec) build(t testing.TB) []byte {
	t.Helper()
	type hdr struct{ name, typ, flags, addr, off, size, link, info, align, entsize uint32 }
	var shstr, strtab []byte
	str := func(tab *[]byte, s string) uint32 {
		off := uint32(len(*tab))
		*tab = append(append(*tab, s...), 0)
		return off
	}
	str(&shstr, "")
	str(&strtab, "")

	// Section indices: 0 null, then the spec's, then one .rela per
	// relocated section, then .symtab, .strtab, .shstrtab.
	index := map[string]int{}
	for i, sec := range s.sections {
		index[sec.name] = 1 + i
	}
	var relaFor []string // sections with relocations, in section order
	for _, sec := range s.sections {
		if slices.ContainsFunc(s.relas, func(r elfRela) bool { return r.section == sec.name }) {
			relaFor = append(relaFor, sec.name)
		}
	}
	symtabIdx := 1 + len(s.sections) + len(relaFor)
	strtabIdx, shstrIdx := symtabIdx+1, symtabIdx+2
	nsec := shstrIdx + 1

	// Symbols: the null one, then the spec's.
	symIndex := map[string]uint32{}
	symtab := make([]byte, 16)
	firstGlobal := uint32(1)
	for i, sym := range s.syms {
		symIndex[sym.name] = uint32(1 + i)
		if sym.bind == elf.STB_LOCAL {
			firstGlobal = uint32(2 + i)
		}
		e := make([]byte, 16)
		binary.BigEndian.PutUint32(e, str(&strtab, sym.name))
		binary.BigEndian.PutUint32(e[4:], sym.value)
		e[12] = byte(sym.bind)<<4 | byte(sym.typ)
		var shndx uint16
		switch sym.section {
		case "ABS":
			shndx = uint16(elf.SHN_ABS)
		case "UNDEF":
			shndx = 0
		case "COMMON":
			shndx = uint16(elf.SHN_COMMON)
		default:
			i, ok := index[sym.section]
			if !ok {
				t.Fatalf("symbol %s: no section %s", sym.name, sym.section)
			}
			shndx = uint16(i)
		}
		binary.BigEndian.PutUint16(e[14:], shndx)
		symtab = append(symtab, e...)
	}
	rela := func(section string) []byte {
		var out []byte
		for _, r := range s.relas {
			if r.section != section {
				continue
			}
			si, ok := symIndex[r.sym]
			if !ok {
				t.Fatalf("relocation at %s+0x%x: no symbol %s", r.section, r.off, r.sym)
			}
			out = binary.BigEndian.AppendUint32(out, r.off)
			out = binary.BigEndian.AppendUint32(out, si<<8|uint32(r.typ))
			out = binary.BigEndian.AppendUint32(out, uint32(r.addend))
		}
		return out
	}

	hdrs := make([]hdr, nsec)
	body := make([]byte, 52) // the ELF header
	place := func(i int, h hdr, data []byte, nobits bool) {
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		h.off = uint32(len(body))
		if !nobits {
			h.size = uint32(len(data))
			body = append(body, data...)
		}
		// A NOBITS section's offset is where the next section's bytes
		// land, as in a real file: reading sh_size bytes there is wrong.
		hdrs[i] = h
	}
	for i, sec := range s.sections {
		h := hdr{name: str(&shstr, sec.name), typ: uint32(sec.typ), flags: uint32(sec.flags), align: 4}
		if sec.typ == elf.SHT_NOBITS {
			h.size = sec.size
		}
		place(1+i, h, sec.data, sec.typ == elf.SHT_NOBITS)
	}
	for k, name := range relaFor {
		h := hdr{name: str(&shstr, ".rela"+name), typ: uint32(elf.SHT_RELA), link: uint32(symtabIdx), info: uint32(index[name]), align: 4, entsize: 12}
		place(1+len(s.sections)+k, h, rela(name), false)
	}
	place(symtabIdx, hdr{name: str(&shstr, ".symtab"), typ: uint32(elf.SHT_SYMTAB), link: uint32(strtabIdx), info: firstGlobal, align: 4, entsize: 16}, symtab, false)
	place(strtabIdx, hdr{name: str(&shstr, ".strtab"), typ: uint32(elf.SHT_STRTAB), align: 1}, strtab, false)
	shstrName := str(&shstr, ".shstrtab")
	place(shstrIdx, hdr{name: shstrName, typ: uint32(elf.SHT_STRTAB), align: 1}, shstr, false)

	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	shoff := len(body)
	for _, h := range hdrs {
		for _, v := range []uint32{h.name, h.typ, h.flags, h.addr, h.off, h.size, h.link, h.info, h.align, h.entsize} {
			body = binary.BigEndian.AppendUint32(body, v)
		}
	}

	class, data, etype, machine := s.class, s.data, s.etype, s.machine
	if class == 0 {
		class = elf.ELFCLASS32
	}
	if data == 0 {
		data = elf.ELFDATA2MSB
	}
	if etype == 0 {
		etype = elf.ET_REL
	}
	if machine == 0 {
		machine = elf.EM_PPC
	}
	copy(body, "\x7fELF")
	body[4], body[5], body[6] = byte(class), byte(data), 1
	be := binary.BigEndian
	be.PutUint16(body[16:], uint16(etype))
	be.PutUint16(body[18:], uint16(machine))
	be.PutUint32(body[20:], 1)
	be.PutUint32(body[32:], uint32(shoff))
	be.PutUint16(body[40:], 52)
	be.PutUint16(body[46:], 40)
	be.PutUint16(body[48:], uint16(nsec))
	be.PutUint16(body[50:], uint16(shstrIdx))
	return body
}

// puppetELF is a module like the puppet REL in miniature: a prolog that
// calls the game (a bl to an absolute symbol), loads the address of its
// profile in .rodata (lis/addi, a HA/LO pair), touches a .bss counter
// and the mailbox (absolute, with an addend), branches to a local
// function, and a .data table of pointers: to _prolog, into the game,
// and into .rodata through the section symbol. .comment and .debug_info
// (with relocations of its own) are not copied; .ctors is empty.
func puppetELF() elfSpec {
	text := bytes.Repeat([]byte{0x60, 0, 0, 0}, 0x10) // 0x40 bytes of nop
	return elfSpec{
		sections: []elfSection{
			{name: ".text", typ: elf.SHT_PROGBITS, flags: elf.SHF_ALLOC | elf.SHF_EXECINSTR, data: text},
			{name: ".rodata", typ: elf.SHT_PROGBITS, flags: elf.SHF_ALLOC, data: []byte("rodata-section-of-0x20-bytes...!")},
			{name: ".data", typ: elf.SHT_PROGBITS, flags: elf.SHF_ALLOC | elf.SHF_WRITE, data: make([]byte, 12)},
			{name: ".bss", typ: elf.SHT_NOBITS, flags: elf.SHF_ALLOC | elf.SHF_WRITE, size: 0x18},
			{name: ".comment", typ: elf.SHT_PROGBITS, data: []byte("GCC: (devkitPPC) 15.1.0\x00")},
			{name: ".sbss", typ: elf.SHT_NOBITS, flags: elf.SHF_ALLOC | elf.SHF_WRITE, size: 4},
			{name: ".debug_info", typ: elf.SHT_PROGBITS, data: make([]byte, 8)},
			{name: ".ctors", typ: elf.SHT_PROGBITS, flags: elf.SHF_ALLOC | elf.SHF_WRITE},
		},
		syms: []elfSym{
			{name: "puppet.c", section: "ABS", typ: elf.STT_FILE},
			{name: ".text", section: ".text", typ: elf.STT_SECTION},
			{name: ".rodata", section: ".rodata", typ: elf.STT_SECTION},
			{name: "counter", section: ".bss", value: 8, typ: elf.STT_OBJECT},
			{name: "helper", section: ".text", value: 0x24, typ: elf.STT_FUNC},
			{name: "_ctors", section: ".ctors", typ: elf.STT_NOTYPE},
			{name: "_prolog", section: ".text", value: 0, bind: elf.STB_GLOBAL, typ: elf.STT_FUNC},
			{name: "_epilog", section: ".text", value: 0x10, bind: elf.STB_GLOBAL, typ: elf.STT_FUNC},
			{name: "_unresolved", section: ".text", value: 0x20, bind: elf.STB_GLOBAL, typ: elf.STT_FUNC},
			{name: "g_profile_PUPPET", section: ".rodata", value: 0x10, bind: elf.STB_GLOBAL, typ: elf.STT_OBJECT},
			{name: "OSReportEnable", section: "ABS", value: 0x8030A0C0, bind: elf.STB_GLOBAL, typ: elf.STT_NOTYPE},
			{name: "g_mailbox", section: "ABS", value: 0x80412F00, bind: elf.STB_GLOBAL, typ: elf.STT_NOTYPE},
			{name: "g_playerPtr", section: "ABS", value: 0x803CA754, bind: elf.STB_GLOBAL, typ: elf.STT_NOTYPE},
		},
		relas: []elfRela{
			{section: ".text", off: 0x00, sym: "OSReportEnable", typ: elf.R_PPC_REL24},
			{section: ".text", off: 0x06, sym: "g_profile_PUPPET", typ: elf.R_PPC_ADDR16_HA},
			{section: ".text", off: 0x0A, sym: "g_profile_PUPPET", typ: elf.R_PPC_ADDR16_LO},
			{section: ".text", off: 0x0E, sym: "counter", typ: elf.R_PPC_ADDR16_LO, addend: 4},
			{section: ".text", off: 0x12, sym: "g_mailbox", typ: elf.R_PPC_ADDR16_HA, addend: 0x1B0},
			{section: ".text", off: 0x16, sym: "g_mailbox", typ: elf.R_PPC_ADDR16_LO, addend: 0x1B0},
			{section: ".text", off: 0x30, sym: "helper", typ: elf.R_PPC_REL24},
			{section: ".data", off: 0x08, sym: ".rodata", typ: elf.R_PPC_ADDR32, addend: 0x10},
			{section: ".data", off: 0x00, sym: "_prolog", typ: elf.R_PPC_ADDR32},
			{section: ".data", off: 0x04, sym: "g_playerPtr", typ: elf.R_PPC_ADDR32},
			{section: ".debug_info", off: 0, sym: "_prolog", typ: elf.R_PPC_ADDR32},
		},
	}
}

func TestFromELF(t *testing.T) {
	spec := puppetELF()
	f, lay, err := FromELF(spec.build(t), 0x58, ELFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Header and sections: the ELF's null section, then the copied ones
	// in ELF order; .bss and .sbss zero-filled data, .ctors empty.
	if f.ID != 0x58 || f.Version != 3 || f.Align != 8 || f.BSSAlign != 1 || f.BSSSize != 0 || f.NameOffset != 0 || f.NameSize != 0 {
		t.Errorf("header: %+v", *f)
	}
	if f.Prolog != (Symbol{1, 0}) || f.Epilog != (Symbol{1, 0x10}) || f.Unresolved != (Symbol{1, 0x20}) {
		t.Errorf("entry points: %v %v %v", f.Prolog, f.Epilog, f.Unresolved)
	}
	wantNames := []string{"", ".text", ".rodata", ".data", ".bss", ".sbss", ".ctors"}
	if got := lay.SectionNames(); !slices.Equal(got, wantNames) {
		t.Errorf("sections %q, want %q", got, wantNames)
	}
	for name, want := range map[string]uint8{".text": 1, ".rodata": 2, ".data": 3, ".bss": 4, ".sbss": 5, ".ctors": 6} {
		if got, ok := lay.Section(name); !ok || got != want {
			t.Errorf("Section(%s) = %d, %v; want %d", name, got, ok, want)
		}
	}
	for _, name := range []string{".comment", ".debug_info", ".rela.text", ".symtab", ""} {
		if _, ok := lay.Section(name); ok {
			t.Errorf("Section(%q) found", name)
		}
	}
	type sec struct {
		exec bool
		data []byte
	}
	wantSecs := []sec{
		{}, {exec: true, data: spec.sections[0].data}, {data: spec.sections[1].data}, {data: make([]byte, 12)},
		{data: make([]byte, 0x18)}, {data: make([]byte, 4)}, {},
	}
	if len(f.Sections) != len(wantSecs) {
		t.Fatalf("%d sections, want %d", len(f.Sections), len(wantSecs))
	}
	for i, w := range wantSecs {
		s := f.Sections[i]
		if s.Exec != w.exec || !bytes.Equal(s.Data, w.data) || (s.Offset == 0) != (w.data == nil) || s.BSS() {
			t.Errorf("section %d = exec %v, offset 0x%x, %d bytes; want exec %v, %d bytes", i, s.Exec, s.Offset, len(s.Data), w.exec, len(w.data))
		}
	}

	// Symbols in copied sections are known, with their REL section.
	for name, want := range map[string]Symbol{
		"_prolog": {1, 0}, "helper": {1, 0x24}, "g_profile_PUPPET": {2, 0x10}, "counter": {4, 8}, "_ctors": {6, 0},
	} {
		sec, off, ok := lay.Symbol(name)
		if !ok || sec != want.Section || off != want.Offset {
			t.Errorf("Symbol(%s) = %d, 0x%x, %v; want %v", name, sec, off, ok, want)
		}
	}
	for _, name := range []string{"OSReportEnable", "g_mailbox", "puppet.c", ".text", "", "nothing"} {
		if _, _, ok := lay.Symbol(name); ok {
			t.Errorf("Symbol(%q) found", name)
		}
	}

	// Relocations: against the module itself and against module 0, in
	// (section, offset) order as Bytes writes them; the ones in
	// .debug_info are dropped with the section.
	want := []Import{
		{Module: 0x58, Relocs: []Reloc{
			{Section: 1, Offset: 0x06, Type: RelocAddr16Ha, TargetSection: 2, Addend: 0x10},
			{Section: 1, Offset: 0x0A, Type: RelocAddr16Lo, TargetSection: 2, Addend: 0x10},
			{Section: 1, Offset: 0x0E, Type: RelocAddr16Lo, TargetSection: 4, Addend: 0xC},
			{Section: 1, Offset: 0x30, Type: RelocRel24, TargetSection: 1, Addend: 0x24},
			{Section: 3, Offset: 0x00, Type: RelocAddr32, TargetSection: 1, Addend: 0},
			{Section: 3, Offset: 0x08, Type: RelocAddr32, TargetSection: 2, Addend: 0x10},
		}},
		{Module: 0, Relocs: []Reloc{
			{Section: 1, Offset: 0x00, Type: RelocRel24, Addend: 0x8030A0C0},
			{Section: 1, Offset: 0x12, Type: RelocAddr16Ha, Addend: 0x804130B0},
			{Section: 1, Offset: 0x16, Type: RelocAddr16Lo, Addend: 0x804130B0},
			{Section: 3, Offset: 0x04, Type: RelocAddr32, Addend: 0x803CA754},
		}},
	}
	got := make([]Import, len(f.Imports))
	for i, imp := range f.Imports {
		got[i] = Import{Module: imp.Module, Relocs: slices.Clone(imp.Relocs)}
		slices.SortFunc(got[i].Relocs, func(a, b Reloc) int {
			if a.Section != b.Section {
				return int(a.Section) - int(b.Section)
			}
			return int(a.Offset) - int(b.Offset)
		})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("imports =\n%+v\nwant\n%+v", got, want)
	}

	// The REL written parses back to the same module, offsets included
	// (FromELF records what Bytes(false) lays out), and writes the same
	// way again.
	out, err := f.Bytes(false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Parse(out)
	if err != nil {
		t.Fatalf("the REL doesn't parse: %v", err)
	}
	if !reflect.DeepEqual(g, f) {
		t.Errorf("Parse(Bytes()) =\n%+v\nwant\n%+v", g, f)
	}
	if again, err := g.Bytes(false); err != nil || !bytes.Equal(again, out) {
		t.Errorf("the parsed REL writes differently: %v", err)
	}
	if again, err := g.Bytes(true); err != nil || !bytes.Equal(again, out) {
		t.Errorf("the parsed REL writes differently with its offsets kept: %v", err)
	}
	if g.FixSize == 0 || int(g.FixSize) > len(out) {
		t.Errorf("fix size 0x%x of %d bytes", g.FixSize, len(out))
	}
}

// Options: the version and alignments are written, and the section list
// can be changed (a .sdata2 the default leaves out).
func TestFromELFOptions(t *testing.T) {
	spec := puppetELF()
	spec.sections = append(spec.sections, elfSection{name: ".sdata2", typ: elf.SHT_PROGBITS, flags: elf.SHF_ALLOC, data: []byte{1, 2, 3, 4}})
	spec.syms = append(spec.syms, elfSym{name: "k", section: ".sdata2", bind: elf.STB_GLOBAL, typ: elf.STT_OBJECT})
	spec.relas = append(spec.relas, elfRela{section: ".text", off: 0x3C, sym: "k", typ: elf.R_PPC_ADDR32})
	b := spec.build(t)
	if _, _, err := FromELF(b, 0x58, ELFOptions{}); err == nil || !strings.Contains(err.Error(), `symbol "k" is in section .sdata2, which isn't copied`) {
		t.Errorf("with the default sections: %v", err)
	}
	opts := ELFOptions{Sections: append(slices.Clone(AllowedSections), ".sdata2"), Version: 2, Align: 32, BSSAlign: 8}
	f, lay, err := FromELF(b, 0x59, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sec, off, ok := lay.Symbol("k"); !ok || sec != 7 || off != 0 {
		t.Errorf("Symbol(k) = %d, 0x%x, %v", sec, off, ok)
	}
	if f.ID != 0x59 || f.Version != 2 || f.Align != 32 || f.BSSAlign != 8 || f.FixSize != 0 {
		t.Errorf("header: %+v", *f)
	}
	out, _ := f.Bytes(false)
	f.FixSize = 0 // Bytes records it for every version; Parse reads it for 3 only
	if g, err := Parse(out); err != nil || g.Version != 2 || g.Align != 32 || !reflect.DeepEqual(g, f) {
		t.Errorf("Parse(Bytes()) = %+v, %v\nwant %+v", g, err, f)
	}
	if _, _, err := FromELF(b, 0x58, ELFOptions{Version: 4}); err == nil || !strings.Contains(err.Error(), "unknown format version 4") {
		t.Errorf("version 4: %v", err)
	}
}

// A global symbol's definition wins over a local of the same name
// (static functions of one name in two units); between locals, the
// first.
func TestFromELFSymbolClash(t *testing.T) {
	spec := puppetELF()
	spec.syms = append(spec.syms,
		elfSym{name: "tick", section: ".text", value: 0x28, typ: elf.STT_FUNC},
		elfSym{name: "tick", section: ".text", value: 0x2C, typ: elf.STT_FUNC},
		elfSym{name: "shared", section: ".text", value: 0x30, typ: elf.STT_FUNC},
		elfSym{name: "shared", section: ".data", value: 4, bind: elf.STB_GLOBAL, typ: elf.STT_OBJECT},
		elfSym{name: "g_profile_PUPPET", section: ".data", value: 8, typ: elf.STT_OBJECT}, // a local after the global
	)
	_, lay, err := FromELF(spec.build(t), 0x58, ELFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]Symbol{"tick": {1, 0x28}, "shared": {3, 4}, "g_profile_PUPPET": {2, 0x10}} {
		sec, off, ok := lay.Symbol(name)
		if !ok || sec != want.Section || off != want.Offset {
			t.Errorf("Symbol(%s) = %d, 0x%x, %v; want %v", name, sec, off, ok, want)
		}
	}
}

func TestFromELFRejects(t *testing.T) {
	with := func(f func(s *elfSpec)) []byte {
		s := puppetELF()
		f(&s)
		return s.build(t)
	}
	rela := func(r elfRela) func(s *elfSpec) {
		return func(s *elfSpec) { s.relas = append(s.relas, r) }
	}
	cases := []struct {
		name    string
		elf     []byte
		module  uint32
		wantErr string
	}{
		{"unresolved symbol", with(func(s *elfSpec) {
			s.syms = append(s.syms, elfSym{name: "missing_fn", section: "UNDEF", bind: elf.STB_GLOBAL})
			rela(elfRela{section: ".text", off: 0x38, sym: "missing_fn", typ: elf.R_PPC_REL24})(s)
		}), 0x58, `.text+0x38: unresolved symbol "missing_fn"`},
		{"REL32 (a jump table)", with(rela(elfRela{section: ".rodata", off: 0, sym: "helper", typ: elf.R_PPC_REL32})), 0x58,
			".rodata+0x0: relocation type R_PPC_REL32 can't be written to a REL (a jump table: compile with -fno-jump-tables)"},
		{"small-data relocation", with(rela(elfRela{section: ".text", off: 0x38, sym: "counter", typ: elf.R_PPC_EMB_SDA21})), 0x58,
			"relocation type R_PPC_EMB_SDA21 can't be written"},
		{"no _prolog", with(func(s *elfSpec) { s.syms[6].section = "UNDEF" }), 0x58, "no _prolog symbol"},
		{"no _epilog", with(func(s *elfSpec) { s.syms[7].section = "ABS" }), 0x58, "no _epilog symbol"},
		{"no _unresolved", with(func(s *elfSpec) { s.syms[8].section = "UNDEF" }), 0x58, "no _unresolved symbol"},
		{"symbol in a section not copied", with(func(s *elfSpec) {
			s.syms = append(s.syms, elfSym{name: "version", section: ".comment", bind: elf.STB_GLOBAL, typ: elf.STT_OBJECT})
			rela(elfRela{section: ".data", off: 0, sym: "version", typ: elf.R_PPC_ADDR32})(s)
		}), 0x58, `.data+0x0: symbol "version" is in section .comment, which isn't copied into the REL`},
		{"symbol in an empty section", with(rela(elfRela{section: ".data", off: 0, sym: "_ctors", typ: elf.R_PPC_ADDR32})), 0x58,
			`symbol "_ctors" is in section .ctors, which is empty`},
		{"common symbol", with(func(s *elfSpec) {
			s.syms = append(s.syms, elfSym{name: "shared_buf", section: "COMMON", value: 4, bind: elf.STB_GLOBAL, typ: elf.STT_OBJECT})
			rela(elfRela{section: ".data", off: 0, sym: "shared_buf", typ: elf.R_PPC_ADDR32})(s)
		}), 0x58, `symbol "shared_buf" has special section index SHN_COMMON`},
		{"relocation past the section", with(rela(elfRela{section: ".data", off: 10, sym: "_prolog", typ: elf.R_PPC_ADDR32})), 0x58,
			".data+0xa: relocation past the end of the section (0xc bytes)"},
		{"halfword relocation past the section", with(rela(elfRela{section: ".data", off: 11, sym: "_prolog", typ: elf.R_PPC_ADDR16_LO})), 0x58,
			".data+0xb: relocation past the end"},
		{"module 0", puppetELF().build(t), 0, "module ID 0"},
		{"huge .bss", with(func(s *elfSpec) { s.sections[3].size = 1 << 30 }), 0x58, "section .bss is 0x40000000 bytes, more than the 0x1000000 cap"},
		{"not an ELF", []byte("Yaz0\x00\x00\x00\x10"), 0x58, "rel: "},
		{"empty", nil, 0x58, "rel: "},
		{"little-endian", with(func(s *elfSpec) { s.data = elf.ELFDATA2LSB }), 0x58, "rel: "}, // debug/elf refuses it first
		{"executable", with(func(s *elfSpec) { s.etype = elf.ET_EXEC }), 0x58, "not a relocatable ELF (ET_EXEC); link with --relocatable"},
		{"x86", with(func(s *elfSpec) { s.machine = elf.EM_386 }), 0x58, "not a PowerPC ELF (EM_386)"},
	}
	for _, c := range cases {
		_, _, err := FromELF(c.elf, c.module, ELFOptions{})
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	// A relocation with the null symbol, or a symbol index past the
	// table, is refused rather than resolved to nothing.
	b := puppetELF().build(t)
	f, _ := elf.NewFile(bytes.NewReader(b))
	relaText := f.Section(".rela.text")
	bad := bytes.Clone(b)
	binary.BigEndian.PutUint32(bad[relaText.Offset+4:], uint32(elf.R_PPC_REL24)) // symbol 0
	if _, _, err := FromELF(bad, 0x58, ELFOptions{}); err == nil || !strings.Contains(err.Error(), "against symbol 0, which the symbol table doesn't have") {
		t.Errorf("null symbol: %v", err)
	}
	binary.BigEndian.PutUint32(bad[relaText.Offset+4:], 0xFFFF<<8|uint32(elf.R_PPC_REL24))
	if _, _, err := FromELF(bad, 0x58, ELFOptions{}); err == nil || !strings.Contains(err.Error(), "against symbol 65535") {
		t.Errorf("symbol past the table: %v", err)
	}
	// A null-typed relocation (padding) is skipped.
	binary.BigEndian.PutUint32(bad[relaText.Offset+4:], 0)
	if _, _, err := FromELF(bad, 0x58, ELFOptions{}); err != nil {
		t.Errorf("R_PPC_NONE: %v", err)
	}
}

// FromELF never panics on arbitrary bytes, and what it converts writes
// to a REL that parses back to the same module.
func FuzzFromELF(f *testing.F) {
	good := puppetELF().build(f)
	f.Add(good)
	f.Add(good[:0x40])
	f.Add(synthREL())
	f.Fuzz(func(t *testing.T, b []byte) {
		r, _, err := FromELF(b, 0x58, ELFOptions{})
		if err != nil {
			return
		}
		out, err := r.Bytes(false)
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		if g, err := Parse(out); err != nil || !reflect.DeepEqual(g, r) {
			t.Fatalf("Parse(Bytes()) = %v, differs", err)
		}
	})
}
