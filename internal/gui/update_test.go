package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/internal/update"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// fakeLatest is a release API on 127.0.0.1 answering tag; it counts the
// requests. Tests never reach GitHub.
type fakeLatest struct {
	srv *httptest.Server
	tag atomic.Value
	n   atomic.Int32
	err atomic.Bool
	// gate, when set, holds every answer until it is closed.
	gate atomic.Value // chan struct{}
}

func newFakeLatest(t *testing.T, tag string) *fakeLatest {
	t.Helper()
	f := &fakeLatest{}
	f.tag.Store(tag)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.n.Add(1)
		if g, _ := f.gate.Load().(chan struct{}); g != nil {
			<-g
		}
		if f.err.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		tag := f.tag.Load().(string)
		_ = json.NewEncoder(w).Encode(update.Release{Tag: tag, HTMLURL: "https://github.com/StephenSHorton/ww-multiplayer/releases/tag/" + tag})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// updBridge is a test Bridge at version, its update client on f, and an
// install that records what it was asked instead of touching files.
type fakeInstall struct {
	mu     sync.Mutex
	calls  []string
	err    error
	hold   chan struct{}
	during func() // runs before the install returns (a session starting meanwhile)
}

func (fi *fakeInstall) run(ctx context.Context, _ *update.Client, rel update.Release, goos, exe string, p update.Progress) error {
	if fi.hold != nil {
		select {
		case <-fi.hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p(50, 100)
	fi.mu.Lock()
	fi.calls = append(fi.calls, rel.Tag+" "+goos+" "+exe)
	fi.mu.Unlock()
	if fi.err != nil {
		return fi.err
	}
	if fi.during != nil {
		fi.during()
	}
	p(100, 100)
	return nil
}

func updBridge(t *testing.T, version string, f *fakeLatest, pre func(path string)) (*Bridge, *fakeEngine, string, *fakeInstall) {
	t.Helper()
	e := newFakeEngine()
	path := filepath.Join(t.TempDir(), "config.json")
	if pre != nil {
		pre(path)
	}
	b := NewBridge(Options{Version: version, Controller: session.New(e, session.Options{}), ConfigPath: path,
		Args: []string{"ui", "--dev"}, Getenv: func(k string) string {
			if k == update.EnvURL && f != nil {
				return f.srv.URL + "/latest"
			}
			return ""
		}})
	t.Cleanup(b.close)
	fi := &fakeInstall{}
	b.updSeam.goos = "windows"
	b.updSeam.exe = func() (string, error) { return `C:\games\ww-multiplayer.exe`, nil }
	b.updSeam.install = fi.run
	b.updSeam.start = func(*exec.Cmd) error { return errors.New("a test starts nothing") }
	b.updSeam.alert = func(string, string) {} // no message box in a test
	b.updSeam.restore = func(string) bool { return false }
	b.procs = func() []launch.Process { return nil }
	return b, e, path, fi
}

func waitUpd(t *testing.T, b *Bridge, stage string) UpdateView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v := b.UpdateStatus(); v.Stage == stage {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the update never reached %q: %+v", stage, b.UpdateStatus())
	return UpdateView{}
}

func TestUpdateCheckAndCache(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, path, _ := updBridge(t, "v0.4.0", f, nil)
	if v := b.UpdateStatus(); v.Available || !v.Enabled || v.Dev || v.Current != "v0.4.0" || !v.CanInstall {
		t.Fatalf("before a check: %+v", v)
	}
	v, err := b.CheckUpdate(false)
	if err != nil || !v.Available || v.Latest != "v0.5.0" || !strings.HasSuffix(v.NotesURL, "/tag/v0.5.0") || f.n.Load() != 1 {
		t.Fatalf("check: %+v %v (requests %d)", v, err, f.n.Load())
	}
	c := loadCfg(t, path)
	if c.Update.Latest != "v0.5.0" || c.Update.LastCheck.IsZero() {
		t.Fatalf("not cached: %+v", c.Update)
	}
	// A second start within update.CheckEvery: the cache answers.
	b2, _, _, _ := updBridge(t, "v0.4.0", f, func(p string) { _ = c.Save(p) })
	if v, _ := b2.CheckUpdate(false); !v.Available || f.n.Load() != 1 {
		t.Fatalf("cached: %+v (requests %d)", v, f.n.Load())
	}
	// Banner from the cache before any check, too.
	if v := b2.UpdateStatus(); !v.Available {
		t.Fatalf("the cached answer shows no banner: %+v", v)
	}
	// Check now asks again.
	if _, err := b2.CheckUpdate(true); err != nil || f.n.Load() != 2 {
		t.Fatalf("forced: %v (requests %d)", err, f.n.Load())
	}
}

func TestUpdateCheckSwitchAndDev(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, path, _ := updBridge(t, "v0.4.0", f, nil)
	b.SetCheckUpdates(false)
	if c := loadCfg(t, path); c.Update.Check {
		t.Fatal("the switch wasn't saved")
	}
	if v, _ := b.CheckUpdate(false); v.Available || v.Enabled || f.n.Load() != 0 {
		t.Fatalf("switched off: %+v (requests %d)", v, f.n.Load())
	}
	// "Check now" still checks.
	if v, _ := b.CheckUpdate(true); !v.Available || f.n.Load() != 1 {
		t.Fatalf("forced with the switch off: %+v", v)
	}

	d, _, _, _ := updBridge(t, "dev", f, nil)
	if v, err := d.CheckUpdate(true); err != nil || !v.Dev || v.Available || v.CanInstall || f.n.Load() != 1 {
		t.Fatalf("dev: %+v %v (requests %d)", v, err, f.n.Load())
	}
	if err := d.InstallUpdate(); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Fatalf("dev install: %v", err)
	}
}

func TestUpdateCheckOfflineIsSilent(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	f.err.Store(true)
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	if v, err := b.CheckUpdate(false); err != nil || v.Available || v.Stage != "idle" {
		t.Fatalf("startup check, no network: %+v %v", v, err)
	}
	if _, err := b.CheckUpdate(true); err == nil || err.Error() != "GitHub didn't answer right now. Try again later." {
		t.Fatalf("Check now, the server failing: %v", err)
	}
	// No server at all: the player's words, not Go's.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	b.updSeam.client = update.NewClient(func(k string) string {
		if k == update.EnvURL {
			return "http://" + addr + "/latest"
		}
		return ""
	}, "v0.4.0")
	_, err = b.CheckUpdate(true)
	if err == nil || err.Error() != "Couldn't reach GitHub. Check your internet connection and try again." {
		t.Fatalf("Check now, no network: %v", err)
	}
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	if v := waitUpd(t, b, "failed"); v.Error != "Couldn't reach GitHub. Check your internet connection and try again. Nothing was changed." {
		t.Fatalf("install, no network: %q", v.Error)
	}
	if got := updateError(context.Background(), fmt.Errorf("x: %w", update.ErrBadBuild)); !strings.Contains(got, "antivirus") {
		t.Fatalf("bad build: %q", got)
	}
}

func TestUpdateCheckSkipsPrerelease(t *testing.T) {
	f := newFakeLatest(t, "v0.6.0-rc1")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	if v, _ := b.CheckUpdate(false); v.Available {
		t.Fatalf("a prerelease offered: %+v", v)
	}
	// Up to date: no banner.
	f.tag.Store("v0.4.0")
	if v, _ := b.CheckUpdate(true); v.Available {
		t.Fatalf("its own version offered: %+v", v)
	}
}

func TestUpdateInstall(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, path, fi := updBridge(t, "v0.4.0", f, nil)
	var quits atomic.Int32
	b.setQuit(func() { quits.Add(1) }, false)
	var started []string
	b.updSeam.start = func(c *exec.Cmd) error { started = c.Args; return nil }
	if _, err := b.CheckUpdate(false); err != nil {
		t.Fatal(err)
	}
	ev := &updEvents{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.pump(ctx, ev.emit)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	v := waitUpd(t, b, "restarting")
	if v.Percent != 100 || quits.Load() != 1 {
		t.Fatalf("restarting: %+v, quit %d", v, quits.Load())
	}
	if len(fi.calls) != 1 || fi.calls[0] != `v0.5.0 windows C:\games\ww-multiplayer.exe` {
		t.Fatalf("install calls %q", fi.calls)
	}
	if c := loadCfg(t, path); c.Update.Installed != "v0.5.0" {
		t.Fatalf("installed not recorded: %+v", c.Update)
	}
	if got := ev.stages(); !strings.Contains(strings.Join(got, ","), "downloading") || got[len(got)-1] != "restarting" {
		t.Fatalf("event stages %v", got)
	}
	if !ev.sawPercent(50) {
		t.Fatal("no progress event")
	}
	// The app closes, then the new version starts with the same arguments.
	b.relaunchNow()
	b.relaunchNow()
	if strings.Join(started, " ") != `C:\games\ww-multiplayer.exe ui --dev` {
		t.Fatalf("relaunched %q", started)
	}
	if err := b.InstallUpdate(); err == nil {
		t.Fatal("a second install while restarting")
	}
}

func TestUpdateInstallWithoutQuitSaysReopen(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	b.CheckUpdate(false)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	if v := waitUpd(t, b, "done"); v.Percent != 100 {
		t.Fatalf("%+v", v)
	}
}

func TestUpdateInstallRefusedDuringSession(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, fi := updBridge(t, "v0.4.0", f, nil)
	b.CheckUpdate(false)
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallUpdate(); err == nil || !strings.Contains(err.Error(), "Leave") {
		t.Fatalf("during a session: %v", err)
	}
	if len(fi.calls) != 0 {
		t.Fatal("installed during a session")
	}
	b.Leave()
	if err := b.InstallUpdate(); err != nil {
		t.Fatalf("after Leave: %v", err)
	}
	waitUpd(t, b, "done")
}

func TestUpdateInstallErrors(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, path, fi := updBridge(t, "v0.4.0", f, nil)
	b.CheckUpdate(false)
	fi.err = fmt.Errorf("wrap: %w", update.ErrHashMismatch)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	v := waitUpd(t, b, "failed")
	if !strings.Contains(v.Error, "checksum") || !strings.Contains(v.Error, "nothing was changed") {
		t.Fatalf("hash mismatch shown as %q", v.Error)
	}
	if c := loadCfg(t, path); c.Update.Installed != "" {
		t.Fatal("a failed install was recorded")
	}
	// A page reload's check keeps the failure on the banner.
	if v, _ := b.CheckUpdate(false); v.Stage != "failed" || v.Error == "" {
		t.Fatalf("the check cleared the failure: %+v", v)
	}
	// Cancel during the download.
	fi.err = nil
	fi.hold = make(chan struct{})
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b, "downloading")
	b.CancelUpdate()
	if v := waitUpd(t, b, "failed"); !strings.Contains(v.Error, "cancelled") {
		t.Fatalf("cancelled: %q", v.Error)
	}
	// Not offered any more (someone pulled the release): refused.
	fi.hold = nil
	f.tag.Store("v0.4.0")
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	if v := waitUpd(t, b, "failed"); !strings.Contains(v.Error, "newest version") {
		t.Fatalf("not newer: %q", v.Error)
	}
	// A platform without an asset can't install.
	b.updSeam.goos = "linux"
	if v := b.UpdateStatus(); v.CanInstall {
		t.Fatal("linux can install")
	}
	if err := b.InstallUpdate(); err == nil {
		t.Fatal("installed on linux")
	}
}

func TestUpdateOpenReleaseNotes(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	if err := b.OpenReleaseNotes(); err == nil {
		t.Fatal("opened without a browser")
	}
	var opened string
	b.setDesktop(nil, func(u string) error { opened = u; return nil })
	b.OpenReleaseNotes()
	if opened != DownloadsURL {
		t.Fatalf("no release yet: opened %q", opened)
	}
	b.CheckUpdate(false)
	b.OpenReleaseNotes()
	if opened != "https://github.com/StephenSHorton/ww-multiplayer/releases/tag/v0.5.0" {
		t.Fatalf("opened %q", opened)
	}
}

func TestRepatchPlan(t *testing.T) {
	dir := t.TempDir()
	vanilla := touch(t, filepath.Join(dir, "ww.ciso"))
	patched := filepath.Join(dir, "ww-multiplayer.iso")
	ins := func(st patch.DiscState, err error) func(string) (patch.DiscState, error) {
		return func(string) (patch.DiscState, error) { return st, err }
	}
	cfg := func(p, v string) config.Config {
		c := config.Defaults()
		c.ISO.Patched, c.ISO.Vanilla = p, v
		return c
	}
	for _, c := range []struct {
		name    string
		c       config.Config
		inspect func(string) (patch.DiscState, error)
		want    string
	}{
		{"no disc yet", cfg("", vanilla), ins(patch.Patched, nil), ""},
		{"same game code", cfg(patched, vanilla), ins(patch.Patched, nil), "keep"},
		{"other version", cfg(patched, vanilla), ins(patch.OtherVersion, nil), "repatch"},
		{"disc gone", cfg(patched, vanilla), ins(patch.Vanilla, os.ErrNotExist), "repatch"},
		{"no vanilla saved", cfg(patched, ""), ins(patch.OtherVersion, nil), "pick"},
		{"vanilla gone", cfg(patched, filepath.Join(dir, "gone.iso")), ins(patch.OtherVersion, nil), "pick"},
	} {
		if got := repatchPlan(c.c, c.inspect, isFile); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestUpdateFirstStartRepatches: the first start after an update reads
// and clears update.installed, decides the disc is from the old version
// and Repatch patches the saved vanilla disc again.
func TestUpdateFirstStartRepatches(t *testing.T) {
	dir := t.TempDir()
	vanilla := touch(t, filepath.Join(dir, "Wind Waker.ciso"))
	patched := touch(t, filepath.Join(dir, "Wind Waker-multiplayer.iso"))
	b, _, path, _ := updBridge(t, "v0.5.0", nil, func(p string) {
		c := config.Defaults()
		c.ISO.Patched, c.ISO.Vanilla = patched, vanilla
		c.Update.Installed, c.Update.Latest = "v0.5.0", "v0.5.0"
		if err := c.Save(p); err != nil {
			t.Fatal(err)
		}
	})
	if c := loadCfg(t, path); c.Update.Installed != "" {
		t.Fatal("update.installed wasn't cleared at start")
	}
	b.inspect = func(string) (patch.DiscState, error) { return patch.OtherVersion, nil }
	fp := &fakePatcher{}
	b.patchRun = fp.run
	v := b.UpdateStatus()
	if v.Installed != "v0.5.0" || v.Repatch != "repatch" || v.Available {
		t.Fatalf("first start: %+v", v)
	}
	if err := b.Repatch(); err != nil {
		t.Fatal(err)
	}
	if pv := waitPatchDone(t, b); pv.Stage != "done" || pv.In != vanilla || pv.Out != patched {
		t.Fatalf("re-patch: %+v", pv)
	}
	if v := b.UpdateStatus(); v.Repatch != "" {
		t.Fatalf("the re-patch is offered again: %+v", v)
	}
	// The re-patch keeps an optional fix (`patch --fix-sea-heap`) on the
	// disc it replaces or on the last patched one: the app has no switch
	// for it (patch.Options.KeepFixes; patch's TestRunKeepsFixes).
	if !fp.opts.KeepFixes || !slices.Contains(fp.opts.KeepFrom, patched) {
		t.Errorf("re-patch options: keep %v from %q, want the fixes kept from %s", fp.opts.KeepFixes, fp.opts.KeepFrom, patched)
	}
	// A second start of the same version: nothing to do.
	b2, _, _, _ := updBridge(t, "v0.5.0", nil, func(p string) {
		data, _ := os.ReadFile(path)
		os.WriteFile(p, data, 0o644)
	})
	if v := b2.UpdateStatus(); v.Installed != "" || v.Repatch != "" {
		t.Fatalf("second start: %+v", v)
	}
}

func TestUpdateFirstStartKeepsCurrentDisc(t *testing.T) {
	dir := t.TempDir()
	patched := touch(t, filepath.Join(dir, "ww-multiplayer.iso"))
	b, _, _, _ := updBridge(t, "v0.5.0", nil, func(p string) {
		c := config.Defaults()
		c.ISO.Patched = patched
		c.Update.Installed = "v0.5.0"
		_ = c.Save(p)
	})
	b.inspect = func(string) (patch.DiscState, error) { return patch.Patched, nil }
	if v := b.UpdateStatus(); v.Repatch != "keep" {
		t.Fatalf("%+v", v)
	}
	if err := b.Repatch(); err == nil {
		t.Fatal("Repatch without a vanilla disc")
	}
}

// updEvents collects a Bridge's "update" events.
type updEvents struct {
	mu  sync.Mutex
	all []UpdateView
}

func (e *updEvents) emit(ev EventView) {
	if ev.Kind != "update" || ev.Update == nil {
		return
	}
	e.mu.Lock()
	e.all = append(e.all, *ev.Update)
	e.mu.Unlock()
}

func (e *updEvents) stages() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var s []string
	for _, v := range e.all {
		if len(s) == 0 || s[len(s)-1] != v.Stage {
			s = append(s, v.Stage)
		}
	}
	return s
}

func (e *updEvents) sawPercent(p int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, v := range e.all {
		if v.Percent == p {
			return true
		}
	}
	return false
}

// TestBrowserUpdateQuit: an update's quit ends ui --browser's Wait, and
// Close then starts the new version.
func TestBrowserUpdateQuit(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	var started atomic.Bool
	b.updSeam.start = func(*exec.Cmd) error { started.Store(true); return nil }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := serveBrowser(b, ln)
	if err != nil {
		t.Fatal(err)
	}
	b.CheckUpdate(false)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait(context.Background()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait didn't return after the update")
	}
	if !started.Load() {
		t.Fatal("the new version wasn't started")
	}
}

// TestUpdateBlocksSessionsWhileDownloading: no session or patch starts
// during a download; they work again once it failed or was cancelled.
func TestUpdateBlocksSessionsWhileDownloading(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, fi := updBridge(t, "v0.4.0", f, nil)
	b.CheckUpdate(false)
	fi.hold = make(chan struct{})
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b, "downloading")
	if err := b.Host(HostRequest{Name: "Link"}); !errors.Is(err, errUpdating) {
		t.Fatalf("Host while downloading: %v", err)
	}
	if err := b.Join(JoinRequest{Addr: "127.0.0.1"}); !errors.Is(err, errUpdating) {
		t.Fatalf("Join while downloading: %v", err)
	}
	if err := b.Start(false); !errors.Is(err, errUpdating) {
		t.Fatalf("Start while downloading: %v", err)
	}
	if err := b.StartPatch(PatchRequest{Path: touch(t, filepath.Join(t.TempDir(), "ww.iso"))}); !errors.Is(err, errUpdating) {
		t.Fatalf("StartPatch while downloading: %v", err)
	}
	b.CancelUpdate()
	waitUpd(t, b, "failed")
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatalf("Host after the download: %v", err)
	}
}

// TestUpdateWaitsForASessionThatStarted: a session that started while
// the update downloaded isn't torn down: the stage is "ready" and the
// app restarts after Leave.
func TestUpdateWaitsForASessionThatStarted(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, fi := updBridge(t, "v0.4.0", f, nil)
	var quits atomic.Int32
	b.setQuit(func() { quits.Add(1) }, false)
	b.CheckUpdate(false)
	fi.during = func() {
		if _, err := b.ctrl.HostLobby(b.ctx, session.HostOptions{Name: "Link", Port: 25565}); err != nil {
			t.Error(err)
		}
		waitRunning(t, b)
	}
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b, "ready")
	if quits.Load() != 0 {
		t.Fatal("the app closed during the session")
	}
	if err := b.InstallUpdate(); err == nil {
		t.Fatal("a second install while ready")
	}
	if err := b.RestartUpdate(); err == nil {
		t.Fatal("Restart during the session")
	}
	b.Leave()
	waitUpd(t, b, "restarting")
	if quits.Load() != 1 {
		t.Fatalf("quit %d times after Leave", quits.Load())
	}
}

func waitRunning(t *testing.T, b *Bridge) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !b.running() {
		if time.Now().After(deadline) {
			t.Fatal("the session never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestUpdateCloseVetoedWaits: "Stay" on the close question an update's
// restart raised leaves the update "ready", not "restarting" for good;
// Restart then closes the app.
func TestUpdateCloseVetoedWaits(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	var quits atomic.Int32
	b.setQuit(func() {
		quits.Add(1)
		// The window asks first, and the player stays (a session that
		// started at the last moment, as the window's Quit would ask).
		b.closeVetoed()
	}, false)
	b.CheckUpdate(false)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b, "ready")
	if _, err := b.CheckUpdate(true); err != nil {
		t.Fatal(err)
	}
	if v := b.UpdateStatus(); v.Stage != "ready" {
		t.Fatalf("a check cleared ready: %+v", v)
	}
	if err := b.RestartUpdate(); err != nil || quits.Load() != 2 {
		t.Fatalf("Restart: %v, quit %d", err, quits.Load())
	}
	// beforeClose's Stay does the same.
	b.setUpd(func(v *UpdateView) { v.Stage = "restarting" })
	if err := b.Host(HostRequest{Name: "Link"}); !errors.Is(err, errUpdating) {
		t.Fatalf("Host while restarting: %v", err)
	}
	b.setUpd(func(v *UpdateView) { v.Stage = "ready" })
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, b)
	b.setUpd(func(v *UpdateView) { v.Stage = "restarting" })
	if !b.beforeClose(func(string) bool { return false }, func() {}) {
		t.Fatal("Stay closed the window")
	}
	if v := b.UpdateStatus(); v.Stage != "ready" {
		t.Fatalf("after Stay: %+v", v)
	}
}

// TestUpdateCheckDuringInstallKeepsStage: Update pressed on the cached
// answer while the start's check is still out: the check's end doesn't
// put the banner back to idle mid-download.
func TestUpdateCheckDuringInstallKeepsStage(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b2, _, _, fi2 := updBridge(t, "v0.4.0", f, nil)
	gate := make(chan struct{})
	f.gate.Store(gate)
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		b2.CheckUpdate(false)
	}()
	waitUpd(t, b2, "checking")
	fi2.hold = make(chan struct{})
	if err := b2.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b2, "downloading")
	f.gate.Store((chan struct{})(nil))
	close(gate)
	<-checked
	if v := b2.UpdateStatus(); v.Stage != "downloading" {
		t.Fatalf("the check's end changed the stage: %+v", v)
	}
	close(fi2.hold)
	waitUpd(t, b2, "done")
}

// TestUpdateRepatchSurvivesAnInterruption: the re-patch doesn't hang on a
// one-time flag. A later start finds the disc still from the old version
// and patches it again; an installed flag naming another version is only
// cleared; a running Dolphin is asked to close first.
func TestUpdateRepatchSurvivesAnInterruption(t *testing.T) {
	dir := t.TempDir()
	vanilla := touch(t, filepath.Join(dir, "Wind Waker.ciso"))
	patched := touch(t, filepath.Join(dir, "Wind Waker-multiplayer.iso"))
	cfgWith := func(installed string) func(string) {
		return func(p string) {
			c := config.Defaults()
			c.ISO.Patched, c.ISO.Vanilla = patched, vanilla
			c.Update.Installed = installed
			if err := c.Save(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A start after the update's first (the re-patch was cancelled).
	b, _, _, _ := updBridge(t, "v0.5.0", nil, cfgWith(""))
	b.inspect = func(string) (patch.DiscState, error) { return patch.OtherVersion, nil }
	if v := b.UpdateStatus(); v.Installed != "" || v.Repatch != "repatch" {
		t.Fatalf("later start: %+v", v)
	}
	b.procs = func() []launch.Process { return []launch.Process{{PID: 7}} }
	if err := b.Repatch(); err == nil || !strings.Contains(err.Error(), "close Dolphin") {
		t.Fatalf("Repatch with Dolphin running: %v", err)
	}
	b.procs = func() []launch.Process { return nil }
	fp := &fakePatcher{}
	b.patchRun = fp.run
	if err := b.Repatch(); err != nil {
		t.Fatal(err)
	}
	waitPatchDone(t, b)

	// Another copy's flag: cleared, no "Updated to".
	b2, _, path2, _ := updBridge(t, "v0.5.0", nil, cfgWith("v0.4.0"))
	b2.inspect = func(string) (patch.DiscState, error) { return patch.Patched, nil }
	if v := b2.UpdateStatus(); v.Installed != "" || v.Repatch != "" {
		t.Fatalf("another version's flag: %+v", v)
	}
	if c := loadCfg(t, path2); c.Update.Installed != "" {
		t.Fatal("the flag wasn't cleared")
	}

	// Not after an update, a disc that isn't readable or is this build's:
	// nothing to do.
	for _, st := range []patch.DiscState{patch.Patched, patch.Vanilla} {
		c := config.Defaults()
		c.ISO.Patched, c.ISO.Vanilla = patched, vanilla
		if got := discPlan(c, func(string) (patch.DiscState, error) { return st, nil }, isFile, false); got != "" {
			t.Errorf("%v: %q", st, got)
		}
	}
	c := config.Defaults()
	c.ISO.Patched = patched
	if got := discPlan(c, func(string) (patch.DiscState, error) { return patch.OtherVersion, nil }, isFile, false); got != "pick" {
		t.Errorf("no vanilla: %q", got)
	}
}

// TestUpdateRelaunchFallback: the new version doesn't start (antivirus
// removed it): the old exe is put back and started, and the player is
// told in a message box.
func TestUpdateRelaunchFallback(t *testing.T) {
	f := newFakeLatest(t, "v0.5.0")
	b, _, _, _ := updBridge(t, "v0.4.0", f, nil)
	b.setQuit(func() {}, false)
	var starts []string
	b.updSeam.start = func(c *exec.Cmd) error {
		starts = append(starts, strings.Join(c.Args, " "))
		if len(starts) == 1 {
			return errors.New("file not found")
		}
		return nil
	}
	var restored string
	b.updSeam.restore = func(exe string) bool { restored = exe; return true }
	var alert string
	b.updSeam.alert = func(_, text string) { alert = text }
	b.CheckUpdate(false)
	if err := b.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpd(t, b, "restarting")
	b.relaunchNow()
	if restored != `C:\games\ww-multiplayer.exe` || len(starts) != 2 || starts[1] != `C:\games\ww-multiplayer.exe ui --dev` {
		t.Fatalf("restored %q, starts %q", restored, starts)
	}
	if !strings.Contains(alert, "previous one was put back") {
		t.Fatalf("alert %q", alert)
	}
}

// TestSetupPatchedStale: the R9 "pick" start (a disc another version
// patched, no vanilla disc saved) and its "repatch" twin. Setup shows the
// patched disc as needing a re-patch, not as OK, whichever of Setup and
// UpdateStatus asks first; a disc of this build stays OK.
func TestSetupPatchedStale(t *testing.T) {
	for _, c := range []struct {
		name    string
		vanilla bool
		st      patch.DiscState
		plan    string
		ok      bool
	}{
		{"pick", false, patch.OtherVersion, "pick", false},
		{"repatch", true, patch.OtherVersion, "repatch", false},
		{"this build's", false, patch.Patched, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			patched := touch(t, filepath.Join(dir, "Wind Waker-multiplayer.iso"))
			vanilla := ""
			if c.vanilla {
				vanilla = touch(t, filepath.Join(dir, "Wind Waker.iso"))
			}
			b, _, _, _ := updBridge(t, "v0.6.1", nil, func(p string) {
				cfg := config.Defaults()
				cfg.ISO.Patched, cfg.ISO.Vanilla = patched, vanilla
				if err := cfg.Save(p); err != nil {
					t.Fatal(err)
				}
			})
			b.inspect = func(string) (patch.DiscState, error) { return c.st, nil }
			s := b.Setup() // before UpdateStatus: Setup decides the plan
			if s.PatchedOK != c.ok || s.PatchedStale == c.ok {
				t.Errorf("Setup: patchedOK %v patchedStale %v, want OK %v", s.PatchedOK, s.PatchedStale, c.ok)
			}
			if v := b.UpdateStatus(); v.Repatch != c.plan {
				t.Errorf("plan %q, want %q", v.Repatch, c.plan)
			}
		})
	}
}
