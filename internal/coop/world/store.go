package world

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A world's directory, <root>/<worldID>/ under os.UserConfigDir()/
// ww-multiplayer/worlds (Dir), holds:
//
//	world.wwco        the world (file.go), replaced atomically on every flush
//	world.wwco.1..5   backups, newest first: one at every session start and
//	                  one an hour while the session runs
//	world.wwco.bad    the last main file Open found damaged, kept for a look
//	journal.log       one line per change (journal.go); journal.log.1 is the
//	                  previous one, rotated at a session start past 1 MiB
//
// A write goes to world.wwco.<random>.tmp in the same directory, is synced,
// and is renamed over its target, so a reader sees the old file or the new
// one, never half of one; Open removes the temporary files a crash leaves.
const (
	FileName    = "world.wwco"
	BadName     = FileName + ".bad"
	JournalName = "journal.log"
	// BackupCount is how many rotated backups a world keeps.
	BackupCount = 5

	worldsSubdir = "ww-multiplayer/worlds"

	// DefaultFlushDelay is how long after a change the world is written.
	DefaultFlushDelay = time.Second
	// DefaultBackupEvery is how often a running session adds a backup.
	DefaultBackupEvery = time.Hour

	// journalRotateSize is the journal size past which a session start
	// moves it to journal.log.1.
	journalRotateSize = 1 << 20
	// maxFlushRetry caps the wait between retries of a failed flush.
	maxFlushRetry = time.Minute
)

// ErrStopped is the error of a Store method called after Stop.
var ErrStopped = errors.New("world: store stopped")

// Dir is the directory worlds are kept in: ww-multiplayer/worlds under
// os.UserConfigDir (%AppData% on Windows, ~/Library/Application Support
// on macOS, $XDG_CONFIG_HOME or ~/.config elsewhere).
func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("world: no config directory for co-op worlds: %w", err)
	}
	return filepath.Join(cfg, filepath.FromSlash(worldsSubdir)), nil
}

// Path is the directory of world id under root (Dir, or a test's own).
func Path(root string, id ID) string { return filepath.Join(root, id.String()) }

// backupName is the name of backup i, 1..BackupCount; 0 is the world file.
func backupName(i int) string {
	if i == 0 {
		return FileName
	}
	return FileName + "." + strconv.Itoa(i)
}

// LoadReport says where a loaded world came from.
type LoadReport struct {
	// From is the file the world was read from: FileName, or the backup
	// Load fell back to.
	From string
	// Skipped is the files tried before From, each with why it was not
	// used (missing, damaged, another world's).
	Skipped []error
	// Replayed is how many journal records were applied over From.
	Replayed int
	// BadLines is how many journal lines were skipped as damaged.
	BadLines int
	// Stripped is how many bits From held that this build's class table
	// (or the file's own tombstones) does not let the world hold.
	Stripped int
	// StaleTmp is how many temporary files of interrupted writes Open
	// removed.
	StaleTmp int
}

// FellBack reports whether the world did not come from the world file.
func (r LoadReport) FellBack() bool { return r.From != FileName }

// Load reads world id from its directory under root without changing
// anything: world.wwco, or if that is missing or damaged the newest good
// backup, then the journal records above its seq. It fails with an error
// matching fs.ErrNotExist when the world has no file at all, ErrCorrupt
// when every file is damaged, and ErrVersion when it reaches a file of
// another version: it never falls back past one, so that a world a newer
// build wrote is not replaced by an older backup.
func Load(root string, id ID) (*World, LoadReport, error) {
	return load(Path(root, id), id)
}

func load(dir string, id ID) (*World, LoadReport, error) {
	var rep LoadReport
	var w *World
	missing := 0
	for i := 0; i <= BackupCount && w == nil; i++ {
		name := backupName(i)
		b, err := readFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			missing++
			rep.Skipped = append(rep.Skipped, fmt.Errorf("%s: missing", name))
			continue
		}
		var stripped int
		if err == nil {
			w, stripped, err = decode(b)
		}
		if err == nil && w.id != id {
			err = corrupt("the file is world %s's", w.id)
			w = nil
		}
		if errors.Is(err, ErrVersion) {
			return nil, rep, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
		}
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Errorf("%s: %w", name, err))
			continue
		}
		rep.From, rep.Stripped = name, stripped
	}
	if w == nil {
		if missing == BackupCount+1 {
			return nil, LoadReport{}, fmt.Errorf("world: no world %s in %s: %w", id, dir, fs.ErrNotExist)
		}
		return nil, rep, fmt.Errorf("world %s: no good file in %s: %w", id, dir, errors.Join(rep.Skipped...))
	}
	for _, name := range []string{JournalName + ".1", JournalName} {
		recs, bad, err := readJournal(filepath.Join(dir, name))
		rep.BadLines += bad
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Errorf("%s: %w", name, err))
		}
		rep.Replayed += replay(w, recs)
	}
	return w, rep, nil
}

// readFile reads a world file, refusing one larger than any Encode writes.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileSize {
		return nil, corrupt("more than %d bytes", maxFileSize)
	}
	return b, nil
}

// Options tune a Store. The zero value is the design's defaults.
type Options struct {
	// FlushDelay is how long after a change the world is written
	// (DefaultFlushDelay if 0). Changes in the meantime ride along.
	FlushDelay time.Duration
	// BackupEvery is how often a running session adds a backup
	// (DefaultBackupEvery if 0).
	BackupEvery time.Duration
	// Logf, if set, gets the errors of background flushes and backups.
	Logf func(format string, args ...any)

	clock clock // tests' fake clock; nil means the real one
}

// clock is the time a Store runs on: real, or a test's.
type clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) stopper
}

type stopper interface{ Stop() bool }

type realClock struct{}

func (realClock) Now() time.Time                              { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }

// Store is a world kept in its directory for one session: the CoopHub's
// single writer. Every change is applied in memory, appended to the
// journal and written to world.wwco FlushDelay later; Stop writes what is
// left. Its methods are safe for concurrent use.
type Store struct {
	dir  string
	opts Options
	clk  clock

	mu         sync.Mutex
	w          *World
	dirty      bool
	stopped    bool
	journal    *os.File
	flushTimer stopper
	retry      time.Duration // the wait before retrying a failed flush; 0 when the last one worked
	backupTmr  stopper
	err        error // the last background error
}

// Create founds world w in its directory under root and starts its
// session. It fails with an error matching fs.ErrExist if the directory
// already holds a world file or a backup.
func Create(root string, w *World, opts Options) (*Store, error) {
	if w.id.IsZero() {
		return nil, errors.New("world: a world needs an ID")
	}
	dir := Path(root, w.id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("world: %w", err)
	}
	for i := 0; i <= BackupCount; i++ {
		if _, err := os.Lstat(filepath.Join(dir, backupName(i))); !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("world: %s already holds world %s (%s): %w", dir, w.id, backupName(i), fs.ErrExist)
		}
	}
	// A journal without a world file is left from nothing we can use.
	for _, name := range []string{JournalName, JournalName + ".1"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("world: %w", err)
		}
	}
	s := newStore(dir, w.Clone(), opts)
	s.dirty = true
	if err := s.flushLocked(); err != nil {
		return nil, err
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	return s, nil
}

// Open loads world id from its directory under root (Load) and starts its
// session. When the world did not come whole from world.wwco (a fallback,
// a replayed journal, stripped bits), world.wwco is rewritten at once; a
// damaged one is first kept as world.wwco.bad.
func Open(root string, id ID, opts Options) (*Store, LoadReport, error) {
	dir := Path(root, id)
	w, rep, err := load(dir, id)
	if err != nil {
		return nil, rep, err
	}
	s := newStore(dir, w, opts)
	rep.StaleTmp = removeStaleTmp(dir)
	if rep.FellBack() || rep.Replayed > 0 || rep.Stripped > 0 {
		main := filepath.Join(dir, FileName)
		if _, err := os.Lstat(main); err == nil && rep.FellBack() {
			if err := replaceFile(main, filepath.Join(dir, BadName)); err != nil {
				return nil, rep, fmt.Errorf("world: keeping the damaged %s: %w", FileName, err)
			}
		}
		s.dirty = true
		if err := s.flushLocked(); err != nil {
			return nil, rep, err
		}
	}
	if err := s.start(); err != nil {
		return nil, rep, err
	}
	return s, rep, nil
}

func newStore(dir string, w *World, opts Options) *Store {
	if opts.FlushDelay <= 0 {
		opts.FlushDelay = DefaultFlushDelay
	}
	if opts.BackupEvery <= 0 {
		opts.BackupEvery = DefaultBackupEvery
	}
	clk := opts.clock
	if clk == nil {
		clk = realClock{}
	}
	return &Store{dir: dir, opts: opts, clk: clk, w: w}
}

// start begins the session: the session-start backup, the journal (moved
// aside first if large) and the hourly backup timer.
func (s *Store) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.backupLocked(); err != nil {
		return err
	}
	path := filepath.Join(s.dir, JournalName)
	if fi, err := os.Stat(path); err == nil && fi.Size() >= journalRotateSize {
		if err := replaceFile(path, path+".1"); err != nil {
			return fmt.Errorf("world: rotating the journal: %w", err)
		}
	}
	j, err := openJournal(path)
	if err != nil {
		return fmt.Errorf("world: %w", err)
	}
	s.journal = j
	s.backupTmr = s.clk.AfterFunc(s.opts.BackupEvery, s.onBackupTimer)
	return nil
}

// openJournal opens the journal for appending. If a crash cut its last
// line short, a newline ends it first, so the next record is a line of its
// own.
func openJournal(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			_, err = f.Write([]byte{'\n'})
			if err != nil {
				f.Close()
				return nil, err
			}
		}
	}
	return f, nil
}

// ID is the world's ID.
func (s *Store) ID() ID { return s.w.id }

// Dir is the world's directory.
func (s *Store) Dir() string { return s.dir }

// Seq is the world's seq.
func (s *Store) Seq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.seq
}

// Image returns a copy of the merged image.
func (s *Store) Image() Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.image
}

// World returns a copy of the world.
func (s *Store) World() *World {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Clone()
}

// SetPolicy sets the policy proposals are checked against (World.SetPolicy).
func (s *Store) SetPolicy(p *Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w.SetPolicy(p)
}

// IsFreshBind reports whether nonce is a pending fresh bind.
func (s *Store) IsFreshBind(nonce ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.IsFreshBind(nonce)
}

// ApplyGains is World.ApplyGains, journaled with origin and flushed.
func (s *Store) ApplyGains(origin Player, gs []Gain) (Result, error) {
	return s.change(origin, OpGains, func(w *World) Result { return w.ApplyGains(gs) })
}

// Merge is World.Merge, journaled with origin and flushed.
func (s *Store) Merge(origin Player, img *Image) (Result, error) {
	return s.change(origin, OpWorld, func(w *World) Result { return w.Merge(img) })
}

// Tombstone is World.Tombstone, journaled with origin 0 (only the host
// tombstones) and flushed.
func (s *Store) Tombstone(off int, mask byte) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return Result{Seq: s.w.seq}, ErrStopped
	}
	before := s.w.seq
	r, err := s.w.Tombstone(off, mask)
	if err == nil && r.Seq != before {
		g := Gain{Off: uint16(off), Val: mask & shared[off]}
		s.journalLocked(Record{Seq: r.Seq, Time: s.clk.Now(), Op: OpTombstone, Gains: []Gain{g}})
	}
	return r, err
}

// AddFreshBind is World.AddFreshBind at the store's clock, journaled and
// flushed.
func (s *Store) AddFreshBind(nonce ID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false, ErrStopped
	}
	now := s.clk.Now()
	added, err := s.w.AddFreshBind(nonce, now)
	if added {
		s.journalLocked(Record{Seq: s.w.seq, Time: now, Op: OpFresh, Nonce: nonce, Minted: msTime(now)})
	}
	return added, err
}

// ClearFreshBind is World.ClearFreshBind, journaled and flushed.
func (s *Store) ClearFreshBind(nonce ID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false, ErrStopped
	}
	cleared := s.w.ClearFreshBind(nonce)
	if cleared {
		s.journalLocked(Record{Seq: s.w.seq, Time: s.clk.Now(), Op: OpUnfresh, Nonce: nonce})
	}
	return cleared, nil
}

// change runs one world operation and journals it if it changed anything.
func (s *Store) change(origin Player, op Op, f func(*World) Result) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return Result{Seq: s.w.seq}, ErrStopped
	}
	r := f(s.w)
	if r.Changed() {
		s.journalLocked(Record{Seq: r.Seq, Time: s.clk.Now(), Origin: origin, Op: op, Gains: r.Delta})
	}
	return r, nil
}

// journalLocked appends rec to the journal and schedules a flush.
func (s *Store) journalLocked(rec Record) {
	if s.journal != nil {
		if _, err := s.journal.WriteString(rec.Format() + "\n"); err != nil {
			s.failLocked(fmt.Errorf("world: journal: %w", err))
		}
	}
	s.dirty = true
	if s.flushTimer == nil {
		s.flushTimer = s.clk.AfterFunc(s.opts.FlushDelay, s.onFlushTimer)
	}
}

func (s *Store) onFlushTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushTimer = nil
	if s.stopped {
		return
	}
	if err := s.flushLocked(); err != nil {
		s.failLocked(err)
		s.retry = min(max(2*s.retry, s.opts.FlushDelay), maxFlushRetry)
		s.flushTimer = s.clk.AfterFunc(s.retry, s.onFlushTimer)
		return
	}
	s.retry = 0
}

func (s *Store) onBackupTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if err := s.backupLocked(); err != nil {
		s.failLocked(err)
	}
	s.backupTmr = s.clk.AfterFunc(s.opts.BackupEvery, s.onBackupTimer)
}

func (s *Store) failLocked(err error) {
	s.err = err
	if s.opts.Logf != nil {
		s.opts.Logf("%v", err)
	}
}

// Err returns the last error of a background flush, backup or journal
// write, or nil.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Flush writes the world now if it changed since the last write.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	if !s.dirty {
		return nil
	}
	if err := writeAtomic(s.dir, filepath.Join(s.dir, FileName), Encode(s.w)); err != nil {
		return fmt.Errorf("world: writing %s: %w", FileName, err)
	}
	s.dirty = false
	return nil
}

// Backup rotates the backups (world.wwco.4 to .5, ..., .1 to .2) and
// writes the world as it is now to world.wwco.1. A session start and every
// BackupEvery of a running session take one.
func (s *Store) Backup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrStopped
	}
	return s.backupLocked()
}

func (s *Store) backupLocked() error {
	for i := BackupCount - 1; i >= 1; i-- {
		from := filepath.Join(s.dir, backupName(i))
		if _, err := os.Lstat(from); err != nil {
			continue
		}
		if err := replaceFile(from, filepath.Join(s.dir, backupName(i+1))); err != nil {
			return fmt.Errorf("world: rotating %s: %w", backupName(i), err)
		}
	}
	if err := writeAtomic(s.dir, filepath.Join(s.dir, backupName(1)), Encode(s.w)); err != nil {
		return fmt.Errorf("world: writing %s: %w", backupName(1), err)
	}
	return nil
}

// Stop ends the session: it writes the world if it changed, stops the
// timers and closes the journal. Later changes fail with ErrStopped.
// Idempotent.
func (s *Store) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopped = true
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
	if s.backupTmr != nil {
		s.backupTmr.Stop()
		s.backupTmr = nil
	}
	err := s.flushLocked()
	if s.journal != nil {
		err = errors.Join(err, s.journal.Close())
		s.journal = nil
	}
	return err
}

// removeStaleTmp removes the temporary files interrupted writes left in
// dir and returns how many.
func removeStaleTmp(dir string) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if name := e.Name(); strings.HasPrefix(name, FileName+".") && strings.HasSuffix(name, ".tmp") && !e.IsDir() {
			if os.Remove(filepath.Join(dir, name)) == nil {
				n++
			}
		}
	}
	return n
}

// writeAtomic writes b to path through a temporary file in dir: written,
// synced, closed, then renamed over path, and the directory synced where
// the OS allows it. On any failure path is untouched and the temporary
// file removed.
func writeAtomic(dir, path string, b []byte) error {
	f, err := os.CreateTemp(dir, FileName+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = replaceFile(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes a rename in dir durable. Windows can't open a directory
// for it (and its renames go through the journaling NTFS keeps), so
// failures are ignored.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// Windows refuses to rename over a file another process has open without
// FILE_SHARE_DELETE: Defender or the search indexer looking at a world
// file just written, or a `coop-world` command reading it. Like the M1
// tables cache (internal/coop/tables/cache.go) and Go's own toolchain
// (cmd/go/internal/robustio), renames are retried for about 2 s: from
// renameRetryFirst, doubling up to renameRetryMaxStep, until the next wait
// would pass renameRetryBudget. (Raw Errnos so this builds everywhere; 5
// and 32 are EIO and EPIPE on Unix, which aren't retried.)
const (
	winErrorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	winErrorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION

	renameRetryFirst   = 5 * time.Millisecond
	renameRetryMaxStep = 250 * time.Millisecond
	renameRetryBudget  = 2 * time.Second
)

// replaceFile renames from over to, retrying the Windows sharing errors.
// It is a variable so tests can make it fail.
var replaceFile = func(from, to string) error {
	return retryRename(os.Rename, from, to, renameErrIsTransient, time.Sleep)
}

// renameErrIsTransient reports whether a failed rename may succeed if
// retried: one of the Windows sharing errors, on Windows.
func renameErrIsTransient(err error) bool {
	return runtime.GOOS == "windows" && isWindowsSharingError(err)
}

func isWindowsSharingError(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == winErrorAccessDenied || errno == winErrorSharingViolation)
}

// retryRename calls rename until it succeeds, fails with an error
// transient rejects, or the retry budget runs out, waiting through sleep.
func retryRename(rename func(oldpath, newpath string) error, oldpath, newpath string,
	transient func(error) bool, sleep func(time.Duration)) error {
	delay, slept := renameRetryFirst, time.Duration(0)
	for {
		err := rename(oldpath, newpath)
		if err == nil || !transient(err) || slept+delay > renameRetryBudget {
			return err
		}
		sleep(delay)
		slept += delay
		delay = min(delay*2, renameRetryMaxStep)
	}
}
