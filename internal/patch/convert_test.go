package patch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphintool"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// convertHelp is enough of DolphinTool's `convert --help` for Probe (the
// full text is dolphintool's RealConvertHelp).
const convertHelp = "  -u USER, --user=USER\n  -i FILE, --input=FILE\n  -o FILE, --output=FILE\n  -f FORMAT, --format=FORMAT\n"

// fakeDolphinTool converts by writing iso at -o; code != 0 fails.
type fakeDolphinTool struct {
	iso   []byte
	code  int
	calls [][]string
	outs  []string
}

func (f *fakeDolphinTool) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	f.calls = append(f.calls, args)
	if slices.Equal(args, dolphintool.HelpArgs) {
		return 0, convertHelp, nil
	}
	i := slices.Index(args, "-o")
	out := args[i+1]
	f.outs = append(f.outs, out)
	if line != nil {
		line("Converting")
	}
	if f.iso != nil {
		if err := os.WriteFile(out, f.iso, 0o644); err != nil {
			return 1, "", err
		}
	}
	return f.code, "", nil
}

// convertCalls are the calls that convert (not `convert --help`).
func convertCalls(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if !slices.Equal(c, dolphintool.HelpArgs) {
			out = append(out, c)
		}
	}
	return out
}

func rvzImage() []byte { return append([]byte("RVZ\x01"), make([]byte, 0x400)...) }

func fakeConverter(f *fakeDolphinTool, free uint64) *Converter {
	return &Converter{
		Tool:      dolphintool.Tool{Path: "DolphinTool.exe", Runner: f},
		Dolphin:   "Dolphin.exe",
		FreeBytes: func(string) (uint64, error) { return free, nil },
	}
}

// noTemps fails when a temporary converted ISO is left in dir.
func noTemps(t *testing.T, dir string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.converting-*"))
	if len(m) != 0 {
		t.Errorf("temporary ISOs left: %q", m)
	}
}

func TestRunConvertsRVZ(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.rvz", rvzImage())
	f := &fakeDolphinTool{iso: tablestest.PatchableISO(tablestest.Vanilla())}
	spy := &tablesSpy{}
	var w bytes.Buffer
	res, err := Run(context.Background(), in, "", &w, Options{PostPatch: []func(io.Writer, string){spy.fn}, Converter: fakeConverter(f, ConvertNeed)})
	out := filepath.Join(dir, "ww-multiplayer.iso")
	if err != nil || res.ConvertedFrom != "RVZ" || res.AlreadyPatched || res.Image() != out {
		t.Fatalf("Run: %+v, %v\n%s", res, err, w.String())
	}
	// convert -u <tmp> -i in -o <temp beside out> -f iso, then the patch
	// of that ISO; the temp is gone and the input untouched.
	if len(f.outs) != 1 || filepath.Dir(f.outs[0]) != dir || !strings.Contains(filepath.Base(f.outs[0]), "ww-multiplayer.iso.converting-") {
		t.Errorf("converted to %q", f.outs)
	}
	if c := convertCalls(f.calls); len(c) != 1 {
		t.Errorf("convert calls %q", f.calls)
	} else if c := c[0]; c[0] != "convert" || c[1] != "-u" || !slices.Equal(c[3:], []string{"-i", in, "-o", f.outs[0], "-f", "iso"}) {
		t.Errorf("convert args %q", c)
	}
	noTemps(t, dir)
	if b, _ := os.ReadFile(in); !bytes.Equal(b, rvzImage()) {
		t.Error("input changed")
	}
	if st, err := Inspect(out); err != nil || st != Patched {
		t.Errorf("output: %v %v", st, err)
	}
	for _, s := range []string{
		"  convert: RVZ image -> plain ISO with DolphinTool.exe\n",
		"  DolphinTool: Converting\n",
		"  convert: done, patching the ISO it wrote (removed afterwards)\n",
		"  co-op : (tables of ww-multiplayer.iso)\n",
		"\nDone. Boot " + out,
	} {
		if !strings.Contains(w.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, w.String())
		}
	}
	if !slices.Equal(spy.images, []string{out}) {
		t.Errorf("tables of %q", spy.images)
	}
}

// TestRunConvertedAlreadyPatched: an RVZ of a disc this build patched
// keeps the converted ISO at out, which is the disc to boot.
func TestRunConvertedAlreadyPatched(t *testing.T) {
	dir := t.TempDir()
	iso := writeImage(t, dir, "ww.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	patched := filepath.Join(dir, "patched.iso")
	if _, err := Run(context.Background(), iso, patched, io.Discard, Options{}); err != nil {
		t.Fatal(err)
	}
	img, _ := os.ReadFile(patched)
	in := writeImage(t, dir, "p.rvz", rvzImage())
	spy := &tablesSpy{}
	var w bytes.Buffer
	res, err := Run(context.Background(), in, "", &w, Options{PostPatch: []func(io.Writer, string){spy.fn}, Converter: fakeConverter(&fakeDolphinTool{iso: img}, ConvertNeed)})
	out := filepath.Join(dir, "p-multiplayer.iso")
	if err != nil || !res.AlreadyPatched || res.ConvertedFrom != "RVZ" || res.Image() != out {
		t.Fatalf("Run: %+v %v\n%s", res, err, w.String())
	}
	if b, _ := os.ReadFile(out); !bytes.Equal(b, img) {
		t.Error("the converted ISO was not kept at out")
	}
	if !strings.Contains(w.String(), "Kept the converted ISO as "+out) || !slices.Equal(spy.images, []string{out}) {
		t.Errorf("output %s, tables %q", w.String(), spy.images)
	}
	noTemps(t, dir)
}

func TestRunConvertErrors(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.rvz", rvzImage())
	iso := tablestest.PatchableISO(tablestest.Vanilla())
	var w bytes.Buffer

	// No converter: the advice, as before.
	_, err := Run(context.Background(), in, "", &w, Options{})
	var u *inject.UnsupportedFormatError
	if !errors.As(err, &u) || u.Format != "RVZ" || errors.Is(err, ErrNoDolphinTool) {
		t.Errorf("no converter: %v", err)
	}

	// No DolphinTool beside the Dolphin: both the advice and where it looked.
	w.Reset()
	_, err = Run(context.Background(), in, "", &w, Options{Converter: &Converter{Dolphin: `C:\Dolphin\Dolphin.exe`}})
	var nt *NoDolphinToolError
	if !errors.As(err, &nt) || !errors.Is(err, ErrNoDolphinTool) || !errors.As(err, &u) || nt.Dolphin != `C:\Dolphin\Dolphin.exe` ||
		!strings.Contains(w.String(), `ERROR: no DolphinTool found (looked beside C:\Dolphin\Dolphin.exe). iso: this is a compressed RVZ image`) {
		t.Errorf("no tool: %v\n%s", err, w.String())
	}
	if _, err := Run(context.Background(), in, "", io.Discard, Options{Converter: ConverterFor("")}); err == nil || !strings.Contains(err.Error(), "no Dolphin was found to look beside") {
		t.Errorf("no Dolphin: %v", err)
	}

	// Too little space: nothing is converted (only the tool's help ran).
	f := &fakeDolphinTool{iso: iso}
	_, err = Run(context.Background(), in, "", io.Discard, Options{Converter: fakeConverter(f, ConvertNeed-1)})
	if !errors.Is(err, ErrLowSpace) || len(convertCalls(f.calls)) != 0 || !strings.Contains(err.Error(), "GB free in "+dir) {
		t.Errorf("low space: %v, calls %q", err, f.calls)
	}

	// The free-space query failing is a note, not a stop.
	f = &fakeDolphinTool{iso: iso}
	c := fakeConverter(f, 0)
	c.FreeBytes = func(string) (uint64, error) { return 0, errors.New("no statfs") }
	w.Reset()
	if _, err := Run(context.Background(), in, filepath.Join(dir, "q.iso"), &w, Options{Converter: c}); err != nil || !strings.Contains(w.String(), "unknown: no statfs; trying anyway") {
		t.Errorf("unknown space: %v\n%s", err, w.String())
	}

	// DolphinTool fails: its error, no output, no temp.
	w.Reset()
	f = &fakeDolphinTool{iso: []byte("half"), code: 4}
	_, err = Run(context.Background(), in, filepath.Join(dir, "f.iso"), &w, Options{Converter: fakeConverter(f, ConvertNeed)})
	if err == nil || !strings.Contains(w.String(), "ERROR: DolphinTool couldn't convert the disc (exit code 4): Converting") {
		t.Errorf("tool failed: %v\n%s", err, w.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "f.iso")); !os.IsNotExist(err) {
		t.Errorf("failed convert wrote out: %v", err)
	}

	// The converted ISO isn't a Wind Waker disc: the patcher's error, temp gone.
	f = &fakeDolphinTool{iso: make([]byte, 0x10000)}
	if _, err := Run(context.Background(), in, filepath.Join(dir, "g.iso"), io.Discard, Options{Converter: fakeConverter(f, ConvertNeed)}); err == nil || !strings.Contains(err.Error(), "unexpected game id") {
		t.Errorf("garbage ISO: %v", err)
	}

	// out naming the input is refused before anything runs.
	f = &fakeDolphinTool{iso: iso}
	if _, err := Run(context.Background(), in, in, io.Discard, Options{Converter: fakeConverter(f, ConvertNeed)}); !errors.Is(err, inject.ErrOutputIsInput) || len(f.calls) != 0 {
		t.Errorf("out == in: %v", err)
	}
	if b, _ := os.ReadFile(in); !bytes.Equal(b, rvzImage()) {
		t.Error("input changed")
	}

	// NKit is never converted.
	nk := tablestest.PatchableISO(tablestest.Vanilla())[:0x1000]
	copy(nk[0x200:], "NKIT")
	nkit := writeImage(t, dir, "ww.nkit.iso", nk)
	f = &fakeDolphinTool{iso: iso}
	if _, err := Run(context.Background(), nkit, "", io.Discard, Options{Converter: fakeConverter(f, ConvertNeed)}); !errors.As(err, &u) || u.Format != "NKit" || len(f.calls) != 0 {
		t.Errorf("nkit: %v, calls %q", err, f.calls)
	}

	// A plain ISO never runs the tool.
	plain := writeImage(t, dir, "plain.iso", iso)
	f = &fakeDolphinTool{}
	if res, err := Run(context.Background(), plain, "", io.Discard, Options{Converter: fakeConverter(f, 0)}); err != nil || res.ConvertedFrom != "" || len(f.calls) != 0 {
		t.Errorf("plain ISO: %+v %v, calls %q", res, err, f.calls)
	}
	noTemps(t, dir)
}

func TestRunConvertCancelled(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.rvz", rvzImage())
	ctx, cancel := context.WithCancel(context.Background())
	f := &cancelTool{fakeDolphinTool{iso: []byte("half")}, cancel}
	var w bytes.Buffer
	_, err := Run(ctx, in, "", &w, Options{Converter: &Converter{Tool: dolphintool.Tool{Path: "t", Runner: f},
		FreeBytes: func(string) (uint64, error) { return ConvertNeed, nil }}})
	if !errors.Is(err, context.Canceled) || strings.Contains(w.String(), "ERROR") {
		t.Errorf("cancelled: %v\n%s", err, w.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "ww-multiplayer.iso")); !os.IsNotExist(err) {
		t.Errorf("out written: %v", err)
	}
	noTemps(t, dir)
}

// cancelTool cancels the run while it converts.
type cancelTool struct {
	fakeDolphinTool
	cancel context.CancelFunc
}

func (c *cancelTool) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	code, out, err := c.fakeDolphinTool.Run(ctx, exe, args, line)
	if args[0] == "convert" && !slices.Equal(args, dolphintool.HelpArgs) {
		c.cancel()
		return -1, out, context.Canceled
	}
	return code, out, err
}

func TestNeedsConvert(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]bool{
		"a.rvz": true, "a.wia": true, "a.gcz": true, "a.iso": false, "a.nkit.iso": false, "missing": false,
	} {
		var img []byte
		switch name {
		case "a.rvz":
			img = rvzImage()
		case "a.wia":
			img = append([]byte("WIA\x01"), make([]byte, 0x400)...)
		case "a.gcz":
			img = append([]byte{0x01, 0xC0, 0x0B, 0xB1}, make([]byte, 0x400)...)
		case "a.iso":
			img = tablestest.PatchableISO(tablestest.Vanilla())[:0x1000]
		case "a.nkit.iso":
			img = make([]byte, 0x1000)
			copy(img[0x200:], "NKIT")
		}
		p := filepath.Join(dir, name)
		if img != nil {
			p = writeImage(t, dir, name, img)
		}
		if got := NeedsConvert(p); got != want {
			t.Errorf("NeedsConvert(%s) = %v", name, got)
		}
	}
}

// dirTool is a fake DolphinTool that, like the real one run by
// dolphintool.ExecRunner, resolves relative -i and -o paths against its
// own folder (dir), not the caller's working directory. It fails when -i
// isn't there, as DolphinTool does.
type dirTool struct {
	dir  string
	iso  []byte
	help string
}

func (d *dirTool) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	if slices.Equal(args, dolphintool.HelpArgs) {
		if d.help != "" {
			return 0, d.help, nil
		}
		return 0, convertHelp, nil
	}
	at := func(f string) string {
		p := args[slices.Index(args, f)+1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(d.dir, p)
		}
		return p
	}
	if _, err := os.Stat(at("-i")); err != nil {
		if line != nil {
			line("Error: Unable to open input disc image")
		}
		return 3, "", nil
	}
	return 0, "", os.WriteFile(at("-o"), d.iso, 0o644)
}

// TestRunConvertRelativePaths: `patch ww.rvz` typed in the disc's folder
// gives Run relative paths, but DolphinTool runs in Dolphin's folder. The
// input must still be found, the patched disc must land beside it, and
// nothing (no 1.36 GB temp ISO) may be left in Dolphin's folder.
func TestRunConvertRelativePaths(t *testing.T) {
	work, toolDir := t.TempDir(), t.TempDir()
	t.Chdir(work)
	writeImage(t, work, "ww.rvz", rvzImage())
	for _, outArg := range []string{"", "out.iso"} {
		f := &dirTool{dir: toolDir, iso: tablestest.PatchableISO(tablestest.Vanilla())}
		c := &Converter{Tool: dolphintool.Tool{Path: "DolphinTool.exe", Runner: f}, Dolphin: "Dolphin.exe",
			FreeBytes: func(string) (uint64, error) { return ConvertNeed, nil }}
		var w bytes.Buffer
		res, err := Run(context.Background(), "ww.rvz", outArg, &w, Options{Converter: c})
		if err != nil || res.ConvertedFrom != "RVZ" {
			t.Fatalf("out %q: %+v %v\n%s", outArg, res, err, w.String())
		}
		want := filepath.Join(work, "ww-multiplayer.iso")
		if outArg != "" {
			want = filepath.Join(work, outArg)
		}
		if st, err := Inspect(want); err != nil || st != Patched {
			t.Errorf("out %q: %s: %v %v", outArg, want, st, err)
		}
		if e, _ := os.ReadDir(toolDir); len(e) != 0 {
			t.Errorf("out %q: left in the tool's folder: %v", outArg, e)
		}
		noTemps(t, work)
	}
}

// TestRunConvertTriesOtherDolphins: a first Dolphin with no DolphinTool,
// or one whose DolphinTool can't take -u (it would use the player's own
// Dolphin user folder), doesn't hide a later one that works; when none
// works, the advice says why.
func TestRunConvertTriesOtherDolphins(t *testing.T) {
	dir := t.TempDir()
	in := writeImage(t, dir, "ww.rvz", rvzImage())
	noUser := strings.Replace(convertHelp, "  -u USER, --user=USER\n", "", 1)
	old := &fakeTool2{help: noUser}
	good := &fakeDolphinTool{iso: tablestest.PatchableISO(tablestest.Vanilla())}
	c := &Converter{
		Dolphin: `C:\Program Files\Dolphin\Dolphin.exe`, // no DolphinTool beside it
		More: []Choice{
			{Dolphin: `C:\Old\Dolphin.exe`, Tool: dolphintool.Tool{Path: `C:\Old\DolphinTool.exe`, Runner: old}},
			{Dolphin: `C:\Desk\Dolphin.exe`, Tool: dolphintool.Tool{Path: `C:\Desk\DolphinTool.exe`, Runner: good}},
		},
		FreeBytes: func(string) (uint64, error) { return ConvertNeed, nil },
	}
	var w bytes.Buffer
	res, err := Run(context.Background(), in, "", &w, Options{Converter: c})
	if err != nil || res.ConvertedFrom != "RVZ" || !strings.Contains(w.String(), `plain ISO with C:\Desk\DolphinTool.exe`) {
		t.Fatalf("Run: %+v %v\n%s", res, err, w.String())
	}
	if len(old.calls) != 1 || !slices.Equal(old.calls[0], dolphintool.HelpArgs) {
		t.Errorf("the tool without -u ran more than its help: %q", old.calls)
	}

	// Only the one without -u: refused, with the advice and the reason.
	c.More = c.More[:1]
	old.calls = nil
	_, err = Run(context.Background(), in, filepath.Join(dir, "b.iso"), io.Discard, Options{Converter: c})
	var nt *NoDolphinToolError
	if !errors.As(err, &nt) || !errors.Is(err, dolphintool.ErrNoUserFlag) || len(old.calls) != 1 ||
		!strings.Contains(err.Error(), `C:\Program Files\Dolphin\Dolphin.exe, C:\Old\Dolphin.exe`) || !strings.Contains(err.Error(), "Convert File") {
		t.Errorf("no -u: %v, calls %q", err, old.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.iso")); !os.IsNotExist(err) {
		t.Errorf("wrote b.iso: %v", err)
	}
	noTemps(t, dir)
}

// fakeTool2 answers its help with help and fails any conversion.
type fakeTool2 struct {
	help  string
	calls [][]string
}

func (f *fakeTool2) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	f.calls = append(f.calls, args)
	if slices.Equal(args, dolphintool.HelpArgs) {
		return 0, f.help, nil
	}
	return 1, "", nil
}
