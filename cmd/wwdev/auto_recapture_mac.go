package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// runAutoRecaptureMac is auto-recapture on macOS (wind#3, the Mac rig).
// A save state made by another Dolphin revision (the PC's 2609a) is
// ignored by the Mac's build, so each Mac rig makes its own start.sav:
// cold boot with the 48 MB MEM1 override (launch.LaunchTest), drive the
// menus through the pad override (driveMenusToInGame, the same as
// Windows), then press Shift+F1 through System Events.
//
// Unlike Windows it never kills other Dolphins: the Mac is shared by many
// agents, each with its own Dolphins. It refuses when a Dolphin already
// runs on USER_DIR_1, and it only ever ends the Dolphin it launched (by
// PID). Nobody is awake to press keys, so when the synthetic Shift+F1
// doesn't land it fails (exit 1) instead of waiting for a person.
//
// System Events needs the Accessibility permission for the app that runs
// wwdev (Terminal, or the agent's host): System Settings > Privacy &
// Security > Accessibility.
func runAutoRecaptureMac(outPath string) {
	rep := log.Stdout{}

	paths := launch.FromEnv()
	exe, iso, userDir := paths.Exe, paths.ISO, paths.UserDir1
	for _, c := range []struct{ what, path, env string }{
		{"Dolphin", exe, "DOLPHIN_EXE"},
		{"Patched ISO", iso, "ISO_PATH"},
		{"Dolphin user dir", userDir, "USER_DIR_1"},
	} {
		if _, err := os.Stat(c.path); err != nil {
			log.Logf(rep, log.Err, "%s not found at %s (set %s)", c.what, c.path, c.env)
			os.Exit(1)
		}
	}
	if pid, ok := dolphinOnUserDir(userDir); ok {
		log.Logf(rep, log.Err, "a Dolphin (pid %d) already runs on %s; close it first (auto-recapture never kills other Dolphins on macOS)", pid, userDir)
		os.Exit(1)
	}
	saveFile := filepath.Join(userDir, "StateSaves", "GZLE01.s01")

	log.Logf(rep, log.Info, "auto-recapture (macOS) starting")
	log.Logf(rep, log.Info, "  Dolphin: %s", exe)
	log.Logf(rep, log.Info, "  ISO:     %s", iso)
	log.Logf(rep, log.Info, "  UserDir: %s", userDir)

	pid, err := launchRecaptureDolphin(rep, exe, userDir, iso)
	if err != nil {
		log.Logf(rep, log.Err, "launch Dolphin: %v", err)
		os.Exit(1)
	}
	log.Logf(rep, log.OK, "launched Dolphin (pid %d)", pid)
	code := 0
	defer func() {
		_ = killProcess(uint32(pid))
		os.Exit(code)
	}()
	fail := func(format string, a ...any) {
		log.Logf(rep, log.Err, format, a...)
		code = 1
	}

	if err := waitForModBlob(uint32(pid), 30*time.Second); err != nil {
		fail("mod blob never came live: %v (is the MEM1 override reaching Dolphin? its log should say \"Memory 48 MB\")", err)
		return
	}
	rep.Log(log.OK, "mod blob live (main01_init fired)")
	time.Sleep(3 * time.Second)

	if err := driveMenusToInGame(uint32(pid), rep); err != nil {
		fail("menu navigation: %v", err)
		return
	}
	rep.Log(log.OK, "in game")

	prev := safeMtime(saveFile)
	for try := 1; try <= 3; try++ {
		log.Logf(rep, log.Info, "Shift+F1 through System Events (try %d)", try)
		if err := macShiftF1(pid); err != nil {
			fail("System Events: %v (grant Accessibility to the app running wwdev: System Settings > Privacy & Security > Accessibility)", err)
			return
		}
		if waitForSaveFileUpdate(saveFile, prev, 5*time.Second) {
			break
		}
	}
	if !waitForSaveFileUpdate(saveFile, prev, 2*time.Second) {
		fail("no save state written to %s: Dolphin ignored the synthetic Shift+F1", saveFile)
		return
	}
	// Dolphin writes the state in the background; let the size settle.
	waitFileSettled(saveFile, 10*time.Second)
	rep.Log(log.OK, "save state written")

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(outPath), err)
		return
	}
	if err := launch.CopyFile(saveFile, outPath); err != nil {
		fail("copy save: %v", err)
		return
	}
	log.Logf(rep, log.OK, "start save refreshed: %s", outPath)
}

// macShiftF1 brings the Dolphin with this PID to the front and holds
// Shift+F1, Dolphin's "save state slot 1" hotkey, for 300 ms. Dolphin's
// Quartz keyboard polls the HID key state once a frame
// (CGEventSourceKeyState), so System Events' `key code`, which presses and
// releases at once, is missed; the keys are posted as CGEvents through
// JXA and held instead. The user dir's hotkeys must be on the keyboard:
// Config/Hotkeys.ini `Device = Quartz/0/Keyboard & Mouse` with
// `Save State/Save State Slot 1 = @(Shift+F1)` (docs/mac-rig.md).
func macShiftF1(pid int) error {
	front := fmt.Sprintf(`tell application "System Events" to set frontmost of (first process whose unix id is %d) to true`, pid)
	if out, err := exec.Command("/usr/bin/osascript", "-e", front).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	if out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", macShiftF1JXA).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// macShiftF1JXA holds Shift (key code 56) and F1 (122) through
// CGEventPost at the HID tap.
const macShiftF1JXA = `ObjC.import('CoreGraphics');
function key(code, down, flags) { var e = $.CGEventCreateKeyboardEvent(null, code, down); $.CGEventSetFlags(e, flags); $.CGEventPost($.kCGHIDEventTap, e); }
var SH = 0x20000;
key(56, true, SH); delay(0.05); key(122, true, SH); delay(0.3); key(122, false, SH); delay(0.05); key(56, false, 0);`

// dolphinOnUserDir reports a running Dolphin whose command line names
// userDir (-u <userDir>).
func dolphinOnUserDir(userDir string) (int, bool) {
	pids, err := emu.ListPIDs()
	if err != nil {
		return 0, false
	}
	for _, p := range pids {
		out, err := exec.Command("/bin/ps", "-o", "args=", "-p", fmt.Sprint(p)).Output()
		if err == nil && containsArg(string(out), userDir) {
			return int(p), true
		}
	}
	return 0, false
}

func containsArg(cmdline, v string) bool {
	cmdline = strings.TrimSpace(cmdline)
	return v != "" && (strings.Contains(cmdline, " "+v+" ") || strings.HasSuffix(cmdline, " "+v))
}

// waitFileSettled waits until path's size stops changing for a second.
func waitFileSettled(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	var last int64 = -1
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil {
			if fi.Size() == last && fi.Size() > 0 {
				return
			}
			last = fi.Size()
		}
		time.Sleep(time.Second)
	}
}
