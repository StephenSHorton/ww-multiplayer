package launch

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PlayerUserDirs are the user directories a player's everyday Dolphin at
// exe may use, which a dev-log launch must never point at: Dolphin's
// default (DefaultUserDirs' first: %APPDATA%\Dolphin Emulator on
// Windows), on Windows Documents\Dolphin Emulator (the older location
// Dolphin still uses when it exists, also under OneDrive's Documents) and
// the registry's UserConfigPath (e.UserConfigPaths), on Linux
// ~/.dolphin-emu and the XDG data dir's dolphin-emu, and the portable
// profile <exe dir>/User (whether or not portable.txt is there yet).
func PlayerUserDirs(e Env, exe string) []string {
	get := func(k string) string {
		if e.Getenv == nil {
			return ""
		}
		return e.Getenv(k)
	}
	var out []string
	add := func(elem ...string) {
		for _, s := range elem {
			if s == "" {
				return
			}
		}
		out = append(out, filepath.Join(elem...))
	}
	first, _ := DefaultUserDirs(e.GOOS, e.ConfigDir)
	if e.ConfigDir != "" {
		out = append(out, first)
	}
	switch e.GOOS {
	case "windows":
		add(e.Home, "Documents", "Dolphin Emulator")
		add(get("OneDrive"), "Documents", "Dolphin Emulator")
		if e.UserConfigPaths != nil {
			for _, p := range e.UserConfigPaths() {
				add(p)
			}
		}
	case "darwin":
	default:
		add(e.Home, ".dolphin-emu")
		add(e.Home, ".local", "share", "dolphin-emu")
		add(get("XDG_DATA_HOME"), "dolphin-emu")
	}
	if exe != "" && filepath.IsAbs(exe) {
		add(filepath.Dir(exe), "User")
	}
	return out
}

// DevLogUserDirError says why a dev-log launch (WW_DOLPHIN_DEVLOG) of
// the Dolphin at exe must not run on userDir, nil when it may: the user
// dir has to be set explicitly (USER_DIR_1 / USER_DIR_2, envKey) and be
// none of PlayerUserDirs. Dolphin appends to <user dir>/Logs/dolphin.log
// at Info and never truncates it, so a test launch must not grow a
// player's main profile.
func DevLogUserDirError(e Env, exe, userDir, envKey string, explicit bool) error {
	if !explicit {
		return fmt.Errorf("%s=1 needs a test Dolphin user dir: %s is not set, so this launch would use %s; set %s to a test user dir (never your main Dolphin profile), or unset %s",
			EnvDevLog, envKey, userDir, envKey, EnvDevLog)
	}
	k := userDirKey(e.GOOS, userDir)
	for _, p := range PlayerUserDirs(e, exe) {
		if userDirKey(e.GOOS, p) == k {
			return fmt.Errorf("%s=1 refused: %s=%s is a player's Dolphin profile (%s's own default, Documents or portable User dir), and the dev log would grow its Logs/dolphin.log; point %s at a test user dir, or unset %s",
				EnvDevLog, envKey, userDir, filepath.Base(exeOr(exe)), envKey, EnvDevLog)
		}
	}
	return nil
}

func exeOr(exe string) string {
	if exe == "" {
		return "Dolphin"
	}
	return exe
}

// userDirKey is how two user dirs are compared: cleaned, without a
// trailing separator, and without case on Windows and macOS (whose
// default file systems ignore it).
func userDirKey(goos, p string) string {
	p = filepath.Clean(strings.TrimSpace(p))
	if len(p) > 1 {
		p = strings.TrimRight(p, `\/`)
	}
	if goos == "windows" || goos == "darwin" {
		p = strings.ToLower(p)
	}
	if goos == "windows" {
		p = strings.ReplaceAll(p, "/", `\`)
	}
	return p
}
