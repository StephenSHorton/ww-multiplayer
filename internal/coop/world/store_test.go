package world

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// fakeClock runs a Store's timers when the test says so.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c    *fakeClock
	when time.Time
	f    func()
	done bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: testCreated} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, when: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.done
	t.done = true
	return was
}

// Advance moves the clock by d, running each timer that falls due on the
// way, in order (without the clock's lock, so a timer can set another).
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.done && !t.when.After(end) && (next == nil || t.when.Before(next.when)) {
				next = t
			}
		}
		if next == nil {
			c.now = end
			c.mu.Unlock()
			return
		}
		c.now, next.done = next.when, true
		c.mu.Unlock()
		next.f()
		c.mu.Lock()
	}
}

// pending is how many timers are set and not yet run.
func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.done {
			n++
		}
	}
	return n
}

// abandon is a crash: the store stops without flushing, its journal
// closed (so the test can look at it, and Windows can remove it).
func (s *Store) abandon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.journal != nil {
		s.journal.Close()
		s.journal = nil
	}
}

func testOptions(c *fakeClock) Options { return Options{clock: c} }

// fileSeq reads the seq of the world file at path, or -1.
func fileSeq(t *testing.T, path string) int64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	w, err := Decode(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return int64(w.Seq())
}

func gainAt(off int, v byte) []Gain { return []Gain{{Off: uint16(off), Val: v}} }

// TestStoreRoundTrip: a world founded, changed and stopped opens again as
// it was; a directory that holds a world can't be founded over.
func TestStoreRoundTrip(t *testing.T) {
	root, clk := t.TempDir(), newFakeClock()
	s, err := Create(root, sampleWorld(t), testOptions(clk))
	if err != nil {
		t.Fatal(err)
	}
	s.SetPolicy(FullPolicy())
	if _, err := s.ApplyGains(1, gainAt(addr.OffTact, 0x03)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFreshBind(ID{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tombstone(addr.OffTact, 0x80); err != nil {
		t.Fatal(err)
	}
	want := s.World()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	if _, err := s.ApplyGains(1, gainAt(addr.OffTact, 0x04)); !errors.Is(err, ErrStopped) {
		t.Errorf("change after Stop: %v", err)
	}

	got, rep, err := Load(root, want.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !worldsEqual(got, want) || rep.From != FileName || rep.Replayed != 0 {
		t.Errorf("loaded %+v (seq %d, want %d)", rep, got.Seq(), want.Seq())
	}
	if _, err := Create(root, sampleWorld(t), testOptions(clk)); !errors.Is(err, fs.ErrExist) {
		t.Errorf("founding over a world: %v", err)
	}
	if _, _, err := Load(root, ID{1}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a world never founded: %v", err)
	}
	if _, err := Create(root, New(ID{}, time.Time{}), testOptions(clk)); err == nil {
		t.Error("founded a world without an ID")
	}
}

// TestStoreFlushDebounce: the world is written FlushDelay after the first
// change, once for every change in that window, and on Stop.
func TestStoreFlushDebounce(t *testing.T) {
	root, clk := t.TempDir(), newFakeClock()
	s, err := Create(root, newTestWorld(), testOptions(clk))
	if err != nil {
		t.Fatal(err)
	}
	s.SetPolicy(FullPolicy())
	main := filepath.Join(s.Dir(), FileName)
	writes := countMainWrites(t, main)

	s.ApplyGains(1, gainAt(offItem, 1))
	clk.Advance(DefaultFlushDelay / 2)
	s.ApplyGains(2, gainAt(offItem, 2))
	s.ApplyGains(2, gainAt(offItem, 2)) // no change: no journal line, no flush of its own
	if fileSeq(t, main) != 0 || *writes != 0 {
		t.Fatal("written before the delay")
	}
	clk.Advance(DefaultFlushDelay/2 - time.Millisecond)
	if *writes != 0 {
		t.Fatal("written before the delay")
	}
	clk.Advance(time.Millisecond)
	if fileSeq(t, main) != 2 || *writes != 1 {
		t.Fatalf("after the delay: file seq %d, %d writes", fileSeq(t, main), *writes)
	}
	clk.Advance(time.Hour - DefaultFlushDelay - time.Millisecond) // nothing changed: nothing written
	if *writes != 1 {
		t.Errorf("%d writes with nothing changed", *writes)
	}

	s.ApplyGains(1, gainAt(offItem, 4))
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if fileSeq(t, main) != 3 || *writes != 2 {
		t.Errorf("Stop: file seq %d, %d writes", fileSeq(t, main), *writes)
	}
	if n := clk.pending(); n != 0 {
		t.Errorf("%d timers left after Stop", n)
	}
}

// countMainWrites counts the renames onto main from now until the test
// ends.
func countMainWrites(t *testing.T, main string) *int {
	n := new(int)
	orig := replaceFile
	t.Cleanup(func() { replaceFile = orig })
	replaceFile = func(from, to string) error {
		if to == main {
			*n++
		}
		return orig(from, to)
	}
	return n
}

// TestStoreBackups: a session start and every hour of a running session
// add a backup; five are kept, newest first.
func TestStoreBackups(t *testing.T) {
	root, clk := t.TempDir(), newFakeClock()
	s, err := Create(root, newTestWorld(), testOptions(clk))
	if err != nil {
		t.Fatal(err)
	}
	s.SetPolicy(FullPolicy())
	dir := s.Dir()
	seqs := func() []int64 {
		var out []int64
		for i := 1; i <= BackupCount+1; i++ {
			out = append(out, fileSeq(t, filepath.Join(dir, backupName(i))))
		}
		return out
	}
	if got := seqs(); !slices.Equal(got, []int64{0, -1, -1, -1, -1, -1}) {
		t.Fatalf("after founding: %v", got)
	}
	for h := 1; h <= 6; h++ {
		s.ApplyGains(1, gainAt(addr.OffGetItem+h, 1))
		clk.Advance(time.Hour)
	}
	if got := seqs(); !slices.Equal(got, []int64{6, 5, 4, 3, 2, -1}) {
		t.Errorf("after six hours: %v", got)
	}
	s.Stop()
	// The next session starts with a backup of its own.
	s, _, err = Open(root, testID, testOptions(clk))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if got := seqs(); !slices.Equal(got, []int64{6, 6, 5, 4, 3, -1}) {
		t.Errorf("after a session start: %v", got)
	}
}

// crashRig is a world with history in every file: founded at seq 0, seq 1
// flushed and backed up (world.wwco.1), seq 2 flushed, seq 3 only in the
// journal, then a crash.
type crashRig struct {
	root, dir string
	clk       *fakeClock
	images    [4]Image // the image at each seq
}

func newCrashRig(t *testing.T) *crashRig {
	t.Helper()
	r := &crashRig{root: t.TempDir(), clk: newFakeClock()}
	s, err := Create(r.root, newTestWorld(), testOptions(r.clk))
	if err != nil {
		t.Fatal(err)
	}
	r.dir = s.Dir()
	s.SetPolicy(FullPolicy())
	step := func(i int, off int, v byte) {
		if res, err := s.ApplyGains(Player(i), gainAt(off, v)); err != nil || res.Seq != uint64(i) {
			t.Fatalf("step %d: %+v %v", i, res, err)
		}
		r.images[i] = s.Image()
	}
	step(1, addr.OffGetItem, 0x01)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(); err != nil {
		t.Fatal(err)
	}
	step(2, addr.OffArrowMax, 30)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	step(3, addr.OffMemBit(3)+addr.MemBitOffTbox, 0x80)
	s.abandon()
	return r
}

func (r *crashRig) path(name string) string { return filepath.Join(r.dir, name) }

func (r *crashRig) read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(r.path(name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (r *crashRig) write(t *testing.T, name string, b []byte) {
	t.Helper()
	if err := os.WriteFile(r.path(name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCrashRecovery is the world file's crash injection: whatever a crash
// or a bad disk leaves, Open recovers the last good world (or world.wwco.1)
// plus what the journal holds, never loads damage, never replaces a newer
// version's file, and leaves world.wwco whole again.
func TestCrashRecovery(t *testing.T) {
	for _, c := range []struct {
		name    string
		crash   func(t *testing.T, r *crashRig)
		from    string
		seq     uint64
		bad     bool // world.wwco kept as world.wwco.bad
		stale   int
		badLine int
		wantErr error
	}{
		{name: "kill before the flush", crash: func(*testing.T, *crashRig) {}, from: FileName, seq: 3},
		{name: "kill between write and rename", crash: func(t *testing.T, r *crashRig) {
			w, _, err := Load(r.root, testID)
			if err != nil {
				t.Fatal(err)
			}
			r.write(t, FileName+".1234.tmp", Encode(w))
		}, from: FileName, seq: 3, stale: 1},
		{name: "truncated tmp file", crash: func(t *testing.T, r *crashRig) {
			w, _, _ := Load(r.root, testID)
			b := Encode(w)
			r.write(t, FileName+".99.tmp", b[:len(b)/3])
		}, from: FileName, seq: 3, stale: 1},
		{name: "renamed but cut off", crash: func(t *testing.T, r *crashRig) {
			b := r.read(t, FileName)
			r.write(t, FileName, b[:len(b)-100])
		}, from: backupName(1), seq: 3, bad: true},
		{name: "renamed but zeroed", crash: func(t *testing.T, r *crashRig) {
			r.write(t, FileName, make([]byte, len(r.read(t, FileName))))
		}, from: backupName(1), seq: 3, bad: true},
		{name: "CRC damage", crash: func(t *testing.T, r *crashRig) {
			b := r.read(t, FileName)
			b[headerLen+secHeaderLen+addr.OffGetItem] ^= 0x10
			r.write(t, FileName, b)
		}, from: backupName(1), seq: 3, bad: true},
		{name: "CRC damage and no journal", crash: func(t *testing.T, r *crashRig) {
			b := r.read(t, FileName)
			b[0x30] ^= 0xFF
			r.write(t, FileName, b)
			os.Remove(r.path(JournalName))
		}, from: backupName(1), seq: 1, bad: true},
		{name: "missing file", crash: func(t *testing.T, r *crashRig) {
			os.Remove(r.path(FileName))
		}, from: backupName(1), seq: 3},
		{name: "another world's file", crash: func(t *testing.T, r *crashRig) {
			r.write(t, FileName, Encode(New(ID{0xEE}, time.Time{})))
		}, from: backupName(1), seq: 3, bad: true},
		{name: "damaged journal line", crash: func(t *testing.T, r *crashRig) {
			j := r.read(t, JournalName)
			j[len(j)-3] ^= 1 // the last record's CRC
			r.write(t, JournalName, j)
		}, from: FileName, seq: 2, badLine: 1},
		{name: "journal cut off mid-line", crash: func(t *testing.T, r *crashRig) {
			j := r.read(t, JournalName)
			r.write(t, JournalName, j[:len(j)-5])
		}, from: FileName, seq: 2, badLine: 1},
		{name: "unknown version", crash: func(t *testing.T, r *crashRig) {
			b := r.read(t, FileName)
			binary.BigEndian.PutUint16(b[8:], FileVersion+1)
			r.write(t, FileName, reseal(b))
		}, wantErr: ErrVersion},
		{name: "everything damaged", crash: func(t *testing.T, r *crashRig) {
			for i := 0; i <= BackupCount; i++ {
				if _, err := os.Stat(r.path(backupName(i))); err == nil {
					r.write(t, backupName(i), []byte("not a world"))
				}
			}
		}, wantErr: ErrCorrupt},
		{name: "nothing left", crash: func(t *testing.T, r *crashRig) {
			os.Remove(r.path(FileName))
			os.Remove(r.path(backupName(1)))
			os.Remove(r.path(backupName(2)))
		}, wantErr: fs.ErrNotExist},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newCrashRig(t)
			c.crash(t, r)
			before := snapshotDir(t, r.dir)

			s, rep, err := Open(r.root, testID, testOptions(r.clk))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Open: %v, want %v", err, c.wantErr)
				}
				if after := snapshotDir(t, r.dir); !mapsEqual(before, after) {
					t.Errorf("a failed Open changed the directory:\nbefore %v\nafter  %v", keys(before), keys(after))
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v (%+v)", err, rep)
			}
			defer s.Stop()
			if rep.From != c.from || s.Seq() != c.seq || rep.StaleTmp != c.stale || rep.BadLines != c.badLine {
				t.Fatalf("from %s seq %d stale %d bad lines %d, want %s %d %d %d (skipped %v)",
					rep.From, s.Seq(), rep.StaleTmp, rep.BadLines, c.from, c.seq, c.stale, c.badLine, rep.Skipped)
			}
			if img := s.Image(); img != r.images[c.seq] {
				t.Errorf("image is not the seq-%d one", c.seq)
			}
			_, err = os.Stat(r.path(BadName))
			if c.bad != (err == nil) {
				t.Errorf("world.wwco.bad exists: %v, want %v", err == nil, c.bad)
			}
			// world.wwco is whole and current again, and no tmp is left.
			if got := fileSeq(t, r.path(FileName)); got != int64(c.seq) {
				t.Errorf("world.wwco seq %d after Open, want %d", got, c.seq)
			}
			for name := range snapshotDir(t, r.dir) {
				if strings.HasSuffix(name, ".tmp") {
					t.Errorf("%s left", name)
				}
			}
			// The journal takes new records on lines of their own, even
			// after a cut-off line.
			if _, err := s.AddFreshBind(ID{5}); err != nil {
				t.Fatal(err)
			}
			s.Stop()
			w, rep2, err := Load(r.root, testID)
			if err != nil || !w.IsFreshBind(ID{5}) || rep2.BadLines != c.badLine {
				t.Errorf("reload: %v, %+v", err, rep2)
			}
		})
	}
}

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		m[e.Name()] = string(b)
	}
	return m
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// TestStoreFlushFails: a write that fails leaves world.wwco as it was and
// no temporary file; a background flush logs, keeps the change and retries
// with a growing wait until it succeeds.
func TestStoreFlushFails(t *testing.T) {
	root, clk := t.TempDir(), newFakeClock()
	var logs []string
	opts := testOptions(clk)
	opts.Logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	s, err := Create(root, newTestWorld(), opts)
	if err != nil {
		t.Fatal(err)
	}
	s.SetPolicy(FullPolicy())
	main := filepath.Join(s.Dir(), FileName)
	before, _ := os.ReadFile(main)

	orig := replaceFile
	t.Cleanup(func() { replaceFile = orig })
	held := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	replaceFile = func(string, string) error { return held }

	s.ApplyGains(1, gainAt(addr.OffGetItem, 1))
	if err := s.Flush(); !errors.Is(err, held) {
		t.Fatalf("Flush: %v", err)
	}
	clk.Advance(DefaultFlushDelay)     // fails, retries in 1 s
	clk.Advance(DefaultFlushDelay)     // fails, retries in 2 s
	clk.Advance(2 * DefaultFlushDelay) // fails, retries in 4 s
	if len(logs) != 3 || !errors.Is(s.Err(), held) {
		t.Fatalf("logs %q, Err %v", logs, s.Err())
	}
	if after, _ := os.ReadFile(main); !bytes.Equal(after, before) {
		t.Error("a failed write changed world.wwco")
	}
	if ents, _ := os.ReadDir(s.Dir()); slices.ContainsFunc(ents, func(e os.DirEntry) bool { return strings.HasSuffix(e.Name(), ".tmp") }) {
		t.Error("a failed write left its temporary file")
	}
	replaceFile = orig
	clk.Advance(4 * DefaultFlushDelay)
	if fileSeq(t, main) != 1 || len(logs) != 3 {
		t.Errorf("after the disk came back: file seq %d, %d logs", fileSeq(t, main), len(logs))
	}
	s.Stop()
}

// TestJournalRotation: a session start moves a large journal aside, and
// Load still replays both.
func TestJournalRotation(t *testing.T) {
	r := newCrashRig(t)
	j := r.read(t, JournalName)
	// Pad the journal past the rotation size with lines Load skips.
	pad := bytes.Repeat([]byte("#\n"), journalRotateSize/2)
	r.write(t, JournalName, append(pad, j...))
	s, rep, err := Open(r.root, testID, testOptions(r.clk))
	if err != nil {
		t.Fatal(err)
	}
	if s.Seq() != 3 || rep.Replayed != 1 {
		t.Fatalf("seq %d, replayed %d", s.Seq(), rep.Replayed)
	}
	if fi, err := os.Stat(r.path(JournalName)); err != nil || fi.Size() != 0 {
		t.Errorf("journal not rotated: %v", err)
	}
	if _, err := os.Stat(r.path(JournalName + ".1")); err != nil {
		t.Error(err)
	}
	s.ApplyGains(1, gainAt(addr.OffSymbol, 1))
	s.abandon()
	w, rep, err := Load(r.root, testID)
	if err != nil || w.Seq() != 4 || rep.Replayed != 1 {
		t.Errorf("after rotation: %v, seq %d, %+v", err, w.Seq(), rep)
	}
}

// fakeRename fails its first n calls with err, then succeeds.
type fakeRename struct {
	n, calls int
	err      error
}

func (f *fakeRename) rename(oldpath, newpath string) error {
	f.calls++
	if f.calls <= f.n {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: f.err}
	}
	return nil
}

// TestRetryRename checks the Windows rename retries: a sharing error is
// ridden out, retried for about 2 s, and any other error is not.
func TestRetryRename(t *testing.T) {
	for _, c := range []struct {
		name  string
		fails int
		err   syscall.Errno
		calls int
		ok    bool
	}{
		{"a sharing violation passes", 3, winErrorSharingViolation, 4, true},
		{"access stays denied", 1 << 30, winErrorAccessDenied, -1, false},
		{"another error", 1 << 30, syscall.Errno(2), 1, false}, // ERROR_FILE_NOT_FOUND / ENOENT
	} {
		t.Run(c.name, func(t *testing.T) {
			fr := &fakeRename{n: c.fails, err: c.err}
			var sleeps []time.Duration
			err := retryRename(fr.rename, "a.tmp", "a", isWindowsSharingError, func(d time.Duration) { sleeps = append(sleeps, d) })
			if c.ok != (err == nil) || (!c.ok && !errors.Is(err, c.err)) {
				t.Fatalf("err = %v", err)
			}
			if (c.calls >= 0 && fr.calls != c.calls) || fr.calls != len(sleeps)+1 {
				t.Errorf("%d calls, %d sleeps", fr.calls, len(sleeps))
			}
			var total time.Duration
			for i, d := range sleeps {
				if want := min(renameRetryFirst<<i, renameRetryMaxStep); d != want {
					t.Errorf("sleep %d = %v, want %v", i, d, want)
				}
				total += d
			}
			if c.calls < 0 && (total > renameRetryBudget || total+renameRetryMaxStep <= renameRetryBudget) {
				t.Errorf("slept %v in all, want just under %v", total, renameRetryBudget)
			}
		})
	}
	err := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	if got, want := renameErrIsTransient(err), runtime.GOOS == "windows"; got != want {
		t.Errorf("renameErrIsTransient(errno 32) on %s = %v, want %v", runtime.GOOS, got, want)
	}
	if isWindowsSharingError(errors.New("access is denied")) || isWindowsSharingError(nil) {
		t.Error("isWindowsSharingError took a non-errno")
	}
}

func TestDir(t *testing.T) {
	d, err := Dir()
	if err != nil {
		t.Skip(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(d), worldsSubdir) {
		t.Errorf("Dir() = %s", d)
	}
	if p := Path("/r", testID); filepath.ToSlash(p) != "/r/"+testID.String() {
		t.Errorf("Path = %s", p)
	}
}

// TestStoreConcurrent changes a store from several goroutines while its
// real timers flush and back it up (run under -race), then checks that
// what Stop leaves loads as the store's last state.
func TestStoreConcurrent(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, newTestWorld(), Options{FlushDelay: time.Millisecond, BackupEvery: 3 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s.SetPolicy(FullPolicy())
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				off := addr.OffOcean + (g*150+i)%addr.OceanSize
				switch i % 3 {
				case 0:
					s.ApplyGains(Player(g+1), gainAt(off, byte(1)<<(i%8)))
				case 1:
					var img Image
					img[off] = byte(i)
					s.Merge(Player(g+1), &img)
				default:
					_ = s.Image()
					_ = s.Seq()
					_ = s.World()
				}
				if i%50 == 0 {
					time.Sleep(time.Millisecond)
				}
			}
		}(g)
	}
	wg.Wait()
	want := s.World()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	got, rep, err := Load(root, testID)
	if err != nil || !worldsEqual(got, want) || rep.FellBack() {
		t.Fatalf("after a busy session: %v, %+v", err, rep)
	}
}
