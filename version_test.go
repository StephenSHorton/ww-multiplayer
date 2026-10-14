package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// setVersionForTest overrides the build-time version string the way
// `-ldflags "-X main.version=..."` does in release.yml / build-mac.sh.
// Assigning to it here also guards that version stays a package-level
// string var: -X silently does nothing to a const or a renamed var.
func setVersionForTest(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestVersionDefaultsToDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("unstamped build: version = %q, want %q", version, "dev")
	}
	if got, want := versionLine(), "ww-multiplayer dev"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
}

func TestVersionLineUsesStampedVersion(t *testing.T) {
	setVersionForTest(t, "v0.3.0-rc1")
	if got, want := versionLine(), "ww-multiplayer v0.3.0-rc1"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
}

func TestPrintHelpHeaderShowsVersion(t *testing.T) {
	setVersionForTest(t, "v9.9.9")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	read := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r) // drain concurrently so a small pipe buffer can't block printHelp
		read <- b
	}()
	stdout := os.Stdout
	os.Stdout = w
	printHelp()
	os.Stdout = stdout
	w.Close()
	out := <-read

	lines := strings.Split(string(out), "\n")
	if got, want := lines[0], "Wind Waker Multiplayer v9.9.9"; got != want {
		t.Fatalf("help header = %q, want %q", got, want)
	}
	if !strings.Contains(string(out), exeCmd+" version") {
		t.Fatalf("help does not list the version subcommand:\n%s", out)
	}
}
