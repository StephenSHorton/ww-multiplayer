package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The backup store: <root>/<id>/ per backup, holding manifest.json and a
// files/ directory of read-only copies. The id is the UTC time it was
// made, 20261014T153012Z, with -2, -3... on a clash; claiming it is an
// os.Mkdir, so two sessions starting at once (mp-local's two Dolphins)
// never share one. manifest.json is written last, by rename, so a backup
// without one is unfinished: List skips it, and Create removes it once it
// is an hour old.
const (
	// Keep is how many backups Create keeps (§5.4).
	Keep = 10

	backupsSubdir = "ww-multiplayer/backups"
	manifestName  = "manifest.json"
	filesDir      = "files"

	// ManifestVersion is the manifest layout this version writes.
	ManifestVersion = 1

	// maxCardFile caps one copied file. Dolphin's biggest raw card,
	// 2043 blocks, is 16 MiB; anything larger is not a card.
	maxCardFile = 32 << 20

	// staleUnfinished is how old an unfinished backup must be before
	// Create removes it: older than any backup takes to write.
	staleUnfinished = time.Hour
)

// idPattern matches a backup id, and nothing that could leave the root.
var idPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z(-[0-9]{1,4})?$`)

const idLayout = "20060102T150405Z"

// ErrNoCards is Create's error when no user directory has a card to back
// up.
var ErrNoCards = errors.New("backup: no Dolphin memory card found")

// Dir is the backup root: ww-multiplayer/backups under os.UserConfigDir
// (%AppData% on Windows, ~/Library/Application Support on macOS,
// $XDG_CONFIG_HOME or ~/.config elsewhere).
func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("backup: no config directory for backups: %w", err)
	}
	return filepath.Join(cfg, filepath.FromSlash(backupsSubdir)), nil
}

// Options configure Create and Restore. The zero Options are the
// defaults.
type Options struct {
	Root  string           // the backup root; "" for Dir()
	Keep  int              // backups to keep; 0 for Keep
	Label string           // why the backup is made, e.g. "session"
	Now   func() time.Time // the clock; nil for time.Now

	protect string // a backup id pruning must keep (Restore's source)
}

func (o Options) root() (string, error) {
	if o.Root != "" {
		return o.Root, nil
	}
	return Dir()
}

func (o Options) keep() int {
	if o.Keep > 0 {
		return o.Keep
	}
	return Keep
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Backup is one backup, as its manifest records it.
type Backup struct {
	ID  string `json:"-"` // the directory name
	Dir string `json:"-"` // its path

	Version  int       `json:"version"`
	Created  time.Time `json:"created"`
	Label    string    `json:"label,omitempty"`
	UserDirs []string  `json:"user_dirs"`
	// Cards are the cards found; Files the files copied from them.
	Cards []Card `json:"cards"`
	Files []File `json:"files"`
}

// File is one copied file.
type File struct {
	Card int `json:"card"` // index into Backup.Cards
	// Source is where it was copied from, and where Restore puts it.
	Source string `json:"source"`
	// Stored is the copy, relative to the backup's directory, with
	// forward slashes.
	Stored  string    `json:"stored"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	ModTime time.Time `json:"mod_time"`
}

// Size is the bytes the backup's copies take.
func (b *Backup) Size() int64 {
	var n int64
	for _, f := range b.Files {
		n += f.Size
	}
	return n
}

// Create backs up every card Locate finds in userDirs (§5.4): raw images
// whole, and Wind Waker's .gci files from GCI folders. The sources are
// only read. Each copy is taken again if the source changed while it was
// read (Dolphin flushing the card), and is made read-only. Once the new
// backup is complete, the oldest beyond Options.Keep are deleted. A
// missing user directory has no cards; when none has any, the error is
// ErrNoCards and nothing is written.
func Create(userDirs []string, opt Options) (*Backup, error) {
	root, err := opt.root()
	if err != nil {
		return nil, err
	}
	b := &Backup{Version: ManifestVersion, Label: opt.Label}
	seen := map[string]bool{} // user dirs and sources, each taken once
	for _, ud := range userDirs {
		if ud == "" {
			continue
		}
		abs, err := filepath.Abs(ud)
		if err != nil {
			return nil, fmt.Errorf("backup: %w", err)
		}
		if seen[pathKey(abs)] {
			continue
		}
		seen[pathKey(abs)] = true
		b.UserDirs = append(b.UserDirs, abs)
		cards, err := Locate(abs)
		if err != nil {
			return nil, err
		}
		for _, c := range cards {
			files := []string{c.Path}
			if c.Kind == GCIFolder {
				if files, err = gciFiles(c.Path); err != nil {
					return nil, err
				}
			}
			files = slices.DeleteFunc(files, func(f string) bool { return seen[pathKey(filepath.Clean(f))] })
			if len(files) == 0 {
				continue
			}
			b.Cards = append(b.Cards, c)
			for _, f := range files {
				seen[pathKey(filepath.Clean(f))] = true
				b.Files = append(b.Files, File{Card: len(b.Cards) - 1, Source: f})
			}
		}
	}
	if len(b.Files) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoCards, strings.Join(userDirs, ", "))
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	b.Created = opt.now().UTC().Truncate(time.Second)
	if b.ID, b.Dir, err = claim(root, b.Created); err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			removeAll(b.Dir)
		}
	}()
	if err := os.Mkdir(filepath.Join(b.Dir, filesDir), 0o755); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	for i := range b.Files {
		f := &b.Files[i]
		f.Stored = path.Join(filesDir, fmt.Sprintf("%02d-%s", i, storedName(filepath.Base(f.Source))))
		if err := copyStable(f, filepath.Join(b.Dir, filepath.FromSlash(f.Stored))); err != nil {
			return nil, err
		}
	}
	if err := writeManifest(b); err != nil {
		return nil, err
	}
	ok = true
	prune(root, opt.keep(), b.ID, opt.protect, opt.now())
	return b, nil
}

// storedName is a source file's name made safe for any file system and
// for validStored: characters other than letters, digits, '.', '-', '_'
// and ' ' become '_'.
func storedName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == ' ':
			return r
		}
		return '_'
	}, name)
}

// claim makes the new backup's directory, under the first free id for t.
func claim(root string, t time.Time) (id, dir string, err error) {
	base := t.Format(idLayout)
	for n := 1; n < 1000; n++ {
		id = base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		dir = filepath.Join(root, id)
		err = os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("backup: %w", err)
		}
	}
	return "", "", fmt.Errorf("backup: no free backup name for %s in %s", base, root)
}

// copyAttempts is how often copyStable reads a source that keeps
// changing, or that Windows won't share, before giving up.
const copyAttempts = 4

// copyStable copies f.Source to dst, filling in f's size, hash and
// modification time. The copy counts only if the source's size and
// modification time are the same after the read as before, so a card
// Dolphin rewrote meanwhile is read again.
func copyStable(f *File, dst string) error {
	var last error
	for attempt := range copyAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		before, err := os.Stat(f.Source)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if !before.Mode().IsRegular() {
			return fmt.Errorf("backup: %s is not a regular file", f.Source)
		}
		if before.Size() > maxCardFile {
			return fmt.Errorf("backup: %s is %d bytes, too big for a memory card", f.Source, before.Size())
		}
		n, sum, err := copyFile(f.Source, dst, 0o444)
		if err != nil {
			last = err
			if isSharingError(err) {
				continue
			}
			return err
		}
		after, err := os.Stat(f.Source)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if n == before.Size() && after.Size() == before.Size() && after.ModTime().Equal(before.ModTime()) {
			f.Size, f.SHA256, f.ModTime = n, sum, before.ModTime().UTC()
			return nil
		}
		last = fmt.Errorf("backup: %s changed while it was copied", f.Source)
		removeAll(dst)
	}
	return last
}

// copyFile copies src to a new file dst with mode perm, synced, and
// returns its size and SHA-256. src is opened read-only.
func copyFile(src, dst string, perm fs.FileMode) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", fmt.Errorf("backup: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("backup: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, maxCardFile+1))
	if err == nil && n > maxCardFile {
		err = fmt.Errorf("%s grew past %d bytes", src, maxCardFile)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(dst, perm)
	}
	if err != nil {
		os.Remove(dst)
		return 0, "", fmt.Errorf("backup: copying %s: %w", src, err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// writeManifest writes b's manifest through a temporary file, so it
// appears whole or not at all.
func writeManifest(b *Backup) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tmp := filepath.Join(b.Dir, manifestName+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if f, err := os.Open(tmp); err == nil {
		f.Sync()
		f.Close()
	}
	if err := renameRetry(tmp, filepath.Join(b.Dir, manifestName)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// maxManifest caps the manifest Open reads.
const maxManifest = 1 << 20

// Open reads the backup id under root, checking its manifest names only
// files inside the backup and absolute sources.
func Open(root, id string) (*Backup, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("backup: %q is not a backup name (like 20261014T153012Z)", id)
	}
	dir := filepath.Join(root, id)
	f, err := os.Open(filepath.Join(dir, manifestName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("backup: no complete backup %s in %s", id, root)
		}
		return nil, fmt.Errorf("backup: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifest+1))
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	b, err := decodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("backup %s: %w", id, err)
	}
	b.ID, b.Dir = id, dir
	return b, nil
}

// decodeManifest parses and checks a manifest.
func decodeManifest(data []byte) (*Backup, error) {
	if len(data) > maxManifest {
		return nil, errors.New("manifest too big")
	}
	b := new(Backup)
	if err := json.Unmarshal(data, b); err != nil {
		return nil, fmt.Errorf("bad manifest: %w", err)
	}
	if b.Version != ManifestVersion {
		return nil, fmt.Errorf("manifest version %d, this version reads %d", b.Version, ManifestVersion)
	}
	if len(b.Files) == 0 {
		return nil, errors.New("manifest lists no files")
	}
	seen := map[string]bool{}
	for i, f := range b.Files {
		switch {
		case f.Card < 0 || f.Card >= len(b.Cards):
			return nil, fmt.Errorf("file %d names card %d of %d", i, f.Card, len(b.Cards))
		case !filepath.IsAbs(f.Source):
			return nil, fmt.Errorf("file %d's source %q is not absolute", i, f.Source)
		case !validStored(f.Stored):
			return nil, fmt.Errorf("file %d is stored at %q, outside the backup", i, f.Stored)
		case f.Size < 0 || f.Size > maxCardFile || len(f.SHA256) != 2*sha256.Size:
			return nil, fmt.Errorf("file %d has size %d, hash %q", i, f.Size, f.SHA256)
		case seen[pathKey(filepath.Clean(f.Source))]:
			return nil, fmt.Errorf("file %d's source %s is listed twice", i, f.Source)
		}
		seen[pathKey(filepath.Clean(f.Source))] = true
	}
	return b, nil
}

// validStored reports whether s names a file under files/ and nowhere
// else.
func validStored(s string) bool {
	if s == "" || strings.ContainsAny(s, `\:`) || path.Clean(s) != s {
		return false
	}
	dir, name := path.Split(s)
	return dir == filesDir+"/" && name != "" && name != "." && name != ".."
}

// List returns the complete backups under root, newest first. A missing
// root has none; a directory that isn't a complete backup is skipped.
func List(root string) ([]*Backup, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	var out []*Backup
	for _, e := range ents {
		if !e.IsDir() || !idPattern.MatchString(e.Name()) {
			continue
		}
		if b, err := Open(root, e.Name()); err == nil {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return idSeq(out[i].ID) > idSeq(out[j].ID)
	})
	return out, nil
}

// idSeq is the clash number of an id: 1 without a suffix.
func idSeq(id string) int {
	n := 1
	if _, s, ok := strings.Cut(id, "-"); ok {
		fmt.Sscan(s, &n)
	}
	return n
}

// Verify checks every copy in b against its recorded size and SHA-256.
func (b *Backup) Verify() error {
	for _, f := range b.Files {
		got, sum, err := hashFile(filepath.Join(b.Dir, filepath.FromSlash(f.Stored)))
		if err != nil {
			return fmt.Errorf("backup %s: %w", b.ID, err)
		}
		if got != f.Size || sum != f.SHA256 {
			return fmt.Errorf("backup %s: %s is damaged (%d bytes, sha256 %.12s; recorded %d, %.12s)",
				b.ID, f.Stored, got, sum, f.Size, f.SHA256)
		}
	}
	return nil
}

func hashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxCardFile+1))
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// prune deletes the complete backups other than the newest keep, except
// that keep1 and keep2 always stay, and unfinished backups older than
// staleUnfinished. Its failures leave a backup too many, which the next
// Create retries, so they are not reported.
func prune(root string, keep int, keep1, keep2 string, now time.Time) {
	all, err := List(root)
	if err != nil {
		return
	}
	complete := map[string]bool{}
	kept := 0
	for _, b := range all { // newest first
		complete[b.ID] = true
		if b.ID != keep1 && b.ID != keep2 && kept >= keep {
			removeAll(b.Dir)
			continue
		}
		kept++
	}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || !idPattern.MatchString(name) || complete[name] || name == keep1 || name == keep2 {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > staleUnfinished {
			removeAll(filepath.Join(root, name))
		}
	}
}

// removeAll removes p, making its read-only copies writable first, which
// Windows needs to delete them.
func removeAll(p string) {
	filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chmod(q, 0o644)
		}
		return nil
	})
	os.RemoveAll(p)
}

// Windows refuses to rename over, or open, a file another process holds
// without sharing (Dolphin flushing a card, Defender or the indexer
// looking at a new file). Like wwapi/tables and internal/inject,
// those errors are retried for about 2 s. (Spelled as raw Errnos so this
// builds everywhere; 5 and 32 are EIO and EPIPE on Unix, which aren't
// retried.)
const (
	winErrorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	winErrorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION

	retryFirst   = 5 * time.Millisecond
	retryMaxStep = 250 * time.Millisecond
	retryBudget  = 2 * time.Second
)

func isSharingError(err error) bool {
	var errno syscall.Errno
	return runtime.GOOS == "windows" && errors.As(err, &errno) &&
		(errno == winErrorAccessDenied || errno == winErrorSharingViolation)
}

// renameRetry renames oldpath over newpath, retrying the Windows sharing
// errors.
func renameRetry(oldpath, newpath string) error {
	delay, slept := retryFirst, time.Duration(0)
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || !isSharingError(err) || slept+delay > retryBudget {
			return err
		}
		time.Sleep(delay)
		slept += delay
		delay = min(delay*2, retryMaxStep)
	}
}
