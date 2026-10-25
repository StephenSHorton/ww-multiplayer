package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rarc is the start of a RARC archive (JKRArchive.h's SArcHeader +
// SArcDataInfo), the shape internal/gamefiles finds anywhere in a file.
func rarc() []byte {
	b := make([]byte, 0x80)
	copy(b, "RARC")
	for off, v := range map[int]uint32{4: 0x80, 8: 0x20, 0xC: 0x40, 0x10: 0x20, 0x20: 1, 0x24: 0x20, 0x28: 2} {
		binary.BigEndian.PutUint32(b[off:], v)
	}
	return b
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func put(t *testing.T, p string, b []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runIn(dir string, args ...string) (int, string) {
	var out, errw bytes.Buffer
	code := run(dir, args, &out, &errw)
	return code, out.String() + errw.String()
}

// TestGitTree: with no path the command checks the tracked files and the
// untracked ones git doesn't ignore (a game file is flagged before it is
// committed), each once; a tracked file deleted from the tree is still
// checked by its name; an ignored file is not looked at. Exit codes: 0
// clean, 1 findings, 2 no git tree.
func TestGitTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	put(t, filepath.Join(dir, "README.md"), []byte("hello\n"))
	put(t, filepath.Join(dir, ".gitignore"), []byte("ignored/\n"))
	put(t, filepath.Join(dir, "ignored", "game.iso"), []byte("not looked at"))
	git(t, dir, "add", "README.md", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "init")

	if code, out := runIn(dir); code != 0 || !strings.Contains(out, "2 file(s) checked") {
		t.Fatalf("clean tree: exit %d\n%s", code, out)
	}

	// Untracked, not ignored: a renamed archive is caught by content.
	put(t, filepath.Join(dir, "assets", "notes.bin"), rarc())
	code, out := runIn(dir)
	if code != 1 || !strings.Contains(out, "assets/notes.bin") {
		t.Errorf("untracked RARC: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "ignored/game.iso") {
		t.Errorf("an ignored file was checked:\n%s", out)
	}

	// Staged and modified: listed by git twice (cached, and as a change),
	// checked once.
	git(t, dir, "add", "assets/notes.bin")
	put(t, filepath.Join(dir, "assets", "notes.bin"), append(rarc(), 1))
	if _, out = runIn(dir); strings.Count(out, "assets/notes.bin") != 1 {
		t.Errorf("not checked exactly once:\n%s", out)
	}

	// A tracked game file deleted from the tree is still flagged by name.
	os.Remove(filepath.Join(dir, "assets", "notes.bin"))
	put(t, filepath.Join(dir, "Roms", "game.iso"), []byte("x"))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "bad")
	os.Remove(filepath.Join(dir, "Roms", "game.iso"))
	if code, out = runIn(dir); code != 1 || !strings.Contains(out, "Roms/game.iso") {
		t.Errorf("deleted tracked .iso: exit %d\n%s", code, out)
	}

	if code, out = runIn(t.TempDir()); code != 2 {
		t.Errorf("not a git tree: exit %d\n%s", code, out)
	}
}

// TestPaths: given paths it checks those (folders walked, zips opened,
// whole files scanned), a missing path is exit 2.
func TestPaths(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ww-multiplayer.exe")
	b := make([]byte, 3<<20)
	copy(b, "MZ")
	put(t, exe, b)
	if code, out := runIn(dir, exe); code != 0 {
		t.Errorf("clean exe: exit %d\n%s", code, out)
	}
	copy(b[0x1ABCD0:], rarc())
	put(t, exe, b)
	if code, out := runIn(dir, dir); code != 1 || !strings.Contains(out, "RARC") {
		t.Errorf("exe with a RARC inside: exit %d\n%s", code, out)
	}
	if code, out := runIn(dir, filepath.Join(dir, "nope")); code != 2 {
		t.Errorf("missing path: exit %d\n%s", code, out)
	}
}
