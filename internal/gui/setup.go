package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/dolphinenv"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// The first-run setup (App PR 6, design §2.1): three skippable steps, your
// name and color, your game (patch the disc, or pick a patched one) and
// Dolphin (detect, browse, and on macOS prepare a copy), then "You're
// set". The window opens on it while the config lacks a name, a patched
// disc or a Dolphin, or a saved file has gone missing (setupGaps), or, on
// macOS, Dolphin still needs its prepared copy (macGap); Home's Setup
// button opens it again.

// The setup steps, in order.
const (
	StepProfile = "profile"
	StepGame    = "game"
	StepDolphin = "dolphin"
)

// SetupGapView is one reason the setup opens: a step and what it lacks.
type SetupGapView struct {
	Step string `json:"step"`
	Text string `json:"text"`
}

// MacView is the macOS prepare step's state (#16): nil on other systems.
type MacView struct {
	State string `json:"state"` // "ready", "needs-copy", "hardened", "outdated", "no-dolphin"
	Text  string `json:"text"`
	// Prepare offers the Prepare Dolphin button; Refresh words it as
	// "Dolphin was updated — refresh the copy".
	Prepare  bool   `json:"prepare"`
	Refresh  bool   `json:"refresh"`
	Original string `json:"original"`
	Copy     string `json:"copy"`
}

// SetupView is what the Setup screen starts from.
type SetupView struct {
	// Needed says the window should open on the setup (Gaps says why).
	Needed bool           `json:"needed"`
	Gaps   []SetupGapView `json:"gaps"`
	// Done is the config's setupDone: the setup was finished once.
	Done       bool   `json:"done"`
	Name       string `json:"name"`
	Color      string `json:"color"` // "#RRGGBB", or "" when none was chosen (classic green)
	Patched    string `json:"patched"`
	PatchedOK  bool   `json:"patchedOK"`
	Vanilla    string `json:"vanilla"`
	VanillaOK  bool   `json:"vanillaOK"`
	DolphinExe string `json:"dolphinExe"`
	DolphinOK  bool   `json:"dolphinOK"`
	UserDir    string `json:"userDir"`
	// FileDialog says PickFile works (the window); in a browser the
	// frontend asks for a typed path instead.
	FileDialog bool `json:"fileDialog"`
	// Env names the environment variables overriding saved settings.
	Env []string `json:"env"`
	// Patch is the disc being patched, or the last patch this run.
	Patch PatchView `json:"patch"`
	Mac   *MacView  `json:"mac"`
}

// ProfileRequest is the setup's first step.
type ProfileRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"` // a palette name or "#RRGGBB"; "" is none (classic green)
}

// ColorCheckView is a custom tunic color, read.
type ColorCheckView struct {
	Hex string `json:"hex"`
	// Name is the palette color it is, if it is one.
	Name string `json:"name"`
}

// DolphinCandidateView is one place Dolphin may be.
type DolphinCandidateView struct {
	Exe    string `json:"exe"`
	Source string `json:"source"`
	Found  bool   `json:"found"`
}

// DolphinView is the Dolphin step: what detection found and what is saved.
type DolphinView struct {
	Exe        string                 `json:"exe"`
	Source     string                 `json:"source"`
	Found      bool                   `json:"found"`
	UserDir    string                 `json:"userDir"`
	Portable   bool                   `json:"portable"`
	Candidates []DolphinCandidateView `json:"candidates"`
	Saved      string                 `json:"saved"`
	SavedOK    bool                   `json:"savedOK"`
	SavedDir   string                 `json:"savedUserDir"`
	Mac        *MacView               `json:"mac"`
}

// DolphinRequest saves the Dolphin to use.
type DolphinRequest struct {
	Exe     string `json:"exe"`
	UserDir string `json:"userDir"` // "" is the one Dolphin uses (dolphinenv.UserDir)
}

// PrepareView is Prepare Dolphin's outcome and its logged lines.
type PrepareView struct {
	OK    bool     `json:"ok"`
	Lines []string `json:"lines"`
	Error string   `json:"error"`
}

// fileDialogs opens the native file dialogs (the window's; a test's fake).
type fileDialogs interface {
	// Open asks for a file of kind ("iso", "dolphin") or a folder
	// ("userdir"), starting in dir; "" when the user cancels.
	Open(kind, title, dir string) (string, error)
}

// setupGaps says why the setup must open for the effective config c
// (WithEnv applied): no name, no patched disc or no Dolphin, or a saved
// one that isFile no longer finds.
func setupGaps(c config.Config, isFile func(string) bool) []SetupGapView {
	gaps := []SetupGapView{}
	if strings.TrimSpace(c.Name) == "" {
		gaps = append(gaps, SetupGapView{StepProfile, "Pick your name and tunic color."})
	}
	switch {
	case c.ISO.Patched == "":
		gaps = append(gaps, SetupGapView{StepGame, "Choose your Wind Waker disc."})
	case !isFile(c.ISO.Patched):
		gaps = append(gaps, SetupGapView{StepGame, "Your patched disc is gone: " + c.ISO.Patched})
	}
	switch {
	case c.Dolphin.Exe == "":
		gaps = append(gaps, SetupGapView{StepDolphin, "Find Dolphin."})
	case !isFile(c.Dolphin.Exe):
		gaps = append(gaps, SetupGapView{StepDolphin, "Dolphin is gone from " + c.Dolphin.Exe})
	}
	return gaps
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// effective is the saved config with the environment's overrides.
func (b *Bridge) effective() (config.Config, []string) {
	b.mu.Lock()
	c := b.cfg
	b.mu.Unlock()
	return c.WithEnv(b.getenv)
}

// macGap is the macOS reason the setup must open (design §2.1: when only
// /Applications/Dolphin.app exists or the copy is still hardened, the setup
// offers Prepare Dolphin), or nil: the prepared copy is missing or hardened,
// or the saved Dolphin is the original bundle's while the copy is there.
// An outdated copy only gets the launch toast. exe is the saved Dolphin;
// err is macStatus's (ErrNotMac elsewhere: no gap).
func macGap(st dolphinenv.MacState, err error, exe string) *SetupGapView {
	if err != nil {
		return nil
	}
	switch st {
	case dolphinenv.MacNeedsCopy, dolphinenv.MacHardened:
		return &SetupGapView{StepDolphin, "Prepare Dolphin for macOS, so this app can read the game."}
	case dolphinenv.MacReady, dolphinenv.MacCopyOutdated:
		if exe == macOriginalExe {
			return &SetupGapView{StepDolphin, "Play on the prepared copy of Dolphin: macOS won't let this app read the original."}
		}
	}
	return nil
}

// macOriginalExe is the program inside /Applications/Dolphin.app.
var macOriginalExe = dolphinenv.MacApp + "/Contents/MacOS/Dolphin"

// Setup returns the setup's state: whether it must open, and the saved
// name, color, discs and Dolphin.
func (b *Bridge) Setup() SetupView {
	c, env := b.effective()
	gaps := setupGaps(c, isFile)
	st, _, macErr := b.macStatus()
	if g := macGap(st, macErr, c.Dolphin.Exe); g != nil {
		gaps = append(gaps, *g)
	}
	v := SetupView{
		Needed: len(gaps) > 0, Gaps: gaps, Done: c.UI.SetupDone,
		Name: c.Name, Patched: c.ISO.Patched, PatchedOK: c.ISO.Patched != "" && isFile(c.ISO.Patched),
		Vanilla: c.ISO.Vanilla, VanillaOK: c.ISO.Vanilla != "" && isFile(c.ISO.Vanilla),
		DolphinExe: c.Dolphin.Exe, DolphinOK: c.Dolphin.Exe != "" && isFile(c.Dolphin.Exe), UserDir: c.Dolphin.UserDir,
		FileDialog: b.dialogs() != nil, Env: append([]string{}, env...),
		Patch: b.PatchStatus(), Mac: b.macViewOf(st, macErr),
	}
	if col, err := playercolor.Parse(c.Color); err == nil && c.Color != "" {
		v.Color = col.Hex()
	}
	return v
}

// SaveProfile saves the first step: the player's name and tunic color.
func (b *Bridge) SaveProfile(r ProfileRequest) (err error) {
	defer b.maskErr(&err)
	name, err := checkName(r.Name)
	if err != nil {
		return err
	}
	col, err := parseColor(r.Color)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg.Name = name
	b.cfg.Color = ""
	if col != nil {
		b.cfg.Color = col.Hex()
	}
	b.mu.Unlock()
	return b.saveErr()
}

// CheckColor reads a custom color ("#RRGGBB" or a palette name) and says
// which palette color it is, if any. Players may share a color (owner
// decision, 2026-10-16), so nothing is said about colors close to others.
func (b *Bridge) CheckColor(s string) (_ ColorCheckView, err error) {
	defer b.maskErr(&err)
	c, err := playercolor.Parse(strings.TrimSpace(s))
	if err != nil {
		return ColorCheckView{}, err
	}
	v := ColorCheckView{Hex: c.Hex()}
	v.Name, _ = playercolor.Name(c)
	return v, nil
}

// PickFile opens the native file dialog for kind: "iso" (a disc image),
// "dolphin" (Dolphin's program) or "userdir" (a folder). It returns "" when
// the user cancels. The window only: ui --browser doesn't serve it
// (browserMethods), and the frontend asks for a typed path there.
func (b *Bridge) PickFile(kind string) (_ string, err error) {
	defer b.maskErr(&err)
	d := b.dialogs()
	if d == nil {
		return "", errors.New("there is no file dialog here; type the path")
	}
	c, _ := b.effective()
	var title, dir string
	switch kind {
	case "iso":
		title = "Choose your Wind Waker disc image (.iso or .ciso, GZLE01)"
		dir = firstDir(c.ISO.Vanilla, c.ISO.Patched)
	case "dolphin":
		title = "Find Dolphin"
		dir = firstDir(c.Dolphin.Exe)
	case "userdir":
		title = "Choose Dolphin's user folder"
		dir = c.Dolphin.UserDir
		if !isDir(dir) {
			dir = ""
		}
	default:
		return "", fmt.Errorf("unknown file kind %q", kind)
	}
	return d.Open(kind, title, dir)
}

// firstDir is the folder of the first path whose folder exists.
func firstDir(paths ...string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if d := filepath.Dir(p); isDir(d) {
			return d
		}
	}
	return ""
}

// cleanPath trims what a pasted path tends to carry: spaces and quotes.
func cleanPath(p string) string {
	return strings.Trim(strings.TrimSpace(p), `"'`)
}

// DetectDolphin looks for Dolphin in the order of dolphinenv.Detect and
// returns what it found next to what is saved.
func (b *Bridge) DetectDolphin() DolphinView {
	env := b.denv()
	best, all := dolphinenv.Detect(env)
	v := DolphinView{Exe: best.Exe, Source: string(best.Source), Found: best.Source != dolphinenv.SourceDefault, Candidates: []DolphinCandidateView{}}
	v.UserDir, v.Portable = dolphinenv.UserDir(env, best.Exe)
	for _, c := range all {
		v.Candidates = append(v.Candidates, DolphinCandidateView{Exe: c.Exe, Source: string(c.Source), Found: c.Found})
	}
	c, _ := b.effective()
	v.Saved, v.SavedDir = c.Dolphin.Exe, c.Dolphin.UserDir
	v.SavedOK = v.Saved != "" && isFile(v.Saved)
	v.Mac = b.macView()
	return v
}

// dolphinProgram is the Dolphin program a chosen path names: a macOS
// Dolphin.app is its program inside, and a folder the Dolphin in it.
func dolphinProgram(p string) string {
	exe := cleanPath(p)
	if strings.EqualFold(filepath.Ext(exe), ".app") {
		return filepath.Join(exe, "Contents", "MacOS", "Dolphin")
	}
	if isDir(exe) {
		for _, n := range []string{"Dolphin.exe", "Dolphin", "dolphin-emu"} {
			if p := filepath.Join(exe, n); isFile(p) {
				return p
			}
		}
	}
	return exe
}

// UserDirFor returns the user folder the Dolphin at exe uses (<exe
// dir>\User when portable, else Dolphin's default), "" when exe is no
// Dolphin program: the Dolphin step shows it whenever the chosen Dolphin
// changes, unless the player set the folder themselves.
func (b *Bridge) UserDirFor(exe string) string {
	p := dolphinProgram(exe)
	if p == "" || !isFile(p) {
		return ""
	}
	dir, _ := dolphinenv.UserDir(b.denv(), p)
	return dir
}

// SetDolphin saves the Dolphin to launch and its user folder ("" is the
// one that Dolphin uses: <exe dir>\User when portable, else Dolphin's
// default). A macOS Dolphin.app is taken as its program inside.
func (b *Bridge) SetDolphin(r DolphinRequest) (err error) {
	defer b.maskErr(&err)
	exe := dolphinProgram(r.Exe)
	if exe == "" {
		return errors.New("choose Dolphin's program first")
	}
	if !isFile(exe) {
		return fmt.Errorf("there's no Dolphin program at %s", exe)
	}
	dir := cleanPath(r.UserDir)
	if dir == "" {
		dir, _ = dolphinenv.UserDir(b.denv(), exe)
	} else if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return fmt.Errorf("%s is a file, not a folder", dir)
	}
	b.mu.Lock()
	b.cfg.Dolphin.Exe, b.cfg.Dolphin.UserDir = exe, dir
	b.mu.Unlock()
	return b.saveErr()
}

// FinishSetup records that the setup was finished ("You're set").
func (b *Bridge) FinishSetup() (err error) {
	defer b.maskErr(&err)
	b.mu.Lock()
	b.cfg.UI.SetupDone = true
	b.mu.Unlock()
	return b.saveErr()
}

// macView is the macOS prepare step's state, nil on other systems.
func (b *Bridge) macView() *MacView {
	st, _, err := b.macStatus()
	return b.macViewOf(st, err)
}

func (b *Bridge) macViewOf(st dolphinenv.MacState, err error) *MacView {
	if err != nil {
		return nil
	}
	home := ""
	if b.home != nil {
		home = b.home()
	}
	v := &MacView{Original: dolphinenv.MacApp, Copy: dolphinenv.MacPreparedApp(home)}
	switch st {
	case dolphinenv.MacReady:
		v.State = "ready"
	case dolphinenv.MacNoDolphin:
		v.State = "no-dolphin"
		v.Text = "Install Dolphin into your Applications folder first (dolphin-emu.org), then come back here."
	case dolphinenv.MacNeedsCopy:
		v.State, v.Prepare = "needs-copy", true
		v.Text = "macOS only lets this app read Dolphin's memory from a copy without Apple's hardened runtime. " +
			"Prepare Dolphin makes that copy in the Applications folder of your home; your original stays untouched and keeps updating."
	case dolphinenv.MacHardened:
		v.State, v.Prepare = "hardened", true
		v.Text = "Your prepared copy of Dolphin still has Apple's hardened runtime, so this app can't read its memory. Prepare it again."
	case dolphinenv.MacCopyOutdated:
		v.State, v.Prepare, v.Refresh = "outdated", true, true
		v.Text = "Dolphin was updated. Refresh the copy so you play on the new version (your settings and saves stay)."
	}
	return v
}

// lineLog collects a reporter's lines (Prepare Dolphin's log).
type lineLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *lineLog) Log(_ report.Level, msg string) {
	l.mu.Lock()
	l.lines = append(l.lines, msg)
	l.mu.Unlock()
}

// PrepareDolphin runs the macOS prepare step (scripts/setup-mac-dolphin.sh
// as Go: dolphinenv.MacPlan) and, when the saved Dolphin is none or the
// original, saves the prepared copy. Elsewhere it says there is nothing
// to prepare.
func (b *Bridge) PrepareDolphin() PrepareView {
	if !b.preparing.TryLock() {
		return PrepareView{Error: "Dolphin is already being prepared."}
	}
	defer b.preparing.Unlock()
	if b.home != nil {
		// Making the copy starts by removing the old one: never under a
		// Dolphin that is running from it.
		app := dolphinenv.MacPreparedApp(b.home())
		for _, exe := range runningExes(b.denv()) {
			if under(exe, app) {
				return PrepareView{Lines: []string{}, Error: "Close Dolphin first, then refresh the copy: it is running from " + app + "."}
			}
		}
	}
	var l lineLog
	err := b.prepareMac(&l)
	v := PrepareView{OK: err == nil, Lines: l.lines}
	if v.Lines == nil {
		v.Lines = []string{}
	}
	if err != nil {
		if errors.Is(err, dolphinenv.ErrNotMac) {
			v.Error = "Only macOS needs a prepared copy of Dolphin."
		} else {
			v.Error = err.Error()
		}
		return v
	}
	if b.home != nil {
		copyExe := filepath.Join(dolphinenv.MacPreparedApp(b.home()), "Contents", "MacOS", "Dolphin")
		b.mu.Lock()
		if b.cfg.Dolphin.Exe == "" || b.cfg.Dolphin.Exe == macOriginalExe || b.cfg.Dolphin.Exe == filepath.FromSlash(macOriginalExe) {
			b.cfg.Dolphin.Exe = copyExe
			if b.cfg.Dolphin.UserDir == "" {
				b.cfg.Dolphin.UserDir, _ = dolphinenv.UserDir(b.denv(), copyExe)
			}
		}
		b.mu.Unlock()
		if err := b.saveErr(); err != nil {
			v.Lines = append(v.Lines, "Couldn't save the settings: "+err.Error())
		}
	}
	return v
}

// runningExes is env's running Dolphin programs (none when it can't say).
func runningExes(env dolphinenv.Env) []string {
	if env.Running == nil {
		return nil
	}
	return env.Running()
}

// under says whether path is dir or inside it (ignoring case, as macOS's
// file system does by default).
func under(path, dir string) bool {
	p, d := filepath.Clean(path), filepath.Clean(dir)
	if strings.EqualFold(p, d) {
		return true
	}
	sep := string(filepath.Separator)
	return len(p) > len(d) && strings.EqualFold(p[:len(d)+1], d+sep)
}

// saveErr writes the config and returns why it couldn't: the setup says so,
// where Host and Join (save) carry on.
func (b *Bridge) saveErr() error {
	if b.cfgPath == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.cfg.Save(b.cfgPath); err != nil {
		return fmt.Errorf("couldn't save your settings: %w", err)
	}
	return nil
}

// macStatusHere is the default Bridge.macStatus.
func macStatusHere() (dolphinenv.MacState, dolphinenv.MacFacts, error) {
	if runtime.GOOS != "darwin" {
		return dolphinenv.MacNoDolphin, dolphinenv.MacFacts{}, dolphinenv.ErrNotMac
	}
	return dolphinenv.MacStatusHere()
}
