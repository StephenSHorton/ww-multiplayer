package coopsession

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind/bindtest"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine/enginetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The engine's fakes (enginetest), as the app's co-op tests use them.
var (
	waitSnap = enginetest.WaitSnap
	waitFor  = enginetest.WaitFor
	hasEvent = enginetest.HasEvent
	freePort = enginetest.FreePort
	optsA    = enginetest.OptsA
	inGame   = enginetest.InGame
)

const (
	userA = enginetest.UserA
	userB = enginetest.UserB
)

// gameDeps is the engine's game side over f's scripted Dolphins, ticking
// fast.
func gameDeps(f *enginetest.Game) *engine.GameDeps {
	return &engine.GameDeps{
		Running:   f.List,
		Probe:     f.Probe,
		Launch:    f.Launch,
		Pipelines: f.Pipelines,
		Reset:     f.Reset,
		Tick:      2 * time.Millisecond,
		BootGrace: time.Hour,
		Retry:     2 * time.Millisecond,
	}
}

// coopSyncFake is engine.GameDeps.CoopSync for f: it records coop-sync's
// runs and waits for its context.
func coopSyncFake(f *enginetest.Game) func(context.Context, string, string, uint32, log.Reporter, engine.CoopSide) error {
	return func(ctx context.Context, name, addr string, pid uint32, _ log.Reporter, _ engine.CoopSide) error {
		f.Event("coop-sync %d start", pid)
		defer f.Event("coop-sync %d stop", pid)
		<-ctx.Done()
		return nil
	}
}

// playingOn is a lobby session's game side that plays on Dolphin pid,
// in game, as far as its co-op side asks.
type playingOn uint32

func (p playingOn) PlayablePID() uint32   { return uint32(p) }
func (playingOn) SetCoop(engine.CoopSide) {}

// coopRig is an app co-op side over a FakeGame: the env's worlds,
// backups and user folder are temporary, and every Dolphin's memory is
// the FakeGame's. envCalls counts the co-op sides started, with whether
// the host's hub was set then.
type coopRig struct {
	worlds string
	m      *mem.FakeMem
	tick   func()

	mu      sync.Mutex
	started []string
	hubSet  func() bool
}

func newCoopRig(t *testing.T, bound bool, worlds string) *coopRig {
	_, m, tick := bindtest.Game(t, bound)
	return &coopRig{worlds: worlds, m: m, tick: tick}
}

func (r *coopRig) deps(t *testing.T, who string) *appCoopDeps {
	ud, backups := coUserDir(t), t.TempDir()
	return &appCoopDeps{
		env: func(string) coopEnv {
			r.mu.Lock()
			hub := r.hubSet != nil && r.hubSet()
			r.started = append(r.started, fmt.Sprintf("%s hub=%v", who, hub))
			r.mu.Unlock()
			return coopEnv{worlds: r.worlds, backups: backups, userDirs: []string{ud}, tick: r.tick}
		},
		openMem: func(uint32) (mem.Mem, func(), error) { return r.m, func() {}, nil },
		tick:    r.tick,
		wait:    2 * time.Millisecond,
	}
}

func (r *coopRig) starts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

// TestLobbyCoopNewWorld: the host chose New world. On Start each app
// launches its Dolphin; the host's founding waits on a file bound to
// another world (the guided dialog says why), and no joiner starts co-op
// meanwhile. The window's "Remove the tag" unbinds it; the world is
// founded, the hub set on the server, and only then GO goes out with the
// world: the joiner's co-op side starts on it (the host's hub already
// set), and both apps run coop-sync on their Dolphins. Merging allowed
// after Start reaches the hub. Leave stops coop-sync before the mailbox
// reset, and the hub removes its session marker.
func TestLobbyCoopNewWorld(t *testing.T) {
	worlds := t.TempDir()
	host := newCoopRig(t, true, worlds) // the host's file: bound to bindtest.World already
	join := newCoopRig(t, false, worlds)
	fa, fb := enginetest.NewGame(), enginetest.NewGame()
	fb.NextPID = 200
	da, db := gameDeps(fa), gameDeps(fb)
	da.CoopSync, db.CoopSync = coopSyncFake(fa), coopSyncFake(fb)
	ea := engine.New(engine.Config{Live: &engine.LiveState{}, Game: da, Coop: coopApp{env: testEnv, lobby: host.deps(t, "host")}})
	eb := engine.New(engine.Config{Live: &engine.LiveState{}, Game: db, Coop: coopApp{env: testEnv, lobby: join.deps(t, "joiner")}})
	join.hubSet = func() bool { cs, _ := ea.CoopSide().(*Session); return cs != nil && cs.hub != nil }
	ca, cb := session.New(ea, session.Options{}), session.New(eb, session.Options{})

	port := freePort(t)
	optsB := optsA
	optsB.UserDir = userB
	if _, err := ca.HostLobby(context.Background(), session.HostOptions{Name: "Host", Port: port, ListenHost: "127.0.0.1", Launch: optsA}); err != nil {
		t.Fatal(err)
	}
	defer ca.Leave()
	waitSnap(t, ca, "host in its lobby", func(st session.State) bool { return st.Phase == session.InLobby && st.Lobby.Host })
	if _, err := cb.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: fmt.Sprintf("127.0.0.1:%d", port), Launch: optsB,
		CoopFn: func() session.CoopOptions { return session.CoopOptions{DryRun: true} }}); err != nil {
		t.Fatal(err)
	}
	defer cb.Leave()
	waitSnap(t, ca, "two members", func(st session.State) bool { return len(st.Lobby.Members) == 2 })
	if err := ca.SetCoopSettings(session.LobbySettings{Coop: session.CoopWorldNew}); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, cb, "the joiner sees New world", func(st session.State) bool { return st.Lobby.Settings.Coop == "new" })
	if err := ca.StartAnyway(); err != nil {
		t.Fatal(err)
	}

	st := waitSnap(t, ca, "the founding blocked on the bound file", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingBlocked
	})
	if st.Founding.Blocker != session.BlockBound || st.Founding.TagWorld != bindtest.World.String() || !strings.Contains(st.Founding.File, `"Link"`) {
		t.Fatalf("founding %+v", st.Founding)
	}
	if st.Phase != session.Starting || !st.Coop.On || st.Coop.World != "new" {
		t.Fatalf("host while founding: phase %v coop %+v", st.Phase, st.Coop)
	}
	time.Sleep(50 * time.Millisecond)
	if got := join.starts(); len(got) != 0 {
		t.Fatalf("the joiner started co-op before GO: %v", got)
	}
	if sb := cb.Snapshot(); sb.Phase != session.Starting || sb.Coop.On {
		t.Fatalf("joiner before GO: phase %v coop %+v", sb.Phase, sb.Coop)
	}

	// A dialog shown for another file's tag removes nothing.
	if err := ca.UnbindFile("ffffffffffffffff"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("UnbindFile for another world = %v", err)
	}
	if err := ca.UnbindFile(bindtest.World.String()); err != nil {
		t.Fatal(err)
	}
	st = waitSnap(t, ca, "founded and in game", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingFounded && st.Phase == session.InGame
	})
	wid := st.Founding.World
	if len(wid) != 16 {
		t.Fatalf("founded world %q", wid)
	}
	sb := waitSnap(t, cb, "the joiner's co-op on", func(st session.State) bool { return st.Phase == session.InGame && st.Coop.On })
	if sb.Coop.World != wid || !sb.Coop.DryRun {
		t.Fatalf("joiner coop %+v, want world %s and its own dry run", sb.Coop, wid)
	}
	if got := join.starts(); len(got) != 1 || got[0] != "joiner hub=true" {
		t.Fatalf("joiner co-op starts %v, want one, after the host's hub was set", got)
	}
	waitFor(t, "host coop-sync", hasEvent(fa, "coop-sync 100 start"))
	waitFor(t, "joiner coop-sync", hasEvent(fb, "coop-sync 200 start"))
	if ws, err := ListWorlds(worlds); err != nil || len(ws) != 1 || ws[0].id.String() != wid || ws[0].marker == nil {
		t.Fatalf("worlds %+v, %v", ws, err)
	}

	// The host allows merging after Start: the hub merges, both see it.
	if err := ca.SetCoopSettings(session.LobbySettings{Coop: "new", Merge: true}); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, ca, "host merge", func(st session.State) bool { return st.Coop.Merge && st.CoopHub != nil && st.CoopHub.Merge })
	waitSnap(t, cb, "joiner merge", func(st session.State) bool { return st.Coop.Merge })

	ca.Leave()
	fa.Event("left")
	// Both stop with the session's context, in either order, and only
	// then is the mailbox reset.
	ev := fa.Events()
	if len(ev) != 7 || strings.Join(ev[:3], ", ") != "launch 100, pipelines 100 start, coop-sync 100 start" ||
		!slices.Contains(ev[3:5], "coop-sync 100 stop") || !slices.Contains(ev[3:5], "pipelines 100 stop") || strings.Join(ev[5:], ", ") != "reset 100, left" {
		t.Fatalf("host teardown %q", ev)
	}
	if ws, _ := ListWorlds(worlds); len(ws) != 1 || ws[0].marker != nil {
		t.Fatalf("the hub left its marker: %+v", ws)
	}
}

// TestLobbyCoopContinue: Continue serves a world on disk: GO goes out at
// once with it, no founding. A world that can't be opened is logged and
// GO goes out without a world: nobody starts co-op.
func TestLobbyCoopContinue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		exist bool
	}{{"world on disk", true}, {"no such world", false}} {
		t.Run(tc.name, func(t *testing.T) {
			worlds := t.TempDir()
			id := world.ID{1, 2, 3, 4, 5, 6, 7, 8}
			if tc.exist {
				coWorld(t, worlds, id, time.Now(), nil)
			}
			host, join := newCoopRig(t, false, worlds), newCoopRig(t, false, worlds)
			fa, fb := enginetest.NewGame(), enginetest.NewGame()
			fb.NextPID = 200
			da, db := gameDeps(fa), gameDeps(fb)
			da.CoopSync, db.CoopSync = coopSyncFake(fa), coopSyncFake(fb)
			ea := engine.New(engine.Config{Live: &engine.LiveState{}, Game: da, Coop: coopApp{env: testEnv, lobby: host.deps(t, "host")}})
			eb := engine.New(engine.Config{Live: &engine.LiveState{}, Game: db, Coop: coopApp{env: testEnv, lobby: join.deps(t, "joiner")}})
			ca, cb := session.New(ea, session.Options{}), session.New(eb, session.Options{})
			port := freePort(t)
			if _, err := ca.HostLobby(context.Background(), session.HostOptions{Name: "Host", Port: port, ListenHost: "127.0.0.1", Launch: optsA}); err != nil {
				t.Fatal(err)
			}
			defer ca.Leave()
			waitSnap(t, ca, "host in its lobby", func(st session.State) bool { return st.Phase == session.InLobby && st.Lobby.Host })
			optsB := optsA
			optsB.UserDir = userB
			if _, err := cb.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: fmt.Sprintf("127.0.0.1:%d", port), Launch: optsB}); err != nil {
				t.Fatal(err)
			}
			defer cb.Leave()
			waitSnap(t, ca, "two members", func(st session.State) bool { return len(st.Lobby.Members) == 2 })
			if err := ca.SetCoopSettings(session.LobbySettings{Coop: id.String()}); err != nil {
				t.Fatal(err)
			}
			waitSnap(t, cb, "settings", func(st session.State) bool { return st.Lobby.Settings.Coop == id.String() })
			if err := ca.StartAnyway(); err != nil {
				t.Fatal(err)
			}
			waitSnap(t, cb, "joiner in game", func(st session.State) bool { return st.Phase == session.InGame })
			if !tc.exist {
				time.Sleep(50 * time.Millisecond)
				if got := join.starts(); len(got) != 0 || cb.Snapshot().Coop.On {
					t.Fatalf("the joiner started co-op without a world: %v", got)
				}
				waitSnap(t, ca, "the host's co-op off", func(st session.State) bool { return !st.Coop.On && st.Phase == session.InGame })
				return
			}
			st := waitSnap(t, cb, "joiner co-op", func(st session.State) bool { return st.Coop.On })
			if st.Coop.World != id.String() || ca.Snapshot().Founding != nil {
				t.Fatalf("joiner coop %+v, host founding %+v", st.Coop, ca.Snapshot().Founding)
			}
			waitFor(t, "joiner coop-sync", hasEvent(fb, "coop-sync 200 start"))
		})
	}
}

// TestClearLoadedTag: "Remove the tag" zeroes a bound file's tag in
// memory and says so; a file without one is left alone. A tag that is
// not the one the dialog showed (another file loaded since), or that
// names the session's own world, is kept.
func TestClearLoadedTag(t *testing.T) {
	_, m, tick := bindtest.Game(t, true)
	log := &coLog{}
	for _, c := range []struct{ want, own string }{
		{"ffffffffffffffff", ""}, // the dialog showed another file's tag
		{"", ""},                 // ... or a file with no readable tag
		{bindtest.World.String(), bindtest.World.String()}, // the session's own world
	} {
		if err := clearLoadedTag(log, m, tick, c.want, c.own); err == nil {
			t.Fatalf("clear %+v removed the tag", c)
		}
	}
	if s, _, _, err := diag.GateSnapshots(m, tick); err != nil || bind.Classify(bind.FileOf(s), bind.Expect{}).State != bind.Bound {
		t.Fatalf("a refused clear changed the file (%v)", err)
	}
	if err := clearLoadedTag(log, m, tick, bindtest.World.String(), "0000000000000001"); err != nil {
		t.Fatal(err)
	}
	if !log.has("co-op: cleared the co-op tag (world "+bindtest.World.String()+", bound)") || !log.has(`"Link"`) {
		t.Fatalf("log:\n%s", log)
	}
	s, st, _, err := diag.GateSnapshots(m, tick)
	if err != nil {
		t.Fatal(err)
	}
	if v := bind.Classify(bind.FileOf(s), bind.Expect{}); !v.Empty || !st.CanWrite() {
		t.Fatalf("after the clear: %+v", v)
	}
	log = &coLog{}
	if err := clearLoadedTag(log, m, tick, "", ""); err != nil || !log.has("has no co-op tag") {
		t.Fatalf("second clear: %v\n%s", err, log)
	}
}

// TestOfferQuestionFields: a bind offer's question names the file and
// the world, for the window's dialog.
func TestOfferQuestionFields(t *testing.T) {
	_, m, tick := bindtest.Game(t, false)
	s, st, _, err := diag.GateSnapshots(m, tick)
	if err != nil {
		t.Fatal(err)
	}
	p, err := bind.PlanBind(s, st, bind.BindRequest{World: bindtest.World, WorldSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	q := offerQuestion("Link", replica.Offer{Plan: p})
	if q.Kind != session.QuestionBind || q.FileName != "Link" || q.FileSlot != 1 || q.World != bindtest.World.String() || q.TagWorld != "" || q.MaybeFresh || len(q.Failed) != 0 {
		t.Fatalf("question %+v", q)
	}
}

// TestAppCoopHostLogsMatchCLI: the app's host founds a world from the
// same file the CLI's `host --coop=new` would, on its own Dolphin, and
// logs the same co-op lines (docs/08 §8.2 M2 step 1): the backup, the
// founding and the world's line, word for word.
func TestAppCoopHostLogsMatchCLI(t *testing.T) {
	ud := coUserDir(t)
	norm := func(lines []string, dirs ...string) []string {
		var out []string
		id := regexp.MustCompile(`\b[0-9a-f]{16}\b`)
		ts := regexp.MustCompile(`\d{8}T\d{6}Z(-\d+)?`)
		for _, l := range lines {
			if !strings.HasPrefix(l, "co-op:") && !strings.HasPrefix(l, "Co-op world") {
				continue
			}
			for i, d := range dirs {
				l = strings.ReplaceAll(l, d, fmt.Sprintf("<dir%d>", i))
			}
			out = append(out, ts.ReplaceAllString(id.ReplaceAllString(l, "<id>"), "<ts>"))
		}
		return out
	}
	f := Flags{on: true, world: coopWorldNew}

	// The CLI.
	_, m1, tick1 := bindtest.Game(t, false)
	worlds1, backups1 := t.TempDir(), t.TempDir()
	cli := &coLog{}
	cs, err := startCoopHost(context.Background(), cli, func(string) {}, f, coopEnv{worlds: worlds1, backups: backups1, userDirs: []string{ud},
		openMem: func() (mem.Mem, func(), error) { return m1, func() {}, nil }, tick: tick1, input: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	cs.Stop()

	// The app.
	_, m2, tick2 := bindtest.Game(t, false)
	worlds2, backups2 := t.TempDir(), t.TempDir()
	out := &enginetest.Out{}
	e := engine.New(engine.Config{Live: &engine.LiveState{}})
	deps := &appCoopDeps{
		env: func(string) coopEnv {
			return coopEnv{worlds: worlds2, backups: backups2, userDirs: []string{ud}, tick: tick2}
		},
		openMem: func(uint32) (mem.Mem, func(), error) { return m2, func() {}, nil },
		tick:    tick2, wait: time.Millisecond,
	}
	ac := newAppCoop(context.Background(), deps, e.LobbyEnv(out, "Host", nil, nil))
	ac.game = playingOn(7)
	if _, err := ac.host(f); err != nil {
		t.Fatal(err)
	}
	ac.Stop()

	got := norm(out.Lines(), worlds2, backups2)
	want := norm(cli.lines, worlds1, backups1)
	if !slices.Equal(got, want) {
		t.Fatalf("the app's co-op lines differ from the CLI's:\napp:\n%s\ncli:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(want) < 4 || !slices.ContainsFunc(want, func(l string) bool { return strings.HasPrefix(l, "co-op: founded world <id>") }) {
		t.Fatalf("too few lines to compare: %q", want)
	}
}

// TestGameRunnerCoopRestart: the app's game side starts coop-sync again
// after it stops (an error here; a Dolphin relaunch or a new boot does
// the same). Each run leaves the console when it stops, so the console
// holds the running pipeline only: Refresh area reaches it alone (no
// "say which player"), "Remove the tag" uses its Dolphin, and the
// session's context outlives the runs. A file the player said no to is
// not asked about again in the next run.
func TestGameRunnerCoopRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clog := &coLog{}
	cs := startCoopSession(clog, Flags{on: true, dryRun: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: strings.NewReader("")})
	cs.console.setCtx(ctx)
	k := coopFileKey{slot: 0, name: [bind.PlayerNameSize]byte{'L', 'i', 'n', 'k'}}

	f := enginetest.NewGame()
	f.Running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinInGame}}
	f.Script(9, inGame)
	var (
		mu   sync.Mutex
		runs []*coopSync
		mems []mem.Mem
	)
	d := gameDeps(f)
	d.CoopSync = func(cctx context.Context, name, addr string, pid uint32, rep log.Reporter, side engine.CoopSide) error {
		cs := side.(*Session)
		cg := newCoGame(t, name, coMine{life: 12})
		return appSyncOn(cctx, cs, name, cg.m, rep, func(cctx context.Context, s *coopSync) error {
			mu.Lock()
			n := len(runs)
			runs, mems = append(runs, s), append(mems, cg.m)
			mu.Unlock()
			if n == 0 {
				s.cs.askedMu.Lock()
				s.asked[k] = true // the player said Not now in the first run
				s.cs.askedMu.Unlock()
				return fmt.Errorf("lost the Dolphin")
			}
			<-cctx.Done()
			return nil
		})
	}
	g := engine.NewGameRunner(d, func() session.LaunchOptions { return optsA }, "Link", "127.0.0.1:1", &enginetest.Out{}, nil, func(error) {})
	g.SetCoop(cs)
	g.Start(ctx)
	defer g.Stop()
	waitFor(t, "coop-sync again", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(runs) == 2
	})
	mu.Lock()
	second, secondMem := runs[1], mems[1]
	mu.Unlock()
	cs.console.mu.Lock()
	syncs := slices.Clone(cs.console.syncs)
	cs.console.mu.Unlock()
	if len(syncs) != 1 || syncs[0] != second {
		t.Fatalf("console pipelines %d, want the running one alone", len(syncs))
	}
	if cs.console.firstMem() != secondMem {
		t.Fatal("Remove the tag would use the stopped run's Dolphin")
	}
	if err := cs.console.sessionCtx().Err(); err != nil {
		t.Fatalf("the session's context ended with a run: %v", err)
	}
	if err := cs.console.exec(cs.console.sessionCtx(), coopCommand{verb: "refresh"}); err != nil {
		t.Fatalf("Refresh area after a restart: %v\n%s", err, clog)
	}
	cs.askedMu.Lock()
	asked := second.asked[k]
	cs.askedMu.Unlock()
	if !asked {
		t.Fatal("the second run forgot the first run's Not now")
	}
}

// TestRetryBindsAfterMerge: a joiner's file has progress the world
// lacks, so it can't bind while the host doesn't merge (the advice).
// retryBinds before the hub merges waits, then says to load the file
// again; once the host allows merging (Hub.SetMerge re-grants the joiner
// the merge feature), it asks to bind the file again, and the bind is
// offered and made.
func TestRetryBindsAfterMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("ticks at the replica's real rate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ga := newCoGame(t, "Link", coMine{life: 12})
	gaFrames := make(chan struct{})
	go func() {
		defer close(gaFrames)
		for ctx.Err() == nil {
			ga.g.Frame()
			time.Sleep(2 * time.Millisecond)
		}
	}()
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds: t.TempDir(), backups: t.TempDir(), userDirs: []string{coUserDir(t)},
		openMem: func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:    func() { time.Sleep(10 * time.Millisecond) },
	}}
	h.start(Flags{on: true, world: coopWorldNew, yes: true})
	defer h.stop()

	// The joiner's file: the host's, plus a bit of progress the world lacks.
	gb := newCoGame(t, "Aryll", coMine{life: 12})
	or, _ := coShareable()
	for _, off := range or {
		if m := world.SharedMask(off); gb.get(off)&m != m {
			gb.put(off, gb.get(off)|(m&^gb.get(off)&-(m&^gb.get(off))))
			break
		}
	}
	gb.m.Record(true)
	frames := make(chan struct{})
	go func() {
		defer close(frames)
		for ctx.Err() == nil {
			gb.g.Frame()
			time.Sleep(2 * time.Millisecond)
		}
	}()
	log := &coLog{}
	cs := startCoopSession(log, Flags{on: true, yes: true}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: strings.NewReader("")})
	s, err := cs.newSync(ctx, "Aryll", gb.m, log)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, h.addr) }()
	coWaitLog(t, log, "co-op: this file can't bind unless the host runs with --coop-merge")

	cs.retryBinds(ctx, 100*time.Millisecond)
	if !log.has("co-op: Aryll: the host allowed merging, but its hub has not said so yet; load the file again to retry") {
		t.Fatalf("no warning without the merge feature:\n%s", log)
	}
	if err := h.cs.hub.SetMerge(true); err != nil {
		t.Fatal(err)
	}
	cs.retryBinds(ctx, 5*time.Second)
	if !log.has("co-op: Aryll: the host merges files now: asking to bind this file again") {
		t.Fatalf("no retry:\n%s", log)
	}
	deadline := time.Now().Add(15 * time.Second)
	for s.r.Status().State != replica.StateArmed {
		if time.Now().After(deadline) {
			t.Fatalf("not armed after the retry: %+v\n%s", s.r.Status(), log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	<-frames
	<-gaFrames
}
