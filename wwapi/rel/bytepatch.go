package rel

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Write is a byte edit to a vanilla REL: New in place of Old at Offset
// in section Section. Old is what the vanilla REL must hold there, so a
// patch never lands on a REL it wasn't written for.
type Write struct {
	Section uint8
	Offset  uint32
	Old     []byte
	New     []byte
}

// SectionAt locates n bytes at file offset off of the expanded REL f
// was parsed from: the section with data holding all of them, and the
// offset in it. A REL patch given as file offsets (wwrando's and
// betterww's `.org` in a REL is one: its RELs are patched through
// convert_rel_offset_to_section_index_and_relative_offset) is resolved
// with this against the REL's own section table, so a REL whose table
// has a different number of entries moves .text and the patch with it.
// It is false for bytes in the header, the section table, the import
// table or the relocations, or across a section's end.
func (f *File) SectionAt(off uint32, n int) (section uint8, offset uint32, ok bool) {
	for i, s := range f.Sections {
		if s.Offset == 0 || i > 0xFF {
			continue
		}
		if uint64(off) >= uint64(s.Offset) && uint64(off)+uint64(n) <= uint64(s.Offset)+uint64(len(s.Data)) {
			return uint8(i), off - s.Offset, true
		}
	}
	return 0, 0, false
}

// PatchStats is what PatchBytes did.
type PatchStats struct {
	// Already says every write's bytes were New already (a REL this
	// patch was applied to): nothing was written.
	Already bool
	// Disabled is how many relocations under the writes were turned
	// into R_DOLPHIN_NOP.
	Disabled int
}

// PatchBytes applies writes to the REL b (uncompressed) in place: the
// section table, the import table and every byte the writes don't cover
// stay as they are, so the result differs from b only at the writes and
// in the type byte of the relocations under them. Each relocation whose
// field overlaps a write becomes R_DOLPHIN_NOP, which keeps its offset
// delta (OSLink still advances the running offset over it,
// OSLink.c's Relocate), so the relocations after it patch where they
// did; left live, the loader would rewrite the patched bytes when it
// links the module (a nop over a bl into main.dol would become a bl
// again). Retargeting a relocation or adding one needs the table
// rebuilt (File.Bytes) and is not done here.
//
// The writes must lie inside sections with data, not overlap each
// other, and be the same length as their Old. Either every write finds
// its Old (the patch is applied) or every write finds its New (Already;
// any live relocation under one is still disabled): a mix, or bytes
// that are neither, is an error and nothing is written. b is not
// modified; the result is a copy.
//
// The idea (patch in place, disable the relocations under the patch)
// is the WW Online patcher's RelBytePatcher, adapted from
// datstarkey/ww-online WWOnline.Patcher/BinaryFormats/Rel/RelBytePatcher.cs
// (MIT), itself from wwrando / betterww's REL patching (MIT). Written
// anew over this package's reader.
func PatchBytes(b []byte, writes []Write) ([]byte, PatchStats, error) {
	var st PatchStats
	if stage.IsYaz0(b) {
		return nil, st, fmt.Errorf("rel: PatchBytes takes an uncompressed REL")
	}
	f, err := Parse(b)
	if err != nil {
		return nil, st, err
	}
	spans := make([][2]uint64, len(writes)) // file offsets [start, end)
	old, already := 0, 0
	for i, w := range writes {
		if len(w.New) == 0 || len(w.Old) != len(w.New) {
			return nil, st, fmt.Errorf("rel: write %d: %d bytes over %d (want the same, at least one)", i, len(w.New), len(w.Old))
		}
		if int(w.Section) >= len(f.Sections) || f.Sections[w.Section].Offset == 0 {
			return nil, st, fmt.Errorf("rel: write %d: section %d has no data", i, w.Section)
		}
		s := f.Sections[w.Section]
		if uint64(w.Offset)+uint64(len(w.New)) > uint64(len(s.Data)) {
			return nil, st, fmt.Errorf("rel: write %d: [0x%x..0x%x] runs past section %d (0x%x bytes)",
				i, w.Offset, uint64(w.Offset)+uint64(len(w.New)), w.Section, len(s.Data))
		}
		start := uint64(s.Offset) + uint64(w.Offset)
		spans[i] = [2]uint64{start, start + uint64(len(w.New))}
		for j := range i {
			if spans[j][0] < spans[i][1] && spans[i][0] < spans[j][1] {
				return nil, st, fmt.Errorf("rel: writes %d and %d overlap", j, i)
			}
		}
		got := s.Data[w.Offset : w.Offset+uint32(len(w.New))]
		switch {
		case bytes.Equal(got, w.Old):
			old++
		case bytes.Equal(got, w.New):
			already++
		default:
			return nil, st, fmt.Errorf("rel: write %d: section %d offset 0x%x holds %x, neither the vanilla %x nor the patched %x",
				i, w.Section, w.Offset, got, w.Old, w.New)
		}
	}
	if old != 0 && already != 0 {
		return nil, st, fmt.Errorf("rel: %d of %d writes are applied already: a REL patched in part", already, len(writes))
	}
	st.Already = already == len(writes) && len(writes) != 0

	out := bytes.Clone(b)
	if !st.Already {
		for i, w := range writes {
			copy(out[spans[i][0]:], w.New)
		}
	}
	entries, err := relocEntries(out, f)
	if err != nil {
		return nil, st, err
	}
	for _, e := range entries {
		size := fieldSize(e.typ)
		if size == 0 {
			continue
		}
		for _, sp := range spans {
			if e.pos < sp[1] && sp[0] < e.pos+size {
				out[e.entry+2] = byte(RelocNop)
				st.Disabled++
				break
			}
		}
	}
	return out, st, nil
}

// relocEntry is a live relocation as it sits in the file: the file
// offset of its 8-byte entry, its type and the file offset of the field
// it patches.
type relocEntry struct {
	entry uint64
	typ   RelocType
	pos   uint64
}

// relocEntries walks b's relocations the way OSLink does (and Parse,
// which f is b parsed by and has checked the walk's bounds), returning
// every one that patches a section with data in the file. R_DOLPHIN_NOP
// and the markers are left out.
func relocEntries(b []byte, f *File) ([]relocEntry, error) {
	be := binary.BigEndian
	impOff, impSize := uint64(be.Uint32(b[0x28:])), uint64(be.Uint32(b[0x2C:]))
	var out []relocEntry
	for k := uint64(0); k < impSize/importEntrySize; k++ {
		off := uint64(be.Uint32(b[impOff+k*importEntrySize+4:]))
		cur, pos := -1, uint64(0)
		for ; ; off += relocEntrySize {
			if off+relocEntrySize > uint64(len(b)) {
				return nil, fmt.Errorf("rel: relocations run past the end")
			}
			e := b[off : off+relocEntrySize]
			typ := RelocType(e[2])
			if typ == RelocEnd {
				break
			}
			if typ == RelocSection {
				cur, pos = int(e[3]), 0
				continue
			}
			pos += uint64(be.Uint16(e))
			if typ == RelocNop || cur < 0 || cur >= len(f.Sections) || f.Sections[cur].Offset == 0 {
				continue // a .bss or a missing section: nothing in the file to clash with
			}
			out = append(out, relocEntry{entry: off, typ: typ, pos: uint64(f.Sections[cur].Offset) + pos})
		}
	}
	return out, nil
}

// fieldSize is how many bytes a relocation of type t rewrites at its
// offset: the half-word types two, the rest of the PowerPC ones a word
// (the 24- and 14-bit branch fields sit in one), R_PPC_NONE and the
// Dolphin markers none.
func fieldSize(t RelocType) uint64 {
	switch t {
	case RelocAddr16, RelocAddr16Lo, RelocAddr16Hi, RelocAddr16Ha:
		return 2
	case RelocAddr32, RelocAddr24, RelocAddr14, RelocAddr14BrTaken, RelocAddr14BrNTkn,
		RelocRel24, RelocRel14, RelocRel14BrTaken, RelocRel14BrNTkn:
		return 4
	}
	return 0
}
