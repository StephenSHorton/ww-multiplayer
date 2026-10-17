// Package launch finds and prepares the Dolphin the tool plays on: the
// default paths dolphin2 and auto-recapture use (Defaults, FromEnv), the
// detection order of the desktop app's first-run setup (Detect, UserDir),
// launching Dolphin detached (Launch), bootstrapping a second user
// directory (BootstrapUserDir), the bundled Gecko cheats
// (EnsureCheatsIni), and the macOS "prepare a copy without the hardened
// runtime" step of scripts/setup-mac-dolphin.sh (PrepareMac).
//
// The code was moved out of package main (mp_local.go, auto_recapture.go)
// without a change in behaviour (it was internal/dolphinenv before Phase C
// C10); the CLI's dolphin2, mp-local and
// auto-recapture call it. Everything that decides something is a pure
// function of an Env or of its arguments, so it is tested on any OS with
// fakes; what asks the OS (running processes, the registry, `open`) sits
// in files with build tags (docs/01-architecture.md).
//
// FromEnv and Detect still read the environment through os.Getenv by
// default; moving that read into cmd is left to a later Phase C step.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package launch

import (
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the Dolphin, the patched disc and the two Dolphin user
// directories dolphin2 runs (the second only for the second Dolphin).
type Paths struct {
	Exe      string
	ISO      string
	UserDir1 string
	UserDir2 string
}

// The environment variables FromEnv reads. They override the defaults and,
// in the desktop app, its saved settings too.
const (
	EnvExe      = "DOLPHIN_EXE"
	EnvISO      = "ISO_PATH"
	EnvUserDir1 = "USER_DIR_1"
	EnvUserDir2 = "USER_DIR_2"
)

// Defaults returns the per-platform fallback paths for goos, with home the
// user's home directory and cfg os.UserConfigDir. Picked so that a fresh
// checkout on either platform "just works" once the user installs Dolphin
// and patches an ISO into the standard location for their OS. exists says
// whether a path exists (os.Stat): on macOS the prepared copy under
// ~/Applications wins over /Applications.
func Defaults(goos, home, cfg string, exists func(string) bool) Paths {
	var p Paths
	switch goos {
	case "darwin":
		// ~/Applications/Dolphin.app is the user-writable copy created
		// by scripts/setup-mac-dolphin.sh (PrepareMac) — re-signed without
		// the hardened runtime so AMFI permits task_for_pid against it.
		// Falls back to /Applications if the user copy isn't there.
		p.Exe = filepath.Join(home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
		if !exists(p.Exe) {
			p.Exe = "/Applications/Dolphin.app/Contents/MacOS/Dolphin"
		}
		p.ISO = filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso")
	case "windows":
		p.Exe = filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe")
		p.ISO = filepath.Join(home, "Desktop", "Dolphin-x64", "Roms", "WW_Multiplayer_Patched.iso")
	default:
		p.Exe = "dolphin-emu"
		p.ISO = filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso")
	}
	p.UserDir1, p.UserDir2 = DefaultUserDirs(goos, cfg)
	return p
}

// DefaultUserDirs returns Dolphin's default user directory for goos under
// cfg (os.UserConfigDir) and the second one dolphin2 bootstraps next to
// it: "Dolphin Emulator" and "Dolphin Emulator 2" on Windows (%APPDATA%),
// "Dolphin" and "Dolphin 2" on macOS (~/Library/Application Support),
// "dolphin-emu" and "dolphin-emu-2" elsewhere. backup.DefaultUserDirs
// returns the same (a test keeps them together).
func DefaultUserDirs(goos, cfg string) (first, second string) {
	switch goos {
	case "darwin":
		return filepath.Join(cfg, "Dolphin"), filepath.Join(cfg, "Dolphin 2")
	case "windows":
		return filepath.Join(cfg, "Dolphin Emulator"), filepath.Join(cfg, "Dolphin Emulator 2")
	}
	return filepath.Join(cfg, "dolphin-emu"), filepath.Join(cfg, "dolphin-emu-2")
}

// DefaultPaths is Defaults for this machine. A home or config directory
// the OS can't name is "", as it always was.
func DefaultPaths() Paths {
	cfg, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	return Defaults(runtime.GOOS, home, cfg, statExists)
}

// WithEnv returns p with each path whose variable getenv sets to a
// non-empty value replaced by it (DOLPHIN_EXE, ISO_PATH, USER_DIR_1,
// USER_DIR_2).
func (p Paths) WithEnv(getenv func(string) string) Paths {
	for _, o := range []struct {
		key string
		dst *string
	}{{EnvExe, &p.Exe}, {EnvISO, &p.ISO}, {EnvUserDir1, &p.UserDir1}, {EnvUserDir2, &p.UserDir2}} {
		if v := getenv(o.key); v != "" {
			*o.dst = v
		}
	}
	return p
}

// FromEnv is the paths dolphin2 and auto-recapture use: DefaultPaths with
// the environment's overrides.
func FromEnv() Paths { return DefaultPaths().WithEnv(os.Getenv) }

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
