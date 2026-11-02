package seachart

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/vanillatest"
)

// TestExtractVanilla reads the chart from the user's own disc (skipped
// without one, WW_VANILLA_DISC).
func TestExtractVanilla(t *testing.T) {
	d := vanillatest.Open(t)
	c, err := Extract(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Islands) != GridSide*GridSide {
		t.Errorf("%d islands, want 49", len(c.Islands))
	}
	var outset *Island
	for i := range c.Islands {
		if c.Islands[i].Square == (Square{-2, 3}) {
			outset = &c.Islands[i]
		}
	}
	if outset == nil {
		t.Fatal("no Outset tile")
	}
	// FmapDat.bin's checkpoint for G2: (105, 19713) from the square's centre.
	if outset.CX != -199895 || outset.CZ != 319713 {
		t.Errorf("Outset at (%d, %d)", outset.CX, outset.CZ)
	}
	img, err := png.Decode(bytes.NewReader(outset.PNG))
	if err != nil || img.Bounds().Dx() != 100*upscaleFactor {
		t.Errorf("Outset tile: %v, %v", err, img)
	}
	for stage, want := range map[string]Square{"LinkRM": {-2, 3}, "Opub": {0, -2}, "Atorizk": {2, -2}, "Ekaze": {0, -3}, "tincle": {-1, -1}} {
		if got, ok := c.Stages[stage]; !ok || got != want {
			t.Errorf("%s: %v %v, want %v", stage, got, ok, want)
		}
	}
	if _, ok := c.Stages["M_NewD2"]; ok {
		t.Error("M_NewD2's 2DMA names a square; the table expects none")
	}
}
