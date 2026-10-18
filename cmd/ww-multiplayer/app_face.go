package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// One exe, three faces (design §4.3, owner decision 2): any subcommand is
// the CLI, exactly as before; no arguments opens the app window (the TUI
// in a build without one, see internal/gui); `tui` opens the TUI; `ui`
// opens the window too, and `ui --browser` serves the same UI to a browser.

// face is what a command line opens.
type face int

const (
	faceCLI     face = iota // a subcommand: the command registry (commands.go)
	faceGUI                 // the app window
	faceTUI                 // the Bubble Tea TUI
	faceBrowser             // ui --browser
)

// appFace says what args (os.Args[1:]) open. guiAvailable is
// gui.Available(): without the window, no arguments opens the TUI as it
// always has.
func appFace(args []string, guiAvailable bool) face {
	if len(args) == 0 {
		if guiAvailable {
			return faceGUI
		}
		return faceTUI
	}
	switch args[0] {
	case "tui":
		return faceTUI
	case "ui":
		for _, a := range args[1:] {
			if a == "--browser" {
				return faceBrowser
			}
		}
		if guiAvailable {
			return faceGUI
		}
		return faceTUI
	}
	return faceCLI
}

// stripPSN drops the `-psn_0_NNNN` process-serial-number argument macOS
// LaunchServices can still pass a Finder-launched .app (the first launch
// of a downloaded, translocated bundle, for one). Left in, it would be
// args[0], an unknown subcommand, and the .app (no console) would quit
// silently instead of opening the window. Other OSes keep args as given.
func stripPSN(args []string, goos string) []string {
	if goos != "darwin" {
		return args
	}
	out := args[:0:0]
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			out = append(out, a)
		}
	}
	return out
}

// runFace runs a face other than the CLI's. args are os.Args[1:]; the
// mod flags (--mods, --no-mod, --dev) after `tui` or `ui` choose the
// app's mods for this run.
func runFace(f face, args []string) {
	var faceArgs []string
	if len(args) > 0 {
		faceArgs = args[1:]
	}
	usage := uiUsage + " " + modFlagsUsage
	if len(args) > 0 && args[0] == "tui" {
		usage = tuiUsage
	}
	mf, rest, err := parseModFlags(faceArgs)
	if err == nil && f != faceBrowser && len(rest) > 0 {
		err = fmt.Errorf("unknown argument %q", rest[0])
	}
	if err == nil {
		err = mf.check(modList(true, nil))
	}
	if err != nil {
		exitUsage(err, usage)
	}
	switch f {
	case faceGUI:
		runGUI(mf)
	case faceTUI:
		a := startApp(mf)
		defer a.stop()
		runTUI(a.ctrl)
	case faceBrowser:
		runBrowserUI(mf, rest)
	}
}

const tuiUsage = "tui " + modFlagsUsage

// modFlagsUsage are the app's mod flags (parseModFlags).
const modFlagsUsage = "[--dev] [--mods=a,b] [--no-mod=x]"

// modFlags are the app's mod flags: --mods=a,b runs exactly the mods
// named (every other one is off), --no-mod=x turns x off (both may be
// given more than once, with comma lists), and --dev lists the dev mods
// (devMods: the example mod) and lets them load. They override the saved
// settings (config mods.<id>.enabled) for this run, which override the
// manifests' defaults. `mods` takes them too, to show their effect.
type modFlags struct {
	only    []string // --mods
	onlySet bool     // --mods was given (an empty list runs no mod)
	off     []string // --no-mod
	dev     bool     // --dev
}

// parseModFlags takes the mod flags out of args (--mods=a,b or --mods a,b;
// --no-mod the same; --dev) and returns the other arguments, in order.
func parseModFlags(args []string) (mf modFlags, rest []string, err error) {
	list := func(v string) []string {
		var ids []string
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		switch name {
		case "--dev":
			if hasVal {
				return modFlags{}, nil, fmt.Errorf("--dev takes no value (%q)", a)
			}
			mf.dev = true
			continue
		case "--mods", "--no-mod":
		default:
			rest = append(rest, a)
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return modFlags{}, nil, fmt.Errorf("%s needs a list of mod ids", name)
			}
			i++
			val = args[i]
		}
		if name == "--mods" {
			mf.onlySet = true
			mf.only = append(mf.only, list(val)...)
			continue
		}
		ids := list(val)
		if len(ids) == 0 {
			return modFlags{}, nil, errors.New("--no-mod needs a mod id")
		}
		mf.off = append(mf.off, ids...)
	}
	return mf, rest, nil
}

// check refuses a mod id the flags name that is not in mods, saying so
// when it is a dev mod without --dev.
func (mf modFlags) check(mods []wwapi.Mod) error {
	known := map[string]bool{}
	var ids []string
	for _, m := range mods {
		man := m.Manifest()
		known[man.ID] = true
		if !man.Dev || mf.dev {
			ids = append(ids, man.ID)
		}
	}
	for _, id := range append(slices.Clone(mf.only), mf.off...) {
		if slices.Contains(ids, id) {
			continue
		}
		if known[id] {
			return fmt.Errorf("%s is a dev mod: add --dev", id)
		}
		return fmt.Errorf("no mod %q in this version (it has %s)", id, strings.Join(ids, ", "))
	}
	return nil
}

// enabled is what the flags say about mod id: on or off, and whether they
// say anything.
func (mf modFlags) enabled(id string) (on, set bool) {
	if mf.onlySet {
		return slices.Contains(mf.only, id) && !slices.Contains(mf.off, id), true
	}
	if slices.Contains(mf.off, id) {
		return false, true
	}
	return false, false
}

// resolveOptions resolve the mods by the flags alone (`mods`: the CLI
// reads no settings).
func (mf modFlags) resolveOptions() wwapi.ResolveOptions {
	return wwapi.ResolveOptions{Dev: mf.dev, Enabled: mf.enabled}
}

// appModConfig is the app's wwapi.ConfigSource: the mod flags over the
// saved settings (config.Mods, which internal/gui reads: package main
// never imports internal/config, TestCLIReadsNoConfig).
type appModConfig struct {
	saved wwapi.ConfigSource
	flags modFlags
}

func (c appModConfig) ModEnabled(id string) (on, set bool) {
	if on, set := c.flags.enabled(id); set {
		return on, true
	}
	if c.saved == nil {
		return false, false
	}
	return c.saved.ModEnabled(id)
}

func (c appModConfig) ModSettings(id string) json.RawMessage {
	if c.saved == nil {
		return nil
	}
	return c.saved.ModSettings(id)
}

// app is a face's mods and session controller: the mod runtime
// (wwapi.Runtime: the saved settings and the flags, the event bus, the
// supervisor) and the controller over the session engine of its loaded
// NetProvider, the multiplayer mod.
type app struct {
	rt    *wwapi.Runtime
	ctrl  *session.Controller
	flags modFlags
	stop  func() // stops the runtime and waits for it
}

// startApp builds the app's mod runtime from the saved settings (saved,
// gui.ModConfig) and mf, runs the Inits, starts it, and makes the
// controller over the loaded NetProvider's engine (none when the
// multiplayer mod is off: Host and Join then say so). That engine runs
// co-op in its lobby sessions only when the co-op mod loaded.
func startApp(mf modFlags) *app { return newApp(mf, gui.ModConfig("")) }

func newApp(mf modFlags, saved wwapi.ConfigSource) *app {
	var rt *wwapi.Runtime
	mods := modList(mf.dev, func() session.Engine { return newAppEngine(modLoaded(rt, "coop"), runtimeNetMods(rt)) })
	lg := &lateLog{}
	rt = wwapi.New(mods, wwapi.Options{
		Resolve: wwapi.ResolveOptions{Dev: mf.dev},
		Config:  appModConfig{saved: saved, flags: mf},
		Log:     lg,
	})
	rt.Init()
	ctrl := session.New(netEngine(rt), session.Options{
		SessionLog: tuiSessionLog,
		Bus:        rt.Bus(),
		Mods:       func() []session.ModStatus { return modStatuses(rt.Mods()) },
	})
	lg.set(ctrl.ModLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = rt.Run(ctx)
	}()
	var once sync.Once
	return &app{rt: rt, ctrl: ctrl, flags: mf, stop: func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}}
}

// modLoaded says whether mod id is LOADED in rt.
func modLoaded(rt *wwapi.Runtime, id string) bool {
	if rt == nil {
		return false
	}
	s, ok := rt.Status(id)
	return ok && s.State == wwapi.Loaded
}

// runtimeMods are rt's LOADED mods, in load order.
func runtimeMods(rt *wwapi.Runtime) []wwapi.Mod {
	var mods []wwapi.Mod
	for _, m := range planMods(rt.Plan()) {
		if modLoaded(rt, m.Manifest().ID) {
			mods = append(mods, m)
		}
	}
	return mods
}

// netEngine is the session engine of rt's loaded NetProvider, or nil.
func netEngine(rt *wwapi.Runtime) session.Engine {
	for _, m := range runtimeMods(rt) {
		if np, ok := m.(wwapi.NetProvider); ok {
			return np.Engine()
		}
	}
	return nil
}

// modStatuses are the runtime's ModInfos as the session controller shows
// them.
func modStatuses(infos []wwapi.ModInfo) []session.ModStatus {
	out := make([]session.ModStatus, 0, len(infos))
	for _, mi := range infos {
		out = append(out, session.ModStatus{ID: mi.ID, Name: mi.Name, Version: mi.Version,
			State: string(mi.Status), Reason: mi.Reason, Missing: mi.Missing})
	}
	return out
}

// lateLog is the runtime's Reporter: the controller's ModLog once it
// exists (the runtime is made first, for its bus); lines before that are
// dropped (a failed Init shows as the mod's status).
type lateLog struct {
	mu sync.Mutex
	r  log.Reporter
}

func (l *lateLog) set(r log.Reporter) {
	l.mu.Lock()
	l.r = r
	l.mu.Unlock()
}

func (l *lateLog) Log(level log.Level, msg string) {
	l.mu.Lock()
	r := l.r
	l.mu.Unlock()
	if r != nil {
		r.Log(level, msg)
	}
}

// runGUI opens the app window; if it can't (no WebView2, for one), the TUI
// takes over in the console with a line saying why.
func runGUI(mf modFlags) {
	a := startApp(mf)
	defer a.stop()
	err := gui.Run(guiOptions(a))
	if err == nil {
		return
	}
	fmt.Printf("The app window couldn't open (%v).\nOpening the terminal menu instead; `%s ui --browser` opens the app in your browser.\n\n", err, exeCmd)
	runTUI(a.ctrl)
}

// guiOptions are the window's and ui --browser's options. The app reads
// its saved settings itself (internal/gui); package main never imports
// internal/config, so the CLI can't read them (TestCLIReadsNoConfig). The
// setup's patch runs the loaded mods' post-patch steps, as `patch` runs
// the CLI's (the co-op tables). The Mods page reads the runtime.
func guiOptions(a *app) gui.Options {
	return gui.Options{Version: version, Controller: a.ctrl, Getenv: os.Getenv, PostPatch: patchSteps(runtimeMods(a.rt)),
		HostIPs: engine.ListHostIPs, Mods: a.rt, ModFlag: a.flags.enabled, DevMods: a.flags.dev, Args: os.Args[1:]}
}

// runTUI runs the Bubble Tea TUI (what no arguments opened before the app
// window). v0.0's TUI was retired in v0.1.2 (a35d4ec) because it never
// wired the connect screen to the pose-feed pipeline; v0.1.5 brought it
// back with the dashboard driving the same engine.RunHostSession / engine.RunJoinSession
// funcs the CLI subcommands run, through a session.Controller over
// engine.Engine (mods/multiplayer/engine), the multiplayer mod's.
func runTUI(ctrl *session.Controller) {
	notice := ""
	if gui.UpdateCheckEnabled() { // the app's "Check for updates" switch
		notice = updateNotice()
	}
	if err := tui.RunNotice(version, notice, ctrl); err != nil {
		fmt.Println(err)
		fmt.Print("Press Enter to exit...")
		bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(1)
	}
}

const uiUsage = "ui [--browser [--port N] [--no-open]]"

// runBrowserUI is `ui --browser [--port N] [--no-open]` (args without the
// mod flags): the app's UI served on 127.0.0.1 (a free port unless
// --port), opened in the default browser, until Ctrl+C.
func runBrowserUI(mf modFlags, args []string) {
	usage := uiUsage + " " + modFlagsUsage
	port, open := 0, true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--browser":
		case "--no-open":
			open = false
		case "--port":
			if i+1 >= len(args) {
				exitUsage(errors.New("--port needs a number"), usage)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || n > 65535 {
				exitUsage(fmt.Errorf("bad --port %q", args[i]), usage)
			}
			port = n
		default:
			exitUsage(fmt.Errorf("unknown argument %q", args[i]), usage)
		}
	}
	a := startApp(mf)
	defer a.stop()
	srv, err := gui.ServeBrowser(guiOptions(a), port)
	if err != nil {
		fmt.Printf("ui: %v\n", err)
		a.stop()
		os.Exit(1)
	}
	fmt.Println("The app is at (keep this window open; Ctrl+C stops it):")
	fmt.Println("  " + srv.URL)
	if open {
		if err := openBrowser(srv.URL); err != nil {
			fmt.Printf("Couldn't open a browser (%v); open the address above yourself.\n", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_ = srv.Wait(ctx)
}

// openBrowser opens url in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
