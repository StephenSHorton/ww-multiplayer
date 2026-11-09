package launch

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LaunchArgs are the Dolphin arguments Launch passes: the user directory
// (-u), the disc to boot (-e), the 48 MB MEM1 override as session-only
// config (-C; the patched disc's heap plan needs it, docs/05 "48 MB
// MEM1"; Dolphin 2609+ would size MEM1 from the disc's bi2.bin anyway,
// older builds need the override, and a vanilla disc in the same session
// trims its arena back to 24 MB on its own), and, when saveState is
// non-empty, --save_state=<path> so the game boots straight into that
// state, bypassing the title screen and save-select menus.
func LaunchArgs(userDir, iso, saveState string) []string {
	args := []string{"-u", userDir, "-e", iso}
	args = append(args, MEM1Override...)
	if saveState != "" {
		args = append(args, "--save_state="+saveState)
	}
	return args
}

// MEM1Override is the Dolphin command line that enables the emulated
// memory size override at 48 MB for this session only (nothing is
// written to the user's Dolphin.ini). Both keys live in Dolphin's
// [Core] section; -C has been accepted since 2017.
var MEM1Override = []string{
	"-C", "Dolphin.Core.RAMOverrideEnable=True",
	"-C", "Dolphin.Core.MEM1Size=0x03000000",
}

// Launch starts Dolphin with the given user dir and auto-boot ISO as a
// backgrounded process whose lifetime is independent of ours, and returns
// its PID. The caller returns at once; Dolphin keeps running after we
// exit. Its stdout and stderr go to the null device so its spam doesn't
// clutter our terminal. On macOS it goes through LaunchServices
// (launch_darwin.go).
func Launch(exe, userDir, iso, saveState string) (int, error) {
	return LaunchWith(exe, userDir, iso, saveState, nil)
}

// LaunchWith is Launch with extra Dolphin arguments after the usual ones
// (the widescreen -C pair, WidescreenArgs).
func LaunchWith(exe, userDir, iso, saveState string, extra []string) (int, error) {
	return launch(exe, append(LaunchArgs(userDir, iso, saveState), extra...))
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

// launchedPID picks, among the Dolphins in cur that were not running
// before a launch, the one started on userDir: its command line names that
// -u folder. Two players starting their Dolphins on one Mac at once both
// see the other's appear (issue #134), so a new PID alone isn't enough.
// With no userDir, or when last is true (the wait ran out) and exactly one
// new Dolphin's command line couldn't be read, that one is taken.
func launchedPID(before []uint32, cur []Process, userDir string, last bool) (uint32, bool) {
	seen := make(map[uint32]bool, len(before))
	for _, p := range before {
		seen[p] = true
	}
	var fresh []Process
	for _, p := range cur {
		if !seen[p.PID] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return 0, false
	}
	if userDir == "" {
		return fresh[0].PID, true
	}
	unknown := 0
	var u uint32
	for _, p := range fresh {
		if !p.ArgsKnown {
			unknown++
			u = p.PID
			continue
		}
		if d, ok := UserDirArg(p.Args); ok && sameDir(d, userDir) {
			return p.PID, true
		}
	}
	if last && unknown == 1 {
		return u, true
	}
	return 0, false
}

// sameDir says a and b name one folder: cleaned, without case (macOS's
// default file system ignores it).
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
