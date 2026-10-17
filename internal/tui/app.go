// Package tui drives the no-args entry point: a three-screen Bubble Tea
// app (splash -> connect -> dashboard) that wires the connect screen's
// Host/Join choice into the same multiplayer session funcs the CLI
// `host` and `join` subcommands run, surfacing every Reporter log line
// in the dashboard's log panel. It reaches those funcs only through a
// session.Controller, never through wwapi/emu or mods/multiplayer/net.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
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
	ctrl    *session.Controller
	version string

	screen    screen
	width     int
	height    int
	splash    splashModel
	connect   connectModel
	dashboard dashboardModel
	quitting  bool

	// showIPs is showIPsKey's toggle: off (the default), every screen
	// masks the IP addresses it draws (internal/redact; owner decision
	// 2026-10-16, streamer-safe). The TUI doesn't save it. The CLI's host
	// and join print the address to share on purpose, unmasked.
	showIPs bool
}

// showIPsKey shows or masks the IP addresses on every screen. It is a
// control key because the connect fields and the chat take letters.
const showIPsKey = "ctrl+r"

// ipsHelp is the help line's entry for showIPsKey.
func ipsHelp(shown bool) string {
	if shown {
		return showIPsKey + ": hide IPs"
	}
	return showIPsKey + ": show IPs"
}

func newModel(ctrl *session.Controller, version string) model {
	return model{
		ctrl:    ctrl,
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
			if m.screen == screenDashboard {
				m.dashboard.leave()
			}
			m.quitting = true
			return m, tea.Quit
		}
		if msg.String() == showIPsKey {
			m.showIPs = !m.showIPs
			m.connect.showIPs = m.showIPs
			m.dashboard.showIPs = m.showIPs
			return m, nil
		}

	case splashDoneMsg:
		m.connect = newConnect()
		m.connect.worlds = coopWorlds(m.ctrl)
		m.connect.mods = modsNotLoaded(m.ctrl)
		m.connect.showIPs = m.showIPs
		m.screen = screenConnect
		return m, nil

	case connectedMsg:
		// A session that can't start (no controller in this build, or
		// the controller refuses) leaves the user on the connect screen
		// with the reason.
		d, err := startDashboard(m.ctrl, msg.role, msg.name, msg.addr, msg.coop, m.width)
		if err != nil {
			m.connect.err = err.Error()
			return m, nil
		}
		d.showIPs = m.showIPs
		m.dashboard = d
		m.screen = screenDashboard
		return m, m.dashboard.initCmd()

	case backMsg:
		// Keep what the user typed (role, IP, name, co-op, focus) so a
		// retry after a failed session is one Enter; only drop a stale
		// error. The worlds are listed again: the session may have
		// founded one.
		m.connect.err = ""
		m.connect.worlds = coopWorlds(m.ctrl)
		m.connect.mods = modsNotLoaded(m.ctrl)
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

// Run starts the TUI. The version string is shown on the splash; ctrl
// runs the multiplayer sessions main injected into it, so this package
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
func Run(version string, ctrl *session.Controller) error {
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

	p := tea.NewProgram(newModel(ctrl, version), tea.WithAltScreen(), tea.WithInputTTY())
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

// coopWorlds lists the co-op worlds a host may continue, or none when
// ctrl can't (no controller, or no co-op in this build).
func coopWorlds(ctrl *session.Controller) []session.CoopWorld {
	if ctrl == nil {
		return nil
	}
	ws, err := ctrl.CoopWorlds()
	if err != nil {
		return nil
	}
	return ws
}

// startDashboard subscribes to ctrl's events, starts the session for
// role with co-op as coop says, and builds its dashboard. It subscribes
// first so the session's first log lines reach the dashboard. width is
// the terminal width as last reported (0 before the first
// WindowSizeMsg).
func startDashboard(ctrl *session.Controller, role, name, addr string, coop session.CoopOptions, width int) (dashboardModel, error) {
	if ctrl == nil {
		if role == string(session.RoleHost) {
			return dashboardModel{}, errors.New("Hosting isn't available in this build (no session controller)")
		}
		return dashboardModel{}, errors.New("Joining isn't available in this build (no session controller)")
	}
	events, unsubscribe := ctrl.Subscribe()
	var (
		id  session.ID
		err error
	)
	if role == string(session.RoleHost) {
		id, err = ctrl.Host(context.Background(), session.HostOptions{Name: name, Coop: coop})
	} else {
		id, err = ctrl.Join(context.Background(), session.JoinOptions{Addr: addr, Name: name, Coop: coop})
	}
	if err != nil {
		unsubscribe()
		return dashboardModel{}, err
	}
	d := newDashboard(ctrl, id, events, unsubscribe, role, name, addr, width)
	d.coop = coop.On
	return d, nil
}

// exeName is the binary as the user types it on this platform, used in the
// headless-stdin error so the suggested commands are copy-pasteable.
var exeName = func() string {
	if runtime.GOOS == "windows" {
		return `.\ww-multiplayer.exe` // PowerShell needs the .\ ; cmd.exe accepts it
	}
	return "./ww-multiplayer"
}()
