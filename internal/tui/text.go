package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Text helpers that keep rendered rows inside a known width. lipgloss wraps
// any line longer than a panel's Width, and every wrapped row pushes the
// dashboard past the terminal height, where Bubble Tea's renderer drops rows
// off the TOP (the header). So single-line elements are truncated and
// multi-line feeds (log, chat) are wrapped inside a fixed row budget.

// truncate cuts s, which may carry ANSI styling, to at most w cells, ending
// in "…" when anything was cut. Rune-, wide-character- and ANSI-safe: the
// cut goes through lipgloss MaxWidth, which uses ansi.Truncate.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(w-1).Render(s) + "…"
}

// displayText makes one log or chat entry safe to lay out as a single
// logical line: every control character (newline, tab, and the ESC that
// would let a peer's chat line inject terminal sequences) becomes a space.
func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// wrapRows wraps s, which may carry ANSI styling, into rows of at most width
// cells. Each row is padded to width.
func wrapRows(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// wrapTail lays out entries (oldest first) as wrapped rows at most width
// cells wide and returns exactly rows of them: the newest entries, with the
// oldest visible one cut from the top like a scrolled terminal, blank-padded
// at the bottom when there are too few. A newest entry that alone is taller
// than rows keeps its start, which is where an error says what went wrong.
func wrapTail(entries []string, rows, width int) []string {
	if rows <= 0 {
		return nil
	}
	var out []string
	for i := len(entries) - 1; i >= 0 && len(out) < rows; i-- {
		block := wrapRows(entries[i], width)
		if len(out) == 0 && len(block) > rows {
			block = block[:rows]
		}
		out = append(block, out...)
	}
	if len(out) > rows {
		out = out[len(out)-rows:]
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// clipRows keeps the first n rows of s. The dashboard layout already fits
// any terminal with room for its minimum panels; this is the backstop for
// tinier ones, where losing the bottom (chat, footer) beats letting the
// renderer drop the header off the top.
func clipRows(s string, n int) string {
	lines := strings.Split(s, "\n")
	if n < 0 || len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}
