package dolphintool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// RealConvertHelp is `DolphinTool.exe convert --help` as the Dolphin-x64
// build on the owner's Desktop printed it on 2026-10-25 (exit 0).
const RealConvertHelp = `Usage: convert [options]... [FILE]...

Options:
  -h, --help            show this help message and exit
  -u USER, --user=USER  User folder path, required for temporary processing
                        files. Will be automatically created if this option is
                        not set.
  -i FILE, --input=FILE
                        Path to disc image FILE.
  -o FILE, --output=FILE
                        Path to the destination FILE.
  -f FORMAT, --format=FORMAT
                        Container format to use. Default is RVZ. [%choices]
  -s, --scrub           Scrub junk data as part of conversion.
  -b BLOCK_SIZE, --block_size=BLOCK_SIZE
                        Block size for GCZ/WIA/RVZ formats, as an integer.
                        Suggested value for RVZ: 131072 (128 KiB)
  -c COMPRESSION, --compression=COMPRESSION
                        Compression method to use when converting to WIA/RVZ.
                        Suggested value for RVZ: zstd [%choices]
  -l COMPRESSION_LEVEL, --compression_level=COMPRESSION_LEVEL
                        Level of compression for the selected method. Ignored
                        if 'none'. Suggested value for zstd: 5
`

func TestParseHelp(t *testing.T) {
	if c, ok := ParseHelp(RealConvertHelp); !ok || !c.User {
		t.Errorf("real help: %+v %v", c, ok)
	}
	noUser := strings.Replace(RealConvertHelp, "  -u USER, --user=USER", "", 1)
	if c, ok := ParseHelp(noUser); !ok || c.User {
		t.Errorf("help without -u: %+v %v", c, ok)
	}
	// The top-level usage, and an extract-only help, are not convert.
	for _, h := range []string{
		"usage: dolphin-tool COMMAND -h\n\ncommands supported: [convert, verify, header, extract]\n",
		"Usage: extract [options]...\n  -i FILE, --input=FILE\n  -o FOLDER, --output=FOLDER\n",
		strings.Replace(RealConvertHelp, "-f FORMAT, --format=FORMAT", "", 1),
	} {
		if _, ok := ParseHelp(h); ok {
			t.Errorf("ParseHelp(%q) ok", h[:30])
		}
	}
}

func TestConvertArgs(t *testing.T) {
	if got, want := ConvertArgs("a.rvz", "b.iso", ""), []string{"convert", "-i", "a.rvz", "-o", "b.iso", "-f", "iso"}; !slices.Equal(got, want) {
		t.Errorf("no user: %q", got)
	}
	if got, want := ConvertArgs("a.rvz", "b.iso", "U"), []string{"convert", "-u", "U", "-i", "a.rvz", "-o", "b.iso", "-f", "iso"}; !slices.Equal(got, want) {
		t.Errorf("user: %q", got)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Dolphin.exe")
	if got := Find(exe, nil); got != "" {
		t.Errorf("no tool: %q", got)
	}
	tool := filepath.Join(dir, "DolphinTool.exe")
	if err := os.WriteFile(tool, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Find(exe, nil); got != tool {
		t.Errorf("Find = %q, want %q", got, tool)
	}
	if got := Find("", nil); got != "" {
		t.Errorf("no Dolphin: %q", got)
	}
	// A folder by that name is no tool.
	dir2 := t.TempDir()
	os.Mkdir(filepath.Join(dir2, "DolphinTool.exe"), 0o755)
	if got := Find(filepath.Join(dir2, "Dolphin.exe"), nil); got != "" {
		t.Errorf("folder: %q", got)
	}
	// Other names, through exists.
	got := Find(filepath.Join("x", "dolphin-emu"), func(p string) bool { return filepath.Base(p) == "dolphin-tool" })
	if got != filepath.Join("x", "dolphin-tool") {
		t.Errorf("dolphin-tool: %q", got)
	}
}

// fakeTool is a fake DolphinTool: help is what `convert --help` prints,
// and a convert writes iso at -o (or fails with code).
type fakeTool struct {
	help     string
	helpCode int
	hang     bool // the help never answers (until ctx ends)
	iso      []byte
	code     int
	lines    []string
	calls    [][]string
	userDirs []string
}

func (f *fakeTool) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	f.calls = append(f.calls, args)
	if slices.Equal(args, HelpArgs) {
		if f.hang {
			<-ctx.Done()
			return -1, "", ctx.Err()
		}
		return f.helpCode, f.help, nil
	}
	if args[0] != "convert" {
		return 1, "unknown", nil
	}
	var out string
	for i := 1; i+1 < len(args); i++ {
		switch args[i] {
		case "-o":
			out = args[i+1]
		case "-u":
			f.userDirs = append(f.userDirs, args[i+1])
			if fi, err := os.Stat(args[i+1]); err != nil || !fi.IsDir() {
				return 1, "", fmt.Errorf("user dir %s missing", args[i+1])
			}
		}
	}
	for _, l := range f.lines {
		if line != nil {
			line(l)
		}
	}
	if out != "" && f.iso != nil {
		if err := os.WriteFile(out, f.iso, 0o644); err != nil {
			return 1, "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return -1, "", err
	}
	return f.code, strings.Join(f.lines, "\n"), nil
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	if c, err := (Tool{Path: "t", Runner: &fakeTool{help: RealConvertHelp}}).Probe(ctx); err != nil || !c.User {
		t.Errorf("real: %+v %v", c, err)
	}
	if _, err := (Tool{Path: "t", Runner: &fakeTool{help: "Usage: nothing"}}).Probe(ctx); !errors.Is(err, ErrNoConvert) {
		t.Errorf("no convert: %v", err)
	}
	if _, err := (Tool{Path: "t", Runner: &fakeTool{help: RealConvertHelp, helpCode: 2}}).Probe(ctx); err == nil || !strings.Contains(err.Error(), "code 2") {
		t.Errorf("exit 2: %v", err)
	}
	tl := Tool{Path: "t", Runner: &fakeTool{hang: true}, HelpTimeout: 20 * time.Millisecond}
	if _, err := tl.Probe(ctx); err == nil || !strings.Contains(err.Error(), "didn't answer") {
		t.Errorf("hang: %v", err)
	}
}

func TestToISO(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iso")
	f := &fakeTool{help: RealConvertHelp, iso: []byte("ISO!"), lines: []string{"Converting...", ""}}
	var got []string
	if err := (Tool{Path: "t", Runner: f}).ToISO(context.Background(), "in.rvz", out, func(l string) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ISO!" {
		t.Errorf("out = %q", b)
	}
	if !slices.Equal(got, []string{"Converting..."}) {
		t.Errorf("lines = %q", got)
	}
	// The temporary user folder was given and is gone again.
	if len(f.userDirs) != 1 {
		t.Fatalf("user dirs %q", f.userDirs)
	}
	if _, err := os.Stat(f.userDirs[0]); !os.IsNotExist(err) {
		t.Errorf("user dir left: %v", err)
	}
	// A second run refuses to write over out.
	if err := (Tool{Path: "t", Runner: f}).ToISO(context.Background(), "in.rvz", out, nil); err == nil {
		t.Error("overwrote out")
	}
}

// TestToISONoUserFlag: a DolphinTool that can't be given -u would use the
// player's own Dolphin user folder, so it is never run to convert.
func TestToISONoUserFlag(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.iso")
	f := &fakeTool{help: strings.Replace(RealConvertHelp, "-u USER, --user=USER", "", 1), iso: []byte("x")}
	tl := Tool{Path: "t", Runner: f}
	if err := tl.ToISO(context.Background(), "in.rvz", out, nil); !errors.Is(err, ErrNoUserFlag) {
		t.Fatalf("ToISO: %v", err)
	}
	if len(f.calls) != 1 || !slices.Equal(f.calls[0], HelpArgs) {
		t.Errorf("ran more than the help: %q", f.calls)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("wrote %s: %v", out, err)
	}
	if err := tl.Ready(context.Background()); !errors.Is(err, ErrNoUserFlag) {
		t.Errorf("Ready: %v", err)
	}
	if err := (Tool{Path: "t", Runner: &fakeTool{help: RealConvertHelp}}).Ready(context.Background()); err != nil {
		t.Errorf("Ready, real help: %v", err)
	}
}

func TestToISOFailures(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.iso")
	// A non-zero exit removes the partial ISO and quotes the last lines.
	f := &fakeTool{help: RealConvertHelp, iso: []byte("half"), code: 1, lines: []string{"a", "b", "c", "Error: the disc is bad"}}
	err := (Tool{Path: "t", Runner: f}).ToISO(context.Background(), "in.rvz", out, nil)
	if err == nil || !strings.Contains(err.Error(), "exit code 1") || !strings.Contains(err.Error(), "b c Error: the disc is bad") {
		t.Errorf("exit 1: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("partial ISO left: %v", err)
	}
	// Exit 0 with no file written.
	f = &fakeTool{help: RealConvertHelp}
	if err := (Tool{Path: "t", Runner: f}).ToISO(context.Background(), "in.rvz", out, nil); err == nil || !strings.Contains(err.Error(), "wrote no ISO") {
		t.Errorf("no ISO: %v", err)
	}
	// Cancelled: ctx's error, and nothing left.
	ctx, cancel := context.WithCancel(context.Background())
	f = &fakeTool{help: RealConvertHelp, iso: []byte("half"), lines: []string{"x"}}
	err = (Tool{Path: "t", Runner: f}).ToISO(ctx, "in.rvz", out, func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("cancelled ISO left: %v", err)
	}
	// A tool with no convert never runs one.
	f = &fakeTool{help: "usage: dolphin-tool COMMAND -h"}
	if err := (Tool{Path: "t", Runner: f}).ToISO(context.Background(), "in.rvz", out, nil); !errors.Is(err, ErrNoConvert) || len(f.calls) != 1 {
		t.Errorf("no convert: %v %q", err, f.calls)
	}
}

func TestFreeBytes(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if errors.Is(err, errNoFreeSpace) {
		t.Skip(err)
	}
	if err != nil || n == 0 {
		t.Errorf("FreeBytes: %d %v", n, err)
	}
}

func TestLineBuf(t *testing.T) {
	var got []string
	w := &lineBuf{fn: func(l string) { got = append(got, l) }}
	w.Write([]byte("one\r\ntw"))
	w.Write([]byte("o\n50%\r60%"))
	w.flush()
	if want := []string{"one", "", "two", "50%", "60%"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if w.all.String() != "one\r\ntwo\n50%\r60%" {
		t.Errorf("all = %q", w.all.String())
	}
}
