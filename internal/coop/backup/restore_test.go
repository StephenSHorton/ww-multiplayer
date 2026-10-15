package backup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// procs is a fake ProcessFinder: each call returns the next answer, the
// last one repeating.
type procs struct {
	answers [][]uint32
	err     error
	calls   int
}

func (p *procs) DolphinPIDs() ([]uint32, error) {
	i := min(p.calls, len(p.answers)-1)
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	if i < 0 {
		return nil, nil
	}
	return p.answers[i], nil
}

// restoreRig is a user dir with a raw card and a GCI folder, backed up
// once, then played on: the cards now differ from the backup.
type restoreRig struct {
	u        *userDir
	root     string
	b        *Backup
	raw, ww  string
	now      func() time.Time
	before   map[string][]byte // the backed-up contents
	afterRaw []byte
}

func newRestoreRig(t *testing.T) *restoreRig {
	t.Helper()
	r := &restoreRig{u: newUserDir(t), root: t.TempDir(), now: clock(day0)}
	r.raw = r.u.write("GC/MemoryCardA.USA.raw", []byte("raw card, before"))
	r.ww = r.u.write("GC/USA/Card A/01-GZLE-gczelda.gci", gci("GZLE", "save, before"))
	b, err := Create([]string{r.u.root}, Options{Root: r.root, Now: r.now})
	if err != nil {
		t.Fatal(err)
	}
	r.b = b
	r.before = map[string][]byte{r.raw: read(t, r.raw), r.ww: read(t, r.ww)}
	r.afterRaw = []byte("raw card, after a bad merge")
	writeFile(t, r.raw, string(r.afterRaw))
	writeFile(t, r.ww, string(gci("GZLE", "save, after")))
	return r
}

func (r *restoreRig) opts() Options { return Options{Root: r.root, Now: r.now} }

// TestRestore: with Dolphin closed, the backup's files go back where they
// came from, the overwritten cards are backed up first, and a Wind
// Waker GCI the backup lacks is reported and left alone.
func TestRestore(t *testing.T) {
	r := newRestoreRig(t)
	extra := r.u.write("GC/USA/Card A/01-GZLE-gczelda2.gci", gci("GZLE", "a second file"))
	other := r.u.write("GC/USA/Card A/01-GALE-smash.gci", gci("GALE", "smash"))

	rep, err := Restore(r.b, &procs{}, r.opts())
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range r.before {
		if got := read(t, p); !bytes.Equal(got, want) {
			t.Errorf("%s = %q after the restore, want %q", p, got, want)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm()&0o200 == 0 {
			t.Errorf("%s is read-only after the restore: Dolphin couldn't save", p)
		}
	}
	if len(rep.Restored) != 2 || !slices.Equal(rep.Left, []string{extra}) {
		t.Errorf("restored %d, left %v", len(rep.Restored), rep.Left)
	}
	if got := read(t, other); !bytes.Contains(got, []byte("smash")) {
		t.Error("another game's save changed")
	}
	// The safety backup holds the cards as they were before the restore,
	// and restoring it undoes the restore.
	if rep.Safety == nil || !strings.Contains(rep.Safety.Label, r.b.ID) {
		t.Fatalf("safety backup %+v", rep.Safety)
	}
	if _, err := Restore(rep.Safety, &procs{}, r.opts()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, r.raw); !bytes.Equal(got, r.afterRaw) {
		t.Errorf("after undoing: %q", got)
	}
	if ents, _ := os.ReadDir(filepath.Dir(r.ww)); len(ents) != 3 {
		t.Errorf("%d files in the GCI folder: a temporary file was left behind?", len(ents))
	}
}

// TestRestoreRefuses: nothing is written while Dolphin runs, when the
// finder can't tell, when a copy is damaged, or when the manifest aims
// outside the card locations.
func TestRestoreRefuses(t *testing.T) {
	cases := []struct {
		name string
		pf   *procs
		mut  func(r *restoreRig)
		want error
	}{
		{"Dolphin running", &procs{answers: [][]uint32{{4242}}}, nil, ErrDolphinRunning},
		{"two Dolphins running", &procs{answers: [][]uint32{{7, 9}}}, nil, ErrDolphinRunning},
		{"can't tell", &procs{err: errors.New("dolphin process memory access is not implemented on plan9")}, nil, ErrDolphinRunning},
		{"a damaged copy", &procs{}, func(r *restoreRig) {
			p := filepath.Join(r.b.Dir, filepath.FromSlash(r.b.Files[0].Stored))
			os.Chmod(p, 0o644)
			os.WriteFile(p, []byte("bit rot"), 0o644)
		}, nil},
		{"a source outside the card locations", &procs{}, func(r *restoreRig) {
			r.b.Files[0].Source = filepath.Join(r.u.root, "Config", "Dolphin.ini")
		}, nil},
		{"a raw card Dolphin.ini no longer names", &procs{}, func(r *restoreRig) {
			r.b.Files[0].Source = filepath.Join(r.root, "MemoryCardA.USA.raw")
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRestoreRig(t)
			if c.mut != nil {
				c.mut(r)
			}
			nBackups := len(mustList(t, r.root))
			rep, err := Restore(r.b, c.pf, r.opts())
			if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("Restore = %+v, %v; want an error matching %v", rep, err, c.want)
			}
			if got := read(t, r.raw); !bytes.Equal(got, r.afterRaw) {
				t.Errorf("the card was written: %q", got)
			}
			if n := len(mustList(t, r.root)); n != nBackups {
				t.Errorf("%d backups after a refused restore, %d before", n, nBackups)
			}
		})
	}
}

// TestRestoreRechecksDolphin: Dolphin started while the safety backup was
// made: no card is written.
func TestRestoreRechecksDolphin(t *testing.T) {
	r := newRestoreRig(t)
	pf := &procs{answers: [][]uint32{nil, {31337}}}
	if _, err := Restore(r.b, pf, r.opts()); !errors.Is(err, ErrDolphinRunning) {
		t.Fatalf("Restore: %v", err)
	}
	if pf.calls != 2 {
		t.Errorf("the finder was asked %d times", pf.calls)
	}
	if got := read(t, r.raw); !bytes.Equal(got, r.afterRaw) {
		t.Errorf("the card was written: %q", got)
	}
}

// TestRestoreKeepsItsSource: the safety backup's rotation never deletes
// the backup being restored, however old.
func TestRestoreKeepsItsSource(t *testing.T) {
	r := newRestoreRig(t)
	opt := r.opts()
	opt.Keep = 2
	for range 2 {
		if _, err := Create([]string{r.u.root}, opt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(r.root, r.b.ID); err == nil {
		t.Fatal("the oldest backup survived rotation; the test can't tell anything")
	}
	// Restore the oldest that is left.
	list := mustList(t, r.root)
	oldest := list[len(list)-1]
	rep, err := Restore(oldest, &procs{}, opt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(r.root, oldest.ID); err != nil {
		t.Errorf("the restored backup was rotated out: %v", err)
	}
	if _, err := Open(r.root, rep.Safety.ID); err != nil {
		t.Errorf("the safety backup is gone: %v", err)
	}
}

// TestRestoreRecreatesFolders: a deleted GCI folder is made again.
func TestRestoreRecreatesFolders(t *testing.T) {
	r := newRestoreRig(t)
	if err := os.RemoveAll(filepath.Dir(r.ww)); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(r.b, &procs{}, r.opts()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, r.ww); !bytes.Equal(got, r.before[r.ww]) {
		t.Errorf("restored %q", got)
	}
}

func mustList(t *testing.T, root string) []*Backup {
	t.Helper()
	l, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
