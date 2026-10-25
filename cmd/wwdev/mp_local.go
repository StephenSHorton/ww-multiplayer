package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/cheats"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// bundledCheatsIni is the curated cheats/GZLE01.ini shipped in the repo
// (package cheats), baked into the binary so dolphin2 can drop it into
// Dolphin user dirs without needing the source tree alongside it.
var bundledCheatsIni = cheats.GZLE01

// dolphin2Defaults returns the OS-appropriate fallback paths for the
// dolphin2 bootstrap when DOLPHIN_EXE / ISO_PATH / USER_DIR_{1,2} are
// unset (launch.DefaultPaths).
func dolphin2Defaults() (exe, iso, userDir1, userDir2 string) {
	p := launch.DefaultPaths()
	return p.Exe, p.ISO, p.UserDir1, p.UserDir2
}

// runDolphin2 (the Go port of the retired scripts/dolphin2.sh) bootstraps the
// "Dolphin Emulator 2" user dir and launches the missing Dolphin
// instance(s) so two are running side-by-side. mp-local can then attach
// to both without any env-var juggling.
//
// Reads DOLPHIN_EXE, ISO_PATH, USER_DIR_1, USER_DIR_2 from env with
// the same defaults the bash script used (DOLPHIN_EXE points into the
// user's local Dolphin install; APPDATA-relative dir paths).
func runDolphin2(reset bool) {
	rep := log.Stdout{}

	paths := launch.FromEnv()
	dolphinExe, isoPath, userDir1, userDir2 := paths.Exe, paths.ISO, paths.UserDir1, paths.UserDir2
	// Optional save-state autoload: when set, both Dolphins boot directly
	// into the given .sav file (Dolphin's --save_state CLI flag), skipping
	// the title screen + save-select menus entirely. The user records a
	// state once via Dolphin's File > Save State menu, exports the .sav,
	// and points SAVE_STATE at it.
	//
	// SAVE_STATE_2, if set, overrides the save state used for Dolphin 2
	// (the second-launched instance). When both env vars are set, the
	// two Dolphins boot with distinct save states — used to force a btp
	// animation phase divergence so face-sync can be visually validated
	// under natural (non-synthetic) conditions (issue #5). Capture a
	// pair via `./wwdev.exe auto-recapture-pair`.
	saveState := os.Getenv("SAVE_STATE")
	saveState2 := os.Getenv("SAVE_STATE_2")

	if reset {
		log.Logf(rep, log.Info, "Removing %s ...", userDir2)
		if err := os.RemoveAll(userDir2); err != nil {
			rep.Log(log.Err, err.Error())
			os.Exit(1)
		}
	}

	if _, err := os.Stat(dolphinExe); err != nil {
		log.Logf(rep, log.Err, "Dolphin not found at %s (set DOLPHIN_EXE to override)", dolphinExe)
		os.Exit(1)
	}
	if _, err := os.Stat(isoPath); err != nil {
		log.Logf(rep, log.Err, "Patched ISO not found at %s", isoPath)
		log.Logf(rep, log.Err, "Run `./ww-multiplayer.exe patch <vanilla.iso>` or set ISO_PATH.")
		os.Exit(1)
	}
	if saveState != "" {
		if _, err := os.Stat(saveState); err != nil {
			log.Logf(rep, log.Err, "SAVE_STATE=%s does not exist", saveState)
			os.Exit(1)
		}
	}
	if saveState2 != "" {
		if _, err := os.Stat(saveState2); err != nil {
			log.Logf(rep, log.Err, "SAVE_STATE_2=%s does not exist", saveState2)
			os.Exit(1)
		}
		if saveState == "" {
			log.Logf(rep, log.Err, "SAVE_STATE_2 is set but SAVE_STATE is not; set both or neither")
			os.Exit(1)
		}
	}
	if _, err := os.Stat(userDir1); err != nil {
		log.Logf(rep, log.Err, "Primary Dolphin user dir not found at %s", userDir1)
		log.Logf(rep, log.Err, "Run Dolphin once normally to bootstrap it, then re-run dolphin2.")
		os.Exit(1)
	}

	if _, err := os.Stat(userDir2); err != nil {
		log.Logf(rep, log.Info, "Bootstrapping %s from %s (skipping Cache/) ...", userDir2, userDir1)
		// Skips Cache/, which a running Dolphin 1 holds locked.
		if err := launch.BootstrapUserDir(userDir1, userDir2); err != nil {
			log.Logf(rep, log.Err, "bootstrap: %v", err)
			os.Exit(1)
		}
		rep.Log(log.OK, "Bootstrap complete.")
	}

	ensureCheatsIni(rep, userDir1)
	ensureCheatsIni(rep, userDir2)

	pids, err := emu.ListPIDs()
	if err != nil {
		log.Logf(rep, log.Warn, "couldn't enumerate Dolphin PIDs: %v (will launch both)", err)
	}

	type launchPlan struct {
		label     string
		userDir   string
		saveState string
	}
	// Dolphin 2 uses saveState2 when set; otherwise it falls back to
	// saveState (preserving the prior single-save-state behavior).
	saveState2Effective := saveState2
	if saveState2Effective == "" {
		saveState2Effective = saveState
	}
	var plan []launchPlan
	switch len(pids) {
	case 0:
		plan = []launchPlan{
			{"Dolphin 1", userDir1, saveState},
			{"Dolphin 2", userDir2, saveState2Effective},
		}
	case 1:
		plan = []launchPlan{{"Dolphin 2", userDir2, saveState2Effective}}
	default:
		rep.Log(log.Info, "Two or more Dolphins already running — nothing to do.")
		return
	}

	// WW_DOLPHIN_DEVLOG=1: each Dolphin also logs to <user dir>/Logs/dolphin.log
	// and never stops on a panic popup (launch.DevLogArgs, session-only -C).
	devLog := launch.DevLogEnabled(os.Getenv)
	var recs []launchRecord
	for _, p := range plan {
		logPath := launch.LogFile(p.userDir)
		offset := fileSize(logPath) // Dolphin appends: this one's lines start here
		pid, err := launch.LaunchTest(dolphinExe, p.userDir, isoPath, p.saveState, devLog)
		if err != nil {
			log.Logf(rep, log.Err, "launch %s: %v", p.label, err)
			os.Exit(1)
		}
		log.Logf(rep, log.OK, "Launched %s (pid %d, user dir %s)", p.label, pid, p.userDir)
		if devLog {
			log.Logf(rep, log.Info, "  %s logs to %s (OSReport, MI, MASTER; panic popups off)", p.label, logPath)
		}
		recs = append(recs, launchRecord{
			Label: p.label, PID: pid, UserDir: p.userDir, SaveState: p.saveState, DevLog: devLog,
			LogPath: logPath, LogOffset: offset, Launched: time.Now(),
		})
	}
	// The record mp-local reads to tell which PID runs on which user dir
	// (WW_DEV_LOGS; off writes nothing).
	if root := devLogsRoot(os.Getenv); root != "" {
		running := slices.Clone(pids)
		for _, r := range recs {
			running = append(running, uint32(r.PID))
		}
		if err := saveLaunchRecords(root, running, recs); err != nil {
			log.Logf(rep, log.Warn, "couldn't record the launch in %s: %v", root, err)
		}
	}
	if saveState != "" {
		if saveState2 != "" && saveState2 != saveState {
			log.Logf(rep, log.Info, "Dolphin 1 booting from %s; Dolphin 2 from %s. Distinct btp phases — face-sync should bridge them once mp-local arms.", saveState, saveState2)
		} else {
			log.Logf(rep, log.Info, "Both Dolphins booting with save state %s. mp-local's wait-loop will detect when they're in-game.", saveState)
		}
	} else {
		rep.Log(log.Info, "Wait for both games to load to a save state, then run `./wwdev.exe mp-local`. (Set SAVE_STATE=<path> to skip the menus next time.)")
	}
}

// ensureCheatsIni drops the bundled cheats/GZLE01.ini into
// <userDir>/GameSettings/GZLE01.ini unless one is there
// (launch.EnsureCheatsIni). Best-effort: a failure is a warning.
func ensureCheatsIni(rep log.Reporter, userDir string) {
	launch.EnsureCheatsIni(rep, userDir, bundledCheatsIni)
}

// runMpLocal (the Go port of the retired scripts/mplay2.sh) spins up the relay
// server + 4 client goroutines (broadcast-pose × 2, puppet-sync × 2)
// inside this process so a single Ctrl+C tears the whole demo down and
// resets shadow_mode on both Dolphins.
//
// co.On() (--coop) makes it a co-op session too (mods/coop/session): the cards
// of both user dirs are backed up, the server runs the CoopHub over the
// world co picks (--coop=new founds it from Dolphin A's file), and each
// Dolphin gets a coop-sync goroutine with its own handle and co-op
// connection. It also turns the startup shift of Dolphin B's Link off
// (startupShift, mp_local_shift.go). Without it none of the co-op pieces
// run, and the startup shift moves B only when the two Links overlap.
func runMpLocal(nameA, nameB string, co coopsession.Flags) {
	var rep log.Reporter = log.Stdout{}
	ctx, cancel := engine.CLIMultiplayerContext(rep)
	defer cancel()

	// ListPIDs returns an error (not an empty slice) when no Dolphin
	// processes are running, so treat err as "0 found" for the
	// user-facing count.
	pids, err := emu.ListPIDs()
	count := len(pids)
	if err != nil {
		count = 0
	}
	if count < 2 {
		log.Logf(rep, log.Err,
			"need 2 Dolphin instances running, found %d (run `./wwdev.exe dolphin2` first)",
			count)
		os.Exit(1)
	}
	pidA, pidB := pids[0], pids[1]
	if a, b, ok := pinnedPIDs(os.Getenv("MP_LOCAL_PIDS"), pids); ok {
		pidA, pidB = a, b
	}
	// logs/latest (WW_DEV_LOGS, mp_local_session.go): this run's output,
	// session manifest and Dolphin logs. Only once two Dolphins are
	// there, so a mis-start leaves the last run in latest; one that
	// times out waiting for the games below has archived it already.
	var sess *mpLocalSession
	rep, sess = startMpLocalSession(rep, os.Args[1:])
	defer func() {
		cancel() // the Dolphin log tails stop with ctx
		sess.finish()
	}()
	log.Logf(rep, log.OK, "Found Dolphin A (pid %d) and B (pid %d).", pidA, pidB)

	// Wait until both Dolphins have a save loaded — player.Link.Ready returns
	// true when Link's actor pointer is in RAM range (not zero, not
	// during reload). 60 s timeout protects against the user never
	// loading a save; 1 s poll interval is responsive without
	// hammering the process snapshots.
	if err := waitForReady(ctx, rep, pidA, pidB, 60*time.Second); err != nil {
		rep.Log(log.Err, err.Error())
		os.Exit(1)
	}
	sess.describe(ctx, rep, pidA, pidB, nameA, nameB, co.On())

	// --coop writes the world into both games (bind, replica, warps), so
	// it starts only when both run this version's patch (strict contract
	// check, Phase E1); coopsession.RunSync re-checks while it plays.
	if co.On() {
		refused := false
		for i, pid := range []uint32{pidA, pidB} {
			if why := contractRefused(pid); why != "" {
				log.Logf(rep, log.Err, "Not starting co-op: Dolphin %c (pid %d)'s game is not this version's patch: %s. %s", 'A'+rune(i), pid, why, wwapi.RepatchAdvice)
				refused = true
			}
		}
		if refused {
			os.Exit(1)
		}
	}

	// Auto-arm the face-state sync hook on both Dolphins. C side
	// (face_hook_process_arm) picks this up next frame and installs
	// once mini_link_state == 1. Symmetrical disarm in teardown below.
	// Only into a game the strict contract check (Phase E1) allows:
	// another version's blob may not have the field there.
	for i, pid := range []uint32{pidA, pidB} {
		if why := contractRefused(pid); why != "" {
			log.Logf(rep, log.Err, "Not arming face sync on Dolphin %c (pid %d): its game is not this version's patch: %s. %s", 'A'+rune(i), pid, why, wwapi.RepatchAdvice)
			continue
		}
		d, err := emu.FindByPID(pid, "GZLE01")
		if err != nil {
			log.Logf(rep, log.Warn, "couldn't arm face-hook on Dolphin %c (pid %d): %v", 'A'+rune(i), pid, err)
			continue
		}
		avatar.SetFaceSync(d, true)
		d.Close()
	}
	rep.Log(log.OK, "Face-state sync armed on the Dolphins that run this version's patch.")

	// When both Dolphins boot from the same SAVE_STATE, both Links
	// spawn at literally the same world coords and visually overlap.
	// startupShift then uses the C-side warp handler (which sets
	// l_debug_keep_pos + current.pos + old.pos so the next frame's
	// execute() doesn't snap-revert) to nudge Dolphin B's Link diagonally
	// a bit from where it stands. Defaults of (+50, 0, +50) put the two
	// Links visibly distinct but still side-by-side — a few Link-heights
	// apart, both on the same patch of ground. It moves B only when the
	// two Links overlap in the same stage and room with both games idle,
	// and never under --coop (mp_local_shift.go). Override via
	// MP_LOCAL_SHIFT_X/Y/Z env, or set all three to 0 to disable.
	shift := [3]float32{
		feed.EnvFloat32("MP_LOCAL_SHIFT_X", 50),
		feed.EnvFloat32("MP_LOCAL_SHIFT_Y", 0),
		feed.EnvFloat32("MP_LOCAL_SHIFT_Z", 50),
	}
	if contractRefused(pidA) == "" && contractRefused(pidB) == "" {
		startupShift(rep, co.On(), pidA, pidB, shift, defaultShiftEnv())
	} else {
		rep.Log(log.Info, "Not moving Dolphin B's Link at start: a Dolphin's game is not this version's patch.")
	}

	// Server
	srv := network.NewServer(25565)
	srvRep := log.Prefix(rep, "[server]")
	srv.OnLog = func(msg string) { srvRep.Log(log.Info, msg) }
	var coop *coopsession.Session
	if co.On() {
		var err error
		if coop, err = coopsession.StartHost(ctx, rep, srv.OnLog, co, coopEnv, pidA); err != nil {
			if ctx.Err() != nil {
				return // Ctrl+C while founding waited for the game
			}
			rep.Log(log.Err, err.Error())
			os.Exit(1)
		}
		srv.Coop = coop.Hub()
	}
	if err := srv.Start(); err != nil {
		log.Logf(rep, log.Err, "server start: %v", err)
		coop.Stop()
		os.Exit(1)
	}
	rep.Log(log.OK, "Server listening on :25565")

	// Give the listener a tick before the first connect — mirrors the
	// 2 s sleep in the retired mplay2.sh that suppressed accept-races on Windows.
	time.Sleep(500 * time.Millisecond)

	addr := "localhost:25565"
	var wg sync.WaitGroup

	// spawn launches one client goroutine running run with the label's
	// prefixed reporter: feed.BroadcastPose, feed.PuppetSync or, with
	// --coop, coopsession.RunSync.
	spawn := func(label string, run func(r log.Reporter) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			r := log.Prefix(rep, "["+label+"]")
			if err := run(r); err != nil {
				log.Logf(r, log.Err, "%v", err)
			}
		}()
	}
	broadcast := func(name string, pid uint32) func(log.Reporter) error {
		return func(r log.Reporter) error { return feed.BroadcastPose(ctx, feedEnv, name, addr, pid, r, nil, nil) }
	}
	puppet := func(name string, pid uint32) func(log.Reporter) error {
		// With --coop, each Dolphin's warp to the partner takes the other's
		// position from this client; without, onClient stays nil.
		onClient := coop.WithPoseClient(name, nil)
		return func(r log.Reporter) error {
			return feed.PuppetSync(ctx, feedEnv, name, addr, name, pid, r, nil, onClient)
		}
	}

	// 0.3 s stagger between connects: four simultaneous accepts against
	// a freshly-listening server occasionally produced "expected welcome,
	// got error or wrong type" on Windows pre-staggering (TCP accept +
	// welcome-write race). Same delay as the retired scripts/mplay2.sh.
	spawn("bcast-A", broadcast(nameA, pidA))
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-A", puppet(nameA, pidA))
	time.Sleep(300 * time.Millisecond)
	spawn("bcast-B", broadcast(nameB, pidB))
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-B", puppet(nameB, pidB))
	// Dolphin B's enemy claims (enemy-mirror-general.json D8): enemy-B
	// fills them and coop-B's K1 refuses what they claim. Per game, never
	// shared: A is the host and mirrors nothing, so coop-A gets none.
	claimsA, claimsB := mpLocalClaims(enemysync.Enabled())
	if enemysync.Enabled() {
		// Unless WW_ENEMY_SYNC=0: Dolphin A's Moblins are the authority (the
		// sampler), Dolphin B mirrors them.
		envA, envB := enemyEnv, enemyEnv
		envA.Claims, envB.Claims = claimsA, claimsB
		time.Sleep(300 * time.Millisecond)
		spawn("enemy-A", func(r log.Reporter) error { return enemysync.HostPipeline(ctx, envA, nameA, addr, pidA, r, nil) })
		time.Sleep(300 * time.Millisecond)
		spawn("enemy-B", func(r log.Reporter) error {
			defer claimsB.Replace(nil) // a stopped client claims nothing
			return enemysync.ClientPipeline(ctx, envB, nameB, addr, pidB, r, nil)
		})
	}
	if coop != nil {
		for _, d := range []struct {
			label, name string
			pid         uint32
			claims      *enemysync.ClaimSet
		}{{"coop-A", nameA, pidA, claimsA}, {"coop-B", nameB, pidB, claimsB}} {
			time.Sleep(300 * time.Millisecond)
			spawn(d.label, func(r log.Reporter) error {
				return coopsession.RunSync(ctx, coopEnv, d.name, addr, d.pid, r, coop, mpLocalClaimFunc(d.claims))
			})
		}
	}

	rep.Log(log.OK, "Local multiplayer running. Walk in either Dolphin — the other should render Link #2 at your real coords.")
	rep.Log(log.Info, "Ctrl+C to stop.")

	<-ctx.Done()
	rep.Log(log.Info, "Tearing down...")
	wg.Wait()
	srv.Stop()
	coop.Stop()

	// Reset both Dolphins to baseline mirror so Link #2 doesn't stay
	// frozen at the last received pose forever.
	for i, pid := range []uint32{pidA, pidB} {
		if contractRefused(pid) != "" {
			continue // not this version's mod: nothing was written there
		}
		d, err := emu.FindByPID(pid, "GZLE01")
		if err != nil {
			log.Logf(rep, log.Warn, "couldn't reset Dolphin %c (pid %d): %v", 'A'+rune(i), pid, err)
			continue
		}
		avatar.Clear(d)
		d.Close()
	}
	rep.Log(log.OK, "Done.")
}

// mpLocalClaims are mp-local's enemy claim sets, one per Dolphin
// (enemy-mirror-general.json D8): none for A, the host, whose enemies
// are the authority; a set of its own for B, the mirror, which enemy-B
// fills and coop-B's K1 reads. Both nil with enemy sync off.
func mpLocalClaims(enemySync bool) (a, b *enemysync.ClaimSet) {
	if !enemySync {
		return nil, nil
	}
	return nil, enemysync.NewClaimSet()
}

// mpLocalClaimFunc is cs's Claimed for a coop-sync's K1, nil for a nil
// set.
func mpLocalClaimFunc(cs *enemysync.ClaimSet) combat.ClaimFunc {
	if cs == nil {
		return nil
	}
	return cs.Claimed
}

// waitForReady blocks until both Dolphins report player.Link.Ready() = true,
// the context is cancelled, or the timeout elapses. Both the FindByPID
// and the Ready call are retried each tick: FindByPID can fail if
// Dolphin's process is alive but its emulated RAM mapping hasn't
// finished bootstrapping yet (especially right after a fresh launch),
// and Ready is false until Link's actor pointer is in RAM range.
// Logs a "still waiting…" line every 5 s so it's clear mp-local is
// alive rather than wedged.
func waitForReady(ctx context.Context, rep log.Reporter, pidA, pidB uint32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	rep.Log(log.Info, "Waiting for both Dolphins to load to an in-game save state...")

	var dA, dB *emu.Dolphin
	defer func() {
		if dA != nil {
			dA.Close()
		}
		if dB != nil {
			dB.Close()
		}
	}()

	lastLogged := time.Now()
	for {
		if dA == nil {
			if d, err := emu.FindByPID(pidA, "GZLE01"); err == nil {
				dA = d
			}
		}
		if dB == nil {
			if d, err := emu.FindByPID(pidB, "GZLE01"); err == nil {
				dB = d
			}
		}
		aReady := dA != nil && player.NewLink(dA).Ready()
		bReady := dB != nil && player.NewLink(dB).Ready()
		if aReady && bReady {
			rep.Log(log.OK, "Both Dolphins in-game — proceeding.")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for Dolphins to load (A ready: %v, B ready: %v)",
				timeout, aReady, bReady)
		}
		if time.Since(lastLogged) >= 5*time.Second {
			log.Logf(rep, log.Info, "  still waiting (A: %s, B: %s)",
				readyLabel(aReady), readyLabel(bReady))
			lastLogged = time.Now()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cancelled before Dolphins were ready")
		case <-time.After(1 * time.Second):
		}
	}
}

func readyLabel(ready bool) string {
	if ready {
		return "ready"
	}
	return "still loading"
}

// contractRefused runs the strict contract check (Phase E1,
// wwapi.CheckContract) on Dolphin pid: "" when this build may write into
// its game, else why not.
func contractRefused(pid uint32) string {
	d, err := emu.FindByPID(pid, "GZLE01")
	if err != nil {
		return err.Error()
	}
	defer d.Close()
	c, err := wwapi.CheckContract(d, wwapi.ContractOptions{})
	if err != nil {
		return err.Error()
	}
	if c.WritesAllowed {
		return ""
	}
	return c.Why
}

// pinnedPIDs reads MP_LOCAL_PIDS ("<A>,<B>", two running Dolphin PIDs):
// which Dolphin is A (the host of every pipeline that has one) instead of
// the enumeration order. Anything else is ignored.
func pinnedPIDs(v string, running []uint32) (a, b uint32, ok bool) {
	parts := strings.Split(v, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var got [2]uint32
	for i, s := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
		if err != nil || !slices.Contains(running, uint32(n)) {
			return 0, 0, false
		}
		got[i] = uint32(n)
	}
	return got[0], got[1], got[0] != got[1]
}
