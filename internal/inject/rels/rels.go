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
	out, _, err := patchRELSArc(arc, moduleID, newREL, profileSection, profileOffset, maxGrowth)
	return out, err
}

// patchRELSArc is PatchRELSArc, also returning the archive entry the
// new REL took (its path names the module's file).
func patchRELSArc(arc []byte, moduleID uint32, newREL []byte, profileSection uint8, profileOffset uint32, maxGrowth int) ([]byte, stage.ArchiveFile, error) {
	var none stage.ArchiveFile
	if moduleID == 0 || moduleID == ProfileListModule {
		return nil, none, fmt.Errorf("rels: module 0x%x can't be replaced", moduleID)
	}
	if stage.IsYaz0(newREL) {
		return nil, none, errors.New("rels: the new REL must be given uncompressed")
	}
	f, err := rel.Parse(newREL)
	if err != nil {
		return nil, none, fmt.Errorf("rels: new REL: %w", err)
	}
	if f.ID != moduleID {
		return nil, none, fmt.Errorf("rels: the new REL is module 0x%x, not 0x%x", f.ID, moduleID)
	}
	if int(profileSection) >= len(f.Sections) || f.Sections[profileSection].Offset == 0 {
		return nil, none, fmt.Errorf("rels: the new REL has no data in section %d for the profile", profileSection)
	}
	if uint64(profileOffset) >= uint64(len(f.Sections[profileSection].Data)) {
		return nil, none, fmt.Errorf("rels: profile at 0x%x is past the end of the new REL's section %d (0x%x bytes)",
			profileOffset, profileSection, len(f.Sections[profileSection].Data))
	}

	a, err := stage.ParseArchive(arc)
	if err != nil {
		return nil, none, fmt.Errorf("rels: %w", err)
	}
	e, err := a.Edit()
	if err != nil {
		return nil, none, fmt.Errorf("rels: %w", err)
	}
	modFile, err := e.ReplaceByFirstWord(moduleID, newREL)
	if err != nil {
		return nil, none, fmt.Errorf("rels: module 0x%x: %w", moduleID, err)
	}

	// The profile list: its one relocation against the module.
	lstFile, lst, err := findByFirstWord(e, ProfileListModule)
	if err != nil {
		return nil, none, err
	}
	lf, err := rel.Parse(lst)
	if err != nil {
		return nil, none, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	r, err := lf.SoleReloc(moduleID)
	if err != nil {
		return nil, none, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	if r.Type != rel.RelocAddr32 {
		return nil, none, fmt.Errorf("rels: %s: the relocation against module 0x%x is type 0x%x, not ADDR32", lstFile.Path, moduleID, r.Type)
	}
	r.TargetSection, r.Addend = profileSection, profileOffset
	patched, err := lf.Bytes(true)
	if err != nil {
		return nil, none, fmt.Errorf("rels: %s: %w", lstFile.Path, err)
	}
	if err := e.Replace(lstFile, patched); err != nil {
		return nil, none, fmt.Errorf("rels: %w", err)
	}

	if g := e.Growth(); g > maxGrowth {
		return nil, none, fmt.Errorf("rels: %w: %d bytes (%d -> %d), limit %d; shrink the REL",
			ErrRELSArcGrowth, g, len(arc), len(arc)+g, maxGrowth)
	}
	out, err := e.Bytes()
	if err != nil {
		return nil, none, fmt.Errorf("rels: %w", err)
	}
	return out, modFile, nil
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
