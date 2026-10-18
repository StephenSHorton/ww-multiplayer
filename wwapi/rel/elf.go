package rel

import (
	"bytes"
	"cmp"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// AllowedSections are the ELF sections FromELF copies into a REL unless
// ELFOptions.Sections says otherwise: what wwrando's elf2rel.py copies
// (and the WW Online patcher's port of it). Everything else (.comment,
// the DWARF sections, .eh_frame, ...) is left out, and so are the
// relocations in it.
var AllowedSections = []string{
	".text", ".ctors", ".dtors", ".rodata", ".data", ".bss",
	".rodata.str1.4", ".rodata.cst4", ".rodata.cst8",
	".sdata", ".sbss",
}

// ELFOptions configures FromELF. The zero value is what elf2rel writes.
type ELFOptions struct {
	Sections []string // the ELF sections to copy, in any order; nil means AllowedSections
	Version  uint32   // REL format version to write: 1, 2 or 3; 0 means 3
	Align    uint32   // the header's section alignment (version 2 and later); 0 means 8
	BSSAlign uint32   // and .bss alignment; 0 means 1
}

// Layout is how FromELF laid an ELF out in a REL: which REL section each
// copied ELF section became, and where each symbol landed.
type Layout struct {
	names    []string          // REL section index -> ELF section name
	sections map[string]uint8  // ELF section name -> REL section index
	symbols  map[string]Symbol // defined symbols in copied sections
}

// Section returns the REL section index of the ELF section name, if it
// was copied.
func (l *Layout) Section(name string) (uint8, bool) {
	i, ok := l.sections[name]
	return i, ok
}

// SectionNames returns the ELF section name of each REL section, by
// index ("" for the null section at 0).
func (l *Layout) SectionNames() []string { return slices.Clone(l.names) }

// Symbol returns where the symbol name is in the REL: its section index
// and offset in that section. Only symbols defined in a copied section
// are known; a name defined more than once (static functions of one name
// in two units) resolves to the global one, else the first. This is how
// a caller finds the actor profile ("g_profile_PUPPET") that
// f_pc_profile_lst.rel is pointed at.
func (l *Layout) Symbol(name string) (section uint8, offset uint32, ok bool) {
	s, ok := l.symbols[name]
	return s.Section, s.Offset, ok
}

// maxELFSectionSize bounds a copied section before its data is
// allocated: a module has to fit in the GameCube's 24 MiB with the game,
// so a header claiming more (a NOBITS size, say) is corrupt or crafted.
const maxELFSectionSize = 16 << 20

// relocWidth is how many bytes each relocation type patches, and so
// which types a REL can carry: OSLink applies exactly these (the SDK's
// OSLink.c, Relocate). R_PPC_REL32 (26) is not among them, which is why
// elf2rel's users build with -fno-jump-tables; anything else (SDA21,
// PLT, GOT, TLS) can't be expressed either.
var relocWidth = map[elf.R_PPC]uint32{
	elf.R_PPC_ADDR32: 4, elf.R_PPC_ADDR24: 4,
	elf.R_PPC_ADDR16: 2, elf.R_PPC_ADDR16_LO: 2, elf.R_PPC_ADDR16_HI: 2, elf.R_PPC_ADDR16_HA: 2,
	elf.R_PPC_ADDR14: 4, elf.R_PPC_ADDR14_BRTAKEN: 4, elf.R_PPC_ADDR14_BRNTAKEN: 4,
	elf.R_PPC_REL24: 4, elf.R_PPC_REL14: 4, elf.R_PPC_REL14_BRTAKEN: 4, elf.R_PPC_REL14_BRNTAKEN: 4,
}

// FromELF converts a relocatable big-endian 32-bit PowerPC ELF (the
// output of `powerpc-eabi-ld --relocatable`) into the REL module
// moduleID, the way elf2rel.py does:
//
//   - The allowed sections (ELFOptions.Sections) become the REL's
//     sections in ELF order, after the ELF's null section 0. A NOBITS
//     section (.bss, .sbss) is written as zero-filled data, not as the
//     REL's one .bss (BSSSize stays 0): the module may have both .bss and
//     .sbss, and a data section keeps its index for the relocations.
//   - Every RELA section against a copied section becomes relocations:
//     a symbol with an absolute value (SHN_ABS: the game's own functions
//     and data, from a `name = 0xADDR;` linker script) is a relocation
//     against module 0 with the address as its addend, with the type the
//     compiler emitted (R_PPC_REL24 for a bl, ADDR16_HA/LO for lis/addi,
//     ADDR32 for a pointer); a symbol in a copied section is one against
//     moduleID, with that section's REL index and the symbol's offset
//     plus the ELF addend. A relocation of a type OSLink can't apply
//     (R_PPC_REL32 first of all), an undefined symbol or one in a
//     section that isn't copied is an error naming it.
//   - _prolog, _epilog and _unresolved come from the symbol table and
//     must be there.
//
// The File returned is as Bytes(false) lays it out, with its section
// offsets and fix size recorded; the Layout says where each ELF section
// and symbol went.
func FromELF(b []byte, moduleID uint32, opts ELFOptions) (*File, *Layout, error) {
	if moduleID == 0 {
		return nil, nil, errors.New("rel: module ID 0 is the game's own code")
	}
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return nil, nil, fmt.Errorf("rel: %w", err)
	}
	defer f.Close()
	switch {
	case f.Class != elf.ELFCLASS32 || f.Data != elf.ELFDATA2MSB:
		return nil, nil, fmt.Errorf("rel: not a big-endian 32-bit ELF (%v, %v)", f.Class, f.Data)
	case f.Machine != elf.EM_PPC:
		return nil, nil, fmt.Errorf("rel: not a PowerPC ELF (%v)", f.Machine)
	case f.Type != elf.ET_REL:
		return nil, nil, fmt.Errorf("rel: not a relocatable ELF (%v); link with --relocatable", f.Type)
	}
	allowed := opts.Sections
	if allowed == nil {
		allowed = AllowedSections
	}
	version := opts.Version
	if version == 0 {
		version = 3
	}
	if _, ok := headerSize(version); !ok {
		return nil, nil, fmt.Errorf("rel: unknown format version %d", version)
	}

	// Sections. elfToRel maps an ELF section index to a REL one, -1 for
	// one not copied.
	out := &File{
		ID: moduleID, Version: version,
		Align: opts.Align, BSSAlign: opts.BSSAlign,
	}
	if out.Align == 0 {
		out.Align = 8
	}
	if out.BSSAlign == 0 {
		out.BSSAlign = 1
	}
	lay := &Layout{sections: map[string]uint8{}, symbols: map[string]Symbol{}}
	elfToRel := make([]int, len(f.Sections))
	for i, s := range f.Sections {
		elfToRel[i] = -1
		if s.Type != elf.SHT_NULL && !slices.Contains(allowed, s.Name) {
			continue
		}
		if len(out.Sections) == 256 {
			return nil, nil, errors.New("rel: more than 256 sections to copy")
		}
		if s.Size > maxELFSectionSize {
			return nil, nil, fmt.Errorf("rel: section %s is 0x%x bytes, more than the 0x%x cap", s.Name, s.Size, uint64(maxELFSectionSize))
		}
		sec := Section{Exec: s.Flags&elf.SHF_EXECINSTR != 0}
		switch {
		case s.Type == elf.SHT_NULL || s.Size == 0:
			// A section with no data, like the empty slots in every
			// vanilla REL. Nothing may be relocated against it (below).
		case s.Type == elf.SHT_NOBITS:
			sec.Data = make([]byte, s.Size)
			sec.Offset = 1 // has data; Bytes sets the real offset
		default:
			if sec.Data, err = s.Data(); err != nil {
				return nil, nil, fmt.Errorf("rel: section %s: %w", s.Name, err)
			}
			sec.Data = bytes.Clone(sec.Data)
			sec.Offset = 1
		}
		sec.Size = uint32(len(sec.Data))
		elfToRel[i] = len(out.Sections)
		if s.Type != elf.SHT_NULL {
			lay.sections[s.Name] = uint8(len(out.Sections))
		}
		lay.names = append(lay.names, s.Name)
		out.Sections = append(out.Sections, sec)
	}

	// Symbols. debug/elf leaves the null symbol out, so the ELF's symbol
	// index i is syms[i-1].
	syms, err := f.Symbols()
	if err != nil {
		return nil, nil, fmt.Errorf("rel: symbols: %w", err)
	}
	for _, s := range syms {
		typ := elf.ST_TYPE(s.Info)
		if s.Name == "" || typ == elf.STT_SECTION || typ == elf.STT_FILE {
			continue
		}
		si := int(s.Section)
		if si == 0 || si >= len(elfToRel) || elfToRel[si] < 0 {
			continue // undefined, absolute, common, or in a section not copied
		}
		if _, dup := lay.symbols[s.Name]; dup && elf.ST_BIND(s.Info) == elf.STB_LOCAL {
			continue
		}
		lay.symbols[s.Name] = Symbol{Section: uint8(elfToRel[si]), Offset: uint32(s.Value)}
	}
	for name, dst := range map[string]*Symbol{"_prolog": &out.Prolog, "_epilog": &out.Epilog, "_unresolved": &out.Unresolved} {
		s, ok := lay.symbols[name]
		if !ok {
			return nil, nil, fmt.Errorf("rel: no %s symbol in a copied section", name)
		}
		*dst = s
	}

	// Relocations, by the section they patch.
	self := &Import{Module: moduleID}
	dol := &Import{Module: 0}
	for _, rs := range f.Sections {
		if rs.Type != elf.SHT_RELA && rs.Type != elf.SHT_REL {
			continue
		}
		if int(rs.Info) >= len(f.Sections) || elfToRel[rs.Info] < 0 {
			continue // relocations in a section not copied (.debug_info, ...)
		}
		target := f.Sections[rs.Info]
		if rs.Type == elf.SHT_REL {
			return nil, nil, fmt.Errorf("rel: %s: REL relocations are not supported, only RELA", rs.Name)
		}
		data, err := rs.Data()
		if err != nil {
			return nil, nil, fmt.Errorf("rel: %s: %w", rs.Name, err)
		}
		if len(data)%12 != 0 {
			return nil, nil, fmt.Errorf("rel: %s: %d bytes is not a whole number of RELA entries", rs.Name, len(data))
		}
		secIdx := uint8(elfToRel[rs.Info])
		secSize := uint32(len(out.Sections[secIdx].Data))
		for e := data; len(e) > 0; e = e[12:] {
			off, info := binary.BigEndian.Uint32(e), binary.BigEndian.Uint32(e[4:])
			addend := int32(binary.BigEndian.Uint32(e[8:]))
			typ, symIdx := elf.R_PPC(info&0xFF), info>>8
			where := fmt.Sprintf("%s+0x%x", target.Name, off)
			if typ == elf.R_PPC_NONE {
				continue
			}
			width, ok := relocWidth[typ]
			if !ok {
				hint := ""
				if typ == elf.R_PPC_REL32 {
					hint = " (a jump table: compile with -fno-jump-tables)"
				}
				return nil, nil, fmt.Errorf("rel: %s: relocation type %v can't be written to a REL%s", where, typ, hint)
			}
			if symIdx == 0 || int(symIdx) > len(syms) {
				return nil, nil, fmt.Errorf("rel: %s: relocation against symbol %d, which the symbol table doesn't have", where, symIdx)
			}
			if uint64(off)+uint64(width) > uint64(secSize) {
				return nil, nil, fmt.Errorf("rel: %s: relocation past the end of the section (0x%x bytes)", where, secSize)
			}
			sym := syms[symIdx-1]
			r := Reloc{Section: secIdx, Offset: off, Type: RelocType(typ)}
			imp := self
			switch si := sym.Section; {
			case si == elf.SHN_UNDEF:
				return nil, nil, fmt.Errorf("rel: %s: unresolved symbol %q", where, sym.Name)
			case si == elf.SHN_ABS:
				imp = dol
			case si >= elf.SHN_LORESERVE:
				return nil, nil, fmt.Errorf("rel: %s: symbol %q has special section index %v", where, sym.Name, si)
			case int(si) >= len(f.Sections) || elfToRel[si] < 0:
				name := fmt.Sprintf("#%d", si)
				if int(si) < len(f.Sections) {
					name = f.Sections[si].Name
				}
				return nil, nil, fmt.Errorf("rel: %s: symbol %q is in section %s, which isn't copied into the REL", where, sym.Name, name)
			case out.Sections[elfToRel[si]].Offset == 0:
				return nil, nil, fmt.Errorf("rel: %s: symbol %q is in section %s, which is empty", where, sym.Name, f.Sections[si].Name)
			default:
				r.TargetSection = uint8(elfToRel[si])
			}
			r.Addend = uint32(int64(sym.Value) + int64(addend))
			imp.Relocs = append(imp.Relocs, r)
		}
	}
	for _, imp := range []*Import{self, dol} {
		if len(imp.Relocs) == 0 {
			continue
		}
		slices.SortStableFunc(imp.Relocs, func(a, b Reloc) int {
			return cmp.Or(cmp.Compare(a.Section, b.Section), cmp.Compare(a.Offset, b.Offset))
		})
		out.Imports = append(out.Imports, *imp)
	}
	// Lay the file out once, so the File is what Parse reads back from
	// Bytes(false): the sections' offsets and the fix size recorded, the
	// relocations in the order they are written.
	if _, err := out.Bytes(false); err != nil {
		return nil, nil, err
	}
	if version < 3 {
		out.FixSize = 0
	}
	return out, lay, nil
}
