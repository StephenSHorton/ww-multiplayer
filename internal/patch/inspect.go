package patch

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
)

// DiscState is what Inspect finds on a disc image.
type DiscState int

const (
	// Vanilla is an unpatched GZLE01 disc: Run patches it.
	Vanilla DiscState = iota
	// Patched holds this build's mod: the disc to boot as it is.
	Patched
	// OtherVersion was patched by a different ww-multiplayer build; patch
	// the vanilla disc with this one instead.
	OtherVersion
)

func (s DiscState) String() string {
	switch s {
	case Vanilla:
		return "vanilla"
	case Patched:
		return "patched"
	case OtherVersion:
		return "patched by another version"
	}
	return fmt.Sprintf("DiscState(%d)", int(s))
}

// Inspect says whether the ISO or CISO image at path is a vanilla Wind
// Waker (GZLE01) disc, one patched by this build, or one patched by
// another. It reads only the disc header, FST and main.dol (a few MB), so
// the app's "I already have a patched ISO" choice can check a disc at
// once. The error is inject.OpenDisc's for a file that is not a GZLE01
// image it can read.
func Inspect(path string) (DiscState, error) {
	d, err := inject.OpenDisc(path)
	if err != nil {
		return Vanilla, err
	}
	defer d.Close()
	dol, err := d.ReadDOL()
	if err != nil {
		return Vanilla, err
	}
	_, err = inject.PatchDOL(dol)
	switch {
	case err == nil:
		return Vanilla, nil
	case errors.Is(err, inject.ErrAlreadyPatched):
		return Patched, nil
	case errors.Is(err, inject.ErrPatchedByOtherVersion):
		return OtherVersion, nil
	}
	return Vanilla, err
}

// InspectFull is Inspect plus the disc's RELS.arc: a disc whose DOL is
// this build's is Patched only when its archive is the one this build
// writes too. A release that changes only the REL leaves the DOL alone, so
// Inspect alone would read the last release's disc as current. It reads a
// few MB more (the archive) and writes nothing.
//
// What this build writes there is deterministic: its module built over
// the disc's own archive (the vanilla one or one a build wrote, as Run
// does), with the optional fixes (rels.Optional) the disc already has
// kept. The disc is current when that rebuild is the archive it has.
func InspectFull(path string) (DiscState, error) {
	st, err := Inspect(path)
	if err != nil || st != Patched {
		return st, err
	}
	m := rels.Blob()
	if m.Empty() && len(m.Patches) == 0 {
		return st, nil // a build without a module has no archive to compare
	}
	d, err := inject.OpenDisc(path)
	if err != nil {
		return Vanilla, err
	}
	defer d.Close()
	arc, err := d.ReadFile("RELS.arc")
	if err != nil {
		return Vanilla, err
	}
	on, err := rels.PatchesOn(arc, knownFixes)
	if err != nil {
		return OtherVersion, nil // an archive this build cannot read is not its own
	}
	m.Patches = append(append([]rels.BytePatch(nil), m.Patches...), on...)
	var want []byte
	if m.Empty() {
		want, _, err = rels.PatchesOnly(arc, m.Patches, m.MaxGrowth)
	} else {
		var b rels.Built
		b, err = m.Build(arc)
		want = b.Arc
	}
	if err != nil || !bytes.Equal(want, arc) {
		return OtherVersion, nil
	}
	return Patched, nil
}
