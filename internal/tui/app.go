// Package tui drives the no-args entry point: a three-screen Bubble Tea
// app (splash -> connect -> dashboard) that wires the connect screen's
// Host/Join choice into the same multiplayer session funcs the CLI
// `host` and `join` subcommands run, surfacing every Reporter log line
// in the dashboard's log panel.
package tui

import (
	"fmt"
	"os"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	isatty "github.com/mattn/go-isatty"
)

type screen int

const (
	screenSplash screen = iota
	screenConnect
	screenDashboard
)

type model struct {
	hooks   Hooks
	version string

	screen    screen
	width     int
	height    int
	splash    splashModel
	connect   connectModel
	dashboard dashboardModel
	quitting  bool
}

func newModel(hooks Hooks, version string) model {
	return model{
		hooks:   hooks,
		version: version,
		screen:  screenSplash,
		splash:  newSplash(version),
	}
}

func (m model) Init() tea.Cmd {
	return m.splash.tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			// On Ctrl+C from the dashboard, tear down the session
			// before quitting so the network goroutines get a chance
			// to clearMultiplayerState() (mailbox shadow_mode=0,
			// pose_seqs[*]=0). Otherwise the receiving Dolphin
			// keeps rendering Link #2 frozen at the last pose.
			if m.screen == screenDashboard && m.dashboard.sess != nil {
				m.dashboard.sess.stop()
			}
			m.quitting = true
			return m, tea.Quit
		}

	case splashDoneMsg:
		m.connect = newConnect()
		m.screen = screenConnect
		return m, nil

	case connectedMsg:
		m.dashboard = newDashboard(m.hooks, msg.role, msg.name, msg.addr)
		m.screen = screenDashboard
		return m, m.dashboard.initCmd()

	case backMsg:
		m.connect = newConnect()
		m.screen = screenConnect
		return m, nil
	}

	var cmd tea.Cmd
	switch m.screen {
	case screenSplash:
		m.splash, cmd = m.splash.update(msg)
	case screenConnect:
		m.connect, cmd = m.connect.update(msg)
	case screenDashboard:
		m.dashboard, cmd = m.dashboard.update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var content string
	switch m.screen {
	case screenSplash:
		content = m.splash.view(m.width)
	case screenConnect:
		content = m.connect.view(m.width)
	case screenDashboard:
		// Dashboard wants full width/height; skip lipgloss.Place center.
		return m.dashboard.view(m.width, m.height)
	}
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		content,
	)
}

// Run starts the TUI. The version string is shown on the splash; hooks
// inject the multiplayer-session functions from main so this package
// stays free of dolphin / network imports.
//
// stdin must be attached to a usable interactive terminal. When launched
// from Git Bash / MSYS2 MinTTY, os.Stdin is frequently a non-console
// handle even though a real console is attached to the process; passing
// tea.WithInputTTY() makes Bubble Tea open a fresh console input handle
// (CONIN$ on Windows, /dev/tty elsewhere) instead of trusting that
// possibly-broken inherited handle, which is what fixes the "error making
// raw: The parameter is incorrect" crash. If stdin isn't attached to any
// interactive terminal at all (piped/redirected input, no console), we
// bail out before ever touching Bubble Tea with a clear, actionable error
// instead of letting the raw-mode call die cryptically.
func Run(version string, hooks Hooks) error {
	if !hasInteractiveStdin() {
		return fmt.Errorf(
			"no interactive terminal detected on stdin.\n"+
				"Run %s from a real console (Windows Terminal, PowerShell, or cmd.exe), "+
				"or skip the TUI entirely and use:\n"+
				"  %s host [name]\n"+
				"  %s join <host-ip> [name]",
			exeName, exeName, exeName,
		)
	}

	p := tea.NewProgram(newModel(hooks, version), tea.WithAltScreen(), tea.WithInputTTY())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

// hasInteractiveStdin reports whether stdin looks attached to something a
// human could type into. isatty.IsTerminal covers a real Windows console /
// unix tty; IsCygwinTerminal additionally covers the MSYS2/Cygwin pty pipes
// MinTTY (Git Bash) uses, which IsTerminal alone doesn't recognize. Piped
// or redirected stdin (e.g. `... < file`, or no console at all) returns
// false from both, which is the genuinely-headless case we want to refuse
// up front rather than crash inside Bubble Tea.
//
// It's a package-level var rather than a plain func so tests can stub it
// out instead of needing a real interactive terminal attached to `go test`.
var hasInteractiveStdin = func() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// exeName is the binary as the user types it on this platform, used in the
// headless-stdin error so the suggested commands are copy-pasteable.
var exeName = func() string {
	if runtime.GOOS == "windows" {
		return "ww-multiplayer.exe"
	}
	return "./ww-multiplayer"
}()
