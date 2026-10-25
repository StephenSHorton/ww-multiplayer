package launch

import (
	"path/filepath"
	"strings"
	"testing"
)

// The dev log appends to <user dir>/Logs/dolphin.log, so it must refuse
// every profile a player's Dolphin uses, however the path is spelled, and
// any user dir that was not set explicitly.
func TestDevLogUserDirError(t *testing.T) {
	root := t.TempDir() // absolute on any test host
	home := filepath.Join(root, "Users", "p")
	cfg := filepath.Join(home, "AppData", "Roaming")
	exe := filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe")
	reg := filepath.Join(root, "DolphinUser")
	e := Env{GOOS: "windows", Home: home, ConfigDir: cfg,
		Getenv:          func(k string) string { return map[string]string{"OneDrive": filepath.Join(home, "OneDrive")}[k] },
		UserConfigPaths: func() []string { return []string{reg} }}
	main := filepath.Join(cfg, "Dolphin Emulator")
	for _, tt := range []struct {
		name, dir string
		explicit  bool
		refused   bool
	}{
		{"main profile", main, true, true},
		{"main profile, other case", strings.ToUpper(main), true, true},
		{"main profile, trailing separator", main + string(filepath.Separator), true, true},
		{"main profile, not explicit", main, false, true},
		{"Documents", filepath.Join(home, "Documents", "Dolphin Emulator"), true, true},
		{"OneDrive Documents", filepath.Join(home, "OneDrive", "Documents", "Dolphin Emulator"), true, true},
		{"registry UserConfigPath", reg, true, true},
		{"portable User", filepath.Join(filepath.Dir(exe), "User"), true, true},
		{"default second dir, not explicit", filepath.Join(cfg, "Dolphin Emulator 2"), false, true},
		{"explicit second dir", filepath.Join(cfg, "Dolphin Emulator 2"), true, false},
		{"explicit test dir", filepath.Join(home, "ww-test", "u1"), true, false},
	} {
		err := DevLogUserDirError(e, exe, tt.dir, EnvUserDir1, tt.explicit)
		if (err != nil) != tt.refused {
			t.Errorf("%s (%s): err %v, refused want %v", tt.name, tt.dir, err, tt.refused)
		}
		if err != nil && (!strings.Contains(err.Error(), EnvUserDir1) || !strings.Contains(err.Error(), EnvDevLog)) {
			t.Errorf("%s: message %q does not say which variable to set", tt.name, err)
		}
	}
}

func TestPlayerUserDirsOtherOS(t *testing.T) {
	for _, tt := range []struct {
		goos, home, cfg string
		want            []string
	}{
		{"darwin", "/Users/p", "/Users/p/Library/Application Support", []string{"/Users/p/Library/Application Support/Dolphin"}},
		{"linux", "/home/p", "/home/p/.config", []string{"/home/p/.config/dolphin-emu", "/home/p/.dolphin-emu", "/home/p/.local/share/dolphin-emu"}},
	} {
		e := Env{GOOS: tt.goos, Home: tt.home, ConfigDir: tt.cfg}
		got := PlayerUserDirs(e, "")
		for _, w := range tt.want {
			w = filepath.FromSlash(w)
			if err := DevLogUserDirError(e, "", w, EnvUserDir1, true); err == nil {
				t.Errorf("%s: %s allowed (PlayerUserDirs %q)", tt.goos, w, got)
			}
		}
		if err := DevLogUserDirError(e, "", filepath.FromSlash(tt.home+"/ww-test/u1"), EnvUserDir1, true); err != nil {
			t.Errorf("%s: explicit test dir refused: %v", tt.goos, err)
		}
	}
}
