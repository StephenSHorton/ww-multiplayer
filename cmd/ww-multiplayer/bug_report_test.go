package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
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
