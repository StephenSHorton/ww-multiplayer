package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// runDolphin2 is the Go port of scripts/dolphin2.sh — bootstraps the
// "Dolphin Emulator 2" user dir and launches the missing Dolphin
// instance(s) so two are running side-by-side. mp-local can then attach
// to both without any env-var juggling.
//
// Reads DOLPHIN_EXE, ISO_PATH, USER_DIR_1, USER_DIR_2 from env with
// the same defaults the bash script used (DOLPHIN_EXE points into the
// user's local Dolphin install; APPDATA-relative dir paths).
func runDolphin2(reset bool) {
	rep := report.Stdout{}

	dolphinExe := envOrDefault("DOLPHIN_EXE", `C:\Users\4step\Desktop\Dolphin-x64\Dolphin.exe`)
	isoPath := envOrDefault("ISO_PATH", `C:\Users\4step\Desktop\Dolphin-x64\Roms\WW_Multiplayer_Patched.iso`)
	appData := os.Getenv("APPDATA")
	userDir1 := envOrDefault("USER_DIR_1", filepath.Join(appData, "Dolphin Emulator"))
	userDir2 := envOrDefault("USER_DIR_2", filepath.Join(appData, "Dolphin Emulator 2"))

	if reset {
		report.Logf(rep, report.Info, "Removing %s ...", userDir2)
		if err := os.RemoveAll(userDir2); err != nil {
			rep.Log(report.Err, err.Error())
			os.Exit(1)
		}
	}

	if _, err := os.Stat(dolphinExe); err != nil {
		report.Logf(rep, report.Err, "Dolphin not found at %s (set DOLPHIN_EXE to override)", dolphinExe)
		os.Exit(1)
	}
	if _, err := os.Stat(isoPath); err != nil {
		report.Logf(rep, report.Err, "Patched ISO not found at %s", isoPath)
		report.Logf(rep, report.Err, "Run `./ww-multiplayer.exe patch <vanilla.iso>` or set ISO_PATH.")
		os.Exit(1)
	}
	if _, err := os.Stat(userDir1); err != nil {
		report.Logf(rep, report.Err, "Primary Dolphin user dir not found at %s", userDir1)
		report.Logf(rep, report.Err, "Run Dolphin once normally to bootstrap it, then re-run dolphin2.")
		os.Exit(1)
	}

	if _, err := os.Stat(userDir2); err != nil {
		report.Logf(rep, report.Info, "Bootstrapping %s from %s (skipping Cache/) ...", userDir2, userDir1)
		// Skip Cache/ — Dolphin 1 holds Windows file locks on
		// Cache/Shaders/*.cache and GZLE01.uidcache while it's
		// running, which would fail the copy. Cache regenerates on
		// first boot of Dolphin 2 (~10–30s shader recompile) so we
		// pay nothing after that.
		if err := copyDirSkip(userDir1, userDir2, []string{"Cache"}); err != nil {
			report.Logf(rep, report.Err, "bootstrap: %v", err)
			os.Exit(1)
		}
		rep.Log(report.OK, "Bootstrap complete.")
	}

	pids, err := dolphin.ListPIDs()
	if err != nil {
		report.Logf(rep, report.Warn, "couldn't enumerate Dolphin PIDs: %v (will launch both)", err)
	}

	type launchPlan struct {
		label   string
		userDir string
	}
	var plan []launchPlan
	switch len(pids) {
	case 0:
		plan = []launchPlan{{"Dolphin 1", userDir1}, {"Dolphin 2", userDir2}}
	case 1:
		plan = []launchPlan{{"Dolphin 2", userDir2}}
	default:
		rep.Log(report.Info, "Two or more Dolphins already running — nothing to do.")
		return
	}

	for _, p := range plan {
		pid, err := launchDolphinDetached(dolphinExe, p.userDir, isoPath)
		if err != nil {
			report.Logf(rep, report.Err, "launch %s: %v", p.label, err)
			os.Exit(1)
		}
		report.Logf(rep, report.OK, "Launched %s (pid %d, user dir %s)", p.label, pid, p.userDir)
	}
	rep.Log(report.Info, "Wait for both games to load to a save state, then run `./ww-multiplayer.exe mp-local`.")
}

// runMpLocal is the Go port of scripts/mplay2.sh — spins up the relay
// server + 4 client goroutines (broadcast-pose × 2, puppet-sync × 2)
// inside this process so a single Ctrl+C tears the whole demo down and
// resets shadow_mode on both Dolphins.
func runMpLocal(nameA, nameB string) {
	rep := report.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
	defer cancel()

	// ListPIDs returns an error (not an empty slice) when no Dolphin
	// processes are running, so treat err as "0 found" for the
	// user-facing count.
	pids, err := dolphin.ListPIDs()
	count := len(pids)
	if err != nil {
		count = 0
	}
	if count < 2 {
		report.Logf(rep, report.Err,
			"need 2 Dolphin instances running, found %d (run `./ww-multiplayer.exe dolphin2` first)",
			count)
		os.Exit(1)
	}
	pidA, pidB := pids[0], pids[1]
	report.Logf(rep, report.OK, "Found Dolphin A (pid %d) and B (pid %d).", pidA, pidB)

	// Server
	srv := network.NewServer(25565)
	srvRep := prefixReporter{inner: rep, prefix: "[server]"}
	srv.OnLog = func(msg string) { srvRep.Log(report.Info, msg) }
	if err := srv.Start(); err != nil {
		report.Logf(rep, report.Err, "server start: %v", err)
		os.Exit(1)
	}
	rep.Log(report.OK, "Server listening on :25565")

	// Give the listener a tick before the first connect — mirrors the
	// 2 s sleep in mplay2.sh that suppressed accept-races on Windows.
	time.Sleep(500 * time.Millisecond)

	addr := "localhost:25565"
	var wg sync.WaitGroup

	// spawn launches one client goroutine. broadcast=true means
	// runBroadcastPoseCtx, false means runPuppetSyncCtx.
	spawn := func(label, name string, pid uint32, broadcast bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			r := prefixReporter{inner: rep, prefix: "[" + label + "]"}
			if broadcast {
				if err := runBroadcastPoseCtx(ctx, name, addr, pid, r); err != nil {
					report.Logf(r, report.Err, "%v", err)
				}
			} else {
				if err := runPuppetSyncCtx(ctx, name, addr, name, pid, r); err != nil {
					report.Logf(r, report.Err, "%v", err)
				}
			}
		}()
	}

	// 0.3 s stagger between connects: four simultaneous accepts against
	// a freshly-listening server occasionally produced "expected welcome,
	// got error or wrong type" on Windows pre-staggering (TCP accept +
	// welcome-write race). Same delay as scripts/mplay2.sh.
	spawn("bcast-A", nameA, pidA, true)
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-A", nameA, pidA, false)
	time.Sleep(300 * time.Millisecond)
	spawn("bcast-B", nameB, pidB, true)
	time.Sleep(300 * time.Millisecond)
	spawn("puppet-B", nameB, pidB, false)

	rep.Log(report.OK, "Local multiplayer running. Walk in either Dolphin — the other should render Link #2 at your real coords.")
	rep.Log(report.Info, "Ctrl+C to stop.")

	<-ctx.Done()
	rep.Log(report.Info, "Tearing down...")
	wg.Wait()
	srv.Stop()

	// Reset both Dolphins to baseline mirror so Link #2 doesn't stay
	// frozen at the last received pose forever.
	for i, pid := range []uint32{pidA, pidB} {
		d, err := dolphin.FindByPID(pid, "GZLE01")
		if err != nil {
			report.Logf(rep, report.Warn, "couldn't reset Dolphin %c (pid %d): %v", 'A'+rune(i), pid, err)
			continue
		}
		d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{0})
		for j := 0; j < maxRemoteLinks; j++ {
			d.WriteAbsolute(mailboxBase+mailboxPoseSeq(j), []byte{0})
		}
		d.Close()
	}
	rep.Log(report.OK, "Done.")
}

// prefixReporter wraps another Reporter and prepends a static label to
// every Log call. Used by mp-local to tag each goroutine's output
// (`[server]`, `[bcast-A]`, etc.) so the merged stdout stream is
// readable.
type prefixReporter struct {
	inner  report.Reporter
	prefix string
}

func (p prefixReporter) Log(level report.Level, msg string) {
	p.inner.Log(level, p.prefix+" "+msg)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// copyDirSkip recursively copies src into dst, skipping any top-level
// child whose name is in skipNames. Used by dolphin2 to bootstrap
// USER_DIR_2 from USER_DIR_1 without copying the locked Cache/.
func copyDirSkip(src, dst string, skipNames []string) error {
	skip := map[string]bool{}
	for _, n := range skipNames {
		skip[n] = true
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		// First path component — used to gate the skip list.
		first := rel
		if i := strings.IndexAny(rel, `\/`); i >= 0 {
			first = rel[:i]
		}
		if skip[first] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}

// launchDolphinDetached starts Dolphin.exe with the given user dir and
// auto-boot ISO as a backgrounded process whose lifetime is independent
// of ours. ww-multiplayer.exe returns immediately; Dolphin keeps running
// after we exit. stdout/stderr go to the null device so Dolphin's spam
// doesn't clutter our terminal.
func launchDolphinDetached(exe, userDir, iso string) (int, error) {
	cmd := exec.Command(exe, "-u", userDir, "-e", iso)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Release disowns the child so we don't block on Wait() and the
	// process survives our exit. Without this Go would keep an OS
	// handle open until either Wait() or process termination.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("release: %w", err)
	}
	return pid, nil
}
