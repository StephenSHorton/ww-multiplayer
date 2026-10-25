// Package relstest builds a RELS.arc in miniature for tests of the
// patcher: the profile list (module 1) with one relocation against each
// actor module, and the actor modules the mod's REL can take over, laid
// out as the disc's archive is (Yaz0-stored RELs under mmem/). No
// Nintendo data is in it.
package relstest

import (
	"bytes"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
)

// The modules in the archive: ProfileListModule is f_pc_profile_lst.rel's
// ID, OtherModule, PuppetModule and SeatagModule three actor modules,
// PuppetModule being d_a_rectangle.rel's (proc 0xB5, PROC_RECTANGLE), the
// one the mod's REL replaces (inject.RELModuleID), and SeatagModule
// d_a_seatag.rel's (proc 0x27), whose entry the full body's profile
// takes (inject.RELBodyProcID).
const (
	ProfileListModule = 1
	OtherModule       = 0x42
	PuppetModule      = 0x58
	SeatagModule      = 0xA6
)

// The processes whose profile-list entries the modules' relocations
// patch: the entry of process p is the pointer at 4*p in the list's
// .data, as in the disc's f_pc_profile_lst.rel.
const (
	OtherProc  = 0x42
	PuppetProc = 0xB5
	SeatagProc = 0x27
)

// SeatagFile is SeatagModule's REL in the archive.
const SeatagFile = "mmem/d_a_seatag.rel"

// PuppetFile is the archive path of PuppetModule's REL, as on the disc.
const PuppetFile = "mmem/d_a_rectangle.rel"

// REL is a small REL of module id with a .text of n pseudo-random bytes
// (which Yaz0 can't shrink) and a .data of 0x80 bytes.
func REL(id uint32, n int) []byte {
	text := make([]byte, n)
	x := uint64(0x9E3779B97F4A7C15) + uint64(id)
	for i := range text {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		text[i] = byte(x)
	}
	f := &rel.File{
		ID: id, Version: 3, Align: 8, BSSAlign: 1,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 4}, Unresolved: rel.Symbol{Section: 1, Offset: 8},
		Sections: []rel.Section{
			{},
			{Offset: 1, Exec: true, Data: text},
			{Offset: 1, Data: bytes.Repeat([]byte{0xAB}, 0x80)},
		},
		Imports: []rel.Import{{Module: 0, Relocs: []rel.Reloc{{Section: 1, Offset: 0, Type: rel.RelocRel24, Addend: 0x80001234}}}},
	}
	b, err := f.Bytes(false)
	if err != nil {
		panic(err)
	}
	return b
}

// ProfileList is a REL like f_pc_profile_lst.rel: module 1, a .data of
// pointers indexed by process, one ADDR32 relocation against each of
// OtherModule, PuppetModule and SeatagModule at its process's entry
// pointing at that module's profile in its .data, and a few against
// module 0.
func ProfileList() []byte {
	f := &rel.File{
		ID: ProfileListModule, Version: 3, Align: 8, BSSAlign: 1,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 4}, Unresolved: rel.Symbol{Section: 1, Offset: 8},
		Sections: []rel.Section{
			{},
			{Offset: 1, Exec: true, Data: make([]byte, 0x20)},
			{Offset: 1, Data: make([]byte, 4*0x100)},
		},
		Imports: []rel.Import{
			{Module: OtherModule, Relocs: []rel.Reloc{{Section: 2, Offset: 4 * OtherProc, Type: rel.RelocAddr32, TargetSection: 2, Addend: 0x20}}},
			{Module: SeatagModule, Relocs: []rel.Reloc{{Section: 2, Offset: 4 * SeatagProc, Type: rel.RelocAddr32, TargetSection: 2, Addend: 0x20}}},
			{Module: PuppetModule, Relocs: []rel.Reloc{{Section: 2, Offset: 4 * PuppetProc, Type: rel.RelocAddr32, TargetSection: 2, Addend: 0x30}}},
			{Module: 0, Relocs: []rel.Reloc{
				{Section: 1, Offset: 0, Type: rel.RelocRel24, Addend: 0x80001234},
				{Section: 2, Offset: 0, Type: rel.RelocAddr32, Addend: 0x80005678},
			}},
		},
	}
	b, err := f.Bytes(false)
	if err != nil {
		panic(err)
	}
	return b
}

// Files are the archive's files: the profile list, the two actor RELs
// (PuppetModule's small, so the mod's REL grows the archive) and an
// uncompressed file.
func Files() []stagetest.File {
	return []stagetest.File{
		{Path: "mmem/f_pc_profile_lst.rel", Data: ProfileList(), Yaz0: true},
		{Path: "mmem/d_a_other.rel", Data: REL(OtherModule, 0x300), Yaz0: true},
		{Path: PuppetFile, Data: REL(PuppetModule, 0x40), Yaz0: true},
		{Path: SeatagFile, Data: REL(SeatagModule, 0x40), Yaz0: true},
		{Path: "mmem/plain.bin", Data: []byte("not a REL at all, eight bytes and more")},
	}
}

// Arc is the archive of Files.
func Arc() []byte { return stagetest.RARC("rels", Files()...) }

// LodBgFile is the archive path of LodBg's REL, as on the disc.
const LodBgFile = "mmem/d_a_lod_bg.rel"

// LodBgModule is LodBg's module ID, the disc's d_a_lod_bg.rel's (the
// sea-heap fix finds its REL by path, not by ID).
const LodBgModule = 0x26

// LodBgEntries is how many section entries the disc's d_a_lod_bg.rel
// has: the null one, the six of the decomp's splits.txt (.text, .ctors,
// .dtors, .rodata, .data, .bss) and twelve empty ones, so its .text
// starts at file 0x4C + 19*8 = 0xE4.
const LodBgEntries = 19

// LodBg is a stand-in for d_a_lod_bg.rel with the disc REL's layout and
// what rels.SeaHeapFix looks for, and none of its bytes: LodBgLayout
// with LodBgEntries section entries.
func LodBg() []byte { return LodBgLayout(LodBgEntries) }

// LodBgLayout is the stand-in with n section entries (at least 7): the
// header and section table of the disc's REL (version 3, align 8; .text
// 0x133C bytes, .ctors and .dtors 4, .rodata 0x1DD, .data 0xC8, a .bss
// of 0x20, the rest empty), each section's data 4-aligned after the
// table as the disc's are. Its .text is pseudo-random filler holding,
// at the decomp's offsets, `lis r4` / `addi r4` (createHeapCallBack,
// relocated against itself) at 0xCE0, `li r5, 0` at 0xCE8 and a bl
// fopAcM_entrySolidHeap at 0xCEC in execReadWait, the six bl
// JKRHeap::free of deleteModelData (LodBgFrees) with two other calls
// among them, and two more JKRHeap::free calls past it. Every bl into
// main.dol is unlinked, a branch to _unresolved (.text 0x58), with a
// REL24 relocation on it, as the disc stores them. With n other than 19
// .text moves and file offsets worked out for 19 entries land in the
// wrong words.
func LodBgLayout(n int) []byte {
	filler := func(seed uint64, size int) []byte {
		b := make([]byte, size)
		x := 0x9E3779B97F4A7C15 ^ seed
		for i := range b {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			b[i] = byte(x)
		}
		return b
	}
	t := filler(1, 0x133C)
	put := func(off uint32, w uint32) {
		t[off], t[off+1], t[off+2], t[off+3] = byte(w>>24), byte(w>>16), byte(w>>8), byte(w)
	}
	const unresolved = 0x58
	var main []rel.Reloc
	bl := func(off, target uint32) {
		put(off, 0x48000001|((unresolved-off)&0x03FFFFFC))
		main = append(main, rel.Reloc{Section: 1, Offset: off, Type: rel.RelocRel24, Addend: target})
	}
	put(0xCE0, 0x3C800000) // lis r4, createHeapCallBack@ha
	put(0xCE4, 0x38840000) // addi r4, r4, createHeapCallBack@l
	put(0xCE8, 0x38A00000) // li r5, 0
	bl(0xCEC, 0x80024D24)  // bl fopAcM_entrySolidHeap
	for _, off := range LodBgFrees {
		bl(off, 0x802B0518) // bl JKRHeap::free
	}
	bl(0x3C0, 0x802B1234) // other calls in deleteModelData
	bl(0x418, 0x802B1234)
	bl(0x5D8, 0x802B0518) // JKRHeap::free outside deleteModelData
	bl(0x6B8, 0x802B0518)
	main = append(main, rel.Reloc{Section: 5, Offset: 0x10, Type: rel.RelocAddr32, Addend: 0x80005678})

	secs := make([]rel.Section, n)
	secs[1] = rel.Section{Offset: 1, Exec: true, Data: t}
	secs[2] = rel.Section{Offset: 1, Data: filler(2, 4)}
	secs[3] = rel.Section{Offset: 1, Data: filler(3, 4)}
	secs[4] = rel.Section{Offset: 1, Data: filler(4, 0x1DD)}
	secs[5] = rel.Section{Offset: 1, Data: filler(5, 0xC8)}
	secs[6] = rel.Section{Size: 0x20}
	f := &rel.File{
		ID: LodBgModule, Version: 3, Align: 8, BSSAlign: 8, BSSSize: 0x20,
		Prolog: rel.Symbol{Section: 1}, Epilog: rel.Symbol{Section: 1, Offset: 0x2C}, Unresolved: rel.Symbol{Section: 1, Offset: unresolved},
		Sections: secs,
		Imports: []rel.Import{
			{Module: LodBgModule, Relocs: []rel.Reloc{
				{Section: 1, Offset: 0xCE2, Type: rel.RelocAddr16Ha, TargetSection: 1, Addend: 0xA38},
				{Section: 1, Offset: 0xCE6, Type: rel.RelocAddr16Lo, TargetSection: 1, Addend: 0xA38},
				{Section: 5, Offset: 0x20, Type: rel.RelocAddr32, TargetSection: 1, Addend: 0x12EC},
			}},
			{Module: 0, Relocs: main},
		},
	}
	b, err := f.Bytes(false)
	if err != nil {
		panic(err)
	}
	return b
}

// LodBgFrees are the .text offsets of deleteModelData's six JKRHeap::free
// calls in LodBg, the decomp's: the fix takes the first and the fourth.
var LodBgFrees = []uint32{0x394, 0x3B0, 0x3CC, 0x3EC, 0x408, 0x424}

// ArcWithLodBg is Arc with LodBg in it too.
func ArcWithLodBg() []byte {
	return stagetest.RARC("rels", append(Files(), stagetest.File{Path: LodBgFile, Data: LodBg(), Yaz0: true})...)
}
