package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Triforce ASCII reveal — the v0.0 TUI's signature opener. Each frame
// adds one more line until all 12 are visible, after which the title
// fades in and the screen waits for any keypress.
var triforceLines = []string{
	"            ▲            ",
	"           ╱ ╲           ",
	"          ╱   ╲          ",
	"         ╱     ╲         ",
	"        ╱       ╲        ",
	"       ╱─────────╲       ",
	"      ╱╲         ╱╲      ",
	"     ╱  ╲       ╱  ╲     ",
	"    ╱    ╲     ╱    ╲    ",
	"   ╱      ╲   ╱      ╲   ",
	"  ╱        ╲ ╱        ╲  ",
	" ╱──────────╲╱──────────╲ ",
}

type splashModel struct {
	frame    int
	maxFrame int
	version  string
}

type splashTickMsg struct{}
type splashDoneMsg struct{}

func newSplash(version string) splashModel {
	return splashModel{frame: 0, maxFrame: 12, version: version}
}

func (m splashModel) tick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return splashTickMsg{}
	})
}

func (m splashModel) update(msg tea.Msg) (splashModel, tea.Cmd) {
	switch msg.(type) {
	case splashTickMsg:
		if m.frame < m.maxFrame {
			m.frame++
			return m, m.tick()
		}

	case tea.KeyMsg:
		if m.frame >= m.maxFrame {
			return m, func() tea.Msg { return splashDoneMsg{} }
		}
		// Skip animation
		m.frame = m.maxFrame
		return m, nil
	}
	return m, nil
}

func (m splashModel) view(width int) string {
	var b strings.Builder

	triforceStyle := lipgloss.NewStyle().
		Foreground(zeldaGold).
		Bold(true)

	var visibleLines []string
	for i, line := range triforceLines {
		if i <= m.frame {
			visibleLines = append(visibleLines, line)
		}
	}
	art := strings.Join(visibleLines, "\n")

	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		triforceStyle.Render(art),
	))
	b.WriteString("\n\n")

	if m.frame >= m.maxFrame {
		title := lipgloss.NewStyle().
			Foreground(zeldaGreen).
			Bold(true).
			Width(width).
			Align(lipgloss.Center).
			Render("T H E   W I N D   W A K E R")

		subtitle := lipgloss.NewStyle().
			Foreground(white).
			Width(width).
			Align(lipgloss.Center).
			Render("M U L T I P L A Y E R")

		ver := lipgloss.NewStyle().
			Foreground(subtle).
			Width(width).
			Align(lipgloss.Center).
			Render(m.version)

		prompt := lipgloss.NewStyle().
			Foreground(dim).
			Width(width).
			Align(lipgloss.Center).
			Blink(true).
			Render("press any key to continue")

		b.WriteString(title)
		b.WriteString("\n")
		b.WriteString(subtitle)
		b.WriteString("\n")
		b.WriteString(ver)
		b.WriteString("\n\n\n")
		b.WriteString(prompt)
	}

	return b.String()
}
