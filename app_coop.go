package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Co-op in the app (App PR 9, design §2.5-§2.8, §3.6): what a lobby
// session's START and GO do when the host chose co-op. The work is the
// CLI's --coop session (coop_session.go), started at a different moment
// and on this player's own Dolphin and user folder, so the log lines are
// the CLI's:
//
//   - The host, on START: backs up the cards of its Dolphin user folder
//     and loads the stage tables (startCoopSide), opens the chosen world
//     or founds a new one from the file loaded in its game (appCoop.found:
//     it waits for the game, reporting each look to the window's guided
//     dialog, and for a file that can't found a world waits for another,
//     or for "Remove the tag"), starts the hub, sets it on its server
//     (Server.SetCoop) and only then sends GO with the world's ID.
//   - A joiner starts its co-op side on GO, and only on a GO that names a
//     world: its co-op connection never meets a server without a hub.
//   - Each app runs coop-sync on its Dolphin beside the pose pipelines
//     (gameRunner.setCoop), once that Dolphin runs the patched game.
//   - The host allowing merging after Start (the lobby's settings merge)
//     turns it on in the hub (hub.SetMerge); a joiner whose file needed it
//     asks to bind again.
//
// A failure to open or found the world is logged; the host then sends GO
// without a world and the session plays without co-op.

// appCoopDeps are the app's co-op seams; the tests fake the Dolphin.
type appCoopDeps struct {
	// env is the co-op environment for a player whose Dolphin user
	// folder is userDir ("" for the CLI's USER_DIR_1/2).
	env func(userDir string) coopEnv
	// openMem opens Dolphin pid's memory, and returns how to close it.
	openMem func(pid uint32) (mem.Mem, func(), error)
	// tick waits between two looks at the game.
	tick func()
	// wait is how long the founding waits for a Dolphin that runs the
	// game before it looks again.
	wait time.Duration
}

func realAppCoopDeps() *appCoopDeps {
	return &appCoopDeps{
		env: appCoopEnv,
		openMem: func(pid uint32) (mem.Mem, func(), error) {
			d, err := openDolphin(pid)
			if err != nil {
				return nil, nil, err
			}
			return mem.NewDolphinMem(d), d.Close, nil
		},
		tick: coopTick,
		wait: time.Second,
	}
}

// appCoopEnv is the co-op environment of an app player: the cards of its
// own Dolphin user folder are backed up (the CLI backs up USER_DIR_1 and
// _2), and the worlds and backups live under the app's config folder, as
// the CLI's do.
func appCoopEnv(userDir string) coopEnv {
	dirs := []string{userDir}
	if userDir == "" {
		a, b := backup.UserDirs()
		dirs = []string{a, b}
	}
	return coopEnv{userDirs: dirs, tick: coopTick, tables: loadCoopStageTables}
}

// appCoop is one lobby session's co-op side.
type appCoop struct {
	e      *sessionEngine
	deps   *appCoopDeps
	out    session.LobbyOutput
	name   string
	coopFn func() session.CoopOptions
	launch func() session.LaunchOptions
	// srvLog gets the hub's lines (the host's server log); nil on a
	// joiner.
	srvLog func(string)
	// setHub sets the hub on the host's server; nil on a joiner.
	setHub func(network.CoopHandler)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	started bool
	cs      *coopSession
	game    *gameRunner
	lc      *network.LobbyClient
	merge   bool
	// hub is the host's hub once it serves the world; nil before, and
	// on a joiner.
	hub *hub.Hub
	// world is the world the session runs (the host's, or GO's).
	world string
	// unbind asks the founding loop, which owns the host's game memory
	// until the world is founded, to zero the loaded file's tag; nil
	// outside the founding.
	unbind chan unbindReq
	// worlds is the host's co-op worlds folder ("" before host).
	worlds string
}

// unbindReq is a "Remove the tag" for the founding loop: want is the
// tag's world the dialog showed; the loop replies on reply.
type unbindReq struct {
	want  string
	reply chan error
}

func newAppCoop(ctx context.Context, e *sessionEngine, out session.LobbyOutput, name string, coopFn func() session.CoopOptions, launch func() session.LaunchOptions) *appCoop {
	ac := &appCoop{e: e, deps: e.coopApp, out: out, name: name, coopFn: coopFn, launch: launch}
	ac.ctx, ac.cancel = context.WithCancel(ctx)
	return ac
}

// own is this player's own co-op choices (the window's).
func (ac *appCoop) own() session.CoopOptions {
	if ac.coopFn == nil {
		return session.CoopOptions{}
	}
	return ac.coopFn()
}

func (ac *appCoop) userDir() string {
	if ac.launch == nil {
		return ""
	}
	return ac.launch().UserDir
}

// bind hands the co-op side the session's lobby client and game side.
func (ac *appCoop) bind(lc *network.LobbyClient, game *gameRunner) {
	ac.mu.Lock()
	ac.lc, ac.game = lc, game
	ac.mu.Unlock()
}

// coopOn says the lobby settings s ask for co-op.
func coopOn(s network.LobbySettingsBody) bool { return s.Coop != "" && s.Coop != network.CoopOff }

// begin says whether co-op starts now (once per session).
func (ac *appCoop) begin() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.started || ac.deps == nil || ac.ctx.Err() != nil {
		return false
	}
	ac.started = true
	return true
}

// front is the co-op front end: the window's questions and advice, and
// the engine's console.
func (ac *appCoop) front(f coopFlags) coopFront {
	return ac.e.front(ac.out, f.options())
}

// onStart is the host's START with co-op on: the world, the hub, then GO.
func (ac *appCoop) onStart(s network.LobbySettingsBody) {
	if !ac.begin() {
		return
	}
	own := ac.own()
	f := coopFlags{on: true, world: s.Coop, merge: s.Merge, dryRun: s.DryRun, liveSwitches: s.LiveSwitches, iso: own.ISO, yes: own.Yes}
	ac.wg.Add(1)
	go func() {
		defer ac.wg.Done()
		world, err := ac.host(f)
		if err != nil {
			if ac.ctx.Err() != nil {
				return // Leave while founding
			}
			ac.out.Log(report.Err, err.Error())
			ac.out.Log(report.Warn, "co-op: the session goes on without co-op (pose sync works).")
			ac.reportCoop(session.CoopOptions{}) // the window shows no co-op panel
			if f.world == coopWorldNew {
				ac.found1(session.Founding{Phase: session.FoundingFailed, Err: err.Error()})
			}
		}
		ac.sendGo(world)
	}()
}

// host opens or founds the world and starts the hub, and returns the
// world's ID.
func (ac *appCoop) host(f coopFlags) (string, error) {
	env := ac.deps.env(ac.userDir())
	env.front = ac.front(f)
	cs := startCoopSide(ac.out, f, env, true)
	cs.poseClient = ac.poseClient
	cs.console.setCtx(ac.ctx)
	ac.setCS(cs)
	ac.coopOptions(f)
	if root, err := env.worldsRoot(); err == nil {
		ac.mu.Lock()
		ac.worlds = root
		ac.mu.Unlock()
	}
	st, err := cs.openWorld(ac.ctx, ac.out, f, env, ac.found)
	if err != nil {
		return "", err
	}
	if err := cs.startHub(ac.out, ac.srvLog, f, st); err != nil {
		return "", err
	}
	if ac.setHub != nil {
		ac.setHub(cs.hub)
	}
	// Merging allowed while the world was being founded counts too.
	ac.mu.Lock()
	ac.merge, ac.hub, ac.world = f.merge, cs.hub, cs.world.String()
	lc := ac.lc
	ac.mu.Unlock()
	if lc != nil {
		if st, ok := lc.State(); ok && st.Settings.Merge != f.merge {
			ac.onSettings(st.Settings)
		}
	}
	id := cs.world.String()
	if f.world == coopWorldNew {
		ac.found1(session.Founding{Phase: session.FoundingFounded, World: id})
	}
	ac.setGame(cs)
	return id, nil
}

// sendGo tells every member the host's side is ready, with the co-op
// world ("" for none).
func (ac *appCoop) sendGo(world string) {
	ac.mu.Lock()
	lc := ac.lc
	ac.mu.Unlock()
	if lc == nil || ac.ctx.Err() != nil {
		return
	}
	if st, ok := lc.State(); ok && st.Phase != network.PhaseStarting {
		return // in game already (a replayed START)
	}
	if err := lc.Go(world); err != nil {
		report.Logf(ac.out, report.Warn, "Couldn't tell the players to go: %v", err)
	}
}

// onGo is a joiner's GO: with a world, its co-op side starts. The card
// backup and the stage tables (a disc read, a build when none is cached)
// take a while, so they run beside the lobby connection, whose reads
// (STATE, chat) go on meanwhile; a Leave during them starts nothing.
func (ac *appCoop) onGo(s network.LobbySettingsBody, worldID string) {
	if worldID == "" || !coopOn(s) || !ac.begin() {
		return
	}
	ac.wg.Add(1)
	go func() {
		defer ac.wg.Done()
		own := ac.own()
		f := coopFlags{on: true, dryRun: s.DryRun || own.DryRun, iso: own.ISO, yes: own.Yes}
		env := ac.deps.env(ac.userDir())
		env.front = ac.front(f)
		cs := startCoopSession(ac.out, f, env)
		cs.poseClient = ac.poseClient
		cs.console.setCtx(ac.ctx)
		ac.mu.Lock()
		ac.merge, ac.world = s.Merge, worldID
		ac.mu.Unlock()
		ac.setCS(cs)
		if ac.ctx.Err() != nil {
			return // Leave: stop stops cs
		}
		o := f.options()
		o.World = worldID
		ac.reportCoop(o)
		ac.setGame(cs)
		// Merging allowed while the side started (onSettings found no
		// co-op side then) counts too.
		ac.mu.Lock()
		lc := ac.lc
		ac.mu.Unlock()
		if lc != nil {
			if st, ok := lc.State(); ok && st.Settings.Merge != s.Merge {
				ac.onSettings(st.Settings)
			}
		}
	}()
}

// onSettings follows the lobby's settings after Start: merging turned on
// (the host allowed a joiner's request) reaches the host's hub, and a
// joiner whose file needed it asks to bind again.
func (ac *appCoop) onSettings(s network.LobbySettingsBody) {
	ac.mu.Lock()
	cs, h, was, wid := ac.cs, ac.hub, ac.merge, ac.world
	if cs == nil || s.Merge == was || (cs.flags.world != "" && h == nil) {
		ac.mu.Unlock()
		return // nothing runs yet (the host's hub comes after the founding, and looks again)
	}
	ac.merge = s.Merge
	ac.mu.Unlock()
	co := cs.flags.options()
	co.Merge, co.World = s.Merge, wid
	if h != nil {
		if err := h.SetMerge(s.Merge); err != nil {
			report.Logf(ac.out, report.Warn, "co-op: %v", err)
		}
	}
	ac.reportCoop(co)
	if s.Merge {
		ac.wg.Add(1)
		go func() {
			defer ac.wg.Done()
			cs.retryBinds(ac.ctx, 15*time.Second)
		}()
	}
}

func (ac *appCoop) setCS(cs *coopSession) {
	if co, ok := ac.out.(session.CoopOutput); ok && cs.backupDir != "" {
		co.Backup(cs.backupDir)
	}
	ac.mu.Lock()
	ac.cs = cs
	ac.mu.Unlock()
}

func (ac *appCoop) setGame(cs *coopSession) {
	ac.mu.Lock()
	g := ac.game
	ac.mu.Unlock()
	if g != nil {
		g.setCoop(cs)
	}
}

func (ac *appCoop) coopOptions(f coopFlags) { ac.reportCoop(f.options()) }

// poseClient is the puppet-sync client of this player's pose pipelines:
// where a warp finds the partner.
func (ac *appCoop) poseClient() *network.Client {
	if ac.e.ls == nil {
		return nil
	}
	return ac.e.ls.puppetClient()
}

// found1 reports the founding's progress to the window.
func (ac *appCoop) found1(f session.Founding) {
	if co, ok := ac.out.(session.CoopOutput); ok {
		co.Founding(f)
	}
}

// stop ends the co-op side: after the game side (coop-sync) and the
// host's server have stopped, the hub stops, which writes the world and
// removes the session marker.
func (ac *appCoop) stop() {
	ac.cancel()
	ac.wg.Wait()
	ac.mu.Lock()
	cs := ac.cs
	ac.mu.Unlock()
	cs.stop()
}

// found founds a world from the file loaded in the host's game
// (--coop=new), as foundCoopWorld does, on the Dolphin the game side
// plays on, and with the window told each look: it waits for the game to
// be in game and idle, logging the CLI's line every 5 s, and for a file
// that can't found a world (bound already, a second quest, ...) it says
// why once and waits for another file, or for the window's "Remove the
// tag" (UnbindFile).
func (ac *appCoop) found(ctx context.Context, pol *world.Policy) (*world.World, error) {
	unbind := make(chan unbindReq)
	ac.mu.Lock()
	ac.unbind = unbind
	root := ac.worlds
	ac.mu.Unlock()
	defer func() {
		ac.mu.Lock()
		ac.unbind = nil
		ac.mu.Unlock()
	}()
	var (
		m        mem.Mem
		closeMem = func() {}
		memPID   uint32
		told     time.Time
		blocked  string
		last     session.Founding
	)
	defer func() { closeMem() }()
	say := func(f session.Founding) {
		if f != last {
			last = f
			ac.found1(f)
		}
	}
	say(session.Founding{Phase: session.FoundingWaiting, Why: "waiting for your Dolphin to run Wind Waker"})
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("co-op: no world was founded: %w", err)
		}
		ac.mu.Lock()
		g := ac.game
		ac.mu.Unlock()
		pid := uint32(0)
		if g != nil {
			pid = g.playablePID()
		}
		if pid != memPID {
			closeMem()
			m, closeMem, memPID = nil, func() {}, 0
			if pid != 0 {
				nm, c, err := ac.deps.openMem(pid)
				if err == nil {
					m, closeMem, memPID = nm, c, pid
				}
			}
		}
		if m == nil {
			say(session.Founding{Phase: session.FoundingWaiting, Why: "waiting for your Dolphin to run Wind Waker"})
			select {
			case <-ctx.Done():
			case req := <-unbind:
				req.reply <- errors.New("no game is running to remove the tag from")
			case <-time.After(ac.deps.wait):
			}
			continue
		}
		select {
		case req := <-unbind:
			req.reply <- clearLoadedTag(ac.out, m, ac.deps.tick, req.want, "")
			blocked = ""
			continue
		default:
		}
		s, st, _, err := gateSnapshots(m, ac.deps.tick)
		if err != nil {
			closeMem()
			m, closeMem, memPID = nil, func() {}, 0
			continue
		}
		if !st.CanWriteIdle() {
			if time.Since(told) >= 5*time.Second {
				report.Logf(ac.out, report.Info, "co-op: waiting for your save file to be loaded in game, out of menus and cutscenes, to found the world from it (%s)", st.Why())
				told = time.Now()
			}
			f := session.Founding{Phase: session.FoundingWaiting, Why: st.Why()}
			if st.CanWrite() {
				f.File = bind.Describe(bind.FileOf(s))
			}
			say(f)
			continue
		}
		file := bind.FileOf(s)
		if kind, err := foundCheck(file); err != nil {
			if err.Error() != blocked {
				blocked = err.Error()
				ac.out.Log(report.Warn, blocked)
			}
			f := session.Founding{Phase: session.FoundingBlocked, File: bind.Describe(file), Blocker: kind, BlockText: err.Error()}
			if v := bind.Classify(file, bind.Expect{}); v.HasTag {
				f.TagWorld = v.Tag.World.String()
				f.TagWorldHere = worldOnDisk(root, f.TagWorld)
			}
			say(f)
			continue
		}
		say(session.Founding{Phase: session.FoundingWaiting, File: bind.Describe(file)})
		return foundFrom(ac.out, s, pol)
	}
}

// worldOnDisk says world id is in the co-op worlds folder root.
func worldOnDisk(root, id string) bool {
	if root == "" {
		return false
	}
	ws, err := listCoopWorlds(root)
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w.id.String() == id {
			return true
		}
	}
	return false
}

// unbindFile zeroes the loaded file's co-op tag in this player's game,
// when it still names world want (the dialog's): through the founding
// loop while it runs, else on the Dolphin coop-sync plays on. A tag of
// the session's own world is never removed.
func (ac *appCoop) unbindFile(want string) error {
	ac.mu.Lock()
	ch, cs, own := ac.unbind, ac.cs, ac.world
	ac.mu.Unlock()
	if ch != nil {
		reply := make(chan error, 1)
		select {
		case ch <- unbindReq{want: want, reply: reply}:
			return <-reply
		case <-time.After(10 * time.Second):
			return errors.New("the founding is busy: try again")
		}
	}
	if cs == nil {
		return errors.New("co-op has not started yet")
	}
	m := cs.console.firstMem()
	if m == nil {
		return errors.New("co-op isn't looking at your game yet: try again once Dolphin runs it")
	}
	return clearLoadedTag(ac.out, m, ac.deps.tick, want, own)
}

// errFileChanged is a "Remove the tag" refused because the loaded file's
// tag is no longer the one the dialog showed.
var errFileChanged = errors.New("the loaded file changed: try again")

// clearLoadedTag is `coop-tag clear --yes` for the app: it zeroes the
// co-op tag of the file loaded in m, in memory only (the card keeps it
// until the next in-game save), and logs what it did. The tag must name
// world want, the one the dialog showed ("": a file shown with no
// readable tag must still have none), and never own, the session's
// world ("" for none yet): a dialog clicked after another file was
// loaded unbinds nothing.
func clearLoadedTag(rep report.Reporter, m mem.Mem, tick func(), want, own string) error {
	s, st, _, err := gateSnapshots(m, tick)
	if err != nil {
		return err
	}
	p, err := bind.PlanClear(s, st, bind.Expect{})
	switch {
	case errors.Is(err, bind.ErrNoTag):
		rep.Log(report.Info, "co-op: this file has no co-op tag: nothing to clear.")
		return nil
	case err != nil:
		return fmt.Errorf("%w (try again in game, out of menus and cutscenes)", err)
	}
	tag := ""
	if p.Verdict.HasTag {
		tag = p.Verdict.Tag.World.String()
	}
	switch {
	case own != "" && tag == own:
		return fmt.Errorf("this file is bound to this session's world %s: its tag stays", own)
	case tag != want:
		return errFileChanged
	}
	p.Confirm()
	if err := bind.Scrub(m, p, s, st); err != nil {
		return err
	}
	what := p.Verdict.Why
	if p.Verdict.HasTag {
		what = fmt.Sprintf("world %s, %s", p.Verdict.Tag.World, p.Verdict.State)
	}
	report.Logf(rep, report.OK, "co-op: cleared the co-op tag (%s) of %s at 0x%08X. Save in game to write the unbound file to the memory card; loading it from the card again brings the tag back.",
		what, bind.Describe(p.File), addr.Reserve)
	return nil
}

// retryBinds asks every pipeline whose unbound file needed the host's
// merge to bind again, once its replica has learned the host merges
// now (the hub's new features), waiting at most wait.
func (cs *coopSession) retryBinds(ctx context.Context, wait time.Duration) {
	cs.console.mu.Lock()
	ss := append([]*coopSync(nil), cs.console.syncs...)
	cs.console.mu.Unlock()
	for _, s := range ss {
		st := s.r.Status()
		if st.Preflight == nil || !st.Preflight.NeedsMerge || st.Offer != nil {
			continue
		}
		deadline := time.Now().Add(wait)
		for s.r.Status().Features&hub.FeatureMerge == 0 {
			if ctx.Err() != nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if s.r.Status().Features&hub.FeatureMerge == 0 {
			report.Logf(s.rep, report.Warn, "co-op: %s: the host allowed merging, but its hub has not said so yet; load the file again to retry", s.name)
			continue
		}
		report.Logf(s.rep, report.Info, "co-op: %s: the host merges files now: asking to bind this file again", s.name)
		s.r.RequestBind()
	}
}

// appCoopSync is gameDeps.coopSync: runCoopSyncCtx for the app. The
// app's game side starts coop-sync again after an error, a Dolphin
// relaunch or a new boot, so each run leaves the console when it stops
// (appSyncOn); the CLI's runs once a process.
func appCoopSync(ctx context.Context, name, addr string, pid uint32, rep report.Reporter, cs *coopSession) error {
	d, err := openDolphin(pid)
	if err != nil {
		return err
	}
	defer d.Close()
	return appSyncOn(ctx, cs, name, mem.NewDolphinMem(d), rep, func(ctx context.Context, s *coopSync) error {
		return s.run(ctx, addr)
	})
}

// appSyncOn builds name's coop-sync pipeline over m, runs it with run,
// and takes it from the console once run returns: its Dolphin handle is
// closed then, and its replica stopped.
func appSyncOn(ctx context.Context, cs *coopSession, name string, m mem.Mem, rep report.Reporter, run func(context.Context, *coopSync) error) error {
	s, err := cs.newSync(ctx, name, m, rep)
	if err != nil {
		return err
	}
	defer cs.console.remove(s)
	return run(ctx, s)
}

// firstMem is the memory of the console's first pipeline, nil before
// one runs.
func (c *coopConsole) firstMem() mem.Mem {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.syncs) == 0 {
		return nil
	}
	return c.syncs[0].m
}

// puppetClient is the puppet-sync client registered with the bridge.
func (ls *liveState) puppetClient() *network.Client {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.client
}

// UnbindFile zeroes the co-op tag of the file loaded in this player's
// game (session.CoopFileEngine).
func (e *sessionEngine) UnbindFile(world string) error {
	e.mu.Lock()
	ac := e.coop1
	e.mu.Unlock()
	if ac == nil {
		return session.ErrNoCoop
	}
	return ac.unbindFile(world)
}

// reportCoop tells the window the co-op options the session runs with.
func (ac *appCoop) reportCoop(o session.CoopOptions) {
	if co, ok := ac.out.(session.CoopOutput); ok {
		co.Coop(o)
	}
}
