package cardtag

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// procs is a fake backup.ProcessFinder: pids running, or err.
type procs struct {
	pids []uint32
	err  error
}

func (p *procs) DolphinPIDs() ([]uint32, error) { return p.pids, p.err }

// lines is a log.Reporter that keeps what it heard.
type lines []string

func (l *lines) Log(_ log.Level, msg string) { *l = append(*l, msg) }

func (l *lines) has(t *testing.T, sub string) {
	t.Helper()
	for _, s := range *l {
		if strings.Contains(s, sub) {
			return
		}
	}
	t.Fatalf("no log line holds %q; got:\n%s", sub, strings.Join(*l, "\n"))
}

const day = uint64(40_500_000) * 86400 // a plausible OSGetTime (ticks), non-zero

// image is a played file: random bytes (seed), then the name, clear count
// 0, the save date and the tag set.
func image(seed uint64, name string, date uint64, tag []byte) save.Image {
	r := rand.New(rand.NewPCG(seed, 7))
	var img save.Image
	for i := range img {
		img[i] = byte(r.Uint32())
	}
	p := savefile.Pack(&img)
	img = savefile.Unpack(&p)
	n := img[addr.OffPlayerName : addr.OffPlayerName+bind.PlayerNameSize]
	clear(n)
	copy(n, name)
	img[addr.OffClearCount] = 0
	binary.BigEndian.PutUint64(img[addr.OffDateIPL:], date)
	t := img[addr.OffReserve : addr.OffReserve+bind.TagSize]
	clear(t)
	copy(t, tag)
	return img
}

// rig is a Dolphin user folder with a GCI card in slot A (Dolphin's
// default), a store and its fakes.
type rig struct {
	t       *testing.T
	ud      string
	card    string
	store   *Store
	procs   *procs
	backups string
	log     lines
}

func newRig(t *testing.T, imgs ...save.Image) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{t: t, ud: filepath.Join(root, "Dolphin"), backups: filepath.Join(root, "backups"), procs: &procs{}}
	r.card = filepath.Join(r.ud, "GC", "USA", "Card A", "01-GZLE-gczelda.gci")
	f := savefile.New()
	for n, img := range imgs {
		var err error
		if f, err = f.WithSlot(n, &img); err != nil {
			t.Fatal(err)
		}
	}
	r.writeCard(f)
	r.store = &Store{Dir: filepath.Join(root, "config"), Backups: r.backups, Procs: r.procs,
		Now: func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }}
	return r
}

func (r *rig) writeCard(f *savefile.File) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.card), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.card, f.Encode(), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) readCard() *savefile.File {
	r.t.Helper()
	f, kind, err := savefile.Read(r.card)
	if err != nil || kind != savefile.KindGCI {
		r.t.Fatalf("reading the card: %v (%v)", err, kind)
	}
	return f
}

func (r *rig) cardBytes() []byte {
	r.t.Helper()
	b, err := os.ReadFile(r.card)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

func (r *rig) pending() []Pending {
	r.t.Helper()
	ps, err := r.store.Pending()
	if err != nil {
		r.t.Fatal(err)
	}
	return ps
}

func (r *rig) backupsMade() []*backup.Backup {
	r.t.Helper()
	bs, err := backup.List(r.backups)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.t.Fatal(err)
	}
	return bs
}

// fileOf is the bind.File the game has in memory with slot n's image
// loaded from the card.
func fileOf(n int, img *save.Image) bind.File {
	var f bind.File
	copy(f.Tag[:], img[addr.OffReserve:])
	f.Slot = uint8(n)
	f.ClearCount = img[addr.OffClearCount]
	copy(f.Name[:], img[addr.OffPlayerName:])
	f.DateIPL = binary.BigEndian.Uint64(img[addr.OffDateIPL:])
	return f
}

var (
	world1 = bind.WorldID{0x0a, 1, 2, 3, 4, 5, 6, 7}
	world2 = bind.WorldID{0x0b, 1, 2, 3, 4, 5, 6, 7}
)

// bound is a bind of file f to w.
func bound(f bind.File, w bind.WorldID, nonce byte) bind.Written {
	t := bind.Tag{Layout: bind.LayoutV1, Slot: f.Slot, ClearCount: f.ClearCount, World: w, Nonce: bind.Nonce{nonce, 9},
		WorldSeq: 3, NameHash: bind.NameHash(f.Name[:])}
	return bind.Written{Kind: bind.KindBind, File: f, Bytes: t.Encode(), Tag: t}
}

// cleared is a scrub or clear of file f.
func cleared(f bind.File) bind.Written { return bind.Written{Kind: bind.KindClear, File: f} }

func tagOf(t *testing.T, f *savefile.File, n int) []byte {
	t.Helper()
	s, err := f.Slot(n)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), s.Image[addr.OffReserve:addr.OffReserve+bind.TagSize]...)
}

// The done-when case: a bind recorded while Dolphin runs waits, and once
// Dolphin closed reaches the GCI card: the card verifies, only the tag
// changed, a backup of the card as it was exists, and nothing is left
// pending.
func TestBindReachesCardAfterDolphinCloses(t *testing.T) {
	imgs := []save.Image{image(1, "Link", day, nil), image(2, "Tetra", day+5, nil), image(3, "Aryll", day+9, nil)}
	r := newRig(t, imgs...)
	before := r.cardBytes()
	was := r.readCard()

	w := bound(fileOf(1, &imgs[1]), world1, 1)
	r.procs.pids = []uint32{4242}
	r.store.Record(r.ud, w, &r.log)
	r.log.has(t, "goes onto your memory card (file 2")
	if ps := r.pending(); len(ps) != 1 || ps[0].Card != r.card || ps[0].Slot != 1 || ps[0].Kind != KindBind {
		t.Fatalf("pending = %+v", ps)
	}

	// Dolphin still runs: nothing is written, the tag waits.
	r.store.Flush(&r.log)
	if !bytes.Equal(r.cardBytes(), before) {
		t.Fatal("the card was written while Dolphin ran")
	}
	if len(r.pending()) != 1 || len(r.backupsMade()) != 0 {
		t.Fatal("the pending tag should wait, with no backup taken")
	}
	r.log.has(t, "wait for Dolphin to close")

	// Dolphin closed.
	r.procs.pids = nil
	r.store.Flush(&r.log)
	r.log.has(t, "wrote the co-op tag of world "+world1.String()+" of file 2")
	if ps := r.pending(); len(ps) != 0 {
		t.Fatalf("still pending: %+v", ps)
	}
	now := r.readCard()
	if err := now.Verify(1); err != nil {
		t.Fatalf("the card does not verify: %v", err)
	}
	if err := OnlyTag(was, now, 1, w.Bytes[:]); err != nil {
		t.Fatalf("more than the tag changed: %v", err)
	}
	got, err := bind.Decode(tagOf(t, now, 1))
	if err != nil || got != w.Tag {
		t.Fatalf("the card's tag = %+v, %v; want %+v", got, err, w.Tag)
	}
	// The game would load it bound: V2-V5 against the card's own slot.
	s, _ := now.Slot(1)
	if v := bind.Classify(fileOf(1, &s.Image), bind.Expect{World: world1}); v.State != bind.Bound {
		t.Fatalf("the card's file classifies %s: %s", v.State, v.Why)
	}
	bs := r.backupsMade()
	if len(bs) != 1 || bs[0].Label != BackupLabel {
		t.Fatalf("backups = %+v", bs)
	}
	if !backedUp(bs[0], r.card, before) {
		t.Fatal("the backup does not hold the card as it was")
	}
	if err := bs[0].Verify(); err != nil {
		t.Fatal(err)
	}
	// No temporary file is left beside the card.
	ents, _ := os.ReadDir(filepath.Dir(r.card))
	if len(ents) != 1 {
		t.Fatalf("the card folder holds %d entries", len(ents))
	}
	// The log file has it too, for a quit with no session log.
	b, err := os.ReadFile(filepath.Join(r.store.Dir, "logs", logName))
	if err != nil || !bytes.Contains(b, []byte("only the tag changed")) {
		t.Fatalf("log file: %v\n%s", err, b)
	}

	// A second flush has nothing to do and writes nothing.
	after := r.cardBytes()
	r.store.Flush(&r.log)
	if !bytes.Equal(r.cardBytes(), after) || len(r.backupsMade()) != 1 {
		t.Fatal("a second flush wrote again")
	}
}

// A pending tag survives the app: a new Store on the same folder (the
// next app start) flushes it.
func TestPendingSurvivesRestart(t *testing.T) {
	img := image(4, "Link", day, nil)
	r := newRig(t, img)
	w := bound(fileOf(0, &img), world1, 2)
	r.procs.pids = []uint32{1}
	r.store.Record(r.ud, w, nil)
	next := &Store{Dir: r.store.Dir, Backups: r.backups, Procs: &procs{}}
	next.Flush(nil)
	if got := tagOf(t, r.readCard(), 0); !bytes.Equal(got, w.Bytes[:]) {
		t.Fatalf("tag = % X", got)
	}
}

// The done-when case: a slot that changed after the record is never
// written. Each change drops the pending tag, leaves the card byte for
// byte as it is, and takes no backup.
func TestChangedSlotNeverWritten(t *testing.T) {
	other := bound(fileOf(0, ptr(image(9, "Link", day, nil))), world2, 7)
	for _, c := range []struct {
		name   string
		change func(img *save.Image) // the card's slot 0 after the record
		why    string
	}{
		{"saved in game with another tag", func(img *save.Image) {
			copy(img[addr.OffReserve:], other.Bytes[:])
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], day+100)
		}, "tag on the card changed since"},
		{"copied over by another file", func(img *save.Image) {
			*img = image(5, "Medli", day+50, nil)
		}, `file 1 on the card is now "Medli"`},
		{"second quest", func(img *save.Image) { img[addr.OffClearCount] = 1 }, "clear count 1"},
		{"an older save", func(img *save.Image) {
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], day-1)
		}, "older save"},
		{"no save date", func(img *save.Image) {
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], 0)
		}, "no save date"},
		{"deleted (new game)", func(img *save.Image) { *img = save.Image{} }, "is new game"},
	} {
		t.Run(c.name, func(t *testing.T) {
			img := image(6, "Link", day, nil)
			r := newRig(t, img)
			r.procs.pids = []uint32{1}
			r.store.Record(r.ud, bound(fileOf(0, &img), world1, 3), nil)

			changed := img
			c.change(&changed)
			f, err := r.readCard().WithSlot(0, &changed)
			if err != nil {
				t.Fatal(err)
			}
			r.writeCard(f)
			before := r.cardBytes()

			r.procs.pids = nil
			r.store.Flush(&r.log)
			r.log.has(t, c.why)
			if !bytes.Equal(r.cardBytes(), before) {
				t.Fatal("the changed slot was written")
			}
			if len(r.pending()) != 0 || len(r.backupsMade()) != 0 {
				t.Fatal("the pending tag should be dropped, with no backup taken")
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// An in-game save after the bind carried the tag already: nothing is
// written.
func TestAlreadyOnCard(t *testing.T) {
	img := image(7, "Link", day, nil)
	r := newRig(t, img)
	w := bound(fileOf(0, &img), world1, 4)
	r.procs.pids = []uint32{1}
	r.store.Record(r.ud, w, nil)

	saved := img
	copy(saved[addr.OffReserve:], w.Bytes[:])
	binary.BigEndian.PutUint64(saved[addr.OffDateIPL:], day+60)
	f, _ := r.readCard().WithSlot(0, &saved)
	r.writeCard(f)
	before := r.cardBytes()

	r.procs.pids = nil
	r.store.Flush(&r.log)
	r.log.has(t, "already on the memory card")
	if !bytes.Equal(r.cardBytes(), before) || len(r.backupsMade()) != 0 || len(r.pending()) != 0 {
		t.Fatal("an already-tagged card was written, backed up or kept pending")
	}
}

// A save made in game before the bind (newer than the copy the game
// loaded, still with the old tag) is the same file: it gets the tag.
func TestSavedBeforeBind(t *testing.T) {
	img := image(8, "Link", day, nil)
	r := newRig(t, img)
	w := bound(fileOf(0, &img), world1, 5)
	saved := img
	binary.BigEndian.PutUint64(saved[addr.OffDateIPL:], day+60)
	saved[addr.OffPlayerName+10] = 0x55 // a byte past the name's NUL: more progress, same file
	f, _ := r.readCard().WithSlot(0, &saved)
	r.writeCard(f)

	r.store.Record(r.ud, w, nil)
	r.store.Flush(&r.log)
	got := r.readCard()
	if !bytes.Equal(tagOf(t, got, 0), w.Bytes[:]) {
		t.Fatalf("the tag was not written: %v", r.log)
	}
	s, _ := got.Slot(0)
	want := saved
	copy(want[addr.OffReserve:], w.Bytes[:])
	if s.Image != want {
		t.Fatal("the card's newer save was not kept")
	}
}

// A file bound before it was ever loaded from the card (mDateIPL 0 in
// memory) is written when the card holds it, saved, with the tag it had.
func TestFreshBindWithSavedSlot(t *testing.T) {
	img := image(10, "Link", day, nil)
	r := newRig(t, img)
	f := fileOf(0, &img)
	f.DateIPL = 0
	w := bound(f, world1, 6)
	r.store.Record(r.ud, w, nil)
	r.store.Flush(&r.log)
	if !bytes.Equal(tagOf(t, r.readCard(), 0), w.Bytes[:]) {
		t.Fatalf("not written: %v", r.log)
	}
}

// A file not on the card (a new game never saved) is not recorded.
func TestNotOnCard(t *testing.T) {
	r := newRig(t, image(11, "Link", day, nil))
	img := image(12, "Komali", 0, nil)
	r.store.Record(r.ud, bound(fileOf(1, &img), world1, 1), &r.log)
	r.log.has(t, "not on your memory card yet")
	if len(r.pending()) != 0 {
		t.Fatal("recorded a file that is not on the card")
	}
}

// A raw card image has no writer: the tag is not recorded, and the log
// says to save in game.
func TestRawCardNotRecorded(t *testing.T) {
	img := image(13, "Link", day, nil)
	r := newRig(t, img)
	os.RemoveAll(filepath.Join(r.ud, "GC"))
	writeFile(t, filepath.Join(r.ud, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = 1\n"))
	writeFile(t, filepath.Join(r.ud, "GC", "MemoryCardA.USA.raw"), make([]byte, 16*savefile.BlockSize))
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), &r.log)
	r.log.has(t, "raw card image")
	r.log.has(t, "save in game")
	if len(r.pending()) != 0 {
		t.Fatal("recorded a tag for a raw card")
	}
}

func writeFile(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Dolphin's settings changed after the record (slot A became a raw
// card): the GCI file is no longer the card, and is not written.
func TestDolphinIniChanged(t *testing.T) {
	img := image(14, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	writeFile(t, filepath.Join(r.ud, "Config", "Dolphin.ini"), []byte("[Core]\nSlotA = 1\n"))
	writeFile(t, filepath.Join(r.ud, "GC", "MemoryCardA.USA.raw"), make([]byte, 16*savefile.BlockSize))
	before := r.cardBytes()
	r.store.Flush(&r.log)
	r.log.has(t, "raw card image")
	if !bytes.Equal(r.cardBytes(), before) || len(r.pending()) != 0 {
		t.Fatal("wrote a card Dolphin no longer uses, or kept the tag")
	}
}

// A process list that can't be read counts as a Dolphin running: the
// tag waits.
func TestProcessListErrorWaits(t *testing.T) {
	img := image(15, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	r.procs.err = errors.New("no process list")
	before := r.cardBytes()
	r.store.Flush(&r.log)
	if !bytes.Equal(r.cardBytes(), before) || len(r.pending()) != 1 {
		t.Fatal("wrote while it could not tell whether Dolphin runs")
	}
}

// A Dolphin that starts between the backup and the write stops it.
func TestDolphinStartsMidFlush(t *testing.T) {
	img := image(16, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	r.store.Procs = &seq{answers: [][]uint32{nil, {77}}}
	before := r.cardBytes()
	r.store.Flush(&r.log)
	if !bytes.Equal(r.cardBytes(), before) || len(r.pending()) != 1 {
		t.Fatal("wrote while a Dolphin had started")
	}
}

// seq answers DolphinPIDs from answers in turn, the last one after.
type seq struct {
	answers [][]uint32
	n       int
}

func (s *seq) DolphinPIDs() ([]uint32, error) {
	a := s.answers[min(s.n, len(s.answers)-1)]
	s.n++
	return a, nil
}

// A leaked tag scrubbed (or a tag removed) in game reaches the card as
// zeros.
func TestClearReachesCard(t *testing.T) {
	leaked := bound(fileOf(0, ptr(image(17, "Link", day, nil))), world2, 8)
	img := image(18, "Link", day, leaked.Bytes[:])
	r := newRig(t, img)
	r.store.Record(r.ud, cleared(fileOf(0, &img)), &r.log)
	r.log.has(t, "the cleared tag goes onto your memory card")
	r.store.Flush(&r.log)
	if got := tagOf(t, r.readCard(), 0); !bytes.Equal(got, make([]byte, bind.TagSize)) {
		t.Fatalf("tag = % X", got)
	}
}

// The newest write to a file decides: a bind then a clear leaves one
// pending clear, which still finds the card's original tag acceptable,
// and so does a card an in-game save gave the bind's tag.
func TestNewestWriteWins(t *testing.T) {
	leaked := bound(fileOf(0, ptr(image(19, "Link", day, nil))), world2, 8)
	for _, savedBind := range []bool{false, true} {
		img := image(20, "Link", day, leaked.Bytes[:])
		r := newRig(t, img)
		f := fileOf(0, &img)
		b := bound(f, world1, 9)
		r.store.Record(r.ud, b, nil)
		f.Tag = b.Bytes
		r.store.Record(r.ud, cleared(f), nil)
		ps := r.pending()
		if len(ps) != 1 || ps[0].Kind != KindClear || len(ps[0].Before) != 2 {
			t.Fatalf("pending = %+v", ps)
		}
		if savedBind {
			s := img
			copy(s[addr.OffReserve:], b.Bytes[:])
			binary.BigEndian.PutUint64(s[addr.OffDateIPL:], day+1)
			nf, _ := r.readCard().WithSlot(0, &s)
			r.writeCard(nf)
		}
		r.store.Flush(&r.log)
		if got := tagOf(t, r.readCard(), 0); !bytes.Equal(got, make([]byte, bind.TagSize)) {
			t.Fatalf("saved bind %v: tag = % X\n%v", savedBind, got, r.log)
		}
	}
}

// Pending tags for two files of one card are both written, with one
// backup.
func TestTwoFilesOneBackup(t *testing.T) {
	imgs := []save.Image{image(21, "Link", day, nil), image(22, "Tetra", day, nil)}
	r := newRig(t, imgs...)
	a, b := bound(fileOf(0, &imgs[0]), world1, 1), bound(fileOf(1, &imgs[1]), world1, 2)
	r.store.Record(r.ud, a, nil)
	r.store.Record(r.ud, b, nil)
	r.store.Flush(&r.log)
	got := r.readCard()
	if !bytes.Equal(tagOf(t, got, 0), a.Bytes[:]) || !bytes.Equal(tagOf(t, got, 1), b.Bytes[:]) {
		t.Fatalf("tags not both written: %v", r.log)
	}
	if n := len(r.backupsMade()); n != 1 {
		t.Fatalf("%d backups, want 1", n)
	}
}

// A write that reads back wrong puts the card's old bytes back.
func TestBadReadBackPutsCardBack(t *testing.T) {
	img := image(23, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	before := r.cardBytes()
	calls := 0
	replaceCard = func(path string, b []byte, perm fs.FileMode) error {
		calls++
		if calls == 1 {
			b = append([]byte(nil), b...)
			b[savefile.HeaderSize+savefile.BlockSize+100] ^= 0xFF // a bad byte in the save block
		}
		return backup.Replace(path, b, perm)
	}
	t.Cleanup(func() { replaceCard = backup.Replace })
	r.store.Flush(&r.log)
	r.log.has(t, "did not read back")
	if calls != 2 || !bytes.Equal(r.cardBytes(), before) {
		t.Fatalf("calls %d; the card was not put back", calls)
	}
	if len(r.pending()) != 0 {
		t.Fatal("kept a tag whose write went wrong")
	}
}

// A failed file write leaves the card as it was and the tag pending.
func TestWriteFailureWaits(t *testing.T) {
	img := image(24, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	before := r.cardBytes()
	replaceCard = func(string, []byte, fs.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { replaceCard = backup.Replace })
	r.store.Flush(&r.log)
	if !bytes.Equal(r.cardBytes(), before) || len(r.pending()) != 1 {
		t.Fatal("a failed write should leave the card and keep the tag")
	}
}

// A backup that can't be taken stops the write; the tag waits.
func TestBackupFailureWaits(t *testing.T) {
	img := image(25, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	writeFile(t, r.backups, []byte("a file where the backup folder goes"))
	before := r.cardBytes()
	r.store.Flush(&r.log)
	r.log.has(t, "could not be backed up first")
	if !bytes.Equal(r.cardBytes(), before) || len(r.pending()) != 1 {
		t.Fatal("wrote without a backup")
	}
}

// Two Wind Waker card files holding the same file: which one Dolphin
// uses is unclear, so nothing is recorded.
func TestAmbiguousCardFiles(t *testing.T) {
	img := image(26, "Link", day, nil)
	r := newRig(t, img)
	b := r.cardBytes()
	writeFile(t, filepath.Join(filepath.Dir(r.card), "01-GZLE-gczelda-copy.gci"), b)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), &r.log)
	r.log.has(t, "more than one Wind Waker card file")
	if len(r.pending()) != 0 {
		t.Fatal("recorded an ambiguous card")
	}
}

// OnlyTag refuses a file with more changed than the tag.
func TestOnlyTag(t *testing.T) {
	img := image(27, "Link", day, nil)
	f, _ := savefile.New().WithSlot(0, &img)
	tag := bound(fileOf(0, &img), world1, 1).Bytes
	good := img
	copy(good[addr.OffReserve:], tag[:])
	nf, _ := f.WithSlot(0, &good)
	if err := OnlyTag(f, nf, 0, tag[:]); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad[addr.OffReserve+bind.TagSize] ^= 1 // past the tag
	nf, _ = f.WithSlot(0, &bad)
	if err := OnlyTag(f, nf, 0, tag[:]); err == nil {
		t.Fatal("OnlyTag passed a change past the tag")
	}
	other := image(28, "Tetra", day, nil)
	nf, _ = f.WithSlot(1, &other)
	nf, _ = nf.WithSlot(0, &good)
	if err := OnlyTag(f, nf, 0, tag[:]); err == nil {
		t.Fatal("OnlyTag passed a change to another slot")
	}
}

// A damaged pending-tags file is moved aside and reads as none.
func TestDamagedStateFile(t *testing.T) {
	r := newRig(t, image(29, "Link", day, nil))
	writeFile(t, filepath.Join(r.store.Dir, stateName), []byte("{not json"))
	if _, err := r.store.Pending(); err == nil {
		t.Fatal("no error for a damaged file")
	}
	if ps := r.pending(); len(ps) != 0 {
		t.Fatalf("pending = %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(r.store.Dir, stateName+".bad")); err != nil {
		t.Fatal("the damaged file was not kept aside")
	}
}
