package main

// The dev tools' run logs (idea 19 of the ww-online review, after
// datstarkey/ww-online's scripts/dev-test.ps1 archive and session.txt;
// clean-room): dolphin2 records which Dolphin it launched on which user
// dir (dolphins.json), and mp-local writes logs/latest/ for each run: its
// own output (mp-local.log), a session manifest (session.json) and, when
// Dolphin logs to file (WW_DOLPHIN_DEVLOG=1), a copy of each Dolphin's
// dolphin.log from the run's start (dolphin-A.log, dolphin-B.log). The
// run before is moved to logs/sessions/<its start>/.
//
// WW_DEV_LOGS picks the root (default "logs", relative to the working
// directory: the repo root, as for saves/); "off" writes nothing.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject/blobstamp"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// envDevLogs is the dev logs root; "off" turns them off.
const envDevLogs = "WW_DEV_LOGS"

// devLogsRoot is the root getenv's WW_DEV_LOGS names: "logs" when unset,
// "" when off.
func devLogsRoot(getenv func(string) string) string {
	v := strings.TrimSpace(getenv(envDevLogs))
	switch strings.ToLower(v) {
	case "":
		return "logs"
	case "off", "0", "none", "false":
		return ""
	}
	return v
}

// launchRecord is one Dolphin dolphin2 launched.
type launchRecord struct {
	Label     string    `json:"label"`
	PID       int       `json:"pid"`
	UserDir   string    `json:"user_dir"`
	SaveState string    `json:"save_state,omitempty"`
	DevLog    bool      `json:"dev_log"`
	LogPath   string    `json:"log_path,omitempty"`
	LogOffset int64     `json:"log_offset"` // dolphin.log's size at launch: this Dolphin's lines start there
	Launched  time.Time `json:"launched"`
}

const launchRecordFile = "dolphins.json"

// readLaunchRecords reads root's dolphins.json; a missing or broken file
// is an empty list.
func readLaunchRecords(root string) []launchRecord {
	b, err := os.ReadFile(filepath.Join(root, launchRecordFile))
	if err != nil {
		return nil
	}
	var recs []launchRecord
	if json.Unmarshal(b, &recs) != nil {
		return nil
	}
	return recs
}

// saveLaunchRecords writes recs to root's dolphins.json, after the ones
// already there whose PID is still in running (a Dolphin that quit is
// dropped; a re-used PID is replaced by its new record).
func saveLaunchRecords(root string, running []uint32, recs []launchRecord) error {
	var keep []launchRecord
	for _, r := range readLaunchRecords(root) {
		if !slices.Contains(running, uint32(r.PID)) {
			continue
		}
		if slices.ContainsFunc(recs, func(n launchRecord) bool { return n.PID == r.PID }) {
			continue
		}
		keep = append(keep, r)
	}
	keep = append(keep, recs...)
	b, err := json.MarshalIndent(keep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, launchRecordFile), append(b, '\n'), 0o644)
}

// fileSize is path's size, 0 when it does not exist.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// rotateLatest moves root/latest, the run before, to
// root/sessions/<its start> (its session.json's started time, else the
// directory's modification time; "-2", "-3", ... when taken) and makes a
// new, empty root/latest. It returns the new directory and where the old
// one went ("" when there was none).
func rotateLatest(root string) (latest, archived string, err error) {
	latest = filepath.Join(root, "latest")
	if fi, err := os.Stat(latest); err == nil && fi.IsDir() {
		stamp := fi.ModTime()
		if b, err := os.ReadFile(filepath.Join(latest, "session.json")); err == nil {
			var m struct {
				Started time.Time `json:"started"`
			}
			if json.Unmarshal(b, &m) == nil && !m.Started.IsZero() {
				stamp = m.Started
			}
		}
		sessions := filepath.Join(root, "sessions")
		if err := os.MkdirAll(sessions, 0o755); err != nil {
			return "", "", err
		}
		base := filepath.Join(sessions, stamp.Format("20060102-150405"))
		archived = base
		for n := 2; ; n++ {
			if _, err := os.Stat(archived); errors.Is(err, os.ErrNotExist) {
				break
			}
			archived = fmt.Sprintf("%s-%d", base, n)
		}
		if err := os.Rename(latest, archived); err != nil {
			return "", "", fmt.Errorf("archive %s: %w", latest, err)
		}
	}
	if err := os.MkdirAll(latest, 0o755); err != nil {
		return "", "", err
	}
	return latest, archived, nil
}

// buildInfo is what the binary says about its own source.
type buildInfo struct {
	Module    string `json:"module_version"`
	Revision  string `json:"vcs_revision"`
	Modified  string `json:"vcs_modified"` // "true": built from a dirty tree
	VCSTime   string `json:"vcs_time,omitempty"`
	GoVersion string `json:"go_version"`
	BlobHash  string `json:"blob_hash"` // blob.go's payload (blobstamp.BlobHash, blob.stamp's "blob" line)
	RelHash   string `json:"rel_hash"`  // blob.go's REL (blob.stamp's "rel" line)
}

// readBuildInfo fills buildInfo from bi (debug.ReadBuildInfo's; nil or
// without VCS settings, as in a test binary or a -buildvcs=false build,
// leaves "unknown").
func readBuildInfo(bi *debug.BuildInfo) buildInfo {
	b := buildInfo{Module: "unknown", Revision: "unknown", Modified: "unknown", GoVersion: runtime.Version(),
		BlobHash: blobstamp.BlobHash(), RelHash: blobstamp.RelHash()}
	if bi == nil {
		return b
	}
	if bi.Main.Version != "" {
		b.Module = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.modified":
			b.Modified = s.Value
		case "vcs.time":
			b.VCSTime = s.Value
		}
	}
	return b
}

// sessionDolphin is one Dolphin of the run.
type sessionDolphin struct {
	Label         string `json:"label"` // A (the host of every pipeline with one) or B
	PID           uint32 `json:"pid"`
	UserDir       string `json:"user_dir,omitempty"`
	FromRecord    bool   `json:"from_dolphin2_record"` // UserDir and SaveState come from dolphins.json
	SaveState     string `json:"save_state,omitempty"`
	SaveStateHash string `json:"save_state_sha256,omitempty"`
	DevLog        bool   `json:"dev_log"`
	Log           string `json:"log,omitempty"` // the copy in latest/
	// The strict contract check's verdict on this Dolphin's game.
	WritesAllowed bool   `json:"writes_allowed"`
	Why           string `json:"why,omitempty"`
	GameBlobCRC   string `json:"game_blob_crc,omitempty"`
	WantBlobCRC   string `json:"want_blob_crc,omitempty"`
	RELLinked     bool   `json:"rel_linked"`
	RELBuildID    string `json:"rel_build_id,omitempty"`
}

// sessionManifest is latest/session.json.
type sessionManifest struct {
	Started  time.Time         `json:"started"`
	Ended    *time.Time        `json:"ended,omitempty"`
	Args     []string          `json:"args"`
	Build    buildInfo         `json:"build"`
	Names    [2]string         `json:"names"`
	Coop     bool              `json:"coop"`
	Dolphins []sessionDolphin  `json:"dolphins"`
	Env      map[string]string `json:"env"` // the dev and session knobs set for this run
	Archived string            `json:"previous_run,omitempty"`
	// UnmatchedLogs are the dolphin-1/2.log copies of USER_DIR_1 and
	// USER_DIR_2's logs when dolphin2 recorded neither PID (which is A is
	// unknown).
	UnmatchedLogs []string `json:"unmatched_logs,omitempty"`
}

// sessionEnv picks the knobs worth keeping from environ: WW_*,
// MP_LOCAL_*, SAVE_STATE*, USER_DIR_*, DOLPHIN_EXE and ISO_PATH.
func sessionEnv(environ []string) map[string]string {
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		for _, p := range []string{"WW_", "MP_LOCAL_", "SAVE_STATE", "USER_DIR_", "DOLPHIN_EXE", "ISO_PATH"} {
			if strings.HasPrefix(strings.ToUpper(k), p) {
				out[k] = v
				break
			}
		}
	}
	return out
}

// write saves m as dir/session.json.
func (m *sessionManifest) write(dir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "session.json"), append(b, '\n'), 0o644)
}

// sha256File is path's SHA-256, "" when it cannot be read.
func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fileReporter writes each line to w with a time and the level's prefix
// (the Stdout format), safe for the pipelines' goroutines.
type fileReporter struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func (f *fileReporter) Log(level log.Level, msg string) {
	p := ""
	switch level {
	case log.Net:
		p = "[net] "
	case log.Err:
		p = "ERROR: "
	case log.Warn:
		p = "WARN: "
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fmt.Fprintf(f.w, "%s %s%s\n", f.now().Format("15:04:05.000"), p, msg)
}

// tailPoll is how often tailFile looks for new bytes.
const tailPoll = 250 * time.Millisecond

// tailFile follows path from offset until ctx ends, handing each complete
// line to line. A file that shrinks (Dolphin restarted on a fresh log, or
// it was cleared) is read again from 0; a missing file is waited for.
// What is left without a newline at the end is handed over then.
func tailFile(ctx context.Context, path string, offset int64, poll time.Duration, line func(string)) {
	var partial []byte
	pos := offset
	flush := func() {
		if len(partial) > 0 {
			line(strings.TrimRight(string(partial), "\r"))
			partial = nil
		}
	}
	readNew := func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return
		}
		if fi.Size() < pos {
			flush()
			pos = 0
		}
		if fi.Size() == pos {
			return
		}
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return
		}
		r := bufio.NewReader(io.LimitReader(f, fi.Size()-pos))
		for {
			b, err := r.ReadBytes('\n')
			pos += int64(len(b))
			if len(b) > 0 && b[len(b)-1] == '\n' {
				partial = append(partial, b[:len(b)-1]...)
				line(strings.TrimRight(string(partial), "\r"))
				partial = nil
			} else {
				partial = append(partial, b...)
			}
			if err != nil {
				return
			}
		}
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		readNew()
		select {
		case <-ctx.Done():
			readNew()
			flush()
			return
		case <-t.C:
		}
	}
}
