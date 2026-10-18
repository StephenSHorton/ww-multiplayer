package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestUpdateNotice: the one-line update notice shows over the splash and
// the connect screen, and nothing shows without one.
func TestUpdateNotice(t *testing.T) {
	const n = "Update available: v0.5.0 (you have v0.4.0)."
	m := newModel(nil, "v0.4.0")
	m.notice = n
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !strings.Contains(mm.View(), "Update available: v0.5.0") {
		t.Fatal("the splash has no notice")
	}
	plain := newModel(nil, "v0.4.0")
	pm, _ := plain.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if strings.Contains(pm.View(), "Update available") {
		t.Fatal("a notice without one")
	}
}
