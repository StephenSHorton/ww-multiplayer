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
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// The last-patched record: `patch` builds the tables of the disc it writes
// (the patched one, whose main.dol hash names a cache file of its own) and
// records in the cache directory which cache entry and which image that
// was. A co-op session started without --coop-iso then knows the tables of
// the disc Dolphin most likely runs: the one the user last patched. The
// disc is recorded even when its tables can't be built, so the session
// tries the image again and says why it fails, and a later binary that
// can build them needs no new patch.
const (
	lastPatchedName = "last-patched.json"
	// LastPatchedSchema is the record's format.
	LastPatchedSchema = 1
	// maxLastPatchedSize caps the record ReadLastPatched reads: a path and
	// two hashes, far below it.
	maxLastPatchedSize = 64 << 10
)

// LastPatched is the record of the disc `patch` last wrote.
type LastPatched struct {
	Schema int `json:"schema"`
	// ID is the disc's (Stats.ID), and names its tables' cache entry,
	// CacheFile(dir, ID.DOL), once they are built and cached.
	ID
	// Image is the patched image's absolute path.
	Image string `json:"image"`
	// At is when it was recorded.
	At time.Time `json:"at"`
}

// ErrNoRecord is ReadLastPatched's error, and GetPatched's, when dir holds
// no record: patch has recorded no disc there.
var ErrNoRecord = errors.New("patch has recorded no disc")

// LastPatchedFile is the record's path in dir.
func LastPatchedFile(dir string) string { return filepath.Join(dir, lastPatchedName) }

// RecordPatched records in dir that image, the disc whose ID is id, is
// the disc last patched. image is made absolute, so a session started from
// another directory finds it.
func RecordPatched(dir, image string, id ID, at time.Time) error {
	abs, err := filepath.Abs(image)
	if err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	rec := LastPatched{Schema: LastPatchedSchema, ID: id, Image: abs, At: at.UTC().Truncate(time.Millisecond)}
	if err := rec.check(); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	path := LastPatchedFile(dir)
	current := func() bool { got, err := os.ReadFile(path); return err == nil && bytes.Equal(got, b) }
	if err := writeAtomic(dir, path, b, current); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	return nil
}

// ReadLastPatched reads dir's record. Without one the error matches
// ErrNoRecord and fs.ErrNotExist; a record of another format matches
// ErrStale, and one that doesn't parse or check matches ErrCorrupt.
func ReadLastPatched(dir string) (LastPatched, error) {
	f, err := os.Open(LastPatchedFile(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return LastPatched{}, fmt.Errorf("tables: %w: %w", ErrNoRecord, err)
	}
	if err != nil {
		return LastPatched{}, fmt.Errorf("tables: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxLastPatchedSize+1))
	if err != nil {
		return LastPatched{}, fmt.Errorf("tables: %w", err)
	}
	return decodeLastPatched(b)
}

func decodeLastPatched(b []byte) (LastPatched, error) {
	if len(b) > maxLastPatchedSize {
		return LastPatched{}, fmt.Errorf("%w: the last-patched record is more than %d bytes", ErrCorrupt, maxLastPatchedSize)
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return LastPatched{}, fmt.Errorf("%w: the last-patched record: %v", ErrCorrupt, err)
	}
	if head.Schema != LastPatchedSchema {
		return LastPatched{}, fmt.Errorf("%w: the last-patched record has format %d, this binary reads %d", ErrStale, head.Schema, LastPatchedSchema)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var rec LastPatched
	if err := dec.Decode(&rec); err != nil {
		return LastPatched{}, fmt.Errorf("%w: the last-patched record: %v", ErrCorrupt, err)
	}
	if err := rec.check(); err != nil {
		return LastPatched{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return rec, nil
}

// check reports what makes rec a record RecordPatched would not write.
func (rec LastPatched) check() error {
	switch {
	case rec.Schema != LastPatchedSchema:
		return fmt.Errorf("last-patched record format %d", rec.Schema)
	case !validHash(rec.DOL) || !validHash(rec.StageFiles):
		return errors.New("the last-patched record's hashes are not SHA-256s in hex")
	case rec.Image == "" || !filepath.IsAbs(rec.Image) || strings.ContainsRune(rec.Image, 0):
		return fmt.Errorf("the last-patched image %q is not an absolute path", rec.Image)
	}
	return nil
}

// PatchedStats says how GetPatched came by its tables.
type PatchedStats struct {
	Stats
	// Record is the last-patched record read.
	Record LastPatched
	// Changed is true when the recorded image now holds another disc (it
	// was patched again by other means, or replaced): its tables were
	// built, and the record now names them.
	Changed bool
	// RecordErr is why the record could not be updated; the tables are
	// returned all the same.
	RecordErr error
}

// GetPatched returns the tables of the disc dir's record names: the cached
// ones while they are this build's, else the recorded image's, read with
// Get (which caches them). It fails without a record (an error matching
// ErrNoRecord), and when the tables are not cached for this build and the
// image can't be read, or can't give them (Get's *BuildError, wrapped).
func GetPatched(dir string) (*Tables, PatchedStats, error) {
	var ps PatchedStats
	rec, err := ReadLastPatched(dir)
	if err != nil {
		return nil, ps, err
	}
	ps.Record = rec
	start := time.Now()
	t, err := Load(dir, rec.ID)
	if err == nil {
		ps.Cached, ps.Path, ps.Read = true, CacheFile(dir, rec.DOL), time.Since(start)
		return t, ps, nil
	}
	why := strings.TrimPrefix(err.Error(), "tables: ")
	if errors.Is(err, fs.ErrNotExist) {
		why = "not cached"
	}
	d, err := inject.OpenDisc(rec.Image)
	if err != nil {
		return nil, ps, fmt.Errorf("tables: the tables of %s, the disc last patched, are %s, and the image can't be read: %w", rec.Image, why, err)
	}
	defer d.Close()
	t, st, err := Get(d, dir)
	if err != nil {
		return nil, ps, fmt.Errorf("%w (reading %s, the disc last patched)", err, rec.Image)
	}
	ps.Stats = st
	if t.ID != rec.ID {
		ps.Changed = true
		ps.RecordErr = RecordPatched(dir, rec.Image, t.ID, time.Now())
	}
	return t, ps, nil
}
