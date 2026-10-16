// Package patch is the `patch` command as a function: it splices the
// multiplayer mod into the user's own Wind Waker disc image
// (inject.PatchISO) and says what it did, in the words the CLI has always
// printed, on a writer. It returns errors instead of exiting, so the
// first-run setup of the desktop app (docs/01-architecture.md) can run it
// with a progress view and the CLI keeps its output and exit codes.
package patch

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// Options configures Run.
type Options struct {
	// CoopTables, when non-nil, builds, caches and records the co-op stage
	// tables of image, the disc Dolphin will run, and writes one line about
	// it on w. It never fails the patch. package main passes its
	// cacheCoopTablesForPatch; nil skips the step.
	CoopTables func(w io.Writer, image string)
}

// Result is what Run did.
type Result struct {
	In, Out string
	// AlreadyPatched says the input already holds this build's mod: nothing
	// was written, and Out was not created. The input is the disc to boot.
	AlreadyPatched bool
	// FST is inject.PatchISO's result, zero when AlreadyPatched.
	FST inject.Result
}

// Image is the disc to boot after a successful Run: Out, or In when it was
// already patched.
func (r Result) Image() string {
	if r.AlreadyPatched {
		return r.In
	}
	return r.Out
}

// DefaultOut is the output path `patch <in>` uses without one: the input
// with its extension (if any) replaced by "-multiplayer.iso", next to it.
func DefaultOut(in string) string {
	base := in
	// Strip the extension before tagging "-multiplayer.iso" so we don't
	// produce things like "wind-waker.ciso-multiplayer.iso".
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			base = base[:i]
			break
		}
		if base[i] == '/' || base[i] == '\\' {
			break
		}
	}
	return base + "-multiplayer.iso"
}

// Run patches in into out (DefaultOut(in) when out is ""), writing the
// patcher's lines on w. An input that already holds this build's mod is not
// an error (Result.AlreadyPatched). Every failure is written on w as the CLI
// prints it and returned too: one made by a different build wraps
// inject.ErrPatchedByOtherVersion, and the rest are inject.PatchISO's
// errors. ctx is checked before the disc is read; the patch itself is not
// interruptible.
func Run(ctx context.Context, in, out string, w io.Writer, o Options) (Result, error) {
	if out == "" {
		out = DefaultOut(in)
	}
	res := Result{In: in, Out: out}
	fmt.Fprintf(w, "Wind Waker Multiplayer patcher\n")
	fmt.Fprintf(w, "  input : %s\n", in)
	fmt.Fprintf(w, "  output: %s\n", out)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	coop := func(image string) {
		if o.CoopTables != nil {
			o.CoopTables(w, image)
		}
	}
	fst, err := inject.PatchISO(in, out)
	if err != nil {
		switch {
		case errors.Is(err, inject.ErrAlreadyPatched):
			fmt.Fprintf(w, "\nThis ISO already contains this version of the multiplayer mod — nothing to do.\n")
			fmt.Fprintf(w, "(Detected an existing T2 section at 0x%08X.)\n", inject.T2Address)
			res.AlreadyPatched = true
			coop(in) // the disc Dolphin will run; never fails the patch
			return res, nil
		case errors.Is(err, inject.ErrPatchedByOtherVersion):
			fmt.Fprintf(w, "\nThis ISO was patched by a different ww-multiplayer version.\n")
			fmt.Fprintf(w, "Patch your vanilla (unmodified) Wind Waker ISO with this version instead.\n")
			return res, err
		}
		fmt.Fprintf(w, "\nERROR: %v\n", err)
		return res, err
	}
	res.FST = fst
	if fst.FSTMoved() {
		fmt.Fprintf(w, "  FST   : moved 0x%X -> 0x%X (0x%X bytes) to make room for the mod\n",
			fst.OldFSTOffset, fst.NewFSTOffset, fst.FSTSize)
	}
	// The co-op stage tables of the disc just written, for --coop
	// sessions: read-only on it, and a warning at worst.
	coop(out)
	fmt.Fprintf(w, "\nDone. Boot %s in Dolphin to play multiplayer.\n", out)
	return res, nil
}
