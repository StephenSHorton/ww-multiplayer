package rels

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// BytePatch is an optional edit to one vanilla REL of RELS.arc, made in
// place (rel.PatchBytes): a few words changed, the relocations under
// them disabled, the rest of the REL as on the disc. It is applied only
// to the exact vanilla REL (VanillaSHA1) or found applied already, so it
// never lands on a REL it was not written for.
type BytePatch struct {
	// Name is the patch's name in the patcher's output.
	Name string
	// File is the REL's path in RELS.arc ("mmem/d_a_lod_bg.rel").
	File string
	// VanillaSHA1 is the SHA-1 (hex) of the vanilla REL: the hash the
	// tww decomp's config/GZLE01/config.yml gives the object. The
	// expanded REL or its stored (Yaz0) bytes may match it.
	VanillaSHA1 string
	// Writes are the edits.
	Writes []rel.Write
	// Check, when set, vets the vanilla REL before the writes: the
	// relocations and call sites the patch is reasoned from.
	Check func(f *rel.File) error
}

// spans are p's writes as file offsets in the expanded REL f.
func (p BytePatch) spans(f *rel.File) ([]inject.Span, error) {
	out := make([]inject.Span, len(p.Writes))
	for i, w := range p.Writes {
		if int(w.Section) >= len(f.Sections) || f.Sections[w.Section].Offset == 0 {
			return nil, fmt.Errorf("rels: %s: section %d has no data", p.Name, w.Section)
		}
		s := uint64(f.Sections[w.Section].Offset) + uint64(w.Offset)
		out[i] = inject.Span{File: p.File, Start: s, End: s + uint64(len(w.New)), Owner: p.Name}
	}
	return out, nil
}

// applyBytePatches applies patches to the archive e, after the mod's own
// edits, which wrote the files named in mod (each a whole-file span):
// the writes are checked against each other and against mod
// (inject.CheckOverlaps) before anything is written. A patch already on
// its REL leaves the stored entry as it is. It returns the names of the
// patches on the archive, applied now or before.
func applyBytePatches(e *stage.ArchiveEditor, patches []BytePatch, mod []inject.Span) ([]string, error) {
	type job struct {
		p    BytePatch
		file stage.ArchiveFile
		raw  []byte
		f    *rel.File
	}
	var jobs []job
	var writes []inject.Span
	for _, p := range patches {
		i := slices.IndexFunc(e.Files(), func(f stage.ArchiveFile) bool { return f.Path == p.File })
		if i < 0 {
			return nil, fmt.Errorf("rels: %s: no %s in RELS.arc", p.Name, p.File)
		}
		file := e.Files()[i]
		raw, err := e.Read(file)
		if err != nil {
			return nil, fmt.Errorf("rels: %s: %w", p.Name, err)
		}
		f, err := rel.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("rels: %s: %s: %w", p.Name, p.File, err)
		}
		sp, err := p.spans(f)
		if err != nil {
			return nil, err
		}
		writes = append(writes, sp...)
		jobs = append(jobs, job{p, file, raw, f})
	}
	if err := inject.CheckOverlaps(append(slices.Clone(mod), writes...), nil); err != nil {
		return nil, fmt.Errorf("rels: %w", err)
	}

	var names []string
	for _, j := range jobs {
		vanilla := sha1Hex(j.raw) == j.p.VanillaSHA1
		if !vanilla {
			// The stored bytes, in case the hash is of the Yaz0 file.
			stored, err := storedBytes(e, j.file)
			if err != nil {
				return nil, err
			}
			vanilla = sha1Hex(stored) == j.p.VanillaSHA1
		}
		if vanilla && j.p.Check != nil {
			if err := j.p.Check(j.f); err != nil {
				return nil, fmt.Errorf("rels: %s: %s: %w", j.p.Name, j.p.File, err)
			}
		}
		out, st, err := rel.PatchBytes(j.raw, j.p.Writes)
		if err != nil {
			return nil, fmt.Errorf("rels: %s: %s: %w", j.p.Name, j.p.File, err)
		}
		switch {
		case vanilla && st.Already:
			return nil, fmt.Errorf("rels: %s: %s matches the vanilla hash but holds the patched bytes", j.p.Name, j.p.File)
		case !vanilla && !st.Already:
			return nil, fmt.Errorf("rels: %s: %s is not the GZLE01 vanilla REL (SHA-1 %s, want %s)", j.p.Name, j.p.File, sha1Hex(j.raw), j.p.VanillaSHA1)
		}
		if !bytes.Equal(out, j.raw) {
			if err := e.Replace(j.file, out); err != nil {
				return nil, fmt.Errorf("rels: %s: %w", j.p.Name, err)
			}
		}
		names = append(names, j.p.Name)
	}
	return names, nil
}

// storedBytes is f's entry as the archive stores it (Yaz0 for a
// compressed one): the expanded bytes re-encoded the way the disc's
// were (stage.EncodeYaz0 reproduces every vanilla REL, TestEncodeYaz0VanillaRELs).
func storedBytes(e *stage.ArchiveEditor, f stage.ArchiveFile) ([]byte, error) {
	raw, err := e.Read(f)
	if err != nil {
		return nil, err
	}
	if !f.Compressed() {
		return raw, nil
	}
	return stage.EncodeYaz0(raw), nil
}

func sha1Hex(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:])
}

// The sea-heap fix's facts, from the tww decomp (GZLE01): the REL's
// path and object hash (config/GZLE01/config.yml), its .text functions
// (config/GZLE01/rels/d_a_lod_bg/symbols.txt) and the main.dol callees
// (config/GZLE01/symbols.txt).
const (
	lodBgFile  = "mmem/d_a_lod_bg.rel"
	lodBgSHA1  = "18fc91dbef845d2268dc14d4822855f64bacb23f"
	lodBgText  = 1      // .text, the first section (splits.txt)
	lodBgTextN = 0x133C // its size: daLodbg_Create ends there

	lodBgDeleteModelData = 0x31C // deleteModelData__9daLodbg_cFv, 0x31C..0x46C
	lodBgDeleteModelEnd  = 0x46C
	lodBgReadWait        = 0xB4C // execReadWait__9daLodbg_cFv, 0xB4C..0xD68
	lodBgReadWaitEnd     = 0xD68

	// The patched words, as .text offsets: betterww's file offsets 0xDCC,
	// 0x478 and 0x4D0 less 0xD4, where .text would start in the file
	// after a version-3 header (0x4C bytes) and 17 section entries.
	// That start is inferred, not read (no vanilla REL in the repo), but
	// all three land inside the functions the fix is about, and Check
	// confirms them against the disc's relocations before any write.
	lodBgEstimateAt = 0xCF8 // li r5, 0: fopAcM_entrySolidHeap's estimate, in execReadWait
	lodBgFree1At    = 0x3A4 // bl JKRHeap::free: JKRFreeToHeap(NULL, mModelData)
	lodBgFree2At    = 0x3FC // bl JKRHeap::free: JKRFreeToHeap(NULL, mModelData2)

	fopAcMEntrySolidHeap = 0x80024D24 // fopAcM_entrySolidHeap__FP10fopAc_ac_cPFP10fopAc_ac_c_iUl
	jkrHeapFree          = 0x802B0518 // free__7JKRHeapFPvP7JKRHeap, what JKRFreeToHeap(heap, ptr) calls
)

var (
	ppcNop = []byte{0x60, 0x00, 0x00, 0x00}
	// ppcBl is an unresolved `bl` as a REL stores it (offset 0, LK set):
	// the REL24 relocation fills the offset in when the module links.
	// Unverified offline (no vanilla REL in the repo): the vanilla-disc
	// test TestSeaHeapFixVanilla checks it.
	ppcBl = []byte{0x48, 0x00, 0x00, 0x01}
)

// SeaHeapFix is fix_sea_heap_fragmentation, betterww's
// fix_vanilla_bugs.asm (MIT) as datstarkey/ww-online ports it
// (GameMod/src/patches/optional/fix_sea_heap_fragmentation.asm, MIT),
// rewritten here as data for rel.PatchBytes. It edits only
// d_a_lod_bg.rel, the sea's far-island LOD model actor (LODBG):
//
//   - execReadWait calls fopAcM_entrySolidHeap(this, createHeapCallBack, 0)
//     (d_a_lod_bg.cpp:255). With an estimate of 0 the actor's model heap
//     is made from the largest free block of the game heap and then
//     shrunk to fit (f_op_actor_mng.cpp:354-373, mDoExt_adjustSolidHeap),
//     so each island's small heap lands wherever the biggest hole was.
//     Sailing creates and deletes these actors over and over, and the
//     game heap ends up cut into pieces. The fix passes 0xEF0, betterww's
//     measured maximum (Windfall; most islands need 0x4B0): the heap is
//     then an ordinary allocation of that size (f_op_actor_mng.cpp:315-340).
//     An island needing more falls back to the old path, as vanilla.
//   - deleteModelData frees mModelData and mModelData2 with
//     JKRFreeToHeap(NULL, ...) (d_a_lod_bg.cpp:69, :81), but both were
//     loaded into the actor's own JKRSolidHeap (loadModelData,
//     d_a_lod_bg.cpp:136-137), which can't free a block: it only prints
//     "free: cannot free memory block" (JKRSolidHeap.cpp:150-152). The
//     heap's destroy just after reclaims them. The fix nops the two calls
//     (their REL24 relocations become R_DOLPHIN_NOP, or the loader would
//     put the bl back).
//
// Why it matters here: remote Links' full bodies and the puppets live in
// the game heap too (wwapi/heap's full-body gate reads its largest free
// block), so a long sail can starve them. It is local only: no player
// check covers d_a_lod_bg.rel (the hello compares protocol versions, the
// contract check hashes T2 in RAM, the REL build id is the mod REL's),
// so players with and without it play together.
var SeaHeapFix = BytePatch{
	Name:        "sea heap fix",
	File:        lodBgFile,
	VanillaSHA1: lodBgSHA1,
	Writes: []rel.Write{
		{Section: lodBgText, Offset: lodBgEstimateAt, Old: []byte{0x38, 0xA0, 0x00, 0x00}, New: []byte{0x38, 0xA0, 0x0E, 0xF0}}, // li r5, 0 -> li r5, 0xEF0
		{Section: lodBgText, Offset: lodBgFree1At, Old: ppcBl, New: ppcNop},
		{Section: lodBgText, Offset: lodBgFree2At, Old: ppcBl, New: ppcNop},
	},
	Check: checkLodBg,
}

// checkLodBg confirms on the disc's d_a_lod_bg.rel what SeaHeapFix is
// reasoned from: .text is the decomp's size; a REL24 into
// fopAcM_entrySolidHeap follows the li r5 within four words, inside
// execReadWait; and deleteModelData has six calls into JKRHeap::free,
// the source's six JKRFreeToHeap in order (mModelData, its binary,
// mDataHeap, then the same for the second model), of which the patch
// takes the first and the fourth, the two model data frees.
func checkLodBg(f *rel.File) error {
	if len(f.Sections) <= lodBgText || !f.Sections[lodBgText].Exec || len(f.Sections[lodBgText].Data) != lodBgTextN {
		return fmt.Errorf("section %d is not the decomp's .text (0x%x bytes of code)", lodBgText, lodBgTextN)
	}
	imp := f.Import(0)
	if imp == nil {
		return fmt.Errorf("no relocations against main.dol")
	}
	var frees []uint32
	entry := false
	for _, r := range imp.Relocs {
		if r.Section != lodBgText || r.Type != rel.RelocRel24 {
			continue
		}
		switch {
		case r.Addend == jkrHeapFree && r.Offset >= lodBgDeleteModelData && r.Offset < lodBgDeleteModelEnd:
			frees = append(frees, r.Offset)
		case r.Addend == fopAcMEntrySolidHeap && r.Offset > lodBgEstimateAt && r.Offset <= lodBgEstimateAt+16 && r.Offset < lodBgReadWaitEnd:
			entry = true
		}
	}
	if lodBgEstimateAt < lodBgReadWait || !entry {
		return fmt.Errorf("no bl fopAcM_entrySolidHeap just after .text 0x%x", lodBgEstimateAt)
	}
	slices.Sort(frees)
	if len(frees) != 6 || frees[0] != lodBgFree1At || frees[3] != lodBgFree2At {
		return fmt.Errorf("deleteModelData's JKRHeap::free calls are at %x, want six with the first at 0x%x and the fourth at 0x%x",
			frees, lodBgFree1At, lodBgFree2At)
	}
	return nil
}
