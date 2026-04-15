package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	zelda_green  = lipgloss.Color("#4CAF50")
	zelda_gold   = lipgloss.Color("#FFD700")
	zelda_dark   = lipgloss.Color("#1B5E20")
	subtle       = lipgloss.Color("#666666")
	danger       = lipgloss.Color("#FF5252")
	white        = lipgloss.Color("#FFFFFF")
	dim          = lipgloss.Color("#888888")

	// Title
	titleStyle = lipgloss.NewStyle().
			Foreground(zelda_green).
			Bold(true).
			Align(lipgloss.Center)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(zelda_gold).
			Align(lipgloss.Center)

	// Cards / Boxes
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			Padding(1, 3).
			Width(28).
			Align(lipgloss.Center)

	selectedCardStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(zelda_green).
				Padding(1, 3).
				Width(28).
				Align(lipgloss.Center).
				Bold(true)

	// Status
	statusOk = lipgloss.NewStyle().
			Foreground(zelda_green).
			Bold(true)

	statusErr = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	// Labels
	labelStyle = lipgloss.NewStyle().
			Foreground(dim)

	valueStyle = lipgloss.NewStyle().
			Foreground(white).
			Bold(true)

	// Help bar
	helpStyle = lipgloss.NewStyle().
			Foreground(subtle).
			Align(lipgloss.Center)

	// Log entries
	logTime = lipgloss.NewStyle().
		Foreground(subtle)

	logInfo = lipgloss.NewStyle().
		Foreground(white)

	logGreen = lipgloss.NewStyle().
			Foreground(zelda_green)

	logGold = lipgloss.NewStyle().
		Foreground(zelda_gold)

	logError = lipgloss.NewStyle().
			Foreground(danger)

	// Dashboard panels
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			Padding(0, 1)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(zelda_gold).
			Bold(true).
			Padding(0, 1)
)
