package gui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/internal/update"
)

// One-click updates (owner decision 2026-10-17): at start the window asks
// CheckUpdate, which asks GitHub's latest release at most every
// update.CheckEvery (the answer is cached in the config's "update"
// section) unless the "Check for updates" switch is off or this is a dev
// build. A newer release shows the banner "vX.Y.Z is out — Update /
// Release notes / Later". Update (InstallUpdate) is refused while a
// session runs (the banner comes back after Leave) or a disc is being
// patched, and while it downloads no session or patch starts (updating);
// it downloads the platform's asset, checks it against the release's
// SHA256SUMS, swaps it in (internal/update; the new version must run, or
// the old one is put back), records the version in config
// update.installed, and closes the app, which then starts the new version
// with the same arguments (relaunch). Should a session or patch have
// started anyway, or the player keep the window open, the stage is
// "ready": the app restarts after Leave, the patch's end, Restart, or
// when it is closed. Every start checks the patched disc (discPlan): one
// patched by another version (after an update, any disc not this
// build's) is patched again from the vanilla disc Setup saved, with the
// Setup screen's progress (Repatch), or, without one, Setup's game step
// opens. An interrupted re-patch is so taken up again at the next start.

// UpdateView is the update banner's state.
type UpdateView struct {
	// Seq grows with every change (events and replies may cross).
	Seq int `json:"seq"`
	// Enabled is the "Check for updates" switch (config update.check).
	Enabled bool `json:"enabled"`
	// Dev says this is a dev build: it never checks.
	Dev     bool   `json:"dev"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Available says Latest is newer than Current.
	Available bool   `json:"available"`
	NotesURL  string `json:"notesURL"`
	// CanInstall says Update works here (Windows, or the macOS .app);
	// elsewhere the banner offers the release notes only.
	CanInstall bool `json:"canInstall"`
	// Stage is "idle", "checking", "downloading", "installing",
	// "restarting" (the app closes and the new version opens), "ready"
	// (installed; the app restarts after Leave, or Restart, or when it is
	// closed), "done" (installed, but this app can't restart itself: open
	// it again) or "failed" (Error says why; nothing was changed).
	Stage   string `json:"stage"`
	Percent int    `json:"percent"`
	Error   string `json:"error"`
	// Installed is the version the last update installed, on that
	// version's first start ("Updated to vX.Y.Z"); Repatch is what this
	// start does with the disc (discPlan): "keep" (it is this build's,
	// after an update), "repatch" (patch it again from the saved vanilla
	// disc: Repatch), "pick" (no vanilla disc saved: Setup's game step) or
	// "".
	Installed string `json:"installed"`
	Repatch   string `json:"repatch"`
}

// updState is the Bridge's update: the view, the running install, and
// the relaunch it leaves for the app's exit.
type updState struct {
	mu       sync.Mutex
	view     UpdateView // Stage, Percent, Error, Latest, NotesURL, Seq
	cancel   context.CancelFunc
	done     chan struct{} // the running install's end
	relaunch *exec.Cmd
	// installed is config update.installed as this run started; plan is
	// its re-patch decision, made once (planOnce).
	installed string
	planOnce  sync.Once
	plan      string
	launched  sync.Once
	closing   atomic.Bool // close ran: no restart from a patch's end
}

// The update's seams (NewBridge sets the real ones; a test replaces them).
type updSeams struct {
	client  *update.Client
	goos    string
	exe     func() (string, error)
	install func(ctx context.Context, c *update.Client, rel update.Release, goos, exe string, progress update.Progress) error
	start   func(*exec.Cmd) error
	args    []string
	quit    func() // closes the app (the window's, ui --browser's); nil: can't
	quitMu  sync.Mutex
	// alert tells the player the new version didn't start once the window
	// is gone (a native message box on Windows); restore puts the old exe
	// back when the new one is missing (update.RestoreMissing).
	alert   func(title, text string)
	restore func(exe string) bool
	// stdio: the new version writes to this run's console (ui --browser,
	// whose console shows the page's address); the window's new version
	// gets a console of its own, which it hides.
	stdio bool
}

func (s *updSeams) init(o Options, getenv func(string) string) {
	s.client = update.NewClient(getenv, o.Version)
	s.goos = runtime.GOOS
	s.exe = os.Executable
	s.install = func(ctx context.Context, c *update.Client, rel update.Release, goos, exe string, p update.Progress) error {
		return c.Install(ctx, rel, goos, exe, p)
	}
	s.start = func(c *exec.Cmd) error { return c.Start() }
	s.alert = messageBox
	s.restore = func(exe string) bool { return update.RestoreMissing(update.OS, exe) }
	s.args = o.Args
	if s.args == nil && len(os.Args) > 1 {
		s.args = append([]string(nil), os.Args[1:]...)
	}
}

// afterUpdate reads (and clears, saving the config) update.installed:
// naming this version, this is the first start of a version an update
// installed; naming another (an older copy of the app sharing the
// config), it is only cleared. The disc is checked at every start anyway
// (discPlan), so clearing it loses no re-patch.
func (b *Bridge) afterUpdate() {
	b.mu.Lock()
	inst := b.cfg.Update.Installed
	b.cfg.Update.Installed = ""
	b.mu.Unlock()
	if inst == "" {
		return
	}
	if inst == b.version {
		b.upd.installed = inst
	}
	b.save()
}

// setQuit gives the Bridge the way to close the app (the window's Quit,
// ui --browser's stop) once an update is in place.
func (b *Bridge) setQuit(quit func(), stdio bool) {
	b.updSeam.quitMu.Lock()
	b.updSeam.quit, b.updSeam.stdio = quit, stdio
	b.updSeam.quitMu.Unlock()
}

// UpdateStatus returns the banner's state. Its first call decides what
// to do with the disc (UpdateView.Repatch, discPlan).
func (b *Bridge) UpdateStatus() UpdateView {
	b.mu.Lock()
	cfg := b.cfg
	b.mu.Unlock()
	b.upd.mu.Lock()
	v := b.upd.view
	b.upd.mu.Unlock()
	if v.Stage == "" {
		v.Stage = "idle"
	}
	v.Enabled = cfg.Update.Check
	v.Current = b.version
	v.Dev = update.IsDev(b.version)
	if v.Latest == "" {
		v.Latest, v.NotesURL = cfg.Update.Latest, cfg.Update.NotesURL
	}
	v.Available = !v.Dev && v.Latest != "" && update.Offer(b.version, update.Release{Tag: v.Latest})
	if v.NotesURL == "" && v.Available {
		v.NotesURL = DownloadsURL + "/tag/" + v.Latest
	}
	v.CanInstall = !v.Dev && update.AssetName(b.updSeam.goos) != "" && b.canInstallHere()
	v.Installed = b.upd.installed
	b.upd.planOnce.Do(func() {
		eff, _ := b.effective()
		plan := discPlan(eff, b.inspect, isFile, v.Installed != "")
		b.upd.mu.Lock()
		b.upd.plan = plan
		b.upd.mu.Unlock()
	})
	b.upd.mu.Lock()
	v.Repatch = b.upd.plan
	b.upd.mu.Unlock()
	return v
}

// discPlan is what this start does with the disc c says the player boots:
// on an update's first start, repatchPlan; on any other, "repatch" or
// "pick" only when the disc was patched by another version (an earlier
// re-patch was interrupted, or the player went back to an older copy),
// else "" (Setup and Start handle a missing or unreadable disc).
func discPlan(c config.Config, inspect func(string) (patch.DiscState, error), isFile func(string) bool, afterUpdate bool) string {
	if afterUpdate {
		return repatchPlan(c, inspect, isFile)
	}
	if c.ISO.Patched == "" {
		return ""
	}
	if st, err := inspect(c.ISO.Patched); err != nil || st != patch.OtherVersion {
		return ""
	}
	if c.ISO.Vanilla != "" && isFile(c.ISO.Vanilla) {
		return "repatch"
	}
	return "pick"
}

func (b *Bridge) canInstallHere() bool {
	exe, err := b.updSeam.exe()
	return err == nil && update.CanInstall(b.updSeam.goos, exe)
}

// repatchPlan is what an update's first start does with the disc c says
// the player boots: "" when there is none (the setup asks for one),
// "keep" when it holds this build's game code, else "repatch" from the
// saved vanilla disc, or "pick" when that is gone or was never saved.
func repatchPlan(c config.Config, inspect func(string) (patch.DiscState, error), isFile func(string) bool) string {
	if c.ISO.Patched == "" {
		return ""
	}
	if st, err := inspect(c.ISO.Patched); err == nil && st == patch.Patched {
		return "keep"
	}
	if c.ISO.Vanilla != "" && isFile(c.ISO.Vanilla) {
		return "repatch"
	}
	return "pick"
}

// setUpd changes the update's view and sends it as an "update" event.
func (b *Bridge) setUpd(f func(v *UpdateView)) {
	b.upd.mu.Lock()
	f(&b.upd.view)
	b.upd.view.Seq++
	b.upd.mu.Unlock()
	v := b.UpdateStatus()
	b.emit(EventView{Kind: "update", Time: b.now().UTC().Format(time.RFC3339Nano), Update: &v})
}

// CheckUpdate checks for a newer release. Without force it does nothing
// when the switch is off, and asks GitHub only when the cached answer is
// older than update.CheckEvery; a failed request is then silent (no
// network: no banner). force (the version dialog's "Check now") always
// asks and returns a failure. A dev build never checks.
func (b *Bridge) CheckUpdate(force bool) (_ UpdateView, err error) {
	defer b.maskErr(&err)
	if update.IsDev(b.version) {
		return b.UpdateStatus(), nil
	}
	b.mu.Lock()
	cfg := b.cfg.Update
	b.mu.Unlock()
	if !force && !cfg.Check {
		return b.UpdateStatus(), nil
	}
	b.upd.mu.Lock()
	prev := b.upd.view.Stage
	busy := prev == "checking" || prev == "restarting" || prev == "ready" || b.upd.cancel != nil
	b.upd.mu.Unlock()
	if busy {
		return b.UpdateStatus(), nil
	}
	st := update.State{LastCheck: cfg.LastCheck, Latest: cfg.Latest, NotesURL: cfg.NotesURL}
	if force {
		st.LastCheck = time.Time{}
	}
	b.setUpd(func(v *UpdateView) { v.Stage = "checking" })
	ctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
	defer cancel()
	_, nst, cerr := b.updSeam.client.Check(ctx, b.version, st, b.now())
	b.mu.Lock()
	b.cfg.Update.LastCheck, b.cfg.Update.Latest, b.cfg.Update.NotesURL = nst.LastCheck, nst.Latest, nst.NotesURL
	b.mu.Unlock()
	b.save()
	b.setUpd(func(v *UpdateView) {
		// An install started meanwhile (Update on the cached answer) keeps
		// its stage and release.
		if v.Stage != "checking" {
			return
		}
		// A failed or finished install stays on the banner.
		v.Stage = "idle"
		if prev == "failed" || prev == "done" {
			v.Stage = prev
		}
		v.Latest, v.NotesURL = nst.Latest, nst.NotesURL
	})
	if cerr != nil && force {
		return b.UpdateStatus(), errors.New(checkError(cerr))
	}
	return b.UpdateStatus(), nil
}

// netWords is a failed request in the player's words: the server's error
// answer, or no connection. ok is false for anything else.
func netWords(err error) (string, bool) {
	var ue *url.Error
	var ne net.Error
	switch {
	case errors.Is(err, update.ErrServer):
		return "GitHub didn't answer right now. Try again later.", true
	case errors.Is(err, context.Canceled):
		return "", false
	case errors.As(err, &ue), errors.As(err, &ne):
		return "Couldn't reach GitHub. Check your internet connection and try again.", true
	}
	return "", false
}

// checkError is Check now's failure in the player's words.
func checkError(err error) string {
	if w, ok := netWords(err); ok {
		return w
	}
	return "Couldn't check for updates: " + strings.TrimPrefix(err.Error(), "update: ") + "."
}

// errUpdating refuses a session or a patch while an update runs.
var errUpdating = errors.New("an update is downloading: wait until the app restarts on the new version, or cancel it on the Home screen")

// updating says an update is downloading or closing the app for the new
// version: Host, Join, Start and StartPatch wait (and Host, Join and
// Start wait for a running patch too, errPatching).
func (b *Bridge) updating() bool {
	b.upd.mu.Lock()
	defer b.upd.mu.Unlock()
	return b.upd.cancel != nil || b.upd.view.Stage == "restarting"
}

// SetCheckUpdates sets the "Check for updates" switch and saves it.
func (b *Bridge) SetCheckUpdates(on bool) {
	b.mu.Lock()
	b.cfg.Update.Check = on
	b.mu.Unlock()
	b.save()
	b.setUpd(func(*UpdateView) {})
}

// InstallUpdate downloads, verifies and installs the latest release in
// the background ("update" events show the progress), then closes the
// app and starts the new version. It is refused during a session (offer
// it again after Leave) and while a disc is being patched.
func (b *Bridge) InstallUpdate() (err error) {
	defer b.maskErr(&err)
	v := b.UpdateStatus()
	switch {
	case v.Dev:
		return errors.New("this is a development build: it doesn't update itself")
	case !v.CanInstall:
		return errors.New("this copy of the app can't update itself: download the new version from the releases page")
	case b.running():
		return errors.New("updates wait until the session is over: press Leave first")
	case b.patching():
		return errors.New("a disc is being patched: update once it is done")
	}
	exe, err := b.updSeam.exe()
	if err != nil {
		return fmt.Errorf("can't find this app's file: %w", err)
	}
	b.upd.mu.Lock()
	if b.upd.cancel != nil || b.upd.view.Stage == "restarting" {
		b.upd.mu.Unlock()
		return errors.New("the update is already running")
	}
	if b.upd.view.Stage == "ready" {
		b.upd.mu.Unlock()
		return errors.New("the update is installed: the app restarts on it after you Leave, or press Restart")
	}
	ctx, cancel := context.WithCancel(b.ctx)
	b.upd.cancel = cancel
	b.upd.done = make(chan struct{})
	done := b.upd.done
	b.upd.mu.Unlock()
	b.setUpd(func(v *UpdateView) { v.Stage, v.Percent, v.Error = "downloading", 0, "" })
	go b.runInstall(ctx, exe, done)
	return nil
}

func (b *Bridge) runInstall(ctx context.Context, exe string, done chan struct{}) {
	defer close(done)
	finish := func(f func(v *UpdateView)) {
		b.upd.mu.Lock()
		if b.upd.cancel != nil {
			b.upd.cancel()
			b.upd.cancel = nil
		}
		b.upd.mu.Unlock()
		b.setUpd(f)
	}
	rel, err := b.updSeam.client.Latest(ctx)
	if err == nil && !update.Offer(b.version, rel) {
		err = fmt.Errorf("%s is already the newest version", b.version)
	}
	if err != nil {
		finish(func(v *UpdateView) { v.Stage, v.Error = "failed", updateError(ctx, err) })
		return
	}
	b.setUpd(func(v *UpdateView) { v.Latest = rel.Tag; v.NotesURL = rel.HTMLURL })
	last := -1
	progress := func(done, total int64) {
		if total <= 0 {
			return
		}
		p := int(done * 100 / total)
		if p >= 100 {
			p = 99 // 100 once it is installed
		}
		if p != last {
			last = p
			b.setUpd(func(v *UpdateView) { v.Percent = p })
		}
	}
	err = b.updSeam.install(ctx, b.updSeam.client, rel, b.updSeam.goos, exe, progress)
	if err != nil {
		finish(func(v *UpdateView) { v.Stage, v.Error = "failed", updateError(ctx, err) })
		return
	}
	// In place: the next start is the new version's first.
	b.mu.Lock()
	b.cfg.Update.Installed = rel.Tag
	b.cfg.Update.Latest = rel.Tag
	b.mu.Unlock()
	b.save()
	b.updSeam.quitMu.Lock()
	quit := b.updSeam.quit
	b.updSeam.quitMu.Unlock()
	b.upd.mu.Lock()
	if quit != nil {
		b.upd.relaunch = update.RelaunchCmd(b.updSeam.goos, exe, b.updSeam.args)
	}
	b.upd.mu.Unlock()
	if quit == nil {
		finish(func(v *UpdateView) { v.Stage, v.Percent = "done", 100 })
		return
	}
	// A session or a patch that started meanwhile isn't cut short: the
	// app restarts once it is over (restartIfReady).
	if b.running() || b.patching() {
		finish(func(v *UpdateView) { v.Stage, v.Percent = "ready", 100 })
		return
	}
	finish(func(v *UpdateView) { v.Stage, v.Percent = "restarting", 100 })
	quit()
}

// restartIfReady closes the app for the installed update ("ready") once
// no session runs and no disc is being patched: after Leave, a patch's
// end, or Restart.
func (b *Bridge) restartIfReady() {
	if b.upd.closing.Load() || b.running() || b.patching() {
		return
	}
	b.updSeam.quitMu.Lock()
	quit := b.updSeam.quit
	b.updSeam.quitMu.Unlock()
	b.upd.mu.Lock()
	ready := b.upd.view.Stage == "ready" && b.upd.relaunch != nil && quit != nil
	b.upd.mu.Unlock()
	if !ready {
		return
	}
	b.setUpd(func(v *UpdateView) { v.Stage = "restarting" })
	quit()
}

// RestartUpdate is the "ready" banner's Restart: it closes the app and
// starts the installed version, unless a session runs or a disc is being
// patched.
func (b *Bridge) RestartUpdate() (err error) {
	defer b.maskErr(&err)
	switch {
	case b.running():
		return errors.New("the app restarts on the new version after you Leave")
	case b.patching():
		return errors.New("a disc is being patched: the app restarts once it is done")
	}
	b.restartIfReady()
	return nil
}

// closeVetoed is a close the player declined ("Stay", "Keep patching"):
// an update's restart waits again ("ready") instead of showing
// "restarting" for good.
func (b *Bridge) closeVetoed() {
	b.upd.mu.Lock()
	restarting := b.upd.view.Stage == "restarting"
	b.upd.mu.Unlock()
	if restarting {
		b.setUpd(func(v *UpdateView) {
			if v.Stage == "restarting" {
				v.Stage = "ready"
			}
		})
	}
}

// updateError is an install's failure in the banner's words.
func updateError(ctx context.Context, err error) string {
	switch {
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		return "Update cancelled. Nothing was changed."
	case errors.Is(err, update.ErrHashMismatch):
		return "The download didn't match the release's checksum (SHA256SUMS), so it was deleted and nothing was changed. Try again later, or download the new version from the releases page."
	case errors.Is(err, update.ErrTooLarge):
		return "The download was larger than an update can be, so nothing was changed. Download the new version from the releases page."
	case errors.Is(err, update.ErrNoAsset):
		return "This release has no download for this computer yet. Try again later, or see the releases page."
	case errors.Is(err, update.ErrInsecureURL):
		return "The download wasn't offered over HTTPS, so it was refused and nothing was changed."
	case errors.Is(err, update.ErrUnsupported):
		return "This copy of the app can't update itself: download the new version from the releases page."
	case errors.Is(err, update.ErrBadBuild):
		return "The new version didn't start on this computer (your antivirus may have blocked it), so this one was kept. Try again later, or download it from the releases page."
	}
	if w, ok := netWords(err); ok {
		return w + " Nothing was changed."
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "update: ")
	return "The update didn't finish: " + msg + ". Nothing was changed."
}

// CancelUpdate stops a running download (a swap in progress finishes).
func (b *Bridge) CancelUpdate() {
	b.upd.mu.Lock()
	c := b.upd.cancel
	b.upd.mu.Unlock()
	if c != nil {
		c()
	}
}

// waitUpdate waits for a running install to end.
func (b *Bridge) waitUpdate() {
	b.upd.mu.Lock()
	d := b.upd.done
	b.upd.mu.Unlock()
	if d != nil {
		<-d
	}
}

// OpenReleaseNotes opens the latest release's page in the user's browser
// (the window's; a browser page opens it itself).
func (b *Bridge) OpenReleaseNotes() (err error) {
	defer b.maskErr(&err)
	b.clipMu.Lock()
	f := b.openURL
	b.clipMu.Unlock()
	if f == nil {
		return errors.New("this window can't open a browser")
	}
	u := b.UpdateStatus().NotesURL
	if u == "" || update.SecureURL(u) != nil {
		u = DownloadsURL
	}
	return f(u)
}

// Repatch is an update's re-patch (UpdateView.Repatch "repatch"): the
// saved vanilla disc patched again with this version, as Setup's game
// step does it (StartPatch; "patch" events show the progress).
func (b *Bridge) Repatch() (err error) {
	defer b.maskErr(&err)
	eff, _ := b.effective()
	if eff.ISO.Vanilla == "" || !isFile(eff.ISO.Vanilla) {
		return errors.New("your vanilla disc isn't where Setup saved it: choose it again")
	}
	// A Dolphin the old version started may still have the patched disc
	// open (Windows then can't replace it): the next start asks again.
	if len(b.procs()) > 0 {
		return errors.New("close Dolphin first: it may have your patched disc open. Then patch your disc again here, or reopen the app")
	}
	if err := b.StartPatch(PatchRequest{Path: eff.ISO.Vanilla}); err != nil {
		return err
	}
	b.upd.mu.Lock()
	b.upd.plan = "" // started: a reload doesn't start it twice
	b.upd.mu.Unlock()
	return nil
}

// relaunchNow starts the new version an update installed, once, after
// the app closed (the end of Run, BrowserServer.Close).
func (b *Bridge) relaunchNow() {
	b.upd.launched.Do(func() {
		b.upd.mu.Lock()
		cmd := b.upd.relaunch
		b.upd.mu.Unlock()
		if cmd == nil {
			return
		}
		b.updSeam.quitMu.Lock()
		stdio := b.updSeam.stdio
		b.updSeam.quitMu.Unlock()
		if stdio {
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		}
		err := b.updSeam.start(cmd)
		if err == nil {
			return
		}
		msg := fmt.Sprintf("The update is installed, but the new version didn't start (%v). Open the app again.", err)
		// The new exe is gone (antivirus removed it): put the old one back
		// and start that.
		if b.updSeam.goos == "windows" && b.updSeam.restore != nil && b.updSeam.restore(cmd.Path) {
			again := exec.Command(cmd.Path, cmd.Args[1:]...)
			again.Dir, again.Stdout, again.Stderr = cmd.Dir, cmd.Stdout, cmd.Stderr
			if b.updSeam.start(again) == nil {
				msg = fmt.Sprintf("The new version didn't start (%v), so the previous one was put back and opened. Your antivirus may have removed the new one.", err)
			}
		}
		fmt.Fprintln(os.Stderr, msg)
		if !stdio && b.updSeam.alert != nil {
			b.updSeam.alert(Title, msg)
		}
	})
}

// UpdateCheckEnabled is the app's "Check for updates" switch (config
// update.check) for the TUI's notice: package main reads no config.
func UpdateCheckEnabled() bool {
	p, err := config.Path()
	if err != nil {
		return true
	}
	c, _ := config.Load(p)
	return c.Update.Check
}
