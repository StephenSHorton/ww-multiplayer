package tui

import (
	"os"
	"strings"
	"testing"
)

// TestMinimapGridLines_SelfCenteredWhenAlone asserts that with no remote
// players, the grid renders self ('@') at the exact center cell and nothing
// else.
func TestMinimapGridLines_SelfCenteredWhenAlone(t *testing.T) {
	lines := minimapGridLines(100, 200, nil, defaultMinimapScale)

	if len(lines) != minimapRows {
		t.Fatalf("minimapGridLines returned %d rows, want %d", len(lines), minimapRows)
	}
	centerRow := []rune(lines[minimapHalfRows])
	if len(centerRow) != minimapCols {
		t.Fatalf("center row has %d cols, want %d", len(centerRow), minimapCols)
	}
	if centerRow[minimapHalfCols] != minimapSelf {
		t.Errorf("center cell = %q, want %q (self)", centerRow[minimapHalfCols], minimapSelf)
	}

	// No other cell should be marked -- everything but the center is
	// unexplored grid.
	for r, line := range lines {
		for c, ch := range line {
			if r == minimapHalfRows && c == minimapHalfCols {
				continue
			}
			if ch != minimapEmpty {
				t.Errorf("unexpected glyph %q at (row=%d,col=%d) with no players present", ch, r, c)
			}
		}
	}
}

// TestMinimapGridLines_RemotePlusX asserts a remote offset only in +X lands
// strictly to the right of center, on the same row as center (since dz=0).
func TestMinimapGridLines_RemotePlusX(t *testing.T) {
	const selfX, selfZ float32 = 1000, 2000
	scale := float32(10)
	players := []PlayerView{{ID: 2, Name: "Zelda", X: selfX + 50, Y: 0, Z: selfZ}}

	lines := minimapGridLines(selfX, selfZ, players, scale)
	wantCol := minimapHalfCols + 5 // 50 world units / scale(10) = 5 cells
	row := []rune(lines[minimapHalfRows])

	if row[wantCol] != 'Z' {
		t.Errorf("expected remote glyph 'Z' at (row=%d,col=%d), got %q\ngrid:\n%s",
			minimapHalfRows, wantCol, row[wantCol], strings.Join(lines, "\n"))
	}
	if wantCol <= minimapHalfCols {
		t.Fatalf("test setup bug: wantCol %d should be right of center %d", wantCol, minimapHalfCols)
	}
	// Center itself must still show self, not get clobbered.
	if row[minimapHalfCols] != minimapSelf {
		t.Errorf("center cell should still be self %q, got %q", minimapSelf, row[minimapHalfCols])
	}
}

// TestMinimapGridLines_RemotePlusZ asserts a remote offset only in +Z lands
// strictly below center, in the same column as center (since dx=0).
func TestMinimapGridLines_RemotePlusZ(t *testing.T) {
	const selfX, selfZ float32 = -500, 0
	scale := float32(10)
	players := []PlayerView{{ID: 3, Name: "ganon", X: selfX, Y: 0, Z: selfZ + 50}}

	lines := minimapGridLines(selfX, selfZ, players, scale)
	wantRow := minimapHalfRows + 5 // 50 / 10 = 5 cells down

	if wantRow <= minimapHalfRows {
		t.Fatalf("test setup bug: wantRow %d should be below center %d", wantRow, minimapHalfRows)
	}
	got := []rune(lines[wantRow])[minimapHalfCols]
	if got != 'G' { // uppercased first rune of "ganon"
		t.Errorf("expected remote glyph 'G' at (row=%d,col=%d), got %q\ngrid:\n%s",
			wantRow, minimapHalfCols, got, strings.Join(lines, "\n"))
	}
}

// TestMinimapGridLines_ClampsFarRemoteToEdge asserts a remote far outside the
// visible grid is clamped to the border rather than silently dropped or
// causing an out-of-range panic.
func TestMinimapGridLines_ClampsFarRemoteToEdge(t *testing.T) {
	players := []PlayerView{{ID: 4, Name: "Far", X: 100000, Y: 0, Z: -100000}}
	lines := minimapGridLines(0, 0, players, defaultMinimapScale)

	topRow := []rune(lines[0])
	if topRow[minimapCols-1] != 'F' {
		t.Errorf("expected far remote clamped to top-right corner, got %q at (0,%d)\ngrid:\n%s",
			topRow[minimapCols-1], minimapCols-1, strings.Join(lines, "\n"))
	}
}

// TestMinimapGlyph covers the first-rune-uppercased / bullet-fallback rule.
func TestMinimapGlyph(t *testing.T) {
	cases := []struct {
		name string
		want rune
	}{
		{"zelda", 'Z'},
		{"Ganon", 'G'},
		{"", '•'},
	}
	for _, c := range cases {
		if got := minimapGlyph(c.name); got != c.want {
			t.Errorf("minimapGlyph(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestRenderMinimap_NoPositionYet asserts the graceful !ok fallback text
// renders instead of a grid when LocalPos hasn't resolved.
func TestRenderMinimap_NoPositionYet(t *testing.T) {
	out := plain(renderMinimap(100, 0, 0, false, nil, defaultMinimapScale))
	if !strings.Contains(out, "no position yet") {
		t.Errorf("renderMinimap with selfOK=false missing the fallback text:\n%s", out)
	}
	if strings.ContainsRune(out, minimapSelf) {
		t.Errorf("renderMinimap with selfOK=false should not draw the grid/self glyph:\n%s", out)
	}
}

// TestRenderMinimap_Title asserts the panel is present and titled, and draws
// the grid (self glyph) when a position IS known.
func TestRenderMinimap_Title(t *testing.T) {
	out := plain(renderMinimap(100, 10, 10, true, nil, defaultMinimapScale))
	if !strings.Contains(out, "Minimap") {
		t.Errorf("renderMinimap missing panel title:\n%s", out)
	}
	if !strings.ContainsRune(out, minimapSelf) {
		t.Errorf("renderMinimap with selfOK=true should draw the self glyph:\n%s", out)
	}
}

// TestMinimapScaleFromEnv covers WW_MINIMAP_SCALE: unset -> default, valid
// override respected, invalid/non-positive value falls back to default
// rather than producing a degenerate (zero/negative) scale.
func TestMinimapScaleFromEnv(t *testing.T) {
	const key = "WW_MINIMAP_SCALE"
	orig, hadOrig := os.LookupEnv(key)
	defer func() {
		if hadOrig {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	}()

	os.Unsetenv(key)
	if got := minimapScaleFromEnv(); got != defaultMinimapScale {
		t.Errorf("minimapScaleFromEnv() unset = %v, want default %v", got, defaultMinimapScale)
	}

	os.Setenv(key, "25")
	if got := minimapScaleFromEnv(); got != 25 {
		t.Errorf("minimapScaleFromEnv() = %v, want 25", got)
	}

	os.Setenv(key, "not-a-number")
	if got := minimapScaleFromEnv(); got != defaultMinimapScale {
		t.Errorf("minimapScaleFromEnv() invalid = %v, want default %v", got, defaultMinimapScale)
	}

	os.Setenv(key, "-5")
	if got := minimapScaleFromEnv(); got != defaultMinimapScale {
		t.Errorf("minimapScaleFromEnv() negative = %v, want default %v", got, defaultMinimapScale)
	}
}
