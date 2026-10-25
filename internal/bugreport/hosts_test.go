package bugreport

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMaskHosts: the addresses a player types or hosts on (DDNS, playit,
// a tailnet, a domain with a port) are masked, port and service kept; a
// bare domain, a file location and an IP's mask are left alone.
func TestMaskHosts(t *testing.T) {
	for in, want := range map[string]string{
		"joining stephen.duckdns.org:25570":             "joining •••.duckdns.org:25570",
		"dial tcp me.duckdns.org: refused":              "dial tcp •••.duckdns.org: refused",
		"host at example-tunnel.ply.gg:41234":           "host at •••.ply.gg:41234",
		"server on desktop-abc.tail1234.ts.net":         "server on •••.ts.net",
		"join MyHouse.Example.COM:25565 ok":             "join •••.com:25565 ok",
		"lookup stephens-pc.local: no such host":        "lookup •••.local: no such host",
		"(stephen.ddns.net:25565)":                      "(•••.ddns.net:25565)",
		"see https://github.com/x/y or dolphin-emu.org": "see https://github.com/x/y or dolphin-emu.org",
		"panic at bug_report.go:74 and main.c:12":       "panic at bug_report.go:74 and main.c:12",
		"joined •••.•••.•••.42:25565":                   "joined •••.•••.•••.42:25565",
		"v0.6.0 at 12:34:56":                            "v0.6.0 at 12:34:56",
		"localhost:25570":                               "localhost:25570",
	} {
		if got := MaskHosts(in); got != want {
			t.Errorf("MaskHosts(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteMasksHosts: a session log's host name and an --include file
// named after one don't reach the zip.
func TestWriteMasksHosts(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	write(t, filepath.Join(logs, "20261025T120000Z-join.log"),
		"NET  joining stephen.duckdns.org:25570\nNET  host on desktop-abc.tail1234.ts.net:25565\n")
	extra := write(t, filepath.Join(root, "stephen.duckdns.org.txt"), "capture\n")
	res, err := Write(filepath.Join(root, "reports"), Options{LogDir: logs, Extra: []string{extra}, Home: root,
		Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	z := readZip(t, res.Path)
	for name, text := range z {
		for _, leak := range []string{"stephen", "tail1234", "desktop-abc"} {
			if strings.Contains(name, leak) || strings.Contains(text, leak) {
				t.Errorf("%s leaks %q:\n%s", name, leak, text)
			}
		}
	}
	if !strings.Contains(z["logs/20261025T120000Z-join.log"], "•••.duckdns.org:25570") {
		t.Errorf("log: %q", z["logs/20261025T120000Z-join.log"])
	}
	if !strings.Contains(z["contents.txt"], "host names") {
		t.Errorf("contents.txt doesn't say host names are masked:\n%s", z["contents.txt"])
	}
}

// TestWriteNoDolphinLog: when no folder had a dolphin.log, contents.txt
// says where it looked (a CLI user on another folder learns of
// --user-dir); one folder with a log is enough to say nothing.
func TestWriteNoDolphinLog(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "A"), filepath.Join(root, "B")
	res, err := Write(filepath.Join(root, "r1"), Options{DolphinUserDirs: []string{a, b, a}, Home: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "dolphin.log: none in ~") || strings.Count(res.Skipped[0], "~") != 2 {
		t.Errorf("skipped %q, want one dolphin.log line naming both folders", res.Skipped)
	}
	write(t, filepath.Join(b, "Logs", "dolphin.log"), "Core: ok\n")
	if res, err = Write(filepath.Join(root, "r2"), Options{DolphinUserDirs: []string{a, b}, Home: root}); err != nil || len(res.Skipped) != 0 {
		t.Errorf("with B's log: %q, %v", res.Skipped, err)
	}
}
