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
	screen    screen
	width     int
	height    int
	splash    splashModel
	connect   connectModel
	dashboard dashboardModel
	quitting  bool
}

func NewApp() model {
	return model{
		screen: screenSplash,
		splash: newSplash(),
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
			m.quitting = true
			return m, tea.Quit
		}

	case splashDoneMsg:
		m.connect = newConnect()
		m.screen = screenConnect
		return m, nil

	case connectedMsg:
		m.dashboard = newDashboard(msg.role, msg.addr)
		m.screen = screenDashboard
		return m, m.dashboard.tick()

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
		content = m.dashboard.view(m.width, m.height)
	}

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		content,
	)
}

func Run() error {
	p := tea.NewProgram(NewApp(), tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
