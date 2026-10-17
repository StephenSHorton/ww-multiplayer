package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// bundledCheatsIni is the curated cheats/GZLE01.ini shipped in the repo,
// baked into the binary so the released ww-multiplayer.exe can drop it
// into Dolphin user dirs without needing the source tree alongside it.
//
//go:embed cheats/GZLE01.ini
var bundledCheatsIni []byte

// dolphin2Defaults returns the OS-appropriate fallback paths for the
// dolphin2 bootstrap when DOLPHIN_EXE / ISO_PATH / USER_DIR_{1,2} are
// unset (launch.DefaultPaths).
func dolphin2Defaults() (exe, iso, userDir1, userDir2 string) {
	p := launch.DefaultPaths()
	return p.Exe, p.ISO, p.UserDir1, p.UserDir2
}

// runDolphin2 is the Go port of scripts/dolphin2.sh — bootstraps the
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
	// pair via `./ww-multiplayer.exe auto-recapture-pair`.
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

	for _, p := range plan {
		pid, err := launch.Launch(dolphinExe, p.userDir, isoPath, p.saveState)
		if err != nil {
			log.Logf(rep, log.Err, "launch %s: %v", p.label, err)
			os.Exit(1)
		}
		log.Logf(rep, log.OK, "Launched %s (pid %d, user dir %s)", p.label, pid, p.userDir)
	}
	if saveState != "" {
		if saveState2 != "" && saveState2 != saveState {
			log.Logf(rep, log.Info, "Dolphin 1 booting from %s; Dolphin 2 from %s. Distinct btp phases — face-sync should bridge them once mp-local arms.", saveState, saveState2)
		} else {
			log.Logf(rep, log.Info, "Both Dolphins booting with save state %s. mp-local's wait-loop will detect when they're in-game.", saveState)
		}
	} else {
		rep.Log(log.Info, "Wait for both games to load to a save state, then run `./ww-multiplayer.exe mp-local`. (Set SAVE_STATE=<path> to skip the menus next time.)")
	}
}

// ensureCheatsIni drops the bundled cheats/GZLE01.ini into
// <userDir>/GameSettings/GZLE01.ini unless one is there
// (launch.EnsureCheatsIni). Best-effort: a failure is a warning.
func ensureCheatsIni(rep log.Reporter, userDir string) {
	launch.EnsureCheatsIni(rep, userDir, bundledCheatsIni)
}

// runMpLocal is the Go port of scripts/mplay2.sh — spins up the relay
// server + 4 client goroutines (broadcast-pose × 2, puppet-sync × 2)
// inside this process so a single Ctrl+C tears the whole demo down and
// resets shadow_mode on both Dolphins.
//
// co.on (--coop) makes it a co-op session too (coop_session.go): the cards
// of both user dirs are backed up, the server runs the CoopHub over the
// world co picks (--coop=new founds it from Dolphin A's file), and each
// Dolphin gets a coop-sync goroutine with its own handle and co-op
// connection. It also turns the startup shift of Dolphin B's Link off
// (startupShift, mp_local_shift.go). Without it none of the co-op pieces
// run, and the startup shift moves B only when the two Links overlap.
func runMpLocal(nameA, nameB string, co coopFlags) {
	rep := log.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
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
			"need 2 Dolphin instances running, found %d (run `./ww-multiplayer.exe dolphin2` first)",
			count)
		os.Exit(1)
	}
	pidA, pidB := pids[0], pids[1]
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

	// Auto-arm the face-state sync hook on both Dolphins. C side
	// (face_hook_process_arm) picks this up next frame and installs
	// once mini_link_state == 1. Symmetrical disarm in teardown below.
	for i, pid := range []uint32{pidA, pidB} {
		d, err := emu.FindByPID(pid, "GZLE01")
		if err != nil {
			log.Logf(rep, log.Warn, "couldn't arm face-hook on Dolphin %c (pid %d): %v", 'A'+rune(i), pid, err)
			continue
		}
		if err := d.Write(mailbox.Mailbox+mailbox.OffFaceHookEnable, []byte{1}); err != nil {
			log.Logf(rep, log.Warn, "couldn't arm face-hook on Dolphin %c: %v", 'A'+rune(i), err)
		}
		d.Close()
	}
	rep.Log(log.OK, "Face-state sync armed on both Dolphins.")

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
		envFloat32("MP_LOCAL_SHIFT_X", 50),
		envFloat32("MP_LOCAL_SHIFT_Y", 0),
		envFloat32("MP_LOCAL_SHIFT_Z", 50),
	}
	startupShift(rep, co.on, pidA, pidB, shift, defaultShiftEnv())

	// Server
	srv := network.NewServer(25565)
	srvRep := log.Prefix(rep, "[server]")
	srv.OnLog = func(msg string) { srvRep.Log(log.Info, msg) }
	var coop *coopSession
	if co.on {
		var err error
		if coop, err = startCoopHost(ctx, rep, srv.OnLog, co, defaultCoopEnv(pidA)); err != nil {
			if ctx.Err() != nil {
				return // Ctrl+C while founding waited for the game
			}
			rep.Log(log.Err, err.Error())
			os.Exit(1)
		}
		srv.Coop = coop.hub
	}
	if err := srv.Start(); err != nil {
		log.Logf(rep, log.Err, "server start: %v", err)
		coop.stop()
		os.Exit(1)
	}
	rep.Log(log.OK, "Server listening on :25565")

	// Give the listener a tick before the first connect — mirrors the
	// 2 s sleep in mplay2.sh that suppressed accept-races on Windows.
	time.Sleep(500 * time.Millisecond)

	addr := "localhost:25565"
	var wg sync.WaitGroup

	// spawn launches one client goroutine running run with the label's
	// prefixed reporter: runBroadcastPoseCtx, runPuppetSyncCtx or, with
	// --coop, runCoopSyncCtx.
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
		return func(r log.Reporter) error { return runBroadcastPoseCtx(ctx, name, addr, pid, r, nil, nil) }
	}
	puppet := func(name string, pid uint32) func(log.Reporter) error {
		// With --coop, each Dolphin's warp to the partner takes the other's
		// position from this client; without, onClient stays nil.
		onClient := coop.withPoseClient(name, nil)
		return func(r log.Reporter) error { return runPuppetSyncCtx(ctx, name, addr, name, pid, r, nil, onClient) }
	}

	// 0.3 s stagger between connects: four simultaneous accepts against
	// a freshly-listening server occasionally produced "expected welcome,
	// got error or wrong type" on Windows pre-staggering (TCP accept +
	// welcome-write race). Same delay as scripts/mplay2.sh.
	spawn("bcast-A", broadcast(nameA, pidA))
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-A", puppet(nameA, pidA))
	time.Sleep(300 * time.Millisecond)
	spawn("bcast-B", broadcast(nameB, pidB))
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-B", puppet(nameB, pidB))
	if coop != nil {
		for _, d := range []struct {
			label, name string
			pid         uint32
		}{{"coop-A", nameA, pidA}, {"coop-B", nameB, pidB}} {
			time.Sleep(300 * time.Millisecond)
			spawn(d.label, func(r log.Reporter) error { return runCoopSyncCtx(ctx, d.name, addr, d.pid, r, coop) })
		}
	}

	rep.Log(log.OK, "Local multiplayer running. Walk in either Dolphin — the other should render Link #2 at your real coords.")
	rep.Log(log.Info, "Ctrl+C to stop.")

	<-ctx.Done()
	rep.Log(log.Info, "Tearing down...")
	wg.Wait()
	srv.Stop()
	coop.stop()

	// Reset both Dolphins to baseline mirror so Link #2 doesn't stay
	// frozen at the last received pose forever.
	for i, pid := range []uint32{pidA, pidB} {
		d, err := emu.FindByPID(pid, "GZLE01")
		if err != nil {
			log.Logf(rep, log.Warn, "couldn't reset Dolphin %c (pid %d): %v", 'A'+rune(i), pid, err)
			continue
		}
		d.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{0})
		d.Write(mailbox.Mailbox+mailbox.OffFaceHookEnable, []byte{0})
		for j := 0; j < maxRemoteLinks; j++ {
			d.Write(mailbox.Mailbox+mailboxPoseSeq(j), []byte{0})
			d.Write(mailbox.Mailbox+mailboxFaceSeq(j), []byte{0})
		}
		d.Close()
	}
	rep.Log(log.OK, "Done.")
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
