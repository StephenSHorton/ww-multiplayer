package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/dolphinenv"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// The app's game side (App PR 8, design §2.6): what a lobby session does
// once the host pressed Start. gameRunner gets this player's Dolphin
// going (session.DecideLaunch: attach, launch or wait), watches it
// (session.DolphinStateOf: launching, title screen, in game, closed, or a
// wrong game / unpatched disc), reports it to the controller and the
// lobby, and runs the pose pipelines (broadcast-pose + puppet-sync) on it
// as soon as it runs the patched game, title screen included, so remote
// Links show there (#38). Co-op stays off (App PR 9). Leave stops the
// pipelines and resets that Dolphin's mailbox (shadow_mode 0, pose seqs,
// tunic slots off); the Dolphin keeps running.
//
// Everything that touches a process or Dolphin's memory is in gameDeps,
// so app_game_test.go drives the runner with fakes. The CLI never runs
// it: no subcommand opens a lobby.

// gameDeps are the game side's seams.
type gameDeps struct {
	// running lists the running Dolphins, with their user folders and
	// states.
	running func() []session.RunningDolphin
	// probe looks at Dolphin pid.
	probe func(pid uint32) session.Probe
	// launch starts the configured Dolphin (and puts the cheats in its
	// user folder first, when asked) and returns its PID.
	launch func(o session.LaunchOptions, rep report.Reporter) (uint32, error)
	// pipelines runs the pose pipelines on Dolphin pid until ctx ends or
	// one fails (runPipelines' contract).
	pipelines func(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, rep report.Reporter) error
	// reset resets Dolphin pid's mailbox after the pipelines stopped.
	reset func(pid uint32)
	// coopSync runs coop-sync on Dolphin pid for co-op session cs until
	// ctx ends (runCoopSyncCtx's contract: appCoopSync); nil runs none.
	coopSync func(ctx context.Context, name, addr string, pid uint32, rep report.Reporter, cs *coopSession) error
	// envPID is WW_DOLPHIN_PID: the Dolphin to play on, whatever runs.
	envPID uint32

	tick      time.Duration // how often the Dolphin is looked at
	bootGrace time.Duration // how long a launched Dolphin counts as launching
	retry     time.Duration // the wait before failed pipelines start again
	now       func() time.Time
}

// realGameDeps are the game side's seams on this machine. ls is the
// app's status bridge: the pipelines register their client and position
// with it (not the client's chat: a lobby session hears chat on its lobby
// connection, which the host relays to the player connections too).
func realGameDeps(ls *liveState) *gameDeps {
	var onClient func(*network.Client)
	var onPos func(x, y, z float32)
	if ls != nil {
		onClient, onPos = ls.setClient, ls.setLocalPos
	}
	return &gameDeps{
		running: runningDolphins,
		probe:   probeDolphin,
		launch:  launchDolphinFor,
		pipelines: func(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, rep report.Reporter) error {
			return runPosePipelinesOn(ctx, cancel, name, addr, pid, rep, onClient, onPos)
		},
		reset:    resetMailboxOn,
		coopSync: appCoopSync,
		envPID:   envDolphinPID(),
	}
}

func (d *gameDeps) defaults() {
	if d.tick <= 0 {
		d.tick = time.Second
	}
	if d.bootGrace <= 0 {
		d.bootGrace = 45 * time.Second
	}
	if d.retry <= 0 {
		d.retry = 3 * time.Second
	}
	if d.now == nil {
		d.now = time.Now
	}
}

// gameRunner is one lobby session's game side. start begins it (once:
// the host's START arrives again on a reconnect, and a late joiner gets
// it at once); stop ends it and waits for the teardown.
type gameRunner struct {
	deps *gameDeps
	opts func() session.LaunchOptions // read at start
	o    session.LaunchOptions
	name string
	addr string
	out  session.LobbyOutput
	// lobby reports the Dolphin's state to the lobby (its chip); nil-safe.
	lobby func(state string)
	// fail ends the session on a fatal pipelines error (a version
	// mismatch, say).
	fail func(error)

	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	done     chan struct{}
	relaunch chan struct{}
	info     session.DolphinInfo
	err      error
	// coop is the session's co-op side once it started (setCoop): its
	// coop-sync runs beside the pose pipelines.
	coop *coopSession
}

func newGameRunner(deps *gameDeps, opts func() session.LaunchOptions, name, addr string, out session.LobbyOutput, lobby func(string), fail func(error)) *gameRunner {
	deps.defaults()
	return &gameRunner{deps: deps, opts: opts, name: name, addr: addr, out: out, lobby: lobby, fail: fail,
		done: make(chan struct{}), relaunch: make(chan struct{}, 1)}
}

// start begins the game side under ctx; later calls do nothing.
func (g *gameRunner) start(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started {
		return
	}
	g.started = true
	if g.opts != nil {
		g.o = g.opts()
	}
	ctx, g.cancel = context.WithCancel(ctx)
	go g.run(ctx)
}

// stop ends the game side and returns once its pipelines have stopped
// and the mailbox is reset. It returns the fatal pipelines error the
// session ended on, if any.
func (g *gameRunner) stop() error {
	g.mu.Lock()
	started, cancel := g.started, g.cancel
	g.started = true // a START after Leave starts nothing
	g.mu.Unlock()
	if !started {
		return nil
	}
	cancel()
	<-g.done
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// setCoop starts coop-sync for co-op session cs on this player's
// Dolphin, beside the pose pipelines, from the next look on (once the
// host's hub serves the world: after GO).
func (g *gameRunner) setCoop(cs *coopSession) {
	g.mu.Lock()
	g.coop = cs
	g.mu.Unlock()
}

func (g *gameRunner) coopSide() *coopSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.coop
}

// playablePID is the Dolphin the game side plays on while it runs the
// patched game (title screen included), else 0.
func (g *gameRunner) playablePID() uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.info.State.Playable() {
		return 0
	}
	return g.info.PID
}

// launchAgain asks for a launch of this player's Dolphin (the "Dolphin
// closed" banner's Launch).
func (g *gameRunner) launchAgain() error {
	g.mu.Lock()
	started, info, manual := g.started && g.cancel != nil, g.info, g.o.Manual
	g.mu.Unlock()
	if !started {
		return session.ErrWrongPhase
	}
	if info.PID != 0 && info.State != session.DolphinNone {
		return errors.New("your Dolphin is running already")
	}
	if manual {
		return errors.New("you start Dolphin yourself (Setup: \"I'll start Dolphin myself\")")
	}
	select {
	case g.relaunch <- struct{}{}:
	default:
	}
	return nil
}

// set records and reports the Dolphin's info when it changed.
func (g *gameRunner) set(info session.DolphinInfo) {
	g.mu.Lock()
	old := g.info
	g.info = info
	g.mu.Unlock()
	if info == old {
		return
	}
	g.out.Dolphin(info)
	if info.State != old.State && g.lobby != nil {
		g.lobby(string(info.State))
	}
}

// run is the game side's loop.
func (g *gameRunner) run(ctx context.Context) {
	defer close(g.done)
	d := g.deps
	info := session.DolphinInfo{State: session.DolphinNone, Manual: g.o.Manual, ISO: g.o.ISO}
	var (
		launchedAt time.Time
		badSeen    session.DolphinState // a wrong-game/unpatched state seen once: reported when seen twice
		offSeen    int                  // looks in a row the pipelines ran on a Dolphin not playing the patched game
		pipeCancel context.CancelFunc
		pipeDone   chan error
		pipePID    uint32
		retryAt    time.Time
		coopCancel context.CancelFunc
		coopDone   chan error
		coopAt     time.Time           // when a failed coop-sync starts again
		played     = map[uint32]bool{} // Dolphins the pipelines ran on: reset at the end
	)
	stopCoop := func() {
		if coopCancel == nil {
			return
		}
		coopCancel()
		<-coopDone
		coopCancel, coopDone = nil, nil
	}
	stopPipes := func() {
		stopCoop()
		if pipeCancel == nil {
			return
		}
		pipeCancel()
		<-pipeDone
		pipeCancel, pipeDone = nil, nil
		info.Pipelines = false
	}
	doLaunch := func() {
		info.Err = ""
		info.LaunchAttempt++
		if g.o.Exe == "" || g.o.ISO == "" {
			info.Err = "Choose your Dolphin and your patched disc in Setup first."
			g.out.Log(report.Err, "Can't start Dolphin: "+info.Err)
			return
		}
		report.Logf(g.out, report.Info, "Starting Dolphin with your patched disc.")
		pid, err := d.launch(g.o, g.out)
		if err != nil {
			info.Err = err.Error()
			report.Logf(g.out, report.Err, "Couldn't start Dolphin: %v", err)
			return
		}
		info.PID, info.Launched, launchedAt = pid, true, d.now()
		info.State = session.DolphinLaunching
		report.Logf(g.out, report.OK, "Dolphin started (pid %d).", pid)
	}
	attach := func(pid uint32, why string) {
		info.PID, info.Launched, info.Err = pid, false, ""
		report.Logf(g.out, report.OK, "Playing on the Dolphin that is running (pid %d): %s.", pid, why)
	}

	// The first decision.
	switch {
	case d.envPID != 0:
		attach(d.envPID, "WW_DOLPHIN_PID names it")
	default:
		plan := session.DecideLaunch(g.o, d.running())
		switch plan.Kind {
		case session.PlanAttach:
			attach(plan.PID, plan.Why)
		case session.PlanLaunch:
			doLaunch()
		case session.PlanWait:
			g.out.Log(report.Info, "Start Dolphin with your patched disc: the session plays as soon as it runs ("+plan.Why+").")
		}
	}

	t := time.NewTicker(d.tick)
	defer t.Stop()
	for {
		// Look for a Dolphin while there is none: the player's own (manual),
		// or one restarted on this player's folder; never launch here.
		if info.PID == 0 && d.envPID == 0 {
			if plan := session.DecideLaunch(g.o, d.running()); plan.Kind == session.PlanAttach {
				attach(plan.PID, plan.Why)
			}
		}
		if info.PID != 0 {
			launching := info.Launched && d.now().Sub(launchedAt) < d.bootGrace
			st := session.DolphinStateOf(d.probe(info.PID), launching)
			if st.Bad() && st != info.State && st != badSeen {
				badSeen = st // once may be the boot mid-way: wait for the next look
				st = info.State
				if st == session.DolphinNone {
					// Just attached: it runs, so it isn't "closed" (only a probe
					// that finds no process is).
					st = session.DolphinNoGame
					if launching {
						st = session.DolphinLaunching
					}
				}
			} else if !st.Bad() {
				badSeen = ""
			}
			switch {
			case st == session.DolphinNone:
				stopPipes()
				report.Logf(g.out, report.Warn, "Dolphin (pid %d) closed. Start it again to keep playing, or Leave.", info.PID)
				info.PID, info.Launched = 0, false
			case st.Bad() && st != info.State:
				stopPipes()
				g.badLog(st)
			}
			// Emulation stopped or another boot under the pipelines: their
			// handle and RAM base are the old boot's. Seen twice (one empty
			// read is no reason), they stop, and start again on the new boot.
			if pipeDone != nil && st != session.DolphinNone && !st.Bad() && !st.Playable() {
				if offSeen++; offSeen >= 2 {
					stopPipes()
					offSeen = 0
				}
			} else {
				offSeen = 0
			}
			info.State = st
			if info.PID == 0 {
				info.State = session.DolphinNone
			}
		}
		// The pipelines: on the patched game, title screen included.
		if pipeDone != nil {
			select {
			case err := <-pipeDone:
				pipeCancel()
				pipeCancel, pipeDone = nil, nil
				info.Pipelines = false
				if err != nil && ctx.Err() == nil {
					if errors.Is(err, network.ErrFatal) {
						g.mu.Lock()
						g.err = err
						g.mu.Unlock()
						g.set(info)
						if g.fail != nil {
							g.fail(err)
						}
						g.finish(played)
						return
					}
					report.Logf(g.out, report.Warn, "The pose pipelines stopped (%v); starting them again.", err)
				}
				retryAt = d.now().Add(d.retry)
			default:
			}
		}
		if info.State.Playable() && pipeDone == nil && !d.now().Before(retryAt) && ctx.Err() == nil {
			pctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			pid := info.PID
			played[pid], pipePID = true, pid
			go func() { done <- d.pipelines(pctx, cancel, g.name, g.addr, pid, g.out) }()
			pipeCancel, pipeDone = cancel, done
			info.Pipelines = true
			report.Logf(g.out, report.OK, "Playing on Dolphin (pid %d): remote Links appear in your game.", pid)
		}
		if pipeDone != nil && pipePID != info.PID {
			stopPipes() // the Dolphin changed under them
		}
		// coop-sync, beside the pose pipelines once co-op has started.
		if coopDone != nil {
			select {
			case err := <-coopDone:
				coopCancel()
				coopCancel, coopDone = nil, nil
				if err != nil && ctx.Err() == nil {
					report.Logf(g.out, report.Warn, "co-op: coop-sync stopped (%v); starting it again.", err)
				}
				coopAt = d.now().Add(d.retry)
			default:
			}
		}
		if cs := g.coopSide(); cs != nil && d.coopSync != nil && pipeDone != nil && coopDone == nil && !d.now().Before(coopAt) && ctx.Err() == nil {
			cctx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			pid := pipePID
			go func() { done <- d.coopSync(cctx, g.name, g.addr, pid, g.out, cs) }()
			coopCancel, coopDone = cancel, done
		}
		g.set(info)

		select {
		case <-ctx.Done():
			stopPipes()
			g.set(info)
			g.finish(played)
			return
		case <-g.relaunch:
			if info.PID == 0 && !g.o.Manual {
				doLaunch()
				g.set(info)
			}
		case <-t.C:
		}
	}
}

// badLog says once what the Dolphin runs instead of the patched game.
func (g *gameRunner) badLog(st session.DolphinState) {
	switch st {
	case session.DolphinWrongGame:
		report.Logf(g.out, report.Err, "Dolphin is running a different game. Boot your patched Wind Waker (%s) in it.", g.o.ISO)
	case session.DolphinUnpatched:
		report.Logf(g.out, report.Err, "Dolphin is running Wind Waker without this version's mod. Boot your patched disc (%s), or patch it again in Setup.", g.o.ISO)
	}
}

// finish resets the mailbox of every Dolphin the pipelines ran on: the
// pipelines are stopped by now, so nothing writes pose-feed again.
func (g *gameRunner) finish(played map[uint32]bool) {
	pids := make([]uint32, 0, len(played))
	for pid := range played {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	for _, pid := range pids {
		g.deps.reset(pid)
	}
	if len(pids) > 0 {
		g.out.Log(report.Info, "Left the game: remote Links are gone from your Dolphin, which keeps running.")
	}
}

// runPosePipelinesOn runs broadcast-pose and puppet-sync on Dolphin pid,
// as runMultiplayerGoroutines does on the env's Dolphin (no co-op).
func runPosePipelinesOn(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, rep report.Reporter, onClient func(*network.Client), onPos func(x, y, z float32)) error {
	connLog := newConnectLog(addr, rep)
	return runPipelines(ctx, cancel, rep,
		pipeline{"broadcast-pose", func(ctx context.Context) error {
			return runBroadcastPoseCtx(ctx, name, addr, pid, rep, connLog, onPos)
		}},
		pipeline{"puppet-sync", func(ctx context.Context) error {
			return runPuppetSyncCtx(ctx, name, addr, name, pid, rep, connLog, onClient)
		}},
	)
}

// resetMailboxOn is clearMultiplayerState for Dolphin pid, plus the tunic
// slots off (puppet-sync turns them off as it stops; this makes sure),
// unless WW_TUNIC=off leaves them to the tunic-slot command.
func resetMailboxOn(pid uint32) {
	d, err := dolphin.FindByPID(pid, session.GameID)
	if err != nil {
		return // closed: the mailbox went with it
	}
	defer d.Close()
	resetMailbox(d, os.Getenv("WW_TUNIC") != "off")
}

// mailboxMem reads and writes emulated RAM; *dolphin.Dolphin implements
// it.
type mailboxMem interface {
	modMem
	WriteAbsolute(addr uint32, data []byte) error
}

// resetMailbox resets the mailbox in m (tunic slots too when tunic), only
// when m still runs this build's mod: a Dolphin that booted the vanilla
// disc since has game heap at the mailbox's address. It says whether it
// wrote.
func resetMailbox(m mailboxMem, tunic bool) bool {
	if !hasThisMod(m) {
		return false
	}
	m.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{0})
	for i := 0; i < maxRemoteLinks; i++ {
		m.WriteAbsolute(mailboxBase+mailboxPoseSeq(i), []byte{0})
	}
	if tunic {
		m.WriteAbsolute(mailboxBase+mailboxTunicEnable, make([]byte, tunicSlots))
	}
	return true
}

// mailboxClaimed says Dolphin pid's mailbox has shadow_mode set: a
// puppet-sync (another session's, when it isn't ours) plays on it.
func mailboxClaimed(pid uint32) bool {
	d, err := dolphin.FindByPID(pid, session.GameID)
	if err != nil {
		return false
	}
	defer d.Close()
	if !hasThisMod(d) {
		return false
	}
	b, err := d.ReadAbsolute(mailboxBase+mailboxShadowMode, 1)
	return err == nil && len(b) == 1 && b[0] != 0
}

// launchDolphinFor launches o's Dolphin on o's user folder with o's disc,
// after putting the bundled cheats there when asked (as dolphin2 does).
func launchDolphinFor(o session.LaunchOptions, rep report.Reporter) (uint32, error) {
	if o.InstallCheats {
		dolphinenv.EnsureCheatsIni(rep, o.UserDir, bundledCheatsIni)
	}
	pid, err := dolphinenv.Launch(o.Exe, o.UserDir, o.ISO, "")
	if err != nil {
		return 0, err
	}
	return uint32(pid), nil
}

// runningDolphins lists the running Dolphins with their user folders and
// states.
func runningDolphins() []session.RunningDolphin {
	env := dolphinenv.Host()
	var out []session.RunningDolphin
	for _, p := range dolphinenv.Processes() {
		dir, _ := dolphinenv.ProcessUserDir(env, p)
		arg, explicit := dolphinenv.UserDirArg(p.Args)
		r := session.RunningDolphin{PID: p.PID, UserDir: dir, Explicit: explicit && arg != "" && p.ArgsKnown,
			State: session.DolphinStateOf(probeDolphin(p.PID), false)}
		if r.State.Playable() {
			r.Claimed = mailboxClaimed(p.PID)
		}
		out = append(out, r)
	}
	return out
}

// probeDolphin looks at Dolphin pid: running, its game, this build's mod
// in its memory, Link in the world.
func probeDolphin(pid uint32) session.Probe {
	pids, _ := dolphin.ListPIDs()
	if !slices.Contains(pids, pid) {
		return session.Probe{}
	}
	p := session.Probe{Running: true}
	id, err := dolphin.DiscID(pid)
	if err != nil || id == "" {
		return p
	}
	p.GameID = id
	if id != session.GameID {
		return p
	}
	d, err := dolphin.FindByPID(pid, session.GameID)
	if err != nil {
		p.GameID = "" // its RAM moved under us: look again
		return p
	}
	defer d.Close()
	p.Patched = hasThisMod(d)
	p.InGame = d.IsInGame()
	return p
}

// modMem reads emulated RAM; *dolphin.Dolphin implements it.
type modMem interface {
	ReadAbsolute(addr uint32, n int) ([]byte, error)
}

// hasThisMod says this build's injected code (inject.T2Bytes) is in RAM
// at inject.T2Address: at least 90% of its non-zero bytes match (a few
// are variables the mod changes as it runs). An unpatched game has its
// heap there; another build's mod is laid out differently.
func hasThisMod(m modMem) bool {
	got, err := m.ReadAbsolute(inject.T2Address, len(inject.T2Bytes))
	if err != nil || len(got) != len(inject.T2Bytes) {
		return false
	}
	return modMatch(got, inject.T2Bytes) >= 0.9
}

// modMatch is the share of want's non-zero bytes that got has too.
func modMatch(got, want []byte) float64 {
	n, same := 0, 0
	for i, b := range want {
		if b == 0 {
			continue
		}
		n++
		if i < len(got) && got[i] == b {
			same++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(same) / float64(n)
}

// envDolphinPID is WW_DOLPHIN_PID, 0 when unset or bad.
func envDolphinPID() uint32 {
	var pid uint32
	if v := strings.TrimSpace(os.Getenv("WW_DOLPHIN_PID")); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &pid); err != nil {
			return 0
		}
	}
	return pid
}
