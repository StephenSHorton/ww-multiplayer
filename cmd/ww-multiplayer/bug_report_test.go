package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

func TestParseBugReportArgs(t *testing.T) {
	a, err := parseBugReportArgs([]string{"--include", "a.txt", "--include=b.log", "--user-dir", "D1", "--out=R"})
	if err != nil || !slices.Equal(a.include, []string{"a.txt", "b.log"}) || !slices.Equal(a.userDirs, []string{"D1"}) || a.out != "R" {
		t.Errorf("got %+v, %v", a, err)
	}
	for _, bad := range [][]string{{"--include"}, {"--out="}, {"--out", "a", "--out", "b"}, {"stray"}, {"--zip"}} {
		if _, err := parseBugReportArgs(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// TestCmdBugReport: the CLI's report reads the app's log folder and the
// Dolphin folder USER_DIR_1 names (the CLI reads no saved settings), and
// writes to --out.
func TestCmdBugReport(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("APPDATA", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	ud := filepath.Join(t.TempDir(), "Dolphin Emulator")
	t.Setenv("USER_DIR_1", ud)
	t.Setenv("USER_DIR_2", filepath.Join(t.TempDir(), "none"))
	logs, err := applog.DefaultDir()
	if err != nil {
		t.Skip(err)
	}
	for p, text := range map[string]string{
		filepath.Join(logs, "20261025T120000Z-join.log"): "dialed 26.1.2.3:25565\n",
		filepath.Join(ud, "Logs", "dolphin.log"):         "Core: ok\n",
	} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	out := filepath.Join(t.TempDir(), "reports")
	cmdBugReport("bug-report", []string{"--out", out})
	ents, err := os.ReadDir(out)
	if err != nil || len(ents) != 1 || !strings.HasSuffix(ents[0].Name(), ".zip") {
		t.Fatalf("reports folder: %v, %v", ents, err)
	}
}

// TestBugReportDolphin: with no --user-dir the CLI reads the user folder
// the GUI would (launch.UserDir): a portable Dolphin's own User folder,
// found running or by DOLPHIN_EXE, not only the default USER_DIR_1.
func TestBugReportDolphin(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	portable := filepath.Join(t.TempDir(), "Dolphin-portable", "Dolphin.exe")
	files := map[string]bool{portable: true, filepath.Join(filepath.Dir(portable), "portable.txt"): true}
	env := func(vars map[string]string, running ...string) launch.Env {
		return launch.Env{
			GOOS: "windows", Home: t.TempDir(), ConfigDir: cfg,
			Getenv:  func(k string) string { return vars[k] },
			Exists:  func(p string) bool { return files[p] },
			Running: func() []string { return running },
		}
	}
	def1, def2 := launch.DefaultUserDirs("windows", cfg)
	defaults := launch.Paths{Exe: `C:\nowhere\Dolphin.exe`, UserDir1: def1, UserDir2: def2}
	want := filepath.Join(filepath.Dir(portable), "User")

	exe, dirs := bugReportDolphin(env(nil, portable), defaults)
	if exe != portable || !slices.Equal(dirs, []string{want, def2}) {
		t.Errorf("running portable: %q %q, want %q", exe, dirs, want)
	}
	p := defaults
	p.Exe = portable
	if exe, dirs = bugReportDolphin(env(map[string]string{launch.EnvExe: portable}), p); exe != portable || dirs[0] != want {
		t.Errorf("DOLPHIN_EXE portable: %q %q, want %q", exe, dirs, want)
	}
	// An installed (not portable) Dolphin plays on the usual folder.
	if _, dirs = bugReportDolphin(env(nil), defaults); !slices.Equal(dirs, []string{def1, def2}) {
		t.Errorf("no portable: %q", dirs)
	}
	// USER_DIR_1 wins, as it does in the GUI.
	p = defaults
	p.UserDir1 = `D:\mine`
	if _, dirs = bugReportDolphin(env(map[string]string{launch.EnvUserDir1: `D:\mine`}, portable), p); dirs[0] != `D:\mine` {
		t.Errorf("USER_DIR_1: %q", dirs)
	}
}
