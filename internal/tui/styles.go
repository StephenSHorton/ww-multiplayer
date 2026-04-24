package tui

import "github.com/charmbracelet/lipgloss"

// Palette borrowed from the v0.0 TUI for visual continuity. Greens map to
// Wind Waker grass / hyrule-green tones, gold to triforce, danger red for
// disconnected/error states.
var (
	zeldaGreen = lipgloss.Color("#4CAF50")
	zeldaGold  = lipgloss.Color("#FFD700")
	zeldaDark  = lipgloss.Color("#1B5E20")
	subtle     = lipgloss.Color("#666666")
	dim        = lipgloss.Color("#888888")
	white      = lipgloss.Color("#FFFFFF")
	danger     = lipgloss.Color("#FF5252")
	cyan       = lipgloss.Color("#00BCD4")

	titleStyle = lipgloss.NewStyle().
			Foreground(zeldaGreen).
			Bold(true).
			Align(lipgloss.Center)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(zeldaGold).
			Align(lipgloss.Center)

	statusOk = lipgloss.NewStyle().
			Foreground(zeldaGreen).
			Bold(true)

	statusErr = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	labelStyle = lipgloss.NewStyle().
			Foreground(dim)

	valueStyle = lipgloss.NewStyle().
			Foreground(white).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtle).
			Align(lipgloss.Center)

	logTime = lipgloss.NewStyle().
		Foreground(subtle)

	logInfo = lipgloss.NewStyle().
		Foreground(white)

	logGreen = lipgloss.NewStyle().
			Foreground(zeldaGreen)

	logGold = lipgloss.NewStyle().
		Foreground(zeldaGold)

	logCyan = lipgloss.NewStyle().
		Foreground(cyan)

	logWarn = lipgloss.NewStyle().
		Foreground(zeldaGold).
		Bold(true)

	logError = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			Padding(0, 1)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(zeldaGold).
			Bold(true).
			Padding(0, 1)
)
