package main

import (
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// runAutoRecapture cold-boots Dolphin from the patched ISO, drives
// title → intro → file select → in-game with scripted input injection,
// then triggers Dolphin's Shift+F1 save-state hotkey via Win32
// PostMessage. The resulting StateSaves/GZLE01.s01 is copied to
// outPath (default saves/start.sav).
//
// This replaces the last human-in-loop step from CLAUDE.md — pressing
// Shift+F1 after a C-blob change. Run this whenever a C change
// (inject/src, inject/include) invalidates saves/start.sav and the auto-loaded state would
// otherwise restore the old blob over the new patched ISO.
//
// Windows-only today: the Shift+F1 send uses PostMessageW (the
// non-Windows screenshot_other.go stub returns an error).
func runAutoRecapture(outPath string) {
	if outPath == "" {
		outPath = filepath.Join("saves", "start.sav")
	}
	rep := log.Stdout{}

	if runtime.GOOS != "windows" {
		log.Logf(rep, log.Err, "auto-recapture requires Windows (Shift+F1 send uses PostMessageW); got %s", runtime.GOOS)
		os.Exit(1)
	}

	// Clean slate — any leftover Dolphins would confuse window
	// enumeration and the file-mtime watch.
	if err := killAllDolphins(); err != nil {
		log.Logf(rep, log.Warn, "kill stragglers: %v", err)
	}

	paths := launch.FromEnv()
	dolphinExe, isoPath, userDir := paths.Exe, paths.ISO, paths.UserDir1

	if _, err := os.Stat(dolphinExe); err != nil {
		log.Logf(rep, log.Err, "Dolphin not found at %s (set DOLPHIN_EXE to override)", dolphinExe)
		os.Exit(1)
	}
	if _, err := os.Stat(isoPath); err != nil {
		log.Logf(rep, log.Err, "Patched ISO not found at %s (set ISO_PATH to override)", isoPath)
		os.Exit(1)
	}
	if _, err := os.Stat(userDir); err != nil {
		log.Logf(rep, log.Err, "Dolphin user dir not found at %s", userDir)
		os.Exit(1)
	}
	saveFile := filepath.Join(userDir, "StateSaves", "GZLE01.s01")

	log.Logf(rep, log.Info, "auto-recapture starting")
	log.Logf(rep, log.Info, "  Dolphin: %s", dolphinExe)
	log.Logf(rep, log.Info, "  ISO:     %s", isoPath)
	log.Logf(rep, log.Info, "  UserDir: %s", userDir)

	pid, err := launch.Launch(dolphinExe, userDir, isoPath, "")
	if err != nil {
		log.Logf(rep, log.Err, "launch Dolphin: %v", err)
		os.Exit(1)
	}
	log.Logf(rep, log.OK, "launched Dolphin (pid %d)", pid)

	// Always kill the Dolphin we spawned on the way out, even on
	// failure — leaving an orphan running with a patched-but-unsaved
	// state would surprise the user.
	defer func() {
		_ = killProcess(uint32(pid))
	}()

	// Step 1: wait for the mod blob to come live. The mailbox at
	// 0x80412F00 starts at zero until main01_init writes the callback
	// pointer (off+0x08); once that fires, multiplayer_update is
	// ticking every frame and our hooks are wired.
	if err := waitForModBlob(uint32(pid), 30*time.Second); err != nil {
		log.Logf(rep, log.Err, "mod blob never came live: %v", err)
		os.Exit(1)
	}
	rep.Log(log.OK, "mod blob live (main01_init fired)")

	// Step 2: the title screen sits idle waiting for Press Start. Give
	// the splash logo a couple seconds to settle so our first Start
	// press isn't dropped by some pre-title-screen splash transition
	// that doesn't accept input yet.
	time.Sleep(3 * time.Second)

	// Step 3: drive menus to in-game.
	if err := driveMenusToInGame(uint32(pid), rep); err != nil {
		log.Logf(rep, log.Err, "menu navigation: %v", err)
		os.Exit(1)
	}

	// Step 4: snapshot screenshot of the spot we're about to save.
	// Helps the user audit where the recapture landed.
	hwnd, err := host.FindWindowByPID(uint32(pid))
	if err != nil {
		log.Logf(rep, log.Err, "find Dolphin window: %v", err)
		os.Exit(1)
	}
	if img, err := host.CaptureWindow(hwnd); err == nil {
		snap := filepath.Join("saves", "auto-recapture-spawn.png")
		if f, err := os.Create(snap); err == nil {
			if err := png.Encode(f, img); err != nil {
				log.Logf(rep, log.Warn, "spawn screenshot encode: %v", err)
			} else {
				log.Logf(rep, log.Info, "spawn screenshot saved to %s", snap)
			}
			f.Close()
		}
	}

	// Step 5: snapshot mtime of the target save file so we can detect
	// the new write deterministically.
	prevMtime := safeMtime(saveFile)

	// Step 6: try every Win32 path we know for Shift+F1, then fall
	// back to asking the user. Modern Dolphin filters synthetic input
	// (LLKHF_INJECTED) for its hotkey handler — verified on Dolphin
	// 2603a with foreground + scan-coded SendInput + PostMessage all
	// silently dropped. We still attempt the auto path in case the
	// user's Dolphin build is friendlier, then prompt if not.
	host.ForegroundWindow(hwnd)
	time.Sleep(200 * time.Millisecond)
	rep.Log(log.Info, "attempting auto Shift+F1 via SendInput + PostMessage")
	_ = host.SendChordToFocusedWindow(host.VKShift, host.VKF1)
	time.Sleep(500 * time.Millisecond)
	_ = host.SendKeyChord(hwnd, host.VKShift, host.VKF1)

	// Step 7: poll for the new save file. Dolphin writes ~36 MB
	// synchronously; if the auto attempt worked we'll see the mtime
	// advance within ~3 s. If not, prompt the user and watch longer.
	if waitForSaveFileUpdate(saveFile, prevMtime, 5*time.Second) {
		rep.Log(log.OK, "auto Shift+F1 worked — save state written")
	} else {
		fmt.Println()
		fmt.Println("─────────────────────────────────────────────────────────────")
		fmt.Println("Auto Shift+F1 was filtered by Dolphin's hotkey handler (modern")
		fmt.Println("Dolphin builds reject synthetic keyboard events for hotkeys).")
		fmt.Println()
		fmt.Printf("PLEASE: click on the Dolphin window (pid %d) and press Shift+F1\n", pid)
		fmt.Println("whenever you're ready. Auto-recapture will detect the save and")
		fmt.Println("copy it. No deadline — Ctrl+C to cancel.")
		fmt.Println("─────────────────────────────────────────────────────────────")
		waitForSaveFileUpdateBlocking(saveFile, prevMtime)
		rep.Log(log.OK, "save state detected — proceeding with copy")
	}

	// Step 8: copy to the output path.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		log.Logf(rep, log.Err, "mkdir %s: %v", filepath.Dir(outPath), err)
		os.Exit(1)
	}
	if err := launch.CopyFile(saveFile, outPath); err != nil {
		log.Logf(rep, log.Err, "copy save: %v", err)
		os.Exit(1)
	}
	log.Logf(rep, log.OK, "saves/start.sav refreshed: %s", outPath)
	rep.Log(log.Info, "next run: SAVE_STATE=$(pwd)/saves/start.sav ./wwdev.exe dolphin2")
}

// killAllDolphins ends every running Dolphin process so window
// enumeration and StateSaves writes don't get tangled with leftovers.
// Best-effort: returns whatever taskkill emitted, but if no Dolphin
// was running the call still succeeds.
func killAllDolphins() error {
	pids, err := emu.ListPIDs()
	if err != nil {
		return nil // none running = nothing to do
	}
	for _, pid := range pids {
		_ = killProcess(pid)
	}
	return nil
}

// killProcess terminates a single PID via taskkill on Windows. We
// already shell out for Dolphin launch; using the same primitive here
// keeps the surface small. On non-Windows this returns an error (and
// runAutoRecapture short-circuits before reaching it anyway).
func killProcess(pid uint32) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("killProcess: not implemented on %s", runtime.GOOS)
	}
	cmd := exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
	return cmd.Run()
}

// waitForModBlob polls the mailbox spawn_trigger counter until it's
// non-zero, meaning multiplayer_update is firing and our hooks are
// installed. Returns an error if the deadline elapses without
// signal.
func waitForModBlob(pid uint32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, err := emu.FindByPID(pid, "GZLE01")
		if err == nil {
			counter, err := d.ReadU32(mailbox.Mailbox) // +0x00 = spawn_trigger
			d.Close()
			if err == nil && counter != 0 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

// driveMenusToInGame sends the canonical "boot to Outset" sequence:
// 9× Start to clear title + intro pages, then spams A every 1.5 s until
// Link's actor pointer appears in RAM. WW's File Selection has *two*
// confirmation steps (pick quest-log slot → Start-button-highlighted
// detail view), so we don't hardcode an A count — we just keep
// pressing A until we're in-game or hit the 30 s ceiling.
func driveMenusToInGame(pid uint32, rep log.Reporter) error {
	d, err := emu.FindByPID(pid, "GZLE01")
	if err != nil {
		return fmt.Errorf("find Dolphin: %w", err)
	}
	defer d.Close()

	rep.Log(log.Info, "skipping title + intro with 9× Start")
	for i := 0; i < 9; i++ {
		if err := player.Press(d, player.Pad{Buttons: 0x1000}, 300*time.Millisecond); err != nil {
			return fmt.Errorf("Start press #%d: %w", i+1, err)
		}
		time.Sleep(700 * time.Millisecond)
	}

	rep.Log(log.Info, "confirming menus with A until Link spawns")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if player.NewLink(d).Ready() {
			// Give the spawn animation a beat to settle so the save
			// state captures Link standing, not mid-fade-in.
			time.Sleep(1500 * time.Millisecond)
			return nil
		}
		if err := player.Press(d, player.Pad{Buttons: 0x0100}, 300*time.Millisecond); err != nil {
			return fmt.Errorf("A press: %w", err)
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return fmt.Errorf("Link actor never appeared within 30s of menu-confirm A presses")
}

// waitForSaveFileUpdate polls the given path until its mtime advances
// past `since`, or `timeout` elapses. Used to detect when Dolphin has
// flushed a new save state to disk.
func waitForSaveFileUpdate(path string, since time.Time, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mt := safeMtime(path)
		if !mt.IsZero() && mt.After(since) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// waitForSaveFileUpdateBlocking polls until the file's mtime advances
// past `since` — forever. No deadline; the caller is expected to
// Ctrl+C to cancel if the user gives up. Used after the auto Shift+F1
// path is known to have failed and we're waiting on a manual press.
func waitForSaveFileUpdateBlocking(path string, since time.Time) {
	for {
		mt := safeMtime(path)
		if !mt.IsZero() && mt.After(since) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// safeMtime returns os.Stat ModTime, or the zero Time if the file
// doesn't exist. Used to detect when Dolphin writes the new save.
func safeMtime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// runAutoRecapturePair cold-boots Dolphin once, drives menus to in-game,
// then prompts for TWO Shift+F1 keystrokes separated by `delay`. The two
// resulting save states are copied to out1 and out2. Used to produce a
// pair of saves at distinct btp animation phases so the two-Dolphin
// mp-local harness can prove face-sync under natural divergence
// (issue #5). delay defaults to 5s if zero.
func runAutoRecapturePair(out1, out2 string, delay time.Duration) {
	if out1 == "" {
		out1 = filepath.Join("saves", "start.sav")
	}
	if out2 == "" {
		out2 = filepath.Join("saves", "start2.sav")
	}
	if delay <= 0 {
		delay = 5 * time.Second
	}
	rep := log.Stdout{}

	if runtime.GOOS != "windows" {
		log.Logf(rep, log.Err, "auto-recapture-pair requires Windows; got %s", runtime.GOOS)
		os.Exit(1)
	}

	if err := killAllDolphins(); err != nil {
		log.Logf(rep, log.Warn, "kill stragglers: %v", err)
	}

	paths := launch.FromEnv()
	dolphinExe, isoPath, userDir := paths.Exe, paths.ISO, paths.UserDir1

	if _, err := os.Stat(dolphinExe); err != nil {
		log.Logf(rep, log.Err, "Dolphin not found at %s (set DOLPHIN_EXE to override)", dolphinExe)
		os.Exit(1)
	}
	if _, err := os.Stat(isoPath); err != nil {
		log.Logf(rep, log.Err, "Patched ISO not found at %s (set ISO_PATH to override)", isoPath)
		os.Exit(1)
	}
	if _, err := os.Stat(userDir); err != nil {
		log.Logf(rep, log.Err, "Dolphin user dir not found at %s", userDir)
		os.Exit(1)
	}
	saveFile := filepath.Join(userDir, "StateSaves", "GZLE01.s01")

	log.Logf(rep, log.Info, "auto-recapture-pair starting (delay between captures: %s)", delay)
	log.Logf(rep, log.Info, "  out1: %s", out1)
	log.Logf(rep, log.Info, "  out2: %s", out2)

	pid, err := launch.Launch(dolphinExe, userDir, isoPath, "")
	if err != nil {
		log.Logf(rep, log.Err, "launch Dolphin: %v", err)
		os.Exit(1)
	}
	log.Logf(rep, log.OK, "launched Dolphin (pid %d)", pid)
	defer func() { _ = killProcess(uint32(pid)) }()

	if err := waitForModBlob(uint32(pid), 30*time.Second); err != nil {
		log.Logf(rep, log.Err, "mod blob never came live: %v", err)
		os.Exit(1)
	}
	rep.Log(log.OK, "mod blob live (main01_init fired)")

	time.Sleep(3 * time.Second)

	if err := driveMenusToInGame(uint32(pid), rep); err != nil {
		log.Logf(rep, log.Err, "menu navigation: %v", err)
		os.Exit(1)
	}

	hwnd, err := host.FindWindowByPID(uint32(pid))
	if err != nil {
		log.Logf(rep, log.Err, "find Dolphin window: %v", err)
		os.Exit(1)
	}

	if err := captureOneShiftF1(rep, hwnd, pid, saveFile, out1, "first"); err != nil {
		os.Exit(1)
	}

	log.Logf(rep, log.Info, "first save captured; letting btp animation advance for %s before second capture", delay)
	time.Sleep(delay)

	if err := captureOneShiftF1(rep, hwnd, pid, saveFile, out2, "second"); err != nil {
		os.Exit(1)
	}

	log.Logf(rep, log.OK, "pair captured: %s and %s", out1, out2)
	log.Logf(rep, log.Info, "next run: SAVE_STATE=$(pwd)/%s SAVE_STATE_2=$(pwd)/%s ./wwdev.exe dolphin2", out1, out2)
}

// captureOneShiftF1 attempts the auto Shift+F1 path, falls back to a
// blocking prompt, then copies the new save state to outPath. Label is
// included in user-facing messages so the prompt distinguishes between
// captures in a multi-capture flow ("first save", "second save").
func captureOneShiftF1(rep log.Reporter, hwnd uintptr, pid int, saveFile, outPath, label string) error {
	prevMtime := safeMtime(saveFile)

	host.ForegroundWindow(hwnd)
	time.Sleep(200 * time.Millisecond)
	log.Logf(rep, log.Info, "attempting auto Shift+F1 for %s save", label)
	_ = host.SendChordToFocusedWindow(host.VKShift, host.VKF1)
	time.Sleep(500 * time.Millisecond)
	_ = host.SendKeyChord(hwnd, host.VKShift, host.VKF1)

	if !waitForSaveFileUpdate(saveFile, prevMtime, 5*time.Second) {
		fmt.Println()
		fmt.Println("─────────────────────────────────────────────────────────────")
		fmt.Printf("PLEASE: click on the Dolphin window (pid %d) and press Shift+F1\n", pid)
		fmt.Printf("for the %s save state. No deadline — Ctrl+C to cancel.\n", label)
		fmt.Println("─────────────────────────────────────────────────────────────")
		waitForSaveFileUpdateBlocking(saveFile, prevMtime)
	}
	log.Logf(rep, log.OK, "%s save state detected", label)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		log.Logf(rep, log.Err, "mkdir %s: %v", filepath.Dir(outPath), err)
		return err
	}
	if err := launch.CopyFile(saveFile, outPath); err != nil {
		log.Logf(rep, log.Err, "copy save: %v", err)
		return err
	}
	log.Logf(rep, log.OK, "%s save copied to %s", label, outPath)
	return nil
}
