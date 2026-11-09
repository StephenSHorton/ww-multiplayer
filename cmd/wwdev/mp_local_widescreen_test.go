package main

import (
	"context"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// TestMpLocalWidescreenOff: without WW_WIDESCREEN, or with a bad one, no
// loop starts and no Dolphin is looked at.
func TestMpLocalWidescreenOff(t *testing.T) {
	for _, v := range []string{"", "  ", "bogus"} {
		t.Setenv("WW_WIDESCREEN", v)
		var wg sync.WaitGroup
		mpLocalWidescreen(context.Background(), log.Discard{}, &wg, 0xFFFFFFF0, 0xFFFFFFF1)
		wg.Wait() // returns at once: nothing was added
	}
}
