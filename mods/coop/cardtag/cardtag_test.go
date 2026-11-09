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
		}, "changed since"},
		{"saved in game again, still untagged", func(img *save.Image) {
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], day+100)
			img[addr.OffPlayerName+12] ^= 0x5A // more progress
		}, "changed since"},
		{"copied over by another file", func(img *save.Image) {
			*img = image(5, "Medli", day+50, nil)
		}, "changed since"},
		// "Link" is the default name: another untagged "Link" (a copy, or a
		// new game saved in the slot) must never get the bind.
		{"another file of the same name, newer", func(img *save.Image) {
			*img = image(30, "Link", day+50, nil)
		}, "changed since"},
		{"another file of the same name and date", func(img *save.Image) {
			*img = image(31, "Link", day, nil)
		}, "changed since"},
		{"second quest", func(img *save.Image) { img[addr.OffClearCount] = 1 }, "changed since"},
		{"an older save", func(img *save.Image) {
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], day-1)
		}, "changed since"},
		{"no save date", func(img *save.Image) {
			binary.BigEndian.PutUint64(img[addr.OffDateIPL:], 0)
		}, "changed since"},
		{"deleted (new game)", func(img *save.Image) { *img = save.Image{} }, "changed since"},
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

// A save made in game before the bind (a card copy newer than the one
// the game loaded) can't be told from a save state of another playthrough
// of a file of the same name, so it is not recorded: the in-game save
// after the bind carries the tag.
func TestSavedBeforeBindNotRecorded(t *testing.T) {
	img := image(8, "Link", day, nil)
	r := newRig(t, img)
	w := bound(fileOf(0, &img), world1, 5)
	saved := img
	binary.BigEndian.PutUint64(saved[addr.OffDateIPL:], day+60)
	f, _ := r.readCard().WithSlot(0, &saved)
	r.writeCard(f)
	r.store.Record(r.ud, w, &r.log)
	r.log.has(t, "not on your memory card")
	if len(r.pending()) != 0 {
		t.Fatal("recorded onto a card copy the game did not load")
	}
}

// A save state of another playthrough of a "Link" in file 1 (loaded from
// an older card copy) never tags the card's own, unrelated file.
func TestSaveStateOtherPlaythrough(t *testing.T) {
	card := image(77, "Link", day+500, nil)
	r := newRig(t, card)
	ram := image(78, "Link", day, nil)
	r.store.Record(r.ud, bound(fileOf(0, &ram), world1, 5), nil)
	before := r.cardBytes()
	r.store.Flush(&r.log)
	if len(r.pending()) != 0 || !bytes.Equal(r.cardBytes(), before) {
		t.Fatal("tagged another playthrough's card file")
	}
}

// A file the game did not load from the card (mDateIPL 0 in memory: a
// new game) is not recorded, even when the card holds a
// file of its name in its slot: which copy it is can't be told.
func TestNeverLoadedNotRecorded(t *testing.T) {
	img := image(10, "Link", day, nil)
	r := newRig(t, img)
	f := fileOf(0, &img)
	f.DateIPL = 0
	r.store.Record(r.ud, bound(f, world1, 6), &r.log)
	r.log.has(t, "not on your memory card")
	if len(r.pending()) != 0 {
		t.Fatal("recorded a file the game never loaded from the card")
	}
}

// A card copy older than the one the game loaded is not the file.
func TestOlderCardCopyNotRecorded(t *testing.T) {
	img := image(32, "Link", day, nil)
	r := newRig(t, img)
	f := fileOf(0, &img)
	f.DateIPL = day + 1
	r.store.Record(r.ud, bound(f, world1, 6), &r.log)
	if len(r.pending()) != 0 {
		t.Fatal("recorded onto an older card copy")
	}
}

// A file not on the card (a new game never saved) is not recorded.
func TestNotOnCard(t *testing.T) {
	r := newRig(t, image(11, "Link", day, nil))
	img := image(12, "Komali", 0, nil)
	img2 := img
	binary.BigEndian.PutUint64(img2[addr.OffDateIPL:], day)
	r.store.Record(r.ud, bound(fileOf(1, &img2), world1, 1), &r.log)
	r.log.has(t, "not on your memory card")
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
// pending clear, which zeroes the card's (leaked) tag. After an in-game
// save between the two, the card copy is no longer the one the game
// loaded: the clear is not recorded, the bind's pending tag goes, and the
// card keeps what the save wrote until the next in-game save.
func TestNewestWriteWins(t *testing.T) {
	leaked := bound(fileOf(0, ptr(image(19, "Link", day, nil))), world2, 8)
	for _, savedBind := range []bool{false, true} {
		img := image(20, "Link", day, leaked.Bytes[:])
		r := newRig(t, img)
		f := fileOf(0, &img)
		b := bound(f, world1, 9)
		r.store.Record(r.ud, b, nil)
		if savedBind {
			s := img
			copy(s[addr.OffReserve:], b.Bytes[:])
			binary.BigEndian.PutUint64(s[addr.OffDateIPL:], day+1)
			nf, _ := r.readCard().WithSlot(0, &s)
			r.writeCard(nf)
		}
		f.Tag = b.Bytes
		r.store.Record(r.ud, cleared(f), &r.log)
		ps := r.pending()
		want := make([]byte, bind.TagSize)
		if savedBind {
			if len(ps) != 0 {
				t.Fatalf("saved bind: pending = %+v", ps)
			}
			r.log.has(t, "not on your memory card")
			want = b.Bytes[:]
		} else if len(ps) != 1 || ps[0].Kind != KindClear {
			t.Fatalf("pending = %+v", ps)
		}
		r.store.Flush(&r.log)
		if got := tagOf(t, r.readCard(), 0); !bytes.Equal(got, want) {
			t.Fatalf("saved bind %v: tag = % X\n%v", savedBind, got, r.log)
		}
	}
}

// A write that fails every time is dropped after maxAttempts flushes, so
// its backups (one per try) can't rotate the older ones away.
func TestAttemptsCap(t *testing.T) {
	img := image(33, "Link", day, nil)
	r := newRig(t, img)
	r.store.Record(r.ud, bound(fileOf(0, &img), world1, 1), nil)
	replaceCard = func(string, []byte, fs.FileMode) error { return errors.New("read-only card") }
	t.Cleanup(func() { replaceCard = backup.Replace })
	for i := 1; i <= maxAttempts+2; i++ {
		r.store.Flush(&r.log)
		if want := max(0, min(1, maxAttempts-i)); len(r.pending()) != want {
			t.Fatalf("after flush %d: %d pending, want %d", i, len(r.pending()), want)
		}
	}
	r.log.has(t, "dropped after")
	if n := len(r.backupsMade()); n != maxAttempts {
		t.Fatalf("%d backups, want %d", n, maxAttempts)
	}
}

// A relative user folder (a typed setting, USER_DIR_1=./u) is made
// absolute, so it matches the backup's paths and the tag is written.
func TestRelativeUserDir(t *testing.T) {
	img := image(34, "Link", day, nil)
	r := newRig(t, img)
	t.Chdir(filepath.Dir(r.ud))
	w := bound(fileOf(0, &img), world1, 1)
	r.store.Record(filepath.Base(r.ud), w, nil)
	r.store.Flush(&r.log)
	if !bytes.Equal(tagOf(t, r.readCard(), 0), w.Bytes[:]) {
		t.Fatalf("not written: %v", r.log)
	}
}

// The lock between app processes: while another holds it, Record gives
// up (and says so), and the file's older pending tag is dropped at the
// next Flush rather than written over the newer, unrecorded change.
// Forget drops a file's pending tags likewise.
func TestLockAndForget(t *testing.T) {
	img := image(35, "Link", day, nil)
	r := newRig(t, img)
	f := fileOf(0, &img)
	b := bound(f, world1, 1)
	r.store.Record(r.ud, b, nil)
	if len(r.pending()) != 1 {
		t.Fatal("not recorded")
	}

	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = 5 * time.Second })
	unlock, busy, err := tryLock(filepath.Join(r.store.Dir, lockName))
	if err != nil || busy {
		t.Fatalf("tryLock: busy %v, %v", busy, err)
	}
	held := &Store{Dir: r.store.Dir, Backups: r.backups, Procs: r.procs}
	f.Tag = b.Bytes
	held.Record(r.ud, cleared(f), &r.log) // another process holds the lock
	r.log.has(t, "could not keep the pending co-op tag")
	unlock()
	held.Flush(&r.log)
	if len(r.pending()) != 0 || !bytes.Equal(tagOf(t, r.readCard(), 0), make([]byte, bind.TagSize)) {
		t.Fatal("the bind superseded by an unrecorded clear reached the card")
	}

	r.store.Record(r.ud, b, nil)
	r.store.Forget(cleared(f), &r.log)
	if len(r.pending()) != 0 {
		t.Fatal("Forget kept the file's pending tag")
	}
	r.log.has(t, "dropped the earlier pending co-op tag")

	// A write queued for forgetting (the lock was busy) is overruled by a
	// newer write recorded once the lock is free.
	unlock, _, _ = tryLock(filepath.Join(r.store.Dir, lockName))
	held.Record(r.ud, b, nil)
	unlock()
	r.store.Record(r.ud, b, nil) // another Store: its own forget list
	held.Record(r.ud, b, nil)
	held.Flush(&r.log)
	if !bytes.Equal(tagOf(t, r.readCard(), 0), b.Bytes[:]) {
		t.Fatalf("a newer recorded write was forgotten: %v", r.log)
	}
}
