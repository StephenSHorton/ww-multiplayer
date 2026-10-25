package bugreport

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, text string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readZip(t *testing.T, p string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// TestWrite: the newest session logs, Dolphin's log and the extra files go
// in redacted (home, tokens, IPs, join codes); saves, discs and binaries
// stay out and are listed; nothing names the user.
func TestWrite(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "Users", "stephen")
	logs := filepath.Join(root, "logs")
	for i := 0; i < SessionLogs+2; i++ {
		write(t, filepath.Join(logs, fmt.Sprintf("20261025T1%d0000Z-host.log", i)), fmt.Sprintf("log %d\n", i))
	}
	newest := fmt.Sprintf("20261025T1%d0000Z-host.log", SessionLogs+1)
	write(t, filepath.Join(logs, newest),
		"2026-10-25T12:00:00.000Z NET  joined 192.168.1.42:25565 with code WW-30AG-09AX token=abc123\n"+
			"2026-10-25T12:00:01.000Z INFO world at "+filepath.Join(home, "AppData", "x")+"\r\n")
	write(t, filepath.Join(logs, "notes.txt"), "not a log")
	ud := filepath.Join(root, "Dolphin Emulator")
	write(t, filepath.Join(ud, "Logs", "dolphin.log"), "Core: booted from 10.0.0.7\n")
	extra := write(t, filepath.Join(root, "mp-local.out"), "server on [fd00::1]:25565\n")
	sav := write(t, filepath.Join(root, "start.sav"), "anything")
	disc := write(t, filepath.Join(root, "renamed.log"), "GZLE01\x00\x00 a disc")
	bin := write(t, filepath.Join(root, "core.txt"), "text\x00binary")

	res, err := Write(filepath.Join(root, "reports"), Options{
		LogDir:          logs,
		DolphinUserDirs: []string{ud, ud, filepath.Join(root, "no-such-dir")},
		Extra:           []string{extra, sav, disc, bin, filepath.Join(root, "gone.log")},
		About:           "WW Multiplayer diagnostics\nuser dir: " + filepath.Join(home, "Dolphin") + "\n",
		Home:            home,
		Now:             func() time.Time { return t0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "reports", "ww-multiplayer-bug-report-20261025T120000Z.zip"); res.Path != want {
		t.Errorf("path %s, want %s", res.Path, want)
	}
	z := readZip(t, res.Path)
	var names []string
	for n := range z {
		names = append(names, n)
	}
	wantIn := []string{"about.txt", "contents.txt", "logs/" + newest, "dolphin/dolphin.log", "extra/mp-local.out"}
	for _, n := range wantIn {
		if _, ok := z[n]; !ok {
			t.Errorf("zip lacks %s (has %v)", n, names)
		}
	}
	logsIn := 0
	for _, n := range names {
		if strings.HasPrefix(n, "logs/") {
			logsIn++
		}
		if strings.Contains(n, ".sav") || strings.Contains(n, "renamed") || strings.Contains(n, "core.txt") || strings.Contains(n, "notes.txt") {
			t.Errorf("zip holds %s", n)
		}
	}
	if logsIn != SessionLogs {
		t.Errorf("%d session logs, want the newest %d", logsIn, SessionLogs)
	}
	if _, ok := z["logs/20261025T100000Z-host.log"]; ok {
		t.Error("the oldest session log went in")
	}
	all := strings.Join(func() []string {
		var s []string
		for _, v := range z {
			s = append(s, v)
		}
		return s
	}(), "\n")
	for _, leak := range []string{"stephen", "192.168.1", "10.0.0", "fd00::1", "30AG", "abc123", "\r"} {
		if strings.Contains(all, leak) {
			t.Errorf("the zip leaks %q", leak)
		}
	}
	for _, kept := range []string{"•••.•••.•••.42:25565", "WW-••••-••••", "token=<redacted>", "~", "booted from •••.•••.•••.7"} {
		if !strings.Contains(all, kept) {
			t.Errorf("the zip lacks %q", kept)
		}
	}
	// A file the CLI named that is gone is said too (its Dolphin and
	// session logs are optional and are not).
	if len(res.Skipped) != 4 {
		t.Errorf("skipped %v, want the save, the disc, the binary and the missing file", res.Skipped)
	}
	for _, s := range []string{"start.sav", "renamed.log", "core.txt", "gone.log"} {
		if !strings.Contains(z["contents.txt"], s) {
			t.Errorf("contents.txt doesn't list %s as left out:\n%s", s, z["contents.txt"])
		}
	}
	if strings.Contains(z["contents.txt"], "no-such-dir") {
		t.Error("a Dolphin folder without a log was listed")
	}
}

// TestWriteTail: a big file keeps its newest MaxFileBytes from a line
// start, and says so.
func TestWriteTail(t *testing.T) {
	root := t.TempDir()
	line := strings.Repeat("x", 99) + "\n"
	big := strings.Repeat(line, MaxFileBytes/len(line)+50) + "LAST LINE\n"
	p := write(t, filepath.Join(root, "big.log"), big)
	res, err := Write(filepath.Join(root, "r"), Options{Extra: []string{p}, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	got := readZip(t, res.Path)["extra/big.log"]
	if !strings.HasPrefix(got, "[the first ") || !strings.HasSuffix(got, "LAST LINE\n") || len(got) > MaxFileBytes+100 {
		t.Errorf("tail: %d bytes, starts %q", len(got), got[:40])
	}
	if strings.Contains(strings.SplitN(got, "\n", 3)[1], "x") && len(strings.SplitN(got, "\n", 3)[1]) != 99 {
		t.Error("the tail starts mid-line")
	}
}

// TestPrune: past Keep, the oldest reports go; a second report in the same
// second gets its own name.
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < Keep+2; i++ {
		now := t0.Add(time.Duration(i) * time.Minute)
		if _, err := Write(dir, Options{Now: func() time.Time { return now }}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Write(dir, Options{Now: func() time.Time { return t0.Add(time.Duration(Keep+1) * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Path, "-2.zip") {
		t.Errorf("same-second report %s", res.Path)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != Keep {
		t.Errorf("%d reports kept, want %d", len(ents), Keep)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), "T120000Z") {
			t.Errorf("the oldest report %s was kept", e.Name())
		}
	}
}
