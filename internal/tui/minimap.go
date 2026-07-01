package tui

import (
	"math"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Minimap panel (#22): a compact top-down text render of the local player
// and nearby remotes on the X-Z ground plane. WW's horizontal plane is X/Z
// (see WW_LINK2_OFFSET_X / WW_LINK2_OFFSET_Z in main.go's loopback-render
// offset) -- Y is vertical/up and intentionally not plotted.

const (
	// minimapCols / minimapRows size the grid. Both are odd so there is a
	// single, unambiguous center cell for "self" -- no fractional center.
	// ~40x15 per #22: compact enough to sit above the log panel without
	// dominating the dashboard.
	minimapCols = 41
	minimapRows = 15

	minimapHalfCols = minimapCols / 2 // 20
	minimapHalfRows = minimapRows / 2 // 7

	minimapEmpty = '.'
	minimapSelf  = '@'

	// defaultMinimapScale is the world-units-per-character-cell used when
	// WW_MINIMAP_SCALE is unset or unparseable. WW world coords run into
	// the thousands, but the minimap only needs to resolve the RELATIVE
	// distance between nearby co-op players -- tens to low hundreds of
	// units. The mp-local local-test harness separates its two Dolphins by
	// 50 units in X/Z by default (MP_LOCAL_SHIFT_X/Z, see mp_local.go). At
	// scale=10, that 50-unit offset lands 5 cells off center: clearly
	// separated from "@" but nowhere near the border, and the full grid
	// still spans +-200 world units horizontally / +-70 vertically before
	// a remote clamps to the edge glyph.
	defaultMinimapScale = 10.0
)

// minimapScaleFromEnv reads WW_MINIMAP_SCALE once. Callers should cache the
// result (see dashboardModel.minimapScale, set at panel/model construction)
// rather than calling this per render.
func minimapScaleFromEnv() float32 {
	v := os.Getenv("WW_MINIMAP_SCALE")
	if v == "" {
		return defaultMinimapScale
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil || f <= 0 {
		return defaultMinimapScale
	}
	return float32(f)
}

// renderMinimap builds the compact #22 minimap panel. Like renderStatus,
// it's PURE (state in, string out) so it's unit-testable without a running
// tea.Program.
//
//	width                 panel width; floors like renderStatus.
//	selfX, selfZ, selfOK  this player's own world coords (LocalPos). selfOK
//	                      false means LocalPos hasn't resolved yet, which
//	                      renders a graceful "no position yet" fallback
//	                      instead of a grid.
//	players               remote players, plotted relative to self.
//	scale                 world units per character cell (WW_MINIMAP_SCALE,
//	                      or defaultMinimapScale when scale <= 0).
func renderMinimap(width int, selfX, selfZ float32, selfOK bool, players []PlayerView, scale float32) string {
	if width < 60 {
		width = 60
	}

	var body string
	if !selfOK {
		body = labelStyle.Render("no position yet")
	} else {
		body = strings.Join(minimapGridLines(selfX, selfZ, players, scale), "\n")
	}

	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Minimap") + "\n" + body,
	)
}

// minimapGridLines computes the raw (unstyled) grid rows: self centered as
// '@', remotes plotted at (selfX,selfZ)-relative X/Z scaled into cells and
// clamped to the grid edge, using the uppercased first rune of their name
// (or '•' when nameless). +X moves right, +Z moves down -- an arbitrary but
// internally consistent top-down orientation. Split out from renderMinimap
// so tests can assert exact cell contents without stripping ANSI/border
// styling.
func minimapGridLines(selfX, selfZ float32, players []PlayerView, scale float32) []string {
	if scale <= 0 {
		scale = defaultMinimapScale
	}

	grid := make([][]rune, minimapRows)
	for r := range grid {
		row := make([]rune, minimapCols)
		for c := range row {
			row[c] = minimapEmpty
		}
		grid[r] = row
	}

	for _, p := range players {
		col := minimapHalfCols + int(math.Round(float64((p.X-selfX)/scale)))
		row := minimapHalfRows + int(math.Round(float64((p.Z-selfZ)/scale)))
		col = clampInt(col, 0, minimapCols-1)
		row = clampInt(row, 0, minimapRows-1)
		grid[row][col] = minimapGlyph(p.Name)
	}

	// Self last so it always wins a same-cell collision with a remote --
	// "@" must always be visible at the center.
	grid[minimapHalfRows][minimapHalfCols] = minimapSelf

	lines := make([]string, minimapRows)
	for i, row := range grid {
		lines[i] = string(row)
	}
	return lines
}

// minimapGlyph picks a remote's map marker: the uppercased first rune of
// their name, or a bullet when nameless (mirrors renderStatus's "player N"
// fallback intent, but a single glyph is all a grid cell can hold).
func minimapGlyph(name string) rune {
	for _, r := range name {
		return unicode.ToUpper(r)
	}
	return '•'
}

// clampInt restricts v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
