package tables

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// The cache: one JSON file per DOL hash, <dir>/<dol sha256>.json, holding
// a whole Tables. A file is used only when its schema, its builder and
// its whole ID match; any other file, or one that doesn't parse, is
// rebuilt over. Writes go to a temporary file in dir, renamed into place,
// so a reader never sees half a file and two runs at once each leave a
// whole one (see replaceCache for Windows).
const (
	cacheSubdir = "ww-multiplayer/coop-tables"

	// maxCacheSize caps the file Load reads. Real tables are a few MiB
	// (thousands of switches and enemy entries); a larger file is not one
	// Save wrote.
	maxCacheSize = 64 << 20
)

var (
	// ErrStale is Load's error for a cache file of another schema, another
	// binary or another disc.
	ErrStale = errors.New("tables: stale cache")
	// ErrCorrupt is Load's error for a cache file that doesn't parse as
	// whole tables.
	ErrCorrupt = errors.New("tables: corrupt cache")
)

// CacheDir is the directory Get keeps tables in: ww-multiplayer/
// coop-tables under os.UserConfigDir (%AppData% on Windows, ~/Library/
// Application Support on macOS, $XDG_CONFIG_HOME or ~/.config elsewhere).
func CacheDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("tables: no config directory for the cache: %w", err)
	}
	return filepath.Join(cfg, filepath.FromSlash(cacheSubdir)), nil
}

// CacheFile is the file in dir that holds the tables of the DOL whose
// SHA-256 is dol.
func CacheFile(dir, dol string) string { return filepath.Join(dir, dol+".json") }

// Get returns src's tables: the ones cached in dir when they are for this
// disc, schema and binary, else built from src (Build) and saved there.
// An empty dir skips the cache. A cache that can't be used is rebuilt
// over, and one that can't be saved leaves the tables returned all the
// same; Stats says which happened.
func Get(src Source, dir string) (*Tables, Stats, error) {
	var st Stats
	start := time.Now()
	id, dol, err := identify(src)
	st.Identify = time.Since(start)
	if err != nil {
		return nil, st, err
	}
	if dir != "" {
		st.Path = CacheFile(dir, id.DOL)
		start = time.Now()
		t, err := load(st.Path, id)
		if err == nil {
			st.Cached, st.Read = true, time.Since(start)
			return t, st, nil
		}
		st.Why = strings.TrimPrefix(err.Error(), "tables: ")
		if errors.Is(err, fs.ErrNotExist) {
			st.Why = "no cache file yet"
		}
	}
	t, err := build(src, dol, id, &st)
	if err != nil {
		return nil, st, err
	}
	if dir != "" {
		start = time.Now()
		_, st.SaveErr = t.Save(dir)
		st.Save = time.Since(start)
	}
	return t, st, nil
}

// Load reads the tables cached in dir for id. A missing file is an error
// matching fs.ErrNotExist; a file of another schema, binary or disc one
// matching ErrStale; one that doesn't parse, or lacks a section, one
// matching ErrCorrupt.
func Load(dir string, id ID) (*Tables, error) {
	if !validHash(id.DOL) {
		return nil, fmt.Errorf("tables: %q is not a SHA-256 in hex", id.DOL)
	}
	path := CacheFile(dir, id.DOL)
	t, err := load(path, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, err
}

// load reads the cache file path as the tables for id. Only the errors
// the OS returns (an *fs.PathError) name the file.
func load(path string, id ID) (*Tables, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxCacheSize+1))
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	return decode(b, id)
}

// decode parses a cache file's bytes as the tables for id.
func decode(b []byte, id ID) (*Tables, error) {
	if len(b) > maxCacheSize {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrCorrupt, maxCacheSize)
	}
	var head struct {
		Schema  int    `json:"schema"`
		Builder string `json:"builder"`
		ID
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	switch {
	case head.Schema != SchemaVersion:
		return nil, fmt.Errorf("%w: schema %d, this binary writes %d", ErrStale, head.Schema, SchemaVersion)
	case head.Builder != builderID():
		return nil, fmt.Errorf("%w: built by %q, this binary is %q", ErrStale, head.Builder, builderID())
	case head.ID.DOL != id.DOL:
		return nil, fmt.Errorf("%w: for main.dol %.12s, not %.12s", ErrStale, head.ID.DOL, id.DOL)
	case head.ID.StageFiles != id.StageFiles:
		return nil, fmt.Errorf("%w: the disc's stage files changed (listing %.12s, cached %.12s)", ErrStale, id.StageFiles, head.ID.StageFiles)
	}
	// The same schema and binary wrote every field, so an unknown one is
	// damage. (Unmarshal has already refused data after the value.)
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	t := new(Tables)
	if err := dec.Decode(t); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if !t.complete() {
		return nil, fmt.Errorf("%w: a section is missing", ErrCorrupt)
	}
	return t, nil
}

// Save writes t to its cache file in dir, creating dir, and returns the
// file's path. Only whole tables (every section) are saved.
func (t *Tables) Save(dir string) (string, error) {
	if !t.complete() {
		return "", errors.New("tables: only whole tables are cached")
	}
	if !validHash(t.DOL) {
		return "", fmt.Errorf("tables: %q is not a SHA-256 in hex", t.DOL)
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("tables: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("tables: %w", err)
	}
	path := CacheFile(dir, t.DOL)
	current := func() bool { _, err := load(path, t.ID); return err == nil }
	if err := writeAtomic(dir, path, b, current); err != nil {
		return "", fmt.Errorf("tables: %w", err)
	}
	return path, nil
}

// writeAtomic writes b to path through a temporary file in dir. If the
// rename fails for good but current then accepts the file at path (whole
// tables for the same disc, schema and binary, which another run saved
// meanwhile), the write counts as done.
func writeAtomic(dir, path string, b []byte, current func() bool) error {
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
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
	renamed := false
	if err == nil {
		if err = replaceCache(tmp, path); err == nil {
			renamed = true
		} else if current() {
			err = nil
		}
	}
	if !renamed {
		os.Remove(tmp)
	}
	return err
}

// Windows refuses to rename over a file another process has open without
// FILE_SHARE_DELETE, which os.Open leaves out: a second coop-tables run
// loading the cache, or Defender or the search indexer looking at a new
// file. The rename then fails with one of these, and like the patcher
// (internal/inject's retryRename) and Go's own toolchain
// (cmd/go/internal/robustio), Save retries it for about 2 s: from
// renameRetryFirst, doubling up to renameRetryMaxStep, until the next
// wait would pass renameRetryBudget. (Spelled as raw Errnos so this builds
// everywhere; 5 and 32 are EIO and EPIPE on Unix, which aren't retried.)
const (
	winErrorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	winErrorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION

	renameRetryFirst   = 5 * time.Millisecond
	renameRetryMaxStep = 250 * time.Millisecond
	renameRetryBudget  = 2 * time.Second
)

// replaceCache renames tmp over path, retrying the Windows sharing
// errors. It is a variable so tests can make the rename fail.
var replaceCache = func(tmp, path string) error {
	return retryRename(os.Rename, tmp, path, renameErrIsTransient, time.Sleep)
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

// validHash reports whether s is a SHA-256 in lowercase hex, as ID holds
// it: the only names CacheFile is given, so a path can't escape dir.
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
