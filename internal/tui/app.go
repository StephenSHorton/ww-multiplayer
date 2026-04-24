// Package tui drives the no-args entry point: a three-screen Bubble Tea
// app (splash -> connect -> dashboard) that wires the connect screen's
// Host/Join choice into the same multiplayer session funcs the CLI
// `host` and `join` subcommands run, surfacing every Reporter log line
// in the dashboard's log panel.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
func Run(version string, hooks Hooks) error {
	p := tea.NewProgram(newModel(hooks, version), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
