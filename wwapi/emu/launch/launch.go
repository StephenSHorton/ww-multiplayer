package launch

import (
	"fmt"
	"strings"
)

// LaunchArgs are the Dolphin arguments Launch passes: the user directory
// (-u), the disc to boot (-e), and, when saveState is non-empty,
// --save_state=<path> so the game boots straight into that state,
// bypassing the title screen and save-select menus.
func LaunchArgs(userDir, iso, saveState string) []string {
	args := []string{"-u", userDir, "-e", iso}
	if saveState != "" {
		args = append(args, "--save_state="+saveState)
	}
	return args
}

// Launch starts Dolphin with the given user dir and auto-boot ISO as a
// backgrounded process whose lifetime is independent of ours, and returns
// its PID. The caller returns at once; Dolphin keeps running after we
// exit. Its stdout and stderr go to the null device so its spam doesn't
// clutter our terminal. On macOS it goes through LaunchServices
// (launch_darwin.go).
func Launch(exe, userDir, iso, saveState string) (int, error) {
	return launch(exe, LaunchArgs(userDir, iso, saveState))
}

// AppBundle returns the .app bundle of a macOS Dolphin executable path
// ("<bundle>/Contents/MacOS/Dolphin"), as `open -a` wants it.
func AppBundle(exe string) (string, error) {
	appPath := strings.TrimSuffix(exe, "/Contents/MacOS/Dolphin")
	if appPath == exe {
		return "", fmt.Errorf("DOLPHIN_EXE %q is not an app-bundle binary; expected path ending in .app/Contents/MacOS/Dolphin", exe)
	}
	return appPath, nil
}

// newPID returns the first PID of cur that is not in before.
func newPID(before, cur []uint32) (uint32, bool) {
	seen := make(map[uint32]bool, len(before))
	for _, p := range before {
		seen[p] = true
	}
	for _, p := range cur {
		if !seen[p] {
			return p, true
		}
	}
	return 0, false
}
