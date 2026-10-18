package rels

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/rel"
)

// Module is a REL module to ship on the disc: a relocatable ELF
// (inject/rel, built by inject/build.py) that takes an actor module's
// place in RELS.arc. Blob is this build's; the zero value (no ELF) is a
// build without one, which patches the DOL alone.
type Module struct {
	// ELF is the relocatable ELF, or empty for no module.
	ELF []byte
	// ID is the module ID the REL is written with: the module it
	// replaces (inject.RELModuleID, d_a_rectangle.rel's).
	ID uint32
	// Profile names the actor profile in the ELF (inject.RELProfileSymbol)
	// that the profile list's entry for the module is pointed at.
	Profile string
	// BuildID is the id the module's _prolog writes into the mailbox
	// slack (inject.RELBuildID), reported by the patcher.
	BuildID uint32
	// MaxGrowth bounds how much the archive may grow, in stored bytes;
	// 0 means RELSArcMaxGrowth.
	MaxGrowth int
}

// Blob is this build's module, from the generated internal/inject/blob.go.
func Blob() Module {
	return Module{
		ELF: inject.RELELF, ID: inject.RELModuleID,
		Profile: inject.RELProfileSymbol, BuildID: inject.RELBuildID,
	}
}

// Empty reports whether there is no module to ship (blob.go generated
// without devkitPPC: RELELF empty).
func (m Module) Empty() bool { return len(m.ELF) == 0 }

// Built is a RELS.arc with the module in it, and what went in.
type Built struct {
	Arc  []byte // the patched archive
	File string // the archive path of the entry the REL took: "mmem/d_a_rectangle.rel"
	REL  []byte // the REL as written, uncompressed
	// Profile is where the actor profile landed in the REL: the
	// section and offset the profile list's entry now points at.
	Profile rel.Symbol
	// Growth is how many bytes bigger than the archive given the patched
	// one is (negative when smaller).
	Growth int
}

// Build converts the ELF to a REL (rel.FromELF) and returns vanilla,
// the disc's RELS.arc, with the REL in module ID's place and the profile
// list's entry for it pointed at Profile (PatchRELSArc, under
// MaxGrowth). The error names the step that failed. An Empty module is
// an error: the caller decides what a build without one means.
func (m Module) Build(vanilla []byte) (Built, error) {
	if m.Empty() {
		return Built{}, errors.New("rels: no REL module in this build")
	}
	f, lay, err := rel.FromELF(m.ELF, m.ID, rel.ELFOptions{})
	if err != nil {
		return Built{}, fmt.Errorf("rels: ELF to REL: %w", err)
	}
	section, offset, ok := lay.Symbol(m.Profile)
	if !ok {
		return Built{}, fmt.Errorf("rels: ELF to REL: no %s symbol in the module (the actor profile)", m.Profile)
	}
	relBytes, err := f.Bytes(false)
	if err != nil {
		return Built{}, fmt.Errorf("rels: write REL: %w", err)
	}
	maxGrowth := m.MaxGrowth
	if maxGrowth == 0 {
		maxGrowth = RELSArcMaxGrowth
	}
	arc, file, err := patchRELSArc(vanilla, m.ID, relBytes, section, offset, maxGrowth)
	if err != nil {
		return Built{}, fmt.Errorf("patch RELS.arc: %w", err)
	}
	return Built{
		Arc: arc, File: file.Path, REL: relBytes,
		Profile: rel.Symbol{Section: section, Offset: offset},
		Growth:  len(arc) - len(vanilla),
	}, nil
}

// BuildRELSArc returns vanilla, the disc's RELS.arc, with this build's
// module in it (Blob().Build).
func BuildRELSArc(vanilla []byte) ([]byte, error) {
	b, err := Blob().Build(vanilla)
	if err != nil {
		return nil, err
	}
	return b.Arc, nil
}

// Result is what a module's patch did: the image patcher's placements
// and, when the build has a module, what went on the disc.
type Result struct {
	inject.Result
	// HasREL says a module went on the disc: false for an Empty module,
	// whose patch is the DOL alone (RELSArcReplaced is false too).
	HasREL bool
	// The module shipped, when HasREL: its ID, the archive entry it
	// took, its build id, its uncompressed size and the archive's
	// growth in stored bytes.
	ModuleID uint32
	File     string
	BuildID  uint32
	RELSize  int
	Growth   int
}

// PatchISOWithREL patches the disc image at inPath into outPath as
// inject.PatchISO does, with this build's REL module in its RELS.arc
// (Blob().PatchISO).
func PatchISOWithREL(inPath, outPath string) (Result, error) {
	return Blob().PatchISO(inPath, outPath)
}

// PatchISO patches the disc image (ISO or CISO) at inPath into outPath:
// inject.PatchISOWith with the module's RELS.arc built from the disc's
// (Build), or the DOL alone for an Empty module. The errors are
// inject.PatchISO's: an image this build patched (DOL and archive) is
// inject.ErrAlreadyPatched, one by another build
// inject.ErrPatchedByOtherVersion, and a Build failure (the ARAM growth
// guard, ErrRELSArcGrowth, among them) is returned wrapped.
func (m Module) PatchISO(inPath, outPath string) (Result, error) {
	var b Built
	res, err := inject.PatchISOWith(inPath, outPath, m.patch(&b))
	return m.result(res, b), err
}

// PatchISOInMemory is PatchISO on an image in memory
// (inject.PatchISOInMemoryWith).
func (m Module) PatchISOInMemory(iso []byte) ([]byte, Result, error) {
	var b Built
	out, res, err := inject.PatchISOInMemoryWith(iso, m.patch(&b))
	return out, m.result(res, b), err
}

// patch is the inject.Patch for the module: a builder that records
// what it built in b, or nothing for an Empty module.
func (m Module) patch(b *Built) inject.Patch {
	if m.Empty() {
		return inject.Patch{}
	}
	return inject.Patch{RELSArcFrom: func(disc []byte) ([]byte, error) {
		built, err := m.Build(disc)
		if err != nil {
			return nil, err
		}
		*b = built
		return built.Arc, nil
	}}
}

func (m Module) result(res inject.Result, b Built) Result {
	r := Result{Result: res}
	if !m.Empty() && res.RELSArcReplaced() {
		r.HasREL, r.ModuleID, r.File, r.BuildID = true, m.ID, b.File, m.BuildID
		r.RELSize, r.Growth = len(b.REL), b.Growth
	}
	return r
}
