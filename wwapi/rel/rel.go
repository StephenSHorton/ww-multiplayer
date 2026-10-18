// Package rel reads and writes REL files, the relocatable modules the
// game loads at run time (every actor in RELS.arc, and f_pc_profile_lst,
// the table of actor profiles). It is a port of wwrando's rel.py as the
// WW Online patcher's C# port has it: a parsed file can be edited (a
// relocation's target, say) and written back, in place when the section
// data must stay where it was. Every reader takes untrusted bytes:
// offsets and counts are bounds-checked and malformed input is an
// error, never a panic.
//
// A REL (OSModuleHeader, the OSLink part of the SDK) is big-endian:
//
//	0x00  module ID; 0x04, 0x08 link pointers (0 on disc)
//	0x0C  section count     0x10  section table offset
//	0x14  module name offset and 0x18 size (into a table not in the file)
//	0x1C  format version    0x20  .bss size
//	0x24  relocation table offset   0x28  import table offset, 0x2C size
//	0x30  u8 section of _prolog, 0x31 _epilog, 0x32 _unresolved, 0x33 pad
//	0x34, 0x38, 0x3C  their offsets in those sections
//	0x40  section alignment, 0x44 .bss alignment (version 2)
//	0x48  fix size: what must stay in memory once linked (version 3)
//
// The section table has 8 bytes per section: the file offset of its
// data with bit 0 set for code, and its size; offset 0 is a section with
// no data (.bss, or an empty one). The import table has 8 bytes per
// module relocated against: its ID and the offset of its relocations,
// 8-byte entries read in order: a u16 offset from the previous
// relocation, a type, the target section, and the symbol's offset in it.
// R_DOLPHIN_SECTION (0xCA) picks the section being patched and resets
// the running offset, R_DOLPHIN_NOP (0xC9) only advances it, and
// R_DOLPHIN_END (0xCB) ends the module's run.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package rel

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// RelocType is a relocation's type: the PowerPC ELF types the REL
// format applies, and the three Dolphin markers that structure a run.
type RelocType uint8

const (
	RelocNone          RelocType = 0x00
	RelocAddr32        RelocType = 0x01
	RelocAddr24        RelocType = 0x02
	RelocAddr16        RelocType = 0x03
	RelocAddr16Lo      RelocType = 0x04
	RelocAddr16Hi      RelocType = 0x05
	RelocAddr16Ha      RelocType = 0x06
	RelocAddr14        RelocType = 0x07
	RelocAddr14BrTaken RelocType = 0x08
	RelocAddr14BrNTkn  RelocType = 0x09
	RelocRel24         RelocType = 0x0A
	RelocRel14         RelocType = 0x0B
	RelocRel14BrTaken  RelocType = 0x0C
	RelocRel14BrNTkn   RelocType = 0x0D

	RelocNop     RelocType = 0xC9 // R_DOLPHIN_NOP: advances the offset only
	RelocSection RelocType = 0xCA // R_DOLPHIN_SECTION: the section the next relocations patch
	RelocEnd     RelocType = 0xCB // R_DOLPHIN_END: ends a module's run
	RelocMrkRef  RelocType = 0xCC // R_DOLPHIN_MRKREF
)

const (
	sectionEntrySize = 8
	importEntrySize  = 8
	relocEntrySize   = 8
)

// File is a parsed REL. Edit its fields and Sections, Imports and their
// Relocs freely; Bytes writes the result.
type File struct {
	ID         uint32 // module ID
	Version    uint32 // format version: 1, 2 or 3
	NameOffset uint32 // module name offset and size, kept as read
	NameSize   uint32
	BSSSize    uint32
	Prolog     Symbol
	Epilog     Symbol
	Unresolved Symbol
	Align      uint32 // version 2 and later
	BSSAlign   uint32
	FixSize    uint32 // version 3; Bytes recomputes it
	Sections   []Section
	Imports    []Import // in file order
}

// Symbol locates an entry point: an offset in a section.
type Symbol struct {
	Section uint8
	Offset  uint32
}

// Section is one section of a File.
type Section struct {
	Offset uint32 // file offset of its data, 0 for a section with none; Bytes sets it
	Exec   bool   // code
	Data   []byte // its data; empty for a section with none
	Size   uint32 // its size when it has no data: .bss (then BSSSize is written) or empty
}

// BSS reports whether the section is the .bss: no data, not code, and
// a size.
func (s Section) BSS() bool { return s.Offset == 0 && !s.Exec && s.Size != 0 }

// Import is the relocations against one module: 0 for the game's own
// code (main.dol), the file's own ID for itself, otherwise another REL.
type Import struct {
	Module uint32
	Relocs []Reloc
}

// Reloc is one relocation: patch Section at Offset with the address of
// the symbol at Addend in TargetSection of the import's module, the way
// Type says. A RelocNop carries only an offset.
type Reloc struct {
	Section       uint8  // the section patched
	Offset        uint32 // in it
	Type          RelocType
	TargetSection uint8  // in the import's module
	Addend        uint32 // the symbol's offset in that section
}

// Parse parses the REL b, expanding it first when it is Yaz0-compressed
// (as every REL in RELS.arc is). Section data is copied.
func Parse(b []byte) (*File, error) {
	if stage.IsYaz0(b) {
		var err error
		if b, err = stage.DecodeYaz0(b); err != nil {
			return nil, fmt.Errorf("rel: %w", err)
		}
	}
	be := binary.BigEndian
	if len(b) < 0x40 {
		return nil, fmt.Errorf("rel: header truncated (%d bytes)", len(b))
	}
	f := &File{ID: be.Uint32(b), Version: be.Uint32(b[0x1C:])}
	hdr, ok := headerSize(f.Version)
	if !ok {
		return nil, fmt.Errorf("rel: unknown format version %d", f.Version)
	}
	if len(b) < hdr {
		return nil, fmt.Errorf("rel: version %d header truncated (%d bytes, need %d)", f.Version, len(b), hdr)
	}
	f.NameOffset, f.NameSize = be.Uint32(b[0x14:]), be.Uint32(b[0x18:])
	f.BSSSize = be.Uint32(b[0x20:])
	f.Prolog = Symbol{b[0x30], be.Uint32(b[0x34:])}
	f.Epilog = Symbol{b[0x31], be.Uint32(b[0x38:])}
	f.Unresolved = Symbol{b[0x32], be.Uint32(b[0x3C:])}
	if f.Version >= 2 {
		f.Align, f.BSSAlign = be.Uint32(b[0x40:]), be.Uint32(b[0x44:])
	}
	if f.Version >= 3 {
		f.FixSize = be.Uint32(b[0x48:])
	}

	// table returns the count entries of size bytes at off.
	table := func(what string, off, count uint64, size int) ([]byte, error) {
		end := off + count*uint64(size)
		if end > uint64(len(b)) {
			return nil, fmt.Errorf("rel: %d %s at 0x%x run past the end (%d bytes)", count, what, off, len(b))
		}
		return b[off:end], nil
	}
	nSec := uint64(be.Uint32(b[0x0C:]))
	secs, err := table("sections", uint64(be.Uint32(b[0x10:])), nSec, sectionEntrySize)
	if err != nil {
		return nil, err
	}
	f.Sections = make([]Section, nSec)
	for i := range f.Sections {
		w, size := be.Uint32(secs[i*sectionEntrySize:]), be.Uint32(secs[i*sectionEntrySize+4:])
		s := Section{Offset: w &^ 1, Exec: w&1 != 0, Size: size}
		if s.Offset != 0 {
			end := uint64(s.Offset) + uint64(size)
			if end > uint64(len(b)) {
				return nil, fmt.Errorf("rel: section %d: data [0x%x..0x%x] runs past the end (%d bytes)", i, s.Offset, end, len(b))
			}
			s.Data = bytes.Clone(b[s.Offset:end])
		}
		f.Sections[i] = s
	}

	impSize := uint64(be.Uint32(b[0x2C:]))
	if impSize%importEntrySize != 0 {
		return nil, fmt.Errorf("rel: import table size 0x%x is not a multiple of %d", impSize, importEntrySize)
	}
	imps, err := table("imports", uint64(be.Uint32(b[0x28:])), impSize/importEntrySize, importEntrySize)
	if err != nil {
		return nil, err
	}
	f.Imports = make([]Import, len(imps)/importEntrySize)
	total := 0 // relocation entries read, capped: a file can't hold more than it has bytes for
	for i := range f.Imports {
		imp := Import{Module: be.Uint32(imps[i*importEntrySize:])}
		off := uint64(be.Uint32(imps[i*importEntrySize+4:]))
		cur, prev := -1, uint32(0)
		for {
			if off+relocEntrySize > uint64(len(b)) {
				return nil, fmt.Errorf("rel: module 0x%x's relocations run past the end (%d bytes) without R_DOLPHIN_END", imp.Module, len(b))
			}
			if total++; total > len(b)/relocEntrySize {
				return nil, fmt.Errorf("rel: more relocation entries than the file holds")
			}
			e := b[off : off+relocEntrySize]
			off += relocEntrySize
			typ := RelocType(e[2])
			if typ == RelocEnd {
				break
			}
			if typ == RelocSection {
				cur, prev = int(e[3]), 0
				continue
			}
			if cur < 0 {
				return nil, fmt.Errorf("rel: module 0x%x: a relocation before any R_DOLPHIN_SECTION", imp.Module)
			}
			prev += uint32(be.Uint16(e))
			imp.Relocs = append(imp.Relocs, Reloc{
				Section: uint8(cur), Offset: prev, Type: typ, TargetSection: e[3], Addend: be.Uint32(e[4:]),
			})
		}
		f.Imports[i] = imp
	}
	return f, nil
}

// headerSize is the header length of a REL of version v.
func headerSize(v uint32) (int, bool) {
	switch v {
	case 1:
		return 0x40, true
	case 2:
		return 0x48, true
	case 3:
		return 0x4C, true
	}
	return 0, false
}

// Import returns the relocations against module, or nil when there are
// none (the first such import when there are several).
func (f *File) Import(module uint32) *Import {
	for i := range f.Imports {
		if f.Imports[i].Module == module {
			return &f.Imports[i]
		}
	}
	return nil
}

// SoleReloc returns the one relocation against module: the way
// f_pc_profile_lst.rel refers to an actor's profile, one ADDR32 per
// actor module. It is an error when there is none or more than one.
func (f *File) SoleReloc(module uint32) (*Reloc, error) {
	var found *Reloc
	n := 0
	for i := range f.Imports {
		imp := &f.Imports[i]
		if imp.Module != module {
			continue
		}
		n += len(imp.Relocs)
		if len(imp.Relocs) > 0 {
			found = &imp.Relocs[0]
		}
	}
	if n != 1 {
		return nil, fmt.Errorf("rel: %d relocations against module 0x%x, want one", n, module)
	}
	return found, nil
}

// Bytes writes the file out, the way rel.py's save_changes does: the
// section table right after the header, each section's data 4-aligned
// after it (or, with preserveOffsets, at the Offset it has, which must
// not be earlier than the data before it; the vanilla RELs 8-align some
// sections, so only that reproduces them), the import table at the end
// of the data, and the relocations after it, module by module, with
// the file's own module and module 0 moved last, each module's
// relocations in (section, offset) order with the markers that need
// and NOPs across gaps over 0xFFFF. The fix size is the end of the
// relocations against other modules. The offsets written are recorded in
// Sections and FixSize.
func (f *File) Bytes(preserveOffsets bool) ([]byte, error) {
	hdr, ok := headerSize(f.Version)
	if !ok {
		return nil, fmt.Errorf("rel: unknown format version %d", f.Version)
	}
	be := binary.BigEndian
	tableEnd := hdr + len(f.Sections)*sectionEntrySize
	next, end := alignUp(tableEnd, 4), tableEnd
	for i := range f.Sections {
		s := &f.Sections[i]
		if s.Offset == 0 {
			continue
		}
		if preserveOffsets {
			if int(s.Offset) < next {
				return nil, fmt.Errorf("rel: section %d at 0x%x overlaps what comes before it (0x%x)", i, s.Offset, next)
			}
			next = int(s.Offset)
		}
		s.Offset = uint32(next)
		end = max(end, next+len(s.Data))
		next = alignUp(next+len(s.Data), 4)
	}

	// Modules relocated against: others first, then the file itself,
	// then module 0, so the fix size can leave the last two out.
	order := make([]*Import, 0, len(f.Imports))
	for _, pick := range []func(m uint32) bool{
		func(m uint32) bool { return m != f.ID && m != 0 },
		func(m uint32) bool { return m == f.ID && m != 0 },
		func(m uint32) bool { return m == 0 },
	} {
		for i := range f.Imports {
			if pick(f.Imports[i].Module) {
				order = append(order, &f.Imports[i])
			}
		}
	}
	impOff := end
	impSize := len(order) * importEntrySize
	relOff := impOff + impSize
	out := make([]byte, relOff)
	fix := relOff
	for k, imp := range order {
		be.PutUint32(out[impOff+k*importEntrySize:], imp.Module)
		be.PutUint32(out[impOff+k*importEntrySize+4:], uint32(len(out)))
		relocs := slices.Clone(imp.Relocs)
		slices.SortStableFunc(relocs, func(a, b Reloc) int {
			return cmp.Or(cmp.Compare(a.Section, b.Section), cmp.Compare(a.Offset, b.Offset))
		})
		cur, prev := -1, uint32(0)
		for _, r := range relocs {
			if int(r.Section) != cur {
				cur, prev = int(r.Section), 0
				out = appendReloc(out, 0, RelocSection, r.Section, 0)
			}
			for r.Offset-prev > 0xFFFF {
				out = appendReloc(out, 0xFFFF, RelocNop, 0, 0)
				prev += 0xFFFF
			}
			sec, addend := r.TargetSection, r.Addend
			if r.Type == RelocNop {
				sec, addend = 0, 0
			}
			out = appendReloc(out, uint16(r.Offset-prev), r.Type, sec, addend)
			prev = r.Offset
		}
		out = appendReloc(out, 0, RelocEnd, 0, 0)
		if imp.Module != 0 && imp.Module != f.ID {
			fix = len(out)
		}
	}
	f.FixSize = uint32(fix)

	be.PutUint32(out[0x00:], f.ID)
	be.PutUint32(out[0x0C:], uint32(len(f.Sections)))
	be.PutUint32(out[0x10:], uint32(hdr))
	be.PutUint32(out[0x14:], f.NameOffset)
	be.PutUint32(out[0x18:], f.NameSize)
	be.PutUint32(out[0x1C:], f.Version)
	be.PutUint32(out[0x20:], f.BSSSize)
	be.PutUint32(out[0x24:], uint32(relOff))
	be.PutUint32(out[0x28:], uint32(impOff))
	be.PutUint32(out[0x2C:], uint32(impSize))
	out[0x30], out[0x31], out[0x32] = f.Prolog.Section, f.Epilog.Section, f.Unresolved.Section
	be.PutUint32(out[0x34:], f.Prolog.Offset)
	be.PutUint32(out[0x38:], f.Epilog.Offset)
	be.PutUint32(out[0x3C:], f.Unresolved.Offset)
	if f.Version >= 2 {
		be.PutUint32(out[0x40:], f.Align)
		be.PutUint32(out[0x44:], f.BSSAlign)
	}
	if f.Version >= 3 {
		be.PutUint32(out[0x48:], f.FixSize)
	}
	for i, s := range f.Sections {
		e := out[hdr+i*sectionEntrySize:]
		w, size := s.Offset, s.Size
		if s.Exec {
			w |= 1
		}
		switch {
		case s.Offset != 0:
			size = uint32(len(s.Data))
			copy(out[s.Offset:], s.Data)
		case s.BSS():
			size = f.BSSSize
		}
		be.PutUint32(e, w)
		be.PutUint32(e[4:], size)
	}
	return out, nil
}

func appendReloc(out []byte, delta uint16, typ RelocType, section uint8, addend uint32) []byte {
	out = binary.BigEndian.AppendUint16(out, delta)
	out = append(out, byte(typ), section)
	return binary.BigEndian.AppendUint32(out, addend)
}

func alignUp(v, a int) int { return (v + a - 1) / a * a }
