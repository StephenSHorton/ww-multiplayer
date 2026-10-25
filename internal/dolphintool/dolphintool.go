// Package dolphintool runs Dolphin's command-line tool (DolphinTool.exe
// next to Dolphin.exe on Windows) to turn a compressed disc image the
// patcher can't edit (RVZ, WIA, GCZ: internal/inject/format.go) into a
// plain ISO, which `patch` and the app's setup then splice as usual. NKit
// stays on the advice path: an NKit image is a trimmed dump, and whether
// DolphinTool gives back the full disc from one is untested.
//
// The tool is found next to the Dolphin the app already locates (Find),
// and whether it can convert is read from its own `convert --help`
// (Probe) rather than assumed from a version. The idea, and probing the
// help text, are from datstarkey/ww-online
// WWOnline.Client/Services/DolphinTool.cs (MIT), which runs `extract`;
// this is a clean-room rewrite for `convert`.
//
// The syntax, from DolphinTool.exe `convert --help` of the Dolphin in use
// on 2026-10-25 (Dolphin-x64 on the owner's Desktop):
//
//	convert [options]... [FILE]...
//	  -u USER, --user=USER        User folder path, required for temporary
//	                              processing files. Will be automatically
//	                              created if this option is not set.
//	  -i FILE, --input=FILE       Path to disc image FILE.
//	  -o FILE, --output=FILE      Path to the destination FILE.
//	  -f FORMAT, --format=FORMAT  Container format to use. Default is RVZ.
//	  -s, --scrub                 Scrub junk data as part of conversion.
//
// The help prints "[%choices]" unexpanded; the choices, "iso gcz wia
// rvz", were read from the strings in that DolphinTool.exe, not from a
// conversion (none was run). We never pass --scrub: the patcher wants the
// whole disc.
//
// A DolphinTool is only run to convert when its help offers -u: it then
// gets a throwaway user folder, so the player's own Dolphin user folder
// (portable User, or %APPDATA%\Dolphin Emulator) is never initialised or
// written by it. One without -u is refused (ErrNoUserFlag) and the caller
// falls back to the convert-in-Dolphin advice. The paths it is given are
// made absolute first, because it runs in its own folder.
package dolphintool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Names are the file names DolphinTool goes by next to Dolphin's program:
// DolphinTool.exe on Windows (seen in the Windows zip build); dolphin-tool
// is the name Dolphin's own build gives it elsewhere (inferred: not seen
// in a macOS bundle or a Linux package).
var Names = []string{"DolphinTool.exe", "dolphin-tool", "DolphinTool"}

// Find returns the DolphinTool in the folder of dolphinExe (Dolphin's
// program), or "" when there is none. exists is os.Stat's yes/no in
// production (nil means that).
func Find(dolphinExe string, exists func(string) bool) string {
	if strings.TrimSpace(dolphinExe) == "" {
		return ""
	}
	if exists == nil {
		exists = isFile
	}
	dir := filepath.Dir(dolphinExe)
	for _, n := range Names {
		if p := filepath.Join(dir, n); exists(p) {
			return p
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Runner runs a program to its end: each line it prints (stdout and
// stderr) goes to line (nil drops them), and the exit code and everything
// it printed come back. err is for a program that couldn't run or was
// stopped by ctx, not for a non-zero exit. ExecRunner runs real programs;
// tests pass a fake DolphinTool.
type Runner interface {
	Run(ctx context.Context, exe string, args []string, line func(string)) (code int, out string, err error)
}

// Tool is one DolphinTool.
type Tool struct {
	Path   string
	Runner Runner // nil is ExecRunner
	// HelpTimeout bounds Probe's `convert --help` so a hung tool can't
	// stall the patch (0 is DefaultHelpTimeout).
	HelpTimeout time.Duration
}

// DefaultHelpTimeout is Probe's limit when Tool.HelpTimeout is 0.
const DefaultHelpTimeout = 10 * time.Second

// Caps is what a DolphinTool's `convert --help` offers.
type Caps struct {
	// User says it takes -u USER: ToISO then gives it a fresh temporary
	// folder, so the player's own Dolphin user folder is never created or
	// touched by the conversion. ToISO refuses a tool without it
	// (ErrNoUserFlag).
	User bool
}

// ErrNoConvert is Probe's error for a DolphinTool whose help shows no
// `convert -i FILE -o FILE -f FORMAT`.
var ErrNoConvert = errors.New("this DolphinTool has no convert command with -i, -o and -f")

// ErrNoUserFlag is Ready's (and ToISO's) error for a DolphinTool whose
// `convert --help` offers no -u USER: without one it would set up and
// use the player's own Dolphin user folder, so it is not run.
var ErrNoUserFlag = errors.New("this DolphinTool's convert has no -u USER option, so it can't be kept out of your Dolphin user folder")

// HelpArgs is what Probe runs.
var HelpArgs = []string{"convert", "--help"}

// ParseHelp reads `convert --help`: ok when it shows the -i FILE, -o FILE
// and -f FORMAT options Convert passes (short and long spellings both, as
// the help prints them).
func ParseHelp(help string) (Caps, bool) {
	for _, s := range []string{"-i FILE", "--input", "-o FILE", "--output", "-f FORMAT", "--format"} {
		if !strings.Contains(help, s) {
			return Caps{}, false
		}
	}
	return Caps{User: strings.Contains(help, "-u USER") && strings.Contains(help, "--user")}, true
}

func (t Tool) runner() Runner {
	if t.Runner == nil {
		return ExecRunner{}
	}
	return t.Runner
}

// Probe runs `convert --help` and says what the tool offers. A tool that
// doesn't answer within HelpTimeout, exits non-zero or shows no convert
// command is an error.
func (t Tool) Probe(ctx context.Context) (Caps, error) {
	limit := t.HelpTimeout
	if limit <= 0 {
		limit = DefaultHelpTimeout
	}
	hctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	code, out, err := t.runner().Run(hctx, t.Path, HelpArgs, nil)
	switch {
	case err != nil && ctx.Err() == nil && hctx.Err() != nil:
		return Caps{}, fmt.Errorf("%s didn't answer `convert --help` within %v", t.Path, limit)
	case err != nil:
		return Caps{}, fmt.Errorf("can't run %s: %w", t.Path, err)
	case code != 0:
		return Caps{}, fmt.Errorf("%s `convert --help` exited with code %d", t.Path, code)
	}
	caps, ok := ParseHelp(out)
	if !ok {
		return Caps{}, ErrNoConvert
	}
	return caps, nil
}

// Ready says the tool can be used to convert: Probe succeeds and it takes
// -u (else ErrNoUserFlag).
func (t Tool) Ready(ctx context.Context) error {
	caps, err := t.Probe(ctx)
	if err != nil {
		return err
	}
	if !caps.User {
		return fmt.Errorf("%s: %w", t.Path, ErrNoUserFlag)
	}
	return nil
}

// ConvertArgs is what Convert runs: convert -i in -o out -f iso, with -u
// userDir first when userDir is not "".
func ConvertArgs(in, out, userDir string) []string {
	args := []string{"convert"}
	if userDir != "" {
		args = append(args, "-u", userDir)
	}
	return append(args, "-i", in, "-o", out, "-f", "iso")
}

// ToISO checks the tool (Ready: it must take -u), then converts the disc
// image in to a plain ISO at out (which must not exist yet), handing each
// line the tool prints to line. in and out are made absolute first (the
// tool runs in its own folder, where relative paths would point
// elsewhere). The tool gets a temporary user folder, removed afterwards.
// On any failure, or when ctx stops it (the tool is killed), whatever it
// wrote at out is removed.
func (t Tool) ToISO(ctx context.Context, in, out string, line func(string)) (err error) {
	if in, err = filepath.Abs(in); err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := t.Ready(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s already exists", out)
	}
	userDir, err := os.MkdirTemp("", "ww-multiplayer-dolphintool-")
	if err != nil {
		return fmt.Errorf("can't make DolphinTool's temporary user folder: %w", err)
	}
	defer os.RemoveAll(userDir)
	defer func() {
		if err != nil {
			os.Remove(out)
		}
	}()
	var tail []string
	keep := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" {
			return
		}
		tail = append(tail, l)
		if len(tail) > 3 {
			tail = tail[1:]
		}
		if line != nil {
			line(l)
		}
	}
	code, _, err := t.runner().Run(ctx, t.Path, ConvertArgs(in, out, userDir), keep)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return fmt.Errorf("can't run %s: %w", t.Path, err)
	case code != 0:
		msg := fmt.Sprintf("DolphinTool couldn't convert the disc (exit code %d)", code)
		if len(tail) > 0 {
			msg += ": " + strings.Join(tail, " ")
		}
		return errors.New(msg)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		return errors.New("DolphinTool said it converted the disc but wrote no ISO")
	}
	return nil
}

// ExecRunner runs real programs with os/exec, with no console window on
// Windows (hideWindow), in the program's own folder. A relative exe is
// made absolute first so it isn't looked up again from inside that folder.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, exe string, args []string, line func(string)) (int, string, error) {
	if a, err := filepath.Abs(exe); err == nil {
		exe = a
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	hideWindow(cmd)
	w := &lineBuf{fn: line}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	w.flush()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return ee.ExitCode(), w.all.String(), nil
	}
	if err != nil {
		return -1, w.all.String(), err
	}
	return 0, w.all.String(), nil
}

// lineBuf keeps all a program printed and hands on whole lines. Stdout and
// stderr share it; os/exec copies each through its own goroutine only when
// they are different writers, and here they are one, so Write is never
// called from two goroutines at once.
type lineBuf struct {
	all  bytes.Buffer
	part []byte
	fn   func(string)
}

func (w *lineBuf) Write(p []byte) (int, error) {
	w.all.Write(p)
	if w.fn == nil {
		return len(p), nil
	}
	w.part = append(w.part, p...)
	for {
		// DolphinTool may redraw a progress line with \r.
		i := bytes.IndexAny(w.part, "\r\n")
		if i < 0 {
			break
		}
		w.fn(string(w.part[:i]))
		w.part = w.part[i+1:]
	}
	return len(p), nil
}

func (w *lineBuf) flush() {
	if w.fn != nil && len(w.part) > 0 {
		w.fn(string(w.part))
	}
	w.part = nil
}
