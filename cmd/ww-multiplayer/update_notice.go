package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/update"
)

// Updates (owner decision 2026-10-17): the app window has the banner and
// the one-click update (internal/gui); the CLI's host, join and version
// and the TUI only print one line when a newer release is out
// (update.CLINotice: a dev build never checks, WW_NO_UPDATE_CHECK=1 turns
// it off, the answer is cached for update.CheckEvery in the user cache
// directory, never in the app's config, and no network means no line).
// The TUI also follows the app's "Check for updates" switch
// (gui.UpdateCheckEnabled, runTUI). Nothing is downloaded from here.

// noticeTimeout bounds the check behind a notice.
const noticeTimeout = 3 * time.Second

// updateNotice returns the one-line notice, or "".
func updateNotice() string {
	if update.IsDev(version) {
		return ""
	}
	return update.CLINotice(context.Background(), os.Getenv, version, noticeTimeout)
}

// printUpdateNotice prints the notice: now (version), or from the
// background while a session starts (host, join) so the check never
// delays it.
func printUpdateNotice(wait bool) {
	if update.IsDev(version) {
		return
	}
	run := func() {
		if n := updateNotice(); n != "" {
			fmt.Println(n)
		}
	}
	if wait {
		run()
		return
	}
	go run()
}

// cleanupOldExe removes what an earlier one-click update moved aside
// next to this program (<exe>.old on Windows, <bundle>.old on macOS).
// The old process may still be closing when the new one starts, so the
// app faces retry for a few seconds in the background.
func cleanupOldExe(retry bool) {
	if os.Getenv(update.EnvSmoke) == "1" {
		return // an update checking this new version runs: keep the old one
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	update.CleanupOld(update.OS, runtime.GOOS, exe)
	if !retry {
		return
	}
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(time.Second)
			update.CleanupOld(update.OS, runtime.GOOS, exe)
		}
	}()
}
