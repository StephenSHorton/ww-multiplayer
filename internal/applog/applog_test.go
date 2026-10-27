package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

func clock(t0 time.Time) func() time.Time { return func() time.Time { return t0 } }

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenWritesStampedLines(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 16, 15, 4, 5, 6e6, time.FixedZone("x", 3600))
	f, err := Open(dir, "Host", clock(t0))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "20261016T140405Z-host.log"); f.Path() != want {
		t.Errorf("path %s, want %s", f.Path(), want)
	}
	f.Log(log.Info, "Server listening on :25565")
	f.Log(log.Net, "connected")
	f.Log(log.Err, "two\nlines\n")
	f.Log(log.OK, "ok")
	f.Log(log.Level(99), "odd level")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f.Log(log.Info, "after close") // dropped, no panic
	want := strings.Join([]string{
		"2026-10-16T14:04:05.006Z INFO Server listening on :25565",
		"2026-10-16T14:04:05.006Z NET  connected",
		"2026-10-16T14:04:05.006Z ERR  two",
		"2026-10-16T14:04:05.006Z ERR  lines",
		"2026-10-16T14:04:05.006Z OK   ok",
		"2026-10-16T14:04:05.006Z INFO odd level",
	}, "\n") + "\n"
	if got := read(t, f.Path()); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	// A second session in the same second gets its own file.
	g, err := Open(dir, "../Host!", clock(t0))
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if filepath.Base(g.Path()) != "20261016T140405Z-host-2.log" {
		t.Errorf("second file %s", g.Path())
	}
	if h, _ := Open(dir, "", clock(t0.Add(time.Second))); h == nil || !strings.HasSuffix(h.Path(), "-session.log") {
		t.Errorf("empty role: %v", h)
	} else {
		h.Close()
	}
}

func TestOpenKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < Keep+5; i++ {
		f, err := Open(dir, "join", clock(t0.Add(time.Duration(i)*time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	ents, _ := os.ReadDir(dir)
	var logs []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) != Keep || logs[0] != "20260101T000500Z-join.log" || len(ents) != Keep+1 {
		t.Errorf("%d logs, first %s; %d entries", len(logs), logs[0], len(ents))
	}
}

func TestSizeCap(t *testing.T) {
	saved := maxBytes
	maxBytes = 200
	t.Cleanup(func() { maxBytes = saved })
	f, err := Open(t.TempDir(), "host", clock(time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		f.Log(log.Info, fmt.Sprintf("line %02d", i))
	}
	f.Close()
	got := read(t, f.Path())
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if !strings.Contains(lines[len(lines)-1], "log file reached 200 bytes") || strings.Contains(got, "line 19") {
		t.Errorf("capped file:\n%s", got)
	}
	if tail := f.Tail(); len(tail) != 20 || !strings.HasSuffix(tail[19], "line 19") {
		t.Errorf("the tail keeps every line: %d", len(tail))
	}
}

func TestTailRing(t *testing.T) {
	f, err := Open(t.TempDir(), "host", clock(time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < TailLines+50; i++ {
		f.Log(log.Info, fmt.Sprintf("n%d", i))
	}
	tail := f.Tail()
	if len(tail) != TailLines || !strings.HasSuffix(tail[0], " n50") || !strings.HasSuffix(tail[TailLines-1], fmt.Sprintf(" n%d", TailLines+49)) {
		t.Errorf("tail %d: %q .. %q", len(tail), tail[0], tail[len(tail)-1])
	}
}

func TestConcurrentLog(t *testing.T) {
	f, err := Open(t.TempDir(), "host", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				f.Log(log.Info, "x")
			}
		}()
	}
	wg.Wait()
	f.Close()
	if n := strings.Count(read(t, f.Path()), "\n"); n != 800 {
		t.Errorf("%d lines", n)
	}
}

func TestRedact(t *testing.T) {
	home := "/home/ann"
	if runtime.GOOS == "windows" {
		home = `C:\Users\Ann`
	}
	r := Redactor{Home: home}
	cases := map[string]string{
		"Dolphin: " + filepath.Join(home, "Desktop", "Dolphin.exe"): "Dolphin: " + filepath.Join("~", "Desktop", "Dolphin.exe"),
		"cards " + filepath.ToSlash(home) + "/x":                    "cards ~/x",
		`welcome {"token":"0a1b2c3d4e5f","host":true}`:              `welcome {"token":"<redacted>","host":true}`,
		"reconnect token=deadbeefcafe ok":                           "reconnect token=<redacted> ok",
		"Password: hunter2":                                         "Password: <redacted>",
		"world 3fa9000000000000 seq 4":                              "world 3fa9000000000000 seq 4",
	}
	if runtime.GOOS == "windows" {
		cases[`c:\users\ann\AppData`] = `~\AppData`
		cases[`exec: "C:\\Users\\Ann\\x"`] = `exec: "~\\x"`
	}
	for in, want := range cases {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	if got := (Redactor{}).Redact("/home/ann/x"); got != "/home/ann/x" {
		t.Errorf("no home: %q", got)
	}
}

// TestRedactOwnerKey: a dedicated server's owner key in the forms a log, a
// saved server console or a pasted config carries it (docs/10 §3).
func TestRedactOwnerKey(t *testing.T) {
	const key = "c3cc8c0123456789abcdef0123bdba94"
	cases := map[string]string{
		"  owner key: " + key:                         "  owner key: <redacted>",
		"WW_OWNER_KEY=" + key + " ./ww-multiplayer":   "WW_OWNER_KEY=<redacted> ./ww-multiplayer",
		`{"name":"Ann","ownerKey":"` + key + `"}`:     `{"name":"Ann","ownerKey":"<redacted>"}`,
		"--owner-key=" + key:                          "--owner-key=<redacted>",
		"owner_key: " + key:                           "owner_key: <redacted>",
		"Owner key set: the app player who joins ...": "Owner key set: the app player who joins ...",
		"set WW_OWNER_KEY to keep one":                "set WW_OWNER_KEY to keep one",
	}
	for in, want := range cases {
		if got := (Redactor{}).Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRedactMSYSHome: a Windows home in its MSYS form (Git Bash, MSYS2), on
// any OS: msysPath converts the separators itself.
func TestRedactMSYSHome(t *testing.T) {
	r := Redactor{Home: `C:\Users\Ann`}
	cases := map[string]string{
		"cd /c/Users/Ann/projects/x": "cd ~/projects/x",
		"$ ls /c/Users/Ann":          "$ ls ~",
		"/d/Users/Ann/x":             "/d/Users/Ann/x", // another drive is another path
	}
	for in, want := range cases {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{`C:\Users\Ann`: "/c/Users/Ann", "D:/x": "/d/x", "/home/ann": "", `\\srv\share`: "", "C:": ""} {
		if got := msysPath(in); got != want {
			t.Errorf("msysPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogRedacts(t *testing.T) {
	f, err := Open(t.TempDir(), "host", clock(time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	f.red = Redactor{Home: "/home/ann"}
	f.Log(log.Info, "token=abcdef0123 at /home/ann/x")
	f.Close()
	if got := read(t, f.Path()); !strings.HasSuffix(got, "INFO token=<redacted> at ~/x\n") {
		t.Errorf("file %q", got)
	}
}

func TestDiagnosticsText(t *testing.T) {
	logs := make([]string, TailLines+3)
	for i := range logs {
		logs[i] = fmt.Sprintf("l%d", i)
	}
	d := Diagnostics{
		App: "ww-multiplayer v0.4.0", OS: "windows/amd64", Dolphin: "/home/ann/Dolphin.exe",
		Status: []string{"co-op: armed, world 3fa9 seq 4", "host 192.168.1.42:25565"}, Log: append(logs, "token=s3cret"),
	}
	got := d.Text(Redactor{Home: "/home/ann"})
	for _, want := range []string{
		"WW Multiplayer diagnostics\n", "app:             ww-multiplayer v0.4.0\n", "dolphin:         ~/Dolphin.exe\n",
		"dolphin version: unknown\n", "co-op status:\nco-op: armed, world 3fa9 seq 4\n",
		fmt.Sprintf("last %d log lines:\nl4\n", TailLines), "token=<redacted>\n",
		"host •••.•••.•••.42:25565\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "s3cret") || strings.Contains(got, "192.168") || strings.Contains(got, "\nl3\n") {
		t.Errorf("text:\n%s", got)
	}
	if got := (Diagnostics{}).Text(Redactor{}); strings.Contains(got, "co-op status") || !strings.Contains(got, "last 0 log lines") {
		t.Errorf("empty:\n%s", got)
	}
	// A bug report's about.txt: the log is in the zip as files.
	d.LogFiles = true
	if got := d.Text(Redactor{}); strings.Contains(got, "log lines") || strings.Contains(got, "l4") || !strings.Contains(got, "co-op status") {
		t.Errorf("LogFiles:\n%s", got)
	}
}

func TestDefaultDir(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	d, err := DefaultDir()
	if err != nil {
		t.Skip(err)
	}
	if filepath.Base(d) != "logs" || filepath.Base(filepath.Dir(d)) != "ww-multiplayer" {
		t.Errorf("DefaultDir() = %s", d)
	}
}

// TestDiagnosticsMaskJoinCodes: a join code holds an address, so Copy
// diagnostics masks it like one.
func TestDiagnosticsMaskJoinCodes(t *testing.T) {
	d := Diagnostics{Log: []string{"chat: join me at WW-30AG-09AX or ww460n00jmrz2q"}}
	got := d.Text(Redactor{})
	if strings.Contains(got, "30AG") || strings.Contains(got, "460n") || !strings.Contains(got, "WW-••••-••••") || !strings.Contains(got, "WW-••••-••••-••••") {
		t.Fatalf("diagnostics = %q", got)
	}
}
