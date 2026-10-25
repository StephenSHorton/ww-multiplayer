package gui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

func touch(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadCfg(t *testing.T, path string) config.Config {
	t.Helper()
	c, warns := config.Load(path)
	if len(warns) > 0 {
		t.Fatalf("config warnings: %q", warns)
	}
	return c
}

func TestSetupGaps(t *testing.T) {
	dir := t.TempDir()
	iso := touch(t, filepath.Join(dir, "ww-multiplayer.iso"))
	exe := touch(t, filepath.Join(dir, "Dolphin.exe"))
	steps := func(g []SetupGapView) string {
		var s []string
		for _, x := range g {
			s = append(s, x.Step)
		}
		return strings.Join(s, ",")
	}
	full := config.Defaults()
	full.Name, full.ISO.Patched, full.Dolphin.Exe = "Tetra", iso, exe
	for name, c := range map[string]struct {
		mod  func(*config.Config)
		want string
		text string
	}{
		"complete":     {func(*config.Config) {}, "", ""},
		"new player":   {func(c *config.Config) { *c = config.Defaults() }, "profile,game,dolphin", ""},
		"blank name":   {func(c *config.Config) { c.Name = "  " }, "profile", ""},
		"no disc":      {func(c *config.Config) { c.ISO.Patched = "" }, "game", "Choose your Wind Waker disc"},
		"disc gone":    {func(c *config.Config) { c.ISO.Patched = filepath.Join(dir, "gone.iso") }, "game", "is gone"},
		"disc a dir":   {func(c *config.Config) { c.ISO.Patched = dir }, "game", "is gone"},
		"no dolphin":   {func(c *config.Config) { c.Dolphin.Exe = "" }, "dolphin", "Find Dolphin"},
		"dolphin gone": {func(c *config.Config) { c.Dolphin.Exe = filepath.Join(dir, "nope.exe") }, "dolphin", "gone from"},
	} {
		cfg := full
		c.mod(&cfg)
		g := setupGaps(cfg, isFile)
		if got := steps(g); got != c.want {
			t.Errorf("%s: gaps %q, want %q", name, got, c.want)
		}
		if c.text != "" && (len(g) != 1 || !strings.Contains(g[0].Text, c.text)) {
			t.Errorf("%s: %+v, want text %q", name, g, c.text)
		}
	}
}

func TestBridgeSetupView(t *testing.T) {
	b, _, path := newTestBridge(t)
	v := b.Setup()
	if !v.Needed || len(v.Gaps) != 3 || v.Done || v.FileDialog || v.Patch.Stage != "idle" {
		t.Fatalf("a new player's Setup = %+v", v)
	}
	if v.Mac != nil && b.macStatus != nil {
		if _, _, err := b.macStatus(); errors.Is(err, launch.ErrNotMac) {
			t.Errorf("Mac view off macOS: %+v", v.Mac)
		}
	}

	// A complete config: nothing needed. The env overrides count, and are
	// named.
	dir := t.TempDir()
	c := config.Defaults()
	c.Name, c.Color = "Tetra", "red"
	c.ISO.Patched = touch(t, filepath.Join(dir, "p.iso"))
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	exe := touch(t, filepath.Join(dir, "Dolphin.exe"))
	b2 := NewBridge(Options{Controller: b.ctrl, ConfigPath: path, Getenv: func(k string) string {
		if k == config.EnvDolphinExe {
			return exe
		}
		return ""
	}})
	defer b2.cancel()
	b2.setDialogs(&fakeDialogs{})
	v = b2.Setup()
	red, _ := playercolor.Parse("red")
	if v.Needed || len(v.Gaps) != 0 || !v.PatchedOK || !v.DolphinOK || v.DolphinExe != exe || v.Color != red.Hex() ||
		!v.FileDialog || !reflect.DeepEqual(v.Env, []string{config.EnvDolphinExe}) {
		t.Fatalf("Setup = %+v", v)
	}
	if err := b2.FinishSetup(); err != nil || !loadCfg(t, path).UI.SetupDone {
		t.Fatalf("FinishSetup: %v", err)
	}
	if loadCfg(t, path).Dolphin.Exe != "" {
		t.Error("the DOLPHIN_EXE override was saved")
	}
}

func TestBridgeSaveProfileAndColor(t *testing.T) {
	b, _, path := newTestBridge(t)
	for _, bad := range []ProfileRequest{{Name: ""}, {Name: strings.Repeat("a", MaxNameLen+1)}, {Name: "Tetra", Color: "chartreuse"}} {
		if err := b.SaveProfile(bad); err == nil {
			t.Errorf("SaveProfile(%+v) passed", bad)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused profile was saved")
	}
	if err := b.SaveProfile(ProfileRequest{Name: " Tetra ", Color: "#d83a30"}); err != nil {
		t.Fatal(err)
	}
	if c := loadCfg(t, path); c.Name != "Tetra" || c.Color != "#D83A30" {
		t.Fatalf("saved %q %q", c.Name, c.Color)
	}
	if v := b.Setup(); v.Needed && v.Gaps[0].Step == StepProfile {
		t.Errorf("the profile step is still a gap: %+v", v.Gaps)
	}
	// No color: classic green, nothing saved for it.
	if err := b.SaveProfile(ProfileRequest{Name: "Tetra"}); err != nil || loadCfg(t, path).Color != "" {
		t.Fatalf("no color: %v, %q", err, loadCfg(t, path).Color)
	}

	if v, err := b.CheckColor("Red"); err != nil || v.Name != "red" || v.Hex != playercolor.Palette()[1].Color.Hex() {
		t.Errorf("CheckColor(Red) = %+v, %v", v, err)
	}
	if v, err := b.CheckColor(" #d83a30 "); err != nil || v.Name != "" || v.Hex != "#D83A30" {
		t.Errorf("CheckColor(custom) = %+v, %v", v, err)
	}
	if _, err := b.CheckColor("#12"); err == nil {
		t.Error("CheckColor(#12) passed")
	}
}

// fakeDialogs answers PickFile.
type fakeDialogs struct {
	path       string
	err        error
	kind, dir  string
	title      string
	calledWith int
}

func (d *fakeDialogs) Open(kind, title, dir string) (string, error) {
	d.kind, d.title, d.dir = kind, title, dir
	d.calledWith++
	return d.path, d.err
}

func TestBridgePickFile(t *testing.T) {
	b, _, _ := newTestBridge(t)
	if _, err := b.PickFile("iso"); err == nil {
		t.Fatal("PickFile without the window's dialogs passed")
	}
	dir := t.TempDir()
	van := touch(t, filepath.Join(dir, "ww.ciso"))
	b.mu.Lock()
	b.cfg.ISO.Vanilla = van
	b.mu.Unlock()
	d := &fakeDialogs{path: filepath.Join(dir, "x.iso")}
	b.setDialogs(d)
	if p, err := b.PickFile("iso"); err != nil || p != d.path || d.kind != "iso" || d.dir != dir || !strings.Contains(d.title, "GZLE01") {
		t.Fatalf("PickFile(iso) = %q, %v; dialog %+v", p, err, d)
	}
	if _, err := b.PickFile("userdir"); err != nil || d.kind != "userdir" || d.dir != "" {
		t.Fatalf("PickFile(userdir): %v %+v", err, d)
	}
	if _, err := b.PickFile("exe"); err == nil || d.calledWith != 2 {
		t.Fatalf("an unknown kind opened a dialog: %v", err)
	}
	d.path, d.err = "", nil
	if p, err := b.PickFile("dolphin"); p != "" || err != nil {
		t.Fatalf("a cancelled dialog: %q, %v", p, err)
	}
}

// patchLines is what patch.Run writes for a vanilla disc, as the CLI
// prints it (cli_golden_test pins those lines), with the co-op step's.
func patchLines(t *testing.T, coop string) []string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	if err := os.WriteFile(in, tablestest.PatchableISO(tablestest.Vanilla()), 0o644); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	if _, err := patch.Run(context.Background(), in, "", &w, patch.Options{PostPatch: []func(io.Writer, string){func(w io.Writer, _ string) { fmt.Fprintln(w, coop) }}}); err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(w.String(), "\n"), "\n")
}

func TestApplyPatchLine(t *testing.T) {
	v := PatchView{}
	last := 0
	for _, l := range patchLines(t, "  co-op : stage tables built and cached in 0.1 s, for --coop sessions") {
		applyPatchLine(&v, l)
		if v.Percent < last {
			t.Fatalf("%q: percent went back from %d to %d", l, last, v.Percent)
		}
		last = v.Percent
		if strings.HasPrefix(l, "  output: ") && (v.Stage != "patch" || v.Percent != 5) {
			t.Errorf("after the output line: %+v", v)
		}
	}
	if v.Percent != 100 || v.Coop.State != "ok" || !strings.HasPrefix(v.Coop.Text, "stage tables built") || v.Error != "" || v.AlreadyPatched {
		t.Fatalf("a whole patch: %+v", v)
	}
	if len(v.Lines) == 0 || v.Lines[0] != "Wind Waker Multiplayer patcher" {
		t.Errorf("lines %q", v.Lines)
	}

	w := PatchView{}
	applyPatchLine(&w, "  co-op : WARNING: no co-op stage tables for this disc (x). The patched ISO is fine")
	if w.Coop.State != "warn" || !strings.HasPrefix(w.Coop.Text, "no co-op stage tables") {
		t.Errorf("warning row: %+v", w.Coop)
	}
	a := PatchView{}
	applyPatchLine(&a, "This ISO already contains this version of the multiplayer mod — nothing to do.")
	applyPatchLine(&a, "\r")
	if !a.AlreadyPatched || len(a.Lines) != 1 {
		t.Errorf("already patched: %+v", a)
	}
	e := PatchView{}
	applyPatchLine(&e, "ERROR: read x: boom\r")
	if e.Error != "read x: boom" {
		t.Errorf("error line: %+v", e)
	}
	many := PatchView{}
	for i := 0; i < maxPatchLines+50; i++ {
		applyPatchLine(&many, fmt.Sprint("line ", i))
	}
	if len(many.Lines) != maxPatchLines || many.Lines[0] != "line 50" {
		t.Errorf("lines kept: %d, first %q", len(many.Lines), many.Lines[0])
	}
	if creep(0) != 5 || creep(time.Hour) > 85 || creep(patchCreep) <= creep(patchCreep/2) {
		t.Errorf("creep: %d %d", creep(0), creep(time.Hour))
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(l string) { got = append(got, l) }}
	fmt.Fprint(w, "a\nb")
	fmt.Fprint(w, "c\n\nd")
	w.flush()
	w.flush()
	if !reflect.DeepEqual(got, []string{"a", "bc", "", "d"}) {
		t.Errorf("lines %q", got)
	}
}

// fakePatcher stands in for patch.Run: it writes the CLI's lines, creates
// the output, runs the co-op step and, when hold is set, waits for it (or
// for ctx, when cancellable) before returning.
type fakePatcher struct {
	already bool
	err     error
	hold    chan struct{}
	started chan struct{}
	calls   int
	opts    patch.Options // the last run's
}

func (f *fakePatcher) run(ctx context.Context, in, out string, w io.Writer, o patch.Options) (patch.Result, error) {
	f.calls++
	f.opts = o
	res := patch.Result{In: in, Out: out, AlreadyPatched: f.already}
	fmt.Fprintf(w, "Wind Waker Multiplayer patcher\n  input : %s\n  output: %s\n", in, out)
	if f.started != nil {
		close(f.started)
	}
	if f.hold != nil {
		<-f.hold
	}
	if f.err != nil {
		fmt.Fprintf(w, "\nERROR: %v\n", f.err)
		return res, f.err
	}
	if !f.already {
		if err := os.WriteFile(out, []byte("patched"), 0o644); err != nil {
			return res, err
		}
	} else {
		fmt.Fprintf(w, "\nThis ISO already contains this version of the multiplayer mod — nothing to do.\n")
	}
	for _, step := range o.PostPatch {
		step(w, res.Image())
	}
	fmt.Fprintf(w, "\nDone. Boot %s in Dolphin to play multiplayer.\n", res.Image())
	return res, nil
}

// events collects a Bridge's "patch" events.
type events struct {
	mu  sync.Mutex
	all []PatchView
}

func (e *events) emit(ev EventView) {
	if ev.Kind != "patch" || ev.Patch == nil {
		return
	}
	e.mu.Lock()
	e.all = append(e.all, *ev.Patch)
	e.mu.Unlock()
}

func (e *events) stages() []string {
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

func waitPatchDone(t *testing.T, b *Bridge) PatchView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v := b.PatchStatus(); !v.Running {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the patch never finished")
	return PatchView{}
}

func patchBridge(t *testing.T, f *fakePatcher) (*Bridge, string, *events) {
	t.Helper()
	b, _, path := newTestBridge(t)
	b.patchRun = f.run
	b.postPatch = []func(io.Writer, string){func(w io.Writer, image string) {
		fmt.Fprintf(w, "  co-op : stage tables of %s\n", filepath.Base(image))
	}}
	ev := &events{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.pump(ctx, ev.emit)
	return b, path, ev
}

func TestBridgeStartPatch(t *testing.T) {
	f := &fakePatcher{}
	b, path, ev := patchBridge(t, f)
	dir := t.TempDir()
	in := touch(t, filepath.Join(dir, "Wind Waker.ciso"))
	for _, bad := range []PatchRequest{{Path: ""}, {Path: filepath.Join(dir, "none.iso")}, {Path: dir}} {
		if err := b.StartPatch(bad); err == nil {
			t.Errorf("StartPatch(%+v) passed", bad)
		}
	}
	if err := b.StartPatch(PatchRequest{Path: ` "` + in + `" `}); err != nil {
		t.Fatal(err)
	}
	v := waitPatchDone(t, b)
	out := filepath.Join(dir, "Wind Waker-multiplayer.iso")
	if v.Stage != "done" || v.Percent != 100 || v.Image != out || v.Out != out || v.In != in || v.Coop.State != "ok" || v.Error != "" {
		t.Fatalf("done: %+v", v)
	}
	if got := ev.stages(); !reflect.DeepEqual(got[len(got)-2:], []string{"coop", "done"}) || got[0] != "start" {
		t.Errorf("event stages %q", got)
	}
	if c := loadCfg(t, path); c.ISO.Patched != out || c.ISO.Vanilla != in {
		t.Fatalf("saved %+v", c.ISO)
	}
	if s := b.Setup(); !s.PatchedOK || !s.VanillaOK || s.Patch.Stage != "done" {
		t.Errorf("Setup after the patch: %+v", s)
	}

	// An already patched disc: taken as it is; the vanilla one stays.
	f.already = true
	pin := touch(t, filepath.Join(dir, "mine-multiplayer.iso"))
	if err := b.StartPatch(PatchRequest{Path: pin}); err != nil {
		t.Fatal(err)
	}
	if v := waitPatchDone(t, b); v.Stage != "done" || !v.AlreadyPatched || v.Image != pin || v.ID != 2 {
		t.Fatalf("already patched: %+v", v)
	}
	if c := loadCfg(t, path); c.ISO.Patched != pin || c.ISO.Vanilla != in {
		t.Fatalf("saved %+v", c.ISO)
	}

	// A failure: shown, nothing saved.
	f.already, f.err = false, fmt.Errorf("wrap: %w", inject.ErrPatchedByOtherVersion)
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	if v := waitPatchDone(t, b); v.Stage != "failed" || !strings.Contains(v.Error, "different ww-multiplayer version") {
		t.Fatalf("failed: %+v", v)
	}
	if c := loadCfg(t, path); c.ISO.Patched != pin {
		t.Fatalf("a failed patch saved %+v", c.ISO)
	}
}

func TestBridgeStartPatchAlreadyPatchedChoice(t *testing.T) {
	f := &fakePatcher{already: true}
	b, path, _ := patchBridge(t, f)
	in := touch(t, filepath.Join(t.TempDir(), "p.iso"))
	for st, want := range map[patch.DiscState]string{patch.Vanilla: "isn't patched yet", patch.OtherVersion: "different ww-multiplayer version"} {
		b.inspect = func(string) (patch.DiscState, error) { return st, nil }
		if err := b.StartPatch(PatchRequest{Path: in, Patched: true}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: %v", st, err)
		}
	}
	b.inspect = func(string) (patch.DiscState, error) { return patch.Vanilla, errors.New("disc: unexpected game id") }
	if err := b.StartPatch(PatchRequest{Path: in, Patched: true}); err == nil || !strings.Contains(err.Error(), "unexpected game id") {
		t.Errorf("not a disc: %v", err)
	}
	if f.calls != 0 {
		t.Fatal("a refused disc was patched")
	}
	b.inspect = func(string) (patch.DiscState, error) { return patch.Patched, nil }
	if err := b.StartPatch(PatchRequest{Path: in, Patched: true}); err != nil {
		t.Fatal(err)
	}
	if v := waitPatchDone(t, b); v.Stage != "done" || v.Image != in {
		t.Fatalf("%+v", v)
	}
	if c := loadCfg(t, path); c.ISO.Patched != in || c.ISO.Vanilla != "" {
		t.Fatalf("saved %+v", c.ISO)
	}
}

func TestBridgeCancelPatch(t *testing.T) {
	f := &fakePatcher{hold: make(chan struct{}), started: make(chan struct{})}
	b, path, _ := patchBridge(t, f)
	coopCalls := 0
	b.postPatch = []func(io.Writer, string){func(io.Writer, string) { coopCalls++ }}
	dir := t.TempDir()
	in := touch(t, filepath.Join(dir, "ww.iso"))
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	if err := b.StartPatch(PatchRequest{Path: in}); err == nil || !strings.Contains(err.Error(), "already being patched") {
		t.Fatalf("a second patch: %v", err)
	}
	if !b.patching() {
		t.Fatal("patching() is false while it runs")
	}
	b.CancelPatch()
	if v := b.PatchStatus(); v.Stage != "canceling" || !v.Running {
		t.Fatalf("canceling: %+v", v)
	}
	close(f.hold) // the disc is written anyway (PatchISO can't stop)
	v := waitPatchDone(t, b)
	out := filepath.Join(dir, "ww-multiplayer.iso")
	if v.Stage != "canceled" || v.Error != "" || v.Stopped != "removed" {
		t.Fatalf("canceled: %+v", v)
	}
	// A cancelled patch builds (and records) no co-op tables for a disc
	// that is about to be removed.
	if coopCalls != 0 || v.Coop.State != "" {
		t.Errorf("co-op step ran after cancel: %d calls, row %+v", coopCalls, v.Coop)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("the cancelled patch's output was kept: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a cancelled patch saved settings")
	}

	// An output that was there before the patch: PatchISO already wrote
	// the new disc over it, so the new one is kept (not removed, which
	// would leave neither) and the view says it replaced the earlier one.
	touch(t, out)
	f.hold, f.started = make(chan struct{}), make(chan struct{})
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	b.CancelPatch()
	close(f.hold)
	if v := waitPatchDone(t, b); v.Stage != "canceled" || v.Stopped != "replaced" {
		t.Errorf("cancel over an earlier output: %+v", v)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "patched" {
		t.Errorf("the output after cancel holds %q (%v), want the new disc", data, err)
	}

	// Stopped before the disc was read: nothing written.
	os.Remove(out)
	f.hold, f.started, f.err = make(chan struct{}), make(chan struct{}), context.Canceled
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	b.CancelPatch()
	close(f.hold)
	if v := waitPatchDone(t, b); v.Stage != "canceled" || v.Stopped != "nothing" {
		t.Errorf("early cancel: %+v", v)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("early cancel wrote %s: %v", out, err)
	}
	b.CancelPatch() // nothing running: no-op
}

// TestBridgeCloseWaitsForPatch: closing the app cancels a running patch
// and waits for it, so no half-written disc or stray output is left.
func TestBridgeCloseWaitsForPatch(t *testing.T) {
	f := &fakePatcher{hold: make(chan struct{}), started: make(chan struct{})}
	b, _, _ := patchBridge(t, f)
	dir := t.TempDir()
	in := touch(t, filepath.Join(dir, "ww.iso"))
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	closed := make(chan struct{})
	go func() { b.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close returned while the patch ran")
	case <-time.After(50 * time.Millisecond):
	}
	close(f.hold)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close never returned")
	}
	if v := b.PatchStatus(); v.Running || v.Stage != "canceled" {
		t.Fatalf("after close: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "ww-multiplayer.iso")); !os.IsNotExist(err) {
		t.Errorf("output left after close: %v", err)
	}
}

// TestBridgeRealPatch runs the real patch.Run on a small disc.
func TestBridgeRealPatch(t *testing.T) {
	b, _, path := newTestBridge(t)
	coop := 0
	b.postPatch = []func(io.Writer, string){func(w io.Writer, image string) {
		coop++
		fmt.Fprintf(w, "  co-op : stage tables built and cached in 0.0 s, for --coop sessions\n")
	}}
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	if err := os.WriteFile(in, tablestest.PatchableISO(tablestest.Vanilla()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	v := waitPatchDone(t, b)
	out := filepath.Join(dir, "ww-multiplayer.iso")
	if v.Stage != "done" || v.Coop.State != "ok" || coop != 1 {
		t.Fatalf("%+v", v)
	}
	if st, err := patch.Inspect(out); err != nil || st != patch.Patched {
		t.Fatalf("output: %v, %v", st, err)
	}
	if c := loadCfg(t, path); c.ISO.Patched != out {
		t.Fatalf("saved %+v", c.ISO)
	}
	// "I already have a patched ISO" with the real Inspect.
	if err := b.StartPatch(PatchRequest{Path: in, Patched: true}); err == nil {
		t.Error("the vanilla disc was taken as patched")
	}
	if err := b.StartPatch(PatchRequest{Path: out, Patched: true}); err != nil {
		t.Fatal(err)
	}
	if v := waitPatchDone(t, b); v.Stage != "done" || !v.AlreadyPatched || v.Image != out {
		t.Fatalf("%+v", v)
	}
}

// fakeEnv is a launch.Env over the real file system plus a fake
// running Dolphin.
func fakeEnv(home, cfg string, running ...string) func() launch.Env {
	return func() launch.Env {
		return launch.Env{
			GOOS: "windows", Home: home, ConfigDir: cfg,
			Getenv:  func(string) string { return "" },
			Exists:  func(p string) bool { _, err := os.Stat(p); return err == nil },
			Running: func() []string { return running },
		}
	}
}

func TestBridgeDolphin(t *testing.T) {
	b, _, path := newTestBridge(t)
	home, cfgDir := t.TempDir(), t.TempDir()
	desk := touch(t, filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe"))
	b.denv = fakeEnv(home, cfgDir)
	v := b.DetectDolphin()
	if !v.Found || v.Exe != desk || v.Source != string(launch.SourcePortable) || v.UserDir != filepath.Join(cfgDir, "Dolphin Emulator") || v.Portable || v.Saved != "" {
		t.Fatalf("DetectDolphin = %+v", v)
	}
	// A running Dolphin comes first.
	other := touch(t, filepath.Join(t.TempDir(), "Dolphin.exe"))
	b.denv = fakeEnv(home, cfgDir, other)
	if v := b.DetectDolphin(); v.Exe != other || v.Source != "running" || len(v.Candidates) < 2 {
		t.Fatalf("running: %+v", v)
	}
	// Nothing found: the default path, not found.
	b.denv = fakeEnv(t.TempDir(), cfgDir)
	if v := b.DetectDolphin(); v.Found || v.Source != string(launch.SourceDefault) {
		t.Fatalf("none: %+v", v)
	}

	b.denv = fakeEnv(home, cfgDir)
	for _, bad := range []DolphinRequest{{}, {Exe: filepath.Join(home, "nope.exe")}, {Exe: desk, UserDir: desk}} {
		if err := b.SetDolphin(bad); err == nil {
			t.Errorf("SetDolphin(%+v) passed", bad)
		}
	}
	if err := b.SetDolphin(DolphinRequest{Exe: desk}); err != nil {
		t.Fatal(err)
	}
	if c := loadCfg(t, path); c.Dolphin.Exe != desk || c.Dolphin.UserDir != filepath.Join(cfgDir, "Dolphin Emulator") {
		t.Fatalf("saved %+v", c.Dolphin)
	}
	// A portable Dolphin, chosen by its folder: <dir>\User.
	pdir := filepath.Join(t.TempDir(), "Dolphin-portable")
	pexe := touch(t, filepath.Join(pdir, "Dolphin.exe"))
	touch(t, filepath.Join(pdir, "portable.txt"))
	if err := b.SetDolphin(DolphinRequest{Exe: pdir}); err != nil {
		t.Fatal(err)
	}
	if c := loadCfg(t, path); c.Dolphin.Exe != pexe || c.Dolphin.UserDir != filepath.Join(pdir, "User") {
		t.Fatalf("portable: %+v", c.Dolphin)
	}
	// A macOS bundle is its program; a user dir given is kept.
	app := filepath.Join(t.TempDir(), "Dolphin.app")
	bin := touch(t, filepath.Join(app, "Contents", "MacOS", "Dolphin"))
	ud := filepath.Join(t.TempDir(), "my user")
	if err := b.SetDolphin(DolphinRequest{Exe: app, UserDir: ud}); err != nil {
		t.Fatal(err)
	}
	if c := loadCfg(t, path); c.Dolphin.Exe != bin || c.Dolphin.UserDir != ud {
		t.Fatalf("bundle: %+v", c.Dolphin)
	}
	if v := b.DetectDolphin(); v.Saved != bin || !v.SavedOK || v.SavedDir != ud {
		t.Fatalf("saved view: %+v", v)
	}
	if s := b.Setup(); !s.DolphinOK {
		t.Errorf("Setup: %+v", s)
	}
}

func TestBridgeMacPrepare(t *testing.T) {
	b, _, path := newTestBridge(t)
	home := t.TempDir()
	b.home = func() string { return home }
	for st, want := range map[launch.MacState]MacView{
		launch.MacReady:        {State: "ready"},
		launch.MacNoDolphin:    {State: "no-dolphin"},
		launch.MacNeedsCopy:    {State: "needs-copy", Prepare: true},
		launch.MacHardened:     {State: "hardened", Prepare: true},
		launch.MacCopyOutdated: {State: "outdated", Prepare: true, Refresh: true},
	} {
		b.macStatus = func() (launch.MacState, launch.MacFacts, error) { return st, launch.MacFacts{}, nil }
		v := b.Setup().Mac
		if v == nil || v.State != want.State || v.Prepare != want.Prepare || v.Refresh != want.Refresh || v.Copy != launch.MacPreparedApp(home) {
			t.Errorf("%v: %+v", st, v)
		}
		if st != launch.MacReady && v.Text == "" {
			t.Errorf("%v has no text", st)
		}
	}
	b.macStatus = func() (launch.MacState, launch.MacFacts, error) {
		return launch.MacNoDolphin, launch.MacFacts{}, launch.ErrNotMac
	}
	if v := b.DetectDolphin().Mac; v != nil {
		t.Errorf("Mac view off macOS: %+v", v)
	}

	b.prepareMac = func(log.Reporter) error { return launch.ErrNotMac }
	if v := b.PrepareDolphin(); v.OK || !strings.Contains(v.Error, "Only macOS") {
		t.Errorf("not a Mac: %+v", v)
	}
	b.prepareMac = func(r log.Reporter) error {
		r.Log(log.Info, "==> Copying")
		return errors.New("cp: exit status 1")
	}
	if v := b.PrepareDolphin(); v.OK || v.Error != "cp: exit status 1" || len(v.Lines) != 1 {
		t.Errorf("failed: %+v", v)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a failed prepare saved settings")
	}
	b.prepareMac = func(r log.Reporter) error { r.Log(log.OK, "Done."); return nil }
	b.denv = fakeEnv(home, t.TempDir())
	if v := b.PrepareDolphin(); !v.OK || v.Error != "" || len(v.Lines) != 1 {
		t.Fatalf("prepared: %+v", v)
	}
	want := filepath.Join(launch.MacPreparedApp(home), "Contents", "MacOS", "Dolphin")
	if c := loadCfg(t, path); c.Dolphin.Exe != want || c.Dolphin.UserDir == "" {
		t.Fatalf("saved %+v", c.Dolphin)
	}
	// A Dolphin the player chose is kept.
	b.mu.Lock()
	b.cfg.Dolphin.Exe = "/elsewhere/Dolphin"
	b.mu.Unlock()
	if v := b.PrepareDolphin(); !v.OK || loadCfg(t, path).Dolphin.Exe != "/elsewhere/Dolphin" {
		t.Fatalf("kept: %+v %+v", v, loadCfg(t, path).Dolphin)
	}
	// One at a time.
	b.preparing.Lock()
	if v := b.PrepareDolphin(); v.OK || !strings.Contains(v.Error, "already") {
		t.Errorf("concurrent: %+v", v)
	}
	b.preparing.Unlock()
}

// FuzzApplyPatchLine: any line keeps the view sane: the percentage in
// 0-100 and never lower, the lines capped.
func FuzzApplyPatchLine(f *testing.F) {
	for _, s := range []string{"Wind Waker Multiplayer patcher", "  output: x", "  co-op : WARNING: y", "ERROR: z", "Done. Boot x", "\r", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		v := PatchView{Percent: 40}
		for i := 0; i < 3; i++ {
			before := v.Percent
			applyPatchLine(&v, line)
			if v.Percent < before || v.Percent > 100 || len(v.Lines) > maxPatchLines {
				t.Fatalf("%q: %+v", line, v)
			}
		}
	})
}

// TestPatchViewSeq: every change to a patch's view raises its seq, so
// the frontend can drop an older snapshot that arrives after a newer one
// (a PatchStatus reply after the final event).
func TestPatchViewSeq(t *testing.T) {
	f := &fakePatcher{}
	b, _, ev := patchBridge(t, f)
	in := touch(t, filepath.Join(t.TempDir(), "ww.iso"))
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	final := waitPatchDone(t, b)
	deadline := time.Now().Add(5 * time.Second)
	for {
		ev.mu.Lock()
		done := false
		for _, v := range ev.all {
			if v.Seq == final.Seq {
				done = true
			}
		}
		ev.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if final.Seq < 2 {
		t.Fatalf("final seq %d", final.Seq)
	}
	seen := map[int]bool{}
	for _, v := range ev.all {
		if v.Seq > final.Seq || (v.Running && v.Seq == final.Seq) {
			t.Errorf("event %s seq %d not older than the final view's %d", v.Stage, v.Seq, final.Seq)
		}
		if v.Seq < 1 {
			t.Errorf("event %s has no seq", v.Stage)
		}
		seen[v.Seq] = true
	}
	if !seen[final.Seq] || len(seen) < 3 {
		t.Errorf("seqs %v, final %d", seen, final.Seq)
	}
}

// TestBridgeBeforeClose: the close questions are all asked before
// anything changes ("Stay" leaves the patch running), and closing during
// a patch keeps the window open until the cancelled patch has ended.
func TestBridgeBeforeClose(t *testing.T) {
	f := &fakePatcher{hold: make(chan struct{}), started: make(chan struct{})}
	b, _, _ := patchBridge(t, f)
	var asked []string
	answers := map[string]bool{}
	ask := func(q string) bool { asked = append(asked, q); return answers[q] }
	quit := make(chan struct{}, 4)
	quitFn := func() { quit <- struct{}{} }

	// Nothing running: closes without asking.
	if b.beforeClose(ask, quitFn) || len(asked) != 0 {
		t.Fatalf("idle: asked %v", asked)
	}
	// A session only: "Stay" keeps it, "Leave" closes.
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	if !b.beforeClose(ask, quitFn) || strings.Join(asked, ",") != "session" {
		t.Fatalf("session, stay: asked %v", asked)
	}
	answers[askLeaveSession], asked = true, nil
	if b.beforeClose(ask, quitFn) {
		t.Fatal("session, leave: window kept")
	}

	// A patch and a session: "Close" to the patch, then "Stay" in the
	// session leaves the patch running (it is not cancelled).
	in := touch(t, filepath.Join(t.TempDir(), "ww.iso"))
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	answers[askStopPatch], answers[askLeaveSession], asked = true, false, nil
	if !b.beforeClose(ask, quitFn) || strings.Join(asked, ",") != "patch,session" {
		t.Fatalf("patch+session, stay: asked %v", asked)
	}
	if v := b.PatchStatus(); !v.Running || v.Stage == "canceling" {
		t.Fatalf("Stay cancelled the patch: %+v", v)
	}
	// "Keep patching": nothing else is asked.
	answers[askStopPatch], asked = false, nil
	if !b.beforeClose(ask, quitFn) || strings.Join(asked, ",") != "patch" || b.PatchStatus().Stage == "canceling" {
		t.Fatalf("keep patching: asked %v, %+v", asked, b.PatchStatus())
	}
	// Close to both: the patch is cancelled, the window stays open until
	// it has ended, then quit runs and the window may close.
	answers[askStopPatch], answers[askLeaveSession], asked = true, true, nil
	if !b.beforeClose(ask, quitFn) {
		t.Fatal("closing during a patch closed the window before the patch ended")
	}
	if v := b.PatchStatus(); v.Stage != "canceling" {
		t.Fatalf("not cancelled: %+v", v)
	}
	asked = nil
	if !b.beforeClose(ask, quitFn) || len(asked) != 0 {
		t.Fatalf("a second close while finishing: asked %v", asked)
	}
	select {
	case <-quit:
		t.Fatal("quit before the patch ended")
	case <-time.After(30 * time.Millisecond):
	}
	close(f.hold)
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("quit never ran")
	}
	if b.beforeClose(ask, quitFn) || len(asked) != 0 {
		t.Fatalf("after the patch ended: window kept or asked %v", asked)
	}
	if v := b.PatchStatus(); v.Stage != "canceled" {
		t.Fatalf("after close: %+v", v)
	}
}

// TestBridgeUserDirFor: the user folder follows the Dolphin chosen, so a
// portable Dolphin picked after a non-portable one was detected is saved
// with its own User folder, not the detected one's.
func TestBridgeUserDirFor(t *testing.T) {
	b, _, path := newTestBridge(t)
	home, cfgDir := t.TempDir(), t.TempDir()
	touch(t, filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe"))
	pdir := filepath.Join(t.TempDir(), "Dolphin-portable")
	pexe := touch(t, filepath.Join(pdir, "Dolphin.exe"))
	touch(t, filepath.Join(pdir, "portable.txt"))
	b.denv = fakeEnv(home, cfgDir)
	v := b.DetectDolphin()
	if v.UserDir != filepath.Join(cfgDir, "Dolphin Emulator") || v.Portable {
		t.Fatalf("detected %+v", v)
	}
	// The player picks the portable Dolphin (by its program or folder).
	for _, p := range []string{pexe, pdir, `"` + pexe + `"`} {
		if got := b.UserDirFor(p); got != filepath.Join(pdir, "User") {
			t.Errorf("UserDirFor(%s) = %q", p, got)
		}
	}
	if got := b.UserDirFor(filepath.Join(home, "nope.exe")); got != "" {
		t.Errorf("UserDirFor(missing) = %q", got)
	}
	if got := b.UserDirFor(""); got != "" {
		t.Errorf("UserDirFor(\"\") = %q", got)
	}
	if err := b.SetDolphin(DolphinRequest{Exe: pexe, UserDir: b.UserDirFor(pexe)}); err != nil {
		t.Fatal(err)
	}
	if c := loadCfg(t, path); c.Dolphin.Exe != pexe || c.Dolphin.UserDir != filepath.Join(pdir, "User") {
		t.Fatalf("saved %+v", c.Dolphin)
	}
}

// TestMacGap: on macOS the setup opens while Dolphin isn't prepared, or
// the saved Dolphin is the original bundle while the copy is there.
func TestMacGap(t *testing.T) {
	copyExe := "/Users/l/Applications/Dolphin.app/Contents/MacOS/Dolphin"
	for _, c := range []struct {
		st   launch.MacState
		err  error
		exe  string
		want bool
	}{
		{launch.MacNeedsCopy, nil, "", true},
		{launch.MacNeedsCopy, nil, macOriginalExe, true},
		{launch.MacHardened, nil, copyExe, true},
		{launch.MacReady, nil, copyExe, false},
		{launch.MacReady, nil, macOriginalExe, true},
		{launch.MacCopyOutdated, nil, copyExe, false}, // the launch toast covers it
		{launch.MacCopyOutdated, nil, macOriginalExe, true},
		{launch.MacNoDolphin, nil, "", false},
		{launch.MacNeedsCopy, launch.ErrNotMac, "", false},
	} {
		g := macGap(c.st, c.err, c.exe)
		if (g != nil) != c.want || (g != nil && g.Step != StepDolphin) {
			t.Errorf("%v %v %q: %+v", c.st, c.err, c.exe, g)
		}
	}

	// Through Setup: an otherwise complete setup opens again.
	b, _, _ := newTestBridge(t)
	dir := t.TempDir()
	iso, exe := touch(t, filepath.Join(dir, "ww.iso")), touch(t, filepath.Join(dir, "Dolphin"))
	b.mu.Lock()
	b.cfg.Name, b.cfg.ISO.Patched, b.cfg.Dolphin.Exe = "Tetra", iso, exe
	b.mu.Unlock()
	b.macStatus = func() (launch.MacState, launch.MacFacts, error) {
		return launch.MacReady, launch.MacFacts{}, nil
	}
	if s := b.Setup(); s.Needed {
		t.Fatalf("ready: %+v", s.Gaps)
	}
	b.macStatus = func() (launch.MacState, launch.MacFacts, error) {
		return launch.MacHardened, launch.MacFacts{}, nil
	}
	if s := b.Setup(); !s.Needed || len(s.Gaps) != 1 || s.Gaps[0].Step != StepDolphin || s.Mac == nil || s.Mac.State != "hardened" {
		t.Fatalf("hardened: %+v", s)
	}
}

// TestBridgePrepareRefusesRunningCopy: refreshing the copy starts by
// removing it, so it never runs under a Dolphin started from it.
func TestBridgePrepareRefusesRunningCopy(t *testing.T) {
	b, _, _ := newTestBridge(t)
	home := t.TempDir()
	b.home = func() string { return home }
	ran := 0
	b.prepareMac = func(log.Reporter) error { ran++; return nil }
	running := filepath.Join(launch.MacPreparedApp(home), "Contents", "MacOS", "Dolphin")
	b.denv = fakeEnv(home, t.TempDir(), running)
	if v := b.PrepareDolphin(); v.OK || !strings.Contains(v.Error, "Close Dolphin first") || ran != 0 {
		t.Fatalf("copy running: %+v, prepare ran %d", v, ran)
	}
	// The original running is fine: the copy is made from it.
	b.denv = fakeEnv(home, t.TempDir(), filepath.Join(launch.MacApp, "Contents", "MacOS", "Dolphin"))
	if v := b.PrepareDolphin(); !v.OK || ran != 1 {
		t.Fatalf("original running: %+v, prepare ran %d", v, ran)
	}
	// A path that only starts like the copy's is not inside it.
	b.denv = fakeEnv(home, t.TempDir(), launch.MacPreparedApp(home)+"-old/Contents/MacOS/Dolphin")
	if v := b.PrepareDolphin(); !v.OK || ran != 2 {
		t.Fatalf("lookalike running: %+v, prepare ran %d", v, ran)
	}
}

// TestBridgeRefusesSessionsWhilePatching: Host, Join and Start wait for
// a running patch (Start would open Dolphin on a disc not yet written;
// the sidebar reaches Home from Setup mid-patch), and work once it ends.
func TestBridgeRefusesSessionsWhilePatching(t *testing.T) {
	f := &fakePatcher{hold: make(chan struct{}), started: make(chan struct{})}
	b, _, _ := patchBridge(t, f)
	in := touch(t, filepath.Join(t.TempDir(), "ww.iso"))
	if err := b.StartPatch(PatchRequest{Path: in}); err != nil {
		t.Fatal(err)
	}
	<-f.started
	if err := b.Host(HostRequest{Name: "Link"}); !errors.Is(err, errPatching) {
		t.Fatalf("Host while patching: %v", err)
	}
	if err := b.Join(JoinRequest{Addr: "127.0.0.1", Name: "Link"}); !errors.Is(err, errPatching) {
		t.Fatalf("Join while patching: %v", err)
	}
	if err := b.Start(true); !errors.Is(err, errPatching) {
		t.Fatalf("Start while patching: %v", err)
	}
	if b.running() {
		t.Fatal("a session started while patching")
	}
	close(f.hold)
	if v := waitPatchDone(t, b); v.Stage != "done" {
		t.Fatalf("%+v", v)
	}
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatalf("Host after the patch: %v", err)
	}
}
