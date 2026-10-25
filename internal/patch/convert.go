package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// Converter is the DolphinTool Run converts with: Tool, beside Dolphin,
// else the first of More that is usable (dolphintool.Tool.Ready: it runs
// `convert --help` and must offer -u). When none is, Run fails with a
// *NoDolphinToolError carrying the convert-in-Dolphin advice.
type Converter struct {
	// Tool is the first DolphinTool to try; Tool.Path "" means none was
	// found beside Dolphin.
	Tool dolphintool.Tool
	// Dolphin is the Dolphin program it was looked for beside ("" when
	// none is known), for that message.
	Dolphin string
	// More are other Dolphins' DolphinTools, tried in order when Tool
	// can't be used.
	More []Choice
	// FreeBytes is the free space on a folder's volume (nil is
	// dolphintool.FreeBytes).
	FreeBytes func(dir string) (uint64, error)
}

// Choice is one Dolphin and the DolphinTool beside it (Tool.Path "" when
// there is none).
type Choice struct {
	Dolphin string
	Tool    dolphintool.Tool
}

func (c *Converter) choices() []Choice {
	return append([]Choice{{Dolphin: c.Dolphin, Tool: c.Tool}}, c.More...)
}

// ConvertNeed is the free space a conversion asks for on the output's
// volume before it starts: the temporary ISO and the patched disc, each a
// full GameCube disc (0x57058000 bytes, as DolphinTool writes an ISO), and
// 64 MB besides.
const ConvertNeed = 2*0x57058000 + 64<<20

// ErrNoDolphinTool is what a *NoDolphinToolError is (errors.Is).
var ErrNoDolphinTool = errors.New("no DolphinTool found")

// NoDolphinToolError is Run's error for an RVZ, WIA or GCZ input when the
// Converter has no usable DolphinTool. It unwraps to ErrNoDolphinTool, to
// the input's *inject.UnsupportedFormatError, whose message says how to
// convert the disc in Dolphin by hand, and to each Unusable error.
type NoDolphinToolError struct {
	Format *inject.UnsupportedFormatError
	// Dolphin is the first Dolphin program it was looked for beside, ""
	// when none was known; Also are the others.
	Dolphin string
	Also    []string
	// Unusable says why each DolphinTool that was found can't be used
	// (dolphintool.ErrNoUserFlag, ErrNoConvert, a help that failed);
	// empty when none was found at all.
	Unusable []error
}

// Looked is every Dolphin program it was looked for beside.
func (e *NoDolphinToolError) Looked() []string {
	if e.Dolphin == "" {
		return e.Also
	}
	return append([]string{e.Dolphin}, e.Also...)
}

func (e *NoDolphinToolError) Error() string {
	looked := e.Looked()
	where := "no Dolphin was found to look beside"
	if len(looked) > 0 {
		where = "looked beside " + strings.Join(looked, ", ")
	}
	if len(e.Unusable) > 0 {
		why := make([]string, len(e.Unusable))
		for i, err := range e.Unusable {
			why[i] = err.Error()
		}
		return fmt.Sprintf("no usable DolphinTool (%s; %s). %v", where, strings.Join(why, "; "), e.Format)
	}
	return fmt.Sprintf("%s (%s). %v", ErrNoDolphinTool, where, e.Format)
}

func (e *NoDolphinToolError) Unwrap() []error {
	return append([]error{ErrNoDolphinTool, e.Format}, e.Unusable...)
}

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
	if a, err := filepath.Abs(out); err == nil {
		// The temporary ISO's path, and so its cleanup, must not depend on
		// a working directory (DolphinTool runs in its own folder).
		out = a
	}
	if fi, err := os.Stat(out); err == nil {
		if ii, err := os.Stat(in); err == nil && os.SameFile(fi, ii) {
			return "", "", cleanup, fmt.Errorf("%w: %s; choose a different output path (the patcher never overwrites your original)",
				inject.ErrOutputIsInput, out)
		}
	}
	tool, err := c.pick(ctx, u)
	if err != nil {
		return "", "", cleanup, err
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

	fmt.Fprintf(w, "  convert: %s image -> plain ISO with %s\n", u.Format, tool.Path)
	if err := tool.ToISO(ctx, in, tmpPath, func(l string) { fmt.Fprintf(w, "  DolphinTool: %s\n", l) }); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	fmt.Fprintf(w, "  convert: done, patching the ISO it wrote (removed afterwards)\n")
	return tmpPath, u.Format, cleanup, nil
}

// pick is the first usable DolphinTool of c's choices, or a
// *NoDolphinToolError for the format u when there is none (ctx's error
// when it stops the probing).
func (c *Converter) pick(ctx context.Context, u *inject.UnsupportedFormatError) (dolphintool.Tool, error) {
	e := &NoDolphinToolError{Format: u}
	for i, ch := range c.choices() {
		if i == 0 {
			e.Dolphin = ch.Dolphin
		} else if ch.Dolphin != "" {
			e.Also = append(e.Also, ch.Dolphin)
		}
		if ch.Tool.Path == "" {
			continue
		}
		err := ch.Tool.Ready(ctx)
		if err == nil {
			return ch.Tool, nil
		}
		if ctx.Err() != nil {
			return dolphintool.Tool{}, ctx.Err()
		}
		e.Unusable = append(e.Unusable, err)
	}
	return dolphintool.Tool{}, e
}

func gb(n uint64) float64 { return float64(n) / (1 << 30) }

// ConverterFor is the Converter for the Dolphin programs dolphinExes, in
// the order they are tried ("" and repeats are skipped; none means no
// Dolphin is known): the DolphinTool beside each, if any.
func ConverterFor(dolphinExes ...string) *Converter {
	c := &Converter{}
	seen := map[string]bool{}
	for _, exe := range dolphinExes {
		if exe == "" || seen[exe] {
			continue
		}
		seen[exe] = true
		ch := Choice{Dolphin: exe, Tool: dolphintool.Tool{Path: dolphintool.Find(exe, nil)}}
		if c.Dolphin == "" {
			c.Dolphin, c.Tool = ch.Dolphin, ch.Tool
		} else {
			c.More = append(c.More, ch)
		}
	}
	return c
}

// NeedsConvert says the image at path is one a Converter turns into an
// ISO (RVZ, WIA or GCZ), so a caller looks for Dolphin only then.
func NeedsConvert(path string) bool {
	u, _ := inject.ImageFormat(path)
	return u != nil && u.Convertible()
}
