package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
)

// One exe, three faces (design §4.3, owner decision 2): any subcommand is
// the CLI, exactly as before; no arguments opens the app window (the TUI
// in a build without one, see internal/gui); `tui` opens the TUI; `ui`
// opens the window too, and `ui --browser` serves the same UI to a browser.

// face is what a command line opens.
type face int

const (
	faceCLI     face = iota // a subcommand: main's switch
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

// runFace runs a face other than the CLI's.
func runFace(f face, args []string) {
	switch f {
	case faceGUI:
		runGUI()
	case faceTUI:
		runTUI(newAppController())
	case faceBrowser:
		runBrowserUI(args)
	}
}

// newAppController is the controller the TUI and the GUI drive:
// sessionEngine with liveState, and a log file per session.
func newAppController() *session.Controller {
	return session.New(newTUIEngine(), session.Options{SessionLog: tuiSessionLog})
}

// runGUI opens the app window; if it can't (no WebView2, for one), the TUI
// takes over in the console with a line saying why.
func runGUI() {
	ctrl := newAppController()
	err := gui.Run(guiOptions(ctrl))
	if err == nil {
		return
	}
	fmt.Printf("The app window couldn't open (%v).\nOpening the terminal menu instead; `%s ui --browser` opens the app in your browser.\n\n", err, exeCmd)
	runTUI(ctrl)
}

// guiOptions are the window's and ui --browser's options. The app reads
// its saved settings itself (internal/gui); package main never imports
// internal/config, so the CLI can't read them (TestCLIReadsNoConfig). The
// setup's patch runs the co-op tables step the `patch` command runs.
func guiOptions(ctrl *session.Controller) gui.Options {
	return gui.Options{Version: version, Controller: ctrl, Getenv: os.Getenv, CoopTables: cacheCoopTablesForPatch, HostIPs: listHostIPs}
}

// runTUI runs the Bubble Tea TUI (what no arguments opened before the app
// window). v0.0's TUI was retired in v0.1.2 (a35d4ec) because it never
// wired the connect screen to the pose-feed pipeline; v0.1.5 brought it
// back with the dashboard driving the same runHostSession / runJoinSession
// funcs the CLI subcommands run, through a session.Controller over
// sessionEngine (session_engine.go).
func runTUI(ctrl *session.Controller) {
	if err := tui.Run(version, ctrl); err != nil {
		fmt.Println(err)
		fmt.Print("Press Enter to exit...")
		bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(1)
	}
}

const uiUsage = "ui [--browser [--port N] [--no-open]]"

// runBrowserUI is `ui --browser [--port N] [--no-open]`: the app's UI served
// on 127.0.0.1 (a free port unless --port), opened in the default browser,
// until Ctrl+C.
func runBrowserUI(args []string) {
	port, open := 0, true
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--browser":
		case "--no-open":
			open = false
		case "--port":
			if i+1 >= len(args) {
				exitUsage(errors.New("--port needs a number"), uiUsage)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || n > 65535 {
				exitUsage(fmt.Errorf("bad --port %q", args[i]), uiUsage)
			}
			port = n
		default:
			exitUsage(fmt.Errorf("unknown argument %q", args[i]), uiUsage)
		}
	}
	srv, err := gui.ServeBrowser(guiOptions(newAppController()), port)
	if err != nil {
		fmt.Printf("ui: %v\n", err)
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
