package synctables

import (
	"bytes"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// buildFixture builds the tables of a disc image holding stages, as
// wwapi/tables' own tests do.
func buildFixture(t testing.TB, stages []stagetest.Stage) *tables.Tables {
	t.Helper()
	img := tablestest.ISO(stages)
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	tb, err := tables.Build(d)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

// replace returns stages with the stage named like s swapped for s.
func replace(stages []stagetest.Stage, s stagetest.Stage) []stagetest.Stage {
	out := slices.Clone(stages)
	for i := range out {
		if out[i].Name == s.Name {
			out[i] = s
			return out
		}
	}
	panic("no stage " + s.Name)
}
