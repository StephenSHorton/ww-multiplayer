// Package rels patches RELS.arc, the archive of the game's relocatable
// modules, to ship mod code as one of them: a REL built from the mod's
// ELF (wwapi/rel.FromELF) takes an actor module's place, and the actor
// profile list is pointed at the mod's profile. The ISO patcher
// (internal/inject) puts the result on the disc.
package rels

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// ProfileListModule is the module ID of f_pc_profile_lst.rel, the REL in
// RELS.arc holding g_fpcPfLst_ProfileList, the table of every actor's
// profile: one ADDR32 relocation per actor module. Its prolog hands the
// table to the game (d_procname / fpcPf_Get), so pointing its entry for
// a module at another profile is how a module's actor is replaced.
const ProfileListModule = 1

// RELSArcMaxGrowth is how much bigger than the disc's RELS.arc the
// patched one may be, in stored (Yaz0) bytes: the guard the WW Online
// patcher settled on, deliberately conservative. The whole of RELS.arc's
// file data is loaded into ARAM for good at boot (it is a JKRCompArchive
// mounted to ARAM: DynamicLink.cpp:185, JKRCompArchive.cpp:126) out of
// the 0x5CE000-byte JKRAramHeap. The boot-time mounts, the picture box
// and boss data and the two LkD0x.arc demo archives take the rest, which
// leaves about 1 MB free in vanilla; past that the first play scene's
// LkD00.arc mount fails ("Failed assertion d_s_play.cpp:3439"). 64 KB
// keeps well clear of it.
const RELSArcMaxGrowth = 0x10000

// ErrRELSArcGrowth is returned (wrapped, with the numbers) by
// PatchRELSArc when the archive would grow by more than allowed.
var ErrRELSArcGrowth = errors.New("RELS.arc would grow past the ARAM guard")

// PatchRELSArc returns arc, the disc's RELS.arc, with the REL whose
// module ID is moduleID replaced by newREL (an uncompressed REL of that
// ID; it is stored Yaz0-compressed, as the disc stores it, under the
// file's own name, since the loader opens RELs by name) and
// f_pc_profile_lst.rel's entry for that module repointed at the actor
// profile at profileOffset in newREL's section profileSection, so the
// game creates newREL's actor for the module's process. Every other
// file of the archive is stored as it was. An archive that would grow
// by more than maxGrowth bytes (RELSArcMaxGrowth, see there) is refused
// with ErrRELSArcGrowth.
func PatchRELSArc(arc []byte, moduleID uint32, newREL []byte, profileSection uint8, profileOffset uint32, maxGrowth int) ([]byte, error) {
	out, _, _, err := patchRELSArc(arc, moduleID, newREL, ProfileTarget{Section: profileSection, Offset: profileOffset}, nil, nil, maxGrowth)
	return out, err
}

// ProfileTarget is a profile-list entry to point into the new REL: the
// entry of process Proc (offset 4*Proc in the list's pointer table,
// g_fpcPfLst_ProfileList[Proc]) at the actor profile at Offset in the new
// REL's Section.
type ProfileTarget struct {
	Proc    uint16
	Section uint8
	Offset  uint32
}

// PatchRELSArcProfiles is PatchRELSArc for a module that serves more
// than one process (remote-link-stage4-plan.md D4-1). main is the
// module's own entry: its relocation against moduleID at offset
// 4*main.Proc (main.Proc 0: the sole relocation against moduleID, as
// PatchRELSArc finds it), which also names the list's section. Each extra
// entry's relocation, found at (that section, 4*Proc) whatever module it
// is against today, is moved into moduleID's import and pointed at its
// profile; the import it left is dropped when that emptied it. The
// result is a fixed point: patching it again with the same inputs gives
// it back.
func PatchRELSArcProfiles(arc []byte, moduleID uint32, newREL []byte, main ProfileTarget, extra []ProfileTarget, maxGrowth int) ([]byte, error) {
	out, _, _, err := patchRELSArc(arc, moduleID, newREL, main, extra, nil, maxGrowth)
	return out, err
}

// checkProfile checks the new REL f has data at the profile p names.
func checkProfile(f *rel.File, p ProfileTarget) error {
	if int(p.Section) >= len(f.Sections) || f.Sections[p.Section].Offset == 0 {
		return fmt.Errorf("rels: the new REL has no data in section %d for the profile", p.Section)
	}
	if uint64(p.Offset) >= uint64(len(f.Sections[p.Section].Data)) {
		return fmt.Errorf("rels: profile at 0x%x is past the end of the new REL's section %d (0x%x bytes)",
			p.Offset, p.Section, len(f.Sections[p.Section].Data))
	}
	return nil
}

// patchRELSArc is PatchRELSArcProfiles with the byte patches too
// (applyBytePatches, after the module and the profile list, inside the
// growth guard), also returning the archive entry the new REL took (its
// path names the module's file) and the byte patches on the archive.
func patchRELSArc(arc []byte, moduleID uint32, newREL []byte, main ProfileTarget, extra []ProfileTarget, patches []BytePatch, maxGrowth int) ([]byte, stage.ArchiveFile, []string, error) {
	var none stage.ArchiveFile
	if moduleID == 0 || moduleID == ProfileListModule {
		return nil, none, nil, fmt.Errorf("rels: module 0x%x can't be replaced", moduleID)
	}
	if stage.IsYaz0(newREL) {
		return nil, none, nil, errors.New("rels: the new REL must be given uncompressed")
	}
	f, err := rel.Parse(newREL)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: new REL: %w", err)
	}
	if f.ID != moduleID {
		return nil, none, nil, fmt.Errorf("rels: the new REL is module 0x%x, not 0x%x", f.ID, moduleID)
	}
	if err := checkProfile(f, main); err != nil {
		return nil, none, nil, err
	}
	seen := map[uint16]bool{main.Proc: true}
	for _, p := range extra {
		if p.Proc == 0 || seen[p.Proc] {
			return nil, none, nil, fmt.Errorf("rels: process 0x%x given twice (or 0)", p.Proc)
		}
		seen[p.Proc] = true
		if err := checkProfile(f, p); err != nil {
			return nil, none, nil, err
		}
	}

	a, err := stage.ParseArchive(arc)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %w", err)
	}
	e, err := a.Edit()
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %w", err)
	}
	modFile, err := e.ReplaceByFirstWord(moduleID, newREL)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: module 0x%x: %w", moduleID, err)
	}

	// The profile list: its one relocation against the module.
	lstFile, lst, err := findByFirstWord(e, ProfileListModule)
	if err != nil {
		return nil, none, nil, err
	}
	lf, err := rel.Parse(lst)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	r, err := mainReloc(lf, moduleID, main.Proc)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	if r.Type != rel.RelocAddr32 {
		return nil, none, nil, fmt.Errorf("rels: %s: the relocation against module 0x%x is type 0x%x, not ADDR32", lstFile.Path, moduleID, r.Type)
	}
	r.TargetSection, r.Addend = main.Section, main.Offset
	listSection := r.Section
	for _, p := range extra {
		at := 4 * uint32(p.Proc)
		imp, idx := lf.RelocAt(listSection, at)
		if imp == nil {
			return nil, none, nil, fmt.Errorf("rels: %s: no relocation at process 0x%x's entry (section %d offset 0x%x)", lstFile.Path, p.Proc, listSection, at)
		}
		if t := imp.Relocs[idx].Type; t != rel.RelocAddr32 {
			return nil, none, nil, fmt.Errorf("rels: %s: process 0x%x's entry is type 0x%x, not ADDR32", lstFile.Path, p.Proc, t)
		}
		if imp.Module == 0 || imp.Module == lf.ID {
			return nil, none, nil, fmt.Errorf("rels: %s: process 0x%x's entry points into module 0x%x, not an actor module", lstFile.Path, p.Proc, imp.Module)
		}
		moved, err := lf.MoveReloc(imp, idx, moduleID)
		if err != nil {
			return nil, none, nil, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
		}
		moved.TargetSection, moved.Addend = p.Section, p.Offset
	}
	patched, err := lf.Bytes(true)
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	if err := e.Replace(lstFile, patched); err != nil {
		return nil, none, nil, fmt.Errorf("rels: %w", err)
	}
	var applied []string
	if len(patches) != 0 {
		// The module's REL and the profile list are rewritten whole.
		mod := []inject.Span{
			{File: modFile.Path, End: inject.WholeFile, Owner: "the mod's REL"},
			{File: lstFile.Path, End: inject.WholeFile, Owner: "the mod's profile list edit"},
		}
		if applied, err = applyBytePatches(e, patches, mod); err != nil {
			return nil, none, nil, err
		}
	}

	if g := e.Growth(); g > maxGrowth {
		return nil, none, nil, fmt.Errorf("rels: %w: %d bytes (%d -> %d), limit %d; shrink the REL",
			ErrRELSArcGrowth, g, len(arc), len(arc)+g, maxGrowth)
	}
	out, err := e.Bytes()
	if err != nil {
		return nil, none, nil, fmt.Errorf("rels: %w", err)
	}
	return out, modFile, applied, nil
}

// mainReloc is the module's own entry in the profile list lf: the sole
// relocation against moduleID (proc 0), or the one against it at offset
// 4*proc.
func mainReloc(lf *rel.File, moduleID uint32, proc uint16) (*rel.Reloc, error) {
	if proc == 0 {
		return lf.SoleReloc(moduleID)
	}
	var found *rel.Reloc
	n := 0
	for i := range lf.Imports {
		imp := &lf.Imports[i]
		if imp.Module != moduleID {
			continue
		}
		for j := range imp.Relocs {
			if r := &imp.Relocs[j]; r.Type != rel.RelocNop && r.Offset == 4*uint32(proc) {
				found = r
				n++
			}
		}
	}
	if n != 1 {
		return nil, fmt.Errorf("%d relocations against module 0x%x at process 0x%x's entry (offset 0x%x), want one", n, moduleID, proc, 4*uint32(proc))
	}
	return found, nil
}

// findByFirstWord returns the first file of e (in entry order) whose
// contents start with the big-endian word, expanded.
func findByFirstWord(e *stage.ArchiveEditor, word uint32) (stage.ArchiveFile, []byte, error) {
	for _, f := range e.Files() {
		b, err := e.Read(f)
		if err != nil {
			return stage.ArchiveFile{}, nil, fmt.Errorf("rels: %w", err)
		}
		if len(b) >= 4 && binary.BigEndian.Uint32(b) == word {
			return f, b, nil
		}
	}
	return stage.ArchiveFile{}, nil, fmt.Errorf("rels: no file starts with the module ID 0x%x", word)
}
