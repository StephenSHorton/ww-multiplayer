package main

// smoke: the scripted two-Dolphin smoke with a pass/fail exit code
// (ww-online review idea 23). datstarkey/ww-online's native test calls a
// run a pass once frames were reached and screenshots saved
// (scripts/dev-test-native.sh:155; NativeTestRun.cs:40-79): it checks no
// sync. This one asserts sync at memory level, unattended, so an agent
// can run it on the owner's machine:
//
//   - setup: boot two fixture Dolphins with dolphin2 (only on user dirs
//     set explicitly to test dirs that are no player's profile, with
//     WW_DOLPHIN_DEVLOG's rules) or attach to two running ones (whose
//     user dirs dolphins.json or USER_DIR_1/2 name the same way), both
//     in-game, both games' contract check passing;
//   - mp-local as a child process (MP_LOCAL_PIDS pins A and B,
//     MP_LOCAL_STOP_FILE stops it cleanly at the end);
//   - steady: on both games the heartbeat advances, the REL is this
//     build's and runs, JUTException caught nothing, the visuals block's
//     last error is 0, the pose is published and fed into a mini-Link;
//   - walk: each Link in turn walks (the pad shim) and the other game's
//     puppet slot and mini-Link root for it end within a tolerance of it;
//   - hop: A hops to another stage and back (stage-hop's checked start),
//     both games keep running and the walk check passes again;
//   - optional: co-op bind on both files (-- --coop...), one memory
//     switch set on A reaching B (--switch), and the smoothing step log
//     under injected jitter (--smooth, smooth-steps).
//
// The report (every check with its numbers) is logs/latest/smoke.json,
// beside mp-local's own logs/latest files, with smoke.log; exit 0 pass,
// 1 a check failed, 2 the smoke could not run.

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// Exit codes.
const (
	smokeExitPass   = 0
	smokeExitFail   = 1 // a check failed
	smokeExitCannot = 2 // the smoke could not run: usage, no Dolphins, a refused user dir, mp-local never came up
)

const smokeUsage = "smoke [--names A,B] [--no-boot] [--hop \"<stage> <point> [room]\"|none] [--tol 50] [--min-move 100] [--walk-ms 1500] [--switch <0-127>] [--smooth <hz>[:<jitter ms>[:<latency ms>]]] [--smooth-max-cv F] [--ready-timeout 120s] [--out FILE] [-v] [-- mp-local args, e.g. --coop=new]"

// smokeOpts are smoke's flags.
type smokeOpts struct {
	nameA, nameB string
	noBoot       bool
	hop          string // "" none
	tol, minMove float64
	walk         time.Duration
	coopSwitch   int // -1 none
	smooth       string
	smoothMaxCV  float64
	readyTimeout time.Duration
	out          string
	verbose      bool
	mpArgs       []string // after --, to mp-local
}

// parseSmokeArgs reads smoke's arguments.
func parseSmokeArgs(args []string) (smokeOpts, error) {
	o := smokeOpts{coopSwitch: -1}
	if i := slices.Index(args, "--"); i >= 0 {
		o.mpArgs = slices.Clone(args[i+1:])
		args = args[:i]
	}
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	names := fs.String("names", "PlayerA,PlayerB", "")
	fs.BoolVar(&o.noBoot, "no-boot", false, "")
	fs.StringVar(&o.hop, "hop", "sea 0 44", "")
	fs.Float64Var(&o.tol, "tol", 50, "")
	fs.Float64Var(&o.minMove, "min-move", 100, "")
	walkMS := fs.Int("walk-ms", 1500, "")
	fs.IntVar(&o.coopSwitch, "switch", -1, "")
	fs.StringVar(&o.smooth, "smooth", "", "")
	fs.Float64Var(&o.smoothMaxCV, "smooth-max-cv", 0, "")
	fs.DurationVar(&o.readyTimeout, "ready-timeout", 120*time.Second, "")
	fs.StringVar(&o.out, "out", "", "")
	fs.BoolVar(&o.verbose, "v", false, "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments %q (mp-local's go after --)", fs.Args())
	}
	n := strings.Split(*names, ",")
	if len(n) != 2 || n[0] == "" || n[1] == "" || n[0] == n[1] {
		return o, fmt.Errorf("--names wants two different names, A,B, not %q", *names)
	}
	o.nameA, o.nameB = strings.TrimSpace(n[0]), strings.TrimSpace(n[1])
	if strings.EqualFold(strings.TrimSpace(o.hop), "none") {
		o.hop = ""
	}
	if o.tol <= 0 || o.minMove <= 0 {
		return o, errors.New("--tol and --min-move must be above 0")
	}
	if *walkMS < 200 || *walkMS > 10000 {
		return o, errors.New("--walk-ms must be 200 to 10000")
	}
	o.walk = time.Duration(*walkMS) * time.Millisecond
	if o.coopSwitch != -1 {
		if o.coopSwitch < 0 || o.coopSwitch >= addr.MemBitSwitchCount {
			return o, fmt.Errorf("--switch must be a memory switch, 0 to %d", addr.MemBitSwitchCount-1)
		}
		if !o.coop() {
			return o, errors.New("--switch needs co-op: add -- --coop=new (or a world) for mp-local")
		}
	}
	if o.smooth != "" {
		if _, err := parseTestRateArg(o.smooth); err != nil {
			return o, err
		}
	}
	return o, nil
}

// coop reports whether mp-local runs with co-op (a --coop flag after --).
func (o smokeOpts) coop() bool {
	return slices.ContainsFunc(o.mpArgs, func(a string) bool { return a == "--coop" || strings.HasPrefix(a, "--coop=") })
}

// smokeReport is smoke.json.
type smokeReport struct {
	Started  time.Time         `json:"started"`
	Ended    time.Time         `json:"ended"`
	Outcome  string            `json:"outcome"` // pass, fail, cannot run: <why>, interrupted
	ExitCode int               `json:"exit_code"`
	Args     []string          `json:"args"`
	PIDs     [2]uint32         `json:"pids"`
	Booted   bool              `json:"booted"`
	Checks   []smokeCheck      `json:"checks"`
	Failed   []string          `json:"failed,omitempty"`
	Smooth   *stepRun          `json:"smoothing,omitempty"`
	Logs     map[string]string `json:"logs,omitempty"`
}

// smokeRun is one smoke run's state.
type smokeRun struct {
	o       smokeOpts
	rep     log.Reporter
	logBuf  *strings.Builder
	report  smokeReport
	a, b    *emu.Dolphin
	child   *mpLocalChild
	ctx     context.Context
	phase   string
	cannot  string // why the smoke could not run
	started bool   // mp-local started (it made logs/latest)
}

func (r *smokeRun) add(cs ...smokeCheck) {
	for _, c := range cs {
		c.Phase = r.phase
		r.report.Checks = append(r.report.Checks, c)
		switch {
		case c.Skipped:
			log.Logf(r.rep, log.Info, "SKIP %s: %s", c.Name, c.Detail)
		case c.Pass:
			log.Logf(r.rep, log.OK, "PASS %s: %s", c.Name, c.Detail)
		default:
			log.Logf(r.rep, log.Err, "FAIL %s: %s", c.Name, c.Detail)
		}
	}
}

// runSmoke is `smoke`.
func runSmoke(_ string, args []string) {
	o, err := parseSmokeArgs(args)
	if err != nil {
		exitUsage(err, smokeUsage)
	}
	buf := &strings.Builder{}
	r := &smokeRun{o: o, logBuf: buf}
	r.rep = log.Multi(log.Stdout{}, &fileReporter{w: buf, now: time.Now})
	r.report = smokeReport{Started: time.Now(), Args: args}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r.ctx = ctx

	r.run()
	os.Exit(r.finish())
}

// run runs the phases; a setup failure sets cannot and returns.
func (r *smokeRun) run() {
	r.phase = "setup"
	if !r.setup() {
		return
	}
	defer r.a.Close()
	defer r.b.Close()
	defer r.stopChild()

	r.phase = "steady"
	r.steady("")
	if r.ctx.Err() != nil {
		return
	}
	r.phase = "walk"
	r.walkBoth()
	if r.o.hop != "" && r.ctx.Err() == nil {
		r.phase = "hop"
		r.hop()
	}
	if r.o.coop() && r.ctx.Err() == nil {
		r.phase = "coop"
		r.coop()
	}
	if r.o.smooth != "" && r.ctx.Err() == nil {
		r.phase = "smooth"
		r.smoothing()
	}
	if r.ctx.Err() == nil {
		r.phase = "end"
		r.steady("end")
		r.add(passIf(r.child.running(), "mp-local alive", "mp-local still running at the end", nil))
	}
}

// finish writes the report and logs and returns the exit code.
func (r *smokeRun) finish() int {
	rep := &r.report
	rep.Ended = time.Now()
	for _, c := range rep.Checks {
		if !c.Pass && !c.Skipped {
			rep.Failed = append(rep.Failed, c.Name)
		}
	}
	switch {
	case r.ctx.Err() != nil:
		rep.Outcome, rep.ExitCode = "interrupted", smokeExitCannot
	case r.cannot != "":
		rep.Outcome, rep.ExitCode = "cannot run: "+r.cannot, smokeExitCannot
	case len(rep.Failed) > 0:
		rep.Outcome, rep.ExitCode = "fail", smokeExitFail
	default:
		rep.Outcome, rep.ExitCode = "pass", smokeExitPass
	}
	if r.cannot != "" {
		log.Logf(r.rep, log.Err, "smoke could not run: %s", r.cannot)
	}
	log.Logf(r.rep, log.Info, "smoke: %s (%d checks, %d failed%s)", rep.Outcome, len(rep.Checks), len(rep.Failed), failedList(rep.Failed))

	dir := ""
	if root := devLogsRoot(os.Getenv); root != "" {
		if r.started {
			dir = filepath.Join(root, "latest") // mp-local made it this run
		} else if d, _, err := rotateLatest(root); err == nil {
			dir = d
		} else {
			log.Logf(r.rep, log.Warn, "run logs: %v", err)
		}
	}
	if dir != "" {
		rep.Logs = map[string]string{"smoke": filepath.Join(dir, "smoke.log")}
		if r.started {
			rep.Logs["mp_local"] = filepath.Join(dir, "mp-local.log")
			for _, l := range []string{"A", "B"} {
				if p := filepath.Join(dir, "dolphin-"+l+".log"); fileSize(p) > 0 {
					rep.Logs["dolphin_"+l] = p
				}
			}
		}
	}
	var paths []string
	if dir != "" {
		paths = append(paths, filepath.Join(dir, "smoke.json"))
	}
	if r.o.out != "" {
		paths = append(paths, r.o.out)
	}
	if len(paths) == 0 {
		paths = []string{"smoke.json"}
	}
	for _, p := range paths {
		if err := writeJSON(p, rep); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: writing %s: %v\n", p, err)
		} else {
			log.Logf(r.rep, log.Info, "Report: %s", p)
		}
	}
	if dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "smoke.log"), []byte(r.logBuf.String()), 0o644)
	}
	return rep.ExitCode
}

func failedList(f []string) string {
	if len(f) == 0 {
		return ""
	}
	return ": " + strings.Join(f, ", ")
}

// setup finds or boots the two Dolphins, checks them and starts mp-local.
func (r *smokeRun) setup() bool {
	exe, err := os.Executable()
	if err != nil {
		r.cannot = err.Error()
		return false
	}
	paths := launch.FromEnv()
	pids, _ := emu.ListPIDs()
	switch {
	case len(pids) == 0 && r.o.noBoot:
		r.cannot = "no Dolphin is running (and --no-boot)"
		return false
	case len(pids) == 0:
		if why := bootRefusal(launch.Host(), os.Getenv, paths); why != "" {
			r.cannot = why
			return false
		}
		log.Logf(r.rep, log.Info, "Booting two Dolphins on the test user dirs %s and %s (dolphin2, WW_DOLPHIN_DEVLOG=1) ...", paths.UserDir1, paths.UserDir2)
		cmd := exec.CommandContext(r.ctx, exe, "dolphin2")
		cmd.Env = append(os.Environ(), launch.EnvDevLog+"=1")
		out, err := cmd.CombinedOutput()
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			log.Logf(r.rep, log.Info, "[dolphin2] %s", strings.TrimRight(l, "\r"))
		}
		if err != nil {
			r.cannot = fmt.Sprintf("dolphin2: %v", err)
			return false
		}
		r.report.Booted = true
		deadline := time.Now().Add(30 * time.Second)
		for len(pids) < 2 && time.Now().Before(deadline) && r.ctx.Err() == nil {
			sleepCtx(r.ctx, 500*time.Millisecond)
			pids, _ = emu.ListPIDs()
		}
	case len(pids) == 1:
		r.cannot = "one Dolphin is running: start both with dolphin2 (or close it and let smoke boot two)"
		return false
	}
	pidA, pidB, err := smokePIDsFrom(pids, os.Getenv, readLaunchRecords(devLogsRootOr(os.Getenv)), processStart)
	if err != nil {
		r.cannot = err.Error()
		return false
	}
	r.report.PIDs = [2]uint32{pidA, pidB}
	if !r.report.Booted {
		recs := readLaunchRecords(devLogsRootOr(os.Getenv))
		if why := attachRefusal([2]uint32{pidA, pidB}, recs, os.Getenv, launch.PlayerUserDirs(launch.Host(), paths.Exe), processStart); why != "" {
			r.cannot = why
			return false
		}
	}
	log.Logf(r.rep, log.OK, "Dolphin A pid %d, B pid %d%s.", pidA, pidB, map[bool]string{true: " (booted)", false: " (attached)"}[r.report.Booted])
	if err := waitForReady(r.ctx, r.rep, pidA, pidB, r.o.readyTimeout); err != nil {
		r.cannot = err.Error()
		return false
	}
	if r.a, err = emu.FindByPID(pidA, "GZLE01"); err != nil {
		r.cannot = err.Error()
		return false
	}
	if r.b, err = emu.FindByPID(pidB, "GZLE01"); err != nil {
		r.a.Close()
		r.cannot = err.Error()
		return false
	}

	ok := true
	for i, d := range []*emu.Dolphin{r.a, r.b} {
		label := string(rune('A' + i))
		c, err := wwapi.CheckContract(d, wwapi.ContractOptions{})
		switch {
		case err != nil:
			r.add(passIf(false, "contract "+label, err.Error(), nil))
			ok = false
		default:
			r.add(passIf(c.WritesAllowed, "contract "+label, wwapi.ContractLine(c), nil))
			ok = ok && c.WritesAllowed
		}
	}
	// Before mp-local: a paused or frozen game fails here, not later.
	r.phase = "pre"
	for i, d := range []*emu.Dolphin{r.a, r.b} {
		label := string(rune('A' + i))
		ss, secs := sampleGame(r.ctx, d, 2*time.Second, 100*time.Millisecond)
		for _, c := range livenessChecks(label, ss, secs, smokeLimits{MinFPS: 10}) {
			if strings.HasPrefix(c.Name, "heartbeat") || strings.HasPrefix(c.Name, "no crash") || strings.HasPrefix(c.Name, "read") {
				r.add(c)
				ok = ok && c.Pass
			}
		}
	}
	if !ok {
		r.a.Close()
		r.b.Close()
		return false // the checks say why; no mp-local into a game it may not write
	}

	r.phase = "setup"
	child, err := startMpLocalChild(r.ctx, exe, r.o, pidA, pidB, r.rep)
	if err != nil {
		r.a.Close()
		r.b.Close()
		r.cannot = err.Error()
		return false
	}
	r.child, r.started = child, true
	if err := r.waitArmed(90 * time.Second); err != nil {
		r.add(passIf(false, "mp-local up", err.Error(), nil))
		r.stopChild()
		r.a.Close()
		r.b.Close()
		return false
	}
	r.add(passIf(true, "mp-local up", "both games' pose feed armed (shadow_mode 5, a pose slot written)", nil))
	return true
}

// waitArmed waits until mp-local armed both games: shadow_mode 5 and a
// pose_seqs byte written on each.
func (r *smokeRun) waitArmed(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !r.child.running() {
			return fmt.Errorf("mp-local exited before arming (%s)", r.child.exitNote())
		}
		armed := true
		for _, d := range []*emu.Dolphin{r.a, r.b} {
			s := readGameSample(d)
			armed = armed && s.ShadowMode == avatar.ModePoseFeed && slices.ContainsFunc(s.PoseSeqs[:], func(b uint8) bool { return b != 0 })
		}
		if armed {
			return nil
		}
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		sleepCtx(r.ctx, 250*time.Millisecond)
	}
	return fmt.Errorf("mp-local did not arm both games within %s", timeout)
}

// sampleGame reads m every every for d; secs is the time the samples span.
func sampleGame(ctx context.Context, m mem.Reader, d, every time.Duration) ([]gameSample, float64) {
	t0 := time.Now()
	var ss []gameSample
	for {
		ss = append(ss, readGameSample(m))
		if time.Since(t0) >= d || ctx.Err() != nil {
			break
		}
		sleepCtx(ctx, every)
	}
	return ss, time.Since(t0).Seconds()
}

// steady samples both games for 3 s and judges liveness and pose flow;
// tag goes into the check names ("" for the first run).
func (r *smokeRun) steady(tag string) {
	lim := smokeLimits{MinFPS: 10, SeqChanges: 5}
	var wg sync.WaitGroup
	var res [2][]smokeCheck
	for i, d := range []*emu.Dolphin{r.a, r.b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := string(rune('A' + i))
			ss, secs := sampleGame(r.ctx, d, 3*time.Second, 100*time.Millisecond)
			res[i] = append(livenessChecks(label, ss, secs, lim), poseFlowChecks(label, ss, lim)...)
		}()
	}
	wg.Wait()
	for _, cs := range res {
		for _, c := range cs {
			if tag != "" {
				c.Name += " (" + tag + ")"
			}
			r.add(c)
		}
	}
}

// walkBoth runs the walk check A->B, then B->A.
func (r *smokeRun) walkBoth() {
	r.add(r.walk("track A on B", r.a, r.b))
	r.add(r.walk("track B on A", r.b, r.a))
}

// smokeSticks are the walk's directions, tried in turn until Link moves.
var smokeSticks = [][2]int8{{0, 100}, {0, -100}, {100, 0}, {-100, 0}}

// walk walks mover's Link (the pad shim) and checks observer's view of
// it after a settle.
func (r *smokeRun) walk(name string, mover, observer mem.Mem) smokeCheck {
	var o trackObs
	var err error
	for _, st := range smokeSticks {
		if o.MoverStart, err = linkPos(mover); err != nil {
			return passIf(false, name, "the walking Link: "+err.Error(), nil)
		}
		if err := player.Override(mover, player.Pad{StickX: st[0], StickY: st[1]}); err != nil {
			return passIf(false, name, "input: "+err.Error(), nil)
		}
		sleepCtx(r.ctx, r.o.walk)
		_ = player.Release(mover)
		sleepCtx(r.ctx, 1200*time.Millisecond) // Link stops, puppet-sync's lerp settles
		if o.MoverEnd, err = linkPos(mover); err != nil {
			return passIf(false, name, "the walking Link: "+err.Error(), nil)
		}
		if vdist(o.MoverStart, o.MoverEnd) >= r.o.minMove || r.ctx.Err() != nil {
			break
		}
		log.Logf(r.rep, log.Info, "%s: Link moved %.1f with stick (%d, %d); trying another way", name, vdist(o.MoverStart, o.MoverEnd), st[0], st[1])
	}
	if root, err := publishedRoot(mover); err == nil {
		o.MoverRoot, o.HaveMoverRoot = root, true
	}
	o.Slot, o.SlotPos, o.HaveSlot = remoteSlotPos(observer, o.MoverEnd)
	if o.HaveMoverRoot {
		o.PoseSlot, o.PoseRoot, o.HavePose = remotePoseRoot(observer, o.MoverRoot)
	}
	return trackCheck(name, o, r.o.minMove, r.o.tol)
}

// hop sends A to --hop's start and back, checking both games survive
// each and the walk check after.
func (r *smokeRun) hop() {
	pos := strings.Fields(r.o.hop)
	h, err := parseStageHop(pos)
	if err != nil {
		r.add(passIf(false, "hop", "--hop: "+err.Error(), nil))
		return
	}
	origin, err := state.Read(r.a, state.Options{})
	if err != nil {
		r.add(passIf(false, "hop", "reading A's stage: "+err.Error(), nil))
		return
	}
	w := &reporterWriter{rep: r.rep}
	iso := launch.FromEnv().ISO
	if err := verifyStageHop(w, iso, &h, len(pos) > 2); err != nil {
		r.add(passIf(false, "hop out", err.Error(), nil))
		return
	}
	if err := stageHopRun(w, r.a, h, diag.Tick); err != nil {
		r.add(passIf(false, "hop out", fmt.Sprintf("A to %s %d %d: %v", h.Name, h.Point, h.Room, err), nil))
		return
	}
	r.add(passIf(true, "hop out", fmt.Sprintf("A went from %s to %s point %d room %d", diag.Where(origin), h.Name, h.Point, h.Room), nil))
	sleepCtx(r.ctx, 2*time.Second)
	r.steadyHeartbeats("after hop out")

	back := stageHop{Name: origin.StageName, Point: origin.StagePoint, Room: origin.StageRoom, Layer: -1}
	if err := verifyStageHop(w, iso, &back, true); err != nil {
		r.add(passIf(false, "hop back", err.Error(), nil))
		return
	}
	if err := stageHopRun(w, r.a, back, diag.Tick); err != nil {
		r.add(passIf(false, "hop back", fmt.Sprintf("A to %s %d %d: %v", back.Name, back.Point, back.Room, err), nil))
		return
	}
	r.add(passIf(true, "hop back", fmt.Sprintf("A back in %s point %d room %d", back.Name, back.Point, back.Room), nil))
	sleepCtx(r.ctx, 3*time.Second) // the pose feed re-arms after the stage change
	r.steady("after hop")
	r.add(r.walk("track A on B (after hop)", r.a, r.b))
}

// steadyHeartbeats checks only that both games keep running (the F1
// class of freeze), as a stage loads.
func (r *smokeRun) steadyHeartbeats(tag string) {
	for i, d := range []*emu.Dolphin{r.a, r.b} {
		label := string(rune('A' + i))
		ss, secs := sampleGame(r.ctx, d, 3*time.Second, 100*time.Millisecond)
		for _, c := range livenessChecks(label, ss, secs, smokeLimits{MinFPS: 10}) {
			if strings.HasPrefix(c.Name, "heartbeat") || strings.HasPrefix(c.Name, "no crash") || strings.HasPrefix(c.Name, "read") {
				c.Name += " (" + tag + ")"
				r.add(c)
			}
		}
	}
}

// coop waits for both files to be bound to one world, then runs the
// optional switch check.
func (r *smokeRun) coop() {
	deadline := time.Now().Add(60 * time.Second)
	var cs []smokeCheck
	for {
		fa, errA := bind.ReadFile(r.a)
		fb, errB := bind.ReadFile(r.b)
		if err := errors.Join(errA, errB); err != nil {
			cs = []smokeCheck{passIf(false, "coop bind", err.Error(), nil)}
		} else {
			cs = bindChecks(fa, fb)
		}
		if allPass(cs) || time.Now().After(deadline) || r.ctx.Err() != nil {
			break
		}
		sleepCtx(r.ctx, time.Second)
	}
	r.add(cs...)
	if r.o.coopSwitch >= 0 && allPass(cs) {
		r.add(r.switchSync(r.o.coopSwitch, 20*time.Second))
	}
}

func allPass(cs []smokeCheck) bool {
	for _, c := range cs {
		if !c.Pass {
			return false
		}
	}
	return len(cs) > 0
}

// switchSync sets memory switch no in A's live memBit (onSwitch's bit,
// d_save.cpp:1084-1087, addr.SwitchBit) and waits for it in B's. Co-op
// syncs only switches its stage tables class as latching, so the switch
// must be one of those (docs/TESTING.md SM3).
func (r *smokeRun) switchSync(no int, timeout time.Duration) smokeCheck {
	name := fmt.Sprintf("coop switch 0x%02X", no)
	_, bit, err := addr.SwitchBit(no)
	if err != nil {
		return passIf(false, name, err.Error(), nil)
	}
	at := addr.LiveMemBit + uint32(bit.Off)
	isSet := func(m mem.Reader) (bool, error) {
		v, err := mem.ReadU8(m, at)
		return v&bit.Mask != 0, err
	}
	if on, err := isSet(r.b); err != nil {
		return passIf(false, name, err.Error(), nil)
	} else if on {
		return smokeCheck{Name: name, Skipped: true, Detail: "already set on B: pick a switch the file has not set"}
	}
	_, st, _, err := diag.GateSnapshots(r.a, diag.Tick)
	if err != nil {
		return passIf(false, name, err.Error(), nil)
	}
	if !st.CanWriteIdle() {
		return passIf(false, name, "A is not idle: "+st.Why(), nil)
	}
	v, err := mem.ReadU8(r.a, at)
	if err == nil {
		err = mem.WriteU8(r.a, at, v|bit.Mask)
	}
	if err != nil {
		return passIf(false, name, "setting it on A: "+err.Error(), nil)
	}
	t0 := time.Now()
	for time.Since(t0) < timeout && r.ctx.Err() == nil {
		if on, err := isSet(r.b); err == nil && on {
			return passIf(true, name, fmt.Sprintf("set on A (0x%08X mask 0x%02X), on B after %.1f s", at, bit.Mask, time.Since(t0).Seconds()), nil)
		}
		sleepCtx(r.ctx, 250*time.Millisecond)
	}
	return passIf(false, name, fmt.Sprintf("set on A (0x%08X mask 0x%02X), not on B after %s (is it a latching switch co-op syncs?)", at, bit.Mask, timeout), nil)
}

// smoothing runs smooth-steps' measurement (A walks, B observes) under
// mp-local's WW_POSE_TEST_RATE (--smooth).
func (r *smokeRun) smoothing() {
	run, err := measureSmoothing(r.ctx, r.a, r.b, 5*time.Second)
	if err != nil {
		r.add(passIf(false, "smoothing", err.Error(), nil))
		return
	}
	run.Walker, run.Summary.Rate = "A", r.o.smooth
	r.report.Smooth = &run
	s := run.Summary
	ok := s.Remote.Steps > 0 && (r.o.smoothMaxCV == 0 || s.Remote.CV <= r.o.smoothMaxCV)
	limit := "no limit set (--smooth-max-cv)"
	if r.o.smoothMaxCV > 0 {
		limit = fmt.Sprintf("cv at most %.3f", r.o.smoothMaxCV)
	}
	r.add(passIf(ok, "smoothing", fmt.Sprintf("rate %s: %s; %s", r.o.smooth, s, limit),
		map[string]any{"cv": s.Remote.CV, "ratio": s.Ratio, "stalls": s.Remote.Stalls, "jumps": s.Remote.Jumps, "behind": s.Behind}))
}

// stopChild stops mp-local (the stop file, then a kill after 30 s, after
// which smoke resets both games' pose feed itself, as mp-local's
// teardown would have).
func (r *smokeRun) stopChild() {
	if r.child == nil {
		return
	}
	c := r.child
	r.child = nil
	clean := c.stop(30 * time.Second)
	if !clean {
		for _, d := range []*emu.Dolphin{r.a, r.b} {
			if d != nil {
				avatar.Clear(d)
			}
		}
	}
	phase := r.phase
	r.phase = "teardown"
	r.add(passIf(clean, "mp-local stopped", c.exitNote(), nil))
	r.phase = phase
}

// reporterWriter is an io.Writer over a Reporter, a line per Info line.
type reporterWriter struct {
	rep log.Reporter
	buf string
}

func (w *reporterWriter) Write(p []byte) (int, error) {
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.rep.Log(log.Info, strings.TrimRight(w.buf[:i], "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// mpLocalChild is the mp-local process smoke runs.
type mpLocalChild struct {
	cmd      *exec.Cmd
	stopFile string
	done     chan struct{}
	err      error // Wait's, once done
}

// startMpLocalChild runs `wwdev mp-local <A> <B> <mpArgs>` with
// MP_LOCAL_PIDS and MP_LOCAL_STOP_FILE (and WW_POSE_TEST_RATE for
// --smooth); its output goes to rep with -v (mp-local writes its own
// logs/latest/mp-local.log either way).
func startMpLocalChild(ctx context.Context, exe string, o smokeOpts, pidA, pidB uint32, rep log.Reporter) (*mpLocalChild, error) {
	stopDir, err := os.MkdirTemp("", "wwdev-smoke-")
	if err != nil {
		return nil, err
	}
	c := &mpLocalChild{stopFile: filepath.Join(stopDir, "stop"), done: make(chan struct{})}
	args := append([]string{"mp-local", o.nameA, o.nameB}, o.mpArgs...)
	c.cmd = exec.Command(exe, args...)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(k, "MP_LOCAL_PIDS") || strings.EqualFold(k, envStopFile) ||
			(o.smooth != "" && strings.EqualFold(k, envPoseTestRate))
	})
	env = append(env, fmt.Sprintf("MP_LOCAL_PIDS=%d,%d", pidA, pidB), envStopFile+"="+c.stopFile)
	if o.smooth != "" {
		env = append(env, envPoseTestRate+"="+o.smooth)
	}
	c.cmd.Env = env
	pr, pw := io.Pipe()
	c.cmd.Stdout, c.cmd.Stderr = pw, pw
	if err := c.cmd.Start(); err != nil {
		os.RemoveAll(stopDir)
		return nil, fmt.Errorf("starting mp-local: %w", err)
	}
	log.Logf(rep, log.OK, "mp-local started (pid %d): %s", c.cmd.Process.Pid, strings.Join(args, " "))
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if o.verbose {
				log.Logf(rep, log.Info, "[mp-local] %s", sc.Text())
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	go func() {
		c.err = c.cmd.Wait()
		pw.Close()
		os.RemoveAll(stopDir)
		close(c.done)
	}()
	return c, nil
}

func (c *mpLocalChild) running() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// stop writes the stop file and waits up to wait for mp-local to tear
// down; then it kills it. clean is false after a kill or a non-zero exit.
func (c *mpLocalChild) stop(wait time.Duration) (clean bool) {
	if c.running() {
		_ = os.WriteFile(c.stopFile, []byte("smoke\n"), 0o644)
		select {
		case <-c.done:
		case <-time.After(wait):
			_ = c.cmd.Process.Kill()
			<-c.done
			return false
		}
	}
	return c.err == nil
}

func (c *mpLocalChild) exitNote() string {
	select {
	case <-c.done:
	default:
		return "still running"
	}
	if c.err != nil {
		return "mp-local exited: " + c.err.Error()
	}
	return "mp-local exited 0 after its teardown"
}

// devLogsRootOr is devLogsRoot, or "logs" with the logs off (dolphin2
// writes no record then, so none is found).
func devLogsRootOr(getenv func(string) string) string {
	if r := devLogsRoot(getenv); r != "" {
		return r
	}
	return "logs"
}

// smokePIDs is smokePIDsFrom over the running Dolphins and dolphins.json.
func smokePIDs(getenv func(string) string) (a, b uint32, err error) {
	pids, _ := emu.ListPIDs()
	return smokePIDsFrom(pids, getenv, readLaunchRecords(devLogsRootOr(getenv)), processStart)
}

// smokePIDsFrom picks A and B among the running pids: MP_LOCAL_PIDS when
// it names two of them; else dolphin2's record of "Dolphin 1" and
// "Dolphin 2" when both are running (the same processes, by start
// token); else the two pids when there are exactly two, in order.
func smokePIDsFrom(pids []uint32, getenv func(string) string, recs []launchRecord, start func(uint32) string) (a, b uint32, err error) {
	if len(pids) < 2 {
		return 0, 0, fmt.Errorf("need 2 Dolphins running, found %d", len(pids))
	}
	if a, b, ok := pinnedPIDs(getenv("MP_LOCAL_PIDS"), pids); ok {
		return a, b, nil
	}
	var byLabel [2]uint32
	for _, rec := range recs {
		pid := uint32(rec.PID)
		if !slices.Contains(pids, pid) || (start != nil && !rec.sameProcess(start(pid))) {
			continue
		}
		switch rec.Label {
		case "Dolphin 1":
			byLabel[0] = pid
		case "Dolphin 2":
			byLabel[1] = pid
		}
	}
	if byLabel[0] != 0 && byLabel[1] != 0 {
		return byLabel[0], byLabel[1], nil
	}
	if len(pids) == 2 {
		return pids[0], pids[1], nil
	}
	return 0, 0, fmt.Errorf("%d Dolphins are running: set MP_LOCAL_PIDS=<A>,<B> to pick two", len(pids))
}

// bootRefusal is why smoke may not boot two Dolphins: it needs a save
// state (title-screen Dolphins never get in-game) and both user dirs set
// explicitly to test dirs that are no player's profile, the rule
// WW_DOLPHIN_DEVLOG launches already follow (launch.DevLogUserDirError).
func bootRefusal(e launch.Env, getenv func(string) string, p launch.Paths) string {
	if s := getenv("SAVE_STATE"); s == "" {
		return "booting needs SAVE_STATE=<a .sav> (or start two Dolphins in-game and smoke attaches)"
	} else if _, err := os.Stat(s); err != nil {
		return fmt.Sprintf("SAVE_STATE=%s: %v", s, err)
	}
	for _, u := range []struct{ dir, key string }{{p.UserDir1, launch.EnvUserDir1}, {p.UserDir2, launch.EnvUserDir2}} {
		if err := launch.DevLogUserDirError(e, p.Exe, u.dir, u.key, strings.TrimSpace(getenv(u.key)) != ""); err != nil {
			return err.Error()
		}
	}
	return ""
}

// attachRefusal is why smoke may not drive two running Dolphins (it
// walks their Links and hops their stages): each must run on a user dir
// that is no player's profile, known from dolphin2's record of it or,
// for a Dolphin it has no record of, declared by USER_DIR_1 (A) and
// USER_DIR_2 (B), set explicitly. players are the player profile dirs
// (launch.PlayerUserDirs).
func attachRefusal(pids [2]uint32, recs []launchRecord, getenv func(string) string, players []string, start func(uint32) string) string {
	for i, pid := range pids {
		label := string(rune('A' + i))
		key := []string{launch.EnvUserDir1, launch.EnvUserDir2}[i]
		dir, from := "", ""
		for _, rec := range recs {
			if uint32(rec.PID) == pid && (start == nil || rec.sameProcess(start(pid))) {
				dir, from = rec.UserDir, "dolphins.json"
			}
		}
		if dir == "" {
			dir, from = strings.TrimSpace(getenv(key)), key
			if dir == "" {
				return fmt.Sprintf("Dolphin %s (pid %d) was not started by dolphin2 and %s is not set: smoke drives only test Dolphins; set %s to its test user dir (never a player's profile), or let smoke boot them", label, pid, key, key)
			}
		}
		for _, p := range players {
			if sameDir(p, dir) {
				return fmt.Sprintf("Dolphin %s (pid %d) runs on %s (%s), a player's Dolphin profile: smoke drives only test user dirs", label, pid, dir, from)
			}
		}
	}
	return ""
}

// sameDir compares two directories as Windows and macOS do (no case),
// cleaned and without a trailing separator.
func sameDir(a, b string) bool {
	k := func(p string) string {
		p = filepath.Clean(strings.TrimSpace(p))
		if len(p) > 1 {
			p = strings.TrimRight(p, `\/`)
		}
		return strings.ReplaceAll(strings.ToLower(p), "/", `\`)
	}
	return k(a) == k(b)
}

// parseTestRateArg checks --smooth as WW_POSE_TEST_RATE would be read.
func parseTestRateArg(s string) (string, error) {
	r, err := feed.ParseTestRate(s)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "", errors.New("--smooth wants <hz>[:<jitter ms>[:<latency ms>]]")
	}
	return r.String(), nil
}
