package patch

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
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
