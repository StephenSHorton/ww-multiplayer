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
	"os"
	"path"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject/rels"
)

// Options configures Run.
type Options struct {
	// PostPatch are the steps that run, in order, once the patched disc
	// is written or found already patched: image is the disc Dolphin
	// will run. Each writes its line(s) on w and never fails the patch
	// (it has no error to return). package main passes the loaded mods'
	// steps (wwapi.PostPatcher): co-op's builds, caches and records the
	// co-op stage tables (stagetables.CacheForPatch). None skips them.
	PostPatch []func(w io.Writer, image string)
	// Converter, when set, turns an RVZ, WIA or GCZ input into a
	// temporary plain ISO with Dolphin's DolphinTool before the splice
	// (convert.go). Nil leaves those inputs an error with the
	// convert-in-Dolphin advice, as before.
	Converter *Converter
	// RELPatches are optional byte patches to vanilla RELs that go into
	// RELS.arc with the mod (rels.SeaHeapFix, `patch --fix-sea-heap`).
	// None by default. They are local: players with and without them
	// play together. A converted input gets them too.
	RELPatches []rels.BytePatch
}

// Result is what Run did.
type Result struct {
	In, Out string
	// AlreadyPatched says the input already holds this build's mod: nothing
	// was written, and Out was not created. The input is the disc to boot,
	// unless it was converted (ConvertedFrom): then the converted ISO was
	// kept at Out, and that is.
	AlreadyPatched bool
	// ConvertedFrom is the input's format ("RVZ", "WIA" or "GCZ") when
	// Options.Converter turned it into a plain ISO first, else "".
	ConvertedFrom string
	// FST is the image patcher's result, zero when AlreadyPatched.
	FST inject.Result
	// REL is the REL module's (rels.PatchISOWithREL: FST and what went
	// into RELS.arc), zero when AlreadyPatched.
	REL rels.Result
}

// Image is the disc to boot after a successful Run: Out, or In when it was
// already patched (and not converted).
func (r Result) Image() string {
	if r.AlreadyPatched && r.ConvertedFrom == "" {
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
// errors. ctx is checked before the disc is read and stops a conversion
// (Options.Converter); the patch itself is not interruptible.
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
	post := func(image string) {
		for _, step := range o.PostPatch {
			step(w, image)
		}
	}
	src, format, cleanup, err := convertInput(ctx, in, out, w, o.Converter)
	defer cleanup()
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(w, "\nERROR: %v\n", err)
		}
		return res, err
	}
	res.ConvertedFrom = format
	m := rels.Blob()
	m.Patches = o.RELPatches
	r, err := m.PatchISO(src, out)
	fst := r.Result
	if err != nil {
		switch {
		case errors.Is(err, inject.ErrAlreadyPatched) && format != "":
			// A converted disc that already holds this build's mod: the
			// ISO DolphinTool wrote is the disc to boot (Dolphin plays the
			// input too, but the tables step can't read it), so it is kept
			// at out.
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if err := os.Rename(src, out); err != nil {
				fmt.Fprintf(w, "\nERROR: keep the converted ISO: %v\n", err)
				return res, err
			}
			fmt.Fprintf(w, "\nThis ISO already contains this version of the multiplayer mod — nothing to do.\n")
			fmt.Fprintf(w, "(Detected an existing T2 section at 0x%08X.)\n", inject.T2Address)
			fmt.Fprintf(w, "Kept the converted ISO as %s.\n", out)
			res.AlreadyPatched = true
			post(out)
			return res, nil
		case errors.Is(err, inject.ErrAlreadyPatched):
			fmt.Fprintf(w, "\nThis ISO already contains this version of the multiplayer mod — nothing to do.\n")
			fmt.Fprintf(w, "(Detected an existing T2 section at 0x%08X.)\n", inject.T2Address)
			res.AlreadyPatched = true
			post(in) // the disc Dolphin will run; never fails the patch
			return res, nil
		case errors.Is(err, inject.ErrPatchedByOtherVersion):
			fmt.Fprintf(w, "\nThis ISO was patched by a different ww-multiplayer version.\n")
			fmt.Fprintf(w, "Patch your vanilla (unmodified) Wind Waker ISO with this version instead.\n")
			return res, err
		}
		fmt.Fprintf(w, "\nERROR: %v\n", err)
		return res, err
	}
	res.FST, res.REL = fst, r
	if fst.FSTMoved() {
		fmt.Fprintf(w, "  FST   : moved 0x%X -> 0x%X (0x%X bytes) to make room for the mod\n",
			fst.OldFSTOffset, fst.NewFSTOffset, fst.FSTSize)
	}
	if r.HasREL {
		fmt.Fprintf(w, "  REL   : module 0x%X (%s) -> wwrel build 0x%08X, RELS.arc at 0x%X (%+d bytes)\n",
			r.ModuleID, path.Base(r.File), r.BuildID, fst.RELSArcOffset, r.Growth)
	} else {
		fmt.Fprintf(w, "  REL   : none (this build has no REL module)\n")
	}
	for _, name := range r.Patches {
		fmt.Fprintf(w, "  fix   : %s (local only)\n", name)
	}
	// The post-patch steps on the disc just written (co-op's stage
	// tables, for --coop sessions): read-only on it, a warning at worst.
	post(out)
	fmt.Fprintf(w, "\nDone. Boot %s in Dolphin to play multiplayer.\n", out)
	return res, nil
}
