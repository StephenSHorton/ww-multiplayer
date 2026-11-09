package main

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/mods/widescreen"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// mpLocalWidescreen runs the app runner's widescreen loop
// (widescreen.RunDolphin) on each Dolphin while ctx lasts, when
// WW_WIDESCREEN is set (native-widescreen.md section 8, "Setting the
// mode"). It is then the only ops writer on those games; a failure ends
// only its own loop. Dolphins not on this version's patch are skipped.
func mpLocalWidescreen(ctx context.Context, rep log.Reporter, wg *sync.WaitGroup, pids ...uint32) {
	setting := strings.TrimSpace(os.Getenv("WW_WIDESCREEN"))
	if setting == "" {
		return
	}
	if _, err := launch.ParseWidescreenSetting(setting); err != nil {
		log.Logf(rep, log.Err, "WW_WIDESCREEN: %v (no widescreen loop)", err)
		return
	}
	for i, pid := range pids {
		r := log.Prefix(rep, "[ws-"+string(rune('A'+i))+"]")
		if why := contractRefused(pid); why != "" {
			log.Logf(r, log.Warn, "no widescreen loop on pid %d: %s", pid, why)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := widescreen.RunDolphin(ctx, pid, setting, r); err != nil {
				log.Logf(r, log.Err, "%v", err)
			}
		}()
	}
}
