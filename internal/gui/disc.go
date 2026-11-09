package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The disc stays this build's (docs/coop-just-works.md §3, card #1172).
// The patched disc is checked (Bridge.inspect: the DOL and RELS.arc,
// patch.InspectFull) when the window opens (CheckDisc), at Host and Join,
// and before any Dolphin launch (Start, LaunchDolphin), every time. One
// another version patched is patched again by itself (ensureDisc) from the
// vanilla disc Setup saved, or, when that is gone, one found next to the
// patched disc or in Dolphin's game folders (findVanilla), into the same
// file as always (patch.DefaultOut). The Play page shows one line for it
// (DiscView, "disc" events); Setup never opens. A Start pressed meanwhile
// waits for the update, then starts (discReady).
//
// Only a Dolphin holding the disc stops it. The app closes one only when
// it launched it (this run's session says so, or its command line is the
// one Launch builds, on this player's folder and disc) and Link isn't in
// the world (ProbeDolphin, asked several times: a scene's reload reads
// "not in the world" for a moment). Any other Dolphin gets the line "Close
// Dolphin to update your disc", Start is refused, and the update starts by
// itself once it closes (watchHolders). PR #328's gate (checkDiscVersion)
// stays the last refusal when the disc could not be updated.
//
// Every check that finds something to do, every Dolphin closed and every
// update's result go to the app's own log (AppLogDir, applog "-app.log"),
// opened on the first such line.

// DiscView is the Play page's disc line.
type DiscView struct {
	// Seq grows with every change (events and replies may cross).
	Seq int `json:"seq"`
	// State is "" (nothing to say: the disc is this build's, or there is
	// none yet, which is Setup's business), "patching" (being updated),
	// "closing" (closing a Dolphin the app launched first), "blocked" (a
	// Dolphin has the disc open), "pick" (no vanilla disc to patch from)
	// or "failed" (the update didn't finish: Error).
	State string `json:"state"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
	// Version is this build's, the version the disc is updated for.
	Version string `json:"version"`
	// Percent is the update's progress (the patch's bar) while patching.
	Percent int `json:"percent"`
	// Started is when the update began (RFC 3339, UTC): the page counts
	// its seconds.
	Started string `json:"started,omitempty"`
	// Blocks says the host's Start can't go now (it waits, or is refused).
	Blocks bool `json:"blocks"`
}

// discState is the Bridge's disc update.
type discState struct {
	ensure sync.Mutex // one ensureDisc at a time

	mu    sync.Mutex
	view  DiscView
	busy  chan struct{} // a close-then-patch running: closed when it ends
	watch bool          // watchHolders runs
	miss  string        // the config (patched|vanilla) findVanilla found nothing for
	seq   int

	// starting is a Start (or Launch) waiting on the disc: one at a time.
	starting atomic.Bool

	logMu  sync.Mutex
	logDir func() (string, error) // nil: no app log
}

// discInfo is one check of the patched disc.
type discInfo struct {
	// Plan is discPlan's: "" (nothing to do), "keep" (an update's first
	// start, the disc is this build's), "repatch" or "pick".
	Plan    string
	Patched string
	State   patch.DiscState
	Err     error
	// Vanilla is the disc a "repatch" patches; Found says the search
	// found it (Setup saved another, or none).
	Vanilla string
	Found   bool
}

// errDiscHeld refuses a Start while a Dolphin the app may not close has
// the disc open that is being updated.
var errDiscHeld = errors.New("close Dolphin to update your disc: it has your patched disc open, and this version needs it updated. The update starts by itself once Dolphin closes; then press Start")

// probeSamples is how many looks at a Dolphin must all say Link isn't in
// the world before the app closes it, probeGap apart: the reload between
// scenes and world-map cutscenes read "not in the world" for a moment.
const (
	probeSamples = 3
	probeGap     = 400 * time.Millisecond
)

// closeWait is how long a closed Dolphin may take to leave.
const closeWait = 10 * time.Second

// discCheck checks the patched disc c says the player boots, and, for
// one to patch again with no vanilla disc where Setup saved it, looks
// for one (findVanilla).
func (b *Bridge) discCheck() discInfo {
	c, _ := b.effective()
	in := discInfo{Patched: c.ISO.Patched}
	if c.ISO.Patched == "" {
		return in
	}
	if isFile(c.ISO.Patched) {
		in.State, in.Err = b.inspect(c.ISO.Patched)
	} else {
		in.Err = os.ErrNotExist
	}
	after := b.upd.installed != ""
	if !after && !isFile(c.ISO.Patched) {
		return in // missing: Setup's business
	}
	in.Plan = discPlanOf(c, in.State, in.Err, isFile, after)
	switch in.Plan {
	case "repatch":
		in.Vanilla = c.ISO.Vanilla
	case "pick":
		if v, ok := b.findVanilla(c); ok {
			in.Plan, in.Vanilla, in.Found = "repatch", v, true
		}
	}
	return in
}

// setPlan records the last check's plan (UpdateView.Repatch, Setup).
func (b *Bridge) setPlan(plan string) {
	b.upd.mu.Lock()
	b.upd.plan = plan
	b.upd.mu.Unlock()
}

func (b *Bridge) lastPlan() string {
	b.upd.mu.Lock()
	defer b.upd.mu.Unlock()
	return b.upd.plan
}

// CheckDisc is the window's check when it opens: the disc is checked, and
// updated if it needs it (ensureDisc). It returns the disc line.
func (b *Bridge) CheckDisc() DiscView {
	b.ensureDisc("app opened")
	return b.DiscStatus()
}

// DiscStatus returns the disc line, with the update's progress.
func (b *Bridge) DiscStatus() DiscView {
	b.disc.mu.Lock()
	v := b.disc.view
	b.disc.mu.Unlock()
	if v.State == "patching" {
		if j := b.runningJob(); j != nil {
			v.Percent = j.snapshot().Percent
		}
	}
	return v
}

// discBlocks says the host's Start can't go now: the disc is being
// updated, a Dolphin holds it, or a Start already waits for it.
func (b *Bridge) discBlocks() bool {
	if b.disc.starting.Load() {
		return true
	}
	b.disc.mu.Lock()
	defer b.disc.mu.Unlock()
	return b.disc.view.Blocks
}

// setDisc replaces the disc line and sends it as a "disc" event.
func (b *Bridge) setDisc(v DiscView) {
	v.Version = b.version
	switch v.State {
	case "patching", "closing", "blocked":
		v.Blocks = true
	}
	b.disc.mu.Lock()
	b.disc.seq++
	v.Seq = b.disc.seq
	b.disc.view = v
	b.disc.mu.Unlock()
	b.emitDisc()
}

// emitState asks the pages to read the state again (Start's button).
func (b *Bridge) emitState() {
	b.emit(EventView{Kind: "state", Time: b.now().UTC().Format(time.RFC3339Nano)})
}

func (b *Bridge) emitDisc() {
	v := b.DiscStatus()
	b.emit(EventView{Kind: "disc", Time: b.now().UTC().Format(time.RFC3339Nano), Disc: &v})
}

// runningJob is the running patch, or nil.
func (b *Bridge) runningJob() *patchJob {
	b.jobMu.Lock()
	j := b.job
	b.jobMu.Unlock()
	if j == nil || !j.snapshot().Running {
		return nil
	}
	return j
}

// ensureDisc checks the disc (why names the moment, for the log) and,
// when another version patched it, starts its update: at once, after
// closing the Dolphins holding it that the app may close, or, when another
// holds it, once that closes (watchHolders). It returns what to wait on
// for the update to end (a patch, or the close and the patch after it),
// or nil when nothing runs: the disc needs nothing, can't be patched
// ("pick"), or is held.
func (b *Bridge) ensureDisc(why string) <-chan struct{} {
	b.disc.ensure.Lock()
	defer b.disc.ensure.Unlock()
	if b.updating() {
		return nil // the new version checks it when it opens
	}
	if j := b.runningJob(); j != nil {
		return j.done
	}
	b.disc.mu.Lock()
	busy := b.disc.busy
	b.disc.mu.Unlock()
	if busy != nil {
		return busy
	}
	in := b.discCheck()
	b.setPlan(in.Plan)
	b.logPlan(why, in)
	switch in.Plan {
	case "repatch":
		return b.repatch(in)
	case "pick":
		b.setDisc(DiscView{State: "pick", Title: "Choose your Wind Waker disc.",
			Text: "Your patched disc was made by another version of this app, and the vanilla disc it came from isn't where it was. Choose it, and the app patches it with this version."})
	default:
		b.setDisc(DiscView{})
	}
	return nil
}

// holder is a running Dolphin that may have the patched disc open.
type holder struct {
	p launch.Process
	// launched says the app launched it.
	launched bool
}

// discHolders are the running Dolphins that may have the patched disc
// open: one booted with it (-e), one on this player's Dolphin folder, and
// any whose command line can't be read. A Dolphin on another folder with
// another disc is someone else's (a second player on this computer).
func (b *Bridge) discHolders() []holder {
	o := b.launchOptions()
	st := b.ctrl.Snapshot().Dolphin
	env := b.denv()
	var hs []holder
	for _, p := range b.procs() {
		h := holder{p: p, launched: st.Launched && st.PID == p.PID && p.PID != 0}
		if !p.ArgsKnown {
			hs = append(hs, h)
			continue
		}
		iso, isoOK := launch.ISOArg(p.Args)
		dir, dirOK := launch.ProcessUserDir(env, p)
		held := (isoOK && o.ISO != "" && samePath(iso, o.ISO)) || (dirOK && o.UserDir != "" && samePath(dir, o.UserDir))
		if !held {
			continue
		}
		if !h.launched && launch.HasLaunchArgs(p.Args) {
			u, _ := launch.UserDirArg(p.Args)
			h.launched = o.UserDir != "" && samePath(u, o.UserDir) && isoOK && o.ISO != "" && samePath(iso, o.ISO)
		}
		hs = append(hs, h)
	}
	return hs
}

// closable says the app may close h: it launched it, and Link isn't in
// the world, in every one of probeSamples looks.
func (b *Bridge) closable(h holder) bool {
	if !h.launched || b.probe == nil {
		return false
	}
	for i := 0; i < probeSamples; i++ {
		if i > 0 {
			time.Sleep(probeGap)
		}
		p := b.probe(h.p.PID)
		if p.Running && p.InGame {
			return false
		}
	}
	return true
}

// repatch starts the update of a disc to patch again (ensureDisc holds
// the ensure lock).
func (b *Bridge) repatch(in discInfo) <-chan struct{} {
	hs := b.discHolders()
	if len(hs) == 0 {
		return b.startRepatch(in)
	}
	for _, h := range hs {
		if !b.closable(h) {
			b.held(hs)
			return nil
		}
	}
	busy := make(chan struct{})
	b.disc.mu.Lock()
	b.disc.busy = busy
	b.disc.mu.Unlock()
	b.setDisc(DiscView{State: "closing", Title: "Closing Dolphin to update your disc…",
		Text: "The app started that Dolphin and it isn't in the game. Your disc is updated for this version next."})
	go func() {
		defer func() {
			b.disc.mu.Lock()
			b.disc.busy = nil
			b.disc.mu.Unlock()
			close(busy)
		}()
		var left []holder
		for _, h := range hs {
			pid := h.p.PID
			b.logDisc(log.Info, "disc update: closing Dolphin pid %d (the app launched it; Link isn't in the world)", pid)
			if err := b.closeProc(pid); err != nil {
				b.logDisc(log.Warn, "disc update: couldn't close Dolphin pid %d: %v", pid, err)
			}
			if !b.waitGone(pid, closeWait) {
				left = append(left, h)
			}
		}
		if len(left) > 0 {
			b.held(left)
			return
		}
		if d := b.startRepatch(in); d != nil {
			<-d
		}
	}()
	return busy
}

// held shows the "Close Dolphin" line for hs and starts watching for
// them to close.
func (b *Bridge) held(hs []holder) {
	pids := make([]string, len(hs))
	for i, h := range hs {
		pids[i] = fmt.Sprint(h.p.PID)
	}
	b.logDisc(log.Warn, "disc update: waiting: Dolphin (pid %s) may have the disc open", strings.Join(pids, ", "))
	b.setDisc(DiscView{State: "blocked", Title: "Close Dolphin to update your disc.",
		Text: "It has your patched disc open, and this version needs it updated. The update starts by itself once Dolphin closes; Start waits until then."})
	b.watchHolders()
}

// watchHolders waits for the Dolphins holding the disc to close, then
// checks it again (which starts its update). It stops once the line is no
// longer "blocked" (the disc changed some other way) or the app closes.
func (b *Bridge) watchHolders() {
	b.disc.mu.Lock()
	if b.disc.watch {
		b.disc.mu.Unlock()
		return
	}
	b.disc.watch = true
	b.disc.mu.Unlock()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		stop := func() {
			b.disc.mu.Lock()
			b.disc.watch = false
			b.disc.mu.Unlock()
		}
		for {
			select {
			case <-b.ctx.Done():
				stop()
				return
			case <-t.C:
			}
			b.disc.mu.Lock()
			blocked := b.disc.view.State == "blocked"
			b.disc.mu.Unlock()
			if !blocked {
				stop()
				return
			}
			if len(b.discHolders()) == 0 {
				stop()
				b.ensureDisc("Dolphin closed")
				return
			}
		}
	}()
}

// startRepatch starts the update's patch from in.Vanilla.
func (b *Bridge) startRepatch(in discInfo) <-chan struct{} {
	j, err := b.startPatch(PatchRequest{Path: in.Vanilla}, true)
	if err != nil {
		b.logDisc(log.Err, "disc update: couldn't start: %v", err)
		b.setDisc(DiscView{State: "failed", Title: "Couldn't update your disc.", Error: err.Error(),
			Text: "Try again, or patch your vanilla disc in Setup."})
		return nil
	}
	from := "the vanilla disc Setup saved"
	if in.Found {
		from = "the vanilla disc found at " + in.Vanilla
	}
	b.logDisc(log.Info, "disc update: patching %s into %s (from %s)", in.Vanilla, patch.DefaultOut(in.Vanilla), from)
	b.setDisc(DiscView{State: "patching", Title: "Updating your disc for " + b.version + "…",
		Text:    "Your patched disc was made by another version of this app, so it is being patched again. Start waits for it.",
		Started: b.now().UTC().Format(time.RFC3339)})
	return j.done
}

// discPatchEnded is the end of patch j (the update's, or one the player
// started in Setup): it logs the update's result and sets the line.
func (b *Bridge) discPatchEnded(j *patchJob) {
	v := j.snapshot()
	switch {
	case v.Stage == "done":
		if j.auto {
			b.logDisc(log.OK, "disc update: done: %s is this version's", v.Image)
		}
		b.setDisc(DiscView{})
	case !j.auto:
	case v.Stage == "canceled":
		b.logDisc(log.Warn, "disc update: stopped (%s)", v.Stopped)
		b.setDisc(DiscView{State: "failed", Title: "Your disc's update was stopped.", Error: "the update was stopped",
			Text: "Try again: the game would run without this version's mod."})
	default:
		b.logDisc(log.Err, "disc update: failed: %s", v.Error)
		b.setDisc(DiscView{State: "failed", Title: "Couldn't update your disc.", Error: v.Error,
			Text: "Try again, or patch your vanilla disc in Setup."})
	}
}

// discReady is what Start and LaunchDolphin wait for: the disc checked
// and, when another version patched it, updated (the wait covers the
// patch, or closing a Dolphin and the patch after it; a patch the player
// started in Setup too). It refuses while a Dolphin holds the disc, and,
// as PR #328 did, when the app would launch Dolphin on a disc still
// another version's.
func (b *Bridge) discReady(why string) error {
	wait := func(ch <-chan struct{}) error {
		if ch == nil {
			return nil
		}
		select {
		case <-ch:
			return nil
		case <-b.ctx.Done():
			return errors.New("the app is closing")
		}
	}
	if err := wait(b.ensureDisc(why)); err != nil {
		return err
	}
	if j := b.runningJob(); j != nil {
		if err := wait(j.done); err != nil {
			return err
		}
	}
	v := b.DiscStatus()
	if v.State == "blocked" {
		return errDiscHeld
	}
	err := b.checkDiscVersion()
	if err != nil && v.State == "failed" {
		return fmt.Errorf("your disc couldn't be updated for this version (%s), so the game would run without its mod. Try again on the Play page, or patch your vanilla disc in Setup", v.Error)
	}
	return err
}

// waitDisc checks the disc and waits for its update or a running patch
// to end (Join), refusing nothing.
func (b *Bridge) waitDisc(why string) error {
	for _, ch := range []<-chan struct{}{b.ensureDisc(why), nil} {
		if ch == nil {
			if j := b.runningJob(); j != nil {
				ch = j.done
			}
		}
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-b.ctx.Done():
			return errors.New("the app is closing")
		}
	}
	return nil
}

// logPlan writes a check's plan to the app log: always when there is
// something to do, else only once the log is open (an all-good start
// writes no file).
func (b *Bridge) logPlan(why string, in discInfo) {
	what := "no patched disc set"
	if in.Patched != "" {
		what = in.Patched + ": " + in.State.String()
		if in.Err != nil {
			what = in.Patched + ": can't read it (" + in.Err.Error() + ")"
		}
	}
	plan := in.Plan
	switch plan {
	case "":
		plan = "nothing to do"
	case "repatch":
		plan = "patch it again from " + in.Vanilla
		if in.Found {
			plan += " (found by the search)"
		}
	case "pick":
		plan = "no vanilla disc to patch from: ask for it"
	}
	msg := fmt.Sprintf("disc check (%s): %s; %s", why, what, plan)
	if in.Plan == "repatch" || in.Plan == "pick" {
		b.logDisc(log.Info, "%s", msg)
		return
	}
	b.logDiscIfOpen(log.Info, msg)
}

// logDisc writes one line to the app log, opening it on the first line
// (b.alog set already, as tests do, is used as it is).
func (b *Bridge) logDisc(level log.Level, format string, args ...any) {
	b.disc.logMu.Lock()
	defer b.disc.logMu.Unlock()
	if b.alog == nil {
		if b.disc.logDir == nil {
			return
		}
		dir, err := b.disc.logDir()
		var f *applog.File
		if err == nil {
			f, err = applog.Open(dir, "app", b.now)
		}
		if err != nil {
			b.disc.logDir = nil // no log this run: never ask again
			return
		}
		f.Log(log.Info, fmt.Sprintf("ww-multiplayer %s app log (%s/%s)", b.version, runtime.GOOS, runtime.GOARCH))
		b.alog = f
	}
	log.Logf(b.alog, level, format, args...)
}

// logDiscIfOpen writes a line only to an app log already open.
func (b *Bridge) logDiscIfOpen(level log.Level, msg string) {
	b.disc.logMu.Lock()
	open := b.alog != nil
	b.disc.logMu.Unlock()
	if open {
		b.logDisc(level, "%s", msg)
	}
}

// closeAppLog closes the app log (the app's end).
func (b *Bridge) closeAppLog() {
	b.disc.logMu.Lock()
	defer b.disc.logMu.Unlock()
	if c, ok := b.alog.(io.Closer); ok {
		c.Close()
	}
}

// samePath says a and b name one file: the same file on disk, else the
// same cleaned path (without case on Windows and macOS).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// The vanilla search's limits.
const (
	vanillaMinSize    = 16 << 20 // a GameCube disc image is far bigger
	vanillaMaxEntries = 256      // per folder
	vanillaMaxInspect = 16       // disc reads in all
	vanillaDeadline   = 5 * time.Second
)

// vanillaExts are the images the search reads: the ones the patcher reads
// directly (a compressed one is patched only once converted, so only the
// one Setup saved is taken).
var vanillaExts = map[string]bool{".iso": true, ".gcm": true, ".ciso": true}

// findVanilla looks for the player's vanilla Wind Waker disc when the
// one Setup saved is gone (or none was): the disc the patched one was made
// from by its name (the inverse of patch.DefaultOut), then the folder
// Setup's vanilla disc was in, the patched disc's folder, and Dolphin's
// game folders (Config/Dolphin.ini ISOPath*, one level of subfolders when
// it searches them). A disc counts when it is GZLE01 and inspects as
// vanilla. A search that found nothing isn't repeated this run for the
// same discs.
func (b *Bridge) findVanilla(c config.Config) (string, bool) {
	key := c.ISO.Patched + "|" + c.ISO.Vanilla
	b.disc.mu.Lock()
	miss := b.disc.miss == key
	b.disc.mu.Unlock()
	if miss {
		return "", false
	}
	ctx, cancel := context.WithTimeout(b.ctx, vanillaDeadline)
	defer cancel()
	s := vanillaSearch{ctx: ctx, inspect: b.inspect, patched: c.ISO.Patched, seen: map[string]bool{}}

	if base, ok := strings.CutSuffix(c.ISO.Patched, "-multiplayer.iso"); ok {
		for _, ext := range []string{".iso", ".gcm", ".ciso"} {
			if s.try(base + ext) {
				return s.found, true
			}
		}
	}
	var dirs []string
	if c.ISO.Vanilla != "" {
		dirs = append(dirs, filepath.Dir(c.ISO.Vanilla))
	}
	dirs = append(dirs, filepath.Dir(c.ISO.Patched))
	for _, d := range dirs {
		if s.dir(d, false) {
			return s.found, true
		}
	}
	if b.isoDirs != nil {
		userDir := b.launchOptions().UserDir
		iso, rec := b.isoDirs(userDir)
		for _, d := range iso {
			if s.dir(d, rec) {
				return s.found, true
			}
		}
	}
	b.disc.mu.Lock()
	b.disc.miss = key
	b.disc.mu.Unlock()
	return "", false
}

// vanillaSearch is one findVanilla.
type vanillaSearch struct {
	ctx     context.Context
	inspect func(string) (patch.DiscState, error)
	patched string
	seen    map[string]bool
	reads   int
	found   string
}

// dir looks in folder d (and its subfolders, one level, when sub).
func (s *vanillaSearch) dir(d string, sub bool) bool {
	if d == "" || d == "." || s.seen["dir:"+d] {
		return false
	}
	s.seen["dir:"+d] = true
	es, err := os.ReadDir(d)
	if err != nil {
		return false
	}
	if len(es) > vanillaMaxEntries {
		es = es[:vanillaMaxEntries]
	}
	var subs []string
	for _, e := range es {
		if s.done() {
			return false
		}
		p := filepath.Join(d, e.Name())
		if e.IsDir() {
			if sub {
				subs = append(subs, p)
			}
			continue
		}
		if s.try(p) {
			return true
		}
	}
	for _, sd := range subs {
		if s.dir(sd, false) {
			return true
		}
	}
	return false
}

func (s *vanillaSearch) done() bool {
	return s.ctx.Err() != nil || s.reads >= vanillaMaxInspect
}

// try takes file p if it is a vanilla Wind Waker disc.
func (s *vanillaSearch) try(p string) bool {
	if s.done() || s.seen[p] {
		return false
	}
	s.seen[p] = true
	name := strings.ToLower(filepath.Base(p))
	if !vanillaExts[filepath.Ext(name)] || strings.HasSuffix(name, "-multiplayer.iso") ||
		strings.Contains(name, ".converting-") || strings.HasSuffix(name, ".tmp") {
		return false
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < vanillaMinSize {
		return false
	}
	if samePath(p, s.patched) {
		return false
	}
	s.reads++
	if st, err := s.inspect(p); err == nil && st == patch.Vanilla {
		s.found = p
		return true
	}
	return false
}

// discPlanOf is discPlan over a disc already inspected (st, err).
func discPlanOf(c config.Config, st patch.DiscState, err error, isFile func(string) bool, afterUpdate bool) string {
	if c.ISO.Patched == "" {
		return ""
	}
	if afterUpdate {
		if err == nil && st == patch.Patched {
			return "keep"
		}
	} else if err != nil || st != patch.OtherVersion {
		return ""
	}
	if c.ISO.Vanilla != "" && isFile(c.ISO.Vanilla) {
		return "repatch"
	}
	return "pick"
}

