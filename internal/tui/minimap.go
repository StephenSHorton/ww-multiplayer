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
	// units. The mp-local local-test harness moves Dolphin B's Link 50
	// units in X/Z by default when the two Links overlap at start, without
	// --coop (MP_LOCAL_SHIFT_X/Z, see mp_local_shift.go). At scale=10,
	// that 50-unit offset lands 5 cells off center: clearly separated from
	// "@" but nowhere near the border, and the full grid still spans +-200
	// world units horizontally / +-70 vertically before a remote clamps to
	// the edge glyph.
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
//	width                 panel width, border included. The grid narrows
//	                      to fit when the panel is slimmer than a full grid.
//	rows                  body rows under the title. The grid uses up to
//	                      minimapRows of them (kept odd, so self has a
//	                      center row) and the rest pad the panel, so it can
//	                      match the height of a panel beside it.
//	selfX, selfZ, selfOK  this player's own world coords (LocalPos). selfOK
//	                      false means LocalPos hasn't resolved yet, which
//	                      renders a graceful "no position yet" fallback
//	                      instead of a grid.
//	players               remote players, plotted relative to self.
//	scale                 world units per character cell (WW_MINIMAP_SCALE,
//	                      or defaultMinimapScale when scale <= 0).
func renderMinimap(width, rows int, selfX, selfZ float32, selfOK bool, players []PlayerView, scale float32) string {
	inner := width - 4 // border + padding

	var body []string
	if !selfOK {
		body = []string{truncate(labelStyle.Render("no position yet"), inner)}
	} else {
		cols := clampInt(inner, 1, minimapCols)
		gridRows := clampInt(rows, 1, minimapRows)
		if cols%2 == 0 {
			cols--
		}
		if gridRows%2 == 0 {
			gridRows--
		}
		body = minimapGridLines(selfX, selfZ, players, scale, cols, gridRows)
		for i := range body {
			// A wide glyph (a CJK initial) makes its row one cell
			// wider than cols; cut it rather than let the panel wrap.
			body[i] = truncate(body[i], inner)
		}
	}
	for len(body) < rows {
		body = append(body, "")
	}

	return panelStyle.Width(width - 2).Render(
		panelTitleStyle.Render("Minimap") + "\n" + strings.Join(body, "\n"),
	)
}

// minimapGridLines computes the raw (unstyled) grid rows: self centered as
// '@', remotes plotted at (selfX,selfZ)-relative X/Z scaled into cells and
// clamped to the grid edge, using the uppercased first rune of their name
// (or '•' when nameless). +X moves right, +Z moves down -- an arbitrary but
// internally consistent top-down orientation. Split out from renderMinimap
// so tests can assert exact cell contents without stripping ANSI/border
// styling. cols and rows size the grid (minimapCols x minimapRows at full
// size); both should be odd so the center cell is unambiguous.
func minimapGridLines(selfX, selfZ float32, players []PlayerView, scale float32, cols, rows int) []string {
	if scale <= 0 {
		scale = defaultMinimapScale
	}
	halfCols, halfRows := cols/2, rows/2

	grid := make([][]rune, rows)
	for r := range grid {
		row := make([]rune, cols)
		for c := range row {
			row[c] = minimapEmpty
		}
		grid[r] = row
	}

	for _, p := range players {
		col := halfCols + int(math.Round(float64((p.X-selfX)/scale)))
		row := halfRows + int(math.Round(float64((p.Z-selfZ)/scale)))
		col = clampInt(col, 0, cols-1)
		row = clampInt(row, 0, rows-1)
		grid[row][col] = minimapGlyph(p.Name)
	}

	// Self last so it always wins a same-cell collision with a remote --
	// "@" must always be visible at the center.
	grid[halfRows][halfCols] = minimapSelf

	lines := make([]string, rows)
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
