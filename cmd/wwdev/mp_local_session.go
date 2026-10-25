package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// envEchoDolphinLog makes mp-local print each Dolphin's new dolphin.log
// lines in its own output too, prefixed [dolphin-A] / [dolphin-B]. They
// go to logs/latest/dolphin-<A|B>.log either way.
const envEchoDolphinLog = "MP_LOCAL_DOLPHIN_LOG"

// mpLocalSession is one mp-local run's logs/latest (devlogs.go). A nil
// session (WW_DEV_LOGS=off, or latest could not be made) writes no
// files; its methods still run the Dolphin log echo.
type mpLocalSession struct {
	root, dir string
	file      *os.File
	manifest  sessionManifest
	tails     sync.WaitGroup
}

// startMpLocalSession archives the previous run, makes logs/latest and
// returns rep teed into latest/mp-local.log. On a failure it warns and
// returns rep alone with a nil session.
func startMpLocalSession(rep log.Reporter, args []string) (log.Reporter, *mpLocalSession) {
	root := devLogsRoot(os.Getenv)
	if root == "" {
		return rep, nil
	}
	dir, archived, err := rotateLatest(root)
	if err != nil {
		log.Logf(rep, log.Warn, "run logs off: %v", err)
		return rep, nil
	}
	f, err := os.Create(filepath.Join(dir, "mp-local.log"))
	if err != nil {
		log.Logf(rep, log.Warn, "run logs off: %v", err)
		return rep, nil
	}
	bi, _ := debug.ReadBuildInfo()
	s := &mpLocalSession{root: root, dir: dir, file: f, manifest: sessionManifest{
		Started: time.Now(), Args: args, Build: readBuildInfo(bi),
		Env: sessionEnv(os.Environ()), Archived: archived,
	}}
	tee := log.Multi(rep, &fileReporter{w: f, now: time.Now})
	log.Logf(tee, log.Info, "Run logs in %s (WW_DEV_LOGS)%s", dir, archivedNote(archived))
	_ = s.manifest.write(dir)
	return tee, s
}

func archivedNote(archived string) string {
	if archived == "" {
		return ""
	}
	return "; the run before is in " + archived
}

// dolphinLogSource is where one Dolphin's log is read from.
type dolphinLogSource struct {
	label   string // A, B, or 1, 2 when the PID's user dir is unknown
	path    string
	offset  int64
	enabled bool
}

// resolveDolphins names the run's two Dolphins from dolphin2's record
// (user dir, save state, log) and, for a PID it has no record of (a
// Dolphin started by hand or by a rig script), the environment's
// SAVE_STATE / SAVE_STATE_2. The log sources follow: the record's log
// from its launch offset, or, with neither PID recorded, USER_DIR_1 and
// USER_DIR_2's logs from their current end under the labels 1 and 2
// (which of them is A is then unknown).
func resolveDolphins(recs []launchRecord, pidA, pidB uint32, getenv func(string) string, paths launch.Paths) ([]sessionDolphin, []dolphinLogSource) {
	byPID := map[uint32]launchRecord{}
	for _, r := range recs {
		byPID[uint32(r.PID)] = r
	}
	var ds []sessionDolphin
	var srcs []dolphinLogSource
	for i, pid := range []uint32{pidA, pidB} {
		label := string(rune('A' + i))
		d := sessionDolphin{Label: label, PID: pid}
		if r, ok := byPID[pid]; ok {
			d.FromRecord, d.UserDir, d.SaveState, d.DevLog = true, r.UserDir, r.SaveState, r.DevLog
			if r.LogPath != "" {
				srcs = append(srcs, dolphinLogSource{label: label, path: r.LogPath, offset: r.LogOffset, enabled: r.DevLog})
			}
		} else {
			d.SaveState = getenv("SAVE_STATE")
			if i == 1 && getenv("SAVE_STATE_2") != "" {
				d.SaveState = getenv("SAVE_STATE_2")
			}
		}
		ds = append(ds, d)
	}
	if len(srcs) == 0 && !ds[0].FromRecord && !ds[1].FromRecord {
		for i, ud := range []string{paths.UserDir1, paths.UserDir2} {
			if ud == "" {
				continue
			}
			p := launch.LogFile(ud)
			if _, err := os.Stat(p); err == nil {
				srcs = append(srcs, dolphinLogSource{label: fmt.Sprint(i + 1), path: p, offset: fileSize(p), enabled: true})
			}
		}
	}
	return ds, srcs
}

// describe fills the manifest's Dolphins (with each game's contract
// verdict and save-state hash), writes it, and starts the Dolphin log
// tails, which stop with ctx (wait for them with finish).
func (s *mpLocalSession) describe(ctx context.Context, rep log.Reporter, pidA, pidB uint32, nameA, nameB string, coop bool) {
	var recs []launchRecord
	if s != nil {
		recs = readLaunchRecords(s.root)
	}
	ds, srcs := resolveDolphins(recs, pidA, pidB, os.Getenv, launch.FromEnv())
	echo := launch.DevLogEnabled(func(string) string { return os.Getenv(envEchoDolphinLog) })
	for _, src := range srcs {
		if !src.enabled {
			continue
		}
		s.tail(ctx, rep, src, echo)
		for i := range ds {
			if ds[i].Label == src.label && s != nil {
				ds[i].Log = filepath.Join(s.dir, "dolphin-"+src.label+".log")
			}
		}
	}
	switch {
	case len(srcs) > 0 && srcs[0].label == "1":
		log.Logf(rep, log.Info, "Dolphin logs: no dolphin2 record for pids %d and %d, following USER_DIR_1 and USER_DIR_2's logs as dolphin-1 and dolphin-2.", pidA, pidB)
		if s != nil {
			for _, src := range srcs {
				s.manifest.UnmatchedLogs = append(s.manifest.UnmatchedLogs, filepath.Join(s.dir, "dolphin-"+src.label+".log"))
			}
		}
	case len(srcs) == 0:
		rep.Log(log.Info, "Dolphin logs: none followed (launch with WW_DOLPHIN_DEVLOG=1 ./wwdev.exe dolphin2 to log to file).")
	}
	if s == nil {
		return
	}
	for i := range ds {
		d := &ds[i]
		if d.SaveState != "" {
			d.SaveStateHash = sha256File(d.SaveState)
		}
		dol, err := emu.FindByPID(d.PID, "GZLE01")
		if err != nil {
			d.Why = err.Error()
			continue
		}
		c, err := wwapi.CheckContract(dol, wwapi.ContractOptions{})
		dol.Close()
		if err != nil {
			d.Why = err.Error()
			continue
		}
		d.WritesAllowed, d.Why = c.WritesAllowed, c.Why
		d.GameBlobCRC, d.WantBlobCRC = fmt.Sprintf("%08X", c.BlobHash), fmt.Sprintf("%08X", c.WantHash)
		d.RELLinked = c.REL.Linked
		if c.REL.Linked {
			d.RELBuildID = fmt.Sprintf("%08X", c.REL.BuildID)
		}
	}
	s.manifest.Names = [2]string{nameA, nameB}
	s.manifest.Coop = coop
	s.manifest.Dolphins = ds
	if err := s.manifest.write(s.dir); err != nil {
		log.Logf(rep, log.Warn, "session manifest: %v", err)
	}
}

// tail follows src into latest/dolphin-<label>.log and, with echo, into
// rep under [dolphin-<label>].
func (s *mpLocalSession) tail(ctx context.Context, rep log.Reporter, src dolphinLogSource, echo bool) {
	var out *os.File
	if s != nil {
		f, err := os.Create(filepath.Join(s.dir, "dolphin-"+src.label+".log"))
		if err != nil {
			log.Logf(rep, log.Warn, "dolphin-%s log copy: %v", src.label, err)
		} else {
			out = f
		}
	}
	if out == nil && !echo {
		return
	}
	pre := log.Prefix(rep, "[dolphin-"+src.label+"]")
	run := func() {
		tailFile(ctx, src.path, src.offset, tailPoll, func(line string) {
			if out != nil {
				fmt.Fprintln(out, line)
			}
			if echo && strings.TrimSpace(line) != "" {
				pre.Log(log.Info, line)
			}
		})
		if out != nil {
			out.Close()
		}
	}
	if s == nil {
		go run()
		return
	}
	s.tails.Add(1)
	go func() {
		defer s.tails.Done()
		run()
	}()
}

// finish waits for the tails (ctx must be done), stamps the end and
// closes mp-local.log.
func (s *mpLocalSession) finish() {
	if s == nil {
		return
	}
	s.tails.Wait()
	now := time.Now()
	s.manifest.Ended = &now
	_ = s.manifest.write(s.dir)
	s.file.Close()
}
