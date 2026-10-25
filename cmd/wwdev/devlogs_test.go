package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/blobstamp"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDevLogsRoot(t *testing.T) {
	for v, want := range map[string]string{"": "logs", "off": "", "OFF": "", "0": "", " /tmp/x ": "/tmp/x"} {
		if got := devLogsRoot(env(map[string]string{envDevLogs: v})); got != want {
			t.Errorf("%s=%q: %q, want %q", envDevLogs, v, got, want)
		}
	}
}

func TestSaveLaunchRecords(t *testing.T) {
	root := t.TempDir()
	if err := saveLaunchRecords(root, []uint32{10, 11}, []launchRecord{{Label: "Dolphin 1", PID: 10}, {Label: "Dolphin 2", PID: 11}}); err != nil {
		t.Fatal(err)
	}
	// Dolphin 2 quit and dolphin2 launched a new one; 10 keeps running.
	if err := saveLaunchRecords(root, []uint32{10, 12}, []launchRecord{{Label: "Dolphin 2", PID: 12, UserDir: "ud2"}}); err != nil {
		t.Fatal(err)
	}
	got := readLaunchRecords(root)
	var pids []int
	for _, r := range got {
		pids = append(pids, r.PID)
	}
	if !slices.Equal(pids, []int{10, 12}) || got[1].UserDir != "ud2" {
		t.Errorf("records %+v", got)
	}
	// A re-used PID is replaced, not doubled.
	if err := saveLaunchRecords(root, []uint32{10, 12}, []launchRecord{{Label: "again", PID: 10}}); err != nil {
		t.Fatal(err)
	}
	got = readLaunchRecords(root)
	if len(got) != 2 || got[1].Label != "again" {
		t.Errorf("records after re-use %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, launchRecordFile), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := readLaunchRecords(root); r != nil {
		t.Errorf("broken file read as %+v", r)
	}
}

func TestRotateLatest(t *testing.T) {
	root := t.TempDir()
	latest, archived, err := rotateLatest(root)
	if err != nil || archived != "" || latest != filepath.Join(root, "latest") {
		t.Fatalf("first run: %q %q %v", latest, archived, err)
	}
	started := time.Date(2026, 10, 25, 14, 3, 9, 0, time.Local)
	m := sessionManifest{Started: started}
	if err := m.write(latest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(latest, "mp-local.log"), []byte("run 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, archived, err = rotateLatest(root)
	want := filepath.Join(root, "sessions", "20261025-140309")
	if err != nil || archived != want {
		t.Fatalf("second run archived to %q (%v), want %q", archived, err, want)
	}
	if b, _ := os.ReadFile(filepath.Join(want, "mp-local.log")); string(b) != "run 1\n" {
		t.Errorf("archived log %q", b)
	}
	if ents, _ := os.ReadDir(latest); len(ents) != 0 {
		t.Errorf("new latest not empty: %v", ents)
	}
	// Same start again: a suffix, nothing overwritten.
	if err := m.write(latest); err != nil {
		t.Fatal(err)
	}
	if _, archived, err = rotateLatest(root); err != nil || archived != want+"-2" {
		t.Errorf("collision archived to %q (%v)", archived, err)
	}
	// No manifest: the directory's time names it.
	mt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	if err := os.Chtimes(latest, mt, mt); err != nil {
		t.Fatal(err)
	}
	if _, archived, err = rotateLatest(root); err != nil || archived != filepath.Join(root, "sessions", "20260102-030405") {
		t.Errorf("no manifest archived to %q (%v)", archived, err)
	}
}

func TestReadBuildInfo(t *testing.T) {
	b := readBuildInfo(nil)
	if b.Revision != "unknown" || b.Modified != "unknown" || b.BlobHash != blobstamp.BlobHash() || b.RelHash != blobstamp.RelHash() {
		t.Errorf("nil build info: %+v", b)
	}
	bi := &debug.BuildInfo{Main: debug.Module{Version: "v0.5.1-0.20261025-abc+dirty"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "27a4508abc"}, {Key: "vcs.modified", Value: "true"}, {Key: "vcs.time", Value: "2026-10-25T10:00:00Z"},
	}}
	b = readBuildInfo(bi)
	if b.Module != bi.Main.Version || b.Revision != "27a4508abc" || b.Modified != "true" || b.VCSTime != "2026-10-25T10:00:00Z" {
		t.Errorf("build info: %+v", b)
	}
}

func TestSessionEnv(t *testing.T) {
	got := sessionEnv([]string{"WW_ENEMY_SYNC=0", "MP_LOCAL_PIDS=1,2", "SAVE_STATE=s.sav", "USER_DIR_2=ud", "PATH=/bin", "HOME=/h", "ISO_PATH=x.iso", "junk"})
	want := map[string]string{"WW_ENEMY_SYNC": "0", "MP_LOCAL_PIDS": "1,2", "SAVE_STATE": "s.sav", "USER_DIR_2": "ud", "ISO_PATH": "x.iso"}
	if len(got) != len(want) {
		t.Fatalf("sessionEnv = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestFileReporter(t *testing.T) {
	var b bytes.Buffer
	r := &fileReporter{w: &b, now: func() time.Time { return time.Date(2026, 1, 1, 9, 8, 7, 6e6, time.UTC) }}
	r.Log(log.Info, "hi")
	r.Log(log.Err, "bad")
	if got, want := b.String(), "09:08:07.006 hi\n09:08:07.006 ERROR: bad\n"; got != want {
		t.Errorf("file log %q, want %q", got, want)
	}
}

// lineSink collects tailFile's lines.
type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *lineSink) add(l string) { s.mu.Lock(); s.lines = append(s.lines, l); s.mu.Unlock() }
func (s *lineSink) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

func waitLines(t *testing.T, s *lineSink, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Equal(s.get(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("lines %q, want %q", s.get(), want)
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestTailFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dolphin.log")
	appendFile(t, path, "before the run\n")
	offset := fileSize(path)
	ctx, cancel := context.WithCancel(context.Background())
	var sink lineSink
	done := make(chan struct{})
	go func() { tailFile(ctx, path, offset, 2*time.Millisecond, sink.add); close(done) }()

	appendFile(t, path, "one\r\ntw")
	waitLines(t, &sink, []string{"one"})
	appendFile(t, path, "o\nthree\n")
	waitLines(t, &sink, []string{"one", "two", "three"})
	// Truncated (a fresh log): read again from the start.
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitLines(t, &sink, []string{"one", "two", "three", "x"})
	// A last line without a newline is handed over at the end.
	appendFile(t, path, "tail end")
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if got := sink.get(); got[len(got)-1] != "tail end" {
		t.Errorf("lines %q: the unterminated last line is missing", got)
	}
}

func TestTailFileWaitsForMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Logs", "dolphin.log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sink lineSink
	go tailFile(ctx, path, 0, 2*time.Millisecond, sink.add)
	time.Sleep(10 * time.Millisecond)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "booted\n")
	waitLines(t, &sink, []string{"booted"})
}

func TestResolveDolphins(t *testing.T) {
	recs := []launchRecord{
		{PID: 7, UserDir: "ud1", SaveState: "a.sav", DevLog: true, LogPath: "ud1/Logs/dolphin.log", LogOffset: 99},
		{PID: 8, UserDir: "ud2", SaveState: "b.sav", DevLog: false, LogPath: "ud2/Logs/dolphin.log"},
	}
	// MP_LOCAL_PIDS made 8 A.
	ds, srcs := resolveDolphins(recs, 8, 7, env(nil), launch.Paths{})
	if ds[0].PID != 8 || ds[0].UserDir != "ud2" || ds[0].SaveState != "b.sav" || !ds[0].FromRecord || ds[1].UserDir != "ud1" {
		t.Errorf("dolphins %+v", ds)
	}
	if len(srcs) != 2 || srcs[0].label != "A" || srcs[0].enabled || srcs[1].label != "B" || !srcs[1].enabled || srcs[1].offset != 99 {
		t.Errorf("sources %+v", srcs)
	}

	// No record: the env's save states, and USER_DIR_1/2's logs that
	// exist, as 1 and 2.
	dir := t.TempDir()
	ud1 := filepath.Join(dir, "ud1")
	if err := os.MkdirAll(filepath.Join(ud1, "Logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	appendFile(t, launch.LogFile(ud1), "old\n")
	ds, srcs = resolveDolphins(nil, 1, 2, env(map[string]string{"SAVE_STATE": "s.sav", "SAVE_STATE_2": "s2.sav"}),
		launch.Paths{UserDir1: ud1, UserDir2: filepath.Join(dir, "ud2")})
	if ds[0].SaveState != "s.sav" || ds[1].SaveState != "s2.sav" || ds[0].FromRecord {
		t.Errorf("dolphins %+v", ds)
	}
	if len(srcs) != 1 || srcs[0].label != "1" || srcs[0].offset != 4 {
		t.Errorf("sources %+v", srcs)
	}
}

func TestSessionManifestJSON(t *testing.T) {
	dir := t.TempDir()
	m := sessionManifest{Started: time.Unix(1, 0), Build: readBuildInfo(nil), Dolphins: []sessionDolphin{{Label: "A", PID: 3}}}
	if err := m.write(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"started"`, `"vcs_revision"`, `"vcs_modified"`, `"blob_hash"`, `"rel_hash"`, `"pid": 3`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("session.json lacks %s:\n%s", k, b)
		}
	}
	var back sessionManifest
	if err := json.Unmarshal(b, &back); err != nil || back.Dolphins[0].PID != 3 {
		t.Errorf("round trip: %+v %v", back, err)
	}
}
