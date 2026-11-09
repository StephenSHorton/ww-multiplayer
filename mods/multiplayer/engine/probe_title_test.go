package engine

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// stageMem answers reads of the current and next stage block.
type stageMem struct {
	name    string
	pending bool
	err     error
}

func (m stageMem) Read(a uint32, n int) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	b := make([]byte, n)
	copy(b, m.name)
	if m.pending {
		b[addr.NextStageEnable-a] = 1
	}
	return b, nil
}

// TestAtTitle: the disc update may close a Dolphin the app launched only
// on a positive read of the title screen (docs/coop-just-works.md D5).
func TestAtTitle(t *testing.T) {
	for _, c := range []struct {
		m    stageMem
		want bool
	}{
		{stageMem{name: addr.StageTitle}, true},
		{stageMem{name: addr.StageTitle, pending: true}, false}, // leaving the title for a save
		{stageMem{name: "sea"}, false},
		{stageMem{name: ""}, false}, // between stages
		{stageMem{err: errors.New("read")}, false},
	} {
		if got := atTitle(c.m); got != c.want {
			t.Errorf("%+v: %v", c.m, got)
		}
	}
}
