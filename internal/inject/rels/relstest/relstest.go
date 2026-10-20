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
