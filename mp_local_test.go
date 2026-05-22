package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// recordingReporter collects log lines in-memory so tests can assert on
// what ensureCheatsIni emitted to the user.
type recordingReporter struct {
	lines []string
}

func (r *recordingReporter) Log(_ report.Level, msg string) {
	r.lines = append(r.lines, msg)
}

func (r *recordingReporter) joined() string { return strings.Join(r.lines, "\n") }

func TestEnsureCheatsIni_BundledIsValid(t *testing.T) {
	if len(bundledCheatsIni) == 0 {
		t.Fatal("bundledCheatsIni is empty; cheats/GZLE01.ini didn't get embedded")
	}
	for _, want := range []string{"[Gecko]", "$Invincible", "$Moon Jump"} {
		if !bytes.Contains(bundledCheatsIni, []byte(want)) {
			t.Errorf("bundled cheats INI missing %q", want)
		}
	}
}

func TestEnsureCheatsIni_WritesWhenMissing(t *testing.T) {
	userDir := t.TempDir()
	rep := &recordingReporter{}

	ensureCheatsIni(rep, userDir)

	target := filepath.Join(userDir, "GameSettings", "GZLE01.ini")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected %s to exist, got %v", target, err)
	}
	if !bytes.Equal(got, bundledCheatsIni) {
		t.Errorf("file contents diverged from bundled INI (got %d bytes, want %d)", len(got), len(bundledCheatsIni))
	}
	if !strings.Contains(rep.joined(), "installed bundled GZLE01.ini") {
		t.Errorf("expected install log line, got: %s", rep.joined())
	}
}

func TestEnsureCheatsIni_PreservesExisting(t *testing.T) {
	userDir := t.TempDir()
	gsDir := filepath.Join(userDir, "GameSettings")
	if err := os.MkdirAll(gsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(gsDir, "GZLE01.ini")
	existing := []byte("[Core]\nCPUThread = False\n")
	if err := os.WriteFile(target, existing, 0o644); err != nil {
		t.Fatal(err)
	}

	rep := &recordingReporter{}
	ensureCheatsIni(rep, userDir)

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Errorf("ensureCheatsIni clobbered an existing GZLE01.ini; got %q want %q", got, existing)
	}
	if !strings.Contains(rep.joined(), "already exists, leaving it untouched") {
		t.Errorf("expected 'leaving untouched' log line, got: %s", rep.joined())
	}
}
