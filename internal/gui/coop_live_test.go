package gui

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
)

// TestCoopLiveSwitchesDefault: live switches are on by default since
// v0.6.1. A config that never chose (a new one, or one from before, which
// saved "liveSwitches" only when on) reads as on; the host's off choice is
// saved as liveSwitchesOff and reads back off.
func TestCoopLiveSwitchesDefault(t *testing.T) {
	for _, js := range []string{`{}`, `{"coop":{"lastMode":"new"}}`, `{"coop":{"liveSwitches":true}}`, `{"coop":{"liveSwitches":false}}`} {
		c, _ := config.Parse([]byte(js))
		if r := choiceOf(c.Coop); !r.LiveSwitches {
			t.Errorf("%s: live switches off by default", js)
		}
	}
	c, _ := config.Parse([]byte(`{"coop":{"liveSwitchesOff":true}}`))
	if choiceOf(c.Coop).LiveSwitches {
		t.Error("the saved off choice reads as on")
	}
}
