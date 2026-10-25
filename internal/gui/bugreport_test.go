package gui

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveBugReport: the zip holds about.txt, the session logs and this
// player's Dolphin log, redacted, and its folder is the one OpenFolder
// "bugreports" opens; the path the window shows has ~ for home.
func TestSaveBugReport(t *testing.T) {
	b, _, _ := newTestBridge(t)
	home := t.TempDir()
	logs := filepath.Join(home, "cfg", "logs")
	reports := filepath.Join(home, "cfg", "bug-reports")
	ud := filepath.Join(home, "Dolphin Emulator")
	b.home = func() string { return home }
	b.bug = bugSeams{
		logDir:    func() (string, error) { return logs, nil },
		reportDir: func() (string, error) { return reports, nil },
	}
	b.mu.Lock()
	b.cfg.Dolphin.UserDir = ud
	b.mu.Unlock()
	for p, text := range map[string]string{
		filepath.Join(logs, "20261025T120000Z-host.log"): "joined 192.168.1.42 from " + home + "\n",
		filepath.Join(ud, "Logs", "dolphin.log"):         "Core: booted\n",
		filepath.Join(ud, "StateSaves", "GZLE01.s01"):    "a state, never read",
	} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	v, err := b.SaveBugReport()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v.Path, "~") || !strings.HasSuffix(v.Path, ".zip") || len(v.Skipped) != 0 {
		t.Errorf("view %+v", v)
	}
	ents, _ := os.ReadDir(reports)
	if len(ents) != 1 {
		t.Fatalf("%d files in the reports folder, want 1", len(ents))
	}
	zr, err := zip.OpenReader(filepath.Join(reports, ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(data)
	}
	if !strings.Contains(got["about.txt"], "ww-multiplayer v9.9.9") || !strings.Contains(got["about.txt"], "user dir:        ~") ||
		strings.Contains(got["about.txt"], "log lines") {
		t.Errorf("about.txt:\n%s", got["about.txt"])
	}
	if got["logs/20261025T120000Z-host.log"] != "joined •••.•••.•••.42 from ~\n" {
		t.Errorf("session log %q", got["logs/20261025T120000Z-host.log"])
	}
	if got["dolphin/dolphin.log"] != "Core: booted\n" {
		t.Errorf("dolphin.log %q", got["dolphin/dolphin.log"])
	}
	for n := range got {
		if strings.Contains(n, "StateSaves") || strings.Contains(n, ".s01") {
			t.Errorf("the zip holds %s", n)
		}
	}

	var opened string
	b.openPath = func(dir string) error { opened = dir; return nil }
	if err := b.OpenFolder("bugreports"); err != nil || opened != reports {
		t.Errorf("OpenFolder(bugreports) opened %q, %v; want %s", opened, err, reports)
	}
}
