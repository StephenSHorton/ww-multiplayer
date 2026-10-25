package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphintool"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// Converting a compressed input (review idea 16, after datstarkey/ww-online
// WWOnline.Client/Services/DolphinTool.cs, MIT, which runs DolphinTool's
// extract; ours is a clean-room `convert`): an RVZ, WIA or GCZ disc is
// turned into a temporary plain ISO by the DolphinTool beside the player's
// Dolphin, next to the output, and the splice reads that. The temporary
// ISO is removed afterwards, whatever happened; the player's image is only
// read. NKit stays an error with its advice (inject.UnsupportedFormatError).

// Converter is the DolphinTool Run converts with.
type Converter struct {
	// Tool is the DolphinTool; Tool.Path "" means none was found (Run
	// then fails with the convert-in-Dolphin advice, saying where it
	// looked).
	Tool dolphintool.Tool
	// Dolphin is the Dolphin program it was looked for beside ("" when
	// none is known), for that message.
	Dolphin string
	// FreeBytes is the free space on a folder's volume (nil is
	// dolphintool.FreeBytes).
	FreeBytes func(dir string) (uint64, error)
}

// ConvertNeed is the free space a conversion asks for on the output's
// volume before it starts: the temporary ISO and the patched disc, each a
// full GameCube disc (0x57058000 bytes, as DolphinTool writes an ISO), and
// 64 MB besides.
const ConvertNeed = 2*0x57058000 + 64<<20

// ErrNoDolphinTool is what a *NoDolphinToolError is (errors.Is).
var ErrNoDolphinTool = errors.New("no DolphinTool found")

// NoDolphinToolError is Run's error for an RVZ, WIA or GCZ input when the
// Converter has no DolphinTool. It unwraps to ErrNoDolphinTool and to the
// input's *inject.UnsupportedFormatError, whose message says how to
// convert the disc in Dolphin by hand.
type NoDolphinToolError struct {
	Format *inject.UnsupportedFormatError
	// Dolphin is the Dolphin program it was looked for beside, "" when
	// none was known.
	Dolphin string
}

func (e *NoDolphinToolError) Error() string {
	where := "no Dolphin was found to look beside"
	if e.Dolphin != "" {
		where = "looked beside " + e.Dolphin
	}
	return fmt.Sprintf("%s (%s). %v", ErrNoDolphinTool, where, e.Format)
}

func (e *NoDolphinToolError) Unwrap() []error { return []error{ErrNoDolphinTool, e.Format} }

// ErrLowSpace is Run's error when the output's volume has less than
// ConvertNeed free for a conversion.
var ErrLowSpace = errors.New("not enough free space to convert the disc")

// convertInput converts in into a temporary ISO beside out when in is an
// RVZ, WIA or GCZ image. It returns the image to splice (in itself when
// no conversion is needed), the format converted from ("" when none) and
// a cleanup that removes the temporary ISO (a no-op when none).
func convertInput(ctx context.Context, in, out string, w io.Writer, c *Converter) (src, format string, cleanup func(), err error) {
	cleanup = func() {}
	u, ferr := inject.ImageFormat(in)
	if c == nil || ferr != nil || u == nil || !u.Convertible() {
		// No converter, or not one DolphinTool converts (or unreadable):
		// the patcher's own errors say what it is.
		return in, "", cleanup, nil
	}
	if c.Tool.Path == "" {
		return "", "", cleanup, &NoDolphinToolError{Format: u, Dolphin: c.Dolphin}
	}
	if fi, err := os.Stat(out); err == nil {
		if ii, err := os.Stat(in); err == nil && os.SameFile(fi, ii) {
			return "", "", cleanup, fmt.Errorf("%w: %s; choose a different output path (the patcher never overwrites your original)",
				inject.ErrOutputIsInput, out)
		}
	}
	dir := filepath.Dir(out)
	free := c.FreeBytes
	if free == nil {
		free = dolphintool.FreeBytes
	}
	if n, err := free(dir); err == nil && n < ConvertNeed {
		return "", "", cleanup, fmt.Errorf("%w: converting needs about %.1f GB free in %s and it has %.1f GB",
			ErrLowSpace, gb(ConvertNeed), dir, gb(n))
	} else if err != nil {
		fmt.Fprintf(w, "  convert: (free space in %s unknown: %v; trying anyway)\n", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(out)+".converting-*.iso")
	if err != nil {
		return "", "", cleanup, fmt.Errorf("can't make the temporary ISO in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	// ToISO wants a path that does not exist yet (CreateTemp only picked
	// a free name).
	os.Remove(tmpPath)
	cleanup = func() { os.Remove(tmpPath) }

	fmt.Fprintf(w, "  convert: %s image -> plain ISO with %s\n", u.Format, c.Tool.Path)
	if err := c.Tool.ToISO(ctx, in, tmpPath, func(l string) { fmt.Fprintf(w, "  DolphinTool: %s\n", l) }); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	fmt.Fprintf(w, "  convert: done, patching the ISO it wrote (removed afterwards)\n")
	return tmpPath, u.Format, cleanup, nil
}

func gb(n uint64) float64 { return float64(n) / (1 << 30) }

// ConverterFor is the Converter for the Dolphin program at dolphinExe
// ("" when none is known): the DolphinTool beside it, if any.
func ConverterFor(dolphinExe string) *Converter {
	return &Converter{Tool: dolphintool.Tool{Path: dolphintool.Find(dolphinExe, nil)}, Dolphin: dolphinExe}
}

// NeedsConvert says the image at path is one a Converter turns into an
// ISO (RVZ, WIA or GCZ), so a caller looks for Dolphin only then.
func NeedsConvert(path string) bool {
	u, _ := inject.ImageFormat(path)
	return u != nil && u.Convertible()
}
