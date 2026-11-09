package gui

import (
	"context"
	"errors"
	"os"
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
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// discFile writes a fake disc image whose first bytes say what it is to
// contentInspect ("vanilla", "old": another version's, "patched": this
// build's, as fakePatcher writes), sized like a disc (sparse) so the
// vanilla search takes it.
func discFile(t *testing.T, p, kind string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(kind), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, vanillaMinSize+1); err != nil {
		t.Fatal(err)
	}
	return p
}

// contentInspect is Bridge.inspect over discFile's images.
func contentInspect(p string) (patch.DiscState, error) {
	f, err := os.Open(p)
	if err != nil {
		return patch.Vanilla, err
	}
	defer f.Close()
	buf := make([]byte, 8)
	n, _ := f.Read(buf)
	s := string(buf[:n])
	switch {
	case strings.HasPrefix(s, "patched"):
		return patch.Patched, nil
	case strings.HasPrefix(s, "old"):
		return patch.OtherVersion, nil
	case strings.HasPrefix(s, "vanilla"):
		return patch.Vanilla, nil
	}
	return patch.Vanilla, errors.New("disc: unexpected game id")
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never happened", what)
}

// discRig is a host's Bridge whose patched disc another version made,
// with the vanilla disc Setup saved next to it, a Dolphin to launch, a
// held fake patcher and the app log recorded.
type discRig struct {
	b       *Bridge
	e       *fakeEngine
	path    string
	fp      *fakePatcher
	log     *lineLog
	dir     string
	userDir string
	vanilla string
	patched string
	procs   atomic.Pointer[[]launch.Process]
}

func newDiscRig(t *testing.T, saveVanilla bool) *discRig {
	t.Helper()
	b, e, path := newTestBridge(t)
	r := &discRig{b: b, e: e, path: path, dir: t.TempDir(), userDir: t.TempDir(), log: &lineLog{}}
	r.vanilla = discFile(t, filepath.Join(r.dir, "Wind Waker.iso"), "vanilla")
	r.patched = discFile(t, filepath.Join(r.dir, "Wind Waker-multiplayer.iso"), "old")
	exe := touch(t, filepath.Join(r.dir, "Dolphin.exe"))
	b.mu.Lock()
	b.cfg.ISO.Patched = r.patched
	if saveVanilla {
		b.cfg.ISO.Vanilla = r.vanilla
	}
	b.cfg.Dolphin.Exe, b.cfg.Dolphin.UserDir, b.cfg.Dolphin.LaunchOnStart = exe, r.userDir, true
	b.mu.Unlock()
	b.inspect = contentInspect
	r.fp = &fakePatcher{hold: make(chan struct{}), started: make(chan struct{})}
	b.patchRun = r.fp.run
	b.alog = r.log
	r.procs.Store(&[]launch.Process{})
	b.procs = func() []launch.Process { return *r.procs.Load() }
	return r
}

// launched is a Dolphin the app launched on the rig's folder and disc.
func (r *discRig) launched(pid uint32) launch.Process {
	return launch.Process{PID: pid, Exe: "Dolphin.exe", ArgsKnown: true, Args: launch.LaunchArgs(r.userDir, r.patched, "")}
}

func (r *discRig) logged(t *testing.T, want ...string) {
	t.Helper()
	r.log.mu.Lock()
	all := strings.Join(r.log.lines, "\n")
	r.log.mu.Unlock()
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("the app log lacks %q:\n%s", w, all)
		}
	}
}

func (r *discRig) host(t *testing.T) {
	t.Helper()
	if err := r.b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	waitLobby(t, r.b)
}

// TestStartRepatchesStaleDiscThenLaunches (card #1172's done-when): a
// disc another version patched, with the vanilla disc Setup saved: Host
// starts its update, Start waits for it (Start's button off, no dialog),
// then starts the session, which launches Dolphin on the new disc.
func TestStartRepatchesStaleDiscThenLaunches(t *testing.T) {
	r := newDiscRig(t, true)
	r.host(t)
	<-r.fp.started // Host's check started the update
	st := r.b.State()
	if st.Disc.State != "patching" || !st.Disc.Blocks || st.Lobby.CanStart {
		t.Fatalf("while patching: disc %+v, canStart %v", st.Disc, st.Lobby.CanStart)
	}
	started := make(chan error, 1)
	go func() { started <- r.b.Start(true) }()
	waitUntil(t, "Start waiting", func() bool { return r.b.State().StartWaiting })
	if cmds := r.e.commandsExceptChip(); len(cmds) != 0 {
		t.Fatalf("Start went on during the patch: %+v", cmds)
	}
	if st := r.b.State(); st.Dialog != nil {
		t.Fatalf("a dialog while the disc updates: %+v", st.Dialog)
	}
	close(r.fp.hold)
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start after the update: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start never went on")
	}
	cmds := r.e.commandsExceptChip()
	if len(cmds) != 1 || cmds[0].Kind != session.LobbyStart {
		t.Fatalf("lobby commands %+v, want one Start", cmds)
	}
	pv := r.b.PatchStatus()
	if r.fp.calls != 1 || pv.In != r.vanilla || pv.Out != r.patched || pv.Out != patch.DefaultOut(r.vanilla) {
		t.Fatalf("%d patches, %+v", r.fp.calls, pv)
	}
	if st := r.b.State(); st.Disc.State != "" || st.StartWaiting || st.Dialog != nil {
		t.Fatalf("after: disc %+v waiting %v dialog %+v", st.Disc, st.StartWaiting, st.Dialog)
	}
	r.logged(t, "disc check (host)", "patch it again from "+r.vanilla, "disc update: patching", "disc update: done")
}

// TestDiscCheckedEveryTime: the check is no one-time decision (the old
// planOnce): a disc that turns stale after a first good check is updated
// at the next Host.
func TestDiscCheckedEveryTime(t *testing.T) {
	r := newDiscRig(t, true)
	if err := os.WriteFile(r.patched, []byte("patched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d := r.b.CheckDisc(); d.State != "" || r.b.patching() {
		t.Fatalf("a current disc: %+v", d)
	}
	discFile(t, r.patched, "old") // another version's again
	r.host(t)
	<-r.fp.started
	close(r.fp.hold)
	waitUntil(t, "the update", func() bool { return r.b.PatchStatus().Stage == "done" })
}

// TestRepatchClosesAppLaunchedDolphinAtTitle: a Dolphin the app launched
// (its command line is Launch's, on this player's folder and disc), with
// Link not in the world, is closed first; then the disc is updated.
func TestRepatchClosesAppLaunchedDolphinAtTitle(t *testing.T) {
	r := newDiscRig(t, true)
	r.procs.Store(&[]launch.Process{r.launched(41)})
	var mu sync.Mutex
	var closed []uint32
	r.b.probe = func(pid uint32) session.Probe {
		return session.Probe{Running: true, GameID: session.GameID, InGame: true, AtTitle: true} // Link exists on the title stage
	}
	r.b.closeProc = func(pid uint32) error {
		mu.Lock()
		closed = append(closed, pid)
		mu.Unlock()
		r.procs.Store(&[]launch.Process{})
		return nil
	}
	r.b.waitGone = func(uint32, time.Duration) bool { return len(*r.procs.Load()) == 0 }
	if d := r.b.CheckDisc(); (d.State != "closing" && d.State != "patching") || !d.Blocks {
		t.Fatalf("check with the app's Dolphin at the title: %+v", d)
	}
	<-r.fp.started
	mu.Lock()
	got := slices.Clone(closed)
	mu.Unlock()
	if !slices.Equal(got, []uint32{41}) {
		t.Fatalf("closed %v before patching, want [41]", got)
	}
	close(r.fp.hold)
	waitUntil(t, "the update", func() bool { return r.b.discStatus().State == "" && !r.b.patching() })
	r.logged(t, "closing Dolphin pid 41", "disc update: done")
}

// TestRepatchWaitsForAnotherDolphin: a Dolphin holding the disc that the
// app may not close (the player started it; the app's, but Link is in the
// world; one whose command line can't be read) is never closed: the line
// says "Close Dolphin", Start is off and refused, and the update starts by
// itself once it closes.
func TestRepatchWaitsForAnotherDolphin(t *testing.T) {
	atTitle := session.Probe{Running: true, GameID: session.GameID, AtTitle: true}
	for _, c := range []struct {
		name  string
		proc  func(r *discRig) launch.Process
		probe session.Probe
	}{
		{"the player's own", func(r *discRig) launch.Process {
			return launch.Process{PID: 51, ArgsKnown: true, Args: []string{"-u", r.userDir}}
		}, atTitle},
		{"the app's, in game", func(r *discRig) launch.Process { return r.launched(52) },
			session.Probe{Running: true, GameID: session.GameID, InGame: true}},
		// Link's pointer is null during a stage load: still a save in play.
		{"the app's, loading a stage", func(r *discRig) launch.Process { return r.launched(54) },
			session.Probe{Running: true, GameID: session.GameID}},
		{"the app's, memory unreadable", func(r *discRig) launch.Process { return r.launched(55) },
			session.Probe{Running: true}},
		{"unreadable command line", func(r *discRig) launch.Process { return launch.Process{PID: 53} }, atTitle},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newDiscRig(t, true)
			r.procs.Store(&[]launch.Process{c.proc(r)})
			r.b.probe = func(uint32) session.Probe { return c.probe }
			r.b.closeProc = func(pid uint32) error { t.Errorf("closed pid %d", pid); return nil }
			r.host(t)
			st := r.b.State()
			if st.Disc.State != "blocked" || !strings.Contains(st.Disc.Title, "Close Dolphin") || st.Lobby.CanStart {
				t.Fatalf("held: disc %+v canStart %v", st.Disc, st.Lobby.CanStart)
			}
			if err := r.b.Start(true); !errors.Is(err, errDiscHeld) {
				t.Fatalf("Start while held: %v", err)
			}
			if r.b.patching() || len(r.e.commands()) != 0 {
				t.Fatal("patched or started while held")
			}
			r.procs.Store(&[]launch.Process{})
			<-r.fp.started // the update starts by itself
			close(r.fp.hold)
			waitUntil(t, "the update", func() bool { return r.b.discStatus().State == "" && !r.b.patching() })
			if st := r.b.State(); !st.Lobby.CanStart {
				t.Fatalf("Start still off: %+v", st.Disc)
			}
			r.logged(t, "waiting: Dolphin", "disc update: done")
		})
	}
}

// TestNotPlaying: only a positive read of no save in play lets the app
// close a Dolphin it launched.
func TestNotPlaying(t *testing.T) {
	for _, c := range []struct {
		p    session.Probe
		want bool
	}{
		{session.Probe{}, true},               // gone
		{session.Probe{Running: true}, false}, // no game found: no sure read
		{session.Probe{Running: true, GameID: session.GameID, AtTitle: true, InGame: true}, true},
		{session.Probe{Running: true, GameID: session.GameID}, false},               // a stage load, file select, a cutscene
		{session.Probe{Running: true, GameID: session.GameID, InGame: true}, false}, // in the world
		{session.Probe{Running: true, GameID: "GALE01", AtTitle: true}, false},      // another game
	} {
		if got := notPlaying(c.p); got != c.want {
			t.Errorf("%+v: %v", c.p, got)
		}
	}
}

// TestRepatchIgnoresUnrelatedDolphin: a Dolphin on another folder with
// another disc (a second player on this computer) doesn't stop the
// update (Repatch refused for any Dolphin before #1172).
func TestRepatchIgnoresUnrelatedDolphin(t *testing.T) {
	r := newDiscRig(t, true)
	other := t.TempDir()
	r.procs.Store(&[]launch.Process{{PID: 61, ArgsKnown: true, Args: launch.LaunchArgs(other, filepath.Join(other, "theirs.iso"), "")}})
	if d := r.b.CheckDisc(); d.State != "patching" {
		t.Fatalf("%+v", d)
	}
	<-r.fp.started
	close(r.fp.hold)
	waitPatchDone(t, r.b)
}

// TestStaleDiscFindsVanillaNearby: with no vanilla disc saved, the one
// the patched disc was made from (by its name) is found and patched, and
// saved as the vanilla disc; with none anywhere the line asks for it
// ("pick") and Start keeps PR #328's refusal.
func TestStaleDiscFindsVanillaNearby(t *testing.T) {
	r := newDiscRig(t, false)
	if d := r.b.CheckDisc(); d.State != "patching" {
		t.Fatalf("%+v", d)
	}
	<-r.fp.started
	close(r.fp.hold)
	if pv := waitPatchDone(t, r.b); pv.In != r.vanilla || pv.Out != r.patched {
		t.Fatalf("%+v", pv)
	}
	if c := loadCfg(t, r.path); c.ISO.Vanilla != r.vanilla {
		t.Fatalf("the found disc wasn't saved: %q", c.ISO.Vanilla)
	}
	r.logged(t, "(found by the search)")

	// Nothing to find: "pick", and Start refuses as PR #328 does.
	r2 := newDiscRig(t, false)
	os.Remove(r2.vanilla)
	if d := r2.b.CheckDisc(); d.State != "pick" || d.Blocks {
		t.Fatalf("no vanilla anywhere: %+v", d)
	}
	r2.host(t)
	if err := r2.b.Start(true); !errors.Is(err, ErrStaleDisc) {
		t.Fatalf("Start with a stale disc and no vanilla: %v", err)
	}
	// The line's Choose takes the update's way: a Dolphin the player
	// started holds the disc, so it waits, with the chosen disc saved;
	// once Dolphin closes the update runs from it.
	chosen := discFile(t, filepath.Join(t.TempDir(), "Wind Waker.iso"), "vanilla")
	r2.procs.Store(&[]launch.Process{{PID: 71, ArgsKnown: true, Args: []string{"-u", r2.userDir}}})
	if err := r2.b.StartPatch(PatchRequest{Path: chosen}); err != nil {
		t.Fatal(err)
	}
	if d := r2.b.discStatus(); d.State != "blocked" {
		t.Fatalf("chosen while Dolphin holds the disc: %+v", d)
	}
	if c := loadCfg(t, r2.path); c.ISO.Vanilla != chosen {
		t.Fatalf("the chosen disc wasn't saved: %q", c.ISO.Vanilla)
	}
	r2.procs.Store(&[]launch.Process{})
	<-r2.fp.started
	close(r2.fp.hold)
	if pv := waitPatchDone(t, r2.b); pv.In != chosen || pv.Stage != "done" {
		t.Fatalf("%+v", pv)
	}
}

// TestFindVanilla: the search's order, filters and limits.
func TestFindVanilla(t *testing.T) {
	b, _, _ := newTestBridge(t)
	b.inspect = contentInspect
	dir, games := t.TempDir(), t.TempDir()
	patched := discFile(t, filepath.Join(dir, "WW-multiplayer.iso"), "old")
	c := config.Defaults()
	c.ISO.Patched = patched

	// Skipped: another version's disc, a small file, a temp file, another
	// extension, another patched output.
	discFile(t, filepath.Join(dir, "a.iso"), "old")
	os.WriteFile(filepath.Join(dir, "b.iso"), []byte("vanilla"), 0o644)
	discFile(t, filepath.Join(dir, "c.iso.1234.tmp"), "vanilla")
	discFile(t, filepath.Join(dir, "d.wbfs"), "vanilla")
	discFile(t, filepath.Join(dir, "e-multiplayer.iso"), "vanilla")
	if p, ok := b.findVanilla(c); ok {
		t.Fatalf("found %s", p)
	}
	// A miss isn't searched again for the same discs...
	want := discFile(t, filepath.Join(dir, "zelda.gcm"), "vanilla")
	if p, ok := b.findVanilla(c); ok {
		t.Fatalf("searched again: %s", p)
	}
	// ...but is once they change.
	c.ISO.Vanilla = filepath.Join(t.TempDir(), "moved.iso")
	if p, ok := b.findVanilla(c); !ok || p != want {
		t.Fatalf("got %q %v, want %s", p, ok, want)
	}

	// Dolphin's game folders, one level down when it searches subfolders.
	os.Remove(want)
	sub := discFile(t, filepath.Join(games, "GameCube", "Wind Waker (USA).iso"), "vanilla")
	b.isoDirs = func(string) ([]string, bool) { return []string{games}, true }
	c.ISO.Vanilla = filepath.Join(t.TempDir(), "gone.iso")
	if p, ok := b.findVanilla(c); !ok || p != sub {
		t.Fatalf("got %q %v, want %s", p, ok, sub)
	}
}

// TestDiscAppLogFile: the app log is opened on the first line worth
// writing (an all-good check writes no file), named -app.log.
func TestDiscAppLogFile(t *testing.T) {
	r := newDiscRig(t, false)
	os.Remove(r.vanilla)
	r.b.alog = nil
	logs := t.TempDir()
	r.b.disc.logDir = func() (string, error) { return logs, nil }
	if err := os.WriteFile(r.patched, []byte("patched"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.b.CheckDisc()
	if es, _ := os.ReadDir(logs); len(es) != 0 {
		t.Fatalf("a good disc wrote a log: %v", es)
	}
	discFile(t, r.patched, "old")
	r.b.CheckDisc()
	es, _ := os.ReadDir(logs)
	if len(es) != 1 || !strings.HasSuffix(es[0].Name(), "-app.log") {
		t.Fatalf("logs %v", es)
	}
	data, _ := os.ReadFile(filepath.Join(logs, es[0].Name()))
	if !strings.Contains(string(data), "app log") || !strings.Contains(string(data), "disc check (app opened)") || !strings.Contains(string(data), "ask for it") {
		t.Fatalf("log:\n%s", data)
	}
}

// TestDiscUpdateShowsOnLobbyRowAndLaunchWaits (card #1176): while a
// player's disc is updated, their lobby row says "updating" (the Dolphin
// chip), and the game side's last check before a launch (prepareLaunch)
// waits for the update instead of launching on the old disc. The row goes
// back to the Dolphin's own state when the update ends.
func TestDiscUpdateShowsOnLobbyRowAndLaunchWaits(t *testing.T) {
	r := newDiscRig(t, true)
	r.host(t)
	<-r.fp.started
	dolphinCmds := func() []string {
		var s []string
		for _, c := range r.e.commands() {
			if c.Kind == session.LobbySetDolphin {
				s = append(s, c.Text)
			}
		}
		return s
	}
	waitUntil(t, "the updating chip", func() bool { return slices.Equal(dolphinCmds(), []string{"updating"}) })
	prep := make(chan error, 1)
	go func() { prep <- r.b.prepareLaunch(r.b.ctx) }()
	select {
	case err := <-prep:
		t.Fatalf("prepareLaunch went on during the update: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(r.fp.hold)
	select {
	case err := <-prep:
		if err != nil {
			t.Fatalf("prepareLaunch after the update: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prepareLaunch never went on")
	}
	waitUntil(t, "the chip back", func() bool { return len(dolphinCmds()) == 2 })
	if got := dolphinCmds(); got[1] == "updating" {
		t.Fatalf("chip stayed %v", got)
	}
	if r.fp.calls != 1 {
		t.Fatalf("%d patches", r.fp.calls)
	}
}

// A launch that is canceled (Leave) while the disc updates returns at once.
func TestPrepareLaunchCanceled(t *testing.T) {
	r := newDiscRig(t, true)
	r.host(t)
	<-r.fp.started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.b.prepareLaunch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	close(r.fp.hold)
}

// commandsExceptChip are the lobby commands but the Dolphin chip's.
func (e *fakeEngine) commandsExceptChip() []session.LobbyCommand {
	var out []session.LobbyCommand
	for _, c := range e.commands() {
		if c.Kind != session.LobbySetDolphin {
			out = append(out, c)
		}
	}
	return out
}
