//go:build darwin

package dolphinenv

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
)

// launch launches Dolphin via `open -n -a` instead of a direct exec of
// Contents/MacOS/Dolphin. Direct exec from a Go child silently ends up
// loading the canonical /Applications/Dolphin binary (still
// hardened-runtime, AMFI denies task_for_pid against it) even when we
// point at the user-copy path under ~/Applications. Going through `open`
// drives LaunchServices, which honours the distinct CFBundleIdentifier set
// by setup-mac-dolphin.sh (PrepareMac) and actually launches the
// ad-hoc-resigned binary we want to attach to.
//
// `open` exits immediately after spawning Dolphin and gives us no PID, so
// we snapshot Dolphin PIDs before the call and poll until a new one shows
// up. dolphin2 launches the two Dolphins sequentially, so the
// snapshot/poll pattern is race-free.
func launch(exe string, args []string) (int, error) {
	appPath, err := AppBundle(exe)
	if err != nil {
		return 0, err
	}

	before, _ := dolphin.ListPIDs()

	openArgs := append([]string{"-n", "-a", appPath, "--args"}, args...)
	if err := exec.Command("/usr/bin/open", openArgs...).Run(); err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := dolphin.ListPIDs()
		if p, ok := newPID(before, cur); ok {
			return int(p), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("launched Dolphin via `open` but no new PID appeared within 10s")
}
